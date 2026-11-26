package filestorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
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
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

const policiesPath = "/v2/file-storage/snapshot-policies/"

func filesystemSnapshotPolicyResourceSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&FilesystemSnapshotPolicyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func regionReference(name string) types.Object {
	return reference.Value(reference.Name, types.StringValue(name))
}

func dayList(days ...string) types.List {
	values := make([]attr.Value, 0, len(days))
	for _, day := range days {
		values = append(values, types.StringValue(day))
	}
	return types.ListValueMust(types.StringType, values)
}

// configuredFilesystemSnapshotPolicy returns a model with every required input
// set and every computed value null.
func configuredFilesystemSnapshotPolicy() FilesystemSnapshotPolicyResourceModel {
	return FilesystemSnapshotPolicyResourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{
			Name: types.StringValue("nightly"), DaysOfWeek: dayList("mon", "fri"), Time: types.StringValue("02:00"),
			Status: types.StringValue("enabled"), Description: types.StringNull(), Retention: types.Int64Null(),
		},
		Region: regionReference("vn-central-1"),
	}
}

func filesystemSnapshotPolicyPlan(ctx context.Context, t *testing.T, s resourceschema.Schema, model FilesystemSnapshotPolicyResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func staticRegion(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
	return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: testRegionID, Name: *filter.Name}}, nil
}

func TestFilesystemSnapshotPolicyResourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, ok := NewFilesystemSnapshotPolicyResource().(*FilesystemSnapshotPolicyResource)
	if !ok {
		t.Fatal("constructor must return *FilesystemSnapshotPolicyResource")
	}
	var metadata resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_fs_filesystem_snapshot_policy" {
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
	fileStorage := testClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	project, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	var configured resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: &providerdata.Configured{FileStorage: fileStorage, Project: project, ProjectID: testProjectID}}, &configured)
	if configured.Diagnostics.HasError() || r.client != fileStorage || r.project != project || r.projectID != testProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestFilesystemSnapshotPolicyResourceSchema(t *testing.T) {
	t.Parallel()
	s := filesystemSnapshotPolicyResourceSchema(t)
	filesystemSnapshotPolicyPlan(context.Background(), t, s, FilesystemSnapshotPolicyResourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{DaysOfWeek: types.ListNull(types.StringType)},
		Region:                        types.ObjectNull(reference.AttributeTypes(reference.Name)),
	})

	for _, name := range []string{"name", "time", "status"} {
		if attribute := s.Attributes[name].(resourceschema.StringAttribute); !attribute.Required || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be required and update in place: %#v", name, attribute)
		}
	}
	if days := s.Attributes["days_of_week"].(resourceschema.ListAttribute); !days.Required || days.ElementType != types.StringType || len(days.PlanModifiers) != 0 {
		t.Errorf("days_of_week must be a required, in-place list of strings: %#v", days)
	}
	if description := s.Attributes["description"].(resourceschema.StringAttribute); !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 {
		t.Errorf("description must be optional, computed, and keep its state: %#v", description)
	}
	if retention := s.Attributes["retention"].(resourceschema.Int64Attribute); !retention.Optional || !retention.Computed || len(retention.PlanModifiers) != 1 {
		t.Errorf("retention must be optional, computed, and keep its state: %#v", retention)
	}
	region := s.Attributes["region"].(resourceschema.SingleNestedAttribute)
	if !region.Required || len(region.PlanModifiers) != 1 {
		t.Fatalf("region must be a required reference with one plan modifier: %#v", region)
	}
	if criterion, ok := region.Attributes[reference.Name].(resourceschema.StringAttribute); !ok || !criterion.Required || len(region.Attributes) != 1 {
		t.Fatalf("region must hold one required name: %#v", region.Attributes)
	}
	if !planFilesystemReference(t, region.PlanModifiers[0], regionReference("vn-central-1"), regionReference("vn-north-1")) {
		t.Error("changing region.name must replace the policy")
	}
	if planFilesystemReference(t, region.PlanModifiers[0], regionReference("vn-central-1"), regionReference("vn-central-1")) {
		t.Error("an unchanged region.name must not replace the policy")
	}
	if planFilesystemReference(t, region.PlanModifiers[0], regionReference("vn-central-1"), regionReference(" vn-central-1 ")) {
		t.Error("outer whitespace in region.name must not replace the policy")
	}

	for name, keepsState := range map[string]bool{
		"id": true, "region_id": true, "region_name": true, "project_id": true, "owner_id": true, "created_at": true,
		"filesystem_count": false, "state": false, "updated_at": false,
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

// TestFilesystemSnapshotPolicyOptionalValuesKeepStateWhenOmitted runs the
// plan modifiers of description and retention. Omitting either one after
// apply plans the prior value, so the PUT carries it and the plan is stable.
func TestFilesystemSnapshotPolicyOptionalValuesKeepStateWhenOmitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	req := func() (tfsdk.State, tfsdk.Plan, tfsdk.Config) {
		return tfsdk.State{Raw: existing}, tfsdk.Plan{Raw: existing}, tfsdk.Config{Raw: existing}
	}

	description := s.Attributes["description"].(resourceschema.StringAttribute).PlanModifiers[0]
	state, plan, config := req()
	stringResponse := planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	description.PlanModifyString(ctx, planmodifier.StringRequest{
		State: state, Plan: plan, Config: config,
		StateValue: types.StringValue("before"), PlanValue: types.StringUnknown(), ConfigValue: types.StringNull(),
	}, &stringResponse)
	if stringResponse.PlanValue.ValueString() != "before" {
		t.Fatalf("an omitted description must plan the prior value, got %v", stringResponse.PlanValue)
	}

	retention := s.Attributes["retention"].(resourceschema.Int64Attribute).PlanModifiers[0]
	state, plan, config = req()
	int64Response := planmodifier.Int64Response{PlanValue: types.Int64Unknown()}
	retention.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State: state, Plan: plan, Config: config,
		StateValue: types.Int64Value(5), PlanValue: types.Int64Unknown(), ConfigValue: types.Int64Null(),
	}, &int64Response)
	if int64Response.PlanValue.ValueInt64() != 5 {
		t.Fatalf("an omitted retention must plan the prior value, got %v", int64Response.PlanValue)
	}
}

