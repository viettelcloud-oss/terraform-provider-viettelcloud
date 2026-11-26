package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type FilesystemSnapshotLister interface {
	ListFilesystemSnapshotIter(context.Context, filestoragesdk.ListFilesystemSnapshotParams) iter.Seq2[*filestoragesdk.FilesystemSnapshotSchema, error]
}

type FilesystemSnapshotFilter struct {
	ID           *core.UUID
	Name         *string
	FilesystemID *core.UUID
}

func (f FilesystemSnapshotFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Part("filesystem_id", f.FilesystemID))
}

func (f FilesystemSnapshotFilter) matches(snapshot *filestoragesdk.FilesystemSnapshotSchema) bool {
	if f.ID != nil && snapshot.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && snapshot.Name != name {
		return false
	}
	return f.FilesystemID == nil || snapshot.Filesystem.Id == *f.FilesystemID
}

func (f FilesystemSnapshotFilter) params(projectID core.UUID) filestoragesdk.ListFilesystemSnapshotParams {
	params := filestoragesdk.ListFilesystemSnapshotParams{ProjectID: projectID, FilesystemId: f.FilesystemID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	return params
}

type FilesystemSnapshotFinder struct {
	client    FilesystemSnapshotLister
	projectID core.UUID
}

var _ finder.Interface[FilesystemSnapshotFilter, filestoragesdk.FilesystemSnapshotSchema] = (*FilesystemSnapshotFinder)(nil)

func NewFilesystemSnapshotFinder(client FilesystemSnapshotLister, projectID core.UUID) *FilesystemSnapshotFinder {
	return &FilesystemSnapshotFinder{client: client, projectID: projectID}
}

func (f *FilesystemSnapshotFinder) Find(ctx context.Context, filter FilesystemSnapshotFilter) ([]filestoragesdk.FilesystemSnapshotSchema, error) {
	return finder.Collect(f.client.ListFilesystemSnapshotIter(ctx, filter.params(f.projectID)), "filesystem snapshot", filter.String(), filter.matches)
}

func (f *FilesystemSnapshotFinder) Resolve(ctx context.Context, filter FilesystemSnapshotFilter) (filestoragesdk.FilesystemSnapshotSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return filestoragesdk.FilesystemSnapshotSchema{}, err
	}
	return finder.ExactlyOne(candidates, "filesystem snapshot", filter.String(), func(snapshot filestoragesdk.FilesystemSnapshotSchema) string {
		return fmt.Sprintf("id=%s (name=%q, filesystem_id=%s)", snapshot.Id, snapshot.Name, snapshot.Filesystem.Id)
	})
}
