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

type fakeLoadBalancerLister struct {
	results []loadbalancersdk.LoadBalancerSchema
	err     error
	params  loadbalancersdk.ListLoadBalancersParams
}

func (f *fakeLoadBalancerLister) ListLoadBalancersIter(
	_ context.Context,
	params loadbalancersdk.ListLoadBalancersParams,
) iter.Seq2[*loadbalancersdk.LoadBalancerSchema, error] {
	f.params = params
	return func(yield func(*loadbalancersdk.LoadBalancerSchema, error) bool) {
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

func sampleLoadBalancer(id core.UUID, name string) loadbalancersdk.LoadBalancerSchema {
	return loadbalancersdk.LoadBalancerSchema{
		Id:                 id,
		Name:               name,
		Package:            loadbalancersdk.LoadBalancerPackageSMALL,
		Topology:           loadbalancersdk.LoadBalancerTopologySINGLE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Region: loadbalancersdk.NestedRegionSchema{
			Id:   core.UUID{9},
			Name: "vn-central-1",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func TestLoadBalancerFinderFindPassesFiltersToBackend(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	regionID := core.UUID{9}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{
		sampleLoadBalancer(lbID, "primary"),
		sampleLoadBalancer(core.UUID{2}, "primary"),
	}}

	matches, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{
		ID:       &lbID,
		Name:     new("  primary  "),
		RegionID: &regionID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != lbID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID {
		t.Fatalf("expected ProjectID=%v, got %v", projectID, lister.params.ProjectID)
	}
	if lister.params.Name == nil || *lister.params.Name != "primary" {
		t.Fatalf("expected Name=primary, got %v", lister.params.Name)
	}
	if lister.params.RegionId == nil || *lister.params.RegionId != regionID {
		t.Fatalf("expected RegionId=%v, got %v", regionID, lister.params.RegionId)
	}
}

func TestLoadBalancerFinderRejectsMismatchedID(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	other := core.UUID{2}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{sampleLoadBalancer(lbID, "primary")}}

	matches, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{ID: &other})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected zero matches, got %#v", matches)
	}
}

func TestLoadBalancerFinderRejectsMismatchedName(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{sampleLoadBalancer(lbID, "primary")}}

	name := "secondary"
	matches, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected zero matches, got %#v", matches)
	}
}

func TestLoadBalancerFinderRejectsMismatchedRegion(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	otherRegion := core.UUID{99}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{sampleLoadBalancer(lbID, "primary")}}

	matches, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{RegionID: &otherRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected zero matches, got %#v", matches)
	}
}

