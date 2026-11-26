package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

var keyPairTestUUID = core.UUID{1}

type keyPairRoundTripFunc func(*http.Request) (*http.Response, error)

func (f keyPairRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func TestKeyPairResourceMetadata(t *testing.T) {
	t.Parallel()

	r := NewKeyPairResource().(*KeyPairResource)
	var resp resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_key_pair" {
		t.Fatalf("got TypeName %q, want viettelcloud_key_pair", resp.TypeName)
	}
}

func TestKeyPairModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	var resourceSchemaResponse resource.SchemaResponse
	(&KeyPairResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceSchemaResponse)
	resourcePlan := tfsdk.Plan{Schema: resourceSchemaResponse.Schema}
	if diags := resourcePlan.Set(context.Background(), &KeyPairResourceModel{}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestKeyPairSchemaMutability(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&KeyPairResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)

	nameAttr, ok := response.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok || !nameAttr.Required || nameAttr.Computed {
		t.Fatalf("name must be required and not computed, got %#v", response.Schema.Attributes["name"])
	}

	pubKeyAttr, ok := response.Schema.Attributes["public_key"].(schema.StringAttribute)
	if !ok || !pubKeyAttr.Optional || !pubKeyAttr.Computed || len(pubKeyAttr.PlanModifiers) != 2 {
		t.Fatalf("public_key must be optional, computed with 2 plan modifiers, got %#v", response.Schema.Attributes["public_key"])
	}

	privKeyAttr, ok := response.Schema.Attributes["private_key"].(schema.StringAttribute)
	if !ok || !privKeyAttr.Computed || !privKeyAttr.Sensitive || len(privKeyAttr.PlanModifiers) != 1 {
		t.Fatalf("private_key must be computed, sensitive with 1 plan modifier, got %#v", response.Schema.Attributes["private_key"])
	}

	idAttr, ok := response.Schema.Attributes["id"].(schema.StringAttribute)
	if !ok || !idAttr.Computed || len(idAttr.PlanModifiers) != 1 {
		t.Fatalf("id must be computed with 1 plan modifier, got %#v", response.Schema.Attributes["id"])
	}

	fpAttr, ok := response.Schema.Attributes["fingerprint"].(schema.StringAttribute)
	if !ok || !fpAttr.Computed || len(fpAttr.PlanModifiers) != 1 {
		t.Fatalf("fingerprint must be computed with 1 plan modifier, got %#v", response.Schema.Attributes["fingerprint"])
	}

	typeAttr, ok := response.Schema.Attributes["type"].(schema.StringAttribute)
	if !ok || !typeAttr.Computed || len(typeAttr.PlanModifiers) != 1 {
		t.Fatalf("type must be computed with 1 plan modifier, got %#v", response.Schema.Attributes["type"])
	}

	createdAtAttr, ok := response.Schema.Attributes["created_at"].(schema.StringAttribute)
	if !ok || !createdAtAttr.Computed || len(createdAtAttr.PlanModifiers) != 1 {
		t.Fatalf("created_at must be computed with 1 plan modifier, got %#v", response.Schema.Attributes["created_at"])
	}

	updatedAtAttr, ok := response.Schema.Attributes["updated_at"].(schema.StringAttribute)
	if !ok || !updatedAtAttr.Computed || len(updatedAtAttr.PlanModifiers) != 0 {
		t.Fatalf("updated_at must be computed with 0 plan modifiers, got %#v", response.Schema.Attributes["updated_at"])
	}
}

func TestBuildKeyPairCreateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		model       KeyPairResourceModel
		expectError bool
		wantName    string
		wantPubKey  *string
	}{
		{
			name: "valid with generated key (no public_key)",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name:      types.StringValue("my-key"),
					PublicKey: types.StringNull(),
				},
			},
			wantName:   "my-key",
			wantPubKey: nil,
		},
		{
			name: "valid with imported public key",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name:      types.StringValue("my-key"),
					PublicKey: types.StringValue("ssh-rsa AAAAB3NzaC1yc2E... user@host"),
				},
			},
			wantName:   "my-key",
			wantPubKey: new("ssh-rsa AAAAB3NzaC1yc2E... user@host"),
		},
		{
			name: "trims whitespace on name and public_key",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name:      types.StringValue("  trimmed-key  "),
					PublicKey: types.StringValue("  ssh-rsa AAA...  "),
				},
			},
			wantName:   "trimmed-key",
			wantPubKey: new("ssh-rsa AAA..."),
		},
		{
			name: "rejects empty name",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue(""),
				},
			},
			expectError: true,
		},
		{
			name: "rejects whitespace-only name",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue("   "),
				},
			},
			expectError: true,
		},
		{
			name: "rejects empty public_key when configured",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name:      types.StringValue("my-key"),
					PublicKey: types.StringValue(""),
				},
			},
			expectError: true,
		},
		{
			name: "rejects whitespace-only public_key when configured",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name:      types.StringValue("my-key"),
					PublicKey: types.StringValue("   "),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, diags := buildKeyPairCreateBody(tt.model)
			if tt.expectError {
				if !diags.HasError() {
					t.Fatal("expected error diagnostic, got none")
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.Name != tt.wantName {
				t.Fatalf("got name %q, want %q", body.Name, tt.wantName)
			}
			if tt.wantPubKey == nil && body.PublicKey != nil {
				t.Fatalf("expected nil PublicKey, got %q", *body.PublicKey)
			}
			if tt.wantPubKey != nil && (body.PublicKey == nil || *body.PublicKey != *tt.wantPubKey) {
				t.Fatalf("got PublicKey %v, want %v", body.PublicKey, tt.wantPubKey)
			}
		})
	}
}

func TestBuildKeyPairUpdateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		model       KeyPairResourceModel
		expectError bool
		wantName    string
	}{
		{
			name: "valid name",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue("new-name"),
				},
			},
			wantName: "new-name",
		},
		{
			name: "trims whitespace",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue("  new-name  "),
				},
			},
			wantName: "new-name",
		},
		{
			name: "rejects empty name",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue(""),
				},
			},
			expectError: true,
		},
		{
			name: "rejects whitespace-only name",
			model: KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					Name: types.StringValue("   "),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, diags := buildKeyPairUpdateBody(tt.model)
			if tt.expectError {
				if !diags.HasError() {
					t.Fatal("expected error diagnostic, got none")
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.Name == nil || *body.Name != tt.wantName {
				t.Fatalf("got name %v, want %q", body.Name, tt.wantName)
			}
		})
	}
}

func TestPopulateKeyPairModel(t *testing.T) {
	t.Parallel()

	var nilModel KeyPairModel
	populateKeyPairModel(nil, &nilModel)
	if !nilModel.ID.IsNull() {
		t.Fatalf("expected null ID for nil API object, got %v", nilModel.ID)
	}

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

	var m KeyPairModel
	populateKeyPairModel(api, &m)

	if m.ID.ValueString() != keyPairTestUUID.String() {
		t.Errorf("got ID %s, want %s", m.ID.ValueString(), keyPairTestUUID.String())
	}
	if m.Name.ValueString() != "test-key" {
		t.Errorf("got Name %s, want test-key", m.Name.ValueString())
	}
	if m.PublicKey.ValueString() != "ssh-rsa AAA..." {
		t.Errorf("got PublicKey %s, want ssh-rsa AAA...", m.PublicKey.ValueString())
	}
	if m.Fingerprint.ValueString() != "11:22:33" {
		t.Errorf("got Fingerprint %s, want 11:22:33", m.Fingerprint.ValueString())
	}
	if m.Type.ValueString() != "rsa" {
		t.Errorf("got Type %s, want rsa", m.Type.ValueString())
	}
	if m.CreatedAt.ValueString() != now.Format(time.RFC3339) {
		t.Errorf("got CreatedAt %s, want %s", m.CreatedAt.ValueString(), now.Format(time.RFC3339))
	}
	if m.UpdatedAt.ValueString() != now.Format(time.RFC3339) {
		t.Errorf("got UpdatedAt %s, want %s", m.UpdatedAt.ValueString(), now.Format(time.RFC3339))
	}
}

