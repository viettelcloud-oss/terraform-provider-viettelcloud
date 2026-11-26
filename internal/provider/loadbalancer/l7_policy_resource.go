package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

var (
	_ resource.Resource                   = &L7PolicyResource{}
	_ resource.ResourceWithConfigure      = &L7PolicyResource{}
	_ resource.ResourceWithImportState    = &L7PolicyResource{}
	_ resource.ResourceWithValidateConfig = &L7PolicyResource{}
)

const (
	l7PolicyPollInterval     = 5 * time.Second
	l7PolicyOperationTimeout = 5 * time.Minute

	// l7PolicyActionPoolAlias is the earlier name of
	// REDIRECT_TO_SERVER_GROUP, which the resource still accepts.
	l7PolicyActionPoolAlias = "REDIRECT_TO_POOL"
	// l7PolicyDefaultRedirectHTTPCode is the code that URL and prefix
	// redirects use when no code is configured or already in effect.
	l7PolicyDefaultRedirectHTTPCode = 302
)

// l7PolicyOperation names the operation that an idle wait precedes.
type l7PolicyOperation int

const (
	l7PolicyCreate l7PolicyOperation = iota
	l7PolicyUpdate
	l7PolicyDelete
)

type L7PolicyResource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

func NewL7PolicyResource() resource.Resource {
	return &L7PolicyResource{}
}

func (r *L7PolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_l7_policy"
}

func (r *L7PolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer L7 Policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "L7 policy ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"listener": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Listener that owns the L7 policy. Changing the listener replaces the policy.",
				Attributes:  reference.Attributes(reference.ID, "Listener ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the L7 policy.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the L7 policy. Omitting it preserves the backend value; set it to an empty string to clear it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"action": schema.StringAttribute{
				Required:    true,
				Description: "Action of the L7 policy: REJECT, REDIRECT_TO_SERVER_GROUP (or REDIRECT_TO_POOL), REDIRECT_TO_URL, or REDIRECT_PREFIX.",
			},
			"position": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Position of the L7 policy within the listener.",
			},
			"redirect_server_group": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Server group to redirect requests to. Required when action is REDIRECT_TO_SERVER_GROUP (or REDIRECT_TO_POOL), and not allowed for other actions.",
				Attributes:  reference.Attributes(reference.ID, "Server group ID (UUID)."),
			},
			"redirect_url": schema.StringAttribute{
				Optional:    true,
				Description: "URL to redirect requests to. Required when action is REDIRECT_TO_URL, and not allowed for other actions.",
			},
			"redirect_prefix": schema.StringAttribute{
				Optional:    true,
				Description: "Prefix to redirect requests to. Required when action is REDIRECT_PREFIX, and not allowed for other actions.",
			},
			"redirect_http_code": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "HTTP response code for URL or prefix redirects (e.g. 301, 302). Omitting it keeps the code in effect, or uses 302. Not allowed for other actions.",
				PlanModifiers: []planmodifier.Int64{
					l7PolicyRedirectHTTPCodePlan{},
				},
			},
			"rule_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of rules associated with the L7 policy.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the L7 policy.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the L7 policy.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 policy was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 policy was last updated (RFC3339).",
			},
		},
	}
}

func (r *L7PolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData),
		)
		return
	}

	r.client = data.LoadBalancer
	r.projectID = data.ProjectID
	r.claims = data.Claims
}

