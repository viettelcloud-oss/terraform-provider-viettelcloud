package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var (
	vdcTestID        = core.UUID{0xa1}
	vdcTestRegionID  = core.UUID{0xa2}
	vdcTestZoneID    = core.UUID{0xa3}
	vdcTestProjectID = core.UUID{0xa4}
)

func vdcFixture() *serversdk.VDCSchema {
	description := nullNullable[string]()
	return &serversdk.VDCSchema{
		Id:          vdcTestID,
		Name:        "vdc-1",
		Description: description,
		Ratio:       1.2,
		Region:      serversdk.NestedRegionSchema{Id: vdcTestRegionID, Name: "vn-central-1", Description: "region"},
		Zone:        serversdk.NestedZoneSchema{Id: vdcTestZoneID, Name: "vn-central-1a", Description: "zone"},
		Stats: []serversdk.VDCStatSchema{
			{Name: "ram", Label: "RAM", Used: 1.2, Total: 64, Unit: core.NullableFromPtr(new("GiB"))},
			{Name: "cpus", Label: "CPU", Used: 3, Total: 16, Unit: core.NullableFromPtr(new("Cores"))},
			{Name: "gpus", Label: "GPU", Used: 0, Total: 0, Unit: nullNullable[string]()},
			{Name: "servers", Label: "Servers", Used: 2, Total: -1, Unit: nullNullable[string]()},
		},
	}
}

func vdcDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()

	var response datasource.SchemaResponse
	(&VDCDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// vdcTestModel returns a configuration with every result attribute null, the
// way Terraform sends a data source configuration to Read.
func vdcTestModel() VDCDataSourceModel {
	return VDCDataSourceModel{
		ID: types.StringNull(), Name: types.StringNull(), Region: types.StringNull(), Zone: types.StringNull(),
		RegionID: types.StringNull(), ZoneID: types.StringNull(), Description: types.StringNull(),
		Ratio: types.Float64Null(), Stats: types.ListNull(types.ObjectType{AttrTypes: vdcStatAttributeTypes()}),
	}
}

func vdcReadRequest(t *testing.T, model VDCDataSourceModel) datasource.ReadRequest {
	t.Helper()
	schema := vdcDataSourceSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	return datasource.ReadRequest{Config: tfsdk.Config{Raw: state.Raw, Schema: schema}}
}

// fakeVDCAPI serves the vDC and project zone endpoints and records every
// request, so that a test can assert which calls a lookup made.
type fakeVDCAPI struct {
	mu        sync.Mutex
	requests  []url.URL
	get       func(w http.ResponseWriter)
	list      []serversdk.VDCSchema
	listError int
	zones     []projectsdk.ProjectZoneSchema
}

func (f *fakeVDCAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, *req.URL)
		f.mu.Unlock()
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/vdcs/":
			if f.listError != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(f.listError)
				_, _ = w.Write([]byte(`{"errors":[{"code":"invalid","message":"zone is required"}],"request_id":"req-list"}`))
				return
			}
			writeJSON(t, w, &serversdk.PagedVDCSchema{Count: len(f.list), Results: f.list})
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v2/server/vdcs/"):
			if f.get == nil {
				t.Errorf("unexpected get: %s", req.URL.Path)
				http.NotFound(w, req)
				return
			}
			f.get(w)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+vdcTestProjectID.String()+"/zones/":
			writeJSON(t, w, &projectsdk.PagedProjectZoneSchema{Count: len(f.zones), Results: f.zones})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			http.NotFound(w, req)
		}
	}
}

func (f *fakeVDCAPI) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := make([]string, 0, len(f.requests))
	for _, requested := range f.requests {
		paths = append(paths, requested.Path)
	}
	return paths
}

func (f *fakeVDCAPI) query(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, requested := range f.requests {
		if requested.Path == path {
			return requested.Query()
		}
	}
	return nil
}

