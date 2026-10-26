package server

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func TestPopulateCustomImageModel(t *testing.T) {
	t.Parallel()
	image := customImageFixture()
	image.Description = ""
	image.Os = new("")
	image.Version = new("v1")
	image.Size = new(0)
	image.MinVolumeSize = new(20)
	image.Metadata = &map[string]any{"key": "value", "backend": float64(1)}
	image.SharedProjects = &[]serversdk.NestedProjectSchema{{Id: core.UUID{0x91}, Name: "shared"}}
	image.Owner = &serversdk.NestedUserSchema{Id: core.UUID{0x92}, Username: "image-owner"}
	model := CustomImageDataSourceModel{CustomImageModel: CustomImageModel{Name: types.StringValue(" image-a "), Region: types.StringValue(" region-a ")}, Volume: types.StringValue(" boot-source ")}
	populateCustomImageDataSourceState(image, &model)
	if model.Project.ValueString() != image.Project.Name || model.OwnerID.ValueString() != image.Owner.Id.String() || model.Owner.ValueString() != image.Owner.Username {
		t.Fatalf("ownership outputs differ from the API: %#v", model)
	}
	if model.ID.ValueString() != image.Id.String() || model.Name.ValueString() != " image-a " || model.Volume.ValueString() != " boot-source " || model.Region.ValueString() != " region-a " || model.OS.IsNull() || model.OS.ValueString() != "" || model.Size.IsNull() || model.Size.ValueInt64() != 0 || model.MinVolumeSize.ValueInt64() != 20 || model.ProjectID.ValueString() != image.Project.Id.String() || model.EncryptKeySize.ValueString() != "0" || len(model.SharedProjectIDs.Elements()) != 1 || model.Metadata.ValueString() != `{"backend":1,"key":"value"}` || model.CreatedAt.ValueString() != "2026-01-02T03:04:05Z" {
		t.Fatalf("%#v", model)
	}
	image.Volume = nil
	image.Os = nil
	image.Version = nil
	image.Size = nil
	image.MinVolumeSize = nil
	image.Metadata = nil
	image.SharedProjects = nil
	image.Owner = nil
	populateCustomImageDataSourceState(image, &model)
	if !model.Owner.IsNull() || !model.OwnerID.IsNull() {
		t.Fatalf("absent owner must clear prior ownership outputs: %#v", model)
	}
	if !model.Volume.IsNull() || !model.VolumeID.IsNull() || !model.OS.IsNull() || !model.Version.IsNull() || !model.Size.IsNull() || !model.MinVolumeSize.IsNull() || !model.Metadata.IsNull() || !model.SharedProjectIDs.IsNull() {
		t.Fatalf("nil pointers did not map to null: %#v", model)
	}
	image.SharedProjects = &[]serversdk.NestedProjectSchema{}
	image.Metadata = &map[string]any{}
	populateCustomImageDataSourceState(image, &model)
	if model.SharedProjectIDs.IsNull() || len(model.SharedProjectIDs.Elements()) != 0 || model.Metadata.ValueString() != "{}" {
		t.Fatal(model)
	}
	// Empty collections are distinct from absent response fields.
	configured, _ := types.SetValueFrom(context.Background(), types.StringType, []string{})
	if !model.SharedProjectIDs.Equal(configured) {
		t.Fatal(model.SharedProjectIDs)
	}
	image.Owner = &serversdk.NestedUserSchema{Id: core.UUID{0x93}, Username: ""}
	populateCustomImageDataSourceState(image, &model)
	if model.Owner.IsNull() || model.Owner.ValueString() != "" || model.OwnerID.ValueString() != image.Owner.Id.String() {
		t.Fatalf("present owner with an empty username must stay known: %#v", model)
	}
}

