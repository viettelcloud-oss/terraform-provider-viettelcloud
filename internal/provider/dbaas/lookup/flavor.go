package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// FlavorLister lists the DBaaS flavor catalog.
type FlavorLister interface {
	ListDbaasFlavorsIter(context.Context, dbaassdk.ListDbaasFlavorsParams) iter.Seq2[*dbaassdk.DBaaSRegionFlavorSchema, error]
}

// FlavorFilter selects a flavor. The backend keeps (region, flavor_name)
// unique.
type FlavorFilter struct {
	RegionID *core.UUID
	Name     *string
}

func (f FlavorFilter) String() string {
	return finder.Criteria(finder.Part("region_id", f.RegionID), finder.Text("name", f.Name))
}

func (f FlavorFilter) matches(flavor *dbaassdk.DBaaSRegionFlavorSchema) bool {
	if f.RegionID != nil && flavor.RegionId != *f.RegionID {
		return false
	}
	return finder.Trim(f.Name) == "" || flavor.FlavorName == finder.Trim(f.Name)
}

func (f FlavorFilter) params() dbaassdk.ListDbaasFlavorsParams {
	return dbaassdk.ListDbaasFlavorsParams{RegionId: f.RegionID}
}

type FlavorFinder struct {
	client FlavorLister
}

var _ finder.Interface[FlavorFilter, dbaassdk.DBaaSRegionFlavorSchema] = (*FlavorFinder)(nil)

func NewFlavorFinder(client FlavorLister) *FlavorFinder {
	return &FlavorFinder{client: client}
}

func (f *FlavorFinder) Find(ctx context.Context, filter FlavorFilter) ([]dbaassdk.DBaaSRegionFlavorSchema, error) {
	return finder.Collect(f.client.ListDbaasFlavorsIter(ctx, filter.params()), "DBaaS flavor", filter.String(), filter.matches)
}

func (f *FlavorFinder) Resolve(ctx context.Context, filter FlavorFilter) (dbaassdk.DBaaSRegionFlavorSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaassdk.DBaaSRegionFlavorSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DBaaS flavor", filter.String(), func(flavor dbaassdk.DBaaSRegionFlavorSchema) string {
		return fmt.Sprintf("id=%s (name=%q)", flavor.Id, flavor.FlavorName)
	})
}
