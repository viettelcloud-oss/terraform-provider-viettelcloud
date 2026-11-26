package network

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	vpcPeeringTestID          = core.UUID{0xa1}
	vpcPeeringRequesterVPCID  = core.UUID{0xa2}
	vpcPeeringAccepterVPCID   = core.UUID{0xa3}
	vpcPeeringProjectID       = core.UUID{0xa4}
	vpcPeeringAccepterProject = core.UUID{0xa5}
	vpcPeeringPath            = "/v2/network/vpc-peerings/" + vpcPeeringTestID.String() + "/"
)

func TestVPCPeeringModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	plan := tfsdk.Plan{Schema: vpcPeeringSchema(t)}
	if diags := plan.Set(context.Background(), emptyVPCPeeringResourceModel()); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestVPCPeeringResourceConstructorAndMetadata(t *testing.T) {
	t.Parallel()

	var resp resource.MetadataResponse
	NewVPCPeeringResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_vpc_peering" {
		t.Fatalf("TypeName = %q, want viettelcloud_vpc_peering", resp.TypeName)
	}
}

func TestVPCPeeringResourceConfigure(t *testing.T) {
	t.Parallel()

	r := &VPCPeeringResource{}
	var nilResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on nil ProviderData: %v", nilResponse.Diagnostics)
	}

	var invalidResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an invalid provider data type")
	}

	client, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	var validResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{Network: client, ProjectID: vpcPeeringProjectID},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on valid ProviderData: %v", validResponse.Diagnostics)
	}
	if r.client != client || r.projectID != vpcPeeringProjectID {
		t.Fatalf("client=%v projectID=%v, want configured values", r.client, r.projectID)
	}
}

func TestVPCPeeringSchemaAttributes(t *testing.T) {
	t.Parallel()

	peeringSchema := vpcPeeringSchema(t)
	if peeringSchema.Description == "" {
		t.Fatal("schema needs a description")
	}
	for _, note := range []string{"`pending` until that project accepts it outside Terraform", "route to the other VPC", "deletes every route"} {
		if !strings.Contains(peeringSchema.Description, note) {
			t.Errorf("schema description does not contain %q", note)
		}
	}

	description, ok := peeringSchema.Attributes["description"].(resourceschema.StringAttribute)
	if !ok || !description.Optional || !description.Computed || description.Required || len(description.PlanModifiers) != 0 {
		t.Fatalf("description = %#v, want Optional and Computed without plan modifiers", peeringSchema.Attributes["description"])
	}

	tests := map[string]bool{
		"id":                  true,
		"name":                true,
		"vpc_peering_type":    true,
		"accepter_project_id": true,
		"created_at":          true,
		"status":              false,
		"updated_at":          false,
	}
	for name, keepsState := range tests {
		attribute, ok := peeringSchema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Computed || attribute.Optional || attribute.Required {
			t.Errorf("%s = %#v, want a Computed string", name, peeringSchema.Attributes[name])
			continue
		}
		if got := len(attribute.PlanModifiers) == 1; got != keepsState {
			t.Errorf("%s has %d plan modifiers, want UseStateForUnknown=%t", name, len(attribute.PlanModifiers), keepsState)
		}
		if attribute.Description == "" {
			t.Errorf("%s needs a description", name)
		}
	}
}

func TestVPCPeeringVPCReferencesRequireReplace(t *testing.T) {
	t.Parallel()

	peeringSchema := vpcPeeringSchema(t)
	for _, name := range []string{"requester_vpc", "accepter_vpc"} {
		attribute := requiredReferenceAttribute(t, peeringSchema, name, reference.ID)
		before := reference.Value(reference.ID, types.StringValue(vpcPeeringRequesterVPCID.String()))
		tests := map[string]struct {
			plan types.Object
			want bool
		}{
			"same id updates in place": {plan: before},
			"padded upper-case id updates in place": {
				plan: reference.Value(reference.ID, types.StringValue(" "+strings.ToUpper(vpcPeeringRequesterVPCID.String())+"\n")),
			},
			"another id replaces": {plan: reference.Value(reference.ID, types.StringValue(core.UUID{0xef}.String())), want: true},
			"unknown id replaces": {plan: reference.Value(reference.ID, types.StringUnknown()), want: true},
		}
		for testName, test := range tests {
			t.Run(name+" "+testName, func(t *testing.T) {
				t.Parallel()
				if got := referenceRequiresReplace(t, peeringSchema, attribute, before, test.plan); got != test.want {
					t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
				}
			})
		}
	}
}

