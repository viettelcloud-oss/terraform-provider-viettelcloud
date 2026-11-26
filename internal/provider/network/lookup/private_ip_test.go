package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type fakePrivateIPLister struct {
	results []networksdk.PrivateIPSchema
	err     error
	params  networksdk.ListPrivateIpsParams
}

func (f *fakePrivateIPLister) ListPrivateIpsIter(
	_ context.Context,
	params networksdk.ListPrivateIpsParams,
) iter.Seq2[*networksdk.PrivateIPSchema, error] {
	f.params = params
	return func(yield func(*networksdk.PrivateIPSchema, error) bool) {
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

func samplePrivateIP(
	id core.UUID,
	ipAddress string,
	subnetID core.UUID,
	subnetName string,
	vpcID core.UUID,
	vpcName string,
	region string,
) networksdk.PrivateIPSchema {
	return networksdk.PrivateIPSchema{
		Id:        id,
		IpAddress: core.NullableFromPtr(&ipAddress),
		Subnet:    networksdk.NestedSubnetSchema{Id: subnetID, Name: subnetName},
		Vpc:       networksdk.NestedVPCSchema{Id: vpcID, Name: vpcName},
		Region:    networksdk.NestedRegionSchema{Name: region},
	}
}

func TestPrivateIPFinderFindMatchesAllCriteria(t *testing.T) {
	t.Parallel()

	projectID := core.UUID{9}
	id := core.UUID{1}
	subnetID := core.UUID{2}
	vpcID := core.UUID{3}
	ipAddress := "10.0.1.10"
	subnetName := "application"
	vpcName := "production"
	region := "vn-central-1"
	lister := &fakePrivateIPLister{results: []networksdk.PrivateIPSchema{
		samplePrivateIP(id, ipAddress, subnetID, subnetName, vpcID, vpcName, region),
		samplePrivateIP(core.UUID{4}, "10.0.1.11", subnetID, subnetName, vpcID, vpcName, region),
	}}

	matches, err := NewPrivateIPFinder(lister, projectID).Find(context.Background(), PrivateIPFilter{
		ID: &id, IPAddress: &ipAddress, SubnetID: &subnetID, VPCID: &vpcID, Region: &region,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Id != id {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if lister.params.ProjectID != projectID || lister.params.IpAddress == nil || *lister.params.IpAddress != ipAddress ||
		lister.params.SubnetId == nil || *lister.params.SubnetId != subnetID ||
		lister.params.VpcId == nil || *lister.params.VpcId != vpcID ||
		lister.params.Region == nil || *lister.params.Region != region {
		t.Fatalf("unexpected list params: %#v", lister.params)
	}
}

func TestPrivateIPFinderRejectsEachMismatchedCriterion(t *testing.T) {
	t.Parallel()

	privateIP := samplePrivateIP(
		core.UUID{1}, "10.0.1.10", core.UUID{2}, "application", core.UUID{3}, "production", "vn-central-1",
	)
	otherID := core.UUID{4}
	otherSubnetID := core.UUID{5}
	otherVPCID := core.UUID{6}
	otherIPAddress := "10.0.1.11"
	otherRegion := "vn-south-1"
	tests := map[string]PrivateIPFilter{
		"id":         {ID: &otherID},
		"ip_address": {IPAddress: &otherIPAddress},
		"subnet_id":  {SubnetID: &otherSubnetID},
		"vpc_id":     {VPCID: &otherVPCID},
		"region":     {Region: &otherRegion},
	}

	for name, filter := range tests {
		t.Run(name, func(t *testing.T) {
			matches, err := NewPrivateIPFinder(
				&fakePrivateIPLister{results: []networksdk.PrivateIPSchema{privateIP}}, core.UUID{9},
			).Find(context.Background(), filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(matches) != 0 {
				t.Fatalf("expected mismatched %s filter to reject private IP", name)
			}
		})
	}
}

func TestPrivateIPFinderTrimsTextFilters(t *testing.T) {
	t.Parallel()

	lister := &fakePrivateIPLister{results: []networksdk.PrivateIPSchema{
		samplePrivateIP(core.UUID{1}, "10.0.1.10", core.UUID{2}, "application", core.UUID{3}, "production", "vn-central-1"),
	}}
	ipAddress := " 10.0.1.10 "
	region := " vn-central-1 "
	matches, err := NewPrivateIPFinder(lister, core.UUID{9}).Find(context.Background(), PrivateIPFilter{
		IPAddress: &ipAddress, Region: &region,
	})
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one trimmed match, got matches=%#v err=%v", matches, err)
	}
	if *lister.params.IpAddress != "10.0.1.10" || *lister.params.Region != "vn-central-1" {
		t.Fatalf("expected trimmed backend params, got %#v", lister.params)
	}
}

func TestPrivateIPFinderResolveCardinalityAndErrors(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("list failed")
	_, err := NewPrivateIPFinder(&fakePrivateIPLister{err: apiErr}, core.UUID{9}).Resolve(
		context.Background(), PrivateIPFilter{},
	)
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected list error, got %v", err)
	}

	ipAddress := "10.0.1.10"
	_, err = NewPrivateIPFinder(&fakePrivateIPLister{}, core.UUID{9}).Resolve(
		context.Background(), PrivateIPFilter{IPAddress: &ipAddress},
	)
	if err == nil || !strings.Contains(err.Error(), `private IP was not found matching criteria (ip_address="10.0.1.10")`) {
		t.Fatalf("expected not-found criteria error, got %v", err)
	}

	results := []networksdk.PrivateIPSchema{
		samplePrivateIP(core.UUID{1}, ipAddress, core.UUID{2}, "application", core.UUID{3}, "production", "vn-central-1"),
		samplePrivateIP(core.UUID{4}, ipAddress, core.UUID{5}, "database", core.UUID{3}, "production", "vn-central-1"),
	}
	_, err = NewPrivateIPFinder(&fakePrivateIPLister{results: results}, core.UUID{9}).Resolve(
		context.Background(), PrivateIPFilter{IPAddress: &ipAddress},
	)
	if err == nil || !strings.Contains(err.Error(), "is ambiguous") ||
		!strings.Contains(err.Error(), results[0].Id.String()) || !strings.Contains(err.Error(), results[1].Id.String()) {
		t.Fatalf("expected candidate-rich ambiguity error, got %v", err)
	}
}

func TestPrivateIPFilterString(t *testing.T) {
	t.Parallel()

	id := core.UUID{1}
	subnetID := core.UUID{2}
	vpcID := core.UUID{3}
	ipAddress := " 10.0.1.10 "
	region := " vn-central-1 "
	filter := PrivateIPFilter{
		ID: &id, IPAddress: &ipAddress, SubnetID: &subnetID, VPCID: &vpcID, Region: &region,
	}
	want := `id=01000000-0000-0000-0000-000000000000, ip_address="10.0.1.10", subnet_id=02000000-0000-0000-0000-000000000000, vpc_id=03000000-0000-0000-0000-000000000000, region="vn-central-1"`
	if got := filter.String(); got != want {
		t.Fatalf("PrivateIPFilter.String() = %q, want %q", got, want)
	}
}