func TestFilesystemSnapshotPolicyResponseMapping(t *testing.T) {
	t.Parallel()
	policy := sampleFilesystemSnapshotPolicy()
	var m FilesystemSnapshotPolicyModel
	populateFilesystemSnapshotPolicyModel(policy, &m)
	if m.ID.ValueString() != testPolicyID.String() || m.Name.ValueString() != "nightly" || !m.Description.IsNull() ||
		!m.DaysOfWeek.Equal(dayList("mon", "fri")) || m.Time.ValueString() != "02:00" || m.Retention.ValueInt64() != 1 ||
		m.Status.ValueString() != "enabled" || m.FilesystemCount.ValueInt64() != 2 || m.State.ValueString() != "ready" ||
		m.RegionID.ValueString() != testRegionID.String() || m.RegionName.ValueString() != "vn-central-1" ||
		m.ProjectID.ValueString() != testProjectID.String() || m.OwnerID.ValueString() != (core.UUID{5}).String() ||
		m.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || m.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("policy response mapping: %#v", m)
	}
	policy.Description = core.NullableFromPtr(new(""))
	policy.OwnerId = core.NullableFromPtr[core.UUID](nil)
	populateFilesystemSnapshotPolicyModel(policy, &m)
	if m.Description.IsNull() || m.Description.ValueString() != "" || !m.OwnerID.IsNull() {
		t.Fatalf("an empty description must be known and a missing owner null: %#v %#v", m.Description, m.OwnerID)
	}
}

