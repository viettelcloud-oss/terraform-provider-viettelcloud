package network

import (
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

var privateIPAttachedVIPAttributeTypes = map[string]attr.Type{
	"id":         types.StringType,
	"ip_address": types.StringType,
}

type PrivateIPModel struct {
	ID           types.String `tfsdk:"id"`
	Description  types.String `tfsdk:"description"`
	IPAddress    types.String `tfsdk:"ip_address"`
	MACAddress   types.String `tfsdk:"mac_address"`
	SubnetName   types.String `tfsdk:"subnet_name"`
	SubnetCIDR   types.String `tfsdk:"subnet_cidr"`
	VPCID        types.String `tfsdk:"vpc_id"`
	VPCName      types.String `tfsdk:"vpc_name"`
	Region       types.String `tfsdk:"region"`
	ServerID     types.String `tfsdk:"server_id"`
	ServerName   types.String `tfsdk:"server_name"`
	DeviceOwner  types.String `tfsdk:"device_owner"`
	DisplayName  types.String `tfsdk:"display_name"`
	PortSecurity types.Bool   `tfsdk:"port_security"`
	AllowedCIDRs types.Set    `tfsdk:"allowed_cidrs"`
	AttachedVIPs types.Set    `tfsdk:"attached_vips"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
}

// PrivateIPResourceModel references its subnet and allowed VIPs as objects,
// while the data source keeps their IDs as plain strings.
type PrivateIPResourceModel struct {
	PrivateIPModel
	Subnet      types.Object `tfsdk:"subnet"`
	AllowedVIPs types.Set    `tfsdk:"allowed_vips"`
}

// VPCCIDR is lookup-only: NestedVPCSchema carries no CIDR, so the backend can
// never populate it and the configured value is passed through untouched.
type PrivateIPDataSourceModel struct {
	PrivateIPModel
	SubnetID      types.String `tfsdk:"subnet_id"`
	AllowedVIPIDs types.Set    `tfsdk:"allowed_vip_ids"`
	VPCCIDR       types.String `tfsdk:"vpc_cidr"`
}

func populatePrivateIPModel(privateIP *networksdk.PrivateIPSchema, m *PrivateIPModel) {
	if privateIP == nil {
		return
	}

	m.ID = types.StringValue(privateIP.Id.String())
	m.SubnetName = types.StringValue(privateIP.Subnet.Name)
	m.SubnetCIDR = types.StringValue(privateIP.Subnet.Cidr)
	m.VPCID = types.StringValue(privateIP.Vpc.Id.String())
	m.VPCName = types.StringValue(privateIP.Vpc.Name)
	m.Region = types.StringValue(privateIP.Region.Name)
	m.DeviceOwner = types.StringValue(string(privateIP.DeviceOwner))
	m.DisplayName = types.StringValue(privateIP.DisplayName)
	m.PortSecurity = types.BoolValue(privateIP.PortSecurity)
	m.CreatedAt = types.StringValue(privateIP.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(privateIP.UpdatedAt.Format(time.RFC3339))

	m.Description = types.StringPointerValue(privateIP.Description)
	m.IPAddress = types.StringPointerValue(privateIP.IpAddress)
	m.MACAddress = types.StringPointerValue(privateIP.MacAddress)

	if privateIP.Server != nil {
		m.ServerID = types.StringValue(privateIP.Server.Id.String())
		m.ServerName = types.StringValue(privateIP.Server.Name)
	} else {
		m.ServerID = types.StringNull()
		m.ServerName = types.StringNull()
	}

	if privateIP.AllowedCidrs != nil {
		values := make([]attr.Value, 0, len(*privateIP.AllowedCidrs))
		for _, cidr := range *privateIP.AllowedCidrs {
			values = append(values, types.StringValue(cidr))
		}
		m.AllowedCIDRs = types.SetValueMust(types.StringType, values)
	} else {
		m.AllowedCIDRs = types.SetNull(types.StringType)
	}

	attachedVIPType := types.ObjectType{AttrTypes: privateIPAttachedVIPAttributeTypes}
	if privateIP.AttachedVips != nil {
		values := make([]attr.Value, 0, len(*privateIP.AttachedVips))
		for _, vip := range *privateIP.AttachedVips {
			values = append(values, types.ObjectValueMust(privateIPAttachedVIPAttributeTypes, map[string]attr.Value{
				"id":         types.StringValue(vip.Id.String()),
				"ip_address": types.StringPointerValue(vip.IpAddress),
			}))
		}
		m.AttachedVIPs = types.SetValueMust(attachedVIPType, values)
	} else {
		m.AttachedVIPs = types.SetNull(attachedVIPType)
	}
}

// privateIPAllowedVIPIDs returns the IDs of the attached VIPs, which are the
// allowed VIPs. A nil result means the backend reported no attached VIPs.
func privateIPAllowedVIPIDs(privateIP *networksdk.PrivateIPSchema) []string {
	if privateIP.AttachedVips == nil {
		return nil
	}
	ids := make([]string, 0, len(*privateIP.AttachedVips))
	for _, vip := range *privateIP.AttachedVips {
		ids = append(ids, vip.Id.String())
	}
	return ids
}

func populatePrivateIPResourceState(privateIP *networksdk.PrivateIPSchema, state *PrivateIPResourceModel) {
	if privateIP == nil {
		return
	}

	originalIPAddress := state.IPAddress
	originalMACAddress := state.MACAddress
	populatePrivateIPModel(privateIP, &state.PrivateIPModel)

	if !compare.SameUUID(reference.Get(state.Subnet, reference.ID), privateIP.Subnet.Id) {
		state.Subnet = reference.Value(reference.ID, types.StringValue(privateIP.Subnet.Id.String()))
	}
	if privateIP.IpAddress != nil && compare.SameIPAddress(originalIPAddress, *privateIP.IpAddress) {
		state.IPAddress = originalIPAddress
	}
	if privateIP.MacAddress != nil && compare.SameMACAddress(originalMACAddress, *privateIP.MacAddress) {
		state.MACAddress = originalMACAddress
	}

	allowedVIPs := types.SetNull(reference.ObjectType(reference.ID))
	if ids := privateIPAllowedVIPIDs(privateIP); ids != nil {
		allowedVIPs = reference.Set(reference.ID, ids)
	}
	if !compare.SameUUIDSet(privateIPAllowedVIPIDSet(state.AllowedVIPs), privateIPAllowedVIPIDSet(allowedVIPs)) {
		state.AllowedVIPs = allowedVIPs
	}
}

func populatePrivateIPDataSourceState(privateIP *networksdk.PrivateIPSchema, state *PrivateIPDataSourceModel) {
	if privateIP == nil {
		return
	}

	populatePrivateIPModel(privateIP, &state.PrivateIPModel)
	state.SubnetID = types.StringValue(privateIP.Subnet.Id.String())
	state.AllowedVIPIDs = types.SetNull(types.StringType)
	if ids := privateIPAllowedVIPIDs(privateIP); ids != nil {
		values := make([]attr.Value, 0, len(ids))
		for _, id := range ids {
			values = append(values, types.StringValue(id))
		}
		state.AllowedVIPIDs = types.SetValueMust(types.StringType, values)
	}
}

// privateIPAllowedVIPIDSet returns the IDs of an allowed VIP reference set as
// a set of strings, keeping a null or unknown set null or unknown.
func privateIPAllowedVIPIDSet(allowedVIPs types.Set) types.Set {
	if allowedVIPs.IsNull() {
		return types.SetNull(types.StringType)
	}
	if allowedVIPs.IsUnknown() {
		return types.SetUnknown(types.StringType)
	}
	ids := reference.Strings(allowedVIPs, reference.ID)
	values := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	return types.SetValueMust(types.StringType, values)
}
