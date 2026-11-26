package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
)

var (
	_ resource.Resource                   = &VolumeBackupPolicyResource{}
	_ resource.ResourceWithImportState    = &VolumeBackupPolicyResource{}
	_ resource.ResourceWithValidateConfig = &VolumeBackupPolicyResource{}
)

// backupPolicyTimePattern matches HH:MM and HH:MM:SS, the two forms of time
// that the API accepts.
var backupPolicyTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)

type VolumeBackupPolicyResource struct {
	client    *blockstoragesdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewVolumeBackupPolicyResource() resource.Resource { return &VolumeBackupPolicyResource{} }

func (r *VolumeBackupPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_backup_policy"
}

func (r *VolumeBackupPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Block Storage volume backup policy, which takes volume backups on a schedule. Deleting a policy detaches it from its volumes, so set `lifecycle { prevent_destroy = true }` on a policy in use.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Backup policy ID (UUID).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Backup policy name, 1 to 100 characters.",
				Validators:  []validator.String{trimmedBackupPolicyValue(stringvalidator.LengthBetween(1, 100))},
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Backup policy description, up to 255 characters. Omitting it preserves the current value. An empty string clears it.",
				Validators:    []validator.String{trimmedBackupPolicyValue(stringvalidator.LengthAtMost(255))},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"region": schema.SingleNestedAttribute{
				Required:      true,
				Description:   "Project region of the backup policy. Changing it replaces the backup policy.",
				Attributes:    reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.Name, compare.SameTrimmedString)},
			},
			"status": schema.StringAttribute{
				Required:    true,
				Description: "Policy status: enabled or disabled. A disabled policy takes no backups.",
				Validators:  []validator.String{trimmedBackupPolicyValue(stringvalidator.OneOf(string(blockstoragesdk.VolumeBackupPolicyStatusEnabled), string(blockstoragesdk.VolumeBackupPolicyStatusDisabled)))},
			},
			"retention": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Number of backups that the policy keeps for each volume, from 1 to 65535. Omitting it at creation keeps 1. Omitting it later preserves the current value.",
				Validators:    []validator.Int64{int64validator.Between(1, 65535)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"schedule": schema.SingleNestedAttribute{
				Required:    true,
				Description: "When the policy takes backups. With `periodic`, set `frequency`, `frequency_unit`, and `first_execution`. With `days_of_month`, set `days_of_month` and `time`.",
				Attributes: map[string]schema.Attribute{
					"execution_strategy": schema.StringAttribute{
						Required:    true,
						Description: "Schedule type: periodic or days_of_month.",
						Validators:  []validator.String{trimmedBackupPolicyValue(stringvalidator.OneOf(backupPolicyStrategyPeriodic, backupPolicyStrategyDaysOfMonth))},
					},
					"frequency": schema.Int64Attribute{
						Optional:    true,
						Description: "Number of frequency units between two backups, from 1 to 1000. Required with `periodic`, not allowed with `days_of_month`.",
						Validators:  []validator.Int64{int64validator.Between(1, 1000)},
					},
					"frequency_unit": schema.StringAttribute{
						Optional:    true,
						Description: "Unit of `frequency`: day, week, or month. Required with `periodic`, not allowed with `days_of_month`.",
						Validators: []validator.String{trimmedBackupPolicyValue(stringvalidator.OneOf(
							string(blockstoragesdk.PolicyFrequencyUnitDay), string(blockstoragesdk.PolicyFrequencyUnitWeek), string(blockstoragesdk.PolicyFrequencyUnitMonth),
						))},
					},
					"first_execution": schema.StringAttribute{
						Optional:    true,
						Description: "Time of the first backup (RFC 3339), on the hour and in the future at creation. Required with `periodic`, not allowed with `days_of_month`.",
					},
					"days_of_month": schema.SetAttribute{
						Optional:    true,
						ElementType: types.Int64Type,
						Description: "Days of the month (1 to 31) on which the policy takes a backup. Required with `days_of_month`, not allowed with `periodic`.",
						Validators: []validator.Set{
							setvalidator.SizeBetween(1, 31),
							setvalidator.ValueInt64sAre(int64validator.Between(1, 31)),
						},
					},
					"time": schema.StringAttribute{
						Optional:    true,
						Description: "Time of day in UTC at which the policy takes a backup, as HH:MM or HH:MM:SS. Required with `days_of_month`, not allowed with `periodic`.",
						Validators:  []validator.String{trimmedBackupPolicyValue(stringvalidator.RegexMatches(backupPolicyTimePattern, "time must use the format HH:MM or HH:MM:SS."))},
					},
				},
			},
			"state":          schema.StringAttribute{Computed: true, Description: "State of the latest backup run: ready, running, done, or fail."},
			"volume_count":   schema.Int64Attribute{Computed: true, Description: "Number of volumes that use the policy."},
			"next_execution": schema.StringAttribute{Computed: true, Description: "Time of the next backup (RFC3339). Null when none is planned."},
			"last_execution": schema.StringAttribute{Computed: true, Description: "Time of the latest backup run (RFC3339). Null before the first run."},
			"created_at":     schema.StringAttribute{Computed: true, Description: "Creation time (RFC3339).", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"updated_at":     schema.StringAttribute{Computed: true, Description: "Last update time (RFC3339)."},
		},
	}
}