func TestPopulateFilesystemSnapshotPolicyResourceStatePreservesConfiguredValues(t *testing.T) {
	t.Parallel()
	policy := sampleFilesystemSnapshotPolicy()
	for _, tt := range []struct {
		name                           string
		prior                          FilesystemSnapshotPolicyResourceModel
		wantName, wantTime, wantStatus string
		wantDays                       types.List
		wantRegion                     string
	}{
		{"import", FilesystemSnapshotPolicyResourceModel{
			FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{DaysOfWeek: types.ListNull(types.StringType)},
			Region:                        types.ObjectNull(reference.AttributeTypes(reference.Name)),
		}, "nightly", "02:00", "enabled", dayList("mon", "fri"), "vn-central-1"},
		{"configured spelling", FilesystemSnapshotPolicyResourceModel{
			FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{
				Name: types.StringValue(" nightly "), Time: types.StringValue(" 02:00 "), Status: types.StringValue(" enabled "), DaysOfWeek: dayList("fri", " mon "),
			},
			Region: regionReference(" vn-central-1 "),
		}, " nightly ", " 02:00 ", " enabled ", dayList("fri", " mon "), " vn-central-1 "},
		{"backend drift", FilesystemSnapshotPolicyResourceModel{
			FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{
				Name: types.StringValue("old"), Time: types.StringValue("03:00"), Status: types.StringValue("disabled"), DaysOfWeek: dayList("mon"),
			},
			Region: regionReference("vn-north-1"),
		}, "nightly", "02:00", "enabled", dayList("mon", "fri"), "vn-central-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.prior
			populateFilesystemSnapshotPolicyResourceState(policy, &state)
			if state.Name.ValueString() != tt.wantName || state.Time.ValueString() != tt.wantTime || state.Status.ValueString() != tt.wantStatus ||
				!state.DaysOfWeek.Equal(tt.wantDays) || !state.Region.Equal(regionReference(tt.wantRegion)) {
				t.Fatalf("state: name=%v time=%v status=%v days=%v region=%v", state.Name, state.Time, state.Status, state.DaysOfWeek, state.Region)
			}
		})
	}
}

func TestValidateFilesystemSnapshotPolicy(t *testing.T) {
	t.Parallel()
	valid := configuredFilesystemSnapshotPolicy().FilesystemSnapshotPolicyModel
	if diags := validateFilesystemSnapshotPolicy(valid); diags.HasError() {
		t.Fatalf("valid policy: %v", diags)
	}
	padded := valid
	padded.DaysOfWeek, padded.Time, padded.Status = dayList(" mon ", "fri "), types.StringValue(" 02:00 "), types.StringValue(" enabled ")
	if diags := validateFilesystemSnapshotPolicy(padded); diags.HasError() {
		t.Fatalf("outer whitespace must not be rejected: %v", diags)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*FilesystemSnapshotPolicyModel)
		want   string
	}{
		{"blank name", func(m *FilesystemSnapshotPolicyModel) { m.Name = types.StringValue("  ") }, "name"},
		{"no days", func(m *FilesystemSnapshotPolicyModel) { m.DaysOfWeek = dayList() }, "days_of_week"},
		{"capitalized day", func(m *FilesystemSnapshotPolicyModel) { m.DaysOfWeek = dayList("Mon") }, "days_of_week[0]"},
		{"unknown day name", func(m *FilesystemSnapshotPolicyModel) { m.DaysOfWeek = dayList("mon", "monday") }, "days_of_week[1]"},
		{"time without minutes", func(m *FilesystemSnapshotPolicyModel) { m.Time = types.StringValue("2") }, "time"},
		{"time with seconds", func(m *FilesystemSnapshotPolicyModel) { m.Time = types.StringValue("02:00:00") }, "time"},
		{"hour out of range", func(m *FilesystemSnapshotPolicyModel) { m.Time = types.StringValue("24:00") }, "time"},
		{"time with zone", func(m *FilesystemSnapshotPolicyModel) { m.Time = types.StringValue("02:00Z") }, "time"},
		{"zero retention", func(m *FilesystemSnapshotPolicyModel) { m.Retention = types.Int64Value(0) }, "retention"},
		{"invalid status", func(m *FilesystemSnapshotPolicyModel) { m.Status = types.StringValue("paused") }, "status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := valid
			tt.mutate(&m)
			diags := validateFilesystemSnapshotPolicy(m)
			if diags.ErrorsCount() != 1 {
				t.Fatalf("want one error, got %v", diags)
			}
			if got := diags.Errors()[0].(interface{ Path() path.Path }).Path().String(); got != tt.want {
				t.Fatalf("error path = %s, want %s", got, tt.want)
			}
		})
	}
	unknown := FilesystemSnapshotPolicyModel{
		Name: types.StringUnknown(), DaysOfWeek: types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
		Time: types.StringUnknown(), Retention: types.Int64Unknown(), Status: types.StringUnknown(),
	}
	if diags := validateFilesystemSnapshotPolicy(unknown); diags.HasError() {
		t.Fatalf("unknown values must pass validation: %v", diags)
	}
}

func TestFilesystemSnapshotPolicyValidateConfigReportsInvalidValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	model := configuredFilesystemSnapshotPolicy()
	model.Status = types.StringValue("Enabled")
	plan := filesystemSnapshotPolicyPlan(ctx, t, s, model)
	var response resource.ValidateConfigResponse
	(&FilesystemSnapshotPolicyResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: plan.Raw}}, &response)
	if response.Diagnostics.ErrorsCount() != 1 || response.Diagnostics.Errors()[0].Summary() != "Invalid snapshot policy status" {
		t.Fatalf("want one status error, got %v", response.Diagnostics)
	}
}

func TestBuildSnapshotPolicyCreateBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plan := configuredFilesystemSnapshotPolicy()
	plan.Name, plan.Region = types.StringValue(" nightly "), regionReference(" vn-central-1 ")
	plan.DaysOfWeek, plan.Time, plan.Status = dayList(" mon", "fri "), types.StringValue(" 02:00 "), types.StringValue(" enabled ")
	var resolved string
	body, diags := buildSnapshotPolicyCreateBody(ctx, plan, func(ctx context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		resolved = *filter.Name
		return staticRegion(ctx, filter)
	})
	if diags.HasError() || resolved != "vn-central-1" || body.Name != "nightly" || body.RegionId != testRegionID || body.Time != "02:00" ||
		body.Status != filestoragesdk.SnapshotPolicyStatusEnabled || len(body.DaysOfWeek) != 2 ||
		body.DaysOfWeek[0] != filestoragesdk.SnapshotDayOfWeekMon || body.DaysOfWeek[1] != filestoragesdk.SnapshotDayOfWeekFri {
		t.Fatalf("create body: %#v, resolved %q, %v", body, resolved, diags)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "description") {
		t.Fatalf("an omitted description must stay out of the request: %s", encoded)
	}
	if retention := core.NullableToPtr(body.Retention); retention == nil || *retention != 1 || !strings.Contains(string(encoded), `"retention":1`) {
		t.Fatalf("an omitted retention must be sent as 1 because the backend requires it: %s", encoded)
	}
	plan.Retention = types.Int64Unknown()
	body, diags = buildSnapshotPolicyCreateBody(ctx, plan, staticRegion)
	if retention := core.NullableToPtr(body.Retention); diags.HasError() || retention == nil || *retention != 1 {
		t.Fatalf("an unknown retention must be sent as 1: %#v %v", body, diags)
	}
	plan.Retention = types.Int64Null()

	plan.Description, plan.Retention = types.StringValue(""), types.Int64Value(7)
	body, diags = buildSnapshotPolicyCreateBody(ctx, plan, staticRegion)
	if retention := core.NullableToPtr(body.Retention); diags.HasError() || body.Description == nil || *body.Description != "" || retention == nil || *retention != 7 {
		t.Fatalf("configured description and retention: %#v %v", body, diags)
	}
	plan.Description = types.StringValue(" saved ")
	body, diags = buildSnapshotPolicyCreateBody(ctx, plan, staticRegion)
	if diags.HasError() || body.Description == nil || *body.Description != "saved" {
		t.Fatalf("a padded description must be sent trimmed: %#v %v", body, diags)
	}

	backendErr := errors.New("region list failed")
	_, diags = buildSnapshotPolicyCreateBody(ctx, plan, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		return projectsdk.ProjectRegionSchema{}, backendErr
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), backendErr.Error()) {
		t.Fatalf("region resolver error: %v", diags)
	}

	for _, tt := range []struct {
		name   string
		mutate func(*FilesystemSnapshotPolicyResourceModel)
	}{
		{"unknown region", func(m *FilesystemSnapshotPolicyResourceModel) {
			m.Region = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
		}},
		{"blank region", func(m *FilesystemSnapshotPolicyResourceModel) { m.Region = regionReference(" ") }},
		{"unknown name", func(m *FilesystemSnapshotPolicyResourceModel) { m.Name = types.StringUnknown() }},
		{"unknown days", func(m *FilesystemSnapshotPolicyResourceModel) { m.DaysOfWeek = types.ListUnknown(types.StringType) }},
		{"unknown day", func(m *FilesystemSnapshotPolicyResourceModel) {
			m.DaysOfWeek = types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})
		}},
		{"invalid time", func(m *FilesystemSnapshotPolicyResourceModel) { m.Time = types.StringValue("2am") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := configuredFilesystemSnapshotPolicy()
			tt.mutate(&m)
			if _, diags := buildSnapshotPolicyCreateBody(ctx, m, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
				t.Error("an invalid plan must not resolve the region")
				return projectsdk.ProjectRegionSchema{}, nil
			}); !diags.HasError() {
				t.Fatal("want a diagnostic")
			}
		})
	}
}

