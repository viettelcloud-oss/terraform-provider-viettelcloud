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
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
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

func volumeSnapshotPolicyResourceSchema(ctx context.Context, t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeSnapshotPolicyResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func snapshotPolicyRegion(name string) types.Object {
	return reference.Value(reference.Name, types.StringValue(name))
}

// configuredSnapshotPolicy returns a model with every required input set and
// every computed value null.
func configuredSnapshotPolicy() VolumeSnapshotPolicyResourceModel {
	return VolumeSnapshotPolicyResourceModel{
		VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{
			Name: types.StringValue("nightly"), Status: types.StringValue("enabled"), Description: types.StringNull(), Retention: types.Int64Null(),
			Schedule: snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00"),
		},
		Region: snapshotPolicyRegion("vn-central-1"),
	}
}

func snapshotPolicyPlan(ctx context.Context, t *testing.T, model VolumeSnapshotPolicyResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: volumeSnapshotPolicyResourceSchema(ctx, t)}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func snapshotPolicyState(ctx context.Context, t *testing.T, model VolumeSnapshotPolicyResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: volumeSnapshotPolicyResourceSchema(ctx, t)}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func staticSnapshotPolicyRegion(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
	return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: snapshotPolicyTestRegionID, Name: *filter.Name}}, nil
}

// snapshotPolicyBody decodes a request body into a generic map, so that a test
// can assert which fields the request holds.
func snapshotPolicyBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func TestVolumeSnapshotPolicyResourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, ok := NewVolumeSnapshotPolicyResource().(*VolumeSnapshotPolicyResource)
	if !ok {
		t.Fatal("constructor must return *VolumeSnapshotPolicyResource")
	}
	var metadata resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_volume_snapshot_policy" {
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
	blockStorage := volumeTestClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	project := volumeTestProjectClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	var configured resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerdata.Configured{BlockStorage: blockStorage, Project: project, ProjectID: volumeTestProjectID}}, &configured)
	if configured.Diagnostics.HasError() || r.client != blockStorage || r.project != project || r.projectID != volumeTestProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestVolumeSnapshotPolicyResourceSchema(t *testing.T) {
	ctx := context.Background()
	t.Parallel()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	snapshotPolicyPlan(ctx, t, VolumeSnapshotPolicyResourceModel{
		VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Schedule: types.ObjectNull(snapshotPolicyScheduleTypes)},
		Region:                    types.ObjectNull(reference.AttributeTypes(reference.Name)),
	})

	for _, name := range []string{"name", "status"} {
		if attribute := s.Attributes[name].(resourceschema.StringAttribute); !attribute.Required || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be required and update in place: %#v", name, attribute)
		}
	}
	if description := s.Attributes["description"].(resourceschema.StringAttribute); !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 {
		t.Errorf("description must be optional, computed, and keep its state: %#v", description)
	}
	if retention := s.Attributes["retention"].(resourceschema.Int64Attribute); !retention.Optional || !retention.Computed || len(retention.PlanModifiers) != 1 {
		t.Errorf("retention must be optional, computed, and keep its state: %#v", retention)
	}

	schedule := s.Attributes["schedule"].(resourceschema.SingleNestedAttribute)
	if !schedule.Required || len(schedule.PlanModifiers) != 0 || len(schedule.Attributes) != 2 {
		t.Fatalf("schedule must be a required object of two attributes that update in place: %#v", schedule)
	}
	if days := schedule.Attributes[snapshotPolicyDaysOfWeek].(resourceschema.SetAttribute); !days.Required || days.ElementType != types.StringType || len(days.PlanModifiers) != 0 {
		t.Errorf("schedule.days_of_week must be a required, in-place set of strings: %#v", days)
	}
	if clock := schedule.Attributes[snapshotPolicyTime].(resourceschema.StringAttribute); !clock.Required || len(clock.PlanModifiers) != 0 {
		t.Errorf("schedule.time must be required and update in place: %#v", clock)
	}

	region := s.Attributes["region"].(resourceschema.SingleNestedAttribute)
	if !region.Required || len(region.PlanModifiers) != 1 {
		t.Fatalf("region must be a required reference with one plan modifier: %#v", region)
	}
	if criterion, ok := region.Attributes[reference.Name].(resourceschema.StringAttribute); !ok || !criterion.Required || len(region.Attributes) != 1 {
		t.Fatalf("region must hold one required name: %#v", region.Attributes)
	}
	if !planReference(t, region.PlanModifiers[0], snapshotPolicyRegion("vn-central-1"), snapshotPolicyRegion("vn-north-1"), snapshotPolicyRegion("vn-north-1")).RequiresReplace {
		t.Error("changing region.name must replace the policy")
	}
	if planReference(t, region.PlanModifiers[0], snapshotPolicyRegion("vn-central-1"), snapshotPolicyRegion("vn-central-1"), snapshotPolicyRegion("vn-central-1")).RequiresReplace {
		t.Error("an unchanged region.name must not replace the policy")
	}
	if planReference(t, region.PlanModifiers[0], snapshotPolicyRegion("vn-central-1"), snapshotPolicyRegion(" vn-central-1\n"), snapshotPolicyRegion(" vn-central-1\n")).RequiresReplace {
		t.Error("a change of outer whitespace in region.name must not replace the policy")
	}
	if planReference(t, region.PlanModifiers[0], snapshotPolicyRegion(" vn-central-1 "), snapshotPolicyRegion(" vn-north-1 "), snapshotPolicyRegion(" vn-north-1 ")).RequiresReplace != true {
		t.Error("another trimmed region.name must replace the policy")
	}
	unknownRegion := reference.Value(reference.Name, types.StringUnknown())
	if !planReference(t, region.PlanModifiers[0], snapshotPolicyRegion("vn-central-1"), unknownRegion, unknownRegion).RequiresReplace {
		t.Error("an unknown region.name must replace the policy")
	}

	for name, keepsState := range map[string]bool{
		"id": true, "created_at": true, "state": false, "volume_count": false, "last_execution": false, "updated_at": false,
	} {
		var computed bool
		var modifiers int
		switch attribute := s.Attributes[name].(type) {
		case resourceschema.StringAttribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		case resourceschema.Int64Attribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		default:
			t.Fatalf("%s has unexpected type %T", name, attribute)
		}
		if !computed || (modifiers == 1) != keepsState {
			t.Errorf("%s: computed=%v plan modifiers=%d, want UseStateForUnknown=%v", name, computed, modifiers, keepsState)
		}
	}
}

