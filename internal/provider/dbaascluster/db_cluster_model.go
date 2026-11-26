package dbaascluster

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

const (
	engineCassandra   = "cassandra"
	engineMemcached   = "memcached"
	engineMongoDB     = "mongodb"
	engineOpenSearch  = "opensearch"
	engineStarRocks   = "starrocks"
	engineTimescaleDB = "timescaledb"

	// retentionMaxBackups and retentionDays name the two ways an engine keeps
	// its automatic backups: by count, or by age in days.
	retentionMaxBackups = "max_auto_backup"
	retentionDays       = "retention_days"

	topologyMongoRs       = "mongo_rs"
	topologyMongoSharding = "mongo_sharding"

	// criterionVersion names the engine version criterion of its reference.
	criterionVersion = "version"
)

var (
	nodePoolAttrTypes = map[string]attr.Type{
		"id":               types.StringType,
		"name":             types.StringType,
		"role":             types.StringType,
		"replicas":         types.Int64Type,
		"partition_count":  types.Int64Type,
		"node_count":       types.Int64Type,
		"resource_config":  reference.ObjectType(reference.Name),
		"storage_class":    reference.ObjectType(reference.Name),
		"disk_size":        types.Int64Type,
		"actual_disk_size": types.Int64Type,
		"used_disk_size":   types.Int64Type,
		"engine_roles":     types.SetType{ElemType: types.StringType},
		"pool_config":      types.ObjectType{AttrTypes: poolConfigAttrTypes},
	}
	poolConfigAttrTypes = map[string]attr.Type{"attribute": types.StringType}

	// nodePoolDataSourceAttrTypes flattens the references of a node pool to
	// their names, as data sources expose referenced objects.
	nodePoolDataSourceAttrTypes = map[string]attr.Type{
		"id":              types.StringType,
		"name":            types.StringType,
		"role":            types.StringType,
		"replicas":        types.Int64Type,
		"partition_count": types.Int64Type,
		"node_count":      types.Int64Type,
		"resource_config": types.StringType,
		"storage_class":   types.StringType,
		"disk_size":       types.Int64Type,
		"used_disk_size":  types.Int64Type,
		"engine_roles":    types.SetType{ElemType: types.StringType},
		"pool_config":     types.ObjectType{AttrTypes: poolConfigAttrTypes},
	}

	maintenanceAttrTypes = map[string]attr.Type{
		"day_of_week": types.StringType,
		"start_hour":  types.Int64Type,
	}
	autoBackupAttrTypes = map[string]attr.Type{
		"enable_auto_backup": types.BoolType,
		"backup_interval":    types.StringType,
		"day_of_week":        types.StringType,
		"start_hour":         types.Int64Type,
		"max_auto_backup":    types.Int64Type,
		"retention_days":     types.Int64Type,
	}
	pluginAttrTypes       = map[string]attr.Type{"name": types.StringType}
	engineConfigAttrTypes = map[string]attr.Type{
		"mode":                  types.StringType,
		"auth_enabled":          types.BoolType,
		"mcrouter_routing_type": types.StringType,
	}
)

// DBClusterModel holds the response fields that the resource and the data
// source share.
type DBClusterModel struct {
	ID                           types.String `tfsdk:"id"`
	Name                         types.String `tfsdk:"name"`
	Description                  types.String `tfsdk:"description"`
	Engine                       types.String `tfsdk:"engine"`
	Topology                     types.String `tfsdk:"topology"`
	KeepBackup                   types.Bool   `tfsdk:"keep_backup"`
	AllowedCIDRs                 types.Set    `tfsdk:"allowed_cidrs"`
	EnablePublicAccess           types.Bool   `tfsdk:"enable_public_access"`
	EnableSSL                    types.Bool   `tfsdk:"enable_ssl"`
	EnableAutoIncreaseVolumeSize types.Bool   `tfsdk:"enable_auto_increase_volume_size"`
	EnableAutoBackup             types.Bool   `tfsdk:"enable_auto_backup"`
	EnablePITRBackup             types.Bool   `tfsdk:"enable_pitr_backup"`
	MaintenanceConfig            types.Object `tfsdk:"maintenance_config"`
	EngineConfig                 types.Object `tfsdk:"engine_config"`
	Metadata                     types.Map    `tfsdk:"metadata"`
	AdminUsername                types.String `tfsdk:"admin_username"`
	Endpoint                     types.Map    `tfsdk:"endpoint"`
	Status                       types.String `tfsdk:"status"`
	OperationalStatus            types.String `tfsdk:"operational_status"`
	ErrorMessage                 types.String `tfsdk:"error_message"`
	CreatedAt                    types.String `tfsdk:"created_at"`
	UpdatedAt                    types.String `tfsdk:"updated_at"`
}

