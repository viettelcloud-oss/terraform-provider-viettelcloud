package dbaascluster

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// statePool builds a pool as state holds it after a refresh: every value is
// known.
func statePool(name string, replicas, partitionCount, diskSize int64) attr.Value {
	return types.ObjectValueMust(nodePoolAttrTypes, map[string]attr.Value{
		"id":               types.StringValue(testPoolID.String()),
		"name":             types.StringValue(name),
		"role":             types.StringValue("mongo_node"),
		"replicas":         types.Int64Value(replicas),
		"partition_count":  types.Int64Value(partitionCount),
		"node_count":       types.Int64Value(replicas * partitionCount),
		"resource_config":  reference.Value(reference.Name, types.StringValue("2c4g")),
		"storage_class":    reference.Value(reference.Name, types.StringValue("ssd")),
		"disk_size":        types.Int64Value(diskSize),
		"actual_disk_size": types.Int64Value(diskSize),
		"used_disk_size":   types.Int64Value(1),
		"engine_roles":     types.SetValueMust(types.StringType, []attr.Value{}),
		"pool_config":      types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringNull()}),
	})
}

func runNodePoolsModifier(t *testing.T, planValue, stateValue types.List, createOrDestroy bool) planmodifier.ListResponse {
	t.Helper()

	schema := resourceSchemaForTest(t)
	objectType, ok := schema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("schema type is not an object")
	}
	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(attributeType, nil)
	}
	raw := tftypes.NewValue(objectType, values)
	nullRaw := tftypes.NewValue(objectType, nil)
	state, plan := tfsdk.State{Schema: schema, Raw: raw}, tfsdk.Plan{Schema: schema, Raw: raw}
	if createOrDestroy {
		state = tfsdk.State{Schema: schema, Raw: nullRaw}
	}

	request := planmodifier.ListRequest{State: state, Plan: plan, PlanValue: planValue, StateValue: stateValue, ConfigValue: planValue}
	response := planmodifier.ListResponse{PlanValue: planValue}
	nodePoolsRequireReplace{}.PlanModifyList(context.Background(), request, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", response.Diagnostics)
	}
	return response
}

func replaceOutcome(t *testing.T, planValue, stateValue types.List, createOrDestroy bool) bool {
	t.Helper()
	return runNodePoolsModifier(t, planValue, stateValue, createOrDestroy).RequiresReplace
}

