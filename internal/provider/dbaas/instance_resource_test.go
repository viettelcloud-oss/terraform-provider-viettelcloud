package dbaas

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	dbaasselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
)

func TestInstanceResourceSchema(t *testing.T) {
	t.Parallel()
	attrs := instanceSchema(t).Attributes
	for _, name := range []string{"name", "database_type", "solution"} {
		attribute := attrs[name].(schema.StringAttribute)
		if !attribute.Required || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must be Required and replace the instance", name)
		}
	}
	for _, name := range []string{"db_version", "flavor", "volume_type"} {
		attribute := attrs[name].(schema.SingleNestedAttribute)
		if !attribute.Required || len(attribute.PlanModifiers) != 0 || !attribute.Attributes["id"].IsComputed() {
			t.Fatalf("%s must be a Required selector that ModifyPlan replaces from its computed id", name)
		}
	}
	for _, name := range []string{"region", "subnet"} {
		if attribute := attrs[name].(schema.SingleNestedAttribute); !attribute.Required || len(attribute.PlanModifiers) != 1 {
			t.Fatalf("%s must be a Required reference that replaces the instance", name)
		}
	}
	diskSize := attrs["disk_size"].(schema.Int64Attribute)
	if !diskSize.Required || len(diskSize.PlanModifiers) != 1 {
		t.Fatal("disk_size must be Required and replace the instance")
	}
	if current := attrs["current_disk_size"].(schema.Int64Attribute); !current.Computed || current.Optional || len(current.PlanModifiers) != 0 {
		t.Fatal("current_disk_size must be Computed without UseStateForUnknown, because auto-increase grows it at any time")
	}
	password := attrs["root_password"].(schema.StringAttribute)
	if !password.Required || !password.WriteOnly || !password.Sensitive {
		t.Fatal("root_password must be Required, WriteOnly, and Sensitive")
	}
	for _, name := range []string{"description", "keep_backup", "enable_public_access", "backup_policy", "auto_increase_volume_size"} {
		if attribute := attrs[name]; !attribute.IsOptional() || !attribute.IsComputed() {
			t.Fatalf("%s must be Optional and Computed", name)
		}
	}
	if modifiers := attrs["enable_public_access"].(schema.BoolAttribute).PlanModifiers; len(modifiers) != 2 {
		t.Fatal("enable_public_access must pair UseStateForUnknown with RequiresReplaceIfConfigured")
	}
	if attrs["maintenance_config"].IsOptional() || !attrs["maintenance_config"].IsComputed() {
		t.Fatal("maintenance_config must be Computed only")
	}
	if diags := instanceState(t, testInstanceState(t)).Raw.IsNull(); diags {
		t.Fatal("model/schema mismatch")
	}
}

func TestInstanceResourceTypeName(t *testing.T) {
	t.Parallel()
	var resp resource.MetadataResponse
	NewInstanceResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &resp)
	if resp.TypeName != "viettelcloud_db_instance" {
		t.Fatalf("type = %q, want viettelcloud_db_instance", resp.TypeName)
	}
}

// The design updates only description, keep_backup, backup_policy, and
// auto_increase_volume_size in place; every other configurable attribute
// replaces the instance.
func TestInstanceResourceUpdatesInPlaceOnlyDesignFields(t *testing.T) {
	t.Parallel()
	attributes := instanceSchema(t).Attributes
	if password := attributes["root_password"].(schema.StringAttribute); !password.WriteOnly {
		t.Fatal("root_password must be write-only, so that changing it plans no update")
	}
	region := func(name string) types.Object { return reference.Value(reference.Name, types.StringValue(name)) }
	subnet := func(id string) types.Object { return reference.Value(reference.ID, types.StringValue(id)) }
	assertInPlaceContract(t, attributes, instanceState(t, testInstanceState(t)),
		map[string]bool{"description": true, "keep_backup": true, "backup_policy": true, "auto_increase_volume_size": true},
		// ModifyPlan decides the selectors; TestInstanceModifyPlanSelectors covers them.
		map[string]bool{"root_password": true, "db_version": true, "flavor": true, "volume_type": true},
		map[string][2]attr.Value{
			"name":                      {types.StringValue("orders"), types.StringValue("orders-2")},
			"database_type":             {types.StringValue("postgresql"), types.StringValue("mysql")},
			"solution":                  {types.StringValue("basic"), types.StringValue("business")},
			"disk_size":                 {types.Int64Value(20), types.Int64Value(30)},
			"region":                    {region("hn-1"), region("hcm-1")},
			"subnet":                    {subnet(testSubnetID.String()), subnet(testOtherID.String())},
			"enable_public_access":      {types.BoolValue(false), types.BoolValue(true)},
			"description":               {types.StringValue("orders db"), types.StringValue("")},
			"keep_backup":               {types.BoolValue(true), types.BoolValue(false)},
			"backup_policy":             {backupPolicyObject(false, "", "", -1, -1), backupPolicyObject(true, "daily", "", 1, 7)},
			"auto_increase_volume_size": {autoIncreaseObject(false, -1, -1), autoIncreaseObject(true, 80, 100)},
		})
}

func TestInstanceEnablePublicAccessReplacesOnlyWhenConfigured(t *testing.T) {
	t.Parallel()
	full := instanceState(t, testInstanceState(t))
	attribute, p := instanceSchema(t).Attributes["enable_public_access"], path.Root("enable_public_access")
	planned, replace := planChange(t, attribute, p, full, types.BoolValue(true), types.BoolUnknown(), types.BoolNull())
	if replace || !planned.Equal(types.BoolValue(true)) {
		t.Fatalf("omitting enable_public_access must keep %v without replacement, got %v replace=%v", true, planned, replace)
	}
	if _, replace := planChange(t, attribute, p, full, types.BoolValue(true), types.BoolValue(false), types.BoolValue(false)); !replace {
		t.Fatal("configuring a different enable_public_access must replace the instance")
	}
	if _, replace := planChange(t, attribute, p, full, types.BoolValue(true), types.BoolValue(true), types.BoolValue(true)); replace {
		t.Fatal("configuring the current enable_public_access must not replace the instance")
	}
}

// A letter-case change of the subnet ID names the same subnet, so it must not
// replace the instance and destroy its data.
func TestInstanceSubnetReplacesOnlyForAnotherSubnet(t *testing.T) {
	t.Parallel()
	assertIDReferenceReplacement(t, instanceSchema(t).Attributes["subnet"], path.Root("subnet"), instanceState(t, testInstanceState(t)), testSubnetID.String())
}

