package blockstorage

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func TestVolumeDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()
	var response datasource.SchemaResponse
	(&VolumeDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	state := tfsdk.State{Schema: response.Schema}
	if diags := state.Set(context.Background(), &VolumeDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestVolumeDataSourceConfigure(t *testing.T) {
	t.Parallel()
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	})
	d := &VolumeDataSource{}
	var invalid datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 42}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}
	var response datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{
		BlockStorage: client, ProjectID: core.UUID{9},
	}}, &response)
	if response.Diagnostics.HasError() || d.client != client || d.projectID != (core.UUID{9}) {
		t.Fatalf("unexpected configure result: data source=%#v diagnostics=%v", d, response.Diagnostics)
	}
}

func TestVolumeDataSourceDirectIDAndFilteredPaths(t *testing.T) {
	t.Parallel()
	listCalls := 0
	getCalls := 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v2/block-storage/volumes/":
			listCalls++
			if req.URL.Query().Get("name") != "data" || req.URL.Query().Get("zone") != "zone-a" {
				t.Fatalf("unexpected list query: %s", req.URL.RawQuery)
			}
			volume := sampleVolumeDetail()
			paged := blockstoragesdk.PagedVolumeSchema{Count: 1, Results: []blockstoragesdk.VolumeSchema{{
				Id: volume.Id, Name: volume.Name, Size: volume.Size, Status: volume.Status, Zone: volume.Zone,
			}}}
			body, _ := json.Marshal(paged)
			return testHTTPResponse(req, http.StatusOK, string(body)), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, volumeTestID.String()):
			getCalls++
			return testHTTPResponse(req, http.StatusOK, volumeResponseJSON(blockstoragesdk.VolumeStatusAvailable)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})
	d := &VolumeDataSource{client: client, projectID: core.UUID{9}}

	volume, diags := d.getVolume(context.Background(), VolumeDataSourceModel{VolumeModel: VolumeModel{ID: types.StringValue(volumeTestID.String())}})
	if diags.HasError() || volume == nil || volume.Id != volumeTestID || listCalls != 0 || getCalls != 1 {
		t.Fatalf("unexpected direct-ID result: volume=%#v list=%d get=%d diags=%v", volume, listCalls, getCalls, diags)
	}

	volume, diags = d.getVolume(context.Background(), VolumeDataSourceModel{VolumeModel: VolumeModel{
		Name: types.StringValue(" data "),
	}, Zone: types.StringValue(" zone-a ")})
	if diags.HasError() || volume == nil || listCalls != 1 || getCalls != 2 {
		t.Fatalf("unexpected filtered result: volume=%#v list=%d get=%d diags=%v", volume, listCalls, getCalls, diags)
	}
}

func TestVolumeDataSourceRejectsInvalidCriteria(t *testing.T) {
	t.Parallel()
	d := &VolumeDataSource{}
	tests := []struct {
		name   string
		config VolumeDataSourceModel
		want   string
	}{
		{name: "missing", config: VolumeDataSourceModel{}, want: "Missing volume lookup criteria"},
		{name: "blank", config: VolumeDataSourceModel{VolumeModel: VolumeModel{Name: types.StringValue(" ")}}, want: "Invalid volume lookup criteria"},
		{name: "empty ID", config: VolumeDataSourceModel{VolumeModel: VolumeModel{ID: types.StringValue("")}}, want: "Invalid volume lookup criteria"},
		{name: "invalid ID", config: VolumeDataSourceModel{VolumeModel: VolumeModel{ID: types.StringValue("not-a-uuid")}}, want: "Invalid UUID"},
		{name: "invalid size", config: VolumeDataSourceModel{VolumeModel: VolumeModel{Size: types.Int64Value(0)}}, want: "Invalid volume lookup criteria"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := d.getVolume(context.Background(), tt.config)
			if !diags.HasError() || diags[0].Summary() != tt.want {
				t.Fatalf("expected %q diagnostic, got %v", tt.want, diags)
			}
		})
	}
}