// DBClusterResourceModel references its region, zone, subnet, engine version
// and node pool catalog entries as objects. RootPassword is write-only, so the
// Framework never stores it in the plan or the state.
type DBClusterResourceModel struct {
	DBClusterModel
	EngineVersion    types.Object `tfsdk:"engine_version"`
	Region           types.Object `tfsdk:"region"`
	Zone             types.Object `tfsdk:"zone"`
	Subnet           types.Object `tfsdk:"subnet"`
	NodePools        types.List   `tfsdk:"node_pools"`
	Plugins          types.Set    `tfsdk:"plugins"`
	AutoBackupConfig types.Object `tfsdk:"auto_backup_config"`
	RootPassword     types.String `tfsdk:"root_password"`
}

// DBClusterDataSourceModel keeps the names of referenced objects as plain
// strings. Lookup arguments are Optional and Computed.
type DBClusterDataSourceModel struct {
	DBClusterModel
	EngineVersion    types.String `tfsdk:"engine_version"`
	Region           types.String `tfsdk:"region"`
	Zone             types.String `tfsdk:"zone"`
	SubnetID         types.String `tfsdk:"subnet_id"`
	SubnetName       types.String `tfsdk:"subnet_name"`
	VPCName          types.String `tfsdk:"vpc_name"`
	NodePools        types.List   `tfsdk:"node_pools"`
	Plugins          types.Set    `tfsdk:"plugins"`
	AutoBackupConfig types.Object `tfsdk:"auto_backup_config"`
}

// nodePoolModel decodes one element of the node_pools resource attribute.
type nodePoolModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Role           types.String `tfsdk:"role"`
	Replicas       types.Int64  `tfsdk:"replicas"`
	PartitionCount types.Int64  `tfsdk:"partition_count"`
	NodeCount      types.Int64  `tfsdk:"node_count"`
	ResourceConfig types.Object `tfsdk:"resource_config"`
	StorageClass   types.Object `tfsdk:"storage_class"`
	DiskSize       types.Int64  `tfsdk:"disk_size"`
	ActualDiskSize types.Int64  `tfsdk:"actual_disk_size"`
	UsedDiskSize   types.Int64  `tfsdk:"used_disk_size"`
	EngineRoles    types.Set    `tfsdk:"engine_roles"`
	PoolConfig     types.Object `tfsdk:"pool_config"`
}

