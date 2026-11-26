package filestorage

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"
)

func TestFilesystemSnapshotDataSourceSchemaAndCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	(&FilesystemSnapshotDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(ctx, &FilesystemSnapshotDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model differs from schema: %v", diags)
	}
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request %s %s", req.Method, req.URL)
		return nil, errors.New("unexpected request")
	})
	d := &FilesystemSnapshotDataSource{client: client, projectID: testProjectID}
	for _, tt := range []struct {
		name   string
		config FilesystemSnapshotDataSourceModel
		want   string
	}{
		{"missing", FilesystemSnapshotDataSourceModel{}, "Missing FilesystemSnapshot lookup criteria"},
		{"blank name", FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue(" ")}}, "Invalid FilesystemSnapshot lookup criteria"},
		{"blank filesystem ID", FilesystemSnapshotDataSourceModel{FilesystemID: types.StringValue(" ")}, "Invalid FilesystemSnapshot lookup criteria"},
		{"invalid ID", FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{ID: types.StringValue("bad")}}, "Invalid UUID"},
		{"invalid filesystem ID", FilesystemSnapshotDataSourceModel{FilesystemID: types.StringValue("bad")}, "Invalid UUID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getSnapshot(ctx, tt.config)
			if !diags.HasError() || diags[0].Summary() != tt.want {
				t.Fatalf("want %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestFilesystemSnapshotDataSourceDirectAndFilteredLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	snapshot := sampleFilesystemSnapshot()
	other := *sampleFilesystemSnapshot()
	other.Id, other.Name = core.UUID{4}, "other"
	listCalls, getCalls := 0, 0
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Project-ID") != testProjectID.String() {
			t.Errorf("missing Project-ID header on %s", req.URL)
		}
		switch req.URL.Path {
		case snapshotsPath:
			listCalls++
			if query := req.URL.Query(); query.Get("name") != "snapshot" || query.Get("filesystem_id") != testFilesystemID.String() {
				t.Errorf("list must pass the trimmed name and filesystem_id: %s", req.URL.RawQuery)
			}
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPageJSON(t, *snapshot, other)), nil
		case snapshotsPath + testSnapshotID.String() + "/":
			getCalls++
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotJSON(t, snapshot)), nil
		default:
			t.Errorf("unexpected request %s", req.URL)
			return nil, errors.New("unexpected request")
		}
	})
	d := &FilesystemSnapshotDataSource{client: client, projectID: testProjectID}

	direct := FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{ID: types.StringValue(testSnapshotID.String())}}
	got, diags := d.getSnapshot(ctx, direct)
	if diags.HasError() || got == nil || got.Id != testSnapshotID || listCalls != 0 || getCalls != 1 {
		t.Fatalf("direct lookup: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}

	filtered := FilesystemSnapshotDataSourceModel{
		FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue(" snapshot ")},
		FilesystemID:            types.StringValue(testFilesystemID.String()),
	}
	got, diags = d.getSnapshot(ctx, filtered)
	if diags.HasError() || got == nil || got.Id != testSnapshotID || listCalls != 1 || getCalls != 1 {
		t.Fatalf("filtered lookup must match the name exactly: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}

	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	upper := strings.ToUpper(testSnapshotID.String())
	if diags := plan.Set(ctx, &FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{ID: types.StringValue(upper)}}); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}}, &response)
	var state FilesystemSnapshotDataSourceModel
	if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() ||
		state.ID.ValueString() != upper || state.Name.ValueString() != "snapshot" || state.FilesystemID.ValueString() != testFilesystemID.String() ||
		state.FilesystemName.ValueString() != "data" || state.Size.ValueInt64() != 50 || state.Status.ValueString() != "available" ||
		!state.Description.IsNull() || state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Fatalf("data source state: %#v, %v %v", state, response.Diagnostics, diags)
	}
}

func TestFilesystemSnapshotDataSourceCardinalityAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	duplicate := *sampleFilesystemSnapshot()
	duplicate.Id = core.UUID{4}
	var page []filestoragesdk.FilesystemSnapshotSchema
	client := testClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == snapshotsPath {
			return testHTTPResponse(req, http.StatusOK, filesystemSnapshotPageJSON(t, page...)), nil
		}
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	d := &FilesystemSnapshotDataSource{client: client, projectID: testProjectID}
	byName := FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{Name: types.StringValue("snapshot")}}
	for _, tt := range []struct {
		name string
		page []filestoragesdk.FilesystemSnapshotSchema
		want string
	}{
		{"zero matches", nil, "not found"},
		{"multiple matches", []filestoragesdk.FilesystemSnapshotSchema{*sampleFilesystemSnapshot(), duplicate}, "ambiguous"},
	} {
		page = tt.page
		_, diags := d.getSnapshot(ctx, byName)
		if !diags.HasError() || diags[0].Summary() != "Unable to resolve filesystem snapshot" || !strings.Contains(diags[0].Detail(), tt.want) {
			t.Fatalf("%s: want %q, got %v", tt.name, tt.want, diags)
		}
	}
	byID := FilesystemSnapshotDataSourceModel{FilesystemSnapshotModel: FilesystemSnapshotModel{ID: types.StringValue(testSnapshotID.String())}}
	if _, diags := d.getSnapshot(ctx, byID); !diags.HasError() || diags[0].Summary() != "Filesystem snapshot not found" {
		t.Fatalf("missing ID diagnostic: %v", diags)
	}
}
