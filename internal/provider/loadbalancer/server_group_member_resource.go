package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                = &ServerGroupMemberResource{}
	_ resource.ResourceWithImportState = &ServerGroupMemberResource{}
)

const (
	serverGroupMemberReadyTimeout      = 30 * time.Minute
	serverGroupMemberReadyPollInterval = 5 * time.Second
)

type ServerGroupMemberResource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

func NewServerGroupMemberResource() resource.Resource {
	return &ServerGroupMemberResource{}
}

func (r *ServerGroupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_member"
}

func (r *ServerGroupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer Server Group Member.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Server Group Member ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"server_group": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Server Group that owns this member.",
				Attributes:  reference.Attributes(reference.ID, "Server Group ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Server Group Member.",
			},
			"address": schema.StringAttribute{
				Required:    true,
				Description: "IP address of the backend member.",
				PlanModifiers: []planmodifier.String{
					replace.String(compare.SameIPAddressString),
				},
			},
			"protocol_port": schema.Int64Attribute{
				Required:    true,
				Description: "Port on which the member listens for traffic (1-65535).",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
				Validators: []validator.Int64{
					memberProtocolPortValidator{},
				},
			},
			"weight": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(1),
				Description: "Weight of the member in the server group (default 1).",
			},
			"backup": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether the member is a backup member (default false).",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the Server Group Member.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the Server Group Member.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group Member was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group Member was last updated (RFC3339).",
			},
		},
	}
}

func (r *ServerGroupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.LoadBalancer
	r.projectID = data.ProjectID
	r.claims = data.Claims
}

func (r *ServerGroupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ServerGroupMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverGroupID, diags := parse.UUIDString(reference.Get(plan.ServerGroup, reference.ID), "server_group.id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if strings.TrimSpace(plan.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Missing member name", "name must be configured and non-empty.")
		return
	}
	if strings.TrimSpace(plan.Address.ValueString()) == "" {
		resp.Diagnostics.AddError("Missing member address", "address must be configured and non-empty.")
		return
	}

	body := buildServerGroupMemberCreateBody(plan)

	loadBalancerID, unlock, err := r.lockLoadBalancer(ctx, serverGroupID)
	if err != nil {
		resp.Diagnostics.AddError("Error creating server group member", err.Error())
		return
	}
	defer unlock()

	member, err := requestLoadBalancerOperation(ctx, fmt.Sprintf("the creation of a member in server group %s", serverGroupID), func(ctx context.Context) (*loadbalancersdk.ServerGroupMemberSchema, error) {
		if err := r.waitUntilIdle(ctx, loadBalancerID, serverGroupID, nil, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval); err != nil {
			return nil, fmt.Errorf("the parents of the member did not become idle before the creation: %w", err)
		}
		return r.client.CreateServerGroupMember(ctx, serverGroupID, loadbalancersdk.CreateServerGroupMemberParams{
			ProjectID: r.projectID,
		}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating server group member", err.Error())
		return
	}

	memberID := member.Id
	populateServerGroupMemberResourceState(member, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	readyMember, err := r.waitUntilReady(ctx, serverGroupID, memberID, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for server group member to become active",
			fmt.Sprintf("%s\n\nServer group member ID %s has been preserved in Terraform state so it will be marked tainted and cleaned up on subsequent runs.", err, memberID),
		)
		return
	}

	populateServerGroupMemberResourceState(readyMember, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerGroupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ServerGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverGroupID, diags := parse.UUIDString(reference.Get(state.ServerGroup, reference.ID), "server_group.id")
	resp.Diagnostics.Append(diags...)
	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.GetServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.GetServerGroupMemberParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading server group member", err.Error())
		return
	}

	if member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateServerGroupMemberResourceState(member, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ServerGroupMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServerGroupMemberResourceModel
	var state ServerGroupMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverGroupID, diags := parse.UUIDString(reference.Get(state.ServerGroup, reference.ID), "server_group.id")
	resp.Diagnostics.Append(diags...)
	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Name.Equal(state.Name) && strings.TrimSpace(plan.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid member name", "name must not be empty or whitespace-only.")
		return
	}

	body, hasChanges := buildServerGroupMemberUpdateBody(plan, state)
	if hasChanges {
		loadBalancerID, unlock, err := r.lockLoadBalancer(ctx, serverGroupID)
		if err != nil {
			resp.Diagnostics.AddError("Error updating server group member", err.Error())
			return
		}
		defer unlock()

		_, err = requestLoadBalancerOperation(ctx, fmt.Sprintf("the update of server group member %s", memberID), func(ctx context.Context) (*loadbalancersdk.ServerGroupMemberSchema, error) {
			if err := r.waitUntilIdle(ctx, loadBalancerID, serverGroupID, &memberID, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval); err != nil {
				return nil, fmt.Errorf("the member did not become idle before the update: %w", err)
			}
			return r.client.PartialUpdateServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.PartialUpdateServerGroupMemberParams{
				ProjectID: r.projectID,
			}, body)
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating server group member", err.Error())
			return
		}

		readyMember, err := r.waitUntilReady(ctx, serverGroupID, memberID, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for server group member update", err.Error())
			return
		}

		populateServerGroupMemberResourceState(readyMember, &plan)
	} else {
		current, err := r.client.GetServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.GetServerGroupMemberParams{
			ProjectID: r.projectID,
		})
		if err != nil {
			resp.Diagnostics.AddError("Error reading server group member", err.Error())
			return
		}
		populateServerGroupMemberResourceState(current, &plan)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerGroupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ServerGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverGroupID, diags := parse.UUIDString(reference.Get(state.ServerGroup, reference.ID), "server_group.id")
	resp.Diagnostics.Append(diags...)
	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	loadBalancerID, unlock, err := r.lockLoadBalancer(ctx, serverGroupID)
	if err != nil {
		// The owner search fails when the Server Group is gone, and the
		// backend deletes the Members of a deleted Server Group. Only the GET
		// of the Member itself proves that the Member is gone too.
		if gone, getErr := r.memberGone(ctx, serverGroupID, memberID); getErr == nil && gone {
			return
		}
		resp.Diagnostics.AddError("Error deleting server group member", err.Error())
		return
	}
	defer unlock()

	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the deletion of server group member %s", memberID), func(ctx context.Context) error {
		gone, err := r.waitUntilDeletable(ctx, loadBalancerID, serverGroupID, memberID, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval)
		if err != nil {
			return fmt.Errorf("the member did not become idle before the deletion: %w", err)
		}
		// A Member that is already gone needs no DELETE, and Delete treats
		// ErrNotFound as a completed deletion.
		if gone {
			return fmt.Errorf("server group member %s is gone: %w", memberID, loadbalancersdk.ErrNotFound)
		}
		return r.client.DeleteServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.DeleteServerGroupMemberParams{
			ProjectID: r.projectID,
		})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting server group member", err.Error())
		return
	}

	if err := r.waitUntilDeleted(ctx, serverGroupID, memberID, serverGroupMemberReadyTimeout, serverGroupMemberReadyPollInterval); err != nil {
		resp.Diagnostics.AddError("Error waiting for server group member deletion", err.Error())
	}
}

