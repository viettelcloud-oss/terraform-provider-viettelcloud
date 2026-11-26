package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &ServerGroupDataSource{}

type ServerGroupDataSource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
}

func NewServerGroupDataSource() datasource.DataSource {
	return &ServerGroupDataSource{}
}

func (d *ServerGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_server_group"
}

func (d *ServerGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read details of a Viettel Cloud Load Balancer Server Group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Server Group ID (UUID).",
			},
			"load_balancer_id": schema.StringAttribute{
				Required:    true,
				Description: "Load Balancer ID (UUID) that owns this server group.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the Server Group.",
			},
			"protocol": schema.StringAttribute{
				Computed:    true,
				Description: "Protocol for the server group.",
			},
			"algorithm": schema.StringAttribute{
				Computed:    true,
				Description: "Load balancing algorithm.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Description of the Server Group.",
			},
			"enable_h2": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether HTTP/2 support is enabled.",
			},
			"session_persistence": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Session persistence configuration.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:    true,
						Description: "Session persistence type.",
					},
					"cookie_name": schema.StringAttribute{
						Computed:    true,
						Description: "Cookie name when session persistence type is APP_COOKIE.",
					},
				},
			},
			"health_check": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "Health check monitor configuration.",
				Attributes: map[string]schema.Attribute{
					"monitor_type": schema.StringAttribute{
						Computed:    true,
						Description: "Monitor type.",
					},
					"delay": schema.Int64Attribute{
						Computed:    true,
						Description: "Delay between health checks in seconds.",
					},
					"timeout": schema.Int64Attribute{
						Computed:    true,
						Description: "Health check timeout in seconds.",
					},
					"max_retries": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of successful checks before considering server online.",
					},
					"max_retries_down": schema.Int64Attribute{
						Computed:    true,
						Description: "Number of failed checks before considering server offline.",
					},
					"path": schema.StringAttribute{
						Computed:    true,
						Description: "HTTP URL path to check.",
					},
					"http_method": schema.StringAttribute{
						Computed:    true,
						Description: "HTTP method to use.",
					},
					"expected_codes": schema.StringAttribute{
						Computed:    true,
						Description: "Expected HTTP status codes.",
					},
				},
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the Server Group.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the Server Group.",
			},
			"member_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of backend members in the Server Group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group was last updated (RFC3339).",
			},
		},
	}
}

func (d *ServerGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ServerGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ServerGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entity, diags := d.getServerGroup(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || entity == nil {
		return
	}

	state := config
	populateServerGroupDataSourceState(entity, &state)
	preserveServerGroupConfiguredValues(entity, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *ServerGroupDataSource) getServerGroup(ctx context.Context, config ServerGroupDataSourceModel) (*loadbalancersdk.ServerGroupDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	if config.LoadBalancerID.IsNull() || config.LoadBalancerID.IsUnknown() || strings.TrimSpace(config.LoadBalancerID.ValueString()) == "" {
		diags.AddError("Missing load balancer ID", "load_balancer_id must be configured and known.")
		return nil, diags
	}
	lbID, parseDiags := parse.UUIDString(config.LoadBalancerID, "load_balancer_id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	if !config.ID.IsNull() && !config.ID.IsUnknown() && strings.TrimSpace(config.ID.ValueString()) == "" {
		diags.AddError("Invalid attribute value", "id must not be empty or whitespace-only.")
		return nil, diags
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid attribute value", "name must not be empty or whitespace-only.")
		return nil, diags
	}

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown() && strings.TrimSpace(config.ID.ValueString()) != ""
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) != ""

	if !hasID && !hasName {
		diags.AddError("Missing Server Group lookup criteria", "At least one of id or name must be specified.")
		return nil, diags
	}

	if hasID {
		return d.getServerGroupByID(ctx, lbID, config)
	}

	return d.getServerGroupByFilter(ctx, lbID, config)
}

func (d *ServerGroupDataSource) getServerGroupByID(ctx context.Context, lbID core.UUID, config ServerGroupDataSourceModel) (*loadbalancersdk.ServerGroupDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	groupID, parseDiags := parse.UUIDString(config.ID, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	sg, err := d.client.GetServerGroup(ctx, lbID, groupID, loadbalancersdk.GetServerGroupParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError("Server Group not found", fmt.Sprintf("Server Group %s not found under Load Balancer %s.", groupID, lbID))
			return nil, diags
		}
		diags.AddError("Error reading Server Group", err.Error())
		return nil, diags
	}

	if sg.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Server Group not found", fmt.Sprintf("Server Group %s not found under Load Balancer %s.", groupID, lbID))
		return nil, diags
	}

	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		configuredName := strings.TrimSpace(config.Name.ValueString())
		if configuredName != "" && sg.Name != configuredName {
			diags.AddError("Server Group mismatch", fmt.Sprintf("Server Group with ID %s has name %q, which does not match expected name %q.", groupID, sg.Name, configuredName))
			return nil, diags
		}
	}

	return sg, diags
}

func (d *ServerGroupDataSource) getServerGroupByFilter(ctx context.Context, lbID core.UUID, config ServerGroupDataSourceModel) (*loadbalancersdk.ServerGroupDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	name := strings.TrimSpace(config.Name.ValueString())
	filter := loadbalancerlookup.ServerGroupFilter{
		Name: &name,
	}

	finderInstance := loadbalancerlookup.NewServerGroupFinder(d.client, d.projectID, lbID)
	candidate, err := finderInstance.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Server Group lookup failed", err.Error())
		return nil, diags
	}

	sg, err := d.client.GetServerGroup(ctx, lbID, candidate.Id, loadbalancersdk.GetServerGroupParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		diags.AddError("Error fetching Server Group details", err.Error())
		return nil, diags
	}

	return sg, diags
}

func preserveServerGroupConfiguredValues(api *loadbalancersdk.ServerGroupDetailSchema, config ServerGroupDataSourceModel, state *ServerGroupDataSourceModel) {
	if api == nil {
		return
	}
	state.LoadBalancerID = config.LoadBalancerID
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == api.Name {
		state.Name = config.Name
	}
	if !config.ID.IsNull() && !config.ID.IsUnknown() && compare.SameUUID(config.ID, api.Id) {
		state.ID = config.ID
	}
}