func TestInstanceCaseOnlySubnetChangeUpdatesStateWithoutPatch(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusOK, sampleInstance() })
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := testInstanceState(t)
	plan := plannedUpdate(state)
	plan.DBVersion, plan.Flavor, plan.VolumeType = state.DBVersion, state.Flavor, state.VolumeType
	upper := strings.ToUpper(testSubnetID.String())
	plan.Subnet = reference.Value(reference.ID, types.StringValue(upper))
	config := plan
	config.KeepBackup = types.BoolValue(false)
	if resp, _ := modifyPlan(t, r, plan, config, &state); resp.Diagnostics.HasError() {
		t.Fatalf("a letter-case change must not count as a create: %v", resp.Diagnostics)
	}
	resp := resource.UpdateResponse{State: instanceState(t, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: instancePlan(t, plan), State: instanceState(t, state)}, &resp)
	var updated InstanceResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &updated)...)
	if resp.Diagnostics.HasError() || reference.Get(updated.Subnet, reference.ID).ValueString() != upper {
		t.Fatalf("state must keep the configured spelling: %v %v", updated.Subnet, resp.Diagnostics)
	}
	if len(api.requestsTo(http.MethodPatch, "/")) != 0 {
		t.Fatal("a letter-case change must not send a PATCH")
	}
}

// A change of outer whitespace names the same value, so it must neither
// replace the instance nor send a PATCH, and state keeps the new spelling.
func TestInstancePaddedChangeUpdatesStateWithoutPatch(t *testing.T) {
	t.Parallel()
	attributes, full := instanceSchema(t).Attributes, instanceState(t, testInstanceState(t))
	region := func(name string) types.Object { return reference.Value(reference.Name, types.StringValue(name)) }
	for name, change := range map[string][2]attr.Value{
		"name":          {types.StringValue("orders"), types.StringValue(" orders ")},
		"database_type": {types.StringValue("postgresql"), types.StringValue(" postgresql")},
		"solution":      {types.StringValue("basic"), types.StringValue("basic\t")},
		"region":        {region("hn-1"), region(" hn-1 ")},
	} {
		if _, replace := planChange(t, attributes[name], path.Root(name), full, change[0], change[1], change[1]); replace {
			t.Errorf("%s: a change of outer whitespace must not replace the instance", name)
		}
	}

	api, client, project := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusOK, weeklyInstance() })
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := testInstanceState(t)
	_ = populateInstanceResourceState(weeklyInstance(), &state)
	plan := plannedUpdate(state)
	plan.DBVersion, plan.Flavor, plan.VolumeType = state.DBVersion, state.Flavor, state.VolumeType
	plan.Name, plan.DatabaseType, plan.Solution = types.StringValue(" orders "), types.StringValue(" postgresql"), types.StringValue("basic\t")
	plan.Description, plan.Region = types.StringValue(" orders db "), region(" hn-1 ")
	plan.BackupPolicy = backupPolicyObject(true, " weekly ", " monday", 2, 7)
	config := plan
	config.KeepBackup = types.BoolValue(false)
	if resp, _ := modifyPlan(t, r, plan, config, &state); resp.Diagnostics.HasError() || len(resp.RequiresReplace) != 0 {
		t.Fatalf("a change of outer whitespace must not count as a create: %v %v", resp.RequiresReplace, resp.Diagnostics)
	}
	resp := resource.UpdateResponse{State: instanceState(t, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: instancePlan(t, plan), State: instanceState(t, state)}, &resp)
	var updated InstanceResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &updated)...)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if len(api.requestsTo(http.MethodPatch, "/")) != 0 {
		t.Fatal("a change of outer whitespace must not send a PATCH")
	}
	assertPaddedInstanceState(t, updated, plan)
}

func TestBuildInstanceCreateBody(t *testing.T) {
	t.Parallel()
	api, client, _ := newFakeAPI(t, func(req apiRequest) (int, any) {
		if status, body, ok := catalog(req); ok {
			return status, body
		}
		t.Errorf("unexpected request %s", req)
		return http.StatusInternalServerError, nil
	})
	ctx := context.Background()

	plan := testInstanceConfig()
	plan.Description, plan.KeepBackup, plan.EnablePublicAccess = types.StringUnknown(), types.BoolUnknown(), types.BoolUnknown()
	plan.BackupPolicy, plan.AutoIncreaseVolumeSize = types.ObjectUnknown(backupPolicyAttributeTypes()), types.ObjectUnknown(autoIncreaseAttributeTypes())
	body, diags := buildInstanceCreateBody(ctx, &plan, types.StringValue("Secret#123"), testRegionID, client)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if body.DbVersionId != testVersionID || body.FlavorId != testFlavorID || body.VolumeTypeId != testVolumeTypeID || body.RegionId != testRegionID || body.SubnetId != testSubnetID {
		t.Fatalf("catalog and references not resolved: %#v", body)
	}
	if body.Description != nil || body.EnablePublicAccess != nil || body.BackupPolicy != nil || body.RootPassword == nil || *body.RootPassword != "Secret#123" {
		t.Fatalf("unknown values must stay out of the request and root_password must be sent: %#v", body)
	}
	for name, value := range map[string]types.Object{"db_version": plan.DBVersion, "flavor": plan.Flavor, "volume_type": plan.VolumeType} {
		if id, ok := selector.ID(value); !ok || id.String() == "" {
			t.Fatalf("%s.id was not recorded before the request: %v", name, value)
		}
	}
	if versions := api.requestsTo(http.MethodGet, "/v2/dbaas/db-versions/"); len(versions) != 1 || versions[0].Query["database_type"][0] != "postgresql" || versions[0].Query["region_id"][0] != testRegionID.String() {
		t.Fatalf("DB versions must be filtered by database_type and region_id: %v", versions)
	}

	plan = testInstanceConfig()
	plan.Description, plan.EnablePublicAccess = types.StringValue(""), types.BoolValue(true)
	plan.BackupPolicy = backupPolicyObject(true, "weekly", "monday", 2, 7)
	body, diags = buildInstanceCreateBody(ctx, &plan, types.StringValue("Secret#123"), testRegionID, client)
	if diags.HasError() {
		t.Fatal(diags)
	}
	raw, err := body.BackupPolicy.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); !strings.Contains(got, `"enable_auto_backup":true`) || !strings.Contains(got, `"day_of_week":"monday"`) || !strings.Contains(got, `"backup_interval":"weekly"`) {
		t.Fatalf("weekly policy must send a boolean discriminator and the day: %s", got)
	}
	if body.Description == nil || *body.Description != "" || body.EnablePublicAccess == nil || !*body.EnablePublicAccess {
		t.Fatalf("explicit empty description and public access must be sent: %#v", body)
	}

	plan = testInstanceConfig()
	plan.BackupPolicy = backupPolicyObject(true, "daily", "", 2, 7)
	plan.BackupPolicy = withAttribute(plan.BackupPolicy, "day_of_week", types.StringUnknown())
	body, _ = buildInstanceCreateBody(ctx, &plan, types.StringValue("Secret#123"), testRegionID, client)
	if raw, _ := body.BackupPolicy.MarshalJSON(); strings.Contains(string(raw), "day_of_week") {
		t.Fatalf("a daily policy must not send day_of_week: %s", raw)
	}
	plan.BackupPolicy = backupPolicyObject(false, "", "", -1, -1)
	body, _ = buildInstanceCreateBody(ctx, &plan, types.StringValue("Secret#123"), testRegionID, client)
	if raw, _ := body.BackupPolicy.MarshalJSON(); string(raw) != `{"enable_auto_backup":false}` {
		t.Fatalf("a disabled policy must send only the flag: %s", raw)
	}
}

