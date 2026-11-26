package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
	_ resource.Resource                   = &L7RuleResource{}
	_ resource.ResourceWithImportState    = &L7RuleResource{}
	_ resource.ResourceWithValidateConfig = &L7RuleResource{}
)

const (
	l7RuleReadyTimeout      = 30 * time.Minute
	l7RuleReadyPollInterval = 5 * time.Second
)

type L7RuleResource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

// l7RuleParents holds the objects above an L7 rule, up to its root Load
// Balancer. Every L7 rule operation claims all three.
type l7RuleParents struct {
	policyID       core.UUID
	listenerID     core.UUID
	loadBalancerID core.UUID
}

func NewL7RuleResource() resource.Resource {
	return &L7RuleResource{}
}

func (r *L7RuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_l7_rule"
}

func (r *L7RuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer L7 Rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "L7 Rule ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"l7_policy": schema.SingleNestedAttribute{
				Required:    true,
				Description: "L7 policy that owns the L7 rule.",
				Attributes:  reference.Attributes(reference.ID, "L7 policy ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "Rule type: HOST_NAME, PATH, FILE_TYPE, HEADER, or COOKIE.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.L7RuleType](),
				},
			},
			"compare_type": schema.StringAttribute{
				Required:    true,
				Description: "Comparison type: CONTAINS, ENDS_WITH, EQUAL_TO, REGEX, or STARTS_WITH.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.L7RuleCompareType](),
				},
			},
			"value": schema.StringAttribute{
				Required:    true,
				Description: "Value to compare against.",
			},
			"key": schema.StringAttribute{
				Optional:    true,
				Description: "Key to match against. Required when type is HEADER or COOKIE.",
			},
			"invert": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Whether to invert the rule match. Defaults to false.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status of the L7 rule.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status of the L7 rule.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 rule was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 rule was last updated (RFC3339).",
			},
		},
	}
}

func (r *L7RuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *L7RuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config L7RuleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Type.IsNull() && !config.Type.IsUnknown() {
		ruleType := loadbalancersdk.L7RuleType(strings.TrimSpace(config.Type.ValueString()))
		if ruleType == loadbalancersdk.L7RuleTypeHEADER || ruleType == loadbalancersdk.L7RuleTypeCOOKIE {
			if config.Key.IsNull() || (!config.Key.IsUnknown() && strings.TrimSpace(config.Key.ValueString()) == "") {
				resp.Diagnostics.AddAttributeError(
					path.Root("key"),
					"Missing key for rule type",
					fmt.Sprintf("key is required when type is %s.", ruleType),
				)
			}
		} else if !config.Key.IsNull() {
			resp.Diagnostics.AddAttributeError(
				path.Root("key"),
				"Invalid key for rule type",
				fmt.Sprintf("key cannot be specified when type is %s.", ruleType),
			)
		}
	}
}