func (r *ServerGroupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError(
			"Invalid server group member import ID",
			fmt.Sprintf("Expected \"<server_group_id>/<member_id>\", got %q.", req.ID),
		)
		return
	}

	serverGroupID := strings.TrimSpace(parts[0])
	memberID := strings.TrimSpace(parts[1])

	if _, diags := parse.UUIDString(types.StringValue(serverGroupID), "server_group.id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(memberID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_group"), reference.Value(reference.ID, types.StringValue(serverGroupID)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(memberID))...)
}

// lockLoadBalancer resolves the root Load Balancer of Server Group
// serverGroupID and takes its claims lock.
//
// Every Member operation claims the Member, its Server Group, and that Load
// Balancer, so the lock serializes the Member with the other children of the
// same Load Balancer. A Member records only its Server Group, so the owner
// search lists the Load Balancers of the project. The caller holds the lock
// from the idle wait to the end of the waiter after the request.
func (r *ServerGroupMemberResource) lockLoadBalancer(ctx context.Context, serverGroupID core.UUID) (core.UUID, func(), error) {
	loadBalancerID, err := loadbalancerlookup.ServerGroupLoadBalancer(ctx, r.client, r.projectID, serverGroupID)
	if err != nil {
		return core.UUID{}, nil, fmt.Errorf("resolve the load balancer of server group %s: %w", serverGroupID, err)
	}
	unlock, err := r.claims.Lock(ctx, loadBalancerID.String())
	if err != nil {
		return core.UUID{}, nil, fmt.Errorf("lock load balancer %s: %w", loadBalancerID, err)
	}
	return loadBalancerID, unlock, nil
}

