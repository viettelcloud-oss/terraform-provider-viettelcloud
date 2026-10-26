package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type ImageLister interface {
	ListImagesIter(context.Context, serversdk.ListImagesParams) iter.Seq2[*serversdk.ImageSchema, error]
}

type ImageFilter struct {
	ID      *core.UUID
	Name    *string
	OS      *string
	Version *string
	Type    *serversdk.ImageType
	Status  *[]serversdk.ImageStatus
	State   *[]serversdk.ImageState
}

func (f ImageFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Text("os", f.OS), finder.Text("version", f.Version), finder.Text("type", (*string)(f.Type)), finder.Part("status", f.Status), finder.Part("state", f.State))
}

// matches reports whether the image satisfies every criterion the filter can
// check locally. ImageSchema does not expose status or state, so those two are
// enforced by the backend list filters instead.
func (f ImageFilter) matches(image *serversdk.ImageSchema) bool {
	if f.ID != nil && image.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && image.Name != name {
		return false
	}
	if os := finder.Trim(f.OS); os != "" && (image.Os == nil || *image.Os != os) {
		return false
	}
	if version := finder.Trim(f.Version); version != "" && (image.Version == nil || *image.Version != version) {
		return false
	}
	if imageType := finder.Trim((*string)(f.Type)); imageType != "" && (image.Type == nil || string(*image.Type) != imageType) {
		return false
	}
	return true
}

func (f ImageFilter) params(projectID core.UUID) serversdk.ListImagesParams {
	params := serversdk.ListImagesParams{ProjectID: projectID, Id: f.ID, Status: f.Status, State: f.State}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if os := finder.Trim(f.OS); os != "" {
		params.Os = &os
	}
	if imageType := finder.Trim((*string)(f.Type)); imageType != "" {
		params.Type = new(serversdk.ImageType(imageType))
	}
	return params
}

type ImageFinder struct {
	client    ImageLister
	projectID core.UUID
}

type ImageResolveFunc func(context.Context, ImageFilter) (serversdk.ImageSchema, error)

var _ finder.Interface[ImageFilter, serversdk.ImageSchema] = (*ImageFinder)(nil)

func NewImageFinder(client ImageLister, projectID core.UUID) *ImageFinder {
	return &ImageFinder{client: client, projectID: projectID}
}

func (f *ImageFinder) Find(ctx context.Context, filter ImageFilter) ([]serversdk.ImageSchema, error) {
	return finder.Collect(f.client.ListImagesIter(ctx, filter.params(f.projectID)), "image", filter.String(), filter.matches)
}

func (f *ImageFinder) Resolve(ctx context.Context, filter ImageFilter) (serversdk.ImageSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return serversdk.ImageSchema{}, err
	}
	return finder.ExactlyOne(candidates, "image", filter.String(), imageSummary)
}

func imageSummary(image serversdk.ImageSchema) string {
	return fmt.Sprintf("id=%s (name=%q, os=%q, version=%q, type=%q)", image.Id, image.Name, finder.Trim(image.Os), finder.Trim(image.Version), finder.Trim((*string)(image.Type)))
}
