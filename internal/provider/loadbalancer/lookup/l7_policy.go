package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

type L7PolicyLister interface {
	ListL7policiesIter(
		context.Context,
		loadbalancersdk.ListL7policiesParams,
	) iter.Seq2[*loadbalancersdk.L7PolicySchema, error]
}

type L7PolicyFilter struct {
	ID         *core.UUID
	Name       *string
	ListenerID *core.UUID
}

func (f L7PolicyFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
		finder.Part("listener_id", f.ListenerID),
	)
}

func (f L7PolicyFilter) matches(policy *loadbalancersdk.L7PolicySchema) bool {
	if policy.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		return false
	}
	if f.ID != nil && policy.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && policy.Name.GetOrEmpty() != name {
		return false
	}
	if f.ListenerID != nil && policy.ListenerId != *f.ListenerID {
		return false
	}
	return true
}

func (f L7PolicyFilter) params(projectID core.UUID) loadbalancersdk.ListL7policiesParams {
	params := loadbalancersdk.ListL7policiesParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if f.ListenerID != nil {
		params.ListenerId = f.ListenerID
	}
	return params
}

type L7PolicyFinder struct {
	client    L7PolicyLister
	projectID core.UUID
}

var _ finder.Interface[L7PolicyFilter, loadbalancersdk.L7PolicySchema] = (*L7PolicyFinder)(nil)

func NewL7PolicyFinder(client L7PolicyLister, projectID core.UUID) *L7PolicyFinder {
	return &L7PolicyFinder{client: client, projectID: projectID}
}

func (f *L7PolicyFinder) Find(ctx context.Context, filter L7PolicyFilter) ([]loadbalancersdk.L7PolicySchema, error) {
	seq := f.client.ListL7policiesIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "L7 policy", filter.String(), filter.matches)
}

func (f *L7PolicyFinder) Resolve(ctx context.Context, filter L7PolicyFilter) (loadbalancersdk.L7PolicySchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return loadbalancersdk.L7PolicySchema{}, err
	}
	return finder.ExactlyOne(candidates, "L7 policy", filter.String(), l7PolicySummary)
}

func l7PolicySummary(policy loadbalancersdk.L7PolicySchema) string {
	return fmt.Sprintf(
		"id=%s (name=%q, listener_id=%s, action=%s)",
		policy.Id,
		policy.Name.GetOrEmpty(),
		policy.ListenerId,
		policy.Action,
	)
}
