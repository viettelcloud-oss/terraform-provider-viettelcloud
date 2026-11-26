package filestorage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	filestoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/lookup"
	filestorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
)

var (
	_ resource.Resource                   = &FilesystemResource{}
	_ resource.ResourceWithImportState    = &FilesystemResource{}
	_ resource.ResourceWithModifyPlan     = &FilesystemResource{}
	_ resource.ResourceWithValidateConfig = &FilesystemResource{}
)

const (
	defaultFilesystemPollInterval = 10 * time.Second
	defaultFilesystemTimeout      = 20 * time.Minute
)

// FilesystemResource implements the viettelcloud_fs_filesystem managed resource.
type FilesystemResource struct {
	client       *filestoragepkg.Client
	projectID    core.UUID
	pollInterval time.Duration
	timeout      time.Duration
}

func NewFilesystemResource() resource.Resource {
	return &FilesystemResource{}
}

func (r *FilesystemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem"
}

func (r *FilesystemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	useStateStr := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	useStateBool := []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
	useStateInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}

	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud File Storage filesystem.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Filesystem ID (UUID).",
				PlanModifiers: useStateStr,
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the filesystem. Must be between 1 and 100 characters.",
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Optional filesystem description. Omitting it preserves the backend value; set it to an empty string to clear it.",
				PlanModifiers: useStateStr,
			},
			"size": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Filesystem size in GiB. Required unless snapshot is set, and must not be set with snapshot at creation; a filesystem created from a snapshot takes its size from the backend. Can only be increased after creation. Omitting it keeps the current size.",
				PlanModifiers: useStateInt,
			},
			"protocol": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Filesystem protocol. One of NFS or SMB. Required unless snapshot is set, and must not be set with snapshot at creation. Changing the configured value replaces the filesystem. Omitting it keeps the current protocol.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), replace.StringIfConfigured(compare.SameTrimmedString)},
			},
			"filesystem_type": filestorageselector.FilesystemTypeAttribute(),
			"subnet": schema.SingleNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Subnet where the filesystem is created. Required unless snapshot is set, and must not be set with snapshot at creation. Changing the configured ID replaces the filesystem. Omitting it keeps the current subnet.",
				Attributes:    reference.PreservedAttributes(reference.ID, "Subnet ID (UUID)."),
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown(), replace.ReferenceIfConfigured(reference.ID, compare.SameUUIDString)},
			},
			"snapshot": schema.SingleNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Source snapshot to create the filesystem from. Set it instead of size, protocol, filesystem_type, and subnet; the backend takes those from the snapshot. Changing it replaces the filesystem. The provider reads the source snapshot that the backend reports, so an import records it. Omitting it keeps the recorded value.",
				Attributes:    reference.PreservedAttributes(reference.ID, "Snapshot ID (UUID)."),
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown(), replace.ReferenceIfConfigured(reference.ID, compare.SameUUIDString)},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current filesystem status.",
			},
			"mount_target_address": schema.StringAttribute{
				Computed:      true,
				Description:   "Mount target IP address, available after the filesystem is created.",
				PlanModifiers: useStateStr,
			},
			"zone_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Zone ID (UUID) where the filesystem resides.",
				PlanModifiers: useStateStr,
			},
			"zone_name": schema.StringAttribute{
				Computed:      true,
				Description:   "Zone name where the filesystem resides.",
				PlanModifiers: useStateStr,
			},
			"region_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Region ID (UUID) where the filesystem resides.",
				PlanModifiers: useStateStr,
			},
			"region_name": schema.StringAttribute{
				Computed:      true,
				Description:   "Region name where the filesystem resides.",
				PlanModifiers: useStateStr,
			},
			"vpc_id": schema.StringAttribute{
				Computed:      true,
				Description:   "VPC ID (UUID) of the subnet.",
				PlanModifiers: useStateStr,
			},
			"vpc_name": schema.StringAttribute{
				Computed:      true,
				Description:   "VPC name of the subnet.",
				PlanModifiers: useStateStr,
			},
			"subnet_cidr": schema.StringAttribute{
				Computed:      true,
				Description:   "CIDR of the subnet.",
				PlanModifiers: useStateStr,
			},
			"has_snapshot": schema.BoolAttribute{
				Computed:      true,
				Description:   "Whether the filesystem has at least one snapshot.",
				PlanModifiers: useStateBool,
			},
			"migration_percentage": schema.Int64Attribute{
				Computed:      true,
				Description:   "Migration progress percentage. Null when not migrating.",
				PlanModifiers: useStateInt,
			},
			"owner_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Owner ID (UUID).",
				PlanModifiers: useStateStr,
			},
			"project_id": schema.StringAttribute{
				Computed:      true,
				Description:   "Project ID (UUID) owning the filesystem.",
				PlanModifiers: useStateStr,
			},
			"snapshot_policy": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Applied snapshot policy. Null when no policy is applied.",
				Attributes: map[string]schema.Attribute{
					"id":   schema.StringAttribute{Computed: true, Description: "Policy ID (UUID)."},
					"name": schema.StringAttribute{Computed: true, Description: "Policy name."},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:      true,
				Description:   "Timestamp when the filesystem was created (RFC3339).",
				PlanModifiers: useStateStr,
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the filesystem was last updated (RFC3339).",
			},
		},
	}
}

