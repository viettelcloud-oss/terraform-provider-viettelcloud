package blockstorage

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func snapshotResourceSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&VolumeSnapshotResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func TestVolumeSnapshotResourceSchemaAndMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := snapshotResourceSchema(t)
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &VolumeSnapshotResourceModel{Volume: nullVolumeReference()}); diags.HasError() {
		t.Fatalf("schema and model differ: %v", diags)
	}
	if _, ok := s.Attributes["volume_id"]; ok {
		t.Fatal("volume_id must be replaced by the volume reference")
	}
	assertVolumeIDReference(t, s.Attributes["volume"])

	snapshot := sampleVolumeSnapshot()
	var model VolumeSnapshotModel
	populateVolumeSnapshotModel(snapshot, &model)
	if model.ID.ValueString() != snapshot.Id.String() || model.VolumeName.ValueString() != "data" ||
		model.Size.ValueInt64() != 30 || model.Status.ValueString() != "available" || model.Zone.ValueString() != "zone-a" ||
		model.ProjectID.ValueString() != volumeTestProjectID.String() || !model.Description.IsNull() {
		t.Fatalf("snapshot response mapping: %#v", model)
	}
	empty := ""
	zero := 0
	snapshot.Description, snapshot.Size, snapshot.Status = &empty, &zero, nil
	populateVolumeSnapshotModel(snapshot, &model)
	if model.Description.IsNull() || model.Description.ValueString() != "" || model.Size.IsNull() || model.Size.ValueInt64() != 0 || !model.Status.IsNull() {
		t.Fatalf("response presence was not preserved: %#v", model)
	}
}

func TestPopulateVolumeSnapshotResourceStateRecordsVolumeReference(t *testing.T) {
	t.Parallel()
	snapshot := sampleVolumeSnapshot()
	upper := strings.ToUpper(volumeTestID.String())
	for _, tt := range []struct {
		name  string
		prior types.Object
		want  string
	}{
		{"import", nullVolumeReference(), volumeTestID.String()},
		{"configured spelling", volumeIDReference(upper), upper},
		{"different volume", volumeIDReference((core.UUID{4}).String()), volumeTestID.String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := VolumeSnapshotResourceModel{Volume: tt.prior}
			populateVolumeSnapshotResourceState(snapshot, &state)
			if !state.Volume.Equal(volumeIDReference(tt.want)) || state.VolumeName.ValueString() != "data" {
				t.Fatalf("volume = %v, volume_name = %v, want id %s", state.Volume, state.VolumeName, tt.want)
			}
		})
	}
}

func TestVolumeSnapshotCreateRequiresAKnownVolumeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := snapshotResourceSchema(t)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
		return nil, nil
	})
	for _, volume := range []types.Object{
		types.ObjectUnknown(reference.AttributeTypes(reference.ID)),
		volumeIDReference("not-a-uuid"),
	} {
		plan := tfsdk.Plan{Schema: s}
		if diags := plan.Set(ctx, &VolumeSnapshotResourceModel{VolumeSnapshotModel: VolumeSnapshotModel{Name: types.StringValue("snapshot")}, Volume: volume}); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		response := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		(&VolumeSnapshotResource{client: client, projectID: volumeTestProjectID}).Create(ctx, resource.CreateRequest{Plan: plan}, &response)
		if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "volume.id") {
			t.Fatalf("volume %v: expected a volume.id diagnostic, got %v", volume, response.Diagnostics)
		}
	}
}

func TestVolumeSnapshotPatchPresenceAndNormalization(t *testing.T) {
	t.Parallel()
	prior := VolumeSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("before")}
	for _, tt := range []struct {
		name                      string
		plan                      VolumeSnapshotModel
		wantName, wantDescription *string
	}{
		{"omit", VolumeSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringNull()}, nil, nil},
		{"unknown", VolumeSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringUnknown()}, nil, nil},
		{"clear", VolumeSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("")}, nil, new("")},
		{"rename", VolumeSnapshotModel{Name: types.StringValue(" renamed "), Description: types.StringValue("before")}, new("renamed"), nil},
		{"equal after trim", VolumeSnapshotModel{Name: types.StringValue(" snapshot "), Description: types.StringValue("before")}, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := buildVolumeSnapshotPatch(tt.plan, prior)
			if (got.Name == nil) != (tt.wantName == nil) || (got.Name != nil && *got.Name != *tt.wantName) ||
				(got.Description == nil) != (tt.wantDescription == nil) || (got.Description != nil && *got.Description != *tt.wantDescription) {
				t.Fatalf("patch: got %#v, want name=%v description=%v", got, tt.wantName, tt.wantDescription)
			}
		})
	}
}