// populateDBClusterModel maps the response fields shared by the resource and
// the data source.
func populateDBClusterModel(cluster *dbaasclustersdk.DBClusterDetailSchema, m *DBClusterModel) {
	if cluster == nil {
		return
	}

	m.ID = types.StringValue(cluster.Id.String())
	m.Name = types.StringValue(cluster.Name)
	m.Description = types.StringValue(cluster.Description)
	m.Engine = types.StringValue(string(cluster.Engine))
	m.Topology = types.StringValue(string(cluster.Topology))
	m.KeepBackup = types.BoolValue(cluster.KeepBackup)
	m.AllowedCIDRs = stringSetValue(cluster.AllowedCidrs)
	m.EnablePublicAccess = types.BoolValue(cluster.EnablePublicAccess)
	m.EnableSSL = types.BoolValue(cluster.EnableSsl)
	m.EnableAutoIncreaseVolumeSize = types.BoolValue(cluster.EnableAutoIncreaseVolumeSize)
	m.EnableAutoBackup = types.BoolValue(cluster.EnableAutoBackup)
	m.EnablePITRBackup = types.BoolValue(cluster.EnablePitrBackup)
	m.MaintenanceConfig = maintenanceValue(cluster.MaintenanceConfig)
	m.EngineConfig = engineConfigValue(cluster.EngineConfig)
	m.Metadata = stringMapValue(cluster.Metadata)
	m.AdminUsername = types.StringValue(cluster.AdminUsername)
	m.Endpoint = stringMapValue(cluster.Endpoint)
	m.Status = types.StringValue(string(cluster.Status))
	m.OperationalStatus = types.StringValue(string(cluster.OperationalStatus))
	m.ErrorMessage = types.StringValue(cluster.ErrorMessage)
	m.CreatedAt = types.StringValue(cluster.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(cluster.UpdatedAt.Format(time.RFC3339))
}

// populateDBClusterResourceState maps a response onto the resource state. It
// keeps the configured spelling of a reference or name that the backend only
// normalizes. It also keeps the configured disk_size of every pool, because the
// backend can grow a disk beyond the requested size.
func populateDBClusterResourceState(cluster *dbaasclustersdk.DBClusterDetailSchema, state *DBClusterResourceModel) {
	if cluster == nil {
		return
	}

	prior := state.DBClusterModel
	populateDBClusterModel(cluster, &state.DBClusterModel)
	// The request trims outer whitespace from these strings. Keep the
	// configured value, otherwise Terraform reports an inconsistent result.
	state.Name = compare.PreservedString(prior.Name, cluster.Name)
	state.Description = compare.PreservedString(prior.Description, cluster.Description)
	state.Engine = compare.PreservedString(prior.Engine, string(cluster.Engine))
	state.Topology = compare.PreservedString(prior.Topology, string(cluster.Topology))
	state.AllowedCIDRs = preservedStringSet(cluster.AllowedCidrs, prior.AllowedCIDRs)
	state.MaintenanceConfig = preservedObjectStrings(prior.MaintenanceConfig, state.MaintenanceConfig, maintenanceAttrTypes, "day_of_week")
	state.EngineConfig = preservedObjectStrings(prior.EngineConfig, state.EngineConfig, engineConfigAttrTypes, "mode", "mcrouter_routing_type")
	state.EngineVersion = referenceState(state.EngineVersion, criterionVersion, cluster.EngineVersion.Version)
	state.Region = referenceState(state.Region, reference.Name, cluster.Region.Name)
	state.Zone = zoneReferenceState(state.Zone, core.NullableToPtr(cluster.Zone))
	state.Subnet = subnetReferenceState(state.Subnet, cluster.Subnet.Id)
	state.NodePools = nodePoolsResourceValue(cluster.NodePools, state.NodePools)
	state.Plugins = pluginsResourceValue(cluster.Plugins, state.Plugins)
	state.AutoBackupConfig = preservedObjectStrings(state.AutoBackupConfig, autoBackupValue(cluster), autoBackupAttrTypes, "backup_interval", "day_of_week")
}

func populateDBClusterDataSourceState(cluster *dbaasclustersdk.DBClusterDetailSchema, state *DBClusterDataSourceModel) {
	if cluster == nil {
		return
	}

	populateDBClusterModel(cluster, &state.DBClusterModel)
	state.EngineVersion = types.StringValue(cluster.EngineVersion.Version)
	state.Region = types.StringValue(cluster.Region.Name)
	state.Zone = types.StringNull()
	if zone := core.NullableToPtr(cluster.Zone); zone != nil {
		state.Zone = types.StringValue(zone.Name)
	}
	state.SubnetID = types.StringValue(cluster.Subnet.Id.String())
	state.SubnetName = types.StringValue(cluster.Subnet.Name)
	state.VPCName = types.StringValue(cluster.Subnet.Vpc.Name)
	state.NodePools = nodePoolsDataSourceValue(cluster.NodePools)
	state.Plugins = pluginNamesValue(cluster.Plugins)
	state.AutoBackupConfig = autoBackupValue(cluster)
}

// preservedObjectStrings returns the backend object with each named string
// attribute set to its prior value when the two differ only by outer
// whitespace. The request trims these strings, so the trimmed backend value
// would otherwise show as drift. A null or unknown prior or backend object
// yields the backend object.
func preservedObjectStrings(prior, backend types.Object, attrTypes map[string]attr.Type, names ...string) types.Object {
	if !configured(prior) || !configured(backend) {
		return backend
	}
	attributes := backend.Attributes()
	for _, name := range names {
		backendValue, ok := attributes[name].(types.String)
		if !ok || !configured(backendValue) {
			continue
		}
		attributes[name] = compare.PreservedString(attributeString(prior, name), backendValue.ValueString())
	}
	return types.ObjectValueMust(attrTypes, attributes)
}

// preservedStringSet maps backend strings to a set. Each element keeps a
// prior element that differs from it only by outer whitespace, because the
// request trims each element.
func preservedStringSet(backend []string, prior types.Set) types.Set {
	var priorValues []types.String
	if configured(prior) {
		for _, element := range prior.Elements() {
			if value, ok := element.(types.String); ok {
				priorValues = append(priorValues, value)
			}
		}
	}
	elements := make([]attr.Value, 0, len(backend))
	for _, value := range backend {
		element := types.StringValue(value)
		for _, candidate := range priorValues {
			if compare.SameTrimmed(candidate, value) {
				element = candidate
				break
			}
		}
		elements = append(elements, element)
	}
	return types.SetValueMust(types.StringType, elements)
}

// referenceState returns the reference for state. It keeps the configured
// reference when its criterion differs from the backend value only by outer
// whitespace, otherwise Terraform reports an inconsistent result.
func referenceState(configured types.Object, criterion, backendValue string) types.Object {
	if compare.SameTrimmed(reference.Get(configured, criterion), backendValue) {
		return configured
	}
	return reference.Value(criterion, types.StringValue(backendValue))
}

// zoneReferenceState maps the optional zone. A cluster without a zone is
// multi-zone, and the backend reports no zone for it.
func zoneReferenceState(configured types.Object, zone *dbaasclustersdk.NestedZoneSchema) types.Object {
	if zone == nil {
		return types.ObjectNull(reference.AttributeTypes(reference.Name))
	}
	return referenceState(configured, reference.Name, zone.Name)
}

// subnetReferenceState keeps the configured subnet ID when it names the same
// UUID in a different spelling.
func subnetReferenceState(configured types.Object, subnetID core.UUID) types.Object {
	if compare.SameUUID(reference.Get(configured, reference.ID), subnetID) {
		return configured
	}
	return reference.Value(reference.ID, types.StringValue(subnetID.String()))
}

// nodePoolsResourceValue maps the response pools in the order of the prior
// value, because the backend orders pools by role and name, not by
// configuration. Pools the prior value does not name follow in response order.
func nodePoolsResourceValue(pools []dbaasclustersdk.NestedDBClusterNodePoolSchema, prior types.List) types.List {
	priorByName := map[string]types.Object{}
	var order []string
	if !prior.IsNull() && !prior.IsUnknown() {
		for _, element := range prior.Elements() {
			object, ok := element.(types.Object)
			if !ok {
				continue
			}
			name := attributeString(object, "name")
			if name.IsNull() || name.IsUnknown() {
				continue
			}
			// The backend trims outer whitespace from a pool name, so a
			// configured name matches the reported name after trimming.
			key := strings.TrimSpace(name.ValueString())
			priorByName[key] = object
			order = append(order, key)
		}
	}

	byName := make(map[string]dbaasclustersdk.NestedDBClusterNodePoolSchema, len(pools))
	for _, pool := range pools {
		byName[pool.Name] = pool
	}

	elements := make([]attr.Value, 0, len(pools))
	seen := make(map[string]bool, len(pools))
	for _, name := range order {
		pool, ok := byName[name]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		elements = append(elements, nodePoolResourceObject(pool, priorByName[name]))
	}
	for _, pool := range pools {
		if seen[pool.Name] {
			continue
		}
		seen[pool.Name] = true
		elements = append(elements, nodePoolResourceObject(pool, types.ObjectNull(nodePoolAttrTypes)))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: nodePoolAttrTypes}, elements)
}