func (r *FilesystemResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.FileStorage
	r.projectID = data.ProjectID
}

func (r *FilesystemResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config FilesystemResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := strings.TrimSpace(config.Name.ValueString())
		if name == "" {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Missing filesystem name", "name must be non-empty.")
			return
		}
		if utf8.RuneCountInString(name) > 100 {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Invalid filesystem name", "name must be at most 100 characters.")
		}
	}
	if !config.Size.IsNull() && !config.Size.IsUnknown() && config.Size.ValueInt64() < 1 {
		resp.Diagnostics.AddAttributeError(path.Root("size"), "Invalid filesystem size", "size must be greater than zero.")
	}
	if !config.Protocol.IsNull() && !config.Protocol.IsUnknown() {
		p := strings.TrimSpace(config.Protocol.ValueString())
		if p != string(filestoragepkg.FilesystemProtocolNFS) && p != string(filestoragepkg.FilesystemProtocolSMB) {
			resp.Diagnostics.AddAttributeError(path.Root("protocol"), "Invalid filesystem protocol", "protocol must be NFS or SMB.")
		}
	}
	if snapshotID := reference.Get(config.Snapshot, reference.ID); !snapshotID.IsNull() && !snapshotID.IsUnknown() {
		if _, idDiags := parse.UUIDString(snapshotID, "snapshot.id"); idDiags.HasError() {
			resp.Diagnostics.Append(idDiags...)
		}
	}
	if subnetID := reference.Get(config.Subnet, reference.ID); !subnetID.IsNull() && !subnetID.IsUnknown() {
		if _, idDiags := parse.UUIDString(subnetID, "subnet.id"); idDiags.HasError() {
			resp.Diagnostics.Append(idDiags...)
		}
	}
}

