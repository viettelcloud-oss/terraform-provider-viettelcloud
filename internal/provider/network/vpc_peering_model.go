package network

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type VPCPeeringModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	Status            types.String `tfsdk:"status"`
	VPCPeeringType    types.String `tfsdk:"vpc_peering_type"`
	AccepterProjectID types.String `tfsdk:"accepter_project_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

// VPCPeeringResourceModel references both VPCs as objects, while the data
// source keeps their IDs as plain lookup arguments and results.
type VPCPeeringResourceModel struct {
	VPCPeeringModel
	RequesterVPC types.Object `tfsdk:"requester_vpc"`
	AccepterVPC  types.Object `tfsdk:"accepter_vpc"`
}

type VPCPeeringDataSourceModel struct {
	VPCPeeringModel
	RequesterVPCID types.String `tfsdk:"requester_vpc_id"`
	AccepterVPCID  types.String `tfsdk:"accepter_vpc_id"`
}

func populateVPCPeeringModel(peering *networksdk.VPCPeeringSchema, m *VPCPeeringModel) {
	if peering == nil {
		return
	}

	m.ID = types.StringValue(peering.Id.String())
	m.Name = types.StringValue(peering.Name)
	m.Description = types.StringPointerValue(core.NullableToPtr(peering.Description))
	m.Status = types.StringValue(string(peering.Status))
	m.VPCPeeringType = types.StringValue(string(peering.VpcPeeringType))
	m.AccepterProjectID = types.StringValue(peering.AccepterVpc.Project.Id.String())
	m.CreatedAt = types.StringValue(peering.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(peering.UpdatedAt.Format(time.RFC3339))
}

func populateVPCPeeringResourceState(peering *networksdk.VPCPeeringSchema, state *VPCPeeringResourceModel) {
	if peering == nil {
		return
	}

	configuredDescription := state.Description
	populateVPCPeeringModel(peering, &state.VPCPeeringModel)
	if description := core.NullableToPtr(peering.Description); description != nil {
		state.Description = compare.PreservedString(configuredDescription, *description)
	}
	// Keep each configured reference only while its parsed UUID still names
	// the VPC in the response, so an upper-case ID plans no change.
	state.RequesterVPC = reference.Value(reference.ID, compare.PreservedUUID(reference.Get(state.RequesterVPC, reference.ID), peering.RequesterVpc.Id))
	state.AccepterVPC = reference.Value(reference.ID, compare.PreservedUUID(reference.Get(state.AccepterVPC, reference.ID), peering.AccepterVpc.Id))
}

func populateVPCPeeringDataSourceState(peering *networksdk.VPCPeeringSchema, state *VPCPeeringDataSourceModel) {
	if peering == nil {
		return
	}

	populateVPCPeeringModel(peering, &state.VPCPeeringModel)
	state.RequesterVPCID = types.StringValue(peering.RequesterVpc.Id.String())
	state.AccepterVPCID = types.StringValue(peering.AccepterVpc.Id.String())
}
