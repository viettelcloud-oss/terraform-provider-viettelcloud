package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"
)

type fakeFilesystemSnapshotPolicyLister struct {
	values []filestoragesdk.FilesystemSnapshotPolicySchema
	err    error
	params filestoragesdk.ListFilesystemSnapshotPoliciesParams
}

func (f *fakeFilesystemSnapshotPolicyLister) ListFilesystemSnapshotPoliciesIter(_ context.Context, params filestoragesdk.ListFilesystemSnapshotPoliciesParams) iter.Seq2[*filestoragesdk.FilesystemSnapshotPolicySchema, error] {
	f.params = params
	return func(yield func(*filestoragesdk.FilesystemSnapshotPolicySchema, error) bool) {
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

func TestFilesystemSnapshotPolicyFinderExactFiltersAndCardinality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projectID, targetID := core.UUID{9}, core.UUID{3}
	name, region := " daily ", " vn-central-1 "
	lister := &fakeFilesystemSnapshotPolicyLister{values: []filestoragesdk.FilesystemSnapshotPolicySchema{
		{Id: targetID, Name: "daily", Region: filestoragesdk.NestedRegionSchema{Name: "vn-central-1"}},
		{Id: core.UUID{4}, Name: "daily", Region: filestoragesdk.NestedRegionSchema{Name: "vn-north-1"}},
		{Id: core.UUID{5}, Name: "daily-2", Region: filestoragesdk.NestedRegionSchema{Name: "vn-central-1"}},
	}}
	finder := NewFilesystemSnapshotPolicyFinder(lister, projectID)

	got, err := finder.Resolve(ctx, FilesystemSnapshotPolicyFilter{Name: &name, RegionName: &region})
	if err != nil || got.Id != targetID {
		t.Fatalf("resolve by name and region: %#v %v", got, err)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "daily" ||
		lister.params.RegionName == nil || *lister.params.RegionName != "vn-central-1" {
		t.Fatalf("backend filters: %#v", lister.params)
	}
	if got, err := finder.Resolve(ctx, FilesystemSnapshotPolicyFilter{ID: &targetID}); err != nil || got.Id != targetID {
		t.Fatalf("resolve by ID: %#v %v", got, err)
	}

	blank := "  "
	candidates, err := finder.Find(ctx, FilesystemSnapshotPolicyFilter{Name: &blank, RegionName: &blank})
	if err != nil || len(candidates) != 3 || lister.params.Name != nil || lister.params.RegionName != nil {
		t.Fatalf("blank criteria must be unset: %d candidates, params %#v, %v", len(candidates), lister.params, err)
	}

	missingID := core.UUID{7}
	if _, err := finder.Resolve(ctx, FilesystemSnapshotPolicyFilter{ID: &missingID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero matches: %v", err)
	}
	if _, err := finder.Resolve(ctx, FilesystemSnapshotPolicyFilter{Name: &name}); err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), core.UUID{4}.String()) {
		t.Fatalf("multiple matches must name every candidate: %v", err)
	}

	backendErr := errors.New("list failed")
	lister.err = backendErr
	if _, err := finder.Find(ctx, FilesystemSnapshotPolicyFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("list error: %v", err)
	}
}
