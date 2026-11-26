package filestorage

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
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func TestFilesystemSnapshotPolicyDataSourceConstructorMetadataConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, ok := NewFilesystemSnapshotPolicyDataSource().(*FilesystemSnapshotPolicyDataSource)
	if !ok {
		t.Fatal("constructor must return *FilesystemSnapshotPolicyDataSource")
	}
	var metadata datasource.MetadataResponse
	d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_fs_filesystem_snapshot_policy" {
		t.Fatalf("type name = %q", metadata.TypeName)
	}
	var wrong datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{ProviderData: "wrong"}, &wrong)
	if !wrong.Diagnostics.HasError() {
		t.Fatal("unexpected provider data must be rejected")
	}
	client := testClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("unused") })
	var configured datasource.ConfigureResponse
	d.Configure(ctx, datasource.ConfigureRequest{ProviderData: &providerdata.Configured{FileStorage: client, ProjectID: testProjectID}}, &configured)
	if configured.Diagnostics.HasError() || d.client != client || d.projectID != testProjectID {
		t.Fatalf("configure: %v", configured.Diagnostics)
	}
}

func TestFilesystemSnapshotPolicyDataSourceSchemaAndCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	(&FilesystemSnapshotPolicyDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(ctx, &FilesystemSnapshotPolicyDataSourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{DaysOfWeek: types.ListNull(types.StringType)},
	}); diags.HasError() {
		t.Fatalf("data source model differs from schema: %v", diags)
	}
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	})
	d := &FilesystemSnapshotPolicyDataSource{client: client, projectID: testProjectID}
	for _, tt := range []struct {
		name   string
		config FilesystemSnapshotPolicyDataSourceModel
		want   string
	}{
		{"missing", FilesystemSnapshotPolicyDataSourceModel{}, "Missing FilesystemSnapshotPolicy lookup criteria"},
		{"blank name", FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{Name: types.StringValue(" ")}}, "Invalid FilesystemSnapshotPolicy lookup criteria"},
		{"blank region", FilesystemSnapshotPolicyDataSourceModel{Region: types.StringValue(" ")}, "Invalid FilesystemSnapshotPolicy lookup criteria"},
		{"invalid ID", FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue("bad")}}, "Invalid UUID"},
		{"invalid ID with name", FilesystemSnapshotPolicyDataSourceModel{
			FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue("bad"), Name: types.StringValue("nightly")},
		}, "Invalid UUID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getPolicy(ctx, tt.config)
			if !diags.HasError() || diags[0].Summary() != tt.want {
				t.Fatalf("want %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestFilesystemSnapshotPolicyDataSourceDirectAndFilteredLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	policy := sampleFilesystemSnapshotPolicy()
	other := *sampleFilesystemSnapshotPolicy()
	other.Id, other.Name = core.UUID{4}, "nightly-2"
	listCalls, getCalls := 0, 0
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Project-ID") != testProjectID.String() {
			t.Errorf("missing Project-ID header on %s", req.URL)
		}
		switch req.URL.Path {
		case policiesPath:
			listCalls++
			if query := req.URL.Query(); query.Get("name") != "nightly" || query.Get("region_name") != "vn-central-1" {
				t.Errorf("list must pass the trimmed name and region_name: %s", req.URL.RawQuery)
			}
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyPageJSON(t, *policy, other)), nil
		case policiesPath + testPolicyID.String() + "/":
			getCalls++
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyJSON(t, policy)), nil
		default:
			t.Errorf("unexpected request %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	d := &FilesystemSnapshotPolicyDataSource{client: client, projectID: testProjectID}

	direct := FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue(testPolicyID.String())}}
	got, diags := d.getPolicy(ctx, direct)
	if diags.HasError() || got == nil || got.Id != testPolicyID || listCalls != 0 || getCalls != 1 {
		t.Fatalf("direct lookup: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}

	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	config := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := config.Set(ctx, &FilesystemSnapshotPolicyDataSourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{Name: types.StringValue(" nightly "), DaysOfWeek: types.ListNull(types.StringType)},
		Region:                        types.StringValue(" vn-central-1 "),
	}); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: config.Raw}}, &response)
	var state FilesystemSnapshotPolicyDataSourceModel
	if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() || listCalls != 1 ||
		state.ID.ValueString() != testPolicyID.String() || state.Name.ValueString() != " nightly " || state.Region.ValueString() != " vn-central-1 " ||
		state.RegionID.ValueString() != testRegionID.String() || state.RegionName.ValueString() != "vn-central-1" ||
		!state.DaysOfWeek.Equal(dayList("mon", "fri")) || state.Time.ValueString() != "02:00" || state.Retention.ValueInt64() != 1 ||
		state.Status.ValueString() != "enabled" || state.State.ValueString() != "ready" || state.FilesystemCount.ValueInt64() != 2 ||
		!state.Description.IsNull() || state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Fatalf("data source state: %#v, %v %v", state, response.Diagnostics, diags)
	}

	upper := FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue(strings.ToUpper(testPolicyID.String()))}}
	result := upper
	populateFilesystemSnapshotPolicyDataSourceState(policy, &result)
	preserveFilesystemSnapshotPolicyFilters(policy, &upper, &result)
	if result.ID != upper.ID || result.Region.ValueString() != "vn-central-1" {
		t.Fatalf("configured ID spelling must be preserved: %v %v", result.ID, result.Region)
	}
}

