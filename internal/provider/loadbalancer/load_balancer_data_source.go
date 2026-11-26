package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &LoadBalancerDataSource{}

type LoadBalancerDataSource struct {
	client    *loadbalancersdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewLoadBalancerDataSource() datasource.DataSource {
	return &LoadBalancerDataSource{}
}

func (d *LoadBalancerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_loadbalancer"
}

func (d *LoadBalancerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get one Viettel Cloud Load Balancer by ID or combined filters. Specify at least one criterion. All configured criteria must match exactly one Load Balancer. Deleted Load Balancers are excluded.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Load Balancer ID (UUID). Exactly one Load Balancer must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the Load Balancer to filter by.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name to filter by. Combine it with a name or ID when the region contains multiple Load Balancers.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Optional description of the Load Balancer.",
			},
			"package": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Load Balancer package to filter by: SMALL, MEDIUM, or LARGE.",
			},
			"topology": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Load Balancer topology to filter by: SINGLE or ACTIVE_STANDBY.",
			},
			"type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Load Balancer type to filter by: native or appliance.",
			},
			"access_log_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Filter by access log status. The value false selects Load Balancers with access logs disabled.",
			},
			"access_log_store_id": schema.StringAttribute{
				Computed:    true,
				Description: "Access log store ID (UUID), when present.",
			},
			"access_log_store_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the access log store, when present.",
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID).",
			},
			"vpc_id": schema.StringAttribute{
				Computed:    true,
				Description: "VPC ID (UUID) containing the Load Balancer.",
			},
			"vpc_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the VPC containing the Load Balancer.",
			},
			"private_ip_id": schema.StringAttribute{
				Computed:    true,
				Description: "Private IP ID (UUID) used as the VIP, when present.",
			},
			"qos_policy_id": schema.StringAttribute{
				Computed:    true,
				Description: "QoS policy ID (UUID) assigned to the Load Balancer, when present.",
			},
			"qos_policy_name": schema.StringAttribute{
				Computed:    true,
				Description: "Name of the assigned QoS policy, when present.",
			},
			"vip_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Private VIP address to filter by.",
			},
			"public_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Public IPv4 address to filter by, when present.",
			},
			"v6_public_address": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Public IPv6 address to filter by, when present.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Load Balancer was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Load Balancer was last updated (RFC3339).",
			},
		},
	}
}

func (d *LoadBalancerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.LoadBalancer
	d.project = data.Project
	d.projectID = data.ProjectID
}

func (d *LoadBalancerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config LoadBalancerDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lb, diags := d.getLoadBalancer(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || lb == nil {
		return
	}

	state := config
	populateLoadBalancerDataSourceState(lb, &state)
	preserveConfiguredLoadBalancerValues(lb, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *LoadBalancerDataSource) getLoadBalancer(
	ctx context.Context,
	config LoadBalancerDataSourceModel,
) (*loadbalancersdk.LoadBalancerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter, filterDiags := loadBalancerDataSourceFilter(config)
	diags.Append(filterDiags...)
	if diags.HasError() {
		return nil, diags
	}
	if filter.ID != nil && filter.Name == nil && config.Region.IsNull() && filter.VIPAddress == nil && filter.PublicAddress == nil && filter.V6PublicAddress == nil && filter.Package == nil && filter.Topology == nil && filter.Type == nil && filter.AccessLogEnabled == nil {
		return d.getLoadBalancerByID(ctx, *filter.ID)
	}
	return d.getLoadBalancerByFilter(ctx, config, filter)
}

func (d *LoadBalancerDataSource) getLoadBalancerByID(
	ctx context.Context,
	lbID core.UUID,
) (*loadbalancersdk.LoadBalancerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	lb, err := d.client.GetLoadBalancer(ctx, lbID, loadbalancersdk.GetLoadBalancerParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError("Load Balancer not found", fmt.Sprintf("No Load Balancer found with id %s", lbID))
			return nil, diags
		}
		diags.AddError("Error reading Load Balancer", err.Error())
		return nil, diags
	}
	if lb.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Load Balancer not found", fmt.Sprintf("Load Balancer %s is in DELETED state.", lbID))
		return nil, diags
	}
	return lb, diags
}

func (d *LoadBalancerDataSource) getLoadBalancerByFilter(
	ctx context.Context,
	config LoadBalancerDataSourceModel,
	filter loadbalancerlookup.LoadBalancerFilter,
) (*loadbalancersdk.LoadBalancerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		regionName := strings.TrimSpace(config.Region.ValueString())
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &regionName})
		if err != nil {
			diags.AddError("Error resolving region", err.Error())
			return nil, diags
		}
		filter.RegionID = &region.Region.Id
	}

	lb, err := loadbalancerlookup.NewLoadBalancerFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving Load Balancer", err.Error())
		return nil, diags
	}
	detail, detailDiags := d.getLoadBalancerByID(ctx, lb.Id)
	diags.Append(detailDiags...)
	if diags.HasError() {
		return nil, diags
	}
	if !loadBalancerDetailMatchesFilter(detail, filter) {
		diags.AddError("Load Balancer lookup criteria changed", fmt.Sprintf("Load Balancer %s no longer matches criteria (%s). Read the data source again.", lb.Id, filter.String()))
		return nil, diags
	}
	return detail, diags
}