func TestNodePoolsRequireReplace(t *testing.T) {
	t.Parallel()

	state := testPools(statePool("mongo", 3, 1, 100))
	grownAttributes := statePool("mongo", 3, 1, 100).(types.Object).Attributes()
	grownAttributes["actual_disk_size"] = types.Int64Value(120)
	grown := testPools(types.ObjectValueMust(nodePoolAttrTypes, grownAttributes))
	tieredAttributes := statePool("mongo", 3, 1, 100).(types.Object).Attributes()
	tieredAttributes["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("data"), types.StringValue("ingest")})
	tieredAttributes["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("hot")})
	tiered := testPools(types.ObjectValueMust(nodePoolAttrTypes, tieredAttributes))
	// plannedPool is the pool Terraform plans for a configuration that omits
	// the Optional and Computed attributes: those take the value from state.
	plannedPool := func(mutate func(map[string]attr.Value)) types.List {
		attributes := statePool("mongo", 3, 1, 100).(types.Object).Attributes()
		attributes["id"] = types.StringUnknown()
		attributes["used_disk_size"] = types.Int64Unknown()
		attributes["actual_disk_size"] = types.Int64Unknown()
		if mutate != nil {
			mutate(attributes)
		}
		return testPools(types.ObjectValueMust(nodePoolAttrTypes, attributes))
	}

	tests := []struct {
		name    string
		planned types.List
		current types.List
		create  bool
		want    bool
	}{
		{name: "unchanged pool with volatile attributes unknown", planned: plannedPool(nil), current: state},
		{name: "pool name differs only by outer whitespace", planned: plannedPool(func(a map[string]attr.Value) { a["name"] = types.StringValue("  mongo ") }), current: state},
		{name: "pool unknown as a whole", planned: testPools(types.ObjectUnknown(nodePoolAttrTypes)), current: state, want: true},
		{name: "replicas change", planned: plannedPool(func(a map[string]attr.Value) { a["replicas"] = types.Int64Value(5) }), current: state, want: true},
		{name: "larger disk size", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(200) }), current: state, want: true},
		{name: "equal disk size", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(100) }), current: state},
		// State holds the size in use after an import, and the backend never
		// shrinks a volume, so a smaller request must not replace the cluster.
		{name: "smaller disk size than state records", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(60) }), current: state},
		// The backend grew the volume from 100 to 120 on its own.
		{name: "request between the recorded request and the size in use", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(110) }), current: grown},
		{name: "request equal to the size in use", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(120) }), current: grown},
		{name: "request above the size in use", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Value(130) }), current: grown, want: true},
		{name: "unknown disk size", planned: plannedPool(func(a map[string]attr.Value) { a["disk_size"] = types.Int64Unknown() }), current: state, want: true},
		{name: "resource config change", planned: plannedPool(func(a map[string]attr.Value) {
			a["resource_config"] = reference.Value(reference.Name, types.StringValue("4c8g"))
		}), current: state, want: true},
		{name: "resource config differs only by outer whitespace", planned: plannedPool(func(a map[string]attr.Value) {
			a["resource_config"] = reference.Value(reference.Name, types.StringValue(" 2c4g\t"))
		}), current: state},
		{name: "storage class differs only by outer whitespace", planned: plannedPool(func(a map[string]attr.Value) {
			a["storage_class"] = reference.Value(reference.Name, types.StringValue(" ssd\t"))
		}), current: state},
		{name: "storage class change", planned: plannedPool(func(a map[string]attr.Value) {
			a["storage_class"] = reference.Value(reference.Name, types.StringValue("hdd"))
		}), current: state, want: true},
		{name: "partition count change", planned: plannedPool(func(a map[string]attr.Value) { a["partition_count"] = types.Int64Value(2) }), current: state, want: true},
		{name: "omitted partition count keeps the state value", planned: plannedPool(func(a map[string]attr.Value) { a["partition_count"] = types.Int64Unknown() }), current: state},
		{name: "omitted engine roles keep the state value", planned: plannedPool(func(a map[string]attr.Value) { a["engine_roles"] = types.SetUnknown(types.StringType) }), current: state},
		{name: "engine roles change", planned: plannedPool(func(a map[string]attr.Value) {
			a["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("data")})
		}), current: state, want: true},
		{name: "engine roles differ only by outer whitespace", planned: plannedPool(func(a map[string]attr.Value) {
			a["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(" data"), types.StringValue("ingest\t")})
			a["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("hot")})
		}), current: tiered},
		{name: "omitted pool config keeps the state value", planned: plannedPool(func(a map[string]attr.Value) { a["pool_config"] = types.ObjectUnknown(poolConfigAttrTypes) }), current: state},
		{name: "pool config change", planned: plannedPool(func(a map[string]attr.Value) {
			a["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("warm")})
		}), current: state, want: true},
		{name: "pool config differs only by outer whitespace", planned: plannedPool(func(a map[string]attr.Value) {
			a["engine_roles"] = types.SetUnknown(types.StringType)
			a["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue(" hot\t")})
		}), current: tiered},
		{name: "renamed pool", planned: plannedPool(func(a map[string]attr.Value) { a["name"] = types.StringValue("other") }), current: state, want: true},
		{name: "unknown name", planned: plannedPool(func(a map[string]attr.Value) { a["name"] = types.StringUnknown() }), current: state, want: true},
		{name: "added pool", planned: testPools(statePool("mongo", 3, 1, 100), statePool("extra", 3, 1, 100)), current: state, want: true},
		{name: "removed pool", planned: testPools(), current: state, want: true},
		{name: "pools in another order", planned: testPools(statePool("b", 1, 1, 10), statePool("a", 1, 1, 10)), current: testPools(statePool("a", 1, 1, 10), statePool("b", 1, 1, 10))},
		{name: "creation never replaces", planned: plannedPool(func(a map[string]attr.Value) { a["replicas"] = types.Int64Value(5) }), current: state, create: true},
		{name: "unknown plan defers", planned: types.ListUnknown(types.ObjectType{AttrTypes: nodePoolAttrTypes}), current: state},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := replaceOutcome(t, tt.planned, tt.current, tt.create); got != tt.want {
				t.Fatalf("RequiresReplace = %t, want %t", got, tt.want)
			}
		})
	}
}

// unknownPool is a planned pool whose Computed attributes are unknown, as the
// Framework plans them for an update that changes something else.
func unknownPool(name string, replicas, diskSize int64) attr.Value {
	return types.ObjectValueMust(nodePoolAttrTypes, map[string]attr.Value{
		"id":               types.StringUnknown(),
		"name":             types.StringValue(name),
		"role":             types.StringUnknown(),
		"replicas":         types.Int64Value(replicas),
		"partition_count":  types.Int64Unknown(),
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

// identifiedPool is a pool as state holds it, with its own ID and node count.
func identifiedPool(name, id string, replicas, diskSize int64) attr.Value {
	attributes := statePool(name, replicas, 1, diskSize).(types.Object).Attributes()
	attributes["id"] = types.StringValue(id)
	attributes["node_count"] = types.Int64Value(replicas)
	attributes["role"] = types.StringValue("opensearch_node")
	attributes["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue(name + "-tier")})
	return types.ObjectValueMust(nodePoolAttrTypes, attributes)
}

func TestNodePoolsPlanTakesPriorValuesByNameNotByIndex(t *testing.T) {
	t.Parallel()

	// The backend reports data before manager, and the configuration lists
	// manager first. Copying by index would hand manager the id of data.
	state := testPools(identifiedPool("data", "id-data", 2, 50), identifiedPool("manager", "id-manager", 3, 50))
	planned := testPools(unknownPool("manager", 3, 50), unknownPool("data", 2, 50))

	response := runNodePoolsModifier(t, planned, state, false)
	if response.RequiresReplace {
		t.Fatal("a reordered list must not replace the cluster")
	}
	var got []nodePoolModel
	if diags := response.PlanValue.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	if got[0].Name.ValueString() != "manager" || got[1].Name.ValueString() != "data" {
		t.Fatalf("the configured order must be kept: %v", got)
	}
	want := map[string][3]any{"manager": {"id-manager", int64(3), "manager-tier"}, "data": {"id-data", int64(2), "data-tier"}}
	for _, pool := range got {
		expected := want[pool.Name.ValueString()]
		if pool.ID.ValueString() != expected[0] || pool.NodeCount.ValueInt64() != expected[1] ||
			attributeString(pool.PoolConfig, "attribute").ValueString() != expected[2] || pool.Role.ValueString() != "opensearch_node" ||
			pool.PartitionCount.ValueInt64() != 1 || pool.EngineRoles.IsUnknown() {
			t.Errorf("%s: id=%s node_count=%s pool_config=%s role=%s", pool.Name, pool.ID, pool.NodeCount, pool.PoolConfig, pool.Role)
		}
		// The volume figures change on their own, so the plan leaves them open.
		if !pool.UsedDiskSize.IsUnknown() || !pool.ActualDiskSize.IsUnknown() {
			t.Errorf("%s: volatile values must stay unknown", pool.Name)
		}
	}
}

func TestNodePoolsPlanKeepsConfiguredValues(t *testing.T) {
	t.Parallel()

	configured := unknownPool("data", 2, 50).(types.Object).Attributes()
	configured["partition_count"] = types.Int64Value(1)
	configured["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringValue("warm")})
	configured["engine_roles"] = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("ingest")})
	state := testPools(identifiedPool("data", "id-data", 2, 50))
	planned := testPools(types.ObjectValueMust(nodePoolAttrTypes, configured))

	var got []nodePoolModel
	response := runNodePoolsModifier(t, planned, state, false)
	if diags := response.PlanValue.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	// A configured pool_config differs from state and so replaces the cluster,
	// and the values that configuration sets are never overwritten.
	if !response.RequiresReplace {
		t.Fatal("a changed pool_config attribute must replace the cluster")
	}

	// An omitted engine_roles is unknown in the plan, and an unknown attribute
	// of a known pool_config is open too. Both come from state.
	configured["engine_roles"] = types.SetUnknown(types.StringType)
	configured["pool_config"] = types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": types.StringUnknown()})
	response = runNodePoolsModifier(t, testPools(types.ObjectValueMust(nodePoolAttrTypes, configured)), testPools(identifiedPool("data", "id-data", 2, 50)), false)
	if diags := response.PlanValue.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode: %v", diags)
	}
	if attributeString(got[0].PoolConfig, "attribute").ValueString() != "data-tier" {
		t.Fatalf("an unknown attribute inside a known pool_config must come from state, got %s", got[0].PoolConfig)
	}
	if got[0].EngineRoles.IsUnknown() || got[0].PartitionCount.ValueInt64() != 1 {
		t.Fatalf("omitted attributes must come from state, got engine_roles=%s partition_count=%s", got[0].EngineRoles, got[0].PartitionCount)
	}
}

func TestFillPoolConfig(t *testing.T) {
	t.Parallel()

	config := func(value types.String) types.Object {
		return types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": value})
	}
	prior := config(types.StringValue("hot"))
	tests := []struct {
		name    string
		planned types.Object
		prior   types.Object
		want    types.Object
	}{
		{name: "unknown object", planned: types.ObjectUnknown(poolConfigAttrTypes), prior: prior, want: prior},
		{name: "unknown attribute", planned: config(types.StringUnknown()), prior: prior, want: prior},
		{name: "configured attribute", planned: config(types.StringValue("cold")), prior: prior, want: config(types.StringValue("cold"))},
		{name: "null planned stays null", planned: types.ObjectNull(poolConfigAttrTypes), prior: prior, want: types.ObjectNull(poolConfigAttrTypes)},
		{name: "null prior leaves the plan", planned: config(types.StringUnknown()), prior: types.ObjectNull(poolConfigAttrTypes), want: config(types.StringUnknown())},
	}
	for _, tt := range tests {
		if got := fillPoolConfig(tt.planned, tt.prior); !got.Equal(tt.want) {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
	}
}
