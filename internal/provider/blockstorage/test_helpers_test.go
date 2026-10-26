package blockstorage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	volumeTestID        = core.UUID{1}
	volumeTestProjectID = core.UUID{9}
)

type volumeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f volumeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func volumeTestClient(t *testing.T, roundTrip volumeRoundTripFunc) *blockstoragesdk.Client {
	t.Helper()
	client, err := blockstoragesdk.NewClient(
		"https://blockstorage.test",
		blockstoragesdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		blockstoragesdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	return client
}

func volumeTestProjectClient(t *testing.T, roundTrip volumeRoundTripFunc) *projectsdk.Client {
	t.Helper()
	client, err := projectsdk.NewClient(
		"https://project.test",
		projectsdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		projectsdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return client
}

func sampleVolumeSnapshot() *blockstoragesdk.VolumeSnapshotSchema {
	status := blockstoragesdk.SnapshotStatusAvailable
	size := 30
	return &blockstoragesdk.VolumeSnapshotSchema{
		Id: core.UUID{3}, Name: "snapshot", Size: &size, Status: &status,
		Volume:    blockstoragesdk.NestedVolumeSchema{Id: volumeTestID, Name: "data", Size: size},
		Project:   blockstoragesdk.NestedProjectSchema{Id: volumeTestProjectID, Name: "project"},
		Zone:      blockstoragesdk.NestedZoneSchema{Id: core.UUID{8}, Name: "zone-a"},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func volumeSnapshotJSON(snapshot *blockstoragesdk.VolumeSnapshotSchema) string {
	body, err := json.Marshal(snapshot)
	if err != nil {
		panic(fmt.Sprintf("marshal volume snapshot: %v", err))
	}
	return string(body)
}

func testHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleVolumeDetail() *blockstoragesdk.VolumeDetailSchema {
	return &blockstoragesdk.VolumeDetailSchema{
		Id: volumeTestID, Name: "data", Size: 100, Status: blockstoragesdk.VolumeStatusAvailable,
		ProjectId: volumeTestProjectID, Zone: blockstoragesdk.NestedZoneSchema{Id: core.UUID{8}, Name: "zone-a"},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{
			VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: core.UUID{2}, Name: "Premium"},
		},
	}
}

func volumeResponseJSON(status blockstoragesdk.VolumeStatus) string {
	volume := sampleVolumeDetail()
	volume.Status = status
	return volumeDetailJSON(volume)
}

func volumeDetailJSON(volume *blockstoragesdk.VolumeDetailSchema) string {
	body, err := json.Marshal(volume)
	if err != nil {
		panic(fmt.Sprintf("marshal volume response: %v", err))
	}
	return string(body)
}

func volumeTypesResponseJSON() string {
	label := func(value string) *string { return &value }
	page := blockstoragesdk.PagedVolumeTypeSchema{
		Count: 1,
		Results: []blockstoragesdk.VolumeTypeSchema{{
			Id:                core.UUID{2},
			Name:              "Premium",
			MaxVolumeSize:     1000,
			VolumeBackendName: "ceph",
			CreatedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			DiskType:          blockstoragesdk.LabeledVolumeTypeDiskType{Value: blockstoragesdk.VolumeTypeDiskTypeSsd, Label: label("SSD")},
			EncryptionKeySize: blockstoragesdk.LabeledVolumeTypeEncryptKeySize{Value: blockstoragesdk.VolumeTypeEncryptKeySizeN256},
			Speed:             blockstoragesdk.LabeledVolumeTypeSpeed{Value: blockstoragesdk.VolumeTypeSpeedN10k},
			State:             blockstoragesdk.LabeledVolumeTypeState{Value: blockstoragesdk.VolumeTypeStateUp},
			Status:            blockstoragesdk.LabeledVolumeTypeStatus{Value: blockstoragesdk.VolumeTypeStatusEnabled},
			Zone:              blockstoragesdk.NestedZoneSchema{Id: core.UUID{8}, Name: "zone-a"},
		}},
	}
	body, err := json.Marshal(page)
	if err != nil {
		panic(fmt.Sprintf("marshal volume type response: %v", err))
	}
	return string(body)
}

func volumeResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("build volume resource schema: %v", response.Diagnostics)
	}
	return response.Schema
}

func decodeVolumeCreateFrom(t *testing.T, source types.Object) volumeSource {
	t.Helper()
	model, diags := volumeCreateFromModel(context.Background(), source)
	if diags.HasError() {
		t.Fatalf("decode create_from: %v", diags)
	}
	return model
}

// volumeCreateFromValue builds create_from for sourceType. An override given as
// a string is the criterion of that reference, such as the image name; an
// override given as an object replaces the whole reference.
func volumeCreateFromValue(t *testing.T, sourceType string, overrides map[string]attr.Value) types.Object {
	t.Helper()
	values := map[string]types.String{"source_type": types.StringValue(sourceType)}
	image := types.ObjectNull(serverselector.ImageAttributeTypes())
	volumeType := types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes())
	objects := map[string]attr.Value{}
	for attribute, value := range overrides {
		switch value := value.(type) {
		case types.String:
			switch attribute {
			case "image":
				image = testImageSelector(value)
			case "volume_type":
				volumeType = testVolumeTypeSelector(value)
			default:
				values[attribute] = value
			}
		default:
			objects[attribute] = value
		}
	}
	result, diags := volumeCreateFromObject(values, image, volumeType)
	if diags.HasError() {
		t.Fatalf("create create_from value: %v", diags)
	}
	if len(objects) == 0 {
		return result
	}
	attributes := maps.Clone(result.Attributes())
	maps.Copy(attributes, objects)
	result, diags = types.ObjectValue(volumeCreateFromAttributeTypes(), attributes)
	if diags.HasError() {
		t.Fatalf("create create_from value: %v", diags)
	}
	return result
}