func (r *L7RuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan L7RuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, policyDiags := parse.UUIDString(reference.Get(plan.L7Policy, reference.ID), "l7_policy.id")
	resp.Diagnostics.Append(policyDiags...)
	body, bodyDiags := buildL7RuleCreateBody(plan)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	parents, err := r.parents(ctx, policyID)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer L7 Rule", err.Error())
		return
	}
	unlock, err := r.claims.Lock(ctx, parents.loadBalancerID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer L7 Rule", err.Error())
		return
	}
	defer unlock()

	rule, err := requestLoadBalancerOperation(ctx, fmt.Sprintf("the creation of an L7 rule under L7 policy %s", policyID), func(ctx context.Context) (*loadbalancersdk.L7RuleSchema, error) {
		if err := r.waitUntilIdle(ctx, parents, nil, l7RuleReadyTimeout, l7RuleReadyPollInterval); err != nil {
			return nil, err
		}
		return r.client.CreateL7rule(ctx, policyID, loadbalancersdk.CreateL7ruleParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer L7 Rule", err.Error())
		return
	}

	populateL7RuleResourceState(rule, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err = r.waitUntilReady(ctx, policyID, rule.Id, l7RuleReadyTimeout, l7RuleReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for Load Balancer L7 Rule to be ready",
			fmt.Sprintf("%s\n\nL7 Rule ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, plan.ID.ValueString()),
		)
		return
	}

	populateL7RuleResourceState(rule, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *L7RuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state L7RuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, policyDiags := parse.UUIDString(reference.Get(state.L7Policy, reference.ID), "l7_policy.id")
	resp.Diagnostics.Append(policyDiags...)
	ruleID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Load Balancer L7 Rule", err.Error())
		return
	}

	if rule.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateL7RuleResourceState(rule, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *L7RuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state L7RuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, policyDiags := parse.UUIDString(reference.Get(state.L7Policy, reference.ID), "l7_policy.id")
	resp.Diagnostics.Append(policyDiags...)
	ruleID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, hasChanges, bodyDiags := buildL7RulePartialUpdateBody(plan, state)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var detail *loadbalancersdk.L7RuleSchema
	var err error

	if hasChanges {
		var parents l7RuleParents
		parents, err = r.parents(ctx, policyID)
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer L7 Rule", err.Error())
			return
		}
		var unlock func()
		unlock, err = r.claims.Lock(ctx, parents.loadBalancerID.String())
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer L7 Rule", err.Error())
			return
		}
		defer unlock()

		detail, err = requestLoadBalancerOperation(ctx, fmt.Sprintf("the update of L7 rule %s", ruleID), func(ctx context.Context) (*loadbalancersdk.L7RuleSchema, error) {
			if err := r.waitUntilIdle(ctx, parents, &ruleID, l7RuleReadyTimeout, l7RuleReadyPollInterval); err != nil {
				return nil, err
			}
			return r.client.PartialUpdateL7rule(ctx, policyID, ruleID, loadbalancersdk.PartialUpdateL7ruleParams{ProjectID: r.projectID}, body)
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer L7 Rule", err.Error())
			return
		}
		if detail.Id != ruleID {
			resp.Diagnostics.AddError("Error updating Load Balancer L7 Rule", fmt.Sprintf("The API answered the update of L7 Rule %s with L7 Rule %s, so the update was not applied in place.", ruleID, detail.Id))
			return
		}

		detail, err = r.waitUntilReady(ctx, policyID, ruleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for Load Balancer L7 Rule update", err.Error())
			return
		}
	} else {
		detail, err = r.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer L7 Rule", err.Error())
			return
		}
	}

	newState := plan
	populateL7RuleResourceState(detail, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *L7RuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state L7RuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyID, policyDiags := parse.UUIDString(reference.Get(state.L7Policy, reference.ID), "l7_policy.id")
	resp.Diagnostics.Append(policyDiags...)
	ruleID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	parents, err := r.parents(ctx, policyID)
	if err != nil {
		// A parent that cannot be read does not prove that the L7 rule is
		// gone. Only the GET of the L7 rule itself can show that.
		rule, getErr := r.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
		if errors.Is(getErr, loadbalancersdk.ErrNotFound) || getErr == nil && rule.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
			return
		}
		resp.Diagnostics.AddError("Error deleting Load Balancer L7 Rule", err.Error())
		return
	}
	unlock, err := r.claims.Lock(ctx, parents.loadBalancerID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer L7 Rule", err.Error())
		return
	}
	defer unlock()

	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the deletion of L7 rule %s", ruleID), func(ctx context.Context) error {
		gone, err := r.waitUntilDeletable(ctx, parents, ruleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
		if err != nil {
			return err
		}
		// An L7 rule that is already gone needs no DELETE, and Delete treats
		// ErrNotFound as a completed deletion.
		if gone {
			return fmt.Errorf("L7 rule %s is gone: %w", ruleID, loadbalancersdk.ErrNotFound)
		}
		return r.client.DeleteL7rule(ctx, policyID, ruleID, loadbalancersdk.DeleteL7ruleParams{ProjectID: r.projectID})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer L7 Rule", err.Error())
		return
	}

	if err := r.waitUntilDeleted(ctx, policyID, ruleID, l7RuleReadyTimeout, l7RuleReadyPollInterval); err != nil {
		resp.Diagnostics.AddError("Error waiting for Load Balancer L7 Rule deletion", err.Error())
	}
}

func (r *L7RuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(strings.TrimSpace(req.ID), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected import ID in format \"<l7_policy_id>/<l7_rule_id>\", got %q.", req.ID),
		)
		return
	}

	policyID := strings.TrimSpace(parts[0])
	ruleID := strings.TrimSpace(parts[1])

	if _, diags := parse.UUIDString(types.StringValue(policyID), "l7_policy.id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(ruleID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("l7_policy"), reference.Value(reference.ID, types.StringValue(policyID)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ruleID)...)
}

