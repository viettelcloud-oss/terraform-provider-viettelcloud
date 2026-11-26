package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	_ resource.Resource                   = &ListenerResource{}
	_ resource.ResourceWithImportState    = &ListenerResource{}
	_ resource.ResourceWithValidateConfig = &ListenerResource{}
)

const (
	listenerReadyTimeout      = 30 * time.Minute
	listenerReadyPollInterval = 5 * time.Second
)

// The create API requires every timeout, so these defaults fill the
// timeouts that a practitioner omits.
const (
	defaultListenerTimeout                 = 50000
	listenerDefaultServerTimeout           = 50000
	listenerDefaultServerConnectingTimeout = 5000
)

type ListenerResource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
	claims    *keylock.Locker
}

func NewListenerResource() resource.Resource {
	return &ListenerResource{}
}

func (r *ListenerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_listener"
}

func (r *ListenerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Load Balancer Listener.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Listener ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"load_balancer": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Load balancer that owns the listener. Changing it replaces the listener.",
				Attributes:  reference.Attributes(reference.ID, "Load balancer ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(reference.ID, compare.SameUUIDString),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the listener.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Description of the listener.",
			},
			"protocol": schema.StringAttribute{
				Required:    true,
				Description: "Protocol for the listener: HTTP, HTTPS, TERMINATED_HTTPS, TCP, or UDP.",
				Validators: []validator.String{
					sdkEnum[loadbalancersdk.ListenerProtocolType](),
				},
				PlanModifiers: []planmodifier.String{
					replace.String(compare.SameTrimmedString),
				},
			},
			"protocol_port": schema.Int64Attribute{
				Required:    true,
				Description: "Port on which the listener listens for traffic (1-65535).",
				Validators: []validator.Int64{
					int64validator.Between(1, 65535),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"server_group": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Default server group for the listener. Removing it from the configuration detaches the server group.",
				Attributes:  reference.Attributes(reference.ID, "Server group ID (UUID)."),
			},
			"default_tls_cert": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Default TLS certificate for HTTPS or TERMINATED_HTTPS listeners. Removing it from the configuration detaches the certificate. The backend rejects detaching it from a TERMINATED_HTTPS listener.",
				Attributes:  reference.Attributes(reference.ID, "TLS certificate ID (UUID)."),
			},
			"sni_tls_certs": schema.SetNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Set of SNI TLS certificates. Omission preserves the current certificates. A TERMINATED_HTTPS listener needs at least one certificate: the backend rejects an omitted or empty set on create and an empty set on update for that protocol.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: reference.PreservedAttributes(reference.ID, "TLS certificate ID (UUID)."),
				},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"allowed_cidrs": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "List of allowed IPv4 CIDRs that can access this listener.",
			},
			"listener_timeout": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Frontend client inactivity timeout in milliseconds (1-2073600000). Default is 50000.",
				Validators: []validator.Int64{
					int64validator.Between(1, 2073600000),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"server_timeout": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Backend member inactivity timeout in milliseconds (1-2073600000). Default is 50000.",
				Validators: []validator.Int64{
					int64validator.Between(1, 2073600000),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"server_connecting_timeout": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Backend connection timeout in milliseconds (1-2073600000). Default is 5000.",
				Validators: []validator.Int64{
					int64validator.Between(1, 2073600000),
				},
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"enable_h2": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether to enable HTTP/2. On create, omission sends false for HTTP and true for HTTPS and TERMINATED_HTTPS. Later omission preserves the current value. The backend always enables HTTP/2 on HTTPS and TERMINATED_HTTPS listeners, so false is rejected for these protocols.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"set_x_forwarded_for": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether to insert X-Forwarded-For header. Omission preserves the current value. Set false to disable it.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"set_x_forwarded_port": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether to insert X-Forwarded-Port header. Omission preserves the current value. Set false to disable it.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"set_x_forwarded_proto": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether to insert X-Forwarded-Proto header. Omission preserves the current value. Set false to disable it.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status of the listener.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status of the listener.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the listener was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the listener was last updated (RFC3339).",
			},
		},
	}
}

