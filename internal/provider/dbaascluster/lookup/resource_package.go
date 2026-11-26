package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type ResourcePackageLister interface {
	ListDbClusterResourcePackagesIter(
		context.Context,
		dbaasclustersdk.ListDbClusterResourcePackagesParams,
	) iter.Seq2[*dbaasclustersdk.DBClusterResourcePackageSchema, error]
}

type ResourcePackageFilter struct {
	Name *string
}

func (f ResourcePackageFilter) String() string {
	return finder.Criteria(finder.Text("name", f.Name))
}

// matches compares the name exactly because the backend filters by
// case-insensitive substring, so its result is a superset of the match.
func (f ResourcePackageFilter) matches(pkg *dbaasclustersdk.DBClusterResourcePackageSchema) bool {
	name := finder.Trim(f.Name)
	return name == "" || pkg.Name == name
}

func (f ResourcePackageFilter) params() dbaasclustersdk.ListDbClusterResourcePackagesParams {
	params := dbaasclustersdk.ListDbClusterResourcePackagesParams{}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type ResourcePackageFinder struct {
	client ResourcePackageLister
}

type ResourcePackageResolveFunc func(context.Context, ResourcePackageFilter) (dbaasclustersdk.DBClusterResourcePackageSchema, error)

var _ finder.Interface[ResourcePackageFilter, dbaasclustersdk.DBClusterResourcePackageSchema] = (*ResourcePackageFinder)(nil)

func NewResourcePackageFinder(client ResourcePackageLister) *ResourcePackageFinder {
	return &ResourcePackageFinder{client: client}
}

func (f *ResourcePackageFinder) Find(ctx context.Context, filter ResourcePackageFilter) ([]dbaasclustersdk.DBClusterResourcePackageSchema, error) {
	seq := f.client.ListDbClusterResourcePackagesIter(ctx, filter.params())
	return finder.Collect(seq, "resource package", filter.String(), filter.matches)
}

func (f *ResourcePackageFinder) Resolve(ctx context.Context, filter ResourcePackageFilter) (dbaasclustersdk.DBClusterResourcePackageSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaasclustersdk.DBClusterResourcePackageSchema{}, err
	}
	return finder.ExactlyOne(candidates, "resource package", filter.String(), resourcePackageSummary)
}

func resourcePackageSummary(pkg dbaasclustersdk.DBClusterResourcePackageSchema) string {
	return fmt.Sprintf("id=%s (name=%q, vcpus=%d, ram=%d)", pkg.Id, pkg.Name, pkg.Vcpus, pkg.Ram)
}
