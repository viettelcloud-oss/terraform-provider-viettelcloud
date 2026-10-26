package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	blockstorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/selector"
	serverselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	resourceplanmodifier "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	blockstoragelookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage/lookup"
	serverlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	testServerID         = core.UUID{1}
	testZoneID           = core.UUID{2}
	testFlavorID         = core.UUID{3}
	testImageID          = core.UUID{4}
	testVolumeTypeID     = core.UUID{5}
	testSubnetID         = core.UUID{6}
	testPrivateIPID      = core.UUID{7}
	testElasticIPID      = core.UUID{8}
	testKeyPairID        = core.UUID{11}
	testPlacementGroupID = core.UUID{12}
)

func TestServerResourceModelMatchesSchema(t *testing.T) {
	t.Parallel()
	plan := tfsdk.Plan{Schema: serverSchema(t)}
	model := emptyServerResourceModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestServerReferenceAttributesAreNestedObjects(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	boot := resourceSchema.Attributes["boot"].(resourceschema.SingleNestedAttribute)
	dataVolumes := resourceSchema.Attributes["data_volumes"].(resourceschema.ListNestedAttribute)
	privateIPs := resourceSchema.Attributes["private_ips"].(resourceschema.ListNestedAttribute)
	securityGroups := resourceSchema.Attributes["security_groups"].(resourceschema.SetNestedAttribute)
	const (
		required = iota
		optional
		preserved
	)
	for _, tc := range []struct {
		name                         string
		attribute                    resourceschema.Attribute
		criteria                     map[string]int
		required, optional, computed bool
		modifiers                    int
	}{
		{"zone", resourceSchema.Attributes["zone"], map[string]int{reference.Name: preserved}, false, true, true, 1},
		{"key_pair", resourceSchema.Attributes["key_pair"], map[string]int{reference.ID: preserved}, false, true, true, 2},
		{"placement_group", resourceSchema.Attributes["placement_group"], map[string]int{reference.ID: preserved}, false, true, true, 2},
		{"boot.custom_image", boot.Attributes["custom_image"], map[string]int{reference.ID: required}, false, true, false, 0},
		{"boot.volume", boot.Attributes["volume"], map[string]int{reference.ID: required}, false, true, false, 1},
		{
			"private_ips.subnet", privateIPs.NestedObject.Attributes["subnet"],
			map[string]int{reference.ID: optional, subnetCIDR: optional},
			false, true, true, 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			attribute, ok := tc.attribute.(resourceschema.SingleNestedAttribute)
			if !ok {
				t.Fatalf("%s is %T, want a single nested attribute", tc.name, tc.attribute)
			}
			if attribute.IsRequired() != tc.required || attribute.IsOptional() != tc.optional || attribute.IsComputed() != tc.computed {
				t.Fatalf("%s flags = required %v optional %v computed %v", tc.name, attribute.IsRequired(), attribute.IsOptional(), attribute.IsComputed())
			}
			if len(attribute.PlanModifiers) != tc.modifiers {
				t.Fatalf("%s has %d plan modifiers, want %d", tc.name, len(attribute.PlanModifiers), tc.modifiers)
			}
			if len(attribute.Attributes) != len(tc.criteria) {
				t.Fatalf("%s criteria = %v, want %v", tc.name, slices.Collect(maps.Keys(attribute.Attributes)), tc.criteria)
			}
			for criterion, want := range tc.criteria {
				inner, ok := attribute.Attributes[criterion].(resourceschema.StringAttribute)
				// private_ips.subnet criteria are Computed because private_ips is
				// Optional and Computed; ValidateConfig enforces exactly one.
				computed := want == preserved || tc.name == "private_ips.subnet"
				if !ok || inner.IsRequired() != (want == required) || inner.IsComputed() != computed ||
					(want == preserved) != (len(inner.Validators) == 1) {
					t.Fatalf("%s.%s = %#v, want criterion kind %d", tc.name, criterion, attribute.Attributes[criterion], want)
				}
			}
		})
	}
	// The image and volume type selectors combine criteria and record the
	// selected catalog ID. ModifyPlan decides their replacement from that ID.
	for name, value := range map[string]resourceschema.Attribute{
		"boot.image": boot.Attributes["image"], "boot.volume_type": boot.Attributes["volume_type"],
		"data_volumes.volume_type": dataVolumes.NestedObject.Attributes["volume_type"],
	} {
		attribute, ok := value.(resourceschema.SingleNestedAttribute)
		if !ok || len(attribute.PlanModifiers) != 0 {
			t.Fatalf("%s = %#v, want a selector without plan modifiers", name, value)
		}
		if id, ok := attribute.Attributes["id"].(resourceschema.StringAttribute); !ok || !id.IsComputed() || id.IsOptional() {
			t.Fatalf("%s.id = %#v, want a computed catalog ID", name, attribute.Attributes["id"])
		}
	}
	if !securityGroups.IsOptional() || !securityGroups.IsComputed() || len(securityGroups.PlanModifiers) != 0 {
		t.Fatalf("security_groups flags = %#v, want optional and computed without plan modifiers", securityGroups)
	}
	id, ok := securityGroups.NestedObject.Attributes[reference.ID].(resourceschema.StringAttribute)
	if !ok || !id.IsOptional() || !id.IsComputed() || len(id.Validators) != 1 || len(securityGroups.NestedObject.Attributes) != 1 {
		t.Fatalf("security_groups element = %#v, want one optional, computed, validated id", securityGroups.NestedObject.Attributes)
	}
}

func TestServerReferencePlanModifiersReplaceOnlyWhenTheReferenceChanges(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("server")
	state := stateFor(t, resourceSchema, model)
	plan := planFor(t, resourceSchema, model)
	// The volume_type selectors replace the server from catalog identity in
	// ModifyPlan; TestServerSelectorModifyPlanUsesIdentity covers them.
	boot := resourceSchema.Attributes["boot"].(resourceschema.SingleNestedAttribute)
	attribute := func(value resourceschema.Attribute) resourceschema.SingleNestedAttribute {
		return value.(resourceschema.SingleNestedAttribute)
	}
	zoneA, zoneB := nameReference(types.StringValue("zone-a")), nameReference(types.StringValue("zone-b"))
	volumeA, volumeB := idReference(types.StringValue(core.UUID{9}.String())), idReference(types.StringValue(core.UUID{10}.String()))
	keyPair := idReference(types.StringValue(testKeyPairID.String()))
	unknownName := types.ObjectUnknown(reference.AttributeTypes(reference.Name))
	unknownID := types.ObjectUnknown(reference.AttributeTypes(reference.ID))
	nullName := types.ObjectNull(reference.AttributeTypes(reference.Name))
	nullID := types.ObjectNull(reference.AttributeTypes(reference.ID))
	for _, tc := range []struct {
		name                   string
		attribute              resourceschema.SingleNestedAttribute
		config, prior, planned types.Object
		wantPlan               types.Object
		wantReplace            bool
	}{
		{"zone changed", attribute(resourceSchema.Attributes["zone"]), zoneB, zoneA, zoneB, zoneB, true},
		{"zone omitted", attribute(resourceSchema.Attributes["zone"]), nullName, zoneA, unknownName, unknownName, false},
		{"key_pair omitted keeps state", attribute(resourceSchema.Attributes["key_pair"]), nullID, keyPair, unknownID, keyPair, false},
		{"key_pair changed", attribute(resourceSchema.Attributes["key_pair"]), volumeA, keyPair, volumeA, volumeA, true},
		{"placement_group omitted keeps state", attribute(resourceSchema.Attributes["placement_group"]), nullID, volumeA, unknownID, volumeA, false},
		{"boot.volume changed", attribute(boot.Attributes["volume"]), volumeB, volumeA, volumeB, volumeB, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := resourceplanmodifier.ObjectRequest{
				State: state, Plan: plan, ConfigValue: tc.config, StateValue: tc.prior, PlanValue: tc.planned,
			}
			resp := resourceplanmodifier.ObjectResponse{PlanValue: tc.planned}
			for _, modifier := range tc.attribute.PlanModifiers {
				modifier.PlanModifyObject(context.Background(), req, &resp)
				req.PlanValue = resp.PlanValue
			}
			if resp.Diagnostics.HasError() || !resp.PlanValue.Equal(tc.wantPlan) || resp.RequiresReplace != tc.wantReplace {
				t.Fatalf("plan = %s replace = %v diagnostics = %v, want %s replace = %v",
					resp.PlanValue, resp.RequiresReplace, resp.Diagnostics, tc.wantPlan, tc.wantReplace)
			}
		})
	}
}

func TestServerVolumeSizeUpdatesInPlaceAndBootVolumeTypeRemovalReplaces(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	stateModel := emptyServerResourceModel()
	stateModel.ID = types.StringValue(testServerID.String())
	stateModel.Name = types.StringValue("server")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
		"volume_size": types.Int64Value(30), "iops": types.Int64Null(),
	})
	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringNull()),
		"volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	state := tfsdk.State{Schema: resourceSchema}
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set modifier state: %v", diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set modifier plan: %v", diags)
	}
	boot := resourceSchema.Attributes["boot"].(resourceschema.SingleNestedAttribute)

	image := selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String()))
	stateModel.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": image, "volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))})
	planModel.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": image})
	state = stateFor(t, resourceSchema, stateModel)
	plan = planFor(t, resourceSchema, planModel)
	response := resource.ModifyPlanResponse{Plan: plan}
	(&ServerResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan, Config: tfsdk.Config{Schema: resourceSchema, Raw: plan.Raw}}, &response)
	if response.Diagnostics.HasError() || !response.RequiresReplace.Contains(path.Root("boot").AtName("volume_type")) {
		t.Fatalf("removing boot.volume_type must replace: %v", response)
	}

	bootSize := boot.Attributes["volume_size"].(resourceschema.Int64Attribute)
	dataVolumes := resourceSchema.Attributes["data_volumes"].(resourceschema.ListNestedAttribute)
	dataSize := dataVolumes.NestedObject.Attributes["volume_size"].(resourceschema.Int64Attribute)
	if len(bootSize.PlanModifiers) != 0 || len(dataSize.PlanModifiers) != 0 {
		t.Fatal("expected boot and data volume size changes to update in place")
	}
}

func TestServerIOPSPlanPreservesAllocatedValues(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	boot := resourceSchema.Attributes["boot"].(resourceschema.SingleNestedAttribute)
	ssd := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
	for _, attribute := range []resourceschema.Int64Attribute{
		boot.Attributes["iops"].(resourceschema.Int64Attribute),
	} {
		for _, tc := range []struct {
			name               string
			size               types.Int64
			volumeType         types.Object
			config, plan, want types.Int64
			replace            bool
		}{
			{"omitted", types.Int64Value(30), ssd, types.Int64Null(), types.Int64Unknown(), types.Int64Value(900), false},
			{"unchanged", types.Int64Value(30), ssd, types.Int64Value(900), types.Int64Value(900), types.Int64Value(900), false},
			{"changed", types.Int64Value(30), ssd, types.Int64Value(1200), types.Int64Value(1200), types.Int64Value(1200), true},
			{"unknown configuration", types.Int64Value(30), ssd, types.Int64Unknown(), types.Int64Unknown(), types.Int64Unknown(), true},
			// Any update can leave the computed selector ID unknown before modifiers run.
			{"omitted with unknown selector ID", types.Int64Value(30), selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringUnknown()), types.Int64Null(), types.Int64Unknown(), types.Int64Value(900), false},
			// The backend recalculates unconfigured IOPS after a resize or a retype.
			{"omitted with resize", types.Int64Value(40), ssd, types.Int64Null(), types.Int64Unknown(), types.Int64Unknown(), false},
			{"omitted with unknown size", types.Int64Unknown(), ssd, types.Int64Null(), types.Int64Unknown(), types.Int64Unknown(), false},
			{"omitted with retype", types.Int64Value(30), selector.WithID(testVolumeTypeSelector(types.StringValue("ssd2")), types.StringUnknown()), types.Int64Null(), types.Int64Unknown(), types.Int64Unknown(), false},
			{"configured with resize", types.Int64Value(40), ssd, types.Int64Value(900), types.Int64Value(900), types.Int64Value(900), false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				model := validServerConfig(t)
				model.ID = types.StringValue(testServerID.String())
				state := tfsdk.State{Schema: resourceSchema}
				plan := tfsdk.Plan{Schema: resourceSchema}
				if diags := state.Set(context.Background(), &model); diags.HasError() {
					t.Fatal(diags)
				}
				model.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("image"), "image": selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String())),
					"volume_type": tc.volumeType, "volume_size": tc.size, "iops": tc.plan,
				})
				if diags := plan.Set(context.Background(), &model); diags.HasError() {
					t.Fatal(diags)
				}
				req := resourceplanmodifier.Int64Request{
					Path: path.Root("boot").AtName("iops"), State: state, Plan: plan, StateValue: types.Int64Value(900),
					ConfigValue: tc.config, PlanValue: tc.plan,
				}
				resp := resourceplanmodifier.Int64Response{PlanValue: tc.plan}
				for _, modifier := range attribute.PlanModifiers {
					modifier.PlanModifyInt64(context.Background(), req, &resp)
					req.PlanValue = resp.PlanValue
				}
				if resp.Diagnostics.HasError() || !resp.PlanValue.Equal(tc.want) || resp.RequiresReplace != tc.replace {
					t.Fatalf("unexpected IOPS plan: value=%v replace=%v diagnostics=%v", resp.PlanValue, resp.RequiresReplace, resp.Diagnostics)
				}
			})
		}
	}
}

func TestServerModifyPlanPreservesOnlyComputedNoOpUpdates(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"computed only", "name change", "configured bandwidth", "unknown input", "replacement", "create", "destroy"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			s := serverSchema(t)
			config := validServerConfig(t)
			prior := config
			prior.ID = types.StringValue(testServerID.String())
			prior.Description = types.StringValue("")
			prior.Bandwidth = types.Int64Value(300)
			prior.PowerState = types.StringValue("running")
			prior.Status = types.StringValue("active")
			prior.CreatedAt = types.StringValue("2026-09-07T01:00:00Z")
			planned := prior
			planned.Description = types.StringUnknown()
			planned.Bandwidth = types.Int64Unknown()
			planned.PowerState = types.StringUnknown()
			planned.Status = types.StringUnknown()
			planned.CreatedAt = types.StringUnknown()
			switch scenario {
			case "name change":
				config.Name = types.StringValue("renamed")
				planned.Name = config.Name
			case "configured bandwidth":
				config.Bandwidth = types.Int64Value(400)
				planned.Bandwidth = config.Bandwidth
			case "unknown input":
				config.Name = types.StringUnknown()
				planned.Name = config.Name
			}
			req := resource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: s, Raw: planFor(t, s, config).Raw},
				State:  stateFor(t, s, prior),
				Plan:   planFor(t, s, planned),
			}
			if scenario == "create" {
				req.State.Raw = tftypes.NewValue(req.State.Raw.Type(), nil)
			}
			if scenario == "destroy" {
				req.Plan.Raw = tftypes.NewValue(req.Plan.Raw.Type(), nil)
			}
			resp := resource.ModifyPlanResponse{Plan: req.Plan}
			if scenario == "replacement" {
				resp.RequiresReplace = path.Paths{path.Root("boot")}
			}
			(&ServerResource{}).ModifyPlan(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			want := req.Plan.Raw
			if scenario == "computed only" {
				want = req.State.Raw
			}
			if !resp.Plan.Raw.Equal(want) {
				t.Fatal("computed-only changes were retained or a real change was suppressed")
			}
			if scenario == "replacement" && len(resp.RequiresReplace) != 1 {
				t.Fatal("replacement requirement was removed")
			}
		})
	}
}

func TestServerResourceConstructorMetadataAndImport(t *testing.T) {
	t.Parallel()
	r := NewServerResource()
	if r == nil {
		t.Fatal("expected non-nil resource")
	}
	var metadata resource.MetadataResponse
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_server" {
		t.Fatalf("expected viettelcloud_server, got %q", metadata.TypeName)
	}

	resourceSchema := serverSchema(t)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: resourceSchema}}
	model := emptyServerResourceModel()
	if diags := response.State.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	r.(*ServerResource).ImportState(context.Background(), resource.ImportStateRequest{ID: testServerID.String()}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var id types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if response.Diagnostics.HasError() || id.ValueString() != testServerID.String() {
		t.Fatalf("expected imported ID %s, got %s (diagnostics: %v)", testServerID, id.ValueString(), response.Diagnostics)
	}
}

func TestServerReadReconstructsImportedBootAndDataVolumeState(t *testing.T) {
	t.Parallel()
	rootVolumeID := core.UUID{9}
	dataVolumeID := core.UUID{10}
	bootIOPS := 1200
	dataIOPS := 900
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "imported", Description: "",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "small", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				Volumes: []serversdk.NestedServerVolumeSchema{
					{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: rootVolumeID, Size: 30, Status: serversdk.VolumeStatusInUse}},
					{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: dataVolumeID, Size: 50, Status: serversdk.VolumeStatusInUse}},
				},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/flavors/":
			writeJSON(t, w, &serversdk.PagedFlavorSchema{
				Count: 1,
				Results: []serversdk.FlavorSchema{{
					Id: testFlavorID, Name: "small", Vcpus: 2, Ram: 4,
				}},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+rootVolumeID.String()+"/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{
				Id: rootVolumeID, Bootable: true, Size: 30, Iops: &bootIOPS, Status: blockstoragesdk.VolumeStatusInUse,
				CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{
					Image:      &blockstoragesdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
					VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"},
				},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+dataVolumeID.String()+"/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{
				Id: dataVolumeID, Size: 50, Iops: &dataIOPS, Status: blockstoragesdk.VolumeStatusInUse,
				CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{
					VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	blockStorageClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	r := &ServerResource{client: serverClient, blockStorageClient: blockStorageClient, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize imported state: %v", diags)
	}
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read imported server: %v", response.Diagnostics)
	}
	var got ServerResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &got)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("decode imported state: %v", response.Diagnostics)
	}
	var boot BootInputModel
	if diags := got.Boot.As(context.Background(), &boot, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("decode imported boot: %v", diags)
	}
	// Import records every reference in its object form.
	if !got.Zone.Equal(nameReference(types.StringValue("zone-a"))) ||
		!got.SecurityGroups.Equal(reference.Set(reference.ID, []string{})) {
		t.Fatalf("unexpected imported references: zone=%s security_groups=%s", got.Zone, got.SecurityGroups)
	}
	if boot.BootType.ValueString() != "image" || boot.Image.Attributes()["name"].(types.String).ValueString() != "ubuntu" ||
		boot.VolumeType.Attributes()["name"].(types.String).ValueString() != "ssd" || boot.VolumeSize.ValueInt64() != 30 || boot.IOPS.ValueInt64() != int64(bootIOPS) {
		t.Fatalf("unexpected imported boot state: %#v", boot)
	}
	// delete_on_termination is computed, so an import records the default the
	// destroy would apply instead of leaving it null.
	if !boot.DeleteOnTermination.ValueBool() {
		t.Fatalf("expected an imported image boot volume to be deleted on termination: %#v", boot)
	}
	var dataVolumes []DataVolumeInputModel
	if diags := got.DataVolumes.ElementsAs(context.Background(), &dataVolumes, false); diags.HasError() {
		t.Fatalf("decode imported data volumes: %v", diags)
	}
	// iops is computed, so the import records what the backend allocated. A
	// configuration that omits iops keeps that value instead of planning a
	// replacement, and one that configures it is compared against it.
	if len(dataVolumes) != 1 || dataVolumes[0].VolumeType.Attributes()["name"].(types.String).ValueString() != "ssd" ||
		dataVolumes[0].VolumeSize.ValueInt64() != 50 || dataVolumes[0].IOPS.ValueInt64() != int64(dataIOPS) ||
		dataVolumes[0].DeleteOnTermination.ValueBool() || dataVolumeKind(dataVolumes[0]) != "existing" {
		t.Fatalf("unexpected imported data volume state: %#v", dataVolumes)
	}
}

func TestServerDataVolumeStateResolvesAllocatedIOPS(t *testing.T) {
	t.Parallel()
	dataVolumeID := core.UUID{10}
	allocated := 900
	var reads atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+dataVolumeID.String()+"/":
			reads.Add(1)
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{
				Id: dataVolumeID, Size: 50, Iops: &allocated, Status: blockstoragesdk.VolumeStatusInUse,
				CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{
					VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	blockStorageClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	r := &ServerResource{blockStorageClient: blockStorageClient, projectID: core.UUID{12}}
	server := &serversdk.ServerDetailSchema{
		Id: testServerID,
		Volumes: []serversdk.NestedServerVolumeSchema{
			{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: core.UUID{9}}},
			{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: dataVolumeID}},
		},
	}
	state := emptyServerResourceModel()
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50), "iops": types.Int64Unknown(),
	}})

	if diags := r.refreshServerDataVolumeState(context.Background(), server, &state); diags.HasError() {
		t.Fatalf("refresh data volume state: %v", diags)
	}

	var volumes []DataVolumeInputModel
	if diags := state.DataVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() {
		t.Fatalf("decode data volumes: %v", diags)
	}
	// iops is computed, so a plan that omits it leaves it unknown. Leaving that
	// unknown in state would fail the apply with an inconsistent result, and
	// carrying the prior value forward instead would be wrong after a
	// replacement allocated a new volume.
	if len(volumes) != 1 || volumes[0].IOPS.ValueInt64() != int64(allocated) {
		t.Fatalf("unexpected data volume state: %#v", volumes)
	}
	// delete_on_termination is computed too, so a plan that omits it is left
	// unknown and the refresh must resolve the default.
	if !volumes[0].DeleteOnTermination.ValueBool() {
		t.Fatalf("expected a data volume to be deleted on termination by default: %#v", volumes)
	}
	if reads.Load() != 1 {
		t.Fatalf("expected exactly one volume read, got %d", reads.Load())
	}
}

func TestServerDataVolumeStateRefreshesBackendIOPS(t *testing.T) {
	t.Parallel()
	dataVolumeID := core.UUID{10}
	allocated := 900
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/v2/block-storage/volumes/"+dataVolumeID.String()+"/" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{
			Id: dataVolumeID, Size: 60, Iops: &allocated, Status: blockstoragesdk.VolumeStatusInUse,
		})
	}))
	defer api.Close()
	client, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	r := &ServerResource{blockStorageClient: client, projectID: core.UUID{12}}
	server := &serversdk.ServerDetailSchema{
		Id: testServerID,
		Volumes: []serversdk.NestedServerVolumeSchema{
			{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: dataVolumeID}},
		},
	}
	state := emptyServerResourceModel()
	state.DataVolumeIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue(dataVolumeID.String())})
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50), "iops": types.Int64Value(5000),
		"delete_on_termination": types.BoolValue(false),
	}})

	if diags := r.refreshServerDataVolumeState(context.Background(), server, &state); diags.HasError() {
		t.Fatalf("refresh data volume state: %v", diags)
	}

	var volumes []DataVolumeInputModel
	if diags := state.DataVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() {
		t.Fatalf("decode data volumes: %v", diags)
	}
	if len(volumes) != 1 || volumes[0].IOPS.ValueInt64() != int64(allocated) ||
		volumes[0].VolumeSize.ValueInt64() != 60 || volumes[0].DeleteOnTermination.ValueBool() {
		t.Fatalf("unexpected data volume state: %#v", volumes)
	}
}

// An unparsable data_volumes id must fail the refresh instead of being
// silently dropped. The final types.ListValueFrom call below reassigns diags,
// so a bare "continue" on the parse error would let it survive to that point
// and then discard it, reporting success with the volume's stale prior state.
func TestServerDataVolumeStateFailsOnUnparsableDataVolumeID(t *testing.T) {
	t.Parallel()
	r := &ServerResource{projectID: core.UUID{12}}
	server := &serversdk.ServerDetailSchema{
		Id: testServerID,
		Volumes: []serversdk.NestedServerVolumeSchema{
			{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: core.UUID{10}}},
		},
	}
	state := emptyServerResourceModel()
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"id": types.StringValue("not-a-uuid"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
		"volume_size": types.Int64Value(50), "iops": types.Int64Value(5000),
	}})

	diags := r.refreshServerDataVolumeState(context.Background(), server, &state)
	if !diags.HasError() {
		t.Fatal("expected an unparsable data_volumes id to fail the refresh")
	}
}

func TestBuildBootVolumeResizePlanValidatesBackendSize(t *testing.T) {
	t.Parallel()
	rootID := core.UUID{31}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v2/server/servers/" + testServerID.String() + "/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{Volumes: []serversdk.NestedServerVolumeSchema{
				{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: rootID}},
			}})
		case "/v2/block-storage/volumes/" + rootID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: rootID, Size: 30, Status: blockstoragesdk.VolumeStatusInUse})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	serverClient, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(stubHTTPClient(t, handler)), serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	volumeClient, err := blockstoragesdk.NewClient("https://server.test", blockstoragesdk.WithHTTPClient(stubHTTPClient(t, handler)), blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{client: serverClient, blockStorageClient: volumeClient, projectID: core.UUID{12}}
	state := validServerConfig(t)
	state.ID = types.StringValue(testServerID.String())
	plan := state
	plan.Boot = bootConfig(t, map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(40),
	})

	resize, diags := r.buildBootVolumeResizePlan(context.Background(), testServerID, plan, state)
	if diags.HasError() {
		t.Fatalf("build boot resize plan: %v", diags)
	}
	if resize == nil || resize.resizeID != rootID || resize.resizeSize != 40 {
		t.Fatalf("unexpected boot resize plan: %#v", resize)
	}
}

func TestBuildDataVolumeUpdatePlanValidatesBackendSize(t *testing.T) {
	t.Parallel()
	dataID := core.UUID{32}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v2/block-storage/volumes/" + dataID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: dataID, Size: 50, Status: blockstoragesdk.VolumeStatusInUse})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	volumeClient, err := blockstoragesdk.NewClient("https://server.test", blockstoragesdk.WithHTTPClient(stubHTTPClient(t, handler)), blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{blockStorageClient: volumeClient, projectID: core.UUID{12}}
	state := validServerConfig(t)
	state.ID = types.StringValue(testServerID.String())
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"id": types.StringValue(dataID.String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
		"volume_size": types.Int64Value(50), "iops": types.Int64Null(),
	}})
	plan := state
	plan.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"id": types.StringValue(dataID.String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
		"volume_size": types.Int64Value(70), "iops": types.Int64Null(),
	}})

	got, diags := r.buildDataVolumeUpdatePlan(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("build data volume update plan: %v", diags)
	}
	if got == nil || !slices.Equal(got.resizeIDs, []core.UUID{dataID}) || !slices.Equal(got.resizeSizes, []int{70}) {
		t.Fatalf("unexpected data volume update plan: %#v", got)
	}
}

// No data volume changed size, so the plan must be nil — the same
// nil-means-no-op convention buildPrivateIPUpdatePlan/buildElasticIPUpdatePlan
// use, which lets applyServerUpdate's req.attachments.dataVolume != nil check
// skip resizing untouched data volumes.
func TestBuildDataVolumeUpdatePlanReturnsNilWithoutChanges(t *testing.T) {
	t.Parallel()
	r := &ServerResource{projectID: core.UUID{12}}
	state := validServerConfig(t)
	var volumes []DataVolumeInputModel
	if diags := state.DataVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() {
		t.Fatal(diags)
	}
	for i := range volumes {
		volumes[i].ID = types.StringValue(core.UUID{byte(i + 20)}.String())
	}
	state.DataVolumes, _ = types.ListValueFrom(context.Background(), state.DataVolumes.ElementType(context.Background()), volumes)
	plan := state

	got, diags := r.buildDataVolumeUpdatePlan(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("build data volume update plan: %v", diags)
	}
	if got != nil {
		t.Fatalf("expected a nil plan when no data volume changed size, got %#v", got)
	}
}

func TestServerVolumeResizeRejectsShrinkBeforeMutation(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	if _, ok := increasedVolumeSize(types.Int64Value(29), types.Int64Value(30), "boot.volume_size", &diags); ok || !diags.HasError() {
		t.Fatalf("expected shrink diagnostic, got %v", diags)
	}
}

func TestApplyServerUpdateExtendsVolumesInPlace(t *testing.T) {
	t.Parallel()
	first := core.UUID{41}
	second := core.UUID{42}
	targets := map[core.UUID]int{first: 40, second: 70}
	extended := make(map[core.UUID]int)
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var id core.UUID
		for candidate := range targets {
			if strings.Contains(req.URL.Path, candidate.String()) {
				id = candidate
				break
			}
		}
		if id == (core.UUID{}) {
			t.Errorf("unexpected volume path: %s", req.URL.Path)
			http.NotFound(w, req)
			return
		}
		switch req.Method {
		case http.MethodPost:
			var body blockstoragesdk.VolumeExtendSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode extend body: %v", err)
			}
			extended[id] = body.Size
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: id, Size: extended[id], Status: blockstoragesdk.VolumeStatusInUse})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	client, err := blockstoragesdk.NewClient("https://server.test", blockstoragesdk.WithHTTPClient(stubHTTPClient(t, handler)), blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{blockStorageClient: client, projectID: core.UUID{12}}
	diags := r.applyServerUpdate(context.Background(), testServerID, serverUpdateRequest{
		attachments: attachmentUpdatePlan{
			boot:       &bootVolumeUpdatePlan{resizeID: first, resizeSize: 40},
			dataVolume: &dataVolumeUpdatePlan{resizeIDs: []core.UUID{second}, resizeSizes: []int{70}},
		},
		priorPowerState: types.StringValue(string(serversdk.ServerPowerStateRunning)),
	})
	if diags.HasError() {
		t.Fatalf("resize volumes: %v", diags)
	}
	if extended[first] != targets[first] || extended[second] != targets[second] {
		t.Fatalf("unexpected extend requests: %v", extended)
	}
}

func TestServerBootStateRequiresImportedVolumeTypeMetadata(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	boot := serverBootStateObject(
		&serversdk.ServerDetailSchema{Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}},
		&blockstoragesdk.VolumeDetailSchema{
			Size:       30,
			CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{Image: &blockstoragesdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}},
		},
		core.UUID{9},
		"image",
		BootInputModel{},
		&diags,
	)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Unable to determine server boot volume type" {
		t.Fatalf("expected missing volume-type metadata diagnostic, boot=%v diagnostics=%v", boot, diags)
	}
}

func TestServerBootStatePreservesConfiguredReferences(t *testing.T) {
	t.Parallel()
	rootVolumeID := core.UUID{9}
	rootVolume := &blockstoragesdk.VolumeDetailSchema{
		Size: 30,
		CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{
			Image:      &blockstoragesdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
			VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"},
		},
	}
	server := &serversdk.ServerDetailSchema{Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}}
	for _, tc := range []struct {
		name     string
		bootType string
		current  BootInputModel
		want     map[string]types.Object
	}{
		{
			name: "configured criteria keep their spelling and record the selected ID", bootType: "image",
			current: BootInputModel{Image: testImageSelector(types.StringValue(" ubuntu ")), VolumeType: testVolumeTypeSelector(types.StringValue("ssd "))},
			want: map[string]types.Object{
				"image":       selector.WithID(testImageSelector(types.StringValue(" ubuntu ")), types.StringValue(testImageID.String())),
				"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("ssd ")), types.StringValue(testVolumeTypeID.String())),
			},
		},
		{
			name: "a different selected ID exposes drift", bootType: "image",
			current: BootInputModel{
				Image:      selector.WithID(testImageSelector(types.StringValue("debian")), types.StringValue(core.UUID{0xee}.String())),
				VolumeType: selector.WithID(testVolumeTypeSelector(types.StringValue("hdd")), types.StringValue(core.UUID{0xef}.String())),
			},
			want: map[string]types.Object{
				"image":       selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String())),
				"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String())),
			},
		},
		{
			name: "existing volume keeps its configured ID", bootType: "volume",
			current: BootInputModel{Volume: idReference(types.StringValue(" " + rootVolumeID.String()))},
			want: map[string]types.Object{
				"volume": idReference(types.StringValue(" " + rootVolumeID.String())), "image": types.ObjectNull(serverselector.ImageAttributeTypes()),
			},
		},
		{
			name: "custom image is reported in object form", bootType: "custom_image",
			want: map[string]types.Object{"custom_image": idReference(types.StringValue(testImageID.String()))},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var diags diag.Diagnostics
			boot := serverBootStateObject(server, rootVolume, rootVolumeID, tc.bootType, tc.current, &diags)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			for name, want := range tc.want {
				if got := boot.Attributes()[name]; !got.Equal(want) {
					t.Fatalf("boot.%s = %s, want %s", name, got, want)
				}
			}
		})
	}
}

func TestSelectServerBootTypeExposesSourceDriftAndPreservesExistingVolume(t *testing.T) {
	t.Parallel()
	if got := selectServerBootType(types.StringValue("image"), "custom_image", true); got != "custom_image" {
		t.Fatalf("expected backend boot-source drift to be exposed, got %q", got)
	}
	if got := selectServerBootType(types.StringValue("volume"), "image", true); got != "volume" {
		t.Fatalf("expected an existing image-origin volume to remain volume boot, got %q", got)
	}
	if got := selectServerBootType(types.StringValue("local_disk"), "", false); got != "local_disk" {
		t.Fatalf("expected known local-disk boot to survive an omitted image payload, got %q", got)
	}
}

func TestBuildServerCreateBody(t *testing.T) {
	t.Parallel()
	plan := ServerResourceModel{
		ServerModel: ServerModel{
			Name: types.StringValue("  application  "), Description: types.StringValue("application server"),
			Zone: nameReference(types.StringValue("zone-a")), UserData: types.StringValue("#cloud-config"), Bandwidth: types.Int64Value(100),
			KeyPair:        idReference(types.StringValue(testKeyPairID.String())),
			PlacementGroup: idReference(types.StringValue(testPlacementGroupID.String())),
		},
		Boot: objectValue(t, bootAttributeTypes(), map[string]attr.Value{
			"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
			"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()),
			"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
			"iops": types.Int64Value(1000),
		}),
		Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
			"kind": types.StringValue("predefined"), "name": types.StringValue("s2.small"),
			"family": types.StringNull(), "vcpus": types.Int64Null(), "ram": types.Int64Null(),
		}),
		Quantity: types.Int64Value(1),
		DataVolumes: dataVolumeList(t, []map[string]attr.Value{{
			"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50), "iops": types.Int64Null(),
		}}),
		PrivateIPs: listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
			"kind": types.StringValue("subnet"), "id": types.StringNull(), "subnet": subnetReference(types.StringNull(), types.StringValue("10.0.1.0/24")), "delete_on_termination": types.BoolValue(true),
			"ip_address": types.StringUnknown(), "mac_address": types.StringUnknown(),
		}}),
		ElasticIPs: listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
			"kind": types.StringValue("new"), "id": types.StringNull(), "enable_ipv4": types.BoolValue(true),
			"enable_ipv6": types.BoolValue(false), "delete_on_termination": types.BoolValue(true),
			"ip_address": types.StringUnknown(), "ipv6_address": types.StringUnknown(), "status": types.StringUnknown(),
		}}),
	}

	volumeResolveCalls := 0
	body, diags := buildServerCreateBody(
		context.Background(),
		plan,
		func(_ context.Context, name string) (projectsdk.ProjectZoneSchema, error) {
			if name != "zone-a" {
				t.Fatalf("unexpected zone name %q", name)
			}
			return projectsdk.ProjectZoneSchema{Id: testZoneID, Name: name}, nil
		},
		func(_ context.Context, filter serverlookup.FlavorFilter) (serversdk.FlavorSchema, error) {
			if filter.Name == nil || *filter.Name != "s2.small" || filter.ZoneID == nil || *filter.ZoneID != testZoneID {
				t.Fatalf("unexpected flavor filter: %#v", filter)
			}
			return serversdk.FlavorSchema{Id: testFlavorID}, nil
		},
		func(_ context.Context, filter serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
			if filter.Name == nil || *filter.Name != "ubuntu" {
				t.Fatalf("unexpected image filter: %#v", filter)
			}
			return serversdk.ImageSchema{Id: testImageID}, nil
		},
		func(_ context.Context, req blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
			volumeResolveCalls++
			if *req.Filter.Name != "ssd" || req.ZoneID == nil || *req.ZoneID != testZoneID {
				t.Fatalf("unexpected volume type request: %#v", req)
			}
			return blockstoragesdk.VolumeTypeSchema{Id: testVolumeTypeID}, nil
		},
		func(_ context.Context, cidr string) (networksdk.SubnetSchema, error) {
			if cidr != "10.0.1.0/24" {
				t.Fatalf("unexpected subnet CIDR %q", cidr)
			}
			return networksdk.SubnetSchema{Id: testSubnetID}, nil
		},
	)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if body.Name != "application" || body.Description == nil || *body.Description != "application server" ||
		body.ZoneId == nil || *body.ZoneId != testZoneID || body.Quantity == nil || *body.Quantity != 1 ||
		body.Bandwidth == nil || *body.Bandwidth != 100 || body.UserData == nil || *body.UserData != "#cloud-config" ||
		body.KeyPairId == nil || *body.KeyPairId != testKeyPairID ||
		body.PlacementGroupId == nil || *body.PlacementGroupId != testPlacementGroupID {
		t.Fatalf("unexpected create body: %#v", body)
	}
	if volumeResolveCalls != 2 {
		t.Fatalf("expected two volume type resolutions, got %d", volumeResolveCalls)
	}
	flavor, err := body.Flavor.AsPredefinedServerFlavor()
	if err != nil || flavor.Id != testFlavorID {
		t.Fatalf("unexpected predefined flavor: %#v, %v", flavor, err)
	}
	boot, err := body.Boot.AsServerBootFromImage()
	if err != nil || boot.ImageId != testImageID || boot.VolumeTypeId != testVolumeTypeID || boot.VolumeSize != 30 || boot.Iops == nil || *boot.Iops != 1000 {
		t.Fatalf("unexpected image boot: %#v, %v", boot, err)
	}
	if body.PrivateIps == nil || len(*body.PrivateIps) != 1 || body.ElasticIps == nil || len(*body.ElasticIps) != 1 ||
		body.DataVolumes == nil || len(*body.DataVolumes) != 1 {
		t.Fatalf("expected all attachment types in body: %#v", body)
	}
	privateIP, err := (*body.PrivateIps)[0].AsNewServerPrivateIP()
	if err != nil || privateIP.SubnetId != testSubnetID {
		t.Fatalf("unexpected private IP: %#v, %v", privateIP, err)
	}
	elasticIP, err := (*body.ElasticIps)[0].AsNewServerElasticIP()
	if err != nil || !elasticIP.EnableIpv4 || elasticIP.EnableIpv6 {
		t.Fatalf("unexpected elastic IP: %#v, %v", elasticIP, err)
	}
}

