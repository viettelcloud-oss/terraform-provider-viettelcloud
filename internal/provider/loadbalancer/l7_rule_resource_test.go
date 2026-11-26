package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

func l7RuleResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&L7RuleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	return response.Schema
}

func sampleL7RuleSchemaJSON(id, policyID core.UUID, ruleType, compareType, value string, key *string, invert bool, provStatus, opStatus string) string {
	keyJSON := "null"
	if key != nil {
		keyJSON = fmt.Sprintf("%q", *key)
	}
	return fmt.Sprintf(`{
		"id": %q,
		"l7policy_id": %q,
		"type": %q,
		"compare_type": %q,
		"value": %q,
		"key": %s,
		"invert": %t,
		"provisioning_status": %q,
		"status": %q,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`, id, policyID, ruleType, compareType, value, keyJSON, invert, provStatus, opStatus)
}

var (
	l7RuleTestListenerID     = core.UUID{3}
	l7RuleTestLoadBalancerID = core.UUID{4}
)

// l7RuleTestParentGone makes the fake API answer a parent GET with HTTP 404.
const l7RuleTestParentGone loadbalancersdk.LoadBalancingProvisioningStatus = "gone"

// l7RuleTestParentStatus returns the provisioning status that the fake API
// reports on a GET of a parent of an L7 rule. kind is "L7 policy",
// "listener", or "load balancer".
type l7RuleTestParentStatus func(kind string) loadbalancersdk.LoadBalancingProvisioningStatus

func l7RuleActiveParents(string) loadbalancersdk.LoadBalancingProvisioningStatus {
	return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
}

func newL7RuleTestResource(t *testing.T, fn l7RuleRoundTripFunc) *L7RuleResource {
	t.Helper()
	return newL7RuleTestResourceWithParents(t, l7RuleActiveParents, fn)
}

// newL7RuleTestResourceWithParents returns an L7RuleResource whose fake API
// answers the parent requests of an operation with status, and passes every
// other request to fn.
func newL7RuleTestResourceWithParents(t *testing.T, status l7RuleTestParentStatus, fn l7RuleRoundTripFunc) *L7RuleResource {
	t.Helper()
	r := NewL7RuleResource().(*L7RuleResource)
	r.client = newL7RuleTestClient(t, func(req *http.Request) (*http.Response, error) {
		if resp := l7RuleTestParentResponse(t, req, status); resp != nil {
			return resp, nil
		}
		return fn(req)
	})
	r.projectID = l7RuleTestProjectID
	r.claims = keylock.New()
	return r
}

