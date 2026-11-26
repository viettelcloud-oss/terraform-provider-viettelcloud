package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"
)

type fakeFilesystemTypeLister struct {
	values []filestoragepkg.FilesystemTypeSchema
	err    error
	params filestoragepkg.ListFilesystemTypeParams
}

func (f *fakeFilesystemTypeLister) ListFilesystemTypeIter(
	_ context.Context,
	params filestoragepkg.ListFilesystemTypeParams,
) iter.Seq2[*filestoragepkg.FilesystemTypeSchema, error] {
	f.params = params
	return func(yield func(*filestoragepkg.FilesystemTypeSchema, error) bool) {
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

func testFilesystemType(id core.UUID, name string, zoneID core.UUID, enabled bool) filestoragepkg.FilesystemTypeSchema {
	status := filestoragepkg.FilesystemTypeStatusDisabled
	if enabled {
		status = filestoragepkg.FilesystemTypeStatusEnabled
	}
	st := core.NullableFromPtr(&status)
	return filestoragepkg.FilesystemTypeSchema{
		Id:     id,
		Name:   name,
		Zone:   filestoragepkg.NestedZoneSchema{Id: zoneID, Name: "zone-a"},
		Status: st,
	}
}

func TestFilesystemTypeFinderResolvesEnabledType(t *testing.T) {
	t.Parallel()
	zoneID := core.UUID{8}
	wantID := core.UUID{2}
	name := "Standard NFS"
	lister := &fakeFilesystemTypeLister{values: []filestoragepkg.FilesystemTypeSchema{
		testFilesystemType(core.UUID{1}, name, zoneID, false),
		testFilesystemType(wantID, name, zoneID, true),
	}}

	got, err := NewFilesystemTypeFinder(lister).Resolve(context.Background(), FilesystemTypeFilter{Name: &name, ZoneID: &zoneID})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve filesystem type: got=%#v err=%v", got, err)
	}
	if lister.params.Name == nil || *lister.params.Name != name || lister.params.ZoneId == nil || *lister.params.ZoneId != zoneID {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestFilesystemTypeFinderRejectsNoEnabledCandidate(t *testing.T) {
	t.Parallel()
	zoneID := core.UUID{8}
	name := "Standard NFS"
	lister := &fakeFilesystemTypeLister{values: []filestoragepkg.FilesystemTypeSchema{
		testFilesystemType(core.UUID{1}, name, zoneID, false),
	}}
	_, err := NewFilesystemTypeFinder(lister).Resolve(context.Background(), FilesystemTypeFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "no enabled candidate") {
		t.Fatalf("expected no-enabled-candidate error, got %v", err)
	}
}

func TestFilesystemTypeFinderRejectsAmbiguousMatch(t *testing.T) {
	t.Parallel()
	zoneID := core.UUID{8}
	name := "Standard NFS"
	lister := &fakeFilesystemTypeLister{values: []filestoragepkg.FilesystemTypeSchema{
		testFilesystemType(core.UUID{1}, name, zoneID, true),
		testFilesystemType(core.UUID{2}, name, zoneID, true),
	}}
	_, err := NewFilesystemTypeFinder(lister).Resolve(context.Background(), FilesystemTypeFilter{Name: &name})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestFilesystemTypeFinderReturnsListError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("list failed")
	_, err := NewFilesystemTypeFinder(&fakeFilesystemTypeLister{err: wantErr}).Find(context.Background(), FilesystemTypeFilter{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected list error, got %v", err)
	}
}

func TestFilesystemTypeFinderZoneNameFilter(t *testing.T) {
	t.Parallel()
	zoneName := "zone-a"
	wantID := core.UUID{1}
	lister := &fakeFilesystemTypeLister{values: []filestoragepkg.FilesystemTypeSchema{
		testFilesystemType(wantID, "NFS", core.UUID{8}, true),
	}}
	got, err := NewFilesystemTypeFinder(lister).Resolve(context.Background(), FilesystemTypeFilter{ZoneName: &zoneName})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve by zone name: got=%#v err=%v", got, err)
	}
	if lister.params.ZoneName == nil || *lister.params.ZoneName != zoneName {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestFilesystemTypeFilterParamsTrimZoneName(t *testing.T) {
	t.Parallel()
	padded := "  zone-a  "
	if got := (FilesystemTypeFilter{ZoneName: &padded}).params(); got.ZoneName == nil || *got.ZoneName != "zone-a" {
		t.Fatalf("expected trimmed zone name, got %#v", got.ZoneName)
	}
	blank := "   "
	if got := (FilesystemTypeFilter{ZoneName: &blank}).params(); got.ZoneName != nil {
		t.Fatalf("expected blank zone name to be omitted, got %q", *got.ZoneName)
	}
}

func TestFilesystemTypeFinderNameAndZoneIDFilter(t *testing.T) {
	t.Parallel()
	name := "NFS"
	zoneID := core.UUID{8}
	wantID := core.UUID{1}
	lister := &fakeFilesystemTypeLister{values: []filestoragepkg.FilesystemTypeSchema{
		testFilesystemType(core.UUID{2}, name, core.UUID{9}, true),
		testFilesystemType(wantID, name, zoneID, true),
		testFilesystemType(core.UUID{3}, "SMB", zoneID, true),
	}}
	got, err := NewFilesystemTypeFinder(lister).Resolve(context.Background(), FilesystemTypeFilter{Name: &name, ZoneID: &zoneID})
	if err != nil || got.Id != wantID {
		t.Fatalf("resolve by name and zone ID: got=%#v err=%v", got, err)
	}
	if lister.params.Name == nil || *lister.params.Name != name {
		t.Fatalf("name not sent: %#v", lister.params)
	}
	if lister.params.ZoneId == nil || *lister.params.ZoneId != zoneID {
		t.Fatalf("zone ID not sent: %#v", lister.params)
	}
	if lister.params.ZoneName != nil {
		t.Fatalf("zone name must not be sent: %#v", lister.params)
	}
}
