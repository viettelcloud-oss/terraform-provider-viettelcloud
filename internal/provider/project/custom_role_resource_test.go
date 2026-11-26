package project

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func customRoleResourceSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&CustomRoleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func customRoleActions(urns ...string) types.Set {
	return reference.Set(reference.URN, urns)
}

// customRoleConfigModel returns a configuration that sets only display_name
// and actions.
func customRoleConfigModel(displayName string, urns ...string) CustomRoleModel {
	return CustomRoleModel{DisplayName: types.StringValue(displayName), Actions: customRoleActions(urns...)}
}

// customRoleCreatePlan returns the plan that Terraform derives from config:
// every unconfigured Computed value is unknown, and enabled takes its default.
func customRoleCreatePlan(config CustomRoleModel) CustomRoleModel {
	plan := config
	plan.ID = types.StringUnknown()
	plan.Name = types.StringUnknown()
	plan.URN = types.StringUnknown()
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	if config.Description.IsNull() {
		plan.Description = types.StringUnknown()
	}
	if config.Enabled.IsNull() {
		plan.Enabled = types.BoolValue(true)
	}
	return plan
}

// customRoleStateModel returns the state of customRoleFixture.
func customRoleStateModel() CustomRoleModel {
	var state CustomRoleModel
	populateCustomRoleModel(customRoleFixture(), &state)
	return state
}

func setCustomRolePlan(t *testing.T, schema resourceschema.Schema, model CustomRoleModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func setCustomRoleState(t *testing.T, schema resourceschema.Schema, model CustomRoleModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func setCustomRoleConfig(t *testing.T, schema resourceschema.Schema, model CustomRoleModel) tfsdk.Config {
	t.Helper()
	plan := setCustomRolePlan(t, schema, model)
	return tfsdk.Config{Schema: schema, Raw: plan.Raw}
}

func getCustomRoleState(t *testing.T, state tfsdk.State) CustomRoleModel {
	t.Helper()
	var model CustomRoleModel
	if diags := state.Get(context.Background(), &model); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	return model
}

// jsonFields decodes a request body into its top-level fields, so a test can
// assert which fields are present.
func jsonFields(t *testing.T, body any) map[string]json.RawMessage {
	t.Helper()
	var raw []byte
	switch value := body.(type) {
	case []byte:
		raw = value
	default:
		var err error
		if raw, err = json.Marshal(value); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode body %s: %v", raw, err)
	}
	return fields
}

func fieldNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestCustomRoleResourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	schema := customRoleResourceSchema(t)
	setCustomRoleState(t, schema, customRoleStateModel())
	setCustomRolePlan(t, schema, customRoleCreatePlan(customRoleConfigModel("Ops", customRoleTestActionA)))
}

func TestCustomRoleResourceSchemaFlags(t *testing.T) {
	t.Parallel()

	schema := customRoleResourceSchema(t)
	tests := []struct {
		name                         string
		required, optional, computed bool
	}{
		{"id", false, false, true},
		{"name", false, false, true},
		{"display_name", true, false, false},
		{"description", false, true, true},
		{"enabled", false, true, true},
		{"actions", true, false, false},
		{"urn", false, false, true},
		{"created_at", false, false, true},
		{"updated_at", false, false, true},
	}
	if len(schema.Attributes) != len(tests) {
		t.Fatalf("schema has %d attributes, want %d", len(schema.Attributes), len(tests))
	}
	for _, tt := range tests {
		attribute, ok := schema.Attributes[tt.name]
		if !ok {
			t.Fatalf("missing attribute %s", tt.name)
		}
		if attribute.IsRequired() != tt.required || attribute.IsOptional() != tt.optional || attribute.IsComputed() != tt.computed {
			t.Errorf("%s: required=%t optional=%t computed=%t, want %t %t %t", tt.name,
				attribute.IsRequired(), attribute.IsOptional(), attribute.IsComputed(), tt.required, tt.optional, tt.computed)
		}
	}

	actions := schema.Attributes["actions"].(resourceschema.SetNestedAttribute)
	urn, ok := actions.NestedObject.Attributes[reference.URN].(resourceschema.StringAttribute)
	if !ok || len(actions.NestedObject.Attributes) != 1 || !urn.Required {
		t.Fatalf("actions must be a set of { urn } references with a required urn, got %#v", actions.NestedObject.Attributes)
	}
}

func TestCustomRoleResourceEnabledDefaultsToTrue(t *testing.T) {
	t.Parallel()

	enabled := customRoleResourceSchema(t).Attributes["enabled"].(resourceschema.BoolAttribute)
	if enabled.Default == nil {
		t.Fatal("enabled must have a default")
	}
	var response defaults.BoolResponse
	enabled.Default.DefaultBool(context.Background(), defaults.BoolRequest{}, &response)
	if !response.PlanValue.Equal(types.BoolValue(true)) {
		t.Fatalf("enabled default = %s, want true", response.PlanValue)
	}
}

func TestCustomRoleResourcePlanModifiers(t *testing.T) {
	t.Parallel()

	schema := customRoleResourceSchema(t)
	state := setCustomRoleState(t, schema, customRoleStateModel())
	stable := []string{"id", "name", "urn", "created_at"}
	for name, attribute := range schema.Attributes {
		var modifiers []planmodifier.String
		switch typed := attribute.(type) {
		case resourceschema.StringAttribute:
			modifiers = typed.PlanModifiers
		case resourceschema.BoolAttribute:
			if len(typed.PlanModifiers) != 0 {
				t.Errorf("%s: unexpected plan modifiers", name)
			}
			continue
		case resourceschema.SetNestedAttribute:
			if len(typed.PlanModifiers) != 0 {
				t.Errorf("%s: unexpected plan modifiers", name)
			}
			continue
		default:
			t.Fatalf("%s: unexpected attribute type %T", name, attribute)
		}

		wantStable := slices.Contains(stable, name)
		if wantStable != (len(modifiers) == 1) || len(modifiers) > 1 {
			t.Errorf("%s: %d plan modifiers, want UseStateForUnknown only=%t", name, len(modifiers), wantStable)
			continue
		}
		for _, modifier := range modifiers {
			unknown := planmodifier.StringResponse{PlanValue: types.StringUnknown()}
			modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
				State: state, Plan: tfsdk.Plan(state), StateValue: types.StringValue("prior"),
				PlanValue: types.StringUnknown(), ConfigValue: types.StringNull(),
			}, &unknown)
			if !unknown.PlanValue.Equal(types.StringValue("prior")) || unknown.RequiresReplace {
				t.Errorf("%s: unknown plan became %s (replace=%t), want the prior value in place", name, unknown.PlanValue, unknown.RequiresReplace)
			}
			changed := planmodifier.StringResponse{PlanValue: types.StringValue("next")}
			modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
				State: state, Plan: tfsdk.Plan(state), StateValue: types.StringValue("prior"),
				PlanValue: types.StringValue("next"), ConfigValue: types.StringValue("next"),
			}, &changed)
			if changed.RequiresReplace {
				t.Errorf("%s: a change must not require replacement", name)
			}
		}
	}
}

