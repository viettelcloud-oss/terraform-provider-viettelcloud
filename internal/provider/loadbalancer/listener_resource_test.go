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

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

const listenerBackendError = `{"errors":[{"message":"backend exploded","code":"internal"}]}`

// listenerAPI answers the requests that name the test listener with the
// handler for their HTTP method, and every other request with 404.
func listenerAPI(handlers map[string]func() *http.Response) listenerRoundTripFunc {
	// A GET before the PATCH or DELETE is the idle wait, so it sees an ACTIVE
	// Listener. The GET handler answers the polls after the mutation.
	hasMutation := handlers[http.MethodPatch] != nil || handlers[http.MethodDelete] != nil
	mutated := false
	return func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/listeners/"+listenerTestID.String()) {
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		if req.Method == http.MethodGet && hasMutation && !mutated {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
		}
		mutated = mutated || req.Method != http.MethodGet
		if handle, ok := handlers[req.Method]; ok {
			return handle(), nil
		}
		return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	}
}

func TestListenerModelMatchesSchema(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	plan := tfsdk.Plan{Schema: s}
	model := ListenerResourceModel{
		ListenerModel: ListenerModel{
			ID:                      types.StringValue(listenerTestID.String()),
			Name:                    types.StringValue("http-listener"),
			Description:             types.StringValue("test listener"),
			Protocol:                types.StringValue("HTTP"),
			ProtocolPort:            types.Int64Value(80),
			AllowedCIDRs:            types.SetValueMust(types.StringType, []attr.Value{types.StringValue("192.168.1.0/24")}),
			ListenerTimeout:         types.Int64Value(50000),
			ServerTimeout:           types.Int64Value(50000),
			ServerConnectingTimeout: types.Int64Value(5000),
			EnableH2:                types.BoolValue(true),
			SetXForwardedFor:        types.BoolValue(true),
			SetXForwardedPort:       types.BoolValue(true),
			SetXForwardedProto:      types.BoolValue(true),
			ProvisioningStatus:      types.StringValue("ACTIVE"),
			Status:                  types.StringValue("ONLINE"),
			CreatedAt:               types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:               types.StringValue("2026-01-01T00:00:00Z"),
		},
		LoadBalancer:   listenerTestReference(listenerTestLoadBalancerID.String()),
		ServerGroup:    listenerTestReference(listenerTestServerGroupID.String()),
		DefaultTLSCert: listenerTestReference(listenerTestTLSCertID.String()),
		SNITLSCerts:    listenerTestReferenceSet(listenerTestSNICertID.String()),
	}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestListenerResourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceUnderTest, ok := NewListenerResource().(*ListenerResource)
	if !ok || resourceUnderTest == nil {
		t.Fatal("constructor did not return a ListenerResource")
	}

	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_listener" {
		t.Fatalf("metadata type=%q, want viettelcloud_lb_listener", metadata.TypeName)
	}

	var badResp resource.ConfigureResponse
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &badResp)
	if !badResp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic for unexpected provider data type")
	}

	testRes := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
		return nil, nil
	})
	configured := &providerdata.Configured{
		LoadBalancer: testRes.client,
		ProjectID:    listenerTestProjectID,
		Claims:       keylock.New(),
	}
	var goodResp resource.ConfigureResponse
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &goodResp)
	if goodResp.Diagnostics.HasError() || resourceUnderTest.client != configured.LoadBalancer || resourceUnderTest.projectID != listenerTestProjectID || resourceUnderTest.claims != configured.Claims {
		t.Fatalf("resource was not configured correctly: diagnostics=%v resource=%#v", goodResp.Diagnostics, resourceUnderTest)
	}
}

func TestListenerResourceSchemaContract(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)

	protocolAttr, ok := s.Attributes["protocol"].(schema.StringAttribute)
	if !ok || !protocolAttr.Required || len(protocolAttr.PlanModifiers) != 1 {
		t.Errorf("protocol must be required and RequiresReplace, got %#v", s.Attributes["protocol"])
	}

	loadBalancerAttr, ok := s.Attributes["load_balancer"].(schema.SingleNestedAttribute)
	if !ok || !loadBalancerAttr.Required || len(loadBalancerAttr.PlanModifiers) != 1 {
		t.Errorf("load_balancer must be a required reference object with RequiresReplace, got %#v", s.Attributes["load_balancer"])
	} else if id, ok := loadBalancerAttr.Attributes[reference.ID].(schema.StringAttribute); !ok || !id.Required || len(loadBalancerAttr.Attributes) != 1 {
		t.Errorf("load_balancer must select the load balancer by a required id only, got %#v", loadBalancerAttr.Attributes)
	}
	for _, name := range []string{"load_balancer_id", "server_group_id", "default_tls_cert_id", "sni_tls_cert_ids"} {
		if _, ok := s.Attributes[name]; ok {
			t.Errorf("%s must be modeled as a reference object, not a plain string", name)
		}
	}

	portAttr, ok := s.Attributes["protocol_port"].(schema.Int64Attribute)
	if !ok || !portAttr.Required || len(portAttr.PlanModifiers) != 1 {
		t.Errorf("protocol_port must be required and RequiresReplace, got %#v", s.Attributes["protocol_port"])
	}

	nameAttr, ok := s.Attributes["name"].(schema.StringAttribute)
	if !ok || !nameAttr.Required || len(nameAttr.PlanModifiers) != 0 {
		t.Errorf("name must be required and mutable, got %#v", s.Attributes["name"])
	}

	for _, name := range []string{"description"} {
		attr, ok := s.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Optional || attr.Computed || len(attr.PlanModifiers) != 0 {
			t.Errorf("%s must be optional only, got %#v", name, s.Attributes[name])
		}
	}
	for _, name := range []string{"server_group", "default_tls_cert"} {
		attribute, ok := s.Attributes[name].(schema.SingleNestedAttribute)
		if !ok || !attribute.Optional || attribute.Computed || len(attribute.PlanModifiers) != 0 {
			t.Errorf("%s must be an optional-only reference object, so removal detaches it, got %#v", name, s.Attributes[name])
		} else if id, ok := attribute.Attributes[reference.ID].(schema.StringAttribute); !ok || !id.Required || id.Computed || len(attribute.Attributes) != 1 {
			t.Errorf("%s must select the object by a required id only, got %#v", name, attribute.Attributes)
		}
	}

	for _, name := range []string{"listener_timeout", "server_timeout", "server_connecting_timeout"} {
		attr, ok := s.Attributes[name].(schema.Int64Attribute)
		if !ok || !attr.Optional || !attr.Computed || len(attr.PlanModifiers) != 1 {
			t.Errorf("%s must be optional, computed, and UseStateForUnknown, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"enable_h2", "set_x_forwarded_for", "set_x_forwarded_port", "set_x_forwarded_proto"} {
		attr, ok := s.Attributes[name].(schema.BoolAttribute)
		if !ok || !attr.Optional || !attr.Computed || len(attr.PlanModifiers) != 1 {
			t.Errorf("%s must be optional and computed with omission preservation, got %#v", name, s.Attributes[name])
		}
	}

	cidrsAttr, ok := s.Attributes["allowed_cidrs"].(schema.SetAttribute)
	if !ok || !cidrsAttr.Optional || cidrsAttr.Computed || len(cidrsAttr.PlanModifiers) != 0 {
		t.Errorf("allowed_cidrs must be optional only, got %#v", s.Attributes["allowed_cidrs"])
	}

	sniAttr, ok := s.Attributes["sni_tls_certs"].(schema.SetNestedAttribute)
	if !ok || !sniAttr.Optional || !sniAttr.Computed || len(sniAttr.PlanModifiers) != 1 {
		t.Errorf("sni_tls_certs must preserve omitted reference objects, got %#v", s.Attributes["sni_tls_certs"])
	} else if id, ok := sniAttr.NestedObject.Attributes[reference.ID].(schema.StringAttribute); !ok || !id.Optional || !id.Computed || id.Required || len(id.Validators) == 0 || len(sniAttr.NestedObject.Attributes) != 1 {
		t.Errorf("sni_tls_certs elements must preserve their id and require it when configured, got %#v", sniAttr.NestedObject.Attributes)
	}

	for _, name := range []string{"provisioning_status", "status", "updated_at"} {
		attr, ok := s.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Optional || attr.Required {
			t.Errorf("%s must be computed-only, got %#v", name, s.Attributes[name])
		}
	}

	for _, name := range []string{"id", "created_at"} {
		attr, ok := s.Attributes[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Optional || attr.Required || len(attr.PlanModifiers) != 1 {
			t.Errorf("%s must be computed-only with UseStateForUnknown, got %#v", name, s.Attributes[name])
		}
	}
}

func TestListenerPreservedReferenceCriterionValidation(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	for _, field := range []string{"sni_tls_certs"} {
		criterion := s.Attributes[field].(schema.SetNestedAttribute).NestedObject.Attributes[reference.ID].(schema.StringAttribute)
		for name, value := range map[string]types.String{
			"missing": types.StringNull(),
			"unknown": types.StringUnknown(),
			"known":   types.StringValue(listenerTestServerGroupID.String()),
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				t.Parallel()
				request := validator.StringRequest{
					Path:        path.Root(field).AtName(reference.ID),
					ConfigValue: value,
				}
				var response validator.StringResponse
				for _, rule := range criterion.Validators {
					rule.ValidateString(context.Background(), request, &response)
				}
				if response.Diagnostics.HasError() != value.IsNull() {
					t.Fatalf("configured reference criterion %s: %v", value, response.Diagnostics)
				}
			})
		}
	}
}

func TestListenerOptionalComputedPlanPreservesOmittedValues(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	state := tfsdk.State{Schema: s}
	model := defaultTestListenerModel()
	model.ServerGroup = listenerTestReference(listenerTestServerGroupID.String())
	model.DefaultTLSCert = listenerTestReference(listenerTestTLSCertID.String())
	model.EnableH2 = types.BoolValue(true)
	model.SetXForwardedFor = types.BoolValue(true)
	model.SetXForwardedPort = types.BoolValue(true)
	model.SetXForwardedProto = types.BoolValue(true)
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize prior state: %v", diags)
	}
	sniAttribute := s.Attributes["sni_tls_certs"].(schema.SetNestedAttribute)
	priorCertificates := listenerTestReferenceSet(listenerTestSNICertID.String())
	setRequest := planmodifier.SetRequest{
		State:       state,
		ConfigValue: types.SetNull(reference.ObjectType(reference.ID)),
		StateValue:  priorCertificates,
		PlanValue:   types.SetUnknown(reference.ObjectType(reference.ID)),
	}
	setResponse := planmodifier.SetResponse{PlanValue: setRequest.PlanValue}
	sniAttribute.PlanModifiers[0].PlanModifySet(context.Background(), setRequest, &setResponse)
	if setResponse.Diagnostics.HasError() || !setResponse.PlanValue.Equal(priorCertificates) || setResponse.RequiresReplace {
		t.Fatalf("omitted SNI certificates must remain configured without replacement: %#v", setResponse)
	}
	for _, name := range []string{"enable_h2", "set_x_forwarded_for", "set_x_forwarded_port", "set_x_forwarded_proto"} {
		attribute := s.Attributes[name].(schema.BoolAttribute)
		for _, desired := range []types.Bool{types.BoolUnknown(), types.BoolValue(false)} {
			request := planmodifier.BoolRequest{
				State:       state,
				ConfigValue: types.BoolNull(),
				StateValue:  types.BoolValue(true),
				PlanValue:   desired,
			}
			response := planmodifier.BoolResponse{PlanValue: desired}
			attribute.PlanModifiers[0].PlanModifyBool(context.Background(), request, &response)
			want := desired
			if desired.IsUnknown() {
				want = request.StateValue
			}
			if response.Diagnostics.HasError() || !response.PlanValue.Equal(want) || response.RequiresReplace {
				t.Errorf("%s must preserve omission and accept false: %#v", name, response)
			}
		}
	}
}

