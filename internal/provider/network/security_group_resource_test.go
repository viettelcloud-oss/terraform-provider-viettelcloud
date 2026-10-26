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
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var securityGroupResourceTestUUID = core.UUID{1}

type securityGroupResourceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f securityGroupResourceRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSecurityGroupModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	var resourceSchema resource.SchemaResponse
	(&SecurityGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceSchema)
	plan := tfsdk.Plan{Schema: resourceSchema.Schema}
	if diags := plan.Set(context.Background(), &SecurityGroupResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestSecurityGroupSchemaMutability(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&SecurityGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)

	name, ok := response.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok || !name.Required || name.Computed || len(name.PlanModifiers) != 0 {
		t.Fatalf("name must be required and updatable in place, got %#v", response.Schema.Attributes["name"])
	}

	description, ok := response.Schema.Attributes["description"].(schema.StringAttribute)
	if !ok || !description.Optional || !description.Computed || len(description.PlanModifiers) != 0 {
		t.Fatalf("description must be optional, computed, and updatable in place, got %#v", response.Schema.Attributes["description"])
	}

	if region := requiredReferenceAttribute(t, response.Schema, "region", reference.Name); len(region.PlanModifiers) != 1 {
		t.Fatalf("region must be create-only, got %#v", region.PlanModifiers)
	}

	for _, attributeName := range []string{"id", "project_id", "created_at"} {
		attribute, ok := response.Schema.Attributes[attributeName].(schema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must be computed and stable, got %#v", attributeName, response.Schema.Attributes[attributeName])
		}
	}

	for _, attributeName := range []string{"display_name", "region_id", "updated_at"} {
		attribute, ok := response.Schema.Attributes[attributeName].(schema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || len(attribute.PlanModifiers) != 0 {
			t.Fatalf("%s must be computed and refreshed, got %#v", attributeName, response.Schema.Attributes[attributeName])
		}
	}

	isDefault, ok := response.Schema.Attributes["is_default"].(schema.BoolAttribute)
	if !ok || isDefault.Optional || !isDefault.Computed || len(isDefault.PlanModifiers) != 0 {
		t.Fatalf("is_default must be computed and refreshed, got %#v", response.Schema.Attributes["is_default"])
	}
}

func TestBuildSecurityGroupCreateBodyResolvesRegionName(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{7}
	body, diags := buildSecurityGroupCreateBody(context.Background(), SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name:        types.StringValue("web"),
			Description: types.StringValue("Managed by Terraform"),
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
	if body.Name != "web" || body.RegionId != regionID ||
		body.Description == nil || *body.Description != "Managed by Terraform" {
		t.Fatalf("unexpected create body: %#v", body)
	}
}

func TestBuildSecurityGroupCreateBodyDescriptionPresence(t *testing.T) {
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

			body, diags := buildSecurityGroupCreateBody(
				context.Background(),
				SecurityGroupResourceModel{
					SecurityGroupModel: SecurityGroupModel{
						Name:        types.StringValue("web"),
						Description: test.description,
					},
					Region: reference.Value(reference.Name, types.StringValue("vn-central")),
				},
				func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
					return projectsdk.ProjectRegionSchema{
						Region: projectsdk.NestedRegionSchema{Id: securityGroupResourceTestUUID, Name: *filter.Name},
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

func TestBuildSecurityGroupCreateBodyRejectsMissingValues(t *testing.T) {
	t.Parallel()

	resolveUnexpected := func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		t.Fatal("resolver must not be called when the plan is incomplete")
		return projectsdk.ProjectRegionSchema{}, nil
	}

	tests := []struct {
		name   string
		plan   SecurityGroupResourceModel
		expect string
	}{
		{
			name:   "blank name",
			plan:   securityGroupCreatePlan(types.StringValue(" "), types.StringValue("vn-central")),
			expect: "Missing name",
		},
		{
			name:   "unknown name",
			plan:   securityGroupCreatePlan(types.StringUnknown(), types.StringValue("vn-central")),
			expect: "Missing name",
		},
		{
			name:   "blank region",
			plan:   securityGroupCreatePlan(types.StringValue("web"), types.StringValue(" ")),
			expect: "Missing region",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diags := buildSecurityGroupCreateBody(
				context.Background(),
				test.plan,
				resolveUnexpected,
			)
			if !diags.HasError() || diags.Errors()[0].Summary() != test.expect {
				t.Fatalf("expected %q diagnostic, got %v", test.expect, diags)
			}
		})
	}

	expected := errors.New("region not found")
	_, diags := buildSecurityGroupCreateBody(
		context.Background(),
		SecurityGroupResourceModel{
			SecurityGroupModel: SecurityGroupModel{
				Name: types.StringValue("web"),
			},
			Region: reference.Value(reference.Name, types.StringValue("vn-central")),
		},
		func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
			return projectsdk.ProjectRegionSchema{}, expected
		},
	)
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), expected.Error()) {
		t.Fatalf("expected resolver diagnostic, got %v", diags)
	}
}

