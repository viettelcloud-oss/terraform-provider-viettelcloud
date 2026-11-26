package loadbalancer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func newServerGroupTestDataSource(t *testing.T, transport http.RoundTripper) *ServerGroupDataSource {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	client, err := loadbalancersdk.NewClient(
		"https://load-balancer.test",
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return &ServerGroupDataSource{
		client:    client,
		projectID: serverGroupTestProjectID,
	}
}

func sampleListServerGroupsJSON(t *testing.T, groups []loadbalancersdk.ServerGroupSchema) string {
	t.Helper()
	paged := loadbalancersdk.PagedServerGroupSchema{
		Count:   len(groups),
		Results: groups,
	}
	b, err := json.Marshal(paged)
	if err != nil {
		t.Fatalf("marshal paged server groups: %v", err)
	}
	return string(b)
}

func serverGroupDataSourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&ServerGroupDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp.Schema
}

func serverGroupDataSourceConfig(t *testing.T, s schema.Schema, model ServerGroupDataSourceModel) tfsdk.Config {
	t.Helper()
	if model.SessionPersistence.IsNull() || model.SessionPersistence.IsUnknown() {
		model.SessionPersistence = types.ObjectNull(serverGroupSessionPersistenceAttrTypes())
	}
	if model.HealthCheck.IsNull() || model.HealthCheck.IsUnknown() {
		model.HealthCheck = types.ObjectNull(serverGroupHealthCheckAttrTypes())
	}
	var state tfsdk.State
	state.Schema = s
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return tfsdk.Config{Schema: s, Raw: state.Raw}
}

func TestServerGroupDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	dsSchema := serverGroupDataSourceSchema(t)
	state := tfsdk.State{Schema: dsSchema}
	model := ServerGroupDataSourceModel{
		ServerGroupModel: serverGroupConfiguredModel().ServerGroupModel,
		LoadBalancerID:   types.StringValue(serverGroupTestLBID.String()),
	}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestServerGroupDataSourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	dsUnderTest, ok := NewServerGroupDataSource().(*ServerGroupDataSource)
	if !ok || dsUnderTest == nil {
		t.Fatal("constructor did not return a ServerGroupDataSource")
	}

	var metadata datasource.MetadataResponse
	dsUnderTest.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_server_group" {
		t.Fatalf("metadata type=%q, want viettelcloud_lb_server_group", metadata.TypeName)
	}

	var response datasource.ConfigureResponse
	dsUnderTest.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unexpected provider data diagnostic")
	}

	clientResource := newServerGroupTestDataSource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}))
	configured := &providerdata.Configured{
		LoadBalancer: clientResource.client,
		ProjectID:    serverGroupTestProjectID,
	}
	response = datasource.ConfigureResponse{}
	dsUnderTest.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: configured}, &response)
	if response.Diagnostics.HasError() || dsUnderTest.client != configured.LoadBalancer || dsUnderTest.projectID != configured.ProjectID {
		t.Fatalf("data source was not configured: diagnostics=%v ds=%#v", response.Diagnostics, dsUnderTest)
	}
}

func TestServerGroupDataSourceSchemaContract(t *testing.T) {
	t.Parallel()

	dsSchema := serverGroupDataSourceSchema(t)

	lbIDAttr, ok := dsSchema.Attributes["load_balancer_id"].(schema.StringAttribute)
	if !ok || !lbIDAttr.Required || lbIDAttr.Computed {
		t.Errorf("load_balancer_id must be required in data source, got %#v", dsSchema.Attributes["load_balancer_id"])
	}

	idAttr, ok := dsSchema.Attributes["id"].(schema.StringAttribute)
	if !ok || !idAttr.Optional || !idAttr.Computed {
		t.Errorf("id must be optional and computed in data source, got %#v", dsSchema.Attributes["id"])
	}

	nameAttr, ok := dsSchema.Attributes["name"].(schema.StringAttribute)
	if !ok || !nameAttr.Optional || !nameAttr.Computed {
		t.Errorf("name must be optional and computed in data source, got %#v", dsSchema.Attributes["name"])
	}

	// all result-only attributes must be Computed and not Optional or Required
	for _, name := range []string{"protocol", "algorithm", "description", "enable_h2", "session_persistence", "health_check", "provisioning_status", "status", "member_count", "created_at", "updated_at"} {
		attr := dsSchema.Attributes[name]
		if attr.IsRequired() || attr.IsOptional() || !attr.IsComputed() {
			t.Errorf("%s must be computed-only, got %#v", name, attr)
		}
	}
	if _, ok := dsSchema.Attributes["encrypt_cert_id"]; ok {
		t.Error("encrypt_cert_id must not be exposed")
	}
}

func TestServerGroupDataSourceMissingCriteria(t *testing.T) {
	t.Parallel()

	ds := newServerGroupTestDataSource(t, nil)
	config := ServerGroupDataSourceModel{
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for missing criteria, got none")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Missing Server Group lookup criteria") {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
}

func TestServerGroupDataSourceWhitespaceOnlyCriteria(t *testing.T) {
	t.Parallel()

	ds := newServerGroupTestDataSource(t, nil)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			Name: types.StringValue("   "),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for whitespace-only name, got none")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "name must not be empty or whitespace-only") {
		t.Fatalf("unexpected error detail: %v", resp.Diagnostics)
	}
}

