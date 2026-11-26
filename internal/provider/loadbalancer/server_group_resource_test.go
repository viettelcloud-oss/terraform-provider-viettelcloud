package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

func newServerGroupTestResource(t *testing.T, transport http.RoundTripper) *ServerGroupResource {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	client, err := loadbalancersdk.NewClient(
		"https://load-balancer.test",
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return &ServerGroupResource{
		client:    client,
		projectID: serverGroupTestProjectID,
		claims:    keylock.New(),
	}
}

// serverGroupBusyCode is the error code of a busy refusal: the request met the
// claim of another operation.
const serverGroupBusyCode = "resource_busy"

// serverGroupBusyRefusalBody is the body of a busy refusal, with HTTP 409.
const serverGroupBusyRefusalBody = `{"errors":[{"location":"","code":"resource_busy","message":"load balancer is busy, please try again later."}],"request_id":"0a1b2c"}`

var (
	serverGroupParentPath = fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/", serverGroupTestLBID)
	serverGroupPath       = fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID, serverGroupTestID)
)

// serverGroupParentJSON is the parent Load Balancer in provisioning status.
func serverGroupParentJSON(t *testing.T, status loadbalancersdk.LoadBalancingProvisioningStatus) string {
	t.Helper()
	return loadBalancerJSON(t, &loadbalancersdk.LoadBalancerDetailSchema{Id: serverGroupTestLBID, ProvisioningStatus: status})
}

// newServerGroupMutationTestResource is newServerGroupTestResource with a
// parent Load Balancer that is always ACTIVE, so the idle wait before each
// request passes on its first poll and transport sees every other request.
func newServerGroupMutationTestResource(t *testing.T, transport http.RoundTripper) *ServerGroupResource {
	t.Helper()
	return newServerGroupTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == serverGroupParentPath {
			return serverGroupHTTPResponse(req, http.StatusOK, serverGroupParentJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		}
		return transport.RoundTrip(req)
	}))
}

func serverGroupSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	(&ServerGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

func serverGroupConfiguredModel() ServerGroupResourceModel {
	spVal, _ := types.ObjectValue(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type":        types.StringValue("NONE"),
		"cookie_name": types.StringNull(),
	})
	return ServerGroupResourceModel{
		ServerGroupModel: ServerGroupModel{
			ID:                 types.StringValue(serverGroupTestID.String()),
			Name:               types.StringValue("test-group"),
			Protocol:           types.StringValue("HTTP"),
			Algorithm:          types.StringValue("ROUND_ROBIN"),
			Description:        types.StringValue("description"),
			EnableH2:           types.BoolValue(false),
			SessionPersistence: spVal,
			HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			MemberCount:        types.Int64Value(0),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		LoadBalancer: reference.Value(reference.ID, types.StringValue(serverGroupTestLBID.String())),
	}
}

func serverGroupTCPHealthCheck() types.Object {
	return types.ObjectValueMust(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
		"monitor_type":     types.StringValue("TCP"),
		"delay":            types.Int64Value(15),
		"timeout":          types.Int64Value(5),
		"max_retries":      types.Int64Value(3),
		"max_retries_down": types.Int64Value(3),
		"path":             types.StringNull(),
		"http_method":      types.StringNull(),
		"expected_codes":   types.StringNull(),
	})
}

func planForServerGroup(t *testing.T, s schema.Schema, model ServerGroupResourceModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: s}
	if diags := p.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return p
}

func stateForServerGroup(t *testing.T, s schema.Schema, model ServerGroupResourceModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: s}
	if diags := st.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return st
}

func TestServerGroupModelMatchesSchema(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupSchema(t)
	plan := tfsdk.Plan{Schema: resSchema}
	model := serverGroupConfiguredModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestServerGroupConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceUnderTest, ok := NewServerGroupResource().(*ServerGroupResource)
	if !ok || resourceUnderTest == nil {
		t.Fatal("constructor did not return a ServerGroupResource")
	}

	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_server_group" {
		t.Fatalf("metadata type=%q, want viettelcloud_lb_server_group", metadata.TypeName)
	}

	var response resource.ConfigureResponse
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unexpected provider data diagnostic")
	}

	clientResource := newServerGroupTestResource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}))
	configured := &providerdata.Configured{
		LoadBalancer: clientResource.client,
		ProjectID:    serverGroupTestProjectID,
		Claims:       keylock.New(),
	}
	response = resource.ConfigureResponse{}
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &response)
	if response.Diagnostics.HasError() || resourceUnderTest.client != configured.LoadBalancer || resourceUnderTest.projectID != configured.ProjectID || resourceUnderTest.claims != configured.Claims {
		t.Fatalf("resource was not configured: diagnostics=%v resource=%#v", response.Diagnostics, resourceUnderTest)
	}
}

func TestServerGroupSchemaContract(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupSchema(t)

	lbAttr, ok := resSchema.Attributes["load_balancer"].(schema.SingleNestedAttribute)
	if !ok || !lbAttr.Required || lbAttr.Computed || len(lbAttr.PlanModifiers) != 1 {
		t.Errorf("load_balancer must be required and RequiresReplace, got %#v", resSchema.Attributes["load_balancer"])
	}
	if lbIDAttr, ok := lbAttr.Attributes["id"].(schema.StringAttribute); !ok || !lbIDAttr.Required || len(lbIDAttr.PlanModifiers) != 0 {
		t.Errorf("load_balancer.id must be a required criterion without plan modifiers, got %#v", lbAttr.Attributes["id"])
	}

	protoAttr, ok := resSchema.Attributes["protocol"].(schema.StringAttribute)
	if !ok || !protoAttr.Required || len(protoAttr.PlanModifiers) != 1 {
		t.Errorf("protocol must be required and RequiresReplace, got %#v", resSchema.Attributes["protocol"])
	}

	for _, name := range []string{"name", "algorithm"} {
		attr, ok := resSchema.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Required || attr.Computed || len(attr.PlanModifiers) != 0 {
			t.Errorf("%s must be required and updateable, got %#v", name, resSchema.Attributes[name])
		}
	}

	idAttr, ok := resSchema.Attributes["id"].(schema.StringAttribute)
	if !ok || !idAttr.Computed || idAttr.Required || len(idAttr.PlanModifiers) != 1 {
		t.Errorf("id must be computed and UseStateForUnknown, got %#v", resSchema.Attributes["id"])
	}

	descAttr, ok := resSchema.Attributes["description"].(schema.StringAttribute)
	if !ok || !descAttr.Optional || !descAttr.Computed || len(descAttr.PlanModifiers) != 1 {
		t.Errorf("description must be optional, computed, UseStateForUnknown, got %#v", resSchema.Attributes["description"])
	}

	// encrypt_cert is not exposed, because the design doc (Q1) leaves the
	// certificate unmanaged.
	if _, ok := resSchema.Attributes["encrypt_cert"]; ok {
		t.Error("encrypt_cert must not be exposed")
	}

	h2Attr, ok := resSchema.Attributes["enable_h2"].(schema.BoolAttribute)
	if !ok || !h2Attr.Optional || !h2Attr.Computed || len(h2Attr.PlanModifiers) != 1 {
		t.Errorf("enable_h2 must be optional, computed, UseStateForUnknown, got %#v", resSchema.Attributes["enable_h2"])
	}

	// Both inputs are Computed too, so an omitted object keeps its prior value.
	spAttr, ok := resSchema.Attributes["session_persistence"].(schema.SingleNestedAttribute)
	if !ok || !spAttr.Optional || !spAttr.Computed || len(spAttr.PlanModifiers) != 1 {
		t.Errorf("session_persistence must be optional, computed with one plan modifier, got %#v", resSchema.Attributes["session_persistence"])
	}
	for _, name := range []string{"type", "cookie_name"} {
		inner, ok := spAttr.Attributes[name].(schema.StringAttribute)
		if !ok || !inner.Optional || !inner.Computed || inner.Required || len(inner.PlanModifiers) != 0 {
			t.Errorf("session_persistence.%s must be optional and computed with no plan modifiers, got %#v", name, spAttr.Attributes[name])
		}
	}

	hcAttr, ok := resSchema.Attributes["health_check"].(schema.SingleNestedAttribute)
	if !ok || !hcAttr.Optional || hcAttr.Computed || len(hcAttr.PlanModifiers) != 0 {
		t.Errorf("health_check must be optional, not computed, no plan modifiers, got %#v", resSchema.Attributes["health_check"])
	}
	for _, childName := range []string{"path", "http_method", "expected_codes"} {
		childAttr, ok := hcAttr.Attributes[childName].(schema.StringAttribute)
		if !ok || !childAttr.Optional || childAttr.Computed || len(childAttr.PlanModifiers) != 0 {
			t.Errorf("health_check.%s must be optional, not computed with no plan modifiers, got %#v", childName, childAttr)
		}
	}

	// Computed attributes: provisioning_status, status, member_count, created_at, updated_at
	for _, name := range []string{"provisioning_status", "status", "updated_at"} {
		attr, ok := resSchema.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be computed-only, got %#v", name, resSchema.Attributes[name])
		}
	}
	memberCountAttr, ok := resSchema.Attributes["member_count"].(schema.Int64Attribute)
	if !ok || !memberCountAttr.Computed || memberCountAttr.Required || memberCountAttr.Optional {
		t.Errorf("member_count must be computed-only, got %#v", resSchema.Attributes["member_count"])
	}
}

func TestServerGroupImmutableAttributesRequireReplace(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupSchema(t)

	stateModel := serverGroupConfiguredModel()
	state := tfsdk.State{Schema: resSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	planModel := stateModel
	planData := tfsdk.Plan{Schema: resSchema}
	if diags := planData.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	lbAttr, ok := resSchema.Attributes["load_balancer"].(schema.SingleNestedAttribute)
	if !ok || len(lbAttr.PlanModifiers) == 0 {
		t.Fatalf("attribute load_balancer has no object plan modifiers: %#v", resSchema.Attributes["load_balancer"])
	}
	otherLBID := core.UUID{9}
	lbReq := planmodifier.ObjectRequest{
		ConfigValue: reference.Value(reference.ID, types.StringValue(otherLBID.String())),
		State:       state,
		Plan:        planData,
		StateValue:  stateModel.LoadBalancer,
		PlanValue:   reference.Value(reference.ID, types.StringValue(otherLBID.String())),
	}
	var lbResp planmodifier.ObjectResponse
	lbAttr.PlanModifiers[0].PlanModifyObject(context.Background(), lbReq, &lbResp)
	if !lbResp.RequiresReplace {
		t.Error("expected modifying load_balancer to require replacement")
	}

	for _, name := range []string{"protocol"} {
		attr, ok := resSchema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("attribute %s is not StringAttribute", name)
		}
		if len(attr.PlanModifiers) == 0 {
			t.Fatalf("attribute %s has no plan modifiers", name)
		}
		modifier := attr.PlanModifiers[0]
		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue("after"),
			State:       state,
			Plan:        planData,
			StateValue:  types.StringValue("before"),
			PlanValue:   types.StringValue("after"),
		}
		var resp planmodifier.StringResponse
		modifier.PlanModifyString(context.Background(), req, &resp)
		if !resp.RequiresReplace {
			t.Errorf("expected modifying %s to require replacement", name)
		}
	}

	// A change of spelling alone names the same value and updates in place.
	lbReq.ConfigValue = reference.Value(reference.ID, types.StringValue("  "+strings.ToUpper(serverGroupTestLBID.String())+"\n"))
	lbReq.PlanValue = lbReq.ConfigValue
	lbResp = planmodifier.ObjectResponse{}
	lbAttr.PlanModifiers[0].PlanModifyObject(context.Background(), lbReq, &lbResp)
	if lbResp.RequiresReplace {
		t.Error("a padded load_balancer.id must not require replacement")
	}
	protocol := resSchema.Attributes["protocol"].(schema.StringAttribute)
	var protocolResp planmodifier.StringResponse
	protocol.PlanModifiers[0].PlanModifyString(context.Background(), planmodifier.StringRequest{
		ConfigValue: types.StringValue(" HTTP "),
		State:       state,
		Plan:        planData,
		StateValue:  stateModel.Protocol,
		PlanValue:   types.StringValue(" HTTP "),
	}, &protocolResp)
	if protocolResp.RequiresReplace {
		t.Error("a padded protocol must not require replacement")
	}
}

