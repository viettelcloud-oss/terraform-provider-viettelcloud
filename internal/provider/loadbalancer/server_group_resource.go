package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &ServerGroupResource{}
	_ resource.ResourceWithImportState    = &ServerGroupResource{}
	_ resource.ResourceWithModifyPlan     = &ServerGroupResource{}
	_ resource.ResourceWithValidateConfig = &ServerGroupResource{}
)

const (
	serverGroupReadyTimeout      = 30 * time.Minute
	serverGroupReadyPollInterval = 5 * time.Second
)

// serverGroupOperation names the request that waitUntilIdle prepares, because
// each request needs a different idle state.
type serverGroupOperation int

const (
	serverGroupCreate serverGroupOperation = iota
	serverGroupUpdate
	serverGroupDelete
)

type ServerGroupResource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

func NewServerGroupResource() resource.Resource {
	return &ServerGroupResource{}
}

func (r *ServerGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_server_group"
}

func (r *ServerGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer Server Group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Server Group ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"load_balancer": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Load Balancer that owns this server group.",
				Attributes:  reference.Attributes(reference.ID, "Load Balancer ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Server Group.",
			},
			"protocol": schema.StringAttribute{
				Required:    true,
				Description: "Protocol for the server group: HTTP, HTTPS, PROXY, PROXYV2, TCP, or UDP.",
				PlanModifiers: []planmodifier.String{
					replace.String(compare.SameTrimmedString),
				},
			},
			"algorithm": schema.StringAttribute{
				Required:    true,
				Description: "Load balancing algorithm: ROUND_ROBIN, LEAST_CONNECTIONS, SOURCE_IP, or SOURCE_IP_PORT.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description. Omission preserves the current value. Set an empty string to clear it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enable_h2": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Enable HTTP/2 support. Omission uses the backend value and preserves it on update. Set false to disable it, except for HTTPS, which always uses HTTP/2.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"session_persistence": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Session persistence configuration. Creation without this object requests NONE. Removal preserves the current value. Set type to NONE to disable persistence.",
				// Both inputs are also Computed, because Terraform plans an omitted
				// object as null instead of its prior value when that value holds a
				// non-Computed attribute. preserveServerGroupSessionPersistenceState plans
				// configured inputs as written, and ValidateConfig requires type.
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Session persistence type: NONE, HTTP_COOKIE, APP_COOKIE, or SOURCE_IP. Required when session_persistence is configured.",
					},
					"cookie_name": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Cookie name when session persistence type is APP_COOKIE. Omit it for other types.",
					},
				},
				PlanModifiers: []planmodifier.Object{
					preserveServerGroupSessionPersistenceState(),
				},
			},
			"health_check": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Health check monitor configuration. Omit to create or update without a health check.",
				Attributes: map[string]schema.Attribute{
					"monitor_type": schema.StringAttribute{
						Required:    true,
						Description: "Monitor type: HTTP, HTTPS, PING, SCTP, TCP, TLS-HELLO, or UDP-CONNECT.",
					},
					"delay": schema.Int64Attribute{
						Required:    true,
						Description: "Delay between health checks in seconds.",
					},
					"timeout": schema.Int64Attribute{
						Required:    true,
						Description: "Health check timeout in seconds.",
					},
					"max_retries": schema.Int64Attribute{
						Required:    true,
						Description: "Number of successful checks before considering server online.",
					},
					"max_retries_down": schema.Int64Attribute{
						Required:    true,
						Description: "Number of failed checks before considering server offline.",
					},
					"path": schema.StringAttribute{
						Optional:    true,
						Description: "HTTP URL path to check (required for HTTP/HTTPS).",
					},
					"http_method": schema.StringAttribute{
						Optional:    true,
						Description: "HTTP method to use (required for HTTP/HTTPS): GET, POST, PUT, DELETE, HEAD, OPTIONS, PATCH, TRACE, CONNECT.",
					},
					"expected_codes": schema.StringAttribute{
						Optional:    true,
						Description: "Expected HTTP status codes, e.g. 200 or 200-299 (required for HTTP/HTTPS).",
					},
				},
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the Server Group.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the Server Group.",
			},
			"member_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of backend members in the Server Group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group was last updated (RFC3339).",
			},
		},
	}
}

