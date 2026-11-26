package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type ServerGroupLister interface {
	ListServerGroupsIter(
		context.Context,
		core.UUID,
		loadbalancersdk.ListServerGroupsParams,
	) iter.Seq2[*loadbalancersdk.ServerGroupSchema, error]
}

type ServerGroupFilter struct {
	ID   *core.UUID
	Name *string
}

func (f ServerGroupFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
	)
}

func (f ServerGroupFilter) matches(sg *loadbalancersdk.ServerGroupSchema) bool {
	if sg.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		return false
	}
	if f.ID != nil && sg.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && sg.Name != name {
		return false
	}
	return true
}

func (f ServerGroupFilter) params(projectID core.UUID) loadbalancersdk.ListServerGroupsParams {
	params := loadbalancersdk.ListServerGroupsParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type ServerGroupFinder struct {
	client         ServerGroupLister
	projectID      core.UUID
	loadBalancerID core.UUID
}

var _ finder.Interface[ServerGroupFilter, loadbalancersdk.ServerGroupSchema] = (*ServerGroupFinder)(nil)

func NewServerGroupFinder(client ServerGroupLister, projectID, loadBalancerID core.UUID) *ServerGroupFinder {
	return &ServerGroupFinder{client: client, projectID: projectID, loadBalancerID: loadBalancerID}
}

func (f *ServerGroupFinder) Find(ctx context.Context, filter ServerGroupFilter) ([]loadbalancersdk.ServerGroupSchema, error) {
	seq := f.client.ListServerGroupsIter(ctx, f.loadBalancerID, filter.params(f.projectID))
	return finder.Collect(seq, "server group", filter.String(), filter.matches)
}

func (f *ServerGroupFinder) Resolve(ctx context.Context, filter ServerGroupFilter) (loadbalancersdk.ServerGroupSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return loadbalancersdk.ServerGroupSchema{}, err
	}
	return finder.ExactlyOne(candidates, "server group", filter.String(), serverGroupSummary)
}

func serverGroupSummary(sg loadbalancersdk.ServerGroupSchema) string {
	return fmt.Sprintf("id=%s (name=%q, protocol=%q)", sg.Id, sg.Name, sg.Protocol)
}
