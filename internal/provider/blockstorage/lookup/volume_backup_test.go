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

type fakeVolumeBackupLister struct {
	items  []*blockstoragesdk.VolumeBackupSchema
	params blockstoragesdk.ListVolumeBackupsParams
	err    error
}

func (f *fakeVolumeBackupLister) ListVolumeBackupsIter(_ context.Context, params blockstoragesdk.ListVolumeBackupsParams) iter.Seq2[*blockstoragesdk.VolumeBackupSchema, error] {
	f.params = params
	return func(yield func(*blockstoragesdk.VolumeBackupSchema, error) bool) {
		for _, item := range f.items {
			if !yield(item, nil) {
				return
			}
		}
		if f.err != nil {
			yield(nil, f.err)
		}
	}
}

func TestVolumeBackupFinder(t *testing.T) {
	name := "selected"
	lister := &fakeVolumeBackupLister{items: []*blockstoragesdk.VolumeBackupSchema{
		{Name: "other"}, {Name: "selected"},
	}}
	finder := NewVolumeBackupFinder(lister, core.UUID{})
	result, err := finder.Resolve(context.Background(), VolumeBackupFilter{Name: &name})
	if err != nil || result.Name != name || lister.params.Name == nil || *lister.params.Name != name {
		t.Fatalf("exact selection failed: %#v %v", result, err)
	}
	name = "missing"
	_, err = finder.Resolve(context.Background(), VolumeBackupFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing backup: %v", err)
	}
	lister.items = append(lister.items, &blockstoragesdk.VolumeBackupSchema{Name: "selected"})
	name = "selected"
	_, err = finder.Resolve(context.Background(), VolumeBackupFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate backup: %v", err)
	}
	lister.err = errors.New("page failed")
	_, err = finder.Find(context.Background(), VolumeBackupFilter{})
	if err == nil || !strings.Contains(err.Error(), "page failed") {
		t.Fatalf("list error: %v", err)
	}
}
