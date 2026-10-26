package server

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type CustomImageModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	DiskFormat      types.String `tfsdk:"disk_format"`
	ContainerFormat types.String `tfsdk:"container_format"`
	OS              types.String `tfsdk:"os"`
	Version         types.String `tfsdk:"version"`
	State           types.String `tfsdk:"state"`
	Status          types.String `tfsdk:"status"`
	// Metadata remains JSON because reads can contain non-string backend values,
	// while the public PATCH body accepts only string values.
	Metadata       types.String `tfsdk:"metadata"`
	Size           types.Int64  `tfsdk:"size"`
	MinVolumeSize  types.Int64  `tfsdk:"min_volume_size"`
	EncryptKeySize types.String `tfsdk:"encrypt_key_size"`
	Region         types.String `tfsdk:"region"`
	ProjectID      types.String `tfsdk:"project_id"`
	Project        types.String `tfsdk:"project"`
	OwnerID        types.String `tfsdk:"owner_id"`
	Owner          types.String `tfsdk:"owner"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

type CustomImageResourceModel struct {
	CustomImageModel
	Volume         types.Object `tfsdk:"volume"`
	SharedProjects types.Set    `tfsdk:"shared_projects"`
}

type CustomImageDataSourceModel struct {
	CustomImageModel
	Volume           types.String `tfsdk:"volume"`
	VolumeID         types.String `tfsdk:"volume_id"`
	SharedProjectIDs types.Set    `tfsdk:"shared_project_ids"`
}

// customImageVolumeCriteria are the criteria that select the source volume of
// a custom image resource.
var customImageVolumeCriteria = []string{reference.Name, reference.ID}

func populateCustomImageModel(image *serversdk.CustomImageSchema, m *CustomImageModel) {
	prior := *m
	m.ID = types.StringValue(image.Id.String())
	m.Name = types.StringValue(image.Name)
	m.Description = types.StringValue(image.Description)
	m.DiskFormat = types.StringValue(string(image.DiskFormat))
	m.ContainerFormat = types.StringValue(string(image.ContainerFormat))
	m.OS = types.StringPointerValue(image.Os)
	m.Version = types.StringPointerValue(image.Version)
	m.State = types.StringValue(string(image.State))
	m.Status = types.StringValue(string(image.Status))
	m.Metadata = types.StringNull()
	if image.Metadata != nil {
		raw, _ := json.Marshal(*image.Metadata)
		m.Metadata = types.StringValue(string(raw))
	}
	m.Size = types.Int64Null()
	if image.Size != nil {
		m.Size = types.Int64Value(int64(*image.Size))
	}
	m.MinVolumeSize = types.Int64Null()
	if image.MinVolumeSize != nil {
		m.MinVolumeSize = types.Int64Value(int64(*image.MinVolumeSize))
	}
	m.EncryptKeySize = types.StringValue(string(image.EncryptKeySize))
	m.Region = types.StringValue(image.Region.Name)
	m.ProjectID = types.StringValue(image.Project.Id.String())
	m.Project = types.StringValue(image.Project.Name)
	m.OwnerID = types.StringNull()
	m.Owner = types.StringNull()
	if image.Owner != nil {
		m.OwnerID = types.StringValue(image.Owner.Id.String())
		m.Owner = types.StringValue(image.Owner.Username)
	}
	m.CreatedAt = types.StringValue(image.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(image.UpdatedAt.Format(time.RFC3339))
	for _, pair := range []struct {
		before types.String
		after  *types.String
	}{
		{prior.Name, &m.Name}, {prior.Region, &m.Region},
	} {
		preserveTrimmedString(pair.before, pair.after)
	}
	if compare.SameUUID(prior.ID, image.Id) {
		m.ID = prior.ID
	}
	if compare.SameJSON(prior.Metadata, m.Metadata) {
		m.Metadata = prior.Metadata
	}
}

func populateCustomImageResourceState(image *serversdk.CustomImageSchema, m *CustomImageResourceModel) {
	source, sharedProjects := m.Volume, m.SharedProjects
	populateCustomImageModel(image, &m.CustomImageModel)
	priorName, priorID := reference.Get(source, reference.Name), reference.Get(source, reference.ID)
	name, id := customImageSourceVolume(image, priorName, priorID)
	m.Volume = reference.Of(map[string]types.String{reference.Name: name, reference.ID: id})
	// The API can omit a removed source volume. Keep the creation reference so
	// deleting the source does not force replacement of its existing image.
	if image.Volume == nil {
		m.Volume = types.ObjectNull(reference.AttributeTypes(customImageVolumeCriteria...))
		if !source.IsNull() && !source.IsUnknown() {
			m.Volume = reference.Of(map[string]types.String{reference.Name: knownOrNull(priorName), reference.ID: knownOrNull(priorID)})
		}
	}
	ids := customImageSharedProjectIDs(image)
	m.SharedProjects = customImageSharedProjects(ids)
	if compare.SameUUIDSet(customImageSharedProjectIDSet(sharedProjects), ids) {
		m.SharedProjects = sharedProjects
	}
}

func populateCustomImageDataSourceState(image *serversdk.CustomImageSchema, state *CustomImageDataSourceModel) {
	configuredOS, configuredVersion := state.OS, state.Version
	sharedProjectIDs := state.SharedProjectIDs
	populateCustomImageModel(image, &state.CustomImageModel)
	state.Volume, state.VolumeID = customImageSourceVolume(image, state.Volume, state.VolumeID)
	state.SharedProjectIDs = customImageSharedProjectIDs(image)
	if compare.SameUUIDSet(sharedProjectIDs, state.SharedProjectIDs) {
		state.SharedProjectIDs = sharedProjectIDs
	}
	for _, pair := range []struct {
		configured types.String
		target     *types.String
	}{{configuredOS, &state.OS}, {configuredVersion, &state.Version}} {
		if !pair.configured.IsNull() && !pair.configured.IsUnknown() && !pair.target.IsNull() && strings.TrimSpace(pair.configured.ValueString()) == pair.target.ValueString() {
			*pair.target = pair.configured
		}
	}
}

// customImageSourceVolume returns the name and UUID of the image's source
// volume, or nulls when the API omits it. A prior value that names the same
// volume keeps its representation.
func customImageSourceVolume(image *serversdk.CustomImageSchema, priorName, priorID types.String) (types.String, types.String) {
	if image.Volume == nil {
		return types.StringNull(), types.StringNull()
	}
	name, id := types.StringValue(image.Volume.Name), types.StringValue(image.Volume.Id.String())
	preserveTrimmedString(priorName, &name)
	if compare.SameUUID(priorID, image.Volume.Id) {
		id = priorID
	}
	return name, id
}

// customImageSharedProjectIDs returns the UUIDs of the projects that share the
// image as a set of strings, or a null set when the API omits the field.
func customImageSharedProjectIDs(image *serversdk.CustomImageSchema) types.Set {
	if image.SharedProjects == nil {
		return types.SetNull(types.StringType)
	}
	ids := make([]attr.Value, 0, len(*image.SharedProjects))
	for _, project := range *image.SharedProjects {
		ids = append(ids, types.StringValue(project.Id.String()))
	}
	return types.SetValueMust(types.StringType, ids)
}

// customImageSharedProjects returns a set of project UUID strings as a
// shared_projects set of references, keeping a null set.
func customImageSharedProjects(ids types.Set) types.Set {
	if ids.IsNull() {
		return types.SetNull(reference.ObjectType(reference.ID))
	}
	projects := make([]attr.Value, 0, len(ids.Elements()))
	for _, element := range ids.Elements() {
		id, _ := element.(types.String)
		projects = append(projects, reference.Value(reference.ID, id))
	}
	return types.SetValueMust(reference.ObjectType(reference.ID), projects)
}

// customImageSharedProjectIDSet returns the project UUIDs of a shared_projects
// set as a set of strings, keeping a null or unknown set.
func customImageSharedProjectIDSet(projects types.Set) types.Set {
	if projects.IsNull() {
		return types.SetNull(types.StringType)
	}
	if projects.IsUnknown() {
		return types.SetUnknown(types.StringType)
	}
	ids := reference.Strings(projects, reference.ID)
	elements := make([]attr.Value, 0, len(ids))
	for _, id := range ids {
		elements = append(elements, id)
	}
	return types.SetValueMust(types.StringType, elements)
}

// preserveTrimmedString keeps a prior value whose trimmed form equals the
// backend value in target.
func preserveTrimmedString(prior types.String, target *types.String) {
	if !prior.IsNull() && !prior.IsUnknown() && strings.TrimSpace(prior.ValueString()) == target.ValueString() {
		*target = prior
	}
}

// knownOrNull returns null for an unknown value.
func knownOrNull(value types.String) types.String {
	if value.IsUnknown() {
		return types.StringNull()
	}
	return value
}