func TestLoadBalancerFinderResolveSuccess(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{sampleLoadBalancer(lbID, "primary")}}

	got, err := NewLoadBalancerFinder(lister, projectID).Resolve(context.Background(), LoadBalancerFilter{Name: new("primary")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Id != lbID {
		t.Fatalf("expected id %v, got %v", lbID, got.Id)
	}
}

func TestLoadBalancerFinderResolveAmbiguous(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{
		sampleLoadBalancer(core.UUID{1}, "primary"),
		sampleLoadBalancer(core.UUID{2}, "primary"),
	}}
	_, err := NewLoadBalancerFinder(lister, projectID).Resolve(context.Background(), LoadBalancerFilter{Name: new("primary")})
	if err == nil {
		t.Fatal("expected ambiguous result error")
	}
	if !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadBalancerFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lister := &fakeLoadBalancerLister{}
	_, err := NewLoadBalancerFinder(lister, projectID).Resolve(context.Background(), LoadBalancerFilter{Name: new("missing")})
	if err == nil {
		t.Fatal("expected error for zero matches")
	}
	if !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadBalancerFinderReturnsListError(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	wantErr := errors.New("boom")
	lister := &fakeLoadBalancerLister{err: wantErr}
	_, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{})
	if err == nil {
		t.Fatal("expected error from lister")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadBalancerFinderExcludesDeleted(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	deletedLB := sampleLoadBalancer(lbID, "primary")
	deletedLB.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{deletedLB}}

	matches, err := NewLoadBalancerFinder(lister, projectID).Find(context.Background(), LoadBalancerFilter{ID: &lbID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected zero matches for DELETED load balancer, got %#v", matches)
	}
}

func TestLoadBalancerFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{1}
	got := LoadBalancerFilter{ID: &id, Name: new("primary")}.String()
	if !strings.Contains(got, "id="+id.String()) {
		t.Fatalf("missing id in %q", got)
	}
	if !strings.Contains(got, `name="primary"`) {
		t.Fatalf("missing name in %q", got)
	}

	empty := LoadBalancerFilter{}.String()
	if empty != "all" {
		t.Fatalf("expected 'all', got %q", empty)
	}
}

func TestLoadBalancerFinderAdditionalFilters(t *testing.T) {
	t.Parallel()
	lb := sampleLoadBalancer(core.UUID{1}, "primary")
	lb.Type = loadbalancersdk.LoadBalancerTypeNative
	lb.VipAddress.Set("10.0.0.10")
	lb.PublicAddress.Set("192.0.2.1")
	lb.V6PublicAddress.Set("2001:db8::1")
	for _, tt := range []struct {
		name    string
		filter  LoadBalancerFilter
		matches bool
	}{
		{"vip", LoadBalancerFilter{VIPAddress: new(" 10.0.0.10 ")}, true},
		{"vip mismatch", LoadBalancerFilter{VIPAddress: new("10.0.0.11")}, false},
		{"invalid IP", LoadBalancerFilter{VIPAddress: new("bad")}, false},
		{"blank IP unset", LoadBalancerFilter{VIPAddress: new(" ")}, true},
		{"public", LoadBalancerFilter{PublicAddress: new("192.0.2.1")}, true},
		{"public mismatch", LoadBalancerFilter{PublicAddress: new("192.0.2.2")}, false},
		{"IPv6 normalized", LoadBalancerFilter{V6PublicAddress: new(" 2001:0DB8:0000::0001 ")}, true},
		{"IPv6 mismatch", LoadBalancerFilter{V6PublicAddress: new("2001:db8::2")}, false},
		{"package", LoadBalancerFilter{Package: new(loadbalancersdk.LoadBalancerPackageSMALL)}, true},
		{"package mismatch", LoadBalancerFilter{Package: new(loadbalancersdk.LoadBalancerPackageLARGE)}, false},
		{"topology", LoadBalancerFilter{Topology: new(loadbalancersdk.LoadBalancerTopologySINGLE)}, true},
		{"topology mismatch", LoadBalancerFilter{Topology: new(loadbalancersdk.LoadBalancerTopologyACTIVESTANDBY)}, false},
		{"type", LoadBalancerFilter{Type: new(loadbalancersdk.LoadBalancerTypeNative)}, true},
		{"type mismatch", LoadBalancerFilter{Type: new(loadbalancersdk.LoadBalancerTypeAppliance)}, false},
		{"access log false", LoadBalancerFilter{AccessLogEnabled: new(false)}, true},
		{"access log true", LoadBalancerFilter{AccessLogEnabled: new(true)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{lb}}
			got, err := NewLoadBalancerFinder(lister, core.UUID{9}).Find(context.Background(), tt.filter)
			if err != nil || (len(got) == 1) != tt.matches {
				t.Fatalf("matches=%v error=%v, want match=%t", got, err, tt.matches)
			}
		})
	}
}

func TestLoadBalancerFinderNullAddressDoesNotMatch(t *testing.T) {
	t.Parallel()
	lb := sampleLoadBalancer(core.UUID{1}, "primary")
	lb.VipAddress.SetNull()
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{lb}}
	got, err := NewLoadBalancerFinder(lister, core.UUID{9}).Find(context.Background(), LoadBalancerFilter{VIPAddress: new("10.0.0.10")})
	if err != nil || len(got) != 0 {
		t.Fatalf("a null vip_address must not match: got=%v error=%v", got, err)
	}
}

