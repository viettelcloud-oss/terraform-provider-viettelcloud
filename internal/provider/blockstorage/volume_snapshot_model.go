package blockstorage

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type VolumeSnapshotModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	VolumeName  types.String `tfsdk:"volume_name"`
	Size        types.Int64  `tfsdk:"size"`
	Status      types.String `tfsdk:"status"`
	FromPolicy  types.Bool   `tfsdk:"from_policy"`
	ProjectID   types.String `tfsdk:"project_id"`
	Zone        types.String `tfsdk:"zone"`
	ZoneID      types.String `tfsdk:"zone_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// VolumeSnapshotResourceModel references the source volume as an object,
// while the data source keeps filtering by a plain volume_id.
type VolumeSnapshotResourceModel struct {
	VolumeSnapshotModel
	Volume types.Object `tfsdk:"volume"`
}

type VolumeSnapshotDataSourceModel struct {
	VolumeSnapshotModel
	VolumeID types.String `tfsdk:"volume_id"`
}

func populateVolumeSnapshotModel(snapshot *blockstoragesdk.VolumeSnapshotSchema, state *VolumeSnapshotModel) {
	state.ID = types.StringValue(snapshot.Id.String())
	state.Name = types.StringValue(snapshot.Name)
	state.VolumeName = types.StringValue(snapshot.Volume.Name)
	state.FromPolicy = types.BoolValue(snapshot.FromPolicy)
	state.ProjectID = types.StringValue(snapshot.Project.Id.String())
	state.Zone = types.StringValue(snapshot.Zone.Name)
	state.ZoneID = types.StringValue(snapshot.Zone.Id.String())
	state.CreatedAt = types.StringValue(snapshot.CreatedAt.Format(time.RFC3339))
	state.UpdatedAt = types.StringValue(snapshot.UpdatedAt.Format(time.RFC3339))
	state.Description = types.StringNull()
	if snapshot.Description != nil {
		state.Description = types.StringValue(*snapshot.Description)
	}
	state.Size = types.Int64Null()
	if snapshot.Size != nil {
		state.Size = types.Int64Value(int64(*snapshot.Size))
	}
	state.Status = types.StringNull()
	if snapshot.Status != nil {
		state.Status = types.StringValue(string(*snapshot.Status))
	}
}

// populateVolumeSnapshotResourceState refreshes resource state from a snapshot
// response and keeps the configured spelling of inputs the backend normalizes.
func populateVolumeSnapshotResourceState(snapshot *blockstoragesdk.VolumeSnapshotSchema, state *VolumeSnapshotResourceModel) {
	prior := *state
	populateVolumeSnapshotModel(snapshot, &state.VolumeSnapshotModel)
	preserveVolumeSnapshotInputs(snapshot, &prior.VolumeSnapshotModel, &state.VolumeSnapshotModel)
	state.Volume = reference.Value(reference.ID, preservedUUID(reference.Get(prior.Volume, reference.ID), snapshot.Volume.Id))
}

func populateVolumeSnapshotDataSourceState(snapshot *blockstoragesdk.VolumeSnapshotSchema, state *VolumeSnapshotDataSourceModel) {
	populateVolumeSnapshotModel(snapshot, &state.VolumeSnapshotModel)
	state.VolumeID = types.StringValue(snapshot.Volume.Id.String())
}

func populateVolumeSnapshotPendingState(state *VolumeSnapshotResourceModel, id core.UUID) {
	state.ID = types.StringValue(id.String())
	if state.Description.IsUnknown() {
		state.Description = types.StringNull()
	}
	state.VolumeName = types.StringNull()
	state.Size = types.Int64Null()
	state.Status = types.StringNull()
	state.FromPolicy = types.BoolNull()
	state.ProjectID = types.StringNull()
	state.Zone = types.StringNull()
	state.ZoneID = types.StringNull()
	state.CreatedAt = types.StringNull()
	state.UpdatedAt = types.StringNull()
}

func preserveVolumeSnapshotInputs(snapshot *blockstoragesdk.VolumeSnapshotSchema, prior, state *VolumeSnapshotModel) {
	if !prior.Name.IsNull() && !prior.Name.IsUnknown() && strings.TrimSpace(prior.Name.ValueString()) == snapshot.Name {
		state.Name = prior.Name
	}
	if !prior.Description.IsNull() && !prior.Description.IsUnknown() && snapshot.Description != nil && prior.Description.ValueString() == *snapshot.Description {
		state.Description = prior.Description
	}
}

func preserveVolumeSnapshotFilters(snapshot *blockstoragesdk.VolumeSnapshotSchema, config, state *VolumeSnapshotDataSourceModel) {
	if compare.SameUUID(config.ID, snapshot.Id) {
		state.ID = config.ID
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() && snapshot.Status != nil && strings.TrimSpace(config.Status.ValueString()) == string(*snapshot.Status) {
		state.Status = config.Status
	}
	if !config.Zone.IsNull() && !config.Zone.IsUnknown() && strings.TrimSpace(config.Zone.ValueString()) == snapshot.Zone.Name {
		state.Zone = config.Zone
	}
	state.VolumeID = preservedUUID(config.VolumeID, snapshot.Volume.Id)
	preserveVolumeSnapshotInputs(snapshot, &config.VolumeSnapshotModel, &state.VolumeSnapshotModel)
}
