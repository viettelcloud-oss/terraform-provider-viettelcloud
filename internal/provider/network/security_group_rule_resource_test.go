package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	ruleTestID        = core.UUID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	ruleTestSGID      = core.UUID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20}
	ruleTestProjectID = core.UUID{0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f, 0x30}
	ruleTestRegionID  = core.UUID{0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f, 0x40}
)

func ruleSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	(&SecurityGroupRuleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

func nullSecurityGroupRuleResourceModel() SecurityGroupRuleResourceModel {
	return SecurityGroupRuleResourceModel{
		ID:                types.StringNull(),
		SecurityGroup:     types.ObjectNull(reference.AttributeTypes(reference.ID)),
		SecurityGroupName: types.StringNull(),
		Direction:         types.StringNull(),
		Protocol:          types.StringNull(),
		Ethertype:         types.StringNull(),
		PortRangeMin:      types.Int64Null(),
		PortRangeMax:      types.Int64Null(),
		RemoteIPPrefix:    types.StringNull(),
		Description:       types.StringNull(),
		Region:            types.StringNull(),
		CreatedAt:         types.StringNull(),
		UpdatedAt:         types.StringNull(),
	}
}

func mockRule(description string) *networksdk.SecurityGroupRuleSchema {
	return &networksdk.SecurityGroupRuleSchema{
		Id:             ruleTestID,
		Direction:      networksdk.SGRDirectionIngress,
		Protocol:       new(networksdk.SGRProtocolTcp),
		Ethertype:      new(networksdk.SGREtherTypeIpv4),
		PortRangeMin:   new(22),
		PortRangeMax:   new(22),
		RemoteIpPrefix: "0.0.0.0/0",
		Description:    new(description),
		SecurityGroup: networksdk.NestedSecurityGroupSchema{
			Id:   ruleTestSGID,
			Name: "test-sg",
		},
		Project: networksdk.NestedProjectSchema{
			Id:   ruleTestProjectID,
			Name: "test-project",
		},
		Region: networksdk.NestedRegionSchema{
			Id:   ruleTestRegionID,
			Name: "vn-central-1",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func ruleTestClient(t *testing.T, handler http.HandlerFunc) *networksdk.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return client
}

func writeRuleResponse(t *testing.T, w http.ResponseWriter, rule *networksdk.SecurityGroupRuleSchema) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(rule); err != nil {
		t.Fatalf("encode rule: %v", err)
	}
}

func TestSecurityGroupRuleModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	resourcePlan := tfsdk.Plan{Schema: ruleSchema(t)}
	if diags := resourcePlan.Set(context.Background(), &SecurityGroupRuleResourceModel{SecurityGroup: types.ObjectNull(reference.AttributeTypes(reference.ID))}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestSecurityGroupRuleResourceConstructorAndMetadata(t *testing.T) {
	t.Parallel()

	r := NewSecurityGroupRuleResource()
	if r == nil {
		t.Fatal("expected non-nil resource")
	}
	var resp resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_security_group_rule" {
		t.Fatalf("expected TypeName 'viettelcloud_security_group_rule', got %q", resp.TypeName)
	}
}

// networksdk.SecurityGroupRuleCreateSchema carries no ethertype field, so a
// configurable ethertype would plan a value the create request cannot send and
// fail the apply with an inconsistent result.
func TestSecurityGroupRuleEthertypeIsComputedOnly(t *testing.T) {
	t.Parallel()

	attribute, ok := ruleSchema(t).Attributes["ethertype"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatal("ethertype is not a string attribute")
	}
	if attribute.Optional || attribute.Required {
		t.Errorf("expected ethertype to be computed only, got Optional=%t Required=%t", attribute.Optional, attribute.Required)
	}
	if !attribute.Computed {
		t.Error("expected ethertype to be computed")
	}
}

func TestSecurityGroupRuleOptionalComputedAttributes(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	for _, name := range []string{"remote_ip_prefix", "description"} {
		attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if !attribute.Optional || !attribute.Computed {
			t.Errorf("expected %s to be optional and computed, got Optional=%t Computed=%t", name, attribute.Optional, attribute.Computed)
		}
	}

	// The backend may assign a port range the practitioner did not configure,
	// so both bounds must be computed to keep that value out of the plan.
	for _, name := range []string{"port_range_min", "port_range_max"} {
		attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.Int64Attribute)
		if !ok {
			t.Fatalf("%s is not an int64 attribute", name)
		}
		if !attribute.Optional || !attribute.Computed {
			t.Errorf("expected %s to be optional and computed, got Optional=%t Computed=%t", name, attribute.Optional, attribute.Computed)
		}
	}
}

func TestSecurityGroupRuleResourceConfigure(t *testing.T) {
	t.Parallel()

	r := &SecurityGroupRuleResource{}

	var respNil resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &respNil)
	if respNil.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil ProviderData: %v", respNil.Diagnostics)
	}

	var respInvalid resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &respInvalid)
	if !respInvalid.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid provider data type")
	}

	var respValid resource.ConfigureResponse
	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	configured := &providerdata.Configured{
		Network:   client,
		ProjectID: ruleTestProjectID,
	}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &respValid)
	if respValid.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on valid ProviderData: %v", respValid.Diagnostics)
	}
	if r.client != client || r.projectID != ruleTestProjectID {
		t.Fatalf("expected client and projectID to be configured, got client=%v, projectID=%v", r.client, r.projectID)
	}
}

