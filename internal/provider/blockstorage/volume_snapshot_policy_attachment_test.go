package blockstorage

// volume_snapshot_policy_attachment_test.go tests the snapshot_policy attribute
// of viettelcloud_volume: schema, create and replace rejection, request bodies,
// the update step, and refresh mapping.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
)

var (
	volumePolicyA = core.UUID{0x31}
	volumePolicyB = core.UUID{0x32}
)

func volumePolicyReference(id core.UUID) types.Object {
	return reference.Value(reference.ID, types.StringValue(id.String()))
}

// paddedVolumePolicyReference spells the policy ID with outer whitespace. The
// padding is not one character on each side, because the UUID parser drops
// the first and last character of any 38-character value.
func paddedVolumePolicyReference(id core.UUID) types.Object {
	return reference.Value(reference.ID, types.StringValue("  "+id.String()+"\n"))
}

func volumeWithSnapshotPolicy(id core.UUID) *blockstoragesdk.VolumeSchema {
	volume := sampleVolumeDetail()
	volume.SnapshotPolicy = core.NullableFromPtr(&blockstoragesdk.NestedVolumeSnapshotPolicySchema{Id: id, Name: "daily"})
	return volume
}

// volumePolicyModel is an empty-source volume model with the given policy
// reference. The volume type already carries its ID, so no catalog call is due.
func volumePolicyModel(t *testing.T, policy types.Object) VolumeResourceModel {
	t.Helper()
	return VolumeResourceModel{
		VolumeModel: VolumeModel{ID: types.StringValue(volumeTestID.String()), Name: types.StringValue("data"), Size: types.Int64Value(100), ZoneID: types.StringValue((core.UUID{8}).String())},
		CreateFrom: volumeCreateFromValue(t, volumeSourceTypeEmpty, map[string]attr.Value{
			"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("Premium")), types.StringValue((core.UUID{2}).String())),
		}),
		Zone:           volumeZone("zone-a"),
		SnapshotPolicy: policy,
		BackupPolicy:   nullVolumeReference(),
	}
}

func TestVolumeResourceSnapshotPolicySchema(t *testing.T) {
	t.Parallel()
	attribute, ok := volumeResourceSchema(t).Attributes["snapshot_policy"].(resourceschema.SingleNestedAttribute)
	if !ok {
		t.Fatal("snapshot_policy must be a single nested attribute")
	}
	if !attribute.Optional || attribute.Computed || attribute.Required || attribute.Sensitive || len(attribute.PlanModifiers) != 0 {
		t.Fatalf("snapshot_policy must be a plain optional reference without plan modifiers: %#v", attribute)
	}
	id, ok := attribute.Attributes[reference.ID].(resourceschema.StringAttribute)
	if len(attribute.Attributes) != 1 || !ok || !id.Required || id.Computed {
		t.Fatalf("snapshot_policy must have one required id criterion: %#v", attribute.Attributes)
	}
	if !strings.Contains(attribute.Description, "detaches") || strings.Count(attribute.Description, ". ") != 1 {
		t.Errorf("the description must be two short sentences that state the detach rule: %s", attribute.Description)
	}
	if strings.Contains(strings.ToLower(attribute.Description), "backend") {
		t.Errorf("the description must not name the backend: %s", attribute.Description)
	}
}

// The attribute Description stays one short clause, so the upgrade and
// ownership warnings of design/blockstorage/volume.md, section 4.3, live in the
// docs template.
func TestVolumeDocsTemplateWarnsAboutSnapshotPolicy(t *testing.T) {
	t.Parallel()
	template, err := os.ReadFile("../../../templates/resources/volume.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range []string{"later apply", "overwrite each other", "before the first apply after you upgrade", "Delete those snapshots first"} {
		if !strings.Contains(string(template), warning) {
			t.Errorf("the volume docs template must warn about %q", warning)
		}
	}
}

func TestVolumeDataSourceHasNoSnapshotPolicy(t *testing.T) {
	t.Parallel()
	var response datasource.SchemaResponse
	(&VolumeDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if _, ok := response.Schema.Attributes["snapshot_policy"]; ok {
		t.Fatal("the volume data source must not expose snapshot_policy")
	}
}

func TestValidateVolumeSnapshotPolicyAbsent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		policy  types.Object
		wantErr bool
	}{
		{name: "null", policy: nullVolumeReference()},
		{name: "known id", policy: volumePolicyReference(volumePolicyA), wantErr: true},
		{name: "unknown id", policy: reference.Value(reference.ID, types.StringUnknown()), wantErr: true},
		{name: "unknown reference", policy: types.ObjectUnknown(reference.AttributeTypes(reference.ID)), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diags := validateVolumeSnapshotPolicyAbsent(tt.policy)
			if diags.HasError() != tt.wantErr {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if tt.wantErr && diags[0].Summary() != "Invalid volume policy" {
				t.Fatalf("expected Invalid volume policy, got %q", diags[0].Summary())
			}
		})
	}
}

