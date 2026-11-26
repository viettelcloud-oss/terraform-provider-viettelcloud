package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

func TestL7PolicyResourceMetadata(t *testing.T) {
	t.Parallel()

	r := NewL7PolicyResource()
	var resp resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_lb_l7_policy" {
		t.Fatalf("expected viettelcloud_lb_l7_policy, got %q", resp.TypeName)
	}
}

// TestL7PolicyListenerReplacementIgnoresSpelling checks that a listener
// reference replaces the policy only when it names another listener, and not
// when the same UUID differs by case or outer whitespace.
func TestL7PolicyListenerReplacementIgnoresSpelling(t *testing.T) {
	t.Parallel()

	r := &L7PolicyResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	listener := schemaResp.Schema.Attributes["listener"].(schema.SingleNestedAttribute)
	state := reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String()))
	// The Framework skips replacement while the resource is created or destroyed,
	// so the request needs a state and a plan that are not null.
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})

	for _, tt := range []struct {
		name            string
		id              string
		wantReplacement bool
	}{
		{name: "same id", id: l7PolicyTestListenerID.String()},
		{name: "upper case with outer whitespace", id: "  " + strings.ToUpper(l7PolicyTestListenerID.String()) + "\n"},
		{name: "another listener", id: l7PolicyTestLoadBalancerID.String(), wantReplacement: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			planned := reference.Value(reference.ID, types.StringValue(tt.id))
			request := planmodifier.ObjectRequest{
				Path:  path.Root("listener"),
				State: tfsdk.State{Schema: schemaResp.Schema, Raw: existing}, Plan: tfsdk.Plan{Schema: schemaResp.Schema, Raw: existing},
				ConfigValue: planned, PlanValue: planned, StateValue: state,
			}
			response := planmodifier.ObjectResponse{PlanValue: planned}
			for _, modifier := range listener.PlanModifiers {
				modifier.PlanModifyObject(context.Background(), request, &response)
			}
			if response.RequiresReplace != tt.wantReplacement {
				t.Fatalf("listener %q: replace=%t, want %t", tt.id, response.RequiresReplace, tt.wantReplacement)
			}
		})
	}
}

func TestL7PolicyResourceSchema(t *testing.T) {
	t.Parallel()

	r := &L7PolicyResource{}
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema error: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.Attributes
	expectedAttrs := []string{
		"id", "listener", "name", "description", "action", "position",
		"redirect_server_group", "redirect_url", "redirect_prefix",
		"redirect_http_code", "rule_count", "provisioning_status", "status",
		"created_at", "updated_at",
	}
	for _, attr := range expectedAttrs {
		if _, ok := attrs[attr]; !ok {
			t.Errorf("missing expected attribute %q", attr)
		}
	}

	if !attrs["id"].IsComputed() {
		t.Errorf("id should be computed")
	}
	if !attrs["listener"].IsRequired() {
		t.Errorf("listener should be required")
	}
	if !attrs["redirect_server_group"].IsOptional() || attrs["redirect_server_group"].IsComputed() {
		t.Errorf("redirect_server_group should be optional and not computed")
	}
	if !attrs["name"].IsRequired() {
		t.Errorf("name should be required")
	}
	if !attrs["action"].IsRequired() {
		t.Errorf("action should be required")
	}
	if !attrs["description"].IsOptional() || !attrs["description"].IsComputed() {
		t.Errorf("description should be optional and computed")
	}
	if !attrs["position"].IsOptional() || !attrs["position"].IsComputed() {
		t.Errorf("position should be optional and computed")
	}
}

func TestL7PolicyResourceConfigure(t *testing.T) {
	t.Parallel()

	r := &L7PolicyResource{}
	var resp resource.ConfigureResponse

	// Nil provider data
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on nil data: %v", resp.Diagnostics)
	}

	// Unexpected provider data
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	// Valid provider data
	resp.Diagnostics = nil
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusOK, `{}`), nil
	})
	claims := keylock.New()
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			LoadBalancer: client,
			ProjectID:    l7PolicyTestProjectID,
			Claims:       claims,
		},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid data: %v", resp.Diagnostics)
	}
	if r.client != client || r.projectID != l7PolicyTestProjectID || r.claims != claims {
		t.Fatal("resource fields not configured correctly")
	}
}

func TestL7PolicyResourceValidateConfig(t *testing.T) {
	t.Parallel()

	serverGroup := reference.Value(reference.ID, types.StringValue(l7PolicyTestServerGroupID.String()))
	tests := []struct {
		name      string
		configure func(*L7PolicyResourceModel)
		wantError string
	}{
		{name: "reject without targets", configure: func(*L7PolicyResourceModel) {}},
		{name: "unknown action", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringUnknown()
			m.RedirectURL = types.StringValue("https://example.com")
		}},
		{name: "invalid action", configure: func(m *L7PolicyResourceModel) { m.Action = types.StringValue("DROP") }, wantError: "Invalid L7 policy action"},
		{name: "reject with code", configure: func(m *L7PolicyResourceModel) { m.RedirectHTTPCode = types.Int64Value(301) }, wantError: "redirect_http_code cannot be configured"},
		{name: "server group alias", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("redirect_to_pool")
			m.RedirectServerGroup = serverGroup
		}},
		{name: "server group missing", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_TO_SERVER_GROUP")
		}, wantError: "redirect_server_group is required"},
		{name: "server group with url", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_TO_SERVER_GROUP")
			m.RedirectServerGroup = serverGroup
			m.RedirectURL = types.StringValue("https://example.com")
		}, wantError: "redirect_url cannot be configured"},
		{name: "server group with code", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_TO_SERVER_GROUP")
			m.RedirectServerGroup = serverGroup
			m.RedirectHTTPCode = types.Int64Value(301)
		}, wantError: "redirect_http_code cannot be configured"},
		{name: "url with code", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_TO_URL")
			m.RedirectURL = types.StringValue("https://example.com")
			m.RedirectHTTPCode = types.Int64Value(301)
		}},
		{name: "url missing", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_TO_URL")
		}, wantError: "redirect_url is required"},
		{name: "prefix with server group", configure: func(m *L7PolicyResourceModel) {
			m.Action = types.StringValue("REDIRECT_PREFIX")
			m.RedirectPrefix = types.StringValue("https://example.com")
			m.RedirectServerGroup = serverGroup
		}, wantError: "redirect_server_group cannot be configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			model := l7PolicyTestModel("REJECT")
			model.ID = types.StringNull()
			tt.configure(&model)
			plan := l7PolicyTestPlan(t, &model)
			resp := &resource.ValidateConfigResponse{}
			(&L7PolicyResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{
				Config: tfsdk.Config(plan),
			}, resp)
			if tt.wantError == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected error: %v", resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), tt.wantError) {
				t.Fatalf("expected error %q, got %v", tt.wantError, resp.Diagnostics)
			}
		})
	}
}