func TestServerGroupComputedRemovalPreservesState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := serverGroupConfiguredModel()
	model.EnableH2 = types.BoolValue(true)
	model.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("APP_COOKIE"), "cookie_name": types.StringValue("SESSIONID"),
	})
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	planModel := model
	planModel.Name = types.StringValue("renamed")
	planModel.EnableH2 = types.BoolUnknown()
	planModel.SessionPersistence = types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes())
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatal(diags)
	}
	h2Attr := serverGroupSchema(t).Attributes["enable_h2"].(schema.BoolAttribute)
	var h2Resp planmodifier.BoolResponse
	h2Attr.PlanModifiers[0].PlanModifyBool(ctx, planmodifier.BoolRequest{
		State: state, Plan: plan, ConfigValue: types.BoolNull(), StateValue: model.EnableH2, PlanValue: planModel.EnableH2,
	}, &h2Resp)
	if h2Resp.Diagnostics.HasError() || !h2Resp.PlanValue.Equal(model.EnableH2) {
		t.Fatalf("removing enable_h2 did not preserve true: %#v", h2Resp)
	}
	spAttr := serverGroupSchema(t).Attributes["session_persistence"].(schema.SingleNestedAttribute)
	var spResp planmodifier.ObjectResponse
	spAttr.PlanModifiers[0].PlanModifyObject(ctx, planmodifier.ObjectRequest{
		State: state, Plan: plan, ConfigValue: types.ObjectNull(serverGroupSessionPersistenceAttrTypes()),
		StateValue: model.SessionPersistence, PlanValue: planModel.SessionPersistence,
	}, &spResp)
	if spResp.Diagnostics.HasError() || !spResp.PlanValue.Equal(model.SessionPersistence) {
		t.Fatalf("removing persistence did not preserve APP_COOKIE: %#v", spResp)
	}
	planModel.EnableH2 = h2Resp.PlanValue
	planModel.SessionPersistence = spResp.PlanValue
	body, diags := buildServerGroupUpdateBody(ctx, planModel, model)
	if h2 := core.NullableToPtr(body.EnableH2); diags.HasError() || h2 == nil || !*h2 {
		t.Fatalf("removed enable_h2 must keep its prior value in PUT: %#v, %v", body, diags)
	}
	appCookie, err := body.SessionPersistence.AsServerGroupSessionPersistenceAppCookie()
	if err != nil || appCookie.SessionPersistenceCookieName != "SESSIONID" {
		t.Fatalf("removed persistence must keep its prior value in PUT: %#v, %v", appCookie, err)
	}
}

func TestServerGroupValidateConfigRequiresPersistenceType(t *testing.T) {
	t.Parallel()
	for name, cookie := range map[string]types.String{"without cookie": types.StringNull(), "with cookie": types.StringValue("x")} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := serverGroupConfiguredModel()
			model.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
				"type": types.StringNull(), "cookie_name": cookie,
			})
			config := tfsdk.Config{Schema: serverGroupSchema(t)}
			plan := tfsdk.Plan{Schema: config.Schema}
			if diags := plan.Set(context.Background(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			config.Raw = plan.Raw
			var resp resource.ValidateConfigResponse
			(&ServerGroupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: config}, &resp)
			if len(resp.Diagnostics.Errors()) != 1 || resp.Diagnostics.Errors()[0].Summary() != "Missing session persistence type" {
				t.Fatalf("expected one missing type error, got %v", resp.Diagnostics)
			}
		})
	}
}

func TestServerGroupValidateConfigCookieName(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"NONE", "HTTP_COOKIE", "SOURCE_IP", "APP_COOKIE"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			model := serverGroupConfiguredModel()
			model.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
				"type": types.StringValue(kind), "cookie_name": types.StringValue("x"),
			})
			config := tfsdk.Config{Schema: serverGroupSchema(t)}
			plan := tfsdk.Plan{Schema: config.Schema}
			if diags := plan.Set(context.Background(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			config.Raw = plan.Raw
			var resp resource.ValidateConfigResponse
			(&ServerGroupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: config}, &resp)
			if resp.Diagnostics.HasError() != (kind != "APP_COOKIE") {
				t.Fatalf("unexpected validation for %s: %v", kind, resp.Diagnostics)
			}
		})
	}
}

// The backend stores enable_h2 = true on an HTTPS group whatever the request
// sends, so only a known false on HTTPS is rejected. Other protocols reach the
// backend, which enforces its own rules for them.
func TestServerGroupValidateConfigRejectsDisabledH2OnHTTPS(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		protocol types.String
		enableH2 types.Bool
		wantErr  bool
	}{
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolValue(false), wantErr: true},
		{protocol: types.StringValue(" HTTPS "), enableH2: types.BoolValue(false), wantErr: true},
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolValue(true)},
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolNull()},
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolUnknown()},
		{protocol: types.StringValue("HTTP"), enableH2: types.BoolValue(false)},
		{protocol: types.StringUnknown(), enableH2: types.BoolValue(false)},
	} {
		t.Run(fmt.Sprintf("%s enable_h2=%s", tt.protocol, tt.enableH2), func(t *testing.T) {
			t.Parallel()
			model := serverGroupConfiguredModel()
			model.Protocol, model.EnableH2 = tt.protocol, tt.enableH2
			config := tfsdk.Config{Schema: serverGroupSchema(t)}
			plan := tfsdk.Plan{Schema: config.Schema}
			if diags := plan.Set(context.Background(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			config.Raw = plan.Raw
			var resp resource.ValidateConfigResponse
			(&ServerGroupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: config}, &resp)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics=%v, want an error: %t", resp.Diagnostics, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			withPath, ok := resp.Diagnostics.Errors()[0].(diag.DiagnosticWithPath)
			if len(resp.Diagnostics.Errors()) != 1 || !ok || !withPath.Path().Equal(path.Root("enable_h2")) {
				t.Fatalf("diagnostics=%v, want one error on enable_h2", resp.Diagnostics)
			}
		})
	}
}

func TestServerGroupModifyPlanPreservesOnlyComputedNoOpUpdates(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"computed only", "name change", "unknown input", "create", "destroy"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			s := serverGroupSchema(t)
			config := serverGroupConfiguredModel()
			// Optional and computed fields unconfigured in config
			config.ID = types.StringNull()
			config.Description = types.StringNull()
			config.EnableH2 = types.BoolNull()
			config.SessionPersistence = types.ObjectNull(serverGroupSessionPersistenceAttrTypes())
			config.MemberCount = types.Int64Null()
			config.ProvisioningStatus = types.StringNull()
			config.Status = types.StringNull()
			config.CreatedAt = types.StringNull()
			config.UpdatedAt = types.StringNull()

			prior := serverGroupConfiguredModel()
			planned := prior
			planned.MemberCount = types.Int64Unknown()
			planned.ProvisioningStatus = types.StringUnknown()
			planned.Status = types.StringUnknown()
			planned.UpdatedAt = types.StringUnknown()

			switch scenario {
			case "name change":
				config.Name = types.StringValue("renamed")
				planned.Name = config.Name
			case "unknown input":
				config.Name = types.StringUnknown()
				planned.Name = config.Name
			}

			plan := planForServerGroup(t, s, planned)
			state := stateForServerGroup(t, s, prior)
			configPlan := planForServerGroup(t, s, config)

			req := resource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: s, Raw: configPlan.Raw},
				State:  state,
				Plan:   plan,
			}
			if scenario == "create" {
				req.State.Raw = tftypes.NewValue(req.State.Raw.Type(), nil)
			}
			if scenario == "destroy" {
				req.Plan.Raw = tftypes.NewValue(req.Plan.Raw.Type(), nil)
			}
			resp := resource.ModifyPlanResponse{Plan: req.Plan}
			(&ServerGroupResource{}).ModifyPlan(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			want := req.Plan.Raw
			if scenario == "computed only" {
				want = req.State.Raw
			}
			if !resp.Plan.Raw.Equal(want) {
				t.Fatalf("scenario %s: computed-only changes were retained or a real change was suppressed", scenario)
			}
		})
	}
}

