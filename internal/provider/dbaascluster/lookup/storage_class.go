package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
)

type StorageClassLister interface {
	ListDbClusterStorageClassesIter(
		context.Context,
		dbaasclustersdk.ListDbClusterStorageClassesParams,
	) iter.Seq2[*dbaasclustersdk.DBClusterStorageClassSchema, error]
}

type StorageClassFilter struct {
	Name *string
}

func (f StorageClassFilter) String() string {
	return finder.Criteria(finder.Text("name", f.Name))
}

// matches compares the name exactly because the backend filters by
// case-insensitive substring, so its result is a superset of the match.
func (f StorageClassFilter) matches(class *dbaasclustersdk.DBClusterStorageClassSchema) bool {
	name := finder.Trim(f.Name)
	return name == "" || class.Name == name
}

func (f StorageClassFilter) params() dbaasclustersdk.ListDbClusterStorageClassesParams {
	params := dbaasclustersdk.ListDbClusterStorageClassesParams{}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type StorageClassFinder struct {
	client StorageClassLister
}

type StorageClassResolveFunc func(context.Context, StorageClassFilter) (dbaasclustersdk.DBClusterStorageClassSchema, error)

var _ finder.Interface[StorageClassFilter, dbaasclustersdk.DBClusterStorageClassSchema] = (*StorageClassFinder)(nil)

func NewStorageClassFinder(client StorageClassLister) *StorageClassFinder {
	return &StorageClassFinder{client: client}
}

func (f *StorageClassFinder) Find(ctx context.Context, filter StorageClassFilter) ([]dbaasclustersdk.DBClusterStorageClassSchema, error) {
	seq := f.client.ListDbClusterStorageClassesIter(ctx, filter.params())
	return finder.Collect(seq, "storage class", filter.String(), filter.matches)
}

func (f *StorageClassFinder) Resolve(ctx context.Context, filter StorageClassFilter) (dbaasclustersdk.DBClusterStorageClassSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaasclustersdk.DBClusterStorageClassSchema{}, err
	}
	return finder.ExactlyOne(candidates, "storage class", filter.String(), storageClassSummary)
}

func storageClassSummary(class dbaasclustersdk.DBClusterStorageClassSchema) string {
	return fmt.Sprintf("id=%s (name=%q, disk_type=%q, speed=%q)", class.Id, class.Name, class.DiskType, class.Speed)
}