func (r *VolumeBackupPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VolumeBackupPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config VolumeBackupPolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateVolumeBackupPolicySchedule(ctx, config.Schedule)...)
}

func (r *VolumeBackupPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VolumeBackupPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildVolumeBackupPolicyCreateBody(ctx, plan, projectlookup.NewRegionFinder(r.project, r.projectID).Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.CreateVolumeBackupPolicy(ctx, blockstoragesdk.CreateVolumeBackupPolicyParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating volume backup policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid volume backup policy response", "The create response did not contain a volume backup policy.")
		return
	}
	resp.Diagnostics.Append(populateVolumeBackupPolicyResourceState(policy, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeBackupPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VolumeBackupPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := r.client.GetVolumeBackupPolicy(ctx, id, blockstoragesdk.GetVolumeBackupPolicyParams{ProjectID: r.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading volume backup policy", err.Error())
		return
	}
	resp.Diagnostics.Append(populateVolumeBackupPolicyResourceState(policy, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends a PATCH that holds only the fields that changed. When the plan
// changes no field, for example when it only respells a time, Update reads the
// policy instead, so that state holds no unknown value.
func (r *VolumeBackupPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state VolumeBackupPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	body, changed, bodyDiags := buildVolumeBackupPolicyPatchBody(ctx, plan.VolumeBackupPolicyModel, state.VolumeBackupPolicyModel)
	resp.Diagnostics.Append(bodyDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var (
		policy *blockstoragesdk.VolumeBackupPolicySchema
		err    error
	)
	if changed {
		policy, err = r.client.PartialUpdateVolumeBackupPolicy(ctx, id, blockstoragesdk.PartialUpdateVolumeBackupPolicyParams{ProjectID: r.projectID}, body)
	} else {
		policy, err = r.client.GetVolumeBackupPolicy(ctx, id, blockstoragesdk.GetVolumeBackupPolicyParams{ProjectID: r.projectID})
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating volume backup policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Invalid volume backup policy response", "The update response did not contain a volume backup policy.")
		return
	}
	resp.Diagnostics.Append(populateVolumeBackupPolicyResourceState(policy, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VolumeBackupPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VolumeBackupPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteVolumeBackupPolicy(ctx, id, blockstoragesdk.DeleteVolumeBackupPolicyParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, blockstoragesdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting volume backup policy", err.Error())
	}
}

func (r *VolumeBackupPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.TrimSpace(req.ID))...)
}

// validateVolumeBackupPolicySchedule checks which schedule fields the
// execution strategy requires and which it forbids. An unknown value passes
// both checks, because Create and Update check it again when it becomes known.
func validateVolumeBackupPolicySchedule(ctx context.Context, schedule types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if !knownValue(schedule) {
		return diags
	}
	var model volumeBackupPolicyScheduleModel
	diags.Append(schedule.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || !knownValue(model.ExecutionStrategy) {
		return diags
	}
	strategy := strings.TrimSpace(model.ExecutionStrategy.ValueString())
	fields := []struct {
		name  string
		value attr.Value
	}{
		{"frequency", model.Frequency},
		{"frequency_unit", model.FrequencyUnit},
		{"first_execution", model.FirstExecution},
		{"days_of_month", model.DaysOfMonth},
		{"time", model.Time},
	}
	periodic := map[string]bool{"frequency": true, "frequency_unit": true, "first_execution": true}
	for _, field := range fields {
		applies := periodic[field.name] == (strategy == backupPolicyStrategyPeriodic)
		switch {
		case strategy != backupPolicyStrategyPeriodic && strategy != backupPolicyStrategyDaysOfMonth:
			// OneOf reports an unknown strategy.
		case applies && field.value.IsNull():
			diags.AddAttributeError(path.Root("schedule").AtName(field.name), "Missing volume backup policy schedule field",
				fmt.Sprintf("schedule.%s is required when execution_strategy is %q.", field.name, strategy))
		case !applies && knownValue(field.value):
			diags.AddAttributeError(path.Root("schedule").AtName(field.name), "Unexpected volume backup policy schedule field",
				fmt.Sprintf("schedule.%s is not allowed when execution_strategy is %q.", field.name, strategy))
		}
	}
	if knownValue(model.FirstExecution) {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(model.FirstExecution.ValueString())); err != nil {
			diags.AddAttributeError(path.Root("schedule").AtName("first_execution"), "Invalid volume backup policy first execution",
				fmt.Sprintf("first_execution %q must be an RFC 3339 timestamp, for example 2030-01-01T02:00:00Z.", model.FirstExecution.ValueString()))
		}
	}
	return diags
}

func buildVolumeBackupPolicyCreateBody(
	ctx context.Context,
	plan VolumeBackupPolicyResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (blockstoragesdk.VolumeBackupPolicyCreateSchema, diag.Diagnostics) {
	var body blockstoragesdk.VolumeBackupPolicyCreateSchema
	schedule, diags := buildVolumeBackupPolicySchedule(ctx, plan.Schedule)
	if !knownValue(plan.Name) {
		diags.AddAttributeError(path.Root("name"), "Missing volume backup policy name", "name must be configured and known.")
	}
	if !knownValue(plan.Status) {
		diags.AddAttributeError(path.Root("status"), "Missing volume backup policy status", "status must be configured and known.")
	}
	regionName := strings.TrimSpace(reference.Get(plan.Region, reference.Name).ValueString())
	if !knownValue(reference.Get(plan.Region, reference.Name)) || regionName == "" {
		diags.AddAttributeError(path.Root("region").AtName(reference.Name), "Missing volume backup policy region", "region.name must be a known, non-empty string.")
	}
	if diags.HasError() {
		return body, diags
	}
	region, err := resolveRegion(ctx, projectlookup.RegionFilter{Name: &regionName})
	if err != nil {
		diags.AddAttributeError(path.Root("region").AtName(reference.Name), "Unable to resolve region", err.Error())
		return body, diags
	}
	body = blockstoragesdk.VolumeBackupPolicyCreateSchema{
		Name:     strings.TrimSpace(plan.Name.ValueString()),
		RegionId: region.Region.Id,
		Status:   blockstoragesdk.VolumeBackupPolicyStatus(strings.TrimSpace(plan.Status.ValueString())),
		Schedule: schedule,
	}
	if knownValue(plan.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description = &description
	}
	if knownValue(plan.Retention) {
		retention := int(plan.Retention.ValueInt64())
		body.Retention = &retention
	}
	return body, diags
}

// buildVolumeBackupPolicyPatchBody builds a PATCH body that holds only the
// fields whose planned value is known and differs from state. A changed
// schedule is sent whole. The bool result reports whether any field changed,
// because the API rejects an empty body.
func buildVolumeBackupPolicyPatchBody(ctx context.Context, plan, state VolumeBackupPolicyModel) (blockstoragesdk.VolumeBackupPolicyPartialUpdateSchema, bool, diag.Diagnostics) {
	var (
		body    blockstoragesdk.VolumeBackupPolicyPartialUpdateSchema
		diags   diag.Diagnostics
		changed bool
	)
	if knownValue(plan.Name) && strings.TrimSpace(plan.Name.ValueString()) != strings.TrimSpace(state.Name.ValueString()) {
		name := strings.TrimSpace(plan.Name.ValueString())
		body.Name, changed = &name, true
	}
	if knownValue(plan.Description) && !sameBackupPolicyString(plan.Description, state.Description) {
		description := strings.TrimSpace(plan.Description.ValueString())
		body.Description, changed = &description, true
	}
	if knownValue(plan.Retention) && !plan.Retention.Equal(state.Retention) {
		retention := int(plan.Retention.ValueInt64())
		body.Retention, changed = &retention, true
	}
	if knownValue(plan.Status) && !sameBackupPolicyString(plan.Status, state.Status) {
		status := blockstoragesdk.VolumeBackupPolicyStatus(strings.TrimSpace(plan.Status.ValueString()))
		body.Status, changed = &status, true
	}
	if !sameVolumeBackupPolicySchedule(ctx, plan.Schedule, state.Schedule) {
		schedule, scheduleDiags := buildVolumeBackupPolicySchedule(ctx, plan.Schedule)
		diags.Append(scheduleDiags...)
		body.Schedule, changed = &schedule, true
	}
	return body, changed, diags
}

// buildVolumeBackupPolicySchedule converts the schedule object, whose fields
// must be known, into the request union of its execution strategy, with
// trimmed string values. The union helpers write execution_strategy
// themselves.
func buildVolumeBackupPolicySchedule(ctx context.Context, schedule types.Object) (blockstoragesdk.VolumeBackupPolicySchedule, diag.Diagnostics) {
	var union blockstoragesdk.VolumeBackupPolicySchedule
	if !knownValue(schedule) {
		var diags diag.Diagnostics
		diags.AddAttributeError(path.Root("schedule"), "Missing volume backup policy schedule", "schedule must be configured and known.")
		return union, diags
	}
	diags := validateVolumeBackupPolicySchedule(ctx, schedule)
	var model volumeBackupPolicyScheduleModel
	diags.Append(schedule.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return union, diags
	}
	for _, field := range []struct {
		name  string
		value interface{ IsUnknown() bool }
	}{
		{"execution_strategy", model.ExecutionStrategy},
		{"frequency", model.Frequency},
		{"frequency_unit", model.FrequencyUnit},
		{"first_execution", model.FirstExecution},
		{"days_of_month", model.DaysOfMonth},
		{"time", model.Time},
	} {
		if field.value.IsUnknown() {
			diags.AddAttributeError(path.Root("schedule").AtName(field.name), "Unknown volume backup policy schedule field", "schedule."+field.name+" must be known to build the request.")
		}
	}
	if diags.HasError() {
		return union, diags
	}
	var err error
	switch strings.TrimSpace(model.ExecutionStrategy.ValueString()) {
	case backupPolicyStrategyPeriodic:
		firstExecution, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(model.FirstExecution.ValueString()))
		if parseErr != nil {
			diags.AddAttributeError(path.Root("schedule").AtName("first_execution"), "Invalid volume backup policy first execution", parseErr.Error())
			return union, diags
		}
		err = union.FromVolumeBackupPolicyPeriodicSchedule(blockstoragesdk.VolumeBackupPolicyPeriodicSchedule{
			FirstExecution: firstExecution,
			Frequency:      int(model.Frequency.ValueInt64()),
			FrequencyUnit:  blockstoragesdk.PolicyFrequencyUnit(strings.TrimSpace(model.FrequencyUnit.ValueString())),
		})
	default:
		var days []int64
		diags.Append(model.DaysOfMonth.ElementsAs(ctx, &days, false)...)
		if diags.HasError() {
			return union, diags
		}
		monthDays := make([]int, 0, len(days))
		for _, day := range days {
			monthDays = append(monthDays, int(day))
		}
		slices.Sort(monthDays)
		err = union.FromVolumeBackupPolicyDaysOfMonthSchedule(blockstoragesdk.VolumeBackupPolicyDaysOfMonthSchedule{
			DaysOfMonth: monthDays,
			Time:        strings.TrimSpace(model.Time.ValueString()),
		})
	}
	if err != nil {
		diags.AddAttributeError(path.Root("schedule"), "Invalid volume backup policy schedule", err.Error())
	}
	return union, diags
}

// sameVolumeBackupPolicySchedule reports whether two schedules mean the same
// thing. It ignores outer whitespace and the spelling of time and
// first_execution, so that 07:30 and 07:30:00 plan no update.
func sameVolumeBackupPolicySchedule(ctx context.Context, a, b types.Object) bool {
	var left, right volumeBackupPolicyScheduleModel
	if !knownValue(a) || !knownValue(b) {
		return a.Equal(b)
	}
	if a.As(ctx, &left, basetypes.ObjectAsOptions{}).HasError() || b.As(ctx, &right, basetypes.ObjectAsOptions{}).HasError() {
		return false
	}
	return sameBackupPolicyString(left.ExecutionStrategy, right.ExecutionStrategy) &&
		left.Frequency.Equal(right.Frequency) &&
		sameBackupPolicyString(left.FrequencyUnit, right.FrequencyUnit) &&
		left.DaysOfMonth.Equal(right.DaysOfMonth) &&
		(left.FirstExecution.Equal(right.FirstExecution) || sameInstant(left.FirstExecution, right.FirstExecution)) &&
		(left.Time.Equal(right.Time) || sameClockTime(left.Time, right.Time))
}

// trimmedBackupPolicyValue runs inner on the configured value without its
// outer whitespace, because the request sends the trimmed value. Limits and
// enum checks then count the trimmed value, and outer whitespace is never
// rejected.
func trimmedBackupPolicyValue(inner validator.String) validator.String {
	return trimmedBackupPolicyValidator{inner: inner}
}

type trimmedBackupPolicyValidator struct{ inner validator.String }

func (v trimmedBackupPolicyValidator) Description(ctx context.Context) string {
	return v.inner.Description(ctx) + " Outer whitespace is ignored."
}

func (v trimmedBackupPolicyValidator) MarkdownDescription(ctx context.Context) string {
	return v.inner.MarkdownDescription(ctx) + " Outer whitespace is ignored."
}

func (v trimmedBackupPolicyValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if knownValue(req.ConfigValue) {
		req.ConfigValue = types.StringValue(strings.TrimSpace(req.ConfigValue.ValueString()))
	}
	v.inner.ValidateString(ctx, req, resp)
}
