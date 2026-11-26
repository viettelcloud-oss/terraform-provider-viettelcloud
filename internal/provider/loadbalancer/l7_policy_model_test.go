package loadbalancer

import (
	"testing"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
)

func TestPopulateL7PolicyModelNil(t *testing.T) {
	t.Parallel()

	var m L7PolicyModel
	populateL7PolicyModel(nil, &m)
	if !m.ID.IsNull() {
		t.Fatal("expected null ID on nil policy")
	}

	var resModel L7PolicyResourceModel
	populateL7PolicyResourceState(nil, &resModel)
	if !resModel.ID.IsNull() {
		t.Fatal("expected null ID on nil policy")
	}

	var dsModel L7PolicyDataSourceModel
	populateL7PolicyDataSourceState(nil, &dsModel)
	if !dsModel.ID.IsNull() {
		t.Fatal("expected null ID on nil policy")
	}

	if l7PolicyDetailToSchema(nil) != nil {
		t.Fatal("expected nil schema from nil detail")
	}
}

func TestPopulateL7PolicyModelVariants(t *testing.T) {
	t.Parallel()

	sg := &loadbalancersdk.NestedServerGroupSchema{
		Id:   l7PolicyTestServerGroupID,
		Name: "test-pool",
	}
	url := "https://example.com/target"
	policy := &loadbalancersdk.L7PolicySchema{
		Id:                  l7PolicyTestID,
		ListenerId:          l7PolicyTestListenerID,
		Action:              loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP,
		RedirectServerGroup: core.NullableFromPtr(sg),
		RedirectUrl:         core.NullableFromPtr(&url),
	}

	var dataSource L7PolicyDataSourceModel
	populateL7PolicyDataSourceState(policy, &dataSource)
	if dataSource.RedirectServerGroupID.ValueString() != l7PolicyTestServerGroupID.String() ||
		dataSource.ListenerID.ValueString() != l7PolicyTestListenerID.String() {
		t.Fatalf("expected flat listener and server group IDs, got %s and %s", dataSource.ListenerID, dataSource.RedirectServerGroupID)
	}
	if dataSource.RedirectURL.ValueString() != url {
		t.Fatalf("expected URL %s, got %s", url, dataSource.RedirectURL.ValueString())
	}

	// An imported state has no references yet, so both come from the response.
	var imported L7PolicyResourceModel
	populateL7PolicyResourceState(policy, &imported)
	if got := reference.Get(imported.Listener, reference.ID); got.ValueString() != l7PolicyTestListenerID.String() {
		t.Fatalf("expected listener reference %s, got %s", l7PolicyTestListenerID, got)
	}
	if got := reference.Get(imported.RedirectServerGroup, reference.ID); got.ValueString() != l7PolicyTestServerGroupID.String() {
		t.Fatalf("expected redirect server group reference %s, got %s", l7PolicyTestServerGroupID, got)
	}

	// A response without a server group clears the reference.
	policy.Action = loadbalancersdk.L7PolicyActionREJECT
	policy.RedirectServerGroup.SetNull()
	populateL7PolicyResourceState(policy, &imported)
	if !imported.RedirectServerGroup.IsNull() {
		t.Fatalf("expected null redirect server group, got %s", imported.RedirectServerGroup)
	}
}

func TestPopulateL7PolicyResourceStateMapsDescriptionPresence(t *testing.T) {
	t.Parallel()

	policy := sampleL7PolicySchema()
	policy.Description.SetNull()
	state := L7PolicyResourceModel{L7PolicyModel: L7PolicyModel{Description: types.StringValue("")}}
	populateL7PolicyResourceState(policy, &state)
	if !state.Description.IsNull() {
		t.Fatalf("expected a nil description to map to null, got %s", state.Description)
	}

	policy.Description.Set("")
	state = L7PolicyResourceModel{}
	populateL7PolicyResourceState(policy, &state)
	if state.Description.IsNull() || state.Description.ValueString() != "" {
		t.Fatalf("expected a pointer to an empty description to map to \"\", got %s", state.Description)
	}
}

