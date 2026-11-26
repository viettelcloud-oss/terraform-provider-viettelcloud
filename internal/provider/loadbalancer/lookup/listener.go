package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type ListenerLister interface {
	ListListenersIter(
		context.Context,
		core.UUID,
		loadbalancersdk.ListListenersParams,
	) iter.Seq2[*loadbalancersdk.ListenerSchema, error]
}

type ListenerFilter struct {
	ID   *core.UUID
	Name *string
}

func (f ListenerFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
	)
}

func (f ListenerFilter) matches(listener *loadbalancersdk.ListenerSchema) bool {
	if listener.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		return false
	}
	if f.ID != nil && listener.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && listener.Name != name {
		return false
	}
	return true
}

func (f ListenerFilter) params(projectID core.UUID) loadbalancersdk.ListListenersParams {
	params := loadbalancersdk.ListListenersParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type ListenerFinder struct {
	client         ListenerLister
	projectID      core.UUID
	loadBalancerID core.UUID
}

var _ finder.Interface[ListenerFilter, loadbalancersdk.ListenerSchema] = (*ListenerFinder)(nil)

func NewListenerFinder(client ListenerLister, projectID core.UUID, loadBalancerID core.UUID) *ListenerFinder {
	return &ListenerFinder{
		client:         client,
		projectID:      projectID,
		loadBalancerID: loadBalancerID,
	}
}

func (f *ListenerFinder) Find(ctx context.Context, filter ListenerFilter) ([]loadbalancersdk.ListenerSchema, error) {
	seq := f.client.ListListenersIter(ctx, f.loadBalancerID, filter.params(f.projectID))
	return finder.Collect(seq, "Listener", filter.String(), filter.matches)
}

func (f *ListenerFinder) Resolve(ctx context.Context, filter ListenerFilter) (loadbalancersdk.ListenerSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return loadbalancersdk.ListenerSchema{}, err
	}
	return finder.ExactlyOne(candidates, "Listener", filter.String(), listenerSummary)
}

func listenerSummary(listener loadbalancersdk.ListenerSchema) string {
	return fmt.Sprintf("id=%s (name=%q, protocol=%s, port=%d)", listener.Id, listener.Name, listener.Protocol, listener.ProtocolPort)
}