func TestVolumeSnapshotResourceLifecycleUsesPublicSDK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := snapshotResourceSchema(t)
	snapshot := sampleVolumeSnapshot()
	requests := 0
	deletedFromBackend := false
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing Project-ID header on %s", req.URL)
		}
		path := "/v2/block-storage/volume-snapshots/"
		switch {
		case req.Method == http.MethodPost && req.URL.Path == path:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["name"] != "snapshot" || body["volume_id"] != volumeTestID.String() {
				t.Errorf("unexpected create body: %v", body)
			}
			if _, exists := body["description"]; exists {
				t.Error("unconfigured description was sent on create")
			}
			return testHTTPResponse(req, http.StatusCreated, volumeSnapshotJSON(snapshot)), nil
		case req.Method == http.MethodPatch && req.URL.Path == path+snapshot.Id.String()+"/":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["name"] != "renamed" || body["description"] != "" {
				t.Errorf("unexpected update body: %v", body)
			}
			snapshot.Name = "renamed"
			snapshot.Description = new("")
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		case req.Method == http.MethodGet && req.URL.Path == path+snapshot.Id.String()+"/":
			if deletedFromBackend {
				return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			}
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		case req.Method == http.MethodDelete && req.URL.Path == path+snapshot.Id.String()+"/":
			deletedFromBackend = true
			return testHTTPResponse(req, http.StatusAccepted, ""), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	r := &VolumeSnapshotResource{client: client, projectID: volumeTestProjectID, pollInterval: time.Millisecond, timeout: time.Second}
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &VolumeSnapshotResourceModel{VolumeSnapshotModel: VolumeSnapshotModel{
		Name: types.StringValue("snapshot"),
	}, Volume: volumeIDReference(volumeTestID.String())}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Config: tfsdk.Config{Raw: plan.Raw, Schema: s}, Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdState VolumeSnapshotResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.ID.ValueString() != snapshot.Id.String() {
		t.Fatalf("created state: %#v %v", createdState, diags)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("refresh: %v", read.Diagnostics)
	}
	updatedModel := createdState
	updatedModel.Name, updatedModel.Description = types.StringValue(" renamed "), types.StringValue("")
	updatePlan := tfsdk.Plan{Schema: s}
	if diags := updatePlan.Set(ctx, &updatedModel); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	var updatedState VolumeSnapshotResourceModel
	if diags := updated.State.Get(ctx, &updatedState); diags.HasError() || updatedState.Name.ValueString() != " renamed " || updatedState.Description.ValueString() != "" {
		t.Fatalf("updated state: %#v %v", updatedState, diags)
	}
	deleted := resource.DeleteResponse{State: tfsdk.State{Schema: s}}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || requests != 7 {
		t.Fatalf("delete: requests=%d diagnostics=%v", requests, deleted.Diagnostics)
	}
}

func TestVolumeSnapshotCreatePreservesIDWhenWaitFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := snapshotResourceSchema(t)
	snapshot := sampleVolumeSnapshot()
	status := blockstoragesdk.SnapshotStatusFail
	requests := 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.Method {
		case http.MethodPost:
			return testHTTPResponse(req, http.StatusCreated, volumeSnapshotJSON(snapshot)), nil
		case http.MethodGet:
			snapshot.Status = &status
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	r := &VolumeSnapshotResource{client: client, projectID: volumeTestProjectID}
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &VolumeSnapshotResourceModel{VolumeSnapshotModel: VolumeSnapshotModel{
		Name: types.StringValue("snapshot"),
	}, Volume: volumeIDReference(volumeTestID.String())}); diags.HasError() {
		t.Fatalf("set create plan: %v", diags)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() || requests != 2 {
		t.Fatalf("wait failure: requests=%d diagnostics=%v", requests, created.Diagnostics)
	}
	var state VolumeSnapshotResourceModel
	if diags := created.State.Get(ctx, &state); diags.HasError() || state.ID.ValueString() != snapshot.Id.String() || !state.Status.IsNull() {
		t.Fatalf("pending state lost the snapshot ID: %#v %v", state, diags)
	}
}