func TestPopulateKeyPairResourceStatePreservesPrivateKeyAndWhitespace(t *testing.T) {
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

	state := KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			Name:      types.StringValue("  test-key  "),
			PublicKey: types.StringValue("  ssh-rsa AAA...  "),
		},
		PrivateKey: types.StringValue("mock-private-key-material"),
	}

	populateKeyPairResourceState(api, &state)

	if state.Name.ValueString() != "  test-key  " {
		t.Errorf("expected whitespace-preserved name, got %q", state.Name.ValueString())
	}
	if state.PublicKey.ValueString() != "  ssh-rsa AAA...  " {
		t.Errorf("expected whitespace-preserved public_key, got %q", state.PublicKey.ValueString())
	}
	if state.PrivateKey.ValueString() != "mock-private-key-material" {
		t.Errorf("expected private_key to be preserved across read/update, got %q", state.PrivateKey.ValueString())
	}
}

func TestPopulateKeyPairCreatedResourceState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Case 1: Generated keypair with private key returned
	generated := &serversdk.KeyPairCreatedSchema{
		Id:          keyPairTestUUID,
		Name:        "generated-key",
		PublicKey:   "ssh-rsa AAA...",
		PrivateKey:  "mock-private-key-material",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	var genState KeyPairResourceModel
	populateKeyPairCreatedResourceState(generated, &genState)

	if genState.PrivateKey.ValueString() != "mock-private-key-material" {
		t.Fatalf("expected private key to be set, got %v", genState.PrivateKey)
	}
	if genState.PublicKey.ValueString() != "ssh-rsa AAA..." {
		t.Fatalf("expected public key to be set, got %v", genState.PublicKey)
	}

	// Case 2: Imported keypair with empty private key
	imported := &serversdk.KeyPairCreatedSchema{
		Id:          keyPairTestUUID,
		Name:        "imported-key",
		PublicKey:   "ssh-rsa AAA...",
		PrivateKey:  "",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	var impState KeyPairResourceModel
	populateKeyPairCreatedResourceState(imported, &impState)

	if !impState.PrivateKey.IsNull() {
		t.Fatalf("expected null private key for imported keypair, got %v", impState.PrivateKey)
	}
}

func TestKeyPairResourceConfigure(t *testing.T) {
	t.Parallel()

	r := NewKeyPairResource().(*KeyPairResource)

	// nil ProviderData is a no-op
	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on nil ProviderData: %v", resp.Diagnostics)
	}

	// Invalid ProviderData reports error
	var errResp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &errResp)
	if !errResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid ProviderData type")
	}

	// Valid ProviderData
	client, err := serversdk.NewClient("https://server.test")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	var okResp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			Server:    client,
			ProjectID: keyPairTestUUID,
		},
	}, &okResp)
	if okResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid ProviderData: %v", okResp.Diagnostics)
	}
}

func TestKeyPairResourceCreate_Generated(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	createdSchema := serversdk.KeyPairCreatedSchema{
		Id:          keyPairTestUUID,
		Name:        "my-key",
		PublicKey:   "ssh-rsa AAA...",
		PrivateKey:  "mock-private-key-material",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.Path != "/v2/server/key-pairs/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			if req.Header.Get("Project-ID") != keyPairTestUUID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			var body serversdk.KeyPairCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body.Name != "my-key" || body.PublicKey != nil {
				t.Fatalf("unexpected request body: %#v", body)
			}
			return jsonResponse(http.StatusCreated, createdSchema)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			Name:        types.StringValue("my-key"),
			ID:          types.StringUnknown(),
			PublicKey:   types.StringUnknown(),
			Fingerprint: types.StringUnknown(),
			Type:        types.StringUnknown(),
			CreatedAt:   types.StringUnknown(),
			UpdatedAt:   types.StringUnknown(),
		},
		PrivateKey: types.StringUnknown(),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create error: %v", createResp.Diagnostics)
	}

	var state KeyPairResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if state.ID.ValueString() != keyPairTestUUID.String() {
		t.Errorf("got ID %s, want %s", state.ID.ValueString(), keyPairTestUUID.String())
	}
	if state.PrivateKey.ValueString() != "mock-private-key-material" {
		t.Errorf("expected private key to be populated, got %v", state.PrivateKey)
	}
}

