package dbaascluster

// engine_memcached.go owns the Memcached rules of the db_cluster resource: no
// topology choice, node pools without storage, no backups, and the
// engine_config that the create body requires.
//
// The backend validates the mode settings and the pools they need, such as
// the mcrouter pool of the mcrouter mode, so this driver checks only the shape
// of the configuration.

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type memcachedDriver struct{}

func (memcachedDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	diags.Append(rejectTopology(engineMemcached, cfg)...)
	diags.Append(rejectPITR(engineMemcached, cfg)...)
	diags.Append(rejectOpenSearchSettings(engineMemcached, cfg)...)
	diags.Append(validatePoolStorage(cfg, false)...)

	diags.Append(rejectAutoBackup(engineMemcached, cfg)...)
	return diags
}

func (memcachedDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	body.NodePools = &pools

	// The backend requires engine_config and applies its defaults to the
	// settings that it leaves out.
	settings := map[string]any{}
	if configured(in.Config.EngineConfig) {
		attributes := in.Config.EngineConfig.Attributes()
		if mode, ok := attributes["mode"].(types.String); ok && configured(mode) {
			settings["mode"] = strings.TrimSpace(mode.ValueString())
		}
		if auth, ok := attributes["auth_enabled"].(types.Bool); ok && configured(auth) {
			settings["auth_enabled"] = auth.ValueBool()
		}
		if routing, ok := attributes["mcrouter_routing_type"].(types.String); ok && configured(routing) {
			settings["mcrouter_routing_type"] = strings.TrimSpace(routing.ValueString())
		}
	}
	body.EngineConfig = core.NullableFromPtr(&settings)
	return diags
}
