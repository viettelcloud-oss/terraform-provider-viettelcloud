package filestorage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
)

var (
	_ resource.Resource                   = &FilesystemSnapshotPolicyResource{}
	_ resource.ResourceWithImportState    = &FilesystemSnapshotPolicyResource{}
	_ resource.ResourceWithValidateConfig = &FilesystemSnapshotPolicyResource{}
)

// snapshotPolicyTimePattern is the format the backend accepts for time, in
// UTC: HH:MM, without seconds.
var snapshotPolicyTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// snapshotPolicyDefaultRetention is the retention that Create sends when the
// configuration omits it, because the backend rejects a create request that
// has no retention.
const snapshotPolicyDefaultRetention = 1

type FilesystemSnapshotPolicyResource struct {
	client    *filestoragesdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

// snapshotPolicySchedule holds the fields that create and update requests
// both require.
type snapshotPolicySchedule struct {
	Name       string
	DaysOfWeek []filestoragesdk.SnapshotDayOfWeek
	Time       string
	Status     filestoragesdk.SnapshotPolicyStatus
}

func NewFilesystemSnapshotPolicyResource() resource.Resource {
	return &FilesystemSnapshotPolicyResource{}
}

func (r *FilesystemSnapshotPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem_snapshot_policy"
}

func (r *FilesystemSnapshotPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud File Storage filesystem snapshot policy, which takes filesystem snapshots on a weekly schedule.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Snapshot policy ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Snapshot policy name."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy description. Omitting it preserves the current value. An empty string clears it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"region": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Project region of the snapshot policy. Changing it replaces the snapshot policy.",
				Attributes:    reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.Name, compare.SameTrimmedString)},
			},
			"days_of_week": schema.ListAttribute{
				Required:    true,
				ElementType: types.StringType,
				Description: "Days of the week on which the policy takes a snapshot: one or more of mon, tue, wed, thu, fri, sat, and sun.",
			},
			"time": schema.StringAttribute{Required: true, Description: "Time of day in UTC at which the policy takes a snapshot, as HH:MM."},
			"retention": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Number of snapshots the policy keeps. Omitting it at creation keeps 1. Omitting it later preserves the current value.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"status":           schema.StringAttribute{Required: true, Description: "Policy status: enabled or disabled."},
			"filesystem_count": schema.Int64Attribute{Computed: true, Description: "Number of filesystems that use the policy."},
			"state":            schema.StringAttribute{Computed: true, Description: "State of the latest policy run: ready, running, done, or fail."},
			"region_id":        schema.StringAttribute{Computed: true, Description: "Region ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"region_name":      schema.StringAttribute{Computed: true, Description: "Region name that the backend reports.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id":       schema.StringAttribute{Computed: true, Description: "Project ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"owner_id":         schema.StringAttribute{Computed: true, Description: "Owner ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"created_at":       schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at":       schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (r *FilesystemSnapshotPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.project, r.projectID = data.FileStorage, data.Project, data.ProjectID
}

func (r *FilesystemSnapshotPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config FilesystemSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateFilesystemSnapshotPolicy(config.FilesystemSnapshotPolicyModel)...)
}

func (r *FilesystemSnapshotPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan FilesystemSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildSnapshotPolicyCreateBody(ctx, plan, projectlookup.NewRegionFinder(r.project, r.projectID).Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.CreateSnapshotPolicy(ctx, filestoragesdk.CreateSnapshotPolicyParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating filesystem snapshot policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid snapshot policy response", "The create response did not contain a snapshot policy.")
		return
	}
	populateFilesystemSnapshotPolicyResourceState(policy, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FilesystemSnapshotPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state FilesystemSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetFilesystemSnapshotPolicy(ctx, id, filestoragesdk.GetFilesystemSnapshotPolicyParams{ProjectID: r.projectID})
	if errors.Is(err, filestoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading filesystem snapshot policy", err.Error())
		return
	}
	populateFilesystemSnapshotPolicyResourceState(policy, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends a PUT with the complete representation, because the endpoint
// requires every schedule field even when only one of them changed.
func (r *FilesystemSnapshotPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state FilesystemSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	body, bodyDiags := buildSnapshotPolicyUpdateBody(plan.FilesystemSnapshotPolicyModel, state.FilesystemSnapshotPolicyModel)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.UpdateSnapshotPolicy(ctx, id, filestoragesdk.UpdateSnapshotPolicyParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating filesystem snapshot policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid snapshot policy response", "The update response did not contain a snapshot policy.")
		return
	}
	populateFilesystemSnapshotPolicyResourceState(policy, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FilesystemSnapshotPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state FilesystemSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteSnapshotPolicy(ctx, id, filestoragesdk.DeleteSnapshotPolicyParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, filestoragesdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting filesystem snapshot policy", err.Error())
	}
}

func (r *FilesystemSnapshotPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// validateFilesystemSnapshotPolicy checks the structure of known values only.
// Unknown values are checked again when Create or Update builds the request.
func validateFilesystemSnapshotPolicy(m FilesystemSnapshotPolicyModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if knownValue(m.Name) && strings.TrimSpace(m.Name.ValueString()) == "" {
		diags.AddAttributeError(path.Root("name"), "Invalid snapshot policy name", "name must be a non-empty string.")
	}
	if knownValue(m.DaysOfWeek) {
		if len(m.DaysOfWeek.Elements()) == 0 {
			diags.AddAttributeError(path.Root("days_of_week"), "Invalid snapshot policy days", "days_of_week must hold at least one day.")
		}
		for i, element := range m.DaysOfWeek.Elements() {
			if day, ok := element.(types.String); ok && knownValue(day) && !filestoragesdk.SnapshotDayOfWeek(strings.TrimSpace(day.ValueString())).Valid() {
				diags.AddAttributeError(path.Root("days_of_week").AtListIndex(i), "Invalid snapshot policy day",
					fmt.Sprintf("%q is not one of mon, tue, wed, thu, fri, sat, sun.", day.ValueString()))
			}
		}
	}
	if knownValue(m.Time) && !snapshotPolicyTimePattern.MatchString(strings.TrimSpace(m.Time.ValueString())) {
		diags.AddAttributeError(path.Root("time"), "Invalid snapshot policy time", fmt.Sprintf("time %q must use the format HH:MM (00:00 to 23:59), without seconds.", m.Time.ValueString()))
	}
	if knownValue(m.Retention) && m.Retention.ValueInt64() <= 0 {
		diags.AddAttributeError(path.Root("retention"), "Invalid snapshot policy retention", "retention must be greater than 0.")
	}
	if knownValue(m.Status) && !filestoragesdk.SnapshotPolicyStatus(strings.TrimSpace(m.Status.ValueString())).Valid() {
		diags.AddAttributeError(path.Root("status"), "Invalid snapshot policy status", fmt.Sprintf("status %q is not one of enabled, disabled.", m.Status.ValueString()))
	}
	return diags
}

func buildSnapshotPolicyCreateBody(
	ctx context.Context,
	plan FilesystemSnapshotPolicyResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (filestoragesdk.FilesystemSnapshotPolicyCreateSchema, diag.Diagnostics) {
	var body filestoragesdk.FilesystemSnapshotPolicyCreateSchema
	schedule, diags := buildSnapshotPolicySchedule(plan.FilesystemSnapshotPolicyModel)
	regionName := strings.TrimSpace(reference.Get(plan.Region, reference.Name).ValueString())
	if !knownValue(reference.Get(plan.Region, reference.Name)) || regionName == "" {
		diags.AddAttributeError(path.Root("region").AtName(reference.Name), "Missing snapshot policy region", "region.name must be a known, non-empty string.")
	}
	if diags.HasError() {
		return body, diags
	}
	region, err := resolveRegion(ctx, projectlookup.RegionFilter{Name: &regionName})
	if err != nil {
		diags.AddAttributeError(path.Root("region").AtName(reference.Name), "Unable to resolve region", err.Error())
		return body, diags
	}
	body = filestoragesdk.FilesystemSnapshotPolicyCreateSchema{
		Name: schedule.Name, DaysOfWeek: schedule.DaysOfWeek, Time: schedule.Time, Status: schedule.Status, RegionId: region.Region.Id,
	}
	if knownValue(plan.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	retention := snapshotPolicyDefaultRetention
	if knownValue(plan.Retention) {
		retention = int(plan.Retention.ValueInt64())
	}
	body.Retention.Set(retention)
	return body, diags
}

// buildSnapshotPolicyUpdateBody builds the complete PUT body. It
// always sends retention, taken from state when the plan has no known value,
// because the backend rejects a PUT without it. It sends description only
// when the planned value is known: omission preserves it, and an empty string
// clears it.
func buildSnapshotPolicyUpdateBody(plan, state FilesystemSnapshotPolicyModel) (filestoragesdk.FilesystemSnapshotPolicyUpdateSchema, diag.Diagnostics) {
	var body filestoragesdk.FilesystemSnapshotPolicyUpdateSchema
	schedule, diags := buildSnapshotPolicySchedule(plan)
	retention := plan.Retention
	if !knownValue(retention) {
		retention = state.Retention
	}
	if !knownValue(retention) {
		diags.AddAttributeError(path.Root("retention"), "Missing snapshot policy retention", "retention has no known value in the plan or in state.")
	}
	if diags.HasError() {
		return body, diags
	}
	body = filestoragesdk.FilesystemSnapshotPolicyUpdateSchema{
		Name: schedule.Name, DaysOfWeek: schedule.DaysOfWeek, Time: schedule.Time, Status: schedule.Status,
	}
	body.Retention.Set(int(retention.ValueInt64()))
	if knownValue(plan.Description) {
		body.Description.Set(strings.TrimSpace(plan.Description.ValueString()))
	}
	return body, diags
}

// buildSnapshotPolicySchedule converts the required plan values, which
// must be known and valid, into trimmed request values.
func buildSnapshotPolicySchedule(m FilesystemSnapshotPolicyModel) (snapshotPolicySchedule, diag.Diagnostics) {
	var schedule snapshotPolicySchedule
	diags := validateFilesystemSnapshotPolicy(m)
	for _, field := range []struct {
		name  string
		value attr.Value
	}{
		{"name", m.Name}, {"days_of_week", m.DaysOfWeek}, {"time", m.Time}, {"status", m.Status},
	} {
		if field.value.IsNull() || field.value.IsUnknown() {
			diags.AddAttributeError(path.Root(field.name), "Missing snapshot policy "+field.name, field.name+" must be configured and known.")
		}
	}
	if diags.HasError() {
		return schedule, diags
	}
	for i, element := range m.DaysOfWeek.Elements() {
		day, ok := element.(types.String)
		if !ok || !knownValue(day) {
			diags.AddAttributeError(path.Root("days_of_week").AtListIndex(i), "Missing snapshot policy day", "Every day in days_of_week must be known.")
			continue
		}
		schedule.DaysOfWeek = append(schedule.DaysOfWeek, filestoragesdk.SnapshotDayOfWeek(strings.TrimSpace(day.ValueString())))
	}
	schedule.Name = strings.TrimSpace(m.Name.ValueString())
	schedule.Time = strings.TrimSpace(m.Time.ValueString())
	schedule.Status = filestoragesdk.SnapshotPolicyStatus(strings.TrimSpace(m.Status.ValueString()))
	return schedule, diags
}
