package blockstorage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// volume_backup_policy_attachment_test.go covers the backup_policy attribute of
// viettelcloud_volume: validation, the create-time ban, the policy request of
// Update, and the refresh from the volume response.

var (
	volumeBackupPolicyA = core.UUID{0x0a}
	volumeBackupPolicyB = core.UUID{0x0b}
)

func volumeBackupPolicyRef(id string) types.Object {
	return reference.Value(reference.ID, types.StringValue(id))
}

// backupPolicyVolumeModel is an existing available volume that has no source
// other than an empty one, so an update touches only what a test changes.
func backupPolicyVolumeModel(t *testing.T, policy types.Object) VolumeResourceModel {
	t.Helper()
	selected := selector.WithID(selector.WithID(testVolumeTypeSelector(types.StringValue("Premium")), types.StringValue((core.UUID{2}).String())), types.StringValue((core.UUID{2}).String()))
	return VolumeResourceModel{
		SnapshotPolicy: nullVolumeReference(),
		BackupPolicy:   policy,
		VolumeModel:    VolumeModel{ID: types.StringValue(volumeTestID.String()), Name: types.StringValue("data"), Size: types.Int64Value(100)},
		CreateFrom:     volumeCreateFromValue(t, volumeSourceTypeEmpty, map[string]attr.Value{"volume_type": selected}),
		Zone:           nullVolumeZone(),
	}
}

func volumeModelState(ctx context.Context, t *testing.T, s schema.Schema, model VolumeResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func volumeModelPlan(ctx context.Context, t *testing.T, s schema.Schema, model VolumeResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func TestVolumeBackupPolicyAttributeSchema(t *testing.T) {
	t.Parallel()
	attribute, ok := volumeResourceSchema(t).Attributes["backup_policy"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("backup_policy must be a nested attribute, got %T", volumeResourceSchema(t).Attributes["backup_policy"])
	}
	if !attribute.Optional || attribute.Computed || attribute.Required {
		t.Fatalf("backup_policy must be Optional only: %+v", attribute)
	}
	if len(attribute.PlanModifiers) != 0 {
		t.Fatalf("backup_policy takes no plan modifier, got %d", len(attribute.PlanModifiers))
	}
	id, ok := attribute.Attributes[reference.ID].(schema.StringAttribute)
	if !ok || !id.Required || id.Optional || id.Computed {
		t.Fatalf("backup_policy.id must be a Required string: %+v", attribute.Attributes[reference.ID])
	}
	if !strings.Contains(attribute.Description, "detaches") || strings.Count(attribute.Description, ". ") != 1 {
		t.Errorf("the description must be two short sentences that state the detach rule: %s", attribute.Description)
	}
}

// The attribute Description stays one short clause, so the upgrade and
// ownership warnings of design/blockstorage/volume.md, section 4.3, live in the
// docs template.
func TestVolumeDocsTemplateWarnsAboutBackupPolicy(t *testing.T) {
	t.Parallel()
	template, err := os.ReadFile("../../../templates/resources/volume.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range []string{"later apply", "overwrite each other", "before the first apply after you upgrade"} {
		if !strings.Contains(string(template), warning) {
			t.Errorf("the volume docs template must warn about %q", warning)
		}
	}
}

func TestVolumeDataSourceHasNoBackupPolicy(t *testing.T) {
	t.Parallel()
	var response datasource.SchemaResponse
	(&VolumeDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if _, found := response.Schema.Attributes["backup_policy"]; found {
		t.Fatal("the volume data source must not expose backup_policy")
	}
}

func TestValidateVolumeBackupPolicyConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		policy    types.Object
		wantError string
	}{
		{name: "omitted", policy: nullVolumeReference()},
		{name: "unknown object", policy: types.ObjectUnknown(reference.AttributeTypes(reference.ID))},
		{name: "unknown id", policy: reference.Value(reference.ID, types.StringUnknown())},
		{name: "valid", policy: volumeBackupPolicyRef(volumeBackupPolicyA.String())},
		{name: "padded id", policy: volumeBackupPolicyRef(" " + volumeBackupPolicyA.String() + "\n")},
		{name: "empty id", policy: volumeBackupPolicyRef(""), wantError: "Missing UUID value"},
		{name: "blank id", policy: volumeBackupPolicyRef("  "), wantError: "Missing UUID value"},
		{name: "malformed id", policy: volumeBackupPolicyRef("not-a-uuid"), wantError: "Invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags := validateVolumeBackupPolicyConfig(tt.policy)
			switch {
			case tt.wantError == "" && diags.HasError():
				t.Fatalf("unexpected diagnostics: %v", diags)
			case tt.wantError != "" && (!diags.HasError() || diags[0].Summary() != tt.wantError):
				t.Fatalf("want %q, got %v", tt.wantError, diags)
			}
		})
	}
}

