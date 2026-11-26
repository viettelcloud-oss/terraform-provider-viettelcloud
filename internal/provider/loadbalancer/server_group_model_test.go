package loadbalancer

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func TestPopulateServerGroupModelResponsePresence(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		description *string
		enableH2    *bool
	}{
		{name: "absent"},
		{name: "empty and false", description: new(""), enableH2: new(false)},
		{name: "nonempty and true", description: new("backend description"), enableH2: new(true)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := sampleServerGroupDetail(serverGroupTestID, "pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
			api.Description = core.NullableFromPtr(tt.description)
			api.EnableH2 = core.NullableFromPtr(tt.enableH2)
			state := serverGroupConfiguredModel()
			populateServerGroupResourceState(api, &state)

			if want := types.StringPointerValue(tt.description); !state.Description.Equal(want) {
				t.Errorf("description = %s, want %s", state.Description, want)
			}
			if want := types.BoolPointerValue(tt.enableH2); !state.EnableH2.Equal(want) {
				t.Errorf("enable_h2 = %s, want %s", state.EnableH2, want)
			}
		})
	}
}

func TestPopulateServerGroupResourceState(t *testing.T) {
	t.Parallel()

	sg := sampleServerGroupDetailWithHealthCheck(serverGroupTestID, "my-sg", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	var state ServerGroupResourceModel
	state.LoadBalancer = reference.Value(reference.ID, types.StringValue(serverGroupTestLBID.String()))
	state.Name = types.StringValue("  my-sg  ")

	populateServerGroupResourceState(sg, &state)

	if state.ID.ValueString() != serverGroupTestID.String() {
		t.Errorf("expected ID %s, got %s", serverGroupTestID.String(), state.ID.ValueString())
	}
	if reference.Get(state.LoadBalancer, reference.ID).ValueString() != serverGroupTestLBID.String() {
		t.Errorf("expected load_balancer.id %s, got %s", serverGroupTestLBID.String(), reference.Get(state.LoadBalancer, reference.ID).ValueString())
	}
	// Configured padded representation is preserved
	if state.Name.ValueString() != "  my-sg  " {
		t.Errorf("expected Name to preserve configured representation, got %s", state.Name.ValueString())
	}
	if state.HealthCheck.IsNull() {
		t.Fatal("expected health check object to be populated")
	}
}

func TestServerGroupNestedConfiguredValuesSurviveNormalization(t *testing.T) {
	t.Parallel()
	api := sampleServerGroupDetailWithHealthCheck(serverGroupTestID, "pool", loadbalancersdk.ServerGroupProtocolTypeHTTP, loadbalancersdk.ServerGroupAlgorithmROUNDROBIN)
	api.SessionPersistenceType.Set(loadbalancersdk.ServerGroupSessionPersistenceAPPCOOKIE)
	api.SessionPersistenceCookieName.Set("SESSIONID")
	state := serverGroupConfiguredModel()
	state.SessionPersistence = types.ObjectValueMust(serverGroupSessionPersistenceAttrTypes(), map[string]attr.Value{
		"type": types.StringValue(" APP_COOKIE "), "cookie_name": types.StringValue(" SESSIONID "),
	})
	state.HealthCheck = types.ObjectValueMust(serverGroupHealthCheckAttrTypes(), map[string]attr.Value{
		"monitor_type": types.StringValue(" HTTP "), "delay": types.Int64Value(10), "timeout": types.Int64Value(5),
		"max_retries": types.Int64Value(3), "max_retries_down": types.Int64Value(3),
		"path": types.StringValue(" /health "), "http_method": types.StringValue(" GET "), "expected_codes": types.StringValue(" 200 "),
	})
	wantPersistence, wantHealthCheck := state.SessionPersistence, state.HealthCheck
	createBody, diags := buildServerGroupCreateBody(context.Background(), state)
	if diags.HasError() {
		t.Fatal(diags)
	}
	httpCheck, err := createBody.HealthCheck.MustGet().AsServerGroupHealthCheckHttpConfig()
	if err != nil || httpCheck.MonitorPath != "/health" || httpCheck.MonitorHttpMethod != "GET" || httpCheck.MonitorExpectedCodes != "200" {
		t.Fatalf("unexpected normalized request: %#v %v", httpCheck, err)
	}
	populateServerGroupResourceState(api, &state)
	if !state.SessionPersistence.Equal(wantPersistence) || !state.HealthCheck.Equal(wantHealthCheck) {
		t.Fatalf("normalized response changed configured nested values: %#v", state)
	}
	populateServerGroupResourceState(api, &state)
	if !state.SessionPersistence.Equal(wantPersistence) || !state.HealthCheck.Equal(wantHealthCheck) {
		t.Fatal("refresh did not preserve equivalent nested values")
	}
	api.MonitorPath.Set("/changed")
	api.SessionPersistenceCookieName.Set("CHANGED")
	populateServerGroupResourceState(api, &state)
	if state.HealthCheck.Attributes()["path"].Equal(wantHealthCheck.Attributes()["path"]) || state.SessionPersistence.Attributes()["cookie_name"].Equal(wantPersistence.Attributes()["cookie_name"]) {
		t.Fatal("configured-value preservation hid backend drift")
	}
	api.SessionPersistenceType = nil
	api.MonitorPath = nil
	populateServerGroupResourceState(api, &state)
	if !state.SessionPersistence.IsNull() || !state.HealthCheck.Attributes()["path"].IsNull() {
		t.Fatal("null backend values must remain null")
	}
}
