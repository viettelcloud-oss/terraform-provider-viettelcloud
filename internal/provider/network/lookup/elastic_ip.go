package lookup

import (
	"context"
	"fmt"
	"iter"
	"net/netip"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type ElasticIPLister interface {
	ListElasticIpsIter(
		context.Context,
		networksdk.ListElasticIpsParams,
	) iter.Seq2[*networksdk.ElasticIPSchema, error]
}

type ElasticIPFilter struct {
	ID          *core.UUID
	IPAddress   *string
	IPv6Address *string
	Status      *string
	Region      *string
	Available   *bool
}

func (f ElasticIPFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("ip_address", f.IPAddress),
		finder.Text("ipv6_address", f.IPv6Address),
		finder.Text("status", f.Status),
		finder.Text("region", f.Region),
		finder.Part("available", f.Available),
	)
}

func (f ElasticIPFilter) matches(eip *networksdk.ElasticIPSchema) bool {
	if f.ID != nil && eip.Id != *f.ID {
		return false
	}
	if address := finder.Trim(f.IPAddress); address != "" {
		if ipAddress := core.NullableToPtr(eip.IpAddress); ipAddress == nil || *ipAddress != address {
			return false
		}
	}
	if address := finder.Trim(f.IPv6Address); address != "" {
		ipv6Address := core.NullableToPtr(eip.Ipv6Address)
		if ipv6Address == nil {
			return false
		}
		candidate, _ := NormalizeElasticIPIPv6Address(*ipv6Address)
		if candidate != address {
			return false
		}
	}
	if status := finder.Trim(f.Status); status != "" {
		if current := core.NullableToPtr(eip.Status); current == nil || string(*current) != status {
			return false
		}
	}
	if region := finder.Trim(f.Region); region != "" && eip.Region.Name != region {
		return false
	}
	// The response carries no availability field: an Elastic IP is available
	// exactly while no server holds it.
	if f.Available != nil && *f.Available == (core.NullableToPtr(eip.Server) != nil) {
		return false
	}
	return true
}

func (f ElasticIPFilter) params(projectID core.UUID) networksdk.ListElasticIpsParams {
	params := networksdk.ListElasticIpsParams{ProjectID: projectID}
	if address := finder.Trim(f.IPAddress); address != "" {
		params.IpAddress = &address
	}
	if address := finder.Trim(f.IPv6Address); address != "" {
		params.Ipv6Address = &address
	}
	if status := finder.Trim(f.Status); status != "" {
		value := networksdk.ElasticIPStatus(status)
		params.Status = &value
	}
	if region := finder.Trim(f.Region); region != "" {
		params.RegionName = &region
	}
	if f.Available != nil {
		params.Available = f.Available
	}
	return params
}

func NormalizeElasticIPIPv6Address(value string) (string, bool) {
	value = strings.TrimSpace(value)
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is6() || address.Zone() != "" {
		return value, false
	}
	return address.String(), true
}

type ElasticIPFinder struct {
	client    ElasticIPLister
	projectID core.UUID
}

var _ finder.Interface[ElasticIPFilter, networksdk.ElasticIPSchema] = (*ElasticIPFinder)(nil)

func NewElasticIPFinder(client ElasticIPLister, projectID core.UUID) *ElasticIPFinder {
	return &ElasticIPFinder{client: client, projectID: projectID}
}

func (f *ElasticIPFinder) Find(ctx context.Context, filter ElasticIPFilter) ([]networksdk.ElasticIPSchema, error) {
	if filter.IPv6Address != nil {
		address, _ := NormalizeElasticIPIPv6Address(*filter.IPv6Address)
		filter.IPv6Address = &address
	}
	seq := f.client.ListElasticIpsIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "Elastic IP", filter.String(), filter.matches)
}

func (f *ElasticIPFinder) Resolve(ctx context.Context, filter ElasticIPFilter) (networksdk.ElasticIPSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return networksdk.ElasticIPSchema{}, err
	}
	return finder.ExactlyOne(candidates, "Elastic IP", filter.String(), elasticIPSummary)
}

func elasticIPSummary(eip networksdk.ElasticIPSchema) string {
	address := ""
	if value := core.NullableToPtr(eip.IpAddress); value != nil {
		address = *value
	}
	ipv6Address := ""
	if value := core.NullableToPtr(eip.Ipv6Address); value != nil {
		ipv6Address = *value
	}
	return fmt.Sprintf(
		"id=%s (ip_address=%q, ipv6_address=%q, region=%q)",
		eip.Id,
		address,
		ipv6Address,
		eip.Region.Name,
	)
}
