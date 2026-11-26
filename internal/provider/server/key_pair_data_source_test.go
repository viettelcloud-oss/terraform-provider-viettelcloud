package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dataschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

func keyPairDataSourceConfig(t *testing.T, s dataschema.Schema, model KeyPairDataSourceModel) tfsdk.Config {
	t.Helper()
	state := tfsdk.State{Schema: s}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set key pair data source config state: %v", diags)
	}
	return tfsdk.Config{Schema: s, Raw: state.Raw}
}

func TestKeyPairDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	var schemaResp datasource.SchemaResponse
	(&KeyPairDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestKeyPairDataSourceMetadata(t *testing.T) {
	t.Parallel()

	d := NewKeyPairDataSource().(*KeyPairDataSource)
	var resp datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_key_pair" {
		t.Fatalf("got TypeName %q, want viettelcloud_key_pair", resp.TypeName)
	}
}

func TestKeyPairDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := NewKeyPairDataSource().(*KeyPairDataSource)

	// nil ProviderData is a no-op
	var resp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on nil ProviderData: %v", resp.Diagnostics)
	}

	// Invalid ProviderData
	var errResp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 123}, &errResp)
	if !errResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid ProviderData type")
	}

	// Valid ProviderData
	client, err := serversdk.NewClient("https://server.test")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	var okResp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			Server:    client,
			ProjectID: keyPairTestUUID,
		},
	}, &okResp)
	if okResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid ProviderData: %v", okResp.Diagnostics)
	}
}

func TestKeyPairDataSourceRead_ByID_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keyPairSchema := serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "test-key",
		PublicKey:   "ssh-rsa AAA...",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Path != "/v2/server/key-pairs/"+keyPairTestUUID.String()+"/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			return jsonResponse(http.StatusOK, keyPairSchema)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	d := &KeyPairDataSource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue("  " + keyPairTestUUID.String() + " "),
		},
	})

	readResp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	var state KeyPairDataSourceModel
	if diags := readResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if state.ID.ValueString() != "  "+keyPairTestUUID.String()+" " {
		t.Errorf("got ID %q, want the configured spelling", state.ID.ValueString())
	}
	if state.Name.ValueString() != "test-key" {
		t.Errorf("got Name %s, want test-key", state.Name.ValueString())
	}
	if state.Fingerprint.ValueString() != "11:22:33" {
		t.Errorf("got Fingerprint %s, want 11:22:33", state.Fingerprint.ValueString())
	}
}

func TestKeyPairDataSourceRead_ByName_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keyPairSchema := serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "searched-key",
		PublicKey:   "ssh-rsa AAA...",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	pagedResult := serversdk.PagedKeyPairSchema{
		Count:   1,
		Results: []serversdk.KeyPairSchema{keyPairSchema},
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Path != "/v2/server/key-pairs/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			if req.URL.Query().Get("name") != "searched-key" {
				t.Fatalf("unexpected name query param: %q", req.URL.Query().Get("name"))
			}
			return jsonResponse(http.StatusOK, pagedResult)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	d := &KeyPairDataSource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			Name: types.StringValue("  searched-key  "),
		},
	})

	readResp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	var state KeyPairDataSourceModel
	if diags := readResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if state.ID.ValueString() != keyPairTestUUID.String() {
		t.Errorf("got ID %s, want %s", state.ID.ValueString(), keyPairTestUUID.String())
	}
	// Verify configured representation is preserved
	if state.Name.ValueString() != "  searched-key  " {
		t.Errorf("expected whitespace preservation, got %q", state.Name.ValueString())
	}
}

func TestKeyPairDataSourceRead_ByFingerprint_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keyPairSchema := serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "test-key",
		PublicKey:   "ssh-rsa AAA...",
		Fingerprint: "11:22:33:aa:bb",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	pagedResult := serversdk.PagedKeyPairSchema{
		Count:   1,
		Results: []serversdk.KeyPairSchema{keyPairSchema},
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Path != "/v2/server/key-pairs/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			return jsonResponse(http.StatusOK, pagedResult)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	d := &KeyPairDataSource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			Fingerprint: types.StringValue(" 11:22:33:AA:BB "),
		},
	})

	readResp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	var state KeyPairDataSourceModel
	if diags := readResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if state.ID.ValueString() != keyPairTestUUID.String() {
		t.Errorf("got ID %s, want %s", state.ID.ValueString(), keyPairTestUUID.String())
	}
	if state.Fingerprint.ValueString() != " 11:22:33:AA:BB " {
		t.Errorf("expected fingerprint preservation, got %q", state.Fingerprint.ValueString())
	}
}

