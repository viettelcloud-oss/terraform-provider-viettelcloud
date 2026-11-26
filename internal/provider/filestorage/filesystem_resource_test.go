package filestorage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	filestorageselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

var (
	fsTestID        = core.UUID{1}
	fsTestProjectID = core.UUID{9}
	fsTestZoneID    = core.UUID{8}
	fsTestSubnetID  = core.UUID{5}
	fsTestTypeID    = core.UUID{2}
)

type fsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func filesystemTestClient(t *testing.T, roundTrip fsRoundTripFunc) *filestoragepkg.Client {
	t.Helper()
	client, err := filestoragepkg.NewClient(
		"https://filestorage.test",
		filestoragepkg.WithHTTPClient(&http.Client{Transport: roundTrip}),
		filestoragepkg.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create file storage client: %v", err)
	}
	return client
}

func sampleFilesystem() *filestoragepkg.FilesystemSchema {
	hasSnapshot := false
	return &filestoragepkg.FilesystemSchema{
		Id:                 fsTestID,
		Name:               "my-fs",
		Size:               100,
		Protocol:           filestoragepkg.FilesystemProtocolNFS,
		Status:             filestoragepkg.FilesystemStatusAvailable,
		MountTargetAddress: "10.0.0.1",
		Zone:               filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
		Region:             filestoragepkg.NestedRegionSchema{Id: core.UUID{7}, Name: "region-a"},
		Vpc:                filestoragepkg.NestedVPCSchema{Id: core.UUID{6}, Name: "vpc-a"},
		Subnet:             filestoragepkg.NestedSubnetSchema{Id: fsTestSubnetID, Cidr: "10.0.0.0/24"},
		ProjectId:          fsTestProjectID,
		HasSnapshot:        &hasSnapshot,
		CreateFrom: filestoragepkg.NestedFilesystemOriginSchema{
			FilesystemType: core.NullableFromPtr(&filestoragepkg.NestedFilesystemTypeSchema{Id: fsTestTypeID, Name: "Standard NFS"}),
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func filesystemJSON(fs *filestoragepkg.FilesystemSchema) string {
	body, err := json.Marshal(fs)
	if err != nil {
		panic(fmt.Sprintf("marshal filesystem: %v", err))
	}
	return string(body)
}

func fsHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func filesystemTypeSelectorCriteria(name string) types.Object {
	attrTypes := filestorageselector.FilesystemTypeAttributeTypes()
	values := make(map[string]attr.Value, len(attrTypes))
	for k := range attrTypes {
		values[k] = types.StringNull()
	}
	values["name"] = types.StringValue(name)
	return types.ObjectValueMust(attrTypes, values)
}

func zeroFilesystemResourceModel() FilesystemResourceModel {
	return FilesystemResourceModel{
		FilesystemModel: FilesystemModel{
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
		},
		FilesystemType: types.ObjectNull(filestorageselector.FilesystemTypeAttributeTypes()),
		Subnet:         types.ObjectNull(reference.AttributeTypes(reference.ID)),
		Snapshot:       types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}
}

func filesystemResourceSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	(&FilesystemResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp
}

func nullFilesystemRaw(t *testing.T) tftypes.Value {
	t.Helper()
	return tftypes.NewValue(filesystemResourceSchema(t).Schema.Type().TerraformType(context.Background()), nil)
}

func TestFilesystemModelsMatchSchemas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	resSchemaResp := filesystemResourceSchema(t)
	plan := tfsdk.Plan{Schema: resSchemaResp.Schema}
	if diags := plan.Set(ctx, zeroFilesystemResourceModel()); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}

	var dsSchemaResp datasource.SchemaResponse
	(&FilesystemDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &dsSchemaResp)
	state := tfsdk.State{Schema: dsSchemaResp.Schema}
	if diags := state.Set(ctx, &FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes)},
	}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

// TestFilesystemResourceSourceAttributeFlags covers Q8: every field of a new
// filesystem is Optional and Computed, because a filesystem created from a
// snapshot takes them from the backend.
func TestFilesystemResourceSourceAttributeFlags(t *testing.T) {
	t.Parallel()
	attributes := filesystemResourceSchema(t).Schema.Attributes
	for _, name := range []string{"size", "protocol", "filesystem_type", "subnet", "snapshot"} {
		a := attributes[name]
		if !a.IsOptional() || !a.IsComputed() || a.IsRequired() {
			t.Errorf("%s: optional=%v computed=%v required=%v, want Optional and Computed", name, a.IsOptional(), a.IsComputed(), a.IsRequired())
		}
	}
	criteria := attributes["filesystem_type"].(schema.SingleNestedAttribute).Attributes
	for _, name := range []string{"name", "zone_id", "zone_name"} {
		if a := criteria[name]; !a.IsOptional() || !a.IsComputed() {
			t.Errorf("filesystem_type.%s: optional=%v computed=%v, want Optional and Computed", name, a.IsOptional(), a.IsComputed())
		}
	}
	if a := criteria["id"]; a.IsOptional() || !a.IsComputed() {
		t.Errorf("filesystem_type.id: optional=%v computed=%v, want Computed only", a.IsOptional(), a.IsComputed())
	}
}

func TestFilesystemResourceConfigure(t *testing.T) {
	t.Parallel()
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	})
	r := &FilesystemResource{}

	var invalid resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: 42}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	var resp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &providerdata.Configured{
		FileStorage: client, ProjectID: fsTestProjectID,
	}}, &resp)
	if resp.Diagnostics.HasError() || r.client != client || r.projectID != fsTestProjectID {
		t.Fatalf("configure failed: %#v diags=%v", r, resp.Diagnostics)
	}
}

func TestFilesystemResourceValidateConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	check := func(t *testing.T, m FilesystemResourceModel, wantErrSubstring string) {
		t.Helper()
		state := tfsdk.State{Schema: schemaResp.Schema}
		if diags := state.Set(ctx, m); diags.HasError() {
			t.Fatalf("set model: %v", diags)
		}
		var resp resource.ValidateConfigResponse
		(&FilesystemResource{}).ValidateConfig(ctx, resource.ValidateConfigRequest{
			Config: tfsdk.Config(state),
		}, &resp)
		if wantErrSubstring == "" {
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected error: %v", resp.Diagnostics)
			}
			return
		}
		if !resp.Diagnostics.HasError() {
			t.Fatalf("expected error containing %q, got none", wantErrSubstring)
		}
		found := false
		for _, d := range resp.Diagnostics {
			if strings.Contains(d.Summary(), wantErrSubstring) || strings.Contains(d.Detail(), wantErrSubstring) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q in diagnostics, got: %v", wantErrSubstring, resp.Diagnostics)
		}
	}

	valid := func() FilesystemResourceModel {
		m := zeroFilesystemResourceModel()
		m.Name = types.StringValue("my-fs")
		m.Size = types.Int64Value(100)
		m.Protocol = types.StringValue("NFS")
		m.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))
		return m
	}

	t.Run("valid model no error", func(t *testing.T) {
		t.Parallel()
		check(t, valid(), "")
	})
	t.Run("empty name", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Name = types.StringValue("   ")
		check(t, m, "non-empty")
	})
	t.Run("name too long", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Name = types.StringValue(strings.Repeat("a", 101))
		check(t, m, "100 characters")
	})
	t.Run("multibyte name within 100 characters", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Name = types.StringValue(strings.Repeat("ệ", 100))
		check(t, m, "")
	})
	t.Run("multibyte name too long", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Name = types.StringValue(strings.Repeat("ệ", 101))
		check(t, m, "100 characters")
	})
	t.Run("zero size", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Size = types.Int64Value(0)
		check(t, m, "greater than zero")
	})
	t.Run("invalid protocol", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Protocol = types.StringValue("FTP")
		check(t, m, "NFS or SMB")
	})
	t.Run("invalid subnet uuid", func(t *testing.T) {
		t.Parallel()
		m := valid()
		m.Subnet = reference.Value(reference.ID, types.StringValue("not-a-uuid"))
		check(t, m, "UUID")
	})
}

