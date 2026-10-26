package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func vpcDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&VPCDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp.Schema
}

func TestVPCDataSourceMetadataAndConstructor(t *testing.T) {
	t.Parallel()

	ds := NewVPCDataSource()
	if ds == nil {
		t.Fatal("expected non-nil VPCDataSource")
	}

	var resp datasource.MetadataResponse
	ds.Metadata(context.Background(), datasource.MetadataRequest{
		ProviderTypeName: "viettelcloud",
	}, &resp)

	if resp.TypeName != "viettelcloud_vpc" {
		t.Fatalf("expected type name viettelcloud_vpc, got %q", resp.TypeName)
	}
}

func TestVPCDataSourceConfigure(t *testing.T) {
	t.Parallel()

	ds := &VPCDataSource{}
	var resp datasource.ConfigureResponse

	// Nil provider data
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil provider data: %v", resp.Diagnostics)
	}

	// Invalid provider data type
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data type")
	}

	// Valid provider data
	resp.Diagnostics = nil
	client, _ := networksdk.NewClient("https://example.com", core.WithHTTPClient(&http.Client{}))
	projectClient, _ := projectsdk.NewClient("https://example.com", core.WithHTTPClient(&http.Client{}))
	ds.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			Network:   client,
			Project:   projectClient,
			ProjectID: core.UUID{1},
		},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on valid provider data: %v", resp.Diagnostics)
	}
	if ds.client != client || ds.project != projectClient || ds.projectID != (core.UUID{1}) {
		t.Fatal("expected client, project, and projectID to be configured")
	}
}

func TestVPCDataSourceReadDirectID(t *testing.T) {
	t.Parallel()

	vpcID := core.UUID{1, 2, 3}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/vpcs/"+vpcID.String()+"/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": "` + vpcID.String() + `",
				"name": "direct-vpc",
				"cidr": "10.0.0.0/16",
				"display_name": "direct-vpc",
				"region": {
					"id": "00000000-0000-0000-0000-000000000001",
					"name": "vn-central"
				},
				"created_at": "2026-01-01T00:00:00Z",
				"updated_at": "2026-01-01T00:00:00Z"
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		projectID: core.UUID{1},
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			ID: types.StringValue(vpcID.String()),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	req := datasource.ReadRequest{
		Config: config,
	}
	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}

	ds.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", resp.Diagnostics)
	}

	var state VPCDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != vpcID.String() || state.Name.ValueString() != "direct-vpc" || state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestVPCDataSourceReadDirectIDNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail": "Not found"}`))
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		projectID: core.UUID{1},
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			ID: types.StringValue(core.UUID{9}.String()),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected not found diagnostic error")
	}
}

func TestVPCDataSourceReadRejectsInvalidID(t *testing.T) {
	t.Parallel()

	ds := &VPCDataSource{}
	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			ID: types.StringValue("invalid-uuid"),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid UUID")
	}
}

func TestVPCDataSourceReadRequiresFilter(t *testing.T) {
	t.Parallel()

	ds := &VPCDataSource{}
	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			ID:   types.StringNull(),
			Name: types.StringNull(),
			CIDR: types.StringNull(),
		},
		Region: types.StringNull(),
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}

	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
	ds.Read(context.Background(), datasource.ReadRequest{
		Config: tfsdk.Config{Raw: configState.Raw, Schema: schema},
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected missing-filter diagnostic error")
	}
}

func TestVPCDataSourceReadRejectsEmptyFilters(t *testing.T) {
	t.Parallel()

	tests := map[string]VPCDataSourceModel{
		"name":   {VPCModel: VPCModel{Name: types.StringValue(" ")}},
		"cidr":   {VPCModel: VPCModel{CIDR: types.StringValue(" ")}},
		"region": {Region: types.StringValue(" ")},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			ds := &VPCDataSource{}
			schema := vpcDataSourceSchema(t)
			configState := tfsdk.State{Schema: schema}
			diags := configState.Set(context.Background(), &data)
			if diags.HasError() {
				t.Fatalf("failed to set config: %v", diags)
			}

			resp := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
			ds.Read(context.Background(), datasource.ReadRequest{
				Config: tfsdk.Config{Raw: configState.Raw, Schema: schema},
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected empty-filter diagnostic error")
			}
		})
	}
}