// l7RuleTestParentResponse answers a GET of the L7 policy, the Load Balancer
// and listener listings, the listener, or the Load Balancer of an L7 rule. It
// returns nil for every other request.
func l7RuleTestParentResponse(t *testing.T, req *http.Request, status l7RuleTestParentStatus) *http.Response {
	t.Helper()
	if req.Method != http.MethodGet {
		return nil
	}
	loadBalancerPath := fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/", l7RuleTestLoadBalancerID)
	var parentStatus loadbalancersdk.LoadBalancingProvisioningStatus
	var body any
	switch req.URL.Path {
	case fmt.Sprintf("/v2/load-balancing/l7policies/%s/", l7RuleTestPolicyID):
		parentStatus = status("L7 policy")
		body = loadbalancersdk.L7PolicyDetailSchema{Id: l7RuleTestPolicyID, ListenerId: l7RuleTestListenerID, ProvisioningStatus: parentStatus}
	case "/v2/load-balancing/loadbalancers/":
		body = loadbalancersdk.PagedLoadBalancerSchema{Count: 1, Results: []loadbalancersdk.LoadBalancerSchema{{Id: l7RuleTestLoadBalancerID}}}
	case loadBalancerPath + "listeners/":
		body = loadbalancersdk.PagedListenerSchema{Count: 1, Results: []loadbalancersdk.ListenerSchema{{Id: l7RuleTestListenerID}}}
	case fmt.Sprintf("%slisteners/%s/", loadBalancerPath, l7RuleTestListenerID):
		parentStatus = status("listener")
		body = loadbalancersdk.ListenerDetailSchema{Id: l7RuleTestListenerID, ProvisioningStatus: parentStatus}
	case loadBalancerPath:
		parentStatus = status("load balancer")
		body = loadbalancersdk.LoadBalancerDetailSchema{Id: l7RuleTestLoadBalancerID, ProvisioningStatus: parentStatus}
	default:
		return nil
	}
	if parentStatus == l7RuleTestParentGone {
		return l7RuleTestHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"Not found."}]}`)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", req.URL.Path, err)
	}
	return l7RuleTestHTTPResponse(req, http.StatusOK, string(encoded))
}

// l7RuleTestRefusal is the error body of a refusal with one error detail.
func l7RuleTestRefusal(code string) string {
	return fmt.Sprintf(`{"errors":[{"location":"","code":%q,"message":"load balancer is busy, please try again later."}],"request_id":"f1dbd828b10848f2b05d2a1795d3cc9a"}`, code)
}

func l7RuleJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestL7RuleModelMatchesSchema(t *testing.T) {
	t.Parallel()

	s := l7RuleResourceSchema(t)
	plan := tfsdk.Plan{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID:                 types.StringValue(l7RuleTestRuleID.String()),
			Type:               types.StringValue("PATH"),
			CompareType:        types.StringValue("STARTS_WITH"),
			Value:              types.StringValue("/api"),
			Key:                types.StringNull(),
			Invert:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestL7RuleResourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceUnderTest, ok := NewL7RuleResource().(*L7RuleResource)
	if !ok || resourceUnderTest == nil {
		t.Fatal("constructor did not return a L7RuleResource")
	}

	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_l7_rule" {
		t.Fatalf("expected type name viettelcloud_lb_l7_rule, got %s", metadata.TypeName)
	}

	t.Run("nil provider data is a no-op", func(t *testing.T) {
		t.Parallel()
		var configResp resource.ConfigureResponse
		resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &configResp)
		if configResp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", configResp.Diagnostics)
		}
	})

	t.Run("wrong provider data type reports error", func(t *testing.T) {
		t.Parallel()
		var configResp resource.ConfigureResponse
		resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &configResp)
		if !configResp.Diagnostics.HasError() {
			t.Fatal("expected diagnostic for wrong provider data type")
		}
	})

	t.Run("valid provider data configures client", func(t *testing.T) {
		t.Parallel()
		var configResp resource.ConfigureResponse
		client := newL7RuleTestClient(t, func(req *http.Request) (*http.Response, error) {
			return nil, nil
		})
		data := &providerdata.Configured{
			LoadBalancer: client,
			ProjectID:    l7RuleTestProjectID,
			Claims:       keylock.New(),
		}
		resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: data}, &configResp)
		if configResp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", configResp.Diagnostics)
		}
		if resourceUnderTest.client != client || resourceUnderTest.projectID != l7RuleTestProjectID || resourceUnderTest.claims != data.Claims {
			t.Fatal("client, projectID, or claims not configured properly")
		}
	})
}

func TestL7RuleValidateConfig(t *testing.T) {
	t.Parallel()

	s := l7RuleResourceSchema(t)
	r := NewL7RuleResource().(*L7RuleResource)

	t.Run("valid path rule without key", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
				Key:         types.StringNull(),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("valid header rule with key", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("HEADER"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("my-header-val"),
				Key:         types.StringValue("X-Custom-Header"),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
	})

	t.Run("missing key for header rule errors", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("HEADER"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("my-header-val"),
				Key:         types.StringNull(),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error for missing key in HEADER rule")
		}
	})

	t.Run("empty key for cookie rule errors", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("COOKIE"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("val"),
				Key:         types.StringValue("   "),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error for empty key in COOKIE rule")
		}
	})

	for _, ruleType := range []string{"HEADER", "PATH"} {
		t.Run("explicit empty key for "+ruleType+" rule errors", func(t *testing.T) {
			t.Parallel()
			plan := tfsdk.Plan{Schema: s}
			model := L7RuleResourceModel{
				L7RuleModel: L7RuleModel{
					Type:        types.StringValue(ruleType),
					CompareType: types.StringValue("EQUAL_TO"),
					Value:       types.StringValue("val"),
					Key:         types.StringValue(""),
					Invert:      types.BoolValue(false),
				},
				L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
			}
			_ = plan.Set(context.Background(), &model)
			cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
			var resp resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected error for an empty key in a %s rule", ruleType)
			}
		})
	}

	t.Run("unknown key for header rule is allowed during plan", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("HEADER"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("val"),
				Key:         types.StringUnknown(),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error for unknown key during plan: %v", resp.Diagnostics)
		}
	})

	t.Run("invalid key on path rule errors", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
				Key:         types.StringValue("not-allowed"),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error when key is specified for PATH rule")
		}
		if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "key cannot be specified when type is PATH.") {
			t.Fatalf("expected error detail about key not allowed for PATH, got %v", resp.Diagnostics)
		}
	})

	t.Run("unknown key on path rule errors", func(t *testing.T) {
		t.Parallel()
		plan := tfsdk.Plan{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
				Key:         types.StringUnknown(),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)
		cfg := tfsdk.Config{Raw: plan.Raw, Schema: s}
		var resp resource.ValidateConfigResponse
		r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error when unknown key is specified for PATH rule")
		}
		if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "key cannot be specified when type is PATH.") {
			t.Fatalf("expected error detail about key not allowed for PATH, got %v", resp.Diagnostics)
		}
	})
}

func TestL7RuleResourceTypeUpdatesInPlace(t *testing.T) {
	t.Parallel()

	attribute, ok := l7RuleResourceSchema(t).Attributes["type"].(schema.StringAttribute)
	if !ok {
		t.Fatal("type is not a string attribute")
	}
	if len(attribute.PlanModifiers) != 0 {
		t.Fatalf("expected no plan modifiers on type, got %d", len(attribute.PlanModifiers))
	}
}

func TestL7RuleResourceL7PolicyReferenceRequiresReplace(t *testing.T) {
	t.Parallel()

	s := l7RuleResourceSchema(t)
	attribute, ok := s.Attributes["l7_policy"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("l7_policy is %T, want a single nested attribute", s.Attributes["l7_policy"])
	}
	if !attribute.Required || attribute.Optional || attribute.Computed {
		t.Fatalf("l7_policy flags: required=%t optional=%t computed=%t, want required only",
			attribute.Required, attribute.Optional, attribute.Computed)
	}
	inner, ok := attribute.Attributes[reference.ID].(schema.StringAttribute)
	if len(attribute.Attributes) != 1 || !ok || !inner.Required {
		t.Fatalf("l7_policy attributes = %#v, want one required string %q", attribute.Attributes, reference.ID)
	}
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("expected one plan modifier for l7_policy, got %d", len(attribute.PlanModifiers))
	}

	before := reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String()))
	tests := map[string]struct {
		plan types.Object
		want bool
	}{
		"same id keeps":       {plan: before, want: false},
		"padded id keeps":     {plan: reference.Value(reference.ID, types.StringValue(" "+strings.ToUpper(l7RuleTestPolicyID.String())+"\n")), want: false},
		"another id replaces": {plan: reference.Value(reference.ID, types.StringValue(core.UUID{0x99}.String())), want: true},
		"unknown id replaces": {plan: reference.Value(reference.ID, types.StringUnknown()), want: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			state := tfsdk.State{Schema: s}
			if diags := state.Set(context.Background(), &L7RuleResourceModel{L7Policy: before}); diags.HasError() {
				t.Fatalf("set modifier state: %v", diags)
			}
			plan := tfsdk.Plan{Schema: s}
			if diags := plan.Set(context.Background(), &L7RuleResourceModel{L7Policy: tt.plan}); diags.HasError() {
				t.Fatalf("set modifier plan: %v", diags)
			}
			request := planmodifier.ObjectRequest{
				Path:       path.Root("l7_policy"),
				State:      state,
				Plan:       plan,
				StateValue: before,
				PlanValue:  tt.plan,
			}
			var response planmodifier.ObjectResponse
			attribute.PlanModifiers[0].PlanModifyObject(context.Background(), request, &response)
			if response.RequiresReplace != tt.want {
				t.Errorf("RequiresReplace = %t, want %t", response.RequiresReplace, tt.want)
			}
		})
	}
}

func TestPopulateL7RuleResourceStatePreservesWhitespaceTrimmedKey(t *testing.T) {
	t.Parallel()

	rule := sampleL7RuleSchema()
	key := "X-Header"
	rule.Key = core.NullableFromPtr(&key)

	state := &L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			Key: types.StringValue("  X-Header  "),
		},
	}
	populateL7RuleResourceState(rule, state)

	if state.Key.ValueString() != "  X-Header  " {
		t.Fatalf("expected state.Key to preserve configured value '  X-Header  ', got %q", state.Key.ValueString())
	}
}

// The request trims value, so state keeps the configured spelling only when
// it differs from the backend value by outer whitespace.
func TestPopulateL7RuleResourceStatePreservesWhitespaceTrimmedValue(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		prior types.String
		want  types.String
	}{
		{name: "trimmed match keeps configured spelling", prior: types.StringValue("\t/api "), want: types.StringValue("\t/api ")},
		{name: "exact match is kept", prior: types.StringValue("/api"), want: types.StringValue("/api")},
		{name: "different value follows the response", prior: types.StringValue(" /v2 "), want: types.StringValue("/api")},
		{name: "null after import is filled", prior: types.StringNull(), want: types.StringValue("/api")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &L7RuleResourceModel{L7RuleModel: L7RuleModel{Value: tt.prior}}
			populateL7RuleResourceState(sampleL7RuleSchema(), state)
			if !state.Value.Equal(tt.want) {
				t.Fatalf("value = %s, want %s", state.Value, tt.want)
			}
		})
	}
}

func TestPopulateL7RuleModelMapsKeyPresenceLiterally(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		apiValue *string
		want     types.String
	}{
		{name: "nil stays null", apiValue: nil, want: types.StringNull()},
		{name: "explicit empty stays known", apiValue: new(""), want: types.StringValue("")},
		{name: "value is mapped", apiValue: new("X-Header"), want: types.StringValue("X-Header")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule := sampleL7RuleSchema()
			rule.Key = core.NullableFromPtr(tt.apiValue)
			var m L7RuleModel
			populateL7RuleModel(rule, &m)
			if !m.Key.Equal(tt.want) {
				t.Fatalf("key = %s, want %s", m.Key, tt.want)
			}
		})
	}
}

func TestPopulateL7RuleResourceStateMapsL7PolicyReference(t *testing.T) {
	t.Parallel()

	upperPolicyID := strings.ToUpper(l7RuleTestPolicyID.String())
	tests := map[string]struct {
		prior types.Object
		want  types.Object
	}{
		"null after import is filled": {
			prior: types.ObjectNull(reference.AttributeTypes(reference.ID)),
			want:  reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		},
		"equal configured casing is kept": {
			prior: reference.Value(reference.ID, types.StringValue(upperPolicyID)),
			want:  reference.Value(reference.ID, types.StringValue(upperPolicyID)),
		},
		"different policy follows the response": {
			prior: reference.Value(reference.ID, types.StringValue(core.UUID{0x99}.String())),
			want:  reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			state := &L7RuleResourceModel{L7Policy: tt.prior}
			populateL7RuleResourceState(sampleL7RuleSchema(), state)
			if !state.L7Policy.Equal(tt.want) {
				t.Fatalf("l7_policy = %s, want %s", state.L7Policy, tt.want)
			}
		})
	}
}

func TestL7RuleTypeValidation(t *testing.T) {
	t.Parallel()

	s := l7RuleResourceSchema(t)
	attr := s.Attributes["type"].(schema.StringAttribute)

	for _, ruleType := range []string{"COOKIE", "FILE_TYPE", "HEADER", "HOST_NAME", "PATH", " PATH\n"} {
		req := validator.StringRequest{
			Path:        path.Root("type"),
			ConfigValue: types.StringValue(ruleType),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if resp.Diagnostics.HasError() {
			t.Errorf("expected type %q to be valid, got errors: %v", ruleType, resp.Diagnostics)
		}
	}

	for _, invalid := range []string{"UNKNOWN", "path", "header", "QUERY", " "} {
		req := validator.StringRequest{
			Path:        path.Root("type"),
			ConfigValue: types.StringValue(invalid),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if !resp.Diagnostics.HasError() {
			t.Errorf("expected type %q to be rejected", invalid)
		}
	}
}

func TestL7RuleCompareTypeValidation(t *testing.T) {
	t.Parallel()

	s := l7RuleResourceSchema(t)
	attr := s.Attributes["compare_type"].(schema.StringAttribute)

	for _, ct := range []string{"CONTAINS", "ENDS_WITH", "EQUAL_TO", "REGEX", "STARTS_WITH", "\tEQUAL_TO "} {
		req := validator.StringRequest{
			Path:        path.Root("compare_type"),
			ConfigValue: types.StringValue(ct),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if resp.Diagnostics.HasError() {
			t.Errorf("expected compare_type %q to be valid, got errors: %v", ct, resp.Diagnostics)
		}
	}

	for _, invalid := range []string{"EXACT", "contains", "regex", "NOT_EQUAL", " "} {
		req := validator.StringRequest{
			Path:        path.Root("compare_type"),
			ConfigValue: types.StringValue(invalid),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if !resp.Diagnostics.HasError() {
			t.Errorf("expected compare_type %q to be rejected", invalid)
		}
	}
}

func TestBuildL7RuleCreateBody(t *testing.T) {
	t.Parallel()

	t.Run("valid path rule with invert", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
				Key:         types.StringNull(),
				Invert:      types.BoolValue(true),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}

		body, diags := buildL7RuleCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}

		if body.Type != loadbalancersdk.L7RuleTypePATH || body.CompareType != loadbalancersdk.L7RuleCompareTypeSTARTSWITH || body.Value != "/api" {
			t.Fatalf("unexpected fields in body: %#v", body)
		}
		if body.Invert == nil || !*body.Invert {
			t.Fatalf("expected invert to be true, got %#v", body.Invert)
		}
		if body.Key.IsSpecified() {
			t.Fatalf("expected key to be nil, got %#v", body.Key)
		}
	})

	t.Run("valid header rule with key", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("HEADER"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("my-val"),
				Key:         types.StringValue("X-Header"),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}

		body, diags := buildL7RuleCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}

		if core.NullableToPtr(body.Key) == nil || *core.NullableToPtr(body.Key) != "X-Header" {
			t.Fatalf("expected key X-Header, got %#v", body.Key)
		}
	})

	t.Run("missing type errors", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringNull(),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
			},
		}
		_, diags := buildL7RuleCreateBody(plan)
		if !diags.HasError() {
			t.Fatal("expected error for missing type")
		}
	})

	t.Run("missing compare_type errors", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringNull(),
				Value:       types.StringValue("/api"),
			},
		}
		_, diags := buildL7RuleCreateBody(plan)
		if !diags.HasError() {
			t.Fatal("expected error for missing compare_type")
		}
	})

	t.Run("missing value errors", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringNull(),
			},
		}
		_, diags := buildL7RuleCreateBody(plan)
		if !diags.HasError() {
			t.Fatal("expected error for missing value")
		}
	})

	t.Run("cookie rule missing key errors", func(t *testing.T) {
		t.Parallel()
		plan := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("COOKIE"),
				CompareType: types.StringValue("EQUAL_TO"),
				Value:       types.StringValue("val"),
				Key:         types.StringNull(),
			},
		}
		_, diags := buildL7RuleCreateBody(plan)
		if !diags.HasError() {
			t.Fatal("expected error for COOKIE rule without key")
		}
	})
}

func TestBuildL7RulePartialUpdateBody(t *testing.T) {
	t.Parallel()

	state := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			Type:        types.StringValue("PATH"),
			CompareType: types.StringValue("STARTS_WITH"),
			Value:       types.StringValue("/api"),
			Key:         types.StringNull(),
			Invert:      types.BoolValue(false),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}

	t.Run("detects compare_type change", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.CompareType = types.StringValue("EQUAL_TO")

		body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if body.CompareType == nil || *body.CompareType != loadbalancersdk.L7RuleCompareTypeEQUALTO {
			t.Fatalf("expected CompareType EQUAL_TO, got %#v", body.CompareType)
		}
		if body.Value != nil || body.Invert != nil || body.Key.IsSpecified() {
			t.Fatalf("expected other fields to be nil, got %#v", body)
		}
	})

	t.Run("detects value and invert changes", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.Value = types.StringValue("/v2")
		plan.Invert = types.BoolValue(true)

		body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if body.Value == nil || *body.Value != "/v2" {
			t.Fatalf("expected Value /v2, got %#v", body.Value)
		}
		if body.Invert == nil || !*body.Invert {
			t.Fatalf("expected Invert true, got %#v", body.Invert)
		}
		if body.CompareType != nil || body.Key.IsSpecified() {
			t.Fatalf("expected other fields to be nil, got %#v", body)
		}
	})

	t.Run("no changes returns false and empty body", func(t *testing.T) {
		t.Parallel()
		body, hasChanges, diags := buildL7RulePartialUpdateBody(state, state)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if hasChanges {
			t.Fatalf("expected hasChanges to be false, got true (body: %#v)", body)
		}
	})

	t.Run("detects key change for header rule", func(t *testing.T) {
		t.Parallel()
		headerState := state
		headerState.Type = types.StringValue("HEADER")
		headerState.Key = types.StringValue("X-Old-Header")

		headerPlan := headerState
		headerPlan.Key = types.StringValue("X-New-Header")

		body, hasChanges, diags := buildL7RulePartialUpdateBody(headerPlan, headerState)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if core.NullableToPtr(body.Key) == nil || *core.NullableToPtr(body.Key) != "X-New-Header" {
			t.Fatalf("expected Key X-New-Header, got %#v", body.Key)
		}
	})

	t.Run("type change to header sends type, compare_type, value, and key", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.Type = types.StringValue("HEADER")
		plan.Key = types.StringValue(" X-Header ")

		body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if body.Type == nil || *body.Type != loadbalancersdk.L7RuleTypeHEADER {
			t.Fatalf("expected Type HEADER, got %#v", body.Type)
		}
		if body.CompareType == nil || *body.CompareType != loadbalancersdk.L7RuleCompareTypeSTARTSWITH {
			t.Fatalf("expected unchanged CompareType to be sent, got %#v", body.CompareType)
		}
		if body.Value == nil || *body.Value != "/api" {
			t.Fatalf("expected unchanged Value to be sent, got %#v", body.Value)
		}
		if core.NullableToPtr(body.Key) == nil || *core.NullableToPtr(body.Key) != "X-Header" {
			t.Fatalf("expected trimmed Key X-Header, got %#v", body.Key)
		}
		if body.Invert != nil {
			t.Fatalf("expected unchanged Invert to be omitted, got %#v", body.Invert)
		}
	})

	t.Run("type change from header to path sends a null key", func(t *testing.T) {
		t.Parallel()
		headerState := state
		headerState.Type = types.StringValue("HEADER")
		headerState.Key = types.StringValue("X-Header")
		plan := state

		body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, headerState)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if body.Type == nil || *body.Type != loadbalancersdk.L7RuleTypePATH {
			t.Fatalf("expected Type PATH, got %#v", body.Type)
		}
		if body.CompareType == nil || body.Value == nil {
			t.Fatalf("expected CompareType and Value with the type change, got %#v", body)
		}
		if !body.Key.IsSpecified() || !body.Key.IsNull() {
			t.Fatalf("expected a null Key for PATH, got %#v", body.Key)
		}
	})

	t.Run("type change from path to host_name sends a null key", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.Type = types.StringValue("HOST_NAME")

		body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
		if diags.HasError() || !hasChanges {
			t.Fatalf("expected changes, diags=%v", diags)
		}
		if body.Type == nil || *body.Type != loadbalancersdk.L7RuleTypeHOSTNAME {
			t.Fatalf("expected Type HOST_NAME, got %#v", body.Type)
		}
		if !body.Key.IsSpecified() || !body.Key.IsNull() {
			t.Fatalf("expected a null Key for HOST_NAME, got %#v", body.Key)
		}
	})

	t.Run("type change to cookie without key errors", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.Type = types.StringValue("COOKIE")

		if _, _, diags := buildL7RulePartialUpdateBody(plan, state); !diags.HasError() {
			t.Fatal("expected a diagnostic for the missing key")
		}
	})

	t.Run("unknown type errors", func(t *testing.T) {
		t.Parallel()
		plan := state
		plan.Type = types.StringUnknown()

		if _, _, diags := buildL7RulePartialUpdateBody(plan, state); !diags.HasError() {
			t.Fatal("expected a diagnostic for the unknown type")
		}
	})
}

// The API trims value, so the patch sends the trimmed value and a change of
// outer whitespace alone is not a change.
func TestBuildL7RulePartialUpdateTrimsValue(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		stateType string
		state     string
		plan      string
		wantPatch *string
	}{
		{name: "padded new value is trimmed", stateType: "PATH", state: "/api", plan: " /v2\n", wantPatch: new("/v2")},
		{name: "outer whitespace alone is unchanged", stateType: "PATH", state: "/api", plan: " /api "},
		{name: "removed outer whitespace is unchanged", stateType: "PATH", state: "\t/api", plan: "/api"},
		{name: "type change resends the trimmed value", stateType: "HOST_NAME", state: "/api", plan: " /api ", wantPatch: new("/api")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := L7RuleResourceModel{
				L7RuleModel: L7RuleModel{
					Type:        types.StringValue(tt.stateType),
					CompareType: types.StringValue("STARTS_WITH"),
					Value:       types.StringValue(tt.state),
					Key:         types.StringNull(),
					Invert:      types.BoolValue(false),
				},
				L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
			}
			plan := state
			plan.Type = types.StringValue("PATH")
			plan.Value = types.StringValue(tt.plan)

			body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if tt.wantPatch == nil {
				if hasChanges || body.Value != nil {
					t.Fatalf("hasChanges=%t value=%#v, want no patch", hasChanges, body.Value)
				}
				return
			}
			if !hasChanges || body.Value == nil || *body.Value != *tt.wantPatch {
				t.Fatalf("hasChanges=%t value=%#v, want %q", hasChanges, body.Value, *tt.wantPatch)
			}
		})
	}
}

// The API trims every string of an L7 rule, so a change of outer whitespace
// alone sends no patch.
func TestBuildL7RulePartialUpdateIgnoresSpelling(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		mutate     func(*L7RuleResourceModel)
		wantChange bool
	}{
		{name: "padded type", mutate: func(m *L7RuleResourceModel) { m.Type = types.StringValue(" HEADER ") }},
		{name: "padded compare type", mutate: func(m *L7RuleResourceModel) { m.CompareType = types.StringValue("EQUAL_TO\n") }},
		{name: "padded key", mutate: func(m *L7RuleResourceModel) { m.Key = types.StringValue("\tX-Env ") }},
		{name: "another key", mutate: func(m *L7RuleResourceModel) { m.Key = types.StringValue(" X-Stage ") }, wantChange: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := L7RuleResourceModel{
				L7RuleModel: L7RuleModel{
					Type:        types.StringValue("HEADER"),
					CompareType: types.StringValue("EQUAL_TO"),
					Value:       types.StringValue("prod"),
					Key:         types.StringValue("X-Env"),
					Invert:      types.BoolValue(false),
				},
				L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
			}
			plan := state
			tt.mutate(&plan)

			body, hasChanges, diags := buildL7RulePartialUpdateBody(plan, state)
			if diags.HasError() || hasChanges != tt.wantChange {
				t.Fatalf("hasChanges=%t diagnostics=%v, want %t", hasChanges, diags, tt.wantChange)
			}
			if !tt.wantChange {
				return
			}
			if key := core.NullableToPtr(body.Key); key == nil || *key != "X-Stage" || body.Type != nil || body.CompareType != nil {
				t.Fatalf("patch = %#v, want only the trimmed key", body)
			}
		})
	}
}

func TestL7RuleResourceCreateLifecycleSuccess(t *testing.T) {
	t.Parallel()

	var createCalled, pollCalled bool

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		// CreateL7rule
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/l7policies/%s/l7rules/", l7RuleTestPolicyID)) {
			createCalled = true
			var body loadbalancersdk.L7RuleCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Type != loadbalancersdk.L7RuleTypePATH || body.CompareType != loadbalancersdk.L7RuleCompareTypeSTARTSWITH || body.Value != "/api" {
				t.Fatalf("unexpected create body payload: %#v", body)
			}
			return l7RuleJSONResponse(http.StatusCreated, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_CREATE", "OFFLINE")), nil
		}
		// WaitUntilReady: GetL7rule
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			pollCalled = true
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	plan := tfsdk.Plan{
		Schema: s,
		Raw:    tfsdk.Config{Schema: s}.Raw,
	}

	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			Type:        types.StringValue("PATH"),
			CompareType: types.StringValue("STARTS_WITH"),
			Value:       types.StringValue("/api"),
			Key:         types.StringNull(),
			Invert:      types.BoolValue(false),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: s}

	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	if !createCalled {
		t.Fatal("expected CreateL7rule to be called")
	}
	if !pollCalled {
		t.Fatal("expected waitUntilReady poll to be called")
	}

	var state L7RuleResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if state.ID.ValueString() != l7RuleTestRuleID.String() {
		t.Fatalf("expected state ID %s, got %s", l7RuleTestRuleID, state.ID.ValueString())
	}
	if state.ProvisioningStatus.ValueString() != "ACTIVE" {
		t.Fatalf("expected state provisioning_status ACTIVE, got %s", state.ProvisioningStatus.ValueString())
	}
}

// The API trims every string of an L7 rule, so Create sends trimmed values and
// state keeps the configured spelling through Create and the next Read.
func TestL7RuleResourceCreateTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	const padded = " /api\n"
	key := "X-Env"
	var sent []string
	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/l7policies/%s/l7rules/", l7RuleTestPolicyID)) {
			var body loadbalancersdk.L7RuleCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if sentKey := core.NullableToPtr(body.Key); body.Type != loadbalancersdk.L7RuleTypeHEADER || body.CompareType != loadbalancersdk.L7RuleCompareTypeEQUALTO || sentKey == nil || *sentKey != key {
				t.Errorf("create body = %#v, want trimmed type, compare_type, and key", body)
			}
			sent = append(sent, body.Value)
			return l7RuleJSONResponse(http.StatusCreated, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "HEADER", "EQUAL_TO", "/api", &key, false, "PENDING_CREATE", "OFFLINE")), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "HEADER", "EQUAL_TO", "/api", &key, false, "ACTIVE", "ONLINE")), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	s := l7RuleResourceSchema(t)
	plan := tfsdk.Plan{Schema: s, Raw: tfsdk.Config{Schema: s}.Raw}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			Type:        types.StringValue(" HEADER"),
			CompareType: types.StringValue("EQUAL_TO\t"),
			Value:       types.StringValue(padded),
			Key:         types.StringValue(" X-Env "),
			Invert:      types.BoolValue(false),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(" "+strings.ToUpper(l7RuleTestPolicyID.String())+" ")),
	}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}
	if want := []string{"/api"}; !slices.Equal(sent, want) {
		t.Fatalf("sent values = %q, want %q", sent, want)
	}
	var created L7RuleResourceModel
	if diags := createResp.State.Get(context.Background(), &created); diags.HasError() {
		t.Fatalf("get create state: %v", diags)
	}
	if !created.Value.Equal(model.Value) || !created.Type.Equal(model.Type) || !created.CompareType.Equal(model.CompareType) ||
		!created.Key.Equal(model.Key) || !created.L7Policy.Equal(model.L7Policy) {
		t.Fatalf("create state = %#v, want the configured spellings", created)
	}

	readResp := resource.ReadResponse{State: createResp.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	var refreshed L7RuleResourceModel
	if diags := readResp.State.Get(context.Background(), &refreshed); diags.HasError() {
		t.Fatalf("get read state: %v", diags)
	}
	if !refreshed.Value.Equal(model.Value) || !refreshed.Type.Equal(model.Type) || !refreshed.CompareType.Equal(model.CompareType) ||
		!refreshed.Key.Equal(model.Key) || !refreshed.L7Policy.Equal(model.L7Policy) {
		t.Fatalf("read state = %#v, want the configured spellings", refreshed)
	}
}

func TestL7RuleResourceCreateWaiterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
			// CreateL7rule succeeds with PENDING_CREATE
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/l7policies/%s/l7rules/", l7RuleTestPolicyID)) {
				return l7RuleJSONResponse(http.StatusCreated, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_CREATE", "OFFLINE")), nil
			}
			// Readiness poll always returns PENDING_CREATE
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_CREATE", "OFFLINE")), nil
			}
			return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		s := l7RuleResourceSchema(t)
		plan := tfsdk.Plan{Schema: s, Raw: tfsdk.Config{Schema: s}.Raw}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				Type:        types.StringValue("PATH"),
				CompareType: types.StringValue("STARTS_WITH"),
				Value:       types.StringValue("/api"),
				Key:         types.StringNull(),
				Invert:      types.BoolValue(false),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		_ = plan.Set(context.Background(), &model)

		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: s}

		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatal("expected error on waiter timeout")
		}

		// State must have preserved the created ID
		var state L7RuleResourceModel
		if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if state.ID.ValueString() != l7RuleTestRuleID.String() {
			t.Fatalf("expected preserved ID %s in state, got %s", l7RuleTestRuleID, state.ID.ValueString())
		}
	})
}

func TestL7RuleResourceCreateWaiterErrorStatus(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		// CreateL7rule succeeds with PENDING_CREATE
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/l7policies/%s/l7rules/", l7RuleTestPolicyID)) {
			return l7RuleJSONResponse(http.StatusCreated, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_CREATE", "OFFLINE")), nil
		}
		// Readiness poll returns ERROR status
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ERROR", "ERROR")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	plan := tfsdk.Plan{Schema: s, Raw: tfsdk.Config{Schema: s}.Raw}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			Type:        types.StringValue("PATH"),
			CompareType: types.StringValue("STARTS_WITH"),
			Value:       types.StringValue("/api"),
			Key:         types.StringNull(),
			Invert:      types.BoolValue(false),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = plan.Set(context.Background(), &model)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: s}

	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected error on ERROR status")
	}
}

func TestL7RuleResourceReadSuccess(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var readResp resource.ReadResponse
	readResp.State = state

	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	var updatedState L7RuleResourceModel
	_ = readResp.State.Get(context.Background(), &updatedState)
	if updatedState.Value.ValueString() != "/api" {
		t.Fatalf("expected Value /api, got %s", updatedState.Value.ValueString())
	}
	if updatedState.ProvisioningStatus.ValueString() != "ACTIVE" {
		t.Fatalf("expected ProvisioningStatus ACTIVE, got %s", updatedState.ProvisioningStatus.ValueString())
	}
}

func TestL7RuleResourceReadNotFoundRemovesState(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var readResp resource.ReadResponse
	readResp.State = state

	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Fatal("expected resource state to be removed on 404")
	}
}

func TestL7RuleResourceReadDeletedStatusRemovesState(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "DELETED", "OFFLINE")), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var readResp resource.ReadResponse
	readResp.State = state

	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Fatal("expected resource state to be removed on DELETED status")
	}
}

func TestL7RuleResourceReadAPIError(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleJSONResponse(http.StatusInternalServerError, `{"detail":"server error"}`), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var readResp resource.ReadResponse
	readResp.State = state

	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic on API error")
	}
}

func TestL7RuleResourceUpdateInPlace(t *testing.T) {
	t.Parallel()

	var patchCalled, pollCalled bool

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		// PartialUpdateL7rule (PATCH)
		if req.Method == http.MethodPatch && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			patchCalled = true
			var body loadbalancersdk.L7RulePartialUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			if body.Value == nil || *body.Value != "/v2" {
				t.Fatalf("expected updated value /v2, got %#v", body.Value)
			}
			if body.Invert == nil || !*body.Invert {
				t.Fatalf("expected updated invert true, got %#v", body.Invert)
			}
			return l7RuleJSONResponse(http.StatusAccepted, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/v2", nil, true, "PENDING_UPDATE", "ONLINE")), nil
		}
		// WaitUntilReady: GetL7rule
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			pollCalled = true
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/v2", nil, true, "ACTIVE", "ONLINE")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	stateModel := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID:                 types.StringValue(l7RuleTestRuleID.String()),
			Type:               types.StringValue("PATH"),
			CompareType:        types.StringValue("STARTS_WITH"),
			Value:              types.StringValue("/api"),
			Key:                types.StringNull(),
			Invert:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}

	planModel := stateModel
	planModel.Value = types.StringValue("/v2")
	planModel.Invert = types.BoolValue(true)

	state := tfsdk.State{Schema: s}
	_ = state.Set(context.Background(), &stateModel)
	plan := tfsdk.Plan{Schema: s, Raw: tfsdk.Config{Schema: s}.Raw}
	_ = plan.Set(context.Background(), &planModel)

	var updateResp resource.UpdateResponse
	updateResp.State = state

	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}

	if !patchCalled {
		t.Fatal("expected PartialUpdateL7rule to be called")
	}
	if !pollCalled {
		t.Fatal("expected waitUntilReady to be called")
	}

	var updatedState L7RuleResourceModel
	_ = updateResp.State.Get(context.Background(), &updatedState)
	if updatedState.Value.ValueString() != "/v2" {
		t.Fatalf("expected updated value /v2, got %s", updatedState.Value.ValueString())
	}
	if !updatedState.Invert.ValueBool() {
		t.Fatal("expected updated invert true")
	}
}

func TestL7RuleResourceUpdateTypeHeaderToPathKeepsIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var patchBody map[string]any
		resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPatch && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
				if err := json.NewDecoder(req.Body).Decode(&patchBody); err != nil {
					t.Fatalf("decode patch body: %v", err)
				}
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_UPDATE", "ONLINE")), nil
			}
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
			}
			return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		s := l7RuleResourceSchema(t)
		stateModel := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{
				ID:                 types.StringValue(l7RuleTestRuleID.String()),
				Type:               types.StringValue("HEADER"),
				CompareType:        types.StringValue("STARTS_WITH"),
				Value:              types.StringValue("/api"),
				Key:                types.StringValue("X-Header"),
				Invert:             types.BoolValue(false),
				ProvisioningStatus: types.StringValue("ACTIVE"),
				Status:             types.StringValue("ONLINE"),
				CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
				UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			},
			L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		planModel := stateModel
		planModel.Type = types.StringValue("PATH")
		planModel.Key = types.StringNull()
		planModel.ProvisioningStatus = types.StringUnknown()
		planModel.Status = types.StringUnknown()
		planModel.UpdatedAt = types.StringUnknown()

		state := tfsdk.State{Schema: s}
		if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		plan := tfsdk.Plan{Schema: s}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		updateResp := resource.UpdateResponse{State: state}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
		if updateResp.Diagnostics.HasError() {
			t.Fatalf("update failed: %v", updateResp.Diagnostics)
		}

		if patchBody["type"] != "PATH" || patchBody["compare_type"] != "STARTS_WITH" || patchBody["value"] != "/api" {
			t.Fatalf("expected type, compare_type, and value in the PATCH body, got %v", patchBody)
		}
		if key, ok := patchBody["key"]; !ok || key != nil {
			t.Fatalf("expected a null key in the PATCH body, got %v", patchBody)
		}

		var updated L7RuleResourceModel
		if diags := updateResp.State.Get(context.Background(), &updated); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if updated.ID.ValueString() != l7RuleTestRuleID.String() {
			t.Fatalf("expected ID %s to be kept, got %s", l7RuleTestRuleID, updated.ID.ValueString())
		}
		if updated.Type.ValueString() != "PATH" || !updated.Key.IsNull() {
			t.Fatalf("expected type PATH with a null key, got type %s key %s", updated.Type, updated.Key)
		}
	})
}

func TestL7RuleResourceUpdateNoChanges(t *testing.T) {
	t.Parallel()

	var getCalled bool

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			t.Fatal("PATCH must not be called when there are no changes")
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			getCalled = true
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID:                 types.StringValue(l7RuleTestRuleID.String()),
			Type:               types.StringValue("PATH"),
			CompareType:        types.StringValue("STARTS_WITH"),
			Value:              types.StringValue("/api"),
			Key:                types.StringNull(),
			Invert:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}

	state := tfsdk.State{Schema: s}
	_ = state.Set(context.Background(), &model)
	plan := tfsdk.Plan{Schema: s, Raw: tfsdk.Config{Schema: s}.Raw}
	_ = plan.Set(context.Background(), &model)

	var updateResp resource.UpdateResponse
	updateResp.State = state

	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}
	if !getCalled {
		t.Fatal("expected GetL7rule to be called on no changes")
	}
}

// A failed Update must return a diagnostic on the first refusal and must not
// poll until l7RuleReadyTimeout. The idle wait before the PATCH reads an ACTIVE
// L7 rule, and get answers every later GET of the L7 rule.
func TestL7RuleResourceUpdateErrors(t *testing.T) {
	t.Parallel()

	ruleJSON := func(id core.UUID, provisioningStatus string) string {
		return sampleL7RuleSchemaJSON(id, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/v2", nil, false, provisioningStatus, "ONLINE")
	}
	tests := []struct {
		name        string
		changeValue bool
		patch       func() *http.Response
		get         func() *http.Response
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "patch refused",
			changeValue: true,
			patch: func() *http.Response {
				return l7RuleJSONResponse(http.StatusBadRequest, `{"errors":[{"message":"Invalid value for this rule type"}]}`)
			},
			wantSummary: "Error updating Load Balancer L7 Rule",
			wantDetail:  "Invalid value for this rule type",
		},
		{
			name:        "patch answers another rule",
			changeValue: true,
			patch: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, ruleJSON(core.UUID{3}, "PENDING_UPDATE"))
			},
			wantSummary: "Error updating Load Balancer L7 Rule",
			wantDetail:  "was not applied in place",
		},
		{
			name:        "rule enters ERROR after patch",
			changeValue: true,
			patch: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, ruleJSON(l7RuleTestRuleID, "PENDING_UPDATE"))
			},
			get:         func() *http.Response { return l7RuleJSONResponse(http.StatusOK, ruleJSON(l7RuleTestRuleID, "ERROR")) },
			wantSummary: "Error waiting for Load Balancer L7 Rule update",
			wantDetail:  "ERROR provisioning status",
		},
		{
			name: "read fails without changes",
			get: func() *http.Response {
				return l7RuleJSONResponse(http.StatusInternalServerError, `{"errors":[{"message":"internal error"}]}`)
			},
			wantSummary: "Error updating Load Balancer L7 Rule",
			wantDetail:  "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				patched := false
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodPatch && tt.patch != nil:
						patched = true
						return tt.patch(), nil
					case req.Method == http.MethodGet && tt.changeValue && !patched:
						return l7RuleJSONResponse(http.StatusOK, ruleJSON(l7RuleTestRuleID, "ACTIVE")), nil
					case req.Method == http.MethodGet && tt.get != nil:
						return tt.get(), nil
					}
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
					return nil, nil
				})

				s := l7RuleResourceSchema(t)
				stateModel := L7RuleResourceModel{
					L7RuleModel: L7RuleModel{
						ID:                 types.StringValue(l7RuleTestRuleID.String()),
						Type:               types.StringValue("PATH"),
						CompareType:        types.StringValue("STARTS_WITH"),
						Value:              types.StringValue("/api"),
						Key:                types.StringNull(),
						Invert:             types.BoolValue(false),
						ProvisioningStatus: types.StringValue("ACTIVE"),
						Status:             types.StringValue("ONLINE"),
						CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
						UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
					},
					L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
				}
				planModel := stateModel
				if tt.changeValue {
					planModel.Value = types.StringValue("/v2")
				}

				state := tfsdk.State{Schema: s}
				if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}
				plan := tfsdk.Plan{Schema: s}
				if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
					t.Fatalf("set plan: %v", diags)
				}

				start := time.Now()
				updateResp := resource.UpdateResponse{State: state}
				resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
				if elapsed := time.Since(start); elapsed != 0 {
					t.Fatalf("expected the update to fail without a poll interval, got %v", elapsed)
				}
				assertL7RuleErrorDiagnostic(t, updateResp.Diagnostics, tt.wantSummary, tt.wantDetail)
			})
		})
	}
}

func TestL7RuleResourceDeleteSuccess(t *testing.T) {
	t.Parallel()

	var deleteCalled, pollCalled bool

	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		// DeleteL7rule
		if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			deleteCalled = true
			return l7RuleJSONResponse(http.StatusAccepted, ""), nil
		}
		// The idle wait reads an ACTIVE L7 rule, and waitUntilDeleted reads 404.
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			if !deleteCalled {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
			}
			pollCalled = true
			return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var deleteResp resource.DeleteResponse
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", deleteResp.Diagnostics)
	}
	if !deleteCalled {
		t.Fatal("expected DeleteL7rule to be called")
	}
	if !pollCalled {
		t.Fatal("expected waitUntilDeleted poll to be called")
	}
}

// Delete succeeds without a diagnostic when the L7 rule is already gone: the
// idle wait reads 404 and sends no DELETE, the GET of the L7 rule reads 404
// after the L7 policy read fails, and a DELETE that answers 404 needs no
// delete wait.
func TestL7RuleResourceDeleteNotFoundSuccess(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		parents     l7RuleTestParentStatus
		ruleFound   bool
		wantDeletes int
	}{
		{name: "rule gone before the delete", parents: l7RuleActiveParents},
		{name: "policy gone before the delete", parents: func(kind string) loadbalancersdk.LoadBalancingProvisioningStatus {
			if kind == "L7 policy" {
				return l7RuleTestParentGone
			}
			return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
		}},
		{name: "delete answers 404", parents: l7RuleActiveParents, ruleFound: true, wantDeletes: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deletes := 0
			resourceUnderTest := newL7RuleTestResourceWithParents(t, tt.parents, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deletes++
				}
				if req.Method == http.MethodGet && tt.ruleFound && deletes == 0 {
					return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
				}
				return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
			})

			diagnostics := executeL7RuleMutation(t, resourceUnderTest, http.MethodDelete)
			if diagnostics.HasError() {
				t.Fatalf("expected the delete of a missing L7 rule to succeed, got %v", diagnostics)
			}
			if deletes != tt.wantDeletes {
				t.Fatalf("DELETE calls=%d, want %d", deletes, tt.wantDeletes)
			}
		})
	}
}

// A missing parent does not prove that the L7 rule is gone. Delete returns a
// diagnostic and sends no DELETE while the GET of the L7 rule still finds it.
func TestL7RuleResourceDeleteFailsWhenOnlyAParentIsGone(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"L7 policy", "listener", "load balancer"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			deletes := 0
			parents := func(got string) loadbalancersdk.LoadBalancingProvisioningStatus {
				if got == kind {
					return l7RuleTestParentGone
				}
				return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
			}
			resourceUnderTest := newL7RuleTestResourceWithParents(t, parents, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					deletes++
				}
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
			})

			diagnostics := executeL7RuleMutation(t, resourceUnderTest, http.MethodDelete)
			assertL7RuleErrorDiagnostic(t, diagnostics, "Error deleting Load Balancer L7 Rule", kind)
			if deletes != 0 {
				t.Fatalf("DELETE calls=%d, want none", deletes)
			}
		})
	}
}

func TestL7RuleResourceDeleteWaitUntilDeletedState(t *testing.T) {
	t.Parallel()

	deleted := false
	resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
		// DeleteL7rule
		if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			deleted = true
			return l7RuleJSONResponse(http.StatusAccepted, ""), nil
		}
		// The idle wait reads an ACTIVE L7 rule, and waitUntilDeleted reads DELETED.
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			status := "ACTIVE"
			if deleted {
				status = "DELETED"
			}
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, status, "OFFLINE")), nil
		}
		return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := l7RuleResourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	_ = state.Set(context.Background(), &model)

	var deleteResp resource.DeleteResponse
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", deleteResp.Diagnostics)
	}
	if !deleted {
		t.Fatal("expected DeleteL7rule to be called")
	}
}

// A failed Delete must return a diagnostic on the first refusal, and a rule in
// ERROR after PENDING_DELETE must stop the delete waiter instead of polling
// until l7RuleReadyTimeout. The idle wait before the DELETE reads an ACTIVE L7
// rule, and get answers the GETs after the DELETE in order.
func TestL7RuleResourceDeleteErrors(t *testing.T) {
	t.Parallel()

	ruleResponse := func(provisioningStatus string) *http.Response {
		return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, provisioningStatus, "ERROR"))
	}
	tests := []struct {
		name        string
		del         func() *http.Response
		get         []string
		wantSummary string
		wantDetail  string
		wantElapsed time.Duration
	}{
		{
			name: "delete refused",
			del: func() *http.Response {
				return l7RuleJSONResponse(http.StatusConflict, `{"errors":[{"message":"L7 rule is in use"}]}`)
			},
			wantSummary: "Error deleting Load Balancer L7 Rule",
			wantDetail:  "L7 rule is in use",
		},
		{
			name:        "rule enters ERROR after pending delete",
			del:         func() *http.Response { return l7RuleJSONResponse(http.StatusAccepted, "") },
			get:         []string{"PENDING_DELETE", "ERROR"},
			wantSummary: "Error waiting for Load Balancer L7 Rule deletion",
			wantDetail:  "deletion failed with provisioning_status=ERROR",
			wantElapsed: l7RuleReadyPollInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				deleted := false
				gets := 0
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodDelete:
						deleted = true
						return tt.del(), nil
					case req.Method == http.MethodGet && !deleted:
						return ruleResponse("ACTIVE"), nil
					case req.Method == http.MethodGet && gets < len(tt.get):
						gets++
						return ruleResponse(tt.get[gets-1]), nil
					}
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
					return nil, nil
				})

				s := l7RuleResourceSchema(t)
				state := tfsdk.State{Schema: s}
				model := L7RuleResourceModel{
					L7RuleModel: L7RuleModel{ID: types.StringValue(l7RuleTestRuleID.String())},
					L7Policy:    reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
				}
				if diags := state.Set(context.Background(), &model); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}

				start := time.Now()
				var deleteResp resource.DeleteResponse
				resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Fatalf("expected the delete to fail after %v, got %v", tt.wantElapsed, elapsed)
				}
				assertL7RuleErrorDiagnostic(t, deleteResp.Diagnostics, tt.wantSummary, tt.wantDetail)
			})
		})
	}
}

// ERROR or UNKNOWN after PENDING_DELETE shows that the deletion failed. Delete
// reports it at once and keeps the state.
func TestL7RuleResourceDeleteFailureAfterPendingDeleteKeepsState(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"ERROR", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				deleted := false
				gets := 0
				after := []string{"PENDING_DELETE", status}
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					switch req.Method {
					case http.MethodDelete:
						deleted = true
						return l7RuleJSONResponse(http.StatusAccepted, ""), nil
					case http.MethodGet:
						current := "ACTIVE"
						if deleted {
							current = after[min(gets, len(after)-1)]
							gets++
						}
						return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, current, "ERROR")), nil
					}
					t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
					return nil, nil
				})

				state := tfsdk.State{Schema: l7RuleResourceSchema(t)}
				model := L7RuleResourceModel{
					L7RuleModel: L7RuleModel{ID: types.StringValue(l7RuleTestRuleID.String())},
					L7Policy:    reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
				}
				if diags := state.Set(context.Background(), &model); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}

				start := time.Now()
				deleteResp := resource.DeleteResponse{State: state}
				resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
				if elapsed := time.Since(start); elapsed != l7RuleReadyPollInterval {
					t.Fatalf("expected the delete to fail after %v, got %v", l7RuleReadyPollInterval, elapsed)
				}
				assertL7RuleErrorDiagnostic(t, deleteResp.Diagnostics, "Error waiting for Load Balancer L7 Rule deletion", "deletion failed with provisioning_status="+status)
				if !deleteResp.State.Raw.Equal(state.Raw) {
					t.Fatal("a failed delete must keep the state")
				}
			})
		})
	}
}

// The GET of an L7 rule that is in ERROR before the DELETE never shows
// PENDING_DELETE, so ERROR after the DELETE cannot prove a failed deletion.
// Delete must wait until l7RuleReadyTimeout, return the timeout, and keep state.
func TestL7RuleResourceDeleteFromErrorWaitsUntilTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		deleted := false
		resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodDelete:
				deleted = true
				return l7RuleJSONResponse(http.StatusAccepted, ""), nil
			case http.MethodGet:
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ERROR", "ERROR")), nil
			}
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		})

		s := l7RuleResourceSchema(t)
		state := tfsdk.State{Schema: s}
		model := L7RuleResourceModel{
			L7RuleModel: L7RuleModel{ID: types.StringValue(l7RuleTestRuleID.String())},
			L7Policy:    reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
		}
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		start := time.Now()
		deleteResp := resource.DeleteResponse{State: state}
		resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
		if elapsed := time.Since(start); elapsed != l7RuleReadyTimeout {
			t.Fatalf("expected the delete to wait %v, got %v", l7RuleReadyTimeout, elapsed)
		}
		if !deleted {
			t.Fatal("expected DeleteL7rule to be called")
		}
		assertL7RuleErrorDiagnostic(t, deleteResp.Diagnostics, "Error waiting for Load Balancer L7 Rule deletion", "provisioning_status=ERROR")
		if detail := deleteResp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, wait.ErrTimeout.Error()) {
			t.Fatalf("expected a timeout, got %q", detail)
		}
		if deleteResp.State.Raw.IsNull() {
			t.Fatal("expected Delete to keep the state")
		}
	})
}

func TestL7RuleResourceImportState(t *testing.T) {
	t.Parallel()

	resourceUnderTest := NewL7RuleResource().(*L7RuleResource)
	s := l7RuleResourceSchema(t)

	t.Run("valid import ID", func(t *testing.T) {
		t.Parallel()
		var resp resource.ImportStateResponse
		resp.State = tfsdk.State{Schema: s}
		if diags := resp.State.Set(context.Background(), &L7RuleResourceModel{L7Policy: types.ObjectNull(reference.AttributeTypes(reference.ID))}); diags.HasError() {
			t.Fatalf("initialize import state: %v", diags)
		}
		importID := fmt.Sprintf("%s/%s", l7RuleTestPolicyID, l7RuleTestRuleID)
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		var policyIDVal, ruleIDVal types.String
		_ = resp.State.GetAttribute(context.Background(), path.Root("l7_policy").AtName(reference.ID), &policyIDVal)
		_ = resp.State.GetAttribute(context.Background(), path.Root("id"), &ruleIDVal)

		if policyIDVal.ValueString() != l7RuleTestPolicyID.String() {
			t.Fatalf("expected l7_policy.id %s, got %s", l7RuleTestPolicyID, policyIDVal.ValueString())
		}
		if ruleIDVal.ValueString() != l7RuleTestRuleID.String() {
			t.Fatalf("expected id %s, got %s", l7RuleTestRuleID, ruleIDVal.ValueString())
		}
	})

	t.Run("invalid format missing slash", func(t *testing.T) {
		t.Parallel()
		var resp resource.ImportStateResponse
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: l7RuleTestRuleID.String()}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error for missing slash in import ID")
		}
	})

	t.Run("invalid format invalid UUID", func(t *testing.T) {
		t.Parallel()
		var resp resource.ImportStateResponse
		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: "not-a-uuid/also-not"}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error for invalid UUID in import ID")
		}
	})
}

func TestL7RuleResourceWaitUntilReadyTransitions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var polls int
		resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
			polls++
			if polls < 3 {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_UPDATE", "ONLINE")), nil
			}
			return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ACTIVE", "ONLINE")), nil
		})

		start := time.Now()
		rule, err := resourceUnderTest.waitUntilReady(context.Background(), l7RuleTestPolicyID, l7RuleTestRuleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
		if err != nil {
			t.Fatalf("unexpected wait error: %v", err)
		}
		if rule.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE {
			t.Fatalf("expected ACTIVE status, got %s", rule.ProvisioningStatus)
		}
		elapsed := time.Since(start)
		if elapsed < 2*l7RuleReadyPollInterval {
			t.Fatalf("expected at least 2 poll intervals (%v), got %v", 2*l7RuleReadyPollInterval, elapsed)
		}
	})
}

// Each terminal answer must end waitUntilReady at the poll that sees it, one
// interval after the pending poll, instead of at l7RuleReadyTimeout.
func TestL7RuleResourceWaitUntilReadyTerminalStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal func() *http.Response
		wantErr  string
	}{
		{
			name: "ERROR",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ERROR", "ERROR"))
			},
			wantErr: "ERROR provisioning status",
		},
		{
			name: "UNKNOWN",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "UNKNOWN", "OFFLINE"))
			},
			wantErr: "UNKNOWN provisioning status",
		},
		{
			name: "DELETED",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "DELETED", "OFFLINE"))
			},
			wantErr: "unexpectedly deleted",
		},
		{
			name: "unrecognized status",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "SUSPENDED", "OFFLINE"))
			},
			wantErr: "unrecognized provisioning_status=SUSPENDED",
		},
		{
			name: "SDK error",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusInternalServerError, `{"errors":[{"message":"internal error"}]}`)
			},
			wantErr: "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var polls int
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					polls++
					if polls == 1 {
						return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_UPDATE", "ONLINE")), nil
					}
					return tt.terminal(), nil
				})

				start := time.Now()
				_, err := resourceUnderTest.waitUntilReady(context.Background(), l7RuleTestPolicyID, l7RuleTestRuleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
				assertL7RuleWaiterStoppedAtTerminal(t, err, tt.wantErr, polls, time.Since(start))
			})
		})
	}
}

// A Delete can start from ERROR or UNKNOWN, so waitUntilDeleted keeps
// polling through either state until it has seen PENDING_DELETE.
func TestL7RuleResourceWaitUntilDeletedTransitions(t *testing.T) {
	t.Parallel()

	for _, statuses := range [][]string{
		{"PENDING_DELETE"},
		{"ERROR", "PENDING_DELETE"},
		{"UNKNOWN", "ACTIVE"},
	} {
		t.Run(strings.Join(statuses, " "), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var polls int
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					polls++
					if polls <= len(statuses) {
						return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, statuses[polls-1], "OFFLINE")), nil
					}
					return l7RuleJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
				})

				start := time.Now()
				err := resourceUnderTest.waitUntilDeleted(context.Background(), l7RuleTestPolicyID, l7RuleTestRuleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
				if err != nil {
					t.Fatalf("unexpected wait error: %v", err)
				}
				if elapsed, want := time.Since(start), time.Duration(len(statuses))*l7RuleReadyPollInterval; elapsed != want {
					t.Fatalf("expected the wait to end after %v, got %v", want, elapsed)
				}
			})
		})
	}
}

// Each terminal answer must end waitUntilDeleted at the poll that sees it, one
// interval after the pending poll, instead of at l7RuleReadyTimeout.
func TestL7RuleResourceWaitUntilDeletedTerminalStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal func() *http.Response
		wantErr  string
	}{
		{
			name: "ERROR",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "ERROR", "ERROR"))
			},
			wantErr: "deletion failed with provisioning_status=ERROR",
		},
		{
			name: "UNKNOWN",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "UNKNOWN", "OFFLINE"))
			},
			wantErr: "deletion failed with provisioning_status=UNKNOWN",
		},
		{
			name: "unrecognized status",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "SUSPENDED", "OFFLINE"))
			},
			wantErr: "deletion failed with provisioning_status=SUSPENDED",
		},
		{
			name: "SDK error",
			terminal: func() *http.Response {
				return l7RuleJSONResponse(http.StatusInternalServerError, `{"errors":[{"message":"internal error"}]}`)
			},
			wantErr: "internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var polls int
				resourceUnderTest := newL7RuleTestResource(t, func(req *http.Request) (*http.Response, error) {
					polls++
					if polls == 1 {
						return l7RuleJSONResponse(http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_DELETE", "OFFLINE")), nil
					}
					return tt.terminal(), nil
				})

				start := time.Now()
				err := resourceUnderTest.waitUntilDeleted(context.Background(), l7RuleTestPolicyID, l7RuleTestRuleID, l7RuleReadyTimeout, l7RuleReadyPollInterval)
				assertL7RuleWaiterStoppedAtTerminal(t, err, tt.wantErr, polls, time.Since(start))
			})
		})
	}
}

// executeL7RuleMutation runs the Framework entry point that sends method:
// Create for POST, Update for PATCH, and Delete for DELETE.
func executeL7RuleMutation(t *testing.T, r *L7RuleResource, method string) diag.Diagnostics {
	t.Helper()
	s := l7RuleResourceSchema(t)
	stateModel := L7RuleResourceModel{
		L7RuleModel: L7RuleModel{
			ID:                 types.StringValue(l7RuleTestRuleID.String()),
			Type:               types.StringValue("PATH"),
			CompareType:        types.StringValue("STARTS_WITH"),
			Value:              types.StringValue("/api"),
			Key:                types.StringNull(),
			Invert:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		L7Policy: reference.Value(reference.ID, types.StringValue(l7RuleTestPolicyID.String())),
	}
	state := tfsdk.State{Schema: s}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	switch method {
	case http.MethodPost:
		planModel := L7RuleResourceModel{L7RuleModel: L7RuleModel{
			Type:        stateModel.Type,
			CompareType: stateModel.CompareType,
			Value:       stateModel.Value,
			Key:         types.StringNull(),
			Invert:      types.BoolValue(false),
		}, L7Policy: stateModel.L7Policy}
		plan := tfsdk.Plan{Schema: s}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		response := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
		return response.Diagnostics
	case http.MethodPatch:
		planModel := stateModel
		planModel.Value = types.StringValue("/v2")
		plan := tfsdk.Plan{Schema: s}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		response := resource.UpdateResponse{State: state}
		r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
		return response.Diagnostics
	default:
		response := resource.DeleteResponse{State: state}
		r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
		return response.Diagnostics
	}
}

// l7RuleTestMutations lists the request method, the error summary, and the
// operation subject of each L7 rule operation that the backend can refuse as
// busy.
var l7RuleTestMutations = []struct {
	method      string
	wantSummary string
	wantSubject string
}{
	{method: http.MethodPost, wantSummary: "Error creating Load Balancer L7 Rule", wantSubject: "the creation of an L7 rule under L7 policy " + l7RuleTestPolicyID.String()},
	{method: http.MethodPatch, wantSummary: "Error updating Load Balancer L7 Rule", wantSubject: "the update of L7 rule " + l7RuleTestRuleID.String()},
	{method: http.MethodDelete, wantSummary: "Error deleting Load Balancer L7 Rule", wantSubject: "the deletion of L7 rule " + l7RuleTestRuleID.String()},
}

// l7RuleTestBusyAPI is a fake API for one L7 rule mutation. The root Load
// Balancer is PENDING_UPDATE on the first GET of each idle wait, and answers
// holds the status and body of each mutation request in order.
type l7RuleTestBusyAPI struct {
	method       string
	answers      []l7RuleTestAnswer
	requestTimes []time.Time
	rootGets     int
	ruleGone     bool
}

// l7RuleTestAnswer is one HTTP answer of the fake API.
type l7RuleTestAnswer struct {
	status int
	body   string
}

func (a *l7RuleTestBusyAPI) parents(kind string) loadbalancersdk.LoadBalancingProvisioningStatus {
	if kind != "load balancer" {
		return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	}
	a.rootGets++
	if a.rootGets%2 == 1 {
		return loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
	}
	return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
}

func (a *l7RuleTestBusyAPI) roundTrip(t *testing.T) l7RuleRoundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		if req.Method == a.method {
			a.requestTimes = append(a.requestTimes, time.Now())
			if len(a.requestTimes) > len(a.answers) {
				t.Fatalf("unexpected %s request %d", req.Method, len(a.requestTimes))
			}
			answer := a.answers[len(a.requestTimes)-1]
			if a.method == http.MethodDelete && answer.status == http.StatusAccepted {
				a.ruleGone = true
			}
			return l7RuleTestHTTPResponse(req, answer.status, answer.body), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/l7rules/%s/", l7RuleTestRuleID)) {
			if a.ruleGone {
				return l7RuleTestHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"Not found."}]}`), nil
			}
			return l7RuleTestHTTPResponse(req, http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/v2", nil, false, "ACTIVE", "ONLINE")), nil
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		return nil, nil
	}
}