func TestKeyPairDataSourceRead_ValidationAndErrors(t *testing.T) {
	t.Parallel()

	d := &KeyPairDataSource{}
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	// Missing criteria
	configMissing := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{})
	var respMissing datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: configMissing}, &respMissing)
	if !respMissing.Diagnostics.HasError() {
		t.Fatal("expected error on missing criteria")
	}

	// Whitespace-only name
	configEmptyName := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{Name: types.StringValue("   ")},
	})
	var respEmptyName datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: configEmptyName}, &respEmptyName)
	if !respEmptyName.Diagnostics.HasError() {
		t.Fatal("expected error on whitespace-only name")
	}

	// Whitespace-only fingerprint
	configEmptyFp := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{Fingerprint: types.StringValue("   ")},
	})
	var respEmptyFp datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: configEmptyFp}, &respEmptyFp)
	if !respEmptyFp.Diagnostics.HasError() {
		t.Fatal("expected error on whitespace-only fingerprint")
	}

	// Invalid UUID
	configInvalidUUID := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{ID: types.StringValue("not-a-valid-uuid")},
	})
	var respInvalidUUID datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: configInvalidUUID}, &respInvalidUUID)
	if !respInvalidUUID.Diagnostics.HasError() || !strings.Contains(respInvalidUUID.Diagnostics.Errors()[0].Detail(), "valid UUID") {
		t.Fatalf("expected invalid UUID diagnostic, got %v", respInvalidUUID.Diagnostics)
	}
}

func TestKeyPairDataSourceRead_ByID_NotFound(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"detail":"Not found"}`))),
			}, nil
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	d := &KeyPairDataSource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue(keyPairTestUUID.String()),
		},
	})

	var readResp datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &readResp)
	if !readResp.Diagnostics.HasError() || !strings.Contains(readResp.Diagnostics.Errors()[0].Detail(), "No key pair found with id") {
		t.Fatalf("expected not found diagnostic, got %v", readResp.Diagnostics)
	}
}

func TestKeyPairDataSourceRead_ByFilter_NotFound(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, serversdk.PagedKeyPairSchema{
				Count:   0,
				Results: []serversdk.KeyPairSchema{},
			})
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	d := &KeyPairDataSource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := keyPairDataSourceConfig(t, schemaResp.Schema, KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			Name: types.StringValue("non-existent-key"),
		},
	})

	var readResp datasource.ReadResponse
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &readResp)
	if !readResp.Diagnostics.HasError() || !strings.Contains(readResp.Diagnostics.Errors()[0].Detail(), "was not found") {
		t.Fatalf("expected not found diagnostic, got %v", readResp.Diagnostics)
	}
}

func TestPopulateKeyPairDataSourceState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	api := &serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "test-key",
		PublicKey:   "ssh-rsa AAA...",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	var state KeyPairDataSourceModel
	populateKeyPairDataSourceState(api, &state)

	if state.ID.ValueString() != keyPairTestUUID.String() {
		t.Errorf("got ID %s, want %s", state.ID.ValueString(), keyPairTestUUID.String())
	}
	if state.Name.ValueString() != "test-key" {
		t.Errorf("got Name %s, want test-key", state.Name.ValueString())
	}
}

func TestPreserveConfiguredValues(t *testing.T) {
	t.Parallel()

	testUUID := core.UUID{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89}
	api := &serversdk.KeyPairSchema{
		Id:          testUUID,
		Name:        "test-key",
		Fingerprint: "11:22:aa",
	}

	config := KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue("  " + strings.ToUpper(testUUID.String()) + " "),
			Name:        types.StringValue("  test-key  "),
			Fingerprint: types.StringValue(" 11:22:AA "),
		},
	}

	state := KeyPairDataSourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue(testUUID.String()),
			Name:        types.StringValue("test-key"),
			Fingerprint: types.StringValue("11:22:aa"),
		},
	}

	preserveConfiguredValues(api, config, &state)

	if state.ID.ValueString() != config.ID.ValueString() {
		t.Errorf("got ID %q, want %q", state.ID.ValueString(), config.ID.ValueString())
	}
	if state.Name.ValueString() != config.Name.ValueString() {
		t.Errorf("got Name %q, want %q", state.Name.ValueString(), config.Name.ValueString())
	}
	if state.Fingerprint.ValueString() != config.Fingerprint.ValueString() {
		t.Errorf("got Fingerprint %q, want %q", state.Fingerprint.ValueString(), config.Fingerprint.ValueString())
	}
}
