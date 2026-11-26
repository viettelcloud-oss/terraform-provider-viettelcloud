package blockstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func volumeBackupPolicyPageJSON(t *testing.T, policies ...blockstoragesdk.VolumeBackupPolicySchema) string {
	t.Helper()
	body, err := json.Marshal(blockstoragesdk.PagedVolumeBackupPolicySchema{Count: len(policies), Results: policies})
	if err != nil {
		t.Fatalf("encode volume backup policy page: %v", err)
	}
	return string(body)
}

func volumeBackupPolicyDataSourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&VolumeBackupPolicyDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

// backupPolicyLookup returns a data source model with only the lookup
// criteria set.
func backupPolicyLookup(id, name, region, status types.String) VolumeBackupPolicyDataSourceModel {
	return VolumeBackupPolicyDataSourceModel{
		VolumeBackupPolicyModel: VolumeBackupPolicyModel{
			ID: id, Name: name, Status: status, Schedule: types.ObjectNull(volumeBackupPolicyScheduleAttrTypes),
		},
		Region: region,
	}
}

func TestVolumeBackupPolicyDataSourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, ok := NewVolumeBackupPolicyDataSource().(*VolumeBackupPolicyDataSource)
	if !ok {
		t.Fatal("constructor must return *VolumeBackupPolicyDataSource")
	}
	var metadata datasource.MetadataResponse
	d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_volume_backup_policy" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var unconfigured datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{}, &unconfigured)
	if unconfigured.Diagnostics.HasError() || d.client != nil {
		t.Fatalf("nil provider data must be ignored: %v", unconfigured.Diagnostics)
	}
	var wrong datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{ProviderData: "wrong"}, &wrong)
	if !wrong.Diagnostics.HasError() {
		t.Fatal("unexpected provider data must be rejected")
	}
	unused := func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") }
	client, project := volumeTestClient(t, unused), volumeTestProjectClient(t, unused)
	var configured datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{ProviderData: &providerdata.Configured{BlockStorage: client, Project: project, ProjectID: volumeTestProjectID}}, &configured)
	if configured.Diagnostics.HasError() || d.client != client || d.project != project || d.projectID != volumeTestProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestVolumeBackupPolicyDataSourceSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := volumeBackupPolicyDataSourceSchema(t)
	state := tfsdk.State{Schema: s}
	if diags := state.Set(ctx, &VolumeBackupPolicyDataSourceModel{
		VolumeBackupPolicyModel: VolumeBackupPolicyModel{Schedule: types.ObjectNull(volumeBackupPolicyScheduleAttrTypes)},
	}); diags.HasError() {
		t.Fatalf("data source model differs from schema: %v", diags)
	}
	for _, name := range []string{"id", "name", "region", "status"} {
		attribute := s.Attributes[name].(schema.StringAttribute)
		if !attribute.Optional || !attribute.Computed {
			t.Errorf("lookup criterion %s must be optional and computed", name)
		}
	}
	for name, attribute := range s.Attributes {
		switch name {
		case "id", "name", "region", "status":
		default:
			if !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() {
				t.Errorf("result attribute %s must be computed only", name)
			}
		}
	}
	statusValidators := s.Attributes["status"].(schema.StringAttribute).Validators
	if stringValidationErrors(t, statusValidators, "enabled") != 0 || stringValidationErrors(t, statusValidators, "paused") != 1 {
		t.Error("status must accept enabled and disabled only")
	}
	if stringValidationErrors(t, statusValidators, " disabled ") != 0 {
		t.Error("status must ignore outer whitespace")
	}
	if nested := s.Attributes["schedule"].(schema.SingleNestedAttribute); len(nested.Attributes) != len(volumeBackupPolicyScheduleAttrTypes) {
		t.Errorf("schedule must expose every schedule field: %v", nested.Attributes)
	}
}

func TestVolumeBackupPolicyDataSourceCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	}), project: volumeTestProjectClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	}), projectID: volumeTestProjectID}
	null := types.StringNull()
	for _, tt := range []struct {
		name   string
		config VolumeBackupPolicyDataSourceModel
		want   string
	}{
		{"missing", backupPolicyLookup(null, null, null, null), "Missing Volume Backup Policy lookup criteria"},
		{"unknown criteria count as missing", backupPolicyLookup(types.StringUnknown(), null, null, null), "Missing Volume Backup Policy lookup criteria"},
		{"blank name", backupPolicyLookup(null, types.StringValue(" "), null, null), "Invalid Volume Backup Policy lookup criteria"},
		{"blank region", backupPolicyLookup(null, null, types.StringValue(" "), null), "Invalid Volume Backup Policy lookup criteria"},
		{"blank status", backupPolicyLookup(null, null, null, types.StringValue(" ")), "Invalid Volume Backup Policy lookup criteria"},
		{"blank ID", backupPolicyLookup(types.StringValue(" "), null, null, null), "Invalid Volume Backup Policy lookup criteria"},
		{"malformed ID", backupPolicyLookup(types.StringValue("bad"), null, null, null), "Invalid UUID"},
		{"malformed ID with name", backupPolicyLookup(types.StringValue("bad"), types.StringValue("nightly"), null, null), "Invalid UUID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getPolicy(ctx, tt.config)
			if !diags.HasError() || diags[0].Summary() != tt.want {
				t.Fatalf("want %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestVolumeBackupPolicyDataSourceDirectAndFilteredLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleVolumeBackupPolicy(t)
	other := *sampleVolumeBackupPolicy(t)
	other.Id, other.Name = core.UUID{4}, "nightly-2"
	listCalls, getCalls, regionCalls := 0, 0, 0
	regionsPath := "/v2/projects/" + volumeTestProjectID.String() + "/regions/"
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == regionsPath {
			regionCalls++
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		}
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing Project-ID header on %s", req.URL)
		}
		switch req.URL.Path {
		case backupPoliciesPath:
			listCalls++
			query := req.URL.Query()
			if query.Get("name") != "nightly" || query.Get("region_id") != backupPolicyTestRegionID.String() || query.Get("status") != "enabled" {
				t.Errorf("list must pass the trimmed name, the resolved region_id, and status: %s", req.URL.RawQuery)
			}
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyPageJSON(t, *policy, other)), nil
		case backupPoliciesPath + backupPolicyTestID.String() + "/":
			getCalls++
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	null := types.StringNull()

	got, diags := d.getPolicy(ctx, backupPolicyLookup(types.StringValue(" "+backupPolicyTestID.String()+"\n"), null, null, null))
	if diags.HasError() || got == nil || got.Id != backupPolicyTestID || listCalls != 0 || getCalls != 1 || regionCalls != 0 {
		t.Fatalf("direct lookup: %#v, list=%d get=%d region=%d, %v", got, listCalls, getCalls, regionCalls, diags)
	}

	s := volumeBackupPolicyDataSourceSchema(t)
	config := tfsdk.State{Schema: s}
	lookup := backupPolicyLookup(null, types.StringValue(" nightly "), types.StringValue(" vn-central-1 "), types.StringValue(" enabled "))
	if diags := config.Set(ctx, &lookup); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: s}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: config.Raw}}, &response)
	var state VolumeBackupPolicyDataSourceModel
	if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() || listCalls != 1 || regionCalls != 1 ||
		state.ID.ValueString() != backupPolicyTestID.String() || state.Name.ValueString() != " nightly " || state.Region.ValueString() != " vn-central-1 " ||
		state.Status.ValueString() != " enabled " || state.Retention.ValueInt64() != 1 || state.State.ValueString() != "ready" ||
		state.VolumeCount.ValueInt64() != 2 || !state.Description.IsNull() || !state.NextExecution.IsNull() || !state.LastExecution.IsNull() ||
		state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || !state.Schedule.Equal(periodicScheduleObject(2, "day", "2030-01-01T02:00:00Z")) {
		t.Fatalf("data source state: %#v, %v %v", state, response.Diagnostics, diags)
	}

	upper := backupPolicyLookup(types.StringValue(" "+strings.ToUpper(backupPolicyTestID.String())), null, null, null)
	result := upper
	if diags := populateVolumeBackupPolicyDataSourceState(policy, &result); diags.HasError() {
		t.Fatal(diags)
	}
	preserveVolumeBackupPolicyFilters(policy, &upper, &result)
	if result.ID != upper.ID || result.Region.ValueString() != "vn-central-1" {
		t.Fatalf("configured ID spelling must be preserved: %v %v", result.ID, result.Region)
	}
}

