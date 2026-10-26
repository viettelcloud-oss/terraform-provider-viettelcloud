package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

func customImageResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewCustomImageResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

// customImageVolume returns a source volume reference with both criteria.
func customImageVolume(name, id types.String) types.Object {
	return reference.Of(map[string]types.String{reference.Name: name, reference.ID: id})
}

// customImageVolumeName returns a source volume reference by name only.
func customImageVolumeName(name string) types.Object {
	return customImageVolume(types.StringValue(name), types.StringNull())
}

// customImageVolumeID returns a source volume reference by UUID only.
func customImageVolumeID(id string) types.Object {
	return customImageVolume(types.StringNull(), types.StringValue(id))
}

// typedCustomImageModel gives zero-valued references the schema types that
// tfsdk needs to encode them.
func typedCustomImageModel(model CustomImageResourceModel) CustomImageResourceModel {
	if model.Volume.IsNull() {
		model.Volume = types.ObjectNull(reference.AttributeTypes(customImageVolumeCriteria...))
	}
	if model.SharedProjects.IsNull() {
		model.SharedProjects = types.SetNull(reference.ObjectType(reference.ID))
	}
	return model
}

func customImagePlan(t *testing.T, model CustomImageResourceModel) tfsdk.Plan {
	t.Helper()
	model = typedCustomImageModel(model)
	plan := tfsdk.Plan{Schema: customImageResourceSchema(t)}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	return plan
}

