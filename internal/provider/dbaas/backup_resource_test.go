package dbaas

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func testBackupConfig() BackupResourceModel {
	m := BackupResourceModel{DBInstance: reference.Value(reference.ID, types.StringValue(testInstanceID.String()))}
	m.Name = types.StringValue("nightly")
	return m
}

func testBackupState() BackupResourceModel {
	m := testBackupConfig()
	populateBackupResourceState(sampleBackup(), &m)
	return m
}

func nullBackupRaw(t *testing.T) tftypes.Value {
	t.Helper()
	return tftypes.NewValue(backupSchema(t).Type().TerraformType(context.Background()), nil)
}

func TestBackupResourceSchema(t *testing.T) {
	t.Parallel()
	attrs := backupSchema(t).Attributes
	if name := attrs["name"].(schema.StringAttribute); !name.Required || len(name.PlanModifiers) != 1 {
		t.Fatal("name must be Required and replace the backup")
	}
	if instance := attrs["db_instance"].(schema.SingleNestedAttribute); !instance.Required || len(instance.PlanModifiers) != 1 {
		t.Fatal("db_instance must be a Required reference that replaces the backup")
	}
	if description := attrs["description"].(schema.StringAttribute); !description.Optional || !description.Computed || len(description.PlanModifiers) != 1 {
		t.Fatal("description must be Optional and Computed with UseStateForUnknown")
	}
	if backupState(t, testBackupState()).Raw.IsNull() {
		t.Fatal("model/schema mismatch")
	}
}

// The design updates only description in place. A new name replaces the
// backup by product decision, although the backend could rename it.
func TestBackupResourceUpdatesInPlaceOnlyDesignFields(t *testing.T) {
	t.Parallel()
	instance := func(id string) types.Object { return reference.Value(reference.ID, types.StringValue(id)) }
	assertInPlaceContract(t, backupSchema(t).Attributes, backupState(t, testBackupState()),
		map[string]bool{"description": true}, nil,
		map[string][2]attr.Value{
			"name":        {types.StringValue("nightly"), types.StringValue("nightly-2")},
			"description": {types.StringValue("before upgrade"), types.StringValue("")},
			"db_instance": {instance(testInstanceID.String()), instance(testOtherID.String())},
		})
}

func TestBackupResourceTypeName(t *testing.T) {
	t.Parallel()
	var resp resource.MetadataResponse
	NewBackupResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_db_backup" {
		t.Fatalf("type = %q, want viettelcloud_db_backup", resp.TypeName)
	}
}

func TestBackupInstanceReplacesOnlyForAnotherInstance(t *testing.T) {
	t.Parallel()
	assertIDReferenceReplacement(t, backupSchema(t).Attributes["db_instance"], path.Root("db_instance"), backupState(t, testBackupState()), testInstanceID.String())
}

// A change of outer whitespace names the same backup, so it must not replace
// the backup.
func TestBackupNameReplacesOnlyForAnotherName(t *testing.T) {
	t.Parallel()
	full := backupState(t, testBackupState())
	attribute := backupSchema(t).Attributes["name"]
	for planned, want := range map[string]bool{"nightly": false, " nightly ": false, "weekly": true} {
		value := types.StringValue(planned)
		if _, replace := planChange(t, attribute, path.Root("name"), full, types.StringValue("nightly"), value, value); replace != want {
			t.Errorf("name %q: replace = %v, want %v", planned, replace, want)
		}
	}
}

func TestBuildBackupUpdateBody(t *testing.T) {
	t.Parallel()
	state := testBackupState()
	plan := state
	plan.Name = types.StringValue("renamed")
	if _, changed := buildBackupUpdateBody(plan, state); changed {
		t.Fatal("name must never be sent")
	}
	plan.Description = types.StringValue("")
	body, changed := buildBackupUpdateBody(plan, state)
	if !changed || body.Name != nil || body.Description == nil || *body.Description != "" {
		t.Fatalf("an explicit empty description must be sent alone: %#v", body)
	}
	plan.Description = types.StringUnknown()
	if _, changed := buildBackupUpdateBody(plan, state); changed {
		t.Fatal("an unknown description must not be sent")
	}
	plan.Description = types.StringValue(" " + state.Description.ValueString() + " ")
	if _, changed := buildBackupUpdateBody(plan, state); changed {
		t.Fatal("a change of outer whitespace must not be sent")
	}
	plan.Description = types.StringValue(" after upgrade ")
	if body, changed := buildBackupUpdateBody(plan, state); !changed || *body.Description != "after upgrade" {
		t.Fatalf("a changed description must be sent trimmed: %#v", body.Description)
	}
}

func TestBackupLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		polls := 0
		api, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
			backup := sampleBackup()
			item := "/v2/dbaas/db-instance-backups/" + testBackupID.String() + "/"
			switch {
			case req.Method == http.MethodPost && req.Path == "/v2/dbaas/db-instance-backups/":
				backup.Status, backup.BackupTimestamp, backup.Description = dbaassdk.BackupStatusCREATING, nil, ""
				return http.StatusCreated, backup
			case req.Method == http.MethodPatch && req.Path == item:
				backup.Description = ""
				return http.StatusOK, backup
			case req.Method == http.MethodDelete && req.Path == item:
				return http.StatusAccepted, nil
			case req.Method == http.MethodGet && req.Path == item:
				polls++
				if polls == 1 {
					backup.Status, backup.BackupTimestamp = dbaassdk.BackupStatusCREATING, nil
				}
				if polls == 4 {
					return http.StatusNotFound, apiError("not_found", "Not found")
				}
				return http.StatusOK, backup
			}
			t.Errorf("unexpected request %s", req)
			return http.StatusInternalServerError, nil
		})
		r := &BackupResource{client: client, projectID: testProjectID}
		plan := testBackupConfig()
		plan.Description = types.StringUnknown()
		created := resource.CreateResponse{State: tfsdk.State{Schema: backupSchema(t)}}
		start := time.Now()
		r.Create(ctx, resource.CreateRequest{Plan: backupPlan(t, plan)}, &created)
		if created.Diagnostics.HasError() || time.Since(start) != backupCreatePollInterval {
			t.Fatalf("create: %v after %s", created.Diagnostics, time.Since(start))
		}
		posts := api.requestsTo(http.MethodPost, "/")
		if len(posts) != 1 || posts[0].RawBody != `{"db_instance_id":"`+testInstanceID.String()+`","name":"nightly"}` || posts[0].ProjectID != testProjectID.String() {
			t.Fatalf("create body must hold only db_instance_id and name: %v", posts)
		}
		var state BackupResourceModel
		created.Diagnostics.Append(created.State.Get(ctx, &state)...)
		if state.Status.ValueString() != "COMPLETED" || state.BackupType.ValueString() != "manual" || state.BackupTimestamp.IsNull() || state.Description.ValueString() != "before upgrade" {
			t.Fatalf("create state: %#v", state)
		}

		read := resource.ReadResponse{State: created.State}
		r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
		if read.Diagnostics.HasError() {
			t.Fatal(read.Diagnostics)
		}
		plan = state
		plan.Description, plan.UpdatedAt = types.StringValue(""), types.StringUnknown()
		updated := resource.UpdateResponse{State: read.State}
		r.Update(ctx, resource.UpdateRequest{Plan: backupPlan(t, plan), State: read.State}, &updated)
		if updated.Diagnostics.HasError() {
			t.Fatal(updated.Diagnostics)
		}
		if patches := api.requestsTo(http.MethodPatch, "/"); len(patches) != 1 || patches[0].RawBody != `{"description":""}` {
			t.Fatalf("PATCH must carry only description: %v", patches)
		}
		updated.Diagnostics.Append(updated.State.Get(ctx, &state)...)
		if state.ID.ValueString() != testBackupID.String() || state.Description.ValueString() != "" {
			t.Fatalf("update state: %#v", state)
		}
		gets := len(api.requestsTo(http.MethodGet, "/"))

		deleted := resource.DeleteResponse{State: updated.State}
		start = time.Now()
		r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
		if deleted.Diagnostics.HasError() || time.Since(start) != 0 {
			t.Fatalf("delete: %v after %s", deleted.Diagnostics, time.Since(start))
		}
		if len(api.requestsTo(http.MethodGet, "/")) != gets+1 {
			t.Fatal("an update must not read the backup again, and delete must poll until 404")
		}
	})
}

func TestBackupCreateTrimsRequestAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
		if req.Method == http.MethodPost {
			return http.StatusCreated, sampleBackup()
		}
		return http.StatusOK, sampleBackup()
	})
	r := &BackupResource{client: client, projectID: testProjectID}
	plan := testBackupConfig()
	plan.Name, plan.Description = types.StringValue(" nightly "), types.StringValue(" before upgrade ")
	plan.DBInstance = reference.Value(reference.ID, types.StringValue("  "+testInstanceID.String()+" "))
	created := resource.CreateResponse{State: tfsdk.State{Schema: backupSchema(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: backupPlan(t, plan)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	want := `{"db_instance_id":"` + testInstanceID.String() + `","description":"before upgrade","name":"nightly"}`
	if posts := api.requestsTo(http.MethodPost, "/"); len(posts) != 1 || posts[0].RawBody != want {
		t.Fatalf("the request must carry trimmed values: %v", posts)
	}
	check := func(stage string, state tfsdk.State) {
		var got BackupResourceModel
		diags := state.Get(ctx, &got)
		if diags.HasError() || !got.Name.Equal(plan.Name) || !got.Description.Equal(plan.Description) ||
			!reference.Get(got.DBInstance, reference.ID).Equal(reference.Get(plan.DBInstance, reference.ID)) {
			t.Fatalf("state after %s must keep the configured spelling: %v %v %v %v", stage, got.Name, got.Description, got.DBInstance, diags)
		}
	}
	check("create", created.State)
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	check("read", read.State)
}

func TestBackupUpdateWithoutChangeSkipsPatch(t *testing.T) {
	t.Parallel()
	api, client, _ := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusOK, sampleBackup() })
	r := &BackupResource{client: client, projectID: testProjectID}
	state := testBackupState()
	plan := state
	plan.UpdatedAt = types.StringUnknown()
	resp := resource.UpdateResponse{State: backupState(t, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: backupPlan(t, plan), State: backupState(t, state)}, &resp)
	if resp.Diagnostics.HasError() || len(api.requestsTo(http.MethodPatch, "/")) != 0 {
		t.Fatalf("an unchanged description must not send a PATCH: %v %v", resp.Diagnostics, api.requests)
	}
}

func TestPopulateBackupResourceState(t *testing.T) {
	t.Parallel()
	state := testBackupState()
	if reference.Get(state.DBInstance, reference.ID).ValueString() != testInstanceID.String() || state.DBInstanceName.ValueString() != "orders" ||
		state.DBVersion.ValueString() != "16" || state.Region.ValueString() != "hn-1" || state.Size.ValueInt64() != 128 || state.RequiredVolumeSize.ValueInt64() != 20 ||
		!state.ZoneID.IsNull() || !state.BackupPlanID.IsNull() || !state.StartRestoreTimestamp.IsNull() || state.BackupTimestamp.ValueString() != "2026-10-02T08:00:00Z" {
		t.Fatalf("incomplete mapping: %#v", state)
	}
	upper := strings.ToUpper(testInstanceID.String())
	state.DBInstance = reference.Value(reference.ID, types.StringValue(upper))
	orphaned := sampleBackup()
	orphaned.DbInstance.SetNull()
	orphaned.BackupTimestamp.SetNull()
	populateBackupResourceState(orphaned, &state)
	if reference.Get(state.DBInstance, reference.ID).ValueString() != upper || !state.DBInstanceName.IsNull() || !state.BackupTimestamp.IsNull() {
		t.Fatalf("a deleted instance must keep db_instance.id and clear its name: %#v", state)
	}
}

