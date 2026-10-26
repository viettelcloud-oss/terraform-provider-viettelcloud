package lookup

import (
	"context"
	"fmt"
	"iter"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type VolumeBackupLister interface {
	ListVolumeBackupsIter(context.Context, blockstoragesdk.ListVolumeBackupsParams) iter.Seq2[*blockstoragesdk.VolumeBackupSchema, error]
}

type VolumeBackupFilter struct {
	ID       *core.UUID
	Name     *string
	VolumeID *core.UUID
	Status   *blockstoragesdk.VolumeBackupStatus
	Size     *int
	Region   *string
}

func (f VolumeBackupFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Part("volume_id", f.VolumeID), finder.Part("status", f.Status), finder.Part("size", f.Size), finder.Text("region", f.Region))
}

func (f VolumeBackupFilter) matches(backup *blockstoragesdk.VolumeBackupSchema) bool {
	if f.ID != nil && backup.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && backup.Name != name {
		return false
	}
	if f.VolumeID != nil && (backup.Volume == nil || backup.Volume.Id != *f.VolumeID) {
		return false
	}
	if f.Status != nil && (backup.Status == nil || *backup.Status != *f.Status) {
		return false
	}
	if f.Size != nil && (backup.Size == nil || *backup.Size != *f.Size) {
		return false
	}
	return finder.Trim(f.Region) == "" || backup.Region.Name == finder.Trim(f.Region)
}

func (f VolumeBackupFilter) params(projectID core.UUID) blockstoragesdk.ListVolumeBackupsParams {
	params := blockstoragesdk.ListVolumeBackupsParams{ProjectID: projectID, VolumeId: f.VolumeID, Size: f.Size}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if f.Status != nil {
		statuses := []blockstoragesdk.VolumeBackupStatus{*f.Status}
		params.Status = &statuses
	}
	if region := finder.Trim(f.Region); region != "" {
		params.RegionName = &region
	}
	return params
}

type VolumeBackupFinder struct {
	client    VolumeBackupLister
	projectID core.UUID
}

var _ finder.Interface[VolumeBackupFilter, blockstoragesdk.VolumeBackupSchema] = (*VolumeBackupFinder)(nil)

func NewVolumeBackupFinder(client VolumeBackupLister, projectID core.UUID) *VolumeBackupFinder {
	return &VolumeBackupFinder{client: client, projectID: projectID}
}

func (f *VolumeBackupFinder) Find(ctx context.Context, filter VolumeBackupFilter) ([]blockstoragesdk.VolumeBackupSchema, error) {
	return finder.Collect(f.client.ListVolumeBackupsIter(ctx, filter.params(f.projectID)), "volume backup", filter.String(), filter.matches)
}

func (f *VolumeBackupFinder) Resolve(ctx context.Context, filter VolumeBackupFilter) (blockstoragesdk.VolumeBackupSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.VolumeBackupSchema{}, err
	}
	return finder.ExactlyOne(candidates, "volume backup", filter.String(), func(backup blockstoragesdk.VolumeBackupSchema) string {
		return fmt.Sprintf("id=%s (name=%q, region=%q)", backup.Id, backup.Name, backup.Region.Name)
	})
}
