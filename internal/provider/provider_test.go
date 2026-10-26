package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func TestProviderFactories(t *testing.T) {
	t.Parallel()

	p := New("test")()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}

	resources := p.Resources(context.Background())
	if len(resources) == 0 {
		t.Error("expected at least one resource, got 0")
	}

	dataSources := p.DataSources(context.Background())
	if len(dataSources) == 0 {
		t.Error("expected at least one data source, got 0")
	}
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	providerUnderTest := &ViettelCloudProvider{version: "test-version"}
	var resp frameworkprovider.MetadataResponse
	providerUnderTest.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "viettelcloud" {
		t.Errorf("provider type name = %q, want %q", resp.TypeName, "viettelcloud")
	}
	if resp.Version != "test-version" {
		t.Errorf("provider version = %q, want %q", resp.Version, "test-version")
	}
}

func TestResolveConfiguredProjectByUUID(t *testing.T) {
	t.Parallel()
	projectID := "00000000-0000-0000-0000-000000000001"
	got, diags := resolveConfiguredProject(context.Background(), ViettelCloudProviderModel{
		ProjectID: types.StringValue(projectID),
	}, func(context.Context, lookup.ProjectFilter) (projectsdk.ProjectSchema, error) {
		t.Fatal("project resolver must not be called for a UUID")
		return projectsdk.ProjectSchema{}, nil
	})
	if diags.HasError() || got.String() != projectID {
		t.Fatalf("resolve project ID: got=%s diagnostics=%v", got, diags)
	}
}

func TestResolveConfiguredProjectBySlug(t *testing.T) {
	t.Parallel()
	wantID := core.UUID{1}
	got, diags := resolveConfiguredProject(context.Background(), ViettelCloudProviderModel{
		ProjectID: types.StringValue(" project-a "),
	}, func(_ context.Context, filter lookup.ProjectFilter) (projectsdk.ProjectSchema, error) {
		if filter.Slug == nil || *filter.Slug != "project-a" {
			t.Fatalf("expected trimmed slug, got %#v", filter)
		}
		return projectsdk.ProjectSchema{Id: wantID, Slug: "project-a"}, nil
	})
	if diags.HasError() || got != wantID {
		t.Fatalf("resolve project slug: got=%s diagnostics=%v", got, diags)
	}
}

