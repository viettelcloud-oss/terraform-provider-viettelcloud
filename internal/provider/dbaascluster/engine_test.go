package dbaascluster

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// configPool builds a node pool as configuration holds it: Optional and
// Computed attributes are null because the practitioner omitted them.
func configPool(name string, replicas int64) nodePoolModel {
	return nodePoolModel{
		ID:             types.StringNull(),
		Name:           types.StringValue(name),
		Role:           types.StringNull(),
		Replicas:       types.Int64Value(replicas),
		PartitionCount: types.Int64Null(),
		NodeCount:      types.Int64Null(),
		ResourceConfig: reference.Value(reference.Name, types.StringValue("2c4g")),
		StorageClass:   reference.Value(reference.Name, types.StringValue("ssd")),
		DiskSize:       types.Int64Value(100),
		ActualDiskSize: types.Int64Null(),
		UsedDiskSize:   types.Int64Null(),
		EngineRoles:    types.SetNull(types.StringType),
		PoolConfig:     types.ObjectNull(poolConfigAttrTypes),
	}
}

func configOf(topology string, pools ...nodePoolModel) engineConfig {
	cfg := engineConfig{
		Topology:         types.StringNull(),
		EnableSSL:        types.BoolNull(),
		KeepBackup:       types.BoolNull(),
		EnablePITRBackup: types.BoolNull(),
		Plugins:          types.SetNull(types.ObjectType{AttrTypes: pluginAttrTypes}),
		AutoBackup:       types.ObjectNull(autoBackupAttrTypes),
		EngineConfig:     types.ObjectNull(engineConfigAttrTypes),
		PoolsKnown:       true,
		Pools:            pools,
	}
	if topology != "" {
		cfg.Topology = types.StringValue(topology)
	}
	return cfg
}

func backupSchedule(enabled bool, interval, day string) types.Object {
	dayValue := types.StringNull()
	if day != "" {
		dayValue = types.StringValue(day)
	}
	return types.ObjectValueMust(autoBackupAttrTypes, map[string]attr.Value{
		"enable_auto_backup": types.BoolValue(enabled),
		"backup_interval":    types.StringValue(interval),
		"day_of_week":        dayValue,
		"start_hour":         types.Int64Value(2),
		"max_auto_backup":    types.Int64Value(7),
		"retention_days":     types.Int64Null(),
	})
}

// retentionSchedule returns a daily schedule that keeps backups by the given
// retention attribute. A zero value leaves that attribute null.
func retentionSchedule(maxBackups, days int64) types.Object {
	value := func(v int64) types.Int64 {
		if v == 0 {
			return types.Int64Null()
		}
		return types.Int64Value(v)
	}
	return types.ObjectValueMust(autoBackupAttrTypes, map[string]attr.Value{
		"enable_auto_backup": types.BoolValue(true),
		"backup_interval":    types.StringValue("daily"),
		"day_of_week":        types.StringNull(),
		"start_hour":         types.Int64Value(2),
		"max_auto_backup":    value(maxBackups),
		"retention_days":     value(days),
	})
}

// storageless returns a pool that configures neither storage_class nor
// disk_size.
func storageless(pool nodePoolModel) nodePoolModel {
	pool.StorageClass = types.ObjectNull(reference.AttributeTypes(reference.Name))
	pool.DiskSize = types.Int64Null()
	return pool
}

func memcachedSettings(mode string) types.Object {
	return types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
		"mode":                  types.StringValue(mode),
		"auth_enabled":          types.BoolNull(),
		"mcrouter_routing_type": types.StringNull(),
	})
}

func shardingPool(shards int64) nodePoolModel {
	pool := configPool("shard", 3)
	pool.PartitionCount = types.Int64Value(shards)
	return pool
}

// errorPaths returns the attribute path of every error, and "" for an error
// without one.
func errorPaths(diags diag.Diagnostics) []string {
	var paths []string
	for _, d := range diags.Errors() {
		if withPath, ok := d.(diag.DiagnosticWithPath); ok {
			paths = append(paths, withPath.Path().String())
			continue
		}
		paths = append(paths, "")
	}
	return paths
}

