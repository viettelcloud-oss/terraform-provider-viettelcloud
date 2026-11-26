package project

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

const (
	customRoleTestActionA = "urn:action:server:server:list"
	customRoleTestActionB = "urn:action:server:server:get"
)

var (
	customRoleTestID        = core.UUID{0x91}
	customRoleTestProjectID = core.UUID{0x92}
	customRoleTestRolesPath = "/v2/projects/" + customRoleTestProjectID.String() + "/custom-roles/"
	customRoleTestPath      = customRoleTestRolesPath + customRoleTestID.String() + "/"
)

// customRoleFixture returns the role that the backend creates from the
// display name "Đội vận hành" with two actions.
func customRoleFixture() *projectsdk.CustomRoleDetailSchema {
	return &projectsdk.CustomRoleDetailSchema{
		Id:          customRoleTestID,
		Name:        "doi-van-hanh",
		DisplayName: "Đội vận hành",
		Description: "",
		Enabled:     true,
		Urn:         "urn:role:prj-demo:doi-van-hanh",
		Actions: []projectsdk.NestedActionSchema{
			{Urn: customRoleTestActionA, DisplayName: "List servers", Description: "List servers."},
			{Urn: customRoleTestActionB, DisplayName: "Get server", Description: "Get a server."},
		},
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC),
	}
}

// customRoleTestClient returns a project client for handler, and fails the
// test when a request leaves the custom roles of the configured project.
func customRoleTestClient(t *testing.T, handler http.HandlerFunc) *projectsdk.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.URL.Path, customRoleTestRolesPath) {
			t.Errorf("%s %s: path is outside %s", req.Method, req.URL.Path, customRoleTestRolesPath)
		}
		handler(w, req)
	}))
	t.Cleanup(server.Close)
	client, err := projectsdk.NewClient(server.URL, projectsdk.WithHTTPClient(server.Client()), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return client
}

func writeCustomRoleJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func writeCustomRoleError(t *testing.T, w http.ResponseWriter, status int, messages ...string) {
	t.Helper()
	errs := make([]projectsdk.ErrorSchema, 0, len(messages))
	for _, message := range messages {
		errs = append(errs, projectsdk.ErrorSchema{Code: "error", Message: message})
	}
	writeCustomRoleJSON(t, w, status, projectsdk.ErrorResponseSchema{Errors: errs})
}

// sameCustomRoleModel compares two models attribute by attribute, because a
// types.Set makes the struct incomparable with ==.
func sameCustomRoleModel(a, b CustomRoleModel) bool {
	return a.ID.Equal(b.ID) && a.Name.Equal(b.Name) && a.DisplayName.Equal(b.DisplayName) &&
		a.Description.Equal(b.Description) && a.Enabled.Equal(b.Enabled) && a.Actions.Equal(b.Actions) &&
		a.URN.Equal(b.URN) && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// customRoleListItem returns role as a list row, which carries no actions.
func customRoleListItem(role *projectsdk.CustomRoleDetailSchema) projectsdk.CustomRoleSchema {
	return projectsdk.CustomRoleSchema{
		Id: role.Id, Name: role.Name, DisplayName: role.DisplayName, Description: role.Description,
		Enabled: role.Enabled, Urn: role.Urn, CreatedAt: role.CreatedAt, UpdatedAt: role.UpdatedAt,
	}
}

var (
	memberRolesTestProjectID = core.UUID{0x91}
	memberRolesTestMemberID  = core.UUID{0x92}
	memberRolesTestUserID    = core.UUID{0x93}
	memberRolesTestCreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	memberRolesTestUpdatedAt = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
)

// memberRolesTestCatalog holds the roles the fake backend can assign.
var memberRolesTestCatalog = map[string]projectsdk.NestedAssignedRoleSchema{
	"urn:role:viewer":            {Urn: "urn:role:viewer", DisplayName: "Viewer", Description: "Read-only access", Type: "predefined", Enabled: true},
	"urn:role:editor":            {Urn: "urn:role:editor", DisplayName: "Editor", Description: "Edit access", Type: "predefined", Enabled: true},
	"urn:role:prj-demo:disabled": {Urn: "urn:role:prj-demo:disabled", DisplayName: "Disabled", Description: "", Type: "custom", Enabled: false},
	"urn:role:billing":           {Urn: "urn:role:billing", DisplayName: "Billing", Description: "Billing access", Type: "predefined", Enabled: true},
}

type memberRolesRequest struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
}

