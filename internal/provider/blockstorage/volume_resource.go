package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &VolumeResource{}
	_ resource.ResourceWithImportState    = &VolumeResource{}
	_ resource.ResourceWithValidateConfig = &VolumeResource{}
	_ resource.ResourceWithModifyPlan     = &VolumeResource{}
)

const (
	defaultVolumePollInterval = 10 * time.Second
	defaultVolumeTimeout      = 30 * time.Minute
)

type VolumeResource struct {
	client        *blockstoragesdk.Client
	server        *serversdk.Client
	projectClient *projectsdk.Client
	projectID     core.UUID
	pollInterval  time.Duration
	timeout       time.Duration
}

// volumeUpdatePlan splits an update into the partial-update body and the
// actions that have to travel separately. A resize is kept out of the body on
// purpose: the backend grows a volume through its own asynchronous extend
// action. A size sent in the patch body is accepted without ever taking
// effect, which surfaces as an inconsistent-result error once the volume is
// read back at its old size.
type volumeUpdatePlan struct {
	body       blockstoragesdk.VolumePartialUpdateSchema
	patch      bool
	extendSize *int
	// retypeSelector holds changed criteria, or null when the type is unchanged.
	// The backend applies a retype through its own action, like a resize.
	retypeSelector types.Object
}

func NewVolumeResource() resource.Resource {
	return &VolumeResource{}
}

func (r *VolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (r *VolumeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	replaceReference := []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	useStateForUnknown := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	imageSelector := serverselector.ImageAttribute()
	imageSelector.Description += " Valid only for image sources. Selecting a different image replaces the volume."

	volumeTypeSelector := blockstorageselector.VolumeTypeAttribute()
	volumeTypeSelector.Description += " Required except for snapshot sources. Selecting a different type retypes the volume in place."

	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Block Storage volume.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Volume ID (UUID).",
				PlanModifiers: useStateForUnknown,
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the volume.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional volume description. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"size": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Volume size in GiB. Required except for snapshot sources; it can only be increased after creation.",
			},
			"iops": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "Provisioned IOPS. Omitting it preserves the backend value.",
			},
			"status": schema.StringAttribute{Computed: true, Description: "Current volume status."},
			// bootable, encrypted, zone, zone_id, and project_id are fixed when
			// the volume is created, so keeping the prior value out of the plan
			// only adds "(known after apply)" noise. create_from.volume_type is
			// the one attribute that changes in place, and a retype could move
			// zone or encryption with it. ModifyPlan therefore returns those
			// two to unknown whenever the plan contains a retype.
			"bootable": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether the volume is bootable.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"encrypted": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether the volume is encrypted.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"zone": schema.SingleNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Zone where the volume is created. Changing it replaces the volume. Removing it keeps the current zone. Do not set it for snapshot sources.",
				Attributes:  reference.PreservedAttributes(reference.Name, "Zone name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
					objectplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"zone_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Zone ID (UUID) where the volume resides.",
				PlanModifiers: useStateForUnknown,
			},
			"volume_type": schema.StringAttribute{Computed: true, Description: "Effective volume type name."},
			"project_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Project ID (UUID) owning the volume.",
				PlanModifiers: useStateForUnknown,
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "Timestamp when the volume was created (RFC3339).",
				PlanModifiers: useStateForUnknown,
			},
			"updated_at": schema.StringAttribute{Computed: true, Description: "Timestamp when the volume was last updated (RFC3339)."},
			"create_from": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Immutable volume source. Configure only fields used by source_type.",
				Attributes: map[string]schema.Attribute{
					"source_type": schema.StringAttribute{
						Required:      true,
						Description:   "Source type: empty, image, custom_image, snapshot, or backup.",
						PlanModifiers: requiresReplace,
					},
					"image": imageSelector,
					"custom_image": schema.SingleNestedAttribute{
						Optional:      true,
						Description:   "Source custom image; valid only for a custom_image source.",
						Attributes:    reference.Attributes(reference.ID, "Custom image ID (UUID)."),
						PlanModifiers: replaceReference,
					},
					"snapshot": schema.SingleNestedAttribute{
						Optional:      true,
						Description:   "Source snapshot; valid only for a snapshot source.",
						Attributes:    reference.Attributes(reference.ID, "Snapshot ID (UUID)."),
						PlanModifiers: replaceReference,
					},
					"backup": schema.SingleNestedAttribute{
						Optional:      true,
						Description:   "Source backup; valid only for a backup source.",
						Attributes:    reference.Attributes(reference.ID, "Backup ID (UUID)."),
						PlanModifiers: replaceReference,
					},
					// The backend retypes a volume in place, so changing this
					// selector updates the volume instead of replacing it.
					"volume_type": volumeTypeSelector,
				},
			},
		},
	}
}

func (r *VolumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.BlockStorage
	r.server = data.Server
	r.projectClient = data.Project
	r.projectID = data.ProjectID
}

func (r *VolumeResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config VolumeResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == "" {
		resp.Diagnostics.AddError("Missing volume name", "name must be configured, known, and non-empty.")
	}
	// Rejecting surrounding whitespace keeps zone.name comparable as a plain
	// string, so RequiresReplaceIfConfigured cannot replace a volume over padding.
	if zone := reference.Get(config.Zone, reference.Name); !zone.IsNull() && !zone.IsUnknown() {
		if name := zone.ValueString(); strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			resp.Diagnostics.AddAttributeError(path.Root("zone").AtName(reference.Name), "Invalid volume zone", "zone.name must be non-empty and have no leading or trailing whitespace when configured.")
		}
	}
	resp.Diagnostics.Append(validateVolumeCreateFromConfig(ctx, config.CreateFrom, config.Size)...)
	resp.Diagnostics.Append(validateVolumeSnapshotZone(ctx, config.Zone, config.CreateFrom)...)
}

