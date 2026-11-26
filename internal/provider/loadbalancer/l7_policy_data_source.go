package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	loadbalancerlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

var (
	_ datasource.DataSource              = &L7PolicyDataSource{}
	_ datasource.DataSourceWithConfigure = &L7PolicyDataSource{}
)

type L7PolicyDataSource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
}

func NewL7PolicyDataSource() datasource.DataSource {
	return &L7PolicyDataSource{}
}

func (d *L7PolicyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_l7_policy"
}

func (d *L7PolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud Load Balancer L7 Policy by ID or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "L7 policy ID (UUID). Exactly one policy must match.",
			},
			"listener_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Listener ID (UUID) owning the L7 policy.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the L7 policy to filter by.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "L7 policy description.",
			},
			"action": schema.StringAttribute{
				Computed:    true,
				Description: "L7 policy action.",
			},
			"position": schema.Int64Attribute{
				Computed:    true,
				Description: "Position of the L7 policy within the listener.",
			},
			"redirect_server_group_id": schema.StringAttribute{
				Computed:    true,
				Description: "Redirect server group ID (UUID).",
			},
			"redirect_url": schema.StringAttribute{
				Computed:    true,
				Description: "Redirect URL.",
			},
			"redirect_prefix": schema.StringAttribute{
				Computed:    true,
				Description: "Redirect prefix.",
			},
			"redirect_http_code": schema.Int64Attribute{
				Computed:    true,
				Description: "Redirect HTTP code.",
			},
			"rule_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of rules associated with the L7 policy.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Provisioning status of the L7 policy.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Operational status of the L7 policy.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 policy was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 policy was last updated (RFC3339).",
			},
		},
	}
}

func (d *L7PolicyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData),
		)
		return
	}

	d.client = data.LoadBalancer
	d.projectID = data.ProjectID
}

func (d *L7PolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config L7PolicyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy, diags := d.getL7Policy(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || policy == nil {
		return
	}

	state := config
	populateL7PolicyDataSourceState(policy, &state)
	preserveL7PolicyConfiguredValues(policy, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *L7PolicyDataSource) getL7Policy(ctx context.Context, config L7PolicyDataSourceModel) (*loadbalancersdk.L7PolicySchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()

	if !hasID && !hasName {
		diags.AddError(
			"Missing L7 policy lookup criteria",
			"At least one of id or name must be specified.",
		)
		return nil, diags
	}

	if hasID {
		trimmedID := strings.TrimSpace(config.ID.ValueString())
		if trimmedID == "" {
			diags.AddError("Invalid lookup criteria", "id must not be empty or whitespace-only.")
			return nil, diags
		}
	}
	if hasName {
		trimmedName := strings.TrimSpace(config.Name.ValueString())
		if trimmedName == "" {
			diags.AddError("Invalid lookup criteria", "name must not be empty or whitespace-only.")
			return nil, diags
		}
	}
	if !config.ListenerID.IsNull() && !config.ListenerID.IsUnknown() {
		trimmedListenerID := strings.TrimSpace(config.ListenerID.ValueString())
		if trimmedListenerID == "" {
			diags.AddError("Invalid lookup criteria", "listener_id must not be empty or whitespace-only.")
			return nil, diags
		}
	}

	if hasID && !hasName && config.ListenerID.IsNull() {
		return d.getL7PolicyByID(ctx, config)
	}
	return d.getL7PolicyByFilter(ctx, config)
}

func (d *L7PolicyDataSource) getL7PolicyByID(ctx context.Context, config L7PolicyDataSourceModel) (*loadbalancersdk.L7PolicySchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	id, diagUUID := parse.UUIDString(config.ID, "id")
	diags.Append(diagUUID...)
	if diags.HasError() {
		return nil, diags
	}

	detail, err := d.client.GetL7policy(ctx, id, loadbalancersdk.GetL7policyParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError(
				"L7 policy not found",
				fmt.Sprintf("L7 policy %s not found: %s", id, err),
			)
			return nil, diags
		}
		diags.AddError("Error fetching L7 policy", err.Error())
		return nil, diags
	}
	if detail == nil {
		diags.AddError("L7 policy not found", fmt.Sprintf("L7 policy %s not found", id))
		return nil, diags
	}

	if detail.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError(
			"L7 policy not found",
			fmt.Sprintf("L7 policy %s is deleted.", id),
		)
		return nil, diags
	}

	if !config.ListenerID.IsNull() && !config.ListenerID.IsUnknown() {
		listenerID, diagListener := parse.UUIDString(config.ListenerID, "listener_id")
		diags.Append(diagListener...)
		if diags.HasError() {
			return nil, diags
		}
		if detail.ListenerId != listenerID {
			diags.AddError(
				"L7 policy not found",
				fmt.Sprintf("L7 policy %s does not belong to listener %s", id, listenerID),
			)
			return nil, diags
		}
	}

	return l7PolicyDetailToSchema(detail), diags
}

func (d *L7PolicyDataSource) getL7PolicyByFilter(ctx context.Context, config L7PolicyDataSourceModel) (*loadbalancersdk.L7PolicySchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	var filter loadbalancerlookup.L7PolicyFilter

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		id, diagID := parse.UUIDString(config.ID, "id")
		diags.Append(diagID...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &id
	}

	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}

	if !config.ListenerID.IsNull() && !config.ListenerID.IsUnknown() {
		listenerID, diagListener := parse.UUIDString(config.ListenerID, "listener_id")
		diags.Append(diagListener...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ListenerID = &listenerID
	}

	finder := loadbalancerlookup.NewL7PolicyFinder(d.client, d.projectID)
	policy, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving L7 policy", err.Error())
		return nil, diags
	}

	return &policy, diags
}

func preserveL7PolicyConfiguredValues(policy *loadbalancersdk.L7PolicySchema, config L7PolicyDataSourceModel, state *L7PolicyDataSourceModel) {
	if policy == nil {
		return
	}
	if compare.SameUUID(config.ID, policy.Id) {
		state.ID = config.ID
	}
	name := core.NullableToPtr(policy.Name)
	if !config.Name.IsNull() && !config.Name.IsUnknown() && name != nil && strings.TrimSpace(config.Name.ValueString()) == *name {
		state.Name = config.Name
	}
	if compare.SameUUID(config.ListenerID, policy.ListenerId) {
		state.ListenerID = config.ListenerID
	}
}