func (r *ServerGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ServerGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config ServerGroupResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The backend stores enable_h2 = true on an HTTPS group whatever the
	// request sends, so a configured false fails after apply as an
	// inconsistent result.
	if !config.EnableH2.IsNull() && !config.EnableH2.IsUnknown() && !config.EnableH2.ValueBool() &&
		!config.Protocol.IsNull() && !config.Protocol.IsUnknown() &&
		loadbalancersdk.ServerGroupProtocolType(strings.TrimSpace(config.Protocol.ValueString())) == loadbalancersdk.ServerGroupProtocolTypeHTTPS {
		resp.Diagnostics.AddAttributeError(
			path.Root("enable_h2"),
			"Invalid server group enable_h2",
			"An HTTPS server group always uses HTTP/2. Omit enable_h2 or set it to true.",
		)
	}
	if config.SessionPersistence.IsNull() || config.SessionPersistence.IsUnknown() {
		return
	}
	var persistence ServerGroupSessionPersistenceModel
	resp.Diagnostics.Append(config.SessionPersistence.As(ctx, &persistence, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	// type is Computed only so that an omitted object keeps its prior value.
	if persistence.Type.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("session_persistence").AtName("type"), "Missing session persistence type", "session_persistence.type must be configured when session_persistence is configured.")
		return
	}
	if persistence.Type.IsUnknown() {
		return
	}
	if strings.TrimSpace(persistence.Type.ValueString()) != "APP_COOKIE" && !persistence.CookieName.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("session_persistence").AtName("cookie_name"), "Invalid cookie name", "cookie_name can only be configured when session_persistence.type is APP_COOKIE.")
	}
}

// ModifyPlan removes computed-only updates after attribute modifiers have
// restored unconfigured optional computed values. When optional inputs are
// omitted or removed, Framework marks unconfigured runtime outputs unknown.
// Preserve them only when restoring the unconfigured computed outputs makes
// the entire plan identical to refreshed state. Any configured change or
// unknown input keeps the normal update plan.
func (r *ServerGroupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || !req.Config.Raw.IsFullyKnown() || len(resp.RequiresReplace) > 0 {
		return
	}
	candidate := req.Plan
	for name, attribute := range req.Plan.Schema.GetAttributes() {
		if !attribute.IsComputed() {
			continue
		}
		p := path.Root(name)
		var configured, planned, prior attr.Value
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, p, &configured)...)
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, p, &planned)...)
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, p, &prior)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if configured.IsNull() && planned.IsUnknown() {
			resp.Diagnostics.Append(candidate.SetAttribute(ctx, p, prior)...)
		}
	}
	if !resp.Diagnostics.HasError() && candidate.Raw.Equal(req.State.Raw) {
		resp.Plan = candidate
	}
}