// parents reads L7 policy policyID and returns it with its listener and root
// Load Balancer. The API records no Load Balancer on an L7 policy or on a
// listener path, so the root comes from loadbalancerlookup.ListenerLoadBalancer.
func (r *L7RuleResource) parents(ctx context.Context, policyID core.UUID) (l7RuleParents, error) {
	policy, err := r.client.GetL7policy(ctx, policyID, loadbalancersdk.GetL7policyParams{ProjectID: r.projectID})
	if err != nil {
		return l7RuleParents{}, fmt.Errorf("read L7 policy %s: %w", policyID, err)
	}
	loadBalancerID, err := loadbalancerlookup.ListenerLoadBalancer(ctx, r.client, r.projectID, policy.ListenerId)
	if err != nil {
		return l7RuleParents{}, fmt.Errorf("find the load balancer of L7 policy %s: %w", policyID, err)
	}
	return l7RuleParents{policyID: policyID, listenerID: policy.ListenerId, loadBalancerID: loadBalancerID}, nil
}

// waitUntilIdle is the idle wait before a POST or PATCH of an L7 rule.
//
// Both operations claim the L7 policy, the listener, and the root Load
// Balancer, and a PATCH also claims L7 rule ruleID. Pass a nil ruleID for a
// POST. The response shows a held claim as PENDING_CREATE, PENDING_UPDATE, or
// PENDING_DELETE, so the wait goes on in these states. ACTIVE is the idle
// state of each object. ERROR, UNKNOWN, DELETED, an unrecognized state, and a
// missing object fail at once. The caller holds the claims lock of the root
// Load Balancer from this wait to the end of the ready wait.
func (r *L7RuleResource) waitUntilIdle(ctx context.Context, parents l7RuleParents, ruleID *core.UUID, timeout, pollInterval time.Duration) error {
	w := wait.New[struct{}](pollInterval, timeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		if ruleID != nil {
			rule, err := r.client.GetL7rule(ctx, parents.policyID, *ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
			if err != nil {
				return struct{}{}, fmt.Errorf("L7 rule %s: %w", *ruleID, err)
			}
			if err := l7RuleObjectActive("L7 rule", *ruleID, rule.ProvisioningStatus); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, r.parentsActive(ctx, parents)
	})
	if err != nil {
		if ruleID != nil {
			return fmt.Errorf("wait for L7 rule %s and its parents to become idle: %w", *ruleID, err)
		}
		return fmt.Errorf("wait for L7 policy %s and its parents to become idle: %w", parents.policyID, err)
	}
	return nil
}

// waitUntilDeletable is the idle wait before a DELETE of L7 rule ruleID, and
// reports whether the L7 rule is already gone.
//
// A DELETE claims the L7 rule, the L7 policy, the listener, and the root Load
// Balancer. The response shows a held claim on the L7 rule as PENDING_CREATE,
// PENDING_UPDATE, or PENDING_DELETE, so the wait goes on in these states.
// ACTIVE, ERROR, and UNKNOWN count as idle for the L7 rule, and Delete sends
// the DELETE in each of them. Each parent must be ACTIVE, and a parent in
// ERROR or UNKNOWN fails at once, as does an unrecognized state of the L7
// rule. Delete holds the claims lock of the root Load Balancer from this wait
// to the end of the delete wait.
func (r *L7RuleResource) waitUntilDeletable(ctx context.Context, parents l7RuleParents, ruleID core.UUID, timeout, pollInterval time.Duration) (bool, error) {
	w := wait.New[bool](pollInterval, timeout)
	gone, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		rule, err := r.client.GetL7rule(ctx, parents.policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("L7 rule %s: %w", ruleID, err)
		}
		switch rule.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return true, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			return false, r.parentsActive(ctx, parents)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return false, fmt.Errorf("L7 rule %s provisioning_status=%s: %w", ruleID, rule.ProvisioningStatus, wait.ErrNotReady)
		default:
			return false, fmt.Errorf("L7 rule %s has unrecognized provisioning_status=%s", ruleID, rule.ProvisioningStatus)
		}
	})
	if err != nil {
		return false, fmt.Errorf("wait for L7 rule %s to become deletable: %w", ruleID, err)
	}
	return gone, nil
}