func TestL7PolicyResourceCreateReadUpdateDelete(t *testing.T) {
	t.Parallel()

	currentPolicy := sampleL7PolicyDetail()
	deleted := false

	parents := &l7PolicyTestAPI{t: t}
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		if resp, ok := parents.serveParents(req); ok {
			return resp, nil
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/l7policies/":
			var createBody loadbalancersdk.L7PolicyCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&createBody); err != nil {
				return nil, err
			}
			return l7PolicyTestHTTPResponse(req, http.StatusCreated, l7PolicySchemaJSON(l7PolicyDetailToSchema(currentPolicy))), nil

		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/l7policies/"+l7PolicyTestID.String()+"/":
			if deleted {
				return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(currentPolicy)), nil

		case req.Method == http.MethodPatch && req.URL.Path == "/v2/load-balancing/l7policies/"+l7PolicyTestID.String()+"/":
			var updateBody loadbalancersdk.L7PolicyPartialUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&updateBody); err != nil {
				return nil, err
			}
			if updateBody.Name.IsSpecified() {
				currentPolicy.Name = updateBody.Name
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicySchemaJSON(l7PolicyDetailToSchema(currentPolicy))), nil

		case req.Method == http.MethodDelete && req.URL.Path == "/v2/load-balancing/l7policies/"+l7PolicyTestID.String()+"/":
			deleted = true
			return l7PolicyTestHTTPResponse(req, http.StatusAccepted, ""), nil

		default:
			return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})

	r := &L7PolicyResource{
		client:    client,
		projectID: l7PolicyTestProjectID,
		claims:    keylock.New(),
	}

	// 1. Create
	schemaResp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, schemaResp)
	tfsdkSchema := schemaResp.Schema

	planState := tfsdk.State{
		Schema: tfsdkSchema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestResourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                    tftypes.NewValue(tftypes.String, nil),
			"listener":              l7PolicyTestReference(l7PolicyTestListenerID.String()),
			"name":                  tftypes.NewValue(tftypes.String, "example-policy"),
			"description":           tftypes.NewValue(tftypes.String, "example description"),
			"action":                tftypes.NewValue(tftypes.String, "REDIRECT_PREFIX"),
			"position":              tftypes.NewValue(tftypes.Number, 1),
			"redirect_server_group": l7PolicyTestReference(nil),
			"redirect_url":          tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":       tftypes.NewValue(tftypes.String, "https://example.com"),
			"redirect_http_code":    tftypes.NewValue(tftypes.Number, 301),
			"rule_count":            tftypes.NewValue(tftypes.Number, nil),
			"provisioning_status":   tftypes.NewValue(tftypes.String, nil),
			"status":                tftypes.NewValue(tftypes.String, nil),
			"created_at":            tftypes.NewValue(tftypes.String, nil),
			"updated_at":            tftypes.NewValue(tftypes.String, nil),
		}),
	}
	createResp := &resource.CreateResponse{
		State: tfsdk.State{Schema: tfsdkSchema},
	}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(planState)}, createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var createdModel L7PolicyResourceModel
	createResp.State.Get(context.Background(), &createdModel)
	if createdModel.ID.ValueString() != l7PolicyTestID.String() {
		t.Fatalf("expected ID %s, got %s", l7PolicyTestID, createdModel.ID.ValueString())
	}

	// 2. Read
	readResp := &resource.ReadResponse{
		State: createResp.State,
	}
	r.Read(context.Background(), resource.ReadRequest{State: createResp.State}, readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	// 3. Update
	updatedPlanState := tfsdk.State{
		Schema: tfsdkSchema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestResourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                    tftypes.NewValue(tftypes.String, l7PolicyTestID.String()),
			"listener":              l7PolicyTestReference(l7PolicyTestListenerID.String()),
			"name":                  tftypes.NewValue(tftypes.String, "renamed-policy"),
			"description":           tftypes.NewValue(tftypes.String, "example description"),
			"action":                tftypes.NewValue(tftypes.String, "REDIRECT_PREFIX"),
			"position":              tftypes.NewValue(tftypes.Number, 1),
			"redirect_server_group": l7PolicyTestReference(nil),
			"redirect_url":          tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":       tftypes.NewValue(tftypes.String, "https://example.com"),
			"redirect_http_code":    tftypes.NewValue(tftypes.Number, 301),
			"rule_count":            tftypes.NewValue(tftypes.Number, 0),
			"provisioning_status":   tftypes.NewValue(tftypes.String, "ACTIVE"),
			"status":                tftypes.NewValue(tftypes.String, "ONLINE"),
			"created_at":            tftypes.NewValue(tftypes.String, "2026-01-01T00:00:00Z"),
			"updated_at":            tftypes.NewValue(tftypes.String, "2026-01-01T00:00:00Z"),
		}),
	}
	updateResp := &resource.UpdateResponse{
		State: readResp.State,
	}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  tfsdk.Plan(updatedPlanState),
		State: readResp.State,
	}, updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}

	var updatedModel L7PolicyResourceModel
	updateResp.State.Get(context.Background(), &updatedModel)
	if updatedModel.Name.ValueString() != "renamed-policy" {
		t.Fatalf("expected updated name 'renamed-policy', got %s", updatedModel.Name.ValueString())
	}

	// 3b. Update with no changes (verifies r.get + populate newState path)
	noChangeUpdateResp := &resource.UpdateResponse{
		State: updateResp.State,
	}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  tfsdk.Plan(updatedPlanState),
		State: updateResp.State,
	}, noChangeUpdateResp)
	if noChangeUpdateResp.Diagnostics.HasError() {
		t.Fatalf("update with no changes failed: %v", noChangeUpdateResp.Diagnostics)
	}
	var noChangeModel L7PolicyResourceModel
	noChangeUpdateResp.State.Get(context.Background(), &noChangeModel)
	if noChangeModel.Name.ValueString() != "renamed-policy" {
		t.Fatalf("expected name to be preserved: %s", noChangeModel.Name.ValueString())
	}

	// 4. Delete
	delResp := &resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: updateResp.State}, delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", delResp.Diagnostics)
	}
	if !deleted {
		t.Fatal("expected policy to be deleted")
	}

	// 5. Read after delete removes resource from state
	postDelReadResp := &resource.ReadResponse{
		State: updateResp.State,
	}
	r.Read(context.Background(), resource.ReadRequest{State: updateResp.State}, postDelReadResp)
	if postDelReadResp.Diagnostics.HasError() {
		t.Fatalf("read after delete returned error: %v", postDelReadResp.Diagnostics)
	}
	var emptyCheck L7PolicyResourceModel
	postDelReadResp.State.Get(context.Background(), &emptyCheck)
	if !emptyCheck.ID.IsNull() {
		t.Fatal("expected resource state to be removed after 404")
	}
}

func TestL7PolicyResourceCreateAPIFailure(t *testing.T) {
	t.Parallel()

	parents := &l7PolicyTestAPI{t: t}
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		if resp, ok := parents.serveParents(req); ok {
			return resp, nil
		}
		return l7PolicyTestHTTPResponse(req, http.StatusInternalServerError, `{"detail":"server error"}`), nil
	})

	r := &L7PolicyResource{
		client:    client,
		projectID: l7PolicyTestProjectID,
		claims:    keylock.New(),
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	planState := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestResourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                    tftypes.NewValue(tftypes.String, nil),
			"listener":              l7PolicyTestReference(l7PolicyTestListenerID.String()),
			"name":                  tftypes.NewValue(tftypes.String, "example-policy"),
			"description":           tftypes.NewValue(tftypes.String, nil),
			"action":                tftypes.NewValue(tftypes.String, "REJECT"),
			"position":              tftypes.NewValue(tftypes.Number, 1),
			"redirect_server_group": l7PolicyTestReference(nil),
			"redirect_url":          tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":       tftypes.NewValue(tftypes.String, nil),
			"redirect_http_code":    tftypes.NewValue(tftypes.Number, nil),
			"rule_count":            tftypes.NewValue(tftypes.Number, nil),
			"provisioning_status":   tftypes.NewValue(tftypes.String, nil),
			"status":                tftypes.NewValue(tftypes.String, nil),
			"created_at":            tftypes.NewValue(tftypes.String, nil),
			"updated_at":            tftypes.NewValue(tftypes.String, nil),
		}),
	}
	createResp := &resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan(planState)}, createResp)
	if !createResp.Diagnostics.HasError() || !strings.Contains(createResp.Diagnostics.Errors()[0].Summary(), "Error creating L7 policy") {
		t.Fatalf("expected create error, got %v", createResp.Diagnostics)
	}
}

func TestL7PolicyResourceCreateKeepsIDWhenWaitFails(t *testing.T) {
	t.Parallel()

	parents := &l7PolicyTestAPI{t: t}
	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		if resp, ok := parents.serveParents(req); ok {
			return resp, nil
		}
		if req.Method == http.MethodPost {
			created := sampleL7PolicySchema()
			created.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
			return l7PolicyTestHTTPResponse(req, http.StatusCreated, l7PolicySchemaJSON(created)), nil
		}
		failed := sampleL7PolicyDetail()
		failed.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusERROR
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(failed)), nil
	})
	r := &L7PolicyResource{client: client, projectID: l7PolicyTestProjectID, claims: keylock.New()}

	model := l7PolicyTestModel("REJECT")
	resp := &resource.CreateResponse{State: tfsdk.State{Schema: l7PolicyTestSchema(t)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: l7PolicyTestPlan(t, &model)}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected the ERROR status to fail the create")
	}

	var state L7PolicyResourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != l7PolicyTestID.String() || state.ProvisioningStatus.ValueString() != "PENDING_CREATE" {
		t.Fatalf("expected the created ID and response status in state, got id=%s status=%s", state.ID, state.ProvisioningStatus)
	}
	if state.Position.IsUnknown() || state.Description.IsUnknown() || state.RuleCount.IsUnknown() {
		t.Fatalf("expected known or null values in state, got %+v", state)
	}
}

