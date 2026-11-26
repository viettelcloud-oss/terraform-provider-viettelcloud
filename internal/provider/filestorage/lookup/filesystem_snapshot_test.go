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

type fakeFilesystemSnapshotLister struct {
	values []filestoragesdk.FilesystemSnapshotSchema
	err    error
	params filestoragesdk.ListFilesystemSnapshotParams
}

func (f *fakeFilesystemSnapshotLister) ListFilesystemSnapshotIter(_ context.Context, params filestoragesdk.ListFilesystemSnapshotParams) iter.Seq2[*filestoragesdk.FilesystemSnapshotSchema, error] {
	f.params = params
	return func(yield func(*filestoragesdk.FilesystemSnapshotSchema, error) bool) {
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

func TestFilesystemSnapshotFinderExactFiltersAndCardinality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	projectID, filesystemID, targetID := core.UUID{9}, core.UUID{1}, core.UUID{3}
	name := " snapshot "
	lister := &fakeFilesystemSnapshotLister{values: []filestoragesdk.FilesystemSnapshotSchema{
		{Id: targetID, Name: "snapshot", Filesystem: filestoragesdk.FilesystemSchema{Id: filesystemID}},
		{Id: core.UUID{4}, Name: "snapshot", Filesystem: filestoragesdk.FilesystemSchema{Id: core.UUID{2}}},
		{Id: core.UUID{5}, Name: "snapshot-2", Filesystem: filestoragesdk.FilesystemSchema{Id: filesystemID}},
	}}
	finder := NewFilesystemSnapshotFinder(lister, projectID)

	got, err := finder.Resolve(ctx, FilesystemSnapshotFilter{Name: &name, FilesystemID: &filesystemID})
	if err != nil || got.Id != targetID {
		t.Fatalf("resolve by name and filesystem: %#v %v", got, err)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "snapshot" ||
		lister.params.FilesystemId == nil || *lister.params.FilesystemId != filesystemID {
		t.Fatalf("backend filters: %#v", lister.params)
	}
	if got, err := finder.Resolve(ctx, FilesystemSnapshotFilter{ID: &targetID}); err != nil || got.Id != targetID {
		t.Fatalf("resolve by ID: %#v %v", got, err)
	}

	blank := "  "
	candidates, err := finder.Find(ctx, FilesystemSnapshotFilter{Name: &blank})
	if err != nil || len(candidates) != 3 || lister.params.Name != nil {
		t.Fatalf("a blank name must be unset: %d candidates, params %#v, %v", len(candidates), lister.params, err)
	}

	missingID := core.UUID{7}
	if _, err := finder.Resolve(ctx, FilesystemSnapshotFilter{ID: &missingID}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero matches: %v", err)
	}
	if _, err := finder.Resolve(ctx, FilesystemSnapshotFilter{Name: &name}); err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), core.UUID{4}.String()) {
		t.Fatalf("multiple matches must name every candidate: %v", err)
	}

	backendErr := errors.New("list failed")
	lister.err = backendErr
	if _, err := finder.Find(ctx, FilesystemSnapshotFilter{}); !errors.Is(err, backendErr) {
		t.Fatalf("list error: %v", err)
	}
}
