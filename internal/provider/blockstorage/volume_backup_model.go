package blockstorage

import (
	"strings"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
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
	Volume        types.Object   `tfsdk:"volume"`
	RestoreVolume types.Object   `tfsdk:"restore_volume"`
	Timeouts      timeouts.Value `tfsdk:"timeouts"`
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
	if volume := core.NullableToPtr(backup.Volume); volume != nil {
		state.VolumeName = types.StringValue(volume.Name)
	}
	state.Status = types.StringNull()
	if status := core.NullableToPtr(backup.Status); status != nil {
		state.Status = types.StringValue(string(*status))
	}
	state.Size = types.Int64Null()
	if size := core.NullableToPtr(backup.Size); size != nil {
		state.Size = types.Int64Value(int64(*size))
	}
	state.DiskType = types.StringNull()
	if diskType := core.NullableToPtr(backup.DiskType); diskType != nil {
		state.DiskType = types.StringValue(string(*diskType))
	}
	state.PolicyID = types.StringNull()
	if policy := core.NullableToPtr(backup.Policy); policy != nil {
		state.PolicyID = types.StringValue(policy.Id.String())
	}
}

func populateVolumeBackupResourceState(backup *blockstoragesdk.VolumeBackupSchema, state *VolumeBackupResourceModel) {
	name, description, volume := state.Name, state.Description, state.Volume
	populateVolumeBackupModel(backup, &state.VolumeBackupModel)
	if !name.IsNull() && !name.IsUnknown() && strings.TrimSpace(name.ValueString()) == backup.Name {
		state.Name = name
	}
	if backup.Description != nil && compare.SameTrimmed(description, *backup.Description) {
		state.Description = description
	}
	if backupVolume := core.NullableToPtr(backup.Volume); backupVolume != nil {
		state.Volume = reference.Value(reference.ID, compare.PreservedUUID(reference.Get(volume, reference.ID), backupVolume.Id))
	}
}

func populateVolumeBackupDataSourceState(backup *blockstoragesdk.VolumeBackupSchema, state *VolumeBackupDataSourceModel) {
	populateVolumeBackupModel(backup, &state.VolumeBackupModel)
	state.VolumeID = types.StringNull()
	if volume := core.NullableToPtr(backup.Volume); volume != nil {
		state.VolumeID = types.StringValue(volume.Id.String())
	}
}

func preserveVolumeBackupConfiguredValues(backup *blockstoragesdk.VolumeBackupSchema, config VolumeBackupDataSourceModel, state *VolumeBackupDataSourceModel) {
	if compare.SameUUID(config.ID, backup.Id) {
		state.ID = config.ID
	}
	if volume := core.NullableToPtr(backup.Volume); volume != nil && compare.SameUUID(config.VolumeID, volume.Id) {
		state.VolumeID = config.VolumeID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == backup.Name {
		state.Name = config.Name
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == backup.Region.Name {
		state.Region = config.Region
	}
	if status := core.NullableToPtr(backup.Status); status != nil && !config.Status.IsNull() && !config.Status.IsUnknown() && strings.TrimSpace(config.Status.ValueString()) == string(*status) {
		state.Status = config.Status
	}
}
