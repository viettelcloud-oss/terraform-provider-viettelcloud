package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VolumeSnapshotPolicyDataSource{}

type VolumeSnapshotPolicyDataSource struct {
	client    *blockstoragesdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

type volumeSnapshotPolicyCriteria struct{ id, name, region, status bool }

func NewVolumeSnapshotPolicyDataSource() datasource.DataSource {
	return &VolumeSnapshotPolicyDataSource{}
}

func (d *VolumeSnapshotPolicyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_snapshot_policy"
}

func (d *VolumeSnapshotPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud volume snapshot policy by ID or exact filter criteria. The policy must use a weekly schedule.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy ID (UUID)."},
			"name":        schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy name to filter by."},
			"region":      schema.StringAttribute{Optional: true, Computed: true, Description: "Project region name to filter by."},
			"status":      schema.StringAttribute{Optional: true, Computed: true, Description: "Policy status to filter by: enabled or disabled."},
			"description": schema.StringAttribute{Computed: true, Description: "Snapshot policy description."},
			"retention":   schema.Int64Attribute{Computed: true, Description: "Number of snapshots that the policy keeps for each volume."},
			"schedule": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Weekly schedule on which the policy takes snapshots.",
				Attributes: map[string]schema.Attribute{
					snapshotPolicyDaysOfWeek: schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Days of the week on which the policy takes a snapshot."},
					snapshotPolicyTime:       schema.StringAttribute{Computed: true, Description: "Time of day in UTC at which the policy takes a snapshot."},
				},
			},
			"state":          schema.StringAttribute{Computed: true, Description: "State of the latest policy run."},
			"volume_count":   schema.Int64Attribute{Computed: true, Description: "Number of volumes that use the policy."},
			"last_execution": schema.StringAttribute{Computed: true, Description: "Time of the latest policy run (RFC3339). Null before the first run."},
			"created_at":     schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339)."},
			"updated_at":     schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (d *VolumeSnapshotPolicyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.project, d.projectID = data.BlockStorage, data.Project, data.ProjectID
}

func (d *VolumeSnapshotPolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VolumeSnapshotPolicyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, diags := d.getPolicy(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || policy == nil {
		return
	}
	state := config
	resp.Diagnostics.Append(populateVolumeSnapshotPolicyDataSourceState(policy, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	preserveVolumeSnapshotPolicyFilters(policy, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VolumeSnapshotPolicyDataSource) getPolicy(ctx context.Context, config VolumeSnapshotPolicyDataSourceModel) (*blockstoragesdk.SnapshotPolicySchema, diag.Diagnostics) {
	criteria, diags := volumeSnapshotPolicyLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (volumeSnapshotPolicyCriteria{id: true}) {
		return d.getPolicyByID(ctx, snapshotPolicyTrimmed(config.ID))
	}
	return d.getPolicyByFilter(ctx, config, criteria)
}

func (d *VolumeSnapshotPolicyDataSource) getPolicyByID(ctx context.Context, idAttr types.String) (*blockstoragesdk.SnapshotPolicySchema, diag.Diagnostics) {
	id, diags := parse.UUIDString(idAttr, "id")
	if diags.HasError() {
		return nil, diags
	}
	policy, err := d.client.GetSnapshotPolicy(ctx, id, blockstoragesdk.GetSnapshotPolicyParams{ProjectID: d.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		diags.AddError("Volume snapshot policy not found", fmt.Sprintf("No snapshot policy found with id %s.", idAttr.ValueString()))
	} else if err != nil {
		diags.AddError("Error reading volume snapshot policy", err.Error())
	}
	return policy, diags
}

func (d *VolumeSnapshotPolicyDataSource) getPolicyByFilter(ctx context.Context, config VolumeSnapshotPolicyDataSourceModel, criteria volumeSnapshotPolicyCriteria) (*blockstoragesdk.SnapshotPolicySchema, diag.Diagnostics) {
	id, diags := parse.OptionalUUIDString(snapshotPolicyTrimmed(config.ID), "id")
	if diags.HasError() {
		return nil, diags
	}
	filter := blockstoragelookup.VolumeSnapshotPolicyFilter{ID: id}
	if criteria.name {
		name := strings.TrimSpace(config.Name.ValueString())
		filter.Name = &name
	}
	if criteria.status {
		status := blockstoragesdk.SnapshotPolicyStatus(strings.TrimSpace(config.Status.ValueString()))
		filter.Status = &status
	}
	if criteria.region {
		regionName := strings.TrimSpace(config.Region.ValueString())
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &regionName})
		if err != nil {
			diags.AddAttributeError(path.Root("region"), "Unable to resolve region", err.Error())
			return nil, diags
		}
		filter.RegionID, filter.RegionName = &region.Region.Id, &regionName
	}
	policy, err := blockstoragelookup.NewVolumeSnapshotPolicyFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve volume snapshot policy", err.Error())
		return nil, diags
	}
	return &policy, diags
}

func volumeSnapshotPolicyLookupCriteria(config VolumeSnapshotPolicyDataSourceModel) (volumeSnapshotPolicyCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	criteria := volumeSnapshotPolicyCriteria{
		snapshotPolicyKnown(config.ID), snapshotPolicyKnown(config.Name), snapshotPolicyKnown(config.Region), snapshotPolicyKnown(config.Status),
	}
	if criteria == (volumeSnapshotPolicyCriteria{}) {
		diags.AddError("Missing Volume Snapshot Policy lookup criteria", "Specify at least one of id, name, region, or status.")
		return criteria, diags
	}
	for _, entry := range []struct {
		field string
		value types.String
		set   bool
	}{
		{"id", config.ID, criteria.id}, {"name", config.Name, criteria.name}, {"region", config.Region, criteria.region}, {"status", config.Status, criteria.status},
	} {
		if entry.set && strings.TrimSpace(entry.value.ValueString()) == "" {
			diags.AddError("Invalid Volume Snapshot Policy lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", entry.field))
		}
	}
	if criteria.status && !blockstoragesdk.SnapshotPolicyStatus(strings.TrimSpace(config.Status.ValueString())).Valid() {
		diags.AddError("Invalid Volume Snapshot Policy lookup criteria", fmt.Sprintf("status %q is not one of enabled, disabled.", config.Status.ValueString()))
	}
	return criteria, diags
}
