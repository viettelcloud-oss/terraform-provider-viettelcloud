package blockstorage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func snapshotPolicyDataSourceModel(id, name, region, status types.String) VolumeSnapshotPolicyDataSourceModel {
	return VolumeSnapshotPolicyDataSourceModel{
		VolumeSnapshotPolicyModel: VolumeSnapshotPolicyModel{
			ID: id, Name: name, Status: status, Schedule: types.ObjectNull(snapshotPolicyScheduleTypes),
		},
		Region: region,
	}
}

func snapshotPolicyPageJSON(t *testing.T, policies ...blockstoragesdk.SnapshotPolicySchema) string {
	t.Helper()
	body, err := json.Marshal(blockstoragesdk.PagedSnapshotPolicySchema{Count: len(policies), Results: policies})
	if err != nil {
		t.Fatalf("encode snapshot policy page: %v", err)
	}
	return string(body)
}

func TestVolumeSnapshotPolicyDataSourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, ok := NewVolumeSnapshotPolicyDataSource().(*VolumeSnapshotPolicyDataSource)
	if !ok {
		t.Fatal("constructor must return *VolumeSnapshotPolicyDataSource")
	}
	var metadata datasource.MetadataResponse
	d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_volume_snapshot_policy" {
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
	client := volumeTestClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	project := volumeTestProjectClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	var configured datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{ProviderData: &providerdata.Configured{BlockStorage: client, Project: project, ProjectID: volumeTestProjectID}}, &configured)
	if configured.Diagnostics.HasError() || d.client != client || d.project != project || d.projectID != volumeTestProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

// AC12, AC13, and AC16: a lookup by ID must read the policy directly. A lookup
// by name, region, and status must send the trimmed criteria to the API, and
// state must keep the configured spelling of each criterion.
func TestVolumeSnapshotPolicyDataSourceDirectAndFilteredLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleSnapshotPolicy(t)
	disabled := *sampleSnapshotPolicy(t)
	disabled.Id, disabled.Status = core.UUID{4}, blockstoragesdk.SnapshotPolicyStatusDisabled
	listCalls, getCalls := 0, 0
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotPolicyRegionsPath() {
			return snapshotPolicyRegionsPage(t, req), nil
		}
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing Project-ID header on %s", req.URL)
		}
		switch req.URL.Path {
		case snapshotPoliciesPath:
			listCalls++
			query := req.URL.Query()
			if query.Get("name") != "nightly" || query.Get("region_id") != snapshotPolicyTestRegionID.String() || query.Get("status") != "enabled" || query.Has("execution_strategy") {
				t.Errorf("list must pass the trimmed name, the region ID, and the status only: %s", req.URL.RawQuery)
			}
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyPageJSON(t, *policy, disabled)), nil
		case snapshotPoliciesPath + snapshotPolicyTestID.String() + "/":
			getCalls++
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}

	direct := snapshotPolicyDataSourceModel(types.StringValue(snapshotPolicyTestID.String()), types.StringNull(), types.StringNull(), types.StringNull())
	got, diags := d.getPolicy(ctx, direct)
	if diags.HasError() || got == nil || got.Id != snapshotPolicyTestID || listCalls != 0 || getCalls != 1 {
		t.Fatalf("direct lookup: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}

	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	config := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := config.Set(ctx, snapshotPolicyDataSourceModel(types.StringNull(), types.StringValue(" nightly "), types.StringValue(" vn-central-1 "), types.StringValue(" enabled "))); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: config.Raw}}, &response)
	var state VolumeSnapshotPolicyDataSourceModel
	if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() || listCalls != 1 ||
		state.ID.ValueString() != snapshotPolicyTestID.String() || state.Name.ValueString() != " nightly " || state.Region.ValueString() != " vn-central-1 " ||
		state.Status.ValueString() != " enabled " || state.Retention.ValueInt64() != 1 || !state.Description.IsNull() ||
		!state.Schedule.Equal(snapshotPolicyScheduleValue([]string{"mon", "fri"}, "02:00")) || state.State.ValueString() != "ready" ||
		state.VolumeCount.ValueInt64() != 2 || !state.LastExecution.IsNull() || state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" ||
		state.UpdatedAt.ValueString() != "2026-01-02T00:00:00Z" {
		t.Fatalf("data source state: %#v, %v %v", state, response.Diagnostics, diags)
	}

	upper := snapshotPolicyDataSourceModel(types.StringValue(strings.ToUpper(snapshotPolicyTestID.String())), types.StringNull(), types.StringNull(), types.StringNull())
	result := upper
	if diags := populateVolumeSnapshotPolicyDataSourceState(policy, &result); diags.HasError() {
		t.Fatal(diags)
	}
	preserveVolumeSnapshotPolicyFilters(policy, &upper, &result)
	if result.ID != upper.ID || result.Region.ValueString() != "vn-central-1" {
		t.Fatalf("configured ID spelling must be preserved: %v %v", result.ID, result.Region)
	}
}

