package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

type fakeProjectMemberLister struct {
	results   []projectsdk.ProjectMemberSchema
	err       error
	projectID core.UUID
	params    projectsdk.ListProjectMembersParams
}

func (f *fakeProjectMemberLister) ListProjectMembersIter(
	_ context.Context,
	projectID core.UUID,
	params projectsdk.ListProjectMembersParams,
) iter.Seq2[*projectsdk.ProjectMemberSchema, error] {
	f.projectID = projectID
	f.params = params
	return func(yield func(*projectsdk.ProjectMemberSchema, error) bool) {
		for i := range f.results {
			if !yield(&f.results[i], nil) {
				return
			}
		}
		if f.err != nil {
			yield(nil, f.err)
		}
	}
}

func TestProjectMemberFinderReturnsOneExactCandidate(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{8}
	lister := &fakeProjectMemberLister{results: []projectsdk.ProjectMemberSchema{
		projectMember(core.UUID{1}, "alice"),
	}}
	matches, err := NewProjectMemberFinder(lister, projectID).
		Find(context.Background(), ProjectMemberFilter{Username: new("alice")})
	if err != nil {
		t.Fatalf("find project members: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{1}) {
		t.Fatalf("unexpected project member matches: %#v", matches)
	}
	if lister.projectID != projectID || lister.params.Username == nil || *lister.params.Username != "alice" {
		t.Fatalf("unexpected list request: project=%s params=%#v", lister.projectID, lister.params)
	}
	if lister.params.Email != nil || lister.params.UserId != nil || lister.params.IsAdmin != nil {
		t.Fatalf("expected only the username filter, got %#v", lister.params)
	}
}

// The backend username filter is icontains, so partial and case-variant
// usernames come back and must be dropped.
func TestProjectMemberFinderDropsPartialAndCaseVariantMatches(t *testing.T) {
	t.Parallel()

	lister := &fakeProjectMemberLister{results: []projectsdk.ProjectMemberSchema{
		projectMember(core.UUID{1}, "alice2"),
		projectMember(core.UUID{2}, "Alice"),
		projectMember(core.UUID{3}, "malice"),
		projectMember(core.UUID{4}, "alice"),
	}}
	matches, err := NewProjectMemberFinder(lister, core.UUID{8}).
		Find(context.Background(), ProjectMemberFilter{Username: new("alice")})
	if err != nil {
		t.Fatalf("find project members: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{4}) {
		t.Fatalf("expected only the exact username, got %#v", matches)
	}
}

func TestProjectMemberFinderTrimsUsername(t *testing.T) {
	t.Parallel()

	lister := &fakeProjectMemberLister{results: []projectsdk.ProjectMemberSchema{
		projectMember(core.UUID{1}, "alice"),
	}}
	matches, err := NewProjectMemberFinder(lister, core.UUID{8}).
		Find(context.Background(), ProjectMemberFilter{Username: new(" alice ")})
	if err != nil {
		t.Fatalf("find project members: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected the padded username to match, got %#v", matches)
	}
	if lister.params.Username == nil || *lister.params.Username != "alice" {
		t.Fatalf("expected trimmed lookup query, got %#v", lister.params.Username)
	}
}

func TestProjectMemberFinderBlankUsernameIsUnset(t *testing.T) {
	t.Parallel()

	lister := &fakeProjectMemberLister{results: []projectsdk.ProjectMemberSchema{
		projectMember(core.UUID{1}, "alice"),
		projectMember(core.UUID{2}, "bob"),
	}}
	matches, err := NewProjectMemberFinder(lister, core.UUID{8}).
		Find(context.Background(), ProjectMemberFilter{Username: new("  ")})
	if err != nil {
		t.Fatalf("find project members: %v", err)
	}
	if len(matches) != 2 || lister.params.Username != nil {
		t.Fatalf("expected a blank username to be unset, matches=%#v params=%#v", matches, lister.params)
	}
}

func TestProjectMemberFinderReturnsListErrors(t *testing.T) {
	t.Parallel()

	listErr := errors.New("request failed")
	matches, err := NewProjectMemberFinder(&fakeProjectMemberLister{err: listErr}, core.UUID{8}).
		Find(context.Background(), ProjectMemberFilter{Username: new("alice")})
	if !errors.Is(err, listErr) || matches != nil {
		t.Fatalf("expected list error, matches=%#v error=%v", matches, err)
	}
	if want := `list project members for criteria (username="alice")`; !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v", want, err)
	}
}

func TestProjectMemberFinderResolveRequiresUniqueCandidate(t *testing.T) {
	t.Parallel()

	member := projectMember(core.UUID{1}, "alice")
	resolved, err := NewProjectMemberFinder(
		&fakeProjectMemberLister{results: []projectsdk.ProjectMemberSchema{member}},
		core.UUID{8},
	).Resolve(context.Background(), ProjectMemberFilter{Username: new("alice")})
	if err != nil || resolved.Id != member.Id {
		t.Fatalf("unexpected resolution: member=%#v error=%v", resolved, err)
	}

	for name, tt := range map[string]struct {
		candidates []projectsdk.ProjectMemberSchema
		want       string
	}{
		"missing": {
			want: `project member was not found matching criteria (username="alice")`,
		},
		"partial match only": {
			candidates: []projectsdk.ProjectMemberSchema{projectMember(core.UUID{1}, "alice2")},
			want:       `project member was not found matching criteria (username="alice")`,
		},
		"ambiguous": {
			candidates: []projectsdk.ProjectMemberSchema{
				projectMember(core.UUID{1}, "alice"),
				projectMember(core.UUID{2}, "alice"),
			},
			want: `project member matching criteria (username="alice") is ambiguous`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewProjectMemberFinder(&fakeProjectMemberLister{results: tt.candidates}, core.UUID{8}).
				Resolve(context.Background(), ProjectMemberFilter{Username: new("alice")})
			if err == nil {
				t.Fatal("expected selection error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

// The SDK iterator requests every page, so an exact match on a later page is
// still found.
func TestProjectMemberFinderReadsEveryPage(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{8}
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/projects/"+projectID.String()+"/members/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if got := req.URL.Query().Get("username"); got != "alice" {
			t.Errorf("username filter = %q, want alice", got)
		}
		page := req.URL.Query().Get("page_number")
		pages = append(pages, page)
		result := projectMember(core.UUID{1}, "alice2")
		if page == "2" {
			result = projectMember(core.UUID{2}, "alice")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(projectsdk.PagedProjectMemberSchema{
			Count:   2,
			Results: []projectsdk.ProjectMemberSchema{result},
		})
	}))
	defer server.Close()

	client, err := projectsdk.NewClient(server.URL, projectsdk.WithHTTPClient(server.Client()), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	resolved, err := NewProjectMemberFinder(client, projectID).
		Resolve(context.Background(), ProjectMemberFilter{Username: new("alice")})
	if err != nil {
		t.Fatalf("resolve project member: %v", err)
	}
	if resolved.Id != (core.UUID{2}) {
		t.Fatalf("expected the exact match from page 2, got %#v", resolved)
	}
	if strings.Join(pages, ",") != "1,2" {
		t.Fatalf("expected pages 1 and 2, got %v", pages)
	}
}

func TestProjectMemberFilterString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		filter   ProjectMemberFilter
		expected string
	}{
		"unset":    {filter: ProjectMemberFilter{}, expected: "all"},
		"blank":    {filter: ProjectMemberFilter{Username: new("  ")}, expected: "all"},
		"username": {filter: ProjectMemberFilter{Username: new("alice")}, expected: `username="alice"`},
		"trimmed":  {filter: ProjectMemberFilter{Username: new("  alice  ")}, expected: `username="alice"`},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.String(); got != tt.expected {
				t.Errorf("ProjectMemberFilter.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func projectMember(id core.UUID, username string) projectsdk.ProjectMemberSchema {
	return projectsdk.ProjectMemberSchema{
		Id:   id,
		User: projectsdk.NestedMemberUserSchema{Id: core.UUID{0xee, id[0]}, Username: username},
	}
}