// AC5: omitting description or retention after apply must plan the prior
// value, so the plan stays stable.
func TestVolumeSnapshotPolicyOptionalValuesKeepStateWhenOmitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})

	description := s.Attributes["description"].(resourceschema.StringAttribute).PlanModifiers[0]
	stringResponse := planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	description.PlanModifyString(ctx, planmodifier.StringRequest{
		State: tfsdk.State{Raw: existing}, Plan: tfsdk.Plan{Raw: existing}, Config: tfsdk.Config{Raw: existing},
		StateValue: types.StringValue("before"), PlanValue: types.StringUnknown(), ConfigValue: types.StringNull(),
	}, &stringResponse)
	if stringResponse.PlanValue.ValueString() != "before" {
		t.Fatalf("an omitted description must plan the prior value, got %v", stringResponse.PlanValue)
	}

	retention := s.Attributes["retention"].(resourceschema.Int64Attribute).PlanModifiers[0]
	int64Response := planmodifier.Int64Response{PlanValue: types.Int64Unknown()}
	retention.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State: tfsdk.State{Raw: existing}, Plan: tfsdk.Plan{Raw: existing}, Config: tfsdk.Config{Raw: existing},
		StateValue: types.Int64Value(5), PlanValue: types.Int64Unknown(), ConfigValue: types.Int64Null(),
	}, &int64Response)
	if int64Response.PlanValue.ValueInt64() != 5 {
		t.Fatalf("an omitted retention must plan the prior value, got %v", int64Response.PlanValue)
	}
}

func TestVolumeSnapshotPolicyResponseMapping(t *testing.T) {
	t.Parallel()
	policy := sampleSnapshotPolicy(t)
	var m VolumeSnapshotPolicyModel
	if diags := populateVolumeSnapshotPolicyModel(policy, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.ID.ValueString() != snapshotPolicyTestID.String() || m.Name.ValueString() != "nightly" || !m.Description.IsNull() ||
		m.Status.ValueString() != "enabled" || m.Retention.ValueInt64() != 1 || !m.Schedule.Equal(snapshotPolicyScheduleValue([]string{"fri", "mon"}, "02:00")) ||
		m.State.ValueString() != "ready" || m.VolumeCount.ValueInt64() != 2 || !m.LastExecution.IsNull() ||
		m.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || m.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("policy response mapping: %#v", m)
	}

	last := policy.UpdatedAt
	policy.Description = core.NullableFromPtr(new(""))
	policy.LastExecution = core.NullableFromPtr(&last)
	if diags := populateVolumeSnapshotPolicyModel(policy, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.Description.IsNull() || m.Description.ValueString() != "" || m.LastExecution.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("an empty description must be known and a last run must be mapped: %#v %#v", m.Description, m.LastExecution)
	}
}

// AC11: the schema cannot hold other schedule types, so the error must name the
// policy ID and the type.
func TestVolumeSnapshotPolicyRejectsUnsupportedSchedules(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, schedule, strategy string }{
		{"days of month", `{"execution_strategy":"days_of_month","days_of_month":[1,15],"time":"02:00:00"}`, "days_of_month"},
		{"periodic", `{"execution_strategy":"periodic","first_execution":"2026-01-01T00:00:00Z","frequency":1,"frequency_unit":"day"}`, "periodic"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := sampleSnapshotPolicyWithSchedule(t, tt.schedule)
			var state VolumeSnapshotPolicyResourceModel
			diags := populateVolumeSnapshotPolicyResourceState(policy, &state)
			if !diags.HasError() || diags.Errors()[0].Summary() != "Unsupported snapshot policy schedule" ||
				!strings.Contains(diags.Errors()[0].Detail(), snapshotPolicyTestID.String()) || !strings.Contains(diags.Errors()[0].Detail(), tt.strategy) {
				t.Fatalf("want an error naming the ID and %q, got %v", tt.strategy, diags)
			}
			if !state.ID.IsNull() {
				t.Fatalf("a failed mapping must not write state: %#v", state)
			}
		})
	}
}