// AC30: a plan that creates or replaces the volume rejects the policy before
// any API call, so Create never has a policy step.
func TestVolumeResourceModifyPlanRejectsSnapshotPolicyOnCreateAndReplace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	tests := []struct {
		name    string
		initial bool
		replace bool
		policy  types.Object
		wantErr bool
	}{
		{name: "initial create", initial: true, policy: volumePolicyReference(volumePolicyA), wantErr: true},
		{name: "initial create with unknown id", initial: true, policy: reference.Value(reference.ID, types.StringUnknown()), wantErr: true},
		{name: "source replacement", replace: true, policy: volumePolicyReference(volumePolicyA), wantErr: true},
		{name: "in place attach", policy: volumePolicyReference(volumePolicyA)},
		{name: "in place detach", policy: nullVolumeReference()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
				t.Errorf("the policy check must not call the API: %s %s", req.Method, req.URL.Path)
				return testHTTPResponse(req, http.StatusInternalServerError, `{}`), nil
			})
			r := &VolumeResource{client: client, projectID: volumeTestProjectID}
			current := volumePolicyModel(t, nullVolumeReference())
			planned := volumePolicyModel(t, tt.policy)
			if tt.replace {
				planned.CreateFrom = volumeCreateFromValue(t, volumeSourceTypeImage, map[string]attr.Value{
					"image":       testImageSelector(types.StringValue("Ubuntu")),
					"volume_type": selector.WithID(testVolumeTypeSelector(types.StringValue("Premium")), types.StringValue((core.UUID{2}).String())),
				})
			}
			if tt.initial {
				planned.ID = types.StringUnknown()
				planned.ZoneID = types.StringUnknown()
				planned.Zone = nullVolumeZone()
			}
			plan := tfsdk.Plan{Schema: volumeSchema}
			if diags := plan.Set(ctx, &planned); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}
			state := tfsdk.State{Schema: volumeSchema}
			if !tt.initial {
				if diags := state.Set(ctx, &current); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}
			}
			config := tfsdk.State{Schema: volumeSchema}
			if diags := config.Set(ctx, &planned); diags.HasError() {
				t.Fatalf("set config: %v", diags)
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Config: tfsdk.Config(config), Plan: plan, State: state}, &response)
			if !tt.wantErr {
				if response.Diagnostics.HasError() {
					t.Fatalf("unexpected plan diagnostics: %v", response.Diagnostics)
				}
				return
			}
			found := false
			for _, diagnostic := range response.Diagnostics.Errors() {
				found = found || diagnostic.Summary() == "Invalid volume policy"
			}
			if !found {
				t.Fatalf("expected Invalid volume policy, got %v", response.Diagnostics)
			}
		})
	}
}

func TestVolumeResourceValidateConfigChecksSnapshotPolicyID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	tests := []struct {
		name    string
		policy  types.Object
		summary string
	}{
		{name: "valid", policy: volumePolicyReference(volumePolicyA)},
		{name: "id with spaces around it", policy: paddedVolumePolicyReference(volumePolicyA)},
		{name: "whitespace-only id", policy: reference.Value(reference.ID, types.StringValue("  ")), summary: "Missing UUID value"},
		{name: "null", policy: nullVolumeReference()},
		{name: "unknown id", policy: reference.Value(reference.ID, types.StringUnknown())},
		{name: "empty id", policy: reference.Value(reference.ID, types.StringValue("")), summary: "Missing UUID value"},
		{name: "malformed id", policy: reference.Value(reference.ID, types.StringValue("not-a-uuid")), summary: "Invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := volumePolicyModel(t, tt.policy)
			model.Zone = nullVolumeZone()
			config := tfsdk.State{Schema: volumeSchema}
			if diags := config.Set(ctx, &model); diags.HasError() {
				t.Fatalf("set config: %v", diags)
			}
			var response resource.ValidateConfigResponse
			(&VolumeResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config(config)}, &response)
			if tt.summary == "" {
				if response.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
				}
				return
			}
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != tt.summary {
				t.Fatalf("expected %q, got %v", tt.summary, response.Diagnostics)
			}
		})
	}
}

