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

type fakeVolumeSnapshotLister struct {
	values []blockstoragesdk.VolumeSnapshotSchema
	err    error
	params blockstoragesdk.ListVolumeSnapshotsParams
}

func (f *fakeVolumeSnapshotLister) ListVolumeSnapshotsIter(_ context.Context, params blockstoragesdk.ListVolumeSnapshotsParams) iter.Seq2[*blockstoragesdk.VolumeSnapshotSchema, error] {
	f.params = params
	return func(yield func(*blockstoragesdk.VolumeSnapshotSchema, error) bool) {
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

func TestVolumeSnapshotFinderExactFiltersAndCardinality(t *testing.T) {
	t.Parallel()
	projectID, volumeID, targetID := core.UUID{9}, core.UUID{1}, core.UUID{3}
	name, zone := " snapshot ", " zone-a "
	status := blockstoragesdk.SnapshotStatusAvailable
	lister := &fakeVolumeSnapshotLister{values: []blockstoragesdk.VolumeSnapshotSchema{
		{Id: targetID, Name: "snapshot", Volume: blockstoragesdk.NestedVolumeSchema{Id: volumeID}, Zone: blockstoragesdk.NestedZoneSchema{Name: "zone-a"}, Status: &status},
		{Id: core.UUID{4}, Name: "snapshot", Volume: blockstoragesdk.NestedVolumeSchema{Id: core.UUID{2}}, Zone: blockstoragesdk.NestedZoneSchema{Name: "zone-a"}, Status: &status},
	}}
	finder := NewVolumeSnapshotFinder(lister, projectID)
	got, err := finder.Resolve(context.Background(), VolumeSnapshotFilter{ID: &targetID, Name: &name, VolumeID: &volumeID, Zone: &zone, Status: &status})
	if err != nil || got.Id != targetID {
		t.Fatalf("resolve snapshot: %#v %v", got, err)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "snapshot" ||
		lister.params.VolumeId == nil || *lister.params.VolumeId != volumeID || lister.params.Zone == nil || *lister.params.Zone != "zone-a" ||
		lister.params.Status == nil || len(*lister.params.Status) != 1 || (*lister.params.Status)[0] != status {
		t.Fatalf("backend filters: %#v", lister.params)
	}
	missingID := core.UUID{7}
	if _, err := finder.Resolve(context.Background(), VolumeSnapshotFilter{ID: &missingID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing snapshot: %v", err)
	}
	if _, err := finder.Resolve(context.Background(), VolumeSnapshotFilter{Name: &name}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous snapshot: %v", err)
	}
	backendErr := errors.New("list failed")
	lister.err = backendErr
	if _, err := finder.Find(context.Background(), VolumeSnapshotFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("list error: %v", err)
	}
}