func TestVolumeSnapshotDeleteStopsOnTerminalFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		s := snapshotResourceSchema(t)
		snapshot := sampleVolumeSnapshot()
		status := blockstoragesdk.SnapshotStatusFail
		snapshot.Status = &status
		requests := 0
		client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			requests++
			switch req.Method {
			case http.MethodDelete:
				return testHTTPResponse(req, http.StatusAccepted, ""), nil
			case http.MethodGet:
				return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
			default:
				t.Fatalf("unexpected request %s %s", req.Method, req.URL)
				return nil, nil
			}
		})
		r := &VolumeSnapshotResource{client: client, projectID: volumeTestProjectID, pollInterval: volumeSnapshotPollInterval, timeout: volumeSnapshotTimeout}
		state := tfsdk.State{Schema: s}
		model := VolumeSnapshotResourceModel{Volume: nullVolumeReference()}
		populateVolumeSnapshotModel(snapshot, &model.VolumeSnapshotModel)
		if diags := state.Set(ctx, &model); diags.HasError() {
			t.Fatalf("set delete state: %v", diags)
		}
		start := time.Now()
		deleted := resource.DeleteResponse{State: state}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
		if !deleted.Diagnostics.HasError() || !strings.Contains(deleted.Diagnostics.Errors()[0].Detail(), "terminal status") || requests != 2 || time.Since(start) != 0 {
			t.Fatalf("delete must fail without polling delay: requests=%d elapsed=%s diagnostics=%v", requests, time.Since(start), deleted.Diagnostics)
		}
	})
}

func TestVolumeSnapshotResourceNotFoundAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := snapshotResourceSchema(t)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	r := &VolumeSnapshotResource{client: client, projectID: volumeTestProjectID}
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &VolumeSnapshotResourceModel{VolumeSnapshotModel: VolumeSnapshotModel{ID: types.StringValue((core.UUID{3}).String())}, Volume: nullVolumeReference()}); diags.HasError() {
		t.Fatal(diags)
	}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("read must remove missing snapshot: %v %#v", read.Diagnostics, read.State)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("delete missing snapshot: %v", deleted.Diagnostics)
	}
	imported := resource.ImportStateResponse{State: state}
	r.ImportState(ctx, resource.ImportStateRequest{ID: (core.UUID{3}).String()}, &imported)
	var id types.String
	if diags := imported.State.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() || id.ValueString() != (core.UUID{3}).String() {
		t.Fatalf("import ID: %v %v", id, diags)
	}
	invalid := resource.ImportStateResponse{State: state}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "not-a-uuid"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("invalid import ID must fail")
	}
}

func TestVolumeSnapshotResourceWaitsForReadyAndFailsTerminalStatus(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		snapshot := sampleVolumeSnapshot()
		checks := 0
		client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			checks++
			status := blockstoragesdk.SnapshotStatusCreating
			if checks > 1 {
				status = blockstoragesdk.SnapshotStatusAvailable
			}
			snapshot.Status = &status
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		})
		r := &VolumeSnapshotResource{client: client, projectID: volumeTestProjectID, pollInterval: volumeSnapshotPollInterval, timeout: volumeSnapshotTimeout}
		start := time.Now()
		if _, err := r.waitReady(ctx, snapshot.Id, nil); err != nil || checks != 2 {
			t.Fatalf("wait: calls=%d err=%v", checks, err)
		}
		if elapsed := time.Since(start); elapsed != volumeSnapshotPollInterval {
			t.Fatalf("waited %s, expected %s", elapsed, volumeSnapshotPollInterval)
		}
		if !volumeSnapshotPatchApplied(snapshot, &blockstoragesdk.VolumeSnapshotPartialUpdateSchema{Name: new("snapshot")}) ||
			volumeSnapshotPatchApplied(snapshot, &blockstoragesdk.VolumeSnapshotPartialUpdateSchema{Name: new("other")}) {
			t.Fatal("patch completion must compare the requested name")
		}
		status := blockstoragesdk.SnapshotStatusFail
		client = volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
			snapshot.Status = &status
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		})
		r.client = client
		if _, err := r.waitReady(ctx, snapshot.Id, nil); err == nil || !strings.Contains(err.Error(), "terminal status") {
			t.Fatalf("terminal failure: %v", err)
		}
	})
}
