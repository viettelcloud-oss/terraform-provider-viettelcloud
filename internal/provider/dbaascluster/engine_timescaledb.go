package dbaascluster

// engine_timescaledb.go owns the TimescaleDB rules of the db_cluster resource:
// no topology choice, one client-named node pool, backups kept by age in days,
// and the node_pools create body.
//
// The backend needs neither topology_config nor engine_config for TimescaleDB,
// so the create body sends only node_pools. The backend checks the pool count,
// rejects public access, and requires PITR together with automatic backup.

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type timescaleDBDriver struct{}

func (timescaleDBDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	diags.Append(rejectTopology(engineTimescaleDB, cfg)...)
	diags.Append(rejectEngineConfig(engineTimescaleDB, cfg)...)
	diags.Append(rejectOpenSearchSettings(engineTimescaleDB, cfg)...)
	diags.Append(validatePoolStorage(cfg, true)...)
	diags.Append(validateRetention(engineTimescaleDB, cfg, retentionDays)...)
	return diags
}

func (timescaleDBDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	body.NodePools = &pools
	return diags
}
