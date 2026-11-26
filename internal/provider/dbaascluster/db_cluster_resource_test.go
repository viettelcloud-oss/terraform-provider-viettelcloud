package dbaascluster

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	dbaasclusterlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaascluster/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

func TestDBClusterModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	var resourceModel DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &resourceModel)
	plan := tfsdk.Plan{Schema: resourceSchemaForTest(t)}
	if diags := plan.Set(context.Background(), &resourceModel); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}

	for _, cluster := range []*dbaasclustersdk.DBClusterDetailSchema{sampleCluster(), sampleMemcachedCluster()} {
		var dataSourceModel DBClusterDataSourceModel
		populateDBClusterDataSourceState(cluster, &dataSourceModel)
		state := tfsdk.State{Schema: dataSourceSchemaForTest(t).Schema}
		if diags := state.Set(context.Background(), &dataSourceModel); diags.HasError() {
			t.Fatalf("data source model does not match schema for %s: %v", cluster.Engine, diags)
		}
	}
}

func TestDBClusterSchemaFlags(t *testing.T) {
	t.Parallel()

	attributes := resourceSchemaForTest(t).Attributes
	password, ok := attributes["root_password"].(schema.StringAttribute)
	if !ok || !password.Required || !password.Sensitive || !password.WriteOnly {
		t.Fatalf("root_password must be required, sensitive and write-only: %#v", attributes["root_password"])
	}
	for _, name := range []string{"description", "keep_backup"} {
		attribute := attributes[name]
		if !attribute.IsOptional() || !attribute.IsComputed() {
			t.Errorf("%s must be optional and computed", name)
		}
	}
	for _, name := range []string{"id", "status", "operational_status", "error_message", "endpoint", "admin_username", "created_at", "updated_at", "enable_auto_backup"} {
		if attribute := attributes[name]; !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
			t.Errorf("%s must be computed only", name)
		}
	}
	for _, name := range []string{"name", "engine", "engine_version", "region", "subnet", "node_pools", "root_password"} {
		if !attributes[name].IsRequired() {
			t.Errorf("%s must be required", name)
		}
	}
	engineConfig := attributes["engine_config"]
	if !engineConfig.IsOptional() || !engineConfig.IsComputed() {
		t.Error("engine_config must be optional and computed")
	}
	// A memcached pool has no storage, so ValidateConfig requires storage
	// per engine instead of the schema.
	pool := attributes["node_pools"].(schema.ListNestedAttribute).NestedObject.Attributes
	for _, name := range []string{"storage_class", "disk_size"} {
		if attribute := pool[name]; !attribute.IsOptional() || attribute.IsComputed() {
			t.Errorf("node_pools.%s must be optional only", name)
		}
	}
}

func TestBuildMongoCreateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		topology string
		pool     attr.Value
		wantPool map[string]any
	}{
		{
			name:     "replica set",
			topology: topologyMongoRs,
			pool:     testPool("mongo", 3, 0, 100),
			wantPool: map[string]any{"name": "mongo", "replicas": 3.0},
		},
		{
			name:     "sharding",
			topology: topologyMongoSharding,
			pool:     testPool("shard", 3, 2, 100),
			wantPool: map[string]any{"name": "shard", "replicas": 3.0, "partition_count": 2.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := testPlan(engineMongoDB, tt.topology, testPools(tt.pool))
			cfg, diags := engineConfigOf(context.Background(), plan)
			if diags.HasError() {
				t.Fatalf("decode: %v", diags)
			}
			body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(1), drivers[engineMongoDB])
			if diags.HasError() {
				t.Fatalf("build: %v", diags)
			}

			got := bodyMap(t, body)
			if got["topology"] != tt.topology {
				t.Errorf("topology = %#v, want %q", got["topology"], tt.topology)
			}
			if !reflect.DeepEqual(got["engine_config"], map[string]any{}) {
				t.Errorf("engine_config = %#v, want an empty object", got["engine_config"])
			}
			wantPool := map[string]any{
				"disk_size": 100.0, "resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
			}
			maps.Copy(wantPool, tt.wantPool)
			if !reflect.DeepEqual(got["node_pools"], []any{wantPool}) {
				t.Errorf("node_pools = %#v, want %#v", got["node_pools"], []any{wantPool})
			}
			wantValues := map[string]any{
				"name": "orders", "engine": "mongodb", "root_password": "Passw0rd!",
				"region_id": testRegionID.String(), "subnet_id": testSubnetID.String(), "engine_version_id": testVersionID.String(),
			}
			for key, want := range wantValues {
				if got[key] != want {
					t.Errorf("%s = %#v, want %#v", key, got[key], want)
				}
			}
			// The backend rejects the cluster-level fields beside node_pools and
			// topology beside topology_config, and an unknown value must never
			// reach the request.
			for _, key := range []string{
				"resource_config_id", "storage_class_id", "disk_size", "topology_config",
				"plugins", "zone_id", "description", "keep_backup", "allowed_cidrs", "metadata",
				"maintenance_config", "auto_backup_config", "enable_ssl", "enable_public_access", "enable_pitr_backup",
				"enable_auto_increase_volume_size",
			} {
				if _, present := got[key]; present {
					t.Errorf("%s must not be sent: %#v", key, got[key])
				}
			}
		})
	}
}

func TestBuildOpenSearchCreateBody(t *testing.T) {
	t.Parallel()

	manager := testPool("manager", 3, 0, 50).(types.Object).Attributes()
	manager["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("cluster_manager")})
	manager["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("hot")})
	plan := testPlan(engineOpenSearch, "", testPools(
		types.ObjectValueMust(nodePoolAttrTypes, manager),
		testPool("data", 2, 0, 80),
	))
	plan.Plugins = types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{
		types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue("analysis-icu")}),
	})
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(2), drivers[engineOpenSearch])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}

	got := bodyMap(t, body)
	for _, key := range []string{"resource_config_id", "storage_class_id", "disk_size", "topology_config", "engine_config", "keep_backup", "enable_ssl"} {
		if _, present := got[key]; present {
			t.Errorf("%s must not be sent for opensearch: %#v", key, got[key])
		}
	}
	if !reflect.DeepEqual(got["plugins"], []any{map[string]any{"name": "analysis-icu"}}) {
		t.Errorf("plugins = %#v", got["plugins"])
	}
	pools, ok := got["node_pools"].([]any)
	if !ok || len(pools) != 2 {
		t.Fatalf("node_pools = %#v", got["node_pools"])
	}
	wantManager := map[string]any{
		"name": "manager", "replicas": 3.0, "disk_size": 50.0,
		"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
		"engine_roles": []any{"cluster_manager"}, "pool_config": map[string]any{"attribute": "hot"},
	}
	if !reflect.DeepEqual(pools[0], wantManager) {
		t.Errorf("manager pool = %#v, want %#v", pools[0], wantManager)
	}
	wantData := map[string]any{
		"name": "data", "replicas": 2.0, "disk_size": 80.0,
		"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
	}
	if !reflect.DeepEqual(pools[1], wantData) {
		t.Errorf("data pool = %#v, want %#v", pools[1], wantData)
	}
}

func TestBuildCassandraCreateBody(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineCassandra, "", testPools(testPool("cassandra", 3, 0, 100)))
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(1), drivers[engineCassandra])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}

	got := bodyMap(t, body)
	// The backend takes exactly one of topology_config and engine_config, and
	// rejects the flat fields beside node_pools.
	for _, key := range []string{"resource_config_id", "storage_class_id", "disk_size", "topology_config", "plugins"} {
		if _, present := got[key]; present {
			t.Errorf("%s must not be sent for cassandra: %#v", key, got[key])
		}
	}
	if !reflect.DeepEqual(got["engine_config"], map[string]any{}) {
		t.Errorf("engine_config = %#v, want an empty object", got["engine_config"])
	}
	wantPools := []any{map[string]any{
		"name": "cassandra", "replicas": 3.0, "disk_size": 100.0,
		"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
	}}
	if !reflect.DeepEqual(got["node_pools"], wantPools) {
		t.Errorf("node_pools = %#v, want %#v", got["node_pools"], wantPools)
	}
}

func TestBuildMemcachedCreateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings types.Object
		want     map[string]any
	}{
		{
			name:     "omitted settings send an empty engine config",
			settings: types.ObjectUnknown(engineConfigAttrTypes),
			want:     map[string]any{},
		},
		{
			name: "configured settings",
			settings: types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
				"mode":                  types.StringValue("mcrouter"),
				"auth_enabled":          types.BoolUnknown(),
				"mcrouter_routing_type": types.StringValue("sharded"),
			}),
			want: map[string]any{"mode": "mcrouter", "mcrouter_routing_type": "sharded"},
		},
		{
			name: "an explicit false is sent",
			settings: types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
				"mode":                  types.StringUnknown(),
				"auth_enabled":          types.BoolValue(false),
				"mcrouter_routing_type": types.StringUnknown(),
			}),
			want: map[string]any{"auth_enabled": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
			plan.EngineConfig = tt.settings
			cfg, diags := engineConfigOf(context.Background(), plan)
			if diags.HasError() {
				t.Fatalf("decode: %v", diags)
			}
			refs := testRefs(1)
			refs.Pools[0].StorageClassID = nil
			body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, refs, drivers[engineMemcached])
			if diags.HasError() {
				t.Fatalf("build: %v", diags)
			}

			got := bodyMap(t, body)
			if !reflect.DeepEqual(got["engine_config"], tt.want) {
				t.Errorf("engine_config = %#v, want %#v", got["engine_config"], tt.want)
			}
			for _, key := range []string{"resource_config_id", "storage_class_id", "disk_size", "topology_config", "auto_backup_config"} {
				if _, present := got[key]; present {
					t.Errorf("%s must not be sent for memcached: %#v", key, got[key])
				}
			}
			// A memcached pool has no storage, and the backend rejects it.
			wantPools := []any{map[string]any{"name": "memcached", "replicas": 2.0, "resource_config_id": testPackageID.String()}}
			if !reflect.DeepEqual(got["node_pools"], wantPools) {
				t.Errorf("node_pools = %#v, want %#v", got["node_pools"], wantPools)
			}
		})
	}
}

func TestBuildStarRocksCreateBody(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineStarRocks, "", testPools(testPool("fe", 1, 0, 50), testPool("be", 3, 0, 200)))
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(2), drivers[engineStarRocks])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}

	got := bodyMap(t, body)
	// The backend requires engine_config for starrocks and rejects
	// topology_config and the flat fields.
	for _, key := range []string{"resource_config_id", "storage_class_id", "disk_size", "topology_config", "plugins"} {
		if _, present := got[key]; present {
			t.Errorf("%s must not be sent for starrocks: %#v", key, got[key])
		}
	}
	if !reflect.DeepEqual(got["engine_config"], map[string]any{}) {
		t.Errorf("engine_config = %#v, want an empty object", got["engine_config"])
	}
	pool := func(name string, replicas, disk float64) map[string]any {
		return map[string]any{
			"name": name, "replicas": replicas, "disk_size": disk,
			"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
		}
	}
	if want := []any{pool("fe", 1, 50), pool("be", 3, 200)}; !reflect.DeepEqual(got["node_pools"], want) {
		t.Errorf("node_pools = %#v, want %#v", got["node_pools"], want)
	}
}

func TestBuildTimescaleDBCreateBody(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineTimescaleDB, "", testPools(testPool("db", 1, 0, 100)))
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(1), drivers[engineTimescaleDB])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}

	got := bodyMap(t, body)
	// TimescaleDB takes neither topology_config nor engine_config.
	for _, key := range []string{"resource_config_id", "storage_class_id", "disk_size", "topology_config", "engine_config", "plugins"} {
		if _, present := got[key]; present {
			t.Errorf("%s must not be sent for timescaledb: %#v", key, got[key])
		}
	}
	want := []any{map[string]any{
		"name": "db", "replicas": 1.0, "disk_size": 100.0,
		"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
	}}
	if !reflect.DeepEqual(got["node_pools"], want) {
		t.Errorf("node_pools = %#v, want %#v", got["node_pools"], want)
	}
}

func TestBuildCreateBodySendsConfiguredOptionalValues(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	plan.Name = types.StringValue("  orders  ")
	plan.Description = types.StringValue("")
	plan.KeepBackup = types.BoolValue(false)
	plan.EnableSSL = types.BoolValue(true)
	plan.EnablePITRBackup = types.BoolValue(true)
	plan.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/24")})
	plan.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"team": types.StringValue("data")})
	plan.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
		"day_of_week": types.StringValue("monday"), "start_hour": types.Int64Unknown(),
	})
	plan.AutoBackupConfig = backupSchedule(true, "weekly", "friday")
	plan.Zone = reference.Value(reference.Name, types.StringValue("zone-a"))
	cfg, diags := engineConfigOf(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	refs := testRefs(1)
	zoneID := testPoolID
	refs.ZoneID = &zoneID

	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, refs, drivers[engineMongoDB])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}
	got := bodyMap(t, body)

	want := map[string]any{
		"name": "orders", "description": "", "keep_backup": false, "enable_ssl": true, "enable_pitr_backup": true,
		"zone_id":            testPoolID.String(),
		"allowed_cidrs":      []any{"10.0.0.0/24"},
		"metadata":           map[string]any{"team": "data"},
		"maintenance_config": map[string]any{"day_of_week": "monday"},
		"auto_backup_config": map[string]any{
			"backup_interval": "weekly", "day_of_week": "friday", "enable_auto_backup": true, "max_auto_backup": 7.0, "start_hour": 2.0,
		},
	}
	for key, wantValue := range want {
		if !reflect.DeepEqual(got[key], wantValue) {
			t.Errorf("%s = %#v, want %#v", key, got[key], wantValue)
		}
	}
}

// paddedString returns s with outer whitespace, as a practitioner may
// configure it.
func paddedString(s string) types.String { return types.StringValue(" " + s + "\t") }