func (f *fakeVDCAPI) dataSource(t *testing.T) *VDCDataSource {
	t.Helper()
	api := httptest.NewServer(f.handler(t))
	t.Cleanup(api.Close)
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	projectClient, err := projectsdk.NewClient(api.URL, projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	return &VDCDataSource{client: client, projectClient: projectClient, projectID: vdcTestProjectID}
}

func readVDC(t *testing.T, d *VDCDataSource, model VDCDataSourceModel) (VDCDataSourceModel, datasource.ReadResponse) {
	t.Helper()
	response := datasource.ReadResponse{State: tfsdk.State{Schema: vdcDataSourceSchema(t)}}
	d.Read(context.Background(), vdcReadRequest(t, model), &response)
	var state VDCDataSourceModel
	if !response.Diagnostics.HasError() {
		if diags := response.State.Get(context.Background(), &state); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
	}
	return state, response
}

func requireVDCError(t *testing.T, response datasource.ReadResponse, summary, detail string) {
	t.Helper()
	for _, err := range response.Diagnostics.Errors() {
		if err.Summary() == summary && strings.Contains(err.Detail(), detail) {
			return
		}
	}
	t.Fatalf("expected error %q containing %q, got %v", summary, detail, response.Diagnostics)
}

func TestVDCDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	state := tfsdk.State{Schema: vdcDataSourceSchema(t)}
	if diags := state.Set(context.Background(), new(vdcTestModel())); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestVDCDataSourceSchemaAttributes(t *testing.T) {
	t.Parallel()

	schema := vdcDataSourceSchema(t)
	for _, name := range []string{"id", "name", "region", "zone"} {
		attribute, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed {
			t.Fatalf("expected %s to be optional+computed, got %#v", name, schema.Attributes[name])
		}
	}
	for _, name := range []string{"region_id", "zone_id", "description"} {
		attribute, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Computed || attribute.Optional {
			t.Fatalf("expected %s to be computed-only, got %#v", name, schema.Attributes[name])
		}
	}
	ratio, ok := schema.Attributes["ratio"].(datasourceschema.Float64Attribute)
	if !ok || !ratio.Computed || ratio.Optional {
		t.Fatalf("expected ratio to be a computed-only number, got %#v", schema.Attributes["ratio"])
	}
	stats, ok := schema.Attributes["stats"].(datasourceschema.ListNestedAttribute)
	if !ok || !stats.Computed || stats.Optional {
		t.Fatalf("expected stats to be a computed-only list, got %#v", schema.Attributes["stats"])
	}
	nested := stats.NestedObject.Attributes
	if len(nested) != 5 {
		t.Fatalf("expected five stats attributes, got %d", len(nested))
	}
	for name, attribute := range nested {
		if !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
			t.Fatalf("expected stats.%s to be computed-only, got %#v", name, attribute)
		}
	}
	for _, name := range []string{"used", "total"} {
		if _, ok := nested[name].(datasourceschema.Float64Attribute); !ok {
			t.Fatalf("expected stats.%s to be a number, got %#v", name, nested[name])
		}
	}
}

func TestVDCDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := &VDCDataSource{}
	var resp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for invalid provider data type")
	}

	resp = datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", resp.Diagnostics)
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	resp = datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Server: client, Project: projectClient, ProjectID: vdcTestProjectID},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if d.client != client || d.projectClient != projectClient || d.projectID != vdcTestProjectID {
		t.Fatal("expected the data source to keep the configured clients")
	}
}

func TestPopulateVDCDataSourceState(t *testing.T) {
	t.Parallel()

	state := vdcTestModel()
	populateVDCDataSourceState(vdcFixture(), &state)

	if state.ID.ValueString() != vdcTestID.String() || state.Name.ValueString() != "vdc-1" ||
		state.Region.ValueString() != "vn-central-1" || state.Zone.ValueString() != "vn-central-1a" ||
		state.RegionID.ValueString() != vdcTestRegionID.String() || state.ZoneID.ValueString() != vdcTestZoneID.String() {
		t.Fatalf("unexpected identity attributes: %#v", state)
	}
	if !state.Description.IsNull() {
		t.Fatalf("expected a null description to stay null, got %v", state.Description)
	}
	if state.Ratio.ValueFloat64() != 1.2 {
		t.Fatalf("expected ratio 1.2 without float32 noise, got %v", state.Ratio.ValueFloat64())
	}

	want := []struct {
		name, label string
		used, total float64
		unit        types.String
	}{
		{"ram", "RAM", 1.2, 64, types.StringValue("GiB")},
		{"cpus", "CPU", 3, 16, types.StringValue("Cores")},
		{"gpus", "GPU", 0, 0, types.StringNull()},
		{"servers", "Servers", 2, -1, types.StringNull()},
	}
	elements := state.Stats.Elements()
	if len(elements) != len(want) {
		t.Fatalf("expected %d stats, got %d", len(want), len(elements))
	}
	for i, element := range elements {
		attrs := element.(types.Object).Attributes()
		if attrs["name"].(types.String).ValueString() != want[i].name ||
			attrs["label"].(types.String).ValueString() != want[i].label ||
			attrs["used"].(types.Float64).ValueFloat64() != want[i].used ||
			attrs["total"].(types.Float64).ValueFloat64() != want[i].total ||
			!attrs["unit"].Equal(want[i].unit) {
			t.Fatalf("stats[%d] = %v, want %+v", i, attrs, want[i])
		}
	}
}

