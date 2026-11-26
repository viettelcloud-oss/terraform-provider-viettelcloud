package network

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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

var _ datasource.DataSource = &PrivateIPDataSource{}

type PrivateIPDataSource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewPrivateIPDataSource() datasource.DataSource {
	return &PrivateIPDataSource{}
}

func (d *PrivateIPDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_private_ip"
}

func (d *PrivateIPDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud private IP by ID or by address with optional subnet, VPC, and region criteria.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Private IP ID (UUID). Exactly one private IP must match.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Optional description of the private IP.",
			},
			"ip_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Private IP address to filter by.",
			},
			"mac_address": schema.StringAttribute{
				Computed:    true,
				Description: "MAC address assigned to the private IP.",
			},
			"subnet_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Subnet ID (UUID) containing the private IP to filter by.",
			},
			"subnet_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the subnet containing the private IP to filter by.",
			},
			"subnet_cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "CIDR of the subnet containing the private IP to filter by.",
			},
			"vpc_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "VPC ID (UUID) containing the private IP to filter by.",
			},
			"vpc_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the VPC containing the private IP to filter by.",
			},
			"vpc_cidr": schema.StringAttribute{
				Optional:    true,
				Description: "Lookup-only CIDR of the VPC containing the private IP.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Region name where the private IP resides to filter by.",
			},
			"server_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID of the attached server, when present.",
			},
			"server_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the attached server, when present.",
			},
			"device_owner": schema.StringAttribute{
				Computed:    true,
				Description: "Device owner classification assigned by the platform.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"port_security": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether port security is enabled.",
			},
			"allowed_cidrs": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "CIDRs currently allowed by the platform for this private IP.",
			},
			"allowed_vip_ids": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Private IP IDs currently allowed as VIPs.",
			},
			"attached_vips": schema.SetNestedAttribute{
				Computed:    true,
				Description: "Private IPs attached as allowed VIPs.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "Allowed VIP private IP ID (UUID).",
						},
						"ip_address": schema.StringAttribute{
							Computed:    true,
							Description: "Allowed VIP address.",
						},
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the private IP was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the private IP was last updated (RFC3339).",
			},
		},
	}
}

func (d *PrivateIPDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PrivateIPDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PrivateIPDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	privateIP, diags := d.getPrivateIP(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || privateIP == nil {
		return
	}

	state := config
	populatePrivateIPDataSourceState(privateIP, &state)
	preserveConfiguredPrivateIPValues(privateIP, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *PrivateIPDataSource) getPrivateIP(
	ctx context.Context,
	config PrivateIPDataSourceModel,
) (*networksdk.PrivateIPSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasIPAddress := !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown()
	hasSubnetID := !config.SubnetID.IsNull() && !config.SubnetID.IsUnknown()
	hasSubnetName := !config.SubnetName.IsNull() && !config.SubnetName.IsUnknown()
	hasSubnetCIDR := !config.SubnetCIDR.IsNull() && !config.SubnetCIDR.IsUnknown()
	hasVPCID := !config.VPCID.IsNull() && !config.VPCID.IsUnknown()
	hasVPCName := !config.VPCName.IsNull() && !config.VPCName.IsUnknown()
	hasVPCCIDR := !config.VPCCIDR.IsNull() && !config.VPCCIDR.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()
	if !hasID && !hasIPAddress {
		diags.AddError(
			"Missing private IP lookup criteria",
			"Specify id, or specify ip_address with any additional subnet, VPC, or region criteria needed to identify one private IP.",
		)
		return nil, diags
	}
	for _, filter := range []struct {
		set   bool
		value types.String
		name  string
	}{
		{hasIPAddress, config.IPAddress, "ip_address"},
		{hasSubnetName, config.SubnetName, "subnet_name"},
		{hasSubnetCIDR, config.SubnetCIDR, "subnet_cidr"},
		{hasVPCName, config.VPCName, "vpc_name"},
		{hasVPCCIDR, config.VPCCIDR, "vpc_cidr"},
		{hasRegion, config.Region, "region"},
	} {
		if filter.set && strings.TrimSpace(filter.value.ValueString()) == "" {
			diags.AddError(
				fmt.Sprintf("Invalid private IP %s filter", filter.name),
				fmt.Sprintf("%s must not be empty.", filter.name),
			)
			return nil, diags
		}
	}

	if hasID && !hasIPAddress && !hasSubnetID && !hasSubnetName && !hasSubnetCIDR &&
		!hasVPCID && !hasVPCName && !hasVPCCIDR && !hasRegion {
		return d.getPrivateIPByID(ctx, config.ID)
	}
	return d.getPrivateIPByFilter(ctx, config)
}

func (d *PrivateIPDataSource) getPrivateIPByID(
	ctx context.Context,
	idAttr types.String,
) (*networksdk.PrivateIPSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	privateIPID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	privateIP, err := d.client.GetPrivateIp(ctx, privateIPID, networksdk.GetPrivateIpParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Private IP not found", fmt.Sprintf("No private IP found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading private IP", err.Error())
		return nil, diags
	}
	return privateIP, diags
}

func (d *PrivateIPDataSource) getPrivateIPByFilter(
	ctx context.Context,
	config PrivateIPDataSourceModel,
) (*networksdk.PrivateIPSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.PrivateIPFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		id, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		filter.ID = &id
	}
	if !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown() {
		ipAddress := config.IPAddress.ValueString()
		filter.IPAddress = &ipAddress
	}
	if diags.HasError() {
		return nil, diags
	}

	vpcID, vpcDiags := d.resolvePrivateIPVPC(ctx, config)
	diags.Append(vpcDiags...)
	if diags.HasError() {
		return nil, diags
	}
	subnetID, subnetDiags := d.resolvePrivateIPSubnet(ctx, config, vpcID)
	diags.Append(subnetDiags...)
	if diags.HasError() {
		return nil, diags
	}
	filter.SubnetID = subnetID
	filter.VPCID = vpcID
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		region := config.Region.ValueString()
		filter.Region = &region
	}

	result, err := networklookup.NewPrivateIPFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving private IP", err.Error())
		return nil, diags
	}
	return &result, diags
}

