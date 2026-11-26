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

type fakeListenerLister struct {
	results   []loadbalancersdk.ListenerSchema
	err       error
	params    loadbalancersdk.ListListenersParams
	lbID      core.UUID
	callCount int
}

func (f *fakeListenerLister) ListListenersIter(
	_ context.Context,
	lbID core.UUID,
	params loadbalancersdk.ListListenersParams,
) iter.Seq2[*loadbalancersdk.ListenerSchema, error] {
	f.callCount++
	f.lbID = lbID
	f.params = params
	return func(yield func(*loadbalancersdk.ListenerSchema, error) bool) {
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

func sampleListener(id core.UUID, name string, protocol loadbalancersdk.ListenerProtocolType, port int, lbID core.UUID) loadbalancersdk.ListenerSchema {
	return loadbalancersdk.ListenerSchema{
		Id:                 id,
		Name:               name,
		Protocol:           protocol,
		ProtocolPort:       port,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		LoadBalancer: loadbalancersdk.NestedLoadBalancerSchema{
			Id:   lbID,
			Name: "lb-test",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestListenerFinderFindByName(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	listeners := []loadbalancersdk.ListenerSchema{
		sampleListener(core.UUID{10}, "http-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID),
		sampleListener(core.UUID{11}, "https-listener", loadbalancersdk.ListenerProtocolTypeHTTPS, 443, lbID),
		sampleListener(core.UUID{12}, "http-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 8080, lbID),
	}
	lister := &fakeListenerLister{results: listeners}
	finder := NewListenerFinder(lister, projectID, lbID)

	name := "http-listener"
	matches, err := finder.Find(context.Background(), ListenerFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 || matches[0].Id != (core.UUID{10}) || matches[1].Id != (core.UUID{12}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.lbID != lbID || lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "http-listener" {
		t.Fatalf("unexpected list params: lbID=%s params=%#v", lister.lbID, lister.params)
	}
}

func TestListenerFinderFindByID(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	matchingID := core.UUID{10}
	listeners := []loadbalancersdk.ListenerSchema{
		sampleListener(matchingID, "http-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID),
		sampleListener(core.UUID{11}, "https-listener", loadbalancersdk.ListenerProtocolTypeHTTPS, 443, lbID),
	}
	lister := &fakeListenerLister{results: listeners}
	finder := NewListenerFinder(lister, projectID, lbID)

	matches, err := finder.Find(context.Background(), ListenerFilter{ID: &matchingID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != matchingID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestListenerFinderReturnsListError(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	finder := NewListenerFinder(&fakeListenerLister{err: apiErr}, core.UUID{9}, core.UUID{1})
	_, err := finder.Find(context.Background(), ListenerFilter{})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected apiErr, got %v", err)
	}
}

func TestListenerFinderResolveSuccess(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	listener := sampleListener(core.UUID{10}, "unique-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID)
	finder := NewListenerFinder(&fakeListenerLister{results: []loadbalancersdk.ListenerSchema{listener}}, projectID, lbID)

	name := "unique-listener"
	res, err := finder.Resolve(context.Background(), ListenerFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Id != listener.Id {
		t.Fatalf("expected resolved listener %s, got %s", listener.Id, res.Id)
	}
}

func TestListenerFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	finder := NewListenerFinder(&fakeListenerLister{results: nil}, core.UUID{9}, core.UUID{1})
	name := "non-existent"
	_, err := finder.Resolve(context.Background(), ListenerFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when no listener matches")
	}
	if !strings.Contains(err.Error(), `Listener was not found matching criteria (name="non-existent")`) {
		t.Fatalf("expected not-found error naming the criteria, got %v", err)
	}
}

func TestListenerFinderResolveMultipleMatches(t *testing.T) {
	t.Parallel()

	lbID := core.UUID{1}
	listeners := []loadbalancersdk.ListenerSchema{
		sampleListener(core.UUID{10}, "dup-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID),
		sampleListener(core.UUID{11}, "dup-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 8080, lbID),
	}
	finder := NewListenerFinder(&fakeListenerLister{results: listeners}, core.UUID{9}, lbID)
	name := "dup-listener"
	_, err := finder.Resolve(context.Background(), ListenerFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when multiple listeners match")
	}
	for _, want := range []string{
		"is ambiguous",
		`id=` + listeners[0].Id.String(),
		`id=` + listeners[1].Id.String(),
		`name="dup-listener"`,
		`port=80`,
		`port=8080`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected ambiguity error to contain %q, got %v", want, err)
		}
	}
}

func TestListenerFinderTrimsNameFilter(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	lister := &fakeListenerLister{results: []loadbalancersdk.ListenerSchema{
		sampleListener(core.UUID{10}, "http-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID),
	}}

	name := "  http-listener  "
	matches, err := NewListenerFinder(lister, projectID, lbID).Find(context.Background(), ListenerFilter{
		Name: &name,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("expected padded filter to match exact listener, got %#v", matches)
	}
	if lister.params.Name == nil || *lister.params.Name != "http-listener" {
		t.Fatalf("expected trimmed name in list params, got %#v", lister.params.Name)
	}
}

func TestListenerFilterString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   ListenerFilter
		expected string
	}{
		{
			name:     "empty filter",
			filter:   ListenerFilter{},
			expected: "all",
		},
		{
			name: "single name filter",
			filter: ListenerFilter{
				Name: new("my-listener"),
			},
			expected: `name="my-listener"`,
		},
		{
			name: "all criteria populated",
			filter: ListenerFilter{
				ID:   &core.UUID{10},
				Name: new("my-listener"),
			},
			expected: `id=0a000000-0000-0000-0000-000000000000, name="my-listener"`,
		},
		{
			name: "padded text is trimmed",
			filter: ListenerFilter{
				Name: new("  my-listener  "),
			},
			expected: `name="my-listener"`,
		},
		{
			name: "blank text is unset",
			filter: ListenerFilter{
				Name: new("   "),
			},
			expected: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.String(); got != tt.expected {
				t.Errorf("ListenerFilter.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestListenerFinderExcludesDeletedStatus(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	lbID := core.UUID{1}
	id := core.UUID{10}
	deletedListener := sampleListener(id, "deleted-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID)
	deletedListener.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED

	activeListener := sampleListener(core.UUID{11}, "active-listener", loadbalancersdk.ListenerProtocolTypeHTTP, 80, lbID)

	fake := &fakeListenerLister{
		results: []loadbalancersdk.ListenerSchema{deletedListener, activeListener},
	}
	finder := NewListenerFinder(fake, projectID, lbID)

	res, err := finder.Find(context.Background(), ListenerFilter{Name: new("deleted-listener")})
	if err != nil {
		t.Fatalf("unexpected find error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 matches for deleted listener, got %d", len(res))
	}

	all, err := finder.Find(context.Background(), ListenerFilter{})
	if err != nil {
		t.Fatalf("unexpected find error: %v", err)
	}
	if len(all) != 1 || all[0].Id != activeListener.Id {
		t.Fatalf("expected only active listener to match, got %v", all)
	}
}