func TestSecurityGroupRuleResourceValidateConfig(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	validConfig := func() SecurityGroupRuleResourceModel {
		config := nullSecurityGroupRuleResourceModel()
		config.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
		config.Direction = types.StringValue("ingress")
		config.Protocol = types.StringValue("tcp")
		return config
	}

	tests := []struct {
		name      string
		configure func(*SecurityGroupRuleResourceModel)
		wantError bool
	}{
		{
			name:      "valid minimal configuration",
			configure: func(*SecurityGroupRuleResourceModel) {},
		},
		{
			name: "untrimmed mixed-case direction and protocol",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Direction = types.StringValue("  INGRESS  ")
				config.Protocol = types.StringValue("  TCP  ")
			},
		},
		{
			name: "unknown direction is deferred to apply",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Direction = types.StringUnknown()
			},
		},
		{
			name: "invalid direction",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Direction = types.StringValue("sideways")
			},
			wantError: true,
		},
		{
			name: "invalid protocol",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Protocol = types.StringValue("unsupported-proto")
			},
			wantError: true,
		},
		{
			name: "protocol outside the common set stays valid",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Protocol = types.StringValue("ipv6-icmp")
			},
		},
		{
			name: "port above the maximum",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.PortRangeMax = types.Int64Value(70000)
			},
			wantError: true,
		},
		{
			name: "negative port",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.PortRangeMin = types.Int64Value(-1)
			},
			wantError: true,
		},
		{
			name: "port_range_min greater than port_range_max",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.PortRangeMin = types.Int64Value(100)
				config.PortRangeMax = types.Int64Value(50)
			},
			wantError: true,
		},
		{
			name: "equal port bounds",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.PortRangeMin = types.Int64Value(22)
				config.PortRangeMax = types.Int64Value(22)
			},
		},
		{
			name: "icmp rejects a configured port",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Protocol = types.StringValue("icmp")
				config.PortRangeMin = types.Int64Value(8)
			},
			wantError: true,
		},
		{
			name: "ipv6-icmp rejects a configured port",
			configure: func(config *SecurityGroupRuleResourceModel) {
				config.Protocol = types.StringValue("  IPV6-ICMP  ")
				config.PortRangeMax = types.Int64Value(0)
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			model := validConfig()
			tt.configure(&model)

			plan := tfsdk.Plan{Schema: ruleSchemaValue}
			if diags := plan.Set(context.Background(), &model); diags.HasError() {
				t.Fatalf("set config: %v", diags)
			}

			var resp resource.ValidateConfigResponse
			(&SecurityGroupRuleResource{}).ValidateConfig(
				context.Background(),
				resource.ValidateConfigRequest{Config: tfsdk.Config{Raw: plan.Raw, Schema: ruleSchemaValue}},
				&resp,
			)
			if got := resp.Diagnostics.HasError(); got != tt.wantError {
				t.Fatalf("expected error=%t, got error=%t (%v)", tt.wantError, got, resp.Diagnostics)
			}
		})
	}
}

func TestSecurityGroupRuleReplacementAttributes(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	state, plan := ruleModifierStateAndPlan(t, ruleSchemaValue)

	for _, name := range []string{"direction", "protocol"} {
		attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if len(attribute.PlanModifiers) != 1 {
			t.Fatalf("expected one plan modifier for %s, got %d", name, len(attribute.PlanModifiers))
		}

		request := planmodifier.StringRequest{
			State:      state,
			Plan:       plan,
			StateValue: types.StringValue("before"),
			PlanValue:  types.StringValue("after"),
		}
		var response planmodifier.StringResponse
		attribute.PlanModifiers[0].PlanModifyString(context.Background(), request, &response)
		if !response.RequiresReplace {
			t.Errorf("expected changing %s to require replacement", name)
		}
	}
}

func TestSecurityGroupRuleSecurityGroupReferenceSchemaAndReplacement(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	securityGroup := requiredReferenceAttribute(t, ruleSchemaValue, "security_group", reference.ID)
	before := reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	tests := map[string]struct {
		plan types.Object
		want bool
	}{
		"same id updates in place": {plan: before},
		"another id replaces":      {plan: reference.Value(reference.ID, types.StringValue(core.UUID{0x77}.String())), want: true},
		"unknown id replaces":      {plan: reference.Value(reference.ID, types.StringUnknown()), want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := referenceRequiresReplace(t, ruleSchemaValue, securityGroup, before, test.plan); got != test.want {
				t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
			}
		})
	}
}

func TestSecurityGroupRuleRequiresReplaceIfConfiguredAttributes(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	state, plan := ruleModifierStateAndPlan(t, ruleSchemaValue)

	attribute, ok := ruleSchemaValue.Attributes["remote_ip_prefix"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatal("remote_ip_prefix is not a string attribute")
	}
	modifier := attribute.PlanModifiers[len(attribute.PlanModifiers)-1]

	var respChange planmodifier.StringResponse
	modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:       state,
		Plan:        plan,
		ConfigValue: types.StringValue("10.0.0.0/8"),
		StateValue:  types.StringValue("0.0.0.0/0"),
		PlanValue:   types.StringValue("10.0.0.0/8"),
	}, &respChange)
	if !respChange.RequiresReplace {
		t.Error("expected changing configured remote_ip_prefix to require replacement")
	}

	var respOmitted planmodifier.StringResponse
	modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:       state,
		Plan:        plan,
		ConfigValue: types.StringNull(),
		StateValue:  types.StringValue("0.0.0.0/0"),
		PlanValue:   types.StringUnknown(),
	}, &respOmitted)
	if respOmitted.RequiresReplace {
		t.Error("expected omitting remote_ip_prefix not to require replacement")
	}

	for _, name := range []string{"port_range_min", "port_range_max"} {
		int64Attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.Int64Attribute)
		if !ok {
			t.Fatalf("%s is not an int64 attribute", name)
		}
		int64Modifier := int64Attribute.PlanModifiers[len(int64Attribute.PlanModifiers)-1]

		var respPortChange planmodifier.Int64Response
		int64Modifier.PlanModifyInt64(context.Background(), planmodifier.Int64Request{
			State:       state,
			Plan:        plan,
			ConfigValue: types.Int64Value(80),
			StateValue:  types.Int64Value(22),
			PlanValue:   types.Int64Value(80),
		}, &respPortChange)
		if !respPortChange.RequiresReplace {
			t.Errorf("expected changing configured %s to require replacement", name)
		}

		var respPortOmitted planmodifier.Int64Response
		int64Modifier.PlanModifyInt64(context.Background(), planmodifier.Int64Request{
			State:       state,
			Plan:        plan,
			ConfigValue: types.Int64Null(),
			StateValue:  types.Int64Value(22),
			PlanValue:   types.Int64Unknown(),
		}, &respPortOmitted)
		if respPortOmitted.RequiresReplace {
			t.Errorf("expected omitting %s not to require replacement", name)
		}
	}
}