func TestProviderConfigureResolvesProjectSlug(t *testing.T) {
	t.Parallel()
	wantID := core.UUID{1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/projects/" || r.URL.Query().Get("slug") != "project-a" {
			t.Errorf("unexpected project lookup request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":%q,"name":"Project A","slug":"project-a"}]}`, wantID.String())
	}))
	t.Cleanup(server.Close)

	providerUnderTest := &ViettelCloudProvider{version: "test"}
	config := testProviderConfig(
		t, providerUnderTest,
		tftypes.NewValue(tftypes.String, server.URL),
		tftypes.NewValue(tftypes.String, "token"),
		tftypes.NewValue(tftypes.String, "project-a"),
	)
	var configureResp frameworkprovider.ConfigureResponse
	providerUnderTest.Configure(context.Background(), frameworkprovider.ConfigureRequest{Config: config}, &configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("configure provider: %v", configureResp.Diagnostics)
	}
	configured, ok := configureResp.ResourceData.(*providerdata.Configured)
	if !ok || configured.ProjectID != wantID || configureResp.DataSourceData != configureResp.ResourceData {
		t.Fatalf("unexpected configured provider data: %#v", configureResp.ResourceData)
	}
}

func TestProviderConfigureRequiresKnownCredentials(t *testing.T) {
	t.Parallel()
	known := tftypes.NewValue(tftypes.String, "value")
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	null := tftypes.NewValue(tftypes.String, nil)
	tests := map[string]struct {
		endpoint tftypes.Value
		token    tftypes.Value
		want     string
	}{
		"unknown endpoint": {endpoint: unknown, token: known, want: "must be known values"},
		"unknown token":    {endpoint: known, token: unknown, want: "must be known values"},
		"null endpoint":    {endpoint: null, token: known, want: "are required"},
		"null token":       {endpoint: known, token: null, want: "are required"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			providerUnderTest := &ViettelCloudProvider{version: "test"}
			config := testProviderConfig(t, providerUnderTest, tt.endpoint, tt.token, known)
			var resp frameworkprovider.ConfigureResponse
			providerUnderTest.Configure(context.Background(), frameworkprovider.ConfigureRequest{Config: config}, &resp)
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), tt.want) {
				t.Fatalf("expected diagnostic containing %q, got %v", tt.want, resp.Diagnostics)
			}
			if resp.ResourceData != nil || resp.DataSourceData != nil {
				t.Fatal("provider data must not be set when configuration is invalid")
			}
		})
	}
}

func TestProviderConfigureReportsProjectLookupFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
	}))
	t.Cleanup(server.Close)

	providerUnderTest := &ViettelCloudProvider{version: "test"}
	config := testProviderConfig(
		t, providerUnderTest,
		tftypes.NewValue(tftypes.String, server.URL),
		tftypes.NewValue(tftypes.String, "token"),
		tftypes.NewValue(tftypes.String, "missing-project"),
	)
	var resp frameworkprovider.ConfigureResponse
	providerUnderTest.Configure(context.Background(), frameworkprovider.ConfigureRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "not found") {
		t.Fatalf("expected project lookup failure, got %v", resp.Diagnostics)
	}
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Fatal("provider data must not be set when project resolution fails")
	}
}

func TestResolveConfiguredProjectValidatesReference(t *testing.T) {
	t.Parallel()
	resolverError := errors.New("lookup failed")
	tests := []struct {
		name   string
		id     types.String
		result error
		want   string
	}{
		{name: "missing", id: types.StringNull(), want: "must be provided"},
		{name: "unknown", id: types.StringUnknown(), want: "known value"},
		{name: "blank", id: types.StringValue("  "), want: "must be provided"},
		{name: "lookup failure", id: types.StringValue("project-a"), result: resolverError, want: "lookup failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := resolveConfiguredProject(context.Background(), ViettelCloudProviderModel{
				ProjectID: tt.id,
			}, func(context.Context, lookup.ProjectFilter) (projectsdk.ProjectSchema, error) {
				return projectsdk.ProjectSchema{}, tt.result
			})
			if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), tt.want) {
				t.Fatalf("expected diagnostic containing %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	server := providerserver.NewProtocol6(New("test")())()
	response, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("get provider schema: %v", err)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}
	if len(response.ResourceSchemas) == 0 {
		t.Error("expected at least one resource schema, got 0")
	}
	for typeName := range response.ResourceSchemas {
		if !strings.HasPrefix(typeName, "viettelcloud_") {
			t.Errorf("resource type name = %q, want viettelcloud_ prefix", typeName)
		}
	}
	if len(response.DataSourceSchemas) == 0 {
		t.Error("expected at least one data source schema, got 0")
	}
	for typeName := range response.DataSourceSchemas {
		if !strings.HasPrefix(typeName, "viettelcloud_") {
			t.Errorf("data source type name = %q, want viettelcloud_ prefix", typeName)
		}
	}
}

// Terraform plans an omitted Optional and Computed nested attribute as null,
// not as its prior value, when that prior value holds a non-Computed nested
// attribute. The framework then marks every Computed attribute unknown, so the
// plan never becomes empty.
func TestProviderSchemaOmittedNestedAttributesKeepPriorValue(t *testing.T) {
	t.Parallel()

	server := providerserver.NewProtocol6(New("test")())()
	response, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("get provider schema: %v", err)
	}
	var walk func(path string, attributes []*tfprotov6.SchemaAttribute, insideOptionalComputed bool)
	walk = func(path string, attributes []*tfprotov6.SchemaAttribute, insideOptionalComputed bool) {
		for _, attribute := range attributes {
			attributePath := path + "." + attribute.Name
			if insideOptionalComputed && !attribute.Computed {
				t.Errorf("%s must be Computed because an enclosing nested attribute is Optional and Computed", attributePath)
			}
			if attribute.NestedType != nil {
				walk(attributePath, attribute.NestedType.Attributes, insideOptionalComputed || attribute.Optional && attribute.Computed)
			}
		}
	}
	for typeName, schema := range response.ResourceSchemas {
		walk(typeName, schema.Block.Attributes, false)
	}
}

func testProviderConfig(t *testing.T, p *ViettelCloudProvider, endpoint, token, projectID tftypes.Value) tfsdk.Config {
	t.Helper()
	var schemaResp frameworkprovider.SchemaResponse
	p.Schema(context.Background(), frameworkprovider.SchemaRequest{}, &schemaResp)
	objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"endpoint": tftypes.String, "token": tftypes.String, "project_id": tftypes.String,
	}}
	return tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(objectType, map[string]tftypes.Value{
			"endpoint": endpoint, "token": token, "project_id": projectID,
		}),
	}
}
