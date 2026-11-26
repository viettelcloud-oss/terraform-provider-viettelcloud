package lookup

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type PlacementGroupLister interface {
	ListPlacementGroupsIter(
		context.Context,
		serversdk.ListPlacementGroupsParams,
	) iter.Seq2[*serversdk.PlacementGroupSchema, error]
}

type PlacementGroupFilter struct {
	ID     *core.UUID
	Name   *string
	Policy *serversdk.PlacementGroupPolicy
	Region *string
}

func (f PlacementGroupFilter) String() string {
	var policy *serversdk.PlacementGroupPolicy
	if trimmed := f.policy(); trimmed != "" {
		policy = &trimmed
	}
	return finder.Criteria(
		finder.Part("id", f.ID), finder.Text("name", f.Name),
		finder.Part("policy", policy), finder.Text("region", f.Region),
	)
}

// policy trims the criterion the way finder.Trim treats the text ones, so a
// blank policy is unset in the request, the match and the description alike.
func (f PlacementGroupFilter) policy() serversdk.PlacementGroupPolicy {
	if f.Policy == nil {
		return ""
	}
	return serversdk.PlacementGroupPolicy(strings.TrimSpace(string(*f.Policy)))
}

func (f PlacementGroupFilter) matches(pg *serversdk.PlacementGroupSchema) bool {
	if f.ID != nil && pg.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && pg.Name != name {
		return false
	}
	// The backend reports policy as optional, so a candidate without one can
	// never satisfy a policy criterion.
	if policy := f.policy(); policy != "" {
		if pgPolicy := core.NullableToPtr(pg.Policy); pgPolicy == nil || *pgPolicy != policy {
			return false
		}
	}
	region := finder.Trim(f.Region)
	return region == "" || pg.Region.Name == region
}

func (f PlacementGroupFilter) params(projectID core.UUID) serversdk.ListPlacementGroupsParams {
	params := serversdk.ListPlacementGroupsParams{ProjectID: projectID, Id: f.ID}
	if policy := f.policy(); policy != "" {
		params.Policy = &policy
	}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if region := finder.Trim(f.Region); region != "" {
		params.RegionName = &region
	}
	return params
}

type PlacementGroupFinder struct {
	client    PlacementGroupLister
	projectID core.UUID
}

var _ finder.Interface[PlacementGroupFilter, serversdk.PlacementGroupSchema] = (*PlacementGroupFinder)(nil)

func NewPlacementGroupFinder(client PlacementGroupLister, projectID core.UUID) *PlacementGroupFinder {
	return &PlacementGroupFinder{client: client, projectID: projectID}
}

func (f *PlacementGroupFinder) Find(ctx context.Context, filter PlacementGroupFilter) ([]serversdk.PlacementGroupSchema, error) {
	seq := f.client.ListPlacementGroupsIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "placement group", filter.String(), filter.matches)
}

func (f *PlacementGroupFinder) Resolve(ctx context.Context, filter PlacementGroupFilter) (serversdk.PlacementGroupSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return serversdk.PlacementGroupSchema{}, err
	}
	return finder.ExactlyOne(candidates, "placement group", filter.String(), placementGroupSummary)
}

func placementGroupSummary(pg serversdk.PlacementGroupSchema) string {
	policy := ""
	if pgPolicy := core.NullableToPtr(pg.Policy); pgPolicy != nil {
		policy = string(*pgPolicy)
	}
	return fmt.Sprintf("id=%s (name=%q, policy=%q, region=%q)", pg.Id, pg.Name, policy, pg.Region.Name)
}