func TestCustomRoleResourceValidators(t *testing.T) {
	t.Parallel()

	schema := customRoleResourceSchema(t)
	actions := schema.Attributes["actions"].(resourceschema.SetNestedAttribute)
	validatorsOf := map[string][]validator.String{
		"display_name": schema.Attributes["display_name"].(resourceschema.StringAttribute).Validators,
		"description":  schema.Attributes["description"].(resourceschema.StringAttribute).Validators,
		"actions.urn":  actions.NestedObject.Attributes[reference.URN].(resourceschema.StringAttribute).Validators,
	}
	tests := []struct {
		attribute string
		value     types.String
		wantErr   bool
	}{
		{"display_name", types.StringValue(""), true},
		{"display_name", types.StringValue(" \t "), true},
		{"display_name", types.StringValue(strings.Repeat("đ", 256)), true},
		{"display_name", types.StringValue(strings.Repeat("đ", 255)), false},
		{"display_name", types.StringValue(" " + strings.Repeat("đ", 255) + "\t"), false},
		{"display_name", types.StringValue(" " + strings.Repeat("đ", 256) + " "), true},
		{"display_name", types.StringValue("Đội vận hành"), false},
		{"display_name", types.StringValue("!!!"), false},
		{"display_name", types.StringUnknown(), false},
		{"description", types.StringValue(""), false},
		{"description", types.StringValue(strings.Repeat("a", 255)), false},
		{"description", types.StringValue(strings.Repeat("a", 256)), true},
		{"description", types.StringValue("  " + strings.Repeat("a", 255) + "\n"), false},
		{"description", types.StringValue(" \t "), false},
		{"actions.urn", types.StringValue(""), true},
		{"actions.urn", types.StringValue("  "), true},
		{"actions.urn", types.StringValue(customRoleTestActionA), false},
	}
	for _, tt := range tests {
		var response validator.StringResponse
		for _, v := range validatorsOf[tt.attribute] {
			v.ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root(tt.attribute), ConfigValue: tt.value,
			}, &response)
		}
		if response.Diagnostics.HasError() != tt.wantErr {
			t.Errorf("%s=%s: diagnostics %v, want error %t", tt.attribute, tt.value, response.Diagnostics, tt.wantErr)
		}
	}
}

