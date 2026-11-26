package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type fakeVDCLister struct {
	results   []serversdk.VDCSchema
	err       error
	params    serversdk.ListVdcsParams
	callCount int
}

func (f *fakeVDCLister) ListVdcsIter(_ context.Context, params serversdk.ListVdcsParams) iter.Seq2[*serversdk.VDCSchema, error] {
	f.callCount++
	f.params = params
	return func(yield func(*serversdk.VDCSchema, error) bool) {
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

func sampleVDC(id core.UUID, name, regionName, zoneName string) serversdk.VDCSchema {
	return serversdk.VDCSchema{
		Id:     id,
		Name:   name,
		Ratio:  1.5,
		Region: serversdk.NestedRegionSchema{Id: core.UUID{0x91}, Name: regionName},
		Zone:   serversdk.NestedZoneSchema{Id: core.UUID{0x92}, Name: zoneName},
	}
}

func TestVDCFinderFindPassesFiltersToBackend(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{7}
	id := core.UUID{10}
	zoneID := core.UUID{0x92}
	lister := &fakeVDCLister{results: []serversdk.VDCSchema{
		sampleVDC(id, "vdc-1", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{11}, "vdc-1", "vn-central", "vn-central-1a"),
	}}

	name := "  vdc-1  "
	region := " vn-central "
	zone := " vn-central-1a "
	matches, err := NewVDCFinder(lister, projectID).Find(context.Background(), VDCFilter{
		ID: &id, Name: &name, Region: &region, Zone: &zone, ZoneID: &zoneID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != id {
		t.Fatalf("expected only the vDC with the configured ID, got %#v", matches)
	}
	if lister.params.ProjectID != projectID ||
		lister.params.Name == nil || *lister.params.Name != "vdc-1" ||
		lister.params.Region == nil || *lister.params.Region != "vn-central" ||
		lister.params.ZoneId == nil || *lister.params.ZoneId != zoneID ||
		lister.params.Ordering == nil || *lister.params.Ordering != "created_at" {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
	if lister.params.RegionId != nil {
		t.Fatalf("expected no region_id filter, got %v", *lister.params.RegionId)
	}
}

// Every list request sorts by created_at, so that an operator edit cannot move
// a vDC between pages while the finder reads them.
func TestVDCFinderFindAlwaysOrdersByCreatedAt(t *testing.T) {
	t.Parallel()

	id := core.UUID{10}
	for name, filter := range map[string]VDCFilter{
		"id only":     {ID: &id},
		"name only":   {Name: new("vdc-1")},
		"region only": {Region: new("vn-central")},
		"no criteria": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lister := &fakeVDCLister{}
			if _, err := NewVDCFinder(lister, core.UUID{7}).Find(context.Background(), filter); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lister.params.Ordering == nil || *lister.params.Ordering != "created_at" {
				t.Fatalf("expected ordering=created_at, got %#v", lister.params.Ordering)
			}
		})
	}
}

func TestVDCFinderFindOmitsBlankCriteria(t *testing.T) {
	t.Parallel()

	lister := &fakeVDCLister{results: []serversdk.VDCSchema{
		sampleVDC(core.UUID{10}, "vdc-1", "vn-central", "vn-central-1a"),
	}}
	blank := "   "
	matches, err := NewVDCFinder(lister, core.UUID{7}).Find(context.Background(), VDCFilter{
		Name: &blank, Region: &blank, Zone: &blank,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected blank criteria to match everything, got %#v", matches)
	}
	if lister.params.Name != nil || lister.params.Region != nil || lister.params.ZoneId != nil {
		t.Fatalf("expected blank criteria to stay out of the request, got %#v", lister.params)
	}
}

// The list API filters name and region with a case-insensitive substring
// match, so the finder must drop rows that only contain the configured value.
func TestVDCFinderFindMatchesExactly(t *testing.T) {
	t.Parallel()

	lister := &fakeVDCLister{results: []serversdk.VDCSchema{
		sampleVDC(core.UUID{10}, "vdc-1", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{11}, "vdc-10", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{12}, "VDC-1", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{13}, "vdc-1", "vn-central-2", "vn-central-1a"),
		sampleVDC(core.UUID{14}, "vdc-1", "vn-central", "vn-central-1b"),
	}}

	matches, err := NewVDCFinder(lister, core.UUID{7}).Find(context.Background(), VDCFilter{
		Name: new("vdc-1"), Region: new("vn-central"), Zone: new("vn-central-1a"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("expected only the exact match, got %#v", matches)
	}
}

func TestVDCFinderFindSeparatesSameNameByZoneOrRegion(t *testing.T) {
	t.Parallel()

	results := []serversdk.VDCSchema{
		sampleVDC(core.UUID{10}, "shared", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{11}, "shared", "vn-central", "vn-central-1b"),
		sampleVDC(core.UUID{12}, "shared", "vn-north", "vn-north-1a"),
	}
	for name, tt := range map[string]struct {
		filter VDCFilter
		want   core.UUID
	}{
		"zone":   {VDCFilter{Name: new("shared"), Zone: new("vn-central-1b")}, core.UUID{11}},
		"region": {VDCFilter{Name: new("shared"), Region: new("vn-north")}, core.UUID{12}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := NewVDCFinder(&fakeVDCLister{results: results}, core.UUID{7}).Resolve(context.Background(), tt.filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Id != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got.Id)
			}
		})
	}
}

func TestVDCFinderResolveReturnsTheOnlyMatch(t *testing.T) {
	t.Parallel()

	want := sampleVDC(core.UUID{10}, "vdc-1", "vn-central", "vn-central-1a")
	got, err := NewVDCFinder(&fakeVDCLister{results: []serversdk.VDCSchema{want}}, core.UUID{7}).
		Resolve(context.Background(), VDCFilter{Name: new("vdc-1")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Id != want.Id || got.Ratio != want.Ratio {
		t.Fatalf("unexpected vDC: %#v", got)
	}
}

func TestVDCFinderResolveRejectsZeroMatches(t *testing.T) {
	t.Parallel()

	id := core.UUID{99}
	lister := &fakeVDCLister{results: []serversdk.VDCSchema{
		sampleVDC(core.UUID{10}, "vdc-1", "vn-central", "vn-central-1a"),
	}}
	_, err := NewVDCFinder(lister, core.UUID{7}).Resolve(context.Background(), VDCFilter{ID: &id, Name: new("vdc-1")})
	if err == nil {
		t.Fatal("expected an error when no vDC matches")
	}
	want := `vDC was not found matching criteria (id=` + id.String() + `, name="vdc-1")`
	if err.Error() != want {
		t.Fatalf("unexpected error:\n got: %s\nwant: %s", err, want)
	}
}

func TestVDCFinderResolveRejectsAmbiguousMatches(t *testing.T) {
	t.Parallel()

	lister := &fakeVDCLister{results: []serversdk.VDCSchema{
		sampleVDC(core.UUID{10}, "vdc-1", "vn-central", "vn-central-1a"),
		sampleVDC(core.UUID{11}, "vdc-1", "vn-central", "vn-central-1b"),
	}}
	_, err := NewVDCFinder(lister, core.UUID{7}).Resolve(context.Background(), VDCFilter{Name: new("vdc-1")})
	if err == nil {
		t.Fatal("expected an error when several vDCs match")
	}
	for _, part := range []string{
		`vDC matching criteria (name="vdc-1") is ambiguous`,
		`id=` + core.UUID{10}.String() + ` (name="vdc-1", region="vn-central", zone="vn-central-1a")`,
		`id=` + core.UUID{11}.String() + ` (name="vdc-1", region="vn-central", zone="vn-central-1b")`,
	} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("expected error to contain %q, got %s", part, err)
		}
	}
}

func TestVDCFinderResolveWrapsListError(t *testing.T) {
	t.Parallel()

	sdkErr := errors.New("boom")
	_, err := NewVDCFinder(&fakeVDCLister{err: sdkErr}, core.UUID{7}).Resolve(context.Background(), VDCFilter{Name: new("vdc-1")})
	if !errors.Is(err, sdkErr) {
		t.Fatalf("expected the SDK error to be wrapped, got %v", err)
	}
	if !strings.Contains(err.Error(), `list vDCs for criteria (name="vdc-1")`) {
		t.Fatalf("expected the criteria in the error, got %v", err)
	}
}
