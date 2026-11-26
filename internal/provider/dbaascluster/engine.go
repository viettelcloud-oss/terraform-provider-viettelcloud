package dbaascluster

// engine.go owns the seam between the engines that share the db_cluster
// resource. A driver holds the only two things that differ by engine: the
// structural validation of a configuration and the engine-specific part of the
// create body. Everything else, including every API call, stays in the
// resource, so a driver is pure and needs no mock to test.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type driver interface {
	// validate reports structural problems of a configuration: attributes that
	// the engine does not accept, attributes it requires, and values that the
	// backend would silently override. It leaves business rules to the backend.
	validate(cfg engineConfig) diag.Diagnostics
	// fillCreateBody sets the part of the create body that depends on the
	// engine.
	fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics
}

var drivers = map[string]driver{
	engineCassandra:   cassandraDriver{},
	engineMemcached:   memcachedDriver{},
	engineMongoDB:     mongoDriver{},
	engineOpenSearch:  openSearchDriver{},
	engineStarRocks:   starRocksDriver{},
	engineTimescaleDB: timescaleDBDriver{},
}

// engineCreateInput carries a plan and the references resolved from it.
type engineCreateInput struct {
	Config engineConfig
	Refs   resolvedRefs
}

// engineConfig is the view of a configuration or a plan that drivers read.
type engineConfig struct {
	Topology         types.String
	EnableSSL        types.Bool
	KeepBackup       types.Bool
	EnablePITRBackup types.Bool
	Plugins          types.Set
	AutoBackup       types.Object
	EngineConfig     types.Object
	// PoolsKnown is false while the node_pools list is unknown, and then Pools
	// is empty.
	PoolsKnown bool
	Pools      []nodePoolModel
}

// resolvedRefs holds the IDs behind the references of a plan. Pools follows
// the order of engineConfig.Pools.
type resolvedRefs struct {
	RegionID        core.UUID
	ZoneID          *core.UUID
	SubnetID        core.UUID
	EngineVersionID core.UUID
	Pools           []resolvedPool
}

// resolvedPool holds the IDs behind the references of a node pool.
// StorageClassID is nil for a pool without storage.
type resolvedPool struct {
	ResourceConfigID core.UUID
	StorageClassID   *core.UUID
}

// driverFor returns the driver for the configured engine, or false when the
// engine is unknown or unsupported. Outer whitespace is not part of the engine.
func driverFor(engine types.String) (driver, bool) {
	if engine.IsNull() || engine.IsUnknown() {
		return nil, false
	}
	selected, ok := drivers[strings.TrimSpace(engine.ValueString())]
	return selected, ok
}

// supportedEngines lists the engines of the registry in a stable order.
func supportedEngines() []string {
	engines := make([]string, 0, len(drivers))
	for engine := range drivers {
		engines = append(engines, engine)
	}
	sort.Strings(engines)
	return engines
}

// engineConfigOf decodes the node pools of a configuration or plan.
func engineConfigOf(ctx context.Context, model DBClusterResourceModel) (engineConfig, diag.Diagnostics) {
	var diags diag.Diagnostics
	cfg := engineConfig{
		Topology:         model.Topology,
		EnableSSL:        model.EnableSSL,
		KeepBackup:       model.KeepBackup,
		EnablePITRBackup: model.EnablePITRBackup,
		Plugins:          model.Plugins,
		AutoBackup:       model.AutoBackupConfig,
		EngineConfig:     model.EngineConfig,
	}
	if model.NodePools.IsNull() || model.NodePools.IsUnknown() {
		return cfg, diags
	}
	// A pool that comes whole from another resource cannot decode yet, so the
	// pool checks wait for apply.
	for _, element := range model.NodePools.Elements() {
		if element.IsUnknown() {
			return cfg, diags
		}
	}
	diags.Append(model.NodePools.ElementsAs(ctx, &cfg.Pools, false)...)
	cfg.PoolsKnown = !diags.HasError()
	return cfg, diags
}

// validateCommon applies the rules that hold for every engine.
func validateCommon(cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics

	if cfg.PoolsKnown && len(cfg.Pools) == 0 {
		diags.AddAttributeError(path.Root("node_pools"), "Missing node pool", "node_pools must contain at least one node pool.")
	}

	diags.Append(validateAutoBackup(cfg)...)
	return diags
}