func TestListenerProtocolValidation(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	attr := s.Attributes["protocol"].(schema.StringAttribute)

	validProtocols := []string{"HTTP", "HTTPS", "TERMINATED_HTTPS", "TCP", "UDP", " HTTP ", "\tUDP\n"}
	for _, proto := range validProtocols {
		req := validator.StringRequest{
			Path:        path.Root("protocol"),
			ConfigValue: types.StringValue(proto),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if resp.Diagnostics.HasError() {
			t.Errorf("expected protocol %q to be valid, got errors: %v", proto, resp.Diagnostics)
		}
	}

	invalidProtocols := []string{"FTP", "GRPC", "http", "udp", "  "}
	for _, proto := range invalidProtocols {
		req := validator.StringRequest{
			Path:        path.Root("protocol"),
			ConfigValue: types.StringValue(proto),
		}
		var resp validator.StringResponse
		for _, v := range attr.Validators {
			v.ValidateString(context.Background(), req, &resp)
		}
		if !resp.Diagnostics.HasError() {
			t.Errorf("expected protocol %q to be rejected", proto)
		}
	}
}

func TestListenerReplacementIgnoresSpelling(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	model := defaultTestListenerModel()
	model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
	model.Name = types.StringValue("web")
	model.Protocol = types.StringValue("HTTP")
	model.ProtocolPort = types.Int64Value(80)
	state := tfsdk.State{Schema: s}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: s, Raw: state.Raw}

	protocol := s.Attributes["protocol"].(schema.StringAttribute)
	for value, wantReplacement := range map[string]bool{"HTTP": false, " HTTP\n": false, "TCP": true} {
		request := planmodifier.StringRequest{
			Path: path.Root("protocol"), State: state, Plan: plan,
			ConfigValue: types.StringValue(value), PlanValue: types.StringValue(value), StateValue: model.Protocol,
		}
		response := planmodifier.StringResponse{PlanValue: request.PlanValue}
		for _, modifier := range protocol.PlanModifiers {
			modifier.PlanModifyString(context.Background(), request, &response)
		}
		if response.RequiresReplace != wantReplacement {
			t.Errorf("protocol %q: replace=%t, want %t", value, response.RequiresReplace, wantReplacement)
		}
	}

	loadBalancer := s.Attributes["load_balancer"].(schema.SingleNestedAttribute)
	for value, wantReplacement := range map[string]bool{
		"  " + strings.ToUpper(listenerTestLoadBalancerID.String()) + " ": false,
		listenerTestServerGroupID.String():                                true,
	} {
		request := planmodifier.ObjectRequest{
			Path: path.Root("load_balancer"), State: state, Plan: plan,
			ConfigValue: listenerTestReference(value), PlanValue: listenerTestReference(value), StateValue: model.LoadBalancer,
		}
		response := planmodifier.ObjectResponse{PlanValue: request.PlanValue}
		for _, modifier := range loadBalancer.PlanModifiers {
			modifier.PlanModifyObject(context.Background(), request, &response)
		}
		if response.RequiresReplace != wantReplacement {
			t.Errorf("load_balancer.id %q: replace=%t, want %t", value, response.RequiresReplace, wantReplacement)
		}
	}
}

func TestListenerPortValidation(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	attr := s.Attributes["protocol_port"].(schema.Int64Attribute)

	for _, port := range []int64{1, 80, 443, 8080, 65535} {
		req := validator.Int64Request{
			Path:        path.Root("protocol_port"),
			ConfigValue: types.Int64Value(port),
		}
		var resp validator.Int64Response
		for _, v := range attr.Validators {
			v.ValidateInt64(context.Background(), req, &resp)
		}
		if resp.Diagnostics.HasError() {
			t.Errorf("expected port %d to be valid, got errors: %v", port, resp.Diagnostics)
		}
	}

	for _, port := range []int64{0, -1, 65536, 100000} {
		req := validator.Int64Request{
			Path:        path.Root("protocol_port"),
			ConfigValue: types.Int64Value(port),
		}
		var resp validator.Int64Response
		for _, v := range attr.Validators {
			v.ValidateInt64(context.Background(), req, &resp)
		}
		if !resp.Diagnostics.HasError() {
			t.Errorf("expected port %d to be rejected", port)
		}
	}
}

func TestListenerTimeoutValidation(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)

	// The API schema bounds every timeout to 1..2073600000 ms, so 0 is rejected
	// before a request reaches the backend.
	for _, name := range []string{"listener_timeout", "server_timeout", "server_connecting_timeout"} {
		attr := s.Attributes[name].(schema.Int64Attribute)
		validate := func(v int64) bool {
			req := validator.Int64Request{Path: path.Root(name), ConfigValue: types.Int64Value(v)}
			var resp validator.Int64Response
			for _, val := range attr.Validators {
				val.ValidateInt64(context.Background(), req, &resp)
			}
			return !resp.Diagnostics.HasError()
		}
		for _, v := range []int64{1, 5000, 50000, 2073600000} {
			if !validate(v) {
				t.Errorf("expected %s %d to be valid", name, v)
			}
		}
		for _, v := range []int64{0, -1, 2073600001} {
			if validate(v) {
				t.Errorf("expected %s %d to be rejected", name, v)
			}
		}
	}
}

// The backend stores enable_h2 = true on HTTPS and TERMINATED_HTTPS whatever
// the request sends, so only a known false on these protocols is rejected.
func TestListenerValidateConfigRejectsDisabledH2OnSecureProtocols(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	for _, tt := range []struct {
		protocol types.String
		enableH2 types.Bool
		wantErr  bool
	}{
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolValue(false), wantErr: true},
		{protocol: types.StringValue("TERMINATED_HTTPS"), enableH2: types.BoolValue(false), wantErr: true},
		{protocol: types.StringValue(" HTTPS\n"), enableH2: types.BoolValue(false), wantErr: true},
		{protocol: types.StringValue("HTTPS"), enableH2: types.BoolValue(true)},
		{protocol: types.StringValue("TERMINATED_HTTPS"), enableH2: types.BoolNull()},
		{protocol: types.StringValue("TERMINATED_HTTPS"), enableH2: types.BoolUnknown()},
		{protocol: types.StringValue("HTTP"), enableH2: types.BoolValue(false)},
		{protocol: types.StringUnknown(), enableH2: types.BoolValue(false)},
	} {
		t.Run(fmt.Sprintf("%s enable_h2=%s", tt.protocol, tt.enableH2), func(t *testing.T) {
			t.Parallel()
			model := defaultTestListenerModel()
			model.Protocol, model.EnableH2 = tt.protocol, tt.enableH2
			config := tfsdk.State{Schema: s}
			if diags := config.Set(context.Background(), &model); diags.HasError() {
				t.Fatalf("build config: %v", diags)
			}

			var resp resource.ValidateConfigResponse
			(&ListenerResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &resp)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics=%v, want an error: %t", resp.Diagnostics, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			withPath, ok := resp.Diagnostics.Errors()[0].(diag.DiagnosticWithPath)
			if !ok || !withPath.Path().Equal(path.Root("enable_h2")) {
				t.Fatalf("diagnostic=%v, want an error on enable_h2", resp.Diagnostics.Errors()[0])
			}
		})
	}
}

func TestListenerValidateConfigRejectsTrimmedDuplicates(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	certID := listenerTestSNICertID.String()
	for _, tt := range []struct {
		name     string
		cidrs    types.Set
		certs    types.Set
		wantPath path.Path
	}{
		{name: "distinct values", cidrs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/24"), types.StringValue(" 10.0.1.0/24 ")}), certs: listenerTestReferenceSet(certID)},
		{name: "unknown CIDR", cidrs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/24"), types.StringUnknown()}), certs: types.SetNull(reference.ObjectType(reference.ID))},
		{name: "padded CIDR", cidrs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/24"), types.StringValue(" 10.0.0.0/24")}), certs: types.SetNull(reference.ObjectType(reference.ID)), wantPath: path.Root("allowed_cidrs")},
		{name: "padded upper-case certificate", cidrs: types.SetNull(types.StringType), certs: listenerTestReferenceSet(certID, " "+strings.ToUpper(certID)), wantPath: path.Root("sni_tls_certs")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := defaultTestListenerModel()
			model.Protocol = types.StringValue("TERMINATED_HTTPS")
			model.AllowedCIDRs, model.SNITLSCerts = tt.cidrs, tt.certs
			config := tfsdk.State{Schema: s}
			if diags := config.Set(context.Background(), &model); diags.HasError() {
				t.Fatalf("build config: %v", diags)
			}

			var resp resource.ValidateConfigResponse
			(&ListenerResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &resp)
			if wantErr := len(tt.wantPath.Steps()) > 0; resp.Diagnostics.HasError() != wantErr {
				t.Fatalf("diagnostics=%v, want an error: %t", resp.Diagnostics, wantErr)
			}
			if resp.Diagnostics.HasError() {
				withPath, ok := resp.Diagnostics.Errors()[0].(diag.DiagnosticWithPath)
				if !ok || !withPath.Path().Equal(tt.wantPath) {
					t.Fatalf("diagnostic=%v, want an error on %s", resp.Diagnostics.Errors()[0], tt.wantPath)
				}
			}
		})
	}
}

func TestListenerResourceCreateFailsOnTerminalStatus(t *testing.T) {
	t.Parallel()

	resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return listenerJSONResponse(http.StatusCreated, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_CREATE", "OFFLINE")), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/listeners/%s/", listenerTestID)) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ERROR", "ERROR")), nil
		}
		return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
	})

	s := listenerSchema(t)
	plan := tfsdk.Plan{Schema: s}
	model := defaultTestListenerModel()
	model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
	model.Name = types.StringValue("web")
	model.Protocol = types.StringValue("HTTP")
	model.ProtocolPort = types.Int64Value(80)
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: s}

	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic on terminal ERROR status")
	}

	var state ListenerResourceModel
	if diags := createResp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != listenerTestID.String() {
		t.Fatalf("expected ID to be preserved in state, got %s", state.ID.ValueString())
	}
}