func TestL7PolicyResourceReadRemovesResourceOnDeletedStatus(t *testing.T) {
	t.Parallel()

	deletedPolicy := sampleL7PolicyDetail()
	deletedPolicy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(deletedPolicy)), nil
	})

	r := &L7PolicyResource{
		client:    client,
		projectID: l7PolicyTestProjectID,
	}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw: tftypes.NewValue(tftypes.Object{AttributeTypes: l7PolicyTestResourceAttributeTypes()}, map[string]tftypes.Value{
			"id":                    tftypes.NewValue(tftypes.String, l7PolicyTestID.String()),
			"listener":              l7PolicyTestReference(l7PolicyTestListenerID.String()),
			"name":                  tftypes.NewValue(tftypes.String, "deleted-policy"),
			"description":           tftypes.NewValue(tftypes.String, nil),
			"action":                tftypes.NewValue(tftypes.String, "REJECT"),
			"position":              tftypes.NewValue(tftypes.Number, 1),
			"redirect_server_group": l7PolicyTestReference(nil),
			"redirect_url":          tftypes.NewValue(tftypes.String, nil),
			"redirect_prefix":       tftypes.NewValue(tftypes.String, nil),
			"redirect_http_code":    tftypes.NewValue(tftypes.Number, nil),
			"rule_count":            tftypes.NewValue(tftypes.Number, 0),
			"provisioning_status":   tftypes.NewValue(tftypes.String, "ACTIVE"),
			"status":                tftypes.NewValue(tftypes.String, "ONLINE"),
			"created_at":            tftypes.NewValue(tftypes.String, "2026-01-01T00:00:00Z"),
			"updated_at":            tftypes.NewValue(tftypes.String, "2026-01-01T00:00:00Z"),
		}),
	}
	resp := &resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", resp.Diagnostics)
	}
	var model L7PolicyResourceModel
	resp.State.Get(context.Background(), &model)
	if !model.ID.IsNull() {
		t.Fatal("expected resource state to be removed when provisioning status is DELETED")
	}
}

func TestL7PolicyResourceReadRefreshesReorderedPositionAndNilName(t *testing.T) {
	t.Parallel()

	client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
		policy := sampleL7PolicyDetail()
		policy.Position = 3
		policy.Name.SetNull()
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
	})
	r := &L7PolicyResource{client: client, projectID: l7PolicyTestProjectID}

	model := l7PolicyTestModel("REDIRECT_PREFIX")
	model.ID = types.StringValue(l7PolicyTestID.String())
	model.Position = types.Int64Value(1)
	state := tfsdk.State(l7PolicyTestPlan(t, &model))
	resp := &resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}

	var refreshed L7PolicyResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &refreshed)...)
	if refreshed.Position.ValueInt64() != 3 {
		t.Fatalf("expected the position that another policy's reorder caused, got %s", refreshed.Position)
	}
	if !refreshed.Name.IsNull() {
		t.Fatalf("expected a nil name to map to null, got %s", refreshed.Name)
	}
}

func TestL7PolicyDeleteOfAGonePolicySendsNoRequest(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		policy []loadbalancersdk.LoadBalancingProvisioningStatus
	}{
		{"not found", []loadbalancersdk.LoadBalancingProvisioningStatus{l7PolicyTestGone}},
		{"deleted status", []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusDELETED}},
		{"pending delete then not found", []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, l7PolicyTestGone}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{policy: tt.policy}
				r := newL7PolicyTestResource(t, api)

				if diags := runL7PolicyTestOperation(t, r, "delete"); diags.HasError() {
					t.Fatalf("delete: %v", diags)
				}
				if api.mutations() != 0 {
					t.Fatalf("mutations=%d, want 0", api.mutations())
				}
			})
		})
	}
}

// A failed Delete of an L7 policy that started in ERROR or UNKNOWN reports the
// failure and keeps the state. After PENDING_DELETE, the failure shows at once.
// Without PENDING_DELETE, Delete cannot tell it from the state before the
// DELETE, so it waits until timeout.
func TestL7PolicyDeleteFromFailedStateKeepsState(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		for name, tt := range map[string]struct {
			afterDelete []loadbalancersdk.LoadBalancingProvisioningStatus
			wantErr     string
			wantWait    time.Duration
			wantTimeout bool
		}{
			"fails at once after PENDING_DELETE": {
				afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, status},
				wantErr:     "terminal provisioning status " + string(status) + " while deleting",
				wantWait:    l7PolicyPollInterval,
			},
			"waits until timeout without PENDING_DELETE": {
				afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{status},
				wantErr:     "still deleting (" + string(status) + ")",
				wantWait:    l7PolicyOperationTimeout,
				wantTimeout: true,
			},
		} {
			t.Run(string(status)+" "+name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					api := &l7PolicyTestAPI{
						policy:  []loadbalancersdk.LoadBalancingProvisioningStatus{status},
						settled: tt.afterDelete,
					}
					r := newL7PolicyTestResource(t, api)

					model := l7PolicyTestModel("REJECT")
					model.ID = types.StringValue(l7PolicyTestID.String())
					state := tfsdk.State(l7PolicyTestPlan(t, &model))
					start := time.Now()
					resp := resource.DeleteResponse{State: state}
					r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
					if api.mutations() != 1 {
						t.Fatalf("mutations=%d, want 1", api.mutations())
					}
					errs := resp.Diagnostics.Errors()
					if len(errs) != 1 || errs[0].Summary() != "Error waiting for L7 policy deletion" || !strings.Contains(errs[0].Detail(), tt.wantErr) {
						t.Fatalf("expected a deletion wait error containing %q, got %v", tt.wantErr, resp.Diagnostics)
					}
					if timedOut := strings.Contains(errs[0].Detail(), wait.ErrTimeout.Error()); timedOut != tt.wantTimeout {
						t.Fatalf("timeout = %t, want %t: %s", timedOut, tt.wantTimeout, errs[0].Detail())
					}
					if waited := time.Since(start); waited != tt.wantWait {
						t.Fatalf("waited %s, want %s", waited, tt.wantWait)
					}
					if !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("a failed delete must keep the state")
					}
				})
			})
		}
	}
}

// When no Load Balancer owns the listener any more, Delete completes only if
// the policy is gone as well.
func TestL7PolicyDeleteWithoutListener(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		policy   loadbalancersdk.LoadBalancingProvisioningStatus
		wantDone bool
	}{
		{"policy gone", l7PolicyTestGone, true},
		{"policy left", l7PolicyTestActive, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{noListener: true, policy: []loadbalancersdk.LoadBalancingProvisioningStatus{tt.policy}}
				r := newL7PolicyTestResource(t, api)

				diags := runL7PolicyTestOperation(t, r, "delete")
				if diags.HasError() == tt.wantDone {
					t.Fatalf("diagnostics=%v, want done=%t", diags, tt.wantDone)
				}
				if api.mutations() != 0 {
					t.Fatalf("mutations=%d, want 0", api.mutations())
				}
			})
		})
	}
}

func TestL7PolicyResourceDeleteNotFoundSucceeds(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		api := &l7PolicyTestAPI{answers: []l7PolicyTestAnswer{{http.StatusNotFound, `{"detail":"not found"}`}}}
		r := newL7PolicyTestResource(t, api)

		if diags := runL7PolicyTestOperation(t, r, "delete"); diags.HasError() {
			t.Fatalf("delete: %v", diags)
		}
		if api.mutations() != 1 {
			t.Fatalf("mutations=%d, want 1", api.mutations())
		}
	})
}

func TestL7PolicyResourceImportState(t *testing.T) {
	t.Parallel()

	r := &L7PolicyResource{}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	response := resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(context.Background()), nil),
	}}

	r.ImportState(context.Background(), resource.ImportStateRequest{
		ID: l7PolicyTestID.String(),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import state failed: %v", response.Diagnostics)
	}

	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() || id.ValueString() != l7PolicyTestID.String() {
		t.Fatalf("unexpected imported ID %q: %v", id.ValueString(), response.Diagnostics)
	}
}

func TestL7PolicyResourceImportStateRejectsInvalidID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "   ", "not-a-uuid", l7PolicyTestListenerID.String() + "/" + l7PolicyTestID.String()} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			sch := l7PolicyTestSchema(t)
			resp := &resource.ImportStateResponse{State: tfsdk.State{
				Schema: sch,
				Raw:    tftypes.NewValue(sch.Type().TerraformType(context.Background()), nil),
			}}
			(&L7PolicyResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected import ID %q to be rejected", id)
			}
		})
	}
}

func TestL7PolicyWaitUntilActiveSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			calls++
			policy := sampleL7PolicyDetail()
			if calls == 1 {
				policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
			} else {
				policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})

		r := &L7PolicyResource{
			client:    client,
			projectID: l7PolicyTestProjectID,
		}

		start := time.Now()
		detail, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if err != nil {
			t.Fatalf("wait failed: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
		if waited := time.Since(start); waited != l7PolicyPollInterval {
			t.Fatalf("expected waited %s, got %s", l7PolicyPollInterval, waited)
		}
		if detail.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE {
			t.Fatalf("expected ACTIVE, got %s", detail.ProvisioningStatus)
		}
	})
}

