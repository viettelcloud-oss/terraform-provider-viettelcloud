package server

import (
	"context"
	"encoding/json"
	"errors"
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
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func placementGroupSchema(t *testing.T) resourceschema.Schema {
	t.Helper()

	var response resource.SchemaResponse
	(&PlacementGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// placementGroupRegion returns a region reference, null for a null name.
func placementGroupRegion(name types.String) types.Object {
	return reference.Value(reference.Name, name)
}

func TestPlacementGroupModelsMatchSchema(t *testing.T) {
	t.Parallel()

	schema := placementGroupSchema(t)
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &PlacementGroupResourceModel{Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestPlacementGroupSchemaMutability(t *testing.T) {
	t.Parallel()

	schema := placementGroupSchema(t)
	policy, ok := schema.Attributes["policy"].(resourceschema.StringAttribute)
	if !ok || !policy.Required || policy.Computed || len(policy.PlanModifiers) != 1 {
		t.Fatalf("policy must be required, computed-false, and replace-only, got %#v", schema.Attributes["policy"])
	}
	region, ok := schema.Attributes["region"].(resourceschema.SingleNestedAttribute)
	if !ok || !region.Required || region.Computed || len(region.PlanModifiers) != 1 || len(region.Attributes) != 1 {
		t.Fatalf("region must be a required, computed-false, and replace-only reference, got %#v", schema.Attributes["region"])
	}
	regionName, ok := region.Attributes[reference.Name].(resourceschema.StringAttribute)
	if !ok || !regionName.Required || regionName.Computed || len(regionName.PlanModifiers) != 0 {
		t.Fatalf("region.name must be a required criterion, got %#v", region.Attributes[reference.Name])
	}
	description, ok := schema.Attributes["description"].(resourceschema.StringAttribute)
	if !ok || !description.Optional || !description.Computed || len(description.PlanModifiers) != 0 {
		t.Fatalf("description must be optional, computed, and update-ready, got %#v", schema.Attributes["description"])
	}
	id, ok := schema.Attributes["id"].(resourceschema.StringAttribute)
	if !ok || id.Optional || !id.Computed || len(id.PlanModifiers) != 1 {
		t.Fatalf("id must be computed and stable, got %#v", schema.Attributes["id"])
	}
	createdAt, ok := schema.Attributes["created_at"].(resourceschema.StringAttribute)
	if !ok || createdAt.Optional || !createdAt.Computed || len(createdAt.PlanModifiers) != 1 {
		t.Fatalf("created_at must be computed and stable, got %#v", schema.Attributes["created_at"])
	}
}

func TestPlacementGroupReplacementAttributes(t *testing.T) {
	t.Parallel()

	schema := placementGroupSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Policy: types.StringValue("affinity"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Policy: types.StringValue("affinity"),
	}, Region: placementGroupRegion(types.StringValue("vn-north"))}); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
	}
	region := schema.Attributes["region"].(resourceschema.SingleNestedAttribute)
	for _, test := range []struct {
		name    string
		plan    types.Object
		replace bool
	}{
		{"same name", placementGroupRegion(types.StringValue("vn-central")), false},
		{"changed name", placementGroupRegion(types.StringValue("vn-north")), true},
		{"unknown name", placementGroupRegion(types.StringUnknown()), true},
		{"unknown reference", types.ObjectUnknown(reference.AttributeTypes(reference.Name)), true},
	} {
		request := planmodifier.ObjectRequest{
			State:      state,
			Plan:       plan,
			Config:     tfsdk.Config(plan),
			StateValue: placementGroupRegion(types.StringValue("vn-central")),
			PlanValue:  test.plan,
		}
		var response planmodifier.ObjectResponse
		region.PlanModifiers[0].PlanModifyObject(context.Background(), request, &response)
		if response.RequiresReplace != test.replace {
			t.Errorf("region %s: replace=%t, want %t", test.name, response.RequiresReplace, test.replace)
		}
	}

	for _, name := range []string{"policy"} {
		attribute, ok := schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if len(attribute.PlanModifiers) != 1 {
			t.Fatalf("expected one plan modifier for %s, got %d", name, len(attribute.PlanModifiers))
		}

		state := tfsdk.State{Schema: schema}
		if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
			Policy: types.StringValue("affinity"),
		}, Region: placementGroupRegion(types.StringValue("vn-central"))}); diags.HasError() {
			t.Fatalf("set modifier state: %v", diags)
		}
		plan := tfsdk.Plan{Schema: schema}
		if diags := plan.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
			Policy: types.StringValue("anti-affinity"),
		}, Region: placementGroupRegion(types.StringValue("vn-north"))}); diags.HasError() {
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
			t.Errorf("expected changing %s to require replacement", name)
		}
	}
}

