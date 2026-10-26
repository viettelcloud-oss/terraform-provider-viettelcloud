package network

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type SecurityGroupModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
	RegionID    types.String `tfsdk:"region_id"`
	ProjectID   types.String `tfsdk:"project_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// SecurityGroupResourceModel references its region as an object, while the
// data source keeps the region name as a plain lookup argument.
type SecurityGroupResourceModel struct {
	SecurityGroupModel
	Region types.Object `tfsdk:"region"`
}

type SecurityGroupDataSourceModel struct {
	SecurityGroupModel
	Region types.String `tfsdk:"region"`
}

func populateSecurityGroupModel(sg *networksdk.SecurityGroupSchema, m *SecurityGroupModel) {
	if sg == nil {
		return
	}

	m.ID = types.StringValue(sg.Id.String())
	m.Name = types.StringValue(sg.Name)
	m.DisplayName = types.StringValue(sg.DisplayName)
	m.IsDefault = types.BoolValue(sg.IsDefault)
	m.RegionID = types.StringValue(sg.Region.Id.String())
	m.ProjectID = types.StringValue(sg.Project.Id.String())
	m.CreatedAt = types.StringValue(sg.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(sg.UpdatedAt.Format(time.RFC3339))

	if sg.Description != nil {
		m.Description = types.StringValue(*sg.Description)
	} else {
		m.Description = types.StringNull()
	}
}

func populateSecurityGroupResourceState(sg *networksdk.SecurityGroupSchema, state *SecurityGroupResourceModel) {
	if sg == nil {
		return
	}

	configuredName := state.Name
	populateSecurityGroupModel(sg, &state.SecurityGroupModel)
	// Keep the configured representation when the backend only trims outer
	// whitespace, otherwise Terraform reports an inconsistent result.
	if keepConfiguredSecurityGroupValue(configuredName, sg.Name) {
		state.Name = configuredName
	}
	state.Region = regionReferenceState(state.Region, sg.Region.Name)
}

func populateSecurityGroupDataSourceState(sg *networksdk.SecurityGroupSchema, state *SecurityGroupDataSourceModel) {
	if sg == nil {
		return
	}

	populateSecurityGroupModel(sg, &state.SecurityGroupModel)
	state.Region = types.StringValue(sg.Region.Name)
}

func keepConfiguredSecurityGroupValue(configured types.String, backend string) bool {
	return !configured.IsNull() && !configured.IsUnknown() &&
		strings.TrimSpace(configured.ValueString()) == backend
}