// The API trims request strings, so Create sends trimmed values. State keeps
// the configured spelling through Create and the next Read. The listener
// response has no description, so state keeps it as configured.
func TestListenerResourceCreateTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	const padded = " web listener\t"
	var sent []string
	resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost:
			if !strings.Contains(req.URL.Path, listenerTestLoadBalancerID.String()) {
				t.Errorf("create path %s does not name load balancer %s", req.URL.Path, listenerTestLoadBalancerID)
			}
			var body struct {
				Name        string  `json:"name"`
				Protocol    string  `json:"protocol"`
				Description *string `json:"description"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Description == nil {
				t.Fatal("create body omitted description")
			}
			if body.Name != "web" || body.Protocol != "HTTP" {
				t.Errorf("create body name=%q protocol=%q, want trimmed values", body.Name, body.Protocol)
			}
			sent = append(sent, *body.Description)
			return listenerJSONResponse(http.StatusCreated, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_CREATE", "OFFLINE")), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/listeners/%s/", listenerTestID)):
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	s := listenerSchema(t)
	plan := tfsdk.Plan{Schema: s}
	model := defaultTestListenerModel()
	model.LoadBalancer = listenerTestReference("  " + strings.ToUpper(listenerTestLoadBalancerID.String()) + "\n")
	model.Name = types.StringValue(" web ")
	model.Description = types.StringValue(padded)
	model.Protocol = types.StringValue(" HTTP\t")
	model.ProtocolPort = types.Int64Value(80)
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResp.Diagnostics)
	}
	if want := []string{"web listener"}; !slices.Equal(sent, want) {
		t.Fatalf("sent descriptions = %q, want %q", sent, want)
	}
	var created ListenerResourceModel
	if diags := createResp.State.Get(context.Background(), &created); diags.HasError() {
		t.Fatalf("get create state: %v", diags)
	}
	if !created.Description.Equal(model.Description) || !created.Name.Equal(model.Name) || !created.Protocol.Equal(model.Protocol) || !created.LoadBalancer.Equal(model.LoadBalancer) {
		t.Fatalf("create state = %#v, want the configured spellings", created)
	}

	readResp := resource.ReadResponse{State: createResp.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResp.Diagnostics)
	}
	var refreshed ListenerResourceModel
	if diags := readResp.State.Get(context.Background(), &refreshed); diags.HasError() {
		t.Fatalf("get read state: %v", diags)
	}
	if !refreshed.Description.Equal(model.Description) || !refreshed.Name.Equal(model.Name) || !refreshed.Protocol.Equal(model.Protocol) || !refreshed.LoadBalancer.Equal(model.LoadBalancer) {
		t.Fatalf("read state = %#v, want the configured spellings", refreshed)
	}
}

// The API trims description, so the patch sends the trimmed description and
// a change of outer whitespace alone is not a change.
func TestBuildListenerPartialUpdateTrimsDescription(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		state     types.String
		plan      types.String
		wantPatch *string
	}{
		{name: "padded new description is trimmed", state: types.StringValue("old desc"), plan: types.StringValue(" new desc\n"), wantPatch: new("new desc")},
		{name: "padded description over null is trimmed", state: types.StringNull(), plan: types.StringValue(" new desc "), wantPatch: new("new desc")},
		{name: "outer whitespace alone is unchanged", state: types.StringValue("same desc"), plan: types.StringValue(" same desc ")},
		{name: "removed outer whitespace is unchanged", state: types.StringValue(" same desc "), plan: types.StringValue("same desc")},
		{name: "omitted description clears", state: types.StringValue(" old desc "), plan: types.StringNull(), wantPatch: new("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := defaultTestListenerModel()
			state.Name = types.StringValue("listener")
			state.Description = tt.state
			plan := state
			plan.Description = tt.plan

			body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if tt.wantPatch == nil {
				if hasChanges || body.Description.IsSpecified() {
					t.Fatalf("hasChanges=%t description=%#v, want no patch", hasChanges, body.Description)
				}
				return
			}
			if !hasChanges || !listenerNullableHolds(body.Description, *tt.wantPatch) {
				t.Fatalf("hasChanges=%t description=%#v, want %q", hasChanges, body.Description, *tt.wantPatch)
			}
		})
	}
}

// The API trims name and parses IDs, so a change of outer whitespace or UUID
// spelling alone sends no patch.
func TestBuildListenerPartialUpdateIgnoresSpelling(t *testing.T) {
	t.Parallel()

	serverGroupID := listenerTestServerGroupID.String()
	certID := listenerTestTLSCertID.String()
	for _, tt := range []struct {
		name       string
		mutate     func(*ListenerResourceModel)
		wantChange bool
	}{
		{name: "padded name", mutate: func(m *ListenerResourceModel) { m.Name = types.StringValue(" listener\n") }},
		{name: "padded upper-case server group", mutate: func(m *ListenerResourceModel) {
			m.ServerGroup = listenerTestReference("  " + strings.ToUpper(serverGroupID) + " ")
		}},
		{name: "padded default certificate", mutate: func(m *ListenerResourceModel) { m.DefaultTLSCert = listenerTestReference("\t" + certID) }},
		{name: "renamed listener", mutate: func(m *ListenerResourceModel) { m.Name = types.StringValue(" renamed ") }, wantChange: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := defaultTestListenerModel()
			state.Name = types.StringValue("listener")
			state.ServerGroup = listenerTestReference(serverGroupID)
			state.DefaultTLSCert = listenerTestReference(certID)
			plan := state
			tt.mutate(&plan)

			body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
			if diags.HasError() || hasChanges != tt.wantChange {
				t.Fatalf("hasChanges=%t diagnostics=%v, want %t", hasChanges, diags, tt.wantChange)
			}
			if tt.wantChange && (body.Name == nil || *body.Name != "renamed") {
				t.Fatalf("name patch = %v, want %q", body.Name, "renamed")
			}
		})
	}
}

func TestListenerResourceRead(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "my-listener", "HTTP", 80, "ACTIVE", "ONLINE")), nil
		})
		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp resource.ReadResponse
		resp.State = state
		resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read failed: %v", resp.Diagnostics)
		}

		var updatedModel ListenerResourceModel
		if diags := resp.State.Get(context.Background(), &updatedModel); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if updatedModel.Name.ValueString() != "my-listener" || updatedModel.ProtocolPort.ValueInt64() != 80 {
			t.Fatalf("unexpected read model: %#v", updatedModel)
		}
	})

	t.Run("404 removes resource from state", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})
		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp resource.ReadResponse
		resp.State = state
		resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Fatal("expected missing listener to be removed from state")
		}
	})

	t.Run("DELETED provisioning status removes resource from state", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "my-listener", "HTTP", 80, "DELETED", "OFFLINE")), nil
		})
		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var resp resource.ReadResponse
		resp.State = state
		resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.State.Raw.IsNull() {
			t.Fatal("expected deleted listener to be removed from state")
		}
	})
}

func TestListenerResourceUpdate(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)

	for _, statusCode := range []int{http.StatusOK, http.StatusAccepted} {
		t.Run(fmt.Sprintf("in place update with HTTP %d", statusCode), func(t *testing.T) {
			t.Parallel()
			var patchBody map[string]any
			resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPatch {
					bodyBytes, _ := io.ReadAll(req.Body)
					_ = json.Unmarshal(bodyBytes, &patchBody)
					return listenerJSONResponse(statusCode, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ACTIVE", "ONLINE")), nil
				}
				if req.Method == http.MethodGet {
					return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ACTIVE", "ONLINE")), nil
				}
				return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
			})

			stateModel := defaultTestListenerModel()
			stateModel.ID = types.StringValue(listenerTestID.String())
			stateModel.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
			stateModel.Name = types.StringValue("old-name")
			stateModel.Protocol = types.StringValue("HTTP")
			stateModel.ProtocolPort = types.Int64Value(80)
			stateModel.ListenerTimeout = types.Int64Value(50000)
			stateModel.ServerTimeout = types.Int64Value(50000)
			stateModel.ServerConnectingTimeout = types.Int64Value(5000)
			planModel := stateModel
			planModel.Name = types.StringValue("new-name")
			planModel.ListenerTimeout = types.Int64Value(60000)

			state := tfsdk.State{Schema: s}
			plan := tfsdk.Plan{Schema: s}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			var updateResp resource.UpdateResponse
			updateResp.State = state

			resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
			if updateResp.Diagnostics.HasError() {
				t.Fatalf("update failed: %v", updateResp.Diagnostics)
			}

			var updatedModel ListenerResourceModel
			if diags := updateResp.State.Get(context.Background(), &updatedModel); diags.HasError() {
				t.Fatalf("get state: %v", diags)
			}
			if updatedModel.ID.ValueString() != listenerTestID.String() || updatedModel.Name.ValueString() != "new-name" {
				t.Fatalf("expected update to preserve identity and refresh the name, got %#v", updatedModel)
			}

			if patchBody["name"] != "new-name" {
				t.Fatalf("expected PATCH request with new name, got %#v", patchBody)
			}
			if timeout, ok := patchBody["listener_timeout"].(float64); !ok || int64(timeout) != 60000 {
				t.Fatalf("expected PATCH request with listener_timeout 60000, got %#v", patchBody["listener_timeout"])
			}
		})
	}

	t.Run("removed references are detached", func(t *testing.T) {
		t.Parallel()
		var patchBody map[string]json.RawMessage
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			detail := listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "listener", "HTTPS", 443, "ACTIVE", "ONLINE"))
			switch req.Method {
			case http.MethodPatch:
				bodyBytes, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(bodyBytes, &patchBody)
				return detail, nil
			case http.MethodGet:
				return detail, nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		stateModel := defaultTestListenerModel()
		stateModel.ID = types.StringValue(listenerTestID.String())
		stateModel.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		stateModel.Name = types.StringValue("listener")
		stateModel.Protocol = types.StringValue("HTTPS")
		stateModel.ProtocolPort = types.Int64Value(443)
		stateModel.ListenerTimeout = types.Int64Value(50000)
		stateModel.ServerTimeout = types.Int64Value(50000)
		stateModel.ServerConnectingTimeout = types.Int64Value(5000)
		stateModel.ServerGroup = listenerTestReference(listenerTestServerGroupID.String())
		stateModel.DefaultTLSCert = listenerTestReference(listenerTestTLSCertID.String())
		planModel := stateModel
		planModel.ServerGroup = types.ObjectNull(reference.AttributeTypes(reference.ID))
		planModel.DefaultTLSCert = types.ObjectNull(reference.AttributeTypes(reference.ID))
		planModel.UpdatedAt = types.StringUnknown()

		state := tfsdk.State{Schema: s}
		plan := tfsdk.Plan{Schema: s}
		if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		updateResp := resource.UpdateResponse{State: state}
		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
		if updateResp.Diagnostics.HasError() {
			t.Fatalf("update failed: %v", updateResp.Diagnostics)
		}
		for _, field := range []string{"server_group_id", "default_tls_cert_id"} {
			if got := string(patchBody[field]); got != "null" {
				t.Errorf("PATCH %s = %q, want JSON null", field, got)
			}
		}
		var updated ListenerResourceModel
		if diags := updateResp.State.Get(context.Background(), &updated); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if !updated.ServerGroup.IsNull() || !updated.DefaultTLSCert.IsNull() || updated.ID.ValueString() != listenerTestID.String() {
			t.Fatalf("detached references must be null in state with the same ID, got %#v", updated)
		}
	})

	t.Run("no-op update does not call update API", func(t *testing.T) {
		t.Parallel()
		var patchCalled bool
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPatch {
				patchCalled = true
				t.Fatal("PATCH must not be called when plan has no changes")
			}
			if req.Method == http.MethodGet {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "same-name", "HTTP", 80, "ACTIVE", "ONLINE")), nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		model.Name = types.StringValue("same-name")
		model.Protocol = types.StringValue("HTTP")
		model.ProtocolPort = types.Int64Value(80)
		model.ListenerTimeout = types.Int64Value(50000)
		model.ServerTimeout = types.Int64Value(50000)
		model.ServerConnectingTimeout = types.Int64Value(5000)
		state := tfsdk.State{Schema: s}
		plan := tfsdk.Plan{Schema: s}
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		if diags := plan.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}

		var updateResp resource.UpdateResponse
		updateResp.State = state

		resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
		if updateResp.Diagnostics.HasError() {
			t.Fatalf("update failed: %v", updateResp.Diagnostics)
		}
		if patchCalled {
			t.Fatal("expected PATCH not to be called")
		}
	})

	stateModel := defaultTestListenerModel()
	stateModel.ID = types.StringValue(listenerTestID.String())
	stateModel.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
	stateModel.Name = types.StringValue("old-name")
	stateModel.Protocol = types.StringValue("HTTP")
	stateModel.ProtocolPort = types.Int64Value(80)
	stateModel.ListenerTimeout = types.Int64Value(50000)
	stateModel.ServerTimeout = types.Int64Value(50000)
	stateModel.ServerConnectingTimeout = types.Int64Value(5000)
	renamedModel := stateModel
	renamedModel.Name = types.StringValue("new-name")

	activeListener := func() *http.Response {
		return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ACTIVE", "ONLINE"))
	}
	for name, tt := range map[string]struct {
		handlers map[string]func() *http.Response
		plan     ListenerResourceModel
		wantErr  string
		wantText string
	}{
		"PATCH error": {
			handlers: map[string]func() *http.Response{
				http.MethodPatch: func() *http.Response {
					return listenerJSONResponse(http.StatusUnprocessableEntity, listenerBackendError)
				},
			},
			plan:     renamedModel,
			wantErr:  "Error updating Load Balancer Listener",
			wantText: "backend exploded",
		},
		"PATCH answered with another listener": {
			handlers: map[string]func() *http.Response{
				http.MethodPatch: func() *http.Response {
					return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(core.UUID{9}, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ACTIVE", "ONLINE"))
				},
			},
			plan:     renamedModel,
			wantErr:  "Error updating Load Balancer Listener",
			wantText: "not applied in place",
		},
		"terminal ERROR after PATCH": {
			handlers: map[string]func() *http.Response{
				http.MethodPatch: activeListener,
				http.MethodGet: func() *http.Response {
					return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ERROR", "ERROR"))
				},
			},
			plan:     renamedModel,
			wantErr:  "Error waiting for Load Balancer Listener update",
			wantText: "terminal provisioning status ERROR",
		},
		"poll answered with another listener": {
			handlers: map[string]func() *http.Response{
				http.MethodPatch: activeListener,
				http.MethodGet: func() *http.Response {
					return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(core.UUID{9}, listenerTestLoadBalancerID, "new-name", "HTTP", 80, "ACTIVE", "ONLINE"))
				},
			},
			plan:     renamedModel,
			wantErr:  "Error updating Load Balancer Listener",
			wantText: "not applied in place",
		},
		"refresh error without changes": {
			handlers: map[string]func() *http.Response{
				http.MethodGet: func() *http.Response {
					return listenerJSONResponse(http.StatusInternalServerError, listenerBackendError)
				},
			},
			plan:     stateModel,
			wantErr:  "Error updating Load Balancer Listener",
			wantText: "backend exploded",
		},
	} {
		t.Run(name+" is reported and keeps the prior state", func(t *testing.T) {
			t.Parallel()
			resourceUnderTest := newListenerTestResource(t, listenerAPI(tt.handlers))
			state := tfsdk.State{Schema: s}
			plan := tfsdk.Plan{Schema: s}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &tt.plan); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			updateResp := resource.UpdateResponse{State: state}
			resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResp)

			errs := updateResp.Diagnostics.Errors()
			if len(errs) != 1 || errs[0].Summary() != tt.wantErr || !strings.Contains(errs[0].Detail(), tt.wantText) {
				t.Fatalf("expected %q mentioning %q, got %v", tt.wantErr, tt.wantText, updateResp.Diagnostics)
			}
			if !updateResp.State.Raw.Equal(state.Raw) {
				t.Fatal("a failed update must not write new state")
			}
		})
	}
}

func TestListenerResourceDelete(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		var deleteCalled bool
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodDelete {
				deleteCalled = true
				return listenerJSONResponse(http.StatusAccepted, ""), nil
			}
			if req.Method == http.MethodGet && !deleteCalled {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
			}
			if req.Method == http.MethodGet {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "DELETED", "OFFLINE")), nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var deleteResp resource.DeleteResponse
		resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
		if deleteResp.Diagnostics.HasError() {
			t.Fatalf("delete failed: %v", deleteResp.Diagnostics)
		}
		if !deleteCalled {
			t.Fatal("expected delete API to be called")
		}
	})

	t.Run("404 during delete treated as success", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}

		var deleteResp resource.DeleteResponse
		resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
		if deleteResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on 404 delete: %v", deleteResp.Diagnostics)
		}
	})

	deleteListener := func(t *testing.T, resourceUnderTest *ListenerResource) resource.DeleteResponse {
		t.Helper()
		state := tfsdk.State{Schema: s}
		model := defaultTestListenerModel()
		model.ID = types.StringValue(listenerTestID.String())
		model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
		if diags := state.Set(context.Background(), &model); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		var deleteResp resource.DeleteResponse
		resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
		return deleteResp
	}

	t.Run("delete API error is reported", func(t *testing.T) {
		t.Parallel()
		resourceUnderTest := newListenerTestResource(t, listenerAPI(map[string]func() *http.Response{
			http.MethodDelete: func() *http.Response {
				return listenerJSONResponse(http.StatusUnprocessableEntity, listenerBackendError)
			},
		}))

		errs := deleteListener(t, resourceUnderTest).Diagnostics.Errors()
		if len(errs) != 1 || errs[0].Summary() != "Error deleting Load Balancer Listener" || !strings.Contains(errs[0].Detail(), "backend exploded") {
			t.Fatalf("expected the delete error to be reported, got %v", errs)
		}
	})

	t.Run("terminal ERROR after the delete request is reported", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			statuses := []string{"PENDING_DELETE", "ERROR"}
			var polls int
			resourceUnderTest := newListenerTestResource(t, listenerAPI(map[string]func() *http.Response{
				http.MethodDelete: func() *http.Response { return listenerJSONResponse(http.StatusAccepted, "") },
				http.MethodGet: func() *http.Response {
					status := statuses[min(polls, len(statuses)-1)]
					polls++
					return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, status, "OFFLINE"))
				},
			}))

			errs := deleteListener(t, resourceUnderTest).Diagnostics.Errors()
			if len(errs) != 1 || errs[0].Summary() != "Error waiting for Load Balancer Listener deletion" || !strings.Contains(errs[0].Detail(), "terminal provisioning status ERROR") {
				t.Fatalf("expected the terminal status to be reported, got %v", errs)
			}
		})
	})
}

// runListenerOperation runs the Create, Update, or Delete that method sends
// and returns its diagnostics.
func runListenerOperation(t *testing.T, resourceUnderTest *ListenerResource, method string) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	s := listenerSchema(t)
	model := defaultTestListenerModel()
	model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
	model.Name = types.StringValue("web")
	model.Protocol = types.StringValue("HTTP")
	model.ProtocolPort = types.Int64Value(80)
	if method == http.MethodPost {
		plan := tfsdk.Plan{Schema: s}
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		response := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		resourceUnderTest.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
		return response.Diagnostics
	}

	model.ID = types.StringValue(listenerTestID.String())
	model.ListenerTimeout = types.Int64Value(defaultListenerTimeout)
	model.ServerTimeout = types.Int64Value(listenerDefaultServerTimeout)
	model.ServerConnectingTimeout = types.Int64Value(listenerDefaultServerConnectingTimeout)
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if method == http.MethodDelete {
		response := resource.DeleteResponse{State: state}
		resourceUnderTest.Delete(ctx, resource.DeleteRequest{State: state}, &response)
		return response.Diagnostics
	}

	planModel := model
	planModel.Name = types.StringValue("new-name")
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
	return response.Diagnostics
}

// listenerOperations names each listener operation by the HTTP method that
// it sends, with the summary of its error diagnostic.
var listenerOperations = []struct {
	name, method, errorSummary string
}{
	{"create", http.MethodPost, "Error creating Load Balancer Listener"},
	{"update", http.MethodPatch, "Error updating Load Balancer Listener"},
	{"delete", http.MethodDelete, "Error deleting Load Balancer Listener"},
}

// The backend refuses a mutation with HTTP 409 and code resource_busy while
// another operation holds the claim of the Load Balancer. The Load Balancer
// reports PENDING_UPDATE on the first poll, so each listener mutation sends
// its one request after exactly one poll interval of idle wait. A busy refusal
// then returns at once, with no further poll and no second request, and the
// diagnostic names the Listener and the Load Balancer. Every other refusal,
// including a 409 with code conflict and a resource_busy code on any other
// status, also returns at once, and is not reported as a busy refusal.
func TestListenerReturnsABusyRefusal(t *testing.T) {
	t.Parallel()

	const (
		busyBody   = `{"errors":[{"location":"","code":"resource_busy","message":"load balancer is busy, please try again later."}]}`
		failedBody = `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`
	)
	subjects := map[string]string{
		http.MethodPost:   "the creation of a listener on load balancer " + listenerTestLoadBalancerID.String(),
		http.MethodPatch:  "the update of listener " + listenerTestID.String() + " on load balancer " + listenerTestLoadBalancerID.String(),
		http.MethodDelete: "the deletion of listener " + listenerTestID.String() + " on load balancer " + listenerTestLoadBalancerID.String(),
	}
	for _, op := range listenerOperations {
		for _, tt := range []struct {
			name     string
			status   int
			body     string
			wantBusy bool
			wantErr  string
		}{
			{name: "busy refusal returns at once", status: http.StatusConflict, body: busyBody, wantBusy: true, wantErr: "load balancer is busy"},
			{name: "409 conflict returns at once", status: http.StatusConflict, body: failedBody, wantErr: "last operation on load balancer failed"},
			{name: "resource_busy on 400 returns at once", status: http.StatusBadRequest, body: busyBody, wantErr: "load balancer is busy"},
			{name: "resource_busy on 503 returns at once", status: http.StatusServiceUnavailable, body: busyBody, wantErr: "load balancer is busy"},
		} {
			t.Run(op.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					var mutations []time.Time
					lbPendingPolls := 1
					pollsAfterMutation := 0
					resourceUnderTest := newListenerTestResourceWithParent(t, func(req *http.Request) (*http.Response, error) {
						if req.Method == http.MethodGet && len(mutations) > 0 {
							pollsAfterMutation++
						}
						switch {
						case req.Method == http.MethodGet && req.URL.Path == listenerTestLoadBalancerPath:
							status := loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
							if lbPendingPolls > 0 {
								lbPendingPolls--
								status = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
							}
							return listenerJSONResponse(http.StatusOK, loadBalancerResponseJSON(status)), nil
						case req.Method == http.MethodGet:
							return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
						case req.Method != op.method:
							t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
							return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
						}
						mutations = append(mutations, time.Now())
						return listenerJSONResponse(tt.status, tt.body), nil
					})
					diags := runListenerOperation(t, resourceUnderTest, op.method)
					if len(mutations) != 1 {
						t.Fatalf("%s calls=%d, want 1 (diagnostics=%v)", op.method, len(mutations), diags)
					}
					if wait := mutations[0].Sub(start); wait != loadBalancerReadyPollInterval {
						t.Fatalf("%s after %s, want after the idle wait of %s", op.method, wait, loadBalancerReadyPollInterval)
					}
					if pollsAfterMutation != 0 {
						t.Fatalf("GET calls after the %s=%d, want 0", op.method, pollsAfterMutation)
					}
					errs := diags.Errors()
					if len(errs) != 1 || errs[0].Summary() != op.errorSummary || !strings.Contains(errs[0].Detail(), tt.wantErr) {
						t.Fatalf("diagnostics=%v, want one %q containing %q", errs, op.errorSummary, tt.wantErr)
					}
					detail := errs[0].Detail()
					if busy := strings.Contains(detail, subjects[op.method]+" was refused as busy"); busy != tt.wantBusy {
						t.Fatalf("detail=%q, want a busy refusal of %q: %t", detail, subjects[op.method], tt.wantBusy)
					}
				})
			})
		}
	}
}

// Before each mutation, the listener waits until the Load Balancer, and for an
// update or a delete also the Listener, reaches its idle state. ERROR and
// UNKNOWN of a parent, or of the Listener before an update, fail without a
// request. A delete accepts a Listener in ERROR or UNKNOWN, and a Listener that
// is already gone completes the delete without a request. A missing Load
// Balancer does not prove that the Listener is gone, so that delete fails.
func TestListenerOperationsWaitForTheIdleState(t *testing.T) {
	t.Parallel()

	const gone = "NOT_FOUND"
	active := loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	pendingUpdate := loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE
	for _, tt := range []struct {
		name             string
		method           string
		lbStatuses       []loadbalancersdk.LoadBalancingProvisioningStatus
		listenerStatuses []string
		wantWait         time.Duration
		wantMutations    int
		wantErr          string
	}{
		{name: "create waits for a pending Load Balancer", method: http.MethodPost, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingUpdate, active}, wantWait: loadBalancerReadyPollInterval, wantMutations: 1},
		{name: "update waits for a pending Load Balancer", method: http.MethodPatch, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingUpdate, active}, wantWait: loadBalancerReadyPollInterval, wantMutations: 1},
		{name: "delete waits for a pending Load Balancer", method: http.MethodDelete, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{pendingUpdate, active}, wantWait: loadBalancerReadyPollInterval, wantMutations: 1},
		{name: "update waits for a pending Listener", method: http.MethodPatch, listenerStatuses: []string{"PENDING_UPDATE", "ACTIVE"}, wantWait: listenerReadyPollInterval, wantMutations: 1},
		{name: "delete waits for a pending Listener and accepts ERROR", method: http.MethodDelete, listenerStatuses: []string{"PENDING_UPDATE", "ERROR"}, wantWait: listenerReadyPollInterval, wantMutations: 1},
		{name: "delete accepts a Listener in UNKNOWN", method: http.MethodDelete, listenerStatuses: []string{"UNKNOWN"}, wantMutations: 1},
		{name: "delete of a gone Listener sends no request", method: http.MethodDelete, listenerStatuses: []string{gone}},
		{name: "create fails on a Load Balancer in ERROR", method: http.MethodPost, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusERROR}, wantErr: "the load balancer did not become idle before the creation"},
		{name: "update fails on a Load Balancer in UNKNOWN", method: http.MethodPatch, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN}, wantErr: "the load balancer did not become idle before the update"},
		{name: "update fails on a Listener in UNKNOWN", method: http.MethodPatch, listenerStatuses: []string{"UNKNOWN"}, wantErr: "the listener did not become idle before the update"},
		{name: "delete fails on a Load Balancer in ERROR", method: http.MethodDelete, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusERROR}, wantErr: "the load balancer did not become idle before the deletion"},
		{name: "create fails on a Load Balancer in UNKNOWN", method: http.MethodPost, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN}, wantErr: "terminal provisioning status UNKNOWN"},
		{name: "update fails on a Listener in ERROR", method: http.MethodPatch, listenerStatuses: []string{"ERROR"}, wantErr: "the listener did not become idle before the update"},
		{name: "delete fails on a Load Balancer in UNKNOWN", method: http.MethodDelete, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN}, wantErr: "terminal provisioning status UNKNOWN"},
		{name: "delete fails on a missing Load Balancer", method: http.MethodDelete, lbStatuses: []loadbalancersdk.LoadBalancingProvisioningStatus{gone}, wantErr: "the load balancer is missing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				lbStatuses := tt.lbStatuses
				if lbStatuses == nil {
					lbStatuses = []loadbalancersdk.LoadBalancingProvisioningStatus{active}
				}
				listenerStatuses := tt.listenerStatuses
				if listenerStatuses == nil {
					listenerStatuses = []string{"ACTIVE"}
				}
				var lbPolls, listenerPolls, mutations int
				var mutatedAt time.Time
				resourceUnderTest := newListenerTestResourceWithParent(t, func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodGet && req.URL.Path == listenerTestLoadBalancerPath:
						status := lbStatuses[min(lbPolls, len(lbStatuses)-1)]
						lbPolls++
						if status == gone {
							return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
						}
						return listenerJSONResponse(http.StatusOK, loadBalancerResponseJSON(status)), nil
					case req.Method == http.MethodGet && mutations > 0 && tt.method == http.MethodDelete:
						return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
					case req.Method == http.MethodGet && mutations > 0:
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
					case req.Method == http.MethodGet:
						status := listenerStatuses[min(listenerPolls, len(listenerStatuses)-1)]
						listenerPolls++
						if status == gone {
							return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
						}
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, status, "ONLINE")), nil
					case req.Method != tt.method:
						t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
						return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
					}
					mutations++
					mutatedAt = time.Now()
					switch tt.method {
					case http.MethodDelete:
						return listenerJSONResponse(http.StatusAccepted, ""), nil
					case http.MethodPost:
						return listenerJSONResponse(http.StatusCreated, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
					default:
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
					}
				})

				start := time.Now()
				diags := runListenerOperation(t, resourceUnderTest, tt.method)
				if mutations != tt.wantMutations {
					t.Fatalf("%s calls=%d, want %d (diagnostics=%v)", tt.method, mutations, tt.wantMutations, diags)
				}
				if tt.wantErr != "" {
					errs := diags.Errors()
					if len(errs) != 1 || !strings.Contains(errs[0].Detail(), tt.wantErr) {
						t.Fatalf("diagnostics=%v, want one error containing %q", errs, tt.wantErr)
					}
					return
				}
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				if mutations > 0 && mutatedAt.Sub(start) != tt.wantWait {
					t.Fatalf("%s sent after %s, want after the idle wait of %s", tt.method, mutatedAt.Sub(start), tt.wantWait)
				}
			})
		})
	}
}

// Every Listener operation claims the Load Balancer, so Listener operations
// on one Load Balancer in one provider process run one at a time, from the
// idle wait to the end of the wait after the request.
func TestListenerOperationsHoldTheClaimsLock(t *testing.T) {
	t.Parallel()

	for _, op := range listenerOperations {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				requests := 0
				deleted := false
				resourceUnderTest := newListenerTestResourceWithParent(t, func(req *http.Request) (*http.Response, error) {
					requests++
					switch {
					case req.Method == http.MethodGet && req.URL.Path == listenerTestLoadBalancerPath:
						return listenerJSONResponse(http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
					case req.Method == http.MethodGet && deleted:
						return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
					case req.Method == http.MethodDelete:
						deleted = true
						return listenerJSONResponse(http.StatusAccepted, ""), nil
					case req.Method == http.MethodPost:
						return listenerJSONResponse(http.StatusCreated, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
					default:
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
					}
				})
				unlock, err := resourceUnderTest.claims.Lock(context.Background(), listenerTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("hold the claims lock: %v", err)
				}
				done := make(chan diag.Diagnostics, 1)
				go func() { done <- runListenerOperation(t, resourceUnderTest, op.method) }()
				synctest.Wait()
				if requests != 0 {
					t.Fatalf("%s sent %d requests while another operation held the claims lock", op.name, requests)
				}
				unlock()
				if diags := <-done; diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				if requests == 0 {
					t.Fatalf("%s sent no request after the claims lock was released", op.name)
				}
			})
		})
	}
}

func TestListenerResourceImportState(t *testing.T) {
	t.Parallel()

	s := listenerSchema(t)
	resourceUnderTest := &ListenerResource{}

	t.Run("valid composite ID", func(t *testing.T) {
		t.Parallel()
		importID := fmt.Sprintf("%s/%s", listenerTestLoadBalancerID, listenerTestID)
		var resp resource.ImportStateResponse
		resp.State = tfsdk.State{Schema: s}
		initModel := defaultTestListenerModel()
		if diags := resp.State.Set(context.Background(), &initModel); diags.HasError() {
			t.Fatalf("initialize import state: %v", diags)
		}

		resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("import failed: %v", resp.Diagnostics)
		}

		var state ListenerResourceModel
		if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		if lbID := reference.Get(state.LoadBalancer, reference.ID); lbID.ValueString() != listenerTestLoadBalancerID.String() || state.ID.ValueString() != listenerTestID.String() {
			t.Fatalf("unexpected imported attributes: load_balancer.id=%s id=%s", lbID.ValueString(), state.ID.ValueString())
		}
	})

	for _, badID := range []string{
		"",
		"just-an-id",
		"lb/id/extra",
		"/id",
		"lb/",
		"not-a-uuid/not-a-uuid",
		fmt.Sprintf("%s/not-a-uuid", listenerTestLoadBalancerID),
		fmt.Sprintf("not-a-uuid/%s", listenerTestID),
	} {
		t.Run("invalid ID "+badID, func(t *testing.T) {
			t.Parallel()
			var resp resource.ImportStateResponse
			resp.State = tfsdk.State{Schema: s}
			resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: badID}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Errorf("expected import ID %q to be rejected", badID)
			}
		})
	}
}

func TestListenerWaitUntilReady(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls int
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_CREATE", "OFFLINE")), nil
			}
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ACTIVE", "ONLINE")), nil
		})

		start := time.Now()
		listener, err := resourceUnderTest.waitUntilReady(
			context.Background(),
			listenerTestLoadBalancerID,
			listenerTestID,
			listenerReadyTimeout,
			listenerReadyPollInterval,
		)
		if err != nil || calls != 2 {
			t.Fatalf("expected success after 2 polls, calls=%d err=%v", calls, err)
		}
		if waited := time.Since(start); waited != listenerReadyPollInterval {
			t.Fatalf("expected one poll interval wait, waited %s", waited)
		}
		if listener.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE {
			t.Fatalf("expected ACTIVE status, got %s", listener.ProvisioningStatus)
		}
	})
}

// A failed Delete of a Listener that started in ERROR or UNKNOWN reports the
// failure and keeps the state. After PENDING_DELETE, the failure shows at once.
// Without PENDING_DELETE, Delete cannot tell it from the state before the
// DELETE, so it waits until timeout.
func TestListenerResourceDeleteFromFailedStateKeepsState(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"ERROR", "UNKNOWN"} {
		for name, tt := range map[string]struct {
			afterDelete []string
			wantErr     string
			wantWait    time.Duration
			wantTimeout bool
		}{
			"fails at once after PENDING_DELETE": {
				afterDelete: []string{"PENDING_DELETE", status},
				wantErr:     "terminal provisioning status " + status + " after pending delete",
				wantWait:    listenerReadyPollInterval,
			},
			"waits until timeout without PENDING_DELETE": {
				afterDelete: []string{status},
				wantErr:     "provisioning_status=" + status,
				wantWait:    listenerReadyTimeout,
				wantTimeout: true,
			},
		} {
			t.Run(status+" "+name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					deleted := false
					polls := 0
					resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
						switch {
						case req.Method == http.MethodDelete:
							deleted = true
							return listenerJSONResponse(http.StatusAccepted, ""), nil
						case req.Method == http.MethodGet && !deleted:
							return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, status, "OFFLINE")), nil
						case req.Method == http.MethodGet:
							current := tt.afterDelete[min(polls, len(tt.afterDelete)-1)]
							polls++
							return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, current, "OFFLINE")), nil
						}
						t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
						return nil, nil
					})

					s := listenerSchema(t)
					state := tfsdk.State{Schema: s}
					model := defaultTestListenerModel()
					model.ID = types.StringValue(listenerTestID.String())
					model.LoadBalancer = listenerTestReference(listenerTestLoadBalancerID.String())
					if diags := state.Set(context.Background(), &model); diags.HasError() {
						t.Fatalf("set state: %v", diags)
					}

					start := time.Now()
					deleteResp := resource.DeleteResponse{State: state}
					resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResp)
					if !deleted {
						t.Fatal("expected the DELETE to be sent")
					}
					errs := deleteResp.Diagnostics.Errors()
					if len(errs) != 1 || errs[0].Summary() != "Error waiting for Load Balancer Listener deletion" || !strings.Contains(errs[0].Detail(), tt.wantErr) {
						t.Fatalf("expected a deletion wait error containing %q, got %v", tt.wantErr, deleteResp.Diagnostics)
					}
					if timedOut := strings.Contains(errs[0].Detail(), wait.ErrTimeout.Error()); timedOut != tt.wantTimeout {
						t.Fatalf("timeout = %t, want %t: %s", timedOut, tt.wantTimeout, errs[0].Detail())
					}
					if waited := time.Since(start); waited != tt.wantWait {
						t.Fatalf("waited %s, want %s", waited, tt.wantWait)
					}
					if !deleteResp.State.Raw.Equal(state.Raw) {
						t.Fatal("a failed delete must keep the state")
					}
				})
			})
		}
	}
}

func TestListenerWaitUntilDeleted(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls int
		resourceUnderTest := newListenerTestResource(t, func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_DELETE", "OFFLINE")), nil
			}
			return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
		})

		start := time.Now()
		err := resourceUnderTest.waitUntilDeleted(
			context.Background(),
			listenerTestLoadBalancerID,
			listenerTestID,
			listenerReadyTimeout,
			listenerReadyPollInterval,
		)
		if err != nil || calls != 2 {
			t.Fatalf("expected success after 2 polls, calls=%d err=%v", calls, err)
		}
		if waited := time.Since(start); waited != listenerReadyPollInterval {
			t.Fatalf("expected one poll interval wait, waited %s", waited)
		}
	})
}

func TestListenerWaitUntilReadyStopsOnFailure(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		respond func() *http.Response
		wantErr string
	}{
		"terminal ERROR": {
			respond: func() *http.Response {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "ERROR", "ERROR"))
			},
			wantErr: "terminal provisioning status ERROR",
		},
		"terminal UNKNOWN": {
			respond: func() *http.Response {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "UNKNOWN", "OFFLINE"))
			},
			wantErr: "terminal provisioning status UNKNOWN",
		},
		"unexpected DELETED": {
			respond: func() *http.Response {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "DELETED", "OFFLINE"))
			},
			wantErr: "terminal provisioning status DELETED",
		},
		"unrecognized status": {
			respond: func() *http.Response {
				return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "SURPRISE", "OFFLINE"))
			},
			wantErr: "unrecognized provisioning_status=SURPRISE",
		},
		"permanent SDK error": {
			respond: func() *http.Response {
				return listenerJSONResponse(http.StatusInternalServerError, listenerBackendError)
			},
			wantErr: "backend exploded",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls int
				resourceUnderTest := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
					calls++
					return tt.respond(), nil
				})

				_, err := resourceUnderTest.waitUntilReady(context.Background(), listenerTestLoadBalancerID, listenerTestID, listenerReadyTimeout, listenerReadyPollInterval)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, wait.ErrTimeout) || calls != 1 {
					t.Fatalf("expected an immediate failure containing %q, got calls=%d err=%v", tt.wantErr, calls, err)
				}
			})
		})
	}
}

func TestListenerWaitUntilReadyTimesOutAfterProductionTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		resourceUnderTest := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_UPDATE", "OFFLINE")), nil
		})

		start := time.Now()
		_, err := resourceUnderTest.waitUntilReady(context.Background(), listenerTestLoadBalancerID, listenerTestID, listenerReadyTimeout, listenerReadyPollInterval)
		if !errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), "provisioning_status=PENDING_UPDATE") {
			t.Fatalf("expected a timeout that names the last status, got %v", err)
		}
		if waited := time.Since(start); waited != listenerReadyTimeout {
			t.Fatalf("expected to wait %s, waited %s", listenerReadyTimeout, waited)
		}
	})
}

func TestListenerWaitUntilReadyReportsCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls int
		resourceUnderTest := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
			calls++
			if calls == 2 {
				cancel()
			}
			return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, "PENDING_CREATE", "OFFLINE")), nil
		})

		_, err := resourceUnderTest.waitUntilReady(ctx, listenerTestLoadBalancerID, listenerTestID, listenerReadyTimeout, listenerReadyPollInterval)
		if !errors.Is(err, context.Canceled) || errors.Is(err, wait.ErrTimeout) {
			t.Fatalf("expected a cancellation that is not a timeout, got %v", err)
		}
	})
}

func TestListenerWaitUntilDeletedClassifiesStatuses(t *testing.T) {
	t.Parallel()

	const (
		gone        = "NOT_FOUND"
		backendFail = "BACKEND_FAILURE"
	)
	tests := map[string]struct {
		statuses    []string
		wantCalls   int
		wantErr     string
		wantTimeout bool
	}{
		"DELETED tombstone completes the wait":       {statuses: []string{"DELETED"}, wantCalls: 1},
		"ERROR then removal completes the wait":      {statuses: []string{"ERROR", gone}, wantCalls: 2},
		"ERROR then ACTIVE then removal":             {statuses: []string{"ERROR", "ACTIVE", "ERROR", gone}, wantCalls: 4},
		"ERROR after PENDING_DELETE is terminal":     {statuses: []string{"PENDING_DELETE", "ERROR"}, wantCalls: 2, wantErr: "terminal provisioning status ERROR after pending delete"},
		"ERROR across polls keeps waiting":           {statuses: []string{"ERROR", "ERROR", "ERROR", gone}, wantCalls: 4},
		"ERROR until the deadline times out":         {statuses: []string{"ERROR"}, wantCalls: -1, wantErr: "provisioning_status=ERROR", wantTimeout: true},
		"UNKNOWN then removal completes the wait":    {statuses: []string{"UNKNOWN", gone}, wantCalls: 2},
		"UNKNOWN after PENDING_DELETE is terminal":   {statuses: []string{"PENDING_DELETE", "UNKNOWN"}, wantCalls: 2, wantErr: "terminal provisioning status UNKNOWN after pending delete"},
		"UNKNOWN across polls keeps waiting":         {statuses: []string{"UNKNOWN", "UNKNOWN", "UNKNOWN", gone}, wantCalls: 4},
		"UNKNOWN until the deadline times out":       {statuses: []string{"UNKNOWN"}, wantCalls: -1, wantErr: "provisioning_status=UNKNOWN", wantTimeout: true},
		"unrecognized status is terminal":            {statuses: []string{"SURPRISE"}, wantCalls: 1, wantErr: "unrecognized provisioning_status=SURPRISE"},
		"permanent SDK error is terminal":            {statuses: []string{backendFail}, wantCalls: 1, wantErr: "backend exploded"},
		"PENDING_UPDATE keeps waiting until removal": {statuses: []string{"PENDING_UPDATE", "ACTIVE", gone}, wantCalls: 3},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls int
				resourceUnderTest := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
					status := tt.statuses[min(calls, len(tt.statuses)-1)]
					calls++
					switch status {
					case gone:
						return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
					case backendFail:
						return listenerJSONResponse(http.StatusInternalServerError, listenerBackendError), nil
					default:
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, status, "OFFLINE")), nil
					}
				})

				err := resourceUnderTest.waitUntilDeleted(context.Background(), listenerTestLoadBalancerID, listenerTestID, listenerReadyTimeout, listenerReadyPollInterval)
				if tt.wantCalls >= 0 && calls != tt.wantCalls {
					t.Fatalf("expected %d polls, got %d (err=%v)", tt.wantCalls, calls, err)
				}
				if tt.wantErr == "" && err != nil {
					t.Fatalf("expected the wait to complete, got %v", err)
				}
				if tt.wantTimeout {
					if !errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("expected a timeout that names %q, got %v", tt.wantErr, err)
					}
					return
				}
				if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, wait.ErrTimeout)) {
					t.Fatalf("expected an immediate failure containing %q, got %v", tt.wantErr, err)
				}
			})
		})
	}
}

func TestListenerWaitUntilDeletableClassifiesStatuses(t *testing.T) {
	t.Parallel()

	const (
		gone        = "NOT_FOUND"
		backendFail = "BACKEND_FAILURE"
	)
	tests := map[string]struct {
		statuses  []string
		wantCalls int
		wantGone  bool
		wantErr   string
	}{
		"ACTIVE is idle":                     {statuses: []string{"ACTIVE"}, wantCalls: 1},
		"ERROR is idle":                      {statuses: []string{"ERROR"}, wantCalls: 1},
		"UNKNOWN is idle":                    {statuses: []string{"UNKNOWN"}, wantCalls: 1},
		"PENDING_UPDATE waits until idle":    {statuses: []string{"PENDING_UPDATE", "ACTIVE"}, wantCalls: 2},
		"PENDING_DELETE waits until removal": {statuses: []string{"PENDING_DELETE", gone}, wantCalls: 2, wantGone: true},
		"not found is gone":                  {statuses: []string{gone}, wantCalls: 1, wantGone: true},
		"DELETED tombstone is gone":          {statuses: []string{"DELETED"}, wantCalls: 1, wantGone: true},
		"unrecognized status is terminal":    {statuses: []string{"SURPRISE"}, wantCalls: 1, wantErr: "unrecognized provisioning_status=SURPRISE"},
		"permanent SDK error is terminal":    {statuses: []string{backendFail}, wantCalls: 1, wantErr: "backend exploded"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls int
				resourceUnderTest := newListenerTestResource(t, func(*http.Request) (*http.Response, error) {
					status := tt.statuses[min(calls, len(tt.statuses)-1)]
					calls++
					switch status {
					case gone:
						return listenerJSONResponse(http.StatusNotFound, `{"detail":"not found"}`), nil
					case backendFail:
						return listenerJSONResponse(http.StatusInternalServerError, listenerBackendError), nil
					default:
						return listenerJSONResponse(http.StatusOK, sampleListenerDetailJSON(listenerTestID, listenerTestLoadBalancerID, "web", "HTTP", 80, status, "OFFLINE")), nil
					}
				})

				start := time.Now()
				isGone, err := resourceUnderTest.waitUntilDeletable(context.Background(), listenerTestLoadBalancerID, listenerTestID, listenerReadyTimeout, listenerReadyPollInterval)
				if calls != tt.wantCalls {
					t.Fatalf("expected %d polls, got %d (err=%v)", tt.wantCalls, calls, err)
				}
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) || errors.Is(err, wait.ErrTimeout) {
						t.Fatalf("expected an immediate failure containing %q, got %v", tt.wantErr, err)
					}
					return
				}
				if err != nil || isGone != tt.wantGone {
					t.Fatalf("gone=%t err=%v, want gone=%t", isGone, err, tt.wantGone)
				}
				if waited, want := time.Since(start), time.Duration(tt.wantCalls-1)*listenerReadyPollInterval; waited != want {
					t.Fatalf("waited %s, want %s", waited, want)
				}
			})
		})
	}
}

func TestBuildListenerCreateBody(t *testing.T) {
	t.Parallel()

	plan := ListenerResourceModel{
		ListenerModel: ListenerModel{
			Name:                    types.StringValue("  my-listener  "),
			Description:             types.StringValue("HTTP web listener"),
			Protocol:                types.StringValue("HTTP"),
			ProtocolPort:            types.Int64Value(80),
			AllowedCIDRs:            types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/24")}),
			ListenerTimeout:         types.Int64Value(60000),
			ServerTimeout:           types.Int64Value(60000),
			ServerConnectingTimeout: types.Int64Value(10000),
			EnableH2:                types.BoolValue(true),
			SetXForwardedFor:        types.BoolValue(true),
			SetXForwardedPort:       types.BoolValue(true),
			SetXForwardedProto:      types.BoolValue(true),
		},
		LoadBalancer:   listenerTestReference(listenerTestLoadBalancerID.String()),
		ServerGroup:    listenerTestReference(listenerTestServerGroupID.String()),
		DefaultTLSCert: listenerTestReference(listenerTestTLSCertID.String()),
		SNITLSCerts:    listenerTestReferenceSet(listenerTestSNICertID.String()),
	}

	body, diags := buildListenerCreateBody(plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if body.Name != "my-listener" || body.Protocol != loadbalancersdk.ListenerProtocolTypeHTTP || body.ProtocolPort != 80 {
		t.Fatalf("unexpected required fields: name=%q protocol=%s port=%d", body.Name, body.Protocol, body.ProtocolPort)
	}
	if body.Description == nil || *body.Description != "HTTP web listener" {
		t.Fatalf("unexpected description: %#v", body.Description)
	}
	if !listenerNullableHolds(body.ServerGroupId, listenerTestServerGroupID) {
		t.Fatalf("unexpected server group ID: %#v", body.ServerGroupId)
	}
	if !listenerNullableHolds(body.DefaultTlsCertId, listenerTestTLSCertID) {
		t.Fatalf("unexpected default TLS cert ID: %#v", body.DefaultTlsCertId)
	}
	if body.SniTlsCertIds == nil || len(*body.SniTlsCertIds) != 1 || (*body.SniTlsCertIds)[0] != listenerTestSNICertID {
		t.Fatalf("unexpected SNI cert IDs: %#v", body.SniTlsCertIds)
	}
	if body.AllowedCidrs == nil || len(*body.AllowedCidrs) != 1 || (*body.AllowedCidrs)[0] != "10.0.0.0/24" {
		t.Fatalf("unexpected allowed CIDRs: %#v", body.AllowedCidrs)
	}
	if !listenerNullableHolds(body.ListenerTimeout, 60000) ||
		!listenerNullableHolds(body.ServerTimeout, 60000) ||
		!listenerNullableHolds(body.ServerConnectingTimeout, 10000) {
		t.Fatalf("unexpected timeouts: %#v", body)
	}
	if !listenerNullableHolds(body.EnableH2, true) ||
		!listenerNullableHolds(body.SetXForwardedFor, true) ||
		!listenerNullableHolds(body.SetXForwardedPort, true) ||
		!listenerNullableHolds(body.SetXForwardedProto, true) {
		t.Fatalf("unexpected bool options: %#v", body)
	}
}

func TestBuildListenerCreateBodyOmittedFields(t *testing.T) {
	t.Parallel()

	plan := ListenerResourceModel{
		ListenerModel: ListenerModel{
			Name:                    types.StringValue("minimal-listener"),
			Protocol:                types.StringValue("TCP"),
			ProtocolPort:            types.Int64Value(443),
			Description:             types.StringNull(),
			AllowedCIDRs:            types.SetNull(types.StringType),
			ListenerTimeout:         types.Int64Null(),
			ServerTimeout:           types.Int64Null(),
			ServerConnectingTimeout: types.Int64Null(),
			EnableH2:                types.BoolNull(),
			SetXForwardedFor:        types.BoolNull(),
			SetXForwardedPort:       types.BoolNull(),
			SetXForwardedProto:      types.BoolNull(),
		},
		LoadBalancer:   listenerTestReference(listenerTestLoadBalancerID.String()),
		ServerGroup:    types.ObjectNull(reference.AttributeTypes(reference.ID)),
		DefaultTLSCert: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		SNITLSCerts:    types.SetNull(reference.ObjectType(reference.ID)),
	}

	body, diags := buildListenerCreateBody(plan)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if body.Description != nil || body.DefaultTlsCertId.IsSpecified() ||
		body.SniTlsCertIds != nil || body.AllowedCidrs != nil ||
		body.EnableH2.IsSpecified() || body.SetXForwardedFor.IsSpecified() || body.SetXForwardedPort.IsSpecified() || body.SetXForwardedProto.IsSpecified() {
		t.Fatalf("expected omitted fields to remain unset, got body=%#v", body)
	}
	if !body.ServerGroupId.IsNull() {
		t.Fatalf("expected an omitted server group to be an explicit null, got %#v", body.ServerGroupId)
	}
	if !listenerNullableHolds(body.ListenerTimeout, defaultListenerTimeout) ||
		!listenerNullableHolds(body.ServerTimeout, listenerDefaultServerTimeout) ||
		!listenerNullableHolds(body.ServerConnectingTimeout, listenerDefaultServerConnectingTimeout) {
		t.Fatalf("expected omitted timeouts to use the create defaults, got %#v, %#v, %#v", body.ListenerTimeout, body.ServerTimeout, body.ServerConnectingTimeout)
	}
}

// The backend requires every timeout key on create, so the serialized
// request must carry the defaults for omitted timeouts.
func TestListenerCreateSerializesDefaultTimeouts(t *testing.T) {
	t.Parallel()

	body, diags := buildListenerCreateBody(ListenerResourceModel{
		ListenerModel: ListenerModel{
			Name:                    types.StringValue("minimal-listener"),
			Protocol:                types.StringValue("TCP"),
			ProtocolPort:            types.Int64Value(443),
			ListenerTimeout:         types.Int64Unknown(),
			ServerTimeout:           types.Int64Null(),
			ServerConnectingTimeout: types.Int64Value(7000),
		},
		LoadBalancer:   listenerTestReference(listenerTestLoadBalancerID.String()),
		ServerGroup:    types.ObjectNull(reference.AttributeTypes(reference.ID)),
		DefaultTLSCert: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		SNITLSCerts:    types.SetNull(reference.ObjectType(reference.ID)),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal create body: %v", err)
	}
	want := map[string]float64{
		"listener_timeout":          defaultListenerTimeout,
		"server_timeout":            listenerDefaultServerTimeout,
		"server_connecting_timeout": 7000,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("expected %s %v in create body, got %#v", key, value, got[key])
		}
	}
}

func TestBuildListenerCreateBodyRejectsMissingRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan ListenerResourceModel
		want string
	}{
		{
			name: "missing name",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringNull(),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Value(80),
				},
			},
			want: "Missing listener name",
		},
		{
			name: "blank name",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("   "),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Value(80),
				},
			},
			want: "Missing listener name",
		},
		{
			name: "missing protocol",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("my-listener"),
					Protocol:     types.StringNull(),
					ProtocolPort: types.Int64Value(80),
				},
			},
			want: "Missing listener protocol",
		},
		{
			name: "missing protocol_port",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("my-listener"),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Null(),
				},
			},
			want: "Missing listener protocol_port",
		},
		{
			name: "invalid server_group_id UUID",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("my-listener"),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Value(80),
				},
				ServerGroup: listenerTestReference("not-a-uuid"),
			},
			want: "Invalid UUID",
		},
		{
			name: "invalid default_tls_cert_id UUID",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("my-listener"),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Value(80),
				},
				DefaultTLSCert: listenerTestReference("not-a-uuid"),
			},
			want: "Invalid UUID",
		},
		{
			name: "invalid sni_tls_cert_ids element",
			plan: ListenerResourceModel{
				ListenerModel: ListenerModel{
					Name:         types.StringValue("my-listener"),
					Protocol:     types.StringValue("HTTP"),
					ProtocolPort: types.Int64Value(80),
				},
				SNITLSCerts: listenerTestReferenceSet("bad-uuid"),
			},
			want: "Invalid UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, diags := buildListenerCreateBody(tt.plan)
			if !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), tt.want) {
				t.Fatalf("expected error containing %q, got: %v", tt.want, diags)
			}
		})
	}
}

func TestListenerCreateSDKNullableServerGroupSerialization(t *testing.T) {
	t.Parallel()
	plan := defaultTestListenerModel()
	plan.Name = types.StringValue("listener")
	plan.Protocol = types.StringValue("HTTP")
	plan.ProtocolPort = types.Int64Value(80)
	body, diags := buildListenerCreateBody(plan)
	encoded, err := json.Marshal(body)
	var fields map[string]any
	if diags.HasError() || err != nil || json.Unmarshal(encoded, &fields) != nil {
		t.Fatalf("marshal create body: %s diagnostics=%v error=%v", encoded, diags, err)
	}
	value, present := fields["server_group_id"]
	if !present || value != nil {
		t.Fatalf("the SDK requires an absent server group to serialize as null: %s", encoded)
	}
	for _, name := range []string{"description", "default_tls_cert_id", "sni_tls_cert_ids", "allowed_cidrs"} {
		if _, present := fields[name]; present {
			t.Errorf("omitted %s must remain absent: %s", name, encoded)
		}
	}
	if v, ok := fields["enable_h2"].(bool); !ok || v != false {
		t.Errorf("omitted enable_h2 for HTTP must serialize as false: %s", encoded)
	}
}

func TestBuildListenerCreateBodyProtocolSpecificDefaults(t *testing.T) {
	t.Parallel()

	t.Run("HTTP defaults enable_h2 to false", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("http-listener")
		plan.Protocol = types.StringValue("HTTP")
		plan.ProtocolPort = types.Int64Value(80)
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !listenerNullableHolds(body.EnableH2, false) {
			t.Errorf("expected enable_h2 to default to false for HTTP, got: %v", body.EnableH2)
		}
		if body.SniTlsCertIds != nil {
			t.Errorf("expected sni_tls_cert_ids to be nil for HTTP, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("HTTPS defaults enable_h2 to true", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("https-listener")
		plan.Protocol = types.StringValue("HTTPS")
		plan.ProtocolPort = types.Int64Value(443)
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !listenerNullableHolds(body.EnableH2, true) {
			t.Errorf("expected enable_h2 to default to true for HTTPS, got: %v", body.EnableH2)
		}
		if body.SniTlsCertIds != nil {
			t.Errorf("expected sni_tls_cert_ids to be nil for HTTPS, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("TERMINATED_HTTPS defaults enable_h2 to true and sni_tls_certs to empty slice", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("tls-listener")
		plan.Protocol = types.StringValue("TERMINATED_HTTPS")
		plan.ProtocolPort = types.Int64Value(443)
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !listenerNullableHolds(body.EnableH2, true) {
			t.Errorf("expected enable_h2 to default to true for TERMINATED_HTTPS, got: %v", body.EnableH2)
		}
		if body.SniTlsCertIds == nil || len(*body.SniTlsCertIds) != 0 {
			t.Errorf("expected sni_tls_cert_ids to default to empty slice for TERMINATED_HTTPS, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("TERMINATED_HTTPS with unknown sni_tls_certs in a create plan sends empty slice", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("tls-listener")
		plan.Protocol = types.StringValue("TERMINATED_HTTPS")
		plan.ProtocolPort = types.Int64Value(443)
		plan.SNITLSCerts = types.SetUnknown(reference.ObjectType(reference.ID))
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if body.SniTlsCertIds == nil || len(*body.SniTlsCertIds) != 0 {
			t.Errorf("expected sni_tls_cert_ids to default to empty slice for unknown TERMINATED_HTTPS plan, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("HTTP with unknown sni_tls_certs keeps sni_tls_cert_ids absent", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("http-listener")
		plan.Protocol = types.StringValue("HTTP")
		plan.ProtocolPort = types.Int64Value(80)
		plan.SNITLSCerts = types.SetUnknown(reference.ObjectType(reference.ID))
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if body.SniTlsCertIds != nil {
			t.Errorf("expected sni_tls_cert_ids to remain nil for HTTP, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("TCP keeps enable_h2 and sni_tls_certs absent", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("tcp-listener")
		plan.Protocol = types.StringValue("TCP")
		plan.ProtocolPort = types.Int64Value(9000)
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if body.EnableH2 != nil {
			t.Errorf("expected enable_h2 to remain nil for TCP, got: %v", body.EnableH2)
		}
		if body.SniTlsCertIds != nil {
			t.Errorf("expected sni_tls_cert_ids to remain nil for TCP, got: %v", body.SniTlsCertIds)
		}
	})

	t.Run("HTTP preserves configured enable_h2 true", func(t *testing.T) {
		t.Parallel()
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("http-listener")
		plan.Protocol = types.StringValue("HTTP")
		plan.ProtocolPort = types.Int64Value(80)
		plan.EnableH2 = types.BoolValue(true)
		body, diags := buildListenerCreateBody(plan)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !listenerNullableHolds(body.EnableH2, true) {
			t.Errorf("expected enable_h2 to be true when configured, got: %v", body.EnableH2)
		}
	})
}

func TestBuildListenerPartialUpdateBody(t *testing.T) {
	t.Parallel()

	t.Run("detects modified fields and builds patch schema", func(t *testing.T) {
		t.Parallel()
		state := ListenerResourceModel{
			ListenerModel: ListenerModel{
				Name:                    types.StringValue("old-name"),
				Description:             types.StringValue("old desc"),
				AllowedCIDRs:            types.SetNull(types.StringType),
				ListenerTimeout:         types.Int64Value(50000),
				ServerTimeout:           types.Int64Value(50000),
				ServerConnectingTimeout: types.Int64Value(5000),
				EnableH2:                types.BoolValue(true),
				SetXForwardedFor:        types.BoolValue(false),
				SetXForwardedPort:       types.BoolValue(false),
				SetXForwardedProto:      types.BoolValue(false),
			},
			ServerGroup:    types.ObjectNull(reference.AttributeTypes(reference.ID)),
			DefaultTLSCert: types.ObjectNull(reference.AttributeTypes(reference.ID)),
			SNITLSCerts:    types.SetNull(reference.ObjectType(reference.ID)),
		}

		plan := ListenerResourceModel{
			ListenerModel: ListenerModel{
				Name:                    types.StringValue("  updated-listener  "),
				Description:             types.StringValue("updated desc"),
				AllowedCIDRs:            types.SetValueMust(types.StringType, []attr.Value{types.StringValue("172.16.0.0/16")}),
				ListenerTimeout:         types.Int64Value(30000),
				ServerTimeout:           types.Int64Value(40000),
				ServerConnectingTimeout: types.Int64Value(3000),
				EnableH2:                types.BoolValue(false),
				SetXForwardedFor:        types.BoolValue(true),
				SetXForwardedPort:       types.BoolValue(true),
				SetXForwardedProto:      types.BoolValue(true),
			},
			ServerGroup:    listenerTestReference(listenerTestServerGroupID.String()),
			DefaultTLSCert: listenerTestReference(listenerTestTLSCertID.String()),
			SNITLSCerts:    listenerTestReferenceSet(listenerTestSNICertID.String()),
		}

		body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !hasChanges {
			t.Fatal("expected hasChanges to be true")
		}

		if body.Name == nil || *body.Name != "updated-listener" {
			t.Fatalf("unexpected name: %#v", body.Name)
		}
		if !listenerNullableHolds(body.Description, "updated desc") {
			t.Fatalf("unexpected description: %#v", body.Description)
		}
		if !listenerNullableHolds(body.ServerGroupId, listenerTestServerGroupID) {
			t.Fatalf("unexpected server group ID: %#v", body.ServerGroupId)
		}
		if !listenerNullableHolds(body.DefaultTlsCertId, listenerTestTLSCertID) {
			t.Fatalf("unexpected default TLS cert ID: %#v", body.DefaultTlsCertId)
		}
		if body.SniTlsCertIds == nil || len(*body.SniTlsCertIds) != 1 || (*body.SniTlsCertIds)[0] != listenerTestSNICertID {
			t.Fatalf("unexpected SNI cert IDs: %#v", body.SniTlsCertIds)
		}
		if body.AllowedCidrs == nil || len(*body.AllowedCidrs) != 1 || (*body.AllowedCidrs)[0] != "172.16.0.0/16" {
			t.Fatalf("unexpected allowed CIDRs: %#v", body.AllowedCidrs)
		}
		if body.ListenerTimeout == nil || *body.ListenerTimeout != 30000 {
			t.Fatalf("unexpected listener timeout: %#v", body.ListenerTimeout)
		}
		if body.ServerTimeout == nil || *body.ServerTimeout != 40000 {
			t.Fatalf("unexpected server timeout: %#v", body.ServerTimeout)
		}
		if body.ServerConnectingTimeout == nil || *body.ServerConnectingTimeout != 3000 {
			t.Fatalf("unexpected server connecting timeout: %#v", body.ServerConnectingTimeout)
		}
		if !listenerNullableHolds(body.EnableH2, false) {
			t.Fatalf("unexpected enable_h2: %#v", body.EnableH2)
		}
		if !listenerNullableHolds(body.SetXForwardedFor, true) {
			t.Fatalf("unexpected set_x_forwarded_for: %#v", body.SetXForwardedFor)
		}
	})

	t.Run("no changes returns false and empty body", func(t *testing.T) {
		t.Parallel()
		model := ListenerResourceModel{
			ListenerModel: ListenerModel{
				Name:                    types.StringValue("same-name"),
				Description:             types.StringValue("same desc"),
				AllowedCIDRs:            types.SetNull(types.StringType),
				ListenerTimeout:         types.Int64Value(50000),
				ServerTimeout:           types.Int64Value(50000),
				ServerConnectingTimeout: types.Int64Value(5000),
				EnableH2:                types.BoolValue(true),
				SetXForwardedFor:        types.BoolValue(true),
				SetXForwardedPort:       types.BoolValue(true),
				SetXForwardedProto:      types.BoolValue(true),
			},
			ServerGroup:    listenerTestReference(listenerTestServerGroupID.String()),
			DefaultTLSCert: types.ObjectNull(reference.AttributeTypes(reference.ID)),
			SNITLSCerts:    types.SetNull(reference.ObjectType(reference.ID)),
		}

		body, hasChanges, diags := buildListenerPartialUpdateBody(model, model)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if hasChanges {
			t.Fatalf("expected hasChanges to be false, got true (body: %#v)", body)
		}
	})

	t.Run("clearing SNI certs and allowed CIDRs sends empty slices", func(t *testing.T) {
		t.Parallel()
		state := ListenerResourceModel{
			ListenerModel: ListenerModel{
				Name:                    types.StringValue("listener"),
				AllowedCIDRs:            types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/8")}),
				ListenerTimeout:         types.Int64Value(50000),
				ServerTimeout:           types.Int64Value(50000),
				ServerConnectingTimeout: types.Int64Value(5000),
			},
			SNITLSCerts: listenerTestReferenceSet(listenerTestSNICertID.String()),
		}
		plan := ListenerResourceModel{
			ListenerModel: ListenerModel{
				Name:                    types.StringValue("listener"),
				AllowedCIDRs:            types.SetValueMust(types.StringType, []attr.Value{}),
				ListenerTimeout:         types.Int64Value(50000),
				ServerTimeout:           types.Int64Value(50000),
				ServerConnectingTimeout: types.Int64Value(5000),
			},
			SNITLSCerts: types.SetValueMust(reference.ObjectType(reference.ID), []attr.Value{}),
		}

		body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !hasChanges {
			t.Fatal("expected hasChanges to be true")
		}
		if body.SniTlsCertIds == nil || len(*body.SniTlsCertIds) != 0 {
			t.Fatalf("expected empty slice for SniTlsCertIds, got %#v", body.SniTlsCertIds)
		}
		if body.AllowedCidrs == nil || len(*body.AllowedCidrs) != 0 {
			t.Fatalf("expected empty slice for AllowedCidrs, got %#v", body.AllowedCidrs)
		}
	})

	t.Run("removing allowed CIDRs from configuration sends an empty slice", func(t *testing.T) {
		t.Parallel()
		state := defaultTestListenerModel()
		state.Name = types.StringValue("listener")
		state.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10.0.0.0/8")})
		plan := state
		plan.AllowedCIDRs = types.SetNull(types.StringType)

		body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
		encoded, err := json.Marshal(body)
		if diags.HasError() || err != nil || !hasChanges || string(encoded) != `{"allowed_cidrs":[]}` {
			t.Fatalf("removal must clear the set with an empty slice: body=%s changed=%t diagnostics=%v error=%v", encoded, hasChanges, diags, err)
		}
	})

	t.Run("omitting allowed CIDRs that the state does not hold sends nothing", func(t *testing.T) {
		t.Parallel()
		for name, prior := range map[string]types.Set{
			"null":  types.SetNull(types.StringType),
			"empty": types.SetValueMust(types.StringType, []attr.Value{}),
		} {
			state := defaultTestListenerModel()
			state.Name = types.StringValue("listener")
			state.AllowedCIDRs = prior
			plan := state
			plan.AllowedCIDRs = types.SetNull(types.StringType)

			body, hasChanges, diags := buildListenerPartialUpdateBody(plan, state)
			if diags.HasError() || hasChanges || body.AllowedCidrs != nil {
				t.Errorf("%s state: omission must not send a PATCH: body=%#v changed=%t diagnostics=%v", name, body, hasChanges, diags)
			}
		}
	})
}

func TestBuildListenerPartialUpdateReferenceDetach(t *testing.T) {
	t.Parallel()

	nullReference := types.ObjectNull(reference.AttributeTypes(reference.ID))
	attached := listenerTestReference(listenerTestServerGroupID.String())
	other := listenerTestReference(listenerTestTLSCertID.String())
	tests := map[string]struct {
		prior, planned types.Object
		want           string
	}{
		"removed from configuration": {prior: attached, planned: nullReference, want: "null"},
		"absent in plan and state":   {prior: nullReference, planned: nullReference, want: ""},
		"unchanged":                  {prior: attached, planned: attached, want: ""},
		"changed":                    {prior: attached, planned: other, want: `"` + listenerTestTLSCertID.String() + `"`},
		"attached":                   {prior: nullReference, planned: other, want: `"` + listenerTestTLSCertID.String() + `"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := defaultTestListenerModel()
			state.Name = types.StringValue("listener")
			state.ServerGroup, state.DefaultTLSCert = tt.prior, tt.prior
			plan := state
			plan.ServerGroup, plan.DefaultTLSCert = tt.planned, tt.planned

			body, changed, diags := buildListenerPartialUpdateBody(plan, state)
			encoded, err := json.Marshal(body)
			var fields map[string]json.RawMessage
			if diags.HasError() || err != nil || json.Unmarshal(encoded, &fields) != nil {
				t.Fatalf("build PATCH body: %s diagnostics=%v error=%v", encoded, diags, err)
			}
			if changed != (tt.want != "") {
				t.Errorf("changed = %t, want %t: %s", changed, tt.want != "", encoded)
			}
			for _, field := range []string{"server_group_id", "default_tls_cert_id"} {
				if got := string(fields[field]); got != tt.want {
					t.Errorf("%s = %q, want %q (empty means absent): %s", field, got, tt.want, encoded)
				}
			}
		})
	}
}

