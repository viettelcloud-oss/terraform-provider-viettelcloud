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
)

func readInstanceDataSource(t *testing.T, client *dbaassdk.Client, config InstanceDataSourceModel) (datasource.ReadResponse, InstanceDataSourceModel) {
	t.Helper()
	ctx := context.Background()
	s := dataSourceSchema(t, NewInstanceDataSource())
	state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
	if diags := state.Set(ctx, &config); diags.HasError() {
		t.Fatal(diags)
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
	(&InstanceDataSource{client: client, projectID: testProjectID}).Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: state.Raw}}, &resp)
	var got InstanceDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	}
	return resp, got
}

func instanceLookup(id, name types.String) InstanceDataSourceModel {
	m := InstanceDataSourceModel{InstanceModel: nullInstanceModel()}
	m.ID, m.Name = id, name
	return m
}

func TestInstanceDataSourceSchema(t *testing.T) {
	t.Parallel()
	attrs := dataSourceSchema(t, NewInstanceDataSource()).Attributes
	for _, name := range []string{"id", "name"} {
		if !attrs[name].IsOptional() || !attrs[name].IsComputed() {
			t.Fatalf("%s must be an Optional and Computed lookup argument", name)
		}
	}
	if _, ok := attrs["disk_size"]; ok {
		t.Fatal("the requested disk_size exists only in the resource")
	}
	if _, ok := attrs["root_password"]; ok {
		t.Fatal("root_password exists only in the resource")
	}
	for _, name := range []string{"current_disk_size", "db_version", "db_version_id", "flavor", "flavor_id", "volume_type", "volume_type_id", "region", "subnet_id", "backup_policy"} {
		if attribute, ok := attrs[name]; !ok || attribute.IsOptional() || !attribute.IsComputed() {
			t.Fatalf("%s must be a Computed output", name)
		}
	}
}

func TestInstanceDataSourceLookup(t *testing.T) {
	t.Parallel()
	api, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
		if req.Path == "/v2/dbaas/db-instances/" {
			prefix := *sampleInstance()
			prefix.Id, prefix.Name = testOtherID, "orders-archive"
			return http.StatusOK, page(prefix, *sampleInstance())
		}
		return http.StatusOK, sampleInstance()
	})
	_, byID := readInstanceDataSource(t, client, instanceLookup(types.StringValue(strings.ToUpper(testInstanceID.String())), types.StringNull()))
	if byID.ID.ValueString() != strings.ToUpper(testInstanceID.String()) || byID.CurrentDiskSize.ValueInt64() != 20 || byID.DBVersion.ValueString() != "16" ||
		byID.DBVersionID.ValueString() != testVersionID.String() || byID.Flavor.ValueString() != "db.small" || byID.VolumeTypeID.ValueString() != testVolumeTypeID.String() ||
		byID.Region.ValueString() != "hn-1" || byID.SubnetID.ValueString() != testSubnetID.String() || byID.Name.ValueString() != "orders" {
		t.Fatalf("flat outputs: %#v", byID)
	}
	if len(api.requestsTo(http.MethodGet, "/v2/dbaas/db-instances/")) != 1 || api.requests[0].Path != "/v2/dbaas/db-instances/"+testInstanceID.String()+"/" {
		t.Fatalf("an id lookup must read the instance directly: %v", api.requests)
	}

	resp, byName := readInstanceDataSource(t, client, instanceLookup(types.StringNull(), types.StringValue("orders")))
	if resp.Diagnostics.HasError() || byName.ID.ValueString() != testInstanceID.String() {
		t.Fatalf("a name lookup must pick the exact match among icontains results: %v %#v", resp.Diagnostics, byName.ID)
	}
	if list := api.requestsTo(http.MethodGet, "/v2/dbaas/db-instances/"); list[len(list)-1].Query["name"][0] != "orders" || list[len(list)-1].ProjectID != testProjectID.String() {
		t.Fatalf("the name filter and project must reach the backend: %v", list)
	}
}

func TestInstanceDataSourceDiagnostics(t *testing.T) {
	t.Parallel()
	_, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
		if req.Path == "/v2/dbaas/db-instances/" {
			prefix := *sampleInstance()
			prefix.Name = "orders-archive"
			return http.StatusOK, page(prefix)
		}
		return http.StatusNotFound, apiError("not_found", "Not found")
	})
	for _, tt := range []struct {
		name   string
		config InstanceDataSourceModel
		want   string
	}{
		{"missing criteria", instanceLookup(types.StringNull(), types.StringNull()), "Missing DB Instance lookup criteria"},
		{"both criteria", instanceLookup(types.StringValue(testInstanceID.String()), types.StringValue("orders")), "Conflicting DB Instance lookup criteria"},
		{"blank name", instanceLookup(types.StringNull(), types.StringValue("  ")), "Invalid DB Instance lookup criteria"},
		{"invalid id", instanceLookup(types.StringValue("orders"), types.StringNull()), "id"},
		{"id not found", instanceLookup(types.StringValue(testInstanceID.String()), types.StringNull()), "DB instance not found"},
		{"name only contained", instanceLookup(types.StringNull(), types.StringValue("orders")), "Unable to resolve DB instance"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, _ := readInstanceDataSource(t, client, tt.config)
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Summary()+resp.Diagnostics[0].Detail(), tt.want) {
				t.Fatalf("want %q, got %v", tt.want, resp.Diagnostics)
			}
		})
	}
}

func TestInstanceDataSourceTypeName(t *testing.T) {
	t.Parallel()
	var resp datasource.MetadataResponse
	NewInstanceDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_db_instance" {
		t.Fatalf("type = %q, want viettelcloud_db_instance", resp.TypeName)
	}
}
