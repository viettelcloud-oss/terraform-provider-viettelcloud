package blockstorage

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// Execution strategies that the schedule object accepts.
const (
	backupPolicyStrategyPeriodic    = string(blockstoragesdk.PolicyExecutionStrategyPeriodic)
	backupPolicyStrategyDaysOfMonth = string(blockstoragesdk.PolicyExecutionStrategyDaysOfMonth)
)

type VolumeBackupPolicyModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Status        types.String `tfsdk:"status"`
	Retention     types.Int64  `tfsdk:"retention"`
	Schedule      types.Object `tfsdk:"schedule"`
	State         types.String `tfsdk:"state"`
	VolumeCount   types.Int64  `tfsdk:"volume_count"`
	NextExecution types.String `tfsdk:"next_execution"`
	LastExecution types.String `tfsdk:"last_execution"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

// VolumeBackupPolicyResourceModel references the region as an object, while
// the data source filters by a plain region name.
type VolumeBackupPolicyResourceModel struct {
	VolumeBackupPolicyModel
	Region types.Object `tfsdk:"region"`
}

type VolumeBackupPolicyDataSourceModel struct {
	VolumeBackupPolicyModel
	Region types.String `tfsdk:"region"`
}

// volumeBackupPolicyScheduleModel is the schedule object. The execution
// strategy selects which of the other fields apply.
type volumeBackupPolicyScheduleModel struct {
	ExecutionStrategy types.String `tfsdk:"execution_strategy"`
	Frequency         types.Int64  `tfsdk:"frequency"`
	FrequencyUnit     types.String `tfsdk:"frequency_unit"`
	FirstExecution    types.String `tfsdk:"first_execution"`
	DaysOfMonth       types.Set    `tfsdk:"days_of_month"`
	Time              types.String `tfsdk:"time"`
}

var volumeBackupPolicyScheduleAttrTypes = map[string]attr.Type{
	"execution_strategy": types.StringType,
	"frequency":          types.Int64Type,
	"frequency_unit":     types.StringType,
	"first_execution":    types.StringType,
	"days_of_month":      types.SetType{ElemType: types.Int64Type},
	"time":               types.StringType,
}

func populateVolumeBackupPolicyModel(policy *blockstoragesdk.VolumeBackupPolicySchema, m *VolumeBackupPolicyModel) diag.Diagnostics {
	var diags diag.Diagnostics
	schedule, scheduleDiags := volumeBackupPolicyScheduleFromResponse(policy)
	diags.Append(scheduleDiags...)
	if diags.HasError() {
		return diags
	}
	m.ID = types.StringValue(policy.Id.String())
	m.Name = types.StringValue(policy.Name)
	m.Description = types.StringPointerValue(core.NullableToPtr(policy.Description))
	m.Status = types.StringValue(string(policy.Status))
	m.Retention = types.Int64Value(int64(policy.Retention))
	m.Schedule = schedule.object()
	m.State = types.StringValue(string(policy.State))
	m.VolumeCount = types.Int64Value(int64(policy.VolumeCount))
	m.NextExecution = timestampPointerValue(core.NullableToPtr(policy.NextExecution))
	m.LastExecution = timestampPointerValue(core.NullableToPtr(policy.LastExecution))
	m.CreatedAt = types.StringValue(policy.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(policy.UpdatedAt.Format(time.RFC3339))
	return diags
}

// populateVolumeBackupPolicyResourceState refreshes resource state from a
// policy response and keeps the configured spelling of inputs that the API
// normalizes.
func populateVolumeBackupPolicyResourceState(policy *blockstoragesdk.VolumeBackupPolicySchema, state *VolumeBackupPolicyResourceModel) diag.Diagnostics {
	prior := *state
	diags := populateVolumeBackupPolicyModel(policy, &state.VolumeBackupPolicyModel)
	if diags.HasError() {
		return diags
	}
	preserveVolumeBackupPolicyInputs(policy, &prior.VolumeBackupPolicyModel, &state.VolumeBackupPolicyModel)
	state.Region = reference.Value(reference.Name, compare.PreservedString(reference.Get(prior.Region, reference.Name), policy.Region.Name))
	return diags
}

func populateVolumeBackupPolicyDataSourceState(policy *blockstoragesdk.VolumeBackupPolicySchema, state *VolumeBackupPolicyDataSourceModel) diag.Diagnostics {
	diags := populateVolumeBackupPolicyModel(policy, &state.VolumeBackupPolicyModel)
	state.Region = types.StringValue(policy.Region.Name)
	return diags
}

// volumeBackupPolicyScheduleFromResponse decodes the schedule union of a
// response. A strategy that the schema cannot hold is an error, so that state
// never silently drops a schedule.
func volumeBackupPolicyScheduleFromResponse(policy *blockstoragesdk.VolumeBackupPolicySchema) (volumeBackupPolicyScheduleModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	model := volumeBackupPolicyScheduleModel{
		Frequency:      types.Int64Null(),
		FrequencyUnit:  types.StringNull(),
		FirstExecution: types.StringNull(),
		DaysOfMonth:    types.SetNull(types.Int64Type),
		Time:           types.StringNull(),
	}
	strategy, err := policy.Schedule.Discriminator()
	if err != nil {
		diags.AddError("Invalid volume backup policy schedule", fmt.Sprintf("Cannot read the schedule of volume backup policy %s: %s", policy.Id, err))
		return model, diags
	}
	model.ExecutionStrategy = types.StringValue(strategy)
	switch strategy {
	case backupPolicyStrategyPeriodic:
		periodic, err := policy.Schedule.AsVolumeBackupPolicyPeriodicScheduleSchema()
		if err != nil {
			diags.AddError("Invalid volume backup policy schedule", fmt.Sprintf("Cannot decode the periodic schedule of volume backup policy %s: %s", policy.Id, err))
			return model, diags
		}
		model.Frequency = types.Int64Value(int64(periodic.Frequency))
		model.FrequencyUnit = types.StringValue(string(periodic.FrequencyUnit))
		model.FirstExecution = types.StringValue(periodic.FirstExecution.UTC().Format(time.RFC3339))
	case backupPolicyStrategyDaysOfMonth:
		monthly, err := policy.Schedule.AsVolumeBackupPolicyDaysOfMonthScheduleSchema()
		if err != nil {
			diags.AddError("Invalid volume backup policy schedule", fmt.Sprintf("Cannot decode the days_of_month schedule of volume backup policy %s: %s", policy.Id, err))
			return model, diags
		}
		days := make([]attr.Value, 0, len(monthly.DaysOfMonth))
		for _, day := range monthly.DaysOfMonth {
			days = append(days, types.Int64Value(int64(day)))
		}
		model.DaysOfMonth = types.SetValueMust(types.Int64Type, days)
		model.Time = types.StringValue(monthly.Time)
	default:
		diags.AddError("Unsupported volume backup policy schedule", fmt.Sprintf("Volume backup policy %s has the execution strategy %q, which this provider does not support.", policy.Id, strategy))
	}
	return model, diags
}

func timestampPointerValue(t *time.Time) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.Format(time.RFC3339))
}

// preserveVolumeBackupPolicyInputs keeps the configured spelling of values
// that the API trims or rewrites: name, description, status, and the
// schedule.
func preserveVolumeBackupPolicyInputs(policy *blockstoragesdk.VolumeBackupPolicySchema, prior, state *VolumeBackupPolicyModel) {
	state.Name = compare.PreservedString(prior.Name, policy.Name)
	if description := core.NullableToPtr(policy.Description); description != nil {
		state.Description = compare.PreservedString(prior.Description, *description)
	}
	state.Status = compare.PreservedString(prior.Status, string(policy.Status))
	state.Schedule = preserveVolumeBackupPolicySchedule(prior.Schedule, state.Schedule)
}

// preserveVolumeBackupPolicySchedule returns the reported schedule, with the
// configured spelling of each value that names the same thing.
func preserveVolumeBackupPolicySchedule(prior, reported types.Object) types.Object {
	priorModel, priorOK := scheduleModelFromObject(prior)
	reportedModel, reportedOK := scheduleModelFromObject(reported)
	if !priorOK || !reportedOK {
		return reported
	}
	if sameBackupPolicyString(priorModel.ExecutionStrategy, reportedModel.ExecutionStrategy) {
		reportedModel.ExecutionStrategy = priorModel.ExecutionStrategy
	}
	if knownValue(reportedModel.FrequencyUnit) && sameBackupPolicyString(priorModel.FrequencyUnit, reportedModel.FrequencyUnit) {
		reportedModel.FrequencyUnit = priorModel.FrequencyUnit
	}
	if sameClockTime(priorModel.Time, reportedModel.Time) {
		reportedModel.Time = priorModel.Time
	}
	if sameInstant(priorModel.FirstExecution, reportedModel.FirstExecution) {
		reportedModel.FirstExecution = priorModel.FirstExecution
	}
	return reportedModel.object()
}

// object returns the schedule as a Framework object.
func (m volumeBackupPolicyScheduleModel) object() types.Object {
	return types.ObjectValueMust(volumeBackupPolicyScheduleAttrTypes, map[string]attr.Value{
		"execution_strategy": m.ExecutionStrategy,
		"frequency":          m.Frequency,
		"frequency_unit":     m.FrequencyUnit,
		"first_execution":    m.FirstExecution,
		"days_of_month":      m.DaysOfMonth,
		"time":               m.Time,
	})
}

// scheduleModelFromObject reads a known schedule object. It reports false for
// a null or unknown object, which has no fields to compare.
func scheduleModelFromObject(object types.Object) (volumeBackupPolicyScheduleModel, bool) {
	var model volumeBackupPolicyScheduleModel
	if !knownValue(object) {
		return model, false
	}
	fields := object.Attributes()
	var ok [6]bool
	model.ExecutionStrategy, ok[0] = fields["execution_strategy"].(types.String)
	model.Frequency, ok[1] = fields["frequency"].(types.Int64)
	model.FrequencyUnit, ok[2] = fields["frequency_unit"].(types.String)
	model.FirstExecution, ok[3] = fields["first_execution"].(types.String)
	model.DaysOfMonth, ok[4] = fields["days_of_month"].(types.Set)
	model.Time, ok[5] = fields["time"].(types.String)
	return model, !slices.Contains(ok[:], false)
}

func preserveVolumeBackupPolicyFilters(policy *blockstoragesdk.VolumeBackupPolicySchema, config, state *VolumeBackupPolicyDataSourceModel) {
	state.ID = compare.PreservedUUID(config.ID, policy.Id)
	state.Name = compare.PreservedString(config.Name, policy.Name)
	state.Region = compare.PreservedString(config.Region, policy.Region.Name)
	state.Status = compare.PreservedString(config.Status, string(policy.Status))
}

func knownValue(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }

// parseClockTime reads HH:MM or HH:MM:SS, the two forms that the API accepts.
func parseClockTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"15:04", "15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// sameClockTime reports whether two values name the same time of day, so that
// 07:30 and 07:30:00 count as equal.
func sameClockTime(a, b types.String) bool {
	if !knownValue(a) || !knownValue(b) {
		return false
	}
	left, leftOK := parseClockTime(a.ValueString())
	right, rightOK := parseClockTime(b.ValueString())
	return leftOK && rightOK && left.Equal(right)
}

// sameInstant reports whether two RFC 3339 values name the same instant in
// any timezone.
func sameInstant(a, b types.String) bool {
	if !knownValue(a) || !knownValue(b) {
		return false
	}
	left, leftErr := time.Parse(time.RFC3339, strings.TrimSpace(a.ValueString()))
	right, rightErr := time.Parse(time.RFC3339, strings.TrimSpace(b.ValueString()))
	return leftErr == nil && rightErr == nil && left.Equal(right)
}

// sameBackupPolicyString reports whether two values differ at most by outer
// whitespace. A null or unknown value is the same only as an equal value.
func sameBackupPolicyString(a, b types.String) bool {
	return a.Equal(b) || compare.SameTrimmedString(a, b)
}

// parseBackupPolicyUUID parses a configured UUID. A value that is blank after
// the trim reports as missing, not as malformed.
func parseBackupPolicyUUID(v types.String, field string) (core.UUID, diag.Diagnostics) {
	if knownValue(v) {
		v = types.StringValue(strings.TrimSpace(v.ValueString()))
	}
	return parse.UUIDString(v, field)
}
