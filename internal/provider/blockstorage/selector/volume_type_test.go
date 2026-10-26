package selector

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
)

func volumeTypeCriteria(values map[string]attr.Value) types.Object {
	complete := map[string]attr.Value{}
	for name, typ := range VolumeTypeAttributeTypes() {
		complete[name] = types.StringNull()
		if typ.Equal(types.BoolType) {
			complete[name] = types.BoolNull()
		}
		if value, ok := values[name]; ok {
			complete[name] = value
		}
	}
	return types.ObjectValueMust(VolumeTypeAttributeTypes(), complete)
}

func TestVolumeTypeAttributeTypesFollowSchema(t *testing.T) {
	t.Parallel()
	want := map[string]attr.Type{"id": types.StringType, "name": types.StringType, "disk_type": types.StringType, "speed": types.StringType, "multiattach": types.BoolType, "encryption_key_size": types.StringType}
	if got := VolumeTypeAttributeTypes(); !(types.ObjectType{AttrTypes: got}).Equal(types.ObjectType{AttrTypes: want}) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestResolveVolumeTypePassesTrimmedTypedCriteriaAndScope(t *testing.T) {
	t.Parallel()
	value := volumeTypeCriteria(map[string]attr.Value{"name": types.StringValue(" fast "), "disk_type": types.StringValue(" ssd "), "speed": types.StringValue(" 10k "), "multiattach": types.BoolValue(false), "encryption_key_size": types.StringValue(" 0 ")})
	zoneID := core.UUID{9}
	got, diags := ResolveVolumeType(context.Background(), value, "boot.volume_type", &zoneID, 30, func(_ context.Context, req blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
		f := req.Filter
		if *f.Name != "fast" || *f.DiskType != blockstoragesdk.VolumeTypeDiskTypeSsd || string(*f.Speed) != "10k" || f.Multiattach == nil || *f.Multiattach || *f.EncryptionKeySize != blockstoragesdk.VolumeTypeEncryptKeySizeN0 || req.ZoneID != &zoneID || req.RequestedSize != 30 {
			t.Fatalf("unexpected criteria: %#v", req)
		}
		return blockstoragesdk.VolumeTypeSchema{Id: core.UUID{2}}, nil
	})
	if diags.HasError() || got.Id != (core.UUID{2}) {
		t.Fatalf("resolve type: %v", diags)
	}
}

func TestResolveVolumeTypeStopsBeforeLookupAndReportsErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, diags := ResolveVolumeType(ctx, volumeTypeCriteria(nil), "data_volumes[0].volume_type", nil, 30, func(context.Context, blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
		t.Fatal("empty selector reached lookup")
		return blockstoragesdk.VolumeTypeSchema{}, nil
	})
	if !diags.HasError() {
		t.Fatal("expected empty selector diagnostic")
	}
	_, diags = ResolveVolumeType(ctx, volumeTypeCriteria(map[string]attr.Value{"multiattach": types.BoolValue(false)}), "create_from.volume_type", nil, 30, func(context.Context, blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
		return blockstoragesdk.VolumeTypeSchema{}, errors.New("not found")
	})
	if !diags.HasError() || diags[0].Summary() != "Unable to resolve volume type" {
		t.Fatalf("expected lookup diagnostic: %v", diags)
	}
}
