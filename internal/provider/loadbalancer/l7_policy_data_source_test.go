package loadbalancer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

func TestL7PolicyDataSourceMetadata(t *testing.T) {
	t.Parallel()

	d := NewL7PolicyDataSource()
	var resp datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_lb_l7_policy" {
		t.Fatalf("expected viettelcloud_lb_l7_policy, got %q", resp.TypeName)
	}
}

func TestL7PolicyDataSourceSchema(t *testing.T) {
	t.Parallel()

	d := &L7PolicyDataSource{}
	var resp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema error: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.Attributes
	expectedAttrs := []string{
		"id", "listener_id", "name", "description", "action", "position",
		"redirect_server_group_id", "redirect_url", "redirect_prefix",
		"redirect_http_code", "rule_count", "provisioning_status", "status",
		"created_at", "updated_at",
	}
	for _, attr := range expectedAttrs {
		if _, ok := attrs[attr]; !ok {
			t.Errorf("missing expected attribute %q", attr)
		}
	}
}

func TestL7PolicyDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := &L7PolicyDataSource{}
	var resp datasource.ConfigureResponse

	// Nil provider data
	d.Configure(context.Background(), datasource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on nil data: %v", resp.Diagnostics)
	}

	// Unexpected provider data
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	// Valid provider data
	resp.Diagnostics = nil
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusOK, `{}`), nil
	})
	d.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			LoadBalancer: client,
			ProjectID:    l7PolicyTestProjectID,
		},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid data: %v", resp.Diagnostics)
	}
	if d.client != client || d.projectID != l7PolicyTestProjectID {
		t.Fatal("data source fields not configured correctly")
	}
}

func TestL7PolicyDataSourceReadByID(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicyDetail()
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/l7policies/"+l7PolicyTestID.String()+"/" {
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		}
		return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestDataSourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                       tftypes.NewValue(tftypes.String, l7PolicyTestID.String()),
			"listener_id":              tftypes.NewValue(tftypes.String, nil),
			"name":                     tftypes.NewValue(tftypes.String, nil),
			"description":              tftypes.NewValue(tftypes.String, nil),
			"action":                   tftypes.NewValue(tftypes.String, nil),
			"position":                 tftypes.NewValue(tftypes.Number, nil),
			"redirect_server_group_id": tftypes.NewValue(tftypes.String, nil),
			"redirect_url":             tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":          tftypes.NewValue(tftypes.String, nil),
			"redirect_http_code":       tftypes.NewValue(tftypes.Number, nil),
			"rule_count":               tftypes.NewValue(tftypes.Number, nil),
			"provisioning_status":      tftypes.NewValue(tftypes.String, nil),
			"status":                   tftypes.NewValue(tftypes.String, nil),
			"created_at":               tftypes.NewValue(tftypes.String, nil),
			"updated_at":               tftypes.NewValue(tftypes.String, nil),
		}),
	}

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read by id failed: %v", resp.Diagnostics)
	}

	var model L7PolicyDataSourceModel
	resp.State.Get(context.Background(), &model)
	if model.ID.ValueString() != l7PolicyTestID.String() || model.Name.ValueString() != "example-policy" {
		t.Fatalf("unexpected model: %#v", model)
	}
}

func TestL7PolicyDataSourceReadByIDDeletedStateErrors(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicyDetail()
	policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestDataSourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                       tftypes.NewValue(tftypes.String, l7PolicyTestID.String()),
			"listener_id":              tftypes.NewValue(tftypes.String, nil),
			"name":                     tftypes.NewValue(tftypes.String, nil),
			"description":              tftypes.NewValue(tftypes.String, nil),
			"action":                   tftypes.NewValue(tftypes.String, nil),
			"position":                 tftypes.NewValue(tftypes.Number, nil),
			"redirect_server_group_id": tftypes.NewValue(tftypes.String, nil),
			"redirect_url":             tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":          tftypes.NewValue(tftypes.String, nil),
			"redirect_http_code":       tftypes.NewValue(tftypes.Number, nil),
			"rule_count":               tftypes.NewValue(tftypes.Number, nil),
			"provisioning_status":      tftypes.NewValue(tftypes.String, nil),
			"status":                   tftypes.NewValue(tftypes.String, nil),
			"created_at":               tftypes.NewValue(tftypes.String, nil),
			"updated_at":               tftypes.NewValue(tftypes.String, nil),
		}),
	}

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "L7 policy not found" {
		t.Fatalf("expected error on DELETED policy, got %v", resp.Diagnostics)
	}
}