func TestPopulateSecurityGroupState(t *testing.T) {
	t.Parallel()

	description := "Managed by Terraform"
	created := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	sg := &networksdk.SecurityGroupSchema{
		Id:          core.UUID{3},
		Name:        "web",
		DisplayName: "web (display)",
		Description: &description,
		IsDefault:   true,
		Project:     networksdk.NestedProjectSchema{Id: core.UUID{4}, Name: "proj", Slug: "proj"},
		Region:      networksdk.NestedRegionSchema{Id: core.UUID{5}, Name: "vn-central"},
		CreatedAt:   created,
		UpdatedAt:   updated,
	}

	var state SecurityGroupResourceModel
	populateSecurityGroupResourceState(sg, &state)

	if state.ID.ValueString() != (core.UUID{3}).String() {
		t.Fatalf("unexpected id %q", state.ID.ValueString())
	}
	if state.Name.ValueString() != "web" || state.DisplayName.ValueString() != "web (display)" ||
		state.Description.ValueString() != description || !state.IsDefault.ValueBool() ||
		reference.Get(state.Region, reference.Name).ValueString() != "vn-central" ||
		state.RegionID.ValueString() != (core.UUID{5}).String() ||
		state.ProjectID.ValueString() != (core.UUID{4}).String() ||
		state.CreatedAt.ValueString() != created.Format(time.RFC3339) ||
		state.UpdatedAt.ValueString() != updated.Format(time.RFC3339) {
		t.Fatalf("unexpected mapped state: %#v", state)
	}

	// A security group created without a description comes back with an empty
	// description rather than an absent field. An empty string must therefore map
	// to a known empty value instead of being folded into null.
	empty := ""
	sg.Description = &empty
	populateSecurityGroupResourceState(sg, &state)
	if state.Description.IsNull() || state.Description.ValueString() != "" {
		t.Fatalf("expected a known empty description, got %#v", state.Description)
	}

	sg.Description = nil
	populateSecurityGroupResourceState(sg, &state)
	if !state.Description.IsNull() {
		t.Fatalf("expected a null description, got %#v", state.Description)
	}
}

func TestPopulateSecurityGroupResourceStateKeepsConfiguredValues(t *testing.T) {
	t.Parallel()

	sg := &networksdk.SecurityGroupSchema{
		Id:     securityGroupResourceTestUUID,
		Name:   "web",
		Region: networksdk.NestedRegionSchema{Id: securityGroupResourceTestUUID, Name: "vn-central"},
	}

	state := SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name: types.StringValue(" web "),
		},
		Region: reference.Value(reference.Name, types.StringValue(" vn-central ")),
	}
	populateSecurityGroupResourceState(sg, &state)
	if state.Name.ValueString() != " web " || reference.Get(state.Region, reference.Name).ValueString() != " vn-central " {
		t.Fatalf("expected the configured representation to be preserved, got %#v", state)
	}

	// A genuinely different backend value must surface as drift.
	state = SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			Name: types.StringValue("renamed"),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-north")),
	}
	populateSecurityGroupResourceState(sg, &state)
	if state.Name.ValueString() != "web" || reference.Get(state.Region, reference.Name).ValueString() != "vn-central" {
		t.Fatalf("expected backend drift to be reported, got %#v", state)
	}
}

