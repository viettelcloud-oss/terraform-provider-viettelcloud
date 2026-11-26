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

type fakeServerGroupMemberLister struct {
	results       []loadbalancersdk.ServerGroupMemberSchema
	err           error
	serverGroupID core.UUID
	params        loadbalancersdk.ListServerGroupMembersParams
	callCount     int
}

func (f *fakeServerGroupMemberLister) ListServerGroupMembersIter(
	_ context.Context,
	serverGroupID core.UUID,
	params loadbalancersdk.ListServerGroupMembersParams,
) iter.Seq2[*loadbalancersdk.ServerGroupMemberSchema, error] {
	f.callCount++
	f.serverGroupID = serverGroupID
	f.params = params
	return func(yield func(*loadbalancersdk.ServerGroupMemberSchema, error) bool) {
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

func sampleServerGroupMember(id core.UUID, name string, address string, port int, provStatus loadbalancersdk.LoadBalancingProvisioningStatus) loadbalancersdk.ServerGroupMemberSchema {
	return loadbalancersdk.ServerGroupMemberSchema{
		Id:                 id,
		Name:               name,
		Address:            address,
		ProtocolPort:       port,
		Weight:             1,
		Backup:             false,
		ProvisioningStatus: provStatus,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestServerGroupMemberFinderFindByName(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	members := []loadbalancersdk.ServerGroupMemberSchema{
		sampleServerGroupMember(core.UUID{10}, "member-a", "10.0.0.1", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleServerGroupMember(core.UUID{11}, "member-b", "10.0.0.2", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleServerGroupMember(core.UUID{12}, "member-a", "10.0.0.3", 8080, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	lister := &fakeServerGroupMemberLister{results: members}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	name := "member-a"
	matches, err := finder.Find(context.Background(), ServerGroupMemberFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 || matches[0].Id != (core.UUID{10}) || matches[1].Id != (core.UUID{12}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.serverGroupID != serverGroupID || lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "member-a" {
		t.Fatalf("unexpected list params: %#v, serverGroupID=%s", lister.params, lister.serverGroupID)
	}
}

func TestServerGroupMemberFinderFindByID(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	targetID := core.UUID{11}
	members := []loadbalancersdk.ServerGroupMemberSchema{
		sampleServerGroupMember(core.UUID{10}, "member-a", "10.0.0.1", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleServerGroupMember(targetID, "member-b", "10.0.0.2", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	lister := &fakeServerGroupMemberLister{results: members}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	matches, err := finder.Find(context.Background(), ServerGroupMemberFilter{ID: &targetID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != targetID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestServerGroupMemberFinderFiltersOutDeleted(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	deletedID := core.UUID{10}
	activeID := core.UUID{11}
	members := []loadbalancersdk.ServerGroupMemberSchema{
		sampleServerGroupMember(deletedID, "member-deleted", "10.0.0.1", 80, loadbalancersdk.LoadBalancingProvisioningStatusDELETED),
		sampleServerGroupMember(activeID, "member-active", "10.0.0.2", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	lister := &fakeServerGroupMemberLister{results: members}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	// Filter by deleted ID -> should return 0 results
	matches, err := finder.Find(context.Background(), ServerGroupMemberFilter{ID: &deletedID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected deleted member to be filtered out, got %#v", matches)
	}

	// Filter by name matching deleted -> should return 0 results
	delName := "member-deleted"
	matches, err = finder.Find(context.Background(), ServerGroupMemberFilter{Name: &delName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected deleted member to be filtered out, got %#v", matches)
	}

	// Filter by active ID -> should match
	matches, err = finder.Find(context.Background(), ServerGroupMemberFilter{ID: &activeID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != activeID {
		t.Fatalf("expected active member to match, got %#v", matches)
	}
}

func TestServerGroupMemberFinderResolve(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	targetID := core.UUID{11}
	members := []loadbalancersdk.ServerGroupMemberSchema{
		sampleServerGroupMember(core.UUID{10}, "member-a", "10.0.0.1", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleServerGroupMember(targetID, "member-b", "10.0.0.2", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	lister := &fakeServerGroupMemberLister{results: members}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	name := "member-b"
	result, err := finder.Resolve(context.Background(), ServerGroupMemberFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Id != targetID {
		t.Fatalf("resolved id=%s, want %s", result.Id, targetID)
	}
}

func TestServerGroupMemberFinderResolveNotFound(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	lister := &fakeServerGroupMemberLister{results: nil}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	name := "non-existent"
	_, err := finder.Resolve(context.Background(), ServerGroupMemberFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestServerGroupMemberFinderResolveAmbiguous(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	members := []loadbalancersdk.ServerGroupMemberSchema{
		sampleServerGroupMember(core.UUID{10}, "member-dup", "10.0.0.1", 80, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleServerGroupMember(core.UUID{11}, "member-dup", "10.0.0.2", 8080, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	lister := &fakeServerGroupMemberLister{results: members}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	name := "member-dup"
	_, err := finder.Resolve(context.Background(), ServerGroupMemberFilter{Name: &name})
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestServerGroupMemberFinderError(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	serverGroupID := core.UUID{8}
	expectedErr := errors.New("network failure")
	lister := &fakeServerGroupMemberLister{err: expectedErr}
	finder := NewServerGroupMemberFinder(lister, projectID, serverGroupID)

	_, err := finder.Find(context.Background(), ServerGroupMemberFilter{})
	if err == nil || !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestServerGroupMemberFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{1}
	name := "web-1"

	f := ServerGroupMemberFilter{ID: &id, Name: &name}
	s := f.String()
	if !strings.Contains(s, "id=") || !strings.Contains(s, `name="web-1"`) {
		t.Fatalf("unexpected filter string: %s", s)
	}

	empty := ServerGroupMemberFilter{}
	if empty.String() != "all" {
		t.Fatalf("expected 'all', got %q", empty.String())
	}
}
