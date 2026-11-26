package filestorage

import (
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type FilesystemSnapshotPolicyModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	DaysOfWeek      types.List   `tfsdk:"days_of_week"`
	Time            types.String `tfsdk:"time"`
	Retention       types.Int64  `tfsdk:"retention"`
	Status          types.String `tfsdk:"status"`
	FilesystemCount types.Int64  `tfsdk:"filesystem_count"`
	State           types.String `tfsdk:"state"`
	RegionID        types.String `tfsdk:"region_id"`
	RegionName      types.String `tfsdk:"region_name"`
	ProjectID       types.String `tfsdk:"project_id"`
	OwnerID         types.String `tfsdk:"owner_id"`
	CreatedAt       types.String `tfsdk:"created_at"`
	UpdatedAt       types.String `tfsdk:"updated_at"`
}

// FilesystemSnapshotPolicyResourceModel references the region as an object,
// while the data source filters by a plain region name.
type FilesystemSnapshotPolicyResourceModel struct {
	FilesystemSnapshotPolicyModel
	Region types.Object `tfsdk:"region"`
}

type FilesystemSnapshotPolicyDataSourceModel struct {
	FilesystemSnapshotPolicyModel
	Region types.String `tfsdk:"region"`
}

func populateFilesystemSnapshotPolicyModel(policy *filestoragesdk.FilesystemSnapshotPolicySchema, m *FilesystemSnapshotPolicyModel) {
	days := make([]attr.Value, 0, len(policy.DaysOfWeek))
	for _, day := range policy.DaysOfWeek {
		days = append(days, types.StringValue(day))
	}
	m.ID = types.StringValue(policy.Id.String())
	m.Name = types.StringValue(policy.Name)
	m.Description = types.StringPointerValue(core.NullableToPtr(policy.Description))
	m.DaysOfWeek = types.ListValueMust(types.StringType, days)
	m.Time = types.StringValue(snapshotPolicyClockTime(policy.Time))
	m.Retention = types.Int64Value(int64(policy.Retention))
	m.Status = types.StringValue(string(policy.Status))
	m.FilesystemCount = types.Int64Value(int64(policy.FilesystemCount))
	m.State = types.StringValue(string(policy.State))
	m.RegionID = types.StringValue(policy.Region.Id.String())
	m.RegionName = types.StringValue(policy.Region.Name)
	m.ProjectID = types.StringValue(policy.ProjectId.String())
	m.OwnerID = types.StringNull()
	if owner := core.NullableToPtr(policy.OwnerId); owner != nil {
		m.OwnerID = types.StringValue(owner.String())
	}
	m.CreatedAt = types.StringValue(policy.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(policy.UpdatedAt.Format(time.RFC3339))
}

// populateFilesystemSnapshotPolicyResourceState refreshes resource state from
// a policy response and keeps the configured spelling of inputs that the
// backend normalizes.
func populateFilesystemSnapshotPolicyResourceState(policy *filestoragesdk.FilesystemSnapshotPolicySchema, state *FilesystemSnapshotPolicyResourceModel) {
	prior := *state
	populateFilesystemSnapshotPolicyModel(policy, &state.FilesystemSnapshotPolicyModel)
	preserveFilesystemSnapshotPolicyInputs(policy, &prior.FilesystemSnapshotPolicyModel, &state.FilesystemSnapshotPolicyModel)
	state.Region = reference.Value(reference.Name, types.StringValue(policy.Region.Name))
	if region := reference.Get(prior.Region, reference.Name); knownValue(region) && strings.TrimSpace(region.ValueString()) == policy.Region.Name {
		state.Region = reference.Value(reference.Name, region)
	}
}

func populateFilesystemSnapshotPolicyDataSourceState(policy *filestoragesdk.FilesystemSnapshotPolicySchema, state *FilesystemSnapshotPolicyDataSourceModel) {
	populateFilesystemSnapshotPolicyModel(policy, &state.FilesystemSnapshotPolicyModel)
	state.Region = types.StringValue(policy.Region.Name)
}

func preserveFilesystemSnapshotPolicyInputs(policy *filestoragesdk.FilesystemSnapshotPolicySchema, prior, state *FilesystemSnapshotPolicyModel) {
	if knownValue(prior.Name) && strings.TrimSpace(prior.Name.ValueString()) == policy.Name {
		state.Name = prior.Name
	}
	if description := core.NullableToPtr(policy.Description); description != nil && compare.SameTrimmed(prior.Description, *description) {
		state.Description = prior.Description
	}
	if knownValue(prior.DaysOfWeek) && sameSnapshotPolicyDays(prior.DaysOfWeek, policy.DaysOfWeek) {
		state.DaysOfWeek = prior.DaysOfWeek
	}
	if compare.SameTrimmed(prior.Time, state.Time.ValueString()) {
		state.Time = prior.Time
	}
	if compare.SameTrimmed(prior.Status, string(policy.Status)) {
		state.Status = prior.Status
	}
}

func preserveFilesystemSnapshotPolicyFilters(policy *filestoragesdk.FilesystemSnapshotPolicySchema, config, state *FilesystemSnapshotPolicyDataSourceModel) {
	if compare.SameUUID(config.ID, policy.Id) {
		state.ID = config.ID
	}
	if knownValue(config.Name) && strings.TrimSpace(config.Name.ValueString()) == policy.Name {
		state.Name = config.Name
	}
	if knownValue(config.Region) && strings.TrimSpace(config.Region.ValueString()) == policy.Region.Name {
		state.Region = config.Region
	}
}

func knownValue(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }

// snapshotPolicyClockTime returns the backend time as HH:MM. The backend
// reports HH:MM:00 for a time sent as HH:MM; a non-zero second is kept so the
// value is never silently changed.
func snapshotPolicyClockTime(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == len("15:04:05") && strings.HasSuffix(value, ":00") {
		return strings.TrimSuffix(value, ":00")
	}
	return value
}

// sameSnapshotPolicyDays reports whether the configured days and the reported
// days hold the same values in any order and letter case. Order and case do
// not change the schedule.
func sameSnapshotPolicyDays(configured types.List, reported []string) bool {
	elements := configured.Elements()
	if len(elements) != len(reported) {
		return false
	}
	want := make([]string, 0, len(elements))
	for _, element := range elements {
		day, ok := element.(types.String)
		if !ok || !knownValue(day) {
			return false
		}
		want = append(want, strings.ToLower(strings.TrimSpace(day.ValueString())))
	}
	got := make([]string, 0, len(reported))
	for _, day := range reported {
		got = append(got, strings.ToLower(day))
	}
	slices.Sort(want)
	slices.Sort(got)
	return slices.Equal(want, got)
}