func TestServerGroupResourceCreate(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "pool-1", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	calls := 0
	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch {
		case req.Method == http.MethodPost && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/", serverGroupTestLBID.String()):
			if req.Header.Get("Project-ID") != serverGroupTestProjectID.String() {
				t.Fatalf("missing Project-ID header: %s", req.Header.Get("Project-ID"))
			}
			return serverGroupHTTPResponse(req, http.StatusCreated, sgDetailJSON), nil
		case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
			return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	r := newServerGroupMutationTestResource(t, transport)
	plan := serverGroupConfiguredModel()
	plan.ID = types.StringUnknown()
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.ProvisioningStatus = types.StringUnknown()
	plan.Status = types.StringUnknown()
	plan.MemberCount = types.Int64Unknown()

	planData := tfsdk.Plan{Schema: serverGroupSchema(t)}
	if diags := planData.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: serverGroupSchema(t)}

	r.Create(context.Background(), resource.CreateRequest{Plan: planData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create returned error: %v", resp.Diagnostics)
	}

	var state ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != serverGroupTestID.String() || reference.Get(state.LoadBalancer, reference.ID).ValueString() != serverGroupTestLBID.String() {
		t.Fatalf("unexpected state after create: %#v", state)
	}
}

// The API trims request strings, so Create sends trimmed values and state
// keeps the configured spelling through Create and the next Read.
func TestServerGroupResourceCreateTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	const padded = " test description\n"
	sgDetailJSON := sampleServerGroupDetailJSON(t, sampleServerGroupDetail(serverGroupTestID, "test-group", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN))
	var sent []string
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/", serverGroupTestLBID.String()):
			var body struct {
				Name        string  `json:"name"`
				Protocol    string  `json:"protocol"`
				Algorithm   string  `json:"algorithm"`
				Description *string `json:"description"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Description == nil {
				t.Fatal("create body omitted description")
			}
			if body.Name != "test-group" || body.Protocol != "HTTP" || body.Algorithm != "ROUND_ROBIN" {
				t.Errorf("create body name=%q protocol=%q algorithm=%q, want trimmed values", body.Name, body.Protocol, body.Algorithm)
			}
			sent = append(sent, *body.Description)
			return serverGroupHTTPResponse(req, http.StatusCreated, sgDetailJSON), nil
		case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
			return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	}))
	plan := serverGroupConfiguredModel()
	plan.ID = types.StringUnknown()
	plan.Description = types.StringValue(padded)
	plan.Name = types.StringValue(" test-group ")
	plan.Protocol = types.StringValue("\tHTTP ")
	plan.Algorithm = types.StringValue(" ROUND_ROBIN\n")
	plan.LoadBalancer = reference.Value(reference.ID, types.StringValue("  "+strings.ToUpper(serverGroupTestLBID.String())+" "))
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.ProvisioningStatus = types.StringUnknown()
	plan.Status = types.StringUnknown()
	plan.MemberCount = types.Int64Unknown()
	planData := tfsdk.Plan{Schema: serverGroupSchema(t)}
	if diags := planData.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	create := resource.CreateResponse{State: tfsdk.State{Schema: serverGroupSchema(t)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: planData}, &create)
	if create.Diagnostics.HasError() {
		t.Fatalf("Create returned error: %v", create.Diagnostics)
	}
	if want := []string{"test description"}; !slices.Equal(sent, want) {
		t.Fatalf("sent descriptions = %q, want %q", sent, want)
	}
	var created ServerGroupResourceModel
	if diags := create.State.Get(context.Background(), &created); diags.HasError() {
		t.Fatalf("get create state: %v", diags)
	}
	if !created.Description.Equal(plan.Description) || !created.Name.Equal(plan.Name) || !created.Protocol.Equal(plan.Protocol) ||
		!created.Algorithm.Equal(plan.Algorithm) || !created.LoadBalancer.Equal(plan.LoadBalancer) {
		t.Fatalf("create state = %#v, want the configured spellings", created)
	}

	read := resource.ReadResponse{State: create.State}
	r.Read(context.Background(), resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", read.Diagnostics)
	}
	var refreshed ServerGroupResourceModel
	if diags := read.State.Get(context.Background(), &refreshed); diags.HasError() {
		t.Fatalf("get read state: %v", diags)
	}
	if !refreshed.Description.Equal(plan.Description) || !refreshed.Name.Equal(plan.Name) || !refreshed.Protocol.Equal(plan.Protocol) ||
		!refreshed.Algorithm.Equal(plan.Algorithm) || !refreshed.LoadBalancer.Equal(plan.LoadBalancer) {
		t.Fatalf("read state = %#v, want the configured spellings", refreshed)
	}
}

// The full PUT sends the trimmed description, whether it comes from the plan
// or is kept from state.
func TestBuildServerGroupUpdateBodyTrimsDescription(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		state types.String
		plan  types.String
	}{
		{name: "configured description", state: types.StringValue("description"), plan: types.StringValue(" web tier\n")},
		{name: "omitted description keeps state", state: types.StringValue(" web tier\n"), plan: types.StringUnknown()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := serverGroupConfiguredModel()
			state.Description = tt.state
			plan := state
			plan.Description = tt.plan
			body, diags := buildServerGroupUpdateBody(context.Background(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.Description != "web tier" {
				t.Fatalf("PUT description = %q, want %q", body.Description, "web tier")
			}
		})
	}
}

func TestServerGroupResourceCreateKeepsIDWhenWaiterFails(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "pool-1", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPost:
			return serverGroupHTTPResponse(req, http.StatusCreated, sgDetailJSON), nil
		case http.MethodGet:
			return serverGroupHTTPResponse(req, http.StatusForbidden, `{"errors":[{"code":"forbidden","message":"forbidden"}]}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	}))
	plan := serverGroupConfiguredModel()
	plan.ID = types.StringUnknown()
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()
	plan.ProvisioningStatus = types.StringUnknown()
	plan.Status = types.StringUnknown()
	plan.MemberCount = types.Int64Unknown()
	planData := tfsdk.Plan{Schema: serverGroupSchema(t)}
	if diags := planData.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: serverGroupSchema(t)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: planData}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a failing waiter must return an error")
	}
	var state ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != serverGroupTestID.String() || state.Status.IsUnknown() || state.UpdatedAt.IsUnknown() {
		t.Fatalf("created group must stay in state with known values: %#v", state)
	}
}

func TestServerGroupResourceRead(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "pool-1", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()) {
			return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	r := newServerGroupTestResource(t, transport)
	state := serverGroupConfiguredModel()
	stateData := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := stateData.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var resp resource.ReadResponse
	resp.State = stateData

	r.Read(context.Background(), resource.ReadRequest{State: stateData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", resp.Diagnostics)
	}

	var newState ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("get new state: %v", diags)
	}
	if newState.ID.ValueString() != serverGroupTestID.String() {
		t.Fatalf("unexpected ID: %s", newState.ID.ValueString())
	}
}

func TestServerGroupResourceReadNotFoundRemovesResource(t *testing.T) {
	t.Parallel()

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return serverGroupHTTPResponse(req, http.StatusNotFound, `{"error":"not found"}`), nil
	})

	r := newServerGroupTestResource(t, transport)
	state := serverGroupConfiguredModel()
	stateData := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := stateData.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var resp resource.ReadResponse
	resp.State = stateData

	r.Read(context.Background(), resource.ReadRequest{State: stateData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", resp.Diagnostics)
	}

	var checkState *ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &checkState); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if checkState != nil {
		t.Fatal("expected state to be removed when 404 is returned")
	}
}

func TestServerGroupResourceReadDeletedRemovesResource(t *testing.T) {
	t.Parallel()

	sgDetail := sampleServerGroupDetail(serverGroupTestID, "pool-1", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	sgDetail.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
	})

	r := newServerGroupTestResource(t, transport)
	state := serverGroupConfiguredModel()
	stateData := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := stateData.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var resp resource.ReadResponse
	resp.State = stateData

	r.Read(context.Background(), resource.ReadRequest{State: stateData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned error: %v", resp.Diagnostics)
	}

	var checkState *ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &checkState); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if checkState != nil {
		t.Fatal("expected state to be removed when DELETED is returned")
	}
}

func TestServerGroupResourceUpdate(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			sgDetail := sampleServerGroupDetail(serverGroupTestID, "updated-pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmLEASTCONNECTIONS)
			sgDetail.Description.Set("description")
			sgDetailJSON := sampleServerGroupDetailJSON(t, sgDetail)

			reads := 0
			transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
					var body map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) != 7 || body["name"] != "updated-pool" || body["algorithm"] != "LEAST_CONNECTIONS" || body["description"] != "description" || body["backend_tls_mode"] != nil {
						t.Fatalf("unexpected PUT body: %#v", body)
					}
					return serverGroupHTTPResponse(req, status, sgDetailJSON), nil
				case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
					reads++
					return serverGroupHTTPResponse(req, http.StatusOK, sgDetailJSON), nil
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			})

			r := newServerGroupMutationTestResource(t, transport)
			state := serverGroupConfiguredModel()
			plan := serverGroupConfiguredModel()
			plan.Name = types.StringValue("updated-pool")
			plan.Algorithm = types.StringValue("LEAST_CONNECTIONS")

			stateData := tfsdk.State{Schema: serverGroupSchema(t)}
			if diags := stateData.Set(context.Background(), &state); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			planData := tfsdk.Plan{Schema: serverGroupSchema(t)}
			if diags := planData.Set(context.Background(), &plan); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			var resp resource.UpdateResponse
			resp.State = stateData

			r.Update(context.Background(), resource.UpdateRequest{Plan: planData, State: stateData}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update returned error: %v", resp.Diagnostics)
			}

			var newState ServerGroupResourceModel
			if diags := resp.State.Get(context.Background(), &newState); diags.HasError() {
				t.Fatalf("get state: %v", diags)
			}
			if newState.Name.ValueString() != "updated-pool" || newState.Algorithm.ValueString() != "LEAST_CONNECTIONS" {
				t.Fatalf("unexpected state after update: %#v", newState)
			}
			if reads != 2 || !newState.ID.Equal(state.ID) {
				t.Fatalf("update must read the group before the PUT and refresh the same group after it: reads=%d, id=%s", reads, newState.ID)
			}
		})
	}
}

func TestServerGroupResourceUpdateClearingAttributes(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			sgCleared := sampleServerGroupDetail(serverGroupTestID, "cleared-pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
			sgCleared.Description.Set("")
			sgCleared.EnableHealthCheck = false
			sgCleared.MonitorType.SetNull()
			sgClearedJSON := sampleServerGroupDetailJSON(t, sgCleared)

			reads := 0
			transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
					var body map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) != 7 || body["description"] != "" || body["health_check"] != nil || body["enable_h2"] != false || body["backend_tls_mode"] != nil {
						t.Fatalf("unexpected PUT body: %#v", body)
					}
					if body["backend_tls_mode"] != nil {
						t.Fatalf("an update of a group without backend TLS mode must send backend_tls_mode as null: %#v", body)
					}
					return serverGroupHTTPResponse(req, status, sgClearedJSON), nil
				case req.Method == http.MethodGet && req.URL.Path == fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID.String(), serverGroupTestID.String()):
					reads++
					return serverGroupHTTPResponse(req, http.StatusOK, sgClearedJSON), nil
				}
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			})

			r := newServerGroupMutationTestResource(t, transport)
			// Prior state with description and health_check
			state := serverGroupConfiguredModel()
			state.Description = types.StringValue("previous description")
			hcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
				"monitor_type":     types.StringValue("TCP"),
				"delay":            types.Int64Value(15),
				"timeout":          types.Int64Value(5),
				"max_retries":      types.Int64Value(3),
				"max_retries_down": types.Int64Value(3),
				"path":             types.StringNull(),
				"http_method":      types.StringNull(),
				"expected_codes":   types.StringNull(),
			})
			state.HealthCheck = hcObj

			// Plan clears description and health_check
			plan := serverGroupConfiguredModel()
			plan.Description = types.StringValue("")
			plan.HealthCheck = types.ObjectNull(serverGroupHealthCheckAttrTypes())

			stateData := tfsdk.State{Schema: serverGroupSchema(t)}
			if diags := stateData.Set(context.Background(), &state); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			planData := tfsdk.Plan{Schema: serverGroupSchema(t)}
			if diags := planData.Set(context.Background(), &plan); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			var resp resource.UpdateResponse
			resp.State = stateData

			r.Update(context.Background(), resource.UpdateRequest{Plan: planData, State: stateData}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update returned error: %v", resp.Diagnostics)
			}

			var newState ServerGroupResourceModel
			if diags := resp.State.Get(context.Background(), &newState); diags.HasError() {
				t.Fatalf("get state: %v", diags)
			}
			if newState.Description.IsNull() || newState.Description.ValueString() != "" {
				t.Errorf("expected known empty description, got %s", newState.Description)
			}
			if !newState.HealthCheck.IsNull() {
				t.Errorf("expected null health_check, got %#v", newState.HealthCheck)
			}
			if reads != 2 || !newState.ID.Equal(state.ID) {
				t.Fatalf("update must read the group before the PUT and refresh the same group after it: reads=%d, id=%s", reads, newState.ID)
			}
		})
	}
}