// A create plan must accept a criterion that becomes known only at apply,
// because Create resolves the filesystem type then.
func TestFilesystemResourceModifyPlanCreateSelectorCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	criteria := func(name, zoneID types.String) types.Object {
		return types.ObjectValueMust(filestorageselector.FilesystemTypeAttributeTypes(), map[string]attr.Value{
			"id":        types.StringUnknown(),
			"name":      name,
			"zone_id":   zoneID,
			"zone_name": types.StringNull(),
		})
	}
	tests := []struct {
		name        string
		selector    types.Object
		wantSummary string
	}{
		{name: "unknown zone_id", selector: criteria(types.StringValue("Standard NFS"), types.StringUnknown())},
		{name: "known criteria", selector: criteria(types.StringValue("Standard NFS"), types.StringValue(fsTestZoneID.String()))},
		{name: "no criteria", selector: criteria(types.StringNull(), types.StringNull()), wantSummary: "Missing selector criteria"},
		{name: "blank name", selector: criteria(types.StringValue("  "), types.StringNull()), wantSummary: "Empty selector criterion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			planned := zeroFilesystemResourceModel()
			planned.ID = types.StringUnknown()
			planned.Name = types.StringValue("fs")
			planned.Size = types.Int64Value(100)
			planned.Protocol = types.StringValue("NFS")
			planned.FilesystemType = tt.selector
			planned.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))

			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := plan.Set(ctx, planned); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}
			state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
			config := tfsdk.Config{Raw: plan.Raw, Schema: schemaResp.Schema}

			resp := resource.ModifyPlanResponse{Plan: plan}
			(&FilesystemResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state, Config: config}, &resp)
			if tt.wantSummary == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
				}
				return
			}
			errs := resp.Diagnostics.Errors()
			if len(errs) == 0 || errs[0].Summary() != tt.wantSummary {
				t.Fatalf("expected %q diagnostic, got %v", tt.wantSummary, resp.Diagnostics)
			}
		})
	}
}

func TestFilesystemResourceModifyPlanRejectsSizeDecrease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	current := zeroFilesystemResourceModel()
	current.ID = types.StringValue(fsTestID.String())
	current.Size = types.Int64Value(200)
	current.Protocol = types.StringValue("NFS")
	current.FilesystemType = selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
	current.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))

	planned := current
	planned.Size = types.Int64Value(100)
	planned.Protocol = types.StringValue(" NFS ")

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(ctx, planned); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, current); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	config := tfsdk.Config{Raw: plan.Raw, Schema: schemaResp.Schema}

	resp := resource.ModifyPlanResponse{Plan: plan}
	(&FilesystemResource{}).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state, Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected size-decrease error from ModifyPlan")
	}
	found := false
	for _, d := range resp.Diagnostics {
		if strings.Contains(strings.ToLower(d.Summary()), "size") || strings.Contains(strings.ToLower(d.Detail()), "size") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected size error in diagnostics: %v", resp.Diagnostics)
	}
}

// TestFilesystemResourceModifyPlanFilesystemTypeSelector covers AC4 for
// filesystem_type: the plan replaces the filesystem only when changed criteria
// resolve to another catalog ID.
func TestFilesystemResourceModifyPlanFilesystemTypeSelector(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)
	otherTypeID := core.UUID{3}

	typePage := func(id core.UUID, name string) string {
		enabled := filestoragepkg.FilesystemTypeStatusEnabled
		body, err := json.Marshal(filestoragepkg.PagedFilesystemTypeSchema{
			Count: 1,
			Results: []filestoragepkg.FilesystemTypeSchema{{
				Id: id, Name: name,
				Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
				Status: core.NullableFromPtr(&enabled),
			}},
		})
		if err != nil {
			t.Fatalf("marshal filesystem types: %v", err)
		}
		return string(body)
	}

	tests := []struct {
		name        string
		plannedName string
		catalog     string
		wantLists   int
		wantReplace bool
		wantID      types.String
	}{
		{
			name:        "unchanged criteria",
			plannedName: "Standard NFS",
			wantID:      types.StringValue(fsTestTypeID.String()),
		},
		{
			name:        "criteria differ only by outer whitespace",
			plannedName: " Standard NFS ",
			wantID:      types.StringValue(fsTestTypeID.String()),
		},
		{
			name:        "changed criteria resolve to the current ID",
			plannedName: "Standard NFS v2",
			catalog:     typePage(fsTestTypeID, "Standard NFS v2"),
			wantLists:   1,
			wantID:      types.StringValue(fsTestTypeID.String()),
		},
		{
			name:        "changed criteria resolve to another ID",
			plannedName: "Premium NFS",
			catalog:     typePage(otherTypeID, "Premium NFS"),
			wantLists:   1,
			wantReplace: true,
			wantID:      types.StringUnknown(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lists := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/") {
					lists++
					return fsHTTPResponse(req, http.StatusOK, tt.catalog), nil
				}
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
			})

			current := zeroFilesystemResourceModel()
			current.ID = types.StringValue(fsTestID.String())
			current.Size = types.Int64Value(100)
			current.Protocol = types.StringValue("NFS")
			current.FilesystemType = selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
			current.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))

			planned := current
			planned.FilesystemType = selector.WithID(filesystemTypeSelectorCriteria(tt.plannedName), types.StringUnknown())

			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := plan.Set(ctx, planned); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}
			state := tfsdk.State{Schema: schemaResp.Schema}
			if diags := state.Set(ctx, current); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			config := tfsdk.Config{Raw: plan.Raw, Schema: schemaResp.Schema}

			r := &FilesystemResource{client: client, projectID: fsTestProjectID}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state, Config: config}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("modify plan: %v", resp.Diagnostics)
			}
			if lists != tt.wantLists {
				t.Fatalf("filesystem type lists = %d, want %d", lists, tt.wantLists)
			}
			replaced := slices.ContainsFunc(resp.RequiresReplace, func(p path.Path) bool {
				return p.Equal(path.Root("filesystem_type"))
			})
			if replaced != tt.wantReplace {
				t.Fatalf("requires replace = %v, want %v (paths %v)", replaced, tt.wantReplace, resp.RequiresReplace)
			}
			var got types.String
			if diags := resp.Plan.GetAttribute(ctx, path.Root("filesystem_type").AtName("id"), &got); diags.HasError() {
				t.Fatalf("read planned filesystem_type.id: %v", diags)
			}
			if !got.Equal(tt.wantID) {
				t.Fatalf("planned filesystem_type.id = %s, want %s", got, tt.wantID)
			}
		})
	}
}