// An unconfigured Optional and Computed attribute is planned as unknown on any
// change, so UseStateForUnknown must restore the prior value before
// RequiresReplaceIfConfigured sees it.
func TestSecurityGroupRuleUseStateForUnknownAttributes(t *testing.T) {
	t.Parallel()

	ruleSchemaValue := ruleSchema(t)
	state, plan := ruleModifierStateAndPlan(t, ruleSchemaValue)

	for _, name := range []string{"ethertype", "remote_ip_prefix"} {
		attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}

		var response planmodifier.StringResponse
		response.PlanValue = types.StringUnknown()
		attribute.PlanModifiers[0].PlanModifyString(context.Background(), planmodifier.StringRequest{
			State:       state,
			Plan:        plan,
			ConfigValue: types.StringNull(),
			StateValue:  types.StringValue("kept"),
			PlanValue:   types.StringUnknown(),
		}, &response)
		if response.PlanValue.IsUnknown() || response.PlanValue.ValueString() != "kept" {
			t.Errorf("expected %s to keep its state value, got %v", name, response.PlanValue)
		}
	}

	for _, name := range []string{"port_range_min", "port_range_max"} {
		attribute, ok := ruleSchemaValue.Attributes[name].(resourceschema.Int64Attribute)
		if !ok {
			t.Fatalf("%s is not an int64 attribute", name)
		}

		var response planmodifier.Int64Response
		response.PlanValue = types.Int64Unknown()
		attribute.PlanModifiers[0].PlanModifyInt64(context.Background(), planmodifier.Int64Request{
			State:       state,
			Plan:        plan,
			ConfigValue: types.Int64Null(),
			StateValue:  types.Int64Value(22),
			PlanValue:   types.Int64Unknown(),
		}, &response)
		if response.PlanValue.IsUnknown() || response.PlanValue.ValueInt64() != 22 {
			t.Errorf("expected %s to keep its state value, got %v", name, response.PlanValue)
		}
	}
}

func TestSecurityGroupRuleResourceCreateSuccess(t *testing.T) {
	t.Parallel()

	rule := mockRule("allow ssh")
	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", req.Method)
		}
		if !strings.HasSuffix(req.URL.Path, "/v2/network/security-group-rules/") {
			t.Errorf("expected path to end with /v2/network/security-group-rules/, got %s", req.URL.Path)
		}

		var body networksdk.SecurityGroupRuleCreateSchema
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		if body.SecurityGroupId != ruleTestSGID {
			t.Errorf("expected SGID %s, got %s", ruleTestSGID, body.SecurityGroupId)
		}
		if body.Direction != networksdk.SGRDirectionIngress {
			t.Errorf("expected direction 'ingress', got %q", body.Direction)
		}
		if body.Protocol == nil || *body.Protocol != networksdk.SGRProtocolTcp {
			t.Errorf("expected protocol 'tcp', got %v", body.Protocol)
		}

		w.WriteHeader(http.StatusCreated)
		writeRuleResponse(t, w, rule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	ruleSchemaValue := ruleSchema(t)

	planModel := nullSecurityGroupRuleResourceModel()
	planModel.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	planModel.Direction = types.StringValue("ingress")
	planModel.Protocol = types.StringValue("tcp")
	planModel.PortRangeMin = types.Int64Value(22)
	planModel.PortRangeMax = types.Int64Value(22)
	planModel.RemoteIPPrefix = types.StringValue("0.0.0.0/0")
	planModel.Description = types.StringValue("allow ssh")

	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: ruleSchemaValue}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create diagnostics: %v", createResp.Diagnostics)
	}

	var state SecurityGroupRuleResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != ruleTestID.String() {
		t.Errorf("expected ID %s, got %s", ruleTestID.String(), state.ID.ValueString())
	}
	if state.Direction.ValueString() != "ingress" {
		t.Errorf("expected direction 'ingress', got %s", state.Direction.ValueString())
	}
	if state.Ethertype.ValueString() != string(networksdk.SGREtherTypeIpv4) {
		t.Errorf("expected backend ethertype %q, got %q", networksdk.SGREtherTypeIpv4, state.Ethertype.ValueString())
	}
	if state.SecurityGroupName.ValueString() != "test-sg" {
		t.Errorf("expected security_group_name 'test-sg', got %q", state.SecurityGroupName.ValueString())
	}
	if state.Region.ValueString() != "vn-central-1" {
		t.Errorf("expected region 'vn-central-1', got %q", state.Region.ValueString())
	}
}

