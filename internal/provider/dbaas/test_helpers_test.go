package dbaas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	dbaasselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

// The instance, subnet, and backup IDs contain hex letters, so that
// strings.ToUpper produces a different spelling of the same ID.
var (
	testProjectID    = core.UUID{9}
	testRegionID     = core.UUID{10}
	testInstanceID   = core.UUID{0xa1}
	testVersionID    = core.UUID{2}
	testFlavorID     = core.UUID{3}
	testVolumeTypeID = core.UUID{4}
	testSubnetID     = core.UUID{0xb5}
	testVPCID        = core.UUID{6}
	testBackupID     = core.UUID{0xc7}
	testOtherID      = core.UUID{8}
)

// apiRequest is one request that the fake API received.
type apiRequest struct {
	Method, Path string
	Query        map[string][]string
	ProjectID    string
	Body         map[string]any
	RawBody      string
}

func (r apiRequest) String() string { return r.Method + " " + r.Path }

// fakeAPI serves every client of a test from handle and records requests.
// handle returns a status and a body that is marshaled as JSON; a nil body
// sends no content.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []apiRequest
	handle   func(apiRequest) (int, any)
	// network serves the subnet check of a DB instance create.
	network *networksdk.Client
}

func (f *fakeAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	recorded := apiRequest{Method: req.Method, Path: req.URL.Path, Query: req.URL.Query(), ProjectID: req.Header.Get("Project-ID")}
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		recorded.RawBody = string(raw)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &recorded.Body); err != nil {
				f.t.Errorf("request body is not a JSON object: %s", raw)
			}
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, recorded)
	f.mu.Unlock()
	status, body := f.handle(recorded)
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			f.t.Fatalf("marshal response: %v", err)
		}
	}
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(payload)), Request: req,
	}, nil
}

// requestsTo returns the recorded requests with the method and path prefix.
func (f *fakeAPI) requestsTo(method, path string) []apiRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []apiRequest
	for _, req := range f.requests {
		if req.Method == method && strings.HasPrefix(req.Path, path) {
			matched = append(matched, req)
		}
	}
	return matched
}

func newFakeAPI(t *testing.T, handle func(apiRequest) (int, any)) (*fakeAPI, *dbaassdk.Client, *projectsdk.Client) {
	t.Helper()
	api := &fakeAPI{t: t, handle: handle}
	httpClient := &http.Client{Transport: api}
	client, err := dbaassdk.NewClient("https://dbaas.test", dbaassdk.WithHTTPClient(httpClient), dbaassdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	project, err := projectsdk.NewClient("https://project.test", projectsdk.WithHTTPClient(httpClient), projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatal(err)
	}
	if api.network, err = networksdk.NewClient("https://network.test", networksdk.WithHTTPClient(httpClient), networksdk.WithRetry(core.NoRetry())); err != nil {
		t.Fatal(err)
	}
	return api, client, project
}

// apiError is the error envelope of the backend.
func apiError(code, message string) map[string]any {
	return map[string]any{"errors": []map[string]string{{"code": code, "message": message}}}
}

func page[T any](items ...T) map[string]any {
	return map[string]any{"count": len(items), "results": items}
}

// catalog answers the catalog and region requests of a create or a selector
// plan. It reports false for any other request.
func catalog(req apiRequest) (int, any, bool) {
	if req.Method != http.MethodGet {
		return 0, nil, false
	}
	switch req.Path {
	case "/v2/projects/" + testProjectID.String() + "/regions/":
		return http.StatusOK, page(projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: testRegionID, Name: "hn-1"}}), true
	case "/v2/dbaas/db-versions/":
		return http.StatusOK, page(
			dbaassdk.DBaaSVersionSchema{Id: testVersionID, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: testRegionID, Version: "16"},
			dbaassdk.DBaaSVersionSchema{Id: testOtherID, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: testRegionID, Version: "17"},
		), true
	case "/v2/dbaas/dbaas-flavors/":
		return http.StatusOK, page(
			dbaassdk.DBaaSRegionFlavorSchema{Id: testFlavorID, RegionId: testRegionID, FlavorName: "db.small", Vcpus: 2, Ram: 4096},
			dbaassdk.DBaaSRegionFlavorSchema{Id: testOtherID, RegionId: testRegionID, FlavorName: "db.large", Vcpus: 8, Ram: 16384},
		), true
	case "/v2/network/subnets/" + testSubnetID.String() + "/":
		return http.StatusOK, testSubnet(testRegionID), true
	case "/v2/dbaas/dbaas-volume-types/":
		return http.StatusOK, page(dbaassdk.DBaaSRegionVolumeTypeSchema{Id: testVolumeTypeID, RegionId: testRegionID, Name: "ssd", DiskType: "ssd"}), true
	}
	return 0, nil, false
}

