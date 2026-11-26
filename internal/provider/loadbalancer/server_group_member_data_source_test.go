package loadbalancer

import (
	"context"
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

func serverGroupMemberDataSourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&ServerGroupMemberDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp.Schema
}

func serverGroupMemberDataSourceConfiguredModel() ServerGroupMemberDataSourceModel {
	return ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:                 types.StringValue(memberTestID.String()),
			Name:               types.StringValue("web-server-1"),
			Address:            types.StringValue("192.168.1.10"),
			ProtocolPort:       types.Int64Value(80),
			Weight:             types.Int64Value(1),
			Backup:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}
}

func serverGroupMemberDataSourceConfig(t *testing.T, model ServerGroupMemberDataSourceModel) tfsdk.Config {
	t.Helper()
	dsSchema := serverGroupMemberDataSourceSchema(t)
	state := tfsdk.State{Schema: dsSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set server group member data source config: %v", diags)
	}
	return tfsdk.Config{Schema: dsSchema, Raw: state.Raw}
}

func TestServerGroupMemberDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	dsSchema := serverGroupMemberDataSourceSchema(t)
	state := tfsdk.State{Schema: dsSchema}
	model := serverGroupMemberDataSourceConfiguredModel()
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestServerGroupMemberDataSourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	dsUnderTest, ok := NewServerGroupMemberDataSource().(*ServerGroupMemberDataSource)
	if !ok || dsUnderTest == nil {
		t.Fatal("constructor did not return a ServerGroupMemberDataSource")
	}

	var metadata datasource.MetadataResponse
	dsUnderTest.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_member" {
		t.Fatalf("metadata type=%q, want viettelcloud_lb_member", metadata.TypeName)
	}

	var response datasource.ConfigureResponse
	dsUnderTest.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unexpected provider data diagnostic")
	}

	clientDS := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}))
	configured := &providerdata.Configured{
		LoadBalancer: clientDS.client,
		ProjectID:    memberTestProjectID,
	}
	response = datasource.ConfigureResponse{}
	dsUnderTest.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: configured}, &response)
	if response.Diagnostics.HasError() || dsUnderTest.client != configured.LoadBalancer || dsUnderTest.projectID != configured.ProjectID {
		t.Fatalf("data source was not configured: diagnostics=%v ds=%#v", response.Diagnostics, dsUnderTest)
	}
}

func TestServerGroupMemberDataSourceSchemaContract(t *testing.T) {
	t.Parallel()

	dsSchema := serverGroupMemberDataSourceSchema(t)

	// server_group_id: Required
	sgIDAttr, ok := dsSchema.Attributes["server_group_id"].(schema.StringAttribute)
	if !ok || !sgIDAttr.Required {
		t.Errorf("server_group_id must be required, got %#v", dsSchema.Attributes["server_group_id"])
	}

	// id, name: Optional, Computed
	for _, name := range []string{"id", "name"} {
		attr, ok := dsSchema.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Optional || !attr.Computed {
			t.Errorf("%s must be optional and computed, got %#v", name, dsSchema.Attributes[name])
		}
	}

	// Result-only attributes
	for _, name := range []string{"address", "provisioning_status", "status", "created_at", "updated_at"} {
		attr, ok := dsSchema.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Optional || attr.Required {
			t.Errorf("%s must be computed-only StringAttribute, got %#v", name, dsSchema.Attributes[name])
		}
	}

	for _, name := range []string{"protocol_port", "weight"} {
		attr, ok := dsSchema.Attributes[name].(schema.Int64Attribute)
		if !ok || !attr.Computed || attr.Optional || attr.Required {
			t.Errorf("%s must be computed-only Int64Attribute, got %#v", name, dsSchema.Attributes[name])
		}
	}

	backupAttr, ok := dsSchema.Attributes["backup"].(schema.BoolAttribute)
	if !ok || !backupAttr.Computed || backupAttr.Optional || backupAttr.Required {
		t.Errorf("backup must be computed-only BoolAttribute, got %#v", dsSchema.Attributes["backup"])
	}
}

func TestServerGroupMemberDataSourceReadByID(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:   types.StringValue(memberTestID.String()),
			Name: types.StringNull(),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{
		Config: serverGroupMemberDataSourceConfig(t, config),
	}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error reading by ID: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberDataSourceModel
	_ = resp.State.Get(context.Background(), &state)
	if state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("state id=%q, want %q", state.ID.ValueString(), memberTestID.String())
	}
	if state.Name.ValueString() != "web-server-1" {
		t.Fatalf("state name=%q, want web-server-1", state.Name.ValueString())
	}
	if state.Address.ValueString() != "192.168.1.10" {
		t.Fatalf("state address=%q, want 192.168.1.10", state.Address.ValueString())
	}
	if state.ProtocolPort.ValueInt64() != 80 {
		t.Fatalf("state protocol_port=%d, want 80", state.ProtocolPort.ValueInt64())
	}
}

func TestServerGroupMemberDataSourceReadByName(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	listJSON := fmt.Sprintf(`{"count":1,"results":[%s]}`, sampleServerGroupMemberDetailJSON(t, member))

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, listJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:   types.StringNull(),
			Name: types.StringValue("web-server-1"),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{
		Config: serverGroupMemberDataSourceConfig(t, config),
	}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error reading by name: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberDataSourceModel
	_ = resp.State.Get(context.Background(), &state)
	if state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("state id=%q, want %q", state.ID.ValueString(), memberTestID.String())
	}
	if state.Name.ValueString() != "web-server-1" {
		t.Fatalf("state name=%q, want web-server-1", state.Name.ValueString())
	}
}

