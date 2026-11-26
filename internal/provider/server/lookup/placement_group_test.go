package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type fakePlacementGroupLister struct {
	results   []serversdk.PlacementGroupSchema
	err       error
	params    serversdk.ListPlacementGroupsParams
	callCount int
}

func (f *fakePlacementGroupLister) ListPlacementGroupsIter(
	_ context.Context,
	params serversdk.ListPlacementGroupsParams,
) iter.Seq2[*serversdk.PlacementGroupSchema, error] {
	f.callCount++
	f.params = params
	return func(yield func(*serversdk.PlacementGroupSchema, error) bool) {
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

func samplePlacementGroup(
	id core.UUID,
	name, regionName string,
	policy serversdk.PlacementGroupPolicy,
) serversdk.PlacementGroupSchema {
	value := policy
	pg := placementGroupWithoutPolicy(id, name, regionName)
	pg.Policy = core.NullableFromPtr(&value)
	return pg
}

func placementGroupWithoutPolicy(id core.UUID, name, regionName string) serversdk.PlacementGroupSchema {
	return serversdk.PlacementGroupSchema{
		Id:          id,
		Name:        name,
		Description: "Placement group " + name,
		Region: serversdk.NestedRegionSchema{
			Id:   core.UUID{9},
			Name: regionName,
		},
		ServerCount: new(0),
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestPlacementGroupFinderFindPassesFiltersToBackend(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{7}
	lister := &fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, "cluster-a", "vn-central", serversdk.PlacementGroupPolicyAffinity),
		samplePlacementGroup(core.UUID{11}, "cluster-b", "vn-central", serversdk.PlacementGroupPolicyAntiAffinity),
	}}

	name := "  cluster-a  "
	region := "vn-central"
	policy := serversdk.PlacementGroupPolicyAffinity
	id := core.UUID{10}
	matches, err := NewPlacementGroupFinder(lister, projectID).Find(context.Background(), PlacementGroupFilter{
		ID:     &id,
		Name:   &name,
		Policy: &policy,
		Region: &region,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID ||
		lister.params.Id == nil || *lister.params.Id != id ||
		lister.params.Name == nil || *lister.params.Name != "cluster-a" ||
		lister.params.RegionName == nil || *lister.params.RegionName != "vn-central" ||
		lister.params.Policy == nil || *lister.params.Policy != serversdk.PlacementGroupPolicyAffinity {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestPlacementGroupFinderFindMatchesEveryCriterion(t *testing.T) {
	t.Parallel()

	policy := serversdk.PlacementGroupPolicyAntiAffinity
	lister := &fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, "cluster-a", "vn-central", serversdk.PlacementGroupPolicyAntiAffinity),
		samplePlacementGroup(core.UUID{11}, "cluster-a", "vn-north", serversdk.PlacementGroupPolicyAntiAffinity),
		samplePlacementGroup(core.UUID{12}, "cluster-a", "vn-central", serversdk.PlacementGroupPolicyAffinity),
		samplePlacementGroup(core.UUID{13}, "cluster-b", "vn-central", serversdk.PlacementGroupPolicyAntiAffinity),
		placementGroupWithoutPolicy(core.UUID{14}, "cluster-a", "vn-central"),
	}}

	name := "cluster-a"
	region := "vn-central"
	matches, err := NewPlacementGroupFinder(lister, core.UUID{7}).Find(context.Background(), PlacementGroupFilter{
		Name:   &name,
		Policy: &policy,
		Region: &region,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestPlacementGroupFinderFindIgnoresBlankCriteria(t *testing.T) {
	t.Parallel()

	lister := &fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, "cluster-a", "vn-central", serversdk.PlacementGroupPolicyAffinity),
	}}

	blank := "   "
	blankPolicy := serversdk.PlacementGroupPolicy(blank)
	matches, err := NewPlacementGroupFinder(lister, core.UUID{7}).Find(context.Background(), PlacementGroupFilter{
		Name:   &blank,
		Policy: &blankPolicy,
		Region: &blank,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected blank criteria to be unset, got %#v", matches)
	}
	if lister.params.Name != nil || lister.params.RegionName != nil || lister.params.Policy != nil {
		t.Fatalf("expected blank criteria to be omitted from list params: %#v", lister.params)
	}
}

// The data source trims before it builds a filter, so the finder has to trim
// the policy criterion itself to stay usable by any other caller.
func TestPlacementGroupFinderTrimsPolicyCriterion(t *testing.T) {
	t.Parallel()

	lister := &fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, "cluster-a", "vn-central", serversdk.PlacementGroupPolicyAffinity),
		samplePlacementGroup(core.UUID{11}, "cluster-b", "vn-central", serversdk.PlacementGroupPolicyAntiAffinity),
	}}

	padded := serversdk.PlacementGroupPolicy("  affinity  ")
	filter := PlacementGroupFilter{Policy: &padded}
	matches, err := NewPlacementGroupFinder(lister, core.UUID{7}).Find(context.Background(), filter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("expected the padded policy to match the affinity candidate, got %#v", matches)
	}
	if lister.params.Policy == nil || *lister.params.Policy != serversdk.PlacementGroupPolicyAffinity {
		t.Fatalf("expected a trimmed policy in the list params, got %#v", lister.params.Policy)
	}
	if got := filter.String(); got != "policy=affinity" {
		t.Fatalf("expected a trimmed policy in the description, got %q", got)
	}
}