func TestCustomImageConfiguredRepresentations(t *testing.T) {
	t.Parallel()
	image := customImageFixture()
	image.Id = core.UUID{0xab}
	image.Volume.Id = core.UUID{0xcd}
	image.Os = new("Linux")
	image.Version = new("1")
	image.Metadata = &map[string]any{"a": "b"}
	image.SharedProjects = &[]serversdk.NestedProjectSchema{{Id: core.UUID{0xef}}}
	configuredIDs, _ := types.SetValueFrom(context.Background(), types.StringType, []string{strings.ToUpper(core.UUID{0xef}.String())})
	state := CustomImageDataSourceModel{CustomImageModel: CustomImageModel{
		ID: types.StringValue(strings.ToUpper(image.Id.String())),
		OS: types.StringValue(" Linux "), Version: types.StringValue(" 1 "), Metadata: types.StringValue(`{ "a" : "b" }`),
	}, VolumeID: types.StringValue(strings.ToUpper(image.Volume.Id.String())), SharedProjectIDs: configuredIDs}
	populateCustomImageDataSourceState(image, &state)
	if state.OS.ValueString() != " Linux " || state.Version.ValueString() != " 1 " || state.Metadata.ValueString() != `{ "a" : "b" }` || state.ID.ValueString() != strings.ToUpper(image.Id.String()) || state.VolumeID.ValueString() != strings.ToUpper(image.Volume.Id.String()) || !state.SharedProjectIDs.Equal(configuredIDs) {
		t.Fatal(state)
	}
	image.Name = "real drift"
	image.Os = new("Windows")
	populateCustomImageDataSourceState(image, &state)
	if state.Name.ValueString() != "real drift" || state.OS.ValueString() != "Windows" {
		t.Fatal(state)
	}
}

func TestPopulateCustomImageResourceStateReferences(t *testing.T) {
	t.Parallel()
	image := customImageFixture()
	image.SharedProjects = &[]serversdk.NestedProjectSchema{{Id: core.UUID{0xef}, Name: "shared"}}
	volumeID, projectID := image.Volume.Id.String(), core.UUID{0xef}.String()

	var imported CustomImageResourceModel
	populateCustomImageResourceState(image, &imported)
	if !imported.Volume.Equal(customImageVolume(types.StringValue("boot-source"), types.StringValue(volumeID))) ||
		!imported.SharedProjects.Equal(reference.Set(reference.ID, []string{projectID})) {
		t.Fatalf("import must record both volume criteria and the shared projects: %s %s", imported.Volume, imported.SharedProjects)
	}

	configuredProjects := reference.Set(reference.ID, []string{strings.ToUpper(projectID)})
	for _, configured := range []types.Object{
		customImageVolume(types.StringValue(" boot-source "), types.StringUnknown()),
		customImageVolume(types.StringUnknown(), types.StringValue(strings.ToUpper(volumeID))),
	} {
		state := CustomImageResourceModel{Volume: configured, SharedProjects: configuredProjects}
		populateCustomImageResourceState(image, &state)
		name, id := reference.Get(state.Volume, reference.Name), reference.Get(state.Volume, reference.ID)
		wantName, wantID := reference.Get(configured, reference.Name), reference.Get(configured, reference.ID)
		if wantName.IsUnknown() {
			wantName = types.StringValue("boot-source")
		}
		if wantID.IsUnknown() {
			wantID = types.StringValue(volumeID)
		}
		if !name.Equal(wantName) || !id.Equal(wantID) || !state.SharedProjects.Equal(configuredProjects) {
			t.Fatalf("configured representations were not preserved: %s %s", state.Volume, state.SharedProjects)
		}
	}

	drift := CustomImageResourceModel{Volume: customImageVolumeName("other"), SharedProjects: reference.Set(reference.ID, []string{core.UUID{0x99}.String()})}
	populateCustomImageResourceState(image, &drift)
	if !drift.Volume.Equal(imported.Volume) || !drift.SharedProjects.Equal(imported.SharedProjects) {
		t.Fatalf("backend values must replace different references: %s %s", drift.Volume, drift.SharedProjects)
	}

	image.Volume = nil
	image.SharedProjects = nil
	removed := CustomImageResourceModel{Volume: customImageVolume(types.StringValue("source removed"), types.StringUnknown())}
	populateCustomImageResourceState(image, &removed)
	if !removed.Volume.Equal(customImageVolumeName("source removed")) || !removed.SharedProjects.IsNull() {
		t.Fatalf("a removed source must keep its configured reference: %s %s", removed.Volume, removed.SharedProjects)
	}
	var importedWithoutSource CustomImageResourceModel
	populateCustomImageResourceState(image, &importedWithoutSource)
	if !importedWithoutSource.Volume.IsNull() {
		t.Fatalf("an import without a source volume must leave volume null: %s", importedWithoutSource.Volume)
	}
}
