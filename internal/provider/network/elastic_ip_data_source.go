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
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &ElasticIPDataSource{}

type ElasticIPDataSource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewElasticIPDataSource() datasource.DataSource {
	return &ElasticIPDataSource{}
}

func (d *ElasticIPDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_elastic_ip"
}

func (d *ElasticIPDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud Elastic IP by ID, address, status, region, or availability.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Elastic IP ID (UUID). Exactly one Elastic IP must match.",
			},
			"ip_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "IPv4 address to filter by.",
			},
			"ipv6_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "IPv6 address to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Elastic IP description.",
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Elastic IP status to filter by, either active or down.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name where the Elastic IP resides to filter by.",
			},
			"available": schema.BoolAttribute{
				Optional: true,
				Description: "Filter on attachment state: true matches Elastic IPs that no server holds, " +
					"false matches attached ones. The API returns no matching field, so this value is never refreshed.",
			},
			"enable_ipv4": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether IPv4 is enabled.",
			},
			"enable_ipv6": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether IPv6 is enabled.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Elastic IP was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Elastic IP was last updated (RFC3339).",
			},
		},
	}
}

func (d *ElasticIPDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ElasticIPDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ElasticIPDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	eip, diags := d.getElasticIP(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || eip == nil {
		return
	}

	state := config
	populateElasticIPDataSourceState(eip, &state)
	preserveConfiguredElasticIPValues(eip, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *ElasticIPDataSource) getElasticIP(ctx context.Context, config ElasticIPDataSourceModel) (*networksdk.ElasticIPDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasIPAddress := !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown()
	hasIPv6Address := !config.IPv6Address.IsNull() && !config.IPv6Address.IsUnknown()
	hasStatus := !config.Status.IsNull() && !config.Status.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()
	hasAvailable := !config.Available.IsNull() && !config.Available.IsUnknown()

	hasFilter := hasIPAddress || hasIPv6Address || hasStatus || hasRegion || hasAvailable
	if !hasID && !hasFilter {
		diags.AddError(
			"Missing Elastic IP lookup criteria",
			"Specify at least one of id, ip_address, ipv6_address, status, region, or available to look up an Elastic IP.",
		)
		return nil, diags
	}
	if hasIPAddress && strings.TrimSpace(config.IPAddress.ValueString()) == "" {
		diags.AddError("Invalid Elastic IP ip_address filter", "ip_address must not be empty.")
		return nil, diags
	}
	if hasIPv6Address && strings.TrimSpace(config.IPv6Address.ValueString()) == "" {
		diags.AddError("Invalid Elastic IP ipv6_address filter", "ipv6_address must not be empty.")
		return nil, diags
	}
	if hasStatus && strings.TrimSpace(config.Status.ValueString()) == "" {
		diags.AddError("Invalid Elastic IP status filter", "status must not be empty.")
		return nil, diags
	}
	if hasRegion && strings.TrimSpace(config.Region.ValueString()) == "" {
		diags.AddError("Invalid Elastic IP region filter", "region must not be empty.")
		return nil, diags
	}

	// Direct ID lookup optimization when only ID is specified
	if hasID && !hasFilter {
		eipID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		return d.getElasticIPByID(ctx, eipID)
	}

	return d.getElasticIPByFilter(ctx, config)
}

func (d *ElasticIPDataSource) getElasticIPByID(ctx context.Context, eipID core.UUID) (*networksdk.ElasticIPDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	eip, err := d.client.GetElasticIp(ctx, eipID, networksdk.GetElasticIpParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Elastic IP not found", fmt.Sprintf("No Elastic IP found with id %s.", eipID))
			return nil, diags
		}
		diags.AddError("Error reading Elastic IP", err.Error())
		return nil, diags
	}
	return eip, diags
}

func (d *ElasticIPDataSource) getElasticIPByFilter(ctx context.Context, config ElasticIPDataSourceModel) (*networksdk.ElasticIPDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.ElasticIPFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		eipID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &eipID
	}
	if !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown() {
		address := config.IPAddress.ValueString()
		filter.IPAddress = &address
	}
	if !config.IPv6Address.IsNull() && !config.IPv6Address.IsUnknown() {
		address := config.IPv6Address.ValueString()
		filter.IPv6Address = &address
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		status := config.Status.ValueString()
		filter.Status = &status
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		region := config.Region.ValueString()
		filter.Region = &region
	}
	if !config.Available.IsNull() && !config.Available.IsUnknown() {
		available := config.Available.ValueBool()
		filter.Available = &available
	}

	candidate, err := networklookup.NewElasticIPFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving Elastic IP", err.Error())
		return nil, diags
	}

	// The list response omits fields the detail response carries, so state is
	// always mapped from a read of the resolved Elastic IP.
	return d.getElasticIPByID(ctx, candidate.Id)
}

func preserveConfiguredElasticIPValues(
	eip *networksdk.ElasticIPDetailSchema,
	config ElasticIPDataSourceModel,
	state *ElasticIPDataSourceModel,
) {
	if eip == nil {
		return
	}
	if compare.SameUUID(config.ID, eip.Id) {
		state.ID = config.ID
	}
	ipAddress, ipv6Address, status := core.NullableToPtr(eip.IpAddress), core.NullableToPtr(eip.Ipv6Address), core.NullableToPtr(eip.Status)
	if !config.IPAddress.IsNull() && !config.IPAddress.IsUnknown() && ipAddress != nil &&
		strings.TrimSpace(config.IPAddress.ValueString()) == *ipAddress {
		state.IPAddress = config.IPAddress
	}
	if !config.IPv6Address.IsNull() && !config.IPv6Address.IsUnknown() && ipv6Address != nil &&
		strings.TrimSpace(config.IPv6Address.ValueString()) != "" {
		configured, configuredOK := networklookup.NormalizeElasticIPIPv6Address(config.IPv6Address.ValueString())
		backend, backendOK := networklookup.NormalizeElasticIPIPv6Address(*ipv6Address)
		if configuredOK && backendOK && configured == backend {
			state.IPv6Address = config.IPv6Address
		}
	}
	if !config.Status.IsNull() && !config.Status.IsUnknown() && status != nil &&
		strings.TrimSpace(config.Status.ValueString()) == string(*status) {
		state.Status = config.Status
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == eip.Region.Name {
		state.Region = config.Region
	}
}