func TestL7PolicyWaitUntilActiveFailsOnStatusError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			policy := sampleL7PolicyDetail()
			policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusERROR
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})

		r := &L7PolicyResource{
			client:    client,
			projectID: l7PolicyTestProjectID,
		}

		_, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if err == nil || !strings.Contains(err.Error(), "terminal provisioning status ERROR") {
			t.Fatalf("expected error state failure, got %v", err)
		}
		if errors.Is(err, wait.ErrNotReady) {
			t.Fatal("terminal error status must not wrap ErrNotReady")
		}
	})
}

func TestL7PolicyWaitUntilActiveDeletedStatusFails(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			policy := sampleL7PolicyDetail()
			policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})

		r := &L7PolicyResource{
			client:    client,
			projectID: l7PolicyTestProjectID,
		}

		_, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if err == nil || !strings.Contains(err.Error(), "deleted unexpectedly") {
			t.Fatalf("expected deleted failure, got %v", err)
		}
	})
}

func TestL7PolicyWaitUntilActiveFailsOnUnrecognizedStatus(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			calls++
			policy := sampleL7PolicyDetail()
			policy.ProvisioningStatus = "MIGRATING"
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})
		r := &L7PolicyResource{client: client, projectID: l7PolicyTestProjectID}

		_, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if err == nil || !strings.Contains(err.Error(), "unrecognized provisioning status") || calls != 1 {
			t.Fatalf("expected an unrecognized status to stop the wait at once, got calls=%d err=%v", calls, err)
		}
	})
}

func TestL7PolicyWaitUntilActiveTimesOutAtProductionTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			policy := sampleL7PolicyDetail()
			policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})
		r := &L7PolicyResource{client: client, projectID: l7PolicyTestProjectID}

		start := time.Now()
		_, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if !errors.Is(err, wait.ErrTimeout) {
			t.Fatalf("expected a timeout, got %v", err)
		}
		if waited := time.Since(start); waited != l7PolicyOperationTimeout {
			t.Fatalf("expected to wait %s, got %s", l7PolicyOperationTimeout, waited)
		}
	})
}

func TestL7PolicyWaitUntilActiveStopsOnCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			cancel()
			policy := sampleL7PolicyDetail()
			policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
		})
		r := &L7PolicyResource{client: client, projectID: l7PolicyTestProjectID}

		start := time.Now()
		_, err := r.waitUntilActive(ctx, l7PolicyTestID)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if waited := time.Since(start); waited >= l7PolicyOperationTimeout {
			t.Fatalf("expected the wait to stop before the timeout, waited %s", waited)
		}
	})
}

// UNKNOWN is as final as ERROR after a create or an update.
func TestL7PolicyWaitUntilActiveFailsOnStatusUnknown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		api := &l7PolicyTestAPI{policy: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN}}
		r := newL7PolicyTestResource(t, api)

		_, err := r.waitUntilActive(context.Background(), l7PolicyTestID)
		if err == nil || !strings.Contains(err.Error(), "terminal provisioning status UNKNOWN") || errors.Is(err, wait.ErrTimeout) {
			t.Fatalf("expected UNKNOWN to stop the wait, got %v", err)
		}
		if api.reads != 1 || time.Since(api.start) != 0 {
			t.Fatalf("reads=%d after %s, want one read at once", api.reads, time.Since(api.start))
		}
	})
}

func TestL7PolicyWaitUntilDeletedSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		client := l7PolicyTestClient(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				policy := sampleL7PolicyDetail()
				policy.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE
				return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(policy)), nil
			}
			return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		r := &L7PolicyResource{
			client:    client,
			projectID: l7PolicyTestProjectID,
		}

		start := time.Now()
		err := r.waitUntilDeleted(context.Background(), l7PolicyTestID)
		if err != nil {
			t.Fatalf("wait until deleted failed: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
		if waited := time.Since(start); waited != l7PolicyPollInterval {
			t.Fatalf("expected waited %s, got %s", l7PolicyPollInterval, waited)
		}
	})
}

// ERROR or UNKNOWN fails the wait only after PENDING_DELETE, because a
// DELETE can start from either state.
func TestL7PolicyWaitUntilDeletedTerminalStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status)+" after pending delete", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{policy: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, status}}
				r := newL7PolicyTestResource(t, api)

				err := r.waitUntilDeleted(context.Background(), l7PolicyTestID)
				if err == nil || !strings.Contains(err.Error(), "terminal provisioning status "+string(status)) || errors.Is(err, wait.ErrTimeout) {
					t.Fatalf("expected the %s status to fail the wait, got %v", status, err)
				}
				if time.Since(api.start) != l7PolicyPollInterval {
					t.Fatalf("waited %s, want %s", time.Since(api.start), l7PolicyPollInterval)
				}
			})
		})
		t.Run(string(status)+" before pending delete", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{policy: []loadbalancersdk.LoadBalancingProvisioningStatus{status, loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, l7PolicyTestGone}}
				r := newL7PolicyTestResource(t, api)

				if err := r.waitUntilDeleted(context.Background(), l7PolicyTestID); err != nil {
					t.Fatalf("wait until deleted: %v", err)
				}
				if time.Since(api.start) != 2*l7PolicyPollInterval {
					t.Fatalf("waited %s, want %s", time.Since(api.start), 2*l7PolicyPollInterval)
				}
			})
		})
	}
}

// An L7 policy in ERROR or UNKNOWN before the DELETE keeps that state until the
// infrastructure accepts the deletion. Without PENDING_DELETE, the wait cannot
// tell it from a failed deletion, so it goes on until timeout.
func TestL7PolicyWaitUntilDeletedWithoutPendingDeleteWaitsUntilTimeout(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{policy: []loadbalancersdk.LoadBalancingProvisioningStatus{status}}
				r := newL7PolicyTestResource(t, api)

				err := r.waitUntilDeleted(context.Background(), l7PolicyTestID)
				if !errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), "still deleting ("+string(status)+")") {
					t.Fatalf("expected a timeout that names %s, got %v", status, err)
				}
				if waited := time.Since(api.start); waited != l7PolicyOperationTimeout {
					t.Fatalf("waited %s, want %s", waited, l7PolicyOperationTimeout)
				}
			})
		})
	}
}

const (
	l7PolicyTestPending = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
	l7PolicyTestActive  = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
)

// The backend refuses a request that meets a held claim, and the provider
// does not send it again, so the request waits for the idle state.
func TestL7PolicyOperationsWaitForIdleBeforeRequest(t *testing.T) {
	t.Parallel()

	busy := []loadbalancersdk.LoadBalancingProvisioningStatus{l7PolicyTestPending, l7PolicyTestActive}
	for _, tt := range []struct {
		operation, object string
		api               l7PolicyTestAPI
	}{
		{"create", "listener", l7PolicyTestAPI{listener: busy}},
		{"create", "load balancer", l7PolicyTestAPI{loadBalancer: busy}},
		{"update", "policy", l7PolicyTestAPI{policy: busy}},
		{"update", "listener", l7PolicyTestAPI{listener: busy}},
		{"update", "load balancer", l7PolicyTestAPI{loadBalancer: busy}},
		{"delete", "policy", l7PolicyTestAPI{policy: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, l7PolicyTestActive}}},
		{"delete", "listener", l7PolicyTestAPI{listener: busy}},
		{"delete", "load balancer", l7PolicyTestAPI{loadBalancer: busy}},
	} {
		t.Run(tt.operation+"/"+tt.object, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := tt.api
				api.settled = l7PolicyTestSettled(tt.operation)
				r := newL7PolicyTestResource(t, &api)

				if diags := runL7PolicyTestOperation(t, r, tt.operation); diags.HasError() {
					t.Fatalf("%s: %v", tt.operation, diags)
				}
				if api.mutations() != 1 || api.mutationTimes[0] != l7PolicyPollInterval {
					t.Fatalf("mutations at %v, want one after %s", api.mutationTimes, l7PolicyPollInterval)
				}
			})
		})
	}
}