func TestDriverValidate(t *testing.T) {
	t.Parallel()

	manager := configPool("manager", 3)
	data := configPool("data", 2)
	rolesPool := configPool("mongo", 3)
	rolesPool.EngineRoles = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("data")})
	tieredPool := configPool("mongo", 3)
	tieredPool.PoolConfig = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("hot")})
	rsPartitioned := configPool("mongo", 3)
	rsPartitioned.PartitionCount = types.Int64Value(2)
	rsSingleShard := configPool("mongo", 3)
	rsSingleShard.PartitionCount = types.Int64Value(1)
	duplicateRoles := configPool("data", 2)
	duplicateRoles.EngineRoles = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("data"), types.StringValue(" data\t")})
	paddedRoles := configPool("data", 2)
	paddedRoles.EngineRoles = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(" data "), types.StringValue("ingest")})
	plugin := types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{
		types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue("analysis-icu")}),
	})

	tests := []struct {
		name   string
		engine string
		cfg    engineConfig
		mutate func(*engineConfig)
		want   []string
	}{
		{name: "mongo replica set", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3))},
		{name: "mongo replica set accepts a partition count of one", engine: engineMongoDB, cfg: configOf(topologyMongoRs, rsSingleShard)},
		{name: "mongo sharding", engine: engineMongoDB, cfg: configOf(topologyMongoSharding, shardingPool(2))},
		{name: "mongo missing topology", engine: engineMongoDB, cfg: configOf("", configPool("mongo", 3)), want: []string{"topology"}},
		{name: "mongo unknown topology", engine: engineMongoDB, cfg: configOf("replicaset", configPool("mongo", 3)), want: []string{"topology"}},
		{name: "mongo unknown topology value defers", engine: engineMongoDB, cfg: configOf("", configPool("mongo", 3)), mutate: func(c *engineConfig) { c.Topology = types.StringUnknown() }},
		{name: "mongo pool name with outer whitespace", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool(" mongo ", 3))},
		{name: "mongo replica set pool must be named mongo", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("shard", 3)), want: []string{"node_pools[0].name"}},
		{name: "mongo sharding pool must be named shard", engine: engineMongoDB, cfg: configOf(topologyMongoSharding, func() nodePoolModel { p := shardingPool(2); p.Name = types.StringValue("mongo"); return p }()), want: []string{"node_pools[0].name"}},
		{name: "mongo takes one pool", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3), configPool("extra", 3)), want: []string{"node_pools", "node_pools[1].name"}},
		{name: "mongo sharding needs the shard count", engine: engineMongoDB, cfg: configOf(topologyMongoSharding, configPool("shard", 3)), want: []string{"node_pools[0].partition_count"}},
		{name: "mongo replica set rejects extra partitions", engine: engineMongoDB, cfg: configOf(topologyMongoRs, rsPartitioned), want: []string{"node_pools[0].partition_count"}},
		{name: "mongo rejects engine roles", engine: engineMongoDB, cfg: configOf(topologyMongoRs, rolesPool), want: []string{"node_pools[0].engine_roles"}},
		{name: "mongo rejects pool config", engine: engineMongoDB, cfg: configOf(topologyMongoRs, tieredPool), want: []string{"node_pools[0].pool_config"}},
		{name: "mongo rejects plugins", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.Plugins = plugin }, want: []string{"plugins"}},
		{name: "mongo sharding rejects disabled ssl", engine: engineMongoDB, cfg: configOf(topologyMongoSharding, shardingPool(2)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(false) }, want: []string{"enable_ssl"}},
		{name: "mongo sharding accepts enabled ssl", engine: engineMongoDB, cfg: configOf(topologyMongoSharding, shardingPool(2)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(true) }},
		{name: "mongo replica set accepts disabled ssl", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(false) }},
		{name: "mongo pitr with a schedule", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) {
			c.EnablePITRBackup = types.BoolValue(true)
			c.AutoBackup = backupSchedule(true, "daily", "")
		}},
		{name: "pitr without a schedule", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.EnablePITRBackup = types.BoolValue(true) }, want: []string{"enable_pitr_backup"}},
		{name: "pitr with a disabled schedule", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) {
			c.EnablePITRBackup = types.BoolValue(true)
			c.AutoBackup = backupSchedule(false, "daily", "")
		}, want: []string{"enable_pitr_backup"}},
		{name: "weekly schedule needs a day", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "weekly", "") }, want: []string{"auto_backup_config.day_of_week"}},
		{name: "weekly schedule with a day", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "weekly", "monday") }},
		{name: "daily schedule rejects a day", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "daily", "monday") }, want: []string{"auto_backup_config.day_of_week"}},
		{name: "schedule rejects an unknown interval", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "hourly", "") }, want: []string{"auto_backup_config.backup_interval"}},

		{name: "opensearch with two pools", engine: engineOpenSearch, cfg: configOf("", manager, data)},
		{name: "opensearch rejects a topology", engine: engineOpenSearch, cfg: configOf(topologyMongoRs, manager), want: []string{"topology"}},
		{name: "opensearch rejects pitr", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) {
			c.EnablePITRBackup = types.BoolValue(true)
			c.AutoBackup = backupSchedule(true, "daily", "")
		}, want: []string{"enable_pitr_backup"}},
		{name: "opensearch rejects disabled ssl", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(false) }, want: []string{"enable_ssl"}},
		{name: "opensearch accepts enabled ssl", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(true) }},
		{name: "opensearch rejects kept backups", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.KeepBackup = types.BoolValue(true) }, want: []string{"keep_backup"}},
		{name: "opensearch accepts deleted backups", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.KeepBackup = types.BoolValue(false) }},
		{name: "opensearch accepts plugins", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.Plugins = plugin }},
		{name: "opensearch rejects plugins that differ only by outer whitespace", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) {
			c.Plugins = types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{
				types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue("analysis-icu")}),
				types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue(" analysis-icu ")}),
			})
		}, want: []string{"plugins"}},
		{name: "opensearch rejects engine roles that differ only by outer whitespace", engine: engineOpenSearch, cfg: configOf("", duplicateRoles), want: []string{"node_pools[0].engine_roles"}},
		{name: "opensearch accepts padded distinct engine roles", engine: engineOpenSearch, cfg: configOf("", paddedRoles)},
		{name: "opensearch leaves duplicate pool names to the backend", engine: engineOpenSearch, cfg: configOf("", manager, configPool("manager", 1))},
		{name: "opensearch rejects engine config", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("direct") }, want: []string{"engine_config"}},
		{name: "opensearch needs pool storage", engine: engineOpenSearch, cfg: configOf("", storageless(manager)), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},
		{name: "opensearch needs a pool", engine: engineOpenSearch, cfg: configOf(""), want: []string{"node_pools"}},
		{name: "cassandra", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3))},
		{name: "cassandra leaves the pool name to the backend", engine: engineCassandra, cfg: configOf("", configPool("node", 3))},
		{name: "cassandra with a schedule", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "daily", "") }},
		{name: "cassandra rejects a topology", engine: engineCassandra, cfg: configOf(topologyMongoRs, configPool("cassandra", 3)), want: []string{"topology"}},
		{name: "cassandra rejects pitr", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) {
			c.EnablePITRBackup = types.BoolValue(true)
			c.AutoBackup = backupSchedule(true, "daily", "")
		}, want: []string{"enable_pitr_backup"}},
		{name: "cassandra rejects enabled ssl", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(true) }, want: []string{"enable_ssl"}},
		{name: "cassandra accepts disabled ssl", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(false) }},
		{name: "cassandra rejects plugins", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.Plugins = plugin }, want: []string{"plugins"}},
		{name: "cassandra rejects engine roles and pool config", engine: engineCassandra, cfg: configOf("", rolesPool, tieredPool), want: []string{"node_pools[0].engine_roles", "node_pools[1].pool_config"}},
		{name: "cassandra rejects engine config", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("direct") }, want: []string{"engine_config"}},
		{name: "cassandra needs pool storage", engine: engineCassandra, cfg: configOf("", storageless(configPool("cassandra", 3))), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},

		{name: "memcached", engine: engineMemcached, cfg: configOf("", storageless(configPool("memcached", 2)))},
		{name: "memcached in mcrouter mode", engine: engineMemcached, cfg: configOf("", storageless(configPool("memcached", 2)), storageless(configPool("mcrouter", 2))), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("mcrouter") }},
		{name: "memcached rejects pool storage", engine: engineMemcached, cfg: configOf("", configPool("memcached", 2)), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},
		{name: "memcached defers unknown pool storage", engine: engineMemcached, cfg: configOf("", func() nodePoolModel {
			p := configPool("memcached", 2)
			p.StorageClass = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
			p.DiskSize = types.Int64Unknown()
			return p
		}())},
		{name: "memcached rejects a schedule", engine: engineMemcached, cfg: configOf("", storageless(configPool("memcached", 2))), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "daily", "") }, want: []string{"auto_backup_config"}},
		{name: "memcached rejects pitr", engine: engineMemcached, cfg: configOf("", storageless(configPool("memcached", 2))), mutate: func(c *engineConfig) { c.EnablePITRBackup = types.BoolValue(true) }, want: []string{"enable_pitr_backup", "enable_pitr_backup"}},
		{name: "memcached rejects a topology", engine: engineMemcached, cfg: configOf(topologyMongoRs, storageless(configPool("memcached", 2))), want: []string{"topology"}},
		{name: "memcached rejects plugins", engine: engineMemcached, cfg: configOf("", storageless(configPool("memcached", 2))), mutate: func(c *engineConfig) { c.Plugins = plugin }, want: []string{"plugins"}},
		{name: "mongo rejects engine config", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("direct") }, want: []string{"engine_config"}},
		{name: "mongo needs pool storage", engine: engineMongoDB, cfg: configOf(topologyMongoRs, storageless(configPool("mongo", 3))), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},
		{name: "mongo schedule needs max_auto_backup", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(0, 0) }, want: []string{"auto_backup_config.max_auto_backup"}},
		{name: "mongo schedule rejects retention_days", engine: engineMongoDB, cfg: configOf(topologyMongoRs, configPool("mongo", 3)), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(7, 8) }, want: []string{"auto_backup_config.retention_days"}},
		{name: "cassandra schedule needs max_auto_backup", engine: engineCassandra, cfg: configOf("", configPool("cassandra", 3)), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(0, 8) }, want: []string{"auto_backup_config.max_auto_backup", "auto_backup_config.retention_days"}},
		{name: "opensearch schedule needs max_auto_backup", engine: engineOpenSearch, cfg: configOf("", manager), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(0, 0) }, want: []string{"auto_backup_config.max_auto_backup"}},

		{name: "starrocks", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1), configPool("be", 3))},
		{name: "starrocks leaves pool names and replicas to the backend", engine: engineStarRocks, cfg: configOf("", configPool("fe", 2))},
		{name: "starrocks rejects a schedule", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1), configPool("be", 3)), mutate: func(c *engineConfig) { c.AutoBackup = backupSchedule(true, "daily", "") }, want: []string{"auto_backup_config"}},
		{name: "starrocks rejects pitr", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1), configPool("be", 3)), mutate: func(c *engineConfig) { c.EnablePITRBackup = types.BoolValue(true) }, want: []string{"enable_pitr_backup", "enable_pitr_backup"}},
		{name: "starrocks rejects enabled ssl", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1), configPool("be", 3)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(true) }, want: []string{"enable_ssl"}},
		{name: "starrocks accepts disabled ssl", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1), configPool("be", 3)), mutate: func(c *engineConfig) { c.EnableSSL = types.BoolValue(false) }},
		{name: "starrocks rejects a topology", engine: engineStarRocks, cfg: configOf(topologyMongoRs, configPool("fe", 1)), want: []string{"topology"}},
		{name: "starrocks rejects engine config", engine: engineStarRocks, cfg: configOf("", configPool("fe", 1)), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("direct") }, want: []string{"engine_config"}},
		{name: "starrocks rejects plugins and pool settings", engine: engineStarRocks, cfg: configOf("", rolesPool), mutate: func(c *engineConfig) { c.Plugins = plugin }, want: []string{"node_pools[0].engine_roles", "plugins"}},
		{name: "starrocks needs pool storage", engine: engineStarRocks, cfg: configOf("", storageless(configPool("fe", 1))), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},

		{name: "timescaledb", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1))},
		{name: "timescaledb with pitr and a schedule", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1)), mutate: func(c *engineConfig) {
			c.EnablePITRBackup = types.BoolValue(true)
			c.AutoBackup = retentionSchedule(0, 8)
		}},
		{name: "timescaledb schedule needs retention_days", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1)), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(0, 0) }, want: []string{"auto_backup_config.retention_days"}},
		{name: "timescaledb schedule rejects max_auto_backup", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1)), mutate: func(c *engineConfig) { c.AutoBackup = retentionSchedule(7, 8) }, want: []string{"auto_backup_config.max_auto_backup"}},
		{name: "timescaledb pitr needs a schedule", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1)), mutate: func(c *engineConfig) { c.EnablePITRBackup = types.BoolValue(true) }, want: []string{"enable_pitr_backup"}},
		{name: "timescaledb leaves the pool count to the backend", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1), configPool("replica", 1))},
		{name: "timescaledb rejects a topology", engine: engineTimescaleDB, cfg: configOf(topologyMongoRs, configPool("db", 1)), want: []string{"topology"}},
		{name: "timescaledb rejects engine config", engine: engineTimescaleDB, cfg: configOf("", configPool("db", 1)), mutate: func(c *engineConfig) { c.EngineConfig = memcachedSettings("direct") }, want: []string{"engine_config"}},
		{name: "timescaledb rejects plugins and pool settings", engine: engineTimescaleDB, cfg: configOf("", tieredPool), mutate: func(c *engineConfig) { c.Plugins = plugin }, want: []string{"node_pools[0].pool_config", "plugins"}},
		{name: "timescaledb needs pool storage", engine: engineTimescaleDB, cfg: configOf("", storageless(configPool("db", 1))), want: []string{"node_pools[0].disk_size", "node_pools[0].storage_class"}},
		{name: "unknown pools defer pool checks", engine: engineOpenSearch, cfg: configOf(""), mutate: func(c *engineConfig) { c.PoolsKnown = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt.cfg
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			selected, ok := drivers[tt.engine]
			if !ok {
				t.Fatalf("no driver for %s", tt.engine)
			}
			got := errorPaths(selected.validate(cfg))
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("error paths = %v, want %v", got, want)
			}
		})
	}
}