func TestBuildCustomRoleCreateBodyOmitsUnconfiguredFields(t *testing.T) {
	t.Parallel()

	config := customRoleConfigModel("Đội vận hành", customRoleTestActionA, customRoleTestActionB)
	body, diags := buildCustomRoleCreateBody(customRoleCreatePlan(config), config)
	if diags.HasError() {
		t.Fatalf("build create body: %v", diags)
	}
	fields := jsonFields(t, body)
	if got := fieldNames(fields); !slices.Equal(got, []string{"action_urns", "display_name"}) {
		t.Fatalf("create body fields = %v, want only action_urns and display_name", got)
	}
	if string(fields["display_name"]) != `"Đội vận hành"` {
		t.Fatalf("display_name = %s", fields["display_name"])
	}
	if want := `["` + customRoleTestActionB + `","` + customRoleTestActionA + `"]`; string(fields["action_urns"]) != want {
		t.Fatalf("action_urns = %s, want %s", fields["action_urns"], want)
	}
}

func TestBuildCustomRoleCreateBodySendsConfiguredFields(t *testing.T) {
	t.Parallel()

	config := customRoleConfigModel("Ops", customRoleTestActionA)
	config.Description = types.StringValue("Operators")
	config.Enabled = types.BoolValue(false)
	body, diags := buildCustomRoleCreateBody(customRoleCreatePlan(config), config)
	if diags.HasError() {
		t.Fatalf("build create body: %v", diags)
	}
	fields := jsonFields(t, body)
	if got := fieldNames(fields); !slices.Equal(got, []string{"action_urns", "description", "display_name", "enabled"}) {
		t.Fatalf("create body fields = %v", got)
	}
	if string(fields["description"]) != `"Operators"` || string(fields["enabled"]) != "false" {
		t.Fatalf("unexpected description or enabled: %s %s", fields["description"], fields["enabled"])
	}
}

func TestBuildCustomRoleCreateBodySendsEmptyActionsAsList(t *testing.T) {
	t.Parallel()

	config := customRoleConfigModel("Ops")
	body, diags := buildCustomRoleCreateBody(customRoleCreatePlan(config), config)
	if diags.HasError() {
		t.Fatalf("build create body: %v", diags)
	}
	if got := string(jsonFields(t, body)["action_urns"]); got != "[]" {
		t.Fatalf("action_urns = %s, want []", got)
	}
}

func TestBuildCustomRoleCreateBodyRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	unknownURN := types.SetValueMust(reference.ObjectType(reference.URN), []attr.Value{reference.Value(reference.URN, types.StringUnknown())})
	tests := map[string]func(*CustomRoleModel){
		"display_name": func(m *CustomRoleModel) { m.DisplayName = types.StringUnknown() },
		"actions":      func(m *CustomRoleModel) { m.Actions = types.SetUnknown(reference.ObjectType(reference.URN)) },
		"actions.urn":  func(m *CustomRoleModel) { m.Actions = unknownURN },
	}
	for name, mutate := range tests {
		config := customRoleConfigModel("Ops", customRoleTestActionA)
		plan := customRoleCreatePlan(config)
		mutate(&plan)
		if _, diags := buildCustomRoleCreateBody(plan, config); !diags.HasError() {
			t.Errorf("unknown %s: expected a diagnostic", name)
		}
	}
}

