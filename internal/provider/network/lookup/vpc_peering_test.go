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

type fakeVPCPeeringLister struct {
	results []networksdk.VPCPeeringSchema
	err     error
	params  networksdk.ListVpcPeeringsParams
}

func (f *fakeVPCPeeringLister) ListVpcPeeringsIter(
	_ context.Context,
	params networksdk.ListVpcPeeringsParams,
) iter.Seq2[*networksdk.VPCPeeringSchema, error] {
	f.params = params
	return func(yield func(*networksdk.VPCPeeringSchema, error) bool) {
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

var (
	peeringRequesterVPCID = core.UUID{8}
	peeringAccepterVPCID  = core.UUID{7}
)

func samplePeering(id core.UUID, status networksdk.VPCPeeringStatus) networksdk.VPCPeeringSchema {
	return networksdk.VPCPeeringSchema{
		Id:             id,
		Name:           "peer-" + id.String(),
		RequesterVpc:   networksdk.NestedPeeredVPCSchema{Id: peeringRequesterVPCID, Name: "requester"},
		AccepterVpc:    networksdk.NestedPeeredVPCSchema{Id: peeringAccepterVPCID, Name: "accepter"},
		Status:         status,
		VpcPeeringType: networksdk.VPCPeeringTypeIntraRegion,
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestVPCPeeringFinderFindMatchesAllCriteria(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	matchingID := core.UUID{1}
	matching := samplePeering(matchingID, networksdk.VPCPeeringStatusActive)
	lister := &fakeVPCPeeringLister{results: []networksdk.VPCPeeringSchema{
		matching,
		samplePeering(core.UUID{2}, networksdk.VPCPeeringStatusActive),
	}}
	name := matching.Name
	status := "active"

	matches, err := NewVPCPeeringFinder(lister, projectID).Find(context.Background(), VPCPeeringFilter{
		ID:             &matchingID,
		Name:           &name,
		RequesterVPCID: &peeringRequesterVPCID,
		AccepterVPCID:  &peeringAccepterVPCID,
		Status:         &status,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != matchingID {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != name ||
		lister.params.RequesterVpcId == nil || *lister.params.RequesterVpcId != peeringRequesterVPCID ||
		lister.params.AccepterVpcId == nil || *lister.params.AccepterVpcId != peeringAccepterVPCID ||
		lister.params.Status == nil || *lister.params.Status != networksdk.VPCPeeringStatusActive {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestVPCPeeringFinderFindRejectsEachMismatchedCriterion(t *testing.T) {
	t.Parallel()

	peering := samplePeering(core.UUID{1}, networksdk.VPCPeeringStatusActive)
	otherID := core.UUID{2}
	otherVPCID := core.UUID{3}
	otherName := "peer-other"
	otherStatus := "pending"
	tests := map[string]VPCPeeringFilter{
		"id":               {ID: &otherID},
		"name":             {Name: &otherName},
		"requester_vpc_id": {RequesterVPCID: &otherVPCID},
		"accepter_vpc_id":  {AccepterVPCID: &otherVPCID},
		"status":           {Status: &otherStatus},
		// The pair criteria are directional.
		"reversed pair": {RequesterVPCID: &peeringAccepterVPCID, AccepterVPCID: &peeringRequesterVPCID},
	}

	for name, filter := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lister := &fakeVPCPeeringLister{results: []networksdk.VPCPeeringSchema{peering}}
			matches, err := NewVPCPeeringFinder(lister, core.UUID{9}).Find(context.Background(), filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(matches) != 0 {
				t.Fatalf("expected mismatched %s filter to reject peering, got %#v", name, matches)
			}
		})
	}
}

func TestVPCPeeringFinderStatusScope(t *testing.T) {
	t.Parallel()

	active := samplePeering(core.UUID{1}, networksdk.VPCPeeringStatusActive)
	pending := samplePeering(core.UUID{2}, networksdk.VPCPeeringStatusPending)
	rejected := samplePeering(core.UUID{3}, networksdk.VPCPeeringStatusRejected)
	failed := samplePeering(core.UUID{4}, networksdk.VPCPeeringStatusError)
	all := []networksdk.VPCPeeringSchema{active, pending, rejected, failed}

	rejectedName := rejected.Name
	rejectedStatus := "rejected"
	activeStatus := " active "
	tests := map[string]struct {
		filter VPCPeeringFilter
		want   []core.UUID
	}{
		"pair without status keeps pending and active": {
			filter: VPCPeeringFilter{RequesterVPCID: &peeringRequesterVPCID, AccepterVPCID: &peeringAccepterVPCID},
			want:   []core.UUID{active.Id, pending.Id},
		},
		"pair with status uses that status": {
			filter: VPCPeeringFilter{RequesterVPCID: &peeringRequesterVPCID, AccepterVPCID: &peeringAccepterVPCID, Status: &activeStatus},
			want:   []core.UUID{active.Id},
		},
		"rejected status finds a rejected peering": {
			filter: VPCPeeringFilter{Status: &rejectedStatus},
			want:   []core.UUID{rejected.Id},
		},
		"name finds a rejected peering": {
			filter: VPCPeeringFilter{Name: &rejectedName},
			want:   []core.UUID{rejected.Id},
		},
		"id finds a failed peering": {
			filter: VPCPeeringFilter{ID: &failed.Id, RequesterVPCID: &peeringRequesterVPCID},
			want:   []core.UUID{failed.Id},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lister := &fakeVPCPeeringLister{results: all}
			matches, err := NewVPCPeeringFinder(lister, core.UUID{9}).Find(context.Background(), test.filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make([]core.UUID, 0, len(matches))
			for _, match := range matches {
				got = append(got, match.Id)
			}
			if len(got) != len(test.want) {
				t.Fatalf("matches = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("matches = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestVPCPeeringFinderTrimsTextFilters(t *testing.T) {
	t.Parallel()

	peering := samplePeering(core.UUID{1}, networksdk.VPCPeeringStatusActive)
	lister := &fakeVPCPeeringLister{results: []networksdk.VPCPeeringSchema{peering}}
	name := "  " + peering.Name + "  "
	status := " active "

	matches, err := NewVPCPeeringFinder(lister, core.UUID{9}).Find(context.Background(), VPCPeeringFilter{
		Name: &name, Status: &status,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one match, got %#v", matches)
	}
	if *lister.params.Name != peering.Name || *lister.params.Status != networksdk.VPCPeeringStatusActive {
		t.Fatalf("expected trimmed list params, got %#v", lister.params)
	}
}

func TestVPCPeeringFinderOmitsBlankParams(t *testing.T) {
	t.Parallel()

	lister := &fakeVPCPeeringLister{}
	blank := " "
	if _, err := NewVPCPeeringFinder(lister, core.UUID{9}).Find(context.Background(), VPCPeeringFilter{
		Name: &blank, Status: &blank,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lister.params.Name != nil || lister.params.Status != nil ||
		lister.params.RequesterVpcId != nil || lister.params.AccepterVpcId != nil {
		t.Fatalf("expected no filter params, got %#v", lister.params)
	}
}

func TestVPCPeeringFinderReturnsListError(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	peeringFinder := NewVPCPeeringFinder(&fakeVPCPeeringLister{err: apiErr}, core.UUID{9})
	if _, err := peeringFinder.Find(context.Background(), VPCPeeringFilter{}); !errors.Is(err, apiErr) {
		t.Fatalf("expected list error, got %v", err)
	}
	if _, err := peeringFinder.Resolve(context.Background(), VPCPeeringFilter{}); !errors.Is(err, apiErr) {
		t.Fatalf("expected resolve to return list error, got %v", err)
	}
}

func TestVPCPeeringFinderCollectsEveryPage(t *testing.T) {
	t.Parallel()

	// The fake yields every page in one sequence; a later page holds the match.
	results := make([]networksdk.VPCPeeringSchema, 0, 101)
	for i := range 100 {
		results = append(results, samplePeering(core.UUID{byte(i + 1)}, networksdk.VPCPeeringStatusRejected))
	}
	last := samplePeering(core.UUID{0xff}, networksdk.VPCPeeringStatusActive)
	results = append(results, last)

	result, err := NewVPCPeeringFinder(&fakeVPCPeeringLister{results: results}, core.UUID{9}).Resolve(
		context.Background(),
		VPCPeeringFilter{RequesterVPCID: &peeringRequesterVPCID, AccepterVPCID: &peeringAccepterVPCID},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Id != last.Id {
		t.Fatalf("resolved %s, want %s", result.Id, last.Id)
	}
}

func TestVPCPeeringFinderResolveZeroMatches(t *testing.T) {
	t.Parallel()

	name := "peer-missing"
	_, err := NewVPCPeeringFinder(&fakeVPCPeeringLister{}, core.UUID{9}).Resolve(
		context.Background(),
		VPCPeeringFilter{Name: &name},
	)
	if err == nil || !strings.Contains(err.Error(), `VPC peering was not found matching criteria (name="peer-missing")`) {
		t.Fatalf("expected not-found error naming the criteria, got %v", err)
	}
}

func TestVPCPeeringFinderResolveMultipleMatches(t *testing.T) {
	t.Parallel()

	peerings := []networksdk.VPCPeeringSchema{
		samplePeering(core.UUID{1}, networksdk.VPCPeeringStatusActive),
		samplePeering(core.UUID{2}, networksdk.VPCPeeringStatusPending),
	}
	_, err := NewVPCPeeringFinder(&fakeVPCPeeringLister{results: peerings}, core.UUID{9}).Resolve(
		context.Background(),
		VPCPeeringFilter{RequesterVPCID: &peeringRequesterVPCID},
	)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	for _, want := range []string{
		"is ambiguous",
		peerings[0].Id.String(),
		peerings[1].Id.String(),
		`status="active"`,
		`status="pending"`,
		"requester_vpc_id=" + peeringRequesterVPCID.String(),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected ambiguity error to contain %q, got %v", want, err)
		}
	}
}

func TestVPCPeeringFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{1}
	name := " peer-a "
	status := " active "
	filter := VPCPeeringFilter{
		ID: &id, Name: &name, RequesterVPCID: &peeringRequesterVPCID, AccepterVPCID: &peeringAccepterVPCID, Status: &status,
	}
	want := `id=01000000-0000-0000-0000-000000000000, name="peer-a", requester_vpc_id=08000000-0000-0000-0000-000000000000, accepter_vpc_id=07000000-0000-0000-0000-000000000000, status="active"`
	if got := filter.String(); got != want {
		t.Fatalf("VPCPeeringFilter.String() = %q, want %q", got, want)
	}
	if got := (VPCPeeringFilter{}).String(); got != "all" {
		t.Fatalf("empty filter description = %q, want all", got)
	}
}