// TestFilesystemResourceReplaceModifiersIgnoreSpelling covers AC15: protocol,
// subnet.id, and snapshot.id replace the filesystem on a real change only.
func TestFilesystemResourceReplaceModifiersIgnoreSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)
	attributes := schemaResp.Schema.Attributes

	// RequiresReplaceIf skips a create or a destroy, so the request needs a
	// known prior state and plan.
	existing := func(t *testing.T) (tfsdk.State, tfsdk.Plan) {
		t.Helper()
		state := tfsdk.State{Schema: schemaResp.Schema}
		if diags := state.Set(ctx, zeroFilesystemResourceModel()); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		return state, tfsdk.Plan{Schema: schemaResp.Schema, Raw: state.Raw}
	}

	planProtocol := func(t *testing.T, config, prior string) bool {
		t.Helper()
		state, plan := existing(t)
		req := planmodifier.StringRequest{
			ConfigValue: types.StringValue(config),
			PlanValue:   types.StringValue(config),
			StateValue:  types.StringValue(prior),
			State:       state,
			Plan:        plan,
		}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		for _, m := range attributes["protocol"].(schema.StringAttribute).PlanModifiers {
			m.PlanModifyString(ctx, req, &resp)
		}
		return resp.RequiresReplace
	}
	if planProtocol(t, " NFS ", "NFS") {
		t.Error("protocol: a change of spelling must not replace the filesystem")
	}
	if !planProtocol(t, "SMB", "NFS") {
		t.Error("protocol: NFS to SMB must replace the filesystem")
	}

	planReference := func(t *testing.T, name string, config, prior types.Object) bool {
		t.Helper()
		planned := config
		if config.IsNull() {
			planned = prior
		}
		state, plan := existing(t)
		req := planmodifier.ObjectRequest{ConfigValue: config, PlanValue: planned, StateValue: prior, State: state, Plan: plan}
		resp := planmodifier.ObjectResponse{PlanValue: planned}
		for _, m := range attributes[name].(schema.SingleNestedAttribute).PlanModifiers {
			m.PlanModifyObject(ctx, req, &resp)
		}
		return resp.RequiresReplace
	}
	id := func(v string) types.Object { return reference.Value(reference.ID, types.StringValue(v)) }
	padded := " " + strings.ToUpper(fsTestSubnetID.String()) + " "
	other := core.UUID{0x42}.String()
	for _, name := range []string{"subnet", "snapshot"} {
		if planReference(t, name, id(padded), id(fsTestSubnetID.String())) {
			t.Errorf("%s: a change of UUID spelling must not replace the filesystem", name)
		}
		if !planReference(t, name, id(other), id(fsTestSubnetID.String())) {
			t.Errorf("%s: a different UUID must replace the filesystem", name)
		}
	}
	for _, name := range []string{"subnet", "snapshot"} {
		if planReference(t, name, types.ObjectNull(reference.AttributeTypes(reference.ID)), id(fsTestSubnetID.String())) {
			t.Errorf("%s: removing the configured value must not replace the filesystem", name)
		}
	}

	state, plan := existing(t)
	req := planmodifier.StringRequest{ConfigValue: types.StringNull(), PlanValue: types.StringUnknown(), StateValue: types.StringValue("NFS"), State: state, Plan: plan}
	resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
	for _, m := range attributes["protocol"].(schema.StringAttribute).PlanModifiers {
		m.PlanModifyString(ctx, req, &resp)
	}
	if resp.RequiresReplace || !resp.PlanValue.Equal(types.StringValue("NFS")) {
		t.Errorf("protocol: an omitted value must keep %q without replacement, got %s replace=%v", "NFS", resp.PlanValue, resp.RequiresReplace)
	}
}

// TestFilesystemResourceModifyPlanFilesystemSource covers AC16. The plan
// checks the source of a new filesystem in the configuration, both for a
// create and for a replacement, and sends no write request.
func TestFilesystemResourceModifyPlanFilesystemSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)
	snapshotA := core.UUID{0xaa}
	snapshotB := core.UUID{0xbb}
	otherTypeID := core.UUID{3}

	ref := func(v types.String) types.Object { return reference.Value(reference.ID, v) }
	newSource := func() FilesystemResourceModel {
		m := zeroFilesystemResourceModel()
		m.Name = types.StringValue("fs")
		m.Size = types.Int64Value(100)
		m.Protocol = types.StringValue("NFS")
		m.FilesystemType = filesystemTypeSelectorCriteria("Standard NFS")
		m.Subnet = ref(types.StringValue(fsTestSubnetID.String()))
		return m
	}
	snapshotSource := func(id types.String) FilesystemResourceModel {
		m := zeroFilesystemResourceModel()
		m.Name = types.StringValue("fs")
		m.Snapshot = ref(id)
		return m
	}
	// fromSnapshot is the state of a filesystem created from snapshot A.
	fromSnapshot := func() FilesystemResourceModel {
		m := zeroFilesystemResourceModel()
		m.ID = types.StringValue(fsTestID.String())
		m.Name = types.StringValue("fs")
		m.Size = types.Int64Value(100)
		m.Protocol = types.StringValue("NFS")
		m.FilesystemType = selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
		m.Subnet = ref(types.StringValue(fsTestSubnetID.String()))
		m.Snapshot = ref(types.StringValue(snapshotA.String()))
		return m
	}
	// planned mirrors the Framework: a configured value is planned as
	// configured, and an omitted one keeps the state value.
	planned := func(config, state FilesystemResourceModel) FilesystemResourceModel {
		p := state
		p.Name = config.Name
		if !config.Size.IsNull() {
			p.Size = config.Size
		}
		if !config.Protocol.IsNull() {
			p.Protocol = config.Protocol
		}
		if !config.FilesystemType.IsNull() {
			p.FilesystemType = selector.WithID(config.FilesystemType, types.StringUnknown())
		}
		if !config.Subnet.IsNull() {
			p.Subnet = config.Subnet
		}
		if !config.Snapshot.IsNull() {
			p.Snapshot = config.Snapshot
		}
		return p
	}
	typePage := func(id core.UUID) string {
		enabled := filestoragepkg.FilesystemTypeStatusEnabled
		body, err := json.Marshal(filestoragepkg.PagedFilesystemTypeSchema{
			Count: 1,
			Results: []filestoragepkg.FilesystemTypeSchema{{
				Id: id, Name: "Premium NFS",
				Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
				Status: core.NullableFromPtr(&enabled),
			}},
		})
		if err != nil {
			t.Fatalf("marshal filesystem types: %v", err)
		}
		return string(body)
	}

	tests := []struct {
		name        string
		config      func() FilesystemResourceModel
		state       *FilesystemResourceModel
		catalog     string
		wantSummary string
		wantReplace bool
	}{
		{name: "create new filesystem", config: newSource},
		{name: "create from snapshot", config: func() FilesystemResourceModel { return snapshotSource(types.StringValue(snapshotA.String())) }},
		{name: "create from snapshot with unknown id", config: func() FilesystemResourceModel { return snapshotSource(types.StringUnknown()) }},
		{
			name: "create from snapshot with size",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Size = types.Int64Value(100)
				return m
			},
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "create from snapshot with protocol",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Protocol = types.StringValue("NFS")
				return m
			},
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "create from snapshot with filesystem_type",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.FilesystemType = filesystemTypeSelectorCriteria("Standard NFS")
				return m
			},
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "create from snapshot with subnet",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Subnet = ref(types.StringValue(fsTestSubnetID.String()))
				return m
			},
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "create from unknown snapshot with size",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringUnknown())
				m.Size = types.Int64Unknown()
				return m
			},
			wantSummary: "Conflicting filesystem source",
		},
		{
			name:        "create without size",
			config:      func() FilesystemResourceModel { m := newSource(); m.Size = types.Int64Null(); return m },
			wantSummary: "Missing filesystem source",
		},
		{
			name:        "create without protocol",
			config:      func() FilesystemResourceModel { m := newSource(); m.Protocol = types.StringNull(); return m },
			wantSummary: "Missing filesystem source",
		},
		{
			name: "create without filesystem_type",
			config: func() FilesystemResourceModel {
				m := newSource()
				m.FilesystemType = types.ObjectNull(filestorageselector.FilesystemTypeAttributeTypes())
				return m
			},
			wantSummary: "Missing filesystem source",
		},
		{
			name: "create without subnet",
			config: func() FilesystemResourceModel {
				m := newSource()
				m.Subnet = types.ObjectNull(reference.AttributeTypes(reference.ID))
				return m
			},
			wantSummary: "Missing filesystem source",
		},
		{
			name:   "from snapshot with source fields omitted",
			config: func() FilesystemResourceModel { return snapshotSource(types.StringValue(snapshotA.String())) },
			state:  new(fromSnapshot()),
		},
		{
			name: "from snapshot adds the state values",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Protocol = types.StringValue(" NFS ")
				m.Subnet = ref(types.StringValue(strings.ToUpper(fsTestSubnetID.String())))
				m.FilesystemType = filesystemTypeSelectorCriteria("Standard NFS")
				return m
			},
			state: new(fromSnapshot()),
		},
		{
			name: "from snapshot adds a larger size",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Size = types.Int64Value(200)
				return m
			},
			state: new(fromSnapshot()),
		},
		{
			name: "from snapshot with size changes snapshot",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotB.String()))
				m.Size = types.Int64Value(100)
				return m
			},
			state:       new(fromSnapshot()),
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "from snapshot adds another protocol",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Protocol = types.StringValue("SMB")
				return m
			},
			state:       new(fromSnapshot()),
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "from snapshot adds another subnet",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.Subnet = ref(types.StringValue(core.UUID{0x42}.String()))
				return m
			},
			state:       new(fromSnapshot()),
			wantSummary: "Conflicting filesystem source",
		},
		{
			name: "from snapshot adds another filesystem_type",
			config: func() FilesystemResourceModel {
				m := snapshotSource(types.StringValue(snapshotA.String()))
				m.FilesystemType = filesystemTypeSelectorCriteria("Premium NFS")
				return m
			},
			state:       new(fromSnapshot()),
			catalog:     typePage(otherTypeID),
			wantSummary: "Conflicting filesystem source",
			wantReplace: true,
		},
		{
			name: "from snapshot replaced by a new filesystem",
			config: func() FilesystemResourceModel {
				m := newSource()
				m.Protocol = types.StringValue("SMB")
				return m
			},
			state: new(fromSnapshot()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				if tt.catalog != "" && req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/") {
					return fsHTTPResponse(req, http.StatusOK, tt.catalog), nil
				}
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
			})

			configModel := tt.config()
			config := tfsdk.Config{Schema: schemaResp.Schema}
			stateValue := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
			plannedModel := configModel
			plannedModel.ID = types.StringUnknown()
			if tt.state != nil {
				if diags := stateValue.Set(ctx, *tt.state); diags.HasError() {
					t.Fatalf("set state: %v", diags)
				}
				plannedModel = planned(configModel, *tt.state)
			}
			configState := tfsdk.State{Schema: schemaResp.Schema}
			if diags := configState.Set(ctx, configModel); diags.HasError() {
				t.Fatalf("set config: %v", diags)
			}
			config.Raw = configState.Raw
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := plan.Set(ctx, plannedModel); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}

			r := &FilesystemResource{client: client, projectID: fsTestProjectID}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: stateValue, Config: config}, &resp)
			if tt.wantSummary == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
				}
			} else {
				errs := resp.Diagnostics.Errors()
				if len(errs) != 1 || errs[0].Summary() != tt.wantSummary {
					t.Fatalf("expected one %q diagnostic, got %v", tt.wantSummary, resp.Diagnostics)
				}
			}
			if replaced := len(resp.RequiresReplace) > 0; replaced != tt.wantReplace {
				t.Fatalf("requires replace = %v, want %v (paths %v)", replaced, tt.wantReplace, resp.RequiresReplace)
			}
		})
	}
}

