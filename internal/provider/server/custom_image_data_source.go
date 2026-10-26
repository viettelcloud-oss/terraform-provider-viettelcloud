package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
)

var _ datasource.DataSource = &CustomImageDataSource{}

type CustomImageDataSource struct {
	client    *serversdk.Client
	projectID core.UUID
}

func NewCustomImageDataSource() datasource.DataSource { return &CustomImageDataSource{} }
func (d *CustomImageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_image"
}

func (d *CustomImageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud custom image by UUID or a unique combination of name, volume, region, operating system, version, state, and status.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image UUID. Exactly one image must match all configured criteria.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image name. Exactly one image must match all configured criteria.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Image description.",
			},
			"volume": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Source volume name. Exactly one image must match all configured criteria.",
			},
			"volume_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Source volume UUID. Exactly one image must match all configured criteria.",
			},
			"disk_format": schema.StringAttribute{
				Computed:    true,
				Description: "Image disk format.",
			},
			"container_format": schema.StringAttribute{
				Computed:    true,
				Description: "Image container format.",
			},
			"os": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Operating system. Exactly one image must match all configured criteria.",
			},
			"version": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Operating system version. Exactly one image must match all configured criteria.",
			},
			"state": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Image state: enabled or disabled. Exactly one image must match all configured criteria.",
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Backend image status. Exactly one image must match all configured criteria.",
			},
			"metadata": schema.StringAttribute{
				Computed:    true,
				Description: "Image metadata as a JSON object returned by the API.",
			},
			"shared_project_ids": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Project UUIDs that can use this image.",
			},
			"size": schema.Int64Attribute{
				Computed:    true,
				Description: "Image size reported by the API.",
			},
			"min_volume_size": schema.Int64Attribute{
				Computed:    true,
				Description: "Minimum volume size reported by the API.",
			},
			"encrypt_key_size": schema.StringAttribute{
				Computed:    true,
				Description: "Encryption key size reported by the API.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Region name. Exactly one image must match all configured criteria.",
			},
			"project_id": schema.StringAttribute{
				Computed:    true,
				Description: "Owner project UUID.",
			},
			"project": schema.StringAttribute{
				Computed:    true,
				Description: "Owner project name.",
			},
			"owner_id": schema.StringAttribute{
				Computed:    true,
				Description: "Owner user UUID. Null when the API omits the owner.",
			},
			"owner": schema.StringAttribute{
				Computed:    true,
				Description: "Owner username. Null when the API omits the owner.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Creation time in RFC3339 format.",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Last update time in RFC3339 format.",
			},
		},
	}
}

func (d *CustomImageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.projectID = data.Server, data.ProjectID
}

func (d *CustomImageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config CustomImageDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	image, diags := d.getCustomImage(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || image == nil {
		return
	}
	state := config
	populateCustomImageDataSourceState(image, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *CustomImageDataSource) getCustomImage(ctx context.Context, config CustomImageDataSourceModel) (*serversdk.CustomImageSchema, diag.Diagnostics) {
	filter, criteria, diags := customImageFilter(config)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == 1 && filter.ID != nil {
		return d.getCustomImageByID(ctx, *filter.ID)
	}
	return d.getCustomImageByFilter(ctx, filter)
}

func (d *CustomImageDataSource) getCustomImageByID(ctx context.Context, id core.UUID) (*serversdk.CustomImageSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	image, err := d.client.GetCustomImage(ctx, id, serversdk.GetCustomImageParams{ProjectID: d.projectID})
	if err != nil {
		diags.AddError("Error reading custom image", err.Error())
		return nil, diags
	}
	return image, diags
}

func (d *CustomImageDataSource) getCustomImageByFilter(ctx context.Context, filter serverlookup.CustomImageFilter) (*serversdk.CustomImageSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	image, err := serverlookup.NewCustomImageFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving custom image", err.Error())
		return nil, diags
	}
	return &image, diags
}

func customImageFilter(config CustomImageDataSourceModel) (serverlookup.CustomImageFilter, int, diag.Diagnostics) {
	var filter serverlookup.CustomImageFilter
	var diags diag.Diagnostics
	count := 0
	for _, field := range []struct {
		name   string
		value  types.String
		target **string
	}{
		{"name", config.Name, &filter.Name}, {"volume", config.Volume, &filter.Volume}, {"region", config.Region, &filter.Region}, {"os", config.OS, &filter.OS}, {"version", config.Version, &filter.Version},
	} {
		if field.value.IsNull() || field.value.IsUnknown() {
			continue
		}
		count++
		value := strings.TrimSpace(field.value.ValueString())
		if value == "" {
			diags.AddError("Invalid custom image lookup criteria", field.name+" must not be blank.")
			continue
		}
		*field.target = &value
	}
	for _, field := range []struct {
		name   string
		value  types.String
		target **core.UUID
	}{{"id", config.ID, &filter.ID}, {"volume_id", config.VolumeID, &filter.VolumeID}} {
		if field.value.IsNull() || field.value.IsUnknown() {
			continue
		}
		count++
		id, idDiags := parse.UUIDString(field.value, field.name)
		diags.Append(idDiags...)
		*field.target = &id
	}
	if !config.State.IsNull() && !config.State.IsUnknown() {
		count++
		value := serversdk.ImageState(config.State.ValueString())
		if !value.Valid() {
			diags.AddError("Invalid image state", "state must be enabled or disabled.")
		} else {
			filter.State = &value
		}
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		count++
		value := serversdk.ImageStatus(config.Status.ValueString())
		if !value.Valid() {
			diags.AddError("Invalid image status", "status must be a status supported by the API.")
		} else {
			filter.Status = &value
		}
	}
	if count == 0 {
		diags.AddError("Missing custom image lookup criteria", "Specify an image UUID or at least one lookup criterion.")
	}
	return filter, count, diags
}
