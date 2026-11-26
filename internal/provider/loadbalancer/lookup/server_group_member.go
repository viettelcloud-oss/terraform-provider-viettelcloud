package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type ServerGroupMemberLister interface {
	ListServerGroupMembersIter(
		context.Context,
		core.UUID,
		loadbalancersdk.ListServerGroupMembersParams,
	) iter.Seq2[*loadbalancersdk.ServerGroupMemberSchema, error]
}

type ServerGroupMemberFilter struct {
	ID   *core.UUID
	Name *string
}

func (f ServerGroupMemberFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
	)
}

func (f ServerGroupMemberFilter) matches(member *loadbalancersdk.ServerGroupMemberSchema) bool {
	if member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		return false
	}
	if f.ID != nil && member.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && member.Name != name {
		return false
	}
	return true
}

func (f ServerGroupMemberFilter) params(projectID core.UUID) loadbalancersdk.ListServerGroupMembersParams {
	params := loadbalancersdk.ListServerGroupMembersParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type ServerGroupMemberFinder struct {
	client        ServerGroupMemberLister
	projectID     core.UUID
	serverGroupID core.UUID
}

var _ finder.Interface[ServerGroupMemberFilter, loadbalancersdk.ServerGroupMemberSchema] = (*ServerGroupMemberFinder)(nil)

func NewServerGroupMemberFinder(client ServerGroupMemberLister, projectID, serverGroupID core.UUID) *ServerGroupMemberFinder {
	return &ServerGroupMemberFinder{client: client, projectID: projectID, serverGroupID: serverGroupID}
}

func (f *ServerGroupMemberFinder) Find(ctx context.Context, filter ServerGroupMemberFilter) ([]loadbalancersdk.ServerGroupMemberSchema, error) {
	seq := f.client.ListServerGroupMembersIter(ctx, f.serverGroupID, filter.params(f.projectID))
	return finder.Collect(seq, "server group member", filter.String(), filter.matches)
}

func (f *ServerGroupMemberFinder) Resolve(ctx context.Context, filter ServerGroupMemberFilter) (loadbalancersdk.ServerGroupMemberSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return loadbalancersdk.ServerGroupMemberSchema{}, err
	}
	return finder.ExactlyOne(candidates, "server group member", filter.String(), serverGroupMemberSummary)
}

func serverGroupMemberSummary(member loadbalancersdk.ServerGroupMemberSchema) string {
	return fmt.Sprintf("id=%s (name=%q, address=%q, protocol_port=%d)", member.Id, member.Name, member.Address, member.ProtocolPort)
}