// A busy refusal after the idle wait fails the operation at once. The
// diagnostic names the claimed objects and includes the refusal.
func TestL7PolicyOperationsReturnABusyRefusal(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		operation, method, wantSummary, wantSubject string
	}{
		{"create", http.MethodPost, "Error creating L7 policy", fmt.Sprintf("the creation of L7 policy under listener %s of load balancer %s", l7PolicyTestListenerID, l7PolicyTestLoadBalancerID)},
		{"update", http.MethodPatch, "Error updating L7 policy", fmt.Sprintf("the update of L7 policy %s under listener %s of load balancer %s", l7PolicyTestID, l7PolicyTestListenerID, l7PolicyTestLoadBalancerID)},
		{"delete", http.MethodDelete, "Error deleting L7 policy", fmt.Sprintf("the deletion of L7 policy %s under listener %s of load balancer %s", l7PolicyTestID, l7PolicyTestListenerID, l7PolicyTestLoadBalancerID)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{
					// The idle wait finds the Load Balancer pending once, so
					// the one request follows a poll interval.
					loadBalancer: []loadbalancersdk.LoadBalancingProvisioningStatus{l7PolicyTestPending, l7PolicyTestActive},
					// A second request would succeed, so a resend cannot
					// pass unnoticed.
					answers: []l7PolicyTestAnswer{{http.StatusConflict, l7PolicyTestBusyBody}, {http.StatusOK, ""}},
				}
				r := newL7PolicyTestResource(t, api)

				diags := runL7PolicyTestOperation(t, r, tt.operation)
				if !diags.HasError() || diags.Errors()[0].Summary() != tt.wantSummary {
					t.Fatalf("diagnostics=%v, want %q", diags, tt.wantSummary)
				}
				detail := diags.Errors()[0].Detail()
				for _, want := range []string{tt.wantSubject + " was refused as busy", "L7 policy is busy"} {
					if !strings.Contains(detail, want) {
						t.Fatalf("detail=%q, want %q", detail, want)
					}
				}
				if api.mutations() != 1 || api.mutationTimes[0] != l7PolicyPollInterval {
					t.Fatalf("mutations at %v, want one after %s", api.mutationTimes, l7PolicyPollInterval)
				}
				if last := api.requests[len(api.requests)-1]; last != tt.method {
					t.Fatalf("requests %v, want none after the refused %s", api.requests, tt.method)
				}
			})
		})
	}
}

// Only an HTTP 409 with code resource_busy is a busy refusal. Every other
// error, a 409 with code conflict included, fails the operation unchanged on
// the one request.
func TestL7PolicyOperationsReturnOtherErrorsUnchanged(t *testing.T) {
	t.Parallel()

	for _, answer := range []struct {
		name string
		l7PolicyTestAnswer
	}{
		{"409 conflict", l7PolicyTestAnswer{http.StatusConflict, l7PolicyTestConflictBody}},
		{"400 with resource_busy", l7PolicyTestAnswer{http.StatusBadRequest, l7PolicyTestBusyBody}},
		{"500 with resource_busy", l7PolicyTestAnswer{http.StatusInternalServerError, l7PolicyTestBusyBody}},
		{"503", l7PolicyTestAnswer{http.StatusServiceUnavailable, `{"detail":"unavailable"}`}},
	} {
		for _, operation := range []string{"create", "update", "delete"} {
			t.Run(answer.name+"/"+operation, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					api := &l7PolicyTestAPI{answers: []l7PolicyTestAnswer{answer.l7PolicyTestAnswer}}
					r := newL7PolicyTestResource(t, api)

					diags := runL7PolicyTestOperation(t, r, operation)
					if !diags.HasError() {
						t.Fatalf("%s succeeded, want the error of the request", operation)
					}
					if detail := diags.Errors()[0].Detail(); strings.Contains(detail, "refused as busy") {
						t.Fatalf("detail=%q, want the error unchanged", detail)
					}
					if api.mutations() != 1 || api.mutationTimes[0] != 0 {
						t.Fatalf("mutations at %v, want one at once", api.mutationTimes)
					}
				})
			})
		}
	}
}

// The idle wait stops at once on a state that the backend refuses: ERROR or
// UNKNOWN of a parent, or of the policy for an update, and a gone parent.
// UNKNOWN is as final as ERROR. A delete counts a policy in ERROR or UNKNOWN
// as idle.
func TestL7PolicyIdleWaitStopsOnTerminalStatus(t *testing.T) {
	t.Parallel()

	statuses := func(s loadbalancersdk.LoadBalancingProvisioningStatus) []loadbalancersdk.LoadBalancingProvisioningStatus {
		return []loadbalancersdk.LoadBalancingProvisioningStatus{s}
	}
	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
		l7PolicyTestGone,
		"MIGRATING",
	} {
		for _, tt := range []struct {
			object, operation string
			api               l7PolicyTestAPI
		}{
			{"listener", "create", l7PolicyTestAPI{listener: statuses(status)}},
			{"listener", "update", l7PolicyTestAPI{listener: statuses(status)}},
			{"listener", "delete", l7PolicyTestAPI{listener: statuses(status)}},
			{"load balancer", "create", l7PolicyTestAPI{loadBalancer: statuses(status)}},
			{"load balancer", "update", l7PolicyTestAPI{loadBalancer: statuses(status)}},
			{"load balancer", "delete", l7PolicyTestAPI{loadBalancer: statuses(status)}},
			{"policy", "update", l7PolicyTestAPI{policy: statuses(status)}},
		} {
			t.Run(fmt.Sprintf("%s %s/%s", tt.object, status, tt.operation), func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					api := tt.api
					r := newL7PolicyTestResource(t, &api)

					diags := runL7PolicyTestOperation(t, r, tt.operation)
					if !diags.HasError() {
						t.Fatalf("%s succeeded, want the idle wait to stop", tt.operation)
					}
					if api.mutations() != 0 || time.Since(api.start) != 0 {
						t.Fatalf("mutations=%d after %s, want none at once", api.mutations(), time.Since(api.start))
					}
				})
			})
		}
	}

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(fmt.Sprintf("policy %s/delete", status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{policy: statuses(status), settled: l7PolicyTestSettled("delete")}
				r := newL7PolicyTestResource(t, api)

				if diags := runL7PolicyTestOperation(t, r, "delete"); diags.HasError() {
					t.Fatalf("delete: %v", diags)
				}
				if api.mutations() != 1 || api.mutationTimes[0] != 0 {
					t.Fatalf("mutations at %v, want one at once", api.mutationTimes)
				}
			})
		})
	}
}

func TestL7PolicyIdleWaitTimesOutAtProductionTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		api := &l7PolicyTestAPI{loadBalancer: []loadbalancersdk.LoadBalancingProvisioningStatus{l7PolicyTestPending}}
		r := newL7PolicyTestResource(t, api)

		diags := runL7PolicyTestOperation(t, r, "create")
		if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), wait.ErrTimeout.Error()) {
			t.Fatalf("diagnostics=%v, want a timeout", diags)
		}
		if api.mutations() != 0 || time.Since(api.start) != l7PolicyOperationTimeout {
			t.Fatalf("mutations=%d after %s, want none after %s", api.mutations(), time.Since(api.start), l7PolicyOperationTimeout)
		}
	})
}

// Terraform runs sibling policies of one Load Balancer in parallel, so
// without the lock they would refuse each other inside one apply.
func TestL7PolicyOperationsHoldTheClaimsLock(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7PolicyTestAPI{settled: l7PolicyTestSettled(operation)}
				r := newL7PolicyTestResource(t, api)
				unlock, err := r.claims.Lock(context.Background(), l7PolicyTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("lock: %v", err)
				}

				done := make(chan diag.Diagnostics)
				go func() { done <- runL7PolicyTestOperation(t, r, operation) }()
				synctest.Wait()
				if len(api.requests) != 0 {
					t.Fatalf("requests %v while another operation holds the lock", api.requests)
				}

				unlock()
				if diags := <-done; diags.HasError() {
					t.Fatalf("%s: %v", operation, diags)
				}
				if api.mutations() != 1 {
					t.Fatalf("mutations=%d, want 1", api.mutations())
				}

				// The operation released the lock when it returned.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				release, err := r.claims.Lock(ctx, l7PolicyTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("lock after %s: %v", operation, err)
				}
				release()
			})
		})
	}
}