func TestServerGroupResourceUpdateRejectsMissingAcceptedPayload(t *testing.T) {
	t.Parallel()
	calls := 0
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 && req.Method == http.MethodGet {
			sg := sampleServerGroupDetail(serverGroupTestID, "pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
			return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sg)), nil
		}
		if calls != 2 || req.Method != http.MethodPut {
			t.Fatalf("unexpected request after failed update: %s", req.Method)
		}
		response := serverGroupHTTPResponse(req, http.StatusAccepted, "")
		response.Header.Del("Content-Type")
		return response, nil
	}))
	stateModel := serverGroupConfiguredModel()
	planModel := stateModel
	planModel.Name = types.StringValue("updated-pool")
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || calls != 2 {
		t.Fatalf("missing payload must fail without polling or retry: calls=%d, diagnostics=%v", calls, resp.Diagnostics)
	}
	if !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("failed update changed prior state")
	}
}

func TestServerGroupRenamePreservesUnconfiguredDescription(t *testing.T) {
	t.Parallel()
	stateModel := serverGroupConfiguredModel()
	stateModel.Description = types.StringValue("console description")
	stateModel.EnableH2 = types.BoolValue(true)
	stateModel.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("APP_COOKIE"), "cookie_name": types.StringValue("SESSIONID"),
	})
	planModel := stateModel
	planModel.Name = types.StringValue("renamed")
	planModel.Description = types.StringUnknown()
	planModel.EnableH2 = types.BoolUnknown()
	planModel.SessionPersistence = types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes())
	api := sampleServerGroupDetail(serverGroupTestID, "renamed", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	api.Description.Set("console description")
	api.EnableH2.Set(true)
	api.SessionPersistenceType.Set(loadbalancersdk.ServerGroupSessionPersistenceAPPCOOKIE)
	api.SessionPersistenceCookieName.Set("SESSIONID")
	puts := 0
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID, serverGroupTestID) {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		switch req.Method {
		case http.MethodPut:
			puts++
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 7 || body["name"] != "renamed" || body["description"] != "console description" || body["enable_h2"] != true || body["backend_tls_mode"] != nil {
				t.Fatalf("rename must send the preserved values: %#v", body)
			}
		case http.MethodGet:
		default:
			t.Fatalf("unexpected method: %s", req.Method)
		}
		return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, api)), nil
	}))
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var updated ServerGroupResourceModel
	if diags := resp.State.Get(context.Background(), &updated); diags.HasError() {
		t.Fatal(diags)
	}
	if puts != 1 || !updated.ID.Equal(stateModel.ID) || !updated.Description.Equal(stateModel.Description) ||
		!updated.EnableH2.Equal(stateModel.EnableH2) || !updated.SessionPersistence.Equal(stateModel.SessionPersistence) {
		t.Fatalf("rename lost preserved values: %#v", updated)
	}
}

func TestServerGroupUpdateSendsBackCurrentBackendTLSMode(t *testing.T) {
	t.Parallel()
	stateModel := serverGroupConfiguredModel()
	planModel := stateModel
	planModel.Name = types.StringValue("renamed")
	api := sampleServerGroupDetail(serverGroupTestID, "renamed", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	api.BackendTlsMode.Set(loadbalancersdk.ServerGroupBackendTLSModeENCRYPTONLY)
	gets, puts := 0, 0
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/server-groups/%s/", serverGroupTestLBID, serverGroupTestID) {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		switch req.Method {
		case http.MethodGet:
			gets++
		case http.MethodPut:
			puts++
			if gets != 1 {
				t.Fatalf("the idle wait must read the backend TLS mode right before the PUT: gets=%d", gets)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 7 || body["name"] != "renamed" || body["backend_tls_mode"] != string(loadbalancersdk.ServerGroupBackendTLSModeENCRYPTONLY) {
				t.Fatalf("rename must send back the current backend TLS mode: %#v", body)
			}
		default:
			t.Fatalf("unexpected method: %s", req.Method)
		}
		return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, api)), nil
	}))
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if puts != 1 || gets != 2 {
		t.Fatalf("update requests: gets=%d puts=%d", gets, puts)
	}
}

func TestServerGroupUpdateDoesNotPutWithoutCurrentBackendTLSMode(t *testing.T) {
	t.Parallel()
	stateModel := serverGroupConfiguredModel()
	planModel := stateModel
	planModel.Name = types.StringValue("renamed")
	r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("a failed idle wait must not send %s", req.Method)
		}
		return serverGroupHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"server group not found"}]}`), nil
	}))
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("update must fail when the current backend TLS mode cannot be read")
	}
	if !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("failed update changed prior state")
	}
}

func TestServerGroupInvalidCookieDoesNotCreateOrUpdate(t *testing.T) {
	t.Parallel()
	r := newServerGroupTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("invalid cookie reached API: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))
	model := serverGroupConfiguredModel()
	state := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	model.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("HTTP_COOKIE"), "cookie_name": types.StringValue("x"),
	})
	plan := tfsdk.Plan{Schema: state.Schema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	create := resource.CreateResponse{State: state}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &create)
	if !create.Diagnostics.HasError() {
		t.Fatal("create must reject cookie_name for HTTP_COOKIE")
	}
	update := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &update)
	if !update.Diagnostics.HasError() {
		t.Fatal("update must reject cookie_name for HTTP_COOKIE")
	}
}

func TestServerGroupResourceDelete(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		deleteStatus int
	}{
		{"accepted", http.StatusAccepted},
		{"already removed", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deletes := 0
			r := newServerGroupMutationTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != serverGroupPath {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				switch {
				case req.Method == http.MethodDelete:
					deletes++
					return serverGroupHTTPResponse(req, tt.deleteStatus, ""), nil
				case req.Method == http.MethodGet && deletes == 0:
					sg := sampleServerGroupDetail(serverGroupTestID, "pool-1", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
					return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sg)), nil
				case req.Method == http.MethodGet:
					return serverGroupHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"not found"}]}`), nil
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			}))
			if diags := executeServerGroupMutation(t, r, http.MethodDelete); diags.HasError() {
				t.Fatalf("Delete returned error: %v", diags)
			}
			if deletes != 1 {
				t.Fatalf("DELETE calls = %d, want 1", deletes)
			}
		})
	}
}

// A group that the idle wait finds gone needs no DELETE.
func TestServerGroupResourceDeleteNotFoundIsSuccess(t *testing.T) {
	t.Parallel()

	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("a group that is gone must not get %s", req.Method)
		}
		return serverGroupHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","message":"not found"}]}`), nil
	})

	r := newServerGroupTestResource(t, transport)
	state := serverGroupConfiguredModel()
	stateData := tfsdk.State{Schema: serverGroupSchema(t)}
	if diags := stateData.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: stateData}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete returned error: %v", resp.Diagnostics)
	}
}

func TestServerGroupResourceImportState(t *testing.T) {
	t.Parallel()

	r := &ServerGroupResource{}
	schemaVal := serverGroupSchema(t)

	newImportResponse := func() resource.ImportStateResponse {
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaVal}}
		emptyModel := &ServerGroupResourceModel{
			ServerGroupModel: ServerGroupModel{
				SessionPersistence: types.ObjectNull(serverGroupSessionPersistenceAttrTypes()),
				HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
			},
			LoadBalancer: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		}
		if diags := resp.State.Set(context.Background(), emptyModel); diags.HasError() {
			t.Fatalf("initialize state: %v", diags)
		}
		return resp
	}

	// Valid composite ID
	req := resource.ImportStateRequest{
		ID: fmt.Sprintf("%s/%s", serverGroupTestLBID.String(), serverGroupTestID.String()),
	}
	resp := newImportResponse()

	r.ImportState(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on import: %v", resp.Diagnostics)
	}

	var loadBalancer types.Object
	var groupID types.String
	resp.State.GetAttribute(context.Background(), path.Root("load_balancer"), &loadBalancer)
	resp.State.GetAttribute(context.Background(), path.Root("id"), &groupID)

	lbID := reference.Get(loadBalancer, reference.ID)
	if lbID.ValueString() != serverGroupTestLBID.String() || groupID.ValueString() != serverGroupTestID.String() {
		t.Fatalf("unexpected imported attributes: load_balancer.id=%s, groupID=%s", lbID.ValueString(), groupID.ValueString())
	}

	// Invalid import IDs
	for _, badID := range []string{
		"only-one-id",
		"/missing-first",
		"missing-second/",
		"invalid-uuid/00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000001/invalid-uuid",
		"a/b/c",
	} {
		badResp := newImportResponse()
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: badID}, &badResp)
		if !badResp.Diagnostics.HasError() {
			t.Errorf("expected import failure for ID %q, got success", badID)
		}
	}
}

func TestServerGroupWaitUntilReadyStatusTransitions(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				sgPending := sampleServerGroupDetail(serverGroupTestID, "sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
				sgPending.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
				return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sgPending)), nil
			}
			sgActive := sampleServerGroupDetail(serverGroupTestID, "sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
			return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sgActive)), nil
		})

		r := newServerGroupTestResource(t, transport)
		sg, err := r.waitUntilReady(context.Background(), serverGroupTestLBID, serverGroupTestID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
		if err != nil {
			t.Fatalf("waitUntilReady error: %v", err)
		}
		if calls != 2 || sg.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE {
			t.Fatalf("expected 2 calls and ACTIVE status, got calls=%d status=%s", calls, sg.ProvisioningStatus)
		}
	})
}

// The backend does not leave ERROR or UNKNOWN on its own, so both end the
// wait on the first poll.
func TestServerGroupWaitUntilReadyTerminalError(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					sg := sampleServerGroupDetail(serverGroupTestID, "sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
					sg.ProvisioningStatus = status
					return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sg)), nil
				})

				r := newServerGroupTestResource(t, transport)
				start := time.Now()
				_, err := r.waitUntilReady(context.Background(), serverGroupTestLBID, serverGroupTestID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
				if err == nil || !strings.Contains(err.Error(), "terminal provisioning status "+string(status)) {
					t.Fatalf("expected terminal status error, got %v", err)
				}
				if calls != 1 || time.Since(start) != 0 {
					t.Fatalf("%s must fail on the first poll: calls=%d elapsed=%s", status, calls, time.Since(start))
				}
			})
		})
	}
}

