package network

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var elasticIPDataSourceTestUUID = core.UUID{1}

type elasticIPDataSourceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f elasticIPDataSourceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestElasticIPDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	state := tfsdk.State{Schema: elasticIPDataSourceSchema(t)}
	if diags := state.Set(context.Background(), &ElasticIPDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestElasticIPDataSourceMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	dataSource := NewElasticIPDataSource()
	var metadata datasource.MetadataResponse
	dataSource.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_elastic_ip" {
		t.Fatalf("expected type name viettelcloud_elastic_ip, got %q", metadata.TypeName)
	}

	client, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(&http.Client{}))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	configured := &ElasticIPDataSource{}
	var response datasource.ConfigureResponse
	configured.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", response.Diagnostics)
	}
	configured.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic for an unexpected provider data type")
	}

	response.Diagnostics = nil
	configured.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Network: client, ProjectID: elasticIPDataSourceTestUUID},
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for valid provider data: %v", response.Diagnostics)
	}
	if configured.client != client || configured.projectID != elasticIPDataSourceTestUUID {
		t.Fatal("expected the data source to keep the configured client and project")
	}
}

func TestElasticIPDataSourceSchemaFilters(t *testing.T) {
	t.Parallel()

	schema := elasticIPDataSourceSchema(t)
	for _, name := range []string{"id", "ip_address", "ipv6_address", "status", "region"} {
		attribute, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed {
			t.Fatalf("%s must be an optional, computed filter, got %#v", name, schema.Attributes[name])
		}
	}
	// description identifies nothing: it is free text the practitioner can change.
	description, ok := schema.Attributes["description"].(datasourceschema.StringAttribute)
	if !ok || description.Optional || !description.Computed {
		t.Fatalf("description must be result-only, got %#v", schema.Attributes["description"])
	}
	available, ok := schema.Attributes["available"].(datasourceschema.BoolAttribute)
	if !ok || !available.Optional || available.Computed {
		t.Fatalf("available must be a configuration-only filter, got %#v", schema.Attributes["available"])
	}
	for _, name := range []string{"enable_ipv4", "enable_ipv6"} {
		attribute, ok := schema.Attributes[name].(datasourceschema.BoolAttribute)
		if !ok || attribute.Optional || !attribute.Computed {
			t.Fatalf("%s must be result-only, got %#v", name, schema.Attributes[name])
		}
	}
}

func TestPopulateElasticIPDataSourceStateMapsRegionName(t *testing.T) {
	t.Parallel()

	eip := &networksdk.ElasticIPDetailSchema{
		Id:     elasticIPDataSourceTestUUID,
		Region: networksdk.NestedRegionSchema{Id: core.UUID{9}, Name: "vn-central"},
	}
	var state ElasticIPDataSourceModel
	populateElasticIPDataSourceState(eip, &state)
	if state.Region.ValueString() != "vn-central" {
		t.Fatalf("expected region vn-central, got %s", state.Region.ValueString())
	}
}

func TestElasticIPDataSourceReadByID(t *testing.T) {
	t.Parallel()

	dataSource, requests := elasticIPDataSource(t, func(req *http.Request) (int, string) {
		if req.URL.Path != "/v2/network/elastic-ips/"+elasticIPDataSourceTestUUID.String()+"/" {
			t.Fatalf("unexpected request path %q", req.URL.Path)
		}
		return http.StatusOK, elasticIPDataSourceJSON("Managed by Terraform", "203.0.113.10")
	})

	state := readElasticIPDataSource(t, dataSource, ElasticIPDataSourceModel{
		ElasticIPModel: ElasticIPModel{ID: types.StringValue(elasticIPDataSourceTestUUID.String())},
	})

	if len(requests()) != 1 {
		t.Fatalf("expected a single direct read, got %d requests", len(requests()))
	}
	if state.ID.ValueString() != elasticIPDataSourceTestUUID.String() ||
		state.IPAddress.ValueString() != "203.0.113.10" ||
		state.Region.ValueString() != "vn-central" ||
		state.Status.ValueString() != "active" ||
		state.Description.ValueString() != "Managed by Terraform" ||
		!state.EnableIPv4.ValueBool() ||
		state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" ||
		state.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if !state.Available.IsNull() {
		t.Fatalf("expected available to stay null, got %#v", state.Available)
	}
}

