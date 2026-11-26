package network

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
	networksdk "github.com/viettelcloud-oss/sdks/go/network"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &VPCPeeringDataSource{}

type VPCPeeringDataSource struct {
	client    *networksdk.Client
	projectID core.UUID
}

func NewVPCPeeringDataSource() datasource.DataSource {
	return &VPCPeeringDataSource{}
}

func (d *VPCPeeringDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc_peering"
}

func (d *VPCPeeringDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud VPC peering by ID, name, requester and accepter VPC, or status. " +
			"Without `id`, `name`, or `status`, only `pending` and `active` peerings match.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "VPC peering ID (UUID).",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Exact name of the peering to filter by.",
			},
			"requester_vpc_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "ID (UUID) of the requester VPC to filter by.",
			},
			"accepter_vpc_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "ID (UUID) of the accepter VPC to filter by.",
			},
			"status": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Peering status to filter by: `pending`, `active`, `rejected`, or `error`.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Description of the peering.",
			},
			"vpc_peering_type": schema.StringAttribute{
				Computed:    true,
				Description: "`intra_region` when both VPCs are in one region, otherwise `inter_region`.",
			},
			"accepter_project_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID (UUID) of the project that owns the accepter VPC.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the peering was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the peering was last updated (RFC3339).",
			},
		},
	}
}

func (d *VPCPeeringDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *VPCPeeringDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VPCPeeringDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	peering, diags := d.getVPCPeering(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || peering == nil {
		return
	}

	state := config
	populateVPCPeeringDataSourceState(peering, &state)
	preserveConfiguredVPCPeeringValues(peering, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *VPCPeeringDataSource) getVPCPeering(ctx context.Context, config VPCPeeringDataSourceModel) (*networksdk.VPCPeeringSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := known(config.ID)
	hasName := known(config.Name)
	hasRequester := known(config.RequesterVPCID)
	hasAccepter := known(config.AccepterVPCID)
	hasStatus := known(config.Status)

	if !hasID && !hasName && !hasRequester && !hasAccepter && !hasStatus {
		diags.AddError(
			"Missing VPC Peering lookup criteria",
			"Specify at least one of id, name, requester_vpc_id, accepter_vpc_id, or status to look up a VPC peering.",
		)
		return nil, diags
	}
	for _, filter := range []struct {
		set   bool
		value types.String
		name  string
	}{
		{hasName, config.Name, "name"},
		{hasStatus, config.Status, "status"},
	} {
		if filter.set && strings.TrimSpace(filter.value.ValueString()) == "" {
			diags.AddError(
				fmt.Sprintf("Invalid VPC peering %s filter", filter.name),
				fmt.Sprintf("%s must not be empty.", filter.name),
			)
			return nil, diags
		}
	}

	if hasID && !hasName && !hasRequester && !hasAccepter && !hasStatus {
		return d.getVPCPeeringByID(ctx, config.ID)
	}
	return d.getVPCPeeringByFilter(ctx, config)
}

func (d *VPCPeeringDataSource) getVPCPeeringByID(ctx context.Context, idAttr types.String) (*networksdk.VPCPeeringSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	peeringID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	peering, err := d.client.GetVpcPeering(ctx, peeringID, networksdk.GetVpcPeeringParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("VPC peering not found", fmt.Sprintf("No VPC peering found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading VPC peering", err.Error())
		return nil, diags
	}
	return peering, diags
}

func (d *VPCPeeringDataSource) getVPCPeeringByFilter(ctx context.Context, config VPCPeeringDataSourceModel) (*networksdk.VPCPeeringSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.VPCPeeringFilter{}

	for _, criterion := range []struct {
		value  types.String
		name   string
		target **core.UUID
	}{
		{config.ID, "id", &filter.ID},
		{config.RequesterVPCID, "requester_vpc_id", &filter.RequesterVPCID},
		{config.AccepterVPCID, "accepter_vpc_id", &filter.AccepterVPCID},
	} {
		if !known(criterion.value) {
			continue
		}
		id, parseDiags := parse.UUIDString(criterion.value, criterion.name)
		diags.Append(parseDiags...)
		*criterion.target = &id
	}
	if diags.HasError() {
		return nil, diags
	}
	if known(config.Name) {
		name := strings.TrimSpace(config.Name.ValueString())
		filter.Name = &name
	}
	if known(config.Status) {
		status := strings.TrimSpace(config.Status.ValueString())
		filter.Status = &status
	}

	result, err := networklookup.NewVPCPeeringFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving VPC peering", err.Error())
		return nil, diags
	}
	return &result, diags
}

// preserveConfiguredVPCPeeringValues keeps each lookup criterion as configured
// when it names the same value as the response, so that a case or whitespace
// variant does not differ from state.
func preserveConfiguredVPCPeeringValues(peering *networksdk.VPCPeeringSchema, config VPCPeeringDataSourceModel, state *VPCPeeringDataSourceModel) {
	if peering == nil {
		return
	}
	state.ID = compare.PreservedUUID(config.ID, peering.Id)
	state.RequesterVPCID = compare.PreservedUUID(config.RequesterVPCID, peering.RequesterVpc.Id)
	state.AccepterVPCID = compare.PreservedUUID(config.AccepterVPCID, peering.AccepterVpc.Id)
	if known(config.Name) && strings.TrimSpace(config.Name.ValueString()) == peering.Name {
		state.Name = config.Name
	}
	if known(config.Status) && strings.TrimSpace(config.Status.ValueString()) == string(peering.Status) {
		state.Status = config.Status
	}
}

func known(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}
