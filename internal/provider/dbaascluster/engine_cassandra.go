package dbaascluster

// engine_cassandra.go owns the Cassandra rules of the db_cluster resource: no
// topology choice, SSL that the backend always disables, and the node_pools
// create body.
//
// The backend accepts either the deprecated flat body with topology_config or
// node_pools with engine_config. Cassandra has no engine settings, so the
// driver sends node_pools with an empty engine_config.

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type cassandraDriver struct{}

func (cassandraDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	diags.Append(rejectTopology(engineCassandra, cfg)...)
	diags.Append(rejectPITR(engineCassandra, cfg)...)
	diags.Append(rejectEngineConfig(engineCassandra, cfg)...)
	diags.Append(validateRetention(engineCassandra, cfg, retentionMaxBackups)...)
	diags.Append(rejectOpenSearchSettings(engineCassandra, cfg)...)
	diags.Append(validatePoolStorage(cfg, true)...)

	// The backend forces SSL off and overrides a true value without an error,
	// which Terraform reports as an inconsistent result.
	if configured(cfg.EnableSSL) && cfg.EnableSSL.ValueBool() {
		diags.AddAttributeError(path.Root("enable_ssl"), "SSL is always disabled", "The cassandra engine always disables SSL. Remove enable_ssl or set it to false.")
	}
	return diags
}

func (cassandraDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	body.NodePools = &pools
	body.EngineConfig = core.NullableFromPtr(&map[string]any{})
	return diags
}
