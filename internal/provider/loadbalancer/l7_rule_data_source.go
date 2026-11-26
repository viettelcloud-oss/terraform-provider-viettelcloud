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
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &L7RuleDataSource{}

type L7RuleDataSource struct {
	client    *loadbalancersdk.Client
	projectID core.UUID
}

func NewL7RuleDataSource() datasource.DataSource {
	return &L7RuleDataSource{}
}

func (d *L7RuleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lb_l7_rule"
}

func (d *L7RuleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read details of a Viettel Cloud Load Balancer L7 Rule.",
		Attributes: map[string]schema.Attribute{
			"l7_policy_id": schema.StringAttribute{
				Required:    true,
				Description: "L7 Policy ID (UUID) that owns the L7 rule.",
			},
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "L7 Rule ID (UUID).",
			},
			"type": schema.StringAttribute{
				Computed:    true,
				Description: "Rule type.",
			},
			"compare_type": schema.StringAttribute{
				Computed:    true,
				Description: "Comparison type.",
			},
			"value": schema.StringAttribute{
				Computed:    true,
				Description: "Value to compare against.",
			},
			"key": schema.StringAttribute{
				Computed:    true,
				Description: "Key to match against.",
			},
			"invert": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the rule match is inverted.",
			},
			"provisioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Current provisioning status of the L7 rule.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current operating status of the L7 rule.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 rule was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the L7 rule was last updated (RFC3339).",
			},
		},
	}
}

func (d *L7RuleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *L7RuleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config L7RuleDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	detail, diags := d.getL7Rule(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || detail == nil {
		return
	}

	state := config
	populateL7RuleDataSourceState(detail, &state)
	preserveL7RuleConfiguredValues(detail, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *L7RuleDataSource) getL7Rule(ctx context.Context, config L7RuleDataSourceModel) (*loadbalancersdk.L7RuleSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	policyID, parseDiags := parse.UUIDString(config.L7PolicyID, "l7_policy_id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	if !hasID || strings.TrimSpace(config.ID.ValueString()) == "" {
		diags.AddError(
			"Missing Load Balancer L7 Rule lookup criteria",
			"Specify id under l7_policy_id to look up an L7 Rule.",
		)
		return nil, diags
	}

	return d.getL7RuleByID(ctx, policyID, config.ID)
}

func (d *L7RuleDataSource) getL7RuleByID(ctx context.Context, policyID core.UUID, idAttr types.String) (*loadbalancersdk.L7RuleSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	ruleID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	rule, err := d.client.GetL7rule(ctx, policyID, ruleID, loadbalancersdk.GetL7ruleParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, loadbalancersdk.ErrNotFound) {
			diags.AddError("L7 Rule not found", fmt.Sprintf("No L7 Rule found with id %s under L7 policy %s", idAttr.ValueString(), policyID))
			return nil, diags
		}
		diags.AddError("Error reading Load Balancer L7 Rule", err.Error())
		return nil, diags
	}
	if rule.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		diags.AddError("L7 Rule not found", fmt.Sprintf("L7 Rule %s under L7 policy %s has been deleted", idAttr.ValueString(), policyID))
		return nil, diags
	}
	return rule, diags
}
