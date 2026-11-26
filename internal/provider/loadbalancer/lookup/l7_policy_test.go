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

type fakeL7PolicyLister struct {
	results []loadbalancersdk.L7PolicySchema
	err     error
	params  loadbalancersdk.ListL7policiesParams
}

func (f *fakeL7PolicyLister) ListL7policiesIter(
	_ context.Context,
	params loadbalancersdk.ListL7policiesParams,
) iter.Seq2[*loadbalancersdk.L7PolicySchema, error] {
	f.params = params
	return func(yield func(*loadbalancersdk.L7PolicySchema, error) bool) {
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

func sampleL7Policy(id core.UUID, name string, listenerID core.UUID, status loadbalancersdk.LoadBalancingProvisioningStatus) loadbalancersdk.L7PolicySchema {
	return loadbalancersdk.L7PolicySchema{
		Id:                 id,
		Name:               core.NullableFromPtr(&name),
		ListenerId:         listenerID,
		Action:             loadbalancersdk.L7PolicyActionREJECT,
		Position:           1,
		ProvisioningStatus: status,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestL7PolicyFinderFindMatchesAllCriteria(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	listenerID := core.UUID{8}
	matchingID := core.UUID{1}
	name := "policy-1"
	lister := &fakeL7PolicyLister{results: []loadbalancersdk.L7PolicySchema{
		sampleL7Policy(matchingID, name, listenerID, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleL7Policy(core.UUID{2}, "other-policy", listenerID, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}}

	matches, err := NewL7PolicyFinder(lister, projectID).Find(context.Background(), L7PolicyFilter{
		ID:         &matchingID,
		Name:       &name,
		ListenerID: &listenerID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != matchingID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != name ||
		lister.params.ListenerId == nil || *lister.params.ListenerId != listenerID {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestL7PolicyFinderFiltersOutDeletedPolicies(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	listenerID := core.UUID{8}
	deletedID := core.UUID{1}
	name := "deleted-policy"
	lister := &fakeL7PolicyLister{results: []loadbalancersdk.L7PolicySchema{
		sampleL7Policy(deletedID, name, listenerID, loadbalancersdk.LoadBalancingProvisioningStatusDELETED),
	}}

	matches, err := NewL7PolicyFinder(lister, projectID).Find(context.Background(), L7PolicyFilter{
		Name: &name,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected deleted policy to be filtered out, got %#v", matches)
	}
}

func TestL7PolicyFinderFindRejectsEachMismatchedCriterion(t *testing.T) {
	t.Parallel()

	policyID := core.UUID{1}
	listenerID := core.UUID{8}
	policy := sampleL7Policy(policyID, "policy-1", listenerID, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)

	otherPolicyID := core.UUID{2}
	otherListenerID := core.UUID{7}
	otherName := "policy-2"
	tests := map[string]L7PolicyFilter{
		"id":          {ID: &otherPolicyID},
		"name":        {Name: &otherName},
		"listener_id": {ListenerID: &otherListenerID},
	}

	for name, filter := range tests {
		t.Run(name, func(t *testing.T) {
			lister := &fakeL7PolicyLister{results: []loadbalancersdk.L7PolicySchema{policy}}
			matches, err := NewL7PolicyFinder(lister, core.UUID{9}).Find(context.Background(), filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(matches) != 0 {
				t.Fatalf("expected mismatched %s filter to reject policy, got %#v", name, matches)
			}
		})
	}
}

func TestL7PolicyFinderTrimsTextFilters(t *testing.T) {
	t.Parallel()

	lister := &fakeL7PolicyLister{results: []loadbalancersdk.L7PolicySchema{
		sampleL7Policy(core.UUID{1}, "policy-1", core.UUID{8}, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}}
	name := "  policy-1  "

	matches, err := NewL7PolicyFinder(lister, core.UUID{9}).Find(context.Background(), L7PolicyFilter{
		Name: &name,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %#v", matches)
	}
	if *lister.params.Name != "policy-1" {
		t.Fatalf("expected trimmed list params, got %#v", lister.params)
	}
}

func TestL7PolicyFinderReturnsListError(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	finder := NewL7PolicyFinder(&fakeL7PolicyLister{err: apiErr}, core.UUID{9})
	_, err := finder.Find(context.Background(), L7PolicyFilter{})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected list error, got %v", err)
	}

	_, err = finder.Resolve(context.Background(), L7PolicyFilter{})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected resolve to return list error, got %v", err)
	}
}

func TestL7PolicyFinderResolveSuccess(t *testing.T) {
	t.Parallel()

	policy := sampleL7Policy(core.UUID{1}, "policy-1", core.UUID{8}, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)
	name := policy.Name.MustGet()
	result, err := NewL7PolicyFinder(
		&fakeL7PolicyLister{results: []loadbalancersdk.L7PolicySchema{policy}},
		core.UUID{9},
	).Resolve(context.Background(), L7PolicyFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Id != policy.Id {
		t.Fatalf("expected policy %s, got %s", policy.Id, result.Id)
	}
}

func TestL7PolicyFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	name := "missing"
	_, err := NewL7PolicyFinder(&fakeL7PolicyLister{}, core.UUID{9}).Resolve(
		context.Background(),
		L7PolicyFilter{Name: &name},
	)
	if err == nil || !strings.Contains(err.Error(), `L7 policy was not found matching criteria (name="missing")`) {
		t.Fatalf("expected not-found error naming the criteria, got %v", err)
	}
}

func TestL7PolicyFinderResolveMultipleMatches(t *testing.T) {
	t.Parallel()

	name := "duplicate"
	policies := []loadbalancersdk.L7PolicySchema{
		sampleL7Policy(core.UUID{1}, name, core.UUID{8}, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
		sampleL7Policy(core.UUID{2}, name, core.UUID{8}, loadbalancersdk.LoadBalancingProvisioningStatusACTIVE),
	}
	_, err := NewL7PolicyFinder(&fakeL7PolicyLister{results: policies}, core.UUID{9}).Resolve(
		context.Background(),
		L7PolicyFilter{Name: &name},
	)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	for _, want := range []string{
		"is ambiguous",
		policies[0].Id.String(),
		policies[1].Id.String(),
		`name="duplicate"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected ambiguity error to contain %q, got %v", want, err)
		}
	}
}

func TestL7PolicyFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{1}
	listenerID := core.UUID{8}
	name := " policy-1 "
	filter := L7PolicyFilter{
		ID: &id, Name: &name, ListenerID: &listenerID,
	}
	want := `id=01000000-0000-0000-0000-000000000000, name="policy-1", listener_id=08000000-0000-0000-0000-000000000000`
	if got := filter.String(); got != want {
		t.Fatalf("L7PolicyFilter.String() = %q, want %q", got, want)
	}

	blank := " "
	if got := (L7PolicyFilter{Name: &blank}).String(); got != "all" {
		t.Fatalf("blank text filter description = %q, want all", got)
	}
}