func TestSecurityGroupRuleResourceCreateOmitsUnconfiguredFields(t *testing.T) {
	t.Parallel()

	rule := mockRule("")
	rule.PortRangeMin = nil
	rule.PortRangeMax = nil
	rule.RemoteIpPrefix = ""

	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		var raw map[string]any
		if err := json.NewDecoder(req.Body).Decode(&raw); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		for _, field := range []string{"description", "remote_ip_prefix", "port_range_min", "port_range_max"} {
			if _, present := raw[field]; present {
				t.Errorf("expected %s to be absent from the create body, got %v", field, raw[field])
			}
		}
		w.WriteHeader(http.StatusCreated)
		writeRuleResponse(t, w, rule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	ruleSchemaValue := ruleSchema(t)

	planModel := nullSecurityGroupRuleResourceModel()
	planModel.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	planModel.Direction = types.StringValue("egress")
	planModel.Protocol = types.StringValue("any")
	planModel.PortRangeMin = types.Int64Unknown()
	planModel.PortRangeMax = types.Int64Unknown()
	planModel.RemoteIPPrefix = types.StringUnknown()
	planModel.Description = types.StringUnknown()

	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: ruleSchemaValue}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create diagnostics: %v", createResp.Diagnostics)
	}

	var state SecurityGroupRuleResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if !state.PortRangeMin.IsNull() || !state.PortRangeMax.IsNull() {
		t.Errorf("expected null port range in state, got %v..%v", state.PortRangeMin, state.PortRangeMax)
	}
	if !state.RemoteIPPrefix.IsNull() {
		t.Errorf("expected null remote_ip_prefix in state, got %v", state.RemoteIPPrefix)
	}
}

func TestSecurityGroupRuleResourceCreateInvalidPlan(t *testing.T) {
	t.Parallel()

	r := &SecurityGroupRuleResource{projectID: ruleTestProjectID}
	ruleSchemaValue := ruleSchema(t)

	planModel := nullSecurityGroupRuleResourceModel()
	planModel.SecurityGroup = reference.Value(reference.ID, types.StringValue("invalid-uuid"))
	planModel.Direction = types.StringValue("ingress")
	planModel.Protocol = types.StringValue("tcp")

	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: ruleSchemaValue}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "security_group.id") {
		t.Fatalf("expected security_group.id diagnostic for invalid UUID in plan, got %v", resp.Diagnostics)
	}
}

func TestSecurityGroupRuleResourceCreateReportsAPIError(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"server error"}`, http.StatusInternalServerError)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	ruleSchemaValue := ruleSchema(t)

	planModel := nullSecurityGroupRuleResourceModel()
	planModel.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	planModel.Direction = types.StringValue("ingress")
	planModel.Protocol = types.StringValue("tcp")

	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: ruleSchemaValue}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic on API error")
	}
}

func TestSecurityGroupRuleResourceReadSuccess(t *testing.T) {
	t.Parallel()

	rule := mockRule("read rule")
	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", req.Method)
		}
		if !strings.Contains(req.URL.Path, ruleTestID.String()) {
			t.Errorf("expected path to contain %s, got %s", ruleTestID.String(), req.URL.Path)
		}
		writeRuleResponse(t, w, rule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read diagnostics: %v", readResp.Diagnostics)
	}

	var afterRead SecurityGroupRuleResourceModel
	if diags := readResp.State.Get(context.Background(), &afterRead); diags.HasError() {
		t.Fatalf("get state after read: %v", diags)
	}
	if afterRead.Description.ValueString() != "read rule" {
		t.Errorf("expected description 'read rule', got %q", afterRead.Description.ValueString())
	}
	if afterRead.Region.ValueString() != "vn-central-1" {
		t.Errorf("expected region 'vn-central-1', got %q", afterRead.Region.ValueString())
	}
}

func TestSecurityGroupRuleResourceReadNotFoundRemovesResource(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on 404: %v", readResp.Diagnostics)
	}

	var afterRead SecurityGroupRuleResourceModel
	_ = readResp.State.Get(context.Background(), &afterRead)
	if !afterRead.ID.IsNull() {
		t.Fatalf("expected resource state to be removed, got ID %q", afterRead.ID.ValueString())
	}
}

func TestSecurityGroupRuleResourceReadReportsAPIError(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"internal error"}`, http.StatusInternalServerError)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic on 500")
	}
}

func TestSecurityGroupRuleResourceReadInvalidID(t *testing.T) {
	t.Parallel()

	r := &SecurityGroupRuleResource{projectID: ruleTestProjectID}
	state := ruleStateWithID(t, "invalid-uuid")

	readResp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid ID in Read")
	}
}

func TestSecurityGroupRuleResourceUpdateDescription(t *testing.T) {
	t.Parallel()

	updatedRule := mockRule("updated description")
	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", req.Method)
		}
		var body networksdk.SecurityGroupRulePartialUpdateSchema
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode patch body: %v", err)
		}
		if body.Description == nil || *body.Description != "updated description" {
			t.Errorf("expected description 'updated description', got %v", body.Description)
		}
		writeRuleResponse(t, w, updatedRule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	afterUpdate := runRuleUpdate(t, r, types.StringValue("old description"), types.StringValue("updated description"), false)
	if afterUpdate.Description.ValueString() != "updated description" {
		t.Errorf("expected description 'updated description', got %q", afterUpdate.Description.ValueString())
	}
}

