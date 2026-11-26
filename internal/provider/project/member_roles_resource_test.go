package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func memberRolesSchema(t *testing.T) resourceschema.Schema {
	t.Helper()

	var response resource.SchemaResponse
	(&MemberRolesResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// newMemberRolesResourceModel returns a model whose collections carry their
// element types, with every value null.
func newMemberRolesResourceModel() MemberRolesResourceModel {
	return MemberRolesResourceModel{
		MemberRolesModel: MemberRolesModel{
			Assignments: types.ListNull(types.ObjectType{AttrTypes: memberRoleAssignmentAttributeTypes()}),
		},
		Member: types.ObjectNull(reference.AttributeTypes(memberCriterionUsername)),
		Roles:  types.SetNull(types.ObjectType{AttrTypes: memberRoleAttributeTypes()}),
	}
}

// newMemberRolesPlanModel returns a create plan: configured member and roles,
// every computed value unknown.
func newMemberRolesPlanModel(username string, roles types.Set) MemberRolesResourceModel {
	return MemberRolesResourceModel{
		MemberRolesModel: MemberRolesModel{
			ID:            types.StringUnknown(),
			MemberUserID:  types.StringUnknown(),
			MemberEmail:   types.StringUnknown(),
			MemberIsAdmin: types.BoolUnknown(),
			Assignments:   types.ListUnknown(types.ObjectType{AttrTypes: memberRoleAssignmentAttributeTypes()}),
			CreatedAt:     types.StringUnknown(),
			UpdatedAt:     types.StringUnknown(),
		},
		Member: reference.Value(memberCriterionUsername, types.StringValue(username)),
		Roles:  roles,
	}
}

func testMemberRole(urn types.String, enabled types.Bool) attr.Value {
	return types.ObjectValueMust(memberRoleAttributeTypes(), map[string]attr.Value{
		"role":    reference.Value(roleCriterionURN, urn),
		"enabled": enabled,
	})
}

func testMemberRoles(elements ...attr.Value) types.Set {
	return types.SetValueMust(types.ObjectType{AttrTypes: memberRoleAttributeTypes()}, elements)
}

func memberRolesPlan(t *testing.T, model MemberRolesResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: memberRolesSchema(t)}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func memberRolesState(t *testing.T, model MemberRolesResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: memberRolesSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func memberRolesStateModel(t *testing.T, state tfsdk.State) MemberRolesResourceModel {
	t.Helper()
	var model MemberRolesResourceModel
	if diags := state.Get(context.Background(), &model); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	return model
}

// memberRolesFromState returns the URN and enabled flag of every roles element.
func memberRolesFromState(t *testing.T, roles types.Set) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, element := range roles.Elements() {
		object := element.(types.Object)
		urn := reference.Get(roleReference(object), roleCriterionURN).ValueString()
		got[urn] = object.Attributes()["enabled"].(types.Bool).ValueBool()
	}
	return got
}

type memberRolesAssignmentValue struct {
	ID          types.String `tfsdk:"id"`
	URN         types.String `tfsdk:"urn"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Type        types.String `tfsdk:"type"`
	RoleEnabled types.Bool   `tfsdk:"role_enabled"`
}

func memberRolesAssignmentsFromState(t *testing.T, assignments types.List) []memberRolesAssignmentValue {
	t.Helper()
	var got []memberRolesAssignmentValue
	if diags := assignments.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode assignments: %v", diags)
	}
	return got
}

// decodePatchRoles returns the roles of a PATCH body and fails unless the body
// holds only roles and every element holds exactly urn and enabled.
func decodePatchRoles(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode PATCH body %s: %v", body, err)
	}
	if len(fields) != 1 || fields["roles"] == nil {
		t.Fatalf("PATCH body must hold only roles, got %s", body)
	}
	var roles []map[string]any
	if err := json.Unmarshal(fields["roles"], &roles); err != nil {
		t.Fatalf("decode roles %s: %v", fields["roles"], err)
	}
	if roles == nil {
		t.Fatalf("roles must be a JSON array, got %s", fields["roles"])
	}
	for _, role := range roles {
		if _, ok := role["urn"].(string); !ok || len(role) != 2 {
			t.Fatalf("each role must hold urn and enabled only, got %v", role)
		}
		if _, ok := role["enabled"].(bool); !ok {
			t.Fatalf("each role must hold a boolean enabled, got %v", role)
		}
	}
	return roles
}

func TestMemberRolesModelsMatchSchema(t *testing.T) {
	t.Parallel()

	memberRolesPlan(t, newMemberRolesResourceModel())
	memberRolesPlan(t, newMemberRolesPlanModel("alice", testMemberRoles(
		testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)),
	)))
}

func TestMemberRolesResourceSchema(t *testing.T) {
	t.Parallel()

	schema := memberRolesSchema(t)

	for _, name := range []string{"id", "member_user_id", "created_at"} {
		attribute, ok := schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || len(attribute.PlanModifiers) != 1 ||
			attribute.PlanModifiers[0].Description(context.Background()) != "Once set, the value of this attribute in state will not change." {
			t.Fatalf("%s must be computed with UseStateForUnknown, got %#v", name, schema.Attributes[name])
		}
	}
	for _, name := range []string{"member_email", "updated_at"} {
		attribute, ok := schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || len(attribute.PlanModifiers) != 0 {
			t.Fatalf("%s must be computed without plan modifiers, got %#v", name, schema.Attributes[name])
		}
	}
	isAdmin, ok := schema.Attributes["member_is_admin"].(resourceschema.BoolAttribute)
	if !ok || isAdmin.Optional || !isAdmin.Computed || len(isAdmin.PlanModifiers) != 0 {
		t.Fatalf("member_is_admin must be computed, got %#v", schema.Attributes["member_is_admin"])
	}

	member, ok := schema.Attributes["member"].(resourceschema.SingleNestedAttribute)
	if !ok || !member.Required || member.Computed || len(member.PlanModifiers) != 1 || len(member.Attributes) != 1 {
		t.Fatalf("member must be a required reference with one plan modifier, got %#v", schema.Attributes["member"])
	}
	username, ok := member.Attributes[memberCriterionUsername].(resourceschema.StringAttribute)
	if !ok || !username.Required || username.Computed || len(username.PlanModifiers) != 0 {
		t.Fatalf("member.username must be a required criterion without plan modifiers, got %#v", member.Attributes[memberCriterionUsername])
	}

	roles, ok := schema.Attributes["roles"].(resourceschema.SetNestedAttribute)
	if !ok || !roles.Required || roles.Computed || len(roles.PlanModifiers) != 0 || len(roles.NestedObject.Attributes) != 2 {
		t.Fatalf("roles must be a required set of role and enabled, got %#v", schema.Attributes["roles"])
	}
	role, ok := roles.NestedObject.Attributes["role"].(resourceschema.SingleNestedAttribute)
	if !ok || !role.Required || role.Computed || len(role.Attributes) != 1 {
		t.Fatalf("roles.role must be a required reference, got %#v", roles.NestedObject.Attributes["role"])
	}
	if urn, ok := role.Attributes[roleCriterionURN].(resourceschema.StringAttribute); !ok || !urn.Required {
		t.Fatalf("roles.role.urn must be required, got %#v", role.Attributes[roleCriterionURN])
	}
	enabled, ok := roles.NestedObject.Attributes["enabled"].(resourceschema.BoolAttribute)
	if !ok || enabled.Required || !enabled.Optional || !enabled.Computed || enabled.Default == nil {
		t.Fatalf("roles.enabled must be optional and computed with a default, got %#v", roles.NestedObject.Attributes["enabled"])
	}
	var defaultResponse defaults.BoolResponse
	enabled.Default.DefaultBool(context.Background(), defaults.BoolRequest{}, &defaultResponse)
	if !defaultResponse.PlanValue.Equal(types.BoolValue(true)) {
		t.Fatalf("roles.enabled default = %s, want true", defaultResponse.PlanValue)
	}

	assignments, ok := schema.Attributes["assignments"].(resourceschema.ListNestedAttribute)
	if !ok || assignments.Optional || !assignments.Computed || len(assignments.PlanModifiers) != 0 {
		t.Fatalf("assignments must be a computed list, got %#v", schema.Attributes["assignments"])
	}
	for name, attribute := range assignments.NestedObject.Attributes {
		if !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
			t.Errorf("assignments.%s must be computed only", name)
		}
	}
	if len(assignments.NestedObject.Attributes) != len(memberRoleAssignmentAttributeTypes()) {
		t.Fatalf("assignments attributes = %d, want %d", len(assignments.NestedObject.Attributes), len(memberRoleAssignmentAttributeTypes()))
	}
}

func TestMemberRolesMemberRequiresReplace(t *testing.T) {
	t.Parallel()

	schema := memberRolesSchema(t)
	member := schema.Attributes["member"].(resourceschema.SingleNestedAttribute)
	roles := testMemberRoles()
	state := memberRolesState(t, newMemberRolesPlanModel("alice", roles))
	plan := memberRolesPlan(t, newMemberRolesPlanModel("bob", roles))
	alice := reference.Value(memberCriterionUsername, types.StringValue("alice"))

	for _, test := range []struct {
		name    string
		plan    types.Object
		replace bool
	}{
		{"same username", alice, false},
		{"padded username", reference.Value(memberCriterionUsername, types.StringValue(" alice\t")), false},
		{"changed username", reference.Value(memberCriterionUsername, types.StringValue("bob")), true},
		{"padded other username", reference.Value(memberCriterionUsername, types.StringValue(" bob ")), true},
		{"changed case", reference.Value(memberCriterionUsername, types.StringValue("Alice")), true},
		{"unknown username", reference.Value(memberCriterionUsername, types.StringUnknown()), true},
		{"unknown reference", types.ObjectUnknown(reference.AttributeTypes(memberCriterionUsername)), true},
	} {
		request := planmodifier.ObjectRequest{
			State:      state,
			Plan:       plan,
			Config:     tfsdk.Config(plan),
			StateValue: alice,
			PlanValue:  test.plan,
		}
		var response planmodifier.ObjectResponse
		member.PlanModifiers[0].PlanModifyObject(context.Background(), request, &response)
		if response.RequiresReplace != test.replace {
			t.Errorf("member %s: replace=%t, want %t", test.name, response.RequiresReplace, test.replace)
		}
	}
}

type memberRolesTestProvider struct{}

func (memberRolesTestProvider) Metadata(_ context.Context, _ frameworkprovider.MetadataRequest, resp *frameworkprovider.MetadataResponse) {
	resp.TypeName = "viettelcloud"
}

func (memberRolesTestProvider) Schema(context.Context, frameworkprovider.SchemaRequest, *frameworkprovider.SchemaResponse) {
}

func (memberRolesTestProvider) Configure(context.Context, frameworkprovider.ConfigureRequest, *frameworkprovider.ConfigureResponse) {
}

func (memberRolesTestProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewMemberRolesResource}
}

func (memberRolesTestProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func memberRolesDynamicValue(t *testing.T, model *MemberRolesResourceModel) *tfprotov6.DynamicValue {
	t.Helper()
	schema := memberRolesSchema(t)
	objectType := schema.Type().TerraformType(context.Background())
	raw := tftypes.NewValue(objectType, nil)
	if model != nil {
		raw = memberRolesState(t, *model).Raw
	}
	value, err := tfprotov6.NewDynamicValue(objectType, raw)
	if err != nil {
		t.Fatalf("encode dynamic value: %v", err)
	}
	return &value
}

// planMemberRolesThroughFramework runs PlanResourceChange through the
// Framework server, so the roles.enabled default is applied as Terraform
// applies it. proposed must be built the way Terraform core builds it: a
// config set element whose optional and computed attributes are null takes
// the prior element with the same configured values.
func planMemberRolesThroughFramework(t *testing.T, prior *MemberRolesResourceModel, config, proposed MemberRolesResourceModel) (tftypes.Value, MemberRolesResourceModel) {
	t.Helper()
	server := providerserver.NewProtocol6(memberRolesTestProvider{})()
	response, err := server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "viettelcloud_project_member_roles",
		PriorState:       memberRolesDynamicValue(t, prior),
		Config:           memberRolesDynamicValue(t, &config),
		ProposedNewState: memberRolesDynamicValue(t, &proposed),
	})
	if err != nil {
		t.Fatalf("plan resource change: %v", err)
	}
	for _, diagnostic := range response.Diagnostics {
		t.Fatalf("plan diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
	}
	schema := memberRolesSchema(t)
	raw, err := response.PlannedState.Unmarshal(schema.Type().TerraformType(context.Background()))
	if err != nil {
		t.Fatalf("decode planned state: %v", err)
	}
	return raw, memberRolesStateModel(t, tfsdk.State{Schema: schema, Raw: raw})
}

// The roles.enabled default sits inside a set element. These cases prove that
// the Framework applies it without changing element identity or producing an
// invalid plan.
func TestMemberRolesResourcePlanAppliesEnabledDefault(t *testing.T) {
	t.Parallel()

	viewer := types.StringValue("urn:role:viewer")
	editor := types.StringValue("urn:role:editor")
	configured := func(roles types.Set) MemberRolesResourceModel {
		model := newMemberRolesResourceModel()
		model.Member = reference.Value(memberCriterionUsername, types.StringValue("alice"))
		model.Roles = roles
		return model
	}
	prior := func(roles types.Set) MemberRolesResourceModel {
		backend := newMemberRolesBackend(t)
		model := configured(roles)
		populateMemberRolesModel(&backend.member, &model.MemberRolesModel)
		return model
	}

	t.Run("create fills the omitted flag", func(t *testing.T) {
		t.Parallel()
		config := configured(testMemberRoles(
			testMemberRole(viewer, types.BoolNull()),
			testMemberRole(editor, types.BoolValue(false)),
		))
		_, planned := planMemberRolesThroughFramework(t, nil, config, config)
		if got := memberRolesFromState(t, planned.Roles); len(got) != 2 || !got["urn:role:viewer"] || got["urn:role:editor"] {
			t.Fatalf("planned roles = %v, want viewer enabled and editor disabled", got)
		}
		if !planned.ID.IsUnknown() || !planned.Assignments.IsUnknown() {
			t.Fatalf("computed values must be unknown on create, got id=%s assignments=%s", planned.ID, planned.Assignments)
		}
	})

	t.Run("omitted flag matching prior state plans no change", func(t *testing.T) {
		t.Parallel()
		state := prior(testMemberRoles(testMemberRole(viewer, types.BoolValue(true))))
		config := configured(testMemberRoles(testMemberRole(viewer, types.BoolNull())))
		raw, _ := planMemberRolesThroughFramework(t, &state, config, state)
		if !raw.Equal(memberRolesState(t, state).Raw) {
			t.Fatalf("expected the planned state to equal the prior state, got %s", raw)
		}
	})

	t.Run("omitting a disabled flag plans true", func(t *testing.T) {
		t.Parallel()
		state := prior(testMemberRoles(testMemberRole(viewer, types.BoolValue(false))))
		config := configured(testMemberRoles(testMemberRole(viewer, types.BoolNull())))
		_, planned := planMemberRolesThroughFramework(t, &state, config, state)
		if got := memberRolesFromState(t, planned.Roles); len(got) != 1 || !got["urn:role:viewer"] {
			t.Fatalf("planned roles = %v, want viewer enabled", got)
		}
		if !planned.ID.Equal(state.ID) || !planned.Assignments.IsUnknown() {
			t.Fatalf("expected an in-place update with known id and unknown assignments, got id=%s assignments=%s", planned.ID, planned.Assignments)
		}
	})

	t.Run("explicit false is kept", func(t *testing.T) {
		t.Parallel()
		state := prior(testMemberRoles(testMemberRole(viewer, types.BoolValue(true))))
		config := configured(testMemberRoles(testMemberRole(viewer, types.BoolValue(false))))
		proposed := state
		proposed.Roles = config.Roles
		_, planned := planMemberRolesThroughFramework(t, &state, config, proposed)
		if got := memberRolesFromState(t, planned.Roles); len(got) != 1 || got["urn:role:viewer"] {
			t.Fatalf("planned roles = %v, want viewer disabled", got)
		}
	})
}

func TestMemberRolesResourceValidateConfig(t *testing.T) {
	t.Parallel()

	many := func(count int) types.Set {
		elements := make([]attr.Value, 0, count)
		for i := range count {
			elements = append(elements, testMemberRole(types.StringValue(fmt.Sprintf("urn:role:r%03d", i)), types.BoolNull()))
		}
		return testMemberRoles(elements...)
	}

	for _, test := range []struct {
		name  string
		roles types.Set
		want  string
	}{
		{name: "distinct roles", roles: testMemberRoles(
			testMemberRole(types.StringValue("urn:role:viewer"), types.BoolNull()),
			testMemberRole(types.StringValue("urn:role:editor"), types.BoolValue(false)),
		)},
		{name: "empty set", roles: testMemberRoles()},
		{name: "unknown set", roles: types.SetUnknown(types.ObjectType{AttrTypes: memberRoleAttributeTypes()})},
		{name: "unknown urn", roles: testMemberRoles(
			testMemberRole(types.StringValue("urn:role:viewer"), types.BoolNull()),
			testMemberRole(types.StringUnknown(), types.BoolNull()),
		)},
		{name: "duplicate urn with different enabled", roles: testMemberRoles(
			testMemberRole(types.StringValue("urn:role:viewer"), types.BoolNull()),
			testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(false)),
		), want: "Duplicate Project Member Roles role"},
		{name: "duplicate urn with outer whitespace", roles: testMemberRoles(
			testMemberRole(types.StringValue("urn:role:viewer"), types.BoolNull()),
			testMemberRole(types.StringValue(" urn:role:viewer "), types.BoolNull()),
		), want: "Duplicate Project Member Roles role"},
		{name: "one hundred roles", roles: many(100)},
		{name: "one hundred and one roles", roles: many(101), want: "Too many Project Member Roles roles"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := newMemberRolesResourceModel()
			model.Member = reference.Value(memberCriterionUsername, types.StringValue("alice"))
			model.Roles = test.roles
			config := tfsdk.Config(memberRolesPlan(t, model))

			var response resource.ValidateConfigResponse
			(&MemberRolesResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: config}, &response)
			if test.want == "" {
				if response.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
				}
				return
			}
			if response.Diagnostics.ErrorsCount() != 1 || response.Diagnostics[0].Summary() != test.want {
				t.Fatalf("expected one %q diagnostic, got %v", test.want, response.Diagnostics)
			}
			if !strings.Contains(response.Diagnostics[0].Detail(), "urn:role:viewer") && test.want == "Duplicate Project Member Roles role" {
				t.Fatalf("expected the duplicate URN in the detail, got %q", response.Diagnostics[0].Detail())
			}
		})
	}
}

// Create replaces the roles the member already holds (AC10), sends only roles
// with urn and enabled on every element, and maps the response into state.
func TestMemberRolesResourceCreateResolvesUsernameAndSendsOnlyRoles(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(
		t,
		memberRolesAssignment(0x01, "urn:role:editor", true),
		memberRolesAssignment(0x05, "urn:role:billing", true),
	)
	backend.others = []projectsdk.ProjectMemberSchema{{Id: core.UUID{0x99}, User: projectsdk.NestedMemberUserSchema{Username: "alice2"}}}
	r := backend.resource(t)

	plan := memberRolesPlan(t, newMemberRolesPlanModel(" alice ", testMemberRoles(
		testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)),
		testMemberRole(types.StringValue("urn:role:prj-demo:disabled"), types.BoolValue(false)),
		testMemberRole(types.StringValue("urn:role:editor"), types.BoolNull()),
	)))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}

	requests := backend.recorded()
	if backend.methods() != "GET,PATCH" {
		t.Fatalf("expected a list and a PATCH, got %s", backend.methods())
	}
	if got := requests[0].Query.Get("username"); got != "alice" {
		t.Fatalf("username filter = %q, want the trimmed username", got)
	}
	if want := "/v2/projects/" + memberRolesTestProjectID.String() + "/members/" + memberRolesTestMemberID.String() + "/"; requests[1].Path != want {
		t.Fatalf("PATCH path = %s, want %s", requests[1].Path, want)
	}
	roles := decodePatchRoles(t, requests[1].Body)
	want := []map[string]any{
		{"urn": "urn:role:editor", "enabled": true},
		{"urn": "urn:role:prj-demo:disabled", "enabled": false},
		{"urn": "urn:role:viewer", "enabled": true},
	}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("PATCH roles = %v, want %v", roles, want)
	}

	state := memberRolesStateModel(t, response.State)
	if state.ID.ValueString() != memberRolesTestMemberID.String() || state.MemberUserID.ValueString() != memberRolesTestUserID.String() ||
		state.MemberEmail.ValueString() != "alice@example.com" || state.MemberIsAdmin.ValueBool() ||
		state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || state.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("unexpected computed state: %#v", state.MemberRolesModel)
	}
	if got := reference.Get(state.Member, memberCriterionUsername).ValueString(); got != " alice " {
		t.Fatalf("member.username = %q, want the configured form", got)
	}
	if got := memberRolesFromState(t, state.Roles); len(got) != 3 || !got["urn:role:editor"] || got["urn:role:prj-demo:disabled"] || !got["urn:role:viewer"] {
		t.Fatalf("state roles = %v", got)
	}
	assignments := memberRolesAssignmentsFromState(t, state.Assignments)
	if len(assignments) != 3 || assignments[0].ID.ValueString() != (core.UUID{0x01}).String() {
		t.Fatalf("expected the editor assignment to keep its ID, got %#v", assignments)
	}
	for _, assignment := range assignments {
		if assignment.URN.ValueString() == "urn:role:billing" {
			t.Fatalf("expected the unconfigured billing role to be removed, got %#v", assignments)
		}
	}
}

// TFIAM2_64: the API trims role.urn, so the request sends the trimmed URN and
// state keeps the configured spelling through Create and the next Read.
func TestMemberRolesResourceCreateTrimsRoleURNAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	const padded = " urn:role:viewer\n"
	backend := newMemberRolesBackend(t)
	r := backend.resource(t)
	plan := memberRolesPlan(t, newMemberRolesPlanModel("alice", testMemberRoles(
		testMemberRole(types.StringValue(padded), types.BoolValue(true)),
	)))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}

	requests := backend.recorded()
	roles := decodePatchRoles(t, requests[len(requests)-1].Body)
	if want := []map[string]any{{"urn": "urn:role:viewer", "enabled": true}}; fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("PATCH roles = %v, want %v", roles, want)
	}
	created := memberRolesStateModel(t, response.State)
	if !created.Roles.Equal(memberRolesStateModel(t, tfsdk.State(plan)).Roles) {
		t.Fatalf("state roles = %v, want the planned roles", created.Roles)
	}

	read := resource.ReadResponse{State: response.State}
	r.Read(context.Background(), resource.ReadRequest{State: response.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", read.Diagnostics)
	}
	if got := memberRolesFromState(t, memberRolesStateModel(t, read.State).Roles); len(got) != 1 || !got[padded] {
		t.Fatalf("read roles = %v, want the configured URN %q", got, padded)
	}
}

func TestMemberRolesResourceCreateRejectsUnknownRoles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		roles types.Set
	}{
		{"unknown set", types.SetUnknown(types.ObjectType{AttrTypes: memberRoleAttributeTypes()})},
		{"unknown urn", testMemberRoles(testMemberRole(types.StringUnknown(), types.BoolValue(true)))},
		{"unknown enabled", testMemberRoles(testMemberRole(types.StringValue("urn:role:viewer"), types.BoolUnknown()))},
		{"blank urn", testMemberRoles(testMemberRole(types.StringValue(" "), types.BoolValue(true)))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := newMemberRolesBackend(t)
			plan := memberRolesPlan(t, newMemberRolesPlanModel("alice", test.roles))
			response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			backend.resource(t).Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected a diagnostic")
			}
			if len(backend.recorded()) != 0 || !response.State.Raw.IsNull() {
				t.Fatalf("expected no request and no state, got requests=%s", backend.methods())
			}
		})
	}
}

// A service account or a non-member is absent from the member list (AC14).
func TestMemberRolesResourceCreateReportsMissingMemberWithoutPatch(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t)
	backend.others = []projectsdk.ProjectMemberSchema{{Id: core.UUID{0x99}, User: projectsdk.NestedMemberUserSchema{Username: "svc-bobby"}}}
	plan := memberRolesPlan(t, newMemberRolesPlanModel("bob", testMemberRoles(
		testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)),
	)))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	backend.resource(t).Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)

	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error resolving Project Member Roles member" ||
		!strings.Contains(response.Diagnostics[0].Detail(), `project member was not found matching criteria (username="bob")`) {
		t.Fatalf("expected a not-found diagnostic, got %v", response.Diagnostics)
	}
	if backend.methods() != "GET" || !response.State.Raw.IsNull() {
		t.Fatalf("expected only the list request and no state, got %s", backend.methods())
	}
}

// AC11 (role missing or from another project) and AC15 (the PAT's own
// membership) come back from PATCH as 400 and 409.
func TestMemberRolesResourceCreateReportsBackendErrorsWithoutState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  int
		message string
	}{
		{"unknown role", http.StatusBadRequest, "Role urn:role:missing does not exist."},
		{"own membership", http.StatusConflict, "Project users can not update their own role"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := newMemberRolesBackend(t)
			backend.failMethod = http.MethodPatch
			backend.failStatus = test.status
			backend.failBody = fmt.Sprintf(`{"errors":[{"location":"roles","code":"invalid","message":%q}]}`, test.message)

			plan := memberRolesPlan(t, newMemberRolesPlanModel("alice", testMemberRoles(
				testMemberRole(types.StringValue("urn:role:missing"), types.BoolValue(true)),
			)))
			response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			backend.resource(t).Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)

			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error creating Project Member Roles" ||
				!strings.Contains(response.Diagnostics[0].Detail(), test.message) {
				t.Fatalf("expected the backend message in the diagnostic, got %v", response.Diagnostics)
			}
			if !response.State.Raw.IsNull() {
				t.Fatal("expected no state after a failed create")
			}
		})
	}
}

// Update sends the whole planned set when roles change (AC3), keeps the
// member ID and the assignment IDs of roles that stay, and only reads the
// member when roles are unchanged.
func TestMemberRolesResourceUpdateSendsFullRolesOnlyWhenChanged(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(
		t,
		memberRolesAssignment(0x01, "urn:role:viewer", true),
		memberRolesAssignment(0x02, "urn:role:editor", true),
	)
	r := backend.resource(t)
	priorModel := newMemberRolesResourceModel()
	priorModel.Member = reference.Value(memberCriterionUsername, types.StringValue("alice"))
	populateMemberRolesResourceState(&backend.member, &priorModel)
	prior := memberRolesState(t, priorModel)

	unchanged := memberRolesPlan(t, priorModel)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: prior.Schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: unchanged, State: prior}, &response)
	if response.Diagnostics.HasError() || backend.methods() != "GET" {
		t.Fatalf("expected only a GET for unchanged roles, got %s %v", backend.methods(), response.Diagnostics)
	}

	planModel := priorModel
	planModel.Assignments = types.ListUnknown(types.ObjectType{AttrTypes: memberRoleAssignmentAttributeTypes()})
	planModel.Roles = testMemberRoles(
		testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)),
		testMemberRole(types.StringValue("urn:role:editor"), types.BoolValue(false)),
		testMemberRole(types.StringValue("urn:role:prj-demo:disabled"), types.BoolValue(true)),
	)
	response = resource.UpdateResponse{State: tfsdk.State{Schema: prior.Schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: memberRolesPlan(t, planModel), State: prior}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	requests := backend.recorded()
	if backend.methods() != "GET,PATCH" {
		t.Fatalf("expected a PATCH for changed roles, got %s", backend.methods())
	}
	roles := decodePatchRoles(t, requests[1].Body)
	want := []map[string]any{
		{"urn": "urn:role:editor", "enabled": false},
		{"urn": "urn:role:prj-demo:disabled", "enabled": true},
		{"urn": "urn:role:viewer", "enabled": true},
	}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("PATCH roles = %v, want %v", roles, want)
	}

	state := memberRolesStateModel(t, response.State)
	if state.ID.ValueString() != memberRolesTestMemberID.String() {
		t.Fatalf("id changed to %s", state.ID)
	}
	ids := map[string]string{}
	for _, assignment := range memberRolesAssignmentsFromState(t, state.Assignments) {
		ids[assignment.URN.ValueString()] = assignment.ID.ValueString()
	}
	if ids["urn:role:viewer"] != (core.UUID{0x01}).String() || ids["urn:role:editor"] != (core.UUID{0x02}).String() {
		t.Fatalf("assignment IDs of remaining roles changed: %v", ids)
	}
}

func TestMemberRolesResourceUpdateIgnoresWhitespaceOnlyEdits(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t, memberRolesAssignment(0x01, "urn:role:viewer", true))
	priorModel := newMemberRolesResourceModel()
	priorModel.Member = reference.Value(memberCriterionUsername, types.StringValue("alice"))
	populateMemberRolesResourceState(&backend.member, &priorModel)
	prior := memberRolesState(t, priorModel)

	planModel := priorModel
	planModel.Member = reference.Value(memberCriterionUsername, types.StringValue(" alice "))
	planModel.Roles = testMemberRoles(testMemberRole(types.StringValue(" urn:role:viewer\n"), types.BoolValue(true)))
	response := resource.UpdateResponse{State: tfsdk.State{Schema: prior.Schema}}
	backend.resource(t).Update(context.Background(), resource.UpdateRequest{Plan: memberRolesPlan(t, planModel), State: prior}, &response)
	if response.Diagnostics.HasError() || backend.methods() != "GET" {
		t.Fatalf("expected only a GET for a whitespace-only edit, got %s %v", backend.methods(), response.Diagnostics)
	}
	state := memberRolesStateModel(t, response.State)
	if got := reference.Get(state.Member, memberCriterionUsername).ValueString(); got != " alice " {
		t.Fatalf("member.username = %q, want the configured spelling", got)
	}
	if got := memberRolesFromState(t, state.Roles); len(got) != 1 || !got[" urn:role:viewer\n"] {
		t.Fatalf("roles = %v, want the configured URN spelling", got)
	}
}

// roles = [] removes every role (AC6) with an explicit empty array.
func TestMemberRolesResourceUpdateClearsRoles(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t, memberRolesAssignment(0x01, "urn:role:viewer", true))
	priorModel := newMemberRolesResourceModel()
	populateMemberRolesResourceState(&backend.member, &priorModel)
	prior := memberRolesState(t, priorModel)

	planModel := priorModel
	planModel.Roles = testMemberRoles()
	response := resource.UpdateResponse{State: tfsdk.State{Schema: prior.Schema}}
	backend.resource(t).Update(context.Background(), resource.UpdateRequest{Plan: memberRolesPlan(t, planModel), State: prior}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	requests := backend.recorded()
	if len(requests) != 1 || string(requests[0].Body) != `{"roles":[]}` {
		t.Fatalf(`expected one PATCH with {"roles":[]}, got %v`, requests)
	}
	state := memberRolesStateModel(t, response.State)
	if len(state.Roles.Elements()) != 0 || len(state.Assignments.Elements()) != 0 || state.Roles.IsNull() {
		t.Fatalf("expected known empty roles and assignments, got roles=%s assignments=%s", state.Roles, state.Assignments)
	}
}

func TestMemberRolesResourceUpdateReportsErrors(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t)
	backend.failMethod = http.MethodPatch
	backend.failStatus = http.StatusConflict
	backend.failBody = `{"errors":[{"message":"Project users can not update their own role"}]}`
	priorModel := newMemberRolesResourceModel()
	populateMemberRolesResourceState(&backend.member, &priorModel)
	prior := memberRolesState(t, priorModel)
	planModel := priorModel
	planModel.Roles = testMemberRoles(testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)))

	response := resource.UpdateResponse{State: prior}
	backend.resource(t).Update(context.Background(), resource.UpdateRequest{Plan: memberRolesPlan(t, planModel), State: prior}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error updating Project Member Roles" ||
		!strings.Contains(response.Diagnostics[0].Detail(), "can not update their own role") {
		t.Fatalf("expected the backend message, got %v", response.Diagnostics)
	}
}

func TestMemberRolesResourceUpdateRejectsAnotherMember(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t)
	priorModel := newMemberRolesResourceModel()
	populateMemberRolesResourceState(&backend.member, &priorModel)
	prior := memberRolesState(t, priorModel)
	backend.member.Id = core.UUID{0x77}

	planModel := priorModel
	planModel.Roles = testMemberRoles(testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(true)))
	response := resource.UpdateResponse{State: prior}
	backend.resource(t).Update(context.Background(), resource.UpdateRequest{Plan: memberRolesPlan(t, planModel), State: prior}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "with project member") {
		t.Fatalf("expected a member mismatch diagnostic, got %v", response.Diagnostics)
	}
}

// Delete keeps the member and removes every role (AC9) with an explicit
// empty array, and a member that already left counts as deleted.
func TestMemberRolesResourceDeleteSendsEmptyRoles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success"},
		{name: "not found", status: http.StatusNotFound},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := newMemberRolesBackend(t, memberRolesAssignment(0x01, "urn:role:viewer", true))
			if test.status != 0 {
				backend.failMethod = http.MethodPatch
				backend.failStatus = test.status
				backend.failBody = `{"detail":"backend response"}`
			}
			stateModel := newMemberRolesResourceModel()
			populateMemberRolesResourceState(&backend.member, &stateModel)
			state := memberRolesState(t, stateModel)

			response := resource.DeleteResponse{State: state}
			backend.resource(t).Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() != test.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", response.Diagnostics, test.wantErr)
			}
			if test.wantErr && response.Diagnostics[0].Summary() != "Error deleting Project Member Roles" {
				t.Fatalf("unexpected summary: %v", response.Diagnostics)
			}
			requests := backend.recorded()
			if len(requests) != 1 || requests[0].Method != http.MethodPatch || string(requests[0].Body) != `{"roles":[]}` {
				t.Fatalf(`expected one PATCH with {"roles":[]}, got %v`, requests)
			}
		})
	}
}

// Read maps every response field, sorts assignments by URN, and keeps an
// empty email and a disabled role or assignment.
func TestMemberRolesResourceReadMapsEveryField(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(
		t,
		memberRolesAssignment(0x03, "urn:role:viewer", true),
		memberRolesAssignment(0x01, "urn:role:prj-demo:disabled", false),
		memberRolesAssignment(0x02, "urn:role:editor", true),
	)
	backend.member.User.Email = ""
	backend.member.IsAdmin = true
	stateModel := newMemberRolesResourceModel()
	stateModel.ID = types.StringValue(memberRolesTestMemberID.String())
	state := memberRolesState(t, stateModel)

	response := resource.ReadResponse{State: state}
	backend.resource(t).Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	got := memberRolesStateModel(t, response.State)
	if reference.Get(got.Member, memberCriterionUsername).ValueString() != "alice" {
		t.Fatalf("member.username = %s, want alice", got.Member)
	}
	if got.MemberEmail.IsNull() || got.MemberEmail.ValueString() != "" || !got.MemberIsAdmin.ValueBool() ||
		got.MemberUserID.ValueString() != memberRolesTestUserID.String() ||
		got.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || got.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("unexpected member fields: %#v", got.MemberRolesModel)
	}
	if roles := memberRolesFromState(t, got.Roles); len(roles) != 3 || roles["urn:role:prj-demo:disabled"] || !roles["urn:role:viewer"] {
		t.Fatalf("state roles = %v", roles)
	}

	assignments := memberRolesAssignmentsFromState(t, got.Assignments)
	var urns []string
	for _, assignment := range assignments {
		urns = append(urns, assignment.URN.ValueString())
	}
	if !slices.IsSorted(urns) || len(urns) != 3 {
		t.Fatalf("assignments must be sorted by URN, got %v", urns)
	}
	disabled := assignments[1]
	if disabled.URN.ValueString() != "urn:role:prj-demo:disabled" || disabled.ID.ValueString() != (core.UUID{0x01}).String() ||
		disabled.Enabled.ValueBool() || disabled.RoleEnabled.ValueBool() || disabled.Type.ValueString() != "custom" ||
		disabled.DisplayName.ValueString() != "Disabled" || disabled.Description.IsNull() || disabled.Description.ValueString() != "" {
		t.Fatalf("unexpected disabled assignment: %#v", disabled)
	}
	editor := assignments[0]
	if editor.DisplayName.ValueString() != "Editor" || editor.Description.ValueString() != "Edit access" ||
		editor.Type.ValueString() != "predefined" || !editor.Enabled.ValueBool() || !editor.RoleEnabled.ValueBool() {
		t.Fatalf("unexpected editor assignment: %#v", editor)
	}
}

// A role added outside Terraform shows up in roles, so the next plan removes
// it (AC7).
func TestMemberRolesResourceReadReportsRolesAddedOutsideTerraform(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t, memberRolesAssignment(0x01, "urn:role:viewer", true))
	stateModel := newMemberRolesResourceModel()
	stateModel.Member = reference.Value(memberCriterionUsername, types.StringValue("alice"))
	populateMemberRolesResourceState(&backend.member, &stateModel)
	state := memberRolesState(t, stateModel)
	backend.member.Roles = append(backend.member.Roles, memberRolesAssignment(0x02, "urn:role:editor", true))

	response := resource.ReadResponse{State: state}
	backend.resource(t).Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	got := memberRolesStateModel(t, response.State)
	if roles := memberRolesFromState(t, got.Roles); len(roles) != 2 || !roles["urn:role:editor"] {
		t.Fatalf("expected the outside role in state, got %v", roles)
	}
	if got.Roles.Equal(stateModel.Roles) {
		t.Fatal("expected roles to differ from the configured set")
	}
}

func TestMemberRolesResourceReadPreservesConfiguredUsername(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t)
	for _, test := range []struct {
		configured string
		want       string
	}{
		{" alice ", " alice "},
		{"alice", "alice"},
		{"Alice", "alice"},
	} {
		state := newMemberRolesResourceModel()
		state.Member = reference.Value(memberCriterionUsername, types.StringValue(test.configured))
		populateMemberRolesResourceState(&backend.member, &state)
		if got := reference.Get(state.Member, memberCriterionUsername).ValueString(); got != test.want {
			t.Errorf("configured %q: member.username = %q, want %q", test.configured, got, test.want)
		}
	}
}

// A member that left the project disappears from state (AC7).
func TestMemberRolesResourceReadRemovesMissingMember(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  int
		removed bool
	}{
		{"not found", http.StatusNotFound, true},
		{"server error", http.StatusInternalServerError, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := newMemberRolesBackend(t)
			backend.failMethod = http.MethodGet
			backend.failStatus = test.status
			backend.failBody = `{"detail":"backend response"}`
			stateModel := newMemberRolesResourceModel()
			stateModel.ID = types.StringValue(memberRolesTestMemberID.String())
			state := memberRolesState(t, stateModel)

			response := resource.ReadResponse{State: state}
			backend.resource(t).Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if test.removed {
				if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
					t.Fatalf("expected the resource to be removed, got %v", response.Diagnostics)
				}
				return
			}
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error reading Project Member Roles" {
				t.Fatalf("expected a read diagnostic, got %v", response.Diagnostics)
			}
		})
	}
}

func TestMemberRolesResourceRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	stateModel := newMemberRolesResourceModel()
	stateModel.ID = types.StringValue("not-a-uuid")
	state := memberRolesState(t, stateModel)
	r := &MemberRolesResource{projectID: memberRolesTestProjectID}

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	updateResponse := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: tfsdk.Plan(state), State: state}, &updateResponse)
	for name, diags := range map[string]bool{
		"read": readResponse.Diagnostics.HasError(), "delete": deleteResponse.Diagnostics.HasError(), "update": updateResponse.Diagnostics.HasError(),
	} {
		if !diags {
			t.Errorf("%s must reject an invalid state ID", name)
		}
	}
}

func TestMemberRolesResourceImportState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		id      string
		want    string
		wantErr bool
	}{
		{name: "uuid", id: memberRolesTestMemberID.String(), want: memberRolesTestMemberID.String()},
		{name: "padded uuid", id: "  " + memberRolesTestMemberID.String() + " ", want: memberRolesTestMemberID.String()},
		{name: "username", id: "alice", wantErr: true},
		{name: "empty", id: "", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := resource.ImportStateResponse{State: memberRolesState(t, newMemberRolesResourceModel())}
			(&MemberRolesResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: test.id}, &response)
			if response.Diagnostics.HasError() != test.wantErr {
				t.Fatalf("diagnostics = %v, want error %t", response.Diagnostics, test.wantErr)
			}
			if test.wantErr {
				return
			}
			var id types.String
			response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
			if id.ValueString() != test.want {
				t.Fatalf("imported id = %q, want %q", id.ValueString(), test.want)
			}
		})
	}
}

// Import followed by Read fills member and roles from the backend, so an
// import configuration that matches the backend plans no change (AC8).
func TestMemberRolesResourceImportThenReadFillsMemberAndRoles(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t, memberRolesAssignment(0x01, "urn:role:viewer", false))
	response := resource.ImportStateResponse{State: memberRolesState(t, newMemberRolesResourceModel())}
	(&MemberRolesResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: memberRolesTestMemberID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: response.State}
	backend.resource(t).Read(context.Background(), resource.ReadRequest{State: response.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	got := memberRolesStateModel(t, readResponse.State)
	want := testMemberRoles(testMemberRole(types.StringValue("urn:role:viewer"), types.BoolValue(false)))
	if reference.Get(got.Member, memberCriterionUsername).ValueString() != "alice" || !got.Roles.Equal(want) {
		t.Fatalf("unexpected imported state: member=%s roles=%s", got.Member, got.Roles)
	}
}

func TestMemberRolesResourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()

	var metadata resource.MetadataResponse
	NewMemberRolesResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_project_member_roles" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	r := &MemberRolesResource{}
	var nilResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResponse.Diagnostics)
	}
	var invalidResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	client, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Project: client, ProjectID: memberRolesTestProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() || r.client != client || r.projectID != memberRolesTestProjectID {
		t.Fatalf("expected the project client and project ID to be configured: %v", validResponse.Diagnostics)
	}
}
