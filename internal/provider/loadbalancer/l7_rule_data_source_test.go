package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func l7RuleDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	(&L7RuleDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema error: %v", resp.Diagnostics)
	}
	return resp.Schema
}

func l7RuleDataSourceConfig(t *testing.T, s datasourceschema.Schema, model L7RuleDataSourceModel) tfsdk.Config {
	t.Helper()
	state := tfsdk.State{Schema: s}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set l7 rule data source config state: %v", diags)
	}
	return tfsdk.Config{Schema: s, Raw: state.Raw}
}

func TestL7RuleDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	s := l7RuleDataSourceSchema(t)
	state := tfsdk.State{Schema: s}
	model := L7RuleDataSourceModel{
		L7RuleModel: L7RuleModel{
			ID:                 types.StringNull(),
			Type:               types.StringNull(),
			CompareType:        types.StringNull(),
			Value:              types.StringNull(),
			Key:                types.StringNull(),
			Invert:             types.BoolNull(),
			ProvisioningStatus: types.StringNull(),
			Status:             types.StringNull(),
			CreatedAt:          types.StringNull(),
			UpdatedAt:          types.StringNull(),
		},
		L7PolicyID: types.StringNull(),
	}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestL7RuleDataSourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	d := NewL7RuleDataSource()
	if d == nil {
		t.Fatal("expected non-nil data source")
	}

	var metadataResp datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadataResp)
	if metadataResp.TypeName != "viettelcloud_lb_l7_rule" {
		t.Fatalf("expected viettelcloud_lb_l7_rule, got %s", metadataResp.TypeName)
	}

	ds := &L7RuleDataSource{}
	var nilResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResp)
	if nilResp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResp.Diagnostics)
	}

	var invalidResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &invalidResp)
	if !invalidResp.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	client := newL7RuleTestClient(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleTestHTTPResponse(req, http.StatusOK, "{}"), nil
	})
	var validResp datasource.ConfigureResponse
	ds.Configure(context.Background(), datasource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			LoadBalancer: client,
			ProjectID:    l7RuleTestProjectID,
		},
	}, &validResp)
	if validResp.Diagnostics.HasError() || ds.client != client || ds.projectID != l7RuleTestProjectID {
		t.Fatalf("unexpected configured data source: %#v diags=%v", ds, validResp.Diagnostics)
	}
}

func TestL7RuleDataSourceSchema(t *testing.T) {
	t.Parallel()

	s := l7RuleDataSourceSchema(t)

	policyIDAttr, ok := s.Attributes["l7_policy_id"].(datasourceschema.StringAttribute)
	if !ok || !policyIDAttr.Required || policyIDAttr.Computed {
		t.Fatalf("expected l7_policy_id to be required and not computed, got %#v", s.Attributes["l7_policy_id"])
	}

	idAttr, ok := s.Attributes["id"].(datasourceschema.StringAttribute)
	if !ok || !idAttr.Optional || !idAttr.Computed {
		t.Fatalf("expected id to be optional and computed, got %#v", s.Attributes["id"])
	}

	for _, name := range []string{"type", "compare_type", "value", "key", "provisioning_status", "status", "created_at", "updated_at"} {
		attr, ok := s.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Fatalf("expected %s to be computed only, got %#v", name, s.Attributes[name])
		}
	}

	invertAttr, ok := s.Attributes["invert"].(datasourceschema.BoolAttribute)
	if !ok || !invertAttr.Computed || invertAttr.Required || invertAttr.Optional {
		t.Fatalf("expected invert to be computed only, got %#v", s.Attributes["invert"])
	}
}

func TestL7RuleDataSourceGetL7RuleValidation(t *testing.T) {
	t.Parallel()

	ds := &L7RuleDataSource{}

	t.Run("invalid l7_policy_id UUID", func(t *testing.T) {
		t.Parallel()
		config := L7RuleDataSourceModel{
			L7RuleModel: L7RuleModel{
				ID: types.StringValue(l7RuleTestRuleID.String()),
			},
			L7PolicyID: types.StringValue("invalid-uuid"),
		}
		_, diags := ds.getL7Rule(context.Background(), config)
		if !diags.HasError() {
			t.Fatal("expected error for invalid policy id")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		t.Parallel()
		config := L7RuleDataSourceModel{
			L7RuleModel: L7RuleModel{
				ID: types.StringNull(),
			},
			L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
		}
		_, diags := ds.getL7Rule(context.Background(), config)
		if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "Missing Load Balancer L7 Rule lookup criteria") {
			t.Fatalf("expected missing criteria diagnostic, got %v", diags)
		}
	})

	t.Run("empty id", func(t *testing.T) {
		t.Parallel()
		config := L7RuleDataSourceModel{
			L7RuleModel: L7RuleModel{
				ID: types.StringValue("   "),
			},
			L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
		}
		_, diags := ds.getL7Rule(context.Background(), config)
		if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), "Missing Load Balancer L7 Rule lookup criteria") {
			t.Fatalf("expected missing criteria diagnostic, got %v", diags)
		}
	})

	t.Run("invalid id UUID", func(t *testing.T) {
		t.Parallel()
		config := L7RuleDataSourceModel{
			L7RuleModel: L7RuleModel{
				ID: types.StringValue("not-a-uuid"),
			},
			L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
		}
		_, diags := ds.getL7Rule(context.Background(), config)
		if !diags.HasError() {
			t.Fatal("expected error for invalid id UUID")
		}
	})
}