// An explicit empty description clears the value, because the PATCH endpoint
// reads an empty string as clear and omission as preserve.
func TestSecurityGroupRuleResourceUpdateClearsDescription(t *testing.T) {
	t.Parallel()

	clearedRule := mockRule("")
	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", req.Method)
		}
		var body networksdk.SecurityGroupRulePartialUpdateSchema
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode patch body: %v", err)
		}
		if body.Description == nil || *body.Description != "" {
			t.Errorf("expected an empty description in the request, got %v", body.Description)
		}
		writeRuleResponse(t, w, clearedRule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	afterUpdate := runRuleUpdate(t, r, types.StringValue("old description"), types.StringValue(""), false)
	if afterUpdate.Description.IsNull() || afterUpdate.Description.IsUnknown() || afterUpdate.Description.ValueString() != "" {
		t.Errorf("expected a known empty description, got %#v", afterUpdate.Description)
	}
}

func TestSecurityGroupRuleResourceUpdateUnchangedDescriptionRefreshes(t *testing.T) {
	t.Parallel()

	rule := mockRule("same description")
	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("expected GET on unchanged description, got %s", req.Method)
		}
		writeRuleResponse(t, w, rule)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	runRuleUpdate(t, r, types.StringValue("same description"), types.StringValue("same description"), false)
}

func TestSecurityGroupRuleResourceUpdateInvalidID(t *testing.T) {
	t.Parallel()

	r := &SecurityGroupRuleResource{projectID: ruleTestProjectID}
	ruleSchemaValue := ruleSchema(t)

	stateModel := nullSecurityGroupRuleResourceModel()
	stateModel.ID = types.StringValue("invalid-uuid")
	stateModel.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	stateModel.Direction = types.StringValue("ingress")
	stateModel.Protocol = types.StringValue("tcp")
	stateModel.Description = types.StringValue("desc1")
	planModel := stateModel
	planModel.Description = types.StringValue("desc2")

	state := tfsdk.State{Schema: ruleSchemaValue}
	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	updateResp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid ID in Update")
	}
}

func TestSecurityGroupRuleResourceUpdateReportsAPIError(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"server error"}`, http.StatusInternalServerError)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	runRuleUpdate(t, r, types.StringValue("desc1"), types.StringValue("desc2"), true)
}

func TestSecurityGroupRuleResourceDeleteSuccess(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", req.Method)
		}
		if !strings.Contains(req.URL.Path, ruleTestID.String()) {
			t.Errorf("expected path to contain %s, got %s", ruleTestID.String(), req.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	deleteResp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("unexpected delete diagnostics: %v", deleteResp.Diagnostics)
	}
}

func TestSecurityGroupRuleResourceDeleteNotFoundSucceeds(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	deleteResp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("expected delete to treat 404 as success, got diagnostics: %v", deleteResp.Diagnostics)
	}
}

func TestSecurityGroupRuleResourceDeleteReportsAPIError(t *testing.T) {
	t.Parallel()

	client := ruleTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"server error"}`, http.StatusInternalServerError)
	})

	r := &SecurityGroupRuleResource{client: client, projectID: ruleTestProjectID}
	state := ruleStateWithID(t, ruleTestID.String())

	deleteResp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error on 500 during delete")
	}
}

func TestSecurityGroupRuleResourceDeleteInvalidID(t *testing.T) {
	t.Parallel()

	r := &SecurityGroupRuleResource{projectID: ruleTestProjectID}
	state := ruleStateWithID(t, "invalid-uuid")

	deleteResp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid ID in Delete")
	}
}

func TestSecurityGroupRuleResourceImportState(t *testing.T) {
	t.Parallel()

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: ruleSchema(t)}}
	if diags := resp.State.Set(context.Background(), &SecurityGroupRuleResourceModel{SecurityGroup: types.ObjectNull(reference.AttributeTypes(reference.ID))}); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	(&SecurityGroupRuleResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: ruleTestID.String()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", resp.Diagnostics)
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("get imported ID: %v", resp.Diagnostics)
	}
	if id.ValueString() != ruleTestID.String() {
		t.Errorf("expected ID %s, got %s", ruleTestID.String(), id.ValueString())
	}
}

func TestSecurityGroupRuleReadAfterImportFillsSecurityGroupReference(t *testing.T) {
	t.Parallel()

	rule := mockRule("imported")
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &SecurityGroupRuleResource{client: client, projectID: ruleTestSGID}
	}, rule.Id.String(), rule)

	var securityGroup types.Object
	if diags := state.GetAttribute(context.Background(), path.Root("security_group"), &securityGroup); diags.HasError() {
		t.Fatalf("read security_group: %v", diags)
	}
	if want := reference.Value(reference.ID, types.StringValue(rule.SecurityGroup.Id.String())); !securityGroup.Equal(want) {
		t.Fatalf("imported security_group = %s, want %s", securityGroup, want)
	}
}

