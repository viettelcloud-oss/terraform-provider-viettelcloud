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

var _ datasource.DataSource = &ServerGroupMemberDataSource{}

type ServerGroupMemberDataSource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
}

func NewServerGroupMemberDataSource() datasource.DataSource {
	return &ServerGroupMemberDataSource{}
}

func (d *ServerGroupMemberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_member"
}

func (d *ServerGroupMemberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read details of a Viettel Cloud Load Balancer Server Group Member.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Server Group Member ID (UUID).",
			},
			"server_group_id": schema.StringAttribute{
				Required:    true,
				Description: "Server Group ID (UUID) that owns this member.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the Server Group Member.",
			},
			"address": schema.StringAttribute{
				Computed:    true,
				Description: "IP address of the backend member.",
			},
			"protocol_port": schema.Int64Attribute{
				Computed:    true,
				Description: "Port on which the member listens for traffic.",
			},
			"weight": schema.Int64Attribute{
				Computed:    true,
				Description: "Weight of the member in the server group.",
			},
			"backup": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the member is a backup member.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the Server Group Member.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the Server Group Member.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group Member was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Server Group Member was last updated (RFC3339).",
			},
		},
	}
}

func (d *ServerGroupMemberDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ServerGroupMemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config ServerGroupMemberDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entity, diags := d.getServerGroupMember(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || entity == nil {
		return
	}

	state := config
	populateServerGroupMemberDataSourceState(entity, &state)
	preserveServerGroupMemberConfiguredValues(entity, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *ServerGroupMemberDataSource) getServerGroupMember(ctx context.Context, config ServerGroupMemberDataSourceModel) (*loadbalancersdk.ServerGroupMemberSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	if config.ServerGroupID.IsNull() || config.ServerGroupID.IsUnknown() || strings.TrimSpace(config.ServerGroupID.ValueString()) == "" {
		diags.AddError("Missing server group ID", "server_group_id must be configured and known.")
		return nil, diags
	}
	serverGroupID, parseDiags := parse.UUIDString(config.ServerGroupID, "server_group_id")
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
		diags.AddError("Missing server group member lookup criteria", "At least one of id or name must be specified.")
		return nil, diags
	}

	if hasID && !hasName {
		return d.getServerGroupMemberByID(ctx, serverGroupID, config)
	}

	return d.getServerGroupMemberByFilter(ctx, serverGroupID, config)
}

func (d *ServerGroupMemberDataSource) getServerGroupMemberByID(ctx context.Context, serverGroupID core.UUID, config ServerGroupMemberDataSourceModel) (*loadbalancersdk.ServerGroupMemberSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	memberID, parseDiags := parse.UUIDString(config.ID, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	member, err := d.client.GetServerGroupMember(ctx, serverGroupID, memberID, loadbalancersdk.GetServerGroupMemberParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError("Server group member not found", fmt.Sprintf("Server group member %s not found under Server Group %s.", memberID, serverGroupID))
			return nil, diags
		}
		diags.AddError("Error reading server group member", err.Error())
		return nil, diags
	}

	if member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Server group member deleted", fmt.Sprintf("Server group member %s is in DELETED state.", memberID))
		return nil, diags
	}

	return member, diags
}

func (d *ServerGroupMemberDataSource) getServerGroupMemberByFilter(ctx context.Context, serverGroupID core.UUID, config ServerGroupMemberDataSourceModel) (*loadbalancersdk.ServerGroupMemberSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	var filter loadbalancerlookup.ServerGroupMemberFilter
	if !config.ID.IsNull() && !config.ID.IsUnknown() && strings.TrimSpace(config.ID.ValueString()) != "" {
		memberID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &memberID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) != "" {
		name := config.Name.ValueString()
		filter.Name = &name
	}

	finder := loadbalancerlookup.NewServerGroupMemberFinder(d.client, d.projectID, serverGroupID)
	member, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving server group member", err.Error())
		return nil, diags
	}

	if member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("Server group member deleted", fmt.Sprintf("Server group member %s is in DELETED state.", member.Id))
		return nil, diags
	}

	return &member, diags
}

func preserveServerGroupMemberConfiguredValues(entity *loadbalancersdk.ServerGroupMemberSchema, config ServerGroupMemberDataSourceModel, state *ServerGroupMemberDataSourceModel) {
	if !config.ServerGroupID.IsNull() && !config.ServerGroupID.IsUnknown() {
		state.ServerGroupID = config.ServerGroupID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == entity.Name {
		state.Name = config.Name
	}
	if compare.SameUUID(config.ID, entity.Id) {
		state.ID = config.ID
	}
}