// memberGone reports whether the GET of Member memberID answers 404 or
// DELETED.
func (r *ServerGroupMemberResource) memberGone(ctx context.Context, serverGroupID, memberID core.UUID) (bool, error) {
	member, err := r.client.GetServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.GetServerGroupMemberParams{
		ProjectID: r.projectID,
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED, nil
}

// waitUntilIdle is the idle wait before a Create or an Update.
//
// A Create claims Server Group serverGroupID and Load Balancer
// loadBalancerID. An Update also claims Member memberID, which is nil for a
// Create. ACTIVE is the idle state of each claimed object, because the
// response shows a held claim as PENDING_UPDATE, PENDING_DELETE, or ERROR.
// ERROR, UNKNOWN, DELETED, a missing object, or an unrecognized state fails
// at once, because the backend refuses the request until a user repairs the
// object.
func (r *ServerGroupMemberResource) waitUntilIdle(
	ctx context.Context,
	loadBalancerID, serverGroupID core.UUID,
	memberID *core.UUID,
	timeout, pollInterval time.Duration,
) error {
	w := wait.New[struct{}](pollInterval, timeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		if memberID != nil {
			member, err := r.client.GetServerGroupMember(ctx, serverGroupID, *memberID, loadbalancersdk.GetServerGroupMemberParams{
				ProjectID: r.projectID,
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("server group member %s: %w", *memberID, err)
			}
			if err := serverGroupMemberActiveIdle("server group member", *memberID, member.ProvisioningStatus); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, r.parentsIdle(ctx, loadBalancerID, serverGroupID)
	})
	return err
}

// waitUntilDeletable is the idle wait before a DELETE, and reports whether
// Member memberID is already gone.
//
// A DELETE claims the Member, Server Group serverGroupID, and Load Balancer
// loadBalancerID. The response shows a held claim on the Member as
// PENDING_CREATE, PENDING_UPDATE, or PENDING_DELETE, so the wait goes on in
// these states. ACTIVE, ERROR, and UNKNOWN of the Member count as idle, and
// Delete sends the DELETE in each of them. A DELETED Member is gone, and an
// unrecognized state fails at once. Each parent is idle in ACTIVE only. A
// parent in ERROR, UNKNOWN, or DELETED, or a missing parent, fails at once. A
// missing parent error never wraps ErrNotFound, so that only the GET of the
// Member can report the Member as gone.
func (r *ServerGroupMemberResource) waitUntilDeletable(
	ctx context.Context,
	loadBalancerID, serverGroupID, memberID core.UUID,
	timeout, pollInterval time.Duration,
) (bool, error) {
	w := wait.New[bool](pollInterval, timeout)
	gone, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		member, err := r.client.GetServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.GetServerGroupMemberParams{
			ProjectID: r.projectID,
		})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("server group member %s: %w", memberID, err)
		}
		switch member.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return true, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return false, fmt.Errorf("server group member %s provisioning_status=%s: %w", memberID, member.ProvisioningStatus, wait.ErrNotReady)
		default:
			return false, fmt.Errorf("server group member %s: unrecognized provisioning_status=%s", memberID, member.ProvisioningStatus)
		}
		err = r.parentsIdle(ctx, loadBalancerID, serverGroupID)
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return false, fmt.Errorf("a parent of server group member %s is missing: %v", memberID, err)
		}
		return false, err
	})
	return gone, err
}

// parentsIdle is one poll of an idle wait for Server Group serverGroupID and
// Load Balancer loadBalancerID. It returns nil when both are ACTIVE.
func (r *ServerGroupMemberResource) parentsIdle(ctx context.Context, loadBalancerID, serverGroupID core.UUID) error {
	group, err := r.client.GetServerGroup(ctx, loadBalancerID, serverGroupID, loadbalancersdk.GetServerGroupParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		return fmt.Errorf("server group %s: %w", serverGroupID, err)
	}
	if err := serverGroupMemberActiveIdle("server group", serverGroupID, group.ProvisioningStatus); err != nil {
		return err
	}
	lb, err := r.client.GetLoadBalancer(ctx, loadBalancerID, loadbalancersdk.GetLoadBalancerParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		return fmt.Errorf("load balancer %s: %w", loadBalancerID, err)
	}
	return serverGroupMemberActiveIdle("load balancer", loadBalancerID, lb.ProvisioningStatus)
}

