package filestorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

const snapshotsPath = "/v2/file-storage/filesystem-snapshots/"

func filesystemSnapshotResourceSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&FilesystemSnapshotResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func filesystemReference(id string) types.Object {
	return reference.Value(reference.ID, types.StringValue(id))
}

func nullFilesystemReference() types.Object {
	return types.ObjectNull(reference.AttributeTypes(reference.ID))
}

// planFilesystemReference runs an object plan modifier for an update of an
// existing resource, the only case in which replacement can be planned.
func planFilesystemReference(t *testing.T, modifier planmodifier.Object, state, plan types.Object) bool {
	t.Helper()
	existing := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	response := planmodifier.ObjectResponse{PlanValue: plan}
	modifier.PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		State: tfsdk.State{Raw: existing}, Plan: tfsdk.Plan{Raw: existing}, Config: tfsdk.Config{Raw: existing},
		StateValue: state, PlanValue: plan, ConfigValue: plan,
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier: %v", response.Diagnostics)
	}
	return response.RequiresReplace
}

func filesystemSnapshotPlan(ctx context.Context, t *testing.T, s resourceschema.Schema, model FilesystemSnapshotResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func filesystemSnapshotState(ctx context.Context, t *testing.T, s resourceschema.Schema, model FilesystemSnapshotResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func TestFilesystemSnapshotResourceSchema(t *testing.T) {
	t.Parallel()
	s := filesystemSnapshotResourceSchema(t)
	filesystemSnapshotPlan(context.Background(), t, s, FilesystemSnapshotResourceModel{Filesystem: nullFilesystemReference()})

	if name := s.Attributes["name"].(resourceschema.StringAttribute); !name.Required {
		t.Fatalf("name must be required: %#v", name)
	}
	if description := s.Attributes["description"].(resourceschema.StringAttribute); !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 {
		t.Fatalf("description must be optional, computed, and keep its state: %#v", description)
	}
	filesystem := s.Attributes["filesystem"].(resourceschema.SingleNestedAttribute)
	if !filesystem.Required || len(filesystem.PlanModifiers) != 1 {
		t.Fatalf("filesystem must be a required reference with one plan modifier: %#v", filesystem)
	}
	if criterion, ok := filesystem.Attributes[reference.ID].(resourceschema.StringAttribute); !ok || !criterion.Required || len(filesystem.Attributes) != 1 {
		t.Fatalf("filesystem must hold one required id: %#v", filesystem.Attributes)
	}
	current, changed := filesystemReference(testFilesystemID.String()), filesystemReference((core.UUID{4}).String())
	if !planFilesystemReference(t, filesystem.PlanModifiers[0], current, changed) {
		t.Fatal("changing filesystem.id must replace the snapshot")
	}
	if planFilesystemReference(t, filesystem.PlanModifiers[0], current, current) {
		t.Fatal("an unchanged filesystem.id must not replace the snapshot")
	}
	if planFilesystemReference(t, filesystem.PlanModifiers[0], current, filesystemReference("  "+strings.ToUpper(testFilesystemID.String())+" ")) {
		t.Fatal("another spelling of filesystem.id must not replace the snapshot")
	}

	for name, keepsState := range map[string]bool{
		"id": true, "filesystem_name": true, "size": true, "owner_id": true, "project_id": true, "created_at": true,
		"from_policy": false, "status": false, "updated_at": false,
	} {
		var computed bool
		var modifiers int
		switch attribute := s.Attributes[name].(type) {
		case resourceschema.StringAttribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		case resourceschema.Int64Attribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		case resourceschema.BoolAttribute:
			computed, modifiers = attribute.Computed && !attribute.Optional, len(attribute.PlanModifiers)
		default:
			t.Fatalf("%s has unexpected type %T", name, attribute)
		}
		if !computed || (modifiers == 1) != keepsState {
			t.Errorf("%s: computed=%v plan modifiers=%d, want UseStateForUnknown=%v", name, computed, modifiers, keepsState)
		}
	}
}

func TestFilesystemSnapshotResponseMapping(t *testing.T) {
	t.Parallel()
	snapshot := sampleFilesystemSnapshot()
	var model FilesystemSnapshotModel
	populateFilesystemSnapshotModel(snapshot, &model)
	if model.ID.ValueString() != testSnapshotID.String() || model.Name.ValueString() != "snapshot" ||
		model.FilesystemName.ValueString() != "data" || model.FromPolicy.ValueBool() || model.Status.ValueString() != "available" ||
		model.Size.ValueInt64() != 50 || model.OwnerID.ValueString() != (core.UUID{5}).String() ||
		model.ProjectID.ValueString() != testProjectID.String() || model.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" ||
		model.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" || !model.Description.IsNull() {
		t.Fatalf("snapshot response mapping: %#v", model)
	}
	snapshot.Description = core.NullableFromPtr(new(""))
	populateFilesystemSnapshotModel(snapshot, &model)
	if model.Description.IsNull() || model.Description.ValueString() != "" {
		t.Fatalf("an empty description must map to a known empty string: %#v", model.Description)
	}
}

func TestPopulateFilesystemSnapshotResourceStatePreservesConfiguredValues(t *testing.T) {
	t.Parallel()
	snapshot := sampleFilesystemSnapshot()
	snapshot.Description = core.NullableFromPtr(new("before"))
	upper := strings.ToUpper(testFilesystemID.String())
	for _, tt := range []struct {
		name                 string
		prior                FilesystemSnapshotResourceModel
		wantName, wantFSID   string
		wantDescriptionValue string
	}{
		{"import", FilesystemSnapshotResourceModel{Filesystem: nullFilesystemReference()}, "snapshot", testFilesystemID.String(), "before"},
		{"configured spelling", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue(" snapshot "), Description: types.StringValue("before")},
			Filesystem:              filesystemReference(upper),
		}, " snapshot ", upper, "before"},
		{"padded description", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue(" before ")},
			Filesystem:              filesystemReference(testFilesystemID.String()),
		}, "snapshot", testFilesystemID.String(), " before "},
		{"backend drift", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("old"), Description: types.StringValue("old")},
			Filesystem:              filesystemReference((core.UUID{4}).String()),
		}, "snapshot", testFilesystemID.String(), "before"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.prior
			populateFilesystemSnapshotResourceState(snapshot, &state)
			if state.Name.ValueString() != tt.wantName || !state.Filesystem.Equal(filesystemReference(tt.wantFSID)) ||
				state.Description.ValueString() != tt.wantDescriptionValue {
				t.Fatalf("state: name=%v filesystem=%v description=%v", state.Name, state.Filesystem, state.Description)
			}
		})
	}
}

