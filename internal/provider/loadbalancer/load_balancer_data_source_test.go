package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"

	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func loadBalancerTestClient(t *testing.T, roundTrip loadBalancerRoundTripFunc) *loadbalancersdk.Client {
	t.Helper()
	client, err := loadbalancersdk.NewClient(
		"https://loadbalancer.test",
		loadbalancersdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return client
}

func projectTestClient(t *testing.T, roundTrip loadBalancerRoundTripFunc) *projectsdk.Client {
	t.Helper()
	client, err := projectsdk.NewClient(
		"https://project.test",
		projectsdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		projectsdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return client
}

func loadBalancerTestHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleLoadBalancerDetail() *loadbalancersdk.LoadBalancerDetailSchema {
	vip := "10.0.0.10"
	detail := &loadbalancersdk.LoadBalancerDetailSchema{
		Id:                 loadBalancerTestID,
		Name:               "primary",
		Package:            loadbalancersdk.LoadBalancerPackageSMALL,
		Topology:           loadbalancersdk.LoadBalancerTopologySINGLE,
		Type:               loadbalancersdk.LoadBalancerTypeNative,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Region: loadbalancersdk.NestedRegionSchema{
			Id:   loadBalancerTestRegionID,
			Name: "vn-central-1",
		},
		Vpc: loadbalancersdk.NestedVPCSchema{
			Id:   loadBalancerTestVPCID,
			Name: "app-vpc",
		},
		VipAddress: core.NullableFromPtr(&vip),
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
	// The backend sends null for a Load Balancer without a description. An
	// unset Nullable would marshal as "" because the field has no omitempty.
	detail.Description.SetNull()
	return detail
}

func loadBalancerDetailJSON() string {
	body, err := json.Marshal(sampleLoadBalancerDetail())
	if err != nil {
		panic(err)
	}
	return string(body)
}

func loadBalancerListJSON() string {
	page := loadbalancersdk.PagedLoadBalancerSchema{
		Count: 1,
		Results: []loadbalancersdk.LoadBalancerSchema{{
			Id:                 loadBalancerTestID,
			Name:               "primary",
			Package:            loadbalancersdk.LoadBalancerPackageSMALL,
			Topology:           loadbalancersdk.LoadBalancerTopologySINGLE,
			Type:               loadbalancersdk.LoadBalancerTypeNative,
			Status:             loadbalancersdk.LoadBalancingStatusONLINE,
			ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			Region: loadbalancersdk.NestedRegionSchema{
				Id:   loadBalancerTestRegionID,
				Name: "vn-central-1",
			},
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		}},
	}
	body, err := json.Marshal(page)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func regionListJSON() string {
	page := projectsdk.PagedProjectRegionSchema{
		Count: 1,
		Results: []projectsdk.ProjectRegionSchema{{
			Region: projectsdk.NestedRegionSchema{
				Id:   loadBalancerTestRegionID,
				Name: "vn-central-1",
			},
		}},
	}
	body, err := json.Marshal(page)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func loadBalancerDataSourceSchema(ctx context.Context, t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&LoadBalancerDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("build load balancer data source schema: %v", response.Diagnostics)
	}
	return response.Schema
}

// loadBalancerDataSourceConfig builds a datasource.ReadRequest config. tfsdk.Config
// exposes no setter, so the value is round-tripped through a state of the same
// schema.
func loadBalancerDataSourceConfig(ctx context.Context, t *testing.T, model LoadBalancerDataSourceModel) tfsdk.Config {
	t.Helper()
	schema := loadBalancerDataSourceSchema(ctx, t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set load balancer data source config: %v", diags)
	}
	return tfsdk.Config{Schema: schema, Raw: state.Raw}
}

func TestLoadBalancerDataSourceNewAndMetadata(t *testing.T) {
	t.Parallel()
	ds := NewLoadBalancerDataSource()
	if ds == nil {
		t.Fatal("expected non-nil data source")
	}
	var resp datasource.MetadataResponse
	ds.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_lb_loadbalancer" {
		t.Fatalf("expected TypeName=viettelcloud_lb_loadbalancer, got %q", resp.TypeName)
	}
}

func TestLoadBalancerDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()
	var response datasource.SchemaResponse
	(&LoadBalancerDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	if diags := state.Set(context.Background(), &LoadBalancerDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
	for name, attribute := range response.Schema.Attributes {
		filter := name == "id" || name == "name" || name == "region" || name == "vip_address" || name == "public_address" || name == "v6_public_address" || name == "package" || name == "topology" || name == "type" || name == "access_log_enabled"
		if !attribute.IsComputed() || attribute.IsOptional() != filter || attribute.IsRequired() {
			t.Errorf("unexpected flags for %s: optional=%t computed=%t required=%t", name, attribute.IsOptional(), attribute.IsComputed(), attribute.IsRequired())
		}
	}
}

func TestLoadBalancerDataSourceConfigure(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	})
	d := &LoadBalancerDataSource{}
	var unconfigured datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &unconfigured)
	if unconfigured.Diagnostics.HasError() || d.client != nil {
		t.Fatalf("nil provider data changed the data source: %v", unconfigured.Diagnostics)
	}
	var invalid datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 42}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}
	var response datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{
		LoadBalancer: client,
		Project:      projectTestClient(t, nil),
		ProjectID:    loadBalancerTestProjectID,
	}}, &response)
	if response.Diagnostics.HasError() || d.client != client || d.project == nil || d.projectID != loadBalancerTestProjectID {
		t.Fatalf("unexpected configure result: data source=%#v diagnostics=%v", d, response.Diagnostics)
	}
}

