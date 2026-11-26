package loadbalancer

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
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &ListenerDataSource{}

type ListenerDataSource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
}

func NewListenerDataSource() datasource.DataSource {
	return &ListenerDataSource{}
}

func (d *ListenerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_listener"
}

func (d *ListenerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud Load Balancer Listener by ID or name under a Load Balancer.",
		Attributes: map[string]schema.Attribute{
			"load_balancer_id": schema.StringAttribute{
				Required:    true,
				Description: "Load Balancer ID (UUID) that owns the listener.",
			},
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Listener ID (UUID). Exactly one Listener must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the listener to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Description of the listener (note: backend API omits description on detail responses and it resolves to null).",
			},
			"protocol": schema.StringAttribute{
				Computed:    true,
				Description: "Protocol of the listener.",
			},
			"protocol_port": schema.Int64Attribute{
				Computed:    true,
				Description: "Port on which the listener listens for traffic.",
			},
			"server_group_id": schema.StringAttribute{
				Computed:    true,
				Description: "Default server group ID (UUID) for the listener.",
			},
			"default_tls_cert_id": schema.StringAttribute{
				Computed:    true,
				Description: "Default TLS certificate ID (UUID) for the listener.",
			},
			"sni_tls_cert_ids": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Set of SNI TLS certificate IDs (UUIDs).",
			},
			"allowed_cidrs": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "List of allowed IPv4 CIDRs that can access this listener.",
			},
			"listener_timeout": schema.Int64Attribute{
				Computed:    true,
				Description: "Frontend client inactivity timeout in milliseconds.",
			},
			"server_timeout": schema.Int64Attribute{
				Computed:    true,
				Description: "Backend member inactivity timeout in milliseconds.",
			},
			"server_connecting_timeout": schema.Int64Attribute{
				Computed:    true,
				Description: "Backend connection timeout in milliseconds.",
			},
			"enable_h2": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether HTTP/2 is enabled.",
			},
			"set_x_forwarded_for": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether X-Forwarded-For header is inserted.",
			},
			"set_x_forwarded_port": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether X-Forwarded-Port header is inserted.",
			},
			"set_x_forwarded_proto": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether X-Forwarded-Proto header is inserted.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status of the listener.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status of the listener.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the listener was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the listener was last updated (RFC3339).",
			},
		},
	}
}

func (d *ListenerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.LoadBalancer
	d.projectID = data.ProjectID
}

func (d *ListenerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ListenerDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, diags := d.getListener(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || detail == nil {
		return
	}

	state := config
	populateListenerDataSourceState(detail, &state)
	preserveListenerConfiguredValues(detail, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *ListenerDataSource) getListener(ctx context.Context, config ListenerDataSourceModel) (*loadbalancersdk.ListenerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	lbID, parseDiags := parse.UUIDString(config.LoadBalancerID, "load_balancer_id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()

	if !hasID && !hasName {
		diags.AddError(
			"Missing Load Balancer Listener lookup criteria",
			"Specify at least one of id or name under load_balancer_id to look up a Listener.",
		)
		return nil, diags
	}

	if hasName && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid listener name filter", "name must not be empty.")
		return nil, diags
	}

	if hasID && !hasName {
		return d.getListenerByID(ctx, lbID, config.ID)
	}

	return d.getListenerByFilter(ctx, lbID, config)
}

func (d *ListenerDataSource) getListenerByID(ctx context.Context, lbID core.UUID, idAttr types.String) (*loadbalancersdk.ListenerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	listenerID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	detail, err := d.client.GetListener(ctx, lbID, listenerID, loadbalancersdk.GetListenerParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError("Listener not found", fmt.Sprintf("No Listener found with id %s under load balancer %s", idAttr.ValueString(), lbID))
			return nil, diags
		}
		diags.AddError("Error reading Load Balancer Listener", err.Error())
		return nil, diags
	}
	if detail.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Listener not found", fmt.Sprintf("Listener %s under load balancer %s has been deleted", idAttr.ValueString(), lbID))
		return nil, diags
	}
	return detail, diags
}

func (d *ListenerDataSource) getListenerByFilter(ctx context.Context, lbID core.UUID, config ListenerDataSourceModel) (*loadbalancersdk.ListenerDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := loadbalancerlookup.ListenerFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		listenerID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &listenerID
	}

	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}

	finder := loadbalancerlookup.NewListenerFinder(d.client, d.projectID, lbID)
	res, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving Load Balancer Listener", err.Error())
		return nil, diags
	}

	detail, err := d.client.GetListener(ctx, lbID, res.Id, loadbalancersdk.GetListenerParams{ProjectID: d.projectID})
	if err != nil {
		diags.AddError("Error reading Load Balancer Listener details", err.Error())
		return nil, diags
	}
	if detail.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Listener not found", fmt.Sprintf("Listener %s under load balancer %s has been deleted", res.Id, lbID))
		return nil, diags
	}

	return detail, diags
}