func customImageState(t *testing.T, model CustomImageResourceModel) tfsdk.State {
	t.Helper()
	model = typedCustomImageModel(model)
	state := tfsdk.State{Schema: customImageResourceSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func customImageBaseModel() CustomImageResourceModel {
	var model CustomImageResourceModel
	populateCustomImageResourceState(customImageFixture(), &model)
	return model
}

func TestCustomImageResourceSchemaAndModifiers(t *testing.T) {
	t.Parallel()
	model := customImageBaseModel()
	_ = customImagePlan(t, model)
	resourceSchema := customImageResourceSchema(t)
	for _, field := range []string{"name", "disk_format"} {
		a := resourceSchema.Attributes[field].(schema.StringAttribute)
		if !a.Required || a.Computed {
			t.Fatalf("%s: %#v", field, a)
		}
	}
	volume := resourceSchema.Attributes["volume"].(schema.SingleNestedAttribute)
	if !volume.Optional || !volume.Computed || volume.Required || len(volume.Attributes) != 2 || len(volume.PlanModifiers) != 1 {
		t.Fatalf("volume must be an optional and computed reference: %#v", volume)
	}
	for _, field := range []string{"volume_id", "shared_project_ids"} {
		if _, ok := resourceSchema.Attributes[field]; ok {
			t.Fatalf("unexpected legacy attribute %s", field)
		}
	}
	attributes := map[string]schema.StringAttribute{
		"volume.name":      volume.Attributes[reference.Name].(schema.StringAttribute),
		"volume.id":        volume.Attributes[reference.ID].(schema.StringAttribute),
		"disk_format":      resourceSchema.Attributes["disk_format"].(schema.StringAttribute),
		"container_format": resourceSchema.Attributes["container_format"].(schema.StringAttribute),
	}
	for field, a := range attributes {
		if field != "disk_format" && (!a.Optional || !a.Computed) {
			t.Fatalf("%s must be optional and computed", field)
		}
		for _, test := range []struct {
			name    string
			value   types.String
			replace bool
		}{
			{"change", types.StringValue("new"), true}, {"remove", types.StringNull(), field == "disk_format"}, {"unknown", types.StringUnknown(), true},
		} {
			t.Run(field+"/"+test.name, func(t *testing.T) {
				response := planmodifier.StringResponse{PlanValue: test.value}
				req := planmodifier.StringRequest{State: customImageState(t, model), Plan: customImagePlan(t, model), ConfigValue: test.value, StateValue: types.StringValue("old"), PlanValue: test.value}
				for _, modifier := range a.PlanModifiers {
					req.PlanValue = response.PlanValue
					modifier.PlanModifyString(context.Background(), req, &response)
				}
				if response.RequiresReplace != test.replace {
					t.Fatalf("replace=%v, want %v", response.RequiresReplace, test.replace)
				}
			})
		}
	}
	sharedProjects := resourceSchema.Attributes["shared_projects"].(schema.SetNestedAttribute)
	projectID := sharedProjects.NestedObject.Attributes[reference.ID].(schema.StringAttribute)
	if !sharedProjects.Optional || !sharedProjects.Computed || len(sharedProjects.PlanModifiers) != 0 ||
		!projectID.Optional || !projectID.Computed || len(projectID.Validators) != 1 || len(sharedProjects.NestedObject.Attributes) != 1 {
		t.Fatalf("shared_projects must be an optional and computed set of references: %#v", sharedProjects)
	}
	if _, ok := resourceSchema.Attributes["region_id"]; ok {
		t.Fatal("unexpected attribute region_id")
	}
	for _, field := range []string{"project", "owner", "owner_id"} {
		a := resourceSchema.Attributes[field].(schema.StringAttribute)
		if !a.Computed || a.Optional || a.Required || len(a.PlanModifiers) != 0 {
			t.Fatalf("%s must be a computed output: %#v", field, a)
		}
	}
	for _, field := range []string{"name", "description", "os", "version", "state", "metadata"} {
		if len(resourceSchema.Attributes[field].(schema.StringAttribute).PlanModifiers) != 0 {
			t.Fatalf("mutable %s has modifier", field)
		}
	}
}

func TestCustomImageSourcePlans(t *testing.T) {
	t.Parallel()
	resourceSchema := customImageResourceSchema(t)
	ctx := context.Background()
	for _, tt := range []struct {
		name        string
		byID        bool
		change      string
		wantReplace bool
		wantUnknown bool
	}{
		{name: "name no change"},
		{name: "ID no change", byID: true},
		{name: "name metadata update", change: "metadata"},
		{name: "ID metadata update", byID: true, change: "metadata"},
		{name: "name replacement", change: "source", wantReplace: true, wantUnknown: true},
		{name: "ID replacement", byID: true, change: "source", wantReplace: true, wantUnknown: true},
		{name: "unknown name", change: "unknown", wantReplace: true, wantUnknown: true},
		{name: "unknown ID", byID: true, change: "unknown", wantReplace: true, wantUnknown: true},
		{name: "name disk replacement", change: "disk", wantReplace: true, wantUnknown: true},
		{name: "ID disk replacement", byID: true, change: "disk", wantReplace: true, wantUnknown: true},
		{name: "switch to same ID", byID: true, change: "metadata"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := customImageBaseModel()
			config := typedCustomImageModel(CustomImageResourceModel{CustomImageModel: CustomImageModel{Name: state.Name, DiskFormat: state.DiskFormat}})
			proposed := state
			stateName, stateID := reference.Get(state.Volume, reference.Name), reference.Get(state.Volume, reference.ID)
			configured, inferred := reference.Name, reference.ID
			if tt.byID {
				configured, inferred = reference.ID, reference.Name
				config.Volume = customImageVolume(types.StringNull(), stateID)
			} else {
				config.Volume = customImageVolume(stateName, types.StringNull())
			}
			switch tt.change {
			case "metadata":
				config.Metadata = types.StringValue(`{"key":"value"}`)
				proposed.Metadata = config.Metadata
			case "source", "unknown":
				value := types.StringValue("replacement-source")
				if tt.byID {
					value = types.StringValue(core.UUID{0x93}.String())
				}
				if tt.change == "unknown" {
					value = types.StringUnknown()
				}
				if tt.byID {
					config.Volume = customImageVolume(types.StringNull(), value)
					proposed.Volume = customImageVolume(stateName, value)
				} else {
					config.Volume = customImageVolume(value, types.StringNull())
					proposed.Volume = customImageVolume(value, stateID)
				}
			case "disk":
				config.DiskFormat = types.StringValue("raw")
				proposed.DiskFormat = config.DiskFormat
			}
			dynamic := func(model CustomImageResourceModel) *tfprotov6.DynamicValue {
				state := tfsdk.State{Schema: resourceSchema}
				if diags := state.Set(ctx, &model); diags.HasError() {
					t.Fatal(diags)
				}
				raw := state.Raw
				value, err := tfprotov6.NewDynamicValue(raw.Type(), raw)
				if err != nil {
					t.Fatal(err)
				}
				return &value
			}
			server := providerserver.NewProtocol6(&customImagePlanningProvider{})()
			response, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "viettelcloud_custom_image", PriorState: dynamic(state), Config: dynamic(config), ProposedNewState: dynamic(proposed),
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, diagnostic := range response.Diagnostics {
				if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatalf("%s: %s", diagnostic.Summary, diagnostic.Detail)
				}
			}
			if (len(response.RequiresReplace) > 0) != tt.wantReplace {
				t.Fatalf("replacement paths=%v, want replacement=%t", response.RequiresReplace, tt.wantReplace)
			}
			planned := tfsdk.Plan{Schema: resourceSchema}
			planned.Raw, err = response.PlannedState.Unmarshal(resourceSchema.Type().TerraformType(ctx))
			if err != nil {
				t.Fatal(err)
			}
			var got CustomImageResourceModel
			if diags := planned.Get(ctx, &got); diags.HasError() {
				t.Fatal(diags)
			}
			source, prior := reference.Get(got.Volume, inferred), reference.Get(state.Volume, inferred)
			if source.IsUnknown() != tt.wantUnknown || (!tt.wantUnknown && !source.Equal(prior)) {
				t.Fatalf("inferred source=%s, wantUnknown=%t", source, tt.wantUnknown)
			}
			if got := reference.Get(got.Volume, configured); !got.Equal(reference.Get(config.Volume, configured)) {
				t.Fatalf("configured source=%s, want %s", got, reference.Get(config.Volume, configured))
			}
		})
	}
}