func TestServerGroupMemberDataSourceMissingCriteria(t *testing.T) {
	t.Parallel()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no network call expected")
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:   types.StringNull(),
			Name: types.StringNull(),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "At least one of id or name") {
		t.Fatalf("expected missing lookup criteria error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceMissingServerGroupID(t *testing.T) {
	t.Parallel()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no network call expected")
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID: types.StringValue(memberTestID.String()),
		},
		ServerGroupID: types.StringNull(),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "server_group_id must be configured") {
		t.Fatalf("expected missing server_group_id error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceWhitespaceCriteria(t *testing.T) {
	t.Parallel()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no network call expected")
		return nil, nil
	}))

	dsSchema := serverGroupMemberDataSourceSchema(t)

	// Whitespace ID
	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID: types.StringValue("   "),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "id must not be empty") {
		t.Fatalf("expected whitespace ID error, got %v", resp.Diagnostics)
	}

	// Whitespace Name
	config = ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			Name: types.StringValue("   "),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}
	req = datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	resp = datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "name must not be empty") {
		t.Fatalf("expected whitespace Name error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceNotFoundByID(t *testing.T) {
	t.Parallel()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID: types.StringValue(memberTestID.String()),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "not found") {
		t.Fatalf("expected not found error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceDeletedStatusByID(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID: types.StringValue(memberTestID.String()),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "DELETED state") {
		t.Fatalf("expected DELETED state error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceNameMismatchWithID(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	listJSON := fmt.Sprintf(`{"count":1,"results":[%s]}`, sampleServerGroupMemberDetailJSON(t, member))

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, listJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:   types.StringValue(memberTestID.String()),
			Name: types.StringValue("different-name"),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "was not found") {
		t.Fatalf("expected not found error due to name mismatch with filter, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceReadByIDAndName(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	listJSON := fmt.Sprintf(`{"count":1,"results":[%s]}`, sampleServerGroupMemberDetailJSON(t, member))

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, listJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:   types.StringValue(memberTestID.String()),
			Name: types.StringValue("web-server-1"),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error reading by ID and name: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberDataSourceModel
	_ = resp.State.Get(context.Background(), &state)
	if state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("state id=%q, want %q", state.ID.ValueString(), memberTestID.String())
	}
	if state.Name.ValueString() != "web-server-1" {
		t.Fatalf("state name=%q, want web-server-1", state.Name.ValueString())
	}
}

func TestServerGroupMemberDataSourcePreservesConfiguredUUIDRepresentation(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	upperUUID := "  " + strings.ToUpper(memberTestID.String()) + "\n"
	serverGroupID := "\t" + memberTestServerGroupID.String()
	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID: types.StringValue(upperUUID),
		},
		ServerGroupID: types.StringValue(serverGroupID),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error reading by uppercase UUID: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberDataSourceModel
	_ = resp.State.Get(context.Background(), &state)
	if state.ID.ValueString() != upperUUID || state.ServerGroupID.ValueString() != serverGroupID {
		t.Fatalf("state id=%q server_group_id=%q, want %q and %q (configured spelling preserved)", state.ID.ValueString(), state.ServerGroupID.ValueString(), upperUUID, serverGroupID)
	}
}

func TestServerGroupMemberDataSourceNotFoundByName(t *testing.T) {
	t.Parallel()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, `{"count":0,"results":[]}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			Name: types.StringValue("missing-server"),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	dsSchema := serverGroupMemberDataSourceSchema(t)
	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: dsSchema}

	ds.Read(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "was not found") {
		t.Fatalf("expected not found error, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberDataSourceAmbiguousName(t *testing.T) {
	t.Parallel()

	first := sampleServerGroupMemberDetail(memberTestID, "web-server", "192.168.1.10", 80, 1, false)
	second := sampleServerGroupMemberDetail(core.UUID{4}, "web-server", "192.168.1.11", 80, 1, false)
	listJSON := fmt.Sprintf(`{"count":2,"results":[%s,%s]}`,
		sampleServerGroupMemberDetailJSON(t, first), sampleServerGroupMemberDetailJSON(t, second))

	resp := readServerGroupMemberDataSourceByName(t, "web-server", func(req *http.Request) *http.Response {
		return serverGroupMemberHTTPResponse(req, http.StatusOK, listJSON)
	})

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected ambiguous lookup error")
	}
	got := resp.Diagnostics.Errors()[0]
	if got.Summary() != "Error resolving server group member" || !strings.Contains(got.Detail(), "ambiguous") {
		t.Fatalf("diagnostic=%q %q, want resolve summary and ambiguous detail", got.Summary(), got.Detail())
	}
}

func TestServerGroupMemberDataSourceListError(t *testing.T) {
	t.Parallel()

	resp := readServerGroupMemberDataSourceByName(t, "web-server", func(req *http.Request) *http.Response {
		return serverGroupMemberHTTPResponse(req, http.StatusForbidden, `{"detail":"forbidden"}`)
	})

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected list error")
	}
	got := resp.Diagnostics.Errors()[0]
	if got.Summary() != "Error resolving server group member" || strings.Contains(got.Detail(), "was not found") {
		t.Fatalf("diagnostic=%q %q, want resolve summary without not-found detail", got.Summary(), got.Detail())
	}
}

func readServerGroupMemberDataSourceByName(t *testing.T, name string, list func(*http.Request) *http.Response) datasource.ReadResponse {
	t.Helper()

	ds := newServerGroupMemberTestDataSource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
			return list(req), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	config := ServerGroupMemberDataSourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			Name: types.StringValue(name),
		},
		ServerGroupID: types.StringValue(memberTestServerGroupID.String()),
	}

	req := datasource.ReadRequest{Config: serverGroupMemberDataSourceConfig(t, config)}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: serverGroupMemberDataSourceSchema(t)}}
	ds.Read(context.Background(), req, &resp)
	return resp
}
