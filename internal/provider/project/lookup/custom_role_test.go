package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

type fakeCustomRoleLister struct {
	results   []projectsdk.CustomRoleSchema
	err       error
	projectID core.UUID
	params    projectsdk.ListCustomRolesParams
}

func (f *fakeCustomRoleLister) ListCustomRolesIter(
	_ context.Context,
	projectID core.UUID,
	params projectsdk.ListCustomRolesParams,
) iter.Seq2[*projectsdk.CustomRoleSchema, error] {
	f.projectID = projectID
	f.params = params
	return func(yield func(*projectsdk.CustomRoleSchema, error) bool) {
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

func sampleCustomRole(id core.UUID, name string) projectsdk.CustomRoleSchema {
	return projectsdk.CustomRoleSchema{
		Id:          id,
		Name:        name,
		DisplayName: "Role " + name,
		Urn:         "urn:role:prj-demo:" + name,
		Enabled:     true,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCustomRoleFinderKeepsExactNameAmongPartialMatches(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{7}
	lister := &fakeCustomRoleLister{results: []projectsdk.CustomRoleSchema{
		sampleCustomRole(core.UUID{1}, "ops-admin"),
		sampleCustomRole(core.UUID{2}, "ops"),
		sampleCustomRole(core.UUID{3}, "dev-ops"),
	}}
	role, err := NewCustomRoleFinder(lister, projectID).Resolve(context.Background(), CustomRoleFilter{Name: new("ops")})
	if err != nil {
		t.Fatalf("resolve custom role: %v", err)
	}
	if role.Id != (core.UUID{2}) {
		t.Fatalf("resolved %s, want the role named ops", role.Id)
	}
	if lister.projectID != projectID || lister.params.Name == nil || *lister.params.Name != "ops" {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestCustomRoleFinderMatchesNameCaseSensitively(t *testing.T) {
	t.Parallel()

	lister := &fakeCustomRoleLister{results: []projectsdk.CustomRoleSchema{
		sampleCustomRole(core.UUID{1}, "OPS"),
	}}
	matches, err := NewCustomRoleFinder(lister, core.UUID{7}).Find(context.Background(), CustomRoleFilter{Name: new("ops")})
	if err != nil {
		t.Fatalf("find custom roles: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no case-insensitive match, got %#v", matches)
	}
}

func TestCustomRoleFinderTrimsName(t *testing.T) {
	t.Parallel()

	lister := &fakeCustomRoleLister{results: []projectsdk.CustomRoleSchema{
		sampleCustomRole(core.UUID{1}, "ops"),
	}}
	matches, err := NewCustomRoleFinder(lister, core.UUID{7}).Find(context.Background(), CustomRoleFilter{Name: new(" ops ")})
	if err != nil {
		t.Fatalf("find custom roles: %v", err)
	}
	if len(matches) != 1 || lister.params.Name == nil || *lister.params.Name != "ops" {
		t.Fatalf("expected the trimmed name in request and match, got matches=%#v params=%#v", matches, lister.params)
	}
}

func TestCustomRoleFinderTreatsBlankNameAsUnset(t *testing.T) {
	t.Parallel()

	lister := &fakeCustomRoleLister{results: []projectsdk.CustomRoleSchema{
		sampleCustomRole(core.UUID{1}, "ops"),
		sampleCustomRole(core.UUID{2}, "dev"),
	}}
	filter := CustomRoleFilter{Name: new("   ")}
	matches, err := NewCustomRoleFinder(lister, core.UUID{7}).Find(context.Background(), filter)
	if err != nil {
		t.Fatalf("find custom roles: %v", err)
	}
	if len(matches) != 2 || lister.params.Name != nil {
		t.Fatalf("expected an unfiltered list, got matches=%d params=%#v", len(matches), lister.params)
	}
	if filter.String() != "all" {
		t.Fatalf("filter description = %q, want all", filter.String())
	}
}

func TestCustomRoleFinderResolveCardinality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []projectsdk.CustomRoleSchema
		wantErr string
	}{
		{"zero", []projectsdk.CustomRoleSchema{sampleCustomRole(core.UUID{1}, "ops-admin")}, `custom role was not found matching criteria (name="ops")`},
		{"one", []projectsdk.CustomRoleSchema{sampleCustomRole(core.UUID{1}, "ops")}, ""},
		{
			"multiple",
			[]projectsdk.CustomRoleSchema{sampleCustomRole(core.UUID{1}, "ops"), sampleCustomRole(core.UUID{2}, "ops")},
			`custom role matching criteria (name="ops") is ambiguous; multiple candidates matched: ` +
				`id=01000000-0000-0000-0000-000000000000 (name="ops", display_name="Role ops"), ` +
				`id=02000000-0000-0000-0000-000000000000 (name="ops", display_name="Role ops")`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lister := &fakeCustomRoleLister{results: tt.results}
			role, err := NewCustomRoleFinder(lister, core.UUID{7}).Resolve(context.Background(), CustomRoleFilter{Name: new("ops")})
			if tt.wantErr == "" {
				if err != nil || role.Name != "ops" {
					t.Fatalf("Resolve() = %#v, %v; want the role named ops", role, err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Resolve() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCustomRoleFinderReturnsListError(t *testing.T) {
	t.Parallel()

	listErr := errors.New("backend unavailable")
	lister := &fakeCustomRoleLister{err: listErr}
	_, err := NewCustomRoleFinder(lister, core.UUID{7}).Resolve(context.Background(), CustomRoleFilter{Name: new("ops")})
	if !errors.Is(err, listErr) {
		t.Fatalf("expected the wrapped list error, got %v", err)
	}
	if !strings.Contains(err.Error(), `list custom roles for criteria (name="ops")`) {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestCustomRoleFinderReadsEveryPageOfThePublicSDK(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{7}
	var (
		mu    sync.Mutex
		pages []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/projects/"+projectID.String()+"/custom-roles/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if got := req.URL.Query().Get("name"); got != "ops" {
			t.Errorf("name query = %q, want ops", got)
		}
		page := req.URL.Query().Get("page_number")
		mu.Lock()
		pages = append(pages, page)
		mu.Unlock()
		result := projectsdk.PagedCustomRoleSchema{Count: 2}
		if page == "1" {
			result.Results = []projectsdk.CustomRoleSchema{sampleCustomRole(core.UUID{1}, "ops-admin")}
		} else {
			result.Results = []projectsdk.CustomRoleSchema{sampleCustomRole(core.UUID{2}, "ops")}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()

	client, err := projectsdk.NewClient(server.URL, projectsdk.WithHTTPClient(server.Client()), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	role, err := NewCustomRoleFinder(client, projectID).Resolve(context.Background(), CustomRoleFilter{Name: new("ops")})
	if err != nil {
		t.Fatalf("resolve custom role: %v", err)
	}
	if role.Id != (core.UUID{2}) {
		t.Fatalf("resolved %s, want the role on the second page", role.Id)
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("requested pages %v, want [1 2]", pages)
	}
}