// The update table of volume.md section 4.3, row by row.
func TestBuildVolumeSnapshotPolicyUpdate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		current  types.Object
		planned  types.Object
		wantCall bool
		wantBody string
		wantErr  string
	}{
		{name: "null to id", current: nullVolumeReference(), planned: volumePolicyReference(volumePolicyA), wantCall: true, wantBody: `{"snapshot_policy_id":"` + volumePolicyA.String() + `"}`},
		{name: "id to another id", current: volumePolicyReference(volumePolicyA), planned: volumePolicyReference(volumePolicyB), wantCall: true, wantBody: `{"snapshot_policy_id":"` + volumePolicyB.String() + `"}`},
		{name: "id to null detaches with JSON null", current: volumePolicyReference(volumePolicyA), planned: nullVolumeReference(), wantCall: true, wantBody: `{"snapshot_policy_id":null}`},
		{name: "null to null", current: nullVolumeReference(), planned: nullVolumeReference()},
		{name: "same id", current: volumePolicyReference(volumePolicyA), planned: volumePolicyReference(volumePolicyA)},
		{
			name:    "same id in another spelling",
			current: volumePolicyReference(volumePolicyA),
			planned: reference.Value(reference.ID, types.StringValue(strings.ToUpper(volumePolicyA.String()))),
		},
		{name: "same id with spaces around it", current: volumePolicyReference(volumePolicyA), planned: paddedVolumePolicyReference(volumePolicyA)},
		{
			name: "id with spaces around it", current: nullVolumeReference(), planned: paddedVolumePolicyReference(volumePolicyA),
			wantCall: true, wantBody: `{"snapshot_policy_id":"` + volumePolicyA.String() + `"}`,
		},
		{name: "whitespace-only id", current: nullVolumeReference(), planned: reference.Value(reference.ID, types.StringValue(" ")), wantErr: "Missing UUID value"},
		{name: "unknown id", current: nullVolumeReference(), planned: reference.Value(reference.ID, types.StringUnknown()), wantErr: "Missing UUID value"},
		{name: "empty id", current: nullVolumeReference(), planned: reference.Value(reference.ID, types.StringValue("")), wantErr: "Missing UUID value"},
		{name: "malformed id", current: nullVolumeReference(), planned: reference.Value(reference.ID, types.StringValue("nope")), wantErr: "Invalid UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, call, diags := buildVolumeSnapshotPolicyUpdate(tt.planned, tt.current)
			if tt.wantErr != "" {
				if !diags.HasError() || diags[0].Summary() != tt.wantErr || call {
					t.Fatalf("expected %q without a call, got call=%v diags=%v", tt.wantErr, call, diags)
				}
				return
			}
			if diags.HasError() || call != tt.wantCall {
				t.Fatalf("unexpected result: call=%v diags=%v", call, diags)
			}
			if !call {
				return
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			if string(encoded) != tt.wantBody {
				t.Fatalf("unexpected request body:\n got %s\nwant %s", encoded, tt.wantBody)
			}
		})
	}
}

