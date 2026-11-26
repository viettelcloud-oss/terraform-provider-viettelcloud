package dbaascluster

// engine_opensearch.go owns the OpenSearch rules of the db_cluster resource:
// no topology choice, one or more client-named node pools, plugins, and the
// node_pools create body.
//
// OpenSearch cannot use the flat create body: its pools differ in flavor and
// disk, so the backend requires node_pools and rejects the flat fields.

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type openSearchDriver struct{}

func (openSearchDriver) validate(cfg engineConfig) diag.Diagnostics {
	diags := validateCommon(cfg)
	diags.Append(rejectTopology(engineOpenSearch, cfg)...)
	diags.Append(rejectPITR(engineOpenSearch, cfg)...)
	diags.Append(rejectEngineConfig(engineOpenSearch, cfg)...)
	diags.Append(validateRetention(engineOpenSearch, cfg, retentionMaxBackups)...)
	diags.Append(validatePoolStorage(cfg, true)...)

	// The backend forces these two settings and overrides a conflicting value
	// without an error, which Terraform reports as an inconsistent result.
	if configured(cfg.EnableSSL) && !cfg.EnableSSL.ValueBool() {
		diags.AddAttributeError(path.Root("enable_ssl"), "SSL is always enabled", "The opensearch engine always enables SSL. Remove enable_ssl or set it to true.")
	}
	if configured(cfg.KeepBackup) && cfg.KeepBackup.ValueBool() {
		diags.AddAttributeError(path.Root("keep_backup"), "Backups are never kept", "The opensearch engine always deletes its backups with the cluster. Remove keep_backup or set it to false.")
	}
	diags.Append(rejectTrimmedDuplicates(cfg)...)
	return diags
}

// rejectTrimmedDuplicates reports two plugins, or two engine roles of a pool,
// that differ only by outer whitespace. The request trims each value, so both
// name the same plugin or role, and the cluster would hold one value where the
// configuration holds two. A value that is unknown at plan is not checked.
func rejectTrimmedDuplicates(cfg engineConfig) diag.Diagnostics {
	var diags diag.Diagnostics
	if configured(cfg.Plugins) {
		names := make([]types.String, 0, len(cfg.Plugins.Elements()))
		for _, element := range cfg.Plugins.Elements() {
			if object, ok := element.(types.Object); ok {
				names = append(names, attributeString(object, "name"))
			}
		}
		if duplicate, ok := trimmedDuplicate(names); ok {
			diags.AddAttributeError(path.Root("plugins"), "Duplicate plugin",
				fmt.Sprintf("plugins[*].name %q appears more than once after outer whitespace is removed. List each plugin once.", duplicate))
		}
	}
	for i, pool := range cfg.Pools {
		if !configured(pool.EngineRoles) {
			continue
		}
		roles := make([]types.String, 0, len(pool.EngineRoles.Elements()))
		for _, element := range pool.EngineRoles.Elements() {
			if role, ok := element.(types.String); ok {
				roles = append(roles, role)
			}
		}
		if duplicate, ok := trimmedDuplicate(roles); ok {
			diags.AddAttributeError(path.Root("node_pools").AtListIndex(i).AtName("engine_roles"), "Duplicate engine role",
				fmt.Sprintf("engine_roles holds %q more than once after outer whitespace is removed. List each role once.", duplicate))
		}
	}
	return diags
}

// trimmedDuplicate returns the first known value that equals an earlier known
// value once outer whitespace is removed.
func trimmedDuplicate(values []types.String) (string, bool) {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !configured(value) {
			continue
		}
		key := strings.TrimSpace(value.ValueString())
		if seen[key] {
			return key, true
		}
		seen[key] = true
	}
	return "", false
}

func (openSearchDriver) fillCreateBody(in engineCreateInput, body *dbaasclustersdk.DBClusterCreateSchema) diag.Diagnostics {
	pools, diags := nodePoolsBody(in)
	if diags.HasError() {
		return diags
	}
	body.NodePools = &pools

	if configured(in.Config.Plugins) {
		plugins := make([]dbaasclustersdk.DBClusterPluginSelectionSchema, 0, len(in.Config.Plugins.Elements()))
		for _, element := range in.Config.Plugins.Elements() {
			object, ok := element.(types.Object)
			if !ok {
				continue
			}
			if name := attributeString(object, "name"); configured(name) {
				plugins = append(plugins, dbaasclustersdk.DBClusterPluginSelectionSchema{Name: strings.TrimSpace(name.ValueString())})
			}
		}
		body.Plugins = &plugins
	}
	return diags
}
