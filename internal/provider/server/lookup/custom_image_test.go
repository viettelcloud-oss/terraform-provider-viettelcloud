package lookup

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

type customImageFakeLister struct {
	images []serversdk.CustomImageSchema
	err    error
	params serversdk.ListCustomImagesParams
}

func (f *customImageFakeLister) ListCustomImagesIter(_ context.Context, params serversdk.ListCustomImagesParams) iter.Seq2[*serversdk.CustomImageSchema, error] {
	f.params = params
	return func(yield func(*serversdk.CustomImageSchema, error) bool) {
		for _, image := range f.images {
			if !yield(&image, nil) {
				return
			}
		}
		if f.err != nil {
			yield(nil, f.err)
		}
	}
}

func TestCustomImageFinder(t *testing.T) {
	t.Parallel()
	image := serversdk.CustomImageSchema{Id: core.UUID{1}, Name: "image", Region: serversdk.NestedRegionSchema{Name: "region"}, Volume: &serversdk.NestedVolumeSchema{Id: core.UUID{2}, Name: "source"}, Os: new("Linux"), Version: new("1"), State: serversdk.ImageStateEnabled, Status: serversdk.ImageStatusActive}
	filter := CustomImageFilter{ID: &image.Id, Name: new(" image "), Volume: new(" source "), VolumeID: &image.Volume.Id, Region: new(" region "), OS: new("Linux"), Version: new("1"), State: &image.State, Status: &image.Status}
	fake := &customImageFakeLister{images: []serversdk.CustomImageSchema{image}}
	candidate, err := NewCustomImageFinder(fake, core.UUID{4}).Resolve(context.Background(), filter)
	if err != nil || candidate.Id != image.Id || fake.params.ProjectID != (core.UUID{4}) || *fake.params.Name != "image" || *fake.params.Volume != "source" || *fake.params.Region != "region" || *fake.params.Os != "Linux" || *fake.params.Version != "1" || (*fake.params.State)[0] != image.State || (*fake.params.Status)[0] != image.Status {
		t.Fatalf("%#v %v %#v", candidate, err, fake.params)
	}
	for _, mismatch := range []CustomImageFilter{
		{ID: new(core.UUID{9})}, {Name: new("other")}, {Volume: new("other")}, {VolumeID: new(core.UUID{9})}, {Region: new("other")}, {OS: new("other")}, {Version: new("other")}, {State: new(serversdk.ImageStateDisabled)}, {Status: new(serversdk.ImageStatusSaving)},
	} {
		if mismatch.matches(&image) {
			t.Fatalf("false match %#v", mismatch)
		}
	}
	image.Volume = nil
	image.Os = nil
	image.Version = nil
	if filter.matches(&image) {
		t.Fatal("nil source matched")
	}
	for _, test := range []struct {
		images []serversdk.CustomImageSchema
		err    error
	}{
		{}, {images: []serversdk.CustomImageSchema{candidate, candidate}}, {err: errors.New("list failed")},
	} {
		fake.images, fake.err = test.images, test.err
		_, err := NewCustomImageFinder(fake, core.UUID{4}).Resolve(context.Background(), CustomImageFilter{Name: new("image")})
		if err == nil {
			t.Fatal("expected lookup error")
		}
	}
}