// paddedPoolAttributes returns a planned pool whose name has outer whitespace.
func paddedPoolAttributes(name string, replicas, diskSize int64) map[string]attr.Value {
	pool := testPool(name, replicas, 0, diskSize).(types.Object).Attributes()
	pool["name"] = paddedString(name)
	return pool
}

// TestBuildCreateBodyTrimsUserStrings checks that every user-supplied string
// of the create body goes out without outer whitespace.
func TestBuildCreateBodyTrimsUserStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		engine string
		plan   func() DBClusterResourceModel
		pools  int
		want   map[string]any
	}{
		{
			name:   "mongodb description, pool name, and days of week",
			engine: engineMongoDB,
			plan: func() DBClusterResourceModel {
				plan := testPlan(engineMongoDB, topologyMongoRs, testPools(types.ObjectValueMust(nodePoolAttrTypes, paddedPoolAttributes("mongo", 3, 100))))
				plan.Description = paddedString("primary")
				plan.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
					"day_of_week": paddedString("monday"), "start_hour": types.Int64Unknown(),
				})
				plan.AutoBackupConfig = backupSchedule(true, "weekly", " friday\t")
				return plan
			},
			pools: 1,
			want: map[string]any{
				"description":        "primary",
				"maintenance_config": map[string]any{"day_of_week": "monday"},
				"auto_backup_config": map[string]any{
					"backup_interval": "weekly", "day_of_week": "friday", "enable_auto_backup": true, "max_auto_backup": 7.0, "start_hour": 2.0,
				},
				"node_pools": []any{map[string]any{
					"name": "mongo", "replicas": 3.0, "disk_size": 100.0,
					"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
				}},
			},
		},
		{
			name:   "opensearch pool name, engine roles, pool tier, and plugin name",
			engine: engineOpenSearch,
			plan: func() DBClusterResourceModel {
				pool := paddedPoolAttributes("data", 2, 80)
				pool["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{paddedString("data"), paddedString("ingest")})
				pool["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": paddedString("hot")})
				plan := testPlan(engineOpenSearch, "", testPools(types.ObjectValueMust(nodePoolAttrTypes, pool)))
				plan.Plugins = types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{
					types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": paddedString("analysis-icu")}),
				})
				return plan
			},
			pools: 1,
			want: map[string]any{
				"plugins": []any{map[string]any{"name": "analysis-icu"}},
				"node_pools": []any{map[string]any{
					"name": "data", "replicas": 2.0, "disk_size": 80.0,
					"resource_config_id": testPackageID.String(), "storage_class_id": testClassID.String(),
					"engine_roles": []any{"data", "ingest"}, "pool_config": map[string]any{"attribute": "hot"},
				}},
			},
		},
		{
			name:   "memcached mode and routing",
			engine: engineMemcached,
			plan: func() DBClusterResourceModel {
				plan := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
				plan.EngineConfig = types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
					"mode": paddedString("mcrouter"), "auth_enabled": types.BoolUnknown(), "mcrouter_routing_type": paddedString("sharded"),
				})
				return plan
			},
			pools: 1,
			want:  map[string]any{"engine_config": map[string]any{"mode": "mcrouter", "mcrouter_routing_type": "sharded"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := tt.plan()
			cfg, diags := engineConfigOf(context.Background(), plan)
			if diags.HasError() {
				t.Fatalf("decode: %v", diags)
			}
			refs := testRefs(tt.pools)
			if tt.engine == engineMemcached {
				refs.Pools[0].StorageClassID = nil
			}
			body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, refs, drivers[tt.engine])
			if diags.HasError() {
				t.Fatalf("build: %v", diags)
			}
			got := bodyMap(t, body)
			for key, want := range tt.want {
				if !reflect.DeepEqual(got[key], want) {
					t.Errorf("%s = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}

func TestNodeRolesTrimsAndDropsDuplicates(t *testing.T) {
	t.Parallel()

	roles := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("data"), paddedString("data"), paddedString("ingest")})
	got, diags := nodeRoles(roles, 0)
	if diags.HasError() {
		t.Fatalf("node roles: %v", diags)
	}
	values := make([]string, 0, len(got))
	for _, role := range got {
		values = append(values, string(role))
	}
	slices.Sort(values)
	if !slices.Equal(values, []string{"data", "ingest"}) {
		t.Fatalf("roles = %q, want data and ingest once each", values)
	}
}

func TestBuildCreateBodyDailyScheduleOmitsDay(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	plan.AutoBackupConfig = backupSchedule(true, "daily", "")
	cfg, _ := engineConfigOf(context.Background(), plan)
	body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(1), drivers[engineMongoDB])
	if diags.HasError() {
		t.Fatalf("build: %v", diags)
	}
	schedule, _ := bodyMap(t, body)["auto_backup_config"].(map[string]any)
	if schedule["backup_interval"] != "daily" {
		t.Fatalf("schedule = %#v", schedule)
	}
	if _, present := schedule["day_of_week"]; present {
		t.Fatalf("a daily schedule must not send day_of_week: %#v", schedule)
	}
}

func TestBuildCreateBodySendsOnlyTheConfiguredRetention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		schedule  types.Object
		wantKey   string
		wantValue float64
		otherKey  string
	}{
		{name: "by count", schedule: retentionSchedule(7, 0), wantKey: "max_auto_backup", wantValue: 7, otherKey: "retention_days"},
		{name: "by age", schedule: retentionSchedule(0, 8), wantKey: "retention_days", wantValue: 8, otherKey: "max_auto_backup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := testPlan(engineTimescaleDB, "", testPools(testPool("db", 1, 0, 100)))
			plan.AutoBackupConfig = tt.schedule
			cfg, _ := engineConfigOf(context.Background(), plan)
			body, diags := buildDBClusterCreateBody(plan, "Passw0rd!", cfg, testRefs(1), drivers[engineTimescaleDB])
			if diags.HasError() {
				t.Fatalf("build: %v", diags)
			}
			schedule, _ := bodyMap(t, body)["auto_backup_config"].(map[string]any)
			if schedule[tt.wantKey] != tt.wantValue {
				t.Fatalf("%s = %#v, want %v", tt.wantKey, schedule[tt.wantKey], tt.wantValue)
			}
			if _, present := schedule[tt.otherKey]; present {
				t.Fatalf("%s must not be sent: %#v", tt.otherKey, schedule)
			}
		})
	}
}

func TestBuildDBClusterUpdateBody(t *testing.T) {
	t.Parallel()

	state := DBClusterResourceModel{DBClusterModel: DBClusterModel{
		Description: types.StringValue("old"), KeepBackup: types.BoolValue(true),
	}}
	tests := []struct {
		name            string
		description     types.String
		keepBackup      types.Bool
		wantChanged     bool
		wantDescription *string
		wantKeepBackup  *bool
	}{
		{name: "nothing changed", description: types.StringValue("old"), keepBackup: types.BoolValue(true)},
		{name: "omitted attributes plan as unknown", description: types.StringUnknown(), keepBackup: types.BoolUnknown()},
		{name: "description changed", description: types.StringValue("new"), keepBackup: types.BoolValue(true), wantChanged: true, wantDescription: new("new")},
		{name: "description cleared", description: types.StringValue(""), keepBackup: types.BoolValue(true), wantChanged: true, wantDescription: new("")},
		{name: "keep backup disabled", description: types.StringValue("old"), keepBackup: types.BoolValue(false), wantChanged: true, wantKeepBackup: new(false)},
		{name: "both changed", description: types.StringValue("new"), keepBackup: types.BoolValue(false), wantChanged: true, wantDescription: new("new"), wantKeepBackup: new(false)},
		{name: "description outer whitespace only", description: types.StringValue(" old\n"), keepBackup: types.BoolValue(true)},
		{name: "padded description sent trimmed", description: types.StringValue(" new "), keepBackup: types.BoolValue(true), wantChanged: true, wantDescription: new("new")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := state
			plan.Description, plan.KeepBackup = tt.description, tt.keepBackup
			body, changed := buildDBClusterUpdateBody(plan, state)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %t, want %t", changed, tt.wantChanged)
			}
			if !reflect.DeepEqual(body.Description, tt.wantDescription) || !reflect.DeepEqual(body.KeepBackup, tt.wantKeepBackup) {
				t.Fatalf("body = %s, want description=%v keep_backup=%v", jsonBody(t, body), tt.wantDescription, tt.wantKeepBackup)
			}
		})
	}
}

func TestPopulateDBClusterResourceState(t *testing.T) {
	t.Parallel()

	cluster := sampleCluster()
	var state DBClusterResourceModel
	populateDBClusterResourceState(cluster, &state)

	checks := map[string][2]string{
		"id":          {state.ID.ValueString(), testClusterID.String()},
		"name":        {state.Name.ValueString(), "orders"},
		"description": {state.Description.ValueString(), "primary"},
		"engine":      {state.Engine.ValueString(), "mongodb"},
		"topology":    {state.Topology.ValueString(), "mongo_rs"},
		"status":      {state.Status.ValueString(), "active"},
		"operational": {state.OperationalStatus.ValueString(), "noop"},
		"version":     {reference.Get(state.EngineVersion, criterionVersion).ValueString(), "7.0"},
		"region":      {reference.Get(state.Region, reference.Name).ValueString(), "region-1"},
		"subnet":      {reference.Get(state.Subnet, reference.ID).ValueString(), testSubnetID.String()},
		"admin":       {state.AdminUsername.ValueString(), "databaseAdmin"},
		"created":     {state.CreatedAt.ValueString(), "2026-01-01T00:00:00Z"},
		"endpoint":    {state.Endpoint.Elements()["connection_string"].(types.String).ValueString(), "mongodb://host:27017"},
	}
	for name, check := range checks {
		if check[0] != check[1] {
			t.Errorf("%s = %q, want %q", name, check[0], check[1])
		}
	}
	if !state.Zone.IsNull() {
		t.Errorf("a multi-zone cluster must have a null zone, got %s", state.Zone)
	}
	if !state.Plugins.IsNull() {
		t.Errorf("a cluster without plugins must keep plugins null, got %s", state.Plugins)
	}
	if !state.AutoBackupConfig.IsNull() {
		t.Errorf("a cluster without a schedule must have a null auto_backup_config, got %s", state.AutoBackupConfig)
	}
	if day := attributeString(state.MaintenanceConfig, "day_of_week"); day.ValueString() != "sunday" {
		t.Errorf("maintenance day = %s", day)
	}
	if hour := state.MaintenanceConfig.Attributes()["start_hour"].(types.Int64); hour.ValueInt64() != 0 || hour.IsNull() {
		t.Errorf("maintenance hour = %s", hour)
	}
	if !state.KeepBackup.ValueBool() || state.EnableSSL.ValueBool() {
		t.Errorf("flags: keep_backup=%s enable_ssl=%s", state.KeepBackup, state.EnableSSL)
	}
}

func TestPopulateDBClusterResourceStateKeepsConfiguredRepresentation(t *testing.T) {
	t.Parallel()

	state := DBClusterResourceModel{
		DBClusterModel: DBClusterModel{Name: types.StringValue("  orders  ")},
		Region:         reference.Value(reference.Name, types.StringValue("  region-1  ")),
		Subnet:         reference.Value(reference.ID, types.StringValue(strings.ToUpper(testSubnetID.String()))),
		EngineVersion:  reference.Value(criterionVersion, types.StringValue(" 7.0 ")),
		Zone:           reference.Value(reference.Name, types.StringValue(" zone-a ")),
	}
	cluster := sampleCluster()
	cluster.Zone = core.NullableFromPtr(&dbaasclustersdk.NestedZoneSchema{Id: core.UUID{12}, Name: "zone-a"})
	populateDBClusterResourceState(cluster, &state)

	if state.Name.ValueString() != "  orders  " {
		t.Errorf("name = %q", state.Name.ValueString())
	}
	if got := reference.Get(state.Region, reference.Name).ValueString(); got != "  region-1  " {
		t.Errorf("region = %q", got)
	}
	if got := reference.Get(state.Subnet, reference.ID).ValueString(); got != strings.ToUpper(testSubnetID.String()) {
		t.Errorf("subnet = %q", got)
	}
	if got := reference.Get(state.EngineVersion, criterionVersion).ValueString(); got != " 7.0 " {
		t.Errorf("engine version = %q", got)
	}
	if got := reference.Get(state.Zone, reference.Name).ValueString(); got != " zone-a " {
		t.Errorf("zone = %q", got)
	}
}

func TestPopulateDBClusterResourceStateUsesChangedBackendValues(t *testing.T) {
	t.Parallel()

	state := DBClusterResourceModel{
		DBClusterModel: DBClusterModel{Name: types.StringValue("renamed")},
		Region:         reference.Value(reference.Name, types.StringValue("other-region")),
		Subnet:         reference.Value(reference.ID, types.StringValue(core.UUID{99}.String())),
	}
	populateDBClusterResourceState(sampleCluster(), &state)

	if state.Name.ValueString() != "orders" ||
		reference.Get(state.Region, reference.Name).ValueString() != "region-1" ||
		reference.Get(state.Subnet, reference.ID).ValueString() != testSubnetID.String() {
		t.Fatalf("drifted values must follow the backend: %s %s %s", state.Name, state.Region, state.Subnet)
	}
}

func TestPopulateDBClusterResourceStateNil(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(nil, &state)
	if !state.ID.IsNull() {
		t.Fatalf("a nil cluster must leave the state untouched, got id %s", state.ID)
	}
}

func poolDiskSizes(t *testing.T, pools types.List) (names []string, requested, actual []int64) {
	t.Helper()
	for _, element := range pools.Elements() {
		attributes := element.(types.Object).Attributes()
		names = append(names, attributes["name"].(types.String).ValueString())
		requested = append(requested, attributes["disk_size"].(types.Int64).ValueInt64())
		actual = append(actual, attributes["actual_disk_size"].(types.Int64).ValueInt64())
	}
	return names, requested, actual
}