// AC31 to AC33: Update calls the policy endpoint after the other steps, with
// the Project-ID header, and records what the volume reports.
func TestVolumeResourceUpdateAppliesSnapshotPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	policyPath := "/v2/block-storage/volumes/" + volumeTestID.String() + "/snapshot-policy/"
	tests := []struct {
		name      string
		current   types.Object
		planned   types.Object
		rename    bool
		response  *blockstoragesdk.VolumeSchema
		wantCalls []string
		wantBody  string
		wantID    string
	}{
		{
			name: "attach", current: nullVolumeReference(), planned: volumePolicyReference(volumePolicyA),
			response:  volumeWithSnapshotPolicy(volumePolicyA),
			wantCalls: []string{"PUT " + policyPath, "GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantBody:  `{"snapshot_policy_id":"` + volumePolicyA.String() + `"}`,
			wantID:    volumePolicyA.String(),
		},
		{
			name: "switch", current: volumePolicyReference(volumePolicyA), planned: volumePolicyReference(volumePolicyB),
			response:  volumeWithSnapshotPolicy(volumePolicyB),
			wantCalls: []string{"PUT " + policyPath, "GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantBody:  `{"snapshot_policy_id":"` + volumePolicyB.String() + `"}`,
			wantID:    volumePolicyB.String(),
		},
		{
			name: "detach", current: volumePolicyReference(volumePolicyA), planned: nullVolumeReference(),
			response:  sampleVolumeDetail(),
			wantCalls: []string{"PUT " + policyPath, "GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantBody:  `{"snapshot_policy_id":null}`,
		},
		{
			name: "unchanged", current: volumePolicyReference(volumePolicyA), planned: volumePolicyReference(volumePolicyA),
			response:  volumeWithSnapshotPolicy(volumePolicyA),
			wantCalls: []string{"GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantID:    volumePolicyA.String(),
		},
		{
			name: "attach an id with spaces around it", current: nullVolumeReference(), planned: paddedVolumePolicyReference(volumePolicyA),
			response:  volumeWithSnapshotPolicy(volumePolicyA),
			wantCalls: []string{"PUT " + policyPath, "GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantBody:  `{"snapshot_policy_id":"` + volumePolicyA.String() + `"}`,
			wantID:    "  " + volumePolicyA.String() + "\n",
		},
		{
			name: "respell the same id with spaces around it", current: volumePolicyReference(volumePolicyA), planned: paddedVolumePolicyReference(volumePolicyA),
			response:  volumeWithSnapshotPolicy(volumePolicyA),
			wantCalls: []string{"GET /v2/block-storage/volumes/" + volumeTestID.String() + "/"},
			wantID:    "  " + volumePolicyA.String() + "\n",
		},
		{
			name: "attach after a rename", current: nullVolumeReference(), planned: volumePolicyReference(volumePolicyA), rename: true,
			response: func() *blockstoragesdk.VolumeSchema {
				volume := volumeWithSnapshotPolicy(volumePolicyA)
				volume.Name = "renamed"
				return volume
			}(),
			wantCalls: []string{
				"PATCH /v2/block-storage/volumes/" + volumeTestID.String() + "/",
				"GET /v2/block-storage/volumes/" + volumeTestID.String() + "/",
				"PUT " + policyPath,
				"GET /v2/block-storage/volumes/" + volumeTestID.String() + "/",
			},
			wantBody: `{"snapshot_policy_id":"` + volumePolicyA.String() + `"}`,
			wantID:   volumePolicyA.String(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			var putBody string
			client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.Method+" "+req.URL.Path)
				switch req.Method {
				case http.MethodPut:
					if got := req.Header.Get("Project-ID"); got != volumeTestProjectID.String() {
						t.Errorf("the policy call must send the Project-ID header, got %q", got)
					}
					raw, err := io.ReadAll(req.Body)
					if err != nil {
						t.Errorf("read policy body: %v", err)
					}
					putBody = string(raw)
					return testHTTPResponse(req, http.StatusNoContent, ""), nil
				default:
					return testHTTPResponse(req, http.StatusOK, volumeDetailJSON(tt.response)), nil
				}
			})
			r := &VolumeResource{client: client, projectID: volumeTestProjectID, pollInterval: time.Millisecond, timeout: time.Second}

			current := volumePolicyModel(t, tt.current)
			current.Zone = nullVolumeZone()
			planned := volumePolicyModel(t, tt.planned)
			planned.Zone = nullVolumeZone()
			if tt.rename {
				planned.Name = types.StringValue("renamed")
			}
			state := tfsdk.State{Schema: volumeSchema}
			if diags := state.Set(ctx, &current); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			plan := tfsdk.Plan{Schema: volumeSchema}
			if diags := plan.Set(ctx, &planned); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}
			response := resource.UpdateResponse{State: tfsdk.State{Schema: volumeSchema}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("update diagnostics: %v", response.Diagnostics)
			}
			if strings.Join(calls, "\n") != strings.Join(tt.wantCalls, "\n") {
				t.Fatalf("unexpected requests:\n got %v\nwant %v", calls, tt.wantCalls)
			}
			if putBody != tt.wantBody {
				t.Fatalf("unexpected policy body:\n got %q\nwant %q", putBody, tt.wantBody)
			}
			var result VolumeResourceModel
			if diags := response.State.Get(ctx, &result); diags.HasError() {
				t.Fatalf("get state: %v", diags)
			}
			if got := reference.Get(result.SnapshotPolicy, reference.ID); tt.wantID == "" && !got.IsNull() || tt.wantID != "" && got.ValueString() != tt.wantID {
				t.Fatalf("unexpected policy in state: %v", result.SnapshotPolicy)
			}
		})
	}
}