// validateAutoBackup checks the schedule against the interval, and PITR
// against the schedule. The backend rejects the same combinations.
func validateAutoBackup(cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	schedulePath := path.Root("auto_backup_config")

	autoBackupEnabled := false
	if !cfg.AutoBackup.IsNull() && !cfg.AutoBackup.IsUnknown() {
		attributes := cfg.AutoBackup.Attributes()
		interval, _ := attributes["backup_interval"].(types.String)
		dayOfWeek, _ := attributes["day_of_week"].(types.String)
		enabled, _ := attributes["enable_auto_backup"].(types.Bool)
		autoBackupEnabled = !enabled.IsNull() && !enabled.IsUnknown() && enabled.ValueBool()

		if !interval.IsNull() && !interval.IsUnknown() && !dayOfWeek.IsUnknown() {
			value := strings.TrimSpace(interval.ValueString())
			switch {
			case value == "weekly" && dayOfWeek.IsNull():
				diags.AddAttributeError(schedulePath.AtName("day_of_week"), "Missing day of week", "day_of_week is required when backup_interval is weekly.")
			case value == "daily" && !dayOfWeek.IsNull():
				diags.AddAttributeError(schedulePath.AtName("day_of_week"), "Unexpected day of week", "day_of_week must not be set when backup_interval is daily.")
			case value != "daily" && value != "weekly":
				diags.AddAttributeError(schedulePath.AtName("backup_interval"), "Invalid backup interval", "backup_interval must be daily or weekly.")
			}
		}
	}

	pitrRequested := !cfg.EnablePITRBackup.IsNull() && !cfg.EnablePITRBackup.IsUnknown() && cfg.EnablePITRBackup.ValueBool()
	scheduleUnknown := cfg.AutoBackup.IsUnknown()
	if pitrRequested && !scheduleUnknown && !autoBackupEnabled {
		diags.AddAttributeError(
			path.Root("enable_pitr_backup"),
			"PITR needs automatic backup",
			"enable_pitr_backup requires auto_backup_config with enable_auto_backup set to true.",
		)
	}
	return diags
}

// rejectAutoBackup reports a backup schedule on an engine without backups.
func rejectAutoBackup(engine string, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured(cfg.AutoBackup) {
		diags.AddAttributeError(path.Root("auto_backup_config"), "Backups are not supported", fmt.Sprintf("The %s engine has no backups, so auto_backup_config must not be set.", engine))
	}
	return diags
}

// validateRetention checks that a schedule sets the one retention attribute
// that the engine keeps backups by. The backend requires that attribute and
// rejects the other one.
func validateRetention(engine string, cfg engineConfig, required string) diag.Diagnostics {
	var diags diag.Diagnostics
	if !configured(cfg.AutoBackup) {
		return diags
	}
	unsupported := retentionMaxBackups
	if required == retentionMaxBackups {
		unsupported = retentionDays
	}
	attributes := cfg.AutoBackup.Attributes()
	schedulePath := path.Root("auto_backup_config")
	if value := attributes[required]; value != nil && value.IsNull() {
		diags.AddAttributeError(schedulePath.AtName(required), "Missing backup retention", fmt.Sprintf("%s is required for the %s engine.", required, engine))
	}
	if value := attributes[unsupported]; value != nil && configured(value) {
		diags.AddAttributeError(schedulePath.AtName(unsupported), "Unsupported backup retention", fmt.Sprintf("The %s engine keeps backups by %s, so %s must not be set.", engine, required, unsupported))
	}
	return diags
}

// rejectTopology reports a topology on an engine that has only one.
func rejectTopology(engine string, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured(cfg.Topology) {
		diags.AddAttributeError(path.Root("topology"), "Unsupported topology", fmt.Sprintf("The %s engine has a single topology, so topology must not be set.", engine))
	}
	return diags
}

// rejectPITR reports point-in-time recovery on an engine without it.
func rejectPITR(engine string, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured(cfg.EnablePITRBackup) && cfg.EnablePITRBackup.ValueBool() {
		diags.AddAttributeError(path.Root("enable_pitr_backup"), "PITR is not supported", fmt.Sprintf("The %s engine does not support point-in-time recovery.", engine))
	}
	return diags
}

// rejectEngineConfig reports engine_config on an engine other than memcached.
// The other engines have no engine settings and report an empty object, so a
// configured value would never reach the state.
func rejectEngineConfig(engine string, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured(cfg.EngineConfig) {
		diags.AddAttributeError(path.Root("engine_config"), "Unsupported engine config", fmt.Sprintf("engine_config applies to the memcached engine only, not %s.", engine))
	}
	return diags
}

// rejectOpenSearchSettings reports the plugins and the pool settings that only
// the opensearch engine accepts.
func rejectOpenSearchSettings(engine string, cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if setHasElements(cfg.Plugins) {
		diags.AddAttributeError(path.Root("plugins"), "Unsupported plugins", fmt.Sprintf("The %s engine has no plugin catalog, so plugins must not be set.", engine))
	}
	for i, pool := range cfg.Pools {
		poolPath := path.Root("node_pools").AtListIndex(i)
		if setHasElements(pool.EngineRoles) {
			diags.AddAttributeError(poolPath.AtName("engine_roles"), "Unsupported engine roles", "engine_roles applies to opensearch node pools only.")
		}
		if configured(attributeString(pool.PoolConfig, "attribute")) {
			diags.AddAttributeError(poolPath.AtName("pool_config"), "Unsupported pool config", "pool_config applies to opensearch node pools only.")
		}
	}
	return diags
}

