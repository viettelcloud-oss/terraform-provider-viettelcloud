package dbaascluster

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

// dataSourceConfig returns the configuration of a lookup with the given
// arguments. A nil argument is omitted.
func dataSourceConfig(t *testing.T, id, name, engine, region *string) tfsdk.Config {
	t.Helper()
	return dataSourceConfigWithStatus(t, id, name, engine, region, nil)
}

func dataSourceConfigWithStatus(t *testing.T, id, name, engine, region, status *string) tfsdk.Config {
	t.Helper()

	optional := func(value *string) types.String {
		if value == nil {
			return types.StringNull()
		}
		return types.StringValue(*value)
	}
	model := DBClusterDataSourceModel{
		DBClusterModel: DBClusterModel{
			ID: optional(id), Name: optional(name), Engine: optional(engine), Status: optional(status),
			AllowedCIDRs: types.SetNull(types.StringType), MaintenanceConfig: types.ObjectNull(maintenanceAttrTypes), EngineConfig: types.ObjectNull(engineConfigAttrTypes),
			Metadata: types.MapNull(types.StringType), Endpoint: types.MapNull(types.StringType),
		},
		Region:           optional(region),
		NodePools:        types.ListNull(types.ObjectType{AttrTypes: nodePoolDataSourceAttrTypes}),
		Plugins:          types.SetNull(types.StringType),
		AutoBackupConfig: types.ObjectNull(autoBackupAttrTypes),
	}
	response := dataSourceSchemaForTest(t)
	state := tfsdk.State{Schema: response.Schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	return tfsdk.Config{Schema: response.Schema, Raw: state.Raw}
}

func newTestDataSource(t *testing.T, backend *fakeBackend) *DBClusterDataSource {
	t.Helper()
	return &DBClusterDataSource{
		client:    testDBaaSClient(t, backend.RoundTrip),
		project:   testProjectClient(t, backend.RoundTrip),
		projectID: testProjectID,
	}
}

func readDataSource(t *testing.T, d *DBClusterDataSource, config tfsdk.Config) (*datasource.ReadResponse, DBClusterDataSourceModel) {
	t.Helper()
	response := dataSourceSchemaForTest(t)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: response.Schema, Raw: tftypes.NewValue(config.Raw.Type(), nil)}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, resp)
	var model DBClusterDataSourceModel
	if !resp.Diagnostics.HasError() {
		if diags := resp.State.Get(context.Background(), &model); diags.HasError() {
			t.Fatalf("state: %v", diags)
		}
	}
	return resp, model
}

func listItem(id core.UUID, name, engine string) map[string]any {
	return map[string]any{
		"id": id.String(), "name": name, "engine": engine, "status": "active",
		"region": map[string]any{"id": testRegionID.String(), "name": "region-1", "description": ""},
	}
}

//go:fix inline
func TestDBClusterDataSourceReadsByIDWithoutListing(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleCluster()))
	configuredID := "  " + strings.ToUpper(testClusterID.String()) + "\t"
	resp, model := readDataSource(t, newTestDataSource(t, backend), dataSourceConfig(t, &configuredID, nil, nil, nil))
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}

	if len(backend.requests) != 1 {
		t.Errorf("a lookup by ID alone must not list, requests=%v", backend.requests)
	}
	if model.ID.ValueString() != configuredID {
		t.Errorf("the configured ID spelling must be kept, got %s", model.ID)
	}
	if model.Name.ValueString() != "orders" || model.Engine.ValueString() != "mongodb" || model.Region.ValueString() != "region-1" ||
		model.EngineVersion.ValueString() != "7.0" || model.SubnetName.ValueString() != "subnet-1" || model.VPCName.ValueString() != "vpc-1" {
		t.Errorf("unexpected state: %#v", model)
	}
	if len(model.NodePools.Elements()) != 1 || !model.Zone.IsNull() {
		t.Errorf("pools=%s zone=%s", model.NodePools, model.Zone)
	}
}

