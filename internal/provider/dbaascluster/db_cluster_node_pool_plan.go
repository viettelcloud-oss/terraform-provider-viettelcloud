package dbaascluster

// db_cluster_node_pool_plan.go owns the plan rules for node_pools: when a
// change replaces the cluster, and which prior values the plan keeps. A node
// pool can change only by replacing the cluster, because this resource does
// not scale or resize pools in place. The one exception is a request for a disk
// that is not larger than the volume in use.

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
)

// nodePoolsRequireReplace replaces the cluster when a configured node pool
// attribute changes. A plain RequiresReplace on the list is wrong here. Every
// planned change marks Computed pool attributes such as used_disk_size unknown,
// so the list would differ from state on every update.
type nodePoolsRequireReplace struct{}

var _ planmodifier.List = nodePoolsRequireReplace{}

func (nodePoolsRequireReplace) Description(context.Context) string {
	return "Replaces the resource when a node pool is added, removed or resized."
}

func (m nodePoolsRequireReplace) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (nodePoolsRequireReplace) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	// Creation and destruction never replace.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if req.PlanValue.IsNull() || req.PlanValue.IsUnknown() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}

	// A pool that comes whole from a value this apply computes cannot be the
	// pool in state, as with the Framework's RequiresReplace.
	for _, element := range req.PlanValue.Elements() {
		if element.IsUnknown() {
			resp.RequiresReplace = true
			return
		}
	}

	var planned, current []nodePoolModel
	resp.PlanValue = req.PlanValue
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planned, false)...)
	resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &current, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if nodePoolsDiffer(planned, current) {
		resp.RequiresReplace = true
		return
	}

	filled, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: nodePoolAttrTypes}, fillPlannedPools(planned, current))
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		resp.PlanValue = filled
	}
}

// fillPlannedPools gives each planned pool the values that the prior state
// holds for the pool of the same name, wherever the plan left them unknown.
// The Framework's UseStateForUnknown copies by list index. The backend reports
// pools in its own order, so a copy by index can give a pool the id and node
// count of another pool.
func fillPlannedPools(planned, current []nodePoolModel) []nodePoolModel {
	currentByName := make(map[string]nodePoolModel, len(current))
	for _, pool := range current {
		currentByName[poolKey(pool.Name)] = pool
	}

	filled := make([]nodePoolModel, len(planned))
	for i, pool := range planned {
		prior, ok := currentByName[poolKey(pool.Name)]
		if !ok {
			filled[i] = pool
			continue
		}
		if pool.ID.IsUnknown() {
			pool.ID = prior.ID
		}
		if pool.Role.IsUnknown() {
			pool.Role = prior.Role
		}
		if pool.NodeCount.IsUnknown() {
			pool.NodeCount = prior.NodeCount
		}
		if pool.PartitionCount.IsUnknown() {
			pool.PartitionCount = prior.PartitionCount
		}
		if pool.EngineRoles.IsUnknown() {
			pool.EngineRoles = prior.EngineRoles
		}
		pool.PoolConfig = fillPoolConfig(pool.PoolConfig, prior.PoolConfig)
		filled[i] = pool
	}
	return filled
}

// fillPoolConfig completes an unknown pool_config, or an unknown attribute
// inside a known one, from the prior value.
func fillPoolConfig(planned, prior types.Object) types.Object {
	if planned.IsUnknown() {
		return prior
	}
	if planned.IsNull() || prior.IsNull() || prior.IsUnknown() {
		return planned
	}
	if attribute := attributeString(planned, "attribute"); attribute.IsUnknown() {
		return types.ObjectValueMust(poolConfigAttrTypes, map[string]attr.Value{"attribute": attributeString(prior, "attribute")})
	}
	return planned
}

