package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

type ProjectMemberLister interface {
	ListProjectMembersIter(
		context.Context,
		core.UUID,
		projectsdk.ListProjectMembersParams,
	) iter.Seq2[*projectsdk.ProjectMemberSchema, error]
}

type ProjectMemberFilter struct {
	Username *string
}

func (f ProjectMemberFilter) String() string {
	return finder.Criteria(finder.Text("username", f.Username))
}

// matches compares username exactly and case-sensitively, because the backend
// username filter matches any username that contains the value, ignoring case.
func (f ProjectMemberFilter) matches(member *projectsdk.ProjectMemberSchema) bool {
	username := finder.Trim(f.Username)
	return username == "" || member.User.Username == username
}

func (f ProjectMemberFilter) params() projectsdk.ListProjectMembersParams {
	params := projectsdk.ListProjectMembersParams{}
	if username := finder.Trim(f.Username); username != "" {
		params.Username = &username
	}
	return params
}

type ProjectMemberFinder struct {
	client    ProjectMemberLister
	projectID core.UUID
}

var _ finder.Interface[ProjectMemberFilter, projectsdk.ProjectMemberSchema] = (*ProjectMemberFinder)(nil)

func NewProjectMemberFinder(client ProjectMemberLister, projectID core.UUID) *ProjectMemberFinder {
	return &ProjectMemberFinder{client: client, projectID: projectID}
}

func (f *ProjectMemberFinder) Find(ctx context.Context, filter ProjectMemberFilter) ([]projectsdk.ProjectMemberSchema, error) {
	seq := f.client.ListProjectMembersIter(ctx, f.projectID, filter.params())
	return finder.Collect(seq, "project member", filter.String(), filter.matches)
}

func (f *ProjectMemberFinder) Resolve(ctx context.Context, filter ProjectMemberFilter) (projectsdk.ProjectMemberSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return projectsdk.ProjectMemberSchema{}, err
	}
	return finder.ExactlyOne(candidates, "project member", filter.String(), projectMemberSummary)
}

func projectMemberSummary(member projectsdk.ProjectMemberSchema) string {
	return fmt.Sprintf("id=%s (username=%q)", member.Id, member.User.Username)
}