// The references are Optional without Computed, so a null plan detaches them
// and only an unknown reference is absent from the PATCH here.
func TestBuildListenerPartialUpdateOmitsAbsentCertificatesAndBooleans(t *testing.T) {
	t.Parallel()

	state := defaultTestListenerModel()
	state.Name = types.StringValue("listener")
	state.ServerGroup = listenerTestReference(listenerTestServerGroupID.String())
	state.DefaultTLSCert = listenerTestReference(listenerTestTLSCertID.String())
	state.SNITLSCerts = listenerTestReferenceSet(listenerTestSNICertID.String())
	state.EnableH2 = types.BoolValue(true)
	state.SetXForwardedFor = types.BoolValue(true)
	state.SetXForwardedPort = types.BoolValue(true)
	state.SetXForwardedProto = types.BoolValue(true)
	for _, unknown := range []bool{false, true} {
		plan := state
		plan.SNITLSCerts = types.SetNull(reference.ObjectType(reference.ID))
		plan.EnableH2, plan.SetXForwardedFor = types.BoolNull(), types.BoolNull()
		plan.SetXForwardedPort, plan.SetXForwardedProto = types.BoolNull(), types.BoolNull()
		if unknown {
			plan.ServerGroup = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
			plan.DefaultTLSCert = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
			plan.SNITLSCerts = types.SetUnknown(reference.ObjectType(reference.ID))
			plan.EnableH2, plan.SetXForwardedFor = types.BoolUnknown(), types.BoolUnknown()
			plan.SetXForwardedPort, plan.SetXForwardedProto = types.BoolUnknown(), types.BoolUnknown()
		}
		body, changed, diags := buildListenerPartialUpdateBody(plan, state)
		encoded, err := json.Marshal(body)
		if diags.HasError() || err != nil || changed || string(encoded) != "{}" {
			t.Errorf("absent values must not cause a PATCH: unknown=%t body=%s changed=%t diagnostics=%v error=%v", unknown, encoded, changed, diags, err)
		}
	}
}