// The SDK owns HTTP retry, so a GET that still fails after it is final.
func TestServerGroupWaitUntilReadyFailsOnServerError(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return serverGroupHTTPResponse(req, http.StatusInternalServerError, `{"errors":[{"code":"error","message":"failed"}]}`), nil
		})

		r := newServerGroupTestResource(t, transport)
		start := time.Now()
		_, err := r.waitUntilReady(context.Background(), serverGroupTestLBID, serverGroupTestID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
		if !errors.Is(err, loadbalancersdk.ErrInternal) || calls != 1 || time.Since(start) != 0 {
			t.Fatalf("server error must fail on the first poll: calls=%d elapsed=%s err=%v", calls, time.Since(start), err)
		}
	})
}

func TestServerGroupWaitUntilDeleted(t *testing.T) {
	t.Parallel()

	const (
		active        = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
		pendingDelete = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE
		errored       = loadbalancersdk.LoadBalancingProvisioningStatusERROR
		unknown       = loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN
		deleted       = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	)
	// A step answers one GET. The last step of a list repeats.
	type step struct {
		httpStatus int
		status     loadbalancersdk.LoadBalancingProvisioningStatus
	}
	for _, tt := range []struct {
		name         string
		groupSteps   []step
		wantErr      error
		wantErrText  string
		wantTimeout  bool
		wantGroupGET int
		wantMinWait  time.Duration
	}{
		{
			name:         "group removed",
			groupSteps:   []step{{http.StatusNotFound, ""}},
			wantGroupGET: 1,
		},
		{
			name:         "pending delete then removed",
			groupSteps:   []step{{http.StatusOK, pendingDelete}, {http.StatusNotFound, ""}},
			wantGroupGET: 2,
			wantMinWait:  serverGroupReadyPollInterval,
		},
		{
			name:         "deleted status waits for removal",
			groupSteps:   []step{{http.StatusOK, deleted}, {http.StatusNotFound, ""}},
			wantGroupGET: 2,
			wantMinWait:  serverGroupReadyPollInterval,
		},
		{
			name:         "error before pending delete waits",
			groupSteps:   []step{{http.StatusOK, errored}, {http.StatusOK, pendingDelete}, {http.StatusNotFound, ""}},
			wantGroupGET: 3,
			wantMinWait:  2 * serverGroupReadyPollInterval,
		},
		{
			name:         "error after pending delete fails at once",
			groupSteps:   []step{{http.StatusOK, pendingDelete}, {http.StatusOK, errored}},
			wantErrText:  "terminal provisioning status ERROR",
			wantGroupGET: 2,
			wantMinWait:  serverGroupReadyPollInterval,
		},
		{
			name:         "unknown fails at once",
			groupSteps:   []step{{http.StatusOK, pendingDelete}, {http.StatusOK, unknown}},
			wantErrText:  "terminal provisioning status UNKNOWN",
			wantGroupGET: 2,
			wantMinWait:  serverGroupReadyPollInterval,
		},
		{
			name:        "pending delete never removed",
			groupSteps:  []step{{http.StatusOK, pendingDelete}},
			wantErr:     wait.ErrTimeout,
			wantTimeout: true,
			wantMinWait: serverGroupReadyTimeout,
		},
		{
			name:        "error without pending delete waits until timeout",
			groupSteps:  []step{{http.StatusOK, errored}},
			wantErr:     wait.ErrTimeout,
			wantTimeout: true,
			wantMinWait: serverGroupReadyTimeout,
		},
		{
			name:        "unknown without pending delete waits until timeout",
			groupSteps:  []step{{http.StatusOK, unknown}},
			wantErr:     wait.ErrTimeout,
			wantTimeout: true,
			wantMinWait: serverGroupReadyTimeout,
		},
		{
			name:        "active group never deleted",
			groupSteps:  []step{{http.StatusOK, active}},
			wantErr:     wait.ErrTimeout,
			wantTimeout: true,
			wantMinWait: serverGroupReadyTimeout,
		},
		{
			name:         "server error fails at once",
			groupSteps:   []step{{http.StatusInternalServerError, ""}},
			wantErr:      loadbalancersdk.ErrInternal,
			wantGroupGET: 1,
		},
		{
			name:         "permanent group error fails at once",
			groupSteps:   []step{{http.StatusForbidden, ""}},
			wantErr:      loadbalancersdk.ErrUnauthorized,
			wantGroupGET: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				groupGET := 0
				transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method != http.MethodGet || !strings.Contains(req.URL.Path, "/server-groups/") {
						t.Fatalf("the waiter must read only the group: %s %s", req.Method, req.URL.Path)
					}
					groupGET++
					current := tt.groupSteps[min(groupGET, len(tt.groupSteps))-1]
					sg := sampleServerGroupDetail(serverGroupTestID, "sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
					sg.ProvisioningStatus = current.status
					body := sampleServerGroupDetailJSON(t, sg)
					if current.httpStatus != http.StatusOK {
						body = `{"errors":[{"code":"error","message":"failed"}]}`
					}
					return serverGroupHTTPResponse(req, current.httpStatus, body), nil
				})

				r := newServerGroupTestResource(t, transport)
				start := time.Now()
				err := r.waitUntilDeleted(context.Background(), serverGroupTestLBID, serverGroupTestID, serverGroupReadyTimeout, serverGroupReadyPollInterval)
				elapsed := time.Since(start)

				switch {
				case tt.wantErr != nil:
					if !errors.Is(err, tt.wantErr) {
						t.Fatalf("waitUntilDeleted error = %v, want %v", err, tt.wantErr)
					}
				case tt.wantErrText != "":
					if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
						t.Fatalf("waitUntilDeleted error = %v, want %q", err, tt.wantErrText)
					}
				case err != nil:
					t.Fatalf("waitUntilDeleted error: %v", err)
				}
				if elapsed < tt.wantMinWait {
					t.Fatalf("waited %s, want at least %s", elapsed, tt.wantMinWait)
				}
				if !tt.wantTimeout && groupGET != tt.wantGroupGET {
					t.Fatalf("group GET calls = %d, want %d", groupGET, tt.wantGroupGET)
				}
			})
		})
	}
}

// serverGroupStep is one answer of serverGroupBackend: an HTTP status, plus
// the provisioning_status of a successful GET or the error code of a failure.
// An httpStatus of 0 fails the request in the transport.
type serverGroupStep struct {
	httpStatus int
	status     loadbalancersdk.LoadBalancingProvisioningStatus
	code       string
}

// serverGroupBackend answers the requests of one Create, Update, or Delete of
// the Server Group. Each step list answers one call per step, and its last
// step repeats. group answers the GETs of the group until a mutation
// succeeds. After that, the group is ACTIVE, or gone after a DELETE.
type serverGroupBackend struct {
	t          *testing.T
	method     string
	parent     []serverGroupStep
	group      []serverGroupStep
	mutation   []serverGroupStep
	start      time.Time
	parentGETs int
	groupGETs  int
	mutations  []time.Duration
	log        []string
	done       bool
}

func (b *serverGroupBackend) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.Method == http.MethodGet && req.URL.Path == serverGroupParentPath:
		b.parentGETs++
		b.log = append(b.log, "GET load balancer")
		return b.answer(req, b.parent[min(b.parentGETs, len(b.parent))-1])
	case req.Method == http.MethodGet && req.URL.Path == serverGroupPath:
		b.groupGETs++
		b.log = append(b.log, "GET server group")
		step := serverGroupStep{httpStatus: http.StatusOK, status: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE}
		switch {
		case b.done && b.method == http.MethodDelete:
			step = serverGroupStep{httpStatus: http.StatusNotFound, code: "not_found"}
		case !b.done && len(b.group) > 0:
			step = b.group[min(b.groupGETs, len(b.group))-1]
		}
		return b.answer(req, step)
	case req.Method == b.method:
		b.mutations = append(b.mutations, time.Since(b.start))
		b.log = append(b.log, b.method)
		step := b.mutation[min(len(b.mutations), len(b.mutation))-1]
		if step.httpStatus >= 200 && step.httpStatus < 300 {
			b.done = true
		}
		return b.answer(req, step)
	default:
		b.t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
	}
}

func (b *serverGroupBackend) answer(req *http.Request, step serverGroupStep) (*http.Response, error) {
	switch {
	case step.httpStatus == 0:
		return nil, errors.New("connection reset by peer")
	case step.httpStatus >= 300:
		body := fmt.Sprintf(`{"errors":[{"location":"","code":%q,"message":"refused"}],"request_id":"0a1b2c"}`, step.code)
		if step.code == serverGroupBusyCode {
			body = serverGroupBusyRefusalBody
		}
		return serverGroupHTTPResponse(req, step.httpStatus, body), nil
	case req.Method == http.MethodDelete:
		return serverGroupHTTPResponse(req, step.httpStatus, ""), nil
	case req.URL.Path == serverGroupParentPath:
		return serverGroupHTTPResponse(req, step.httpStatus, serverGroupParentJSON(b.t, step.status)), nil
	default:
		sg := sampleServerGroupDetail(serverGroupTestID, "test-group", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
		if step.status != "" {
			sg.ProvisioningStatus = step.status
		}
		return serverGroupHTTPResponse(req, step.httpStatus, sampleServerGroupDetailJSON(b.t, sg)), nil
	}
}

// run executes the mutation of b.method against b and returns its diagnostics.
func (b *serverGroupBackend) run(t *testing.T) diag.Diagnostics {
	t.Helper()
	b.t = t
	b.start = time.Now()
	return executeServerGroupMutation(t, newServerGroupTestResource(t, b), b.method)
}

// serverGroupMutationSuccess answers a successful request of method.
func serverGroupMutationSuccess(method string) serverGroupStep {
	switch method {
	case http.MethodPost:
		return serverGroupStep{httpStatus: http.StatusCreated}
	case http.MethodPut:
		return serverGroupStep{httpStatus: http.StatusOK}
	default:
		return serverGroupStep{httpStatus: http.StatusAccepted}
	}
}

// serverGroupMutationSummary is the diagnostic summary of a failed method.
func serverGroupMutationSummary(method string) string {
	switch method {
	case http.MethodPost:
		return "Error creating Server Group"
	case http.MethodPut:
		return "Error updating Server Group"
	default:
		return "Error deleting Server Group"
	}
}

// executeServerGroupMutation runs the Create, Update, or Delete that sends
// method, and returns its diagnostics.
func executeServerGroupMutation(t *testing.T, r *ServerGroupResource, method string) diag.Diagnostics {
	t.Helper()
	s := serverGroupSchema(t)
	state := serverGroupConfiguredModel()
	switch method {
	case http.MethodPost:
		plan := state
		plan.ID = types.StringUnknown()
		plan.CreatedAt = types.StringUnknown()
		plan.UpdatedAt = types.StringUnknown()
		plan.ProvisioningStatus = types.StringUnknown()
		plan.Status = types.StringUnknown()
		plan.MemberCount = types.Int64Unknown()
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(context.Background(), resource.CreateRequest{Plan: planForServerGroup(t, s, plan)}, &resp)
		return resp.Diagnostics
	case http.MethodPut:
		plan := state
		plan.Name = types.StringValue("renamed")
		stateData := stateForServerGroup(t, s, state)
		resp := resource.UpdateResponse{State: stateData}
		r.Update(context.Background(), resource.UpdateRequest{Plan: planForServerGroup(t, s, plan), State: stateData}, &resp)
		return resp.Diagnostics
	default:
		stateData := stateForServerGroup(t, s, state)
		resp := resource.DeleteResponse{State: stateData}
		r.Delete(context.Background(), resource.DeleteRequest{State: stateData}, &resp)
		return resp.Diagnostics
	}
}

var serverGroupMutationMethods = []string{http.MethodPost, http.MethodPut, http.MethodDelete}

// Each request waits until the Load Balancer, and the group for an update or
// a delete, leaves PENDING_*. The update and delete polls read the group first
// and read the Load Balancer only when the group is idle.
func TestServerGroupWaitsForIdleStateBeforeRequest(t *testing.T) {
	t.Parallel()

	const pendingUpdate = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
	const active = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	for _, tt := range []struct {
		method      string
		wantElapsed time.Duration
	}{
		{http.MethodPost, serverGroupReadyPollInterval},
		{http.MethodPut, 2 * serverGroupReadyPollInterval},
		{http.MethodDelete, 2 * serverGroupReadyPollInterval},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					method:   tt.method,
					parent:   []serverGroupStep{{http.StatusOK, pendingUpdate, ""}, {http.StatusOK, active, ""}},
					group:    []serverGroupStep{{http.StatusOK, pendingUpdate, ""}, {http.StatusOK, active, ""}},
					mutation: []serverGroupStep{serverGroupMutationSuccess(tt.method)},
				}
				if diags := b.run(t); diags.HasError() {
					t.Fatalf("%s returned error: %v", tt.method, diags)
				}
				if len(b.mutations) != 1 || b.mutations[0] != tt.wantElapsed {
					t.Fatalf("%s sent at %v, want once after %s", tt.method, b.mutations, tt.wantElapsed)
				}
			})
		})
	}
}

