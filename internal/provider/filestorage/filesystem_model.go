package filestorage

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	filestorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

var snapshotPolicyAttrTypes = map[string]attr.Type{
	"id":   types.StringType,
	"name": types.StringType,
}

// FilesystemModel holds the API response fields shared by both the resource
// and the data source.
type FilesystemModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	Size                types.Int64  `tfsdk:"size"`
	Protocol            types.String `tfsdk:"protocol"`
	Status              types.String `tfsdk:"status"`
	MountTargetAddress  types.String `tfsdk:"mount_target_address"`
	ZoneID              types.String `tfsdk:"zone_id"`
	RegionID            types.String `tfsdk:"region_id"`
	RegionName          types.String `tfsdk:"region_name"`
	VpcID               types.String `tfsdk:"vpc_id"`
	VpcName             types.String `tfsdk:"vpc_name"`
	SubnetCIDR          types.String `tfsdk:"subnet_cidr"`
	HasSnapshot         types.Bool   `tfsdk:"has_snapshot"`
	MigrationPercentage types.Int64  `tfsdk:"migration_percentage"`
	OwnerID             types.String `tfsdk:"owner_id"`
	ProjectID           types.String `tfsdk:"project_id"`
	SnapshotPolicy      types.Object `tfsdk:"snapshot_policy"`
	CreatedAt           types.String `tfsdk:"created_at"`
	UpdatedAt           types.String `tfsdk:"updated_at"`
}

// FilesystemResourceModel is the Terraform state model for the managed resource.
type FilesystemResourceModel struct {
	FilesystemModel
	ZoneName       types.String `tfsdk:"zone_name"`
	FilesystemType types.Object `tfsdk:"filesystem_type"`
	Subnet         types.Object `tfsdk:"subnet"`
	Snapshot       types.Object `tfsdk:"snapshot"`
}

// FilesystemDataSourceModel is the Terraform state model for the data source.
type FilesystemDataSourceModel struct {
	FilesystemModel
	Zone               types.String `tfsdk:"zone"`
	SubnetID           types.String `tfsdk:"subnet_id"`
	FilesystemTypeID   types.String `tfsdk:"filesystem_type_id"`
	FilesystemTypeName types.String `tfsdk:"filesystem_type_name"`
	SnapshotID         types.String `tfsdk:"snapshot_id"`
}