func TestElasticIPDataSourceReadByFilters(t *testing.T) {
	t.Parallel()

	dataSource, requests := elasticIPDataSource(t, func(req *http.Request) (int, string) {
		switch req.URL.Path {
		case "/v2/network/elastic-ips/":
			query := req.URL.Query()
			expected := url.Values{
				"ip_address":  []string{"203.0.113.10"},
				"region_name": []string{"vn-central"},
				"status":      []string{"active"},
				"available":   []string{"true"},
			}
			for key, want := range expected {
				if query.Get(key) != want[0] {
					t.Fatalf("expected query %s=%s, got %q", key, want[0], req.URL.RawQuery)
				}
			}
			return http.StatusOK, fmt.Sprintf(
				`{"count":1,"next":null,"previous":null,"results":[%s]}`,
				elasticIPDataSourceJSON("Managed by Terraform", "203.0.113.10"),
			)
		case "/v2/network/elastic-ips/" + elasticIPDataSourceTestUUID.String() + "/":
			return http.StatusOK, elasticIPDataSourceJSON("Managed by Terraform", "203.0.113.10")
		}
		t.Fatalf("unexpected request path %q", req.URL.Path)
		return 0, ""
	})

	state := readElasticIPDataSource(t, dataSource, ElasticIPDataSourceModel{
		ElasticIPModel: ElasticIPModel{
			IPAddress: types.StringValue("  203.0.113.10  "),
			Status:    types.StringValue("active"),
		},
		Region:    types.StringValue("vn-central"),
		Available: types.BoolValue(true),
	})

	if len(requests()) != 2 {
		t.Fatalf("expected a list followed by a detail read, got %d requests", len(requests()))
	}
	if state.ID.ValueString() != elasticIPDataSourceTestUUID.String() || state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
	if state.IPAddress.ValueString() != "  203.0.113.10  " {
		t.Fatalf("expected the configured address to be preserved, got %q", state.IPAddress.ValueString())
	}
	if !state.Available.ValueBool() {
		t.Fatalf("expected available to stay as configured, got %#v", state.Available)
	}
}

func TestElasticIPDataSourceReadPreservesEquivalentIPv6Representation(t *testing.T) {
	t.Parallel()

	const (
		configuredIPv6 = " 2001:0DB8:0000:0000:0000:0000:0000:0010 "
		backendIPv6    = "2001:db8::10"
	)
	dataSource, requests := elasticIPDataSource(t, func(req *http.Request) (int, string) {
		body := elasticIPDataSourceJSON("Managed by Terraform", "203.0.113.10", backendIPv6)
		switch req.URL.Path {
		case "/v2/network/elastic-ips/":
			if got := req.URL.Query().Get("ipv6_address"); got != backendIPv6 {
				t.Fatalf("expected canonical IPv6 query %q, got %q", backendIPv6, got)
			}
			return http.StatusOK, fmt.Sprintf(
				`{"count":1,"next":null,"previous":null,"results":[%s]}`,
				body,
			)
		case "/v2/network/elastic-ips/" + elasticIPDataSourceTestUUID.String() + "/":
			return http.StatusOK, body
		}
		t.Fatalf("unexpected request path %q", req.URL.Path)
		return 0, ""
	})

	state := readElasticIPDataSource(t, dataSource, ElasticIPDataSourceModel{
		ElasticIPModel: ElasticIPModel{IPv6Address: types.StringValue(configuredIPv6)},
	})

	if len(requests()) != 2 {
		t.Fatalf("expected a list followed by a detail read, got %d requests", len(requests()))
	}
	if state.IPv6Address.ValueString() != configuredIPv6 {
		t.Fatalf("expected configured IPv6 representation to be preserved, got %q", state.IPv6Address.ValueString())
	}
}

func TestElasticIPDataSourceReadRejectsInvalidCriteria(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		config          ElasticIPDataSourceModel
		expectedSummary string
	}{
		{
			name:            "no criteria",
			config:          ElasticIPDataSourceModel{},
			expectedSummary: "Missing Elastic IP lookup criteria",
		},
		{
			name: "blank address filter",
			config: ElasticIPDataSourceModel{
				ElasticIPModel: ElasticIPModel{IPAddress: types.StringValue("   ")},
			},
			expectedSummary: "Invalid Elastic IP ip_address filter",
		},
		{
			name: "blank region filter",
			config: ElasticIPDataSourceModel{
				ElasticIPModel: ElasticIPModel{ID: types.StringValue(elasticIPDataSourceTestUUID.String())},
				Region:         types.StringValue(" "),
			},
			expectedSummary: "Invalid Elastic IP region filter",
		},
		{
			name: "invalid uuid",
			config: ElasticIPDataSourceModel{
				ElasticIPModel: ElasticIPModel{ID: types.StringValue("not-a-uuid")},
			},
			expectedSummary: "Invalid UUID",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dataSource, _ := elasticIPDataSource(t, func(req *http.Request) (int, string) {
				t.Fatalf("no request expected, got %s %s", req.Method, req.URL.Path)
				return 0, ""
			})
			assertElasticIPDataSourceDiagnostic(t, dataSource, test.config, test.expectedSummary)
		})
	}
}

func TestElasticIPDataSourceReportsAPIErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		status          int
		expectedSummary string
	}{
		{name: "not found", status: http.StatusNotFound, expectedSummary: "Elastic IP not found"},
		{name: "backend error", status: http.StatusInternalServerError, expectedSummary: "Error reading Elastic IP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dataSource, _ := elasticIPDataSource(t, func(req *http.Request) (int, string) {
				if req.Method != http.MethodGet || req.URL.Path != "/v2/network/elastic-ips/"+elasticIPDataSourceTestUUID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Project-ID") != elasticIPDataSourceTestUUID.String() {
					t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
				}
				return test.status, `{"detail":"backend response"}`
			})
			assertElasticIPDataSourceDiagnostic(t, dataSource, ElasticIPDataSourceModel{
				ElasticIPModel: ElasticIPModel{ID: types.StringValue(elasticIPDataSourceTestUUID.String())},
			}, test.expectedSummary)
		})
	}
}

func TestElasticIPDataSourceReportsLookupCardinality(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		results string
		detail  string
	}{
		{name: "zero matches", results: "", detail: "was not found"},
		{
			name: "ambiguous matches",
			results: elasticIPDataSourceJSON("first", "203.0.113.10") + "," +
				strings.Replace(
					elasticIPDataSourceJSON("second", "203.0.113.10"),
					elasticIPDataSourceTestUUID.String(),
					core.UUID{2}.String(),
					1,
				),
			detail: "is ambiguous",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dataSource, _ := elasticIPDataSource(t, func(req *http.Request) (int, string) {
				if req.URL.Path != "/v2/network/elastic-ips/" {
					t.Fatalf("unexpected request path %q", req.URL.Path)
				}
				return http.StatusOK, fmt.Sprintf(`{"count":0,"next":null,"previous":null,"results":[%s]}`, test.results)
			})

			response := readElasticIPDataSourceResponse(t, dataSource, ElasticIPDataSourceModel{
				ElasticIPModel: ElasticIPModel{IPAddress: types.StringValue("203.0.113.10")},
			})
			diagnostics := response.Diagnostics
			if !diagnostics.HasError() || diagnostics[0].Summary() != "Error resolving Elastic IP" {
				t.Fatalf("expected a resolution diagnostic, got %v", diagnostics)
			}
			if !strings.Contains(diagnostics[0].Detail(), test.detail) {
				t.Fatalf("expected detail to contain %q, got %q", test.detail, diagnostics[0].Detail())
			}
		})
	}
}

func elasticIPDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()

	var response datasource.SchemaResponse
	(&ElasticIPDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	return response.Schema
}

// elasticIPDataSource serves the responses respond returns and records every
// request, so lookup paths are asserted without a backend.
func elasticIPDataSource(t *testing.T, respond func(*http.Request) (int, string)) (*ElasticIPDataSource, func() []string) {
	t.Helper()

	var paths []string
	httpClient := &http.Client{Transport: elasticIPDataSourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		status, body := respond(req)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	client, err := networksdk.NewClient(
		"https://network.test",
		networksdk.WithHTTPClient(httpClient),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	return &ElasticIPDataSource{client: client, projectID: elasticIPDataSourceTestUUID},
		func() []string { return paths }
}

func readElasticIPDataSource(
	t *testing.T,
	dataSource *ElasticIPDataSource,
	config ElasticIPDataSourceModel,
) ElasticIPDataSourceModel {
	t.Helper()

	response := readElasticIPDataSourceResponse(t, dataSource, config)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected read diagnostics: %v", response.Diagnostics)
	}

	var state ElasticIPDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read data source state: %v", diags)
	}
	return state
}

func assertElasticIPDataSourceDiagnostic(
	t *testing.T,
	dataSource *ElasticIPDataSource,
	config ElasticIPDataSourceModel,
	expectedSummary string,
) {
	t.Helper()

	response := readElasticIPDataSourceResponse(t, dataSource, config)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != expectedSummary {
		t.Fatalf("expected diagnostic %q, got %v", expectedSummary, response.Diagnostics)
	}
}

func readElasticIPDataSourceResponse(
	t *testing.T,
	dataSource *ElasticIPDataSource,
	config ElasticIPDataSourceModel,
) datasource.ReadResponse {
	t.Helper()

	schema := elasticIPDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	if diags := configState.Set(context.Background(), &config); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}

	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	dataSource.Read(
		context.Background(),
		datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: schema}},
		&response,
	)
	return response
}

func elasticIPDataSourceJSON(description, ipAddress string, ipv6Addresses ...string) string {
	ipv6Address := ""
	enableIPv6 := false
	if len(ipv6Addresses) > 0 {
		enableIPv6 = true
		ipv6Address = fmt.Sprintf(`"ipv6_address":%q,`, ipv6Addresses[0])
	}
	return fmt.Sprintf(
		`{"id":%q,"description":%q,"display_name":"eip","enable_ipv4":true,"enable_ipv6":%t,`+
			`"ip_address":%q,%s"status":"active",`+
			`"project":{"id":%q,"name":"project","slug":"project"},`+
			`"region":{"id":%q,"name":"vn-central","description":""},`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`,
		elasticIPDataSourceTestUUID.String(), description, enableIPv6, ipAddress, ipv6Address,
		elasticIPDataSourceTestUUID.String(), elasticIPDataSourceTestUUID.String(),
	)
}