func TestPopulateVolumeSnapshotPolicyResourceStatePreservesConfiguredValues(t *testing.T) {
	t.Parallel()
	policy := sampleSnapshotPolicy(t)
	for _, tt := range []struct {
		name       string
		prior      VolumeSnapshotPolicyResourceModel
		wantName   string
		wantTime   string
		wantRegion string
	}{
		{"import", VolumeSnapshotPolicyResourceModel{
			VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Schedule: types.ObjectNull(snapshotPolicyScheduleTypes)},
			Region:                    types.ObjectNull(reference.AttributeTypes(reference.Name)),
		}, "nightly", "02:00", "vn-central-1"},
		{"configured spelling", VolumeSnapshotPolicyResourceModel{
			VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Name: types.StringValue(" nightly "), Schedule: snapshotPolicyScheduleValue([]string{"fri", "mon"}, "02:00:00")},
			Region:                    snapshotPolicyRegion(" vn-central-1 "),
		}, " nightly ", "02:00:00", " vn-central-1 "},
		{"short time spelling", VolumeSnapshotPolicyResourceModel{
			VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Name: types.StringValue("nightly"), Schedule: snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00")},
			Region:                    snapshotPolicyRegion("vn-central-1"),
		}, "nightly", "02:00", "vn-central-1"},
		{"drift", VolumeSnapshotPolicyResourceModel{
			VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Name: types.StringValue("old"), Schedule: snapshotPolicyScheduleValue([]string{"mon"}, "03:00")},
			Region:                    snapshotPolicyRegion("vn-north-1"),
		}, "nightly", "02:00", "vn-central-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.prior
			if diags := populateVolumeSnapshotPolicyResourceState(policy, &state); diags.HasError() {
				t.Fatal(diags)
			}
			schedule, ok := decodeSnapshotPolicySchedule(state.Schedule)
			if !ok || state.Name.ValueString() != tt.wantName || schedule.Time.ValueString() != tt.wantTime ||
				!schedule.DaysOfWeek.Equal(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("mon"), types.StringValue("fri")})) ||
				!state.Region.Equal(snapshotPolicyRegion(tt.wantRegion)) {
				t.Fatalf("state: name=%v schedule=%v region=%v", state.Name, state.Schedule, state.Region)
			}
		})
	}
}

// The API trims description, so a configured value with spaces around the
// stored one must stay in state, or apply reports an inconsistent result.
func TestPopulateVolumeSnapshotPolicyResourceStatePreservesConfiguredDescription(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		reported *string
		prior    types.String
		want     types.String
	}{
		{"spaces around the stored value", new("weekday runs"), types.StringValue(" weekday runs\n"), types.StringValue(" weekday runs\n")},
		{"cleared with spaces", new(""), types.StringValue("  "), types.StringValue("  ")},
		{"drift", new("weekday runs"), types.StringValue("old"), types.StringValue("weekday runs")},
		{"unknown at create", new(""), types.StringUnknown(), types.StringValue("")},
		{"null in the response", nil, types.StringValue("old"), types.StringNull()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := sampleSnapshotPolicy(t)
			if tt.reported != nil {
				policy.Description.Set(*tt.reported)
			}
			state := VolumeSnapshotPolicyResourceModel{
				VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{Name: types.StringValue("nightly"), Description: tt.prior, Schedule: types.ObjectNull(snapshotPolicyScheduleTypes)},
				Region:                    snapshotPolicyRegion("vn-central-1"),
			}
			if diags := populateVolumeSnapshotPolicyResourceState(policy, &state); diags.HasError() {
				t.Fatal(diags)
			}
			if !state.Description.Equal(tt.want) {
				t.Fatalf("description = %v, want %v", state.Description, tt.want)
			}
		})
	}
}

// The API trims status and the schedule days, so a configured value with spaces
// around the stored one must stay in state, while real drift still shows.
func TestPopulateVolumeSnapshotPolicyResourceStatePreservesPaddedStatusAndSchedule(t *testing.T) {
	t.Parallel()
	paddedDays := []string{" mon", "fri\t", " mon "}
	for _, tt := range []struct {
		name       string
		status     string
		days       []string
		wantStatus string
		wantDays   []string
	}{
		{"spaces around the stored values", " enabled\n", paddedDays, " enabled\n", paddedDays},
		{"drift", " disabled ", []string{" sun"}, "enabled", []string{"mon", "fri"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := VolumeSnapshotPolicyResourceModel{
				VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{
					Name: types.StringValue("nightly"), Status: types.StringValue(tt.status), Schedule: snapshotPolicyScheduleValue(tt.days, " 02:00 "),
				},
				Region: snapshotPolicyRegion("vn-central-1"),
			}
			if diags := populateVolumeSnapshotPolicyResourceState(sampleSnapshotPolicy(t), &state); diags.HasError() {
				t.Fatal(diags)
			}
			if state.Status.ValueString() != tt.wantStatus || !state.Schedule.Equal(snapshotPolicyScheduleValue(tt.wantDays, " 02:00 ")) {
				t.Fatalf("state: status=%v schedule=%v", state.Status, state.Schedule)
			}
		})
	}
}

