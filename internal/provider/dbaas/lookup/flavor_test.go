package lookup

import (
	"context"
	"iter"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

type fakeFlavorLister struct {
	items  []*dbaassdk.DBaaSRegionFlavorSchema
	err    error
	params dbaassdk.ListDbaasFlavorsParams
}

func (f *fakeFlavorLister) ListDbaasFlavorsIter(_ context.Context, params dbaassdk.ListDbaasFlavorsParams) iter.Seq2[*dbaassdk.DBaaSRegionFlavorSchema, error] {
	f.params = params
	return fakeSeq(f.items, f.err)
}

func TestFlavorFinder(t *testing.T) {
	t.Parallel()
	region := core.UUID{1}
	match := &dbaassdk.DBaaSRegionFlavorSchema{Id: core.UUID{3}, RegionId: region, FlavorName: "db.small"}
	prefix := &dbaassdk.DBaaSRegionFlavorSchema{Id: core.UUID{4}, RegionId: region, FlavorName: "db.small.2"}
	lister := &fakeFlavorLister{}
	assertCardinality(t, match, prefix, func(items []*dbaassdk.DBaaSRegionFlavorSchema, err error) (dbaassdk.DBaaSRegionFlavorSchema, error) {
		lister.items, lister.err = items, err
		return NewFlavorFinder(lister).Resolve(background(), FlavorFilter{RegionID: &region, Name: new(" db.small ")})
	}, func(flavor dbaassdk.DBaaSRegionFlavorSchema) string { return flavor.Id.String() })
	if lister.params.RegionId == nil || *lister.params.RegionId != region {
		t.Fatalf("region_id must reach the backend: %#v", lister.params)
	}
	lister.items, lister.err = []*dbaassdk.DBaaSRegionFlavorSchema{{Id: core.UUID{5}, RegionId: core.UUID{2}, FlavorName: "db.small"}}, nil
	if found, err := NewFlavorFinder(lister).Find(background(), FlavorFilter{RegionID: &region, Name: new("db.small")}); err != nil || len(found) != 0 {
		t.Fatalf("a flavor in another region matched: %v %v", found, err)
	}
}