func TestPopulateKeepsRequestedDiskSizeWhenBackendGrowsTheDisk(t *testing.T) {
	t.Parallel()

	grown := 120
	cluster := sampleCluster()
	cluster.NodePools[0].DiskSize = core.NullableFromPtr(&grown)
	state := DBClusterResourceModel{NodePools: testPools(statePool("mongo", 3, 1, 100))}
	populateDBClusterResourceState(cluster, &state)

	_, requested, actual := poolDiskSizes(t, state.NodePools)
	if requested[0] != 100 || actual[0] != 120 {
		t.Fatalf("requested=%d actual=%d, want 100 and 120", requested[0], actual[0])
	}
}

func TestPopulateTakesDiskSizeFromBackendOnImport(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &state)
	_, requested, actual := poolDiskSizes(t, state.NodePools)
	if requested[0] != 100 || actual[0] != 100 {
		t.Fatalf("requested=%d actual=%d, want 100 and 100", requested[0], actual[0])
	}
}

func TestPopulateOrdersPoolsLikeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prior types.List
		want  []string
	}{
		{name: "configuration order", prior: testPools(statePool("data", 2, 1, 50), statePool("manager", 3, 1, 50)), want: []string{"data", "manager"}},
		{name: "backend order without prior state", prior: types.ListNull(types.ObjectType{AttrTypes: nodePoolAttrTypes}), want: []string{"manager", "data"}},
		{name: "pools new to state follow the known ones", prior: testPools(statePool("data", 2, 1, 50)), want: []string{"data", "manager"}},
		{name: "pools gone from the backend are dropped", prior: testPools(statePool("gone", 1, 1, 10), statePool("data", 2, 1, 50)), want: []string{"data", "manager"}},
		// The backend trims outer whitespace from a pool name, and state keeps
		// the configured spelling.
		{name: "configured whitespace is kept", prior: testPools(statePool("  data  ", 2, 1, 50), statePool("manager", 3, 1, 50)), want: []string{"  data  ", "manager"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := DBClusterResourceModel{NodePools: tt.prior}
			populateDBClusterResourceState(sampleOpenSearchCluster(), &state)
			names, _, _ := poolDiskSizes(t, state.NodePools)
			if !reflect.DeepEqual(names, tt.want) {
				t.Fatalf("pool order = %v, want %v", names, tt.want)
			}
		})
	}
}

func TestPopulateMapsOpenSearchPools(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(sampleOpenSearchCluster(), &state)

	pool := state.NodePools.Elements()[1].(types.Object).Attributes()
	if pool["name"].(types.String).ValueString() != "data" || pool["role"].(types.String).ValueString() != "opensearch_node" {
		t.Fatalf("pool = %v", pool)
	}
	roles := pool["engine_roles"].(types.Set)
	if len(roles.Elements()) != 2 {
		t.Fatalf("engine roles = %s", roles)
	}
	if attribute := attributeString(pool["pool_config"].(types.Object), "attribute"); attribute.ValueString() != "hot" {
		t.Fatalf("pool_config.attribute = %s", attribute)
	}
	if !state.EnableSSL.ValueBool() || state.KeepBackup.ValueBool() {
		t.Fatalf("the forced flags must come from the backend: ssl=%s keep=%s", state.EnableSSL, state.KeepBackup)
	}
}

func TestPopulateMapsMemcachedSettingsAndPoolsWithoutStorage(t *testing.T) {
	t.Parallel()

	// A plan from a configuration that omits engine_config and the storage
	// of the pool.
	plan := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
	state := plan
	populateDBClusterResourceState(sampleMemcachedCluster(), &state)

	wantSettings := types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
		"mode":                  types.StringValue("direct"),
		"auth_enabled":          types.BoolValue(false),
		"mcrouter_routing_type": types.StringNull(),
	})
	if !state.EngineConfig.Equal(wantSettings) {
		t.Fatalf("engine_config = %s, want %s", state.EngineConfig, wantSettings)
	}
	pool := state.NodePools.Elements()[0].(types.Object).Attributes()
	if !pool["storage_class"].IsNull() || !pool["disk_size"].IsNull() || !pool["actual_disk_size"].IsNull() {
		t.Fatalf("a pool without storage must keep null storage: %v", pool)
	}
	resourceState := tfsdk.State{Schema: resourceSchemaForTest(t)}
	if diags := resourceState.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("memcached state does not match the schema: %v", diags)
	}
}

func TestPopulateKeepsConfiguredWhitespaceOfNameAndDescription(t *testing.T) {
	t.Parallel()

	state := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	state.Name = types.StringValue("orders ")
	state.Description = types.StringValue("primary\n")
	populateDBClusterResourceState(sampleCluster(), &state)
	if state.Name.ValueString() != "orders " || state.Description.ValueString() != "primary\n" {
		t.Fatalf("name=%q description=%q, want the configured values", state.Name.ValueString(), state.Description.ValueString())
	}

	state.Description = types.StringValue("other")
	populateDBClusterResourceState(sampleCluster(), &state)
	if state.Description.ValueString() != "primary" {
		t.Fatalf("description=%q, want the backend value when it differs", state.Description.ValueString())
	}
}

// TestPopulateKeepsConfiguredWhitespaceOfEnumStrings checks that state after
// create and after the next read keeps the configured spelling of each string
// that the request trims, and takes the backend value when it really differs.
func TestPopulateKeepsConfiguredWhitespaceOfEnumStrings(t *testing.T) {
	t.Parallel()

	paddedSchedule := backupSchedule(true, "weekly", " friday\t")
	tests := []struct {
		name    string
		cluster func() *dbaasclustersdk.DBClusterDetailSchema
		plan    func() DBClusterResourceModel
		check   func(t *testing.T, state DBClusterResourceModel)
	}{
		{
			name: "opensearch pool name, engine roles, and pool tier",
			cluster: func() *dbaasclustersdk.DBClusterDetailSchema {
				cluster := sampleOpenSearchCluster()
				cluster.NodePools = cluster.NodePools[1:]
				return cluster
			},
			plan: func() DBClusterResourceModel {
				pool := paddedPoolAttributes("data", 2, 50)
				pool["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{paddedString("data"), paddedString("ingest")})
				pool["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": paddedString("hot")})
				return testPlan(engineOpenSearch, "", testPools(types.ObjectValueMust(nodePoolAttrTypes, pool)))
			},
			check: func(t *testing.T, state DBClusterResourceModel) {
				t.Helper()
				pool := state.NodePools.Elements()[0].(types.Object).Attributes()
				if !pool["name"].Equal(paddedString("data")) {
					t.Errorf("pool name = %s, want the configured spelling", pool["name"])
				}
				wantRoles := types.SetValueMust(types.StringType, []attr.Value{paddedString("data"), paddedString("ingest")})
				if !pool["engine_roles"].Equal(wantRoles) {
					t.Errorf("engine_roles = %s, want %s", pool["engine_roles"], wantRoles)
				}
				if attribute := attributeString(pool["pool_config"].(types.Object), "attribute"); !attribute.Equal(paddedString("hot")) {
					t.Errorf("pool_config.attribute = %s, want the configured spelling", attribute)
				}
			},
		},
		{
			name: "memcached mode and routing",
			cluster: func() *dbaasclustersdk.DBClusterDetailSchema {
				cluster := sampleMemcachedCluster()
				cluster.EngineConfig = map[string]any{"mode": "mcrouter", "auth_enabled": false, "mcrouter_routing_type": "sharded"}
				return cluster
			},
			plan: func() DBClusterResourceModel {
				plan := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
				plan.EngineConfig = types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
					"mode": paddedString("mcrouter"), "auth_enabled": types.BoolUnknown(), "mcrouter_routing_type": paddedString("sharded"),
				})
				return plan
			},
			check: func(t *testing.T, state DBClusterResourceModel) {
				t.Helper()
				want := types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
					"mode": paddedString("mcrouter"), "auth_enabled": types.BoolValue(false), "mcrouter_routing_type": paddedString("sharded"),
				})
				if !state.EngineConfig.Equal(want) {
					t.Errorf("engine_config = %s, want %s", state.EngineConfig, want)
				}
			},
		},
		{
			name: "maintenance and backup days of week",
			cluster: func() *dbaasclustersdk.DBClusterDetailSchema {
				cluster := sampleCluster()
				cluster.MaintenanceConfig = map[string]any{"day_of_week": "monday", "start_hour": float64(0)}
				cluster.EnableAutoBackup = true
				day, maximum := dbaasclustersdk.DayOfWeekFriday, 7
				cluster.AutoBackupConfig = core.NullableFromPtr(&dbaasclustersdk.DBClusterAutoBackupConfigSchema{
					BackupInterval: dbaasclustersdk.BackupIntervalWeekly, DayOfWeek: core.NullableFromPtr(&day), StartHour: 2, MaxAutoBackup: core.NullableFromPtr(&maximum),
				})
				return cluster
			},
			plan: func() DBClusterResourceModel {
				plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
				plan.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
					"day_of_week": paddedString("monday"), "start_hour": types.Int64Unknown(),
				})
				plan.AutoBackupConfig = paddedSchedule
				return plan
			},
			check: func(t *testing.T, state DBClusterResourceModel) {
				t.Helper()
				if day := attributeString(state.MaintenanceConfig, "day_of_week"); !day.Equal(paddedString("monday")) {
					t.Errorf("maintenance_config.day_of_week = %s, want the configured spelling", day)
				}
				if !state.AutoBackupConfig.Equal(paddedSchedule) {
					t.Errorf("auto_backup_config = %s, want %s", state.AutoBackupConfig, paddedSchedule)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create populates from the plan, and the next read from that state.
			state := tt.plan()
			populateDBClusterResourceState(tt.cluster(), &state)
			tt.check(t, state)
			resourceState := tfsdk.State{Schema: resourceSchemaForTest(t)}
			if diags := resourceState.Set(context.Background(), &state); diags.HasError() {
				t.Fatalf("state does not match the schema: %v", diags)
			}
			populateDBClusterResourceState(tt.cluster(), &state)
			tt.check(t, state)
		})
	}
}

func TestPopulateTakesChangedBackendEnumStrings(t *testing.T) {
	t.Parallel()

	cluster := sampleOpenSearchCluster()
	cluster.NodePools = cluster.NodePools[1:]
	cluster.MaintenanceConfig = map[string]any{"day_of_week": "tuesday", "start_hour": float64(0)}
	pool := paddedPoolAttributes("data", 2, 50)
	pool["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{paddedString("data"), paddedString("ml")})
	pool["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": paddedString("warm")})
	state := testPlan(engineOpenSearch, "", testPools(types.ObjectValueMust(nodePoolAttrTypes, pool)))
	state.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
		"day_of_week": paddedString("monday"), "start_hour": types.Int64Value(0),
	})
	populateDBClusterResourceState(cluster, &state)

	got := state.NodePools.Elements()[0].(types.Object).Attributes()
	wantRoles := types.SetValueMust(types.StringType, []attr.Value{paddedString("data"), types.StringValue("ingest")})
	if !got["engine_roles"].Equal(wantRoles) {
		t.Errorf("engine_roles = %s, want %s", got["engine_roles"], wantRoles)
	}
	if attribute := attributeString(got["pool_config"].(types.Object), "attribute"); attribute.ValueString() != "hot" {
		t.Errorf("pool_config.attribute = %s, want the backend value hot", attribute)
	}
	if day := attributeString(state.MaintenanceConfig, "day_of_week"); day.ValueString() != "tuesday" {
		t.Errorf("maintenance_config.day_of_week = %s, want the backend value tuesday", day)
	}
}

func TestPopulateMapsAnEngineWithoutSettingsToNull(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &state)
	if !state.EngineConfig.IsNull() {
		t.Fatalf("engine_config = %s, want null", state.EngineConfig)
	}
}

func TestPopulateMapsPluginsAndSchedule(t *testing.T) {
	t.Parallel()

	cluster := sampleOpenSearchCluster()
	cluster.Plugins = []dbaasclustersdk.NestedDBClusterPluginSchema{{Id: core.UUID{20}, Name: "analysis-icu"}}
	cluster.EnableAutoBackup = true
	day := dbaasclustersdk.DayOfWeekFriday
	maximum := 14
	cluster.AutoBackupConfig = core.NullableFromPtr(&dbaasclustersdk.DBClusterAutoBackupConfigSchema{
		BackupInterval: dbaasclustersdk.BackupIntervalWeekly, DayOfWeek: core.NullableFromPtr(&day), StartHour: 3, MaxAutoBackup: core.NullableFromPtr(&maximum),
	})
	var state DBClusterResourceModel
	populateDBClusterResourceState(cluster, &state)

	if len(state.Plugins.Elements()) != 1 {
		t.Fatalf("plugins = %s", state.Plugins)
	}
	want := backupSchedule(true, "weekly", "friday")
	attributes := want.Attributes()
	attributes["start_hour"] = types.Int64Value(3)
	attributes["max_auto_backup"] = types.Int64Value(14)
	if !state.AutoBackupConfig.Equal(types.ObjectValueMust(autoBackupAttrTypes, attributes)) {
		t.Fatalf("schedule = %s", state.AutoBackupConfig)
	}
}

func TestPopulateMapsARetentionInDays(t *testing.T) {
	t.Parallel()

	cluster := sampleCluster()
	cluster.Engine = dbaasclustersdk.DBClusterEngineTypeTimescaledb
	cluster.EnableAutoBackup = true
	days := 8
	cluster.AutoBackupConfig = core.NullableFromPtr(&dbaasclustersdk.DBClusterAutoBackupConfigSchema{
		BackupInterval: dbaasclustersdk.BackupIntervalDaily, StartHour: 2, RetentionDays: core.NullableFromPtr(&days),
	})
	var state DBClusterResourceModel
	populateDBClusterResourceState(cluster, &state)
	if want := retentionSchedule(0, 8); !state.AutoBackupConfig.Equal(want) {
		t.Fatalf("schedule = %s, want %s", state.AutoBackupConfig, want)
	}
}