// ModifyPlan checks create-time constraints before a replacement can destroy
// the prior volume. It also withdraws computed values that can change.
//
// resp.RequiresReplace cannot be read to recognize a replacement. The framework
// hands this method a response whose path list starts empty and appends the
// attribute plan modifiers' paths only after it returns, so the list is always
// empty here. The immutable source fields are compared directly instead.
func (r *VolumeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan VolumeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var config VolumeResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateCreate := func() {
		// size is Optional+Computed, so the proposed plan can carry the prior
		// state's value even when the practitioner omitted it. The backend's
		// snapshot create rule applies to configuration, not that carried value.
		resp.Diagnostics.Append(validateVolumeCreateSize(ctx, plan.CreateFrom, config.Size)...)
		resp.Diagnostics.Append(validateVolumeSnapshotZone(ctx, config.Zone, plan.CreateFrom)...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(r.validateVolumeCreatePlacement(ctx, plan, config.Zone)...)
	}

	if req.State.Raw.IsNull() {
		validateCreate()
		return
	}

	var state VolumeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	planned, plannedDiags := volumeCreateFromModel(ctx, plan.CreateFrom)
	resp.Diagnostics.Append(plannedDiags...)
	current, currentDiags := volumeCreateFromModel(ctx, state.CreateFrom)
	resp.Diagnostics.Append(currentDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A replacement destroys this volume and creates another one, so the in-place
	// size rules do not apply to it: the new volume is allowed to be smaller.
	// Reporting the create rules here as well fails the plan one pass before
	// Terraform plans the create half against a null prior state.
	if volumeSourceReplaced(planned, current) {
		validateCreate()
		if resp.Diagnostics.HasError() {
			return
		}
		if config.Zone.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone"), types.ObjectUnknown(reference.AttributeTypes(reference.Name)))...)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone_id"), types.StringUnknown())...)
		return
	}
	// This mirrors RequiresReplaceIfConfigured on zone, because ModifyPlan cannot
	// read the replacement paths that the attribute modifiers report.
	if !config.Zone.IsNull() && !plan.Zone.Equal(state.Zone) {
		validateCreate()
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone_id"), types.StringUnknown())...)
		return
	}

	imageChanged, typeChanged := r.planSelectors(ctx, req, resp, plan, state, config.Zone)
	if resp.Diagnostics.HasError() {
		return
	}
	if imageChanged {
		validateCreate()
		if resp.Diagnostics.HasError() {
			return
		}
		if config.Zone.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone"), types.ObjectUnknown(reference.AttributeTypes(reference.Name)))...)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone_id"), types.StringUnknown())...)
		return
	}
	// A retype is the one in-place path that can move a value the schema
	// otherwise freezes with UseStateForUnknown. Leaving the prior zone or
	// encryption flag in the plan would fail the apply with an inconsistent
	// result after the volume has already been retyped. The plan therefore
	// stops claiming to know them.
	if typeChanged {
		// A configured zone stays in the plan because Terraform cannot turn a
		// configured value into an unknown one. Retype lookup uses its zone ID.
		if config.Zone.IsNull() {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone"), types.ObjectUnknown(reference.AttributeTypes(reference.Name)))...)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("zone_id"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("encrypted"), types.BoolUnknown())...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if plan.Size.IsNull() || plan.Size.IsUnknown() || state.Size.IsNull() || state.Size.IsUnknown() {
		return
	}
	if plan.Size.ValueInt64() < state.Size.ValueInt64() {
		resp.Diagnostics.AddAttributeError(
			path.Root("size"),
			"Invalid volume size update",
			fmt.Sprintf("size can only increase from %d GiB; planned value is %d GiB. Replace the volume to make it smaller.", state.Size.ValueInt64(), plan.Size.ValueInt64()),
		)
	}
}

