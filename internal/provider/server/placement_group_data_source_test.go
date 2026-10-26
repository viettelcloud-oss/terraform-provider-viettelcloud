package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func mockPlacementGroupDataSource(name, description string, policy serversdk.PlacementGroupPolicy) *serversdk.PlacementGroupSchema {
	return &serversdk.PlacementGroupSchema{
		Id:          placementGroupTestUUID,
		Name:        name,
		Description: description,
		Policy:      new(policy),
		ServerCount: new(3),
		Region: serversdk.NestedRegionSchema{
			Id:   placementGroupTestRegion,
			Name: "vn-central",
		},
		Project: serversdk.NestedProjectSchema{
			Id:   placementGroupProjectID,
			Name: "integration",
			Slug: "integration",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func placementGroupDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()

	var response datasource.SchemaResponse
	(&PlacementGroupDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func TestPlacementGroupDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	schema := placementGroupDataSourceSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestPlacementGroupDataSourceSchemaAttributes(t *testing.T) {
	t.Parallel()

	schema := placementGroupDataSourceSchema(t)
	filters := []string{"id", "name", "policy", "region"}
	for _, name := range filters {
		attribute, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed {
			t.Fatalf("expected %s to be optional+computed, got %#v", name, schema.Attributes[name])
		}
	}
	for _, name := range []string{"region_id", "project", "created_at", "updated_at"} {
		attribute, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Computed || attribute.Optional {
			t.Fatalf("expected %s to be computed-only, got %#v", name, attribute)
		}
	}
	serverCount, ok := schema.Attributes["server_count"].(datasourceschema.Int64Attribute)
	if !ok || !serverCount.Computed || serverCount.Optional {
		t.Fatalf("expected server_count to be computed-only, got %#v", schema.Attributes["server_count"])
	}
	description, ok := schema.Attributes["description"].(datasourceschema.StringAttribute)
	if !ok || !description.Computed || description.Optional {
		t.Fatalf("expected description to be computed-only, got %#v", description)
	}
}

func TestPlacementGroupDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{}
	var resp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for invalid provider data type")
	}

	resp = datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", resp.Diagnostics)
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	resp = datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Server: client, ProjectID: placementGroupProjectID},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for valid provider data: %v", resp.Diagnostics)
	}
	if d.client != client || d.projectID != placementGroupProjectID {
		t.Fatal("expected data source to be configured with provided clients")
	}
}

func TestPlacementGroupDataSourcePreserveConfiguredValues(t *testing.T) {
	t.Parallel()

	model := PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:     types.StringValue(placementGroupTestUUID.String()),
		Name:   types.StringValue("  cluster-a  "),
		Policy: types.StringValue("affinity"),
	}, Region: types.StringValue("  vn-central  ")}
	policy := serversdk.PlacementGroupPolicyAffinity
	preserveConfiguredPlacementGroupValues(&serversdk.PlacementGroupSchema{
		Id:      placementGroupTestUUID,
		Name:    "cluster-a",
		Policy:  &policy,
		Region:  serversdk.NestedRegionSchema{Id: placementGroupTestRegion, Name: "vn-central"},
		Project: placementGroupTestProject,
	}, model, &model)
	if model.Name.ValueString() != "  cluster-a  " || model.Region.ValueString() != "  vn-central  " {
		t.Fatalf("expected configured representation to be preserved, got name=%q region=%q", model.Name.ValueString(), model.Region.ValueString())
	}
}

func TestPlacementGroupDataSourcePreserveConfiguredValuesNilPG(t *testing.T) {
	t.Parallel()

	state := PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-a"),
	}}
	preserveConfiguredPlacementGroupValues(nil, PlacementGroupDataSourceModel{}, &state)
	if state.Name.ValueString() != "cluster-a" {
		t.Fatalf("expected state to be untouched, got name=%q", state.Name.ValueString())
	}
}

func placementGroupDataSourceRequest(t *testing.T, model PlacementGroupDataSourceModel) datasource.ReadRequest {
	t.Helper()
	schema := placementGroupDataSourceSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return datasource.ReadRequest{Config: tfsdk.Config{Raw: state.Raw, Schema: schema}}
}

func TestPlacementGroupDataSourceReadRejectsInvalidPolicy(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:   types.StringValue("cluster-a"),
		Policy: types.StringValue("closest"),
	}}), &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for invalid policy")
	}
}

func TestPlacementGroupDataSourceReadRequiresFilter(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{}), &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic when no filters are provided")
	}
}

