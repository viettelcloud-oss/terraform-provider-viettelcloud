package blockstorage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
)

type VolumeModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Size        types.Int64  `tfsdk:"size"`
	Status      types.String `tfsdk:"status"`
	Bootable    types.Bool   `tfsdk:"bootable"`
	Encrypted   types.Bool   `tfsdk:"encrypted"`
	ZoneID      types.String `tfsdk:"zone_id"`
	VolumeType  types.String `tfsdk:"volume_type"`
	ProjectID   types.String `tfsdk:"project_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
	IOPS        types.Int64  `tfsdk:"iops"`
}

// VolumeResourceModel references the zone as an object, while the data source
// keeps filtering by a plain zone name.
type VolumeResourceModel struct {
	VolumeModel
	Zone       types.Object `tfsdk:"zone"`
	CreateFrom types.Object `tfsdk:"create_from"`
}

type VolumeDataSourceModel struct {
	VolumeModel
	Zone types.String `tfsdk:"zone"`
}

type VolumeCreateFromModel struct {
	SourceType  types.String `tfsdk:"source_type"`
	Image       types.Object `tfsdk:"image"`
	CustomImage types.Object `tfsdk:"custom_image"`
	Snapshot    types.Object `tfsdk:"snapshot"`
	Backup      types.Object `tfsdk:"backup"`
	VolumeType  types.Object `tfsdk:"volume_type"`
}

// volumeSource is create_from with every ID reference reduced to its ID, which
// is what validation, requests, and plan checks read. The image and volume type
// selectors stay objects because they combine several criteria.
type volumeSource struct {
	SourceType    types.String
	Image         types.Object
	CustomImageID types.String
	SnapshotID    types.String
	BackupID      types.String
	VolumeType    types.Object
}

const (
	volumeSourceTypeEmpty       = "empty"
	volumeSourceTypeImage       = "image"
	volumeSourceTypeCustomImage = "custom_image"
	volumeSourceTypeSnapshot    = "snapshot"
	volumeSourceTypeBackup      = "backup"
)

type volumeOrigin struct {
	sourceType string
	attribute  string
	value      types.String
}

// volumeSourceCriteria maps each create_from ID reference to the criterion that
// selects the referenced object.
var volumeSourceCriteria = map[string]string{
	"custom_image": reference.ID,
	"snapshot":     reference.ID,
	"backup":       reference.ID,
}

func volumeCreateFromAttributeTypes() map[string]attr.Type {
	attributeTypes := map[string]attr.Type{
		"source_type": types.StringType,
		"image":       types.ObjectType{AttrTypes: serverselector.ImageAttributeTypes()},
		"volume_type": types.ObjectType{AttrTypes: blockstorageselector.VolumeTypeAttributeTypes()},
	}
	for attribute, criterion := range volumeSourceCriteria {
		attributeTypes[attribute] = reference.ObjectType(criterion)
	}
	return attributeTypes
}

// volumeCreateFromObject builds create_from from source_type and the ID of each
// ID reference, keyed by attribute name, and from the image and volume type
// selectors. A missing or null ID leaves its reference null.
func volumeCreateFromObject(values map[string]types.String, image, volumeType types.Object) (types.Object, diag.Diagnostics) {
	sourceType, ok := values["source_type"]
	if !ok {
		sourceType = types.StringNull()
	}
	attributes := map[string]attr.Value{"source_type": sourceType, "image": image, "volume_type": volumeType}
	for attribute, criterion := range volumeSourceCriteria {
		value, ok := values[attribute]
		if !ok {
			value = types.StringNull()
		}
		attributes[attribute] = reference.Value(criterion, value)
	}
	return types.ObjectValue(volumeCreateFromAttributeTypes(), attributes)
}

func populateVolumeModel(volume *blockstoragesdk.VolumeDetailSchema, state *VolumeModel) {
	if volume == nil {
		return
	}

	state.ID = types.StringValue(volume.Id.String())
	state.Name = types.StringValue(volume.Name)
	state.Size = types.Int64Value(int64(volume.Size))
	state.Status = types.StringValue(string(volume.Status))
	state.Bootable = types.BoolValue(volume.Bootable)
	state.ZoneID = types.StringValue(volume.Zone.Id.String())
	state.ProjectID = types.StringValue(volume.ProjectId.String())
	state.CreatedAt = types.StringValue(volume.CreatedAt.Format(time.RFC3339))
	state.UpdatedAt = types.StringValue(volume.UpdatedAt.Format(time.RFC3339))

	state.Description = types.StringNull()
	if volume.Description != nil {
		state.Description = types.StringValue(*volume.Description)
	}
	state.Encrypted = types.BoolNull()
	if volume.Encrypted != nil {
		state.Encrypted = types.BoolValue(*volume.Encrypted)
	}
	state.IOPS = types.Int64Null()
	if volume.Iops != nil {
		state.IOPS = types.Int64Value(int64(*volume.Iops))
	}
	state.VolumeType = types.StringNull()
	if volume.CreateFrom.VolumeType != nil {
		state.VolumeType = types.StringValue(volume.CreateFrom.VolumeType.Name)
	}
}

// populateVolumeResourceState refreshes state from a volume response, nested
// create_from included. The top-level volume_type is read from that same
// object. Leaving create_from at its prior value would let an out-of-band
// retype produce a state whose two views of the volume type disagree while the
// plan stays clean.
func populateVolumeResourceState(ctx context.Context, volume *blockstoragesdk.VolumeDetailSchema, state *VolumeResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if volume == nil {
		return diags
	}

	configuredName := state.Name
	configuredSource := state.CreateFrom
	populateVolumeModel(volume, &state.VolumeModel)
	state.Zone = reference.Value(reference.Name, types.StringValue(volume.Zone.Name))
	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == volume.Name {
		state.Name = configuredName
	}
	createFrom, sourceDiags := volumeCreateFromState(ctx, volume, configuredSource)
	diags.Append(sourceDiags...)
	if diags.HasError() {
		return diags
	}
	state.CreateFrom = createFrom
	return diags
}

func volumeCreateFromState(ctx context.Context, volume *blockstoragesdk.VolumeDetailSchema, configured types.Object) (types.Object, diag.Diagnostics) {
	attributeTypes := volumeCreateFromAttributeTypes()
	prior, diags := volumeCreateFromModel(ctx, configured)
	if diags.HasError() {
		return types.ObjectNull(attributeTypes), diags
	}
	priorSourceType := strings.TrimSpace(prior.SourceType.ValueString())

	origins := volumeOrigins(volume)
	var selected *volumeOrigin
	// retainPrior records that the backend reported no origin at all while a
	// source is already known. "Not reported" is not evidence of "created
	// empty": a deleted snapshot or backup stops being reported while the
	// volume it produced lives on. Every create_from field that names a source
	// requires replacement, so recording "empty" would plan the destruction of
	// a healthy volume that no surviving source could rebuild.
	retainPrior := false
	switch {
	case len(origins) == 0:
		// Only a volume with nothing recorded at all, such as a first import,
		// is genuinely empty.
		retainPrior = priorSourceType != "" && priorSourceType != volumeSourceTypeEmpty
	case len(origins) == 1:
		selected = &origins[0]
	default:
		for i := range origins {
			if origins[i].sourceType == priorSourceType {
				selected = &origins[i]
			}
		}
		if selected == nil {
			reported := make([]string, 0, len(origins))
			for _, origin := range origins {
				reported = append(reported, origin.sourceType)
			}
			// Read is handed prior state, never configuration, so editing
			// create_from in the configuration cannot resolve this. The source
			// type has to arrive with the import ID itself.
			diags.AddError(
				"Ambiguous volume source",
				fmt.Sprintf(
					"Volume %s reports several origins (%s) and nothing in state selects one, so the source it was "+
						"created from cannot be determined. Import the volume with a composite ID that names the "+
						"intended origin, for example \"%s,%s\". Changing create_from in the configuration has no "+
						"effect here, because refreshing a volume reads state rather than configuration.",
					volume.Id, strings.Join(reported, ", "), volume.Id, reported[0],
				),
			)
			return types.ObjectNull(attributeTypes), diags
		}
	}

	values := map[string]types.String{"source_type": types.StringValue(volumeSourceTypeEmpty)}
	image := types.ObjectNull(serverselector.ImageAttributeTypes())
	volumeType := types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes())
	sourceType := volumeSourceTypeEmpty
	switch {
	case selected != nil:
		sourceType = selected.sourceType
		values["source_type"] = types.StringValue(selected.sourceType)
		if selected.sourceType == volumeSourceTypeImage {
			value, valueDiags := selector.Refresh(prior.Image, serverselector.ImageAttributeTypes(), &volume.CreateFrom.Image.Id, volume.CreateFrom.Image.Name)
			diags.Append(valueDiags...)
			image = value
		} else {
			values[selected.attribute] = selected.value
		}
	case retainPrior:
		sourceType = priorSourceType
		values["source_type"] = types.StringValue(priorSourceType)
		if priorSourceType == volumeSourceTypeImage {
			image = prior.Image
		} else if attribute, value, ok := priorVolumeSourceID(prior, priorSourceType); ok {
			values[attribute] = value
		}
	}
	// A snapshot source rejects create_from.volume_type in configuration because
	// the snapshot picks the type, so the response value stays out of state.
	if volume.CreateFrom.VolumeType != nil && sourceType != volumeSourceTypeSnapshot {
		value, valueDiags := selector.Refresh(prior.VolumeType, blockstorageselector.VolumeTypeAttributeTypes(), &volume.CreateFrom.VolumeType.Id, volume.CreateFrom.VolumeType.Name)
		diags.Append(valueDiags...)
		volumeType = value
	}
	preserveConfiguredVolumeSource(values, prior)

	result, objectDiags := volumeCreateFromObject(values, image, volumeType)
	diags.Append(objectDiags...)
	if diags.HasError() {
		return types.ObjectNull(attributeTypes), diags
	}
	return result, diags
}

// volumeOrigins lists every source the response reports for the volume.
// NestedVolumeOriginSchema declares each origin field as independent and
// optional, and carries no discriminator. More than one can therefore be
// populated at once: a snapshot of a volume that itself came from an image.
func volumeOrigins(volume *blockstoragesdk.VolumeDetailSchema) []volumeOrigin {
	var origins []volumeOrigin
	if volume.CreateFrom.Image != nil {
		origins = append(origins, volumeOrigin{volumeSourceTypeImage, "image", types.StringValue(volume.CreateFrom.Image.Name)})
	}
	if volume.CreateFrom.CustomImage != nil {
		origins = append(origins, volumeOrigin{volumeSourceTypeCustomImage, "custom_image", types.StringValue(volume.CreateFrom.CustomImage.Id.String())})
	}
	if volume.CreateFrom.Snapshot != nil {
		origins = append(origins, volumeOrigin{volumeSourceTypeSnapshot, "snapshot", types.StringValue(volume.CreateFrom.Snapshot.Id.String())})
	}
	if volume.CreateFrom.Backup != nil {
		origins = append(origins, volumeOrigin{volumeSourceTypeBackup, "backup", types.StringValue(volume.CreateFrom.Backup.Id.String())})
	}
	return origins
}

func priorVolumeSourceID(prior volumeSource, sourceType string) (string, types.String, bool) {
	switch sourceType {
	case volumeSourceTypeCustomImage:
		return "custom_image", prior.CustomImageID, true
	case volumeSourceTypeSnapshot:
		return "snapshot", prior.SnapshotID, true
	case volumeSourceTypeBackup:
		return "backup", prior.BackupID, true
	default:
		return "", types.StringNull(), false
	}
}

func preserveConfiguredVolumeSource(values map[string]types.String, prior volumeSource) {
	keep := func(attribute string, configured types.String, equal func(configured types.String, refreshed string) bool) {
		refreshed, ok := values[attribute]
		if !ok || refreshed.IsNull() || configured.IsNull() || configured.IsUnknown() {
			return
		}
		if equal(configured, refreshed.ValueString()) {
			values[attribute] = configured
		}
	}
	exact := func(configured types.String, refreshed string) bool {
		return strings.TrimSpace(configured.ValueString()) == refreshed
	}
	// An ID is the same ID however it is spelled, so it is compared as a UUID
	// rather than as text.
	sameUUID := func(configured types.String, refreshed string) bool {
		parsed, err := core.ParseUUID(refreshed)
		return err == nil && compare.SameUUID(configured, parsed)
	}

	keep("source_type", prior.SourceType, exact)
	keep("custom_image", prior.CustomImageID, sameUUID)
	keep("snapshot", prior.SnapshotID, sameUUID)
	keep("backup", prior.BackupID, sameUUID)
}

// preservedUUID keeps the configured spelling of id, such as upper case or
// braces, when it names the same object, and the backend's otherwise.
func preservedUUID(configured types.String, id core.UUID) types.String {
	if compare.SameUUID(configured, id) {
		return configured
	}
	return types.StringValue(id.String())
}