func TestLoadBalancerDataSourceGetByID(t *testing.T) {
	t.Parallel()
	getCalls := 0
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()) {
			getCalls++
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		return nil, nil
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		ID: types.StringValue(loadBalancerTestID.String()),
	})
	if diags.HasError() || lb == nil || lb.Id != loadBalancerTestID || getCalls != 1 {
		t.Fatalf("unexpected result: lb=%#v get=%d diags=%v", lb, getCalls, diags)
	}
}

func TestLoadBalancerDataSourceGetByName(t *testing.T) {
	t.Parallel()
	listCalls := 0
	getCalls := 0
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			listCalls++
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()):
			getCalls++
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Name: types.StringValue(" primary "),
	})
	if diags.HasError() || lb == nil || listCalls != 1 || getCalls != 1 {
		t.Fatalf("unexpected result: lb=%#v list=%d get=%d diags=%v", lb, listCalls, getCalls, diags)
	}
}

func TestLoadBalancerDataSourceGetByNameAndRegion(t *testing.T) {
	t.Parallel()
	var sawRegionID bool
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			if req.URL.Query().Get("region_id") == loadBalancerTestRegionID.String() {
				sawRegionID = true
			}
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()):
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	project := projectTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "regions") {
			return loadBalancerTestHTTPResponse(req, http.StatusOK, regionListJSON()), nil
		}
		t.Fatalf("unexpected project request: %s %s", req.Method, req.URL)
		return nil, nil
	})
	d := &LoadBalancerDataSource{client: client, project: project, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Name:   types.StringValue("primary"),
		Region: types.StringValue("vn-central-1"),
	})
	if diags.HasError() || lb == nil || !sawRegionID {
		t.Fatalf("unexpected result: lb=%#v regionFilter=%v diags=%v", lb, sawRegionID, diags)
	}
}

func TestLoadBalancerDataSourceRejectsInvalidCriteria(t *testing.T) {
	t.Parallel()
	d := &LoadBalancerDataSource{}
	tests := []struct {
		name   string
		config LoadBalancerDataSourceModel
		want   string
	}{
		{
			name:   "missing",
			config: LoadBalancerDataSourceModel{},
			want:   "Missing Load Balancer lookup criteria",
		},
		{
			name: "blank name",
			config: LoadBalancerDataSourceModel{
				Name: types.StringValue(" "),
			},
			want: "Invalid Load Balancer name filter",
		},
		{
			name: "blank region",
			config: LoadBalancerDataSourceModel{
				Name:   types.StringValue("primary"),
				Region: types.StringValue(" "),
			},
			want: "Invalid Load Balancer region filter",
		},
	}
	for _, criterion := range []string{"id", "name", "region"} {
		for _, value := range []types.String{types.StringUnknown(), types.StringValue(""), types.StringValue(" ")} {
			model := LoadBalancerDataSourceModel{Name: types.StringValue("primary")}
			want := "Unknown Load Balancer lookup criteria"
			if !value.IsUnknown() {
				want = fmt.Sprintf("Invalid Load Balancer %s filter", criterion)
				if criterion == "id" {
					want = "UUID"
				}
			}
			switch criterion {
			case "id":
				model.ID = value
			case "name":
				model.Name = value
			case "region":
				model.Region = value
			}
			tests = append(tests, struct {
				name   string
				config LoadBalancerDataSourceModel
				want   string
			}{criterion + "/" + value.String(), model, want})
		}
	}
	for _, withName := range []bool{false, true} {
		model := LoadBalancerDataSourceModel{ID: types.StringValue("not-a-uuid")}
		if withName {
			model.Name = types.StringValue("primary")
		}
		tests = append(tests, struct {
			name   string
			config LoadBalancerDataSourceModel
			want   string
		}{fmt.Sprintf("invalid ID with name=%t", withName), model, "Invalid UUID"})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, diags := d.getLoadBalancer(context.Background(), tc.config)
			if !diags.HasError() {
				t.Fatalf("expected error diagnostic containing %q", tc.want)
			}
			if !strings.Contains(diags.Errors()[0].Summary(), tc.want) {
				t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
			}
		})
	}
}

