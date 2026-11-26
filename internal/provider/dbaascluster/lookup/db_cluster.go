package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type DBClusterLister interface {
	ListDbClustersIter(
		context.Context,
		dbaasclustersdk.ListDbClustersParams,
	) iter.Seq2[*dbaasclustersdk.DBClusterListSchema, error]
}

type DBClusterFilter struct {
	ID       *core.UUID
	Name     *string
	Engine   *string
	Status   *string
	RegionID *core.UUID
}

func (f DBClusterFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID),
		finder.Text("name", f.Name),
		finder.Text("engine", f.Engine),
		finder.Text("status", f.Status),
		finder.Part("region_id", f.RegionID),
	)
}

// matches compares the name exactly because the backend filters by
// case-insensitive substring, so its result is a superset of the match.
func (f DBClusterFilter) matches(cluster *dbaasclustersdk.DBClusterListSchema) bool {
	if f.ID != nil && cluster.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && cluster.Name != name {
		return false
	}
	if engine := finder.Trim(f.Engine); engine != "" && string(cluster.Engine) != engine {
		return false
	}
	if status := finder.Trim(f.Status); status != "" && string(cluster.Status) != status {
		return false
	}
	if f.RegionID != nil && cluster.Region.Id != *f.RegionID {
		return false
	}
	return true
}

func (f DBClusterFilter) params(projectID core.UUID) dbaasclustersdk.ListDbClustersParams {
	params := dbaasclustersdk.ListDbClustersParams{ProjectID: projectID, RegionId: f.RegionID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if engine := finder.Trim(f.Engine); engine != "" {
		engines := []dbaasclustersdk.DBClusterEngineType{dbaasclustersdk.DBClusterEngineType(engine)}
		params.Engine = &engines
	}
	if status := finder.Trim(f.Status); status != "" {
		statuses := []dbaasclustersdk.DBClusterStatus{dbaasclustersdk.DBClusterStatus(status)}
		params.Status = &statuses
	}
	return params
}

type DBClusterFinder struct {
	client    DBClusterLister
	projectID core.UUID
}

var _ finder.Interface[DBClusterFilter, dbaasclustersdk.DBClusterListSchema] = (*DBClusterFinder)(nil)

func NewDBClusterFinder(client DBClusterLister, projectID core.UUID) *DBClusterFinder {
	return &DBClusterFinder{client: client, projectID: projectID}
}

func (f *DBClusterFinder) Find(ctx context.Context, filter DBClusterFilter) ([]dbaasclustersdk.DBClusterListSchema, error) {
	seq := f.client.ListDbClustersIter(ctx, filter.params(f.projectID))
	return finder.Collect(seq, "DB cluster", filter.String(), filter.matches)
}

func (f *DBClusterFinder) Resolve(ctx context.Context, filter DBClusterFilter) (dbaasclustersdk.DBClusterListSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaasclustersdk.DBClusterListSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DB cluster", filter.String(), dbClusterSummary)
}

func dbClusterSummary(cluster dbaasclustersdk.DBClusterListSchema) string {
	return fmt.Sprintf("id=%s (name=%q, engine=%q, region=%q)", cluster.Id, cluster.Name, cluster.Engine, cluster.Region.Name)
}
