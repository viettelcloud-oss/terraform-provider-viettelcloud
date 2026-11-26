package lookup

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type fakeClusterLister struct {
	results []dbaasclustersdk.DBClusterListSchema
	err     error
	params  dbaasclustersdk.ListDbClustersParams
}

func (f *fakeClusterLister) ListDbClustersIter(_ context.Context, params dbaasclustersdk.ListDbClustersParams) iter.Seq2[*dbaasclustersdk.DBClusterListSchema, error] {
	f.params = params
	return func(yield func(*dbaasclustersdk.DBClusterListSchema, error) bool) {
		for i := range f.results {
			if !yield(&f.results[i], nil) {
				return
			}
		}
		if f.err != nil {
			yield(nil, f.err)
		}
	}
}

func sampleClusterItem(id core.UUID, name string, engine dbaasclustersdk.DBClusterEngineType, regionID core.UUID) dbaasclustersdk.DBClusterListSchema {
	return dbaasclustersdk.DBClusterListSchema{
		Id: id, Name: name, Engine: engine,
		Region: dbaasclustersdk.NestedRegionSchema{Id: regionID, Name: "region"},
	}
}

func TestDBClusterFinderMatchesEveryCriterionExactly(t *testing.T) {
	t.Parallel()

	projectID, regionID, otherRegion := core.UUID{9}, core.UUID{8}, core.UUID{7}
	lister := &fakeClusterLister{results: []dbaasclustersdk.DBClusterListSchema{
		sampleClusterItem(core.UUID{1}, "orders", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID),
		// The backend filters by substring and ignores case, so these arrive too.
		sampleClusterItem(core.UUID{2}, "orders-archive", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID),
		sampleClusterItem(core.UUID{3}, "Orders", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID),
		sampleClusterItem(core.UUID{4}, "orders", dbaasclustersdk.DBClusterEngineTypeOpensearch, regionID),
		sampleClusterItem(core.UUID{5}, "orders", dbaasclustersdk.DBClusterEngineTypeMongodb, otherRegion),
	}}
	name, engine := " orders ", "mongodb"
	got, err := NewDBClusterFinder(lister, projectID).Find(context.Background(), DBClusterFilter{Name: &name, Engine: &engine, RegionID: &regionID})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 || got[0].Id != (core.UUID{1}) {
		t.Fatalf("matches = %#v", got)
	}

	if lister.params.ProjectID != projectID || lister.params.Name == nil || *lister.params.Name != "orders" ||
		lister.params.Engine == nil || len(*lister.params.Engine) != 1 || (*lister.params.Engine)[0] != dbaasclustersdk.DBClusterEngineTypeMongodb ||
		lister.params.RegionId == nil || *lister.params.RegionId != regionID {
		t.Fatalf("every criterion must reach the backend: %#v", lister.params)
	}
}

func TestDBClusterFinderResolve(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{8}
	one := sampleClusterItem(core.UUID{1}, "orders", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID)
	other := sampleClusterItem(core.UUID{2}, "orders", dbaasclustersdk.DBClusterEngineTypeOpensearch, regionID)
	name := "orders"

	tests := []struct {
		name    string
		lister  *fakeClusterLister
		filter  DBClusterFilter
		wantErr string
		wantID  core.UUID
	}{
		{name: "one match", lister: &fakeClusterLister{results: []dbaasclustersdk.DBClusterListSchema{one}}, filter: DBClusterFilter{Name: &name}, wantID: one.Id},
		{name: "no match", lister: &fakeClusterLister{}, filter: DBClusterFilter{Name: &name}, wantErr: "was not found"},
		{name: "ambiguous", lister: &fakeClusterLister{results: []dbaasclustersdk.DBClusterListSchema{one, other}}, filter: DBClusterFilter{Name: &name}, wantErr: "ambiguous"},
		{name: "by ID", lister: &fakeClusterLister{results: []dbaasclustersdk.DBClusterListSchema{one, other}}, filter: DBClusterFilter{ID: &other.Id}, wantID: other.Id},
		{name: "SDK error", lister: &fakeClusterLister{err: errors.New("boom")}, filter: DBClusterFilter{Name: &name}, wantErr: "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewDBClusterFinder(tt.lister, core.UUID{9}).Resolve(context.Background(), tt.filter)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.Id != tt.wantID {
				t.Fatalf("got %v err %v, want %v", got.Id, err, tt.wantID)
			}
		})
	}
}

func TestDBClusterFinderFiltersByStatus(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{8}
	active := sampleClusterItem(core.UUID{1}, "orders", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID)
	active.Status = dbaasclustersdk.DBClusterStatusActive
	failed := sampleClusterItem(core.UUID{2}, "orders", dbaasclustersdk.DBClusterEngineTypeMongodb, regionID)
	failed.Status = dbaasclustersdk.DBClusterStatusError
	lister := &fakeClusterLister{results: []dbaasclustersdk.DBClusterListSchema{active, failed}}

	status := " active "
	got, err := NewDBClusterFinder(lister, core.UUID{9}).Resolve(context.Background(), DBClusterFilter{Status: &status})
	if err != nil || got.Id != active.Id {
		t.Fatalf("got %v err %v, want %v", got.Id, err, active.Id)
	}
	if lister.params.Status == nil || len(*lister.params.Status) != 1 || (*lister.params.Status)[0] != dbaasclustersdk.DBClusterStatusActive {
		t.Fatalf("the status must reach the backend: %#v", lister.params.Status)
	}
}
