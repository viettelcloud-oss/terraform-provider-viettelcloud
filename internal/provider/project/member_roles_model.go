package project

import (
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// Criterion names of the member and role references.
const (
	memberCriterionUsername = "username"
	roleCriterionURN        = "urn"
)

// maxMemberRoles is the backend limit on the roles of one member.
const maxMemberRoles = 100

type MemberRolesModel struct {
	ID            types.String `tfsdk:"id"`
	MemberUserID  types.String `tfsdk:"member_user_id"`
	MemberEmail   types.String `tfsdk:"member_email"`
	MemberIsAdmin types.Bool   `tfsdk:"member_is_admin"`
	Assignments   types.List   `tfsdk:"assignments"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

type MemberRolesResourceModel struct {
	MemberRolesModel
	Member types.Object `tfsdk:"member"`
	Roles  types.Set    `tfsdk:"roles"`
}

type MemberRolesDataSourceModel struct {
	MemberRolesModel
	Username types.String `tfsdk:"username"`
	Roles    types.Set    `tfsdk:"roles"`
}

// memberRoleModel is one element of the resource roles set.
type memberRoleModel struct {
	Role    types.Object `tfsdk:"role"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func memberRoleAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"role":    reference.ObjectType(roleCriterionURN),
		"enabled": types.BoolType,
	}
}

func memberRoleDataSourceAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"role_urn": types.StringType,
		"enabled":  types.BoolType,
	}
}

func memberRoleAssignmentAttributeTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":           types.StringType,
		"urn":          types.StringType,
		"enabled":      types.BoolType,
		"display_name": types.StringType,
		"description":  types.StringType,
		"type":         types.StringType,
		"role_enabled": types.BoolType,
	}
}

func populateMemberRolesModel(member *projectsdk.ProjectMemberSchema, m *MemberRolesModel) {
	if member == nil {
		return
	}

	m.ID = types.StringValue(member.Id.String())
	m.MemberUserID = types.StringValue(member.User.Id.String())
	m.MemberEmail = types.StringValue(member.User.Email)
	m.MemberIsAdmin = types.BoolValue(member.IsAdmin)
	m.Assignments = memberRoleAssignments(member.Roles)
	m.CreatedAt = types.StringValue(member.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(member.UpdatedAt.Format(time.RFC3339))
}

func populateMemberRolesResourceState(member *projectsdk.ProjectMemberSchema, state *MemberRolesResourceModel) {
	if member == nil {
		return
	}

	configuredUsername := reference.Get(state.Member, memberCriterionUsername)
	configuredURNs := roleURNs(state.Roles)
	populateMemberRolesModel(member, &state.MemberRolesModel)
	// The finder trims member.username before the lookup, so keep the
	// configured form when it names the same user.
	state.Member = reference.Value(memberCriterionUsername, compare.PreservedString(configuredUsername, member.User.Username))
	state.Roles = memberRoles(member.Roles, configuredURNs)
}

func populateMemberRolesDataSourceState(member *projectsdk.ProjectMemberSchema, state *MemberRolesDataSourceModel) {
	if member == nil {
		return
	}

	populateMemberRolesModel(member, &state.MemberRolesModel)
	state.Username = types.StringValue(member.User.Username)
	state.Roles = memberDataSourceRoles(member.Roles)
}

// memberRoles maps the assignments to the resource roles set, so that a role
// added or removed outside Terraform shows as drift. The request trims each
// URN, so a configured URN keeps its spelling when it names the same role.
func memberRoles(assignments []projectsdk.NestedProjectUserRoleSchema, configuredURNs []types.String) types.Set {
	elements := make([]attr.Value, 0, len(assignments))
	for _, assignment := range assignments {
		urn := types.StringValue(assignment.Role.Urn)
		for _, configured := range configuredURNs {
			if compare.SameTrimmed(configured, assignment.Role.Urn) {
				urn = configured
				break
			}
		}
		elements = append(elements, types.ObjectValueMust(memberRoleAttributeTypes(), map[string]attr.Value{
			"role":    reference.Value(roleCriterionURN, urn),
			"enabled": types.BoolValue(assignment.Enabled),
		}))
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: memberRoleAttributeTypes()}, elements)
}

// roleURNs returns the role.urn of every element of a roles set, or nil for a
// null or unknown set.
func roleURNs(roles types.Set) []types.String {
	if roles.IsNull() || roles.IsUnknown() {
		return nil
	}
	urns := make([]types.String, 0, len(roles.Elements()))
	for _, element := range roles.Elements() {
		if object, ok := element.(types.Object); ok && !object.IsNull() && !object.IsUnknown() {
			urns = append(urns, reference.Get(roleReference(object), roleCriterionURN))
		}
	}
	return urns
}

func memberDataSourceRoles(assignments []projectsdk.NestedProjectUserRoleSchema) types.Set {
	elements := make([]attr.Value, 0, len(assignments))
	for _, assignment := range assignments {
		elements = append(elements, types.ObjectValueMust(memberRoleDataSourceAttributeTypes(), map[string]attr.Value{
			"role_urn": types.StringValue(assignment.Role.Urn),
			"enabled":  types.BoolValue(assignment.Enabled),
		}))
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: memberRoleDataSourceAttributeTypes()}, elements)
}

// memberRoleAssignments sorts by URN, because the backend does not promise an
// order and a reordered list would show as a change.
func memberRoleAssignments(assignments []projectsdk.NestedProjectUserRoleSchema) types.List {
	sorted := slices.Clone(assignments)
	slices.SortFunc(sorted, func(a, b projectsdk.NestedProjectUserRoleSchema) int {
		return strings.Compare(a.Role.Urn, b.Role.Urn)
	})
	elements := make([]attr.Value, 0, len(sorted))
	for _, assignment := range sorted {
		elements = append(elements, types.ObjectValueMust(memberRoleAssignmentAttributeTypes(), map[string]attr.Value{
			"id":           types.StringValue(assignment.Id.String()),
			"urn":          types.StringValue(assignment.Role.Urn),
			"enabled":      types.BoolValue(assignment.Enabled),
			"display_name": types.StringValue(assignment.Role.DisplayName),
			"description":  types.StringValue(assignment.Role.Description),
			"type":         types.StringValue(assignment.Role.Type),
			"role_enabled": types.BoolValue(assignment.Role.Enabled),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: memberRoleAssignmentAttributeTypes()}, elements)
}