// A padded ID must select the policy by the trimmed ID, both alone and with a
// name, and state must keep the configured spelling of each criterion.
func TestVolumeSnapshotPolicyDataSourcePaddedIDKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleSnapshotPolicy(t)
	roundTrip := func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case snapshotPoliciesPath:
			if req.URL.Query().Get("name") != "nightly" {
				t.Errorf("list must pass the trimmed name: %s", req.URL.RawQuery)
			}
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyPageJSON(t, *policy)), nil
		case snapshotPoliciesPath + snapshotPolicyTestID.String() + "/":
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), projectID: volumeTestProjectID}
	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	paddedID := types.StringValue("  " + snapshotPolicyTestID.String() + "\n")
	for _, tt := range []struct {
		name   string
		config VolumeSnapshotPolicyDataSourceModel
	}{
		{"id only", snapshotPolicyDataSourceModel(paddedID, types.StringNull(), types.StringNull(), types.StringNull())},
		{"id and name", snapshotPolicyDataSourceModel(paddedID, types.StringValue(" nightly "), types.StringNull(), types.StringNull())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := tfsdk.State{Schema: schemaResponse.Schema}
			if diags := config.Set(ctx, tt.config); diags.HasError() {
				t.Fatalf("config: %v", diags)
			}
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: config.Raw}}, &response)
			var state VolumeSnapshotPolicyDataSourceModel
			if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() ||
				state.ID != paddedID || (!tt.config.Name.IsNull() && state.Name != tt.config.Name) {
				t.Fatalf("state: id=%q name=%q, %v %v", state.ID.ValueString(), state.Name.ValueString(), response.Diagnostics, diags)
			}
		})
	}
}

// AC16: two policies share a name and a region, so only the status can pick
// one.
func TestVolumeSnapshotPolicyDataSourceStatusSelectsPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enabled := *sampleSnapshotPolicy(t)
	disabled := *sampleSnapshotPolicy(t)
	disabled.Id, disabled.Status = core.UUID{4}, blockstoragesdk.SnapshotPolicyStatusDisabled
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotPolicyRegionsPath() {
			return snapshotPolicyRegionsPage(t, req), nil
		}
		return testHTTPResponse(req, http.StatusOK, snapshotPolicyPageJSON(t, enabled, disabled)), nil
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	name, region := types.StringValue("nightly"), types.StringValue("vn-central-1")

	if _, diags := d.getPolicy(ctx, snapshotPolicyDataSourceModel(types.StringNull(), name, region, types.StringNull())); !diags.HasError() || !strings.Contains(diags[0].Detail(), "ambiguous") {
		t.Fatalf("name and region alone must be ambiguous here: %v", diags)
	}
	got, diags := d.getPolicy(ctx, snapshotPolicyDataSourceModel(types.StringNull(), name, region, types.StringValue("disabled")))
	if diags.HasError() || got == nil || got.Id != (core.UUID{4}) {
		t.Fatalf("status disabled: %#v %v", got, diags)
	}
	got, diags = d.getPolicy(ctx, snapshotPolicyDataSourceModel(types.StringNull(), name, region, types.StringValue("enabled")))
	if diags.HasError() || got == nil || got.Id != snapshotPolicyTestID {
		t.Fatalf("status enabled: %#v %v", got, diags)
	}
}