func TestVPCDataSourceReadFilterByName(t *testing.T) {
	t.Parallel()

	vpcID := core.UUID{5}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/vpcs/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"id": "` + vpcID.String() + `",
						"name": "filtered-vpc",
						"cidr": "172.16.0.0/16",
						"display_name": "filtered-vpc",
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z"
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		projectID: core.UUID{1},
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("filtered-vpc"),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", resp.Diagnostics)
	}

	var state VPCDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != vpcID.String() || state.CIDR.ValueString() != "172.16.0.0/16" || state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestVPCDataSourceReadFilterByCIDRAndRegion(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	vpcID := core.UUID{8}
	regionID := core.UUID{7}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/projects/"+projectID.String()+"/regions/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z",
						"region": {
							"id": "` + regionID.String() + `",
							"name": "vn-central",
							"created_at": "2026-01-01T00:00:00Z",
							"updated_at": "2026-01-01T00:00:00Z"
						}
					}
				]
			}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/vpcs/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"id": "` + vpcID.String() + `",
						"name": "cidr-vpc",
						"cidr": "10.50.0.0/16",
						"display_name": "cidr-vpc",
						"region": {
							"id": "` + regionID.String() + `",
							"name": "vn-central"
						},
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z"
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create network client: %v", err)
	}
	projectClient, err := projectsdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create project client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		project:   projectClient,
		projectID: projectID,
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			CIDR: types.StringValue("10.50.0.0/16"),
		},
		Region: types.StringValue("vn-central"),
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", resp.Diagnostics)
	}

	var state VPCDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != vpcID.String() || state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestVPCDataSourceReadMultipleMatchesError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/vpcs/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 2,
				"results": [
					{
						"id": "00000000-0000-0000-0000-000000000001",
						"name": "dup-vpc",
						"cidr": "10.0.0.0/16",
						"display_name": "dup-vpc",
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z"
					},
					{
						"id": "00000000-0000-0000-0000-000000000002",
						"name": "dup-vpc",
						"cidr": "10.1.0.0/16",
						"display_name": "dup-vpc",
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z"
					}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		projectID: core.UUID{1},
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("dup-vpc"),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected ambiguous multiple matches error")
	}
}

func TestVPCDataSourceReadZeroMatchesError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/vpcs/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 0,
				"results": []
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, core.WithHTTPClient(server.Client()), core.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	ds := &VPCDataSource{
		client:    client,
		projectID: core.UUID{1},
	}

	schema := vpcDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &VPCDataSourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("non-existent-vpc"),
		},
	})
	if diags.HasError() {
		t.Fatalf("failed to set config: %v", diags)
	}
	config := tfsdk.Config{Raw: configState.Raw, Schema: schema}

	resp := datasource.ReadResponse{
		State: tfsdk.State{Schema: schema},
	}
	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected not found error when 0 VPC matches")
	}
}

func TestPreserveConfiguredValuesPreservesCIDRWhenBackendTrimsWhitespace(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	config := VPCDataSourceModel{VPCModel: VPCModel{CIDR: types.StringValue("  10.0.0.0/16  ")}}
	var state VPCDataSourceModel
	populateVPCDataSourceState(vpc, &state)
	preserveConfiguredValues(vpc, config, &state)

	if state.CIDR.ValueString() != "  10.0.0.0/16  " {
		t.Fatalf("expected configured CIDR representation to be preserved, got %q", state.CIDR.ValueString())
	}
}

func TestPreserveConfiguredValuesUsesChangedBackendCIDR(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	config := VPCDataSourceModel{VPCModel: VPCModel{CIDR: types.StringValue("10.1.0.0/16")}}
	var state VPCDataSourceModel
	populateVPCDataSourceState(vpc, &state)
	preserveConfiguredValues(vpc, config, &state)

	if state.CIDR.ValueString() != vpc.Cidr {
		t.Fatalf("expected backend CIDR %q, got %q", vpc.Cidr, state.CIDR.ValueString())
	}
}
