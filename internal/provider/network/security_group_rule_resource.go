package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                   = &SecurityGroupRuleResource{}
	_ resource.ResourceWithConfigure      = &SecurityGroupRuleResource{}
	_ resource.ResourceWithValidateConfig = &SecurityGroupRuleResource{}
	_ resource.ResourceWithImportState    = &SecurityGroupRuleResource{}
)

const (
	minSecurityGroupRulePort = 0
	maxSecurityGroupRulePort = 65535
)

// supportedSecurityGroupRuleProtocols lists the protocol enum the SDK accepts,
// so the schema description and the invalid-protocol diagnostic stay in step
// with networksdk.SGRProtocol.
const supportedSecurityGroupRuleProtocols = "ah, any, dccp, egp, esp, gre, hopopt, icmp, igmp, ip, " +
	"ipip, ipv6-encap, ipv6-frag, ipv6-icmp, ipv6-nonxt, ipv6-opts, ipv6-route, ospf, pgm, rsvp, " +
	"sctp, tcp, udp, udplite, vrrp"

type SecurityGroupRuleResource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewSecurityGroupRuleResource() resource.Resource {
	return &SecurityGroupRuleResource{}
}

func (r *SecurityGroupRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group_rule"
}

func (r *SecurityGroupRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud security group rule.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Security group rule ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"security_group": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Security group owning the rule.",
				Attributes:  reference.Attributes(reference.ID, "Security group ID (UUID)."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"security_group_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the security group owning the rule.",
			},
			"direction": schema.StringAttribute{
				Required:    true,
				Description: "Direction of the rule: ingress or egress.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"protocol": schema.StringAttribute{
				Required:    true,
				Description: "Protocol filter. Supported values: " + supportedSecurityGroupRuleProtocols + ".",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ethertype": schema.StringAttribute{
				Computed: true,
				// The SDK create schema carries no ethertype field, so the
				// backend alone decides the address family. Accepting a
				// configured value here would plan a value the request cannot
				// carry and fail the apply as an inconsistent result.
				Description: "Address family assigned by the backend: ipv4 or ipv6.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"port_range_min": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Lower bound of the allowed port range. It cannot be configured for ICMP protocols. Omitting it keeps whatever the backend assigned.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
					int64planmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"port_range_max": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Upper bound of the allowed port range. It cannot be configured for ICMP protocols. Omitting it keeps whatever the backend assigned.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
					int64planmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"remote_ip_prefix": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "CIDR allowed to reach the rule. Omitting it keeps whatever the backend assigned.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Description of the rule, the only attribute that can change in place. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"region": schema.StringAttribute{
				Computed:    true,
				Description: "Region name inherited from the security group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the rule was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the rule was last updated (RFC3339).",
			},
		},
	}
}

func (r *SecurityGroupRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Network
	r.projectID = data.ProjectID
}

// ValidateConfig reports structural problems while Terraform is still planning.
// buildSecurityGroupRuleCreateBody repeats these checks because a value that
// comes from another resource is unknown here and known only at apply time.
func (r *SecurityGroupRuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config SecurityGroupRuleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Direction.IsNull() && !config.Direction.IsUnknown() {
		if direction := normalizeSGRValue(config.Direction); !networksdk.SGRDirection(direction).Valid() {
			resp.Diagnostics.AddAttributeError(
				path.Root("direction"),
				"Invalid direction",
				fmt.Sprintf("direction %q is invalid; must be %q or %q.", config.Direction.ValueString(), networksdk.SGRDirectionIngress, networksdk.SGRDirectionEgress),
			)
		}
	}

	if !config.Protocol.IsNull() && !config.Protocol.IsUnknown() {
		if protocol := normalizeSGRValue(config.Protocol); !networksdk.SGRProtocol(protocol).Valid() {
			resp.Diagnostics.AddAttributeError(
				path.Root("protocol"),
				"Invalid protocol",
				fmt.Sprintf("protocol %q is not supported; must be one of %s.", config.Protocol.ValueString(), supportedSecurityGroupRuleProtocols),
			)
		}
	}

	for _, port := range []struct {
		attribute path.Path
		name      string
		value     types.Int64
	}{
		{path.Root("port_range_min"), "port_range_min", config.PortRangeMin},
		{path.Root("port_range_max"), "port_range_max", config.PortRangeMax},
	} {
		if port.value.IsNull() || port.value.IsUnknown() {
			continue
		}
		if value := port.value.ValueInt64(); value < minSecurityGroupRulePort || value > maxSecurityGroupRulePort {
			resp.Diagnostics.AddAttributeError(
				port.attribute,
				"Invalid port number",
				fmt.Sprintf("%s must be between %d and %d, got %d.", port.name, minSecurityGroupRulePort, maxSecurityGroupRulePort, value),
			)
		}
	}

	resp.Diagnostics.Append(validateSecurityGroupRulePortCompatibility(config.Protocol, config.PortRangeMin, config.PortRangeMax)...)
}

