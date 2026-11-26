package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &VolumeBackupResource{}
	_ resource.ResourceWithImportState    = &VolumeBackupResource{}
	_ resource.ResourceWithValidateConfig = &VolumeBackupResource{}
	_ resource.ResourceWithModifyPlan     = &VolumeBackupResource{}
)

const (
	volumeBackupPollInterval  = 10 * time.Second
	volumeBackupTimeout       = 30 * time.Minute
	volumeBackupUpdateTimeout = 60 * time.Minute
	resourceBusyErrorCode     = "resource_busy"
	volumeBusyReplayTimeout   = 2 * time.Minute
)

// errRestoreFailed marks a restore that the API reported as failed. Only then
// is it safe to send the restore again on the next apply.
var errRestoreFailed = errors.New("restore failed")

type VolumeBackupResource struct {
	client       *blockstoragesdk.Client
	claims       *keylock.Locker
	projectID    core.UUID
	pollInterval time.Duration
	timeout      time.Duration
}

func NewVolumeBackupResource() resource.Resource { return &VolumeBackupResource{} }

func (r *VolumeBackupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_backup"
}

func (r *VolumeBackupResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manage a Viettel Cloud volume backup, and restore it into an existing volume.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Backup ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"name":        schema.StringAttribute{Required: true, Description: "Backup name."},
		"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Backup description. Omit it to preserve the current value. Set an empty string to clear it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		// A backup remains bound to its source volume when the volume is renamed.
		// The UUID gives the create request a stable, unique reference.
		"volume": schema.SingleNestedAttribute{
			Required:      true,
			Description:   "Source volume. Changing it replaces the backup.",
			Attributes:    reference.Attributes(reference.ID, "Volume ID (UUID)."),
			PlanModifiers: []planmodifier.Object{replace.Reference(reference.ID, compare.SameUUIDString)},
		},
		// The API does not report restores, so the value lives only in
		// configuration and state, and a change of volume is the only signal
		// to restore.
		"restore_volume": schema.SingleNestedAttribute{
			Optional: true,
			Description: "Existing volume to restore this backup into. The restore overwrites all data on the volume. " +
				"Adding it or naming another volume restores; removing it changes nothing. " +
				"It cannot be set when the backup is created or replaced. " +
				"To restore into the same volume again, remove it and apply, then add it back. " +
				"The volume must be in the same region as the backup. " +
				"Some failed restores end without an error and keep the old data, so check the volume after a restore.",
			Attributes: reference.Attributes(reference.ID, "Volume ID (UUID)."),
		},
		"timeouts":            volumeBackupTimeoutsAttribute(ctx),
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
	r.client, r.claims, r.projectID = data.BlockStorage, data.Claims, data.ProjectID
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
	if restoreVolumeID := reference.Get(config.RestoreVolume, reference.ID); !restoreVolumeID.IsNull() && !restoreVolumeID.IsUnknown() {
		_, diags := parse.UUIDString(restoreVolumeID, "restore_volume.id")
		resp.Diagnostics.Append(diags...)
	}
}

// ModifyPlan rejects restore_volume in a plan that creates a backup, including
// a replacement. Terraform core plans every replacement again with a null
// prior state, and plans a tainted backup with a null prior state only. A null
// prior state therefore identifies every new backup.
//
// A restore inside Create would taint a backup that is usable when the restore
// fails, so the next apply would delete it.
func (r *VolumeBackupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || !req.State.Raw.IsNull() {
		return
	}
	var restoreVolume types.Object
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("restore_volume"), &restoreVolume)...)
	if resp.Diagnostics.HasError() || restoreVolume.IsNull() || restoreVolume.IsUnknown() {
		return
	}
	resp.Diagnostics.Append(restoreOnNewBackupError())
}

