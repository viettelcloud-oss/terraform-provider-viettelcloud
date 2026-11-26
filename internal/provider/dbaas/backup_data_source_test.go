package dbaas

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
)

func readBackupDataSource(t *testing.T, client *dbaassdk.Client, project *projectsdk.Client, config BackupDataSourceModel) (datasource.ReadResponse, BackupDataSourceModel) {
	t.Helper()
	ctx := context.Background()
	s := dataSourceSchema(t, NewBackupDataSource())
	null := tftypes.NewValue(s.Type().TerraformType(ctx), nil)
	configState := tfsdk.State{Schema: s, Raw: null}
	if diags := configState.Set(ctx, &config); diags.HasError() {
		t.Fatal(diags)
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: null}}
	(&BackupDataSource{client: client, project: project, projectID: testProjectID}).Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: configState.Raw}}, &resp)
	var got BackupDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	}
	return resp, got
}

func backupLookup(values map[string]string) BackupDataSourceModel {
	value := func(name string) types.String {
		if v, ok := values[name]; ok {
			return types.StringValue(v)
		}
		return types.StringNull()
	}
	m := BackupDataSourceModel{DBInstanceID: value("db_instance_id")}
	m.ID, m.Name, m.BackupType, m.Status, m.DatabaseType, m.Region = value("id"), value("name"), value("backup_type"), value("status"), value("database_type"), value("region")
	return m
}

func TestBackupDataSourceSchema(t *testing.T) {
	t.Parallel()
	attrs := dataSourceSchema(t, NewBackupDataSource()).Attributes
	for _, name := range []string{"id", "name", "db_instance_id", "backup_type", "status", "database_type", "region"} {
		if !attrs[name].IsOptional() || !attrs[name].IsComputed() {
			t.Fatalf("%s must be an Optional and Computed lookup argument", name)
		}
	}
	if _, ok := attrs["db_instance"]; ok {
		t.Fatal("the data source reports db_instance_id flat")
	}
}

func TestBackupDataSourceLookup(t *testing.T) {
	t.Parallel()
	automatic := *sampleBackup()
	automatic.Id, automatic.BackupType = testOtherID, dbaassdk.BackupTypeAutomatic
	orphaned := *sampleBackup()
	orphaned.DbInstance.SetNull()
	api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		if status, body, ok := catalog(req); ok {
			return status, body
		}
		if req.Path == "/v2/dbaas/db-instance-backups/" {
			return http.StatusOK, page(automatic, *sampleBackup())
		}
		return http.StatusOK, orphaned
	})

	_, byID := readBackupDataSource(t, client, project, backupLookup(map[string]string{"id": testBackupID.String()}))
	if byID.ID.ValueString() != testBackupID.String() || !byID.DBInstanceID.IsNull() || !byID.DBInstanceName.IsNull() || byID.Size.ValueInt64() != 128 {
		t.Fatalf("an id lookup must map a missing instance to a null db_instance_id: %#v", byID)
	}

	resp, unique := readBackupDataSource(t, client, project, backupLookup(map[string]string{
		"name": "nightly", "db_instance_id": testInstanceID.String(), "backup_type": "manual", "region": "hn-1",
	}))
	if resp.Diagnostics.HasError() || unique.ID.ValueString() != testBackupID.String() || unique.DBInstanceID.ValueString() != testInstanceID.String() || unique.Region.ValueString() != "hn-1" {
		t.Fatalf("name, db_instance_id, and backup_type must select one backup: %v %#v", resp.Diagnostics, unique)
	}
	list := api.requestsTo(http.MethodGet, "/v2/dbaas/db-instance-backups/")
	query := list[len(list)-1].Query
	if query["name"][0] != "nightly" || query["db_instance_id"][0] != testInstanceID.String() || query["region_id"][0] != testRegionID.String() || query["backup_type"] != nil {
		t.Fatalf("supported filters must reach the backend and backup_type must not: %v", query)
	}
}

func TestBackupDataSourceDiagnostics(t *testing.T) {
	t.Parallel()
	prefix := *sampleBackup()
	prefix.Name = "nightly-old"
	_, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		if req.Path == "/v2/dbaas/db-instance-backups/" {
			if req.Query["name"][0] == "twin" {
				twin := *sampleBackup()
				twin.Name = "twin"
				other := twin
				other.Id = testOtherID
				return http.StatusOK, page(twin, other)
			}
			return http.StatusOK, page(prefix)
		}
		return http.StatusNotFound, apiError("not_found", "Not found")
	})
	for _, tt := range []struct {
		name   string
		config map[string]string
		want   string
	}{
		{"missing criteria", map[string]string{}, "Missing DB Instance Backup lookup criteria"},
		{"blank name", map[string]string{"name": " "}, "Invalid DB Instance Backup lookup criteria"},
		{"invalid id", map[string]string{"id": "nightly"}, "id"},
		{"invalid instance id", map[string]string{"name": "nightly", "db_instance_id": "orders"}, "db_instance_id"},
		{"id not found", map[string]string{"id": testBackupID.String()}, "DB instance backup not found"},
		{"name only contained", map[string]string{"name": "nightly"}, "not found"},
		{"ambiguous", map[string]string{"name": "twin"}, "ambiguous"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, _ := readBackupDataSource(t, client, project, backupLookup(tt.config))
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Summary()+resp.Diagnostics[0].Detail(), tt.want) {
				t.Fatalf("want %q, got %v", tt.want, resp.Diagnostics)
			}
		})
	}
}

func TestBackupDataSourceTypeName(t *testing.T) {
	t.Parallel()
	var resp datasource.MetadataResponse
	NewBackupDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_db_backup" {
		t.Fatalf("type = %q, want viettelcloud_db_backup", resp.TypeName)
	}
}
