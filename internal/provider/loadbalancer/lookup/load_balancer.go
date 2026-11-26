package lookup

import (
	"context"
	"fmt"
	"iter"
	"net/netip"

	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// LoadBalancerLister is the part of the SDK client that the finder needs.
type LoadBalancerLister interface {
	ListLoadBalancersIter(
		ctx context.Context,
		params loadbalancersdk.ListLoadBalancersParams,
	) iter.Seq2[*loadbalancersdk.LoadBalancerSchema, error]
}

// LoadBalancerFilter narrows the set of load balancers returned by a
// ListLoadBalancers call. Empty fields are ignored during match.
type LoadBalancerFilter struct {
	ID               *core.UUID
	Name             *string
	RegionID         *core.UUID
	VIPAddress       *string
	PublicAddress    *string
	V6PublicAddress  *string
	Package          *loadbalancersdk.LoadBalancerPackage
	Topology         *loadbalancersdk.LoadBalancerTopology
	Type             *loadbalancersdk.LoadBalancerType
	AccessLogEnabled *bool
}

// String renders the filter for diagnostic messages.
func (f LoadBalancerFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
		finder.Part("region_id", f.RegionID),
		finder.Text("vip_address", normalizedLoadBalancerAddress(f.VIPAddress)),
		finder.Text("public_address", normalizedLoadBalancerAddress(f.PublicAddress)),
		finder.Text("v6_public_address", normalizedLoadBalancerAddress(f.V6PublicAddress)),
		finder.Part("package", f.Package),
		finder.Part("topology", f.Topology),
		finder.Part("type", f.Type),
		finder.Part("access_log_enabled", f.AccessLogEnabled),
	)
}

func (f LoadBalancerFilter) matches(lb *loadbalancersdk.LoadBalancerSchema) bool {
	if lb.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
		return false
	}
	if f.ID != nil && lb.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && lb.Name != name {
		return false
	}
	if f.RegionID != nil && lb.Region.Id != *f.RegionID {
		return false
	}
	if f.Package != nil && lb.Package != *f.Package || f.Topology != nil && lb.Topology != *f.Topology || f.Type != nil && lb.Type != *f.Type {
		return false
	}
	if f.AccessLogEnabled != nil && lb.AccessLogEnabled != *f.AccessLogEnabled {
		return false
	}
	for _, address := range []struct{ filter, actual *string }{
		{f.VIPAddress, core.NullableToPtr(lb.VipAddress)},
		{f.PublicAddress, core.NullableToPtr(lb.PublicAddress)},
		{f.V6PublicAddress, core.NullableToPtr(lb.V6PublicAddress)},
	} {
		if finder.Trim(address.filter) == "" {
			continue
		}
		if address.actual == nil {
			return false
		}
		want, err := netip.ParseAddr(finder.Trim(address.filter))
		got, actualErr := netip.ParseAddr(finder.Trim(address.actual))
		if err != nil || actualErr != nil || want != got {
			return false
		}
	}
	return true
}

func (f LoadBalancerFilter) params(projectID core.UUID) loadbalancersdk.ListLoadBalancersParams {
	params := loadbalancersdk.ListLoadBalancersParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if f.RegionID != nil {
		params.RegionId = f.RegionID
	}
	params.VipAddress = normalizedLoadBalancerAddress(f.VIPAddress)
	params.PublicAddress = normalizedLoadBalancerAddress(f.PublicAddress)
	params.V6PublicAddress = normalizedLoadBalancerAddress(f.V6PublicAddress)
	params.Package = f.Package
	params.Topology = f.Topology
	params.Type = f.Type
	params.AccessLogEnabled = f.AccessLogEnabled
	return params
}

func normalizedLoadBalancerAddress(value *string) *string {
	text := finder.Trim(value)
	if text == "" {
		return nil
	}
	address, err := netip.ParseAddr(text)
	if err == nil {
		text = address.String()
	}
	return &text
}

// LoadBalancerFinder implements finder.Interface over the SDK list call.
type LoadBalancerFinder struct {
	client    LoadBalancerLister
	projectID core.UUID
}

var _ finder.Interface[LoadBalancerFilter, loadbalancersdk.LoadBalancerSchema] = (*LoadBalancerFinder)(nil)

// NewLoadBalancerFinder builds a finder rooted at the given SDK client.
func NewLoadBalancerFinder(client LoadBalancerLister, projectID core.UUID) *LoadBalancerFinder {
	return &LoadBalancerFinder{client: client, projectID: projectID}
}

// Find returns every candidate that satisfies filter.
func (f *LoadBalancerFinder) Find(ctx context.Context, filter LoadBalancerFilter) ([]loadbalancersdk.LoadBalancerSchema, error) {
	seq := f.client.ListLoadBalancersIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "load balancer", filter.String(), filter.matches)
}