func TestInstanceCreateWaitsForActiveWithoutPatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		polls := 0
		api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
			if status, body, ok := catalog(req); ok {
				return status, body
			}
			instance := sampleInstance()
			instance.DiskSize = 25
			switch {
			case req.Method == http.MethodPost && req.Path == "/v2/dbaas/db-instances/":
				instance.Status = dbaassdk.DBInstanceStatusCREATING
				return http.StatusCreated, instance
			case req.Method == http.MethodGet && req.Path == "/v2/dbaas/db-instances/"+testInstanceID.String()+"/":
				if polls++; polls == 1 {
					instance.Status = dbaassdk.DBInstanceStatusCREATING
				}
				return http.StatusOK, instance
			}
			t.Errorf("unexpected request %s", req)
			return http.StatusInternalServerError, nil
		})
		r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
		config := testInstanceConfig()
		plan := config
		plan.Description, plan.KeepBackup, plan.EnablePublicAccess = types.StringUnknown(), types.BoolUnknown(), types.BoolUnknown()
		resp := resource.CreateResponse{State: tfsdk.State{Schema: instanceSchema(t)}}
		start := time.Now()
		r.Create(context.Background(), resource.CreateRequest{Plan: instancePlan(t, plan), Config: instanceConfig(t, config)}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		if elapsed := time.Since(start); elapsed != instancePollInterval {
			t.Fatalf("create waited %s, want one poll interval", elapsed)
		}
		if len(api.requestsTo(http.MethodPatch, "/")) != 0 {
			t.Fatal("create must not send a PATCH")
		}
		posts := api.requestsTo(http.MethodPost, "/v2/dbaas/db-instances/")
		if len(posts) != 1 || posts[0].ProjectID != testProjectID.String() || posts[0].Body["root_password"] != "Secret#123" {
			t.Fatalf("create request: %#v", posts)
		}
		for _, field := range []string{"description", "enable_public_access", "backup_policy", "keep_backup", "auto_increase_volume_size_config", "m_dow"} {
			if _, ok := posts[0].Body[field]; ok {
				t.Fatalf("unconfigured %s must not be sent: %s", field, posts[0].RawBody)
			}
		}
		var state InstanceResourceModel
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
		if state.ID.ValueString() != testInstanceID.String() || state.Status.ValueString() != "ACTIVE" || !state.RootPassword.IsNull() ||
			state.DiskSize.ValueInt64() != 20 || state.CurrentDiskSize.ValueInt64() != 25 || !state.KeepBackup.ValueBool() {
			t.Fatalf("create state: %#v", state)
		}
	})
}

// weeklyInstance is sampleInstance with a weekly automatic backup on monday.
func weeklyInstance() *dbaassdk.DBInstanceSchema {
	instance := sampleInstance()
	day := dbaassdk.DayOfWeekMonday
	instance.EnableAutoBackup = true
	instance.AutoBackupConfig = core.NullableFromPtr(&dbaassdk.NestedAutoBackupConfigSchema{
		BackupInterval: dbaassdk.BackupIntervalWeekly, DayOfWeek: core.NullableFromPtr(&day), StartHour: 2, MaxAutoBackup: 7,
	})
	return instance
}

// assertPaddedInstanceState checks that state keeps the configured spelling
// of every user-supplied string of want.
func assertPaddedInstanceState(t *testing.T, got, want InstanceResourceModel) {
	t.Helper()
	for name, pair := range map[string][2]types.String{
		"name":            {got.Name, want.Name},
		"description":     {got.Description, want.Description},
		"database_type":   {got.DatabaseType, want.DatabaseType},
		"solution":        {got.Solution, want.Solution},
		"region.name":     {reference.Get(got.Region, reference.Name), reference.Get(want.Region, reference.Name)},
		"subnet.id":       {reference.Get(got.Subnet, reference.ID), reference.Get(want.Subnet, reference.ID)},
		"backup_interval": {got.BackupPolicy.Attributes()["backup_interval"].(types.String), want.BackupPolicy.Attributes()["backup_interval"].(types.String)},
		"day_of_week":     {got.BackupPolicy.Attributes()["day_of_week"].(types.String), want.BackupPolicy.Attributes()["day_of_week"].(types.String)},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Errorf("%s = %v, want the configured spelling %v", name, pair[0], pair[1])
		}
	}
}

func TestInstanceCreateTrimsRequestAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		if status, body, ok := catalog(req); ok {
			return status, body
		}
		if req.Method == http.MethodPost {
			return http.StatusCreated, weeklyInstance()
		}
		return http.StatusOK, weeklyInstance()
	})
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	config := testInstanceConfig()
	config.Name, config.Description = types.StringValue(" orders "), types.StringValue(" orders db ")
	config.DatabaseType, config.Solution = types.StringValue(" postgresql "), types.StringValue(" basic ")
	config.Region = reference.Value(reference.Name, types.StringValue(" hn-1 "))
	config.Subnet = reference.Value(reference.ID, types.StringValue("  "+testSubnetID.String()+" "))
	config.BackupPolicy = backupPolicyObject(true, " weekly ", " monday ", 2, 7)
	config.RootPassword = types.StringValue(" Secret#123 ")
	plan := config
	plan.KeepBackup, plan.EnablePublicAccess = types.BoolUnknown(), types.BoolUnknown()
	ctx := context.Background()
	created := resource.CreateResponse{State: tfsdk.State{Schema: instanceSchema(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: instancePlan(t, plan), Config: instanceConfig(t, config)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	posts := api.requestsTo(http.MethodPost, "/v2/dbaas/db-instances/")
	if len(posts) != 1 {
		t.Fatalf("create requests: %v", posts)
	}
	body := posts[0].Body
	policy, _ := body["backup_policy"].(map[string]any)
	if body["name"] != "orders" || body["description"] != "orders db" || body["database_type"] != "postgresql" || body["solution"] != "basic" ||
		body["subnet_id"] != testSubnetID.String() || policy["backup_interval"] != "weekly" || policy["day_of_week"] != "monday" {
		t.Fatalf("the request must carry trimmed values: %s", posts[0].RawBody)
	}
	if body["root_password"] != " Secret#123 " {
		t.Fatalf("root_password must be sent as configured: %v", body["root_password"])
	}
	if regions := api.requestsTo(http.MethodGet, "/v2/projects/"); len(regions) != 1 || regions[0].Query["name"][0] != "hn-1" {
		t.Fatalf("the region lookup must filter by the trimmed name: %v", regions)
	}
	if versions := api.requestsTo(http.MethodGet, "/v2/dbaas/db-versions/"); len(versions) != 1 || versions[0].Query["database_type"][0] != "postgresql" {
		t.Fatalf("the DB version lookup must filter by the trimmed database_type: %v", versions)
	}
	var state InstanceResourceModel
	created.Diagnostics.Append(created.State.Get(ctx, &state)...)
	assertPaddedInstanceState(t, state, config)

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	read.Diagnostics.Append(read.State.Get(ctx, &state)...)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	assertPaddedInstanceState(t, state, config)
}

func TestInstanceCreateWaitFailurePreservesID(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  dbaassdk.DBInstanceStatus
		elapsed time.Duration
	}{
		{"error", dbaassdk.DBInstanceStatusERROR, 0},
		{"abnormal", dbaassdk.DBInstanceStatusABNORMAL, 0},
		{"timeout", dbaassdk.DBInstanceStatusCREATING, instanceCreateTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
					if status, body, ok := catalog(req); ok {
						return status, body
					}
					instance := sampleInstance()
					instance.Status, instance.ErrorMessage = tt.status, "core rejected the instance"
					if req.Method == http.MethodPost {
						instance.Status = dbaassdk.DBInstanceStatusCREATING
						return http.StatusCreated, instance
					}
					return http.StatusOK, instance
				})
				r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
				resp := resource.CreateResponse{State: tfsdk.State{Schema: instanceSchema(t)}}
				start := time.Now()
				r.Create(context.Background(), resource.CreateRequest{Plan: instancePlan(t, testInstanceConfig()), Config: instanceConfig(t, testInstanceConfig())}, &resp)
				if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), testInstanceID.String()) {
					t.Fatalf("the failure must name the preserved instance: %v", resp.Diagnostics)
				}
				if tt.elapsed == 0 && !strings.Contains(resp.Diagnostics[0].Detail(), "core rejected the instance") {
					t.Fatalf("a terminal status must report error_message: %v", resp.Diagnostics)
				}
				if elapsed := time.Since(start); elapsed != tt.elapsed {
					t.Fatalf("waited %s, want %s", elapsed, tt.elapsed)
				}
				var state InstanceResourceModel
				resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
				if state.ID.ValueString() != testInstanceID.String() {
					t.Fatalf("the instance ID must stay in state: %#v", state.ID)
				}
			})
		})
	}
}

// modifyPlan runs ModifyPlan for a planned model against a prior state.
func modifyPlan(t *testing.T, r *InstanceResource, plan, config InstanceResourceModel, state *InstanceResourceModel) (resource.ModifyPlanResponse, InstanceResourceModel) {
	t.Helper()
	req := resource.ModifyPlanRequest{Plan: instancePlan(t, plan), Config: instanceConfig(t, config), State: tfsdk.State{Schema: instanceSchema(t), Raw: nullRaw(t)}}
	if state != nil {
		req.State = instanceState(t, *state)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	var planned InstanceResourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.Plan.Get(context.Background(), &planned)...)
	}
	return resp, planned
}

// plannedUpdate mirrors the framework plan of an update: the selector IDs are
// unknown because configuration leaves them null.
func plannedUpdate(state InstanceResourceModel) InstanceResourceModel {
	plan := state
	plan.DBVersion = selector.WithID(plan.DBVersion, types.StringUnknown())
	plan.Flavor = selector.WithID(plan.Flavor, types.StringUnknown())
	plan.VolumeType = selector.WithID(plan.VolumeType, types.StringUnknown())
	plan.CurrentDiskSize, plan.Status, plan.UpdatedAt = types.Int64Unknown(), types.StringUnknown(), types.StringUnknown()
	return plan
}

func TestInstanceModifyPlanSelectors(t *testing.T) {
	t.Parallel()
	state := testInstanceState(t)
	for _, tt := range []struct {
		name         string
		change       func(*InstanceResourceModel)
		wantReplace  []string
		wantFlavorID types.String
		wantCatalog  bool
	}{
		{"in-place description", func(m *InstanceResourceModel) { m.Description = types.StringValue("new") }, nil, types.StringValue(testFlavorID.String()), false},
		// A single exact-name criterion can only change without selecting another
		// entry by padding, which selector.Equal ignores without a catalog read.
		{"padded criterion keeps the current entry", func(m *InstanceResourceModel) {
			m.Flavor = testSelector(dbaasselector.FlavorAttributeTypes(), "name", " db.small ", types.StringUnknown())
		}, nil, types.StringValue(testFlavorID.String()), false},
		{"criteria select another entry", func(m *InstanceResourceModel) {
			m.Flavor = testSelector(dbaasselector.FlavorAttributeTypes(), "name", "db.large", types.StringUnknown())
		}, []string{"flavor"}, types.StringUnknown(), true},
		{"padded region and database type keep every selector", func(m *InstanceResourceModel) {
			m.Region = reference.Value(reference.Name, types.StringValue(" hn-1 "))
			m.DatabaseType = types.StringValue("postgresql ")
		}, nil, types.StringValue(testFlavorID.String()), false},
		{"region change replaces every selector", func(m *InstanceResourceModel) {
			m.Region = reference.Value(reference.Name, types.StringValue("hcm-1"))
		}, []string{"db_version", "flavor", "volume_type"}, types.StringUnknown(), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
				if status, body, ok := catalog(req); ok {
					return status, body
				}
				return http.StatusOK, sampleInstance()
			})
			plan := plannedUpdate(state)
			tt.change(&plan)
			config := plan
			config.DBVersion = selector.WithID(plan.DBVersion, types.StringNull())
			config.Flavor = selector.WithID(plan.Flavor, types.StringNull())
			config.VolumeType = selector.WithID(plan.VolumeType, types.StringNull())
			resp, planned := modifyPlan(t, &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}, plan, config, &state)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var replaced []string
			for _, p := range resp.RequiresReplace {
				replaced = append(replaced, p.String())
			}
			if strings.Join(replaced, ",") != strings.Join(tt.wantReplace, ",") {
				t.Fatalf("replace = %v, want %v", replaced, tt.wantReplace)
			}
			if got := planned.Flavor.Attributes()["id"]; !got.Equal(tt.wantFlavorID) {
				t.Fatalf("flavor.id = %v, want %v", got, tt.wantFlavorID)
			}
			if got := len(api.requestsTo(http.MethodGet, "/v2/dbaas/dbaas-flavors/")) > 0; got != tt.wantCatalog {
				t.Fatalf("catalog read = %v, want %v", got, tt.wantCatalog)
			}
			if tt.wantReplace == nil {
				if id, _ := selector.ID(planned.DBVersion); id != testVersionID {
					t.Fatalf("an unchanged selector must keep its id: %v", planned.DBVersion)
				}
			}
		})
	}
}