func TestPopulateKeepsConfiguredPluginNames(t *testing.T) {
	t.Parallel()

	pluginSet := func(names ...string) types.Set {
		elements := make([]attr.Value, 0, len(names))
		for _, name := range names {
			elements = append(elements, types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue(name)}))
		}
		return types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, elements)
	}
	tests := []struct {
		name  string
		prior types.Set
		want  types.Set
	}{
		{name: "whitespace only differs", prior: pluginSet(" analysis-icu "), want: pluginSet(" analysis-icu ")},
		{name: "a different plugin", prior: pluginSet("analysis-kuromoji"), want: pluginSet("analysis-icu")},
		{name: "import", prior: types.SetNull(types.ObjectType{AttrTypes: pluginAttrTypes}), want: pluginSet("analysis-icu")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := sampleOpenSearchCluster()
			cluster.Plugins = []dbaasclustersdk.NestedDBClusterPluginSchema{{Id: core.UUID{20}, Name: "analysis-icu"}}
			state := DBClusterResourceModel{Plugins: tt.prior}
			populateDBClusterResourceState(cluster, &state)
			if !state.Plugins.Equal(tt.want) {
				t.Fatalf("plugins = %s, want %s", state.Plugins, tt.want)
			}
		})
	}
}

func TestPopulateKeepsAnExplicitEmptyPluginSet(t *testing.T) {
	t.Parallel()

	empty := types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{})
	state := DBClusterResourceModel{Plugins: empty}
	populateDBClusterResourceState(sampleOpenSearchCluster(), &state)
	if state.Plugins.IsNull() || len(state.Plugins.Elements()) != 0 {
		t.Fatalf("a configured empty set must stay empty, got %s", state.Plugins)
	}
}

func TestStringMapValueEncodesNonStrings(t *testing.T) {
	t.Parallel()

	got := stringMapValue(map[string]any{
		"host": "db.test", "port": float64(27017), "replicas": []any{"a", "b"}, "tls": true,
	}).Elements()
	want := map[string]string{"host": "db.test", "port": "27017", "replicas": `["a","b"]`, "tls": "true"}
	for key, value := range want {
		if got[key].(types.String).ValueString() != value {
			t.Errorf("%s = %s, want %s", key, got[key], value)
		}
	}
	if len(stringMapValue(nil).Elements()) != 0 {
		t.Error("a nil map must map to an empty map")
	}
}

func TestMaintenanceValueToleratesMissingFields(t *testing.T) {
	t.Parallel()

	empty := maintenanceValue(nil)
	if !attributeString(empty, "day_of_week").IsNull() || !empty.Attributes()["start_hour"].(types.Int64).IsNull() {
		t.Fatalf("missing fields must be null, got %s", empty)
	}
}

// runStringModifiers applies the plan modifiers of a string attribute the way
// the Framework does for an update of an existing resource.
func runStringModifiers(t *testing.T, attribute schema.StringAttribute, state, plan, config types.String) (types.String, bool) {
	t.Helper()

	resourceSchema := resourceSchemaForTest(t)
	raw := nullRawForTest(t, resourceSchema)
	request := planmodifier.StringRequest{
		State: tfsdk.State{Schema: resourceSchema, Raw: raw}, Plan: tfsdk.Plan{Schema: resourceSchema, Raw: raw},
		StateValue: state, PlanValue: plan, ConfigValue: config,
	}
	replace := false
	for _, modifier := range attribute.PlanModifiers {
		response := planmodifier.StringResponse{PlanValue: request.PlanValue}
		modifier.PlanModifyString(context.Background(), request, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("diagnostics: %v", response.Diagnostics)
		}
		request.PlanValue = response.PlanValue
		replace = replace || response.RequiresReplace
	}
	return request.PlanValue, replace
}

func TestAttributeReplacementSemantics(t *testing.T) {
	t.Parallel()

	attributes := resourceSchemaForTest(t).Attributes
	known := func(value string) types.String { return types.StringValue(value) }

	t.Run("name replaces on change", func(t *testing.T) {
		t.Parallel()
		_, replace := runStringModifiers(t, attributes["name"].(schema.StringAttribute), known("a"), known("b"), known("b"))
		if !replace {
			t.Fatal("changing name must replace the cluster")
		}
	})
	t.Run("name keeps the cluster when unchanged", func(t *testing.T) {
		t.Parallel()
		_, replace := runStringModifiers(t, attributes["name"].(schema.StringAttribute), known("a"), known("a"), known("a"))
		if replace {
			t.Fatal("an unchanged name must not replace the cluster")
		}
	})
	t.Run("description updates in place", func(t *testing.T) {
		t.Parallel()
		_, replace := runStringModifiers(t, attributes["description"].(schema.StringAttribute), known("a"), known("b"), known("b"))
		if replace {
			t.Fatal("changing description must update in place")
		}
	})
	t.Run("description omitted keeps the state value", func(t *testing.T) {
		t.Parallel()
		planned, replace := runStringModifiers(t, attributes["description"].(schema.StringAttribute), known("a"), types.StringUnknown(), types.StringNull())
		if replace || planned.ValueString() != "a" {
			t.Fatalf("planned=%s replace=%t", planned, replace)
		}
	})
	t.Run("topology configured change replaces", func(t *testing.T) {
		t.Parallel()
		_, replace := runStringModifiers(t, attributes["topology"].(schema.StringAttribute), known("mongo_rs"), known("mongo_sharding"), known("mongo_sharding"))
		if !replace {
			t.Fatal("changing a configured topology must replace the cluster")
		}
	})
	t.Run("topology omitted keeps the state value", func(t *testing.T) {
		t.Parallel()
		planned, replace := runStringModifiers(t, attributes["topology"].(schema.StringAttribute), known("opensearch"), types.StringUnknown(), types.StringNull())
		if replace || planned.ValueString() != "opensearch" {
			t.Fatalf("planned=%s replace=%t", planned, replace)
		}
	})
	t.Run("topology configured from an unknown value replaces", func(t *testing.T) {
		t.Parallel()
		// A configured value that this apply computes cannot be the value in
		// state, so the Framework plans a replacement.
		_, replace := runStringModifiers(t, attributes["topology"].(schema.StringAttribute), known("mongo_rs"), types.StringUnknown(), types.StringUnknown())
		if !replace {
			t.Fatal("a configured unknown value must replace the cluster")
		}
	})
}

// TestSpellingChangesKeepTheCluster checks that a change of outer whitespace
// alone in an immutable string updates state in place. The request trims each
// value and state keeps the configured spelling, so the cluster is the same.
func TestSpellingChangesKeepTheCluster(t *testing.T) {
	t.Parallel()

	attributes := resourceSchemaForTest(t).Attributes
	nested := func(object, name string) schema.StringAttribute {
		return attributes[object].(schema.SingleNestedAttribute).Attributes[name].(schema.StringAttribute)
	}
	values := map[string]struct {
		attribute    schema.StringAttribute
		state, other string
	}{
		"name":                                {attributes["name"].(schema.StringAttribute), "orders", "payments"},
		"engine":                              {attributes["engine"].(schema.StringAttribute), engineMongoDB, engineOpenSearch},
		"topology":                            {attributes["topology"].(schema.StringAttribute), topologyMongoRs, topologyMongoSharding},
		"maintenance_config.day_of_week":      {nested("maintenance_config", "day_of_week"), "sunday", "monday"},
		"engine_config.mode":                  {nested("engine_config", "mode"), "mcrouter", "direct"},
		"engine_config.mcrouter_routing_type": {nested("engine_config", "mcrouter_routing_type"), "sharded", "replicated"},
	}
	for name, tt := range values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := types.StringValue(tt.state)
			if _, replace := runStringModifiers(t, tt.attribute, state, paddedString(tt.state), paddedString(tt.state)); replace {
				t.Error("a change of outer whitespace must keep the cluster")
			}
			if _, replace := runStringModifiers(t, tt.attribute, state, types.StringValue(tt.other), types.StringValue(tt.other)); !replace {
				t.Error("another value must replace the cluster")
			}
		})
	}

	resourceSchema := resourceSchemaForTest(t)
	raw := nullRawForTest(t, resourceSchema)
	runObject := func(t *testing.T, attribute schema.SingleNestedAttribute, state, plan types.Object) bool {
		t.Helper()
		request := planmodifier.ObjectRequest{
			State: tfsdk.State{Schema: resourceSchema, Raw: raw}, Plan: tfsdk.Plan{Schema: resourceSchema, Raw: raw},
			StateValue: state, PlanValue: plan, ConfigValue: plan,
		}
		replace := false
		for _, modifier := range attribute.PlanModifiers {
			response := planmodifier.ObjectResponse{PlanValue: request.PlanValue}
			modifier.PlanModifyObject(context.Background(), request, &response)
			request.PlanValue = response.PlanValue
			replace = replace || response.RequiresReplace
		}
		return replace
	}
	references := map[string]struct {
		criterion    string
		state, other string
		padded       string
	}{
		"engine_version": {criterionVersion, "7.0", "8.0", " 7.0\t"},
		"region":         {reference.Name, "region-1", "region-2", " region-1\t"},
		"zone":           {reference.Name, "zone-a", "zone-b", " zone-a\t"},
		"subnet":         {reference.ID, testSubnetID.String(), testRegionID.String(), "  " + strings.ToUpper(testSubnetID.String()) + "\t"},
	}
	for name, tt := range references {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			attribute := attributes[name].(schema.SingleNestedAttribute)
			state := reference.Value(tt.criterion, types.StringValue(tt.state))
			if runObject(t, attribute, state, reference.Value(tt.criterion, types.StringValue(tt.padded))) {
				t.Error("another spelling of the same reference must keep the cluster")
			}
			if !runObject(t, attribute, state, reference.Value(tt.criterion, types.StringValue(tt.other))) {
				t.Error("another reference must replace the cluster")
			}
		})
	}
	t.Run("auto_backup_config", func(t *testing.T) {
		t.Parallel()
		attribute := attributes["auto_backup_config"].(schema.SingleNestedAttribute)
		state := backupSchedule(true, "weekly", "monday")
		if runObject(t, attribute, state, backupSchedule(true, " weekly\t", " monday ")) {
			t.Error("a change of outer whitespace in the schedule must keep the cluster")
		}
		if !runObject(t, attribute, state, backupSchedule(true, "weekly", "friday")) {
			t.Error("another day must replace the cluster")
		}
		if !runObject(t, attribute, state, backupSchedule(false, "weekly", "monday")) {
			t.Error("disabling the schedule must replace the cluster")
		}
	})

	runSet := func(t *testing.T, attribute schema.Attribute, state, plan, config types.Set) bool {
		t.Helper()
		var modifiers []planmodifier.Set
		switch typed := attribute.(type) {
		case schema.SetAttribute:
			modifiers = typed.PlanModifiers
		case schema.SetNestedAttribute:
			modifiers = typed.PlanModifiers
		}
		request := planmodifier.SetRequest{
			State: tfsdk.State{Schema: resourceSchema, Raw: raw}, Plan: tfsdk.Plan{Schema: resourceSchema, Raw: raw},
			StateValue: state, PlanValue: plan, ConfigValue: config,
		}
		replace := false
		for _, modifier := range modifiers {
			response := planmodifier.SetResponse{PlanValue: request.PlanValue}
			modifier.PlanModifySet(context.Background(), request, &response)
			request.PlanValue = response.PlanValue
			replace = replace || response.RequiresReplace
		}
		return replace
	}
	t.Run("plugins", func(t *testing.T) {
		t.Parallel()
		pluginType := types.ObjectType{AttrTypes: pluginAttrTypes}
		plugins := func(names ...string) types.Set {
			elements := make([]attr.Value, 0, len(names))
			for _, name := range names {
				elements = append(elements, types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue(name)}))
			}
			return types.SetValueMust(pluginType, elements)
		}
		state := plugins("analysis-icu", "analysis-kuromoji")
		for name, tt := range map[string]struct {
			plan types.Set
			want bool
		}{
			"same plugins":          {plan: plugins("analysis-icu", "analysis-kuromoji")},
			"padded names":          {plan: plugins(" analysis-icu ", "analysis-kuromoji\n")},
			"another plugin":        {plan: plugins("analysis-icu", "analysis-nori"), want: true},
			"a plugin removed":      {plan: plugins("analysis-icu"), want: true},
			"a plugin added":        {plan: plugins("analysis-icu", "analysis-kuromoji", "analysis-nori"), want: true},
			"every plugin removed":  {plan: types.SetNull(pluginType), want: true},
			"plugins not yet known": {plan: types.SetUnknown(pluginType), want: true},
		} {
			if got := runSet(t, attributes["plugins"], state, tt.plan, tt.plan); got != tt.want {
				t.Errorf("%s: replace = %t, want %t", name, got, tt.want)
			}
		}
	})
	t.Run("allowed_cidrs", func(t *testing.T) {
		t.Parallel()
		state := stringSetValue([]string{"10.0.0.0/24", "10.1.0.5/32"})
		for name, tt := range map[string]struct {
			plan, config types.Set
			want         bool
		}{
			"padded blocks":   {plan: stringSetValue([]string{" 10.0.0.0/24", "10.1.0.5/32\t"})},
			"another block":   {plan: stringSetValue([]string{"10.0.0.0/24", "10.2.0.5/32"}), want: true},
			"omitted":         {plan: types.SetUnknown(types.StringType), config: types.SetNull(types.StringType)},
			"explicitly none": {plan: stringSetValue(nil), want: true},
		} {
			config := tt.config
			if config.IsNull() && !tt.plan.IsUnknown() {
				config = tt.plan
			}
			if got := runSet(t, attributes["allowed_cidrs"], state, tt.plan, config); got != tt.want {
				t.Errorf("%s: replace = %t, want %t", name, got, tt.want)
			}
		}
	})
}

