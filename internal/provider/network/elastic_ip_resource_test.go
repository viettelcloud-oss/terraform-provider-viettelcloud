package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var elasticIPResourceTestUUID = core.UUID{1}

type elasticIPResourceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f elasticIPResourceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestElasticIPModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	var resourceSchema resource.SchemaResponse
	(&ElasticIPResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceSchema)
	plan := tfsdk.Plan{Schema: resourceSchema.Schema}
	if diags := plan.Set(context.Background(), &ElasticIPResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestElasticIPSchemaMutability(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&ElasticIPResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)

	if region := requiredReferenceAttribute(t, response.Schema, "region", reference.Name); len(region.PlanModifiers) != 1 {
		t.Fatalf("region must be create-only, got %#v", region.PlanModifiers)
	}
	description, ok := response.Schema.Attributes["description"].(schema.StringAttribute)
	if !ok || !description.Optional || !description.Computed || len(description.PlanModifiers) != 0 {
		t.Fatalf("description must be optional, computed, and update-ready, got %#v", response.Schema.Attributes["description"])
	}
	for _, name := range []string{"enable_ipv4", "enable_ipv6"} {
		attribute, ok := response.Schema.Attributes[name].(schema.BoolAttribute)
		if !ok || !attribute.Optional || !attribute.Computed || len(attribute.PlanModifiers) != 2 {
			t.Fatalf("%s must be optional, computed, and create-only, got %#v", name, response.Schema.Attributes[name])
		}
	}
	createdAt, ok := response.Schema.Attributes["created_at"].(schema.StringAttribute)
	if !ok || createdAt.Optional || !createdAt.Computed || len(createdAt.PlanModifiers) != 1 {
		t.Fatalf("created_at must be computed and stable, got %#v", response.Schema.Attributes["created_at"])
	}
}

// Any planned change marks every Computed attribute that is null in
// configuration as unknown. The address-family modifiers must resolve that
// unknown back to prior state so an unrelated update, such as a new
// description, is not turned into a replacement.
func TestElasticIPAddressFamilyPlanModification(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&ElasticIPResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)

	tests := []struct {
		name            string
		config          types.Bool
		plan            types.Bool
		state           types.Bool
		wantPlan        types.Bool
		wantReplacement bool
	}{
		{
			name:     "unconfigured value keeps prior state without replacement",
			config:   types.BoolNull(),
			plan:     types.BoolUnknown(),
			state:    types.BoolValue(true),
			wantPlan: types.BoolValue(true),
		},
		{
			name:     "configured unchanged value updates in place",
			config:   types.BoolValue(true),
			plan:     types.BoolValue(true),
			state:    types.BoolValue(true),
			wantPlan: types.BoolValue(true),
		},
		{
			name:            "configured change requires replacement",
			config:          types.BoolValue(true),
			plan:            types.BoolValue(true),
			state:           types.BoolValue(false),
			wantPlan:        types.BoolValue(true),
			wantReplacement: true,
		},
	}

	for _, name := range []string{"enable_ipv4", "enable_ipv6"} {
		attribute, ok := response.Schema.Attributes[name].(schema.BoolAttribute)
		if !ok {
			t.Fatalf("%s is not a bool attribute", name)
		}

		for _, tt := range tests {
			t.Run(name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				request := planmodifier.BoolRequest{
					State:       elasticIPModifierState(t, response.Schema),
					Plan:        elasticIPModifierPlan(t, response.Schema),
					ConfigValue: tt.config,
					PlanValue:   tt.plan,
					StateValue:  tt.state,
				}

				var modifierResponse planmodifier.BoolResponse
				modifierResponse.PlanValue = tt.plan
				for _, modifier := range attribute.PlanModifiers {
					request.PlanValue = modifierResponse.PlanValue
					modifier.PlanModifyBool(context.Background(), request, &modifierResponse)
				}

				if !modifierResponse.PlanValue.Equal(tt.wantPlan) {
					t.Errorf("planned %s = %v, want %v", name, modifierResponse.PlanValue, tt.wantPlan)
				}
				if modifierResponse.RequiresReplace != tt.wantReplacement {
					t.Errorf("replacement of %s = %t, want %t", name, modifierResponse.RequiresReplace, tt.wantReplacement)
				}
			})
		}
	}
}

func elasticIPModifierState(t *testing.T, resourceSchema schema.Schema) tfsdk.State {
	t.Helper()

	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &ElasticIPResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	return state
}