func TestInstanceModifyPlanRejectsNonDefaultValuesAtCreate(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		if status, body, ok := catalog(req); ok {
			return status, body
		}
		return http.StatusOK, sampleInstance()
	})
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := testInstanceState(t)
	keepFalse := func(m *InstanceResourceModel) { m.KeepBackup = types.BoolValue(false) }
	autoOn := func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(true, 80, 100) }
	defaults := func(m *InstanceResourceModel) {
		m.KeepBackup, m.AutoIncreaseVolumeSize = types.BoolValue(true), autoIncreaseObject(false, -1, -1)
	}
	for _, tt := range []struct {
		name      string
		prior     *InstanceResourceModel
		configure func(*InstanceResourceModel)
		wantError bool
	}{
		{"create with keep_backup false", nil, keepFalse, true},
		{"create with auto-increase enabled", nil, autoOn, true},
		{"create with the defaults", nil, defaults, false},
		{"in-place keep_backup false", &state, keepFalse, false},
		{"in-place auto-increase enabled", &state, autoOn, false},
		{"replace by name with keep_backup false", &state, func(m *InstanceResourceModel) {
			keepFalse(m)
			m.Name = types.StringValue("orders-2")
		}, true},
		{"replace by solution with keep_backup false", &state, func(m *InstanceResourceModel) {
			keepFalse(m)
			m.Solution = types.StringValue("business")
		}, true},
		{"replace by subnet with keep_backup false", &state, func(m *InstanceResourceModel) {
			keepFalse(m)
			m.Subnet = reference.Value(reference.ID, types.StringValue(testOtherID.String()))
		}, true},
		{"replace by configured enable_public_access with keep_backup false", &state, func(m *InstanceResourceModel) {
			keepFalse(m)
			m.EnablePublicAccess = types.BoolValue(true)
		}, true},
		{"replace by selector with auto-increase enabled", &state, func(m *InstanceResourceModel) {
			autoOn(m)
			m.Flavor = testSelector(dbaasselector.FlavorAttributeTypes(), "name", "db.large", types.StringUnknown())
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := testInstanceConfig()
			if tt.prior != nil {
				plan = plannedUpdate(*tt.prior)
			}
			tt.configure(&plan)
			config := testInstanceConfig()
			tt.configure(&config)
			config.Flavor = selector.WithID(plan.Flavor, types.StringNull())
			resp, _ := modifyPlan(t, r, plan, config, tt.prior)
			if resp.Diagnostics.HasError() != tt.wantError {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tt.wantError)
			}
			if tt.wantError && !strings.Contains(resp.Diagnostics[0].Detail(), "later apply") {
				t.Fatalf("the error must explain the two-step apply: %v", resp.Diagnostics)
			}
		})
	}
}

func TestInstanceModifyPlanDayOfWeek(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusOK, sampleInstance() })
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := testInstanceState(t)
	state.BackupPolicy = backupPolicyObject(true, "weekly", "monday", 2, 7)
	for _, tt := range []struct {
		name     string
		interval string
		want     types.String
	}{
		{"weekly to daily", "daily", types.StringUnknown()},
		{"interval unchanged", "weekly", types.StringValue("monday")},
		{"interval differs only by whitespace", " weekly", types.StringValue("monday")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := plannedUpdate(state)
			// UseStateForUnknown has already copied the prior day into the plan.
			plan.BackupPolicy = backupPolicyObject(true, tt.interval, "monday", 3, 7)
			config := plan
			config.BackupPolicy = backupPolicyObject(true, tt.interval, "", 3, 7)
			resp, planned := modifyPlan(t, r, plan, config, &state)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if got := planned.BackupPolicy.Attributes()["day_of_week"]; !got.Equal(tt.want) {
				t.Fatalf("day_of_week = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildInstanceUpdateBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state := testInstanceState(t)
	state.BackupPolicy = backupPolicyObject(true, "weekly", "monday", 2, 7)
	state.AutoIncreaseVolumeSize = autoIncreaseObject(true, 80, 100)

	if _, changed, diags := buildInstanceUpdateBody(ctx, plannedUpdate(state), state); changed || diags.HasError() {
		t.Fatalf("no change must skip the PATCH: %v", diags)
	}

	plan := plannedUpdate(state)
	plan.Description, plan.KeepBackup = types.StringValue(""), types.BoolValue(false)
	body, changed, diags := buildInstanceUpdateBody(ctx, plan, state)
	if diags.HasError() || !changed || body.Description == nil || *body.Description != "" || body.KeepBackup == nil || *body.KeepBackup || body.BackupPolicy != nil || body.AutoIncreaseVolumeSizeConfig != nil {
		t.Fatalf("only description and keep_backup changed: %#v %v", body, diags)
	}

	plan = plannedUpdate(state)
	plan.Description = types.StringValue(" " + state.Description.ValueString() + " ")
	plan.BackupPolicy = backupPolicyObject(true, " weekly", "monday ", 2, 7)
	if _, changed, diags := buildInstanceUpdateBody(ctx, plan, state); changed || diags.HasError() {
		t.Fatalf("a change of outer whitespace must skip the PATCH: %v", diags)
	}
	plan.Description = types.StringValue(" reporting db ")
	if body, _, _ := buildInstanceUpdateBody(ctx, plan, state); body.Description == nil || *body.Description != "reporting db" {
		t.Fatalf("a changed description must be sent trimmed: %#v", body.Description)
	}

	plan = plannedUpdate(state)
	plan.BackupPolicy = backupPolicyObject(true, " weekly ", " monday ", 5, 7)
	body, changed, _ = buildInstanceUpdateBody(ctx, plan, state)
	raw, _ := body.BackupPolicy.MarshalJSON()
	if !changed || body.Description != nil || body.KeepBackup != nil || string(raw) != `{"backup_interval":"weekly","day_of_week":"monday","enable_auto_backup":true,"max_auto_backup":7,"start_hour":5}` {
		t.Fatalf("a changed child must send the whole policy: %s", raw)
	}

	plan = plannedUpdate(state)
	plan.BackupPolicy = withAttribute(backupPolicyObject(true, "daily", "", 2, 7), "day_of_week", types.StringUnknown())
	body, _, _ = buildInstanceUpdateBody(ctx, plan, state)
	if raw, _ := body.BackupPolicy.MarshalJSON(); strings.Contains(string(raw), "day_of_week") {
		t.Fatalf("a daily policy must not send day_of_week: %s", raw)
	}

	plan = plannedUpdate(state)
	plan.AutoIncreaseVolumeSize = autoIncreaseObject(false, 80, 100)
	body, changed, _ = buildInstanceUpdateBody(ctx, plan, state)
	raw, _ = body.AutoIncreaseVolumeSizeConfig.MarshalJSON()
	if !changed || body.BackupPolicy != nil || string(raw) != `{"enable_auto_increase_volume_size":false}` {
		t.Fatalf("disabling auto-increase must send only the flag: %s", raw)
	}
	plan.AutoIncreaseVolumeSize = autoIncreaseObject(true, 90, 200)
	body, _, _ = buildInstanceUpdateBody(ctx, plan, state)
	raw, _ = body.AutoIncreaseVolumeSizeConfig.MarshalJSON()
	if string(raw) != `{"auto_increase_limit":200,"enable_auto_increase_volume_size":true,"threshold":90}` {
		t.Fatalf("auto-increase body: %s", raw)
	}
}

func TestInstanceUpdatePatchesThenReads(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		instance := sampleInstance()
		switch req.Method {
		case http.MethodPatch:
			return http.StatusAccepted, nil
		case http.MethodGet:
			instance.Description, instance.DiskSize = "renamed", 40
			return http.StatusOK, instance
		}
		t.Errorf("unexpected request %s", req)
		return http.StatusInternalServerError, nil
	})
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := testInstanceState(t)
	plan := plannedUpdate(state)
	plan.DBVersion, plan.Flavor, plan.VolumeType = state.DBVersion, state.Flavor, state.VolumeType
	plan.Description = types.StringValue("renamed")
	resp := resource.UpdateResponse{State: instanceState(t, state)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: instancePlan(t, plan), State: instanceState(t, state)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	patches := api.requestsTo(http.MethodPatch, "/v2/dbaas/db-instances/"+testInstanceID.String()+"/")
	if len(patches) != 1 || patches[0].RawBody != `{"description":"renamed"}` {
		t.Fatalf("PATCH must carry only the changed field: %#v", patches)
	}
	var updated InstanceResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &updated)...)
	if updated.ID.ValueString() != testInstanceID.String() || updated.Description.ValueString() != "renamed" || updated.CurrentDiskSize.ValueInt64() != 40 || updated.DiskSize.ValueInt64() != 20 {
		t.Fatalf("update state: %#v", updated)
	}
}

