// Package selector defines the catalog selectors of server-owned entities.
// Server and volume resources share them. It is a leaf package: it imports no
// service package, so both server and blockstorage can import it without an
// import cycle.
package selector

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// imageModel decodes the image selector object.
type imageModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	OS      types.String `tfsdk:"os"`
	Version types.String `tfsdk:"version"`
	Type    types.String `tfsdk:"type"`
}

// ImageAttribute returns the image selector schema attribute.
func ImageAttribute() schema.SingleNestedAttribute {
	return selector.Attribute(
		"Image selector. Supply at least one criterion. Creation requires exactly one image. Changed criteria keep the current image when it still matches.",
		map[string]schema.Attribute{
			"name":    schema.StringAttribute{Optional: true, Description: "Exact image name."},
			"os":      schema.StringAttribute{Optional: true, Description: "Exact operating system name."},
			"version": schema.StringAttribute{Optional: true, Description: "Exact operating system version. The provider filters this value locally."},
			"type":    schema.StringAttribute{Optional: true, Description: "Image type value from the SDK."},
		},
	)
}

// ImageAttributeTypes returns the object attribute types of the image selector.
func ImageAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(ImageAttribute())
}

// ImageFilter converts known image criteria into an image lookup filter.
func ImageFilter(ctx context.Context, value types.Object, field string) (serverlookup.ImageFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	var m imageModel
	var filter serverlookup.ImageFilter
	if diags.HasError() {
		return filter, diags
	}
	diags.Append(value.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return filter, diags
	}
	filter.Name, filter.OS, filter.Version = selector.Text(m.Name), selector.Text(m.OS), selector.Text(m.Version)
	if value := selector.Text(m.Type); value != nil {
		filter.Type = new(serversdk.ImageType(*value))
	}
	return filter, diags
}

// ResolveImage selects exactly one image for the criteria.
func ResolveImage(ctx context.Context, value types.Object, field string, resolve serverlookup.ImageResolveFunc) (serversdk.ImageSchema, diag.Diagnostics) {
	filter, diags := ImageFilter(ctx, value, field)
	if diags.HasError() {
		return serversdk.ImageSchema{}, diags
	}
	image, err := resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve image", fmt.Sprintf("%s: %s", field, err))
	}
	return image, diags
}
