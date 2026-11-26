package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

type fakeServerGroupLister struct {
	results   []loadbalancersdk.ServerGroupSchema
	err       error
	lbID      core.UUID
	params    loadbalancersdk.ListServerGroupsParams
	callCount int
}

func (f *fakeServerGroupLister) ListServerGroupsIter(
	_ context.Context,
	lbID core.UUID,
	params loadbalancersdk.ListServerGroupsParams,
) iter.Seq2[*loadbalancersdk.ServerGroupSchema, error] {
	f.callCount++
	f.lbID = lbID
	f.params = params
	return func(yield func(*loadbalancersdk.ServerGroupSchema, error) bool) {
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

func sampleServerGroup(id core.UUID, name string, protocol loadbalancersdk.ServerGroupProtocolType) loadbalancersdk.ServerGroupSchema {
	return loadbalancersdk.ServerGroupSchema{
		Id:                 id,
		Name:               name,
		Protocol:           protocol,
		Algorithm:          loadbalancersdk.ServerGroupAlgorithmROUNDROBIN,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		MemberCount:        2,
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestServerGroupFinderFindByName(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{8}
	groups := []loadbalancersdk.ServerGroupSchema{
		sampleServerGroup(core.UUID{10}, "group-a", loadbalancersdk.ServerGroupProtocolTypeHTTP),
		sampleServerGroup(core.UUID{11}, "group-b", loadbalancersdk.ServerGroupProtocolTypeTCP),
		sampleServerGroup(core.UUID{12}, "group-a", loadbalancersdk.ServerGroupProtocolTypeHTTPS),
	}
	lister := &fakeServerGroupLister{results: groups}
	finder := NewServerGroupFinder(lister, projectID, lbID)

	name := "group-a"
	matches, err := finder.Find(context.Background(), ServerGroupFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 || matches[0].Id != (core.UUID{10}) || matches[1].Id != (core.UUID{12}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.lbID != lbID || lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "group-a" {
		t.Fatalf("unexpected list params: %#v, lbID=%s", lister.params, lister.lbID)
	}
}

func TestServerGroupFinderFindByID(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{8}
	targetID := core.UUID{11}
	groups := []loadbalancersdk.ServerGroupSchema{
		sampleServerGroup(core.UUID{10}, "group-a", loadbalancersdk.ServerGroupProtocolTypeHTTP),
		sampleServerGroup(targetID, "group-b", loadbalancersdk.ServerGroupProtocolTypeTCP),
	}
	lister := &fakeServerGroupLister{results: groups}
	finder := NewServerGroupFinder(lister, projectID, lbID)

	matches, err := finder.Find(context.Background(), ServerGroupFilter{ID: &targetID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != targetID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestServerGroupFinderReturnsListError(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	finder := NewServerGroupFinder(&fakeServerGroupLister{err: apiErr}, core.UUID{9}, core.UUID{8})
	_, err := finder.Find(context.Background(), ServerGroupFilter{})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected apiErr, got %v", err)
	}
}

func TestServerGroupFinderResolveSuccess(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{8}
	group := sampleServerGroup(core.UUID{10}, "unique-group", loadbalancersdk.ServerGroupProtocolTypeHTTP)
	finder := NewServerGroupFinder(&fakeServerGroupLister{results: []loadbalancersdk.ServerGroupSchema{group}}, projectID, lbID)

	name := "unique-group"
	res, err := finder.Resolve(context.Background(), ServerGroupFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Id != group.Id {
		t.Fatalf("expected resolved server group %s, got %s", group.Id, res.Id)
	}
}

func TestServerGroupFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	finder := NewServerGroupFinder(&fakeServerGroupLister{results: nil}, core.UUID{9}, core.UUID{8})
	name := "non-existent"
	_, err := finder.Resolve(context.Background(), ServerGroupFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when no server group matches")
	}
	if !strings.Contains(err.Error(), `server group was not found matching criteria (name="non-existent")`) {
		t.Fatalf("expected not-found error naming the criteria, got %v", err)
	}
}

func TestServerGroupFinderResolveMultipleMatches(t *testing.T) {
	t.Parallel()

	groups := []loadbalancersdk.ServerGroupSchema{
		sampleServerGroup(core.UUID{10}, "dup-group", loadbalancersdk.ServerGroupProtocolTypeHTTP),
		sampleServerGroup(core.UUID{11}, "dup-group", loadbalancersdk.ServerGroupProtocolTypeTCP),
	}
	finder := NewServerGroupFinder(&fakeServerGroupLister{results: groups}, core.UUID{9}, core.UUID{8})
	name := "dup-group"
	_, err := finder.Resolve(context.Background(), ServerGroupFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when multiple server groups match")
	}
	for _, want := range []string{
		"is ambiguous",
		`id=` + groups[0].Id.String(),
		`id=` + groups[1].Id.String(),
		`protocol="HTTP"`,
		`protocol="TCP"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected ambiguity error to contain %q, got %v", want, err)
		}
	}
}

func TestServerGroupFinderTrimsNameFilter(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{8}
	lister := &fakeServerGroupLister{results: []loadbalancersdk.ServerGroupSchema{
		sampleServerGroup(core.UUID{10}, "group-a", loadbalancersdk.ServerGroupProtocolTypeHTTP),
	}}

	name := "  group-a  "
	matches, err := NewServerGroupFinder(lister, projectID, lbID).Find(context.Background(), ServerGroupFilter{
		Name: &name,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("expected padded filter to match exact server group, got %#v", matches)
	}
	if lister.params.Name == nil || *lister.params.Name != "group-a" {
		t.Fatalf("expected trimmed name in list params, got %#v", lister.params.Name)
	}
}

func TestServerGroupFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{10}
	name := "my-group"
	padded := "  my-group  "
	blank := "   "

	tests := []struct {
		name     string
		filter   ServerGroupFilter
		expected string
	}{
		{
			name:     "empty filter",
			filter:   ServerGroupFilter{},
			expected: "all",
		},
		{
			name: "name filter",
			filter: ServerGroupFilter{
				Name: &name,
			},
			expected: `name="my-group"`,
		},
		{
			name: "id and name filter",
			filter: ServerGroupFilter{
				ID:   &id,
				Name: &name,
			},
			expected: `id=0a000000-0000-0000-0000-000000000000, name="my-group"`,
		},
		{
			name: "padded name filter",
			filter: ServerGroupFilter{
				Name: &padded,
			},
			expected: `name="my-group"`,
		},
		{
			name: "blank name filter",
			filter: ServerGroupFilter{
				Name: &blank,
			},
			expected: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.String(); got != tt.expected {
				t.Errorf("ServerGroupFilter.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestServerGroupFinderExcludesDeleted(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{1}
	lbID := core.UUID{2}
	deletedID := core.UUID{3}
	activeID := core.UUID{4}

	delGroup := sampleServerGroup(deletedID, "deleted-group", loadbalancersdk.ServerGroupProtocolTypeHTTP)
	delGroup.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	actGroup := sampleServerGroup(activeID, "active-group", loadbalancersdk.ServerGroupProtocolTypeHTTP)
	actGroup.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE

	lister := &fakeServerGroupLister{
		results: []loadbalancersdk.ServerGroupSchema{delGroup, actGroup},
	}

	finder := NewServerGroupFinder(lister, projectID, lbID)
	name := "deleted-group"
	results, err := finder.Find(context.Background(), ServerGroupFilter{Name: &name})
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for deleted group, got %d", len(results))
	}
}
