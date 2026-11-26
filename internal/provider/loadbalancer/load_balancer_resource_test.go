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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

// loadBalancerBusyBody is the HTTP 409 refusal that the backend answers while
// another operation holds the claim of the resource. The claim clears when
// that operation finishes.
const loadBalancerBusyBody = `{"errors":[{"location":"","code":"resource_busy","message":"load balancer is busy, please try again later."}],"request_id":"f1dbd828b10848f2b05d2a1795d3cc9a"}`

// busyRefusalSubject names the operation in busy refusal diagnostics.
const busyRefusalSubject = "the update of load balancer 11111111-1111-1111-1111-111111111111"

func TestLoadBalancerConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceUnderTest, ok := NewLoadBalancerResource().(*LoadBalancerResource)
	if !ok || resourceUnderTest == nil {
		t.Fatal("constructor did not return a LoadBalancerResource")
	}
	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_loadbalancer" {
		t.Fatalf("metadata type=%q", metadata.TypeName)
	}

	var response resource.ConfigureResponse
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unexpected provider data diagnostic")
	}

	clientResource := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}))
	configured := &providerdata.Configured{
		LoadBalancer: clientResource.client,
		Project:      clientResource.project,
		ProjectID:    loadBalancerTestProjectID,
		Claims:       keylock.New(),
	}
	response = resource.ConfigureResponse{}
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &response)
	if response.Diagnostics.HasError() || resourceUnderTest.client != configured.LoadBalancer || resourceUnderTest.project != configured.Project || resourceUnderTest.projectID != configured.ProjectID || resourceUnderTest.claims != configured.Claims {
		t.Fatalf("resource was not configured: diagnostics=%v resource=%#v", response.Diagnostics, resourceUnderTest)
	}
}

func TestLoadBalancerModelMatchesSchema(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	plan := tfsdk.Plan{Schema: resourceSchema}
	model := loadBalancerConfiguredModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestLoadBalancerSchemaContract(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	for _, name := range []string{"name", "package", "topology"} {
		attribute, ok := resourceSchema.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.Required || attribute.Computed || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be required and updateable, got %#v", name, resourceSchema.Attributes[name])
		}
		if name != "name" && len(attribute.Validators) != 1 {
			t.Errorf("%s must have one enum validator, got %#v", name, attribute.Validators)
		}
	}
	for name, criterion := range map[string]string{"region": reference.Name, "vpc": reference.ID} {
		attribute, ok := resourceSchema.Attributes[name].(schema.SingleNestedAttribute)
		if !ok || !attribute.Required || attribute.Computed || len(attribute.PlanModifiers) != 1 {
			t.Errorf("%s must be a required, create-only reference, got %#v", name, resourceSchema.Attributes[name])
		}
		assertLoadBalancerReferenceCriterion(t, name, attribute, criterion)
	}
	useStateForUnknownDescription := stringplanmodifier.UseStateForUnknown().Description(context.Background())
	description, ok := resourceSchema.Attributes["description"].(schema.StringAttribute)
	if !ok || !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 ||
		description.PlanModifiers[0].Description(context.Background()) != useStateForUnknownDescription {
		t.Errorf("description must be optional, computed, and UseStateForUnknown, got %#v", resourceSchema.Attributes["description"])
	}
	// An omitted reference stays null. The backend association appears in
	// the flat computed ID instead of populating the optional reference.
	for _, name := range []string{"private_ip", "qos_policy"} {
		attribute, ok := resourceSchema.Attributes[name].(schema.SingleNestedAttribute)
		if !ok || !attribute.Optional || attribute.Computed || len(attribute.PlanModifiers) != 1 {
			t.Errorf("%s must be an optional, non-computed reference, got %#v", name, resourceSchema.Attributes[name])
		}
		assertLoadBalancerReferenceCriterion(t, name, attribute, reference.ID)
	}
	loadBalancerType, ok := resourceSchema.Attributes["type"].(schema.StringAttribute)
	if !ok || !loadBalancerType.Optional || !loadBalancerType.Computed || len(loadBalancerType.Validators) != 1 || len(loadBalancerType.PlanModifiers) != 2 ||
		loadBalancerType.PlanModifiers[0].Description(context.Background()) != useStateForUnknownDescription ||
		loadBalancerType.PlanModifiers[1].Description(context.Background()) != replace.StringIfConfigured(compare.SameTrimmedString).Description(context.Background()) {
		t.Errorf("type must be optional, computed, UseStateForUnknown, then replace.StringIfConfigured, got %#v", resourceSchema.Attributes["type"])
	}
	// The association IDs carry no UseStateForUnknown, because the backend
	// fills private_ip_id only after the Load Balancer becomes ACTIVE.
	for _, name := range []string{"private_ip_id", "qos_policy_id", "region_id", "vpc_name", "qos_policy_name", "vip_address", "public_address", "v6_public_address", "status", "provisioning_status", "access_log_store_id", "access_log_store_name", "updated_at"} {
		attribute, ok := resourceSchema.Attributes[name].(schema.StringAttribute)
		if !ok || attribute.Optional || attribute.Required || !attribute.Computed || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be computed-only without plan modifiers, got %#v", name, resourceSchema.Attributes[name])
		}
	}
	accessLogEnabled, ok := resourceSchema.Attributes["access_log_enabled"].(schema.BoolAttribute)
	if !ok || accessLogEnabled.Optional || accessLogEnabled.Required || !accessLogEnabled.Computed {
		t.Errorf("access_log_enabled must be computed-only, got %#v", resourceSchema.Attributes["access_log_enabled"])
	}
	// An omitted public_ip_mode turns public IP off, so it stays null in state
	// instead of keeping a backend value.
	publicIPMode, ok := resourceSchema.Attributes["public_ip_mode"].(schema.StringAttribute)
	if !ok || !publicIPMode.Optional || publicIPMode.Required || publicIPMode.Computed || len(publicIPMode.PlanModifiers) != 0 || len(publicIPMode.Validators) != 1 {
		t.Errorf("public_ip_mode must be optional, non-computed, updateable in place, and validated, got %#v", resourceSchema.Attributes["public_ip_mode"])
	}
	for _, text := range []string{"`ipv4-only`", "`dual-stack`", "Omitting it turns public IP off", "cannot change to `ipv4-only`"} {
		if !strings.Contains(publicIPMode.Description, text) {
			t.Errorf("public_ip_mode description must contain %q, got %q", text, publicIPMode.Description)
		}
	}
	// The baseline contract leaves child objects to separate resources.
	for _, name := range []string{"listeners", "server_groups"} {
		if _, ok := resourceSchema.Attributes[name]; ok {
			t.Errorf("%s is outside the baseline contract", name)
		}
	}
}

func TestLoadBalancerEnumValidation(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	for _, tt := range []struct {
		name      string
		attribute string
		value     types.String
		wantError bool
	}{
		{name: "package", attribute: "package", value: types.StringValue("MEDIUM")},
		{name: "padded package", attribute: "package", value: types.StringValue(" MEDIUM ")},
		{name: "blank package", attribute: "package", value: types.StringValue("  "), wantError: true},
		{name: "invalid package", attribute: "package", value: types.StringValue("small"), wantError: true},
		{name: "topology", attribute: "topology", value: types.StringValue("ACTIVE_STANDBY")},
		{name: "padded topology", attribute: "topology", value: types.StringValue("\tSINGLE ")},
		{name: "invalid topology", attribute: "topology", value: types.StringValue("HA"), wantError: true},
		{name: "type", attribute: "type", value: types.StringValue("native")},
		{name: "padded type", attribute: "type", value: types.StringValue(" native\n")},
		{name: "invalid type", attribute: "type", value: types.StringValue("APPLIANCE"), wantError: true},
		{name: "null type", attribute: "type", value: types.StringNull()},
		{name: "unknown package", attribute: "package", value: types.StringUnknown()},
		{name: "public IP mode", attribute: "public_ip_mode", value: types.StringValue("ipv4-only")},
		{name: "dual-stack public IP mode", attribute: "public_ip_mode", value: types.StringValue("dual-stack")},
		{name: "invalid public IP mode", attribute: "public_ip_mode", value: types.StringValue("IPV4-ONLY"), wantError: true},
		{name: "empty public IP mode", attribute: "public_ip_mode", value: types.StringValue(""), wantError: true},
		{name: "padded public IP mode", attribute: "public_ip_mode", value: types.StringValue(" dual-stack ")},
		{name: "blank public IP mode", attribute: "public_ip_mode", value: types.StringValue("  "), wantError: true},
		{name: "padded invalid public IP mode", attribute: "public_ip_mode", value: types.StringValue(" IPV4-ONLY "), wantError: true},
		{name: "null public IP mode", attribute: "public_ip_mode", value: types.StringNull()},
		{name: "unknown public IP mode", attribute: "public_ip_mode", value: types.StringUnknown()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attribute := resourceSchema.Attributes[tt.attribute].(schema.StringAttribute)
			var response validator.StringResponse
			for _, attributeValidator := range attribute.Validators {
				attributeValidator.ValidateString(context.Background(), validator.StringRequest{
					Path:        path.Root(tt.attribute),
					ConfigValue: tt.value,
				}, &response)
			}
			if response.Diagnostics.HasError() != tt.wantError {
				t.Fatalf("diagnostics=%v, want error=%t", response.Diagnostics, tt.wantError)
			}
		})
	}
}

func TestLoadBalancerOptionalReferencePlanModification(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	associated := loadBalancerTestPrivateIPID
	other := loadBalancerTestQoSID
	tests := []struct {
		name            string
		config          types.Object
		state           types.Object
		associated      types.String
		wantReplacement bool
	}{
		{
			name:       "omission keeps the Load Balancer",
			config:     types.ObjectNull(reference.AttributeTypes(reference.ID)),
			state:      reference.Value(reference.ID, types.StringValue(associated.String())),
			associated: types.StringValue(associated.String()),
		},
		{
			name:       "configuring the associated object after omission",
			config:     reference.Value(reference.ID, types.StringValue(strings.ToUpper(associated.String()))),
			state:      types.ObjectNull(reference.AttributeTypes(reference.ID)),
			associated: types.StringValue(associated.String()),
		},
		{
			name:       "padded associated object keeps the Load Balancer",
			config:     reference.Value(reference.ID, types.StringValue(" "+associated.String()+"\n")),
			state:      reference.Value(reference.ID, types.StringValue(associated.String())),
			associated: types.StringValue(associated.String()),
		},
		{
			name:            "configuring another object replaces",
			config:          reference.Value(reference.ID, types.StringValue(other.String())),
			state:           reference.Value(reference.ID, types.StringValue(associated.String())),
			associated:      types.StringValue(associated.String()),
			wantReplacement: true,
		},
		{
			name:            "configuring an object without an association replaces",
			config:          reference.Value(reference.ID, types.StringValue(associated.String())),
			state:           types.ObjectNull(reference.AttributeTypes(reference.ID)),
			associated:      types.StringNull(),
			wantReplacement: true,
		},
		{
			name:            "unknown configured object replaces",
			config:          reference.Value(reference.ID, types.StringUnknown()),
			state:           reference.Value(reference.ID, types.StringValue(associated.String())),
			associated:      types.StringValue(associated.String()),
			wantReplacement: true,
		},
	}

	for _, attributeName := range []string{"private_ip", "qos_policy"} {
		attribute := resourceSchema.Attributes[attributeName].(schema.SingleNestedAttribute)
		for _, tt := range tests {
			t.Run(attributeName+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				stateModel.PrivateIP = tt.state
				stateModel.PrivateIPID = tt.associated
				stateModel.QoSPolicy = tt.state
				stateModel.QoSPolicyID = tt.associated
				state := tfsdk.State{Schema: resourceSchema}
				if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}
				request := planmodifier.ObjectRequest{
					Path:        path.Root(attributeName),
					State:       state,
					Plan:        loadBalancerModifierPlan(t, resourceSchema),
					ConfigValue: tt.config,
					PlanValue:   tt.config,
					StateValue:  tt.state,
				}
				response := planmodifier.ObjectResponse{PlanValue: tt.config}
				for _, modifier := range attribute.PlanModifiers {
					modifier.PlanModifyObject(context.Background(), request, &response)
				}
				if response.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
				}
				if response.RequiresReplace != tt.wantReplacement {
					t.Fatalf("replace=%t, want %t", response.RequiresReplace, tt.wantReplacement)
				}
			})
		}
	}
}

func TestLoadBalancerRequiredCreateOnlyPlanModification(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	vpcID := loadBalancerTestVPCID.String()
	for _, tt := range []struct {
		name, attribute, criterion, state, plan string
		wantReplacement                         bool
	}{
		{name: "region change", attribute: "region", criterion: reference.Name, state: "vn-central-1", plan: "vn-north-1", wantReplacement: true},
		{name: "padded region", attribute: "region", criterion: reference.Name, state: "vn-central-1", plan: " vn-central-1\t"},
		{name: "vpc change", attribute: "vpc", criterion: reference.ID, state: vpcID, plan: loadBalancerTestQoSID.String(), wantReplacement: true},
		{name: "padded upper-case vpc", attribute: "vpc", criterion: reference.ID, state: vpcID, plan: "  " + strings.ToUpper(vpcID) + " "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attribute := resourceSchema.Attributes[tt.attribute].(schema.SingleNestedAttribute)
			request := planmodifier.ObjectRequest{
				Path:        path.Root(tt.attribute),
				State:       loadBalancerModifierState(t, resourceSchema),
				Plan:        loadBalancerModifierPlan(t, resourceSchema),
				ConfigValue: reference.Value(tt.criterion, types.StringValue(tt.plan)),
				PlanValue:   reference.Value(tt.criterion, types.StringValue(tt.plan)),
				StateValue:  reference.Value(tt.criterion, types.StringValue(tt.state)),
			}
			response := planmodifier.ObjectResponse{PlanValue: request.PlanValue}
			for _, modifier := range attribute.PlanModifiers {
				modifier.PlanModifyObject(context.Background(), request, &response)
			}
			if response.RequiresReplace != tt.wantReplacement {
				t.Fatalf("replace=%t, want %t", response.RequiresReplace, tt.wantReplacement)
			}
		})
	}
}

func TestLoadBalancerTypePlanModification(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	attribute := resourceSchema.Attributes["type"].(schema.StringAttribute)
	appliance := types.StringValue("appliance")
	for _, tt := range []struct {
		name            string
		config          types.String
		plan            types.String
		wantPlan        types.String
		wantReplacement bool
	}{
		{name: "omission keeps the backend type", config: types.StringNull(), plan: types.StringUnknown(), wantPlan: appliance},
		{name: "configuring the backend type", config: appliance, plan: appliance, wantPlan: appliance},
		{name: "padded backend type keeps the Load Balancer", config: types.StringValue(" appliance "), plan: types.StringValue(" appliance "), wantPlan: types.StringValue(" appliance ")},
		{name: "configuring another type replaces", config: types.StringValue("native"), plan: types.StringValue("native"), wantPlan: types.StringValue("native"), wantReplacement: true},
		{name: "unknown configured type replaces", config: types.StringUnknown(), plan: types.StringUnknown(), wantPlan: types.StringUnknown(), wantReplacement: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := planmodifier.StringRequest{
				Path:        path.Root("type"),
				State:       loadBalancerModifierState(t, resourceSchema),
				Plan:        loadBalancerModifierPlan(t, resourceSchema),
				ConfigValue: tt.config,
				PlanValue:   tt.plan,
				StateValue:  appliance,
			}
			response := planmodifier.StringResponse{PlanValue: tt.plan}
			for _, modifier := range attribute.PlanModifiers {
				request.PlanValue = response.PlanValue
				modifier.PlanModifyString(context.Background(), request, &response)
			}
			if response.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
			}
			if !response.PlanValue.Equal(tt.wantPlan) || response.RequiresReplace != tt.wantReplacement {
				t.Fatalf("plan=%v replace=%t, want %v and %t", response.PlanValue, response.RequiresReplace, tt.wantPlan, tt.wantReplacement)
			}
		})
	}
}

