package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
)

var (
	_ resource.Resource                   = &MemberRolesResource{}
	_ resource.ResourceWithImportState    = &MemberRolesResource{}
	_ resource.ResourceWithValidateConfig = &MemberRolesResource{}
)

type MemberRolesResource struct {
	client    *projectsdk.Client
	projectID core.UUID
}

func NewMemberRolesResource() resource.Resource {
	return &MemberRolesResource{}
}

func (r *MemberRolesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_member_roles"
}

func (r *MemberRolesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage the complete set of IAM roles of an existing project member. " +
			"The resource owns the member's whole role set: creating it replaces every role the member holds, " +
			"including roles assigned outside Terraform, and every later plan proposes removing any role assigned outside Terraform. " +
			"Destroying it removes every role of the member, keeps the member in the project, and leaves the member's project admin flag unchanged. " +
			"Manage the roles of one member in one place only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Project member ID (UUID). This is the import ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"member": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Existing human member of the project whose roles the resource manages. Changing the member removes every role of the previous member and replaces the resource.",
				Attributes:  reference.Attributes(memberCriterionUsername, "Username of the member. Outer whitespace is ignored; the rest must match exactly, including case."),
				PlanModifiers: []planmodifier.Object{
					replace.Reference(memberCriterionUsername, compare.SameTrimmedString),
				},
			},
			"roles": schema.SetNestedAttribute{
				Required: true,
				Description: fmt.Sprintf("Complete set of role assignments of the member, at most %d. ", maxMemberRoles) +
					"Each role may appear once. Roles not listed are removed from the member; set it to [] to remove every role.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role": schema.SingleNestedAttribute{
							Required:    true,
							Description: "Predefined role, or custom role of this project, to assign.",
							Attributes:  reference.Attributes(roleCriterionURN, "URN of the role."),
						},
						"enabled": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(true),
							Description: "Whether the assignment grants the role's permissions. Defaults to true; set it to false to keep the role assigned without granting it.",
						},
					},
				},
			},
			"assignments": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Role assignments reported by the backend, sorted by URN.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           schema.StringAttribute{Computed: true, Description: "Assignment ID (UUID). It stays the same while the role remains assigned."},
						"urn":          schema.StringAttribute{Computed: true, Description: "URN of the role."},
						"enabled":      schema.BoolAttribute{Computed: true, Description: "Whether the assignment is enabled."},
						"display_name": schema.StringAttribute{Computed: true, Description: "Display name of the role."},
						"description":  schema.StringAttribute{Computed: true, Description: "Description of the role."},
						"type":         schema.StringAttribute{Computed: true, Description: "Role type: predefined or custom."},
						"role_enabled": schema.BoolAttribute{Computed: true, Description: "Whether the role itself is enabled. A disabled role grants nothing even when the assignment is enabled."},
					},
				},
			},
			"member_user_id": schema.StringAttribute{
				Computed:    true,
				Description: "User ID (UUID) of the member.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"member_email": schema.StringAttribute{
				Computed:    true,
				Description: "Email of the member. It can be empty.",
			},
			"member_is_admin": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether the member is a project admin. A project admin holds every permission, so its roles grant nothing more.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the member joined the project (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the member was last updated (RFC3339). Role changes alone do not change it.",
			},
		},
	}
}

func (r *MemberRolesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Project
	r.projectID = data.ProjectID
}

func (r *MemberRolesResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var roles types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("roles"), &roles)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateMemberRoles(roles)...)
}

