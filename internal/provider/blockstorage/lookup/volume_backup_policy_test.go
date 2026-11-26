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

type fakeVolumeBackupPolicyLister struct {
	values []blockstoragesdk.VolumeBackupPolicySchema
	err    error
	params blockstoragesdk.ListVolumeBackupPoliciesParams
}

func (f *fakeVolumeBackupPolicyLister) ListVolumeBackupPoliciesIter(_ context.Context, params blockstoragesdk.ListVolumeBackupPoliciesParams) iter.Seq2[*blockstoragesdk.VolumeBackupPolicySchema, error] {
	f.params = params
	return func(yield func(*blockstoragesdk.VolumeBackupPolicySchema, error) bool) {
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

func TestVolumeBackupPolicyFinderExactFiltersAndCardinality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projectID, targetID, centralID, northID := core.UUID{9}, core.UUID{3}, core.UUID{7}, core.UUID{8}
	name := " daily "
	enabled, disabled := blockstoragesdk.VolumeBackupPolicyStatusEnabled, blockstoragesdk.VolumeBackupPolicyStatusDisabled
	lister := &fakeVolumeBackupPolicyLister{values: []blockstoragesdk.VolumeBackupPolicySchema{
		{Id: targetID, Name: "daily", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: centralID, Name: "vn-central-1"}},
		{Id: core.UUID{4}, Name: "daily", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: northID, Name: "vn-north-1"}},
		{Id: core.UUID{5}, Name: "daily", Status: disabled, Region: blockstoragesdk.NestedRegionSchema{Id: centralID, Name: "vn-central-1"}},
		{Id: core.UUID{6}, Name: "daily-2", Status: enabled, Region: blockstoragesdk.NestedRegionSchema{Id: centralID, Name: "vn-central-1"}},
	}}
	finder := NewVolumeBackupPolicyFinder(lister, projectID)

	got, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{Name: &name, RegionID: &centralID, Status: &enabled})
	if err != nil || got.Id != targetID {
		t.Fatalf("resolve by name, region, and status: %#v %v", got, err)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "daily" ||
		lister.params.RegionId == nil || *lister.params.RegionId != centralID || lister.params.Status == nil || *lister.params.Status != enabled {
		t.Fatalf("backend filters: %#v", lister.params)
	}
	if got, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{ID: &targetID}); err != nil || got.Id != targetID {
		t.Fatalf("resolve by ID: %#v %v", got, err)
	}
	if got, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{Name: &name, RegionID: &centralID, Status: &disabled}); err != nil || got.Id != (core.UUID{5}) {
		t.Fatalf("the status must match exactly on the client: %#v %v", got, err)
	}

	blank := "  "
	candidates, err := finder.Find(ctx, VolumeBackupPolicyFilter{Name: &blank})
	if err != nil || len(candidates) != 4 || lister.params.Name != nil || lister.params.RegionId != nil || lister.params.Status != nil {
		t.Fatalf("blank criteria must be unset: %d candidates, params %#v, %v", len(candidates), lister.params, err)
	}

	missingID := core.UUID{1}
	if _, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{ID: &missingID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero matches: %v", err)
	}
	if _, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{Name: &name, RegionID: &centralID}); err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), core.UUID{5}.String()) {
		t.Fatalf("multiple matches must name every candidate: %v", err)
	}

	backendErr := errors.New("list failed")
	lister.err = backendErr
	if _, err := finder.Find(ctx, VolumeBackupPolicyFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("list error: %v", err)
	}
	if _, err := finder.Resolve(ctx, VolumeBackupPolicyFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("resolve must pass the list error: %v", err)
	}
}