func TestL7RuleDataSourceReadSuccess(t *testing.T) {
	t.Parallel()

	rule := sampleL7RuleSchema()
	keyVal := "X-Header"
	rule.Key = core.NullableFromPtr(&keyVal)
	rule.Type = loadbalancersdk.L7RuleTypeHEADER

	ds := newL7RuleTestDataSource(t, func(req *http.Request) (*http.Response, error) {
		expectedPath := fmt.Sprintf("/v2/load-balancing/l7policies/%s/l7rules/%s/", l7RuleTestPolicyID, l7RuleTestRuleID)
		if req.Method == http.MethodGet && req.URL.Path == expectedPath {
			return l7RuleTestHTTPResponse(req, http.StatusOK, l7RuleJSON(rule)), nil
		}
		return l7RuleTestHTTPResponse(req, http.StatusNotFound, `{"error":"not found"}`), nil
	})

	s := l7RuleDataSourceSchema(t)
	configModel := L7RuleDataSourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
	}

	config := l7RuleDataSourceConfig(t, s, configModel)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: s}

	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var stateModel L7RuleDataSourceModel
	if diags := resp.State.Get(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}

	if stateModel.ID.ValueString() != l7RuleTestRuleID.String() {
		t.Fatalf("expected id %s, got %s", l7RuleTestRuleID, stateModel.ID.ValueString())
	}
	if stateModel.L7PolicyID.ValueString() != l7RuleTestPolicyID.String() {
		t.Fatalf("expected l7_policy_id %s, got %s", l7RuleTestPolicyID, stateModel.L7PolicyID.ValueString())
	}
	if stateModel.Type.ValueString() != string(loadbalancersdk.L7RuleTypeHEADER) {
		t.Fatalf("expected type %s, got %s", loadbalancersdk.L7RuleTypeHEADER, stateModel.Type.ValueString())
	}
	if stateModel.CompareType.ValueString() != string(loadbalancersdk.L7RuleCompareTypeSTARTSWITH) {
		t.Fatalf("expected compare_type %s, got %s", loadbalancersdk.L7RuleCompareTypeSTARTSWITH, stateModel.CompareType.ValueString())
	}
	if stateModel.Value.ValueString() != "/api" {
		t.Fatalf("expected value /api, got %s", stateModel.Value.ValueString())
	}
	if stateModel.Key.ValueString() != "X-Header" {
		t.Fatalf("expected key X-Header, got %s", stateModel.Key.ValueString())
	}
	if stateModel.Invert.ValueBool() != false {
		t.Fatalf("expected invert false, got %v", stateModel.Invert.ValueBool())
	}
	if stateModel.ProvisioningStatus.ValueString() != string(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE) {
		t.Fatalf("expected provisioning_status ACTIVE, got %s", stateModel.ProvisioningStatus.ValueString())
	}
	if stateModel.Status.ValueString() != string(loadbalancersdk.LoadBalancingStatusONLINE) {
		t.Fatalf("expected status ONLINE, got %s", stateModel.Status.ValueString())
	}
	if stateModel.CreatedAt.ValueString() != rule.CreatedAt.Format(time.RFC3339) {
		t.Fatalf("expected created_at %s, got %s", rule.CreatedAt.Format(time.RFC3339), stateModel.CreatedAt.ValueString())
	}
	if stateModel.UpdatedAt.ValueString() != rule.UpdatedAt.Format(time.RFC3339) {
		t.Fatalf("expected updated_at %s, got %s", rule.UpdatedAt.Format(time.RFC3339), stateModel.UpdatedAt.ValueString())
	}
}

func TestL7RuleDataSourceReadNotFound(t *testing.T) {
	t.Parallel()

	ds := newL7RuleTestDataSource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleTestHTTPResponse(req, http.StatusNotFound, `{"error":"not found"}`), nil
	})

	s := l7RuleDataSourceSchema(t)
	configModel := L7RuleDataSourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
	}
	config := l7RuleDataSourceConfig(t, s, configModel)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: s}

	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic for not found rule")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "L7 Rule not found") {
		t.Fatalf("expected 'L7 Rule not found' summary, got %v", resp.Diagnostics)
	}
}

func TestL7RuleDataSourceReadDeletedStatus(t *testing.T) {
	t.Parallel()

	deletedRule := sampleL7RuleSchema()
	deletedRule.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED

	ds := newL7RuleTestDataSource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleTestHTTPResponse(req, http.StatusOK, l7RuleJSON(deletedRule)), nil
	})

	s := l7RuleDataSourceSchema(t)
	configModel := L7RuleDataSourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
	}
	config := l7RuleDataSourceConfig(t, s, configModel)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: s}

	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic for DELETED rule")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "has been deleted") {
		t.Fatalf("expected 'has been deleted' detail, got %v", resp.Diagnostics)
	}
}

func TestL7RuleDataSourceReadAPIError(t *testing.T) {
	t.Parallel()

	ds := newL7RuleTestDataSource(t, func(req *http.Request) (*http.Response, error) {
		return l7RuleTestHTTPResponse(req, http.StatusInternalServerError, `{"error":"internal error"}`), nil
	})

	s := l7RuleDataSourceSchema(t)
	configModel := L7RuleDataSourceModel{
		L7RuleModel: L7RuleModel{
			ID: types.StringValue(l7RuleTestRuleID.String()),
		},
		L7PolicyID: types.StringValue(l7RuleTestPolicyID.String()),
	}
	config := l7RuleDataSourceConfig(t, s, configModel)

	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: s}

	ds.Read(context.Background(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic on API error")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Summary(), "Error reading Load Balancer L7 Rule") {
		t.Fatalf("expected 'Error reading Load Balancer L7 Rule' summary, got %v", resp.Diagnostics)
	}
}
