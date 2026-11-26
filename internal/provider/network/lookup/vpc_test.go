package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type fakeVpcLister struct {
	results   []networksdk.VPCSchema
	err       error
	params    networksdk.ListVpcsParams
	callCount int
}

func (f *fakeVpcLister) ListVpcsIter(
	_ context.Context,
	params networksdk.ListVpcsParams,
) iter.Seq2[*networksdk.VPCSchema, error] {
	f.callCount++
	f.params = params
	return func(yield func(*networksdk.VPCSchema, error) bool) {
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

func sampleVPC(id core.UUID, name, cidr string, regionID core.UUID, regionName string) networksdk.VPCSchema {
	desc := name + " description"
	return networksdk.VPCSchema{
		Id:          id,
		Name:        name,
		DisplayName: name + "-display",
		Description: core.NullableFromPtr(&desc),
		Cidr:        cidr,
		Region: networksdk.NestedRegionSchema{
			Id:   regionID,
			Name: regionName,
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestVPCFinderFindByName(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	regionID := core.UUID{1}
	vpcs := []networksdk.VPCSchema{
		sampleVPC(core.UUID{10}, "vpc-a", "10.0.0.0/16", regionID, "vn-central"),
		sampleVPC(core.UUID{11}, "vpc-b", "10.1.0.0/16", regionID, "vn-central"),
		sampleVPC(core.UUID{12}, "vpc-a", "10.2.0.0/16", regionID, "vn-north"),
	}
	lister := &fakeVpcLister{results: vpcs}
	finder := NewVPCFinder(lister, projectID)

	name := "vpc-a"
	matches, err := finder.Find(context.Background(), VPCFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 2 || matches[0].Id != (core.UUID{10}) || matches[1].Id != (core.UUID{12}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "vpc-a" {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestVPCFinderFindByCIDRAndRegionID(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	regionID1 := core.UUID{1}
	regionID2 := core.UUID{2}
	vpcs := []networksdk.VPCSchema{
		sampleVPC(core.UUID{10}, "vpc-a", "10.0.0.0/16", regionID1, "vn-central"),
		sampleVPC(core.UUID{11}, "vpc-b", "10.0.0.0/16", regionID2, "vn-north"),
	}
	lister := &fakeVpcLister{results: vpcs}
	finder := NewVPCFinder(lister, projectID)

	cidr := "10.0.0.0/16"
	matches, err := finder.Find(context.Background(), VPCFilter{
		CIDR:     &cidr,
		RegionID: &regionID1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.RegionId == nil || *lister.params.RegionId != regionID1 {
		t.Fatalf("expected RegionId param, got %#v", lister.params.RegionId)
	}
}

func TestVPCFinderFindMatchesAllCriteria(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	regionID := core.UUID{1}
	matchingID := core.UUID{10}
	name := "vpc-a"
	cidr := "10.0.0.0/16"
	lister := &fakeVpcLister{results: []networksdk.VPCSchema{
		sampleVPC(matchingID, name, cidr, regionID, "vn-central"),
		sampleVPC(core.UUID{11}, name, "10.1.0.0/16", regionID, "vn-central"),
	}}

	matches, err := NewVPCFinder(lister, projectID).Find(context.Background(), VPCFilter{
		ID:       &matchingID,
		Name:     &name,
		CIDR:     &cidr,
		RegionID: &regionID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != matchingID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.Name == nil || *lister.params.Name != name || lister.params.RegionId == nil || *lister.params.RegionId != regionID {
		t.Fatalf("expected name and region filters in list params, got %#v", lister.params)
	}
}

func TestVPCFinderReturnsListError(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	finder := NewVPCFinder(&fakeVpcLister{err: apiErr}, core.UUID{9})
	_, err := finder.Find(context.Background(), VPCFilter{})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected apiErr, got %v", err)
	}
}

func TestVPCFinderResolveSuccess(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	regionID := core.UUID{1}
	vpc := sampleVPC(core.UUID{10}, "unique-vpc", "10.0.0.0/16", regionID, "vn-central")
	finder := NewVPCFinder(&fakeVpcLister{results: []networksdk.VPCSchema{vpc}}, projectID)

	name := "unique-vpc"
	res, err := finder.Resolve(context.Background(), VPCFilter{Name: &name})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Id != vpc.Id {
		t.Fatalf("expected resolved VPC %s, got %s", vpc.Id, res.Id)
	}
}

func TestVPCFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	finder := NewVPCFinder(&fakeVpcLister{results: nil}, core.UUID{9})
	name := "non-existent"
	_, err := finder.Resolve(context.Background(), VPCFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when no VPC matches")
	}
	if !strings.Contains(err.Error(), `VPC was not found matching criteria (name="non-existent")`) {
		t.Fatalf("expected not-found error naming the criteria, got %v", err)
	}
}

func TestVPCFinderResolveMultipleMatches(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{1}
	vpcs := []networksdk.VPCSchema{
		sampleVPC(core.UUID{10}, "dup-vpc", "10.0.0.0/16", regionID, "vn-central"),
		sampleVPC(core.UUID{11}, "dup-vpc", "10.1.0.0/16", regionID, "vn-central"),
	}
	finder := NewVPCFinder(&fakeVpcLister{results: vpcs}, core.UUID{9})
	name := "dup-vpc"
	_, err := finder.Resolve(context.Background(), VPCFilter{Name: &name})
	if err == nil {
		t.Fatal("expected error when multiple VPCs match")
	}
	for _, want := range []string{
		"is ambiguous",
		`id=` + vpcs[0].Id.String(),
		`id=` + vpcs[1].Id.String(),
		`cidr="10.0.0.0/16"`,
		`cidr="10.1.0.0/16"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected ambiguity error to contain %q, got %v", want, err)
		}
	}
}

func TestVPCFinderTrimsNameAndCIDRFilters(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	regionID := core.UUID{1}
	lister := &fakeVpcLister{results: []networksdk.VPCSchema{
		sampleVPC(core.UUID{10}, "vpc-a", "10.0.0.0/16", regionID, "vn-central"),
	}}

	name := "  vpc-a  "
	cidr := " 10.0.0.0/16 "
	matches, err := NewVPCFinder(lister, projectID).Find(context.Background(), VPCFilter{
		Name: &name,
		CIDR: &cidr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("expected the padded filters to match the exact VPC, got %#v", matches)
	}
	if lister.params.Name == nil || *lister.params.Name != "vpc-a" {
		t.Fatalf("expected trimmed name in list params, got %#v", lister.params.Name)
	}
}

func TestVPCFilterString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   VPCFilter
		expected string
	}{
		{
			name:     "empty filter",
			filter:   VPCFilter{},
			expected: "all",
		},
		{
			name: "single name filter",
			filter: VPCFilter{
				Name: new("my-vpc"),
			},
			expected: `name="my-vpc"`,
		},
		{
			name: "all criteria populated",
			filter: VPCFilter{
				ID:       &core.UUID{10},
				Name:     new("my-vpc"),
				CIDR:     new("10.0.0.0/16"),
				RegionID: &core.UUID{1},
			},
			expected: `id=0a000000-0000-0000-0000-000000000000, name="my-vpc", cidr="10.0.0.0/16", region_id=01000000-0000-0000-0000-000000000000`,
		},
		{
			name: "padded text is trimmed, as params and matches trim it",
			filter: VPCFilter{
				Name: new("  my-vpc  "),
				CIDR: new(" 10.0.0.0/16 "),
			},
			expected: `name="my-vpc", cidr="10.0.0.0/16"`,
		},
		{
			name: "blank text is unset, as params and matches ignore it",
			filter: VPCFilter{
				Name: new("   "),
				CIDR: new(""),
			},
			expected: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.filter.String(); got != tt.expected {
				t.Errorf("VPCFilter.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}