// AC14 and AC15: invalid criteria must fail before any API request.
func TestVolumeSnapshotPolicyDataSourceCriteriaErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	unused := func(req *http.Request) (*http.Response, error) {
		t.Errorf("an invalid lookup must not call the API: %s", req.URL)
		return nil, errors.New("unexpected request")
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, unused), project: volumeTestProjectClient(t, unused), projectID: volumeTestProjectID}
	null, blank := types.StringNull(), types.StringValue("  ")
	for _, tt := range []struct {
		name    string
		config  VolumeSnapshotPolicyDataSourceModel
		summary string
	}{
		{"no criteria", snapshotPolicyDataSourceModel(null, null, null, null), "Missing Volume Snapshot Policy lookup criteria"},
		{"blank name", snapshotPolicyDataSourceModel(null, blank, null, null), "Invalid Volume Snapshot Policy lookup criteria"},
		{"blank region", snapshotPolicyDataSourceModel(null, null, blank, null), "Invalid Volume Snapshot Policy lookup criteria"},
		{"blank id", snapshotPolicyDataSourceModel(blank, null, null, null), "Invalid Volume Snapshot Policy lookup criteria"},
		{"invalid status", snapshotPolicyDataSourceModel(null, null, null, types.StringValue("paused")), "Invalid Volume Snapshot Policy lookup criteria"},
		{"malformed ID", snapshotPolicyDataSourceModel(types.StringValue("not-a-uuid"), null, null, null), ""},
		{"malformed ID with a name", snapshotPolicyDataSourceModel(types.StringValue("not-a-uuid"), types.StringValue("nightly"), null, null), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getPolicy(ctx, tt.config)
			if !diags.HasError() || (tt.summary != "" && diags.Errors()[0].Summary() != tt.summary) {
				t.Fatalf("want %q, got %v", tt.summary, diags)
			}
		})
	}
}

// AC14: the lookup must never pick one of several matches, and no match is an
// error.
func TestVolumeSnapshotPolicyDataSourceCardinalityAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	duplicate := *sampleSnapshotPolicy(t)
	duplicate.Id = core.UUID{4}
	var page []blockstoragesdk.SnapshotPolicySchema
	roundTrip := func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case snapshotPolicyRegionsPath():
			return snapshotPolicyRegionsPage(t, req), nil
		case snapshotPoliciesPath:
			return testHTTPResponse(req, http.StatusOK, snapshotPolicyPageJSON(t, page...)), nil
		}
		return testHTTPResponse(req, http.StatusNotFound, `{"errors":[{"code":"not_found","detail":"missing"}]}`), nil
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	byName := snapshotPolicyDataSourceModel(types.StringNull(), types.StringValue("nightly"), types.StringValue("vn-central-1"), types.StringNull())
	for _, tt := range []struct {
		name string
		page []blockstoragesdk.SnapshotPolicySchema
		want string
	}{
		{"zero matches", nil, "not found"},
		{"multiple matches", []blockstoragesdk.SnapshotPolicySchema{*sampleSnapshotPolicy(t), duplicate}, "ambiguous"},
	} {
		page = tt.page
		_, diags := d.getPolicy(ctx, byName)
		if !diags.HasError() || diags[0].Summary() != "Unable to resolve volume snapshot policy" || !strings.Contains(diags[0].Detail(), tt.want) {
			t.Fatalf("%s: want %q, got %v", tt.name, tt.want, diags)
		}
	}
	byID := snapshotPolicyDataSourceModel(types.StringValue(snapshotPolicyTestID.String()), types.StringNull(), types.StringNull(), types.StringNull())
	if _, diags := d.getPolicy(ctx, byID); !diags.HasError() || diags[0].Summary() != "Volume snapshot policy not found" {
		t.Fatalf("missing ID diagnostic: %v", diags)
	}
}