func TestFilesystemResourceCreate(t *testing.T) {
	t.Parallel()

	typeJSONStr := func() string {
		enabled := filestoragepkg.FilesystemTypeStatusEnabled
		st := core.NullableFromPtr(&enabled)
		page := filestoragepkg.PagedFilesystemTypeSchema{
			Count: 1,
			Results: []filestoragepkg.FilesystemTypeSchema{{
				Id:     fsTestTypeID,
				Name:   "Standard NFS",
				Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
				Status: st,
			}},
		}
		body, _ := json.Marshal(page)
		return string(body)
	}()

	planned := zeroFilesystemResourceModel()
	planned.Name = types.StringValue("my-fs")
	planned.Size = types.Int64Value(100)
	planned.Protocol = types.StringValue("NFS")
	planned.FilesystemType = filesystemTypeSelectorCriteria("Standard NFS")
	planned.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))

	buildPlan := func(t *testing.T) tfsdk.Plan {
		t.Helper()
		schemaResp := filesystemResourceSchema(t)
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		if diags := plan.Set(context.Background(), planned); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		return plan
	}

	t.Run("available", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			fs := sampleFilesystem()
			fsJSONStr := filesystemJSON(fs)

			creates := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/"):
					return fsHTTPResponse(req, http.StatusOK, typeJSONStr), nil
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/filesystems/"):
					creates++
					return fsHTTPResponse(req, http.StatusCreated, fsJSONStr), nil
				case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
					gets++
					return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
				}
			})

			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.CreateResponse{State: tfsdk.State{Schema: filesystemResourceSchema(t).Schema, Raw: nullFilesystemRaw(t)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: buildPlan(t)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("create failed: %v", resp.Diagnostics)
			}
			if creates != 1 || gets != 1 {
				t.Fatalf("unexpected request counts: creates=%d gets=%d", creates, gets)
			}
			if waited := time.Since(start); waited != 0 {
				t.Fatalf("expected no wait, elapsed %s", waited)
			}

			var result FilesystemResourceModel
			if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
				t.Fatalf("read state: %v", diags)
			}
			if result.ID.ValueString() != fsTestID.String() {
				t.Fatalf("unexpected ID: %s", result.ID.ValueString())
			}
			if result.Name.ValueString() != fs.Name {
				t.Fatalf("unexpected name: %s", result.Name.ValueString())
			}
		})
	})

	t.Run("fail", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			creating := sampleFilesystem()
			creating.Status = filestoragepkg.FilesystemStatusCreating
			creatingJSONStr := filesystemJSON(creating)

			failed := sampleFilesystem()
			failed.Status = filestoragepkg.FilesystemStatusFail
			failedJSONStr := filesystemJSON(failed)

			creates := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/"):
					return fsHTTPResponse(req, http.StatusOK, typeJSONStr), nil
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/filesystems/"):
					creates++
					return fsHTTPResponse(req, http.StatusCreated, creatingJSONStr), nil
				case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
					gets++
					return fsHTTPResponse(req, http.StatusOK, failedJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
				}
			})

			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.CreateResponse{State: tfsdk.State{Schema: filesystemResourceSchema(t).Schema, Raw: nullFilesystemRaw(t)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: buildPlan(t)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected terminal-failure diagnostic, got none")
			}
			if creates != 1 || gets != 1 {
				t.Fatalf("unexpected request counts: creates=%d gets=%d", creates, gets)
			}
			if waited := time.Since(start); waited != 0 {
				t.Fatalf("expected waiter to stop on first poll, elapsed %s", waited)
			}
		})
	})

	t.Run("timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			creating := sampleFilesystem()
			creating.Status = filestoragepkg.FilesystemStatusCreating
			creatingJSONStr := filesystemJSON(creating)

			creates := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/"):
					return fsHTTPResponse(req, http.StatusOK, typeJSONStr), nil
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/filesystems/"):
					creates++
					return fsHTTPResponse(req, http.StatusCreated, creatingJSONStr), nil
				case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
					gets++
					return fsHTTPResponse(req, http.StatusOK, creatingJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
				}
			})

			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.CreateResponse{State: tfsdk.State{Schema: filesystemResourceSchema(t).Schema, Raw: nullFilesystemRaw(t)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: buildPlan(t)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected timeout diagnostic, got none")
			}
			if creates != 1 {
				t.Fatalf("expected exactly one create, got %d", creates)
			}
			minGets := int(defaultFilesystemTimeout / defaultFilesystemPollInterval)
			if gets < minGets {
				t.Fatalf("expected at least %d polls, got %d", minGets, gets)
			}
			if waited := time.Since(start); waited != defaultFilesystemTimeout {
				t.Fatalf("expected waiter to time out after %s, elapsed %s", defaultFilesystemTimeout, waited)
			}
		})
	})
}

func TestFilesystemResourceReadNotFoundRemovesResource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	r := &FilesystemResource{client: client, projectID: fsTestProjectID}

	current := zeroFilesystemResourceModel()
	current.ID = types.StringValue(fsTestID.String())
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, current); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("state must be removed when filesystem not found")
	}
}

