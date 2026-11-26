package dbaascluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	testClusterID = core.UUID{1}
	testProjectID = core.UUID{9}
	testRegionID  = core.UUID{2}
	testSubnetID  = core.UUID{3}
	testVersionID = core.UUID{4}
	testPackageID = core.UUID{5}
	testClassID   = core.UUID{6}
	testPoolID    = core.UUID{7}
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testDBaaSClient(t *testing.T, roundTrip roundTripFunc) *dbaasclustersdk.Client {
	t.Helper()
	client, err := dbaasclustersdk.NewClient(
		"https://dbaas.test",
		dbaasclustersdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		dbaasclustersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create DBaaS cluster client: %v", err)
	}
	return client
}

func testProjectClient(t *testing.T, roundTrip roundTripFunc) *projectsdk.Client {
	t.Helper()
	client, err := projectsdk.NewClient(
		"https://project.test",
		projectsdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		projectsdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return client
}

func testHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func jsonBody(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return string(encoded)
}

// pageBody wraps results in the paged envelope of a list endpoint.
func pageBody(t *testing.T, results ...any) string {
	t.Helper()
	return jsonBody(t, map[string]any{"count": len(results), "results": results})
}

// sampleCluster returns a mongo_rs cluster with one pool, as the backend
// reports it once active.
func sampleCluster() *dbaasclustersdk.DBClusterDetailSchema {
	diskSize := 100
	return &dbaasclustersdk.DBClusterDetailSchema{
		Id:          testClusterID,
		Name:        "orders",
		Description: "primary",
		Engine:      dbaasclustersdk.DBClusterEngineTypeMongodb,
		Topology:    dbaasclustersdk.DBClusterTopologyKindMongoRs,
		EngineVersion: dbaasclustersdk.NestedDBClusterEngineVersionSchema{
			Id: testVersionID, Engine: dbaasclustersdk.DBClusterEngineTypeMongodb, Version: "7.0",
		},
		Region:  dbaasclustersdk.NestedRegionSchema{Id: testRegionID, Name: "region-1"},
		Project: dbaasclustersdk.NestedProjectSchema{Id: testProjectID, Name: "project"},
		Subnet: dbaasclustersdk.NestedDBClusterSubnetSchema{
			Id: testSubnetID, Name: "subnet-1", Cidr: "10.0.0.0/24",
			Vpc: dbaasclustersdk.NestedVPCSchema{Id: core.UUID{8}, Name: "vpc-1"},
		},
		Status:            dbaasclustersdk.DBClusterStatusActive,
		OperationalStatus: dbaasclustersdk.DBClusterOperationalStatusNoop,
		KeepBackup:        true,
		AllowedCidrs:      []string{},
		Endpoint:          map[string]any{"connection_string": "mongodb://host:27017"},
		Metadata:          map[string]any{},
		MaintenanceConfig: map[string]any{"day_of_week": "sunday", "start_hour": float64(0)},
		AdminUsername:     "databaseAdmin",
		NodePools: []dbaasclustersdk.NestedDBClusterNodePoolSchema{{
			Id: testPoolID, Name: "mongo", Role: dbaasclustersdk.DBClusterNodeRoleMongoNode,
			Replicas: 3, PartitionCount: 1, NodeCount: 3,
			ResourceConfig: dbaasclustersdk.NestedDBClusterResourceConfigSchema{Id: testPackageID, Name: "2c4g", Vcpus: 2, Ram: 4},
			StorageClass:   core.NullableFromPtr(&dbaasclustersdk.NestedDBClusterStorageClassSchema{Id: testClassID, Name: "ssd"}),
			DiskSize:       core.NullableFromPtr(&diskSize),
			UsedDiskSize:   10,
			EngineRoles:    []string{},
			PoolConfig:     map[string]any{},
		}},
		Plugins:   []dbaasclustersdk.NestedDBClusterPluginSchema{},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

// sampleOpenSearchCluster returns an opensearch cluster with a data pool and a
// manager pool.
func sampleOpenSearchCluster() *dbaasclustersdk.DBClusterDetailSchema {
	cluster := sampleCluster()
	cluster.Name = "search"
	cluster.Engine = dbaasclustersdk.DBClusterEngineTypeOpensearch
	cluster.Topology = dbaasclustersdk.DBClusterTopologyKindOpensearch
	cluster.EngineVersion.Engine = dbaasclustersdk.DBClusterEngineTypeOpensearch
	cluster.EngineVersion.Version = "2.11"
	cluster.KeepBackup = false
	cluster.EnableSsl = true
	diskSize := 50
	cluster.NodePools = []dbaasclustersdk.NestedDBClusterNodePoolSchema{
		openSearchPool(core.UUID{10}, "manager", []string{"cluster_manager"}, 3, &diskSize),
		openSearchPool(core.UUID{11}, "data", []string{"data", "ingest"}, 2, &diskSize),
	}
	return cluster
}

func openSearchPool(id core.UUID, name string, roles []string, replicas int, diskSize *int) dbaasclustersdk.NestedDBClusterNodePoolSchema {
	return dbaasclustersdk.NestedDBClusterNodePoolSchema{
		Id: id, Name: name, Role: dbaasclustersdk.DBClusterNodeRoleOpensearchNode,
		Replicas: replicas, PartitionCount: 1, NodeCount: replicas,
		ResourceConfig: dbaasclustersdk.NestedDBClusterResourceConfigSchema{Id: testPackageID, Name: "2c4g"},
		StorageClass:   core.NullableFromPtr(&dbaasclustersdk.NestedDBClusterStorageClassSchema{Id: testClassID, Name: "ssd"}),
		DiskSize:       core.NullableFromPtr(diskSize),
		EngineRoles:    roles,
		PoolConfig:     map[string]any{"attribute": "hot"},
	}
}

// sampleMemcachedCluster returns a memcached cluster in direct mode with one
// pool. Its pool has no storage.
func sampleMemcachedCluster() *dbaasclustersdk.DBClusterDetailSchema {
	cluster := sampleCluster()
	cluster.Name = "cache"
	cluster.Engine = dbaasclustersdk.DBClusterEngineTypeMemcached
	cluster.Topology = dbaasclustersdk.DBClusterTopologyKindMemcached
	cluster.EngineVersion.Engine = dbaasclustersdk.DBClusterEngineTypeMemcached
	cluster.EngineVersion.Version = "1.6"
	cluster.EngineConfig = map[string]any{"mode": "direct", "auth_enabled": false, "mcrouter_routing_type": nil}
	cluster.NodePools = []dbaasclustersdk.NestedDBClusterNodePoolSchema{{
		Id: testPoolID, Name: "memcached", Role: dbaasclustersdk.DBClusterNodeRoleMemcachedNode,
		Replicas: 2, PartitionCount: 1, NodeCount: 2,
		ResourceConfig: dbaasclustersdk.NestedDBClusterResourceConfigSchema{Id: testPackageID, Name: "2c4g"},
		EngineRoles:    []string{},
		PoolConfig:     map[string]any{},
	}}
	return cluster
}

// memcachedPool builds a planned memcached pool, which has no storage.
func memcachedPool(name string, replicas int64) attr.Value {
	attributes := testPool(name, replicas, 0, 0).(types.Object).Attributes()
	attributes["storage_class"] = types.ObjectNull(reference.AttributeTypes(reference.Name))
	attributes["disk_size"] = types.Int64Null()
	return types.ObjectValueMust(nodePoolAttrTypes, attributes)
}

// testPool builds a planned node pool. A zero partitionCount leaves the
// attribute unknown, as the Framework plans an omitted Optional and Computed
// attribute.
func testPool(name string, replicas, partitionCount, diskSize int64) attr.Value {
	partitions := types.Int64Unknown()
	if partitionCount != 0 {
		partitions = types.Int64Value(partitionCount)
	}
	return types.ObjectValueMust(nodePoolAttrTypes, map[string]attr.Value{
		"id":               types.StringUnknown(),
		"name":             types.StringValue(name),
		"role":             types.StringUnknown(),
		"replicas":         types.Int64Value(replicas),
		"partition_count":  partitions,
		"node_count":       types.Int64Unknown(),
		"resource_config":  reference.Value(reference.Name, types.StringValue("2c4g")),
		"storage_class":    reference.Value(reference.Name, types.StringValue("ssd")),
		"disk_size":        types.Int64Value(diskSize),
		"actual_disk_size": types.Int64Unknown(),
		"used_disk_size":   types.Int64Unknown(),
		"engine_roles":     types.SetUnknown(types.StringType),
		"pool_config":      types.ObjectUnknown(poolConfigAttrTypes),
	})
}

func testPools(pools ...attr.Value) types.List {
	return types.ListValueMust(types.ObjectType{AttrTypes: nodePoolAttrTypes}, pools)
}

// testPlan returns a resource model as the Framework plans a minimal
// configuration: every Optional and Computed value is unknown.
func testPlan(engine, topology string, pools types.List) DBClusterResourceModel {
	topologyValue := types.StringUnknown()
	if topology != "" {
		topologyValue = types.StringValue(topology)
	}
	return DBClusterResourceModel{
		DBClusterModel: DBClusterModel{
			ID:                           types.StringUnknown(),
			Name:                         types.StringValue("orders"),
			Description:                  types.StringUnknown(),
			Engine:                       types.StringValue(engine),
			Topology:                     topologyValue,
			KeepBackup:                   types.BoolUnknown(),
			AllowedCIDRs:                 types.SetUnknown(types.StringType),
			EnablePublicAccess:           types.BoolUnknown(),
			EnableSSL:                    types.BoolUnknown(),
			EnableAutoIncreaseVolumeSize: types.BoolUnknown(),
			EnableAutoBackup:             types.BoolUnknown(),
			EnablePITRBackup:             types.BoolUnknown(),
			MaintenanceConfig:            types.ObjectUnknown(maintenanceAttrTypes),
			EngineConfig:                 types.ObjectUnknown(engineConfigAttrTypes),
			Metadata:                     types.MapUnknown(types.StringType),
			AdminUsername:                types.StringUnknown(),
			Endpoint:                     types.MapUnknown(types.StringType),
			Status:                       types.StringUnknown(),
			OperationalStatus:            types.StringUnknown(),
			ErrorMessage:                 types.StringUnknown(),
			CreatedAt:                    types.StringUnknown(),
			UpdatedAt:                    types.StringUnknown(),
		},
		EngineVersion:    reference.Value(criterionVersion, types.StringValue("7.0")),
		Region:           reference.Value(reference.Name, types.StringValue("region-1")),
		Zone:             types.ObjectUnknown(reference.AttributeTypes(reference.Name)),
		Subnet:           reference.Value(reference.ID, types.StringValue(testSubnetID.String())),
		NodePools:        pools,
		Plugins:          types.SetNull(types.ObjectType{AttrTypes: pluginAttrTypes}),
		AutoBackupConfig: types.ObjectNull(autoBackupAttrTypes),
		RootPassword:     types.StringNull(),
	}
}

func testRefs(pools int) resolvedRefs {
	refs := resolvedRefs{RegionID: testRegionID, SubnetID: testSubnetID, EngineVersionID: testVersionID}
	for range pools {
		classID := testClassID
		refs.Pools = append(refs.Pools, resolvedPool{ResourceConfigID: testPackageID, StorageClassID: &classID})
	}
	return refs
}

func resourceSchemaForTest(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&DBClusterResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("resource schema: %v", response.Diagnostics)
	}
	return response.Schema
}

func dataSourceSchemaForTest(t *testing.T) datasource.SchemaResponse {
	t.Helper()
	var response datasource.SchemaResponse
	(&DBClusterDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source schema: %v", response.Diagnostics)
	}
	return response
}

// bodyMap marshals a create body the way the SDK sends it.
func bodyMap(t *testing.T, body dbaasclustersdk.DBClusterCreateSchema) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonBody(t, body)), &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return decoded
}

// nullRawForTest returns a known resource object whose attributes are all null.
// Plan modifiers read a null Raw as create or destroy, so an update needs this
// non-null value.
func nullRawForTest(t *testing.T, resourceSchema schema.Schema) tftypes.Value {
	t.Helper()

	objectType, ok := resourceSchema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("resource schema type is not an object")
	}
	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(attributeType, nil)
	}
	return tftypes.NewValue(objectType, values)
}