func (r *VolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var configuredZone types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("zone"), &configuredZone)...)
	if configuredZone.IsUnknown() {
		configuredZone = plan.Zone
	}
	resp.Diagnostics.Append(validateVolumeSnapshotZone(ctx, configuredZone, plan.CreateFrom)...)
	if resp.Diagnostics.HasError() {
		return
	}

	zoneID, zoneDiags := r.resolveZoneID(ctx, configuredZone)
	resp.Diagnostics.Append(zoneDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	imageFinder := serverlookup.NewImageFinder(r.server, r.projectID)
	volumeTypeFinder := blockstoragelookup.NewVolumeTypeFinder(r.client)
	body, diags := buildVolumeCreateBody(ctx, plan, zoneID, imageFinder.Resolve, volumeTypeFinder.ResolveForCreate)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(recordVolumeSelectorIDs(ctx, body, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	volume, err := r.client.CreateVolume(ctx, blockstoragesdk.CreateVolumeParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume", err.Error())
		return
	}

	populateVolumePendingState(&plan, volume.Id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.waitUntilReady(ctx, volume.Id); err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for volume creation",
			fmt.Sprintf("%s\n\nVolume ID %s was preserved in state to avoid creating an untracked duplicate.", err, volume.Id),
		)
		return
	}

	volume, err = r.get(ctx, volume.Id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume after creation", err.Error())
		return
	}
	resp.Diagnostics.Append(populateVolumeResourceState(ctx, volume, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VolumeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	volumeID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	volume, err := r.get(ctx, volumeID)
	if err != nil {
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading volume", err.Error())
		return
	}

	resp.Diagnostics.Append(populateVolumeResourceState(ctx, volume, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan VolumeResourceModel
	var state VolumeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	volumeID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	update, diags := buildVolumeUpdatePlan(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if update.patch {
		if _, err := r.client.UpdateVolume(ctx, volumeID, blockstoragesdk.UpdateVolumeParams{ProjectID: r.projectID}, update.body); err != nil {
			resp.Diagnostics.AddError("Error updating volume", err.Error())
			return
		}
		if err := r.waitUntilPatched(ctx, volumeID, update.body); err != nil {
			resp.Diagnostics.AddError("Error waiting for volume update", err.Error())
			return
		}
	}
	// The retype is resolved against the size the volume ends up with, so a type
	// too small for the planned growth is reported before anything is changed.
	if !update.retypeSelector.IsNull() {
		var configuredZone types.Object
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("zone"), &configuredZone)...)
		if resp.Diagnostics.HasError() {
			return
		}
		var zoneID *core.UUID
		if !configuredZone.IsNull() {
			id, idDiags := parse.UUIDString(state.ZoneID, "zone_id")
			resp.Diagnostics.Append(idDiags...)
			if resp.Diagnostics.HasError() {
				return
			}
			zoneID = &id
		}
		resolved, typeDiags := r.retype(ctx, volumeID, update.retypeSelector, volumeTargetSize(plan, state), zoneID, state.CreateFrom)
		resp.Diagnostics.Append(typeDiags...)
		if !typeDiags.HasError() {
			source, sourceDiags := volumeCreateFromModel(ctx, plan.CreateFrom)
			resp.Diagnostics.Append(sourceDiags...)
			if !sourceDiags.HasError() {
				resp.Diagnostics.Append(setVolumeSelectors(ctx, &plan.CreateFrom, source.Image, resolved)...)
			}
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if update.extendSize != nil {
		body := blockstoragesdk.VolumeExtendSchema{Size: *update.extendSize}
		if err := r.client.ExtendVolume(ctx, volumeID, blockstoragesdk.ExtendVolumeParams{ProjectID: r.projectID}, body); err != nil {
			resp.Diagnostics.AddError("Error resizing volume", err.Error())
			return
		}
		if err := r.waitUntilResized(ctx, volumeID, *update.extendSize); err != nil {
			resp.Diagnostics.AddError("Error waiting for volume resize", err.Error())
			return
		}
	}

	volume, err := r.get(ctx, volumeID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume after update", err.Error())
		return
	}
	newState := plan
	resp.Diagnostics.Append(populateVolumeResourceState(ctx, volume, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *VolumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VolumeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	volumeID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteVolume(ctx, volumeID, blockstoragesdk.DeleteVolumeParams{ProjectID: r.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting volume", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, volumeID); err != nil {
		resp.Diagnostics.AddError("Error waiting for volume deletion", err.Error())
	}
}

// ImportState accepts the volume ID on its own, or the composite
// "<volume_id>,<source_type>" form. The second form exists because the backend
// can report several origins for one volume, and Read only ever sees prior
// state. Without a source type carried in through the import ID, a
// practitioner has no way to say which origin the volume was created from.
func (r *VolumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	rawID, rawSourceType, composite := strings.Cut(req.ID, ",")
	volumeID := strings.TrimSpace(rawID)
	if volumeID == "" {
		resp.Diagnostics.AddError(
			"Invalid volume import ID",
			fmt.Sprintf("Expected \"<volume_id>\" or \"<volume_id>,<source_type>\", got %q.", req.ID),
		)
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(volumeID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), volumeID)...)
	if !composite || resp.Diagnostics.HasError() {
		return
	}

	sourceType := strings.TrimSpace(rawSourceType)
	if !validVolumeSourceType(sourceType) {
		resp.Diagnostics.AddError(
			"Invalid volume import source type",
			fmt.Sprintf("%q is not a supported create_from.source_type. Supported values are: empty, image, custom_image, snapshot, backup.", sourceType),
		)
		return
	}
	createFrom, diags := volumeCreateFromObject(
		map[string]types.String{"source_type": types.StringValue(sourceType)},
		types.ObjectNull(serverselector.ImageAttributeTypes()),
		types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes()),
	)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Read fills in the rest of create_from from the response; source_type only
	// has to be present so it can pick the matching origin.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("create_from"), createFrom)...)
}

// planSelectors decides replacement and retype from catalog identity.
func (r *VolumeResource) planSelectors(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse, plan, state VolumeResourceModel, configuredZone types.Object) (bool, bool) {
	planned, diags := volumeCreateFromModel(ctx, plan.CreateFrom)
	resp.Diagnostics.Append(diags...)
	prior, diags := volumeCreateFromModel(ctx, state.CreateFrom)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return false, false
	}
	decide := func(planned, prior types.Object, field string, image bool) bool {
		if planned.IsNull() && prior.IsNull() {
			return false
		}
		p := path.Root("create_from").AtName(field)
		find := func(ctx context.Context) ([]core.UUID, error) {
			var ids []core.UUID
			if image {
				filter, diags := serverselector.ImageFilter(ctx, planned, p.String())
				if diags.HasError() {
					return nil, fmt.Errorf("%v", diags)
				}
				candidates, err := serverlookup.NewImageFinder(r.server, r.projectID).Find(ctx, filter)
				for _, candidate := range candidates {
					ids = append(ids, candidate.Id)
				}
				return ids, err
			}
			filter, diags := blockstorageselector.VolumeTypeFilter(ctx, planned, p.String())
			if diags.HasError() {
				return nil, fmt.Errorf("%v", diags)
			}
			zoneID, diags := r.resolveZoneID(ctx, configuredZone)
			if diags.HasError() {
				return nil, fmt.Errorf("%v", diags)
			}
			filter.ZoneID = zoneID
			candidates, err := blockstoragelookup.NewVolumeTypeFinder(r.client).Find(ctx, filter)
			for _, candidate := range candidates {
				ids = append(ids, candidate.Id)
			}
			return ids, err
		}
		current := func(ctx context.Context) (core.UUID, error) {
			id, err := core.ParseUUID(state.ID.ValueString())
			if err != nil {
				return core.UUID{}, err
			}
			volume, err := r.get(ctx, id)
			if err != nil {
				return core.UUID{}, err
			}
			if volume != nil {
				if image && volume.CreateFrom.Image != nil {
					return volume.CreateFrom.Image.Id, nil
				}
				if !image && volume.CreateFrom.VolumeType != nil {
					return volume.CreateFrom.VolumeType.Id, nil
				}
			}
			return core.UUID{}, fmt.Errorf("volume %s: %w", field, selector.ErrNoIdentity)
		}
		decision, id, diags := selector.Decide(ctx, planned, prior, p.String(), find, current)
		resp.Diagnostics.Append(diags...)
		if image && decision != selector.Same {
			resp.RequiresReplace = append(resp.RequiresReplace, p)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, p, selector.WithID(planned, id))...)
		return decision != selector.Same
	}
	if plan.CreateFrom.IsUnknown() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("create_from").AtName("image"))
		return true, true
	}
	imageChanged := decide(planned.Image, prior.Image, "image", true)
	if resp.Diagnostics.HasError() {
		return imageChanged, false
	}
	typeChanged := decide(planned.VolumeType, prior.VolumeType, "volume_type", false)
	return imageChanged, typeChanged
}

func (r *VolumeResource) get(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeDetailSchema, error) {
	return r.client.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: r.projectID})
}

// resolveZoneID resolves an optional configured zone name to its ID, so a
// create_from.volume_type name that exists in more than one zone can be
// narrowed to the zone the volume is meant to land in. A volume with no
// configured zone resolves its type across every zone, as before.
func (r *VolumeResource) resolveZoneID(ctx context.Context, zone types.Object) (*core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	zoneName := reference.Get(zone, reference.Name)
	if zoneName.IsNull() || zoneName.IsUnknown() {
		return nil, diags
	}
	name := strings.TrimSpace(zoneName.ValueString())
	if name == "" {
		return nil, diags
	}
	resolved, err := projectlookup.NewZoneFinder(r.projectClient, r.projectID).Resolve(ctx, projectlookup.ZoneFilter{Name: &name})
	if err != nil {
		diags.AddError("Unable to resolve zone", fmt.Sprintf("zone.name: %s", err))
		return nil, diags
	}
	return &resolved.Id, diags
}

// validateVolumeCreatePlacement checks a configured zone and its volume type
// before Terraform destroys a volume for replacement. Create resolves them
// again because the catalogue can change between plan and apply.
func (r *VolumeResource) validateVolumeCreatePlacement(ctx context.Context, plan VolumeResourceModel, zone types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if zoneName := reference.Get(zone, reference.Name); zoneName.IsNull() || zoneName.IsUnknown() || strings.TrimSpace(zoneName.ValueString()) == "" {
		return diags
	}
	source, sourceDiags := volumeCreateFromModel(ctx, plan.CreateFrom)
	diags.Append(sourceDiags...)
	if diags.HasError() || source.SourceType.IsNull() || source.SourceType.IsUnknown() ||
		strings.TrimSpace(source.SourceType.ValueString()) == volumeSourceTypeSnapshot {
		return diags
	}
	zoneID, zoneDiags := r.resolveZoneID(ctx, zone)
	diags.Append(zoneDiags...)
	if diags.HasError() {
		return diags
	}
	if source.VolumeType.IsNull() || selector.HasUnknown(source.VolumeType) || plan.Size.IsNull() || plan.Size.IsUnknown() {
		return diags
	}
	_, typeDiags := blockstorageselector.ResolveVolumeType(ctx, source.VolumeType, "create_from.volume_type", zoneID, int(plan.Size.ValueInt64()), blockstoragelookup.NewVolumeTypeFinder(r.client).ResolveForCreate)
	diags.Append(typeDiags...)
	return diags
}

// retype changes the volume type through the dedicated backend action.
// The configured criteria and zone select the target without replacing the
// volume that holds the data.
func (r *VolumeResource) retype(ctx context.Context, volumeID core.UUID, value types.Object, size int, zoneID *core.UUID, prior types.Object) (types.Object, diag.Diagnostics) {
	volumeType, diags := blockstorageselector.ResolveVolumeType(ctx, value, "create_from.volume_type", zoneID, size, blockstoragelookup.NewVolumeTypeFinder(r.client).ResolveForCreate)
	if diags.HasError() {
		return value, diags
	}
	source, sourceDiags := volumeCreateFromModel(ctx, prior)
	diags.Append(sourceDiags...)
	if diags.HasError() {
		return value, diags
	}
	currentID, known := selector.ID(source.VolumeType)
	if !known {
		volume, err := r.get(ctx, volumeID)
		if err != nil {
			diags.AddError("Unable to read volume type identity", err.Error())
			return value, diags
		}
		if volume == nil || volume.CreateFrom.VolumeType == nil {
			diags.AddError("Unable to read volume type identity", "The volume response does not identify its volume type.")
			return value, diags
		}
		currentID = volume.CreateFrom.VolumeType.Id
	}
	value = selector.WithID(value, types.StringValue(volumeType.Id.String()))
	if currentID == volumeType.Id {
		return value, diags
	}
	body := blockstoragesdk.VolumeRetypeSchema{VolumeTypeId: volumeType.Id}
	if err := r.client.RetypeVolume(ctx, volumeID, blockstoragesdk.RetypeVolumeParams{ProjectID: r.projectID}, body); err != nil {
		diags.AddError("Error retyping volume", err.Error())
		return value, diags
	}
	if err := r.waitUntilRetyped(ctx, volumeID, volumeType.Id); err != nil {
		diags.AddError("Error waiting for volume retype", err.Error())
	}
	return value, diags
}

func (r *VolumeResource) waitUntilReady(ctx context.Context, id core.UUID) error {
	_, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		if volumeStatusReady(volume.Status) {
			return volume, nil
		}
		if volumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %q", id, volume.Status)
		}
		return nil, fmt.Errorf("status=%s: %w", volume.Status, wait.ErrNotReady)
	})
	return volumeWaitError(err, id, "become ready")
}

