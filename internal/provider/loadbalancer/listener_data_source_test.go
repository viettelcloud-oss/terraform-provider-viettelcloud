package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func newListenerTestDataSource(t *testing.T, fn listenerRoundTripFunc) *ListenerDataSource {
	t.Helper()
	httpClient := &http.Client{Transport: fn}
	client, err := loadbalancersdk.NewClient(
		"https://lb.example.com",
		loadbalancersdk.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	d := NewListenerDataSource().(*ListenerDataSource)
	d.client = client
	d.projectID = listenerTestProjectID
	return d
}

func listenerDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&ListenerDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	return response.Schema
}

func defaultTestListenerDataSourceModel() ListenerDataSourceModel {
	return ListenerDataSourceModel{
		ListenerModel: ListenerModel{AllowedCIDRs: types.SetNull(types.StringType)},
		SNITLSCertIDs: types.SetNull(types.StringType),
	}
}

func TestListenerDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	state := tfsdk.State{Schema: listenerDataSourceSchema(t)}
	model := defaultTestListenerDataSourceModel()
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestListenerDataSourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	d := NewListenerDataSource()
	if d == nil {
		t.Fatal("expected non-nil data source")
	}

	var metadataResponse datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadataResponse)
	if metadataResponse.TypeName != "viettelcloud_lb_listener" {
		t.Fatalf("expected viettelcloud_lb_listener metadata, got %q", metadataResponse.TypeName)
	}

	dataSource := &ListenerDataSource{}
	var nilResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResponse.Diagnostics)
	}

	var invalidResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	lbClient, err := loadbalancersdk.NewClient("http://localhost", loadbalancersdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create loadbalancer client: %v", err)
	}

	var validResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{
		LoadBalancer: lbClient,
		ProjectID:    listenerTestProjectID,
	}}, &validResponse)
	if validResponse.Diagnostics.HasError() || dataSource.client != lbClient || dataSource.projectID != listenerTestProjectID {
		t.Fatalf("unexpected configured data source: %#v diagnostics=%v", dataSource, validResponse.Diagnostics)
	}
}

func TestListenerDataSourceSchemaContract(t *testing.T) {
	t.Parallel()

	s := listenerDataSourceSchema(t)

	lbIDAttr, ok := s.Attributes["load_balancer_id"].(datasourceschema.StringAttribute)
	if !ok || !lbIDAttr.Required || lbIDAttr.Computed || lbIDAttr.Optional {
		t.Fatalf("expected load_balancer_id to be Required only, got %#v", s.Attributes["load_balancer_id"])
	}

	for _, name := range []string{"id", "name"} {
		attr, ok := s.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attr.Optional || !attr.Computed || attr.Required {
			t.Fatalf("expected %s to be optional and computed, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"description", "protocol", "server_group_id", "default_tls_cert_id", "provisioning_status", "status", "created_at", "updated_at"} {
		attr, ok := s.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || attr.Optional || !attr.Computed || attr.Required {
			t.Fatalf("expected %s to be computed only, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"protocol_port", "listener_timeout", "server_timeout", "server_connecting_timeout"} {
		attr, ok := s.Attributes[name].(datasourceschema.Int64Attribute)
		if !ok || attr.Optional || !attr.Computed || attr.Required {
			t.Fatalf("expected %s to be computed only, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"enable_h2", "set_x_forwarded_for", "set_x_forwarded_port", "set_x_forwarded_proto"} {
		attr, ok := s.Attributes[name].(datasourceschema.BoolAttribute)
		if !ok || attr.Optional || !attr.Computed || attr.Required {
			t.Fatalf("expected %s to be computed only, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"sni_tls_cert_ids", "allowed_cidrs"} {
		attr, ok := s.Attributes[name].(datasourceschema.SetAttribute)
		if !ok || attr.Optional || !attr.Computed || attr.Required || attr.ElementType != types.StringType {
			t.Fatalf("expected %s to be computed string set, got %#v", name, s.Attributes[name])
		}
	}
}

func TestListenerDataSourceMissingCriteria(t *testing.T) {
	t.Parallel()

	d := &ListenerDataSource{}

	t.Run("missing load_balancer_id", func(t *testing.T) {
		t.Parallel()
		model := defaultTestListenerDataSourceModel()
		model.ID = types.StringValue(listenerTestID.String())
		_, diags := d.getListener(context.Background(), model)
		if !diags.HasError() {
			t.Fatal("expected error on missing load_balancer_id")
		}
	})

	t.Run("invalid load_balancer_id UUID", func(t *testing.T) {
		t.Parallel()
		model := defaultTestListenerDataSourceModel()
		model.LoadBalancerID = types.StringValue("not-a-uuid")
		model.ID = types.StringValue(listenerTestID.String())
		_, diags := d.getListener(context.Background(), model)
		if !diags.HasError() {
			t.Fatal("expected error on invalid load_balancer_id UUID")
		}
	})

	t.Run("missing both id and name", func(t *testing.T) {
		t.Parallel()
		model := defaultTestListenerDataSourceModel()
		model.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		_, diags := d.getListener(context.Background(), model)
		if !diags.HasError() || diags.Errors()[0].Summary() != "Missing Load Balancer Listener lookup criteria" {
			t.Fatalf("expected missing criteria diagnostic, got %v", diags)
		}
	})

	t.Run("blank name filter", func(t *testing.T) {
		t.Parallel()
		model := defaultTestListenerDataSourceModel()
		model.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		model.Name = types.StringValue("   ")
		_, diags := d.getListener(context.Background(), model)
		if !diags.HasError() {
			t.Fatal("expected error on blank name filter")
		}
	})

	for name, tt := range map[string]struct {
		id   string
		name types.String
	}{
		"invalid id UUID":           {id: "not-a-uuid", name: types.StringNull()},
		"invalid id UUID with name": {id: "not-a-uuid", name: types.StringValue("web")},
		"blank id":                  {id: "  ", name: types.StringNull()},
		"blank id with name":        {id: "  ", name: types.StringValue("web")},
	} {
		t.Run(name+" fails before any request", func(t *testing.T) {
			t.Parallel()
			model := defaultTestListenerDataSourceModel()
			model.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
			model.ID = types.StringValue(tt.id)
			model.Name = tt.name
			detail, diags := d.getListener(context.Background(), model)
			if !diags.HasError() || detail != nil {
				t.Fatalf("expected a diagnostic and no listener, got %v", diags)
			}
		})
	}
}

func TestListenerDataSourceReadByID(t *testing.T) {
	t.Parallel()

	s := listenerDataSourceSchema(t)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.ID = types.StringValue(listenerTestID.String())

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("read failed: %v", resp.Diagnostics)
		}

		var result ListenerDataSourceModel
		if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}

		if result.ID.ValueString() != listenerTestID.String() {
			t.Fatalf("expected ID %s, got %s", listenerTestID, result.ID.ValueString())
		}
		if result.Name.ValueString() != "web" {
			t.Fatalf("expected name web, got %s", result.Name.ValueString())
		}
		if result.ProtocolPort.ValueInt64() != 80 {
			t.Fatalf("expected port 80, got %d", result.ProtocolPort.ValueInt64())
		}
	})

	t.Run("name mismatch fails", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "actual-name", "HTTP", 80, "ACTIVE", "ONLINE")), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.ID = types.StringValue(listenerTestID.String())
		config.Name = types.StringValue("different-name")

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Fatal("expected name mismatch diagnostic")
		}
	})

	t.Run("404 not found", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.ID = types.StringValue(listenerTestID.String())

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Fatal("expected not found diagnostic")
		}
	})

	t.Run("DELETED provisioning status treated as not found", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "DELETED", "OFFLINE")), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.ID = types.StringValue(listenerTestID.String())

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Fatal("expected not found diagnostic on DELETED status")
		}
	})
}