func TestFilesystemResourceDelete(t *testing.T) {
	t.Parallel()

	buildState := func(t *testing.T) tfsdk.State {
		t.Helper()
		schemaResp := filesystemResourceSchema(t)
		current := zeroFilesystemResourceModel()
		current.ID = types.StringValue(fsTestID.String())
		state := tfsdk.State{Schema: schemaResp.Schema}
		if diags := state.Set(context.Background(), current); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		return state
	}

	t.Run("waits_for_gone", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			deleting := sampleFilesystem()
			deleting.Status = filestoragepkg.FilesystemStatusDeleting
			deletingJSONStr := filesystemJSON(deleting)

			deletes := 0
			getCalls := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.Method {
				case http.MethodDelete:
					deletes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case http.MethodGet:
					getCalls++
					if getCalls <= 1 {
						return fsHTTPResponse(req, http.StatusOK, deletingJSONStr), nil
					}
					return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: buildState(t)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("delete failed: %v", resp.Diagnostics)
			}
			if deletes != 1 {
				t.Fatalf("expected exactly one delete, got %d", deletes)
			}
			if getCalls < 2 {
				t.Fatalf("expected waiter to poll at least twice; got %d get calls", getCalls)
			}
			if waited := time.Since(start); waited != defaultFilesystemPollInterval {
				t.Fatalf("expected waiter to wait exactly one poll interval (%s), elapsed %s", defaultFilesystemPollInterval, waited)
			}
		})
	})

	t.Run("fail", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			failed := sampleFilesystem()
			failed.Status = filestoragepkg.FilesystemStatusFail
			failedJSONStr := filesystemJSON(failed)

			deletes := 0
			getCalls := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.Method {
				case http.MethodDelete:
					deletes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case http.MethodGet:
					getCalls++
					return fsHTTPResponse(req, http.StatusOK, failedJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: buildState(t)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected terminal-failure diagnostic, got none")
			}
			if deletes != 1 || getCalls != 1 {
				t.Fatalf("unexpected request counts: deletes=%d getCalls=%d", deletes, getCalls)
			}
			if waited := time.Since(start); waited != 0 {
				t.Fatalf("expected waiter to stop on first poll, elapsed %s", waited)
			}
		})
	})

	t.Run("timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			deleting := sampleFilesystem()
			deleting.Status = filestoragepkg.FilesystemStatusDeleting
			deletingJSONStr := filesystemJSON(deleting)

			deletes := 0
			getCalls := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.Method {
				case http.MethodDelete:
					deletes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case http.MethodGet:
					getCalls++
					return fsHTTPResponse(req, http.StatusOK, deletingJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: buildState(t)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected timeout diagnostic, got none")
			}
			if deletes != 1 {
				t.Fatalf("expected exactly one delete, got %d", deletes)
			}
			minGets := int(defaultFilesystemTimeout / defaultFilesystemPollInterval)
			if getCalls < minGets {
				t.Fatalf("expected at least %d polls, got %d", minGets, getCalls)
			}
			if waited := time.Since(start); waited != defaultFilesystemTimeout {
				t.Fatalf("expected waiter to time out after %s, elapsed %s", defaultFilesystemTimeout, waited)
			}
		})
	})

	t.Run("delete_not_found", func(t *testing.T) {
		t.Parallel()
		getCalls := 0
		client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodDelete:
				return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			case http.MethodGet:
				getCalls++
				return fsHTTPResponse(req, http.StatusOK, filesystemJSON(sampleFilesystem())), nil
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
			}
		})
		r := &FilesystemResource{
			client:       client,
			projectID:    fsTestProjectID,
			pollInterval: defaultFilesystemPollInterval,
			timeout:      defaultFilesystemTimeout,
		}
		resp := resource.DeleteResponse{}
		r.Delete(context.Background(), resource.DeleteRequest{State: buildState(t)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("expected success on 404 DELETE, got: %v", resp.Diagnostics)
		}
		if getCalls != 0 {
			t.Fatalf("expected no GET calls after 404 DELETE, got %d", getCalls)
		}
	})
}