func TestFilesystemSnapshotPolicyDataSourceCardinalityAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	duplicate := *sampleFilesystemSnapshotPolicy()
	duplicate.Id = core.UUID{4}
	var page []filestoragesdk.FilesystemSnapshotPolicySchema
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == policiesPath {
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPolicyPageJSON(t, page...)), nil
		}
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	d := &FilesystemSnapshotPolicyDataSource{client: client, projectID: testProjectID}
	byName := FilesystemSnapshotPolicyDataSourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{Name: types.StringValue("nightly")},
		Region:                        types.StringValue("vn-central-1"),
	}
	for _, tt := range []struct {
		name string
		page []filestoragesdk.FilesystemSnapshotPolicySchema
		want string
	}{
		{"zero matches", nil, "not found"},
		{"multiple matches", []filestoragesdk.FilesystemSnapshotPolicySchema{*sampleFilesystemSnapshotPolicy(), duplicate}, "ambiguous"},
	} {
		page = tt.page
		_, diags := d.getPolicy(ctx, byName)
		if !diags.HasError() || diags[0].Summary() != "Unable to resolve filesystem snapshot policy" || !strings.Contains(diags[0].Detail(), tt.want) {
			t.Fatalf("%s: want %q, got %v", tt.name, tt.want, diags)
		}
	}
	byID := FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue(testPolicyID.String())}}
	if _, diags := d.getPolicy(ctx, byID); !diags.HasError() || diags[0].Summary() != "Filesystem snapshot policy not found" {
		t.Fatalf("missing ID diagnostic: %v", diags)
	}
}

// TestFilesystemSnapshotPolicyDataSourceFollowsPagination puts the only match
// on the second page, so the lookup must request every page.
func TestFilesystemSnapshotPolicyDataSourceFollowsPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	first := *sampleFilesystemSnapshotPolicy()
	first.Id, first.Name = core.UUID{4}, "nightly-other"
	var pages []string
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		page := req.URL.Query().Get("page_number")
		pages = append(pages, page)
		body := filestoragesdk.PagedFilesystemSnapshotPolicySchema{Count: 2, Results: []filestoragesdk.FilesystemSnapshotPolicySchema{first}}
		if page == "2" {
			body.Results = []filestoragesdk.FilesystemSnapshotPolicySchema{*sampleFilesystemSnapshotPolicy()}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return testHTTPResponse(req, http.StatusOK, string(encoded)), nil
	})
	d := &FilesystemSnapshotPolicyDataSource{client: client, projectID: testProjectID}
	got, diags := d.getPolicy(ctx, FilesystemSnapshotPolicyDataSourceModel{
		FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{Name: types.StringValue("nightly")},
	})
	if diags.HasError() || got == nil || got.Id != testPolicyID || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("paginated lookup: %#v, pages %v, %v", got, pages, diags)
	}
}

func TestFilesystemSnapshotPolicyDataSourceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusInternalServerError, `{"detail":"failure"}`), nil
	})
	d := &FilesystemSnapshotPolicyDataSource{client: client, projectID: testProjectID}
	byID := FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{ID: types.StringValue(testPolicyID.String())}}
	if _, diags := d.getPolicy(ctx, byID); !diags.HasError() || diags[0].Summary() != "Error reading filesystem snapshot policy" {
		t.Fatalf("get error: %v", diags)
	}
	byName := FilesystemSnapshotPolicyDataSourceModel{FilesystemSnapshotPolicyModel: FilesystemSnapshotPolicyModel{Name: types.StringValue("nightly")}}
	if _, diags := d.getPolicy(ctx, byName); !diags.HasError() || diags[0].Summary() != "Unable to resolve filesystem snapshot policy" {
		t.Fatalf("list error: %v", diags)
	}
}
