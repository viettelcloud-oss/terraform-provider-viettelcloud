package project

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func memberRolesDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()

	var response datasource.SchemaResponse
	(&MemberRolesDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func memberRolesDataSourceConfig(t *testing.T, username types.String) tfsdk.Config {
	t.Helper()
	model := MemberRolesDataSourceModel{
		MemberRolesModel: MemberRolesModel{
			Assignments: types.ListNull(types.ObjectType{AttrTypes: memberRoleAssignmentAttributeTypes()}),
		},
		Username: username,
		Roles:    types.SetNull(types.ObjectType{AttrTypes: memberRoleDataSourceAttributeTypes()}),
	}
	state := tfsdk.State{Schema: memberRolesDataSourceSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	return tfsdk.Config(state)
}

func readMemberRolesDataSource(t *testing.T, backend *memberRolesBackend, username types.String) datasource.ReadResponse {
	t.Helper()
	config := memberRolesDataSourceConfig(t, username)
	d := &MemberRolesDataSource{client: backend.client(t), projectID: memberRolesTestProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
	return response
}

func TestMemberRolesDataSourceSchema(t *testing.T) {
	t.Parallel()

	schema := memberRolesDataSourceSchema(t)
	for name, attribute := range schema.Attributes {
		if name == "username" {
			if !attribute.IsOptional() || !attribute.IsComputed() {
				t.Errorf("username must be optional and computed")
			}
			continue
		}
		if !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
			t.Errorf("%s must be computed only", name)
		}
	}
	memberRolesDataSourceConfig(t, types.StringNull())
}

// Lookup by username returns every attribute of the member (AC16).
func TestMemberRolesDataSourceReadByUsername(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(
		t,
		memberRolesAssignment(0x02, "urn:role:viewer", true),
		memberRolesAssignment(0x01, "urn:role:prj-demo:disabled", false),
	)
	backend.member.User.Email = ""
	backend.others = []projectsdk.ProjectMemberSchema{{Id: core.UUID{0x99}, User: projectsdk.NestedMemberUserSchema{Username: "alice2"}}}

	response := readMemberRolesDataSource(t, backend, types.StringValue(" alice "))
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	requests := backend.recorded()
	if len(requests) != 1 || requests[0].Method != http.MethodGet || requests[0].Query.Get("username") != "alice" {
		t.Fatalf("expected one list request filtered by the trimmed username, got %v", requests)
	}

	var state MemberRolesDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.Username.ValueString() != " alice " || state.ID.ValueString() != memberRolesTestMemberID.String() ||
		state.MemberUserID.ValueString() != memberRolesTestUserID.String() || state.MemberEmail.IsNull() || state.MemberEmail.ValueString() != "" ||
		state.MemberIsAdmin.ValueBool() || state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || state.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("unexpected member fields: username=%s %#v", state.Username, state.MemberRolesModel)
	}
	roles := map[string]bool{}
	for _, element := range state.Roles.Elements() {
		attributes := element.(types.Object).Attributes()
		roles[attributes["role_urn"].(types.String).ValueString()] = attributes["enabled"].(types.Bool).ValueBool()
	}
	if len(roles) != 2 || !roles["urn:role:viewer"] || roles["urn:role:prj-demo:disabled"] {
		t.Fatalf("unexpected roles: %v", roles)
	}
	assignments := memberRolesAssignmentsFromState(t, state.Assignments)
	if len(assignments) != 2 || assignments[0].URN.ValueString() != "urn:role:prj-demo:disabled" || assignments[0].RoleEnabled.ValueBool() {
		t.Fatalf("expected assignments sorted by URN with the disabled role first, got %#v", assignments)
	}
}

func TestMemberRolesDataSourceReadRejectsCriteria(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		username types.String
		summary  string
		detail   string
		requests int
	}{
		{name: "missing", username: types.StringNull(), summary: "Missing Project Member Roles lookup criteria", detail: "Specify username"},
		{name: "whitespace", username: types.StringValue("  "), summary: "Invalid Project Member Roles lookup criteria", detail: "whitespace-only"},
		// The backend filter is icontains, so it returns alice2 for alice (AC17).
		{
			name: "partial match only", username: types.StringValue("alic"), summary: "Error resolving Project Member Roles",
			detail: `project member was not found matching criteria (username="alic")`, requests: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := newMemberRolesBackend(t)
			response := readMemberRolesDataSource(t, backend, test.username)
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != test.summary ||
				!strings.Contains(response.Diagnostics[0].Detail(), test.detail) {
				t.Fatalf("expected %q containing %q, got %v", test.summary, test.detail, response.Diagnostics)
			}
			if got := len(backend.recorded()); got != test.requests {
				t.Fatalf("requests = %d, want %d", got, test.requests)
			}
			if !response.State.Raw.IsNull() {
				t.Fatal("expected no state")
			}
		})
	}
}

func TestMemberRolesDataSourceReadReportsListError(t *testing.T) {
	t.Parallel()

	backend := newMemberRolesBackend(t)
	backend.failMethod = http.MethodGet
	backend.failStatus = http.StatusInternalServerError
	backend.failBody = `{"detail":"backend response"}`
	response := readMemberRolesDataSource(t, backend, types.StringValue("alice"))
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "backend response") {
		t.Fatalf("expected the backend error, got %v", response.Diagnostics)
	}
}

func TestMemberRolesDataSourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()

	var metadata datasource.MetadataResponse
	NewMemberRolesDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_project_member_roles" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	d := &MemberRolesDataSource{}
	var nilResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResponse.Diagnostics)
	}
	var invalidResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	client, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Project: client, ProjectID: memberRolesTestProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() || d.client != client || d.projectID != memberRolesTestProjectID {
		t.Fatalf("expected the project client and project ID to be configured: %v", validResponse.Diagnostics)
	}
}
