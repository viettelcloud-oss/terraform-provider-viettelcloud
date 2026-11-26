package selector

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
)

func TestAttributesAreRequiredSelectors(t *testing.T) {
	t.Parallel()
	for name, attribute := range map[string]interface {
		IsRequired() bool
		IsOptional() bool
	}{"db_version": DBVersionAttribute(""), "flavor": FlavorAttribute(""), "volume_type": VolumeTypeAttribute("")} {
		if !attribute.IsRequired() || attribute.IsOptional() {
			t.Fatalf("%s must be Required", name)
		}
	}
	if _, ok := DBVersionAttributeTypes()[DBVersionCriterion]; !ok || len(DBVersionAttributeTypes()) != 2 {
		t.Fatalf("db_version criteria: %v", DBVersionAttributeTypes())
	}
}

func TestFiltersCarryTheInstanceScope(t *testing.T) {
	t.Parallel()
	region := core.UUID{1}
	value := func(types map[string]attr.Type, criterion string) types.Object {
		return typesObject(types, criterion, " 16 ")
	}
	version, diags := DBVersionFilter(value(DBVersionAttributeTypes(), DBVersionCriterion), "db_version", dbaassdk.DatabaseTypeMysql, region)
	if diags.HasError() || *version.Version != "16" || *version.DatabaseType != dbaassdk.DatabaseTypeMysql || *version.RegionID != region {
		t.Fatalf("db_version filter: %#v %v", version, diags)
	}
	flavor, diags := FlavorFilter(value(FlavorAttributeTypes(), "name"), "flavor", region)
	if diags.HasError() || *flavor.Name != "16" || *flavor.RegionID != region {
		t.Fatalf("flavor filter: %#v %v", flavor, diags)
	}
	volumeType, diags := VolumeTypeFilter(value(VolumeTypeAttributeTypes(), "name"), "volume_type", region)
	if diags.HasError() || *volumeType.Name != "16" || *volumeType.RegionID != region {
		t.Fatalf("volume_type filter: %#v %v", volumeType, diags)
	}
	if _, diags := FlavorFilter(typesObject(FlavorAttributeTypes(), "name", " "), "flavor", region); !diags.HasError() {
		t.Fatal("a blank criterion must be rejected")
	}
}

func typesObject(attributeTypes map[string]attr.Type, criterion, value string) types.Object {
	return types.ObjectValueMust(attributeTypes, map[string]attr.Value{criterion: types.StringValue(value), "id": types.StringNull()})
}
