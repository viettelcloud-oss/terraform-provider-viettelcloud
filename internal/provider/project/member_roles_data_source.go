package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &MemberRolesDataSource{}

type MemberRolesDataSource struct {
	client    *projectsdk.Client
	projectID core.UUID
}

func NewMemberRolesDataSource() datasource.DataSource {
	return &MemberRolesDataSource{}
}

func (d *MemberRolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_member_roles"
}

func (d *MemberRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Get the IAM roles of a human member of the project by username.",
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Username of the member to look up. Outer whitespace is ignored; the rest must match exactly, including case.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Project member ID (UUID).",
			},
			"member_user_id": schema.StringAttribute{
				Computed:    true,
				Description: "User ID (UUID) of the member.",
			},
			"member_email": schema.StringAttribute{
				Computed:    true,
				Description: "Email of the member. It can be empty.",
			},
			"member_is_admin": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the member is a project admin.",
			},
			"roles": schema.SetNestedAttribute{
				Computed:    true,
				Description: "Role assignments of the member.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role_urn": schema.StringAttribute{Computed: true, Description: "URN of the role."},
						"enabled":  schema.BoolAttribute{Computed: true, Description: "Whether the assignment is enabled."},
					},
				},
			},
			"assignments": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Role assignments with role details, sorted by URN.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           schema.StringAttribute{Computed: true, Description: "Assignment ID (UUID)."},
						"urn":          schema.StringAttribute{Computed: true, Description: "URN of the role."},
						"enabled":      schema.BoolAttribute{Computed: true, Description: "Whether the assignment is enabled."},
						"display_name": schema.StringAttribute{Computed: true, Description: "Display name of the role."},
						"description":  schema.StringAttribute{Computed: true, Description: "Description of the role."},
						"type":         schema.StringAttribute{Computed: true, Description: "Role type: predefined or custom."},
						"role_enabled": schema.BoolAttribute{Computed: true, Description: "Whether the role itself is enabled."},
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the member joined the project (RFC3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the member was last updated (RFC3339).",
			},
		},
	}
}

func (d *MemberRolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MemberRolesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config MemberRolesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, diags := d.getMemberRoles(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || member == nil {
		return
	}

	state := config
	populateMemberRolesDataSourceState(member, &state)
	if strings.TrimSpace(config.Username.ValueString()) == member.User.Username {
		state.Username = config.Username
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// getMemberRoles resolves the member from the list result, which carries the
// same ProjectMemberSchema as a direct read, so no second call by ID follows.
func (d *MemberRolesDataSource) getMemberRoles(ctx context.Context, config MemberRolesDataSourceModel) (*projectsdk.ProjectMemberSchema, diag.Diagnostics) {
	var diags diag.Diagnostics

	if config.Username.IsNull() || config.Username.IsUnknown() {
		diags.AddError("Missing Project Member Roles lookup criteria", "Specify username to look up the roles of a project member.")
		return nil, diags
	}
	username := config.Username.ValueString()
	if strings.TrimSpace(username) == "" {
		diags.AddError("Invalid Project Member Roles lookup criteria", "username must not be empty or whitespace-only.")
		return nil, diags
	}

	member, err := projectlookup.NewProjectMemberFinder(d.client, d.projectID).
		Resolve(ctx, projectlookup.ProjectMemberFilter{Username: &username})
	if err != nil {
		diags.AddError("Error resolving Project Member Roles", err.Error())
		return nil, diags
	}
	return &member, diags
}
