package network

import (
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type ElasticIPModel struct {
	ID          types.String `tfsdk:"id"`
	Description types.String `tfsdk:"description"`
	EnableIPv4  types.Bool   `tfsdk:"enable_ipv4"`
	EnableIPv6  types.Bool   `tfsdk:"enable_ipv6"`
	IPAddress   types.String `tfsdk:"ip_address"`
	IPv6Address types.String `tfsdk:"ipv6_address"`
	Status      types.String `tfsdk:"status"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// ElasticIPResourceModel references its region as an object, while the data
// source keeps the region name as a plain lookup argument.
type ElasticIPResourceModel struct {
	ElasticIPModel
	Region types.Object `tfsdk:"region"`
}

type ElasticIPDataSourceModel struct {
	ElasticIPModel
	Region types.String `tfsdk:"region"`
	// Available filters on attachment state; the API returns no matching field,
	// so it stays as configured.
	Available types.Bool `tfsdk:"available"`
}

func populateElasticIPModel(eip *networksdk.ElasticIPDetailSchema, m *ElasticIPModel) {
	if eip == nil {
		return
	}

	m.ID = types.StringValue(eip.Id.String())
	m.EnableIPv4 = types.BoolValue(eip.EnableIpv4)
	m.EnableIPv6 = types.BoolValue(eip.EnableIpv6)
	m.CreatedAt = types.StringValue(eip.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(eip.UpdatedAt.Format(time.RFC3339))

	if eip.Description != nil {
		m.Description = types.StringValue(*eip.Description)
	} else {
		m.Description = types.StringNull()
	}
	if ipAddress := core.NullableToPtr(eip.IpAddress); ipAddress != nil {
		m.IPAddress = types.StringValue(*ipAddress)
	} else {
		m.IPAddress = types.StringNull()
	}
	if ipv6Address := core.NullableToPtr(eip.Ipv6Address); ipv6Address != nil {
		m.IPv6Address = types.StringValue(*ipv6Address)
	} else {
		m.IPv6Address = types.StringNull()
	}
	if status := core.NullableToPtr(eip.Status); status != nil {
		m.Status = types.StringValue(fmt.Sprint(*status))
	} else {
		m.Status = types.StringNull()
	}
}

func populateElasticIPResourceState(eip *networksdk.ElasticIPDetailSchema, state *ElasticIPResourceModel) {
	if eip == nil {
		return
	}

	configuredDescription := state.Description
	populateElasticIPModel(eip, &state.ElasticIPModel)
	state.Description = preservedNullableString(configuredDescription, state.Description)
	state.Region = regionReferenceState(state.Region, eip.Region.Name)
}

func populateElasticIPDataSourceState(eip *networksdk.ElasticIPDetailSchema, state *ElasticIPDataSourceModel) {
	if eip == nil {
		return
	}

	configuredDescription := state.Description
	populateElasticIPModel(eip, &state.ElasticIPModel)
	state.Description = preservedNullableString(configuredDescription, state.Description)
	state.Region = types.StringValue(eip.Region.Name)
}
