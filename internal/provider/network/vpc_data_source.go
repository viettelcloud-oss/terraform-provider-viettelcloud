package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VPCDataSource{}

type VPCDataSource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewVPCDataSource() datasource.DataSource {
	return &VPCDataSource{}
}

func (d *VPCDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (d *VPCDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud VPC by ID, name, CIDR, or region.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "VPC ID (UUID). Exactly one VPC must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the VPC to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Optional description of the VPC.",
			},
			"cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "IPv4 network CIDR to filter by, e.g. 10.0.0.0/16.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name where the VPC resides to filter by.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the VPC was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the VPC was last updated (RFC3339).",
			},
		},
	}
}

func (d *VPCDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.Network
	d.project = data.Project
	d.projectID = data.ProjectID
}

func (d *VPCDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VPCDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpc, diags := d.getVPC(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || vpc == nil {
		return
	}

	state := config
	populateVPCDataSourceState(vpc, &state)
	preserveConfiguredValues(vpc, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VPCDataSource) getVPC(ctx context.Context, config VPCDataSourceModel) (*networksdk.VPCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasCIDR := !config.CIDR.IsNull() && !config.CIDR.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()

	if !hasID && !hasName && !hasCIDR && !hasRegion {
		diags.AddError(
			"Missing VPC lookup criteria",
			"Specify at least one of id, name, cidr, or region to look up a VPC.",
		)
		return nil, diags
	}
	if hasName && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid VPC name filter", "name must not be empty.")
		return nil, diags
	}
	if hasCIDR && strings.TrimSpace(config.CIDR.ValueString()) == "" {
		diags.AddError("Invalid VPC CIDR filter", "cidr must not be empty.")
		return nil, diags
	}
	if hasRegion && strings.TrimSpace(config.Region.ValueString()) == "" {
		diags.AddError("Invalid VPC region filter", "region must not be empty.")
		return nil, diags
	}

	// Direct ID lookup optimization when only ID is specified
	if hasID && !hasName && !hasCIDR && !hasRegion {
		return d.getVPCByID(ctx, config.ID)
	}

	return d.getVPCByFilter(ctx, config)
}

func (d *VPCDataSource) getVPCByID(ctx context.Context, idAttr types.String) (*networksdk.VPCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	vpcID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	vpc, err := d.client.GetVpc(ctx, vpcID, networksdk.GetVpcParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("VPC not found", fmt.Sprintf("No VPC found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading VPC", err.Error())
		return nil, diags
	}
	return vpc, diags
}

func (d *VPCDataSource) getVPCByFilter(ctx context.Context, config VPCDataSourceModel) (*networksdk.VPCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.VPCFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		vpcID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &vpcID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if !config.CIDR.IsNull() && !config.CIDR.IsUnknown() {
		cidr := config.CIDR.ValueString()
		filter.CIDR = &cidr
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		regionFinder := projectlookup.NewRegionFinder(d.project, d.projectID)
		name := config.Region.ValueString()
		reg, err := regionFinder.Resolve(ctx, projectlookup.RegionFilter{Name: &name})
		if err != nil {
			diags.AddError("Error resolving region", err.Error())
			return nil, diags
		}
		filter.RegionID = &reg.Region.Id
	}

	finder := networklookup.NewVPCFinder(d.client, d.projectID)
	res, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving VPC", err.Error())
		return nil, diags
	}
	return &res, diags
}

func preserveConfiguredValues(vpc *networksdk.VPCSchema, config VPCDataSourceModel, state *VPCDataSourceModel) {
	if vpc == nil {
		return
	}
	if compare.SameUUID(config.ID, vpc.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == vpc.Name {
		state.Name = config.Name
	}
	if !config.CIDR.IsNull() && !config.CIDR.IsUnknown() && strings.TrimSpace(config.CIDR.ValueString()) == vpc.Cidr {
		state.CIDR = config.CIDR
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == vpc.Region.Name {
		state.Region = config.Region
	}
}