func elasticIPModifierPlan(t *testing.T, resourceSchema schema.Schema) tfsdk.Plan {
	t.Helper()

	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &ElasticIPResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
	}
	return plan
}

func TestBuildElasticIPCreateBodyResolvesRegionName(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{7}
	body, diags := buildElasticIPCreateBody(context.Background(), ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			Description: types.StringValue("Managed by Terraform"),
			EnableIPv4:  types.BoolValue(true),
			EnableIPv6:  types.BoolValue(false),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}, func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		if filter.Name == nil || *filter.Name != "vn-central" {
			t.Fatalf("unexpected region filter %#v", filter.Name)
		}
		return projectsdk.ProjectRegionSchema{
			Region: projectsdk.NestedRegionSchema{Id: regionID, Name: *filter.Name},
		}, nil
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.RegionId != regionID || body.Description == nil || *body.Description != "Managed by Terraform" ||
		body.EnableIpv4 == nil || !*body.EnableIpv4 ||
		body.EnableIpv6 == nil || *body.EnableIpv6 {
		t.Fatalf("unexpected create body: %#v", body)
	}
}

func TestBuildElasticIPCreateBodyDescriptionPresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description types.String
		wantSet     bool
		want        string
	}{
		{name: "null is omitted", description: types.StringNull()},
		{name: "unknown is omitted", description: types.StringUnknown()},
		{name: "empty is sent", description: types.StringValue(""), wantSet: true},
		{name: "configured is sent", description: types.StringValue("Managed by Terraform"), wantSet: true, want: "Managed by Terraform"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			body, diags := buildElasticIPCreateBody(
				context.Background(),
				ElasticIPResourceModel{
					ElasticIPModel: ElasticIPModel{
						Description: test.description,
					},
					Region: reference.Value(reference.Name, types.StringValue("vn-central")),
				},
				func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
					return projectsdk.ProjectRegionSchema{
						Region: projectsdk.NestedRegionSchema{Id: elasticIPResourceTestUUID, Name: *filter.Name},
					}, nil
				},
			)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if !test.wantSet && body.Description != nil {
				t.Fatalf("expected description to be omitted, got %q", *body.Description)
			}
			if test.wantSet && (body.Description == nil || *body.Description != test.want) {
				t.Fatalf("expected description %q, got %#v", test.want, body.Description)
			}
		})
	}
}

func TestBuildElasticIPCreateBodyRejectsMissingOrUnresolvedRegion(t *testing.T) {
	t.Parallel()

	_, diags := buildElasticIPCreateBody(
		context.Background(),
		ElasticIPResourceModel{Region: reference.Value(reference.Name, types.StringValue(" "))},
		func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
			t.Fatal("resolver must not be called for an empty region")
			return projectsdk.ProjectRegionSchema{}, nil
		},
	)
	if !diags.HasError() {
		t.Fatal("expected missing region diagnostic")
	}

	expected := errors.New("region not found")
	_, diags = buildElasticIPCreateBody(
		context.Background(),
		ElasticIPResourceModel{Region: reference.Value(reference.Name, types.StringValue("vn-central"))},
		func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
			return projectsdk.ProjectRegionSchema{}, expected
		},
	)
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), expected.Error()) {
		t.Fatalf("expected resolver diagnostic, got %v", diags)
	}
}