// memberRolesBackend fakes the v2 member endpoints for one member. A PATCH
// replaces the whole role set and keeps the assignment ID of every role that
// stays, as the backend does.
type memberRolesBackend struct {
	t *testing.T

	mu       sync.Mutex
	member   projectsdk.ProjectMemberSchema
	others   []projectsdk.ProjectMemberSchema
	requests []memberRolesRequest
	nextID   byte

	// failMethod answers that method with failStatus and failBody.
	failMethod string
	failStatus int
	failBody   string
}

func newMemberRolesBackend(t *testing.T, roles ...projectsdk.NestedProjectUserRoleSchema) *memberRolesBackend {
	t.Helper()
	return &memberRolesBackend{
		t:      t,
		nextID: 0xa0,
		member: projectsdk.ProjectMemberSchema{
			Id:        memberRolesTestMemberID,
			User:      projectsdk.NestedMemberUserSchema{Id: memberRolesTestUserID, Username: "alice", Email: "alice@example.com"},
			IsAdmin:   false,
			Roles:     roles,
			CreatedAt: memberRolesTestCreatedAt,
			UpdatedAt: memberRolesTestUpdatedAt,
		},
	}
}

func (b *memberRolesBackend) client(t *testing.T) *projectsdk.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(server.Close)
	client, err := projectsdk.NewClient(server.URL, projectsdk.WithHTTPClient(server.Client()), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return client
}

func (b *memberRolesBackend) resource(t *testing.T) *MemberRolesResource {
	t.Helper()
	return &MemberRolesResource{client: b.client(t), projectID: memberRolesTestProjectID}
}

func (b *memberRolesBackend) serve(w http.ResponseWriter, req *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()

	body, err := io.ReadAll(req.Body)
	if err != nil {
		b.t.Errorf("read request body: %v", err)
	}
	b.requests = append(b.requests, memberRolesRequest{Method: req.Method, Path: req.URL.Path, Query: req.URL.Query(), Body: body})

	if req.Method == b.failMethod {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.failStatus)
		_, _ = io.WriteString(w, b.failBody)
		return
	}

	membersPath := "/v2/projects/" + memberRolesTestProjectID.String() + "/members/"
	memberPath := membersPath + memberRolesTestMemberID.String() + "/"
	switch {
	case req.Method == http.MethodGet && req.URL.Path == membersPath:
		username := strings.ToLower(req.URL.Query().Get("username"))
		var results []projectsdk.ProjectMemberSchema
		for _, member := range append([]projectsdk.ProjectMemberSchema{b.member}, b.others...) {
			if strings.Contains(strings.ToLower(member.User.Username), username) {
				results = append(results, member)
			}
		}
		b.write(w, projectsdk.PagedProjectMemberSchema{Count: len(results), Results: results})
	case req.Method == http.MethodGet && req.URL.Path == memberPath:
		b.write(w, b.member)
	case req.Method == http.MethodPatch && req.URL.Path == memberPath:
		var update projectsdk.ProjectMemberPartialUpdateSchema
		if err := json.Unmarshal(body, &update); err != nil {
			b.t.Errorf("decode PATCH body: %v", err)
		}
		if update.Roles != nil {
			b.member.Roles = b.apply(*update.Roles)
		}
		b.write(w, b.member)
	default:
		b.t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (b *memberRolesBackend) apply(assignments []projectsdk.MemberRoleAssignmentSchema) []projectsdk.NestedProjectUserRoleSchema {
	existing := make(map[string]core.UUID, len(b.member.Roles))
	for _, role := range b.member.Roles {
		existing[role.Role.Urn] = role.Id
	}
	roles := make([]projectsdk.NestedProjectUserRoleSchema, 0, len(assignments))
	for _, assignment := range assignments {
		id, ok := existing[assignment.Urn]
		if !ok {
			b.nextID++
			id = core.UUID{b.nextID}
		}
		enabled := assignment.Enabled == nil || *assignment.Enabled
		roles = append(roles, projectsdk.NestedProjectUserRoleSchema{Id: id, Role: memberRolesTestCatalog[assignment.Urn], Enabled: enabled})
	}
	return roles
}

func (b *memberRolesBackend) write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		b.t.Errorf("encode response: %v", err)
	}
}

func (b *memberRolesBackend) recorded() []memberRolesRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]memberRolesRequest(nil), b.requests...)
}

func (b *memberRolesBackend) methods() string {
	var methods []string
	for _, request := range b.recorded() {
		methods = append(methods, request.Method)
	}
	return strings.Join(methods, ",")
}

// memberRolesAssignment returns an assignment of a catalog role.
func memberRolesAssignment(id byte, urn string, enabled bool) projectsdk.NestedProjectUserRoleSchema {
	return projectsdk.NestedProjectUserRoleSchema{Id: core.UUID{id}, Role: memberRolesTestCatalog[urn], Enabled: enabled}
}
