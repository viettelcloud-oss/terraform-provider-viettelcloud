package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                   = &VolumeSnapshotPolicyResource{}
	_ resource.ResourceWithImportState    = &VolumeSnapshotPolicyResource{}
	_ resource.ResourceWithValidateConfig = &VolumeSnapshotPolicyResource{}
)

// snapshotPolicyTimePattern matches HH:MM and HH:MM:SS, in UTC.
var snapshotPolicyTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)

// snapshotPolicyWeekdays lists the valid days in calendar order. Requests send
// the days in this order, so that a set always serializes the same way.
var snapshotPolicyWeekdays = []blockstoragesdk.SnapshotDayOfWeek{
	blockstoragesdk.SnapshotDayOfWeekMon, blockstoragesdk.SnapshotDayOfWeekTue, blockstoragesdk.SnapshotDayOfWeekWed,
	blockstoragesdk.SnapshotDayOfWeekThu, blockstoragesdk.SnapshotDayOfWeekFri, blockstoragesdk.SnapshotDayOfWeekSat,
	blockstoragesdk.SnapshotDayOfWeekSun,
}

type VolumeSnapshotPolicyResource struct {
	client    *blockstoragesdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewVolumeSnapshotPolicyResource() resource.Resource { return &VolumeSnapshotPolicyResource{} }

func (r *VolumeSnapshotPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_snapshot_policy"
}

func (r *VolumeSnapshotPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud volume snapshot policy, which takes snapshots of its volumes on a weekly schedule. " +
			"Deleting a policy detaches it from its volumes, so set `lifecycle { prevent_destroy = true }` on a policy in use.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Snapshot policy ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Snapshot policy name."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Snapshot policy description. Omitting it preserves the current value. An empty string clears it.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"region": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Project region of the snapshot policy. Changing it replaces the snapshot policy.",
				Attributes:    reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{snapshotPolicyRegionReplace()},
			},
			"status": schema.StringAttribute{Required: true, Description: "Policy status: enabled or disabled."},
			"retention": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Number of snapshots that the policy keeps for each volume. Omitting it at creation keeps 1. Omitting it later preserves the current value.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"schedule": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Weekly schedule on which the policy takes snapshots.",
				Attributes: map[string]schema.Attribute{
					snapshotPolicyDaysOfWeek: schema.SetAttribute{Required: true, ElementType: types.StringType, Description: "Days of the week on which the policy takes a snapshot: one or more of mon, tue, wed, thu, fri, sat, and sun."},
					snapshotPolicyTime:       schema.StringAttribute{Required: true, Description: "Time of day in UTC at which the policy takes a snapshot, as HH:MM or HH:MM:SS."},
				},
			},
			"state":          schema.StringAttribute{Computed: true, Description: "State of the latest policy run: ready, running, done, or fail."},
			"volume_count":   schema.Int64Attribute{Computed: true, Description: "Number of volumes that use the policy."},
			"last_execution": schema.StringAttribute{Computed: true, Description: "Time of the latest policy run (RFC3339). Null before the first run."},
			"created_at":     schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at":     schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (r *VolumeSnapshotPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.project, r.projectID = data.BlockStorage, data.Project, data.ProjectID
}

func (r *VolumeSnapshotPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config VolumeSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateVolumeSnapshotPolicy(config.VolumeSnapshotPolicyModel)...)
}