func TestPopulateElasticIPState(t *testing.T) {
	t.Parallel()

	ipv4 := "203.0.113.10"
	ipv6 := "2001:db8::10"
	description := "Managed by Terraform"
	status := networksdk.ElasticIPStatusActive
	eip := &networksdk.ElasticIPDetailSchema{
		Id:          elasticIPResourceTestUUID,
		Description: &description,
		EnableIpv4:  true,
		EnableIpv6:  true,
		IpAddress:   &ipv4,
		Ipv6Address: &ipv6,
		Status:      &status,
		Region: networksdk.NestedRegionSchema{
			Id:   elasticIPResourceTestUUID,
			Name: "vn-central",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	var state ElasticIPResourceModel
	populateElasticIPResourceState(eip, &state)

	if state.ID.ValueString() != eip.Id.String() {
		t.Errorf("expected ID %s, got %s", eip.Id, state.ID.ValueString())
	}
	if reference.Get(state.Region, reference.Name).ValueString() != eip.Region.Name {
		t.Errorf("expected region %s, got %s", eip.Region.Name, reference.Get(state.Region, reference.Name).ValueString())
	}
	if state.Description.ValueString() != description {
		t.Errorf("expected description %q, got %q", description, state.Description.ValueString())
	}
	if state.IPAddress.ValueString() != ipv4 || state.IPv6Address.ValueString() != ipv6 {
		t.Errorf("unexpected IP addresses: %s, %s", state.IPAddress.ValueString(), state.IPv6Address.ValueString())
	}
	if !state.EnableIPv4.ValueBool() || !state.EnableIPv6.ValueBool() {
		t.Error("expected IPv4 and IPv6 to be enabled")
	}
}

func TestPopulateElasticIPResourceStateKeepsConfiguredRegion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		configured types.String
		want       string
	}{
		{name: "untrimmed region is kept", configured: types.StringValue("  vn-central  "), want: "  vn-central  "},
		{name: "unknown region takes the backend value", configured: types.StringUnknown(), want: "vn-central"},
		{name: "imported null region takes the backend value", configured: types.StringNull(), want: "vn-central"},
		{name: "another region takes the backend value", configured: types.StringValue("vn-north"), want: "vn-central"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			eip := &networksdk.ElasticIPDetailSchema{
				Id:     elasticIPResourceTestUUID,
				Region: networksdk.NestedRegionSchema{Id: core.UUID{9}, Name: "vn-central"},
			}
			state := ElasticIPResourceModel{Region: reference.Value(reference.Name, test.configured)}
			populateElasticIPResourceState(eip, &state)
			if reference.Get(state.Region, reference.Name).ValueString() != test.want {
				t.Fatalf("expected region %q, got %q", test.want, reference.Get(state.Region, reference.Name).ValueString())
			}
		})
	}
}

func TestPopulateElasticIPStateNullAddresses(t *testing.T) {
	t.Parallel()

	eip := &networksdk.ElasticIPDetailSchema{
		Id:     elasticIPResourceTestUUID,
		Region: networksdk.NestedRegionSchema{Id: elasticIPResourceTestUUID, Name: "vn-central"},
	}
	var state ElasticIPResourceModel
	populateElasticIPResourceState(eip, &state)

	if !state.Description.IsNull() || !state.IPAddress.IsNull() || !state.IPv6Address.IsNull() || !state.Status.IsNull() {
		t.Error("expected absent API values to map to null")
	}
}

func TestPopulateElasticIPPendingState(t *testing.T) {
	t.Parallel()

	state := ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID:          types.StringUnknown(),
			Description: types.StringValue("Managed by Terraform"),
			EnableIPv4:  types.BoolValue(true),
			EnableIPv6:  types.BoolUnknown(),
			IPAddress:   types.StringUnknown(),
			IPv6Address: types.StringUnknown(),
			Status:      types.StringUnknown(),
			CreatedAt:   types.StringUnknown(),
			UpdatedAt:   types.StringUnknown(),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}
	populateElasticIPPendingState(&state, elasticIPResourceTestUUID)

	if state.ID.ValueString() != elasticIPResourceTestUUID.String() {
		t.Fatalf("expected preserved ID %s, got %#v", elasticIPResourceTestUUID, state.ID)
	}
	if state.Description.ValueString() != "Managed by Terraform" ||
		!state.EnableIPv4.ValueBool() ||
		reference.Get(state.Region, reference.Name).ValueString() != "vn-central" {
		t.Fatalf("expected configured values to be preserved, got %#v", state)
	}
	if !state.EnableIPv6.IsNull() ||
		!state.IPAddress.IsNull() ||
		!state.IPv6Address.IsNull() ||
		!state.Status.IsNull() ||
		!state.CreatedAt.IsNull() ||
		!state.UpdatedAt.IsNull() {
		t.Fatalf("expected unknown pending values to become null, got %#v", state)
	}
}

