package blockstorage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func volumeBackupResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeBackupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	return response.Schema
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
	state := tfsdk.State{Schema: resp.Schema}
	if diags := state.Set(context.Background(), &VolumeBackupResourceModel{Volume: nullVolumeReference()}); diags.HasError() {
		t.Fatalf("model/schema mismatch: %v", diags)
	}
}

func TestVolumeBackupImportState(t *testing.T) {
	var schemaResponse resource.SchemaResponse
	r := &VolumeBackupResource{}
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	if diags := response.State.Set(context.Background(), &VolumeBackupResourceModel{Volume: nullVolumeReference()}); diags.HasError() {
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
	state := VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("before"), Description: types.StringValue("before")}, Volume: nullVolumeReference()}
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
	state := VolumeBackupResourceModel{Volume: volumeIDReference(backup.Volume.Id.String())}
	populateVolumeBackupResourceState(backup, &state)
	if state.ID.ValueString() != backup.Id.String() || reference.Get(state.Volume, reference.ID).ValueString() != backup.Volume.Id.String() || state.VolumeName.ValueString() != "volume" || state.Size.ValueInt64() != 30 || state.Status.ValueString() != "available" || state.PolicyID.IsNull() {
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
	volumeID := testVolumeBackup().Volume.Id
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
			state := VolumeBackupResourceModel{Volume: tt.prior}
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
		{"invalid", volumeIDReference("not-a-uuid"), true},
		{"unknown reference", types.ObjectUnknown(reference.AttributeTypes(reference.ID)), false},
		{"unknown ID", reference.Of(map[string]types.String{reference.ID: types.StringUnknown()}), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := volumeBackupState(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup")}, Volume: tt.volume})
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
	_, err := client.CreateVolumeBackup(context.Background(), blockstoragesdk.CreateVolumeBackupParams{ProjectID: backup.Project.Id}, blockstoragesdk.VolumeBackupCreateSchema{Name: "backup", VolumeId: backup.Volume.Id})
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
				backup.Status = &status
			}
			if polls == 2 {
				status := blockstoragesdk.VolumeBackupStatusAvailable
				backup.Status = &status
			}
			if polls == 3 {
				return testHTTPResponse(req, 404, `{}`), nil
			}
			return testHTTPResponse(req, 200, backupResponseJSON(backup)), nil
		})
		r := &VolumeBackupResource{client: client, projectID: backup.Project.Id}
		start := time.Now()
		result, err := r.waitUntilAvailable(context.Background(), backup.Id)
		if err != nil || result.Status == nil || *result.Status != blockstoragesdk.VolumeBackupStatusAvailable {
			t.Fatalf("create waiter: %v", err)
		}
		if time.Since(start) != volumeBackupPollInterval {
			t.Fatalf("poll elapsed %s", time.Since(start))
		}
		if err := r.waitUntilDeleted(context.Background(), backup.Id); err != nil {
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
		r := &VolumeBackupResource{client: client, projectID: backup.Project.Id}
		name := " renamed "
		start := time.Now()
		result, err := r.waitUntilAvailableMatching(context.Background(), backup.Id, &blockstoragesdk.VolumeBackupPartialUpdateSchema{Name: &name})
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
			if req.Method != http.MethodPost || req.URL.Path != collection {
				t.Fatalf("wrong create request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "backup" || body["volume_id"] != backup.Volume.Id.String() || body["description"] != "saved" {
				t.Fatalf("wrong create body: %#v", body)
			}
			creating := blockstoragesdk.VolumeBackupStatusCreating
			backup.Status = &creating
			return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
		case 2, 3:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong read request: %s %s", req.Method, req.URL.Path)
			}
			available := blockstoragesdk.VolumeBackupStatusAvailable
			backup.Status = &available
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 4:
			if req.Method != http.MethodPatch || req.URL.Path != item {
				t.Fatalf("wrong update request: %s %s", req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["name"] != " renamed " || body["description"] != "" {
				t.Fatalf("wrong patch body: %#v", body)
			}
			backup.Name = "renamed"
			empty := ""
			backup.Description = &empty
			updating := blockstoragesdk.VolumeBackupStatusUpdating
			backup.Status = &updating
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 5:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong update poll: %s %s", req.Method, req.URL.Path)
			}
			available := blockstoragesdk.VolumeBackupStatusAvailable
			backup.Status = &available
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		case 6:
			if req.Method != http.MethodDelete || req.URL.Path != item {
				t.Fatalf("wrong delete request: %s %s", req.Method, req.URL.Path)
			}
			return testHTTPResponse(req, http.StatusAccepted, ""), nil
		case 7:
			if req.Method != http.MethodGet || req.URL.Path != item {
				t.Fatalf("wrong delete poll: %s %s", req.Method, req.URL.Path)
			}
			return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
		default:
			t.Fatalf("unexpected request %d: %s %s", requests, req.Method, req.URL.Path)
			return nil, nil
		}
	})
	r := &VolumeBackupResource{client: client, projectID: backup.Project.Id}
	createPlan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
		Name: types.StringValue("backup"), Description: types.StringValue("saved"),
	}, Volume: volumeIDReference(backup.Volume.Id.String())})
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
	if refreshedModel.ID.ValueString() != backup.Id.String() || reference.Get(refreshedModel.Volume, reference.ID).ValueString() != backup.Volume.Id.String() || refreshedModel.Region.ValueString() != "region" {
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
	if deleted.Diagnostics.HasError() || requests != 7 {
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
				backup.Status = &tt.backupStatus
				client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPost {
						return testHTTPResponse(req, http.StatusCreated, backupResponseJSON(backup)), nil
					}
					return testHTTPResponse(req, tt.status, backupResponseJSON(backup)), nil
				})
				r := &VolumeBackupResource{client: client, projectID: backup.Project.Id}
				plan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
					Name: types.StringValue("backup"),
				}, Volume: volumeIDReference(backup.Volume.Id.String())})
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
	model := VolumeBackupResourceModel{Volume: volumeIDReference(backup.Volume.Id.String())}
	populateVolumeBackupResourceState(backup, &model)
	state := volumeBackupState(t, model)

	notFound := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{}`), nil
	})
	r := &VolumeBackupResource{client: notFound, projectID: backup.Project.Id}
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
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	r.client = errClient
	createPlan := volumeBackupPlan(t, VolumeBackupResourceModel{VolumeBackupModel: VolumeBackupModel{
		Name: types.StringValue("backup"),
	}, Volume: volumeIDReference(backup.Volume.Id.String())})
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