func TestBuildPlacementGroupCreateBodyResolvesRegionName(t *testing.T) {
	t.Parallel()

	body, diags := buildPlacementGroupCreateBody(context.Background(), PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("  cluster-a  "),
		Description: types.StringValue("primary cluster"),
		Policy:      types.StringValue("affinity"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}, func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		if filter.Name == nil || *filter.Name != "vn-central" {
			t.Fatalf("unexpected region filter %#v", filter.Name)
		}
		return projectsdk.ProjectRegionSchema{
			Region: projectsdk.NestedRegionSchema{Id: placementGroupTestRegion, Name: *filter.Name},
		}, nil
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "cluster-a" || body.Policy != serversdk.PlacementGroupPolicyAffinity ||
		body.RegionId != placementGroupTestRegion ||
		body.Description == nil || *body.Description != "primary cluster" {
		t.Fatalf("unexpected create body: %#v", body)
	}
}

func TestBuildPlacementGroupCreateBodyRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model PlacementGroupResourceModel
	}{
		{
			name:  "null name",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Policy: types.StringValue("affinity")}, Region: placementGroupRegion(types.StringValue("vn-central"))},
		},
		{
			name:  "unknown policy",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a"), Policy: types.StringUnknown()}, Region: placementGroupRegion(types.StringValue("vn-central"))},
		},
		{
			name:  "blank region",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a"), Policy: types.StringValue("affinity")}, Region: placementGroupRegion(types.StringValue("   "))},
		},
		{
			name:  "null region",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a"), Policy: types.StringValue("affinity")}, Region: placementGroupRegion(types.StringNull())},
		},
		{
			name:  "unknown region",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a"), Policy: types.StringValue("affinity")}, Region: placementGroupRegion(types.StringUnknown())},
		},
		{
			name:  "unknown region name",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a"), Policy: types.StringValue("affinity")}, Region: reference.Of(map[string]types.String{reference.Name: types.StringUnknown()})},
		},
		{
			name: "invalid policy",
			model: PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
				Name:   types.StringValue("cluster-a"),
				Policy: types.StringValue("closest"),
			}, Region: placementGroupRegion(types.StringValue("vn-central"))},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, diags := buildPlacementGroupCreateBody(context.Background(), test.model, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
				t.Fatal("resolver must not be called for invalid input")
				return projectsdk.ProjectRegionSchema{}, nil
			})
			if !diags.HasError() {
				t.Fatalf("expected diagnostics, got %#v", diags)
			}
		})
	}
}

func TestBuildPlacementGroupCreateBodyRejectsRegionResolverError(t *testing.T) {
	t.Parallel()

	expected := errors.New("region not found")
	_, diags := buildPlacementGroupCreateBody(context.Background(), PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:   types.StringValue("cluster-a"),
		Policy: types.StringValue("affinity"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}, func(context.Context, projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		return projectsdk.ProjectRegionSchema{}, expected
	})
	if !diags.HasError() || !strings.Contains(diags[0].Detail(), expected.Error()) {
		t.Fatalf("expected a wrapped region-resolver diagnostic, got %v", diags)
	}
}

func TestBuildPlacementGroupUpdateBodySendsOnlyChanges(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("before"),
	}}
	plan := state
	plan.Name = types.StringValue("  cluster-b  ")
	plan.Description = types.StringValue("after")

	body, changed, diags := buildPlacementGroupUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Name == nil || *body.Name != "cluster-b" ||
		body.Description == nil || *body.Description != "after" {
		t.Fatalf("unexpected sparse update body: %#v", body)
	}
}

func TestBuildPlacementGroupUpdateBodyOmitsEqualValues(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("primary"),
	}}
	body, changed, diags := buildPlacementGroupUpdateBody(state, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if changed || body.Name != nil || body.Description != nil {
		t.Fatalf("expected no change, got changed=%v body=%#v", changed, body)
	}
}

func TestBuildPlacementGroupUpdateBodyPreservesOmittedDescription(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("primary"),
	}}
	plan := state
	plan.Description = types.StringNull()

	body, changed, diags := buildPlacementGroupUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if changed || body.Description != nil || body.Name != nil {
		t.Fatalf("expected no change, got changed=%v body=%#v", changed, body)
	}
}