func TestLoadBalancerDataSourceReadPopulatesState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schema := loadBalancerDataSourceSchema(ctx, t)
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()) {
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		return nil, nil
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	config := loadBalancerDataSourceConfig(ctx, t, LoadBalancerDataSourceModel{
		ID: types.StringValue(loadBalancerTestID.String()),
	})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	d.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var state LoadBalancerDataSourceModel
	if diags := response.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.Name.ValueString() != "primary" {
		t.Fatalf("unexpected name: %q", state.Name.ValueString())
	}
	if state.Region.ValueString() != "vn-central-1" {
		t.Fatalf("unexpected region: %q", state.Region.ValueString())
	}
	if state.VPCID.ValueString() != loadBalancerTestVPCID.String() {
		t.Fatalf("unexpected vpc_id: %q", state.VPCID.ValueString())
	}
}

func TestLoadBalancerDataSourceGetByIDNotFound(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		return loadBalancerTestHTTPResponse(req, http.StatusNotFound, `{"code":"NOT_FOUND","message":"not found"}`), nil
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		ID: types.StringValue(loadBalancerTestID.String()),
	})
	if !diags.HasError() || lb != nil {
		t.Fatalf("expected error for not found, got lb=%#v diags=%v", lb, diags)
	}
	if !strings.Contains(diags.Errors()[0].Summary(), "Load Balancer not found") {
		t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
	}
}

func TestLoadBalancerDataSourceGetByIDDeleted(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		detail := sampleLoadBalancerDetail()
		detail.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
		body, _ := json.Marshal(detail)
		return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		ID: types.StringValue(loadBalancerTestID.String()),
	})
	if !diags.HasError() || lb != nil {
		t.Fatalf("expected error for DELETED state, got lb=%#v diags=%v", lb, diags)
	}
	if !strings.Contains(diags.Errors()[0].Summary(), "Load Balancer not found") {
		t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
	}
	if !strings.Contains(diags.Errors()[0].Detail(), "DELETED state") {
		t.Fatalf("unexpected detail: %q", diags.Errors()[0].Detail())
	}
}