func TestServerGroupDataSourceInvalidUUID(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		loadBalancerID string
		id             string
		field          string
	}{
		"load_balancer_id": {
			loadBalancerID: "not-a-uuid",
			id:             serverGroupTestID.String(),
			field:          "load_balancer_id",
		},
		"id": {
			loadBalancerID: serverGroupTestLBID.String(),
			id:             "not-a-uuid",
			field:          "id",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				return serverGroupHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
			})
			ds := newServerGroupTestDataSource(t, transport)
			config := ServerGroupDataSourceModel{
				ServerGroupModel: ServerGroupModel{
					ID: types.StringValue(tc.id),
				},
				LoadBalancerID: types.StringValue(tc.loadBalancerID),
			}

			configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

			var resp datasource.ReadResponse
			resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

			ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected diagnostic error for invalid %s, got none", tc.field)
			}
			got := resp.Diagnostics.Errors()[0]
			if got.Summary() != "Invalid UUID" || !strings.HasPrefix(got.Detail(), tc.field+" must be a valid UUID") {
				t.Fatalf("unexpected error: %v", resp.Diagnostics)
			}
		})
	}
}

func TestServerGroupDataSourceReadByID(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetailWithHealthCheck(serverGroupTestID, "web-sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()) {
			return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	ds := newServerGroupTestDataSource(t, transport)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			ID: types.StringValue(serverGroupTestID.String()),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", resp.Diagnostics)
	}

	var state ServerGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != serverGroupTestID.String() || state.Name.ValueString() != "web-sg" || state.Protocol.ValueString() != "HTTP" {
		t.Fatalf("unexpected state after read: %#v", state)
	}
}

func TestServerGroupDataSourceReadByNameAndIDMismatch(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "actual-name", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
	})

	ds := newServerGroupTestDataSource(t, transport)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			ID:   types.StringValue(serverGroupTestID.String()),
			Name: types.StringValue("different-name"),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for name mismatch, got none")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "does not match expected name") {
		t.Fatalf("unexpected error detail: %v", resp.Diagnostics)
	}
}

func TestServerGroupDataSourceNotFound(t *testing.T) {
	t.Parallel()

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return serverGroupHTTPResponse(req, http.StatusNotFound, `{"error":"not found"}`), nil
	})

	ds := newServerGroupTestDataSource(t, transport)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			ID: types.StringValue(serverGroupTestID.String()),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for not found, got none")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Server Group not found") {
		t.Fatalf("unexpected error: %v", resp.Diagnostics)
	}
}

func TestServerGroupDataSourceReadByIDDeleted(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "deleted-sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetail.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
	})

	ds := newServerGroupTestDataSource(t, transport)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			ID: types.StringValue(serverGroupTestID.String()),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for deleted server group, got none")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Server Group not found") {
		t.Fatalf("unexpected error summary: %v", resp.Diagnostics)
	}
}

func TestServerGroupDataSourceReadByName(t *testing.T) {
	t.Parallel()

	sgSummary := loadbalancersdk.ServerGroupSchema{
		Id:                 serverGroupTestID,
		Name:               "app-sg",
		Protocol:           loadbalancersdk.ServerGroupProtocolTypeTCP,
		Algorithm:          loadbalancersdk.ServerGroupAlgorithmROUNDROBIN,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
	}
	listJSON := sampleListServerGroupsJSON(t, []loadbalancersdk.ServerGroupSchema{sgSummary})
	sgDetail := sampleServerGroupDetail(serverGroupTestID, "app-sg", loadbalancersdk.ServerGroupProtocolTypeTCP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/", serverGroupTestLBID.String()):
			if req.URL.Query().Get("name") != "app-sg" {
				t.Fatalf("expected query param name=app-sg, got %s", req.URL.Query().Get("name"))
			}
			return serverGroupHTTPResponse(req, http.StatusOK, listJSON), nil
		case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
			return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	ds := newServerGroupTestDataSource(t, transport)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			Name: types.StringValue("app-sg"),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}

	configData := serverGroupDataSourceConfig(t, serverGroupDataSourceSchema(t), config)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: serverGroupDataSourceSchema(t)}

	ds.Read(context.Background(), datasource.ReadRequest{Config: configData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", resp.Diagnostics)
	}

	var state ServerGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != serverGroupTestID.String() || state.Name.ValueString() != "app-sg" || state.Protocol.ValueString() != "TCP" {
		t.Fatalf("unexpected state after read: %#v", state)
	}
}

func TestServerGroupDataSourcePreservesConfiguredValues(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	config := ServerGroupDataSourceModel{
		ServerGroupModel: ServerGroupModel{
			Name: types.StringValue("  pool  "),
			ID:   types.StringValue(strings.ToUpper(serverGroupTestID.String())),
		},
		LoadBalancerID: types.StringValue(serverGroupTestLBID.String()),
	}
	var state ServerGroupDataSourceModel

	populateServerGroupDataSourceState(sgDetail, &state)
	preserveServerGroupConfiguredValues(sgDetail, config, &state)

	if state.Name.ValueString() != "  pool  " {
		t.Errorf("expected preserved configured name, got %s", state.Name.ValueString())
	}
	if state.ID.ValueString() != strings.ToUpper(serverGroupTestID.String()) {
		t.Errorf("expected preserved configured ID, got %s", state.ID.ValueString())
	}
	if state.LoadBalancerID.ValueString() != serverGroupTestLBID.String() {
		t.Errorf("expected preserved LoadBalancerID, got %s", state.LoadBalancerID.ValueString())
	}
}
