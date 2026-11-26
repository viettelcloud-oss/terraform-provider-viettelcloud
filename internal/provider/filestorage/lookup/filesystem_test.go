package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"
)

type fakeFilesystemLister struct {
	values []filestoragepkg.FilesystemSchema
	err    error
	params filestoragepkg.ListFilesystemParams
}

func (f *fakeFilesystemLister) ListFilesystemIter(
	_ context.Context,
	params filestoragepkg.ListFilesystemParams,
) iter.Seq2[*filestoragepkg.FilesystemSchema, error] {
	f.params = params
	return func(yield func(*filestoragepkg.FilesystemSchema, error) bool) {
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

func testFilesystem(id core.UUID, name string, zone string, subnetID core.UUID) filestoragepkg.FilesystemSchema {
	return filestoragepkg.FilesystemSchema{
		Id:        id,
		Name:      name,
		Zone:      filestoragepkg.NestedZoneSchema{Id: core.UUID{8}, Name: zone},
		Subnet:    filestoragepkg.NestedSubnetSchema{Id: subnetID},
		Status:    filestoragepkg.FilesystemStatusAvailable,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func TestFilesystemFinderResolvesUniqueMatch(t *testing.T) {
	t.Parallel()
	projectID := core.UUID{9}
	wantID := core.UUID{1}
	subnetID := core.UUID{5}
	name := "my-fs"
	zone := "zone-a"
	lister := &fakeFilesystemLister{values: []filestoragepkg.FilesystemSchema{
		testFilesystem(wantID, name, zone, subnetID),
	}}
	finder := NewFilesystemFinder(lister, projectID)
	got, err := finder.Resolve(context.Background(), FilesystemFilter{Name: &name, Zone: &zone})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve filesystem: got=%#v err=%v", got, err)
	}
	if lister.params.ProjectID != projectID {
		t.Fatalf("project ID not sent: %#v", lister.params)
	}
	if lister.params.Name == nil || *lister.params.Name != name {
		t.Fatalf("name not sent: %#v", lister.params)
	}
	if lister.params.Zone == nil || *lister.params.Zone != zone {
		t.Fatalf("zone not sent: %#v", lister.params)
	}
}

func TestFilesystemFilterParamsTrimZone(t *testing.T) {
	t.Parallel()
	padded := "  zone-a  "
	if got := (FilesystemFilter{Zone: &padded}).params(core.UUID{9}); got.Zone == nil || *got.Zone != "zone-a" {
		t.Fatalf("expected trimmed zone, got %#v", got.Zone)
	}
	blank := "   "
	if got := (FilesystemFilter{Zone: &blank}).params(core.UUID{9}); got.Zone != nil {
		t.Fatalf("expected blank zone to be omitted, got %q", *got.Zone)
	}
}

func TestFilesystemFinderRejectsAmbiguousMatch(t *testing.T) {
	t.Parallel()
	name := "my-fs"
	lister := &fakeFilesystemLister{values: []filestoragepkg.FilesystemSchema{
		testFilesystem(core.UUID{1}, name, "zone-a", core.UUID{5}),
		testFilesystem(core.UUID{2}, name, "zone-a", core.UUID{5}),
	}}
	_, err := NewFilesystemFinder(lister, core.UUID{9}).Resolve(context.Background(), FilesystemFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestFilesystemFinderReturnsListError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("list failed")
	_, err := NewFilesystemFinder(&fakeFilesystemLister{err: wantErr}, core.UUID{9}).Find(context.Background(), FilesystemFilter{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected list error, got %v", err)
	}
}

func TestFilesystemFinderSubnetIDFilter(t *testing.T) {
	t.Parallel()
	subnetID := core.UUID{5}
	wantID := core.UUID{1}
	other := core.UUID{6}
	lister := &fakeFilesystemLister{values: []filestoragepkg.FilesystemSchema{
		testFilesystem(wantID, "fs-a", "zone-a", subnetID),
		testFilesystem(core.UUID{2}, "fs-b", "zone-a", other),
	}}
	got, err := NewFilesystemFinder(lister, core.UUID{9}).Resolve(context.Background(), FilesystemFilter{SubnetID: &subnetID})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve by subnet: got=%#v err=%v", got, err)
	}
	if lister.params.SubnetId == nil || *lister.params.SubnetId != subnetID {
		t.Fatalf("subnet_id not sent: %#v", lister.params)
	}
}