func TestValidateVolumeSnapshotPolicy(t *testing.T) {
	t.Parallel()
	valid := configuredSnapshotPolicy().VolumeSnapshotPolicyModel
	if diags := validateVolumeSnapshotPolicy(valid); diags.HasError() {
		t.Fatalf("valid policy: %v", diags)
	}
	withSeconds := valid
	withSeconds.Schedule = snapshotPolicyScheduleValue([]string{"sun"}, "23:59:59")
	if diags := validateVolumeSnapshotPolicy(withSeconds); diags.HasError() {
		t.Fatalf("a time with seconds must be valid: %v", diags)
	}
	padded := valid
	padded.Status = types.StringValue(" enabled\n")
	padded.Schedule = snapshotPolicyScheduleValue([]string{" mon", "fri\t"}, " 02:00 ")
	if diags := validateVolumeSnapshotPolicy(padded); diags.HasError() {
		t.Fatalf("outer whitespace around status, days, and time must be valid: %v", diags)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*VolumeSnapshotPolicyModel)
		want   string
	}{
		{"blank name", func(m *VolumeSnapshotPolicyModel) { m.Name = types.StringValue("  ") }, "name"},
		{"blank status", func(m *VolumeSnapshotPolicyModel) { m.Status = types.StringValue(" ") }, "status"},
		{"blank day", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"mon", " "}, "02:00")
		}, "schedule.days_of_week"},
		{"blank time", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue([]string{"mon"}, "  ") }, "schedule.time"},
		{"no days", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue(nil, "02:00") }, "schedule.days_of_week"},
		{"capitalized day", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue([]string{"Mon"}, "02:00") }, "schedule.days_of_week"},
		{"unknown day name", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"mon", "monday"}, "02:00")
		}, "schedule.days_of_week"},
		{"time without minutes", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue([]string{"mon"}, "2") }, "schedule.time"},
		{"hour out of range", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue([]string{"mon"}, "24:00") }, "schedule.time"},
		{"time with zone", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"mon"}, "02:00Z")
		}, "schedule.time"},
		{"invalid status", func(m *VolumeSnapshotPolicyModel) { m.Status = types.StringValue("paused") }, "status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := valid
			tt.mutate(&m)
			diags := validateVolumeSnapshotPolicy(m)
			if diags.ErrorsCount() != 1 {
				t.Fatalf("want one error, got %v", diags)
			}
			if got := diags.Errors()[0].(interface{ Path() path.Path }).Path().String(); got != tt.want {
				t.Fatalf("error path = %s, want %s", got, tt.want)
			}
		})
	}

	unknown := VolumeSnapshotPolicyModel{
		Name: types.StringUnknown(), Status: types.StringUnknown(), Retention: types.Int64Unknown(),
		Schedule: types.ObjectValueMust(snapshotPolicyScheduleTypes, map[string]attr.Value{
			snapshotPolicyDaysOfWeek: types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
			snapshotPolicyTime:       types.StringUnknown(),
		}),
	}
	if diags := validateVolumeSnapshotPolicy(unknown); diags.HasError() {
		t.Fatalf("unknown values must pass validation: %v", diags)
	}
	unknown.Schedule = types.ObjectUnknown(snapshotPolicyScheduleTypes)
	if diags := validateVolumeSnapshotPolicy(unknown); diags.HasError() {
		t.Fatalf("an unknown schedule must pass validation: %v", diags)
	}
}

// AC10: an invalid schedule must fail at plan, before any API request.
func TestVolumeSnapshotPolicyValidateConfigReportsInvalidSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	for _, tt := range []struct {
		name     string
		schedule types.Object
		summary  string
	}{
		{"empty days", snapshotPolicyScheduleValue(nil, "02:00"), "Invalid snapshot policy days"},
		{"invalid day", snapshotPolicyScheduleValue([]string{"funday"}, "02:00"), "Invalid snapshot policy day"},
		{"invalid time", snapshotPolicyScheduleValue([]string{"mon"}, "25:00"), "Invalid snapshot policy time"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := configuredSnapshotPolicy()
			model.Schedule = tt.schedule
			plan := snapshotPolicyPlan(ctx, t, model)
			var response resource.ValidateConfigResponse
			(&VolumeSnapshotPolicyResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: plan.Raw}}, &response)
			if response.Diagnostics.ErrorsCount() != 1 || response.Diagnostics.Errors()[0].Summary() != tt.summary {
				t.Fatalf("want one %q error, got %v", tt.summary, response.Diagnostics)
			}
		})
	}
}