func TestBuildServerCreateBodyReadsReferenceObjects(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"null", "unknown"} {
		t.Run(scenario+" optional references are omitted", func(t *testing.T) {
			t.Parallel()
			plan := localDiskServerModel(t, "application")
			if scenario == "unknown" {
				plan.Zone = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
				plan.KeyPair = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
				plan.PlacementGroup = idReference(types.StringUnknown())
				plan.SecurityGroups = types.SetUnknown(reference.ObjectType(reference.ID))
			} else {
				plan.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
			}
			body, diags := buildServerCreateBodyForTest(t, plan, func(context.Context, string) (projectsdk.ProjectZoneSchema, error) {
				t.Fatal("an omitted zone must not be resolved")
				return projectsdk.ProjectZoneSchema{}, nil
			})
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if body.ZoneId != nil || body.KeyPairId != nil || body.PlacementGroupId != nil || body.SecurityGroups != nil {
				t.Fatalf("expected omitted references to stay out of the request: %#v", body)
			}
		})
	}

	t.Run("known references", func(t *testing.T) {
		t.Parallel()
		securityGroupID := core.UUID{13}
		plan := localDiskServerModel(t, "application")
		plan.Zone = nameReference(types.StringValue(" zone-a "))
		plan.SecurityGroups = reference.Set(reference.ID, []string{securityGroupID.String()})
		plan.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
			"kind": types.StringValue("subnet"), "subnet": subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		}})
		body, diags := buildServerCreateBodyForTest(t, plan, func(_ context.Context, name string) (projectsdk.ProjectZoneSchema, error) {
			if name != "zone-a" {
				t.Fatalf("unexpected zone name %q", name)
			}
			return projectsdk.ProjectZoneSchema{Id: testZoneID}, nil
		})
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if body.ZoneId == nil || *body.ZoneId != testZoneID || body.SecurityGroups == nil ||
			len(*body.SecurityGroups) != 1 || (*body.SecurityGroups)[0].Id != securityGroupID {
			t.Fatalf("unexpected references in request: %#v", body)
		}
		privateIP, err := (*body.PrivateIps)[0].AsNewServerPrivateIP()
		if err != nil || privateIP.SubnetId != testSubnetID {
			t.Fatalf("expected subnet.id to be sent without a CIDR lookup, got %#v, %v", privateIP, err)
		}
	})

	t.Run("invalid references name the criterion", func(t *testing.T) {
		t.Parallel()
		plan := localDiskServerModel(t, "application")
		plan.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
		plan.KeyPair = idReference(types.StringValue("not-a-uuid"))
		plan.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{
			{"kind": types.StringValue("subnet"), "subnet": subnetReference(types.StringValue("not-a-uuid"), types.StringNull())},
			{"kind": types.StringValue("subnet"), "subnet": subnetReference(types.StringValue(testSubnetID.String()), types.StringValue("10.0.1.0/24"))},
			{"kind": types.StringValue("ip"), "id": types.StringValue(testPrivateIPID.String()), "subnet": subnetReference(types.StringNull(), types.StringValue("10.0.1.0/24"))},
		})
		_, diags := buildServerCreateBodyForTest(t, plan, nil)
		var details []string
		for _, diagnostic := range diags.Errors() {
			details = append(details, diagnostic.Detail())
		}
		joined := strings.Join(details, "\n")
		for _, want := range []string{
			"key_pair.id", "private_ips[0].subnet.id",
			"private_ips[1].subnet must configure exactly one of id or cidr",
			"private_ips[2] must not configure subnet when kind is ip",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("expected a diagnostic naming %q, got %v", want, diags)
			}
		}
	})
}

// buildServerCreateBodyForTest builds a create request whose subnet CIDR
// lookup always fails, so a test proves which references skip that lookup.
func buildServerCreateBodyForTest(
	t *testing.T,
	plan ServerResourceModel,
	resolveZone zoneResolveFunc,
) (serversdk.ServerCreateSchema, diag.Diagnostics) {
	t.Helper()
	return buildServerCreateBody(
		context.Background(), plan, resolveZone,
		func(context.Context, serverlookup.FlavorFilter) (serversdk.FlavorSchema, error) {
			return serversdk.FlavorSchema{}, nil
		},
		func(context.Context, serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
			return serversdk.ImageSchema{Id: testImageID}, nil
		},
		func(context.Context, blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
			return blockstoragesdk.VolumeTypeSchema{Id: testVolumeTypeID}, nil
		},
		func(context.Context, string) (networksdk.SubnetSchema, error) {
			return networksdk.SubnetSchema{}, errors.New("subnet lookup is not expected")
		},
	)
}

func TestBuildServerCreateBodyRejectsInvalidStructuralInputs(t *testing.T) {
	t.Parallel()
	plan := ServerResourceModel{
		ServerModel: ServerModel{Name: types.StringUnknown()},
		Quantity:    types.Int64Value(2),
		Boot: objectValue(t, bootAttributeTypes(), map[string]attr.Value{
			"boot_type": types.StringValue("volume"), "image": testImageSelector(types.StringNull()), "custom_image": idReference(types.StringNull()),
			"volume": idReference(types.StringValue("not-a-uuid")), "volume_type": testVolumeTypeSelector(types.StringNull()),
			"volume_size": types.Int64Null(), "iops": types.Int64Null(),
		}),
		Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
			"kind": types.StringValue("invalid"), "name": types.StringNull(), "family": types.StringNull(),
			"vcpus": types.Int64Null(), "ram": types.Int64Null(),
		}),
	}
	_, diags := buildServerCreateBody(
		context.Background(), plan,
		func(context.Context, string) (projectsdk.ProjectZoneSchema, error) {
			return projectsdk.ProjectZoneSchema{}, nil
		},
		func(context.Context, serverlookup.FlavorFilter) (serversdk.FlavorSchema, error) {
			return serversdk.FlavorSchema{}, nil
		},
		func(context.Context, serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
			return serversdk.ImageSchema{}, nil
		},
		func(context.Context, blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
			return blockstoragesdk.VolumeTypeSchema{}, nil
		},
		func(context.Context, string) (networksdk.SubnetSchema, error) { return networksdk.SubnetSchema{}, nil },
	)
	if !diags.HasError() || len(diags.Errors()) < 3 {
		t.Fatalf("expected name, quantity, and flavor diagnostics, got %v", diags)
	}
}

func TestBuildBootResolverErrorIsDiagnostic(t *testing.T) {
	t.Parallel()
	boot := BootInputModel{
		BootType: types.StringValue("local_disk"),
		Image:    testImageSelector(types.StringValue("missing")),
	}
	_, diags := buildBoot(
		context.Background(), boot, nil,
		func(context.Context, serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
			return serversdk.ImageSchema{}, errors.New("not found")
		},
		func(context.Context, blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
			return blockstoragesdk.VolumeTypeSchema{}, nil
		},
	)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Unable to resolve image" {
		t.Fatalf("expected image resolution diagnostic, got %v", diags)
	}
}

func TestBuildElasticIPsRejectsUnknownAddressFamily(t *testing.T) {
	t.Parallel()
	value := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringNull(),
		"enable_ipv4": types.BoolUnknown(), "enable_ipv6": types.BoolValue(false),
		"delete_on_termination": types.BoolValue(false), "ip_address": types.StringUnknown(),
		"ipv6_address": types.StringUnknown(), "status": types.StringUnknown(),
	}})
	var body serversdk.ServerCreateSchema
	var diags diag.Diagnostics
	buildElasticIPs(context.Background(), value, &body, &diags)
	if !diags.HasError() || (body.ElasticIps != nil && len(*body.ElasticIps) != 0) {
		t.Fatalf("expected unknown address family diagnostic without request field, body=%#v diagnostics=%v", body, diags)
	}
}

func TestBuildServerUpdateBody(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{
		ServerModel: ServerModel{
			Name: types.StringValue("before"), Description: types.StringValue("before"), Bandwidth: types.Int64Value(10),
		},
	}
	plan := state
	plan.Name = types.StringValue("  after  ")
	plan.Description = types.StringValue("")
	plan.Bandwidth = types.Int64Value(20)
	body, changed, diags := buildServerUpdateBody(plan, state)
	if diags.HasError() || !changed || body.Name == nil || *body.Name != "after" || body.Description == nil || *body.Description != "" {
		t.Fatalf("unexpected update body: %#v, changed=%v, diagnostics=%v", body, changed, diags)
	}
	if !bandwidthChanged(plan, state) {
		t.Fatal("expected bandwidth change")
	}

	plan = state
	plan.Name = types.StringValue("  before  ")
	plan.Description = types.StringNull()
	body, changed, diags = buildServerUpdateBody(plan, state)
	if diags.HasError() || changed || body.Name != nil || body.Description != nil {
		t.Fatalf("expected omitted description and equivalent name to produce no patch, got %#v", body)
	}
}

func TestPopulateServerResourceStatePreservesAttachmentPolicy(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{
		ServerModel: ServerModel{Name: types.StringValue("  application  "), Zone: nameReference(types.StringValue(" zone-a "))},
		PrivateIPs: listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
			"kind": types.StringValue("subnet"), "id": types.StringUnknown(), "subnet": subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()), "delete_on_termination": types.BoolValue(true),
			"ip_address": types.StringUnknown(), "mac_address": types.StringUnknown(),
		}}),
		ElasticIPs: listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
			"kind": types.StringValue("new"), "id": types.StringUnknown(), "enable_ipv4": types.BoolValue(true),
			"enable_ipv6": types.BoolValue(false), "delete_on_termination": types.BoolValue(true),
			"ip_address": types.StringUnknown(), "ipv6_address": types.StringUnknown(), "status": types.StringUnknown(),
		}}),
	}
	ipv4 := "10.0.1.10"
	publicIP := "203.0.113.10"
	bandwidth := 100
	server := &serversdk.ServerDetailSchema{
		Id: testServerID, Name: "application", Description: "desc",
		Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
		Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
		PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
		ServerType: "vm", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Bandwidth: &bandwidth,
		PrivateIps: []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID, IpAddress: &ipv4}},
		ElasticIps: []serversdk.NestedElasticIPSchema{{Id: testElasticIPID, IpAddress: &publicIP, Status: serversdk.ElasticIPStatusActive}},
		Volumes:    []serversdk.NestedServerVolumeSchema{{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: core.UUID{9}}}},
	}
	diags := populateServerResourceState(context.Background(), server, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.Name.ValueString() != "  application  " || !state.Zone.Equal(nameReference(types.StringValue(" zone-a "))) || state.Bandwidth.ValueInt64() != 100 {
		t.Fatalf("configured representations or bandwidth not preserved: %#v", state)
	}
	var privateIPs []PrivateIPInputModel
	if valueDiags := state.PrivateIPs.ElementsAs(context.Background(), &privateIPs, false); valueDiags.HasError() {
		t.Fatalf("decode private IPs: %v", valueDiags)
	}
	if len(privateIPs) != 1 || privateIPs[0].ID.ValueString() != testPrivateIPID.String() ||
		!privateIPs[0].DeleteOnTermination.ValueBool() || privateIPs[0].IPAddress.ValueString() != ipv4 ||
		!privateIPs[0].Subnet.Equal(subnetReference(types.StringValue(testSubnetID.String()), types.StringNull())) {
		t.Fatalf("unexpected private IP state: %#v", privateIPs)
	}
	var elasticIPs []ElasticIPInputModel
	if valueDiags := state.ElasticIPs.ElementsAs(context.Background(), &elasticIPs, false); valueDiags.HasError() {
		t.Fatalf("decode elastic IPs: %v", valueDiags)
	}
	if len(elasticIPs) != 1 || elasticIPs[0].ID.ValueString() != testElasticIPID.String() || !elasticIPs[0].DeleteOnTermination.ValueBool() {
		t.Fatalf("unexpected elastic IP state: %#v", elasticIPs)
	}
}

func TestServerCascadeTargets(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{
		PrivateIPs: listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{
			privateIPValues(testPrivateIPID, true), privateIPValues(core.UUID{10}, false),
		}),
		ElasticIPs: listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{
			elasticIPValues(testElasticIPID, true), elasticIPValues(core.UUID{11}, false),
		}),
	}
	privateIDs, elasticIDs, diags := serverCascadeTargets(context.Background(), state)
	if diags.HasError() || len(privateIDs) != 1 || privateIDs[0] != testPrivateIPID ||
		len(elasticIDs) != 1 || elasticIDs[0] != testElasticIPID {
		t.Fatalf("unexpected cascade targets: private=%v elastic=%v diagnostics=%v", privateIDs, elasticIDs, diags)
	}
}

func TestPopulateServerResourceStateExposesAttachmentAndFlavorDrift(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{
		Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
			"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringValue("basic"),
			"vcpus": types.Int64Value(2), "ram": types.Int64Value(2),
		}),
		PrivateIPs: listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{
			privateIPValues(testPrivateIPID, false),
		}),
		ElasticIPs: types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}),
	}
	backend := &serversdk.ServerDetailSchema{
		Id: testServerID, Name: "application",
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 4, Ram: 8},
		Zone:   serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
	}
	diags := populateServerResourceState(context.Background(), backend, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(state.PrivateIPs.Elements()) != 0 {
		t.Fatalf("expected missing backend attachment to be removed from state, got %v", state.PrivateIPs)
	}
	var flavor FlavorInputModel
	if valueDiags := state.Flavor.As(context.Background(), &flavor, basetypes.ObjectAsOptions{}); valueDiags.HasError() {
		t.Fatalf("decode flavor: %v", valueDiags)
	}
	if flavor.Family.ValueString() != "basic" || flavor.VCPUs.ValueInt64() != 4 || flavor.RAM.ValueInt64() != 8 {
		t.Fatalf("expected backend flavor capacity with configured family, got %#v", flavor)
	}
}

func TestServerDeleteRemovesSelectedAttachments(t *testing.T) {
	t.Parallel()
	serverDeleted := false
	privateIPDeleted := false
	elasticIPDeleted := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			if serverDeleted {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application", Description: "",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateShutdown, Status: serversdk.ServerStatusShutoff,
				PrivateIps: []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID}},
				ElasticIps: []serversdk.NestedElasticIPSchema{{Id: testElasticIPID}},
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			serverDeleted = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
			privateIPDeleted = true
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
			http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/network/elastic-ips/"+testElasticIPID.String()+"/":
			elasticIPDeleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	networkClient, err := networksdk.NewClient(api.URL, networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &ServerResource{client: serverClient, networkClient: networkClient, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("application")
	model.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{privateIPValues(testPrivateIPID, true)})
	model.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{elasticIPValues(testElasticIPID, true)})
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set delete state: %v", diags)
	}

	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if !serverDeleted || !privateIPDeleted || !elasticIPDeleted {
		t.Fatalf("expected server and selected attachments to be deleted: server=%v private=%v elastic=%v", serverDeleted, privateIPDeleted, elasticIPDeleted)
	}
}

func TestServerVolumeCascadeTargets(t *testing.T) {
	t.Parallel()
	rootVolumeID := core.UUID{9}
	firstDataVolumeID := core.UUID{10}
	secondDataVolumeID := core.UUID{11}
	serverWith := func(mountAs ...string) *serversdk.ServerDetailSchema {
		ids := []core.UUID{rootVolumeID, firstDataVolumeID, secondDataVolumeID}
		attachments := make([]serversdk.NestedServerVolumeSchema, 0, len(mountAs))
		for i, mount := range mountAs {
			attachments = append(attachments, serversdk.NestedServerVolumeSchema{
				MountAs: mount, Volume: serversdk.NestedVolumeSchema{Id: ids[i]},
			})
		}
		return &serversdk.ServerDetailSchema{Id: testServerID, Volumes: attachments}
	}

	tests := []struct {
		name   string
		mounts []string
		state  func(*ServerResourceModel)
		want   []core.UUID
	}{
		{
			name:   "an image boot volume is deleted by default",
			mounts: []string{"root"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("image"),
				})
			},
			want: []core.UUID{rootVolumeID},
		},
		{
			name:   "an image boot volume is kept when the practitioner opts out",
			mounts: []string{"root"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("image"), "delete_on_termination": types.BoolValue(false),
				})
			},
			want: nil,
		},
		{
			name:   "an existing boot volume is kept by default",
			mounts: []string{"root"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("volume"), "volume": idReference(types.StringValue(rootVolumeID.String())),
				})
			},
			want: nil,
		},
		{
			name:   "an existing boot volume is deleted when the practitioner opts in",
			mounts: []string{"root"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("volume"), "volume": idReference(types.StringValue(rootVolumeID.String())),
					"delete_on_termination": types.BoolValue(true),
				})
			},
			want: []core.UUID{rootVolumeID},
		},
		{
			name:   "a local disk boot has no volume to delete",
			mounts: nil,
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("local_disk"), "image": testImageSelector(types.StringValue("ubuntu")),
				})
			},
			want: nil,
		},
		{
			name:   "data volumes are deleted by default and opted out individually",
			mounts: []string{"root", "data", "data"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("image"), "delete_on_termination": types.BoolValue(false),
				})
				state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
					{
						"id":          types.StringValue(firstDataVolumeID.String()),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50),
					},
					{
						"id":          types.StringValue(secondDataVolumeID.String()),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(60),
						"delete_on_termination": types.BoolValue(false),
					},
				})
			},
			want: []core.UUID{firstDataVolumeID},
		},
		{
			name:   "a server-owned volume remains a cleanup target after detach",
			mounts: []string{"root", "data"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("image"),
				})
				state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
					{
						"id":          types.StringValue(firstDataVolumeID.String()),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50),
					},
					{
						"id":          types.StringValue(secondDataVolumeID.String()),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(60),
					},
				})
			},
			want: []core.UUID{rootVolumeID, firstDataVolumeID, secondDataVolumeID},
		},
		{
			name:   "data volumes with null or empty id are safely skipped",
			mounts: []string{"root", "data"},
			state: func(state *ServerResourceModel) {
				state.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
					"boot_type": types.StringValue("image"),
				})
				state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
					{
						"id":          types.StringNull(),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50),
					},
					{
						"id":          types.StringValue(""),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(60),
					},
					{
						"id":          types.StringValue(firstDataVolumeID.String()),
						"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(70),
					},
				})
			},
			want: []core.UUID{rootVolumeID, firstDataVolumeID},
		},
		{
			name:   "a state without boot or data volumes deletes nothing",
			mounts: []string{"root", "data"},
			state:  func(*ServerResourceModel) {},
			want:   nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := emptyServerResourceModel()
			test.state(&state)
			got, diags := serverVolumeCascadeTargets(context.Background(), state, serverWith(test.mounts...))
			if diags.HasError() {
				t.Fatalf("resolve cascade targets: %v", diags)
			}
			if len(got) != len(test.want) {
				t.Fatalf("expected targets %v, got %v", test.want, got)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("expected targets %v, got %v", test.want, got)
				}
			}
		})
	}
}

func TestServerDeleteRemovesTerminatedVolumes(t *testing.T) {
	t.Parallel()
	rootVolumeID := core.UUID{9}
	firstDataVolumeID := core.UUID{10}
	secondDataVolumeID := core.UUID{11}
	serverDeleted := false
	deletedVolumes := make(map[string]bool)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			if serverDeleted {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateShutdown, Status: serversdk.ServerStatusShutoff,
				Volumes: []serversdk.NestedServerVolumeSchema{
					{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: rootVolumeID}},
					{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: firstDataVolumeID}},
					{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: secondDataVolumeID}},
				},
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			serverDeleted = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+rootVolumeID.String()+"/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: rootVolumeID, Status: blockstoragesdk.VolumeStatusAvailable})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+firstDataVolumeID.String()+"/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: firstDataVolumeID, Status: blockstoragesdk.VolumeStatusAvailable})
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/block-storage/volumes/"+rootVolumeID.String()+"/":
			if !serverDeleted {
				t.Errorf("boot volume was deleted before the server")
			}
			deletedVolumes[rootVolumeID.String()] = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/block-storage/volumes/"+firstDataVolumeID.String()+"/":
			deletedVolumes[firstDataVolumeID.String()] = true
			w.WriteHeader(http.StatusAccepted)
		default:
			// Any request for the opted-out data volume lands here.
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	blockStorageClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	r := &ServerResource{client: serverClient, blockStorageClient: blockStorageClient, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("application")
	model.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
	})
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
		{
			"id":          types.StringValue(firstDataVolumeID.String()),
			"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50),
		},
		{
			"id":          types.StringValue(secondDataVolumeID.String()),
			"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(60),
			"delete_on_termination": types.BoolValue(false),
		},
	})
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set delete state: %v", diags)
	}

	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if !serverDeleted {
		t.Fatal("expected the server to be deleted")
	}
	if !deletedVolumes[rootVolumeID.String()] || !deletedVolumes[firstDataVolumeID.String()] {
		t.Fatalf("expected the boot volume and the first data volume to be deleted, got %v", deletedVolumes)
	}
}

// A volume can already be gone when Terraform asks for it, for example because
// the backend removed it with the server or an operator deleted it.
func TestServerDeleteTreatsAMissingVolumeAsDeleted(t *testing.T) {
	t.Parallel()
	volumeID := core.UUID{9}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The detach wait reads the volume before the deletion is requested, so
		// a volume that is already gone must end that wait rather than poll to
		// the deadline.
		if (req.Method == http.MethodDelete || req.Method == http.MethodGet) &&
			req.URL.Path == "/v2/block-storage/volumes/"+volumeID.String()+"/" {
			http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
			return
		}
		t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		http.NotFound(w, req)
	}))
	defer api.Close()

	blockStorageClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}
	r := &ServerResource{blockStorageClient: blockStorageClient, projectID: core.UUID{12}}
	var diags diag.Diagnostics
	r.deleteTerminatedVolumes(context.Background(), []core.UUID{volumeID}, &diags)
	if diags.HasError() {
		t.Fatalf("expected a missing volume to be treated as deleted, got %v", diags)
	}
}

// Deleting a server does not detach its volumes synchronously: a volume can
// still report in-use once the server is gone, and the block storage API
// rejects a deletion in that status with "Volume with status in-use is not
// allowed to delete". Requesting the deletion straight after the server
// disappears therefore loses the volume to that race.
func TestServerDeleteWaitsForVolumesToLeaveInUse(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"detached", "detach abandoned"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				testServerDeleteWaitsForVolumeDetach(t, scenario)
			})
		})
	}
}

func TestServerCascadeDeletionRetries(t *testing.T) {
	t.Parallel()
	for _, entity := range []struct {
		name          string
		path          string
		successStatus int
		interval      time.Duration
		timeout       time.Duration
		delete        func(*ServerResource, context.Context, core.UUID) error
		messages      []string
	}{
		{
			name: "volume", path: "/v2/block-storage/volumes/", successStatus: http.StatusAccepted,
			interval: volumeDetachInterval, timeout: volumeDetachTimeout, delete: (*ServerResource).deleteVolumeWithRetry,
			messages: []string{"dependent objects", "conflict", "Volume with status in-use is not allowed to delete", "in use", "ATTACHED"},
		},
		{
			name: "private IP", path: "/v2/network/private-ips/", successStatus: http.StatusNoContent,
			interval: privateIPDeleteInterval, timeout: privateIPDeleteTimeout, delete: (*ServerResource).deletePrivateIPWithRetry,
			messages: []string{"ATTACHED", "in use", "in-use", "dependent"},
		},
		{
			name: "elastic IP", path: "/v2/network/elastic-ips/", successStatus: http.StatusNoContent,
			interval: privateIPDeleteInterval, timeout: privateIPDeleteTimeout, delete: (*ServerResource).deleteElasticIPWithRetry,
			messages: []string{"ATTACHED", "associated", "in use", "in-use"},
		},
	} {
		t.Run(entity.name, func(t *testing.T) {
			t.Parallel()
			type scenario struct {
				name     string
				status   int
				message  string
				failures int
				attempts int
				waited   time.Duration
				wantErr  error
				cancel   bool
			}
			cases := []scenario{
				{name: "success", attempts: 1},
				{name: "not found", status: http.StatusNotFound, failures: 1, attempts: 1},
				{name: "conflict then success", status: http.StatusConflict, message: "operation busy", failures: 1, attempts: 2, waited: entity.interval},
				{name: "validation failure", status: http.StatusBadRequest, message: "invalid request", failures: 1, attempts: 1, wantErr: core.ErrValidation},
				{name: "authorization failure", status: http.StatusForbidden, message: "access denied", failures: 1, attempts: 1, wantErr: core.ErrUnauthorized},
				{name: "timeout", status: http.StatusConflict, message: "operation busy", failures: -1, attempts: int(entity.timeout / entity.interval), waited: entity.timeout, wantErr: wait.ErrTimeout},
				{name: "cancellation", status: http.StatusConflict, message: "operation busy", failures: -1, attempts: 1, wantErr: context.Canceled, cancel: true},
			}
			for _, message := range entity.messages {
				cases = append(cases, scenario{name: message, status: http.StatusBadRequest, message: message, failures: 1, attempts: 2, waited: entity.interval})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					synctest.Test(t, func(t *testing.T) {
						id, projectID := core.UUID{30}, core.UUID{12}
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						attempts := 0
						httpClient := stubHTTPClient(t, func(w http.ResponseWriter, req *http.Request) {
							if req.Method != http.MethodDelete || req.URL.Path != entity.path+id.String()+"/" || req.Header.Get("Project-ID") != projectID.String() {
								t.Fatalf("unexpected delete: %s %s project=%s", req.Method, req.URL.Path, req.Header.Get("Project-ID"))
							}
							attempts++
							if tc.failures < 0 || attempts <= tc.failures {
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(tc.status)
								writeJSON(t, w, map[string]string{"detail": tc.message})
								if tc.cancel {
									cancel()
								}
								return
							}
							w.WriteHeader(entity.successStatus)
						})
						storage, err := blockstoragesdk.NewClient("https://server.test", blockstoragesdk.WithHTTPClient(httpClient), blockstoragesdk.WithRetry(core.NoRetry()))
						if err != nil {
							t.Fatal(err)
						}
						network, err := networksdk.NewClient("https://server.test", networksdk.WithHTTPClient(httpClient), networksdk.WithRetry(core.NoRetry()))
						if err != nil {
							t.Fatal(err)
						}
						r := &ServerResource{blockStorageClient: storage, networkClient: network, projectID: projectID}
						start := time.Now()
						err = entity.delete(r, ctx, id)
						attemptsMatch := attempts == tc.attempts
						// The poll timer and deadline can fire together at the timeout.
						if errors.Is(tc.wantErr, wait.ErrTimeout) {
							attemptsMatch = attemptsMatch || attempts == tc.attempts+1
						}
						if !errors.Is(err, tc.wantErr) || !attemptsMatch || time.Since(start) != tc.waited {
							t.Fatalf("err=%v want=%v attempts=%d want=%d waited=%s want=%s", err, tc.wantErr, attempts, tc.attempts, time.Since(start), tc.waited)
						}
						if errors.Is(err, wait.ErrTimeout) && !strings.Contains(err.Error(), id.String()) {
							t.Fatalf("timeout lost attachment identity: %v", err)
						}
					})
				})
			}
		})
	}
}

func testServerDeleteWaitsForVolumeDetach(t *testing.T, scenario string) {
	t.Helper()
	volumeID := core.UUID{9}
	var polls atomic.Int32
	var deleted atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/"+volumeID.String()+"/":
			attempt := polls.Add(1)
			status := blockstoragesdk.VolumeStatusInUse
			switch {
			case scenario == "detach abandoned":
				status = blockstoragesdk.VolumeStatusDetachFailed
			case attempt > 2:
				status = blockstoragesdk.VolumeStatusAvailable
			}
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: volumeID, Status: status})
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/block-storage/volumes/"+volumeID.String()+"/":
			if polls.Load() < 3 {
				t.Errorf("volume %s was deleted while it still reported in-use", volumeID)
			}
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	blockStorageClient, err := blockstoragesdk.NewClient(
		"https://server.test",
		blockstoragesdk.WithHTTPClient(stubHTTPClient(t, handler)),
		blockstoragesdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create block storage client: %v", err)
	}

	r := &ServerResource{blockStorageClient: blockStorageClient, projectID: core.UUID{12}}
	var diags diag.Diagnostics
	start := time.Now()
	r.deleteTerminatedVolumes(context.Background(), []core.UUID{volumeID}, &diags)
	waited := time.Since(start)

	if scenario == "detach abandoned" {
		// A detach the backend gave up on never becomes available, so the wait
		// ends on the first read with the status it reported rather than
		// holding the destroy open for the whole timeout.
		if !diags.HasError() || deleted.Load() || polls.Load() != 1 || waited != 0 {
			t.Fatalf(
				"expected an abandoned detach to fail immediately: polls=%d deleted=%v waited=%s diagnostics=%v",
				polls.Load(), deleted.Load(), waited, diags,
			)
		}
		return
	}
	if diags.HasError() {
		t.Fatalf("delete diagnostics: %v", diags)
	}
	if !deleted.Load() || polls.Load() != 3 {
		t.Fatalf("expected the volume to be deleted once available, got %d polls and deleted=%v", polls.Load(), deleted.Load())
	}
	if waited != 2*volumeDetachInterval {
		t.Fatalf("expected two detach poll intervals of waiting, waited %s", waited)
	}
}

// delete_on_termination is Optional and Computed, so any planned change marks
// it unknown wherever the configuration omits it. An unknown left in the plan
// spreads to every other computed attribute in the resource, which breaks a
// plan-only assertion such as the one an import makes. The value is provider
// metadata that no operation changes, so the prior value is the right stand-in.
func TestServerDeleteOnTerminationPlanKeepsThePriorValue(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	boot := resourceSchema.Attributes["boot"].(resourceschema.SingleNestedAttribute)
	for _, attribute := range []resourceschema.BoolAttribute{
		boot.Attributes["delete_on_termination"].(resourceschema.BoolAttribute),
	} {
		for _, tc := range []struct {
			name               string
			config, plan, want types.Bool
			create             bool
		}{
			{"omitted", types.BoolNull(), types.BoolUnknown(), types.BoolValue(true), false},
			{"unchanged", types.BoolValue(true), types.BoolValue(true), types.BoolValue(true), false},
			{"opted out", types.BoolValue(false), types.BoolValue(false), types.BoolValue(false), false},
			// There is no prior value to carry forward on a create, and the
			// refresh resolves the documented default instead.
			{"create", types.BoolNull(), types.BoolUnknown(), types.BoolUnknown(), true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				model := emptyServerResourceModel()
				model.ID = types.StringValue(testServerID.String())
				state := tfsdk.State{Schema: resourceSchema}
				plan := tfsdk.Plan{Schema: resourceSchema}
				if !tc.create {
					if diags := state.Set(context.Background(), &model); diags.HasError() {
						t.Fatal(diags)
					}
				}
				if diags := plan.Set(context.Background(), &model); diags.HasError() {
					t.Fatal(diags)
				}
				req := resourceplanmodifier.BoolRequest{
					State: state, Plan: plan, StateValue: types.BoolValue(true),
					ConfigValue: tc.config, PlanValue: tc.plan,
				}
				resp := resourceplanmodifier.BoolResponse{PlanValue: tc.plan}
				for _, modifier := range attribute.PlanModifiers {
					modifier.PlanModifyBool(context.Background(), req, &resp)
					req.PlanValue = resp.PlanValue
				}
				// Changing the flag only changes what a later destroy removes,
				// so it must never plan a replacement.
				if resp.Diagnostics.HasError() || !resp.PlanValue.Equal(tc.want) || resp.RequiresReplace {
					t.Fatalf(
						"unexpected delete_on_termination plan: value=%v replace=%v diagnostics=%v",
						resp.PlanValue, resp.RequiresReplace, resp.Diagnostics,
					)
				}
			})
		}
	}
}

func TestPopulateServerResourceStatePreservesValuesTheResponseOmits(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{ServerModel: ServerModel{
		Name:      types.StringValue("application"),
		UserData:  types.StringValue("#cloud-config\npackages:\n  - nginx\n"),
		Bandwidth: types.Int64Value(100),
	}}
	server := &serversdk.ServerDetailSchema{
		Id: testServerID, Name: "application",
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
		Zone:   serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
		// The response omits both values, which must not clear configured state:
		// user_data would otherwise replace the server on the next plan.
		UserData: "", Bandwidth: nil,
	}
	if diags := populateServerResourceState(context.Background(), server, &state); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if state.UserData.ValueString() != "#cloud-config\npackages:\n  - nginx\n" || state.Bandwidth.ValueInt64() != 100 {
		t.Fatalf("omitted response values overwrote configured state: %#v", state.ServerModel)
	}
}

func TestPopulateServerPendingStateLeavesNoUnknownValues(t *testing.T) {
	t.Parallel()
	plan := emptyServerResourceModel()
	plan.Name = types.StringValue("application")
	plan.Description = types.StringUnknown()
	plan.Zone = types.ObjectUnknown(reference.AttributeTypes(reference.Name))
	plan.KeyPair = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
	plan.KeyPairName = types.StringUnknown()
	plan.PlacementGroup = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
	plan.PlacementGroupName = types.StringUnknown()
	plan.UserData = types.StringUnknown()
	plan.Bandwidth = types.Int64Unknown()
	plan.SecurityGroups = types.SetUnknown(reference.ObjectType(reference.ID))
	plan.Flavor = customFlavorValue(t, 2, 2)
	plan.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
		{"id": types.StringUnknown(), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20), "iops": types.Int64Unknown()},
		{"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30), "iops": types.Int64Value(900)},
	})
	plan.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
		"iops": types.Int64Unknown(),
	})
	plan.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringUnknown(),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true),
		"ip_address":            types.StringUnknown(), "mac_address": types.StringUnknown(),
	}})
	if diags := populateServerPendingState(context.Background(), &plan, testServerID); diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	// Intermediate state is written before the server is ready, so Terraform
	// must be able to persist it after a failure.
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("persist pending state: %v", diags)
	}
	if !state.Raw.IsFullyKnown() {
		t.Fatalf("pending state still contains unknown values: %v", state.Raw)
	}
	var volumes []DataVolumeInputModel
	if diags := plan.DataVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() {
		t.Fatal(diags)
	}
	if len(volumes) != 2 || !volumes[0].IOPS.IsNull() || volumes[1].IOPS.ValueInt64() != 900 ||
		volumes[0].VolumeSize.ValueInt64() != 20 || volumes[1].VolumeSize.ValueInt64() != 30 {
		t.Fatalf("pending state changed configured data-volume values: %#v", volumes)
	}
}