func TestBuildSecurityGroupRuleCreateBody(t *testing.T) {
	t.Parallel()

	plan := nullSecurityGroupRuleResourceModel()
	plan.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	plan.Direction = types.StringValue("  INGRESS  ")
	plan.Protocol = types.StringValue("  TCP  ")
	plan.PortRangeMin = types.Int64Value(22)
	plan.PortRangeMax = types.Int64Value(22)
	plan.RemoteIPPrefix = types.StringValue("  0.0.0.0/0  ")
	plan.Description = types.StringValue("SSH access")

	body, diags := buildSecurityGroupRuleCreateBody(plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if body.SecurityGroupId != ruleTestSGID {
		t.Errorf("expected SGID %s, got %s", ruleTestSGID, body.SecurityGroupId)
	}
	if body.Direction != networksdk.SGRDirectionIngress {
		t.Errorf("expected direction 'ingress', got %q", body.Direction)
	}
	if body.Protocol == nil || *body.Protocol != networksdk.SGRProtocolTcp {
		t.Errorf("expected protocol 'tcp', got %v", body.Protocol)
	}
	if body.PortRangeMin == nil || *body.PortRangeMin != 22 {
		t.Errorf("expected port_range_min 22, got %v", body.PortRangeMin)
	}
	if body.PortRangeMax == nil || *body.PortRangeMax != 22 {
		t.Errorf("expected port_range_max 22, got %v", body.PortRangeMax)
	}
	if body.RemoteIpPrefix == nil || *body.RemoteIpPrefix != "0.0.0.0/0" {
		t.Errorf("expected remote_ip_prefix '0.0.0.0/0', got %v", body.RemoteIpPrefix)
	}
	if body.Description == nil || *body.Description != "SSH access" {
		t.Errorf("expected description 'SSH access', got %v", body.Description)
	}
}

func TestBuildSecurityGroupRuleCreateBodyOmitsUnsetFields(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		set  func(*SecurityGroupRuleResourceModel)
	}{
		{
			name: "null optional values",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.PortRangeMin = types.Int64Null()
				plan.PortRangeMax = types.Int64Null()
				plan.RemoteIPPrefix = types.StringNull()
				plan.Description = types.StringNull()
			},
		},
		{
			name: "unknown optional values",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.PortRangeMin = types.Int64Unknown()
				plan.PortRangeMax = types.Int64Unknown()
				plan.RemoteIPPrefix = types.StringUnknown()
				plan.Description = types.StringUnknown()
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := nullSecurityGroupRuleResourceModel()
			plan.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
			plan.Direction = types.StringValue("egress")
			plan.Protocol = types.StringValue("any")
			tt.set(&plan)

			body, diags := buildSecurityGroupRuleCreateBody(plan)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.Direction != networksdk.SGRDirectionEgress {
				t.Errorf("expected direction 'egress', got %q", body.Direction)
			}
			if body.Protocol == nil || *body.Protocol != networksdk.SGRProtocolAny {
				t.Errorf("expected protocol 'any', got %v", body.Protocol)
			}
			if body.PortRangeMin != nil || body.PortRangeMax != nil {
				t.Errorf("expected nil port range, got %v..%v", body.PortRangeMin, body.PortRangeMax)
			}
			if body.RemoteIpPrefix != nil {
				t.Errorf("expected nil remote_ip_prefix, got %v", body.RemoteIpPrefix)
			}
			if body.Description != nil {
				t.Errorf("expected nil description, got %v", body.Description)
			}
		})
	}
}

func TestBuildSecurityGroupRuleCreateBodySendsEmptyDescription(t *testing.T) {
	t.Parallel()

	plan := nullSecurityGroupRuleResourceModel()
	plan.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	plan.Direction = types.StringValue("ingress")
	plan.Protocol = types.StringValue("tcp")
	plan.Description = types.StringValue("")

	body, diags := buildSecurityGroupRuleCreateBody(plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Description == nil || *body.Description != "" {
		t.Errorf("expected an explicit empty description, got %v", body.Description)
	}
}

func TestBuildSecurityGroupRuleCreateBodyRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		set  func(*SecurityGroupRuleResourceModel)
	}{
		{
			name: "invalid security group ID",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.SecurityGroup = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
			},
		},
		{
			name: "missing direction",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Direction = types.StringNull()
			},
		},
		{
			name: "unknown direction",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Direction = types.StringUnknown()
			},
		},
		{
			name: "invalid direction",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Direction = types.StringValue("invalid-dir")
			},
		},
		{
			name: "missing protocol",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Protocol = types.StringNull()
			},
		},
		{
			name: "invalid protocol",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Protocol = types.StringValue("unsupported-proto")
			},
		},
		{
			name: "port_range_min greater than port_range_max",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.PortRangeMin = types.Int64Value(100)
				plan.PortRangeMax = types.Int64Value(50)
			},
		},
		{
			name: "port above the maximum becomes known at apply",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.PortRangeMax = types.Int64Value(70000)
			},
		},
		{
			name: "negative port becomes known at apply",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.PortRangeMin = types.Int64Value(-1)
			},
		},
		{
			name: "icmp port becomes known at apply",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Protocol = types.StringValue("icmp")
				plan.PortRangeMin = types.Int64Value(8)
			},
		},
		{
			name: "ipv6-icmp port becomes known at apply",
			set: func(plan *SecurityGroupRuleResourceModel) {
				plan.Protocol = types.StringValue("ipv6-icmp")
				plan.PortRangeMax = types.Int64Value(0)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			plan := nullSecurityGroupRuleResourceModel()
			plan.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
			plan.Direction = types.StringValue("ingress")
			plan.Protocol = types.StringValue("tcp")
			tt.set(&plan)

			if _, diags := buildSecurityGroupRuleCreateBody(plan); !diags.HasError() {
				t.Fatal("expected an error diagnostic")
			}
		})
	}
}