// waitUntilPatched waits until the volume reports the patched fields. A ready
// status proves nothing on its own: the volume keeps reporting the values it
// already had until the backend applies the change. A waiter that stops there
// hands the caller stale values, which Terraform rejects as an inconsistent
// result. The fields still outstanding are named in the not-ready reason,
// which the waiter carries into its timeout error, so a backend that never
// applies one is identifiable.
func (r *VolumeResource) waitUntilPatched(ctx context.Context, id core.UUID, body blockstoragesdk.VolumePartialUpdateSchema) error {
	_, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		outstanding := unappliedVolumePatchFields(volume, body)
		if volumeStatusReady(volume.Status) && len(outstanding) == 0 {
			return volume, nil
		}
		if volumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %q while updating", id, volume.Status)
		}
		if len(outstanding) == 0 {
			return nil, fmt.Errorf("status=%s: %w", volume.Status, wait.ErrNotReady)
		}
		return nil, fmt.Errorf(
			"status=%s, the volume still reports the previous %s: %w",
			volume.Status, strings.Join(outstanding, ", "), wait.ErrNotReady,
		)
	})
	return volumeWaitError(err, id, "apply the requested changes")
}

// waitUntilRetyped waits until the volume reports the new volume type. Like
// the extend action, retype only returns "accepted", so the effective volume
// type is the observable that settles rather than the status.
func (r *VolumeResource) waitUntilRetyped(ctx context.Context, id core.UUID, typeID core.UUID) error {
	_, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		if volumeStatusReady(volume.Status) && volume.CreateFrom.VolumeType != nil && volume.CreateFrom.VolumeType.Id == typeID {
			return volume, nil
		}
		if volumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %q while retyping to %q", id, volume.Status, typeID)
		}
		current := ""
		if volume.CreateFrom.VolumeType != nil {
			current = volume.CreateFrom.VolumeType.Id.String()
		}
		return nil, fmt.Errorf("status=%s, volume_type=%q: %w", volume.Status, current, wait.ErrNotReady)
	})
	return volumeWaitError(err, id, fmt.Sprintf("become volume type %s", typeID))
}