// Before a delete, the idle wait counts a group in ERROR or UNKNOWN as idle, so
// Delete sends the DELETE without a wait.
func TestServerGroupDeleteTreatsFailedGroupAsIdle(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					method:   http.MethodDelete,
					parent:   []serverGroupStep{{http.StatusOK, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, ""}},
					group:    []serverGroupStep{{http.StatusOK, status, ""}},
					mutation: []serverGroupStep{serverGroupMutationSuccess(http.MethodDelete)},
				}
				if diags := b.run(t); diags.HasError() {
					t.Fatalf("Delete returned error: %v", diags)
				}
				if len(b.mutations) != 1 || b.mutations[0] != 0 {
					t.Fatalf("DELETE sent at %v, want once without a wait", b.mutations)
				}
			})
		})
	}
}

// A failed Delete of a group that started in ERROR or UNKNOWN reports the
// failure and keeps the state. After PENDING_DELETE, the failure shows at once.
// Without PENDING_DELETE, Delete cannot tell it from the state before the
// DELETE, so it waits until timeout.
func TestServerGroupDeleteFromFailedStateKeepsState(t *testing.T) {
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
				wantErr:     "terminal provisioning status " + string(status),
				wantWait:    serverGroupReadyPollInterval,
			},
			"waits until timeout without PENDING_DELETE": {
				afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{status},
				wantErr:     string(status),
				wantWait:    serverGroupReadyTimeout,
				wantTimeout: true,
			},
		} {
			t.Run(string(status)+" "+name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					deleted := false
					polls := 0
					r := newServerGroupTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
						switch {
						case req.Method == http.MethodGet && req.URL.Path == serverGroupParentPath:
							return serverGroupHTTPResponse(req, http.StatusOK, serverGroupParentJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
						case req.Method == http.MethodDelete && req.URL.Path == serverGroupPath:
							deleted = true
							return serverGroupHTTPResponse(req, http.StatusAccepted, ""), nil
						case req.Method == http.MethodGet && req.URL.Path == serverGroupPath:
							current := status
							if deleted {
								current = tt.afterDelete[min(polls, len(tt.afterDelete)-1)]
								polls++
							}
							sg := sampleServerGroupDetail(serverGroupTestID, "test-group", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
							sg.ProvisioningStatus = current
							return serverGroupHTTPResponse(req, http.StatusOK, sampleServerGroupDetailJSON(t, sg)), nil
						}
						t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
						return nil, nil
					}))

					state := stateForServerGroup(t, serverGroupSchema(t), serverGroupConfiguredModel())
					start := time.Now()
					resp := resource.DeleteResponse{State: state}
					r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
					if !deleted {
						t.Fatal("expected the DELETE to be sent")
					}
					errs := resp.Diagnostics.Errors()
					if len(errs) != 1 || errs[0].Summary() != "Error waiting for Server Group deletion" || !strings.Contains(errs[0].Detail(), tt.wantErr) {
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

// The idle wait fails on its first poll, without a request, when the Load
// Balancer is ERROR, UNKNOWN, or gone. Before an update, it also fails at once
// when the group is ERROR, UNKNOWN, or gone. A delete does not take a Load
// Balancer that is gone as proof that the group is gone.
func TestServerGroupIdleWaitFailsAtOnce(t *testing.T) {
	t.Parallel()

	const (
		active  = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
		errored = loadbalancersdk.LoadBalancingProvisioningStatusERROR
		unknown = loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN
	)
	activeStep := serverGroupStep{http.StatusOK, active, ""}
	for _, tt := range []struct {
		name     string
		method   string
		parent   serverGroupStep
		group    serverGroupStep
		wantText string
	}{
		{"create parent error", http.MethodPost, serverGroupStep{http.StatusOK, errored, ""}, activeStep, "terminal provisioning status ERROR"},
		{"create parent unknown", http.MethodPost, serverGroupStep{http.StatusOK, unknown, ""}, activeStep, "terminal provisioning status UNKNOWN"},
		{"create parent gone", http.MethodPost, serverGroupStep{http.StatusNotFound, "", "not_found"}, activeStep, "load balancer " + serverGroupTestLBID.String() + " is gone"},
		{"update parent error", http.MethodPut, serverGroupStep{http.StatusOK, errored, ""}, activeStep, "terminal provisioning status ERROR"},
		{"update parent unknown", http.MethodPut, serverGroupStep{http.StatusOK, unknown, ""}, activeStep, "terminal provisioning status UNKNOWN"},
		{"update parent gone", http.MethodPut, serverGroupStep{http.StatusNotFound, "", "not_found"}, activeStep, "load balancer " + serverGroupTestLBID.String() + " is gone"},
		{"update group error", http.MethodPut, activeStep, serverGroupStep{http.StatusOK, errored, ""}, "terminal provisioning status ERROR"},
		{"update group unknown", http.MethodPut, activeStep, serverGroupStep{http.StatusOK, unknown, ""}, "terminal provisioning status UNKNOWN"},
		{"update group gone", http.MethodPut, activeStep, serverGroupStep{http.StatusNotFound, "", "not_found"}, "server group " + serverGroupTestID.String() + " is gone"},
		{"delete parent error", http.MethodDelete, serverGroupStep{http.StatusOK, errored, ""}, activeStep, "terminal provisioning status ERROR"},
		{"delete parent unknown", http.MethodDelete, serverGroupStep{http.StatusOK, unknown, ""}, activeStep, "terminal provisioning status UNKNOWN"},
		{"delete parent gone", http.MethodDelete, serverGroupStep{http.StatusNotFound, "", "not_found"}, activeStep, "load balancer " + serverGroupTestLBID.String() + " is gone"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					method:   tt.method,
					parent:   []serverGroupStep{tt.parent},
					group:    []serverGroupStep{tt.group},
					mutation: []serverGroupStep{serverGroupMutationSuccess(tt.method)},
				}
				diags := b.run(t)
				if !diags.HasError() || diags[0].Summary() != serverGroupMutationSummary(tt.method) || !strings.Contains(diags[0].Detail(), tt.wantText) {
					t.Fatalf("diagnostics = %v, want %q", diags, tt.wantText)
				}
				if len(b.mutations) != 0 || time.Since(b.start) != 0 {
					t.Fatalf("idle wait must fail on its first poll without a request: mutations=%v elapsed=%s", b.mutations, time.Since(b.start))
				}
			})
		})
	}
}

// A busy refusal after the idle wait returns at once. The request is sent
// once, after the idle wait, and no request follows the refusal. The
// diagnostic names the operation, the group, and the Load Balancer, and keeps
// the refusal.
func TestServerGroupReturnsABusyRefusal(t *testing.T) {
	t.Parallel()

	const (
		pendingUpdate = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
		active        = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	)
	for _, tt := range []struct {
		method      string
		wantSubject string
	}{
		{http.MethodPost, "the creation of a server group on load balancer " + serverGroupTestLBID.String()},
		{http.MethodPut, "the update of server group " + serverGroupTestID.String() + " on load balancer " + serverGroupTestLBID.String()},
		{http.MethodDelete, "the deletion of server group " + serverGroupTestID.String() + " on load balancer " + serverGroupTestLBID.String()},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					method:   tt.method,
					parent:   []serverGroupStep{{http.StatusOK, pendingUpdate, ""}, {http.StatusOK, active, ""}},
					mutation: []serverGroupStep{{http.StatusConflict, "", serverGroupBusyCode}, serverGroupMutationSuccess(tt.method)},
				}
				diags := b.run(t)
				if !diags.HasError() || diags[0].Summary() != serverGroupMutationSummary(tt.method) {
					t.Fatalf("diagnostics = %v, want %q", diags, serverGroupMutationSummary(tt.method))
				}
				for _, want := range []string{tt.wantSubject, "refused as busy", serverGroupBusyCode, "load balancer is busy"} {
					if !strings.Contains(diags[0].Detail(), want) {
						t.Fatalf("diagnostic detail = %q, want it to contain %q", diags[0].Detail(), want)
					}
				}
				if len(b.mutations) != 1 || b.mutations[0] != serverGroupReadyPollInterval {
					t.Fatalf("%s sent at %v, want once after one idle poll", tt.method, b.mutations)
				}
				if last := b.log[len(b.log)-1]; last != tt.method {
					t.Fatalf("requests after the busy refusal: %v", b.log)
				}
			})
		})
	}
}

