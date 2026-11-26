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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                = &LoadBalancerResource{}
	_ resource.ResourceWithImportState = &LoadBalancerResource{}
	_ resource.ResourceWithModifyPlan  = &LoadBalancerResource{}
)

const (
	loadBalancerReadyTimeout      = 30 * time.Minute
	loadBalancerReadyPollInterval = 5 * time.Second
	// resourceBusyErrorCode identifies a refusal because another operation
	// holds the claim of the resource. It clears when that operation finishes.
	resourceBusyErrorCode = "resource_busy"
	// loadBalancerDualStackToIPv4OnlyText explains why a dual-stack Load
	// Balancer cannot use ipv4-only, and what to configure instead.
	loadBalancerDualStackToIPv4OnlyText = `A dual-stack Load Balancer is on a dual-stack region, which does not assign a public IPv4 address alone, so it cannot use public_ip_mode "ipv4-only". Keep "dual-stack", or remove public_ip_mode to turn public IP off.`
)

type LoadBalancerResource struct {
	client    *loadbalancersdk.Client
	project   *projectsdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

func NewLoadBalancerResource() resource.Resource {
	return &LoadBalancerResource{}
}

func (r *LoadBalancerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_loadbalancer"
}

func (r *LoadBalancerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Load Balancer ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Load Balancer.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description. Omission preserves the backend value. Set an empty string to clear it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"package": schema.StringAttribute{
				Required:    true,
				Description: "Load Balancer package: SMALL, MEDIUM, or LARGE.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.LoadBalancerPackage](),
				},
			},
			"topology": schema.StringAttribute{
				Required:    true,
				Description: "Load Balancer topology: SINGLE or ACTIVE_STANDBY.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.LoadBalancerTopology](),
				},
			},
			"type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Load Balancer type: appliance or native. When omitted during creation, the backend uses appliance. Later omission keeps the backend value. Changing a configured type replaces the Load Balancer.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.LoadBalancerType](),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					replace.StringIfConfigured(compare.SameTrimmedString),
				},
			},
			"region": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Project region where the Load Balancer is created. Changing the region replaces the Load Balancer.",
				Attributes:  reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.Name, compare.SameTrimmedString),
				},
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID).",
			},
			"vpc": schema.SingleNestedAttribute{
				Required:    true,
				Description: "VPC where the Load Balancer is created. Changing the VPC replaces the Load Balancer.",
				Attributes:  reference.Attributes(reference.ID, "VPC ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"vpc_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the VPC containing the Load Balancer.",
			},
			"private_ip": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Private IP to use as the VIP. When omitted during creation, the backend allocates one and reports it in `private_ip_id`. Later omission keeps the Load Balancer. Configuring a Private IP other than `private_ip_id` replaces the Load Balancer.",
				Attributes:  reference.Attributes(reference.ID, "Private IP ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					requiresReplaceUnlessAssociated("private_ip_id"),
				},
			},
			"private_ip_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID (UUID) of the Private IP that holds the VIP. Null when the API reports no association.",
			},
			"qos_policy": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "QoS policy assigned during creation. Later omission keeps the Load Balancer. Configuring a QoS policy other than `qos_policy_id` replaces the Load Balancer.",
				Attributes:  reference.Attributes(reference.ID, "QoS policy ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					requiresReplaceUnlessAssociated("qos_policy_id"),
				},
			},
			"qos_policy_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID (UUID) of the assigned QoS policy. Null when the API reports no association.",
			},
			"qos_policy_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the assigned QoS policy.",
			},
			"vip_address": schema.StringAttribute{
				Computed:    true,
				Description: "Private VIP address.",
			},
			"public_ip_mode": schema.StringAttribute{
				Optional:    true,
				Description: "Public IP mode of the Load Balancer: `ipv4-only` assigns a public IPv4 address, and `dual-stack` assigns a public IPv4 and a public IPv6 address. Set the mode that the region supports. Omitting it turns public IP off, also on a Load Balancer whose public IP was assigned outside Terraform or before this attribute existed, so configure the current mode to keep an existing public IP, including after import. Changes apply in place without replacing the Load Balancer. A `dual-stack` Load Balancer cannot change to `ipv4-only` unless the region changes too.",
				Validators: []validator.String{
					trimmedSDKEnum[loadbalancersdk.LoadBalancerPublicIPMode](),
				},
			},
			"public_address": schema.StringAttribute{
				Computed:    true,
				Description: "Public IPv4 address. Null when the Load Balancer has no public IPv4 address.",
			},
			"v6_public_address": schema.StringAttribute{
				Computed:    true,
				Description: "Public IPv6 address. Null when the Load Balancer has no public IPv6 address.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status.",
			},
			"access_log_enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether access logging is enabled.",
			},
			"access_log_store_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID (UUID) of the access log store. Null when the Load Balancer has no access log store.",
			},
			"access_log_store_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the access log store. Null when the Load Balancer has no access log store.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Load Balancer was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Load Balancer was last updated (RFC3339).",
			},
		},
	}
}

