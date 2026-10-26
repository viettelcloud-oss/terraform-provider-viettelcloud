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
	subnetTestUUID  = core.UUID{0xab}
	subnetTestVPCID = core.UUID{0xcd}
	subnetProjectID = core.UUID{4}
)

func TestSubnetModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	subnetResourceSchema := subnetSchema(t)
	resourcePlan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := resourcePlan.Set(context.Background(), &SubnetResourceModel{VPC: types.ObjectNull(reference.AttributeTypes(reference.ID))}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestSubnetResourceConstructorAndMetadata(t *testing.T) {
	t.Parallel()

	r := NewSubnetResource()
	if r == nil {
		t.Fatal("expected non-nil resource")
	}
	var resp resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_subnet" {
		t.Fatalf("expected TypeName 'viettelcloud_subnet', got %q", resp.TypeName)
	}
}

func TestSubnetResourceConfigure(t *testing.T) {
	t.Parallel()

	r := &SubnetResource{}

	// Case 1: nil ProviderData
	var resp1 resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp1)
	if resp1.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil ProviderData: %v", resp1.Diagnostics)
	}

	// Case 2: invalid ProviderData type
	var resp2 resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &resp2)
	if !resp2.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid provider data type")
	}

	// Case 3: valid ProviderData
	var resp3 resource.ConfigureResponse
	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	configured := &providerdata.Configured{
		Network:   client,
		ProjectID: subnetProjectID,
	}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &resp3)
	if resp3.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on valid ProviderData: %v", resp3.Diagnostics)
	}
	if r.client != client || r.projectID != subnetProjectID {
		t.Fatalf("expected client and projectID to be configured, got client=%v, projectID=%v", r.client, r.projectID)
	}
}

func TestSubnetReplacementAttributes(t *testing.T) {
	t.Parallel()

	subnetResourceSchema := subnetSchema(t)
	attribute, ok := subnetResourceSchema.Attributes["cidr"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatal("cidr is not a string attribute")
	}
	if len(attribute.PlanModifiers) != 1 {
		t.Fatalf("expected one plan modifier for cidr, got %d", len(attribute.PlanModifiers))
	}

	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{CIDR: types.StringValue("before")},
		VPC:         reference.Value(reference.ID, types.StringValue("before")),
	}); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := plan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{CIDR: types.StringValue("after")},
		VPC:         reference.Value(reference.ID, types.StringValue("after")),
	}); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
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
		t.Error("expected changing cidr to require replacement")
	}
}

func TestSubnetVPCReferenceSchemaAndReplacement(t *testing.T) {
	t.Parallel()

	subnetResourceSchema := subnetSchema(t)
	vpc := requiredReferenceAttribute(t, subnetResourceSchema, "vpc", reference.ID)
	before := reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String()))
	tests := map[string]struct {
		plan types.Object
		want bool
	}{
		"same id updates in place": {plan: before},
		"another id replaces":      {plan: reference.Value(reference.ID, types.StringValue(core.UUID{0xef}.String())), want: true},
		"unknown id replaces":      {plan: reference.Value(reference.ID, types.StringUnknown()), want: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := referenceRequiresReplace(t, subnetResourceSchema, vpc, before, test.plan); got != test.want {
				t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
			}
		})
	}
}

func TestBuildSubnetCreateBody(t *testing.T) {
	t.Parallel()

	body, diags := buildSubnetCreateBody(SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name:        types.StringValue("  application  "),
			Description: types.StringValue("application tier"),
			CIDR:        types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String())),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "application" || body.Cidr != "10.0.1.0/24" || body.VpcId != subnetTestVPCID ||
		body.Description == nil || *body.Description != "application tier" {
		t.Fatalf("unexpected create body: %#v", body)
	}
}

func TestBuildSubnetCreateBodyOmitsDescription(t *testing.T) {
	t.Parallel()

	body, diags := buildSubnetCreateBody(SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name:        types.StringValue("application"),
			Description: types.StringNull(),
			CIDR:        types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String())),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Description != nil {
		t.Fatalf("expected description to be omitted, got %q", *body.Description)
	}
}