func TestFilesystemResourceUpdate(t *testing.T) {
	t.Parallel()

	schemaResp := filesystemResourceSchema(t)

	buildState := func(t *testing.T, m FilesystemResourceModel) tfsdk.State {
		t.Helper()
		state := tfsdk.State{Schema: schemaResp.Schema}
		if diags := state.Set(context.Background(), m); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
		return state
	}
	buildPlan := func(t *testing.T, m FilesystemResourceModel) tfsdk.Plan {
		t.Helper()
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		if diags := plan.Set(context.Background(), m); diags.HasError() {
			t.Fatalf("set plan: %v", diags)
		}
		return plan
	}
	currentModel := func() FilesystemResourceModel {
		m := zeroFilesystemResourceModel()
		m.ID = types.StringValue(fsTestID.String())
		m.Name = types.StringValue("old-name")
		m.Size = types.Int64Value(100)
		m.Protocol = types.StringValue("NFS")
		m.FilesystemType = selector.WithID(
			filesystemTypeSelectorCriteria("Standard NFS"),
			types.StringValue(fsTestTypeID.String()),
		)
		m.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))
		return m
	}

	t.Run("metadata_only", func(t *testing.T) {
		t.Parallel()
		planModel := currentModel()
		planModel.Name = types.StringValue("new-name")

		fs := sampleFilesystem()
		fs.Name = "new-name"
		fsJSONStr := filesystemJSON(fs)

		puts := 0
		gets := 0
		client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodPut:
				puts++
				return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
			case http.MethodGet:
				gets++
				return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
			}
		})
		r := &FilesystemResource{
			client:       client,
			projectID:    fsTestProjectID,
			pollInterval: defaultFilesystemPollInterval,
			timeout:      defaultFilesystemTimeout,
		}

		resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
		r.Update(context.Background(), resource.UpdateRequest{
			Plan:  buildPlan(t, planModel),
			State: buildState(t, currentModel()),
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update failed: %v", resp.Diagnostics)
		}
		// State comes from the PUT response, so no GET follows it.
		if puts != 1 || gets != 0 {
			t.Fatalf("unexpected request counts: puts=%d gets=%d", puts, gets)
		}
		var result FilesystemResourceModel
		if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
			t.Fatalf("read state: %v", diags)
		}
		if result.Name.ValueString() != "new-name" {
			t.Fatalf("unexpected name: %s", result.Name.ValueString())
		}
	})

	t.Run("spelling_only", func(t *testing.T) {
		t.Parallel()
		planModel := currentModel()
		planModel.Name = types.StringValue(" old-name ")

		fs := sampleFilesystem()
		fs.Name = "old-name"
		fsJSONStr := filesystemJSON(fs)

		gets := 0
		client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				gets++
				return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
			}
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
		})
		r := &FilesystemResource{
			client:       client,
			projectID:    fsTestProjectID,
			pollInterval: defaultFilesystemPollInterval,
			timeout:      defaultFilesystemTimeout,
		}

		resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
		r.Update(context.Background(), resource.UpdateRequest{
			Plan:  buildPlan(t, planModel),
			State: buildState(t, currentModel()),
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update failed: %v", resp.Diagnostics)
		}
		if gets != 1 {
			t.Fatalf("expected one GET for a spelling-only update, got %d", gets)
		}
		var result FilesystemResourceModel
		if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
			t.Fatalf("read state: %v", diags)
		}
		if result.Name.ValueString() != " old-name " {
			t.Fatalf("expected the configured spelling in state, got %q", result.Name.ValueString())
		}
	})

	t.Run("resize/available", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			planModel := currentModel()
			planModel.Size = types.Int64Value(200)

			resized := sampleFilesystem()
			resized.Size = 200
			resizedJSONStr := filesystemJSON(resized)

			puts := 0
			resizes := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut:
					puts++
					return fsHTTPResponse(req, http.StatusOK, resizedJSONStr), nil
				case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "resize"):
					resizes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case req.Method == http.MethodGet:
					gets++
					return fsHTTPResponse(req, http.StatusOK, resizedJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:  buildPlan(t, planModel),
				State: buildState(t, currentModel()),
			}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("update failed: %v", resp.Diagnostics)
			}
			if puts != 0 || resizes != 1 {
				t.Fatalf("unexpected request counts: puts=%d resizes=%d gets=%d", puts, resizes, gets)
			}
			// State comes from the successful resize poll, so no GET follows it.
			if gets != 1 {
				t.Fatalf("expected exactly one GET (the resize poll), got %d", gets)
			}
			if waited := time.Since(start); waited != 0 {
				t.Fatalf("expected no wait when resize completes on first poll, elapsed %s", waited)
			}
			var result FilesystemResourceModel
			if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
				t.Fatalf("read state: %v", diags)
			}
			if result.Size.ValueInt64() != 200 {
				t.Fatalf("unexpected size in state: %d", result.Size.ValueInt64())
			}
		})
	})

	// AC17: a filesystem created from a snapshot grows in place when the
	// configuration adds a larger size.
	t.Run("resize/from_snapshot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			snapshotID := core.UUID{0xaa}
			current := currentModel()
			current.Snapshot = reference.Value(reference.ID, types.StringValue(snapshotID.String()))
			planModel := current
			planModel.Size = types.Int64Value(200)

			resized := sampleFilesystem()
			resized.Size = 200
			resized.CreateFrom.Snapshot = core.NullableFromPtr(&filestoragepkg.NestedFilesystemSnapshotSchema{Id: snapshotID})
			resizedJSONStr := filesystemJSON(resized)

			var resizeBody map[string]any
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/resize/"):
					if err := json.NewDecoder(req.Body).Decode(&resizeBody); err != nil {
						t.Errorf("decode resize body: %v", err)
					}
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case req.Method == http.MethodGet:
					return fsHTTPResponse(req, http.StatusOK, resizedJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{client: client, projectID: fsTestProjectID}

			resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:  buildPlan(t, planModel),
				State: buildState(t, current),
			}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("update failed: %v", resp.Diagnostics)
			}
			if resizeBody["new_size"] != float64(200) {
				t.Fatalf("resize body = %v, want new_size 200", resizeBody)
			}
			var result FilesystemResourceModel
			if diags := resp.State.Get(context.Background(), &result); diags.HasError() {
				t.Fatalf("read state: %v", diags)
			}
			if result.ID.ValueString() != fsTestID.String() || result.Size.ValueInt64() != 200 {
				t.Fatalf("id, size = %s, %s, want unchanged ID and 200", result.ID, result.Size)
			}
			if id := reference.Get(result.Snapshot, reference.ID).ValueString(); id != snapshotID.String() {
				t.Fatalf("snapshot.id = %q, want %q", id, snapshotID)
			}
		})
	})

	t.Run("resize/fail", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			planModel := currentModel()
			planModel.Size = types.Int64Value(200)

			failed := sampleFilesystem()
			failed.Status = filestoragepkg.FilesystemStatusFail
			failedJSONStr := filesystemJSON(failed)

			puts := 0
			resizes := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut:
					puts++
					return fsHTTPResponse(req, http.StatusOK, failedJSONStr), nil
				case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "resize"):
					resizes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case req.Method == http.MethodGet:
					gets++
					return fsHTTPResponse(req, http.StatusOK, failedJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:  buildPlan(t, planModel),
				State: buildState(t, currentModel()),
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected terminal-failure diagnostic, got none")
			}
			if puts != 0 || resizes != 1 || gets != 1 {
				t.Fatalf("unexpected request counts: puts=%d resizes=%d gets=%d", puts, resizes, gets)
			}
			if waited := time.Since(start); waited != 0 {
				t.Fatalf("expected waiter to stop on first poll, elapsed %s", waited)
			}
		})
	})

	t.Run("resize/timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			planModel := currentModel()
			planModel.Size = types.Int64Value(200)

			resizing := sampleFilesystem()
			resizing.Status = filestoragepkg.FilesystemStatusResizing
			resizingJSONStr := filesystemJSON(resizing)

			puts := 0
			resizes := 0
			gets := 0
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch {
				case req.Method == http.MethodPut:
					puts++
					return fsHTTPResponse(req, http.StatusOK, resizingJSONStr), nil
				case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "resize"):
					resizes++
					return fsHTTPResponse(req, http.StatusAccepted, "{}"), nil
				case req.Method == http.MethodGet:
					gets++
					return fsHTTPResponse(req, http.StatusOK, resizingJSONStr), nil
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusInternalServerError, ""), nil
				}
			})
			r := &FilesystemResource{
				client:       client,
				projectID:    fsTestProjectID,
				pollInterval: defaultFilesystemPollInterval,
				timeout:      defaultFilesystemTimeout,
			}

			start := time.Now()
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:  buildPlan(t, planModel),
				State: buildState(t, currentModel()),
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected timeout diagnostic, got none")
			}
			if puts != 0 || resizes != 1 {
				t.Fatalf("expected no PUT and one resize, got: puts=%d resizes=%d gets=%d", puts, resizes, gets)
			}
			minGets := int(defaultFilesystemTimeout / defaultFilesystemPollInterval)
			if gets < minGets {
				t.Fatalf("expected at least %d polls, got %d", minGets, gets)
			}
			if waited := time.Since(start); waited != defaultFilesystemTimeout {
				t.Fatalf("expected waiter to time out after %s, elapsed %s", defaultFilesystemTimeout, waited)
			}
		})
	})
}

func TestFilesystemResourceImportState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
	(&FilesystemResource{}).ImportState(ctx, resource.ImportStateRequest{ID: fsTestID.String()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("import failed: %v", resp.Diagnostics)
	}

	var id types.String
	if diags := resp.State.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		t.Fatalf("read id: %v", diags)
	}
	if id.ValueString() != fsTestID.String() {
		t.Fatalf("unexpected id: %s", id.ValueString())
	}
}

