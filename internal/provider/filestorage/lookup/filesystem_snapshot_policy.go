package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type FilesystemSnapshotPolicyLister interface {
	ListFilesystemSnapshotPoliciesIter(context.Context, filestoragesdk.ListFilesystemSnapshotPoliciesParams) iter.Seq2[*filestoragesdk.FilesystemSnapshotPolicySchema, error]
}

type FilesystemSnapshotPolicyFilter struct {
	ID         *core.UUID
	Name       *string
	RegionName *string
}

func (f FilesystemSnapshotPolicyFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Text("region", f.RegionName))
}

func (f FilesystemSnapshotPolicyFilter) matches(policy *filestoragesdk.FilesystemSnapshotPolicySchema) bool {
	if f.ID != nil && policy.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && policy.Name != name {
		return false
	}
	if region := finder.Trim(f.RegionName); region != "" && policy.Region.Name != region {
		return false
	}
	return true
}

func (f FilesystemSnapshotPolicyFilter) params(projectID core.UUID) filestoragesdk.ListFilesystemSnapshotPoliciesParams {
	params := filestoragesdk.ListFilesystemSnapshotPoliciesParams{ProjectID: projectID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if region := finder.Trim(f.RegionName); region != "" {
		params.RegionName = &region
	}
	return params
}

type FilesystemSnapshotPolicyFinder struct {
	client    FilesystemSnapshotPolicyLister
	projectID core.UUID
}

var _ finder.Interface[FilesystemSnapshotPolicyFilter, filestoragesdk.FilesystemSnapshotPolicySchema] = (*FilesystemSnapshotPolicyFinder)(nil)

func NewFilesystemSnapshotPolicyFinder(client FilesystemSnapshotPolicyLister, projectID core.UUID) *FilesystemSnapshotPolicyFinder {
	return &FilesystemSnapshotPolicyFinder{client: client, projectID: projectID}
}

func (f *FilesystemSnapshotPolicyFinder) Find(ctx context.Context, filter FilesystemSnapshotPolicyFilter) ([]filestoragesdk.FilesystemSnapshotPolicySchema, error) {
	return finder.Collect(f.client.ListFilesystemSnapshotPoliciesIter(ctx, filter.params(f.projectID)), "filesystem snapshot policy", filter.String(), filter.matches)
}

func (f *FilesystemSnapshotPolicyFinder) Resolve(ctx context.Context, filter FilesystemSnapshotPolicyFilter) (filestoragesdk.FilesystemSnapshotPolicySchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return filestoragesdk.FilesystemSnapshotPolicySchema{}, err
	}
	return finder.ExactlyOne(candidates, "filesystem snapshot policy", filter.String(), func(policy filestoragesdk.FilesystemSnapshotPolicySchema) string {
		return fmt.Sprintf("id=%s (name=%q, region=%q)", policy.Id, policy.Name, policy.Region.Name)
	})
}