func (r *SecurityGroupRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SecurityGroupRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildSecurityGroupRuleCreateBody(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.CreateSecurityGroupRule(ctx, networksdk.CreateSecurityGroupRuleParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating security group rule", err.Error())
		return
	}

	populateSecurityGroupRuleResourceState(rule, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SecurityGroupRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SecurityGroupRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ruleID, parseDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.GetSecurityGroupRule(ctx, ruleID, networksdk.GetSecurityGroupRuleParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading security group rule", err.Error())
		return
	}

	populateSecurityGroupRuleResourceState(rule, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SecurityGroupRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state SecurityGroupRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ruleID, parseDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, changed := buildSecurityGroupRuleUpdateBody(plan, state)

	var rule *networksdk.SecurityGroupRuleSchema
	var err error
	if changed {
		rule, err = r.client.PartialUpdateSecurityGroupRule(ctx, ruleID, networksdk.PartialUpdateSecurityGroupRuleParams{
			ProjectID: r.projectID,
		}, body)
	} else {
		rule, err = r.client.GetSecurityGroupRule(ctx, ruleID, networksdk.GetSecurityGroupRuleParams{
			ProjectID: r.projectID,
		})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating security group rule", err.Error())
		return
	}

	newState := plan
	populateSecurityGroupRuleResourceState(rule, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *SecurityGroupRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SecurityGroupRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ruleID, parseDiags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSecurityGroupRule(ctx, ruleID, networksdk.DeleteSecurityGroupRuleParams{
		ProjectID: r.projectID,
	})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting security group rule", err.Error())
	}
}

func (r *SecurityGroupRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildSecurityGroupRuleCreateBody(plan SecurityGroupRuleResourceModel) (networksdk.SecurityGroupRuleCreateSchema, diag.Diagnostics) {
	var body networksdk.SecurityGroupRuleCreateSchema
	var diags diag.Diagnostics

	securityGroupID, parseDiags := parse.UUIDString(reference.Get(plan.SecurityGroup, reference.ID), "security_group.id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return body, diags
	}
	body.SecurityGroupId = securityGroupID

	if plan.Direction.IsNull() || plan.Direction.IsUnknown() {
		diags.AddError("Missing direction", "direction must be configured and known.")
		return body, diags
	}
	direction := networksdk.SGRDirection(normalizeSGRValue(plan.Direction))
	if !direction.Valid() {
		diags.AddError(
			"Invalid direction",
			fmt.Sprintf("direction %q is invalid; must be %q or %q.", plan.Direction.ValueString(), networksdk.SGRDirectionIngress, networksdk.SGRDirectionEgress),
		)
		return body, diags
	}
	body.Direction = direction

	if plan.Protocol.IsNull() || plan.Protocol.IsUnknown() {
		diags.AddError("Missing protocol", "protocol must be configured and known.")
		return body, diags
	}
	protocol := networksdk.SGRProtocol(normalizeSGRValue(plan.Protocol))
	if !protocol.Valid() {
		diags.AddError(
			"Invalid protocol",
			fmt.Sprintf("protocol %q is not supported; must be one of %s.", plan.Protocol.ValueString(), supportedSecurityGroupRuleProtocols),
		)
		return body, diags
	}
	body.Protocol = &protocol

	for _, port := range []struct {
		name  string
		value types.Int64
	}{
		{"port_range_min", plan.PortRangeMin},
		{"port_range_max", plan.PortRangeMax},
	} {
		if port.value.IsNull() || port.value.IsUnknown() {
			continue
		}
		if value := port.value.ValueInt64(); value < minSecurityGroupRulePort || value > maxSecurityGroupRulePort {
			diags.AddError(
				"Invalid port number",
				fmt.Sprintf("%s must be between %d and %d, got %d.", port.name, minSecurityGroupRulePort, maxSecurityGroupRulePort, value),
			)
		}
	}
	diags.Append(validateSecurityGroupRulePortCompatibility(plan.Protocol, plan.PortRangeMin, plan.PortRangeMax)...)
	if diags.HasError() {
		return body, diags
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}

	if !plan.RemoteIPPrefix.IsNull() && !plan.RemoteIPPrefix.IsUnknown() {
		remoteIPPrefix := strings.TrimSpace(plan.RemoteIPPrefix.ValueString())
		body.RemoteIpPrefix = &remoteIPPrefix
	}

	if !plan.PortRangeMin.IsNull() && !plan.PortRangeMin.IsUnknown() {
		portRangeMin := int(plan.PortRangeMin.ValueInt64())
		body.PortRangeMin = &portRangeMin
	}

	if !plan.PortRangeMax.IsNull() && !plan.PortRangeMax.IsUnknown() {
		portRangeMax := int(plan.PortRangeMax.ValueInt64())
		body.PortRangeMax = &portRangeMax
	}

	return body, diags
}

// buildSecurityGroupRuleUpdateBody reports whether the plan asks for a change
// the PATCH endpoint can carry. An unconfigured description is omitted, because
// the endpoint reads omission as preserve and an empty string as clear.
func buildSecurityGroupRuleUpdateBody(
	plan SecurityGroupRuleResourceModel,
	state SecurityGroupRuleResourceModel,
) (networksdk.SecurityGroupRulePartialUpdateSchema, bool) {
	var body networksdk.SecurityGroupRulePartialUpdateSchema

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		description := plan.Description.ValueString()
		body.Description = &description
		return body, true
	}
	return body, false
}

func validateSecurityGroupRulePortCompatibility(protocol types.String, minPort, maxPort types.Int64) diag.Diagnostics {
	var diags diag.Diagnostics
	if !protocol.IsNull() && !protocol.IsUnknown() {
		normalizedProtocol := networksdk.SGRProtocol(normalizeSGRValue(protocol))
		if (normalizedProtocol == networksdk.SGRProtocolIcmp || normalizedProtocol == networksdk.SGRProtocolIpv6Icmp) &&
			((!minPort.IsNull() && !minPort.IsUnknown()) || (!maxPort.IsNull() && !maxPort.IsUnknown())) {
			diags.AddError(
				"Invalid port range for ICMP",
				"port_range_min and port_range_max cannot be configured when protocol is icmp or ipv6-icmp.",
			)
			return diags
		}
	}

	if minPort.IsNull() || minPort.IsUnknown() || maxPort.IsNull() || maxPort.IsUnknown() {
		return diags
	}
	if minPort.ValueInt64() > maxPort.ValueInt64() {
		diags.AddError(
			"Invalid port range",
			fmt.Sprintf("port_range_min (%d) must be less than or equal to port_range_max (%d).", minPort.ValueInt64(), maxPort.ValueInt64()),
		)
	}
	return diags
}

func normalizeSGRValue(v types.String) string {
	return strings.ToLower(strings.TrimSpace(v.ValueString()))
}