func (r *LoadBalancerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.LoadBalancer
	r.project = data.Project
	r.projectID = data.ProjectID
	r.claims = data.Claims
}

// ModifyPlan rejects a change of public_ip_mode from dual-stack to ipv4-only
// on the same region. A dual-stack Load Balancer is on a dual-stack region,
// which never assigns a public IPv4 address alone, and the backend refuses
// ipv4-only while a public address exists. The check also covers a plan that
// replaces the Load Balancer in the same region, because the new one is on the
// same region. Only a region change, the replacement condition of region, lets
// the plan through. The plan carries no replacement paths of attribute
// modifiers here, so the check compares region itself. An unknown region or
// mode skips the check, and Update repeats it before any request.
func (r *LoadBalancerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state LoadBalancerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.PublicIPMode.IsUnknown() || !plan.Region.Equal(state.Region) {
		return
	}
	resp.Diagnostics.Append(loadBalancerPublicIPModeTransitionDiagnostics(state.PublicIPMode, plan.PublicIPMode)...)
}

func (r *LoadBalancerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan LoadBalancerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regionFinder := projectlookup.NewRegionFinder(r.project, r.projectID)
	body, diags := buildLoadBalancerCreateBody(ctx, plan, regionFinder.Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	lb, err := r.client.CreateLoadBalancer(ctx, loadbalancersdk.CreateLoadBalancerParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer", err.Error())
		return
	}

	// The create response can lack associations that the backend reports
	// only once the Load Balancer is ACTIVE. Map it into a copy, so the final
	// mapping still compares the backend with the configured references.
	created := plan
	populateLoadBalancerResourceState(lb, &created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &created)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lb, err = r.waitUntilReady(ctx, lb.Id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for Load Balancer to be ready",
			fmt.Sprintf("%s\n\nLoad Balancer ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, created.ID.ValueString()),
		)
		return
	}

	configuredPrivateIPID := reference.Get(plan.PrivateIP, reference.ID)
	configuredQoSPolicyID := reference.Get(plan.QoSPolicy, reference.ID)
	configuredPublicIPMode := plan.PublicIPMode
	populateLoadBalancerResourceState(lb, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	resp.Diagnostics.Append(loadBalancerAssociationDiagnostics("private_ip", configuredPrivateIPID, core.NullableToPtr(lb.PrivateIpId))...)
	var appliedQoSPolicyID *core.UUID
	if qosPolicy := core.NullableToPtr(lb.QosPolicy); qosPolicy != nil {
		appliedQoSPolicyID = &qosPolicy.Id
	}
	resp.Diagnostics.Append(loadBalancerAssociationDiagnostics("qos_policy", configuredQoSPolicyID, appliedQoSPolicyID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The create body has no public IP field, so a configured mode needs a
	// second request on the ACTIVE Load Balancer.
	if !configuredPublicIPMode.IsNull() {
		unlock, err := r.claims.Lock(ctx, lb.Id.String())
		if err != nil {
			resp.Diagnostics.AddError("Error configuring Load Balancer public IP", err.Error())
			return
		}
		defer unlock()
		lb, err = r.configurePublicIPMode(ctx, lb.Id, configuredPublicIPMode)
		if lb != nil {
			// The state write after the POST holds the derived mode. The
			// configured spelling stays when it names the reported mode.
			plan.PublicIPMode = configuredPublicIPMode
			populateLoadBalancerResourceState(lb, &plan)
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		// State already holds Load Balancer plan.ID from the write after the
		// POST, so a failed public IP step leaves it tracked, and Terraform
		// taints it.
		if err != nil {
			resp.Diagnostics.AddError(
				"Error configuring Load Balancer public IP",
				fmt.Sprintf("%s\n\nLoad Balancer ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, plan.ID.ValueString()),
			)
			return
		}
	}
	resp.Diagnostics.Append(loadBalancerPublicIPModeDiagnostics(configuredPublicIPMode, plan.PublicIPMode)...)
}

func (r *LoadBalancerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state LoadBalancerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	lb, err := r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Load Balancer", err.Error())
		return
	}
	if lb.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateLoadBalancerResourceState(lb, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *LoadBalancerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan LoadBalancerResourceModel
	var state LoadBalancerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// ModifyPlan skips this check while region or public_ip_mode is unknown,
	// so Update repeats it before any request.
	resp.Diagnostics.Append(loadBalancerPublicIPModeTransitionDiagnostics(state.PublicIPMode, plan.PublicIPMode)...)
	body, changed, diags := buildLoadBalancerUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	publicIPModeChanged := !samePublicIPMode(plan.PublicIPMode, state.PublicIPMode)
	if changed || publicIPModeChanged {
		unlock, err := r.claims.Lock(ctx, id.String())
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer", err.Error())
			return
		}
		defer unlock()
	}

	var lb *loadbalancersdk.LoadBalancerDetailSchema
	var err error
	if changed {
		lb, err = requestLoadBalancerOperation(ctx, fmt.Sprintf("the update of load balancer %s", id), func(ctx context.Context) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
			if _, err := r.waitUntilReady(ctx, id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval); err != nil {
				return nil, fmt.Errorf("the load balancer did not become idle before the update: %w", err)
			}
			return r.client.PartialUpdateLoadBalancer(ctx, id, loadbalancersdk.PartialUpdateLoadBalancerParams{ProjectID: r.projectID}, body)
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer", err.Error())
			return
		}
		if lb.Id != id {
			resp.Diagnostics.AddError("Error updating Load Balancer", fmt.Sprintf("The API answered the update of Load Balancer %s with Load Balancer %s, so the update was not applied in place.", id, lb.Id))
			return
		}
		// The backend applies a name or description change before it answers.
		// Only a package or topology change starts an asynchronous resize.
		if body.Package != nil || body.Topology != nil {
			lb, err = r.waitUntilReady(ctx, id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
			if err != nil {
				resp.Diagnostics.AddError("Error waiting for Load Balancer update", err.Error())
				return
			}
		}
	}

	// The public IP request runs after the PATCH and its resize, because both
	// operations claim the Load Balancer. Saving the PATCH result first means
	// a failed public IP change leaves state with the applied PATCH and the
	// old mode, so the next plan retries only the mode.
	if publicIPModeChanged {
		if lb != nil {
			patched := plan
			populateLoadBalancerResourceState(lb, &patched)
			resp.Diagnostics.Append(resp.State.Set(ctx, &patched)...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
		lb, err = r.configurePublicIPMode(ctx, id, plan.PublicIPMode)
		if err != nil {
			if lb != nil {
				observed := plan
				populateLoadBalancerResourceState(lb, &observed)
				resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
			}
			resp.Diagnostics.AddError("Error configuring Load Balancer public IP", err.Error())
			return
		}
	}

	if lb == nil {
		lb, err = r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer", err.Error())
			return
		}
	}

	newState := plan
	populateLoadBalancerResourceState(lb, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
	resp.Diagnostics.Append(loadBalancerPublicIPModeDiagnostics(plan.PublicIPMode, newState.PublicIPMode)...)
}

func (r *LoadBalancerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state LoadBalancerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	unlock, err := r.claims.Lock(ctx, id.String())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer", err.Error())
		return
	}
	defer unlock()
	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the deletion of load balancer %s", id), func(ctx context.Context) error {
		gone, err := r.waitUntilDeletable(ctx, id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
		if err != nil {
			return fmt.Errorf("the load balancer did not become idle before the deletion: %w", err)
		}
		// A Load Balancer that is already gone needs no DELETE, and Delete
		// treats ErrNotFound as a completed deletion.
		if gone {
			return fmt.Errorf("load balancer %s is gone: %w", id, loadbalancersdk.ErrNotFound)
		}
		return r.client.DeleteLoadBalancer(ctx, id, loadbalancersdk.DeleteLoadBalancerParams{ProjectID: r.projectID})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval); err != nil {
		resp.Diagnostics.AddError("Error waiting for Load Balancer deletion", err.Error())
	}
}

func (r *LoadBalancerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// waitUntilReady waits until Load Balancer id reports ACTIVE, and fails at
// once on ERROR, UNKNOWN, DELETED, or an unrecognized state.
//
// Create calls it after the POST. The POST claims nothing, so Create holds no
// claims lock for it. Update calls it as the idle wait before the PATCH, and
// configurePublicIPMode before the public IP request. Both requests claim the
// Load Balancer. The response shows a held claim as PENDING_UPDATE,
// PENDING_DELETE, or ERROR, so ACTIVE is the idle state. The backend refuses
// an update in ERROR or UNKNOWN, and the public IP contract sends no request
// in them, so both states fail at once. The caller holds the claims lock of
// the Load Balancer from this wait to the end of the ready wait that follows
// the request.
func (r *LoadBalancerResource) waitUntilReady(ctx context.Context, id core.UUID, timeout, pollInterval time.Duration) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
	w := wait.New[*loadbalancersdk.LoadBalancerDetailSchema](pollInterval, timeout)
	lb, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
		lb, err := r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if err != nil {
			return nil, err
		}
		if err := loadBalancerProvisioningReady(lb.ProvisioningStatus); err != nil {
			return nil, err
		}
		return lb, nil
	})
	if err != nil {
		return nil, fmt.Errorf("load balancer %s: %w", id, err)
	}
	return lb, nil
}

// configurePublicIPMode applies the public IP mode and waits for the result.
// The caller holds the claims lock of Load Balancer id.
//
// The public IP request claims the Load Balancer, and the backend accepts it in
// any provisioning status. So the idle wait before it uses waitUntilReady:
// PENDING_* waits for ACTIVE, and ERROR or UNKNOWN fail without a request. The
// last read of that wait decides whether a request is needed. The backend
// refuses a request that changes nothing, such as turning off a public IP
// that is already off, so a Load Balancer that already reports mode gets none.
// The endpoint answers 202 without a body, so the waiter supplies the state.
// On failure it also returns the last Load Balancer that it read, or nil when
// it read none.
func (r *LoadBalancerResource) configurePublicIPMode(ctx context.Context, id core.UUID, mode types.String) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
	body, err := buildLoadBalancerPublicIPBody(mode)
	if err != nil {
		return nil, err
	}
	lb, err := r.waitUntilReady(ctx, id, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
	if err != nil {
		return nil, fmt.Errorf("the load balancer did not become idle before the public IP change: %w", err)
	}
	if loadBalancerPublicIPModeReached(lb, mode) {
		return lb, nil
	}
	if err := loadBalancerPublicIPModeSupported(lb, mode); err != nil {
		return lb, fmt.Errorf("load balancer %s: %w", id, err)
	}
	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the public IP change of load balancer %s", id), func(ctx context.Context) error {
		return r.client.ConfigurePublicIpLoadBalancer(ctx, id, loadbalancersdk.ConfigurePublicIpLoadBalancerParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		return lb, err
	}
	return r.waitUntilPublicIPMode(ctx, id, mode, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
}

// waitUntilPublicIPMode polls until the Load Balancer is ACTIVE and reports
// mode, as loadBalancerPublicIPModeReached decides. public_ip_mode is not
// computed, so state after apply must equal the plan, and the mode check keeps
// the waiter from accepting the ACTIVE state that precedes the change. The
// request leaves provisioning_status ACTIVE, so only the addresses show the
// end of the change. On failure it returns the last Load Balancer that it
// read, or nil.
func (r *LoadBalancerResource) waitUntilPublicIPMode(ctx context.Context, id core.UUID, mode types.String, timeout, pollInterval time.Duration) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
	var observed *loadbalancersdk.LoadBalancerDetailSchema
	w := wait.New[*loadbalancersdk.LoadBalancerDetailSchema](pollInterval, timeout)
	lb, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.LoadBalancerDetailSchema, error) {
		lb, err := r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if err != nil {
			return nil, err
		}
		observed = lb
		if err := loadBalancerProvisioningReady(lb.ProvisioningStatus); err != nil && !errors.Is(err, wait.ErrNotReady) {
			return nil, fmt.Errorf("%w, %s", err, loadBalancerPublicAddressesText(lb))
		}
		if lb.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE || !loadBalancerPublicIPModeReached(lb, mode) {
			return nil, fmt.Errorf("provisioning_status=%s, %s: %w", lb.ProvisioningStatus, loadBalancerPublicAddressesText(lb), wait.ErrNotReady)
		}
		return lb, nil
	})
	if err != nil {
		return observed, fmt.Errorf("load balancer %s: %w", id, err)
	}
	return lb, nil
}

// waitUntilDeletable is the idle wait before a DELETE, and reports whether
// Load Balancer id is already gone.
//
// A DELETE claims the Load Balancer. The response shows a held claim as
// PENDING_CREATE, PENDING_UPDATE, or PENDING_DELETE, so the wait goes on in
// these states. ACTIVE, ERROR, and UNKNOWN count as idle, and Delete sends the
// DELETE in each of them. Only an unrecognized state fails at once. Delete
// holds the claims lock of the Load Balancer from this wait to the end of the
// delete wait.
func (r *LoadBalancerResource) waitUntilDeletable(ctx context.Context, id core.UUID, timeout, pollInterval time.Duration) (bool, error) {
	w := wait.New[bool](pollInterval, timeout)
	gone, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		lb, err := r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		switch lb.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return true, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			return false, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return false, fmt.Errorf("provisioning_status=%s: %w", lb.ProvisioningStatus, wait.ErrNotReady)
		default:
			return false, fmt.Errorf("unrecognized provisioning_status=%s", lb.ProvisioningStatus)
		}
	})
	if err != nil {
		return false, fmt.Errorf("load balancer %s: %w", id, err)
	}
	return gone, nil
}