func TestCustomImageConfigureAndMetadata(t *testing.T) {
	t.Parallel()
	r := NewCustomImageResource().(*CustomImageResource)
	var meta resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &meta)
	if meta.TypeName != "viettelcloud_custom_image" {
		t.Fatal(meta.TypeName)
	}
	var response resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{}, &response)
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "wrong"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected type error")
	}
	response = resource.ConfigureResponse{}
	data := &providerdata.Configured{ProjectID: placementGroupProjectID}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: data}, &response)
	if response.Diagnostics.HasError() || r.projectID != data.ProjectID {
		t.Fatal(response.Diagnostics)
	}
}

func TestBuildCustomImageCreateBody(t *testing.T) {
	t.Parallel()
	for _, description := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue(""), types.StringValue("description")} {
		model := customImageBaseModel()
		model.Name = types.StringValue(" image-a ")
		model.Volume = customImageVolumeName(" boot-source ")
		model.Description = description
		model.ContainerFormat = types.StringUnknown()
		body, diags := buildCustomImageCreateBody(context.Background(), model, core.UUID{0x82})
		if diags.HasError() || body.Name != "image-a" || body.VolumeId != (core.UUID{0x82}) || body.ContainerFormat != serversdk.ImageCustomContainerFormatBare {
			t.Fatalf("%#v %v", body, diags)
		}
		raw, _ := json.Marshal(body)
		var values map[string]any
		_ = json.Unmarshal(raw, &values)
		_, present := values["description"]
		if present != (!description.IsNull() && !description.IsUnknown()) {
			t.Fatalf("presence: %s", raw)
		}
	}
}

func TestBuildCustomImageRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		modify func(*CustomImageResourceModel)
	}{
		{"name", func(m *CustomImageResourceModel) { m.Name = types.StringValue(" ") }},
		{"blank volume", func(m *CustomImageResourceModel) { m.Volume = customImageVolumeName(" ") }},
		{"invalid volume ID", func(m *CustomImageResourceModel) { m.Volume = customImageVolumeID("invalid") }},
		{"unknown disk", func(m *CustomImageResourceModel) { m.DiskFormat = types.StringUnknown() }},
		{"disk", func(m *CustomImageResourceModel) { m.DiskFormat = types.StringValue("vmdk") }},
		{"container", func(m *CustomImageResourceModel) { m.ContainerFormat = types.StringValue("ova") }},
		{"state", func(m *CustomImageResourceModel) { m.State = types.StringValue("invalid") }},
		{"metadata number", func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`{"a":1}`) }},
		{"metadata null", func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`{"a":null}`) }},
		{"metadata array", func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`[]`) }},
		{"metadata root null", func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`null`) }},
		{"invalid project", func(m *CustomImageResourceModel) { m.SharedProjects = reference.Set(reference.ID, []string{"wrong"}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := customImageBaseModel()
			test.modify(&model)
			_, diags := buildCustomImageCreateBody(context.Background(), model, core.UUID{0x82})
			if !diags.HasError() {
				t.Fatal("expected diagnostics")
			}
		})
	}
}