func (r *VolumeBackupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeBackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// ModifyPlan rejects this at plan time. An unknown restore_volume can
	// reach apply, where Terraform plans again with the known value.
	if !plan.RestoreVolume.IsNull() {
		resp.Diagnostics.Append(restoreOnNewBackupError())
		return
	}
	volumeID, diags := parse.UUIDString(reference.Get(plan.Volume, reference.ID), "volume.id")
	resp.Diagnostics.Append(diags...)
	createTimeout, diags := plan.Timeouts.Create(ctx, r.timeoutOr(volumeBackupTimeout))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := withOperationTimeout(ctx, createTimeout)
	defer cancel()
	body := blockstoragesdk.VolumeBackupCreateSchema{Name: strings.TrimSpace(plan.Name.ValueString()), VolumeId: volumeID}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &value
	}
	unlock, err := r.claims.Lock(ctx, volumeID.String())
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume backup", operationTimeoutDetail(ctx, err.Error(), "create", createTimeout))
		return
	}
	defer unlock()
	if err := r.waitUntilVolumeIdle(ctx, volumeID, createTimeout); err != nil {
		resp.Diagnostics.AddError("Error creating volume backup", operationTimeoutDetail(ctx, err.Error(), "create", createTimeout))
		return
	}
	backup, err := replayVolumeBusy(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeBackupSchema, error) {
		return r.client.CreateVolumeBackup(ctx, blockstoragesdk.CreateVolumeBackupParams{ProjectID: r.projectID}, body)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume backup", operationTimeoutDetail(ctx, volumeRequestError(err, volumeID), "create", createTimeout))
		return
	}
	// Save the new ID before polling so a timeout does not leave an untracked backup.
	populateVolumeBackupResourceState(backup, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	backupID := backup.Id
	backup, err = r.waitUntilAvailable(ctx, backupID, createTimeout)
	if err != nil {
		detail := operationTimeoutDetail(ctx, err.Error(), "create", createTimeout)
		resp.Diagnostics.AddError("Error waiting for volume backup", fmt.Sprintf("%s\n\nBackup ID %s was preserved in state.", detail, backupID))
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
	restoreVolumeID, diags := volumeBackupRestoreTarget(plan, state)
	resp.Diagnostics.Append(diags...)
	updateTimeout, diags := plan.Timeouts.Update(ctx, r.timeoutOr(volumeBackupUpdateTimeout))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := withOperationTimeout(ctx, updateTimeout)
	defer cancel()
	body := buildVolumeBackupUpdate(plan, state)
	if body.Name != nil || body.Description != nil {
		_, err := r.client.PartialUpdateVolumeBackup(ctx, id, blockstoragesdk.PartialUpdateVolumeBackupParams{ProjectID: r.projectID}, body)
		if err != nil {
			resp.Diagnostics.AddError("Error updating volume backup", operationTimeoutDetail(ctx, err.Error(), "update", updateTimeout))
			return
		}
	}
	backup, err := r.waitUntilAvailableMatching(ctx, id, &body, updateTimeout)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume backup update", operationTimeoutDetail(ctx, err.Error(), "update", updateTimeout))
		return
	}
	newState := plan
	populateVolumeBackupResourceState(backup, &newState)
	if restoreVolumeID != nil {
		restored, accepted, diags := r.restore(ctx, id, *restoreVolumeID, updateTimeout)
		if diags.HasError() {
			// Only a restore that never started or that the API reported as
			// failed keeps the prior restore_volume, so the next plan proposes
			// it again. Sending a restore with an unknown outcome again could
			// overwrite newer data.
			if !accepted {
				newState.RestoreVolume = state.RestoreVolume
			}
			resp.Diagnostics.Append(diags...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
			return
		}
		populateVolumeBackupResourceState(restored, &newState)
	}
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
	if err := r.waitUntilDeleted(ctx, id, r.timeoutOr(volumeBackupTimeout)); err != nil {
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

// restore restores backup backupID into volume volumeID and waits until both
// are ready again. It holds the claims lock of the volume from the idle wait
// to the end of the ready wait, because the restore claims the volume.
//
// accepted reports whether state must record volumeID. It is true after a
// successful restore. It is also true when the API accepted the restore but
// the outcome is unknown, for example after the update deadline, because the
// restore can still run.
func (r *VolumeBackupResource) restore(ctx context.Context, backupID, volumeID core.UUID, timeout time.Duration) (backup *blockstoragesdk.VolumeBackupSchema, accepted bool, diags diag.Diagnostics) {
	unlock, err := r.claims.Lock(ctx, volumeID.String())
	if err != nil {
		diags.AddError("Error restoring volume backup", operationTimeoutDetail(ctx, err.Error(), "update", timeout))
		return nil, false, diags
	}
	defer unlock()
	if err := r.waitUntilRestoreIdle(ctx, backupID, volumeID, timeout); err != nil {
		diags.AddError("Error restoring volume backup", operationTimeoutDetail(ctx, err.Error(), "update", timeout))
		return nil, false, diags
	}
	_, err = replayVolumeBusy(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, r.client.RestoreVolumeBackup(ctx, backupID, blockstoragesdk.RestoreVolumeBackupParams{ProjectID: r.projectID}, blockstoragesdk.VolumeBackupRestoreSchema{VolumeId: volumeID})
	})
	if err != nil {
		diags.AddError("Error restoring volume backup", operationTimeoutDetail(ctx, volumeRequestError(err, volumeID), "update", timeout))
		return nil, false, diags
	}
	backup, err = r.waitUntilRestored(ctx, backupID, volumeID, timeout)
	if errors.Is(err, errRestoreFailed) {
		diags.AddError("Error waiting for volume backup restore", err.Error())
		return nil, false, diags
	}
	if err != nil {
		detail := operationTimeoutDetail(ctx, err.Error(), "update", timeout)
		diags.AddError("Error waiting for volume backup restore", fmt.Sprintf(
			"%s\n\nThe restore can still be running. restore_volume now records volume %s, so the next apply does not restore again. "+
				"Check the data on the volume. To restore again, remove restore_volume, apply, then add it back.", detail, volumeID,
		))
		return nil, true, diags
	}
	return backup, true, diags
}

func (r *VolumeBackupResource) waitUntilAvailable(ctx context.Context, id core.UUID, timeout time.Duration) (*blockstoragesdk.VolumeBackupSchema, error) {
	return r.waitUntilAvailableMatching(ctx, id, nil, timeout)
}

func (r *VolumeBackupResource) waitUntilAvailableMatching(ctx context.Context, id core.UUID, expected *blockstoragesdk.VolumeBackupPartialUpdateSchema, timeout time.Duration) (*blockstoragesdk.VolumeBackupSchema, error) {
	w := wait.New[*blockstoragesdk.VolumeBackupSchema](r.pollIntervalOrDefault(), timeout)
	backup, err := w.WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeBackupSchema, error) {
		backup, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		status := core.NullableToPtr(backup.Status)
		if status == nil {
			return nil, fmt.Errorf("backup status is absent: %w", wait.ErrNotReady)
		}
		switch *status {
		case blockstoragesdk.VolumeBackupStatusAvailable:
			if expected != nil {
				if expected.Name != nil && backup.Name != *expected.Name && backup.Name != strings.TrimSpace(*expected.Name) {
					return nil, fmt.Errorf("backup name is %q: %w", backup.Name, wait.ErrNotReady)
				}
				if expected.Description != nil && (backup.Description == nil || strings.TrimSpace(*backup.Description) != strings.TrimSpace(*expected.Description)) {
					return nil, fmt.Errorf("backup description has not changed: %w", wait.ErrNotReady)
				}
			}
			return backup, nil
		case blockstoragesdk.VolumeBackupStatusFail:
			return nil, fmt.Errorf("backup %s failed", id)
		case blockstoragesdk.VolumeBackupStatusCreating, blockstoragesdk.VolumeBackupStatusDeleting,
			blockstoragesdk.VolumeBackupStatusRestoring, blockstoragesdk.VolumeBackupStatusUpdating:
			return nil, fmt.Errorf("backup status is %s: %w", *status, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("backup %s has unknown status %s", id, *status)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("volume backup %s: %w", id, err)
	}
	return backup, nil
}

func (r *VolumeBackupResource) waitUntilDeleted(ctx context.Context, id core.UUID, timeout time.Duration) error {
	w := wait.New[struct{}](r.pollIntervalOrDefault(), timeout)
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

// waitUntilVolumeIdle is the idle wait of volume volumeID before a backup
// Create, which claims the source volume.
//
// The volume is idle when it is available or in-use. The backend refuses the
// request in the states that volumeIdle reports as fatal, and it never leaves
// them by itself, so the wait fails at once there.
func (r *VolumeBackupResource) waitUntilVolumeIdle(ctx context.Context, volumeID core.UUID, timeout time.Duration) error {
	w := wait.New[struct{}](r.pollIntervalOrDefault(), timeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		volume, err := r.client.GetVolume(ctx, volumeID, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, volumeIdle(volume.Status)
	})
	if err != nil {
		return fmt.Errorf("volume %s did not become idle: %w", volumeID, err)
	}
	return nil
}

// waitUntilRestoreIdle is the idle wait before a restore of backup backupID
// into volume volumeID. The restore claims the volume, and the backend also
// refuses it unless the backup is available.
//
// The volume is idle as in waitUntilVolumeIdle, and the backup is idle when it
// is available. A failed or deleting backup fails at once.
func (r *VolumeBackupResource) waitUntilRestoreIdle(ctx context.Context, backupID, volumeID core.UUID, timeout time.Duration) error {
	_, err := r.pollRestore(ctx, backupID, volumeID, timeout, func(status blockstoragesdk.VolumeStatus) error {
		if err := volumeIdle(status); err != nil {
			return fmt.Errorf("volume %s did not become idle: %w", volumeID, err)
		}
		return nil
	}, backupIdle)
	if err != nil {
		return fmt.Errorf("volume backup %s: %w", backupID, err)
	}
	return nil
}

// waitUntilRestored is the ready wait after a restore of backup backupID into
// volume volumeID. Both leave restoring when the restore ends: the volume
// becomes available or in-use, and the backup becomes available.
//
// The backend sets both to restoring before it answers the request, so the
// first poll cannot see the state from before the restore.
func (r *VolumeBackupResource) waitUntilRestored(ctx context.Context, backupID, volumeID core.UUID, timeout time.Duration) (*blockstoragesdk.VolumeBackupSchema, error) {
	backup, err := r.pollRestore(ctx, backupID, volumeID, timeout, func(status blockstoragesdk.VolumeStatus) error {
		return volumeRestored(volumeID, status)
	}, func(status *blockstoragesdk.VolumeBackupStatus) error {
		return backupRestored(backupID, status)
	})
	if err != nil {
		return nil, fmt.Errorf("volume backup %s: %w", backupID, err)
	}
	return backup, nil
}

// pollRestore polls volume volumeID and backup backupID until both checks
// pass, and returns the last backup. Every GET error fails at once, including
// not-found, because a restore needs both objects.
func (r *VolumeBackupResource) pollRestore(
	ctx context.Context,
	backupID, volumeID core.UUID,
	timeout time.Duration,
	checkVolume func(blockstoragesdk.VolumeStatus) error,
	checkBackup func(*blockstoragesdk.VolumeBackupStatus) error,
) (*blockstoragesdk.VolumeBackupSchema, error) {
	w := wait.New[*blockstoragesdk.VolumeBackupSchema](r.pollIntervalOrDefault(), timeout)
	return w.WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeBackupSchema, error) {
		volume, err := r.client.GetVolume(ctx, volumeID, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil {
			return nil, fmt.Errorf("read volume %s: %w", volumeID, err)
		}
		if err := checkVolume(volume.Status); err != nil {
			return nil, err
		}
		backup, err := r.get(ctx, backupID)
		if err != nil {
			return nil, err
		}
		if err := checkBackup(core.NullableToPtr(backup.Status)); err != nil {
			return nil, err
		}
		return backup, nil
	})
}

func (r *VolumeBackupResource) pollIntervalOrDefault() time.Duration {
	if r.pollInterval > 0 {
		return r.pollInterval
	}
	return volumeBackupPollInterval
}

// timeoutOr returns the timeout that a test set, or fallback.
func (r *VolumeBackupResource) timeoutOr(fallback time.Duration) time.Duration {
	if r.timeout > 0 {
		return r.timeout
	}
	return fallback
}

func buildVolumeBackupUpdate(plan, state VolumeBackupResourceModel) blockstoragesdk.VolumeBackupPartialUpdateSchema {
	var body blockstoragesdk.VolumeBackupPartialUpdateSchema
	// The API trims both strings, so a change in outer whitespace only is no
	// change.
	if !plan.Name.IsNull() && !plan.Name.IsUnknown() && !compare.SameTrimmed(state.Name, plan.Name.ValueString()) {
		value := strings.TrimSpace(plan.Name.ValueString())
		body.Name = &value
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !compare.SameTrimmed(state.Description, plan.Description.ValueString()) {
		value := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &value
	}
	return body
}

// volumeBackupRestoreTarget returns the volume that Update restores the backup
// into, or nil when the plan needs no restore. Only a new volume restores: the
// API does not report restores, so the same UUID in another spelling, or a
// removed restore_volume, changes state only.
func volumeBackupRestoreTarget(plan, state VolumeBackupResourceModel) (*core.UUID, diag.Diagnostics) {
	planned := reference.Get(plan.RestoreVolume, reference.ID)
	if plan.RestoreVolume.IsNull() || compare.SameUUIDString(planned, reference.Get(state.RestoreVolume, reference.ID)) {
		return nil, nil
	}
	id, diags := parse.UUIDString(planned, "restore_volume.id")
	if diags.HasError() {
		return nil, diags
	}
	return &id, nil
}

// volumeBackupTimeoutsAttribute builds the timeouts attribute. It has no delete
// key, because a delete does not depend on the size of the data.
func volumeBackupTimeoutsAttribute(ctx context.Context) schema.Attribute {
	attribute := timeouts.Attributes(ctx, timeouts.Opts{
		Create:            true,
		Update:            true,
		CreateDescription: `Maximum duration of a create, such as "90m". Defaults to "30m".`,
		UpdateDescription: `Maximum duration of an in-place update, including a restore into restore_volume, such as "2h". Defaults to "60m". A restore into a large volume can need more.`,
	})
	nested, ok := attribute.(schema.SingleNestedAttribute)
	if !ok {
		return attribute
	}
	nested.Description = "Maximum durations of operations. A change of timeouts alone sends no request. A delete waits at most 30 minutes."
	return nested
}

// withOperationTimeout makes timeout the deadline of a whole operation. The
// cause makes a wait that the deadline ends report the configured value, not
// the time that was left for that wait.
func withOperationTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, timeout, fmt.Errorf("%w after %s", wait.ErrTimeout, timeout))
}

// operationTimeoutDetail adds the configured timeout to detail when the
// operation deadline ended the operation, so that the practitioner knows which
// value to raise. A busy replay that runs out of its own time is not such a
// case.
func operationTimeoutDetail(ctx context.Context, detail, name string, timeout time.Duration) string {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return detail
	}
	return fmt.Sprintf("%s\n\nThe operation did not finish within timeouts.%s (%s).", detail, name, timeout)
}

func restoreOnNewBackupError() diag.Diagnostic {
	return diag.NewAttributeErrorDiagnostic(
		path.Root("restore_volume"),
		"Restore requires an existing backup",
		"restore_volume cannot be set in a plan that creates or replaces the backup. "+
			"Remove restore_volume and apply to create the backup, then add restore_volume and apply again.",
	)
}

// volumeIdle classifies a volume status before a request that claims the
// volume. It returns nil when the volume is idle. While another operation
// holds the volume, it returns an error that wraps wait.ErrNotReady. It returns
// any other error for a state in which the backend refuses the request. An
// absent status is not ready.
func volumeIdle(status blockstoragesdk.VolumeStatus) error {
	switch status {
	case blockstoragesdk.VolumeStatusAvailable, blockstoragesdk.VolumeStatusInUse:
		return nil
	case "", blockstoragesdk.VolumeStatusCreating, blockstoragesdk.VolumeStatusAttaching,
		blockstoragesdk.VolumeStatusDetaching, blockstoragesdk.VolumeStatusBackingUp,
		blockstoragesdk.VolumeStatusRestoring, blockstoragesdk.VolumeStatusRetyping,
		blockstoragesdk.VolumeStatusUpdating, blockstoragesdk.VolumeStatusUploading:
		return fmt.Errorf("volume status is %q: %w", status, wait.ErrNotReady)
	case blockstoragesdk.VolumeStatusFailed, blockstoragesdk.VolumeStatusRestoreFailed,
		blockstoragesdk.VolumeStatusAttachFailed, blockstoragesdk.VolumeStatusDetachFailed,
		blockstoragesdk.VolumeStatusExtendFailed, blockstoragesdk.VolumeStatusBackupFailed,
		blockstoragesdk.VolumeStatusDeleting, blockstoragesdk.VolumeStatusAwaitingTransfer:
		return fmt.Errorf("volume status %s refuses the request", status)
	default:
		return fmt.Errorf("volume has unknown status %s", status)
	}
}

// backupIdle classifies a backup status before a restore, as volumeIdle does
// for the volume.
func backupIdle(status *blockstoragesdk.VolumeBackupStatus) error {
	if status == nil {
		return fmt.Errorf("backup status is absent: %w", wait.ErrNotReady)
	}
	switch *status {
	case blockstoragesdk.VolumeBackupStatusAvailable:
		return nil
	case blockstoragesdk.VolumeBackupStatusCreating, blockstoragesdk.VolumeBackupStatusRestoring,
		blockstoragesdk.VolumeBackupStatusUpdating:
		return fmt.Errorf("backup status is %s: %w", *status, wait.ErrNotReady)
	case blockstoragesdk.VolumeBackupStatusFail, blockstoragesdk.VolumeBackupStatusDeleting:
		return fmt.Errorf("backup status %s refuses the restore", *status)
	default:
		return fmt.Errorf("backup has unknown status %s", *status)
	}
}

// volumeRestored classifies the status of volume id after a restore request.
// Only restoring and an absent status are not ready. The status of another
// operation means that the volume left the restore, so it fails at once.
func volumeRestored(id core.UUID, status blockstoragesdk.VolumeStatus) error {
	switch status {
	case blockstoragesdk.VolumeStatusAvailable, blockstoragesdk.VolumeStatusInUse:
		return nil
	case "", blockstoragesdk.VolumeStatusRestoring:
		return fmt.Errorf("volume status is %q: %w", status, wait.ErrNotReady)
	case blockstoragesdk.VolumeStatusRestoreFailed, blockstoragesdk.VolumeStatusFailed:
		return fmt.Errorf("volume %s %w", id, errRestoreFailed)
	case blockstoragesdk.VolumeStatusAttaching, blockstoragesdk.VolumeStatusDetaching,
		blockstoragesdk.VolumeStatusAttachFailed, blockstoragesdk.VolumeStatusDetachFailed,
		blockstoragesdk.VolumeStatusExtendFailed, blockstoragesdk.VolumeStatusBackingUp,
		blockstoragesdk.VolumeStatusBackupFailed, blockstoragesdk.VolumeStatusCreating,
		blockstoragesdk.VolumeStatusDeleting, blockstoragesdk.VolumeStatusRetyping,
		blockstoragesdk.VolumeStatusUpdating, blockstoragesdk.VolumeStatusUploading,
		blockstoragesdk.VolumeStatusAwaitingTransfer:
		return fmt.Errorf("volume %s has unexpected status %s during restore", id, status)
	default:
		return fmt.Errorf("volume %s has unknown status %s", id, status)
	}
}

// backupRestored classifies the status of backup id after a restore request,
// as volumeRestored does for the volume.
func backupRestored(id core.UUID, status *blockstoragesdk.VolumeBackupStatus) error {
	if status == nil {
		return fmt.Errorf("backup status is absent: %w", wait.ErrNotReady)
	}
	switch *status {
	case blockstoragesdk.VolumeBackupStatusAvailable:
		return nil
	case blockstoragesdk.VolumeBackupStatusRestoring:
		return fmt.Errorf("backup status is %s: %w", *status, wait.ErrNotReady)
	case blockstoragesdk.VolumeBackupStatusFail:
		return fmt.Errorf("backup %s failed, so the %w", id, errRestoreFailed)
	case blockstoragesdk.VolumeBackupStatusCreating, blockstoragesdk.VolumeBackupStatusDeleting,
		blockstoragesdk.VolumeBackupStatusUpdating:
		return fmt.Errorf("backup %s has unexpected status %s during restore", id, *status)
	default:
		return fmt.Errorf("backup %s has unknown status %s", id, *status)
	}
}

// replayVolumeBusy sends a request that claims a volume, and sends it again
// after each busy refusal. It rests a random part of 10 s, 20 s, then 30 s
// between requests, for volumeBusyReplayTimeout at most. Every other error
// returns at once.
//
// The replay works around cmp-backend#1976. The backend releases the claim of
// a backup Create on the next tick of a 60 s watcher, while GET already shows
// the volume as idle. The idle wait therefore cannot see that claim, and two
// ticks bound the timeout. Remove the replay when GET of the volume shows the
// claim.
func replayVolumeBusy[T any](ctx context.Context, send func(context.Context) (T, error)) (T, error) {
	w := wait.NewBackoffWithJitter[T]([]time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second}, volumeBusyReplayTimeout)
	return w.WaitFor(ctx, func(ctx context.Context) (T, error) {
		value, err := send(ctx)
		if volumeBusyRefusal(err) {
			return value, fmt.Errorf("%w: %w", err, wait.ErrNotReady)
		}
		return value, err
	})
}

// volumeRequestError describes the error of a request that claims volume
// volumeID. A busy refusal that outlasts the replay names the busy volume.
func volumeRequestError(err error, volumeID core.UUID) string {
	if volumeBusyRefusal(err) {
		return fmt.Sprintf("volume %s is busy, because another operation holds it: %s", volumeID, err)
	}
	return err.Error()
}

// volumeBusyRefusal reports whether err is a busy refusal: an HTTP 409
// response with at least one error detail whose code is resource_busy. The
// message is not part of the contract, so it is never matched.
func volumeBusyRefusal(err error) bool {
	var apiErr *blockstoragesdk.APIError
	if !errors.As(err, &apiErr) || !errors.Is(apiErr, blockstoragesdk.ErrConflict) {
		return false
	}
	for _, detail := range apiErr.Errors {
		if detail.Code == resourceBusyErrorCode {
			return true
		}
	}
	return false
}