func TestBuildPlacementGroupUpdateBodyClearsDescription(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("primary"),
	}}
	plan := state
	plan.Description = types.StringValue("")

	body, changed, diags := buildPlacementGroupUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !changed || body.Description == nil || *body.Description != "" || body.Name != nil {
		t.Fatalf("expected description to be cleared, got changed=%v body=%#v", changed, body)
	}
}

func TestBuildPlacementGroupUpdateBodyPreservesUntrimmedNameWhenSemanticallyEqual(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a")}}
	plan := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("  cluster-a  ")}}

	body, changed, diags := buildPlacementGroupUpdateBody(plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if changed || body.Name != nil {
		t.Fatalf("expected no change when name differs only by whitespace, got changed=%v body=%#v", changed, body)
	}
}

func TestBuildPlacementGroupUpdateBodyRejectsUnknownName(t *testing.T) {
	t.Parallel()

	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{Name: types.StringValue("cluster-a")}}
	plan := state
	plan.Name = types.StringUnknown()

	_, _, diags := buildPlacementGroupUpdateBody(plan, state)
	if !diags.HasError() {
		t.Fatal("expected diagnostic for unknown name in update plan")
	}
}

func TestPopulatePlacementGroupModelNil(t *testing.T) {
	t.Parallel()

	var m PlacementGroupModel
	populatePlacementGroupModel(nil, &m)
	if !m.ID.IsNull() {
		t.Fatalf("expected null ID on nil Placement Group, got %v", m.ID)
	}
}

func TestPopulatePlacementGroupModelMapsAllFields(t *testing.T) {
	t.Parallel()

	policy := serversdk.PlacementGroupPolicyAffinity
	serverCount := 3
	pg := &serversdk.PlacementGroupSchema{
		Id:          placementGroupTestUUID,
		Name:        "cluster-a",
		Description: "primary cluster",
		Policy:      &policy,
		Project: serversdk.NestedProjectSchema{
			Id:   placementGroupProjectID,
			Name: "integration",
			Slug: "integration",
		},
		Region: serversdk.NestedRegionSchema{
			Id:   placementGroupTestRegion,
			Name: "vn-central",
		},
		ServerCount: &serverCount,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}

	var state PlacementGroupResourceModel
	populatePlacementGroupResourceState(pg, &state)

	if state.ID.ValueString() != pg.Id.String() ||
		state.Name.ValueString() != "cluster-a" ||
		state.Description.ValueString() != "primary cluster" ||
		state.Policy.ValueString() != "affinity" ||
		reference.Get(state.Region, reference.Name).ValueString() != "vn-central" ||
		state.RegionID.ValueString() != placementGroupTestRegion.String() ||
		state.Project.ValueString() != "integration" ||
		state.ServerCount.ValueInt64() != 3 ||
		state.CreatedAt.ValueString() != pg.CreatedAt.Format(time.RFC3339) ||
		state.UpdatedAt.ValueString() != pg.UpdatedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestPopulatePlacementGroupResourceStatePreservesConfiguredNameAndRegion(t *testing.T) {
	t.Parallel()

	policy := serversdk.PlacementGroupPolicyAffinity
	pg := &serversdk.PlacementGroupSchema{
		Id:     placementGroupTestUUID,
		Name:   "cluster-a",
		Policy: &policy,
		Region: serversdk.NestedRegionSchema{
			Id:   placementGroupTestRegion,
			Name: "vn-central",
		},
		Project: placementGroupTestProject,
	}
	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("  cluster-a  "),
	}, Region: placementGroupRegion(types.StringValue("  vn-central  "))}
	populatePlacementGroupResourceState(pg, &state)
	if state.Name.ValueString() != "  cluster-a  " || reference.Get(state.Region, reference.Name).ValueString() != "  vn-central  " {
		t.Fatalf("expected configured representation to be preserved, got name=%q region=%q", state.Name.ValueString(), reference.Get(state.Region, reference.Name).ValueString())
	}
}