func TestBuildCustomImageUpdatePresence(t *testing.T) {
	t.Parallel()
	state := customImageBaseModel()
	state.Description = types.StringValue("old")
	state.OS = types.StringValue("Linux")
	state.Version = types.StringValue("1")
	state.Metadata = types.StringValue(`{"a":"b"}`)
	state.SharedProjects = reference.Set(reference.ID, []string{core.UUID{0x91}.String()})
	for _, value := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("")} {
		plan := state
		plan.Description = value
		plan.OS = value
		plan.Version = value
		plan.Metadata = types.StringNull()
		plan.SharedProjects = types.SetNull(reference.ObjectType(reference.ID))
		body, changed, diags := buildCustomImageUpdateBody(context.Background(), plan, state)
		clearing := !value.IsNull() && !value.IsUnknown()
		if diags.HasError() || changed != clearing || (body.Description != nil) != clearing || (body.Os != nil) != clearing || (body.Version != nil) != clearing || body.Metadata != nil || body.SharedProjectIds != nil {
			t.Fatalf("%#v %v", body, diags)
		}
	}
	plan := state
	plan.Name = types.StringValue(" renamed ")
	plan.State = types.StringValue("disabled")
	plan.Metadata = types.StringValue("{}")
	plan.SharedProjects = reference.Set(reference.ID, nil)
	body, changed, diags := buildCustomImageUpdateBody(context.Background(), plan, state)
	raw, _ := json.Marshal(body)
	if diags.HasError() || !changed || body.Name == nil || *body.Name != "renamed" || body.Metadata == nil || len(*body.Metadata) != 0 || body.SharedProjectIds == nil || len(*body.SharedProjectIds) != 0 || body.State == nil {
		t.Fatalf("%s %v", raw, diags)
	}
	if !strings.Contains(string(raw), `"metadata":{}`) || !strings.Contains(string(raw), `"shared_project_ids":[]`) {
		t.Fatal(string(raw))
	}
	plan = state
	plan.Name = types.StringValue(" image-a ")
	plan.Metadata = types.StringValue(`{ "a" : "b" }`)
	plan.SharedProjects = reference.Set(reference.ID, []string{strings.ToUpper(core.UUID{0x91}.String())})
	_, changed, diags = buildCustomImageUpdateBody(context.Background(), plan, state)
	if diags.HasError() || changed {
		t.Fatal("semantically equal values must not patch")
	}
	plan.Name = types.StringUnknown()
	_, _, diags = buildCustomImageUpdateBody(context.Background(), plan, state)
	if !diags.HasError() {
		t.Fatal("expected unknown name error")
	}
}

func TestCustomImageCreateAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	image := customImageFixture()
	post, patch, get := 0, 0, 0
	client := customImageClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Project-ID") != placementGroupProjectID.String() {
			t.Error("wrong project")
		}
		if req.URL.Path != "/v2/server/custom-images/" && req.URL.Path != "/v2/server/custom-images/"+image.Id.String()+"/" {
			t.Error(req.URL.Path)
		}
		switch req.Method {
		case http.MethodPost:
			post++
			var body serversdk.CustomImageCreateSchema
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.VolumeId != image.Volume.Id || body.Name != "image-a" {
				t.Errorf("bad create: %#v", body)
			}
			w.WriteHeader(http.StatusCreated)
			writeCustomImageResponse(t, w, image)
		case http.MethodPatch:
			patch++
			var body serversdk.CustomImagePartialUpdateSchema
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.Os != nil {
				image.Os = body.Os
			}
			if body.Name != nil {
				image.Name = *body.Name
			}
			if body.Description != nil {
				image.Description = *body.Description
			}
			writeCustomImageResponse(t, w, image)
		case http.MethodGet:
			get++
			writeCustomImageResponse(t, w, image)
		default:
			t.Error(req.Method)
		}
	})
	volumeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("name") != "boot-source" {
			t.Error(req.URL.String())
		}
		_ = json.NewEncoder(w).Encode(blockstoragesdk.PagedVolumeSchema{Count: 1, Results: []blockstoragesdk.VolumeSchema{{Id: image.Volume.Id, Name: "boot-source"}}})
	}))
	defer volumeServer.Close()
	blockStorage, err := blockstoragesdk.NewClient(volumeServer.URL, blockstoragesdk.WithPAT("test"))
	if err != nil {
		t.Fatal(err)
	}
	r := &CustomImageResource{client: client, blockStorage: blockStorage, projectID: placementGroupProjectID}
	model := customImageBaseModel()
	model.OS = types.StringValue("Linux")
	model.Name = types.StringValue(" image-a ")
	config := model
	config.Volume = customImageVolumeName("boot-source")
	response := resource.CreateResponse{State: tfsdk.State{Schema: customImageResourceSchema(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: customImagePlan(t, model), Config: tfsdk.Config(customImageState(t, config))}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state CustomImageResourceModel
	_ = response.State.Get(ctx, &state)
	if state.OS.ValueString() != "Linux" || state.ID.ValueString() != image.Id.String() || state.Name.ValueString() != " image-a " || post != 1 || patch != 1 || get != 1 {
		t.Fatalf("state=%#v counts=%d,%d,%d", state, post, patch, get)
	}
	plan := state
	plan.Name = types.StringValue("new-name")
	plan.Description = types.StringValue("updated")
	update := resource.UpdateResponse{State: response.State}
	r.Update(ctx, resource.UpdateRequest{State: response.State, Plan: customImagePlan(t, plan)}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	_ = update.State.Get(ctx, &state)
	if state.Name.ValueString() != "new-name" || state.ID.ValueString() != image.Id.String() || patch != 2 {
		t.Fatal(state)
	}
	plan = state
	plan.Description = types.StringUnknown()
	r.Update(ctx, resource.UpdateRequest{State: update.State, Plan: customImagePlan(t, plan)}, &update)
	if update.Diagnostics.HasError() || patch != 2 || get != 2 {
		t.Fatalf("%v patch=%d get=%d", update.Diagnostics, patch, get)
	}
}

func TestCustomImageCreateByVolumeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	image := customImageFixture()
	image.Owner = &serversdk.NestedUserSchema{Id: core.UUID{0x92}, Username: "image-owner"}
	image.Volume.Id = core.UUID{0xab}
	volumeCalls, creates := 0, 0
	volumeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		volumeCalls++
		if req.Method != http.MethodGet || req.URL.Path != "/v2/block-storage/volumes/"+image.Volume.Id.String()+"/" || req.Header.Get("Project-ID") != placementGroupProjectID.String() {
			t.Errorf("unexpected volume lookup: %s %s", req.Method, req.URL)
		}
		_ = json.NewEncoder(w).Encode(blockstoragesdk.VolumeDetailSchema{Id: image.Volume.Id, ProjectId: placementGroupProjectID, Name: image.Volume.Name})
	}))
	defer volumeServer.Close()
	blockStorage, err := blockstoragesdk.NewClient(volumeServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := customImageClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			creates++
			var body serversdk.CustomImageCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.VolumeId != image.Volume.Id {
				t.Error(body.VolumeId)
			}
			w.WriteHeader(http.StatusCreated)
		}
		writeCustomImageResponse(t, w, image)
	})
	config := customImageBaseModel()
	configuredID := types.StringValue(strings.ToUpper(image.Volume.Id.String()))
	config.Volume = customImageVolume(types.StringNull(), configuredID)
	plan := config
	plan.Volume = customImageVolume(types.StringUnknown(), configuredID)
	r := &CustomImageResource{client: client, blockStorage: blockStorage, projectID: placementGroupProjectID}
	response := resource.CreateResponse{State: tfsdk.State{Schema: customImageResourceSchema(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: customImagePlan(t, plan), Config: tfsdk.Config(customImageState(t, config))}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state CustomImageResourceModel
	if diags := response.State.Get(ctx, &state); diags.HasError() {
		t.Fatal(diags)
	}
	if state.ID.ValueString() != image.Id.String() || !state.Volume.Equal(customImageVolume(types.StringValue(image.Volume.Name), configuredID)) || volumeCalls != 1 || creates != 1 {
		t.Fatalf("state=%#v volumeCalls=%d creates=%d", state, volumeCalls, creates)
	}
	if state.Project.ValueString() != image.Project.Name || state.OwnerID.ValueString() != image.Owner.Id.String() || state.Owner.ValueString() != image.Owner.Username {
		t.Fatalf("ownership outputs differ from the API: %#v", state)
	}
	image.Volume = nil
	image.Owner = nil
	read := resource.ReadResponse{State: response.State}
	r.Read(ctx, resource.ReadRequest{State: response.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if diags := read.State.Get(ctx, &state); diags.HasError() {
		t.Fatal(diags)
	}
	if !state.Volume.Equal(customImageVolume(types.StringValue("boot-source"), configuredID)) {
		t.Fatalf("removed source lost its creation reference: %#v", state)
	}
	if !state.Owner.IsNull() || !state.OwnerID.IsNull() || state.Project.ValueString() != image.Project.Name {
		t.Fatalf("ownership outputs did not refresh from the API: %#v", state)
	}
}

func TestCustomImageReadDeleteImport(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := customImageClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
			r := &CustomImageResource{client: client, projectID: placementGroupProjectID}
			state := customImageState(t, customImageBaseModel())
			read := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
			del := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &del)
			if read.Diagnostics.HasError() != (status != 404) || del.Diagnostics.HasError() != (status != 404) {
				t.Fatalf("read=%v delete=%v", read.Diagnostics, del.Diagnostics)
			}
			if status == 404 && !read.State.Raw.IsNull() {
				t.Fatal("not-found state retained")
			}
		})
	}
	r := &CustomImageResource{}
	response := resource.ImportStateResponse{State: customImageState(t, CustomImageResourceModel{})}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: customImageFixture().Id.String()}, &response)
	var id types.String
	_ = response.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if response.Diagnostics.HasError() || id.ValueString() != customImageFixture().Id.String() {
		t.Fatal(response.Diagnostics, id)
	}
	response = resource.ImportStateResponse{}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("invalid import accepted")
	}
}