func backupResponseJSON(backup *blockstoragesdk.VolumeBackupSchema) string {
	body, err := json.Marshal(backup)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func testVolumeBackup() *blockstoragesdk.VolumeBackupSchema {
	id := core.UUID{1}
	status := blockstoragesdk.VolumeBackupStatusAvailable
	description := "saved"
	size := 30
	diskType := blockstoragesdk.VolumeTypeDiskTypeSsd
	return &blockstoragesdk.VolumeBackupSchema{
		Id: id, Name: "backup", Description: &description, Status: &status, Size: &size,
		DiskType: &diskType, Expired: false, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		Project:   blockstoragesdk.NestedProjectSchema{Id: core.UUID{9}},
		Region:    blockstoragesdk.NestedRegionSchema{Id: core.UUID{3}, Name: "region"},
		Volume:    &blockstoragesdk.NestedVolumeSchema{Id: core.UUID{2}, Name: "volume"},
		Policy:    &blockstoragesdk.NestedVolumeBackupPolicySchema{Id: core.UUID{4}, Name: "policy"},
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

func volumeSelectorImageClient(t *testing.T) *serversdk.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/server/images/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		image := serversdk.ImageSchema{Id: core.UUID{4}, Name: req.URL.Query().Get("name")}
		if err := json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []serversdk.ImageSchema{image}}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := serversdk.NewClient(server.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeJSONForVolume(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func volumeIDReference(id string) types.Object {
	return reference.Value(reference.ID, types.StringValue(id))
}

// referenceAttribute returns attribute as a reference object after checking
// that it holds exactly one required string criterion.
func referenceAttribute(t *testing.T, attribute schema.Attribute, criterion string) schema.SingleNestedAttribute {
	t.Helper()
	object, ok := attribute.(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("reference must be a single nested object, got %T", attribute)
	}
	inner, ok := object.Attributes[criterion].(schema.StringAttribute)
	if len(object.Attributes) != 1 || !ok || inner.Description == "" {
		t.Fatalf("reference must hold one %s criterion: %#v", criterion, object.Attributes)
	}
	// An Optional and Computed reference keeps its prior value when omitted only
	// if the criterion is Computed too; a validator then keeps it required.
	if preserved := object.Optional && object.Computed; preserved {
		if !inner.Optional || !inner.Computed || len(inner.Validators) != 1 {
			t.Fatalf("preserved reference must hold an optional, computed, validated %s criterion: %#v", criterion, inner)
		}
	} else if !inner.Required {
		t.Fatalf("reference must hold one required %s criterion: %#v", criterion, inner)
	}
	if object.Description == "" {
		t.Fatal("reference must describe the referenced object")
	}
	return object
}

// planReference runs an object plan modifier for an update of an existing
// resource, the only case in which replacement can be planned.
func planReference(t *testing.T, modifier planmodifier.Object, state, plan, config types.Object) planmodifier.ObjectResponse {
	t.Helper()
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	response := planmodifier.ObjectResponse{PlanValue: plan}
	modifier.PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		State: tfsdk.State{Raw: existing}, Plan: tfsdk.Plan{Raw: existing}, Config: tfsdk.Config{Raw: existing},
		StateValue: state, PlanValue: plan, ConfigValue: config,
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier: %v", response.Diagnostics)
	}
	return response
}

// assertVolumeIDReference checks a required volume = { id } reference whose
// changed ID replaces the resource.
func assertVolumeIDReference(t *testing.T, attribute schema.Attribute) {
	t.Helper()
	volume := referenceAttribute(t, attribute, reference.ID)
	if !volume.Required || volume.Optional || volume.Computed || len(volume.PlanModifiers) != 1 {
		t.Fatalf("volume must be required and replacement-only: %#v", volume)
	}
	current, changed := volumeIDReference(volumeTestID.String()), volumeIDReference((core.UUID{4}).String())
	if !planReference(t, volume.PlanModifiers[0], current, changed, changed).RequiresReplace {
		t.Fatal("changing volume.id must replace the resource")
	}
	if planReference(t, volume.PlanModifiers[0], current, current, current).RequiresReplace {
		t.Fatal("an unchanged volume.id must not replace the resource")
	}
	assertUnknownReferenceReplaces(t, volume.PlanModifiers[0], current, reference.ID, "volume")
}

// assertUnknownReferenceReplaces checks that a reference whose planned value
// is unknown, as a whole object or as its criterion, replaces the resource.
// An unknown value cannot prove the referenced object stays the same.
func assertUnknownReferenceReplaces(t *testing.T, modifier planmodifier.Object, current types.Object, criterion, name string) {
	t.Helper()
	for _, unknown := range []types.Object{
		types.ObjectUnknown(reference.AttributeTypes(criterion)),
		reference.Of(map[string]types.String{criterion: types.StringUnknown()}),
	} {
		if !planReference(t, modifier, current, unknown, unknown).RequiresReplace {
			t.Fatalf("an unknown %s %v must replace the resource", name, unknown)
		}
	}
}

func volumeZone(name string) types.Object {
	return reference.Value(reference.Name, types.StringValue(name))
}

func nullVolumeZone() types.Object {
	return types.ObjectNull(reference.AttributeTypes(reference.Name))
}

func nullVolumeReference() types.Object {
	return types.ObjectNull(reference.AttributeTypes(reference.ID))
}