// waitUntilDeleted waits until the GET of Load Balancer id answers 404.
//
// A failed delete puts the Load Balancer back to its old state, so ACTIVE,
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, and the wait stops.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: a Load Balancer in ERROR
// or UNKNOWN never shows PENDING_DELETE, so a failed delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *LoadBalancerResource) waitUntilDeleted(ctx context.Context, id core.UUID, timeout, pollInterval time.Duration) error {
	w := wait.New[struct{}](pollInterval, timeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		lb, err := r.client.GetLoadBalancer(ctx, id, loadbalancersdk.GetLoadBalancerParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		switch lb.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return struct{}{}, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE:
			pendingDeleteObserved = true
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", lb.ProvisioningStatus, wait.ErrNotReady)
		// Before PENDING_DELETE, this may be the state the object had before the DELETE.
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("terminal provisioning status %s after pending delete", lb.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", lb.ProvisioningStatus, wait.ErrNotReady)
		// ACTIVE after PENDING_DELETE: the delete failed and the old state is back.
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("deletion failed: provisioning status %s after pending delete", lb.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", lb.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", lb.ProvisioningStatus, wait.ErrNotReady)
		default:
			return struct{}{}, fmt.Errorf("unrecognized provisioning_status=%s", lb.ProvisioningStatus)
		}
	})
	if err != nil {
		return fmt.Errorf("load balancer %s: %w", id, err)
	}
	return nil
}