// ValidateConfig requires the target of the configured action and rejects the
// targets of every other action. The backend clears a target that the action
// does not use on each save, so a configured one could never match state.
func (r *L7PolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config L7PolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Action.IsNull() || config.Action.IsUnknown() {
		return
	}

	action, ok := normalizeL7PolicyAction(config.Action.ValueString())
	if !ok {
		resp.Diagnostics.AddAttributeError(
			path.Root("action"),
			"Invalid L7 policy action",
			fmt.Sprintf("action %q is not valid. Supported actions: REJECT, REDIRECT_TO_SERVER_GROUP (or REDIRECT_TO_POOL), REDIRECT_TO_URL, REDIRECT_PREFIX.", config.Action.ValueString()),
		)
		return
	}

	targets := []struct {
		attribute  string
		null       bool
		requiredBy loadbalancersdk.L7PolicyAction
		allowedBy  []loadbalancersdk.L7PolicyAction
	}{
		{"redirect_server_group", config.RedirectServerGroup.IsNull(), loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP, nil},
		{"redirect_url", config.RedirectURL.IsNull(), loadbalancersdk.L7PolicyActionREDIRECTTOURL, nil},
		{"redirect_prefix", config.RedirectPrefix.IsNull(), loadbalancersdk.L7PolicyActionREDIRECTPREFIX, nil},
		{"redirect_http_code", config.RedirectHTTPCode.IsNull(), "", []loadbalancersdk.L7PolicyAction{loadbalancersdk.L7PolicyActionREDIRECTTOURL, loadbalancersdk.L7PolicyActionREDIRECTPREFIX}},
	}
	for _, target := range targets {
		switch {
		case target.requiredBy == action && target.null:
			resp.Diagnostics.AddAttributeError(
				path.Root(target.attribute),
				"Missing L7 policy redirect target",
				fmt.Sprintf("%s is required when action is %s.", target.attribute, config.Action.ValueString()),
			)
		case target.requiredBy != action && !slices.Contains(target.allowedBy, action) && !target.null:
			resp.Diagnostics.AddAttributeError(
				path.Root(target.attribute),
				"Invalid L7 policy redirect target",
				fmt.Sprintf("%s cannot be configured when action is %s.", target.attribute, config.Action.ValueString()),
			)
		}
	}
}