func TestElasticIPCreatePreservesIDWhenPollingFails(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	regionID := core.UUID{2}
	eipID := core.UUID{3}
	httpClient := &http.Client{Transport: elasticIPResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		response := &http.Response{
			Header:  http.Header{"Content-Type": []string{"application/json"}},
			Request: req,
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+projectID.String()+"/regions/":
			if req.URL.Query().Get("name") != "vn-central" {
				t.Fatalf("unexpected region query %q", req.URL.RawQuery)
			}
			response.StatusCode = http.StatusOK
			response.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"count":1,"next":null,"previous":null,"results":[{"region":{"id":%q,"name":"vn-central","description":""},"services":[]}]}`,
				regionID.String(),
			)))
		case req.Method == http.MethodPost && req.URL.Path == "/v2/network/elastic-ips/":
			if req.Header.Get("Project-ID") != projectID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			var body struct {
				Description *string `json:"description"`
				RegionID    string  `json:"region_id"`
				EnableIPv4  *bool   `json:"enable_ipv4"`
				EnableIPv6  *bool   `json:"enable_ipv6"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if body.Description != nil || body.RegionID != regionID.String() || body.EnableIPv4 != nil || body.EnableIPv6 != nil {
				t.Fatalf("unexpected create request: %#v", body)
			}
			response.StatusCode = http.StatusCreated
			response.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"id":%q,"display_name":"eip","enable_ipv4":true,"enable_ipv6":false,"ip_address":null,"status":"down","project":{"id":%q,"name":"project","slug":"project"},"region":{"id":%q,"name":"vn-central","description":""},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
				eipID.String(), projectID.String(), regionID.String(),
			)))
		case req.Method == http.MethodGet && req.URL.Path == "/v2/network/elastic-ips/"+eipID.String()+"/":
			response.StatusCode = http.StatusInternalServerError
			response.Body = io.NopCloser(strings.NewReader(`{"detail":"poll failed"}`))
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		return response, nil
	})}

	networkClient, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	resourceUnderTest := &ElasticIPResource{client: networkClient, project: projectClient, projectID: projectID}

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID:          types.StringUnknown(),
			Description: types.StringUnknown(),
			EnableIPv4:  types.BoolUnknown(),
			EnableIPv6:  types.BoolUnknown(),
			IPAddress:   types.StringUnknown(),
			IPv6Address: types.StringUnknown(),
			Status:      types.StringUnknown(),
			CreatedAt:   types.StringUnknown(),
			UpdatedAt:   types.StringUnknown(),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected polling error")
	}
	if !strings.Contains(response.Diagnostics[0].Detail(), eipID.String()) ||
		!strings.Contains(response.Diagnostics[0].Detail(), "preserved in Terraform state") {
		t.Fatalf("expected preserved-ID diagnostic, got %v", response.Diagnostics)
	}
	if !response.State.Raw.IsFullyKnown() {
		t.Fatal("partial state must not contain unknown values")
	}
	var state ElasticIPResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read partial state: %v", diags)
	}
	if state.ID.ValueString() != eipID.String() {
		t.Fatalf("expected preserved ID %s, got %s", eipID, state.ID.ValueString())
	}
	if !state.EnableIPv4.IsNull() || !state.EnableIPv6.IsNull() || !state.IPAddress.IsNull() {
		t.Fatalf("unexpected partial state: %#v", state)
	}
	if !state.Description.IsNull() {
		t.Fatalf("expected omitted description to remain null, got %#v", state.Description)
	}
}