func TestCustomImageResolveSourceVolume(t *testing.T) {
	t.Parallel()
	id := core.UUID{0xab}
	for _, tt := range []struct {
		name      string
		volume    types.String
		volumeID  types.String
		null      bool
		status    int
		count     int
		wantError bool
		wantCalls int
	}{
		{name: "name", volume: types.StringValue(" boot-source "), status: 200, count: 1, wantCalls: 1},
		{name: "UUID-shaped name", volume: types.StringValue(id.String()), status: 200, count: 1, wantCalls: 1},
		{name: "ID", volumeID: types.StringValue(strings.ToUpper(id.String())), status: 200, wantCalls: 1},
		{name: "missing name", volume: types.StringValue("boot-source"), status: 200, wantError: true, wantCalls: 1},
		{name: "ambiguous name", volume: types.StringValue("boot-source"), status: 200, count: 2, wantError: true, wantCalls: 1},
		{name: "name API error", volume: types.StringValue("boot-source"), status: 500, wantError: true, wantCalls: 1},
		{name: "ID not found", volumeID: types.StringValue(id.String()), status: 404, wantError: true, wantCalls: 1},
		{name: "ID forbidden", volumeID: types.StringValue(id.String()), status: 403, wantError: true, wantCalls: 1},
		{name: "ID API error", volumeID: types.StringValue(id.String()), status: 500, wantError: true, wantCalls: 1},
		{name: "no source", wantError: true},
		{name: "null reference", null: true, wantError: true},
		{name: "both sources", volume: types.StringValue("boot-source"), volumeID: types.StringValue(id.String()), wantError: true},
		{name: "blank name", volume: types.StringValue(" "), wantError: true},
		{name: "unknown name", volume: types.StringUnknown(), wantError: true},
		{name: "invalid ID", volumeID: types.StringValue("invalid"), wantError: true},
		{name: "unknown ID", volumeID: types.StringUnknown(), wantError: true},
		{name: "unknown name and known ID", volume: types.StringUnknown(), volumeID: types.StringValue(id.String()), wantError: true},
		{name: "known name and unknown ID", volume: types.StringValue("boot-source"), volumeID: types.StringUnknown(), wantError: true},
		{name: "both unknown sources", volume: types.StringUnknown(), volumeID: types.StringUnknown(), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != http.MethodGet || req.Header.Get("Project-ID") != placementGroupProjectID.String() {
					t.Errorf("wrong request scope: %s %s", req.Method, req.URL)
				}
				w.WriteHeader(tt.status)
				if tt.status != 200 {
					return
				}
				if !tt.volumeID.IsNull() {
					if req.URL.Path != "/v2/block-storage/volumes/"+id.String()+"/" {
						t.Error(req.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(blockstoragesdk.VolumeDetailSchema{Id: id, ProjectId: placementGroupProjectID})
					return
				}
				name := strings.TrimSpace(tt.volume.ValueString())
				if req.URL.Path != "/v2/block-storage/volumes/" || req.URL.Query().Get("name") != name {
					t.Error(req.URL.String())
				}
				volumes := make([]blockstoragesdk.VolumeSchema, tt.count)
				for i := range volumes {
					volumes[i] = blockstoragesdk.VolumeSchema{Id: id, Name: name}
				}
				_ = json.NewEncoder(w).Encode(blockstoragesdk.PagedVolumeSchema{Count: tt.count, Results: volumes})
			}))
			defer server.Close()
			client, err := blockstoragesdk.NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			r := &CustomImageResource{blockStorage: client, projectID: placementGroupProjectID}
			config := CustomImageResourceModel{Volume: customImageVolume(tt.volume, tt.volumeID)}
			if tt.null {
				config.Volume = types.ObjectNull(reference.AttributeTypes(customImageVolumeCriteria...))
			}
			got, diags := r.resolveSourceVolume(context.Background(), config)
			if diags.HasError() != tt.wantError || calls != tt.wantCalls || (!tt.wantError && got != id) {
				t.Fatalf("ID=%s calls=%d diagnostics=%v", got, calls, diags)
			}
		})
	}
}

