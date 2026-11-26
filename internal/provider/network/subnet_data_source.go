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

	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &SubnetDataSource{}

type SubnetDataSource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewSubnetDataSource() datasource.DataSource {
	return &SubnetDataSource{}
}

func (d *SubnetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (d *SubnetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud subnet by ID, name, CIDR, VPC, or region.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Subnet ID (UUID). Exactly one subnet must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the subnet to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Optional description of the subnet.",
			},
			"cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "IPv4 network CIDR to filter by, e.g. 10.0.1.0/24.",
			},
			"vpc_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "VPC ID (UUID) containing the subnet to filter by.",
			},
			"vpc_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the VPC containing the subnet to filter by.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Region name where the subnet resides to filter by.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the subnet was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the subnet was last updated (RFC3339).",
			},
		},
	}
}

func (d *SubnetDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.Network
	d.projectID = data.ProjectID
}

func (d *SubnetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config SubnetDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	subnet, diags := d.getSubnet(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || subnet == nil {
		return
	}

	state := config
	populateSubnetDataSourceState(subnet, &state)
	preserveConfiguredSubnetValues(subnet, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *SubnetDataSource) getSubnet(ctx context.Context, config SubnetDataSourceModel) (*networksdk.SubnetSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasCIDR := !config.CIDR.IsNull() && !config.CIDR.IsUnknown()
	hasVPCID := !config.VPCID.IsNull() && !config.VPCID.IsUnknown()
	hasVPCName := !config.VPCName.IsNull() && !config.VPCName.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()

	if !hasID && !hasName && !hasCIDR && !hasVPCID && !hasVPCName && !hasRegion {
		diags.AddError(
			"Missing subnet lookup criteria",
			"Specify at least one of id, name, cidr, vpc_id, vpc_name, or region to look up a subnet.",
		)
		return nil, diags
	}
	for _, filter := range []struct {
		set   bool
		value types.String
		name  string
	}{
		{hasName, config.Name, "name"},
		{hasCIDR, config.CIDR, "cidr"},
		{hasVPCName, config.VPCName, "vpc_name"},
		{hasRegion, config.Region, "region"},
	} {
		if filter.set && strings.TrimSpace(filter.value.ValueString()) == "" {
			diags.AddError(
				fmt.Sprintf("Invalid subnet %s filter", filter.name),
				fmt.Sprintf("%s must not be empty.", filter.name),
			)
			return nil, diags
		}
	}

	if hasID && !hasName && !hasCIDR && !hasVPCID && !hasVPCName && !hasRegion {
		return d.getSubnetByID(ctx, config.ID)
	}

	return d.getSubnetByFilter(ctx, config)
}

func (d *SubnetDataSource) getSubnetByID(ctx context.Context, idAttr types.String) (*networksdk.SubnetSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	subnetID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	subnet, err := d.client.GetSubnet(ctx, subnetID, networksdk.GetSubnetParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Subnet not found", fmt.Sprintf("No subnet found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading subnet", err.Error())
		return nil, diags
	}
	return subnet, diags
}

func (d *SubnetDataSource) getSubnetByFilter(ctx context.Context, config SubnetDataSourceModel) (*networksdk.SubnetSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.SubnetFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		id, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		filter.ID = &id
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if !config.CIDR.IsNull() && !config.CIDR.IsUnknown() {
		cidr := config.CIDR.ValueString()
		filter.CIDR = &cidr
	}
	if !config.VPCID.IsNull() && !config.VPCID.IsUnknown() {
		vpcID, parseDiags := parse.UUIDString(config.VPCID, "vpc_id")
		diags.Append(parseDiags...)
		filter.VPCID = &vpcID
	}
	if diags.HasError() {
		return nil, diags
	}
	if !config.VPCName.IsNull() && !config.VPCName.IsUnknown() {
		vpcName := config.VPCName.ValueString()
		filter.VPCName = &vpcName
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		region := config.Region.ValueString()
		filter.Region = &region
	}

	result, err := networklookup.NewSubnetFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving subnet", err.Error())
		return nil, diags
	}
	return &result, diags
}

func preserveConfiguredSubnetValues(subnet *networksdk.SubnetSchema, config SubnetDataSourceModel, state *SubnetDataSourceModel) {
	if subnet == nil {
		return
	}
	if compare.SameUUID(config.ID, subnet.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == subnet.Name {
		state.Name = config.Name
	}
	if !config.CIDR.IsNull() && !config.CIDR.IsUnknown() && strings.TrimSpace(config.CIDR.ValueString()) == subnet.Cidr {
		state.CIDR = config.CIDR
	}
	if compare.SameUUID(config.VPCID, subnet.Vpc.Id) {
		state.VPCID = config.VPCID
	}
	if !config.VPCName.IsNull() && !config.VPCName.IsUnknown() && strings.TrimSpace(config.VPCName.ValueString()) == subnet.Vpc.Name {
		state.VPCName = config.VPCName
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == subnet.Region.Name {
		state.Region = config.Region
	}
}