func TestLoadBalancerDescriptionUseStateForUnknown(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	attribute := resourceSchema.Attributes["description"].(schema.StringAttribute)
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("description must have exactly one plan modifier, got %#v", attribute.PlanModifiers)
	}
	modifier := attribute.PlanModifiers[0]
	state := loadBalancerModifierState(t, resourceSchema)
	plan := loadBalancerModifierPlan(t, resourceSchema)
	knownState := types.StringValue("existing-value")

	// An unknown planned value (the Framework's default for a Computed
	// attribute left unconfigured) must fall back to the prior state
	// value instead of forcing an update on every plan.
	response := planmodifier.StringResponse{PlanValue: types.StringUnknown()}
	modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:       state,
		Plan:        plan,
		ConfigValue: types.StringNull(),
		PlanValue:   types.StringUnknown(),
		StateValue:  knownState,
	}, &response)
	if !response.PlanValue.Equal(knownState) {
		t.Errorf("unknown plan with known state = %v, want %v", response.PlanValue, knownState)
	}

	// A configured value, whether a change or an explicit clear, must
	// reach Update unmodified rather than being pinned to prior state.
	configured := types.StringValue("configured-value")
	response = planmodifier.StringResponse{PlanValue: configured}
	modifier.PlanModifyString(context.Background(), planmodifier.StringRequest{
		State:       state,
		Plan:        plan,
		ConfigValue: configured,
		PlanValue:   configured,
		StateValue:  knownState,
	}, &response)
	if !response.PlanValue.Equal(configured) {
		t.Errorf("configured plan value = %v, want %v", response.PlanValue, configured)
	}
}

// A dual-stack Load Balancer is on a dual-stack region, so ModifyPlan rejects
// ipv4-only unless the plan also changes region. A same-region replacement
// keeps the diagnostic, and an unknown region or mode defers it to Update.
func TestLoadBalancerModifyPlanRejectsDualStackToIPv4Only(t *testing.T) {
	t.Parallel()

	dualStack, ipv4Only := types.StringValue("dual-stack"), types.StringValue("ipv4-only")
	for _, tt := range []struct {
		name                string
		stateMode, planMode types.String
		plan                func(*LoadBalancerResourceModel)
		create, destroy     bool
		wantError           bool
	}{
		{name: "dual-stack to ipv4-only", stateMode: dualStack, planMode: ipv4Only, wantError: true},
		{name: "padded dual-stack to padded ipv4-only", stateMode: types.StringValue(" dual-stack "), planMode: types.StringValue(" ipv4-only "), wantError: true},
		{name: "dual-stack respelled with outer whitespace", stateMode: dualStack, planMode: types.StringValue(" dual-stack ")},
		{name: "null to ipv4-only", stateMode: types.StringNull(), planMode: ipv4Only},
		{name: "ipv4-only unchanged", stateMode: ipv4Only, planMode: ipv4Only},
		{name: "ipv4-only to dual-stack", stateMode: ipv4Only, planMode: dualStack},
		{name: "dual-stack unchanged", stateMode: dualStack, planMode: dualStack},
		{name: "dual-stack turned off", stateMode: dualStack, planMode: types.StringNull()},
		{
			name: "same-region replacement by vpc", stateMode: dualStack, planMode: ipv4Only, wantError: true,
			plan: func(m *LoadBalancerResourceModel) {
				m.VPC = reference.Value(reference.ID, types.StringValue("99999999-9999-9999-9999-999999999999"))
			},
		},
		{
			name: "same-region replacement by qos_policy", stateMode: dualStack, planMode: ipv4Only, wantError: true,
			plan: func(m *LoadBalancerResourceModel) {
				m.QoSPolicy = reference.Value(reference.ID, types.StringValue("99999999-9999-9999-9999-999999999999"))
			},
		},
		{
			name: "region change", stateMode: dualStack, planMode: ipv4Only,
			plan: func(m *LoadBalancerResourceModel) {
				m.Region = reference.Value(reference.Name, types.StringValue("vn-south-1"))
			},
		},
		{
			name: "unknown region", stateMode: dualStack, planMode: ipv4Only,
			plan: func(m *LoadBalancerResourceModel) {
				m.Region = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
			},
		},
		{
			name: "unknown region name", stateMode: dualStack, planMode: ipv4Only,
			plan: func(m *LoadBalancerResourceModel) {
				m.Region = reference.Value(reference.Name, types.StringUnknown())
			},
		},
		{name: "unknown mode", stateMode: dualStack, planMode: types.StringUnknown()},
		{name: "create", stateMode: dualStack, planMode: ipv4Only, create: true},
		{name: "destroy", stateMode: dualStack, planMode: ipv4Only, destroy: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceSchema := loadBalancerSchema(t)
			nullValue := tftypes.NewValue(resourceSchema.Type().TerraformType(context.Background()), nil)
			stateModel := loadBalancerConfiguredModel()
			stateModel.ID = types.StringValue(loadBalancerTestID.String())
			stateModel.PublicIPMode = tt.stateMode
			planModel := stateModel
			planModel.PublicIPMode = tt.planMode
			if tt.plan != nil {
				tt.plan(&planModel)
			}
			state := tfsdk.State{Schema: resourceSchema, Raw: nullValue}
			if !tt.create {
				if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}
			}
			plan := tfsdk.Plan{Schema: resourceSchema, Raw: nullValue}
			if !tt.destroy {
				if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
					t.Fatalf("set plan: %v", diags)
				}
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			(&LoadBalancerResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: resourceSchema, Raw: plan.Raw},
				Plan:   plan,
				State:  state,
			}, &response)
			if !tt.wantError {
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
				return
			}
			assertLoadBalancerDiagnostic(t, response.Diagnostics, "Unsupported public_ip_mode change")
			withPath, ok := response.Diagnostics[0].(diag.DiagnosticWithPath)
			if !ok || !withPath.Path().Equal(path.Root("public_ip_mode")) {
				t.Fatalf("diagnostic=%v, want it at public_ip_mode", response.Diagnostics[0])
			}
			for _, want := range []string{"dual-stack region", `Keep "dual-stack"`, "remove public_ip_mode"} {
				if !strings.Contains(response.Diagnostics[0].Detail(), want) {
					t.Fatalf("diagnostic detail %q does not contain %q", response.Diagnostics[0].Detail(), want)
				}
			}
		})
	}
}

func TestLoadBalancerCreateUsesSDKAndWaits(t *testing.T) {
	t.Parallel()

	getCalls := 0
	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Project-ID") != "" && req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
			t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
			return loadBalancerResponse(req, http.StatusOK, fmt.Sprintf(`{"count":1,"next":null,"previous":null,"results":[{"region":{"id":%q,"name":"vn-central-1","description":""},"services":[]}]}`, loadBalancerTestRegionID.String())), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			if req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			for _, name := range []string{"name", "description", "package", "topology", "region_id", "vpc_id", "private_ip_id", "qos_policy_id"} {
				if _, ok := body[name]; !ok {
					t.Errorf("create body omitted %s: %s", name, body)
				}
			}
			for _, name := range []string{"type", "listeners", "server_groups"} {
				if _, ok := body[name]; ok {
					t.Errorf("create body must not send %s: %s", name, body)
				}
			}
			return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
			getCalls++
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})
	resourceUnderTest := newLoadBalancerTestResource(t, transport)
	planModel := loadBalancerCreatePlanModel()
	plan := tfsdk.Plan{Schema: loadBalancerSchema(t)}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: loadBalancerSchema(t)}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected create diagnostics: %v", response.Diagnostics)
	}
	if getCalls != 1 {
		t.Fatalf("waiter GETs=%d, want 1", getCalls)
	}
	var state LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read create state: %v", diags)
	}
	if state.ID.ValueString() != loadBalancerTestID.String() || !state.PublicAddress.IsNull() || !state.PublicIPMode.IsNull() || state.ProvisioningStatus.ValueString() != "ACTIVE" || state.VIPAddress.ValueString() != "10.0.0.10" {
		t.Fatalf("unexpected create state: %#v", state)
	}
}

// The API trims request strings, so Create sends trimmed values and state
// keeps the configured spelling through Create and the next Read.
func TestLoadBalancerCreateTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	padded := func(value string) string { return " " + value + "\n" }
	type createBody struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Package     string  `json:"package"`
		Topology    string  `json:"topology"`
		Type        *string `json:"type"`
		VPCID       string  `json:"vpc_id"`
		PrivateIPID string  `json:"private_ip_id"`
		QoSPolicyID string  `json:"qos_policy_id"`
	}
	var sent []createBody
	var regionNames []string
	transport := loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
			regionNames = append(regionNames, req.URL.Query().Get("name"))
			return loadBalancerRegionResponse(req), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			var body createBody
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			sent = append(sent, body)
			return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})
	resourceUnderTest := newLoadBalancerTestResource(t, transport)
	planModel := loadBalancerCreatePlanModel()
	planModel.Description = types.StringValue(padded("Managed by Terraform"))
	planModel.Package = types.StringValue(padded("SMALL"))
	planModel.Topology = types.StringValue(padded("SINGLE"))
	planModel.Type = types.StringValue(padded("appliance"))
	planModel.VPC = reference.Value(reference.ID, types.StringValue(padded(loadBalancerTestVPCID.String())))
	planModel.PrivateIP = reference.Value(reference.ID, types.StringValue(padded(loadBalancerTestPrivateIPID.String())))
	planModel.QoSPolicy = reference.Value(reference.ID, types.StringValue(padded(loadBalancerTestQoSID.String())))
	plan := tfsdk.Plan{Schema: loadBalancerSchema(t)}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: loadBalancerSchema(t)}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected create diagnostics: %v", response.Diagnostics)
	}
	if want := []string{"vn-central-1"}; !slices.Equal(regionNames, want) {
		t.Fatalf("region lookups = %q, want %q", regionNames, want)
	}
	if len(sent) != 1 {
		t.Fatalf("create requests = %d, want 1", len(sent))
	}
	got := sent[0]
	if got.Name != "terraform-lb" || got.Description == nil || *got.Description != "Managed by Terraform" ||
		got.Package != "SMALL" || got.Topology != "SINGLE" || got.Type == nil || *got.Type != "appliance" ||
		got.VPCID != loadBalancerTestVPCID.String() || got.PrivateIPID != loadBalancerTestPrivateIPID.String() || got.QoSPolicyID != loadBalancerTestQoSID.String() {
		t.Fatalf("create body was not trimmed: %#v", got)
	}

	assertConfiguredSpelling := func(stage string, state tfsdk.State) {
		t.Helper()
		var model LoadBalancerResourceModel
		if diags := state.Get(context.Background(), &model); diags.HasError() {
			t.Fatalf("read %s state: %v", stage, diags)
		}
		for name, values := range map[string][2]types.String{
			"name":          {model.Name, planModel.Name},
			"description":   {model.Description, planModel.Description},
			"package":       {model.Package, planModel.Package},
			"topology":      {model.Topology, planModel.Topology},
			"type":          {model.Type, planModel.Type},
			"region.name":   {reference.Get(model.Region, reference.Name), reference.Get(planModel.Region, reference.Name)},
			"vpc.id":        {reference.Get(model.VPC, reference.ID), reference.Get(planModel.VPC, reference.ID)},
			"private_ip.id": {reference.Get(model.PrivateIP, reference.ID), reference.Get(planModel.PrivateIP, reference.ID)},
			"qos_policy.id": {reference.Get(model.QoSPolicy, reference.ID), reference.Get(planModel.QoSPolicy, reference.ID)},
		} {
			if !values[0].Equal(values[1]) {
				t.Errorf("%s state %s = %s, want %s", stage, name, values[0], values[1])
			}
		}
	}
	assertConfiguredSpelling("create", response.State)

	read := resource.ReadResponse{State: response.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: response.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("unexpected read diagnostics: %v", read.Diagnostics)
	}
	assertConfiguredSpelling("read", read.State)
}

func TestLoadBalancerCreateReportsAPIErrors(t *testing.T) {
	t.Parallel()

	t.Run("create request", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
				return loadBalancerRegionResponse(req), nil
			case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
				return loadBalancerResponse(req, http.StatusInternalServerError, `{"detail":"create failed"}`), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error creating Load Balancer")
	})

	// The backend checks no claim before it creates a Load Balancer, so a
	// busy refusal of the POST is an unknown answer and is never retried.
	t.Run("busy refusal of the create request", func(t *testing.T) {
		t.Parallel()
		posts := 0
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
				return loadBalancerRegionResponse(req), nil
			case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
				posts++
				return loadBalancerResponse(req, http.StatusConflict, loadBalancerBusyBody), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error creating Load Balancer")
		if posts != 1 {
			t.Fatalf("POST sent %d times, want once", posts)
		}
	})
}

func TestLoadBalancerCreateTimeoutPreservesKnownState(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
				return loadBalancerRegionResponse(req), nil
			case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
				return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
			case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
				return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		start := time.Now()
		response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
		if elapsed := time.Since(start); elapsed != loadBalancerReadyTimeout {
			t.Fatalf("create waited %s, want %s", elapsed, loadBalancerReadyTimeout)
		}
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error waiting for Load Balancer to be ready")
		if !response.State.Raw.IsFullyKnown() {
			t.Fatalf("create timeout state contains unknown values: %s", response.State.Raw)
		}
		var state LoadBalancerResourceModel
		if diags := response.State.Get(context.Background(), &state); diags.HasError() || state.ID.ValueString() != loadBalancerTestID.String() ||
			state.PrivateIPID.ValueString() != loadBalancerTestPrivateIPID.String() ||
			state.QoSPolicyID.ValueString() != loadBalancerTestQoSID.String() {
			t.Fatalf("unexpected preserved state: %#v diagnostics=%v", state, diags)
		}
	})
}

// The backend can report a configured association only once the Load Balancer
// is ACTIVE. The configured references must survive the create response that
// lacks them, or Terraform rejects the state as inconsistent with the plan.
func TestLoadBalancerCreateKeepsReferencesMissingFromCreateResponse(t *testing.T) {
	t.Parallel()

	created := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)
	created.PrivateIpId = nil
	created.QosPolicy = nil
	createdBody := loadBalancerJSON(t, created)
	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
			return loadBalancerRegionResponse(req), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			return loadBalancerResponse(req, http.StatusCreated, createdBody), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	}))
	planModel := loadBalancerCreatePlanModel()
	planModel.PrivateIP = reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestPrivateIPID.String())))
	response := executeLoadBalancerCreate(t, resourceUnderTest, planModel)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
	var state LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read create state: %v", diags)
	}
	if !state.PrivateIP.Equal(planModel.PrivateIP) || !state.QoSPolicy.Equal(planModel.QoSPolicy) {
		t.Fatalf("private_ip=%v qos_policy=%v, want the configured references %v and %v", state.PrivateIP, state.QoSPolicy, planModel.PrivateIP, planModel.QoSPolicy)
	}
	if state.PrivateIPID.ValueString() != loadBalancerTestPrivateIPID.String() || state.QoSPolicyID.ValueString() != loadBalancerTestQoSID.String() {
		t.Fatalf("private_ip_id=%v qos_policy_id=%v, want the ACTIVE associations", state.PrivateIPID, state.QoSPolicyID)
	}
}

func TestLoadBalancerCreateReportsConfiguredAssociations(t *testing.T) {
	t.Parallel()

	t.Run("omitted association fails create and preserves returned state", func(t *testing.T) {
		t.Parallel()
		lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
		lb.PrivateIpId = nil
		lb.QosPolicy = nil
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerAssociationTransport(t, loadBalancerJSON(t, lb)))
		response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "Load Balancer private_ip was not reported by the API")
		if len(response.Diagnostics) != 2 {
			t.Fatalf("expected two missing association errors: %v", response.Diagnostics)
		}
		assertLoadBalancerDiagnostic(t, response.Diagnostics[1:], "Load Balancer qos_policy was not reported by the API")
		var state LoadBalancerResourceModel
		if diags := response.State.Get(context.Background(), &state); diags.HasError() {
			t.Fatalf("read create state: %v", diags)
		}
		if state.ID.ValueString() != loadBalancerTestID.String() || !state.PrivateIPID.IsNull() ||
			!state.QoSPolicyID.IsNull() || !state.QoSPolicyName.IsNull() {
			t.Fatalf("created object must remain tracked with returned associations: %#v", state)
		}
	})

	t.Run("mismatched association fails the apply", func(t *testing.T) {
		t.Parallel()
		assigned := core.UUID{7}
		lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
		lb.PrivateIpId.Set(assigned)
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerAssociationTransport(t, loadBalancerJSON(t, lb)))
		response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "Load Balancer private_ip does not match the configuration")
		var state LoadBalancerResourceModel
		if diags := response.State.Get(context.Background(), &state); diags.HasError() {
			t.Fatalf("read create state: %v", diags)
		}
		if state.ID.ValueString() != loadBalancerTestID.String() || state.PrivateIPID.ValueString() != assigned.String() ||
			reference.Get(state.PrivateIP, reference.ID).ValueString() != assigned.String() {
			t.Fatalf("mismatched association state=%#v", state)
		}
	})
}

