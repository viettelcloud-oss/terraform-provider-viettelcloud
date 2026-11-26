package dbaas

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	dbaasselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// InstanceModel holds the response fields that the resource and the data
// source share.
type InstanceModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	DatabaseType           types.String `tfsdk:"database_type"`
	RegionID               types.String `tfsdk:"region_id"`
	FlavorVCPUs            types.Int64  `tfsdk:"flavor_vcpus"`
	FlavorRAM              types.Int64  `tfsdk:"flavor_ram"`
	VolumeTypeDiskType     types.String `tfsdk:"volume_type_disk_type"`
	CurrentDiskSize        types.Int64  `tfsdk:"current_disk_size"`
	UsedDiskSize           types.Int64  `tfsdk:"used_disk_size"`
	DiskFull               types.Bool   `tfsdk:"disk_full"`
	Solution               types.String `tfsdk:"solution"`
	SubnetName             types.String `tfsdk:"subnet_name"`
	SubnetCIDR             types.String `tfsdk:"subnet_cidr"`
	VPCID                  types.String `tfsdk:"vpc_id"`
	VPCName                types.String `tfsdk:"vpc_name"`
	EnablePublicAccess     types.Bool   `tfsdk:"enable_public_access"`
	MaintenanceConfig      types.Object `tfsdk:"maintenance_config"`
	KeepBackup             types.Bool   `tfsdk:"keep_backup"`
	BackupPolicy           types.Object `tfsdk:"backup_policy"`
	AutoIncreaseVolumeSize types.Object `tfsdk:"auto_increase_volume_size"`
	AllowedCIDRs           types.Set    `tfsdk:"allowed_cidrs"`
	EnableSSL              types.Bool   `tfsdk:"enable_ssl"`
	PrivateURL             types.String `tfsdk:"private_url"`
	PublicURL              types.String `tfsdk:"public_url"`
	Port                   types.Int64  `tfsdk:"port"`
	InitBackupID           types.String `tfsdk:"init_backup_id"`
	ProjectID              types.String `tfsdk:"project_id"`
	Status                 types.String `tfsdk:"status"`
	OperationalStatus      types.String `tfsdk:"operational_status"`
	ErrorMessage           types.String `tfsdk:"error_message"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

// InstanceResourceModel selects catalog entries and references the region
// and subnet as objects. disk_size is the size requested at creation;
// current_disk_size reports the size the backend has provisioned since.
type InstanceResourceModel struct {
	InstanceModel
	DBVersion    types.Object `tfsdk:"db_version"`
	Region       types.Object `tfsdk:"region"`
	Flavor       types.Object `tfsdk:"flavor"`
	VolumeType   types.Object `tfsdk:"volume_type"`
	Subnet       types.Object `tfsdk:"subnet"`
	DiskSize     types.Int64  `tfsdk:"disk_size"`
	RootPassword types.String `tfsdk:"root_password"`
}

// InstanceDataSourceModel reports catalog entries, the region, and the
// subnet as flat attributes.
type InstanceDataSourceModel struct {
	InstanceModel
	DBVersion    types.String `tfsdk:"db_version"`
	DBVersionID  types.String `tfsdk:"db_version_id"`
	Flavor       types.String `tfsdk:"flavor"`
	FlavorID     types.String `tfsdk:"flavor_id"`
	VolumeType   types.String `tfsdk:"volume_type"`
	VolumeTypeID types.String `tfsdk:"volume_type_id"`
	Region       types.String `tfsdk:"region"`
	SubnetID     types.String `tfsdk:"subnet_id"`
}

// backupPolicyModel decodes backup_policy. enabled maps to the instance flag
// enable_auto_backup and the rest to auto_backup_config.
type backupPolicyModel struct {
	Enabled        types.Bool   `tfsdk:"enabled"`
	BackupInterval types.String `tfsdk:"backup_interval"`
	DayOfWeek      types.String `tfsdk:"day_of_week"`
	StartHour      types.Int64  `tfsdk:"start_hour"`
	MaxAutoBackup  types.Int64  `tfsdk:"max_auto_backup"`
}

// autoIncreaseModel decodes auto_increase_volume_size. enabled maps to the
// instance flag enable_auto_increase_volume_size and the rest to
// auto_increase_volume_size_config.
type autoIncreaseModel struct {
	Enabled   types.Bool  `tfsdk:"enabled"`
	Threshold types.Int64 `tfsdk:"threshold"`
	Limit     types.Int64 `tfsdk:"limit"`
}

func maintenanceConfigAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{"day_of_week": types.StringType, "start_hour": types.Int64Type}
}

func backupPolicyAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"enabled": types.BoolType, "backup_interval": types.StringType, "day_of_week": types.StringType,
		"start_hour": types.Int64Type, "max_auto_backup": types.Int64Type,
	}
}

func autoIncreaseAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{"enabled": types.BoolType, "threshold": types.Int64Type, "limit": types.Int64Type}
}

func populateInstanceModel(instance *dbaassdk.DBInstanceSchema, m *InstanceModel) {
	m.ID = types.StringValue(instance.Id.String())
	m.Name = types.StringValue(instance.Name)
	m.Description = types.StringValue(instance.Description)
	m.DatabaseType = types.StringValue(string(instance.DatabaseType))
	m.RegionID = types.StringValue(instance.Region.Id.String())
	m.FlavorVCPUs = types.Int64Value(int64(instance.Flavor.Vcpus))
	m.FlavorRAM = types.Int64Value(int64(instance.Flavor.Ram))
	m.VolumeTypeDiskType = types.StringValue(string(instance.VolumeType.DiskType))
	m.CurrentDiskSize = types.Int64Value(int64(instance.DiskSize))
	m.UsedDiskSize = types.Int64Value(int64(instance.UsedDiskSize))
	m.DiskFull = types.BoolValue(instance.DiskFull)
	m.Solution = types.StringValue(string(instance.Solution))
	m.SubnetName = types.StringValue(instance.Subnet.Name)
	m.SubnetCIDR = types.StringValue(instance.Subnet.Cidr)
	m.VPCID = types.StringValue(instance.Subnet.Vpc.Id.String())
	m.VPCName = types.StringValue(instance.Subnet.Vpc.Name)
	m.EnablePublicAccess = types.BoolValue(instance.EnablePublicAccess)
	m.MaintenanceConfig = types.ObjectValueMust(maintenanceConfigAttributeTypes(), map[string]attr.Value{
		"day_of_week": types.StringValue(string(instance.MDow)), "start_hour": types.Int64Value(int64(instance.MStartHour)),
	})
	m.KeepBackup = types.BoolValue(instance.KeepBackup)
	m.BackupPolicy = backupPolicyValue(instance)
	m.AutoIncreaseVolumeSize = autoIncreaseValue(instance)
	cidrs := make([]attr.Value, 0, len(instance.AllowedCidrs))
	for _, cidr := range instance.AllowedCidrs {
		cidrs = append(cidrs, types.StringValue(cidr))
	}
	m.AllowedCIDRs = types.SetValueMust(types.StringType, cidrs)
	m.EnableSSL = types.BoolValue(instance.EnableSsl)
	m.PrivateURL = types.StringValue(instance.PrivateUrl)
	m.PublicURL = types.StringValue(instance.PublicUrl)
	m.Port = types.Int64Value(int64(instance.Port))
	m.InitBackupID = types.StringNull()
	if id := core.NullableToPtr(instance.InitBackupId); id != nil {
		m.InitBackupID = types.StringValue(id.String())
	}
	m.ProjectID = types.StringValue(instance.ProjectId.String())
	m.Status = types.StringValue(string(instance.Status))
	m.OperationalStatus = types.StringValue(string(instance.OperationalStatus))
	m.ErrorMessage = types.StringValue(instance.ErrorMessage)
	m.CreatedAt = types.StringValue(instance.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(instance.UpdatedAt.Format(time.RFC3339))
}

// backupPolicyValue maps the instance flag and auto_backup_config. The backend
// keeps the config row when it disables automatic backups, so a disabled
// policy can still report the earlier schedule.
func backupPolicyValue(instance *dbaassdk.DBInstanceSchema) types.Object {
	values := map[string]attr.Value{
		"enabled": types.BoolValue(instance.EnableAutoBackup), "backup_interval": types.StringNull(),
		"day_of_week": types.StringNull(), "start_hour": types.Int64Null(), "max_auto_backup": types.Int64Null(),
	}
	if config := core.NullableToPtr(instance.AutoBackupConfig); config != nil {
		values["backup_interval"] = types.StringValue(string(config.BackupInterval))
		values["start_hour"] = types.Int64Value(int64(config.StartHour))
		values["max_auto_backup"] = types.Int64Value(int64(config.MaxAutoBackup))
		if day := core.NullableToPtr(config.DayOfWeek); day != nil {
			values["day_of_week"] = types.StringValue(string(*day))
		}
	}
	return types.ObjectValueMust(backupPolicyAttributeTypes(), values)
}

func autoIncreaseValue(instance *dbaassdk.DBInstanceSchema) types.Object {
	values := map[string]attr.Value{
		"enabled": types.BoolValue(instance.EnableAutoIncreaseVolumeSize), "threshold": types.Int64Null(), "limit": types.Int64Null(),
	}
	if config := core.NullableToPtr(instance.AutoIncreaseVolumeSizeConfig); config != nil {
		values["threshold"] = types.Int64Value(int64(config.Threshold))
		values["limit"] = types.Int64Value(int64(config.AutoIncreaseLimit))
	}
	return types.ObjectValueMust(autoIncreaseAttributeTypes(), values)
}

// populateInstanceResourceState maps a response into resource state. It
// keeps the configured spelling of strings and references that the backend
// reports with the same value, and the disk_size that creation requested.
func populateInstanceResourceState(instance *dbaassdk.DBInstanceSchema, state *InstanceResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	prior := state.InstanceModel
	populateInstanceModel(instance, &state.InstanceModel)
	state.Name = compare.PreservedString(prior.Name, instance.Name)
	state.Description = compare.PreservedString(prior.Description, instance.Description)
	state.DatabaseType = compare.PreservedString(prior.DatabaseType, string(instance.DatabaseType))
	state.Solution = compare.PreservedString(prior.Solution, string(instance.Solution))
	state.BackupPolicy = preservedBackupPolicy(prior.BackupPolicy, state.BackupPolicy)
	state.Region = regionReferenceState(state.Region, instance.Region.Name)
	subnetID := reference.Get(state.Subnet, reference.ID)
	if !compare.SameUUID(subnetID, instance.Subnet.Id) {
		subnetID = types.StringValue(instance.Subnet.Id.String())
	}
	state.Subnet = reference.Value(reference.ID, subnetID)

	version, versionDiags := selector.RefreshCriterion(state.DBVersion, dbaasselector.DBVersionAttributeTypes(), &instance.DbVersion.Id, dbaasselector.DBVersionCriterion, instance.DbVersion.Version)
	diags.Append(versionDiags...)
	flavor, flavorDiags := selector.Refresh(state.Flavor, dbaasselector.FlavorAttributeTypes(), &instance.Flavor.Id, instance.Flavor.FlavorName)
	diags.Append(flavorDiags...)
	volumeType, volumeTypeDiags := selector.Refresh(state.VolumeType, dbaasselector.VolumeTypeAttributeTypes(), &instance.VolumeType.Id, instance.VolumeType.Name)
	diags.Append(volumeTypeDiags...)
	state.DBVersion, state.Flavor, state.VolumeType = version, flavor, volumeType

	// The backend reports the provisioned size, which auto-increase can grow
	// past the requested one. Only import, which has no requested size, takes it.
	if state.DiskSize.IsNull() || state.DiskSize.IsUnknown() {
		state.DiskSize = types.Int64Value(int64(instance.DiskSize))
	}
	state.RootPassword = types.StringNull()
	return diags
}

func populateInstanceDataSourceState(instance *dbaassdk.DBInstanceSchema, state *InstanceDataSourceModel) {
	populateInstanceModel(instance, &state.InstanceModel)
	state.DBVersion = types.StringValue(instance.DbVersion.Version)
	state.DBVersionID = types.StringValue(instance.DbVersion.Id.String())
	state.Flavor = types.StringValue(instance.Flavor.FlavorName)
	state.FlavorID = types.StringValue(instance.Flavor.Id.String())
	state.VolumeType = types.StringValue(instance.VolumeType.Name)
	state.VolumeTypeID = types.StringValue(instance.VolumeType.Id.String())
	state.Region = types.StringValue(instance.Region.Name)
	state.SubnetID = types.StringValue(instance.Subnet.Id.String())
}

// preserveInstanceConfiguredValues keeps the configured spelling of the
// lookup arguments when the backend value is the same.
func preserveInstanceConfiguredValues(instance *dbaassdk.DBInstanceSchema, config InstanceDataSourceModel, state *InstanceDataSourceModel) {
	if compare.SameUUID(config.ID, instance.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == instance.Name {
		state.Name = config.Name
	}
}

// regionReferenceState keeps the configured region reference when its name
// differs from the backend name only by outer whitespace.
func regionReferenceState(configured types.Object, backendName string) types.Object {
	if compare.SameTrimmed(reference.Get(configured, reference.Name), backendName) {
		return configured
	}
	return reference.Value(reference.Name, types.StringValue(backendName))
}

// preservedBackupPolicy keeps the configured spelling of backup_interval and
// day_of_week when the backend reports the same value.
func preservedBackupPolicy(configured, backend types.Object) types.Object {
	if configured.IsNull() || configured.IsUnknown() {
		return backend
	}
	values := backend.Attributes()
	for _, name := range []string{"backup_interval", "day_of_week"} {
		prior, _ := configured.Attributes()[name].(types.String)
		if current, ok := values[name].(types.String); ok && !current.IsNull() && compare.SameTrimmed(prior, current.ValueString()) {
			values[name] = prior
		}
	}
	return types.ObjectValueMust(backupPolicyAttributeTypes(), values)
}

// decodeBackupPolicy decodes a known backup_policy object.
func decodeBackupPolicy(ctx context.Context, value types.Object) (backupPolicyModel, diag.Diagnostics) {
	var m backupPolicyModel
	diags := value.As(ctx, &m, basetypes.ObjectAsOptions{})
	return m, diags
}

// decodeAutoIncrease decodes a known auto_increase_volume_size object.
func decodeAutoIncrease(ctx context.Context, value types.Object) (autoIncreaseModel, diag.Diagnostics) {
	var m autoIncreaseModel
	diags := value.As(ctx, &m, basetypes.ObjectAsOptions{})
	return m, diags
}
