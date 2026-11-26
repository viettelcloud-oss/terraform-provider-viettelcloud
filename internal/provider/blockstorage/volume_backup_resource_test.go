package blockstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

// volumeBackupTestVolumePath is the GET path of the source volume of
// testVolumeBackup.
var volumeBackupTestVolumePath = "/v2/block-storage/volumes/" + core.NullableToPtr(testVolumeBackup().Volume).Id.String() + "/"

func volumeBackupResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeBackupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	return response.Schema
}

// nullVolumeBackupTimeouts is an unconfigured timeouts value. A zero
// timeouts.Value has no attribute types and does not match the schema.
func nullVolumeBackupTimeouts() timeouts.Value {
	return timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{"create": types.StringType, "update": types.StringType})}
}

// volumeBackupTimeoutsValue configures timeouts. An empty argument leaves that
// key unconfigured.
func volumeBackupTimeoutsValue(create, update string) timeouts.Value {
	value := func(s string) attr.Value {
		if s == "" {
			return types.StringNull()
		}
		return types.StringValue(s)
	}
	return timeouts.Value{Object: types.ObjectValueMust(
		map[string]attr.Type{"create": types.StringType, "update": types.StringType},
		map[string]attr.Value{"create": value(create), "update": value(update)},
	)}
}

func volumeBackupPlan(t *testing.T, model VolumeBackupResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: volumeBackupResourceSchema(t)}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	return plan
}

func volumeBackupState(t *testing.T, model VolumeBackupResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: volumeBackupResourceSchema(t)}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func TestVolumeBackupResourceSchema(t *testing.T) {
	r := NewVolumeBackupResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	attrs := resp.Schema.Attributes
	if !attrs["name"].(schema.StringAttribute).Required {
		t.Fatal("name must be required")
	}
	assertVolumeIDReference(t, attrs["volume"])
	description := attrs["description"].(schema.StringAttribute)
	if !description.Optional || !description.Computed {
		t.Fatal("description must retain the effective value")
	}
	// A restore_volume change updates in place, and omission stays null.
	restoreVolume := referenceAttribute(t, attrs["restore_volume"], reference.ID)
	if !restoreVolume.Optional || restoreVolume.Computed || restoreVolume.Required || len(restoreVolume.PlanModifiers) != 0 {
		t.Fatalf("restore_volume must be Optional without plan modifiers: %#v", restoreVolume)
	}
	// A delete does not depend on the size of the data, so it has no key.
	timeoutsAttribute := attrs["timeouts"].(schema.SingleNestedAttribute)
	if !timeoutsAttribute.Optional || timeoutsAttribute.Computed || len(timeoutsAttribute.Attributes) != 2 || timeoutsAttribute.Attributes["create"] == nil || timeoutsAttribute.Attributes["update"] == nil {
		t.Fatalf("timeouts must be Optional with create and update only: %#v", timeoutsAttribute)
	}
	state := tfsdk.State{Schema: resp.Schema}
	if diags := state.Set(context.Background(), &VolumeBackupResourceModel{Volume: nullVolumeReference(), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}); diags.HasError() {
		t.Fatalf("model/schema mismatch: %v", diags)
	}
}

func TestVolumeBackupImportState(t *testing.T) {
	var schemaResponse resource.SchemaResponse
	r := &VolumeBackupResource{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	if diags := response.State.Set(context.Background(), &VolumeBackupResourceModel{Volume: nullVolumeReference(), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}); diags.HasError() {
		t.Fatal(diags)
	}
	id := testVolumeBackup().Id.String()
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &response)
	var imported types.String
	response.Diagnostics.Append(response.State.GetAttribute(context.Background(), path.Root("id"), &imported)...)
	if response.Diagnostics.HasError() || imported.ValueString() != id {
		t.Fatalf("import ID: %s %v", imported.ValueString(), response.Diagnostics)
	}
	invalid := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "invalid"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("invalid import ID accepted")
	}
}

func TestBuildVolumeBackupUpdate(t *testing.T) {
	state := VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("before"), Description: types.StringValue("before")}, Volume: nullVolumeReference(), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}
	plan := state
	plan.Name = types.StringValue("after")
	plan.Description = types.StringValue("")
	body := buildVolumeBackupUpdate(plan, state)
	if body.Name == nil || *body.Name != "after" || body.Description == nil || *body.Description != "" {
		t.Fatalf("wrong patch: %#v", body)
	}
	plan = state
	plan.Description = types.StringNull()
	body = buildVolumeBackupUpdate(plan, state)
	if body.Name != nil || body.Description != nil {
		t.Fatalf("omitted description must preserve value: %#v", body)
	}
}

func TestPopulateVolumeBackupResourceState(t *testing.T) {
	backup := testVolumeBackup()
	state := VolumeBackupResourceModel{Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}
	populateVolumeBackupResourceState(backup, &state)
	if state.ID.ValueString() != backup.Id.String() || reference.Get(state.Volume, reference.ID).ValueString() != core.NullableToPtr(backup.Volume).Id.String() || state.VolumeName.ValueString() != "volume" || state.Size.ValueInt64() != 30 || state.Status.ValueString() != "available" || state.PolicyID.IsNull() {
		t.Fatalf("incomplete state: %#v", state)
	}
	backup.Volume = nil
	backup.Description = nil
	backup.Size = nil
	populateVolumeBackupResourceState(backup, &state)
	if state.Volume.IsNull() || !state.Description.IsNull() || !state.Size.IsNull() {
		t.Fatalf("response presence lost: %#v", state)
	}
}

func TestPopulateVolumeBackupResourceStateRecordsVolumeReference(t *testing.T) {
	t.Parallel()
	volumeID := core.NullableToPtr(testVolumeBackup().Volume).Id
	upper := strings.ToUpper(volumeID.String())
	for _, tt := range []struct {
		name     string
		prior    types.Object
		reported bool
		want     types.Object
	}{
		{"import", nullVolumeReference(), true, volumeIDReference(volumeID.String())},
		{"configured spelling", volumeIDReference(upper), true, volumeIDReference(upper)},
		{"different volume", volumeIDReference(volumeTestID.String()), true, volumeIDReference(volumeID.String())},
		{"volume no longer reported", volumeIDReference(upper), false, volumeIDReference(upper)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backup := testVolumeBackup()
			if !tt.reported {
				backup.Volume = nil
			}
			state := VolumeBackupResourceModel{Volume: tt.prior, RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}
			populateVolumeBackupResourceState(backup, &state)
			if !state.Volume.Equal(tt.want) {
				t.Fatalf("volume = %v, want %v", state.Volume, tt.want)
			}
		})
	}
}

func TestVolumeBackupValidateConfigChecksVolumeID(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		volume  types.Object
		wantErr bool
	}{
		{"valid", volumeIDReference(volumeTestID.String()), false},
		{"padded", volumeIDReference("  " + volumeTestID.String() + " "), false},
		{"invalid", volumeIDReference("not-a-uuid"), true},
		{"unknown reference", types.ObjectUnknown(reference.AttributeTypes(reference.ID)), false},
		{"unknown ID", reference.Of(map[string]types.String{reference.ID: types.StringUnknown()}), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := volumeBackupState(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: tt.volume, RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
			var response resource.ValidateConfigResponse
			(&VolumeBackupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &response)
			if response.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", response.Diagnostics, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(response.Diagnostics[0].Detail(), "volume.id") {
				t.Fatalf("the diagnostic must name volume.id: %v", response.Diagnostics)
			}
		})
	}
}

func TestVolumeBackupValidateConfigChecksRestoreVolumeID(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		restoreVolume types.Object
		wantErr       bool
	}{
		{"omitted", nullVolumeReference(), false},
		{"valid", volumeIDReference(restoreTestVolumeID.String()), false},
		{"padded uppercase", volumeIDReference(" " + strings.ToUpper(restoreTestVolumeID.String()) + " "), false},
		{"invalid", volumeIDReference("not-a-uuid"), true},
		{"unknown ID", reference.Of(map[string]types.String{reference.ID: types.StringUnknown()}), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := volumeBackupState(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: volumeIDReference(volumeTestID.String()), RestoreVolume: tt.restoreVolume, Timeouts: nullVolumeBackupTimeouts()})
			var response resource.ValidateConfigResponse
			(&VolumeBackupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &response)
			if response.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", response.Diagnostics, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(response.Diagnostics[0].Detail(), "restore_volume.id") {
				t.Fatalf("the diagnostic must name restore_volume.id: %v", response.Diagnostics)
			}
		})
	}
}

