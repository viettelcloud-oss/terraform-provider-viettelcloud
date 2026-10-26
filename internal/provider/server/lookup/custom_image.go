package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"
)

type CustomImageLister interface {
	ListCustomImagesIter(context.Context, serversdk.ListCustomImagesParams) iter.Seq2[*serversdk.CustomImageSchema, error]
}
type CustomImageFilter struct {
	ID       *core.UUID
	Name     *string
	Volume   *string
	VolumeID *core.UUID
	Region   *string
	OS       *string
	Version  *string
	State    *serversdk.ImageState
	Status   *serversdk.ImageStatus
}

func (f CustomImageFilter) String() string {
	return finder.Criteria(finder.Part("id", f.ID), finder.Text("name", f.Name), finder.Text("volume", f.Volume), finder.Part("volume_id", f.VolumeID), finder.Text("region", f.Region), finder.Text("os", f.OS), finder.Text("version", f.Version), finder.Part("state", f.State), finder.Part("status", f.Status))
}

func (f CustomImageFilter) matches(image *serversdk.CustomImageSchema) bool {
	if f.ID != nil && image.Id != *f.ID {
		return false
	}
	for _, pair := range []struct {
		filter *string
		value  string
	}{{f.Name, image.Name}, {f.Region, image.Region.Name}} {
		if value := finder.Trim(pair.filter); value != "" && value != pair.value {
			return false
		}
	}
	if volume := finder.Trim(f.Volume); volume != "" && (image.Volume == nil || image.Volume.Name != volume) {
		return false
	}
	if f.VolumeID != nil && (image.Volume == nil || image.Volume.Id != *f.VolumeID) {
		return false
	}
	if os := finder.Trim(f.OS); os != "" && (image.Os == nil || *image.Os != os) {
		return false
	}
	if version := finder.Trim(f.Version); version != "" && (image.Version == nil || *image.Version != version) {
		return false
	}
	return (f.State == nil || image.State == *f.State) && (f.Status == nil || image.Status == *f.Status)
}

func (f CustomImageFilter) params(projectID core.UUID) serversdk.ListCustomImagesParams {
	params := serversdk.ListCustomImagesParams{ProjectID: projectID, VolumeId: f.VolumeID}
	for _, pair := range []struct {
		source *string
		target **string
	}{{f.Name, &params.Name}, {f.Volume, &params.Volume}, {f.Region, &params.Region}, {f.OS, &params.Os}, {f.Version, &params.Version}} {
		if value := finder.Trim(pair.source); value != "" {
			*pair.target = &value
		}
	}
	if f.State != nil {
		values := []serversdk.ImageState{*f.State}
		params.State = &values
	}
	if f.Status != nil {
		values := []serversdk.ImageStatus{*f.Status}
		params.Status = &values
	}
	return params
}

type CustomImageFinder struct {
	client    CustomImageLister
	projectID core.UUID
}

var _ finder.Interface[CustomImageFilter, serversdk.CustomImageSchema] = (*CustomImageFinder)(nil)

func NewCustomImageFinder(client CustomImageLister, projectID core.UUID) *CustomImageFinder {
	return &CustomImageFinder{client: client, projectID: projectID}
}

func (f *CustomImageFinder) Find(ctx context.Context, filter CustomImageFilter) ([]serversdk.CustomImageSchema, error) {
	return finder.Collect(f.client.ListCustomImagesIter(ctx, filter.params(f.projectID)), "custom image", filter.String(), filter.matches)
}

func (f *CustomImageFinder) Resolve(ctx context.Context, filter CustomImageFilter) (serversdk.CustomImageSchema, error) {
	images, err := f.Find(ctx, filter)
	if err != nil {
		return serversdk.CustomImageSchema{}, err
	}
	return finder.ExactlyOne(images, "custom image", filter.String(), customImageSummary)
}

func customImageSummary(image serversdk.CustomImageSchema) string {
	return fmt.Sprintf("id=%s (name=%q, region=%q)", image.Id, image.Name, image.Region.Name)
}
