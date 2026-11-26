package blockstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// sampleDaysOfMonthBackupPolicy returns a policy whose time the API reports
// with seconds.
func sampleDaysOfMonthBackupPolicy(t *testing.T) *blockstoragesdk.VolumeBackupPolicySchema {
	t.Helper()
	policy := sampleVolumeBackupPolicy(t)
	if err := policy.Schedule.FromVolumeBackupPolicyDaysOfMonthScheduleSchema(blockstoragesdk.VolumeBackupPolicyDaysOfMonthScheduleSchema{
		DaysOfMonth: []int{1, 15}, Time: "07:30:00",
	}); err != nil {
		t.Fatalf("build days_of_month schedule: %v", err)
	}
	return policy
}

func backupPolicyRegionReference(name string) types.Object {
	return reference.Value(reference.Name, types.StringValue(name))
}

// daysOfMonthScheduleObject returns a days_of_month schedule object whose
// frequency fields are null.
func daysOfMonthScheduleObject(clock string, days ...int64) types.Object {
	values := make([]attr.Value, 0, len(days))
	for _, day := range days {
		values = append(values, types.Int64Value(day))
	}
	return scheduleObject(volumeBackupPolicyScheduleModel{
		ExecutionStrategy: types.StringValue("days_of_month"), Frequency: types.Int64Null(), FrequencyUnit: types.StringNull(),
		FirstExecution: types.StringNull(), DaysOfMonth: types.SetValueMust(types.Int64Type, values), Time: types.StringValue(clock),
	})
}

// paddedPeriodicScheduleObject returns a periodic schedule whose strings carry
// outer whitespace.
func paddedPeriodicScheduleObject(unit string) types.Object {
	return scheduleObject(volumeBackupPolicyScheduleModel{
		ExecutionStrategy: types.StringValue(" periodic "), Frequency: types.Int64Value(2), FrequencyUnit: types.StringValue(" " + unit + "\t"),
		FirstExecution: types.StringValue(" 2030-01-01T02:00:00Z\n"), DaysOfMonth: types.SetNull(types.Int64Type), Time: types.StringNull(),
	})
}

// configuredVolumeBackupPolicy returns a model with every required input set
// and every computed value null.
func configuredVolumeBackupPolicy() VolumeBackupPolicyResourceModel {
	return VolumeBackupPolicyResourceModel{
		VolumeBackupPolicyModel: VolumeBackupPolicyModel{
			Name: types.StringValue("nightly"), Status: types.StringValue("enabled"),
			Description: types.StringNull(), Retention: types.Int64Null(),
			Schedule: periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z"),
		},
		Region: backupPolicyRegionReference("vn-central-1"),
	}
}

func volumeBackupPolicyResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeBackupPolicyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func volumeBackupPolicyPlan(ctx context.Context, t *testing.T, s schema.Schema, model VolumeBackupPolicyResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func backupPolicyStateOf(ctx context.Context, t *testing.T, s schema.Schema, model VolumeBackupPolicyResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func int64ValidationErrors(t *testing.T, validators []validator.Int64, value int64) int {
	t.Helper()
	var response validator.Int64Response
	for _, v := range validators {
		v.ValidateInt64(context.Background(), validator.Int64Request{Path: path.Root("value"), ConfigValue: types.Int64Value(value)}, &response)
	}
	return response.Diagnostics.ErrorsCount()
}

func int64SetValidationErrors(t *testing.T, validators []validator.Set, values ...int64) int {
	t.Helper()
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.Int64Value(value))
	}
	var response validator.SetResponse
	for _, v := range validators {
		v.ValidateSet(context.Background(), validator.SetRequest{Path: path.Root("value"), ConfigValue: types.SetValueMust(types.Int64Type, elements)}, &response)
	}
	return response.Diagnostics.ErrorsCount()
}

func TestVolumeBackupPolicyResourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, ok := NewVolumeBackupPolicyResource().(*VolumeBackupPolicyResource)
	if !ok {
		t.Fatal("constructor must return *VolumeBackupPolicyResource")
	}
	var metadata resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_volume_backup_policy" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var unconfigured resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{}, &unconfigured)
	if unconfigured.Diagnostics.HasError() || r.client != nil {
		t.Fatalf("nil provider data must be ignored: %v", unconfigured.Diagnostics)
	}
	var wrong resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "wrong"}, &wrong)
	if !wrong.Diagnostics.HasError() {
		t.Fatal("unexpected provider data must be rejected")
	}
	unused := func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") }
	client, project := volumeTestClient(t, unused), volumeTestProjectClient(t, unused)
	var configured resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerdata.Configured{BlockStorage: client, Project: project, ProjectID: volumeTestProjectID}}, &configured)
	if configured.Diagnostics.HasError() || r.client != client || r.project != project || r.projectID != volumeTestProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestVolumeBackupPolicyResourceSchema(t *testing.T) {
	t.Parallel()
	s := volumeBackupPolicyResourceSchema(t)
	volumeBackupPolicyPlan(context.Background(), t, s, VolumeBackupPolicyResourceModel{
		VolumeBackupPolicyModel: VolumeBackupPolicyModel{Schedule: types.ObjectNull(volumeBackupPolicyScheduleAttrTypes)},
		Region:                  types.ObjectNull(reference.AttributeTypes(reference.Name)),
	})

	for _, name := range []string{"name", "status"} {
		if attribute := s.Attributes[name].(schema.StringAttribute); !attribute.Required || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be required and update in place: %#v", name, attribute)
		}
	}
	if description := s.Attributes["description"].(schema.StringAttribute); !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 {
		t.Errorf("description must be optional, computed, and keep its state: %#v", description)
	}
	if retention := s.Attributes["retention"].(schema.Int64Attribute); !retention.Optional || !retention.Computed || len(retention.PlanModifiers) != 1 {
		t.Errorf("retention must be optional, computed, and keep its state: %#v", retention)
	}
	region := s.Attributes["region"].(schema.SingleNestedAttribute)
	if !region.Required || len(region.PlanModifiers) != 1 {
		t.Fatalf("region must be a required reference with one plan modifier: %#v", region)
	}
	if criterion, ok := region.Attributes[reference.Name].(schema.StringAttribute); !ok || !criterion.Required || len(region.Attributes) != 1 {
		t.Fatalf("region must hold one required name: %#v", region.Attributes)
	}
	if !planBackupPolicyRegion(t, region.PlanModifiers[0], backupPolicyRegionReference("vn-central-1"), backupPolicyRegionReference("vn-north-1")) {
		t.Error("changing region.name must replace the policy")
	}
	if planBackupPolicyRegion(t, region.PlanModifiers[0], backupPolicyRegionReference("vn-central-1"), backupPolicyRegionReference("vn-central-1")) {
		t.Error("an unchanged region.name must not replace the policy")
	}
	if planBackupPolicyRegion(t, region.PlanModifiers[0], backupPolicyRegionReference("vn-central-1"), backupPolicyRegionReference(" vn-central-1 ")) {
		t.Error("a whitespace-only edit of region.name must not replace the policy")
	}
	if !planBackupPolicyRegion(t, region.PlanModifiers[0], backupPolicyRegionReference("vn-central-1"), types.ObjectUnknown(reference.AttributeTypes(reference.Name))) {
		t.Error("an unknown region must replace the policy")
	}

	scheduleAttribute := s.Attributes["schedule"].(schema.SingleNestedAttribute)
	if !scheduleAttribute.Required || len(scheduleAttribute.PlanModifiers) != 0 || len(scheduleAttribute.Attributes) != len(volumeBackupPolicyScheduleAttrTypes) {
		t.Fatalf("schedule must be a required object that updates in place: %#v", scheduleAttribute)
	}
	for name := range volumeBackupPolicyScheduleAttrTypes {
		var required, optional, computed bool
		switch attribute := scheduleAttribute.Attributes[name].(type) {
		case schema.StringAttribute:
			required, optional, computed = attribute.Required, attribute.Optional, attribute.Computed
		case schema.Int64Attribute:
			required, optional, computed = attribute.Required, attribute.Optional, attribute.Computed
		case schema.SetAttribute:
			required, optional, computed = attribute.Required, attribute.Optional, attribute.Computed
		default:
			t.Fatalf("schedule.%s has unexpected type %T", name, attribute)
		}
		if wantRequired := name == "execution_strategy"; required != wantRequired || optional == wantRequired || computed {
			t.Errorf("schedule.%s: required=%v optional=%v computed=%v", name, required, optional, computed)
		}
	}

	for name, keepsState := range map[string]bool{
		"id": true, "created_at": true, "state": false, "volume_count": false,
		"next_execution": false, "last_execution": false, "updated_at": false,
	} {
		var computed bool
		var modifiers int
		switch attribute := s.Attributes[name].(type) {
		case schema.StringAttribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		case schema.Int64Attribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		default:
			t.Fatalf("%s has unexpected type %T", name, attribute)
		}
		if !computed || (modifiers == 1) != keepsState {
			t.Errorf("%s: computed=%v plan modifiers=%d, want UseStateForUnknown=%v", name, computed, modifiers, keepsState)
		}
	}
}

func planBackupPolicyRegion(t *testing.T, modifier planmodifier.Object, state, plan types.Object) bool {
	t.Helper()
	response := planmodifier.ObjectResponse{PlanValue: plan}
	raw := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	modifier.PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw}, Config: tfsdk.Config{Raw: raw},
		StateValue: state, PlanValue: plan, ConfigValue: plan,
	}, &response)
	return response.RequiresReplace
}

func TestVolumeBackupPolicyAttributeValidators(t *testing.T) {
	t.Parallel()
	s := volumeBackupPolicyResourceSchema(t)
	scheduleAttributes := s.Attributes["schedule"].(schema.SingleNestedAttribute).Attributes
	name := s.Attributes["name"].(schema.StringAttribute).Validators
	description := s.Attributes["description"].(schema.StringAttribute).Validators
	status := s.Attributes["status"].(schema.StringAttribute).Validators
	retention := s.Attributes["retention"].(schema.Int64Attribute).Validators
	strategy := scheduleAttributes["execution_strategy"].(schema.StringAttribute).Validators
	frequency := scheduleAttributes["frequency"].(schema.Int64Attribute).Validators
	unit := scheduleAttributes["frequency_unit"].(schema.StringAttribute).Validators
	days := scheduleAttributes["days_of_month"].(schema.SetAttribute).Validators
	clock := scheduleAttributes["time"].(schema.StringAttribute).Validators

	for _, tt := range []struct {
		name string
		got  int
		want int
	}{
		{"name 100 characters", stringValidationErrors(t, name, strings.Repeat("a", 100)), 0},
		{"name 101 characters", stringValidationErrors(t, name, strings.Repeat("a", 101)), 1},
		{"padded name of 100 characters", stringValidationErrors(t, name, "  "+strings.Repeat("a", 100)+"\t"), 0},
		{"empty name", stringValidationErrors(t, name, ""), 1},
		{"blank name", stringValidationErrors(t, name, "  "), 1},
		{"description 255 characters", stringValidationErrors(t, description, strings.Repeat("a", 255)), 0},
		{"padded description of 255 characters", stringValidationErrors(t, description, " "+strings.Repeat("a", 255)+" "), 0},
		{"description 256 characters", stringValidationErrors(t, description, strings.Repeat("a", 256)), 1},
		{"empty description clears", stringValidationErrors(t, description, ""), 0},
		{"status enabled", stringValidationErrors(t, status, "enabled"), 0},
		{"status disabled", stringValidationErrors(t, status, "disabled"), 0},
		{"padded status", stringValidationErrors(t, status, " enabled\n"), 0},
		{"status capitalized", stringValidationErrors(t, status, "Enabled"), 1},
		{"retention 1", int64ValidationErrors(t, retention, 1), 0},
		{"retention 65535", int64ValidationErrors(t, retention, 65535), 0},
		{"retention 0", int64ValidationErrors(t, retention, 0), 1},
		{"retention 65536", int64ValidationErrors(t, retention, 65536), 1},
		{"strategy periodic", stringValidationErrors(t, strategy, "periodic"), 0},
		{"strategy days_of_month", stringValidationErrors(t, strategy, "days_of_month"), 0},
		{"padded strategy", stringValidationErrors(t, strategy, " periodic "), 0},
		{"strategy days_of_week", stringValidationErrors(t, strategy, "days_of_week"), 1},
		{"frequency 1000", int64ValidationErrors(t, frequency, 1000), 0},
		{"frequency 0", int64ValidationErrors(t, frequency, 0), 1},
		{"frequency 1001", int64ValidationErrors(t, frequency, 1001), 1},
		{"unit week", stringValidationErrors(t, unit, "week"), 0},
		{"padded unit", stringValidationErrors(t, unit, " week "), 0},
		{"unit year", stringValidationErrors(t, unit, "year"), 1},
		{"days 1 and 31", int64SetValidationErrors(t, days, 1, 31), 0},
		{"day 0", int64SetValidationErrors(t, days, 0), 1},
		{"day 32", int64SetValidationErrors(t, days, 32), 1},
		{"no days", int64SetValidationErrors(t, days), 1},
		{"time HH:MM", stringValidationErrors(t, clock, "07:30"), 0},
		{"time HH:MM:SS", stringValidationErrors(t, clock, "07:30:15"), 0},
		{"padded time", stringValidationErrors(t, clock, " 07:30 "), 0},
		{"hour 24", stringValidationErrors(t, clock, "24:00"), 1},
		{"time with zone", stringValidationErrors(t, clock, "07:30Z"), 1},
		{"time without minutes", stringValidationErrors(t, clock, "7"), 1},
	} {
		if tt.got != tt.want {
			t.Errorf("%s: %d errors, want %d", tt.name, tt.got, tt.want)
		}
	}
}