// parentsActive reads the L7 policy, the listener, and the root Load Balancer
// in parents once, and returns nil only when all three are ACTIVE.
//
// A read error of a parent is returned as text, not wrapped. Delete treats a
// wrapped ErrNotFound as a deleted L7 rule, and a missing parent does not
// prove that.
func (r *L7RuleResource) parentsActive(ctx context.Context, parents l7RuleParents) error {
	policy, err := r.client.GetL7policy(ctx, parents.policyID, loadbalancersdk.GetL7policyParams{ProjectID: r.projectID})
	if err != nil {
		return fmt.Errorf("L7 policy %s: %v", parents.policyID, err)
	}
	if err := l7RuleObjectActive("L7 policy", parents.policyID, policy.ProvisioningStatus); err != nil {
		return err
	}
	listener, err := r.client.GetListener(ctx, parents.loadBalancerID, parents.listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
	if err != nil {
		return fmt.Errorf("listener %s: %v", parents.listenerID, err)
	}
	if err := l7RuleObjectActive("listener", parents.listenerID, listener.ProvisioningStatus); err != nil {
		return err
	}
	lb, err := r.client.GetLoadBalancer(ctx, parents.loadBalancerID, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
	if err != nil {
		return fmt.Errorf("load balancer %s: %v", parents.loadBalancerID, err)
	}
	return l7RuleObjectActive("load balancer", parents.loadBalancerID, lb.ProvisioningStatus)
}

// waitUntilReady waits until L7 rule ruleID reports ACTIVE after a POST or
// PATCH, and fails at once on ERROR, UNKNOWN, DELETED, or an unrecognized
// state.
func (r *L7RuleResource) waitUntilReady(
	ctx context.Context,
	policyID core.UUID,
	ruleID core.UUID,
	timeout, pollInterval time.Duration,
) (*loadbalancersdk.L7RuleSchema, error) {
	w := wait.New[*loadbalancersdk.L7RuleSchema](pollInterval, timeout)
	rule, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.L7RuleSchema, error) {
		detail, err := r.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
		if err != nil {
			return nil, err
		}
		switch detail.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			return detail, nil
		// The backend sets UNKNOWN when its event watcher gives up, and never
		// leaves it on its own, so UNKNOWN is as final as ERROR.
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			return nil, fmt.Errorf("L7 rule entered %s provisioning status", detail.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return nil, fmt.Errorf("L7 rule was unexpectedly deleted")
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return nil, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("unrecognized provisioning_status=%s", detail.ProvisioningStatus)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("L7 rule %s: %w", ruleID, err)
	}
	return rule, nil
}

// waitUntilDeleted waits until L7 rule ruleID is gone.
//
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, so the wait
// stops. Before PENDING_DELETE it can still be the old state, so the wait goes on.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: the GET hides the delete
// until the infrastructure accepts it, so a refused delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *L7RuleResource) waitUntilDeleted(
	ctx context.Context,
	policyID core.UUID,
	ruleID core.UUID,
	timeout, pollInterval time.Duration,
) error {
	w := wait.New[struct{}](pollInterval, timeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		detail, err := r.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		switch detail.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return struct{}{}, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			pendingDeleteObserved = true
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("L7 rule deletion failed with provisioning_status=%s", detail.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		default:
			return struct{}{}, fmt.Errorf("L7 rule deletion failed with provisioning_status=%s", detail.ProvisioningStatus)
		}
	})
	if err != nil {
		return fmt.Errorf("L7 rule %s deletion: %w", ruleID, err)
	}
	return nil
}

func buildL7RuleCreateBody(plan L7RuleResourceModel) (loadbalancersdk.L7RuleCreateSchema, diag.Diagnostics) {
	var body loadbalancersdk.L7RuleCreateSchema
	var diags diag.Diagnostics

	if plan.Type.IsNull() || plan.Type.IsUnknown() || strings.TrimSpace(plan.Type.ValueString()) == "" {
		diags.AddError("Missing rule type", "type must be configured, known, and non-empty.")
	} else {
		body.Type = loadbalancersdk.L7RuleType(strings.TrimSpace(plan.Type.ValueString()))
	}

	if plan.CompareType.IsNull() || plan.CompareType.IsUnknown() || strings.TrimSpace(plan.CompareType.ValueString()) == "" {
		diags.AddError("Missing rule compare type", "compare_type must be configured, known, and non-empty.")
	} else {
		body.CompareType = loadbalancersdk.L7RuleCompareType(strings.TrimSpace(plan.CompareType.ValueString()))
	}

	if plan.Value.IsNull() || plan.Value.IsUnknown() {
		diags.AddError("Missing rule value", "value must be configured and known.")
	} else {
		body.Value = strings.TrimSpace(plan.Value.ValueString())
	}

	if !plan.Invert.IsNull() && !plan.Invert.IsUnknown() {
		invert := plan.Invert.ValueBool()
		body.Invert = &invert
	}

	if !plan.Key.IsNull() && !plan.Key.IsUnknown() && strings.TrimSpace(plan.Key.ValueString()) != "" {
		key := strings.TrimSpace(plan.Key.ValueString())
		body.Key.Set(key)
	} else if body.Type == loadbalancersdk.L7RuleTypeHEADER || body.Type == loadbalancersdk.L7RuleTypeCOOKIE {
		diags.AddError("Missing key for rule type", fmt.Sprintf("key is required when type is %s.", body.Type))
	}

	return body, diags
}

