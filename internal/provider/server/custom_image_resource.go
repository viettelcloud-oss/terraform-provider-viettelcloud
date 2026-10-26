package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &CustomImageResource{}
	_ resource.ResourceWithImportState    = &CustomImageResource{}
	_ resource.ResourceWithValidateConfig = &CustomImageResource{}
	_ resource.ResourceWithModifyPlan     = &CustomImageResource{}
)

const (
	customImageTimeout      = 30 * time.Minute
	customImagePollInterval = 5 * time.Second
)

type CustomImageResource struct {
	client       *serversdk.Client
	blockStorage *blockstoragesdk.Client
	projectID    core.UUID
}

func NewCustomImageResource() resource.Resource { return &CustomImageResource{} }
func (r *CustomImageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_image"
}

func (r *CustomImageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud custom image created from a volume. Import an existing volume-backed image with its UUID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Image UUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Image name.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image description. Omission preserves the backend value. An empty string clears the value.",
			},
			"volume": schema.SingleNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Source volume. Configure exactly one of name or id. Changing a configured criterion replaces the image.",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				// Each criterion is also computed so that state records both the
				// name and the UUID of the source volume. A criterion that is not
				// configured is then unknown on any planned change, so replacement
				// is decided per criterion, not on the whole object.
				Attributes: map[string]schema.Attribute{
					reference.Name: schema.StringAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Source volume name. Exactly one volume must match in the provider project.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
					},
					reference.ID: schema.StringAttribute{
						Optional:      true,
						Computed:      true,
						Description:   "Source volume ID (UUID). The volume must belong to the provider project.",
						PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
					},
				},
			},
			"disk_format": schema.StringAttribute{
				Required:      true,
				Description:   "Disk format: raw or qcow2. Changing this value replaces the image.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"container_format": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Container format: bare. Changing a configured value replaces the image.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
			},
			"os": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Operating system. Omission preserves the backend value. An empty string clears the value.",
			},
			"version": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Operating system version. Omission preserves the backend value. An empty string clears the value.",
			},
			"state": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image state: enabled or disabled. Omission preserves the backend value.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Backend image status.",
			},
			"metadata": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image metadata as a JSON object. Configured values must be strings. Omission preserves metadata. Use {} to clear metadata.",
			},
			"shared_projects": schema.SetNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Projects that can use this image. Omission preserves sharing. Use an empty set to stop sharing.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: reference.PreservedAttributes(reference.ID, "Project ID (UUID)."),
				},
			},
			"size": schema.Int64Attribute{
				Computed:    true,
				Description: "Image size reported by the API.",
			},
			"min_volume_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Minimum volume size reported by the API.",
			},
			"encrypt_key_size": schema.StringAttribute{
				Computed:    true,
				Description: "Encryption key size reported by the API.",
			},
			"region": schema.StringAttribute{
				Computed:    true,
				Description: "Region name.",
			},
			"project_id": schema.StringAttribute{
				Computed:    true,
				Description: "Owner project UUID.",
			},
			"project": schema.StringAttribute{
				Computed:    true,
				Description: "Owner project name.",
			},
			"owner_id": schema.StringAttribute{
				Computed:    true,
				Description: "Owner user UUID. Null when the API omits the owner.",
			},
			"owner": schema.StringAttribute{
				Computed:    true,
				Description: "Owner username. Null when the API omits the owner.",
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "Creation time in RFC3339 format.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Last update time in RFC3339 format.",
			},
		},
	}
}

func (r *CustomImageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.blockStorage, r.projectID = data.Server, data.BlockStorage, data.ProjectID
}

func (r *CustomImageResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config CustomImageResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateCustomImageInputs(ctx, config)...)
	// Terraform validates variable references before their values are known.
	name, id := reference.Get(config.Volume, reference.Name), reference.Get(config.Volume, reference.ID)
	if !name.IsUnknown() && !id.IsUnknown() {
		resp.Diagnostics.Append(validateCustomImageSource(config)...)
	}
}