// waitUntilResized waits until the volume reports the new size. A ready status
// is not proof on its own: the extend call only returns "accepted", and the
// volume keeps reporting the status it already had until the resize actually
// starts. A waiter that stops there returns before the new size exists, and
// the caller stores the old one. The reported size is the observable that
// settles. A backend that overshoots the requested size ends the wait too,
// rather than polling to the timeout; the honest value is then stored and
// Terraform reports the mismatch.
func (r *VolumeResource) waitUntilResized(ctx context.Context, id core.UUID, size int) error {
	_, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		if volumeStatusReady(volume.Status) && volume.Size >= size {
			return volume, nil
		}
		if volumeStatusFailed(volume.Status) {
			return nil, fmt.Errorf("volume %s entered terminal status %q while resizing to %d GiB", id, volume.Status, size)
		}
		return nil, fmt.Errorf("status=%s, size=%d GiB: %w", volume.Status, volume.Size, wait.ErrNotReady)
	})
	return volumeWaitError(err, id, fmt.Sprintf("reach %d GiB", size))
}

func (r *VolumeResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	// The volume may still report its previous status for a poll or two after
	// the delete call is accepted. A ready status therefore only means the
	// deletion failed once deleting has actually been observed.
	sawDeleting := false
	_, err := r.newWaiter().WaitFor(ctx, func(ctx context.Context) (*blockstoragesdk.VolumeDetailSchema, error) {
		volume, err := r.get(ctx, id)
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			// The volume is gone, which is the whole point of this wait; there
			// is no value left to hand back.
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("poll volume %s: %w", id, err)
		}
		if volume.Status == blockstoragesdk.VolumeStatusDeleting {
			sawDeleting = true
		}
		if volumeStatusFailed(volume.Status) || (sawDeleting && volumeStatusReady(volume.Status)) {
			return nil, fmt.Errorf("volume %s stopped deleting and settled in status %q", id, volume.Status)
		}
		return nil, fmt.Errorf("status=%s: %w", volume.Status, wait.ErrNotReady)
	})
	return volumeWaitError(err, id, "be deleted")
}

// newWaiter builds the waiter every volume poll runs on. Block storage actions
// run longer than the wait package assumes, so the volume defaults stand in for
// unset values rather than the package ones.
func (r *VolumeResource) newWaiter() *wait.Waiter[*blockstoragesdk.VolumeDetailSchema] {
	interval := r.pollInterval
	if interval <= 0 {
		interval = defaultVolumePollInterval
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultVolumeTimeout
	}
	return wait.New[*blockstoragesdk.VolumeDetailSchema](interval, timeout)
}

func volumeWaitError(err error, id core.UUID, action string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("waiting for volume %s to %s: %w", id, action, err)
}

// recordVolumeSelectorIDs records resolved create identities before refresh.
func recordVolumeSelectorIDs(ctx context.Context, body blockstoragesdk.VolumeCreateSchema, plan *VolumeResourceModel) diag.Diagnostics {
	source, diags := volumeCreateFromModel(ctx, plan.CreateFrom)
	if diags.HasError() {
		return diags
	}
	value, err := body.CreateFrom.ValueByDiscriminator()
	if err != nil {
		diags.AddError("Unable to read resolved volume source", err.Error())
		return diags
	}
	var imageID, volumeTypeID *core.UUID
	switch value := value.(type) {
	case blockstoragesdk.VolumeCreateFromImageSchema:
		imageID, volumeTypeID = &value.ImageId, &value.VolumeTypeId
	case blockstoragesdk.VolumeCreateFromCustomImageSchema:
		volumeTypeID = &value.VolumeTypeId
	case blockstoragesdk.VolumeCreateFromBackupSchema:
		volumeTypeID = &value.VolumeTypeId
	case blockstoragesdk.VolumeCreateEmptySchema:
		volumeTypeID = &value.VolumeTypeId
	}
	if imageID != nil {
		source.Image = selector.WithID(source.Image, types.StringValue(imageID.String()))
	}
	if volumeTypeID != nil {
		source.VolumeType = selector.WithID(source.VolumeType, types.StringValue(volumeTypeID.String()))
	}
	diags.Append(setVolumeSelectors(ctx, &plan.CreateFrom, source.Image, source.VolumeType)...)
	return diags
}

// setVolumeSelectors replaces the image and volume type selectors in
// createFrom and keeps its other attributes.
func setVolumeSelectors(ctx context.Context, createFrom *types.Object, image, volumeType types.Object) diag.Diagnostics {
	var model VolumeCreateFromModel
	diags := createFrom.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return diags
	}
	model.Image, model.VolumeType = image, volumeType
	value, valueDiags := types.ObjectValueFrom(ctx, volumeCreateFromAttributeTypes(), model)
	diags.Append(valueDiags...)
	if !diags.HasError() {
		*createFrom = value
	}
	return diags
}

func buildVolumeCreateBody(
	ctx context.Context,
	plan VolumeResourceModel,
	zoneID *core.UUID,
	resolveImage serverlookup.ImageResolveFunc,
	resolveVolumeType blockstoragelookup.VolumeTypeResolveFunc,
) (blockstoragesdk.VolumeCreateSchema, diag.Diagnostics) {
	var body blockstoragesdk.VolumeCreateSchema
	var diags diag.Diagnostics
	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing volume name", "name must be configured, known, and non-empty.")
		return body, diags
	}

	createFrom, sourceDiags := buildVolumeCreateFrom(ctx, plan.CreateFrom, plan.Size, zoneID, resolveImage, resolveVolumeType)
	diags.Append(sourceDiags...)
	if diags.HasError() {
		return body, diags
	}

	body.Name = strings.TrimSpace(plan.Name.ValueString())
	body.CreateFrom = createFrom
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	if !plan.IOPS.IsNull() && !plan.IOPS.IsUnknown() {
		value := int(plan.IOPS.ValueInt64())
		body.Iops = &value
	}
	return body, diags
}