// requestLoadBalancerOperation sends request once, and names subject in the
// error of a busy refusal.
//
// request waits for the idle state of every object that the operation claims,
// and then sends the operation. The caller holds the claims lock of the root
// Load Balancer across this call. A busy refusal after the idle wait means
// that another client took the claim, which stays held for most of its
// operation, so the request is not sent again. Every other answer returns
// unchanged.
func requestLoadBalancerOperation[T any](
	ctx context.Context,
	subject string,
	request func(context.Context) (T, error),
) (T, error) {
	value, err := request(ctx)
	if loadBalancerBusyRefusal(err) {
		var zero T
		return zero, fmt.Errorf("%s was refused as busy, because another operation holds the claim: %w", subject, err)
	}
	return value, err
}

// requestLoadBalancerVoidOperation is requestLoadBalancerOperation for the
// endpoints that answer without a payload.
func requestLoadBalancerVoidOperation(ctx context.Context, subject string, request func(context.Context) error) error {
	_, err := requestLoadBalancerOperation(ctx, subject, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, request(ctx)
	})
	return err
}

// loadBalancerBusyRefusal reports whether err is a busy refusal: an HTTP 409
// response with at least one error detail whose code is resource_busy. The
// message is not part of the contract, so it is never matched.
func loadBalancerBusyRefusal(err error) bool {
	var apiErr *loadbalancersdk.APIError
	if !errors.As(err, &apiErr) || !errors.Is(apiErr, loadbalancersdk.ErrConflict) {
		return false
	}
	for _, detail := range apiErr.Errors {
		if detail.Code == resourceBusyErrorCode {
			return true
		}
	}
	return false
}