// AC5: the API keeps description and retention when a request omits them, so
// the plan must keep the prior values and stay stable.
func TestVolumeBackupPolicyOptionalValuesKeepStateWhenOmitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	raw := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})

	description := s.Attributes["description"].(schema.StringAttribute).PlanModifiers[0]
	stringResponse := planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	description.PlanModifyString(ctx, planmodifier.StringRequest{
		State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw}, Config: tfsdk.Config{Raw: raw},
		StateValue: types.StringValue("before"), PlanValue: types.StringUnknown(), ConfigValue: types.StringNull(),
	}, &stringResponse)
	if stringResponse.PlanValue.ValueString() != "before" {
		t.Fatalf("an omitted description must plan the prior value, got %v", stringResponse.PlanValue)
	}

	retention := s.Attributes["retention"].(schema.Int64Attribute).PlanModifiers[0]
	int64Response := planmodifier.Int64Response{PlanValue: types.Int64Unknown()}
	retention.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw}, Config: tfsdk.Config{Raw: raw},
		StateValue: types.Int64Value(5), PlanValue: types.Int64Unknown(), ConfigValue: types.Int64Null(),
	}, &int64Response)
	if int64Response.PlanValue.ValueInt64() != 5 {
		t.Fatalf("an omitted retention must plan the prior value, got %v", int64Response.PlanValue)
	}
}

func TestVolumeBackupPolicyResponseMapping(t *testing.T) {
	t.Parallel()
	policy := sampleVolumeBackupPolicy(t)
	var m VolumeBackupPolicyModel
	if diags := populateVolumeBackupPolicyModel(policy, &m); diags.HasError() {
		t.Fatalf("populate: %v", diags)
	}
	if m.ID.ValueString() != backupPolicyTestID.String() || m.Name.ValueString() != "nightly" || !m.Description.IsNull() ||
		m.Status.ValueString() != "enabled" || m.Retention.ValueInt64() != 1 || m.State.ValueString() != "ready" || m.VolumeCount.ValueInt64() != 2 ||
		!m.NextExecution.IsNull() || !m.LastExecution.IsNull() ||
		m.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || m.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" ||
		!m.Schedule.Equal(periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z")) {
		t.Fatalf("periodic response mapping: %#v", m)
	}

	next, last := backupPolicyFirstExecution, backupPolicyFirstExecution.AddDate(0, 0, -1)
	policy.Description = core.NullableFromPtr(new(""))
	policy.NextExecution, policy.LastExecution = core.NullableFromPtr(&next), core.NullableFromPtr(&last)
	if diags := populateVolumeBackupPolicyModel(policy, &m); diags.HasError() {
		t.Fatalf("populate: %v", diags)
	}
	if m.Description.IsNull() || m.Description.ValueString() != "" || m.NextExecution.ValueString() != "2030-01-01T02:00:00Z" || m.LastExecution.ValueString() != "2029-12-31T02:00:00Z" {
		t.Fatalf("an empty description must be known, and executions must map: %#v", m)
	}

	if diags := populateVolumeBackupPolicyModel(sampleDaysOfMonthBackupPolicy(t), &m); diags.HasError() || !m.Schedule.Equal(daysOfMonthScheduleObject("07:30:00", 1, 15)) {
		t.Fatalf("days_of_month response mapping: %v %v", m.Schedule, diags)
	}
}