// nodePoolsDiffer compares pools by name, because the backend reports them in
// its own order. It compares only attributes that configuration sets: Computed
// attributes are the backend's to change.
func nodePoolsDiffer(planned, current []nodePoolModel) bool {
	if len(planned) != len(current) {
		return true
	}
	currentByName := make(map[string]nodePoolModel, len(current))
	for _, pool := range current {
		currentByName[poolKey(pool.Name)] = pool
	}

	for _, pool := range planned {
		// An unknown name depends on a value that this apply computes, and so
		// it cannot be the name in state.
		if pool.Name.IsUnknown() {
			return true
		}
		prior, ok := currentByName[poolKey(pool.Name)]
		if !ok {
			return true
		}
		if configuredValueDiffers(pool.Replicas, prior.Replicas) ||
			diskSizeNeedsReplace(pool.DiskSize, prior.DiskSize, prior.ActualDiskSize) ||
			configuredStringDiffers(attributeString(pool.ResourceConfig, "name"), attributeString(prior.ResourceConfig, "name")) ||
			configuredStringDiffers(attributeString(pool.StorageClass, "name"), attributeString(prior.StorageClass, "name")) {
			return true
		}
		// These three are Optional and Computed. An unknown planned value is
		// the Framework's placeholder for an omitted one, and omission keeps
		// the prior value.
		if optionalValueDiffers(pool.PartitionCount, prior.PartitionCount) ||
			optionalSetDiffers(pool.EngineRoles, prior.EngineRoles) ||
			optionalStringDiffers(attributeString(pool.PoolConfig, "attribute"), attributeString(prior.PoolConfig, "attribute")) {
			return true
		}
	}
	return false
}

// poolKey returns the name that identifies a pool. The backend trims outer
// whitespace from a pool name, so a name that differs only by that whitespace
// names the same pool.
func poolKey(name types.String) string {
	return strings.TrimSpace(name.ValueString())
}

// diskSizeNeedsReplace reports whether the requested disk size needs a larger
// volume than the cluster has. A request that is not larger needs nothing,
// because a volume never shrinks and the backend can grow it on its own. The
// volume in use is the larger of the recorded request and the reported size.
// After the backend grows the disk, state holds a request below the size in
// use. After an import, state holds the size in use. A request between the two
// is recorded without touching the cluster.
func diskSizeNeedsReplace(planned, requested, actual types.Int64) bool {
	if planned.IsUnknown() {
		return true
	}
	if planned.IsNull() || requested.IsNull() || requested.IsUnknown() {
		return !planned.Equal(requested)
	}
	inUse := requested.ValueInt64()
	if !actual.IsNull() && !actual.IsUnknown() {
		inUse = max(inUse, actual.ValueInt64())
	}
	return planned.ValueInt64() > inUse
}

// configuredValueDiffers treats an unknown planned value as a change, as the
// Framework's RequiresReplace does.
func configuredValueDiffers(planned, prior attr.Value) bool {
	return planned.IsUnknown() || !planned.Equal(prior)
}

// configuredStringDiffers works like configuredValueDiffers, but a change of
// outer whitespace only is no change. The request trims the value, and state
// keeps the configured spelling.
func configuredStringDiffers(planned, prior types.String) bool {
	return planned.IsUnknown() || !planned.Equal(prior) && !compare.SameTrimmedString(planned, prior)
}

func optionalValueDiffers(planned, prior attr.Value) bool {
	return !planned.IsUnknown() && !planned.Equal(prior)
}

// optionalStringDiffers works like optionalValueDiffers, but a change of outer
// whitespace only is no change, for the reason given on
// configuredStringDiffers.
func optionalStringDiffers(planned, prior types.String) bool {
	return optionalValueDiffers(planned, prior) && !compare.SameTrimmedString(planned, prior)
}

// optionalSetDiffers works like optionalStringDiffers for a set of strings.
func optionalSetDiffers(planned, prior types.Set) bool {
	return optionalValueDiffers(planned, prior) && !sameTrimmedSet(planned, prior, stringElement)
}