func TestFilesystemSnapshotName(t *testing.T) {
	t.Parallel()
	for _, value := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("  ")} {
		if _, diags := filesystemSnapshotName(value); !diags.HasError() {
			t.Errorf("name %v must be rejected", value)
		}
	}
	if name, diags := filesystemSnapshotName(types.StringValue(" snapshot ")); diags.HasError() || name != "snapshot" {
		t.Fatalf("trimmed name: %q %v", name, diags)
	}
}

func TestBuildFilesystemSnapshotUpdateBody(t *testing.T) {
	t.Parallel()
	prior := FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("before")}
	for _, tt := range []struct {
		name                      string
		plan                      FilesystemSnapshotModel
		wantName, wantDescription *string
	}{
		{"omit description", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringNull()}, nil, nil},
		{"unknown description", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringUnknown()}, nil, nil},
		{"unchanged", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("before")}, nil, nil},
		{"clear description", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("")}, new("snapshot"), new("")},
		{"change description", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("after")}, new("snapshot"), new("after")},
		{"rename", FilesystemSnapshotModel{Name: types.StringValue(" renamed "), Description: types.StringValue("before")}, new("renamed"), nil},
		{"equal after trim", FilesystemSnapshotModel{Name: types.StringValue(" snapshot "), Description: types.StringValue("before")}, nil, nil},
		{"rename and change description", FilesystemSnapshotModel{Name: types.StringValue(" renamed "), Description: types.StringValue("after")}, new("renamed"), new("after")},
		{"padded description", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue(" after ")}, new("snapshot"), new("after")},
		{"description equal after trim", FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue(" before ")}, nil, nil},
		{"unknown name", FilesystemSnapshotModel{Name: types.StringUnknown(), Description: types.StringValue("before")}, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := buildFilesystemSnapshotUpdateBody(tt.plan, prior)
			assertNullableString(t, "name", body.Name.IsSpecified(), core.NullableToPtr(body.Name), tt.wantName)
			assertNullableString(t, "description", body.Description.IsSpecified(), core.NullableToPtr(body.Description), tt.wantDescription)
		})
	}
}