func TestBuildSubnetCreateBodyRejectsUnknownAndInvalidInputs(t *testing.T) {
	t.Parallel()

	// Unknown inputs
	_, diagsUnknown := buildSubnetCreateBody(SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringUnknown(),
			CIDR: types.StringUnknown(),
		},
		VPC: reference.Value(reference.ID, types.StringValue("not-a-uuid")),
	})
	if !diagsUnknown.HasError() || len(diagsUnknown.Errors()) != 3 {
		t.Fatalf("expected diagnostics for unknown name, CIDR, and invalid VPC ID, got %v", diagsUnknown)
	}

	// Null inputs
	_, diagsNull := buildSubnetCreateBody(SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringNull(),
			CIDR: types.StringNull(),
		},
		VPC: reference.Value(reference.ID, types.StringNull()),
	})
	if !diagsNull.HasError() || len(diagsNull.Errors()) != 3 {
		t.Fatalf("expected diagnostics for null name, CIDR, and VPC ID, got %v", diagsNull)
	}

	// Unknown VPC ID
	_, diagsUnknownVPC := buildSubnetCreateBody(SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue("application"),
			CIDR: types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringUnknown()),
	})
	if !diagsUnknownVPC.HasError() || !strings.Contains(diagsUnknownVPC.Errors()[0].Detail(), "vpc.id") {
		t.Fatalf("expected diagnostics naming vpc.id for an unknown VPC ID, got %v", diagsUnknownVPC)
	}
}

func TestBuildSubnetUpdateBodySendsOnlyChanges(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name:        types.StringValue("application"),
		Description: types.StringValue("before"),
	}}
	plan := state
	plan.Name = types.StringValue("  services  ")

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Name == nil || *body.Name != "services" || body.Description != nil {
		t.Fatalf("unexpected sparse update body: %#v", body)
	}
}

func TestBuildSubnetUpdateBodyPreservesOmittedDescription(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name:        types.StringValue("application"),
		Description: types.StringValue("keep me"),
	}}
	plan := state
	plan.Description = types.StringNull()

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if changed || body.Description != nil || body.Name != nil {
		t.Fatalf("expected an empty update body, got %#v", body)
	}
}

func TestBuildSubnetUpdateBodyClearsDescription(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name:        types.StringValue("application"),
		Description: types.StringValue("clear me"),
	}}
	plan := state
	plan.Description = types.StringValue("")

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Description == nil || *body.Description != "" || body.Name != nil {
		t.Fatalf("expected an explicit description clear, got %#v", body)
	}
}

func TestBuildSubnetUpdateBodyDescriptionOnly(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name:        types.StringValue("application"),
		Description: types.StringValue("before"),
	}}
	plan := state
	plan.Description = types.StringValue("after")

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Name != nil || body.Description == nil || *body.Description != "after" {
		t.Fatalf("expected description update body, got %#v", body)
	}
}

func TestBuildSubnetUpdateBodyStateNullDescription(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name:        types.StringValue("application"),
		Description: types.StringNull(),
	}}
	plan := state
	plan.Description = types.StringValue("new description")

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Description == nil || *body.Description != "new description" {
		t.Fatalf("expected description to be set from null state, got %#v", body)
	}
}

func TestBuildSubnetUpdateBodyPreservesUntrimmedNameWhenSemanticallyEqual(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name: types.StringValue("application"),
	}}
	plan := SubnetResourceModel{SubnetModel: SubnetModel{
		Name: types.StringValue("  application  "),
	}}

	body, changed, diags := buildSubnetUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if changed || body.Name != nil {
		t.Fatalf("expected no change when name differs only by whitespace, got changed=%v body=%#v", changed, body)
	}
}

func TestBuildSubnetUpdateBodyRejectsUnknownAndNullName(t *testing.T) {
	t.Parallel()

	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name: types.StringValue("application"),
	}}

	// Case 1: Unknown name
	planUnknown := state
	planUnknown.Name = types.StringUnknown()
	_, _, diagsUnknown := buildSubnetUpdateBody(planUnknown, state)
	if !diagsUnknown.HasError() {
		t.Fatal("expected diagnostic for unknown name in update plan")
	}

	// Case 2: Null name
	planNull := state
	planNull.Name = types.StringNull()
	_, _, diagsNull := buildSubnetUpdateBody(planNull, state)
	if !diagsNull.HasError() {
		t.Fatal("expected diagnostic for null name in update plan")
	}
}

