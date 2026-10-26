package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &VolumeBackupResource{}
	_ resource.ResourceWithImportState    = &VolumeBackupResource{}
	_ resource.ResourceWithValidateConfig = &VolumeBackupResource{}
)

const (
	volumeBackupPollInterval = 10 * time.Second
	volumeBackupTimeout      = 30 * time.Minute
)

type VolumeBackupResource struct {
	client       *blockstoragesdk.Client
	projectID    core.UUID
	pollInterval time.Duration
	timeout      time.Duration
}

func NewVolumeBackupResource() resource.Resource { return &VolumeBackupResource{} }

func (r *VolumeBackupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_backup"
}

func (r *VolumeBackupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manage a Viettel Cloud volume backup.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Backup ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":        schema.StringAttribute{Required: true, Description: "Backup name."},
		"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Backup description. Omit it to preserve the current value. Set an empty string to clear it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		// A backup remains bound to its source volume when the volume is renamed.
		// The UUID gives the create request a stable, unique reference.
		"volume": schema.SingleNestedAttribute{
			Required:      true,
			Description:   "Source volume. Changing it replaces the backup.",
			Attributes:    reference.Attributes(reference.ID, "Volume ID (UUID)."),
			PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		},
		"volume_name":         schema.StringAttribute{Computed: true, Description: "Source volume name."},
		"status":              schema.StringAttribute{Computed: true, Description: "Current backup status."},
		"size":                schema.Int64Attribute{Computed: true, Description: "Backup size in GiB."},
		"expired":             schema.BoolAttribute{Computed: true, Description: "Whether the backup expired."},
		"disk_type":           schema.StringAttribute{Computed: true, Description: "Source disk type."},
		"encryption_key_size": schema.StringAttribute{Computed: true, Description: "Encryption key size."},
		"project_id":          schema.StringAttribute{Computed: true, Description: "Owning project ID (UUID)."},
		"region":              schema.StringAttribute{Computed: true, Description: "Region name."},
		"region_id":           schema.StringAttribute{Computed: true, Description: "Region ID (UUID)."},
		"policy_id":           schema.StringAttribute{Computed: true, Description: "Backup policy ID (UUID), if any."},
		"created_at":          schema.StringAttribute{Computed: true, Description: "Creation timestamp (RFC3339)."},
		"updated_at":          schema.StringAttribute{Computed: true, Description: "Last update timestamp (RFC3339)."},
	}}
}

func (r *VolumeBackupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.projectID = data.BlockStorage, data.ProjectID
}

func (r *VolumeBackupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config VolumeBackupResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid backup name", "name must not be empty or whitespace-only.")
	}
	if volumeID := reference.Get(config.Volume, reference.ID); !volumeID.IsNull() && !volumeID.IsUnknown() {
		_, diags := parse.UUIDString(volumeID, "volume.id")
		resp.Diagnostics.Append(diags...)
	}
}

func (r *VolumeBackupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeBackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	volumeID, diags := parse.UUIDString(reference.Get(plan.Volume, reference.ID), "volume.id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := blockstoragesdk.VolumeBackupCreateSchema{Name: plan.Name.ValueString(), VolumeId: volumeID}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	backup, err := r.client.CreateVolumeBackup(ctx, blockstoragesdk.CreateVolumeBackupParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume backup", err.Error())
		return
	}
	// Save the new ID before polling so a timeout does not leave an untracked backup.
	populateVolumeBackupResourceState(backup, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	backupID := backup.Id
	backup, err = r.waitUntilAvailable(ctx, backupID)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume backup", fmt.Sprintf("%s\n\nBackup ID %s was preserved in state.", err, backupID))
		return
	}
	populateVolumeBackupResourceState(backup, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeBackupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VolumeBackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	backup, err := r.get(ctx, id)
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume backup", err.Error())
		return
	}
	populateVolumeBackupResourceState(backup, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VolumeBackupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state VolumeBackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := buildVolumeBackupUpdate(plan, state)
	if body.Name != nil || body.Description != nil {
		_, err := r.client.PartialUpdateVolumeBackup(ctx, id, blockstoragesdk.PartialUpdateVolumeBackupParams{ProjectID: r.projectID}, body)
		if err != nil {
			resp.Diagnostics.AddError("Error updating volume backup", err.Error())
			return
		}
	}
	backup, err := r.waitUntilAvailableMatching(ctx, id, &body)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume backup update", err.Error())
		return
	}
	newState := plan
	populateVolumeBackupResourceState(backup, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *VolumeBackupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VolumeBackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteVolumeBackup(ctx, id, blockstoragesdk.DeleteVolumeBackupParams{ProjectID: r.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting volume backup", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for volume backup deletion", err.Error())
	}
}

