package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VolumeDataSource{}

type VolumeDataSource struct {
	client    *blockstoragesdk.Client
	projectID core.UUID
}

// volumeCriteria records which lookup arguments the configuration supplied.
type volumeCriteria struct {
	id, name, size, status, bootable, zone bool
}

func NewVolumeDataSource() datasource.DataSource {
	return &VolumeDataSource{}
}

func (d *VolumeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume"
}

func (d *VolumeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud Block Storage volume by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Optional: true, Computed: true, Description: "Volume ID (UUID)."},
			"name":        schema.StringAttribute{Optional: true, Computed: true, Description: "Volume name to filter by."},
			"size":        schema.Int64Attribute{Optional: true, Computed: true, Description: "Volume size in GiB to filter by."},
			"status":      schema.StringAttribute{Optional: true, Computed: true, Description: "Volume status to filter by."},
			"bootable":    schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether to filter for bootable volumes."},
			"zone":        schema.StringAttribute{Optional: true, Computed: true, Description: "Zone name to filter by."},
			"description": schema.StringAttribute{Computed: true, Description: "Volume description."},
			"encrypted":   schema.BoolAttribute{Computed: true, Description: "Whether the volume is encrypted."},
			"zone_id":     schema.StringAttribute{Computed: true, Description: "Zone ID (UUID) where the volume resides."},
			"volume_type": schema.StringAttribute{Computed: true, Description: "Effective volume type name."},
			"project_id":  schema.StringAttribute{Computed: true, Description: "Project ID (UUID) owning the volume."},
			"created_at":  schema.StringAttribute{Computed: true, Description: "Timestamp when the volume was created (RFC3339)."},
			"updated_at":  schema.StringAttribute{Computed: true, Description: "Timestamp when the volume was last updated (RFC3339)."},
			"iops":        schema.Int64Attribute{Computed: true, Description: "Provisioned IOPS."},
		},
	}
}

func (d *VolumeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.BlockStorage
	d.projectID = data.ProjectID
}

func (d *VolumeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VolumeDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	volume, diags := d.getVolume(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || volume == nil {
		return
	}

	state := config
	populateVolumeDataSourceState(volume, &state)
	preserveVolumeConfiguredValues(volume, &config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VolumeDataSource) getVolume(ctx context.Context, config VolumeDataSourceModel) (*blockstoragesdk.VolumeDetailSchema, diag.Diagnostics) {
	criteria, diags := volumeLookupCriteria(config)
	if diags.HasError() {
		return nil, diags
	}
	// An ID on its own identifies the volume, so it is read directly instead of
	// through the list-and-match finder.
	if criteria == (volumeCriteria{id: true}) {
		return d.getVolumeByIDCriterion(ctx, config)
	}
	return d.getVolumeByFilter(ctx, config, criteria)
}

func (d *VolumeDataSource) getVolumeByIDCriterion(ctx context.Context, config VolumeDataSourceModel) (*blockstoragesdk.VolumeDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	volumeID, idDiags := parse.UUIDString(config.ID, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return nil, diags
	}
	volume, err := d.getVolumeByID(ctx, volumeID)
	if err != nil {
		if errors.Is(err, blockstoragesdk.ErrNotFound) {
			diags.AddError("Volume not found", fmt.Sprintf("No volume found with id %s.", config.ID.ValueString()))
		} else {
			diags.AddError("Error reading volume", err.Error())
		}
		return nil, diags
	}
	return volume, diags
}

func (d *VolumeDataSource) getVolumeByFilter(
	ctx context.Context,
	config VolumeDataSourceModel,
	criteria volumeCriteria,
) (*blockstoragesdk.VolumeDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	volumeID, idDiags := parse.OptionalUUIDString(config.ID, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return nil, diags
	}

	filter := blockstoragelookup.VolumeFilter{ID: volumeID}
	if criteria.name {
		value := config.Name.ValueString()
		filter.Name = &value
	}
	if criteria.size {
		value := int(config.Size.ValueInt64())
		filter.Size = &value
	}
	if criteria.status {
		value := blockstoragesdk.VolumeStatus(strings.TrimSpace(config.Status.ValueString()))
		filter.Status = &value
	}
	if criteria.bootable {
		value := config.Bootable.ValueBool()
		filter.Bootable = &value
	}
	if criteria.zone {
		value := config.Zone.ValueString()
		filter.Zone = &value
	}

	candidate, err := blockstoragelookup.NewVolumeFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve volume", err.Error())
		return nil, diags
	}
	volume, err := d.getVolumeByID(ctx, candidate.Id)
	if err != nil {
		diags.AddError("Error reading resolved volume", err.Error())
		return nil, diags
	}
	return volume, diags
}

func (d *VolumeDataSource) getVolumeByID(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeDetailSchema, error) {
	return d.client.GetVolume(ctx, id, blockstoragesdk.GetVolumeParams{ProjectID: d.projectID})
}

func volumeLookupCriteria(config VolumeDataSourceModel) (volumeCriteria, diag.Diagnostics) {
	var diags diag.Diagnostics
	known := func(value attr.Value) bool { return !value.IsNull() && !value.IsUnknown() }
	criteria := volumeCriteria{
		id:       known(config.ID),
		name:     known(config.Name),
		size:     known(config.Size),
		status:   known(config.Status),
		bootable: known(config.Bootable),
		zone:     known(config.Zone),
	}
	if criteria == (volumeCriteria{}) {
		diags.AddError("Missing volume lookup criteria", "Specify at least one of id, name, size, status, bootable, or zone.")
		return criteria, diags
	}

	texts := []struct {
		set   bool
		field string
		value types.String
	}{
		{criteria.id, "id", config.ID},
		{criteria.name, "name", config.Name},
		{criteria.status, "status", config.Status},
		{criteria.zone, "zone", config.Zone},
	}
	for _, text := range texts {
		if text.set && strings.TrimSpace(text.value.ValueString()) == "" {
			diags.AddError("Invalid volume lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", text.field))
		}
	}
	if criteria.size && config.Size.ValueInt64() < 1 {
		diags.AddError("Invalid volume lookup criteria", "size must be greater than zero.")
	}
	return criteria, diags
}

func populateVolumeDataSourceState(volume *blockstoragesdk.VolumeDetailSchema, state *VolumeDataSourceModel) {
	populateVolumeModel(volume, &state.VolumeModel)
	state.Zone = types.StringValue(volume.Zone.Name)
}

func preserveVolumeConfiguredValues(volume *blockstoragesdk.VolumeDetailSchema, config, state *VolumeDataSourceModel) {
	if compare.SameUUID(config.ID, volume.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == volume.Name {
		state.Name = config.Name
	}
	if !config.Zone.IsNull() && !config.Zone.IsUnknown() && strings.TrimSpace(config.Zone.ValueString()) == volume.Zone.Name {
		state.Zone = config.Zone
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() && strings.TrimSpace(config.Status.ValueString()) == string(volume.Status) {
		state.Status = config.Status
	}
}
