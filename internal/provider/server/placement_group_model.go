package server

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type PlacementGroupModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Policy      types.String `tfsdk:"policy"`
	RegionID    types.String `tfsdk:"region_id"`
	Project     types.String `tfsdk:"project"`
	ServerCount types.Int64  `tfsdk:"server_count"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

type PlacementGroupResourceModel struct {
	PlacementGroupModel
	Region types.Object `tfsdk:"region"`
}

type PlacementGroupDataSourceModel struct {
	PlacementGroupModel
	Region types.String `tfsdk:"region"`
}

func populatePlacementGroupModel(pg *serversdk.PlacementGroupSchema, m *PlacementGroupModel) {
	if pg == nil {
		return
	}

	m.ID = types.StringValue(pg.Id.String())
	m.Name = types.StringValue(pg.Name)
	m.Description = types.StringValue(pg.Description)
	m.RegionID = types.StringValue(pg.Region.Id.String())
	m.Project = types.StringValue(pg.Project.Name)
	m.CreatedAt = types.StringValue(pg.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(pg.UpdatedAt.Format(time.RFC3339))

	if pg.Policy != nil {
		m.Policy = types.StringValue(string(*pg.Policy))
	} else {
		m.Policy = types.StringNull()
	}
	if pg.ServerCount != nil {
		m.ServerCount = types.Int64Value(int64(*pg.ServerCount))
	} else {
		m.ServerCount = types.Int64Null()
	}
}

func populatePlacementGroupResourceState(pg *serversdk.PlacementGroupSchema, state *PlacementGroupResourceModel) {
	if pg == nil {
		return
	}

	configuredName := state.Name
	configuredRegion := reference.Get(state.Region, reference.Name)
	populatePlacementGroupModel(pg, &state.PlacementGroupModel)
	state.Region = reference.Value(reference.Name, types.StringValue(pg.Region.Name))

	// buildPlacementGroupCreateBody and buildPlacementGroupUpdateBody trim outer
	// whitespace from name and region before every request, so the backend
	// reports the trimmed form; preserve the configured representation so
	// Terraform does not report drift.
	if !configuredName.IsNull() && !configuredName.IsUnknown() &&
		strings.TrimSpace(configuredName.ValueString()) == pg.Name {
		state.Name = configuredName
	}
	if !configuredRegion.IsNull() && !configuredRegion.IsUnknown() &&
		strings.TrimSpace(configuredRegion.ValueString()) == pg.Region.Name {
		state.Region = reference.Value(reference.Name, configuredRegion)
	}
}

func populatePlacementGroupDataSourceState(pg *serversdk.PlacementGroupSchema, state *PlacementGroupDataSourceModel) {
	if pg == nil {
		return
	}

	populatePlacementGroupModel(pg, &state.PlacementGroupModel)
	state.Region = types.StringValue(pg.Region.Name)
}