func TestL7PolicyDataSourceReadByFilter(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicySchema()
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/l7policies/" {
			page := loadbalancersdk.PagedL7PolicySchema{
				Count:   1,
				Results: []loadbalancersdk.L7PolicySchema{*policy},
			}
			b, _ := json.Marshal(page)
			return l7PolicyTestHTTPResponse(req, http.StatusOK, string(b)), nil
		}
		return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)

	config := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestDataSourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                       tftypes.NewValue(tftypes.String, nil),
			"listener_id":              tftypes.NewValue(tftypes.String, l7PolicyTestListenerID.String()),
			"name":                     tftypes.NewValue(tftypes.String, "example-policy"),
			"description":              tftypes.NewValue(tftypes.String, nil),
			"action":                   tftypes.NewValue(tftypes.String, nil),
			"position":                 tftypes.NewValue(tftypes.Number, nil),
			"redirect_server_group_id": tftypes.NewValue(tftypes.String, nil),
			"redirect_url":             tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":          tftypes.NewValue(tftypes.String, nil),
			"redirect_http_code":       tftypes.NewValue(tftypes.Number, nil),
			"rule_count":               tftypes.NewValue(tftypes.Number, nil),
			"provisioning_status":      tftypes.NewValue(tftypes.String, nil),
			"status":                   tftypes.NewValue(tftypes.String, nil),
			"created_at":               tftypes.NewValue(tftypes.String, nil),
			"updated_at":               tftypes.NewValue(tftypes.String, nil),
		}),
	}

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read by filter failed: %v", resp.Diagnostics)
	}

	var model L7PolicyDataSourceModel
	resp.State.Get(context.Background(), &model)
	if model.ID.ValueString() != l7PolicyTestID.String() || model.Name.ValueString() != "example-policy" {
		t.Fatalf("unexpected model: %#v", model)
	}
}

func TestL7PolicyDataSourceValidation(t *testing.T) {
	t.Parallel()

	d := &L7PolicyDataSource{
		projectID: l7PolicyTestProjectID,
	}

	tests := []struct {
		name      string
		config    L7PolicyDataSourceModel
		wantError string
	}{
		{
			name: "missing all criteria",
			config: L7PolicyDataSourceModel{
				L7PolicyModel: L7PolicyModel{},
			},
			wantError: "Missing L7 policy lookup criteria",
		},
		{
			name: "whitespace id",
			config: L7PolicyDataSourceModel{
				L7PolicyModel: L7PolicyModel{
					ID: types.StringValue("   "),
				},
			},
			wantError: "Invalid lookup criteria",
		},
		{
			name: "whitespace name",
			config: L7PolicyDataSourceModel{
				L7PolicyModel: L7PolicyModel{
					Name: types.StringValue("   "),
				},
			},
			wantError: "Invalid lookup criteria",
		},
		{
			name: "whitespace listener_id",
			config: L7PolicyDataSourceModel{
				L7PolicyModel: L7PolicyModel{
					Name: types.StringValue("test"),
				},
				ListenerID: types.StringValue("   "),
			},
			wantError: "Invalid lookup criteria",
		},
		{
			name: "invalid uuid id",
			config: L7PolicyDataSourceModel{
				L7PolicyModel: L7PolicyModel{
					ID: types.StringValue("not-a-uuid"),
				},
			},
			wantError: "Invalid UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, diags := d.getL7Policy(context.Background(), tt.config)
			if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, diags)
			}
		})
	}
}