func TestSecurityGroupCreateSendsResolvedRegionAndMapsResponse(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	regionID := core.UUID{2}
	sgID := core.UUID{3}
	httpClient := &http.Client{Transport: securityGroupResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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
		case req.Method == http.MethodPost && req.URL.Path == "/v2/network/security-groups/":
			if req.Header.Get("Project-ID") != projectID.String() {
				t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
			}
			var body networksdk.SecurityGroupCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			if body.Name != "web" || body.RegionId != regionID ||
				body.Description == nil || *body.Description != "Managed by Terraform" {
				t.Fatalf("unexpected create request: %#v", body)
			}
			response.StatusCode = http.StatusCreated
			response.Body = io.NopCloser(strings.NewReader(securityGroupResponseJSON(sgID, projectID, regionID)))
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
	resourceUnderTest := &SecurityGroupResource{client: networkClient, project: projectClient, projectID: projectID}

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(context.Background(), &SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID:          types.StringUnknown(),
			Name:        types.StringValue("web"),
			DisplayName: types.StringUnknown(),
			Description: types.StringValue("Managed by Terraform"),
			IsDefault:   types.BoolUnknown(),
			RegionID:    types.StringUnknown(),
			ProjectID:   types.StringUnknown(),
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

	var state SecurityGroupResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if state.ID.ValueString() != sgID.String() || state.Name.ValueString() != "web" ||
		state.DisplayName.ValueString() != "web" || state.IsDefault.ValueBool() ||
		reference.Get(state.Region, reference.Name).ValueString() != "vn-central" || state.RegionID.ValueString() != regionID.String() ||
		state.ProjectID.ValueString() != projectID.String() {
		t.Fatalf("unexpected create state: %#v", state)
	}
}

func TestSecurityGroupReadResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		status          int
		expectedSummary string
		removed         bool
	}{
		{name: "read removes missing resource", status: http.StatusNotFound, removed: true},
		{name: "read reports API error", status: http.StatusInternalServerError, expectedSummary: "Error reading security group"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			httpClient := &http.Client{Transport: securityGroupResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet ||
					req.URL.Path != "/v2/network/security-groups/"+securityGroupResourceTestUUID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Project-ID") != securityGroupResourceTestUUID.String() {
					t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
				}
				return &http.Response{
					StatusCode: test.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"detail":"backend response"}`)),
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
			resourceUnderTest := &SecurityGroupResource{client: client, projectID: securityGroupResourceTestUUID}
			state := securityGroupResourceTestState(t, resourceUnderTest)

			response := resource.ReadResponse{State: state}
			resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if test.removed && !response.State.Raw.IsNull() {
				t.Fatalf("expected the missing security group to be removed, got %s", response.State.Raw)
			}
			assertSecurityGroupDiagnostic(t, response.Diagnostics, test.expectedSummary)
		})
	}
}

func TestSecurityGroupReadRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	resourceUnderTest := &SecurityGroupResource{projectID: securityGroupResourceTestUUID}
	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{ID: types.StringValue("not-a-uuid")},
		Region:             types.ObjectNull(reference.AttributeTypes(reference.Name)),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}

	response := resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected an invalid UUID diagnostic")
	}
}

func TestBuildSecurityGroupUpdateBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		plan               SecurityGroupModel
		state              SecurityGroupModel
		wantChanged        bool
		wantName           string
		wantNameSet        bool
		wantDescription    string
		wantDescriptionSet bool
	}{
		{
			name:        "renaming sends only the name",
			plan:        SecurityGroupModel{Name: types.StringValue("renamed"), Description: types.StringValue("managed")},
			state:       SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			wantChanged: true,
			wantName:    "renamed",
			wantNameSet: true,
		},
		{
			name:               "changed description sends only the description",
			plan:               SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("updated")},
			state:              SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			wantChanged:        true,
			wantDescription:    "updated",
			wantDescriptionSet: true,
		},
		{
			name:               "an explicit empty description clears it",
			plan:               SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("")},
			state:              SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			wantChanged:        true,
			wantDescription:    "",
			wantDescriptionSet: true,
		},
		{
			name:        "an unknown description is preserved by omission",
			plan:        SecurityGroupModel{Name: types.StringValue("renamed"), Description: types.StringUnknown()},
			state:       SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			wantChanged: true,
			wantName:    "renamed",
			wantNameSet: true,
		},
		{
			name:        "a null description is preserved by omission",
			plan:        SecurityGroupModel{Name: types.StringValue("renamed"), Description: types.StringNull()},
			state:       SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			wantChanged: true,
			wantName:    "renamed",
			wantNameSet: true,
		},
		{
			name:  "a name differing only by surrounding whitespace is not a change",
			plan:  SecurityGroupModel{Name: types.StringValue("  web  "), Description: types.StringValue("managed")},
			state: SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
		},
		{
			name:  "an unchanged security group sends nothing",
			plan:  SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
			state: SecurityGroupModel{Name: types.StringValue("web"), Description: types.StringValue("managed")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, changed := buildSecurityGroupUpdateBody(
				SecurityGroupResourceModel{SecurityGroupModel: tt.plan},
				SecurityGroupResourceModel{SecurityGroupModel: tt.state},
			)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %t, want %t", changed, tt.wantChanged)
			}
			switch {
			case tt.wantNameSet && (body.Name == nil || *body.Name != tt.wantName):
				t.Fatalf("name = %v, want %q", body.Name, tt.wantName)
			case !tt.wantNameSet && body.Name != nil:
				t.Fatalf("expected no name in the request, got %q", *body.Name)
			}
			switch {
			case tt.wantDescriptionSet && (body.Description == nil || *body.Description != tt.wantDescription):
				t.Fatalf("description = %v, want %q", body.Description, tt.wantDescription)
			case !tt.wantDescriptionSet && body.Description != nil:
				t.Fatalf("expected no description in the request, got %q", *body.Description)
			}
		})
	}
}

func TestSecurityGroupUpdateSendsPatchAndMapsResponse(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	regionID := core.UUID{2}
	sgID := core.UUID{3}
	httpClient := &http.Client{Transport: securityGroupResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPatch ||
			req.URL.Path != "/v2/network/security-groups/"+sgID.String()+"/" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if req.Header.Get("Project-ID") != projectID.String() {
			t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
		}
		var body networksdk.SecurityGroupPartialUpdateSchema
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode update request: %v", err)
		}
		if body.Name == nil || *body.Name != "renamed" {
			t.Fatalf("expected the new name in the request, got %#v", body.Name)
		}
		if body.Description != nil {
			t.Fatalf("expected an unchanged description to be omitted, got %q", *body.Description)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
				"id": %q,
				"name": "renamed",
				"display_name": "renamed",
				"description": "Managed by Terraform",
				"is_default": false,
				"owner": null,
				"project": {"id": %q, "name": "proj", "slug": "proj"},
				"region": {"id": %q, "name": "vn-central", "description": ""},
				"created_at": "2026-01-02T03:04:05Z",
				"updated_at": "2026-03-04T05:06:07Z"
			}`, sgID.String(), projectID.String(), regionID.String()))),
			Request: req,
		}, nil
	})}

	client, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	resourceUnderTest := &SecurityGroupResource{client: client, projectID: projectID}

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID:          types.StringValue(sgID.String()),
			Name:        types.StringValue("web"),
			DisplayName: types.StringValue("web"),
			Description: types.StringValue("Managed by Terraform"),
			IsDefault:   types.BoolValue(false),
			RegionID:    types.StringValue(regionID.String()),
			ProjectID:   types.StringValue(projectID.String()),
			CreatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
			UpdatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(context.Background(), &SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID:          types.StringValue(sgID.String()),
			Name:        types.StringValue("renamed"),
			DisplayName: types.StringUnknown(),
			Description: types.StringValue("Managed by Terraform"),
			IsDefault:   types.BoolUnknown(),
			RegionID:    types.StringUnknown(),
			ProjectID:   types.StringValue(projectID.String()),
			CreatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
			UpdatedAt:   types.StringUnknown(),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", response.Diagnostics)
	}

	var newState SecurityGroupResourceModel
	if diags := response.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if newState.ID.ValueString() != sgID.String() || newState.Name.ValueString() != "renamed" ||
		newState.DisplayName.ValueString() != "renamed" ||
		newState.UpdatedAt.ValueString() != "2026-03-04T05:06:07Z" {
		t.Fatalf("unexpected update state: %#v", newState)
	}
}