func TestBuildVolumeSnapshotPolicyCreateBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plan := configuredSnapshotPolicy()
	plan.Name, plan.Region, plan.Status = types.StringValue(" nightly "), snapshotPolicyRegion(" vn-central-1 "), types.StringValue(" enabled\n")
	plan.Schedule = snapshotPolicyScheduleValue([]string{" sun", "mon", "fri\t", " mon "}, " 02:00 ")
	var resolved string
	body, diags := buildVolumeSnapshotPolicyCreateBody(ctx, plan, func(ctx context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		resolved = *filter.Name
		return staticSnapshotPolicyRegion(ctx, filter)
	})
	if diags.HasError() || resolved != "vn-central-1" || body.Name != "nightly" || body.RegionId != snapshotPolicyTestRegionID ||
		body.Status != blockstoragesdk.SnapshotPolicyStatusEnabled {
		t.Fatalf("create body: %#v, resolved %q, %v", body, resolved, diags)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"description", "retention"} {
		if strings.Contains(string(encoded), `"`+field+`"`) {
			t.Fatalf("an omitted %s must stay out of the request: %s", field, encoded)
		}
	}
	var sent struct {
		Schedule map[string]any `json:"schedule"`
	}
	if err := json.Unmarshal(encoded, &sent); err != nil {
		t.Fatal(err)
	}
	days, _ := sent.Schedule["days_of_week"].([]any)
	if len(sent.Schedule) != 3 || sent.Schedule["execution_strategy"] != "days_of_week" || sent.Schedule["time"] != "02:00" ||
		len(days) != 3 || days[0] != "mon" || days[1] != "fri" || days[2] != "sun" {
		t.Fatalf("schedule must carry trimmed, unique days_of_week in calendar order and the trimmed time: %v", sent.Schedule)
	}

	plan.Description, plan.Retention = types.StringUnknown(), types.Int64Unknown()
	body, diags = buildVolumeSnapshotPolicyCreateBody(ctx, plan, staticSnapshotPolicyRegion)
	if diags.HasError() || body.Description != nil || body.Retention != nil {
		t.Fatalf("unknown optional values must stay out of the request: %#v %v", body, diags)
	}
	plan.Description, plan.Retention = types.StringValue(""), types.Int64Value(7)
	body, diags = buildVolumeSnapshotPolicyCreateBody(ctx, plan, staticSnapshotPolicyRegion)
	if diags.HasError() || body.Description == nil || *body.Description != "" || body.Retention == nil || *body.Retention != 7 {
		t.Fatalf("configured description and retention: %#v %v", body, diags)
	}
	plan.Description = types.StringValue(" weekday runs\n")
	body, diags = buildVolumeSnapshotPolicyCreateBody(ctx, plan, staticSnapshotPolicyRegion)
	if diags.HasError() || body.Description == nil || *body.Description != "weekday runs" {
		t.Fatalf("description must be sent trimmed, as the API stores it: %#v %v", body, diags)
	}

	regionErr := errors.New("region list failed")
	_, diags = buildVolumeSnapshotPolicyCreateBody(ctx, plan, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		return projectsdk.ProjectRegionSchema{}, regionErr
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), regionErr.Error()) {
		t.Fatalf("region resolver error: %v", diags)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*VolumeSnapshotPolicyResourceModel)
	}{
		{"unknown region", func(m *VolumeSnapshotPolicyResourceModel) {
			m.Region = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
		}},
		{"blank region", func(m *VolumeSnapshotPolicyResourceModel) { m.Region = snapshotPolicyRegion(" ") }},
		{"unknown name", func(m *VolumeSnapshotPolicyResourceModel) { m.Name = types.StringUnknown() }},
		{"unknown status", func(m *VolumeSnapshotPolicyResourceModel) { m.Status = types.StringUnknown() }},
		{"unknown schedule", func(m *VolumeSnapshotPolicyResourceModel) {
			m.Schedule = types.ObjectUnknown(snapshotPolicyScheduleTypes)
		}},
		{"unknown time", func(m *VolumeSnapshotPolicyResourceModel) {
			m.Schedule = types.ObjectValueMust(snapshotPolicyScheduleTypes, map[string]attr.Value{
				snapshotPolicyDaysOfWeek: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("mon")}),
				snapshotPolicyTime:       types.StringUnknown(),
			})
		}},
		{"unknown day", func(m *VolumeSnapshotPolicyResourceModel) {
			m.Schedule = types.ObjectValueMust(snapshotPolicyScheduleTypes, map[string]attr.Value{
				snapshotPolicyDaysOfWeek: types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
				snapshotPolicyTime:       types.StringValue("02:00"),
			})
		}},
		{"invalid time", func(m *VolumeSnapshotPolicyResourceModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"mon"}, "2am")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := configuredSnapshotPolicy()
			tt.mutate(&m)
			if _, diags := buildVolumeSnapshotPolicyCreateBody(ctx, m, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
				t.Error("an invalid plan must not resolve the region")
				return projectsdk.ProjectRegionSchema{}, nil
			}); !diags.HasError() {
				t.Fatal("want a diagnostic")
			}
		})
	}
}