// The create body has no public IP field. A configured mode is a second
// request after the Load Balancer is ACTIVE and the idle wait before the
// request, and the provider waits until the public addresses appear.
func TestLoadBalancerCreateConfiguresPublicIPMode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		mode            string
		v6PublicAddress string
	}{
		{mode: "ipv4-only"},
		{mode: "dual-stack", v6PublicAddress: "2001:db8::10"},
		{mode: " dual-stack ", v6PublicAddress: "2001:db8::10"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var requests []string
				putCalls := 0
				// The request sends the trimmed mode, and state keeps the
				// configured spelling.
				wantBody := fmt.Sprintf(`{"mode":%q}`, strings.TrimSpace(tt.mode))
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests = append(requests, req.Method)
					switch {
					case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
						return loadBalancerRegionResponse(req), nil
					case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
						var body map[string]json.RawMessage
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
							t.Fatalf("decode create body: %v", err)
						}
						if _, ok := body["mode"]; ok {
							t.Errorf("create body must not send a public IP mode: %s", body)
						}
						return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
					case req.Method == http.MethodPut && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/public-ip/":
						putCalls++
						if req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
							t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
						}
						body, err := io.ReadAll(req.Body)
						if err != nil || string(body) != wantBody {
							t.Fatalf("public IP body=%s err=%v, want %s", body, err, wantBody)
						}
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
						if putCalls == 0 || len(requests) == 6 {
							// The ready wait, the idle wait, and the first poll
							// after the request see the ACTIVE Load Balancer
							// without an address.
							return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
						}
						return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", tt.v6PublicAddress)), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				planModel := loadBalancerCreatePlanModel()
				planModel.PublicIPMode = types.StringValue(tt.mode)
				start := time.Now()
				response := executeLoadBalancerCreate(t, resourceUnderTest, planModel)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
				if elapsed := time.Since(start); elapsed != loadBalancerReadyPollInterval {
					t.Fatalf("create waited %s, want one poll interval for the public IP", elapsed)
				}
				if putCalls != 1 || strings.Join(requests, " ") != "GET POST GET GET PUT GET GET" {
					t.Fatalf("requests=%v, want region lookup, create, ready wait, idle wait, public IP request, and two public IP polls", requests)
				}
				var state LoadBalancerResourceModel
				if diags := response.State.Get(context.Background(), &state); diags.HasError() {
					t.Fatalf("read create state: %v", diags)
				}
				if state.PublicIPMode.ValueString() != tt.mode || state.PublicAddress.ValueString() != "203.0.113.10" || state.V6PublicAddress.ValueString() != tt.v6PublicAddress || state.V6PublicAddress.IsNull() != (tt.v6PublicAddress == "") {
					t.Fatalf("public_ip_mode=%v public_address=%v v6_public_address=%v, want %s with its addresses", state.PublicIPMode, state.PublicAddress, state.V6PublicAddress, tt.mode)
				}
			})
		})
	}
}

// The POST claims nothing, so Create takes the claims lock of the new Load
// Balancer only for the public IP step, before its idle wait.
func TestLoadBalancerCreateHoldsTheClaimsLockForThePublicIPStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var requests []string
		putCalls := 0
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.Method)
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
				return loadBalancerRegionResponse(req), nil
			case req.Method == http.MethodPost:
				return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
			case req.Method == http.MethodPut:
				putCalls++
				return loadBalancerResponse(req, http.StatusAccepted, ""), nil
			case req.Method == http.MethodGet && putCalls > 0:
				return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "")), nil
			case req.Method == http.MethodGet:
				return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		unlock, err := resourceUnderTest.claims.Lock(context.Background(), loadBalancerTestID.String())
		if err != nil {
			t.Fatalf("hold the claims lock: %v", err)
		}
		planModel := loadBalancerCreatePlanModel()
		planModel.PublicIPMode = types.StringValue("ipv4-only")
		done := make(chan diag.Diagnostics, 1)
		go func() { done <- executeLoadBalancerCreate(t, resourceUnderTest, planModel).Diagnostics }()
		synctest.Wait()
		if got := strings.Join(requests, " "); got != "GET POST GET" {
			t.Fatalf("requests=%s while another operation held the claims lock, want only region lookup, create, and ready wait", got)
		}
		unlock()
		assertLoadBalancerDiagnostic(t, <-done, "")
		if got := strings.Join(requests, " "); got != "GET POST GET GET PUT GET" {
			t.Fatalf("requests=%s, want the public IP step after the claims lock was released", got)
		}
	})
}

// A failed public IP step keeps the created Load Balancer in state with the
// mode derived from the last read, and fails the apply so Terraform taints it.
func TestLoadBalancerCreatePublicIPFailurePreservesState(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		putStatus  int
		putBody    string
		lastRead   string
		wantDetail string
		wantMode   types.String
		wantWait   time.Duration
	}{
		{
			name:       "refused request",
			putStatus:  http.StatusBadRequest,
			putBody:    `{"errors":[{"location":"","code":"invalid_input","message":"No public IP is available."}]}`,
			wantDetail: "No public IP is available",
			wantMode:   types.StringNull(),
		},
		{
			name:       "wait timeout",
			putStatus:  http.StatusAccepted,
			lastRead:   loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "2001:db8::10"),
			wantDetail: "v6_public_address=\"2001:db8::10\"",
			wantMode:   types.StringValue("dual-stack"),
			wantWait:   loadBalancerReadyTimeout,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				putCalls := 0
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
						return loadBalancerRegionResponse(req), nil
					case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
						return loadBalancerResponse(req, http.StatusCreated, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE)), nil
					case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
						if putCalls == 0 {
							return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
						}
						return loadBalancerResponse(req, http.StatusOK, tt.lastRead), nil
					case req.Method == http.MethodPut && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/public-ip/":
						putCalls++
						return loadBalancerResponse(req, tt.putStatus, tt.putBody), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				planModel := loadBalancerCreatePlanModel()
				planModel.PublicIPMode = types.StringValue("ipv4-only")
				start := time.Now()
				response := executeLoadBalancerCreate(t, resourceUnderTest, planModel)
				if elapsed := time.Since(start); elapsed != tt.wantWait {
					t.Fatalf("create waited %s, want %s", elapsed, tt.wantWait)
				}
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error configuring Load Balancer public IP")
				if detail := response.Diagnostics[0].Detail(); !strings.Contains(detail, tt.wantDetail) || !strings.Contains(detail, "marked tainted") {
					t.Fatalf("diagnostic must report the failure and the tainted Load Balancer, got %q", detail)
				}
				if !response.State.Raw.IsFullyKnown() {
					t.Fatalf("state after a failed public IP step contains unknown values: %s", response.State.Raw)
				}
				var state LoadBalancerResourceModel
				if diags := response.State.Get(context.Background(), &state); diags.HasError() {
					t.Fatalf("read create state: %v", diags)
				}
				if state.ID.ValueString() != loadBalancerTestID.String() || state.ProvisioningStatus.ValueString() != "ACTIVE" || !state.PublicIPMode.Equal(tt.wantMode) {
					t.Fatalf("id=%v provisioning_status=%v public_ip_mode=%v, want the tracked Load Balancer with mode %v", state.ID, state.ProvisioningStatus, state.PublicIPMode, tt.wantMode)
				}
			})
		})
	}
}

// Create sends no public IP request for an omitted mode, so a public address
// that the backend assigns on its own contradicts the configuration.
func TestLoadBalancerCreateReportsUnexpectedPublicIP(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerAssociationTransport(t, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "")))
	response := executeLoadBalancerCreate(t, resourceUnderTest, loadBalancerCreatePlanModel())
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "Load Balancer public_ip_mode does not match the configuration")
	var state LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read create state: %v", diags)
	}
	if state.ID.ValueString() != loadBalancerTestID.String() || state.PublicIPMode.ValueString() != "ipv4-only" {
		t.Fatalf("state must track the reported mode: %#v", state)
	}
}

func TestLoadBalancerReadAndDeleteResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		method          string
		status          int
		body            string
		wantSummary     string
		resourceRemoved bool
	}{
		{name: "read removes not found", method: http.MethodGet, status: http.StatusNotFound, resourceRemoved: true},
		{name: "read removes soft deleted", method: http.MethodGet, status: http.StatusOK, body: loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusDELETED), resourceRemoved: true},
		{name: "read reports backend error", method: http.MethodGet, status: http.StatusInternalServerError, wantSummary: "Error reading Load Balancer"},
		{name: "delete accepts not found", method: http.MethodDelete, status: http.StatusNotFound},
		{name: "delete reports backend error", method: http.MethodDelete, status: http.StatusInternalServerError, wantSummary: "Error deleting Load Balancer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if tt.method == http.MethodDelete && req.Method == http.MethodGet {
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
				}
				if req.Method != tt.method || req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				body := tt.body
				if body == "" {
					body = `{"detail":"backend response"}`
				}
				return loadBalancerResponse(req, tt.status, body), nil
			}))
			state := loadBalancerState(t)
			if tt.method == http.MethodGet {
				response := resource.ReadResponse{State: state}
				resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
				if tt.resourceRemoved && !response.State.Raw.IsNull() {
					t.Fatalf("expected state removal, got %s", response.State.Raw)
				}
				assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
				return
			}
			response := resource.DeleteResponse{State: state}
			resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
		})
	}
}

func TestLoadBalancerReadRemovesAbsentAssociations(t *testing.T) {
	t.Parallel()

	lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	lb.PrivateIpId = nil
	lb.QosPolicy = nil
	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		return loadBalancerResponse(req, http.StatusOK, loadBalancerJSON(t, lb)), nil
	}))

	state := loadBalancerState(t)
	var before LoadBalancerResourceModel
	if diags := state.Get(context.Background(), &before); diags.HasError() {
		t.Fatalf("read initial state: %v", diags)
	}
	before.QoSPolicyName = types.StringValue("gold")
	if diags := state.Set(context.Background(), &before); diags.HasError() {
		t.Fatalf("set initial state: %v", diags)
	}
	response := resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics=%v", response.Diagnostics)
	}
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if !got.PrivateIPID.IsNull() || !got.QoSPolicyID.IsNull() || !got.QoSPolicyName.IsNull() {
		t.Fatalf("absent API associations must remove prior values: %#v", got)
	}
}

// GET has no mode field, so Read, and import through it, derive public_ip_mode
// from the public addresses.
func TestLoadBalancerReadDerivesPublicIPMode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name            string
		publicAddress   string
		v6PublicAddress string
		want            types.String
	}{
		{name: "no public address", want: types.StringNull()},
		{name: "IPv4 only", publicAddress: "203.0.113.10", want: types.StringValue("ipv4-only")},
		{name: "IPv4 and IPv6", publicAddress: "203.0.113.10", v6PublicAddress: "2001:db8::10", want: types.StringValue("dual-stack")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, tt.publicAddress, tt.v6PublicAddress)
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return loadBalancerResponse(req, http.StatusOK, body), nil
			}))
			state := loadBalancerState(t)
			response := resource.ReadResponse{State: state}
			resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
			var got LoadBalancerResourceModel
			if diags := response.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("read state: %v", diags)
			}
			if !got.PublicIPMode.Equal(tt.want) {
				t.Fatalf("public_ip_mode=%v, want %v", got.PublicIPMode, tt.want)
			}
		})
	}
}

// Read and import derive public_ip_mode from the addresses, so a configured
// mode with outer whitespace stays in state while it names the derived mode.
func TestLoadBalancerReadKeepsPaddedPublicIPMode(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "")), nil
	}))
	model := loadBalancerConfiguredModel()
	model.ID = types.StringValue(loadBalancerTestID.String())
	model.PublicIPMode = types.StringValue(" ipv4-only ")
	state := tfsdk.State{Schema: loadBalancerSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set resource state: %v", diags)
	}
	response := resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if got.PublicIPMode.ValueString() != " ipv4-only " {
		t.Fatalf("public_ip_mode=%v, want the configured spelling \" ipv4-only \"", got.PublicIPMode)
	}
}

// A public_ip_mode edit of outer whitespace only names the same mode, so
// Update sends no public IP request and state takes the new spelling.
func TestLoadBalancerUpdateIgnoresPublicIPModeWhitespaceEdit(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("a whitespace-only public_ip_mode edit must send no request, got %s %s", req.Method, req.URL.Path)
		}
		return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "2001:db8::10")), nil
	}))
	stateModel := loadBalancerConfiguredModel()
	stateModel.ID = types.StringValue(loadBalancerTestID.String())
	stateModel.PublicIPMode = types.StringValue("dual-stack")
	planModel := stateModel
	planModel.PublicIPMode = types.StringValue(" dual-stack ")
	response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read update state: %v", diags)
	}
	if got.PublicIPMode.ValueString() != " dual-stack " {
		t.Fatalf("public_ip_mode=%v, want the configured spelling \" dual-stack \"", got.PublicIPMode)
	}
}

func TestLoadBalancerUpdateMapsSynchronousPatch(t *testing.T) {
	t.Parallel()

	calls := 0
	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
			t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
		}
		switch calls {
		case 1:
			if req.Method != http.MethodGet {
				t.Fatalf("expected the idle wait GET before the update, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		case 2:
			if req.Method != http.MethodPatch || req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
				t.Fatalf("unexpected update request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode update body: %v", err)
			}
			if len(body) != 2 || string(body["name"]) != `"updated"` || string(body["description"]) != `""` {
				t.Fatalf("expected only changed fields, got %s", body)
			}
			response := strings.ReplaceAll(loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE), `"terraform-lb"`, `"updated"`)
			response = strings.Replace(response, `"description":"Managed by Terraform"`, `"description":""`, 1)
			return loadBalancerResponse(req, http.StatusOK, response), nil
		default:
			t.Fatalf("unexpected extra request %d", calls)
			return nil, nil
		}
	}))

	stateModel := loadBalancerConfiguredModel()
	stateModel.ID = types.StringValue(loadBalancerTestID.String())
	planModel := stateModel
	planModel.Name = types.StringValue(" updated ")
	planModel.Description = types.StringValue("")
	resourceSchema := loadBalancerSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	planState := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set update state: %v", diags)
	}
	if diags := planState.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan{Schema: resourceSchema, Raw: planState.Raw}}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", response.Diagnostics)
	}
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read update state: %v", diags)
	}
	if got.ID.ValueString() != loadBalancerTestID.String() || got.Name.ValueString() != " updated " || got.Description.ValueString() != "" || calls != 2 {
		t.Fatalf("calls=%d, want the idle wait and the PATCH response mapped without polling; state=%#v", calls, got)
	}
}

// A package or topology change starts an asynchronous resize that the API
// accepts with 202 and the same payload as 200. The provider must wait for
// readiness after it.
func TestLoadBalancerUpdateWaitsForAcceptedResize(t *testing.T) {
	t.Parallel()

	calls := 0
	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body := strings.ReplaceAll(loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE), `"SMALL"`, `"MEDIUM"`)
		switch calls {
		case 1:
			if req.Method != http.MethodGet {
				t.Fatalf("expected the idle wait GET, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		case 2:
			if req.Method != http.MethodPatch {
				t.Fatalf("expected PATCH, got %s %s", req.Method, req.URL.Path)
			}
			return loadBalancerResponse(req, http.StatusAccepted, strings.ReplaceAll(loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE), `"SMALL"`, `"MEDIUM"`)), nil
		case 3:
			if req.Method != http.MethodGet {
				t.Fatalf("expected waiter GET, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusOK, body), nil
		default:
			t.Fatalf("unexpected extra request %d", calls)
			return nil, nil
		}
	}))

	stateModel := loadBalancerConfiguredModel()
	stateModel.ID = types.StringValue(loadBalancerTestID.String())
	planModel := stateModel
	planModel.Package = types.StringValue("MEDIUM")
	response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read update state: %v", diags)
	}
	if got.Package.ValueString() != "MEDIUM" || got.ProvisioningStatus.ValueString() != "ACTIVE" || calls != 3 {
		t.Fatalf("package=%v provisioning_status=%v calls=%d, want MEDIUM ACTIVE 3", got.Package, got.ProvisioningStatus, calls)
	}
}

func TestLoadBalancerUpdateReportsPatchFailures(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		transport   http.RoundTripper
		wantSummary string
	}{
		{
			name: "request error",
			transport: loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
				}
				return loadBalancerResponse(req, http.StatusInternalServerError, `{"detail":"update failed"}`), nil
			}),
			wantSummary: "Error updating Load Balancer",
		},
		{
			name: "mismatched response ID",
			transport: loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
				}
				otherID := core.UUID{9}
				body := strings.Replace(loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE), loadBalancerTestID.String(), otherID.String(), 1)
				return loadBalancerResponse(req, http.StatusOK, body), nil
			}),
			wantSummary: "Error updating Load Balancer",
		},
		{
			name: "ready waiter error",
			transport: func() http.RoundTripper {
				patched := false
				return loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPatch {
						patched = true
						return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE)), nil
					}
					if !patched {
						return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
					}
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusERROR)), nil
				})
			}(),
			wantSummary: "Error waiting for Load Balancer update",
		},
		{
			name: "idle wait reads ERROR",
			transport: loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet {
					t.Errorf("an update must not be sent while the Load Balancer is in ERROR, got %s", req.Method)
				}
				return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusERROR)), nil
			}),
			wantSummary: "Error updating Load Balancer",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceUnderTest := newLoadBalancerTestResource(t, tt.transport)
			stateModel := loadBalancerConfiguredModel()
			stateModel.ID = types.StringValue(loadBalancerTestID.String())
			planModel := stateModel
			planModel.Topology = types.StringValue("ACTIVE_STANDBY")
			response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
		})
	}
}