func (r *VolumeSnapshotPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildVolumeSnapshotPolicyCreateBody(ctx, plan, projectlookup.NewRegionFinder(r.project, r.projectID).Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.CreateSnapshotPolicy(ctx, blockstoragesdk.CreateSnapshotPolicyParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume snapshot policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid snapshot policy response", "The create response did not contain a snapshot policy.")
		return
	}
	resp.Diagnostics.Append(populateVolumeSnapshotPolicyResourceState(policy, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeSnapshotPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VolumeSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetSnapshotPolicy(ctx, id, blockstoragesdk.GetSnapshotPolicyParams{ProjectID: r.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume snapshot policy", err.Error())
		return
	}
	resp.Diagnostics.Append(populateVolumeSnapshotPolicyResourceState(policy, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends a PATCH with the changed fields only. When the plan changes no
// field that the API holds, such as the spelling of time, it reads the policy
// instead, because a PATCH body needs at least one field.
func (r *VolumeSnapshotPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state VolumeSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	body, changed, bodyDiags := buildVolumeSnapshotPolicyUpdateBody(plan.VolumeSnapshotPolicyModel, state.VolumeSnapshotPolicyModel)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var policy *blockstoragesdk.SnapshotPolicySchema
	var err error
	if changed {
		policy, err = r.client.PartialUpdateSnapshotPolicy(ctx, id, blockstoragesdk.PartialUpdateSnapshotPolicyParams{ProjectID: r.projectID}, body)
	} else {
		policy, err = r.client.GetSnapshotPolicy(ctx, id, blockstoragesdk.GetSnapshotPolicyParams{ProjectID: r.projectID})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating volume snapshot policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid snapshot policy response", "The update response did not contain a snapshot policy.")
		return
	}
	resp.Diagnostics.Append(populateVolumeSnapshotPolicyResourceState(policy, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeSnapshotPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VolumeSnapshotPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteSnapshotPolicy(ctx, id, blockstoragesdk.DeleteSnapshotPolicyParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting volume snapshot policy", err.Error())
	}
}

func (r *VolumeSnapshotPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.TrimSpace(req.ID))...)
}

// snapshotPolicyRegionReplace replaces the policy when region.name changes. A
// plain RequiresReplace compares raw values, so a change of outer whitespace
// alone would destroy and create the same policy.
func snapshotPolicyRegionReplace() planmodifier.Object {
	const description = "Changing the region replaces the snapshot policy. A change of outer whitespace only updates state."
	return objectplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !snapshotPolicySameTrimmedString(reference.Get(req.PlanValue, reference.Name), reference.Get(req.StateValue, reference.Name))
		},
		description, description,
	)
}

// validateVolumeSnapshotPolicy checks the structure of known values only.
// Create and Update check unknown values again when they build the request.
// The API enforces length and range limits, so the provider does not repeat
// them.
func validateVolumeSnapshotPolicy(m VolumeSnapshotPolicyModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if snapshotPolicyKnown(m.Name) && strings.TrimSpace(m.Name.ValueString()) == "" {
		diags.AddAttributeError(path.Root("name"), "Invalid snapshot policy name", "name must be a non-empty string.")
	}
	if snapshotPolicyKnown(m.Status) && !blockstoragesdk.SnapshotPolicyStatus(strings.TrimSpace(m.Status.ValueString())).Valid() {
		diags.AddAttributeError(path.Root("status"), "Invalid snapshot policy status", fmt.Sprintf("status %q is not one of enabled, disabled.", m.Status.ValueString()))
	}
	schedule, ok := decodeSnapshotPolicySchedule(m.Schedule)
	if !ok {
		return diags
	}
	scheduleDays := path.Root("schedule").AtName(snapshotPolicyDaysOfWeek)
	if snapshotPolicyKnown(schedule.DaysOfWeek) {
		if len(schedule.DaysOfWeek.Elements()) == 0 {
			diags.AddAttributeError(scheduleDays, "Invalid snapshot policy days", "schedule.days_of_week must hold at least one day.")
		}
		for _, element := range schedule.DaysOfWeek.Elements() {
			if day, isString := element.(types.String); isString && snapshotPolicyKnown(day) && !blockstoragesdk.SnapshotDayOfWeek(strings.TrimSpace(day.ValueString())).Valid() {
				diags.AddAttributeError(scheduleDays, "Invalid snapshot policy day",
					fmt.Sprintf("%q is not one of mon, tue, wed, thu, fri, sat, sun.", day.ValueString()))
			}
		}
	}
	if snapshotPolicyKnown(schedule.Time) && !snapshotPolicyTimePattern.MatchString(strings.TrimSpace(schedule.Time.ValueString())) {
		diags.AddAttributeError(path.Root("schedule").AtName(snapshotPolicyTime), "Invalid snapshot policy time",
			fmt.Sprintf("schedule.time %q must use the format HH:MM or HH:MM:SS (00:00 to 23:59).", schedule.Time.ValueString()))
	}
	return diags
}

func buildVolumeSnapshotPolicyCreateBody(
	ctx context.Context,
	plan VolumeSnapshotPolicyResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (blockstoragesdk.SnapshotPolicyCreateSchema, diag.Diagnostics) {
	var body blockstoragesdk.SnapshotPolicyCreateSchema
	diags := requireSnapshotPolicyInputs(plan.VolumeSnapshotPolicyModel)
	schedule, scheduleDiags := buildSnapshotPolicySchedule(plan.Schedule)
	diags.Append(scheduleDiags...)
	regionName := strings.TrimSpace(reference.Get(plan.Region, reference.Name).ValueString())
	if !snapshotPolicyKnown(reference.Get(plan.Region, reference.Name)) || regionName == "" {
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
	body = blockstoragesdk.SnapshotPolicyCreateSchema{
		Name:     strings.TrimSpace(plan.Name.ValueString()),
		RegionId: region.Region.Id,
		Status:   blockstoragesdk.SnapshotPolicyStatus(strings.TrimSpace(plan.Status.ValueString())),
		Schedule: schedule,
	}
	if snapshotPolicyKnown(plan.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	if snapshotPolicyKnown(plan.Retention) {
		retention := int(plan.Retention.ValueInt64())
		body.Retention = &retention
	}
	return body, diags
}

// buildVolumeSnapshotPolicyUpdateBody builds the PATCH body from the fields
// whose planned value is known and differs from state. It sends the whole
// schedule when the days or the time change, and reports false when no field
// changed.
func buildVolumeSnapshotPolicyUpdateBody(plan, state VolumeSnapshotPolicyModel) (blockstoragesdk.SnapshotPolicyPartialUpdateSchema, bool, diag.Diagnostics) {
	var body blockstoragesdk.SnapshotPolicyPartialUpdateSchema
	diags := requireSnapshotPolicyInputs(plan)
	schedule, scheduleDiags := buildSnapshotPolicySchedule(plan.Schedule)
	diags.Append(scheduleDiags...)
	if diags.HasError() {
		return body, false, diags
	}
	changed := false
	if name := strings.TrimSpace(plan.Name.ValueString()); name != strings.TrimSpace(state.Name.ValueString()) {
		body.Name, changed = &name, true
	}
	if snapshotPolicyKnown(plan.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		if !snapshotPolicyKnown(state.Description) || description != strings.TrimSpace(state.Description.ValueString()) {
			body.Description, changed = &description, true
		}
	}
	if status := strings.TrimSpace(plan.Status.ValueString()); status != strings.TrimSpace(state.Status.ValueString()) {
		value := blockstoragesdk.SnapshotPolicyStatus(status)
		body.Status, changed = &value, true
	}
	if snapshotPolicyKnown(plan.Retention) && (!snapshotPolicyKnown(state.Retention) || plan.Retention.ValueInt64() != state.Retention.ValueInt64()) {
		retention := int(plan.Retention.ValueInt64())
		body.Retention, changed = &retention, true
	}
	if snapshotPolicyScheduleChanged(plan.Schedule, state.Schedule) {
		body.Schedule, changed = &schedule, true
	}
	return body, changed, diags
}

// snapshotPolicyScheduleChanged reports whether the planned days or time
// differ from state. Two spellings of one time, and days that differ only by
// outer whitespace, are not a change.
func snapshotPolicyScheduleChanged(plan, state types.Object) bool {
	planned, plannedOK := decodeSnapshotPolicySchedule(plan)
	current, currentOK := decodeSnapshotPolicySchedule(state)
	if !plannedOK || !currentOK {
		return true
	}
	plannedDays, plannedOK := snapshotPolicyDays(planned.DaysOfWeek)
	currentDays, currentOK := snapshotPolicyDays(current.DaysOfWeek)
	return !plannedOK || !currentOK || !slices.Equal(plannedDays, currentDays) ||
		normalizeSnapshotPolicyTime(planned.Time.ValueString()) != normalizeSnapshotPolicyTime(current.Time.ValueString())
}

// requireSnapshotPolicyInputs reports every required scalar input that is
// null or unknown, because a request needs known values for them.
func requireSnapshotPolicyInputs(m VolumeSnapshotPolicyModel) diag.Diagnostics {
	diags := validateVolumeSnapshotPolicy(m)
	for _, field := range []struct {
		name  string
		value attr.Value
	}{
		{"name", m.Name}, {"status", m.Status},
	} {
		if !snapshotPolicyKnown(field.value) {
			diags.AddAttributeError(path.Root(field.name), "Missing snapshot policy "+field.name, field.name+" must be configured and known.")
		}
	}
	return diags
}

// buildSnapshotPolicySchedule converts the planned schedule, whose values
// must be known and valid, into a days-of-week request schedule. The provider
// always sends the days_of_week strategy, which the SDK sets on the union.
func buildSnapshotPolicySchedule(object types.Object) (blockstoragesdk.SnapshotPolicySchedule, diag.Diagnostics) {
	var request blockstoragesdk.SnapshotPolicySchedule
	var diags diag.Diagnostics
	schedule, ok := decodeSnapshotPolicySchedule(object)
	if !ok || !snapshotPolicyKnown(schedule.DaysOfWeek) || !snapshotPolicyKnown(schedule.Time) {
		diags.AddAttributeError(path.Root("schedule"), "Missing snapshot policy schedule", "schedule.days_of_week and schedule.time must be configured and known.")
		return request, diags
	}
	days, known := snapshotPolicyDays(schedule.DaysOfWeek)
	if !known {
		diags.AddAttributeError(path.Root("schedule").AtName(snapshotPolicyDaysOfWeek), "Missing snapshot policy day", "Every day in schedule.days_of_week must be known.")
		return request, diags
	}
	err := request.FromSnapshotPolicyDaysOfWeekSchedule(blockstoragesdk.SnapshotPolicyDaysOfWeekSchedule{
		DaysOfWeek: days,
		Time:       strings.TrimSpace(schedule.Time.ValueString()),
	})
	if err != nil {
		diags.AddAttributeError(path.Root("schedule"), "Invalid snapshot policy schedule", err.Error())
	}
	return request, diags
}
