package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type EngineVersionLister interface {
	ListDbClusterEngineVersionsIter(
		context.Context,
		dbaasclustersdk.ListDbClusterEngineVersionsParams,
	) iter.Seq2[*dbaasclustersdk.DBClusterEngineVersionSchema, error]
}

type EngineVersionFilter struct {
	Engine   *string
	Version  *string
	IsActive *bool
}

func (f EngineVersionFilter) String() string {
	return finder.Criteria(
		finder.Text("engine", f.Engine),
		finder.Text("version", f.Version),
		finder.Part("is_active", f.IsActive),
	)
}

func (f EngineVersionFilter) matches(version *dbaasclustersdk.DBClusterEngineVersionSchema) bool {
	if engine := finder.Trim(f.Engine); engine != "" && string(version.Engine) != engine {
		return false
	}
	if want := finder.Trim(f.Version); want != "" && version.Version != want {
		return false
	}
	if f.IsActive != nil && version.IsActive != *f.IsActive {
		return false
	}
	return true
}

func (f EngineVersionFilter) params() dbaasclustersdk.ListDbClusterEngineVersionsParams {
	params := dbaasclustersdk.ListDbClusterEngineVersionsParams{IsActive: f.IsActive}
	if engine := finder.Trim(f.Engine); engine != "" {
		typed := dbaasclustersdk.DBClusterEngineType(engine)
		params.Engine = &typed
	}
	return params
}

type EngineVersionFinder struct {
	client EngineVersionLister
}

type EngineVersionResolveFunc func(context.Context, EngineVersionFilter) (dbaasclustersdk.DBClusterEngineVersionSchema, error)

var _ finder.Interface[EngineVersionFilter, dbaasclustersdk.DBClusterEngineVersionSchema] = (*EngineVersionFinder)(nil)

func NewEngineVersionFinder(client EngineVersionLister) *EngineVersionFinder {
	return &EngineVersionFinder{client: client}
}

func (f *EngineVersionFinder) Find(ctx context.Context, filter EngineVersionFilter) ([]dbaasclustersdk.DBClusterEngineVersionSchema, error) {
	seq := f.client.ListDbClusterEngineVersionsIter(ctx, filter.params())
	return finder.Collect(seq, "engine version", filter.String(), filter.matches)
}

func (f *EngineVersionFinder) Resolve(ctx context.Context, filter EngineVersionFilter) (dbaasclustersdk.DBClusterEngineVersionSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaasclustersdk.DBClusterEngineVersionSchema{}, err
	}
	return finder.ExactlyOne(candidates, "engine version", filter.String(), engineVersionSummary)
}

func engineVersionSummary(version dbaasclustersdk.DBClusterEngineVersionSchema) string {
	return fmt.Sprintf("id=%s (engine=%q, version=%q, is_active=%t)", version.Id, version.Engine, version.Version, version.IsActive)
}