func TestLoadBalancerUpdateWithoutChangesRefreshesState(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		status      int
		body        string
		wantSummary string
	}{
		{name: "refreshes", status: http.StatusOK, body: loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)},
		{name: "reports refresh error", status: http.StatusInternalServerError, body: `{"detail":"refresh failed"}`, wantSummary: "Error updating Load Balancer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				return loadBalancerResponse(req, tt.status, tt.body), nil
			}))
			stateModel := loadBalancerConfiguredModel()
			stateModel.ID = types.StringValue(loadBalancerTestID.String())
			response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, stateModel)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
			if calls != 1 {
				t.Fatalf("GET calls=%d, want 1", calls)
			}
		})
	}
}

func TestLoadBalancerUpdateChangesOptionalReferencesInState(t *testing.T) {
	t.Parallel()

	omitted := types.ObjectNull(reference.AttributeTypes(reference.ID))
	for _, tt := range []struct {
		name          string
		state         func(*LoadBalancerResourceModel)
		plan          func(*LoadBalancerResourceModel)
		wantPrivateIP types.Object
		wantQoSPolicy types.Object
	}{
		{
			name:          "omission after configuration",
			plan:          func(m *LoadBalancerResourceModel) { m.PrivateIP, m.QoSPolicy = omitted, omitted },
			wantPrivateIP: omitted,
			wantQoSPolicy: omitted,
		},
		{
			name: "configuration of the associated objects after omission",
			state: func(m *LoadBalancerResourceModel) {
				m.PrivateIP, m.QoSPolicy = omitted, omitted
			},
			plan: func(m *LoadBalancerResourceModel) {
				m.PrivateIP = reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestPrivateIPID.String())))
				m.QoSPolicy = reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestQoSID.String())))
			},
			wantPrivateIP: reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestPrivateIPID.String()))),
			wantQoSPolicy: reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestQoSID.String()))),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/" {
					t.Fatalf("a reference change must not call the API, got %s %s", req.Method, req.URL.Path)
				}
				return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
			}))
			stateModel := loadBalancerConfiguredModel()
			stateModel.ID = types.StringValue(loadBalancerTestID.String())
			stateModel.PrivateIPID = types.StringValue(loadBalancerTestPrivateIPID.String())
			stateModel.QoSPolicyID = types.StringValue(loadBalancerTestQoSID.String())
			if tt.state != nil {
				tt.state(&stateModel)
			}
			planModel := stateModel
			tt.plan(&planModel)

			response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
			var got LoadBalancerResourceModel
			if diags := response.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("read update state: %v", diags)
			}
			if !got.PrivateIP.Equal(tt.wantPrivateIP) || !got.QoSPolicy.Equal(tt.wantQoSPolicy) {
				t.Fatalf("private_ip=%v qos_policy=%v, want %v and %v", got.PrivateIP, got.QoSPolicy, tt.wantPrivateIP, tt.wantQoSPolicy)
			}
			if got.PrivateIPID.ValueString() != loadBalancerTestPrivateIPID.String() || got.QoSPolicyID.ValueString() != loadBalancerTestQoSID.String() {
				t.Fatalf("flat IDs must keep the backend associations: private_ip_id=%v qos_policy_id=%v", got.PrivateIPID, got.QoSPolicyID)
			}
		})
	}
}

func TestLoadBalancerUpdateConfiguresPublicIPMode(t *testing.T) {
	t.Parallel()

	const v4, v6 = "203.0.113.10", "2001:db8::10"
	for _, tt := range []struct {
		name                string
		stateMode, planMode types.String
		wantBody            string
		beforeV4, beforeV6  string
		afterV4, afterV6    string
	}{
		{name: "enable ipv4-only", stateMode: types.StringNull(), planMode: types.StringValue("ipv4-only"), wantBody: `{"mode":"ipv4-only"}`, afterV4: v4},
		{name: "ipv4-only to dual-stack", stateMode: types.StringValue("ipv4-only"), planMode: types.StringValue("dual-stack"), wantBody: `{"mode":"dual-stack"}`, beforeV4: v4, afterV4: v4, afterV6: v6},
		{name: "padded ipv4-only to padded dual-stack", stateMode: types.StringValue(" ipv4-only "), planMode: types.StringValue(" dual-stack "), wantBody: `{"mode":"dual-stack"}`, beforeV4: v4, afterV4: v4, afterV6: v6},
		{name: "omission turns ipv4-only off", stateMode: types.StringValue("ipv4-only"), planMode: types.StringNull(), wantBody: `{"mode":null}`, beforeV4: v4},
		{name: "omission turns dual-stack off", stateMode: types.StringValue("dual-stack"), planMode: types.StringNull(), wantBody: `{"mode":null}`, beforeV4: v4, beforeV6: v6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var requests []string
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests = append(requests, req.Method)
					switch {
					case req.Method == http.MethodPut && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/public-ip/":
						body, err := io.ReadAll(req.Body)
						if err != nil || string(body) != tt.wantBody {
							t.Fatalf("public IP body=%s err=%v, want %s", body, err, tt.wantBody)
						}
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
						// The idle wait and the first poll after the request see
						// the ACTIVE Load Balancer before the change.
						if len(requests) == 1 || len(requests) == 3 {
							return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, tt.beforeV4, tt.beforeV6)), nil
						}
						return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, tt.afterV4, tt.afterV6)), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				stateModel.PublicIPMode = tt.stateMode
				planModel := stateModel
				planModel.PublicIPMode = tt.planMode
				start := time.Now()
				response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
				if elapsed := time.Since(start); elapsed != loadBalancerReadyPollInterval || strings.Join(requests, " ") != "GET PUT GET GET" {
					t.Fatalf("requests=%v elapsed=%s, want the idle wait, one public IP request, and polling until it applies", requests, elapsed)
				}
				var got LoadBalancerResourceModel
				if diags := response.State.Get(context.Background(), &got); diags.HasError() {
					t.Fatalf("read update state: %v", diags)
				}
				if !got.PublicIPMode.Equal(tt.planMode) || got.PublicAddress.ValueString() != tt.afterV4 || got.PublicAddress.IsNull() != (tt.afterV4 == "") ||
					got.V6PublicAddress.ValueString() != tt.afterV6 || got.V6PublicAddress.IsNull() != (tt.afterV6 == "") {
					t.Fatalf("public_ip_mode=%v public_address=%v v6_public_address=%v, want %v, %q, and %q", got.PublicIPMode, got.PublicAddress, got.V6PublicAddress, tt.planMode, tt.afterV4, tt.afterV6)
				}
			})
		})
	}
}

// The PATCH and the public IP request both claim the Load Balancer, so the
// resize finishes before the public IP request, and each request follows its
// own idle wait. A public IP failure must keep
// the applied PATCH in state and the old mode, so the next plan retries only
// the mode.
func TestLoadBalancerUpdatePatchesBeforePublicIP(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		putStatus   int
		wantSummary string
		wantMode    types.String
	}{
		{name: "both apply", putStatus: http.StatusAccepted, wantMode: types.StringValue("ipv4-only")},
		{name: "public IP failure keeps the PATCH", putStatus: http.StatusInternalServerError, wantSummary: "Error configuring Load Balancer public IP", wantMode: types.StringNull()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var requests []string
			putCalls := 0
			resized := func(publicAddress string) string {
				lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
				lb.Package = loadbalancersdk.LoadBalancerPackageMEDIUM
				if publicAddress != "" {
					lb.PublicAddress.Set(publicAddress)
				}
				return loadBalancerJSON(t, lb)
			}
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method)
				switch req.Method {
				case http.MethodPatch:
					return loadBalancerResponse(req, http.StatusAccepted, strings.ReplaceAll(loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE), `"SMALL"`, `"MEDIUM"`)), nil
				case http.MethodPut:
					putCalls++
					return loadBalancerResponse(req, tt.putStatus, `{"detail":"public IP failed"}`), nil
				case http.MethodGet:
					if putCalls == 0 {
						return loadBalancerResponse(req, http.StatusOK, resized("")), nil
					}
					return loadBalancerResponse(req, http.StatusOK, resized("203.0.113.10")), nil
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}
			}))
			stateModel := loadBalancerConfiguredModel()
			stateModel.ID = types.StringValue(loadBalancerTestID.String())
			planModel := stateModel
			planModel.Package = types.StringValue("MEDIUM")
			planModel.PublicIPMode = types.StringValue("ipv4-only")
			response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
			assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
			wantRequests := "GET PATCH GET GET PUT GET"
			if tt.wantSummary != "" {
				wantRequests = "GET PATCH GET GET PUT"
			}
			if strings.Join(requests, " ") != wantRequests {
				t.Fatalf("requests=%v, want %s", requests, wantRequests)
			}
			var got LoadBalancerResourceModel
			if diags := response.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("read update state: %v", diags)
			}
			if got.Package.ValueString() != "MEDIUM" || !got.PublicIPMode.Equal(tt.wantMode) {
				t.Fatalf("package=%v public_ip_mode=%v, want the applied package and mode %v", got.Package, got.PublicIPMode, tt.wantMode)
			}
		})
	}
}

// Update sends one public IP request after the idle wait, and returns its
// refusal without another idle wait, request, or public IP wait. Only a busy
// refusal names the public IP change in the diagnostic. Update releases the
// claims lock of the Load Balancer afterwards.
func TestLoadBalancerUpdateReturnsARefusalOfTheOnePublicIPRequest(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		put        loadBalancerAnswer
		wantDetail []string
		wantBusy   bool
	}{
		{
			name:       "busy refusal",
			put:        loadBalancerAnswer{http.StatusConflict, loadBalancerBusyBody},
			wantDetail: []string{"the public IP change of load balancer " + loadBalancerTestID.String(), "refused as busy", "load balancer is busy"},
			wantBusy:   true,
		},
		{
			name:       "conflict of a failed operation",
			put:        loadBalancerAnswer{http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`},
			wantDetail: []string{"The last operation on load balancer failed."},
		},
		{
			name:       "busy message without the busy code",
			put:        loadBalancerAnswer{http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"load balancer is busy, please try again later."}]}`},
			wantDetail: []string{"load balancer is busy"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var resourceUnderTest *LoadBalancerResource
				var sentAt []time.Duration
				var lockTaken atomic.Bool
				gets := 0
				start := time.Now()
				resourceUnderTest = newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch req.Method {
					case http.MethodGet:
						if len(sentAt) > 0 {
							t.Fatal("Update read the Load Balancer again after the refused public IP request")
						}
						gets++
						// The first two GETs show the claim of another
						// client as PENDING_UPDATE.
						if gets <= 2 {
							return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE).response(req), nil
						}
						return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
					case http.MethodPut:
						sentAt = append(sentAt, time.Since(start))
						go func() {
							unlock, err := resourceUnderTest.claims.Lock(context.Background(), loadBalancerTestID.String())
							if err == nil {
								lockTaken.Store(true)
								unlock()
							}
						}()
						return tt.put.response(req), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				planModel := stateModel
				planModel.PublicIPMode = types.StringValue("ipv4-only")
				response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error configuring Load Balancer public IP")
				if want := []time.Duration{2 * loadBalancerReadyPollInterval}; !slices.Equal(sentAt, want) {
					t.Fatalf("public IP request sent at %v, want once at %v", sentAt, want)
				}
				detail := response.Diagnostics[0].Detail()
				for _, want := range tt.wantDetail {
					if !strings.Contains(detail, want) {
						t.Fatalf("diagnostic detail %q does not contain %q", detail, want)
					}
				}
				if got := strings.Contains(detail, "refused as busy"); got != tt.wantBusy {
					t.Fatalf("diagnostic detail %q names a busy refusal=%t, want %t", detail, got, tt.wantBusy)
				}
				var got LoadBalancerResourceModel
				if diags := response.State.Get(context.Background(), &got); diags.HasError() {
					t.Fatalf("read update state: %v", diags)
				}
				if !got.PublicIPMode.IsNull() || got.ProvisioningStatus.ValueString() != "ACTIVE" {
					t.Fatalf("public_ip_mode=%v provisioning_status=%v, want the old mode from the idle wait", got.PublicIPMode, got.ProvisioningStatus)
				}
				synctest.Wait()
				if !lockTaken.Load() {
					t.Fatal("Update did not release the claims lock")
				}
			})
		})
	}
}

// Before the public IP request, the idle wait reads the Load Balancer. A
// pending state waits, and ERROR or UNKNOWN fail without a request. A Load
// Balancer that already reports the planned mode needs no request, and a
// dual-stack Load Balancer cannot take ipv4-only. State keeps that last read.
func TestLoadBalancerUpdateChecksBeforePublicIPRequest(t *testing.T) {
	t.Parallel()

	const v4, v6 = "203.0.113.10", "2001:db8::10"
	for _, tt := range []struct {
		name                string
		stateMode, planMode types.String
		reads               []string
		wantRequests        string
		wantWait            time.Duration
		wantSummary         string
		wantDetail          string
		wantMode            types.String
	}{
		{
			name:         "pending waits for ACTIVE",
			stateMode:    types.StringNull(),
			planMode:     types.StringValue("ipv4-only"),
			reads:        []string{"PENDING_UPDATE", "ACTIVE", "ACTIVE " + v4},
			wantRequests: "GET GET PUT GET",
			wantWait:     loadBalancerReadyPollInterval,
			wantMode:     types.StringValue("ipv4-only"),
		},
		{
			name:         "ERROR fails without a request",
			stateMode:    types.StringNull(),
			planMode:     types.StringValue("ipv4-only"),
			reads:        []string{"ERROR"},
			wantRequests: "GET",
			wantSummary:  "Error configuring Load Balancer public IP",
			wantDetail:   "terminal provisioning status ERROR",
			wantMode:     types.StringNull(),
		},
		{
			name:         "UNKNOWN fails without a request",
			stateMode:    types.StringValue("ipv4-only"),
			planMode:     types.StringNull(),
			reads:        []string{"UNKNOWN " + v4},
			wantRequests: "GET",
			wantSummary:  "Error configuring Load Balancer public IP",
			wantDetail:   "terminal provisioning status UNKNOWN",
			wantMode:     types.StringValue("ipv4-only"),
		},
		{
			name:         "public IP already off",
			stateMode:    types.StringValue("ipv4-only"),
			planMode:     types.StringNull(),
			reads:        []string{"ACTIVE"},
			wantRequests: "GET",
			wantMode:     types.StringNull(),
		},
		{
			name:         "ipv4-only already on",
			stateMode:    types.StringNull(),
			planMode:     types.StringValue("ipv4-only"),
			reads:        []string{"ACTIVE " + v4},
			wantRequests: "GET",
			wantMode:     types.StringValue("ipv4-only"),
		},
		{
			name:         "dual-stack already on",
			stateMode:    types.StringValue("ipv4-only"),
			planMode:     types.StringValue("dual-stack"),
			reads:        []string{"ACTIVE " + v4 + " " + v6},
			wantRequests: "GET",
			wantMode:     types.StringValue("dual-stack"),
		},
		{
			name:         "turning off waits for a left IPv6 address",
			stateMode:    types.StringValue("dual-stack"),
			planMode:     types.StringNull(),
			reads:        []string{"ACTIVE - " + v6, "ACTIVE"},
			wantRequests: "GET PUT GET",
			wantMode:     types.StringNull(),
		},
		{
			name:         "ipv4-only on a dual-stack Load Balancer",
			stateMode:    types.StringNull(),
			planMode:     types.StringValue("ipv4-only"),
			reads:        []string{"ACTIVE " + v4 + " " + v6},
			wantRequests: "GET",
			wantSummary:  "Error configuring Load Balancer public IP",
			wantDetail:   `cannot use public_ip_mode "ipv4-only"`,
			wantMode:     types.StringValue("dual-stack"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var requests []string
				gets := 0
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests = append(requests, req.Method)
					switch req.Method {
					case http.MethodGet:
						// Each read is "<status> [<public_address> [<v6_public_address>]]",
						// and "-" stands for an absent address.
						fields := append(strings.Fields(tt.reads[min(gets, len(tt.reads)-1)]), "-", "-")
						gets++
						address := func(field string) string {
							if field == "-" {
								return ""
							}
							return field
						}
						return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatus(fields[0]), address(fields[1]), address(fields[2]))), nil
					case http.MethodPut:
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				stateModel.PublicIPMode = tt.stateMode
				planModel := stateModel
				planModel.PublicIPMode = tt.planMode
				start := time.Now()
				response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, tt.wantSummary)
				if tt.wantDetail != "" && !strings.Contains(response.Diagnostics[0].Detail(), tt.wantDetail) {
					t.Fatalf("diagnostic detail %q does not contain %q", response.Diagnostics[0].Detail(), tt.wantDetail)
				}
				if got := strings.Join(requests, " "); got != tt.wantRequests {
					t.Fatalf("requests=%s, want %s", got, tt.wantRequests)
				}
				if elapsed := time.Since(start); elapsed != tt.wantWait {
					t.Fatalf("update waited %s, want %s", elapsed, tt.wantWait)
				}
				var got LoadBalancerResourceModel
				if diags := response.State.Get(context.Background(), &got); diags.HasError() {
					t.Fatalf("read update state: %v", diags)
				}
				if !got.PublicIPMode.Equal(tt.wantMode) {
					t.Fatalf("public_ip_mode=%v, want %v", got.PublicIPMode, tt.wantMode)
				}
			})
		})
	}
}