func TestBuildSnapshotPolicyUpdateBody(t *testing.T) {
	t.Parallel()
	state := configuredFilesystemSnapshotPolicy().FilesystemSnapshotPolicyModel
	state.Description, state.Retention = types.StringValue("before"), types.Int64Value(3)
	for _, tt := range []struct {
		name            string
		mutate          func(*FilesystemSnapshotPolicyModel)
		wantRetention   int
		wantDescription *string
	}{
		{"unchanged", func(*FilesystemSnapshotPolicyModel) {}, 3, new("before")},
		{"omitted retention comes from state", func(m *FilesystemSnapshotPolicyModel) { m.Retention = types.Int64Null() }, 3, new("before")},
		{"unknown retention comes from state", func(m *FilesystemSnapshotPolicyModel) { m.Retention = types.Int64Unknown() }, 3, new("before")},
		{"changed retention", func(m *FilesystemSnapshotPolicyModel) { m.Retention = types.Int64Value(9) }, 9, new("before")},
		{"unknown description is omitted", func(m *FilesystemSnapshotPolicyModel) { m.Description = types.StringUnknown() }, 3, nil},
		{"null description is omitted", func(m *FilesystemSnapshotPolicyModel) { m.Description = types.StringNull() }, 3, nil},
		{"cleared description", func(m *FilesystemSnapshotPolicyModel) { m.Description = types.StringValue("") }, 3, new("")},
		{"padded description is trimmed", func(m *FilesystemSnapshotPolicyModel) { m.Description = types.StringValue(" after ") }, 3, new("after")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := state
			plan.Name, plan.DaysOfWeek, plan.Time, plan.Status = types.StringValue(" renamed "), dayList(" sun "), types.StringValue(" 23:30 "), types.StringValue(" disabled ")
			tt.mutate(&plan)
			body, diags := buildSnapshotPolicyUpdateBody(plan, state)
			if diags.HasError() {
				t.Fatalf("update body: %v", diags)
			}
			if body.Name != "renamed" || len(body.DaysOfWeek) != 1 || body.DaysOfWeek[0] != filestoragesdk.SnapshotDayOfWeekSun ||
				body.Time != "23:30" || body.Status != filestoragesdk.SnapshotPolicyStatusDisabled {
				t.Fatalf("required fields: %#v", body)
			}
			if retention := core.NullableToPtr(body.Retention); retention == nil || *retention != tt.wantRetention {
				t.Fatalf("retention = %v, want %d", retention, tt.wantRetention)
			}
			assertNullableString(t, "description", body.Description.IsSpecified(), core.NullableToPtr(body.Description), tt.wantDescription)
		})
	}

	noRetention := state
	noRetention.Retention = types.Int64Null()
	if _, diags := buildSnapshotPolicyUpdateBody(noRetention, noRetention); !diags.HasError() {
		t.Fatal("an update without any known retention must fail instead of sending null")
	}
	unknownStatus := state
	unknownStatus.Status = types.StringUnknown()
	if _, diags := buildSnapshotPolicyUpdateBody(unknownStatus, state); !diags.HasError() {
		t.Fatal("an unknown status must fail")
	}
}

func TestFilesystemSnapshotPolicyResourceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	policy := sampleFilesystemSnapshotPolicy()
	itemPath := policiesPath + testPolicyID.String() + "/"
	regionsPath := "/v2/projects/" + testProjectID.String() + "/regions/"
	var calls []string
	roundTrip := func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		if req.URL.Path == regionsPath {
			if req.URL.Query().Get("name") != "vn-central-1" {
				t.Errorf("region lookup must filter by the trimmed name: %s", req.URL.RawQuery)
			}
			page, err := json.Marshal(projectsdk.PagedProjectRegionSchema{Count: 1, Results: []projectsdk.ProjectRegionSchema{{
				Region: projectsdk.NestedRegionSchema{Id: testRegionID, Name: "vn-central-1"},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			return testHTTPResponse(req, http.StatusOK, string(page)), nil
		}
		if req.Header.Get("Project-ID") != testProjectID.String() {
			t.Errorf("missing Project-ID header on %s %s", req.Method, req.URL)
		}
		var body map[string]any
		if req.Body != nil && req.Method != http.MethodGet && req.Method != http.MethodDelete {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == policiesPath:
			if len(body) != 6 || body["name"] != "nightly" || body["region_id"] != testRegionID.String() || body["time"] != "02:00" || body["status"] != "enabled" || body["retention"] != float64(1) {
				t.Errorf("create body must hold the required fields and the default retention: %v", body)
			}
			return testHTTPResponse(req, http.StatusCreated, filesystemSnapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodPut && req.URL.Path == itemPath:
			if len(body) != 6 || body["name"] != "renamed" || body["retention"] != float64(1) || body["description"] != "" || body["status"] != "disabled" {
				t.Errorf("update body must be complete and carry retention from state: %v", body)
			}
			policy.Name, policy.Status, policy.Description = "renamed", filestoragesdk.SnapshotPolicyStatusDisabled, core.NullableFromPtr(new(""))
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyJSON(t, policy)), nil
		case req.Method == http.MethodDelete && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusNoContent, ""), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(&http.Client{Transport: testRoundTripFunc(roundTrip)}), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &FilesystemSnapshotPolicyResource{client: testClient(t, roundTrip), project: projectClient, projectID: testProjectID}

	model := configuredFilesystemSnapshotPolicy()
	model.Description, model.Retention = types.StringUnknown(), types.Int64Unknown()
	model.ID, model.FilesystemCount, model.State, model.RegionID, model.RegionName = types.StringUnknown(), types.Int64Unknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	model.ProjectID, model.OwnerID, model.CreatedAt, model.UpdatedAt = types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdState FilesystemSnapshotPolicyResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.ID.ValueString() != testPolicyID.String() ||
		createdState.Retention.ValueInt64() != 1 || !createdState.Description.IsNull() || createdState.Time.ValueString() != "02:00" ||
		createdState.RegionID.ValueString() != testRegionID.String() || createdState.State.ValueString() != "ready" {
		t.Fatalf("created state: %#v %v", createdState, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatalf("refresh must keep a stable state: %v", read.Diagnostics)
	}

	updatedModel := createdState
	updatedModel.Name, updatedModel.Status, updatedModel.Description, updatedModel.Retention = types.StringValue("renamed"), types.StringValue("disabled"), types.StringValue(""), types.Int64Null()
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, updatedModel), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	var updatedState FilesystemSnapshotPolicyResourceModel
	if diags := updated.State.Get(ctx, &updatedState); diags.HasError() || updatedState.ID.ValueString() != testPolicyID.String() ||
		updatedState.Name.ValueString() != "renamed" || updatedState.Status.ValueString() != "disabled" || updatedState.Description.ValueString() != "" ||
		updatedState.Retention.ValueInt64() != 1 {
		t.Fatalf("updated state: %#v %v", updatedState, diags)
	}

	removed := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &removed)
	if removed.Diagnostics.HasError() {
		t.Fatalf("delete: %v", removed.Diagnostics)
	}
	want := []string{"GET " + regionsPath, "POST " + policiesPath, "GET " + itemPath, "PUT " + itemPath, "DELETE " + itemPath}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", calls, want)
	}
}

func TestFilesystemSnapshotPolicyResourceNotFoundErrorsAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	status := http.StatusNotFound
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, status, `{"detail":"failure"}`), nil
	})
	r := &FilesystemSnapshotPolicyResource{client: client, projectID: testProjectID}
	model := configuredFilesystemSnapshotPolicy()
	model.ID = types.StringValue(testPolicyID.String())
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}

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
	r.ImportState(ctx, resource.ImportStateRequest{ID: testPolicyID.String()}, &imported)
	var id types.String
	if diags := imported.State.GetAttribute(ctx, path.Root("id"), &id); imported.Diagnostics.HasError() || diags.HasError() || id.ValueString() != testPolicyID.String() {
		t.Fatalf("import ID: %v %v %v", id, imported.Diagnostics, diags)
	}
}

func TestFilesystemSnapshotPolicyResourceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/regions/") {
			page, err := json.Marshal(projectsdk.PagedProjectRegionSchema{Count: 1, Results: []projectsdk.ProjectRegionSchema{{
				Region: projectsdk.NestedRegionSchema{Id: testRegionID, Name: "vn-central-1"},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			return testHTTPResponse(req, http.StatusOK, string(page)), nil
		}
		return testHTTPResponse(req, http.StatusBadRequest, `{"detail":"retention may not be null"}`), nil
	}
	project, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(&http.Client{Transport: testRoundTripFunc(roundTrip)}), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &FilesystemSnapshotPolicyResource{client: testClient(t, roundTrip), project: project, projectID: testProjectID}

	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, configuredFilesystemSnapshotPolicy())}, &created)
	if !created.Diagnostics.HasError() || created.Diagnostics.Errors()[0].Summary() != "Error creating filesystem snapshot policy" || !created.State.Raw.IsNull() {
		t.Fatalf("create must report the API error and write no state: %v", created.Diagnostics)
	}

	model := configuredFilesystemSnapshotPolicy()
	model.ID, model.Retention, model.Description = types.StringValue(testPolicyID.String()), types.Int64Value(1), types.StringValue("")
	prior := tfsdk.State{Schema: s}
	if diags := prior.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	updated := resource.UpdateResponse{State: prior}
	r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, model), State: prior}, &updated)
	if !updated.Diagnostics.HasError() || updated.Diagnostics.Errors()[0].Summary() != "Error updating filesystem snapshot policy" {
		t.Fatalf("update must report the API error: %v", updated.Diagnostics)
	}
}