func loadBalancerDataSourceFilter(config LoadBalancerDataSourceModel) (loadbalancerlookup.LoadBalancerFilter, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := loadbalancerlookup.LoadBalancerFilter{}
	hasCriteria := false
	for _, criterion := range []struct {
		name  string
		value types.String
	}{
		{"id", config.ID},
		{"name", config.Name},
		{"region", config.Region},
		{"vip_address", config.VIPAddress},
		{"public_address", config.PublicAddress},
		{"v6_public_address", config.V6PublicAddress},
		{"package", config.Package},
		{"topology", config.Topology},
		{"type", config.Type},
	} {
		if criterion.value.IsNull() {
			continue
		}
		hasCriteria = true
		if criterion.value.IsUnknown() {
			diags.AddError("Unknown Load Balancer lookup criteria", fmt.Sprintf("%s must be known before reading a Load Balancer.", criterion.name))
			continue
		}
		value := strings.TrimSpace(criterion.value.ValueString())
		if criterion.name != "id" && value == "" {
			diags.AddError(fmt.Sprintf("Invalid Load Balancer %s filter", criterion.name), fmt.Sprintf("%s must not be empty.", criterion.name))
			continue
		}
		switch criterion.name {
		case "id":
			id, parseDiags := parse.UUIDString(criterion.value, "id")
			diags.Append(parseDiags...)
			filter.ID = &id
		case "name":
			filter.Name = &value
		case "region":
		case "package":
			packageValue := loadbalancersdk.LoadBalancerPackage(value)
			if !packageValue.Valid() {
				diags.AddError("Invalid Load Balancer package filter", "package must be SMALL, MEDIUM, or LARGE.")
			}
			filter.Package = &packageValue
		case "topology":
			topology := loadbalancersdk.LoadBalancerTopology(value)
			if !topology.Valid() {
				diags.AddError("Invalid Load Balancer topology filter", "topology must be SINGLE or ACTIVE_STANDBY.")
			}
			filter.Topology = &topology
		case "type":
			lbType := loadbalancersdk.LoadBalancerType(value)
			if !lbType.Valid() {
				diags.AddError("Invalid Load Balancer type filter", "type must be native or appliance.")
			}
			filter.Type = &lbType
		default:
			address, err := netip.ParseAddr(value)
			if err != nil || address.Zone() != "" {
				diags.AddError(fmt.Sprintf("Invalid Load Balancer %s filter", criterion.name), fmt.Sprintf("%s must be a valid IP address without a zone.", criterion.name))
				continue
			}
			value = address.String()
			switch criterion.name {
			case "vip_address":
				filter.VIPAddress = &value
			case "public_address":
				filter.PublicAddress = &value
			case "v6_public_address":
				filter.V6PublicAddress = &value
			}
		}
	}
	if !config.AccessLogEnabled.IsNull() {
		hasCriteria = true
		if config.AccessLogEnabled.IsUnknown() {
			diags.AddError("Unknown Load Balancer lookup criteria", "access_log_enabled must be known before reading a Load Balancer.")
		} else {
			filter.AccessLogEnabled = new(config.AccessLogEnabled.ValueBool())
		}
	}
	if !hasCriteria {
		diags.AddError("Missing Load Balancer lookup criteria", "Specify at least one of id, name, region, vip_address, public_address, v6_public_address, package, topology, type, or access_log_enabled.")
	}
	return filter, diags
}

func loadBalancerDetailMatchesFilter(lb *loadbalancersdk.LoadBalancerDetailSchema, filter loadbalancerlookup.LoadBalancerFilter) bool {
	if filter.ID != nil && lb.Id != *filter.ID || filter.Name != nil && lb.Name != strings.TrimSpace(*filter.Name) || filter.RegionID != nil && lb.Region.Id != *filter.RegionID {
		return false
	}
	if filter.Package != nil && lb.Package != *filter.Package || filter.Topology != nil && lb.Topology != *filter.Topology || filter.Type != nil && lb.Type != *filter.Type {
		return false
	}
	if filter.AccessLogEnabled != nil && lb.AccessLogEnabled != *filter.AccessLogEnabled {
		return false
	}
	for _, address := range []struct{ filter, actual *string }{
		{filter.VIPAddress, core.NullableToPtr(lb.VipAddress)},
		{filter.PublicAddress, core.NullableToPtr(lb.PublicAddress)},
		{filter.V6PublicAddress, core.NullableToPtr(lb.V6PublicAddress)},
	} {
		if address.filter != nil && (address.actual == nil || !compare.SameIPAddress(types.StringValue(*address.filter), *address.actual)) {
			return false
		}
	}
	return true
}