// ModifyPlan skips a plan whose region or public_ip_mode is unknown, so
// Update rejects dual-stack to ipv4-only before any request, the PATCH
// included.
func TestLoadBalancerUpdateRejectsDualStackToIPv4OnlyBeforeRequests(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))
	stateModel := loadBalancerConfiguredModel()
	stateModel.ID = types.StringValue(loadBalancerTestID.String())
	stateModel.PublicIPMode = types.StringValue("dual-stack")
	planModel := stateModel
	planModel.Package = types.StringValue("MEDIUM")
	planModel.PublicIPMode = types.StringValue("ipv4-only")
	response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "Unsupported public_ip_mode change")
	withPath, ok := response.Diagnostics[0].(diag.DiagnosticWithPath)
	if !ok || !withPath.Path().Equal(path.Root("public_ip_mode")) {
		t.Fatalf("diagnostic=%v, want it at public_ip_mode", response.Diagnostics[0])
	}
}

// Update holds the claims lock of the Load Balancer from the idle wait to the
// end of the public IP wait, so another operation on it waits that long.
func TestLoadBalancerUpdateHoldsTheClaimsLockThroughThePublicIPWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var resourceUnderTest *LoadBalancerResource
		lockedAt := make(chan time.Duration, 1)
		gets := 0
		start := time.Now()
		resourceUnderTest = newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodGet:
				gets++
				// The idle wait and the first poll after the request see the
				// ACTIVE Load Balancer before the change.
				if gets <= 2 {
					return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
				}
				return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "")), nil
			case http.MethodPut:
				go func() {
					unlock, err := resourceUnderTest.claims.Lock(context.Background(), loadBalancerTestID.String())
					if err != nil {
						t.Errorf("take the claims lock: %v", err)
						return
					}
					lockedAt <- time.Since(start)
					unlock()
				}()
				return loadBalancerResponse(req, http.StatusAccepted, ""), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		stateModel := loadBalancerConfiguredModel()
		stateModel.ID = types.StringValue(loadBalancerTestID.String())
		planModel := stateModel
		planModel.PublicIPMode = types.StringValue("ipv4-only")
		response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
		if got := <-lockedAt; got != loadBalancerReadyPollInterval {
			t.Fatalf("another operation took the claims lock after %s, want after the public IP wait of %s", got, loadBalancerReadyPollInterval)
		}
	})
}

// Update compares the result with the configured mode even when the mode did
// not change, so a dual-stack Load Balancer that a skipped refresh missed
// cannot pass as ipv4-only.
func TestLoadBalancerUpdateReportsUnexpectedPublicIPMode(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
		}
		if req.Method != http.MethodPatch {
			t.Fatalf("an unchanged public_ip_mode must not call the public IP endpoint, got %s %s", req.Method, req.URL.Path)
		}
		lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
		lb.Name = "updated"
		lb.PublicAddress.Set("203.0.113.10")
		lb.V6PublicAddress.Set("2001:db8::10")
		return loadBalancerResponse(req, http.StatusOK, loadBalancerJSON(t, lb)), nil
	}))
	stateModel := loadBalancerConfiguredModel()
	stateModel.ID = types.StringValue(loadBalancerTestID.String())
	stateModel.PublicIPMode = types.StringValue("ipv4-only")
	planModel := stateModel
	planModel.Name = types.StringValue("updated")
	response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
	assertLoadBalancerDiagnostic(t, response.Diagnostics, "Load Balancer public_ip_mode does not match the configuration")
	var got LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read update state: %v", diags)
	}
	if got.Name.ValueString() != "updated" || got.PublicIPMode.ValueString() != "dual-stack" {
		t.Fatalf("name=%v public_ip_mode=%v, want the applied name and the reported dual-stack", got.Name, got.PublicIPMode)
	}
}

func TestLoadBalancerDeleteCallsSDKAndWaits(t *testing.T) {
	t.Parallel()

	calls := 0
	resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Project-ID") != loadBalancerTestProjectID.String() {
			t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
		}
		switch calls {
		case 1:
			if req.Method != http.MethodGet {
				t.Fatalf("expected the idle wait GET, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		case 2:
			if req.Method != http.MethodDelete {
				t.Fatalf("expected DELETE, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusAccepted, ""), nil
		case 3:
			if req.Method != http.MethodGet {
				t.Fatalf("expected delete waiter GET, got %s", req.Method)
			}
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusDELETED)), nil
		default:
			t.Fatalf("unexpected extra request %d", calls)
			return nil, nil
		}
	}))
	state := loadBalancerState(t)
	response := resource.DeleteResponse{State: state}
	resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() || calls != 3 {
		t.Fatalf("delete calls=%d diagnostics=%v", calls, response.Diagnostics)
	}
}

// A failed Delete of a Load Balancer reports the failure and keeps the state.
// A failed deletion returns the Load Balancer to its state before the DELETE,
// so ACTIVE, ERROR, or UNKNOWN after PENDING_DELETE shows at once. A Load
// Balancer in ERROR or UNKNOWN never shows PENDING_DELETE, so Delete waits
// until timeout.
func TestLoadBalancerDeleteFailureKeepsState(t *testing.T) {
	t.Parallel()

	const (
		active        = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
		errored       = loadbalancersdk.LoadBalancingProvisioningStatusERROR
		unknown       = loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN
		pendingDelete = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE
	)
	for _, tt := range []struct {
		name        string
		before      loadbalancersdk.LoadBalancingProvisioningStatus
		afterDelete []loadbalancersdk.LoadBalancingProvisioningStatus
		wantErr     string
		wantWait    time.Duration
		wantTimeout bool
	}{
		{
			name:        "ACTIVE returns after PENDING_DELETE",
			before:      active,
			afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingDelete, active},
			wantErr:     "deletion failed: provisioning status ACTIVE after pending delete",
			wantWait:    loadBalancerReadyPollInterval,
		},
		{
			name:        "ERROR after PENDING_DELETE",
			before:      active,
			afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingDelete, errored},
			wantErr:     "terminal provisioning status ERROR after pending delete",
			wantWait:    loadBalancerReadyPollInterval,
		},
		{
			name:        "UNKNOWN after PENDING_DELETE",
			before:      active,
			afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingDelete, unknown},
			wantErr:     "terminal provisioning status UNKNOWN after pending delete",
			wantWait:    loadBalancerReadyPollInterval,
		},
		{
			name:        "ERROR for the whole deletion",
			before:      errored,
			afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{errored},
			wantErr:     "provisioning_status=ERROR",
			wantWait:    loadBalancerReadyTimeout,
			wantTimeout: true,
		},
		{
			name:        "UNKNOWN for the whole deletion",
			before:      unknown,
			afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{unknown},
			wantErr:     "provisioning_status=UNKNOWN",
			wantWait:    loadBalancerReadyTimeout,
			wantTimeout: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				deleted := false
				polls := 0
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodDelete:
						deleted = true
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					case req.Method == http.MethodGet && !deleted:
						return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(tt.before)), nil
					case req.Method == http.MethodGet:
						current := tt.afterDelete[min(polls, len(tt.afterDelete)-1)]
						polls++
						return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(current)), nil
					}
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
					return nil, nil
				}))

				state := loadBalancerState(t)
				start := time.Now()
				response := resource.DeleteResponse{State: state}
				resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
				if !deleted {
					t.Fatal("expected the DELETE to be sent")
				}
				errs := response.Diagnostics.Errors()
				if len(errs) != 1 || errs[0].Summary() != "Error waiting for Load Balancer deletion" || !strings.Contains(errs[0].Detail(), tt.wantErr) {
					t.Fatalf("expected a deletion wait error containing %q, got %v", tt.wantErr, response.Diagnostics)
				}
				if timedOut := strings.Contains(errs[0].Detail(), wait.ErrTimeout.Error()); timedOut != tt.wantTimeout {
					t.Fatalf("timeout = %t, want %t: %s", timedOut, tt.wantTimeout, errs[0].Detail())
				}
				if waited := time.Since(start); waited != tt.wantWait {
					t.Fatalf("waited %s, want %s", waited, tt.wantWait)
				}
				if !response.State.Raw.Equal(state.Raw) {
					t.Fatal("a failed delete must keep the state")
				}
			})
		})
	}
}

func TestLoadBalancerUpdateWaitsForIdleBeforePatch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		gets := 0
		start := time.Now()
		var patchedAfter time.Duration
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodGet:
				gets++
				if gets <= 2 {
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE)), nil
				}
				return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
			case http.MethodPatch:
				patchedAfter = time.Since(start)
				return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}
		}))
		stateModel := loadBalancerConfiguredModel()
		stateModel.ID = types.StringValue(loadBalancerTestID.String())
		planModel := stateModel
		planModel.Name = types.StringValue("updated")
		response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
		assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
		if want := 2 * loadBalancerReadyPollInterval; patchedAfter != want {
			t.Fatalf("PATCH sent after %s, want after the idle wait of %s", patchedAfter, want)
		}
	})
}

// waitUntilDeletable counts ERROR and UNKNOWN as idle and waits while the
// Load Balancer is pending.
func TestLoadBalancerDeleteWaitsForIdle(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		statuses   []int
		bodies     []string
		wantDelete bool
		wantWait   time.Duration
	}{
		{
			name:       "pending then ERROR",
			statuses:   []int{http.StatusOK, http.StatusOK},
			bodies:     []string{loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE), loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusERROR)},
			wantDelete: true,
			wantWait:   loadBalancerReadyPollInterval,
		},
		{
			name:       "UNKNOWN",
			statuses:   []int{http.StatusOK},
			bodies:     []string{loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN)},
			wantDelete: true,
		},
		{
			name:     "already gone",
			statuses: []int{http.StatusNotFound},
			bodies:   []string{`{"detail":"not found"}`},
		},
		{
			name:     "deleted by another client while pending",
			statuses: []int{http.StatusOK, http.StatusNotFound},
			bodies:   []string{loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE), `{"detail":"not found"}`},
			wantWait: loadBalancerReadyPollInterval,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				gets := 0
				deleted := false
				start := time.Now()
				var deletedAfter time.Duration
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch req.Method {
					case http.MethodGet:
						if deleted {
							return loadBalancerResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
						}
						i := min(gets, len(tt.bodies)-1)
						gets++
						return loadBalancerResponse(req, tt.statuses[i], tt.bodies[i]), nil
					case http.MethodDelete:
						deleted = true
						deletedAfter = time.Since(start)
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				state := loadBalancerState(t)
				response := resource.DeleteResponse{State: state}
				resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "")
				if deleted != tt.wantDelete {
					t.Fatalf("DELETE sent=%t, want %t", deleted, tt.wantDelete)
				}
				if tt.wantDelete && deletedAfter != tt.wantWait {
					t.Fatalf("DELETE sent after %s, want after the idle wait of %s", deletedAfter, tt.wantWait)
				}
			})
		})
	}
}

// Every operation of the Load Balancer family claims the root Load Balancer,
// so operations on one Load Balancer in one provider process run one at a
// time.
func TestLoadBalancerOperationsHoldTheClaimsLock(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		run  func(t *testing.T, r *LoadBalancerResource) diag.Diagnostics
	}{
		{
			name: "update",
			run: func(t *testing.T, r *LoadBalancerResource) diag.Diagnostics {
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				planModel := stateModel
				planModel.Name = types.StringValue("updated")
				return executeLoadBalancerUpdate(t, r, stateModel, planModel).Diagnostics
			},
		},
		{
			name: "public IP change",
			run: func(t *testing.T, r *LoadBalancerResource) diag.Diagnostics {
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				planModel := stateModel
				planModel.PublicIPMode = types.StringValue("ipv4-only")
				return executeLoadBalancerUpdate(t, r, stateModel, planModel).Diagnostics
			},
		},
		{
			name: "delete",
			run: func(t *testing.T, r *LoadBalancerResource) diag.Diagnostics {
				state := loadBalancerState(t)
				response := resource.DeleteResponse{State: state}
				r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
				return response.Diagnostics
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				requests := 0
				deleted, publicIPRequested := false, false
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					requests++
					switch {
					case req.Method == http.MethodDelete:
						deleted = true
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					case req.Method == http.MethodGet && deleted:
						return loadBalancerResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
					case req.Method == http.MethodPut:
						publicIPRequested = true
						return loadBalancerResponse(req, http.StatusAccepted, ""), nil
					case req.Method == http.MethodGet && publicIPRequested:
						return loadBalancerResponse(req, http.StatusOK, loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "")), nil
					default:
						return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
					}
				}))
				unlock, err := resourceUnderTest.claims.Lock(context.Background(), loadBalancerTestID.String())
				if err != nil {
					t.Fatalf("hold the claims lock: %v", err)
				}
				done := make(chan diag.Diagnostics, 1)
				go func() { done <- tt.run(t, resourceUnderTest) }()
				synctest.Wait()
				if requests != 0 {
					t.Fatalf("%s sent %d requests while another operation held the claims lock", tt.name, requests)
				}
				unlock()
				assertLoadBalancerDiagnostic(t, <-done, "")
				if requests == 0 {
					t.Fatalf("%s sent no request after the claims lock was released", tt.name)
				}
			})
		})
	}
}

func TestLoadBalancerImportState(t *testing.T) {
	t.Parallel()

	resourceSchema := loadBalancerSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: resourceSchema}}
	model := loadBalancerConfiguredModel()
	if diags := response.State.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	(&LoadBalancerResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: loadBalancerTestID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", response.Diagnostics)
	}
	var state LoadBalancerResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read import state: %v", diags)
	}
	if state.ID.ValueString() != loadBalancerTestID.String() {
		t.Fatalf("imported ID=%s, want %s", state.ID.ValueString(), loadBalancerTestID)
	}
}

func TestLoadBalancerWaitUntilReady(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			status := loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
			if calls == 2 {
				status = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
			}
			return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(status)), nil
		}))
		start := time.Now()
		lb, err := resourceUnderTest.waitUntilReady(context.Background(), loadBalancerTestID, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
		if err != nil || calls != 2 || lb == nil {
			t.Fatalf("waiter result calls=%d lb=%#v error=%v", calls, lb, err)
		}
		if elapsed := time.Since(start); elapsed != loadBalancerReadyPollInterval {
			t.Fatalf("expected one poll interval, got %s", elapsed)
		}
	})
}

func TestLoadBalancerWaitUntilReadyErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		status     loadbalancersdk.LoadBalancingProvisioningStatus
		wantErr    error
		wantDetail string
		wantWait   time.Duration
	}{
		{name: "terminal error", status: loadbalancersdk.LoadBalancingProvisioningStatusERROR, wantDetail: "terminal provisioning status ERROR"},
		{name: "deleted is terminal", status: loadbalancersdk.LoadBalancingProvisioningStatusDELETED, wantDetail: "terminal provisioning status DELETED"},
		{name: "unknown is terminal", status: loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN, wantDetail: "terminal provisioning status UNKNOWN"},
		{name: "pending times out", status: loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE, wantErr: wait.ErrTimeout, wantDetail: "PENDING_UPDATE", wantWait: loadBalancerReadyTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(tt.status)), nil
				}))
				start := time.Now()
				_, err := resourceUnderTest.waitUntilReady(context.Background(), loadBalancerTestID, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
				if err == nil || !strings.Contains(err.Error(), tt.wantDetail) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Fatalf("unexpected waiter error: %v", err)
				}
				if elapsed := time.Since(start); elapsed != tt.wantWait {
					t.Fatalf("waited %s, want %s", elapsed, tt.wantWait)
				}
			})
		})
	}
}