func assertNullableString(t *testing.T, field string, specified bool, got, want *string) {
	t.Helper()
	if specified != (want != nil) || (want != nil && (got == nil || *got != *want)) {
		t.Fatalf("%s: specified=%v value=%v, want %v", field, specified, got, want)
	}
}

func TestFilesystemSnapshotValidateConfigChecksFilesystemID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	for _, tt := range []struct {
		name       string
		filesystem types.Object
		wantErr    bool
	}{
		{"valid", filesystemReference(testFilesystemID.String()), false},
		{"padded", filesystemReference("  " + testFilesystemID.String() + " "), false},
		{"invalid", filesystemReference("not-a-uuid"), true},
		{"unknown reference", types.ObjectUnknown(reference.AttributeTypes(reference.ID)), false},
		{"unknown ID", reference.Of(map[string]types.String{reference.ID: types.StringUnknown()}), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := filesystemSnapshotState(ctx, t, s, FilesystemSnapshotResourceModel{
				FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot")}, Filesystem: tt.filesystem,
			})
			var response resource.ValidateConfigResponse
			(&FilesystemSnapshotResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &response)
			if response.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", response.Diagnostics, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(response.Diagnostics[0].Detail(), "filesystem.id") {
				t.Fatalf("the diagnostic must name filesystem.id: %v", response.Diagnostics)
			}
		})
	}
}

func TestFilesystemSnapshotCreateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	})
	r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
	for _, tt := range []struct {
		name  string
		model FilesystemSnapshotResourceModel
		want  string
	}{
		{"invalid filesystem id", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot")}, Filesystem: filesystemReference("not-a-uuid"),
		}, "filesystem.id"},
		{"unknown filesystem", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot")}, Filesystem: types.ObjectUnknown(reference.AttributeTypes(reference.ID)),
		}, "filesystem.id"},
		{"blank name", FilesystemSnapshotResourceModel{
			FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("  ")}, Filesystem: filesystemReference(testFilesystemID.String()),
		}, "non-empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: filesystemSnapshotPlan(ctx, t, s, tt.model)}, &response)
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), tt.want) {
				t.Fatalf("want a diagnostic about %q, got %v", tt.want, response.Diagnostics)
			}
		})
	}
}

// A synctest bubble makes a waiter regression fail at once on the fake
// clock instead of polling for the real 20-minute timeout.
func TestFilesystemSnapshotResourceLifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, checkFilesystemSnapshotResourceLifecycle)
}

func checkFilesystemSnapshotResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	snapshot := sampleFilesystemSnapshot()
	itemPath := snapshotsPath + testSnapshotID.String() + "/"
	var calls []string
	deleted := false
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method)
		if req.Header.Get("Project-ID") != testProjectID.String() {
			t.Errorf("missing Project-ID header on %s %s", req.Method, req.URL)
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == snapshotsPath:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["name"] != "snapshot" || body["filesystem_id"] != testFilesystemID.String() {
				t.Errorf("create body must hold only the trimmed name and filesystem_id: %v", body)
			}
			created := *snapshot
			created.Status = filestoragesdk.FilesystemSnapshotStatusCreating
			return testHTTPResponse(req, http.StatusCreated, filesystemSnapshotJSON(t, &created)), nil
		case req.Method == http.MethodPut && req.URL.Path == itemPath:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["name"] != "renamed" || body["description"] != "" {
				t.Errorf("update body must hold the trimmed name and the cleared description: %v", body)
			}
			snapshot.Name, snapshot.Description = "renamed", core.NullableFromPtr(new(""))
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			if deleted {
				return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			}
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		case req.Method == http.MethodDelete && req.URL.Path == itemPath:
			deleted = true
			return testHTTPResponse(req, http.StatusAccepted, ""), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}

	plan := filesystemSnapshotPlan(ctx, t, s, FilesystemSnapshotResourceModel{
		FilesystemSnapshotModel: FilesystemSnapshotModel{
			ID: types.StringUnknown(), Name: types.StringValue("snapshot"), Description: types.StringUnknown(),
			FilesystemName: types.StringUnknown(), FromPolicy: types.BoolUnknown(), Status: types.StringUnknown(),
			Size: types.Int64Unknown(), OwnerID: types.StringUnknown(), ProjectID: types.StringUnknown(),
			CreatedAt: types.StringUnknown(), UpdatedAt: types.StringUnknown(),
		},
		Filesystem: filesystemReference(testFilesystemID.String()),
	})
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	var createdState FilesystemSnapshotResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() || createdState.ID.ValueString() != testSnapshotID.String() ||
		createdState.Status.ValueString() != "available" || !createdState.Description.IsNull() || createdState.Size.ValueInt64() != 50 {
		t.Fatalf("created state: %#v %v", createdState, diags)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("refresh: %v", read.Diagnostics)
	}

	unchanged := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPlan(ctx, t, s, createdState), State: created.State}, &unchanged)
	if unchanged.Diagnostics.HasError() {
		t.Fatalf("update without changes: %v", unchanged.Diagnostics)
	}

	updatedModel := createdState
	updatedModel.Name, updatedModel.Description = types.StringValue(" renamed "), types.StringValue("")
	updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPlan(ctx, t, s, updatedModel), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update: %v", updated.Diagnostics)
	}
	var updatedState FilesystemSnapshotResourceModel
	if diags := updated.State.Get(ctx, &updatedState); diags.HasError() || updatedState.ID.ValueString() != testSnapshotID.String() ||
		updatedState.Name.ValueString() != " renamed " || updatedState.Description.IsNull() || updatedState.Description.ValueString() != "" {
		t.Fatalf("updated state: %#v %v", updatedState, diags)
	}

	removed := resource.DeleteResponse{State: updated.State}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &removed)
	if removed.Diagnostics.HasError() {
		t.Fatalf("delete: %v", removed.Diagnostics)
	}
	want := []string{http.MethodPost, http.MethodGet, http.MethodGet, http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodGet}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", calls, want)
	}
}

// A synctest bubble makes a waiter regression fail at once on the fake
// clock instead of polling for the real 20-minute timeout.
func TestFilesystemSnapshotCreatePreservesIDWhenWaitFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, checkFilesystemSnapshotCreatePreservesIDWhenWaitFails)
}

func checkFilesystemSnapshotCreatePreservesIDWhenWaitFails(t *testing.T) {
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	snapshot := sampleFilesystemSnapshot()
	requests := 0
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.Method {
		case http.MethodPost:
			snapshot.Status = filestoragesdk.FilesystemSnapshotStatusCreating
			return testHTTPResponse(req, http.StatusCreated, filesystemSnapshotJSON(t, snapshot)), nil
		case http.MethodGet:
			snapshot.Status = filestoragesdk.FilesystemSnapshotStatusFail
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
	plan := filesystemSnapshotPlan(ctx, t, s, FilesystemSnapshotResourceModel{
		FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue("before")},
		Filesystem:              filesystemReference(testFilesystemID.String()),
	})
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() || !strings.Contains(created.Diagnostics.Errors()[0].Detail(), "terminal status") || requests != 2 {
		t.Fatalf("wait failure: requests=%d diagnostics=%v", requests, created.Diagnostics)
	}
	var state FilesystemSnapshotResourceModel
	if diags := created.State.Get(ctx, &state); diags.HasError() || state.ID.ValueString() != testSnapshotID.String() ||
		!state.Status.IsNull() || state.Description.ValueString() != "before" {
		t.Fatalf("pending state lost the snapshot ID: %#v %v", state, diags)
	}
}