// The API trims outer whitespace from description, so the resource accepts a
// padded value and trims it in the request instead of rejecting it at plan.
func TestVPCPeeringResourceHasNoConfigValidation(t *testing.T) {
	t.Parallel()

	if _, ok := any(&VPCPeeringResource{}).(resource.ResourceWithValidateConfig); ok {
		t.Fatal("VPCPeeringResource must not validate its config; it trims outer whitespace instead")
	}
}

func TestBuildVPCPeeringCreateBody(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		description types.String
		want        *string
	}{
		"null omits description":    {description: types.StringNull()},
		"unknown omits description": {description: types.StringUnknown()},
		"empty is sent":             {description: types.StringValue(""), want: new("")},
		"value is sent":             {description: types.StringValue("app to db"), want: new("app to db")},
		"padded value is trimmed":   {description: types.StringValue(" app to db\n"), want: new("app to db")},
		"whitespace only clears":    {description: types.StringValue("   "), want: new("")},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := vpcPeeringPlanModel(test.description)
			model.RequesterVPC = reference.Value(reference.ID, types.StringValue(strings.ToUpper(vpcPeeringRequesterVPCID.String())))
			model.AccepterVPC = reference.Value(reference.ID, types.StringValue(" "+vpcPeeringAccepterVPCID.String()+"\t"))
			body, diags := buildVPCPeeringCreateBody(model)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.RequesterVpcId != vpcPeeringRequesterVPCID || body.AccepterVpcId != vpcPeeringAccepterVPCID {
				t.Fatalf("unexpected VPC IDs: %#v", body)
			}
			if (body.Description == nil) != (test.want == nil) || (body.Description != nil && *body.Description != *test.want) {
				t.Fatalf("description = %v, want %v", body.Description, test.want)
			}
		})
	}
}

func TestBuildVPCPeeringCreateBodyRejectsInvalidVPCIDs(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*VPCPeeringResourceModel){
		"invalid requester": func(m *VPCPeeringResourceModel) {
			m.RequesterVPC = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
		},
		"blank requester": func(m *VPCPeeringResourceModel) {
			m.RequesterVPC = reference.Value(reference.ID, types.StringValue("  "))
		},
		"unknown accepter": func(m *VPCPeeringResourceModel) {
			m.AccepterVPC = reference.Value(reference.ID, types.StringUnknown())
		},
		"null accepter": func(m *VPCPeeringResourceModel) {
			m.AccepterVPC = types.ObjectNull(reference.AttributeTypes(reference.ID))
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := vpcPeeringPlanModel(types.StringNull())
			change(&model)
			if _, diags := buildVPCPeeringCreateBody(model); !diags.HasError() {
				t.Fatal("expected a diagnostic")
			}
		})
	}
}

func TestBuildVPCPeeringUpdateBody(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		plan  types.String
		state types.String
		want  string
	}{
		"planned value":                  {plan: types.StringValue("after"), state: types.StringValue("before"), want: "after"},
		"planned empty clears":           {plan: types.StringValue(""), state: types.StringValue("before"), want: ""},
		"padded planned value trimmed":   {plan: types.StringValue(" after "), state: types.StringValue("before"), want: "after"},
		"omitted keeps prior value":      {plan: types.StringUnknown(), state: types.StringValue("before"), want: "before"},
		"omitted trims padded prior":     {plan: types.StringUnknown(), state: types.StringValue("\tbefore "), want: "before"},
		"omitted after null sends empty": {plan: types.StringUnknown(), state: types.StringNull(), want: ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := buildVPCPeeringUpdateBody(vpcPeeringPlanModel(test.plan), vpcPeeringPlanModel(test.state))
			if body.Description != test.want {
				t.Fatalf("description = %q, want %q", body.Description, test.want)
			}
		})
	}
}

