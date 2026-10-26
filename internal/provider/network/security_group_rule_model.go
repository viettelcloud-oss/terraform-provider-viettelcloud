package network

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type SecurityGroupRuleModel struct {
	ID                types.String `tfsdk:"id"`
	SecurityGroup     types.Object `tfsdk:"security_group"`
	SecurityGroupName types.String `tfsdk:"security_group_name"`
	Direction         types.String `tfsdk:"direction"`
	Protocol          types.String `tfsdk:"protocol"`
	Ethertype         types.String `tfsdk:"ethertype"`
	PortRangeMin      types.Int64  `tfsdk:"port_range_min"`
	PortRangeMax      types.Int64  `tfsdk:"port_range_max"`
	RemoteIPPrefix    types.String `tfsdk:"remote_ip_prefix"`
	Description       types.String `tfsdk:"description"`
	Region            types.String `tfsdk:"region"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

type SecurityGroupRuleResourceModel = SecurityGroupRuleModel

// populateSecurityGroupRuleModel maps the API response to the shared model.
func populateSecurityGroupRuleModel(rule *networksdk.SecurityGroupRuleSchema, m *SecurityGroupRuleModel) {
	if rule == nil {
		return
	}

	m.ID = types.StringValue(rule.Id.String())
	m.SecurityGroup = reference.Value(reference.ID, types.StringValue(rule.SecurityGroup.Id.String()))
	m.SecurityGroupName = types.StringValue(rule.SecurityGroup.Name)
	m.Direction = types.StringValue(string(rule.Direction))
	m.Description = types.StringPointerValue(rule.Description)
	m.Region = types.StringValue(rule.Region.Name)
	m.CreatedAt = types.StringValue(rule.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(rule.UpdatedAt.Format(time.RFC3339))

	if rule.Protocol != nil {
		m.Protocol = types.StringValue(string(*rule.Protocol))
	} else {
		m.Protocol = types.StringNull()
	}

	if rule.Ethertype != nil {
		m.Ethertype = types.StringValue(string(*rule.Ethertype))
	} else {
		m.Ethertype = types.StringNull()
	}

	if rule.PortRangeMin != nil {
		m.PortRangeMin = types.Int64Value(int64(*rule.PortRangeMin))
	} else {
		m.PortRangeMin = types.Int64Null()
	}

	if rule.PortRangeMax != nil {
		m.PortRangeMax = types.Int64Value(int64(*rule.PortRangeMax))
	} else {
		m.PortRangeMax = types.Int64Null()
	}

	// remote_ip_prefix is a plain string in the response, and the backend
	// reports a rule without a remote prefix as an empty string. An empty
	// string is not a CIDR, so state carries null instead.
	if rule.RemoteIpPrefix != "" {
		m.RemoteIPPrefix = types.StringValue(rule.RemoteIpPrefix)
	} else {
		m.RemoteIPPrefix = types.StringNull()
	}
}

// populateSecurityGroupRuleResourceState maps the API response into resource
// state, keeping the practitioner's representation of any value the backend
// reports as semantically equal.
func populateSecurityGroupRuleResourceState(rule *networksdk.SecurityGroupRuleSchema, state *SecurityGroupRuleResourceModel) {
	if rule == nil {
		return
	}

	configuredSecurityGroup := state.SecurityGroup
	configuredDirection := state.Direction
	configuredProtocol := state.Protocol
	configuredRemoteIPPrefix := state.RemoteIPPrefix

	populateSecurityGroupRuleModel(rule, state)

	if compare.SameUUID(reference.Get(configuredSecurityGroup, reference.ID), rule.SecurityGroup.Id) {
		state.SecurityGroup = configuredSecurityGroup
	}
	if sameNormalizedSGRValue(configuredDirection, string(rule.Direction)) {
		state.Direction = configuredDirection
	}
	if rule.Protocol != nil && sameNormalizedSGRValue(configuredProtocol, string(*rule.Protocol)) {
		state.Protocol = configuredProtocol
	}
	if !configuredRemoteIPPrefix.IsNull() && !configuredRemoteIPPrefix.IsUnknown() &&
		strings.TrimSpace(configuredRemoteIPPrefix.ValueString()) == rule.RemoteIpPrefix {
		state.RemoteIPPrefix = configuredRemoteIPPrefix
	}
}

// sameNormalizedSGRValue reports whether a configured value names the backend
// value, because the provider trims and lowercases direction and protocol
// before sending them and the backend echoes the normalized form.
func sameNormalizedSGRValue(configured types.String, backend string) bool {
	if configured.IsNull() || configured.IsUnknown() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(configured.ValueString()), backend)
}