func TestPopulateVDCDataSourceStateDescription(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		description core.Nullable[string]
		want        types.String
	}{
		"absent": {core.Nullable[string]{}, types.StringNull()},
		"null":   {nullNullable[string](), types.StringNull()},
		"empty":  {core.NullableFromPtr(new("")), types.StringValue("")},
		"value":  {core.NullableFromPtr(new("dedicated")), types.StringValue("dedicated")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			vdc := vdcFixture()
			vdc.Description = tt.description
			state := vdcTestModel()
			populateVDCDataSourceState(vdc, &state)
			if !state.Description.Equal(tt.want) {
				t.Fatalf("description = %v, want %v", state.Description, tt.want)
			}
		})
	}
}

func TestPopulateVDCDataSourceStateEmptyStats(t *testing.T) {
	t.Parallel()

	vdc := vdcFixture()
	vdc.Stats = nil
	state := vdcTestModel()
	populateVDCDataSourceState(vdc, &state)
	if state.Stats.IsNull() || len(state.Stats.Elements()) != 0 {
		t.Fatalf("expected an empty known stats list, got %v", state.Stats)
	}

	populateVDCDataSourceState(nil, &state)
	if state.ID.ValueString() != vdcTestID.String() {
		t.Fatal("expected a nil vDC to leave the state unchanged")
	}
}