func TestBuildCustomRoleUpdateBodySendsOnlyChangedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*CustomRoleModel)
		want   map[string]string
	}{
		{"no change", func(*CustomRoleModel) {}, map[string]string{}},
		{
			"display_name",
			func(m *CustomRoleModel) { m.DisplayName = types.StringValue("Platform team") },
			map[string]string{"display_name": `"Platform team"`},
		},
		{
			"description equal",
			func(m *CustomRoleModel) { m.Description = types.StringValue("") },
			map[string]string{},
		},
		{
			"description omitted",
			func(m *CustomRoleModel) { m.Description = types.StringUnknown() },
			map[string]string{},
		},
		{
			"enabled",
			func(m *CustomRoleModel) { m.Enabled = types.BoolValue(false) },
			map[string]string{"enabled": "false"},
		},
		{
			"action added and removed",
			func(m *CustomRoleModel) {
				m.Actions = customRoleActions(customRoleTestActionA, "urn:action:network:vpc:all")
			},
			map[string]string{"action_urns": `["urn:action:network:vpc:all","` + customRoleTestActionA + `"]`},
		},
		{
			"actions cleared",
			func(m *CustomRoleModel) { m.Actions = customRoleActions() },
			map[string]string{"action_urns": "[]"},
		},
		{
			"outer whitespace only",
			func(m *CustomRoleModel) {
				m.DisplayName = types.StringValue(" Đội vận hành ")
				m.Description = types.StringValue("  ")
			},
			map[string]string{},
		},
		{
			"actions reordered",
			func(m *CustomRoleModel) { m.Actions = customRoleActions(customRoleTestActionB, customRoleTestActionA) },
			map[string]string{},
		},
		{
			"action outer whitespace only",
			func(m *CustomRoleModel) {
				m.Actions = customRoleActions(" "+customRoleTestActionA+" ", customRoleTestActionB+"\t")
			},
			map[string]string{},
		},
		{
			"padded values sent trimmed",
			func(m *CustomRoleModel) {
				m.DisplayName = types.StringValue(" Platform team ")
				m.Description = types.StringValue(" Operators ")
				m.Actions = customRoleActions(" "+customRoleTestActionA+" ", " urn:action:network:vpc:all ")
			},
			map[string]string{
				"display_name": `"Platform team"`,
				"description":  `"Operators"`,
				"action_urns":  `["urn:action:network:vpc:all","` + customRoleTestActionA + `"]`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := customRoleStateModel()
			plan := customRoleStateModel()
			tt.mutate(&plan)
			body, changed, diags := buildCustomRoleUpdateBody(plan, state)
			if diags.HasError() {
				t.Fatalf("build update body: %v", diags)
			}
			fields := jsonFields(t, body)
			if changed != (len(fields) > 0) || len(fields) != len(tt.want) {
				t.Fatalf("update body = %v (changed=%t), want %v", fields, changed, tt.want)
			}
			for name, want := range tt.want {
				if string(fields[name]) != want {
					t.Errorf("%s = %s, want %s", name, fields[name], want)
				}
			}
		})
	}
}

func TestBuildCustomRoleUpdateBodyClearsDescription(t *testing.T) {
	t.Parallel()

	state := customRoleStateModel()
	state.Description = types.StringValue("Operators")
	plan := customRoleStateModel()
	plan.Description = types.StringValue("")
	body, changed, diags := buildCustomRoleUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("build update body: changed=%t %v", changed, diags)
	}
	fields := jsonFields(t, body)
	if got := fieldNames(fields); !slices.Equal(got, []string{"description"}) || string(fields["description"]) != `""` {
		t.Fatalf("update body = %v, want only description \"\"", fields)
	}
}

func TestBuildCustomRoleUpdateBodyReenablesOmittedEnabled(t *testing.T) {
	t.Parallel()

	state := customRoleStateModel()
	state.Enabled = types.BoolValue(false)
	// Removing enabled from the configuration plans its default, true.
	plan := customRoleStateModel()
	plan.Enabled = types.BoolValue(true)
	body, _, diags := buildCustomRoleUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("build update body: %v", diags)
	}
	if fields := jsonFields(t, body); len(fields) != 1 || string(fields["enabled"]) != "true" {
		t.Fatalf("update body = %v, want only enabled=true", fields)
	}
}

func TestBuildCustomRoleUpdateBodyRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*CustomRoleModel){
		"display_name": func(m *CustomRoleModel) { m.DisplayName = types.StringUnknown() },
		"actions":      func(m *CustomRoleModel) { m.Actions = types.SetUnknown(reference.ObjectType(reference.URN)) },
	} {
		plan := customRoleStateModel()
		mutate(&plan)
		if _, _, diags := buildCustomRoleUpdateBody(plan, customRoleStateModel()); !diags.HasError() {
			t.Errorf("unknown %s: expected a diagnostic", name)
		}
	}
}

func TestPopulateCustomRoleModelMapsEveryField(t *testing.T) {
	t.Parallel()

	got := customRoleStateModel()
	want := CustomRoleModel{
		ID:          types.StringValue(customRoleTestID.String()),
		Name:        types.StringValue("doi-van-hanh"),
		DisplayName: types.StringValue("Đội vận hành"),
		Description: types.StringValue(""),
		Enabled:     types.BoolValue(true),
		Actions:     customRoleActions(customRoleTestActionA, customRoleTestActionB),
		URN:         types.StringValue("urn:role:prj-demo:doi-van-hanh"),
		CreatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
		UpdatedAt:   types.StringValue("2026-01-03T04:05:06Z"),
	}
	if !sameCustomRoleModel(got, want) {
		t.Fatalf("populated model = %#v, want %#v", got, want)
	}
}

func TestPopulateCustomRoleResourceStateKeepsWhitespaceVariants(t *testing.T) {
	t.Parallel()

	state := CustomRoleModel{
		DisplayName: types.StringValue(" Đội vận hành "),
		Description: types.StringValue("  "),
	}
	populateCustomRoleResourceState(customRoleFixture(), &state)
	if state.DisplayName.ValueString() != " Đội vận hành " || state.Description.ValueString() != "  " {
		t.Fatalf("state display_name=%s description=%s, want the configured whitespace kept", state.DisplayName, state.Description)
	}
	if !state.URN.Equal(types.StringValue(customRoleFixture().Urn)) {
		t.Fatalf("urn = %s, want the response value", state.URN)
	}

	changed := CustomRoleModel{DisplayName: types.StringValue("Ops"), Description: types.StringUnknown()}
	populateCustomRoleResourceState(customRoleFixture(), &changed)
	if changed.DisplayName.ValueString() != "Đội vận hành" || changed.Description.ValueString() != "" {
		t.Fatalf("state display_name=%s description=%s, want the response values", changed.DisplayName, changed.Description)
	}

	before := customRoleStateModel()
	populateCustomRoleResourceState(nil, &before)
	if !sameCustomRoleModel(before, customRoleStateModel()) {
		t.Fatal("a nil role must leave the state unchanged")
	}
}

func TestPopulateCustomRoleModelIgnoresActionOrder(t *testing.T) {
	t.Parallel()

	role := customRoleFixture()
	slices.Reverse(role.Actions)
	var got CustomRoleModel
	populateCustomRoleModel(role, &got)
	if planned := customRoleActions(customRoleTestActionA, customRoleTestActionB); !got.Actions.Equal(planned) {
		t.Fatalf("actions %s differ from planned %s", got.Actions, planned)
	}
}

func TestPopulateCustomRoleModelMapsMissingActionsToEmptySet(t *testing.T) {
	t.Parallel()

	role := customRoleFixture()
	role.Actions = nil
	var got CustomRoleModel
	populateCustomRoleModel(role, &got)
	if got.Actions.IsNull() || got.Actions.IsUnknown() || len(got.Actions.Elements()) != 0 {
		t.Fatalf("actions = %s, want a known empty set", got.Actions)
	}

	before := got
	populateCustomRoleModel(nil, &got)
	if !sameCustomRoleModel(got, before) {
		t.Fatal("a nil role must leave the model unchanged")
	}
}

func TestCustomRoleResourceMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	r, ok := NewCustomRoleResource().(*CustomRoleResource)
	if !ok {
		t.Fatal("NewCustomRoleResource must return *CustomRoleResource")
	}
	var metadata resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_project_custom_role" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	var nilResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("nil provider data: %v", nilResponse.Diagnostics)
	}
	var invalidResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for invalid provider data")
	}

	client, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Project: client, ProjectID: customRoleTestProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() || r.client != client || r.projectID != customRoleTestProjectID {
		t.Fatalf("expected the project client and project ID to be configured: %v", validResponse.Diagnostics)
	}
}

func TestCustomRoleResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	current := customRoleFixture()
	var calls []string
	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == customRoleTestRolesPath:
			fields := jsonFields(t, raw)
			if got := fieldNames(fields); !slices.Equal(got, []string{"action_urns", "display_name"}) {
				t.Errorf("create body fields = %v, want action_urns and display_name only", got)
			}
			writeCustomRoleJSON(t, w, http.StatusCreated, current)
		case req.Method == http.MethodGet && req.URL.Path == customRoleTestPath:
			writeCustomRoleJSON(t, w, http.StatusOK, current)
		case req.Method == http.MethodPatch && req.URL.Path == customRoleTestPath:
			fields := jsonFields(t, raw)
			if got := fieldNames(fields); !slices.Equal(got, []string{"action_urns", "description", "display_name", "enabled"}) {
				t.Errorf("update body fields = %v", got)
			}
			var body projectsdk.CustomRolePartialUpdateSchema
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			current.DisplayName = *body.DisplayName
			current.Description = *body.Description
			current.Enabled = *body.Enabled
			current.Actions = nil
			for _, urn := range *body.ActionUrns {
				current.Actions = append(current.Actions, projectsdk.NestedActionSchema{Urn: urn})
			}
			writeCustomRoleJSON(t, w, http.StatusOK, current)
		case req.Method == http.MethodDelete && req.URL.Path == customRoleTestPath:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	schema := customRoleResourceSchema(t)

	config := customRoleConfigModel("Đội vận hành", customRoleTestActionA, customRoleTestActionB)
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{
		Plan:   setCustomRolePlan(t, schema, customRoleCreatePlan(config)),
		Config: setCustomRoleConfig(t, schema, config),
	}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	created := getCustomRoleState(t, createResponse.State)
	if !sameCustomRoleModel(created, customRoleStateModel()) {
		t.Fatalf("created state = %#v, want every attribute from the response", created)
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	plan := getCustomRoleState(t, readResponse.State)
	plan.DisplayName = types.StringValue("Platform team")
	plan.Description = types.StringValue("Operators")
	plan.Enabled = types.BoolValue(false)
	plan.Actions = customRoleActions(customRoleTestActionB, "urn:action:network:vpc:all")
	plan.UpdatedAt = types.StringUnknown()
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: setCustomRolePlan(t, schema, plan), State: readResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}
	updated := getCustomRoleState(t, updateResponse.State)
	if updated.ID != created.ID || updated.Name != created.Name || updated.URN != created.URN {
		t.Fatalf("update changed identity: %#v", updated)
	}
	if updated.DisplayName.ValueString() != "Platform team" || updated.Description.ValueString() != "Operators" ||
		updated.Enabled.ValueBool() || !updated.Actions.Equal(plan.Actions) {
		t.Fatalf("updated state = %#v, want the planned values", updated)
	}

	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	want := []string{"POST " + customRoleTestRolesPath, "GET " + customRoleTestPath, "PATCH " + customRoleTestPath, "DELETE " + customRoleTestPath}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// TestCustomRoleResourceCreateTrimsTextAndActionsAndKeepsConfiguredSpelling
