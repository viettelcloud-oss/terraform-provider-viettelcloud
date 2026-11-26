package loadbalancer

import (
	"strings"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

type L7PolicyModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	Action             types.String `tfsdk:"action"`
	Position           types.Int64  `tfsdk:"position"`
	RedirectURL        types.String `tfsdk:"redirect_url"`
	RedirectPrefix     types.String `tfsdk:"redirect_prefix"`
	RedirectHTTPCode   types.Int64  `tfsdk:"redirect_http_code"`
	RuleCount          types.Int64  `tfsdk:"rule_count"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	Status             types.String `tfsdk:"status"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

// L7PolicyResourceModel references the listener and the redirect server group
// as objects, while the data source reports both as flat IDs.
type L7PolicyResourceModel struct {
	L7PolicyModel
	Listener            types.Object `tfsdk:"listener"`
	RedirectServerGroup types.Object `tfsdk:"redirect_server_group"`
}

type L7PolicyDataSourceModel struct {
	L7PolicyModel
	ListenerID            types.String `tfsdk:"listener_id"`
	RedirectServerGroupID types.String `tfsdk:"redirect_server_group_id"`
}

func populateL7PolicyModel(policy *loadbalancersdk.L7PolicySchema, m *L7PolicyModel) {
	if policy == nil {
		return
	}

	m.ID = types.StringValue(policy.Id.String())
	m.Name = types.StringPointerValue(core.NullableToPtr(policy.Name))
	m.Description = types.StringPointerValue(core.NullableToPtr(policy.Description))
	m.Action = types.StringValue(string(policy.Action))
	m.Position = types.Int64Value(int64(policy.Position))
	m.RedirectURL = types.StringPointerValue(core.NullableToPtr(policy.RedirectUrl))
	m.RedirectPrefix = types.StringPointerValue(core.NullableToPtr(policy.RedirectPrefix))

	if code := core.NullableToPtr(policy.RedirectHttpCode); code != nil {
		m.RedirectHTTPCode = types.Int64Value(int64(*code))
	} else {
		m.RedirectHTTPCode = types.Int64Null()
	}

	m.RuleCount = types.Int64Value(int64(policy.RuleCount))
	m.ProvisioningStatus = types.StringValue(string(policy.ProvisioningStatus))
	m.Status = types.StringValue(string(policy.Status))
	m.CreatedAt = types.StringValue(policy.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(policy.UpdatedAt.Format(time.RFC3339))
}

// populateL7PolicyResourceState maps the API response into resource state,
// preserving configured representations when semantically identical.
func populateL7PolicyResourceState(policy *loadbalancersdk.L7PolicySchema, state *L7PolicyResourceModel) {
	if policy == nil {
		return
	}

	configuredName := state.Name
	configuredDescription := state.Description
	configuredListener := state.Listener
	configuredAction := state.Action
	configuredRedirectServerGroup := state.RedirectServerGroup
	configuredRedirectURL := state.RedirectURL
	configuredRedirectPrefix := state.RedirectPrefix

	populateL7PolicyModel(policy, &state.L7PolicyModel)

	state.Name = preserveL7PolicyTrimmedString(configuredName, state.Name)
	state.Description = preserveL7PolicyTrimmedString(configuredDescription, state.Description)
	if !compare.SameUUID(reference.Get(configuredListener, reference.ID), policy.ListenerId) {
		state.Listener = reference.Value(reference.ID, types.StringValue(policy.ListenerId.String()))
	}
	// Support REDIRECT_TO_POOL as alias for REDIRECT_TO_SERVER_GROUP and preserve configured case.
	if !configuredAction.IsNull() && !configuredAction.IsUnknown() {
		trimmedAction := strings.TrimSpace(configuredAction.ValueString())
		if strings.EqualFold(trimmedAction, string(policy.Action)) ||
			(strings.EqualFold(trimmedAction, l7PolicyActionPoolAlias) && policy.Action == loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP) {
			state.Action = configuredAction
		}
	}
	redirectServerGroup := core.NullableToPtr(policy.RedirectServerGroup)
	switch {
	case redirectServerGroup == nil:
		state.RedirectServerGroup = types.ObjectNull(reference.AttributeTypes(reference.ID))
	case !compare.SameUUID(reference.Get(configuredRedirectServerGroup, reference.ID), redirectServerGroup.Id):
		state.RedirectServerGroup = reference.Value(reference.ID, types.StringValue(redirectServerGroup.Id.String()))
	}
	state.RedirectURL = preserveL7PolicyTrimmedString(configuredRedirectURL, state.RedirectURL)
	state.RedirectPrefix = preserveL7PolicyTrimmedString(configuredRedirectPrefix, state.RedirectPrefix)
}

// preserveL7PolicyTrimmedString returns configured when the backend value
// equals it without surrounding whitespace, and the backend value otherwise.
// The backend trims name, description, redirect_url, and redirect_prefix, so
// a configured "web " comes back as "web".
func preserveL7PolicyTrimmedString(configured, backend types.String) types.String {
	if configured.IsNull() || configured.IsUnknown() || backend.IsNull() || backend.IsUnknown() {
		return backend
	}
	if strings.TrimSpace(configured.ValueString()) == backend.ValueString() {
		return configured
	}
	return backend
}

func populateL7PolicyDataSourceState(policy *loadbalancersdk.L7PolicySchema, state *L7PolicyDataSourceModel) {
	if policy == nil {
		return
	}
	populateL7PolicyModel(policy, &state.L7PolicyModel)
	state.ListenerID = types.StringValue(policy.ListenerId.String())
	if redirectServerGroup := core.NullableToPtr(policy.RedirectServerGroup); redirectServerGroup != nil {
		state.RedirectServerGroupID = types.StringValue(redirectServerGroup.Id.String())
	} else {
		state.RedirectServerGroupID = types.StringNull()
	}
}

func l7PolicyDetailToSchema(d *loadbalancersdk.L7PolicyDetailSchema) *loadbalancersdk.L7PolicySchema {
	if d == nil {
		return nil
	}
	return &loadbalancersdk.L7PolicySchema{
		Action:              d.Action,
		CreatedAt:           d.CreatedAt,
		Description:         d.Description,
		Id:                  d.Id,
		ListenerId:          d.ListenerId,
		Name:                d.Name,
		Position:            d.Position,
		ProvisioningStatus:  d.ProvisioningStatus,
		RedirectHttpCode:    d.RedirectHttpCode,
		RedirectPrefix:      d.RedirectPrefix,
		RedirectServerGroup: d.RedirectServerGroup,
		RedirectUrl:         d.RedirectUrl,
		RuleCount:           d.RuleCount,
		Status:              d.Status,
		UpdatedAt:           d.UpdatedAt,
	}
}