// Only HTTP 409 with code resource_busy is a busy refusal. Every other
// refusal, such as a 409 with code conflict, a 5xx, or a transport error,
// returns at once and unchanged.
func TestServerGroupReturnsOtherRefusalsUnchanged(t *testing.T) {
	t.Parallel()

	for _, method := range serverGroupMutationMethods {
		for _, refusal := range []serverGroupStep{
			{http.StatusConflict, "", "conflict"},
			{http.StatusBadRequest, "", serverGroupBusyCode},
			{http.StatusInternalServerError, "", serverGroupBusyCode},
			{http.StatusServiceUnavailable, "", "error"},
			{0, "", ""},
		} {
			t.Run(fmt.Sprintf("%s %d %s", method, refusal.httpStatus, refusal.code), func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					b := &serverGroupBackend{
						method:   method,
						parent:   []serverGroupStep{{http.StatusOK, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, ""}},
						mutation: []serverGroupStep{refusal, serverGroupMutationSuccess(method)},
					}
					diags := b.run(t)
					if !diags.HasError() || diags[0].Summary() != serverGroupMutationSummary(method) {
						t.Fatalf("diagnostics = %v, want %q", diags, serverGroupMutationSummary(method))
					}
					if strings.Contains(diags[0].Detail(), "refused as busy") {
						t.Fatalf("diagnostic detail = %q, want the refusal unchanged", diags[0].Detail())
					}
					if len(b.mutations) != 1 {
						t.Fatalf("%s calls = %d, want the refusal returned on the first attempt", method, len(b.mutations))
					}
					if last := b.log[len(b.log)-1]; last != method {
						t.Fatalf("requests after the refusal: %v", b.log)
					}
				})
			})
		}
	}
}

// A delete ends without a request when the idle wait finds the group gone,
// by a 404 or by DELETED.
func TestServerGroupDeleteSendsNothingWhenGroupIsGone(t *testing.T) {
	t.Parallel()

	for _, gone := range []serverGroupStep{
		{http.StatusNotFound, "", "not_found"},
		{http.StatusOK, loadbalancersdk.LoadBalancingProvisioningStatusDELETED, ""},
	} {
		t.Run(fmt.Sprintf("%d %s", gone.httpStatus, gone.status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					method:   http.MethodDelete,
					parent:   []serverGroupStep{{http.StatusOK, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, ""}},
					group:    []serverGroupStep{gone},
					mutation: []serverGroupStep{serverGroupMutationSuccess(http.MethodDelete)},
				}
				if diags := b.run(t); diags.HasError() {
					t.Fatalf("Delete returned error: %v", diags)
				}
				if len(b.mutations) != 0 || b.parentGETs != 0 {
					t.Fatalf("Delete sent %d DELETE and %d Load Balancer GET, want none", len(b.mutations), b.parentGETs)
				}
			})
		})
	}
}

// Every request claims the Load Balancer, so a mutation waits for the claims
// lock of that Load Balancer before its first request.
func TestServerGroupMutationHoldsLoadBalancerLock(t *testing.T) {
	t.Parallel()

	for _, method := range serverGroupMutationMethods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				b := &serverGroupBackend{
					t:        t,
					method:   method,
					parent:   []serverGroupStep{{http.StatusOK, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, ""}},
					mutation: []serverGroupStep{serverGroupMutationSuccess(method)},
				}
				r := newServerGroupTestResource(t, b)
				unlock, err := r.claims.Lock(context.Background(), serverGroupTestLBID.String())
				if err != nil {
					t.Fatal(err)
				}
				result := make(chan diag.Diagnostics, 1)
				go func() { result <- executeServerGroupMutation(t, r, method) }()
				synctest.Wait()
				if len(b.log) != 0 {
					t.Fatalf("%s sent requests while another operation held the lock: %v", method, b.log)
				}
				unlock()
				if diags := <-result; diags.HasError() {
					t.Fatalf("%s returned error: %v", method, diags)
				}
				if len(b.mutations) != 1 {
					t.Fatalf("%s calls = %d, want 1", method, len(b.mutations))
				}
			})
		})
	}
}

func TestBuildServerGroupCreateBody(t *testing.T) {
	t.Parallel()

	// 1. Valid body without health check, default session persistence
	plan := ServerGroupResourceModel{
		ServerGroupModel: ServerGroupModel{
			Name:               types.StringValue("  web-pool  "),
			Protocol:           types.StringValue("HTTP"),
			Algorithm:          types.StringValue("ROUND_ROBIN"),
			Description:        types.StringValue("web tier"),
			EnableH2:           types.BoolValue(true),
			SessionPersistence: types.ObjectNull(serverGroupSessionPersistenceAttrTypes()),
			HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
		},
	}

	body, diags := buildServerGroupCreateBody(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "web-pool" || body.Protocol != loadbalancersdk.ServerGroupProtocolTypeHTTP ||
		body.Algorithm != loadbalancersdk.ServerGroupAlgorithmROUNDROBIN ||
		body.Description == nil || *body.Description != "web tier" ||
		core.NullableToPtr(body.EnableH2) == nil || !body.EnableH2.MustGet() || !body.HealthCheck.IsNull() {
		t.Fatalf("unexpected create body: %#v", body)
	}

	// 2. Valid body with HTTP health check and APP_COOKIE session persistence
	spObj, _ := types.ObjectValue(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type":        types.StringValue("APP_COOKIE"),
		"cookie_name": types.StringValue("session_id"),
	})
	hcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
		"monitor_type":     types.StringValue("HTTP"),
		"delay":            types.Int64Value(10),
		"timeout":          types.Int64Value(5),
		"max_retries":      types.Int64Value(3),
		"max_retries_down": types.Int64Value(3),
		"path":             types.StringValue("/health"),
		"http_method":      types.StringValue("GET"),
		"expected_codes":   types.StringValue("200,204"),
	})
	plan.SessionPersistence = spObj
	plan.HealthCheck = hcObj

	body, diags = buildServerGroupCreateBody(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	healthCheck := core.NullableToPtr(body.HealthCheck)
	if healthCheck == nil {
		t.Fatalf("expected health check, got %#v", body)
	}
	httpCheck, err := healthCheck.AsServerGroupHealthCheckHttpConfig()
	if err != nil || httpCheck.MonitorPath != "/health" || httpCheck.MonitorDelay != 10 {
		t.Fatalf("unexpected HTTP health check: %v, %#v", err, httpCheck)
	}
	appCookie, err := body.SessionPersistence.AsServerGroupSessionPersistenceAppCookie()
	if err != nil || appCookie.SessionPersistenceCookieName != "session_id" {
		t.Fatalf("unexpected app cookie persistence: %v, %#v", err, appCookie)
	}

	// 3. TCP health check
	tcpHcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
		"monitor_type":     types.StringValue("TCP"),
		"delay":            types.Int64Value(15),
		"timeout":          types.Int64Value(5),
		"max_retries":      types.Int64Value(2),
		"max_retries_down": types.Int64Value(2),
		"path":             types.StringNull(),
		"http_method":      types.StringNull(),
		"expected_codes":   types.StringNull(),
	})
	plan.HealthCheck = tcpHcObj
	body, diags = buildServerGroupCreateBody(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	otherCheck, err := body.HealthCheck.MustGet().AsServerGroupHealthCheckOtherConfig()
	if err != nil || otherCheck.MonitorType != loadbalancersdk.ServerGroupHealthCheckOtherConfigMonitorTypeTCP {
		t.Fatalf("unexpected TCP health check: %v, %#v", err, otherCheck)
	}
}

// Omitted optional inputs stay off the wire, except health_check. Its SDK
// field has no omitempty, so creation must send it as an explicit null.
func TestBuildServerGroupCreateBodyOmittedInputsOnTheWire(t *testing.T) {
	t.Parallel()
	plan := ServerGroupResourceModel{
		ServerGroupModel: ServerGroupModel{
			Name:               types.StringValue("web-pool"),
			Protocol:           types.StringValue("TCP"),
			Algorithm:          types.StringValue("ROUND_ROBIN"),
			Description:        types.StringNull(),
			EnableH2:           types.BoolUnknown(),
			SessionPersistence: types.ObjectNull(serverGroupSessionPersistenceAttrTypes()),
			HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
		},
	}
	body, diags := buildServerGroupCreateBody(context.Background(), plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	wire, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	healthCheck, present := fields["health_check"]
	if len(fields) != 5 || !present || healthCheck != nil {
		t.Fatalf("create body must omit description, enable_h2, and backend_tls_mode and send health_check as null: %s", wire)
	}
}

func TestBuildServerGroupCreateBodyValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(*ServerGroupResourceModel)
		wantError string
	}{
		{
			name: "missing name",
			mutate: func(m *ServerGroupResourceModel) {
				m.Name = types.StringNull()
			},
			wantError: "Missing Server Group name",
		},
		{
			name: "invalid protocol",
			mutate: func(m *ServerGroupResourceModel) {
				m.Protocol = types.StringValue("INVALID")
			},
			wantError: "Invalid protocol",
		},
		{
			name: "invalid algorithm",
			mutate: func(m *ServerGroupResourceModel) {
				m.Algorithm = types.StringValue("INVALID")
			},
			wantError: "Invalid algorithm",
		},
		{
			name: "app cookie missing cookie name",
			mutate: func(m *ServerGroupResourceModel) {
				spObj, _ := types.ObjectValue(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
					"type":        types.StringValue("APP_COOKIE"),
					"cookie_name": types.StringNull(),
				})
				m.SessionPersistence = spObj
			},
			wantError: "Missing cookie name",
		},
		{
			name: "invalid session persistence type",
			mutate: func(m *ServerGroupResourceModel) {
				spObj, _ := types.ObjectValue(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
					"type":        types.StringValue("UNKNOWN"),
					"cookie_name": types.StringNull(),
				})
				m.SessionPersistence = spObj
			},
			wantError: "Invalid session persistence type",
		},
		{
			name: "health check missing path for HTTP",
			mutate: func(m *ServerGroupResourceModel) {
				hcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
					"monitor_type":     types.StringValue("HTTP"),
					"delay":            types.Int64Value(10),
					"timeout":          types.Int64Value(5),
					"max_retries":      types.Int64Value(3),
					"max_retries_down": types.Int64Value(3),
					"path":             types.StringNull(),
					"http_method":      types.StringValue("GET"),
					"expected_codes":   types.StringValue("200"),
				})
				m.HealthCheck = hcObj
			},
			wantError: "Missing health check path",
		},
		{
			name: "health check non-HTTP specifying path",
			mutate: func(m *ServerGroupResourceModel) {
				hcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
					"monitor_type":     types.StringValue("TCP"),
					"delay":            types.Int64Value(10),
					"timeout":          types.Int64Value(5),
					"max_retries":      types.Int64Value(3),
					"max_retries_down": types.Int64Value(3),
					"path":             types.StringValue("/path"),
					"http_method":      types.StringNull(),
					"expected_codes":   types.StringNull(),
				})
				m.HealthCheck = hcObj
			},
			wantError: "path, http_method, and expected_codes can only be specified when monitor_type is HTTP or HTTPS",
		},
		{
			name: "health check non-positive delay",
			mutate: func(m *ServerGroupResourceModel) {
				hcObj, _ := types.ObjectValue(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
					"monitor_type":     types.StringValue("TCP"),
					"delay":            types.Int64Value(0),
					"timeout":          types.Int64Value(5),
					"max_retries":      types.Int64Value(3),
					"max_retries_down": types.Int64Value(3),
					"path":             types.StringNull(),
					"http_method":      types.StringNull(),
					"expected_codes":   types.StringNull(),
				})
				m.HealthCheck = hcObj
			},
			wantError: "must be known and positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := ServerGroupResourceModel{
				ServerGroupModel: ServerGroupModel{
					Name:               types.StringValue("pool"),
					Protocol:           types.StringValue("HTTP"),
					Algorithm:          types.StringValue("ROUND_ROBIN"),
					SessionPersistence: types.ObjectNull(serverGroupSessionPersistenceAttrTypes()),
					HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
				},
			}
			tt.mutate(&plan)
			_, diags := buildServerGroupCreateBody(context.Background(), plan)
			if !diags.HasError() {
				t.Fatalf("expected error containing %q, got none", tt.wantError)
			}
			found := false
			for _, d := range diags.Errors() {
				if strings.Contains(d.Summary(), tt.wantError) || strings.Contains(d.Detail(), tt.wantError) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, diags)
			}
		})
	}
}