func TestBuildVolumeSnapshotPolicyUpdateBody(t *testing.T) {
	t.Parallel()
	state := configuredSnapshotPolicy().VolumeSnapshotPolicyModel
	state.Description, state.Retention = types.StringValue("before"), types.Int64Value(3)

	for _, tt := range []struct {
		name     string
		mutate   func(*VolumeSnapshotPolicyModel)
		wantKeys []string
	}{
		{"nothing changed", func(*VolumeSnapshotPolicyModel) {}, nil},
		{"computed values only", func(m *VolumeSnapshotPolicyModel) {
			m.UpdatedAt, m.State, m.VolumeCount = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
		}, nil},
		{"same time other spelling", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00:00")
		}, nil},
		{"unknown description is omitted", func(m *VolumeSnapshotPolicyModel) { m.Description = types.StringUnknown() }, nil},
		{"unknown retention is omitted", func(m *VolumeSnapshotPolicyModel) { m.Retention = types.Int64Unknown() }, nil},
		{"name with spaces around the same value", func(m *VolumeSnapshotPolicyModel) { m.Name = types.StringValue(" nightly ") }, nil},
		{"renamed", func(m *VolumeSnapshotPolicyModel) { m.Name = types.StringValue(" renamed ") }, []string{"name"}},
		{"status", func(m *VolumeSnapshotPolicyModel) { m.Status = types.StringValue("disabled") }, []string{"status"}},
		{"status with spaces around the same value", func(m *VolumeSnapshotPolicyModel) { m.Status = types.StringValue(" enabled\n") }, nil},
		{"changed status with spaces", func(m *VolumeSnapshotPolicyModel) { m.Status = types.StringValue(" disabled ") }, []string{"status"}},
		{"days and time with spaces around the same values", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{" fri", "mon\t", " mon "}, " 02:00:00\n")
		}, nil},
		{"changed days with spaces", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{" sun "}, " 02:00 ")
		}, []string{"schedule"}},
		{"retention", func(m *VolumeSnapshotPolicyModel) { m.Retention = types.Int64Value(9) }, []string{"retention"}},
		{"description", func(m *VolumeSnapshotPolicyModel) { m.Description = types.StringValue("after") }, []string{"description"}},
		{"cleared description", func(m *VolumeSnapshotPolicyModel) { m.Description = types.StringValue("") }, []string{"description"}},
		{"description with spaces around the same value", func(m *VolumeSnapshotPolicyModel) { m.Description = types.StringValue(" before\n") }, nil},
		{"changed description with spaces", func(m *VolumeSnapshotPolicyModel) { m.Description = types.StringValue(" after\n") }, []string{"description"}},
		{"days only", func(m *VolumeSnapshotPolicyModel) { m.Schedule = snapshotPolicyScheduleValue([]string{"sun"}, "02:00") }, []string{"schedule"}},
		{"time only", func(m *VolumeSnapshotPolicyModel) {
			m.Schedule = snapshotPolicyScheduleValue([]string{"fri", "mon"}, "23:30")
		}, []string{"schedule"}},
		{"everything", func(m *VolumeSnapshotPolicyModel) {
			m.Name, m.Status, m.Retention, m.Description = types.StringValue("renamed"), types.StringValue("disabled"), types.Int64Value(9), types.StringValue("after")
			m.Schedule = snapshotPolicyScheduleValue([]string{"sun"}, "23:30")
		}, []string{"description", "name", "retention", "schedule", "status"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := state
			tt.mutate(&plan)
			body, changed, diags := buildVolumeSnapshotPolicyUpdateBody(plan, state)
			if diags.HasError() {
				t.Fatalf("update body: %v", diags)
			}
			if changed != (len(tt.wantKeys) > 0) {
				t.Fatalf("changed = %v, want keys %v", changed, tt.wantKeys)
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var sent map[string]any
			if err := json.Unmarshal(encoded, &sent); err != nil {
				t.Fatal(err)
			}
			if len(sent) != len(tt.wantKeys) {
				t.Fatalf("body = %s, want only %v", encoded, tt.wantKeys)
			}
			for _, key := range tt.wantKeys {
				if _, ok := sent[key]; !ok {
					t.Fatalf("body = %s, want %q", encoded, key)
				}
			}
			if schedule, ok := sent["schedule"].(map[string]any); ok && (len(schedule) != 3 || schedule["execution_strategy"] != "days_of_week") {
				t.Fatalf("a schedule change must send the whole days_of_week schedule: %v", schedule)
			}
			if status, ok := sent["status"]; ok && status != strings.TrimSpace(plan.Status.ValueString()) {
				t.Fatalf("status = %v, want %q trimmed", status, plan.Status.ValueString())
			}
			if schedule, ok := sent["schedule"].(map[string]any); ok {
				days, _ := schedule["days_of_week"].([]any)
				for _, value := range append(days, schedule["time"]) {
					if text, _ := value.(string); text == "" || text != strings.TrimSpace(text) {
						t.Fatalf("a schedule change must send trimmed days and time: %s", encoded)
					}
				}
			}
			if description, ok := sent["description"]; ok && description != strings.TrimSpace(plan.Description.ValueString()) {
				t.Fatalf("description = %v, want %q trimmed", description, plan.Description.ValueString())
			}
		})
	}

	unknownStatus := state
	unknownStatus.Status = types.StringUnknown()
	if _, _, diags := buildVolumeSnapshotPolicyUpdateBody(unknownStatus, state); !diags.HasError() {
		t.Fatal("an unknown status must fail")
	}
	unknownSchedule := state
	unknownSchedule.Schedule = types.ObjectUnknown(snapshotPolicyScheduleTypes)
	if _, _, diags := buildVolumeSnapshotPolicyUpdateBody(unknownSchedule, state); !diags.HasError() {
		t.Fatal("an unknown schedule must fail")
	}
}

