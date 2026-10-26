package network

// region_reference.go owns the region reference that the VPC, security group,
// and Elastic IP resources share: its resolution to a region ID and its
// representation in state.

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// resolveRegionReference resolves the region reference to a region ID. It
// passes the configured name unmodified, so the region finder alone decides
// how to match it.
func resolveRegionReference(
	ctx context.Context,
	region types.Object,
	resolveRegion projectlookup.RegionResolveFunc,
) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := reference.Get(region, reference.Name)
	if name.IsNull() || name.IsUnknown() || strings.TrimSpace(name.ValueString()) == "" {
		diags.AddError("Missing region", "region.name must be configured and known.")
		return core.UUID{}, diags
	}

	value := name.ValueString()
	resolved, err := resolveRegion(ctx, projectlookup.RegionFilter{Name: &value})
	if err != nil {
		diags.AddError("Unable to resolve region", "region.name: "+err.Error())
		return core.UUID{}, diags
	}
	return resolved.Region.Id, diags
}

// regionReferenceState returns the region reference for state. It keeps the
// configured reference when its name differs from the backend name only by
// outer whitespace, otherwise Terraform reports an inconsistent result.
func regionReferenceState(configured types.Object, backendName string) types.Object {
	name := reference.Get(configured, reference.Name)
	if !name.IsNull() && !name.IsUnknown() && strings.TrimSpace(name.ValueString()) == backendName {
		return configured
	}
	return reference.Value(reference.Name, types.StringValue(backendName))
}