func nodePoolResourceObject(pool dbaasclustersdk.NestedDBClusterNodePoolSchema, prior types.Object) types.Object {
	// disk_size records the requested size, and a pool without storage
	// requests none. An import has no request, so it starts from the size the
	// backend reports.
	diskSize := intValue(core.NullableToPtr(pool.DiskSize))
	if value, ok := prior.Attributes()["disk_size"].(types.Int64); ok && !prior.IsNull() && !value.IsUnknown() {
		diskSize = value
	}

	storageClass := types.ObjectNull(reference.AttributeTypes(reference.Name))
	if class := core.NullableToPtr(pool.StorageClass); class != nil {
		storageClass = referenceState(attributeObject(prior, "storage_class"), reference.Name, class.Name)
	}

	return types.ObjectValueMust(nodePoolAttrTypes, map[string]attr.Value{
		"id":               types.StringValue(pool.Id.String()),
		"name":             compare.PreservedString(attributeString(prior, "name"), pool.Name),
		"role":             types.StringValue(string(pool.Role)),
		"replicas":         types.Int64Value(int64(pool.Replicas)),
		"partition_count":  types.Int64Value(int64(pool.PartitionCount)),
		"node_count":       types.Int64Value(int64(pool.NodeCount)),
		"resource_config":  referenceState(attributeObject(prior, "resource_config"), reference.Name, pool.ResourceConfig.Name),
		"storage_class":    storageClass,
		"disk_size":        diskSize,
		"actual_disk_size": intValue(core.NullableToPtr(pool.DiskSize)),
		"used_disk_size":   types.Int64Value(int64(pool.UsedDiskSize)),
		"engine_roles":     preservedStringSet(pool.EngineRoles, attributeSet(prior, "engine_roles")),
		"pool_config":      preservedObjectStrings(attributeObject(prior, "pool_config"), poolConfigValue(pool.PoolConfig), poolConfigAttrTypes, "attribute"),
	})
}