func buildLoadBalancerCreateBody(ctx context.Context, plan LoadBalancerResourceModel, resolveRegion projectlookup.RegionResolveFunc) (loadbalancersdk.LoadBalancerCreateSchema, diag.Diagnostics) {
	var body loadbalancersdk.LoadBalancerCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing Load Balancer name", "name must be configured, known, and non-empty.")
	}
	if plan.Package.IsNull() || plan.Package.IsUnknown() || strings.TrimSpace(plan.Package.ValueString()) == "" {
		diags.AddError("Missing Load Balancer package", "package must be configured, known, and non-empty.")
	}
	if plan.Topology.IsNull() || plan.Topology.IsUnknown() || strings.TrimSpace(plan.Topology.ValueString()) == "" {
		diags.AddError("Missing Load Balancer topology", "topology must be configured, known, and non-empty.")
	}
	planRegion := reference.Get(plan.Region, reference.Name)
	if planRegion.IsNull() || planRegion.IsUnknown() || strings.TrimSpace(planRegion.ValueString()) == "" {
		diags.AddError("Missing region", "region.name must be configured, known, and non-empty.")
	}
	vpcID, vpcDiags := parse.UUIDString(reference.Get(plan.VPC, reference.ID), "vpc.id")
	diags.Append(vpcDiags...)
	if diags.HasError() {
		return body, diags
	}

	regionName := strings.TrimSpace(planRegion.ValueString())
	region, err := resolveRegion(ctx, projectlookup.RegionFilter{Name: &regionName})
	if err != nil {
		diags.AddError("Unable to resolve region", "region.name: "+err.Error())
		return body, diags
	}

	body.Name = strings.TrimSpace(plan.Name.ValueString())
	body.Package = loadbalancersdk.LoadBalancerPackage(strings.TrimSpace(plan.Package.ValueString()))
	body.Topology = loadbalancersdk.LoadBalancerTopology(strings.TrimSpace(plan.Topology.ValueString()))
	if !plan.Type.IsNull() && !plan.Type.IsUnknown() {
		value := loadbalancersdk.LoadBalancerType(strings.TrimSpace(plan.Type.ValueString()))
		body.Type = &value
	}
	body.RegionId = region.Region.Id
	body.VpcId = vpcID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	if planPrivateIPID := reference.Get(plan.PrivateIP, reference.ID); !planPrivateIPID.IsNull() && !planPrivateIPID.IsUnknown() {
		privateIPID, privateIPDiags := parse.UUIDString(planPrivateIPID, "private_ip.id")
		diags.Append(privateIPDiags...)
		if !privateIPDiags.HasError() {
			body.PrivateIpId.Set(privateIPID)
		}
	}
	if planQoSPolicyID := reference.Get(plan.QoSPolicy, reference.ID); !planQoSPolicyID.IsNull() && !planQoSPolicyID.IsUnknown() {
		qosPolicyID, qosPolicyDiags := parse.UUIDString(planQoSPolicyID, "qos_policy.id")
		diags.Append(qosPolicyDiags...)
		if !qosPolicyDiags.HasError() {
			body.QosPolicyId.Set(qosPolicyID)
		}
	}
	return body, diags
}