func (r *ListenerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig rejects enable_h2 = false on HTTPS and TERMINATED_HTTPS. The
// backend stores true for these protocols whatever the request sends, so a
// configured false fails after apply as an inconsistent result.
//
// It also rejects two allowed_cidrs or sni_tls_certs elements that name the
// same value after the trim. The request sends one value for both, so the
// state after apply would hold fewer elements than the plan.
func (r *ListenerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var protocol types.String
	var enableH2 types.Bool
	var allowedCIDRs, sniTLSCerts types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("protocol"), &protocol)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("enable_h2"), &enableH2)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("allowed_cidrs"), &allowedCIDRs)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("sni_tls_certs"), &sniTLSCerts)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if duplicate, ok := listenerTrimmedDuplicate(listenerSetStrings(allowedCIDRs), strings.TrimSpace); ok {
		resp.Diagnostics.AddAttributeError(path.Root("allowed_cidrs"), "Duplicate listener allowed CIDR",
			fmt.Sprintf("allowed_cidrs holds %q more than once after outer whitespace is removed. List each CIDR once.", duplicate))
	}
	if !sniTLSCerts.IsNull() && !sniTLSCerts.IsUnknown() {
		if duplicate, ok := listenerTrimmedDuplicate(reference.Strings(sniTLSCerts, reference.ID), listenerUUIDKey); ok {
			resp.Diagnostics.AddAttributeError(path.Root("sni_tls_certs"), "Duplicate listener SNI TLS certificate",
				fmt.Sprintf("sni_tls_certs holds certificate %q more than once. List each certificate once.", duplicate))
		}
	}
	if protocol.IsNull() || protocol.IsUnknown() || enableH2.IsNull() || enableH2.IsUnknown() {
		return
	}
	if enableH2.ValueBool() || !listenerAlwaysEnablesH2(loadbalancersdk.ListenerProtocolType(strings.TrimSpace(protocol.ValueString()))) {
		return
	}
	resp.Diagnostics.AddAttributeError(
		path.Root("enable_h2"),
		"Invalid listener enable_h2",
		fmt.Sprintf("The backend always enables HTTP/2 on a %s listener, so enable_h2 cannot be false. Omit enable_h2 or set it to true.", strings.TrimSpace(protocol.ValueString())),
	)
}

// listenerSetStrings returns the elements of a string set, or none when the
// set is null or unknown.
func listenerSetStrings(set types.Set) []types.String {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	values := make([]types.String, 0, len(set.Elements()))
	for _, element := range set.Elements() {
		if value, ok := element.(types.String); ok {
			values = append(values, value)
		}
	}
	return values
}

// listenerTrimmedDuplicate returns the first known value whose key equals the
// key of an earlier known value. The value is returned trimmed.
func listenerTrimmedDuplicate(values []types.String, key func(string) string) (string, bool) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		k := key(value.ValueString())
		if _, ok := seen[k]; ok {
			return strings.TrimSpace(value.ValueString()), true
		}
		seen[k] = struct{}{}
	}
	return "", false
}

// listenerUUIDKey returns the canonical form of a UUID, so that two spellings
// of one certificate ID compare equal. A value that is not a UUID is only
// trimmed.
func listenerUUIDKey(value string) string {
	if id, err := parse.UUID(value); err == nil {
		return id.String()
	}
	return strings.TrimSpace(value)
}