// The waiter accepts only an ACTIVE Load Balancer whose derived mode equals
// the planned mode. It keeps waiting on the ACTIVE state before the change.
// It also waits while a dual-stack Load Balancer moves to ipv4-only, or an
// IPv4-only one moves to dual-stack. An IPv6 address left after turning
// public IP off keeps it waiting too.
func TestLoadBalancerWaitUntilPublicIPMode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		mode      types.String
		responses []string
		wantWait  time.Duration
	}{
		{
			name: "enable waits for the IPv4 address",
			mode: types.StringValue("ipv4-only"),
			responses: []string{
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "", ""),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE, "203.0.113.10", ""),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", ""),
			},
			wantWait: 2 * loadBalancerReadyPollInterval,
		},
		{
			name: "enable from dual-stack waits for the IPv6 address to go",
			mode: types.StringValue("ipv4-only"),
			responses: []string{
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "2001:db8::10"),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", ""),
			},
			wantWait: loadBalancerReadyPollInterval,
		},
		{
			name: "dual-stack waits for the IPv6 address",
			mode: types.StringValue("dual-stack"),
			responses: []string{
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", ""),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "2001:db8::10"),
			},
			wantWait: loadBalancerReadyPollInterval,
		},
		{
			name: "disable waits for the IPv4 address to go",
			mode: types.StringNull(),
			responses: []string{
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", ""),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "", ""),
			},
			wantWait: loadBalancerReadyPollInterval,
		},
		{
			name: "disable from dual-stack waits for both addresses to go",
			mode: types.StringNull(),
			responses: []string{
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", "2001:db8::10"),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "", "2001:db8::10"),
				loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "", ""),
			},
			wantWait: 2 * loadBalancerReadyPollInterval,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					body := tt.responses[min(calls, len(tt.responses)-1)]
					calls++
					return loadBalancerResponse(req, http.StatusOK, body), nil
				}))
				start := time.Now()
				lb, err := resourceUnderTest.waitUntilPublicIPMode(context.Background(), loadBalancerTestID, tt.mode, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
				if err != nil || lb == nil || !loadBalancerPublicIPMode(lb).Equal(tt.mode) {
					t.Fatalf("waiter result lb=%#v err=%v", lb, err)
				}
				if elapsed := time.Since(start); elapsed != tt.wantWait || calls != len(tt.responses) {
					t.Fatalf("waited %s over %d reads, want %s over %d", elapsed, calls, tt.wantWait, len(tt.responses))
				}
			})
		})
	}
}

func TestLoadBalancerWaitUntilPublicIPModeErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		mode       string
		response   string
		wantStatus loadbalancersdk.LoadBalancingProvisioningStatus
		wantErr    error
		wantDetail string
		wantWait   time.Duration
	}{
		{
			name:       "terminal error",
			mode:       "ipv4-only",
			response:   loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusERROR),
			wantStatus: loadbalancersdk.LoadBalancingProvisioningStatusERROR,
			wantDetail: "terminal provisioning status ERROR, public_address=null",
		},
		{
			name:       "missing IPv4 address times out",
			mode:       "ipv4-only",
			response:   loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
			wantStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			wantErr:    wait.ErrTimeout,
			wantDetail: "provisioning_status=ACTIVE, public_address=null, v6_public_address=null",
			wantWait:   loadBalancerReadyTimeout,
		},
		{
			name:       "missing IPv6 address for dual-stack times out",
			mode:       "dual-stack",
			response:   loadBalancerPublicIPJSON(t, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, "203.0.113.10", ""),
			wantStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
			wantErr:    wait.ErrTimeout,
			wantDetail: "provisioning_status=ACTIVE, public_address=\"203.0.113.10\", v6_public_address=null",
			wantWait:   loadBalancerReadyTimeout,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					return loadBalancerResponse(req, http.StatusOK, tt.response), nil
				}))
				start := time.Now()
				observed, err := resourceUnderTest.waitUntilPublicIPMode(context.Background(), loadBalancerTestID, types.StringValue(tt.mode), loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
				if err == nil || !strings.Contains(err.Error(), tt.wantDetail) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Fatalf("unexpected waiter error: %v", err)
				}
				if observed == nil || observed.ProvisioningStatus != tt.wantStatus {
					t.Fatalf("the waiter must return the last Load Balancer it read, got %#v", observed)
				}
				if elapsed := time.Since(start); elapsed != tt.wantWait {
					t.Fatalf("waited %s, want %s", elapsed, tt.wantWait)
				}
			})
		})
	}
}

func TestLoadBalancerWaitUntilDeleted(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		statuses  []loadbalancersdk.LoadBalancingProvisioningStatus
		finalCode int
		wantErr   error
		wantErrIs string
		wantWait  time.Duration
	}{
		{
			name:     "pending then deleted",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusDELETED},
			wantWait: loadBalancerReadyPollInterval,
		},
		{name: "not found is deleted", finalCode: http.StatusNotFound},
		{
			name:     "error recovers into deletion",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusERROR, loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusDELETED},
			wantWait: 2 * loadBalancerReadyPollInterval,
		},
		{
			name: "consecutive errors before pending delete keep polling into deletion",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
				loadbalancersdk.LoadBalancingProvisioningStatusDELETED,
			},
			wantWait: 4 * loadBalancerReadyPollInterval,
		},
		{
			name:      "error without pending delete keeps polling to the deadline",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusERROR},
			wantErr:   wait.ErrTimeout,
			wantErrIs: "provisioning_status=ERROR",
			wantWait:  loadBalancerReadyTimeout,
		},
		{
			name:      "unknown without pending delete keeps polling to the deadline",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN},
			wantErr:   wait.ErrTimeout,
			wantErrIs: "provisioning_status=UNKNOWN",
			wantWait:  loadBalancerReadyTimeout,
		},
		{
			name:      "error after pending delete fails immediately",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusERROR},
			wantErrIs: "terminal provisioning status ERROR after pending delete",
			wantWait:  loadBalancerReadyPollInterval,
		},
		{
			name:     "unknown before pending delete keeps polling into deletion",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN, loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusDELETED},
			wantWait: 2 * loadBalancerReadyPollInterval,
		},
		{
			name:      "unknown after pending delete fails immediately",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN},
			wantErrIs: "terminal provisioning status UNKNOWN after pending delete",
			wantWait:  loadBalancerReadyPollInterval,
		},
		{
			name: "error for the whole deletion keeps polling until removal",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusDELETED,
			},
			wantWait: 3 * loadBalancerReadyPollInterval,
		},
		{
			name:      "active after pending delete fails immediately",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE},
			wantErrIs: "deletion failed: provisioning status ACTIVE after pending delete",
			wantWait:  loadBalancerReadyPollInterval,
		},
		{
			name:     "active before pending delete keeps polling into deletion",
			statuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusACTIVE, loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, loadbalancersdk.LoadBalancingProvisioningStatusDELETED},
			wantWait: 2 * loadBalancerReadyPollInterval,
		},
		{
			name:      "single error followed by recovery keeps polling to the deadline",
			statuses:  []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusERROR, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE},
			wantErr:   wait.ErrTimeout,
			wantErrIs: "provisioning_status=ACTIVE",
			wantWait:  loadBalancerReadyTimeout,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if tt.finalCode != 0 {
						return loadBalancerResponse(req, tt.finalCode, `{"detail":"not found"}`), nil
					}
					status := tt.statuses[min(calls, len(tt.statuses)-1)]
					calls++
					return loadBalancerResponse(req, http.StatusOK, loadBalancerResponseJSON(status)), nil
				}))
				start := time.Now()
				err := resourceUnderTest.waitUntilDeleted(context.Background(), loadBalancerTestID, loadBalancerReadyTimeout, loadBalancerReadyPollInterval)
				if tt.wantErr == nil && tt.wantErrIs == "" && err != nil {
					t.Fatalf("unexpected delete waiter error: %v", err)
				}
				if tt.wantErr != nil && (err == nil || !errors.Is(err, tt.wantErr)) {
					t.Fatalf("delete waiter error=%v, want %v", err, tt.wantErr)
				}
				if tt.wantErrIs != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrIs)) {
					t.Fatalf("delete waiter error=%v, want containing %q", err, tt.wantErrIs)
				}
				if elapsed := time.Since(start); elapsed != tt.wantWait {
					t.Fatalf("waited %s, want %s", elapsed, tt.wantWait)
				}
			})
		})
	}
}

func TestLoadBalancerBusyRefusal(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		status   int
		body     string
		wantBusy bool
	}{
		{"busy", http.StatusConflict, loadBalancerBusyBody, true},
		{"busy code after another detail", http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"Listener is busy."},{"location":"","code":"resource_busy","message":"load balancer is busy, please try again later."}]}`, true},
		{"failed operation", http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`, false},
		{"busy message without the busy code", http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"load balancer is busy, please try again later."}]}`, false},
		{"immutable while pending", http.StatusBadRequest, `{"errors":[{"location":"","code":"invalid_input","message":"Load Balancer is immutable, please try again later."}]}`, false},
		{"busy code on a validation status", http.StatusUnprocessableEntity, loadBalancerBusyBody, false},
		{"busy code on a backend failure", http.StatusInternalServerError, loadBalancerBusyBody, false},
		{"backend failure", http.StatusInternalServerError, `{"detail":"delete failed"}`, false},
		{"not found", http.StatusNotFound, `{"detail":"gone"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				return loadBalancerResponse(req, tt.status, tt.body), nil
			}))
			err := resourceUnderTest.client.DeleteLoadBalancer(context.Background(), loadBalancerTestID, loadbalancersdk.DeleteLoadBalancerParams{ProjectID: loadBalancerTestProjectID})
			if err == nil {
				t.Fatal("the SDK returned no error")
			}
			if got := loadBalancerBusyRefusal(err); got != tt.wantBusy {
				t.Fatalf("loadBalancerBusyRefusal(%v)=%t, want %t", err, got, tt.wantBusy)
			}
		})
	}
}

func TestRequestLoadBalancerOperationReturnsABusyRefusal(t *testing.T) {
	t.Parallel()

	refusal := busyRefusalError(http.StatusConflict, resourceBusyErrorCode)
	calls := 0
	_, err := requestLoadBalancerOperation(context.Background(), busyRefusalSubject, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, refusal
	})
	if calls != 1 {
		t.Fatalf("request sent %d times, want once", calls)
	}
	if !errors.Is(err, refusal) || !errors.Is(err, loadbalancersdk.ErrConflict) {
		t.Fatalf("err=%v, want the busy refusal wrapped", err)
	}
	for _, want := range []string{busyRefusalSubject, "refused as busy", "load balancer is busy"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestRequestLoadBalancerOperationReturnsOtherAnswersAtOnce(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		calls := 0
		got, err := requestLoadBalancerOperation(context.Background(), busyRefusalSubject, func(context.Context) (string, error) {
			calls++
			return "done", nil
		})
		if err != nil || got != "done" || calls != 1 {
			t.Fatalf("got=%q calls=%d err=%v, want the value of one request", got, calls, err)
		}
	})
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"conflict", busyRefusalError(http.StatusConflict, "conflict")},
		{"busy code on a validation status", busyRefusalError(http.StatusUnprocessableEntity, resourceBusyErrorCode)},
		{"busy code on a backend failure", busyRefusalError(http.StatusInternalServerError, resourceBusyErrorCode)},
		{"backend failure", busyRefusalError(http.StatusBadGateway, "bad_gateway")},
		{"not found", busyRefusalError(http.StatusNotFound, "not_found")},
		{"transport failure", errors.New("connection reset by peer")},
		{"request deadline", context.DeadlineExceeded},
		{"idle wait timeout", fmt.Errorf("load balancer: %w after %s", wait.ErrTimeout, loadBalancerReadyTimeout)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			_, err := requestLoadBalancerOperation(context.Background(), busyRefusalSubject, func(context.Context) (struct{}, error) {
				calls++
				return struct{}{}, tt.err
			})
			if calls != 1 || err != tt.err { //nolint:errorlint // The helper must return the answer itself, not a wrapped copy.
				t.Fatalf("calls=%d err=%v, want %v on the first attempt", calls, err, tt.err)
			}
		})
	}
}

// Update sends one PATCH after the idle wait, and returns its refusal without
// another idle wait or PATCH. Only a busy refusal names the update in the
// diagnostic. Update releases the claims lock of the Load Balancer afterwards.
func TestLoadBalancerUpdateReturnsARefusalOfTheOnePatch(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		patch      loadBalancerAnswer
		wantDetail []string
		wantBusy   bool
	}{
		{
			name:       "busy refusal",
			patch:      loadBalancerAnswer{http.StatusConflict, loadBalancerBusyBody},
			wantDetail: []string{"the update of load balancer " + loadBalancerTestID.String(), "refused as busy", "load balancer is busy"},
			wantBusy:   true,
		},
		{
			name:       "conflict of a failed operation",
			patch:      loadBalancerAnswer{http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`},
			wantDetail: []string{"The last operation on load balancer failed."},
		},
		{
			name:       "busy code on a validation status",
			patch:      loadBalancerAnswer{http.StatusUnprocessableEntity, loadBalancerBusyBody},
			wantDetail: []string{"load balancer is busy"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var resourceUnderTest *LoadBalancerResource
				var patchedAt []time.Duration
				var lockTaken atomic.Bool
				gets := 0
				start := time.Now()
				resourceUnderTest = newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch req.Method {
					case http.MethodGet:
						if len(patchedAt) > 0 {
							t.Fatal("Update read the Load Balancer again after the refused PATCH")
						}
						gets++
						// The first two GETs show the claim of another
						// client as PENDING_UPDATE.
						if gets <= 2 {
							return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE).response(req), nil
						}
						return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
					case http.MethodPatch:
						patchedAt = append(patchedAt, time.Since(start))
						go func() {
							unlock, err := resourceUnderTest.claims.Lock(context.Background(), loadBalancerTestID.String())
							if err == nil {
								lockTaken.Store(true)
								unlock()
							}
						}()
						return tt.patch.response(req), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				stateModel := loadBalancerConfiguredModel()
				stateModel.ID = types.StringValue(loadBalancerTestID.String())
				planModel := stateModel
				planModel.Name = types.StringValue("updated")
				response := executeLoadBalancerUpdate(t, resourceUnderTest, stateModel, planModel)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error updating Load Balancer")
				if want := []time.Duration{2 * loadBalancerReadyPollInterval}; !slices.Equal(patchedAt, want) {
					t.Fatalf("PATCH sent at %v, want once at %v", patchedAt, want)
				}
				detail := response.Diagnostics[0].Detail()
				for _, want := range tt.wantDetail {
					if !strings.Contains(detail, want) {
						t.Fatalf("diagnostic detail %q does not contain %q", detail, want)
					}
				}
				if got := strings.Contains(detail, "refused as busy"); got != tt.wantBusy {
					t.Fatalf("diagnostic detail %q names a busy refusal=%t, want %t", detail, got, tt.wantBusy)
				}
				synctest.Wait()
				if !lockTaken.Load() {
					t.Fatal("Update did not release the claims lock")
				}
			})
		})
	}
}

// Delete sends one DELETE after the idle wait, and returns its refusal without
// another idle wait, DELETE, or delete wait. Only a busy refusal names the
// deletion in the diagnostic.
func TestLoadBalancerDeleteReturnsARefusalOfTheOneDelete(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		deletion   loadBalancerAnswer
		wantDetail []string
		wantBusy   bool
	}{
		{
			name:       "busy refusal",
			deletion:   loadBalancerAnswer{http.StatusConflict, loadBalancerBusyBody},
			wantDetail: []string{"the deletion of load balancer " + loadBalancerTestID.String(), "refused as busy", "load balancer is busy"},
			wantBusy:   true,
		},
		{
			name:       "conflict of a failed operation",
			deletion:   loadBalancerAnswer{http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`},
			wantDetail: []string{"The last operation on load balancer failed."},
		},
		{
			name:       "backend failure",
			deletion:   loadBalancerAnswer{http.StatusInternalServerError, `{"detail":"delete failed"}`},
			wantDetail: []string{"delete failed"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var deletedAt []time.Duration
				gets := 0
				start := time.Now()
				resourceUnderTest := newLoadBalancerTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch req.Method {
					case http.MethodGet:
						if len(deletedAt) > 0 {
							t.Fatal("Delete read the Load Balancer again after the refused DELETE")
						}
						gets++
						if gets == 1 {
							return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE).response(req), nil
						}
						return loadBalancerStatusAnswer(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE).response(req), nil
					case http.MethodDelete:
						deletedAt = append(deletedAt, time.Since(start))
						return tt.deletion.response(req), nil
					default:
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}
				}))
				state := loadBalancerState(t)
				response := resource.DeleteResponse{State: state}
				resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
				assertLoadBalancerDiagnostic(t, response.Diagnostics, "Error deleting Load Balancer")
				if want := []time.Duration{loadBalancerReadyPollInterval}; !slices.Equal(deletedAt, want) {
					t.Fatalf("DELETE sent at %v, want once at %v", deletedAt, want)
				}
				detail := response.Diagnostics[0].Detail()
				for _, want := range tt.wantDetail {
					if !strings.Contains(detail, want) {
						t.Fatalf("diagnostic detail %q does not contain %q", detail, want)
					}
				}
				if got := strings.Contains(detail, "refused as busy"); got != tt.wantBusy {
					t.Fatalf("diagnostic detail %q names a busy refusal=%t, want %t", detail, got, tt.wantBusy)
				}
			})
		})
	}
}