// Terraform core v1.14.0 plans a replacement again with a null prior state,
// and plans a tainted backup with a null prior state only
// (internal/terraform/node_resource_abstract_instance.go). A null prior state
// therefore stands for Create, a volume.id replacement, -replace, and taint.
func TestVolumeBackupModifyPlanRejectsRestoreOnNewBackup(t *testing.T) {
	t.Parallel()
	prior := restoreTestModel(nullVolumeReference())
	for _, tt := range []struct {
		name          string
		prior         *VolumeBackupResourceModel
		restoreVolume types.Object
		destroy       bool
		wantErr       bool
	}{
		{name: "new backup without restore_volume", restoreVolume: nullVolumeReference()},
		{name: "new backup with restore_volume", restoreVolume: volumeIDReference(restoreTestVolumeID.String()), wantErr: true},
		{name: "new backup with unknown restore_volume.id", restoreVolume: reference.Of(map[string]types.String{reference.ID: types.StringUnknown()}), wantErr: true},
		{name: "new backup with unknown restore_volume", restoreVolume: types.ObjectUnknown(reference.AttributeTypes(reference.ID))},
		{name: "in-place update with restore_volume", prior: &prior, restoreVolume: volumeIDReference(restoreTestVolumeID.String())},
		{name: "destroy", prior: &prior, restoreVolume: nullVolumeReference(), destroy: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			planned := restoreTestModel(tt.restoreVolume)
			plan := volumeBackupPlan(t, planned)
			if tt.destroy {
				plan.Raw = tftypes.NewValue(plan.Schema.Type().TerraformType(context.Background()), nil)
			}
			state := tfsdk.State{Schema: plan.Schema, Raw: tftypes.NewValue(plan.Schema.Type().TerraformType(context.Background()), nil)}
			if tt.prior != nil {
				state = volumeBackupState(t, *tt.prior)
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			(&VolumeBackupResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan, State: state, Config: tfsdk.Config(plan)}, &response)
			if response.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", response.Diagnostics, tt.wantErr)
			}
			if tt.wantErr {
				withPath, ok := response.Diagnostics[0].(diag.DiagnosticWithPath)
				if !ok || !withPath.Path().Equal(path.Root("restore_volume")) {
					t.Fatalf("the diagnostic must point at restore_volume: %v", response.Diagnostics)
				}
			}
		})
	}
}

// TestVolumeBackupPlanRejectsRestoreOnReplacement plans through the Framework
// as Terraform does for a replacement. Terraform first plans with the prior
// state. When that plan requires replacement, or when the user forces it with
// -replace or a tainted backup, Terraform plans the new backup again with a
// null prior state. Only that second plan sees a new backup.
func TestVolumeBackupPlanRejectsRestoreOnReplacement(t *testing.T) {
	t.Parallel()
	source := core.NullableToPtr(testVolumeBackup().Volume).Id.String()
	other := core.UUID{0x7a}.String()
	for _, tt := range []struct {
		name          string
		volumeID      string
		restoreVolume types.Object
		forced        bool
		wantReplace   bool
		wantErr       bool
	}{
		{name: "new volume.id with restore_volume", volumeID: other, restoreVolume: volumeIDReference(restoreTestVolumeID.String()), wantReplace: true, wantErr: true},
		{name: "new volume.id without restore_volume", volumeID: other, restoreVolume: nullVolumeReference(), wantReplace: true},
		{name: "same volume.id in another spelling with restore_volume", volumeID: " " + strings.ToUpper(source) + " ", restoreVolume: volumeIDReference(restoreTestVolumeID.String())},
		{name: "forced replacement with restore_volume", volumeID: source, restoreVolume: volumeIDReference(restoreTestVolumeID.String()), forced: true, wantErr: true},
		{name: "forced replacement without restore_volume", volumeID: source, restoreVolume: nullVolumeReference(), forced: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prior := restoreTestModel(volumeIDReference(restoreTestVolumeID.String()))
			config := VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: prior.Name}, Volume: volumeIDReference(tt.volumeID), RestoreVolume: tt.restoreVolume, Timeouts: nullVolumeBackupTimeouts()}
			proposed := prior
			proposed.Volume, proposed.RestoreVolume = config.Volume, config.RestoreVolume

			first := planVolumeBackupThroughFramework(t, &prior, config, proposed)
			if errs := volumeBackupPlanErrors(first); len(errs) > 0 {
				t.Fatalf("the plan with the prior state must not fail: %v", errs)
			}
			if (len(first.RequiresReplace) > 0) != tt.wantReplace {
				t.Fatalf("replacement paths = %v, want replacement %t", first.RequiresReplace, tt.wantReplace)
			}
			if !tt.wantReplace && !tt.forced {
				return
			}
			second := planVolumeBackupThroughFramework(t, nil, config, config)
			errs := volumeBackupPlanErrors(second)
			if (len(errs) > 0) != tt.wantErr {
				t.Fatalf("errors of the plan with a null prior state = %v, want error %t", errs, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(errs[0].Detail, "restore_volume") {
				t.Fatalf("the error must name restore_volume: %v", errs)
			}
		})
	}
}

// planVolumeBackupThroughFramework sends one PlanResourceChange for
// viettelcloud_volume_backup. A nil prior is the null prior state of a new
// backup.
func planVolumeBackupThroughFramework(t *testing.T, prior *VolumeBackupResourceModel, config, proposed VolumeBackupResourceModel) *tfprotov6.PlanResourceChangeResponse {
	t.Helper()
	resourceSchema := volumeBackupResourceSchema(t)
	dynamic := func(model *VolumeBackupResourceModel) *tfprotov6.DynamicValue {
		raw := tftypes.NewValue(resourceSchema.Type().TerraformType(context.Background()), nil)
		if model != nil {
			state := tfsdk.State{Schema: resourceSchema}
			if diags := state.Set(context.Background(), model); diags.HasError() {
				t.Fatal(diags)
			}
			raw = state.Raw
		}
		value, err := tfprotov6.NewDynamicValue(raw.Type(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return &value
	}
	server := providerserver.NewProtocol6(&volumeBackupPlanningProvider{})()
	response, err := server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "viettelcloud_volume_backup", PriorState: dynamic(prior), Config: dynamic(&config), ProposedNewState: dynamic(&proposed),
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func volumeBackupPlanErrors(response *tfprotov6.PlanResourceChangeResponse) []*tfprotov6.Diagnostic {
	var errs []*tfprotov6.Diagnostic
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			errs = append(errs, diagnostic)
		}
	}
	return errs
}

// volumeBackupPlanningProvider serves only VolumeBackupResource, without
// provider configuration, for PlanResourceChange tests.
type volumeBackupPlanningProvider struct{}

func (p *volumeBackupPlanningProvider) Metadata(_ context.Context, _ frameworkprovider.MetadataRequest, resp *frameworkprovider.MetadataResponse) {
	resp.TypeName = "viettelcloud"
}

func (p *volumeBackupPlanningProvider) Schema(_ context.Context, _ frameworkprovider.SchemaRequest, resp *frameworkprovider.SchemaResponse) {
	resp.Schema = providerschema.Schema{}
}

func (p *volumeBackupPlanningProvider) Configure(context.Context, frameworkprovider.ConfigureRequest, *frameworkprovider.ConfigureResponse) {
}

func (p *volumeBackupPlanningProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewVolumeBackupResource}
}

func (p *volumeBackupPlanningProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func TestVolumeBackupCreateRejectsRestoreVolumeWithoutRequests(t *testing.T) {
	t.Parallel()
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: testVolumeBackup().Project.Id}
	plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: volumeIDReference(volumeTestID.String()), RestoreVolume: volumeIDReference(restoreTestVolumeID.String()), Timeouts: nullVolumeBackupTimeouts()})
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("Create must reject restore_volume")
	}
}

func TestVolumeBackupSDKRequestShape(t *testing.T) {
	backup := testVolumeBackup()
	var calls []string
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		var body []byte
		if req.Body != nil {
			var err error
			body, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
		calls = append(calls, req.Method+" "+req.URL.Path+" "+string(body))
		if req.Header.Get("Project-ID") != backup.Project.Id.String() {
			t.Fatalf("wrong project scope: %q", req.Header.Get("Project-ID"))
		}
		switch req.Method {
		case http.MethodPost:
			return testHTTPResponse(req, 201, backupResponseJSON(backup)), nil
		case http.MethodPatch:
			return testHTTPResponse(req, 200, backupResponseJSON(backup)), nil
		case http.MethodDelete:
			return testHTTPResponse(req, 202, ""), nil
		default:
			t.Fatalf("unexpected method %s", req.Method)
			return nil, nil
		}
	})
	_, err := client.CreateVolumeBackup(context.Background(), blockstoragesdk.CreateVolumeBackupParams{ProjectID: backup.Project.Id}, blockstoragesdk.VolumeBackupCreateSchema{Name: "backup", VolumeId: core.NullableToPtr(backup.Volume).Id})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	_, err = client.PartialUpdateVolumeBackup(context.Background(), backup.Id, blockstoragesdk.PartialUpdateVolumeBackupParams{ProjectID: backup.Project.Id}, blockstoragesdk.VolumeBackupPartialUpdateSchema{Description: &empty})
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteVolumeBackup(context.Background(), backup.Id, blockstoragesdk.DeleteVolumeBackupParams{ProjectID: backup.Project.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !strings.Contains(calls[0], `"volume_id"`) || strings.Contains(calls[0], `"description"`) || !strings.Contains(calls[1], `"description":""`) || strings.Contains(calls[1], `"name"`) || !strings.Contains(calls[2], "/volume-backups/") {
		t.Fatalf("wrong requests: %#v", calls)
	}
}

func TestVolumeBackupWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backup := testVolumeBackup()
		polls := 0
		client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			polls++
			if req.Method != http.MethodGet {
				t.Fatalf("wrong method: %s", req.Method)
			}
			if polls == 1 {
				status := blockstoragesdk.VolumeBackupStatusCreating
				backup.Status.Set(status)
			}
			if polls == 2 {
				status := blockstoragesdk.VolumeBackupStatusAvailable
				backup.Status.Set(status)
			}
			if polls == 3 {
				return testHTTPResponse(req, 404, `{}`), nil
			}
			return testHTTPResponse(req, 200, backupResponseJSON(backup)), nil
		})
		r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
		start := time.Now()
		result, err := r.waitUntilAvailable(context.Background(), backup.Id, volumeBackupTimeout)
		if err != nil || core.NullableToPtr(result.Status) == nil || *core.NullableToPtr(result.Status) != blockstoragesdk.VolumeBackupStatusAvailable {
			t.Fatalf("create waiter: %v", err)
		}
		if time.Since(start) != volumeBackupPollInterval {
			t.Fatalf("poll elapsed %s", time.Since(start))
		}
		if err := r.waitUntilDeleted(context.Background(), backup.Id, volumeBackupTimeout); err != nil {
			t.Fatalf("delete waiter: %v", err)
		}
		if polls != 3 {
			t.Fatalf("expected three GETs, got %d", polls)
		}
	})
}

func TestVolumeBackupUpdateWaitsForNewValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backup := testVolumeBackup()
		polls := 0
		client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			polls++
			if polls == 2 {
				backup.Name = "renamed"
			}
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		})
		r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
		name := " renamed "
		start := time.Now()
		result, err := r.waitUntilAvailableMatching(context.Background(), backup.Id, &blockstoragesdk.VolumeBackupPartialUpdateSchema{Name: &name}, volumeBackupUpdateTimeout)
		if err != nil || result.Name != "renamed" || polls != 2 || time.Since(start) != volumeBackupPollInterval {
			t.Fatalf("update waiter returned early: result=%#v polls=%d elapsed=%s err=%v", result, polls, time.Since(start), err)
		}
	})
}

func TestVolumeBackupResourceFrameworkLifecycle(t *testing.T) {
	ctx := context.Background()
	backup := testVolumeBackup()
	requests := 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Project-ID") != backup.Project.Id.String() {
			t.Fatalf("wrong project: %q", req.Header.Get("Project-ID"))
		}
		collection := "/v2/block-storage/volume-backups/"
		item := collection + backup.Id.String() + "/"
		switch requests {
		case 1:
			if req.Method != http.MethodGet || req.URL.Path != volumeBackupTestVolumePath {
				t.Fatalf("wrong source volume idle poll: %s %s", req.Method, req.URL.Path)
			}
			return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusInUse)), nil
		case 2:
			if req.Method != http.MethodPost || req.URL.Path != collection {
				t.Fatalf("wrong create request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "backup" || body["volume_id"] != core.NullableToPtr(backup.Volume).Id.String() || body["description"] != "saved" {
				t.Fatalf("wrong create body: %#v", body)
			}
			creating := blockstoragesdk.VolumeBackupStatusCreating
			backup.Status.Set(creating)
			return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
		case 3, 4:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong read request: %s %s", req.Method, req.URL.Path)
			}
			available := blockstoragesdk.VolumeBackupStatusAvailable
			backup.Status.Set(available)
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 5:
			if req.Method != http.MethodPatch || req.URL.Path != item {
				t.Fatalf("wrong update request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["name"] != "renamed" || body["description"] != "" {
				t.Fatalf("wrong patch body: %#v", body)
			}
			backup.Name = "renamed"
			empty := ""
			backup.Description = &empty
			updating := blockstoragesdk.VolumeBackupStatusUpdating
			backup.Status.Set(updating)
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 6:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong update poll: %s %s", req.Method, req.URL.Path)
			}
			available := blockstoragesdk.VolumeBackupStatusAvailable
			backup.Status.Set(available)
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 7:
			if req.Method != http.MethodDelete || req.URL.Path != item {
				t.Fatalf("wrong delete request: %s %s", req.Method, req.URL.Path)
			}
			return testHTTPResponse(req, http.StatusAccepted, ""), nil
		case 8:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong delete poll: %s %s", req.Method, req.URL.Path)
			}
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		default:
			t.Fatalf("unexpected request %d: %s %s", requests, req.Method, req.URL.Path)
			return nil, nil
		}
	})
	r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
	createPlan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
		Name: types.StringValue("backup"), Description: types.StringValue("saved"),
	}, Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
	created := resource.CreateResponse{State: tfsdk.State{Schema: createPlan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdModel VolumeBackupResourceModel
	if diags := created.State.Get(ctx, &createdModel); diags.HasError() {
		t.Fatal(diags)
	}
	if createdModel.ID.ValueString() != backup.Id.String() || createdModel.Name.ValueString() != "backup" || createdModel.Description.ValueString() != "saved" || createdModel.Status.ValueString() != "available" || createdModel.Size.ValueInt64() != 30 {
		t.Fatalf("create state differs from plan or response: %#v", createdModel)
	}
	refreshed := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &refreshed)
	if refreshed.Diagnostics.HasError() {
		t.Fatalf("read: %v", refreshed.Diagnostics)
	}
	var refreshedModel VolumeBackupResourceModel
	if diags := refreshed.State.Get(ctx, &refreshedModel); diags.HasError() {
		t.Fatal(diags)
	}
	if refreshedModel.ID.ValueString() != backup.Id.String() || reference.Get(refreshedModel.Volume, reference.ID).ValueString() != core.NullableToPtr(backup.Volume).Id.String() || refreshedModel.Region.ValueString() != "region" {
		t.Fatalf("read state incomplete: %#v", refreshedModel)
	}
	updateModel := refreshedModel
	updateModel.Name = types.StringValue(" renamed ")
	updateModel.Description = types.StringValue("")
	updatePlan := volumeBackupPlan(t, updateModel)
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: updatePlan.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: refreshed.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	var updatedModel VolumeBackupResourceModel
	if diags := updated.State.Get(ctx, &updatedModel); diags.HasError() {
		t.Fatal(diags)
	}
	if updatedModel.ID.ValueString() != backup.Id.String() || updatedModel.Name.ValueString() != " renamed " || updatedModel.Description.ValueString() != "" || updatedModel.Status.ValueString() != "available" {
		t.Fatalf("update broke plan/state consistency: %#v", updatedModel)
	}
	deleted := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || requests != 8 {
		t.Fatalf("delete: requests=%d diagnostics=%v", requests, deleted.Diagnostics)
	}
}

func TestVolumeBackupCreateWaitFailurePreservesIdentity(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		backupStatus blockstoragesdk.VolumeBackupStatus
		elapsed      time.Duration
	}{
		{name: "terminal status", status: http.StatusOK, backupStatus: blockstoragesdk.VolumeBackupStatusFail},
		{name: "SDK error", status: http.StatusInternalServerError, backupStatus: blockstoragesdk.VolumeBackupStatusCreating},
		{name: "timeout", status: http.StatusOK, backupStatus: blockstoragesdk.VolumeBackupStatusCreating, elapsed: volumeBackupTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backup := testVolumeBackup()
				backup.Status.Set(tt.backupStatus)
				client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == volumeBackupTestVolumePath {
						return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusAvailable)), nil
					}
					if req.Method == http.MethodPost {
						return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
					}
					return testHTTPResponse(req, tt.status, backupResponseJSON(backup)), nil
				})
				r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
				plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
					Name: types.StringValue("backup"),
				}, Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
				response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
				start := time.Now()
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
				if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), backup.Id.String()) {
					t.Fatalf("wait failure must identify the backup: %v", response.Diagnostics)
				}
				if time.Since(start) != tt.elapsed {
					t.Fatalf("wait elapsed %s, want %s", time.Since(start), tt.elapsed)
				}
				var state VolumeBackupResourceModel
				if diags := response.State.Get(context.Background(), &state); diags.HasError() {
					t.Fatal(diags)
				}
				if state.ID.ValueString() != backup.Id.String() {
					t.Fatalf("wait failure lost backup identity: %#v", state)
				}
			})
		})
	}
}

func TestVolumeBackupResourceNotFoundAndSDKErrors(t *testing.T) {
	ctx := context.Background()
	backup := testVolumeBackup()
	model := VolumeBackupResourceModel{Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}
	populateVolumeBackupResourceState(backup, &model)
	state := volumeBackupState(t, model)

	notFound := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
	})
	r := &VolumeBackupResource{client: notFound, claims: keylock.New(), projectID: backup.Project.Id}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("a missing backup must be removed from state: %v", read.Diagnostics)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("deleting an already missing backup must succeed: %v", deleted.Diagnostics)
	}

	errClient := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == volumeBackupTestVolumePath {
			return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusAvailable)), nil
		}
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	r.client = errClient
	createPlan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
		Name: types.StringValue("backup"),
	}, Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
	created := resource.CreateResponse{State: tfsdk.State{Schema: createPlan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &created)
	if !created.Diagnostics.HasError() {
		t.Fatal("create SDK failure must return a diagnostic")
	}
	read = resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() || read.State.Raw.IsNull() {
		t.Fatalf("read SDK failure must keep state and return a diagnostic: %v", read.Diagnostics)
	}
	updateModel := model
	updateModel.Name = types.StringValue("renamed")
	updatePlan := volumeBackupPlan(t, updateModel)
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: state}, &updated)
	if !updated.Diagnostics.HasError() {
		t.Fatal("update SDK failure must return a diagnostic")
	}
	deleted = resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() {
		t.Fatal("delete SDK failure must return a diagnostic")
	}
}

// The API trims name and description, so the update sends trimmed values and a
// change in outer whitespace only sends nothing.
func TestBuildVolumeBackupUpdateTrimsStrings(t *testing.T) {
	t.Parallel()
	state := VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue(" backup "), Description: types.StringValue(" saved ")}, Volume: nullVolumeReference(), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()}
	for _, tt := range []struct {
		name                      string
		planName, planDescription string
		wantName, wantDescription *string
	}{
		{"padded changes", " renamed ", " after ", new("renamed"), new("after")},
		{"outer whitespace only", "backup", "saved", nil, nil},
		{"whitespace only description clears", " backup ", "  ", nil, new("")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := state
			plan.Name, plan.Description = types.StringValue(tt.planName), types.StringValue(tt.planDescription)
			body := buildVolumeBackupUpdate(plan, state)
			if (body.Name == nil) != (tt.wantName == nil) || (body.Name != nil && *body.Name != *tt.wantName) ||
				(body.Description == nil) != (tt.wantDescription == nil) || (body.Description != nil && *body.Description != *tt.wantDescription) {
				t.Fatalf("patch: got %#v, want name=%v description=%v", body, tt.wantName, tt.wantDescription)
			}
		})
	}
}

// The requests carry the trimmed name and description, and state after create,
// read, and update keeps the configured spelling.
func TestVolumeBackupResourceTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backup := testVolumeBackup()
	itemPath := "/v2/block-storage/volume-backups/" + backup.Id.String() + "/"
	var sent []string
	// The fake backend trims both strings, as the API does.
	store := func(req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode %s body: %v", req.Method, err)
			return
		}
		if name, ok := body["name"].(string); ok {
			sent = append(sent, "name="+name)
			backup.Name = strings.TrimSpace(name)
		}
		if description, ok := body["description"].(string); ok {
			sent = append(sent, "description="+description)
			backup.Description = new(strings.TrimSpace(description))
		}
		if volumeID, ok := body["volume_id"].(string); ok {
			sent = append(sent, "volume_id="+volumeID)
		}
	}
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v2/block-storage/volume-backups/":
			store(req)
			return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
		case req.Method == http.MethodPatch && req.URL.Path == itemPath:
			store(req)
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case req.Method == http.MethodGet && req.URL.Path == volumeBackupTestVolumePath:
			return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusAvailable)), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
		}
	})
	r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id, pollInterval: time.Millisecond, timeout: time.Second}
	model := func(t *testing.T, state tfsdk.State) VolumeBackupResourceModel {
		t.Helper()
		var m VolumeBackupResourceModel
		if diags := state.Get(ctx, &m); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		return m
	}

	volumeID := core.NullableToPtr(backup.Volume).Id.String()
	paddedVolume := volumeIDReference("  " + volumeID + " ")
	createPlan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
		Name: types.StringValue(" backup "), Description: types.StringValue(" saved "),
	}, Volume: paddedVolume, RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
	created := resource.CreateResponse{State: tfsdk.State{Schema: createPlan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: createPlan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	createSent := "name=backup|description=saved|volume_id=" + volumeID
	if got := model(t, created.State); got.Name.ValueString() != " backup " || got.Description.ValueString() != " saved " || !got.Volume.Equal(paddedVolume) || strings.Join(sent, "|") != createSent {
		t.Fatalf("after create: name %q description %q volume %v, sent %q", got.Name.ValueString(), got.Description.ValueString(), got.Volume, sent)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if got := model(t, read.State); read.Diagnostics.HasError() || got.Name.ValueString() != " backup " || got.Description.ValueString() != " saved " || !got.Volume.Equal(paddedVolume) {
		t.Fatalf("after read: name %q description %q volume %v %v", got.Name.ValueString(), got.Description.ValueString(), got.Volume, read.Diagnostics)
	}

	current := read.State
	for _, step := range []struct {
		name, description string
		wantSent          string
	}{
		// The waiter must accept the trimmed description the backend reports.
		{" backup ", " after ", createSent + "|description=after"},
		// A change in outer whitespace only sends no request.
		{"backup", "after", createSent + "|description=after"},
	} {
		m := model(t, current)
		m.Name, m.Description = types.StringValue(step.name), types.StringValue(step.description)
		updatePlan := volumeBackupPlan(t, m)
		updated := resource.UpdateResponse{State: tfsdk.State{Schema: updatePlan.Schema}}
		r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: current}, &updated)
		if updated.Diagnostics.HasError() {
			t.Fatalf("update to %q/%q: %v", step.name, step.description, updated.Diagnostics)
		}
		if got := model(t, updated.State); got.Name.ValueString() != step.name || got.Description.ValueString() != step.description || strings.Join(sent, "|") != step.wantSent {
			t.Fatalf("after update to %q/%q: name %q description %q, sent %q", step.name, step.description, got.Name.ValueString(), got.Description.ValueString(), sent)
		}
		current = updated.State
	}
}

// restoreTestVolumeID is the restore target volume of the restore tests.
var restoreTestVolumeID = core.UUID{10}

const volumeBusyBody = `{"errors":[{"location":"","code":"resource_busy","message":"volume is busy, please try again later."}],"request_id":"f1dbd828"}`

// restoreTestModel returns the state of testVolumeBackup with restoreVolume.
func restoreTestModel(restoreVolume types.Object) VolumeBackupResourceModel {
	backup := testVolumeBackup()
	model := VolumeBackupResourceModel{Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: restoreVolume, Timeouts: nullVolumeBackupTimeouts()}
	populateVolumeBackupResourceState(backup, &model)
	return model
}

// volumeBackupRestoreFake answers the GETs of volume restoreTestVolumeID and
// of testVolumeBackup, and the PATCH and restore of that backup. A GET answers
// the next status of its list and then repeats the last one. The after lists
// replace the before lists once the restore request succeeds. An empty status
// stands for a response without status.
type volumeBackupRestoreFake struct {
	t                         *testing.T
	backup                    *blockstoragesdk.VolumeBackupSchema
	volumeBefore, volumeAfter []blockstoragesdk.VolumeStatus
	backupBefore, backupAfter []blockstoragesdk.VolumeBackupStatus
	restoreStatus             int
	restoreBody               string
	restoreBusy               int
	volumeMissingAfter        bool
	restored                  bool
	calls                     []string
	restoreBodies             []string
	onRestore                 func()
}

func newVolumeBackupRestoreFake(t *testing.T) *volumeBackupRestoreFake {
	return &volumeBackupRestoreFake{
		t:            t,
		backup:       testVolumeBackup(),
		volumeBefore: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable},
		volumeAfter:  []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring, blockstoragesdk.VolumeStatusAvailable},
		backupBefore: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable},
		backupAfter:  []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusRestoring, blockstoragesdk.VolumeBackupStatusAvailable},
	}
}

func nextRestoreTestStatus[T any](list *[]T) T {
	value := (*list)[0]
	if len(*list) > 1 {
		*list = (*list)[1:]
	}
	return value
}

func (f *volumeBackupRestoreFake) resource() *VolumeBackupResource {
	backupPath := "/v2/block-storage/volume-backups/" + f.backup.Id.String() + "/"
	volumePath := "/v2/block-storage/volumes/" + restoreTestVolumeID.String() + "/"
	client := volumeTestClient(f.t, func(req *http.Request) (*http.Response, error) {
		f.calls = append(f.calls, req.Method+" "+req.URL.Path)
		if req.Header.Get("Project-ID") != f.backup.Project.Id.String() {
			f.t.Errorf("wrong project scope: %q", req.Header.Get("Project-ID"))
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == volumePath:
			if f.restored && f.volumeMissingAfter {
				return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
			}
			statuses := &f.volumeBefore
			if f.restored {
				statuses = &f.volumeAfter
			}
			volume := sampleVolumeDetail()
			volume.Id, volume.Status = restoreTestVolumeID, nextRestoreTestStatus(statuses)
			return testHTTPResponse(req, http.StatusOK, volumeDetailJSON(volume)), nil
		case req.Method == http.MethodGet && req.URL.Path == backupPath:
			statuses := &f.backupBefore
			if f.restored {
				statuses = &f.backupAfter
			}
			if status := nextRestoreTestStatus(statuses); status == "" {
				f.backup.Status.SetNull()
			} else {
				f.backup.Status.Set(status)
			}
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(f.backup)), nil
		case req.Method == http.MethodPatch && req.URL.Path == backupPath:
			var body blockstoragesdk.VolumeBackupPartialUpdateSchema
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				f.t.Errorf("decode PATCH body: %v", err)
			}
			if body.Name != nil {
				f.backup.Name = *body.Name
			}
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(f.backup)), nil
		case req.Method == http.MethodPost && req.URL.Path == backupPath+"restore/":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				f.t.Errorf("read restore body: %v", err)
			}
			f.restoreBodies = append(f.restoreBodies, string(body))
			if f.onRestore != nil {
				f.onRestore()
			}
			if len(f.restoreBodies) <= f.restoreBusy {
				return testHTTPResponse(req, http.StatusConflict, volumeBusyBody), nil
			}
			if f.restoreStatus != 0 {
				return testHTTPResponse(req, f.restoreStatus, f.restoreBody), nil
			}
			f.restored = true
			return testHTTPResponse(req, http.StatusAccepted, ""), nil
		default:
			f.t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
		}
	})
	return &VolumeBackupResource{client: client, claims: keylock.New(), projectID: f.backup.Project.Id}
}

// update runs Update from prior to planned. The response starts from the
// prior state, as the Framework seeds it.
func (f *volumeBackupRestoreFake) update(r *VolumeBackupResource, prior, planned VolumeBackupResourceModel) (VolumeBackupResourceModel, diag.Diagnostics) {
	f.t.Helper()
	state := volumeBackupState(f.t, prior)
	response := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: volumeBackupPlan(f.t, planned), State: state}, &response)
	var got VolumeBackupResourceModel
	if diags := response.State.Get(context.Background(), &got); diags.HasError() {
		f.t.Fatalf("get state: %v", diags)
	}
	return got, response.Diagnostics
}

func TestVolumeBackupCreateWaitsForIdleSourceVolume(t *testing.T) {
	for _, tt := range []struct {
		name           string
		volume         []blockstoragesdk.VolumeStatus
		createStatus   int
		createBody     string
		busyRefusals   int
		wantErr        string
		wantPosts      int
		wantElapsed    time.Duration
		wantMaxElapsed time.Duration
		wantNotDetail  string
	}{
		{name: "busy volume becomes idle", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusBackingUp, blockstoragesdk.VolumeStatusInUse}, wantPosts: 1, wantElapsed: volumeBackupPollInterval},
		{name: "absent status becomes idle", volume: []blockstoragesdk.VolumeStatus{"", blockstoragesdk.VolumeStatusAvailable}, wantPosts: 1, wantElapsed: volumeBackupPollInterval},
		{name: "refusing status fails at once", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusDeleting}, wantErr: "volume status deleting refuses the request"},
		{name: "unknown status fails at once", volume: []blockstoragesdk.VolumeStatus{"strange"}, wantErr: "volume has unknown status strange"},
		// The jitter rests between half and all of 10 s, then of 20 s.
		{name: "busy refusal is sent again until accepted", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, busyRefusals: 2, wantPosts: 3, wantElapsed: 15 * time.Second, wantMaxElapsed: 30 * time.Second},
		{name: "busy refusal outlasts the replay", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, createStatus: http.StatusConflict, createBody: volumeBusyBody, wantErr: "is busy, because another operation holds it: timeout after 2m0s", wantPosts: -1, wantElapsed: volumeBusyReplayTimeout},
		{name: "other conflict is not a busy refusal", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, createStatus: http.StatusConflict, createBody: `{"errors":[{"location":"","code":"conflict","message":"volume is busy"}]}`, wantErr: "volume is busy", wantNotDetail: "is busy, because", wantPosts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backup := testVolumeBackup()
				volumes, posts := tt.volume, 0
				client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodGet && req.URL.Path == volumeBackupTestVolumePath:
						return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(nextRestoreTestStatus(&volumes))), nil
					case req.Method == http.MethodPost:
						posts++
						if posts <= tt.busyRefusals {
							return testHTTPResponse(req, http.StatusConflict, volumeBusyBody), nil
						}
						if tt.createStatus != 0 {
							return testHTTPResponse(req, tt.createStatus, tt.createBody), nil
						}
						return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
					default:
						return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
					}
				})
				r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
				plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
				response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
				start := time.Now()
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
				elapsed := time.Since(start)
				if tt.wantPosts < 0 && posts < 2 || tt.wantPosts >= 0 && posts != tt.wantPosts {
					t.Fatalf("posts = %d, want %d (-1 is more than one)", posts, tt.wantPosts)
				}
				if tt.wantMaxElapsed == 0 && elapsed != tt.wantElapsed || tt.wantMaxElapsed != 0 && (elapsed < tt.wantElapsed || elapsed > tt.wantMaxElapsed) {
					t.Fatalf("elapsed %s, want %s (max %s)", elapsed, tt.wantElapsed, tt.wantMaxElapsed)
				}
				if tt.wantErr == "" {
					if response.Diagnostics.HasError() {
						t.Fatalf("create: %v", response.Diagnostics)
					}
					return
				}
				if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error creating volume backup" || !strings.Contains(response.Diagnostics[0].Detail(), tt.wantErr) {
					t.Fatalf("diagnostics = %v, want %q", response.Diagnostics, tt.wantErr)
				}
				if tt.wantNotDetail != "" && strings.Contains(response.Diagnostics[0].Detail(), tt.wantNotDetail) {
					t.Fatalf("diagnostics = %v, must not contain %q", response.Diagnostics, tt.wantNotDetail)
				}
			})
		})
	}
}

// A backup Create claims its source volume until the backup is available, so
// the lock covers the idle wait, the POST, and the ready wait.
func TestVolumeBackupCreateHoldsSourceVolumeLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backup := testVolumeBackup()
		volumeID := core.NullableToPtr(backup.Volume).Id.String()
		ctx := context.Background()
		r := &VolumeBackupResource{claims: keylock.New(), projectID: backup.Project.Id}
		var calls []string
		contended := make(chan int, 1)
		backupGets := 0
		r.client = volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			switch {
			case req.Method == http.MethodGet && req.URL.Path == volumeBackupTestVolumePath:
				calls = append(calls, "GET volume")
				return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusAvailable)), nil
			case req.Method == http.MethodPost:
				calls = append(calls, "POST backup")
				backup.Status.Set(blockstoragesdk.VolumeBackupStatusCreating)
				go func() {
					release, err := r.claims.Lock(ctx, volumeID)
					if err != nil {
						t.Error(err)
						return
					}
					contended <- len(calls)
					release()
				}()
				return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
			default:
				calls = append(calls, "GET backup")
				if backupGets++; backupGets == 2 {
					backup.Status.Set(blockstoragesdk.VolumeBackupStatusAvailable)
				}
				return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
			}
		})
		unlock, err := r.claims.Lock(ctx, volumeID)
		if err != nil {
			t.Fatal(err)
		}
		plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: volumeIDReference(volumeID), RestoreVolume: nullVolumeReference(), Timeouts: nullVolumeBackupTimeouts()})
		done := make(chan diag.Diagnostics, 1)
		go func() {
			response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
			done <- response.Diagnostics
		}()
		synctest.Wait()
		if len(calls) != 0 {
			t.Fatalf("Create must wait for the source volume lock, calls: %v", calls)
		}
		unlock()
		if diags := <-done; diags.HasError() {
			t.Fatalf("create: %v", diags)
		}
		want := []string{"GET volume", "POST backup", "GET backup", "GET backup"}
		if !slices.Equal(calls, want) {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
		if got := <-contended; got != len(want) {
			t.Fatalf("the lock was free after %d of %d requests", got, len(want))
		}
	})
}

