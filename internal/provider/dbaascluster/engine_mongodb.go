package dbaascluster

// engine_mongodb.go owns the MongoDB rules of the db_cluster resource: the
// topology choice and the single node pool.
//
// MongoDB sends node_pools and an empty engine_config, like the other
// engines, plus topology. That field picks between the two MongoDB
// topologies, because engine_config has no setting for it.

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type mongoDriver struct{}

// mongoPoolNames maps a topology to the only node pool name it accepts. The
// backend takes the pool role from this name and rejects another name at
// apply, so the driver reports it at plan instead.
var mongoPoolNames = map[string]string{
	topologyMongoRs:       "mongo",
	topologyMongoSharding: "shard",
}

func (mongoDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	topologyPath := path.Root("topology")
	topology := strings.TrimSpace(cfg.Topology.ValueString())

	switch {
	case cfg.Topology.IsNull():
		diags.AddAttributeError(topologyPath, "Missing topology", "topology is required for the mongodb engine and must be mongo_rs or mongo_sharding.")
	case cfg.Topology.IsUnknown():
	default:
		if _, ok := mongoPoolNames[topology]; !ok {
			diags.AddAttributeError(topologyPath, "Invalid topology", fmt.Sprintf("topology must be mongo_rs or mongo_sharding for the mongodb engine, got %q.", cfg.Topology.ValueString()))
		}
	}

	diags.Append(rejectEngineConfig(engineMongoDB, cfg)...)
	diags.Append(validateRetention(engineMongoDB, cfg, retentionMaxBackups)...)
	diags.Append(rejectOpenSearchSettings(engineMongoDB, cfg)...)
	diags.Append(validatePoolStorage(cfg, true)...)

	// The backend forces SSL on for sharding and overrides a false value
	// without an error, which Terraform reports as an inconsistent result.
	sharding := configured(cfg.Topology) && topology == topologyMongoSharding
	if sharding && configured(cfg.EnableSSL) && !cfg.EnableSSL.ValueBool() {
		diags.AddAttributeError(path.Root("enable_ssl"), "SSL is always enabled", "The mongo_sharding topology always enables SSL. Remove enable_ssl or set it to true.")
	}

	if !cfg.PoolsKnown {
		return diags
	}
	if len(cfg.Pools) > 1 {
		diags.AddAttributeError(path.Root("node_pools"), "Too many node pools", "The mongodb engine takes exactly one node pool.")
	}
	for i, pool := range cfg.Pools {
		poolPath := path.Root("node_pools").AtListIndex(i)

		if want, ok := mongoPoolNames[topology]; ok && configured(pool.Name) && strings.TrimSpace(pool.Name.ValueString()) != want {
			diags.AddAttributeError(poolPath.AtName("name"), "Invalid node pool name", fmt.Sprintf("The %s topology names its node pool %q, got %q.", topology, want, pool.Name.ValueString()))
		}
		if !configured(cfg.Topology) {
			continue
		}
		switch topology {
		case topologyMongoSharding:
			if pool.PartitionCount.IsNull() {
				diags.AddAttributeError(poolPath.AtName("partition_count"), "Missing shard count", "partition_count is the number of shards and is required for the mongo_sharding topology.")
			}
		case topologyMongoRs:
			if configured(pool.PartitionCount) && pool.PartitionCount.ValueInt64() != 1 {
				diags.AddAttributeError(poolPath.AtName("partition_count"), "Unsupported shard count", "partition_count must be 1, or omitted, for the mongo_rs topology.")
			}
		}
	}
	return diags
}

func (mongoDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	topology := dbaasclustersdk.DBClusterTopologyKind(strings.TrimSpace(in.Config.Topology.ValueString()))
	body.NodePools = &pools
	body.EngineConfig = core.NullableFromPtr(&map[string]any{})
	body.Topology = core.NullableFromPtr(&topology)
	return diags
}