func TestFloat32Value(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		in   float32
		want float64
	}{
		{1.2, 1.2}, {0.1, 0.1}, {4, 4}, {-1, -1}, {0, 0}, {3.75, 3.75}, {123456.7, 123456.7},
	} {
		if got := float32Value(tt.in).ValueFloat64(); got != tt.want {
			t.Fatalf("float32Value(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestVDCDataSourceReadRequiresCriteria(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{}
	_, response := readVDC(t, api.dataSource(t), vdcTestModel())
	requireVDCError(t, response, "Missing vDC lookup criteria", "Specify at least one of id, name, region, or zone to look up a vDC.")
	if paths := api.paths(); len(paths) != 0 {
		t.Fatalf("expected no API call, got %v", paths)
	}
}

// A practitioner should learn about every blank criterion at once instead of
// fixing the configuration one diagnostic at a time.
func TestVDCDataSourceReadRejectsEveryBlankCriterion(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{}
	model := vdcTestModel()
	model.ID = types.StringValue("  ")
	model.Name = types.StringValue("  ")
	model.Region = types.StringValue("\t")
	model.Zone = types.StringValue("")
	_, response := readVDC(t, api.dataSource(t), model)
	if count := response.Diagnostics.ErrorsCount(); count != 4 {
		t.Fatalf("expected one error per blank criterion, got %d: %v", count, response.Diagnostics)
	}
	for _, field := range []string{"id", "name", "region", "zone"} {
		requireVDCError(t, response, "Invalid vDC lookup criteria", field+" must not be empty or whitespace-only.")
	}
	if paths := api.paths(); len(paths) != 0 {
		t.Fatalf("expected no API call, got %v", paths)
	}
}

func TestVDCDataSourceReadRejectsInvalidUUIDWithoutAPICall(t *testing.T) {
	t.Parallel()

	for name, configure := range map[string]func(*VDCDataSourceModel){
		"id only":      func(*VDCDataSourceModel) {},
		"id with name": func(m *VDCDataSourceModel) { m.Name = types.StringValue("vdc-1") },
		"id with zone": func(m *VDCDataSourceModel) { m.Zone = types.StringValue("vn-central-1a") },
		"id with all": func(m *VDCDataSourceModel) {
			m.Name, m.Region = types.StringValue("vdc-1"), types.StringValue("vn-central-1")
		},
		"padded": func(m *VDCDataSourceModel) { m.ID = types.StringValue(" not-a-uuid ") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := &fakeVDCAPI{}
			model := vdcTestModel()
			model.ID = types.StringValue("not-a-uuid")
			configure(&model)
			_, response := readVDC(t, api.dataSource(t), model)
			requireVDCError(t, response, "Invalid UUID", "id must be a valid UUID")
			if paths := api.paths(); len(paths) != 0 {
				t.Fatalf("expected no API call, got %v", paths)
			}
		})
	}
}

func TestVDCDataSourceReadByIDUsesGetOnly(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{get: func(w http.ResponseWriter) { writeJSON(t, w, vdcFixture()) }}
	model := vdcTestModel()
	model.ID = types.StringValue(vdcTestID.String())
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if paths := api.paths(); len(paths) != 1 || paths[0] != "/v2/server/vdcs/"+vdcTestID.String()+"/" {
		t.Fatalf("expected only GetVdc, got %v", paths)
	}
	if state.ID.ValueString() != vdcTestID.String() || state.Name.ValueString() != "vdc-1" ||
		state.Zone.ValueString() != "vn-central-1a" || len(state.Stats.Elements()) != 4 || !state.Description.IsNull() {
		t.Fatalf("unexpected state: %#v", state)
	}
}

// CONTRIBUTING.md trims every user-supplied string, a UUID included, so a
// padded ID still uses GetVdc and stays in state as configured.
func TestVDCDataSourceReadByPaddedIDKeepsConfiguredValue(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{get: func(w http.ResponseWriter) { writeJSON(t, w, vdcFixture()) }}
	model := vdcTestModel()
	model.ID = types.StringValue(" " + strings.ToUpper(vdcTestID.String()) + " ")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if paths := api.paths(); len(paths) != 1 || paths[0] != "/v2/server/vdcs/"+vdcTestID.String()+"/" {
		t.Fatalf("expected only GetVdc with the trimmed ID, got %v", paths)
	}
	if !state.ID.Equal(model.ID) {
		t.Fatalf("expected id to keep %v, got %v", model.ID, state.ID)
	}
}

func TestVDCDataSourceReadByIDNotFound(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{get: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":"not_found","message":"Not found."}]}`))
	}}
	model := vdcTestModel()
	model.ID = types.StringValue(vdcTestID.String())
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "vDC not found", "No vDC found with id "+vdcTestID.String()+".")
}

func TestVDCDataSourceReadByIDReportsOtherErrors(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{get: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":[{"code":"invalid","message":"zone is required"}],"request_id":"req-get"}`))
	}}
	model := vdcTestModel()
	model.ID = types.StringValue(vdcTestID.String())
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "Error reading vDC", "request_id=req-get")
}

// AC2: the list filter is a case-insensitive substring match, so vdc-10 and
// VDC-1 come back for name = "vdc-1" and the data source must pick vdc-1.
func TestVDCDataSourceReadByNameMatchesExactly(t *testing.T) {
	t.Parallel()

	other := *vdcFixture()
	other.Id, other.Name = core.UUID{0xb1}, "vdc-10"
	api := &fakeVDCAPI{list: []serversdk.VDCSchema{other, *vdcFixture()}}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if state.ID.ValueString() != vdcTestID.String() {
		t.Fatalf("expected vdc-1, got %s", state.ID.ValueString())
	}
	query := api.query("/v2/server/vdcs/")
	if query.Get("name") != "vdc-1" || query.Get("ordering") != "created_at" || query.Has("zone_id") || query.Has("region_id") {
		t.Fatalf("unexpected list query: %v", query)
	}

	upper := vdcTestModel()
	upper.Name = types.StringValue("VDC-1")
	_, response = readVDC(t, api.dataSource(t), upper)
	requireVDCError(t, response, "Error resolving vDC", `vDC was not found matching criteria (name="VDC-1")`)
}