func TestVolumeResourceValidateConfigReportsInvalidBackupPolicyID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	model := backupPolicyVolumeModel(t, volumeBackupPolicyRef("not-a-uuid"))
	model.CreateFrom = volumeCreateFromValue(t, volumeSourceTypeEmpty, map[string]attr.Value{"volume_type": types.StringValue("Premium")})
	config := tfsdk.Config{Raw: volumeModelPlan(ctx, t, volumeSchema, model).Raw, Schema: volumeSchema}
	var response resource.ValidateConfigResponse
	(&VolumeResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Invalid UUID" {
		t.Fatalf("want an Invalid UUID diagnostic, got %v", response.Diagnostics)
	}
}

func TestVolumeResourceModifyPlanRejectsBackupPolicyOnCreateAndReplace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	policy := volumeBackupPolicyRef(volumeBackupPolicyA.String())
	tests := []struct {
		name      string
		initial   bool
		replace   bool
		policy    types.Object
		wantError bool
	}{
		{name: "create with a policy", initial: true, policy: policy, wantError: true},
		{name: "replace with a policy", replace: true, policy: policy, wantError: true},
		{name: "create with a policy that is still unknown", initial: true, policy: types.ObjectUnknown(reference.AttributeTypes(reference.ID))},
		{name: "update in place with a policy", policy: policy},
		{name: "replace without a policy", replace: true, policy: nullVolumeReference()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			current := backupPolicyVolumeModel(t, policy)
			planned := current
			planned.BackupPolicy = tt.policy
			if tt.replace {
				planned.CreateFrom = volumeCreateFromValue(t, volumeSourceTypeSnapshot, map[string]attr.Value{"snapshot": types.StringValue((core.UUID{5}).String())})
				planned.Size = types.Int64Null()
			}
			state := tfsdk.State{Schema: volumeSchema}
			if !tt.initial {
				state = volumeModelState(ctx, t, volumeSchema, current)
			}
			plan := volumeModelPlan(ctx, t, volumeSchema, planned)
			config := tfsdk.Config{Raw: plan.Raw, Schema: volumeSchema}

			response := resource.ModifyPlanResponse{Plan: plan}
			(&VolumeResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Config: config, Plan: plan, State: state}, &response)
			if tt.wantError {
				if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Invalid volume policy" {
					t.Fatalf("want Invalid volume policy, got %v", response.Diagnostics)
				}
				return
			}
			for _, d := range response.Diagnostics {
				if d.Summary() == "Invalid volume policy" {
					t.Fatalf("unexpected policy diagnostic: %v", response.Diagnostics)
				}
			}
		})
	}
}

func TestVolumeResourceCreateRejectsBackupPolicyWithoutCallingTheAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("Create must send no request when backup_policy is set, got %s %s", req.Method, req.URL.Path)
		return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
	})
	model := backupPolicyVolumeModel(t, volumeBackupPolicyRef(volumeBackupPolicyA.String()))
	plan := volumeModelPlan(ctx, t, volumeSchema, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: volumeSchema}}
	(&VolumeResource{client: client, projectID: volumeTestProjectID}).Create(
		ctx, resource.CreateRequest{Config: tfsdk.Config{Raw: plan.Raw, Schema: volumeSchema}, Plan: plan}, &response,
	)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Invalid volume policy" {
		t.Fatalf("want Invalid volume policy, got %v", response.Diagnostics)
	}
}

func TestBuildVolumeBackupPolicyBody(t *testing.T) {
	t.Parallel()
	a, b := volumeBackupPolicyA.String(), volumeBackupPolicyB.String()
	tests := []struct {
		name     string
		plan     types.Object
		state    types.Object
		wantCall bool
		wantJSON string
		wantErr  bool
	}{
		{name: "attach", plan: volumeBackupPolicyRef(a), state: nullVolumeReference(), wantCall: true, wantJSON: `{"backup_policy_id":"` + a + `"}`},
		{name: "replace", plan: volumeBackupPolicyRef(b), state: volumeBackupPolicyRef(a), wantCall: true, wantJSON: `{"backup_policy_id":"` + b + `"}`},
		{name: "detach sends JSON null", plan: nullVolumeReference(), state: volumeBackupPolicyRef(a), wantCall: true, wantJSON: `{"backup_policy_id":null}`},
		{name: "nothing attached and nothing planned", plan: nullVolumeReference(), state: nullVolumeReference()},
		{name: "same policy", plan: volumeBackupPolicyRef(a), state: volumeBackupPolicyRef(a)},
		{name: "same policy spelled differently", plan: volumeBackupPolicyRef(strings.ToUpper(a)), state: volumeBackupPolicyRef(a)},
		{name: "same policy with outer whitespace", plan: volumeBackupPolicyRef(a), state: volumeBackupPolicyRef(" " + a + " ")},
		{name: "padded id is sent trimmed", plan: volumeBackupPolicyRef(" " + b + "\t"), state: volumeBackupPolicyRef(a), wantCall: true, wantJSON: `{"backup_policy_id":"` + b + `"}`},
		{name: "unknown planned id", plan: reference.Value(reference.ID, types.StringUnknown()), state: nullVolumeReference(), wantErr: true},
		{name: "malformed planned id", plan: volumeBackupPolicyRef("nope"), state: nullVolumeReference(), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, call, diags := buildVolumeBackupPolicyBody(tt.plan, tt.state)
			if diags.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", diags, tt.wantErr)
			}
			if call != tt.wantCall {
				t.Fatalf("call = %v, want %v", call, tt.wantCall)
			}
			if !tt.wantCall {
				return
			}
			got, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			if string(got) != tt.wantJSON {
				t.Fatalf("body = %s, want %s", got, tt.wantJSON)
			}
		})
	}
}

// policyRecorder answers the volume API for the Update tests. It serves the
// volume with the policy that attached holds, and records every write.
type policyRecorder struct {
	mu       sync.Mutex
	calls    []string
	putBody  string
	putPath  string
	project  string
	attached *core.UUID
	name     string
	putCode  int
}

func (p *policyRecorder) handle(t *testing.T) volumeRoundTripFunc {
	t.Helper()
	return func(req *http.Request) (*http.Response, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.calls = append(p.calls, req.Method)
		volume := sampleVolumeDetail()
		volume.Name = p.name
		if p.attached != nil {
			volume.BackupPolicy = core.NullableFromPtr(&blockstoragesdk.NestedVolumeBackupPolicySchema{Id: *p.attached, Name: "policy"})
		}
		switch req.Method {
		case http.MethodPut:
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			p.putBody, p.putPath, p.project = string(body), req.URL.Path, req.Header.Get("Project-ID")
			if p.putCode != http.StatusNoContent {
				return testHTTPResponse(req, p.putCode, `{"errors":[{"code":"invalid"}]}`), nil
			}
			return testHTTPResponse(req, http.StatusNoContent, ``), nil
		default:
			return testHTTPResponse(req, http.StatusOK, volumeDetailJSON(volume)), nil
		}
	}
}