// AC1, AC3, and AC9: the API rejects an empty PATCH body, so an update must send
// only the changed fields, and must send no PATCH when nothing changed.
func TestVolumeSnapshotPolicyResourceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	policy := sampleSnapshotPolicy(t)
	itemPath := snapshotPoliciesPath + snapshotPolicyTestID.String() + "/"
	var calls []string
	var patchBody map[string]any
	roundTrip := func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		if req.URL.Path == snapshotPolicyRegionsPath() {
			if req.URL.Query().Get("name") != "vn-central-1" {
				t.Errorf("region lookup must filter by the trimmed name: %s", req.URL.RawQuery)
			}
			return snapshotPolicyRegionsPage(t, req), nil
		}
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing Project-ID header on %s %s", req.Method, req.URL)
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == snapshotPoliciesPath:
			body := snapshotPolicyBody(t, req)
			schedule, _ := body["schedule"].(map[string]any)
			if len(body) != 4 || body["name"] != "nightly" || body["region_id"] != snapshotPolicyTestRegionID.String() || body["status"] != "enabled" ||
				schedule["execution_strategy"] != "days_of_week" {
				t.Errorf("create body must hold only name, region_id, status, and schedule: %v", body)
			}
			return testHTTPResponse(req, http.StatusCreated, snapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodPatch && req.URL.Path == itemPath:
			patchBody = snapshotPolicyBody(t, req)
			policy.Name, policy.Status, policy.Description = "renamed", blockstoragesdk.SnapshotPolicyStatusDisabled, core.NullableFromPtr(new(""))
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodDelete && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusNoContent, ""), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	r := &VolumeSnapshotPolicyResource{
		client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID,
	}

	model := configuredSnapshotPolicy()
	model.Description, model.Retention = types.StringUnknown(), types.Int64Unknown()
	model.ID, model.State, model.VolumeCount, model.LastExecution = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown(), types.StringUnknown()
	model.CreatedAt, model.UpdatedAt = types.StringUnknown(), types.StringUnknown()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: snapshotPolicyPlan(ctx, t, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdState VolumeSnapshotPolicyResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.ID.ValueString() != snapshotPolicyTestID.String() ||
		createdState.Retention.ValueInt64() != 1 || !createdState.Description.IsNull() || createdState.State.ValueString() != "ready" ||
		createdState.VolumeCount.ValueInt64() != 2 || !createdState.LastExecution.IsNull() ||
		!createdState.Schedule.Equal(snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00")) {
		t.Fatalf("created state: %#v %v", createdState, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("refresh must keep a stable state: %v", read.Diagnostics)
	}

	updatedModel := createdState
	updatedModel.Name, updatedModel.Status, updatedModel.Description = types.StringValue("renamed"), types.StringValue("disabled"), types.StringValue("")
	updatedModel.UpdatedAt = types.StringUnknown()
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: snapshotPolicyPlan(ctx, t, updatedModel), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	if len(patchBody) != 3 || patchBody["name"] != "renamed" || patchBody["status"] != "disabled" || patchBody["description"] != "" {
		t.Errorf("PATCH body must hold only the changed fields: %v", patchBody)
	}
	var updatedState VolumeSnapshotPolicyResourceModel
	if diags := updated.State.Get(ctx, &updatedState); diags.HasError() || updatedState.ID.ValueString() != snapshotPolicyTestID.String() ||
		updatedState.Name.ValueString() != "renamed" || updatedState.Status.ValueString() != "disabled" || updatedState.Description.ValueString() != "" ||
		updatedState.Retention.ValueInt64() != 1 {
		t.Fatalf("updated state: %#v %v", updatedState, diags)
	}

	// A plan that changes only the spelling of time reads the policy instead of
	// sending an empty PATCH body, and keeps the configured spelling.
	respelled := updatedState
	respelled.Schedule = snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00:00")
	respelled.UpdatedAt = types.StringUnknown()
	patchBody = nil
	again := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: snapshotPolicyPlan(ctx, t, respelled), State: updated.State}, &again)
	var againState VolumeSnapshotPolicyResourceModel
	if diags := again.State.Get(ctx, &againState); again.Diagnostics.HasError() || diags.HasError() || patchBody != nil ||
		!againState.Schedule.Equal(snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00:00")) {
		t.Fatalf("a respelled time must not send a PATCH: body=%v state=%v %v %v", patchBody, againState.Schedule, again.Diagnostics, diags)
	}

	removed := resource.DeleteResponse{State: again.State}
	r.Delete(ctx, resource.DeleteRequest{State: again.State}, &removed)
	if removed.Diagnostics.HasError() {
		t.Fatalf("delete: %v", removed.Diagnostics)
	}
	want := []string{
		"GET " + snapshotPolicyRegionsPath(), "POST " + snapshotPoliciesPath, "GET " + itemPath, "PATCH " + itemPath, "GET " + itemPath, "DELETE " + itemPath,
	}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", calls, want)
	}
}

// The API trims request strings, so every padded input must reach the API
// trimmed. State after create and after the next read must keep the configured
// spelling, or Terraform reports an inconsistent result.
func TestVolumeSnapshotPolicyResourcePaddedInputsKeepConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	policy := sampleSnapshotPolicy(t)
	policy.Description.Set("weekday runs")
	var createBody map[string]any
	roundTrip := func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == snapshotPolicyRegionsPath():
			if req.URL.Query().Get("name") != "vn-central-1" {
				t.Errorf("region lookup must filter by the trimmed name: %s", req.URL.RawQuery)
			}
			return snapshotPolicyRegionsPage(t, req), nil
		case req.Method == http.MethodPost:
			createBody = snapshotPolicyBody(t, req)
			return testHTTPResponse(req, http.StatusCreated, snapshotPolicyJSON(t, policy)), nil
		default:
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
		}
	}
	r := &VolumeSnapshotPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	model := configuredSnapshotPolicy()
	model.Name, model.Status, model.Description = types.StringValue(" nightly "), types.StringValue(" enabled\n"), types.StringValue(" weekday runs\n")
	model.Region = snapshotPolicyRegion(" vn-central-1 ")
	model.Schedule = snapshotPolicyScheduleValue([]string{" mon", "fri\t"}, " 02:00 ")
	model.ID, model.Retention, model.State, model.VolumeCount = types.StringUnknown(), types.Int64Unknown(), types.StringUnknown(), types.Int64Unknown()
	model.LastExecution, model.CreatedAt, model.UpdatedAt = types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: snapshotPolicyPlan(ctx, t, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	schedule, _ := createBody["schedule"].(map[string]any)
	days, _ := schedule["days_of_week"].([]any)
	if createBody["name"] != "nightly" || createBody["status"] != "enabled" || createBody["description"] != "weekday runs" ||
		schedule["time"] != "02:00" || len(days) != 2 || days[0] != "mon" || days[1] != "fri" {
		t.Fatalf("create body must carry trimmed values: %v", createBody)
	}
	var createdState VolumeSnapshotPolicyResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.Name != model.Name || createdState.Status != model.Status ||
		createdState.Description != model.Description || !createdState.Region.Equal(model.Region) || !createdState.Schedule.Equal(model.Schedule) {
		t.Fatalf("created state must keep the configured spelling: %#v %v", createdState, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("refresh must keep the configured spelling: %v", read.Diagnostics)
	}
}

// A create response that the schema cannot hold must not reach state.
func TestVolumeSnapshotPolicyResourceCreateReportsUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleSnapshotPolicyWithSchedule(t, `{"execution_strategy":"days_of_month","days_of_month":[1],"time":"02:00:00"}`)
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotPolicyRegionsPath() {
			return snapshotPolicyRegionsPage(t, req), nil
		}
		return testHTTPResponse(req, http.StatusCreated, snapshotPolicyJSON(t, policy)), nil
	}
	r := &VolumeSnapshotPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	created := resource.CreateResponse{State: tfsdk.State{Schema: volumeSnapshotPolicyResourceSchema(ctx, t)}}
	r.Create(ctx, resource.CreateRequest{Plan: snapshotPolicyPlan(ctx, t, configuredSnapshotPolicy())}, &created)
	if !created.Diagnostics.HasError() || created.Diagnostics.Errors()[0].Summary() != "Unsupported snapshot policy schedule" || !created.State.Raw.IsNull() {
		t.Fatalf("create must report the schedule and write no state: %v", created.Diagnostics)
	}
}