func TestSecurityGroupUpdateWithoutChangesReadsInstead(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	regionID := core.UUID{2}
	sgID := core.UUID{3}
	httpClient := &http.Client{Transport: securityGroupResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet ||
			req.URL.Path != "/v2/network/security-groups/"+sgID.String()+"/" {
			t.Fatalf("expected a read, got %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(securityGroupResponseJSON(sgID, projectID, regionID))),
			Request:    req,
		}, nil
	})}

	client, err := networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	resourceUnderTest := &SecurityGroupResource{client: client, projectID: projectID}

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	unchanged := SecurityGroupResourceModel{SecurityGroupModel: SecurityGroupModel{
		ID:          types.StringValue(sgID.String()),
		Name:        types.StringValue("web"),
		DisplayName: types.StringValue("web"),
		Description: types.StringValue("Managed by Terraform"),
		IsDefault:   types.BoolValue(false),
		RegionID:    types.StringValue(regionID.String()),
		ProjectID:   types.StringValue(projectID.String()),
		CreatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
		UpdatedAt:   types.StringValue("2026-01-02T03:04:05Z"),
	}, Region: reference.Value(reference.Name, types.StringValue("vn-central"))}
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &unchanged); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	planned := unchanged
	planned.UpdatedAt = types.StringUnknown()
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(context.Background(), &planned); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: state}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected update diagnostics: %v", response.Diagnostics)
	}

	var newState SecurityGroupResourceModel
	if diags := response.State.Get(context.Background(), &newState); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if newState.UpdatedAt.IsUnknown() || newState.Name.ValueString() != "web" {
		t.Fatalf("unexpected state after a no-op update: %#v", newState)
	}
}

func TestSecurityGroupDeleteResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		status          int
		expectedSummary string
	}{
		{name: "delete succeeds", status: http.StatusNoContent},
		{name: "delete tolerates an already removed security group", status: http.StatusNotFound},
		{name: "delete reports API error", status: http.StatusInternalServerError, expectedSummary: "Error deleting security group"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			called := false
			httpClient := &http.Client{Transport: securityGroupResourceRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				if req.Method != http.MethodDelete ||
					req.URL.Path != "/v2/network/security-groups/"+securityGroupResourceTestUUID.String()+"/" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				if req.Header.Get("Project-ID") != securityGroupResourceTestUUID.String() {
					t.Fatalf("unexpected Project-ID header %q", req.Header.Get("Project-ID"))
				}
				return &http.Response{
					StatusCode: test.status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"detail":"backend response"}`)),
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
			resourceUnderTest := &SecurityGroupResource{client: client, projectID: securityGroupResourceTestUUID}
			state := securityGroupResourceTestState(t, resourceUnderTest)

			response := resource.DeleteResponse{State: state}
			resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if !called {
				t.Fatal("Delete must call the backend")
			}
			assertSecurityGroupDiagnostic(t, response.Diagnostics, test.expectedSummary)
		})
	}
}

func TestSecurityGroupImportState(t *testing.T) {
	t.Parallel()

	resourceUnderTest := &SecurityGroupResource{}
	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	if diags := response.State.Set(context.Background(), &SecurityGroupResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	resourceUnderTest.ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: securityGroupResourceTestUUID.String()},
		&response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected import diagnostics: %v", response.Diagnostics)
	}

	var state SecurityGroupResourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("read imported state: %v", diags)
	}
	if state.ID.ValueString() != securityGroupResourceTestUUID.String() {
		t.Fatalf("expected imported ID %s, got %s", securityGroupResourceTestUUID, state.ID.ValueString())
	}
}

func TestSecurityGroupReadAfterImportFillsRegionReference(t *testing.T) {
	t.Parallel()

	sg := &networksdk.SecurityGroupSchema{
		Id:      securityGroupResourceTestUUID,
		Name:    "web",
		Project: networksdk.NestedProjectSchema{Id: core.UUID{4}, Name: "proj", Slug: "proj"},
		Region:  networksdk.NestedRegionSchema{Id: core.UUID{5}, Name: "vn-central"},
	}
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &SecurityGroupResource{client: client, projectID: securityGroupResourceTestUUID}
	}, sg.Id.String(), sg)

	var region types.Object
	if diags := state.GetAttribute(context.Background(), path.Root("region"), &region); diags.HasError() {
		t.Fatalf("read region: %v", diags)
	}
	if want := reference.Value(reference.Name, types.StringValue("vn-central")); !region.Equal(want) {
		t.Fatalf("imported region = %s, want %s", region, want)
	}
}

func securityGroupResourceTestState(t *testing.T, resourceUnderTest *SecurityGroupResource) tfsdk.State {
	t.Helper()

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(context.Background(), &SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{
			ID:   types.StringValue(securityGroupResourceTestUUID.String()),
			Name: types.StringValue("web"),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func assertSecurityGroupDiagnostic(t *testing.T, diagnostics diag.Diagnostics, expectedSummary string) {
	t.Helper()

	if expectedSummary == "" {
		if diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diagnostics)
		}
		return
	}
	if !diagnostics.HasError() {
		t.Fatalf("expected %q diagnostic", expectedSummary)
	}
	if summary := diagnostics.Errors()[0].Summary(); summary != expectedSummary {
		t.Fatalf("expected summary %q, got %q", expectedSummary, summary)
	}
}

func securityGroupResponseJSON(sgID, projectID, regionID core.UUID) string {
	return fmt.Sprintf(`{
		"id": %q,
		"name": "web",
		"display_name": "web",
		"description": "Managed by Terraform",
		"is_default": false,
		"owner": null,
		"project": {"id": %q, "name": "proj", "slug": "proj"},
		"region": {"id": %q, "name": "vn-central", "description": ""},
		"created_at": "2026-01-02T03:04:05Z",
		"updated_at": "2026-01-02T03:04:05Z"
	}`, sgID.String(), projectID.String(), regionID.String())
}

func securityGroupCreatePlan(name, region types.String) SecurityGroupResourceModel {
	return SecurityGroupResourceModel{
		SecurityGroupModel: SecurityGroupModel{Name: name},
		Region:             reference.Value(reference.Name, region),
	}
}