func TestElasticIPCreateUsesResolvedRegionAndRefreshesState(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	regionID := core.UUID{2}
	eipID := core.UUID{3}
	getCalls := 0
	httpClient := &http.Client{Transport: elasticIPResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		response := &http.Response{
			Header:  http.Header{"Content-Type": []string{"application/json"}},
			Request: req,
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+projectID.String()+"/regions/":
			response.StatusCode = http.StatusOK
			response.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"count":1,"next":null,"previous":null,"results":[{"region":{"id":%q,"name":"vn-central","description":""},"services":[]}]}`,
				regionID.String(),
			)))
		case req.Method == http.MethodPost && req.URL.Path == "/v2/network/elastic-ips/":
			if req.Header.Get("Project-ID") != projectID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			var body networksdk.ElasticIPCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if body.Description == nil || *body.Description != "Managed by Terraform" ||
				body.RegionId != regionID || body.EnableIpv4 == nil || !*body.EnableIpv4 ||
				body.EnableIpv6 == nil || *body.EnableIpv6 {
				t.Fatalf("unexpected create request: %#v", body)
			}
			response.StatusCode = http.StatusCreated
			response.Body = io.NopCloser(strings.NewReader(elasticIPResponseJSON(eipID, projectID, regionID)))
		case req.Method == http.MethodGet && req.URL.Path == "/v2/network/elastic-ips/"+eipID.String()+"/":
			if req.Header.Get("Project-ID") != projectID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			getCalls++
			response.StatusCode = http.StatusOK
			response.Body = io.NopCloser(strings.NewReader(elasticIPResponseJSON(eipID, projectID, regionID)))
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		return response, nil
	})}

	networkClient, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	resourceUnderTest := &ElasticIPResource{client: networkClient, project: projectClient, projectID: projectID}

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID:          types.StringUnknown(),
			Description: types.StringValue("Managed by Terraform"),
			EnableIPv4:  types.BoolValue(true),
			EnableIPv6:  types.BoolValue(false),
			IPAddress:   types.StringUnknown(),
			IPv6Address: types.StringUnknown(),
			Status:      types.StringUnknown(),
			CreatedAt:   types.StringUnknown(),
			UpdatedAt:   types.StringUnknown(),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected create diagnostics: %v", response.Diagnostics)
	}
	if getCalls != 1 {
		t.Fatalf("expected the waiter poll to double as the final refresh, got %d requests", getCalls)
	}

	var state ElasticIPResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if state.ID.ValueString() != eipID.String() || state.Description.ValueString() != "Managed by Terraform" ||
		reference.Get(state.Region, reference.Name).ValueString() != "vn-central" ||
		state.IPAddress.ValueString() != "203.0.113.10" {
		t.Fatalf("unexpected create state: %#v", state)
	}
}

func TestElasticIPReadDeleteResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		method          string
		status          int
		expectedSummary string
		removed         bool
	}{
		{name: "read removes missing resource", method: http.MethodGet, status: http.StatusNotFound, removed: true},
		{name: "read reports API error", method: http.MethodGet, status: http.StatusInternalServerError, expectedSummary: "Error reading Elastic IP"},
		{name: "delete accepts missing resource", method: http.MethodDelete, status: http.StatusNotFound},
		{name: "delete reports API error", method: http.MethodDelete, status: http.StatusInternalServerError, expectedSummary: "Error deleting Elastic IP"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			httpClient := &http.Client{Transport: elasticIPResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != test.method || req.URL.Path != "/v2/network/elastic-ips/"+elasticIPResourceTestUUID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Project-ID") != elasticIPResourceTestUUID.String() {
					t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
				}
				return &http.Response{
					StatusCode: test.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"detail":"backend response"}`)),
					Request:    req,
				}, nil
			})}
			client, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatalf("create network client: %v", err)
			}
			resourceUnderTest := &ElasticIPResource{client: client, projectID: elasticIPResourceTestUUID}
			state := elasticIPResourceTestState(t, resourceUnderTest)

			if test.method == http.MethodGet {
				response := resource.ReadResponse{State: state}
				resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
				if test.removed && !response.State.Raw.IsNull() {
					t.Fatalf("expected missing Elastic IP to be removed, got %s", response.State.Raw)
				}
				assertElasticIPDiagnostic(t, response.Diagnostics, test.expectedSummary)
				return
			}

			response := resource.DeleteResponse{State: state}
			resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			assertElasticIPDiagnostic(t, response.Diagnostics, test.expectedSummary)
		})
	}
}

func TestBuildElasticIPUpdateBody(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		plan        types.String
		state       types.String
		wantChanged bool
		want        string
	}{
		{name: "unchanged description is not sent", plan: types.StringValue("kept"), state: types.StringValue("kept")},
		{name: "omitted description is not sent", plan: types.StringNull(), state: types.StringValue("kept")},
		{name: "unknown description is not sent", plan: types.StringUnknown(), state: types.StringValue("kept")},
		{
			name:        "changed description is sent",
			plan:        types.StringValue("updated"),
			state:       types.StringValue("kept"),
			wantChanged: true,
			want:        "updated",
		},
		{
			name:        "empty description clears the value",
			plan:        types.StringValue(""),
			state:       types.StringValue("kept"),
			wantChanged: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			body, changed := buildElasticIPUpdateBody(
				ElasticIPResourceModel{ElasticIPModel: ElasticIPModel{Description: test.plan}},
				ElasticIPResourceModel{ElasticIPModel: ElasticIPModel{Description: test.state}},
			)
			if changed != test.wantChanged {
				t.Fatalf("expected changed=%t, got %t", test.wantChanged, changed)
			}
			if !test.wantChanged {
				if body.Description != nil {
					t.Fatalf("expected description to be omitted, got %q", *body.Description)
				}
				return
			}
			if body.Description == nil || *body.Description != test.want {
				t.Fatalf("expected description %q, got %#v", test.want, body.Description)
			}
		})
	}
}