func TestDBClusterDataSourceMatchesTheNameExactly(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	registerCatalog(t, backend)
	var query string
	backend.on(http.MethodGet, clustersPath, func(req *http.Request) (*http.Response, error) {
		query = req.URL.RawQuery
		// The backend matches the name as a case-insensitive substring.
		return testHTTPResponse(req, http.StatusOK, pageBody(
			t,
			listItem(testClusterID, "orders", "mongodb"),
			listItem(core.UUID{2, 2}, "orders-archive", "mongodb"),
			listItem(core.UUID{3, 3}, "Orders", "mongodb"),
		)), nil
	})
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleCluster()))

	resp, model := readDataSource(t, newTestDataSource(t, backend), dataSourceConfig(t, nil, new("orders"), new("mongodb"), new("region-1")))
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	if model.ID.ValueString() != testClusterID.String() {
		t.Errorf("id = %s", model.ID)
	}
	for _, want := range []string{"name=orders", "engine=mongodb", "region_id=" + testRegionID.String()} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}
}

func TestDBClusterDataSourceKeepsConfiguredLookupSpelling(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clustersPath, http.StatusOK, pageBody(t, listItem(testClusterID, "orders", "mongodb")))
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleCluster()))
	_, model := readDataSource(t, newTestDataSource(t, backend), dataSourceConfig(t, nil, new(" orders "), new(" mongodb "), nil))
	if model.Name.ValueString() != " orders " || model.Engine.ValueString() != " mongodb " {
		t.Fatalf("name=%q engine=%q", model.Name.ValueString(), model.Engine.ValueString())
	}
}

func TestDBClusterDataSourceReportsLookupFailures(t *testing.T) {
	t.Parallel()

	two := pageBody(t, listItem(testClusterID, "orders", "mongodb"), listItem(core.UUID{2}, "orders", "opensearch"))
	tests := []struct {
		name    string
		config  func(*testing.T) tfsdk.Config
		prepare func(*fakeBackend)
		want    string
	}{
		{
			name:   "no criteria",
			config: func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, nil, nil, nil) },
			want:   "Missing DB Cluster lookup criteria",
		},
		{
			name:   "whitespace name",
			config: func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, new("   "), nil, nil) },
			want:   "Invalid DB cluster name filter",
		},
		{
			name:   "whitespace engine",
			config: func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, new("orders"), new(" "), nil) },
			want:   "Invalid DB cluster engine filter",
		},
		{
			name:   "invalid UUID",
			config: func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, new("not-a-uuid"), nil, nil, nil) },
			want:   "id",
		},
		{
			name: "missing cluster by ID",
			config: func(t *testing.T) tfsdk.Config {
				return dataSourceConfig(t, new(testClusterID.String()), nil, nil, nil)
			},
			prepare: func(b *fakeBackend) { b.respond(http.MethodGet, clusterPath(), http.StatusNotFound, `{}`) },
			want:    "DB cluster not found",
		},
		{
			name:    "no match",
			config:  func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, new("orders"), new("mongodb"), nil) },
			prepare: func(b *fakeBackend) { b.respond(http.MethodGet, clustersPath, http.StatusOK, pageBody(t)) },
			want:    "was not found",
		},
		{
			name:    "ambiguous name without an engine",
			config:  func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, new("orders"), nil, nil) },
			prepare: func(b *fakeBackend) { b.respond(http.MethodGet, clustersPath, http.StatusOK, two) },
			want:    "ambiguous",
		},
		{
			name:    "unknown region",
			config:  func(t *testing.T) tfsdk.Config { return dataSourceConfig(t, nil, new("orders"), nil, new("nowhere")) },
			prepare: func(b *fakeBackend) { b.respond(http.MethodGet, regionsPath, http.StatusOK, pageBody(t)) },
			want:    "Error resolving region",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend := newFakeBackend(t)
			if tt.prepare != nil {
				tt.prepare(backend)
			}
			resp, _ := readDataSource(t, newTestDataSource(t, backend), tt.config(t))
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
			var messages []string
			for _, d := range resp.Diagnostics.Errors() {
				messages = append(messages, d.Summary()+" "+d.Detail())
			}
			if !strings.Contains(strings.Join(messages, "\n"), tt.want) {
				t.Fatalf("diagnostics %q lack %q", messages, tt.want)
			}
		})
	}
}

func TestDBClusterDataSourceMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	d := NewDBClusterDataSource().(*DBClusterDataSource)
	var metadata datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_dbc_cluster" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}

	var ignored datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &ignored)
	if ignored.Diagnostics.HasError() || d.client != nil {
		t.Fatalf("a nil provider data must be ignored: %v", ignored.Diagnostics)
	}
	var wrong datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 42}, &wrong)
	if !wrong.Diagnostics.HasError() {
		t.Fatal("unexpected provider data must fail")
	}

	backend := newFakeBackend(t)
	client, project := testDBaaSClient(t, backend.RoundTrip), testProjectClient(t, backend.RoundTrip)
	var configured datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{DBaaSCluster: client, Project: project, ProjectID: testProjectID}}, &configured)
	if configured.Diagnostics.HasError() || d.client != client || d.project != project || d.projectID != testProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestDBClusterDataSourceReportsReadFailures(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.respond(http.MethodGet, clusterPath(), http.StatusInternalServerError, `{}`)
	resp, _ := readDataSource(t, newTestDataSource(t, backend), dataSourceConfig(t, new(testClusterID.String()), nil, nil, nil))
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Error reading DB cluster") {
		t.Fatalf("expected a read error, got %v", resp.Diagnostics)
	}
}

func TestDBClusterDataSourceMapsPluginsAndSchedule(t *testing.T) {
	t.Parallel()

	cluster := sampleOpenSearchCluster()
	cluster.Plugins = []dbaasclustersdk.NestedDBClusterPluginSchema{{Id: core.UUID{20}, Name: "analysis-icu"}}
	zone := dbaasclustersdk.NestedZoneSchema{Id: core.UUID{12}, Name: "zone-a"}
	cluster.Zone = core.NullableFromPtr(&zone)
	var model DBClusterDataSourceModel
	populateDBClusterDataSourceState(cluster, &model)
	if len(model.Plugins.Elements()) != 1 || model.Zone.ValueString() != "zone-a" || len(model.NodePools.Elements()) != 2 {
		t.Fatalf("plugins=%s zone=%s pools=%s", model.Plugins, model.Zone, model.NodePools)
	}
}

func TestDBClusterDataSourceFiltersByStatus(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	var query string
	backend.on(http.MethodGet, clustersPath, func(req *http.Request) (*http.Response, error) {
		query = req.URL.RawQuery
		failed := listItem(core.UUID{2, 2}, "orders", "mongodb")
		failed["status"] = "error"
		return testHTTPResponse(req, http.StatusOK, pageBody(t, listItem(testClusterID, "orders", "mongodb"), failed)), nil
	})
	backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleCluster()))

	resp, model := readDataSource(t, newTestDataSource(t, backend), dataSourceConfigWithStatus(t, nil, new("orders"), new("mongodb"), nil, new(" active ")))
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	if model.ID.ValueString() != testClusterID.String() || model.Status.ValueString() != " active " {
		t.Errorf("id=%s status=%q", model.ID, model.Status.ValueString())
	}
	if !strings.Contains(query, "status=active") {
		t.Errorf("query %q lacks the status filter", query)
	}
}

func TestDBClusterDataSourceAcceptsAnySingleCriterion(t *testing.T) {
	t.Parallel()

	tests := map[string]tfsdk.Config{
		"engine only": dataSourceConfig(t, nil, nil, new("mongodb"), nil),
		"status only": dataSourceConfigWithStatus(t, nil, nil, nil, nil, new("active")),
		"name only":   dataSourceConfig(t, nil, new("orders"), nil, nil),
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			backend := newFakeBackend(t)
			backend.respond(http.MethodGet, clustersPath, http.StatusOK, pageBody(t, listItem(testClusterID, "orders", "mongodb")))
			backend.respond(http.MethodGet, clusterPath(), http.StatusOK, jsonBody(t, sampleCluster()))
			if resp, _ := readDataSource(t, newTestDataSource(t, backend), config); resp.Diagnostics.HasError() {
				t.Fatalf("one criterion is enough when it selects one cluster: %v", resp.Diagnostics)
			}
		})
	}
}

func TestDBClusterDataSourceRejectsABlankStatus(t *testing.T) {
	t.Parallel()

	resp, _ := readDataSource(t, newTestDataSource(t, newFakeBackend(t)), dataSourceConfigWithStatus(t, nil, nil, nil, nil, new("  ")))
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "status filter") {
		t.Fatalf("expected a blank status error, got %v", resp.Diagnostics)
	}
}