func TestFilesystemSnapshotResourceNotFoundAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
	state := filesystemSnapshotState(ctx, t, s, FilesystemSnapshotResourceModel{
		FilesystemSnapshotModel: FilesystemSnapshotModel{ID: types.StringValue(testSnapshotID.String())}, Filesystem: nullFilesystemReference(),
	})

	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("read must remove a missing snapshot: %v %#v", read.Diagnostics, read.State)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("delete of a missing snapshot must succeed: %v", deleted.Diagnostics)
	}

	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: testSnapshotID.String()}, &imported)
	var id types.String
	if diags := imported.State.GetAttribute(ctx, path.Root("id"), &id); imported.Diagnostics.HasError() || diags.HasError() || id.ValueString() != testSnapshotID.String() {
		t.Fatalf("import ID: %v %v %v", id, imported.Diagnostics, diags)
	}
}

func TestFilesystemSnapshotWaitReady(t *testing.T) {
	t.Parallel()
	const missing, serverError = "missing", "server error"
	creating, available, fail := string(filestoragesdk.FilesystemSnapshotStatusCreating),
		string(filestoragesdk.FilesystemSnapshotStatusAvailable), string(filestoragesdk.FilesystemSnapshotStatusFail)
	for _, tt := range []struct {
		name     string
		polls    []string
		wantErr  error
		wantText string
		elapsed  time.Duration
	}{
		{"not visible then creating then available", []string{missing, creating, available}, nil, "", 2 * filesystemSnapshotPollInterval},
		{"fail is terminal", []string{fail}, nil, "terminal status", 0},
		{"SDK error is fatal", []string{serverError}, nil, "filesystem snapshot", 0},
		{"stays creating until timeout", nil, wait.ErrTimeout, "", filesystemSnapshotTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				snapshot := sampleFilesystemSnapshot()
				polls := 0
				client := testClient(t, func(req *http.Request) (*http.Response, error) {
					step := creating
					if polls < len(tt.polls) {
						step = tt.polls[polls]
					}
					polls++
					switch step {
					case missing:
						return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
					case serverError:
						return testHTTPResponse(req, http.StatusInternalServerError, `{"detail":"error"}`), nil
					}
					snapshot.Status = filestoragesdk.FilesystemSnapshotStatus(step)
					return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
				})
				r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
				start := time.Now()
				got, err := r.waitReady(context.Background(), testSnapshotID)
				if elapsed := time.Since(start); elapsed != tt.elapsed {
					t.Fatalf("waited %s, want %s", elapsed, tt.elapsed)
				}
				switch {
				case tt.wantErr != nil:
					if !errors.Is(err, tt.wantErr) {
						t.Fatalf("err = %v, want %v", err, tt.wantErr)
					}
				case tt.wantText != "":
					if err == nil || errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), tt.wantText) {
						t.Fatalf("err = %v, want an immediate error with %q", err, tt.wantText)
					}
				default:
					if err != nil || got == nil || got.Status != filestoragesdk.FilesystemSnapshotStatusAvailable {
						t.Fatalf("wait: %#v %v", got, err)
					}
				}
			})
		})
	}
}

func TestFilesystemSnapshotWaitDeleted(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		goneAfter int
		wantErr   error
		elapsed   time.Duration
	}{
		{"deleting then not found", 1, nil, filesystemSnapshotPollInterval},
		{"stays deleting until timeout", -1, wait.ErrTimeout, filesystemSnapshotTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				snapshot := sampleFilesystemSnapshot()
				snapshot.Status = filestoragesdk.FilesystemSnapshotStatusDeleting
				polls := 0
				client := testClient(t, func(req *http.Request) (*http.Response, error) {
					polls++
					if tt.goneAfter >= 0 && polls > tt.goneAfter {
						return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
					}
					return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
				})
				r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
				start := time.Now()
				err := r.waitDeleted(context.Background(), testSnapshotID)
				if elapsed := time.Since(start); elapsed != tt.elapsed {
					t.Fatalf("waited %s, want %s", elapsed, tt.elapsed)
				}
				if (tt.wantErr == nil && err != nil) || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			})
		})
	}
}