// The requests carry the trimmed description, and state after create, read,
// and update keeps the configured spelling.
func TestFilesystemSnapshotPolicyResourceTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotPolicyResourceSchema(t)
	policy := sampleFilesystemSnapshotPolicy()
	itemPath := policiesPath + testPolicyID.String() + "/"
	regionsPath := "/v2/projects/" + testProjectID.String() + "/regions/"
	var sent []string
	roundTrip := func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == regionsPath:
			if got := req.URL.Query().Get("name"); got != "vn-central-1" {
				t.Errorf("region filter = %q, want the trimmed name", got)
			}
			page, err := json.Marshal(projectsdk.PagedProjectRegionSchema{Count: 1, Results: []projectsdk.ProjectRegionSchema{{
				Region: projectsdk.NestedRegionSchema{Id: testRegionID, Name: "vn-central-1"},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			return testHTTPResponse(req, http.StatusOK, string(page)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyJSON(t, policy)), nil
		case (req.Method == http.MethodPost && req.URL.Path == policiesPath) || (req.Method == http.MethodPut && req.URL.Path == itemPath):
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			description, ok := body["description"].(string)
			if !ok {
				t.Errorf("%s body has no description: %#v", req.Method, body)
			}
			sent = append(sent, description)
			if body["time"] != "02:00" || body["status"] != "enabled" || !reflect.DeepEqual(body["days_of_week"], []any{"mon", "fri"}) {
				t.Errorf("%s body must carry trimmed schedule values: %#v", req.Method, body)
			}
			// The fake backend trims the description, as the API does.
			policy.Description = core.NullableFromPtr(new(strings.TrimSpace(description)))
			status := http.StatusOK
			if req.Method == http.MethodPost {
				status = http.StatusCreated
			}
			return testHTTPResponse(req, status, filesystemSnapshotPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(&http.Client{Transport: testRoundTripFunc(roundTrip)}), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &FilesystemSnapshotPolicyResource{client: testClient(t, roundTrip), project: projectClient, projectID: testProjectID}
	description := func(t *testing.T, state tfsdk.State) string {
		t.Helper()
		var model FilesystemSnapshotPolicyResourceModel
		if diags := state.Get(ctx, &model); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		return model.Description.ValueString()
	}

	model := configuredFilesystemSnapshotPolicy()
	model.Description = types.StringValue(" saved ")
	model.DaysOfWeek, model.Time, model.Status = dayList(" mon ", " fri "), types.StringValue(" 02:00 "), types.StringValue(" enabled ")
	model.Region = regionReference(" vn-central-1 ")
	keepsSchedule := func(t *testing.T, state tfsdk.State) {
		t.Helper()
		var got FilesystemSnapshotPolicyResourceModel
		if diags := state.Get(ctx, &got); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if !got.DaysOfWeek.Equal(model.DaysOfWeek) || !got.Time.Equal(model.Time) || !got.Status.Equal(model.Status) || !got.Region.Equal(model.Region) {
			t.Fatalf("state must keep the configured spelling: days=%v time=%v status=%v region=%v", got.DaysOfWeek, got.Time, got.Status, got.Region)
		}
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	if got := description(t, created.State); got != " saved " || strings.Join(sent, "|") != "saved" {
		t.Fatalf("after create: state %q, sent %q", got, sent)
	}
	keepsSchedule(t, created.State)
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || description(t, read.State) != " saved " {
		t.Fatalf("after read: state %q %v", description(t, read.State), read.Diagnostics)
	}
	keepsSchedule(t, read.State)

	var current FilesystemSnapshotPolicyResourceModel
	if diags := read.State.Get(ctx, &current); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	current.Description = types.StringValue(" after ")
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPolicyPlan(ctx, t, s, current), State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	if got := description(t, updated.State); got != " after " || strings.Join(sent, "|") != "saved|after" {
		t.Fatalf("after update: state %q, sent %q", got, sent)
	}
}