// ModifyPlan checks the filesystem source before a create or a replacement
// can destroy the prior filesystem, resolves the filesystem_type selector, and
// rejects a size decrease of an in-place update.
//
// resp.RequiresReplace holds only the paths that this method adds, because the
// Framework appends the paths of attribute plan modifiers after it returns.
// filesystemReplaced therefore repeats the comparisons of those modifiers.
func (r *FilesystemResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, config FilesystemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		resp.Diagnostics.Append(validateFilesystemSource(config)...)
		if !config.FilesystemType.IsNull() {
			resp.Diagnostics.Append(selector.Validate(plan.FilesystemType, "filesystem_type", false)...)
		}
		return
	}
	var state FilesystemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An omitted selector keeps the state value. UseStateForUnknown cannot keep
	// a null state value, which a backend that does not report the type leaves.
	decision := selector.Same
	if config.FilesystemType.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("filesystem_type"), state.FilesystemType)...)
	} else {
		var id types.String
		var selectorDiags diag.Diagnostics
		decision, id, selectorDiags = r.decideFilesystemTypeSelector(ctx, plan.FilesystemType, state.FilesystemType, state.ID)
		resp.Diagnostics.Append(selectorDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("filesystem_type"), selector.WithID(plan.FilesystemType, id))...)
		if decision != selector.Same {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("filesystem_type"))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// A replacement creates a new filesystem, so the create rules apply and
	// the in-place size rule does not.
	if decision != selector.Same || filesystemReplaced(config, state) {
		resp.Diagnostics.Append(validateFilesystemSource(config)...)
		return
	}
	if !plan.Size.IsNull() && !plan.Size.IsUnknown() && !state.Size.IsNull() && !state.Size.IsUnknown() {
		if plan.Size.ValueInt64() < state.Size.ValueInt64() {
			resp.Diagnostics.AddAttributeError(
				path.Root("size"),
				"Invalid filesystem size update",
				fmt.Sprintf("size can only increase from %d GiB; planned value is %d GiB.", state.Size.ValueInt64(), plan.Size.ValueInt64()),
			)
		}
	}
}

