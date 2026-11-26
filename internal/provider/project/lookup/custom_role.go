package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

type CustomRoleLister interface {
	ListCustomRolesIter(context.Context, core.UUID, projectsdk.ListCustomRolesParams) iter.Seq2[*projectsdk.CustomRoleSchema, error]
}

type CustomRoleFilter struct {
	Name *string
}

func (f CustomRoleFilter) String() string {
	return finder.Criteria(finder.Text("name", f.Name))
}

// matches compares name exactly and case-sensitively, because the backend
// name filter matches any role whose name contains the value, ignoring case.
func (f CustomRoleFilter) matches(role *projectsdk.CustomRoleSchema) bool {
	name := finder.Trim(f.Name)
	return name == "" || role.Name == name
}

func (f CustomRoleFilter) params() projectsdk.ListCustomRolesParams {
	var params projectsdk.ListCustomRolesParams
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type CustomRoleFinder struct {
	client    CustomRoleLister
	projectID core.UUID
}

var _ finder.Interface[CustomRoleFilter, projectsdk.CustomRoleSchema] = (*CustomRoleFinder)(nil)

func NewCustomRoleFinder(client CustomRoleLister, projectID core.UUID) *CustomRoleFinder {
	return &CustomRoleFinder{client: client, projectID: projectID}
}

func (f *CustomRoleFinder) Find(ctx context.Context, filter CustomRoleFilter) ([]projectsdk.CustomRoleSchema, error) {
	seq := f.client.ListCustomRolesIter(ctx, f.projectID, filter.params())
	return finder.Collect(seq, "custom role", filter.String(), filter.matches)
}

func (f *CustomRoleFinder) Resolve(ctx context.Context, filter CustomRoleFilter) (projectsdk.CustomRoleSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return projectsdk.CustomRoleSchema{}, err
	}
	return finder.ExactlyOne(candidates, "custom role", filter.String(), customRoleSummary)
}

func customRoleSummary(role projectsdk.CustomRoleSchema) string {
	return fmt.Sprintf("id=%s (name=%q, display_name=%q)", role.Id, role.Name, role.DisplayName)
}