func TestPopulateServerResourceStateMapsKeyPairAndPlacementGroup(t *testing.T) {
	t.Parallel()
	t.Run("populated from response", func(t *testing.T) {
		t.Parallel()
		state := emptyServerResourceModel()
		configuredKeyPair := idReference(types.StringValue(" " + strings.ToUpper(testKeyPairID.String()) + " "))
		configuredPlacementGroup := idReference(types.StringValue(" " + testPlacementGroupID.String() + " "))
		state.KeyPair = configuredKeyPair
		state.PlacementGroup = configuredPlacementGroup

		server := &serversdk.ServerDetailSchema{
			Id: testServerID, Name: "application",
			Flavor:         serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
			Zone:           serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
			KeyPair:        &serversdk.NestedKeyPairSchema{Id: testKeyPairID, Name: "my-keypair"},
			PlacementGroup: &serversdk.NestedPlacementGroupSchema{Id: testPlacementGroupID, Name: "my-pg"},
		}
		if diags := populateServerResourceState(context.Background(), server, &state); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !state.KeyPair.Equal(configuredKeyPair) {
			t.Fatalf("expected configured key_pair representation to be preserved, got %s", state.KeyPair)
		}
		if state.KeyPairName.ValueString() != "my-keypair" {
			t.Fatalf("expected key_pair_name to be %q, got %q", "my-keypair", state.KeyPairName.ValueString())
		}
		if !state.PlacementGroup.Equal(configuredPlacementGroup) {
			t.Fatalf("expected configured placement_group representation to be preserved, got %s", state.PlacementGroup)
		}
		if state.PlacementGroupName.ValueString() != "my-pg" {
			t.Fatalf("expected placement_group_name to be %q, got %q", "my-pg", state.PlacementGroupName.ValueString())
		}
	})

	t.Run("nil in response", func(t *testing.T) {
		t.Parallel()
		state := emptyServerResourceModel()
		server := &serversdk.ServerDetailSchema{
			Id: testServerID, Name: "application",
			Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
			Zone:   serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
		}
		if diags := populateServerResourceState(context.Background(), server, &state); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !state.KeyPair.IsNull() || !state.KeyPairName.IsNull() {
			t.Fatalf("expected null key_pair fields, got key_pair=%v name=%v", state.KeyPair, state.KeyPairName)
		}
		if !state.PlacementGroup.IsNull() || !state.PlacementGroupName.IsNull() {
			t.Fatalf("expected null placement_group fields, got placement_group=%v name=%v", state.PlacementGroup, state.PlacementGroupName)
		}
	})

	t.Run("response replaces a reference to another object", func(t *testing.T) {
		t.Parallel()
		state := emptyServerResourceModel()
		state.KeyPair = idReference(types.StringValue(core.UUID{0x99}.String()))
		server := &serversdk.ServerDetailSchema{
			Id: testServerID, Name: "application",
			Flavor:  serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
			Zone:    serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
			KeyPair: &serversdk.NestedKeyPairSchema{Id: testKeyPairID, Name: "my-keypair"},
		}
		if diags := populateServerResourceState(context.Background(), server, &state); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !state.KeyPair.Equal(idReference(types.StringValue(testKeyPairID.String()))) {
			t.Fatalf("expected key_pair drift to be exposed, got %s", state.KeyPair)
		}
		if !state.Zone.Equal(nameReference(types.StringValue("zone-a"))) {
			t.Fatalf("expected the backend zone in object form, got %s", state.Zone)
		}
	})
}

func TestKeyPairAndPlacementGroupPlanModifiers(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)

	keyPairAttr := resourceSchema.Attributes["key_pair"].(resourceschema.SingleNestedAttribute)
	pgAttr := resourceSchema.Attributes["placement_group"].(resourceschema.SingleNestedAttribute)
	nullReference := types.ObjectNull(reference.AttributeTypes(reference.ID))

	for _, attr := range []struct {
		name      string
		attribute resourceschema.SingleNestedAttribute
	}{
		{name: "key_pair", attribute: keyPairAttr},
		{name: "placement_group", attribute: pgAttr},
	} {
		t.Run(attr.name+" unconfigured keeps null without replace", func(t *testing.T) {
			t.Parallel()
			stateModel := emptyServerResourceModel()
			stateModel.ID = types.StringValue(testServerID.String())
			stateModel.Name = types.StringValue("server")
			planModel := stateModel

			state := tfsdk.State{Schema: resourceSchema}
			plan := tfsdk.Plan{Schema: resourceSchema}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			req := resourceplanmodifier.ObjectRequest{
				ConfigValue: nullReference,
				State:       state,
				Plan:        plan,
				StateValue:  nullReference,
				PlanValue:   types.ObjectUnknown(reference.AttributeTypes(reference.ID)),
			}
			var resp resourceplanmodifier.ObjectResponse
			for _, modifier := range attr.attribute.PlanModifiers {
				modifier.PlanModifyObject(context.Background(), req, &resp)
				req.PlanValue = resp.PlanValue
			}
			if resp.RequiresReplace || !resp.PlanValue.IsNull() {
				t.Fatalf("expected unconfigured %s to keep null without replacement, got %s replace=%v", attr.name, resp.PlanValue, resp.RequiresReplace)
			}
		})

		t.Run(attr.name+" changing configured value requires replace", func(t *testing.T) {
			t.Parallel()
			stateModel := emptyServerResourceModel()
			stateModel.ID = types.StringValue(testServerID.String())
			stateModel.Name = types.StringValue("server")
			planModel := stateModel

			state := tfsdk.State{Schema: resourceSchema}
			plan := tfsdk.Plan{Schema: resourceSchema}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			req := resourceplanmodifier.ObjectRequest{
				ConfigValue: idReference(types.StringValue("00000000-0000-0000-0000-000000000002")),
				State:       state,
				Plan:        plan,
				StateValue:  idReference(types.StringValue("00000000-0000-0000-0000-000000000001")),
				PlanValue:   idReference(types.StringValue("00000000-0000-0000-0000-000000000002")),
			}
			var resp resourceplanmodifier.ObjectResponse
			for _, modifier := range attr.attribute.PlanModifiers {
				modifier.PlanModifyObject(context.Background(), req, &resp)
				req.PlanValue = resp.PlanValue
			}
			if !resp.RequiresReplace {
				t.Fatalf("expected changing %s to require replacement", attr.name)
			}
		})
	}
}

func TestUserDataPlanModifiers(t *testing.T) {
	t.Parallel()
	resourceSchema := serverSchema(t)
	userDataAttr := resourceSchema.Attributes["user_data"].(resourceschema.StringAttribute)

	tests := []struct {
		name            string
		configValue     types.String
		stateValue      types.String
		planValue       types.String
		wantPlanValue   types.String
		requiresReplace bool
	}{
		{
			name:          "unconfigured null state",
			configValue:   types.StringNull(),
			stateValue:    types.StringNull(),
			planValue:     types.StringUnknown(),
			wantPlanValue: types.StringNull(),
		},
		{
			name:          "removed configuration preserves state",
			configValue:   types.StringNull(),
			stateValue:    types.StringValue("#cloud-config\npackages:\n  - nginx\n"),
			planValue:     types.StringUnknown(),
			wantPlanValue: types.StringValue("#cloud-config\npackages:\n  - nginx\n"),
		},
		{
			name:            "configured change requires replacement",
			configValue:     types.StringValue("#cloud-config\npackage_update: true\n"),
			stateValue:      types.StringValue("#cloud-config\npackages:\n  - nginx\n"),
			planValue:       types.StringValue("#cloud-config\npackage_update: true\n"),
			wantPlanValue:   types.StringValue("#cloud-config\npackage_update: true\n"),
			requiresReplace: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stateModel := emptyServerResourceModel()
			stateModel.ID = types.StringValue(testServerID.String())
			stateModel.Name = types.StringValue("server-before")
			stateModel.UserData = tt.stateValue
			planModel := stateModel
			planModel.Name = types.StringValue("server-after")
			planModel.UserData = tt.planValue

			state := tfsdk.State{Schema: resourceSchema}
			plan := tfsdk.Plan{Schema: resourceSchema}
			if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			req := resourceplanmodifier.StringRequest{
				ConfigValue: tt.configValue,
				State:       state,
				Plan:        plan,
				StateValue:  tt.stateValue,
				PlanValue:   tt.planValue,
			}
			requiresReplace := false
			for _, modifier := range userDataAttr.PlanModifiers {
				resp := resourceplanmodifier.StringResponse{PlanValue: req.PlanValue}
				modifier.PlanModifyString(context.Background(), req, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
				}
				req.PlanValue = resp.PlanValue
				requiresReplace = requiresReplace || resp.RequiresReplace
			}

			if !req.PlanValue.Equal(tt.wantPlanValue) {
				t.Fatalf("expected planned user_data %v, got %v", tt.wantPlanValue, req.PlanValue)
			}
			if requiresReplace != tt.requiresReplace {
				t.Fatalf("expected RequiresReplace=%t, got %t", tt.requiresReplace, requiresReplace)
			}
		})
	}
}

func TestServerReadForgetsDeletedServer(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.ServerDetailSchema{
			Id: testServerID, Name: "application",
			Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
			Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
			PowerState: serversdk.ServerPowerStateShutdown, Status: serversdk.ServerStatusDeleted,
		})
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set prior state: %v", diags)
	}
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatalf("expected a deleted server to be removed from state, got %v", response.State.Raw)
	}
}

func TestServerDeleteContinuesWhenTheServerCannotBeStopped(t *testing.T) {
	t.Parallel()
	serverDeleted := false
	stopRejected := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			if serverDeleted {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
			})
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			stopRejected = true
			http.Error(w, `{"detail":"server cannot be stopped"}`, http.StatusConflict)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			serverDeleted = true
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("application")
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set delete state: %v", diags)
	}
	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if !stopRejected || !serverDeleted {
		t.Fatalf("expected deletion to continue after a rejected shutdown: stopped=%v deleted=%v", stopRejected, serverDeleted)
	}
}

func TestServerOperationWaitsOnlyForExplicitBusyRejections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		retry  bool
	}{
		{"busy", http.StatusConflict, `{"errors":[{"code":"conflict","message":"There is another operation running on this server, retry again later"}]}`, true},
		{"unrelated conflict", http.StatusConflict, `{"errors":[{"code":"conflict","message":"The server cannot start in this state"}]}`, false},
		{"transport failure", http.StatusBadGateway, `{"detail":"upstream unavailable"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
					if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/start/") {
						t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					}
					if calls.Add(1) == 1 {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(tc.body))
						return
					}
					w.WriteHeader(http.StatusAccepted)
				})
				r := &ServerResource{client: client, projectID: core.UUID{12}}
				start := time.Now()
				err := r.requestServerOperation(context.Background(), testServerID, func(ctx context.Context) error {
					return client.StartServer(ctx, testServerID, serversdk.StartServerParams{ProjectID: r.projectID})
				})
				if tc.retry {
					if err != nil || calls.Load() != 2 || time.Since(start) != serverPollInterval {
						t.Fatalf("busy operation was not retried at the production interval: calls=%d elapsed=%s err=%v", calls.Load(), time.Since(start), err)
					}
				} else if err == nil || calls.Load() != 1 || time.Since(start) != 0 {
					t.Fatalf("non-busy rejection was retried: calls=%d elapsed=%s err=%v", calls.Load(), time.Since(start), err)
				}
			})
		})
	}
}

func TestServerOperationBusyWaitHonorsTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		busy := &serversdk.APIError{StatusCode: http.StatusConflict, Errors: []core.ErrorDetail{
			{Code: "conflict", Message: "There is another operation running on this server, retry again later"},
		}}
		r := &ServerResource{}
		start := time.Now()
		err := r.requestServerOperation(context.Background(), testServerID, func(context.Context) error { return busy })
		if !errors.Is(err, wait.ErrTimeout) || time.Since(start) != serverOperationTimeout || !strings.Contains(err.Error(), busy.Error()) {
			t.Fatalf("expected bounded wait with the backend rejection: elapsed=%s err=%v", time.Since(start), err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		err = r.requestServerOperation(ctx, testServerID, func(context.Context) error {
			cancel()
			return busy
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	})
}

func TestStartAfterOfflineUpdateWaitsUntilNotPendingBeforeStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls []string
		var reads atomic.Int32
		var started atomic.Bool
		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			switch {
			case req.Method == http.MethodGet:
				calls = append(calls, "get")
				powerState, status := serversdk.ServerPowerStatePending, serversdk.ServerStatusShutoff
				if reads.Add(1) > 1 {
					powerState = serversdk.ServerPowerStateShutdown
				}
				if started.Load() {
					powerState, status = serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
				}
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id: testServerID, PowerState: powerState, Status: status,
				})
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/start/"):
				calls = append(calls, "start")
				if reads.Load() < 2 {
					t.Error("start was requested before the server settled in shutdown")
				}
				started.Store(true)
				w.WriteHeader(http.StatusAccepted)
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		var diags diag.Diagnostics
		r.startAfterOfflineUpdate(context.Background(), testServerID, &diags)
		if diags.HasError() {
			t.Fatalf("start after offline update diagnostics: %v", diags)
		}
		want := []string{"get", "get", "start", "get"}
		if !slices.Equal(calls, want) {
			t.Fatalf("unexpected wait/start sequence: got %v want %v", calls, want)
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one poll interval before start, waited %s", waited)
		}
	})
}

func TestServerWaiterRidesOutFailedPolls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, _ *http.Request) {
			switch polls.Add(1) {
			case 1:
				// A single slow response times out in the SDK HTTP client. Abandoning
				// the wait here would fail a create that is still running.
				http.Error(w, `{"detail":"gateway timeout"}`, http.StatusGatewayTimeout)
			case 2:
				// The backend reports a status this provider cannot map.
				writeUnsupportedStatusError(t, w)
			default:
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id: testServerID, Name: "application",
					Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
					Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
					PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				})
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		if err := r.waitUntilReady(context.Background(), testServerID); err != nil {
			t.Fatalf("expected the waiter to keep polling after failed polls, got %v", err)
		}
		if polls.Load() != 3 {
			t.Fatalf("expected the waiter to poll past both failures, got %d polls", polls.Load())
		}
		if waited := time.Since(start); waited != 2*serverPollInterval {
			t.Fatalf("expected one production poll interval between polls, waited %s", waited)
		}
	})
}

func TestWaitUntilShutdownWaitsForPendingPowerStateToSettle(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, _ *http.Request) {
			powerState := serversdk.ServerPowerStatePending
			if polls.Add(1) > 1 {
				powerState = serversdk.ServerPowerStateShutdown
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: powerState, Status: serversdk.ServerStatusShutoff,
			})
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		server, err := r.waitUntilShutdown(context.Background(), testServerID)
		if err != nil {
			t.Fatalf("wait for shutdown: %v", err)
		}
		if polls.Load() != 2 || server == nil || server.PowerState != serversdk.ServerPowerStateShutdown {
			t.Fatalf("waiter returned before shutdown settled: polls=%d server=%#v", polls.Load(), server)
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one production poll interval between polls, waited %s", waited)
		}
	})
}

func TestServerDeleteRemovesServerWithUnsupportedStatus(t *testing.T) {
	t.Parallel()
	var deleted atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			if deleted.Load() {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			// A server that failed to build reports a status outside the SDK enum.
			writeUnsupportedStatusError(t, w)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			// A stop request would land here: an unmappable status must be
			// deleted directly instead of being shut down first.
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("application")
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set delete state: %v", diags)
	}
	var response resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if !deleted.Load() {
		t.Fatal("expected a server with an unsupported status to still be deleted")
	}
}

func TestServerDeleteWaitsOutASettlingServer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		testServerDeleteWaitsOutASettlingServer(t)
	})
}

func testServerDeleteWaitsOutASettlingServer(t *testing.T) {
	t.Helper()
	var rejections atomic.Int32
	var deleted atomic.Bool
	client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
		settling := rejections.Load() < 2
		switch req.Method {
		case http.MethodGet:
			if deleted.Load() {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			powerState := serversdk.ServerPowerStateShutdown
			if settling {
				powerState = serversdk.ServerPowerStatePending
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState, Status: serversdk.ServerStatusBuilding,
			})
		case http.MethodDelete:
			if settling {
				// The backend refuses to delete a server that is still settling.
				rejections.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				body := `{"errors":[{"location":"","code":"invalid_input",` +
					`"message":"Cannot delete instance with power state pending"}],"request_id":"test"}`
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("write rejection: %v", err)
				}
				return
			}
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	start := time.Now()
	if diags := r.deleteServer(context.Background(), testServerID, nil); diags.HasError() {
		t.Fatalf("expected deletion to wait out a settling server, got %v", diags)
	}
	if rejections.Load() != 2 || !deleted.Load() {
		t.Fatalf("expected retries then a deletion: rejections=%d deleted=%v", rejections.Load(), deleted.Load())
	}
	// Two rejections rest a production poll interval each before the request
	// the backend finally accepts.
	if waited := time.Since(start); waited != 2*serverPollInterval {
		t.Fatalf("expected two production poll intervals of backoff, waited %s", waited)
	}
}

func TestServerDeleteReportsAPermanentRejection(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			switch req.Method {
			case http.MethodGet:
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id: testServerID, Name: "application",
					Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "s2.small"},
					Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
					PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				})
			case http.MethodDelete:
				attempts.Add(1)
				http.Error(w, `{"detail":"server has protected resources"}`, http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusAccepted)
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		diags := r.deleteServer(context.Background(), testServerID, nil)
		// A settled server that still refuses deletion must fail immediately instead
		// of retrying until the operation deadline.
		if !diags.HasError() || attempts.Load() != 1 {
			t.Fatalf("expected one failed attempt, got %d attempts and %v", attempts.Load(), diags)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("expected a permanent rejection to fail without backoff, waited %s", waited)
		}
	})
}

func TestServerReadKeepsStateWhenStatusIsUnsupported(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeUnsupportedStatusError(t, w)
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue("application")
	state := tfsdk.State{Schema: serverSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set prior state: %v", diags)
	}
	response := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("expected a warning instead of an error, got %v", response.Diagnostics)
	}
	if response.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("expected one refresh warning, got %v", response.Diagnostics)
	}
	var kept ServerResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &kept)...)
	if kept.ID.ValueString() != testServerID.String() || kept.Name.ValueString() != "application" {
		t.Fatalf("expected the last known state to be kept, got %#v", kept.ServerModel)
	}
}

// The backend can report a server as ready before an attachment surfaces in its
// detail response. Refreshing state on that response drops the configured
// element, which Terraform rejects as ".private_ips: element 0 has vanished".
func TestWaitUntilAttachmentsSettledWaitsForALaggingAttach(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		ipv4 := "10.0.0.2"
		r := attachmentWaiterResource(t, func(w http.ResponseWriter, _ *http.Request) {
			server := &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
			}
			switch {
			case polls.Add(1) > 3:
				server.PrivateIps = []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID, IpAddress: &ipv4}}
			case polls.Load() > 2:
				// The attachment surfaces before its address does, and refreshing
				// state on that response records a null ip_address.
				server.PrivateIps = []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID}}
			}
			writeJSON(t, w, server)
		})

		start := time.Now()
		server, err := r.waitUntilAttachmentsSettled(context.Background(), testServerID,
			attachmentList(t, 1), types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}))
		if err != nil {
			t.Fatalf("wait for attachments: %v", err)
		}
		if polls.Load() != 4 || server == nil || len(server.PrivateIps) != 1 || server.PrivateIps[0].IpAddress == nil {
			t.Fatalf("waiter returned before the attach surfaced with its address: polls=%d server=%#v", polls.Load(), server)
		}
		if waited := time.Since(start); waited != 3*serverPollInterval {
			t.Fatalf("expected three production poll intervals of waiting, waited %s", waited)
		}
	})
}

// The mirror image: a detach that lags would leave an extra element in state.
func TestWaitUntilAttachmentsSettledWaitsForALaggingDetach(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		r := attachmentWaiterResource(t, func(w http.ResponseWriter, _ *http.Request) {
			publicIP := "203.0.113.10"
			elasticIPs := []serversdk.NestedElasticIPSchema{{Id: testElasticIPID, IpAddress: &publicIP}, {Id: core.UUID{9}, IpAddress: &publicIP}}
			if polls.Add(1) > 1 {
				elasticIPs = elasticIPs[:1]
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				ElasticIps: elasticIPs,
			})
		})

		start := time.Now()
		server, err := r.waitUntilAttachmentsSettled(context.Background(), testServerID,
			types.ListNull(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}), attachmentList(t, 1))
		if err != nil {
			t.Fatalf("wait for attachments: %v", err)
		}
		if polls.Load() != 2 || server == nil || len(server.ElasticIps) != 1 {
			t.Fatalf("waiter returned before the detach surfaced: polls=%d server=%#v", polls.Load(), server)
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one production poll interval of waiting, waited %s", waited)
		}
	})
}

// A list the practitioner left unset fixes no count, so whatever the backend
// allocates is correct and the waiter must not hold out for a particular number.
func TestWaitUntilAttachmentsSettledAcceptsAnyCountThePlanLeftOpen(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		r := attachmentWaiterResource(t, func(w http.ResponseWriter, _ *http.Request) {
			polls.Add(1)
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				PrivateIps: []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID}, {Id: core.UUID{9}}},
			})
		})

		start := time.Now()
		server, err := r.waitUntilAttachmentsSettled(
			context.Background(), testServerID,
			types.ListUnknown(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}),
			types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}),
		)
		if err != nil {
			t.Fatalf("wait for attachments: %v", err)
		}
		if polls.Load() != 1 || server == nil {
			t.Fatalf("expected the waiter to accept the first response, got %d polls, server=%#v", polls.Load(), server)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("expected the first response to settle the wait, waited %s", waited)
		}
	})
}

// An attachment that never surfaces must name the counts, not leave the
// practitioner with Terraform's "element 0 has vanished".
func TestWaitUntilAttachmentsSettledReportsTheCountsOnTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := attachmentWaiterResource(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				PrivateIps: []serversdk.NestedPrivateIPSchema{{Id: testPrivateIPID}},
			})
		})

		start := time.Now()
		server, err := r.waitUntilAttachmentsSettled(context.Background(), testServerID,
			attachmentList(t, 2), types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}))
		if err == nil {
			t.Fatalf("expected a timeout error, got server=%#v", server)
		}
		if !errors.Is(err, wait.ErrTimeout) {
			t.Fatalf("expected the shared waiter timeout, got %v", err)
		}
		if !strings.Contains(err.Error(), "reported 1 private IPs") {
			t.Fatalf("expected the error to report the attachment counts, got %v", err)
		}
		// An attachment that never surfaces must give up on the production
		// deadline rather than a shortened one.
		if waited := time.Since(start); waited != serverOperationTimeout {
			t.Fatalf("expected the wait to end on the production timeout, waited %s", waited)
		}
	})
}

// --- Update tests ---

func TestServerUpdateResizesFlavorInPlace(t *testing.T) {
	t.Parallel()
	resized := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			vcpus, ram := 2, 4
			if resized {
				vcpus, ram = 4, 8
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application", Description: "",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: vcpus, Ram: ram},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning,
				Status:     serversdk.ServerStatusActive,
			})
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode resize body: %v", err)
			}
			flavor, ok := body["flavor"].(map[string]any)
			if !ok || flavor["kind"] != "custom" || flavor["vcpus"] != float64(4) || flavor["ram"] != float64(8) {
				t.Errorf("unexpected resize body: %#v", body)
			}
			resized = true
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	stateModel := emptyServerResourceModel()
	stateModel.ID = types.StringValue(testServerID.String())
	stateModel.Name = types.StringValue("application")
	stateModel.Description = types.StringValue("")
	stateModel.Zone = nameReference(types.StringValue("zone-a"))
	stateModel.Quantity = types.Int64Value(1)
	stateModel.Flavor = customFlavorValue(t, 2, 4)
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("local_disk"), "image": selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String())),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringNull()),
		"volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	planModel := stateModel
	planModel.Flavor = customFlavorValue(t, 4, 8)

	resourceSchema := serverSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set prior state: %v", diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !resized {
		t.Fatal("expected resize API to be called")
	}
}

// resizeServer provides the fallback-enabled resize workflow for unit testing.
func (r *ServerResource) resizeServer(ctx context.Context, serverID core.UUID, body serversdk.ServerResizeSchema) diag.Diagnostics {
	needsShutdown, diags := r.tryLiveResize(ctx, serverID, body)
	if diags.HasError() || !needsShutdown {
		return diags
	}
	return r.resizeStoppedServer(ctx, serverID, body)
}

// resizeStoppedServer provides the stop/resize/start fallback sequence for unit testing.
func (r *ServerResource) resizeStoppedServer(
	ctx context.Context,
	serverID core.UUID,
	body serversdk.ServerResizeSchema,
) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.StopServer(ctx, serverID, serversdk.StopServerParams{ProjectID: r.projectID})
	}); err != nil {
		diags.AddError("Error stopping server for resize", err.Error())
		return diags
	}
	if _, err := r.waitUntilShutdown(ctx, serverID); err != nil {
		diags.AddError("Error waiting for server shutdown before resize", err.Error())
		return diags
	}
	diags.Append(r.resizeOfflineServer(ctx, serverID, body)...)
	if err := r.requestServerOperation(ctx, serverID, func(ctx context.Context) error {
		return r.client.StartServer(ctx, serverID, serversdk.StartServerParams{ProjectID: r.projectID})
	}); err != nil {
		diags.AddError("Error restarting server after resize", err.Error())
		return diags
	}
	if err := r.waitUntilRunning(ctx, serverID); err != nil {
		diags.AddError("Error waiting for server restart after resize", err.Error())
	}
	return diags
}

func TestResizeServerStopsAndRestartsWhenLiveResizeIsRejected(t *testing.T) {
	t.Parallel()
	var calls []string
	resizeCalls := 0
	stopped := false
	started := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			calls = append(calls, "resize")
			resizeCalls++
			if resizeCalls == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"errors":[{"code":"invalid_input","message":"Cannot resize server with power state running"}]}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			vcpus, ram := 2, 4
			if resizeCalls > 1 {
				vcpus, ram = 4, 8
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Flavor: serversdk.NestedFlavorSchema{Vcpus: vcpus, Ram: ram},
				PowerState: powerState, Status: status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	var flavor serversdk.ServerFlavor
	if err := flavor.FromCustomServerFlavor(serversdk.CustomServerFlavor{Family: serversdk.FlavorFamilyBasic, Vcpus: 4, Ram: 8}); err != nil {
		t.Fatalf("build resize flavor: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	if diags := r.resizeServer(context.Background(), testServerID, serversdk.ServerResizeSchema{Flavor: flavor}); diags.HasError() {
		t.Fatalf("resize diagnostics: %v", diags)
	}
	want := []string{"resize", "stop", "get", "resize", "get", "start", "get"}
	if !slices.Equal(calls, want) {
		t.Fatalf("unexpected fallback sequence: got %v want %v", calls, want)
	}
}

// The stop the fallback performs is the provider's own doing, so a resize the
// backend rejects once the server is stopped must still start it again. The
// backend refuses a flavor the server already runs on, which is exactly the
// case that used to leave a running server shut down.
func TestResizeServerRestartsWhenTheStoppedResizeIsRejected(t *testing.T) {
	t.Parallel()
	var calls []string
	resizeCalls := 0
	stopped := false
	started := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			calls = append(calls, "resize")
			resizeCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if resizeCalls == 1 {
				_, _ = w.Write([]byte(`{"errors":[{"code":"invalid_input","message":"Cannot resize server with power state running"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"errors":[{"code":"invalid_input","message":"Choose a different flavor from the server's current flavor"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Flavor: serversdk.NestedFlavorSchema{Vcpus: 2, Ram: 2},
				PowerState: powerState, Status: status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	var flavor serversdk.ServerFlavor
	if err := flavor.FromCustomServerFlavor(serversdk.CustomServerFlavor{Family: serversdk.FlavorFamilyBasic, Vcpus: 2, Ram: 2}); err != nil {
		t.Fatalf("build resize flavor: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	diags := r.resizeServer(context.Background(), testServerID, serversdk.ServerResizeSchema{Flavor: flavor})
	if !diags.HasError() {
		t.Fatal("expected the rejected resize to report an error")
	}
	if summary := diags.Errors()[0].Summary(); summary != "Error resizing stopped server" {
		t.Fatalf("unexpected first error summary: %q", summary)
	}
	want := []string{"resize", "stop", "get", "resize", "start", "get"}
	if !slices.Equal(calls, want) {
		t.Fatalf("unexpected fallback sequence: got %v want %v", calls, want)
	}
}

func TestResizeRequiresShutdownOnlyForRunningPowerStateRejection(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		err  error
		want bool
	}{
		"matching rejection": {
			err: &serversdk.APIError{Errors: []core.ErrorDetail{{
				Code: "invalid_input", Message: " Cannot resize server with power state RUNNING ",
			}}},
			want: true,
		},
		"different validation error": {
			err: &serversdk.APIError{Errors: []core.ErrorDetail{{
				Code: "invalid_input", Message: "Requested flavor is unavailable",
			}}},
		},
		"different error code": {
			err: &serversdk.APIError{Errors: []core.ErrorDetail{{
				Code: "provider_error", Message: "Cannot resize server with power state running",
			}}},
		},
		"unstructured error": {err: errors.New("cannot resize server with power state running")},
		"no error":           {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := resizeRequiresShutdown(test.err); got != test.want {
				t.Fatalf("resizeRequiresShutdown() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestServerUpdateKeepsAppliedChangesWhenALaterRequestFails(t *testing.T) {
	t.Parallel()
	renamed := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPatch && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			renamed = true
			writeJSON(t, w, &serversdk.ServerSchema{Id: testServerID, Name: "after"})
		case req.Method == http.MethodPut && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/bandwidth/":
			http.Error(w, `{"detail":"bandwidth quota exceeded"}`, http.StatusConflict)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			name := "before"
			if renamed {
				name = "after"
			}
			bandwidth := 100
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: name, Bandwidth: &bandwidth,
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "before")
	stateModel.Bandwidth = types.Int64Value(100)
	planModel := stateModel
	planModel.Name = types.StringValue("after")
	planModel.Bandwidth = types.Int64Value(200)

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected the rejected bandwidth update to fail")
	}
	var saved ServerResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &saved)...)
	// Without a refresh after the failure, Terraform would keep the prior name
	// and try to rename an already renamed server on the next apply.
	if saved.Name.ValueString() != "after" || saved.Bandwidth.ValueInt64() != 100 {
		t.Fatalf("expected state to record the applied rename and the unchanged bandwidth, got %#v", saved.ServerModel)
	}
}

func TestServerUpdateStopsBeforeResizingAndStartsLast(t *testing.T) {
	t.Parallel()
	var mutations atomic.Int32
	var stoppedAt atomic.Int32
	var resizedAt atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			stoppedAt.Store(mutations.Add(1))
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			resizedAt.Store(mutations.Add(1))
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			vcpus, ram := 2, 4
			if resizedAt.Load() > 0 {
				vcpus, ram = 4, 8
			}
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stoppedAt.Load() > 0 {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "application",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: vcpus, Ram: ram},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState, Status: status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "application")
	stateModel.PowerState = types.StringValue("running")
	planModel := stateModel
	planModel.PowerState = types.StringValue("shutdown")
	planModel.Flavor = customFlavorValue(t, 4, 8)

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	// The backend can reject a resize on a running server, so a requested
	// shutdown has to happen first.
	if stoppedAt.Load() != 1 || resizedAt.Load() != 2 {
		t.Fatalf("expected the shutdown to precede the resize, stopped=%d resized=%d", stoppedAt.Load(), resizedAt.Load())
	}
}

func TestServerUpdateRebuildRunningServerStopsRebuildsAndRestarts(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	stopped := false
	started := false
	rebuilt := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuilt = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			currentImage := testImageID
			if rebuilt {
				currentImage = rebuiltImageID
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: currentImage, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	wantCalls := []string{"stop", "get", "rebuild", "get", "get", "start", "get", "get"}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("unexpected call sequence: got %v want %v", calls, wantCalls)
	}
}

func TestServerUpdateRebuildStoppedServerRebuildsWithoutRestart(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	rebuilt := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuilt = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			currentImage := testImageID
			if rebuilt {
				currentImage = rebuiltImageID
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: currentImage, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateShutdown,
				Status:     serversdk.ServerStatusShutoff,
			})
		case req.Method == http.MethodPost && (req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/" ||
			req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/"):
			t.Errorf("unexpected power mutation on stopped server: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("shutdown")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	wantCalls := []string{"rebuild", "get", "get"}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("unexpected call sequence: got %v want %v", calls, wantCalls)
	}
}

func TestServerUpdateRebuildStoppedServerStartsWhenRunningRequested(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls []string
		var startAttempts atomic.Int32
		rebuiltImageID := core.UUID{10, 20}
		rebuilt := false
		started := false

		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			switch {
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/rebuild/"):
				calls = append(calls, "rebuild")
				rebuilt = true
				w.WriteHeader(http.StatusAccepted)
			case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/start/"):
				calls = append(calls, "start")
				if startAttempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"errors":[{"code":"conflict","message":"There is another operation running on this server, retry again later"}]}`))
					return
				}
				started = true
				w.WriteHeader(http.StatusAccepted)
			case req.Method == http.MethodGet:
				calls = append(calls, "get")
				imageID := testImageID
				if rebuilt {
					imageID = rebuiltImageID
				}
				powerState, status := serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
				if started {
					powerState, status = serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
				}
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id:         testServerID,
					Name:       "server-rebuild-start",
					Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
					Image:      &serversdk.NestedImageSchema{Id: imageID, Name: "image"},
					Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
					PowerState: powerState,
					Status:     status,
				})
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		stateModel := localDiskServerModel(t, "server-rebuild-start")
		stateModel.PowerState = types.StringValue("shutdown")
		stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
			"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
			"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
			"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
		})

		planModel := stateModel
		planModel.PowerState = types.StringValue("running")
		planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
			"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
			"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
			"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
		})

		resourceSchema := serverSchema(t)
		response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
		start := time.Now()
		r.Update(context.Background(), resource.UpdateRequest{
			Plan:  planFor(t, resourceSchema, planModel),
			State: stateFor(t, resourceSchema, stateModel),
		}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("update diagnostics: %v", response.Diagnostics)
		}
		want := []string{"rebuild", "get", "get", "start", "start", "get", "get"}
		if !slices.Equal(calls, want) {
			t.Fatalf("unexpected rebuild/start sequence: got %v want %v", calls, want)
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one retry interval before start succeeded, waited %s", waited)
		}
	})
}

func TestServerUpdateRebuildAndLiveResizeRunningServerSingleRestart(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	stopped := false
	started := false
	rebuilt := false
	resized := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			calls = append(calls, "resize")
			resized = true
			var body serversdk.ServerResizeSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode resize body: %v", err)
			}
			if body.LiveResize == nil || !*body.LiveResize {
				t.Errorf("expected live_resize to be true in tryLiveResize, got %v", body.LiveResize)
			}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuilt = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			currentImage := testImageID
			if rebuilt {
				currentImage = rebuiltImageID
			}
			currentFlavor := serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4}
			if resized {
				currentFlavor = serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 4, Ram: 8}
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild-resize",
				Flavor:     currentFlavor,
				Image:      &serversdk.NestedImageSchema{Id: currentImage, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild-resize")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	stateModel.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(2), "ram": types.Int64Value(4), "name": types.StringNull(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	planModel.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(4), "ram": types.Int64Value(8), "name": types.StringNull(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	stopCount := 0
	startCount := 0
	for _, call := range calls {
		if call == "stop" {
			stopCount++
		}
		if call == "start" {
			startCount++
		}
	}
	if stopCount != 1 || startCount != 1 {
		t.Fatalf("expected exactly 1 stop and 1 start (1 restart), got stop=%d start=%d calls=%v", stopCount, startCount, calls)
	}

	wantCalls := []string{"get", "resize", "get", "stop", "get", "rebuild", "get", "get", "start", "get", "get"}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("unexpected call sequence: got %v want %v", calls, wantCalls)
	}

	// Live resize occurs in Phase 1 before stop for Phase 2 rebuild, followed by start in Phase 3
	stopIdx, rebuildIdx, resizeIdx, startIdx := -1, -1, -1, -1
	for i, call := range calls {
		switch call {
		case "stop":
			if stopIdx == -1 {
				stopIdx = i
			}
		case "rebuild":
			rebuildIdx = i
		case "resize":
			if resizeIdx == -1 {
				resizeIdx = i
			}
		case "start":
			startIdx = i
		}
	}
	if resizeIdx >= stopIdx || stopIdx >= rebuildIdx || rebuildIdx >= startIdx {
		t.Fatalf("expected order resize < stop < rebuild < start, got sequence: %v", calls)
	}
}

func TestServerUpdateRebuildAndOfflineResizeRunningServerSingleRestart(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	resizeAttempts := 0
	stopped := false
	started := false
	rebuilt := false
	resized := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			calls = append(calls, "resize")
			resizeAttempts++
			var body serversdk.ServerResizeSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode resize body: %v", err)
			}
			if resizeAttempts == 1 {
				if body.LiveResize == nil || !*body.LiveResize {
					t.Errorf("expected live_resize to be true for attempt 1 (live), got %v", body.LiveResize)
				}
				// Reject live resize to trigger offline fallback
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"errors":[{"code":"invalid_input","message":"Cannot resize server with power state running"}]}`))
				return
			}
			if body.LiveResize == nil || *body.LiveResize {
				t.Errorf("expected live_resize to be false for attempt 2 (offline), got %v", body.LiveResize)
			}
			resized = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuilt = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			currentImage := testImageID
			if rebuilt {
				currentImage = rebuiltImageID
			}
			vcpus, ram := 2, 4
			if resized {
				vcpus, ram = 4, 8
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild-resize",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: vcpus, Ram: ram},
				Image:      &serversdk.NestedImageSchema{Id: currentImage, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild-resize")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	stateModel.Flavor = customFlavorValue(t, 2, 4)

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	planModel.Flavor = customFlavorValue(t, 4, 8)

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	stopCount := 0
	startCount := 0
	for _, call := range calls {
		if call == "stop" {
			stopCount++
		}
		if call == "start" {
			startCount++
		}
	}
	if stopCount != 1 || startCount != 1 {
		t.Fatalf("expected exactly 1 stop and 1 start (1 restart), got stop=%d start=%d calls=%v", stopCount, startCount, calls)
	}

	// Live resize rejected -> stop once -> offline resize -> offline rebuild -> start once
	stopIdx, rebuildIdx, offlineResizeIdx, startIdx := -1, -1, -1, -1
	resizeCount := 0
	for i, call := range calls {
		switch call {
		case "stop":
			if stopIdx == -1 {
				stopIdx = i
			}
		case "rebuild":
			rebuildIdx = i
		case "resize":
			resizeCount++
			if resizeCount == 2 {
				offlineResizeIdx = i
			}
		case "start":
			startIdx = i
		}
	}
	if stopIdx >= offlineResizeIdx || offlineResizeIdx >= rebuildIdx || rebuildIdx >= startIdx {
		t.Fatalf("expected order stop < offline resize < rebuild < start, got sequence: %v", calls)
	}
}

func TestServerUpdateRebuildFailedPreservesLiveResize(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	stopped := false
	started := false
	resized := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/resize/":
			calls = append(calls, "resize")
			resized = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"code":"server_error","message":"Rebuild failed in backend"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			vcpus, ram := 2, 4
			if resized {
				vcpus, ram = 4, 8
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild-resize",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: vcpus, Ram: ram},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild-resize")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	stateModel.Flavor = customFlavorValue(t, 2, 4)

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	planModel.Flavor = customFlavorValue(t, 4, 8)

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)

	if !response.Diagnostics.HasError() {
		t.Fatal("expected update to fail when rebuild fails")
	}

	// Verify server was restarted after failed rebuild
	startedFound := slices.Contains(calls, "start")
	if !startedFound {
		t.Fatalf("expected server to be restarted after failed rebuild, calls=%v", calls)
	}

	// Verify the live resize was preserved in refreshed state despite the rebuild failure
	var kept ServerResourceModel
	if diags := response.State.Get(context.Background(), &kept); diags.HasError() {
		t.Fatalf("read refreshed state: %v", diags)
	}
	var flavor FlavorInputModel
	if diags := kept.Flavor.As(context.Background(), &flavor, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("extract flavor from state: %v", diags)
	}
	if flavor.VCPUs.ValueInt64() != 4 || flavor.RAM.ValueInt64() != 8 {
		t.Fatalf("expected state to preserve live resize (4 vCPUs, 8 RAM), got: %d vCPUs, %d RAM", flavor.VCPUs.ValueInt64(), flavor.RAM.ValueInt64())
	}
}

func TestServerUpdateRebuildWithExplicitShutdownDoesNotRestart(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	stopped := false
	rebuilt := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuilt = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			t.Errorf("unexpected start request when explicit shutdown is configured")
			w.WriteHeader(http.StatusBadRequest)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			currentImage := testImageID
			if rebuilt {
				currentImage = rebuiltImageID
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: currentImage, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.PowerState = types.StringValue("shutdown")
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	for _, call := range calls {
		if call == "start" {
			t.Fatal("expected server not to be restarted after rebuild when desired state is shutdown")
		}
	}
}

func TestServerUpdateRebuildFailedRestartsRunningServer(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuiltImageID := core.UUID{10, 20}
	stopped := false
	started := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"code":"server_error","message":"Rebuild failed in backend"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected update diagnostics to report error for failed rebuild")
	}

	startedFound := slices.Contains(calls, "start")
	if !startedFound {
		t.Fatalf("expected server to be restarted after failed rebuild to restore running state, calls=%v", calls)
	}
}

func TestServerFlavorMatchesDispatchesCustomUnion(t *testing.T) {
	t.Parallel()
	var expected serversdk.ServerFlavor
	if err := expected.FromCustomServerFlavor(serversdk.CustomServerFlavor{
		Family: serversdk.FlavorFamilyBasic,
		Vcpus:  4,
		Ram:    8,
	}); err != nil {
		t.Fatalf("build custom flavor: %v", err)
	}
	matches, err := serverFlavorMatches(serversdk.NestedFlavorSchema{Id: testFlavorID, Vcpus: 4, Ram: 8}, expected)
	if err != nil || !matches {
		t.Fatalf("expected custom flavor to match, matches=%v err=%v", matches, err)
	}
}

func TestServerUpdateRebuildPausedServerReturnsActionableError(t *testing.T) {
	t.Parallel()
	rebuiltImageID := core.UUID{10, 20}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/" {
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStatePaused,
				Status:     serversdk.ServerStatusActive,
			})
			return
		}
		t.Errorf("unexpected request on paused server rebuild: %s %s", req.Method, req.URL.Path)
		http.NotFound(w, req)
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("paused")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected update to fail when rebuilding a paused server")
	}
	if summary := response.Diagnostics.Errors()[0].Summary(); summary != "Cannot rebuild server in current power state" {
		t.Fatalf("unexpected error summary: %q", summary)
	}
}