// checks that create sends display_name, description, and every action URN
// trimmed, and that state after create and after the next read keeps the
// configured spelling, because the backend returns the trimmed values.
func TestCustomRoleResourceCreateTrimsTextAndActionsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	current := customRoleFixture()
	var created projectsdk.CustomRoleCreateSchema
	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == customRoleTestRolesPath:
			if err := json.NewDecoder(req.Body).Decode(&created); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			current.DisplayName = strings.TrimSpace(created.DisplayName)
			current.Description = strings.TrimSpace(*created.Description)
			current.Actions = nil
			for _, urn := range created.ActionUrns {
				current.Actions = append(current.Actions, projectsdk.NestedActionSchema{Urn: strings.TrimSpace(urn)})
			}
			writeCustomRoleJSON(t, w, http.StatusCreated, current)
		case req.Method == http.MethodGet && req.URL.Path == customRoleTestPath:
			writeCustomRoleJSON(t, w, http.StatusOK, current)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	schema := customRoleResourceSchema(t)

	paddedA, paddedB := " "+customRoleTestActionA+" ", customRoleTestActionB+"\t"
	config := customRoleConfigModel(" Đội vận hành ", paddedA, paddedB)
	config.Description = types.StringValue(" desc ")
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{
		Plan:   setCustomRolePlan(t, schema, customRoleCreatePlan(config)),
		Config: setCustomRoleConfig(t, schema, config),
	}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	if created.DisplayName != "Đội vận hành" || created.Description == nil || *created.Description != "desc" ||
		!slices.Equal(created.ActionUrns, []string{customRoleTestActionB, customRoleTestActionA}) {
		t.Fatalf("create body display_name=%q description=%v action_urns=%q, want trimmed values",
			created.DisplayName, created.Description, created.ActionUrns)
	}

	check := func(stage string, state CustomRoleModel) {
		t.Helper()
		if state.DisplayName.ValueString() != " Đội vận hành " || state.Description.ValueString() != " desc " {
			t.Errorf("%s: display_name=%s description=%s, want the configured spelling", stage, state.DisplayName, state.Description)
		}
		if want := customRoleActions(paddedA, paddedB); !state.Actions.Equal(want) {
			t.Errorf("%s: actions = %s, want the configured spelling %s", stage, state.Actions, want)
		}
	}
	check("create", getCustomRoleState(t, createResponse.State))

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	check("read", getCustomRoleState(t, readResponse.State))
}

func TestPopulateCustomRoleResourceStateShowsActionDrift(t *testing.T) {
	t.Parallel()

	state := customRoleStateModel()
	state.Actions = customRoleActions(" "+customRoleTestActionA+" ", "urn:action:network:vpc:all")
	populateCustomRoleResourceState(customRoleFixture(), &state)
	if want := customRoleActions(" "+customRoleTestActionA+" ", customRoleTestActionB); !state.Actions.Equal(want) {
		t.Fatalf("actions = %s, want %s: the matching URN keeps its spelling and the response adds the other", state.Actions, want)
	}
}

func TestValidateCustomRoleActionsRejectsTrimmedDuplicates(t *testing.T) {
	t.Parallel()

	unknownURN := reference.Value(reference.URN, types.StringUnknown())
	tests := []struct {
		name    string
		actions types.Set
		wantErr bool
	}{
		{"distinct", customRoleActions(customRoleTestActionA, customRoleTestActionB), false},
		{"same after trim", customRoleActions(customRoleTestActionA, " "+customRoleTestActionA+"\t"), true},
		{"empty", customRoleActions(), false},
		{"null", types.SetNull(reference.ObjectType(reference.URN)), false},
		{"unknown set", types.SetUnknown(reference.ObjectType(reference.URN)), false},
		{
			"unknown urn",
			types.SetValueMust(reference.ObjectType(reference.URN), []attr.Value{
				reference.Value(reference.URN, types.StringValue(customRoleTestActionA)), unknownURN,
			}),
			false,
		},
	}
	for _, tt := range tests {
		if diags := validateCustomRoleActions(tt.actions); diags.HasError() != tt.wantErr {
			t.Errorf("%s: diagnostics %v, want error %t", tt.name, diags, tt.wantErr)
		}
	}
}

func TestCustomRoleResourceCreateReportsEveryBackendError(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusBadRequest,
			"Project custom role name must be unique in a project",
			"Action urn:action:unknown:thing:get does not exist")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	schema := customRoleResourceSchema(t)
	config := customRoleConfigModel("Server operator", "urn:action:unknown:thing:get")
	response := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan:   setCustomRolePlan(t, schema, customRoleCreatePlan(config)),
		Config: setCustomRoleConfig(t, schema, config),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a create diagnostic")
	}
	detail := response.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, "must be unique in a project") || !strings.Contains(detail, "does not exist") {
		t.Fatalf("diagnostic %q must report every backend error", detail)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("a failed create must not write state")
	}
}

func TestCustomRoleResourceReadRemovesMissingRole(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusNotFound, "Not found")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), customRoleStateModel())
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("expected the missing role to be removed from state")
	}
}