// fakeBackend answers requests by method and path, and records them in order.
type fakeBackend struct {
	t        *testing.T
	handlers map[string]roundTripFunc
	requests []string
}

func newFakeBackend(t *testing.T) *fakeBackend {
	t.Helper()
	return &fakeBackend{t: t, handlers: map[string]roundTripFunc{}}
}

func (b *fakeBackend) on(method, path string, handler roundTripFunc) {
	b.handlers[method+" "+path] = handler
}

// respond registers a handler that always answers with status and body.
func (b *fakeBackend) respond(method, path string, status int, body string) {
	b.on(method, path, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, status, body), nil
	})
}

func (b *fakeBackend) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Path
	b.requests = append(b.requests, key)
	handler, ok := b.handlers[key]
	if !ok {
		b.t.Errorf("unexpected request %s", key)
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	}
	return handler(req)
}

const (
	clustersPath = "/v2/dbaas/db-clusters/"
	regionsPath  = "/v2/projects/09000000-0000-0000-0000-000000000000/regions/"
)

func clusterPath() string { return clustersPath + testClusterID.String() + "/" }

func requestBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	return decoded
}

// newTestResource wires a resource to the fake backend.
func newTestResource(t *testing.T, backend *fakeBackend) *DBClusterResource {
	t.Helper()
	client := testDBaaSClient(t, backend.RoundTrip)
	project := testProjectClient(t, backend.RoundTrip)
	return &DBClusterResource{client: client, project: project, projectID: testProjectID}
}

// registerCatalog answers the lookups that Create runs.
func registerCatalog(t *testing.T, backend *fakeBackend) {
	t.Helper()
	backend.respond(http.MethodGet, regionsPath, http.StatusOK, pageBody(t, map[string]any{
		"region": map[string]any{"id": testRegionID.String(), "name": "region-1", "description": ""}, "services": []any{},
	}))
	backend.respond(http.MethodGet, "/v2/dbaas/engine-versions/", http.StatusOK, pageBody(t, map[string]any{
		"id": testVersionID.String(), "engine": "mongodb", "version": "7.0", "is_active": true, "default_admin_username": "databaseAdmin",
	}))
	backend.respond(http.MethodGet, "/v2/dbaas/resource-packages/", http.StatusOK, pageBody(t, map[string]any{
		"id": testPackageID.String(), "name": "2c4g", "vcpus": 2, "ram": 4, "description": "",
	}))
	backend.respond(http.MethodGet, "/v2/dbaas/storage-classes/", http.StatusOK, pageBody(t, map[string]any{
		"id": testClassID.String(), "name": "ssd", "disk_type": "ssd", "speed": "10k", "encrypted": false, "description": "",
	}))
}

func clusterWithStatus(status dbaasclustersdk.DBClusterStatus, operation dbaasclustersdk.DBClusterOperationalStatus) *dbaasclustersdk.DBClusterDetailSchema {
	cluster := sampleCluster()
	cluster.Status, cluster.OperationalStatus = status, operation
	return cluster
}

func nullStateFor(t *testing.T, resourceSchema schema.Schema) tfsdk.State {
	t.Helper()
	objectType, ok := resourceSchema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("resource schema type is not an object")
	}
	return tfsdk.State{Schema: resourceSchema, Raw: tftypes.NewValue(objectType, nil)}
}

func stateFromModel(t *testing.T, model DBClusterResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchemaForTest(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

// createRequest returns the request Terraform sends for plan, with password in
// the config only, as for a write-only attribute.
func createRequest(t *testing.T, plan DBClusterResourceModel, password string) resource.CreateRequest {
	t.Helper()
	resourceSchema := resourceSchemaForTest(t)
	planned := tfsdk.Plan{Schema: resourceSchema}
	if diags := planned.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	configState := stateFromModel(t, plan)
	if diags := configState.SetAttribute(context.Background(), path.Root("root_password"), types.StringValue(password)); diags.HasError() {
		t.Fatalf("set password: %v", diags)
	}
	return resource.CreateRequest{Plan: planned, Config: tfsdk.Config{Schema: resourceSchema, Raw: configState.Raw}}
}

func TestDBClusterCreateSendsTheWriteOnlyPasswordAndWaitsUntilActive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		registerCatalog(t, backend)
		var created map[string]any
		backend.on(http.MethodPost, clustersPath, func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("Project-ID"); got != testProjectID.String() {
				t.Errorf("Project-ID = %q", got)
			}
			created = requestBody(t, req)
			return testHTTPResponse(req, http.StatusCreated, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusCreating, dbaasclustersdk.DBClusterOperationalStatusNoop))), nil
		})
		polls := 0
		backend.on(http.MethodGet, clusterPath(), func(req *http.Request) (*http.Response, error) {
			polls++
			cluster := clusterWithStatus(dbaasclustersdk.DBClusterStatusProvisioning, dbaasclustersdk.DBClusterOperationalStatusNoop)
			if polls == 2 {
				// Active but still running an operation: not ready yet.
				cluster.Status = dbaasclustersdk.DBClusterStatusActive
				cluster.OperationalStatus = dbaasclustersdk.DBClusterOperationalStatusScaling
			}
			if polls >= 3 {
				cluster.Status = dbaasclustersdk.DBClusterStatusActive
			}
			return testHTTPResponse(req, http.StatusOK, jsonBody(t, cluster)), nil
		})

		r := newTestResource(t, backend)
		plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
		resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
		start := time.Now()
		r.Create(context.Background(), createRequest(t, plan, "Passw0rd!"), &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}

		if created["root_password"] != "Passw0rd!" {
			t.Errorf("the password from the config was not sent: %#v", created["root_password"])
		}
		if polls != 3 || time.Since(start) != 2*dbClusterPollInterval {
			t.Errorf("polls=%d elapsed=%s, want 3 polls and %s", polls, time.Since(start), 2*dbClusterPollInterval)
		}
		var got DBClusterResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("read state: %v", diags)
		}
		if got.ID.ValueString() != testClusterID.String() || got.Status.ValueString() != "active" || got.OperationalStatus.ValueString() != "noop" {
			t.Errorf("state id=%s status=%s operation=%s", got.ID, got.Status, got.OperationalStatus)
		}
		if !got.RootPassword.IsNull() {
			t.Errorf("a write-only password must not be stored, got %s", got.RootPassword)
		}
	})
}

// TestDBClusterCreateTrimsUserStringsAndKeepsConfiguredSpelling checks that
// create sends the padded strings trimmed, and that state after create and
// after the next read keeps the configured spelling.
func TestDBClusterCreateTrimsUserStringsAndKeepsConfiguredSpelling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		registerCatalog(t, backend)
		cluster := sampleCluster()
		cluster.Description = "primary"
		cluster.MaintenanceConfig = map[string]any{"day_of_week": "monday", "start_hour": float64(0)}
		cluster.EnableAutoBackup = true
		day, maximum := dbaasclustersdk.DayOfWeekFriday, 7
		cluster.AutoBackupConfig = core.NullableFromPtr(&dbaasclustersdk.DBClusterAutoBackupConfigSchema{
			BackupInterval: dbaasclustersdk.BackupIntervalWeekly, DayOfWeek: core.NullableFromPtr(&day), StartHour: 2, MaxAutoBackup: core.NullableFromPtr(&maximum),
		})
		cluster.AllowedCidrs = []string{"10.0.0.0/24"}
		var created map[string]any
		backend.on(http.MethodPost, clustersPath, func(req *http.Request) (*http.Response, error) {
			created = requestBody(t, req)
			return testHTTPResponse(req, http.StatusCreated, jsonBody(t, cluster)), nil
		})
		backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, cluster))
		var lookups []string
		for _, lookupPath := range []string{regionsPath, "/v2/dbaas/resource-packages/", "/v2/dbaas/storage-classes/"} {
			handler := backend.handlers[http.MethodGet+" "+lookupPath]
			backend.on(http.MethodGet, lookupPath, func(req *http.Request) (*http.Response, error) {
				lookups = append(lookups, req.URL.Query().Get("name"))
				return handler(req)
			})
		}

		var engineVersionEngines []string
		engineVersionHandler := backend.handlers[http.MethodGet+" /v2/dbaas/engine-versions/"]
		backend.on(http.MethodGet, "/v2/dbaas/engine-versions/", func(req *http.Request) (*http.Response, error) {
			engineVersionEngines = append(engineVersionEngines, req.URL.Query().Get("engine"))
			return engineVersionHandler(req)
		})

		r := newTestResource(t, backend)
		plannedPool := paddedPoolAttributes("mongo", 3, 100)
		plannedPool["resource_config"] = reference.Value(reference.Name, paddedString("2c4g"))
		plannedPool["storage_class"] = reference.Value(reference.Name, paddedString("ssd"))
		plan := testPlan(engineMongoDB, topologyMongoRs, testPools(types.ObjectValueMust(nodePoolAttrTypes, plannedPool)))
		plan.Name = paddedString("orders")
		plan.Engine = paddedString(engineMongoDB)
		plan.Topology = paddedString(topologyMongoRs)
		plan.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{paddedString("10.0.0.0/24")})
		plan.EngineVersion = reference.Value(criterionVersion, paddedString("7.0"))
		plan.Region = reference.Value(reference.Name, paddedString("region-1"))
		plan.Subnet = reference.Value(reference.ID, paddedString(testSubnetID.String()))
		plan.Description = paddedString("primary")
		plan.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"team": paddedString("data")})
		plan.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
			"day_of_week": paddedString("monday"), "start_hour": types.Int64Unknown(),
		})
		plan.AutoBackupConfig = backupSchedule(true, " weekly\t", " friday\t")
		const paddedPassword = " Passw0rd! "
		resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
		r.Create(context.Background(), createRequest(t, plan, paddedPassword), &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", resp.Diagnostics)
		}

		pools, _ := created["node_pools"].([]any)
		pool, _ := pools[0].(map[string]any)
		schedule, _ := created["auto_backup_config"].(map[string]any)
		if created["description"] != "primary" || pool["name"] != "mongo" || schedule["day_of_week"] != "friday" ||
			!reflect.DeepEqual(created["maintenance_config"], map[string]any{"day_of_week": "monday"}) {
			t.Errorf("create body description=%#v pool name=%#v maintenance=%#v schedule=%#v, want trimmed values",
				created["description"], pool["name"], created["maintenance_config"], schedule)
		}
		if created["name"] != "orders" || created["engine"] != engineMongoDB || created["topology"] != topologyMongoRs ||
			schedule["backup_interval"] != "weekly" || created["subnet_id"] != testSubnetID.String() ||
			!reflect.DeepEqual(created["allowed_cidrs"], []any{"10.0.0.0/24"}) {
			t.Errorf("create body name=%#v engine=%#v topology=%#v interval=%#v subnet=%#v cidrs=%#v, want trimmed values",
				created["name"], created["engine"], created["topology"], schedule["backup_interval"], created["subnet_id"], created["allowed_cidrs"])
		}
		if want := []string{"region-1", "2c4g", "ssd"}; !reflect.DeepEqual(lookups, want) {
			t.Errorf("lookup names = %q, want %q", lookups, want)
		}
		if want := []string{engineMongoDB}; !reflect.DeepEqual(engineVersionEngines, want) {
			t.Errorf("engine version lookup engines = %q, want %q", engineVersionEngines, want)
		}
		// root_password and metadata values keep their whitespace.
		if created["root_password"] != paddedPassword || !reflect.DeepEqual(created["metadata"], map[string]any{"team": " data\t"}) {
			t.Errorf("create body root_password=%#v metadata=%#v, want the values as configured", created["root_password"], created["metadata"])
		}

		check := func(stage string, state tfsdk.State) {
			t.Helper()
			var got DBClusterResourceModel
			if diags := state.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("%s state: %v", stage, diags)
			}
			gotPool := got.NodePools.Elements()[0].(types.Object).Attributes()
			if !got.Description.Equal(paddedString("primary")) || !gotPool["name"].Equal(paddedString("mongo")) ||
				!attributeString(got.MaintenanceConfig, "day_of_week").Equal(paddedString("monday")) ||
				!got.AutoBackupConfig.Equal(plan.AutoBackupConfig) {
				t.Errorf("%s state description=%s pool name=%s maintenance=%s schedule=%s, want the configured spelling",
					stage, got.Description, gotPool["name"], got.MaintenanceConfig, got.AutoBackupConfig)
			}
			if !got.Name.Equal(plan.Name) || !got.Engine.Equal(plan.Engine) || !got.Topology.Equal(plan.Topology) ||
				!got.AllowedCIDRs.Equal(plan.AllowedCIDRs) {
				t.Errorf("%s state name=%s engine=%s topology=%s cidrs=%s, want the configured spelling",
					stage, got.Name, got.Engine, got.Topology, got.AllowedCIDRs)
			}
			for name, pair := range map[string][2]types.Object{
				"engine_version": {got.EngineVersion, plan.EngineVersion}, "region": {got.Region, plan.Region}, "subnet": {got.Subnet, plan.Subnet},
				"resource_config": {gotPool["resource_config"].(types.Object), plannedPool["resource_config"].(types.Object)},
				"storage_class":   {gotPool["storage_class"].(types.Object), plannedPool["storage_class"].(types.Object)},
			} {
				if !pair[0].Equal(pair[1]) {
					t.Errorf("%s state %s = %s, want the configured spelling %s", stage, name, pair[0], pair[1])
				}
			}
		}
		check("create", resp.State)

		readResp := resource.ReadResponse{State: resp.State}
		r.Read(context.Background(), resource.ReadRequest{State: resp.State}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
		}
		check("read", readResp.State)
	})
}

