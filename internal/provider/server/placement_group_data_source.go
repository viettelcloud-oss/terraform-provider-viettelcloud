package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
)

var _ datasource.DataSource = &PlacementGroupDataSource{}

type PlacementGroupDataSource struct {
	client    *serversdk.Client
	projectID core.UUID
}

func NewPlacementGroupDataSource() datasource.DataSource {
	return &PlacementGroupDataSource{}
}

func (d *PlacementGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_placement_group"
}

func (d *PlacementGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud Placement Group by ID, name, region, or policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Placement Group ID (UUID). Exactly one Placement Group must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Placement Group name to filter by.",
			},
			"policy": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Placement Group policy to filter by: affinity, anti-affinity, soft-affinity, or soft-anti-affinity.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name where the Placement Group resides to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Placement Group description.",
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID) backing the configured region.",
			},
			"project": schema.StringAttribute{
				Computed:    true,
				Description: "Project name owning the Placement Group.",
			},
			"server_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of servers currently assigned to the Placement Group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Placement Group was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Placement Group was last updated (RFC3339).",
			},
		},
	}
}

func (d *PlacementGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.Server
	d.projectID = data.ProjectID
}

func (d *PlacementGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PlacementGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pg, diags := d.getPlacementGroup(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || pg == nil {
		return
	}

	state := config
	populatePlacementGroupDataSourceState(pg, &state)
	preserveConfiguredPlacementGroupValues(pg, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *PlacementGroupDataSource) getPlacementGroup(ctx context.Context, config PlacementGroupDataSourceModel) (*serversdk.PlacementGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasPolicy := !config.Policy.IsNull() && !config.Policy.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()

	if !hasID && !hasName && !hasPolicy && !hasRegion {
		diags.AddError(
			"Missing Placement Group lookup criteria",
			"Specify at least one of id, name, policy, or region to look up a Placement Group.",
		)
		return nil, diags
	}
	criteria := []struct {
		set   bool
		field string
		value types.String
	}{
		{hasID, "id", config.ID},
		{hasName, "name", config.Name},
		{hasPolicy, "policy", config.Policy},
		{hasRegion, "region", config.Region},
	}
	for _, criterion := range criteria {
		if criterion.set && strings.TrimSpace(criterion.value.ValueString()) == "" {
			diags.AddError(
				"Invalid Placement Group lookup criteria",
				fmt.Sprintf("%s must not be empty or whitespace-only.", criterion.field),
			)
		}
	}
	if policy := placementGroupPolicyCriterion(config.Policy); hasPolicy && policy != "" && !policy.Valid() {
		diags.AddError(
			"Invalid Placement Group lookup criteria",
			fmt.Sprintf("policy %q is not one of affinity, anti-affinity, soft-affinity, soft-anti-affinity.", config.Policy.ValueString()),
		)
	}
	if diags.HasError() {
		return nil, diags
	}

	if hasID && !hasName && !hasPolicy && !hasRegion {
		pgID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		return d.getPlacementGroupByID(ctx, pgID)
	}

	return d.getPlacementGroupByFilter(ctx, config)
}

func (d *PlacementGroupDataSource) getPlacementGroupByID(ctx context.Context, pgID core.UUID) (*serversdk.PlacementGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	pg, err := d.client.GetPlacementGroup(ctx, pgID, serversdk.GetPlacementGroupParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			diags.AddError("Placement Group not found", fmt.Sprintf("No Placement Group found with id %s.", pgID))
			return nil, diags
		}
		diags.AddError("Error reading Placement Group", err.Error())
		return nil, diags
	}
	return pg, diags
}

func (d *PlacementGroupDataSource) getPlacementGroupByFilter(ctx context.Context, config PlacementGroupDataSourceModel) (*serversdk.PlacementGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	pgID, idDiags := parse.OptionalUUIDString(config.ID, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return nil, diags
	}

	filter := serverlookup.PlacementGroupFilter{ID: pgID}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if !config.Policy.IsNull() && !config.Policy.IsUnknown() {
		value := placementGroupPolicyCriterion(config.Policy)
		filter.Policy = &value
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		region := config.Region.ValueString()
		filter.Region = &region
	}

	candidate, err := serverlookup.NewPlacementGroupFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving Placement Group", err.Error())
		return nil, diags
	}

	// A list row carries the same PlacementGroupSchema as a direct read, so the
	// resolved candidate already holds every attribute the schema exposes and a
	// second call by ID would only cost a round trip. The real-API test compares
	// a name lookup against the ID lookup attribute by attribute to keep that
	// assumption honest.
	return &candidate, diags
}

func preserveConfiguredPlacementGroupValues(
	pg *serversdk.PlacementGroupSchema,
	config PlacementGroupDataSourceModel,
	state *PlacementGroupDataSourceModel,
) {
	if pg == nil {
		return
	}
	if compare.SameUUID(config.ID, pg.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() &&
		strings.TrimSpace(config.Name.ValueString()) == pg.Name {
		state.Name = config.Name
	}
	if policy := core.NullableToPtr(pg.Policy); !config.Policy.IsNull() && !config.Policy.IsUnknown() && policy != nil &&
		placementGroupPolicyCriterion(config.Policy) == *policy {
		state.Policy = config.Policy
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() &&
		strings.TrimSpace(config.Region.ValueString()) == pg.Region.Name {
		state.Region = config.Region
	}
}

// placementGroupPolicyCriterion reads the configured policy the way every other
// string criterion is read: surrounding whitespace is not part of the value.
func placementGroupPolicyCriterion(configured types.String) serversdk.PlacementGroupPolicy {
	return serversdk.PlacementGroupPolicy(strings.TrimSpace(configured.ValueString()))
}