func TestBuildLoadBalancerCreateBody(t *testing.T) {
	t.Parallel()

	plan := loadBalancerConfiguredModel()
	plan.Type = types.StringValue(" native ")
	body, diags := buildLoadBalancerCreateBody(context.Background(), plan, func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		if filter.Name == nil || *filter.Name != "vn-central-1" {
			t.Fatalf("unexpected region filter: %#v", filter)
		}
		return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: loadBalancerTestRegionID, Name: "vn-central-1"}}, nil
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "terraform-lb" || body.Package != loadbalancersdk.LoadBalancerPackageSMALL ||
		body.Topology != loadbalancersdk.LoadBalancerTopologySINGLE || body.RegionId != loadBalancerTestRegionID || body.VpcId != loadBalancerTestVPCID {
		t.Fatalf("unexpected required create fields: %#v", body)
	}
	if body.Description == nil || *body.Description != "Managed by Terraform" ||
		core.NullableToPtr(body.PrivateIpId) == nil || *core.NullableToPtr(body.PrivateIpId) != loadBalancerTestPrivateIPID ||
		core.NullableToPtr(body.QosPolicyId) == nil || *core.NullableToPtr(body.QosPolicyId) != loadBalancerTestQoSID ||
		body.Type == nil || *body.Type != loadbalancersdk.LoadBalancerTypeNative {
		t.Fatalf("unexpected optional create fields: %#v", body)
	}
}

func TestBuildLoadBalancerCreateBodyPresenceAndValidation(t *testing.T) {
	t.Parallel()

	resolve := func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: loadBalancerTestRegionID, Name: "vn-central-1"}}, nil
	}

	plan := loadBalancerConfiguredModel()
	plan.Description = types.StringNull()
	plan.PrivateIP = reference.Value(reference.ID, types.StringUnknown())
	plan.QoSPolicy = types.ObjectNull(reference.AttributeTypes(reference.ID))
	plan.Type = types.StringUnknown()
	body, diags := buildLoadBalancerCreateBody(context.Background(), plan, resolve)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Description != nil || body.PrivateIpId.IsSpecified() || body.QosPolicyId.IsSpecified() || body.Type != nil {
		t.Fatalf("null and unknown optional values must be omitted: %#v", body)
	}

	plan.Description = types.StringValue("")
	body, diags = buildLoadBalancerCreateBody(context.Background(), plan, resolve)
	if diags.HasError() || body.Description == nil || *body.Description != "" {
		t.Fatalf("explicit empty description must be sent: body=%#v diagnostics=%v", body, diags)
	}

	for _, test := range []struct {
		name   string
		mutate func(*LoadBalancerResourceModel)
	}{
		{name: "missing name", mutate: func(m *LoadBalancerResourceModel) { m.Name = types.StringValue(" ") }},
		{name: "missing package", mutate: func(m *LoadBalancerResourceModel) { m.Package = types.StringUnknown() }},
		{name: "missing topology", mutate: func(m *LoadBalancerResourceModel) { m.Topology = types.StringNull() }},
		{name: "missing region", mutate: func(m *LoadBalancerResourceModel) {
			m.Region = reference.Value(reference.Name, types.StringValue(" "))
		}},
		{name: "unknown region", mutate: func(m *LoadBalancerResourceModel) {
			m.Region = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
		}},
		{name: "invalid vpc", mutate: func(m *LoadBalancerResourceModel) {
			m.VPC = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
		}},
		{name: "invalid private IP", mutate: func(m *LoadBalancerResourceModel) {
			m.PrivateIP = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
		}},
		{name: "invalid QoS policy", mutate: func(m *LoadBalancerResourceModel) {
			m.QoSPolicy = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			invalid := loadBalancerConfiguredModel()
			test.mutate(&invalid)
			_, gotDiags := buildLoadBalancerCreateBody(context.Background(), invalid, resolve)
			if !gotDiags.HasError() {
				t.Fatal("expected validation diagnostic")
			}
		})
	}
}