// populateFilesystemModel maps the API response to the shared model fields.
func populateFilesystemModel(api *filestoragepkg.FilesystemSchema, m *FilesystemModel) {
	m.ID = types.StringValue(api.Id.String())
	m.Name = types.StringValue(api.Name)
	m.Size = types.Int64Value(int64(api.Size))
	m.Protocol = types.StringValue(string(api.Protocol))
	m.Status = types.StringValue(string(api.Status))
	m.MountTargetAddress = types.StringValue(api.MountTargetAddress)
	m.ZoneID = types.StringValue(api.Zone.Id.String())
	m.RegionID = types.StringValue(api.Region.Id.String())
	m.RegionName = types.StringValue(api.Region.Name)
	m.VpcID = types.StringValue(api.Vpc.Id.String())
	m.VpcName = types.StringValue(api.Vpc.Name)
	m.SubnetCIDR = types.StringValue(api.Subnet.Cidr)
	m.ProjectID = types.StringValue(api.ProjectId.String())
	m.CreatedAt = types.StringValue(api.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(api.UpdatedAt.Format(time.RFC3339))

	m.Description = types.StringNull()
	if desc := core.NullableToPtr(api.Description); desc != nil {
		m.Description = types.StringValue(*desc)
	}

	m.OwnerID = types.StringNull()
	if ownerID := core.NullableToPtr(api.OwnerId); ownerID != nil {
		m.OwnerID = types.StringValue(ownerID.String())
	}

	m.HasSnapshot = types.BoolNull()
	if api.HasSnapshot != nil {
		m.HasSnapshot = types.BoolValue(*api.HasSnapshot)
	}

	m.MigrationPercentage = types.Int64Null()
	if pct := core.NullableToPtr(api.MigrationPercentage); pct != nil {
		m.MigrationPercentage = types.Int64Value(int64(*pct))
	}

	m.SnapshotPolicy = types.ObjectNull(snapshotPolicyAttrTypes)
	if sp := core.NullableToPtr(api.SnapshotPolicy); sp != nil {
		m.SnapshotPolicy = types.ObjectValueMust(snapshotPolicyAttrTypes, map[string]attr.Value{
			"id":   types.StringValue(sp.Id.String()),
			"name": types.StringValue(sp.Name),
		})
	}
}

// populateFilesystemResourceState refreshes the resource state from an API
// response, including the filesystem_type selector.
func populateFilesystemResourceState(
	ctx context.Context,
	api *filestoragepkg.FilesystemSchema,
	state *FilesystemResourceModel,
) diag.Diagnostics {
	prior := state.FilesystemModel
	populateFilesystemModel(api, &state.FilesystemModel)
	state.Name = compare.PreservedString(prior.Name, api.Name)
	state.Protocol = compare.PreservedString(prior.Protocol, string(api.Protocol))
	state.Description = normalizeFilesystemDescription(api, prior.Description)
	state.ZoneName = types.StringValue(api.Zone.Name)

	var ftID *core.UUID
	if ft := core.NullableToPtr(api.CreateFrom.FilesystemType); ft != nil {
		ftID = &ft.Id
	}
	refreshed, diags := selector.Refresh(state.FilesystemType, filestorageselector.FilesystemTypeAttributeTypes(), ftID, filesystemTypeName(api))
	if diags.HasError() {
		return diags
	}
	// A filesystem created from a snapshot plans an unknown selector. When the
	// backend does not report the type, the selector is null.
	if refreshed.IsUnknown() {
		refreshed = types.ObjectNull(filestorageselector.FilesystemTypeAttributeTypes())
	}
	state.FilesystemType = refreshed
	state.Snapshot = refreshFilesystemSnapshot(api, state.Snapshot)

	// Keep the configured subnet id when semantically equal to the response.
	if !compare.SameUUID(reference.Get(state.Subnet, reference.ID), api.Subnet.Id) {
		state.Subnet = reference.Value(reference.ID, types.StringValue(api.Subnet.Id.String()))
	}
	return diags
}

// normalizeFilesystemDescription treats a null and an empty backend
// description as the same "no description" value. The backend returns "" after
// create without a description and null after a clear. When the backend has no
// description, state keeps a blank prior plan or state value, and null
// otherwise. A prior value that differs from the backend value only by outer
// whitespace also keeps its spelling.
func normalizeFilesystemDescription(api *filestoragepkg.FilesystemSchema, prior types.String) types.String {
	if desc := core.NullableToPtr(api.Description); desc != nil && *desc != "" {
		return compare.PreservedString(prior, *desc)
	}
	if !prior.IsNull() && !prior.IsUnknown() && strings.TrimSpace(prior.ValueString()) == "" {
		return prior
	}
	return types.StringNull()
}

// refreshFilesystemSnapshot records the source snapshot that the backend
// reports in create_from.snapshot, and null when it reports none. A prior
// reference to the same snapshot keeps its configured spelling.
func refreshFilesystemSnapshot(api *filestoragepkg.FilesystemSchema, prior types.Object) types.Object {
	origin := core.NullableToPtr(api.CreateFrom.Snapshot)
	if origin == nil {
		return types.ObjectNull(reference.AttributeTypes(reference.ID))
	}
	if compare.SameUUID(reference.Get(prior, reference.ID), origin.Id) {
		return prior
	}
	return reference.Value(reference.ID, types.StringValue(origin.Id.String()))
}

// filesystemSnapshotID returns the source snapshot ID that the backend
// reports, or null.
func filesystemSnapshotID(api *filestoragepkg.FilesystemSchema) types.String {
	if origin := core.NullableToPtr(api.CreateFrom.Snapshot); origin != nil {
		return types.StringValue(origin.Id.String())
	}
	return types.StringNull()
}

func filesystemTypeName(api *filestoragepkg.FilesystemSchema) string {
	if ft := core.NullableToPtr(api.CreateFrom.FilesystemType); ft != nil {
		return ft.Name
	}
	return ""
}

// preserveFilesystemFilters keeps the configured spelling of each lookup
// criterion that names the same value as the response.
func preserveFilesystemFilters(api *filestoragepkg.FilesystemSchema, config FilesystemDataSourceModel, state *FilesystemDataSourceModel) {
	state.ID = compare.PreservedUUID(config.ID, api.Id)
	state.SubnetID = compare.PreservedUUID(config.SubnetID, api.Subnet.Id)
	state.Name = compare.PreservedString(config.Name, api.Name)
	state.Zone = compare.PreservedString(config.Zone, api.Zone.Name)
}

// populateFilesystemDataSourceState fills the data source model from the API
// response.
func populateFilesystemDataSourceState(api *filestoragepkg.FilesystemSchema, state *FilesystemDataSourceModel) {
	populateFilesystemModel(api, &state.FilesystemModel)
	state.Zone = types.StringValue(api.Zone.Name)
	state.SubnetID = types.StringValue(api.Subnet.Id.String())
	state.SnapshotID = filesystemSnapshotID(api)
	state.FilesystemTypeID = types.StringNull()
	state.FilesystemTypeName = types.StringNull()
	if ft := core.NullableToPtr(api.CreateFrom.FilesystemType); ft != nil {
		state.FilesystemTypeID = types.StringValue(ft.Id.String())
		state.FilesystemTypeName = types.StringValue(ft.Name)
	}
}