func TestBackupCreateRejected(t *testing.T) {
	t.Parallel()
	for _, message := range []string{"DB instance is not active.", "Backup name already exists."} {
		api, client, _ := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusConflict, apiError("instance_not_active", message) })
		r := &BackupResource{client: client, projectID: testProjectID}
		resp := resource.CreateResponse{State: tfsdk.State{Schema: backupSchema(t), Raw: nullBackupRaw(t)}}
		r.Create(context.Background(), resource.CreateRequest{Plan: backupPlan(t, testBackupConfig())}, &resp)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), message) || !resp.State.Raw.IsNull() || len(api.requests) != 1 {
			t.Fatalf("a rejected create must report %q and save no state: %v", message, resp.Diagnostics)
		}
	}
}

func TestBackupCreateErrorPreservesID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
			backup := sampleBackup()
			backup.Status, backup.ErrorMessage = dbaassdk.BackupStatusERROR, "core backup failed"
			if req.Method == http.MethodPost {
				backup.Status = dbaassdk.BackupStatusCREATING
				return http.StatusCreated, backup
			}
			return http.StatusOK, backup
		})
		r := &BackupResource{client: client, projectID: testProjectID}
		resp := resource.CreateResponse{State: tfsdk.State{Schema: backupSchema(t)}}
		start := time.Now()
		r.Create(context.Background(), resource.CreateRequest{Plan: backupPlan(t, testBackupConfig())}, &resp)
		if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "core backup failed") || time.Since(start) != 0 {
			t.Fatalf("ERROR must stop the wait at once with error_message: %v after %s", resp.Diagnostics, time.Since(start))
		}
		var state BackupResourceModel
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
		if state.ID.ValueString() != testBackupID.String() {
			t.Fatalf("the backup ID must stay in state: %v", state.ID)
		}
	})
}

func TestBackupReadRefusesAutomaticBackup(t *testing.T) {
	t.Parallel()
	_, client, _ := newFakeAPI(t, func(apiRequest) (int, any) {
		backup := sampleBackup()
		backup.BackupType = dbaassdk.BackupTypeAutomatic
		return http.StatusOK, backup
	})
	r := &BackupResource{client: client, projectID: testProjectID}
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: backupSchema(t), Raw: nullBackupRaw(t)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testBackupID.String()}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	read := resource.ReadResponse{State: imported.State}
	r.Read(context.Background(), resource.ReadRequest{State: imported.State}, &read)
	if !read.Diagnostics.HasError() || !strings.Contains(read.Diagnostics[0].Detail(), "only manual backups") {
		t.Fatalf("an automatic backup must not enter state: %v", read.Diagnostics)
	}
}

// A replacement would delete the only copy of the deleted instance's data and
// then fail, because no instance remains to back up.
func TestBackupImportOfOrphanedBackupAdoptsConfiguredInstance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api, client, _ := newFakeAPI(t, func(apiRequest) (int, any) {
		backup := sampleBackup()
		backup.DbInstance.SetNull()
		return http.StatusOK, backup
	})
	r := &BackupResource{client: client, projectID: testProjectID}
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: backupSchema(t), Raw: nullBackupRaw(t)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: testBackupID.String()}, &imported)
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	var state BackupResourceModel
	read.Diagnostics.Append(read.State.Get(ctx, &state)...)
	if read.Diagnostics.HasError() || !state.DBInstance.IsNull() || !state.DBInstanceName.IsNull() {
		t.Fatalf("an orphaned backup has no instance to record: %v %#v", read.Diagnostics, state.DBInstance)
	}

	configured := reference.Value(reference.ID, types.StringValue(testInstanceID.String()))
	if _, replace := planChange(t, backupSchema(t).Attributes["db_instance"], path.Root("db_instance"), read.State, state.DBInstance, configured, configured); replace {
		t.Fatal("configuring db_instance after an orphaned import must not replace the backup")
	}

	plan := state
	plan.DBInstance, plan.UpdatedAt = configured, types.StringUnknown()
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: backupPlan(t, plan), State: read.State}, &updated)
	updated.Diagnostics.Append(updated.State.Get(ctx, &state)...)
	if updated.Diagnostics.HasError() || !state.DBInstance.Equal(configured) || len(api.requestsTo(http.MethodPatch, "/")) != 0 {
		t.Fatalf("the update must adopt the configured instance without a PATCH: %v %#v", updated.Diagnostics, state.DBInstance)
	}
}

