package loadbalancer

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
)

type ServerGroupMemberModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Address            types.String `tfsdk:"address"`
	ProtocolPort       types.Int64  `tfsdk:"protocol_port"`
	Weight             types.Int64  `tfsdk:"weight"`
	Backup             types.Bool   `tfsdk:"backup"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	Status             types.String `tfsdk:"status"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

// ServerGroupMemberResourceModel references its server group as an object,
// while the data source keeps the server group ID as a plain lookup scope.
type ServerGroupMemberResourceModel struct {
	ServerGroupMemberModel
	ServerGroup types.Object `tfsdk:"server_group"`
}

type ServerGroupMemberDataSourceModel struct {
	ServerGroupMemberModel
	ServerGroupID types.String `tfsdk:"server_group_id"`
}

// populateServerGroupMemberModel maps the API response to the shared model.
func populateServerGroupMemberModel(api *loadbalancersdk.ServerGroupMemberSchema, m *ServerGroupMemberModel) {
	if api == nil {
		return
	}

	m.ID = types.StringValue(api.Id.String())
	m.Name = types.StringValue(api.Name)
	m.Address = types.StringValue(api.Address)
	m.ProtocolPort = types.Int64Value(int64(api.ProtocolPort))
	m.Weight = types.Int64Value(int64(api.Weight))
	m.Backup = types.BoolValue(api.Backup)
	m.ProvisioningStatus = types.StringValue(string(api.ProvisioningStatus))
	m.Status = types.StringValue(string(api.Status))
	m.CreatedAt = types.StringValue(api.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(api.UpdatedAt.Format(time.RFC3339))
}

// populateServerGroupMemberResourceState maps the API response into resource
// state, preserving configured values that match backend values to prevent
// false drift. The response does not name the server group, so the reference
// stays as the request path set it.
func populateServerGroupMemberResourceState(api *loadbalancersdk.ServerGroupMemberSchema, state *ServerGroupMemberResourceModel) {
	if api == nil {
		return
	}

	configuredName := state.Name
	configuredAddress := state.Address

	populateServerGroupMemberModel(api, &state.ServerGroupMemberModel)

	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == api.Name {
		state.Name = configuredName
	}
	if compare.SameIPAddress(configuredAddress, api.Address) {
		state.Address = configuredAddress
	}
}

// populateServerGroupMemberDataSourceState maps the API response into data-source state.
func populateServerGroupMemberDataSourceState(api *loadbalancersdk.ServerGroupMemberSchema, state *ServerGroupMemberDataSourceModel) {
	populateServerGroupMemberModel(api, &state.ServerGroupMemberModel)
}
