// Package selector defines the catalog selectors of dbaas-owned entities: the
// DB version, flavor, and volume type of a DB instance. It is a leaf package:
// it imports no service package.
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

// DBVersionCriterion names the entry that the DB version selector reports.
const DBVersionCriterion = "version"

// DBVersionAttribute returns the required DB version selector attribute.
func DBVersionAttribute(description string) schema.SingleNestedAttribute {
	return required(selector.Attribute(description, map[string]schema.Attribute{
		DBVersionCriterion: schema.StringAttribute{Optional: true, Description: "Exact DB version, such as \"16\"."},
	}))
}

// DBVersionAttributeTypes returns the object attribute types of the DB
// version selector.
func DBVersionAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(DBVersionAttribute(""))
}

// DBVersionFilter converts the known version criterion into a lookup filter
// in the database type and region of the DB instance.
func DBVersionFilter(value types.Object, field string, databaseType dbaassdk.DatabaseType, regionID core.UUID) (dbaaslookup.DBVersionFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	if diags.HasError() {
		return dbaaslookup.DBVersionFilter{}, diags
	}
	return dbaaslookup.DBVersionFilter{
		DatabaseType: &databaseType, RegionID: &regionID,
		Version: selector.Text(criterion(value, DBVersionCriterion)),
	}, diags
}

// ResolveDBVersion selects exactly one DB version for the criteria.
func ResolveDBVersion(ctx context.Context, value types.Object, field string, databaseType dbaassdk.DatabaseType, regionID core.UUID, finder *dbaaslookup.DBVersionFinder) (dbaassdk.DBaaSVersionSchema, diag.Diagnostics) {
	filter, diags := DBVersionFilter(value, field, databaseType, regionID)
	if diags.HasError() {
		return dbaassdk.DBaaSVersionSchema{}, diags
	}
	version, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve DB version", fmt.Sprintf("%s: %s", field, err))
	}
	return version, diags
}

// required makes a selector Required. A DB instance always names its
// catalog entries, unlike the optional selectors of servers and volumes.
func required(attribute schema.SingleNestedAttribute) schema.SingleNestedAttribute {
	attribute.Optional, attribute.Required = false, true
	return attribute
}

// criterion returns one string criterion of a known selector.
func criterion(value types.Object, name string) types.String {
	if text, ok := value.Attributes()[name].(types.String); ok {
		return text
	}
	return types.StringNull()
}