func nodePoolsDataSourceValue(pools []dbaasclustersdk.NestedDBClusterNodePoolSchema) types.List {
	elements := make([]attr.Value, 0, len(pools))
	for _, pool := range pools {
		storageClass := types.StringNull()
		if class := core.NullableToPtr(pool.StorageClass); class != nil {
			storageClass = types.StringValue(class.Name)
		}
		elements = append(elements, types.ObjectValueMust(nodePoolDataSourceAttrTypes, map[string]attr.Value{
			"id":              types.StringValue(pool.Id.String()),
			"name":            types.StringValue(pool.Name),
			"role":            types.StringValue(string(pool.Role)),
			"replicas":        types.Int64Value(int64(pool.Replicas)),
			"partition_count": types.Int64Value(int64(pool.PartitionCount)),
			"node_count":      types.Int64Value(int64(pool.NodeCount)),
			"resource_config": types.StringValue(pool.ResourceConfig.Name),
			"storage_class":   storageClass,
			"disk_size":       intValue(core.NullableToPtr(pool.DiskSize)),
			"used_disk_size":  types.Int64Value(int64(pool.UsedDiskSize)),
			"engine_roles":    stringSetValue(pool.EngineRoles),
			"pool_config":     poolConfigValue(pool.PoolConfig),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: nodePoolDataSourceAttrTypes}, elements)
}

func poolConfigValue(config map[string]any) types.Object {
	attribute := types.StringNull()
	if value, ok := config["attribute"].(string); ok {
		attribute = types.StringValue(value)
	}
	return types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": attribute})
}

// pluginsResourceValue keeps plugins null when the cluster has none and the
// prior value is null, so an unconfigured Optional attribute stays null.
func pluginsResourceValue(plugins []dbaasclustersdk.NestedDBClusterPluginSchema, prior types.Set) types.Set {
	if len(plugins) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.SetNull(types.ObjectType{AttrTypes: pluginAttrTypes})
	}
	if samePluginNames(prior, plugins) {
		return prior
	}
	elements := make([]attr.Value, 0, len(plugins))
	for _, plugin := range plugins {
		elements = append(elements, types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{
			"name": types.StringValue(plugin.Name),
		}))
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, elements)
}

// samePluginNames reports whether a known prior set names the reported
// plugins. The backend trims outer whitespace from a plugin name, so names
// are compared after trimming.
func samePluginNames(prior types.Set, plugins []dbaasclustersdk.NestedDBClusterPluginSchema) bool {
	if prior.IsNull() || prior.IsUnknown() || len(prior.Elements()) != len(plugins) {
		return false
	}
	reported := make(map[string]bool, len(plugins))
	for _, plugin := range plugins {
		reported[plugin.Name] = true
	}
	for _, element := range prior.Elements() {
		object, ok := element.(types.Object)
		if !ok {
			return false
		}
		name := attributeString(object, "name")
		if name.IsNull() || name.IsUnknown() || !reported[strings.TrimSpace(name.ValueString())] {
			return false
		}
	}
	return true
}

