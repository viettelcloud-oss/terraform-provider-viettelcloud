// Package selector defines the catalog selectors of filestorage-owned entities.
package selector

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	filestoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

type filesystemTypeModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	ZoneID   types.String `tfsdk:"zone_id"`
	ZoneName types.String `tfsdk:"zone_name"`
}

// FilesystemTypeAttribute returns the filesystem type selector schema
// attribute. The selector is Optional and Computed because a filesystem
// created from a snapshot takes its type from the backend. Each criterion is
// Computed too and keeps its prior value when omitted, so that an unrelated
// change does not plan an unknown criterion and a replacement.
func FilesystemTypeAttribute() schema.SingleNestedAttribute {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	attribute := selector.Attribute(
		"Filesystem type selector. Required unless snapshot is set, and must not be set with snapshot at creation. Supply at least one criterion. The provider selects exactly one enabled type matching all criteria. Changing the resolved ID replaces the filesystem. Omitting it keeps the current type.",
		map[string]schema.Attribute{
			"name":      schema.StringAttribute{Optional: true, Computed: true, Description: "Exact filesystem type name.", PlanModifiers: keep},
			"zone_id":   schema.StringAttribute{Optional: true, Computed: true, Description: "Zone ID (UUID) where the filesystem type is available.", PlanModifiers: keep},
			"zone_name": schema.StringAttribute{Optional: true, Computed: true, Description: "Zone name where the filesystem type is available.", PlanModifiers: keep},
		},
	)
	attribute.Computed = true
	attribute.PlanModifiers = []planmodifier.Object{objectplanmodifier.UseStateForUnknown()}
	return attribute
}

// FilesystemTypeAttributeTypes returns the object attribute types of the
// filesystem type selector.
func FilesystemTypeAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(FilesystemTypeAttribute())
}

// FilesystemTypeFilter converts known filesystem type criteria into a lookup
// filter.
func FilesystemTypeFilter(ctx context.Context, value types.Object, field string) (filestoragelookup.FilesystemTypeFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	var m filesystemTypeModel
	var filter filestoragelookup.FilesystemTypeFilter
	if diags.HasError() {
		return filter, diags
	}
	diags.Append(value.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return filter, diags
	}
	filter.Name = selector.Text(m.Name)
	filter.ZoneName = selector.Text(m.ZoneName)
	if zoneIDStr := selector.Text(m.ZoneID); zoneIDStr != nil {
		parsed, err := core.ParseUUID(*zoneIDStr)
		if err != nil {
			diags.AddError("Invalid zone_id", fmt.Sprintf("%s.zone_id must be a valid UUID: %s", field, err))
			return filter, diags
		}
		filter.ZoneID = &parsed
	}
	return filter, diags
}

// ResolveFilesystemType selects exactly one enabled filesystem type for the
// criteria in value.
func ResolveFilesystemType(
	ctx context.Context,
	value types.Object,
	field string,
	resolve filestoragelookup.FilesystemTypeResolveFunc,
) (filestoragepkg.FilesystemTypeSchema, diag.Diagnostics) {
	filter, diags := FilesystemTypeFilter(ctx, value, field)
	if diags.HasError() {
		return filestoragepkg.FilesystemTypeSchema{}, diags
	}
	ft, err := resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve filesystem type", fmt.Sprintf("%s: %s", field, err))
	}
	return ft, diags
}