func buildLoadBalancerUpdateBody(plan, state LoadBalancerResourceModel) (loadbalancersdk.LoadBalancerPartialUpdateSchema, bool, diag.Diagnostics) {
	var body loadbalancersdk.LoadBalancerPartialUpdateSchema
	var diags diag.Diagnostics
	changed := false

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing Load Balancer name", "name must be configured, known, and non-empty.")
	} else {
		name := strings.TrimSpace(plan.Name.ValueString())
		if state.Name.IsNull() || state.Name.IsUnknown() || strings.TrimSpace(state.Name.ValueString()) != name {
			body.Name = &name
			changed = true
		}
	}

	if plan.Package.IsNull() || plan.Package.IsUnknown() || strings.TrimSpace(plan.Package.ValueString()) == "" {
		diags.AddError("Missing Load Balancer package", "package must be configured, known, and non-empty.")
	} else {
		value := loadbalancersdk.LoadBalancerPackage(strings.TrimSpace(plan.Package.ValueString()))
		if state.Package.IsNull() || state.Package.IsUnknown() || strings.TrimSpace(state.Package.ValueString()) != string(value) {
			body.Package = &value
			changed = true
		}
	}

	if plan.Topology.IsNull() || plan.Topology.IsUnknown() || strings.TrimSpace(plan.Topology.ValueString()) == "" {
		diags.AddError("Missing Load Balancer topology", "topology must be configured, known, and non-empty.")
	} else {
		value := loadbalancersdk.LoadBalancerTopology(strings.TrimSpace(plan.Topology.ValueString()))
		if state.Topology.IsNull() || state.Topology.IsUnknown() || strings.TrimSpace(state.Topology.ValueString()) != string(value) {
			body.Topology = &value
			changed = true
		}
	}

	// The API trims description, so a change of outer whitespace alone is not
	// a change.
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := strings.TrimSpace(plan.Description.ValueString())
		if !compare.SameTrimmed(state.Description, description) {
			body.Description.Set(description)
			changed = true
		}
	}
	return body, changed, diags
}