func TestVolumeSnapshotPolicyDataSourceReportsRegionAndAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	regionEmpty := true
	roundTrip := func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotPolicyRegionsPath() {
			if regionEmpty {
				return testHTTPResponse(req, http.StatusOK, `{"count":0,"results":[]}`), nil
			}
			return snapshotPolicyRegionsPage(t, req), nil
		}
		return testHTTPResponse(req, http.StatusInternalServerError, `{"errors":[{"code":"error","detail":"failure"}]}`), nil
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), project: volumeTestProjectClient(t, roundTrip), projectID: volumeTestProjectID}
	byRegion := snapshotPolicyDataSourceModel(types.StringNull(), types.StringNull(), types.StringValue("vn-central-1"), types.StringNull())
	if _, diags := d.getPolicy(ctx, byRegion); !diags.HasError() || diags[0].Summary() != "Unable to resolve region" {
		t.Fatalf("an unknown region must fail: %v", diags)
	}
	regionEmpty = false
	if _, diags := d.getPolicy(ctx, byRegion); !diags.HasError() || diags[0].Summary() != "Unable to resolve volume snapshot policy" {
		t.Fatalf("list error: %v", diags)
	}
	byID := snapshotPolicyDataSourceModel(types.StringValue(snapshotPolicyTestID.String()), types.StringNull(), types.StringNull(), types.StringNull())
	if _, diags := d.getPolicy(ctx, byID); !diags.HasError() || diags[0].Summary() != "Error reading volume snapshot policy" {
		t.Fatalf("get error: %v", diags)
	}
}

// The only match is on the second page, so the lookup must request every page.
func TestVolumeSnapshotPolicyDataSourceFollowsPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first := *sampleSnapshotPolicy(t)
	first.Id, first.Name = core.UUID{4}, "nightly-other"
	var pages []string
	roundTrip := func(req *http.Request) (*http.Response, error) {
		page := req.URL.Query().Get("page_number")
		pages = append(pages, page)
		body := blockstoragesdk.PagedSnapshotPolicySchema{Count: 2, Results: []blockstoragesdk.SnapshotPolicySchema{first}}
		if page == "2" {
			body.Results = []blockstoragesdk.SnapshotPolicySchema{*sampleSnapshotPolicy(t)}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return testHTTPResponse(req, http.StatusOK, string(encoded)), nil
	}
	d := &VolumeSnapshotPolicyDataSource{client: volumeTestClient(t, roundTrip), projectID: volumeTestProjectID}
	got, diags := d.getPolicy(ctx, snapshotPolicyDataSourceModel(types.StringNull(), types.StringValue("nightly"), types.StringNull(), types.StringNull()))
	if diags.HasError() || got == nil || got.Id != snapshotPolicyTestID || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("paginated lookup: %#v, pages %v, %v", got, pages, diags)
	}
}

// AC11, data source half: the schema has no place for other schedule types.
func TestVolumeSnapshotPolicyDataSourceRejectsUnsupportedSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleSnapshotPolicyWithSchedule(t, `{"execution_strategy":"days_of_month","days_of_month":[1],"time":"02:00:00"}`)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusOK, snapshotPolicyJSON(t, policy)), nil
	})
	d := &VolumeSnapshotPolicyDataSource{client: client, projectID: volumeTestProjectID}
	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	config := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := config.Set(ctx, snapshotPolicyDataSourceModel(types.StringValue(snapshotPolicyTestID.String()), types.StringNull(), types.StringNull(), types.StringNull())); diags.HasError() {
		t.Fatal(diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: config.Raw}}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Unsupported snapshot policy schedule" ||
		!strings.Contains(response.Diagnostics.Errors()[0].Detail(), snapshotPolicyTestID.String()) || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "days_of_month") ||
		!response.State.Raw.IsNull() {
		t.Fatalf("read must name the ID and the schedule type and write no state: %v", response.Diagnostics)
	}
}