func TestL7PolicyDataSourceReadByIDNotFound(t *testing.T) {
	t.Parallel()

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	_, diags := d.getL7PolicyByID(context.Background(), L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			ID: types.StringValue(l7PolicyTestID.String()),
		},
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "not found") {
		t.Fatalf("expected not found error, got %v", diags)
	}
}

func TestL7PolicyDataSourceReadByIDAPIError(t *testing.T) {
	t.Parallel()

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusInternalServerError, `{"detail":"server error"}`), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	_, diags := d.getL7PolicyByID(context.Background(), L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			ID: types.StringValue(l7PolicyTestID.String()),
		},
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "Error fetching L7 policy") {
		t.Fatalf("expected API error, got %v", diags)
	}
}

func TestL7PolicyDataSourceReadByIDListenerMismatch(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicyDetail()
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	differentListenerID := core.UUID{7}
	_, diags := d.getL7PolicyByID(context.Background(), L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			ID: types.StringValue(l7PolicyTestID.String()),
		},
		ListenerID: types.StringValue(differentListenerID.String()),
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "does not belong to listener") {
		t.Fatalf("expected listener mismatch error, got %v", diags)
	}
}

func TestL7PolicyDataSourceReadByFilterNotFound(t *testing.T) {
	t.Parallel()

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		page := loadbalancersdk.PagedL7PolicySchema{
			Count:   0,
			Results: []loadbalancersdk.L7PolicySchema{},
		}
		b, _ := json.Marshal(page)
		return l7PolicyTestHTTPResponse(req, http.StatusOK, string(b)), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	_, diags := d.getL7PolicyByFilter(context.Background(), L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			Name: types.StringValue("non-existent"),
		},
	})
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "Error resolving L7 policy") {
		t.Fatalf("expected resolving error, got %v", diags)
	}
}

func TestL7PolicyDataSourceReadByMultiCriteriaFilter(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicySchema()
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		page := loadbalancersdk.PagedL7PolicySchema{
			Count:   1,
			Results: []loadbalancersdk.L7PolicySchema{*policy},
		}
		b, _ := json.Marshal(page)
		return l7PolicyTestHTTPResponse(req, http.StatusOK, string(b)), nil
	})

	d := &L7PolicyDataSource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	// When both ID and Name are provided, getL7Policy dispatches to getL7PolicyByFilter
	res, diags := d.getL7Policy(context.Background(), L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			ID:   types.StringValue(l7PolicyTestID.String()),
			Name: types.StringValue("example-policy"),
		},
		ListenerID: types.StringValue(l7PolicyTestListenerID.String()),
	})
	if diags.HasError() {
		t.Fatalf("expected multi-criteria filter lookup to succeed, got %v", diags)
	}
	if res.Id != l7PolicyTestID {
		t.Fatalf("expected ID %s, got %s", l7PolicyTestID, res.Id)
	}
}

func TestPreserveL7PolicyConfiguredValues(t *testing.T) {
	t.Parallel()

	// 1. policy == nil does nothing
	var state L7PolicyDataSourceModel
	config := L7PolicyDataSourceModel{
		L7PolicyModel: L7PolicyModel{
			ID:   types.StringValue(l7PolicyTestID.String()),
			Name: types.StringValue("example-policy"),
		},
		ListenerID: types.StringValue(l7PolicyTestListenerID.String()),
	}
	preserveL7PolicyConfiguredValues(nil, config, &state)
	if !state.ID.IsNull() {
		t.Fatalf("expected state.ID to remain null when policy is nil, got %v", state.ID)
	}

	// 2. Matching UUIDs and Name are preserved
	policy := sampleL7PolicySchema()
	preserveL7PolicyConfiguredValues(policy, config, &state)
	if state.ID.ValueString() != l7PolicyTestID.String() {
		t.Fatalf("expected ID to be preserved, got %v", state.ID)
	}
	if state.Name.ValueString() != "example-policy" {
		t.Fatalf("expected Name to be preserved, got %v", state.Name)
	}
	if state.ListenerID.ValueString() != l7PolicyTestListenerID.String() {
		t.Fatalf("expected ListenerID to be preserved, got %v", state.ListenerID)
	}
}
