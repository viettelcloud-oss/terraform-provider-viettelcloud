package selector

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
)

func imageCriteria(values map[string]attr.Value) types.Object {
	complete := map[string]attr.Value{}
	for name := range ImageAttributeTypes() {
		complete[name] = types.StringNull()
		if value, ok := values[name]; ok {
			complete[name] = value
		}
	}
	return types.ObjectValueMust(ImageAttributeTypes(), complete)
}

func TestImageAttributeTypesFollowSchema(t *testing.T) {
	t.Parallel()
	want := map[string]attr.Type{"id": types.StringType, "name": types.StringType, "os": types.StringType, "version": types.StringType, "type": types.StringType}
	if got := ImageAttributeTypes(); !(types.ObjectType{AttrTypes: got}).Equal(types.ObjectType{AttrTypes: want}) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestResolveImagePassesTrimmedTypedCriteria(t *testing.T) {
	t.Parallel()
	value := imageCriteria(map[string]attr.Value{"name": types.StringValue(" ubuntu "), "os": types.StringValue(" Ubuntu "), "version": types.StringValue(" 22.04 "), "type": types.StringValue(" server ")})
	got, diags := ResolveImage(context.Background(), value, "boot.image", func(_ context.Context, filter serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
		if *filter.Name != "ubuntu" || *filter.OS != "Ubuntu" || *filter.Version != "22.04" || *filter.Type != serversdk.ImageTypeServer {
			t.Fatalf("unexpected criteria: %#v", filter)
		}
		return serversdk.ImageSchema{Id: core.UUID{1}}, nil
	})
	if diags.HasError() || got.Id != (core.UUID{1}) {
		t.Fatalf("resolve image: %v", diags)
	}
}

func TestResolveImageStopsBeforeLookupAndReportsErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, diags := ResolveImage(ctx, imageCriteria(map[string]attr.Value{"name": types.StringUnknown()}), "boot.image", func(context.Context, serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
		t.Fatal("unknown criterion reached lookup")
		return serversdk.ImageSchema{}, nil
	})
	if !diags.HasError() {
		t.Fatal("expected unknown diagnostic")
	}
	_, diags = ResolveImage(ctx, imageCriteria(map[string]attr.Value{"os": types.StringValue("Ubuntu")}), "create_from.image", func(context.Context, serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
		return serversdk.ImageSchema{}, errors.New("ambiguous candidates")
	})
	if !diags.HasError() || diags[0].Summary() != "Unable to resolve image" {
		t.Fatalf("expected lookup diagnostic: %v", diags)
	}
}