// AC15: status is also matched on the client, so a policy with the same name
// and region but another status is skipped even when the API returns it.
func TestVolumeBackupPolicyDataSourceStatusFiltersExactly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enabled, disabled := *sampleVolumeBackupPolicy(t), *sampleVolumeBackupPolicy(t)
	disabled.Id, disabled.Status = core.UUID{4}, blockstoragesdk.VolumeBackupPolicyStatusDisabled
	regionsPath := "/v2/projects/" + volumeTestProjectID.String() + "/regions/"
	page := []blockstoragesdk.VolumeBackupPolicySchema{enabled, disabled}
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == regionsPath {
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		}
		return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyPageJSON(t, page...)), nil
	}
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	null := types.StringNull()
	byNameAndRegion := backupPolicyLookup(null, types.StringValue("nightly"), types.StringValue("vn-central-1"), null)
	if _, diags := d.getPolicy(ctx, byNameAndRegion); !diags.HasError() || !strings.Contains(diags[0].Detail(), "ambiguous") {
		t.Fatalf("name and region match both policies: %v", diags)
	}
	byStatus := backupPolicyLookup(null, types.StringValue("nightly"), types.StringValue("vn-central-1"), types.StringValue("disabled"))
	got, diags := d.getPolicy(ctx, byStatus)
	if diags.HasError() || got == nil || got.Id != disabled.Id {
		t.Fatalf("status disabled must select the disabled policy: %#v %v", got, diags)
	}
	page = []blockstoragesdk.VolumeBackupPolicySchema{disabled}
	if _, diags := d.getPolicy(ctx, backupPolicyLookup(null, types.StringValue("nightly"), types.StringValue("vn-central-1"), types.StringValue("enabled"))); !diags.HasError() || !strings.Contains(diags[0].Detail(), "not found") {
		t.Fatalf("a policy with another status must not be selected: %v", diags)
	}
}

func TestVolumeBackupPolicyDataSourceCardinalityAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	duplicate := *sampleVolumeBackupPolicy(t)
	duplicate.Id = core.UUID{4}
	var page []blockstoragesdk.VolumeBackupPolicySchema
	regionsPath := "/v2/projects/" + volumeTestProjectID.String() + "/regions/"
	roundTrip := func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case regionsPath:
			return backupPolicyRegionsHandler(t, req, "vn-central-1"), nil
		case backupPoliciesPath:
			return testHTTPResponse(req, http.StatusOK, volumeBackupPolicyPageJSON(t, page...)), nil
		}
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	}
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	null := types.StringNull()
	byName := backupPolicyLookup(null, types.StringValue("nightly"), types.StringValue("vn-central-1"), null)
	for _, tt := range []struct {
		name string
		page []blockstoragesdk.VolumeBackupPolicySchema
		want string
	}{
		{"zero matches", nil, "not found"},
		{"multiple matches", []blockstoragesdk.VolumeBackupPolicySchema{*sampleVolumeBackupPolicy(t), duplicate}, "ambiguous"},
	} {
		page = tt.page
		_, diags := d.getPolicy(ctx, byName)
		if !diags.HasError() || diags[0].Summary() != "Unable to resolve volume backup policy" || !strings.Contains(diags[0].Detail(), tt.want) {
			t.Fatalf("%s: want %q, got %v", tt.name, tt.want, diags)
		}
	}
	byID := backupPolicyLookup(types.StringValue(backupPolicyTestID.String()), null, null, null)
	if _, diags := d.getPolicy(ctx, byID); !diags.HasError() || diags[0].Summary() != "Volume backup policy not found" {
		t.Fatalf("missing ID diagnostic: %v", diags)
	}
}