func TestCustomRoleResourceReadReportsAPIError(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusForbidden, "Permission denied")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), customRoleStateModel())
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || response.State.Raw.IsNull() {
		t.Fatalf("expected a read diagnostic and kept state, got %v", response.Diagnostics)
	}
}

func TestCustomRoleResourceUpdateWithoutChangesReadsRole(t *testing.T) {
	t.Parallel()

	var methods []string
	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		methods = append(methods, req.Method)
		writeCustomRoleJSON(t, w, http.StatusOK, customRoleFixture())
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	schema := customRoleResourceSchema(t)
	state := setCustomRoleState(t, schema, customRoleStateModel())
	response := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: setCustomRolePlan(t, schema, customRoleStateModel()), State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !slices.Equal(methods, []string{http.MethodGet}) {
		t.Fatalf("methods = %v, want a single GET", methods)
	}
}

func TestCustomRoleResourceUpdateRejectsAnotherRole(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		other := customRoleFixture()
		other.Id = core.UUID{0x99}
		writeCustomRoleJSON(t, w, http.StatusOK, other)
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	schema := customRoleResourceSchema(t)
	plan := customRoleStateModel()
	plan.DisplayName = types.StringValue("Renamed")
	response := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan: setCustomRolePlan(t, schema, plan), State: setCustomRoleState(t, schema, customRoleStateModel()),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic when the API answers with another role")
	}
}

func TestCustomRoleResourceDeleteAcceptsNotFound(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusNotFound, "Not found")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), customRoleStateModel())
	response := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
}

func TestCustomRoleResourceDeleteReportsRoleInUse(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusConflict,
			"Role is assigned to user alice@example.com",
			"Role is assigned to group operators")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), customRoleStateModel())
	response := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a role in use")
	}
	diagnostic := response.Diagnostics.Errors()[0]
	if diagnostic.Summary() != "Custom Role is in use" ||
		!strings.Contains(diagnostic.Detail(), "alice@example.com") || !strings.Contains(diagnostic.Detail(), "group operators") {
		t.Fatalf("diagnostic = %q: %q, want the users and groups that hold the role", diagnostic.Summary(), diagnostic.Detail())
	}
}

func TestCustomRoleResourceDeleteReportsAPIError(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusForbidden, "Permission denied")
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), customRoleStateModel())
	response := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Error deleting Custom Role" {
		t.Fatalf("expected a delete diagnostic, got %v", response.Diagnostics)
	}
}

func TestCustomRoleResourceRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	r := &CustomRoleResource{}
	schema := customRoleResourceSchema(t)
	model := customRoleStateModel()
	model.ID = types.StringValue("not-a-uuid")
	state := setCustomRoleState(t, schema, model)

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	updateResponse := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: tfsdk.Plan(state), State: state}, &updateResponse)
	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	for name, diags := range map[string]bool{
		"read":   readResponse.Diagnostics.HasError(),
		"update": updateResponse.Diagnostics.HasError(),
		"delete": deleteResponse.Diagnostics.HasError(),
	} {
		if !diags {
			t.Errorf("%s: expected an invalid UUID diagnostic", name)
		}
	}
}

func TestCustomRoleResourceImportState(t *testing.T) {
	t.Parallel()

	schema := customRoleResourceSchema(t)
	response := resource.ImportStateResponse{State: setCustomRoleState(t, schema, CustomRoleModel{
		Actions: types.SetNull(reference.ObjectType(reference.URN)),
	})}
	(&CustomRoleResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: customRoleTestID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if id.ValueString() != customRoleTestID.String() {
		t.Fatalf("imported id = %s, want %s", id, customRoleTestID)
	}
}

func TestCustomRoleResourceReadAfterImportFillsEveryAttribute(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleJSON(t, w, http.StatusOK, customRoleFixture())
	})
	r := &CustomRoleResource{client: client, projectID: customRoleTestProjectID}
	state := setCustomRoleState(t, customRoleResourceSchema(t), CustomRoleModel{
		ID:      types.StringValue(customRoleTestID.String()),
		Actions: types.SetNull(reference.ObjectType(reference.URN)),
	})
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if got := getCustomRoleState(t, response.State); !sameCustomRoleModel(got, customRoleStateModel()) {
		t.Fatalf("imported state = %#v, want every attribute from the response", got)
	}
}
