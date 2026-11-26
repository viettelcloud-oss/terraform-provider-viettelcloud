package filestorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	filestoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &FilesystemSnapshotDataSource{}

type FilesystemSnapshotDataSource struct {
	client    *filestoragesdk.Client
	projectID core.UUID
}

type filesystemSnapshotCriteria struct{ id, name, filesystemID bool }

func NewFilesystemSnapshotDataSource() datasource.DataSource { return &FilesystemSnapshotDataSource{} }

func (d *FilesystemSnapshotDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem_snapshot"
}

func (d *FilesystemSnapshotDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud File Storage filesystem snapshot by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot ID (UUID)."},
			"name":            schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot name to filter by."},
			"filesystem_id":   schema.StringAttribute{Optional: true, Computed: true, Description: "Source filesystem ID (UUID) to filter by."},
			"description":     schema.StringAttribute{Computed: true, Description: "Snapshot description."},
			"filesystem_name": schema.StringAttribute{Computed: true, Description: "Source filesystem name."},
			"from_policy":     schema.BoolAttribute{Computed: true, Description: "Whether a policy created the snapshot."},
			"status":          schema.StringAttribute{Computed: true, Description: "Current snapshot status."},
			"size":            schema.Int64Attribute{Computed: true, Description: "Snapshot size in GiB."},
			"owner_id":        schema.StringAttribute{Computed: true, Description: "Owner ID (UUID)."},
			"project_id":      schema.StringAttribute{Computed: true, Description: "Project ID (UUID)."},
			"created_at":      schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339)."},
			"updated_at":      schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (d *FilesystemSnapshotDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.FileStorage, data.ProjectID
}

func (d *FilesystemSnapshotDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config FilesystemSnapshotDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	snapshot, diags := d.getSnapshot(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || snapshot == nil {
		return
	}
	state := config
	populateFilesystemSnapshotDataSourceState(snapshot, &state)
	preserveFilesystemSnapshotFilters(snapshot, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *FilesystemSnapshotDataSource) getSnapshot(ctx context.Context, config FilesystemSnapshotDataSourceModel) (*filestoragesdk.FilesystemSnapshotSchema, diag.Diagnostics) {
	criteria, diags := filesystemSnapshotLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (filesystemSnapshotCriteria{id: true}) {
		return d.getSnapshotByID(ctx, config.ID)
	}
	return d.getSnapshotByFilter(ctx, config, criteria)
}

func (d *FilesystemSnapshotDataSource) getSnapshotByID(ctx context.Context, idAttr types.String) (*filestoragesdk.FilesystemSnapshotSchema, diag.Diagnostics) {
	id, diags := parse.UUIDString(idAttr, "id")
	if diags.HasError() {
		return nil, diags
	}
	snapshot, err := d.client.GetFilesystemSnapshot(ctx, id, filestoragesdk.GetFilesystemSnapshotParams{ProjectID: d.projectID})
	if errors.Is(err, filestoragesdk.ErrNotFound) {
		diags.AddError("Filesystem snapshot not found", fmt.Sprintf("No snapshot found with id %s.", idAttr.ValueString()))
	} else if err != nil {
		diags.AddError("Error reading filesystem snapshot", err.Error())
	}
	return snapshot, diags
}

func (d *FilesystemSnapshotDataSource) getSnapshotByFilter(ctx context.Context, config FilesystemSnapshotDataSourceModel, criteria filesystemSnapshotCriteria) (*filestoragesdk.FilesystemSnapshotSchema, diag.Diagnostics) {
	id, diags := parse.OptionalUUIDString(config.ID, "id")
	filesystemID, filesystemDiags := parse.OptionalUUIDString(config.FilesystemID, "filesystem_id")
	diags.Append(filesystemDiags...)
	if diags.HasError() {
		return nil, diags
	}
	filter := filestoragelookup.FilesystemSnapshotFilter{ID: id, FilesystemID: filesystemID}
	if criteria.name {
		value := config.Name.ValueString()
		filter.Name = &value
	}
	snapshot, err := filestoragelookup.NewFilesystemSnapshotFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve filesystem snapshot", err.Error())
		return nil, diags
	}
	return &snapshot, diags
}

func filesystemSnapshotLookupCriteria(config FilesystemSnapshotDataSourceModel) (filesystemSnapshotCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	known := func(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }
	criteria := filesystemSnapshotCriteria{known(config.ID), known(config.Name), known(config.FilesystemID)}
	if criteria == (filesystemSnapshotCriteria{}) {
		diags.AddError("Missing FilesystemSnapshot lookup criteria", "Specify at least one of id, name, or filesystem_id.")
		return criteria, diags
	}
	for _, entry := range []struct {
		field string
		value types.String
		set   bool
	}{
		{"id", config.ID, criteria.id}, {"name", config.Name, criteria.name}, {"filesystem_id", config.FilesystemID, criteria.filesystemID},
	} {
		if entry.set && strings.TrimSpace(entry.value.ValueString()) == "" {
			diags.AddError("Invalid FilesystemSnapshot lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", entry.field))
		}
	}
	return criteria, diags
}