// AC1 and AC2: the list row maps to the same state as the direct read.
func TestVDCDataSourceReadByFilterMatchesReadByID(t *testing.T) {
	t.Parallel()

	byIDAPI := &fakeVDCAPI{get: func(w http.ResponseWriter) { writeJSON(t, w, vdcFixture()) }}
	idModel := vdcTestModel()
	idModel.ID = types.StringValue(vdcTestID.String())
	byID, response := readVDC(t, byIDAPI.dataSource(t), idModel)
	if response.Diagnostics.HasError() {
		t.Fatalf("read by id: %v", response.Diagnostics)
	}

	byNameAPI := &fakeVDCAPI{list: []serversdk.VDCSchema{*vdcFixture()}}
	nameModel := vdcTestModel()
	nameModel.Name = types.StringValue("vdc-1")
	byName, response := readVDC(t, byNameAPI.dataSource(t), nameModel)
	if response.Diagnostics.HasError() {
		t.Fatalf("read by name: %v", response.Diagnostics)
	}
	if !byID.ID.Equal(byName.ID) || !byID.Region.Equal(byName.Region) || !byID.Zone.Equal(byName.Zone) ||
		!byID.RegionID.Equal(byName.RegionID) || !byID.ZoneID.Equal(byName.ZoneID) ||
		!byID.Description.Equal(byName.Description) || !byID.Ratio.Equal(byName.Ratio) || !byID.Stats.Equal(byName.Stats) {
		t.Fatalf("lookup by name differs from lookup by id:\n id: %#v\nname: %#v", byID, byName)
	}
}

// AC3: two vDCs share a name in different zones, and the zone selects one.
func TestVDCDataSourceReadByNameAndZoneResolvesZone(t *testing.T) {
	t.Parallel()

	other := *vdcFixture()
	other.Id = core.UUID{0xb2}
	other.Zone = serversdk.NestedZoneSchema{Id: core.UUID{0xb3}, Name: "vn-central-1b"}
	api := &fakeVDCAPI{
		list:  []serversdk.VDCSchema{other, *vdcFixture()},
		zones: []projectsdk.ProjectZoneSchema{{Id: vdcTestZoneID, Name: "vn-central-1a"}},
	}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	model.Zone = types.StringValue(" vn-central-1a ")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if state.ID.ValueString() != vdcTestID.String() {
		t.Fatalf("expected the vDC in vn-central-1a, got %s", state.ID.ValueString())
	}
	if zoneQuery := api.query("/v2/projects/" + vdcTestProjectID.String() + "/zones/"); zoneQuery.Get("name") != "vn-central-1a" {
		t.Fatalf("expected the trimmed zone name in the zone lookup, got %v", zoneQuery)
	}
	if query := api.query("/v2/server/vdcs/"); query.Get("zone_id") != vdcTestZoneID.String() || query.Get("name") != "vdc-1" {
		t.Fatalf("expected the resolved zone ID in the list query, got %v", query)
	}
}

// AC3: two vDCs share a name in different regions, and the region selects one.
func TestVDCDataSourceReadByNameAndRegion(t *testing.T) {
	t.Parallel()

	other := *vdcFixture()
	other.Id = core.UUID{0xb4}
	other.Region = serversdk.NestedRegionSchema{Id: core.UUID{0xb5}, Name: "vn-central-10"}
	api := &fakeVDCAPI{list: []serversdk.VDCSchema{other, *vdcFixture()}}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	model.Region = types.StringValue("vn-central-1")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if state.ID.ValueString() != vdcTestID.String() {
		t.Fatalf("expected the vDC in vn-central-1, got %s", state.ID.ValueString())
	}
	if query := api.query("/v2/server/vdcs/"); query.Get("region") != "vn-central-1" || query.Has("region_id") {
		t.Fatalf("expected the region name in the list query, got %v", query)
	}
}

// The id criterion with another criterion goes through the finder, because the
// list API has no id filter and the other criterion must also match.
func TestVDCDataSourceReadByIDAndZoneUsesFinder(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{
		list:  []serversdk.VDCSchema{*vdcFixture()},
		zones: []projectsdk.ProjectZoneSchema{{Id: vdcTestZoneID, Name: "vn-central-1a"}},
	}
	model := vdcTestModel()
	model.ID = types.StringValue(vdcTestID.String())
	model.Zone = types.StringValue("vn-central-1a")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if state.ID.ValueString() != vdcTestID.String() {
		t.Fatalf("unexpected vDC: %s", state.ID.ValueString())
	}
	paths := api.paths()
	want := []string{"/v2/projects/" + vdcTestProjectID.String() + "/zones/", "/v2/server/vdcs/"}
	if len(paths) != len(want) || paths[0] != want[0] || paths[1] != want[1] {
		t.Fatalf("expected zone lookup then list without GetVdc, got %v", paths)
	}
	if query := api.query("/v2/server/vdcs/"); query.Get("zone_id") != vdcTestZoneID.String() || query.Has("id") {
		t.Fatalf("unexpected list query: %v", query)
	}
}

