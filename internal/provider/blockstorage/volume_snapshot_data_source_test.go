package blockstorage

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
)

func TestVolumeSnapshotDataSourceSchemaAndCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	(&VolumeSnapshotDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	state := tfsdk.State{Schema: schemaResponse.Schema}
	if diags := state.Set(ctx, &VolumeSnapshotDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model differs from schema: %v", diags)
	}
	d := &VolumeSnapshotDataSource{}
	for _, tt := range []struct {
		name   string
		config VolumeSnapshotDataSourceModel
		want   string
	}{
		{"missing", VolumeSnapshotDataSourceModel{}, "Missing volume snapshot lookup criteria"},
		{"blank", VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{Name: types.StringValue(" ")}}, "Invalid volume snapshot lookup criteria"},
		{"invalid ID", VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{ID: types.StringValue("bad")}}, "Invalid UUID"},
		{"invalid volume ID", VolumeSnapshotDataSourceModel{VolumeID: types.StringValue("bad")}, "Invalid UUID"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getSnapshot(ctx, tt.config)
			if !diags.HasError() || diags[0].Summary() != tt.want {
				t.Fatalf("want %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestVolumeSnapshotDataSourceDirectAndFilteredLookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	snapshot := sampleVolumeSnapshot()
	listCalls, getCalls := 0, 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Project-ID") != volumeTestProjectID.String() {
			t.Errorf("missing project header: %s", req.URL)
		}
		switch req.URL.Path {
		case "/v2/block-storage/volume-snapshots/":
			listCalls++
			if req.URL.Query().Get("name") != "snapshot" || req.URL.Query().Get("zone") != "zone-a" || req.URL.Query().Get("volume_id") != volumeTestID.String() {
				t.Errorf("unexpected list filters: %s", req.URL.RawQuery)
			}
			body, _ := json.Marshal(blockstoragesdk.PagedVolumeSnapshotSchema{Count: 1, Results: []blockstoragesdk.VolumeSnapshotSchema{*snapshot}})
			return testHTTPResponse(req, http.StatusOK, string(body)), nil
		case "/v2/block-storage/volume-snapshots/" + snapshot.Id.String() + "/":
			getCalls++
			return testHTTPResponse(req, http.StatusOK, volumeSnapshotJSON(snapshot)), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})
	d := &VolumeSnapshotDataSource{client: client, projectID: volumeTestProjectID}
	direct := VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{ID: types.StringValue(snapshot.Id.String())}}
	got, diags := d.getSnapshot(ctx, direct)
	if diags.HasError() || got == nil || got.Id != snapshot.Id || listCalls != 0 || getCalls != 1 {
		t.Fatalf("direct lookup: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}
	filtered := VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{
		Name: types.StringValue(" snapshot "), Zone: types.StringValue(" zone-a "),
	}, VolumeID: types.StringValue(volumeTestID.String())}
	got, diags = d.getSnapshot(ctx, filtered)
	if diags.HasError() || got == nil || got.Id != snapshot.Id || listCalls != 1 || getCalls != 1 {
		t.Fatalf("filtered lookup: %#v, list=%d get=%d, %v", got, listCalls, getCalls, diags)
	}
	var schemaResponse datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	plan := tfsdk.Plan{Schema: schemaResponse.Schema}
	if diags := plan.Set(ctx, &VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{ID: types.StringValue(strings.ToUpper(snapshot.Id.String()))}}); diags.HasError() {
		t.Fatalf("config: %v", diags)
	}
	config := tfsdk.Config{Schema: schemaResponse.Schema, Raw: plan.Raw}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	var state VolumeSnapshotDataSourceModel
	if diags := response.State.Get(ctx, &state); response.Diagnostics.HasError() || diags.HasError() ||
		state.ID.ValueString() != strings.ToUpper(snapshot.Id.String()) || state.VolumeID.ValueString() != volumeTestID.String() ||
		state.Size.ValueInt64() != 30 || state.Status.ValueString() != "available" {
		t.Fatalf("data source state: %#v, %v %v", state, response.Diagnostics, diags)
	}
}

func TestVolumeSnapshotDataSourceMissingIDDiagnostic(t *testing.T) {
	t.Parallel()
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	d := &VolumeSnapshotDataSource{client: client, projectID: core.UUID{9}}
	_, diags := d.getSnapshot(context.Background(), VolumeSnapshotDataSourceModel{VolumeSnapshotModel: VolumeSnapshotModel{ID: types.StringValue((core.UUID{3}).String())}})
	if !diags.HasError() || diags[0].Summary() != "Volume snapshot not found" {
		t.Fatalf("missing ID diagnostic: %v", diags)
	}
}
