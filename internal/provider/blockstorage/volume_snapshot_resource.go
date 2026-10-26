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
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                = &VolumeSnapshotResource{}
	_ resource.ResourceWithImportState = &VolumeSnapshotResource{}
)

const (
	volumeSnapshotPollInterval = 10 * time.Second
	volumeSnapshotTimeout      = 30 * time.Minute
)

type VolumeSnapshotResource struct {
	client       *blockstoragesdk.Client
	projectID    core.UUID
	pollInterval time.Duration
	timeout      time.Duration
}

func NewVolumeSnapshotResource() resource.Resource { return &VolumeSnapshotResource{} }

func (r *VolumeSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_snapshot"
}

func (r *VolumeSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Block Storage volume snapshot.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Snapshot ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Snapshot name."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot description. Omitting it preserves the current value. An empty string clears it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"volume": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Source volume. Changing it replaces the snapshot.",
				Attributes:    reference.Attributes(reference.ID, "Volume ID (UUID)."),
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
			},
			"volume_name": schema.StringAttribute{Computed: true, Description: "Name of the source volume."},
			"size":        schema.Int64Attribute{Computed: true, Description: "Snapshot size in GiB."},
			"status":      schema.StringAttribute{Computed: true, Description: "Current snapshot status."},
			"from_policy": schema.BoolAttribute{Computed: true, Description: "Whether a policy created the snapshot."},
			"project_id":  schema.StringAttribute{Computed: true, Description: "Project ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"zone":        schema.StringAttribute{Computed: true, Description: "Source volume zone name.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"zone_id":     schema.StringAttribute{Computed: true, Description: "Source volume zone ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"created_at":  schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at":  schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (r *VolumeSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VolumeSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	volumeID, diags := parse.UUIDString(reference.Get(plan.Volume, reference.ID), "volume.id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Invalid snapshot name", "name must be a known, non-empty string.")
		return
	}
	body := blockstoragesdk.VolumeSnapshotCreateSchema{Name: strings.TrimSpace(plan.Name.ValueString()), VolumeId: volumeID}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	created, err := r.client.CreateVolumeSnapshot(ctx, blockstoragesdk.CreateVolumeSnapshotParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume snapshot", err.Error())
		return
	}
	if created == nil {
		resp.Diagnostics.AddError("Invalid snapshot response", "The create response did not contain a snapshot.")
		return
	}
	populateVolumeSnapshotPendingState(&plan, created.Id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	snapshot, err := r.waitReady(ctx, created.Id, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume snapshot", fmt.Sprintf("%s\n\nSnapshot ID %s was preserved in state.", err, created.Id))
		return
	}
	populateVolumeSnapshotResourceState(snapshot, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VolumeSnapshotResourceModel
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
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume snapshot", err.Error())
		return
	}
	populateVolumeSnapshotResourceState(snapshot, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VolumeSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state VolumeSnapshotResourceModel
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
	body := buildVolumeSnapshotPatch(plan.VolumeSnapshotModel, state.VolumeSnapshotModel)
	if body.Name != nil || body.Description != nil {
		if _, err := r.client.UpdateVolumeSnapshot(ctx, id, blockstoragesdk.UpdateVolumeSnapshotParams{ProjectID: r.projectID}, body); err != nil {
			resp.Diagnostics.AddError("Error updating volume snapshot", err.Error())
			return
		}
	}
	snapshot, err := r.waitReady(ctx, id, &body)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume snapshot update", err.Error())
		return
	}
	populateVolumeSnapshotResourceState(snapshot, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VolumeSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteVolumeSnapshot(ctx, id, blockstoragesdk.DeleteVolumeSnapshotParams{ProjectID: r.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting volume snapshot", err.Error())
		return
	}
	_, err = r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeSnapshotSchema, error) {
		snapshot, err := r.get(ctx, id)
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if status := volumeSnapshotStatus(snapshot); status == blockstoragesdk.SnapshotStatusFail {
			return nil, fmt.Errorf("snapshot %s entered terminal status %q while deleting", id, status)
		}
		return nil, fmt.Errorf("status=%s: %w", volumeSnapshotStatus(snapshot), wait.ErrNotReady)
	})
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for volume snapshot deletion", err.Error())
	}
}

func (r *VolumeSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := core.ParseUUID(strings.TrimSpace(req.ID))
	if err != nil {
		resp.Diagnostics.AddError("Invalid volume snapshot import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id.String())...)
}

func (r *VolumeSnapshotResource) get(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeSnapshotSchema, error) {
	return r.client.GetVolumeSnapshot(ctx, id, blockstoragesdk.GetVolumeSnapshotParams{ProjectID: r.projectID})
}

func (r *VolumeSnapshotResource) newWaiter() *wait.Waiter[*blockstoragesdk.VolumeSnapshotSchema] {
	interval, timeout := r.pollInterval, r.timeout
	if interval <= 0 {
		interval = volumeSnapshotPollInterval
	}
	if timeout <= 0 {
		timeout = volumeSnapshotTimeout
	}
	return wait.New[*blockstoragesdk.VolumeSnapshotSchema](interval, timeout)
}

func (r *VolumeSnapshotResource) waitReady(ctx context.Context, id core.UUID, patch *blockstoragesdk.VolumeSnapshotPartialUpdateSchema) (*blockstoragesdk.VolumeSnapshotSchema, error) {
	snapshot, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeSnapshotSchema, error) {
		snapshot, err := r.get(ctx, id)
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			return nil, fmt.Errorf("snapshot %s is not visible yet: %w", id, wait.ErrNotReady)
		}
		if err != nil {
			return nil, err
		}
		status := volumeSnapshotStatus(snapshot)
		if status == blockstoragesdk.SnapshotStatusFail {
			return nil, fmt.Errorf("snapshot %s entered terminal status %q", id, status)
		}
		if status != blockstoragesdk.SnapshotStatusAvailable || !volumeSnapshotPatchApplied(snapshot, patch) {
			return nil, fmt.Errorf("status=%s, requested fields not yet visible: %w", status, wait.ErrNotReady)
		}
		return snapshot, nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", id, err)
	}
	return snapshot, nil
}

func buildVolumeSnapshotPatch(plan, state VolumeSnapshotModel) blockstoragesdk.VolumeSnapshotPartialUpdateSchema {
	var body blockstoragesdk.VolumeSnapshotPartialUpdateSchema
	if !plan.Name.IsNull() && !plan.Name.IsUnknown() && strings.TrimSpace(plan.Name.ValueString()) != strings.TrimSpace(state.Name.ValueString()) {
		value := strings.TrimSpace(plan.Name.ValueString())
		body.Name = &value
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	return body
}

func volumeSnapshotStatus(snapshot *blockstoragesdk.VolumeSnapshotSchema) blockstoragesdk.SnapshotStatus {
	if snapshot.Status == nil {
		return ""
	}
	return *snapshot.Status
}

func volumeSnapshotPatchApplied(snapshot *blockstoragesdk.VolumeSnapshotSchema, patch *blockstoragesdk.VolumeSnapshotPartialUpdateSchema) bool {
	if patch == nil {
		return true
	}
	if patch.Name != nil && snapshot.Name != *patch.Name {
		return false
	}
	return patch.Description == nil || snapshot.Description != nil && *snapshot.Description == *patch.Description
}