func (r *ListenerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ListenerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, lbDiags := parse.UUIDString(reference.Get(plan.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(lbDiags...)
	body, bodyDiags := buildListenerCreateBody(plan)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	unlock, err := r.claims.Lock(ctx, lbID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer Listener", err.Error())
		return
	}
	defer unlock()
	listener, err := requestLoadBalancerOperation(ctx, fmt.Sprintf("the creation of a listener on load balancer %s", lbID), func(ctx context.Context) (*loadbalancersdk.ListenerDetailSchema, error) {
		if err := r.waitUntilLoadBalancerIdle(ctx, lbID); err != nil {
			return nil, fmt.Errorf("the load balancer did not become idle before the creation: %w", err)
		}
		return r.client.CreateListener(ctx, lbID, loadbalancersdk.CreateListenerParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Load Balancer Listener", err.Error())
		return
	}

	populateListenerResourceState(listener, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	listener, err = r.waitUntilReady(ctx, lbID, listener.Id, listenerReadyTimeout, listenerReadyPollInterval)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for Load Balancer Listener to be ready",
			fmt.Sprintf("%s\n\nListener ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, plan.ID.ValueString()),
		)
		return
	}

	populateListenerResourceState(listener, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ListenerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ListenerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, lbDiags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(lbDiags...)
	listenerID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, err := r.client.GetListener(ctx, lbID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Load Balancer Listener", err.Error())
		return
	}
	if detail.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		resp.State.RemoveResource(ctx)
		return
	}

	populateListenerResourceState(detail, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ListenerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ListenerResourceModel
	var state ListenerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, lbDiags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(lbDiags...)
	listenerID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var detail *loadbalancersdk.ListenerDetailSchema
	var err error

	body, hasChanges, bodyDiags := buildListenerPartialUpdateBody(plan, state)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if hasChanges {
		unlock, err := r.claims.Lock(ctx, lbID.String())
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer Listener", err.Error())
			return
		}
		defer unlock()
		detail, err = requestLoadBalancerOperation(ctx, fmt.Sprintf("the update of listener %s on load balancer %s", listenerID, lbID), func(ctx context.Context) (*loadbalancersdk.ListenerDetailSchema, error) {
			if err := r.waitUntilLoadBalancerIdle(ctx, lbID); err != nil {
				return nil, fmt.Errorf("the load balancer did not become idle before the update: %w", err)
			}
			if _, err := r.waitUntilReady(ctx, lbID, listenerID, listenerReadyTimeout, listenerReadyPollInterval); err != nil {
				return nil, fmt.Errorf("the listener did not become idle before the update: %w", err)
			}
			return r.client.PartialUpdateListener(ctx, lbID, listenerID, loadbalancersdk.PartialUpdateListenerParams{ProjectID: r.projectID}, body)
		})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer Listener", err.Error())
			return
		}
		if detail != nil && detail.Id != listenerID {
			resp.Diagnostics.AddError("Error updating Load Balancer Listener", fmt.Sprintf("The API answered the update of Listener %s with Listener %s, so the update was not applied in place.", listenerID, detail.Id))
			return
		}

		detail, err = r.waitUntilReady(ctx, lbID, listenerID, listenerReadyTimeout, listenerReadyPollInterval)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for Load Balancer Listener update", err.Error())
			return
		}
		if detail.Id != listenerID {
			resp.Diagnostics.AddError("Error updating Load Balancer Listener", fmt.Sprintf("The API answered the update of Listener %s with Listener %s, so the update was not applied in place.", listenerID, detail.Id))
			return
		}
	} else {
		detail, err = r.client.GetListener(ctx, lbID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
		if err != nil {
			resp.Diagnostics.AddError("Error updating Load Balancer Listener", err.Error())
			return
		}
	}

	newState := plan
	populateListenerResourceState(detail, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *ListenerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ListenerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lbID, lbDiags := parse.UUIDString(reference.Get(state.LoadBalancer, reference.ID), "load_balancer.id")
	resp.Diagnostics.Append(lbDiags...)
	listenerID, idDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(idDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	unlock, err := r.claims.Lock(ctx, lbID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer Listener", err.Error())
		return
	}
	defer unlock()
	err = requestLoadBalancerVoidOperation(ctx, fmt.Sprintf("the deletion of listener %s on load balancer %s", listenerID, lbID), func(ctx context.Context) error {
		gone, err := r.waitUntilDeletable(ctx, lbID, listenerID, listenerReadyTimeout, listenerReadyPollInterval)
		if err != nil {
			return fmt.Errorf("the listener did not become idle before the deletion: %w", err)
		}
		// A Listener that is already gone needs no DELETE, and Delete treats
		// ErrNotFound as a completed deletion.
		if gone {
			return fmt.Errorf("listener %s is gone: %w", listenerID, loadbalancersdk.ErrNotFound)
		}
		if err := r.waitUntilLoadBalancerIdle(ctx, lbID); err != nil {
			return fmt.Errorf("the load balancer did not become idle before the deletion: %w", err)
		}
		return r.client.DeleteListener(ctx, lbID, listenerID, loadbalancersdk.DeleteListenerParams{ProjectID: r.projectID})
	})
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting Load Balancer Listener", err.Error())
		return
	}

	if err := r.waitUntilDeleted(ctx, lbID, listenerID, listenerReadyTimeout, listenerReadyPollInterval); err != nil {
		resp.Diagnostics.AddError("Error waiting for Load Balancer Listener deletion", err.Error())
	}
}

func (r *ListenerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(strings.TrimSpace(req.ID), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected import ID in format \"<load_balancer_id>/<listener_id>\", got %q.", req.ID),
		)
		return
	}

	lbID := strings.TrimSpace(parts[0])
	listenerID := strings.TrimSpace(parts[1])

	if _, diags := parse.UUIDString(types.StringValue(lbID), "load_balancer_id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(listenerID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("load_balancer"), reference.Value(reference.ID, types.StringValue(lbID)))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), listenerID)...)
}

// waitUntilReady waits until Listener listenerID reports ACTIVE, and fails at
// once on ERROR, UNKNOWN, DELETED, or an unrecognized state.
//
// Create and Update call it after the request. Update also calls it as the
// idle wait before the PATCH, which claims the Listener and Load Balancer
// loadBalancerID. The response shows a held claim as PENDING_UPDATE,
// PENDING_DELETE, or ERROR, so ACTIVE is the idle state. The backend refuses
// an update in ERROR, and never leaves UNKNOWN on its own, so both states fail
// at once. The caller holds the claims lock of the Load Balancer.
func (r *ListenerResource) waitUntilReady(
	ctx context.Context,
	loadBalancerID core.UUID,
	listenerID core.UUID,
	timeout, pollInterval time.Duration,
) (*loadbalancersdk.ListenerDetailSchema, error) {
	w := wait.New[*loadbalancersdk.ListenerDetailSchema](pollInterval, timeout)
	listener, err := w.WaitFor(ctx, func(ctx context.Context) (*loadbalancersdk.ListenerDetailSchema, error) {
		detail, err := r.client.GetListener(ctx, loadBalancerID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
		if err != nil {
			return nil, err
		}
		switch detail.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE:
			return detail, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
			loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return nil, fmt.Errorf("terminal provisioning status %s", detail.ProvisioningStatus)
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return nil, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("unrecognized provisioning_status=%s", detail.ProvisioningStatus)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("listener %s: %w", listenerID, err)
	}
	return listener, nil
}

// waitUntilDeletable is the idle wait of Listener listenerID before a DELETE,
// and reports whether the Listener is already gone.
//
// A DELETE claims the Listener and Load Balancer loadBalancerID. The response
// shows a held claim as PENDING_CREATE, PENDING_UPDATE, or PENDING_DELETE, so
// the wait goes on in these states. ACTIVE, ERROR, and UNKNOWN count as idle,
// and Delete sends the DELETE in each of them. Only an unrecognized state fails
// at once. The caller holds the claims lock of the Load Balancer.
func (r *ListenerResource) waitUntilDeletable(
	ctx context.Context,
	loadBalancerID core.UUID,
	listenerID core.UUID,
	timeout, pollInterval time.Duration,
) (bool, error) {
	w := wait.New[bool](pollInterval, timeout)
	gone, err := w.WaitFor(ctx, func(ctx context.Context) (bool, error) {
		detail, err := r.client.GetListener(ctx, loadBalancerID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		switch detail.ProvisioningStatus {
		case loadbalancersdk.LoadBalancingProvisioningStatusDELETED:
			return true, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			return false, nil
		case loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return false, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		default:
			return false, fmt.Errorf("unrecognized provisioning_status=%s", detail.ProvisioningStatus)
		}
	})
	if err != nil {
		return false, fmt.Errorf("listener %s: %w", listenerID, err)
	}
	return gone, nil
}

// waitUntilDeleted waits until the GET of Listener listenerID answers 404.
//
// ERROR or UNKNOWN after PENDING_DELETE means the delete failed, so the wait
// stops. Before PENDING_DELETE it can still be the old state, so the wait goes on.
//
// Exception to "Lifecycle Waiters" in CONTRIBUTING.md: the GET hides the delete
// until the infrastructure accepts it, so a refused delete fails only at timeout.
// Remove this once the GET shows PENDING_DELETE right after the 202.
func (r *ListenerResource) waitUntilDeleted(
	ctx context.Context,
	loadBalancerID core.UUID,
	listenerID core.UUID,
	timeout, pollInterval time.Duration,
) error {
	w := wait.New[struct{}](pollInterval, timeout)
	pendingDeleteObserved := false
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		detail, err := r.client.GetListener(ctx, loadBalancerID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: r.projectID})
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
		// Before PENDING_DELETE, this may be the state the object had before the DELETE.
		case loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN:
			if pendingDeleteObserved {
				return struct{}{}, fmt.Errorf("terminal provisioning status %s after pending delete", detail.ProvisioningStatus)
			}
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		case loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE,
			loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE:
			return struct{}{}, fmt.Errorf("provisioning_status=%s: %w", detail.ProvisioningStatus, wait.ErrNotReady)
		default:
			return struct{}{}, fmt.Errorf("unrecognized provisioning_status=%s", detail.ProvisioningStatus)
		}
	})
	if err != nil {
		return fmt.Errorf("listener %s: %w", listenerID, err)
	}
	return nil
}

// waitUntilLoadBalancerIdle is the idle wait of Load Balancer loadBalancerID,
// the parent of every Listener, before a Listener request.
//
// Every Listener Create, Update, and Delete claims that Load Balancer. The
// backend refuses each of them while the Load Balancer is PENDING_*, in ERROR,
// or in UNKNOWN, so ACTIVE is the idle state, and ERROR and UNKNOWN fail at
// once. The caller holds the claims lock of the Load Balancer.
//
// A missing Load Balancer does not prove that the Listener is gone, so the
// error never matches ErrNotFound, which Delete treats as a completed
// deletion.
func (r *ListenerResource) waitUntilLoadBalancerIdle(ctx context.Context, loadBalancerID core.UUID) error {
	parent := LoadBalancerResource{client: r.client, projectID: r.projectID}
	_, err := parent.waitUntilReady(ctx, loadBalancerID, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
	if errors.Is(err, loadbalancersdk.ErrNotFound) {
		return fmt.Errorf("the load balancer is missing: %s", err)
	}
	return err
}

func buildListenerCreateBody(plan ListenerResourceModel) (loadbalancersdk.ListenerCreateSchema, diag.Diagnostics) {
	var body loadbalancersdk.ListenerCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing listener name", "name must be configured, known, and non-empty.")
	} else {
		body.Name = strings.TrimSpace(plan.Name.ValueString())
	}

	if plan.Protocol.IsNull() || plan.Protocol.IsUnknown() || strings.TrimSpace(plan.Protocol.ValueString()) == "" {
		diags.AddError("Missing listener protocol", "protocol must be configured, known, and non-empty.")
	} else {
		body.Protocol = loadbalancersdk.ListenerProtocolType(strings.TrimSpace(plan.Protocol.ValueString()))
	}

	if plan.ProtocolPort.IsNull() || plan.ProtocolPort.IsUnknown() {
		diags.AddError("Missing listener protocol_port", "protocol_port must be configured and known.")
	} else {
		body.ProtocolPort = int(plan.ProtocolPort.ValueInt64())
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		desc := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &desc
	}

	// Keep the documented create defaults when the practitioner omits timeouts.
	body.ListenerTimeout.Set(listenerTimeoutOrDefault(plan.ListenerTimeout, defaultListenerTimeout))
	body.ServerTimeout.Set(listenerTimeoutOrDefault(plan.ServerTimeout, listenerDefaultServerTimeout))
	body.ServerConnectingTimeout.Set(listenerTimeoutOrDefault(plan.ServerConnectingTimeout, listenerDefaultServerConnectingTimeout))

	// ListenerCreateSchema has no way to omit server_group_id, and an unset
	// Nullable without omitempty serializes as the zero UUID. So an absent
	// server group is sent as an explicit JSON null.
	body.ServerGroupId.SetNull()
	if sg := reference.Get(plan.ServerGroup, reference.ID); !sg.IsNull() && !sg.IsUnknown() {
		sgID, sgDiags := parse.UUIDString(sg, "server_group.id")
		diags.Append(sgDiags...)
		body.ServerGroupId.Set(sgID)
	}

	if cert := reference.Get(plan.DefaultTLSCert, reference.ID); !cert.IsNull() && !cert.IsUnknown() {
		certID, certDiags := parse.UUIDString(cert, "default_tls_cert.id")
		diags.Append(certDiags...)
		body.DefaultTlsCertId.Set(certID)
	}

	sniIDs, sniKnown, sniDiags := listenerSNITLSCertIDs(plan.SNITLSCerts)
	diags.Append(sniDiags...)
	// An omitted Optional+Computed set is unknown, not null, in a create plan,
	// so both states mean that the practitioner left sni_tls_certs out.
	sniOmitted := plan.SNITLSCerts.IsNull() || plan.SNITLSCerts.IsUnknown()
	if sniKnown {
		body.SniTlsCertIds = &sniIDs
	} else if sniOmitted && body.Protocol == loadbalancersdk.ListenerProtocolTypeTERMINATEDHTTPS {
		// The OpenAPI schema defaults sni_tls_cert_ids to [], but the backend
		// rejects an omitted value on TERMINATED_HTTPS with 400 invalid_input:
		// "sni_tls_certs is required when listener protocol is TERMINATED_HTTPS."
		ids := make([]core.UUID, 0)
		body.SniTlsCertIds = &ids
	}

	if !plan.AllowedCIDRs.IsNull() && !plan.AllowedCIDRs.IsUnknown() {
		cidrs := make([]string, 0, len(plan.AllowedCIDRs.Elements()))
		known := true
		for _, elem := range plan.AllowedCIDRs.Elements() {
			s, ok := elem.(types.String)
			if !ok || s.IsNull() {
				diags.AddError("Invalid listener collection element", "Listener certificate and CIDR sets must contain non-null strings.")
				continue
			}
			if s.IsUnknown() {
				known = false
				break
			}
			cidrs = append(cidrs, strings.TrimSpace(s.ValueString()))
		}
		if known {
			body.AllowedCidrs = &cidrs
		}
	}

	// The OpenAPI schema makes enable_h2 optional, but the backend rejects an
	// omitted value on HTTP, HTTPS, and TERMINATED_HTTPS with 400 invalid_input:
	// "Field enable_h2 must be set True or False when protocol is HTTP." So an
	// omitted value sends the value that the backend keeps for the protocol.
	switch {
	case !plan.EnableH2.IsNull() && !plan.EnableH2.IsUnknown():
		body.EnableH2.Set(plan.EnableH2.ValueBool())
	case listenerAlwaysEnablesH2(body.Protocol):
		body.EnableH2.Set(true)
	case body.Protocol == loadbalancersdk.ListenerProtocolTypeHTTP:
		body.EnableH2.Set(false)
	}

	if !plan.SetXForwardedFor.IsNull() && !plan.SetXForwardedFor.IsUnknown() {
		body.SetXForwardedFor.Set(plan.SetXForwardedFor.ValueBool())
	}

	if !plan.SetXForwardedPort.IsNull() && !plan.SetXForwardedPort.IsUnknown() {
		body.SetXForwardedPort.Set(plan.SetXForwardedPort.ValueBool())
	}

	if !plan.SetXForwardedProto.IsNull() && !plan.SetXForwardedProto.IsUnknown() {
		body.SetXForwardedProto.Set(plan.SetXForwardedProto.ValueBool())
	}

	return body, diags
}

// listenerAlwaysEnablesH2 reports whether the API always returns
// enable_h2 = true for protocol, whatever the request sends. This holds for
// HTTPS and TERMINATED_HTTPS.
func listenerAlwaysEnablesH2(protocol loadbalancersdk.ListenerProtocolType) bool {
	return protocol == loadbalancersdk.ListenerProtocolTypeHTTPS || protocol == loadbalancersdk.ListenerProtocolTypeTERMINATEDHTTPS
}

func listenerTimeoutOrDefault(v types.Int64, fallback int) int {
	if v.IsNull() || v.IsUnknown() {
		return fallback
	}
	return int(v.ValueInt64())
}

func buildListenerPartialUpdateBody(plan, state ListenerResourceModel) (loadbalancersdk.ListenerPartialUpdateSchema, bool, diag.Diagnostics) {
	var body loadbalancersdk.ListenerPartialUpdateSchema
	var diags diag.Diagnostics
	hasChanges := false

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing listener name", "name must be configured, known, and non-empty.")
	} else if name := strings.TrimSpace(plan.Name.ValueString()); !compare.SameTrimmed(state.Name, name) {
		body.Name = &name
		hasChanges = true
	}

	// The API trims description, so a change of outer whitespace alone is not
	// a change.
	sameDescription := !plan.Description.IsNull() && !plan.Description.IsUnknown() &&
		compare.SameTrimmed(state.Description, plan.Description.ValueString())
	if !plan.Description.Equal(state.Description) && !sameDescription {
		if !plan.Description.IsUnknown() {
			body.Description.Set(strings.TrimSpace(plan.Description.ValueString()))
			hasChanges = true
		}
	}

	if !plan.ListenerTimeout.Equal(state.ListenerTimeout) && !plan.ListenerTimeout.IsNull() && !plan.ListenerTimeout.IsUnknown() {
		v := int(plan.ListenerTimeout.ValueInt64())
		body.ListenerTimeout = &v
		hasChanges = true
	}

	if !plan.ServerTimeout.Equal(state.ServerTimeout) && !plan.ServerTimeout.IsNull() && !plan.ServerTimeout.IsUnknown() {
		v := int(plan.ServerTimeout.ValueInt64())
		body.ServerTimeout = &v
		hasChanges = true
	}

	if !plan.ServerConnectingTimeout.Equal(state.ServerConnectingTimeout) && !plan.ServerConnectingTimeout.IsNull() && !plan.ServerConnectingTimeout.IsUnknown() {
		v := int(plan.ServerConnectingTimeout.ValueInt64())
		body.ServerConnectingTimeout = &v
		hasChanges = true
	}

	if id, changed, idDiags := listenerReferenceUpdate(plan.ServerGroup, state.ServerGroup, "server_group.id"); changed {
		diags.Append(idDiags...)
		body.ServerGroupId = id
		hasChanges = true
	}

	if id, changed, idDiags := listenerReferenceUpdate(plan.DefaultTLSCert, state.DefaultTLSCert, "default_tls_cert.id"); changed {
		diags.Append(idDiags...)
		body.DefaultTlsCertId = id
		hasChanges = true
	}

	if !plan.SNITLSCerts.IsNull() && !plan.SNITLSCerts.IsUnknown() && !compare.SameUUIDSet(listenerReferenceIDSet(plan.SNITLSCerts), listenerReferenceIDSet(state.SNITLSCerts)) {
		ids, known, idDiags := listenerSNITLSCertIDs(plan.SNITLSCerts)
		diags.Append(idDiags...)
		if known {
			body.SniTlsCertIds = &ids
			hasChanges = true
		}
	}

	if !plan.AllowedCIDRs.IsUnknown() && !listenerSameStringSet(plan.AllowedCIDRs, state.AllowedCIDRs) {
		if !plan.AllowedCIDRs.IsNull() && !plan.AllowedCIDRs.IsUnknown() {
			cidrs := make([]string, 0, len(plan.AllowedCIDRs.Elements()))
			known := true
			for _, elem := range plan.AllowedCIDRs.Elements() {
				s, ok := elem.(types.String)
				if !ok || s.IsNull() {
					diags.AddError("Invalid listener collection element", "Listener certificate and CIDR sets must contain non-null strings.")
					continue
				}
				if s.IsUnknown() {
					known = false
					break
				}
				cidrs = append(cidrs, strings.TrimSpace(s.ValueString()))
			}
			if known {
				body.AllowedCidrs = &cidrs
				hasChanges = true
			}
		} else if !state.AllowedCIDRs.IsNull() && len(state.AllowedCIDRs.Elements()) > 0 {
			empty := []string{}
			body.AllowedCidrs = &empty
			hasChanges = true
		}
	}

	if !plan.EnableH2.Equal(state.EnableH2) && !plan.EnableH2.IsNull() && !plan.EnableH2.IsUnknown() {
		body.EnableH2.Set(plan.EnableH2.ValueBool())
		hasChanges = true
	}

	if !plan.SetXForwardedFor.Equal(state.SetXForwardedFor) && !plan.SetXForwardedFor.IsNull() && !plan.SetXForwardedFor.IsUnknown() {
		body.SetXForwardedFor.Set(plan.SetXForwardedFor.ValueBool())
		hasChanges = true
	}

	if !plan.SetXForwardedPort.Equal(state.SetXForwardedPort) && !plan.SetXForwardedPort.IsNull() && !plan.SetXForwardedPort.IsUnknown() {
		body.SetXForwardedPort.Set(plan.SetXForwardedPort.ValueBool())
		hasChanges = true
	}

	if !plan.SetXForwardedProto.Equal(state.SetXForwardedProto) && !plan.SetXForwardedProto.IsNull() && !plan.SetXForwardedProto.IsUnknown() {
		body.SetXForwardedProto.Set(plan.SetXForwardedProto.ValueBool())
		hasChanges = true
	}

	return body, hasChanges, diags
}

// listenerReferenceUpdate returns the PATCH value of a single reference.
// changed is false when the planned ID is unknown or names the object in prior
// state. A reference removed from configuration sends JSON null, which
// detaches it.
func listenerReferenceUpdate(plan, state types.Object, path string) (core.Nullable[core.UUID], bool, diag.Diagnostics) {
	var value core.Nullable[core.UUID]
	planned := reference.Get(plan, reference.ID)
	prior := reference.Get(state, reference.ID)
	if planned.IsUnknown() || planned.Equal(prior) || compare.SameUUIDString(planned, prior) {
		return value, false, nil
	}
	if planned.IsNull() {
		value.SetNull()
		return value, true, nil
	}
	id, diags := parse.UUIDString(planned, path)
	value.Set(id)
	return value, true, diags
}

// listenerSNITLSCertIDs parses the IDs of a configured SNI certificate set.
// known is false when the set is null or unknown or holds an unknown ID, so
// the caller never sends a partial set.
func listenerSNITLSCertIDs(certs types.Set) ([]core.UUID, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if certs.IsNull() || certs.IsUnknown() {
		return nil, false, diags
	}
	values := reference.Strings(certs, reference.ID)
	ids := make([]core.UUID, 0, len(values))
	for _, value := range values {
		if value.IsUnknown() {
			return nil, false, diags
		}
		if value.IsNull() {
			diags.AddError("Invalid listener collection element", "Listener certificate and CIDR sets must contain non-null strings.")
			continue
		}
		id, idDiags := parse.UUIDString(value, "sni_tls_certs.id")
		diags.Append(idDiags...)
		ids = append(ids, id)
	}
	return ids, true, diags
}
