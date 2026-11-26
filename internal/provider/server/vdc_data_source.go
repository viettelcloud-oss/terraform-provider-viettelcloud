package server

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
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
)

var _ datasource.DataSource = &VDCDataSource{}

type VDCDataSource struct {
	client        *serversdk.Client
	projectClient *projectsdk.Client
	projectID     core.UUID
}

type vdcCriteria struct{ id, name, region, zone bool }

func NewVDCDataSource() datasource.DataSource {
	return &VDCDataSource{}
}

func (d *VDCDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vdc"
}

func (d *VDCDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud vDC, a group of compute hosts in one zone that an operator reserves for the project, " +
			"by ID, name, region, or zone. A vDC without a zone cannot be read. A lookup by `name` without `region` or `zone` " +
			"can also fail when the name of such a vDC contains the configured name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "vDC ID (UUID). Exactly one vDC must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "vDC name to filter by. Names are matched exactly and are not unique.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Region name of the vDC to filter by.",
			},
			"zone": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Zone name of the vDC to filter by. The zone must exist in the project.",
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID) of the vDC.",
			},
			"zone_id": schema.StringAttribute{
				Computed:    true,
				Description: "Zone ID (UUID) of the vDC.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "vDC description. Null when the vDC has none.",
			},
			"ratio": schema.Float64Attribute{
				Computed:    true,
				Description: "Overcommit ratio of the vDC, from 0 to 4. It does not scale `stats[*].total`.",
			},
			"stats": schema.ListNestedAttribute{
				Computed: true,
				Description: "Resource usage of the vDC, in the order ram, cpus, gpus, servers. The values change on every refresh. " +
					"`total` is the physical capacity, not multiplied by `ratio`, so `used` can be greater than `total`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Resource key: ram, cpus, gpus, or servers.",
						},
						"label": schema.StringAttribute{
							Computed:    true,
							Description: "Display label of the resource.",
						},
						"used": schema.Float64Attribute{
							Computed:    true,
							Description: "Amount in use. ram and gpus are in GiB, and gpus counts GPU memory, not GPUs.",
						},
						"total": schema.Float64Attribute{
							Computed:    true,
							Description: "Physical capacity. ram and gpus are in GiB. For servers it is -1, which means no limit.",
						},
						"unit": schema.StringAttribute{
							Computed:    true,
							Description: "Unit of `used` and `total`, or null when the resource has no unit.",
						},
					},
				},
			},
		},
	}
}

func (d *VDCDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.Server
	d.projectClient = data.Project
	d.projectID = data.ProjectID
}

func (d *VDCDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config VDCDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vdc, diags := d.getVDC(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || vdc == nil {
		return
	}

	state := config
	populateVDCDataSourceState(vdc, &state)
	preserveConfiguredVDCValues(vdc, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// getVDC validates every criterion before the first API call, so that an
// invalid lookup fails at plan time without a request.
func (d *VDCDataSource) getVDC(ctx context.Context, config VDCDataSourceModel) (*serversdk.VDCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	criteria := vdcCriteria{
		id:     known(config.ID),
		name:   known(config.Name),
		region: known(config.Region),
		zone:   known(config.Zone),
	}
	if !criteria.id && !criteria.name && !criteria.region && !criteria.zone {
		diags.AddError("Missing vDC lookup criteria", "Specify at least one of id, name, region, or zone to look up a vDC.")
		return nil, diags
	}
	for _, criterion := range []struct {
		set   bool
		field string
		value types.String
	}{
		{criteria.id, "id", config.ID},
		{criteria.name, "name", config.Name},
		{criteria.region, "region", config.Region},
		{criteria.zone, "zone", config.Zone},
	} {
		if criterion.set && strings.TrimSpace(criterion.value.ValueString()) == "" {
			diags.AddError("Invalid vDC lookup criteria", fmt.Sprintf("%s must not be empty or whitespace-only.", criterion.field))
		}
	}
	if diags.HasError() {
		return nil, diags
	}

	vdcID, idDiags := parse.OptionalUUIDString(config.ID, "id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return nil, diags
	}
	if criteria == (vdcCriteria{id: true}) {
		return d.getVDCByID(ctx, *vdcID)
	}
	return d.getVDCByFilter(ctx, config, vdcID)
}

func (d *VDCDataSource) getVDCByID(ctx context.Context, vdcID core.UUID) (*serversdk.VDCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	vdc, err := d.client.GetVdc(ctx, vdcID, serversdk.GetVdcParams{ProjectID: d.projectID})
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			diags.AddError("vDC not found", fmt.Sprintf("No vDC found with id %s.", vdcID))
			return nil, diags
		}
		diags.AddError("Error reading vDC", err.Error())
		return nil, diags
	}
	return vdc, diags
}

func (d *VDCDataSource) getVDCByFilter(ctx context.Context, config VDCDataSourceModel, vdcID *core.UUID) (*serversdk.VDCSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	filter := serverlookup.VDCFilter{ID: vdcID}
	if known(config.Name) {
		filter.Name = config.Name.ValueStringPointer()
	}
	if known(config.Region) {
		filter.Region = config.Region.ValueStringPointer()
	}
	if known(config.Zone) {
		filter.Zone = config.Zone.ValueStringPointer()
		zone, err := projectlookup.NewZoneFinder(d.projectClient, d.projectID).Resolve(ctx, projectlookup.ZoneFilter{Name: filter.Zone})
		if err != nil {
			diags.AddError("Unable to resolve zone", fmt.Sprintf("zone: %s", err))
			return nil, diags
		}
		filter.ZoneID = &zone.Id
	}

	vdc, err := serverlookup.NewVDCFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError(vdcLookupErrorSummary(err), err.Error())
		return nil, diags
	}

	// A list row carries the same VDCSchema as a direct read, so the resolved
	// row already holds every attribute that the schema exposes.
	return &vdc, diags
}

func preserveConfiguredVDCValues(vdc *serversdk.VDCSchema, config VDCDataSourceModel, state *VDCDataSourceModel) {
	if vdc == nil {
		return
	}
	state.ID = compare.PreservedUUID(config.ID, vdc.Id)
	state.Name = compare.PreservedString(config.Name, vdc.Name)
	state.Region = compare.PreservedString(config.Region, vdc.Region.Name)
	state.Zone = compare.PreservedString(config.Zone, vdc.Zone.Name)
}

// vdcLookupErrorSummary separates a failed list request, for example HTTP 400
// for a vDC without a zone on the same page, from a lookup that found zero or
// several vDCs.
func vdcLookupErrorSummary(err error) string {
	var apiErr *core.APIError
	var responseErr *core.ResponseError
	if errors.As(err, &apiErr) || errors.As(err, &responseErr) {
		return "Error reading vDC"
	}
	return "Error resolving vDC"
}

func known(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }
