package filestorage

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragepkg "github.com/viettelcloud-oss/sdks/go/filestorage"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func filesystemDataSourceSchema(t *testing.T) datasource.SchemaResponse {
	t.Helper()
	var resp datasource.SchemaResponse
	(&FilesystemDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	return resp
}

func TestFilesystemDataSourceConfigure(t *testing.T) {
	t.Parallel()
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	})
	d := &FilesystemDataSource{}

	var invalid datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: 42}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	var resp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{
		FileStorage: client, ProjectID: fsTestProjectID,
	}}, &resp)
	if resp.Diagnostics.HasError() || d.client != client || d.projectID != fsTestProjectID {
		t.Fatalf("configure failed: %#v diags=%v", d, resp.Diagnostics)
	}
}

func TestFilesystemDataSourceDirectIDPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	fs := sampleFilesystem()
	fsJSONStr := filesystemJSON(fs)
	getCalls := 0
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
			getCalls++
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
			ID:             types.StringValue(fsTestID.String()),
		},
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", resp.Diagnostics)
	}
	if getCalls != 1 {
		t.Fatalf("expected 1 GET call, got %d", getCalls)
	}
	var result FilesystemDataSourceModel
	if diags := resp.State.Get(ctx, &result); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if result.ID.ValueString() != fsTestID.String() {
		t.Fatalf("unexpected ID: %s", result.ID.ValueString())
	}
	if result.Name.ValueString() != fs.Name {
		t.Fatalf("unexpected name: %s", result.Name.ValueString())
	}
}

func TestFilesystemDataSourceFilterPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	fs := sampleFilesystem()
	fsJSONStr := filesystemJSON(fs)
	listCalls := 0
	getCalls := 0
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystems/"):
			listCalls++
			if req.URL.Query().Get("name") != "my-fs" {
				t.Errorf("unexpected list query: %s", req.URL.RawQuery)
			}
			paged := filestoragepkg.PagedFilesystemSchema{
				Count: 1,
				Results: []filestoragepkg.FilesystemSchema{{
					Id:     fsTestID,
					Name:   "my-fs",
					Status: filestoragepkg.FilesystemStatusAvailable,
					Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
					Subnet: filestoragepkg.NestedSubnetSchema{Id: fsTestSubnetID},
				}},
			}
			body, _ := json.Marshal(paged)
			return fsHTTPResponse(req, http.StatusOK, string(body)), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
			getCalls++
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	name := "my-fs"
	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
			Name:           types.StringValue(name),
		},
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", resp.Diagnostics)
	}
	if listCalls != 1 || getCalls != 1 {
		t.Fatalf("unexpected call counts: list=%d get=%d", listCalls, getCalls)
	}
}

// TestFilesystemDataSourceKeepsConfiguredSpelling covers AC15 for lookups:
// the finder matches trimmed criteria, and state keeps the configured spelling
// of id, name, zone, and subnet_id.
func TestFilesystemDataSourceKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	fs := sampleFilesystem()
	fsJSONStr := filesystemJSON(fs)
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystems/") {
			if q := req.URL.Query(); q.Get("name") != "my-fs" || q.Get("zone") != "zone-a" {
				t.Errorf("list query must carry trimmed criteria: %s", req.URL.RawQuery)
			}
			body, _ := json.Marshal(filestoragepkg.PagedFilesystemSchema{Count: 1, Results: []filestoragepkg.FilesystemSchema{*fs}})
			return fsHTTPResponse(req, http.StatusOK, string(body)), nil
		}
		return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{
			ID:             types.StringValue(" " + strings.ToUpper(fsTestID.String()) + " "),
			Name:           types.StringValue(" my-fs "),
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
		},
		Zone:     types.StringValue(" zone-a "),
		SubnetID: types.StringValue(strings.ToUpper(fsTestSubnetID.String())),
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set config: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", resp.Diagnostics)
	}
	var got FilesystemDataSourceModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatalf("read state: %v", diags)
	}
	if !got.ID.Equal(config.ID) || !got.Name.Equal(config.Name) || !got.Zone.Equal(config.Zone) || !got.SubnetID.Equal(config.SubnetID) {
		t.Fatalf("configured spelling lost: id=%s name=%s zone=%s subnet_id=%s", got.ID, got.Name, got.Zone, got.SubnetID)
	}
}

func TestFilesystemDataSourceMissingCriteriaError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	d := &FilesystemDataSource{client: filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request: %s", req.URL)
		return nil, nil
	}), projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes)},
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error for missing criteria")
	}
	if got := resp.Diagnostics[0].Summary(); got != "Missing Filesystem lookup criteria" {
		t.Fatalf("diagnostic summary = %q, want %q", got, "Missing Filesystem lookup criteria")
	}
}

func TestFilesystemDataSourceNotFoundError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
			ID:             types.StringValue(fsTestID.String()),
		},
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error when filesystem not found")
	}
}