// Another client can create a schedule that the schema cannot hold. Read,
// import, and the data source must then fail instead of writing a partial
// state.
func TestVolumeBackupPolicyResponseWithUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	body := `{"id":"` + backupPolicyTestID.String() + `","name":"weekly","retention":1,"status":"enabled","state":"ready","volume_count":0,` +
		`"schedule":{"execution_strategy":"days_of_week","days_of_week":["mon"],"time":"02:00:00"},` +
		`"region":{"id":"` + backupPolicyTestRegionID.String() + `","name":"vn-central-1","description":""},` +
		`"project":{"id":"` + volumeTestProjectID.String() + `","name":"project","slug":"p"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, body), nil
	}), projectID: volumeTestProjectID}
	model := configuredVolumeBackupPolicy()
	model.ID = types.StringValue(backupPolicyTestID.String())
	state := backupPolicyStateOf(ctx, t, s, model)

	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("read must reject an unsupported schedule")
	}
	detail := read.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, backupPolicyTestID.String()) || !strings.Contains(detail, "days_of_week") {
		t.Fatalf("the error must name the id and the strategy: %q", detail)
	}
	if !read.State.Raw.Equal(state.Raw) {
		t.Fatal("a failed read must leave state unchanged")
	}
}

func TestPopulateVolumeBackupPolicyResourceStatePreservesConfiguredValues(t *testing.T) {
	t.Parallel()
	nullPrior := VolumeBackupPolicyResourceModel{
		VolumeBackupPolicyModel: VolumeBackupPolicyModel{Schedule: types.ObjectNull(volumeBackupPolicyScheduleAttrTypes)},
		Region:                  types.ObjectNull(reference.AttributeTypes(reference.Name)),
	}
	periodic := sampleVolumeBackupPolicy(t)
	monthly := sampleDaysOfMonthBackupPolicy(t)
	for _, tt := range []struct {
		name         string
		policy       *blockstoragesdk.VolumeBackupPolicySchema
		prior        VolumeBackupPolicyResourceModel
		wantName     string
		wantSchedule types.Object
		wantRegion   string
	}{
		{"import has no prior", periodic, nullPrior, "nightly", periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z"), "vn-central-1"},
		{"configured spelling", periodic, VolumeBackupPolicyResourceModel{
			VolumeBackupPolicyModel: VolumeBackupPolicyModel{Name: types.StringValue(" nightly "), Schedule: periodicScheduleObject(2, "day", "2030-01-01T09:00:00+07:00")},
			Region:                  backupPolicyRegionReference(" vn-central-1 "),
		}, " nightly ", periodicScheduleObject(2, "day", "2030-01-01T09:00:00+07:00"), " vn-central-1 "},
		{"time without seconds", monthly, VolumeBackupPolicyResourceModel{
			VolumeBackupPolicyModel: VolumeBackupPolicyModel{Name: types.StringValue("nightly"), Schedule: daysOfMonthScheduleObject("07:30", 1, 15)},
			Region:                  backupPolicyRegionReference("vn-central-1"),
		}, "nightly", daysOfMonthScheduleObject("07:30", 1, 15), "vn-central-1"},
		{"drift is reported", periodic, VolumeBackupPolicyResourceModel{
			VolumeBackupPolicyModel: VolumeBackupPolicyModel{Name: types.StringValue("old"), Schedule: periodicScheduleObject(1, "week", "2030-02-01T02:00:00Z")},
			Region:                  backupPolicyRegionReference("vn-north-1"),
		}, "nightly", periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z"), "vn-central-1"},
		{"strategy change drops the old fields", monthly, VolumeBackupPolicyResourceModel{
			VolumeBackupPolicyModel: VolumeBackupPolicyModel{Name: types.StringValue("nightly"), Schedule: periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z")},
			Region:                  backupPolicyRegionReference("vn-central-1"),
		}, "nightly", daysOfMonthScheduleObject("07:30:00", 1, 15), "vn-central-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.prior
			if diags := populateVolumeBackupPolicyResourceState(tt.policy, &state); diags.HasError() {
				t.Fatal(diags)
			}
			if state.Name.ValueString() != tt.wantName || !state.Schedule.Equal(tt.wantSchedule) || !state.Region.Equal(backupPolicyRegionReference(tt.wantRegion)) {
				t.Fatalf("state: name=%v schedule=%v region=%v", state.Name, state.Schedule, state.Region)
			}
		})
	}

	policy := sampleVolumeBackupPolicy(t)
	policy.Description = core.NullableFromPtr(new("daily"))
	state := configuredVolumeBackupPolicy()
	state.Description = types.StringValue(" daily ")
	if diags := populateVolumeBackupPolicyResourceState(policy, &state); diags.HasError() || state.Description.ValueString() != " daily " {
		t.Fatalf("a trimmed description must keep its configured spelling: %v %v", state.Description, diags)
	}
}

func TestValidateVolumeBackupPolicySchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if diags := validateVolumeBackupPolicySchedule(ctx, periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z")); diags.HasError() {
		t.Fatalf("valid periodic schedule: %v", diags)
	}
	if diags := validateVolumeBackupPolicySchedule(ctx, daysOfMonthScheduleObject("07:30", 1, 15)); diags.HasError() {
		t.Fatalf("valid days_of_month schedule: %v", diags)
	}
	if diags := validateVolumeBackupPolicySchedule(ctx, paddedPeriodicScheduleObject("day")); diags.HasError() {
		t.Fatalf("outer whitespace must not fail the schedule check: %v", diags)
	}
	periodic := func(mutate func(*volumeBackupPolicyScheduleModel)) types.Object {
		model := volumeBackupPolicyScheduleModel{
			ExecutionStrategy: types.StringValue("periodic"), Frequency: types.Int64Value(2), FrequencyUnit: types.StringValue("day"),
			FirstExecution: types.StringValue("2030-01-01T02:00:00Z"), DaysOfMonth: types.SetNull(types.Int64Type), Time: types.StringNull(),
		}
		mutate(&model)
		return scheduleObject(model)
	}
	monthly := func(mutate func(*volumeBackupPolicyScheduleModel)) types.Object {
		model := volumeBackupPolicyScheduleModel{
			ExecutionStrategy: types.StringValue("days_of_month"), Frequency: types.Int64Null(), FrequencyUnit: types.StringNull(),
			FirstExecution: types.StringNull(), DaysOfMonth: types.SetValueMust(types.Int64Type, []attr.Value{types.Int64Value(1)}), Time: types.StringValue("07:30"),
		}
		mutate(&model)
		return scheduleObject(model)
	}
	for _, tt := range []struct {
		name     string
		schedule types.Object
		want     string
	}{
		{"periodic without frequency", periodic(func(m *volumeBackupPolicyScheduleModel) { m.Frequency = types.Int64Null() }), "schedule.frequency"},
		{"periodic without unit", periodic(func(m *volumeBackupPolicyScheduleModel) { m.FrequencyUnit = types.StringNull() }), "schedule.frequency_unit"},
		{"periodic without first execution", periodic(func(m *volumeBackupPolicyScheduleModel) { m.FirstExecution = types.StringNull() }), "schedule.first_execution"},
		{"periodic with time", periodic(func(m *volumeBackupPolicyScheduleModel) { m.Time = types.StringValue("07:30") }), "schedule.time"},
		{"periodic with days", periodic(func(m *volumeBackupPolicyScheduleModel) {
			m.DaysOfMonth = types.SetValueMust(types.Int64Type, []attr.Value{types.Int64Value(1)})
		}), "schedule.days_of_month"},
		{"periodic with invalid first execution", periodic(func(m *volumeBackupPolicyScheduleModel) { m.FirstExecution = types.StringValue("2030-01-01") }), "schedule.first_execution"},
		{"days_of_month without days", monthly(func(m *volumeBackupPolicyScheduleModel) { m.DaysOfMonth = types.SetNull(types.Int64Type) }), "schedule.days_of_month"},
		{"days_of_month without time", monthly(func(m *volumeBackupPolicyScheduleModel) { m.Time = types.StringNull() }), "schedule.time"},
		{"days_of_month with frequency", monthly(func(m *volumeBackupPolicyScheduleModel) { m.Frequency = types.Int64Value(1) }), "schedule.frequency"},
		{"days_of_month with unit", monthly(func(m *volumeBackupPolicyScheduleModel) { m.FrequencyUnit = types.StringValue("day") }), "schedule.frequency_unit"},
		{"days_of_month with first execution", monthly(func(m *volumeBackupPolicyScheduleModel) { m.FirstExecution = types.StringValue("2030-01-01T02:00:00Z") }), "schedule.first_execution"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			diags := validateVolumeBackupPolicySchedule(ctx, tt.schedule)
			if diags.ErrorsCount() != 1 {
				t.Fatalf("want one error, got %v", diags)
			}
			if got := diags.Errors()[0].(interface{ Path() path.Path }).Path().String(); got != tt.want {
				t.Fatalf("error path = %s, want %s", got, tt.want)
			}
		})
	}

	unknown := scheduleObject(volumeBackupPolicyScheduleModel{
		ExecutionStrategy: types.StringValue("periodic"), Frequency: types.Int64Unknown(), FrequencyUnit: types.StringUnknown(),
		FirstExecution: types.StringUnknown(), DaysOfMonth: types.SetNull(types.Int64Type), Time: types.StringNull(),
	})
	unknownStrategy := scheduleObject(volumeBackupPolicyScheduleModel{
		ExecutionStrategy: types.StringUnknown(), Frequency: types.Int64Unknown(), FrequencyUnit: types.StringUnknown(),
		FirstExecution: types.StringUnknown(), DaysOfMonth: types.SetUnknown(types.Int64Type), Time: types.StringUnknown(),
	})
	unknownForbidden := periodic(func(m *volumeBackupPolicyScheduleModel) {
		m.DaysOfMonth = types.SetUnknown(types.Int64Type)
		m.Time = types.StringUnknown()
	})
	for name, schedule := range map[string]types.Object{
		"unknown fields": unknown, "unknown strategy": unknownStrategy, "unknown forbidden fields": unknownForbidden,
		"unknown object": types.ObjectUnknown(volumeBackupPolicyScheduleAttrTypes), "null object": types.ObjectNull(volumeBackupPolicyScheduleAttrTypes),
	} {
		if diags := validateVolumeBackupPolicySchedule(ctx, schedule); diags.HasError() {
			t.Errorf("%s must pass validation: %v", name, diags)
		}
	}
}

func TestVolumeBackupPolicyValidateConfigReportsInvalidSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	model := configuredVolumeBackupPolicy()
	model.Schedule = scheduleObject(volumeBackupPolicyScheduleModel{
		ExecutionStrategy: types.StringValue("days_of_month"), Frequency: types.Int64Null(), FrequencyUnit: types.StringNull(),
		FirstExecution: types.StringNull(), DaysOfMonth: types.SetNull(types.Int64Type), Time: types.StringValue("07:30"),
	})
	var response resource.ValidateConfigResponse
	(&VolumeBackupPolicyResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: volumeBackupPolicyPlan(ctx, t, s, model).Raw}}, &response)
	if response.Diagnostics.ErrorsCount() != 1 || response.Diagnostics.Errors()[0].Summary() != "Missing volume backup policy schedule field" {
		t.Fatalf("want one missing field error, got %v", response.Diagnostics)
	}
}

func TestBuildVolumeBackupPolicyCreateBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plan := configuredVolumeBackupPolicy()
	plan.Name, plan.Region = types.StringValue(" nightly "), backupPolicyRegionReference(" vn-central-1 ")
	resolve := func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		if *filter.Name != "vn-central-1" {
			t.Errorf("the region lookup must receive the trimmed name, got %q", *filter.Name)
		}
		return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: backupPolicyTestRegionID, Name: *filter.Name}}, nil
	}
	body, diags := buildVolumeBackupPolicyCreateBody(ctx, plan, resolve)
	if diags.HasError() || body.Name != "nightly" || body.RegionId != backupPolicyTestRegionID || body.Status != blockstoragesdk.VolumeBackupPolicyStatusEnabled {
		t.Fatalf("create body: %#v, %v", body, diags)
	}
	encoded := marshalBackupPolicyBody(t, body)
	if _, present := encoded["description"]; present {
		t.Fatalf("an omitted description must stay out of the request: %v", encoded)
	}
	if _, present := encoded["retention"]; present {
		t.Fatalf("an omitted retention must stay out of the request: %v", encoded)
	}
	wantSchedule := map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(2), "frequency_unit": "day"}
	if !sameJSONObject(encoded["schedule"], wantSchedule) {
		t.Fatalf("periodic schedule = %v, want %v", encoded["schedule"], wantSchedule)
	}

	plan.Description, plan.Retention = types.StringUnknown(), types.Int64Unknown()
	body, diags = buildVolumeBackupPolicyCreateBody(ctx, plan, resolve)
	encoded = marshalBackupPolicyBody(t, body)
	if _, present := encoded["description"]; diags.HasError() || present || encoded["retention"] != nil {
		t.Fatalf("unknown description and retention must stay out of the request: %v %v", encoded, diags)
	}

	plan.Description, plan.Retention = types.StringValue(""), types.Int64Value(7)
	plan.Schedule = daysOfMonthScheduleObject("07:30", 15, 1)
	body, diags = buildVolumeBackupPolicyCreateBody(ctx, plan, resolve)
	encoded = marshalBackupPolicyBody(t, body)
	if diags.HasError() || encoded["description"] != "" || encoded["retention"] != float64(7) {
		t.Fatalf("configured description and retention: %v %v", encoded, diags)
	}
	wantSchedule = map[string]any{"execution_strategy": "days_of_month", "days_of_month": []any{float64(1), float64(15)}, "time": "07:30"}
	if !sameJSONObject(encoded["schedule"], wantSchedule) {
		t.Fatalf("days_of_month schedule = %v, want %v", encoded["schedule"], wantSchedule)
	}

	padded := scheduleModelOf(t, plan.Schedule)
	padded.ExecutionStrategy, padded.Time = types.StringValue(" days_of_month "), types.StringValue("\t07:30 ")
	plan.Schedule, plan.Status, plan.Description = scheduleObject(padded), types.StringValue(" enabled "), types.StringValue(" daily ")
	body, diags = buildVolumeBackupPolicyCreateBody(ctx, plan, resolve)
	encoded = marshalBackupPolicyBody(t, body)
	if diags.HasError() || encoded["status"] != "enabled" || encoded["description"] != "daily" || !sameJSONObject(encoded["schedule"], wantSchedule) {
		t.Fatalf("padded values must reach the request trimmed: %v %v", encoded, diags)
	}

	apiErr := errors.New("region list failed")
	_, diags = buildVolumeBackupPolicyCreateBody(ctx, plan, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		return projectsdk.ProjectRegionSchema{}, apiErr
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), apiErr.Error()) {
		t.Fatalf("region resolver error: %v", diags)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*VolumeBackupPolicyResourceModel)
	}{
		{"unknown region", func(m *VolumeBackupPolicyResourceModel) {
			m.Region = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
		}},
		{"blank region", func(m *VolumeBackupPolicyResourceModel) { m.Region = backupPolicyRegionReference(" ") }},
		{"unknown name", func(m *VolumeBackupPolicyResourceModel) { m.Name = types.StringUnknown() }},
		{"unknown status", func(m *VolumeBackupPolicyResourceModel) { m.Status = types.StringUnknown() }},
		{"unknown schedule", func(m *VolumeBackupPolicyResourceModel) {
			m.Schedule = types.ObjectUnknown(volumeBackupPolicyScheduleAttrTypes)
		}},
		{"unknown first execution", func(m *VolumeBackupPolicyResourceModel) {
			model := scheduleModelOf(t, m.Schedule)
			model.FirstExecution = types.StringUnknown()
			m.Schedule = scheduleObject(model)
		}},
		{"missing frequency", func(m *VolumeBackupPolicyResourceModel) {
			model := scheduleModelOf(t, m.Schedule)
			model.Frequency = types.Int64Null()
			m.Schedule = scheduleObject(model)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := configuredVolumeBackupPolicy()
			tt.mutate(&m)
			if _, diags := buildVolumeBackupPolicyCreateBody(ctx, m, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
				t.Error("an invalid plan must not resolve the region")
				return projectsdk.ProjectRegionSchema{}, nil
			}); !diags.HasError() {
				t.Fatal("want a diagnostic")
			}
		})
	}
}

func TestBuildVolumeBackupPolicyPatchBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state := configuredVolumeBackupPolicy().VolumeBackupPolicyModel
	state.Description, state.Retention = types.StringValue("before"), types.Int64Value(3)
	for _, tt := range []struct {
		name    string
		mutate  func(*VolumeBackupPolicyModel)
		want    map[string]any
		changed bool
	}{
		{"nothing changed", func(*VolumeBackupPolicyModel) {}, map[string]any{}, false},
		{"only computed values changed", func(m *VolumeBackupPolicyModel) {
			m.State, m.UpdatedAt, m.VolumeCount = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
		}, map[string]any{}, false},
		{"unknown optional values are omitted", func(m *VolumeBackupPolicyModel) {
			m.Description, m.Retention = types.StringUnknown(), types.Int64Unknown()
		}, map[string]any{}, false},
		{"null optional values are omitted", func(m *VolumeBackupPolicyModel) {
			m.Description, m.Retention = types.StringNull(), types.Int64Null()
		}, map[string]any{}, false},
		{"name is trimmed", func(m *VolumeBackupPolicyModel) { m.Name = types.StringValue(" renamed ") }, map[string]any{"name": "renamed"}, true},
		{"description cleared", func(m *VolumeBackupPolicyModel) { m.Description = types.StringValue("") }, map[string]any{"description": ""}, true},
		{"retention", func(m *VolumeBackupPolicyModel) { m.Retention = types.Int64Value(9) }, map[string]any{"retention": float64(9)}, true},
		{"status", func(m *VolumeBackupPolicyModel) { m.Status = types.StringValue("disabled") }, map[string]any{"status": "disabled"}, true},
		{
			"one schedule field sends the whole schedule", func(m *VolumeBackupPolicyModel) {
				m.Schedule = periodicScheduleObject(2, "week", "2030-01-01T02:00:00Z")
			},
			map[string]any{"schedule": map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(2), "frequency_unit": "week"}},
			true,
		},
		{
			"strategy change", func(m *VolumeBackupPolicyModel) { m.Schedule = daysOfMonthScheduleObject("07:30", 1) },
			map[string]any{"schedule": map[string]any{"execution_strategy": "days_of_month", "days_of_month": []any{float64(1)}, "time": "07:30"}},
			true,
		},
		{"respelled first execution is not a change", func(m *VolumeBackupPolicyModel) {
			m.Schedule = periodicScheduleObject(2, "day", "2030-01-01T09:00:00+07:00")
		}, map[string]any{}, false},
		{"whitespace-only edits are not a change", func(m *VolumeBackupPolicyModel) {
			m.Name, m.Description, m.Status = types.StringValue(" nightly "), types.StringValue(" before "), types.StringValue(" enabled ")
			m.Schedule = paddedPeriodicScheduleObject("day")
		}, map[string]any{}, false},
		{"padded values are sent trimmed", func(m *VolumeBackupPolicyModel) {
			m.Description, m.Status = types.StringValue(" after "), types.StringValue(" disabled ")
			m.Schedule = paddedPeriodicScheduleObject("week")
		}, map[string]any{
			"description": "after", "status": "disabled",
			"schedule": map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(2), "frequency_unit": "week"},
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := state
			tt.mutate(&plan)
			body, changed, diags := buildVolumeBackupPolicyPatchBody(ctx, plan, state)
			if diags.HasError() || changed != tt.changed {
				t.Fatalf("changed=%v want %v, %v", changed, tt.changed, diags)
			}
			if got := marshalBackupPolicyBody(t, body); !sameJSONObject(got, tt.want) {
				t.Fatalf("body = %v, want %v", got, tt.want)
			}
		})
	}

	monthlyState := state
	monthlyState.Schedule = daysOfMonthScheduleObject("07:30:00", 1, 15)
	plan := monthlyState
	plan.Schedule = daysOfMonthScheduleObject("07:30", 15, 1)
	if _, changed, diags := buildVolumeBackupPolicyPatchBody(ctx, plan, monthlyState); diags.HasError() || changed {
		t.Fatalf("a respelled time and reordered days are not a change: %v %v", changed, diags)
	}
	plan.Schedule = daysOfMonthScheduleObject("08:00", 1, 15)
	if _, changed, diags := buildVolumeBackupPolicyPatchBody(ctx, plan, monthlyState); diags.HasError() || !changed {
		t.Fatalf("a new time is a change: %v %v", changed, diags)
	}
	plan.Schedule = types.ObjectUnknown(volumeBackupPolicyScheduleAttrTypes)
	if _, _, diags := buildVolumeBackupPolicyPatchBody(ctx, plan, monthlyState); !diags.HasError() {
		t.Fatal("an unknown schedule must fail instead of sending a partial body")
	}
}

func TestVolumeBackupPolicyResourceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	policy := sampleVolumeBackupPolicy(t)
	itemPath := backupPoliciesPath + backupPolicyTestID.String() + "/"
	regionsPath := "/v2/projects/" + volumeTestProjectID.String() + "/regions/"
	var calls []string
	roundTrip := func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		if req.URL.Path == regionsPath {
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		}
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing Project-ID header on %s %s", req.Method, req.URL)
		}
		var body map[string]any
		if req.Body != nil && req.Method != http.MethodGet && req.Method != http.MethodDelete {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == backupPoliciesPath:
			want := map[string]any{
				"name": "nightly", "region_id": backupPolicyTestRegionID.String(), "status": "enabled",
				"schedule": map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(2), "frequency_unit": "day"},
			}
			if !sameJSONObject(body, want) {
				t.Errorf("create body = %v, want %v", body, want)
			}
			return testHTTPResponse(req, http.StatusCreated, volumeBackupPolicyJSON(t, policy)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyJSON(t, policy)), nil
		case req.Method == http.MethodPatch && req.URL.Path == itemPath:
			want := map[string]any{
				"name": "renamed", "status": "disabled", "description": "",
				"schedule": map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(3), "frequency_unit": "day"},
			}
			if !sameJSONObject(body, want) {
				t.Errorf("patch body = %v, want %v", body, want)
			}
			policy.Name, policy.Status, policy.Description = "renamed", blockstoragesdk.VolumeBackupPolicyStatusDisabled, core.NullableFromPtr(new(""))
			if err := policy.Schedule.FromVolumeBackupPolicyPeriodicScheduleSchema(blockstoragesdk.VolumeBackupPolicyPeriodicScheduleSchema{
				FirstExecution: backupPolicyFirstExecution, Frequency: 3, FrequencyUnit: blockstoragesdk.PolicyFrequencyUnitDay,
			}); err != nil {
				t.Fatal(err)
			}
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyJSON(t, policy)), nil
		case req.Method == http.MethodDelete && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusNoContent, ""), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	model := unknownBackupPolicyComputed(configuredVolumeBackupPolicy())
	model.Description, model.Retention = types.StringUnknown(), types.Int64Unknown()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdState VolumeBackupPolicyResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.ID.ValueString() != backupPolicyTestID.String() ||
		createdState.Retention.ValueInt64() != 1 || !createdState.Description.IsNull() || createdState.VolumeCount.ValueInt64() != 2 ||
		createdState.State.ValueString() != "ready" || !createdState.NextExecution.IsNull() || !createdState.LastExecution.IsNull() ||
		!createdState.Region.Equal(backupPolicyRegionReference("vn-central-1")) {
		t.Fatalf("created state: %#v %v", createdState, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("refresh must keep a stable state: %v", read.Diagnostics)
	}

	updatedModel := unknownBackupPolicyComputed(createdState)
	updatedModel.Name, updatedModel.Status, updatedModel.Description = types.StringValue("renamed"), types.StringValue("disabled"), types.StringValue("")
	updatedModel.Schedule = periodicScheduleObject(3, "day", "2030-01-01T02:00:00Z")
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, updatedModel), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	var updatedState VolumeBackupPolicyResourceModel
	if diags := updated.State.Get(ctx, &updatedState); diags.HasError() || updatedState.ID.ValueString() != backupPolicyTestID.String() ||
		updatedState.Name.ValueString() != "renamed" || updatedState.Status.ValueString() != "disabled" || updatedState.Description.IsNull() ||
		updatedState.Description.ValueString() != "" || updatedState.Retention.ValueInt64() != 1 ||
		!updatedState.Schedule.Equal(periodicScheduleObject(3, "day", "2030-01-01T02:00:00Z")) {
		t.Fatalf("updated state: %#v %v", updatedState, diags)
	}

	respelled := unknownBackupPolicyComputed(updatedState)
	respelled.Schedule = periodicScheduleObject(3, "day", "2030-01-01T09:00:00+07:00")
	unchanged := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, respelled), State: updated.State}, &unchanged)
	var unchangedState VolumeBackupPolicyResourceModel
	if diags := unchanged.State.Get(ctx, &unchangedState); unchanged.Diagnostics.HasError() || diags.HasError() ||
		unchangedState.State.IsUnknown() || unchangedState.UpdatedAt.IsUnknown() || !unchangedState.Schedule.Equal(respelled.Schedule) {
		t.Fatalf("an update that changes no field must read the policy and keep the configured spelling: %#v %v %v", unchangedState, unchanged.Diagnostics, diags)
	}

	removed := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &removed)
	if removed.Diagnostics.HasError() {
		t.Fatalf("delete: %v", removed.Diagnostics)
	}
	want := []string{"GET " + regionsPath, "POST " + backupPoliciesPath, "GET " + itemPath, "PATCH " + itemPath, "GET " + itemPath, "DELETE " + itemPath}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", calls, want)
	}
}

// The API trims string inputs, so a request must carry the trimmed values,
// state after Create and Read must keep the configured spelling, and an edit of
// whitespace only must send no PATCH.
func TestVolumeBackupPolicyResourcePaddedValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	policy := sampleVolumeBackupPolicy(t)
	policy.Description = core.NullableFromPtr(new("daily"))
	itemPath := backupPoliciesPath + backupPolicyTestID.String() + "/"
	var calls []string
	roundTrip := func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method)
		if strings.HasSuffix(req.URL.Path, "/regions/") {
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == backupPoliciesPath:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"name": "nightly", "description": "daily", "region_id": backupPolicyTestRegionID.String(), "status": "enabled",
				"schedule": map[string]any{"execution_strategy": "periodic", "first_execution": "2030-01-01T02:00:00Z", "frequency": float64(2), "frequency_unit": "day"},
			}
			if !sameJSONObject(body, want) {
				t.Errorf("create body = %v, want %v", body, want)
			}
			return testHTTPResponse(req, http.StatusCreated, volumeBackupPolicyJSON(t, policy)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	model := configuredVolumeBackupPolicy()
	model.Name, model.Description, model.Status = types.StringValue(" nightly "), types.StringValue("\tdaily "), types.StringValue(" enabled ")
	model.Region, model.Schedule = backupPolicyRegionReference(" vn-central-1 "), paddedPeriodicScheduleObject("day")
	model = unknownBackupPolicyComputed(model)
	model.Retention = types.Int64Unknown()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, model)}, &created)
	var createdState VolumeBackupPolicyResourceModel
	if diags := created.State.Get(ctx, &createdState); created.Diagnostics.HasError() || diags.HasError() ||
		createdState.Name != model.Name || createdState.Description != model.Description || createdState.Status != model.Status ||
		!createdState.Region.Equal(model.Region) || !createdState.Schedule.Equal(model.Schedule) {
		t.Fatalf("state after create must keep the configured spelling: %#v %v %v", createdState, created.Diagnostics, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("state after read must keep the configured spelling: %v", read.Diagnostics)
	}

	respelled := unknownBackupPolicyComputed(createdState)
	respelled.Name, respelled.Description, respelled.Status = types.StringValue("nightly "), types.StringValue("daily"), types.StringValue("enabled\n")
	respelled.Region, respelled.Schedule = backupPolicyRegionReference("vn-central-1"), periodicScheduleObject(2, " day", "2030-01-01T02:00:00Z ")
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, respelled), State: created.State}, &updated)
	var updatedState VolumeBackupPolicyResourceModel
	if diags := updated.State.Get(ctx, &updatedState); updated.Diagnostics.HasError() || diags.HasError() ||
		updatedState.Name != respelled.Name || updatedState.Description != respelled.Description || updatedState.Status != respelled.Status ||
		!updatedState.Region.Equal(respelled.Region) || !updatedState.Schedule.Equal(respelled.Schedule) {
		t.Fatalf("a whitespace-only edit must keep the new spelling: %#v %v %v", updatedState, updated.Diagnostics, diags)
	}
	if want := "GET,POST,GET,GET"; strings.Join(calls, ",") != want {
		t.Fatalf("requests = %v, want %s (no PATCH for a whitespace-only edit)", calls, want)
	}
}

func TestVolumeBackupPolicyResourceNotFoundErrorsAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	status := http.StatusNotFound
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, status, `{"detail":"failure"}`), nil
	}), projectID: volumeTestProjectID}
	model := configuredVolumeBackupPolicy()
	model.ID = types.StringValue(backupPolicyTestID.String())
	state := backupPolicyStateOf(ctx, t, s, unknownFree(model))

	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("read must remove a missing policy: %v", read.Diagnostics)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("delete of a missing policy must succeed: %v", deleted.Diagnostics)
	}

	status = http.StatusInternalServerError
	read = resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("read must report a server error")
	}
	deleted = resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() {
		t.Fatal("delete must report a server error")
	}

	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: " " + backupPolicyTestID.String() + "\n"}, &imported)
	var id types.String
	if diags := imported.State.GetAttribute(ctx, path.Root("id"), &id); imported.Diagnostics.HasError() || diags.HasError() || id.ValueString() != backupPolicyTestID.String() {
		t.Fatalf("import ID: %v %v %v", id, imported.Diagnostics, diags)
	}
}

func TestVolumeBackupPolicyResourceRejectsInvalidIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("an invalid ID must not reach the API: %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	}), projectID: volumeTestProjectID}
	model := configuredVolumeBackupPolicy()
	model.ID = types.StringValue("not-a-uuid")
	state := backupPolicyStateOf(ctx, t, s, unknownFree(model))

	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, unknownFree(model)), State: state}, &updated)
	deleted := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if !read.Diagnostics.HasError() || !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() {
		t.Fatalf("read, update, and delete must reject a malformed ID: %v %v %v", read.Diagnostics, updated.Diagnostics, deleted.Diagnostics)
	}
}

func TestVolumeBackupPolicyResourceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyResourceSchema(t)
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/regions/") {
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		}
		return testHTTPResponse(req, http.StatusBadRequest, `{"detail":"Initial first_execution must be in the future."}`), nil
	}
	r := &VolumeBackupPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, configuredVolumeBackupPolicy())}, &created)
	if !created.Diagnostics.HasError() || created.Diagnostics.Errors()[0].Summary() != "Error creating volume backup policy" || !created.State.Raw.IsNull() {
		t.Fatalf("create must report the API error and write no state: %v", created.Diagnostics)
	}

	model := configuredVolumeBackupPolicy()
	model.ID, model.Retention, model.Description = types.StringValue(backupPolicyTestID.String()), types.Int64Value(1), types.StringValue("")
	prior := backupPolicyStateOf(ctx, t, s, unknownFree(model))
	renamed := model
	renamed.Name = types.StringValue("renamed")
	updated := resource.UpdateResponse{State: prior}
	r.Update(ctx, resource.UpdateRequest{Plan: volumeBackupPolicyPlan(ctx, t, s, unknownFree(renamed)), State: prior}, &updated)
	if !updated.Diagnostics.HasError() || updated.Diagnostics.Errors()[0].Summary() != "Error updating volume backup policy" {
		t.Fatalf("update must report the API error: %v", updated.Diagnostics)
	}
}

// unknownBackupPolicyComputed marks the computed values that a plan with a
// change leaves unknown.
func unknownBackupPolicyComputed(m VolumeBackupPolicyResourceModel) VolumeBackupPolicyResourceModel {
	m.State, m.VolumeCount, m.NextExecution, m.LastExecution, m.UpdatedAt = types.StringUnknown(), types.Int64Unknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	if m.ID.IsNull() {
		m.ID, m.CreatedAt = types.StringUnknown(), types.StringUnknown()
	}
	return m
}

// unknownFree fills the computed values that a configured model leaves null,
// so that state and plan hold the values of a stored policy.
func unknownFree(m VolumeBackupPolicyResourceModel) VolumeBackupPolicyResourceModel {
	m.State, m.VolumeCount = types.StringValue("ready"), types.Int64Value(0)
	m.CreatedAt, m.UpdatedAt = types.StringValue("2026-01-01T00:00:00Z"), types.StringValue("2026-01-01T00:00:00Z")
	if m.Retention.IsNull() {
		m.Retention = types.Int64Value(1)
	}
	if m.Description.IsNull() {
		m.Description = types.StringValue("")
	}
	return m
}

func marshalBackupPolicyBody(t *testing.T, body any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func sameJSONObject(got, want any) bool {
	g, gerr := json.Marshal(got)
	w, werr := json.Marshal(want)
	return gerr == nil && werr == nil && string(g) == string(w)
}

func scheduleModelOf(t *testing.T, object types.Object) volumeBackupPolicyScheduleModel {
	t.Helper()
	model, ok := scheduleModelFromObject(object)
	if !ok {
		t.Fatalf("schedule object %v is not a known schedule", object)
	}
	return model
}