func TestServerUpdateRebuildRetriesOnOperationConflict(t *testing.T) {
	t.Parallel()
	var calls []string
	rebuildAttempts := 0
	stopped := false
	started := false
	rebuiltImageID := core.UUID{10, 20}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			calls = append(calls, "stop")
			stopped = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/rebuild/":
			calls = append(calls, "rebuild")
			rebuildAttempts++
			if rebuildAttempts == 1 {
				// First attempt returns 409 Conflict indicating previous operation lock is still settling
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"errors":[{"code":"conflict","message":"There is another operation running on this server, retry again later"}]}`))
				return
			}
			// Second attempt succeeds
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			calls = append(calls, "start")
			started = true
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			calls = append(calls, "get")
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			imageID := testImageID
			if stopped && !started {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			if rebuildAttempts > 1 {
				imageID = rebuiltImageID
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-rebuild",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: imageID, Name: "image"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	stateModel := localDiskServerModel(t, "server-rebuild")
	stateModel.PowerState = types.StringValue("running")
	stateModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(testImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	planModel := stateModel
	planModel.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringNull()),
		"custom_image": idReference(types.StringValue(rebuiltImageID.String())), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected update error: %v", response.Diagnostics)
	}

	if rebuildAttempts != 2 {
		t.Fatalf("expected 2 rebuild attempts (1 conflict + 1 retry success), got: %d", rebuildAttempts)
	}
}

func TestServerUpdateAllowsUnmanagedPowerStateDuringUnrelatedChange(t *testing.T) {
	t.Parallel()
	var renamed atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPatch && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			renamed.Store(true)
			writeJSON(t, w, &serversdk.ServerSchema{Id: testServerID, Name: "after"})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, Name: "after",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStatePaused, Status: serversdk.ServerStatusActive,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	stateModel := localDiskServerModel(t, "before")
	stateModel.PowerState = types.StringValue("paused")
	planModel := stateModel
	planModel.Name = types.StringValue("after")
	planModel.PowerState = types.StringValue("paused")
	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unrelated update failed on unmanaged power state: %v", response.Diagnostics)
	}
	if !renamed.Load() {
		t.Fatal("expected unrelated rename to be applied")
	}
}

func TestServerUpdateRejectsInvalidAttachmentBeforeMutation(t *testing.T) {
	t.Parallel()
	stateModel := emptyServerResourceModel()
	stateModel.ID = types.StringValue(testServerID.String())
	stateModel.Name = types.StringValue("server-before")
	planModel := stateModel
	planModel.Name = types.StringValue("server-after")
	planModel.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringUnknown(),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringValue("10.0.0.0/24")),
		"delete_on_termination": types.BoolValue(false), "ip_address": types.StringUnknown(), "mac_address": types.StringUnknown(),
	}})

	resourceSchema := serverSchema(t)
	state := tfsdk.State{Schema: resourceSchema}
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &stateModel); diags.HasError() {
		t.Fatalf("set prior state: %v", diags)
	}
	if diags := plan.Set(context.Background(), &planModel); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	// A nil API client makes any accidental mutation panic, so a diagnostic return
	// also proves attachment validation happens before the name PATCH.
	(&ServerResource{}).Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Invalid subnet reference" {
		t.Fatalf("expected attachment preflight diagnostic, got %v", response.Diagnostics)
	}
}

func TestFlavorAndBootSourceChangesIgnoreEquivalentWhitespace(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{
		Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
			"kind": types.StringValue("predefined"), "name": types.StringValue("small"),
			"family": types.StringNull(), "vcpus": types.Int64Null(), "ram": types.Int64Null(),
		}),
		Boot: objectValue(t, bootAttributeTypes(), map[string]attr.Value{
			"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
			"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
			"volume_size": types.Int64Value(30), "iops": types.Int64Null(),
		}),
	}
	plan := state
	plan.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue(" predefined "), "name": types.StringValue(" small "),
		"family": types.StringNull(), "vcpus": types.Int64Null(), "ram": types.Int64Null(),
	})
	plan.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue(" image "), "image": selector.WithID(testImageSelector(types.StringValue(" ubuntu ")), types.StringValue(testImageID.String())),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
		"volume_size": types.Int64Value(30), "iops": types.Int64Null(),
	})
	changed, flavorDiags := flavorChanged(context.Background(), plan, state)
	bootChanged, bootDiags := bootSourceChanged(context.Background(), plan, state)
	if flavorDiags.HasError() || bootDiags.HasError() || changed || bootChanged {
		t.Fatalf("equivalent values caused update: flavor=%v boot=%v flavor_diags=%v boot_diags=%v", changed, bootChanged, flavorDiags, bootDiags)
	}
}

func TestFlavorChangedAdoptsImportedCustomFamilyWithoutResize(t *testing.T) {
	t.Parallel()
	state := ServerResourceModel{Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringNull(),
		"vcpus": types.Int64Value(4), "ram": types.Int64Value(8),
	})}
	plan := ServerResourceModel{Flavor: objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(4), "ram": types.Int64Value(8),
	})}
	changed, diags := flavorChanged(context.Background(), plan, state)
	if diags.HasError() || changed {
		t.Fatalf("imported custom flavor family adoption required resize: changed=%v diagnostics=%v", changed, diags)
	}
}

func TestBuildServerRebuildBody(t *testing.T) {
	t.Parallel()
	boot := objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()),
		"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30), "iops": types.Int64Null(),
	})
	body, imageID, diags := buildServerRebuildBody(
		context.Background(),
		boot,
		core.UUID{9},
		func(_ context.Context, filter serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
			if filter.Name == nil || *filter.Name != "ubuntu" {
				t.Fatalf("unexpected image filter: %#v", filter)
			}
			return serversdk.ImageSchema{Id: testImageID}, nil
		},
	)
	if diags.HasError() || body.ImageId == nil || *body.ImageId != testImageID || imageID != testImageID {
		t.Fatalf("unexpected rebuild body: %#v, image=%s, diagnostics=%v", body, imageID, diags)
	}
}

func TestBuildSecurityGroupUpdateBodySupportsDetachAll(t *testing.T) {
	t.Parallel()
	body, diags := buildSecurityGroupUpdateBody(reference.Set(reference.ID, []string{}))
	if diags.HasError() || body.SecurityGroups == nil || len(body.SecurityGroups) != 0 {
		t.Fatalf("expected an explicit empty security group list, got %#v and %v", body, diags)
	}
}

// --- Attachment tests ---

func TestMatchPrivateIPConfigsPreservesComputedSubnetID(t *testing.T) {
	t.Parallel()
	planned := []PrivateIPInputModel{{
		Kind: types.StringValue("subnet"), ID: types.StringUnknown(),
		Subnet: subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
	}}
	prior := []PrivateIPInputModel{{
		Kind: types.StringValue("subnet"), ID: types.StringValue(testPrivateIPID.String()),
		Subnet: subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
	}}
	matches, used := matchPrivateIPConfigs(planned, prior)
	if len(matches) != 1 || matches[0] != 0 || len(used) != 1 || !used[0] || planned[0].ID.ValueString() != testPrivateIPID.String() {
		t.Fatalf("expected prior computed ID to be retained, planned=%#v matches=%v used=%v", planned, matches, used)
	}
}

func TestMatchElasticIPConfigsRecreatesWhenAddressFamilyChanges(t *testing.T) {
	t.Parallel()
	planned := []ElasticIPInputModel{{
		Kind: types.StringValue("new"), ID: types.StringUnknown(), EnableIPv4: types.BoolValue(false), EnableIPv6: types.BoolValue(true),
	}}
	prior := []ElasticIPInputModel{{
		Kind: types.StringValue("new"), ID: types.StringValue(testElasticIPID.String()), EnableIPv4: types.BoolValue(true), EnableIPv6: types.BoolValue(false),
	}}
	matches, used := matchElasticIPConfigs(planned, prior)
	if len(matches) != 1 || matches[0] != -1 || len(used) != 1 || used[0] || !planned[0].ID.IsUnknown() {
		t.Fatalf("expected changed address-family options to recreate the attachment, planned=%#v matches=%v used=%v", planned, matches, used)
	}
}

func TestPrivateIPPlanModifierPreservesIDOnlyForEquivalentSubnet(t *testing.T) {
	t.Parallel()
	prior := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringValue(testPrivateIPID.String()),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("10.0.0.2"),
		"mac_address": types.StringValue("02:00:00:00:00:01"),
	}})
	for name, subnetID := range map[string]string{
		"unchanged": testSubnetID.String(),
		"changed":   "07000000-0000-0000-0000-000000000000",
	} {
		t.Run(name, func(t *testing.T) {
			configured := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
				"kind": types.StringValue("subnet"), "id": types.StringNull(),
				"subnet":                subnetReference(types.StringValue(subnetID), types.StringNull()),
				"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
				"mac_address": types.StringNull(),
			}})
			// Terraform's ProposedNew value carries computed attributes from the
			// prior element at the same list index, even though config omitted them.
			planned := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
				"kind": types.StringValue("subnet"), "id": types.StringValue(testPrivateIPID.String()),
				"subnet":                subnetReference(types.StringValue(subnetID), types.StringNull()),
				"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("10.0.0.2"),
				"mac_address": types.StringValue("02:00:00:00:00:01"),
			}})
			response := resourceplanmodifier.ListResponse{PlanValue: planned}
			preservePrivateIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
				ConfigValue: configured, PlanValue: planned, StateValue: prior,
			}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
			}
			var values []PrivateIPInputModel
			if diags := response.PlanValue.ElementsAs(context.Background(), &values, false); diags.HasError() {
				t.Fatalf("decode planned private IPs: %v", diags)
			}
			if name == "unchanged" && values[0].ID.ValueString() != testPrivateIPID.String() {
				t.Fatalf("expected unchanged attachment ID to be preserved, got %v", values[0].ID)
			}
			if name == "changed" && (!values[0].ID.IsUnknown() || !values[0].IPAddress.IsUnknown() ||
				!values[0].MACAddress.IsUnknown() || !values[0].DeleteOnTermination.IsUnknown()) {
				t.Fatalf("expected changed attachment computed values to be unknown, got %#v", values[0])
			}
		})
	}
}

func TestElasticIPPlanModifierLeavesNewIDUnknownWhenAddressFamilyChanges(t *testing.T) {
	t.Parallel()
	prior := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("203.0.113.2"),
		"ipv6_address": types.StringNull(), "status": types.StringValue("active"),
	}})
	configured := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringNull(),
		"enable_ipv4": types.BoolValue(false), "enable_ipv6": types.BoolValue(true),
		"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
		"ipv6_address": types.StringNull(), "status": types.StringNull(),
	}})
	planned := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(false), "enable_ipv6": types.BoolValue(true),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("203.0.113.2"),
		"ipv6_address": types.StringNull(), "status": types.StringValue("active"),
	}})
	response := resourceplanmodifier.ListResponse{PlanValue: planned}
	preserveElasticIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
		ConfigValue: configured, PlanValue: planned, StateValue: prior,
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
	}
	var values []ElasticIPInputModel
	if diags := response.PlanValue.ElementsAs(context.Background(), &values, false); diags.HasError() {
		t.Fatalf("decode planned elastic IPs: %v", diags)
	}
	if !values[0].ID.IsUnknown() || !values[0].IPAddress.IsUnknown() || !values[0].IPv6Address.IsUnknown() ||
		!values[0].Status.IsUnknown() || !values[0].DeleteOnTermination.IsUnknown() {
		t.Fatalf("expected recreated elastic IP computed values to be unknown, got %#v", values[0])
	}
}

func TestElasticIPPlanModifierLeavesStatusUnknownWhenAddingAttachment(t *testing.T) {
	t.Parallel()
	priorElement := map[string]attr.Value{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("203.0.113.2"),
		"ipv6_address": types.StringNull(), "status": types.StringValue("down"),
	}
	prior := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{priorElement})
	configured := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{
		{
			"kind": types.StringValue("new"), "id": types.StringNull(),
			"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
			"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
			"ipv6_address": types.StringNull(), "status": types.StringNull(),
		},
		{
			"kind": types.StringValue("new"), "id": types.StringNull(),
			"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
			"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
			"ipv6_address": types.StringNull(), "status": types.StringNull(),
		},
	})
	planned := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{
		priorElement,
		{
			"kind": types.StringValue("new"), "id": types.StringUnknown(),
			"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
			"delete_on_termination": types.BoolUnknown(), "ip_address": types.StringUnknown(),
			"ipv6_address": types.StringUnknown(), "status": types.StringUnknown(),
		},
	})
	response := resourceplanmodifier.ListResponse{PlanValue: planned}
	preserveElasticIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
		ConfigValue: configured, PlanValue: planned, StateValue: prior,
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
	}
	var values []ElasticIPInputModel
	if diags := response.PlanValue.ElementsAs(context.Background(), &values, false); diags.HasError() {
		t.Fatalf("decode planned elastic IPs: %v", diags)
	}
	if len(values) != 2 || values[0].ID.ValueString() != testElasticIPID.String() || !values[0].Status.IsUnknown() ||
		!values[1].ID.IsUnknown() || !values[1].Status.IsUnknown() {
		t.Fatalf("expected the existing ID to remain stable and both statuses to be unknown, got %#v", values)
	}
}

// The attachment inputs are Computed only so that an omitted list keeps its
// prior value. Terraform proposes a Computed input that config omits from the
// prior element at the same index, and the Framework marks it unknown on
// create, so the plan must restore the configured null.
func TestElasticIPPlanModifierPlansConfiguredInputs(t *testing.T) {
	t.Parallel()
	prior := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(true),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("203.0.113.2"),
		"ipv6_address": types.StringValue("2001:db8::2"), "status": types.StringValue("active"),
	}})
	configured := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringNull(),
		"enable_ipv4": types.BoolNull(), "enable_ipv6": types.BoolNull(),
		"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
		"ipv6_address": types.StringNull(), "status": types.StringNull(),
	}})
	for name, test := range map[string]struct {
		planIPv4, planIPv6 types.Bool
		state              types.List
	}{
		"create":            {types.BoolUnknown(), types.BoolUnknown(), types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()})},
		"update from prior": {types.BoolValue(true), types.BoolValue(true), prior},
	} {
		t.Run(name, func(t *testing.T) {
			planned := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
				"kind": types.StringValue("new"), "id": types.StringUnknown(),
				"enable_ipv4": test.planIPv4, "enable_ipv6": test.planIPv6,
				"delete_on_termination": types.BoolUnknown(), "ip_address": types.StringUnknown(),
				"ipv6_address": types.StringUnknown(), "status": types.StringUnknown(),
			}})
			response := resourceplanmodifier.ListResponse{PlanValue: planned}
			preserveElasticIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
				ConfigValue: configured, PlanValue: planned, StateValue: test.state,
			}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
			}
			var values []ElasticIPInputModel
			if diags := response.PlanValue.ElementsAs(context.Background(), &values, false); diags.HasError() {
				t.Fatalf("decode planned elastic IPs: %v", diags)
			}
			if !values[0].EnableIPv4.IsNull() || !values[0].EnableIPv6.IsNull() || values[0].Kind.ValueString() != "new" {
				t.Fatalf("expected configured inputs in the plan, got %#v", values[0])
			}
		})
	}
}

func TestPrivateIPPlanModifierPlansConfiguredInputs(t *testing.T) {
	t.Parallel()
	configured := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("ip"), "id": types.StringValue(testPrivateIPID.String()),
		"subnet":                types.ObjectNull(reference.AttributeTypes(reference.ID, subnetCIDR)),
		"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
		"mac_address": types.StringNull(),
	}})
	planned := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("ip"), "id": types.StringValue(testPrivateIPID.String()),
		"subnet":                types.ObjectUnknown(reference.AttributeTypes(reference.ID, subnetCIDR)),
		"delete_on_termination": types.BoolUnknown(), "ip_address": types.StringUnknown(),
		"mac_address": types.StringUnknown(),
	}})
	response := resourceplanmodifier.ListResponse{PlanValue: planned}
	preservePrivateIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
		ConfigValue: configured, PlanValue: planned,
		StateValue: types.ListNull(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
	}
	var values []PrivateIPInputModel
	if diags := response.PlanValue.ElementsAs(context.Background(), &values, false); diags.HasError() {
		t.Fatalf("decode planned private IPs: %v", diags)
	}
	if !values[0].Subnet.IsNull() || !values[0].IPAddress.IsUnknown() {
		t.Fatalf("expected the configured null subnet and an unknown address, got %#v", values[0])
	}
}

// Terraform proposes the prior list for an omitted list once every nested
// attribute is Computed, so the plan must keep it as is.
func TestAttachmentPlanModifiersKeepAnOmittedList(t *testing.T) {
	t.Parallel()
	prior := listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("203.0.113.2"),
		"ipv6_address": types.StringNull(), "status": types.StringValue("active"),
	}})
	response := resourceplanmodifier.ListResponse{PlanValue: prior}
	preserveElasticIPState().PlanModifyList(context.Background(), resourceplanmodifier.ListRequest{
		ConfigValue: types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}),
		PlanValue:   prior, StateValue: prior,
	}, &response)
	if response.Diagnostics.HasError() || !response.PlanValue.Equal(prior) {
		t.Fatalf("expected the prior elastic IPs, got %s: %v", response.PlanValue, response.Diagnostics)
	}
}

func TestAttachmentDeletionDefaultsFollowOwnership(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	privateCreated := privateIPResourceObject(PrivateIPInputModel{Kind: types.StringValue("subnet")}, nil, &diags)
	privateExisting := privateIPResourceObject(PrivateIPInputModel{Kind: types.StringValue("ip")}, nil, &diags)
	elasticCreated := elasticIPResourceObject(ElasticIPInputModel{Kind: types.StringValue("new")}, nil, &diags)
	elasticExisting := elasticIPResourceObject(ElasticIPInputModel{Kind: types.StringValue("existing")}, nil, &diags)
	if diags.HasError() {
		t.Fatalf("build attachment states: %v", diags)
	}
	for name, testCase := range map[string]struct {
		value types.Object
		want  bool
	}{
		"private created":  {privateCreated, true},
		"private existing": {privateExisting, false},
		"elastic created":  {elasticCreated, true},
		"elastic existing": {elasticExisting, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := testCase.value.Attributes()["delete_on_termination"].(types.Bool)
			if !ok || got.IsNull() || got.IsUnknown() || got.ValueBool() != testCase.want {
				t.Fatalf("expected delete_on_termination=%v, got %v", testCase.want, got)
			}
		})
	}
}

func TestUpdatePrivateIPsRecreatesChangedSubnetAttachment(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		testUpdatePrivateIPsRecreatesChangedSubnetAttachment(t)
	})
}

func testUpdatePrivateIPsRecreatesChangedSubnetAttachment(t *testing.T) {
	t.Helper()
	newSubnetID := core.UUID{10}
	newPrivateIPID := core.UUID{11}
	newAddress := "10.0.1.5"
	var polls atomic.Int32
	var detached atomic.Bool
	var attached atomic.Bool
	var deleted atomic.Bool
	var deletionPolls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/detach-private-ip/":
			detached.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/network/private-ips/":
			var body networksdk.PrivateIPCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.SubnetId != newSubnetID {
				t.Errorf("unexpected private IP allocation: body=%#v err=%v", body, err)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, &networksdk.PrivateIPSchema{Id: newPrivateIPID})
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/attach-private-ip/":
			var body serversdk.ServerAttachPrivateIPSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.PrivateIpId != newPrivateIPID {
				t.Errorf("unexpected attachment identity: body=%#v err=%v", body, err)
			}
			attached.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodDelete && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
			// The backend only accepts the delete request, so the address is
			// still readable on the first poll. Reporting the update complete
			// here would let a subnet destroy in the same apply race a private
			// IP the backend has not removed yet.
			if deletionPolls.Add(1) > 1 {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			writeJSON(t, w, &networksdk.PrivateIPSchema{Id: testPrivateIPID})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			// The allocation gives the provider the exact ID, but the server
			// can still report that attachment before its address is assigned.
			privateIP := serversdk.NestedPrivateIPSchema{Id: newPrivateIPID}
			if polls.Add(1) > 1 {
				privateIP.IpAddress = &newAddress
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				PrivateIps: []serversdk.NestedPrivateIPSchema{privateIP},
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	})
	httpClient := stubHTTPClient(t, handler)
	client, err := serversdk.NewClient(
		"https://server.test",
		serversdk.WithHTTPClient(httpClient),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	networkClient, err := networksdk.NewClient(
		"https://server.test",
		networksdk.WithHTTPClient(httpClient),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	r := &ServerResource{client: client, networkClient: networkClient, projectID: core.UUID{12}}
	prior := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringValue(testPrivateIPID.String()),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("10.0.0.2"), "mac_address": types.StringNull(),
	}})
	planned := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringUnknown(),
		"subnet":                subnetReference(types.StringValue(newSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringUnknown(), "mac_address": types.StringUnknown(),
	}})
	start := time.Now()
	diags := r.updatePrivateIPs(context.Background(), testServerID, planned, prior)
	if diags.HasError() {
		t.Fatalf("update private IPs: %v", diags)
	}
	if !detached.Load() || !attached.Load() {
		t.Fatalf("expected changed subnet attachment to be recreated: detached=%v attached=%v", detached.Load(), attached.Load())
	}
	if !deleted.Load() {
		t.Fatal("expected the replaced provider-created private IP to be deleted instead of leaked")
	}
	if polls.Load() != 2 {
		t.Fatalf("expected the waiter to hold out for the new attachment address, got %d polls", polls.Load())
	}
	if deletionPolls.Load() != 2 {
		t.Fatalf("expected the cleanup to poll until the deleted private IP was gone, got %d polls", deletionPolls.Load())
	}
	if waited := time.Since(start); waited != serverPollInterval+privateIPDeleteInterval {
		t.Fatalf("expected one attachment and one deletion poll interval of waiting, waited %s", waited)
	}
}

func TestAttachNewPrivateIPRetainsAllocationIdentityAcrossFailures(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"busy then accepted", "rejected and cleaned", "rejected cleanup failed"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var creates, attaches, deletes atomic.Int32
				httpClient := stubHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					switch {
					case req.Method == http.MethodPost && req.URL.Path == "/v2/network/private-ips/":
						creates.Add(1)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusCreated)
						writeJSON(t, w, &networksdk.PrivateIPSchema{Id: testPrivateIPID})
					case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/attach-private-ip/"):
						var body serversdk.ServerAttachPrivateIPSchema
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.PrivateIpId != testPrivateIPID {
							t.Errorf("attach lost the allocation ID: %v %v", body, err)
						}
						attempt := attaches.Add(1)
						if scenario == "busy then accepted" {
							if attempt == 1 {
								http.Error(w, `{"errors":[{"code":"conflict","message":"There is another operation running on this server, retry again later"}]}`, http.StatusConflict)
							} else {
								w.WriteHeader(http.StatusAccepted)
							}
						} else {
							http.Error(w, `{"detail":"attachment rejected"}`, http.StatusBadRequest)
						}
					case req.Method == http.MethodDelete && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
						deletes.Add(1)
						if scenario == "rejected cleanup failed" {
							http.Error(w, `{"detail":"IP is attached"}`, http.StatusConflict)
						} else {
							w.WriteHeader(http.StatusNoContent)
						}
					case req.Method == http.MethodGet && req.URL.Path == "/v2/network/private-ips/"+testPrivateIPID.String()+"/":
						http.NotFound(w, req)
					default:
						t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
						http.NotFound(w, req)
					}
				}))
				client, err := serversdk.NewClient("https://server.test", serversdk.WithHTTPClient(httpClient), serversdk.WithRetry(core.NoRetry()))
				if err != nil {
					t.Fatal(err)
				}
				networkClient, err := networksdk.NewClient("https://server.test", networksdk.WithHTTPClient(httpClient), networksdk.WithRetry(core.NoRetry()))
				if err != nil {
					t.Fatal(err)
				}
				r := &ServerResource{client: client, networkClient: networkClient, projectID: core.UUID{12}}
				id, diags := r.attachNewPrivateIP(context.Background(), testServerID, testSubnetID)
				if id != testPrivateIPID || creates.Load() != 1 {
					t.Fatalf("allocation was lost or duplicated: id=%s creates=%d", id, creates.Load())
				}
				if scenario == "busy then accepted" {
					if diags.HasError() || attaches.Load() != 2 || deletes.Load() != 0 {
						t.Fatalf("unexpected busy recovery: attaches=%d deletes=%d diags=%v", attaches.Load(), deletes.Load(), diags)
					}
				} else if !diags.HasError() || attaches.Load() != 1 || deletes.Load() != 1 || !strings.Contains(diags[0].Detail(), testPrivateIPID.String()) {
					t.Fatalf("failed attach lost cleanup or recovery information: %v", diags)
				}
				if scenario == "rejected cleanup failed" && diags.ErrorsCount() != 2 {
					t.Fatalf("cleanup failure was not reported: %v", diags)
				}
			})
		})
	}
}

func TestUpdatePrivateIPsKeepsUserOwnedDetachedIP(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/detach-private-ip/":
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
			})
		default:
			// A DELETE would land here and fail the test, which is the point: an
			// attachment the practitioner owns must only be detached.
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	prior := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{
		privateIPValues(testPrivateIPID, true),
	})
	planned := types.ListValueMust(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}, []attr.Value{})
	if diags := r.updatePrivateIPs(context.Background(), testServerID, planned, prior); diags.HasError() {
		t.Fatalf("detach all private IPs: %v", diags)
	}
}

func TestUpdatePrivateIPsValidatesReplacementBeforeDetach(t *testing.T) {
	t.Parallel()
	prior := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringValue(testPrivateIPID.String()),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringNull(), "mac_address": types.StringNull(),
	}})
	planned := listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringUnknown(),
		"subnet":                subnetReference(types.StringValue(core.UUID{10}.String()), types.StringValue("10.0.1.0/24")),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringUnknown(), "mac_address": types.StringUnknown(),
	}})
	diags := (&ServerResource{}).updatePrivateIPs(context.Background(), testServerID, planned, prior)
	if !diags.HasError() {
		t.Fatal("expected invalid replacement subnet reference to fail before any API call")
	}
}

// The backend records a created elastic IP before allocating its address, and
// attaching one that early is what it refuses. The attach has to wait for the
// address to surface first.
func TestWaitUntilElasticIPAllocatedHoldsOutForTheAddress(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var polls atomic.Int32
		address := "203.0.113.10"
		networkClient, err := networksdk.NewClient(
			"https://network.test",
			networksdk.WithHTTPClient(stubHTTPClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Path != "/v2/network/elastic-ips/"+testElasticIPID.String()+"/" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					http.NotFound(w, req)
					return
				}
				elasticIP := &networksdk.ElasticIPDetailSchema{Id: testElasticIPID, EnableIpv4: true}
				if polls.Add(1) > 1 {
					elasticIP.IpAddress = &address
				}
				writeJSON(t, w, elasticIP)
			})),
			networksdk.WithRetry(core.NoRetry()),
		)
		if err != nil {
			t.Fatalf("create network client: %v", err)
		}
		r := &ServerResource{networkClient: networkClient, projectID: core.UUID{12}}

		start := time.Now()
		if err := r.waitUntilElasticIPAllocated(context.Background(), testElasticIPID, true, false); err != nil {
			t.Fatalf("wait for the elastic IP address: %v", err)
		}
		if polls.Load() != 2 {
			t.Fatalf("expected the waiter to hold out for the address, got %d polls", polls.Load())
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one production poll interval of waiting, waited %s", waited)
		}
	})
}

// A refusal the backend labels provider_error is worth replaying even without
// a 5xx status. That code is how a busy upstream reports a request it may well
// accept a moment later.
func TestAttachElasticIPReplaysARefusalTheBackendCodesAsTransient(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attaches atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			switch {
			case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/attach-eip/":
				if attaches.Add(1) > 1 {
					w.WriteHeader(http.StatusOK)
					return
				}
				writeProviderError(t, w)
			case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
				// The refusal did not hide a successful attach.
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
				})
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		if err := r.attachElasticIP(context.Background(), testServerID, testElasticIPID); err != nil {
			t.Fatalf("expected the refused attach to be replayed, got %v", err)
		}
		if attaches.Load() != 2 {
			t.Fatalf("expected two attach attempts, got %d", attaches.Load())
		}
		if waited := time.Since(start); waited != serverPollInterval {
			t.Fatalf("expected one production poll interval of backoff, waited %s", waited)
		}
	})
}

// A refusal that names a real problem must surface on the first attempt instead
// of being replayed until the deadline.
func TestAttachElasticIPReportsAPermanentRefusal(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attaches atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			if req.Method != http.MethodPost {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
				return
			}
			attaches.Add(1)
			http.Error(w, `{"errors":[{"code":"conflict","message":"Elastic IP is already attached"}]}`, http.StatusConflict)
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		start := time.Now()
		err := r.attachElasticIP(context.Background(), testServerID, testElasticIPID)
		if err == nil || attaches.Load() != 1 {
			t.Fatalf("expected one failed attempt, got %d attempts and %v", attaches.Load(), err)
		}
		if !errors.Is(err, serversdk.ErrConflict) {
			t.Fatalf("expected the backend rejection to be preserved, got %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("expected a permanent refusal to fail without backoff, waited %s", waited)
		}
	})
}

// A refusal that hid a successful attach counts as done: replaying it forever
// would fail an apply the backend already carried out.
func TestAttachElasticIPAcceptsARefusalThatHidASuccessfulAttach(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attaches atomic.Int32
		client := stubServerClient(t, func(w http.ResponseWriter, req *http.Request) {
			switch req.Method {
			case http.MethodPost:
				attaches.Add(1)
				writeProviderError(t, w)
			case http.MethodGet:
				address := "203.0.113.10"
				writeJSON(t, w, &serversdk.ServerDetailSchema{
					Id: testServerID, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive,
					ElasticIps: []serversdk.NestedElasticIPSchema{{Id: testElasticIPID, IpAddress: &address}},
				})
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
				http.NotFound(w, req)
			}
		})
		r := &ServerResource{client: client, projectID: core.UUID{12}}

		if err := r.attachElasticIP(context.Background(), testServerID, testElasticIPID); err != nil {
			t.Fatalf("expected an already attached elastic IP to count as attached, got %v", err)
		}
		if attaches.Load() != 1 {
			t.Fatalf("expected the attach not to be replayed once it landed, got %d attempts", attaches.Load())
		}
	})
}

// --- ValidateConfig tests ---

func TestServerValidateConfigRejectsCommonMistakes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		config  func(*ServerResourceModel)
		summary string
	}{
		{
			name: "predefined flavor without a name",
			config: func(config *ServerResourceModel) {
				config.Flavor = predefinedFlavorConfig(t, types.StringNull())
			},
			summary: "Missing flavor.name",
		},
		{
			name: "predefined flavor with custom capacity",
			config: func(config *ServerResourceModel) {
				config.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
					"kind": types.StringValue("predefined"), "name": types.StringValue("s2.small"),
					"family": types.StringNull(), "vcpus": types.Int64Value(4), "ram": types.Int64Null(),
				})
			},
			summary: "Unsupported flavor.vcpus",
		},
		{
			name: "custom flavor without capacity",
			config: func(config *ServerResourceModel) {
				config.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
					"kind": types.StringValue("custom"), "name": types.StringNull(),
					"family": types.StringValue("basic"), "vcpus": types.Int64Null(), "ram": types.Int64Value(4),
				})
			},
			summary: "Missing flavor.vcpus",
		},
		{
			name: "custom flavor with an unknown family",
			config: func(config *ServerResourceModel) {
				config.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
					"kind": types.StringValue("custom"), "name": types.StringNull(),
					"family": types.StringValue("balanced"), "vcpus": types.Int64Value(2), "ram": types.Int64Value(2),
				})
			},
			summary: "Invalid custom flavor family",
		},
		{
			name: "custom flavor with zero RAM",
			config: func(config *ServerResourceModel) {
				config.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
					"kind": types.StringValue("custom"), "name": types.StringNull(),
					"family": types.StringValue("basic"), "vcpus": types.Int64Value(2), "ram": types.Int64Value(0),
				})
			},
			summary: "Invalid flavor.ram",
		},
		{
			name: "image boot without a volume type",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
					"volume_size": types.Int64Value(30),
				})
			},
			summary: "Missing boot.volume_type",
		},
		{
			name: "image boot without a volume size",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
				})
			},
			summary: "Missing boot.volume_size",
		},
		{
			name: "image boot with a whitespace-only image name",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("   ")),
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
				})
			},
			summary: "Empty selector criterion",
		},
		{
			name: "image boot that also references an existing volume",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringValue("ubuntu")),
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
					"volume": idReference(types.StringValue(core.UUID{9}.String())),
				})
			},
			summary: "Unsupported boot.volume",
		},
		{
			name: "custom image boot without a custom image",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type":   types.StringValue("custom_image"),
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
				})
			},
			summary: "Missing boot.custom_image",
		},
		{
			name: "custom image boot configured with an image name",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("custom_image"), "image": testImageSelector(types.StringValue("ubuntu")),
					"custom_image": idReference(types.StringValue(testImageID.String())),
					"volume_type":  testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
				})
			},
			summary: "Unsupported boot.image",
		},
		{
			name: "local disk boot with a volume size",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("local_disk"), "image": testImageSelector(types.StringValue("ubuntu")),
					"volume_size": types.Int64Value(30),
				})
			},
			summary: "Unsupported boot.volume_size",
		},
		{
			name: "local disk boot that sets delete_on_termination",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{
					"boot_type": types.StringValue("local_disk"), "image": testImageSelector(types.StringValue("ubuntu")),
					"delete_on_termination": types.BoolValue(true),
				})
			},
			summary: "Unsupported boot.delete_on_termination",
		},
		{
			name: "existing volume boot without a volume ID",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("volume")})
			},
			summary: "Missing boot.volume",
		},
		{
			name: "unsupported boot type",
			config: func(config *ServerResourceModel) {
				config.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("snapshot")})
			},
			summary: "Invalid boot type",
		},
		{
			name: "subnet private IP with both subnet references",
			config: func(config *ServerResourceModel) {
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("subnet"), "id": types.StringNull(),
					"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringValue("10.0.1.0/24")),
					"delete_on_termination": types.BoolValue(true),
					"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
				}})
			},
			summary: "Invalid subnet reference",
		},
		{
			name: "subnet private IP without any subnet reference",
			config: func(config *ServerResourceModel) {
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("subnet"), "id": types.StringNull(),
					"subnet":                subnetReference(types.StringNull(), types.StringNull()),
					"delete_on_termination": types.BoolValue(true),
					"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
				}})
			},
			summary: "Missing private_ips[0].subnet",
		},
		{
			name: "subnet private IP with an empty subnet object",
			config: func(config *ServerResourceModel) {
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("subnet"), "id": types.StringNull(),
					"subnet":                reference.Of(map[string]types.String{reference.ID: types.StringNull(), subnetCIDR: types.StringNull()}),
					"delete_on_termination": types.BoolValue(true),
					"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
				}})
			},
			summary: "Invalid subnet reference",
		},
		{
			name: "existing private IP with a subnet",
			config: func(config *ServerResourceModel) {
				values := privateIPValues(testPrivateIPID, false)
				values["subnet"] = subnetReference(types.StringNull(), types.StringValue("10.0.1.0/24"))
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{values})
			},
			summary: "Unsupported private_ips[0].subnet",
		},
		{
			name: "existing private IP without an ID",
			config: func(config *ServerResourceModel) {
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("ip"), "id": types.StringNull(),
					"subnet":                subnetReference(types.StringNull(), types.StringNull()),
					"delete_on_termination": types.BoolValue(false),
					"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
				}})
			},
			summary: "Missing private_ips[0].id",
		},
		{
			name: "the same private IP attached twice",
			config: func(config *ServerResourceModel) {
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{
					privateIPValues(testPrivateIPID, false), privateIPValues(testPrivateIPID, false),
				})
			},
			summary: "Duplicate private_ips entry",
		},
		{
			name: "new elastic IP with both address families disabled",
			config: func(config *ServerResourceModel) {
				config.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("new"), "id": types.StringNull(),
					"enable_ipv4": types.BoolValue(false), "enable_ipv6": types.BoolValue(false),
					"delete_on_termination": types.BoolValue(true), "ip_address": types.StringNull(),
					"ipv6_address": types.StringNull(), "status": types.StringNull(),
				}})
			},
			summary: "Invalid new elastic IP",
		},
		{
			name: "existing elastic IP configured with an address family",
			config: func(config *ServerResourceModel) {
				config.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringValue("existing"), "id": types.StringValue(testElasticIPID.String()),
					"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolNull(),
					"delete_on_termination": types.BoolValue(false), "ip_address": types.StringNull(),
					"ipv6_address": types.StringNull(), "status": types.StringNull(),
				}})
			},
			summary: "Unsupported elastic_ips[0].enable_ipv4",
		},
		{
			name: "private IP without a kind",
			config: func(config *ServerResourceModel) {
				values := privateIPValues(testPrivateIPID, false)
				values["kind"] = types.StringNull()
				config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{values})
			},
			summary: "Missing private_ips[0].kind",
		},
		{
			name: "elastic IP without a kind",
			config: func(config *ServerResourceModel) {
				config.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
					"kind": types.StringNull(), "id": types.StringValue(testElasticIPID.String()),
					"enable_ipv4": types.BoolNull(), "enable_ipv6": types.BoolNull(),
					"delete_on_termination": types.BoolNull(), "ip_address": types.StringNull(),
					"ipv6_address": types.StringNull(), "status": types.StringNull(),
				}})
			},
			summary: "Missing elastic_ips[0].kind",
		},
		{
			name: "data volume without a size",
			config: func(config *ServerResourceModel) {
				config.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(0), "iops": types.Int64Null(),
				}})
			},
			summary: "Invalid data_volumes[0].volume_size",
		},
		{
			name: "several servers in one resource",
			config: func(config *ServerResourceModel) {
				config.Quantity = types.Int64Value(3)
			},
			summary: "Unsupported server quantity",
		},
		{
			name: "unsupported power state",
			config: func(config *ServerResourceModel) {
				config.PowerState = types.StringValue("paused")
			},
			summary: "Invalid server power state",
		},
		{
			name: "invalid key_pair.id UUID",
			config: func(config *ServerResourceModel) {
				config.KeyPair = idReference(types.StringValue("invalid-uuid"))
			},
			summary: "Invalid key_pair.id",
		},
		{
			name: "invalid placement_group.id UUID",
			config: func(config *ServerResourceModel) {
				config.PlacementGroup = idReference(types.StringValue("invalid-uuid"))
			},
			summary: "Invalid placement_group.id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := validServerConfig(t)
			test.config(&config)
			diags := validateServerConfig(t, config)
			if !diags.HasError() {
				t.Fatalf("expected %q, got no diagnostics", test.summary)
			}
			for _, err := range diags.Errors() {
				if err.Summary() == test.summary {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.summary, diags)
		})
	}
}

func TestServerValidateConfigAcceptsSupportedConfigurations(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*ServerResourceModel){
		"image boot with every attachment type": func(*ServerResourceModel) {},
		"predefined flavor": func(config *ServerResourceModel) {
			config.Flavor = predefinedFlavorConfig(t, types.StringValue("s2.small"))
		},
		"local disk boot": func(config *ServerResourceModel) {
			config.Boot = bootConfig(t, map[string]attr.Value{
				"boot_type": types.StringValue("local_disk"), "image": testImageSelector(types.StringValue("ubuntu")),
			})
		},
		"existing volume boot": func(config *ServerResourceModel) {
			config.Boot = bootConfig(t, map[string]attr.Value{
				"boot_type": types.StringValue("volume"), "volume": idReference(types.StringValue(testVolumeTypeID.String())),
			})
		},
		"existing volume boot that opts into deletion": func(config *ServerResourceModel) {
			config.Boot = bootConfig(t, map[string]attr.Value{
				"boot_type": types.StringValue("volume"), "volume": idReference(types.StringValue(testVolumeTypeID.String())),
				"delete_on_termination": types.BoolValue(true),
			})
		},
		"values that are only known after apply": func(config *ServerResourceModel) {
			config.Boot = bootConfig(t, map[string]attr.Value{
				"boot_type": types.StringValue("image"), "image": testImageSelector(types.StringUnknown()),
				"volume_type": testVolumeTypeSelector(types.StringUnknown()), "volume_size": types.Int64Unknown(),
			})
			config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
				"kind": types.StringValue("subnet"), "id": types.StringNull(),
				"subnet":                subnetReference(types.StringUnknown(), types.StringNull()),
				"delete_on_termination": types.BoolValue(true),
				"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
			}})
		},
		"an unknown attachment list": func(config *ServerResourceModel) {
			config.ElasticIPs = types.ListUnknown(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()})
		},
		"an empty attachment list that detaches everything": func(config *ServerResourceModel) {
			config.PrivateIPs = types.ListValueMust(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}, []attr.Value{})
		},
		"valid key_pair and placement_group": func(config *ServerResourceModel) {
			config.KeyPair = idReference(types.StringValue("29ffcb10-44f2-459b-8ca8-d4f9773bf58c"))
			config.PlacementGroup = idReference(types.StringValue("897d541a-c877-4f1b-bb85-e813bf25e922"))
		},
		"unknown key_pair and placement_group": func(config *ServerResourceModel) {
			config.KeyPair = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
			config.PlacementGroup = idReference(types.StringUnknown())
		},
	}

	for name, apply := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := validServerConfig(t)
			apply(&config)
			if diags := validateServerConfig(t, config); diags.HasError() {
				t.Fatalf("expected a valid configuration, got %v", diags)
			}
		})
	}
}

func TestServerUpdateSecurityGroups(t *testing.T) {
	t.Parallel()
	var updated atomic.Bool
	sgID := core.UUID{13}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPut && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/security-groups/":
			var body serversdk.ServerUpdateSecurityGroupSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode sg body: %v", err)
			}
			if len(body.SecurityGroups) != 1 || body.SecurityGroups[0].Id != sgID {
				t.Errorf("unexpected sg body: %#v", body)
			}
			updated.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			var sgs []serversdk.NestedSecurityGroupSchema
			if updated.Load() {
				sgs = []serversdk.NestedSecurityGroupSchema{{Id: sgID, Name: "web-sg"}}
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:             testServerID,
				Name:           "server-sg",
				Flavor:         serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:          &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:           serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState:     serversdk.ServerPowerStateRunning,
				Status:         serversdk.ServerStatusActive,
				SecurityGroups: sgs,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-sg")
	stateModel.SecurityGroups = types.SetNull(reference.ObjectType(reference.ID))

	planModel := stateModel
	planModel.SecurityGroups = reference.Set(reference.ID, []string{sgID.String()})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !updated.Load() {
		t.Fatal("expected security group update API to be called")
	}
}

func TestServerUpdateExplicitPowerStateStart(t *testing.T) {
	t.Parallel()
	var started atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/start/":
			started.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			powerState, status := serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			if started.Load() {
				powerState, status = serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-start",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-start")
	stateModel.PowerState = types.StringValue("shutdown")

	planModel := stateModel
	planModel.PowerState = types.StringValue("running")

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !started.Load() {
		t.Fatal("expected server start API to be called")
	}
}

func TestServerUpdateExplicitPowerStateStop(t *testing.T) {
	t.Parallel()
	var stopped atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/stop/":
			stopped.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			powerState, status := serversdk.ServerPowerStateRunning, serversdk.ServerStatusActive
			if stopped.Load() {
				powerState, status = serversdk.ServerPowerStateShutdown, serversdk.ServerStatusShutoff
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-stop",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: powerState,
				Status:     status,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-stop")
	stateModel.PowerState = types.StringValue("running")

	planModel := stateModel
	planModel.PowerState = types.StringValue("shutdown")

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !stopped.Load() {
		t.Fatal("expected server stop API to be called")
	}
}

func TestServerUpdateBandwidthSuccess(t *testing.T) {
	t.Parallel()
	var updatedBandwidth atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPut && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/bandwidth/":
			var body serversdk.ServerUpdateBandwidthSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode bandwidth body: %v", err)
			}
			if body.Bandwidth != nil {
				updatedBandwidth.Store(int64(*body.Bandwidth))
			}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			bw := int(updatedBandwidth.Load())
			if bw == 0 {
				bw = 100
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-bw",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning,
				Status:     serversdk.ServerStatusActive,
				Bandwidth:  &bw,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-bw")
	stateModel.Bandwidth = types.Int64Value(100)

	planModel := stateModel
	planModel.Bandwidth = types.Int64Value(200)

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if updatedBandwidth.Load() != 200 {
		t.Fatalf("expected bandwidth update to 200, got %d", updatedBandwidth.Load())
	}
}

func TestServerUpdateAttachAndDetachExistingElasticIP(t *testing.T) {
	t.Parallel()
	var detached atomic.Bool
	var attached atomic.Bool
	eip1 := core.UUID{8}
	eip2 := core.UUID{9}
	publicIP := "203.0.113.50"

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/detach-eip/":
			var body serversdk.ServerElasticIPAttachSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode detach eip body: %v", err)
			}
			if body.EipId == eip1 {
				detached.Store(true)
			}
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/attach-eip/":
			var body serversdk.ServerElasticIPAttachSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode attach eip body: %v", err)
			}
			if body.EipId == eip2 {
				attached.Store(true)
			}
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			var eips []serversdk.NestedElasticIPSchema
			if attached.Load() && detached.Load() {
				eips = []serversdk.NestedElasticIPSchema{{Id: eip2, IpAddress: &publicIP}}
			} else {
				eips = []serversdk.NestedElasticIPSchema{{Id: eip1, IpAddress: &publicIP}}
			}
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:         testServerID,
				Name:       "server-eip",
				Flavor:     serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4},
				Image:      &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"},
				Zone:       serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
				PowerState: serversdk.ServerPowerStateRunning,
				Status:     serversdk.ServerStatusActive,
				ElasticIps: eips,
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}

	stateModel := localDiskServerModel(t, "server-eip")
	stateModel.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{elasticIPValues(eip1, false)})

	planModel := stateModel
	planModel.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{elasticIPValues(eip2, false)})

	resourceSchema := serverSchema(t)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: resourceSchema}}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  planFor(t, resourceSchema, planModel),
		State: stateFor(t, resourceSchema, stateModel),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if !detached.Load() || !attached.Load() {
		t.Fatalf("expected detach and attach calls: detached=%v attached=%v", detached.Load(), attached.Load())
	}
}

func TestServerHelpersAndPlanModifiersCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1. emptyServerResponse
	emptyMsg := emptyServerResponse(testServerID)
	if !strings.Contains(emptyMsg, testServerID.String()) {
		t.Errorf("unexpected empty server response message: %q", emptyMsg)
	}

	// 2. deletionErrorDetail
	errBase := errors.New("delete failed")
	if got := deletionErrorDetail(errBase, nil); got != "delete failed" {
		t.Errorf("unexpected deletion error detail without stop err: %q", got)
	}
	stopErr := errors.New("stop failed")
	if got := deletionErrorDetail(errBase, stopErr); !strings.Contains(got, "stop failed") {
		t.Errorf("unexpected deletion error detail with stop err: %q", got)
	}

	// 3. serverIsShutdown
	if !serverIsShutdown(&serversdk.ServerDetailSchema{PowerState: serversdk.ServerPowerStateShutdown}) {
		t.Error("expected serverIsShutdown=true for PowerStateShutdown")
	}
	if !serverIsShutdown(&serversdk.ServerDetailSchema{Status: serversdk.ServerStatusShutoff}) {
		t.Error("expected serverIsShutdown=true for StatusShutoff")
	}
	if !serverIsShutdown(&serversdk.ServerDetailSchema{Status: serversdk.ServerStatusStopped}) {
		t.Error("expected serverIsShutdown=true for StatusStopped")
	}
	if serverIsShutdown(&serversdk.ServerDetailSchema{PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive}) {
		t.Error("expected serverIsShutdown=false for running server")
	}

	// 4. isTransientEnumError
	if isTransientEnumError(errors.New("plain error")) {
		t.Error("expected isTransientEnumError=false for plain error")
	}

	// 5. Plan modifier descriptions
	var priv privateIPStatePlanModifier
	if priv.Description(ctx) == "" || priv.MarkdownDescription(ctx) == "" {
		t.Error("expected non-empty descriptions for privateIPStatePlanModifier")
	}
	var el elasticIPStatePlanModifier
	if el.Description(ctx) == "" || el.MarkdownDescription(ctx) == "" {
		t.Error("expected non-empty descriptions for elasticIPStatePlanModifier")
	}

	// 6. validateDesiredPowerState
	if diags := validateDesiredPowerState(types.StringValue("running")); diags.HasError() {
		t.Errorf("unexpected diags for running: %v", diags)
	}
	if diags := validateDesiredPowerState(types.StringValue("shutdown")); diags.HasError() {
		t.Errorf("unexpected diags for shutdown: %v", diags)
	}
	if diags := validateDesiredPowerState(types.StringValue("paused")); !diags.HasError() {
		t.Error("expected error diags for paused")
	}
	if diags := validateDesiredPowerState(types.StringNull()); diags.HasError() {
		t.Errorf("unexpected diags for null: %v", diags)
	}
	if diags := validateDesiredPowerState(types.StringUnknown()); diags.HasError() {
		t.Errorf("unexpected diags for unknown: %v", diags)
	}

	// 7. powerStateIsRunning and powerStateIsShutdown
	if !powerStateIsRunning(types.StringNull()) || !powerStateIsRunning(types.StringUnknown()) {
		t.Error("expected powerStateIsRunning=true for null/unknown")
	}
	if !powerStateIsRunning(types.StringValue("running")) || powerStateIsRunning(types.StringValue("shutdown")) {
		t.Error("unexpected powerStateIsRunning evaluation")
	}
	if powerStateIsShutdown(types.StringNull()) || powerStateIsShutdown(types.StringUnknown()) {
		t.Error("expected powerStateIsShutdown=false for null/unknown")
	}
	if !powerStateIsShutdown(types.StringValue("shutdown")) || !powerStateIsShutdown(types.StringValue("shutoff")) || !powerStateIsShutdown(types.StringValue("stopped")) {
		t.Error("expected powerStateIsShutdown=true for shutdown/shutoff/stopped")
	}
	if powerStateIsShutdown(types.StringValue("running")) {
		t.Error("expected powerStateIsShutdown=false for running")
	}
}

func TestServerUpdateLifecycleOperationErrors(t *testing.T) {
	t.Parallel()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && (strings.Contains(req.URL.Path, "/start/") || strings.Contains(req.URL.Path, "/stop/") || strings.Contains(req.URL.Path, "/resize/")):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"code":"server_error","message":"backend failure"}]}`))
		case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/security-groups/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"code":"server_error","message":"backend failure"}]}`))
		case req.Method == http.MethodGet:
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id: testServerID, PowerState: serversdk.ServerPowerStateShutdown, Status: serversdk.ServerStatusShutoff,
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	var flavor serversdk.ServerFlavor
	_ = flavor.FromCustomServerFlavor(serversdk.CustomServerFlavor{Family: serversdk.FlavorFamilyBasic, Vcpus: 2, Ram: 4})

	// 1. startAfterOfflineUpdate failure
	var diags diag.Diagnostics
	r.startAfterOfflineUpdate(context.Background(), testServerID, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Error starting server after offline update" {
		t.Fatalf("expected Error starting server after offline update, got %v", diags)
	}

	// 2. tryLiveResize failure
	needsShutdown, liveDiags := r.tryLiveResize(context.Background(), testServerID, serversdk.ServerResizeSchema{Flavor: flavor})
	if needsShutdown || !liveDiags.HasError() || liveDiags.Errors()[0].Summary() != "Error resizing server" {
		t.Fatalf("expected Error resizing server, got needsShutdown=%v diags=%v", needsShutdown, liveDiags)
	}

	// 3. resizeOfflineServer failure
	offlineDiags := r.resizeOfflineServer(context.Background(), testServerID, serversdk.ServerResizeSchema{Flavor: flavor})
	if !offlineDiags.HasError() || offlineDiags.Errors()[0].Summary() != "Error resizing stopped server" {
		t.Fatalf("expected Error resizing stopped server, got %v", offlineDiags)
	}

	// 4. updateSecurityGroups failure
	sgDiags := r.updateSecurityGroups(context.Background(), testServerID, serversdk.ServerUpdateSecurityGroupSchema{
		SecurityGroups: []serversdk.ServerSecurityGroup{{Id: core.UUID{13}}},
	})
	if !sgDiags.HasError() || sgDiags.Errors()[0].Summary() != "Error updating server security groups" {
		t.Fatalf("expected Error updating server security groups, got %v", sgDiags)
	}

	// 5. updatePowerState start failure
	startDiags := r.updatePowerState(context.Background(), testServerID, types.StringValue("running"))
	if !startDiags.HasError() || startDiags.Errors()[0].Summary() != "Error starting server" {
		t.Fatalf("expected Error starting server, got %v", startDiags)
	}

	// 6. updatePowerState stop failure
	stopDiags := r.updatePowerState(context.Background(), testServerID, types.StringValue("shutdown"))
	if !stopDiags.HasError() || stopDiags.Errors()[0].Summary() != "Error stopping server" {
		t.Fatalf("expected Error stopping server, got %v", stopDiags)
	}
}

func TestServerFlavorMatchesPredefinedAndUnsupported(t *testing.T) {
	t.Parallel()
	var expectedPredefined serversdk.ServerFlavor
	if err := expectedPredefined.FromPredefinedServerFlavor(serversdk.PredefinedServerFlavor{Id: testFlavorID}); err != nil {
		t.Fatalf("build predefined flavor: %v", err)
	}
	matches, err := serverFlavorMatches(serversdk.NestedFlavorSchema{Id: testFlavorID}, expectedPredefined)
	if err != nil || !matches {
		t.Fatalf("expected predefined flavor to match, matches=%v err=%v", matches, err)
	}
	otherID := core.UUID{99}
	matches, err = serverFlavorMatches(serversdk.NestedFlavorSchema{Id: otherID}, expectedPredefined)
	if err != nil || matches {
		t.Fatalf("expected predefined flavor to not match, matches=%v err=%v", matches, err)
	}

	var expectedCustom serversdk.ServerFlavor
	if err := expectedCustom.FromCustomServerFlavor(serversdk.CustomServerFlavor{Family: serversdk.FlavorFamilyBasic, Vcpus: 4, Ram: 8}); err != nil {
		t.Fatalf("build custom flavor: %v", err)
	}
	matches, err = serverFlavorMatches(serversdk.NestedFlavorSchema{Vcpus: 2, Ram: 8}, expectedCustom)
	if err != nil || matches {
		t.Fatalf("expected custom flavor with mismatched vcpus to not match, matches=%v err=%v", matches, err)
	}

	var emptyFlavor serversdk.ServerFlavor
	_, err = serverFlavorMatches(serversdk.NestedFlavorSchema{Id: testFlavorID}, emptyFlavor)
	if err == nil {
		t.Fatal("expected error for empty flavor discriminator")
	}
}

func TestServerStopBeforeDeletion(t *testing.T) {
	t.Parallel()

	// 1. serverIsShutdown(server) == true -> returns nil immediately without calling API
	r := &ServerResource{}
	if err := r.stopBeforeDeletion(context.Background(), testServerID, &serversdk.ServerDetailSchema{
		PowerState: serversdk.ServerPowerStateShutdown,
	}); err != nil {
		t.Fatalf("expected nil error when server is already shutdown, got: %v", err)
	}

	// 2. StopServer returns ErrNotFound -> returns nil
	apiNotFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer apiNotFound.Close()
	clientNotFound, err := serversdk.NewClient(apiNotFound.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	r = &ServerResource{client: clientNotFound, projectID: core.UUID{12}}
	if err := r.stopBeforeDeletion(context.Background(), testServerID, &serversdk.ServerDetailSchema{
		PowerState: serversdk.ServerPowerStateRunning,
	}); err != nil {
		t.Fatalf("expected nil when StopServer returns not found, got: %v", err)
	}

	// 3. StopServer returns an error
	apiErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer apiErr.Close()
	clientErr, _ := serversdk.NewClient(apiErr.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientErr, projectID: core.UUID{12}}
	if err := r.stopBeforeDeletion(context.Background(), testServerID, &serversdk.ServerDetailSchema{
		PowerState: serversdk.ServerPowerStateRunning,
	}); err == nil || !strings.Contains(err.Error(), "stop server") {
		t.Fatalf("expected stop server error, got: %v", err)
	}
}

func TestServerWaitUntilRunning(t *testing.T) {
	t.Parallel()

	// 1. Server status deleted
	apiDeleted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.ServerDetailSchema{
			Id:         testServerID,
			PowerState: serversdk.ServerPowerStateShutdown,
			Status:     serversdk.ServerStatusDeleted,
		})
	}))
	defer apiDeleted.Close()
	clientDeleted, _ := serversdk.NewClient(apiDeleted.URL, serversdk.WithRetry(core.NoRetry()))
	r := &ServerResource{client: clientDeleted, projectID: core.UUID{12}}
	err := r.waitUntilRunning(context.Background(), testServerID)
	if err == nil || !strings.Contains(err.Error(), "was deleted while waiting to start") {
		t.Fatalf("expected deleted error, got: %v", err)
	}

	// 2. Server reaches running
	apiRunning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.ServerDetailSchema{
			Id:         testServerID,
			PowerState: serversdk.ServerPowerStateRunning,
			Status:     serversdk.ServerStatusActive,
		})
	}))
	defer apiRunning.Close()
	clientRunning, _ := serversdk.NewClient(apiRunning.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientRunning, projectID: core.UUID{12}}
	if err := r.waitUntilRunning(context.Background(), testServerID); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestServerRegionID(t *testing.T) {
	t.Parallel()
	regionID := core.UUID{42}
	projectID := core.UUID{12}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{
				Id:   testServerID,
				Zone: serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/projects/"+projectID.String()+"/zones/":
			writeJSON(t, w, &projectsdk.PagedProjectZoneSchema{
				Count: 1,
				Results: []projectsdk.ProjectZoneSchema{{
					Id:     testZoneID,
					Name:   "zone-a",
					Region: projectsdk.NestedRegionSchema{Id: regionID, Name: "region-1"},
				}},
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	serverClient, _ := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	projClient, _ := projectsdk.NewClient(api.URL, projectsdk.WithRetry(core.NoRetry()))
	r := &ServerResource{client: serverClient, projectClient: projClient, projectID: projectID}

	gotRegionID, diags := r.serverRegionID(context.Background(), testServerID)
	if diags.HasError() || gotRegionID != regionID {
		t.Fatalf("expected regionID %s, got %s (diags: %v)", regionID, gotRegionID, diags)
	}

	// Server get error
	rErr := &ServerResource{client: serverClient, projectClient: projClient, projectID: projectID}
	_, errDiags := rErr.serverRegionID(context.Background(), core.UUID{99})
	if !errDiags.HasError() || errDiags.Errors()[0].Summary() != "Error reading server zone" {
		t.Fatalf("expected Error reading server zone, got: %v", errDiags)
	}
}

func TestServerBuildSecurityGroups(t *testing.T) {
	t.Parallel()
	// 1. Null set
	var body serversdk.ServerCreateSchema
	var diags diag.Diagnostics
	buildSecurityGroups(types.SetNull(reference.ObjectType(reference.ID)), &body, &diags)
	if diags.HasError() || body.SecurityGroups != nil {
		t.Fatalf("expected nil SecurityGroups for null set, got %#v, diags=%v", body.SecurityGroups, diags)
	}

	// 2. Unknown set
	buildSecurityGroups(types.SetUnknown(reference.ObjectType(reference.ID)), &body, &diags)
	if diags.HasError() || body.SecurityGroups != nil {
		t.Fatalf("expected nil SecurityGroups for unknown set, got %#v, diags=%v", body.SecurityGroups, diags)
	}

	// 3. Invalid UUID in set
	buildSecurityGroups(reference.Set(reference.ID, []string{"not-a-uuid"}), &body, &diags)
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "security_groups[0].id") {
		t.Fatalf("expected error diagnostic naming security_groups[0].id, got %v", diags)
	}

	// 4. Valid UUIDs in set
	diags = diag.Diagnostics{}
	validUUID := core.UUID{13}
	buildSecurityGroups(reference.Set(reference.ID, []string{validUUID.String()}), &body, &diags)
	if diags.HasError() || body.SecurityGroups == nil || len(*body.SecurityGroups) != 1 || (*body.SecurityGroups)[0].Id != validUUID {
		t.Fatalf("expected security group ID %s, got %#v (diags: %v)", validUUID, body.SecurityGroups, diags)
	}
}

func TestServerInitializeImportedFlavorState(t *testing.T) {
	t.Parallel()
	projectID := core.UUID{12}

	// 1. server == nil
	r := &ServerResource{projectID: projectID}
	var state ServerResourceModel
	state.Flavor = types.ObjectNull(flavorAttributeTypes())
	if diags := r.initializeImportedFlavorState(context.Background(), nil, &state); diags.HasError() {
		t.Fatalf("expected nil diags for nil server, got: %v", diags)
	}

	// 2. state.Flavor already set
	state.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(2), "ram": types.Int64Value(4), "name": types.StringNull(),
	})
	if diags := r.initializeImportedFlavorState(context.Background(), &serversdk.ServerDetailSchema{
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID},
	}, &state); diags.HasError() {
		t.Fatalf("expected nil diags when flavor is already set, got: %v", diags)
	}

	// 3. Find returns >1 candidate (ambiguous)
	apiAmbiguous := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.PagedFlavorSchema{
			Count: 2,
			Results: []serversdk.FlavorSchema{
				{Id: testFlavorID, Name: "flavor-1"},
				{Id: testFlavorID, Name: "flavor-2"},
			},
		})
	}))
	defer apiAmbiguous.Close()
	clientAmbiguous, _ := serversdk.NewClient(apiAmbiguous.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientAmbiguous, projectID: projectID}
	state.Flavor = types.ObjectNull(flavorAttributeTypes())
	diags := r.initializeImportedFlavorState(context.Background(), &serversdk.ServerDetailSchema{
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Vcpus: 2, Ram: 4},
	}, &state)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Ambiguous imported server flavor" {
		t.Fatalf("expected Ambiguous imported server flavor error, got: %v", diags)
	}

	// 4. Find returns 0 candidates (fallback to custom)
	apiEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.PagedFlavorSchema{Count: 0, Results: []serversdk.FlavorSchema{}})
	}))
	defer apiEmpty.Close()
	clientEmpty, _ := serversdk.NewClient(apiEmpty.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientEmpty, projectID: projectID}
	state.Flavor = types.ObjectNull(flavorAttributeTypes())
	diags = r.initializeImportedFlavorState(context.Background(), &serversdk.ServerDetailSchema{
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Vcpus: 4, Ram: 8},
	}, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	var flavorModel FlavorInputModel
	_ = state.Flavor.As(context.Background(), &flavorModel, basetypes.ObjectAsOptions{})
	if flavorModel.Kind.ValueString() != "custom" || flavorModel.VCPUs.ValueInt64() != 4 || flavorModel.RAM.ValueInt64() != 8 {
		t.Fatalf("expected custom flavor with 4 vcpus, 8 ram, got: %#v", flavorModel)
	}

	// 5. Find returns 1 candidate (predefined)
	apiSingle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(t, w, &serversdk.PagedFlavorSchema{
			Count:   1,
			Results: []serversdk.FlavorSchema{{Id: testFlavorID, Name: "s2.large"}},
		})
	}))
	defer apiSingle.Close()
	clientSingle, _ := serversdk.NewClient(apiSingle.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientSingle, projectID: projectID}
	state.Flavor = types.ObjectNull(flavorAttributeTypes())
	diags = r.initializeImportedFlavorState(context.Background(), &serversdk.ServerDetailSchema{
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Vcpus: 4, Ram: 8},
	}, &state)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	_ = state.Flavor.As(context.Background(), &flavorModel, basetypes.ObjectAsOptions{})
	if flavorModel.Kind.ValueString() != "predefined" || flavorModel.Name.ValueString() != "s2.large" {
		t.Fatalf("expected predefined flavor s2.large, got: %#v", flavorModel)
	}

	// 6. Find returns error
	apiErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer apiErr.Close()
	clientErr, _ := serversdk.NewClient(apiErr.URL, serversdk.WithRetry(core.NoRetry()))
	r = &ServerResource{client: clientErr, projectID: projectID}
	state.Flavor = types.ObjectNull(flavorAttributeTypes())
	diags = r.initializeImportedFlavorState(context.Background(), &serversdk.ServerDetailSchema{
		Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Vcpus: 4, Ram: 8},
	}, &state)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Error identifying imported server flavor" {
		t.Fatalf("expected Error identifying imported server flavor, got: %v", diags)
	}
}

func TestServerPreserveUnconfiguredAttachmentList(t *testing.T) {
	t.Parallel()
	req := resourceplanmodifier.ListRequest{
		PlanValue: types.ListNull(types.StringType),
	}
	var resp resourceplanmodifier.ListResponse
	if preserveUnconfiguredAttachmentList(req, &resp) {
		t.Error("expected false when PlanValue is not unknown")
	}

	req = resourceplanmodifier.ListRequest{
		PlanValue:   types.ListUnknown(types.StringType),
		ConfigValue: types.ListUnknown(types.StringType),
	}
	resp = resourceplanmodifier.ListResponse{}
	if !preserveUnconfiguredAttachmentList(req, &resp) {
		t.Error("expected true when ConfigValue is unknown")
	}

	stateVal := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("test")})
	req = resourceplanmodifier.ListRequest{
		PlanValue:   types.ListUnknown(types.StringType),
		ConfigValue: types.ListNull(types.StringType),
		StateValue:  stateVal,
	}
	resp = resourceplanmodifier.ListResponse{}
	if !preserveUnconfiguredAttachmentList(req, &resp) || !resp.PlanValue.Equal(stateVal) {
		t.Errorf("expected true and PlanValue preserved from StateValue, got %v", resp.PlanValue)
	}
}

func TestServerBuildFlavorAndRebuildBodyEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// buildFlavor: null flavor
	var diags diag.Diagnostics
	buildFlavor(ctx, types.ObjectNull(flavorAttributeTypes()), nil, nil, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Missing flavor" {
		t.Fatalf("expected Missing flavor error, got: %v", diags)
	}

	// buildFlavor: missing kind
	diags = diag.Diagnostics{}
	missingKindObj := objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringNull(), "family": types.StringNull(), "vcpus": types.Int64Null(),
		"ram": types.Int64Null(), "name": types.StringNull(),
	})
	buildFlavor(ctx, missingKindObj, nil, nil, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Missing flavor kind" {
		t.Fatalf("expected Missing flavor kind error, got: %v", diags)
	}

	// buildFlavor: invalid kind
	diags = diag.Diagnostics{}
	invalidKindObj := objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("unsupported"), "family": types.StringNull(), "vcpus": types.Int64Null(),
		"ram": types.Int64Null(), "name": types.StringNull(),
	})
	buildFlavor(ctx, invalidKindObj, nil, nil, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Invalid flavor kind" {
		t.Fatalf("expected Invalid flavor kind error, got: %v", diags)
	}

	// buildFlavor: incomplete custom flavor
	diags = diag.Diagnostics{}
	incompleteCustomObj := objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringNull(), "vcpus": types.Int64Null(),
		"ram": types.Int64Null(), "name": types.StringNull(),
	})
	buildFlavor(ctx, incompleteCustomObj, nil, nil, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Incomplete custom flavor" {
		t.Fatalf("expected Incomplete custom flavor error, got: %v", diags)
	}

	// buildFlavor: invalid custom flavor (vcpus 0)
	diags = diag.Diagnostics{}
	invalidCustomObj := objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringValue("basic"), "vcpus": types.Int64Value(0),
		"ram": types.Int64Value(4), "name": types.StringNull(),
	})
	buildFlavor(ctx, invalidCustomObj, nil, nil, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Invalid custom flavor" {
		t.Fatalf("expected Invalid custom flavor error, got: %v", diags)
	}

	// buildServerRebuildBody: null boot
	_, _, rebuildDiags := buildServerRebuildBody(ctx, types.ObjectNull(bootAttributeTypes()), core.UUID{12}, nil)
	if !rebuildDiags.HasError() || rebuildDiags.Errors()[0].Summary() != "Missing boot configuration" {
		t.Fatalf("expected Missing boot configuration error, got: %v", rebuildDiags)
	}

	// buildServerRebuildBody: unsupported boot type (volume)
	volumeBootObj := objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("volume"), "volume": idReference(types.StringValue(testVolumeTypeID.String())),
		"image": testImageSelector(types.StringNull()), "custom_image": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringNull()),
		"volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	_, _, rebuildDiags = buildServerRebuildBody(ctx, volumeBootObj, core.UUID{12}, nil)
	if !rebuildDiags.HasError() || rebuildDiags.Errors()[0].Summary() != "Unsupported server rebuild" {
		t.Fatalf("expected Unsupported server rebuild error, got: %v", rebuildDiags)
	}

	// isTransientBackendError
	if !isTransientBackendError(&core.APIError{StatusCode: 500}) {
		t.Error("expected true for 500 error")
	}
	if isTransientBackendError(&core.APIError{StatusCode: 400}) {
		t.Error("expected false for 400 error")
	}
	if isTransientBackendError(errors.New("generic")) {
		t.Error("expected false for generic error")
	}

	// resolveConfiguredName
	_, resDiags := resolveConfiguredName(ctx, types.StringNull(), "test", "item", func(context.Context, string) (string, error) {
		return "val", nil
	})
	if !resDiags.HasError() || !strings.Contains(resDiags.Errors()[0].Summary(), "Missing") {
		t.Errorf("expected Missing error for null name, got: %v", resDiags)
	}

	_, resDiags = resolveConfiguredName(ctx, types.StringValue("   "), "test", "item", func(context.Context, string) (string, error) {
		return "val", nil
	})
	if !resDiags.HasError() || !strings.Contains(resDiags.Errors()[0].Summary(), "Missing item") {
		t.Errorf("expected Missing item error for whitespace name, got: %v", resDiags)
	}

	_, resDiags = resolveConfiguredName(ctx, types.StringValue("val"), "test", "item", func(context.Context, string) (string, error) {
		return "", errors.New("lookup failed")
	})
	if !resDiags.HasError() || !strings.Contains(resDiags.Errors()[0].Summary(), "Unable to resolve") {
		t.Errorf("expected Unable to resolve error, got: %v", resDiags)
	}

	// appendUnionDiagnostic
	unionDiags := &diag.Diagnostics{}
	appendUnionDiagnostic(unionDiags, nil, "test")
	if unionDiags.HasError() {
		t.Error("expected no error when err is nil")
	}
	appendUnionDiagnostic(unionDiags, errors.New("boom"), "test")
	if !unionDiags.HasError() || !strings.Contains(unionDiags.Errors()[0].Summary(), "Invalid test configuration") {
		t.Errorf("expected Invalid test configuration, got: %v", unionDiags)
	}
}

func TestServerImportStateAndBuildCreateBody(t *testing.T) {
	t.Parallel()

	// ImportState
	resourceSchema := serverSchema(t)
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: resourceSchema}}
	model := emptyServerResourceModel()
	if diags := resp.State.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	r := &ServerResource{}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testServerID.String()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", resp.Diagnostics)
	}
	var importedID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(context.Background(), path.Root("id"), &importedID)...)
	if resp.Diagnostics.HasError() || importedID.ValueString() != testServerID.String() {
		t.Fatalf("expected imported id %s, got %s (diags: %v)", testServerID.String(), importedID.ValueString(), resp.Diagnostics)
	}

	// buildCreateBody on empty plan returns validation errors
	_, createDiags := r.buildCreateBody(context.Background(), ServerResourceModel{})
	if !createDiags.HasError() {
		t.Fatal("expected error on empty create plan")
	}
}

func TestServerAttachCreatedElasticIP(t *testing.T) {
	t.Parallel()
	serverID := testServerID
	eipID := testElasticIPID
	ip := "1.2.3.4"
	ipv6 := "2001:db8::1"
	projectID := core.UUID{12}
	attached := false

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/network/elastic-ips/"+eipID.String()+"/":
			writeJSON(t, w, &networksdk.ElasticIPDetailSchema{
				Id:          eipID,
				IpAddress:   &ip,
				Ipv6Address: &ipv6,
			})
		case req.Method == http.MethodPost && req.URL.Path == "/v2/server/servers/"+serverID.String()+"/attach-eip/":
			attached = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer api.Close()

	srvClient, _ := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	netClient, _ := networksdk.NewClient(api.URL, networksdk.WithRetry(core.NoRetry()))
	r := &ServerResource{client: srvClient, networkClient: netClient, projectID: projectID}

	err := r.attachCreatedElasticIP(context.Background(), serverID, eipID, true, true)
	if err != nil || !attached {
		t.Fatalf("expected successful attachCreatedElasticIP, err=%v attached=%v", err, attached)
	}
}

func TestServerDeleteTerminatedAttachmentsAndServerErrors(t *testing.T) {
	t.Parallel()
	projectID := core.UUID{12}

	// 1. deleteTerminatedAttachments with networkClient error
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "/elastic-ips/") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"message":"network error"}]}`))
			return
		}
		http.NotFound(w, req)
	}))
	defer api.Close()
	netClient, _ := networksdk.NewClient(api.URL, networksdk.WithRetry(core.NoRetry()))
	r := &ServerResource{networkClient: netClient, projectID: projectID}
	state := emptyServerResourceModel()
	state.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(testElasticIPID.String()),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolValue(false),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringValue("1.2.3.4"),
		"ipv6_address": types.StringNull(), "status": types.StringValue("active"),
	}})
	var diags diag.Diagnostics
	r.deleteTerminatedAttachments(context.Background(), state, &diags)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Error deleting server elastic IP" {
		t.Fatalf("expected Error deleting server elastic IP, got: %v", diags)
	}

	// 2. deleteServer requestDeletion error
	apiDelErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer apiDelErr.Close()
	srvClientDel, _ := serversdk.NewClient(apiDelErr.URL, serversdk.WithRetry(core.NoRetry()))
	rDel := &ServerResource{client: srvClientDel, projectID: projectID}
	delDiags := rDel.deleteServer(context.Background(), testServerID, errors.New("stop failed"))
	if !delDiags.HasError() || delDiags.Errors()[0].Summary() != "Error deleting server" {
		t.Fatalf("expected Error deleting server, got: %v", delDiags)
	}

	// 3. updateBandwidth error
	bwDiags := rDel.updateBandwidth(context.Background(), testServerID, 100)
	if !bwDiags.HasError() || bwDiags.Errors()[0].Summary() != "Error updating server bandwidth" {
		t.Fatalf("expected Error updating server bandwidth, got: %v", bwDiags)
	}

	// 4. buildServerResizeBody server get error
	flavorObj := objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(2), "ram": types.Int64Value(4), "name": types.StringNull(),
	})
	_, resizeDiags := rDel.buildServerResizeBody(context.Background(), testServerID, flavorObj)
	if !resizeDiags.HasError() || resizeDiags.Errors()[0].Summary() != "Error reading server before resize" {
		t.Fatalf("expected Error reading server before resize, got: %v", resizeDiags)
	}
}

