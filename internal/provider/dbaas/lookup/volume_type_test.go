package lookup

import (
	"context"
	"iter"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

type fakeVolumeTypeLister struct {
	items  []*dbaassdk.DBaaSRegionVolumeTypeSchema
	err    error
	params dbaassdk.ListDbaasVolumeTypesParams
}

func (f *fakeVolumeTypeLister) ListDbaasVolumeTypesIter(_ context.Context, params dbaassdk.ListDbaasVolumeTypesParams) iter.Seq2[*dbaassdk.DBaaSRegionVolumeTypeSchema, error] {
	f.params = params
	return fakeSeq(f.items, f.err)
}

func TestVolumeTypeFinder(t *testing.T) {
	t.Parallel()
	region := core.UUID{1}
	match := &dbaassdk.DBaaSRegionVolumeTypeSchema{Id: core.UUID{3}, RegionId: region, Name: "ssd"}
	prefix := &dbaassdk.DBaaSRegionVolumeTypeSchema{Id: core.UUID{4}, RegionId: region, Name: "ssd-encrypted"}
	lister := &fakeVolumeTypeLister{}
	assertCardinality(t, match, prefix, func(items []*dbaassdk.DBaaSRegionVolumeTypeSchema, err error) (dbaassdk.DBaaSRegionVolumeTypeSchema, error) {
		lister.items, lister.err = items, err
		return NewVolumeTypeFinder(lister).Resolve(background(), VolumeTypeFilter{RegionID: &region, Name: new("ssd")})
	}, func(volumeType dbaassdk.DBaaSRegionVolumeTypeSchema) string { return volumeType.Id.String() })
	if lister.params.RegionId == nil || *lister.params.RegionId != region {
		t.Fatalf("region_id must reach the backend: %#v", lister.params)
	}
	lister.items, lister.err = []*dbaassdk.DBaaSRegionVolumeTypeSchema{{Id: core.UUID{5}, RegionId: core.UUID{2}, Name: "ssd"}}, nil
	if found, err := NewVolumeTypeFinder(lister).Find(background(), VolumeTypeFilter{RegionID: &region, Name: new("ssd")}); err != nil || len(found) != 0 {
		t.Fatalf("a volume type in another region matched: %v %v", found, err)
	}
}
