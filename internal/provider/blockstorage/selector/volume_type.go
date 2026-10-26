// Package selector defines the catalog selectors of blockstorage-owned
// entities. Server and volume resources share them. It is a leaf package: it
// imports no service package, so both server and blockstorage can import it
// without an import cycle.
package selector

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// volumeTypeModel decodes the volume type selector object.
type volumeTypeModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	DiskType          types.String `tfsdk:"disk_type"`
	Speed             types.String `tfsdk:"speed"`
	Multiattach       types.Bool   `tfsdk:"multiattach"`
	EncryptionKeySize types.String `tfsdk:"encryption_key_size"`
}

// VolumeTypeAttribute returns the volume type selector schema attribute.
func VolumeTypeAttribute() schema.SingleNestedAttribute {
	return selector.Attribute(
		"Volume type selector. Supply at least one criterion. New selections require exactly one enabled, up volume type in the requested zone. Changed criteria keep the current type when it still matches.",
		map[string]schema.Attribute{
			"name":                schema.StringAttribute{Optional: true, Description: "Exact volume type name."},
			"disk_type":           schema.StringAttribute{Optional: true, Description: "Disk type value from the SDK, such as ssd or hdd."},
			"speed":               schema.StringAttribute{Optional: true, Description: "Speed tier value from the SDK, such as 10k."},
			"multiattach":         schema.BoolAttribute{Optional: true, Description: "Whether the volume type permits multiple server attachments. False is an explicit criterion."},
			"encryption_key_size": schema.StringAttribute{Optional: true, Description: "Encryption key size value from the SDK, such as \"0\", \"128\", or \"256\"."},
		},
	)
}

// VolumeTypeAttributeTypes returns the object attribute types of the volume
// type selector.
func VolumeTypeAttributeTypes() map[string]attr.Type {
	return selector.AttributeTypes(VolumeTypeAttribute())
}

// VolumeTypeFilter converts known volume type criteria into a volume type
// lookup filter.
func VolumeTypeFilter(ctx context.Context, value types.Object, field string) (blockstoragelookup.VolumeTypeFilter, diag.Diagnostics) {
	diags := selector.Validate(value, field, true)
	var m volumeTypeModel
	var filter blockstoragelookup.VolumeTypeFilter
	if diags.HasError() {
		return filter, diags
	}
	diags.Append(value.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return filter, diags
	}
	filter.Name = selector.Text(m.Name)
	if value := selector.Text(m.DiskType); value != nil {
		filter.DiskType = new(blockstoragesdk.VolumeTypeDiskType(*value))
	}
	if value := selector.Text(m.Speed); value != nil {
		filter.Speed = new(blockstoragesdk.VolumeTypeSpeed(*value))
	}
	if value := selector.Text(m.EncryptionKeySize); value != nil {
		filter.EncryptionKeySize = new(blockstoragesdk.VolumeTypeEncryptKeySize(*value))
	}
	if !m.Multiattach.IsNull() {
		filter.Multiattach = m.Multiattach.ValueBoolPointer()
	}
	return filter, diags
}

// ResolveVolumeType selects exactly one volume type for the criteria in the
// zone and for the requested size.
func ResolveVolumeType(ctx context.Context, value types.Object, field string, zoneID *core.UUID, size int, resolve blockstoragelookup.VolumeTypeResolveFunc) (blockstoragesdk.VolumeTypeSchema, diag.Diagnostics) {
	filter, diags := VolumeTypeFilter(ctx, value, field)
	if diags.HasError() {
		return blockstoragesdk.VolumeTypeSchema{}, diags
	}
	volumeType, err := resolve(ctx, blockstoragelookup.VolumeTypeResolveRequest{Filter: filter, ZoneID: zoneID, RequestedSize: size})
	if err != nil {
		diags.AddError("Unable to resolve volume type", fmt.Sprintf("%s: %s", field, err))
	}
	return volumeType, diags
}