func buildVolumeUpdatePlan(ctx context.Context, plan, state VolumeResourceModel) (volumeUpdatePlan, diag.Diagnostics) {
	var update volumeUpdatePlan
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing volume name", "name must be configured, known, and non-empty.")
		return volumeUpdatePlan{}, diags
	}
	planned, plannedDiags := volumeCreateFromModel(ctx, plan.CreateFrom)
	diags.Append(plannedDiags...)
	if diags.HasError() {
		return volumeUpdatePlan{}, diags
	}
	if !planned.VolumeType.IsNull() {
		_, selectorDiags := blockstorageselector.VolumeTypeFilter(ctx, planned.VolumeType, "create_from.volume_type")
		diags.Append(selectorDiags...)
		if diags.HasError() {
			return volumeUpdatePlan{}, diags
		}
	}
	update.retypeSelector = volumeRetypeSelector(planned)
	if !plan.Name.Equal(state.Name) {
		value := strings.TrimSpace(plan.Name.ValueString())
		update.body.Name = &value
		update.patch = true
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		value := plan.Description.ValueString()
		update.body.Description = &value
		update.patch = true
	}
	if !plan.IOPS.IsNull() && !plan.IOPS.IsUnknown() && !plan.IOPS.Equal(state.IOPS) {
		value := int(plan.IOPS.ValueInt64())
		update.body.Iops = &value
		update.patch = true
	}
	if !plan.Size.IsNull() && !plan.Size.IsUnknown() && !plan.Size.Equal(state.Size) {
		if plan.Size.ValueInt64() <= state.Size.ValueInt64() {
			diags.AddError("Invalid volume size update", fmt.Sprintf("size can only increase from %d GiB; planned value is %d GiB.", state.Size.ValueInt64(), plan.Size.ValueInt64()))
		} else {
			value := int(plan.Size.ValueInt64())
			update.extendSize = &value
		}
	}
	return update, diags
}

func unappliedVolumePatchFields(volume *blockstoragesdk.VolumeDetailSchema, body blockstoragesdk.VolumePartialUpdateSchema) []string {
	var fields []string
	if body.Name != nil && volume.Name != *body.Name {
		fields = append(fields, "name")
	}
	// Clearing a description is configured as an empty string, and the backend
	// may echo that back as an omitted field; both mean cleared.
	if body.Description != nil {
		current := ""
		if volume.Description != nil {
			current = *volume.Description
		}
		if current != *body.Description {
			fields = append(fields, "description")
		}
	}
	if body.Iops != nil && (volume.Iops == nil || *volume.Iops != *body.Iops) {
		fields = append(fields, "iops")
	}
	return fields
}

// volumeTargetSize is the size the volume ends the apply on. That is the
// planned size when the configuration owns it, and the current size when it
// does not, as with a snapshot source that never configured a size.
func volumeTargetSize(plan, state VolumeResourceModel) int {
	if !plan.Size.IsNull() && !plan.Size.IsUnknown() {
		return int(plan.Size.ValueInt64())
	}
	return int(state.Size.ValueInt64())
}

func populateVolumePendingState(state *VolumeResourceModel, id core.UUID) {
	state.ID = types.StringValue(id.String())
	if state.Description.IsUnknown() {
		state.Description = types.StringNull()
	}
	if state.Size.IsUnknown() {
		state.Size = types.Int64Null()
	}
	if state.IOPS.IsUnknown() {
		state.IOPS = types.Int64Null()
	}
	state.Status = types.StringNull()
	state.Bootable = types.BoolNull()
	state.Encrypted = types.BoolNull()
	if state.Zone.IsUnknown() {
		state.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
	}
	state.ZoneID = types.StringNull()
	state.VolumeType = types.StringNull()
	state.ProjectID = types.StringNull()
	state.CreatedAt = types.StringNull()
	state.UpdatedAt = types.StringNull()
}

func volumeStatusReady(status blockstoragesdk.VolumeStatus) bool {
	return status == blockstoragesdk.VolumeStatusAvailable || status == blockstoragesdk.VolumeStatusInUse
}

func volumeStatusFailed(status blockstoragesdk.VolumeStatus) bool {
	switch status {
	case blockstoragesdk.VolumeStatusAttachFailed,
		blockstoragesdk.VolumeStatusBackupFailed,
		blockstoragesdk.VolumeStatusDetachFailed,
		blockstoragesdk.VolumeStatusExtendFailed,
		blockstoragesdk.VolumeStatusFailed,
		blockstoragesdk.VolumeStatusRestoreFailed:
		return true
	default:
		return false
	}
}

// volumeCreateFromModel decodes the create_from object and reduces each
// reference to its criterion. A null or unknown create_from yields the zero
// source, whose fields are all null, so callers can read it without repeating
// the presence check. A null or unknown reference yields a null or unknown
// criterion to match.
func volumeCreateFromModel(ctx context.Context, createFrom types.Object) (volumeSource, diag.Diagnostics) {
	var diags diag.Diagnostics
	if createFrom.IsNull() || createFrom.IsUnknown() {
		return volumeSource{}, diags
	}
	var model VolumeCreateFromModel
	diags.Append(createFrom.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return volumeSource{}, diags
	}
	return volumeSource{
		SourceType:    model.SourceType,
		Image:         model.Image,
		CustomImageID: reference.Get(model.CustomImage, reference.ID),
		SnapshotID:    reference.Get(model.Snapshot, reference.ID),
		BackupID:      reference.Get(model.Backup, reference.ID),
		VolumeType:    model.VolumeType,
	}, diags
}

// validateVolumeSnapshotZone rejects a zone that the snapshot creation request
// cannot honor. The SDK accepts only a snapshot ID for that source.
func validateVolumeSnapshotZone(ctx context.Context, zone, createFrom types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if zone.IsNull() || zone.IsUnknown() {
		return diags
	}
	source, sourceDiags := volumeCreateFromModel(ctx, createFrom)
	diags.Append(sourceDiags...)
	if diags.HasError() || source.SourceType.IsNull() || source.SourceType.IsUnknown() {
		return diags
	}
	if strings.TrimSpace(source.SourceType.ValueString()) == volumeSourceTypeSnapshot {
		diags.AddAttributeError(path.Root("zone"), "Invalid volume zone", "zone cannot be set when create_from.source_type is \"snapshot\"; the snapshot determines the volume's zone.")
	}
	return diags
}

