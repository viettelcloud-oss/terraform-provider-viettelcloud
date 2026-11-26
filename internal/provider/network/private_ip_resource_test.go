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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	privateIPTestID        = core.UUID{0x11}
	privateIPTestSubnetID  = core.UUID{0x22}
	privateIPTestVPCID     = core.UUID{0x33}
	privateIPTestProjectID = core.UUID{0x44}
	privateIPTestServerID  = core.UUID{0x55}
	privateIPTestVIPID     = core.UUID{0x66}
)

func TestPrivateIPModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	plan := tfsdk.Plan{Schema: privateIPSchema(t)}
	model := nullPrivateIPResourceModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestPrivateIPResourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceValue := NewPrivateIPResource()
	if resourceValue == nil {
		t.Fatal("expected non-nil resource")
	}
	var metadataResponse resource.MetadataResponse
	resourceValue.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadataResponse)
	if metadataResponse.TypeName != "viettelcloud_private_ip" {
		t.Fatalf("expected viettelcloud_private_ip metadata, got %q", metadataResponse.TypeName)
	}

	r := &PrivateIPResource{}
	var nilResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResponse.Diagnostics)
	}

	var invalidResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	var validResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &providerdata.Configured{
		Network:   client,
		ProjectID: privateIPTestProjectID,
	}}, &validResponse)
	if validResponse.Diagnostics.HasError() || r.client != client || r.projectID != privateIPTestProjectID {
		t.Fatalf("unexpected configured resource: client=%v projectID=%v diagnostics=%v", r.client, r.projectID, validResponse.Diagnostics)
	}
}