// AC34: the API refuses a policy from another region with this 400 response,
// so the update stops and shows the API message.
func TestVolumeResourceUpdateReportsSnapshotPolicyFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	var calls []string
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		return testHTTPResponse(req, http.StatusBadRequest, `{"errors":[{"location":"","code":"invalid_input","message":"Snapshot policy must be in the same region with volume"}]}`), nil
	})
	r := &VolumeResource{client: client, projectID: volumeTestProjectID, pollInterval: time.Millisecond, timeout: time.Second}
	current := volumePolicyModel(t, nullVolumeReference())
	current.Zone = nullVolumeZone()
	planned := volumePolicyModel(t, volumePolicyReference(volumePolicyA))
	planned.Zone = nullVolumeZone()
	state := tfsdk.State{Schema: volumeSchema}
	if diags := state.Set(ctx, &current); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: volumeSchema}
	if diags := plan.Set(ctx, &planned); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: volumeSchema}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Error updating snapshot policy" {
		t.Fatalf("expected Error updating snapshot policy, got %v", response.Diagnostics)
	}
	if detail := response.Diagnostics[0].Detail(); !strings.Contains(detail, "Snapshot policy must be in the same region with volume") {
		t.Fatalf("the diagnostic must show the API message, got %q", detail)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "PUT ") {
		t.Fatalf("a failed policy call must stop the update, got %v", calls)
	}
}

// AC35 to AC38: refresh reports the policy the volume has now, so drift shows
// in the next plan, and an import fills the reference.
func TestPopulateVolumeResourceStateMapsSnapshotPolicy(t *testing.T) {
	t.Parallel()
	upperA := reference.Value(reference.ID, types.StringValue(strings.ToUpper(volumePolicyA.String())))
	tests := []struct {
		name     string
		prior    types.Object
		response *blockstoragesdk.VolumeSchema
		want     types.Object
	}{
		{name: "no policy reported", prior: nullVolumeReference(), response: sampleVolumeDetail(), want: nullVolumeReference()},
		{name: "policy reported on an import", prior: nullVolumeReference(), response: volumeWithSnapshotPolicy(volumePolicyA), want: volumePolicyReference(volumePolicyA)},
		{name: "policy removed outside Terraform", prior: volumePolicyReference(volumePolicyA), response: sampleVolumeDetail(), want: nullVolumeReference()},
		{name: "policy switched outside Terraform", prior: volumePolicyReference(volumePolicyA), response: volumeWithSnapshotPolicy(volumePolicyB), want: volumePolicyReference(volumePolicyB)},
		{name: "configured spelling is kept", prior: upperA, response: volumeWithSnapshotPolicy(volumePolicyA), want: upperA},
		{
			name: "spaces around the configured id are kept", prior: paddedVolumePolicyReference(volumePolicyA),
			response: volumeWithSnapshotPolicy(volumePolicyA), want: paddedVolumePolicyReference(volumePolicyA),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := volumePolicyModel(t, tt.prior)
			state.Zone = nullVolumeZone()
			if diags := populateVolumeResourceState(context.Background(), tt.response, &state); diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if !state.SnapshotPolicy.Equal(tt.want) {
				t.Fatalf("unexpected snapshot_policy:\n got %v\nwant %v", state.SnapshotPolicy, tt.want)
			}
		})
	}
}

func TestVolumeResourceReadAfterImportRecordsSnapshotPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeResourceSchema(t)
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: volumeSchema}}
	initial := VolumeResourceModel{SnapshotPolicy: nullVolumeReference(), BackupPolicy: nullVolumeReference(), CreateFrom: types.ObjectNull(volumeCreateFromAttributeTypes()), Zone: nullVolumeZone()}
	if diags := imported.State.Set(ctx, &initial); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}
	(&VolumeResource{}).ImportState(ctx, resource.ImportStateRequest{ID: volumeTestID.String()}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatalf("import: %v", imported.Diagnostics)
	}
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, volumeDetailJSON(volumeWithSnapshotPolicy(volumePolicyA))), nil
	})
	read := resource.ReadResponse{State: imported.State}
	(&VolumeResource{client: client, projectID: volumeTestProjectID}).Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read imported volume: %v", read.Diagnostics)
	}
	var state VolumeResourceModel
	if diags := read.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if !state.SnapshotPolicy.Equal(volumePolicyReference(volumePolicyA)) {
		t.Fatalf("import must record the attached policy, got %v", state.SnapshotPolicy)
	}
}
