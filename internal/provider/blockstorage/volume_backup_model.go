package blockstorage

import (
	"strings"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"

	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
)

type VolumeBackupModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	VolumeName        types.String `tfsdk:"volume_name"`
	Status            types.String `tfsdk:"status"`
	Size              types.Int64  `tfsdk:"size"`
	Expired           types.Bool   `tfsdk:"expired"`
	DiskType          types.String `tfsdk:"disk_type"`
	EncryptionKeySize types.String `tfsdk:"encryption_key_size"`
	ProjectID         types.String `tfsdk:"project_id"`
	Region            types.String `tfsdk:"region"`
	RegionID          types.String `tfsdk:"region_id"`
	PolicyID          types.String `tfsdk:"policy_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

// VolumeBackupResourceModel references the source volume as an object, while
// the data source keeps filtering by a plain volume_id.
type VolumeBackupResourceModel struct {
	VolumeBackupModel
	Volume types.Object `tfsdk:"volume"`
}

type VolumeBackupDataSourceModel struct {
	VolumeBackupModel
	VolumeID types.String `tfsdk:"volume_id"`
}

func populateVolumeBackupModel(backup *blockstoragesdk.VolumeBackupSchema, state *VolumeBackupModel) {
	state.ID = types.StringValue(backup.Id.String())
	state.Name = types.StringValue(backup.Name)
	state.ProjectID = types.StringValue(backup.Project.Id.String())
	state.Region = types.StringValue(backup.Region.Name)
	state.RegionID = types.StringValue(backup.Region.Id.String())
	state.EncryptionKeySize = types.StringValue(string(backup.EncryptionKeySize))
	state.Expired = types.BoolValue(backup.Expired)
	state.CreatedAt = types.StringValue(backup.CreatedAt.Format(time.RFC3339))
	state.UpdatedAt = types.StringValue(backup.UpdatedAt.Format(time.RFC3339))
	state.Description = types.StringNull()
	if backup.Description != nil {
		state.Description = types.StringValue(*backup.Description)
	}
	state.VolumeName = types.StringNull()
	if backup.Volume != nil {
		state.VolumeName = types.StringValue(backup.Volume.Name)
	}
	state.Status = types.StringNull()
	if backup.Status != nil {
		state.Status = types.StringValue(string(*backup.Status))
	}
	state.Size = types.Int64Null()
	if backup.Size != nil {
		state.Size = types.Int64Value(int64(*backup.Size))
	}
	state.DiskType = types.StringNull()
	if backup.DiskType != nil {
		state.DiskType = types.StringValue(string(*backup.DiskType))
	}
	state.PolicyID = types.StringNull()
	if backup.Policy != nil {
		state.PolicyID = types.StringValue(backup.Policy.Id.String())
	}
}

func populateVolumeBackupResourceState(backup *blockstoragesdk.VolumeBackupSchema, state *VolumeBackupResourceModel) {
	name, description, volume := state.Name, state.Description, state.Volume
	populateVolumeBackupModel(backup, &state.VolumeBackupModel)
	if !name.IsNull() && !name.IsUnknown() && strings.TrimSpace(name.ValueString()) == backup.Name {
		state.Name = name
	}
	if backup.Description != nil && !description.IsNull() && !description.IsUnknown() && description.ValueString() == *backup.Description {
		state.Description = description
	}
	if backup.Volume != nil {
		state.Volume = reference.Value(reference.ID, preservedUUID(reference.Get(volume, reference.ID), backup.Volume.Id))
	}
}

func populateVolumeBackupDataSourceState(backup *blockstoragesdk.VolumeBackupSchema, state *VolumeBackupDataSourceModel) {
	populateVolumeBackupModel(backup, &state.VolumeBackupModel)
	state.VolumeID = types.StringNull()
	if backup.Volume != nil {
		state.VolumeID = types.StringValue(backup.Volume.Id.String())
	}
}

func preserveVolumeBackupConfiguredValues(backup *blockstoragesdk.VolumeBackupSchema, config VolumeBackupDataSourceModel, state *VolumeBackupDataSourceModel) {
	if compare.SameUUID(config.ID, backup.Id) {
		state.ID = config.ID
	}
	if backup.Volume != nil && compare.SameUUID(config.VolumeID, backup.Volume.Id) {
		state.VolumeID = config.VolumeID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == backup.Name {
		state.Name = config.Name
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == backup.Region.Name {
		state.Region = config.Region
	}
	if backup.Status != nil && !config.Status.IsNull() && !config.Status.IsUnknown() && strings.TrimSpace(config.Status.ValueString()) == string(*backup.Status) {
		state.Status = config.Status
	}
}
