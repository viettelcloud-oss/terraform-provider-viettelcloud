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

func securityGroupDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&SecurityGroupDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp.Schema
}

func TestSecurityGroupDataSourceMetadataAndConstructor(t *testing.T) {
	t.Parallel()

	ds := NewSecurityGroupDataSource()
	if ds == nil {
		t.Fatal("expected non-nil SecurityGroupDataSource")
	}

	var resp datasource.MetadataResponse
	ds.Metadata(context.Background(), datasource.MetadataRequest{
		ProviderTypeName: "viettelcloud",
	}, &resp)

	if resp.TypeName != "viettelcloud_security_group" {
		t.Fatalf("expected type name viettelcloud_security_group, got %q", resp.TypeName)
	}
}

func TestSecurityGroupDataSourceConfigure(t *testing.T) {
	t.Parallel()

	ds := &SecurityGroupDataSource{}
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

func TestSecurityGroupDataSourceReadDirectID(t *testing.T) {
	t.Parallel()

	sgID := core.UUID{1, 2, 3}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/"+sgID.String()+"/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": "` + sgID.String() + `",
				"name": "direct-sg",
				"display_name": "direct-sg-display",
				"description": "direct description",
				"is_default": false,
				"region": {
					"id": "00000000-0000-0000-0000-000000000001",
					"name": "vn-central"
				},
				"project": {
					"id": "00000000-0000-0000-0000-000000000009"
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: core.UUID{9},
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID: types.StringValue(sgID.String()),
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

	var state SecurityGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != sgID.String() || state.Name.ValueString() != "direct-sg" || state.Region.ValueString() != "vn-central" || state.Description.ValueString() != "direct description" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSecurityGroupDataSourceReadDirectIDNotFound(t *testing.T) {
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: core.UUID{9},
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
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

func TestSecurityGroupDataSourceReadRejectsInvalidID(t *testing.T) {
	t.Parallel()

	ds := &SecurityGroupDataSource{}
	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
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

func TestSecurityGroupDataSourceReadRequiresFilter(t *testing.T) {
	t.Parallel()

	ds := &SecurityGroupDataSource{}
	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID:          types.StringNull(),
			Name:        types.StringNull(),
			Description: types.StringNull(),
			IsDefault:   types.BoolNull(),
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

func TestSecurityGroupDataSourceReadRejectsEmptyFilters(t *testing.T) {
	t.Parallel()

	tests := map[string]SecurityGroupDataSourceModel{
		"name":   {SecurityGroupModel: SecurityGroupModel{Name: types.StringValue(" ")}},
		"region": {Region: types.StringValue(" ")},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			ds := &SecurityGroupDataSource{}
			schema := securityGroupDataSourceSchema(t)
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

func TestSecurityGroupDataSourceReadFilterByName(t *testing.T) {
	t.Parallel()

	sgID := core.UUID{5}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"id": "` + sgID.String() + `",
						"name": "filtered-sg",
						"display_name": "filtered-sg",
						"description": "filtered description",
						"is_default": false,
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"project": {
							"id": "00000000-0000-0000-0000-000000000009"
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: core.UUID{9},
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name: types.StringValue("filtered-sg"),
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

	var state SecurityGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != sgID.String() || state.Region.ValueString() != "vn-central" || state.Description.ValueString() != "filtered description" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSecurityGroupDataSourceReadFilterByRegionAndIsDefault(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	sgID := core.UUID{8}
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
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"id": "` + sgID.String() + `",
						"name": "default-sg",
						"display_name": "default-sg",
						"description": "default sg description",
						"is_default": true,
						"region": {
							"id": "` + regionID.String() + `",
							"name": "vn-central"
						},
						"project": {
							"id": "` + projectID.String() + `"
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

	ds := &SecurityGroupDataSource{
		client:    client,
		project:   projectClient,
		projectID: projectID,
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			IsDefault: types.BoolValue(true),
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

	var state SecurityGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != sgID.String() || state.IsDefault.ValueBool() != true || state.Region.ValueString() != "vn-central" {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSecurityGroupDataSourceSchemaAttributeFlags(t *testing.T) {
	t.Parallel()

	schema := securityGroupDataSourceSchema(t)

	for _, name := range []string{"id", "name", "region", "is_default"} {
		switch attr := schema.Attributes[name].(type) {
		case datasourceschema.StringAttribute:
			if !attr.Optional || !attr.Computed {
				t.Errorf("%s must be Optional=true, Computed=true (filter), got Optional=%v Computed=%v", name, attr.Optional, attr.Computed)
			}
		case datasourceschema.BoolAttribute:
			if !attr.Optional || !attr.Computed {
				t.Errorf("%s must be Optional=true, Computed=true (filter), got Optional=%v Computed=%v", name, attr.Optional, attr.Computed)
			}
		default:
			t.Errorf("%s has unexpected attribute type %T", name, schema.Attributes[name])
		}
	}

	for _, name := range []string{"display_name", "description", "region_id", "project_id", "created_at", "updated_at"} {
		attr, ok := schema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || attr.Optional || !attr.Computed {
			t.Errorf("%s must be result-only (Optional=false, Computed=true), got %#v", name, schema.Attributes[name])
		}
	}
}

func TestSecurityGroupDataSourceReadFilterByIsDefaultAlone(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	sgID := core.UUID{8}
	regionID := core.UUID{7}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 1,
				"results": [
					{
						"id": "` + sgID.String() + `",
						"name": "default-sg",
						"display_name": "default-sg",
						"description": "default sg description",
						"is_default": true,
						"region": {
							"id": "` + regionID.String() + `",
							"name": "vn-central"
						},
						"project": {
							"id": "` + projectID.String() + `"
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: projectID,
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			IsDefault: types.BoolValue(true),
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

	var state SecurityGroupDataSourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to get state: %v", diags)
	}
	if state.ID.ValueString() != sgID.String() || state.IsDefault.ValueBool() != true {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestSecurityGroupDataSourceReadMultipleMatchesError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"count": 2,
				"results": [
					{
						"id": "00000000-0000-0000-0000-000000000001",
						"name": "dup-sg",
						"display_name": "dup-sg",
						"is_default": false,
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"project": {
							"id": "00000000-0000-0000-0000-000000000009"
						},
						"created_at": "2026-01-01T00:00:00Z",
						"updated_at": "2026-01-01T00:00:00Z"
					},
					{
						"id": "00000000-0000-0000-0000-000000000002",
						"name": "dup-sg",
						"display_name": "dup-sg",
						"is_default": false,
						"region": {
							"id": "00000000-0000-0000-0000-000000000001",
							"name": "vn-central"
						},
						"project": {
							"id": "00000000-0000-0000-0000-000000000009"
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: core.UUID{9},
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name: types.StringValue("dup-sg"),
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

func TestSecurityGroupDataSourceReadZeroMatchesError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/network/security-groups/" {
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

	ds := &SecurityGroupDataSource{
		client:    client,
		projectID: core.UUID{9},
	}

	schema := securityGroupDataSourceSchema(t)
	configState := tfsdk.State{Schema: schema}
	diags := configState.Set(context.Background(), &SecurityGroupDataSourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name: types.StringValue("non-existent-sg"),
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
		t.Fatal("expected not found error when 0 Security Groups match")
	}
}