func (d *PrivateIPDataSource) resolvePrivateIPVPC(
	ctx context.Context,
	config PrivateIPDataSourceModel,
) (*core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	hasID := !config.VPCID.IsNull() && !config.VPCID.IsUnknown()
	hasName := !config.VPCName.IsNull() && !config.VPCName.IsUnknown()
	hasCIDR := !config.VPCCIDR.IsNull() && !config.VPCCIDR.IsUnknown()
	if !hasID && !hasName && !hasCIDR {
		return nil, diags
	}

	filter := networklookup.VPCFilter{}
	if hasID {
		id, parseDiags := parse.UUIDString(config.VPCID, "vpc_id")
		diags.Append(parseDiags...)
		filter.ID = &id
	}
	if diags.HasError() {
		return nil, diags
	}
	if hasID && !hasName && !hasCIDR {
		return filter.ID, diags
	}
	if hasName {
		name := config.VPCName.ValueString()
		filter.Name = &name
	}
	if hasCIDR {
		cidr := config.VPCCIDR.ValueString()
		filter.CIDR = &cidr
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		name := config.Region.ValueString()
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &name})
		if err != nil {
			diags.AddError("Error resolving region", err.Error())
			return nil, diags
		}
		filter.RegionID = &region.Region.Id
	}

	vpc, err := networklookup.NewVPCFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving VPC", err.Error())
		return nil, diags
	}
	return &vpc.Id, diags
}

func (d *PrivateIPDataSource) resolvePrivateIPSubnet(
	ctx context.Context,
	config PrivateIPDataSourceModel,
	vpcID *core.UUID,
) (*core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	hasID := !config.SubnetID.IsNull() && !config.SubnetID.IsUnknown()
	hasName := !config.SubnetName.IsNull() && !config.SubnetName.IsUnknown()
	hasCIDR := !config.SubnetCIDR.IsNull() && !config.SubnetCIDR.IsUnknown()
	if !hasID && !hasName && !hasCIDR {
		return nil, diags
	}

	filter := networklookup.SubnetFilter{VPCID: vpcID}
	if hasID {
		id, parseDiags := parse.UUIDString(config.SubnetID, "subnet_id")
		diags.Append(parseDiags...)
		filter.ID = &id
	}
	if diags.HasError() {
		return nil, diags
	}
	if hasID && !hasName && !hasCIDR {
		return filter.ID, diags
	}
	if hasName {
		name := config.SubnetName.ValueString()
		filter.Name = &name
	}
	if hasCIDR {
		cidr := config.SubnetCIDR.ValueString()
		filter.CIDR = &cidr
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		region := config.Region.ValueString()
		filter.Region = &region
	}

	subnet, err := networklookup.NewSubnetFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving subnet", err.Error())
		return nil, diags
	}
	return &subnet.Id, diags
}

func preserveConfiguredPrivateIPValues(
	privateIP *networksdk.PrivateIPSchema,
	config PrivateIPDataSourceModel,
	state *PrivateIPDataSourceModel,
) {
	if privateIP == nil {
		return
	}
	if compare.SameUUID(config.ID, privateIP.Id) {
		state.ID = config.ID
	}
	if ipAddress := core.NullableToPtr(privateIP.IpAddress); ipAddress != nil && !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown() {
		configured, configuredErr := netip.ParseAddr(strings.TrimSpace(config.IPAddress.ValueString()))
		backend, backendErr := netip.ParseAddr(strings.TrimSpace(*ipAddress))
		if configuredErr == nil && backendErr == nil && configured == backend {
			state.IPAddress = config.IPAddress
		}
	}
	if compare.SameUUID(config.SubnetID, privateIP.Subnet.Id) {
		state.SubnetID = config.SubnetID
	}
	if !config.SubnetName.IsNull() && !config.SubnetName.IsUnknown() && strings.TrimSpace(config.SubnetName.ValueString()) == privateIP.Subnet.Name {
		state.SubnetName = config.SubnetName
	}
	if !config.SubnetCIDR.IsNull() && !config.SubnetCIDR.IsUnknown() && strings.TrimSpace(config.SubnetCIDR.ValueString()) == privateIP.Subnet.Cidr {
		state.SubnetCIDR = config.SubnetCIDR
	}
	if compare.SameUUID(config.VPCID, privateIP.Vpc.Id) {
		state.VPCID = config.VPCID
	}
	if !config.VPCName.IsNull() && !config.VPCName.IsUnknown() && strings.TrimSpace(config.VPCName.ValueString()) == privateIP.Vpc.Name {
		state.VPCName = config.VPCName
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == privateIP.Region.Name {
		state.Region = config.Region
	}
}