func TestBuildL7PolicyCreateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plan      L7PolicyResourceModel
		wantError string
		checkBody func(*testing.T, loadbalancersdk.L7PolicyCreateSchema)
	}{
		{
			name: "valid reject",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:        types.StringValue("reject-policy"),
					Action:      types.StringValue("REJECT"),
					Description: types.StringValue("reject all"),
					Position:    types.Int64Value(1),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			checkBody: func(t *testing.T, body loadbalancersdk.L7PolicyCreateSchema) {
				t.Helper()
				if body.ListenerId != l7PolicyTestListenerID || *body.Name != "reject-policy" ||
					body.Description == nil || *body.Description != "reject all" ||
					!l7PolicyNullableEquals(body.Position, 1) {
					t.Fatalf("unexpected body: %#v", body)
				}
				reject, err := body.Redirect.AsL7PolicyRedirectReject()
				if err != nil {
					t.Fatalf("redirect not reject: %v", err)
				}
				_ = reject
			},
		},
		{
			name: "valid redirect to server group",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("pool-policy"),
					Action: types.StringValue("REDIRECT_TO_SERVER_GROUP"),
				},
				Listener:            reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
				RedirectServerGroup: reference.Value(reference.ID, types.StringValue(l7PolicyTestServerGroupID.String())),
			},
			checkBody: func(t *testing.T, body loadbalancersdk.L7PolicyCreateSchema) {
				t.Helper()
				sg, err := body.Redirect.AsL7PolicyRedirectServerGroup()
				if err != nil {
					t.Fatalf("redirect not server group: %v", err)
				}
				if sg.RedirectServerGroupId != l7PolicyTestServerGroupID {
					t.Fatalf("expected server group %s, got %s", l7PolicyTestServerGroupID, sg.RedirectServerGroupId)
				}
			},
		},
		{
			name: "valid redirect to pool alias",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("pool-policy"),
					Action: types.StringValue("REDIRECT_TO_POOL"),
				},
				Listener:            reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
				RedirectServerGroup: reference.Value(reference.ID, types.StringValue(l7PolicyTestServerGroupID.String())),
			},
			checkBody: func(t *testing.T, body loadbalancersdk.L7PolicyCreateSchema) {
				t.Helper()
				sg, err := body.Redirect.AsL7PolicyRedirectServerGroup()
				if err != nil {
					t.Fatalf("redirect not server group: %v", err)
				}
				if sg.RedirectServerGroupId != l7PolicyTestServerGroupID {
					t.Fatalf("expected server group %s, got %s", l7PolicyTestServerGroupID, sg.RedirectServerGroupId)
				}
			},
		},
		{
			name: "valid redirect to URL",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:             types.StringValue("url-policy"),
					Action:           types.StringValue("REDIRECT_TO_URL"),
					RedirectURL:      types.StringValue("https://example.com/target"),
					RedirectHTTPCode: types.Int64Value(307),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			checkBody: func(t *testing.T, body loadbalancersdk.L7PolicyCreateSchema) {
				t.Helper()
				u, err := body.Redirect.AsL7PolicyRedirectURL()
				if err != nil {
					t.Fatalf("redirect not url: %v", err)
				}
				if u.RedirectUrl != "https://example.com/target" || u.RedirectHttpCode != 307 {
					t.Fatalf("unexpected url redirect: %#v", u)
				}
			},
		},
		{
			name: "valid redirect prefix with default 302 code",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:           types.StringValue("prefix-policy"),
					Action:         types.StringValue("REDIRECT_PREFIX"),
					RedirectPrefix: types.StringValue("https://example.com"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			checkBody: func(t *testing.T, body loadbalancersdk.L7PolicyCreateSchema) {
				t.Helper()
				p, err := body.Redirect.AsL7PolicyRedirectPrefix()
				if err != nil {
					t.Fatalf("redirect not prefix: %v", err)
				}
				if p.RedirectPrefix != "https://example.com" || p.RedirectHttpCode != 302 {
					t.Fatalf("unexpected prefix redirect: %#v", p)
				}
			},
		},
		{
			name: "missing listener_id",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("policy"),
					Action: types.StringValue("REJECT"),
				},
			},
			wantError: "Missing",
		},
		{
			name: "missing name",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Action: types.StringValue("REJECT"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Missing L7 policy name",
		},
		{
			name: "missing action",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name: types.StringValue("policy"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Missing L7 policy action",
		},
		{
			name: "invalid action",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("policy"),
					Action: types.StringValue("UNKNOWN_ACTION"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Invalid L7 policy action",
		},
		{
			name: "missing server group id for REDIRECT_TO_SERVER_GROUP",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("policy"),
					Action: types.StringValue("REDIRECT_TO_SERVER_GROUP"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Missing redirect server group",
		},
		{
			name: "missing url for REDIRECT_TO_URL",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("policy"),
					Action: types.StringValue("REDIRECT_TO_URL"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Missing redirect URL",
		},
		{
			name: "missing prefix for REDIRECT_PREFIX",
			plan: L7PolicyResourceModel{
				L7PolicyModel: L7PolicyModel{
					Name:   types.StringValue("policy"),
					Action: types.StringValue("REDIRECT_PREFIX"),
				},
				Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
			},
			wantError: "Missing redirect prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, diags := buildL7PolicyCreateBody(tt.plan)
			if tt.wantError != "" {
				if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %v", tt.wantError, diags)
				}
				return
			}
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			if tt.checkBody != nil {
				tt.checkBody(t, body)
			}
		})
	}
}

// The API trims these strings, so the request carries the trimmed values.
func TestBuildL7PolicyCreateBodyTrimsStrings(t *testing.T) {
	t.Parallel()

	listener := reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String()))
	urlBody, diags := buildL7PolicyCreateBody(L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			Name:        types.StringValue(" web "),
			Description: types.StringValue("\tPadded description "),
			Action:      types.StringValue("REDIRECT_TO_URL"),
			RedirectURL: types.StringValue(" https://example.com/maintenance\n"),
		},
		Listener: listener,
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if *urlBody.Name != "web" || urlBody.Description == nil || *urlBody.Description != "Padded description" {
		t.Fatalf("name=%q description=%v, want trimmed values", *urlBody.Name, urlBody.Description)
	}
	redirectURL, err := urlBody.Redirect.AsL7PolicyRedirectURL()
	if err != nil || redirectURL.RedirectUrl != "https://example.com/maintenance" {
		t.Fatalf("redirect URL = %#v, err: %v", redirectURL, err)
	}

	prefixBody, diags := buildL7PolicyCreateBody(L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			Name:           types.StringValue("prefix"),
			Action:         types.StringValue("REDIRECT_PREFIX"),
			RedirectPrefix: types.StringValue(" https://example.com "),
		},
		Listener: listener,
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	prefix, err := prefixBody.Redirect.AsL7PolicyRedirectPrefix()
	if err != nil || prefix.RedirectPrefix != "https://example.com" {
		t.Fatalf("redirect prefix = %#v, err: %v", prefix, err)
	}
}

func TestBuildL7PolicyUpdateBody(t *testing.T) {
	t.Parallel()

	state := L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			ID:             types.StringValue(l7PolicyTestID.String()),
			Name:           types.StringValue("initial-name"),
			Description:    types.StringValue("initial-desc"),
			Action:         types.StringValue("REJECT"),
			Position:       types.Int64Value(1),
			RedirectPrefix: types.StringNull(),
		},
		Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
	}

	// 1. No changes
	plan := state
	body, changed, diags := buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || changed {
		t.Fatalf("expected no changes, got changed=%v diags=%v", changed, diags)
	}

	// 2. Name changed only
	plan = state
	plan.Name = types.StringValue("new-name")
	body, changed, diags = buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("expected changes, got changed=%v diags=%v", changed, diags)
	}
	if !l7PolicyNullableEquals(body.Name, "new-name") || body.Description.IsSpecified() || body.Position != nil || body.Redirect != nil {
		t.Fatalf("expected only name changed, got %#v", body)
	}

	// 3. Description cleared with empty string
	plan = state
	plan.Description = types.StringValue("")
	body, changed, diags = buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("expected changes, got changed=%v diags=%v", changed, diags)
	}
	if !l7PolicyNullableEquals(body.Description, "") {
		t.Fatalf("expected description cleared with empty string, got %#v", body.Description)
	}

	// 4. Description omitted (null) preserves backend value (no change)
	plan = state
	plan.Description = types.StringNull()
	body, changed, diags = buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || changed {
		t.Fatalf("expected no changes when description is null, got changed=%v diags=%v", changed, diags)
	}

	// 5. Position changed only
	plan = state
	plan.Position = types.Int64Value(5)
	body, changed, diags = buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("expected changes, got changed=%v diags=%v", changed, diags)
	}
	if body.Position == nil || *body.Position != 5 || body.Name.IsSpecified() || body.Description.IsSpecified() || body.Redirect != nil {
		t.Fatalf("expected only position changed, got %#v", body)
	}

	// 6. Action/Redirect changed
	plan = state
	plan.Action = types.StringValue("REDIRECT_PREFIX")
	plan.RedirectPrefix = types.StringValue("https://example.com")
	plan.RedirectHTTPCode = types.Int64Value(301)
	body, changed, diags = buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("expected changes, got changed=%v diags=%v", changed, diags)
	}
	if body.Redirect == nil {
		t.Fatal("expected redirect in update body")
	}
	prefix, err := body.Redirect.AsL7PolicyRedirectPrefix()
	if err != nil || prefix.RedirectPrefix != "https://example.com" || prefix.RedirectHttpCode != 301 {
		t.Fatalf("unexpected redirect prefix: %#v, err: %v", prefix, err)
	}
}

