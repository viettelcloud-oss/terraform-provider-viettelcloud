package loadbalancer

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type L7RuleModel struct {
	ID                 types.String `tfsdk:"id"`
	Type               types.String `tfsdk:"type"`
	CompareType        types.String `tfsdk:"compare_type"`
	Value              types.String `tfsdk:"value"`
	Key                types.String `tfsdk:"key"`
	Invert             types.Bool   `tfsdk:"invert"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	Status             types.String `tfsdk:"status"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

// L7RuleResourceModel references the owning L7 policy as an object, while the
// data source scopes its lookup by a flat l7_policy_id.
type L7RuleResourceModel struct {
	L7RuleModel
	L7Policy types.Object `tfsdk:"l7_policy"`
}

type L7RuleDataSourceModel struct {
	L7RuleModel
	L7PolicyID types.String `tfsdk:"l7_policy_id"`
}

// populateL7RuleModel maps the API response to the shared model.
func populateL7RuleModel(rule *loadbalancersdk.L7RuleSchema, m *L7RuleModel) {
	if rule == nil {
		return
	}

	m.ID = types.StringValue(rule.Id.String())
	m.Type = types.StringValue(string(rule.Type))
	m.CompareType = types.StringValue(string(rule.CompareType))
	m.Value = types.StringValue(rule.Value)
	m.Invert = types.BoolValue(rule.Invert)
	m.Key = types.StringPointerValue(core.NullableToPtr(rule.Key))
	m.ProvisioningStatus = types.StringValue(string(rule.ProvisioningStatus))
	m.Status = types.StringValue(string(rule.Status))
	m.CreatedAt = types.StringValue(rule.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(rule.UpdatedAt.Format(time.RFC3339))
}

// populateL7RuleResourceState maps the API response into resource state,
// preserving configured representations when semantically identical.
func populateL7RuleResourceState(rule *loadbalancersdk.L7RuleSchema, state *L7RuleResourceModel) {
	if rule == nil {
		return
	}

	configuredID := state.ID
	configuredPolicy := state.L7Policy
	configuredType := state.Type
	configuredCompareType := state.CompareType
	configuredValue := state.Value
	configuredKey := state.Key
	configuredInvert := state.Invert

	populateL7RuleModel(rule, &state.L7RuleModel)

	if compare.SameUUID(configuredID, rule.Id) {
		state.ID = configuredID
	}
	if !compare.SameUUID(reference.Get(configuredPolicy, reference.ID), rule.L7policyId) {
		state.L7Policy = reference.Value(reference.ID, types.StringValue(rule.L7policyId.String()))
	}
	if !configuredType.IsNull() && !configuredType.IsUnknown() && strings.TrimSpace(configuredType.ValueString()) == string(rule.Type) {
		state.Type = configuredType
	}
	if !configuredCompareType.IsNull() && !configuredCompareType.IsUnknown() && strings.TrimSpace(configuredCompareType.ValueString()) == string(rule.CompareType) {
		state.CompareType = configuredCompareType
	}
	// The API trims value, so keep the configured spelling when it differs
	// from the backend value only by outer whitespace.
	state.Value = compare.PreservedString(configuredValue, rule.Value)
	if key := core.NullableToPtr(rule.Key); !configuredKey.IsNull() && !configuredKey.IsUnknown() && key != nil && strings.TrimSpace(configuredKey.ValueString()) == *key {
		state.Key = configuredKey
	}
	if !configuredInvert.IsNull() && !configuredInvert.IsUnknown() && configuredInvert.ValueBool() == rule.Invert {
		state.Invert = configuredInvert
	}
}

// populateL7RuleDataSourceState maps the API response into data-source state.
func populateL7RuleDataSourceState(rule *loadbalancersdk.L7RuleSchema, state *L7RuleDataSourceModel) {
	if rule == nil {
		return
	}
	populateL7RuleModel(rule, &state.L7RuleModel)
	state.L7PolicyID = types.StringValue(rule.L7policyId.String())
}

// preserveL7RuleConfiguredValues preserves configured values in data-source state.
func preserveL7RuleConfiguredValues(rule *loadbalancersdk.L7RuleSchema, config L7RuleDataSourceModel, state *L7RuleDataSourceModel) {
	if rule == nil {
		return
	}
	if compare.SameUUID(config.L7PolicyID, rule.L7policyId) {
		state.L7PolicyID = config.L7PolicyID
	}
	if compare.SameUUID(config.ID, rule.Id) {
		state.ID = config.ID
	}
}