func (r *CustomImageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var config, plan, state CustomImageResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// ModifyPlan cannot read replacement paths from attribute modifiers.
	// Reset the inferred source when a create-only value replaces the image.
	replacing := !plan.DiskFormat.Equal(state.DiskFormat) ||
		(!config.ContainerFormat.IsNull() && !plan.ContainerFormat.Equal(state.ContainerFormat))
	for _, criterion := range customImageVolumeCriteria {
		configured := reference.Get(config.Volume, criterion)
		replacing = replacing || (!configured.IsNull() && !reference.Get(plan.Volume, criterion).Equal(reference.Get(state.Volume, criterion)))
	}
	if !replacing {
		return
	}
	if config.Volume.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("volume"), types.ObjectUnknown(reference.AttributeTypes(customImageVolumeCriteria...)))...)
		return
	}
	if plan.Volume.IsUnknown() {
		return
	}
	for _, criterion := range customImageVolumeCriteria {
		if reference.Get(config.Volume, criterion).IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("volume").AtName(criterion), types.StringUnknown())...)
		}
	}
}

func (r *CustomImageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config CustomImageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateCustomImageSource(config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	volumeID, diags := r.resolveSourceVolume(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildCustomImageCreateBody(ctx, plan, volumeID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateCustomImage(ctx, serversdk.CreateCustomImageParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating custom image", err.Error())
		return
	}
	state := plan
	populateCustomImageResourceState(created, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	image, err := r.waitUntilReady(ctx, created.Id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for custom image", err.Error())
		return
	}
	current := CustomImageResourceModel{}
	populateCustomImageResourceState(image, &current)
	patch, changed, diags := buildCustomImageUpdateBody(ctx, plan, current)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if changed {
		image, err = r.client.PartialUpdateCustomImage(ctx, created.Id, serversdk.PartialUpdateCustomImageParams{ProjectID: r.projectID}, patch)
		if err != nil {
			resp.Diagnostics.AddError("Error configuring custom image", err.Error())
			return
		}
	}
	if image.Id != created.Id {
		resp.Diagnostics.AddError("Unexpected custom image identity", "The API returned a different image UUID.")
		return
	}
	state = plan
	populateCustomImageResourceState(image, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CustomImageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CustomImageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	image, err := r.get(ctx, id)
	if errors.Is(err, serversdk.ErrNotFound) || (err == nil && image.Status == serversdk.ImageStatusDeleted) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom image", err.Error())
		return
	}
	populateCustomImageResourceState(image, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CustomImageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state CustomImageResourceModel
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
	body, changed, diags := buildCustomImageUpdateBody(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var image *serversdk.CustomImageSchema
	var err error
	if changed {
		image, err = r.client.PartialUpdateCustomImage(ctx, id, serversdk.PartialUpdateCustomImageParams{ProjectID: r.projectID}, body)
	} else {
		image, err = r.get(ctx, id)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating custom image", err.Error())
		return
	}
	if image.Id != id {
		resp.Diagnostics.AddError("Unexpected custom image identity", "The API returned a different image UUID.")
		return
	}
	populateCustomImageResourceState(image, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomImageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CustomImageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteCustomImage(ctx, id, serversdk.DeleteCustomImageParams{ProjectID: r.projectID})
	if errors.Is(err, serversdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting custom image", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for custom image deletion", err.Error())
	}
}

func (r *CustomImageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	_, diags := parse.UUIDString(types.StringValue(req.ID), "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *CustomImageResource) get(ctx context.Context, id core.UUID) (*serversdk.CustomImageSchema, error) {
	return r.client.GetCustomImage(ctx, id, serversdk.GetCustomImageParams{ProjectID: r.projectID})
}

// resolveSourceVolume returns the UUID of the source volume that config
// selects. Only configured criteria select the volume, because the plan also
// carries the criterion that the provider infers.
func (r *CustomImageResource) resolveSourceVolume(ctx context.Context, config CustomImageResourceModel) (core.UUID, diag.Diagnostics) {
	diags := validateCustomImageSource(config)
	if diags.HasError() {
		return core.NilUUID, diags
	}
	name, volumeID := reference.Get(config.Volume, reference.Name), reference.Get(config.Volume, reference.ID)
	if !volumeID.IsNull() {
		id, idDiags := parse.UUIDString(volumeID, "volume.id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return core.NilUUID, diags
		}
		volume, err := r.blockStorage.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
		if err != nil {
			diags.AddError("Unable to read source volume", err.Error())
			return core.NilUUID, diags
		}
		return volume.Id, diags
	}
	if name.IsUnknown() || strings.TrimSpace(name.ValueString()) == "" {
		diags.AddError("Missing source volume name", "volume.name must be known and non-empty during creation.")
		return core.NilUUID, diags
	}
	trimmed := strings.TrimSpace(name.ValueString())
	volume, err := blockstoragelookup.NewVolumeFinder(r.blockStorage, r.projectID).Resolve(ctx, blockstoragelookup.VolumeFilter{Name: &trimmed})
	if err != nil {
		diags.AddError("Unable to resolve source volume", err.Error())
		return core.NilUUID, diags
	}
	return volume.Id, diags
}

func (r *CustomImageResource) waitUntilReady(ctx context.Context, id core.UUID) (*serversdk.CustomImageSchema, error) {
	w := wait.New[*serversdk.CustomImageSchema](customImagePollInterval, customImageTimeout)
	return w.WaitFor(ctx, func(ctx context.Context) (*serversdk.CustomImageSchema, error) {
		image, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		switch image.Status {
		case serversdk.ImageStatusActive:
			return image, nil
		case serversdk.ImageStatusKilled, serversdk.ImageStatusDeleted, serversdk.ImageStatusPendingDelete, serversdk.ImageStatusDeactivated, serversdk.ImageStatusProtected:
			return nil, fmt.Errorf("custom image %s reached status %q", id, image.Status)
		case serversdk.ImageStatusQueued, serversdk.ImageStatusSaving, serversdk.ImageStatusUploading, serversdk.ImageStatusImporting:
			return nil, fmt.Errorf("custom image %s has status %q: %w", id, image.Status, wait.ErrNotReady)
		default:
			return nil, fmt.Errorf("custom image %s has unsupported status %q", id, image.Status)
		}
	})
}

func (r *CustomImageResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[*serversdk.CustomImageSchema](customImagePollInterval, customImageTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (*serversdk.CustomImageSchema, error) {
		image, err := r.get(ctx, id)
		if errors.Is(err, serversdk.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if image.Status == serversdk.ImageStatusDeleted {
			return image, nil
		}
		if image.Status == serversdk.ImageStatusKilled {
			return nil, fmt.Errorf("custom image %s reached status killed during deletion", id)
		}
		return nil, fmt.Errorf("custom image %s still exists with status %q: %w", id, image.Status, wait.ErrNotReady)
	})
	return err
}

func validateCustomImageInputs(ctx context.Context, plan CustomImageResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if !plan.Name.IsUnknown() && (plan.Name.IsNull() || strings.TrimSpace(plan.Name.ValueString()) == "") {
		diags.AddError("Missing custom image input", "name must be non-empty.")
	}
	volumeName, volumeID := reference.Get(plan.Volume, reference.Name), reference.Get(plan.Volume, reference.ID)
	if !volumeName.IsNull() && !volumeName.IsUnknown() && strings.TrimSpace(volumeName.ValueString()) == "" {
		diags.AddError("Invalid source volume name", "volume.name must be non-empty.")
	}
	if !volumeID.IsNull() && !volumeID.IsUnknown() {
		_, idDiags := parse.UUIDString(volumeID, "volume.id")
		diags.Append(idDiags...)
	}
	if !plan.DiskFormat.IsUnknown() && (plan.DiskFormat.IsNull() || !serversdk.ImageCustomFormat(plan.DiskFormat.ValueString()).Valid()) {
		diags.AddError("Invalid disk format", "disk_format must be raw or qcow2.")
	}
	if !plan.ContainerFormat.IsNull() && !plan.ContainerFormat.IsUnknown() && !serversdk.ImageCustomContainerFormat(plan.ContainerFormat.ValueString()).Valid() {
		diags.AddError("Invalid container format", "container_format must be bare.")
	}
	if !plan.State.IsNull() && !plan.State.IsUnknown() && !serversdk.ImageState(plan.State.ValueString()).Valid() {
		diags.AddError("Invalid image state", "state must be enabled or disabled.")
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() {
		_, err := customImageMetadata(plan.Metadata.ValueString())
		if err != nil {
			diags.AddError("Invalid image metadata", err.Error())
		}
	}
	for _, value := range reference.Strings(plan.SharedProjects, reference.ID) {
		if !value.IsUnknown() {
			_, idDiags := parse.UUIDString(value, "shared_projects.id")
			diags.Append(idDiags...)
		}
	}
	return diags
}

func validateCustomImageSource(config CustomImageResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	name, id := reference.Get(config.Volume, reference.Name), reference.Get(config.Volume, reference.ID)
	if name.IsNull() == id.IsNull() {
		diags.AddError("Invalid custom image source", "Configure volume with exactly one of name or id.")
	}
	return diags
}

func buildCustomImageCreateBody(ctx context.Context, plan CustomImageResourceModel, volumeID core.UUID) (serversdk.CustomImageCreateSchema, diag.Diagnostics) {
	var body serversdk.CustomImageCreateSchema
	diags := validateCustomImageInputs(ctx, plan)
	for _, field := range []struct {
		name  string
		value types.String
	}{{"name", plan.Name}, {"disk_format", plan.DiskFormat}} {
		if field.value.IsUnknown() {
			diags.AddError("Unknown custom image input", field.name+" must be known during creation.")
		}
	}
	if diags.HasError() {
		return body, diags
	}
	body.Name = strings.TrimSpace(plan.Name.ValueString())
	body.VolumeId = volumeID
	body.DiskFormat = serversdk.ImageCustomFormat(plan.DiskFormat.ValueString())
	// Create requires a container format. The SDK exposes bare as its only value.
	body.ContainerFormat = serversdk.ImageCustomContainerFormatBare
	if !plan.ContainerFormat.IsNull() && !plan.ContainerFormat.IsUnknown() {
		body.ContainerFormat = serversdk.ImageCustomContainerFormat(plan.ContainerFormat.ValueString())
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	return body, diags
}

func buildCustomImageUpdateBody(ctx context.Context, plan, state CustomImageResourceModel) (serversdk.CustomImagePartialUpdateSchema, bool, diag.Diagnostics) {
	var body serversdk.CustomImagePartialUpdateSchema
	var diags diag.Diagnostics
	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing custom image name", "name must be known and non-empty.")
		return body, false, diags
	}
	name := strings.TrimSpace(plan.Name.ValueString())
	if name != strings.TrimSpace(state.Name.ValueString()) {
		body.Name = &name
	}
	for _, field := range []struct {
		plan, state types.String
		target      **string
	}{{plan.Description, state.Description, &body.Description}, {plan.OS, state.OS, &body.Os}, {plan.Version, state.Version, &body.Version}} {
		if !field.plan.IsNull() && !field.plan.IsUnknown() && !field.plan.Equal(field.state) {
			value := field.plan.ValueString()
			*field.target = &value
		}
	}
	if !plan.State.IsNull() && !plan.State.IsUnknown() && !plan.State.Equal(state.State) {
		value := serversdk.ImageState(plan.State.ValueString())
		if !value.Valid() {
			diags.AddError("Invalid image state", "state must be enabled or disabled.")
		} else {
			body.State = &value
		}
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && !compare.SameJSON(plan.Metadata, state.Metadata) {
		value, err := customImageMetadata(plan.Metadata.ValueString())
		if err != nil {
			diags.AddError("Invalid image metadata", err.Error())
		} else {
			body.Metadata = &value
		}
	}
	if !plan.SharedProjects.IsNull() && !plan.SharedProjects.IsUnknown() &&
		!compare.SameUUIDSet(customImageSharedProjectIDSet(plan.SharedProjects), customImageSharedProjectIDSet(state.SharedProjects)) {
		values := reference.Strings(plan.SharedProjects, reference.ID)
		ids := make([]core.UUID, 0, len(values))
		for _, value := range values {
			id, idDiags := parse.UUIDString(value, "shared_projects.id")
			diags.Append(idDiags...)
			ids = append(ids, id)
		}
		body.SharedProjectIds = &ids
	}
	changed := body.Name != nil || body.Description != nil || body.Os != nil || body.Version != nil || body.State != nil || body.Metadata != nil || body.SharedProjectIds != nil
	return body, changed, diags
}

// customImageMetadata enforces the public PATCH schema's string-only values.
// Other resources do not need this conversion.
func customImageMetadata(raw string) (map[string]string, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return nil, fmt.Errorf("metadata must be a JSON object with string values")
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		var text string
		if string(item) == "null" {
			return nil, fmt.Errorf("metadata value %q must be a string", key)
		}
		if err := json.Unmarshal(item, &text); err != nil {
			return nil, fmt.Errorf("metadata value %q must be a string", key)
		}
		result[key] = text
	}
	return result, nil
}