func TestBuildListenerPartialUpdateUnknownSetsDoNotClear(t *testing.T) {
	t.Parallel()
	state := defaultTestListenerModel()
	state.Name = types.StringValue("listener")
	state.SNITLSCerts = listenerTestReferenceSet(listenerTestSNICertID.String())
	state.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("192.0.2.0/24")})
	plan := state
	plan.SNITLSCerts = types.SetUnknown(reference.ObjectType(reference.ID))
	plan.AllowedCIDRs = types.SetUnknown(types.StringType)
	body, changed, diags := buildListenerPartialUpdateBody(plan, state)
	if diags.HasError() || changed || body.SniTlsCertIds != nil || body.AllowedCidrs != nil {
		t.Fatalf("unknown sets must not clear prior values: %#v changed=%t diagnostics=%v", body, changed, diags)
	}
}

func TestListenerUnknownSetElementsDoNotSendPartialCollections(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"HTTP", "TERMINATED_HTTPS"} {
		state := defaultTestListenerModel()
		state.Name = types.StringValue("listener")
		state.Protocol = types.StringValue(protocol)
		state.ProtocolPort = types.Int64Value(80)
		state.SNITLSCerts = listenerTestReferenceSet(listenerTestSNICertID.String())
		state.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("192.0.2.0/24")})
		plan := state
		plan.SNITLSCerts = types.SetValueMust(reference.ObjectType(reference.ID), []attr.Value{
			listenerTestReference(listenerTestTLSCertID.String()),
			types.ObjectValueMust(reference.AttributeTypes(reference.ID), map[string]attr.Value{reference.ID: types.StringUnknown()}),
		})
		plan.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("198.51.100.0/24"), types.StringUnknown(),
		})
		create, createDiags := buildListenerCreateBody(plan)
		update, changed, updateDiags := buildListenerPartialUpdateBody(plan, state)
		if createDiags.HasError() || updateDiags.HasError() || changed ||
			create.SniTlsCertIds != nil || create.AllowedCidrs != nil || update.SniTlsCertIds != nil || update.AllowedCidrs != nil {
			t.Fatalf("partially unknown sets must remain absent: create=%#v update=%#v changed=%t diagnostics=%v/%v", create, update, changed, createDiags, updateDiags)
		}
	}
}

func TestListenerEmptyReferenceDoesNotMeanOmission(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"server_group", "default_tls_cert"} {
		plan := defaultTestListenerModel()
		plan.Name = types.StringValue("listener")
		plan.Protocol = types.StringValue("HTTP")
		plan.ProtocolPort = types.Int64Value(80)
		state := plan
		if field == "server_group" {
			plan.ServerGroup = listenerTestReference("")
		} else {
			plan.DefaultTLSCert = listenerTestReference("")
		}
		_, createDiags := buildListenerCreateBody(plan)
		_, _, updateDiags := buildListenerPartialUpdateBody(plan, state)
		if !createDiags.HasError() || !updateDiags.HasError() {
			t.Errorf("an empty %s must fail UUID parsing: create=%v update=%v", field, createDiags, updateDiags)
		}
	}
}

// listenerNullableHolds reports whether n holds value, rather than null or unset.
func listenerNullableHolds[T comparable](n core.Nullable[T], value T) bool {
	got, err := n.Get()
	return err == nil && got == value
}