// AC11, import and refresh half: the schema has no place for other schedule
// types.
func TestVolumeSnapshotPolicyResourceReadReportsUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleSnapshotPolicyWithSchedule(t, `{"execution_strategy":"periodic","first_execution":"2026-01-01T00:00:00Z","frequency":1,"frequency_unit":"day"}`)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
	})
	r := &VolumeSnapshotPolicyResource{client: client, projectID: volumeTestProjectID}
	model := configuredSnapshotPolicy()
	model.ID = types.StringValue(snapshotPolicyTestID.String())
	state := snapshotPolicyState(ctx, t, model)
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() || read.Diagnostics.Errors()[0].Summary() != "Unsupported snapshot policy schedule" ||
		!strings.Contains(read.Diagnostics.Errors()[0].Detail(), snapshotPolicyTestID.String()) || !strings.Contains(read.Diagnostics.Errors()[0].Detail(), "periodic") {
		t.Fatalf("read must name the ID and the schedule type: %v", read.Diagnostics)
	}
	if !read.State.Raw.Equal(state.Raw) {
		t.Fatal("a failed read must not change state")
	}
}

// AC7: a policy deleted outside Terraform must leave state, and a delete of a
// missing policy must succeed. AC8 needs the import ID to land in id.
func TestVolumeSnapshotPolicyResourceNotFoundErrorsAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	status := http.StatusNotFound
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, status, `{"errors":[{"code":"not_found","detail":"missing"}]}`), nil
	})
	r := &VolumeSnapshotPolicyResource{client: client, projectID: volumeTestProjectID}
	model := configuredSnapshotPolicy()
	model.ID = types.StringValue(snapshotPolicyTestID.String())
	state := snapshotPolicyState(ctx, t, model)

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
	r.ImportState(ctx, resource.ImportStateRequest{ID: snapshotPolicyTestID.String()}, &imported)
	var id types.String
	if diags := imported.State.GetAttribute(ctx, path.Root("id"), &id); imported.Diagnostics.HasError() || diags.HasError() || id.ValueString() != snapshotPolicyTestID.String() {
		t.Fatalf("import ID: %v %v %v", id, imported.Diagnostics, diags)
	}
}

// An import refresh must produce a state that a configuration of only the
// required attributes matches. The import ID has spaces around it, because the
// provider trims it before the read parses it.
func TestVolumeSnapshotPolicyResourceImportRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	policy := sampleSnapshotPolicy(t)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
	})
	r := &VolumeSnapshotPolicyResource{client: client, projectID: volumeTestProjectID}
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "  " + snapshotPolicyTestID.String() + "\n"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	var state VolumeSnapshotPolicyResourceModel
	if diags := read.State.Get(ctx, &state); read.Diagnostics.HasError() || diags.HasError() {
		t.Fatalf("refresh after import: %v %v", read.Diagnostics, diags)
	}
	config := configuredSnapshotPolicy()
	if state.ID.ValueString() != snapshotPolicyTestID.String() || state.Name != config.Name || state.Status != config.Status || !state.Region.Equal(config.Region) ||
		!state.Schedule.Equal(config.Schedule) || state.Retention.ValueInt64() != 1 || !state.Description.IsNull() {
		t.Fatalf("an imported state must match the configuration of the required attributes: %#v", state)
	}
}

func TestVolumeSnapshotPolicyResourceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeSnapshotPolicyResourceSchema(ctx, t)
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotPolicyRegionsPath() {
			return snapshotPolicyRegionsPage(t, req), nil
		}
		return testHTTPResponse(req, http.StatusBadRequest, `{"errors":[{"code":"invalid","detail":"bad request"}]}`), nil
	}
	r := &VolumeSnapshotPolicyResource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: snapshotPolicyPlan(ctx, t, configuredSnapshotPolicy())}, &created)
	if !created.Diagnostics.HasError() || created.Diagnostics.Errors()[0].Summary() != "Error creating volume snapshot policy" || !created.State.Raw.IsNull() {
		t.Fatalf("create must report the API error and write no state: %v", created.Diagnostics)
	}

	model := configuredSnapshotPolicy()
	model.ID, model.Retention, model.Description = types.StringValue(snapshotPolicyTestID.String()), types.Int64Value(1), types.StringValue("")
	prior := snapshotPolicyState(ctx, t, model)
	plan := model
	plan.Status = types.StringValue("disabled")
	updated := resource.UpdateResponse{State: prior}
	r.Update(ctx, resource.UpdateRequest{Plan: snapshotPolicyPlan(ctx, t, plan), State: prior}, &updated)
	if !updated.Diagnostics.HasError() || updated.Diagnostics.Errors()[0].Summary() != "Error updating volume snapshot policy" {
		t.Fatalf("update must report the API error: %v", updated.Diagnostics)
	}
}