func TestPopulateL7PolicyResourceStatePreservation(t *testing.T) {
	t.Parallel()

	sg := &loadbalancersdk.NestedServerGroupSchema{
		Id:   l7PolicyTestServerGroupID,
		Name: "test-pool",
	}
	policy := &loadbalancersdk.L7PolicySchema{
		Id:                  l7PolicyTestID,
		ListenerId:          l7PolicyTestListenerID,
		Action:              loadbalancersdk.L7PolicyActionREDIRECTTOSERVERGROUP,
		RedirectServerGroup: core.NullableFromPtr(sg),
	}

	// 1. Lowercase action should be preserved
	state := L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			Action: types.StringValue("redirect_to_server_group"),
		},
		Listener:            reference.Value(reference.ID, types.StringValue(l7PolicyTestListenerID.String())),
		RedirectServerGroup: reference.Value(reference.ID, types.StringValue(l7PolicyTestServerGroupID.String())),
	}
	populateL7PolicyResourceState(policy, &state)
	if state.Action.ValueString() != "redirect_to_server_group" {
		t.Fatalf("expected action to be preserved as lowercase, got %s", state.Action.ValueString())
	}
	if got := reference.Get(state.Listener, reference.ID); got.ValueString() != l7PolicyTestListenerID.String() {
		t.Fatalf("expected listener to be preserved, got %s", got)
	}
	if got := reference.Get(state.RedirectServerGroup, reference.ID); got.ValueString() != l7PolicyTestServerGroupID.String() {
		t.Fatalf("expected redirect_server_group to be preserved, got %s", got)
	}

	// 2. Lowercase alias redirect_to_pool should be preserved
	stateAlias := L7PolicyResourceModel{
		L7PolicyModel: L7PolicyModel{
			Action: types.StringValue("redirect_to_pool"),
		},
	}
	populateL7PolicyResourceState(policy, &stateAlias)
	if stateAlias.Action.ValueString() != "redirect_to_pool" {
		t.Fatalf("expected alias to be preserved, got %s", stateAlias.Action.ValueString())
	}
}

// The backend trims name, description, redirect_url, and redirect_prefix, so
// state keeps a configured value with surrounding whitespace when the trimmed
// value matches. A different backend value is drift and replaces it.
func TestPopulateL7PolicyResourceStatePreservesTrimmedStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		backend    string
		want       string
	}{
		{"surrounding whitespace kept", "  web ", "web", "  web "},
		{"whitespace-only kept", "  ", "", "  "},
		{"exact value kept", "web", "web", "web"},
		{"different value is drift", "web ", "api", "api"},
		{"inner whitespace is drift", "w eb", "web", "web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			backend := tt.backend
			fields := map[string]struct {
				policy *loadbalancersdk.L7PolicySchema
				state  L7PolicyResourceModel
				got    func(L7PolicyResourceModel) types.String
			}{
				"name": {
					policy: &loadbalancersdk.L7PolicySchema{Action: loadbalancersdk.L7PolicyActionREJECT, Name: core.NullableFromPtr(&backend)},
					state:  L7PolicyResourceModel{L7PolicyModel: L7PolicyModel{Name: types.StringValue(tt.configured)}},
					got:    func(m L7PolicyResourceModel) types.String { return m.Name },
				},
				"description": {
					policy: &loadbalancersdk.L7PolicySchema{Action: loadbalancersdk.L7PolicyActionREJECT, Description: core.NullableFromPtr(&backend)},
					state:  L7PolicyResourceModel{L7PolicyModel: L7PolicyModel{Description: types.StringValue(tt.configured)}},
					got:    func(m L7PolicyResourceModel) types.String { return m.Description },
				},
				"redirect_url": {
					policy: &loadbalancersdk.L7PolicySchema{Action: loadbalancersdk.L7PolicyActionREDIRECTTOURL, RedirectUrl: core.NullableFromPtr(&backend)},
					state:  L7PolicyResourceModel{L7PolicyModel: L7PolicyModel{RedirectURL: types.StringValue(tt.configured)}},
					got:    func(m L7PolicyResourceModel) types.String { return m.RedirectURL },
				},
				"redirect_prefix": {
					policy: &loadbalancersdk.L7PolicySchema{Action: loadbalancersdk.L7PolicyActionREDIRECTPREFIX, RedirectPrefix: core.NullableFromPtr(&backend)},
					state:  L7PolicyResourceModel{L7PolicyModel: L7PolicyModel{RedirectPrefix: types.StringValue(tt.configured)}},
					got:    func(m L7PolicyResourceModel) types.String { return m.RedirectPrefix },
				},
			}
			for attribute, field := range fields {
				state := field.state
				populateL7PolicyResourceState(field.policy, &state)
				if got := field.got(state); got.IsNull() || got.ValueString() != tt.want {
					t.Errorf("%s = %s, want %q", attribute, got, tt.want)
				}
			}
		})
	}
}
