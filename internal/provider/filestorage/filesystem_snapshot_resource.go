package filestorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &FilesystemSnapshotResource{}
	_ resource.ResourceWithImportState    = &FilesystemSnapshotResource{}
	_ resource.ResourceWithValidateConfig = &FilesystemSnapshotResource{}
)

const (
	filesystemSnapshotPollInterval = 10 * time.Second
	filesystemSnapshotTimeout      = 20 * time.Minute
)

type FilesystemSnapshotResource struct {
	client    *filestoragesdk.Client
	projectID core.UUID
}

func NewFilesystemSnapshotResource() resource.Resource { return &FilesystemSnapshotResource{} }

func (r *FilesystemSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem_snapshot"
}

func (r *FilesystemSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud File Storage filesystem snapshot.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Snapshot ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Snapshot name."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot description. Omitting it preserves the current value. An empty string clears it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"filesystem": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Source filesystem. Changing it replaces the snapshot.",
				Attributes:    reference.Attributes(reference.ID, "Filesystem ID (UUID)."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.ID, compare.SameUUIDString)},
			},
			"filesystem_name": schema.StringAttribute{Computed: true, Description: "Name of the source filesystem.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"from_policy":     schema.BoolAttribute{Computed: true, Description: "Whether a policy created the snapshot."},
			"status":          schema.StringAttribute{Computed: true, Description: "Current snapshot status."},
			"size":            schema.Int64Attribute{Computed: true, Description: "Snapshot size in GiB.", PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"owner_id":        schema.StringAttribute{Computed: true, Description: "Owner ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id":      schema.StringAttribute{Computed: true, Description: "Project ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"created_at":      schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at":      schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (r *FilesystemSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.projectID = data.FileStorage, data.ProjectID
}

func (r *FilesystemSnapshotResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config FilesystemSnapshotResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if filesystemID := reference.Get(config.Filesystem, reference.ID); !filesystemID.IsNull() && !filesystemID.IsUnknown() {
		_, diags := parse.UUIDString(filesystemID, "filesystem.id")
		resp.Diagnostics.Append(diags...)
	}
}

func (r *FilesystemSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan FilesystemSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filesystemID, diags := parse.UUIDString(reference.Get(plan.Filesystem, reference.ID), "filesystem.id")
	resp.Diagnostics.Append(diags...)
	name, nameDiags := filesystemSnapshotName(plan.Name)
	resp.Diagnostics.Append(nameDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := filestoragesdk.FilesystemSnapshotCreateSchema{Name: name, FilesystemId: filesystemID}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		body.Description.Set(strings.TrimSpace(plan.Description.ValueString()))
	}
	created, err := r.client.CreateFilesystemSnapshot(ctx, filestoragesdk.CreateFilesystemSnapshotParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating filesystem snapshot", err.Error())
		return
	}
	if created == nil {
		resp.Diagnostics.AddError("Invalid snapshot response", "The create response did not contain a snapshot.")
		return
	}
	populateFilesystemSnapshotPendingState(&plan, created.Id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	snapshot, err := r.waitReady(ctx, created.Id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for filesystem snapshot", fmt.Sprintf("%s\n\nSnapshot ID %s was preserved in state.", err, created.Id))
		return
	}
	populateFilesystemSnapshotResourceState(snapshot, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FilesystemSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state FilesystemSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	snapshot, err := r.get(ctx, id)
	if errors.Is(err, filestoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading filesystem snapshot", err.Error())
		return
	}
	populateFilesystemSnapshotResourceState(snapshot, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update needs no waiter: the backend applies a name or description change
// synchronously and returns the updated snapshot.
func (r *FilesystemSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state FilesystemSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	_, nameDiags := filesystemSnapshotName(plan.Name)
	resp.Diagnostics.Append(nameDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var snapshot *filestoragesdk.FilesystemSnapshotSchema
	var err error
	if body := buildFilesystemSnapshotUpdateBody(plan.FilesystemSnapshotModel, state.FilesystemSnapshotModel); body.Name.IsSpecified() || body.Description.IsSpecified() {
		snapshot, err = r.client.UpdateFilesystemSnapshot(ctx, id, filestoragesdk.UpdateFilesystemSnapshotParams{ProjectID: r.projectID}, body)
	} else {
		snapshot, err = r.get(ctx, id)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating filesystem snapshot", err.Error())
		return
	}
	if snapshot == nil {
		resp.Diagnostics.AddError("Invalid snapshot response", "The update response did not contain a snapshot.")
		return
	}
	populateFilesystemSnapshotResourceState(snapshot, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FilesystemSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state FilesystemSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteFilesystemSnapshot(ctx, id, filestoragesdk.DeleteFilesystemSnapshotParams{ProjectID: r.projectID})
	if errors.Is(err, filestoragesdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting filesystem snapshot", err.Error())
		return
	}
	if err := r.waitDeleted(ctx, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for filesystem snapshot deletion", err.Error())
	}
}

func (r *FilesystemSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *FilesystemSnapshotResource) get(ctx context.Context, id core.UUID) (*filestoragesdk.FilesystemSnapshotSchema, error) {
	return r.client.GetFilesystemSnapshot(ctx, id, filestoragesdk.GetFilesystemSnapshotParams{ProjectID: r.projectID})
}

// waitReady polls the snapshot until it is available and stops at once when
// it enters the terminal fail status.
func (r *FilesystemSnapshotResource) waitReady(ctx context.Context, id core.UUID) (*filestoragesdk.FilesystemSnapshotSchema, error) {
	w := wait.New[*filestoragesdk.FilesystemSnapshotSchema](filesystemSnapshotPollInterval, filesystemSnapshotTimeout)
	snapshot, err := w.WaitFor(ctx, func(ctx context.Context) (*filestoragesdk.FilesystemSnapshotSchema, error) {
		snapshot, err := r.get(ctx, id)
		if errors.Is(err, filestoragesdk.ErrNotFound) {
			return nil, fmt.Errorf("snapshot is not visible yet: %w", wait.ErrNotReady)
		}
		if err != nil {
			return nil, err
		}
		switch snapshot.Status {
		case filestoragesdk.FilesystemSnapshotStatusAvailable:
			return snapshot, nil
		case filestoragesdk.FilesystemSnapshotStatusFail:
			return nil, fmt.Errorf("snapshot entered terminal status %q", snapshot.Status)
		case filestoragesdk.FilesystemSnapshotStatusCreating,
			filestoragesdk.FilesystemSnapshotStatusUpdating,
			filestoragesdk.FilesystemSnapshotStatusDeleting:
			return nil, fmt.Errorf("status=%s: %w", snapshot.Status, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("unexpected status %q: %w", snapshot.Status, wait.ErrNotReady)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("filesystem snapshot %s: %w", id, err)
	}
	return snapshot, nil
}

// waitDeleted polls until the backend reports the snapshot as not found. A
// failed deletion leaves the snapshot in deleting with no terminal status, so
// only the timeout ends an unsuccessful wait.
func (r *FilesystemSnapshotResource) waitDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](filesystemSnapshotPollInterval, filesystemSnapshotTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		snapshot, err := r.get(ctx, id)
		if errors.Is(err, filestoragesdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, fmt.Errorf("status=%s: %w", snapshot.Status, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("filesystem snapshot %s: %w", id, err)
	}
	return nil
}

// filesystemSnapshotName returns the trimmed name, which must be non-empty.
func filesystemSnapshotName(value types.String) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := strings.TrimSpace(value.ValueString())
	if value.IsNull() || value.IsUnknown() || name == "" {
		diags.AddAttributeError(path.Root("name"), "Invalid snapshot name", "name must be a known, non-empty string.")
	}
	return name, diags
}

// buildFilesystemSnapshotUpdateBody sets description only when its planned
// value is known and differs from state. Leaving it unset preserves it on the
// backend, and an explicit empty string clears it. The backend rejects a PUT
// without name even though the SDK marks it optional, so any change sends the
// planned name too.
func buildFilesystemSnapshotUpdateBody(plan, state FilesystemSnapshotModel) filestoragesdk.FilesystemSnapshotUpdateSchema {
	var body filestoragesdk.FilesystemSnapshotUpdateSchema
	name := strings.TrimSpace(plan.Name.ValueString())
	nameKnown := !plan.Name.IsNull() && !plan.Name.IsUnknown()
	// The API trims the description, so a change in outer whitespace only is
	// no change.
	descriptionChanged := !plan.Description.IsNull() && !plan.Description.IsUnknown() && !compare.SameTrimmed(state.Description, plan.Description.ValueString())
	if descriptionChanged {
		body.Description.Set(strings.TrimSpace(plan.Description.ValueString()))
	}
	if nameKnown && (descriptionChanged || name != strings.TrimSpace(state.Name.ValueString())) {
		body.Name.Set(name)
	}
	return body
}