func TestVolumeDataSourceReadPopulatesStateFromBackend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeDataSourceSchema(t)
	description := "database"
	iops := 3000
	volume := sampleVolumeDetail()
	volume.Description = &description
	volume.Iops = &iops

	requests := 0
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if got := req.Header.Get("Project-ID"); got != volumeTestProjectID.String() {
			t.Errorf("expected Project-ID %q, got %q", volumeTestProjectID, got)
		}
		if req.Method != http.MethodGet || !strings.Contains(req.URL.Path, volumeTestID.String()) {
			t.Fatalf("a known ID must be read directly, got %s %s", req.Method, req.URL.Path)
		}
		return testHTTPResponse(req, http.StatusOK, volumeDetailJSON(volume)), nil
	})
	d := &VolumeDataSource{client: client, projectID: volumeTestProjectID}

	config := volumeDataSourceConfig(t, VolumeDataSourceModel{VolumeModel: VolumeModel{ID: volumeIDUpper(volumeTestID)}})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: volumeSchema}}
	d.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}

	var state VolumeDataSourceModel
	if diags := response.State.Get(ctx, &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != strings.ToUpper(volumeTestID.String()) {
		t.Fatalf("the configured ID spelling must survive the read, got %q", state.ID.ValueString())
	}
	if state.Name.ValueString() != "data" || state.Size.ValueInt64() != 100 || state.Zone.ValueString() != "zone-a" ||
		state.VolumeType.ValueString() != "Premium" || state.Description.ValueString() != "database" || state.IOPS.ValueInt64() != 3000 {
		t.Fatalf("unexpected data source state: %#v", state)
	}
	if requests != 1 {
		t.Fatalf("expected a single read request, got %d", requests)
	}
}

func TestVolumeDataSourceReadReportsMissingVolume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	volumeSchema := volumeDataSourceSchema(t)
	client := volumeTestClient(t, func(req *http.Request) (*http.Response, error) {
		return testHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	d := &VolumeDataSource{client: client, projectID: volumeTestProjectID}

	config := volumeDataSourceConfig(t, VolumeDataSourceModel{VolumeModel: VolumeModel{ID: types.StringValue(volumeTestID.String())}})
	response := datasource.ReadResponse{State: tfsdk.State{Schema: volumeSchema}}
	d.Read(ctx, datasource.ReadRequest{Config: config}, &response)
	if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != "Volume not found" {
		t.Fatalf("expected a not-found diagnostic, got %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("a failed read must not write state")
	}
}

func TestPreserveVolumeConfiguredValues(t *testing.T) {
	t.Parallel()
	volume := sampleVolumeDetail()
	config := VolumeDataSourceModel{VolumeModel: VolumeModel{
		ID: volumeIDUpper(volume.Id), Name: types.StringValue("  data  "), Status: types.StringValue(" available "),
	}, Zone: types.StringValue(" zone-a ")}
	var state VolumeDataSourceModel
	populateVolumeDataSourceState(volume, &state)
	preserveVolumeConfiguredValues(volume, &config, &state)
	if state.ID.ValueString() != strings.ToUpper(volume.Id.String()) || state.Name.ValueString() != "  data  " ||
		state.Status.ValueString() != " available " || state.Zone.ValueString() != " zone-a " {
		t.Fatalf("configured representations were not preserved: %#v", state)
	}

	// core.ParseUUID accepts the braced spelling, so it names the same volume and
	// must survive the read; comparing IDs as text would rewrite it.
	braced := VolumeDataSourceModel{VolumeModel: VolumeModel{ID: types.StringValue("{" + volume.Id.String() + "}")}}
	var bracedState VolumeDataSourceModel
	populateVolumeDataSourceState(volume, &bracedState)
	preserveVolumeConfiguredValues(volume, &braced, &bracedState)
	if bracedState.ID.ValueString() != "{"+volume.Id.String()+"}" {
		t.Fatalf("a braced UUID names the same volume and must be preserved, got %q", bracedState.ID.ValueString())
	}
}

func volumeIDUpper(id core.UUID) types.String {
	return types.StringValue(strings.ToUpper(id.String()))
}

func volumeDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&VolumeDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("build volume data source schema: %v", response.Diagnostics)
	}
	return response.Schema
}

// volumeDataSourceConfig builds a datasource.ReadRequest config. tfsdk.Config
// exposes no setter, so the value is round-tripped through a state of the same
// schema.
func volumeDataSourceConfig(t *testing.T, model VolumeDataSourceModel) tfsdk.Config {
	t.Helper()
	dataSourceSchema := volumeDataSourceSchema(t)
	state := tfsdk.State{Schema: dataSourceSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set volume data source config: %v", diags)
	}
	return tfsdk.Config{Schema: dataSourceSchema, Raw: state.Raw}
}