func TestKeyPairResourceCreate_Imported(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	createdSchema := serversdk.KeyPairCreatedSchema{
		Id:          keyPairTestUUID,
		Name:        "imported-key",
		PublicKey:   "ssh-rsa AAA... user@host",
		PrivateKey:  "",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.Path != "/v2/server/key-pairs/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			var body serversdk.KeyPairCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body.Name != "imported-key" || body.PublicKey == nil || *body.PublicKey != "ssh-rsa AAA... user@host" {
				t.Fatalf("unexpected request body: %#v", body)
			}
			return jsonResponse(http.StatusCreated, createdSchema)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			Name:        types.StringValue("imported-key"),
			PublicKey:   types.StringValue("ssh-rsa AAA... user@host"),
			ID:          types.StringUnknown(),
			Fingerprint: types.StringUnknown(),
			Type:        types.StringUnknown(),
			CreatedAt:   types.StringUnknown(),
			UpdatedAt:   types.StringUnknown(),
		},
		PrivateKey: types.StringUnknown(),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create error: %v", createResp.Diagnostics)
	}

	var state KeyPairResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if !state.PrivateKey.IsNull() {
		t.Errorf("expected null private key for imported key pair, got %v", state.PrivateKey)
	}
}

func TestKeyPairResourceCreate_APIError(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusConflict,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"detail":"Key pair already exists"}`))),
			}, nil
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			Name: types.StringValue("duplicate-key"),
		},
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var createResp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() || !strings.Contains(createResp.Diagnostics.Errors()[0].Summary(), "Error creating key pair") {
		t.Fatalf("expected create error diagnostic, got %v", createResp.Diagnostics)
	}
}

func TestKeyPairResourceRead_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keyPairSchema := serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "my-key",
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

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue(keyPairTestUUID.String()),
			Name:        types.StringValue("old-name"),
			PublicKey:   types.StringValue("old-pub"),
			Fingerprint: types.StringValue("old-fp"),
			Type:        types.StringValue("rsa"),
			CreatedAt:   types.StringValue(now.Format(time.RFC3339)),
			UpdatedAt:   types.StringValue(now.Format(time.RFC3339)),
		},
		PrivateKey: types.StringValue("existing-private-key"),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	var updatedState KeyPairResourceModel
	if diags := readResp.State.Get(context.Background(), &updatedState); diags.HasError() {
		t.Fatalf("get updated state: %v", diags)
	}

	if updatedState.Name.ValueString() != "my-key" {
		t.Errorf("got name %q, want my-key", updatedState.Name.ValueString())
	}
	if updatedState.PrivateKey.ValueString() != "existing-private-key" {
		t.Errorf("expected private key to be preserved, got %v", updatedState.PrivateKey)
	}
}

func TestKeyPairResourceRead_NotFound(t *testing.T) {
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

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue(keyPairTestUUID.String()),
		},
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 read: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Fatalf("expected resource to be removed from state on 404, got state: %v", readResp.State)
	}
}

func TestKeyPairResourceRead_InvalidUUID(t *testing.T) {
	t.Parallel()

	r := &KeyPairResource{
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue("invalid-uuid"),
		},
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var readResp resource.ReadResponse
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() || !strings.Contains(readResp.Diagnostics.Errors()[0].Detail(), "valid UUID") {
		t.Fatalf("expected invalid UUID error diagnostic, got %v", readResp.Diagnostics)
	}
}

func TestKeyPairResourceUpdate_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedSchema := serversdk.KeyPairSchema{
		Id:          keyPairTestUUID,
		Name:        "renamed-key",
		PublicKey:   "ssh-rsa AAA...",
		Fingerprint: "11:22:33",
		Type:        serversdk.KeyPairTypeRsa,
		CreatedAt:   now,
		UpdatedAt:   now.Add(time.Hour),
	}

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPatch || req.URL.Path != "/v2/server/key-pairs/"+keyPairTestUUID.String()+"/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			var body serversdk.KeyPairPartialUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode update body: %v", err)
			}
			if body.Name == nil || *body.Name != "renamed-key" {
				t.Fatalf("unexpected update body: %#v", body)
			}
			return jsonResponse(http.StatusOK, updatedSchema)
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue(keyPairTestUUID.String()),
			Name:        types.StringValue("old-name"),
			PublicKey:   types.StringValue("ssh-rsa AAA..."),
			Fingerprint: types.StringValue("11:22:33"),
			Type:        types.StringValue("rsa"),
			CreatedAt:   types.StringValue(now.Format(time.RFC3339)),
			UpdatedAt:   types.StringValue(now.Format(time.RFC3339)),
		},
		PrivateKey: types.StringValue("my-private-key"),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue(keyPairTestUUID.String()),
			Name:        types.StringValue("renamed-key"),
			PublicKey:   types.StringValue("ssh-rsa AAA..."),
			Fingerprint: types.StringValue("11:22:33"),
			Type:        types.StringValue("rsa"),
			CreatedAt:   types.StringValue(now.Format(time.RFC3339)),
			UpdatedAt:   types.StringUnknown(),
		},
		PrivateKey: types.StringValue("my-private-key"),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected update error: %v", updateResp.Diagnostics)
	}

	var newState KeyPairResourceModel
	if diags := updateResp.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if newState.Name.ValueString() != "renamed-key" {
		t.Errorf("got name %q, want renamed-key", newState.Name.ValueString())
	}
	if newState.PrivateKey.ValueString() != "my-private-key" {
		t.Errorf("expected private key to be preserved across update, got %v", newState.PrivateKey)
	}
}

func TestKeyPairResourceUpdate_PublicKeyWhitespaceKeepsConfiguredValue(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPatch || req.URL.Path != "/v2/server/key-pairs/"+keyPairTestUUID.String()+"/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read update body: %v", err)
			}
			if strings.Contains(string(raw), "public_key") {
				t.Fatalf("update body must not contain public_key: %s", raw)
			}
			return jsonResponse(http.StatusOK, serversdk.KeyPairSchema{
				Id:          keyPairTestUUID,
				Name:        "my-key",
				PublicKey:   "ssh-rsa AAA... user",
				Fingerprint: "11:22:33",
				Type:        serversdk.KeyPairTypeRsa,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	r := &KeyPairResource{client: client, projectID: keyPairTestUUID}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	priorState := KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID:          types.StringValue(keyPairTestUUID.String()),
			Name:        types.StringValue("my-key"),
			PublicKey:   types.StringValue("ssh-rsa AAA... user"),
			Fingerprint: types.StringValue("11:22:33"),
			Type:        types.StringValue("rsa"),
			CreatedAt:   types.StringValue(now.Format(time.RFC3339)),
			UpdatedAt:   types.StringValue(now.Format(time.RFC3339)),
		},
		PrivateKey: types.StringNull(),
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &priorState); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	planModel := priorState
	planModel.PublicKey = types.StringValue("ssh-rsa AAA... user\n")
	planModel.UpdatedAt = types.StringUnknown()
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected update error: %v", updateResp.Diagnostics)
	}

	var newState KeyPairResourceModel
	if diags := updateResp.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if got := newState.PublicKey.ValueString(); got != "ssh-rsa AAA... user\n" {
		t.Errorf("expected state to keep the configured public_key, got %q", got)
	}
	if got := newState.ID.ValueString(); got != keyPairTestUUID.String() {
		t.Errorf("expected id to stay %s, got %s", keyPairTestUUID, got)
	}
}

func TestKeyPairResourceDelete_Success(t *testing.T) {
	t.Parallel()

	deleted := false
	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodDelete || req.URL.Path != "/v2/server/key-pairs/"+keyPairTestUUID.String()+"/" {
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
			deleted = true
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue(keyPairTestUUID.String()),
		},
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var deleteResp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("unexpected delete error: %v", deleteResp.Diagnostics)
	}
	if !deleted {
		t.Fatal("expected delete request to be sent")
	}
}

func TestKeyPairResourceDelete_NotFound(t *testing.T) {
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

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue(keyPairTestUUID.String()),
		},
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var deleteResp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 delete (should be idempotent): %v", deleteResp.Diagnostics)
	}
}

func TestKeyPairResourceDelete_APIError(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{
		Transport: keyPairRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"detail":"Internal server error"}`))),
			}, nil
		}),
	}

	client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	r := &KeyPairResource{
		client:    client,
		projectID: keyPairTestUUID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &KeyPairResourceModel{
		KeyPairModel: KeyPairModel{
			ID: types.StringValue(keyPairTestUUID.String()),
		},
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var deleteResp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() || !strings.Contains(deleteResp.Diagnostics.Errors()[0].Summary(), "Error deleting key pair") {
		t.Fatalf("expected delete error diagnostic, got %v", deleteResp.Diagnostics)
	}
}

func TestKeyPairResourceImportState(t *testing.T) {
	t.Parallel()

	r := NewKeyPairResource().(*KeyPairResource)

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	var model KeyPairResourceModel
	if diags := response.State.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	r.ImportState(context.Background(), resource.ImportStateRequest{
		ID: keyPairTestUUID.String(),
	}, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import error: %v", response.Diagnostics)
	}

	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() || id.ValueString() != keyPairTestUUID.String() {
		t.Fatalf("expected imported ID %s, got %s (diagnostics: %v)", keyPairTestUUID.String(), id.ValueString(), response.Diagnostics)
	}
}

func TestKeyPairPlanModifiers(t *testing.T) {
	t.Parallel()

	var schemaResp resource.SchemaResponse
	(&KeyPairResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	pubKeyAttr := schemaResp.Schema.Attributes["public_key"].(schema.StringAttribute)
	privKeyAttr := schemaResp.Schema.Attributes["private_key"].(schema.StringAttribute)

	t.Run("public_key unconfigured keeps state without replace", func(t *testing.T) {
		t.Parallel()

		stateModel := KeyPairResourceModel{
			KeyPairModel: KeyPairModel{
				ID:        types.StringValue(keyPairTestUUID.String()),
				Name:      types.StringValue("my-key"),
				PublicKey: types.StringValue("ssh-rsa AAA..."),
			},
		}
		planModel := stateModel

		state := tfsdk.State{Schema: schemaResp.Schema}
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		req := planmodifier.StringRequest{
			ConfigValue: types.StringNull(),
			State:       state,
			Plan:        plan,
			StateValue:  types.StringValue("ssh-rsa AAA..."),
			PlanValue:   types.StringUnknown(),
		}
		var resp planmodifier.StringResponse
		for _, modifier := range pubKeyAttr.PlanModifiers {
			modifier.PlanModifyString(context.Background(), req, &resp)
			req.PlanValue = resp.PlanValue
		}
		if resp.RequiresReplace {
			t.Fatal("expected unconfigured public_key not to require replacement")
		}
		if resp.PlanValue.ValueString() != "ssh-rsa AAA..." {
			t.Fatalf("expected plan value to retain state value, got %v", resp.PlanValue)
		}
	})

	t.Run("public_key changing configured value requires replace", func(t *testing.T) {
		t.Parallel()

		stateModel := KeyPairResourceModel{
			KeyPairModel: KeyPairModel{
				ID:        types.StringValue(keyPairTestUUID.String()),
				Name:      types.StringValue("my-key"),
				PublicKey: types.StringValue("ssh-rsa AAA..."),
			},
		}
		planModel := stateModel
		planModel.PublicKey = types.StringValue("ssh-rsa BBB...")

		state := tfsdk.State{Schema: schemaResp.Schema}
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue("ssh-rsa BBB..."),
			State:       state,
			Plan:        plan,
			StateValue:  types.StringValue("ssh-rsa AAA..."),
			PlanValue:   types.StringValue("ssh-rsa BBB..."),
		}
		var resp planmodifier.StringResponse
		for _, modifier := range pubKeyAttr.PlanModifiers {
			modifier.PlanModifyString(context.Background(), req, &resp)
			req.PlanValue = resp.PlanValue
		}
		if !resp.RequiresReplace {
			t.Fatal("expected changed public_key to require replacement")
		}
	})

	for _, tc := range []struct {
		name        string
		stateValue  string
		configValue types.String
		wantReplace bool
	}{
		{
			name:        "public_key gaining a trailing newline updates in place",
			stateValue:  "ssh-rsa AAA... user",
			configValue: types.StringValue("ssh-rsa AAA... user\n"),
		},
		{
			name:        "public_key losing outer whitespace updates in place",
			stateValue:  "  ssh-rsa AAA... user\r\n",
			configValue: types.StringValue("ssh-rsa AAA... user"),
		},
		{
			name:        "public_key changing key material with a newline requires replace",
			stateValue:  "ssh-rsa AAA... user",
			configValue: types.StringValue("ssh-rsa BBB... user\n"),
			wantReplace: true,
		},
		{
			name:        "public_key changing inner whitespace requires replace",
			stateValue:  "ssh-rsa AAA... user",
			configValue: types.StringValue("ssh-rsa  AAA... user"),
			wantReplace: true,
		},
		{
			name:        "public_key unknown in configuration requires replace",
			stateValue:  "ssh-rsa AAA... user",
			configValue: types.StringUnknown(),
			wantReplace: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stateModel := KeyPairResourceModel{
				KeyPairModel: KeyPairModel{
					ID:        types.StringValue(keyPairTestUUID.String()),
					Name:      types.StringValue("my-key"),
					PublicKey: types.StringValue(tc.stateValue),
				},
			}
			planModel := stateModel
			planModel.PublicKey = tc.configValue

			state := tfsdk.State{Schema: schemaResp.Schema}
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			req := planmodifier.StringRequest{
				ConfigValue: tc.configValue,
				State:       state,
				Plan:        plan,
				StateValue:  types.StringValue(tc.stateValue),
				PlanValue:   tc.configValue,
			}
			resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
			for _, modifier := range pubKeyAttr.PlanModifiers {
				modifier.PlanModifyString(context.Background(), req, &resp)
				req.PlanValue = resp.PlanValue
			}
			if resp.RequiresReplace != tc.wantReplace {
				t.Fatalf("RequiresReplace = %v, want %v", resp.RequiresReplace, tc.wantReplace)
			}
			if !resp.PlanValue.Equal(tc.configValue) {
				t.Fatalf("expected plan value to stay the configured value %v, got %v", tc.configValue, resp.PlanValue)
			}
		})
	}

	t.Run("private_key retains state value when plan is unknown", func(t *testing.T) {
		t.Parallel()

		stateModel := KeyPairResourceModel{
			KeyPairModel: KeyPairModel{
				ID:   types.StringValue(keyPairTestUUID.String()),
				Name: types.StringValue("my-key"),
			},
			PrivateKey: types.StringValue("my-secret-key"),
		}
		planModel := stateModel

		state := tfsdk.State{Schema: schemaResp.Schema}
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		req := planmodifier.StringRequest{
			ConfigValue: types.StringNull(),
			State:       state,
			Plan:        plan,
			StateValue:  types.StringValue("my-secret-key"),
			PlanValue:   types.StringUnknown(),
		}
		var resp planmodifier.StringResponse
		for _, modifier := range privKeyAttr.PlanModifiers {
			modifier.PlanModifyString(context.Background(), req, &resp)
			req.PlanValue = resp.PlanValue
		}
		if resp.PlanValue.ValueString() != "my-secret-key" {
			t.Fatalf("expected private_key to retain state value %q, got %v", "my-secret-key", resp.PlanValue)
		}
	})
}