func TestFilesystemDataSourcePopulatesAllFields(t *testing.T) {
	t.Parallel()
	fs := sampleFilesystem()
	state := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes)},
	}
	populateFilesystemDataSourceState(fs, &state)

	if state.ID.ValueString() != fsTestID.String() {
		t.Errorf("id mismatch: %s", state.ID.ValueString())
	}
	if state.Zone.ValueString() != "zone-a" {
		t.Errorf("zone mismatch: %s", state.Zone.ValueString())
	}
	if state.SubnetID.ValueString() != fsTestSubnetID.String() {
		t.Errorf("subnet_id mismatch: %s", state.SubnetID.ValueString())
	}
	if state.FilesystemTypeID.ValueString() != fsTestTypeID.String() {
		t.Errorf("filesystem_type_id mismatch: %s", state.FilesystemTypeID.ValueString())
	}
	if state.FilesystemTypeName.ValueString() != "Standard NFS" {
		t.Errorf("filesystem_type_name mismatch: %s", state.FilesystemTypeName.ValueString())
	}
}

func TestFilesystemDataSourceSubnetIDFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	fs := sampleFilesystem()
	fsJSONStr := filesystemJSON(fs)
	listCalls := 0
	getCalls := 0
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystems/"):
			listCalls++
			gotSubnet := req.URL.Query().Get("subnet_id")
			if gotSubnet != fsTestSubnetID.String() {
				t.Errorf("expected subnet_id=%s, got %s", fsTestSubnetID, gotSubnet)
			}
			paged := filestoragepkg.PagedFilesystemSchema{
				Count: 1,
				Results: []filestoragepkg.FilesystemSchema{{
					Id:     fsTestID,
					Name:   "my-fs",
					Status: filestoragepkg.FilesystemStatusAvailable,
					Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
					Subnet: filestoragepkg.NestedSubnetSchema{Id: fsTestSubnetID},
				}},
			}
			body, _ := json.Marshal(paged)
			return fsHTTPResponse(req, http.StatusOK, string(body)), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
			getCalls++
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes)},
		SubnetID:        types.StringValue(fsTestSubnetID.String()),
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read by subnet_id failed: %v", resp.Diagnostics)
	}
	if listCalls != 1 || getCalls != 1 {
		t.Fatalf("unexpected call counts: list=%d get=%d", listCalls, getCalls)
	}

	// Also check that passing an invalid UUID for subnet_id fails.
	badConfig := config
	badConfig.SubnetID = types.StringValue("not-a-uuid")
	badState := tfsdk.State{Schema: schemaResp.Schema}
	if diags := badState.Set(ctx, badConfig); diags.HasError() {
		t.Fatalf("set bad state: %v", diags)
	}
	badResp := datasource.ReadResponse{State: badState}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(badState)}, &badResp)
	if !badResp.Diagnostics.HasError() {
		t.Fatal("expected error for invalid subnet_id UUID")
	}
}

func TestFilesystemDataSourceIDFilterCombinedWithName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	fs := sampleFilesystem()
	fsJSONStr := filesystemJSON(fs)
	client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/filesystems/"):
			paged := filestoragepkg.PagedFilesystemSchema{
				Count: 1,
				Results: []filestoragepkg.FilesystemSchema{{
					Id:     fsTestID,
					Name:   "my-fs",
					Status: filestoragepkg.FilesystemStatusAvailable,
					Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
					Subnet: filestoragepkg.NestedSubnetSchema{Id: fsTestSubnetID},
				}},
			}
			body, _ := json.Marshal(paged)
			return fsHTTPResponse(req, http.StatusOK, string(body)), nil
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, fsTestID.String()):
			return fsHTTPResponse(req, http.StatusOK, fsJSONStr), nil
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
	})
	d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

	config := FilesystemDataSourceModel{
		FilesystemModel: FilesystemModel{
			SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
			ID:             types.StringValue(fsTestID.String()),
			Name:           types.StringValue("my-fs"),
		},
	}
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, config); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("combined id+name read failed: %v", resp.Diagnostics)
	}

	// Now test that a mismatched id causes an error.
	mismatch := config
	otherID := core.UUID{99}
	mismatch.ID = types.StringValue(otherID.String())
	mismatchState := tfsdk.State{Schema: schemaResp.Schema}
	if diags := mismatchState.Set(ctx, mismatch); diags.HasError() {
		t.Fatalf("set mismatch state: %v", diags)
	}
	mismatchResp := datasource.ReadResponse{State: mismatchState}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(mismatchState)}, &mismatchResp)
	if !mismatchResp.Diagnostics.HasError() {
		t.Fatal("expected mismatch error when resolved id differs from configured id")
	}
}