func (r *ServerGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ServerGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, diags := parse.UUIDString(reference.Get(plan.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildServerGroupCreateBody(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	unlock, err := r.claims.Lock(ctx, lbID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Server Group", err.Error())
		return
	}
	defer unlock()
	sg, err := requestLoadBalancerOperation(ctx, fmt.Sprintf("the creation of a server group on load balancer %s", lbID), func(ctx context.Context) (*loadbalancersdk.ServerGroupDetailSchema, error) {
		if _, err := r.waitUntilIdle(ctx, serverGroupCreate, lbID, core.UUID{}, serverGroupReadyTimeout, serverGroupReadyPollInterval); err != nil {
			return nil, fmt.Errorf("the load balancer did not become idle before the creation: %w", err)
		}
		return r.client.CreateServerGroup(ctx, lbID, loadbalancersdk.CreateServerGroupParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Server Group", err.Error())
		return
	}

	groupID := sg.Id
	// Keep the identity before the waiter so that a timeout leaves the group in state.
	populateServerGroupResourceState(sg, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	readySG, err := r.waitUntilReady(ctx, lbID, groupID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for Server Group to become active", err.Error())
		return
	}

	populateServerGroupResourceState(readySG, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ServerGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, diags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(diags...)
	groupID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sg, err := r.client.GetServerGroup(ctx, lbID, groupID, loadbalancersdk.GetServerGroupParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Server Group", err.Error())
		return
	}

	if sg.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateServerGroupResourceState(sg, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ServerGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServerGroupResourceModel
	var state ServerGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, diags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(diags...)
	groupID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, bodyDiags := buildServerGroupUpdateBody(ctx, plan, state)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	unlock, err := r.claims.Lock(ctx, lbID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error updating Server Group", err.Error())
		return
	}
	defer unlock()
	_, err = requestLoadBalancerOperation(ctx, fmt.Sprintf("the update of server group %s on load balancer %s", groupID, lbID), func(ctx context.Context) (*loadbalancersdk.ServerGroupDetailSchema, error) {
		current, err := r.waitUntilIdle(ctx, serverGroupUpdate, lbID, groupID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
		if err != nil {
			return nil, fmt.Errorf("the server group did not become idle before the update: %w", err)
		}
		// The schema does not manage backend_tls_mode, but every PUT body carries
		// it. Send back the mode that the idle wait read right before this PUT,
		// so that the update keeps it.
		request := body
		if current.BackendTlsMode.IsSpecified() && !current.BackendTlsMode.IsNull() {
			request.BackendTlsMode = current.BackendTlsMode
		} else {
			request.BackendTlsMode.SetNull()
		}
		return r.client.UpdateServerGroup(ctx, lbID, groupID, loadbalancersdk.UpdateServerGroupParams{ProjectID: r.projectID}, request)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error updating Server Group", err.Error())
		return
	}

	readySG, err := r.waitUntilReady(ctx, lbID, groupID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for Server Group update", err.Error())
		return
	}

	populateServerGroupResourceState(readySG, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ServerGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ServerGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, diags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(diags...)
	groupID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	unlock, err := r.claims.Lock(ctx, lbID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Server Group", err.Error())
		return
	}
	defer unlock()
	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the deletion of server group %s on load balancer %s", groupID, lbID), func(ctx context.Context) error {
		// A group that is already gone needs no DELETE: waitUntilIdle then
		// fails with ErrNotFound, which Delete treats as a completed deletion.
		if _, err := r.waitUntilIdle(ctx, serverGroupDelete, lbID, groupID, serverGroupReadyTimeout, serverGroupReadyPollInterval); err != nil {
			return fmt.Errorf("the server group did not become idle before the deletion: %w", err)
		}
		return r.client.DeleteServerGroup(ctx, lbID, groupID, loadbalancersdk.DeleteServerGroupParams{ProjectID: r.projectID})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Server Group", err.Error())
		return
	}

	if err := r.waitUntilDeleted(ctx, lbID, groupID, serverGroupReadyTimeout, serverGroupReadyPollInterval); err != nil {
		resp.Diagnostics.AddError("Error waiting for Server Group deletion", err.Error())
	}
}

func (r *ServerGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError(
			"Invalid Server Group import ID",
			fmt.Sprintf("Expected \"<load_balancer_id>/<server_group_id>\", got %q.", req.ID),
		)
		return
	}

	lbID := strings.TrimSpace(parts[0])
	groupID := strings.TrimSpace(parts[1])

	if _, diags := parse.UUIDString(types.StringValue(lbID), "load_balancer.id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(groupID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("load_balancer"), reference.Value(reference.ID, types.StringValue(lbID)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), groupID)...)
}

// waitUntilIdle is the idle wait before the request of op on Server Group id
// of Load Balancer lbID. It returns the group that the last poll read, or nil
// for a create, which passes the zero id.
//
// A create claims the Load Balancer, which is the root and the only parent.
// An update or a delete claims the group and the Load Balancer. The wait goes
// on while either object is PENDING_CREATE, PENDING_UPDATE, or PENDING_DELETE.
//
// The Load Balancer is idle when ACTIVE, and ERROR, UNKNOWN, or DELETED fail
// at once before every request. The group is idle when ACTIVE before an
// update, and ERROR or UNKNOWN fail at once. Before a delete, ACTIVE, ERROR,
// and UNKNOWN of the group count as idle, and Delete sends the DELETE in each
// of them. Where UNKNOWN fails, it fails like ERROR, because the backend never
// leaves it on its own.
//
// A group that is gone, by a 404 or DELETED, fails with an error that wraps
// ErrNotFound. A Load Balancer that is gone fails without ErrNotFound, because
// only a GET of the group proves that the group is gone. The caller holds the
// claims lock of lbID from this wait to the end of the wait after the request.
func (r *ServerGroupResource) waitUntilIdle(ctx context.Context, op serverGroupOperation, lbID, id core.UUID, timeout, pollInterval time.Duration) (*loadbalancersdk.ServerGroupDetailSchema, error) {
	w := wait.New[*loadbalancersdk.ServerGroupDetailSchema](pollInterval, timeout)
	return w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.ServerGroupDetailSchema, error) {
		var sg *loadbalancersdk.ServerGroupDetailSchema
		if op != serverGroupCreate {
			var err error
			sg, err = r.client.GetServerGroup(ctx, lbID, id, loadbalancersdk.GetServerGroupParams{ProjectID: r.projectID})
			if errors.Is(err, loadbalancersdk.ErrNotFound) {
				return nil, fmt.Errorf("server group %s is gone: %w", id, loadbalancersdk.ErrNotFound)
			}
			if err != nil {
				return nil, fmt.Errorf("server group %s: %w", id, err)
			}
			if sg.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
				return nil, fmt.Errorf("server group %s is gone: %w", id, loadbalancersdk.ErrNotFound)
			}
			if err := serverGroupIdleStatus(sg.ProvisioningStatus, op == serverGroupDelete); err != nil {
				return nil, fmt.Errorf("server group %s: %w", id, err)
			}
		}
		lb, err := r.client.GetLoadBalancer(ctx, lbID, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return nil, fmt.Errorf("load balancer %s is gone", lbID)
		}
		if err != nil {
			return nil, fmt.Errorf("load balancer %s: %w", lbID, err)
		}
		if err := serverGroupIdleStatus(lb.ProvisioningStatus, false); err != nil {
			return nil, fmt.Errorf("load balancer %s: %w", lbID, err)
		}
		return sg, nil
	})
}

// waitUntilReady waits until Server Group id of Load Balancer lbID reports
// ACTIVE after a create or an update. ACTIVE also shows that the operation
// released its claim on the group and on the Load Balancer. ERROR and UNKNOWN
// fail at once, because the backend does not leave either state on its own.
// The caller holds the claims lock of lbID until the wait ends.
func (r *ServerGroupResource) waitUntilReady(ctx context.Context, lbID, id core.UUID, timeout, pollInterval time.Duration) (*loadbalancersdk.ServerGroupDetailSchema, error) {
	w := wait.New[*loadbalancersdk.ServerGroupDetailSchema](pollInterval, timeout)
	sg, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.ServerGroupDetailSchema, error) {
		sg, err := r.client.GetServerGroup(ctx, lbID, id, loadbalancersdk.GetServerGroupParams{ProjectID: r.projectID})
		if err != nil {
			return nil, err
		}
		switch sg.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			return sg, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
			loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return nil, fmt.Errorf("terminal provisioning status %s", sg.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return nil, fmt.Errorf("provisioning_status=%s: %w", sg.ProvisioningStatus, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("unrecognized provisioning_status=%s", sg.ProvisioningStatus)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("server group %s: %w", id, err)
	}
	return sg, nil
}

// waitUntilDeleted waits until the GET of Server Group id of Load Balancer
// lbID answers 404. The backend deletes a group hard, so only a 404 proves that
// it is gone, and a parent Load Balancer that is ACTIVE again proves nothing.
// The caller holds the claims lock of lbID until the wait ends.
//
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, so the wait
// stops. Before PENDING_DELETE it can still be the old state, so the wait goes on.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: the GET hides the delete
// until the infrastructure accepts it, so a refused delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *ServerGroupResource) waitUntilDeleted(ctx context.Context, lbID, id core.UUID, timeout, pollInterval time.Duration) error {
	w := wait.New[struct{}](pollInterval, timeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		sg, err := r.client.GetServerGroup(ctx, lbID, id, loadbalancersdk.GetServerGroupParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		switch sg.ProvisioningStatus {
		// Before PENDING_DELETE, this may be the state the object had before the
		// DELETE, because waitUntilIdle counts both as idle.
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("terminal provisioning status %s after pending delete", sg.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", sg.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			pendingDeleteObserved = true
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", sg.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED,
			loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", sg.ProvisioningStatus, wait.ErrNotReady)
		default:
			return struct{}{}, fmt.Errorf("unrecognized provisioning_status=%s", sg.ProvisioningStatus)
		}
	})
	if err != nil {
		return fmt.Errorf("server group %s: %w", id, err)
	}
	return nil
}

// serverGroupIdleStatus classifies status for an idle wait: nil when idle, an
// error that wraps wait.ErrNotReady while PENDING_*, and any other error to
// fail at once. ACTIVE is always idle. ERROR and UNKNOWN are idle only when
// failedIsIdle is true, which is the group before a delete.
func serverGroupIdleStatus(status loadbalancersdk.LoadBalancingProvisioningStatus, failedIsIdle bool) error {
	switch status {
	case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
		return nil
	case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
		if failedIsIdle {
			return nil
		}
		return fmt.Errorf("terminal provisioning status %s", status)
	case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
		return fmt.Errorf("terminal provisioning status %s", status)
	case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
		return fmt.Errorf("provisioning_status=%s: %w", status, wait.ErrNotReady)
	default:
		return fmt.Errorf("unrecognized provisioning_status=%s", status)
	}
}

func buildServerGroupCreateBody(ctx context.Context, plan ServerGroupResourceModel) (loadbalancersdk.ServerGroupCreateSchema, diag.Diagnostics) {
	var body loadbalancersdk.ServerGroupCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing Server Group name", "name must be configured, known, and non-empty.")
	} else {
		body.Name = strings.TrimSpace(plan.Name.ValueString())
	}

	if plan.Protocol.IsNull() || plan.Protocol.IsUnknown() || strings.TrimSpace(plan.Protocol.ValueString()) == "" {
		diags.AddError("Missing protocol", "protocol must be configured, known, and non-empty.")
	} else {
		proto := loadbalancersdk.ServerGroupProtocolType(strings.TrimSpace(plan.Protocol.ValueString()))
		if !proto.Valid() {
			diags.AddError("Invalid protocol", "protocol supports HTTP, HTTPS, PROXY, PROXYV2, TCP, or UDP.")
		} else {
			body.Protocol = proto
		}
	}

	if plan.Algorithm.IsNull() || plan.Algorithm.IsUnknown() || strings.TrimSpace(plan.Algorithm.ValueString()) == "" {
		diags.AddError("Missing algorithm", "algorithm must be configured, known, and non-empty.")
	} else {
		algo := loadbalancersdk.ServerGroupAlgorithm(strings.TrimSpace(plan.Algorithm.ValueString()))
		if !algo.Valid() {
			diags.AddError("Invalid algorithm", "algorithm supports LEAST_CONNECTIONS, ROUND_ROBIN, SOURCE_IP, or SOURCE_IP_PORT.")
		} else {
			body.Algorithm = algo
		}
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		desc := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &desc
	}

	if !plan.EnableH2.IsNull() && !plan.EnableH2.IsUnknown() {
		body.EnableH2.Set(plan.EnableH2.ValueBool())
	}

	sp, spDiags := buildServerGroupSessionPersistenceKind(ctx, plan.SessionPersistence)
	diags.Append(spDiags...)
	body.SessionPersistence = sp

	hc, hcDiags := buildServerGroupHealthCheckConfig(ctx, plan.HealthCheck)
	diags.Append(hcDiags...)
	body.HealthCheck = hc

	return body, diags
}

// buildServerGroupUpdateBody builds the full PUT body. An omitted
// Optional+Computed input keeps its refreshed value instead of a zero value.
func buildServerGroupUpdateBody(ctx context.Context, plan, state ServerGroupResourceModel) (loadbalancersdk.ServerGroupUpdateSchema, diag.Diagnostics) {
	if plan.Description.IsNull() || plan.Description.IsUnknown() {
		plan.Description = state.Description
	}
	if plan.EnableH2.IsNull() || plan.EnableH2.IsUnknown() {
		plan.EnableH2 = state.EnableH2
	}
	if plan.SessionPersistence.IsNull() || plan.SessionPersistence.IsUnknown() {
		plan.SessionPersistence = state.SessionPersistence
	}
	var body loadbalancersdk.ServerGroupUpdateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing Server Group name", "name must be configured, known, and non-empty.")
	} else {
		body.Name = strings.TrimSpace(plan.Name.ValueString())
	}

	if plan.Algorithm.IsNull() || plan.Algorithm.IsUnknown() || strings.TrimSpace(plan.Algorithm.ValueString()) == "" {
		diags.AddError("Missing algorithm", "algorithm must be configured, known, and non-empty.")
	} else {
		algo := loadbalancersdk.ServerGroupAlgorithm(strings.TrimSpace(plan.Algorithm.ValueString()))
		if !algo.Valid() {
			diags.AddError("Invalid algorithm", "algorithm supports LEAST_CONNECTIONS, ROUND_ROBIN, SOURCE_IP, or SOURCE_IP_PORT.")
		} else {
			body.Algorithm = algo
		}
	}

	if plan.Description.IsNull() || plan.Description.IsUnknown() {
		diags.AddError("Cannot preserve Server Group description", "A full update requires a known description. Configure description before updating.")
	} else {
		body.Description = strings.TrimSpace(plan.Description.ValueString())
	}
	// A null effective enable_h2 goes out as JSON null. The SDK field has no
	// omitempty, so an unset value would serialize as false instead.
	if plan.EnableH2.IsNull() || plan.EnableH2.IsUnknown() {
		body.EnableH2.SetNull()
	} else {
		body.EnableH2.Set(plan.EnableH2.ValueBool())
	}
	// The SDK field backend_tls_mode has no omitempty, so an unset value would
	// serialize as an empty string, which the API rejects. Set it to null.
	body.BackendTlsMode.SetNull()

	if plan.SessionPersistence.IsNull() || plan.SessionPersistence.IsUnknown() {
		diags.AddError("Cannot preserve session persistence", "A full update requires known session persistence. Configure session_persistence before updating.")
	}
	if plan.HealthCheck.IsUnknown() || plan.EnableH2.IsUnknown() {
		diags.AddError("Unknown Server Group configuration", "health_check and enable_h2 must be known before an update.")
	}
	sp, spDiags := buildServerGroupSessionPersistenceKind(ctx, plan.SessionPersistence)
	diags.Append(spDiags...)
	body.SessionPersistence = sp

	hc, hcDiags := buildServerGroupHealthCheckConfig(ctx, plan.HealthCheck)
	diags.Append(hcDiags...)
	body.HealthCheck = hc

	return body, diags
}

// preserveServerGroupSessionPersistenceState returns the plan modifier of
// session_persistence. An omitted object keeps its prior value.
//
// A configured object is planned exactly as written. Otherwise Terraform
// proposes the prior cookie_name when the configuration omits it, but the
// backend clears cookie_name for every type except APP_COOKIE.
func preserveServerGroupSessionPersistenceState() planmodifier.Object {
	return serverGroupSessionPersistenceStatePlanModifier{}
}

type serverGroupSessionPersistenceStatePlanModifier struct{}

func (serverGroupSessionPersistenceStatePlanModifier) Description(context.Context) string {
	return "Keeps the prior session persistence when the object is omitted and plans a configured object as written."
}

func (m serverGroupSessionPersistenceStatePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (serverGroupSessionPersistenceStatePlanModifier) PlanModifyObject(_ context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	if req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsNull() {
		if req.PlanValue.IsUnknown() && !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
			resp.PlanValue = req.StateValue
		}
		return
	}
	resp.PlanValue = req.ConfigValue
}

func buildServerGroupSessionPersistenceKind(ctx context.Context, spObj types.Object) (loadbalancersdk.ServerGroupSessionPersistenceKind, diag.Diagnostics) {
	var diags diag.Diagnostics
	var spKind loadbalancersdk.ServerGroupSessionPersistenceKind

	if spObj.IsNull() || spObj.IsUnknown() {
		err := spKind.FromServerGroupSessionPersistenceBasic(loadbalancersdk.ServerGroupSessionPersistenceBasic{
			Kind: loadbalancersdk.ServerGroupSessionPersistenceBasicKindNONE,
		})
		if err != nil {
			diags.AddError("Invalid session persistence", err.Error())
		}
		return spKind, diags
	}

	var spModel ServerGroupSessionPersistenceModel
	diags.Append(spObj.As(ctx, &spModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return spKind, diags
	}

	if spModel.Type.IsNull() || spModel.Type.IsUnknown() || strings.TrimSpace(spModel.Type.ValueString()) == "" {
		diags.AddError("Missing session persistence type", "session_persistence.type must be configured and known.")
		return spKind, diags
	}

	spType := strings.TrimSpace(spModel.Type.ValueString())
	if spType == "APP_COOKIE" {
		if spModel.CookieName.IsNull() || spModel.CookieName.IsUnknown() || strings.TrimSpace(spModel.CookieName.ValueString()) == "" {
			diags.AddError("Missing cookie name", "cookie_name must be configured when session_persistence type is APP_COOKIE.")
			return spKind, diags
		}
		err := spKind.FromServerGroupSessionPersistenceAppCookie(loadbalancersdk.ServerGroupSessionPersistenceAppCookie{
			Kind:                         "APP_COOKIE",
			SessionPersistenceCookieName: strings.TrimSpace(spModel.CookieName.ValueString()),
		})
		if err != nil {
			diags.AddError("Invalid session persistence", err.Error())
		}
		return spKind, diags
	}

	if !spModel.CookieName.IsNull() {
		diags.AddError("Invalid cookie name", "cookie_name can only be configured when session_persistence.type is APP_COOKIE.")
		return spKind, diags
	}

	basicKind := loadbalancersdk.ServerGroupSessionPersistenceBasicKind(spType)
	if !basicKind.Valid() {
		diags.AddError("Invalid session persistence type", "session_persistence type supports NONE, HTTP_COOKIE, APP_COOKIE, or SOURCE_IP.")
		return spKind, diags
	}

	err := spKind.FromServerGroupSessionPersistenceBasic(loadbalancersdk.ServerGroupSessionPersistenceBasic{
		Kind: basicKind,
	})
	if err != nil {
		diags.AddError("Invalid session persistence", err.Error())
	}
	return spKind, diags
}

// buildServerGroupHealthCheckConfig returns an explicit null for an omitted
// health_check, so that a full PUT clears the monitor.
func buildServerGroupHealthCheckConfig(ctx context.Context, hcObj types.Object) (core.Nullable[loadbalancersdk.ServerGroupHealthCheckConfig], diag.Diagnostics) {
	var diags diag.Diagnostics
	var hcConfig core.Nullable[loadbalancersdk.ServerGroupHealthCheckConfig]

	if hcObj.IsNull() || hcObj.IsUnknown() {
		hcConfig.SetNull()
		return hcConfig, diags
	}

	var hcModel ServerGroupHealthCheckModel
	diags.Append(hcObj.As(ctx, &hcModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return hcConfig, diags
	}

	if hcModel.MonitorType.IsNull() || hcModel.MonitorType.IsUnknown() || strings.TrimSpace(hcModel.MonitorType.ValueString()) == "" {
		diags.AddError("Missing health check monitor type", "health_check.monitor_type must be configured and known.")
		return hcConfig, diags
	}

	monitorType := strings.TrimSpace(hcModel.MonitorType.ValueString())
	if !loadbalancersdk.ServerGroupMonitorType(monitorType).Valid() {
		diags.AddError("Invalid health check monitor type", "health_check.monitor_type supports HTTP, HTTPS, PING, SCTP, TCP, TLS-HELLO, or UDP-CONNECT.")
		return hcConfig, diags
	}

	for name, value := range map[string]types.Int64{
		"delay":            hcModel.Delay,
		"timeout":          hcModel.Timeout,
		"max_retries":      hcModel.MaxRetries,
		"max_retries_down": hcModel.MaxRetriesDown,
	} {
		if value.IsNull() || value.IsUnknown() || value.ValueInt64() < 1 {
			diags.AddError("Invalid health check configuration", fmt.Sprintf("health_check.%s must be known and positive.", name))
		}
	}
	if diags.HasError() {
		return hcConfig, diags
	}

	var config loadbalancersdk.ServerGroupHealthCheckConfig
	delay := int(hcModel.Delay.ValueInt64())
	timeout := int(hcModel.Timeout.ValueInt64())
	maxRetries := int(hcModel.MaxRetries.ValueInt64())
	maxRetriesDown := int(hcModel.MaxRetriesDown.ValueInt64())

	if monitorType == "HTTP" || monitorType == "HTTPS" {
		if hcModel.Path.IsNull() || hcModel.Path.IsUnknown() || strings.TrimSpace(hcModel.Path.ValueString()) == "" {
			diags.AddError("Missing health check path", "health_check.path must be configured for HTTP/HTTPS monitors.")
		}
		if hcModel.HTTPMethod.IsNull() || hcModel.HTTPMethod.IsUnknown() || strings.TrimSpace(hcModel.HTTPMethod.ValueString()) == "" {
			diags.AddError("Missing health check HTTP method", "health_check.http_method must be configured for HTTP/HTTPS monitors.")
		} else if !loadbalancersdk.ServerGroupMonitorHTTPMethod(strings.TrimSpace(hcModel.HTTPMethod.ValueString())).Valid() {
			diags.AddError("Invalid health check HTTP method", "health_check.http_method supports GET, POST, PUT, DELETE, HEAD, OPTIONS, PATCH, TRACE, or CONNECT.")
		}
		if hcModel.ExpectedCodes.IsNull() || hcModel.ExpectedCodes.IsUnknown() || strings.TrimSpace(hcModel.ExpectedCodes.ValueString()) == "" {
			diags.AddError("Missing health check expected codes", "health_check.expected_codes must be configured for HTTP/HTTPS monitors.")
		}
		if diags.HasError() {
			return hcConfig, diags
		}

		err := config.FromServerGroupHealthCheckHttpConfig(loadbalancersdk.ServerGroupHealthCheckHttpConfig{
			MonitorType:           loadbalancersdk.ServerGroupHealthCheckHttpConfigMonitorType(monitorType),
			MonitorDelay:          delay,
			MonitorTimeout:        timeout,
			MonitorMaxRetries:     maxRetries,
			MonitorMaxRetriesDown: maxRetriesDown,
			MonitorPath:           strings.TrimSpace(hcModel.Path.ValueString()),
			MonitorHttpMethod:     loadbalancersdk.ServerGroupMonitorHTTPMethod(strings.TrimSpace(hcModel.HTTPMethod.ValueString())),
			MonitorExpectedCodes:  strings.TrimSpace(hcModel.ExpectedCodes.ValueString()),
		})
		if err != nil {
			diags.AddError("Invalid health check configuration", err.Error())
			return hcConfig, diags
		}
		hcConfig.Set(config)
		return hcConfig, diags
	}

	if (!hcModel.Path.IsNull() && !hcModel.Path.IsUnknown()) ||
		(!hcModel.HTTPMethod.IsNull() && !hcModel.HTTPMethod.IsUnknown()) ||
		(!hcModel.ExpectedCodes.IsNull() && !hcModel.ExpectedCodes.IsUnknown()) {
		diags.AddError("Invalid health check configuration", "path, http_method, and expected_codes can only be specified when monitor_type is HTTP or HTTPS.")
		return hcConfig, diags
	}

	err := config.FromServerGroupHealthCheckOtherConfig(loadbalancersdk.ServerGroupHealthCheckOtherConfig{
		MonitorType:           loadbalancersdk.ServerGroupHealthCheckOtherConfigMonitorType(monitorType),
		MonitorDelay:          delay,
		MonitorTimeout:        timeout,
		MonitorMaxRetries:     maxRetries,
		MonitorMaxRetriesDown: maxRetriesDown,
	})
	if err != nil {
		diags.AddError("Invalid health check configuration", err.Error())
		return hcConfig, diags
	}
	hcConfig.Set(config)
	return hcConfig, diags
}