func TestPopulateSubnetModelNil(t *testing.T) {
	t.Parallel()

	var m SubnetModel
	populateSubnetModel(nil, &m)
	if !m.ID.IsNull() {
		t.Fatalf("expected null ID on nil subnet, got %v", m.ID)
	}
}

func TestPopulateSubnetResourceStateNil(t *testing.T) {
	t.Parallel()

	var state SubnetResourceModel
	populateSubnetResourceState(nil, &state)
	if !state.ID.IsNull() {
		t.Fatalf("expected null ID on nil subnet, got %v", state.ID)
	}
}

func TestPopulateSubnetResourceState(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "application tier")
	state := SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue("  application  "),
		},
		VPC: reference.Value(reference.ID, types.StringValue(strings.ToUpper(subnet.Vpc.Id.String()))),
	}
	populateSubnetResourceState(subnet, &state)

	if state.ID.ValueString() != subnet.Id.String() || state.Name.ValueString() != "  application  " ||
		state.Description.ValueString() != "application tier" || state.CIDR.ValueString() != subnet.Cidr ||
		reference.Get(state.VPC, reference.ID).ValueString() != strings.ToUpper(subnet.Vpc.Id.String()) || state.VPCName.ValueString() != subnet.Vpc.Name ||
		state.Region.ValueString() != subnet.Region.Name || state.DisplayName.ValueString() != subnet.DisplayName {
		t.Fatalf("unexpected subnet state: %#v", state)
	}
	if state.CreatedAt.ValueString() != subnet.CreatedAt.Format(time.RFC3339) ||
		state.UpdatedAt.ValueString() != subnet.UpdatedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected subnet timestamps: %#v", state)
	}
}

func TestPopulateSubnetResourceStateNilDescription(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "")
	subnet.Description = nil
	var state SubnetResourceModel
	populateSubnetResourceState(subnet, &state)
	if !state.Description.IsNull() {
		t.Fatalf("expected null description, got %q", state.Description.ValueString())
	}
}

func TestPopulateSubnetResourceStatePreservesConfiguredNameWhenBackendTrimsWhitespace(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "application tier")
	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name: types.StringValue("  application  "),
	}}
	populateSubnetResourceState(subnet, &state)
	if state.Name.ValueString() != "  application  " {
		t.Fatalf("expected configured untrimmed name to be preserved, got %q", state.Name.ValueString())
	}
}

func TestPopulateSubnetResourceStateUsesChangedBackendName(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("new-backend-name", "application tier")
	state := SubnetResourceModel{SubnetModel: SubnetModel{
		Name: types.StringValue("old-name"),
	}}
	populateSubnetResourceState(subnet, &state)
	if state.Name.ValueString() != "new-backend-name" {
		t.Fatalf("expected updated backend name %q, got %q", "new-backend-name", state.Name.ValueString())
	}
}

func TestPopulateSubnetResourceStateUsesChangedBackendVPCID(t *testing.T) {
	t.Parallel()

	configuredVPCID := subnetTestVPCID
	subnet := mockSubnet("application", "application tier")
	subnet.Vpc.Id = core.UUID{0xef}
	state := SubnetResourceModel{
		VPC: reference.Value(reference.ID, types.StringValue(strings.ToUpper(configuredVPCID.String()))),
	}

	populateSubnetResourceState(subnet, &state)

	if reference.Get(state.VPC, reference.ID).ValueString() != subnet.Vpc.Id.String() {
		t.Fatalf("expected backend VPC ID %q, got %q", subnet.Vpc.Id, reference.Get(state.VPC, reference.ID).ValueString())
	}
}