func TestLoadBalancerDataSourceGetByFilterNotFound(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()):
			return loadBalancerTestHTTPResponse(req, http.StatusNotFound, `{"code":"NOT_FOUND","message":"not found"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Name: types.StringValue("primary"),
	})
	if !diags.HasError() || lb != nil {
		t.Fatalf("expected error for filter not found, got lb=%#v diags=%v", lb, diags)
	}
	if !strings.Contains(diags.Errors()[0].Summary(), "Load Balancer not found") {
		t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
	}
}

func TestLoadBalancerDataSourceGetByFilterDeleted(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()):
			detail := sampleLoadBalancerDetail()
			detail.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
			body, _ := json.Marshal(detail)
			return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Name: types.StringValue("primary"),
	})
	if !diags.HasError() || lb != nil {
		t.Fatalf("expected error for filter DELETED state, got lb=%#v diags=%v", lb, diags)
	}
	if !strings.Contains(diags.Errors()[0].Summary(), "Load Balancer not found") {
		t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
	}
	if !strings.Contains(diags.Errors()[0].Detail(), "DELETED state") {
		t.Fatalf("unexpected detail: %q", diags.Errors()[0].Detail())
	}
}

func TestLoadBalancerDataSourceGetByFilterListDeletedExcluded(t *testing.T) {
	t.Parallel()
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		page := loadbalancersdk.PagedLoadBalancerSchema{
			Count: 1,
			Results: []loadbalancersdk.LoadBalancerSchema{{
				Id:                 loadBalancerTestID,
				Name:               "primary",
				Package:            loadbalancersdk.LoadBalancerPackageSMALL,
				Topology:           loadbalancersdk.LoadBalancerTopologySINGLE,
				Type:               loadbalancersdk.LoadBalancerTypeNative,
				Status:             loadbalancersdk.LoadBalancingStatusONLINE,
				ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusDELETED,
				Region: loadbalancersdk.NestedRegionSchema{
					Id:   loadBalancerTestRegionID,
					Name: "vn-central-1",
				},
			}},
		}
		body, _ := json.Marshal(page)
		return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
	})
	d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Name: types.StringValue("primary"),
	})
	if !diags.HasError() || lb != nil {
		t.Fatalf("expected error for excluded deleted candidate, got lb=%#v diags=%v", lb, diags)
	}
	if !strings.Contains(diags.Errors()[0].Summary(), "Error resolving Load Balancer") {
		t.Fatalf("unexpected summary: %q", diags.Errors()[0].Summary())
	}
}

func TestLoadBalancerDataSourceGetByRegionAlone(t *testing.T) {
	t.Parallel()
	var sawRegionID bool
	client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			if req.URL.Query().Get("region_id") == loadBalancerTestRegionID.String() {
				sawRegionID = true
			}
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, loadBalancerTestID.String()):
			return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	project := projectTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "regions") {
			return loadBalancerTestHTTPResponse(req, http.StatusOK, regionListJSON()), nil
		}
		t.Fatalf("unexpected project request: %s %s", req.Method, req.URL)
		return nil, nil
	})
	d := &LoadBalancerDataSource{client: client, project: project, projectID: loadBalancerTestProjectID}

	lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
		Region: types.StringValue("vn-central-1"),
	})
	if diags.HasError() || lb == nil || !sawRegionID {
		t.Fatalf("unexpected result: lb=%#v sawRegionID=%v diags=%v", lb, sawRegionID, diags)
	}
}

func TestLoadBalancerDataSourcePaginatedLookup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		names      []string
		filterID   types.String
		pageError  bool
		wantDetail string
	}{
		{name: "match on second page", names: []string{"primary-other", "primary"}},
		{name: "ambiguity on later page", names: []string{"primary", "primary"}, wantDetail: "multiple"},
		{name: "no exact matches", names: []string{"primary-other", "Primary"}, wantDetail: "was not found"},
		{name: "ID narrows duplicate names", names: []string{"primary", "primary"}, filterID: types.StringValue(loadBalancerTestID.String())},
		{name: "ID conflicts with name", names: []string{"primary", "other"}, filterID: types.StringValue(loadBalancerTestID.String()), wantDetail: "was not found"},
		{name: "later page fails after match", names: []string{"primary", "primary"}, pageError: true, wantDetail: "page failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			listCalls, getCalls := 0, 0
			client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
					t.Fatalf("unexpected scope or method: %s %s", req.Method, req.URL)
				}
				if req.URL.Path == "/v2/load-balancing/loadbalancers/" {
					listCalls++
					if req.URL.Query().Get("page_number") != fmt.Sprint(listCalls) || req.URL.Query().Get("name") != "primary" {
						t.Fatalf("unexpected page or name: %s", req.URL)
					}
					if listCalls == 2 && tc.pageError {
						return nil, errors.New("page failed")
					}
					if listCalls > len(tc.names) {
						t.Fatal("unexpected extra list page")
					}
					row := loadbalancersdk.LoadBalancerSchema{
						Id:   core.UUID{99},
						Name: tc.names[listCalls-1],
						Region: loadbalancersdk.NestedRegionSchema{
							Id:   loadBalancerTestRegionID,
							Name: "vn-central-1",
						},
						ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
					}
					if listCalls == 2 {
						row.Id = loadBalancerTestID
					}
					body, err := json.Marshal(loadbalancersdk.PagedLoadBalancerSchema{Count: len(tc.names), Results: []loadbalancersdk.LoadBalancerSchema{row}})
					if err != nil {
						t.Fatal(err)
					}
					return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
				}
				if req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
					t.Fatalf("unexpected detail request: %s", req.URL)
				}
				getCalls++
				return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerDetailJSON()), nil
			})
			d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}
			lb, diags := d.getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{
				ID:   tc.filterID,
				Name: types.StringValue(" primary "),
			})
			if listCalls != 2 {
				t.Fatalf("expected two list pages, got %d", listCalls)
			}
			if tc.wantDetail != "" {
				if !diags.HasError() || lb != nil || getCalls != 0 || !strings.Contains(diags.Errors()[0].Detail(), tc.wantDetail) {
					t.Fatalf("expected lookup failure %q without detail request, got lb=%#v get=%d diagnostics=%v", tc.wantDetail, lb, getCalls, diags)
				}
				return
			}
			if diags.HasError() || lb == nil || lb.Id != loadBalancerTestID || getCalls != 1 {
				t.Fatalf("unexpected result: lb=%#v get=%d diagnostics=%v", lb, getCalls, diags)
			}
		})
	}
}

func TestLoadBalancerDataSourceAPIErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config LoadBalancerDataSourceModel
		status int
		want   string
	}{
		{name: "direct detail fails", config: LoadBalancerDataSourceModel{ID: types.StringValue(loadBalancerTestID.String())}, status: http.StatusForbidden, want: "Error reading Load Balancer"},
		{name: "list fails", config: LoadBalancerDataSourceModel{Name: types.StringValue("primary")}, status: http.StatusInternalServerError, want: "Error resolving Load Balancer"},
		{name: "region resolution fails", config: LoadBalancerDataSourceModel{Region: types.StringValue("vn-central-1")}, status: http.StatusForbidden, want: "Error resolving region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return loadBalancerTestHTTPResponse(req, tc.status, `{"message":"access failed"}`), nil
			})
			d := &LoadBalancerDataSource{client: loadBalancerTestClient(t, transport), project: projectTestClient(t, transport), projectID: loadBalancerTestProjectID}
			lb, diags := d.getLoadBalancer(context.Background(), tc.config)
			if !diags.HasError() || lb != nil || diags.Errors()[0].Summary() != tc.want {
				t.Fatalf("expected %q, got lb=%#v diagnostics=%v", tc.want, lb, diags)
			}
		})
	}
}

func TestLoadBalancerDataSourceReadFullStateAndNormalization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, withAssociations := range []bool{false, true} {
		t.Run(fmt.Sprintf("associations=%t", withAssociations), func(t *testing.T) {
			t.Parallel()
			detail := sampleLoadBalancerDetail()
			detail.Id = core.UUID{0xab}
			want := LoadBalancerDataSourceModel{
				ID:                 types.StringValue(strings.ToUpper(detail.Id.String())),
				Name:               types.StringValue("  primary  "),
				Package:            types.StringValue("SMALL"),
				Topology:           types.StringValue("SINGLE"),
				RegionID:           types.StringValue(loadBalancerTestRegionID.String()),
				VPCName:            types.StringValue("app-vpc"),
				VIPAddress:         types.StringValue("10.0.0.10"),
				Status:             types.StringValue("ONLINE"),
				ProvisioningStatus: types.StringValue("ACTIVE"),
				CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
				UpdatedAt:          types.StringValue("2026-01-02T00:00:00Z"),
				Region:             types.StringValue("  vn-central-1  "),
				VPCID:              types.StringValue(loadBalancerTestVPCID.String()),
			}
			want.AccessLogEnabled = types.BoolValue(false)
			want.Type = types.StringValue("native")
			if withAssociations {
				detail.Description.Set("")
				detail.PrivateIpId.Set(loadBalancerTestPrivateIPID)
				detail.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{Id: loadBalancerTestQoSID, Name: "qos-silver"})
				detail.PublicAddress.Set("192.0.2.1")
				detail.V6PublicAddress.Set("2001:db8::1")
				want.Description = types.StringValue("")
				want.PrivateIPID = types.StringValue(loadBalancerTestPrivateIPID.String())
				want.QoSPolicyID = types.StringValue(loadBalancerTestQoSID.String())
				want.QoSPolicyName = types.StringValue("qos-silver")
				want.PublicAddress = types.StringValue("192.0.2.1")
				want.V6PublicAddress = types.StringValue("2001:db8::1")
			}
			client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/v2/load-balancing/loadbalancers/" {
					body, err := json.Marshal(loadbalancersdk.PagedLoadBalancerSchema{Count: 1, Results: []loadbalancersdk.LoadBalancerSchema{{Id: detail.Id, Name: detail.Name, Region: detail.Region}}})
					if err != nil {
						t.Fatal(err)
					}
					return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
				}
				return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerJSON(t, detail)), nil
			})
			project := projectTestClient(t, func(req *http.Request) (*http.Response, error) {
				return loadBalancerTestHTTPResponse(req, http.StatusOK, regionListJSON()), nil
			})
			d := &LoadBalancerDataSource{client: client, project: project, projectID: loadBalancerTestProjectID}
			config := LoadBalancerDataSourceModel{ID: want.ID, Name: want.Name, PrivateIPID: types.StringUnknown(), QoSPolicyID: types.StringUnknown(), QoSPolicyName: types.StringUnknown(), Region: want.Region}
			response := datasource.ReadResponse{State: tfsdk.State{Schema: loadBalancerDataSourceSchema(ctx, t)}}
			d.Read(ctx, datasource.ReadRequest{Config: loadBalancerDataSourceConfig(ctx, t, config)}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("read failed: %v", response.Diagnostics)
			}
			expected := tfsdk.State{Schema: response.State.Schema}
			if diags := expected.Set(ctx, &want); diags.HasError() {
				t.Fatal(diags)
			}
			if !response.State.Raw.Equal(expected.Raw) {
				t.Fatalf("unexpected state:\ngot %s\nwant %s", response.State.Raw, expected.Raw)
			}
		})
	}
}

func TestPopulateLoadBalancerDataSourceState(t *testing.T) {
	t.Parallel()

	// nil safety check
	populateLoadBalancerDataSourceState(nil, &LoadBalancerDataSourceModel{})

	privIPID := core.UUID{10}
	qosID := core.UUID{11}
	desc := "test description"
	pubIPv4 := "192.0.2.1"
	pubIPv6 := "2001:db8::1"

	detail := sampleLoadBalancerDetail()
	detail.PrivateIpId.Set(privIPID)
	detail.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{
		Id:   qosID,
		Name: "qos-silver",
	})
	detail.Description.Set(desc)
	detail.PublicAddress.Set(pubIPv4)
	detail.V6PublicAddress.Set(pubIPv6)

	var state LoadBalancerDataSourceModel
	populateLoadBalancerDataSourceState(detail, &state)

	if state.PrivateIPID.ValueString() != privIPID.String() {
		t.Fatalf("expected private_ip_id=%s, got %s", privIPID, state.PrivateIPID.ValueString())
	}
	if state.QoSPolicyID.ValueString() != qosID.String() {
		t.Fatalf("expected qos_policy_id=%s, got %s", qosID, state.QoSPolicyID.ValueString())
	}
	if state.QoSPolicyName.ValueString() != "qos-silver" {
		t.Fatalf("expected qos_policy_name=qos-silver, got %s", state.QoSPolicyName.ValueString())
	}
	if state.Description.ValueString() != desc {
		t.Fatalf("expected description=%s, got %s", desc, state.Description.ValueString())
	}
	if state.PublicAddress.ValueString() != pubIPv4 {
		t.Fatalf("expected public_address=%s, got %s", pubIPv4, state.PublicAddress.ValueString())
	}
	if state.V6PublicAddress.ValueString() != pubIPv6 {
		t.Fatalf("expected v6_public_address=%s, got %s", pubIPv6, state.V6PublicAddress.ValueString())
	}
}

func TestPreserveConfiguredLoadBalancerValues(t *testing.T) {
	t.Parallel()

	// nil safety check
	preserveConfiguredLoadBalancerValues(nil, LoadBalancerDataSourceModel{}, &LoadBalancerDataSourceModel{})

	detail := sampleLoadBalancerDetail()

	// Matching values with leading/trailing whitespace
	config := LoadBalancerDataSourceModel{
		ID:     types.StringValue(loadBalancerTestID.String()),
		Name:   types.StringValue("  primary  "),
		Region: types.StringValue("  vn-central-1  "),
	}
	state := LoadBalancerDataSourceModel{}
	preserveConfiguredLoadBalancerValues(detail, config, &state)

	if state.ID.ValueString() != config.ID.ValueString() {
		t.Fatalf("expected preserved ID %q, got %q", config.ID.ValueString(), state.ID.ValueString())
	}
	if state.Name.ValueString() != config.Name.ValueString() {
		t.Fatalf("expected preserved Name %q, got %q", config.Name.ValueString(), state.Name.ValueString())
	}
	if state.Region.ValueString() != config.Region.ValueString() {
		t.Fatalf("expected preserved Region %q, got %q", config.Region.ValueString(), state.Region.ValueString())
	}

	// Mismatched values
	mismatchedConfig := LoadBalancerDataSourceModel{
		ID:     types.StringValue(core.UUID{99}.String()),
		Name:   types.StringValue("different"),
		Region: types.StringValue("different-region"),
	}
	mismatchedState := LoadBalancerDataSourceModel{}
	preserveConfiguredLoadBalancerValues(detail, mismatchedConfig, &mismatchedState)

	if !mismatchedState.ID.IsNull() && mismatchedState.ID.ValueString() == mismatchedConfig.ID.ValueString() {
		t.Fatalf("mismatched ID should not be preserved")
	}
	if !mismatchedState.Name.IsNull() && mismatchedState.Name.ValueString() == mismatchedConfig.Name.ValueString() {
		t.Fatalf("mismatched Name should not be preserved")
	}
	if !mismatchedState.Region.IsNull() && mismatchedState.Region.ValueString() == mismatchedConfig.Region.ValueString() {
		t.Fatalf("mismatched Region should not be preserved")
	}
}

func TestLoadBalancerDataSourceAdditionalFilters(t *testing.T) {
	t.Parallel()
	for _, criterion := range []string{"vip_address", "public_address", "v6_public_address", "package", "topology", "type", "access_log_enabled", "combined"} {
		t.Run(criterion, func(t *testing.T) {
			t.Parallel()
			detail := sampleLoadBalancerDetail()
			detail.PublicAddress.Set("192.0.2.1")
			detail.V6PublicAddress.Set("2001:db8::1")
			config := LoadBalancerDataSourceModel{}
			wantQuery := map[string]string{}
			set := func(name string) {
				switch name {
				case "vip_address":
					config.VIPAddress = types.StringValue(" 10.0.0.10 ")
					wantQuery[name] = "10.0.0.10"
				case "public_address":
					config.PublicAddress = types.StringValue(" 192.0.2.1 ")
					wantQuery[name] = "192.0.2.1"
				case "v6_public_address":
					config.V6PublicAddress = types.StringValue(" 2001:0DB8:0000::0001 ")
					wantQuery[name] = "2001:db8::1"
				case "package":
					config.Package = types.StringValue(" SMALL ")
					wantQuery[name] = "SMALL"
				case "topology":
					config.Topology = types.StringValue("\tSINGLE ")
					wantQuery[name] = "SINGLE"
				case "type":
					config.Type = types.StringValue(" native\n")
					wantQuery[name] = "native"
				case "access_log_enabled":
					config.AccessLogEnabled = types.BoolValue(false)
					wantQuery[name] = "false"
				}
			}
			if criterion == "combined" {
				config.ID = types.StringValue(strings.ToUpper(detail.Id.String()))
				for _, name := range []string{"vip_address", "public_address", "v6_public_address", "package", "topology", "type", "access_log_enabled"} {
					set(name)
				}
			} else {
				set(criterion)
			}
			listCalls, getCalls := 0, 0
			client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
					t.Errorf("unexpected project header: %v", req.Header)
				}
				if req.URL.Path == "/v2/load-balancing/loadbalancers/" {
					listCalls++
					for name, want := range wantQuery {
						if req.URL.Query().Get(name) != want {
							t.Errorf("query %s=%q, want %q", name, req.URL.Query().Get(name), want)
						}
					}
					row := loadbalancersdk.LoadBalancerSchema{
						Id:               detail.Id,
						Name:             detail.Name,
						Region:           detail.Region,
						Package:          detail.Package,
						Topology:         detail.Topology,
						Type:             detail.Type,
						AccessLogEnabled: detail.AccessLogEnabled,
						VipAddress:       detail.VipAddress,
						PublicAddress:    detail.PublicAddress,
						V6PublicAddress:  detail.V6PublicAddress,
					}
					body, err := json.Marshal(loadbalancersdk.PagedLoadBalancerSchema{Count: 1, Results: []loadbalancersdk.LoadBalancerSchema{row}})
					if err != nil {
						t.Fatal(err)
					}
					return loadBalancerTestHTTPResponse(req, http.StatusOK, string(body)), nil
				}
				getCalls++
				return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerJSON(t, detail)), nil
			})
			d := &LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}
			ctx := context.Background()
			resp := datasource.ReadResponse{State: tfsdk.State{Schema: loadBalancerDataSourceSchema(ctx, t)}}
			d.Read(ctx, datasource.ReadRequest{Config: loadBalancerDataSourceConfig(ctx, t, config)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if listCalls != 1 || getCalls != 1 {
				t.Fatalf("list calls=%d get calls=%d", listCalls, getCalls)
			}
			var state LoadBalancerDataSourceModel
			if diags := resp.State.Get(ctx, &state); diags.HasError() {
				t.Fatal(diags)
			}
			if strings.TrimSpace(state.Type.ValueString()) != "native" || state.AccessLogEnabled.IsNull() || state.AccessLogEnabled.ValueBool() || state.VPCID.ValueString() != detail.Vpc.Id.String() {
				t.Fatalf("unexpected detail mapping: %+v", state)
			}
			for _, pair := range [][2]types.String{
				{config.ID, state.ID},
				{config.VIPAddress, state.VIPAddress},
				{config.PublicAddress, state.PublicAddress},
				{config.V6PublicAddress, state.V6PublicAddress},
				{config.Package, state.Package},
				{config.Topology, state.Topology},
				{config.Type, state.Type},
			} {
				if !pair[0].IsNull() && !pair[0].Equal(pair[1]) {
					t.Errorf("configured %s became %s", pair[0], pair[1])
				}
			}
		})
	}
}

func TestLoadBalancerDataSourceRejectsInvalidAdditionalFilters(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"vip_address", "public_address", "v6_public_address", "package", "topology", "type"} {
		for _, value := range []types.String{types.StringUnknown(), types.StringValue(""), types.StringValue(" "), types.StringValue("invalid"), types.StringValue("fe80::1%eth0")} {
			t.Run(name+"/"+value.String(), func(t *testing.T) {
				t.Parallel()
				config := LoadBalancerDataSourceModel{}
				switch name {
				case "vip_address":
					config.VIPAddress = value
				case "public_address":
					config.PublicAddress = value
				case "v6_public_address":
					config.V6PublicAddress = value
				case "package":
					config.Package = value
				case "topology":
					config.Topology = value
				case "type":
					config.Type = value
				}
				_, diags := (&LoadBalancerDataSource{}).getLoadBalancer(context.Background(), config)
				if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), name) {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
			})
		}
	}
	_, diags := (&LoadBalancerDataSource{}).getLoadBalancer(context.Background(), LoadBalancerDataSourceModel{AccessLogEnabled: types.BoolUnknown()})
	if !diags.HasError() {
		t.Fatal("unknown boolean must return a diagnostic")
	}
}

func TestLoadBalancerDataSourceTypeMapping(t *testing.T) {
	t.Parallel()
	for _, lbType := range []loadbalancersdk.LoadBalancerType{"", loadbalancersdk.LoadBalancerTypeNative} {
		t.Run(fmt.Sprintf("type=%q", lbType), func(t *testing.T) {
			t.Parallel()
			detail := sampleLoadBalancerDetail()
			detail.Type = lbType
			state := LoadBalancerDataSourceModel{Type: types.StringValue("stale")}
			populateLoadBalancerDataSourceState(detail, &state)
			want := types.StringNull()
			if lbType != "" {
				want = types.StringValue(string(lbType))
			}
			if !state.Type.Equal(want) {
				t.Fatalf("expected type %s, got %s", want, state.Type)
			}
			filterType := loadbalancersdk.LoadBalancerTypeNative
			if matches := loadBalancerDetailMatchesFilter(detail, loadbalancerlookup.LoadBalancerFilter{Type: &filterType}); matches != (lbType != "") {
				t.Fatalf("expected type filter match=%t, got %t", lbType != "", matches)
			}
		})
	}
}

func TestLoadBalancerDataSourceAccessLogMapping(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%t/store=%t", enabled, present), func(t *testing.T) {
				t.Parallel()
				detail := sampleLoadBalancerDetail()
				detail.AccessLogEnabled = enabled
				if present {
					detail.AccessLogStore.Set(loadbalancersdk.NestedLogStoreSchema{Id: core.UUID{0xab}, Name: "logs"})
				}
				state := LoadBalancerDataSourceModel{AccessLogStoreID: types.StringValue("stale"), AccessLogStoreName: types.StringValue("stale")}
				populateLoadBalancerDataSourceState(detail, &state)
				if !state.AccessLogEnabled.Equal(types.BoolValue(enabled)) || state.AccessLogStoreID.IsNull() == present || state.AccessLogStoreName.IsNull() == present {
					t.Fatalf("unexpected access log state: %+v", state)
				}
				if present && (state.AccessLogStoreID.ValueString() != core.UUID{0xab}.String() || state.AccessLogStoreName.ValueString() != "logs") {
					t.Fatalf("unexpected store: %+v", state)
				}
			})
		}
	}
}

func TestLoadBalancerDataSourceMapsExplicitNulls(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(sampleLoadBalancerDetail())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"description", "private_ip_id", "vip_address", "public_address", "v6_public_address", "qos_policy", "access_log_store"} {
		fields[name] = nil
	}
	if body, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	var detail loadbalancersdk.LoadBalancerDetailSchema
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	stale := types.StringValue("stale")
	state := LoadBalancerDataSourceModel{
		Description: stale, PrivateIPID: stale, VIPAddress: stale, PublicAddress: stale, V6PublicAddress: stale,
		QoSPolicyID: stale, QoSPolicyName: stale, AccessLogStoreID: stale, AccessLogStoreName: stale,
	}
	populateLoadBalancerDataSourceState(&detail, &state)
	for name, value := range map[string]types.String{
		"description": state.Description, "private_ip_id": state.PrivateIPID, "vip_address": state.VIPAddress,
		"public_address": state.PublicAddress, "v6_public_address": state.V6PublicAddress,
		"qos_policy_id": state.QoSPolicyID, "qos_policy_name": state.QoSPolicyName,
		"access_log_store_id": state.AccessLogStoreID, "access_log_store_name": state.AccessLogStoreName,
	} {
		if !value.IsNull() {
			t.Errorf("%s: expected null for a JSON null, got %s", name, value)
		}
	}
	filter := loadbalancerlookup.LoadBalancerFilter{VIPAddress: new("10.0.0.10")}
	if loadBalancerDetailMatchesFilter(&detail, filter) {
		t.Fatal("a null vip_address must not match a vip_address filter")
	}
}

func TestLoadBalancerDataSourceRejectsConflictingFiltersAndChangedDetail(t *testing.T) {
	t.Parallel()
	for _, changedDetail := range []bool{false, true} {
		t.Run(fmt.Sprintf("changedDetail=%t", changedDetail), func(t *testing.T) {
			t.Parallel()
			getCalls := 0
			client := loadBalancerTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/v2/load-balancing/loadbalancers/" {
					return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerListJSON()), nil
				}
				getCalls++
				detail := sampleLoadBalancerDetail()
				detail.Name = "renamed"
				return loadBalancerTestHTTPResponse(req, http.StatusOK, loadBalancerJSON(t, detail)), nil
			})
			config := LoadBalancerDataSourceModel{ID: types.StringValue(loadBalancerTestID.String()), Name: types.StringValue("different")}
			if changedDetail {
				config.Name = types.StringValue("primary")
			}
			_, diags := (&LoadBalancerDataSource{client: client, projectID: loadBalancerTestProjectID}).getLoadBalancer(context.Background(), config)
			if !diags.HasError() || getCalls != map[bool]int{false: 0, true: 1}[changedDetail] {
				t.Fatalf("get calls=%d diagnostics=%v", getCalls, diags)
			}
		})
	}
}
