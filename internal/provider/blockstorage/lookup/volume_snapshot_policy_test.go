package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
)

type fakeVolumeSnapshotPolicyLister struct {
	values []blockstoragesdk.SnapshotPolicySchema
	err    error
	params blockstoragesdk.ListSnapshotPoliciesParams
}

func (f *fakeVolumeSnapshotPolicyLister) ListSnapshotPoliciesIter(_ context.Context, params blockstoragesdk.ListSnapshotPoliciesParams) iter.Seq2[*blockstoragesdk.SnapshotPolicySchema, error] {
	f.params = params
	return func(yield func(*blockstoragesdk.SnapshotPolicySchema, error) bool) {
		for i := range f.values {
			if !yield(&f.values[i], nil) {
				return
			}
		}
		if f.err != nil {
			yield(nil, f.err)
		}
	}
}

func TestVolumeSnapshotPolicyFinderExactFiltersAndCardinality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projectID, targetID := core.UUID{9}, core.UUID{3}
	central, north := core.UUID{7}, core.UUID{8}
	name, regionName := " daily ", " vn-central-1 "
	enabled, disabled := blockstoragesdk.SnapshotPolicyStatusEnabled, blockstoragesdk.SnapshotPolicyStatusDisabled
	lister := &fakeVolumeSnapshotPolicyLister{values: []blockstoragesdk.SnapshotPolicySchema{
		{Id: targetID, Name: "daily", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: central, Name: "vn-central-1"}},
		{Id: core.UUID{4}, Name: "daily", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: north, Name: "vn-north-1"}},
		{Id: core.UUID{5}, Name: "daily", Status: disabled, Region: blockstoragesdk.NestedRegionSchema{Id: central, Name: "vn-central-1"}},
		{Id: core.UUID{6}, Name: "daily-2", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: central, Name: "vn-central-1"}},
	}}
	finder := NewVolumeSnapshotPolicyFinder(lister, projectID)

	got, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{Name: &name, RegionID: &central, RegionName: &regionName, Status: &enabled})
	if err != nil || got.Id != targetID {
		t.Fatalf("resolve by name, region, and status: %#v %v", got, err)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "daily" ||
		lister.params.RegionId == nil || *lister.params.RegionId != central || lister.params.Status == nil || *lister.params.Status != enabled ||
		lister.params.ExecutionStrategy != nil {
		t.Fatalf("API filters: %#v", lister.params)
	}
	if got, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{ID: &targetID}); err != nil || got.Id != targetID {
		t.Fatalf("resolve by ID: %#v %v", got, err)
	}
	if got, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{Name: &name, RegionID: &central, Status: &disabled}); err != nil || got.Id != (core.UUID{5}) {
		t.Fatalf("the status must be an exact criterion: %#v %v", got, err)
	}

	blank := "  "
	candidates, err := finder.Find(ctx, VolumeSnapshotPolicyFilter{Name: &blank, RegionName: &blank})
	if err != nil || len(candidates) != 4 || lister.params.Name != nil || lister.params.RegionId != nil || lister.params.Status != nil {
		t.Fatalf("blank criteria must be unset: %d candidates, params %#v, %v", len(candidates), lister.params, err)
	}

	missingID := core.UUID{7}
	if _, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{ID: &missingID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero matches: %v", err)
	}
	if _, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{Name: &name}); err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), core.UUID{4}.String()) {
		t.Fatalf("multiple matches must name every candidate: %v", err)
	}
	if _, err := finder.Resolve(ctx, VolumeSnapshotPolicyFilter{Name: &name, RegionName: &regionName}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("name and region without status must be ambiguous: %v", err)
	}

	backendErr := errors.New("list failed")
	lister.err = backendErr
	if _, err := finder.Find(ctx, VolumeSnapshotPolicyFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("list error: %v", err)
	}
}