// validatePoolStorage checks storage_class and disk_size of every pool. A pool
// with storage needs both, and a pool without storage, such as a memcached
// pool, must set neither, because the backend rejects them.
func validatePoolStorage(cfg engineConfig, withStorage bool) diag.Diagnostics {
	var diags diag.Diagnostics
	for i, pool := range cfg.Pools {
		poolPath := path.Root("node_pools").AtListIndex(i)
		for _, attribute := range []struct {
			name  string
			value attr.Value
		}{
			{"storage_class", pool.StorageClass},
			{"disk_size", pool.DiskSize},
		} {
			switch {
			case withStorage && attribute.value.IsNull():
				diags.AddAttributeError(poolPath.AtName(attribute.name), "Missing node pool storage", attribute.name+" is required for a node pool with storage.")
			case !withStorage && configured(attribute.value):
				diags.AddAttributeError(poolPath.AtName(attribute.name), "Unsupported node pool storage", attribute.name+" must not be set, because the node pools of this engine have no storage.")
			}
		}
	}
	return diags
}

// nodePoolsBody builds the node_pools part of a create body. A pool without
// storage sends neither storage_class_id nor disk_size.
func nodePoolsBody(in engineCreateInput) ([]dbaasclustersdk.DBClusterNodePoolCreateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(in.Config.Pools) != len(in.Refs.Pools) {
		diags.AddAttributeError(path.Root("node_pools"), "Invalid node pools", "Every node pool needs resolved references.")
		return nil, diags
	}

	pools := make([]dbaasclustersdk.DBClusterNodePoolCreateSchema, 0, len(in.Config.Pools))
	for i, pool := range in.Config.Pools {
		refs := in.Refs.Pools[i]
		item := dbaasclustersdk.DBClusterNodePoolCreateSchema{
			Name:             strings.TrimSpace(pool.Name.ValueString()),
			Replicas:         int(pool.Replicas.ValueInt64()),
			ResourceConfigId: refs.ResourceConfigID,
			StorageClassId:   core.NullableFromPtr(refs.StorageClassID),
		}
		if configured(pool.DiskSize) {
			diskSize := int(pool.DiskSize.ValueInt64())
			item.DiskSize = core.NullableFromPtr(&diskSize)
		}
		if configured(pool.PartitionCount) {
			partitions := int(pool.PartitionCount.ValueInt64())
			item.PartitionCount = &partitions
		}
		if setHasElements(pool.EngineRoles) {
			roles, roleDiags := nodeRoles(pool.EngineRoles, i)
			diags.Append(roleDiags...)
			item.EngineRoles = &roles
		}
		if attribute := attributeString(pool.PoolConfig, "attribute"); configured(attribute) {
			poolConfig := map[string]any{"attribute": strings.TrimSpace(attribute.ValueString())}
			item.PoolConfig = &poolConfig
		}
		pools = append(pools, item)
	}
	return pools, diags
}

// nodeRoles converts the engine roles of a pool, trimmed and without
// duplicates. The backend validates the values, so a role it does not know is
// reported by the API.
func nodeRoles(roles types.Set, index int) ([]dbaasclustersdk.OpenSearchNodeRole, diag.Diagnostics) {
	var diags diag.Diagnostics
	converted := make([]dbaasclustersdk.OpenSearchNodeRole, 0, len(roles.Elements()))
	seen := make(map[string]bool, len(roles.Elements()))
	for _, element := range roles.Elements() {
		role, ok := element.(types.String)
		if !ok || !configured(role) {
			diags.AddAttributeError(
				path.Root("node_pools").AtListIndex(index).AtName("engine_roles"),
				"Invalid engine role",
				fmt.Sprintf("engine_roles must hold known strings, got %s.", element),
			)
			continue
		}
		value := strings.TrimSpace(role.ValueString())
		if seen[value] {
			continue
		}
		seen[value] = true
		converted = append(converted, dbaasclustersdk.OpenSearchNodeRole(value))
	}
	return converted, diags
}

// configured reports whether a value holds a configuration. An unknown value
// counts as not configured because its content is not yet available.
func configured(value attr.Value) bool {
	return !value.IsNull() && !value.IsUnknown()
}

// setHasElements reports whether a configured set holds at least one element.
func setHasElements(value types.Set) bool {
	return configured(value) && len(value.Elements()) > 0
}
