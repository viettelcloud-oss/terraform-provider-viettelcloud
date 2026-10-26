package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

var (
	placementGroupTestUUID    = core.UUID{0x71}
	placementGroupTestRegion  = core.UUID{0x72}
	placementGroupProjectID   = core.UUID{4}
	placementGroupTestProject = serversdk.NestedProjectSchema{
		Id:   placementGroupProjectID,
		Name: "integration",
		Slug: "integration",
	}
)

func writePlacementGroupResponse(t *testing.T, w http.ResponseWriter, pg *serversdk.PlacementGroupSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(pg); err != nil {
		t.Errorf("encode Placement Group response: %v", err)
	}
}

func customImageFixture() *serversdk.CustomImageSchema {
	return &serversdk.CustomImageSchema{
		Id: core.UUID{0x81}, Name: "image-a", Description: "", DiskFormat: serversdk.ImageCustomFormatQcow2,
		ContainerFormat: serversdk.ImageCustomContainerFormatBare, State: serversdk.ImageStateEnabled, Status: serversdk.ImageStatusActive,
		EncryptKeySize: serversdk.VolumeTypeEncryptKeySizeN0,
		Volume:         &serversdk.NestedVolumeSchema{Id: core.UUID{0x82}, Name: "boot-source", Size: 20},
		Region:         serversdk.NestedRegionSchema{Id: core.UUID{0x83}, Name: "region-a"},
		Project:        placementGroupTestProject, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func customImageClient(t *testing.T, handler http.HandlerFunc) *serversdk.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := serversdk.NewClient(server.URL, serversdk.WithPAT("test"))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeCustomImageResponse(t *testing.T, w http.ResponseWriter, image *serversdk.CustomImageSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(image); err != nil {
		t.Error(err)
	}
}

func testImageSelector(name types.String) types.Object {
	if name.IsNull() {
		return types.ObjectNull(serverselector.ImageAttributeTypes())
	}
	if name.IsUnknown() {
		return types.ObjectUnknown(serverselector.ImageAttributeTypes())
	}
	return selectorTestCriteria(serverselector.ImageAttributeTypes(), map[string]attr.Value{"name": name})
}

func testVolumeTypeSelector(name types.String) types.Object {
	if name.IsNull() {
		return types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes())
	}
	if name.IsUnknown() {
		return types.ObjectUnknown(blockstorageselector.VolumeTypeAttributeTypes())
	}
	return selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"name": name})
}

func selectorTestCriteria(typesMap map[string]attr.Type, values map[string]attr.Value) types.Object {
	complete := make(map[string]attr.Value, len(typesMap))
	for name, typ := range typesMap {
		if value, ok := values[name]; ok {
			complete[name] = value
		} else if typ.Equal(types.BoolType) {
			complete[name] = types.BoolNull()
		} else {
			complete[name] = types.StringNull()
		}
	}
	return types.ObjectValueMust(typesMap, complete)
}