func TestBuildSecurityGroupRuleUpdateBody(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name            string
		planDescription types.String
		wantChanged     bool
		wantDescription *string
	}{
		{
			name:            "changed description is sent",
			planDescription: types.StringValue("updated description"),
			wantChanged:     true,
			wantDescription: new("updated description"),
		},
		{
			name:            "explicit empty description clears the value",
			planDescription: types.StringValue(""),
			wantChanged:     true,
			wantDescription: new(""),
		},
		{
			name:            "unchanged description sends nothing",
			planDescription: types.StringValue("old description"),
		},
		{
			// An unconfigured Optional and Computed description plans the prior
			// state value, so omission must never reach the request as an empty
			// string and silently clear the backend value.
			name:            "unconfigured description is omitted",
			planDescription: types.StringNull(),
		},
		{
			name:            "unknown description is omitted",
			planDescription: types.StringUnknown(),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := nullSecurityGroupRuleResourceModel()
			state.Description = types.StringValue("old description")
			plan := state
			plan.Description = tt.planDescription

			body, changed := buildSecurityGroupRuleUpdateBody(plan, state)
			if changed != tt.wantChanged {
				t.Fatalf("expected changed=%t, got %t", tt.wantChanged, changed)
			}
			switch {
			case tt.wantDescription == nil && body.Description != nil:
				t.Fatalf("expected no description in the request, got %q", *body.Description)
			case tt.wantDescription != nil && body.Description == nil:
				t.Fatalf("expected description %q, got none", *tt.wantDescription)
			case tt.wantDescription != nil && *body.Description != *tt.wantDescription:
				t.Fatalf("expected description %q, got %q", *tt.wantDescription, *body.Description)
			}
		})
	}
}

func TestPopulateSecurityGroupRuleModel(t *testing.T) {
	t.Parallel()

	rule := mockRule("test rule")
	var m SecurityGroupRuleModel
	populateSecurityGroupRuleModel(rule, &m)

	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{"id", m.ID.ValueString(), rule.Id.String()},
		{"security_group.id", reference.Get(m.SecurityGroup, reference.ID).ValueString(), rule.SecurityGroup.Id.String()},
		{"security_group_name", m.SecurityGroupName.ValueString(), rule.SecurityGroup.Name},
		{"direction", m.Direction.ValueString(), string(rule.Direction)},
		{"protocol", m.Protocol.ValueString(), string(*rule.Protocol)},
		{"ethertype", m.Ethertype.ValueString(), string(*rule.Ethertype)},
		{"remote_ip_prefix", m.RemoteIPPrefix.ValueString(), rule.RemoteIpPrefix},
		{"description", m.Description.ValueString(), *rule.Description},
		{"region", m.Region.ValueString(), rule.Region.Name},
		{"created_at", m.CreatedAt.ValueString(), rule.CreatedAt.Format(time.RFC3339)},
		{"updated_at", m.UpdatedAt.ValueString(), rule.UpdatedAt.Format(time.RFC3339)},
	} {
		if field.got != field.want {
			t.Errorf("expected %s %q, got %q", field.name, field.want, field.got)
		}
	}
	if m.PortRangeMin.ValueInt64() != int64(*rule.PortRangeMin) {
		t.Errorf("expected PortRangeMin %d, got %d", *rule.PortRangeMin, m.PortRangeMin.ValueInt64())
	}
	if m.PortRangeMax.ValueInt64() != int64(*rule.PortRangeMax) {
		t.Errorf("expected PortRangeMax %d, got %d", *rule.PortRangeMax, m.PortRangeMax.ValueInt64())
	}
}

func TestPopulateSecurityGroupRuleModelNilFields(t *testing.T) {
	t.Parallel()

	rule := mockRule("")
	rule.Protocol = nil
	rule.Ethertype = nil
	rule.PortRangeMin = nil
	rule.PortRangeMax = nil
	rule.RemoteIpPrefix = ""
	rule.Description = nil

	var m SecurityGroupRuleModel
	populateSecurityGroupRuleModel(rule, &m)

	if !m.Protocol.IsNull() {
		t.Errorf("expected Protocol null, got %v", m.Protocol)
	}
	if !m.Ethertype.IsNull() {
		t.Errorf("expected Ethertype null, got %v", m.Ethertype)
	}
	if !m.PortRangeMin.IsNull() {
		t.Errorf("expected PortRangeMin null, got %v", m.PortRangeMin)
	}
	if !m.PortRangeMax.IsNull() {
		t.Errorf("expected PortRangeMax null, got %v", m.PortRangeMax)
	}
	if !m.RemoteIPPrefix.IsNull() {
		t.Errorf("expected RemoteIPPrefix null, got %v", m.RemoteIPPrefix)
	}
	if !m.Description.IsNull() {
		t.Errorf("expected Description null, got %v", m.Description)
	}

	var fromNil SecurityGroupRuleModel
	populateSecurityGroupRuleModel(nil, &fromNil)
	if !fromNil.ID.IsNull() {
		t.Errorf("expected ID null, got %v", fromNil.ID)
	}
}