func TestListenerDataSourceReadByName(t *testing.T) {
	t.Parallel()

	s := listenerDataSourceSchema(t)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, fmt.Sprintf("/listeners/%s/", listenerTestID)) {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "my-service", "HTTP", 8080, "ACTIVE", "ONLINE")), nil
			}
			if strings.Contains(req.URL.Path, "/listeners/") {
				return listenerJSONResponse(http.StatusOK, fmt.Sprintf(`{
					"count": 1,
					"results": [
						{"id": %q, "name": "my-service", "protocol": "HTTP", "protocol_port": 8080, "provisioning_status": "ACTIVE", "status": "ONLINE"}
					]
				}`, listenerTestID)), nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.Name = types.StringValue("my-service")

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("read failed: %v", resp.Diagnostics)
		}

		var result ListenerDataSourceModel
		if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}

		if result.ID.ValueString() != listenerTestID.String() {
			t.Fatalf("expected ID %s, got %s", listenerTestID, result.ID.ValueString())
		}
		if result.Name.ValueString() != "my-service" {
			t.Fatalf("expected name my-service, got %s", result.Name.ValueString())
		}
		if result.ProtocolPort.ValueInt64() != 8080 {
			t.Fatalf("expected port 8080, got %d", result.ProtocolPort.ValueInt64())
		}
	})

	t.Run("zero matches returns not found", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, `{"count": 0, "results": []}`), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.Name = types.StringValue("non-existent")

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Fatal("expected not found diagnostic for zero matches")
		}
	})

	t.Run("multiple matches returns error", func(t *testing.T) {
		t.Parallel()
		dataSource := newListenerTestDataSource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, fmt.Sprintf(`{
				"count": 2,
				"results": [
					{"id": %q, "name": "duplicate", "protocol": "HTTP", "protocol_port": 80, "provisioning_status": "ACTIVE", "status": "ONLINE"},
					{"id": %q, "name": "duplicate", "protocol": "HTTP", "protocol_port": 81, "provisioning_status": "ACTIVE", "status": "ONLINE"}
				]
			}`, listenerTestID, core.UUID{99})), nil
		})

		config := defaultTestListenerDataSourceModel()
		config.LoadBalancerID = types.StringValue(listenerTestLoadBalancerID.String())
		config.Name = types.StringValue("duplicate")

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &config); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp datasource.ReadResponse
		resp.State = state

		dataSource.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{
			Schema: s,
			Raw:    state.Raw,
		}}, &resp)

		if !resp.Diagnostics.HasError() {
			t.Fatal("expected ambiguous matches diagnostic")
		}
	})
}
