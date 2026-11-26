package project

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func customRoleDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&CustomRoleDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// readCustomRoleDataSource runs Read for a configuration with the given
// lookup criteria; a nil criterion stays null.
func readCustomRoleDataSource(t *testing.T, client *projectsdk.Client, id, name *string) datasource.ReadResponse {
	t.Helper()
	schema := customRoleDataSourceSchema(t)
	model := CustomRoleModel{Actions: types.SetNull(reference.ObjectType(reference.URN))}
	if id != nil {
		model.ID = types.StringValue(*id)
	}
	if name != nil {
		model.Name = types.StringValue(*name)
	}
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	d := &CustomRoleDataSource{client: client, projectID: customRoleTestProjectID}
	d.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Schema: schema, Raw: state.Raw}}, &response)
	return response
}

func customRoleDataSourceState(t *testing.T, response datasource.ReadResponse) CustomRoleModel {
	t.Helper()
	var model CustomRoleModel
	if diags := response.State.Get(context.Background(), &model); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	return model
}

// unusedCustomRoleClient fails the test on any request, for lookups that must
// be rejected before the API is called.
func unusedCustomRoleClient(t *testing.T) *projectsdk.Client {
	t.Helper()
	return customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		http.NotFound(w, req)
	})
}

func TestCustomRoleDataSourceSchema(t *testing.T) {
	t.Parallel()

	schema := customRoleDataSourceSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), new(customRoleStateModel())); diags.HasError() {
		t.Fatalf("model does not match schema: %v", diags)
	}
	for name, attribute := range schema.Attributes {
		lookup := name == "id" || name == "name"
		if !attribute.IsComputed() || attribute.IsRequired() || attribute.IsOptional() != lookup {
			t.Errorf("%s: optional=%t computed=%t, want optional=%t and computed", name, attribute.IsOptional(), attribute.IsComputed(), lookup)
		}
	}
}

func TestCustomRoleDataSourceMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	d, ok := NewCustomRoleDataSource().(*CustomRoleDataSource)
	if !ok {
		t.Fatal("NewCustomRoleDataSource must return *CustomRoleDataSource")
	}
	var metadata datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_project_custom_role" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	var nilResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	var invalidResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 1}, &invalidResponse)
	if nilResponse.Diagnostics.HasError() || !invalidResponse.Diagnostics.HasError() {
		t.Fatalf("nil=%v invalid=%v, want only the invalid provider data rejected", nilResponse.Diagnostics, invalidResponse.Diagnostics)
	}
	client, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Project: client, ProjectID: customRoleTestProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() || d.client != client || d.projectID != customRoleTestProjectID {
		t.Fatalf("expected the project client and project ID to be configured: %v", validResponse.Diagnostics)
	}
}

func TestCustomRoleDataSourceRejectsInvalidCriteria(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		id       *string
		roleName *string
		summary  string
	}{
		{"no criteria", nil, nil, "Missing Custom Role lookup criteria"},
		{"blank id", new("  "), nil, "Invalid Custom Role lookup criteria"},
		{"blank name", nil, new(" \t"), "Invalid Custom Role lookup criteria"},
		{"invalid uuid", new("not-a-uuid"), nil, "Invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response := readCustomRoleDataSource(t, unusedCustomRoleClient(t), tt.id, tt.roleName)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != tt.summary {
				t.Fatalf("diagnostics = %v, want %q", response.Diagnostics, tt.summary)
			}
		})
	}
}

func TestCustomRoleDataSourceReadByIDUsesGetCustomRole(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != customRoleTestPath {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		writeCustomRoleJSON(t, w, http.StatusOK, customRoleFixture())
	})
	id := "  " + strings.ToUpper(customRoleTestID.String()) + "\t"
	response := readCustomRoleDataSource(t, client, &id, nil)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	want := customRoleStateModel()
	want.ID = types.StringValue(id)
	if got := customRoleDataSourceState(t, response); !sameCustomRoleModel(got, want) {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
}

func TestCustomRoleDataSourceReadByNameReadsActionsFromGetCustomRole(t *testing.T) {
	t.Parallel()

	var calls []string
	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case customRoleTestRolesPath:
			if req.URL.Query().Get("name") != "ops" {
				t.Errorf("name query = %q, want ops", req.URL.Query().Get("name"))
			}
			admin := customRoleFixture()
			admin.Id, admin.Name = core.UUID{0x93}, "ops-admin"
			ops := customRoleFixture()
			ops.Name = "ops"
			writeCustomRoleJSON(t, w, http.StatusOK, projectsdk.PagedCustomRoleSchema{
				Count: 2, Results: []projectsdk.CustomRoleSchema{customRoleListItem(admin), customRoleListItem(ops)},
			})
		case customRoleTestPath:
			ops := customRoleFixture()
			ops.Name = "ops"
			writeCustomRoleJSON(t, w, http.StatusOK, ops)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	response := readCustomRoleDataSource(t, client, nil, new(" ops "))
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if want := []string{"GET " + customRoleTestRolesPath, "GET " + customRoleTestPath}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want the list followed by a get of the matched role", calls)
	}
	got := customRoleDataSourceState(t, response)
	if got.ID.ValueString() != customRoleTestID.String() || got.Name.ValueString() != " ops " ||
		got.URN.ValueString() != customRoleFixture().Urn ||
		!got.Actions.Equal(customRoleActions(customRoleTestActionA, customRoleTestActionB)) {
		t.Fatalf("state = %#v, want the role named ops with actions from the get response", got)
	}
}

