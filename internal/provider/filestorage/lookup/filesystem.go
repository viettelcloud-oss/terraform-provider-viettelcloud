package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// FilesystemLister is the slice of the filestorage client that the finder uses.
type FilesystemLister interface {
	ListFilesystemIter(context.Context, filestoragepkg.ListFilesystemParams) iter.Seq2[*filestoragepkg.FilesystemSchema, error]
}

// FilesystemFilter holds the optional matching criteria for a filesystem lookup.
type FilesystemFilter struct {
	Name     *string
	Zone     *string
	SubnetID *core.UUID
}

func (f FilesystemFilter) String() string {
	return finder.Criteria(
		finder.Text("name", f.Name),
		finder.Text("zone", f.Zone),
		finder.Part("subnet_id", f.SubnetID),
	)
}

func (f FilesystemFilter) matches(fs *filestoragepkg.FilesystemSchema) bool {
	if name := finder.Trim(f.Name); name != "" && fs.Name != name {
		return false
	}
	if zone := finder.Trim(f.Zone); zone != "" && fs.Zone.Name != zone {
		return false
	}
	if f.SubnetID != nil && fs.Subnet.Id != *f.SubnetID {
		return false
	}
	return true
}

func (f FilesystemFilter) params(projectID core.UUID) filestoragepkg.ListFilesystemParams {
	params := filestoragepkg.ListFilesystemParams{ProjectID: projectID, SubnetId: f.SubnetID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if zone := finder.Trim(f.Zone); zone != "" {
		params.Zone = &zone
	}
	return params
}

// FilesystemFinder resolves filesystems by filter criteria within a project.
type FilesystemFinder struct {
	client    FilesystemLister
	projectID core.UUID
}

var _ finder.Interface[FilesystemFilter, filestoragepkg.FilesystemSchema] = (*FilesystemFinder)(nil)

// NewFilesystemFinder returns a finder backed by the given lister.
func NewFilesystemFinder(client FilesystemLister, projectID core.UUID) *FilesystemFinder {
	return &FilesystemFinder{client: client, projectID: projectID}
}

// Find returns all filesystems that satisfy filter.
func (f *FilesystemFinder) Find(ctx context.Context, filter FilesystemFilter) ([]filestoragepkg.FilesystemSchema, error) {
	return finder.Collect(f.client.ListFilesystemIter(ctx, filter.params(f.projectID)), "filesystem", filter.String(), filter.matches)
}

// Resolve returns exactly one filesystem for the given filter.
func (f *FilesystemFinder) Resolve(ctx context.Context, filter FilesystemFilter) (filestoragepkg.FilesystemSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return filestoragepkg.FilesystemSchema{}, err
	}
	return finder.ExactlyOne(candidates, "filesystem", filter.String(), filesystemSummary)
}

func filesystemSummary(fs filestoragepkg.FilesystemSchema) string {
	return fmt.Sprintf("id=%s (name=%q, zone=%q, subnet_id=%s)", fs.Id, fs.Name, fs.Zone.Name, fs.Subnet.Id)
}