func (r *L7PolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan L7PolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildL7PolicyCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	loadBalancerID, err := r.listenerLoadBalancer(ctx, body.ListenerId)
	if err != nil {
		resp.Diagnostics.AddError("Error creating L7 policy", err.Error())
		return
	}
	unlock, err := r.claims.Lock(ctx, loadBalancerID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error creating L7 policy", err.Error())
		return
	}
	defer unlock()

	subject := fmt.Sprintf("the creation of L7 policy under listener %s of load balancer %s", body.ListenerId, loadBalancerID)
	policy, err := requestLoadBalancerOperation(ctx, subject, func(ctx context.Context) (*loadbalancersdk.L7PolicySchema, error) {
		if err := r.waitUntilIdle(ctx, l7PolicyCreate, loadBalancerID, body.ListenerId, core.UUID{}); err != nil {
			return nil, fmt.Errorf("the parents did not become idle before the creation: %w", err)
		}
		return r.client.CreateL7policy(ctx, loadbalancersdk.CreateL7policyParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating L7 policy", err.Error())
		return
	}

	populateL7PolicyResourceState(policy, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, err := r.waitUntilActive(ctx, policy.Id)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for L7 policy to become active",
			fmt.Sprintf("%s\n\nL7 policy ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, policy.Id),
		)
		return
	}

	populateL7PolicyResourceState(l7PolicyDetailToSchema(detail), &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *L7PolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state L7PolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy, err := r.get(ctx, policyID)
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading L7 policy", err.Error())
		return
	}

	if policy == nil || policy.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateL7PolicyResourceState(l7PolicyDetailToSchema(policy), &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *L7PolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state L7PolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, hasChanges, diags := buildL7PolicyUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hasChanges {
		listenerID, diag := parse.UUIDString(reference.Get(state.Listener, reference.ID), "listener.id")
		resp.Diagnostics.Append(diag...)
		if resp.Diagnostics.HasError() {
			return
		}
		loadBalancerID, err := r.listenerLoadBalancer(ctx, listenerID)
		if err != nil {
			resp.Diagnostics.AddError("Error updating L7 policy", err.Error())
			return
		}
		unlock, err := r.claims.Lock(ctx, loadBalancerID.String())
		if err != nil {
			resp.Diagnostics.AddError("Error updating L7 policy", err.Error())
			return
		}
		defer unlock()

		subject := fmt.Sprintf("the update of L7 policy %s under listener %s of load balancer %s", policyID, listenerID, loadBalancerID)
		_, err = requestLoadBalancerOperation(ctx, subject, func(ctx context.Context) (*loadbalancersdk.L7PolicySchema, error) {
			if err := r.waitUntilIdle(ctx, l7PolicyUpdate, loadBalancerID, listenerID, policyID); err != nil {
				return nil, fmt.Errorf("the L7 policy and its parents did not become idle before the update: %w", err)
			}
			return r.client.PartialUpdateL7policy(ctx, policyID, loadbalancersdk.PartialUpdateL7policyParams{ProjectID: r.projectID}, body)
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating L7 policy", err.Error())
			return
		}

		detail, err := r.waitUntilActive(ctx, policyID)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for L7 policy to settle", err.Error())
			return
		}

		newState := plan
		populateL7PolicyResourceState(l7PolicyDetailToSchema(detail), &newState)
		resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
		return
	}

	detail, err := r.get(ctx, policyID)
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading L7 policy", err.Error())
		return
	}
	if detail == nil || detail.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	newState := plan
	populateL7PolicyResourceState(l7PolicyDetailToSchema(detail), &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *L7PolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state L7PolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	listenerID, diag := parse.UUIDString(reference.Get(state.Listener, reference.ID), "listener.id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}
	loadBalancerID, err := r.listenerLoadBalancer(ctx, listenerID)
	if err != nil {
		// A Load Balancer deletion also removes its listeners and their
		// policies, so a listener without an owner usually means that the
		// policy is gone too. Only a GET of the policy itself confirms that.
		if gone, getErr := r.gone(ctx, policyID); getErr == nil && gone {
			return
		}
		resp.Diagnostics.AddError("Error deleting L7 policy", err.Error())
		return
	}
	unlock, err := r.claims.Lock(ctx, loadBalancerID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting L7 policy", err.Error())
		return
	}
	defer unlock()

	subject := fmt.Sprintf("the deletion of L7 policy %s under listener %s of load balancer %s", policyID, listenerID, loadBalancerID)
	err = requestLoadBalancerVoidOperation(ctx, subject, func(ctx context.Context) error {
		// An L7 policy that is already gone needs no DELETE. The idle wait
		// then wraps ErrNotFound, which Delete reads as a completed deletion.
		if err := r.waitUntilIdle(ctx, l7PolicyDelete, loadBalancerID, listenerID, policyID); err != nil {
			return fmt.Errorf("the L7 policy and its parents did not become idle before the deletion: %w", err)
		}
		return r.client.DeleteL7policy(ctx, policyID, loadbalancersdk.DeleteL7policyParams{ProjectID: r.projectID})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting L7 policy", err.Error())
		return
	}

	if err := r.waitUntilDeleted(ctx, policyID); err != nil {
		resp.Diagnostics.AddError("Error waiting for L7 policy deletion", err.Error())
		return
	}
}

func (r *L7PolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if id == "" || strings.Contains(id, "/") {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected import ID in format \"<l7_policy_id>\", got %q.", req.ID),
		)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(id), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *L7PolicyResource) get(ctx context.Context, id core.UUID) (*loadbalancersdk.L7PolicyDetailSchema, error) {
	return r.client.GetL7policy(ctx, id, loadbalancersdk.GetL7policyParams{
		ProjectID: r.projectID,
	})
}

// waitUntilActive waits until L7 policy id reports ACTIVE after a create or
// an update. UNKNOWN is as final as ERROR: the backend sets it when its event
// watcher gives up, and never leaves it on its own.
func (r *L7PolicyResource) waitUntilActive(
	ctx context.Context,
	id core.UUID,
) (*loadbalancersdk.L7PolicyDetailSchema, error) {
	w := wait.New[*loadbalancersdk.L7PolicyDetailSchema](l7PolicyPollInterval, l7PolicyOperationTimeout)
	obj, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.L7PolicyDetailSchema, error) {
		policy, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if policy == nil {
			return nil, fmt.Errorf("L7 policy %s not found yet: %w", id, wait.ErrNotReady)
		}
		switch policy.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			return policy, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			return nil, fmt.Errorf("L7 policy %s reached terminal provisioning status %s", id, policy.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return nil, fmt.Errorf("L7 policy %s was deleted unexpectedly: %s", id, policy.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			return nil, fmt.Errorf("L7 policy %s is still provisioning (%s): %w", id, policy.ProvisioningStatus, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("L7 policy %s reported an unrecognized provisioning status %q", id, policy.ProvisioningStatus)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("L7 policy %s: %w", id, err)
	}
	return obj, nil
}

// waitUntilDeleted waits until L7 policy id is gone.
//
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, so the wait
// stops. Before PENDING_DELETE it can still be the old state, so the wait goes on.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: the GET hides the delete
// until the infrastructure accepts it, so a refused delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *L7PolicyResource) waitUntilDeleted(
	ctx context.Context,
	id core.UUID,
) error {
	w := wait.New[struct{}](l7PolicyPollInterval, l7PolicyOperationTimeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		policy, err := r.get(ctx, id)
		if err != nil {
			if errors.Is(err, loadbalancersdk.ErrNotFound) {
				return struct{}{}, nil
			}
			return struct{}{}, err
		}
		if policy == nil {
			return struct{}{}, nil
		}
		switch policy.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return struct{}{}, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			pendingDeleteObserved = true
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("L7 policy %s reached terminal provisioning status %s while deleting", id, policy.ProvisioningStatus)
			}
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
		default:
			return struct{}{}, fmt.Errorf("L7 policy %s reported an unrecognized provisioning status %q", id, policy.ProvisioningStatus)
		}
		return struct{}{}, fmt.Errorf("L7 policy %s is still deleting (%s): %w", id, policy.ProvisioningStatus, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("delete L7 policy %s: %w", id, err)
	}
	return nil
}

// waitUntilIdle is the idle wait of an L7 policy operation. It waits until
// every object that the operation claims is idle. Each poll reads
// the policy (Update and Delete only), its listener, and the root Load
// Balancer of that listener.
//
//   - Claimed objects: Create claims the listener and the root Load Balancer.
//     Update and Delete also claim the policy.
//   - Idle state: the listener and the Load Balancer are idle in ACTIVE. For
//     an Update, the policy is idle in ACTIVE. For a Delete, the policy is idle
//     in ACTIVE, ERROR, and UNKNOWN.
//   - Stop at once: a listener or Load Balancer in ERROR or UNKNOWN, or gone;
//     for an Update, a policy in ERROR or UNKNOWN, or gone. UNKNOWN is as final
//     as ERROR, because the backend never leaves it on its own. For a Delete,
//     a policy that is gone ends the wait with an error that wraps
//     loadbalancersdk.ErrNotFound, and Delete then completes without a request.
//     A parent that is gone never wraps ErrNotFound.
//
// The caller holds the claims lock of the root Load Balancer.
func (r *L7PolicyResource) waitUntilIdle(ctx context.Context, op l7PolicyOperation, loadBalancerID, listenerID, policyID core.UUID) error {
	w := wait.New[struct{}](l7PolicyPollInterval, l7PolicyOperationTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		if op != l7PolicyCreate {
			if err := r.policyIdle(ctx, op, policyID); err != nil {
				return struct{}{}, err
			}
		}
		listener, err := r.client.GetListener(ctx, loadBalancerID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
		if err != nil {
			return struct{}{}, l7PolicyParentError("listener", listenerID, err)
		}
		if err := l7PolicyStatusIdle("listener", listenerID, listener.ProvisioningStatus, false); err != nil {
			return struct{}{}, err
		}
		lb, err := r.client.GetLoadBalancer(ctx, loadBalancerID, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if err != nil {
			return struct{}{}, l7PolicyParentError("load balancer", loadBalancerID, err)
		}
		return struct{}{}, l7PolicyStatusIdle("load balancer", loadBalancerID, lb.ProvisioningStatus, false)
	})
	return err
}

// policyIdle reports whether L7 policy id is idle for op, as waitUntilIdle
// describes.
func (r *L7PolicyResource) policyIdle(ctx context.Context, op l7PolicyOperation, id core.UUID) error {
	policy, err := r.get(ctx, id)
	if err != nil && !errors.Is(err, loadbalancersdk.ErrNotFound) {
		return err
	}
	if err != nil || policy == nil || policy.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		if op == l7PolicyDelete {
			return fmt.Errorf("L7 policy %s is gone: %w", id, loadbalancersdk.ErrNotFound)
		}
		return fmt.Errorf("L7 policy %s is gone", id)
	}
	return l7PolicyStatusIdle("L7 policy", id, policy.ProvisioningStatus, op == l7PolicyDelete)
}

// listenerLoadBalancer returns the ID of the root Load Balancer of listener
// listenerID. No L7 policy response carries it, and the API reads a listener
// only under the path of its Load Balancer, so the lookup searches the Load
// Balancers of the project.
func (r *L7PolicyResource) listenerLoadBalancer(ctx context.Context, listenerID core.UUID) (core.UUID, error) {
	id, err := loadbalancerlookup.ListenerLoadBalancer(ctx, r.client, r.projectID, listenerID)
	if err != nil {
		return core.UUID{}, fmt.Errorf("find the load balancer of listener %s: %w", listenerID, err)
	}
	return id, nil
}

// gone reports whether the backend no longer has L7 policy id.
func (r *L7PolicyResource) gone(ctx context.Context, id core.UUID) (bool, error) {
	policy, err := r.get(ctx, id)
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return policy == nil || policy.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED, nil
}

// l7PolicyStatusIdle classifies the provisioning status of one object that an
// L7 policy operation claims. ACTIVE is idle, and PENDING_* keeps the wait
// going. ERROR and UNKNOWN stop the wait, unless deleting is true, which
// counts them as idle too.
func l7PolicyStatusIdle(kind string, id core.UUID, status loadbalancersdk.LoadBalancingProvisioningStatus, deleting bool) error {
	switch status {
	case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
		return nil
	case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
		return fmt.Errorf("%s %s is %s: %w", kind, id, status, wait.ErrNotReady)
	case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
		if deleting {
			return nil
		}
		return fmt.Errorf("%s %s is in terminal provisioning status %s", kind, id, status)
	case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
		return fmt.Errorf("%s %s is gone", kind, id)
	default:
		return fmt.Errorf("%s %s reported an unrecognized provisioning status %q", kind, id, status)
	}
}

// l7PolicyParentError reports a failed read of a parent. A parent that is
// gone fails the operation, so the error does not wrap ErrNotFound, which
// Delete reads as a completed deletion.
func l7PolicyParentError(kind string, id core.UUID, err error) error {
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return fmt.Errorf("%s %s is gone", kind, id)
	}
	return fmt.Errorf("read %s %s: %w", kind, id, err)
}

func buildL7PolicyCreateBody(plan L7PolicyResourceModel) (loadbalancersdk.L7PolicyCreateSchema, diag.Diagnostics) {
	var body loadbalancersdk.L7PolicyCreateSchema
	var diags diag.Diagnostics

	listenerID, diagUUID := parse.UUIDString(reference.Get(plan.Listener, reference.ID), "listener.id")
	diags.Append(diagUUID...)
	if diags.HasError() {
		return body, diags
	}
	body.ListenerId = listenerID

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing L7 policy name", "name must be configured and known.")
		return body, diags
	}
	name := strings.TrimSpace(plan.Name.ValueString())
	body.Name = &name

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		desc := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &desc
	}

	if !plan.Position.IsNull() && !plan.Position.IsUnknown() {
		body.Position.Set(int(plan.Position.ValueInt64()))
	}

	redirect, redirectDiags := buildL7PolicyRedirect(plan)
	diags.Append(redirectDiags...)
	if diags.HasError() {
		return body, diags
	}
	body.Redirect = redirect

	return body, diags
}

// buildL7PolicyRedirect builds the complete redirect variant of the planned
// action, because the API replaces the action and its targets together.
func buildL7PolicyRedirect(plan L7PolicyResourceModel) (loadbalancersdk.L7PolicyRedirect, diag.Diagnostics) {
	var redirect loadbalancersdk.L7PolicyRedirect
	var diags diag.Diagnostics

	if plan.Action.IsNull() || plan.Action.IsUnknown() || strings.TrimSpace(plan.Action.ValueString()) == "" {
		diags.AddError("Missing L7 policy action", "action must be configured and known.")
		return redirect, diags
	}
	action, ok := normalizeL7PolicyAction(plan.Action.ValueString())
	if !ok {
		diags.AddError(
			"Invalid L7 policy action",
			fmt.Sprintf("action %q is not valid. Supported actions: REJECT, REDIRECT_TO_SERVER_GROUP (or REDIRECT_TO_POOL), REDIRECT_TO_URL, REDIRECT_PREFIX.", plan.Action.ValueString()),
		)
		return redirect, diags
	}

	httpCode := l7PolicyDefaultRedirectHTTPCode
	if !plan.RedirectHTTPCode.IsNull() && !plan.RedirectHTTPCode.IsUnknown() {
		httpCode = int(plan.RedirectHTTPCode.ValueInt64())
	}

	var err error
	switch action {
	case loadbalancersdk.L7PolicyActionREJECT:
		err = redirect.FromL7PolicyRedirectReject(loadbalancersdk.L7PolicyRedirectReject{})

	case loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP:
		serverGroup := reference.Get(plan.RedirectServerGroup, reference.ID)
		if serverGroup.IsNull() || serverGroup.IsUnknown() || strings.TrimSpace(serverGroup.ValueString()) == "" {
			diags.AddError(
				"Missing redirect server group",
				"redirect_server_group is required when action is REDIRECT_TO_SERVER_GROUP (or REDIRECT_TO_POOL).",
			)
			return redirect, diags
		}
		serverGroupID, diagUUID := parse.UUIDString(serverGroup, "redirect_server_group.id")
		diags.Append(diagUUID...)
		if diags.HasError() {
			return redirect, diags
		}
		err = redirect.FromL7PolicyRedirectServerGroup(loadbalancersdk.L7PolicyRedirectServerGroup{
			RedirectServerGroupId: serverGroupID,
		})

	case loadbalancersdk.L7PolicyActionREDIRECTTOURL:
		if plan.RedirectURL.IsNull() || plan.RedirectURL.IsUnknown() || strings.TrimSpace(plan.RedirectURL.ValueString()) == "" {
			diags.AddError("Missing redirect URL", "redirect_url is required when action is REDIRECT_TO_URL.")
			return redirect, diags
		}
		err = redirect.FromL7PolicyRedirectURL(loadbalancersdk.L7PolicyRedirectURL{
			RedirectUrl:      strings.TrimSpace(plan.RedirectURL.ValueString()),
			RedirectHttpCode: httpCode,
		})

	case loadbalancersdk.L7PolicyActionREDIRECTPREFIX:
		if plan.RedirectPrefix.IsNull() || plan.RedirectPrefix.IsUnknown() || strings.TrimSpace(plan.RedirectPrefix.ValueString()) == "" {
			diags.AddError("Missing redirect prefix", "redirect_prefix is required when action is REDIRECT_PREFIX.")
			return redirect, diags
		}
		err = redirect.FromL7PolicyRedirectPrefix(loadbalancersdk.L7PolicyRedirectPrefix{
			RedirectPrefix:   strings.TrimSpace(plan.RedirectPrefix.ValueString()),
			RedirectHttpCode: httpCode,
		})
	}
	if err != nil {
		diags.AddError("Error encoding redirect", err.Error())
	}
	return redirect, diags
}

func buildL7PolicyUpdateBody(
	plan L7PolicyResourceModel,
	state L7PolicyResourceModel,
) (loadbalancersdk.L7PolicyPartialUpdateSchema, bool, diag.Diagnostics) {
	var body loadbalancersdk.L7PolicyPartialUpdateSchema
	var diags diag.Diagnostics
	changed := false

	if !sameL7PolicyTrimmedString(plan.Name, state.Name) {
		if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
			diags.AddError("Missing L7 policy name", "name must be configured and known.")
			return body, false, diags
		}
		body.Name.Set(strings.TrimSpace(plan.Name.ValueString()))
		changed = true
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !sameL7PolicyTrimmedString(plan.Description, state.Description) {
		body.Description.Set(strings.TrimSpace(plan.Description.ValueString()))
		changed = true
	}

	if !plan.Position.Equal(state.Position) {
		if !plan.Position.IsNull() && !plan.Position.IsUnknown() {
			pos := int(plan.Position.ValueInt64())
			body.Position = &pos
			changed = true
		}
	}

	redirectChanged := !plan.Action.Equal(state.Action) ||
		!reference.Get(plan.RedirectServerGroup, reference.ID).Equal(reference.Get(state.RedirectServerGroup, reference.ID)) ||
		!sameL7PolicyTrimmedString(plan.RedirectURL, state.RedirectURL) ||
		!sameL7PolicyTrimmedString(plan.RedirectPrefix, state.RedirectPrefix) ||
		(!plan.RedirectHTTPCode.IsUnknown() && !plan.RedirectHTTPCode.Equal(state.RedirectHTTPCode))

	if redirectChanged {
		redirect, redirectDiags := buildL7PolicyRedirect(plan)
		diags.Append(redirectDiags...)
		if diags.HasError() {
			return body, false, diags
		}
		body.Redirect = &redirect
		changed = true
	}

	return body, changed, diags
}

// sameL7PolicyTrimmedString reports whether plan and state hold the same
// value once outer whitespace is removed. The API trims name, description,
// redirect_url, and redirect_prefix, so a change of outer whitespace alone
// sends no update.
func sameL7PolicyTrimmedString(plan, state types.String) bool {
	if plan.IsNull() || plan.IsUnknown() || state.IsNull() || state.IsUnknown() {
		return plan.Equal(state)
	}
	return strings.TrimSpace(plan.ValueString()) == strings.TrimSpace(state.ValueString())
}

// normalizeL7PolicyAction returns the API action for a configured action,
// ignoring case and surrounding spaces and resolving REDIRECT_TO_POOL.
func normalizeL7PolicyAction(configured string) (loadbalancersdk.L7PolicyAction, bool) {
	action := strings.ToUpper(strings.TrimSpace(configured))
	if action == l7PolicyActionPoolAlias {
		return loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP, true
	}
	switch loadbalancersdk.L7PolicyAction(action) {
	case loadbalancersdk.L7PolicyActionREJECT,
		loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP,
		loadbalancersdk.L7PolicyActionREDIRECTTOURL,
		loadbalancersdk.L7PolicyActionREDIRECTPREFIX:
		return loadbalancersdk.L7PolicyAction(action), true
	}
	return "", false
}

// l7PolicyRedirectHTTPCodePlan plans an omitted redirect_http_code from the
// planned action. An action without a code plans null. A URL or prefix
// redirect keeps the code already in effect, or plans 302 when the prior
// action had none.
type l7PolicyRedirectHTTPCodePlan struct{}

func (l7PolicyRedirectHTTPCodePlan) Description(context.Context) string {
	return "Plans the effective redirect HTTP code of the planned action when the code is omitted."
}

func (m l7PolicyRedirectHTTPCodePlan) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (l7PolicyRedirectHTTPCodePlan) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if !req.ConfigValue.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var planned types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("action"), &planned)...)
	if resp.Diagnostics.HasError() || planned.IsNull() || planned.IsUnknown() {
		return
	}
	action, ok := normalizeL7PolicyAction(planned.ValueString())
	if !ok {
		return
	}
	if action != loadbalancersdk.L7PolicyActionREDIRECTTOURL && action != loadbalancersdk.L7PolicyActionREDIRECTPREFIX {
		resp.PlanValue = types.Int64Null()
		return
	}

	resp.PlanValue = types.Int64Value(l7PolicyDefaultRedirectHTTPCode)
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}
	var prior types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("action"), &prior)...)
	if resp.Diagnostics.HasError() || prior.IsNull() || prior.IsUnknown() {
		return
	}
	if priorAction, ok := normalizeL7PolicyAction(prior.ValueString()); ok &&
		(priorAction == loadbalancersdk.L7PolicyActionREDIRECTTOURL || priorAction == loadbalancersdk.L7PolicyActionREDIRECTPREFIX) {
		resp.PlanValue = req.StateValue
	}
}