func TestBackupNotFoundAndDeleteRejected(t *testing.T) {
	t.Parallel()
	_, client, _ := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusNotFound, apiError("not_found", "Not found") })
	r := &BackupResource{client: client, projectID: testProjectID}
	state := backupState(t, testBackupState())
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("a missing backup must leave state: %v", read.Diagnostics)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("deleting a missing backup must succeed: %v", deleted.Diagnostics)
	}

	api, client, _ := newFakeAPI(t, func(apiRequest) (int, any) {
		return http.StatusConflict, apiError("backup_not_deletable", "Backup in CREATING status cannot be deleted.")
	})
	r.client = client
	deleted = resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() || !strings.Contains(deleted.Diagnostics[0].Detail(), "CREATING status") || len(api.requests) != 1 {
		t.Fatalf("a rejected delete must report the backend message once: %v", deleted.Diagnostics)
	}
}

func TestBackupDeleteWaiter(t *testing.T) {
	for _, tt := range []struct {
		name      string
		prior     dbaassdk.BackupStatus
		wantError string
		elapsed   time.Duration
	}{
		{"completed backup fails to delete", dbaassdk.BackupStatusCOMPLETED, "core delete failed", 0},
		{"errored backup waits for 404", dbaassdk.BackupStatusERROR, "timeout", backupDeleteTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				_, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
					if req.Method == http.MethodDelete {
						return http.StatusAccepted, nil
					}
					backup := sampleBackup()
					backup.Status, backup.ErrorMessage = dbaassdk.BackupStatusERROR, "core delete failed"
					return http.StatusOK, backup
				})
				r := &BackupResource{client: client, projectID: testProjectID}
				model := testBackupState()
				model.Status = types.StringValue(string(tt.prior))
				state := backupState(t, model)
				deleted := resource.DeleteResponse{State: state}
				start := time.Now()
				r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
				if !deleted.Diagnostics.HasError() || !strings.Contains(deleted.Diagnostics[0].Detail(), tt.wantError) || time.Since(start) != tt.elapsed {
					t.Fatalf("want %q after %s, got %v after %s", tt.wantError, tt.elapsed, deleted.Diagnostics, time.Since(start))
				}
			})
		})
	}
}

func TestBackupValidateConfig(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		change    func(*BackupResourceModel)
		wantError string
	}{
		{"required only", func(*BackupResourceModel) {}, ""},
		{"empty description", func(m *BackupResourceModel) { m.Description = types.StringValue("") }, ""},
		{"padded name", func(m *BackupResourceModel) { m.Name = types.StringValue(" nightly") }, ""},
		{"blank name", func(m *BackupResourceModel) { m.Name = types.StringValue(" ") }, "name"},
		{"padded description", func(m *BackupResourceModel) { m.Description = types.StringValue("note ") }, ""},
		{"padded instance id", func(m *BackupResourceModel) {
			m.DBInstance = reference.Value(reference.ID, types.StringValue("  "+testInstanceID.String()+" "))
		}, ""},
		{"invalid instance id", func(m *BackupResourceModel) {
			m.DBInstance = reference.Value(reference.ID, types.StringValue("orders"))
		}, "db_instance.id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := testBackupConfig()
			tt.change(&config)
			state := backupState(t, config)
			var resp resource.ValidateConfigResponse
			(&BackupResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config(state)}, &resp)
			if (tt.wantError == "") == resp.Diagnostics.HasError() || tt.wantError != "" && !strings.Contains(resp.Diagnostics[0].Detail(), tt.wantError) {
				t.Fatalf("want error %q, got %v", tt.wantError, resp.Diagnostics)
			}
		})
	}
}

func TestBackupImportState(t *testing.T) {
	t.Parallel()
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: backupSchema(t), Raw: nullBackupRaw(t)}}
	(&BackupResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: testBackupID.String()}, &resp)
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() || id.ValueString() != testBackupID.String() {
		t.Fatalf("import id = %v: %v", id, resp.Diagnostics)
	}
}
