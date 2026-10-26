package network

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type VPCModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	CIDR        types.String `tfsdk:"cidr"`
	DisplayName types.String `tfsdk:"display_name"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// VPCResourceModel references its region as an object, while the data source
// keeps the region name as a plain lookup argument.
type VPCResourceModel struct {
	VPCModel
	Region types.Object `tfsdk:"region"`
}

type VPCDataSourceModel struct {
	VPCModel
	Region types.String `tfsdk:"region"`
}

func populateVPCModel(vpc *networksdk.VPCSchema, m *VPCModel) {
	if vpc == nil {
		return
	}

	m.ID = types.StringValue(vpc.Id.String())
	m.Name = types.StringValue(vpc.Name)
	m.DisplayName = types.StringValue(vpc.DisplayName)
	m.CIDR = types.StringValue(vpc.Cidr)
	m.CreatedAt = types.StringValue(vpc.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(vpc.UpdatedAt.Format(time.RFC3339))

	m.Description = types.StringPointerValue(vpc.Description)
}

func populateVPCResourceState(vpc *networksdk.VPCSchema, state *VPCResourceModel) {
	if vpc == nil {
		return
	}

	configuredName := state.Name
	populateVPCModel(vpc, &state.VPCModel)
	// Keep the configured representation when the backend only trims outer
	// whitespace, otherwise Terraform reports an inconsistent result.
	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == vpc.Name {
		state.Name = configuredName
	}
	state.Region = regionReferenceState(state.Region, vpc.Region.Name)
}

func populateVPCDataSourceState(vpc *networksdk.VPCSchema, state *VPCDataSourceModel) {
	if vpc == nil {
		return
	}

	populateVPCModel(vpc, &state.VPCModel)
	state.Region = types.StringValue(vpc.Region.Name)
}