func validVolumeSourceType(sourceType string) bool {
	switch sourceType {
	case volumeSourceTypeEmpty, volumeSourceTypeImage, volumeSourceTypeCustomImage, volumeSourceTypeSnapshot, volumeSourceTypeBackup:
		return true
	default:
		return false
	}
}

// volumeSourceReplaced reports whether the plan changes a create_from field
// that requires replacement. The comparison mirrors what those attribute plan
// modifiers do — a plain inequality between plan and state. The resource and
// the modifiers therefore cannot disagree about what counts as a replacement.
// Image replacement and volume type retype use selector identity separately.
func volumeSourceReplaced(planned, current volumeSource) bool {
	return !planned.SourceType.Equal(current.SourceType) ||
		!planned.CustomImageID.Equal(current.CustomImageID) ||
		!planned.SnapshotID.Equal(current.SnapshotID) ||
		!planned.BackupID.Equal(current.BackupID)
}

// volumeRetypeSelector returns criteria that still need an identity decision.
func volumeRetypeSelector(planned volumeSource) types.Object {
	if planned.VolumeType.IsNull() || planned.VolumeType.IsUnknown() {
		return types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes())
	}
	if !selector.Pending(planned.VolumeType) {
		return types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes())
	}
	return planned.VolumeType
}

// validateVolumeCreateSize enforces the size rules that only apply when the
// volume is about to be created, including as the replacement half of a
// destroy-then-create. A snapshot decides the size of the volume it produces,
// so a configured size is refused. Sending one fails the create, and an apply
// that has already destroyed the previous volume then leaves the practitioner
// with neither the volume nor a configuration that can rebuild it.
func validateVolumeCreateSize(ctx context.Context, createFrom types.Object, size types.Int64) diag.Diagnostics {
	source, diags := volumeCreateFromModel(ctx, createFrom)
	if diags.HasError() {
		return diags
	}
	if source.SourceType.IsNull() || source.SourceType.IsUnknown() || size.IsNull() || size.IsUnknown() {
		return diags
	}
	if strings.TrimSpace(source.SourceType.ValueString()) != volumeSourceTypeSnapshot {
		return diags
	}
	diags.AddError(
		"Invalid size for snapshot source",
		"size must be omitted because the snapshot determines it. A volume created from a snapshot can be grown afterwards, "+
			"but the grown size cannot be carried into a new volume: remove size before changing create_from.snapshot.",
	)
	return diags
}

func validateVolumeCreateFromConfig(ctx context.Context, createFrom types.Object, size types.Int64) diag.Diagnostics {
	var diags diag.Diagnostics
	if createFrom.IsNull() {
		diags.AddError("Missing create_from", "create_from must be configured.")
		return diags
	}
	if createFrom.IsUnknown() {
		return diags
	}

	source, sourceDiags := volumeCreateFromModel(ctx, createFrom)
	diags.Append(sourceDiags...)
	if diags.HasError() {
		return diags
	}
	if source.SourceType.IsNull() {
		diags.AddError("Missing create_from.source_type", "create_from.source_type must be configured.")
		return diags
	}
	if source.SourceType.IsUnknown() {
		return diags
	}

	sourceType := strings.TrimSpace(source.SourceType.ValueString())
	if !validVolumeSourceType(sourceType) {
		diags.AddError("Invalid create_from.source_type", "Supported values are: empty, image, custom_image, snapshot, backup.")
		return diags
	}

	if invalid := conflictingVolumeSourceFields(source, sourceType, false); len(invalid) > 0 {
		diags.AddError(
			"Invalid create_from fields",
			fmt.Sprintf("create_from.source_type %q cannot be used with: %s.", sourceType, strings.Join(invalid, ", ")),
		)
	}

	validateUUID := func(value types.String, field string) {
		if value.IsUnknown() {
			return
		}
		_, idDiags := parse.UUIDString(value, field)
		diags.Append(idDiags...)
	}

	switch sourceType {
	case volumeSourceTypeImage:
		diags.Append(selector.Validate(source.Image, "create_from.image", false)...)
	case volumeSourceTypeCustomImage:
		validateUUID(source.CustomImageID, "create_from.custom_image.id")
	case volumeSourceTypeSnapshot:
		validateUUID(source.SnapshotID, "create_from.snapshot.id")
	case volumeSourceTypeBackup:
		validateUUID(source.BackupID, "create_from.backup.id")
	}

	// A snapshot determines the initial size, but the backend still accepts a
	// later resize. ValidateConfig cannot tell create from update, so the
	// create-only "size must be omitted" rule lives in buildVolumeCreateFrom.
	if sourceType == volumeSourceTypeSnapshot {
		return diags
	}

	diags.Append(selector.Validate(source.VolumeType, "create_from.volume_type", false)...)
	if size.IsNull() {
		diags.AddError("Missing volume size", fmt.Sprintf("size is required for create_from.source_type %q.", sourceType))
	} else if !size.IsUnknown() && size.ValueInt64() < 1 {
		diags.AddError("Invalid volume size", "size must be greater than zero.")
	}
	return diags
}

