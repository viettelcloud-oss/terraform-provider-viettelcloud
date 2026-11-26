// Package lookup defines finders for filestorage-owned entities.
package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

// FilesystemTypeLister is the slice of the filestorage client that the finder
// uses for paged listing.
type FilesystemTypeLister interface {
	ListFilesystemTypeIter(context.Context, filestoragepkg.ListFilesystemTypeParams) iter.Seq2[*filestoragepkg.FilesystemTypeSchema, error]
}

// FilesystemTypeFilter holds the optional matching criteria for a filesystem
// type lookup.
type FilesystemTypeFilter struct {
	Name     *string
	ZoneID   *core.UUID
	ZoneName *string
}

func (f FilesystemTypeFilter) String() string {
	return finder.Criteria(
		finder.Text("name", f.Name),
		finder.Part("zone_id", f.ZoneID),
		finder.Text("zone_name", f.ZoneName),
	)
}

func (f FilesystemTypeFilter) matches(ft *filestoragepkg.FilesystemTypeSchema) bool {
	if name := finder.Trim(f.Name); name != "" && ft.Name != name {
		return false
	}
	if f.ZoneID != nil && ft.Zone.Id != *f.ZoneID {
		return false
	}
	if zoneName := finder.Trim(f.ZoneName); zoneName != "" && ft.Zone.Name != zoneName {
		return false
	}
	return true
}

func (f FilesystemTypeFilter) params() filestoragepkg.ListFilesystemTypeParams {
	params := filestoragepkg.ListFilesystemTypeParams{ZoneId: f.ZoneID}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if zoneName := finder.Trim(f.ZoneName); zoneName != "" {
		params.ZoneName = &zoneName
	}
	return params
}

// FilesystemTypeFinder resolves filesystem types by filter criteria.
type FilesystemTypeFinder struct {
	client FilesystemTypeLister
}

// FilesystemTypeResolveFunc is a dependency-injectable resolve call.
type FilesystemTypeResolveFunc func(context.Context, FilesystemTypeFilter) (filestoragepkg.FilesystemTypeSchema, error)

var _ finder.Interface[FilesystemTypeFilter, filestoragepkg.FilesystemTypeSchema] = (*FilesystemTypeFinder)(nil)

// NewFilesystemTypeFinder returns a finder backed by the given lister.
func NewFilesystemTypeFinder(client FilesystemTypeLister) *FilesystemTypeFinder {
	return &FilesystemTypeFinder{client: client}
}

// Find returns all filesystem types that satisfy filter.
func (f *FilesystemTypeFinder) Find(ctx context.Context, filter FilesystemTypeFilter) ([]filestoragepkg.FilesystemTypeSchema, error) {
	return finder.Collect(f.client.ListFilesystemTypeIter(ctx, filter.params()), "filesystem type", filter.String(), filter.matches)
}

// Resolve returns exactly one enabled filesystem type for the given filter.
func (f *FilesystemTypeFinder) Resolve(ctx context.Context, filter FilesystemTypeFilter) (filestoragepkg.FilesystemTypeSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return filestoragepkg.FilesystemTypeSchema{}, err
	}
	enabled := make([]filestoragepkg.FilesystemTypeSchema, 0, len(candidates))
	for _, c := range candidates {
		if st := core.NullableToPtr(c.Status); st != nil && *st == filestoragepkg.FilesystemTypeStatusEnabled {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 && len(candidates) > 0 {
		summaries := make([]string, 0, len(candidates))
		for _, c := range candidates {
			summaries = append(summaries, filesystemTypeSummary(c))
		}
		return filestoragepkg.FilesystemTypeSchema{}, fmt.Errorf(
			"filesystem type (%s) has no enabled candidate: %s",
			filter.String(), summaries,
		)
	}
	if len(enabled) > 0 {
		return finder.ExactlyOne(enabled, "filesystem type", filter.String(), filesystemTypeSummary)
	}
	return finder.ExactlyOne(candidates, "filesystem type", filter.String(), filesystemTypeSummary)
}

func filesystemTypeSummary(ft filestoragepkg.FilesystemTypeSchema) string {
	status := ""
	if st := core.NullableToPtr(ft.Status); st != nil {
		status = string(*st)
	}
	return fmt.Sprintf("id=%s (name=%q, zone=%q, status=%s)", ft.Id, ft.Name, ft.Zone.Name, status)
}
