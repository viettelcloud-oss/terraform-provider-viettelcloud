package filestorage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	filestoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &FilesystemDataSource{}

// FilesystemDataSource implements the viettelcloud_fs_filesystem data source.
type FilesystemDataSource struct {
	client    *filestoragepkg.Client
	projectID core.UUID
}

func NewFilesystemDataSource() datasource.DataSource {
	return &FilesystemDataSource{}
}

func (d *FilesystemDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fs_filesystem"
}

func (d *FilesystemDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud File Storage filesystem by ID or exact filter criteria.",
		Attributes: map[string]schema.Attribute{
			"id":                   schema.StringAttribute{Optional: true, Computed: true, Description: "Filesystem ID (UUID)."},
			"name":                 schema.StringAttribute{Optional: true, Computed: true, Description: "Filesystem name to filter by."},
			"zone":                 schema.StringAttribute{Optional: true, Computed: true, Description: "Zone name to filter by."},
			"subnet_id":            schema.StringAttribute{Optional: true, Computed: true, Description: "Subnet ID (UUID) to filter by."},
			"description":          schema.StringAttribute{Computed: true, Description: "Filesystem description."},
			"size":                 schema.Int64Attribute{Computed: true, Description: "Filesystem size in GiB."},
			"protocol":             schema.StringAttribute{Computed: true, Description: "Filesystem protocol (NFS or SMB)."},
			"status":               schema.StringAttribute{Computed: true, Description: "Current filesystem status."},
			"mount_target_address": schema.StringAttribute{Computed: true, Description: "Mount target IP address."},
			"zone_id":              schema.StringAttribute{Computed: true, Description: "Zone ID (UUID)."},
			"region_id":            schema.StringAttribute{Computed: true, Description: "Region ID (UUID)."},
			"region_name":          schema.StringAttribute{Computed: true, Description: "Region name."},
			"vpc_id":               schema.StringAttribute{Computed: true, Description: "VPC ID (UUID) of the subnet."},
			"vpc_name":             schema.StringAttribute{Computed: true, Description: "VPC name of the subnet."},
			"subnet_cidr":          schema.StringAttribute{Computed: true, Description: "CIDR of the subnet."},
			"has_snapshot":         schema.BoolAttribute{Computed: true, Description: "Whether the filesystem has at least one snapshot."},
			"migration_percentage": schema.Int64Attribute{Computed: true, Description: "Migration progress percentage. Null when not migrating."},
			"owner_id":             schema.StringAttribute{Computed: true, Description: "Owner ID (UUID)."},
			"project_id":           schema.StringAttribute{Computed: true, Description: "Project ID (UUID)."},
			"filesystem_type_id":   schema.StringAttribute{Computed: true, Description: "Filesystem type ID (UUID)."},
			"filesystem_type_name": schema.StringAttribute{Computed: true, Description: "Filesystem type name."},
			"snapshot_id":          schema.StringAttribute{Computed: true, Description: "Source snapshot ID (UUID) that the filesystem was created from. Null when it was not created from a snapshot."},
			"snapshot_policy": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Applied snapshot policy. Null when no policy is applied.",
				Attributes: map[string]schema.Attribute{
					"id":   schema.StringAttribute{Computed: true, Description: "Policy ID (UUID)."},
					"name": schema.StringAttribute{Computed: true, Description: "Policy name."},
				},
			},
			"created_at": schema.StringAttribute{Computed: true, Description: "Timestamp when the filesystem was created (RFC3339)."},
			"updated_at": schema.StringAttribute{Computed: true, Description: "Timestamp when the filesystem was last updated (RFC3339)."},
		},
	}
}

func (d *FilesystemDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.FileStorage
	d.projectID = data.ProjectID
}

func (d *FilesystemDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config FilesystemDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fs, diags := d.getFilesystem(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || fs == nil {
		return
	}

	state := config
	populateFilesystemDataSourceState(fs, &state)
	preserveFilesystemFilters(fs, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *FilesystemDataSource) getFilesystem(ctx context.Context, config FilesystemDataSourceModel) (*filestoragepkg.FilesystemSchema, diag.Diagnostics) {
	var diagsOut diag.Diagnostics

	known := func(v types.String) bool { return !v.IsNull() && !v.IsUnknown() }
	hasID := known(config.ID)
	hasName := known(config.Name)
	hasZone := known(config.Zone)
	hasSubnet := known(config.SubnetID)

	if !hasID && !hasName && !hasZone && !hasSubnet {
		diagsOut.AddError("Missing Filesystem lookup criteria", "Specify at least one of id, name, zone, or subnet_id.")
		return nil, diagsOut
	}

	if hasID && !hasName && !hasZone && !hasSubnet {
		return d.getByID(ctx, config.ID)
	}
	return d.getByFilter(ctx, config, hasID, hasName, hasZone, hasSubnet)
}

func (d *FilesystemDataSource) getByID(ctx context.Context, idVal types.String) (*filestoragepkg.FilesystemSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	fsID, idDiags := parse.UUIDString(idVal, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return nil, diags
	}
	fs, err := d.client.GetFilesystem(ctx, fsID, filestoragepkg.GetFilesystemParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, filestoragepkg.ErrNotFound) {
			diags.AddError("Filesystem not found", fmt.Sprintf("No filesystem found with id %s.", idVal.ValueString()))
		} else {
			diags.AddError("Error reading filesystem", err.Error())
		}
		return nil, diags
	}
	return fs, diags
}

func (d *FilesystemDataSource) getByFilter(
	ctx context.Context,
	config FilesystemDataSourceModel,
	hasID, hasName, hasZone, hasSubnet bool,
) (*filestoragepkg.FilesystemSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	var configuredID *core.UUID
	if hasID {
		fsID, idDiags := parse.UUIDString(config.ID, "id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return nil, diags
		}
		configuredID = &fsID
	}

	filter := filestoragelookup.FilesystemFilter{}
	if hasName {
		name := strings.TrimSpace(config.Name.ValueString())
		if name == "" {
			diags.AddError("Invalid filesystem lookup criteria", "name must not be empty or whitespace-only.")
			return nil, diags
		}
		filter.Name = &name
	}
	if hasZone {
		zone := strings.TrimSpace(config.Zone.ValueString())
		if zone == "" {
			diags.AddError("Invalid filesystem lookup criteria", "zone must not be empty or whitespace-only.")
			return nil, diags
		}
		filter.Zone = &zone
	}
	if hasSubnet {
		subnetID, subnetDiags := parse.UUIDString(config.SubnetID, "subnet_id")
		diags.Append(subnetDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.SubnetID = &subnetID
	}

	candidate, err := filestoragelookup.NewFilesystemFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve filesystem", err.Error())
		return nil, diags
	}

	if configuredID != nil && candidate.Id != *configuredID {
		diags.AddError(
			"Filesystem lookup mismatch",
			fmt.Sprintf("The filesystem resolved by filter criteria has id %s, but the configured id is %s.", candidate.Id, config.ID.ValueString()),
		)
		return nil, diags
	}

	fs, err := d.client.GetFilesystem(ctx, candidate.Id, filestoragepkg.GetFilesystemParams{ProjectID: d.projectID})
	if err != nil {
		diags.AddError("Error reading resolved filesystem", err.Error())
		return nil, diags
	}
	return fs, diags
}