// l7RuleTestSuccess is the success answer of a mutation request.
func l7RuleTestSuccess(method string) l7RuleTestAnswer {
	switch method {
	case http.MethodPost:
		return l7RuleTestAnswer{http.StatusCreated, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, "PENDING_CREATE", "OFFLINE")}
	case http.MethodPatch:
		return l7RuleTestAnswer{http.StatusAccepted, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/v2", nil, false, "PENDING_UPDATE", "ONLINE")}
	default:
		return l7RuleTestAnswer{http.StatusAccepted, ""}
	}
}

// Each mutation waits for the idle state and sends its request once. A busy
// refusal returns at once, with no second idle wait and no second request,
// and the diagnostic names the refused operation and includes the refusal.
func TestL7RuleResourceReturnsABusyRefusalAfterTheIdleWait(t *testing.T) {
	t.Parallel()

	for _, tt := range l7RuleTestMutations {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7RuleTestBusyAPI{method: tt.method, answers: []l7RuleTestAnswer{
					{http.StatusConflict, l7RuleTestRefusal(resourceBusyErrorCode)},
				}}
				resourceUnderTest := newL7RuleTestResourceWithParents(t, api.parents, api.roundTrip(t))

				start := time.Now()
				diagnostics := executeL7RuleMutation(t, resourceUnderTest, tt.method)
				if len(api.requestTimes) != 1 {
					t.Fatalf("%s calls=%d, want 1", tt.method, len(api.requestTimes))
				}
				if got := api.requestTimes[0].Sub(start); got != l7RuleReadyPollInterval {
					t.Fatalf("%s after %v, want it after one idle poll of %v", tt.method, got, l7RuleReadyPollInterval)
				}
				if elapsed := time.Since(start); elapsed != l7RuleReadyPollInterval {
					t.Fatalf("expected the busy refusal to return at once, got %v", elapsed)
				}
				if api.rootGets != 2 {
					t.Fatalf("load balancer GETs=%d, want the 2 of the one idle wait", api.rootGets)
				}
				assertL7RuleErrorDiagnostic(t, diagnostics, tt.wantSummary, tt.wantSubject+" was refused as busy, because another operation holds the claim: ")
				if detail := diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "("+resourceBusyErrorCode+")") {
					t.Fatalf("expected the refusal with code %s in the diagnostic, got %q", resourceBusyErrorCode, detail)
				}
			})
		})
	}
}