func buildVolumeCreateFrom(
	ctx context.Context,
	createFrom types.Object,
	size types.Int64,
	zoneID *core.UUID,
	resolveImage serverlookup.ImageResolveFunc,
	resolveVolumeType blockstoragelookup.VolumeTypeResolveFunc,
) (blockstoragesdk.VolumeCreateSourceOptions, diag.Diagnostics) {
	var result blockstoragesdk.VolumeCreateSourceOptions
	var diags diag.Diagnostics
	if createFrom.IsNull() || createFrom.IsUnknown() {
		diags.AddError("Missing create_from", "create_from must be configured and known.")
		return result, diags
	}

	source, sourceDiags := volumeCreateFromModel(ctx, createFrom)
	diags.Append(sourceDiags...)
	if diags.HasError() {
		return result, diags
	}
	if source.SourceType.IsNull() || source.SourceType.IsUnknown() {
		diags.AddError("Missing create_from.source_type", "create_from.source_type must be configured and known.")
		return result, diags
	}

	sourceType := strings.TrimSpace(source.SourceType.ValueString())
	if invalid := conflictingVolumeSourceFields(source, sourceType, true); len(invalid) > 0 {
		diags.AddError(
			"Invalid create_from fields",
			fmt.Sprintf("create_from.source_type %q cannot be used with: %s.", sourceType, strings.Join(invalid, ", ")),
		)
		return result, diags
	}

	requireSize := func() (int, bool) {
		if size.IsNull() || size.IsUnknown() {
			diags.AddError("Missing volume size", fmt.Sprintf("size is required for create_from.source_type %q.", sourceType))
			return 0, false
		}
		if size.ValueInt64() < 1 {
			diags.AddError("Invalid volume size", "size must be greater than zero.")
			return 0, false
		}
		return int(size.ValueInt64()), true
	}
	resolveType := func(size int) (blockstoragesdk.VolumeTypeSchema, bool) {
		volumeType, typeDiags := blockstorageselector.ResolveVolumeType(ctx, source.VolumeType, "create_from.volume_type", zoneID, size, resolveVolumeType)
		diags.Append(typeDiags...)
		if typeDiags.HasError() {
			return blockstoragesdk.VolumeTypeSchema{}, false
		}
		return volumeType, true
	}

	switch sourceType {
	case volumeSourceTypeEmpty:
		size, ok := requireSize()
		if !ok {
			return result, diags
		}
		volumeType, ok := resolveType(size)
		if !ok {
			return result, diags
		}
		appendVolumeUnionError(&diags, result.FromVolumeCreateEmptySchema(blockstoragesdk.VolumeCreateEmptySchema{
			Size: size, VolumeTypeId: volumeType.Id,
		}))
	case volumeSourceTypeImage:
		size, ok := requireSize()
		if !ok {
			return result, diags
		}
		image, imageDiags := serverselector.ResolveImage(ctx, source.Image, "create_from.image", resolveImage)
		diags.Append(imageDiags...)
		if imageDiags.HasError() {
			return result, diags
		}
		if image.MinVolumeSize != nil && size < *image.MinVolumeSize {
			diags.AddError("Invalid volume size", fmt.Sprintf("image %q requires at least %d GiB, requested %d GiB.", image.Name, *image.MinVolumeSize, size))
			return result, diags
		}
		volumeType, ok := resolveType(size)
		if !ok {
			return result, diags
		}
		appendVolumeUnionError(&diags, result.FromVolumeCreateFromImageSchema(blockstoragesdk.VolumeCreateFromImageSchema{
			ImageId: image.Id, Size: size, VolumeTypeId: volumeType.Id,
		}))
	case volumeSourceTypeCustomImage:
		size, ok := requireSize()
		if !ok {
			return result, diags
		}
		imageID, idDiags := parse.UUIDString(source.CustomImageID, "create_from.custom_image.id")
		diags.Append(idDiags...)
		volumeType, typeOK := resolveType(size)
		if diags.HasError() || !typeOK {
			return result, diags
		}
		appendVolumeUnionError(&diags, result.FromVolumeCreateFromCustomImageSchema(blockstoragesdk.VolumeCreateFromCustomImageSchema{
			CustomImageId: imageID, Size: size, VolumeTypeId: volumeType.Id,
		}))
	case volumeSourceTypeSnapshot:
		if !size.IsNull() && !size.IsUnknown() {
			diags.AddError("Invalid size for snapshot source", "size must be omitted because the snapshot determines it.")
			return result, diags
		}
		snapshotID, idDiags := parse.UUIDString(source.SnapshotID, "create_from.snapshot.id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return result, diags
		}
		appendVolumeUnionError(&diags, result.FromVolumeCreateFromSnapshotSchema(blockstoragesdk.VolumeCreateFromSnapshotSchema{SnapshotId: snapshotID}))
	case volumeSourceTypeBackup:
		size, ok := requireSize()
		if !ok {
			return result, diags
		}
		backupID, idDiags := parse.UUIDString(source.BackupID, "create_from.backup.id")
		diags.Append(idDiags...)
		volumeType, typeOK := resolveType(size)
		if diags.HasError() || !typeOK {
			return result, diags
		}
		appendVolumeUnionError(&diags, result.FromVolumeCreateFromBackupSchema(blockstoragesdk.VolumeCreateFromBackupSchema{
			BackupId: backupID, Size: size, VolumeTypeId: volumeType.Id,
		}))
	default:
		diags.AddError("Invalid create_from.source_type", "Supported values are: empty, image, custom_image, snapshot, backup.")
	}
	return result, diags
}

// conflictingVolumeSourceFields lists the create_from fields that sourceType
// cannot use. includeUnknown selects the caller's stance on an unknown value:
// ValidateConfig defers it to apply, while the create path has a fully known
// plan and treats it as configured.
func conflictingVolumeSourceFields(source volumeSource, sourceType string, includeUnknown bool) []string {
	configured := func(value attr.Value) bool {
		return !value.IsNull() && (includeUnknown || !value.IsUnknown())
	}
	fields := []struct {
		name    string
		value   attr.Value
		forType string
	}{
		{name: "image", value: source.Image, forType: volumeSourceTypeImage},
		{name: "custom_image", value: source.CustomImageID, forType: volumeSourceTypeCustomImage},
		{name: "snapshot", value: source.SnapshotID, forType: volumeSourceTypeSnapshot},
		{name: "backup", value: source.BackupID, forType: volumeSourceTypeBackup},
	}
	var invalid []string
	for _, field := range fields {
		if configured(field.value) && field.forType != sourceType {
			invalid = append(invalid, "create_from."+field.name)
		}
	}
	if configured(source.VolumeType) && sourceType == volumeSourceTypeSnapshot {
		invalid = append(invalid, "create_from.volume_type")
	}
	return invalid
}

func appendVolumeUnionError(diags *diag.Diagnostics, err error) {
	if err != nil {
		diags.AddError("Invalid create_from value", fmt.Sprintf("Unable to encode create_from: %s", err))
	}
}