// Resolve narrows Find to a single load balancer, or explains why it cannot.
func (f *LoadBalancerFinder) Resolve(ctx context.Context, filter LoadBalancerFilter) (loadbalancersdk.LoadBalancerSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return loadbalancersdk.LoadBalancerSchema{}, err
	}
	return finder.ExactlyOne(candidates, "load balancer", filter.String(), func(lb loadbalancersdk.LoadBalancerSchema) string {
		return fmt.Sprintf("id=%s (name=%q)", lb.Id, lb.Name)
	})
}

// ListenerOwnerLister is the SDK listing that ListenerLoadBalancer needs: the
// Load Balancers of a project and the Listeners of each.
type ListenerOwnerLister interface {
	LoadBalancerLister
	ListListenersIter(
		ctx context.Context,
		loadBalancerID core.UUID,
		params loadbalancersdk.ListListenersParams,
	) iter.Seq2[*loadbalancersdk.ListenerSchema, error]
}

// ServerGroupOwnerLister is the SDK listing that ServerGroupLoadBalancer
// needs: the Load Balancers of a project and the Server Groups of each.
type ServerGroupOwnerLister interface {
	LoadBalancerLister
	ListServerGroupsIter(
		ctx context.Context,
		loadBalancerID core.UUID,
		params loadbalancersdk.ListServerGroupsParams,
	) iter.Seq2[*loadbalancersdk.ServerGroupSchema, error]
}

// ListenerLoadBalancer returns the ID of the Load Balancer that owns the
// Listener listenerID. An L7 Policy and an L7 Rule record no Load Balancer.
// The API lists Listeners only under the path of their Load Balancer, so the
// search lists every Load Balancer of the project.
func ListenerLoadBalancer(ctx context.Context, client ListenerOwnerLister, projectID, listenerID core.UUID) (core.UUID, error) {
	return ownerLoadBalancer(ctx, client, projectID, "listener", listenerID,
		func(ctx context.Context, loadBalancerID core.UUID) iter.Seq2[core.UUID, error] {
			return ownedIDs(client.ListListenersIter(ctx, loadBalancerID, loadbalancersdk.ListListenersParams{ProjectID: projectID}),
				func(listener *loadbalancersdk.ListenerSchema) core.UUID { return listener.Id })
		})
}

// ServerGroupLoadBalancer returns the ID of the Load Balancer that owns the
// Server Group serverGroupID. A member records only its Server Group. The API
// lists Server Groups only under the path of their Load Balancer, so the
// search lists every Load Balancer of the project.
func ServerGroupLoadBalancer(ctx context.Context, client ServerGroupOwnerLister, projectID, serverGroupID core.UUID) (core.UUID, error) {
	return ownerLoadBalancer(ctx, client, projectID, "server group", serverGroupID,
		func(ctx context.Context, loadBalancerID core.UUID) iter.Seq2[core.UUID, error] {
			return ownedIDs(client.ListServerGroupsIter(ctx, loadBalancerID, loadbalancersdk.ListServerGroupsParams{ProjectID: projectID}),
				func(group *loadbalancersdk.ServerGroupSchema) core.UUID { return group.Id })
		})
}

// ownerLoadBalancer lists the Load Balancers of the project with
// LoadBalancerFinder and returns the first one whose children include childID.
func ownerLoadBalancer(
	ctx context.Context,
	client LoadBalancerLister,
	projectID core.UUID,
	kind string,
	childID core.UUID,
	children func(context.Context, core.UUID) iter.Seq2[core.UUID, error],
) (core.UUID, error) {
	loadBalancers, err := NewLoadBalancerFinder(client, projectID).Find(ctx, LoadBalancerFilter{})
	if err != nil {
		return core.UUID{}, err
	}
	for _, lb := range loadBalancers {
		for id, err := range children(ctx, lb.Id) {
			if err != nil {
				return core.UUID{}, fmt.Errorf("list %ss of load balancer %s: %w", kind, lb.Id, err)
			}
			if id == childID {
				return lb.Id, nil
			}
		}
	}
	return core.UUID{}, fmt.Errorf("no load balancer of project %s owns %s %s", projectID, kind, childID)
}

func ownedIDs[T any](seq iter.Seq2[*T, error], id func(*T) core.UUID) iter.Seq2[core.UUID, error] {
	return func(yield func(core.UUID, error) bool) {
		for child, err := range seq {
			if err != nil {
				yield(core.UUID{}, err)
				return
			}
			if !yield(id(child), nil) {
				return
			}
		}
	}
}