// A 409 with another code, any other status with the resource_busy code, and
// a 5xx return after the one request, without the busy refusal wording.
func TestL7RuleResourceReturnsOtherRefusalsAtOnce(t *testing.T) {
	t.Parallel()

	answers := []struct {
		name   string
		answer l7RuleTestAnswer
	}{
		{name: "409 conflict", answer: l7RuleTestAnswer{http.StatusConflict, l7RuleTestRefusal("conflict")}},
		{name: "400 resource_busy", answer: l7RuleTestAnswer{http.StatusBadRequest, l7RuleTestRefusal(resourceBusyErrorCode)}},
		{name: "500 resource_busy", answer: l7RuleTestAnswer{http.StatusInternalServerError, l7RuleTestRefusal(resourceBusyErrorCode)}},
		{name: "503", answer: l7RuleTestAnswer{http.StatusServiceUnavailable, `{"errors":[{"code":"unavailable","message":"Service unavailable."}]}`}},
	}
	for _, mutation := range l7RuleTestMutations {
		for _, tt := range answers {
			t.Run(mutation.method+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					api := &l7RuleTestBusyAPI{method: mutation.method, answers: []l7RuleTestAnswer{tt.answer}}
					resourceUnderTest := newL7RuleTestResourceWithParents(t, api.parents, api.roundTrip(t))

					diagnostics := executeL7RuleMutation(t, resourceUnderTest, mutation.method)
					if diagnostics.ErrorsCount() != 1 || diagnostics.Errors()[0].Summary() != mutation.wantSummary {
						t.Fatalf("expected one %q diagnostic, got %v", mutation.wantSummary, diagnostics)
					}
					if len(api.requestTimes) != 1 {
						t.Fatalf("%s calls=%d, want 1", mutation.method, len(api.requestTimes))
					}
					if detail := diagnostics.Errors()[0].Detail(); strings.Contains(detail, "refused as busy") {
						t.Fatalf("expected the error unchanged, got %q", detail)
					}
				})
			})
		}
	}
}

