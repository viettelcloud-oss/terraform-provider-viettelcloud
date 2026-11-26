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

type fakeElasticIPLister struct {
	results   []networksdk.ElasticIPSchema
	err       error
	params    networksdk.ListElasticIpsParams
	callCount int
}

func (f *fakeElasticIPLister) ListElasticIpsIter(
	_ context.Context,
	params networksdk.ListElasticIpsParams,
) iter.Seq2[*networksdk.ElasticIPSchema, error] {
	f.callCount++
	f.params = params
	return func(yield func(*networksdk.ElasticIPSchema, error) bool) {
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

func sampleElasticIP(
	id core.UUID,
	ipAddress string,
	regionName string,
	status networksdk.ElasticIPStatus,
	attached bool,
) networksdk.ElasticIPSchema {
	description := "eip " + ipAddress
	eip := networksdk.ElasticIPSchema{
		Id:          id,
		Description: &description,
		DisplayName: ipAddress,
		EnableIpv4:  true,
		IpAddress:   core.NullableFromPtr(&ipAddress),
		Status:      core.NullableFromPtr(&status),
		Region: networksdk.NestedRegionSchema{
			Id:   core.UUID{9},
			Name: regionName,
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if attached {
		eip.Server.Set(networksdk.NestedServerSchema{Id: core.UUID{8}, Name: "server"})
	}
	return eip
}

func TestElasticIPFinderFindPassesFiltersToBackend(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{7}
	lister := &fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		sampleElasticIP(core.UUID{10}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, false),
		sampleElasticIP(core.UUID{11}, "203.0.113.11", "vn-central", networksdk.ElasticIPStatusActive, false),
	}}

	address := "  203.0.113.10  "
	region := "vn-central"
	status := string(networksdk.ElasticIPStatusActive)
	available := true
	matches, err := NewElasticIPFinder(lister, projectID).Find(context.Background(), ElasticIPFilter{
		IPAddress: &address,
		Region:    &region,
		Status:    &status,
		Available: &available,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID ||
		lister.params.IpAddress == nil || *lister.params.IpAddress != "203.0.113.10" ||
		lister.params.RegionName == nil || *lister.params.RegionName != "vn-central" ||
		lister.params.Status == nil || *lister.params.Status != networksdk.ElasticIPStatusActive ||
		lister.params.Available == nil || !*lister.params.Available {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestElasticIPFinderFindPassesEveryCriterionToBackend(t *testing.T) {
	t.Parallel()

	lister := &fakeElasticIPLister{}
	address := " 203.0.113.10 "
	ipv6Address := " 2001:0DB8:0000:0000:0000:0000:0000:0010 "
	region := " vn-central "
	status := " active "
	available := false
	id := core.UUID{10}
	if _, err := NewElasticIPFinder(lister, core.UUID{7}).Find(context.Background(), ElasticIPFilter{
		ID:          &id,
		IPAddress:   &address,
		IPv6Address: &ipv6Address,
		Status:      &status,
		Region:      &region,
		Available:   &available,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	params := lister.params
	if params.IpAddress == nil || *params.IpAddress != "203.0.113.10" ||
		params.Ipv6Address == nil || *params.Ipv6Address != "2001:db8::10" ||
		params.Status == nil || *params.Status != networksdk.ElasticIPStatusActive ||
		params.RegionName == nil || *params.RegionName != "vn-central" ||
		params.Available == nil || *params.Available {
		t.Fatalf("unexpected list params: %#v", params)
	}
}

func TestElasticIPFinderFindMatchesEveryCriterion(t *testing.T) {
	t.Parallel()

	ipv6 := "2001:db8::10"
	configuredIPv6 := "2001:0DB8:0000:0000:0000:0000:0000:0010"
	wanted := sampleElasticIP(core.UUID{10}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, false)
	wanted.Ipv6Address = core.NullableFromPtr(&ipv6)

	lister := &fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		wanted,
		sampleElasticIP(core.UUID{11}, "203.0.113.10", "vn-north", networksdk.ElasticIPStatusActive, false),
		sampleElasticIP(core.UUID{12}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusDown, false),
		sampleElasticIP(core.UUID{13}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, true),
	}}

	id := core.UUID{10}
	address := "203.0.113.10"
	region := "vn-central"
	status := string(networksdk.ElasticIPStatusActive)
	available := true
	matches, err := NewElasticIPFinder(lister, core.UUID{7}).Find(context.Background(), ElasticIPFilter{
		ID:          &id,
		IPAddress:   &address,
		IPv6Address: &configuredIPv6,
		Status:      &status,
		Region:      &region,
		Available:   &available,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != id {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestElasticIPFinderFindMatchesAttachedOnly(t *testing.T) {
	t.Parallel()

	lister := &fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		sampleElasticIP(core.UUID{10}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, false),
		sampleElasticIP(core.UUID{11}, "203.0.113.11", "vn-central", networksdk.ElasticIPStatusActive, true),
	}}

	available := false
	matches, err := NewElasticIPFinder(lister, core.UUID{7}).Find(context.Background(), ElasticIPFilter{
		Available: &available,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{11}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestElasticIPFinderFindTreatsNullServerAsAvailable(t *testing.T) {
	t.Parallel()

	eip := sampleElasticIP(core.UUID{10}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, false)
	eip.Server.SetNull()
	lister := &fakeElasticIPLister{results: []networksdk.ElasticIPSchema{eip}}

	available := true
	matches, err := NewElasticIPFinder(lister, core.UUID{7}).Find(context.Background(), ElasticIPFilter{
		Available: &available,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != (core.UUID{10}) {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func TestElasticIPFinderFindIgnoresBlankCriteria(t *testing.T) {
	t.Parallel()

	lister := &fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		sampleElasticIP(core.UUID{10}, "203.0.113.10", "vn-central", networksdk.ElasticIPStatusActive, false),
	}}

	blank := "   "
	matches, err := NewElasticIPFinder(lister, core.UUID{7}).Find(context.Background(), ElasticIPFilter{
		IPAddress: &blank,
		Region:    &blank,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected blank criteria to be unset, got %#v", matches)
	}
	if lister.params.IpAddress != nil || lister.params.RegionName != nil {
		t.Fatalf("expected blank criteria to be omitted from list params: %#v", lister.params)
	}
}

func TestElasticIPFinderResolveCardinality(t *testing.T) {
	t.Parallel()

	address := "203.0.113.10"
	finder := NewElasticIPFinder(&fakeElasticIPLister{}, core.UUID{7})
	if _, err := finder.Resolve(context.Background(), ElasticIPFilter{IPAddress: &address}); err == nil ||
		!strings.Contains(err.Error(), "was not found") {
		t.Fatalf("expected a not-found error, got %v", err)
	}

	ambiguous := NewElasticIPFinder(&fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		sampleElasticIP(core.UUID{10}, address, "vn-central", networksdk.ElasticIPStatusActive, false),
		sampleElasticIP(core.UUID{11}, address, "vn-north", networksdk.ElasticIPStatusActive, false),
	}}, core.UUID{7})
	_, err := ambiguous.Resolve(context.Background(), ElasticIPFilter{IPAddress: &address})
	if err == nil || !strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("expected an ambiguity error, got %v", err)
	}
	if !strings.Contains(err.Error(), `region="vn-central"`) || !strings.Contains(err.Error(), `region="vn-north"`) {
		t.Fatalf("expected both candidates to be summarized, got %v", err)
	}

	single := NewElasticIPFinder(&fakeElasticIPLister{results: []networksdk.ElasticIPSchema{
		sampleElasticIP(core.UUID{10}, address, "vn-central", networksdk.ElasticIPStatusActive, false),
	}}, core.UUID{7})
	resolved, err := single.Resolve(context.Background(), ElasticIPFilter{IPAddress: &address})
	if err != nil || resolved.Id != (core.UUID{10}) {
		t.Fatalf("expected the single candidate, got %#v error=%v", resolved, err)
	}
}

func TestElasticIPFinderFindReportsSDKErrors(t *testing.T) {
	t.Parallel()

	listErr := errors.New("backend unavailable")
	finder := NewElasticIPFinder(&fakeElasticIPLister{err: listErr}, core.UUID{7})

	address := "203.0.113.10"
	ipv6Address := "2001:0DB8:0000:0000:0000:0000:0000:0010"
	_, err := finder.Find(context.Background(), ElasticIPFilter{IPAddress: &address, IPv6Address: &ipv6Address})
	if !errors.Is(err, listErr) {
		t.Fatalf("expected the SDK error to be wrapped, got %v", err)
	}
	if !strings.Contains(err.Error(), `ip_address="203.0.113.10"`) {
		t.Fatalf("expected the criteria in the error, got %v", err)
	}
	if !strings.Contains(err.Error(), `ipv6_address="2001:db8::10"`) {
		t.Fatalf("expected canonical IPv6 criteria in the error, got %v", err)
	}
}

func TestElasticIPFilterString(t *testing.T) {
	t.Parallel()

	if got := (ElasticIPFilter{}).String(); got != "all" {
		t.Fatalf("expected an empty filter to describe every Elastic IP, got %q", got)
	}

	id := core.UUID{10}
	address := "203.0.113.10"
	ipv6Address := " 2001:0DB8:0000:0000:0000:0000:0000:0010 "
	available := true
	got := ElasticIPFilter{ID: &id, IPAddress: &address, IPv6Address: &ipv6Address, Available: &available}.String()
	want := "id=" + id.String() +
		`, ip_address="203.0.113.10", ipv6_address="2001:0DB8:0000:0000:0000:0000:0000:0010", available=true`
	if got != want {
		t.Fatalf("expected description %q, got %q", want, got)
	}
}
