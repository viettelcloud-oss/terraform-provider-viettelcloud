package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type InstanceLister interface {
	ListDbInstancesIter(context.Context, dbaassdk.ListDbInstancesParams) iter.Seq2[*dbaassdk.DBInstanceSchema, error]
}

// InstanceFilter selects a DB instance. The backend filters name with
// icontains, so the finder matches it exactly. Names are unique in a project.
type InstanceFilter struct {
	Name *string
}

func (f InstanceFilter) String() string {
	return finder.Criteria(finder.Text("name", f.Name))
}

func (f InstanceFilter) matches(instance *dbaassdk.DBInstanceSchema) bool {
	return finder.Trim(f.Name) == "" || instance.Name == finder.Trim(f.Name)
}

func (f InstanceFilter) params(projectID core.UUID) dbaassdk.ListDbInstancesParams {
	params := dbaassdk.ListDbInstancesParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type InstanceFinder struct {
	client    InstanceLister
	projectID core.UUID
}

var _ finder.Interface[InstanceFilter, dbaassdk.DBInstanceSchema] = (*InstanceFinder)(nil)

func NewInstanceFinder(client InstanceLister, projectID core.UUID) *InstanceFinder {
	return &InstanceFinder{client: client, projectID: projectID}
}

func (f *InstanceFinder) Find(ctx context.Context, filter InstanceFilter) ([]dbaassdk.DBInstanceSchema, error) {
	return finder.Collect(f.client.ListDbInstancesIter(ctx, filter.params(f.projectID)), "DB instance", filter.String(), filter.matches)
}

func (f *InstanceFinder) Resolve(ctx context.Context, filter InstanceFilter) (dbaassdk.DBInstanceSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaassdk.DBInstanceSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DB instance", filter.String(), func(instance dbaassdk.DBInstanceSchema) string {
		return fmt.Sprintf("id=%s (name=%q, region=%q)", instance.Id, instance.Name, instance.Region.Name)
	})
}