func TestElasticIPUpdatePatchesDescription(t *testing.T) {
	t.Parallel()

	resourceUnderTest, calls := elasticIPStubResource(t, func(req *http.Request, _ int) (int, string) {
		if req.Method != http.MethodPatch {
			t.Fatalf("expected a PATCH request, got %s", req.Method)
		}
		var body struct {
			Description *string `json:"description"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode update body: %v", err)
		}
		if body.Description == nil || *body.Description != "updated" {
			t.Fatalf("unexpected update body: %#v", body.Description)
		}
		return http.StatusOK, strings.Replace(
			elasticIPResponseJSON(elasticIPResourceTestUUID, elasticIPResourceTestUUID, elasticIPResourceTestUUID),
			`"description":"Managed by Terraform"`,
			`"description":"updated"`,
			1,
		)
	})

	newState, diagnostics := elasticIPUpdate(t, resourceUnderTest,
		types.StringValue("updated"), types.StringValue("Managed by Terraform"))
	if diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", diagnostics)
	}
	if calls() != 1 {
		t.Fatalf("expected a single update request, got %d", calls())
	}
	if newState.Description.ValueString() != "updated" || reference.Get(newState.Region, reference.Name).ValueString() != "vn-central" {
		t.Fatalf("unexpected state after update: %#v", newState)
	}
}

func TestElasticIPUpdateWithoutChangesReadsElasticIP(t *testing.T) {
	t.Parallel()

	resourceUnderTest, calls := elasticIPStubResource(t, func(req *http.Request, _ int) (int, string) {
		if req.Method != http.MethodGet {
			t.Fatalf("expected a GET request on an unchanged update, got %s", req.Method)
		}
		return http.StatusOK, elasticIPResponseJSON(elasticIPResourceTestUUID, elasticIPResourceTestUUID, elasticIPResourceTestUUID)
	})

	newState, diagnostics := elasticIPUpdate(t, resourceUnderTest,
		types.StringNull(), types.StringValue("Managed by Terraform"))
	if diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", diagnostics)
	}
	if calls() != 1 {
		t.Fatalf("expected a single read request, got %d", calls())
	}
	if newState.Description.ValueString() != "Managed by Terraform" {
		t.Fatalf("expected the backend description to be kept, got %#v", newState.Description)
	}
}

func TestElasticIPUpdateRejectsAnotherElasticIP(t *testing.T) {
	t.Parallel()

	other := core.UUID{9}
	resourceUnderTest, _ := elasticIPStubResource(t, func(*http.Request, int) (int, string) {
		return http.StatusOK, elasticIPResponseJSON(other, elasticIPResourceTestUUID, elasticIPResourceTestUUID)
	})

	_, diagnostics := elasticIPUpdate(t, resourceUnderTest,
		types.StringValue("updated"), types.StringValue("Managed by Terraform"))
	if !diagnostics.HasError() || diagnostics[0].Summary() != "Error updating Elastic IP" {
		t.Fatalf("expected an update diagnostic, got %v", diagnostics)
	}
	if !strings.Contains(diagnostics[0].Detail(), other.String()) {
		t.Fatalf("expected the returned Elastic IP in the detail, got %q", diagnostics[0].Detail())
	}
}

func TestElasticIPUpdateRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	resourceUnderTest, _ := elasticIPStubResource(t, func(req *http.Request, _ int) (int, string) {
		t.Fatalf("no request expected, got %s %s", req.Method, req.URL.Path)
		return 0, ""
	})

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID: types.StringValue("not-a-uuid"),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema, Raw: state.Raw}

	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for an unparsable state ID")
	}
}

// The waiter tests run inside a synctest bubble so the production timeout and
// poll interval are exercised on a fake clock instead of shortened durations.
func TestElasticIPWaitUntilReady(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		resourceUnderTest, calls := elasticIPStubResource(t, func(_ *http.Request, call int) (int, string) {
			if call == 1 {
				return http.StatusOK, elasticIPPollJSON("203.0.113.10", "")
			}
			return http.StatusOK, elasticIPPollJSON("203.0.113.10", "2001:db8::10")
		})

		start := time.Now()
		eip, err := resourceUnderTest.waitUntilReady(
			context.Background(),
			elasticIPResourceTestUUID,
			true,
			true,
			elasticIPReadyTimeout,
			elasticIPReadyPollInterval,
		)
		if err != nil || calls() != 2 {
			t.Fatalf("expected waiter success after two polls, calls=%d error=%v", calls(), err)
		}
		if waited := time.Since(start); waited != elasticIPReadyPollInterval {
			t.Fatalf("expected one poll interval between polls, waited %s", waited)
		}
		if eip == nil || eip.Ipv6Address == nil || *eip.Ipv6Address != "2001:db8::10" {
			t.Fatalf("expected the settled Elastic IP to be returned, got %#v", eip)
		}
	})
}

func TestElasticIPWaitUntilReadyReturnsPollAndContextErrors(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		resourceUnderTest, _ := elasticIPStubResource(t, func(*http.Request, int) (int, string) {
			return http.StatusInternalServerError, `{"detail":"backend unavailable"}`
		})
		_, err := resourceUnderTest.waitUntilReady(
			context.Background(),
			elasticIPResourceTestUUID,
			true,
			false,
			elasticIPReadyTimeout,
			elasticIPReadyPollInterval,
		)
		if err == nil || !strings.Contains(err.Error(), "elastic IP "+elasticIPResourceTestUUID.String()) {
			t.Fatalf("expected wrapped poll error, got %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		resourceUnderTest, _ = elasticIPStubResource(t, func(*http.Request, int) (int, string) {
			return http.StatusOK, elasticIPPollJSON("203.0.113.10", "2001:db8::10")
		})
		_, err = resourceUnderTest.waitUntilReady(
			ctx,
			elasticIPResourceTestUUID,
			true,
			false,
			elasticIPReadyTimeout,
			elasticIPReadyPollInterval,
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
		if errors.Is(err, wait.ErrTimeout) {
			t.Fatalf("an interrupted apply must not be reported as a timeout, got %v", err)
		}
	})
}

func TestElasticIPWaitUntilReadyRequiresRequestedAddressFamilies(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		ipv4        bool
		ipv6        bool
		ipv4Address string
		ipv6Address string
		wantReady   bool
		wantReason  string
	}{
		{name: "no address family requested", wantReady: true},
		{name: "requested IPv4 assigned", ipv4: true, ipv4Address: "203.0.113.10", wantReady: true},
		{name: "requested IPv4 still empty", ipv4: true, wantReason: "no IPv4 assigned yet"},
		{name: "requested IPv6 still empty", ipv4: true, ipv6: true, ipv4Address: "203.0.113.10", wantReason: "no IPv6 assigned yet"},
		{
			name:        "both requested families assigned",
			ipv4:        true,
			ipv6:        true,
			ipv4Address: "203.0.113.10",
			ipv6Address: "2001:db8::10",
			wantReady:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				resourceUnderTest, _ := elasticIPStubResource(t, func(*http.Request, int) (int, string) {
					return http.StatusOK, elasticIPPollJSON(test.ipv4Address, test.ipv6Address)
				})

				start := time.Now()
				eip, err := resourceUnderTest.waitUntilReady(
					context.Background(),
					elasticIPResourceTestUUID,
					test.ipv4,
					test.ipv6,
					elasticIPReadyTimeout,
					elasticIPReadyPollInterval,
				)
				waited := time.Since(start)
				if test.wantReady {
					if err != nil {
						t.Fatalf("expected the Elastic IP to be ready, got %v", err)
					}
					if eip == nil {
						t.Fatal("expected the settled Elastic IP to be returned")
					}
					if waited != 0 {
						t.Fatalf("expected the first poll to satisfy the waiter, waited %s", waited)
					}
					return
				}
				if !errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), test.wantReason) {
					t.Fatalf("expected a timeout error, got %v", err)
				}
				if waited != elasticIPReadyTimeout {
					t.Fatalf("expected the waiter to give up after %s, waited %s", elasticIPReadyTimeout, waited)
				}
			})
		})
	}
}

func TestElasticIPImportState(t *testing.T) {
	t.Parallel()

	resourceUnderTest := &ElasticIPResource{}
	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	if diags := response.State.Set(context.Background(), &ElasticIPResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	resourceUnderTest.ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: elasticIPResourceTestUUID.String()},
		&response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", response.Diagnostics)
	}

	var state ElasticIPResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read imported state: %v", diags)
	}
	if state.ID.ValueString() != elasticIPResourceTestUUID.String() {
		t.Fatalf("expected imported ID %s, got %s", elasticIPResourceTestUUID, state.ID.ValueString())
	}
}

func TestElasticIPReadAfterImportFillsRegionReference(t *testing.T) {
	t.Parallel()

	eip := &networksdk.ElasticIPDetailSchema{
		Id:     elasticIPResourceTestUUID,
		Region: networksdk.NestedRegionSchema{Id: core.UUID{9}, Name: "vn-central"},
	}
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &ElasticIPResource{client: client, projectID: elasticIPResourceTestUUID}
	}, eip.Id.String(), eip)

	var region types.Object
	if diags := state.GetAttribute(context.Background(), path.Root("region"), &region); diags.HasError() {
		t.Fatalf("read region: %v", diags)
	}
	if want := reference.Value(reference.Name, types.StringValue("vn-central")); !region.Equal(want) {
		t.Fatalf("imported region = %s, want %s", region, want)
	}
}

// elasticIPStubResource answers every SDK call with respond and counts the
// calls, so waiters and updates run against the real client.
func elasticIPStubResource(t *testing.T, respond func(req *http.Request, call int) (int, string)) (*ElasticIPResource, func() int) {
	t.Helper()

	calls := 0
	httpClient := &http.Client{Transport: elasticIPResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v2/network/elastic-ips/"+elasticIPResourceTestUUID.String()+"/" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		calls++
		status, body := respond(req, calls)
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	client, err := networksdk.NewClient(
		"https://network.test",
		networksdk.WithHTTPClient(httpClient),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	return &ElasticIPResource{client: client, projectID: elasticIPResourceTestUUID},
		func() int { return calls }
}

func elasticIPUpdate(
	t *testing.T,
	resourceUnderTest *ElasticIPResource,
	planDescription types.String,
	stateDescription types.String,
) (ElasticIPResourceModel, diag.Diagnostics) {
	t.Helper()

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)

	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID:          types.StringValue(elasticIPResourceTestUUID.String()),
			Description: stateDescription,
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	planState := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := planState.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID:          types.StringValue(elasticIPResourceTestUUID.String()),
			Description: planDescription,
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planState.Raw, Schema: schemaResponse.Schema},
		State: state,
	}, &response)
	if response.Diagnostics.HasError() {
		return ElasticIPResourceModel{}, response.Diagnostics
	}

	var newState ElasticIPResourceModel
	if diags := response.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("read updated state: %v", diags)
	}
	return newState, response.Diagnostics
}

func elasticIPPollJSON(ipv4Address, ipv6Address string) string {
	return fmt.Sprintf(
		`{"id":%q,"display_name":"eip","enable_ipv4":true,"enable_ipv6":true,`+
			`"ip_address":%q,"ipv6_address":%q,`+
			`"project":{"id":%q,"name":"project","slug":"project"},`+
			`"region":{"id":%q,"name":"vn-central","description":""},`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		elasticIPResourceTestUUID.String(), ipv4Address, ipv6Address,
		elasticIPResourceTestUUID.String(), elasticIPResourceTestUUID.String(),
	)
}

func elasticIPResponseJSON(eipID, projectID, regionID core.UUID) string {
	return fmt.Sprintf(
		`{"id":%q,"description":"Managed by Terraform","display_name":"eip","enable_ipv4":true,"enable_ipv6":false,"ip_address":"203.0.113.10","status":"active","project":{"id":%q,"name":"project","slug":"project"},"region":{"id":%q,"name":"vn-central","description":""},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`,
		eipID.String(), projectID.String(), regionID.String(),
	)
}

func elasticIPResourceTestState(t *testing.T, resourceUnderTest *ElasticIPResource) tfsdk.State {
	t.Helper()

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &ElasticIPResourceModel{
		ElasticIPModel: ElasticIPModel{
			ID: types.StringValue(elasticIPResourceTestUUID.String()),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set resource state: %v", diags)
	}
	return state
}

func assertElasticIPDiagnostic(t *testing.T, diagnostics diag.Diagnostics, expectedSummary string) {
	t.Helper()

	if expectedSummary == "" {
		if diagnostics.HasError() {
			t.Fatal("unexpected diagnostics")
		}
		return
	}
	if !diagnostics.HasError() || len(diagnostics) == 0 {
		t.Fatalf("expected diagnostic %q", expectedSummary)
	}
	if diagnostics[0].Summary() != expectedSummary {
		t.Fatalf("expected diagnostic %q, got %q", expectedSummary, diagnostics[0].Summary())
	}
}
