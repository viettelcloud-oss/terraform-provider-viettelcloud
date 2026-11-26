package filestorage

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type FilesystemSnapshotModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	FilesystemName types.String `tfsdk:"filesystem_name"`
	FromPolicy     types.Bool   `tfsdk:"from_policy"`
	Status         types.String `tfsdk:"status"`
	Size           types.Int64  `tfsdk:"size"`
	OwnerID        types.String `tfsdk:"owner_id"`
	ProjectID      types.String `tfsdk:"project_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

// FilesystemSnapshotResourceModel references the source filesystem as an
// object, while the data source keeps filtering by a plain filesystem_id.
type FilesystemSnapshotResourceModel struct {
	FilesystemSnapshotModel
	Filesystem types.Object `tfsdk:"filesystem"`
}

type FilesystemSnapshotDataSourceModel struct {
	FilesystemSnapshotModel
	FilesystemID types.String `tfsdk:"filesystem_id"`
}

func populateFilesystemSnapshotModel(snapshot *filestoragesdk.FilesystemSnapshotSchema, state *FilesystemSnapshotModel) {
	state.ID = types.StringValue(snapshot.Id.String())
	state.Name = types.StringValue(snapshot.Name)
	state.FilesystemName = types.StringValue(snapshot.Filesystem.Name)
	state.FromPolicy = types.BoolValue(snapshot.FromPolicy)
	state.OwnerID = types.StringValue(snapshot.OwnerId.String())
	state.ProjectID = types.StringValue(snapshot.ProjectId.String())
	state.CreatedAt = types.StringValue(snapshot.CreatedAt.Format(time.RFC3339))
	state.UpdatedAt = types.StringValue(snapshot.UpdatedAt.Format(time.RFC3339))
	state.Description = types.StringPointerValue(core.NullableToPtr(snapshot.Description))
	state.Size = types.Int64Value(int64(snapshot.Size))
	state.Status = types.StringValue(string(snapshot.Status))
}

// populateFilesystemSnapshotResourceState refreshes resource state from a
// snapshot response and keeps the configured spelling of inputs that the
// backend normalizes.
func populateFilesystemSnapshotResourceState(snapshot *filestoragesdk.FilesystemSnapshotSchema, state *FilesystemSnapshotResourceModel) {
	prior := *state
	populateFilesystemSnapshotModel(snapshot, &state.FilesystemSnapshotModel)
	preserveFilesystemSnapshotInputs(snapshot, &prior.FilesystemSnapshotModel, &state.FilesystemSnapshotModel)
	state.Filesystem = reference.Value(reference.ID, compare.PreservedUUID(reference.Get(prior.Filesystem, reference.ID), snapshot.Filesystem.Id))
}

func populateFilesystemSnapshotDataSourceState(snapshot *filestoragesdk.FilesystemSnapshotSchema, state *FilesystemSnapshotDataSourceModel) {
	populateFilesystemSnapshotModel(snapshot, &state.FilesystemSnapshotModel)
	state.FilesystemID = types.StringValue(snapshot.Filesystem.Id.String())
}

func populateFilesystemSnapshotPendingState(state *FilesystemSnapshotResourceModel, id core.UUID) {
	state.ID = types.StringValue(id.String())
	if state.Description.IsUnknown() {
		state.Description = types.StringNull()
	}
	state.FilesystemName = types.StringNull()
	state.FromPolicy = types.BoolNull()
	state.Status = types.StringNull()
	state.Size = types.Int64Null()
	state.OwnerID = types.StringNull()
	state.ProjectID = types.StringNull()
	state.CreatedAt = types.StringNull()
	state.UpdatedAt = types.StringNull()
}

func preserveFilesystemSnapshotInputs(snapshot *filestoragesdk.FilesystemSnapshotSchema, prior, state *FilesystemSnapshotModel) {
	if !prior.Name.IsNull() && !prior.Name.IsUnknown() && strings.TrimSpace(prior.Name.ValueString()) == snapshot.Name {
		state.Name = prior.Name
	}
	if description := core.NullableToPtr(snapshot.Description); description != nil && compare.SameTrimmed(prior.Description, *description) {
		state.Description = prior.Description
	}
}

func preserveFilesystemSnapshotFilters(snapshot *filestoragesdk.FilesystemSnapshotSchema, config, state *FilesystemSnapshotDataSourceModel) {
	if compare.SameUUID(config.ID, snapshot.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == snapshot.Name {
		state.Name = config.Name
	}
	state.FilesystemID = compare.PreservedUUID(config.FilesystemID, snapshot.Filesystem.Id)
	preserveFilesystemSnapshotInputs(snapshot, &config.FilesystemSnapshotModel, &state.FilesystemSnapshotModel)
}