func TestVolumeResourceUpdateConfiguresBackupPolicy(t *testing.T) {
	t.Parallel()
	a, b := volumeBackupPolicyA, volumeBackupPolicyB
	tests := []struct {
		name         string
		state        types.Object
		plan         types.Object
		renameTo     string
		servedAfter  *core.UUID
		putCode      int
		wantPut      string
		wantCalls    []string
		wantPolicy   types.Object
		wantErrorFor string
	}{
		{name: "attach", state: nullVolumeReference(), plan: volumeBackupPolicyRef(a.String()), servedAfter: &a, putCode: http.StatusNoContent, wantPut: `{"backup_policy_id":"` + a.String() + `"}`, wantCalls: []string{"PUT", "GET"}, wantPolicy: volumeBackupPolicyRef(a.String())},
		{name: "replace", state: volumeBackupPolicyRef(a.String()), plan: volumeBackupPolicyRef(b.String()), servedAfter: &b, putCode: http.StatusNoContent, wantPut: `{"backup_policy_id":"` + b.String() + `"}`, wantCalls: []string{"PUT", "GET"}, wantPolicy: volumeBackupPolicyRef(b.String())},
		{name: "detach", state: volumeBackupPolicyRef(a.String()), plan: nullVolumeReference(), putCode: http.StatusNoContent, wantPut: `{"backup_policy_id":null}`, wantCalls: []string{"PUT", "GET"}, wantPolicy: nullVolumeReference()},
		{name: "unchanged policy sends no request", state: volumeBackupPolicyRef(a.String()), plan: volumeBackupPolicyRef(a.String()), servedAfter: &a, wantCalls: []string{"GET"}, wantPolicy: volumeBackupPolicyRef(a.String())},
		{name: "padded attach keeps the configured spelling", state: nullVolumeReference(), plan: volumeBackupPolicyRef(" " + a.String() + " "), servedAfter: &a, putCode: http.StatusNoContent, wantPut: `{"backup_policy_id":"` + a.String() + `"}`, wantCalls: []string{"PUT", "GET"}, wantPolicy: volumeBackupPolicyRef(" " + a.String() + " ")},
		{name: "a whitespace-only edit sends no request", state: volumeBackupPolicyRef(" " + a.String() + " "), plan: volumeBackupPolicyRef(a.String()), servedAfter: &a, wantCalls: []string{"GET"}, wantPolicy: volumeBackupPolicyRef(a.String())},
		{name: "no policy before or after sends no request", state: nullVolumeReference(), plan: nullVolumeReference(), wantCalls: []string{"GET"}, wantPolicy: nullVolumeReference()},
		{name: "the policy request runs after the volume patch", state: nullVolumeReference(), plan: volumeBackupPolicyRef(a.String()), renameTo: "renamed", servedAfter: &a, putCode: http.StatusNoContent, wantPut: `{"backup_policy_id":"` + a.String() + `"}`, wantCalls: []string{"PATCH", "GET", "PUT", "GET"}, wantPolicy: volumeBackupPolicyRef(a.String())},
		{name: "a rejected request reports the policy error", state: nullVolumeReference(), plan: volumeBackupPolicyRef(a.String()), putCode: http.StatusBadRequest, wantPut: `{"backup_policy_id":"` + a.String() + `"}`, wantCalls: []string{"PUT"}, wantErrorFor: "Error updating backup policy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			volumeSchema := volumeResourceSchema(t)
			recorder := &policyRecorder{name: "data", attached: tt.servedAfter, putCode: tt.putCode}
			if tt.renameTo != "" {
				recorder.name = tt.renameTo
			}
			r := &VolumeResource{client: volumeTestClient(t, recorder.handle(t)), projectID: volumeTestProjectID, pollInterval: time.Millisecond, timeout: time.Second}

			current := backupPolicyVolumeModel(t, tt.state)
			planned := backupPolicyVolumeModel(t, tt.plan)
			if tt.renameTo != "" {
				planned.Name = types.StringValue(tt.renameTo)
			}
			plan := volumeModelPlan(ctx, t, volumeSchema, planned)
			state := volumeModelState(ctx, t, volumeSchema, current)
			response := resource.UpdateResponse{State: tfsdk.State{Schema: volumeSchema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state, Config: tfsdk.Config{Raw: plan.Raw, Schema: volumeSchema}}, &response)

			if tt.wantErrorFor != "" {
				if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != tt.wantErrorFor {
					t.Fatalf("want %q, got %v", tt.wantErrorFor, response.Diagnostics)
				}
			} else {
				if response.Diagnostics.HasError() {
					t.Fatalf("update diagnostics: %v", response.Diagnostics)
				}
				var result VolumeResourceModel
				if diags := response.State.Get(ctx, &result); diags.HasError() {
					t.Fatalf("read state: %v", diags)
				}
				if !result.BackupPolicy.Equal(tt.wantPolicy) {
					t.Fatalf("state backup_policy = %s, want %s", result.BackupPolicy, tt.wantPolicy)
				}
			}
			if strings.Join(recorder.calls, ",") != strings.Join(tt.wantCalls, ",") {
				t.Fatalf("requests = %v, want %v", recorder.calls, tt.wantCalls)
			}
			if recorder.putBody != tt.wantPut {
				t.Fatalf("PUT body = %q, want %q", recorder.putBody, tt.wantPut)
			}
			if tt.wantPut != "" {
				if want := "/v2/block-storage/volumes/" + volumeTestID.String() + "/backup-policy/"; recorder.putPath != want {
					t.Fatalf("PUT path = %s, want %s", recorder.putPath, want)
				}
				if recorder.project != volumeTestProjectID.String() {
					t.Fatalf("Project-ID header = %q, want %s", recorder.project, volumeTestProjectID)
				}
			}
		})
	}
}

