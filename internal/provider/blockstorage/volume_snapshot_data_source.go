package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VolumeSnapshotDataSource{}

type VolumeSnapshotDataSource struct {
	client    *blockstoragesdk.Client
	projectID core.UUID
}

type volumeSnapshotCriteria struct{ id, name, volumeID, zone, status bool }

func NewVolumeSnapshotDataSource() datasource.DataSource { return &VolumeSnapshotDataSource{} }

func (d *VolumeSnapshotDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_snapshot"
}

func (d *VolumeSnapshotDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud Block Storage volume snapshot by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot ID (UUID)."},
			"name":        schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot name to filter by."},
			"volume_id":   schema.StringAttribute{Optional: true, Computed: true, Description: "Source volume ID (UUID) to filter by."},
			"zone":        schema.StringAttribute{Optional: true, Computed: true, Description: "Source volume zone name to filter by."},
			"status":      schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot status to filter by."},
			"description": schema.StringAttribute{Computed: true, Description: "Snapshot description."},
			"volume_name": schema.StringAttribute{Computed: true, Description: "Source volume name."},
			"size":        schema.Int64Attribute{Computed: true, Description: "Snapshot size in GiB."},
			"from_policy": schema.BoolAttribute{Computed: true, Description: "Whether a policy created the snapshot."},
			"project_id":  schema.StringAttribute{Computed: true, Description: "Project ID (UUID)."},
			"zone_id":     schema.StringAttribute{Computed: true, Description: "Source volume zone ID (UUID)."},
			"created_at":  schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339)."},
			"updated_at":  schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (d *VolumeSnapshotDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.BlockStorage, data.ProjectID
}

func (d *VolumeSnapshotDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VolumeSnapshotDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	snapshot, diags := d.getSnapshot(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || snapshot == nil {
		return
	}
	state := config
	populateVolumeSnapshotDataSourceState(snapshot, &state)
	preserveVolumeSnapshotFilters(snapshot, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VolumeSnapshotDataSource) getSnapshot(ctx context.Context, config VolumeSnapshotDataSourceModel) (*blockstoragesdk.VolumeSnapshotSchema, diag.Diagnostics) {
	criteria, diags := volumeSnapshotLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (volumeSnapshotCriteria{id: true}) {
		return d.getSnapshotByID(ctx, config.ID)
	}
	return d.getSnapshotByFilter(ctx, config, criteria)
}

func (d *VolumeSnapshotDataSource) getSnapshotByID(ctx context.Context, idAttr types.String) (*blockstoragesdk.VolumeSnapshotSchema, diag.Diagnostics) {
	id, diags := parse.UUIDString(idAttr, "id")
	if diags.HasError() {
		return nil, diags
	}
	snapshot, err := d.client.GetVolumeSnapshot(ctx, id, blockstoragesdk.GetVolumeSnapshotParams{ProjectID: d.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		diags.AddError("Volume snapshot not found", fmt.Sprintf("No snapshot found with id %s.", idAttr.ValueString()))
	} else if err != nil {
		diags.AddError("Error reading volume snapshot", err.Error())
	}
	return snapshot, diags
}

func (d *VolumeSnapshotDataSource) getSnapshotByFilter(ctx context.Context, config VolumeSnapshotDataSourceModel, criteria volumeSnapshotCriteria) (*blockstoragesdk.VolumeSnapshotSchema, diag.Diagnostics) {
	id, diags := parse.OptionalUUIDString(config.ID, "id")
	volumeID, volumeDiags := parse.OptionalUUIDString(config.VolumeID, "volume_id")
	diags.Append(volumeDiags...)
	if diags.HasError() {
		return nil, diags
	}
	filter := blockstoragelookup.VolumeSnapshotFilter{ID: id, VolumeID: volumeID}
	if criteria.name {
		value := config.Name.ValueString()
		filter.Name = &value
	}
	if criteria.zone {
		value := config.Zone.ValueString()
		filter.Zone = &value
	}
	if criteria.status {
		value := blockstoragesdk.SnapshotStatus(strings.TrimSpace(config.Status.ValueString()))
		filter.Status = &value
	}
	snapshot, err := blockstoragelookup.NewVolumeSnapshotFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve volume snapshot", err.Error())
		return nil, diags
	}
	return &snapshot, diags
}

func volumeSnapshotLookupCriteria(config VolumeSnapshotDataSourceModel) (volumeSnapshotCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	known := func(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }
	criteria := volumeSnapshotCriteria{known(config.ID), known(config.Name), known(config.VolumeID), known(config.Zone), known(config.Status)}
	if criteria == (volumeSnapshotCriteria{}) {
		diags.AddError("Missing volume snapshot lookup criteria", "Specify at least one of id, name, volume_id, zone, or status.")
		return criteria, diags
	}
	for _, entry := range []struct {
		field string
		value types.String
		set   bool
	}{
		{"id", config.ID, criteria.id}, {"name", config.Name, criteria.name}, {"volume_id", config.VolumeID, criteria.volumeID}, {"zone", config.Zone, criteria.zone}, {"status", config.Status, criteria.status},
	} {
		if entry.set && strings.TrimSpace(entry.value.ValueString()) == "" {
			diags.AddError("Invalid volume snapshot lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", entry.field))
		}
	}
	return criteria, diags
}