// loadBalancerAssociationDiagnostics rejects a missing or different association
// after create so Terraform does not accept state that contradicts configuration.
func loadBalancerAssociationDiagnostics(attribute string, configured types.String, applied *core.UUID) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured.IsNull() || configured.IsUnknown() {
		return diags
	}
	switch {
	case applied == nil:
		diags.AddAttributeError(
			path.Root(attribute).AtName(reference.ID),
			"Load Balancer "+attribute+" was not reported by the API",
			fmt.Sprintf("The API returned no %s for configured value %q. Terraform tracks the created Load Balancer with the returned state.", attribute, configured.ValueString()),
		)
	case !compare.SameUUID(configured, *applied):
		diags.AddAttributeError(
			path.Root(attribute).AtName(reference.ID),
			"Load Balancer "+attribute+" does not match the configuration",
			fmt.Sprintf("The configuration requested %s %q, but the API assigned %s. The Load Balancer was created and is tracked in Terraform state; align the configuration with the assigned value or replace the Load Balancer.", attribute, configured.ValueString(), applied),
		)
	}
	return diags
}

// loadBalancerProvisioningReady classifies a provisioning status for a waiter
// that needs an ACTIVE Load Balancer.
func loadBalancerProvisioningReady(status loadbalancersdk.LoadBalancingProvisioningStatus) error {
	switch status {
	case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
		return nil
	// The backend sets UNKNOWN when its event watcher gives up, and never
	// leaves it on its own, so UNKNOWN is as final as ERROR.
	case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
		loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
		return fmt.Errorf("terminal provisioning status %s", status)
	case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
		return fmt.Errorf("provisioning_status=%s: %w", status, wait.ErrNotReady)
	default:
		return fmt.Errorf("unrecognized provisioning_status=%s", status)
	}
}

// buildLoadBalancerPublicIPBody builds the public IP request. A null mode
// sends an explicit JSON null, which turns public IP off. The SDK field has
// no omitempty, so an unset field would send an empty string instead.
func buildLoadBalancerPublicIPBody(mode types.String) (loadbalancersdk.LoadBalancerPublicIPSchema, error) {
	var body loadbalancersdk.LoadBalancerPublicIPSchema
	switch {
	case mode.IsUnknown():
		return body, errors.New("public_ip_mode must be known before the public IP can be configured")
	case mode.IsNull():
		body.Mode.SetNull()
	default:
		body.Mode.Set(loadbalancersdk.LoadBalancerPublicIPMode(strings.TrimSpace(mode.ValueString())))
	}
	return body, nil
}

// loadBalancerPublicIPModeReached reports whether Load Balancer lb already
// reports mode. Turning public IP off also needs the IPv6 address to be gone,
// because the derived mode ignores it once the IPv4 address is gone.
func loadBalancerPublicIPModeReached(lb *loadbalancersdk.LoadBalancerDetailSchema, mode types.String) bool {
	released := !mode.IsNull() || core.NullableToPtr(lb.V6PublicAddress) == nil
	return released && samePublicIPMode(loadBalancerPublicIPMode(lb), mode)
}

// loadBalancerPublicIPModeSupported rejects ipv4-only on a Load Balancer that
// reports both public addresses. That Load Balancer is dual-stack, and
// ModifyPlan rejects the same change when state already shows it. State can
// lag the backend, for example after a plan with -refresh=false.
func loadBalancerPublicIPModeSupported(lb *loadbalancersdk.LoadBalancerDetailSchema, mode types.String) error {
	if strings.TrimSpace(mode.ValueString()) != string(loadbalancersdk.LoadBalancerPublicIPModeIpv4Only) ||
		core.NullableToPtr(lb.PublicAddress) == nil || core.NullableToPtr(lb.V6PublicAddress) == nil {
		return nil
	}
	return fmt.Errorf("%s, so public_ip_mode is \"dual-stack\". %s", loadBalancerPublicAddressesText(lb), loadBalancerDualStackToIPv4OnlyText)
}

// loadBalancerPublicIPModeTransitionDiagnostics rejects a change from
// dual-stack to ipv4-only, which no request can apply.
func loadBalancerPublicIPModeTransitionDiagnostics(prior, planned types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if prior.IsUnknown() || planned.IsUnknown() ||
		strings.TrimSpace(prior.ValueString()) != string(loadbalancersdk.LoadBalancerPublicIPModeDualStack) ||
		strings.TrimSpace(planned.ValueString()) != string(loadbalancersdk.LoadBalancerPublicIPModeIpv4Only) {
		return diags
	}
	diags.AddAttributeError(
		path.Root("public_ip_mode"),
		"Unsupported public_ip_mode change",
		loadBalancerDualStackToIPv4OnlyText,
	)
	return diags
}