// buildL7RulePartialUpdateBody sends only the changed fields. A type change
// also sends compare_type and value, because the backend validates them
// against the new type.
//
// A type change also sends key: the planned key for HEADER and COOKIE, and
// null for every other type. The backend validates the stored key against the
// new type before it saves, so a PATCH without key answers HTTP 400 "Field
// key must not be set when type is PATH." (Q8 of
// design/loadbalancer/l7_rule.md).
func buildL7RulePartialUpdateBody(plan, state L7RuleResourceModel) (loadbalancersdk.L7RulePartialUpdateSchema, bool, diag.Diagnostics) {
	var body loadbalancersdk.L7RulePartialUpdateSchema
	var diags diag.Diagnostics
	var hasChanges bool

	typeChanged := !sameL7RuleString(plan.Type, state.Type)
	ruleType := loadbalancersdk.L7RuleType(strings.TrimSpace(plan.Type.ValueString()))
	if typeChanged {
		if plan.Type.IsNull() || plan.Type.IsUnknown() || ruleType == "" {
			diags.AddError("Invalid rule type", "type must be known and non-empty.")
		} else {
			body.Type = &ruleType
			hasChanges = true
		}
	}

	if typeChanged || !sameL7RuleString(plan.CompareType, state.CompareType) {
		if plan.CompareType.IsNull() || plan.CompareType.IsUnknown() || strings.TrimSpace(plan.CompareType.ValueString()) == "" {
			diags.AddError("Invalid rule compare type", "compare_type must be known and non-empty.")
		} else {
			compareType := loadbalancersdk.L7RuleCompareType(strings.TrimSpace(plan.CompareType.ValueString()))
			body.CompareType = &compareType
			hasChanges = true
		}
	}

	if typeChanged || !sameL7RuleString(plan.Value, state.Value) {
		if plan.Value.IsNull() || plan.Value.IsUnknown() {
			diags.AddError("Invalid rule value", "value must be known.")
		} else {
			val := strings.TrimSpace(plan.Value.ValueString())
			body.Value = &val
			hasChanges = true
		}
	}

	if !plan.Invert.Equal(state.Invert) {
		if plan.Invert.IsUnknown() {
			diags.AddError("Invalid rule invert", "invert must be known.")
		} else {
			invert := plan.Invert.ValueBool()
			body.Invert = &invert
			hasChanges = true
		}
	}

	usesKey := ruleType == loadbalancersdk.L7RuleTypeHEADER || ruleType == loadbalancersdk.L7RuleTypeCOOKIE
	if !usesKey && typeChanged {
		body.Key.SetNull()
	}
	if usesKey && (typeChanged || !sameL7RuleString(plan.Key, state.Key)) {
		if plan.Key.IsNull() || plan.Key.IsUnknown() || strings.TrimSpace(plan.Key.ValueString()) == "" {
			diags.AddError("Missing key for rule type", fmt.Sprintf("key is required when type is %s.", ruleType))
		} else {
			key := strings.TrimSpace(plan.Key.ValueString())
			body.Key.Set(key)
			hasChanges = true
		}
	}

	return body, hasChanges, diags
}

// sameL7RuleString reports whether a planned string equals the prior state
// value after the trim. The API trims every string of an L7 rule, so a change
// of outer whitespace alone is not a change.
func sameL7RuleString(plan, state types.String) bool {
	return plan.Equal(state) || compare.SameTrimmedString(plan, state)
}

// l7RuleObjectActive classifies one provisioning status of an object that an
// L7 rule operation claims. ACTIVE is idle and returns nil. PENDING_* returns
// an error that wraps wait.ErrNotReady. ERROR, UNKNOWN, DELETED, and an
// unrecognized state return a terminal error.
func l7RuleObjectActive(kind string, id core.UUID, status loadbalancersdk.LoadBalancingProvisioningStatus) error {
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
		return fmt.Errorf("%s %s has terminal provisioning_status=%s", kind, id, status)
	default:
		return fmt.Errorf("%s %s has unrecognized provisioning_status=%s", kind, id, status)
	}
}