func (r *FilesystemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan FilesystemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := filestoragepkg.FilesystemCreateSchema{Name: strings.TrimSpace(plan.Name.ValueString())}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		body.Description = new(strings.TrimSpace(plan.Description.ValueString()))
	}
	// At apply, a configured value is known, so an unknown snapshot is one that
	// the configuration omits. The two sources are exclusive: the API rejects a
	// body with snapshot_id and any field of a new filesystem as field_conflict.
	if snapshotID := reference.Get(plan.Snapshot, reference.ID); !snapshotID.IsNull() && !snapshotID.IsUnknown() {
		parsed, parseDiags := parse.UUIDString(snapshotID, "snapshot.id")
		resp.Diagnostics.Append(parseDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.SnapshotId.Set(parsed)
	} else {
		resp.Diagnostics.Append(r.setNewFilesystemSource(ctx, &plan, &body)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	fs, err := r.client.CreateFilesystem(ctx, filestoragepkg.CreateFilesystemParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating filesystem", err.Error())
		return
	}

	// Record the ID before waiting so a timeout leaves a recoverable state.
	plan.ID = types.StringValue(fs.Id.String())
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ready, waitErr := r.waitUntilCreated(ctx, fs.Id)
	if waitErr != nil {
		resp.Diagnostics.AddError(
			"Error waiting for filesystem creation",
			fmt.Sprintf("%s\n\nFilesystem ID %s was preserved in state to avoid creating an untracked duplicate.", waitErr, fs.Id),
		)
		return
	}

	resp.Diagnostics.Append(populateFilesystemResourceState(ctx, ready, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FilesystemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state FilesystemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	fs, err := r.get(ctx, fsID)
	if err != nil {
		if errors.Is(err, filestoragepkg.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading filesystem", err.Error())
		return
	}

	resp.Diagnostics.Append(populateFilesystemResourceState(ctx, fs, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *FilesystemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan FilesystemResourceModel
	var state FilesystemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var fs *filestoragepkg.FilesystemSchema
	if filesystemMetadataChanged(plan, state) {
		body := buildFilesystemUpdateBody(plan)
		updated, err := r.client.UpdateFilesystem(ctx, fsID, filestoragepkg.UpdateFilesystemParams{ProjectID: r.projectID}, body)
		if err != nil {
			resp.Diagnostics.AddError("Error updating filesystem", err.Error())
			return
		}
		fs = updated
	}

	if !plan.Size.Equal(state.Size) {
		newSize := int(plan.Size.ValueInt64())
		resizeBody := filestoragepkg.FilesystemResizeSchema{NewSize: newSize}
		if err := r.client.ResizeFilesystem(ctx, fsID, filestoragepkg.ResizeFilesystemParams{ProjectID: r.projectID}, resizeBody); err != nil {
			resp.Diagnostics.AddError("Error resizing filesystem", err.Error())
			return
		}
		resized, waitErr := r.waitUntilResized(ctx, fsID, newSize)
		if waitErr != nil {
			resp.Diagnostics.AddError("Error waiting for filesystem resize", waitErr.Error())
			return
		}
		fs = resized
	}

	// A plan that changes only the spelling of name or description sends no
	// request, so state comes from a fresh read.
	if fs == nil {
		read, err := r.get(ctx, fsID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading filesystem after update", err.Error())
			return
		}
		fs = read
	}
	newState := plan
	resp.Diagnostics.Append(populateFilesystemResourceState(ctx, fs, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *FilesystemResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state FilesystemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteFilesystem(ctx, fsID, filestoragepkg.DeleteFilesystemParams{ProjectID: r.projectID})
	if errors.Is(err, filestoragepkg.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting filesystem", err.Error())
		return
	}
	if waitErr := r.waitUntilDeleted(ctx, fsID); waitErr != nil {
		resp.Diagnostics.AddError("Error waiting for filesystem deletion", waitErr.Error())
	}
}

func (r *FilesystemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	fsID := strings.TrimSpace(req.ID)
	if fsID == "" {
		resp.Diagnostics.AddError("Invalid filesystem import ID", "Expected a non-empty filesystem UUID.")
		return
	}
	if _, diags := parse.UUIDString(types.StringValue(fsID), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fsID)...)
}

// decideFilesystemTypeSelector applies catalog-selector identity logic to the
// filesystem_type attribute.
func (r *FilesystemResource) decideFilesystemTypeSelector(
	ctx context.Context,
	planned, prior types.Object,
	stateID types.String,
) (selector.Decision, types.String, diag.Diagnostics) {
	find := func(ctx context.Context) ([]core.UUID, error) {
		filter, diags := filestorageselector.FilesystemTypeFilter(ctx, planned, "filesystem_type")
		if diags.HasError() {
			return nil, fmt.Errorf("%v", diags)
		}
		candidates, err := filestoragelookup.NewFilesystemTypeFinder(r.client).Find(ctx, filter)
		if err != nil {
			return nil, err
		}
		ids := make([]core.UUID, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.Id)
		}
		return ids, nil
	}
	current := func(ctx context.Context) (core.UUID, error) {
		fsID, err := core.ParseUUID(stateID.ValueString())
		if err != nil {
			return core.UUID{}, err
		}
		fs, err := r.get(ctx, fsID)
		if err != nil {
			return core.UUID{}, err
		}
		if ft := core.NullableToPtr(fs.CreateFrom.FilesystemType); ft != nil {
			return ft.Id, nil
		}
		return core.UUID{}, fmt.Errorf("filesystem %s: %w", fsID, selector.ErrNoIdentity)
	}
	return selector.Decide(ctx, planned, prior, "filesystem_type", find, current)
}

// setNewFilesystemSource adds the fields of a new filesystem to body: the
// size, the trimmed protocol, the resolved filesystem type, and the subnet. It
// records the resolved type ID in plan.
func (r *FilesystemResource) setNewFilesystemSource(
	ctx context.Context,
	plan *FilesystemResourceModel,
	body *filestoragepkg.FilesystemCreateSchema,
) diag.Diagnostics {
	var diags diag.Diagnostics
	if plan.Size.IsNull() || plan.Size.IsUnknown() || plan.Protocol.IsNull() || plan.Protocol.IsUnknown() {
		diags.AddError("Missing filesystem source", "size, protocol, filesystem_type, and subnet must be known when snapshot is not set.")
		return diags
	}
	subnetID, subnetDiags := parse.UUIDString(reference.Get(plan.Subnet, reference.ID), "subnet.id")
	diags.Append(subnetDiags...)
	if diags.HasError() {
		return diags
	}
	plan.FilesystemType = configuredFilesystemTypeCriteria(plan.FilesystemType)
	ft, ftDiags := filestorageselector.ResolveFilesystemType(ctx, plan.FilesystemType, "filesystem_type", filestoragelookup.NewFilesystemTypeFinder(r.client).Resolve)
	diags.Append(ftDiags...)
	if diags.HasError() {
		return diags
	}
	plan.FilesystemType = selector.WithID(plan.FilesystemType, types.StringValue(ft.Id.String()))

	body.Size.Set(int(plan.Size.ValueInt64()))
	body.Protocol.Set(filestoragepkg.FilesystemProtocol(strings.TrimSpace(plan.Protocol.ValueString())))
	body.FilesystemTypeId.Set(ft.Id)
	body.SubnetId.Set(subnetID)
	return diags
}

func (r *FilesystemResource) get(ctx context.Context, id core.UUID) (*filestoragepkg.FilesystemSchema, error) {
	return r.client.GetFilesystem(ctx, id, filestoragepkg.GetFilesystemParams{ProjectID: r.projectID})
}

func (r *FilesystemResource) waitUntilCreated(ctx context.Context, id core.UUID) (*filestoragepkg.FilesystemSchema, error) {
	w := r.newWaiter()
	fs, err := w.WaitFor(ctx, func(ctx context.Context) (*filestoragepkg.FilesystemSchema, error) {
		fs, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll filesystem %s: %w", id, err)
		}
		if fs.Status == filestoragepkg.FilesystemStatusAvailable {
			return fs, nil
		}
		if fs.Status == filestoragepkg.FilesystemStatusFail {
			return nil, fmt.Errorf("filesystem %s entered terminal status %q", id, fs.Status)
		}
		return nil, fmt.Errorf("status=%s: %w", fs.Status, wait.ErrNotReady)
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for filesystem %s to become available: %w", id, err)
	}
	return fs, nil
}

func (r *FilesystemResource) waitUntilResized(ctx context.Context, id core.UUID, newSize int) (*filestoragepkg.FilesystemSchema, error) {
	w := r.newWaiter()
	fs, err := w.WaitFor(ctx, func(ctx context.Context) (*filestoragepkg.FilesystemSchema, error) {
		fs, err := r.get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("poll filesystem %s: %w", id, err)
		}
		if fs.Status == filestoragepkg.FilesystemStatusAvailable && fs.Size >= newSize {
			return fs, nil
		}
		if fs.Status == filestoragepkg.FilesystemStatusFail {
			return nil, fmt.Errorf("filesystem %s entered terminal status %q while resizing to %d GiB", id, fs.Status, newSize)
		}
		return nil, fmt.Errorf("status=%s, size=%d GiB: %w", fs.Status, fs.Size, wait.ErrNotReady)
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for filesystem %s to reach %d GiB: %w", id, newSize, err)
	}
	return fs, nil
}

func (r *FilesystemResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := r.newWaiter()
	_, err := w.WaitFor(ctx, func(ctx context.Context) (*filestoragepkg.FilesystemSchema, error) {
		fs, err := r.get(ctx, id)
		if errors.Is(err, filestoragepkg.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("poll filesystem %s: %w", id, err)
		}
		if fs.Status == filestoragepkg.FilesystemStatusFail {
			return nil, fmt.Errorf("filesystem %s entered terminal status %q while deleting", id, fs.Status)
		}
		return nil, fmt.Errorf("status=%s: %w", fs.Status, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("waiting for filesystem %s to be deleted: %w", id, err)
	}
	return nil
}

func (r *FilesystemResource) newWaiter() *wait.Waiter[*filestoragepkg.FilesystemSchema] {
	interval := r.pollInterval
	if interval <= 0 {
		interval = defaultFilesystemPollInterval
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultFilesystemTimeout
	}
	return wait.New[*filestoragepkg.FilesystemSchema](interval, timeout)
}

// validateFilesystemSource checks that the configuration selects exactly one
// source for a new filesystem: snapshot, or all of size, protocol,
// filesystem_type, and subnet. An unknown value counts as configured.
func validateFilesystemSource(config FilesystemResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	fields := []struct {
		name       string
		configured bool
	}{
		{"size", !config.Size.IsNull()},
		{"protocol", !config.Protocol.IsNull()},
		{"filesystem_type", !config.FilesystemType.IsNull()},
		{"subnet", !config.Subnet.IsNull()},
	}
	var conflicting, missing []string
	for _, f := range fields {
		if f.configured {
			conflicting = append(conflicting, f.name)
		} else {
			missing = append(missing, f.name)
		}
	}
	if !config.Snapshot.IsNull() {
		if len(conflicting) > 0 {
			diags.AddError(
				"Conflicting filesystem source",
				fmt.Sprintf("A filesystem created from snapshot takes its size, protocol, filesystem_type, and subnet from the snapshot. Remove %s, or remove snapshot.", strings.Join(conflicting, ", ")),
			)
		}
		return diags
	}
	if len(missing) > 0 {
		diags.AddError(
			"Missing filesystem source",
			fmt.Sprintf("Set snapshot, or set all of size, protocol, filesystem_type, and subnet. Missing: %s.", strings.Join(missing, ", ")),
		)
	}
	return diags
}

// filesystemReplaced reports whether the configured protocol, subnet, or
// snapshot replaces the filesystem. It uses the comparisons of their replace
// plan modifiers.
func filesystemReplaced(config, state FilesystemResourceModel) bool {
	changed := func(configured, prior types.String, same replace.Same) bool {
		return !configured.IsNull() && !configured.Equal(prior) && !same(configured, prior)
	}
	return changed(config.Protocol, state.Protocol, compare.SameTrimmedString) ||
		changed(reference.Get(config.Subnet, reference.ID), reference.Get(state.Subnet, reference.ID), compare.SameUUIDString) ||
		changed(reference.Get(config.Snapshot, reference.ID), reference.Get(state.Snapshot, reference.ID), compare.SameUUIDString)
}

// configuredFilesystemTypeCriteria sets each unknown criterion to null. At
// apply, a configured criterion is known, so an unknown one is a criterion
// that the configuration omits.
func configuredFilesystemTypeCriteria(value types.Object) types.Object {
	if value.IsNull() || value.IsUnknown() {
		return value
	}
	values := value.Attributes()
	for name, criterion := range values {
		if name != "id" && criterion.IsUnknown() {
			values[name] = types.StringNull()
		}
	}
	return types.ObjectValueMust(filestorageselector.FilesystemTypeAttributeTypes(), values)
}

// filesystemMetadataChanged reports whether the trimmed name or the known
// description differs from state, so that Update sends PUT only for a real
// change.
func filesystemMetadataChanged(plan, state FilesystemResourceModel) bool {
	if !compare.SameTrimmedString(plan.Name, state.Name) {
		return true
	}
	if plan.Description.IsNull() || plan.Description.IsUnknown() {
		return false
	}
	return !compare.SameTrimmedString(plan.Description, state.Description)
}

// buildFilesystemUpdateBody constructs the PUT body. It always sends the
// trimmed name, and sends the trimmed description only when the plan value is
// known. A blank description clears it with JSON null.
func buildFilesystemUpdateBody(plan FilesystemResourceModel) filestoragepkg.FilesystemUpdateSchema {
	var body filestoragepkg.FilesystemUpdateSchema
	body.Name.Set(strings.TrimSpace(plan.Name.ValueString()))
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		if description := strings.TrimSpace(plan.Description.ValueString()); description == "" {
			body.Description.SetNull()
		} else {
			body.Description.Set(description)
		}
	}
	return body
}