func TestFilesystemResourceImportStateRejectsInvalidID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
	(&FilesystemResource{}).ImportState(ctx, resource.ImportStateRequest{ID: "not-a-uuid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestBuildFilesystemUpdateBody(t *testing.T) {
	t.Parallel()
	m := zeroFilesystemResourceModel()
	m.Name = types.StringValue("  my-fs  ")

	// null description → not sent
	m.Description = types.StringNull()
	body := buildFilesystemUpdateBody(m)
	name, err := body.Name.Get()
	if err != nil || name != "my-fs" {
		t.Fatalf("expected name='my-fs', got name=%q err=%v", name, err)
	}
	if body.Description.IsSpecified() {
		t.Fatal("null description must not be sent in body")
	}

	// empty string description → null in body
	m.Description = types.StringValue("")
	body = buildFilesystemUpdateBody(m)
	if !body.Description.IsNull() {
		t.Fatal("empty description must set null in body")
	}

	// non-empty description → value in body
	m.Description = types.StringValue("hello")
	body = buildFilesystemUpdateBody(m)
	desc, err := body.Description.Get()
	if err != nil || desc != "hello" {
		t.Fatalf("expected description='hello', got desc=%q err=%v", desc, err)
	}
}

func TestNormalizeFilesystemDescription(t *testing.T) {
	cases := []struct {
		name  string
		api   *string
		prior types.String
		want  types.String
	}{
		{"backend empty, omitted", new(""), types.StringUnknown(), types.StringNull()},
		{"backend empty, prior null", new(""), types.StringNull(), types.StringNull()},
		{"backend null, cleared", nil, types.StringValue(""), types.StringValue("")},
		{"backend empty, cleared", new(""), types.StringValue(""), types.StringValue("")},
		{"backend null, prior value", nil, types.StringValue("old"), types.StringNull()},
		{"backend value", new("hello"), types.StringValue(""), types.StringValue("hello")},
		{"backend value, padded prior", new("hello"), types.StringValue(" hello "), types.StringValue(" hello ")},
		{"backend null, blank prior", nil, types.StringValue("  "), types.StringValue("  ")},
		{"backend empty, blank prior", new(""), types.StringValue("  "), types.StringValue("  ")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := &filestoragepkg.FilesystemSchema{Description: core.NullableFromPtr(tc.api)}
			if got := normalizeFilesystemDescription(api, tc.prior); !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestFilesystemResourceTrimsUserSuppliedStrings covers AC15: Create sends
// the trimmed name, description, and protocol, and Create, Read, and an Update
// that changes only spelling keep the configured spelling in state. That
// Update sends no PUT but still reads the filesystem, so no computed attribute
// stays unknown.
func TestFilesystemResourceTrimsUserSuppliedStrings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	fs := sampleFilesystem()
	fs.Description = core.NullableFromPtr(new("shared files"))
	fsJSONStr := filesystemJSON(fs)
	typeJSONStr := func() string {
		enabled := filestoragepkg.FilesystemTypeStatusEnabled
		body, _ := json.Marshal(filestoragepkg.PagedFilesystemTypeSchema{
			Count: 1,
			Results: []filestoragepkg.FilesystemTypeSchema{{
				Id: fsTestTypeID, Name: "Standard NFS",
				Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
				Status: core.NullableFromPtr(&enabled),
			}},
		})
		return string(body)
	}()

	var created map[string]any
	puts := 0
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/"):
			return fsHTTPResponse(req, http.StatusOK, typeJSONStr), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/filesystems/"):
			if err := json.NewDecoder(req.Body).Decode(&created); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			return fsHTTPResponse(req, http.StatusCreated, fsJSONStr), nil
		case req.Method == http.MethodPut:
			puts++
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})
	r := &FilesystemResource{client: client, projectID: fsTestProjectID}

	planned := zeroFilesystemResourceModel()
	planned.Name = types.StringValue(" my-fs ")
	planned.Description = types.StringValue(" shared files ")
	planned.Size = types.Int64Value(100)
	planned.Protocol = types.StringValue(" NFS ")
	planned.FilesystemType = filesystemTypeSelectorCriteria(" Standard NFS ")
	planned.Subnet = reference.Value(reference.ID, types.StringValue(" "+strings.ToUpper(fsTestSubnetID.String())+" "))
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(ctx, planned); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}

	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}
	for field, want := range map[string]string{"name": "my-fs", "description": "shared files", "protocol": "NFS", "subnet_id": fsTestSubnetID.String()} {
		if got := created[field]; got != want {
			t.Errorf("create body %s = %#v, want %q", field, got, want)
		}
	}

	checkSpelling := func(t *testing.T, step string, state tfsdk.State) {
		t.Helper()
		var got FilesystemResourceModel
		if diags := state.Get(ctx, &got); diags.HasError() {
			t.Fatalf("%s: read state: %v", step, diags)
		}
		if !got.Name.Equal(planned.Name) || !got.Description.Equal(planned.Description) || !got.Protocol.Equal(planned.Protocol) || !got.Subnet.Equal(planned.Subnet) {
			t.Fatalf("%s: configured spelling lost: name=%s description=%s protocol=%s subnet=%s", step, got.Name, got.Description, got.Protocol, got.Subnet)
		}
		if got.Status.IsUnknown() || got.UpdatedAt.IsUnknown() || got.HasSnapshot.IsUnknown() || got.SnapshotPolicy.IsUnknown() {
			t.Fatalf("%s: computed attribute left unknown", step)
		}
	}
	checkSpelling(t, "create", createResp.State)

	readResp := resource.ReadResponse{State: createResp.State}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	checkSpelling(t, "read", readResp.State)

	var prior FilesystemResourceModel
	if diags := readResp.State.Get(ctx, &prior); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	respelled := prior
	respelled.Name = types.StringValue("my-fs  ")
	respelled.Description = types.StringValue("shared files")
	respelled.Status = types.StringUnknown()
	respelled.UpdatedAt = types.StringUnknown()
	respelled.HasSnapshot = types.BoolUnknown()
	respelled.SnapshotPolicy = types.ObjectUnknown(snapshotPolicyAttrTypes)
	updatePlan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := updatePlan.Set(ctx, respelled); diags.HasError() {
		t.Fatalf("set update plan: %v", diags)
	}
	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}
	if puts != 0 {
		t.Fatalf("a change of spelling must not send PUT, got %d", puts)
	}
	planned.Name = respelled.Name
	planned.Description = respelled.Description
	checkSpelling(t, "update", updateResp.State)
}

// TestFilesystemResourceUpdateSendsTrimmedMetadata covers AC15 for PUT: a
// real name or description change sends the trimmed values.
func TestFilesystemResourceUpdateSendsTrimmedMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemResourceSchema(t)

	fs := sampleFilesystem()
	fs.Name = "new-name"
	fs.Description = core.NullableFromPtr(new("new text"))
	fsJSONStr := filesystemJSON(fs)
	var sent map[string]any
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut {
			if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
				t.Errorf("decode update body: %v", err)
			}
		}
		return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
	})
	r := &FilesystemResource{client: client, projectID: fsTestProjectID}

	current := zeroFilesystemResourceModel()
	current.ID = types.StringValue(fsTestID.String())
	current.Name = types.StringValue("old-name")
	current.Size = types.Int64Value(100)
	current.Protocol = types.StringValue("NFS")
	current.FilesystemType = selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
	current.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))
	planned := current
	planned.Name = types.StringValue(" new-name ")
	planned.Description = types.StringValue(" new text ")

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, current); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(ctx, planned); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", resp.Diagnostics)
	}
	if sent["name"] != "new-name" || sent["description"] != "new text" {
		t.Fatalf("unexpected PUT body: %#v", sent)
	}
	var got FilesystemResourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if !got.Name.Equal(planned.Name) || !got.Description.Equal(planned.Description) {
		t.Fatalf("configured spelling lost: name=%s description=%s", got.Name, got.Description)
	}
}