// waitUntilReady waits until Member id reports ACTIVE after a Create or an
// Update. ERROR and UNKNOWN fail at once: the backend sets UNKNOWN when its
// event watcher gives up, and never leaves it on its own.
func (r *ServerGroupMemberResource) waitUntilReady(
	ctx context.Context,
	serverGroupID, id core.UUID,
	timeout, pollInterval time.Duration,
) (*loadbalancersdk.ServerGroupMemberSchema, error) {
	w := wait.New[*loadbalancersdk.ServerGroupMemberSchema](pollInterval, timeout)
	member, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.ServerGroupMemberSchema, error) {
		member, err := r.client.GetServerGroupMember(ctx, serverGroupID, id, loadbalancersdk.GetServerGroupMemberParams{
			ProjectID: r.projectID,
		})
		if err != nil {
			return nil, err
		}
		switch member.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			return member, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
			loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return nil, fmt.Errorf("terminal provisioning status %s", member.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return nil, fmt.Errorf("provisioning_status=%s: %w", member.ProvisioningStatus, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("unrecognized provisioning_status=%s", member.ProvisioningStatus)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("server group member %s: %w", id, err)
	}
	return member, nil
}

// waitUntilDeleted waits until the GET of member id of Server Group
// serverGroupID answers 404.
//
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, so the wait
// stops. Before PENDING_DELETE it can still be the old state, so the wait goes on.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: the GET hides the delete
// until the infrastructure accepts it, so a refused delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *ServerGroupMemberResource) waitUntilDeleted(
	ctx context.Context,
	serverGroupID, id core.UUID,
	timeout, pollInterval time.Duration,
) error {
	w := wait.New[struct{}](pollInterval, timeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		member, err := r.client.GetServerGroupMember(ctx, serverGroupID, id, loadbalancersdk.GetServerGroupMemberParams{
			ProjectID: r.projectID,
		})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		switch member.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return struct{}{}, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			pendingDeleteObserved = true
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", member.ProvisioningStatus, wait.ErrNotReady)
		// Before PENDING_DELETE, this may be the state the object had before the DELETE.
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("terminal provisioning status %s while deleting", member.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", member.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", member.ProvisioningStatus, wait.ErrNotReady)
		default:
			return struct{}{}, fmt.Errorf("unrecognized provisioning_status=%s", member.ProvisioningStatus)
		}
	})
	if err != nil {
		return fmt.Errorf("server group member %s: %w", id, err)
	}
	return nil
}

func buildServerGroupMemberCreateBody(plan ServerGroupMemberResourceModel) loadbalancersdk.ServerGroupMemberCreateSchema {
	body := loadbalancersdk.ServerGroupMemberCreateSchema{
		Address:      strings.TrimSpace(plan.Address.ValueString()),
		Name:         strings.TrimSpace(plan.Name.ValueString()),
		ProtocolPort: int(plan.ProtocolPort.ValueInt64()),
	}
	if !plan.Backup.IsNull() && !plan.Backup.IsUnknown() {
		b := plan.Backup.ValueBool()
		body.Backup = &b
	}
	if !plan.Weight.IsNull() && !plan.Weight.IsUnknown() {
		w := int(plan.Weight.ValueInt64())
		body.Weight = &w
	}
	return body
}

func buildServerGroupMemberUpdateBody(plan, state ServerGroupMemberResourceModel) (loadbalancersdk.ServerGroupMemberPartialUpdateSchema, bool) {
	var body loadbalancersdk.ServerGroupMemberPartialUpdateSchema
	hasChanges := false

	if name := strings.TrimSpace(plan.Name.ValueString()); !compare.SameTrimmed(state.Name, name) {
		body.Name = &name
		hasChanges = true
	}

	if !plan.Weight.Equal(state.Weight) {
		weight := int(plan.Weight.ValueInt64())
		body.Weight = &weight
		hasChanges = true
	}

	if !plan.Backup.Equal(state.Backup) {
		backup := plan.Backup.ValueBool()
		body.Backup = &backup
		hasChanges = true
	}

	return body, hasChanges
}

type memberProtocolPortValidator struct{}

func (v memberProtocolPortValidator) Description(_ context.Context) string {
	return "value must be between 1 and 65535"
}

func (v memberProtocolPortValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v memberProtocolPortValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	val := req.ConfigValue.ValueInt64()
	if val < 1 || val > 65535 {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid protocol_port",
			fmt.Sprintf("protocol_port must be between 1 and 65535, got %d", val),
		)
	}
}

// serverGroupMemberActiveIdle classifies status for one poll of an idle wait
// that needs ACTIVE: ACTIVE is idle, PENDING_* keeps waiting, and every other
// status fails at once. The backend sets UNKNOWN when its event watcher gives
// up, and never leaves it on its own, so UNKNOWN is as final as ERROR.
func serverGroupMemberActiveIdle(kind string, id core.UUID, status loadbalancersdk.LoadBalancingProvisioningStatus) error {
	switch status {
	case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
		return nil
	case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
		return fmt.Errorf("%s %s provisioning_status=%s: %w", kind, id, status, wait.ErrNotReady)
	case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
		loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
		return fmt.Errorf("%s %s is in terminal provisioning status %s", kind, id, status)
	default:
		return fmt.Errorf("%s %s: unrecognized provisioning_status=%s", kind, id, status)
	}
}