func TestPrivateIPReplacementAttributes(t *testing.T) {
	t.Parallel()

	resourceSchema := privateIPSchema(t)
	tests := map[string]struct {
		attribute     string
		state, config types.String
		want          bool
	}{
		"ip_address/same":            {attribute: "ip_address", state: types.StringValue("10.0.1.10"), config: types.StringValue("10.0.1.10")},
		"ip_address/padded":          {attribute: "ip_address", state: types.StringValue("10.0.1.10"), config: types.StringValue(" 10.0.1.10 ")},
		"ip_address/another":         {attribute: "ip_address", state: types.StringValue("10.0.1.10"), config: types.StringValue("10.0.1.11"), want: true},
		"ip_address/omitted":         {attribute: "ip_address", state: types.StringValue("10.0.1.10"), config: types.StringNull()},
		"mac_address/same":           {attribute: "mac_address", state: types.StringValue("aa:bb:cc:dd:ee:ff"), config: types.StringValue("aa:bb:cc:dd:ee:ff")},
		"mac_address/padded":         {attribute: "mac_address", state: types.StringValue("aa:bb:cc:dd:ee:ff"), config: types.StringValue(" AA:BB:CC:DD:EE:FF ")},
		"mac_address/another":        {attribute: "mac_address", state: types.StringValue("aa:bb:cc:dd:ee:ff"), config: types.StringValue("aa:bb:cc:dd:ee:00"), want: true},
		"mac_address/omitted":        {attribute: "mac_address", state: types.StringValue("aa:bb:cc:dd:ee:ff"), config: types.StringNull()},
		"ip_address/unparsable":      {attribute: "ip_address", state: types.StringValue("before"), config: types.StringValue("after"), want: true},
		"mac_address/unknown config": {attribute: "mac_address", state: types.StringValue("aa:bb:cc:dd:ee:ff"), config: types.StringUnknown(), want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// An omitted Optional and Computed value is planned as unknown.
			plan := test.config
			if plan.IsNull() {
				plan = types.StringUnknown()
			}
			if got := stringRequiresReplace(t, resourceSchema, test.attribute, test.state, plan, test.config); got != test.want {
				t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPrivateIPSubnetReferenceSchemaAndReplacement(t *testing.T) {
	t.Parallel()

	resourceSchema := privateIPSchema(t)
	subnet := requiredReferenceAttribute(t, resourceSchema, "subnet", reference.ID)
	before := reference.Value(reference.ID, types.StringValue(privateIPTestSubnetID.String()))
	tests := map[string]struct {
		plan types.Object
		want bool
	}{
		"same id updates in place":   {plan: before},
		"padded id updates in place": {plan: reference.Value(reference.ID, types.StringValue("  "+privateIPTestSubnetID.String()+" "))},
		"another id replaces":        {plan: reference.Value(reference.ID, types.StringValue(core.UUID{0x99}.String())), want: true},
		"unknown id replaces":        {plan: reference.Value(reference.ID, types.StringUnknown()), want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := referenceRequiresReplace(t, resourceSchema, subnet, before, test.plan); got != test.want {
				t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPrivateIPAllowedVIPsSchema(t *testing.T) {
	t.Parallel()

	attribute, ok := privateIPSchema(t).Attributes["allowed_vips"].(resourceschema.SetNestedAttribute)
	if !ok {
		t.Fatal("allowed_vips is not a set nested attribute")
	}
	if !attribute.Optional || !attribute.Computed || attribute.Required || len(attribute.PlanModifiers) != 0 {
		t.Fatalf("allowed_vips must be optional and computed without plan modifiers, got %#v", attribute)
	}
	id, ok := attribute.NestedObject.Attributes[reference.ID].(resourceschema.StringAttribute)
	if len(attribute.NestedObject.Attributes) != 1 || !ok || !id.Optional || !id.Computed || len(id.Validators) != 1 {
		t.Fatalf("allowed_vips elements = %#v, want one optional, computed, validated id", attribute.NestedObject.Attributes)
	}
}

func TestPrivateIPAllowedVIPsRejectsDuplicates(t *testing.T) {
	t.Parallel()

	attribute, ok := privateIPSchema(t).Attributes["allowed_vips"].(resourceschema.SetNestedAttribute)
	if !ok {
		t.Fatal("allowed_vips is not a set nested attribute")
	}
	first := core.UUID{0x31}.String()
	second := core.UUID{0x32}.String()
	tests := map[string]struct {
		value     types.Set
		wantError bool
	}{
		"distinct ids":         {value: reference.Set(reference.ID, []string{first, second})},
		"padded duplicate":     {value: reference.Set(reference.ID, []string{first, "  " + first + "  "}), wantError: true},
		"upper-case duplicate": {value: reference.Set(reference.ID, []string{first, strings.ToUpper(first)}), wantError: true},
		"unparsable ids":       {value: reference.Set(reference.ID, []string{"a", "b"})},
		"null set":             {value: types.SetNull(reference.ObjectType(reference.ID))},
		"unknown set":          {value: types.SetUnknown(reference.ObjectType(reference.ID))},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := validator.SetRequest{Path: path.Root("allowed_vips"), ConfigValue: test.value}
			var response validator.SetResponse
			for _, v := range attribute.Validators {
				v.ValidateSet(context.Background(), request, &response)
			}
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics = %v, want error %t", response.Diagnostics, test.wantError)
			}
		})
	}
}

func TestBuildPrivateIPCreateBody(t *testing.T) {
	t.Parallel()

	body, diags := buildPrivateIPCreateBody(PrivateIPResourceModel{
		PrivateIPModel: PrivateIPModel{
			Description: types.StringValue("application address"),
			IPAddress:   types.StringValue("10.0.1.10"),
			MACAddress:  types.StringValue("aa:bb:cc:dd:ee:ff"),
		},
		Subnet: reference.Value(reference.ID, types.StringValue(strings.ToUpper(privateIPTestSubnetID.String()))),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.SubnetId != privateIPTestSubnetID || body.Description == nil || *body.Description != "application address" ||
		core.NullableToPtr(body.IpAddress) == nil || *core.NullableToPtr(body.IpAddress) != "10.0.1.10" || core.NullableToPtr(body.MacAddress) == nil || *core.NullableToPtr(body.MacAddress) != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected create body: %#v", body)
	}
}

func TestBuildPrivateIPCreateBodyOmitsOptionalValues(t *testing.T) {
	t.Parallel()

	body, diags := buildPrivateIPCreateBody(PrivateIPResourceModel{
		PrivateIPModel: PrivateIPModel{
			Description: types.StringNull(),
			IPAddress:   types.StringUnknown(),
			MACAddress:  types.StringNull(),
		},
		Subnet: reference.Value(reference.ID, types.StringValue(privateIPTestSubnetID.String())),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Description != nil || body.IpAddress.IsSpecified() || body.MacAddress.IsSpecified() {
		t.Fatalf("expected optional fields to be omitted, got %#v", body)
	}
}

func TestBuildPrivateIPCreateBodyRejectsInvalidSubnet(t *testing.T) {
	t.Parallel()

	for _, subnet := range []types.Object{
		types.ObjectNull(reference.AttributeTypes(reference.ID)),
		types.ObjectUnknown(reference.AttributeTypes(reference.ID)),
		reference.Value(reference.ID, types.StringUnknown()),
		reference.Value(reference.ID, types.StringValue("invalid")),
	} {
		_, diags := buildPrivateIPCreateBody(PrivateIPResourceModel{Subnet: subnet})
		if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "subnet.id") {
			t.Errorf("expected invalid subnet.id diagnostic for %v, got %v", subnet, diags)
		}
	}
}

func TestBuildPrivateIPUpdateBody(t *testing.T) {
	t.Parallel()

	state := PrivateIPResourceModel{PrivateIPModel: PrivateIPModel{Description: types.StringValue("before")}}
	plan := state
	plan.Description = types.StringValue("after")
	body, changed := buildPrivateIPUpdateBody(plan, state)
	if !changed || core.NullableToPtr(body.Description) == nil || *core.NullableToPtr(body.Description) != "after" {
		t.Fatalf("expected sparse description update, got changed=%v body=%#v", changed, body)
	}

	plan.Description = types.StringValue("")
	body, changed = buildPrivateIPUpdateBody(plan, state)
	if !changed || core.NullableToPtr(body.Description) == nil || *core.NullableToPtr(body.Description) != "" {
		t.Fatalf("expected explicit clear, got changed=%v body=%#v", changed, body)
	}

	plan.Description = types.StringValue("  after  ")
	body, changed = buildPrivateIPUpdateBody(plan, state)
	if !changed || core.NullableToPtr(body.Description) == nil || *core.NullableToPtr(body.Description) != "after" {
		t.Fatalf("expected trimmed description update, got changed=%v body=%#v", changed, body)
	}

	for _, description := range []types.String{types.StringNull(), types.StringUnknown(), state.Description, types.StringValue("  before  ")} {
		plan.Description = description
		body, changed = buildPrivateIPUpdateBody(plan, state)
		if changed || body.Description.IsSpecified() {
			t.Errorf("expected omitted or unchanged description to be preserved, got changed=%v body=%#v", changed, body)
		}
	}
}

func TestBuildPrivateIPAllowedVIPsBodies(t *testing.T) {
	t.Parallel()

	configured := reference.Set(reference.ID, []string{strings.ToUpper(privateIPTestVIPID.String())})
	body, present, diags := buildPrivateIPAllowedVIPsBody(configured)
	if diags.HasError() || !present || len(body.AllowedVipIds) != 1 || body.AllowedVipIds[0] != privateIPTestVIPID {
		t.Fatalf("unexpected configured allowed VIP body: present=%v body=%#v diagnostics=%v", present, body, diags)
	}

	empty := reference.Set(reference.ID, nil)
	body, present, diags = buildPrivateIPAllowedVIPsBody(empty)
	if diags.HasError() || !present || body.AllowedVipIds == nil || len(body.AllowedVipIds) != 0 {
		t.Fatalf("expected explicit empty set body: present=%v body=%#v diagnostics=%v", present, body, diags)
	}

	for _, omitted := range []types.Set{types.SetNull(reference.ObjectType(reference.ID)), types.SetUnknown(reference.ObjectType(reference.ID))} {
		_, present, diags = buildPrivateIPAllowedVIPsBody(omitted)
		if diags.HasError() || present {
			t.Fatalf("expected omitted set %v to be absent: present=%v diagnostics=%v", omitted, present, diags)
		}
	}

	invalid := reference.Set(reference.ID, []string{"invalid"})
	_, _, diags = buildPrivateIPAllowedVIPsBody(invalid)
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "allowed_vips[0].id") {
		t.Fatalf("expected invalid allowed_vips[0].id diagnostic, got %v", diags)
	}
}

func TestPopulatePrivateIPResourceState(t *testing.T) {
	t.Parallel()

	privateIP := mockPrivateIP("before")
	configuredSubnetID := strings.ToUpper(privateIPTestSubnetID.String())
	configuredMAC := "AA-BB-CC-DD-EE-FF"
	state := PrivateIPResourceModel{
		PrivateIPModel: PrivateIPModel{
			IPAddress:  types.StringValue("10.0.1.10"),
			MACAddress: types.StringValue(configuredMAC),
		},
		Subnet: reference.Value(reference.ID, types.StringValue(configuredSubnetID)),
	}
	populatePrivateIPResourceState(privateIP, &state)

	if state.ID.ValueString() != privateIPTestID.String() || state.Description.ValueString() != "before" ||
		state.IPAddress.ValueString() != "10.0.1.10" || state.MACAddress.ValueString() != configuredMAC ||
		reference.Get(state.Subnet, reference.ID).ValueString() != configuredSubnetID || state.SubnetName.ValueString() != "application" ||
		state.SubnetCIDR.ValueString() != "10.0.1.0/24" || state.VPCID.ValueString() != privateIPTestVPCID.String() ||
		state.VPCName.ValueString() != "production" || state.Region.ValueString() != "vn-central-1" ||
		state.ServerID.ValueString() != privateIPTestServerID.String() || state.ServerName.ValueString() != "app-server" ||
		state.DeviceOwner.ValueString() != string(networksdk.PrivateIPDeviceOwnerUser) || state.DisplayName.ValueString() != "10.0.1.10" ||
		!state.PortSecurity.ValueBool() {
		t.Fatalf("unexpected private IP state: %#v", state)
	}
	if state.CreatedAt.ValueString() != privateIP.CreatedAt.Format(time.RFC3339) || state.UpdatedAt.ValueString() != privateIP.UpdatedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected timestamps: %#v", state)
	}
	if len(state.AllowedCIDRs.Elements()) != 2 || len(state.AttachedVIPs.Elements()) != 1 {
		t.Fatalf("unexpected collection mapping: allowed=%v attached=%v", state.AllowedCIDRs, state.AttachedVIPs)
	}
	if len(state.AllowedVIPs.Elements()) != 1 {
		t.Fatalf("unexpected allowed VIP ID mapping: %v", state.AllowedVIPs)
	}
}

func TestPopulatePrivateIPResourceStateMapsNullableFields(t *testing.T) {
	t.Parallel()

	privateIP := mockPrivateIP("")
	privateIP.Description = nil
	privateIP.IpAddress = nil
	privateIP.MacAddress = nil
	privateIP.Server = nil
	privateIP.AllowedCidrs = nil
	privateIP.AttachedVips = nil
	var state PrivateIPResourceModel
	populatePrivateIPResourceState(privateIP, &state)
	if !state.Description.IsNull() || !state.IPAddress.IsNull() || !state.MACAddress.IsNull() ||
		!state.ServerID.IsNull() || !state.ServerName.IsNull() || !state.AllowedCIDRs.IsNull() ||
		!state.AllowedVIPs.IsNull() || !state.AttachedVIPs.IsNull() {
		t.Fatalf("expected nullable fields to map to null: %#v", state)
	}

	populatePrivateIPResourceState(nil, &state)
}

func TestPrivateIPResourceUpdateAllowedVIPsUsesPublicSDK(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != "/v2/network/private-ips/"+privateIPTestID.String()+"/allowed-vips/" {
			t.Errorf("unexpected allowed VIP request: %s %s", req.Method, req.URL.Path)
		}
		var body networksdk.PrivateIPAllowedVIPsUpdateSchema
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode allowed VIP body: %v", err)
		}
		if len(body.AllowedVipIds) != 1 || body.AllowedVipIds[0] != privateIPTestVIPID {
			t.Errorf("unexpected allowed VIP body: %#v", body)
		}
		response := mockPrivateIP("before")
		w.Header().Set("Content-Type", "application/json")
		writePrivateIPResponse(t, w, response)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	resourceSchema := privateIPSchema(t)
	privateIP := mockPrivateIP("before")
	emptyVIPs := []networksdk.NestedPrivateIPAllowedVIPSchema{}
	privateIP.AttachedVips = &emptyVIPs
	var stateModel PrivateIPResourceModel
	populatePrivateIPResourceState(privateIP, &stateModel)
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	planModel := stateModel
	planModel.AllowedVIPs = reference.Set(reference.ID, []string{strings.ToUpper(privateIPTestVIPID.String())})
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	var got PrivateIPResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get updated state: %v", diags)
	}
	if len(got.AllowedVIPs.Elements()) != 1 {
		t.Fatalf("expected one allowed VIP in state, got %v", got.AllowedVIPs)
	}
}

func TestPrivateIPResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if got := req.Header.Get("Project-ID"); got != privateIPTestProjectID.String() {
			t.Errorf("expected Project-ID %q, got %q", privateIPTestProjectID, got)
		}
		w.Header().Set("Content-Type", "application/json")

		switch requests {
		case 1:
			if req.Method != http.MethodPost || req.URL.Path != "/v2/network/private-ips/" {
				t.Errorf("unexpected create request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if len(body) != 2 || body["subnet_id"] != privateIPTestSubnetID.String() || body["description"] != "before" {
				t.Errorf("unexpected create body: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writePrivateIPResponse(t, w, mockPrivateIP("before"))
		case 2:
			if req.Method != http.MethodGet || req.URL.Path != "/v2/network/private-ips/"+privateIPTestID.String()+"/" {
				t.Errorf("unexpected read request: %s %s", req.Method, req.URL.Path)
			}
			writePrivateIPResponse(t, w, mockPrivateIP("before"))
		case 3:
			if req.Method != http.MethodPatch || req.URL.Path != "/v2/network/private-ips/"+privateIPTestID.String()+"/" {
				t.Errorf("unexpected update request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			if len(body) != 1 || body["description"] != "after" {
				t.Errorf("unexpected sparse update body: %#v", body)
			}
			writePrivateIPResponse(t, w, mockPrivateIP("after"))
		case 4:
			if req.Method != http.MethodDelete || req.URL.Path != "/v2/network/private-ips/"+privateIPTestID.String()+"/" {
				t.Errorf("unexpected delete request: %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected extra request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	resourceSchema := privateIPSchema(t)

	createPlan := tfsdk.Plan{Schema: resourceSchema}
	createModel := nullPrivateIPResourceModel()
	createModel.Subnet = reference.Value(reference.ID, types.StringValue(strings.ToUpper(privateIPTestSubnetID.String())))
	createModel.Description = types.StringValue("before")
	if diags := createPlan.Set(ctx, &createModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var updateModel PrivateIPResourceModel
	if diags := readResponse.State.Get(ctx, &updateModel); diags.HasError() {
		t.Fatalf("get state before update: %v", diags)
	}
	updateModel.Description = types.StringValue("after")
	updatePlan := tfsdk.Plan{Schema: resourceSchema}
	if diags := updatePlan.Set(ctx, &updateModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}

	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if requests != 4 {
		t.Fatalf("expected four SDK requests, got %d", requests)
	}
}

func TestPrivateIPResourceTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")

		switch requests {
		case 1:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body["description"] != "before" || body["ip_address"] != "10.0.1.10" || body["mac_address"] != "aa:bb:cc:dd:ee:ff" ||
				body["subnet_id"] != privateIPTestSubnetID.String() {
				t.Errorf("create body = %#v, want trimmed description, ip_address, mac_address, and subnet_id", body)
			}
			w.WriteHeader(http.StatusCreated)
			writePrivateIPResponse(t, w, mockPrivateIP("before"))
		case 2:
			writePrivateIPResponse(t, w, mockPrivateIP("before"))
		case 3:
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			if len(body) != 1 || body["description"] != "after" {
				t.Errorf("update body = %#v, want only trimmed description", body)
			}
			writePrivateIPResponse(t, w, mockPrivateIP("after"))
		default:
			t.Errorf("unexpected extra request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	resourceSchema := privateIPSchema(t)

	createModel := nullPrivateIPResourceModel()
	createModel.Subnet = reference.Value(reference.ID, types.StringValue("  "+privateIPTestSubnetID.String()+"  "))
	createModel.Description = types.StringValue("  before  ")
	createModel.IPAddress = types.StringValue("  10.0.1.10  ")
	createModel.MACAddress = types.StringValue("  aa:bb:cc:dd:ee:ff  ")
	createPlan := tfsdk.Plan{Schema: resourceSchema}
	if diags := createPlan.Set(ctx, &createModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	for step, state := range map[string]tfsdk.State{"create": createResponse.State, "read": readResponse.State} {
		var model PrivateIPResourceModel
		if diags := state.Get(ctx, &model); diags.HasError() {
			t.Fatalf("get state after %s: %v", step, diags)
		}
		if !model.Description.Equal(createModel.Description) || !model.IPAddress.Equal(createModel.IPAddress) ||
			!model.MACAddress.Equal(createModel.MACAddress) || !model.Subnet.Equal(createModel.Subnet) {
			t.Fatalf("state after %s: description=%q ip_address=%q mac_address=%q subnet=%s, want the configured spelling",
				step, model.Description.ValueString(), model.IPAddress.ValueString(), model.MACAddress.ValueString(), model.Subnet)
		}
	}

	var updateModel PrivateIPResourceModel
	if diags := readResponse.State.Get(ctx, &updateModel); diags.HasError() {
		t.Fatalf("get state before update: %v", diags)
	}
	updateModel.Description = types.StringValue("  after  ")
	updatePlan := tfsdk.Plan{Schema: resourceSchema}
	if diags := updatePlan.Set(ctx, &updateModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}
	var afterUpdate PrivateIPResourceModel
	if diags := updateResponse.State.Get(ctx, &afterUpdate); diags.HasError() {
		t.Fatalf("get state after update: %v", diags)
	}
	if afterUpdate.Description.ValueString() != "  after  " {
		t.Fatalf("description after update = %q, want configured %q", afterUpdate.Description.ValueString(), "  after  ")
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want create, read, and update", requests)
	}
}

func TestPrivateIPResourceCreateConfiguresAllowedVIPs(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			if req.Method != http.MethodPost || req.URL.Path != "/v2/network/private-ips/" {
				t.Errorf("unexpected create request: %s %s", req.Method, req.URL.Path)
			}
			privateIP := mockPrivateIP("before")
			emptyVIPs := []networksdk.NestedPrivateIPAllowedVIPSchema{}
			privateIP.AttachedVips = &emptyVIPs
			w.WriteHeader(http.StatusCreated)
			writePrivateIPResponse(t, w, privateIP)
		case 2:
			if req.Method != http.MethodPut || req.URL.Path != "/v2/network/private-ips/"+privateIPTestID.String()+"/allowed-vips/" {
				t.Errorf("unexpected allowed VIP request: %s %s", req.Method, req.URL.Path)
			}
			var body networksdk.PrivateIPAllowedVIPsUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode allowed VIP body: %v", err)
			}
			if len(body.AllowedVipIds) != 1 || body.AllowedVipIds[0] != privateIPTestVIPID {
				t.Errorf("unexpected allowed VIP body: %#v", body)
			}
			writePrivateIPResponse(t, w, mockPrivateIP("before"))
		default:
			t.Errorf("unexpected extra request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	resourceSchema := privateIPSchema(t)
	model := nullPrivateIPResourceModel()
	model.Subnet = reference.Value(reference.ID, types.StringValue(privateIPTestSubnetID.String()))
	configuredVIPID := strings.ToUpper(privateIPTestVIPID.String())
	model.AllowedVIPs = reference.Set(reference.ID, []string{configuredVIPID})
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: resourceSchema}}
	(&PrivateIPResource{client: client, projectID: privateIPTestProjectID}).Create(
		context.Background(), resource.CreateRequest{Plan: plan}, &response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}
	var state PrivateIPResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	values := reference.Strings(state.AllowedVIPs, reference.ID)
	if requests != 2 || len(values) != 1 || values[0].ValueString() != configuredVIPID {
		t.Fatalf("unexpected configured allowed VIP state: requests=%d state=%v", requests, state.AllowedVIPs)
	}
}

func TestPrivateIPResourceReadRemovesMissingResourceAndDeleteAcceptsNotFound(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()
	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	resourceSchema := privateIPSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	stateModel := nullPrivateIPResourceModel()
	stateModel.ID = types.StringValue(privateIPTestID.String())
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if readResponse.Diagnostics.HasError() || !readResponse.State.Raw.IsNull() {
		t.Fatalf("expected missing resource removed without error: diagnostics=%v state=%v", readResponse.Diagnostics, readResponse.State.Raw)
	}
	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() || requests != 2 {
		t.Fatalf("expected not-found delete success: diagnostics=%v requests=%d", deleteResponse.Diagnostics, requests)
	}
}

func TestPrivateIPResourceReportsAPIErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"failed"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	resourceSchema := privateIPSchema(t)

	createPlan := tfsdk.Plan{Schema: resourceSchema}
	createModel := nullPrivateIPResourceModel()
	createModel.Subnet = reference.Value(reference.ID, types.StringValue(privateIPTestSubnetID.String()))
	if diags := createPlan.Set(context.Background(), &createModel); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: createPlan}, &createResponse)
	if !createResponse.Diagnostics.HasError() {
		t.Fatal("expected create API diagnostic")
	}

	stateModel := nullPrivateIPResourceModel()
	stateModel.ID = types.StringValue(privateIPTestID.String())
	stateModel.Description = types.StringValue("before")
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	planModel := stateModel
	planModel.Description = types.StringValue("after")
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Fatal("expected update API diagnostic")
	}

	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Fatal("expected delete API diagnostic")
	}
}

func TestPrivateIPResourceRejectsInvalidStateIDs(t *testing.T) {
	t.Parallel()

	resourceSchema := privateIPSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	stateModel := nullPrivateIPResourceModel()
	stateModel.ID = types.StringValue("invalid")
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set invalid state: %v", diags)
	}
	r := &PrivateIPResource{}
	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if !readResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid read ID diagnostic")
	}
	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid delete ID diagnostic")
	}
}

func TestPrivateIPResourceImportState(t *testing.T) {
	t.Parallel()

	resourceSchema := privateIPSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: resourceSchema}}
	model := nullPrivateIPResourceModel()
	if diags := response.State.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	(&PrivateIPResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: privateIPTestID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() || id.ValueString() != privateIPTestID.String() {
		t.Fatalf("unexpected imported ID %q: %v", id.ValueString(), response.Diagnostics)
	}
}

func TestPrivateIPReadAfterImportFillsReferences(t *testing.T) {
	t.Parallel()

	privateIP := mockPrivateIP("imported")
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &PrivateIPResource{client: client, projectID: privateIPTestProjectID}
	}, privateIP.Id.String(), privateIP)

	var got PrivateIPResourceModel
	if diags := state.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if want := reference.Value(reference.ID, types.StringValue(privateIPTestSubnetID.String())); !got.Subnet.Equal(want) {
		t.Fatalf("imported subnet = %s, want %s", got.Subnet, want)
	}
	if want := reference.Set(reference.ID, []string{privateIPTestVIPID.String()}); !got.AllowedVIPs.Equal(want) {
		t.Fatalf("imported allowed_vips = %s, want %s", got.AllowedVIPs, want)
	}
}

func privateIPSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&PrivateIPResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func nullPrivateIPResourceModel() PrivateIPResourceModel {
	return PrivateIPResourceModel{
		PrivateIPModel: PrivateIPModel{
			AllowedCIDRs: types.SetNull(types.StringType),
			AttachedVIPs: types.SetNull(types.ObjectType{AttrTypes: privateIPAttachedVIPAttributeTypes}),
		},
		Subnet:      types.ObjectNull(reference.AttributeTypes(reference.ID)),
		AllowedVIPs: types.SetNull(reference.ObjectType(reference.ID)),
	}
}

func mockPrivateIP(description string) *networksdk.PrivateIPSchema {
	ipAddress := "10.0.1.10"
	macAddress := "aa:bb:cc:dd:ee:ff"
	vipAddress := "10.0.1.100"
	allowedCIDRs := []string{"10.0.1.0/24", "10.0.2.0/24"}
	attachedVIPs := []networksdk.NestedPrivateIPAllowedVIPSchema{{
		Id:        privateIPTestVIPID,
		IpAddress: core.NullableFromPtr(&vipAddress),
	}}
	return &networksdk.PrivateIPSchema{
		Id:           privateIPTestID,
		Description:  core.NullableFromPtr(&description),
		IpAddress:    core.NullableFromPtr(&ipAddress),
		MacAddress:   core.NullableFromPtr(&macAddress),
		DisplayName:  ipAddress,
		DeviceOwner:  networksdk.PrivateIPDeviceOwnerUser,
		PortSecurity: true,
		AllowedCidrs: &allowedCIDRs,
		AttachedVips: &attachedVIPs,
		Subnet: networksdk.NestedSubnetSchema{
			Id:   privateIPTestSubnetID,
			Name: "application",
			Cidr: "10.0.1.0/24",
		},
		Vpc: networksdk.NestedVPCSchema{
			Id:          privateIPTestVPCID,
			Name:        "production",
			DisplayName: "production",
		},
		Region: networksdk.NestedRegionSchema{
			Id:          core.UUID{0x77},
			Name:        "vn-central-1",
			Description: "Central region",
		},
		Server: core.NullableFromPtr(&networksdk.NestedServerSchema{
			Id:   privateIPTestServerID,
			Name: "app-server",
		}),
		Project: networksdk.NestedProjectSchema{
			Id:   privateIPTestProjectID,
			Name: "integration",
			Slug: "integration",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func writePrivateIPResponse(t *testing.T, w http.ResponseWriter, privateIP *networksdk.PrivateIPSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(privateIP); err != nil {
		t.Errorf("encode private IP response: %v", err)
	}
}