func TestCustomImageWaiters(t *testing.T) {
	for _, test := range []struct {
		name     string
		statuses []serversdk.ImageStatus
		deleted  bool
		wantErr  bool
		timeout  bool
	}{
		{name: "ready", statuses: []serversdk.ImageStatus{serversdk.ImageStatusSaving, serversdk.ImageStatusActive}},
		{name: "killed", statuses: []serversdk.ImageStatus{serversdk.ImageStatusKilled}, wantErr: true},
		{name: "unknown", statuses: []serversdk.ImageStatus{"unknown"}, wantErr: true},
		{name: "timeout", statuses: []serversdk.ImageStatus{serversdk.ImageStatusSaving}, wantErr: true, timeout: true},
		{name: "deleted status", statuses: []serversdk.ImageStatus{serversdk.ImageStatusPendingDelete, serversdk.ImageStatusDeleted}, deleted: true},
		{name: "deleted 404", deleted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				image := customImageFixture()
				client, clientErr := serversdk.NewClient("https://image.test", serversdk.WithHTTPClient(&http.Client{Transport: customImageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					status := http.StatusOK
					raw := []byte(`{}`)
					if len(test.statuses) == 0 {
						status = 404
					} else {
						index := min(calls, len(test.statuses)-1)
						calls++
						image.Status = test.statuses[index]
						raw, _ = json.Marshal(image)
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw))), Request: req}, nil
				})}))
				if clientErr != nil {
					t.Fatal(clientErr)
				}
				r := &CustomImageResource{client: client, projectID: placementGroupProjectID}
				start := time.Now()
				var err error
				if test.deleted {
					err = r.waitUntilDeleted(context.Background(), image.Id)
				} else {
					_, err = r.waitUntilReady(context.Background(), image.Id)
				}
				if (err != nil) != test.wantErr {
					t.Fatalf("%v", err)
				}
				if test.timeout && (!errors.Is(err, wait.ErrTimeout) || time.Since(start) != customImageTimeout) {
					t.Fatalf("timeout=%v elapsed=%v", err, time.Since(start))
				}
				if len(test.statuses) > 1 && time.Since(start) != customImagePollInterval {
					t.Fatal(time.Since(start))
				}
			})
		})
	}
}

func TestCustomImageCreateFailureRetainsIdentity(t *testing.T) {
	t.Parallel()
	image := customImageFixture()
	client := customImageClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			w.WriteHeader(201)
			writeCustomImageResponse(t, w, image)
			return
		}
		image.Status = serversdk.ImageStatusKilled
		writeCustomImageResponse(t, w, image)
	})
	volumeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(blockstoragesdk.PagedVolumeSchema{Count: 1, Results: []blockstoragesdk.VolumeSchema{{Id: core.UUID{0x82}, Name: "boot-source"}}})
	}))
	defer volumeServer.Close()
	blockStorage, err := blockstoragesdk.NewClient(volumeServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := &CustomImageResource{client: client, blockStorage: blockStorage, projectID: placementGroupProjectID}
	config := customImageBaseModel()
	config.Volume = customImageVolumeName("boot-source")
	response := resource.CreateResponse{State: tfsdk.State{Schema: customImageResourceSchema(t)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: customImagePlan(t, customImageBaseModel()), Config: tfsdk.Config(customImageState(t, config))}, &response)
	var id types.String
	_ = response.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if !response.Diagnostics.HasError() || id.ValueString() != image.Id.String() {
		t.Fatalf("%v %v", response.Diagnostics, id)
	}
}

type customImageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f customImageRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestCustomImageValidateConfig(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		modify    func(*CustomImageResourceModel)
		wantError bool
	}{
		{name: "valid", modify: func(*CustomImageResourceModel) {}},
		{name: "unknown reference", modify: func(m *CustomImageResourceModel) {
			m.Volume = types.ObjectUnknown(reference.AttributeTypes(customImageVolumeCriteria...))
		}},
		{name: "unknown name", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringUnknown(), types.StringNull())
		}},
		{name: "ID reference", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolumeID(customImageFixture().Volume.Id.String())
		}},
		{name: "unknown ID reference", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringNull(), types.StringUnknown())
		}},
		{name: "no reference", modify: func(m *CustomImageResourceModel) {
			m.Volume = types.ObjectNull(reference.AttributeTypes(customImageVolumeCriteria...))
		}, wantError: true},
		{name: "no criterion", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringNull(), types.StringNull())
		}, wantError: true},
		{name: "both references", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringValue("boot-source"), types.StringValue(customImageFixture().Volume.Id.String()))
		}, wantError: true},
		{name: "unknown and known references", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringValue("boot-source"), types.StringUnknown())
		}},
		{name: "unknown name and known ID", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringUnknown(), types.StringValue(customImageFixture().Volume.Id.String()))
		}},
		{name: "both unknown references", modify: func(m *CustomImageResourceModel) {
			m.Volume = customImageVolume(types.StringUnknown(), types.StringUnknown())
		}},
		{name: "blank volume", modify: func(m *CustomImageResourceModel) { m.Volume = customImageVolumeName(" ") }, wantError: true},
		{name: "invalid volume ID", modify: func(m *CustomImageResourceModel) { m.Volume = customImageVolumeID("invalid") }, wantError: true},
		{name: "invalid shared project", modify: func(m *CustomImageResourceModel) { m.SharedProjects = reference.Set(reference.ID, []string{"invalid"}) }, wantError: true},
		{name: "blank name", modify: func(m *CustomImageResourceModel) { m.Name = types.StringValue(" ") }, wantError: true},
		{name: "invalid metadata", modify: func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`{"key": false}`) }, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := customImageBaseModel()
			model.Volume = customImageVolumeName("boot-source")
			test.modify(&model)
			state := customImageState(t, model)
			var response resource.ValidateConfigResponse
			(&CustomImageResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(state)}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}