func TestPlacementGroupDataSourceReadRejectsBlankFilters(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{projectID: placementGroupProjectID}
	for _, name := range []string{"id", "name", "policy", "region"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := PlacementGroupDataSourceModel{}
			switch name {
			case "id":
				model.ID = types.StringValue("   ")
			case "name":
				model.Name = types.StringValue("   ")
			case "policy":
				model.Policy = types.StringValue("   ")
			case "region":
				model.Region = types.StringValue("   ")
			}
			response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
			d.Read(context.Background(), placementGroupDataSourceRequest(t, model), &response)
			if !response.Diagnostics.HasError() {
				t.Fatalf("expected diagnostic for blank %s filter", name)
			}
		})
	}
}

// A practitioner should learn about every blank criterion at once instead of
// fixing the configuration one diagnostic at a time.
func TestPlacementGroupDataSourceReadReportsEveryBlankFilter(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:     types.StringValue("   "),
		Name:   types.StringValue("   "),
		Policy: types.StringValue("   "),
	}, Region: types.StringValue("   ")}), &response)

	errs := response.Diagnostics.Errors()
	if len(errs) != 4 {
		t.Fatalf("expected one diagnostic per blank criterion, got %d: %v", len(errs), errs)
	}
	for _, field := range []string{"id", "name", "policy", "region"} {
		found := false
		for _, err := range errs {
			if strings.Contains(err.Detail(), field+" must not be empty") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a diagnostic naming %s, got %v", field, errs)
		}
	}
}

func TestPlacementGroupDataSourceReadByIDUsesGetPlacementGroup(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != http.MethodGet || req.URL.Path != "/v2/server/placement-groups/"+placementGroupTestUUID.String()+"/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writePlacementGroupResponse(t, w, mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity))
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue(placementGroupTestUUID.String()),
	}}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if requests != 1 {
		t.Fatalf("expected one request, got %d", requests)
	}

	var state PlacementGroupDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != placementGroupTestUUID.String() ||
		state.Name.ValueString() != "cluster-a" ||
		state.Policy.ValueString() != "affinity" ||
		state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestPlacementGroupDataSourceReadByFiltersUsesList(t *testing.T) {
	t.Parallel()

	var listRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/server/placement-groups/" {
			t.Errorf("unexpected request path: %s", req.URL.Path)
		}
		listRequests++
		if req.Method != http.MethodGet {
			t.Errorf("unexpected list method: %s", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{
			Count:   1,
			Results: []serversdk.PlacementGroupSchema{*mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity)},
		})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:   types.StringValue("cluster-a"),
		Policy: types.StringValue("affinity"),
	}}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if listRequests != 1 {
		t.Fatalf("expected one list request, got %d", listRequests)
	}
}

func TestPlacementGroupDataSourceReadTrimsCriteriaAndPreservesConfiguredValues(t *testing.T) {
	t.Parallel()

	var requests int
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/server/placement-groups/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		requests++
		query = req.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{
			Count:   1,
			Results: []serversdk.PlacementGroupSchema{*mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity)},
		})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}

	configuredID := strings.ToUpper(placementGroupTestUUID.String())
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:     types.StringValue(configuredID),
		Name:   types.StringValue("  cluster-a  "),
		Policy: types.StringValue("  affinity  "),
	}, Region: types.StringValue("  vn-central  ")}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if requests != 1 {
		t.Fatalf("expected one list request, got %d", requests)
	}
	if query.Get("id") != placementGroupTestUUID.String() ||
		query.Get("name") != "cluster-a" ||
		query.Get("policy") != "affinity" ||
		query.Get("region_name") != "vn-central" {
		t.Fatalf("expected trimmed criteria in the list query, got %v", query)
	}

	var state PlacementGroupDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != configuredID ||
		state.Name.ValueString() != "  cluster-a  " ||
		state.Policy.ValueString() != "  affinity  " ||
		state.Region.ValueString() != "  vn-central  " {
		t.Fatalf("expected the configured representation to be preserved, got %#v", state)
	}
}

func TestPlacementGroupDataSourceReadByNameReportsAmbiguousResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{
			Count: 2,
			Results: []serversdk.PlacementGroupSchema{
				*mockPlacementGroupDataSource("cluster-a", "first", serversdk.PlacementGroupPolicyAffinity),
				*mockPlacementGroupDataSource("cluster-a", "second", serversdk.PlacementGroupPolicyAntiAffinity),
			},
		})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-a"),
	}}), &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for ambiguous placement groups")
	}
	if !strings.Contains(response.Diagnostics[0].Detail(), "ambiguous") {
		t.Fatalf("expected ambiguity diagnostic, got %v", response.Diagnostics)
	}
}

func TestPlacementGroupDataSourceReadByNameReportsNoResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{Count: 0, Results: nil})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-a"),
	}}), &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic when no placement group matches")
	}
}

func TestPlacementGroupDataSourceMetadata(t *testing.T) {
	t.Parallel()

	var response datasource.MetadataResponse
	(&PlacementGroupDataSource{}).Metadata(
		context.Background(),
		datasource.MetadataRequest{ProviderTypeName: "viettelcloud"},
		&response,
	)
	if response.TypeName != "viettelcloud_placement_group" {
		t.Fatalf("expected type name viettelcloud_placement_group, got %q", response.TypeName)
	}
}