func (r *MemberRolesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MemberRolesResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	username := reference.Get(plan.Member, memberCriterionUsername)
	if username.IsNull() || username.IsUnknown() || strings.TrimSpace(username.ValueString()) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("member").AtName(memberCriterionUsername),
			"Missing Project Member Roles member", "member.username must be configured, known, and not blank.")
	}
	body, diags := buildMemberRolesRequest(ctx, plan.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	usernameValue := username.ValueString()
	member, err := projectlookup.NewProjectMemberFinder(r.client, r.projectID).
		Resolve(ctx, projectlookup.ProjectMemberFilter{Username: &usernameValue})
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("member").AtName(memberCriterionUsername),
			"Error resolving Project Member Roles member", err.Error())
		return
	}

	updated, err := r.client.UpdateProjectMember(ctx, r.projectID, member.Id, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Project Member Roles", err.Error())
		return
	}
	if updated.Id != member.Id {
		resp.Diagnostics.AddError("Error creating Project Member Roles",
			fmt.Sprintf("The API answered the role update of project member %s with project member %s.", member.Id, updated.Id))
		return
	}

	populateMemberRolesResourceState(updated, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MemberRolesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MemberRolesResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.GetProjectMember(ctx, r.projectID, memberID)
	if err != nil {
		if errors.Is(err, projectsdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Project Member Roles", err.Error())
		return
	}

	populateMemberRolesResourceState(member, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MemberRolesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MemberRolesResourceModel
	var state MemberRolesResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildMemberRolesRequest(ctx, plan.Roles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var member *projectsdk.ProjectMemberSchema
	var err error
	if current, diags := buildMemberRolesRequest(ctx, state.Roles); !diags.HasError() && sameMemberRoles(body, current) {
		member, err = r.client.GetProjectMember(ctx, r.projectID, memberID)
	} else {
		member, err = r.client.UpdateProjectMember(ctx, r.projectID, memberID, body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating Project Member Roles", err.Error())
		return
	}
	if member.Id != memberID {
		resp.Diagnostics.AddError("Error updating Project Member Roles",
			fmt.Sprintf("The API answered the role update of project member %s with project member %s.", memberID, member.Id))
		return
	}

	populateMemberRolesResourceState(member, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes every role of the member and keeps the member in the
// project. The member DELETE endpoint would remove the member itself.
func (r *MemberRolesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MemberRolesResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	memberID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdateProjectMember(ctx, r.projectID, memberID, projectsdk.ProjectMemberPartialUpdateSchema{
		Roles: &[]projectsdk.MemberRoleAssignmentSchema{},
	})
	if err != nil && !errors.Is(err, projectsdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting Project Member Roles", err.Error())
	}
}

func (r *MemberRolesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if _, diags := parse.UUIDString(types.StringValue(id), "import ID"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// validateMemberRoles rejects what the backend rejects for the whole set, so
// that plan reports it: more than maxMemberRoles elements, or a URN that
// appears twice. Unknown elements are checked again at apply by the backend.
func validateMemberRoles(roles types.Set) diag.Diagnostics {
	var diags diag.Diagnostics
	if roles.IsNull() || roles.IsUnknown() {
		return diags
	}

	elements := roles.Elements()
	if len(elements) > maxMemberRoles {
		diags.AddAttributeError(path.Root("roles"), "Too many Project Member Roles roles",
			fmt.Sprintf("roles holds %d elements; a member can hold at most %d roles.", len(elements), maxMemberRoles))
	}

	seen := make(map[string]bool, len(elements))
	for _, element := range elements {
		object, ok := element.(types.Object)
		if !ok || object.IsNull() || object.IsUnknown() {
			continue
		}
		urn := reference.Get(roleReference(object), roleCriterionURN)
		if urn.IsNull() || urn.IsUnknown() {
			continue
		}
		// The API trims the URN, so URNs that differ only by outer
		// whitespace name the same role.
		key := strings.TrimSpace(urn.ValueString())
		if seen[key] {
			diags.AddAttributeError(path.Root("roles"), "Duplicate Project Member Roles role",
				fmt.Sprintf("role.urn %q appears in more than one element of roles. Assign each role once and set its enabled flag there.", key))
			continue
		}
		seen[key] = true
	}
	return diags
}

// buildMemberRolesRequest sends the complete roles set with enabled on every
// element, because the backend replaces the whole set. The result is sorted
// by URN and never nil, so an empty set serializes as "roles": [].
func buildMemberRolesRequest(ctx context.Context, roles types.Set) (projectsdk.ProjectMemberPartialUpdateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if roles.IsNull() || roles.IsUnknown() {
		diags.AddAttributeError(path.Root("roles"), "Missing Project Member Roles roles", "roles must be configured and known.")
		return projectsdk.ProjectMemberPartialUpdateSchema{}, diags
	}

	var elements []memberRoleModel
	diags.Append(roles.ElementsAs(ctx, &elements, false)...)
	if diags.HasError() {
		return projectsdk.ProjectMemberPartialUpdateSchema{}, diags
	}

	assignments := make([]projectsdk.MemberRoleAssignmentSchema, 0, len(elements))
	for _, element := range elements {
		urn := reference.Get(element.Role, roleCriterionURN)
		if urn.IsNull() || urn.IsUnknown() || strings.TrimSpace(urn.ValueString()) == "" {
			diags.AddAttributeError(path.Root("roles"), "Missing Project Member Roles role", "Every roles element needs a known, non-blank role.urn.")
			continue
		}
		if element.Enabled.IsUnknown() {
			diags.AddAttributeError(path.Root("roles"), "Unknown Project Member Roles enabled flag",
				fmt.Sprintf("enabled of role %q must be known before apply.", urn.ValueString()))
			continue
		}
		// The schema default fills enabled; null still means the default.
		enabled := element.Enabled.IsNull() || element.Enabled.ValueBool()
		assignments = append(assignments, projectsdk.MemberRoleAssignmentSchema{Urn: strings.TrimSpace(urn.ValueString()), Enabled: &enabled})
	}
	if diags.HasError() {
		return projectsdk.ProjectMemberPartialUpdateSchema{}, diags
	}

	slices.SortFunc(assignments, func(a, b projectsdk.MemberRoleAssignmentSchema) int {
		return strings.Compare(a.Urn, b.Urn)
	})
	return projectsdk.ProjectMemberPartialUpdateSchema{Roles: &assignments}, diags
}

// sameMemberRoles reports whether two requests built by
// buildMemberRolesRequest assign the same roles. Both are sorted by trimmed
// URN, so a URN edit in outer whitespace only is not a change.
func sameMemberRoles(a, b projectsdk.ProjectMemberPartialUpdateSchema) bool {
	return slices.EqualFunc(*a.Roles, *b.Roles, func(x, y projectsdk.MemberRoleAssignmentSchema) bool {
		return x.Urn == y.Urn && *x.Enabled == *y.Enabled
	})
}

// roleReference returns the role reference of one roles element.
func roleReference(element types.Object) types.Object {
	role, ok := element.Attributes()["role"].(types.Object)
	if !ok {
		return types.ObjectNull(reference.AttributeTypes(roleCriterionURN))
	}
	return role
}