func TestDBClusterCreateKeepsTheIDWhenProvisioningFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		registerCatalog(t, backend)
		backend.respond(http.MethodPost, clustersPath, http.StatusCreated, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusCreating, dbaasclustersdk.DBClusterOperationalStatusNoop)))
		failed := clusterWithStatus(dbaasclustersdk.DBClusterStatusError, dbaasclustersdk.DBClusterOperationalStatusNoop)
		failed.ErrorMessage = "out of capacity"
		backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, failed))

		r := newTestResource(t, backend)
		plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
		resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
		start := time.Now()
		r.Create(context.Background(), createRequest(t, plan, "Passw0rd!"), &resp)

		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "out of capacity") {
			t.Fatalf("expected the backend error message, got %v", resp.Diagnostics)
		}
		if time.Since(start) != 0 {
			t.Errorf("a terminal status must stop the wait at once, waited %s", time.Since(start))
		}
		var id types.String
		if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &id); diags.HasError() || id.ValueString() != testClusterID.String() {
			t.Fatalf("the cluster ID must stay in state so Terraform can delete it: id=%s diags=%v", id, diags)
		}
	})
}

func TestDBClusterCreateSurfacesBackendValidationErrors(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	registerCatalog(t, backend)
	backend.respond(http.MethodPost, clustersPath, http.StatusBadRequest,
		`{"errors":[{"code":"unique","location":"name","message":"A DB cluster with this name already exists for this database type."}],"request_id":"r1"}`)

	r := newTestResource(t, backend)
	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
	r.Create(context.Background(), createRequest(t, plan, "Passw0rd!"), &resp)

	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "already exists") {
		t.Fatalf("expected the backend message, got %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("a rejected create must leave no state")
	}
	if last := backend.requests[len(backend.requests)-1]; last != "POST "+clustersPath {
		t.Fatalf("a rejected create must not poll, last request %s", last)
	}
}

func TestDBClusterCreateRejectsAnEmptyPassword(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	r := newTestResource(t, backend)
	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
	r.Create(context.Background(), createRequest(t, plan, ""), &resp)
	if !resp.Diagnostics.HasError() || len(backend.requests) != 0 {
		t.Fatalf("an empty password must fail before any request: diags=%v requests=%v", resp.Diagnostics, backend.requests)
	}
}

// ValidateConfig skips the engine rules while engine is unknown, so Create
// must apply them before it sends a request.
func TestDBClusterCreateAppliesTheEngineRules(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	r := newTestResource(t, backend)
	plan := testPlan(engineOpenSearch, "", testPools(testPool("data", 3, 0, 100)))
	plan.EnableSSL = types.BoolValue(false)
	resp := resource.CreateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
	r.Create(context.Background(), createRequest(t, plan, "Passw0rd!"), &resp)
	if !resp.Diagnostics.HasError() || len(backend.requests) != 0 {
		t.Fatalf("a value the engine forbids must fail before any request: diags=%v requests=%v", resp.Diagnostics, backend.requests)
	}
}

func readRequest(t *testing.T, model DBClusterResourceModel) (resource.ReadRequest, *resource.ReadResponse) {
	t.Helper()
	state := stateFromModel(t, model)
	return resource.ReadRequest{State: state}, &resource.ReadResponse{State: state}
}

func TestDBClusterReadRemovesAMissingClusterFromState(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusNotFound, `{}`)
	r := newTestResource(t, backend)

	var model DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &model)
	req, resp := readRequest(t, model)
	r.Read(context.Background(), req, resp)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Fatalf("a missing cluster must leave state: diags=%v", resp.Diagnostics)
	}
}

func TestDBClusterReadKeepsStateOnOtherErrors(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusInternalServerError, `{}`)
	r := newTestResource(t, backend)

	var model DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &model)
	req, resp := readRequest(t, model)
	r.Read(context.Background(), req, resp)
	if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
		t.Fatalf("a server error must report a diagnostic and keep state: diags=%v", resp.Diagnostics)
	}
}

func TestDBClusterImportThenReadFillsTheState(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleOpenSearchCluster()))
	r := newTestResource(t, backend)

	importResp := resource.ImportStateResponse{State: nullStateFor(t, resourceSchemaForTest(t))}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testClusterID.String()}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("import: %v", importResp.Diagnostics)
	}
	readResp := resource.ReadResponse{State: importResp.State}
	r.Read(context.Background(), resource.ReadRequest{State: importResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read: %v", readResp.Diagnostics)
	}

	var got DBClusterResourceModel
	if diags := readResp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	names, _, _ := poolDiskSizes(t, got.NodePools)
	if got.Engine.ValueString() != "opensearch" || !reflect.DeepEqual(names, []string{"manager", "data"}) || !got.RootPassword.IsNull() {
		t.Fatalf("imported state: engine=%s pools=%v password=%s", got.Engine, names, got.RootPassword)
	}
}

func updateRequest(t *testing.T, plan, state DBClusterResourceModel) (resource.UpdateRequest, *resource.UpdateResponse) {
	t.Helper()
	planned := tfsdk.Plan{Schema: resourceSchemaForTest(t)}
	if diags := planned.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	prior := stateFromModel(t, state)
	return resource.UpdateRequest{Plan: planned, State: prior}, &resource.UpdateResponse{State: prior}
}

// updatePlan returns the plan for state with the description and keep_backup
// changed and every other Computed attribute unknown, as Terraform plans it.
func updatePlan(state DBClusterResourceModel, description types.String, keepBackup types.Bool) DBClusterResourceModel {
	plan := state
	plan.Description, plan.KeepBackup = description, keepBackup
	plan.Status, plan.OperationalStatus = types.StringUnknown(), types.StringUnknown()
	plan.UpdatedAt, plan.ErrorMessage, plan.Endpoint = types.StringUnknown(), types.StringUnknown(), types.MapUnknown(types.StringType)
	return plan
}

func TestDBClusterUpdatePatchesOnlyChangedAttributes(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &state)

	tests := []struct {
		name        string
		description types.String
		keepBackup  types.Bool
		wantPatch   map[string]any
	}{
		{name: "description", description: types.StringValue("new"), keepBackup: state.KeepBackup, wantPatch: map[string]any{"description": "new"}},
		{name: "description cleared", description: types.StringValue(""), keepBackup: state.KeepBackup, wantPatch: map[string]any{"description": ""}},
		{name: "keep backup false", description: state.Description, keepBackup: types.BoolValue(false), wantPatch: map[string]any{"keep_backup": false}},
		{name: "nothing changed", description: state.Description, keepBackup: state.KeepBackup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			updated := sampleCluster()
			if description, ok := tt.wantPatch["description"].(string); ok {
				updated.Description = description
			}
			if keepBackup, ok := tt.wantPatch["keep_backup"].(bool); ok {
				updated.KeepBackup = keepBackup
			}
			backend := newFakeBackend(t)
			var patch map[string]any
			backend.on(http.MethodPatch, clusterPath(), func(req *http.Request) (*http.Response, error) {
				patch = requestBody(t, req)
				return testHTTPResponse(req, http.StatusOK, jsonBody(t, updated)), nil
			})
			backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, updated))
			r := newTestResource(t, backend)

			req, resp := updateRequest(t, updatePlan(state, tt.description, tt.keepBackup), state)
			r.Update(context.Background(), req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("update: %v", resp.Diagnostics)
			}

			if !reflect.DeepEqual(patch, tt.wantPatch) {
				t.Errorf("PATCH body = %#v, want %#v", patch, tt.wantPatch)
			}
			var got DBClusterResourceModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("state: %v", diags)
			}
			if got.ID.ValueString() != testClusterID.String() || got.Description.ValueString() != updated.Description || got.KeepBackup.ValueBool() != updated.KeepBackup {
				t.Errorf("state id=%s description=%q keep_backup=%t", got.ID, got.Description.ValueString(), got.KeepBackup.ValueBool())
			}
			if got.Status.IsUnknown() || got.Endpoint.IsUnknown() {
				t.Error("an update must leave no unknown value in state")
			}
		})
	}
}

func TestDBClusterUpdateReportsBackendErrors(t *testing.T) {
	t.Parallel()

	var state DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &state)
	backend := newFakeBackend(t)
	backend.respond(http.MethodPatch, clusterPath(), http.StatusBadRequest, `{"errors":[{"code":"invalid","location":"description","message":"too long"}]}`)
	r := newTestResource(t, backend)

	req, resp := updateRequest(t, updatePlan(state, types.StringValue("new"), state.KeepBackup), state)
	r.Update(context.Background(), req, resp)
	if !resp.Diagnostics.HasError() || len(backend.requests) != 1 {
		t.Fatalf("a rejected PATCH must stop the update: diags=%v requests=%v", resp.Diagnostics, backend.requests)
	}
}

func deleteRequest(t *testing.T) resource.DeleteRequest {
	t.Helper()
	var model DBClusterResourceModel
	populateDBClusterResourceState(sampleCluster(), &model)
	return resource.DeleteRequest{State: stateFromModel(t, model)}
}

func TestDBClusterDeleteWaitsUntilTheClusterIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		backend.respond(http.MethodDelete, clusterPath(), http.StatusNoContent, "")
		polls := 0
		backend.on(http.MethodGet, clusterPath(), func(req *http.Request) (*http.Response, error) {
			polls++
			if polls == 3 {
				return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
			}
			cluster := clusterWithStatus(dbaasclustersdk.DBClusterStatusDeleting, dbaasclustersdk.DBClusterOperationalStatusDeleting)
			return testHTTPResponse(req, http.StatusOK, jsonBody(t, cluster)), nil
		})
		r := newTestResource(t, backend)

		resp := resource.DeleteResponse{}
		start := time.Now()
		r.Delete(context.Background(), deleteRequest(t), &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("delete: %v", resp.Diagnostics)
		}
		if polls != 3 || time.Since(start) != 2*dbClusterPollInterval {
			t.Fatalf("polls=%d elapsed=%s", polls, time.Since(start))
		}
	})
}

func TestDBClusterDeleteSucceedsWhenTheClusterIsAlreadyGone(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodDelete, clusterPath(), http.StatusNotFound, `{}`)
	r := newTestResource(t, backend)
	resp := resource.DeleteResponse{}
	r.Delete(context.Background(), deleteRequest(t), &resp)
	if resp.Diagnostics.HasError() || len(backend.requests) != 1 {
		t.Fatalf("a missing cluster is already deleted: diags=%v requests=%v", resp.Diagnostics, backend.requests)
	}
}

func TestDBClusterDeleteReportsARefusalWithoutWaiting(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodDelete, clusterPath(), http.StatusBadRequest,
		`{"errors":[{"code":"invalid","location":"","message":"DB cluster cannot be deleted while a backup is being created."}]}`)
	r := newTestResource(t, backend)
	resp := resource.DeleteResponse{}
	r.Delete(context.Background(), deleteRequest(t), &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "backup is being created") || len(backend.requests) != 1 {
		t.Fatalf("expected the backend refusal and no polling: diags=%v requests=%v", resp.Diagnostics, backend.requests)
	}
}

func TestDBClusterWaitUntilActiveStopsOnTerminalStatuses(t *testing.T) {
	t.Parallel()

	for _, status := range []dbaasclustersdk.DBClusterStatus{dbaasclustersdk.DBClusterStatusError, dbaasclustersdk.DBClusterStatusAbnormal} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			backend := newFakeBackend(t)
			backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, clusterWithStatus(status, dbaasclustersdk.DBClusterOperationalStatusNoop)))
			r := newTestResource(t, backend)
			_, err := r.waitUntilActive(context.Background(), testClusterID)
			if err == nil || errors.Is(err, wait.ErrTimeout) || len(backend.requests) != 1 {
				t.Fatalf("expected an immediate failure: err=%v requests=%d", err, len(backend.requests))
			}
		})
	}
}

func TestDBClusterWaitUntilActiveTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusCreating, dbaasclustersdk.DBClusterOperationalStatusNoop)))
		r := newTestResource(t, backend)

		start := time.Now()
		_, err := r.waitUntilActive(context.Background(), testClusterID)
		if !errors.Is(err, wait.ErrTimeout) || time.Since(start) != dbClusterCreateTimeout {
			t.Fatalf("err=%v elapsed=%s, want a timeout after %s", err, time.Since(start), dbClusterCreateTimeout)
		}
	})
}

// The SDK already retries 429 and 5xx responses, so the waiter stops on any
// read error instead of adding a second retry layer.
func TestDBClusterWaitUntilActiveStopsOnReadErrors(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{
		"server error": http.StatusInternalServerError,
		"unauthorized": http.StatusUnauthorized,
		"not found":    http.StatusNotFound,
		"rejected":     http.StatusBadRequest,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			backend := newFakeBackend(t)
			backend.respond(http.MethodGet, clusterPath(), status, `{}`)
			r := newTestResource(t, backend)
			if _, err := r.waitUntilActive(context.Background(), testClusterID); err == nil || len(backend.requests) != 1 {
				t.Fatalf("%s must stop the wait: err=%v requests=%d", name, err, len(backend.requests))
			}
		})
	}
}

func TestDBClusterWaitUntilActiveReportsAMissingErrorMessage(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusError, dbaasclustersdk.DBClusterOperationalStatusNoop)))
	r := newTestResource(t, backend)
	_, err := r.waitUntilActive(context.Background(), testClusterID)
	if err == nil || !strings.Contains(err.Error(), "no error message") {
		t.Fatalf("expected a failure that names the missing message: err=%v", err)
	}
}

func TestDBClusterWaitUntilDeletedStopsWhenTheCoreRefuses(t *testing.T) {
	t.Parallel()

	refused := clusterWithStatus(dbaasclustersdk.DBClusterStatusAbnormal, dbaasclustersdk.DBClusterOperationalStatusNoop)
	refused.ErrorMessage = "node still attached"
	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, refused))
	r := newTestResource(t, backend)
	err := r.waitUntilDeleted(context.Background(), testClusterID)
	if err == nil || !strings.Contains(err.Error(), "node still attached") || len(backend.requests) != 1 {
		t.Fatalf("expected an immediate failure with the backend message: err=%v requests=%d", err, len(backend.requests))
	}
}