func TestPopulateSecurityGroupRuleResourceStatePreservesConfiguredRepresentations(t *testing.T) {
	t.Parallel()

	rule := mockRule("test rule")
	upperSecurityGroupID := strings.ToUpper(ruleTestSGID.String())

	state := nullSecurityGroupRuleResourceModel()
	state.SecurityGroup = reference.Value(reference.ID, types.StringValue(upperSecurityGroupID))
	state.Direction = types.StringValue("  INGRESS  ")
	state.Protocol = types.StringValue("  TCP  ")
	state.RemoteIPPrefix = types.StringValue("0.0.0.0/0")
	state.Description = types.StringValue("test rule")

	populateSecurityGroupRuleResourceState(rule, &state)

	if reference.Get(state.SecurityGroup, reference.ID).ValueString() != upperSecurityGroupID {
		t.Errorf("expected preserved SGID casing %q, got %q", upperSecurityGroupID, reference.Get(state.SecurityGroup, reference.ID).ValueString())
	}
	if state.Direction.ValueString() != "  INGRESS  " {
		t.Errorf("expected preserved direction %q, got %q", "  INGRESS  ", state.Direction.ValueString())
	}
	if state.Protocol.ValueString() != "  TCP  " {
		t.Errorf("expected preserved protocol %q, got %q", "  TCP  ", state.Protocol.ValueString())
	}
	if state.RemoteIPPrefix.ValueString() != "0.0.0.0/0" {
		t.Errorf("expected preserved remote_ip_prefix %q, got %q", "0.0.0.0/0", state.RemoteIPPrefix.ValueString())
	}
	if state.Description.ValueString() != "test rule" {
		t.Errorf("expected preserved description %q, got %q", "test rule", state.Description.ValueString())
	}
	// ethertype is computed only, so the backend value always wins.
	if state.Ethertype.ValueString() != string(networksdk.SGREtherTypeIpv4) {
		t.Errorf("expected backend ethertype %q, got %q", networksdk.SGREtherTypeIpv4, state.Ethertype.ValueString())
	}
}

// An import refresh has no configured representation to preserve, so every
// attribute must come from the response.
func TestPopulateSecurityGroupRuleResourceStateFillsNullStateAfterImport(t *testing.T) {
	t.Parallel()

	rule := mockRule("imported rule")
	state := nullSecurityGroupRuleResourceModel()
	populateSecurityGroupRuleResourceState(rule, &state)

	if reference.Get(state.SecurityGroup, reference.ID).ValueString() != rule.SecurityGroup.Id.String() {
		t.Errorf("expected security_group_id %q, got %q", rule.SecurityGroup.Id.String(), reference.Get(state.SecurityGroup, reference.ID).ValueString())
	}
	if state.SecurityGroupName.ValueString() != rule.SecurityGroup.Name {
		t.Errorf("expected security_group_name %q, got %q", rule.SecurityGroup.Name, state.SecurityGroupName.ValueString())
	}
	if state.Direction.ValueString() != string(rule.Direction) {
		t.Errorf("expected direction %q, got %q", rule.Direction, state.Direction.ValueString())
	}
	if state.Protocol.ValueString() != string(*rule.Protocol) {
		t.Errorf("expected protocol %q, got %q", *rule.Protocol, state.Protocol.ValueString())
	}
	if state.RemoteIPPrefix.ValueString() != rule.RemoteIpPrefix {
		t.Errorf("expected remote_ip_prefix %q, got %q", rule.RemoteIpPrefix, state.RemoteIPPrefix.ValueString())
	}
	if state.Region.ValueString() != rule.Region.Name {
		t.Errorf("expected region %q, got %q", rule.Region.Name, state.Region.ValueString())
	}
	if state.Description.ValueString() != *rule.Description {
		t.Errorf("expected description %q, got %q", *rule.Description, state.Description.ValueString())
	}
}

func TestPopulateSecurityGroupRuleResourceStateMapsDescriptionPresence(t *testing.T) {
	t.Parallel()

	rule := mockRule("")
	state := nullSecurityGroupRuleResourceModel()
	populateSecurityGroupRuleResourceState(rule, &state)

	if state.Description.IsNull() || state.Description.IsUnknown() || state.Description.ValueString() != "" {
		t.Fatalf("a cleared description must map to a known empty string, got %#v", state.Description)
	}

	rule.Description = nil
	state.Description = types.StringValue("")
	populateSecurityGroupRuleResourceState(rule, &state)
	if !state.Description.IsNull() {
		t.Fatalf("a nil SDK description must map to null, got %#v", state.Description)
	}
}

func ruleModifierStateAndPlan(t *testing.T, ruleSchemaValue resourceschema.Schema) (tfsdk.State, tfsdk.Plan) {
	t.Helper()

	model := nullSecurityGroupRuleResourceModel()
	state := tfsdk.State{Schema: ruleSchemaValue}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
	}
	return state, plan
}

func ruleStateWithID(t *testing.T, id string) tfsdk.State {
	t.Helper()

	model := nullSecurityGroupRuleResourceModel()
	model.ID = types.StringValue(id)
	model.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	model.Direction = types.StringValue("ingress")
	model.Protocol = types.StringValue("tcp")

	state := tfsdk.State{Schema: ruleSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func runRuleUpdate(
	t *testing.T,
	r *SecurityGroupRuleResource,
	stateDescription types.String,
	planDescription types.String,
	wantError bool,
) SecurityGroupRuleResourceModel {
	t.Helper()

	ruleSchemaValue := ruleSchema(t)
	stateModel := nullSecurityGroupRuleResourceModel()
	stateModel.ID = types.StringValue(ruleTestID.String())
	stateModel.SecurityGroup = reference.Value(reference.ID, types.StringValue(ruleTestSGID.String()))
	stateModel.Direction = types.StringValue("ingress")
	stateModel.Protocol = types.StringValue("tcp")
	stateModel.Description = stateDescription

	planModel := stateModel
	planModel.Description = planDescription

	state := tfsdk.State{Schema: ruleSchemaValue}
	plan := tfsdk.Plan{Schema: ruleSchemaValue}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	updateResp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if got := updateResp.Diagnostics.HasError(); got != wantError {
		t.Fatalf("expected update error=%t, got error=%t (%v)", wantError, got, updateResp.Diagnostics)
	}

	var afterUpdate SecurityGroupRuleResourceModel
	if !wantError {
		if diags := updateResp.State.Get(context.Background(), &afterUpdate); diags.HasError() {
			t.Fatalf("get state after update: %v", diags)
		}
	}
	return afterUpdate
}
