package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestSubnetDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	dsSchema := subnetDataSourceSchema(t)
	state := tfsdk.State{Schema: dsSchema}
	if diags := state.Set(context.Background(), &SubnetDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestSubnetDataSourceConstructorAndMetadata(t *testing.T) {
	t.Parallel()

	d := NewSubnetDataSource()
	if d == nil {
		t.Fatal("expected non-nil data source")
	}
	var resp datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_subnet" {
		t.Fatalf("expected TypeName 'viettelcloud_subnet', got %q", resp.TypeName)
	}
}

func TestSubnetDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := &SubnetDataSource{}

	var nilResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil ProviderData: %v", nilResponse.Diagnostics)
	}

	var invalidResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 123}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid provider data type")
	}

	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	configured := &providerdata.Configured{Network: client, ProjectID: subnetProjectID}
	var validResponse datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: configured}, &validResponse)
	if validResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on valid ProviderData: %v", validResponse.Diagnostics)
	}
	if d.client != client || d.projectID != subnetProjectID {
		t.Fatalf("expected client and projectID to be configured, got client=%v, projectID=%v", d.client, d.projectID)
	}
}

func TestSubnetDataSourceFilterSchema(t *testing.T) {
	t.Parallel()

	dsSchema := subnetDataSourceSchema(t)
	for _, name := range []string{"id", "name", "cidr", "vpc_id", "vpc_name", "region"} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if !attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("expected %s to be optional and computed, got %#v", name, attribute)
		}
	}
	for _, name := range []string{"description", "display_name", "created_at", "updated_at"} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("expected %s to be computed only, got %#v", name, attribute)
		}
	}
}

func TestSubnetDataSourceReadRequiresFilter(t *testing.T) {
	t.Parallel()

	dsSchema := subnetDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	if diags := configState.Set(context.Background(), &SubnetDataSourceModel{}); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}

	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	(&SubnetDataSource{}).Read(context.Background(), datasource.ReadRequest{
		Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema},
	}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Missing subnet lookup criteria" {
		t.Fatalf("expected missing lookup criteria diagnostic, got %v", response.Diagnostics)
	}
}

func TestSubnetDataSourceReadRejectsEmptyTextFilters(t *testing.T) {
	t.Parallel()

	tests := map[string]SubnetDataSourceModel{
		"name":     {SubnetModel: SubnetModel{Name: types.StringValue(" ")}},
		"cidr":     {SubnetModel: SubnetModel{CIDR: types.StringValue(" ")}},
		"vpc_name": {SubnetModel: SubnetModel{VPCName: types.StringValue(" ")}},
		"region":   {SubnetModel: SubnetModel{Region: types.StringValue(" ")}},
	}
	for name, configModel := range tests {
		t.Run(name, func(t *testing.T) {
			dsSchema := subnetDataSourceSchema(t)
			configState := tfsdk.State{Schema: dsSchema}
			if diags := configState.Set(context.Background(), &configModel); diags.HasError() {
				t.Fatalf("set data source config: %v", diags)
			}

			response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
			(&SubnetDataSource{}).Read(context.Background(), datasource.ReadRequest{
				Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema},
			}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected empty-filter diagnostic")
			}
		})
	}
}

func TestSubnetDataSourceReadRejectsInvalidVPCID(t *testing.T) {
	t.Parallel()

	dsSchema := subnetDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	if diags := configState.Set(context.Background(), &SubnetDataSourceModel{
		SubnetModel: SubnetModel{Name: types.StringValue("application")},
		VPCID:       types.StringValue("invalid-uuid"),
	}); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}

	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	(&SubnetDataSource{}).Read(context.Background(), datasource.ReadRequest{
		Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema},
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected invalid vpc_id diagnostic")
	}
}

func TestSubnetDataSourceReadPreservesConfiguredIDRepresentation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/network/subnets/"+subnetTestUUID.String()+"/" {
			t.Errorf("unexpected data source request: %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writeSubnetResponse(t, w, mockSubnet("application", "application tier"))
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}

	ctx := context.Background()
	dsSchema := subnetDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	configuredID := strings.ToUpper(subnetTestUUID.String())
	if diags := configState.Set(ctx, &SubnetDataSourceModel{SubnetModel: SubnetModel{
		ID: types.StringValue(configuredID),
	}}); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}

	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	(&SubnetDataSource{client: client, projectID: subnetProjectID}).Read(
		ctx,
		datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}},
		&response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source diagnostics: %v", response.Diagnostics)
	}

	var state SubnetDataSourceModel
	if diags := response.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("get data source state: %v", diags)
	}
	if state.ID.ValueString() != configuredID {
		t.Fatalf("expected configured subnet ID %q, got %q", configuredID, state.ID.ValueString())
	}
}

