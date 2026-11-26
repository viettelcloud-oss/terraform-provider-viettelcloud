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
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

const (
	snapshotPolicyDaysOfWeek = "days_of_week"
	snapshotPolicyTime       = "time"
)

// snapshotPolicyScheduleTypes is the attribute type map of the schedule
// object in resource and data source state.
var snapshotPolicyScheduleTypes = map[string]attr.Type{
	snapshotPolicyDaysOfWeek: types.SetType{ElemType: types.StringType},
	snapshotPolicyTime:       types.StringType,
}

type VolumeSnapshotPolicyModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	Status        types.String `tfsdk:"status"`
	Retention     types.Int64  `tfsdk:"retention"`
	Schedule      types.Object `tfsdk:"schedule"`
	State         types.String `tfsdk:"state"`
	VolumeCount   types.Int64  `tfsdk:"volume_count"`
	LastExecution types.String `tfsdk:"last_execution"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

// VolumeSnapshotPolicyResourceModel references the region as an object, while
// the data source filters by a plain region name.
type VolumeSnapshotPolicyResourceModel struct {
	VolumeSnapshotPolicyModel
	Region types.Object `tfsdk:"region"`
}

type VolumeSnapshotPolicyDataSourceModel struct {
	VolumeSnapshotPolicyModel
	Region types.String `tfsdk:"region"`
}

// volumeSnapshotPolicySchedule is the decoded schedule object.
type volumeSnapshotPolicySchedule struct {
	DaysOfWeek types.Set    `tfsdk:"days_of_week"`
	Time       types.String `tfsdk:"time"`
}

// populateVolumeSnapshotPolicyModel maps a policy response to the shared
// model. It returns an error for a policy whose schedule is not a days-of-week
// schedule, because the schema has no place for the other schedule types.
func populateVolumeSnapshotPolicyModel(policy *blockstoragesdk.SnapshotPolicySchema, m *VolumeSnapshotPolicyModel) diag.Diagnostics {
	schedule, diags := snapshotPolicyScheduleObject(policy)
	if diags.HasError() {
		return diags
	}
	m.ID = types.StringValue(policy.Id.String())
	m.Name = types.StringValue(policy.Name)
	m.Description = types.StringPointerValue(core.NullableToPtr(policy.Description))
	m.Status = types.StringValue(string(policy.Status))
	m.Retention = types.Int64Value(int64(policy.Retention))
	m.Schedule = schedule
	m.State = types.StringValue(string(policy.State))
	m.VolumeCount = types.Int64Value(int64(policy.VolumeCount))
	m.LastExecution = types.StringNull()
	if last := core.NullableToPtr(policy.LastExecution); last != nil {
		m.LastExecution = types.StringValue(last.Format(time.RFC3339))
	}
	m.CreatedAt = types.StringValue(policy.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(policy.UpdatedAt.Format(time.RFC3339))
	return diags
}

// snapshotPolicyScheduleObject converts the schedule of a response. The error
// names the policy ID and the schedule type that the provider does not
// support, so that the practitioner can find the policy.
func snapshotPolicyScheduleObject(policy *blockstoragesdk.SnapshotPolicySchema) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	strategy, err := policy.Schedule.Discriminator()
	if err != nil || strategy != string(blockstoragesdk.ListSnapshotPoliciesParamsExecutionStrategyDaysOfWeek) {
		diags.AddError("Unsupported snapshot policy schedule",
			fmt.Sprintf("Snapshot policy %s has the schedule type %q. This provider supports only the days_of_week schedule.", policy.Id, strategy))
		return types.ObjectNull(snapshotPolicyScheduleTypes), diags
	}
	weekly, err := policy.Schedule.AsSnapshotPolicyDaysOfWeekScheduleSchema()
	if err != nil {
		diags.AddError("Invalid snapshot policy schedule", fmt.Sprintf("Snapshot policy %s has an unreadable schedule: %s", policy.Id, err))
		return types.ObjectNull(snapshotPolicyScheduleTypes), diags
	}
	days := make([]attr.Value, 0, len(weekly.DaysOfWeek))
	for _, day := range weekly.DaysOfWeek {
		days = append(days, types.StringValue(string(day)))
	}
	object, objectDiags := types.ObjectValue(snapshotPolicyScheduleTypes, map[string]attr.Value{
		snapshotPolicyDaysOfWeek: types.SetValueMust(types.StringType, days),
		snapshotPolicyTime:       types.StringValue(snapshotPolicyClockTime(weekly.Time)),
	})
	diags.Append(objectDiags...)
	return object, diags
}

// populateVolumeSnapshotPolicyResourceState refreshes resource state from a
// policy response and keeps the configured spelling of inputs that the API
// normalizes.
func populateVolumeSnapshotPolicyResourceState(policy *blockstoragesdk.SnapshotPolicySchema, state *VolumeSnapshotPolicyResourceModel) diag.Diagnostics {
	prior := *state
	diags := populateVolumeSnapshotPolicyModel(policy, &state.VolumeSnapshotPolicyModel)
	if diags.HasError() {
		return diags
	}
	preserveVolumeSnapshotPolicyInputs(policy, &prior.VolumeSnapshotPolicyModel, &state.VolumeSnapshotPolicyModel)
	state.Region = reference.Value(reference.Name, types.StringValue(policy.Region.Name))
	if region := reference.Get(prior.Region, reference.Name); snapshotPolicySameTrimmed(region, policy.Region.Name) {
		state.Region = reference.Value(reference.Name, region)
	}
	return diags
}

func populateVolumeSnapshotPolicyDataSourceState(policy *blockstoragesdk.SnapshotPolicySchema, state *VolumeSnapshotPolicyDataSourceModel) diag.Diagnostics {
	diags := populateVolumeSnapshotPolicyModel(policy, &state.VolumeSnapshotPolicyModel)
	state.Region = types.StringValue(policy.Region.Name)
	return diags
}

func preserveVolumeSnapshotPolicyInputs(policy *blockstoragesdk.SnapshotPolicySchema, prior, state *VolumeSnapshotPolicyModel) {
	if snapshotPolicySameTrimmed(prior.Name, policy.Name) {
		state.Name = prior.Name
	}
	if description := core.NullableToPtr(policy.Description); description != nil && snapshotPolicySameTrimmed(prior.Description, *description) {
		state.Description = prior.Description
	}
	if snapshotPolicySameTrimmed(prior.Status, string(policy.Status)) {
		state.Status = prior.Status
	}
	priorSchedule, ok := decodeSnapshotPolicySchedule(prior.Schedule)
	if !ok {
		return
	}
	current, ok := decodeSnapshotPolicySchedule(state.Schedule)
	if !ok {
		return
	}
	days, clock := current.DaysOfWeek, current.Time
	if priorDays, known := snapshotPolicyDays(priorSchedule.DaysOfWeek); known {
		if currentDays, _ := snapshotPolicyDays(current.DaysOfWeek); slices.Equal(priorDays, currentDays) {
			days = priorSchedule.DaysOfWeek
		}
	}
	if snapshotPolicyKnown(priorSchedule.Time) && normalizeSnapshotPolicyTime(priorSchedule.Time.ValueString()) == normalizeSnapshotPolicyTime(current.Time.ValueString()) {
		clock = priorSchedule.Time
	}
	state.Schedule = types.ObjectValueMust(snapshotPolicyScheduleTypes, map[string]attr.Value{
		snapshotPolicyDaysOfWeek: days,
		snapshotPolicyTime:       clock,
	})
}

func preserveVolumeSnapshotPolicyFilters(policy *blockstoragesdk.SnapshotPolicySchema, config, state *VolumeSnapshotPolicyDataSourceModel) {
	if compare.SameUUID(snapshotPolicyTrimmed(config.ID), policy.Id) {
		state.ID = config.ID
	}
	if snapshotPolicySameTrimmed(config.Name, policy.Name) {
		state.Name = config.Name
	}
	if snapshotPolicySameTrimmed(config.Region, policy.Region.Name) {
		state.Region = config.Region
	}
	if snapshotPolicySameTrimmed(config.Status, string(policy.Status)) {
		state.Status = config.Status
	}
}

func snapshotPolicyKnown(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }

// decodeSnapshotPolicySchedule reads the schedule object. It reports false for
// a null or unknown object, so callers must decide what an absent schedule
// means.
func decodeSnapshotPolicySchedule(object types.Object) (volumeSnapshotPolicySchedule, bool) {
	var schedule volumeSnapshotPolicySchedule
	if !snapshotPolicyKnown(object) {
		return schedule, false
	}
	days, daysOK := object.Attributes()[snapshotPolicyDaysOfWeek].(types.Set)
	clock, timeOK := object.Attributes()[snapshotPolicyTime].(types.String)
	if !daysOK || !timeOK {
		return schedule, false
	}
	return volumeSnapshotPolicySchedule{DaysOfWeek: days, Time: clock}, true
}

// snapshotPolicyClockTime returns the API time as HH:MM. The API reports
// HH:MM:00 for a time sent as HH:MM. A non-zero second stays, so the value is
// never changed silently.
func snapshotPolicyClockTime(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == len("15:04:05") && strings.HasSuffix(value, ":00") {
		return strings.TrimSuffix(value, ":00")
	}
	return value
}

// normalizeSnapshotPolicyTime returns HH:MM:SS for HH:MM and HH:MM:SS, so
// that the two spellings of one time compare as equal.
func normalizeSnapshotPolicyTime(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == len("15:04") {
		return value + ":00"
	}
	return value
}

// snapshotPolicyDays returns the trimmed days of a days_of_week set in
// calendar order, without duplicates. Two sets that differ only by outer
// whitespace give the same days. It reports false when the set or a day is
// null or unknown.
func snapshotPolicyDays(set types.Set) ([]blockstoragesdk.SnapshotDayOfWeek, bool) {
	if !snapshotPolicyKnown(set) {
		return nil, false
	}
	days := make([]blockstoragesdk.SnapshotDayOfWeek, 0, len(set.Elements()))
	for _, element := range set.Elements() {
		day, isString := element.(types.String)
		if !isString || !snapshotPolicyKnown(day) {
			return nil, false
		}
		days = append(days, blockstoragesdk.SnapshotDayOfWeek(strings.TrimSpace(day.ValueString())))
	}
	slices.SortFunc(days, func(a, b blockstoragesdk.SnapshotDayOfWeek) int {
		if order := slices.Index(snapshotPolicyWeekdays, a) - slices.Index(snapshotPolicyWeekdays, b); order != 0 {
			return order
		}
		return strings.Compare(string(a), string(b))
	})
	return slices.Compact(days), true
}

// snapshotPolicyTrimmed returns a known value without outer whitespace. The
// API trims request strings, so outer whitespace is not part of a
// user-supplied value. Null and unknown values stay as they are.
func snapshotPolicyTrimmed(v types.String) types.String {
	if !snapshotPolicyKnown(v) {
		return v
	}
	return types.StringValue(strings.TrimSpace(v.ValueString()))
}

// snapshotPolicySameTrimmed reports whether a known value equals the backend
// value after the trim, so that state can keep the configured spelling.
func snapshotPolicySameTrimmed(v types.String, backend string) bool {
	return snapshotPolicyKnown(v) && strings.TrimSpace(v.ValueString()) == backend
}

// snapshotPolicySameTrimmedString reports whether a planned and a prior known
// value differ at most by outer whitespace. The region replacement uses it, so
// that a change of spelling alone updates state in place.
func snapshotPolicySameTrimmedString(plan, state types.String) bool {
	return snapshotPolicyKnown(state) && snapshotPolicySameTrimmed(plan, strings.TrimSpace(state.ValueString()))
}