func (r *VolumeBackupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if _, diags := parse.UUIDString(types.StringValue(id), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *VolumeBackupResource) get(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeBackupSchema, error) {
	return r.client.GetVolumeBackup(ctx, id, blockstoragesdk.GetVolumeBackupParams{ProjectID: r.projectID})
}

func (r *VolumeBackupResource) waitUntilAvailable(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeBackupSchema, error) {
	return r.waitUntilAvailableMatching(ctx, id, nil)
}

func (r *VolumeBackupResource) waitUntilAvailableMatching(ctx context.Context, id core.UUID, expected *blockstoragesdk.VolumeBackupPartialUpdateSchema) (*blockstoragesdk.VolumeBackupSchema, error) {
	w := wait.New[*blockstoragesdk.VolumeBackupSchema](r.pollIntervalOrDefault(), r.timeoutOrDefault())
	backup, err := w.WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeBackupSchema, error) {
		backup, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if backup.Status == nil {
			return nil, fmt.Errorf("backup status is absent: %w", wait.ErrNotReady)
		}
		switch *backup.Status {
		case blockstoragesdk.VolumeBackupStatusAvailable:
			if expected != nil {
				if expected.Name != nil && backup.Name != *expected.Name && backup.Name != strings.TrimSpace(*expected.Name) {
					return nil, fmt.Errorf("backup name is %q: %w", backup.Name, wait.ErrNotReady)
				}
				if expected.Description != nil && (backup.Description == nil || *backup.Description != *expected.Description) {
					return nil, fmt.Errorf("backup description has not changed: %w", wait.ErrNotReady)
				}
			}
			return backup, nil
		case blockstoragesdk.VolumeBackupStatusFail:
			return nil, fmt.Errorf("backup %s failed", id)
		case blockstoragesdk.VolumeBackupStatusCreating, blockstoragesdk.VolumeBackupStatusDeleting,
			blockstoragesdk.VolumeBackupStatusRestoring, blockstoragesdk.VolumeBackupStatusUpdating:
			return nil, fmt.Errorf("backup status is %s: %w", *backup.Status, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("backup %s has unknown status %s", id, *backup.Status)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("volume backup %s: %w", id, err)
	}
	return backup, nil
}

func (r *VolumeBackupResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](r.pollIntervalOrDefault(), r.timeoutOrDefault())
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		_, err := r.get(ctx, id)
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, fmt.Errorf("backup still exists: %w", wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("volume backup %s: %w", id, err)
	}
	return nil
}

func (r *VolumeBackupResource) pollIntervalOrDefault() time.Duration {
	if r.pollInterval > 0 {
		return r.pollInterval
	}
	return volumeBackupPollInterval
}

func (r *VolumeBackupResource) timeoutOrDefault() time.Duration {
	if r.timeout > 0 {
		return r.timeout
	}
	return volumeBackupTimeout
}

func buildVolumeBackupUpdate(plan, state VolumeBackupResourceModel) blockstoragesdk.VolumeBackupPartialUpdateSchema {
	var body blockstoragesdk.VolumeBackupPartialUpdateSchema
	if !plan.Name.IsUnknown() && !plan.Name.Equal(state.Name) {
		value := plan.Name.ValueString()
		body.Name = &value
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	return body
}