func pluginNamesValue(plugins []dbaasclustersdk.NestedDBClusterPluginSchema) types.Set {
	names := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		names = append(names, plugin.Name)
	}
	return stringSetValue(names)
}

// autoBackupValue maps the schedule. The response carries the enabled flag on
// the cluster and the schedule beside it, so both feed one object. A cluster
// without a schedule maps to null.
func autoBackupValue(cluster *dbaasclustersdk.DBClusterDetailSchema) types.Object {
	config := core.NullableToPtr(cluster.AutoBackupConfig)
	if config == nil {
		return types.ObjectNull(autoBackupAttrTypes)
	}
	dayOfWeek := types.StringNull()
	if day := core.NullableToPtr(config.DayOfWeek); day != nil {
		dayOfWeek = types.StringValue(string(*day))
	}
	return types.ObjectValueMust(autoBackupAttrTypes, map[string]attr.Value{
		"enable_auto_backup": types.BoolValue(cluster.EnableAutoBackup),
		"backup_interval":    types.StringValue(string(config.BackupInterval)),
		"day_of_week":        dayOfWeek,
		"start_hour":         types.Int64Value(int64(config.StartHour)),
		"max_auto_backup":    intValue(core.NullableToPtr(config.MaxAutoBackup)),
		"retention_days":     intValue(core.NullableToPtr(config.RetentionDays)),
	})
}

// engineConfigValue reads the engine settings. Only memcached has settings;
// the other engines report an empty object, which maps to null.
func engineConfigValue(config map[string]any) types.Object {
	mode, hasMode := config["mode"].(string)
	auth, hasAuth := config["auth_enabled"].(bool)
	routing, hasRouting := config["mcrouter_routing_type"].(string)
	if !hasMode && !hasAuth && !hasRouting {
		return types.ObjectNull(engineConfigAttrTypes)
	}
	values := map[string]attr.Value{
		"mode":                  types.StringNull(),
		"auth_enabled":          types.BoolNull(),
		"mcrouter_routing_type": types.StringNull(),
	}
	if hasMode {
		values["mode"] = types.StringValue(mode)
	}
	if hasAuth {
		values["auth_enabled"] = types.BoolValue(auth)
	}
	if hasRouting {
		values["mcrouter_routing_type"] = types.StringValue(routing)
	}
	return types.ObjectValueMust(engineConfigAttrTypes, values)
}

// maintenanceValue reads the free-form maintenance window the backend returns.
// JSON decodes numbers as float64.
func maintenanceValue(config map[string]any) types.Object {
	dayOfWeek := types.StringNull()
	if value, ok := config["day_of_week"].(string); ok {
		dayOfWeek = types.StringValue(value)
	}
	startHour := types.Int64Null()
	if value, ok := config["start_hour"].(float64); ok {
		startHour = types.Int64Value(int64(value))
	}
	return types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
		"day_of_week": dayOfWeek,
		"start_hour":  startHour,
	})
}

// stringMapValue maps a free-form object to a map of strings. A string value
// is kept as is, and any other value is encoded as JSON so that no information
// is lost.
func stringMapValue(values map[string]any) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		if text, ok := value.(string); ok {
			elements[key] = types.StringValue(text)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			encoded = fmt.Append(nil, value)
		}
		elements[key] = types.StringValue(string(encoded))
	}
	return types.MapValueMust(types.StringType, elements)
}

func stringSetValue(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}

func intValue(value *int) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

// attributeString returns a string attribute of an object. An unknown object
// yields an unknown string, and a null object a null string.
func attributeString(object types.Object, name string) types.String {
	if object.IsUnknown() {
		return types.StringUnknown()
	}
	if object.IsNull() {
		return types.StringNull()
	}
	value, ok := object.Attributes()[name].(types.String)
	if !ok {
		return types.StringNull()
	}
	return value
}

// attributeSet returns a string set attribute of an object, or null.
func attributeSet(object types.Object, name string) types.Set {
	value, ok := object.Attributes()[name].(types.Set)
	if !ok {
		return types.SetNull(types.StringType)
	}
	return value
}

// attributeObject returns an object attribute of an object, or null.
func attributeObject(object types.Object, name string) types.Object {
	value, ok := object.Attributes()[name].(types.Object)
	if !ok {
		return types.ObjectNull(reference.AttributeTypes(reference.Name))
	}
	return value
}