// loadBalancerPublicIPModeDiagnostics rejects a public IP mode that the API
// reports differently from the configuration, so Terraform does not accept
// state that contradicts it.
func loadBalancerPublicIPModeDiagnostics(configured, applied types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured.IsUnknown() || samePublicIPMode(configured, applied) {
		return diags
	}
	diags.AddAttributeError(
		path.Root("public_ip_mode"),
		"Load Balancer public_ip_mode does not match the configuration",
		fmt.Sprintf("The configuration requested public_ip_mode %s, but the API reports %s, which the provider derives from public_address and v6_public_address. Terraform tracks the Load Balancer with the reported mode.", publicIPModeText(configured), publicIPModeText(applied)),
	)
	return diags
}

// loadBalancerPublicAddressesText describes the public addresses of a Load
// Balancer in a waiter error.
func loadBalancerPublicAddressesText(lb *loadbalancersdk.LoadBalancerDetailSchema) string {
	text := func(address core.Nullable[string]) string {
		if value := core.NullableToPtr(address); value != nil {
			return fmt.Sprintf("%q", *value)
		}
		return "null"
	}
	return fmt.Sprintf("public_address=%s, v6_public_address=%s", text(lb.PublicAddress), text(lb.V6PublicAddress))
}

// publicIPModeText describes a public_ip_mode value in a diagnostic.
func publicIPModeText(mode types.String) string {
	if mode.IsNull() {
		return "null (public IP off)"
	}
	return fmt.Sprintf("%q", mode.ValueString())
}

// requiresReplaceUnlessAssociated replaces the Load Balancer when a configured
// ID reference names an object other than the one in the computed attribute,
// which the API cannot change in place. Configuring the associated object, or
// omitting the reference, only updates state.
func requiresReplaceUnlessAssociated(computedAttribute string) planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			if req.ConfigValue.IsNull() {
				return
			}
			var associated types.String
			resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root(computedAttribute), &associated)...)
			if resp.Diagnostics.HasError() {
				return
			}
			resp.RequiresReplace = !compare.SameUUIDString(reference.Get(req.ConfigValue, reference.ID), associated)
		},
		"Changing the associated object requires replacement.",
		"Changing the associated object requires replacement.",
	)
}

// sdkEnumValue is an SDK enum type that reports which values it defines.
type sdkEnumValue interface {
	~string
	Valid() bool
}

// sdkEnum returns a validator that accepts exactly the values that the SDK
// enum T defines, so the accepted set follows the SDK release instead of a
// copied list. It ignores outer whitespace, which the request trims.
func sdkEnum[T sdkEnumValue]() validator.String {
	return sdkEnumValidator[T]{}
}

type sdkEnumValidator[T sdkEnumValue] struct{}

func (sdkEnumValidator[T]) Description(context.Context) string {
	return "Must be a value that the SDK defines."
}

func (v sdkEnumValidator[T]) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (sdkEnumValidator[T]) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || T(strings.TrimSpace(req.ConfigValue.ValueString())).Valid() {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("%s does not accept %q. The attribute description lists the accepted values.", req.Path, req.ConfigValue.ValueString()))
}

// trimmedSDKEnum is sdkEnum for an attribute whose request sends the trimmed
// value, so it ignores outer whitespace. A value that is blank after the trim
// is still invalid.
func trimmedSDKEnum[T sdkEnumValue]() validator.String {
	return trimmedSDKEnumValidator[T]{}
}

type trimmedSDKEnumValidator[T sdkEnumValue] struct {
	sdkEnumValidator[T]
}

func (v trimmedSDKEnumValidator[T]) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && T(strings.TrimSpace(req.ConfigValue.ValueString())).Valid() {
		return
	}
	v.sdkEnumValidator.ValidateString(ctx, req, resp)
}

// samePublicIPMode reports whether two public_ip_mode values name the same
// mode. Known values compare after the trim of outer whitespace, and null or
// unknown values compare as they are.
func samePublicIPMode(a, b types.String) bool {
	if a.IsNull() || a.IsUnknown() || b.IsNull() || b.IsUnknown() {
		return a.Equal(b)
	}
	return strings.TrimSpace(a.ValueString()) == strings.TrimSpace(b.ValueString())
}

// preservedPublicIPMode keeps the prior spelling of public_ip_mode when it
// names the derived mode, and the derived mode otherwise.
func preservedPublicIPMode(prior, derived types.String) types.String {
	if samePublicIPMode(prior, derived) {
		return prior
	}
	return derived
}
