package lookup

import (
	"context"
	"fmt"
	"iter"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type VolumeSnapshotLister interface {
	ListVolumeSnapshotsIter(context.Context, blockstoragesdk.ListVolumeSnapshotsParams) iter.Seq2[*blockstoragesdk.VolumeSnapshotSchema, error]
}

type VolumeSnapshotFilter struct {
	ID       *core.UUID
	Name     *string
	VolumeID *core.UUID
	Zone     *string
	Status   *blockstoragesdk.SnapshotStatus
}

func (f VolumeSnapshotFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Part("volume_id", f.VolumeID), finder.Text("zone", f.Zone), finder.Part("status", f.Status))
}

func (f VolumeSnapshotFilter) matches(snapshot *blockstoragesdk.VolumeSnapshotSchema) bool {
	if f.ID != nil && snapshot.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && snapshot.Name != name {
		return false
	}
	if f.VolumeID != nil && snapshot.Volume.Id != *f.VolumeID {
		return false
	}
	if zone := finder.Trim(f.Zone); zone != "" && snapshot.Zone.Name != zone {
		return false
	}
	return f.Status == nil || snapshot.Status != nil && *snapshot.Status == *f.Status
}

func (f VolumeSnapshotFilter) params(projectID core.UUID) blockstoragesdk.ListVolumeSnapshotsParams {
	params := blockstoragesdk.ListVolumeSnapshotsParams{ProjectID: projectID, VolumeId: f.VolumeID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if zone := finder.Trim(f.Zone); zone != "" {
		params.Zone = &zone
	}
	if f.Status != nil {
		values := []blockstoragesdk.SnapshotStatus{*f.Status}
		params.Status = &values
	}
	return params
}

type VolumeSnapshotFinder struct {
	client    VolumeSnapshotLister
	projectID core.UUID
}

var _ finder.Interface[VolumeSnapshotFilter, blockstoragesdk.VolumeSnapshotSchema] = (*VolumeSnapshotFinder)(nil)

func NewVolumeSnapshotFinder(client VolumeSnapshotLister, projectID core.UUID) *VolumeSnapshotFinder {
	return &VolumeSnapshotFinder{client: client, projectID: projectID}
}

func (f *VolumeSnapshotFinder) Find(ctx context.Context, filter VolumeSnapshotFilter) ([]blockstoragesdk.VolumeSnapshotSchema, error) {
	return finder.Collect(f.client.ListVolumeSnapshotsIter(ctx, filter.params(f.projectID)), "volume snapshot", filter.String(), filter.matches)
}

func (f *VolumeSnapshotFinder) Resolve(ctx context.Context, filter VolumeSnapshotFilter) (blockstoragesdk.VolumeSnapshotSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.VolumeSnapshotSchema{}, err
	}
	return finder.ExactlyOne(candidates, "volume snapshot", filter.String(), func(snapshot blockstoragesdk.VolumeSnapshotSchema) string {
		return fmt.Sprintf("id=%s (name=%q, volume_id=%s, zone=%q)", snapshot.Id, snapshot.Name, snapshot.Volume.Id, snapshot.Zone.Name)
	})
}
