package lookup

import (
	"context"
	"fmt"
	"iter"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type VolumeSnapshotPolicyLister interface {
	ListSnapshotPoliciesIter(context.Context, blockstoragesdk.ListSnapshotPoliciesParams) iter.Seq2[*blockstoragesdk.SnapshotPolicySchema, error]
}

// VolumeSnapshotPolicyFilter selects policies by exact criteria. The API
// filters by region ID, so a caller that has only a region name resolves it
// first and sets RegionID. RegionName then makes the description readable and
// the client-side match exact.
type VolumeSnapshotPolicyFilter struct {
	ID         *core.UUID
	Name       *string
	RegionID   *core.UUID
	RegionName *string
	Status     *blockstoragesdk.SnapshotPolicyStatus
}

func (f VolumeSnapshotPolicyFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Part("region_id", f.RegionID), finder.Text("region", f.RegionName), finder.Part("status", f.Status))
}

func (f VolumeSnapshotPolicyFilter) matches(policy *blockstoragesdk.SnapshotPolicySchema) bool {
	if f.ID != nil && policy.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && policy.Name != name {
		return false
	}
	if f.RegionID != nil && policy.Region.Id != *f.RegionID {
		return false
	}
	if region := finder.Trim(f.RegionName); region != "" && policy.Region.Name != region {
		return false
	}
	return f.Status == nil || policy.Status == *f.Status
}

func (f VolumeSnapshotPolicyFilter) params(projectID core.UUID) blockstoragesdk.ListSnapshotPoliciesParams {
	params := blockstoragesdk.ListSnapshotPoliciesParams{ProjectID: projectID, RegionId: f.RegionID, Status: f.Status}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type VolumeSnapshotPolicyFinder struct {
	client    VolumeSnapshotPolicyLister
	projectID core.UUID
}

var _ finder.Interface[VolumeSnapshotPolicyFilter, blockstoragesdk.SnapshotPolicySchema] = (*VolumeSnapshotPolicyFinder)(nil)

func NewVolumeSnapshotPolicyFinder(client VolumeSnapshotPolicyLister, projectID core.UUID) *VolumeSnapshotPolicyFinder {
	return &VolumeSnapshotPolicyFinder{client: client, projectID: projectID}
}

func (f *VolumeSnapshotPolicyFinder) Find(ctx context.Context, filter VolumeSnapshotPolicyFilter) ([]blockstoragesdk.SnapshotPolicySchema, error) {
	return finder.Collect(f.client.ListSnapshotPoliciesIter(ctx, filter.params(f.projectID)), "volume snapshot policy", filter.String(), filter.matches)
}

func (f *VolumeSnapshotPolicyFinder) Resolve(ctx context.Context, filter VolumeSnapshotPolicyFilter) (blockstoragesdk.SnapshotPolicySchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.SnapshotPolicySchema{}, err
	}
	return finder.ExactlyOne(candidates, "volume snapshot policy", filter.String(), func(policy blockstoragesdk.SnapshotPolicySchema) string {
		return fmt.Sprintf("id=%s (name=%q, region=%q)", policy.Id, policy.Name, policy.Region.Name)
	})
}