func TestPlacementGroupFinderResolveCardinality(t *testing.T) {
	t.Parallel()

	name := "cluster-a"
	finder := NewPlacementGroupFinder(&fakePlacementGroupLister{}, core.UUID{7})
	if _, err := finder.Resolve(context.Background(), PlacementGroupFilter{Name: &name}); err == nil ||
		!strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}

	ambiguous := NewPlacementGroupFinder(&fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, name, "vn-central", serversdk.PlacementGroupPolicyAffinity),
		samplePlacementGroup(core.UUID{11}, name, "vn-north", serversdk.PlacementGroupPolicyAffinity),
	}}, core.UUID{7})
	_, err := ambiguous.Resolve(context.Background(), PlacementGroupFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("expected an ambiguity error, got %v", err)
	}
	if !strings.Contains(err.Error(), `region="vn-central"`) || !strings.Contains(err.Error(), `region="vn-north"`) {
		t.Fatalf("expected both candidates to be summarized, got %v", err)
	}

	single := NewPlacementGroupFinder(&fakePlacementGroupLister{results: []serversdk.PlacementGroupSchema{
		samplePlacementGroup(core.UUID{10}, name, "vn-central", serversdk.PlacementGroupPolicyAffinity),
	}}, core.UUID{7})
	resolved, err := single.Resolve(context.Background(), PlacementGroupFilter{Name: &name})
	if err != nil || resolved.Id != (core.UUID{10}) {
		t.Fatalf("expected the single candidate, got %#v error=%v", resolved, err)
	}
}

func TestPlacementGroupFinderFindReportsSDKErrors(t *testing.T) {
	t.Parallel()

	listErr := errors.New("backend unavailable")
	finder := NewPlacementGroupFinder(&fakePlacementGroupLister{err: listErr}, core.UUID{7})

	name := "cluster-a"
	region := "vn-central"
	_, err := finder.Find(context.Background(), PlacementGroupFilter{Name: &name, Region: &region})
	if !errors.Is(err, listErr) {
		t.Fatalf("expected the SDK error to be wrapped, got %v", err)
	}
	if !strings.Contains(err.Error(), `name="cluster-a"`) ||
		!strings.Contains(err.Error(), `region="vn-central"`) {
		t.Fatalf("expected the criteria in the error, got %v", err)
	}
}

func TestPlacementGroupFilterString(t *testing.T) {
	t.Parallel()

	if got := (PlacementGroupFilter{}).String(); got != "all" {
		t.Fatalf("expected an empty filter to describe every placement group, got %q", got)
	}

	id := core.UUID{10}
	name := " cluster-a "
	region := " vn-central "
	policy := serversdk.PlacementGroupPolicyAffinity
	got := PlacementGroupFilter{ID: &id, Name: &name, Policy: &policy, Region: &region}.String()
	want := "id=" + id.String() + `, name="cluster-a", policy=affinity, region="vn-central"`
	if got != want {
		t.Fatalf("expected description %q, got %q", want, got)
	}
}
