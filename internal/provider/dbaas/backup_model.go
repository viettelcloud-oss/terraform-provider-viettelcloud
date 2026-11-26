package dbaas

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// BackupModel holds the response fields that the resource and the
// data source share.
type BackupModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	Description           types.String `tfsdk:"description"`
	DBInstanceName        types.String `tfsdk:"db_instance_name"`
	DatabaseType          types.String `tfsdk:"database_type"`
	DBVersion             types.String `tfsdk:"db_version"`
	DBVersionID           types.String `tfsdk:"db_version_id"`
	Region                types.String `tfsdk:"region"`
	RegionID              types.String `tfsdk:"region_id"`
	ZoneID                types.String `tfsdk:"zone_id"`
	BackupType            types.String `tfsdk:"backup_type"`
	BackupInfo            types.String `tfsdk:"backup_info"`
	Size                  types.Int64  `tfsdk:"size"`
	RequiredVolumeSize    types.Int64  `tfsdk:"required_volume_size"`
	BackupTimestamp       types.String `tfsdk:"backup_timestamp"`
	StartRestoreTimestamp types.String `tfsdk:"start_restore_timestamp"`
	EndRestoreTimestamp   types.String `tfsdk:"end_restore_timestamp"`
	BackupPlanID          types.String `tfsdk:"backup_plan_id"`
	OwnerID               types.String `tfsdk:"owner_id"`
	ProjectID             types.String `tfsdk:"project_id"`
	Status                types.String `tfsdk:"status"`
	ErrorMessage          types.String `tfsdk:"error_message"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
}

// BackupResourceModel references the source DB instance as an
// object, while the data source filters by a plain db_instance_id.
type BackupResourceModel struct {
	BackupModel
	DBInstance types.Object `tfsdk:"db_instance"`
}

type BackupDataSourceModel struct {
	BackupModel
	DBInstanceID types.String `tfsdk:"db_instance_id"`
}

func populateBackupModel(backup *dbaassdk.DBInstanceBackupSchema, m *BackupModel) {
	m.ID = types.StringValue(backup.Id.String())
	m.Name = types.StringValue(backup.Name)
	m.Description = types.StringValue(backup.Description)
	m.DBInstanceName = types.StringNull()
	if instance := core.NullableToPtr(backup.DbInstance); instance != nil {
		m.DBInstanceName = types.StringValue(instance.Name)
	}
	m.DatabaseType = types.StringValue(string(backup.DatabaseType))
	m.DBVersion = types.StringValue(backup.DbVersion.Version)
	m.DBVersionID = types.StringValue(backup.DbVersion.Id.String())
	m.Region = types.StringValue(backup.Region.Name)
	m.RegionID = types.StringValue(backup.Region.Id.String())
	m.ZoneID = uuidValue(backup.ZoneId)
	m.BackupType = types.StringValue(string(backup.BackupType))
	m.BackupInfo = types.StringValue(backup.BackupInfo)
	m.Size = types.Int64Value(int64(backup.Size))
	m.RequiredVolumeSize = types.Int64Value(int64(backup.RequiredVolumeSize))
	m.BackupTimestamp = timeValue(backup.BackupTimestamp)
	m.StartRestoreTimestamp = timeValue(backup.StartRestoreTimestamp)
	m.EndRestoreTimestamp = timeValue(backup.EndRestoreTimestamp)
	m.BackupPlanID = uuidValue(backup.BackupPlanId)
	m.OwnerID = types.StringValue(backup.OwnerId.String())
	m.ProjectID = types.StringValue(backup.ProjectId.String())
	m.Status = types.StringValue(string(backup.Status))
	m.ErrorMessage = types.StringValue(backup.ErrorMessage)
	m.CreatedAt = types.StringValue(backup.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(backup.UpdatedAt.Format(time.RFC3339))
}

// populateBackupResourceState keeps the configured spelling of name and
// description when the backend reports the same value.
//
// It also keeps db_instance from state when the response no longer names the
// instance. A backup that outlives its instance reports db_instance as null,
// and replacing it would fail, because its instance is gone.
func populateBackupResourceState(backup *dbaassdk.DBInstanceBackupSchema, state *BackupResourceModel) {
	prior := state.BackupModel
	populateBackupModel(backup, &state.BackupModel)
	state.Name = compare.PreservedString(prior.Name, backup.Name)
	state.Description = compare.PreservedString(prior.Description, backup.Description)
	instance := core.NullableToPtr(backup.DbInstance)
	if instance == nil {
		return
	}
	id := reference.Get(state.DBInstance, reference.ID)
	if !compare.SameUUID(id, instance.Id) {
		id = types.StringValue(instance.Id.String())
	}
	state.DBInstance = reference.Value(reference.ID, id)
}

func populateBackupDataSourceState(backup *dbaassdk.DBInstanceBackupSchema, state *BackupDataSourceModel) {
	populateBackupModel(backup, &state.BackupModel)
	state.DBInstanceID = types.StringNull()
	if instance := core.NullableToPtr(backup.DbInstance); instance != nil {
		state.DBInstanceID = types.StringValue(instance.Id.String())
	}
}

// preserveBackupConfiguredValues keeps the configured spelling of
// the lookup arguments when the backend value is the same.
func preserveBackupConfiguredValues(backup *dbaassdk.DBInstanceBackupSchema, config BackupDataSourceModel, state *BackupDataSourceModel) {
	if compare.SameUUID(config.ID, backup.Id) {
		state.ID = config.ID
	}
	if instance := core.NullableToPtr(backup.DbInstance); instance != nil && compare.SameUUID(config.DBInstanceID, instance.Id) {
		state.DBInstanceID = config.DBInstanceID
	}
	for _, item := range []struct {
		configured types.String
		backend    string
		target     *types.String
	}{
		{config.Name, backup.Name, &state.Name},
		{config.BackupType, string(backup.BackupType), &state.BackupType},
		{config.Status, string(backup.Status), &state.Status},
		{config.DatabaseType, string(backup.DatabaseType), &state.DatabaseType},
		{config.Region, backup.Region.Name, &state.Region},
	} {
		if known(item.configured) && strings.TrimSpace(item.configured.ValueString()) == item.backend {
			*item.target = item.configured
		}
	}
}

func uuidValue(value core.Nullable[core.UUID]) types.String {
	id := core.NullableToPtr(value)
	if id == nil {
		return types.StringNull()
	}
	return types.StringValue(id.String())
}

func timeValue(value core.Nullable[time.Time]) types.String {
	t := core.NullableToPtr(value)
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.Format(time.RFC3339))
}