// A match can sit on any page, so the lookup must request every page.
func TestVolumeBackupPolicyDataSourceFollowsPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first := *sampleVolumeBackupPolicy(t)
	first.Id, first.Name = core.UUID{4}, "nightly-other"
	var pages []string
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		page := req.URL.Query().Get("page_number")
		pages = append(pages, page)
		body := blockstoragesdk.PagedVolumeBackupPolicySchema{Count: 2, Results: []blockstoragesdk.VolumeBackupPolicySchema{first}}
		if page == "2" {
			body.Results = []blockstoragesdk.VolumeBackupPolicySchema{*sampleVolumeBackupPolicy(t)}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return testHTTPResponse(req, http.StatusOK, string(encoded)), nil
	}), projectID: volumeTestProjectID}
	null := types.StringNull()
	got, diags := d.getPolicy(ctx, backupPolicyLookup(null, types.StringValue("nightly"), null, null))
	if diags.HasError() || got == nil || got.Id != backupPolicyTestID || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("paginated lookup: %#v, pages %v, %v", got, pages, diags)
	}
}

func TestVolumeBackupPolicyDataSourceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	regionsPath := "/v2/projects/" + volumeTestProjectID.String() + "/regions/"
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == regionsPath {
			return testHTTPResponse(req, http.StatusInternalServerError, `{"detail":"region failure"}`), nil
		}
		return testHTTPResponse(req, http.StatusInternalServerError, `{"detail":"failure"}`), nil
	}
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	null := types.StringNull()
	if _, diags := d.getPolicy(ctx, backupPolicyLookup(types.StringValue(backupPolicyTestID.String()), null, null, null)); !diags.HasError() || diags[0].Summary() != "Error reading volume backup policy" {
		t.Fatalf("get error: %v", diags)
	}
	if _, diags := d.getPolicy(ctx, backupPolicyLookup(null, types.StringValue("nightly"), null, null)); !diags.HasError() || diags[0].Summary() != "Unable to resolve volume backup policy" {
		t.Fatalf("list error: %v", diags)
	}
	if _, diags := d.getPolicy(ctx, backupPolicyLookup(null, null, types.StringValue("vn-central-1"), null)); !diags.HasError() || diags[0].Summary() != "Unable to resolve region" {
		t.Fatalf("region error: %v", diags)
	}
}

func TestVolumeBackupPolicyDataSourceReadRejectsUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body := `{"id":"` + backupPolicyTestID.String() + `","name":"weekly","retention":1,"status":"enabled","state":"ready","volume_count":0,` +
		`"schedule":{"execution_strategy":"days_of_week","days_of_week":["mon"],"time":"02:00:00"},` +
		`"region":{"id":"` + backupPolicyTestRegionID.String() + `","name":"vn-central-1","description":""},` +
		`"project":{"id":"` + volumeTestProjectID.String() + `","name":"project","slug":"p"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	d := &VolumeBackupPolicyDataSource{client: volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, body), nil
	}), projectID: volumeTestProjectID}
	s := volumeBackupPolicyDataSourceSchema(t)
	config := tfsdk.State{Schema: s}
	lookup := backupPolicyLookup(types.StringValue(backupPolicyTestID.String()), types.StringNull(), types.StringNull(), types.StringNull())
	if diags := config.Set(ctx, &lookup); diags.HasError() {
		t.Fatal(diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: s}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: config.Raw}}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "days_of_week") || !response.State.Raw.IsNull() {
		t.Fatalf("an unsupported schedule must fail and write no state: %v", response.Diagnostics)
	}
}