func TestVolumeBackupCreateTimeouts(t *testing.T) {
	for _, tt := range []struct {
		name        string
		timeouts    timeouts.Value
		wantTimeout time.Duration
	}{
		{name: "default", timeouts: nullVolumeBackupTimeouts(), wantTimeout: volumeBackupTimeout},
		{name: "configured", timeouts: volumeBackupTimeoutsValue("5m", ""), wantTimeout: 5 * time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backup := testVolumeBackup()
				posts := 0
				client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPost {
						posts++
					}
					return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusBackingUp)), nil
				})
				r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
				plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: volumeIDReference(core.NullableToPtr(backup.Volume).Id.String()), RestoreVolume: nullVolumeReference(), Timeouts: tt.timeouts})
				response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
				start := time.Now()
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
				if elapsed := time.Since(start); elapsed != tt.wantTimeout {
					t.Fatalf("elapsed %s, want %s", elapsed, tt.wantTimeout)
				}
				want := fmt.Sprintf("within timeouts.create (%s)", tt.wantTimeout)
				if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error creating volume backup" || !strings.Contains(response.Diagnostics[0].Detail(), want) || posts != 0 {
					t.Fatalf("diagnostics = %v, posts = %d, want %q and no POST", response.Diagnostics, posts, want)
				}
			})
		})
	}
}

func TestVolumeBackupRestoreTarget(t *testing.T) {
	t.Parallel()
	a, b := restoreTestVolumeID.String(), volumeTestID.String()
	for _, tt := range []struct {
		name        string
		state, plan types.Object
		wantRestore *core.UUID
	}{
		{"null to A", nullVolumeReference(), volumeIDReference(a), &restoreTestVolumeID},
		{"null to padded uppercase A", nullVolumeReference(), volumeIDReference(" " + strings.ToUpper(a) + " "), &restoreTestVolumeID},
		{"A to B", volumeIDReference(a), volumeIDReference(b), &volumeTestID},
		{"A to A in uppercase", volumeIDReference(a), volumeIDReference(strings.ToUpper(a)), nil},
		{"A to padded A", volumeIDReference(a), volumeIDReference("  " + a + " "), nil},
		{"A to null", volumeIDReference(a), nullVolumeReference(), nil},
		{"null to null", nullVolumeReference(), nullVolumeReference(), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := volumeBackupRestoreTarget(restoreTestModel(tt.plan), restoreTestModel(tt.state))
			if diags.HasError() || (got == nil) != (tt.wantRestore == nil) || (got != nil && *got != *tt.wantRestore) {
				t.Fatalf("restore target = %v %v, want %v", got, diags, tt.wantRestore)
			}
		})
	}
}

// Each case runs Update with the prior and planned restore_volume, and checks
// the requests, the restore body, and the state restore_volume.
func TestVolumeBackupUpdateRestoresIntoNewVolume(t *testing.T) {
	a := restoreTestVolumeID.String()
	backupPath := "/v2/block-storage/volume-backups/" + testVolumeBackup().Id.String() + "/"
	volumePath := "/v2/block-storage/volumes/" + a + "/"
	restoreCalls := []string{
		"GET " + backupPath,                      // the Update wait for the backup
		"GET " + volumePath, "GET " + backupPath, // the idle wait
		"POST " + backupPath + "restore/",
		"GET " + volumePath, "GET " + volumePath, "GET " + backupPath, "GET " + volumePath, "GET " + backupPath, // the restore wait
	}
	for _, tt := range []struct {
		name        string
		prior, plan types.Object
		volumeAfter []blockstoragesdk.VolumeStatus
		wantCalls   []string
	}{
		{name: "null to A, detached volume", prior: nullVolumeReference(), plan: volumeIDReference(a), wantCalls: restoreCalls},
		{name: "null to padded uppercase A, attached volume", prior: nullVolumeReference(), plan: volumeIDReference(" " + strings.ToUpper(a) + " "), volumeAfter: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring, blockstoragesdk.VolumeStatusInUse}, wantCalls: restoreCalls},
		{name: "B to A", prior: volumeIDReference(volumeTestID.String()), plan: volumeIDReference(a), wantCalls: restoreCalls},
		{name: "A to A in another spelling", prior: volumeIDReference(a), plan: volumeIDReference(" " + strings.ToUpper(a)), wantCalls: []string{"GET " + backupPath}},
		{name: "A to null", prior: volumeIDReference(a), plan: nullVolumeReference(), wantCalls: []string{"GET " + backupPath}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newVolumeBackupRestoreFake(t)
				if tt.volumeAfter != nil {
					f.volumeAfter = tt.volumeAfter
				}
				got, diags := f.update(f.resource(), restoreTestModel(tt.prior), restoreTestModel(tt.plan))
				if diags.HasError() {
					t.Fatalf("update: %v", diags)
				}
				if strings.Join(f.calls, "\n") != strings.Join(tt.wantCalls, "\n") {
					t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(tt.wantCalls, "\n"))
				}
				if len(f.restoreBodies) > 0 && f.restoreBodies[0] != `{"volume_id":"`+a+`"}` {
					t.Fatalf("restore body = %s", f.restoreBodies[0])
				}
				if !got.RestoreVolume.Equal(tt.plan) || got.ID.ValueString() != testVolumeBackup().Id.String() || got.Status.ValueString() != "available" {
					t.Fatalf("state restore_volume = %v id = %s status = %s, want %v", got.RestoreVolume, got.ID.ValueString(), got.Status.ValueString(), tt.plan)
				}
			})
		})
	}
}

func TestVolumeBackupUpdatePatchesBeforeRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newVolumeBackupRestoreFake(t)
		planned := restoreTestModel(volumeIDReference(restoreTestVolumeID.String()))
		planned.Name = types.StringValue("renamed")
		got, diags := f.update(f.resource(), restoreTestModel(nullVolumeReference()), planned)
		if diags.HasError() {
			t.Fatalf("update: %v", diags)
		}
		backupPath := "/v2/block-storage/volume-backups/" + f.backup.Id.String() + "/"
		calls := strings.Join(f.calls, "|")
		if !strings.HasPrefix(calls, "PATCH "+backupPath+"|GET "+backupPath+"|GET /v2/block-storage/volumes/") || !strings.Contains(calls, "POST "+backupPath+"restore/") {
			t.Fatalf("PATCH and its wait must come before the restore: %s", calls)
		}
		if got.Name.ValueString() != "renamed" || !got.RestoreVolume.Equal(planned.RestoreVolume) {
			t.Fatalf("state = %#v", got)
		}
	})
}

