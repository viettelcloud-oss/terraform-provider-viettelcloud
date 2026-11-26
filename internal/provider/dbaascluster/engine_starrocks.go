package dbaascluster

// engine_starrocks.go owns the StarRocks rules of the db_cluster resource: no
// topology choice, the fe and be node pools, and no backups. It also covers SSL,
// which the backend always disables, and the engine_config of the create body.
//
// The fe and be pools differ in size, so the backend rejects the flat body and
// topology_config and requires node_pools with engine_config. The backend
// checks the pool names and the replica counts that each pool allows.

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type starRocksDriver struct{}

func (starRocksDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	diags.Append(rejectTopology(engineStarRocks, cfg)...)
	diags.Append(rejectPITR(engineStarRocks, cfg)...)
	diags.Append(rejectAutoBackup(engineStarRocks, cfg)...)
	diags.Append(rejectEngineConfig(engineStarRocks, cfg)...)
	diags.Append(rejectOpenSearchSettings(engineStarRocks, cfg)...)
	diags.Append(validatePoolStorage(cfg, true)...)

	// The backend forces SSL off and overrides a true value without an error,
	// which Terraform reports as an inconsistent result.
	if configured(cfg.EnableSSL) && cfg.EnableSSL.ValueBool() {
		diags.AddAttributeError(path.Root("enable_ssl"), "SSL is always disabled", "The starrocks engine always disables SSL. Remove enable_ssl or set it to false.")
	}
	return diags
}

func (starRocksDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	body.NodePools = &pools
	// StarRocks has no engine settings, but the backend requires the field.
	body.EngineConfig = core.NullableFromPtr(&map[string]any{})
	return diags
}