func TestFilesystemDataSourceRejectsWhitespaceOnlyCriteria(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	cases := []struct {
		name  string
		model FilesystemDataSourceModel
		want  string
	}{
		{"name", FilesystemDataSourceModel{FilesystemModel: FilesystemModel{Name: types.StringValue("   ")}}, "name must not be empty or whitespace-only"},
		{"zone", FilesystemDataSourceModel{Zone: types.StringValue("\t ")}, "zone must not be empty or whitespace-only"},
		{"name with zone", FilesystemDataSourceModel{FilesystemModel: FilesystemModel{Name: types.StringValue(" ")}, Zone: types.StringValue("zone-a")}, "name must not be empty or whitespace-only"},
		{"id", FilesystemDataSourceModel{FilesystemModel: FilesystemModel{ID: types.StringValue("  ")}}, "id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &FilesystemDataSource{client: filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			}), projectID: fsTestProjectID}

			model := tc.model
			model.SnapshotPolicy = types.ObjectNull(snapshotPolicyAttrTypes)
			state := tfsdk.State{Schema: schemaResp.Schema}
			if diags := state.Set(ctx, model); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			resp := datasource.ReadResponse{State: state}
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected error for whitespace-only criterion")
			}
			got := resp.Diagnostics[0].Summary() + " " + resp.Diagnostics[0].Detail()
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected %q in diagnostic, got: %s", tc.want, got)
			}
		})
	}
}

// TestFilesystemDataSourceFilterRequiresExactlyOneMatch covers AC13: a filter
// lookup that matches no filesystem or more than one fails without selecting
// a filesystem.
func TestFilesystemDataSourceFilterRequiresExactlyOneMatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schemaResp := filesystemDataSourceSchema(t)

	match := func(id core.UUID) filestoragepkg.FilesystemSchema {
		return filestoragepkg.FilesystemSchema{
			Id:     id,
			Name:   "my-fs",
			Status: filestoragepkg.FilesystemStatusAvailable,
			Zone:   filestoragepkg.NestedZoneSchema{Id: fsTestZoneID, Name: "zone-a"},
			Subnet: filestoragepkg.NestedSubnetSchema{Id: fsTestSubnetID},
		}
	}
	cases := []struct {
		name    string
		results []filestoragepkg.FilesystemSchema
		want    string
	}{
		{"no match", nil, "was not found matching criteria"},
		{"other names only", []filestoragepkg.FilesystemSchema{{Id: core.UUID{3}, Name: "my-fs-old"}}, "was not found matching criteria"},
		{"multiple matches", []filestoragepkg.FilesystemSchema{match(fsTestID), match(core.UUID{4})}, "is ambiguous"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := filesystemTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/filesystems/") {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					return fsHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
				}
				body, _ := json.Marshal(filestoragepkg.PagedFilesystemSchema{Count: len(tc.results), Results: tc.results})
				return fsHTTPResponse(req, http.StatusOK, string(body)), nil
			})
			d := &FilesystemDataSource{client: client, projectID: fsTestProjectID}

			config := FilesystemDataSourceModel{
				FilesystemModel: FilesystemModel{
					SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes),
					Name:           types.StringValue("my-fs"),
				},
				Zone: types.StringValue("zone-a"),
			}
			state := tfsdk.State{Schema: schemaResp.Schema}
			if diags := state.Set(ctx, config); diags.HasError() {
				t.Fatalf("set state: %v", diags)
			}
			resp := datasource.ReadResponse{State: state}
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config(state)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error")
			}
			if got := resp.Diagnostics[0].Summary(); got != "Unable to resolve filesystem" {
				t.Fatalf("diagnostic summary = %q", got)
			}
			if got := resp.Diagnostics[0].Detail(); !strings.Contains(got, tc.want) {
				t.Fatalf("diagnostic detail %q does not contain %q", got, tc.want)
			}
			var got FilesystemDataSourceModel
			if diags := resp.State.Get(ctx, &got); diags.HasError() {
				t.Fatalf("get state: %v", diags)
			}
			if !got.ID.IsNull() {
				t.Fatalf("a filesystem was selected: id=%s", got.ID)
			}
		})
	}
}

func TestFilesystemDataSourceSnapshotID(t *testing.T) {
	t.Parallel()
	snapshotID := core.UUID{0xab}
	fs := sampleFilesystem()
	state := FilesystemDataSourceModel{FilesystemModel: FilesystemModel{SnapshotPolicy: types.ObjectNull(snapshotPolicyAttrTypes)}}
	populateFilesystemDataSourceState(fs, &state)
	if !state.SnapshotID.IsNull() {
		t.Fatalf("snapshot_id = %s, want null for a filesystem not created from a snapshot", state.SnapshotID)
	}
	fs.CreateFrom.Snapshot = core.NullableFromPtr(&filestoragepkg.NestedFilesystemSnapshotSchema{Id: snapshotID, Name: "snap"})
	populateFilesystemDataSourceState(fs, &state)
	if state.SnapshotID.ValueString() != snapshotID.String() {
		t.Fatalf("snapshot_id = %s, want %s", state.SnapshotID, snapshotID)
	}
}