// A cluster in error status can be deleted, so the error status alone must
// not stop the wait.
func TestDBClusterWaitUntilDeletedWaitsForAnErroredCluster(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		polls := 0
		backend.on(http.MethodGet, clusterPath(), func(req *http.Request) (*http.Response, error) {
			polls++
			if polls == 1 {
				return testHTTPResponse(req, http.StatusOK, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusError, dbaasclustersdk.DBClusterOperationalStatusDeleting))), nil
			}
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		})
		r := newTestResource(t, backend)

		start := time.Now()
		if err := r.waitUntilDeleted(context.Background(), testClusterID); err != nil || polls != 2 || time.Since(start) != dbClusterPollInterval {
			t.Fatalf("err=%v polls=%d elapsed=%s", err, polls, time.Since(start))
		}
	})
}

func TestDBClusterWaitUntilDeletedTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := newFakeBackend(t)
		backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, clusterWithStatus(dbaasclustersdk.DBClusterStatusActive, dbaasclustersdk.DBClusterOperationalStatusDeleting)))
		r := newTestResource(t, backend)

		start := time.Now()
		err := r.waitUntilDeleted(context.Background(), testClusterID)
		if !errors.Is(err, wait.ErrTimeout) || time.Since(start) != dbClusterDeleteTimeout {
			t.Fatalf("err=%v elapsed=%s, want a timeout after %s", err, time.Since(start), dbClusterDeleteTimeout)
		}
	})
}

func TestDBClusterWaitUntilDeletedStopsOnReadErrors(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusInternalServerError, `{}`)
	r := newTestResource(t, backend)
	if err := r.waitUntilDeleted(context.Background(), testClusterID); err == nil || len(backend.requests) != 1 {
		t.Fatalf("a read error must stop the wait: err=%v requests=%d", err, len(backend.requests))
	}
}

func TestClusterReadyRequiresActiveAndIdle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status    dbaasclustersdk.DBClusterStatus
		operation dbaasclustersdk.DBClusterOperationalStatus
		ready     bool
		failed    bool
	}{
		{dbaasclustersdk.DBClusterStatusActive, dbaasclustersdk.DBClusterOperationalStatusNoop, true, false},
		{dbaasclustersdk.DBClusterStatusActive, dbaasclustersdk.DBClusterOperationalStatusScaling, false, false},
		{dbaasclustersdk.DBClusterStatusCreating, dbaasclustersdk.DBClusterOperationalStatusNoop, false, false},
		{dbaasclustersdk.DBClusterStatusProvisioning, dbaasclustersdk.DBClusterOperationalStatusNoop, false, false},
		{dbaasclustersdk.DBClusterStatusRestoring, dbaasclustersdk.DBClusterOperationalStatusNoop, false, false},
		{dbaasclustersdk.DBClusterStatusError, dbaasclustersdk.DBClusterOperationalStatusNoop, false, true},
		{dbaasclustersdk.DBClusterStatusAbnormal, dbaasclustersdk.DBClusterOperationalStatusNoop, false, true},
	}
	for _, tt := range tests {
		ready, err := clusterReady(clusterWithStatus(tt.status, tt.operation))
		if ready != tt.ready || (err != nil) != tt.failed {
			t.Errorf("%s/%s: ready=%t err=%v, want ready=%t failed=%t", tt.status, tt.operation, ready, err, tt.ready, tt.failed)
		}
	}
}

func TestDBClusterMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	r := NewDBClusterResource().(*DBClusterResource)
	var metadata resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_dbc_cluster" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	var ignored resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &ignored)
	if ignored.Diagnostics.HasError() || r.client != nil {
		t.Fatalf("a nil provider data must be ignored: %v", ignored.Diagnostics)
	}

	var wrong resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "not provider data"}, &wrong)
	if !wrong.Diagnostics.HasError() {
		t.Fatal("unexpected provider data must fail")
	}

	client := testDBaaSClient(t, newFakeBackend(t).RoundTrip)
	project := testProjectClient(t, newFakeBackend(t).RoundTrip)
	var configured resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &providerdata.Configured{DBaaSCluster: client, Project: project, ProjectID: testProjectID}}, &configured)
	if configured.Diagnostics.HasError() || r.client != client || r.project != project || r.projectID != testProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestDBClusterNodePoolPlanModifierDescription(t *testing.T) {
	t.Parallel()

	modifier := nodePoolsRequireReplace{}
	if modifier.Description(context.Background()) == "" || modifier.MarkdownDescription(context.Background()) == "" {
		t.Fatal("the plan modifier needs a description")
	}
}

// validateConfigDiagnostics runs the resource ValidateConfig on a configuration.
func validateConfigDiagnostics(t *testing.T, config DBClusterResourceModel) []string {
	t.Helper()
	resourceSchema := resourceSchemaForTest(t)
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &config); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	var resp resource.ValidateConfigResponse
	(&DBClusterResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: tfsdk.Config{Schema: resourceSchema, Raw: state.Raw},
	}, &resp)
	return errorPaths(resp.Diagnostics)
}

// configModel returns a configuration: an Optional and Computed attribute that
// the practitioner omits is null.
func configModel(engine, topology string, pools ...attr.Value) DBClusterResourceModel {
	model := testPlan(engine, topology, testPools(pools...))
	model.Topology = types.StringNull()
	if topology != "" {
		model.Topology = types.StringValue(topology)
	}
	model.Description, model.KeepBackup = types.StringNull(), types.BoolNull()
	model.EnableSSL, model.EnablePITRBackup = types.BoolNull(), types.BoolNull()
	model.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
	model.EngineConfig = types.ObjectNull(engineConfigAttrTypes)
	return model
}

func configPoolValue(name string, replicas int64, partitions types.Int64) attr.Value {
	attributes := testPool(name, replicas, 0, 100).(types.Object).Attributes()
	attributes["partition_count"] = partitions
	attributes["engine_roles"] = types.SetNull(types.StringType)
	attributes["pool_config"] = types.ObjectNull(poolConfigAttrTypes)
	return types.ObjectValueMust(nodePoolAttrTypes, attributes)
}

func TestDBClusterValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config DBClusterResourceModel
		want   []string
	}{
		{name: "valid mongo", config: configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))},
		{name: "valid opensearch", config: configModel(engineOpenSearch, "", configPoolValue("data", 3, types.Int64Null()))},
		{name: "valid cassandra", config: configModel(engineCassandra, "", configPoolValue("cassandra", 3, types.Int64Null()))},
		{name: "valid memcached", config: configModel(engineMemcached, "", memcachedPool("memcached", 2))},
		{name: "memcached rejects storage", config: configModel(engineMemcached, "", configPoolValue("memcached", 2, types.Int64Null())), want: []string{"node_pools[0].storage_class", "node_pools[0].disk_size"}},
		{name: "valid starrocks", config: configModel(engineStarRocks, "", configPoolValue("fe", 1, types.Int64Null()), configPoolValue("be", 3, types.Int64Null()))},
		{name: "valid timescaledb", config: configModel(engineTimescaleDB, "", configPoolValue("db", 1, types.Int64Null()))},
		{name: "unsupported engine", config: configModel("postgres", "", configPoolValue("db", 1, types.Int64Null())), want: []string{"engine"}},
		{name: "padded engine, topology and backup interval", config: func() DBClusterResourceModel {
			model := configModel(" mongodb\t", " mongo_sharding ", configPoolValue("shard", 3, types.Int64Value(2)))
			model.AutoBackupConfig = backupSchedule(true, " weekly ", "monday")
			return model
		}()},
		{name: "padded backup interval keeps the interval rules", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.AutoBackupConfig = backupSchedule(true, " daily ", "monday")
			return model
		}(), want: []string{"auto_backup_config.day_of_week"}},
		{name: "blank cluster, pool and plugin names", config: func() DBClusterResourceModel {
			model := configModel(engineOpenSearch, "", configPoolValue(" ", 3, types.Int64Null()))
			model.Name = types.StringValue("  ")
			model.Plugins = types.SetValueMust(types.ObjectType{AttrTypes: pluginAttrTypes}, []attr.Value{
				types.ObjectValueMust(pluginAttrTypes, map[string]attr.Value{"name": types.StringValue("\t")}),
			})
			return model
		}(), want: []string{"name", "node_pools[0].name", "plugins"}},
		{name: "padded subnet id", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.Subnet = reference.Value(reference.ID, paddedString(testSubnetID.String()))
			return model
		}()},
		{name: "subnet id that is not a uuid", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.Subnet = reference.Value(reference.ID, types.StringValue("subnet-1"))
			return model
		}(), want: []string{"subnet.id"}},
		{name: "mongo without a topology", config: configModel(engineMongoDB, "", configPoolValue("mongo", 3, types.Int64Null())), want: []string{"topology"}},
		{name: "unknown engine defers", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, "", configPoolValue("mongo", 3, types.Int64Null()))
			model.Engine = types.StringUnknown()
			return model
		}()},
		{name: "blank region and engine version", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.Region = reference.Value(reference.Name, types.StringValue("  "))
			model.EngineVersion = reference.Value(criterionVersion, types.StringValue(""))
			return model
		}(), want: []string{"engine_version.version", "region.name"}},
		{name: "blank zone", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.Zone = reference.Value(reference.Name, types.StringValue(" "))
			return model
		}(), want: []string{"zone.name"}},
		{name: "blank pool references", config: func() DBClusterResourceModel {
			attributes := configPoolValue("mongo", 3, types.Int64Null()).(types.Object).Attributes()
			attributes["resource_config"] = reference.Value(reference.Name, types.StringValue(""))
			attributes["storage_class"] = reference.Value(reference.Name, types.StringValue("  "))
			return configModel(engineMongoDB, topologyMongoRs, types.ObjectValueMust(nodePoolAttrTypes, attributes))
		}(), want: []string{"node_pools[0].resource_config.name", "node_pools[0].storage_class.name"}},
		{name: "blank names are checked while engine is unknown", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, "", configPoolValue("mongo", 3, types.Int64Null()))
			model.Engine = types.StringUnknown()
			model.Region = reference.Value(reference.Name, types.StringValue(""))
			return model
		}(), want: []string{"region.name"}},
		{name: "allowed cidrs need an ipv4 prefix", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.AllowedCIDRs = stringSetValue([]string{"10.0.0.0/24", "10.0.0.5", " 10.1.0.0/16\t", "fd00::/8"})
			return model
		}(), want: []string{"allowed_cidrs", "allowed_cidrs"}},
		{name: "allowed cidrs equal after the trim", config: func() DBClusterResourceModel {
			model := configModel(engineMongoDB, topologyMongoRs, configPoolValue("mongo", 3, types.Int64Null()))
			model.AllowedCIDRs = stringSetValue([]string{"10.0.0.0/24", " 10.0.0.0/24"})
			return model
		}(), want: []string{"allowed_cidrs"}},
		{name: "unknown pools defer pool checks", config: func() DBClusterResourceModel {
			model := configModel(engineOpenSearch, "")
			model.NodePools = types.ListUnknown(types.ObjectType{AttrTypes: nodePoolAttrTypes})
			return model
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := validateConfigDiagnostics(t, tt.config); !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Fatalf("error paths = %v, want %v", got, tt.want)
			}
		})
	}
}

func fakeResolvers(calls *[]string) clusterResolvers {
	record := func(call string) { *calls = append(*calls, call) }
	return clusterResolvers{
		region: func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
			record("region:" + *filter.Name)
			return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: testRegionID}}, nil
		},
		zone: func(_ context.Context, filter projectlookup.ZoneFilter) (projectsdk.ProjectZoneSchema, error) {
			record("zone:" + *filter.Name)
			return projectsdk.ProjectZoneSchema{Id: testPoolID}, nil
		},
		engineVersion: func(_ context.Context, filter dbaasclusterlookup.EngineVersionFilter) (dbaasclustersdk.DBClusterEngineVersionSchema, error) {
			record("version:" + *filter.Engine + "/" + *filter.Version + "/active=" + map[bool]string{true: "true", false: "false"}[*filter.IsActive])
			return dbaasclustersdk.DBClusterEngineVersionSchema{Id: testVersionID}, nil
		},
		resourcePackage: func(_ context.Context, filter dbaasclusterlookup.ResourcePackageFilter) (dbaasclustersdk.DBClusterResourcePackageSchema, error) {
			record("package:" + *filter.Name)
			return dbaasclustersdk.DBClusterResourcePackageSchema{Id: testPackageID}, nil
		},
		storageClass: func(_ context.Context, filter dbaasclusterlookup.StorageClassFilter) (dbaasclustersdk.DBClusterStorageClassSchema, error) {
			record("class:" + *filter.Name)
			return dbaasclustersdk.DBClusterStorageClassSchema{Id: testClassID}, nil
		},
	}
}

func TestResolveClusterRefsResolvesEveryReference(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	plan.Zone = reference.Value(reference.Name, types.StringValue("zone-a"))
	cfg, _ := engineConfigOf(context.Background(), plan)

	var calls []string
	refs, diags := resolveClusterRefs(context.Background(), plan, cfg, fakeResolvers(&calls))
	if diags.HasError() {
		t.Fatalf("resolve: %v", diags)
	}
	want := []string{"region:region-1", "zone:zone-a", "version:mongodb/7.0/active=true", "package:2c4g", "class:ssd"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if refs.RegionID != testRegionID || refs.ZoneID == nil || *refs.ZoneID != testPoolID || refs.SubnetID != testSubnetID ||
		refs.EngineVersionID != testVersionID || len(refs.Pools) != 1 || refs.Pools[0].ResourceConfigID != testPackageID ||
		refs.Pools[0].StorageClassID == nil || *refs.Pools[0].StorageClassID != testClassID {
		t.Fatalf("refs = %#v", refs)
	}
}

func TestResolveClusterRefsLeavesAnOmittedZoneUnresolved(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
	cfg, _ := engineConfigOf(context.Background(), plan)
	var calls []string
	refs, diags := resolveClusterRefs(context.Background(), plan, cfg, fakeResolvers(&calls))
	if diags.HasError() || refs.ZoneID != nil {
		t.Fatalf("an omitted zone must stay nil: zone=%v diags=%v", refs.ZoneID, diags)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "zone:") {
			t.Fatalf("the zone must not be looked up: %v", calls)
		}
	}
}

