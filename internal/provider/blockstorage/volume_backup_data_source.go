package blockstorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VolumeBackupDataSource{}

type VolumeBackupDataSource struct {
	client    *blockstoragesdk.Client
	projectID core.UUID
}

func NewVolumeBackupDataSource() datasource.DataSource { return &VolumeBackupDataSource{} }

func (d *VolumeBackupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_volume_backup"
}

func (d *VolumeBackupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Find one Viettel Cloud volume backup by ID or exact filters.", Attributes: map[string]schema.Attribute{
		"id":                  schema.StringAttribute{Optional: true, Computed: true, Description: "Backup ID (UUID)."},
		"name":                schema.StringAttribute{Optional: true, Computed: true, Description: "Exact backup name."},
		"volume_id":           schema.StringAttribute{Optional: true, Computed: true, Description: "Source volume ID (UUID)."},
		"status":              schema.StringAttribute{Optional: true, Computed: true, Description: "Exact backup status."},
		"size":                schema.Int64Attribute{Optional: true, Computed: true, Description: "Backup size in GiB."},
		"region":              schema.StringAttribute{Optional: true, Computed: true, Description: "Exact region name."},
		"description":         schema.StringAttribute{Computed: true, Description: "Backup description."},
		"volume_name":         schema.StringAttribute{Computed: true, Description: "Source volume name."},
		"expired":             schema.BoolAttribute{Computed: true, Description: "Whether the backup expired."},
		"disk_type":           schema.StringAttribute{Computed: true, Description: "Source disk type."},
		"encryption_key_size": schema.StringAttribute{Computed: true, Description: "Encryption key size."},
		"project_id":          schema.StringAttribute{Computed: true, Description: "Owning project ID (UUID)."},
		"region_id":           schema.StringAttribute{Computed: true, Description: "Region ID (UUID)."},
		"policy_id":           schema.StringAttribute{Computed: true, Description: "Backup policy ID (UUID), if any."},
		"created_at":          schema.StringAttribute{Computed: true, Description: "Creation timestamp (RFC3339)."},
		"updated_at":          schema.StringAttribute{Computed: true, Description: "Last update timestamp (RFC3339)."},
	}}
}

func (d *VolumeBackupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.BlockStorage, data.ProjectID
}

func (d *VolumeBackupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VolumeBackupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	backup, diags := d.getVolumeBackup(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || backup == nil {
		return
	}
	state := config
	populateVolumeBackupDataSourceState(backup, &state)
	preserveVolumeBackupConfiguredValues(backup, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VolumeBackupDataSource) getVolumeBackup(ctx context.Context, config VolumeBackupDataSourceModel) (*blockstoragesdk.VolumeBackupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter, direct, filterDiags := volumeBackupFilter(config)
	diags.Append(filterDiags...)
	if diags.HasError() {
		return nil, diags
	}
	if direct {
		return d.getVolumeBackupByID(ctx, *filter.ID)
	}
	return d.getVolumeBackupByFilter(ctx, filter)
}

func (d *VolumeBackupDataSource) getVolumeBackupByID(ctx context.Context, id core.UUID) (*blockstoragesdk.VolumeBackupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	backup, err := d.client.GetVolumeBackup(ctx, id, blockstoragesdk.GetVolumeBackupParams{ProjectID: d.projectID})
	if errors.Is(err, blockstoragesdk.ErrNotFound) {
		diags.AddError("Volume backup not found", fmt.Sprintf("No backup found with id %s.", id))
	} else if err != nil {
		diags.AddError("Error reading volume backup", err.Error())
	}
	return backup, diags
}

func (d *VolumeBackupDataSource) getVolumeBackupByFilter(ctx context.Context, filter blockstoragelookup.VolumeBackupFilter) (*blockstoragesdk.VolumeBackupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	match, err := blockstoragelookup.NewVolumeBackupFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve volume backup", err.Error())
		return nil, diags
	}
	backup, err := d.client.GetVolumeBackup(ctx, match.Id, blockstoragesdk.GetVolumeBackupParams{ProjectID: d.projectID})
	if err != nil {
		diags.AddError("Error reading resolved volume backup", err.Error())
		return nil, diags
	}
	return backup, diags
}

func volumeBackupFilter(config VolumeBackupDataSourceModel) (blockstoragelookup.VolumeBackupFilter, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := blockstoragelookup.VolumeBackupFilter{}
	known := func(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }
	count := 0
	for _, value := range []types.String{config.ID, config.Name, config.VolumeID, config.Status, config.Region} {
		if known(value) {
			count++
		}
	}
	if !config.Size.IsNull() && !config.Size.IsUnknown() {
		count++
	}
	if count == 0 {
		diags.AddError("Missing volume backup lookup criteria", "Specify id, name, volume_id, status, size, or region.")
		return filter, false, diags
	}
	for _, item := range []struct {
		name  string
		value types.String
	}{{"id", config.ID}, {"name", config.Name}, {"volume_id", config.VolumeID}, {"status", config.Status}, {"region", config.Region}} {
		if known(item.value) && strings.TrimSpace(item.value.ValueString()) == "" {
			diags.AddError("Invalid volume backup lookup criteria", item.name+" must not be empty or whitespace-only.")
		}
	}
	if !config.Size.IsNull() && !config.Size.IsUnknown() && config.Size.ValueInt64() < 1 {
		diags.AddError("Invalid volume backup lookup criteria", "size must be greater than zero.")
	}
	if diags.HasError() {
		return filter, false, diags
	}
	if known(config.ID) {
		id, ds := parse.UUIDString(config.ID, "id")
		diags.Append(ds...)
		filter.ID = &id
	}
	if known(config.VolumeID) {
		id, ds := parse.UUIDString(config.VolumeID, "volume_id")
		diags.Append(ds...)
		filter.VolumeID = &id
	}
	if known(config.Name) {
		value := config.Name.ValueString()
		filter.Name = &value
	}
	if known(config.Status) {
		value := blockstoragesdk.VolumeBackupStatus(strings.TrimSpace(config.Status.ValueString()))
		filter.Status = &value
	}
	if known(config.Region) {
		value := config.Region.ValueString()
		filter.Region = &value
	}
	if !config.Size.IsNull() && !config.Size.IsUnknown() {
		value := int(config.Size.ValueInt64())
		filter.Size = &value
	}
	return filter, count == 1 && filter.ID != nil, diags
}