func TestPopulateVPCPeeringResourceState(t *testing.T) {
	t.Parallel()

	peering := mockVPCPeering(new("app to db"), networksdk.VPCPeeringStatusActive)
	configuredRequester := reference.Value(reference.ID, types.StringValue(strings.ToUpper(vpcPeeringRequesterVPCID.String())))
	state := VPCPeeringResourceModel{
		RequesterVPC: configuredRequester,
		AccepterVPC:  reference.Value(reference.ID, types.StringValue(core.UUID{0xee}.String())),
	}
	populateVPCPeeringResourceState(peering, &state)

	want := VPCPeeringModel{
		ID:                types.StringValue(vpcPeeringTestID.String()),
		Name:              types.StringValue("peer-" + vpcPeeringTestID.String()),
		Description:       types.StringValue("app to db"),
		Status:            types.StringValue("active"),
		VPCPeeringType:    types.StringValue("intra_region"),
		AccepterProjectID: types.StringValue(vpcPeeringAccepterProject.String()),
		CreatedAt:         types.StringValue("2026-01-01T00:00:00Z"),
		UpdatedAt:         types.StringValue("2026-01-02T00:00:00Z"),
	}
	if state.VPCPeeringModel != want {
		t.Fatalf("state = %#v, want %#v", state.VPCPeeringModel, want)
	}
	if !state.RequesterVPC.Equal(configuredRequester) {
		t.Fatalf("requester_vpc = %s, want the configured spelling %s", state.RequesterVPC, configuredRequester)
	}
	if wantAccepter := reference.Value(reference.ID, types.StringValue(vpcPeeringAccepterVPCID.String())); !state.AccepterVPC.Equal(wantAccepter) {
		t.Fatalf("accepter_vpc = %s, want the response VPC %s", state.AccepterVPC, wantAccepter)
	}

	populateVPCPeeringResourceState(nil, &state)
	if state.VPCPeeringModel != want {
		t.Fatal("a nil response changed state")
	}
}

func TestPopulateVPCPeeringModelDescriptionPresence(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		description *string
		want        types.String
	}{
		"missing maps to null": {want: types.StringNull()},
		"empty maps to empty":  {description: new(""), want: types.StringValue("")},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var model VPCPeeringModel
			populateVPCPeeringModel(mockVPCPeering(test.description, networksdk.VPCPeeringStatusActive), &model)
			if !model.Description.Equal(test.want) {
				t.Fatalf("description = %s, want %s", model.Description, test.want)
			}
		})
	}
}

// TestVPCPeeringResourceStateKeepsConfiguredDescription checks that state keeps
// the configured description when the response differs from it only by outer
// whitespace, and the response value otherwise.
func TestVPCPeeringResourceStateKeepsConfiguredDescription(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configured types.String
		response   *string
		want       types.String
	}{
		"padded value kept":           {configured: types.StringValue(" app\n"), response: new("app"), want: types.StringValue(" app\n")},
		"whitespace only kept":        {configured: types.StringValue("  "), response: new(""), want: types.StringValue("  ")},
		"drift shows response":        {configured: types.StringValue(" app "), response: new("other"), want: types.StringValue("other")},
		"unknown takes response":      {configured: types.StringUnknown(), response: new("app"), want: types.StringValue("app")},
		"null takes response":         {configured: types.StringNull(), response: new(""), want: types.StringValue("")},
		"null response stays null":    {configured: types.StringValue(" "), want: types.StringNull()},
		"inner whitespace is a drift": {configured: types.StringValue("app  db"), response: new("app db"), want: types.StringValue("app db")},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := vpcPeeringPlanModel(test.configured)
			populateVPCPeeringResourceState(mockVPCPeering(test.response, networksdk.VPCPeeringStatusActive), &state)
			if !state.Description.Equal(test.want) {
				t.Fatalf("description = %s, want %s", state.Description, test.want)
			}
		})
	}
}