func TestResolveClusterRefsSkipsTheStorageOfAPoolWithoutStorage(t *testing.T) {
	t.Parallel()

	plan := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
	cfg, _ := engineConfigOf(context.Background(), plan)
	var calls []string
	refs, diags := resolveClusterRefs(context.Background(), plan, cfg, fakeResolvers(&calls))
	if diags.HasError() || len(refs.Pools) != 1 || refs.Pools[0].StorageClassID != nil {
		t.Fatalf("a pool without storage must have no storage class: refs=%#v diags=%v", refs, diags)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "class:") {
			t.Fatalf("the storage class must not be looked up: %v", calls)
		}
	}
}

func TestResolveClusterRefsReportsEveryFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	tests := []struct {
		name   string
		mutate func(*DBClusterResourceModel, *clusterResolvers)
		want   string
	}{
		{name: "missing region", mutate: func(p *DBClusterResourceModel, _ *clusterResolvers) {
			p.Region = reference.Value(reference.Name, types.StringNull())
		}, want: "region"},
		{name: "blank region", mutate: func(p *DBClusterResourceModel, _ *clusterResolvers) {
			p.Region = reference.Value(reference.Name, types.StringValue("  "))
		}, want: "region"},
		{name: "unresolvable region", mutate: func(_ *DBClusterResourceModel, r *clusterResolvers) {
			r.region = func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
				return projectsdk.ProjectRegionSchema{}, boom
			}
		}, want: "region"},
		{name: "unresolvable zone", mutate: func(p *DBClusterResourceModel, r *clusterResolvers) {
			p.Zone = reference.Value(reference.Name, types.StringValue("zone-a"))
			r.zone = func(context.Context, projectlookup.ZoneFilter) (projectsdk.ProjectZoneSchema, error) {
				return projectsdk.ProjectZoneSchema{}, boom
			}
		}, want: "zone"},
		{name: "invalid subnet", mutate: func(p *DBClusterResourceModel, _ *clusterResolvers) {
			p.Subnet = reference.Value(reference.ID, types.StringValue("nope"))
		}, want: "subnet"},
		{name: "missing engine version", mutate: func(p *DBClusterResourceModel, _ *clusterResolvers) {
			p.EngineVersion = reference.Value(criterionVersion, types.StringNull())
		}, want: "engine_version"},
		{name: "unresolvable engine version", mutate: func(_ *DBClusterResourceModel, r *clusterResolvers) {
			r.engineVersion = func(context.Context, dbaasclusterlookup.EngineVersionFilter) (dbaasclustersdk.DBClusterEngineVersionSchema, error) {
				return dbaasclustersdk.DBClusterEngineVersionSchema{}, boom
			}
		}, want: "engine_version"},
		{name: "unresolvable resource package", mutate: func(_ *DBClusterResourceModel, r *clusterResolvers) {
			r.resourcePackage = func(context.Context, dbaasclusterlookup.ResourcePackageFilter) (dbaasclustersdk.DBClusterResourcePackageSchema, error) {
				return dbaasclustersdk.DBClusterResourcePackageSchema{}, boom
			}
		}, want: "node_pools[0].resource_config"},
		{name: "unresolvable storage class", mutate: func(_ *DBClusterResourceModel, r *clusterResolvers) {
			r.storageClass = func(context.Context, dbaasclusterlookup.StorageClassFilter) (dbaasclustersdk.DBClusterStorageClassSchema, error) {
				return dbaasclustersdk.DBClusterStorageClassSchema{}, boom
			}
		}, want: "node_pools[0].storage_class"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := testPlan(engineMongoDB, topologyMongoRs, testPools(testPool("mongo", 3, 0, 100)))
			var calls []string
			resolvers := fakeResolvers(&calls)
			tt.mutate(&plan, &resolvers)
			cfg, _ := engineConfigOf(context.Background(), plan)
			_, diags := resolveClusterRefs(context.Background(), plan, cfg, resolvers)
			if got := errorPaths(diags); !reflect.DeepEqual(got, []string{tt.want}) && (tt.want != "subnet" || len(got) != 1) {
				t.Fatalf("error paths = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestMaintenanceBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config types.Object
		want   string
	}{
		{name: "null", config: types.ObjectNull(maintenanceAttrTypes), want: "null"},
		{name: "unknown", config: types.ObjectUnknown(maintenanceAttrTypes), want: "null"},
		{name: "both unknown", config: types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{"day_of_week": types.StringUnknown(), "start_hour": types.Int64Unknown()}), want: "null"},
		{name: "day only", config: types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{"day_of_week": types.StringValue("monday"), "start_hour": types.Int64Unknown()}), want: `{"day_of_week":"monday"}`},
		{name: "hour only", config: types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{"day_of_week": types.StringNull(), "start_hour": types.Int64Value(0)}), want: `{"start_hour":0}`},
		{name: "both", config: types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{"day_of_week": types.StringValue("friday"), "start_hour": types.Int64Value(23)}), want: `{"day_of_week":"friday","start_hour":23}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := jsonBody(t, maintenanceBody(tt.config)); got != tt.want {
				t.Fatalf("body = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAutoBackupBodyRejectsAnUnknownInterval(t *testing.T) {
	t.Parallel()

	if body, diags := autoBackupBody(backupSchedule(true, "hourly", "")); !diags.HasError() || body != nil {
		t.Fatalf("an unknown interval must fail: body=%v diags=%v", body, diags)
	}
	if body, diags := autoBackupBody(types.ObjectNull(autoBackupAttrTypes)); diags.HasError() || body != nil {
		t.Fatalf("an omitted schedule sends nothing: body=%v diags=%v", body, diags)
	}
}

func TestDiskSizeNeedsReplace(t *testing.T) {
	t.Parallel()

	i := types.Int64Value
	tests := []struct {
		name      string
		planned   types.Int64
		requested types.Int64
		actual    types.Int64
		want      bool
	}{
		{name: "larger than both", planned: i(200), requested: i(100), actual: i(100), want: true},
		{name: "equal", planned: i(100), requested: i(100), actual: i(100)},
		{name: "smaller", planned: i(60), requested: i(100), actual: i(100)},
		{name: "between the request and the size in use", planned: i(110), requested: i(100), actual: i(120)},
		{name: "equal to the size in use", planned: i(120), requested: i(100), actual: i(120)},
		{name: "above the size in use", planned: i(130), requested: i(100), actual: i(120), want: true},
		{name: "size in use unknown", planned: i(110), requested: i(100), actual: types.Int64Unknown(), want: true},
		{name: "size in use missing", planned: i(110), requested: i(100), actual: types.Int64Null(), want: true},
		{name: "unknown plan", planned: types.Int64Unknown(), requested: i(100), actual: i(100), want: true},
		{name: "null request differs", planned: i(100), requested: types.Int64Null(), actual: i(100), want: true},
		{name: "both null", planned: types.Int64Null(), requested: types.Int64Null(), actual: types.Int64Null()},
	}
	for _, tt := range tests {
		if got := diskSizeNeedsReplace(tt.planned, tt.requested, tt.actual); got != tt.want {
			t.Errorf("%s: got %t, want %t", tt.name, got, tt.want)
		}
	}
}

// The Framework marks every Computed child that configuration leaves null as
// unknown once a plan changes anything. A partly configured object must still
// keep the cluster, so this test runs the whole plan through the provider
// server instead of calling the modifiers one by one.
func TestDBClusterPlanKeepsPartlyConfiguredObjectsOnUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		mode        string
		wantReplace bool
	}{
		{name: "a description change updates in place", mode: "direct"},
		{name: "a configured engine_config change replaces", mode: "mcrouter", wantReplace: true},
		{name: "omitted objects keep the state value", mode: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			resourceSchema := resourceSchemaForTest(t)

			state := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
			populateDBClusterResourceState(sampleMemcachedCluster(), &state)

			// The configuration changes the description. It sets only mode and
			// day_of_week, or omits both objects when mode is empty.
			config := testPlan(engineMemcached, "", testPools(memcachedPool("memcached", 2)))
			config.Description = types.StringValue("changed")
			config.RootPassword = types.StringValue("Passw0rd!")
			// Terraform proposes the configured values and keeps prior values
			// for Computed attributes that configuration leaves null.
			proposed := state
			proposed.Description = config.Description
			if tt.mode != "" {
				config.EngineConfig = types.ObjectValueMust(engineConfigAttrTypes, map[string]attr.Value{
					"mode": types.StringValue(tt.mode), "auth_enabled": types.BoolUnknown(), "mcrouter_routing_type": types.StringUnknown(),
				})
				config.MaintenanceConfig = types.ObjectValueMust(maintenanceAttrTypes, map[string]attr.Value{
					"day_of_week": types.StringValue("sunday"), "start_hour": types.Int64Unknown(),
				})
				proposedSettings := state.EngineConfig.Attributes()
				proposedSettings["mode"] = types.StringValue(tt.mode)
				proposed.EngineConfig = types.ObjectValueMust(engineConfigAttrTypes, proposedSettings)
			}

			response, err := providerserver.NewProtocol6(&dbClusterPlanningProvider{})().PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "viettelcloud_dbc_cluster",
				PriorState:       planningValue(t, resourceSchema, state, false),
				Config:           planningValue(t, resourceSchema, config, true),
				ProposedNewState: planningValue(t, resourceSchema, proposed, false),
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, diagnostic := range response.Diagnostics {
				if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatalf("%s: %s", diagnostic.Summary, diagnostic.Detail)
				}
			}
			if (len(response.RequiresReplace) > 0) != tt.wantReplace {
				t.Fatalf("replacement paths=%v, want replacement=%t", response.RequiresReplace, tt.wantReplace)
			}
		})
	}
}

// planningValue encodes a model for a PlanResourceChange request. A
// configuration has no unknown Computed values, so asConfig turns every
// unknown value into null.
func planningValue(t *testing.T, resourceSchema schema.Schema, model DBClusterResourceModel, asConfig bool) *tfprotov6.DynamicValue {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	raw := state.Raw
	if asConfig {
		var err error
		raw, err = tftypes.Transform(raw, func(_ *tftypes.AttributePath, value tftypes.Value) (tftypes.Value, error) {
			if !value.IsKnown() {
				return tftypes.NewValue(value.Type(), nil), nil
			}
			return value, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	value, err := tfprotov6.NewDynamicValue(raw.Type(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return &value
}

type dbClusterPlanningProvider struct{}

func (p *dbClusterPlanningProvider) Metadata(_ context.Context, _ frameworkprovider.MetadataRequest, resp *frameworkprovider.MetadataResponse) {
	resp.TypeName = "viettelcloud"
}

func (p *dbClusterPlanningProvider) Schema(_ context.Context, _ frameworkprovider.SchemaRequest, resp *frameworkprovider.SchemaResponse) {
	resp.Schema = providerschema.Schema{}
}

func (p *dbClusterPlanningProvider) Configure(context.Context, frameworkprovider.ConfigureRequest, *frameworkprovider.ConfigureResponse) {
}

func (p *dbClusterPlanningProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewDBClusterResource}
}

func (p *dbClusterPlanningProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func TestBackupAttributesReplaceTheCluster(t *testing.T) {
	t.Parallel()

	attributes := resourceSchemaForTest(t).Attributes
	resourceSchema := resourceSchemaForTest(t)
	raw := nullRawForTest(t, resourceSchema)
	state, plan := tfsdk.State{Schema: resourceSchema, Raw: raw}, tfsdk.Plan{Schema: resourceSchema, Raw: raw}

	schedule := attributes["auto_backup_config"].(schema.SingleNestedAttribute)
	runSchedule := func(stateValue, planValue types.Object) bool {
		request := planmodifier.ObjectRequest{State: state, Plan: plan, StateValue: stateValue, PlanValue: planValue, ConfigValue: planValue}
		replace := false
		for _, modifier := range schedule.PlanModifiers {
			response := planmodifier.ObjectResponse{PlanValue: planValue}
			modifier.PlanModifyObject(context.Background(), request, &response)
			replace = replace || response.RequiresReplace
		}
		return replace
	}
	weekly, daily := backupSchedule(true, "weekly", "monday"), backupSchedule(true, "daily", "")
	if !runSchedule(weekly, daily) {
		t.Error("changing the schedule must replace the cluster")
	}
	if !runSchedule(weekly, types.ObjectNull(autoBackupAttrTypes)) {
		t.Error("removing the schedule must replace the cluster")
	}
	if !runSchedule(types.ObjectNull(autoBackupAttrTypes), weekly) {
		t.Error("adding a schedule must replace the cluster")
	}
	if runSchedule(weekly, backupSchedule(true, "weekly", "monday")) {
		t.Error("an unchanged schedule must not replace the cluster")
	}

	pitr := attributes["enable_pitr_backup"].(schema.BoolAttribute)
	runPITR := func(stateValue, planValue, configValue types.Bool) bool {
		request := planmodifier.BoolRequest{State: state, Plan: plan, StateValue: stateValue, PlanValue: planValue, ConfigValue: configValue}
		replace := false
		for _, modifier := range pitr.PlanModifiers {
			response := planmodifier.BoolResponse{PlanValue: request.PlanValue}
			modifier.PlanModifyBool(context.Background(), request, &response)
			request.PlanValue = response.PlanValue
			replace = replace || response.RequiresReplace
		}
		return replace
	}
	if !runPITR(types.BoolValue(false), types.BoolValue(true), types.BoolValue(true)) {
		t.Error("enabling PITR must replace the cluster")
	}
	if runPITR(types.BoolValue(true), types.BoolUnknown(), types.BoolNull()) {
		t.Error("omitting PITR must keep the cluster")
	}
}