// TestFilesystemResourceCreateFromSnapshot covers AC10 offline: Create sends
// the configured snapshot as snapshot_id and records the source snapshot that
// the backend reports, keeping the configured spelling of the UUID.
// TestFilesystemResourceCreateSource covers AC10 and Q8. The create body
// carries the keys of one source only, and a filesystem created from a
// snapshot takes size, protocol, filesystem_type, and subnet from the response.
func TestFilesystemResourceCreateSource(t *testing.T) {
	t.Parallel()
	snapshotID := core.UUID{0xab, 0xcd, 3}
	sourceSubnetID := core.UUID{0x55}
	schemaResp := filesystemResourceSchema(t)

	enabled := filestoragepkg.FilesystemTypeStatusEnabled
	typePage, err := json.Marshal(filestoragepkg.PagedFilesystemTypeSchema{
		Count: 1,
		Results: []filestoragepkg.FilesystemTypeSchema{{
			Id:     fsTestTypeID,
			Name:   "Standard NFS",
			Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
			Status: core.NullableFromPtr(&enabled),
		}},
	})
	if err != nil {
		t.Fatalf("marshal filesystem types: %v", err)
	}

	// The Framework plans an omitted Optional and Computed value as unknown.
	unknownSource := func(m *FilesystemResourceModel) {
		m.Size = types.Int64Unknown()
		m.Protocol = types.StringUnknown()
		m.FilesystemType = types.ObjectUnknown(filestorageselector.FilesystemTypeAttributeTypes())
		m.Subnet = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
	}
	configured := strings.ToUpper(snapshotID.String())

	tests := []struct {
		name       string
		plan       func() FilesystemResourceModel
		response   func() *filestoragepkg.FilesystemSchema
		wantKeys   []string
		wantBody   map[string]any
		wantLookup bool
		check      func(t *testing.T, got FilesystemResourceModel)
	}{
		{
			name: "new filesystem",
			plan: func() FilesystemResourceModel {
				m := zeroFilesystemResourceModel()
				m.ID = types.StringUnknown()
				m.Name = types.StringValue("my-fs")
				m.Size = types.Int64Value(100)
				m.Protocol = types.StringValue("NFS")
				m.FilesystemType = types.ObjectValueMust(filestorageselector.FilesystemTypeAttributeTypes(), map[string]attr.Value{
					"id":        types.StringUnknown(),
					"name":      types.StringValue("Standard NFS"),
					"zone_id":   types.StringUnknown(),
					"zone_name": types.StringUnknown(),
				})
				m.Subnet = reference.Value(reference.ID, types.StringValue(fsTestSubnetID.String()))
				m.Snapshot = types.ObjectUnknown(reference.AttributeTypes(reference.ID))
				return m
			},
			response: sampleFilesystem,
			wantKeys: []string{"filesystem_type_id", "name", "protocol", "size", "subnet_id"},
			wantBody: map[string]any{
				"filesystem_type_id": fsTestTypeID.String(),
				"name":               "my-fs",
				"protocol":           "NFS",
				"size":               float64(100),
				"subnet_id":          fsTestSubnetID.String(),
			},
			wantLookup: true,
			check: func(t *testing.T, got FilesystemResourceModel) {
				want := selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
				if !got.FilesystemType.Equal(want) {
					t.Errorf("filesystem_type = %s, want %s with omitted criteria null", got.FilesystemType, want)
				}
				if !got.Snapshot.IsNull() {
					t.Errorf("snapshot = %s, want null", got.Snapshot)
				}
			},
		},
		{
			name: "from snapshot",
			plan: func() FilesystemResourceModel {
				m := zeroFilesystemResourceModel()
				m.ID = types.StringUnknown()
				m.Name = types.StringValue("my-fs")
				m.Description = types.StringValue("restored")
				unknownSource(&m)
				m.Snapshot = reference.Value(reference.ID, types.StringValue(configured))
				return m
			},
			response: func() *filestoragepkg.FilesystemSchema {
				fs := sampleFilesystem()
				fs.Size = 300
				fs.Protocol = filestoragepkg.FilesystemProtocolSMB
				fs.Description = core.NullableFromPtr(new("restored"))
				fs.Subnet = filestoragepkg.NestedSubnetSchema{Id: sourceSubnetID, Cidr: "10.0.1.0/24"}
				fs.CreateFrom.Snapshot = core.NullableFromPtr(&filestoragepkg.NestedFilesystemSnapshotSchema{Id: snapshotID, Name: "snap-a"})
				return fs
			},
			wantKeys: []string{"description", "name", "snapshot_id"},
			wantBody: map[string]any{"description": "restored", "name": "my-fs", "snapshot_id": snapshotID.String()},
			check: func(t *testing.T, got FilesystemResourceModel) {
				if got.Size.ValueInt64() != 300 || got.Protocol.ValueString() != "SMB" {
					t.Errorf("size, protocol = %s, %s, want 300, SMB from the response", got.Size, got.Protocol)
				}
				if id := reference.Get(got.Subnet, reference.ID).ValueString(); id != sourceSubnetID.String() {
					t.Errorf("subnet.id = %q, want %q", id, sourceSubnetID)
				}
				want := selector.WithID(filesystemTypeSelectorCriteria("Standard NFS"), types.StringValue(fsTestTypeID.String()))
				if !got.FilesystemType.Equal(want) {
					t.Errorf("filesystem_type = %s, want %s", got.FilesystemType, want)
				}
				if id := reference.Get(got.Snapshot, reference.ID).ValueString(); id != configured {
					t.Errorf("snapshot.id = %q, want configured %q", id, configured)
				}
			},
		},
		{
			name: "from snapshot without reported type",
			plan: func() FilesystemResourceModel {
				m := zeroFilesystemResourceModel()
				m.ID = types.StringUnknown()
				m.Name = types.StringValue("my-fs")
				unknownSource(&m)
				m.Snapshot = reference.Value(reference.ID, types.StringValue(snapshotID.String()))
				return m
			},
			response: func() *filestoragepkg.FilesystemSchema {
				fs := sampleFilesystem()
				fs.CreateFrom = filestoragepkg.NestedFilesystemOriginSchema{
					Snapshot: core.NullableFromPtr(&filestoragepkg.NestedFilesystemSnapshotSchema{Id: snapshotID}),
				}
				return fs
			},
			wantKeys: []string{"name", "snapshot_id"},
			wantBody: map[string]any{"name": "my-fs", "snapshot_id": snapshotID.String()},
			check: func(t *testing.T, got FilesystemResourceModel) {
				if !got.FilesystemType.IsNull() {
					t.Errorf("filesystem_type = %s, want null", got.FilesystemType)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if diags := plan.Set(context.Background(), tt.plan()); diags.HasError() {
				t.Fatalf("set plan: %v", diags)
			}
			fsJSONStr := filesystemJSON(tt.response())
			synctest.Test(t, func(t *testing.T) {
				var body map[string]any
				lookups := 0
				client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
					switch {
					case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystem-types/"):
						lookups++
						return fsHTTPResponse(req, http.StatusOK, string(typePage)), nil
					case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/filesystems/"):
						if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
							t.Errorf("decode create body: %v", err)
						}
						return fsHTTPResponse(req, http.StatusCreated, fsJSONStr), nil
					case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
						return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
					default:
						t.Errorf("unexpected request: %s %s", req.Method, req.URL)
						return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
					}
				})
				r := &FilesystemResource{
					client:       client,
					projectID:    fsTestProjectID,
					pollInterval: defaultFilesystemPollInterval,
					timeout:      defaultFilesystemTimeout,
				}

				resp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: nullFilesystemRaw(t)}}
				r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("create failed: %v", resp.Diagnostics)
				}
				if got := slices.Sorted(maps.Keys(body)); !slices.Equal(got, tt.wantKeys) {
					t.Fatalf("create body keys = %v, want %v", got, tt.wantKeys)
				}
				for key, want := range tt.wantBody {
					if body[key] != want {
						t.Errorf("create body %s = %v, want %v", key, body[key], want)
					}
				}
				if (lookups > 0) != tt.wantLookup {
					t.Errorf("filesystem type lookups = %d, want lookup %v", lookups, tt.wantLookup)
				}
				if !resp.State.Raw.IsFullyKnown() {
					t.Fatalf("state has unknown values: %s", resp.State.Raw)
				}
				var got FilesystemResourceModel
				if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
					t.Fatalf("read state: %v", diags)
				}
				tt.check(t, got)
			})
		})
	}
}

func TestRefreshFilesystemSnapshot(t *testing.T) {
	t.Parallel()
	a := core.UUID{0xaa}
	b := core.UUID{0xbb}
	ref := func(id string) types.Object { return reference.Value(reference.ID, types.StringValue(id)) }
	null := reference.Value(reference.ID, types.StringNull())
	unknown := reference.Value(reference.ID, types.StringUnknown())
	cases := []struct {
		name   string
		origin *core.UUID
		prior  types.Object
		want   types.Object
	}{
		{"created without snapshot", nil, unknown, null},
		{"no origin keeps null", nil, null, null},
		{"no origin clears recorded snapshot", nil, ref(a.String()), null},
		{"import records origin", &a, null, ref(a.String())},
		{"create records origin", &a, unknown, ref(a.String())},
		{"same snapshot keeps configured spelling", &a, ref(strings.ToUpper(a.String())), ref(strings.ToUpper(a.String()))},
		{"different origin is reported", &b, ref(a.String()), ref(b.String())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := sampleFilesystem()
			if tc.origin != nil {
				api.CreateFrom.Snapshot = core.NullableFromPtr(&filestoragepkg.NestedFilesystemSnapshotSchema{Id: *tc.origin})
			}
			if got := refreshFilesystemSnapshot(api, tc.prior); !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