// The idle wait fails at once, before any request, when an object that the
// operation claims is in ERROR or UNKNOWN. A Delete counts an L7 rule in ERROR
// or UNKNOWN as idle, and sends the DELETE.
func TestL7RuleResourceIdleWaitStopsOnStuckObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      string
		parent      string
		ruleStatus  string
		status      loadbalancersdk.LoadBalancingProvisioningStatus
		wantDetail  string
		wantRequest bool
	}{
		{name: "create with UNKNOWN load balancer", method: http.MethodPost, parent: "load balancer", status: loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN, wantDetail: "load balancer 04000000-0000-0000-0000-000000000000 has terminal provisioning_status=UNKNOWN"},
		{name: "create with ERROR L7 policy", method: http.MethodPost, parent: "L7 policy", status: loadbalancersdk.LoadBalancingProvisioningStatusERROR, wantDetail: "L7 policy 01000000-0000-0000-0000-000000000000 has terminal provisioning_status=ERROR"},
		{name: "update of an UNKNOWN rule", method: http.MethodPatch, ruleStatus: "UNKNOWN", wantDetail: "L7 rule 02000000-0000-0000-0000-000000000000 has terminal provisioning_status=UNKNOWN"},
		{name: "update with UNKNOWN listener", method: http.MethodPatch, parent: "listener", status: loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN, wantDetail: "listener 03000000-0000-0000-0000-000000000000 has terminal provisioning_status=UNKNOWN"},
		{name: "delete with UNKNOWN load balancer", method: http.MethodDelete, parent: "load balancer", status: loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN, wantDetail: "load balancer 04000000-0000-0000-0000-000000000000 has terminal provisioning_status=UNKNOWN"},
		{name: "delete of an UNKNOWN rule", method: http.MethodDelete, ruleStatus: "UNKNOWN", wantRequest: true},
		{name: "delete of an ERROR rule", method: http.MethodDelete, ruleStatus: "ERROR", wantRequest: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ruleStatus := tt.ruleStatus
				if ruleStatus == "" {
					ruleStatus = "ACTIVE"
				}
				requests := 0
				parents := func(kind string) loadbalancersdk.LoadBalancingProvisioningStatus {
					if kind == tt.parent {
						return tt.status
					}
					return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
				}
				resourceUnderTest := newL7RuleTestResourceWithParents(t, parents, func(req *http.Request) (*http.Response, error) {
					if req.Method == tt.method {
						requests++
						return l7RuleTestHTTPResponse(req, http.StatusAccepted, ""), nil
					}
					if requests > 0 {
						return l7RuleTestHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"Not found."}]}`), nil
					}
					return l7RuleTestHTTPResponse(req, http.StatusOK, sampleL7RuleSchemaJSON(l7RuleTestRuleID, l7RuleTestPolicyID, "PATH", "STARTS_WITH", "/api", nil, false, ruleStatus, "ONLINE")), nil
				})

				start := time.Now()
				diagnostics := executeL7RuleMutation(t, resourceUnderTest, tt.method)
				if tt.wantRequest {
					if diagnostics.HasError() || requests != 1 {
						t.Fatalf("expected one %s and no diagnostic, got calls=%d %v", tt.method, requests, diagnostics)
					}
					return
				}
				if elapsed := time.Since(start); elapsed != 0 {
					t.Fatalf("expected the idle wait to stop at once, got %v", elapsed)
				}
				if requests != 0 {
					t.Fatalf("%s calls=%d, want none", tt.method, requests)
				}
				if diagnostics.ErrorsCount() != 1 || !strings.Contains(diagnostics.Errors()[0].Detail(), tt.wantDetail) {
					t.Fatalf("expected a diagnostic containing %q, got %v", tt.wantDetail, diagnostics)
				}
			})
		})
	}
}

// Each mutation holds the claims lock of the root Load Balancer, so it sends
// no request while another operation holds that lock.
func TestL7RuleResourceOperationsHoldTheClaimsLock(t *testing.T) {
	t.Parallel()

	for _, tt := range l7RuleTestMutations {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				api := &l7RuleTestBusyAPI{method: tt.method, answers: []l7RuleTestAnswer{l7RuleTestSuccess(tt.method)}}
				resourceUnderTest := newL7RuleTestResourceWithParents(t, l7RuleActiveParents, api.roundTrip(t))
				unlock, err := resourceUnderTest.claims.Lock(context.Background(), l7RuleTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("lock: %v", err)
				}

				done := make(chan diag.Diagnostics)
				go func() { done <- executeL7RuleMutation(t, resourceUnderTest, tt.method) }()
				synctest.Wait()
				if len(api.requestTimes) != 0 {
					t.Fatalf("%s sent while the claims lock was held", tt.method)
				}
				unlock()
				if diagnostics := <-done; diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diagnostics)
				}
				if len(api.requestTimes) != 1 {
					t.Fatalf("%s calls=%d, want 1", tt.method, len(api.requestTimes))
				}
			})
		})
	}
}

func assertL7RuleWaiterStoppedAtTerminal(t *testing.T, err error, wantErr string, polls int, elapsed time.Duration) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a terminal error")
	}
	if errors.Is(err, wait.ErrTimeout) {
		t.Fatalf("expected a terminal error, got a timeout: %v", err)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("expected error containing %q, got %v", wantErr, err)
	}
	if polls != 2 {
		t.Fatalf("expected 2 polls, got %d", polls)
	}
	if elapsed != l7RuleReadyPollInterval {
		t.Fatalf("expected the waiter to stop after %v, got %v", l7RuleReadyPollInterval, elapsed)
	}
}

func assertL7RuleErrorDiagnostic(t *testing.T, diagnostics diag.Diagnostics, wantSummary, wantDetail string) {
	t.Helper()
	if diagnostics.ErrorsCount() != 1 {
		t.Fatalf("expected one error diagnostic, got %v", diagnostics)
	}
	got := diagnostics.Errors()[0]
	if got.Summary() != wantSummary || !strings.Contains(got.Detail(), wantDetail) {
		t.Fatalf("expected %q with detail containing %q, got %q: %q", wantSummary, wantDetail, got.Summary(), got.Detail())
	}
}
