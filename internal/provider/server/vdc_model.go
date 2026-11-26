package server

import (
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type VDCDataSourceModel struct {
	ID          types.String  `tfsdk:"id"`
	Name        types.String  `tfsdk:"name"`
	Region      types.String  `tfsdk:"region"`
	Zone        types.String  `tfsdk:"zone"`
	RegionID    types.String  `tfsdk:"region_id"`
	ZoneID      types.String  `tfsdk:"zone_id"`
	Description types.String  `tfsdk:"description"`
	Ratio       types.Float64 `tfsdk:"ratio"`
	Stats       types.List    `tfsdk:"stats"`
}

func vdcStatAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":  types.StringType,
		"label": types.StringType,
		"used":  types.Float64Type,
		"total": types.Float64Type,
		"unit":  types.StringType,
	}
}

func populateVDCDataSourceState(vdc *serversdk.VDCSchema, state *VDCDataSourceModel) {
	if vdc == nil {
		return
	}

	state.ID = types.StringValue(vdc.Id.String())
	state.Name = types.StringValue(vdc.Name)
	state.Region = types.StringValue(vdc.Region.Name)
	state.Zone = types.StringValue(vdc.Zone.Name)
	state.RegionID = types.StringValue(vdc.Region.Id.String())
	state.ZoneID = types.StringValue(vdc.Zone.Id.String())
	state.Description = types.StringPointerValue(core.NullableToPtr(vdc.Description))
	state.Ratio = float32Value(vdc.Ratio)
	state.Stats = vdcStats(vdc.Stats)
}

// vdcStats keeps the response order, which is ram, cpus, gpus, servers.
func vdcStats(stats []serversdk.VDCStatSchema) types.List {
	elements := make([]attr.Value, 0, len(stats))
	for _, stat := range stats {
		elements = append(elements, types.ObjectValueMust(vdcStatAttributeTypes(), map[string]attr.Value{
			"name":  types.StringValue(stat.Name),
			"label": types.StringValue(stat.Label),
			"used":  float32Value(stat.Used),
			"total": float32Value(stat.Total),
			"unit":  types.StringPointerValue(core.NullableToPtr(stat.Unit)),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: vdcStatAttributeTypes()}, elements)
}

// float32Value converts through the shortest decimal form of the float32, so
// 1.2 stays 1.2 instead of becoming 1.2000000476837158.
func float32Value(v float32) types.Float64 {
	parsed, err := strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	if err != nil {
		return types.Float64Value(float64(v))
	}
	return types.Float64Value(parsed)
}