func TestVDCDataSourceReadByIDAndNameRejectsMismatch(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{list: []serversdk.VDCSchema{*vdcFixture()}}
	model := vdcTestModel()
	model.ID = types.StringValue(core.UUID{0xcc}.String())
	model.Name = types.StringValue("vdc-1")
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "Error resolving vDC", "vDC was not found matching criteria (id="+core.UUID{0xcc}.String()+`, name="vdc-1")`)
}

func TestVDCDataSourceReadRejectsAmbiguousMatch(t *testing.T) {
	t.Parallel()

	other := *vdcFixture()
	other.Id = core.UUID{0xb6}
	api := &fakeVDCAPI{list: []serversdk.VDCSchema{*vdcFixture(), other}}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "Error resolving vDC", `vDC matching criteria (name="vdc-1") is ambiguous`)
}

func TestVDCDataSourceReadRejectsUnknownZone(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{zones: []projectsdk.ProjectZoneSchema{{Id: core.UUID{0xb7}, Name: "vn-central-1b"}}}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	model.Zone = types.StringValue("vn-central-1a")
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "Unable to resolve zone", `zone: zone was not found matching criteria (name="vn-central-1a")`)
	for _, path := range api.paths() {
		if path == "/v2/server/vdcs/" {
			t.Fatal("expected no vDC list after the zone lookup failed")
		}
	}
}

// G2: a vDC without a zone on the listed page makes the list answer HTTP 400.
func TestVDCDataSourceReadReportsListError(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{listError: http.StatusBadRequest}
	model := vdcTestModel()
	model.Name = types.StringValue("vdc-1")
	_, response := readVDC(t, api.dataSource(t), model)
	requireVDCError(t, response, "Error reading vDC", "request_id=req-list")
}

// AC9: padded criteria and an upper-case ID select the vDC and stay as
// configured.
func TestVDCDataSourceReadPreservesConfiguredValues(t *testing.T) {
	t.Parallel()

	api := &fakeVDCAPI{
		list:  []serversdk.VDCSchema{*vdcFixture()},
		zones: []projectsdk.ProjectZoneSchema{{Id: vdcTestZoneID, Name: "vn-central-1a"}},
	}
	model := vdcTestModel()
	model.ID = types.StringValue(strings.ToUpper(vdcTestID.String()))
	model.Name = types.StringValue(" vdc-1 ")
	model.Region = types.StringValue(" vn-central-1 ")
	model.Zone = types.StringValue(" vn-central-1a ")
	state, response := readVDC(t, api.dataSource(t), model)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	for name, pair := range map[string][2]attr.Value{
		"id": {state.ID, model.ID}, "name": {state.Name, model.Name},
		"region": {state.Region, model.Region}, "zone": {state.Zone, model.Zone},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Fatalf("expected %s to keep %v, got %v", name, pair[1], pair[0])
		}
	}
}

func TestPreserveConfiguredVDCValuesKeepsBackendValueOnDifference(t *testing.T) {
	t.Parallel()

	vdc := vdcFixture()
	config := vdcTestModel()
	config.ID = types.StringValue(core.UUID{0xdd}.String())
	config.Name = types.StringValue("other")
	state := vdcTestModel()
	populateVDCDataSourceState(vdc, &state)
	preserveConfiguredVDCValues(vdc, config, &state)
	if state.ID.ValueString() != vdcTestID.String() || state.Name.ValueString() != "vdc-1" {
		t.Fatalf("expected backend values when the configuration names another value, got %#v", state)
	}

	preserveConfiguredVDCValues(nil, config, &state)
	if state.Name.ValueString() != "vdc-1" {
		t.Fatal("expected a nil vDC to leave the state unchanged")
	}
}