// The requests carry the trimmed description, and state after create, read,
// and update keeps the configured spelling.
func TestFilesystemSnapshotResourceTrimsDescriptionAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, checkFilesystemSnapshotResourceTrimsDescription)
}

func checkFilesystemSnapshotResourceTrimsDescription(t *testing.T) {
	ctx := context.Background()
	s := filesystemSnapshotResourceSchema(t)
	snapshot := sampleFilesystemSnapshot()
	itemPath := snapshotsPath + testSnapshotID.String() + "/"
	var sent []string
	var sentFilesystemID any
	// The fake backend trims the description, as the API does.
	storeDescription := func(req *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode %s body: %v", req.Method, err)
			return
		}
		if filesystemID, ok := body["filesystem_id"]; ok {
			sentFilesystemID = filesystemID
		}
		description, ok := body["description"].(string)
		if !ok {
			t.Errorf("%s body has no description: %#v", req.Method, body)
			return
		}
		sent = append(sent, description)
		snapshot.Description = core.NullableFromPtr(new(strings.TrimSpace(description)))
	}
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == snapshotsPath:
			storeDescription(req)
			return testHTTPResponse(req, http.StatusCreated, filesystemSnapshotJSON(t, snapshot)), nil
		case req.Method == http.MethodPut && req.URL.Path == itemPath:
			storeDescription(req)
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		case req.Method == http.MethodGet && req.URL.Path == itemPath:
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	r := &FilesystemSnapshotResource{client: client, projectID: testProjectID}
	description := func(t *testing.T, state tfsdk.State) string {
		t.Helper()
		var model FilesystemSnapshotResourceModel
		if diags := state.Get(ctx, &model); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		return model.Description.ValueString()
	}

	paddedFilesystem := filesystemReference("  " + testFilesystemID.String() + " ")
	filesystem := func(t *testing.T, state tfsdk.State) types.Object {
		t.Helper()
		var model FilesystemSnapshotResourceModel
		if diags := state.Get(ctx, &model); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		return model.Filesystem
	}

	plan := filesystemSnapshotPlan(ctx, t, s, FilesystemSnapshotResourceModel{
		FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot"), Description: types.StringValue(" saved ")},
		Filesystem:              paddedFilesystem,
	})
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create: %v", created.Diagnostics)
	}
	if got := description(t, created.State); got != " saved " || strings.Join(sent, "|") != "saved" || sentFilesystemID != testFilesystemID.String() {
		t.Fatalf("after create: state %q, sent %q, filesystem_id %v", got, sent, sentFilesystemID)
	}
	if got := filesystem(t, created.State); !got.Equal(paddedFilesystem) {
		t.Fatalf("after create: filesystem %v, want the configured spelling", got)
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || description(t, read.State) != " saved " || !filesystem(t, read.State).Equal(paddedFilesystem) {
		t.Fatalf("after read: state %q filesystem %v %v", description(t, read.State), filesystem(t, read.State), read.Diagnostics)
	}

	current := read.State
	for _, step := range []struct {
		description string
		wantSent    string
	}{
		{" after ", "saved|after"},
		// A change in outer whitespace only sends no request.
		{"after", "saved|after"},
	} {
		var model FilesystemSnapshotResourceModel
		if diags := current.Get(ctx, &model); diags.HasError() {
			t.Fatalf("get state: %v", diags)
		}
		model.Description = types.StringValue(step.description)
		updated := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: filesystemSnapshotPlan(ctx, t, s, model), State: current}, &updated)
		if updated.Diagnostics.HasError() {
			t.Fatalf("update to %q: %v", step.description, updated.Diagnostics)
		}
		if got := description(t, updated.State); got != step.description || strings.Join(sent, "|") != step.wantSent {
			t.Fatalf("after update to %q: state %q, sent %q", step.description, got, sent)
		}
		current = updated.State
	}
}