func TestPopulatePlacementGroupResourceStateUsesChangedBackend(t *testing.T) {
	t.Parallel()

	policy := serversdk.PlacementGroupPolicyAffinity
	pg := &serversdk.PlacementGroupSchema{
		Id:     placementGroupTestUUID,
		Name:   "new-backend-name",
		Policy: &policy,
		Region: serversdk.NestedRegionSchema{
			Id:   core.UUID{0x99},
			Name: "vn-north",
		},
		Project: placementGroupTestProject,
	}
	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("old-name"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}
	populatePlacementGroupResourceState(pg, &state)
	if state.Name.ValueString() != "new-backend-name" || reference.Get(state.Region, reference.Name).ValueString() != "vn-north" {
		t.Fatalf("expected backend values to replace configuration, got name=%q region=%q", state.Name.ValueString(), reference.Get(state.Region, reference.Name).ValueString())
	}
}

// An import puts only the ID in state, so the refresh that follows it has no
// configured name or region to preserve and must adopt the backend
// representation of both. A configuration that then repeats those backend
// values plans no change.
func TestPopulatePlacementGroupResourceStateFillsNullStateAfterImport(t *testing.T) {
	t.Parallel()

	pg := mockPlacementGroup("cluster-a", "", serversdk.PlacementGroupPolicyAntiAffinity)
	state := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue(placementGroupTestUUID.String()),
	}}
	populatePlacementGroupResourceState(pg, &state)

	if state.Name.ValueString() != "cluster-a" || !state.Region.Equal(placementGroupRegion(types.StringValue("vn-central"))) {
		t.Fatalf("expected the backend name and region reference, got name=%q region=%s", state.Name.ValueString(), state.Region)
	}
	if state.Policy.ValueString() != string(serversdk.PlacementGroupPolicyAntiAffinity) {
		t.Fatalf("expected policy %q, got %q", serversdk.PlacementGroupPolicyAntiAffinity, state.Policy.ValueString())
	}
	if state.RegionID.ValueString() != placementGroupTestRegion.String() || state.Project.ValueString() != "integration" {
		t.Fatalf("expected the backend region ID and project, got region_id=%q project=%q", state.RegionID.ValueString(), state.Project.ValueString())
	}
	if state.Description.IsNull() || state.Description.ValueString() != "" {
		t.Fatalf("expected a known empty description, got %v", state.Description)
	}
}

func TestPopulatePlacementGroupModelWithoutPolicyAndServerCount(t *testing.T) {
	t.Parallel()

	pg := &serversdk.PlacementGroupSchema{
		Id:   placementGroupTestUUID,
		Name: "cluster-a",
		Region: serversdk.NestedRegionSchema{
			Id:   placementGroupTestRegion,
			Name: "vn-central",
		},
		Project: placementGroupTestProject,
	}
	var state PlacementGroupResourceModel
	populatePlacementGroupResourceState(pg, &state)
	if !state.Policy.IsNull() {
		t.Fatalf("expected null policy, got %q", state.Policy.ValueString())
	}
	if !state.ServerCount.IsNull() {
		t.Fatalf("expected null server_count, got %d", state.ServerCount.ValueInt64())
	}
}

func TestPlacementGroupResourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()

	resourceValue := NewPlacementGroupResource()
	if resourceValue == nil {
		t.Fatal("expected non-nil resource")
	}

	var metadataResponse resource.MetadataResponse
	resourceValue.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadataResponse)
	if metadataResponse.TypeName != "viettelcloud_placement_group" {
		t.Fatalf("expected viettelcloud_placement_group metadata, got %q", metadataResponse.TypeName)
	}

	r := &PlacementGroupResource{}
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

	client, err := serversdk.NewClient("https://server.test", serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	projectClient, err := projectsdk.NewClient("https://project.test", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{
		ProviderData: &providerdata.Configured{
			Server:    client,
			Project:   projectClient,
			ProjectID: placementGroupProjectID,
		},
	}, &validResponse)
	if validResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for valid provider data: %v", validResponse.Diagnostics)
	}
	if r.client != client || r.project != projectClient || r.projectID != placementGroupProjectID {
		t.Fatalf("expected client, project, and projectID to be configured")
	}
}

func TestPlacementGroupResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var (
		serverRequests  int
		projectRequests int
		currentName     = "cluster-a"
		currentDesc     = "before"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/v2/projects/"+placementGroupProjectID.String()+"/regions/":
			projectRequests++
			if req.Method != http.MethodGet {
				t.Errorf("unexpected project method: %s", req.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(projectsdk.PagedProjectRegionSchema{
				Count: 1,
				Results: []projectsdk.ProjectRegionSchema{{
					Region: projectsdk.NestedRegionSchema{
						Id:   placementGroupTestRegion,
						Name: "vn-central",
					},
				}},
			})
		case req.URL.Path == "/v2/server/placement-groups/":
			serverRequests++
			if req.Method != http.MethodPost {
				t.Errorf("unexpected server method: %s", req.Method)
			}
			var body serversdk.PlacementGroupCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.Name != "cluster-a" || body.Policy != serversdk.PlacementGroupPolicyAffinity ||
				body.RegionId != placementGroupTestRegion ||
				body.Description == nil || *body.Description != "before" {
				t.Errorf("unexpected create body: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writePlacementGroupResponse(t, w, mockPlacementGroup(currentName, currentDesc, serversdk.PlacementGroupPolicyAffinity))
		case req.URL.Path == "/v2/server/placement-groups/"+placementGroupTestUUID.String()+"/":
			serverRequests++
			switch req.Method {
			case http.MethodGet:
				writePlacementGroupResponse(t, w, mockPlacementGroup(currentName, currentDesc, serversdk.PlacementGroupPolicyAffinity))
			case http.MethodPatch:
				var body serversdk.PlacementGroupPartialUpdateSchema
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Errorf("decode update body: %v", err)
				}
				if body.Name != nil {
					currentName = *body.Name
				}
				if body.Description != nil {
					currentDesc = *body.Description
				}
				writePlacementGroupResponse(t, w, mockPlacementGroup(currentName, currentDesc, serversdk.PlacementGroupPolicyAffinity))
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected server method on placement group: %s", req.Method)
			}
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()

	serverClient, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	projectClient, err := projectsdk.NewClient(
		server.URL,
		projectsdk.WithHTTPClient(server.Client()),
		projectsdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	r := &PlacementGroupResource{client: serverClient, project: projectClient, projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)

	createPlan := tfsdk.Plan{Schema: schema}
	if diags := createPlan.Set(ctx, &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("before"),
		Policy:      types.StringValue("affinity"),
		RegionID:    types.StringValue(placementGroupTestRegion.String()),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: createResponse.State}
	r.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var updatePlanModel PlacementGroupResourceModel
	if diags := readResponse.State.Get(ctx, &updatePlanModel); diags.HasError() {
		t.Fatalf("get state before update: %v", diags)
	}
	updatePlanModel.Name = types.StringValue("cluster-b")
	updatePlanModel.Description = types.StringValue("after")
	updatePlan := tfsdk.Plan{Schema: schema}
	if diags := updatePlan.Set(ctx, &updatePlanModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}

	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	r.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if serverRequests != 4 {
		t.Fatalf("expected four SDK requests, got %d", serverRequests)
	}
}

func TestPlacementGroupResourceReadRemovesMissingAndDeleteAcceptsNotFound(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &PlacementGroupResource{client: client, projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue(placementGroupTestUUID.String()),
	}, Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("set initial state: %v", diags)
	}

	readResponse := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	if !readResponse.State.Raw.IsNull() {
		t.Fatal("expected missing Placement Group to be removed from state")
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

func TestPlacementGroupResourceReadAndDeleteReportAPIErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		method          string
		status          int
		expectedSummary string
	}{
		{name: "read 500", method: http.MethodGet, status: http.StatusInternalServerError, expectedSummary: "Error reading Placement Group"},
		{name: "delete 500", method: http.MethodDelete, status: http.StatusInternalServerError, expectedSummary: "Error deleting Placement Group"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"detail":"backend response"}`, test.status)
			}))
			defer server.Close()

			client, err := serversdk.NewClient(
				server.URL,
				serversdk.WithHTTPClient(server.Client()),
				serversdk.WithRetry(core.NoRetry()),
			)
			if err != nil {
				t.Fatalf("create server client: %v", err)
			}
			r := &PlacementGroupResource{client: client, projectID: placementGroupProjectID}
			schema := placementGroupSchema(t)
			state := tfsdk.State{Schema: schema}
			if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
				ID: types.StringValue(placementGroupTestUUID.String()),
			}, Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}

			if test.method == http.MethodGet {
				response := resource.ReadResponse{State: state}
				r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
				if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != test.expectedSummary {
					t.Fatalf("expected diagnostic %q, got %v", test.expectedSummary, response.Diagnostics)
				}
				return
			}
			response := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != test.expectedSummary {
				t.Fatalf("expected diagnostic %q, got %v", test.expectedSummary, response.Diagnostics)
			}
		})
	}
}

func TestPlacementGroupResourceReadRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	r := &PlacementGroupResource{projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID: types.StringValue("not-a-uuid"),
	}, Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("set invalid state: %v", diags)
	}

	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid state ID")
	}
}

func TestPlacementGroupResourceUpdateRejectsInvalidStateID(t *testing.T) {
	t.Parallel()

	r := &PlacementGroupResource{projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:   types.StringValue("not-a-uuid"),
		Name: types.StringValue("cluster-a"),
	}, Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("set invalid state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name: types.StringValue("cluster-b"),
	}, Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error for invalid state ID")
	}
}

func TestPlacementGroupResourceUpdateRejectsAnotherPlacementGroup(t *testing.T) {
	t.Parallel()

	other := core.UUID{0x88}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writePlacementGroupResponse(t, w, &serversdk.PlacementGroupSchema{
			Id:   other,
			Name: "different",
			Region: serversdk.NestedRegionSchema{
				Id:   placementGroupTestRegion,
				Name: "vn-central",
			},
			Project: placementGroupTestProject,
		})
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &PlacementGroupResource{client: client, projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:          types.StringValue(placementGroupTestUUID.String()),
		Name:        types.StringValue("cluster-a"),
		Policy:      types.StringValue("affinity"),
		RegionID:    types.StringValue(placementGroupTestRegion.String()),
		Description: types.StringValue("before"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		Name:        types.StringValue("cluster-b"),
		Policy:      types.StringValue("affinity"),
		RegionID:    types.StringValue(placementGroupTestRegion.String()),
		Description: types.StringValue("after"),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error updating Placement Group" {
		t.Fatalf("expected an update diagnostic, got %v", response.Diagnostics)
	}
	if !strings.Contains(response.Diagnostics[0].Detail(), other.String()) {
		t.Fatalf("expected the returned Placement Group ID in the detail, got %q", response.Diagnostics[0].Detail())
	}
}

func TestPlacementGroupResourceUpdateWithoutChangesCallsGetPlacementGroup(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != http.MethodGet || req.URL.Path != "/v2/server/placement-groups/"+placementGroupTestUUID.String()+"/" {
			t.Errorf("unexpected request on unchanged update: %s %s", req.Method, req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		writePlacementGroupResponse(t, w, mockPlacementGroup("cluster-a", "primary", serversdk.PlacementGroupPolicyAffinity))
	}))
	defer server.Close()

	client, err := serversdk.NewClient(
		server.URL,
		serversdk.WithHTTPClient(server.Client()),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &PlacementGroupResource{client: client, projectID: placementGroupProjectID}
	schema := placementGroupSchema(t)
	model := PlacementGroupResourceModel{PlacementGroupModel: PlacementGroupModel{
		ID:          types.StringValue(placementGroupTestUUID.String()),
		Name:        types.StringValue("cluster-a"),
		Description: types.StringValue("primary"),
		Policy:      types.StringValue("affinity"),
		RegionID:    types.StringValue(placementGroupTestRegion.String()),
	}, Region: placementGroupRegion(types.StringValue("vn-central"))}
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	response := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics on unchanged update: %v", response.Diagnostics)
	}
	if requests != 1 {
		t.Fatalf("expected exactly 1 GET request on unchanged update, got %d", requests)
	}
}

func TestPlacementGroupResourceImportState(t *testing.T) {
	t.Parallel()

	schema := placementGroupSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	if diags := response.State.Set(context.Background(), &PlacementGroupResourceModel{Region: placementGroupRegion(types.StringNull())}); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	(&PlacementGroupResource{}).ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: placementGroupTestUUID.String()},
		&response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}

	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("get imported ID: %v", response.Diagnostics)
	}
	if id.ValueString() != placementGroupTestUUID.String() {
		t.Fatalf("expected imported ID %q, got %q", placementGroupTestUUID, id.ValueString())
	}
}

func mockPlacementGroup(name, description string, policy serversdk.PlacementGroupPolicy) *serversdk.PlacementGroupSchema {
	value := policy
	return &serversdk.PlacementGroupSchema{
		Id:          placementGroupTestUUID,
		Name:        name,
		Description: description,
		Policy:      &value,
		Region: serversdk.NestedRegionSchema{
			Id:   placementGroupTestRegion,
			Name: "vn-central",
		},
		Project: serversdk.NestedProjectSchema{
			Id:   placementGroupProjectID,
			Name: "integration",
			Slug: "integration",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}