// TestVPCPeeringResourceKeepsPaddedValues checks that a padded description and
// padded VPC IDs reach the API trimmed, and that state after Create and the
// next Read keeps the configured spelling.
func TestVPCPeeringResourceKeepsPaddedValues(t *testing.T) {
	t.Parallel()

	const configuredDescription = " app to db\n"
	configuredRequester := reference.Value(reference.ID, types.StringValue(" "+strings.ToUpper(vpcPeeringRequesterVPCID.String())+" "))
	configuredAccepter := reference.Value(reference.ID, types.StringValue("\t"+vpcPeeringAccepterVPCID.String()))
	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPost {
			body := decodeJSONObject(t, req.Body)
			if body["description"] != "app to db" || body["requester_vpc_id"] != vpcPeeringRequesterVPCID.String() ||
				body["accepter_vpc_id"] != vpcPeeringAccepterVPCID.String() {
				t.Errorf("create body = %#v, want trimmed values", body)
			}
			w.WriteHeader(http.StatusCreated)
		}
		writeVPCPeeringResponse(t, w, mockVPCPeering(new("app to db"), networksdk.VPCPeeringStatusActive))
	}))
	defer server.Close()

	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	peeringSchema := vpcPeeringSchema(t)
	model := vpcPeeringPlanModel(types.StringValue(configuredDescription))
	model.RequesterVPC = configuredRequester
	model.AccepterVPC = configuredAccepter
	plan := tfsdk.Plan{Schema: peeringSchema}
	if diags := plan.Set(ctx, model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: peeringSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	for name, state := range map[string]tfsdk.State{"create": createResponse.State, "read": readResponse.State} {
		var got VPCPeeringResourceModel
		if diags := state.Get(ctx, &got); diags.HasError() {
			t.Fatalf("%s: get state: %v", name, diags)
		}
		if got.Description.ValueString() != configuredDescription ||
			!got.RequesterVPC.Equal(configuredRequester) || !got.AccepterVPC.Equal(configuredAccepter) {
			t.Fatalf("%s: state = %#v, want the configured spelling", name, got)
		}
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want create and read", requests)
	}
}

func TestVPCPeeringResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if got := req.Header.Get("Project-ID"); got != vpcPeeringProjectID.String() {
			t.Errorf("Project-ID = %q, want %q", got, vpcPeeringProjectID)
		}
		w.Header().Set("Content-Type", "application/json")

		switch requests {
		case 1:
			if req.Method != http.MethodPost || req.URL.Path != "/v2/network/vpc-peerings/" {
				t.Errorf("unexpected create request: %s %s", req.Method, req.URL.Path)
			}
			body := decodeJSONObject(t, req.Body)
			if len(body) != 2 || body["requester_vpc_id"] != vpcPeeringRequesterVPCID.String() ||
				body["accepter_vpc_id"] != vpcPeeringAccepterVPCID.String() {
				t.Errorf("unexpected create body without description: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writeVPCPeeringResponse(t, w, mockVPCPeering(new(""), networksdk.VPCPeeringStatusActive))
		case 2:
			if req.Method != http.MethodGet || req.URL.Path != vpcPeeringPath {
				t.Errorf("unexpected read request: %s %s", req.Method, req.URL.Path)
			}
			writeVPCPeeringResponse(t, w, mockVPCPeering(new(""), networksdk.VPCPeeringStatusActive))
		case 3:
			if req.Method != http.MethodPut || req.URL.Path != vpcPeeringPath {
				t.Errorf("unexpected update request: %s %s", req.Method, req.URL.Path)
			}
			if body := decodeJSONObject(t, req.Body); len(body) != 1 || body["description"] != "after" {
				t.Errorf("unexpected update body: %#v", body)
			}
			writeVPCPeeringResponse(t, w, mockVPCPeering(new("after"), networksdk.VPCPeeringStatusActive))
		case 4:
			if req.Method != http.MethodDelete || req.URL.Path != vpcPeeringPath {
				t.Errorf("unexpected delete request: %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected extra request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	peeringSchema := vpcPeeringSchema(t)

	createPlan := tfsdk.Plan{Schema: peeringSchema}
	if diags := createPlan.Set(ctx, vpcPeeringPlanModel(types.StringUnknown())); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: peeringSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var created VPCPeeringResourceModel
	createResponse.State.Get(ctx, &created)
	if created.ID.ValueString() != vpcPeeringTestID.String() || created.Status.ValueString() != "active" ||
		created.Description.ValueString() != "" || created.Description.IsNull() {
		t.Fatalf("unexpected created state: %#v", created)
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var updatePlanModel VPCPeeringResourceModel
	readResponse.State.Get(ctx, &updatePlanModel)
	updatePlanModel.Description = types.StringValue("after")
	updatePlanModel.UpdatedAt = types.StringUnknown()
	updatePlan := tfsdk.Plan{Schema: peeringSchema}
	if diags := updatePlan.Set(ctx, &updatePlanModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: peeringSchema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}
	var updated VPCPeeringResourceModel
	updateResponse.State.Get(ctx, &updated)
	if updated.ID.ValueString() != vpcPeeringTestID.String() || updated.Description.ValueString() != "after" {
		t.Fatalf("unexpected updated state: %#v", updated)
	}

	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if requests != 4 {
		t.Fatalf("got %d SDK requests, want 4", requests)
	}
}

func TestVPCPeeringResourceCreatePendingPeeringWithoutPolling(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if body := decodeJSONObject(t, req.Body); body["description"] != "cross project" {
			t.Errorf("unexpected create body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		writeVPCPeeringResponse(t, w, mockVPCPeering(new("cross project"), networksdk.VPCPeeringStatusPending))
	}))
	defer server.Close()

	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	peeringSchema := vpcPeeringSchema(t)
	plan := tfsdk.Plan{Schema: peeringSchema}
	plan.Set(context.Background(), vpcPeeringPlanModel(types.StringValue("cross project")))
	response := resource.CreateResponse{State: tfsdk.State{Schema: peeringSchema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}

	var state VPCPeeringResourceModel
	response.State.Get(context.Background(), &state)
	if state.Status.ValueString() != "pending" || state.AccepterProjectID.ValueString() != vpcPeeringAccepterProject.String() {
		t.Fatalf("unexpected state: %#v", state)
	}
	if requests != 1 {
		t.Fatalf("got %d requests, want the create request only", requests)
	}
}

func TestVPCPeeringResourceCreateReportsAPIErrorWithoutState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"detail":"The two VPCs have overlapping CIDRs"}`)
	}))
	defer server.Close()

	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	peeringSchema := vpcPeeringSchema(t)
	plan := tfsdk.Plan{Schema: peeringSchema}
	plan.Set(context.Background(), vpcPeeringPlanModel(types.StringNull()))
	response := resource.CreateResponse{State: tfsdk.State{Schema: peeringSchema, Raw: nullAttributesValue(t, peeringSchema)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a create diagnostic")
	}
	if detail := response.Diagnostics[0].Detail(); !strings.Contains(detail, "overlapping CIDRs") {
		t.Fatalf("diagnostic does not carry the API message: %q", detail)
	}
	var id types.String
	response.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if !id.IsNull() {
		t.Fatalf("failed create wrote id %s to state", id)
	}
}

func TestVPCPeeringResourceCreateRejectsInvalidPlan(t *testing.T) {
	t.Parallel()

	peeringSchema := vpcPeeringSchema(t)
	model := vpcPeeringPlanModel(types.StringNull())
	model.RequesterVPC = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
	plan := tfsdk.Plan{Schema: peeringSchema}
	plan.Set(context.Background(), model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: peeringSchema}}
	(&VPCPeeringResource{}).Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an invalid requester VPC ID")
	}
}

func TestVPCPeeringResourceReadRemovesMissingResourceAndDeleteAcceptsNotFound(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	state := vpcPeeringState(t)

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	if !readResponse.State.Raw.IsNull() {
		t.Fatal("expected a missing peering to be removed from state")
	}

	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want read and delete", requests)
	}
}

func TestVPCPeeringResourceReportsAPIErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"internal server error"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := context.Background()
	r := &VPCPeeringResource{client: newVPCPeeringTestClient(t, server), projectID: vpcPeeringProjectID}
	state := vpcPeeringState(t)

	readResponse := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
	if !readResponse.Diagnostics.HasError() {
		t.Error("expected a read diagnostic")
	}

	updateResponse := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan(state), State: state}, &updateResponse)
	if !updateResponse.Diagnostics.HasError() {
		t.Error("expected an update diagnostic")
	}

	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Error("expected a delete diagnostic")
	}
}

func TestVPCPeeringResourceRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	peeringSchema := vpcPeeringSchema(t)
	model := vpcPeeringPlanModel(types.StringValue(""))
	model.ID = types.StringValue("not-a-uuid")
	state := tfsdk.State{Schema: peeringSchema}
	state.Set(ctx, model)
	r := &VPCPeeringResource{}

	readResponse := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResponse)
	updateResponse := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan(state), State: state}, &updateResponse)
	deleteResponse := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
	if !readResponse.Diagnostics.HasError() || !updateResponse.Diagnostics.HasError() || !deleteResponse.Diagnostics.HasError() {
		t.Fatalf("read=%v update=%v delete=%v, want an error from each",
			readResponse.Diagnostics, updateResponse.Diagnostics, deleteResponse.Diagnostics)
	}
}

func TestVPCPeeringResourceImportState(t *testing.T) {
	t.Parallel()

	peeringSchema := vpcPeeringSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: peeringSchema, Raw: nullAttributesValue(t, peeringSchema)}}
	(&VPCPeeringResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: vpcPeeringTestID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var id types.String
	response.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if id.ValueString() != vpcPeeringTestID.String() {
		t.Fatalf("imported id = %q, want %q", id.ValueString(), vpcPeeringTestID)
	}
}

func TestVPCPeeringReadAfterImportFillsState(t *testing.T) {
	t.Parallel()

	peering := mockVPCPeering(new("imported"), networksdk.VPCPeeringStatusActive)
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &VPCPeeringResource{client: client, projectID: vpcPeeringProjectID}
	}, peering.Id.String(), peering)

	var model VPCPeeringResourceModel
	if diags := state.Get(context.Background(), &model); diags.HasError() {
		t.Fatalf("read imported state: %v", diags)
	}
	wantRequester := reference.Value(reference.ID, types.StringValue(vpcPeeringRequesterVPCID.String()))
	wantAccepter := reference.Value(reference.ID, types.StringValue(vpcPeeringAccepterVPCID.String()))
	if !model.RequesterVPC.Equal(wantRequester) || !model.AccepterVPC.Equal(wantAccepter) ||
		model.Description.ValueString() != "imported" || model.Status.ValueString() != "active" ||
		model.Name.ValueString() != peering.Name {
		t.Fatalf("unexpected imported state: %#v", model)
	}
}

func vpcPeeringSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VPCPeeringResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func emptyVPCPeeringResourceModel() *VPCPeeringResourceModel {
	return &VPCPeeringResourceModel{
		RequesterVPC: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		AccepterVPC:  types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}
}

// vpcPeeringPlanModel returns the plan of a new peering between the test VPCs,
// with every computed value unknown.
func vpcPeeringPlanModel(description types.String) VPCPeeringResourceModel {
	return VPCPeeringResourceModel{
		VPCPeeringModel: VPCPeeringModel{
			ID:                types.StringUnknown(),
			Name:              types.StringUnknown(),
			Description:       description,
			Status:            types.StringUnknown(),
			VPCPeeringType:    types.StringUnknown(),
			AccepterProjectID: types.StringUnknown(),
			CreatedAt:         types.StringUnknown(),
			UpdatedAt:         types.StringUnknown(),
		},
		RequesterVPC: reference.Value(reference.ID, types.StringValue(vpcPeeringRequesterVPCID.String())),
		AccepterVPC:  reference.Value(reference.ID, types.StringValue(vpcPeeringAccepterVPCID.String())),
	}
}

func vpcPeeringState(t *testing.T) tfsdk.State {
	t.Helper()
	model := vpcPeeringPlanModel(types.StringValue("before"))
	populateVPCPeeringResourceState(mockVPCPeering(new("before"), networksdk.VPCPeeringStatusActive), &model)
	state := tfsdk.State{Schema: vpcPeeringSchema(t)}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func mockVPCPeering(description *string, status networksdk.VPCPeeringStatus) *networksdk.VPCPeeringSchema {
	region := networksdk.NestedRegionSchema{Id: core.UUID{5}, Name: "vn-central-1"}
	return &networksdk.VPCPeeringSchema{
		Id:          vpcPeeringTestID,
		Name:        "peer-" + vpcPeeringTestID.String(),
		Description: core.NullableFromPtr(description),
		RequesterVpc: networksdk.NestedPeeredVPCSchema{
			Id:      vpcPeeringRequesterVPCID,
			Name:    "requester",
			Cidr:    "10.51.0.0/16",
			Project: networksdk.NestedProjectSchema{Id: vpcPeeringProjectID},
			Region:  region,
		},
		AccepterVpc: networksdk.NestedPeeredVPCSchema{
			Id:      vpcPeeringAccepterVPCID,
			Name:    "accepter",
			Cidr:    "10.52.0.0/16",
			Project: networksdk.NestedProjectSchema{Id: vpcPeeringAccepterProject},
			Region:  region,
		},
		Status:         status,
		VpcPeeringType: networksdk.VPCPeeringTypeIntraRegion,
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func newVPCPeeringTestClient(t *testing.T, server *httptest.Server) *networksdk.Client {
	t.Helper()
	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	return client
}

func writeVPCPeeringResponse(t *testing.T, w http.ResponseWriter, peering *networksdk.VPCPeeringSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(peering); err != nil {
		t.Errorf("encode VPC peering response: %v", err)
	}
}

func decodeJSONObject(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.NewDecoder(body).Decode(&object); err != nil {
		t.Errorf("decode request body: %v", err)
	}
	return object
}
