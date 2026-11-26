package project

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// CustomRoleModel is the state of both the resource and the data source,
// which expose the same attributes.
type CustomRoleModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Actions     types.Set    `tfsdk:"actions"`
	URN         types.String `tfsdk:"urn"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// populateCustomRoleModel maps a custom role response into m. Each action
// keeps only its URN, the criterion that the practitioner configures.
func populateCustomRoleModel(role *projectsdk.CustomRoleDetailSchema, m *CustomRoleModel) {
	if role == nil {
		return
	}

	m.ID = types.StringValue(role.Id.String())
	m.Name = types.StringValue(role.Name)
	m.DisplayName = types.StringValue(role.DisplayName)
	m.Description = types.StringValue(role.Description)
	m.Enabled = types.BoolValue(role.Enabled)
	m.Actions = reference.Set(reference.URN, customRoleActionURNs(role.Actions))
	m.URN = types.StringValue(role.Urn)
	m.CreatedAt = types.StringValue(role.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(role.UpdatedAt.Format(time.RFC3339))
}

// populateCustomRoleResourceState maps a response into the resource state, and
// keeps the prior display_name, description, and actions[*].urn when they
// differ from the response only by outer whitespace. The request trims these
// values, so the trimmed response value would otherwise show as drift.
func populateCustomRoleResourceState(role *projectsdk.CustomRoleDetailSchema, state *CustomRoleModel) {
	if role == nil {
		return
	}
	priorDisplayName, priorDescription := state.DisplayName, state.Description
	var priorURNs []types.String
	if !state.Actions.IsNull() && !state.Actions.IsUnknown() {
		priorURNs = reference.Strings(state.Actions, reference.URN)
	}
	populateCustomRoleModel(role, state)
	if compare.SameTrimmed(priorDisplayName, role.DisplayName) {
		state.DisplayName = priorDisplayName
	}
	if compare.SameTrimmed(priorDescription, role.Description) {
		state.Description = priorDescription
	}
	state.Actions = customRoleActionsState(role.Actions, priorURNs)
}

// customRoleActionsState maps the response actions to the resource actions
// set. Each action keeps a configured URN that differs from the response URN
// only by outer whitespace. Other actions take the response URN, so that an
// action added or removed outside Terraform still shows as drift.
func customRoleActionsState(actions []projectsdk.NestedActionSchema, configured []types.String) types.Set {
	elements := make([]attr.Value, 0, len(actions))
	for _, action := range actions {
		urn := types.StringValue(action.Urn)
		for _, candidate := range configured {
			if compare.SameTrimmed(candidate, action.Urn) {
				urn = candidate
				break
			}
		}
		elements = append(elements, reference.Value(reference.URN, urn))
	}
	return types.SetValueMust(reference.ObjectType(reference.URN), elements)
}

func customRoleActionURNs(actions []projectsdk.NestedActionSchema) []string {
	urns := make([]string, 0, len(actions))
	for _, action := range actions {
		urns = append(urns, action.Urn)
	}
	return urns
}