func TestPopulateInstanceResourceState(t *testing.T) {
	t.Parallel()
	state := testInstanceState(t)
	instance := sampleInstance()
	if state.DiskSize.ValueInt64() != 20 || state.CurrentDiskSize.ValueInt64() != 20 || state.FlavorVCPUs.ValueInt64() != 2 || state.FlavorRAM.ValueInt64() != 4096 ||
		state.VolumeTypeDiskType.ValueString() != "ssd" || state.SubnetName.ValueString() != "db-subnet" || state.SubnetCIDR.ValueString() != "10.0.1.0/24" ||
		state.VPCID.ValueString() != testVPCID.String() || state.VPCName.ValueString() != "db-vpc" || state.RegionID.ValueString() != testRegionID.String() ||
		state.Port.ValueInt64() != 5432 || state.PrivateURL.ValueString() != "orders.internal" || len(state.AllowedCIDRs.Elements()) != 1 ||
		!state.InitBackupID.IsNull() || state.OperationalStatus.ValueString() != "NOOP" || state.CreatedAt.ValueString() != "2026-10-01T08:00:00Z" {
		t.Fatalf("incomplete mapping: %#v", state)
	}
	if got := state.MaintenanceConfig.Attributes(); got["day_of_week"].(types.String).ValueString() != "sunday" || got["start_hour"].(types.Int64).ValueInt64() != 0 {
		t.Fatalf("maintenance_config: %v", state.MaintenanceConfig)
	}
	policy := state.BackupPolicy.Attributes()
	if policy["enabled"].(types.Bool).ValueBool() || !policy["backup_interval"].IsNull() || !policy["start_hour"].IsNull() {
		t.Fatalf("a null auto_backup_config must leave the children null: %v", state.BackupPolicy)
	}
	if auto := state.AutoIncreaseVolumeSize.Attributes(); auto["enabled"].(types.Bool).ValueBool() || !auto["threshold"].IsNull() || !auto["limit"].IsNull() {
		t.Fatalf("a null auto_increase_volume_size_config must leave the children null: %v", state.AutoIncreaseVolumeSize)
	}

	day := dbaassdk.DayOfWeekFriday
	instance.AutoBackupConfig = core.NullableFromPtr(&dbaassdk.NestedAutoBackupConfigSchema{BackupInterval: dbaassdk.BackupIntervalWeekly, DayOfWeek: core.NullableFromPtr(&day), StartHour: 3, MaxAutoBackup: 9})
	instance.AutoIncreaseVolumeSizeConfig = core.NullableFromPtr(&dbaassdk.NestedAutoIncreaseVolumeSizeConfigSchema{Threshold: 80, AutoIncreaseLimit: 100})
	instance.DiskSize = 50
	instance.Flavor = dbaassdk.NestedDBaaSRegionFlavorSchema{Id: testOtherID, FlavorName: "db.large"}
	backupID := testBackupID
	instance.InitBackupId = core.NullableFromPtr(&backupID)
	if diags := populateInstanceResourceState(instance, &state); diags.HasError() {
		t.Fatal(diags)
	}
	if state.DiskSize.ValueInt64() != 20 || state.CurrentDiskSize.ValueInt64() != 50 {
		t.Fatalf("refresh must keep disk_size and report the grown size: %v %v", state.DiskSize, state.CurrentDiskSize)
	}
	if !state.Flavor.Equal(testSelector(dbaasselector.FlavorAttributeTypes(), "name", "db.large", types.StringValue(testOtherID.String()))) {
		t.Fatalf("a different flavor id must reset the selector: %v", state.Flavor)
	}
	if policy := state.BackupPolicy.Attributes(); policy["day_of_week"].(types.String).ValueString() != "friday" || policy["max_auto_backup"].(types.Int64).ValueInt64() != 9 {
		t.Fatalf("backup_policy: %v", state.BackupPolicy)
	}
	if auto := state.AutoIncreaseVolumeSize.Attributes(); auto["limit"].(types.Int64).ValueInt64() != 100 || state.InitBackupID.ValueString() != testBackupID.String() {
		t.Fatalf("auto_increase_volume_size or init_backup_id: %v", state.AutoIncreaseVolumeSize)
	}

	imported := InstanceResourceModel{
		InstanceModel: nullInstanceModel(),
		DBVersion:     types.ObjectNull(dbaasselector.DBVersionAttributeTypes()), Flavor: types.ObjectNull(dbaasselector.FlavorAttributeTypes()),
		VolumeType: types.ObjectNull(dbaasselector.VolumeTypeAttributeTypes()), Region: types.ObjectNull(reference.AttributeTypes(reference.Name)),
		Subnet: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}
	if diags := populateInstanceResourceState(instance, &imported); diags.HasError() {
		t.Fatal(diags)
	}
	if imported.DiskSize.ValueInt64() != 50 || !imported.DBVersion.Equal(testSelector(dbaasselector.DBVersionAttributeTypes(), "version", "16", types.StringValue(testVersionID.String()))) ||
		reference.Get(imported.Region, reference.Name).ValueString() != "hn-1" || reference.Get(imported.Subnet, reference.ID).ValueString() != testSubnetID.String() {
		t.Fatalf("import must build selectors, references, and disk_size from the response: %#v", imported)
	}

	configured := testInstanceState(t)
	upper := strings.ToUpper(testSubnetID.String())
	configured.Subnet = reference.Value(reference.ID, types.StringValue(upper))
	configured.Region = reference.Value(reference.Name, types.StringValue(" hn-1"))
	configured.Name, configured.Description = types.StringValue(" orders"), types.StringValue("orders db ")
	configured.DatabaseType, configured.Solution = types.StringValue(" postgresql"), types.StringValue("basic ")
	configured.BackupPolicy = backupPolicyObject(true, " weekly", "monday ", 2, 7)
	want := configured
	_ = populateInstanceResourceState(weeklyInstance(), &configured)
	assertPaddedInstanceState(t, configured, want)

	configured.Name, configured.Description = types.StringValue("billing"), types.StringValue("billing db")
	_ = populateInstanceResourceState(sampleInstance(), &configured)
	if configured.Name.ValueString() != "orders" || configured.Description.ValueString() != "orders db" {
		t.Fatalf("a different backend value must replace the configured one: %v %v", configured.Name, configured.Description)
	}
}