func TestPopulateVolumeBackupPolicy(t *testing.T) {
	t.Parallel()
	a, b := volumeBackupPolicyA, volumeBackupPolicyB
	withPolicy := func(id *core.UUID) *blockstoragesdk.VolumeSchema {
		volume := sampleVolumeDetail()
		if id != nil {
			volume.BackupPolicy = core.NullableFromPtr(&blockstoragesdk.NestedVolumeBackupPolicySchema{Id: *id, Name: "policy"})
		}
		return volume
	}
	tests := []struct {
		name     string
		response *blockstoragesdk.VolumeSchema
		prior    types.Object
		want     types.Object
	}{
		{name: "response without a policy maps to null", response: withPolicy(nil), prior: nullVolumeReference(), want: nullVolumeReference()},
		{name: "import fills the policy", response: withPolicy(&a), prior: nullVolumeReference(), want: volumeBackupPolicyRef(a.String())},
		{name: "a policy removed outside Terraform shows as null", response: withPolicy(nil), prior: volumeBackupPolicyRef(a.String()), want: nullVolumeReference()},
		{name: "a policy replaced outside Terraform shows the new ID", response: withPolicy(&b), prior: volumeBackupPolicyRef(a.String()), want: volumeBackupPolicyRef(b.String())},
		{name: "the configured spelling is kept for the same UUID", response: withPolicy(&a), prior: volumeBackupPolicyRef(strings.ToUpper(a.String())), want: volumeBackupPolicyRef(strings.ToUpper(a.String()))},
		{name: "outer whitespace is kept for the same UUID", response: withPolicy(&a), prior: volumeBackupPolicyRef(" " + a.String() + "\n"), want: volumeBackupPolicyRef(" " + a.String() + "\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := backupPolicyVolumeModel(t, tt.prior)
			populateVolumeBackupPolicy(tt.response, &model)
			if !model.BackupPolicy.Equal(tt.want) {
				t.Fatalf("backup_policy = %s, want %s", model.BackupPolicy, tt.want)
			}
		})
	}
}

func TestVolumeResourceReadMapsBackupPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	policy := volumeBackupPolicyA
	recorder := &policyRecorder{name: "data", attached: &policy}
	r := &VolumeResource{client: volumeTestClient(t, recorder.handle(t)), projectID: volumeTestProjectID}
	state := volumeModelState(ctx, t, volumeSchema, backupPolicyVolumeModel(t, nullVolumeReference()))
	response := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var result VolumeResourceModel
	if diags := response.State.Get(ctx, &result); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if want := volumeBackupPolicyRef(policy.String()); !result.BackupPolicy.Equal(want) {
		t.Fatalf("backup_policy = %s, want %s", result.BackupPolicy, want)
	}
}
