package lookup

import (
	"context"
	"iter"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

type fakeBackupLister struct {
	items  []*dbaassdk.DBInstanceBackupSchema
	err    error
	params dbaassdk.ListDbInstanceBackupsParams
}

func (f *fakeBackupLister) ListDbInstanceBackupsIter(_ context.Context, params dbaassdk.ListDbInstanceBackupsParams) iter.Seq2[*dbaassdk.DBInstanceBackupSchema, error] {
	f.params = params
	return fakeSeq(f.items, f.err)
}

func TestBackupFinder(t *testing.T) {
	t.Parallel()
	projectID, instanceID, regionID := core.UUID{9}, core.UUID{1}, core.UUID{2}
	instance := &dbaassdk.NestedDBInstanceSchema{Id: instanceID, Name: "orders"}
	match := &dbaassdk.DBInstanceBackupSchema{Id: core.UUID{3}, Name: "nightly", DbInstance: core.NullableFromPtr(instance), BackupType: dbaassdk.BackupTypeManual}
	prefix := &dbaassdk.DBInstanceBackupSchema{Id: core.UUID{4}, Name: "nightly-old", DbInstance: core.NullableFromPtr(instance), BackupType: dbaassdk.BackupTypeManual}
	filter := BackupFilter{Name: new(" nightly "), DBInstanceID: &instanceID, BackupType: new(dbaassdk.BackupTypeManual)}
	lister := &fakeBackupLister{}
	assertCardinality(t, match, prefix, func(items []*dbaassdk.DBInstanceBackupSchema, err error) (dbaassdk.DBInstanceBackupSchema, error) {
		lister.items, lister.err = items, err
		return NewBackupFinder(lister, projectID).Resolve(background(), filter)
	}, func(backup dbaassdk.DBInstanceBackupSchema) string { return backup.Id.String() })
	if lister.params.ProjectID != projectID || *lister.params.Name != "nightly" || len(*lister.params.DbInstanceId) != 1 || (*lister.params.DbInstanceId)[0] != instanceID {
		t.Fatalf("name, db_instance_id, and project must reach the backend: %#v", lister.params)
	}

	automatic := *match
	automatic.Id, automatic.BackupType = core.UUID{5}, dbaassdk.BackupTypeAutomatic
	orphaned := *match
	orphaned.Id = core.UUID{6}
	orphaned.DbInstance.SetNull()
	lister.items, lister.err = []*dbaassdk.DBInstanceBackupSchema{&automatic, &orphaned, match}, nil
	found, err := NewBackupFinder(lister, projectID).Find(background(), filter)
	if err != nil || len(found) != 1 || found[0].Id != match.Id {
		t.Fatalf("backup_type must be filtered locally and a backup without its instance must not match: %v %v", found, err)
	}

	completed := *match
	completed.Status, completed.DatabaseType, completed.Region = dbaassdk.BackupStatusCOMPLETED, dbaassdk.DatabaseTypeMysql, dbaassdk.NestedRegionSchema{Id: regionID}
	other := completed
	other.Id, other.Region.Id = core.UUID{7}, core.UUID{8}
	lister.items = []*dbaassdk.DBInstanceBackupSchema{&other, &completed}
	byStatus := BackupFilter{Status: new(dbaassdk.BackupStatusCOMPLETED), DatabaseType: new(dbaassdk.DatabaseTypeMysql), RegionID: &regionID}
	got, err := NewBackupFinder(lister, projectID).Resolve(background(), byStatus)
	if err != nil || got.Id != completed.Id {
		t.Fatalf("status, database_type, and region filter: %v %v", got.Id, err)
	}
	if (*lister.params.Status)[0] != dbaassdk.BackupStatusCOMPLETED || *lister.params.DatabaseType != dbaassdk.DatabaseTypeMysql || *lister.params.RegionId != regionID {
		t.Fatalf("status, database_type, and region_id must reach the backend: %#v", lister.params)
	}
}