// --- Fixtures and stubs ---

func TestServerDataVolumeKindsValidateConfiguration(t *testing.T) {
	t.Parallel()
	newVolume := func() map[string]attr.Value {
		return map[string]attr.Value{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}
	}
	existing := func() map[string]attr.Value {
		return map[string]attr.Value{"kind": types.StringValue("existing"), "id": types.StringValue((core.UUID{30}).String())}
	}
	for _, tc := range []struct {
		name    string
		values  []map[string]attr.Value
		invalid bool
	}{
		{"legacy new", []map[string]attr.Value{{"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}}, false},
		{"new", []map[string]attr.Value{newVolume()}, false},
		{"existing", []map[string]attr.Value{existing()}, false},
		{"mixed", []map[string]attr.Value{newVolume(), existing()}, false},
		{"existing unknown ID", []map[string]attr.Value{{"kind": types.StringValue("existing"), "id": types.StringUnknown()}}, false},
		{"new missing size", []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd"))}}, true},
		{"new missing type", []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_size": types.Int64Value(20)}}, true},
		{"new missing type name", []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringNull()), "volume_size": types.Int64Value(20)}}, true},
		{"new configured ID", []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue((core.UUID{30}).String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}}, false},
		{"existing missing ID", []map[string]attr.Value{{"kind": types.StringValue("existing")}}, true},
		{"existing invalid ID", []map[string]attr.Value{{"kind": types.StringValue("existing"), "id": types.StringValue("bad")}}, true},
		{"existing configured size", []map[string]attr.Value{{"kind": types.StringValue("existing"), "id": types.StringValue((core.UUID{30}).String()), "volume_size": types.Int64Value(20)}}, true},
		{"duplicate existing ID", []map[string]attr.Value{existing(), existing()}, true},
		{"duplicate normalized existing ID", []map[string]attr.Value{existing(), {"kind": types.StringValue("existing"), "id": types.StringValue(strings.ToUpper((core.UUID{30}).String()))}}, true},
		{"invalid kind", []map[string]attr.Value{{"kind": types.StringValue("other")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := validServerConfig(t)
			config.DataVolumes = dataVolumeList(t, tc.values)
			if diags := validateServerConfig(t, config); diags.HasError() != tc.invalid {
				t.Fatalf("diagnostics=%v, invalid=%t", diags, tc.invalid)
			}
		})
	}
}

func TestServerDataVolumeCreateBodyOnlyCreatesNewVolumes(t *testing.T) {
	t.Parallel()
	model := localDiskServerModel(t, "server")
	model.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("existing"), "id": types.StringValue((core.UUID{30}).String())},
		{"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)},
	})
	body, diags := buildServerCreateBodyForTest(t, model, nil)
	if diags.HasError() || body.DataVolumes == nil || len(*body.DataVolumes) != 1 || (*body.DataVolumes)[0].VolumeSize != 20 {
		t.Fatalf("create body=%#v diagnostics=%v", body, diags)
	}
	if diags := recordServerSelectorIDs(context.Background(), body, &model); diags.HasError() {
		t.Fatal(diags)
	}
	var volumes []DataVolumeInputModel
	if diags := model.DataVolumes.ElementsAs(context.Background(), &volumes, false); diags.HasError() || len(volumes) != 2 {
		t.Fatalf("mixed volume selector state: %v %v", volumes, diags)
	}
	id, known := selector.ID(volumes[1].VolumeType)
	if !known || id != (*body.DataVolumes)[0].VolumeTypeId || !volumes[0].VolumeType.IsNull() {
		t.Fatalf("mixed volumes lost the new volume selector ID: %v", volumes)
	}
}