func TestLoadBalancerFinderAllFiltersPassNormalizedParameters(t *testing.T) {
	t.Parallel()
	lister := &fakeLoadBalancerLister{}
	filter := LoadBalancerFilter{
		Name: new(" primary "), RegionID: new(core.UUID{9}),
		VIPAddress: new(" 10.0.0.10 "), PublicAddress: new(" 192.0.2.1 "),
		V6PublicAddress: new("2001:0DB8:0000::0001"),
		Package:         new(loadbalancersdk.LoadBalancerPackageSMALL), Topology: new(loadbalancersdk.LoadBalancerTopologySINGLE),
		Type: new(loadbalancersdk.LoadBalancerTypeNative), AccessLogEnabled: new(false),
	}
	_, err := NewLoadBalancerFinder(lister, core.UUID{8}).Find(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	p := lister.params
	if p.ProjectID != (core.UUID{8}) || p.Name == nil || *p.Name != "primary" || p.RegionId == nil || *p.RegionId != (core.UUID{9}) ||
		p.VipAddress == nil || *p.VipAddress != "10.0.0.10" || p.PublicAddress == nil || *p.PublicAddress != "192.0.2.1" ||
		p.V6PublicAddress == nil || *p.V6PublicAddress != "2001:db8::1" || p.Package == nil || *p.Package != *filter.Package ||
		p.Topology == nil || *p.Topology != *filter.Topology || p.Type == nil || *p.Type != *filter.Type || p.AccessLogEnabled == nil || *p.AccessLogEnabled {
		t.Fatalf("unexpected backend parameters: %+v", p)
	}
	for _, part := range []string{`vip_address="10.0.0.10"`, `public_address="192.0.2.1"`, `v6_public_address="2001:db8::1"`, "package=SMALL", "topology=SINGLE", "type=native", "access_log_enabled=false"} {
		if !strings.Contains(filter.String(), part) {
			t.Errorf("missing %s from %s", part, filter.String())
		}
	}
}

func TestLoadBalancerFinderMissingAddressAndLateError(t *testing.T) {
	t.Parallel()
	lb := sampleLoadBalancer(core.UUID{1}, "primary")
	lister := &fakeLoadBalancerLister{results: []loadbalancersdk.LoadBalancerSchema{lb}}
	got, err := NewLoadBalancerFinder(lister, core.UUID{9}).Find(context.Background(), LoadBalancerFilter{PublicAddress: new("192.0.2.1")})
	if err != nil || len(got) != 0 {
		t.Fatalf("unexpected matches=%v error=%v", got, err)
	}
	lister.err = errors.New("later page failed")
	_, err = NewLoadBalancerFinder(lister, core.UUID{9}).Resolve(context.Background(), LoadBalancerFilter{Name: new("primary")})
	if err == nil || !strings.Contains(err.Error(), "later page failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeOwnerLister struct {
	loadBalancers  []core.UUID
	listeners      map[core.UUID][]core.UUID
	serverGroups   map[core.UUID][]core.UUID
	listErr        error
	statuses       map[core.UUID]loadbalancersdk.LoadBalancingProvisioningStatus
	childErr       map[core.UUID]error
	projectIDs     []core.UUID
	childListCalls []core.UUID
}

func (f *fakeOwnerLister) ListLoadBalancersIter(
	_ context.Context,
	params loadbalancersdk.ListLoadBalancersParams,
) iter.Seq2[*loadbalancersdk.LoadBalancerSchema, error] {
	f.projectIDs = append(f.projectIDs, params.ProjectID)
	return func(yield func(*loadbalancersdk.LoadBalancerSchema, error) bool) {
		for _, id := range f.loadBalancers {
			status := loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
			if s, ok := f.statuses[id]; ok {
				status = s
			}
			if !yield(&loadbalancersdk.LoadBalancerSchema{Id: id, ProvisioningStatus: status}, nil) {
				return
			}
		}
		if f.listErr != nil {
			yield(nil, f.listErr)
		}
	}
}

func (f *fakeOwnerLister) ListListenersIter(
	_ context.Context,
	loadBalancerID core.UUID,
	params loadbalancersdk.ListListenersParams,
) iter.Seq2[*loadbalancersdk.ListenerSchema, error] {
	f.projectIDs = append(f.projectIDs, params.ProjectID)
	f.childListCalls = append(f.childListCalls, loadBalancerID)
	return func(yield func(*loadbalancersdk.ListenerSchema, error) bool) {
		if err := f.childErr[loadBalancerID]; err != nil {
			yield(nil, err)
			return
		}
		for _, id := range f.listeners[loadBalancerID] {
			if !yield(&loadbalancersdk.ListenerSchema{Id: id}, nil) {
				return
			}
		}
	}
}

func (f *fakeOwnerLister) ListServerGroupsIter(
	_ context.Context,
	loadBalancerID core.UUID,
	params loadbalancersdk.ListServerGroupsParams,
) iter.Seq2[*loadbalancersdk.ServerGroupSchema, error] {
	f.projectIDs = append(f.projectIDs, params.ProjectID)
	f.childListCalls = append(f.childListCalls, loadBalancerID)
	return func(yield func(*loadbalancersdk.ServerGroupSchema, error) bool) {
		if err := f.childErr[loadBalancerID]; err != nil {
			yield(nil, err)
			return
		}
		for _, id := range f.serverGroups[loadBalancerID] {
			if !yield(&loadbalancersdk.ServerGroupSchema{Id: id}, nil) {
				return
			}
		}
	}
}

func TestOwnerLoadBalancer(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	firstLB, secondLB, thirdLB := core.UUID{1}, core.UUID{2}, core.UUID{3}
	child := core.UUID{7}
	search := map[string]func(context.Context, *fakeOwnerLister, core.UUID) (core.UUID, error){
		"listener": func(ctx context.Context, f *fakeOwnerLister, id core.UUID) (core.UUID, error) {
			return ListenerLoadBalancer(ctx, f, projectID, id)
		},
		"server group": func(ctx context.Context, f *fakeOwnerLister, id core.UUID) (core.UUID, error) {
			return ServerGroupLoadBalancer(ctx, f, projectID, id)
		},
	}
	children := func(owned map[core.UUID][]core.UUID) *fakeOwnerLister {
		return &fakeOwnerLister{
			loadBalancers: []core.UUID{firstLB, secondLB, thirdLB},
			listeners:     owned,
			serverGroups:  owned,
		}
	}

	for kind, find := range search {
		t.Run(kind+" found stops at the owner", func(t *testing.T) {
			t.Parallel()
			f := children(map[core.UUID][]core.UUID{firstLB: {{5}}, secondLB: {{6}, child}, thirdLB: {child}})
			got, err := find(context.Background(), f, child)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != secondLB {
				t.Fatalf("owner = %s, want %s", got, secondLB)
			}
			if len(f.childListCalls) != 2 {
				t.Fatalf("child lists = %v, want the first two load balancers only", f.childListCalls)
			}
			for _, id := range f.projectIDs {
				if id != projectID {
					t.Fatalf("project ID = %s, want %s", id, projectID)
				}
			}
		})

		t.Run(kind+" skips deleted load balancers", func(t *testing.T) {
			t.Parallel()
			f := children(map[core.UUID][]core.UUID{firstLB: {child}, secondLB: {child}})
			f.statuses = map[core.UUID]loadbalancersdk.LoadBalancingProvisioningStatus{
				firstLB: loadbalancersdk.LoadBalancingProvisioningStatusDELETED,
			}
			got, err := find(context.Background(), f, child)
			if err != nil || got != secondLB {
				t.Fatalf("owner = %s, %v; want %s", got, err, secondLB)
			}
		})

		t.Run(kind+" not found", func(t *testing.T) {
			t.Parallel()
			f := children(map[core.UUID][]core.UUID{firstLB: {{5}}})
			_, err := find(context.Background(), f, child)
			if err == nil || !strings.Contains(err.Error(), "no load balancer of project") || !strings.Contains(err.Error(), kind) {
				t.Fatalf("error = %v, want a not-owned error naming the %s", err, kind)
			}
		})

		t.Run(kind+" load balancer list error", func(t *testing.T) {
			t.Parallel()
			listErr := errors.New("list failed")
			f := children(nil)
			f.loadBalancers = nil
			f.listErr = listErr
			_, err := find(context.Background(), f, child)
			if !errors.Is(err, listErr) || !strings.Contains(err.Error(), "list load balancers for criteria") {
				t.Fatalf("error = %v, want the wrapped list error", err)
			}
		})

		t.Run(kind+" child list error", func(t *testing.T) {
			t.Parallel()
			childErr := errors.New("children failed")
			f := children(nil)
			f.childErr = map[core.UUID]error{firstLB: childErr}
			_, err := find(context.Background(), f, child)
			if !errors.Is(err, childErr) || !strings.Contains(err.Error(), "of load balancer "+firstLB.String()) {
				t.Fatalf("error = %v, want the wrapped child list error", err)
			}
		})
	}
}
