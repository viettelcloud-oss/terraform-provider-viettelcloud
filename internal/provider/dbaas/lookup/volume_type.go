package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// VolumeTypeLister lists the DBaaS volume type catalog.
type VolumeTypeLister interface {
	ListDbaasVolumeTypesIter(context.Context, dbaassdk.ListDbaasVolumeTypesParams) iter.Seq2[*dbaassdk.DBaaSRegionVolumeTypeSchema, error]
}

// VolumeTypeFilter selects a volume type. The backend keeps (name, region)
// unique.
type VolumeTypeFilter struct {
	RegionID *core.UUID
	Name     *string
}

func (f VolumeTypeFilter) String() string {
	return finder.Criteria(finder.Part("region_id", f.RegionID), finder.Text("name", f.Name))
}

func (f VolumeTypeFilter) matches(volumeType *dbaassdk.DBaaSRegionVolumeTypeSchema) bool {
	if f.RegionID != nil && volumeType.RegionId != *f.RegionID {
		return false
	}
	return finder.Trim(f.Name) == "" || volumeType.Name == finder.Trim(f.Name)
}

func (f VolumeTypeFilter) params() dbaassdk.ListDbaasVolumeTypesParams {
	return dbaassdk.ListDbaasVolumeTypesParams{RegionId: f.RegionID}
}

type VolumeTypeFinder struct {
	client VolumeTypeLister
}

var _ finder.Interface[VolumeTypeFilter, dbaassdk.DBaaSRegionVolumeTypeSchema] = (*VolumeTypeFinder)(nil)

func NewVolumeTypeFinder(client VolumeTypeLister) *VolumeTypeFinder {
	return &VolumeTypeFinder{client: client}
}

func (f *VolumeTypeFinder) Find(ctx context.Context, filter VolumeTypeFilter) ([]dbaassdk.DBaaSRegionVolumeTypeSchema, error) {
	return finder.Collect(f.client.ListDbaasVolumeTypesIter(ctx, filter.params()), "DBaaS volume type", filter.String(), filter.matches)
}

func (f *VolumeTypeFinder) Resolve(ctx context.Context, filter VolumeTypeFilter) (dbaassdk.DBaaSRegionVolumeTypeSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaassdk.DBaaSRegionVolumeTypeSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DBaaS volume type", filter.String(), func(volumeType dbaassdk.DBaaSRegionVolumeTypeSchema) string {
		return fmt.Sprintf("id=%s (name=%q)", volumeType.Id, volumeType.Name)
	})
}