func TestServerDataVolumePlanPreservesIdentityAndOmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prior := dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "id": types.StringValue((core.UUID{30}).String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20), "iops": types.Int64Value(900), "delete_on_termination": types.BoolValue(false)},
		{"kind": types.StringValue("new"), "id": types.StringValue((core.UUID{31}).String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30), "iops": types.Int64Value(900), "delete_on_termination": types.BoolValue(true)},
	})
	configured := dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30)},
		{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(25)},
	})
	planned := dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "id": types.StringUnknown(), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30), "iops": types.Int64Unknown(), "delete_on_termination": types.BoolUnknown()},
		{"kind": types.StringValue("new"), "id": types.StringUnknown(), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(25), "iops": types.Int64Unknown(), "delete_on_termination": types.BoolUnknown()},
	})
	resp := resourceplanmodifier.ListResponse{PlanValue: planned}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: prior, PlanValue: planned}, &resp)
	var got []DataVolumeInputModel
	if diags := resp.PlanValue.ElementsAs(ctx, &got, false); resp.Diagnostics.HasError() || diags.HasError() || got[0].ID.ValueString() != (core.UUID{31}).String() || got[1].ID.ValueString() != (core.UUID{30}).String() || got[1].DeleteOnTermination.ValueBool() || got[0].IOPS.ValueInt64() != 900 || !got[1].IOPS.IsUnknown() || resp.RequiresReplace {
		t.Fatalf("plan values=%#v diagnostics=%v %v", got, resp.Diagnostics, diags)
	}
	unknown := types.ListUnknown(prior.ElementType(ctx))
	resp = resourceplanmodifier.ListResponse{PlanValue: unknown}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: types.ListNull(prior.ElementType(ctx)), StateValue: prior, PlanValue: unknown}, &resp)
	if !resp.PlanValue.Equal(prior) {
		t.Fatal("omission must preserve the prior attachment list")
	}
	empty := dataVolumeList(t, nil)
	resp = resourceplanmodifier.ListResponse{PlanValue: empty}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: empty, StateValue: prior, PlanValue: empty}, &resp)
	if len(resp.PlanValue.Elements()) != 0 || resp.RequiresReplace {
		t.Fatal("an empty list must detach without server replacement")
	}
}

func TestServerDataVolumePlanRecomputesBackendIOPS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := types.StringValue((core.UUID{30}).String())
	for _, tc := range []struct {
		name       string
		kind       types.String
		volumeType types.Object
		size       types.Int64
		iops       types.Int64
		want       types.Int64
	}{
		{"unchanged", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Value(20), types.Int64Null(), types.Int64Value(500)},
		{"resize", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Value(30), types.Int64Null(), types.Int64Unknown()},
		{"legacy resize", types.StringNull(), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Value(30), types.Int64Null(), types.Int64Unknown()},
		{"explicit IOPS", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Value(30), types.Int64Value(700), types.Int64Value(700)},
		{"unknown IOPS", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Value(30), types.Int64Unknown(), types.Int64Unknown()},
		{"retype", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd2")), types.Int64Value(20), types.Int64Null(), types.Int64Unknown()},
		{"unknown size", types.StringValue("new"), testVolumeTypeSelector(types.StringValue("ssd")), types.Int64Unknown(), types.Int64Null(), types.Int64Unknown()},
		{"unknown type", types.StringValue("new"), types.ObjectUnknown(blockstorageselector.VolumeTypeAttributeTypes()), types.Int64Value(20), types.Int64Null(), types.Int64Unknown()},
		{"existing attachment", types.StringValue("existing"), types.ObjectNull(blockstorageselector.VolumeTypeAttributeTypes()), types.Int64Null(), types.Int64Null(), types.Int64Value(500)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind := tc.kind
			if kind.IsNull() {
				kind = types.StringValue("new")
			}
			prior := dataVolumeList(t, []map[string]attr.Value{{
				"kind": kind, "id": id, "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20), "iops": types.Int64Value(500),
			}})
			values := map[string]attr.Value{"kind": tc.kind, "id": id, "volume_type": tc.volumeType, "volume_size": tc.size, "iops": tc.iops}
			configured := dataVolumeList(t, []map[string]attr.Value{values})
			plannedValues := maps.Clone(values)
			if tc.iops.IsNull() {
				plannedValues["iops"] = types.Int64Unknown()
			}
			planned := dataVolumeList(t, []map[string]attr.Value{plannedValues})
			response := resourceplanmodifier.ListResponse{PlanValue: planned}
			dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: prior, PlanValue: planned}, &response)
			var got []DataVolumeInputModel
			diags := response.PlanValue.ElementsAs(ctx, &got, false)
			if response.Diagnostics.HasError() || diags.HasError() || len(got) != 1 || !got[0].IOPS.Equal(tc.want) || !got[0].ID.Equal(id) || response.RequiresReplace {
				t.Fatalf("planned=%#v want IOPS=%v diagnostics=%v %v", got, tc.want, response.Diagnostics, diags)
			}
		})
	}
}

func TestServerDataVolumeResizeRefreshesRecalculatedIOPS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newDataVolumeTestAPI(t)
	id := core.UUID{30}
	oldIOPS, newIOPS := 500, 600
	api.volumes[id.String()].Iops = &oldIOPS
	api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
	api.attached[id.String()] = true
	api.resizeIOPS = &newIOPS
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String())}})
	if diags := api.resource.refreshServerDataVolumeState(ctx, api.server(), &state); diags.HasError() {
		t.Fatal(diags)
	}
	configured := dataVolumeList(t, []map[string]attr.Value{{
		"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30),
	}})
	response := resourceplanmodifier.ListResponse{PlanValue: configured}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: state.DataVolumes, PlanValue: configured}, &response)
	var volumes []DataVolumeInputModel
	if diags := response.PlanValue.ElementsAs(ctx, &volumes, false); diags.HasError() || response.Diagnostics.HasError() || !volumes[0].IOPS.IsUnknown() {
		t.Fatalf("plan=%#v diagnostics=%v %v", volumes, response.Diagnostics, diags)
	}
	plan := state
	plan.DataVolumes = response.PlanValue
	update, diags := api.resource.buildDataVolumeUpdatePlan(ctx, plan, state)
	if diags.HasError() || update == nil || len(update.updates) != 0 {
		t.Fatalf("update=%#v diagnostics=%v", update, diags)
	}
	if diags := api.resource.applyDataVolumeUpdate(ctx, testServerID, update); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := api.resource.refreshServerDataVolumeState(ctx, api.server(), &plan); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := plan.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || volumes[0].IOPS.ValueInt64() != 600 || volumes[0].VolumeSize.ValueInt64() != 30 || volumes[0].ID.ValueString() != id.String() {
		t.Fatalf("refreshed=%#v diagnostics=%v", volumes, diags)
	}
	if !slices.Equal(api.mutations, []string{"POST /v2/block-storage/volumes/" + id.String() + "/extend/"}) || api.creates != 0 {
		t.Fatalf("mutations=%v creates=%d", api.mutations, api.creates)
	}
	response = resourceplanmodifier.ListResponse{PlanValue: configured}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: plan.DataVolumes, PlanValue: configured}, &response)
	if response.Diagnostics.HasError() || !response.PlanValue.Equal(plan.DataVolumes) {
		t.Fatalf("repeat plan=%v state=%v diagnostics=%v", response.PlanValue, plan.DataVolumes, response.Diagnostics)
	}
}

func TestServerDataVolumeAmbiguousChangesStopBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, sizes := range [][]int64{{20, 50}, {20, 20}} {
		t.Run(fmt.Sprint(sizes), func(t *testing.T) {
			ctx := context.Background()
			api := newDataVolumeTestAPI(t)
			state := emptyServerResourceModel()
			state.ID = types.StringValue(testServerID.String())
			values := make([]map[string]attr.Value, len(sizes))
			for i, size := range sizes {
				values[i] = map[string]attr.Value{
					"kind": types.StringValue("new"), "id": types.StringValue((core.UUID{30 + byte(i)}).String()),
					"volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(size),
					"delete_on_termination": types.BoolValue(true),
				}
			}
			state.DataVolumes = dataVolumeList(t, values)
			for _, size := range []int64{sizes[1], 60} {
				configured := dataVolumeList(t, []map[string]attr.Value{{
					"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(size),
				}})
				response := resourceplanmodifier.ListResponse{PlanValue: configured}
				dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: state.DataVolumes, PlanValue: configured}, &response)
				plan := state
				plan.DataVolumes = configured
				update, diags := api.resource.buildDataVolumeUpdatePlan(ctx, plan, state)
				ambiguous := size == 60 || sizes[0] == sizes[1]
				if response.Diagnostics.HasError() != ambiguous || diags.HasError() != ambiguous || (ambiguous && update != nil) || len(api.mutations) != 0 {
					t.Fatalf("size=%d update=%#v plan diagnostics=%v update diagnostics=%v mutations=%v", size, update, response.Diagnostics, diags, api.mutations)
				}
			}
		})
	}
}

func TestServerDataVolumeExplicitIDRemovesAndResizesTheIntendedVolumes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newDataVolumeTestAPI(t)
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	first, second := core.UUID{30}, core.UUID{31}
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "id": types.StringValue(first.String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20), "delete_on_termination": types.BoolValue(true)},
		{"kind": types.StringValue("new"), "id": types.StringValue(second.String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50), "delete_on_termination": types.BoolValue(true)},
	})
	api.volumes[second.String()] = &blockstoragesdk.VolumeDetailSchema{Id: second, Size: 50, Status: blockstoragesdk.VolumeStatusInUse, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}}
	api.volumes[first.String()].Status = blockstoragesdk.VolumeStatusInUse
	api.attached[first.String()] = true
	api.attached[second.String()] = true
	configured := dataVolumeList(t, []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue(strings.ToUpper(second.String())), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(60),
	}})
	response := resourceplanmodifier.ListResponse{PlanValue: configured}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: state.DataVolumes, PlanValue: configured}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	plan := state
	plan.DataVolumes = response.PlanValue
	update, diags := api.resource.buildDataVolumeUpdatePlan(ctx, plan, state)
	if diags.HasError() || update == nil || !slices.Equal(update.resizeIDs, []core.UUID{second}) || !slices.Equal(update.resizeSizes, []int{60}) || !slices.Equal(update.deletedIDs, []core.UUID{first}) {
		t.Fatalf("update=%#v diagnostics=%v", update, diags)
	}
	if _, ok := update.removedIDs[second]; ok {
		t.Fatal("the retained volume is a removal target")
	}
	if diags := api.resource.applyDataVolumeUpdate(ctx, testServerID, update); diags.HasError() {
		t.Fatal(diags)
	}
	if api.volumes[first.String()] != nil || api.volumes[second.String()].Size != 60 || !api.attached[second.String()] || len(api.mutations) != 3 {
		t.Fatalf("volumes=%v attachments=%v mutations=%v", api.volumes, api.attached, api.mutations)
	}
	var expected []DataVolumeInputModel
	if diags := response.PlanValue.ElementsAs(ctx, &expected, false); diags.HasError() || !expected[0].IOPS.IsUnknown() {
		t.Fatalf("planned=%#v diagnostics=%v", expected, diags)
	}
	expected[0].IOPS = types.Int64Null()
	expected[0].VolumeType = selector.WithID(expected[0].VolumeType, types.StringValue(testVolumeTypeID.String()))
	wantState, diags := types.ListValueFrom(ctx, response.PlanValue.ElementType(ctx), expected)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if diags := api.resource.refreshServerDataVolumeState(ctx, api.server(), &plan); diags.HasError() || !plan.DataVolumes.Equal(wantState) {
		t.Fatalf("refreshed plan=%v expected=%v diagnostics=%v", plan.DataVolumes, wantState, diags)
	}
	var retained []DataVolumeInputModel
	if diags := plan.DataVolumes.ElementsAs(ctx, &retained, false); diags.HasError() || retained[0].ID.ValueString() != strings.ToUpper(second.String()) {
		t.Fatalf("retained volumes=%#v diagnostics=%v", retained, diags)
	}
}

func TestServerDataVolumeMatchingBoundaries(t *testing.T) {
	t.Parallel()
	volume := func(id types.String, size int64) DataVolumeInputModel {
		return DataVolumeInputModel{Kind: types.StringValue("new"), ID: id, VolumeType: testVolumeTypeSelector(types.StringValue("ssd")), VolumeSize: types.Int64Value(size), IOPS: types.Int64Null()}
	}
	first, second := types.StringValue((core.UUID{30}).String()), types.StringValue((core.UUID{31}).String())
	for _, tc := range []struct {
		name           string
		planned, prior []DataVolumeInputModel
		want           []int
		invalid        bool
	}{
		{"identical unchanged volumes", []DataVolumeInputModel{volume(types.StringNull(), 20), volume(types.StringNull(), 20)}, []DataVolumeInputModel{volume(first, 20), volume(second, 20)}, []int{0, 1}, false},
		{"reserve ID before content", []DataVolumeInputModel{volume(types.StringNull(), 20), volume(first, 30)}, []DataVolumeInputModel{volume(first, 20), volume(second, 20)}, []int{1, 0}, false},
		{"unknown ID", []DataVolumeInputModel{volume(types.StringUnknown(), 60)}, []DataVolumeInputModel{volume(first, 20), volume(second, 50)}, nil, true},
		{"unrecognized ID", []DataVolumeInputModel{volume(second, 60)}, []DataVolumeInputModel{volume(first, 20)}, nil, true},
		{"invalid ID", []DataVolumeInputModel{volume(types.StringValue("bad"), 60)}, []DataVolumeInputModel{volume(first, 20)}, nil, true},
		{"duplicate ID", []DataVolumeInputModel{volume(first, 20), volume(first, 30)}, []DataVolumeInputModel{volume(first, 20)}, nil, true},
		{"ambiguous retype", []DataVolumeInputModel{{Kind: types.StringValue("new"), ID: types.StringNull(), VolumeType: testVolumeTypeSelector(types.StringValue("ssd2")), VolumeSize: types.Int64Value(20)}}, []DataVolumeInputModel{volume(first, 20), volume(second, 50)}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, _, diags := matchDataVolumeConfigs(tc.planned, tc.prior)
			if diags.HasError() != tc.invalid || !slices.Equal(matches, tc.want) {
				t.Fatalf("matches=%v diagnostics=%v", matches, diags)
			}
		})
	}
}

func TestServerDataVolumeNewIDIsRejectedOnCreate(t *testing.T) {
	t.Parallel()
	model := localDiskServerModel(t, "server")
	model.Zone = types.ObjectNull(reference.AttributeTypes(reference.Name))
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringValue((core.UUID{30}).String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20),
	}})
	_, diags := buildServerCreateBodyForTest(t, model, nil)
	if !diags.HasError() || !strings.Contains(fmt.Sprint(diags), "Invalid new data volume ID on create") {
		t.Fatal(diags)
	}
}

func TestServerDataVolumeUpdateReconcilesMixedKinds(t *testing.T) {
	t.Parallel()
	api := newDataVolumeTestAPI(t)
	ctx := context.Background()
	resourceSchema := serverSchema(t)
	stateModel := localDiskServerModel(t, "server")
	stateModel.PowerState = types.StringValue("running")
	stateModel.DataVolumes = dataVolumeList(t, nil)
	state := stateFor(t, serverSchema(t), stateModel)
	update := func(values []map[string]attr.Value) ServerResourceModel {
		t.Helper()
		model := stateModel
		model.DataVolumes = dataVolumeList(t, values)
		plan := tfsdk.Plan{Schema: resourceSchema}
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		resp := resource.UpdateResponse{State: state}
		api.resource.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		state = resp.State
		if diags := state.Get(ctx, &stateModel); diags.HasError() {
			t.Fatal(diags)
		}
		if stateModel.ID.ValueString() != testServerID.String() {
			t.Fatal("attachment update replaced the server")
		}
		return stateModel
	}
	existing := map[string]attr.Value{"kind": types.StringValue("existing"), "id": types.StringValue((core.UUID{30}).String())}
	newVolume := map[string]attr.Value{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}
	model := update([]map[string]attr.Value{existing, newVolume})
	var volumes []DataVolumeInputModel
	if diags := model.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() {
		t.Fatal(diags)
	}
	createdID := volumes[1].ID.ValueString()
	if createdID == "" || volumes[0].DeleteOnTermination.ValueBool() || !volumes[1].DeleteOnTermination.ValueBool() || api.creates != 1 {
		t.Fatalf("mixed attachment defaults=%#v creates=%d", volumes, api.creates)
	}
	mutations := len(api.mutations)
	model = update([]map[string]attr.Value{newVolume, existing})
	if diags := model.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() {
		t.Fatal(diags)
	}
	if volumes[0].ID.ValueString() != createdID || len(api.mutations) != mutations {
		t.Fatal("reordering changed attachment identity")
	}
	model = update([]map[string]attr.Value{existing})
	if _, ok := api.volumes[createdID]; ok || len(model.DataVolumes.Elements()) != 1 {
		t.Fatal("removing a new attachment must delete its owned volume")
	}
	update(nil)
	if api.attached[(core.UUID{30}).String()] || api.volumes[(core.UUID{30}).String()] == nil {
		t.Fatal("removing an existing attachment must preserve its volume")
	}
}

// The matcher binds a new entry without an ID to an unused prior new volume,
// so one apply cannot both delete a removed new volume and create another.
// Each case therefore covers a different subset of the reconciliation steps.
func TestServerDataVolumeUpdateFollowsReconciliationOrder(t *testing.T) {
	t.Parallel()
	removed, retyped, existing, resized := core.UUID{30}, core.UUID{31}, core.UUID{32}, core.UUID{33}
	priorType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
	targetType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd2")), types.StringValue((core.UUID{15}).String()))
	owned := func(id core.UUID, volumeType types.Object, size int64) map[string]attr.Value {
		return map[string]attr.Value{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": volumeType, "volume_size": types.Int64Value(size), "delete_on_termination": types.BoolValue(true)}
	}
	attachExisting := map[string]attr.Value{"kind": types.StringValue("existing"), "id": types.StringValue(existing.String())}
	server := "/v2/server/servers/" + testServerID.String()
	volume := func(id core.UUID) string { return "/v2/block-storage/volumes/" + id.String() + "/" }
	tests := map[string]struct {
		prior, planned []map[string]attr.Value
		attached       []core.UUID
		want           []string
	}{
		"deleteBeforePropertyChanges": {
			prior: []map[string]attr.Value{owned(removed, priorType, 20), owned(retyped, priorType, 20), owned(resized, priorType, 20)},
			planned: []map[string]attr.Value{
				func() map[string]attr.Value {
					entry := owned(retyped, targetType, 20)
					entry["iops"] = types.Int64Value(1200)
					return entry
				}(),
				owned(resized, priorType, 30),
				attachExisting,
			},
			attached: []core.UUID{removed, retyped, resized},
			want: []string{
				"POST " + server + "/detach-volume/",
				"DELETE " + volume(removed),
				"POST " + volume(retyped) + "retype/",
				"PATCH " + volume(retyped),
				"POST " + volume(resized) + "extend/",
				"POST " + server + "/attach-volume/",
			},
		},
		"createAfterExistingAttachments": {
			prior: []map[string]attr.Value{
				{"kind": types.StringValue("existing"), "id": types.StringValue(removed.String()), "attached": types.BoolValue(true), "delete_on_termination": types.BoolValue(false)},
				owned(resized, priorType, 20),
			},
			planned: []map[string]attr.Value{
				owned(resized, priorType, 30),
				attachExisting,
				{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)},
			},
			attached: []core.UUID{removed, resized},
			want: []string{
				"POST " + server + "/detach-volume/",
				"POST " + volume(resized) + "extend/",
				"POST " + server + "/attach-volume/",
				"POST /v2/block-storage/volumes/",
				"POST " + server + "/attach-volume/",
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			api := newDataVolumeTestAPI(t)
			for _, id := range []core.UUID{retyped, existing, resized} {
				api.volumes[id.String()] = &blockstoragesdk.VolumeDetailSchema{Id: id, Size: 20, Status: blockstoragesdk.VolumeStatusAvailable, Zone: blockstoragesdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}}
			}
			for _, id := range test.attached {
				api.attached[id.String()] = true
				api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
			}
			stateModel := localDiskServerModel(t, "server")
			stateModel.PowerState = types.StringValue("running")
			stateModel.DataVolumes = dataVolumeList(t, test.prior)
			planModel := stateModel
			planModel.DataVolumes = dataVolumeList(t, test.planned)
			schema := serverSchema(t)
			state := stateFor(t, schema, stateModel)
			resp := resource.UpdateResponse{State: state}
			api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, planModel), State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !slices.Equal(api.mutations, test.want) {
				t.Fatalf("mutations=%q want=%q", api.mutations, test.want)
			}
		})
	}
}

func TestServerDataVolumeUpdateAfterRenameHandlesOperationConflicts(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"detach", "attachExisting", "attachNew"} {
		for _, outcome := range []string{"settled", "otherConflict", "badRequest", "timeout", "cancelled"} {
			t.Run(operation+"/"+outcome, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					api := newDataVolumeTestAPI(t)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					id := core.UUID{30}
					stateModel := localDiskServerModel(t, "server")
					stateModel.PowerState = types.StringValue("running")
					stateModel.DataVolumes = dataVolumeList(t, nil)
					existing := map[string]attr.Value{"kind": types.StringValue("existing"), "id": types.StringValue(id.String())}
					if operation == "detach" {
						api.attached[id.String()] = true
						api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
						stateModel.DataVolumes = dataVolumeList(t, []map[string]attr.Value{existing})
					}
					planModel := stateModel
					planModel.Name = types.StringValue("renamed-server")
					switch operation {
					case "detach":
						planModel.DataVolumes = dataVolumeList(t, nil)
					case "attachExisting":
						planModel.DataVolumes = dataVolumeList(t, []map[string]attr.Value{existing})
					case "attachNew":
						planModel.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
							"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20),
						}})
					}
					start := time.Now()
					attempts := 0
					api.rejectVolumeOperation = func(w http.ResponseWriter, _ *http.Request) bool {
						attempts++
						if api.name != "renamed-server" {
							t.Fatal("volume operation ran before the rename")
						}
						// A visible rename does not prove that the operation lock is free.
						if outcome == "settled" && time.Since(start) >= 2*serverPollInterval {
							return false
						}
						status, code := http.StatusConflict, "conflict"
						message := "There is another operation running on this server, retry again later"
						switch outcome {
						case "otherConflict":
							message = "The volume is attached to another server"
						case "badRequest":
							status, code = http.StatusBadRequest, "invalid"
						case "cancelled":
							cancel()
						}
						w.WriteHeader(status)
						writeJSON(t, w, map[string]any{"errors": []any{map[string]string{"code": code, "message": message}}})
						return true
					}
					schema := serverSchema(t)
					state := stateFor(t, schema, stateModel)
					resp := resource.UpdateResponse{State: state}
					api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, planModel), State: state}, &resp)
					elapsed := time.Since(start)
					if outcome != "settled" {
						if !resp.Diagnostics.HasError() {
							t.Fatal("expected the rejected volume operation to fail")
						}
						if outcome == "timeout" {
							if elapsed != serverOperationTimeout || attempts < 2 || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "timeout") {
								t.Fatalf("attempts=%d elapsed=%s diagnostics=%v", attempts, elapsed, resp.Diagnostics)
							}
						} else if attempts != 1 || elapsed != 0 {
							t.Fatalf("unexpected retry: attempts=%d elapsed=%s diagnostics=%v", attempts, elapsed, resp.Diagnostics)
						}
						if outcome == "cancelled" && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), context.Canceled.Error()) {
							t.Fatalf("cancellation lost its cause: %v", resp.Diagnostics)
						}
						return
					}
					if resp.Diagnostics.HasError() || attempts != 3 || elapsed != 2*serverPollInterval {
						t.Fatalf("attempts=%d elapsed=%s diagnostics=%v", attempts, elapsed, resp.Diagnostics)
					}
					var got ServerResourceModel
					if diags := resp.State.Get(ctx, &got); diags.HasError() {
						t.Fatal(diags)
					}
					if got.ID != stateModel.ID || got.Name != planModel.Name {
						t.Fatalf("server identity or rename lost: %#v", got)
					}
					if operation == "detach" {
						if len(got.DataVolumes.Elements()) != 0 || api.attached[id.String()] || api.volumes[id.String()] == nil {
							t.Fatal("detach lost the retained volume or kept its attachment")
						}
					} else {
						var volumes []DataVolumeInputModel
						if diags := got.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || len(volumes) != 1 || !api.attached[volumes[0].ID.ValueString()] {
							t.Fatalf("attachment missing from state: %#v diagnostics=%v", volumes, diags)
						}
						if operation == "attachNew" && (api.creates != 1 || api.volumes[volumes[0].ID.ValueString()] == nil) {
							t.Fatal("busy rejection recreated or deleted the new volume")
						}
					}
				})
			})
		}
	}
}

func TestServerDataVolumeExistingAttachmentCreateAndDrift(t *testing.T) {
	t.Parallel()
	api := newDataVolumeTestAPI(t)
	model := localDiskServerModel(t, "server")
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("existing"), "id": types.StringValue((core.UUID{30}).String())}})
	server, diags := api.resource.attachExistingDataVolumesAfterCreate(context.Background(), testServerID, &model)
	if diags.HasError() || server == nil || !api.attached[(core.UUID{30}).String()] {
		t.Fatalf("create attach=%v %v", server, diags)
	}
	if diags := api.resource.refreshServerDataVolumeState(context.Background(), server, &model); diags.HasError() {
		t.Fatal(diags)
	}
	api.attached[(core.UUID{30}).String()] = false
	if diags := api.resource.refreshServerDataVolumeState(context.Background(), api.server(), &model); diags.HasError() || len(model.DataVolumes.Elements()) != 0 {
		t.Fatalf("external detach was hidden: %v", diags)
	}
}