func TestCustomRoleDataSourceReadByNameReportsMissingRoleOnGet(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == customRoleTestRolesPath {
			writeCustomRoleJSON(t, w, http.StatusOK, projectsdk.PagedCustomRoleSchema{
				Count: 1, Results: []projectsdk.CustomRoleSchema{customRoleListItem(customRoleFixture())},
			})
			return
		}
		writeCustomRoleError(t, w, http.StatusNotFound, "Not found")
	})
	response := readCustomRoleDataSource(t, client, nil, new("doi-van-hanh"))
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Custom Role not found" {
		t.Fatalf("diagnostics = %v, want Custom Role not found", response.Diagnostics)
	}
}

func TestCustomRoleDataSourceReadByNameReportsNoAndManyMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		names   []string
		wantErr string
	}{
		{"none", []string{"ops-admin"}, "was not found"},
		{"many", []string{"ops", "ops"}, "is ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				page := projectsdk.PagedCustomRoleSchema{Count: len(tt.names)}
				for i, name := range tt.names {
					role := customRoleFixture()
					role.Id, role.Name = core.UUID{byte(i + 1)}, name
					page.Results = append(page.Results, customRoleListItem(role))
				}
				writeCustomRoleJSON(t, w, http.StatusOK, page)
			})
			response := readCustomRoleDataSource(t, client, nil, new("ops"))
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), tt.wantErr) {
				t.Fatalf("diagnostics = %v, want %q", response.Diagnostics, tt.wantErr)
			}
		})
	}
}

func TestCustomRoleDataSourceReadByIDAndNameRequiresMatch(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != customRoleTestPath {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		writeCustomRoleJSON(t, w, http.StatusOK, customRoleFixture())
	})
	id := customRoleTestID.String()

	matching := readCustomRoleDataSource(t, client, &id, new("doi-van-hanh"))
	if matching.Diagnostics.HasError() {
		t.Fatalf("matching id and name: %v", matching.Diagnostics)
	}
	mismatched := readCustomRoleDataSource(t, client, &id, new("ops"))
	if !mismatched.Diagnostics.HasError() || mismatched.Diagnostics.Errors()[0].Summary() != "Custom Role lookup criteria do not match" {
		t.Fatalf("mismatched id and name: %v", mismatched.Diagnostics)
	}
}

func TestCustomRoleDataSourceReadByIDReportsAPIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status  int
		summary string
	}{
		{http.StatusNotFound, "Custom Role not found"},
		{http.StatusForbidden, "Error reading Custom Role"},
	}
	for _, tt := range tests {
		client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeCustomRoleError(t, w, tt.status, "failure")
		})
		id := customRoleTestID.String()
		response := readCustomRoleDataSource(t, client, &id, nil)
		if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != tt.summary {
			t.Errorf("status %d: diagnostics = %v, want %q", tt.status, response.Diagnostics, tt.summary)
		}
	}
}

func TestCustomRoleDataSourceReadByNameReportsListError(t *testing.T) {
	t.Parallel()

	client := customRoleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeCustomRoleError(t, w, http.StatusForbidden, "Permission denied")
	})
	response := readCustomRoleDataSource(t, client, nil, new("ops"))
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Error resolving Custom Role" {
		t.Fatalf("diagnostics = %v", response.Diagnostics)
	}
}

func TestPreserveConfiguredCustomRoleValues(t *testing.T) {
	t.Parallel()

	role := customRoleFixture()
	config := CustomRoleModel{
		ID:   types.StringValue(strings.ToUpper(role.Id.String())),
		Name: types.StringValue(" doi-van-hanh "),
	}
	state := customRoleStateModel()
	preserveConfiguredCustomRoleValues(role, config, &state)
	if !state.ID.Equal(config.ID) || !state.Name.Equal(config.Name) {
		t.Fatalf("state id=%s name=%s, want the configured representation", state.ID, state.Name)
	}

	other := CustomRoleModel{ID: types.StringValue(core.UUID{0x99}.String()), Name: types.StringValue("ops")}
	state = customRoleStateModel()
	preserveConfiguredCustomRoleValues(role, other, &state)
	if state.ID.ValueString() != role.Id.String() || state.Name.ValueString() != role.Name {
		t.Fatalf("state id=%s name=%s, want the backend values", state.ID, state.Name)
	}

	before := customRoleStateModel()
	state = customRoleStateModel()
	preserveConfiguredCustomRoleValues(nil, config, &state)
	if !sameCustomRoleModel(state, before) {
		t.Fatal("a nil role must leave the state unchanged")
	}
}