func TestBuildLoadBalancerUpdateBody(t *testing.T) {
	t.Parallel()

	base := loadBalancerConfiguredModel()
	tests := []struct {
		name        string
		mutate      func(*LoadBalancerResourceModel)
		wantChanged bool
		check       func(*testing.T, loadbalancersdk.LoadBalancerPartialUpdateSchema)
	}{
		{name: "unchanged", mutate: func(*LoadBalancerResourceModel) {}},
		{name: "omitted description preserves", mutate: func(m *LoadBalancerResourceModel) { m.Description = types.StringNull() }},
		{
			name: "name is trimmed",
			mutate: func(m *LoadBalancerResourceModel) {
				m.Name = types.StringValue(" updated ")
			},
			wantChanged: true,
			check: func(t *testing.T, body loadbalancersdk.LoadBalancerPartialUpdateSchema) {
				if body.Name == nil || *body.Name != "updated" {
					t.Fatalf("unexpected name: %#v", body.Name)
				}
			},
		},
		{
			name: "padded description is trimmed",
			mutate: func(m *LoadBalancerResourceModel) {
				m.Description = types.StringValue(" updated ")
			},
			wantChanged: true,
			check: func(t *testing.T, body loadbalancersdk.LoadBalancerPartialUpdateSchema) {
				if description := core.NullableToPtr(body.Description); description == nil || *description != "updated" {
					t.Fatalf("unexpected description: %#v", body.Description)
				}
			},
		},
		{
			name: "outer whitespace in description alone is unchanged",
			mutate: func(m *LoadBalancerResourceModel) {
				m.Description = types.StringValue(" Managed by Terraform\t")
			},
		},
		{
			name: "empty description clears",
			mutate: func(m *LoadBalancerResourceModel) {
				m.Description = types.StringValue("")
			},
			wantChanged: true,
			check: func(t *testing.T, body loadbalancersdk.LoadBalancerPartialUpdateSchema) {
				if description := core.NullableToPtr(body.Description); description == nil || *description != "" {
					t.Fatalf("unexpected description: %#v", body.Description)
				}
			},
		},
		{
			name: "package and topology change",
			mutate: func(m *LoadBalancerResourceModel) {
				m.Package = types.StringValue("MEDIUM")
				m.Topology = types.StringValue("ACTIVE_STANDBY")
			},
			wantChanged: true,
			check: func(t *testing.T, body loadbalancersdk.LoadBalancerPartialUpdateSchema) {
				if body.Package == nil || *body.Package != loadbalancersdk.LoadBalancerPackageMEDIUM ||
					body.Topology == nil || *body.Topology != loadbalancersdk.LoadBalancerTopologyACTIVESTANDBY {
					t.Fatalf("unexpected sizing update: %#v", body)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := base
			tt.mutate(&plan)
			body, changed, diags := buildLoadBalancerUpdateBody(plan, base)
			if diags.HasError() || changed != tt.wantChanged {
				t.Fatalf("changed=%t diagnostics=%v", changed, diags)
			}
			if tt.check != nil {
				tt.check(t, body)
			}
		})
	}

	state := base
	state.Package = types.StringValue(" SMALL ")
	state.Topology = types.StringValue(" SINGLE ")
	plan := base
	plan.Description = types.StringValue("updated")
	body, changed, diags := buildLoadBalancerUpdateBody(plan, state)
	if diags.HasError() || !changed || core.NullableToPtr(body.Description) == nil || *core.NullableToPtr(body.Description) != "updated" || body.Package != nil || body.Topology != nil {
		t.Fatalf("normalized unchanged enums must not be patched: body=%#v diagnostics=%v", body, diags)
	}
}

func TestLoadBalancerAssociationDiagnostics(t *testing.T) {
	t.Parallel()

	applied := loadBalancerTestQoSID
	other := core.UUID{7}
	for _, tt := range []struct {
		name        string
		configured  types.String
		applied     *core.UUID
		wantSummary string
		wantError   bool
	}{
		{name: "unconfigured", configured: types.StringNull()},
		{name: "unknown", configured: types.StringUnknown(), applied: &applied},
		{name: "echoed", configured: types.StringValue(applied.String()), applied: &applied},
		{
			name:        "omitted",
			configured:  types.StringValue(applied.String()),
			wantSummary: "Load Balancer qos_policy was not reported by the API",
			wantError:   true,
		},
		{
			name:        "mismatched",
			configured:  types.StringValue(applied.String()),
			applied:     &other,
			wantSummary: "Load Balancer qos_policy does not match the configuration",
			wantError:   true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags := loadBalancerAssociationDiagnostics("qos_policy", tt.configured, tt.applied)
			if tt.wantSummary == "" {
				if len(diags) != 0 {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if len(diags) != 1 || diags[0].Summary() != tt.wantSummary || diags.HasError() != tt.wantError {
				t.Fatalf("diagnostics=%v, want %q error=%t", diags, tt.wantSummary, tt.wantError)
			}
			withPath, ok := diags[0].(diag.DiagnosticWithPath)
			if !ok || !withPath.Path().Equal(path.Root("qos_policy").AtName(reference.ID)) {
				t.Fatalf("diagnostic path=%v, want qos_policy.id", diags[0])
			}
		})
	}
}

func TestBuildLoadBalancerPublicIPBody(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		mode     types.String
		wantBody string
	}{
		{name: "ipv4-only", mode: types.StringValue("ipv4-only"), wantBody: `{"mode":"ipv4-only"}`},
		{name: "dual-stack", mode: types.StringValue("dual-stack"), wantBody: `{"mode":"dual-stack"}`},
		{name: "null turns public IP off", mode: types.StringNull(), wantBody: `{"mode":null}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := buildLoadBalancerPublicIPBody(tt.mode)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			encoded, err := json.Marshal(body)
			if err != nil || string(encoded) != tt.wantBody {
				t.Fatalf("body=%s err=%v, want %s", encoded, err, tt.wantBody)
			}
		})
	}
	if _, err := buildLoadBalancerPublicIPBody(types.StringUnknown()); err == nil {
		t.Fatal("an unknown mode must not be sent")
	}
	// An unset SDK field sends no JSON null, which is why the builder sets it.
	if encoded, err := json.Marshal(loadbalancersdk.LoadBalancerPublicIPSchema{}); err != nil || string(encoded) == `{"mode":null}` {
		t.Fatalf("unset mode body=%s err=%v; the builder no longer needs SetNull", encoded, err)
	}
}

func TestLoadBalancerPublicIPModeDiagnostics(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		configured types.String
		applied    types.String
		wantError  bool
	}{
		{name: "both null", configured: types.StringNull(), applied: types.StringNull()},
		{name: "same mode", configured: types.StringValue("ipv4-only"), applied: types.StringValue("ipv4-only")},
		{name: "padded same mode", configured: types.StringValue(" ipv4-only "), applied: types.StringValue("ipv4-only")},
		{name: "unknown configuration", configured: types.StringUnknown(), applied: types.StringNull()},
		{name: "missing public IP", configured: types.StringValue("ipv4-only"), applied: types.StringNull(), wantError: true},
		{name: "unexpected public IP", configured: types.StringNull(), applied: types.StringValue("ipv4-only"), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags := loadBalancerPublicIPModeDiagnostics(tt.configured, tt.applied)
			if diags.HasError() != tt.wantError {
				t.Fatalf("diagnostics=%v, want error=%t", diags, tt.wantError)
			}
			if !tt.wantError {
				return
			}
			withPath, ok := diags[0].(diag.DiagnosticWithPath)
			if !ok || !withPath.Path().Equal(path.Root("public_ip_mode")) || !strings.Contains(diags[0].Detail(), "public IP off") {
				t.Fatalf("diagnostic=%v, want a public_ip_mode error that names the off state", diags[0])
			}
		})
	}
}

func TestPopulateLoadBalancerResourceState(t *testing.T) {
	t.Parallel()

	privateIPID := loadBalancerTestPrivateIPID
	lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	lb.PrivateIpId.Set(privateIPID)
	lb.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{Id: loadBalancerTestQoSID, Name: "gold"})
	lb.PublicAddress.Set("203.0.113.10")
	state := loadBalancerConfiguredModel()
	state.Name = types.StringValue(" terraform-lb ")
	state.VPC = reference.Value(reference.ID, types.StringValue(strings.ToUpper(loadBalancerTestVPCID.String())))
	populateLoadBalancerResourceState(lb, &state)

	if state.ID.ValueString() != loadBalancerTestID.String() || state.Name.ValueString() != " terraform-lb " ||
		reference.Get(state.Region, reference.Name).ValueString() != "  vn-central-1  " ||
		reference.Get(state.VPC, reference.ID).ValueString() != strings.ToUpper(loadBalancerTestVPCID.String()) {
		t.Fatalf("configured equivalent values were not preserved: %#v", state)
	}
	if reference.Get(state.PrivateIP, reference.ID).ValueString() != loadBalancerTestPrivateIPID.String() ||
		reference.Get(state.QoSPolicy, reference.ID).ValueString() != loadBalancerTestQoSID.String() {
		t.Fatalf("configured associations were not preserved: %#v", state)
	}
	if state.PrivateIPID.ValueString() != loadBalancerTestPrivateIPID.String() ||
		state.QoSPolicyID.ValueString() != loadBalancerTestQoSID.String() || state.QoSPolicyName.ValueString() != "gold" ||
		state.VIPAddress.ValueString() != "10.0.0.10" || state.PublicAddress.ValueString() != "203.0.113.10" ||
		!state.V6PublicAddress.IsNull() || state.ProvisioningStatus.ValueString() != "ACTIVE" ||
		state.Type.ValueString() != "appliance" || !state.AccessLogEnabled.Equal(types.BoolValue(false)) ||
		!state.AccessLogStoreID.IsNull() || !state.AccessLogStoreName.IsNull() {
		t.Fatalf("API values were not mapped: %#v", state)
	}
	accessLogStoreID := core.UUID{8}
	lb.AccessLogEnabled = true
	lb.AccessLogStore.Set(loadbalancersdk.NestedLogStoreSchema{Id: accessLogStoreID, Name: "logs"})
	populateLoadBalancerResourceState(lb, &state)
	if !state.AccessLogEnabled.Equal(types.BoolValue(true)) || state.AccessLogStoreID.ValueString() != accessLogStoreID.String() || state.AccessLogStoreName.ValueString() != "logs" {
		t.Fatalf("access log store was not mapped: %#v", state)
	}
	v6PublicAddress := "2001:db8::10"
	lb.V6PublicAddress.Set(v6PublicAddress)
	populateLoadBalancerResourceState(lb, &state)
	if state.V6PublicAddress.ValueString() != v6PublicAddress {
		t.Fatalf("IPv6 public address was not mapped: %#v", state)
	}

	lb.PrivateIpId = nil
	lb.QosPolicy = nil
	lb.PublicAddress = nil
	lb.V6PublicAddress = nil
	populateLoadBalancerResourceState(lb, &state)
	if !state.PublicAddress.IsNull() || !state.V6PublicAddress.IsNull() ||
		!state.PrivateIPID.IsNull() || !state.QoSPolicyID.IsNull() || !state.QoSPolicyName.IsNull() {
		t.Fatalf("absent API associations must map to null: %#v", state)
	}
	// A configured reference without a backend association maps to null, so
	// the plan shows the drift.
	if !state.PrivateIP.IsNull() || !state.QoSPolicy.IsNull() {
		t.Fatalf("configured references must show missing associations: %#v", state)
	}

	planned := loadBalancerCreatePlanModel()
	planned.PrivateIP = types.ObjectNull(reference.AttributeTypes(reference.ID))
	planned.QoSPolicy = types.ObjectNull(reference.AttributeTypes(reference.ID))
	populateLoadBalancerResourceState(lb, &planned)
	if !planned.PrivateIPID.IsNull() || !planned.QoSPolicyID.IsNull() || !planned.QoSPolicyName.IsNull() {
		t.Fatalf("omitted associations must resolve unknown to null: %#v", planned)
	}
}

func TestPopulateLoadBalancerModelKeepsOmittedReferencesNull(t *testing.T) {
	t.Parallel()

	privateIPID := loadBalancerTestPrivateIPID
	lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	lb.PrivateIpId.Set(privateIPID)
	lb.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{Id: loadBalancerTestQoSID, Name: "gold"})
	state := loadBalancerCreatePlanModel()
	state.PrivateIP = types.ObjectNull(reference.AttributeTypes(reference.ID))
	state.QoSPolicy = types.ObjectNull(reference.AttributeTypes(reference.ID))

	populateLoadBalancerResourceState(lb, &state)

	if !state.PrivateIP.IsNull() || !state.QoSPolicy.IsNull() {
		t.Fatalf("omitted references must stay null: private_ip=%v qos_policy=%v", state.PrivateIP, state.QoSPolicy)
	}
	if state.PrivateIPID.ValueString() != privateIPID.String() || state.QoSPolicyID.ValueString() != loadBalancerTestQoSID.String() {
		t.Fatalf("backend associations must appear in the flat IDs: private_ip_id=%v qos_policy_id=%v", state.PrivateIPID, state.QoSPolicyID)
	}
}

func TestPopulateLoadBalancerModelReplacesDriftedReferences(t *testing.T) {
	t.Parallel()

	privateIPID := core.UUID{7}
	lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	lb.PrivateIpId.Set(privateIPID)
	lb.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{Id: core.UUID{8}, Name: "gold"})
	lb.Vpc.Id = core.UUID{9}
	lb.Region.Name = "vn-south-1"
	state := loadBalancerConfiguredModel()

	populateLoadBalancerResourceState(lb, &state)

	for name, got := range map[string]types.String{
		"region":     reference.Get(state.Region, reference.Name),
		"vpc":        reference.Get(state.VPC, reference.ID),
		"private_ip": reference.Get(state.PrivateIP, reference.ID),
		"qos_policy": reference.Get(state.QoSPolicy, reference.ID),
	} {
		want := map[string]string{
			"region":     "vn-south-1",
			"vpc":        core.UUID{9}.String(),
			"private_ip": privateIPID.String(),
			"qos_policy": core.UUID{8}.String(),
		}[name]
		if got.ValueString() != want {
			t.Errorf("%s=%v, want backend value %q", name, got, want)
		}
	}
}

func TestPopulateLoadBalancerModelPreservesConfiguredAssociationUUIDs(t *testing.T) {
	t.Parallel()

	privateIPID, err := core.ParseUUID("abcdefab-cdef-abcd-efab-cdefabcdefab")
	if err != nil {
		t.Fatal(err)
	}
	qosPolicyID, err := core.ParseUUID("fedcbafe-dcba-fedc-bafe-dcbafedcbafe")
	if err != nil {
		t.Fatal(err)
	}
	lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	lb.PrivateIpId.Set(privateIPID)
	lb.QosPolicy.Set(loadbalancersdk.NestedQoSPolicySchema{Id: qosPolicyID, Name: "gold"})
	state := loadBalancerConfiguredModel()
	state.PrivateIP = reference.Value(reference.ID, types.StringValue(strings.ToUpper(privateIPID.String())))
	state.QoSPolicy = reference.Value(reference.ID, types.StringValue(strings.ToUpper(qosPolicyID.String())))

	populateLoadBalancerResourceState(lb, &state)

	if got := reference.Get(state.PrivateIP, reference.ID); got.ValueString() != strings.ToUpper(privateIPID.String()) {
		t.Fatalf("private_ip.id=%q, want configured UUID representation", got.ValueString())
	}
	if got := reference.Get(state.QoSPolicy, reference.ID); got.ValueString() != strings.ToUpper(qosPolicyID.String()) {
		t.Fatalf("qos_policy.id=%q, want configured UUID representation", got.ValueString())
	}
	if state.PrivateIPID.ValueString() != privateIPID.String() || state.QoSPolicyID.ValueString() != qosPolicyID.String() {
		t.Fatalf("flat IDs must use the backend representation: private_ip_id=%v qos_policy_id=%v", state.PrivateIPID, state.QoSPolicyID)
	}
}

func TestPopulateLoadBalancerDescriptionPresence(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		prior    types.String
		apiValue *string
		want     types.String
	}{
		{name: "absent removes prior value", want: types.StringNull()},
		{name: "explicit empty stays known", apiValue: new(""), want: types.StringValue("")},
		{name: "nonempty replaces prior value", apiValue: new("backend"), want: types.StringValue("backend")},
		{name: "trimmed match keeps configured spelling", prior: types.StringValue(" backend\n"), apiValue: new("backend"), want: types.StringValue(" backend\n")},
		{name: "absent removes padded prior value", prior: types.StringValue(" backend "), want: types.StringNull()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
			lb.Description = core.NullableFromPtr(tt.apiValue)
			state := loadBalancerConfiguredModel()
			if !tt.prior.IsNull() {
				state.Description = tt.prior
			}
			populateLoadBalancerResourceState(lb, &state)
			if !state.Description.Equal(tt.want) {
				t.Fatalf("description=%v, want %v", state.Description, tt.want)
			}
		})
	}
}

func TestPopulateLoadBalancerTypeWhenBackendOmitsIt(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		prior    types.String
		apiValue loadbalancersdk.LoadBalancerType
		want     types.String
	}{
		{name: "backend value replaces prior value", prior: types.StringValue("appliance"), apiValue: loadbalancersdk.LoadBalancerTypeNative, want: types.StringValue("native")},
		{name: "backend value keeps padded prior spelling", prior: types.StringValue(" native "), apiValue: loadbalancersdk.LoadBalancerTypeNative, want: types.StringValue(" native ")},
		{name: "absent keeps configured value", prior: types.StringValue("native"), want: types.StringValue("native")},
		{name: "absent after omission uses create default", prior: types.StringUnknown(), want: types.StringValue("appliance")},
		{name: "absent on import uses create default", prior: types.StringNull(), want: types.StringValue("appliance")},
		{name: "absent replaces empty prior value", prior: types.StringValue(""), want: types.StringValue("appliance")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
			lb.Type = tt.apiValue
			state := loadBalancerConfiguredModel()
			state.Type = tt.prior
			populateLoadBalancerResourceState(lb, &state)
			if !state.Type.Equal(tt.want) {
				t.Fatalf("type=%v, want %v", state.Type, tt.want)
			}
		})
	}
}

func TestPopulateLoadBalancerPublicIPMode(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name            string
		publicAddress   *string
		v6PublicAddress *string
		prior           types.String
		want            types.String
	}{
		{name: "no address is off", prior: types.StringValue("ipv4-only"), want: types.StringNull()},
		{name: "IPv4 address", publicAddress: new("203.0.113.10"), want: types.StringValue("ipv4-only")},
		{name: "IPv4 and IPv6 addresses", publicAddress: new("203.0.113.10"), v6PublicAddress: new("2001:db8::10"), prior: types.StringValue("ipv4-only"), want: types.StringValue("dual-stack")},
		{name: "IPv6 without IPv4 is off", v6PublicAddress: new("2001:db8::10"), want: types.StringNull()},
		{name: "padded prior keeps its spelling", publicAddress: new("203.0.113.10"), v6PublicAddress: new("2001:db8::10"), prior: types.StringValue(" dual-stack "), want: types.StringValue(" dual-stack ")},
		{name: "padded prior of another mode", publicAddress: new("203.0.113.10"), prior: types.StringValue(" dual-stack "), want: types.StringValue("ipv4-only")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lb := loadBalancerAPIModel(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
			lb.PublicAddress = core.NullableFromPtr(tt.publicAddress)
			lb.V6PublicAddress = core.NullableFromPtr(tt.v6PublicAddress)
			state := loadBalancerConfiguredModel()
			state.PublicIPMode = tt.prior
			populateLoadBalancerResourceState(lb, &state)
			if !state.PublicIPMode.Equal(tt.want) {
				t.Fatalf("public_ip_mode=%v, want %v", state.PublicIPMode, tt.want)
			}
		})
	}
}

func loadBalancerConfiguredModel() LoadBalancerResourceModel {
	return LoadBalancerResourceModel{
		LoadBalancerModel: LoadBalancerModel{
			Name:        types.StringValue(" terraform-lb "),
			Description: types.StringValue("Managed by Terraform"),
			Package:     types.StringValue("SMALL"),
			Topology:    types.StringValue("SINGLE"),
			Region:      reference.Value(reference.Name, types.StringValue("  vn-central-1  ")),
			VPC:         reference.Value(reference.ID, types.StringValue(loadBalancerTestVPCID.String())),
			PrivateIP:   reference.Value(reference.ID, types.StringValue(loadBalancerTestPrivateIPID.String())),
			QoSPolicy:   reference.Value(reference.ID, types.StringValue(loadBalancerTestQoSID.String())),
			PrivateIPID: types.StringNull(),
			QoSPolicyID: types.StringNull(),
		},
	}
}

func loadBalancerCreatePlanModel() LoadBalancerResourceModel {
	model := loadBalancerConfiguredModel()
	model.ID = types.StringUnknown()
	model.RegionID = types.StringUnknown()
	model.VPCName = types.StringUnknown()
	model.PrivateIPID = types.StringUnknown()
	model.QoSPolicyID = types.StringUnknown()
	model.QoSPolicyName = types.StringUnknown()
	model.VIPAddress = types.StringUnknown()
	model.PublicAddress = types.StringUnknown()
	model.V6PublicAddress = types.StringUnknown()
	model.Status = types.StringUnknown()
	model.ProvisioningStatus = types.StringUnknown()
	model.Type = types.StringUnknown()
	model.AccessLogEnabled = types.BoolUnknown()
	model.AccessLogStoreID = types.StringUnknown()
	model.AccessLogStoreName = types.StringUnknown()
	model.CreatedAt = types.StringUnknown()
	model.UpdatedAt = types.StringUnknown()
	return model
}

func loadBalancerAPIModel(status loadbalancersdk.LoadBalancingProvisioningStatus) *loadbalancersdk.LoadBalancerDetailSchema {
	description := "Managed by Terraform"
	privateIPID := loadBalancerTestPrivateIPID
	vipAddress := "10.0.0.10"
	return &loadbalancersdk.LoadBalancerDetailSchema{
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Description:        core.NullableFromPtr(&description),
		Id:                 loadBalancerTestID,
		Name:               "terraform-lb",
		Package:            loadbalancersdk.LoadBalancerPackageSMALL,
		PrivateIpId:        core.NullableFromPtr(&privateIPID),
		ProvisioningStatus: status,
		QosPolicy:          core.NullableFromPtr(&loadbalancersdk.NestedQoSPolicySchema{Id: loadBalancerTestQoSID, Name: "gold"}),
		Region:             loadbalancersdk.NestedRegionSchema{Id: loadBalancerTestRegionID, Name: "vn-central-1"},
		Status:             loadbalancersdk.LoadBalancingStatusNOMONITOR,
		Topology:           loadbalancersdk.LoadBalancerTopologySINGLE,
		Type:               loadbalancersdk.LoadBalancerTypeAppliance,
		UpdatedAt:          time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		VipAddress:         core.NullableFromPtr(&vipAddress),
		Vpc:                loadbalancersdk.NestedVPCSchema{Id: loadBalancerTestVPCID, Name: "terraform-vpc", DisplayName: "vpc-display"},
	}
}

func loadBalancerResponseJSON(status loadbalancersdk.LoadBalancingProvisioningStatus) string {
	body, err := json.Marshal(loadBalancerAPIModel(status))
	if err != nil {
		panic(err)
	}
	return string(body)
}

// loadBalancerPublicIPJSON returns a Load Balancer response with the given
// public addresses. An empty address is absent from the response.
func loadBalancerPublicIPJSON(t *testing.T, status loadbalancersdk.LoadBalancingProvisioningStatus, publicAddress, v6PublicAddress string) string {
	t.Helper()
	lb := loadBalancerAPIModel(status)
	if publicAddress != "" {
		lb.PublicAddress.Set(publicAddress)
	}
	if v6PublicAddress != "" {
		lb.V6PublicAddress.Set(v6PublicAddress)
	}
	return loadBalancerJSON(t, lb)
}

func newLoadBalancerTestResource(t *testing.T, transport http.RoundTripper) *LoadBalancerResource {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	client, err := loadbalancersdk.NewClient("https://load-balancer.test", loadbalancersdk.WithHTTPClient(httpClient), loadbalancersdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(httpClient), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return &LoadBalancerResource{client: client, project: projectClient, projectID: loadBalancerTestProjectID, claims: keylock.New()}
}

func loadBalancerResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func loadBalancerRegionResponse(req *http.Request) *http.Response {
	return loadBalancerResponse(req, http.StatusOK, fmt.Sprintf(`{"count":1,"next":null,"previous":null,"results":[{"region":{"id":%q,"name":"vn-central-1","description":""},"services":[]}]}`, loadBalancerTestRegionID.String()))
}

func loadBalancerAssociationTransport(t *testing.T, body string) loadBalancerRoundTripFunc {
	t.Helper()
	return func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+loadBalancerTestProjectID.String()+"/regions/":
			return loadBalancerRegionResponse(req), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v2/load-balancing/loadbalancers/":
			return loadBalancerResponse(req, http.StatusCreated, body), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v2/load-balancing/loadbalancers/"+loadBalancerTestID.String()+"/":
			return loadBalancerResponse(req, http.StatusOK, body), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	}
}

func executeLoadBalancerCreate(t *testing.T, resourceUnderTest *LoadBalancerResource, planModel LoadBalancerResourceModel) resource.CreateResponse {
	t.Helper()
	resourceSchema := loadBalancerSchema(t)
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: resourceSchema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	return response
}

func executeLoadBalancerUpdate(t *testing.T, resourceUnderTest *LoadBalancerResource, stateModel, planModel LoadBalancerResourceModel) resource.UpdateResponse {
	t.Helper()
	resourceSchema := loadBalancerSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	planState := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set update state: %v", diags)
	}
	if diags := planState.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan{Schema: resourceSchema, Raw: planState.Raw}}, &response)
	return response
}

func loadBalancerSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&LoadBalancerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	return response.Schema
}

func loadBalancerState(t *testing.T) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: loadBalancerSchema(t)}
	model := loadBalancerConfiguredModel()
	model.ID = types.StringValue(loadBalancerTestID.String())
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set resource state: %v", diags)
	}
	return state
}

func loadBalancerModifierState(t *testing.T, resourceSchema schema.Schema) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema}
	model := loadBalancerConfiguredModel()
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	return state
}

func loadBalancerModifierPlan(t *testing.T, resourceSchema schema.Schema) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: resourceSchema}
	model := loadBalancerConfiguredModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
	}
	return plan
}

func assertLoadBalancerReferenceCriterion(t *testing.T, name string, attribute schema.SingleNestedAttribute, criterion string) {
	t.Helper()
	if len(attribute.Attributes) != 1 {
		t.Errorf("%s must have exactly one criterion, got %#v", name, attribute.Attributes)
	}
	value, ok := attribute.Attributes[criterion].(schema.StringAttribute)
	if !ok || !value.Required {
		t.Errorf("%s.%s must be a required string criterion, got %#v", name, criterion, attribute.Attributes[criterion])
	}
}

func assertLoadBalancerDiagnostic(t *testing.T, diagnostics diag.Diagnostics, wantSummary string) {
	t.Helper()
	if wantSummary == "" {
		if diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diagnostics)
		}
		return
	}
	if !diagnostics.HasError() || diagnostics[0].Summary() != wantSummary {
		t.Fatalf("diagnostics=%v, want summary %q", diagnostics, wantSummary)
	}
}

// loadBalancerAnswer is one HTTP answer of the fake Load Balancer API.
type loadBalancerAnswer struct {
	status int
	body   string
}

// loadBalancerStatusAnswer is a GET answer for a Load Balancer in status.
func loadBalancerStatusAnswer(status loadbalancersdk.LoadBalancingProvisioningStatus) loadBalancerAnswer {
	return loadBalancerAnswer{http.StatusOK, loadBalancerResponseJSON(status)}
}

func (a loadBalancerAnswer) response(req *http.Request) *http.Response {
	return loadBalancerResponse(req, a.status, a.body)
}

// busyRefusalError returns the SDK error for an HTTP status whose one error
// detail has code and the message of loadBalancerBusyBody.
func busyRefusalError(status int, code string) error {
	body, err := json.Marshal(map[string]any{
		"errors":     []map[string]string{{"location": "", "code": code, "message": "load balancer is busy, please try again later."}},
		"request_id": "f1dbd828b10848f2b05d2a1795d3cc9a",
	})
	if err != nil {
		panic(err)
	}
	return core.ParseErrorResponse(status, body)
}