func TestServerDataVolumeCreateAttachmentsHandleOperationConflicts(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"settled", "otherConflict", "badRequest", "timeout", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				api := newDataVolumeTestAPI(t)
				first, second := core.UUID{30}, core.UUID{31}
				volume := *api.volumes[first.String()]
				volume.Id = second
				api.volumes[second.String()] = &volume
				model := localDiskServerModel(t, "server")
				model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
					{"kind": types.StringValue("existing"), "id": types.StringValue(first.String())},
					{"kind": types.StringValue("existing"), "id": types.StringValue(second.String())},
				})
				attempts := 0
				start := time.Now()
				api.rejectVolumeOperation = func(w http.ResponseWriter, _ *http.Request) bool {
					attempts++
					if outcome == "settled" && attempts%2 == 0 {
						return false
					}
					status, message := http.StatusConflict, "There is another operation running on this server, retry again later"
					switch outcome {
					case "otherConflict":
						message = "The volume is attached to another server"
					case "badRequest":
						status = http.StatusBadRequest
					case "cancelled":
						cancel()
					}
					w.WriteHeader(status)
					writeJSON(t, w, map[string]any{"errors": []any{map[string]string{"code": "conflict", "message": message}}})
					return true
				}
				server, diags := api.resource.attachExistingDataVolumesAfterCreate(ctx, testServerID, &model)
				elapsed := time.Since(start)
				if outcome == "settled" {
					if diags.HasError() || attempts != 4 || elapsed != 2*serverPollInterval || server == nil || len(server.Volumes) != 2 {
						t.Fatalf("attempts=%d elapsed=%s server=%v diagnostics=%v", attempts, elapsed, server, diags)
					}
					if !api.attached[first.String()] || !api.attached[second.String()] || api.creates != 0 {
						t.Fatal("existing volumes were not attached exactly once")
					}
					return
				}
				if !diags.HasError() || len(api.attached) != 0 {
					t.Fatalf("rejected attachment did not fail: diagnostics=%v attachments=%v", diags, api.attached)
				}
				if outcome == "timeout" {
					if elapsed != serverOperationTimeout || attempts < 2 || !strings.Contains(diags.Errors()[0].Detail(), "timeout") {
						t.Fatalf("attempts=%d elapsed=%s diagnostics=%v", attempts, elapsed, diags)
					}
				} else if elapsed != 0 || attempts != 1 {
					t.Fatalf("unexpected retry: attempts=%d elapsed=%s diagnostics=%v", attempts, elapsed, diags)
				}
				if outcome == "cancelled" && !strings.Contains(diags.Errors()[0].Detail(), context.Canceled.Error()) {
					t.Fatalf("cancellation lost its cause: %v", diags)
				}
			})
		})
	}
}

func TestServerDataVolumeImportAndCleanupOwnership(t *testing.T) {
	t.Parallel()
	api := newDataVolumeTestAPI(t)
	id := core.UUID{30}
	api.attached[id.String()] = true
	model := emptyServerResourceModel()
	if diags := api.resource.refreshServerDataVolumeState(context.Background(), api.server(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	var configs []DataVolumeInputModel
	if diags := model.DataVolumes.ElementsAs(context.Background(), &configs, false); diags.HasError() || dataVolumeKind(configs[0]) != "existing" || configs[0].DeleteOnTermination.ValueBool() {
		t.Fatalf("import assumed volume ownership: %v %#v", diags, configs)
	}
	ids, diags := serverVolumeCascadeTargets(context.Background(), model, api.server())
	if diags.HasError() || len(ids) != 0 {
		t.Fatalf("imported volume selected for deletion: %v %v", ids, diags)
	}
	configs[0].DeleteOnTermination = types.BoolValue(true)
	model.DataVolumes, _ = types.ListValueFrom(context.Background(), model.DataVolumes.ElementType(context.Background()), configs)
	ids, diags = serverVolumeCascadeTargets(context.Background(), model, api.server())
	if diags.HasError() || len(ids) != 1 || ids[0] != id {
		t.Fatalf("explicit deletion flag ignored: %v %v", ids, diags)
	}
}

func TestServerDataVolumeImportConfiguredAttachmentsConverge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newDataVolumeTestAPI(t)
	id := core.UUID{30}
	api.attached[id.String()] = true
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	if diags := api.resource.refreshServerDataVolumeState(ctx, api.server(), &state); diags.HasError() {
		t.Fatal(diags)
	}
	configured := dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("existing"), "id": types.StringValue(id.String())}})
	response := resourceplanmodifier.ListResponse{PlanValue: configured}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: state.DataVolumes, PlanValue: configured}, &response)
	if response.Diagnostics.HasError() || !response.PlanValue.Equal(state.DataVolumes) {
		t.Fatalf("plan=%v state=%v diagnostics=%v", response.PlanValue, state.DataVolumes, response.Diagnostics)
	}
	plan := state
	plan.DataVolumes = response.PlanValue
	if update, diags := api.resource.buildDataVolumeUpdatePlan(ctx, plan, state); update != nil || diags.HasError() || len(api.mutations) != 0 {
		t.Fatalf("update=%#v diagnostics=%v mutations=%v", update, diags, api.mutations)
	}
	ids, diags := serverVolumeCascadeTargets(ctx, state, api.server())
	if diags.HasError() || len(ids) != 0 {
		t.Fatalf("import cleanup IDs=%v diagnostics=%v", ids, diags)
	}
}

func TestServerDataVolumeCleanupFailureRetainsOwnedVolume(t *testing.T) {
	t.Parallel()
	api := newDataVolumeTestAPI(t)
	id := core.UUID{30}
	api.attached[id.String()] = true
	api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
	api.failDelete = true
	model := localDiskServerModel(t, "server")
	model.PowerState = types.StringValue("running")
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}})
	resourceSchema := serverSchema(t)
	state := stateFor(t, resourceSchema, model)
	model.DataVolumes = dataVolumeList(t, nil)
	plan := planFor(t, resourceSchema, model)
	resp := resource.UpdateResponse{State: state}
	api.resource.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("cleanup failure must report an error")
	}
	var failed ServerResourceModel
	if diags := resp.State.Get(context.Background(), &failed); diags.HasError() {
		t.Fatal(diags)
	}
	var configs []DataVolumeInputModel
	if diags := failed.DataVolumes.ElementsAs(context.Background(), &configs, false); diags.HasError() || len(configs) != 1 || configs[0].ID.ValueString() != id.String() || configs[0].Attached.ValueBool() {
		t.Fatalf("cleanup failure lost its owned volume: %v %#v", diags, configs)
	}
	api.failDelete = false
	retry := resource.UpdateResponse{State: resp.State}
	api.resource.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: resp.State}, &retry)
	if retry.Diagnostics.HasError() {
		t.Fatal(retry.Diagnostics)
	}
	if api.volumes[id.String()] != nil {
		t.Fatal("retry did not finish deleting the owned volume")
	}
}

func TestServerDataVolumeFailedUpdateKeepsUndetachedVolumeIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newDataVolumeTestAPI(t)
	owned, existing := core.UUID{30}, core.UUID{31}
	api.volumes[existing.String()] = &blockstoragesdk.VolumeDetailSchema{Id: existing, Size: 30, Status: blockstoragesdk.VolumeStatusInUse, Zone: blockstoragesdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}}
	api.volumes[owned.String()].Status = blockstoragesdk.VolumeStatusInUse
	api.attached[owned.String()] = true
	api.attached[existing.String()] = true
	api.failPatch = true
	model := localDiskServerModel(t, "server")
	model.PowerState = types.StringValue("running")
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "id": types.StringValue(owned.String()), "attached": types.BoolValue(true), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20), "delete_on_termination": types.BoolValue(true)},
		{"kind": types.StringValue("existing"), "id": types.StringValue(existing.String()), "attached": types.BoolValue(true), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(30), "delete_on_termination": types.BoolValue(false)},
	})
	resourceSchema := serverSchema(t)
	state := stateFor(t, resourceSchema, model)
	// The PATCH fails before the new volume exists and before the existing
	// volume is detached, so the new entry still has an unknown ID.
	configured := dataVolumeList(t, []map[string]attr.Value{
		{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)},
		{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(50)},
	})
	planned := resourceplanmodifier.ListResponse{PlanValue: configured}
	dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: model.DataVolumes, PlanValue: configured}, &planned)
	if planned.Diagnostics.HasError() {
		t.Fatal(planned.Diagnostics)
	}
	model.Name = types.StringValue("renamed")
	model.DataVolumes = planned.PlanValue
	resp := resource.UpdateResponse{State: state}
	api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, resourceSchema, model), State: state}, &resp)
	if !resp.Diagnostics.HasError() || api.creates != 0 || !api.attached[existing.String()] {
		t.Fatalf("diagnostics=%v creates=%d attachments=%v", resp.Diagnostics, api.creates, api.attached)
	}
	var failed ServerResourceModel
	if diags := resp.State.Get(ctx, &failed); diags.HasError() {
		t.Fatal(diags)
	}
	var volumes []DataVolumeInputModel
	if diags := failed.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || len(volumes) != 2 {
		t.Fatalf("volumes=%#v diagnostics=%v", volumes, diags)
	}
	if volumes[0].ID.ValueString() != owned.String() || volumes[0].Kind.ValueString() != "new" || !volumes[0].DeleteOnTermination.ValueBool() {
		t.Fatalf("owned volume lost its identity: %#v", volumes[0])
	}
	if volumes[1].ID.ValueString() != existing.String() || volumes[1].Kind.ValueString() != "existing" || volumes[1].DeleteOnTermination.ValueBool() {
		t.Fatalf("the uncreated volume took the existing volume identity: %#v", volumes[1])
	}
}

func TestServerDataVolumesAfterUpdateKeepOnlyUndetachedRemovals(t *testing.T) {
	t.Parallel()
	volume := func(kind string, id types.String) DataVolumeInputModel {
		return DataVolumeInputModel{Kind: types.StringValue(kind), ID: id}
	}
	kept, uncreated := volume("new", types.StringValue((core.UUID{30}).String())), volume("new", types.StringUnknown())
	undetached, detached := volume("existing", types.StringValue((core.UUID{31}).String())), volume("new", types.StringValue((core.UUID{32}).String()))
	cleanup := volume("new", types.StringValue((core.UUID{33}).String()))
	server := &serversdk.ServerDetailSchema{Volumes: []serversdk.NestedServerVolumeSchema{
		{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: core.UUID{30}}},
		{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: core.UUID{31}}},
	}}
	plan := &dataVolumeUpdatePlan{
		planned:        []DataVolumeInputModel{kept, uncreated},
		detachConfigs:  []DataVolumeInputModel{undetached, detached},
		cleanupConfigs: map[core.UUID]DataVolumeInputModel{{33}: cleanup},
	}
	got := dataVolumesAfterUpdate(plan, server)
	if want := []DataVolumeInputModel{kept, undetached, cleanup}; !slices.EqualFunc(got, want, func(left, right DataVolumeInputModel) bool {
		return left.Kind.Equal(right.Kind) && left.ID.Equal(right.ID)
	}) {
		t.Fatalf("volumes=%#v want=%#v", got, want)
	}
}

func TestServerDataVolumeTypeAndIOPSUpdatesKeepVolumeIdentity(t *testing.T) {
	t.Parallel()
	api := newDataVolumeTestAPI(t)
	id := core.UUID{30}
	api.attached[id.String()] = true
	api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
	model := localDiskServerModel(t, "server")
	model.PowerState = types.StringValue("running")
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}})
	resourceSchema := serverSchema(t)
	state := stateFor(t, resourceSchema, model)
	model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd2")), "volume_size": types.Int64Value(20), "iops": types.Int64Value(1200)}})
	plan := planFor(t, resourceSchema, model)
	resp := resource.UpdateResponse{State: state}
	api.resource.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if diags := resp.State.Get(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	var configs []DataVolumeInputModel
	if diags := model.DataVolumes.ElementsAs(context.Background(), &configs, false); diags.HasError() || configs[0].ID.ValueString() != id.String() || configs[0].IOPS.ValueInt64() != 1200 || referenceCriterion(configs[0].VolumeType, reference.Name) != "ssd2" || api.creates != 0 {
		t.Fatalf("type and IOPS update replaced the volume: %#v %v", configs, diags)
	}
}

func TestServerDataVolumeDetachedOwnershipFollowsPlan(t *testing.T) {
	t.Parallel()
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			api := newDataVolumeTestAPI(t)
			id := core.UUID{30}
			model := localDiskServerModel(t, "server")
			model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
				"kind": types.StringValue("new"), "id": types.StringValue(id.String()),
				"delete_on_termination": types.BoolValue(false),
			}})
			if diags := api.resource.refreshServerResourceState(ctx, api.server(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			schema := serverSchema(t)
			state := stateFor(t, schema, model)
			priorVolumes := model.DataVolumes
			model.Name = types.StringValue("renamed")
			model.DataVolumes = types.ListNull(priorVolumes.ElementType(ctx))
			if configured {
				model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
					"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")),
					"volume_size": types.Int64Value(20),
				}})
			}
			configPlan := planFor(t, schema, model)
			config := tfsdk.Config{Schema: schema, Raw: configPlan.Raw}
			plannedVolumes := model.DataVolumes
			if !configured {
				plannedVolumes = types.ListUnknown(priorVolumes.ElementType(ctx))
			}
			listPlan := resourceplanmodifier.ListResponse{PlanValue: plannedVolumes}
			dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{
				ConfigValue: model.DataVolumes, StateValue: priorVolumes, PlanValue: plannedVolumes,
			}, &listPlan)
			if listPlan.Diagnostics.HasError() {
				t.Fatal(listPlan.Diagnostics)
			}
			model.DataVolumes = listPlan.PlanValue
			plan := planFor(t, schema, model)
			modified := resource.ModifyPlanResponse{Plan: plan}
			api.resource.ModifyPlan(ctx, resource.ModifyPlanRequest{Config: config, Plan: plan, State: state}, &modified)
			if modified.Diagnostics.HasError() {
				t.Fatal(modified.Diagnostics)
			}
			var planned []DataVolumeInputModel
			if diags := modified.Plan.GetAttribute(ctx, path.Root("data_volumes"), &planned); diags.HasError() || len(planned) != 1 || !planned[0].Attached.Equal(types.BoolValue(configured)) {
				t.Fatalf("planned volumes=%#v diagnostics=%v", planned, diags)
			}
			resp := resource.UpdateResponse{State: state}
			api.resource.Update(ctx, resource.UpdateRequest{Config: config, Plan: modified.Plan, State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if diags := resp.State.Get(ctx, &model); diags.HasError() {
				t.Fatal(diags)
			}
			if !model.DataVolumes.Equal(listPlan.PlanValue) || model.Name.ValueString() != "renamed" || model.ID.ValueString() != testServerID.String() {
				t.Fatalf("update contradicted its plan: volumes=%v name=%v id=%v", model.DataVolumes, model.Name, model.ID)
			}
			wantMutations := []string{"PATCH /v2/server/servers/" + testServerID.String() + "/"}
			if configured {
				wantMutations = append(wantMutations, "POST /v2/server/servers/"+testServerID.String()+"/attach-volume/")
			}
			if !slices.Equal(api.mutations, wantMutations) || api.creates != 0 || api.attached[id.String()] != configured || api.volumes[id.String()] == nil {
				t.Fatalf("mutations=%v creates=%d attached=%v", api.mutations, api.creates, api.attached)
			}
		})
	}
}

func TestServerDataVolumeCreationKeepsOwnershipWhenRefreshFails(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"volumeWait", "attachRejected", "attachmentWait", "finalRead", "emptyRead", "secondCreate"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				api := newDataVolumeTestAPI(t)
				priorID, createdID := core.UUID{30}, core.UUID{41}
				api.attached[priorID.String()] = true
				api.volumes[priorID.String()].Status = blockstoragesdk.VolumeStatusInUse
				model := localDiskServerModel(t, "server")
				model.PowerState = types.StringValue("running")
				model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
					"kind": types.StringValue("existing"), "id": types.StringValue(priorID.String()),
					"attached": types.BoolValue(true), "delete_on_termination": types.BoolValue(false),
				}})
				if diags := api.resource.refreshServerDataVolumeState(ctx, api.server(), &model); diags.HasError() {
					t.Fatal(diags)
				}
				schema := serverSchema(t)
				state := stateFor(t, schema, model)
				configured := []map[string]attr.Value{
					{"kind": types.StringValue("existing"), "id": types.StringValue(priorID.String())},
					{"kind": types.StringValue("new"), "id": types.StringUnknown(), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)},
				}
				if failure == "secondCreate" {
					configured = append(configured, configured[1])
				}
				model.DataVolumes = dataVolumeList(t, configured)
				resp := resource.UpdateResponse{State: state}
				serverReads := 0
				secondCreateRejected := false
				api.failDelete = failure == "attachRejected"
				api.rejectRequest = func(w http.ResponseWriter, req *http.Request) bool {
					if api.creates == 0 {
						return false
					}
					// Ownership must reach state before the first fallible request
					// after allocation, without copying unknown planned values.
					if !resp.State.Raw.IsFullyKnown() {
						t.Fatal("allocation checkpoint contains unknown values")
					}
					var saved ServerResourceModel
					if diags := resp.State.Get(ctx, &saved); diags.HasError() {
						t.Fatal(diags)
					}
					var volumes []DataVolumeInputModel
					if diags := saved.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || len(volumes) != 2 || volumes[1].ID.ValueString() != createdID.String() || dataVolumeKind(volumes[1]) != "new" || !volumes[1].DeleteOnTermination.ValueBool() {
						t.Fatalf("allocation lost ownership before %s %s: volumes=%#v diagnostics=%v", req.Method, req.URL.Path, volumes, diags)
					}
					if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/server/servers/") {
						serverReads++
						if (failure == "finalRead" || failure == "emptyRead") && serverReads < 3 || failure == "secondCreate" && !secondCreateRejected {
							return false
						}
						if failure == "emptyRead" {
							w.WriteHeader(http.StatusOK)
						} else {
							w.WriteHeader(http.StatusBadRequest)
						}
						return true
					}
					if failure == "volumeWait" && req.Method == http.MethodGet && strings.Contains(req.URL.Path, createdID.String()) ||
						failure == "attachRejected" && strings.HasSuffix(req.URL.Path, "/attach-volume/") {
						w.WriteHeader(http.StatusBadRequest)
						return true
					}
					if failure == "secondCreate" && req.Method == http.MethodPost && req.URL.Path == "/v2/block-storage/volumes/" {
						secondCreateRejected = true
						w.WriteHeader(http.StatusBadRequest)
						return true
					}
					return false
				}
				start := time.Now()
				api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, model), State: state}, &resp)
				if !resp.Diagnostics.HasError() || api.creates != 1 || serverReads == 0 {
					t.Fatalf("creates=%d reads=%d diagnostics=%v", api.creates, serverReads, resp.Diagnostics)
				}
				if (failure == "finalRead" || failure == "emptyRead") && time.Since(start) != serverOperationTimeout {
					t.Fatalf("refresh failed before its production timeout: elapsed=%s", time.Since(start))
				}
				api.rejectRequest, api.failDelete = nil, false
				read := resource.ReadResponse{State: resp.State}
				api.resource.Read(ctx, resource.ReadRequest{State: resp.State}, &read)
				if read.Diagnostics.HasError() {
					t.Fatal(read.Diagnostics)
				}
				var refreshed ServerResourceModel
				if diags := read.State.Get(ctx, &refreshed); diags.HasError() {
					t.Fatal(diags)
				}
				var volumes []DataVolumeInputModel
				if diags := refreshed.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || len(volumes) != 2 {
					t.Fatalf("volumes=%#v diagnostics=%v", volumes, diags)
				}
				if volumes[0].ID.ValueString() != priorID.String() || dataVolumeKind(volumes[0]) != "existing" || volumes[0].DeleteOnTermination.ValueBool() ||
					volumes[1].ID.ValueString() != createdID.String() || dataVolumeKind(volumes[1]) != "new" || !volumes[1].DeleteOnTermination.ValueBool() || volumes[1].Attached.ValueBool() != api.attached[createdID.String()] {
					t.Fatalf("refresh changed identity or ownership: %#v", volumes)
				}
				// Retry with the recorded ID. The allocation that failed before
				// returning an ID is the only volume that still needs creating.
				configured[1] = maps.Clone(configured[1])
				configured[1]["id"] = types.StringValue(createdID.String())
				if failure == "secondCreate" {
					configured[2] = maps.Clone(configured[2])
					configured[2]["id"] = types.StringNull()
				}
				config := dataVolumeList(t, configured)
				planned := resourceplanmodifier.ListResponse{PlanValue: config}
				dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: config, StateValue: refreshed.DataVolumes, PlanValue: config}, &planned)
				if planned.Diagnostics.HasError() {
					t.Fatal(planned.Diagnostics)
				}
				refreshed.DataVolumes = planned.PlanValue
				retry := resource.UpdateResponse{State: read.State}
				api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, refreshed), State: read.State}, &retry)
				wantCreates := uint8(1)
				if failure == "secondCreate" {
					wantCreates++
				}
				if retry.Diagnostics.HasError() || api.creates != wantCreates || !api.attached[createdID.String()] {
					t.Fatalf("retry lost or duplicated the owned volume: creates=%d diagnostics=%v", api.creates, retry.Diagnostics)
				}
			})
		})
	}
}

func TestServerDataVolumeAcceptedAttachTimeoutKeepsOwnedVolumeForRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := newDataVolumeTestAPI(t)
		api.attachPending = true
		ctx := context.Background()
		resourceSchema := serverSchema(t)
		model := localDiskServerModel(t, "server")
		model.PowerState = types.StringValue("running")
		model.DataVolumes = dataVolumeList(t, nil)
		state := stateFor(t, resourceSchema, model)
		model.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}})
		plan := tfsdk.Plan{Schema: resourceSchema}
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		resp := resource.UpdateResponse{State: state}
		api.resource.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("an accepted attachment that never settles must fail")
		}
		if diags := resp.State.Get(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		var configs []DataVolumeInputModel
		if diags := model.DataVolumes.ElementsAs(ctx, &configs, false); diags.HasError() || len(configs) != 1 || configs[0].Attached.ValueBool() || configs[0].ID.IsNull() {
			t.Fatalf("failed attach lost volume ownership: %#v %v", configs, diags)
		}
		id := configs[0].ID.ValueString()
		api.attachPending = false
		configured := dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "volume_type": testVolumeTypeSelector(types.StringValue("ssd")), "volume_size": types.Int64Value(20)}})
		listPlan := resourceplanmodifier.ListResponse{PlanValue: configured}
		dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: configured, StateValue: model.DataVolumes, PlanValue: configured}, &listPlan)
		if listPlan.Diagnostics.HasError() {
			t.Fatal(listPlan.Diagnostics)
		}
		model.DataVolumes = listPlan.PlanValue
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		retry := resource.UpdateResponse{State: resp.State}
		api.resource.Update(ctx, resource.UpdateRequest{Plan: plan, State: resp.State}, &retry)
		if retry.Diagnostics.HasError() {
			t.Fatal(retry.Diagnostics)
		}
		if diags := retry.State.Get(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		if diags := model.DataVolumes.ElementsAs(ctx, &configs, false); diags.HasError() || api.creates != 1 || !configs[0].Attached.ValueBool() || configs[0].ID.ValueString() != id {
			t.Fatalf("retry allocated another volume: %#v %v", configs, diags)
		}
	})
}

func TestServerDataVolumeWaitsAndFailureDiagnostics(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := newDataVolumeTestAPI(t)
		id := core.UUID{30}
		api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusAttaching
		go func() {
			time.Sleep(serverPollInterval)
			api.mu.Lock()
			defer api.mu.Unlock()
			api.attached[id.String()] = true
			api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
		}()
		start := time.Now()
		if _, err := api.resource.waitUntilDataVolumeAttachment(context.Background(), testServerID, id, true); err != nil || time.Since(start) < serverPollInterval {
			t.Fatalf("attach returned early: %v", err)
		}
		api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusDetachFailed
		if _, err := api.resource.waitUntilDataVolumeAttachment(context.Background(), testServerID, id, false); err == nil {
			t.Fatal("terminal failure must stop the waiter")
		}
		api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusDetaching
		start = time.Now()
		if _, err := api.resource.waitUntilDataVolumeAttachment(context.Background(), testServerID, id, false); !errors.Is(err, wait.ErrTimeout) || time.Since(start) != serverOperationTimeout {
			t.Fatalf("timeout=%v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := api.resource.waitUntilDataVolumeAttachment(ctx, testServerID, id, false); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
		api.attached[id.String()] = false
		api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
		if _, err := api.resource.waitUntilDataVolumeAttachment(context.Background(), testServerID, id, false); err != nil {
			t.Fatalf("multiattach volume can remain in-use: %v", err)
		}
	})
}

// A delete that the backend accepts can still fail asynchronously. The waiter
// must report that at once instead of polling until serverOperationTimeout.
func TestServerDataVolumeDeleteWaiterStopsOnTerminalStatus(t *testing.T) {
	t.Parallel()
	id := core.UUID{30}
	for _, tt := range []struct {
		name     string
		statuses []blockstoragesdk.VolumeStatus
		wantErr  string
	}{
		{
			name:     "deleting then failed",
			statuses: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusDeleting, blockstoragesdk.VolumeStatusFailed},
			wantErr:  "volume " + id.String() + " stopped deleting and settled in status failed",
		},
		{
			name:     "deleting then available",
			statuses: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusDeleting, blockstoragesdk.VolumeStatusAvailable},
			wantErr:  "volume " + id.String() + " stopped deleting and settled in status available",
		},
		{
			// An empty status serves 404. The leading available status is the
			// one that the volume reports before the delete takes effect.
			name:     "available then deleting then gone",
			statuses: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable, blockstoragesdk.VolumeStatusDeleting, ""},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				gets := 0
				handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method != http.MethodGet || req.URL.Path != "/v2/block-storage/volumes/"+id.String()+"/" {
						t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
						http.NotFound(w, req)
						return
					}
					status := tt.statuses[min(gets, len(tt.statuses)-1)]
					gets++
					if status == "" {
						http.NotFound(w, req)
						return
					}
					writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: id, Status: status})
				})
				storage, err := blockstoragesdk.NewClient("https://volume.test", blockstoragesdk.WithHTTPClient(stubHTTPClient(t, handler)), blockstoragesdk.WithRetry(core.NoRetry()))
				if err != nil {
					t.Fatal(err)
				}
				r := &ServerResource{blockStorageClient: storage, projectID: core.UUID{12}}

				start := time.Now()
				err = r.waitUntilDataVolumeDeleted(context.Background(), id)
				elapsed := time.Since(start)

				if tt.wantErr == "" && err != nil {
					t.Fatalf("expected the deletion to finish, got %v", err)
				}
				if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr || errors.Is(err, wait.ErrTimeout)) {
					t.Fatalf("expected %q, got %v", tt.wantErr, err)
				}
				if gets != len(tt.statuses) {
					t.Fatalf("expected %d GET requests, got %d", len(tt.statuses), gets)
				}
				if want := time.Duration(len(tt.statuses)-1) * serverPollInterval; elapsed != want {
					t.Fatalf("expected a wait of %s, got %s", want, elapsed)
				}
			})
		})
	}
}

func TestServerDataVolumeSelectorPlanningFollowsReorderedVolumeIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first, second, existing := core.UUID{30}, core.UUID{31}, core.UUID{32}
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	firstType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
	secondType := selector.WithID(testVolumeTypeSelector(types.StringValue("hdd")), types.StringValue((core.UUID{15}).String()))
	volumes := []map[string]attr.Value{
		{"kind": types.StringValue("new"), "id": types.StringValue(first.String()), "volume_type": firstType, "volume_size": types.Int64Value(20)},
		{"kind": types.StringValue("new"), "id": types.StringValue(second.String()), "volume_type": secondType, "volume_size": types.Int64Value(30)},
		{"kind": types.StringValue("existing"), "id": types.StringValue(existing.String())},
	}
	state.DataVolumes = dataVolumeList(t, volumes)
	planned := state
	planned.DataVolumes = dataVolumeList(t, []map[string]attr.Value{volumes[1], volumes[2], volumes[0]})
	schema := serverSchema(t)
	plan := planFor(t, schema, planned)
	resp := resource.ModifyPlanResponse{Plan: plan}
	(&ServerResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: stateFor(t, schema, state), Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &resp)
	if resp.Diagnostics.HasError() || len(resp.RequiresReplace) != 0 {
		t.Fatalf("reordered selectors: %v replacement: %v", resp.Diagnostics, resp.RequiresReplace)
	}
	var got ServerResourceModel
	if diags := resp.Plan.Get(ctx, &got); diags.HasError() || !got.DataVolumes.Equal(planned.DataVolumes) {
		t.Fatalf("reordering changed catalog identities: %v diagnostics: %v", got.DataVolumes, diags)
	}
}

func TestServerDataVolumeKnownSelectorRetypesWithoutCatalogLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newDataVolumeTestAPI(t)
	id, targetID := core.UUID{30}, core.UUID{15}
	api.attached[id.String()] = true
	api.volumes[id.String()].Status = blockstoragesdk.VolumeStatusInUse
	state := localDiskServerModel(t, "server")
	state.PowerState = types.StringValue("running")
	priorType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": priorType, "volume_size": types.Int64Value(20)}})
	targetType := selector.WithID(selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"disk_type": types.StringValue("ssd"), "speed": types.StringValue("20k")}), types.StringValue(targetID.String()))
	planned := state
	planned.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": targetType, "volume_size": types.Int64Value(20)}})
	schema := serverSchema(t)
	resp := resource.UpdateResponse{State: stateFor(t, schema, state)}
	api.resource.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, planned), State: resp.State}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got ServerResourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatal(diags)
	}
	var volumes []DataVolumeInputModel
	if diags := got.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() || len(volumes) != 1 || !volumes[0].VolumeType.Equal(targetType) || volumes[0].ID.ValueString() != id.String() || got.ID.ValueString() != state.ID.ValueString() || api.catalogReads != 0 || len(api.mutations) != 1 {
		t.Fatalf("retype state=%v catalog reads=%d mutations=%v diagnostics=%v", volumes, api.catalogReads, api.mutations, diags)
	}
}

// A null selector ID is an entry that the backend does not identify, so it
// never starts a retype, even when the criteria differ.
func TestServerDataVolumeUnidentifiedSelectorDoesNotRetype(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := core.UUID{30}
	priorType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringNull())
	changedType := selector.WithID(selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"disk_type": types.StringValue("ssd"), "speed": types.StringValue("20k")}), types.StringNull())
	for name, plannedType := range map[string]types.Object{"same criteria": priorType, "changed criteria": changedType} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			api := newDataVolumeTestAPI(t)
			api.attached[id.String()] = true
			state := localDiskServerModel(t, "server")
			state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": priorType, "volume_size": types.Int64Value(20)}})
			plan := state
			plan.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": types.StringValue(id.String()), "attached": types.BoolValue(true), "volume_type": plannedType, "volume_size": types.Int64Value(20)}})
			update, diags := api.resource.buildDataVolumeUpdatePlan(ctx, plan, state)
			if diags.HasError() || update != nil && len(update.updates) != 0 || api.catalogReads != 0 || len(api.mutations) != 0 {
				t.Fatalf("update=%#v catalog reads=%d mutations=%v diagnostics=%v", update, api.catalogReads, api.mutations, diags)
			}
		})
	}
}

func TestServerDataVolumePlanKeepsConfiguredSelectorCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	priorType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
	configuredType := selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"disk_type": types.StringValue("ssd")})
	plannedType := selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"disk_type": types.StringValue("ssd"), "name": types.StringUnknown(), "speed": types.StringUnknown(), "multiattach": types.BoolUnknown(), "encryption_key_size": types.StringUnknown(), "id": types.StringUnknown()})
	volume := func(volumeType types.Object) types.List {
		id := types.StringNull()
		if volumeType.Equal(priorType) {
			id = types.StringValue((core.UUID{30}).String())
		}
		return dataVolumeList(t, []map[string]attr.Value{{"kind": types.StringValue("new"), "id": id, "volume_type": volumeType, "volume_size": types.Int64Value(20)}})
	}
	for _, prior := range []types.List{volume(priorType), dataVolumeList(t, nil), types.ListNull(types.ObjectType{AttrTypes: dataVolumeAttributeTypes()})} {
		resp := resourceplanmodifier.ListResponse{PlanValue: volume(plannedType)}
		dataVolumeStatePlanModifier{}.PlanModifyList(ctx, resourceplanmodifier.ListRequest{ConfigValue: volume(configuredType), PlanValue: resp.PlanValue, StateValue: prior}, &resp)
		var got []DataVolumeInputModel
		if diags := resp.PlanValue.ElementsAs(ctx, &got, false); resp.Diagnostics.HasError() || diags.HasError() || len(got) != 1 {
			t.Fatalf("planned selectors: %v %v", resp.Diagnostics, diags)
		}
		want := selector.WithID(configuredType, types.StringUnknown())
		if !got[0].VolumeType.Equal(want) {
			t.Fatalf("unconfigured criteria entered the plan: got %v want %v", got[0].VolumeType, want)
		}
	}
}

type dataVolumeTestAPI struct {
	mu            sync.Mutex
	name          string
	resource      *ServerResource
	volumes       map[string]*blockstoragesdk.VolumeDetailSchema
	attached      map[string]bool
	mutations     []string
	creates       uint8
	attachPending bool
	failDelete    bool
	failPatch     bool
	catalogReads  int
	resizeIOPS    *int

	rejectVolumeOperation func(http.ResponseWriter, *http.Request) bool
	rejectRequest         func(http.ResponseWriter, *http.Request) bool
}

func newDataVolumeTestAPI(t *testing.T) *dataVolumeTestAPI {
	t.Helper()
	api := &dataVolumeTestAPI{name: "server", volumes: map[string]*blockstoragesdk.VolumeDetailSchema{}, attached: map[string]bool{}}
	api.volumes[(core.UUID{30}).String()] = &blockstoragesdk.VolumeDetailSchema{Id: core.UUID{30}, Size: 20, Status: blockstoragesdk.VolumeStatusAvailable, Zone: blockstoragesdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}}
	client := stubHTTPClient(t, func(w http.ResponseWriter, req *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		if strings.Contains(req.URL.Path, "/server/") && req.Header.Get("Project-ID") != (core.UUID{12}).String() {
			t.Fatal("request lost its project scope")
		}
		if req.Method != http.MethodGet {
			api.mutations = append(api.mutations, req.Method+" "+req.URL.Path)
		}
		if api.rejectRequest != nil && api.rejectRequest(w, req) {
			return
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, api.server())
		case req.Method == http.MethodPatch && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/" && api.failPatch:
			w.WriteHeader(http.StatusBadRequest)
		case req.Method == http.MethodPatch && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			var body serversdk.ServerPartialUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Name == nil {
				t.Fatalf("invalid server rename: %#v %v", body, err)
			}
			api.name = *body.Name
			writeJSON(t, w, api.server())
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volume-types/":
			api.catalogReads++
			if req.URL.Query().Get("zone_id") != testZoneID.String() {
				t.Fatal("new volume lookup lost the server zone")
			}
			typeID := testVolumeTypeID
			if req.URL.Query().Get("name") == "ssd2" {
				typeID = core.UUID{15}
			}
			writeJSON(t, w, map[string]any{"count": 1, "results": []any{map[string]any{"id": typeID, "name": req.URL.Query().Get("name"), "zone": map[string]any{"id": testZoneID, "name": "zone-a"}, "status": map[string]any{"value": "enabled"}, "state": map[string]any{"value": "up"}}}})
		case req.Method == http.MethodPost && (strings.HasSuffix(req.URL.Path, "/attach-volume/") || strings.HasSuffix(req.URL.Path, "/detach-volume/")):
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || api.volumes[body["volume_id"]] == nil {
				t.Fatalf("invalid volume attachment request: %v", body)
			}
			if api.rejectVolumeOperation != nil && api.rejectVolumeOperation(w, req) {
				return
			}
			attached := strings.HasSuffix(req.URL.Path, "/attach-volume/")
			if attached && api.attachPending {
				api.volumes[body["volume_id"]].Status = blockstoragesdk.VolumeStatusAttaching
				w.WriteHeader(http.StatusAccepted)
				return
			}
			api.attached[body["volume_id"]] = attached
			api.volumes[body["volume_id"]].Status = blockstoragesdk.VolumeStatusAvailable
			if attached {
				api.volumes[body["volume_id"]].Status = blockstoragesdk.VolumeStatusInUse
			}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && req.URL.Path == "/v2/block-storage/volumes/":
			var body blockstoragesdk.VolumeCreateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			source, err := body.CreateFrom.AsVolumeCreateEmptySchema()
			if err != nil || source.VolumeTypeId != testVolumeTypeID || source.Size != 20 || body.Name == "" || body.Iops != nil {
				t.Fatalf("invalid new volume request: %#v %v", body, err)
			}
			api.creates++
			id := core.UUID{40 + api.creates}
			volume := &blockstoragesdk.VolumeDetailSchema{Id: id, Size: source.Size, Status: blockstoragesdk.VolumeStatusAvailable, Zone: blockstoragesdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}}
			api.volumes[id.String()] = volume
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, volume)
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/retype/"):
			id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/block-storage/volumes/"), "/retype/")
			var body blockstoragesdk.VolumeRetypeSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.VolumeTypeId != (core.UUID{15}) {
				t.Fatal("unexpected type change")
			}
			api.volumes[id].CreateFrom.VolumeType = &blockstoragesdk.NestedVolumeTypeSchema{Id: body.VolumeTypeId, Name: "ssd2"}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/extend/"):
			id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/block-storage/volumes/"), "/extend/")
			var body blockstoragesdk.VolumeExtendSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if api.volumes[id] == nil || body.Size <= api.volumes[id].Size {
				t.Fatalf("invalid extend request: %s %#v", id, body)
			}
			api.volumes[id].Size = body.Size
			if api.resizeIOPS != nil {
				api.volumes[id].Iops = api.resizeIOPS
			}
			w.WriteHeader(http.StatusAccepted)
		case req.Method == http.MethodPatch && strings.HasPrefix(req.URL.Path, "/v2/block-storage/volumes/"):
			id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/block-storage/volumes/"), "/")
			var body map[string]int
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 1 || body["iops"] != 1200 {
				t.Fatalf("invalid IOPS update: %v", body)
			}
			value := body["iops"]
			api.volumes[id].Iops = &value
			writeJSON(t, w, api.volumes[id])
		case strings.HasPrefix(req.URL.Path, "/v2/block-storage/volumes/"):
			id := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/block-storage/volumes/"), "/")
			if req.Method == http.MethodDelete {
				if api.failDelete {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if api.attached[id] {
					t.Fatal("deleted an attached volume")
				}
				delete(api.volumes, id)
				w.WriteHeader(http.StatusAccepted)
				return
			}
			if volume := api.volumes[id]; volume != nil {
				writeJSON(t, w, volume)
			} else {
				http.NotFound(w, req)
			}
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})
	server, err := serversdk.NewClient("https://volume.test", serversdk.WithHTTPClient(client), serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	storage, err := blockstoragesdk.NewClient("https://volume.test", blockstoragesdk.WithHTTPClient(client), blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	api.resource = &ServerResource{client: server, blockStorageClient: storage, projectID: core.UUID{12}}
	return api
}

func (api *dataVolumeTestAPI) server() *serversdk.ServerDetailSchema {
	server := &serversdk.ServerDetailSchema{Id: testServerID, Name: api.name, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive, Flavor: serversdk.NestedFlavorSchema{Vcpus: 2, Ram: 4}, Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}, Zone: serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}}
	for _, id := range slices.Sorted(maps.Keys(api.attached)) {
		if api.attached[id] {
			volume := api.volumes[id]
			server.Volumes = append(server.Volumes, serversdk.NestedServerVolumeSchema{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: volume.Id, Size: volume.Size}})
		}
	}
	return server
}

func serverSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&ServerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func bootAttributeTypes() map[string]attr.Type {
	return bootResourceAttributeTypes()
}

func nameReference(value types.String) types.Object {
	return reference.Value(reference.Name, value)
}

func idReference(value types.String) types.Object {
	return reference.Value(reference.ID, value)
}

// subnetReference builds private_ips[*].subnet. A subnet without either
// criterion is an omitted subnet, as kind ip configures it.
func subnetReference(id, cidr types.String) types.Object {
	if id.IsNull() && cidr.IsNull() {
		return types.ObjectNull(reference.AttributeTypes(reference.ID, subnetCIDR))
	}
	return reference.Of(map[string]types.String{reference.ID: id, subnetCIDR: cidr})
}

func dataVolumeAttributeTypes() map[string]attr.Type {
	return dataVolumeResourceAttributeTypes()
}

// dataVolumeList builds a data_volumes list, defaulting the computed id so a
// test states only the attributes it is about.
func dataVolumeList(t *testing.T, values []map[string]attr.Value) types.List {
	t.Helper()
	complete := make([]map[string]attr.Value, 0, len(values))
	for _, value := range values {
		element := map[string]attr.Value{"id": types.StringNull()}
		maps.Copy(element, value)
		complete = append(complete, element)
	}
	return listValue(t, dataVolumeAttributeTypes(), complete)
}

func emptyServerResourceModel() ServerResourceModel {
	return ServerResourceModel{
		ServerModel: ServerModel{
			Zone:           types.ObjectNull(reference.AttributeTypes(reference.Name)),
			KeyPair:        types.ObjectNull(reference.AttributeTypes(reference.ID)),
			PlacementGroup: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		},
		Boot:           types.ObjectNull(bootAttributeTypes()),
		Flavor:         types.ObjectNull(flavorAttributeTypes()),
		DataVolumes:    types.ListNull(types.ObjectType{AttrTypes: dataVolumeAttributeTypes()}),
		PrivateIPs:     types.ListNull(types.ObjectType{AttrTypes: privateIPResourceAttributeTypes()}),
		ElasticIPs:     types.ListNull(types.ObjectType{AttrTypes: elasticIPResourceAttributeTypes()}),
		SecurityGroups: types.SetNull(reference.ObjectType(reference.ID)),
		DataVolumeIDs:  types.ListNull(types.StringType),
	}
}

func objectValue(t *testing.T, attributeTypes map[string]attr.Type, values map[string]attr.Value) types.Object {
	t.Helper()
	complete := make(map[string]attr.Value, len(attributeTypes))
	for name, attributeType := range attributeTypes {
		if value, ok := values[name]; ok {
			complete[name] = value
			continue
		}
		complete[name] = nullValueOf(t, attributeType)
	}
	value, diags := types.ObjectValue(attributeTypes, complete)
	if diags.HasError() {
		t.Fatalf("create object value: %v", diags)
	}
	return value
}

func nullValueOf(t *testing.T, attributeType attr.Type) attr.Value {
	t.Helper()
	switch attributeType := attributeType.(type) {
	case types.ObjectType:
		return types.ObjectNull(attributeType.AttrTypes)
	case basetypes.StringType:
		return types.StringNull()
	case basetypes.Int64Type:
		return types.Int64Null()
	case basetypes.BoolType:
		return types.BoolNull()
	default:
		t.Fatalf("no null value for attribute type %s", attributeType)
		return nil
	}
}

func listValue(t *testing.T, attributeTypes map[string]attr.Type, values []map[string]attr.Value) types.List {
	t.Helper()
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, objectValue(t, attributeTypes, value))
	}
	result, diags := types.ListValue(types.ObjectType{AttrTypes: attributeTypes}, elements)
	if diags.HasError() {
		t.Fatalf("create list value: %v", diags)
	}
	return result
}

func privateIPValues(id core.UUID, deleteOnTermination bool) map[string]attr.Value {
	return map[string]attr.Value{
		"kind": types.StringValue("ip"), "id": types.StringValue(id.String()), "subnet": subnetReference(types.StringNull(), types.StringNull()), "delete_on_termination": types.BoolValue(deleteOnTermination),
		"ip_address": types.StringNull(), "mac_address": types.StringNull(),
	}
}

func elasticIPValues(id core.UUID, deleteOnTermination bool) map[string]attr.Value {
	return map[string]attr.Value{
		"kind": types.StringValue("existing"), "id": types.StringValue(id.String()), "enable_ipv4": types.BoolNull(),
		"enable_ipv6": types.BoolNull(), "delete_on_termination": types.BoolValue(deleteOnTermination),
		"ip_address": types.StringNull(), "ipv6_address": types.StringNull(), "status": types.StringNull(),
	}
}

// attachmentWaiterResource serves handler over a stub transport so the waiter
// tests can run on the fake clock; see stubServerClient.
func attachmentWaiterResource(t *testing.T, handler http.HandlerFunc) *ServerResource {
	t.Helper()
	return &ServerResource{client: stubServerClient(t, handler), projectID: core.UUID{12}}
}

// stubHTTPClient answers requests from handler through an http.RoundTripper
// instead of a real listener. A testing/synctest bubble only advances its fake
// clock once every goroutine inside it is durably blocked, and a goroutine
// waiting on a real socket never is. A waiter test served by httptest.NewServer
// would therefore hang instead of stepping through its poll interval.
func stubHTTPClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		response := recorder.Result()
		response.Request = req
		return response, nil
	})}
}

func stubServerClient(t *testing.T, handler http.HandlerFunc) *serversdk.Client {
	t.Helper()
	client, err := serversdk.NewClient(
		"https://server.test",
		serversdk.WithHTTPClient(stubHTTPClient(t, handler)),
		serversdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create server client: %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// attachmentList builds a plan-shaped list of n attachments; only its length
// matters to the waiter.
func attachmentList(t *testing.T, n int) types.List {
	t.Helper()
	values := make([]map[string]attr.Value, 0, n)
	for i := range n {
		values = append(values, privateIPValues(core.UUID{byte(i + 1)}, true))
	}
	return listValue(t, privateIPResourceAttributeTypes(), values)
}

func customFlavorValue(t *testing.T, vcpus, ram int64) types.Object {
	t.Helper()
	return objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(vcpus), "ram": types.Int64Value(ram),
	})
}

func localDiskServerModel(t *testing.T, name string) ServerResourceModel {
	t.Helper()
	model := emptyServerResourceModel()
	model.ID = types.StringValue(testServerID.String())
	model.Name = types.StringValue(name)
	model.Description = types.StringValue("")
	model.Zone = nameReference(types.StringValue("zone-a"))
	model.Quantity = types.Int64Value(1)
	model.Flavor = customFlavorValue(t, 2, 4)
	model.Boot = objectValue(t, bootAttributeTypes(), map[string]attr.Value{
		"boot_type": types.StringValue("local_disk"), "image": selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String())),
		"custom_image": idReference(types.StringNull()), "volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringNull()),
		"volume_size": types.Int64Null(), "iops": types.Int64Null(),
	})
	return model
}

func planFor(t *testing.T, resourceSchema resourceschema.Schema, model ServerResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: resourceSchema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func stateFor(t *testing.T, resourceSchema resourceschema.Schema, model ServerResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set prior state: %v", diags)
	}
	return state
}

func validateServerConfig(t *testing.T, model ServerResourceModel) diag.Diagnostics {
	t.Helper()
	resourceSchema := serverSchema(t)
	// tfsdk.Config exposes no setter, so build the value through a state and
	// reuse its raw representation as the configuration under validation.
	state := tfsdk.State{Schema: resourceSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set configuration: %v", diags)
	}
	var response resource.ValidateConfigResponse
	(&ServerResource{}).ValidateConfig(
		context.Background(),
		resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: resourceSchema, Raw: state.Raw}},
		&response,
	)
	return response.Diagnostics
}

func validServerConfig(t *testing.T) ServerResourceModel {
	t.Helper()
	config := emptyServerResourceModel()
	config.Name = types.StringValue("application")
	config.Zone = nameReference(types.StringValue("zone-a"))
	config.Flavor = objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("custom"), "name": types.StringNull(), "family": types.StringValue("basic"),
		"vcpus": types.Int64Value(2), "ram": types.Int64Value(2),
	})
	config.Boot = bootConfig(t, map[string]attr.Value{
		"boot_type": types.StringValue("image"), "image": selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String())),
		"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String())), "volume_size": types.Int64Value(30),
	})
	config.PrivateIPs = listValue(t, privateIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("subnet"), "id": types.StringNull(),
		"subnet":                subnetReference(types.StringValue(testSubnetID.String()), types.StringNull()),
		"delete_on_termination": types.BoolValue(true),
		"ip_address":            types.StringNull(), "mac_address": types.StringNull(),
	}})
	config.ElasticIPs = listValue(t, elasticIPResourceAttributeTypes(), []map[string]attr.Value{{
		"kind": types.StringValue("new"), "id": types.StringNull(),
		"enable_ipv4": types.BoolValue(true), "enable_ipv6": types.BoolNull(),
		"delete_on_termination": types.BoolValue(true), "ip_address": types.StringNull(),
		"ipv6_address": types.StringNull(), "status": types.StringNull(),
	}})
	config.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{
		"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String())), "volume_size": types.Int64Value(50), "iops": types.Int64Null(),
	}})
	return config
}

// bootConfig fills the attributes a boot block leaves unset with null so each
// test only states the values it cares about.
func bootConfig(t *testing.T, values map[string]attr.Value) types.Object {
	t.Helper()
	complete := map[string]attr.Value{
		"boot_type": types.StringNull(), "image": testImageSelector(types.StringNull()), "custom_image": idReference(types.StringNull()),
		"volume": idReference(types.StringNull()), "volume_type": testVolumeTypeSelector(types.StringNull()),
		"volume_size": types.Int64Null(), "iops": types.Int64Null(),
	}
	maps.Copy(complete, values)
	return objectValue(t, bootAttributeTypes(), complete)
}

func predefinedFlavorConfig(t *testing.T, name types.String) types.Object {
	t.Helper()
	return objectValue(t, flavorAttributeTypes(), map[string]attr.Value{
		"kind": types.StringValue("predefined"), "name": name, "family": types.StringNull(),
		"vcpus": types.Int64Null(), "ram": types.Int64Null(),
	})
}

// writeUnsupportedStatusError reproduces the error the backend returns when a
// server reports a lifecycle status this SDK cannot decode, such as a build
// failure.
func writeUnsupportedStatusError(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	body := `{"errors":[{"location":"response.status","code":"enum",` +
		`"message":"Input should be '', 'active', 'building'"}],"request_id":"test"}`
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write unsupported status response: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// writeProviderError writes the refusal a busy upstream returns. The backend
// pairs the code with a 4xx status, which is why the replay cannot be gated on
// the status class alone.
func writeProviderError(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	body := `{"errors":[{"location":"","code":"provider_error",` +
		`"message":"The service cannot handle the request right now."}],"request_id":"test"}`
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write provider error: %v", err)
	}
}

func TestServerBootAndDataVolumesUseTypedSelectors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	image := objectValue(t, serverselector.ImageAttributeTypes(), map[string]attr.Value{"os": types.StringValue("Ubuntu"), "version": types.StringValue("22.04"), "type": types.StringValue("server")})
	volumeType := objectValue(t, blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"disk_type": types.StringValue("ssd"), "speed": types.StringValue("10k"), "multiattach": types.BoolValue(false), "encryption_key_size": types.StringValue("0")})
	resolveImage := func(_ context.Context, filter serverlookup.ImageFilter) (serversdk.ImageSchema, error) {
		if filter.Name != nil || *filter.OS != "Ubuntu" || *filter.Version != "22.04" || *filter.Type != serversdk.ImageTypeServer {
			t.Fatalf("wrong image criteria: %#v", filter)
		}
		return serversdk.ImageSchema{Id: testImageID}, nil
	}
	typeCalls := 0
	resolveType := func(_ context.Context, req blockstoragelookup.VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error) {
		typeCalls++
		f := req.Filter
		if f.Name != nil || *f.DiskType != blockstoragesdk.VolumeTypeDiskTypeSsd || string(*f.Speed) != "10k" || f.Multiattach == nil || *f.Multiattach || *f.EncryptionKeySize != blockstoragesdk.VolumeTypeEncryptKeySizeN0 || req.ZoneID == nil || *req.ZoneID != testZoneID || req.RequestedSize != 30 {
			t.Fatalf("wrong volume type criteria: %#v", req)
		}
		return blockstoragesdk.VolumeTypeSchema{Id: testVolumeTypeID}, nil
	}
	boot := BootInputModel{BootType: types.StringValue("image"), Image: image, VolumeType: volumeType, VolumeSize: types.Int64Value(30)}
	body, diags := buildBoot(ctx, boot, &testZoneID, resolveImage, resolveType)
	if diags.HasError() {
		t.Fatalf("boot diagnostics: %v", diags)
	}
	fromImage, err := body.AsServerBootFromImage()
	if err != nil || fromImage.ImageId != testImageID || fromImage.VolumeTypeId != testVolumeTypeID {
		t.Fatalf("wrong boot body: %#v, %v", fromImage, err)
	}
	bootObject := bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": image, "volume_type": volumeType, "volume_size": types.Int64Value(30)})
	rebuilt, id, diags := buildServerRebuildBody(ctx, bootObject, core.UUID{12}, resolveImage)
	if diags.HasError() || id != testImageID || rebuilt.ImageId == nil || *rebuilt.ImageId != testImageID {
		t.Fatalf("wrong rebuild: %#v, %v", rebuilt, diags)
	}
	var create serversdk.ServerCreateSchema
	buildDataVolumes(ctx, dataVolumeList(t, []map[string]attr.Value{{"volume_type": volumeType, "volume_size": types.Int64Value(30)}}), &create, &testZoneID, resolveType, &diags)
	if diags.HasError() || typeCalls != 2 {
		t.Fatalf("data volume criteria: calls=%d, %v", typeCalls, diags)
	}
	prior := ServerResourceModel{Boot: bootObject}
	changed := prior
	changedImage := objectValue(t, serverselector.ImageAttributeTypes(), map[string]attr.Value{"os": types.StringValue("Ubuntu"), "version": types.StringValue("24.04"), "type": types.StringValue("server")})
	// ModifyPlan leaves the ID of changed criteria unknown until apply.
	changed.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": selector.WithID(changedImage, types.StringUnknown()), "volume_type": volumeType, "volume_size": types.Int64Value(30)})
	needsRebuild, diags := bootSourceChanged(ctx, changed, prior)
	if diags.HasError() || !needsRebuild {
		t.Fatalf("version change missed: %v", diags)
	}
}

func TestServerRefreshKeepsSelectorWhenImageOriginIsNotReported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prior := objectValue(t, serverselector.ImageAttributeTypes(), map[string]attr.Value{"name": types.StringValue("Ubuntu"), "type": types.StringValue("server")})
	state := emptyServerResourceModel()
	state.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("local_disk"), "image": prior})
	// A nil client proves an unreported origin does not trigger an ID-zero lookup.
	diags := (&ServerResource{}).refreshServerBootState(ctx, &serversdk.ServerDetailSchema{}, &state)
	if diags.HasError() {
		t.Fatal(diags)
	}
	var boot BootInputModel
	if diags := state.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatal(diags)
	}
	if !boot.Image.Equal(prior) {
		t.Fatalf("unreported image changed selector: %v", boot.Image)
	}
}

func TestServerSelectorModifyPlanUsesIdentity(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"image", "boot type", "data type"} {
		for _, scenario := range []string{"unchanged", "same ID", "current among many", "different ID", "unknown"} {
			t.Run(field+"/"+scenario, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				currentID := testVolumeTypeID
				isImage := field == "image"
				if isImage {
					currentID = testImageID
				}
				calls := 0
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls++
					if scenario == "unchanged" || scenario == "unknown" {
						t.Errorf("unexpected catalog call: %s", req.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					id := currentID
					if scenario == "different ID" {
						id = core.UUID{6}
					}
					if isImage {
						entries := []serversdk.ImageSchema{{Id: id, Name: "ubuntu", Os: new("Ubuntu"), Version: new("22.04")}}
						if scenario == "current among many" {
							entries = append(entries, serversdk.ImageSchema{Id: core.UUID{6}, Name: "other", Os: new("Ubuntu"), Version: new("22.04")})
						}
						if req.URL.Path != "/v2/server/images/" {
							t.Errorf("unexpected path: %s", req.URL)
						}
						if err := json.NewEncoder(w).Encode(map[string]any{"count": len(entries), "results": entries}); err != nil {
							t.Error(err)
						}
					} else {
						entries := []blockstoragesdk.VolumeTypeSchema{{Id: id, Name: "ssd", DiskType: blockstoragesdk.LabeledVolumeTypeDiskType{Value: blockstoragesdk.VolumeTypeDiskTypeSsd}, Status: blockstoragesdk.LabeledVolumeTypeStatus{Value: blockstoragesdk.VolumeTypeStatusDisabled}}}
						if scenario == "current among many" {
							entries = append(entries, blockstoragesdk.VolumeTypeSchema{Id: core.UUID{6}, Name: "other", DiskType: blockstoragesdk.LabeledVolumeTypeDiskType{Value: blockstoragesdk.VolumeTypeDiskTypeSsd}})
						}
						if req.URL.Path != "/v2/block-storage/volume-types/" {
							t.Errorf("unexpected path: %s", req.URL)
						}
						if err := json.NewEncoder(w).Encode(map[string]any{"count": len(entries), "results": entries}); err != nil {
							t.Error(err)
						}
					}
				}))
				defer api.Close()
				serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
				if err != nil {
					t.Fatal(err)
				}
				blockClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
				if err != nil {
					t.Fatal(err)
				}
				r := &ServerResource{client: serverClient, blockStorageClient: blockClient, projectID: core.UUID{12}}
				state := emptyServerResourceModel()
				state.ID = types.StringValue(testServerID.String())
				image := selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String()))
				volumeType := selector.WithID(testVolumeTypeSelector(types.StringValue("ssd")), types.StringValue(testVolumeTypeID.String()))
				state.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": image, "volume_type": volumeType})
				state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"volume_type": volumeType, "volume_size": types.Int64Value(30)}})
				p := path.Root("boot").AtName("volume_type")
				prior := volumeType
				attrs := blockstorageselector.VolumeTypeAttributeTypes()
				changed := selectorTestCriteria(attrs, map[string]attr.Value{"disk_type": types.StringValue("ssd")})
				if isImage {
					p = path.Root("boot").AtName("image")
					prior = image
					attrs = serverselector.ImageAttributeTypes()
					changed = selectorTestCriteria(attrs, map[string]attr.Value{"os": types.StringValue("Ubuntu"), "version": types.StringValue("22.04")})
				}
				if field == "data type" {
					p = path.Root("data_volumes").AtListIndex(0).AtName("volume_type")
				}
				switch scenario {
				case "unchanged":
					changed = prior
				case "unknown":
					changed = types.ObjectUnknown(attrs)
				}
				changed = selector.WithID(changed, types.StringUnknown())
				schema := serverSchema(t)
				plan := planFor(t, schema, state)
				if diags := plan.SetAttribute(ctx, p, changed); diags.HasError() {
					t.Fatal(diags)
				}
				config := tfsdk.Config{Schema: schema, Raw: plan.Raw}
				configPlan := plan
				if diags := configPlan.SetAttribute(ctx, path.Root("security_groups"), types.SetUnknown(reference.ObjectType(reference.ID))); diags.HasError() {
					t.Fatal(diags)
				}
				config.Raw = configPlan.Raw
				resp := resource.ModifyPlanResponse{Plan: plan}
				r.ModifyPlan(ctx, resource.ModifyPlanRequest{Config: config, State: stateFor(t, schema, state), Plan: plan}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				different := scenario == "different ID" || scenario == "unknown"
				if resp.RequiresReplace.Contains(p) != (field == "boot type" && different) {
					t.Fatalf("replacement paths: %v", resp.RequiresReplace)
				}
				var got types.Object
				if diags := resp.Plan.GetAttribute(ctx, p, &got); diags.HasError() {
					t.Fatal(diags)
				}
				gotID, known := selector.ID(got)
				if known == different || known && gotID != currentID {
					t.Fatalf("plan selector ID: %v", got)
				}
				expectedCalls := 1
				if scenario == "unchanged" || scenario == "unknown" {
					expectedCalls = 0
				}
				if calls != expectedCalls {
					t.Fatalf("catalog calls=%d want %d", calls, expectedCalls)
				}
			})
		}
	}
}

func TestServerUpdateResolvedCurrentImageSkipsRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	catalogCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/images/":
			catalogCalls++
			writeJSON(t, w, map[string]any{"count": 1, "results": []serversdk.ImageSchema{{Id: testImageID, Name: "ubuntu", Type: new(serversdk.ImageTypeServer)}}})
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{Id: testServerID, Name: "server", Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4}, Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}, Zone: serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive})
		default:
			t.Errorf("unexpected catalog request or mutation: %s %s", req.Method, req.URL)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	prior := localDiskServerModel(t, "server")
	plan := prior
	image := selectorTestCriteria(serverselector.ImageAttributeTypes(), map[string]attr.Value{"type": types.StringValue("server"), "id": types.StringUnknown()})
	plan.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("local_disk"), "image": image})
	schema := serverSchema(t)
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(ctx, resource.UpdateRequest{State: stateFor(t, schema, prior), Plan: planFor(t, schema, plan)}, &resp)
	if resp.Diagnostics.HasError() || catalogCalls != 1 {
		t.Fatalf("diagnostics=%v catalog=%d", resp.Diagnostics, catalogCalls)
	}
	var got ServerResourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatal(diags)
	}
	var boot BootInputModel
	if diags := got.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatal(diags)
	}
	if !boot.Image.Equal(selector.WithID(image, types.StringValue(testImageID.String()))) {
		t.Fatalf("Update lost the resolved identity or changed criteria: %v", boot.Image)
	}
}

func TestServerSelectorFallbackUsesConfiguredDataVolumeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	firstID, secondID := core.UUID{9}, core.UUID{10}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v2/server/servers/" + testServerID.String() + "/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{Id: testServerID, Volumes: []serversdk.NestedServerVolumeSchema{{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: secondID}}, {MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: firstID}}}})
		case "/v2/block-storage/volumes/" + firstID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: firstID, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}})
		default:
			t.Errorf("wrong volume or catalog requested during identity fallback: %s", req.URL)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	blockClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	state.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"id": types.StringValue(firstID.String()), "volume_type": testVolumeTypeSelector(types.StringValue("ssd"))}, {"id": types.StringValue(secondID.String()), "volume_type": testVolumeTypeSelector(types.StringValue("hdd"))}})
	state.DataVolumeIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue(secondID.String()), types.StringValue(firstID.String())})
	r := &ServerResource{client: serverClient, blockStorageClient: blockClient, projectID: core.UUID{12}}
	id, err := r.currentSelectorID(ctx, state, "volume_type", 0)
	if err != nil || id != testVolumeTypeID {
		t.Fatalf("fallback followed attachment order instead of configured identity: %s %v", id, err)
	}
}

// State written before selectors had IDs must keep planning when the backend
// no longer identifies the image, such as after the image left the catalog.
func TestServerPlanKeepsUnidentifiedImageFromEarlierState(t *testing.T) {
	t.Parallel()
	rootID := core.UUID{11}
	for _, tc := range []struct {
		name     string
		bootType string
		volumes  []serversdk.NestedServerVolumeSchema
	}{
		{"local disk without root volume", "local_disk", nil},
		{"root volume without image origin", "image", []serversdk.NestedServerVolumeSchema{{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: rootID}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/v2/server/servers/" + testServerID.String() + "/":
					writeJSON(t, w, &serversdk.ServerDetailSchema{Id: testServerID, Volumes: tc.volumes})
				case "/v2/block-storage/volumes/" + rootID.String() + "/":
					writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: rootID})
				default:
					t.Errorf("unexpected request during identity fallback: %s", req.URL)
					http.NotFound(w, req)
				}
			}))
			defer api.Close()
			serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
			if err != nil {
				t.Fatal(err)
			}
			blockClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
			if err != nil {
				t.Fatal(err)
			}
			r := &ServerResource{client: serverClient, blockStorageClient: blockClient, projectID: core.UUID{12}}
			state := emptyServerResourceModel()
			state.ID = types.StringValue(testServerID.String())
			state.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue(tc.bootType), "image": testImageSelector(types.StringValue("ubuntu"))})
			schema := serverSchema(t)
			plan := planFor(t, schema, state)
			p := path.Root("boot").AtName("image")
			if diags := plan.SetAttribute(ctx, p, selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringUnknown())); diags.HasError() {
				t.Fatal(diags)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}, State: stateFor(t, schema, state), Plan: plan}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unchanged criteria failed the plan: %v", resp.Diagnostics)
			}
			if len(resp.RequiresReplace) > 0 {
				t.Fatalf("unchanged criteria planned replacement: %v", resp.RequiresReplace)
			}
			var planned ServerResourceModel
			if diags := resp.Plan.Get(ctx, &planned); diags.HasError() {
				t.Fatal(diags)
			}
			rebuild, diags := bootSourceChanged(ctx, planned, state)
			if diags.HasError() || rebuild {
				t.Fatalf("unchanged criteria planned a rebuild: %v %v", rebuild, diags)
			}
		})
	}
}

// A data volume that the backend returns without its volume type origin has
// no identity, so the selector keeps the earlier state instead of failing.
func TestServerSelectorFallbackReportsDataVolumeWithoutVolumeType(t *testing.T) {
	t.Parallel()
	volumeID := core.UUID{9}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v2/server/servers/" + testServerID.String() + "/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{Id: testServerID, Volumes: []serversdk.NestedServerVolumeSchema{{MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: volumeID}}}})
		case "/v2/block-storage/volumes/" + volumeID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: volumeID})
		default:
			t.Errorf("unexpected request during identity fallback: %s", req.URL)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	blockClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	state := emptyServerResourceModel()
	state.ID = types.StringValue(testServerID.String())
	r := &ServerResource{client: serverClient, blockStorageClient: blockClient, projectID: core.UUID{12}}
	if _, err := r.currentSelectorID(context.Background(), state, "volume_type", 0); !errors.Is(err, selector.ErrNoIdentity) {
		t.Fatalf("missing volume type origin did not report ErrNoIdentity: %v", err)
	}
}

func TestServerFailedPatchDoesNotLosePendingRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targetID := core.UUID{6}
	catalogCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/images/":
			catalogCalls++
			writeJSON(t, w, map[string]any{"count": 1, "results": []serversdk.ImageSchema{{Id: targetID, Name: "target", Type: new(serversdk.ImageTypeServer)}}})
		case req.Method == http.MethodPatch:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"patch failed"}`))
		case req.Method == http.MethodGet && req.URL.Path == "/v2/server/servers/"+testServerID.String()+"/":
			writeJSON(t, w, &serversdk.ServerDetailSchema{Id: testServerID, Name: "before", Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4}, Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}, Zone: serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive})
		default:
			t.Errorf("unexpected request before successful patch: %s %s", req.Method, req.URL)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	client, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{client: client, projectID: core.UUID{12}}
	prior := localDiskServerModel(t, "before")
	plan := prior
	plan.Name = types.StringValue("after")
	target := selectorTestCriteria(serverselector.ImageAttributeTypes(), map[string]attr.Value{"name": types.StringValue("target"), "type": types.StringValue("server"), "id": types.StringUnknown()})
	plan.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("local_disk"), "image": target})
	schema := serverSchema(t)
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(ctx, resource.UpdateRequest{State: stateFor(t, schema, prior), Plan: planFor(t, schema, plan)}, &resp)
	if !resp.Diagnostics.HasError() || catalogCalls != 1 {
		t.Fatalf("diagnostics=%v catalog=%d", resp.Diagnostics, catalogCalls)
	}
	var result ServerResourceModel
	if diags := resp.State.Get(ctx, &result); diags.HasError() {
		t.Fatal(diags)
	}
	var boot BootInputModel
	if diags := result.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatal(diags)
	}
	id, known := selector.ID(boot.Image)
	if !known || id != testImageID || boot.Image.Attributes()["name"].(types.String).ValueString() != "ubuntu" {
		t.Fatalf("failed apply retained target criteria with the old image: %v", boot.Image)
	}
	read := resource.ReadResponse{State: resp.State}
	r.Read(ctx, resource.ReadRequest{State: resp.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	decision, _, diags := selector.Decide(ctx, target, boot.Image, "boot.image", func(context.Context) ([]core.UUID, error) { return []core.UUID{targetID}, nil }, func(context.Context) (core.UUID, error) { t.Fatal("unexpected fallback"); return core.UUID{}, nil })
	if diags.HasError() || decision != selector.Different {
		t.Fatalf("next plan lost rebuild: %v %v", decision, diags)
	}
}

func TestServerCreateReadAndKnownIdentityUpdateDoNotRefreshCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rootID, dataID := core.UUID{9}, core.UUID{10}
	created := false
	catalogCalls := 0
	detail := &serversdk.ServerDetailSchema{Id: testServerID, Name: "server", Flavor: serversdk.NestedFlavorSchema{Id: testFlavorID, Name: "custom", Vcpus: 2, Ram: 4}, Image: &serversdk.NestedImageSchema{Id: testImageID, Name: "ubuntu", Os: "Different metadata"}, Zone: serversdk.NestedZoneSchema{Id: testZoneID, Name: "zone-a"}, PowerState: serversdk.ServerPowerStateRunning, Status: serversdk.ServerStatusActive, Volumes: []serversdk.NestedServerVolumeSchema{{MountAs: "root", Volume: serversdk.NestedVolumeSchema{Id: rootID, Size: 30, Status: serversdk.VolumeStatusInUse}}, {MountAs: "data", Volume: serversdk.NestedVolumeSchema{Id: dataID, Size: 50, Status: serversdk.VolumeStatusInUse}}}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		catalog := strings.Contains(req.URL.Path, "images") || strings.Contains(req.URL.Path, "volume-types")
		if catalog {
			catalogCalls++
			if created {
				t.Errorf("catalog request after create: %s", req.URL)
			}
		}
		switch req.URL.Path {
		case "/v2/server/images/":
			writeJSON(t, w, map[string]any{"count": 1, "results": []serversdk.ImageSchema{{Id: testImageID, Name: "ubuntu", Type: new(serversdk.ImageTypeServer)}}})
		case "/v2/block-storage/volume-types/":
			writeJSON(t, w, map[string]any{"count": 1, "results": []blockstoragesdk.VolumeTypeSchema{{Id: testVolumeTypeID, Name: "ssd", EncryptionKeySize: blockstoragesdk.LabeledVolumeTypeEncryptKeySize{Value: blockstoragesdk.VolumeTypeEncryptKeySizeN0}, Status: blockstoragesdk.LabeledVolumeTypeStatus{Value: blockstoragesdk.VolumeTypeStatusEnabled}, State: blockstoragesdk.LabeledVolumeTypeState{Value: blockstoragesdk.VolumeTypeStateUp}, MaxVolumeSize: 100}}})
		case "/v2/server/servers/":
			if req.Method != http.MethodPost {
				t.Errorf("unexpected server create method: %s", req.Method)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]any{"server_ids": []core.UUID{testServerID}})
		case "/v2/server/servers/" + testServerID.String() + "/":
			if req.Method != http.MethodGet {
				t.Errorf("unexpected mutation: %s", req.Method)
			}
			writeJSON(t, w, detail)
		case "/v2/block-storage/volumes/" + rootID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: rootID, Size: 30, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{Image: &blockstoragesdk.NestedImageSchema{Id: testImageID, Name: "ubuntu"}, VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd", DiskType: blockstoragesdk.VolumeTypeDiskTypeHdd}}})
		case "/v2/block-storage/volumes/" + dataID.String() + "/":
			writeJSON(t, w, &blockstoragesdk.VolumeDetailSchema{Id: dataID, Size: 50, CreateFrom: blockstoragesdk.NestedVolumeOriginSchema{VolumeType: &blockstoragesdk.NestedVolumeTypeSchema{Id: testVolumeTypeID, Name: "ssd"}}})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			http.NotFound(w, req)
		}
	}))
	defer api.Close()
	serverClient, err := serversdk.NewClient(api.URL, serversdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	blockClient, err := blockstoragesdk.NewClient(api.URL, blockstoragesdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	r := &ServerResource{client: serverClient, blockStorageClient: blockClient, projectID: core.UUID{12}}
	image := selectorTestCriteria(serverselector.ImageAttributeTypes(), map[string]attr.Value{"type": types.StringValue("server"), "id": types.StringUnknown()})
	volumeType := selectorTestCriteria(blockstorageselector.VolumeTypeAttributeTypes(), map[string]attr.Value{"name": types.StringValue("ssd"), "encryption_key_size": types.StringValue("0"), "id": types.StringUnknown()})
	plan := emptyServerResourceModel()
	plan.Name = types.StringValue("server")
	plan.Flavor = customFlavorValue(t, 2, 4)
	plan.Boot = bootConfig(t, map[string]attr.Value{"boot_type": types.StringValue("image"), "image": image, "volume_type": volumeType, "volume_size": types.Int64Value(30)})
	plan.DataVolumes = dataVolumeList(t, []map[string]attr.Value{{"volume_type": volumeType, "volume_size": types.Int64Value(50)}})
	schema := serverSchema(t)
	create := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: planFor(t, schema, plan)}, &create)
	if create.Diagnostics.HasError() {
		t.Fatal(create.Diagnostics)
	}
	if catalogCalls != 3 {
		t.Fatalf("expected image, boot type, and data type resolution, got %d calls", catalogCalls)
	}
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var got ServerResourceModel
	if diags := read.State.Get(ctx, &got); diags.HasError() {
		t.Fatal(diags)
	}
	var boot BootInputModel
	if diags := got.Boot.As(ctx, &boot, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatal(diags)
	}
	if !boot.Image.Equal(selector.WithID(image, types.StringValue(testImageID.String()))) || !boot.VolumeType.Equal(selector.WithID(volumeType, types.StringValue(testVolumeTypeID.String()))) {
		t.Fatalf("create or Read lost identity and criteria: %v", boot)
	}
	// A known plan ID is authoritative even when the text selects by another field.
	boot.Image = selector.WithID(testImageSelector(types.StringValue("ubuntu")), types.StringValue(testImageID.String()))
	got.Boot = objectValue(t, bootResourceAttributeTypes(), map[string]attr.Value{"boot_type": boot.BootType, "image": boot.Image, "volume_type": boot.VolumeType, "volume_size": boot.VolumeSize, "iops": boot.IOPS, "delete_on_termination": boot.DeleteOnTermination})
	update := resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: planFor(t, schema, got), State: read.State}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	if catalogCalls != 3 {
		t.Fatal("Read or known-identity Update called catalog")
	}
	var volumes []DataVolumeInputModel
	if diags := got.DataVolumes.ElementsAs(ctx, &volumes, false); diags.HasError() {
		t.Fatal(diags)
	}
	if len(volumes) != 1 || !volumes[0].VolumeType.Equal(selector.WithID(volumeType, types.StringValue(testVolumeTypeID.String()))) {
		t.Fatal("data selector lost resolved ID or criteria")
	}
}
