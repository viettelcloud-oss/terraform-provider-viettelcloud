package lookup

import (
	"context"
	"fmt"
	"iter"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type VolumeBackupPolicyLister interface {
	ListVolumeBackupPoliciesIter(context.Context, blockstoragesdk.ListVolumeBackupPoliciesParams) iter.Seq2[*blockstoragesdk.VolumeBackupPolicySchema, error]
}

type VolumeBackupPolicyFilter struct {
	ID       *core.UUID
	Name     *string
	RegionID *core.UUID
	Status   *blockstoragesdk.VolumeBackupPolicyStatus
}

func (f VolumeBackupPolicyFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Part("region_id", f.RegionID), finder.Part("status", f.Status))
}

func (f VolumeBackupPolicyFilter) matches(policy *blockstoragesdk.VolumeBackupPolicySchema) bool {
	if f.ID != nil && policy.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && policy.Name != name {
		return false
	}
	if f.RegionID != nil && policy.Region.Id != *f.RegionID {
		return false
	}
	return f.Status == nil || policy.Status == *f.Status
}

func (f VolumeBackupPolicyFilter) params(projectID core.UUID) blockstoragesdk.ListVolumeBackupPoliciesParams {
	params := blockstoragesdk.ListVolumeBackupPoliciesParams{ProjectID: projectID, RegionId: f.RegionID, Status: f.Status}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type VolumeBackupPolicyFinder struct {
	client    VolumeBackupPolicyLister
	projectID core.UUID
}

var _ finder.Interface[VolumeBackupPolicyFilter, blockstoragesdk.VolumeBackupPolicySchema] = (*VolumeBackupPolicyFinder)(nil)

func NewVolumeBackupPolicyFinder(client VolumeBackupPolicyLister, projectID core.UUID) *VolumeBackupPolicyFinder {
	return &VolumeBackupPolicyFinder{client: client, projectID: projectID}
}

func (f *VolumeBackupPolicyFinder) Find(ctx context.Context, filter VolumeBackupPolicyFilter) ([]blockstoragesdk.VolumeBackupPolicySchema, error) {
	return finder.Collect(f.client.ListVolumeBackupPoliciesIter(ctx, filter.params(f.projectID)), "volume backup policy", filter.String(), filter.matches)
}

func (f *VolumeBackupPolicyFinder) Resolve(ctx context.Context, filter VolumeBackupPolicyFilter) (blockstoragesdk.VolumeBackupPolicySchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.VolumeBackupPolicySchema{}, err
	}
	return finder.ExactlyOne(candidates, "volume backup policy", filter.String(), func(policy blockstoragesdk.VolumeBackupPolicySchema) string {
		return fmt.Sprintf("id=%s (name=%q, region=%q, status=%q)", policy.Id, policy.Name, policy.Region.Name, policy.Status)
	})
}
