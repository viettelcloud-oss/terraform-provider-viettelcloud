package lookup

import (
	"context"
	"iter"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

type fakeDBVersionLister struct {
	items  []*dbaassdk.DBaaSVersionSchema
	err    error
	params dbaassdk.ListDbVersionsParams
}

func (f *fakeDBVersionLister) ListDbVersionsIter(_ context.Context, params dbaassdk.ListDbVersionsParams) iter.Seq2[*dbaassdk.DBaaSVersionSchema, error] {
	f.params = params
	return fakeSeq(f.items, f.err)
}

func TestDBVersionFinder(t *testing.T) {
	t.Parallel()
	region, otherRegion := core.UUID{1}, core.UUID{2}
	match := &dbaassdk.DBaaSVersionSchema{Id: core.UUID{3}, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: region, Version: "16"}
	prefix := &dbaassdk.DBaaSVersionSchema{Id: core.UUID{4}, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: region, Version: "16.4"}
	filter := DBVersionFilter{DatabaseType: new(dbaassdk.DatabaseType(" postgresql ")), RegionID: &region, Version: new(" 16 ")}
	lister := &fakeDBVersionLister{}
	assertCardinality(t, match, prefix, func(items []*dbaassdk.DBaaSVersionSchema, err error) (dbaassdk.DBaaSVersionSchema, error) {
		lister.items, lister.err = items, err
		return NewDBVersionFinder(lister).Resolve(background(), filter)
	}, func(version dbaassdk.DBaaSVersionSchema) string { return version.Id.String() })
	if lister.params.DatabaseType == nil || *lister.params.DatabaseType != dbaassdk.DatabaseTypePostgresql || lister.params.RegionId == nil || *lister.params.RegionId != region {
		t.Fatalf("database_type and region_id must reach the backend: %#v", lister.params)
	}
	for name, candidate := range map[string]*dbaassdk.DBaaSVersionSchema{
		"other database type": {Id: core.UUID{5}, DatabaseType: dbaassdk.DatabaseTypeMysql, RegionId: region, Version: "16"},
		"other region":        {Id: core.UUID{6}, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: otherRegion, Version: "16"},
	} {
		lister.items, lister.err = []*dbaassdk.DBaaSVersionSchema{candidate}, nil
		if found, err := NewDBVersionFinder(lister).Find(background(), filter); err != nil || len(found) != 0 {
			t.Fatalf("%s matched: %v %v", name, found, err)
		}
	}
}
