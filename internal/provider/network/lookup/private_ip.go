package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type PrivateIPLister interface {
	ListPrivateIpsIter(
		context.Context,
		networksdk.ListPrivateIpsParams,
	) iter.Seq2[*networksdk.PrivateIPSchema, error]
}

type PrivateIPFilter struct {
	ID        *core.UUID
	IPAddress *string
	SubnetID  *core.UUID
	VPCID     *core.UUID
	Region    *string
}

func (f PrivateIPFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("ip_address", f.IPAddress),
		finder.Part("subnet_id", f.SubnetID),
		finder.Part("vpc_id", f.VPCID),
		finder.Text("region", f.Region),
	)
}

func (f PrivateIPFilter) matches(privateIP *networksdk.PrivateIPSchema) bool {
	if f.ID != nil && privateIP.Id != *f.ID {
		return false
	}
	if ipAddress := finder.Trim(f.IPAddress); ipAddress != "" {
		if current := core.NullableToPtr(privateIP.IpAddress); current == nil || *current != ipAddress {
			return false
		}
	}
	if f.SubnetID != nil && privateIP.Subnet.Id != *f.SubnetID {
		return false
	}
	if f.VPCID != nil && privateIP.Vpc.Id != *f.VPCID {
		return false
	}
	if region := finder.Trim(f.Region); region != "" && privateIP.Region.Name != region {
		return false
	}
	return true
}

func (f PrivateIPFilter) params(projectID core.UUID) networksdk.ListPrivateIpsParams {
	params := networksdk.ListPrivateIpsParams{ProjectID: projectID}
	if ipAddress := finder.Trim(f.IPAddress); ipAddress != "" {
		params.IpAddress = &ipAddress
	}
	if f.SubnetID != nil {
		params.SubnetId = f.SubnetID
	}
	if f.VPCID != nil {
		params.VpcId = f.VPCID
	}
	if region := finder.Trim(f.Region); region != "" {
		params.Region = &region
	}
	return params
}

type PrivateIPFinder struct {
	client    PrivateIPLister
	projectID core.UUID
}

var _ finder.Interface[PrivateIPFilter, networksdk.PrivateIPSchema] = (*PrivateIPFinder)(nil)

func NewPrivateIPFinder(client PrivateIPLister, projectID core.UUID) *PrivateIPFinder {
	return &PrivateIPFinder{client: client, projectID: projectID}
}

func (f *PrivateIPFinder) Find(ctx context.Context, filter PrivateIPFilter) ([]networksdk.PrivateIPSchema, error) {
	seq := f.client.ListPrivateIpsIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "private IP", filter.String(), filter.matches)
}

func (f *PrivateIPFinder) Resolve(ctx context.Context, filter PrivateIPFilter) (networksdk.PrivateIPSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return networksdk.PrivateIPSchema{}, err
	}
	return finder.ExactlyOne(candidates, "private IP", filter.String(), privateIPSummary)
}

func privateIPSummary(privateIP networksdk.PrivateIPSchema) string {
	ipAddress := ""
	if value := core.NullableToPtr(privateIP.IpAddress); value != nil {
		ipAddress = *value
	}
	return fmt.Sprintf(
		"id=%s (ip_address=%q, subnet_name=%q, vpc_name=%q, region=%q)",
		privateIP.Id,
		ipAddress,
		privateIP.Subnet.Name,
		privateIP.Vpc.Name,
		privateIP.Region.Name,
	)
}