func TestCustomImageRefreshAndDelete(t *testing.T) {
	t.Parallel()
	image := customImageFixture()
	deleted := false
	client := customImageClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(204)
			return
		}
		if deleted {
			w.WriteHeader(404)
			return
		}
		image.Description = "external change"
		image.Volume = nil
		writeCustomImageResponse(t, w, image)
	})
	r := &CustomImageResource{client: client, projectID: placementGroupProjectID}
	state := customImageState(t, customImageBaseModel())
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var got CustomImageResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got.Description.ValueString() != "external change" || !got.Volume.Equal(customImageVolume(types.StringValue("boot-source"), types.StringValue(customImageFixture().Volume.Id.String()))) {
		t.Fatal(got)
	}
	del := resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: response.State}, &del)
	if del.Diagnostics.HasError() || !deleted {
		t.Fatal(del.Diagnostics)
	}
	image.Status = serversdk.ImageStatusDeleted
	r.client = customImageClient(t, func(w http.ResponseWriter, _ *http.Request) { writeCustomImageResponse(t, w, image) })
	response = resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
		t.Fatal(response.Diagnostics)
	}
}

func TestCustomImageUpdateFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		status    int
		changedID bool
	}{
		{name: "API error", status: 500}, {name: "identity mismatch", status: 200, changedID: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := customImageFixture()
			client := customImageClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if test.changedID {
					image.Id = core.UUID{0x99}
				}
				w.WriteHeader(test.status)
				if test.status == 200 {
					writeCustomImageResponse(t, w, image)
				}
			})
			model := customImageBaseModel()
			plan := model
			plan.Name = types.StringValue("new-name")
			state := customImageState(t, model)
			response := resource.UpdateResponse{State: state}
			(&CustomImageResource{client: client, projectID: placementGroupProjectID}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: customImagePlan(t, plan)}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected diagnostic")
			}
		})
	}
}

func TestCustomImageUpdateInvalidAndUnknownOptionalValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		modify func(*CustomImageResourceModel)
	}{
		{"state", func(m *CustomImageResourceModel) { m.State = types.StringValue("invalid") }},
		{"metadata", func(m *CustomImageResourceModel) { m.Metadata = types.StringValue(`{"a": []}`) }},
		{"project UUID", func(m *CustomImageResourceModel) { m.SharedProjects = reference.Set(reference.ID, []string{"invalid"}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := customImageBaseModel()
			plan := state
			test.modify(&plan)
			_, _, diags := buildCustomImageUpdateBody(context.Background(), plan, state)
			if !diags.HasError() {
				t.Fatal("expected diagnostic")
			}
		})
	}
	state := customImageBaseModel()
	plan := state
	plan.State = types.StringUnknown()
	plan.Metadata = types.StringUnknown()
	plan.SharedProjects = types.SetUnknown(reference.ObjectType(reference.ID))
	body, changed, diags := buildCustomImageUpdateBody(context.Background(), plan, state)
	if diags.HasError() || changed || body.State != nil || body.Metadata != nil || body.SharedProjectIds != nil {
		t.Fatal(body, diags)
	}
}

type customImagePlanningProvider struct{}

func (p *customImagePlanningProvider) Metadata(_ context.Context, _ frameworkprovider.MetadataRequest, resp *frameworkprovider.MetadataResponse) {
	resp.TypeName = "viettelcloud"
}

func (p *customImagePlanningProvider) Schema(_ context.Context, _ frameworkprovider.SchemaRequest, resp *frameworkprovider.SchemaResponse) {
	resp.Schema = providerschema.Schema{}
}

func (p *customImagePlanningProvider) Configure(context.Context, frameworkprovider.ConfigureRequest, *frameworkprovider.ConfigureResponse) {
}

func (p *customImagePlanningProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewCustomImageResource}
}

func (p *customImagePlanningProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}