func TestPopulateSubnetResourceStateNullAndUnknownInputs(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "tier")
	state := SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringNull(),
		},
		VPC: reference.Value(reference.ID, types.StringUnknown()),
	}
	populateSubnetResourceState(subnet, &state)
	if state.Name.ValueString() != subnet.Name || reference.Get(state.VPC, reference.ID).ValueString() != subnet.Vpc.Id.String() {
		t.Fatalf("expected state to receive backend values, got name=%q vpc_id=%q", state.Name.ValueString(), reference.Get(state.VPC, reference.ID).ValueString())
	}
}

func TestPopulateSubnetResourceStateInvalidVPCIDInState(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "tier")
	state := SubnetResourceModel{
		VPC: reference.Value(reference.ID, types.StringValue("not-a-valid-uuid")),
	}
	populateSubnetResourceState(subnet, &state)
	if reference.Get(state.VPC, reference.ID).ValueString() != subnet.Vpc.Id.String() {
		t.Fatalf("expected backend VPC ID %q, got %q", subnet.Vpc.Id, reference.Get(state.VPC, reference.ID).ValueString())
	}
}

func TestSubnetResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if got := req.Header.Get("Project-ID"); got != subnetProjectID.String() {
			t.Errorf("expected Project-ID %q, got %q", subnetProjectID, got)
		}
		w.Header().Set("Content-Type", "application/json")

		switch requests {
		case 1:
			if req.Method != http.MethodPost || req.URL.Path != "/v2/network/subnets/" {
				t.Errorf("unexpected create request: %s %s", req.Method, req.URL.Path)
			}
			var body networksdk.SubnetCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.Name != "application" || body.Cidr != "10.0.1.0/24" || body.VpcId != subnetTestVPCID ||
				body.Description == nil || *body.Description != "before" {
				t.Errorf("unexpected create body: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writeSubnetResponse(t, w, mockSubnet("application", "before"))
		case 2:
			if req.Method != http.MethodGet || req.URL.Path != "/v2/network/subnets/"+subnetTestUUID.String()+"/" {
				t.Errorf("unexpected read request: %s %s", req.Method, req.URL.Path)
			}
			writeSubnetResponse(t, w, mockSubnet("application", "before"))
		case 3:
			if req.Method != http.MethodPatch || req.URL.Path != "/v2/network/subnets/"+subnetTestUUID.String()+"/" {
				t.Errorf("unexpected update request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			if len(body) != 2 || body["name"] != "services" || body["description"] != "after" {
				t.Errorf("unexpected sparse update body: %#v", body)
			}
			writeSubnetResponse(t, w, mockSubnet("services", "after"))
		case 4:
			if req.Method != http.MethodDelete || req.URL.Path != "/v2/network/subnets/"+subnetTestUUID.String()+"/" {
				t.Errorf("unexpected delete request: %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected extra request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)

	createPlan := tfsdk.Plan{Schema: subnetResourceSchema}
	configuredVPCID := strings.ToUpper(subnetTestVPCID.String())
	if diags := createPlan.Set(ctx, &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name:        types.StringValue("application"),
			Description: types.StringValue("before"),
			CIDR:        types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(configuredVPCID)),
	}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: subnetResourceSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var updatePlanModel SubnetResourceModel
	if diags := readResponse.State.Get(ctx, &updatePlanModel); diags.HasError() {
		t.Fatalf("get state before update: %v", diags)
	}
	if reference.Get(updatePlanModel.VPC, reference.ID).ValueString() != configuredVPCID {
		t.Fatalf("expected configured VPC ID %q after read, got %q", configuredVPCID, reference.Get(updatePlanModel.VPC, reference.ID).ValueString())
	}
	updatePlanModel.Name = types.StringValue("services")
	updatePlanModel.Description = types.StringValue("after")
	updatePlan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := updatePlan.Set(ctx, &updatePlanModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: subnetResourceSchema}}
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

func TestSubnetResourceLifecycleWithUntrimmedName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")

		switch requests {
		case 1:
			if req.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", req.Method)
			}
			var body networksdk.SubnetCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.Name != "application" {
				t.Errorf("expected trimmed create body name 'application', got %q", body.Name)
			}
			w.WriteHeader(http.StatusCreated)
			writeSubnetResponse(t, w, mockSubnet("application", "tier"))
		case 2:
			if req.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", req.Method)
			}
			writeSubnetResponse(t, w, mockSubnet("application", "tier"))
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)

	untrimmedName := "  application  "
	createPlan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := createPlan.Set(ctx, &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue(untrimmedName),
			CIDR: types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String())),
	}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: subnetResourceSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	var stateAfterCreate SubnetResourceModel
	if diags := createResponse.State.Get(ctx, &stateAfterCreate); diags.HasError() {
		t.Fatalf("get state after create: %v", diags)
	}
	if stateAfterCreate.Name.ValueString() != untrimmedName {
		t.Fatalf("expected state name to preserve untrimmed %q, got %q", untrimmedName, stateAfterCreate.Name.ValueString())
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var stateAfterRead SubnetResourceModel
	if diags := readResponse.State.Get(ctx, &stateAfterRead); diags.HasError() {
		t.Fatalf("get state after read: %v", diags)
	}
	if stateAfterRead.Name.ValueString() != untrimmedName {
		t.Fatalf("expected read state name to preserve untrimmed %q, got %q", untrimmedName, stateAfterRead.Name.ValueString())
	}
}