func sampleInstance() *dbaassdk.DBInstanceSchema {
	created := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	return &dbaassdk.DBInstanceSchema{
		Id: testInstanceID, Name: "orders", Description: "orders db", DatabaseType: dbaassdk.DatabaseTypePostgresql,
		DbVersion: dbaassdk.NestedDBVersionSchema{Id: testVersionID, DatabaseType: dbaassdk.DatabaseTypePostgresql, RegionId: testRegionID, Version: "16"},
		Flavor:    dbaassdk.NestedDBaaSRegionFlavorSchema{Id: testFlavorID, FlavorName: "db.small", RegionId: testRegionID, Vcpus: 2, Ram: 4096},
		VolumeType: dbaassdk.NestedDBaaSRegionVolumeTypeSchema{
			Id: testVolumeTypeID, Name: "ssd", DiskType: "ssd", RegionId: testRegionID,
		},
		Region:   dbaassdk.NestedRegionSchema{Id: testRegionID, Name: "hn-1"},
		DiskSize: 20, UsedDiskSize: 512, Solution: dbaassdk.SolutionBasic,
		Subnet: dbaassdk.NestedDBaaSSubnetSchema{
			Id: testSubnetID, Name: "db-subnet", Cidr: "10.0.1.0/24", CreatedAt: created, UpdatedAt: created,
			Vpc: dbaassdk.NestedVPCSchema{Id: testVPCID, Name: "db-vpc"},
		},
		MDow: dbaassdk.DayOfWeekSunday, MStartHour: 0, KeepBackup: true,
		AllowedCidrs: []string{"10.0.0.0/8"}, PrivateUrl: "orders.internal", Port: 5432,
		ProjectId: testProjectID, Status: dbaassdk.DBInstanceStatusACTIVE, OperationalStatus: dbaassdk.DBInstanceOperationalStatusNOOP,
		CreatedAt: created, UpdatedAt: created,
	}
}

func sampleBackup() *dbaassdk.DBInstanceBackupSchema {
	created := time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)
	return &dbaassdk.DBInstanceBackupSchema{
		Id: testBackupID, Name: "nightly", Description: "before upgrade", BackupType: dbaassdk.BackupTypeManual,
		DbInstance:   core.NullableFromPtr(&dbaassdk.NestedDBInstanceSchema{Id: testInstanceID, Name: "orders"}),
		DatabaseType: dbaassdk.DatabaseTypePostgresql,
		DbVersion:    dbaassdk.NestedDBVersionSchema{Id: testVersionID, Version: "16"},
		Region:       dbaassdk.NestedRegionSchema{Id: testRegionID, Name: "hn-1"},
		BackupInfo:   "full", Size: 128, RequiredVolumeSize: 20, BackupTimestamp: core.NullableFromPtr(&created),
		OwnerId: testOtherID, ProjectId: testProjectID, Status: dbaassdk.BackupStatusCOMPLETED,
		CreatedAt: created, UpdatedAt: created,
	}
}

func instanceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewInstanceResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp.Schema
}

func backupSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewBackupResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp.Schema
}

func dataSourceSchema(t *testing.T, d datasource.DataSource) dsschema.Schema {
	t.Helper()
	var resp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp.Schema
}

// nullInstanceModel returns a model whose objects and sets are typed nulls.
func nullInstanceModel() InstanceModel {
	return InstanceModel{
		MaintenanceConfig:      types.ObjectNull(maintenanceConfigAttributeTypes()),
		BackupPolicy:           types.ObjectNull(backupPolicyAttributeTypes()),
		AutoIncreaseVolumeSize: types.ObjectNull(autoIncreaseAttributeTypes()),
		AllowedCIDRs:           types.SetNull(types.StringType),
	}
}

func testSelector(attributeTypes map[string]attr.Type, criterion, value string, id types.String) types.Object {
	return types.ObjectValueMust(attributeTypes, map[string]attr.Value{criterion: types.StringValue(value), "id": id})
}

