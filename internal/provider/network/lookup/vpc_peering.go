package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

type VPCPeeringLister interface {
	ListVpcPeeringsIter(
		context.Context,
		networksdk.ListVpcPeeringsParams,
	) iter.Seq2[*networksdk.VPCPeeringSchema, error]
}

// VPCPeeringFilter selects VPC peerings by exact criteria. The pair criteria
// are directional: requester A and accepter B never match the peering from B
// to A.
type VPCPeeringFilter struct {
	ID             *core.UUID
	Name           *string
	RequesterVPCID *core.UUID
	AccepterVPCID  *core.UUID
	Status         *string
}

func (f VPCPeeringFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
		finder.Part("requester_vpc_id", f.RequesterVPCID),
		finder.Part("accepter_vpc_id", f.AccepterVPCID),
		finder.Text("status", f.Status),
	)
}

// matches keeps only pending and active peerings when no criterion names one
// peering or a status. The backend keeps at most one pending or active peering
// between two VPCs, but rejected and failed peerings of the same pair stay in
// the list and would otherwise make a pair lookup ambiguous.
func (f VPCPeeringFilter) matches(peering *networksdk.VPCPeeringSchema) bool {
	if f.ID != nil && peering.Id != *f.ID {
		return false
	}
	name := finder.Trim(f.Name)
	if name != "" && peering.Name != name {
		return false
	}
	if f.RequesterVPCID != nil && peering.RequesterVpc.Id != *f.RequesterVPCID {
		return false
	}
	if f.AccepterVPCID != nil && peering.AccepterVpc.Id != *f.AccepterVPCID {
		return false
	}
	status := finder.Trim(f.Status)
	if status != "" {
		return string(peering.Status) == status
	}
	if f.ID == nil && name == "" {
		return peering.Status == networksdk.VPCPeeringStatusPending ||
			peering.Status == networksdk.VPCPeeringStatusActive
	}
	return true
}

func (f VPCPeeringFilter) params(projectID core.UUID) networksdk.ListVpcPeeringsParams {
	params := networksdk.ListVpcPeeringsParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if f.RequesterVPCID != nil {
		params.RequesterVpcId = f.RequesterVPCID
	}
	if f.AccepterVPCID != nil {
		params.AccepterVpcId = f.AccepterVPCID
	}
	if status := finder.Trim(f.Status); status != "" {
		value := networksdk.VPCPeeringStatus(status)
		params.Status = &value
	}
	return params
}

type VPCPeeringFinder struct {
	client    VPCPeeringLister
	projectID core.UUID
}

var _ finder.Interface[VPCPeeringFilter, networksdk.VPCPeeringSchema] = (*VPCPeeringFinder)(nil)

func NewVPCPeeringFinder(client VPCPeeringLister, projectID core.UUID) *VPCPeeringFinder {
	return &VPCPeeringFinder{client: client, projectID: projectID}
}

func (f *VPCPeeringFinder) Find(ctx context.Context, filter VPCPeeringFilter) ([]networksdk.VPCPeeringSchema, error) {
	seq := f.client.ListVpcPeeringsIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "VPC peering", filter.String(), filter.matches)
}

func (f *VPCPeeringFinder) Resolve(ctx context.Context, filter VPCPeeringFilter) (networksdk.VPCPeeringSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return networksdk.VPCPeeringSchema{}, err
	}
	return finder.ExactlyOne(candidates, "VPC peering", filter.String(), vpcPeeringSummary)
}

func vpcPeeringSummary(peering networksdk.VPCPeeringSchema) string {
	return fmt.Sprintf(
		"id=%s (name=%q, requester_vpc_id=%s, accepter_vpc_id=%s, status=%q)",
		peering.Id,
		peering.Name,
		peering.RequesterVpc.Id,
		peering.AccepterVpc.Id,
		peering.Status,
	)
}