func TestDriverForRejectsUnsupportedEngines(t *testing.T) {
	t.Parallel()

	for _, engine := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("postgres"), types.StringValue("MongoDB"), types.StringValue("")} {
		if _, ok := driverFor(engine); ok {
			t.Errorf("driverFor(%s) found a driver", engine)
		}
	}
	for _, engine := range supportedEngines() {
		if _, ok := driverFor(types.StringValue(engine)); !ok {
			t.Errorf("driverFor(%q) found no driver", engine)
		}
	}
	if got := strings.Join(supportedEngines(), ","); got != "cassandra,memcached,mongodb,opensearch,starrocks,timescaledb" {
		t.Errorf("supported engines = %q", got)
	}
}

func TestEngineConfigOfDecodesPools(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() || !cfg.PoolsKnown || len(cfg.Pools) != 1 || cfg.Pools[0].Name.ValueString() != "mongo" {
		t.Fatalf("unexpected config: %#v diags=%v", cfg, diags)
	}

	plan.NodePools = types.ListUnknown(types.ObjectType{AttrTypes: nodePoolAttrTypes})
	cfg, diags = engineConfigOf(context.Background(), plan)
	if diags.HasError() || cfg.PoolsKnown {
		t.Fatalf("an unknown pool list must not decode: %#v diags=%v", cfg, diags)
	}

	plan.NodePools = testPools(types.ObjectUnknown(nodePoolAttrTypes))
	cfg, diags = engineConfigOf(context.Background(), plan)
	if diags.HasError() || cfg.PoolsKnown {
		t.Fatalf("an unknown pool must not decode: %#v diags=%v", cfg, diags)
	}
}