// A failed restore keeps the prior restore_volume and the backup ID, so the
// next plan proposes the restore again and nothing replaces the backup. A PATCH
// that succeeded before the restore stays in state.
func TestVolumeBackupFailedRestoreKeepsPriorRestoreVolume(t *testing.T) {
	a := restoreTestVolumeID.String()
	for _, tt := range []struct {
		name        string
		prior       types.Object
		setup       func(*volumeBackupRestoreFake)
		wantSummary string
		wantDetail  string
		notDetail   string
		wantPosts   int
	}{
		{name: "validation refusal", prior: nullVolumeReference(), setup: func(f *volumeBackupRestoreFake) {
			f.restoreStatus, f.restoreBody = http.StatusBadRequest, `{"errors":[{"location":"","code":"value_error","message":"volume size is smaller than the backup"}]}`
		}, wantSummary: "Error restoring volume backup", wantDetail: "smaller than the backup", notDetail: "is busy, because", wantPosts: 1},
		{name: "busy refusal outlasts the replay", prior: volumeIDReference(volumeTestID.String()), setup: func(f *volumeBackupRestoreFake) {
			f.restoreStatus, f.restoreBody = http.StatusConflict, volumeBusyBody
		}, wantSummary: "Error restoring volume backup", wantDetail: "volume " + a + " is busy, because another operation holds it: timeout after 2m0s", wantPosts: -1},
		{name: "conflict without the busy code", prior: nullVolumeReference(), setup: func(f *volumeBackupRestoreFake) {
			f.restoreStatus, f.restoreBody = http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"previous operation failed"}]}`
		}, wantSummary: "Error restoring volume backup", wantDetail: "previous operation failed", notDetail: "is busy, because", wantPosts: 1},
		{name: "volume ends restore_failed", prior: nullVolumeReference(), setup: func(f *volumeBackupRestoreFake) {
			f.volumeAfter = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoreFailed}
		}, wantSummary: "Error waiting for volume backup restore", wantDetail: "volume " + a + " restore failed", wantPosts: 1},
		{name: "backup ends fail", prior: nullVolumeReference(), setup: func(f *volumeBackupRestoreFake) {
			f.backupAfter = []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusFail}
		}, wantSummary: "Error waiting for volume backup restore", wantDetail: "backup " + testVolumeBackup().Id.String() + " failed, so the restore failed", notDetail: "can still be running", wantPosts: 1},
		{name: "volume refuses before the request", prior: nullVolumeReference(), setup: func(f *volumeBackupRestoreFake) {
			f.volumeBefore = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusExtendFailed}
		}, wantSummary: "Error restoring volume backup", wantDetail: "volume status extend_failed refuses the request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newVolumeBackupRestoreFake(t)
				tt.setup(f)
				planned := restoreTestModel(volumeIDReference(a))
				planned.Name = types.StringValue("renamed")
				got, diags := f.update(f.resource(), restoreTestModel(tt.prior), planned)
				if !diags.HasError() || diags[0].Summary() != tt.wantSummary || !strings.Contains(diags[0].Detail(), tt.wantDetail) {
					t.Fatalf("diagnostics = %v, want %q: %q", diags, tt.wantSummary, tt.wantDetail)
				}
				if tt.notDetail != "" && strings.Contains(diags[0].Detail(), tt.notDetail) {
					t.Fatalf("diagnostics = %v, must not contain %q", diags, tt.notDetail)
				}
				if posts := len(f.restoreBodies); tt.wantPosts < 0 && posts < 2 || tt.wantPosts >= 0 && posts != tt.wantPosts {
					t.Fatalf("restore requests = %d, want %d (-1 is more than one)", posts, tt.wantPosts)
				}
				if !got.RestoreVolume.Equal(tt.prior) || got.ID.ValueString() != f.backup.Id.String() || got.Name.ValueString() != "renamed" {
					t.Fatalf("state restore_volume = %v id = %s name = %s, want %v", got.RestoreVolume, got.ID.ValueString(), got.Name.ValueString(), tt.prior)
				}
			})
		})
	}
}

// After a 202 the restore can still run, so every outcome that the API did not
// report as failed records the new restore_volume. Sending it again could
// overwrite newer data. The deadline covers the whole Update.
func TestVolumeBackupRestoreDeadlineAndUnknownOutcome(t *testing.T) {
	a := restoreTestVolumeID.String()
	for _, tt := range []struct {
		name         string
		timeouts     timeouts.Value
		setup        func(*volumeBackupRestoreFake)
		wantSummary  string
		wantDetails  []string
		wantElapsed  time.Duration
		wantPosts    int
		wantRecorded bool
	}{
		{name: "default deadline during the restore", timeouts: nullVolumeBackupTimeouts(), setup: func(f *volumeBackupRestoreFake) {
			f.volumeAfter = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring}
		}, wantSummary: "Error waiting for volume backup restore", wantDetails: []string{"timeout after 1h0m0s", "within timeouts.update (1h0m0s)", "can still be running", "records volume " + a}, wantElapsed: volumeBackupUpdateTimeout, wantPosts: 1, wantRecorded: true},
		// The idle wait uses 10 s of the 45 m deadline. The error still shows
		// the configured 45m0s, not the 44m50s that was left for the restore
		// wait.
		{name: "configured deadline shared with the idle wait", timeouts: volumeBackupTimeoutsValue("", "45m"), setup: func(f *volumeBackupRestoreFake) {
			f.volumeBefore = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAttaching, blockstoragesdk.VolumeStatusAvailable}
			f.volumeAfter = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring}
		}, wantSummary: "Error waiting for volume backup restore", wantDetails: []string{"timeout after 45m0s", "within timeouts.update (45m0s)", "can still be running"}, wantElapsed: 45 * time.Minute, wantPosts: 1, wantRecorded: true},
		{name: "volume read fails after the request", timeouts: nullVolumeBackupTimeouts(), setup: func(f *volumeBackupRestoreFake) {
			f.volumeMissingAfter = true
		}, wantSummary: "Error waiting for volume backup restore", wantDetails: []string{"read volume " + a, "can still be running"}, wantPosts: 1, wantRecorded: true},
		{name: "volume moves to another operation", timeouts: nullVolumeBackupTimeouts(), setup: func(f *volumeBackupRestoreFake) {
			f.volumeAfter = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAttaching}
		}, wantSummary: "Error waiting for volume backup restore", wantDetails: []string{"unexpected status attaching during restore", "can still be running"}, wantPosts: 1, wantRecorded: true},
		{name: "deadline before the request", timeouts: volumeBackupTimeoutsValue("", "20m"), setup: func(f *volumeBackupRestoreFake) {
			f.volumeBefore = []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAttaching}
		}, wantSummary: "Error restoring volume backup", wantDetails: []string{"within timeouts.update (20m0s)"}, wantElapsed: 20 * time.Minute, wantPosts: 0, wantRecorded: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newVolumeBackupRestoreFake(t)
				tt.setup(f)
				prior := restoreTestModel(nullVolumeReference())
				planned := restoreTestModel(volumeIDReference(a))
				planned.Timeouts = tt.timeouts
				start := time.Now()
				got, diags := f.update(f.resource(), prior, planned)
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Fatalf("elapsed %s, want %s", elapsed, tt.wantElapsed)
				}
				if !diags.HasError() || diags[0].Summary() != tt.wantSummary {
					t.Fatalf("diagnostics = %v, want %q", diags, tt.wantSummary)
				}
				for _, want := range tt.wantDetails {
					if !strings.Contains(diags[0].Detail(), want) {
						t.Fatalf("detail = %q, want %q", diags[0].Detail(), want)
					}
				}
				if posts := len(f.restoreBodies); posts != tt.wantPosts {
					t.Fatalf("restore requests = %d, want %d", posts, tt.wantPosts)
				}
				want := prior.RestoreVolume
				if tt.wantRecorded {
					want = planned.RestoreVolume
				}
				if !got.RestoreVolume.Equal(want) {
					t.Fatalf("state restore_volume = %v, want %v", got.RestoreVolume, want)
				}
			})
		})
	}
}

func TestVolumeBackupTimeoutsChangeSendsNoWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newVolumeBackupRestoreFake(t)
		prior := restoreTestModel(volumeIDReference(restoreTestVolumeID.String()))
		planned := prior
		planned.Timeouts = volumeBackupTimeoutsValue("90m", "2h")
		got, diags := f.update(f.resource(), prior, planned)
		if diags.HasError() {
			t.Fatalf("update: %v", diags)
		}
		backupPath := "/v2/block-storage/volume-backups/" + f.backup.Id.String() + "/"
		if want := []string{"GET " + backupPath}; !slices.Equal(f.calls, want) {
			t.Fatalf("calls = %v, want %v", f.calls, want)
		}
		if !got.Timeouts.Equal(planned.Timeouts) || !got.RestoreVolume.Equal(prior.RestoreVolume) {
			t.Fatalf("state timeouts = %v restore_volume = %v", got.Timeouts, got.RestoreVolume)
		}
	})
}

func TestVolumeBackupRestoreReplaysBusyRefusal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newVolumeBackupRestoreFake(t)
		f.restoreBusy = 2
		planned := restoreTestModel(volumeIDReference(restoreTestVolumeID.String()))
		start := time.Now()
		got, diags := f.update(f.resource(), restoreTestModel(nullVolumeReference()), planned)
		if diags.HasError() {
			t.Fatalf("update: %v", diags)
		}
		// The jitter rests at least half of 10 s and of 20 s before the third request.
		if len(f.restoreBodies) != 3 || time.Since(start) < 15*time.Second {
			t.Fatalf("restore requests = %d, elapsed %s; want 3, at least 15s", len(f.restoreBodies), time.Since(start))
		}
		if !got.RestoreVolume.Equal(planned.RestoreVolume) {
			t.Fatalf("state restore_volume = %v, want %v", got.RestoreVolume, planned.RestoreVolume)
		}
	})
}

func TestVolumeBackupRestoreIdleWait(t *testing.T) {
	for _, tt := range []struct {
		name        string
		volume      []blockstoragesdk.VolumeStatus
		backup      []blockstoragesdk.VolumeBackupStatus
		wantErr     string
		wantElapsed time.Duration
	}{
		{name: "idle", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusInUse}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}},
		{name: "volume attaching then idle", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAttaching, blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantElapsed: volumeBackupPollInterval},
		{name: "backup restoring then idle", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusRestoring, "", blockstoragesdk.VolumeBackupStatusAvailable}, wantElapsed: 2 * volumeBackupPollInterval},
		{name: "volume refuses", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoreFailed}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "volume " + restoreTestVolumeID.String() + " did not become idle: volume status restore_failed refuses the request"},
		{name: "backup failed", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusFail}, wantErr: "backup status fail refuses the restore"},
		{name: "backup deleting", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusDeleting}, wantErr: "backup status deleting refuses the restore"},
		{name: "backup unknown status", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{"strange"}, wantErr: "backup has unknown status strange"},
		{name: "timeout", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRetyping}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "timeout after 1h0m0s", wantElapsed: volumeBackupUpdateTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newVolumeBackupRestoreFake(t)
				f.volumeBefore, f.backupBefore = tt.volume, tt.backup
				start := time.Now()
				err := f.resource().waitUntilRestoreIdle(context.Background(), f.backup.Id, restoreTestVolumeID, volumeBackupUpdateTimeout)
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Fatalf("elapsed %s, want %s", elapsed, tt.wantElapsed)
				}
				if (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			})
		})
	}
}

func TestVolumeBackupRestoreWait(t *testing.T) {
	a := restoreTestVolumeID.String()
	for _, tt := range []struct {
		name        string
		volume      []blockstoragesdk.VolumeStatus
		backup      []blockstoragesdk.VolumeBackupStatus
		missing     bool
		wantErr     string
		wantErrIs   error
		wantElapsed time.Duration
	}{
		{name: "restoring to available", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring, blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantElapsed: volumeBackupPollInterval},
		{name: "restoring to in-use", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring, "", blockstoragesdk.VolumeStatusInUse}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantElapsed: 2 * volumeBackupPollInterval},
		{name: "backup still restoring", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusRestoring, "", blockstoragesdk.VolumeBackupStatusAvailable}, wantElapsed: 2 * volumeBackupPollInterval},
		{name: "restore_failed", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring, blockstoragesdk.VolumeStatusRestoreFailed}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "volume " + a + " restore failed", wantElapsed: volumeBackupPollInterval},
		{name: "failed", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusFailed}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "volume " + a + " restore failed"},
		{name: "status of another operation", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAttaching}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "volume " + a + " has unexpected status attaching during restore"},
		{name: "unknown volume status", volume: []blockstoragesdk.VolumeStatus{"strange"}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErr: "volume " + a + " has unknown status strange"},
		{name: "backup failed", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusFail}, wantErr: "backup " + testVolumeBackup().Id.String() + " failed"},
		{name: "backup updating", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusUpdating}, wantErr: "has unexpected status updating during restore"},
		{name: "unknown backup status", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{"strange"}, wantErr: "has unknown status strange"},
		{name: "volume not found", missing: true, volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusAvailable}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusAvailable}, wantErrIs: blockstoragesdk.ErrNotFound},
		{name: "timeout", volume: []blockstoragesdk.VolumeStatus{blockstoragesdk.VolumeStatusRestoring}, backup: []blockstoragesdk.VolumeBackupStatus{blockstoragesdk.VolumeBackupStatusRestoring}, wantErrIs: wait.ErrTimeout, wantElapsed: volumeBackupUpdateTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newVolumeBackupRestoreFake(t)
				f.restored, f.volumeAfter, f.backupAfter, f.volumeMissingAfter = true, tt.volume, tt.backup, tt.missing
				start := time.Now()
				backup, err := f.resource().waitUntilRestored(context.Background(), f.backup.Id, restoreTestVolumeID, volumeBackupUpdateTimeout)
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Fatalf("elapsed %s, want %s", elapsed, tt.wantElapsed)
				}
				switch {
				case tt.wantErr != "":
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("err = %v, want %q", err, tt.wantErr)
					}
				case tt.wantErrIs != nil:
					if !errors.Is(err, tt.wantErrIs) {
						t.Fatalf("err = %v, want %v", err, tt.wantErrIs)
					}
				default:
					if err != nil || *core.NullableToPtr(backup.Status) != blockstoragesdk.VolumeBackupStatusAvailable {
						t.Fatalf("restore wait: %v", err)
					}
				}
				if err != nil && !strings.HasPrefix(err.Error(), "volume backup "+f.backup.Id.String()+": ") {
					t.Fatalf("err = %v, want the volume backup prefix", err)
				}
			})
		})
	}
}

// The restore holds the claims lock of the target volume from the idle wait to
// the end of the restore wait.
func TestVolumeBackupRestoreHoldsVolumeLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newVolumeBackupRestoreFake(t)
		r := f.resource()
		ctx := context.Background()
		unlock, err := r.claims.Lock(ctx, restoreTestVolumeID.String())
		if err != nil {
			t.Fatal(err)
		}
		contended := make(chan int, 1)
		f.onRestore = func() {
			go func() {
				release, err := r.claims.Lock(ctx, restoreTestVolumeID.String())
				if err != nil {
					t.Error(err)
					return
				}
				contended <- len(f.calls)
				release()
			}()
		}
		done := make(chan diag.Diagnostics, 1)
		go func() {
			_, diags := f.update(r, restoreTestModel(nullVolumeReference()), restoreTestModel(volumeIDReference(restoreTestVolumeID.String())))
			done <- diags
		}()
		synctest.Wait()
		if len(f.calls) != 1 {
			t.Fatalf("the restore must wait for the volume lock, calls: %v", f.calls)
		}
		unlock()
		if diags := <-done; diags.HasError() {
			t.Fatalf("update: %v", diags)
		}
		if got := <-contended; got != len(f.calls) {
			t.Fatalf("the lock was free after %d of %d requests", got, len(f.calls))
		}
	})
}

func TestVolumeBackupReadKeepsRestoreVolume(t *testing.T) {
	t.Parallel()
	backup := testVolumeBackup()
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
	})
	r := &VolumeBackupResource{client: client, claims: keylock.New(), projectID: backup.Project.Id}
	for _, restoreVolume := range []types.Object{nullVolumeReference(), volumeIDReference(" " + strings.ToUpper(restoreTestVolumeID.String()))} {
		state := volumeBackupState(t, restoreTestModel(restoreVolume))
		response := resource.ReadResponse{State: state}
		r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
		var got VolumeBackupResourceModel
		response.Diagnostics.Append(response.State.Get(context.Background(), &got)...)
		if response.Diagnostics.HasError() || !got.RestoreVolume.Equal(restoreVolume) {
			t.Fatalf("restore_volume = %v, want %v: %v", got.RestoreVolume, restoreVolume, response.Diagnostics)
		}
	}
}

// Every SDK status has a class in the idle wait and the restore wait, so no
// SDK status fails as unknown.
func TestVolumeStatusClassification(t *testing.T) {
	t.Parallel()
	const ready, poll, fail = "ready", "poll", "fail"
	class := func(err error) string {
		switch {
		case err == nil:
			return ready
		case errors.Is(err, wait.ErrNotReady):
			return poll
		case strings.Contains(err.Error(), "unknown status"):
			return "unknown"
		default:
			return fail
		}
	}
	for status, want := range map[blockstoragesdk.VolumeStatus][2]string{
		blockstoragesdk.VolumeStatusAvailable:        {ready, ready},
		blockstoragesdk.VolumeStatusInUse:            {ready, ready},
		"":                                           {poll, poll},
		blockstoragesdk.VolumeStatusRestoring:        {poll, poll},
		blockstoragesdk.VolumeStatusCreating:         {poll, fail},
		blockstoragesdk.VolumeStatusAttaching:        {poll, fail},
		blockstoragesdk.VolumeStatusDetaching:        {poll, fail},
		blockstoragesdk.VolumeStatusBackingUp:        {poll, fail},
		blockstoragesdk.VolumeStatusRetyping:         {poll, fail},
		blockstoragesdk.VolumeStatusUpdating:         {poll, fail},
		blockstoragesdk.VolumeStatusUploading:        {poll, fail},
		blockstoragesdk.VolumeStatusFailed:           {fail, fail},
		blockstoragesdk.VolumeStatusRestoreFailed:    {fail, fail},
		blockstoragesdk.VolumeStatusAttachFailed:     {fail, fail},
		blockstoragesdk.VolumeStatusDetachFailed:     {fail, fail},
		blockstoragesdk.VolumeStatusExtendFailed:     {fail, fail},
		blockstoragesdk.VolumeStatusBackupFailed:     {fail, fail},
		blockstoragesdk.VolumeStatusDeleting:         {fail, fail},
		blockstoragesdk.VolumeStatusAwaitingTransfer: {fail, fail},
	} {
		if got := [2]string{class(volumeIdle(status)), class(volumeRestored(restoreTestVolumeID, status))}; got != want {
			t.Errorf("status %q: idle, restored = %v, want %v", status, got, want)
		}
	}
	for status, want := range map[blockstoragesdk.VolumeBackupStatus][2]string{
		blockstoragesdk.VolumeBackupStatusAvailable: {ready, ready},
		blockstoragesdk.VolumeBackupStatusRestoring: {poll, poll},
		blockstoragesdk.VolumeBackupStatusCreating:  {poll, fail},
		blockstoragesdk.VolumeBackupStatusUpdating:  {poll, fail},
		blockstoragesdk.VolumeBackupStatusDeleting:  {fail, fail},
		blockstoragesdk.VolumeBackupStatusFail:      {fail, fail},
	} {
		if got := [2]string{class(backupIdle(&status)), class(backupRestored(testVolumeBackup().Id, &status))}; got != want {
			t.Errorf("backup status %q: idle, restored = %v, want %v", status, got, want)
		}
	}
}
