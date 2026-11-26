package selector

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	dbaaslookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// VolumeTypeAttribute returns the required volume type selector attribute.
func VolumeTypeAttribute(description string) schema.SingleNestedAttribute {
	return required(selector.Attribute(description, map[string]schema.Attribute{
		"name": schema.StringAttribute{Optional: true, Description: "Exact DBaaS volume type name in the region."},
	}))
}

// VolumeTypeAttributeTypes returns the object attribute types of the volume
// type selector.
func VolumeTypeAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(VolumeTypeAttribute(""))
}

// VolumeTypeFilter converts the known name criterion into a lookup filter in
// the region of the DB instance.
func VolumeTypeFilter(value types.Object, field string, regionID core.UUID) (dbaaslookup.VolumeTypeFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	if diags.HasError() {
		return dbaaslookup.VolumeTypeFilter{}, diags
	}
	return dbaaslookup.VolumeTypeFilter{RegionID: &regionID, Name: selector.Text(criterion(value, "name"))}, diags
}

// ResolveVolumeType selects exactly one volume type for the criteria.
func ResolveVolumeType(ctx context.Context, value types.Object, field string, regionID core.UUID, finder *dbaaslookup.VolumeTypeFinder) (dbaassdk.DBaaSRegionVolumeTypeSchema, diag.Diagnostics) {
	filter, diags := VolumeTypeFilter(value, field, regionID)
	if diags.HasError() {
		return dbaassdk.DBaaSRegionVolumeTypeSchema{}, diags
	}
	volumeType, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve DBaaS volume type", fmt.Sprintf("%s: %s", field, err))
	}
	return volumeType, diags
}