// testInstanceConfig returns the required configuration of a DB instance.
func testInstanceConfig() InstanceResourceModel {
	m := InstanceResourceModel{
		InstanceModel: nullInstanceModel(),
		DBVersion:     testSelector(dbaasselector.DBVersionAttributeTypes(), "version", "16", types.StringNull()),
		Flavor:        testSelector(dbaasselector.FlavorAttributeTypes(), "name", "db.small", types.StringNull()),
		VolumeType:    testSelector(dbaasselector.VolumeTypeAttributeTypes(), "name", "ssd", types.StringNull()),
		Region:        reference.Value(reference.Name, types.StringValue("hn-1")),
		Subnet:        reference.Value(reference.ID, types.StringValue(testSubnetID.String())),
		DiskSize:      types.Int64Value(20),
		RootPassword:  types.StringValue("Secret#123"),
	}
	m.Name, m.DatabaseType, m.Solution = types.StringValue("orders"), types.StringValue("postgresql"), types.StringValue("basic")
	return m
}

// testInstanceState returns the state that a create of
// testInstanceConfig records for sampleInstance.
func testInstanceState(t *testing.T) InstanceResourceModel {
	t.Helper()
	m := testInstanceConfig()
	m.DBVersion = selector.WithID(m.DBVersion, types.StringValue(testVersionID.String()))
	m.Flavor = selector.WithID(m.Flavor, types.StringValue(testFlavorID.String()))
	m.VolumeType = selector.WithID(m.VolumeType, types.StringValue(testVolumeTypeID.String()))
	if diags := populateInstanceResourceState(sampleInstance(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return m
}

func instancePlan(t *testing.T, m InstanceResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: instanceSchema(t)}
	m.RootPassword = types.StringNull()
	if diags := plan.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return plan
}

func instanceState(t *testing.T, m InstanceResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: instanceSchema(t)}
	m.RootPassword = types.StringNull()
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func instanceConfig(t *testing.T, m InstanceResourceModel) tfsdk.Config {
	t.Helper()
	state := tfsdk.State{Schema: instanceSchema(t)}
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return tfsdk.Config(state)
}

func backupState(t *testing.T, m BackupResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: backupSchema(t)}
	if diags := state.Set(context.Background(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	return state
}

func backupPlan(t *testing.T, m BackupResourceModel) tfsdk.Plan {
	t.Helper()
	state := backupState(t, m)
	return tfsdk.Plan(state)
}

func backupPolicyObject(enabled bool, interval, day string, startHour, maxBackups int64) types.Object {
	values := map[string]attr.Value{
		"enabled": types.BoolValue(enabled), "backup_interval": types.StringNull(), "day_of_week": types.StringNull(),
		"start_hour": types.Int64Null(), "max_auto_backup": types.Int64Null(),
	}
	if interval != "" {
		values["backup_interval"] = types.StringValue(interval)
	}
	if day != "" {
		values["day_of_week"] = types.StringValue(day)
	}
	if startHour >= 0 {
		values["start_hour"] = types.Int64Value(startHour)
	}
	if maxBackups >= 0 {
		values["max_auto_backup"] = types.Int64Value(maxBackups)
	}
	return types.ObjectValueMust(backupPolicyAttributeTypes(), values)
}

func autoIncreaseObject(enabled bool, threshold, limit int64) types.Object {
	values := map[string]attr.Value{"enabled": types.BoolValue(enabled), "threshold": types.Int64Null(), "limit": types.Int64Null()}
	if threshold >= 0 {
		values["threshold"] = types.Int64Value(threshold)
	}
	if limit >= 0 {
		values["limit"] = types.Int64Value(limit)
	}
	return types.ObjectValueMust(autoIncreaseAttributeTypes(), values)
}

// nullRaw is the null value of the DB instance resource, the prior state of a
// create.
func nullRaw(t *testing.T) tftypes.Value {
	t.Helper()
	return tftypes.NewValue(instanceSchema(t).Type().TerraformType(context.Background()), nil)
}

// withAttribute returns object with one attribute replaced.
func withAttribute(object types.Object, name string, value attr.Value) types.Object {
	values := object.Attributes()
	values[name] = value
	return types.ObjectValueMust(object.AttributeTypes(context.Background()), values)
}

// testSubnet is the subnet of the test DB instances, in the given region.
func testSubnet(regionID core.UUID) networksdk.SubnetSchema {
	created := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	return networksdk.SubnetSchema{
		Id: testSubnetID, Name: "db-subnet", Cidr: "10.0.1.0/24", CreatedAt: created, UpdatedAt: created,
		Region: networksdk.NestedRegionSchema{Id: regionID, Name: "hn-1"},
		Vpc:    networksdk.NestedVPCSchema{Id: testVPCID, Name: "db-vpc"},
	}
}

// planChange runs the plan modifiers of one top-level attribute for a change
// from prior to planned, and reports the resulting plan value and whether the
// change replaces the resource. Full state and plan values are needed because
// RequiresReplace ignores creates and destroys.
func planChange(t *testing.T, attribute schema.Attribute, p path.Path, full tfsdk.State, prior, planned, config attr.Value) (attr.Value, bool) {
	t.Helper()
	ctx := context.Background()
	plan := tfsdk.Plan(full)
	cfg := tfsdk.Config(full)
	replace := false
	switch a := attribute.(type) {
	case schema.StringAttribute:
		value := planned.(types.String)
		for _, m := range a.PlanModifiers {
			resp := planmodifier.StringResponse{PlanValue: value}
			m.PlanModifyString(ctx, planmodifier.StringRequest{Path: p, State: full, Plan: plan, Config: cfg, StateValue: prior.(types.String), PlanValue: value, ConfigValue: config.(types.String)}, &resp)
			value, replace = resp.PlanValue, replace || resp.RequiresReplace
		}
		return value, replace
	case schema.Int64Attribute:
		value := planned.(types.Int64)
		for _, m := range a.PlanModifiers {
			resp := planmodifier.Int64Response{PlanValue: value}
			m.PlanModifyInt64(ctx, planmodifier.Int64Request{Path: p, State: full, Plan: plan, Config: cfg, StateValue: prior.(types.Int64), PlanValue: value, ConfigValue: config.(types.Int64)}, &resp)
			value, replace = resp.PlanValue, replace || resp.RequiresReplace
		}
		return value, replace
	case schema.BoolAttribute:
		value := planned.(types.Bool)
		for _, m := range a.PlanModifiers {
			resp := planmodifier.BoolResponse{PlanValue: value}
			m.PlanModifyBool(ctx, planmodifier.BoolRequest{Path: p, State: full, Plan: plan, Config: cfg, StateValue: prior.(types.Bool), PlanValue: value, ConfigValue: config.(types.Bool)}, &resp)
			value, replace = resp.PlanValue, replace || resp.RequiresReplace
		}
		return value, replace
	case schema.SingleNestedAttribute:
		value := planned.(types.Object)
		for _, m := range a.PlanModifiers {
			resp := planmodifier.ObjectResponse{PlanValue: value}
			m.PlanModifyObject(ctx, planmodifier.ObjectRequest{Path: p, State: full, Plan: plan, Config: cfg, StateValue: prior.(types.Object), PlanValue: value, ConfigValue: config.(types.Object)}, &resp)
			value, replace = resp.PlanValue, replace || resp.RequiresReplace
		}
		return value, replace
	}
	t.Fatalf("%s: unsupported attribute type %T", p, attribute)
	return nil, false
}

// assertInPlaceContract checks that a change to each configurable attribute
// updates in place exactly when the design lists the attribute as mutable.
// Every configurable attribute needs a case in changes, so that a new
// attribute cannot slip in without a decision.
func assertInPlaceContract(t *testing.T, attributes map[string]schema.Attribute, full tfsdk.State, inPlace, decidedElsewhere map[string]bool, changes map[string][2]attr.Value) {
	t.Helper()
	for name, attribute := range attributes {
		if !attribute.IsRequired() && !attribute.IsOptional() || decidedElsewhere[name] {
			continue
		}
		change, ok := changes[name]
		if !ok {
			t.Errorf("%s is configurable but has no update-or-replace case", name)
			continue
		}
		_, replace := planChange(t, attribute, path.Root(name), full, change[0], change[1], change[1])
		switch {
		case inPlace[name] && replace:
			t.Errorf("%s: the design updates it in place, but a change replaces the resource", name)
		case !inPlace[name] && !replace:
			t.Errorf("%s: the design does not update it in place, but a change plans an in-place update", name)
		}
	}
}

// assertIDReferenceReplacement checks that an ID reference replaces the
// resource for another or unknown ID, and not for a change of letter case or
// outer whitespace.
func assertIDReferenceReplacement(t *testing.T, attribute schema.Attribute, p path.Path, full tfsdk.State, id string) {
	t.Helper()
	value := func(id types.String) types.Object { return reference.Value(reference.ID, id) }
	prior := value(types.StringValue(id))
	for _, tt := range []struct {
		name    string
		planned types.Object
		replace bool
	}{
		{"same ID", prior, false},
		{"letter case only", value(types.StringValue(strings.ToUpper(id))), false},
		{"outer whitespace only", value(types.StringValue("  " + id + "\n")), false},
		{"another ID", value(types.StringValue(testOtherID.String())), true},
		{"unknown ID", value(types.StringUnknown()), true},
	} {
		if _, replace := planChange(t, attribute, p, full, prior, tt.planned, tt.planned); replace != tt.replace {
			t.Errorf("%s: replace = %v, want %v", tt.name, replace, tt.replace)
		}
	}
}
