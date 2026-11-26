// Package lookup holds the finders of dbaas-owned entities: DB instances, DB
// instance backups, and the DBaaS catalog of DB versions, flavors, and volume
// types.
package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// DBVersionLister lists the DB version catalog. The catalog endpoints do not
// take a Project-ID header.
type DBVersionLister interface {
	ListDbVersionsIter(context.Context, dbaassdk.ListDbVersionsParams) iter.Seq2[*dbaassdk.DBaaSVersionSchema, error]
}

// DBVersionFilter selects a DB version. The backend keeps (region,
// database_type, version) unique.
type DBVersionFilter struct {
	DatabaseType *dbaassdk.DatabaseType
	RegionID     *core.UUID
	Version      *string
}

func (f DBVersionFilter) String() string {
	return finder.Criteria(finder.Text("database_type", (*string)(f.DatabaseType)), finder.Part("region_id", f.RegionID), finder.Text("version", f.Version))
}

func (f DBVersionFilter) matches(version *dbaassdk.DBaaSVersionSchema) bool {
	if value := finder.Trim((*string)(f.DatabaseType)); value != "" && string(version.DatabaseType) != value {
		return false
	}
	if f.RegionID != nil && version.RegionId != *f.RegionID {
		return false
	}
	return finder.Trim(f.Version) == "" || version.Version == finder.Trim(f.Version)
}

func (f DBVersionFilter) params() dbaassdk.ListDbVersionsParams {
	params := dbaassdk.ListDbVersionsParams{RegionId: f.RegionID}
	if value := finder.Trim((*string)(f.DatabaseType)); value != "" {
		params.DatabaseType = new(dbaassdk.DatabaseType(value))
	}
	return params
}

type DBVersionFinder struct {
	client DBVersionLister
}

var _ finder.Interface[DBVersionFilter, dbaassdk.DBaaSVersionSchema] = (*DBVersionFinder)(nil)

func NewDBVersionFinder(client DBVersionLister) *DBVersionFinder {
	return &DBVersionFinder{client: client}
}

func (f *DBVersionFinder) Find(ctx context.Context, filter DBVersionFilter) ([]dbaassdk.DBaaSVersionSchema, error) {
	return finder.Collect(f.client.ListDbVersionsIter(ctx, filter.params()), "DB version", filter.String(), filter.matches)
}

func (f *DBVersionFinder) Resolve(ctx context.Context, filter DBVersionFilter) (dbaassdk.DBaaSVersionSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaassdk.DBaaSVersionSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DB version", filter.String(), func(version dbaassdk.DBaaSVersionSchema) string {
		return fmt.Sprintf("id=%s (database_type=%q, version=%q)", version.Id, version.DatabaseType, version.Version)
	})
}
