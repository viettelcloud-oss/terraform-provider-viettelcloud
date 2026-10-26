package network

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type SubnetModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	CIDR        types.String `tfsdk:"cidr"`
	VPCName     types.String `tfsdk:"vpc_name"`
	Region      types.String `tfsdk:"region"`
	DisplayName types.String `tfsdk:"display_name"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// SubnetResourceModel references its VPC as an object, while the data source
// keeps the VPC ID as a plain lookup argument and result.
type SubnetResourceModel struct {
	SubnetModel
	VPC types.Object `tfsdk:"vpc"`
}

type SubnetDataSourceModel struct {
	SubnetModel
	VPCID types.String `tfsdk:"vpc_id"`
}

func populateSubnetModel(subnet *networksdk.SubnetSchema, m *SubnetModel) {
	if subnet == nil {
		return
	}

	m.ID = types.StringValue(subnet.Id.String())
	m.Name = types.StringValue(subnet.Name)
	m.CIDR = types.StringValue(subnet.Cidr)
	m.VPCName = types.StringValue(subnet.Vpc.Name)
	m.Region = types.StringValue(subnet.Region.Name)
	m.DisplayName = types.StringValue(subnet.DisplayName)
	m.CreatedAt = types.StringValue(subnet.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(subnet.UpdatedAt.Format(time.RFC3339))

	m.Description = types.StringPointerValue(subnet.Description)
}

func populateSubnetResourceState(subnet *networksdk.SubnetSchema, state *SubnetResourceModel) {
	if subnet == nil {
		return
	}

	originalName := state.Name
	populateSubnetModel(subnet, &state.SubnetModel)

	if !originalName.IsNull() && !originalName.IsUnknown() && strings.TrimSpace(originalName.ValueString()) == subnet.Name {
		state.Name = originalName
	}
	// Keep the original reference only when its parsed UUID still identifies
	// the VPC returned by the backend.
	if !compare.SameUUID(reference.Get(state.VPC, reference.ID), subnet.Vpc.Id) {
		state.VPC = reference.Value(reference.ID, types.StringValue(subnet.Vpc.Id.String()))
	}
}

func populateSubnetDataSourceState(subnet *networksdk.SubnetSchema, state *SubnetDataSourceModel) {
	if subnet == nil {
		return
	}

	populateSubnetModel(subnet, &state.SubnetModel)
	state.VPCID = types.StringValue(subnet.Vpc.Id.String())
}
