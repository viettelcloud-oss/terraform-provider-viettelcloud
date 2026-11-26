package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var _ datasource.DataSource = &CustomRoleDataSource{}

type CustomRoleDataSource struct {
	client    *projectsdk.Client
	projectID core.UUID
}

func NewCustomRoleDataSource() datasource.DataSource {
	return &CustomRoleDataSource{}
}

func (d *CustomRoleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_custom_role"
}

func (d *CustomRoleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get a custom role of the configured Viettel Cloud project by ID or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Custom Role ID (UUID) to look up.",
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Custom Role name to look up. It must match exactly; `ops` does not match `ops-admin`.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name of the Custom Role.",
			},
			"description": schema.StringAttribute{
				Computed:    true,
				Description: "Description of the Custom Role.",
			},
			"enabled": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the Custom Role grants its actions.",
			},
			"actions": schema.SetNestedAttribute{
				Computed:    true,
				Description: "IAM actions that the Custom Role grants.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						reference.URN: schema.StringAttribute{
							Computed:    true,
							Description: "URN of the IAM action.",
						},
					},
				},
			},
			"urn": schema.StringAttribute{
				Computed:    true,
				Description: "Custom Role URN.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Custom Role was created (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Custom Role was last updated (RFC3339).",
			},
		},
	}
}

func (d *CustomRoleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client = data.Project
	d.projectID = data.ProjectID
}

func (d *CustomRoleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config CustomRoleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	role, diags := d.getCustomRole(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || role == nil {
		return
	}

	state := config
	populateCustomRoleModel(role, &state)
	preserveConfiguredCustomRoleValues(role, config, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// getCustomRole reads by ID whenever an ID is configured, and then requires a
// configured name to match the role found, so both criteria must agree.
func (d *CustomRoleDataSource) getCustomRole(ctx context.Context, config CustomRoleModel) (*projectsdk.CustomRoleDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	hasID := !config.ID.IsNull() && !config.ID.IsUnknown()
	hasName := !config.Name.IsNull() && !config.Name.IsUnknown()
	if !hasID && !hasName {
		diags.AddError("Missing Custom Role lookup criteria", "Specify id or name to look up a Custom Role.")
		return nil, diags
	}
	if hasID && strings.TrimSpace(config.ID.ValueString()) == "" {
		diags.AddError("Invalid Custom Role lookup criteria", "id must not be empty or whitespace-only.")
	}
	if hasName && strings.TrimSpace(config.Name.ValueString()) == "" {
		diags.AddError("Invalid Custom Role lookup criteria", "name must not be empty or whitespace-only.")
	}
	if diags.HasError() {
		return nil, diags
	}

	if !hasID {
		return d.getCustomRoleByFilter(ctx, config)
	}

	roleID, parseDiags := parse.UUIDString(config.ID, "id")
	diags.Append(parseDiags...)
	if diags.HasError() {
		return nil, diags
	}
	role, getDiags := d.getCustomRoleByID(ctx, roleID)
	diags.Append(getDiags...)
	if diags.HasError() {
		return nil, diags
	}
	if name := strings.TrimSpace(config.Name.ValueString()); hasName && role.Name != name {
		diags.AddError(
			"Custom Role lookup criteria do not match",
			fmt.Sprintf("Custom Role %s is named %q, not %q.", roleID, role.Name, name),
		)
		return nil, diags
	}
	return role, diags
}

func (d *CustomRoleDataSource) getCustomRoleByID(ctx context.Context, roleID core.UUID) (*projectsdk.CustomRoleDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	role, err := d.client.GetCustomRole(ctx, d.projectID, roleID)
	if err != nil {
		if errors.Is(err, projectsdk.ErrNotFound) {
			diags.AddError("Custom Role not found", fmt.Sprintf("No Custom Role found with id %s in the configured project.", roleID))
			return nil, diags
		}
		diags.AddError("Error reading Custom Role", customRoleAPIErrorDetail(err))
		return nil, diags
	}
	return role, diags
}

func (d *CustomRoleDataSource) getCustomRoleByFilter(ctx context.Context, config CustomRoleModel) (*projectsdk.CustomRoleDetailSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	name := config.Name.ValueString()
	role, err := projectlookup.NewCustomRoleFinder(d.client, d.projectID).Resolve(ctx, projectlookup.CustomRoleFilter{Name: &name})
	if err != nil {
		diags.AddError("Error resolving Custom Role", err.Error())
		return nil, diags
	}

	// A list row has no actions, so the role is read again by its ID.
	return d.getCustomRoleByID(ctx, role.Id)
}

// preserveConfiguredCustomRoleValues keeps the configured lookup criteria when
// they name the role that was found, so outer whitespace or UUID casing in the
// configuration does not appear as drift.
func preserveConfiguredCustomRoleValues(role *projectsdk.CustomRoleDetailSchema, config CustomRoleModel, state *CustomRoleModel) {
	if role == nil {
		return
	}
	if compare.SameUUID(config.ID, role.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() &&
		strings.TrimSpace(config.Name.ValueString()) == role.Name {
		state.Name = config.Name
	}
}