// The API trims these strings, so a change of outer whitespace alone sends no
// field, and a real change sends the trimmed value.
func TestBuildL7PolicyUpdateBodyComparesTrimmedStrings(t *testing.T) {
	t.Parallel()

	state := L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			Name:        types.StringValue("web"),
			Description: types.StringValue("desc"),
			Action:      types.StringValue("REDIRECT_TO_URL"),
			RedirectURL: types.StringValue("https://example.com"),
		},
		Listener: reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
	}

	plan := state
	plan.Name = types.StringValue(" web ")
	plan.Description = types.StringValue("desc\n")
	plan.RedirectURL = types.StringValue(" https://example.com ")
	_, changed, diags := buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || changed {
		t.Fatalf("outer whitespace alone: changed=%v diags=%v, want no update", changed, diags)
	}

	plan.Name = types.StringValue(" api ")
	plan.Description = types.StringValue(" new desc ")
	plan.RedirectURL = types.StringValue(" https://example.org ")
	body, changed, diags := buildL7PolicyUpdateBody(plan, state)
	if diags.HasError() || !changed {
		t.Fatalf("real change: changed=%v diags=%v", changed, diags)
	}
	if !l7PolicyNullableEquals(body.Name, "api") || !l7PolicyNullableEquals(body.Description, "new desc") || body.Redirect == nil {
		t.Fatalf("unexpected body: %#v", body)
	}
	redirectURL, err := body.Redirect.AsL7PolicyRedirectURL()
	if err != nil || redirectURL.RedirectUrl != "https://example.org" {
		t.Fatalf("redirect URL = %#v, err: %v", redirectURL, err)
	}

	prefixState := state
	prefixState.Action = types.StringValue("REDIRECT_PREFIX")
	prefixState.RedirectURL = types.StringNull()
	prefixState.RedirectPrefix = types.StringValue("https://example.com")
	prefixPlan := prefixState
	prefixPlan.RedirectPrefix = types.StringValue("https://example.com ")
	if _, changed, diags := buildL7PolicyUpdateBody(prefixPlan, prefixState); diags.HasError() || changed {
		t.Fatalf("redirect_prefix outer whitespace: changed=%v diags=%v, want no update", changed, diags)
	}
}

func TestBuildL7PolicyUpdateBodySerializesEveryActionTransition(t *testing.T) {
	t.Parallel()

	variants := map[string]func(*L7PolicyResourceModel){
		"REJECT": func(*L7PolicyResourceModel) {},
		"REDIRECT_TO_SERVER_GROUP": func(m *L7PolicyResourceModel) {
			m.RedirectServerGroup = reference.Value(reference.ID, types.StringValue(l7PolicyTestServerGroupID.String()))
		},
		"REDIRECT_TO_URL": func(m *L7PolicyResourceModel) {
			m.RedirectURL = types.StringValue("https://example.com/maintenance")
			m.RedirectHTTPCode = types.Int64Value(307)
		},
		"REDIRECT_PREFIX": func(m *L7PolicyResourceModel) {
			m.RedirectPrefix = types.StringValue("https://example.com")
			m.RedirectHTTPCode = types.Int64Value(308)
		},
	}
	wantTargets := map[string]map[string]any{
		"REJECT":                   {},
		"REDIRECT_TO_SERVER_GROUP": {"redirect_server_group_id": l7PolicyTestServerGroupID.String()},
		"REDIRECT_TO_URL":          {"redirect_url": "https://example.com/maintenance", "redirect_http_code": float64(307)},
		"REDIRECT_PREFIX":          {"redirect_prefix": "https://example.com", "redirect_http_code": float64(308)},
	}
	for from, configureFrom := range variants {
		for to, configureTo := range variants {
			if from == to {
				continue
			}
			t.Run(from+" to "+to, func(t *testing.T) {
				t.Parallel()

				state := l7PolicyTestModel(from)
				configureFrom(&state)
				plan := l7PolicyTestModel(to)
				configureTo(&plan)

				body, changed, diags := buildL7PolicyUpdateBody(plan, state)
				if diags.HasError() || !changed || body.Redirect == nil {
					t.Fatalf("expected a redirect update, got changed=%v diags=%v", changed, diags)
				}
				encoded, err := json.Marshal(body.Redirect)
				if err != nil {
					t.Fatalf("marshal redirect: %v", err)
				}
				var got map[string]any
				if err := json.Unmarshal(encoded, &got); err != nil {
					t.Fatalf("unmarshal redirect: %v", err)
				}
				if got["action"] != to {
					t.Fatalf("expected discriminator %s, got %s", to, encoded)
				}
				delete(got, "action")
				if len(got) != len(wantTargets[to]) {
					t.Fatalf("expected only the targets of %s, got %s", to, encoded)
				}
				for key, want := range wantTargets[to] {
					if got[key] != want {
						t.Fatalf("expected %s=%v, got %s", key, want, encoded)
					}
				}
			})
		}
	}
}

func TestL7PolicyRedirectHTTPCodePlan(t *testing.T) {
	t.Parallel()

	withCode := func(action string, code types.Int64) *L7PolicyResourceModel {
		m := l7PolicyTestModel(action)
		m.RedirectHTTPCode = code
		return &m
	}
	tests := []struct {
		name       string
		config     types.Int64
		planned    *L7PolicyResourceModel
		prior      *L7PolicyResourceModel
		priorValue types.Int64
		want       types.Int64
	}{
		{name: "configured code is kept", config: types.Int64Value(301), planned: withCode("REDIRECT_TO_URL", types.Int64Value(301)), want: types.Int64Value(301)},
		{name: "create url uses 302", config: types.Int64Null(), planned: withCode("REDIRECT_TO_URL", types.Int64Unknown()), want: types.Int64Value(302)},
		{name: "url keeps code in effect", config: types.Int64Null(), planned: withCode("REDIRECT_TO_URL", types.Int64Unknown()), prior: withCode("REDIRECT_TO_URL", types.Int64Value(301)), priorValue: types.Int64Value(301), want: types.Int64Value(301)},
		{name: "prefix to url keeps code in effect", config: types.Int64Null(), planned: withCode("REDIRECT_TO_URL", types.Int64Unknown()), prior: withCode("REDIRECT_PREFIX", types.Int64Value(308)), priorValue: types.Int64Value(308), want: types.Int64Value(308)},
		{name: "server group to prefix uses 302", config: types.Int64Null(), planned: withCode("REDIRECT_PREFIX", types.Int64Unknown()), prior: withCode("REDIRECT_TO_SERVER_GROUP", types.Int64Null()), priorValue: types.Int64Null(), want: types.Int64Value(302)},
		{name: "url to reject plans null", config: types.Int64Null(), planned: withCode("REJECT", types.Int64Unknown()), prior: withCode("REDIRECT_TO_URL", types.Int64Value(301)), priorValue: types.Int64Value(301), want: types.Int64Null()},
		{name: "url to pool alias plans null", config: types.Int64Null(), planned: withCode("REDIRECT_TO_POOL", types.Int64Unknown()), prior: withCode("REDIRECT_TO_URL", types.Int64Value(301)), priorValue: types.Int64Value(301), want: types.Int64Null()},
		{name: "unknown action stays unknown", config: types.Int64Null(), planned: func() *L7PolicyResourceModel {
			m := withCode("REJECT", types.Int64Unknown())
			m.Action = types.StringUnknown()
			return m
		}(), want: types.Int64Unknown()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := planmodifier.Int64Request{
				Path:        path.Root("redirect_http_code"),
				ConfigValue: tt.config,
				Plan:        l7PolicyTestPlan(t, tt.planned),
				PlanValue:   tt.planned.RedirectHTTPCode,
				State:       tfsdk.State(l7PolicyTestPlan(t, tt.prior)),
				StateValue:  types.Int64Null(),
			}
			if tt.prior != nil {
				req.StateValue = tt.priorValue
			}
			resp := &planmodifier.Int64Response{PlanValue: req.PlanValue}
			l7PolicyRedirectHTTPCodePlan{}.PlanModifyInt64(context.Background(), req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modifier: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tt.want) {
				t.Fatalf("expected %s, got %s", tt.want, resp.PlanValue)
			}
		})
	}
}

func l7PolicyTestSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	(&L7PolicyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// l7PolicyTestPlan encodes model as a plan of the resource schema. A null
// model encodes a null plan, as for a destroy.
func l7PolicyTestPlan(t *testing.T, model *L7PolicyResourceModel) tfsdk.Plan {
	t.Helper()
	sch := l7PolicyTestSchema(t)
	plan := tfsdk.Plan{Schema: sch, Raw: tftypes.NewValue(sch.Type().TerraformType(context.Background()), nil)}
	if model != nil {
		if diags := plan.Set(context.Background(), model); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
	}
	return plan
}

func l7PolicyTestModel(action string) L7PolicyResourceModel {
	return L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			ID:     types.StringUnknown(),
			Name:   types.StringValue("example-policy"),
			Action: types.StringValue(action),
		},
		Listener:            reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
		RedirectServerGroup: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}
}

// l7PolicyTestLoadBalancerID is the root Load Balancer of listener
// l7PolicyTestListenerID in l7PolicyTestAPI.
var l7PolicyTestLoadBalancerID = core.UUID{7}

// l7PolicyTestGone marks a read of an object that the backend no longer has.
const l7PolicyTestGone loadbalancersdk.LoadBalancingProvisioningStatus = "gone"

const (
	l7PolicyTestBusyBody     = `{"errors":[{"location":"","code":"resource_busy","message":"L7 policy is busy, please try again later."}],"request_id":"r1"}`
	l7PolicyTestConflictBody = `{"errors":[{"location":"","code":"conflict","message":"The last operation on L7 policy failed."}],"request_id":"r2"}`
)

// l7PolicyTestAnswer is the scripted answer to one mutation request.
type l7PolicyTestAnswer struct {
	status int
	body   string
}

// l7PolicyTestAPI serves the test policy, listener l7PolicyTestListenerID,
// and its Load Balancer l7PolicyTestLoadBalancerID. Each status list gives
// the status of one read in turn and repeats its last entry; an empty list
// reads ACTIVE. policy applies until a mutation succeeds, and settled after
// it. answers works the same for the mutations, and an empty list accepts
// every mutation.
type l7PolicyTestAPI struct {
	t                                       *testing.T
	listener, loadBalancer, policy, settled []loadbalancersdk.LoadBalancingProvisioningStatus
	answers                                 []l7PolicyTestAnswer
	// noListener leaves the listener out of the listener list, as after its
	// deletion.
	noListener bool

	start                                   time.Time
	accepted                                bool
	listenerReads, loadBalancerReads, reads int
	settledReads                            int
	requests                                []string
	mutationTimes                           []time.Duration
}

func (a *l7PolicyTestAPI) roundTrip(req *http.Request) (*http.Response, error) {
	if resp, ok := a.serveParents(req); ok {
		return resp, nil
	}
	policy := "/v2/load-balancing/l7policies/" + l7PolicyTestID.String() + "/"
	if req.Method == http.MethodGet && req.URL.Path == policy {
		a.requests = append(a.requests, "GET policy")
		status := l7PolicyTestStatus(a.policy, &a.reads)
		if a.accepted {
			status = l7PolicyTestStatus(a.settled, &a.settledReads)
		}
		if status == l7PolicyTestGone {
			return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		detail := sampleL7PolicyDetail()
		detail.ProvisioningStatus = status
		return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicyDetailJSON(detail)), nil
	}

	if (req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/l7policies/") ||
		((req.Method == http.MethodPatch || req.Method == http.MethodDelete) && req.URL.Path == policy) {
		a.requests = append(a.requests, req.Method)
		a.mutationTimes = append(a.mutationTimes, time.Since(a.start))
		if n := len(a.mutationTimes); len(a.answers) > 0 {
			answer := a.answers[min(n, len(a.answers))-1]
			if answer.status >= http.StatusBadRequest {
				return l7PolicyTestHTTPResponse(req, answer.status, answer.body), nil
			}
		}
		a.accepted = true
		switch req.Method {
		case http.MethodPost:
			return l7PolicyTestHTTPResponse(req, http.StatusCreated, l7PolicySchemaJSON(sampleL7PolicySchema())), nil
		case http.MethodPatch:
			return l7PolicyTestHTTPResponse(req, http.StatusOK, l7PolicySchemaJSON(sampleL7PolicySchema())), nil
		default:
			return l7PolicyTestHTTPResponse(req, http.StatusAccepted, ""), nil
		}
	}

	a.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
	return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
}

// serveParents answers the reads of listener l7PolicyTestListenerID and of
// its Load Balancer, including the lists that find the Load Balancer.
func (a *l7PolicyTestAPI) serveParents(req *http.Request) (*http.Response, bool) {
	const lbs = "/v2/load-balancing/loadbalancers/"
	lb := lbs + l7PolicyTestLoadBalancerID.String() + "/"
	listener := lb + "listeners/" + l7PolicyTestListenerID.String() + "/"
	if req.Method == http.MethodGet {
		switch req.URL.Path {
		case lbs:
			return l7PolicyTestHTTPResponse(req, http.StatusOK, fmt.Sprintf(`{"count":1,"results":[{"id":%q,"provisioning_status":"ACTIVE"}]}`, l7PolicyTestLoadBalancerID)), true
		case lb + "listeners/":
			if a.noListener {
				return l7PolicyTestHTTPResponse(req, http.StatusOK, `{"count":0,"results":[]}`), true
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, fmt.Sprintf(`{"count":1,"results":[{"id":%q,"provisioning_status":"ACTIVE"}]}`, l7PolicyTestListenerID)), true
		case listener:
			a.requests = append(a.requests, "GET listener")
			status := l7PolicyTestStatus(a.listener, &a.listenerReads)
			if status == l7PolicyTestGone {
				return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), true
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, fmt.Sprintf(`{"id":%q,"provisioning_status":%q,"load_balancer":{"id":%q,"name":"lb"}}`, l7PolicyTestListenerID, status, l7PolicyTestLoadBalancerID)), true
		case lb:
			a.requests = append(a.requests, "GET load balancer")
			status := l7PolicyTestStatus(a.loadBalancer, &a.loadBalancerReads)
			if status == l7PolicyTestGone {
				return l7PolicyTestHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), true
			}
			return l7PolicyTestHTTPResponse(req, http.StatusOK, loadBalancerJSON(a.t, &loadbalancersdk.LoadBalancerDetailSchema{Id: l7PolicyTestLoadBalancerID, ProvisioningStatus: status})), true
		}
	}
	return nil, false
}

// mutations returns the number of mutation requests that the API received.
func (a *l7PolicyTestAPI) mutations() int {
	return len(a.mutationTimes)
}

func l7PolicyTestStatus(statuses []loadbalancersdk.LoadBalancingProvisioningStatus, reads *int) loadbalancersdk.LoadBalancingProvisioningStatus {
	*reads++
	if len(statuses) == 0 {
		return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	}
	return statuses[min(*reads, len(statuses))-1]
}

// newL7PolicyTestResource returns a resource that talks to api. Call it in
// the synctest bubble of the test, because the claims lock uses channels.
func newL7PolicyTestResource(t *testing.T, api *l7PolicyTestAPI) *L7PolicyResource {
	t.Helper()
	api.t = t
	api.start = time.Now()
	return &L7PolicyResource{client: l7PolicyTestClient(t, api.roundTrip), projectID: l7PolicyTestProjectID, claims: keylock.New()}
}

// runL7PolicyTestOperation runs operation ("create", "update", or "delete")
// on a REJECT policy and returns its diagnostics.
func runL7PolicyTestOperation(t *testing.T, r *L7PolicyResource, operation string) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	model := l7PolicyTestModel("REJECT")
	switch operation {
	case "create":
		resp := &resource.CreateResponse{State: tfsdk.State{Schema: l7PolicyTestSchema(t)}}
		r.Create(ctx, resource.CreateRequest{Plan: l7PolicyTestPlan(t, &model)}, resp)
		return resp.Diagnostics
	case "update":
		model.ID = types.StringValue(l7PolicyTestID.String())
		state := tfsdk.State(l7PolicyTestPlan(t, &model))
		planModel := model
		planModel.Name = types.StringValue("renamed-policy")
		resp := &resource.UpdateResponse{State: state}
		r.Update(ctx, resource.UpdateRequest{Plan: l7PolicyTestPlan(t, &planModel), State: state}, resp)
		return resp.Diagnostics
	case "delete":
		model.ID = types.StringValue(l7PolicyTestID.String())
		state := tfsdk.State(l7PolicyTestPlan(t, &model))
		resp := &resource.DeleteResponse{State: state}
		r.Delete(ctx, resource.DeleteRequest{State: state}, resp)
		return resp.Diagnostics
	}
	t.Fatalf("unknown operation %q", operation)
	return nil
}

// l7PolicyTestSettled is the policy status after an accepted mutation of
// operation: ACTIVE after a create or an update, and gone after a delete.
func l7PolicyTestSettled(operation string) []loadbalancersdk.LoadBalancingProvisioningStatus {
	if operation == "delete" {
		return []loadbalancersdk.LoadBalancingProvisioningStatus{l7PolicyTestGone}
	}
	return nil
}