func TestInstanceReadAndDeleteNotFound(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(apiRequest) (int, any) { return http.StatusNotFound, apiError("not_found", "Not found") })
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := instanceState(t, testInstanceState(t))
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("a missing instance must leave state: %v", read.Diagnostics)
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatalf("deleting a missing instance must succeed: %v", deleted.Diagnostics)
	}
}

func TestInstanceDeleteRejectedReportsBackendMessage(t *testing.T) {
	t.Parallel()
	for _, message := range []string{"DB instance is busy with another operation.", "DB instance in ABNORMAL status cannot be deleted."} {
		api, client, project := newFakeAPI(t, func(apiRequest) (int, any) {
			return http.StatusConflict, apiError("instance_not_deletable", message)
		})
		r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
		state := instanceState(t, testInstanceState(t))
		deleted := resource.DeleteResponse{State: state}
		r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
		if !deleted.Diagnostics.HasError() || !strings.Contains(deleted.Diagnostics[0].Detail(), message) {
			t.Fatalf("the backend message must be reported: %v", deleted.Diagnostics)
		}
		if len(api.requests) != 1 {
			t.Fatalf("a rejected delete must not be retried: %v", api.requests)
		}
	}
}

func TestInstanceDeleteWaiter(t *testing.T) {
	for _, tt := range []struct {
		name      string
		second    dbaassdk.DBInstanceOperationalStatus
		wantError bool
		elapsed   time.Duration
	}{
		{"removed", "", false, instancePollInterval},
		{"failed", dbaassdk.DBInstanceOperationalStatusFAILED, true, instancePollInterval},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				polls := 0
				api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
					if req.Method == http.MethodDelete {
						return http.StatusAccepted, nil
					}
					instance := sampleInstance()
					instance.OperationalStatus, instance.ErrorMessage = dbaassdk.DBInstanceOperationalStatusDELETING, "core delete failed"
					if polls++; polls == 2 {
						if tt.second == "" {
							return http.StatusNotFound, apiError("not_found", "Not found")
						}
						instance.OperationalStatus = tt.second
					}
					return http.StatusOK, instance
				})
				r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
				state := instanceState(t, testInstanceState(t))
				deleted := resource.DeleteResponse{State: state}
				start := time.Now()
				r.Delete(context.Background(), resource.DeleteRequest{State: state}, &deleted)
				if deleted.Diagnostics.HasError() != tt.wantError || time.Since(start) != tt.elapsed {
					t.Fatalf("diagnostics %v after %s", deleted.Diagnostics, time.Since(start))
				}
				if tt.wantError && !strings.Contains(deleted.Diagnostics[0].Detail(), "core delete failed") {
					t.Fatalf("a failed delete must report error_message: %v", deleted.Diagnostics)
				}
			})
		})
	}
}

func TestInstanceValidateConfig(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		change    func(*InstanceResourceModel)
		wantError string
	}{
		{"required only", func(*InstanceResourceModel) {}, ""},
		{"empty description", func(m *InstanceResourceModel) { m.Description = types.StringValue("") }, ""},
		{"padded name", func(m *InstanceResourceModel) { m.Name = types.StringValue("orders ") }, ""},
		{"blank name", func(m *InstanceResourceModel) { m.Name = types.StringValue("  ") }, "name"},
		{"padded description", func(m *InstanceResourceModel) { m.Description = types.StringValue(" prod") }, ""},
		{"blank description", func(m *InstanceResourceModel) { m.Description = types.StringValue("  ") }, ""},
		{"padded database type and solution", func(m *InstanceResourceModel) {
			m.DatabaseType, m.Solution = types.StringValue(" postgresql"), types.StringValue("basic ")
		}, ""},
		{"unknown database type", func(m *InstanceResourceModel) { m.DatabaseType = types.StringValue("postgres") }, "database_type"},
		{"unknown solution", func(m *InstanceResourceModel) { m.Solution = types.StringValue("premium") }, "solution"},
		{"invalid subnet", func(m *InstanceResourceModel) {
			m.Subnet = reference.Value(reference.ID, types.StringValue("subnet"))
		}, "subnet.id"},
		{"padded subnet", func(m *InstanceResourceModel) {
			m.Subnet = reference.Value(reference.ID, types.StringValue("  "+testSubnetID.String()+" "))
		}, ""},
		{"padded region", func(m *InstanceResourceModel) { m.Region = reference.Value(reference.Name, types.StringValue("hn-1 ")) }, ""},
		{"empty region", func(m *InstanceResourceModel) { m.Region = reference.Value(reference.Name, types.StringValue("")) }, "region.name"},
		{"blank region", func(m *InstanceResourceModel) { m.Region = reference.Value(reference.Name, types.StringValue(" \t")) }, "region.name"},
		{"weekly policy", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "weekly", "monday", 2, 7) }, ""},
		{"padded weekly policy", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, " weekly ", " monday", 2, 7) }, ""},
		{"unknown interval", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "hourly", "", 2, 7) }, "backup_interval"},
		{"unknown day", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "weekly", "funday", 2, 7) }, "day_of_week"},
		{"weekly without day", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "weekly", "", 2, 7) }, "day_of_week"},
		{"daily with day", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "daily", "monday", 2, 7) }, "day_of_week"},
		{"enabled without schedule", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "", "", -1, -1) }, "backup_interval"},
		{"start hour out of range", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(true, "daily", "", 24, 7) }, "start_hour"},
		{"disabled with schedule", func(m *InstanceResourceModel) { m.BackupPolicy = backupPolicyObject(false, "daily", "", -1, -1) }, "backup_interval"},
		{"policy without flag", func(m *InstanceResourceModel) {
			m.BackupPolicy = withAttribute(backupPolicyObject(true, "", "", -1, -1), "enabled", types.BoolNull())
		}, "enabled"},
		{"auto-increase", func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(true, 80, 100) }, ""},
		{"auto-increase without threshold", func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(true, -1, 100) }, "threshold"},
		{"threshold out of range", func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(true, 96, 100) }, "threshold"},
		{"limit not a multiple of 10", func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(true, 80, 105) }, "limit"},
		{"disabled with limit", func(m *InstanceResourceModel) { m.AutoIncreaseVolumeSize = autoIncreaseObject(false, -1, 100) }, "limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := testInstanceConfig()
			tt.change(&config)
			var resp resource.ValidateConfigResponse
			NewInstanceResource().(*InstanceResource).ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: instanceConfig(t, config)}, &resp)
			if tt.wantError == "" {
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), tt.wantError) {
				t.Fatalf("want an error about %s, got %v", tt.wantError, resp.Diagnostics)
			}
		})
	}
}

