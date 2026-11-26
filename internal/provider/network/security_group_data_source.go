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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	networklookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &SecurityGroupDataSource{}

type SecurityGroupDataSource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewSecurityGroupDataSource() datasource.DataSource {
	return &SecurityGroupDataSource{}
}

func (d *SecurityGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (d *SecurityGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a Viettel Cloud security group by ID, name, region, or default status.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Security group ID (UUID). Exactly one security group must match.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name of the security group to filter by.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the backend.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Optional description of the security group.",
			},
			"is_default": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether this is the project's default security group to filter by.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Project region name where the security group resides to filter by.",
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID) that owns the security group.",
			},
			"project_id": schema.StringAttribute{
				Computed:    true,
				Description: "Project ID (UUID) that owns the security group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the security group was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the security group was last updated (RFC3339).",
			},
		},
	}
}

func (d *SecurityGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SecurityGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config SecurityGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sg, diags := d.getSecurityGroup(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || sg == nil {
		return
	}

	state := config
	populateSecurityGroupDataSourceState(sg, &state)
	preserveConfiguredSecurityGroupDataSourceValues(sg, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *SecurityGroupDataSource) getSecurityGroup(ctx context.Context, config SecurityGroupDataSourceModel) (*networksdk.SecurityGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	hasRegion := !config.Region.IsNull() && !config.Region.IsUnknown()
	hasIsDefault := !config.IsDefault.IsNull() && !config.IsDefault.IsUnknown()

	if !hasID && !hasName && !hasRegion && !hasIsDefault {
		diags.AddError(
			"Missing Security Group lookup criteria",
			"Specify at least one of id, name, region, or is_default to look up a security group.",
		)
		return nil, diags
	}
	if hasName && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid security group name filter", "name must not be empty.")
		return nil, diags
	}
	if hasRegion && strings.TrimSpace(config.Region.ValueString()) == "" {
		diags.AddError("Invalid security group region filter", "region must not be empty.")
		return nil, diags
	}

	if hasID && !hasName && !hasRegion && !hasIsDefault {
		return d.getSecurityGroupByID(ctx, config.ID)
	}

	return d.getSecurityGroupByFilter(ctx, config)
}

func (d *SecurityGroupDataSource) getSecurityGroupByID(ctx context.Context, idAttr types.String) (*networksdk.SecurityGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	sgID, parseDiags := parse.UUIDString(idAttr, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}

	sg, err := d.client.GetSecurityGroup(ctx, sgID, networksdk.GetSecurityGroupParams{
		ProjectID: d.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			diags.AddError("Security Group not found", fmt.Sprintf("No security group found with id %s", idAttr.ValueString()))
			return nil, diags
		}
		diags.AddError("Error reading security group", err.Error())
		return nil, diags
	}
	return sg, diags
}

func (d *SecurityGroupDataSource) getSecurityGroupByFilter(ctx context.Context, config SecurityGroupDataSourceModel) (*networksdk.SecurityGroupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	filter := networklookup.SecurityGroupFilter{}

	if !config.ID.IsNull() && !config.ID.IsUnknown() {
		sgID, parseDiags := parse.UUIDString(config.ID, "id")
		diags.Append(parseDiags...)
		if diags.HasError() {
			return nil, diags
		}
		filter.ID = &sgID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() {
		name := config.Name.ValueString()
		filter.Name = &name
	}
	if !config.IsDefault.IsNull() && !config.IsDefault.IsUnknown() {
		isDefault := config.IsDefault.ValueBool()
		filter.IsDefault = &isDefault
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() {
		regionFinder := projectlookup.NewRegionFinder(d.project, d.projectID)
		name := config.Region.ValueString()
		reg, err := regionFinder.Resolve(ctx, projectlookup.RegionFilter{Name: &name})
		if err != nil {
			diags.AddError("Error resolving region", err.Error())
			return nil, diags
		}
		filter.RegionID = &reg.Region.Id
	}

	finder := networklookup.NewSecurityGroupFinder(d.client, d.projectID)
	res, err := finder.Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Error resolving Security Group", err.Error())
		return nil, diags
	}
	return &res, diags
}

func preserveConfiguredSecurityGroupDataSourceValues(sg *networksdk.SecurityGroupSchema, config SecurityGroupDataSourceModel, state *SecurityGroupDataSourceModel) {
	if sg == nil {
		return
	}
	if compare.SameUUID(config.ID, sg.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == sg.Name {
		state.Name = config.Name
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == sg.Region.Name {
		state.Region = config.Region
	}
	if !config.IsDefault.IsNull() && !config.IsDefault.IsUnknown() && config.IsDefault.ValueBool() == sg.IsDefault {
		state.IsDefault = config.IsDefault
	}
}