func TestSubnetResourceCreateRejectsInvalidPlan(t *testing.T) {
	t.Parallel()

	r := &SubnetResource{projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	plan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := plan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue("application"),
			CIDR: types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue("invalid-uuid")),
	}); diags.HasError() {
		t.Fatalf("set invalid plan: %v", diags)
	}

	var response resource.CreateResponse
	response.State = tfsdk.State{Schema: subnetResourceSchema}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostics error on invalid VPC ID in plan")
	}
}

func TestSubnetResourceCreateReportsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"service unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	subnetResourceSchema := subnetSchema(t)
	plan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := plan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue("application"),
			CIDR: types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String())),
	}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}

	response := resource.CreateResponse{State: tfsdk.State{Schema: subnetResourceSchema}}
	(&SubnetResource{client: client, projectID: subnetProjectID}).Create(
		context.Background(),
		resource.CreateRequest{Plan: plan},
		&response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected API failure diagnostic")
	}
}

func TestSubnetResourceReadRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	r := &SubnetResource{projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue("not-a-valid-uuid"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set invalid state ID: %v", diags)
	}

	var response resource.ReadResponse
	response.State = state
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid state ID")
	}
}

func TestSubnetResourceReadRemovesMissingResourceAndDeleteAcceptsNotFound(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue(subnetTestUUID.String()),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set initial state: %v", diags)
	}

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	if !readResponse.State.Raw.IsNull() {
		t.Fatal("expected missing subnet to be removed from state")
	}

	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if requests != 2 {
		t.Fatalf("expected read and delete requests, got %d", requests)
	}
}

func TestSubnetResourceReadReportsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"internal server error"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue(subnetTestUUID.String()),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var response resource.ReadResponse
	response.State = state
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for 500 API response on Read")
	}
}

func TestSubnetResourceUpdateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	r := &SubnetResource{projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)

	// Case 1: Invalid state ID
	invalidState := tfsdk.State{Schema: subnetResourceSchema}
	if diags := invalidState.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue("bad-id"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set invalid state: %v", diags)
	}
	validPlan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := validPlan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringValue("app"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	var resp1 resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{Plan: validPlan, State: invalidState}, &resp1)
	if !resp1.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid state ID")
	}

	// Case 2: Invalid plan name
	validState := tfsdk.State{Schema: subnetResourceSchema}
	if diags := validState.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID:   types.StringValue(subnetTestUUID.String()),
			Name: types.StringValue("app"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	invalidPlan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := invalidPlan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name: types.StringUnknown(),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set invalid plan: %v", diags)
	}
	var resp2 resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{Plan: invalidPlan, State: validState}, &resp2)
	if !resp2.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid plan name")
	}
}

func TestSubnetResourceUpdateNoChangesCallsGetSubnet(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != http.MethodGet || req.URL.Path != "/v2/network/subnets/"+subnetTestUUID.String()+"/" {
			t.Errorf("unexpected request on unchanged update: %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writeSubnetResponse(t, w, mockSubnet("application", "application tier"))
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)

	model := SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID:          types.StringValue(subnetTestUUID.String()),
			Name:        types.StringValue("application"),
			Description: types.StringValue("application tier"),
			CIDR:        types.StringValue("10.0.1.0/24"),
		},
		VPC: reference.Value(reference.ID, types.StringValue(subnetTestVPCID.String())),
	}

	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var response resource.UpdateResponse
	response.State = tfsdk.State{Schema: subnetResourceSchema}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on unchanged update: %v", response.Diagnostics)
	}
	if requests != 1 {
		t.Fatalf("expected exactly 1 GET request on unchanged update, got %d", requests)
	}
}

func TestSubnetResourceUpdateReportsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"update failed"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)

	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID:          types.StringValue(subnetTestUUID.String()),
			Name:        types.StringValue("before"),
			Description: types.StringValue("before"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: subnetResourceSchema}
	if diags := plan.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			Name:        types.StringValue("after"),
			Description: types.StringValue("after"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	var response resource.UpdateResponse
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for 500 API response on Update")
	}
}

func TestSubnetResourceDeleteRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	r := &SubnetResource{projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue("invalid-uuid"),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid state ID on Delete")
	}
}

func TestSubnetResourceDeleteReportsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"cannot delete subnet"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &SubnetResource{client: client, projectID: subnetProjectID}
	subnetResourceSchema := subnetSchema(t)
	state := tfsdk.State{Schema: subnetResourceSchema}
	if diags := state.Set(context.Background(), &SubnetResourceModel{
		SubnetModel: SubnetModel{
			ID: types.StringValue(subnetTestUUID.String()),
		},
		VPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for 500 API response on Delete")
	}
}

func TestSubnetResourceImportState(t *testing.T) {
	t.Parallel()

	subnetResourceSchema := subnetSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: subnetResourceSchema}}
	if diags := response.State.Set(context.Background(), &SubnetResourceModel{VPC: types.ObjectNull(reference.AttributeTypes(reference.ID))}); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	(&SubnetResource{}).ImportState(context.Background(), resource.ImportStateRequest{
		ID: subnetTestUUID.String(),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}

	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("get imported ID: %v", response.Diagnostics)
	}
	if id.ValueString() != subnetTestUUID.String() {
		t.Fatalf("expected imported ID %q, got %q", subnetTestUUID, id.ValueString())
	}
}

func TestSubnetReadAfterImportFillsVPCReference(t *testing.T) {
	t.Parallel()

	subnet := mockSubnet("application", "description")
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &SubnetResource{client: client, projectID: subnetProjectID}
	}, subnet.Id.String(), subnet)

	var vpc types.Object
	if diags := state.GetAttribute(context.Background(), path.Root("vpc"), &vpc); diags.HasError() {
		t.Fatalf("read vpc: %v", diags)
	}
	if want := reference.Value(reference.ID, types.StringValue(subnet.Vpc.Id.String())); !vpc.Equal(want) {
		t.Fatalf("imported vpc = %s, want %s", vpc, want)
	}
}

func subnetSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&SubnetResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func mockSubnet(name, description string) *networksdk.SubnetSchema {
	return &networksdk.SubnetSchema{
		Id:          subnetTestUUID,
		Name:        name,
		Description: &description,
		Cidr:        "10.0.1.0/24",
		Vpc: networksdk.NestedVPCSchema{
			Id:          subnetTestVPCID,
			Name:        "production",
			DisplayName: "production",
		},
		Region: networksdk.NestedRegionSchema{
			Id:          core.UUID{5},
			Name:        "vn-central-1",
			Description: "Central region",
		},
		Project: networksdk.NestedProjectSchema{
			Id:   subnetProjectID,
			Name: "integration",
			Slug: "integration",
		},
		DisplayName: name,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func writeSubnetResponse(t *testing.T, w http.ResponseWriter, subnet *networksdk.SubnetSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(subnet); err != nil {
		t.Errorf("encode subnet response: %v", err)
	}
}