func TestInstanceImportState(t *testing.T) {
	t.Parallel()
	r := &InstanceResource{}
	resp := resource.ImportStateResponse{State: tfsdk.State{Schema: instanceSchema(t), Raw: nullRaw(t)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "  " + testInstanceID.String() + " "}, &resp)
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() || id.ValueString() != testInstanceID.String() {
		t.Fatalf("import id = %v: %v", id, resp.Diagnostics)
	}
	invalid := resource.ImportStateResponse{State: tfsdk.State{Schema: instanceSchema(t), Raw: nullRaw(t)}}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "orders"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("a non-UUID import ID must be rejected")
	}
}

func TestInstanceReadRefreshesState(t *testing.T) {
	t.Parallel()
	api, client, project := newFakeAPI(t, func(apiRequest) (int, any) {
		instance := sampleInstance()
		instance.Description, instance.DiskSize, instance.Status = "changed outside", 30, dbaassdk.DBInstanceStatusBACKINGUP
		return http.StatusOK, instance
	})
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	state := instanceState(t, testInstanceState(t))
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	var got InstanceResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if got.Description.ValueString() != "changed outside" || got.Status.ValueString() != "BACKING_UP" || got.DiskSize.ValueInt64() != 20 || got.CurrentDiskSize.ValueInt64() != 30 || !got.RootPassword.IsNull() {
		t.Fatalf("refresh must report backend values and keep the requested disk_size: %#v", got)
	}
	if id, _ := selector.ID(got.Flavor); id != testFlavorID || reference.Get(got.Subnet, reference.ID).ValueString() != testSubnetID.String() {
		t.Fatalf("refresh changed the selectors or references: %v %v", got.Flavor, got.Subnet)
	}
}

func TestInstanceCreateChecksSubnetBeforeCreating(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		status int
		body   any
		want   string
	}{
		{"missing subnet", http.StatusNotFound, apiError("not_found", "Not found"), "Subnet not found"},
		{"subnet in another region", http.StatusOK, testSubnet(testOtherID), "Subnet in another region"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
				if strings.HasPrefix(req.Path, "/v2/network/subnets/") {
					return tt.status, tt.body
				}
				if status, body, ok := catalog(req); ok {
					return status, body
				}
				return http.StatusCreated, sampleInstance()
			})
			r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: instanceSchema(t), Raw: nullRaw(t)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: instancePlan(t, testInstanceConfig()), Config: instanceConfig(t, testInstanceConfig())}, &resp)
			if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != tt.want {
				t.Fatalf("want %q, got %v", tt.want, resp.Diagnostics)
			}
			if len(api.requestsTo(http.MethodPost, "/")) != 0 || !resp.State.Raw.IsNull() {
				t.Fatal("an unusable subnet must stop the create before the request")
			}
		})
	}
}

func TestInstanceReportsAPIErrors(t *testing.T) {
	t.Parallel()
	const message = "DB instance name already exists."
	api, client, project := newFakeAPI(t, func(req apiRequest) (int, any) {
		if status, body, ok := catalog(req); ok {
			return status, body
		}
		if req.Method == http.MethodGet {
			return http.StatusInternalServerError, apiError("internal_error", "backend unavailable")
		}
		return http.StatusConflict, apiError("conflict", message)
	})
	r := &InstanceResource{client: client, network: api.network, project: project, projectID: testProjectID}
	ctx := context.Background()

	created := resource.CreateResponse{State: tfsdk.State{Schema: instanceSchema(t), Raw: nullRaw(t)}}
	r.Create(ctx, resource.CreateRequest{Plan: instancePlan(t, testInstanceConfig()), Config: instanceConfig(t, testInstanceConfig())}, &created)
	if !created.Diagnostics.HasError() || !strings.Contains(created.Diagnostics[0].Detail(), message) || !created.State.Raw.IsNull() {
		t.Fatalf("a rejected create must report the backend message and save no state: %v", created.Diagnostics)
	}

	state := instanceState(t, testInstanceState(t))
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() || read.State.Raw.IsNull() {
		t.Fatalf("a failed read must keep state and return a diagnostic: %v", read.Diagnostics)
	}

	plan := plannedUpdate(testInstanceState(t))
	plan.DBVersion, plan.Flavor, plan.VolumeType = testInstanceState(t).DBVersion, testInstanceState(t).Flavor, testInstanceState(t).VolumeType
	plan.Description = types.StringValue("renamed")
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: instancePlan(t, plan), State: state}, &updated)
	if !updated.Diagnostics.HasError() || !strings.Contains(updated.Diagnostics[0].Detail(), message) {
		t.Fatalf("a rejected update must report the backend message: %v", updated.Diagnostics)
	}
	var kept InstanceResourceModel
	updated.Diagnostics.Append(updated.State.Get(ctx, &kept)...)
	if kept.Description.ValueString() != "orders db" {
		t.Fatalf("a rejected update must keep the prior state: %v", kept.Description)
	}
}
