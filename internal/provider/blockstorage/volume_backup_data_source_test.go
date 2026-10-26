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

func TestVolumeBackupDataSourceModelMatchesSchema(t *testing.T) {
	var response datasource.SchemaResponse
	(&VolumeBackupDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	if diags := state.Set(context.Background(), &VolumeBackupDataSourceModel{}); diags.HasError() {
		t.Fatalf("model/schema mismatch: %v", diags)
	}
}

func TestVolumeBackupFilter(t *testing.T) {
	filter, direct, diags := volumeBackupFilter(VolumeBackupDataSourceModel{})
	if !diags.HasError() || direct || filter.ID != nil {
		t.Fatal("missing criteria must fail")
	}
	for _, config := range []VolumeBackupDataSourceModel{
		{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("   ")}},
		{VolumeBackupModel: VolumeBackupModel{ID: types.StringValue("invalid")}},
		{VolumeBackupModel: VolumeBackupModel{Size: types.Int64Value(0)}},
	} {
		_, _, diags = volumeBackupFilter(config)
		if !diags.HasError() {
			t.Fatalf("invalid criteria passed: %#v", config)
		}
	}
	id := testVolumeBackup().Id.String()
	filter, direct, diags = volumeBackupFilter(VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{ID: types.StringValue(id)}})
	if diags.HasError() || !direct || filter.ID == nil {
		t.Fatalf("direct ID failed: %v", diags)
	}
	filter, direct, diags = volumeBackupFilter(VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue("backup"), Region: types.StringValue("region")}})
	if diags.HasError() || direct || filter.Name == nil || filter.Region == nil {
		t.Fatalf("combined criteria failed: %v", diags)
	}
}

func TestPreserveVolumeBackupConfiguredValues(t *testing.T) {
	backup := testVolumeBackup()
	config := VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue(" backup "), Region: types.StringValue(" region ")}}
	state := VolumeBackupDataSourceModel{}
	populateVolumeBackupDataSourceState(backup, &state)
	preserveVolumeBackupConfiguredValues(backup, config, &state)
	if !state.Name.Equal(config.Name) || !state.Region.Equal(config.Region) {
		t.Fatalf("normalized filters changed: %#v", state)
	}
}

func TestVolumeBackupDataSourceDirectAndFilteredReads(t *testing.T) {
	backup := testVolumeBackup()
	listCalls, getCalls := 0, 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Project-ID") != backup.Project.Id.String() {
			t.Fatalf("wrong project: %q", req.Header.Get("Project-ID"))
		}
		switch {
		case req.URL.Path == "/v2/block-storage/volume-backups/":
			listCalls++
			if req.URL.Query().Get("name") != "backup" || req.URL.Query().Get("region_name") != "region" {
				t.Fatalf("wrong list filters: %s", req.URL.RawQuery)
			}
			page := blockstoragesdk.PagedVolumeBackupSchema{Count: 1, Results: []blockstoragesdk.VolumeBackupSchema{*backup}}
			body, _ := json.Marshal(page)
			return testHTTPResponse(req, http.StatusOK, string(body)), nil
		case strings.Contains(req.URL.Path, backup.Id.String()):
			getCalls++
			return testHTTPResponse(req, http.StatusOK, backupResponseJSON(backup)), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})
	d := &VolumeBackupDataSource{client: client, projectID: backup.Project.Id}
	result, diags := d.getVolumeBackup(context.Background(), VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{ID: types.StringValue(backup.Id.String())}})
	if diags.HasError() || result == nil || listCalls != 0 || getCalls != 1 {
		t.Fatalf("direct lookup failed: result=%#v calls=%d/%d diagnostics=%v", result, listCalls, getCalls, diags)
	}
	result, diags = d.getVolumeBackup(context.Background(), VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue(" backup "), Region: types.StringValue(" region ")}})
	if diags.HasError() || result == nil || listCalls != 1 || getCalls != 2 {
		t.Fatalf("filtered lookup failed: result=%#v calls=%d/%d diagnostics=%v", result, listCalls, getCalls, diags)
	}
}

func TestVolumeBackupDataSourceReadDiagnostics(t *testing.T) {
	backup := testVolumeBackup()
	other := *backup
	other.Id = core.UUID{5}
	for _, tt := range []struct {
		name        string
		config      VolumeBackupDataSourceModel
		status      int
		body        string
		wantSummary string
	}{
		{
			name:        "missing direct ID",
			config:      VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{ID: types.StringValue(backup.Id.String())}},
			status:      http.StatusNotFound,
			body:        `{}`,
			wantSummary: "Volume backup not found",
		},
		{
			name:        "no list match",
			config:      VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue(backup.Name)}},
			status:      http.StatusOK,
			body:        `{"count":0,"results":[]}`,
			wantSummary: "Unable to resolve volume backup",
		},
		{
			name:        "ambiguous list match",
			config:      VolumeBackupDataSourceModel{VolumeBackupModel: VolumeBackupModel{Name: types.StringValue(backup.Name)}},
			status:      http.StatusOK,
			body:        backupPageJSON(t, []blockstoragesdk.VolumeBackupSchema{*backup, other}),
			wantSummary: "Unable to resolve volume backup",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
				return testHTTPResponse(req, tt.status, tt.body), nil
			})
			d := &VolumeBackupDataSource{client: client, projectID: backup.Project.Id}
			var schemaResponse datasource.SchemaResponse
			d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
			plan := tfsdk.Plan{Schema: schemaResponse.Schema}
			if diags := plan.Set(context.Background(), &tt.config); diags.HasError() {
				t.Fatalf("set data source configuration: %v", diags)
			}
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
			d.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Raw: plan.Raw, Schema: schemaResponse.Schema}}, &response)
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != tt.wantSummary {
				t.Fatalf("want %q, got %v", tt.wantSummary, response.Diagnostics)
			}
		})
	}
}

func backupPageJSON(t *testing.T, results []blockstoragesdk.VolumeBackupSchema) string {
	t.Helper()
	body, err := json.Marshal(blockstoragesdk.PagedVolumeBackupSchema{Count: len(results), Results: results})
	if err != nil {
		t.Fatalf("marshal backup page: %v", err)
	}
	return string(body)
}
