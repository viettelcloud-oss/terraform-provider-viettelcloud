package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type BackupLister interface {
	ListDbInstanceBackupsIter(context.Context, dbaassdk.ListDbInstanceBackupsParams) iter.Seq2[*dbaassdk.DBInstanceBackupSchema, error]
}

// BackupFilter selects a DB instance backup. The list returns both
// manual and automatic backups, and the backend has no backup_type filter, so
// the finder applies it. Only name, db_instance_id, and backup_type together
// are unique.
type BackupFilter struct {
	Name         *string
	DBInstanceID *core.UUID
	BackupType   *dbaassdk.BackupType
	Status       *dbaassdk.BackupStatus
	DatabaseType *dbaassdk.DatabaseType
	RegionID     *core.UUID
}

func (f BackupFilter) String() string {
	return finder.Criteria(
		finder.Text("name", f.Name), finder.Part("db_instance_id", f.DBInstanceID),
		finder.Text("backup_type", (*string)(f.BackupType)), finder.Text("status", (*string)(f.Status)),
		finder.Text("database_type", (*string)(f.DatabaseType)), finder.Part("region_id", f.RegionID),
	)
}

func (f BackupFilter) matches(backup *dbaassdk.DBInstanceBackupSchema) bool {
	if name := finder.Trim(f.Name); name != "" && backup.Name != name {
		return false
	}
	if f.DBInstanceID != nil {
		if instance := core.NullableToPtr(backup.DbInstance); instance == nil || instance.Id != *f.DBInstanceID {
			return false
		}
	}
	if value := finder.Trim((*string)(f.BackupType)); value != "" && string(backup.BackupType) != value {
		return false
	}
	if value := finder.Trim((*string)(f.Status)); value != "" && string(backup.Status) != value {
		return false
	}
	if value := finder.Trim((*string)(f.DatabaseType)); value != "" && string(backup.DatabaseType) != value {
		return false
	}
	return f.RegionID == nil || backup.Region.Id == *f.RegionID
}

func (f BackupFilter) params(projectID core.UUID) dbaassdk.ListDbInstanceBackupsParams {
	params := dbaassdk.ListDbInstanceBackupsParams{ProjectID: projectID, RegionId: f.RegionID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if f.DBInstanceID != nil {
		params.DbInstanceId = &[]core.UUID{*f.DBInstanceID}
	}
	if value := finder.Trim((*string)(f.Status)); value != "" {
		params.Status = &[]dbaassdk.BackupStatus{dbaassdk.BackupStatus(value)}
	}
	if value := finder.Trim((*string)(f.DatabaseType)); value != "" {
		params.DatabaseType = new(dbaassdk.DatabaseType(value))
	}
	return params
}

type BackupFinder struct {
	client    BackupLister
	projectID core.UUID
}

var _ finder.Interface[BackupFilter, dbaassdk.DBInstanceBackupSchema] = (*BackupFinder)(nil)

func NewBackupFinder(client BackupLister, projectID core.UUID) *BackupFinder {
	return &BackupFinder{client: client, projectID: projectID}
}

func (f *BackupFinder) Find(ctx context.Context, filter BackupFilter) ([]dbaassdk.DBInstanceBackupSchema, error) {
	return finder.Collect(f.client.ListDbInstanceBackupsIter(ctx, filter.params(f.projectID)), "DB instance backup", filter.String(), filter.matches)
}

func (f *BackupFinder) Resolve(ctx context.Context, filter BackupFilter) (dbaassdk.DBInstanceBackupSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return dbaassdk.DBInstanceBackupSchema{}, err
	}
	return finder.ExactlyOne(candidates, "DB instance backup", filter.String(), func(backup dbaassdk.DBInstanceBackupSchema) string {
		instance := "none"
		if source := core.NullableToPtr(backup.DbInstance); source != nil {
			instance = source.Id.String()
		}
		return fmt.Sprintf("id=%s (name=%q, backup_type=%q, db_instance_id=%s)", backup.Id, backup.Name, backup.BackupType, instance)
	})
}
