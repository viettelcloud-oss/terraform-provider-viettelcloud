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

// FlavorAttribute returns the required flavor selector attribute.
func FlavorAttribute(description string) schema.SingleNestedAttribute {
	return required(selector.Attribute(description, map[string]schema.Attribute{
		"name": schema.StringAttribute{Optional: true, Description: "Exact flavor name in the region."},
	}))
}

// FlavorAttributeTypes returns the object attribute types of the flavor
// selector.
func FlavorAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(FlavorAttribute(""))
}

// FlavorFilter converts the known name criterion into a lookup filter in the
// region of the DB instance.
func FlavorFilter(value types.Object, field string, regionID core.UUID) (dbaaslookup.FlavorFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	if diags.HasError() {
		return dbaaslookup.FlavorFilter{}, diags
	}
	return dbaaslookup.FlavorFilter{RegionID: &regionID, Name: selector.Text(criterion(value, "name"))}, diags
}

// ResolveFlavor selects exactly one flavor for the criteria.
func ResolveFlavor(ctx context.Context, value types.Object, field string, regionID core.UUID, finder *dbaaslookup.FlavorFinder) (dbaassdk.DBaaSRegionFlavorSchema, diag.Diagnostics) {
	filter, diags := FlavorFilter(value, field, regionID)
	if diags.HasError() {
		return dbaassdk.DBaaSRegionFlavorSchema{}, diags
	}
	flavor, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve DBaaS flavor", fmt.Sprintf("%s: %s", field, err))
	}
	return flavor, diags
}