func TestSubnetDataSourceReadRejectsInvalidID(t *testing.T) {
	t.Parallel()

	tests := map[string]SubnetDataSourceModel{
		"direct ID lookup": {SubnetModel: SubnetModel{
			ID: types.StringValue("invalid-uuid"),
		}},
		"combined filter lookup": {SubnetModel: SubnetModel{
			ID:   types.StringValue("invalid-uuid"),
			Name: types.StringValue("application"),
		}},
	}
	for name, configModel := range tests {
		t.Run(name, func(t *testing.T) {
			dsSchema := subnetDataSourceSchema(t)
			configState := tfsdk.State{Schema: dsSchema}
			if diags := configState.Set(context.Background(), &configModel); diags.HasError() {
				t.Fatalf("set data source config: %v", diags)
			}

			response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
			(&SubnetDataSource{projectID: subnetProjectID}).Read(
				context.Background(),
				datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}},
				&response,
			)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected diagnostic error for invalid UUID on DataSource Read")
			}
		})
	}
}

func TestSubnetDataSourceReadDirectIDErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status          int
		expectedSummary string
	}{
		"not found": {status: http.StatusNotFound, expectedSummary: "Subnet not found"},
		"API error": {status: http.StatusInternalServerError, expectedSummary: "Error reading subnet"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"detail":"request failed"}`, test.status)
			}))
			defer server.Close()

			client, err := networksdk.NewClient(
				server.URL,
				networksdk.WithHTTPClient(server.Client()),
				networksdk.WithRetry(core.NoRetry()),
			)
			if err != nil {
				t.Fatalf("create network client: %v", err)
			}

			dsSchema := subnetDataSourceSchema(t)
			configState := tfsdk.State{Schema: dsSchema}
			if diags := configState.Set(context.Background(), &SubnetDataSourceModel{SubnetModel: SubnetModel{
				ID: types.StringValue(subnetTestUUID.String()),
			}}); diags.HasError() {
				t.Fatalf("set data source config: %v", diags)
			}

			response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
			(&SubnetDataSource{client: client, projectID: subnetProjectID}).Read(
				context.Background(),
				datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}},
				&response,
			)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected direct-ID diagnostic error")
			}
			if got := response.Diagnostics.Errors()[0].Summary(); got != test.expectedSummary {
				t.Fatalf("expected summary %q, got %q", test.expectedSummary, got)
			}
		})
	}
}

func TestSubnetDataSourceReadByCombinedFilters(t *testing.T) {
	t.Parallel()

	wantSubnet := mockSubnet("application", "application tier")
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/network/subnets/" {
			t.Errorf("unexpected data source request: %s %s", req.Method, req.URL.Path)
		}
		query = req.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(networksdk.PagedSubnetSchema{
			Count: 1, Results: []networksdk.SubnetSchema{*wantSubnet},
		}); err != nil {
			t.Errorf("encode subnet page: %v", err)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}

	dsSchema := subnetDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	configuredName := "  application  "
	configuredCIDR := " 10.0.1.0/24 "
	configuredVPCID := strings.ToUpper(subnetTestVPCID.String())
	configuredVPCName := " production "
	configuredRegion := " vn-central-1 "
	if diags := configState.Set(context.Background(), &SubnetDataSourceModel{
		SubnetModel: SubnetModel{
			Name:    types.StringValue(configuredName),
			CIDR:    types.StringValue(configuredCIDR),
			VPCName: types.StringValue(configuredVPCName),
			Region:  types.StringValue(configuredRegion),
		},
		VPCID: types.StringValue(configuredVPCID),
	}); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}

	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	(&SubnetDataSource{client: client, projectID: subnetProjectID}).Read(
		context.Background(),
		datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}},
		&response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source diagnostics: %v", response.Diagnostics)
	}

	for key, want := range map[string]string{
		"name": "application", "cidr": "10.0.1.0/24", "vpc_id": subnetTestVPCID.String(),
		"vpc_name": "production", "region": "vn-central-1",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}

	var state SubnetDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get data source state: %v", diags)
	}
	if state.ID.ValueString() != wantSubnet.Id.String() ||
		state.Name.ValueString() != configuredName ||
		state.CIDR.ValueString() != configuredCIDR ||
		state.VPCID.ValueString() != configuredVPCID ||
		state.VPCName.ValueString() != configuredVPCName ||
		state.Region.ValueString() != configuredRegion {
		t.Fatalf("unexpected data source state: %#v", state)
	}
}

func TestSubnetDataSourceReadFilterCardinalityErrors(t *testing.T) {
	t.Parallel()

	tests := map[string][]networksdk.SubnetSchema{
		"zero": nil,
		"multiple": {
			*mockSubnet("duplicate", "first"),
			func() networksdk.SubnetSchema {
				subnet := *mockSubnet("duplicate", "second")
				subnet.Id = core.UUID{2}
				subnet.Cidr = "10.0.2.0/24"
				return subnet
			}(),
		},
	}
	for name, results := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(networksdk.PagedSubnetSchema{Count: len(results), Results: results}); err != nil {
					t.Errorf("encode subnet page: %v", err)
				}
			}))
			defer server.Close()

			client, err := networksdk.NewClient(
				server.URL,
				networksdk.WithHTTPClient(server.Client()),
				networksdk.WithRetry(core.NoRetry()),
			)
			if err != nil {
				t.Fatalf("create network client: %v", err)
			}

			dsSchema := subnetDataSourceSchema(t)
			configState := tfsdk.State{Schema: dsSchema}
			if diags := configState.Set(context.Background(), &SubnetDataSourceModel{SubnetModel: SubnetModel{
				Name: types.StringValue("duplicate"),
			}}); diags.HasError() {
				t.Fatalf("set data source config: %v", diags)
			}

			response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
			(&SubnetDataSource{client: client, projectID: subnetProjectID}).Read(
				context.Background(),
				datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema}},
				&response,
			)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Error resolving subnet" {
				t.Fatalf("expected subnet cardinality diagnostic, got %v", response.Diagnostics)
			}
		})
	}
}

func TestPreserveConfiguredSubnetValuesUsesChangedBackendValues(t *testing.T) {
	t.Parallel()

	unchanged := SubnetDataSourceModel{SubnetModel: SubnetModel{Name: types.StringValue("unchanged")}}
	preserveConfiguredSubnetValues(nil, SubnetDataSourceModel{}, &unchanged)
	if unchanged.Name.ValueString() != "unchanged" {
		t.Fatalf("expected nil subnet to leave state unchanged, got %#v", unchanged)
	}

	subnet := mockSubnet("backend-name", "description")
	config := SubnetDataSourceModel{
		SubnetModel: SubnetModel{
			Name:    types.StringValue("configured-name"),
			CIDR:    types.StringValue("10.9.0.0/24"),
			VPCName: types.StringValue("configured-vpc"),
			Region:  types.StringValue("configured-region"),
		},
		VPCID: types.StringValue(core.UUID{7}.String()),
	}
	var state SubnetDataSourceModel
	populateSubnetDataSourceState(subnet, &state)
	preserveConfiguredSubnetValues(subnet, config, &state)

	if state.Name.ValueString() != subnet.Name || state.CIDR.ValueString() != subnet.Cidr ||
		state.VPCID.ValueString() != subnet.Vpc.Id.String() || state.VPCName.ValueString() != subnet.Vpc.Name ||
		state.Region.ValueString() != subnet.Region.Name {
		t.Fatalf("expected backend values, got %#v", state)
	}
}

func TestPopulateSubnetDataSourceState(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "tier")
	var state SubnetDataSourceModel
	populateSubnetDataSourceState(subnet, &state)

	if state.ID.ValueString() != subnet.Id.String() ||
		state.Name.ValueString() != subnet.Name ||
		state.Description.ValueString() != "tier" ||
		state.CIDR.ValueString() != subnet.Cidr ||
		state.VPCID.ValueString() != subnet.Vpc.Id.String() ||
		state.VPCName.ValueString() != subnet.Vpc.Name ||
		state.Region.ValueString() != subnet.Region.Name ||
		state.DisplayName.ValueString() != subnet.DisplayName {
		t.Fatalf("unexpected data source state: %#v", state)
	}

	subnet.Description = nil
	populateSubnetDataSourceState(subnet, &state)
	if !state.Description.IsNull() {
		t.Fatalf("expected null description, got %q", state.Description.ValueString())
	}

	var nilState SubnetDataSourceModel
	populateSubnetDataSourceState(nil, &nilState)
	if !nilState.ID.IsNull() {
		t.Fatalf("expected null ID on nil subnet, got %v", nilState.ID)
	}
}

func subnetDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&SubnetDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}