func TestPlacementGroupDataSourceReadRejectsInvalidID(t *testing.T) {
	t.Parallel()

	d := &PlacementGroupDataSource{projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue("not-a-uuid"),
	}}), &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for invalid ID")
	}
}

func TestPlacementGroupDataSourceGetByIDReportsNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"missing"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	_, diags := d.getPlacementGroupByID(context.Background(), placementGroupTestUUID)
	if !diags.HasError() || !strings.Contains(diags[0].Summary(), "not found") {
		t.Fatalf("expected not-found diagnostic, got %v", diags)
	}
}

func TestPlacementGroupDataSourceGetByIDReportsServerErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"backend down"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	_, diags := d.getPlacementGroupByID(context.Background(), placementGroupTestUUID)
	if !diags.HasError() || diags[0].Summary() != "Error reading Placement Group" {
		t.Fatalf("expected backend diagnostic, got %v", diags)
	}
}

func TestPlacementGroupDataSourceGetByFilterReportsEmptyMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{Count: 0, Results: nil})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	_, diags := d.getPlacementGroupByFilter(context.Background(), PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-a"),
	}})
	if !diags.HasError() {
		t.Fatal("expected diagnostic for empty match")
	}
}

func TestPlacementGroupDataSourcePopulatesAllFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writePlacementGroupResponse(t, w, mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity))
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	schema := placementGroupDataSourceSchema(t)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue(placementGroupTestUUID.String()),
	}}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}

	var state PlacementGroupDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	expectedCreatedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	expectedUpdatedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if state.ID.ValueString() != placementGroupTestUUID.String() ||
		state.Name.ValueString() != "cluster-a" ||
		state.Description.ValueString() != "primary" ||
		state.Policy.ValueString() != "affinity" ||
		state.Region.ValueString() != "vn-central" ||
		state.RegionID.ValueString() != placementGroupTestRegion.String() ||
		state.Project.ValueString() != "integration" ||
		state.ServerCount.ValueInt64() != 3 ||
		state.CreatedAt.ValueString() != expectedCreatedAt ||
		state.UpdatedAt.ValueString() != expectedUpdatedAt {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestPlacementGroupDataSourcePopulatesNullOptionalFields(t *testing.T) {
	t.Parallel()

	pg := mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity)
	pg.Policy = nil
	pg.ServerCount = nil

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writePlacementGroupResponse(t, w, pg)
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	schema := placementGroupDataSourceSchema(t)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue(placementGroupTestUUID.String()),
	}}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}

	var state PlacementGroupDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if !state.Policy.IsNull() || !state.ServerCount.IsNull() {
		t.Fatalf("expected null policy and server_count, got policy=%v server_count=%v", state.Policy, state.ServerCount)
	}
}

// The filter path maps the list row straight into state, so the row has to
// carry every attribute a direct read would. This pins the mapping; the
// real-API test proves the backend actually fills the row.
func TestPlacementGroupDataSourcePopulatesAllFieldsByFilter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v2/server/placement-groups/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serversdk.PagedPlacementGroupSchema{
			Count:   1,
			Results: []serversdk.PlacementGroupSchema{*mockPlacementGroupDataSource("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity)},
		})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	d := &PlacementGroupDataSource{client: client, projectID: placementGroupProjectID}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: placementGroupDataSourceSchema(t)}}
	d.Read(context.Background(), placementGroupDataSourceRequest(t, PlacementGroupDataSourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-a"),
	}}), &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}

	var state PlacementGroupDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != placementGroupTestUUID.String() ||
		state.Description.ValueString() != "primary" ||
		state.Policy.ValueString() != "affinity" ||
		state.Region.ValueString() != "vn-central" ||
		state.RegionID.ValueString() != placementGroupTestRegion.String() ||
		state.Project.ValueString() != "integration" ||
		state.ServerCount.ValueInt64() != 3 ||
		state.CreatedAt.ValueString() != time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339) ||
		state.UpdatedAt.ValueString() != time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339) {
		t.Fatalf("unexpected state from the filter path: %#v", state)
	}
}

func TestNewPlacementGroupDataSource(t *testing.T) {
	t.Parallel()

	ds := NewPlacementGroupDataSource()
	if ds == nil {
		t.Fatal("expected non-nil data source from constructor")
	}
}

func TestPopulatePlacementGroupDataSourceStateNil(t *testing.T) {
	t.Parallel()

	var state PlacementGroupDataSourceModel
	populatePlacementGroupDataSourceState(nil, &state)
	if state != (PlacementGroupDataSourceModel{}) {
		t.Fatalf("expected empty state, got %#v", state)
	}
}