func TestBuildServerGroupUpdateBody(t *testing.T) {
	t.Parallel()

	plan := ServerGroupResourceModel{
		ServerGroupModel: ServerGroupModel{
			Name:               types.StringValue("updated-name"),
			Algorithm:          types.StringValue("LEAST_CONNECTIONS"),
			Description:        types.StringValue("new description"),
			EnableH2:           types.BoolValue(false),
			SessionPersistence: serverGroupConfiguredModel().SessionPersistence,
			HealthCheck:        types.ObjectNull(serverGroupHealthCheckAttrTypes()),
		},
	}

	body, diags := buildServerGroupUpdateBody(context.Background(), plan, serverGroupConfiguredModel())
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "updated-name" || body.Algorithm != loadbalancersdk.ServerGroupAlgorithmLEASTCONNECTIONS ||
		body.Description != "new description" || core.NullableToPtr(body.EnableH2) == nil || body.EnableH2.MustGet() || !body.HealthCheck.IsNull() {
		t.Fatalf("unexpected update body: %#v", body)
	}
}

func TestBuildServerGroupUpdateBodyIsComplete(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		mutate func(*ServerGroupResourceModel)
		want   map[string]any
	}{
		{"rename with unknown computed values", func(m *ServerGroupResourceModel) {
			m.Description = types.StringUnknown()
			m.EnableH2 = types.BoolUnknown()
			m.SessionPersistence = types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes())
		}, map[string]any{"description": "console description", "enable_h2": true, "session_persistence.kind": "APP_COOKIE"}},
		{"rename with null computed values", func(m *ServerGroupResourceModel) {
			m.Description = types.StringNull()
			m.EnableH2 = types.BoolNull()
			m.SessionPersistence = types.ObjectNull(serverGroupSessionPersistenceAttrTypes())
		}, map[string]any{"description": "console description", "enable_h2": true, "session_persistence.kind": "APP_COOKIE"}},
		{"clear description", func(m *ServerGroupResourceModel) {
			m.Description = types.StringValue("")
		}, map[string]any{"description": "", "enable_h2": true, "session_persistence.kind": "APP_COOKIE"}},
		{"disable h2", func(m *ServerGroupResourceModel) {
			m.EnableH2 = types.BoolValue(false)
		}, map[string]any{"description": "console description", "enable_h2": false, "session_persistence.kind": "APP_COOKIE"}},
		{"disable persistence", func(m *ServerGroupResourceModel) {
			m.SessionPersistence = serverGroupConfiguredModel().SessionPersistence
		}, map[string]any{"description": "console description", "enable_h2": true, "session_persistence.kind": "NONE"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := serverGroupConfiguredModel()
			state.Description = types.StringValue("console description")
			state.EnableH2 = types.BoolValue(true)
			state.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
				"type": types.StringValue("APP_COOKIE"), "cookie_name": types.StringValue("SESSIONID"),
			})
			plan := state
			plan.Name = types.StringValue("renamed")
			tt.mutate(&plan)
			body, diags := buildServerGroupUpdateBody(context.Background(), plan, state)
			if diags.HasError() {
				t.Fatal(diags)
			}
			wire, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			persistence, _ := fields["session_persistence"].(map[string]any)
			got := map[string]any{"description": fields["description"], "enable_h2": fields["enable_h2"], "session_persistence.kind": persistence["kind"]}
			if len(fields) != 7 || fields["name"] != "renamed" || fields["algorithm"] != "ROUND_ROBIN" || fields["health_check"] != nil || fields["backend_tls_mode"] != nil {
				t.Fatalf("PUT body must carry every field: %s", wire)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Fatalf("%s = %#v, want %#v in %s", key, got[key], want, wire)
				}
			}
		})
	}
}

func TestBuildServerGroupUpdateBodyPreservesComputedValues(t *testing.T) {
	t.Parallel()
	state := serverGroupConfiguredModel()
	state.EnableH2 = types.BoolValue(true)
	state.Description = types.StringValue("console description")
	state.HealthCheck = serverGroupTCPHealthCheck()
	state.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("APP_COOKIE"), "cookie_name": types.StringValue("SESSIONID"),
	})
	plan := state
	plan.Name = types.StringValue("renamed")
	plan.Description = types.StringUnknown()
	plan.EnableH2 = types.BoolUnknown()
	plan.SessionPersistence = types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes())
	plan.HealthCheck = types.ObjectNull(serverGroupHealthCheckAttrTypes())
	body, diags := buildServerGroupUpdateBody(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if body.Description != "console description" || core.NullableToPtr(body.EnableH2) == nil || !body.EnableH2.MustGet() || !body.HealthCheck.IsNull() {
		t.Fatalf("full update lost prior values: %#v", body)
	}
	appCookie, err := body.SessionPersistence.AsServerGroupSessionPersistenceAppCookie()
	if err != nil || appCookie.SessionPersistenceCookieName != "SESSIONID" {
		t.Fatalf("lost persistence: %#v, %v", appCookie, err)
	}
	wire, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 7 || fields["description"] != "console description" || fields["enable_h2"] != true || fields["health_check"] != nil || fields["backend_tls_mode"] != nil {
		t.Fatalf("unexpected complete PUT body: %s", wire)
	}

	for _, name := range []string{"description", "session_persistence"} {
		t.Run("missing "+name, func(t *testing.T) {
			prior := state
			switch name {
			case "description":
				prior.Description = types.StringNull()
			case "session_persistence":
				prior.SessionPersistence = types.ObjectNull(serverGroupSessionPersistenceAttrTypes())
			}
			if _, diags := buildServerGroupUpdateBody(context.Background(), plan, prior); !diags.HasError() {
				t.Fatalf("a missing %s must not turn into a cleared value", name)
			}
		})
	}

	// The full PUT cannot omit enable_h2, and false would change the value,
	// so a null prior enable_h2 must reach the wire as JSON null.
	t.Run("null enable_h2", func(t *testing.T) {
		prior := state
		prior.EnableH2 = types.BoolNull()
		body, diags := buildServerGroupUpdateBody(context.Background(), plan, prior)
		if diags.HasError() {
			t.Fatalf("a null enable_h2 must not block the update: %v", diags)
		}
		if !body.EnableH2.IsNull() {
			t.Fatalf("enable_h2 = %#v, want null", body.EnableH2)
		}
		wire, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		h2, present := fields["enable_h2"]
		if len(fields) != 7 || !present || h2 != nil || fields["description"] != "console description" || fields["backend_tls_mode"] != nil {
			t.Fatalf("PUT body must send enable_h2 as null: %s", wire)
		}
	})

	t.Run("unknown enable_h2", func(t *testing.T) {
		prior := state
		prior.EnableH2 = types.BoolUnknown()
		if _, diags := buildServerGroupUpdateBody(context.Background(), plan, prior); !diags.HasError() {
			t.Fatal("an unknown enable_h2 must not be serialized")
		}
	})
}

// The inputs of session_persistence are Computed only so that an omitted
// object keeps its prior value. Terraform proposes the prior cookie_name when
// the configuration omits it and the Framework marks it unknown, so the plan
// must restore the configured null before the update body is built.
func TestServerGroupSessionPersistenceModifierPlansConfiguredInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prior := types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("APP_COOKIE"), "cookie_name": types.StringValue("SESSIONID"),
	})
	configured := types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue("SOURCE_IP"), "cookie_name": types.StringNull(),
	})
	for name, test := range map[string]struct {
		planCookie types.String
		state      types.Object
	}{
		"create":            {types.StringUnknown(), types.ObjectNull(serverGroupSessionPersistenceAttrTypes())},
		"update from prior": {types.StringValue("SESSIONID"), prior},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			planned := types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
				"type": types.StringValue("SOURCE_IP"), "cookie_name": test.planCookie,
			})
			response := planmodifier.ObjectResponse{PlanValue: planned}
			preserveServerGroupSessionPersistenceState().PlanModifyObject(ctx, planmodifier.ObjectRequest{
				ConfigValue: configured, PlanValue: planned, StateValue: test.state,
			}, &response)
			if response.Diagnostics.HasError() || !response.PlanValue.Equal(configured) {
				t.Fatalf("expected the configured persistence in the plan, got %#v", response)
			}
		})
	}

	t.Run("unknown configuration keeps the plan", func(t *testing.T) {
		t.Parallel()
		planned := types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes())
		response := planmodifier.ObjectResponse{PlanValue: planned}
		preserveServerGroupSessionPersistenceState().PlanModifyObject(ctx, planmodifier.ObjectRequest{
			ConfigValue: types.ObjectUnknown(serverGroupSessionPersistenceAttrTypes()), PlanValue: planned, StateValue: prior,
		}, &response)
		if response.Diagnostics.HasError() || !response.PlanValue.Equal(planned) {
			t.Fatalf("unknown configuration must keep the plan, got %#v", response)
		}
	})

	t.Run("update body sends the configured type", func(t *testing.T) {
		t.Parallel()
		state := serverGroupConfiguredModel()
		state.SessionPersistence = prior
		plan := state
		plan.SessionPersistence = configured
		body, diags := buildServerGroupUpdateBody(ctx, plan, state)
		if diags.HasError() {
			t.Fatalf("expected a persistence update, got %#v, %v", body, diags)
		}
		basic, err := body.SessionPersistence.AsServerGroupSessionPersistenceBasic()
		if err != nil || basic.Kind != loadbalancersdk.ServerGroupSessionPersistenceBasicKindSOURCEIP {
			t.Fatalf("unexpected persistence body: %#v, %v", basic, err)
		}
	})
}
