package loadbalancer

import (
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func TestPopulateListenerModelResponsePresence(t *testing.T) {
	t.Parallel()

	tests := map[string]*bool{
		"absent": nil,
		"false":  new(false),
		"true":   new(true),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := defaultTestListenerModel()
			state.EnableH2 = types.BoolValue(true)
			state.SetXForwardedFor = types.BoolValue(true)
			state.SetXForwardedPort = types.BoolValue(true)
			state.SetXForwardedProto = types.BoolValue(true)
			state.DefaultTLSCert = listenerTestReference(listenerTestTLSCertID.String())
			state.ServerGroup = listenerTestReference(listenerTestServerGroupID.String())
			dataSourceState := ListenerDataSourceModel{
				DefaultTLSCertID: types.StringValue(listenerTestTLSCertID.String()),
				ServerGroupID:    types.StringValue(listenerTestServerGroupID.String()),
			}
			detail := &loadbalancersdk.ListenerDetailSchema{
				EnableH2:           core.NullableFromPtr(value),
				SetXForwardedFor:   core.NullableFromPtr(value),
				SetXForwardedPort:  core.NullableFromPtr(value),
				SetXForwardedProto: core.NullableFromPtr(value),
			}
			populateListenerResourceState(detail, &state)
			populateListenerDataSourceState(detail, &dataSourceState)
			want := types.BoolNull()
			if value != nil {
				want = types.BoolValue(*value)
			}
			for field, got := range map[string]types.Bool{
				"enable_h2":             state.EnableH2,
				"set_x_forwarded_for":   state.SetXForwardedFor,
				"set_x_forwarded_port":  state.SetXForwardedPort,
				"set_x_forwarded_proto": state.SetXForwardedProto,
			} {
				if !got.Equal(want) {
					t.Errorf("%s = %s, want %s", field, got, want)
				}
			}
			if !state.DefaultTLSCert.IsNull() || !state.ServerGroup.IsNull() ||
				!dataSourceState.DefaultTLSCertID.IsNull() || !dataSourceState.ServerGroupID.IsNull() {
				t.Fatal("absent references must map to null, regardless of prior state")
			}
		})
	}
}

func TestPopulateListenerModelTimeoutPresence(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]*int{
		"absent": nil,
		"zero":   new(0),
		"set":    new(60000),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			detail := &loadbalancersdk.ListenerDetailSchema{
				ListenerTimeout:         core.NullableFromPtr(value),
				ServerTimeout:           core.NullableFromPtr(value),
				ServerConnectingTimeout: core.NullableFromPtr(value),
			}
			for mode, populate := range map[string]func(*loadbalancersdk.ListenerDetailSchema, *ListenerModel){
				"resource": func(detail *loadbalancersdk.ListenerDetailSchema, model *ListenerModel) {
					state := ListenerResourceModel{ListenerModel: *model}
					populateListenerResourceState(detail, &state)
					*model = state.ListenerModel
				},
				"data source": func(detail *loadbalancersdk.ListenerDetailSchema, model *ListenerModel) {
					state := ListenerDataSourceModel{ListenerModel: *model}
					populateListenerDataSourceState(detail, &state)
					*model = state.ListenerModel
				},
			} {
				state := ListenerModel{
					ListenerTimeout:         types.Int64Value(50000),
					ServerTimeout:           types.Int64Unknown(),
					ServerConnectingTimeout: types.Int64Value(5000),
				}
				populate(detail, &state)
				want := types.Int64Null()
				if value != nil {
					want = types.Int64Value(int64(*value))
				}
				for field, got := range map[string]types.Int64{
					"listener_timeout":          state.ListenerTimeout,
					"server_timeout":            state.ServerTimeout,
					"server_connecting_timeout": state.ServerConnectingTimeout,
				} {
					if !got.Equal(want) {
						t.Errorf("%s %s = %s, want %s", mode, field, got, want)
					}
				}
			}
		})
	}
}

func TestPopulateListenerModelDescriptionOutsideDetailSchema(t *testing.T) {
	t.Parallel()

	for _, configured := range []types.String{
		types.StringNull(), types.StringUnknown(), types.StringValue(""), types.StringValue("configured description"),
	} {
		state := ListenerModel{Description: configured}
		populateListenerModel(&loadbalancersdk.ListenerDetailSchema{}, &state)
		want := configured
		if configured.IsUnknown() {
			want = types.StringNull()
		}
		if !state.Description.Equal(want) {
			t.Errorf("description = %s, want %s", state.Description, want)
		}
	}
}

func TestPopulateListenerResourceStateEmptySetPreservation(t *testing.T) {
	t.Parallel()

	state := defaultTestListenerModel()
	state.SNITLSCerts = listenerTestReferenceSet()
	state.AllowedCIDRs = types.SetValueMust(types.StringType, []attr.Value{})

	detail := &loadbalancersdk.ListenerDetailSchema{
		Id:                 listenerTestID,
		LoadBalancer:       loadbalancersdk.NestedLoadBalancerSchema{Id: listenerTestLoadBalancerID, Name: "lb"},
		Name:               "listener",
		Protocol:           loadbalancersdk.ListenerProtocolTypeHTTP,
		ProtocolPort:       80,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		SniTlsCerts:        []loadbalancersdk.NestedCertificateSchema{},
		AllowedCidrs:       []string{},
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	populateListenerResourceState(detail, &state)

	if state.SNITLSCerts.IsNull() || len(state.SNITLSCerts.Elements()) != 0 {
		t.Fatalf("expected SNITLSCerts to be preserved as empty set, got %#v", state.SNITLSCerts)
	}
	if state.AllowedCIDRs.IsNull() || len(state.AllowedCIDRs.Elements()) != 0 {
		t.Fatalf("expected AllowedCIDRs to be preserved as empty set, got %#v", state.AllowedCIDRs)
	}

	nullState := defaultTestListenerModel()
	populateListenerResourceState(detail, &nullState)
	if !nullState.SNITLSCerts.IsNull() {
		t.Fatalf("expected null SNITLSCerts to remain null, got %#v", nullState.SNITLSCerts)
	}
	if !nullState.AllowedCIDRs.IsNull() {
		t.Fatalf("expected null AllowedCIDRs to remain null, got %#v", nullState.AllowedCIDRs)
	}
}

func TestPopulateListenerResourceStateReferences(t *testing.T) {
	t.Parallel()

	detail := &loadbalancersdk.ListenerDetailSchema{
		Id:               listenerTestID,
		LoadBalancer:     loadbalancersdk.NestedLoadBalancerSchema{Id: listenerTestLoadBalancerID, Name: "lb"},
		ServerGroup:      core.NullableFromPtr(&loadbalancersdk.NestedServerGroupSchema{Id: listenerTestServerGroupID, Name: "group"}),
		DefaultTlsCertId: core.NullableFromPtr(&listenerTestTLSCertID),
		SniTlsCerts:      []loadbalancersdk.NestedCertificateSchema{{Id: listenerTestSNICertID}},
	}

	t.Run("configured spelling is preserved", func(t *testing.T) {
		t.Parallel()
		state := defaultTestListenerModel()
		state.LoadBalancer = listenerTestReference(strings.ToUpper(listenerTestLoadBalancerID.String()))
		state.ServerGroup = listenerTestReference(strings.ToUpper(listenerTestServerGroupID.String()))
		state.DefaultTLSCert = listenerTestReference(strings.ToUpper(listenerTestTLSCertID.String()))
		state.SNITLSCerts = listenerTestReferenceSet(strings.ToUpper(listenerTestSNICertID.String()))
		prior := state

		populateListenerResourceState(detail, &state)

		if !state.LoadBalancer.Equal(prior.LoadBalancer) || !state.ServerGroup.Equal(prior.ServerGroup) ||
			!state.DefaultTLSCert.Equal(prior.DefaultTLSCert) || !state.SNITLSCerts.Equal(prior.SNITLSCerts) {
			t.Fatalf("equivalent references must keep their configured spelling: %#v", state)
		}
	})

	t.Run("backend references replace omitted or different ones", func(t *testing.T) {
		t.Parallel()
		state := defaultTestListenerModel()
		state.LoadBalancer = listenerTestReference(listenerTestID.String())
		state.ServerGroup = types.ObjectUnknown(reference.AttributeTypes(reference.ID))

		populateListenerResourceState(detail, &state)

		if !state.LoadBalancer.Equal(listenerTestReference(listenerTestLoadBalancerID.String())) ||
			!state.ServerGroup.Equal(listenerTestReference(listenerTestServerGroupID.String())) ||
			!state.DefaultTLSCert.Equal(listenerTestReference(listenerTestTLSCertID.String())) ||
			!state.SNITLSCerts.Equal(listenerTestReferenceSet(listenerTestSNICertID.String())) {
			t.Fatalf("references must follow the backend: %#v", state)
		}
	})
}

func TestPopulateListenerResourceStateAllowedCIDRs(t *testing.T) {
	t.Parallel()

	cidrs := func(values ...string) types.Set {
		elements := make([]attr.Value, 0, len(values))
		for _, value := range values {
			elements = append(elements, types.StringValue(value))
		}
		return types.SetValueMust(types.StringType, elements)
	}
	tests := map[string]struct {
		prior   types.Set
		backend []string
		want    types.Set
	}{
		"imported state takes the backend set": {
			prior:   types.SetNull(types.StringType),
			backend: []string{"10.0.0.0/24", "192.0.2.0/24"},
			want:    cidrs("10.0.0.0/24", "192.0.2.0/24"),
		},
		"configured spelling is preserved": {
			prior:   cidrs(" 10.0.0.0/24 ", "192.0.2.0/24"),
			backend: []string{"192.0.2.0/24", "10.0.0.0/24"},
			want:    cidrs(" 10.0.0.0/24 ", "192.0.2.0/24"),
		},
		"another set follows the backend": {
			prior:   cidrs("10.0.0.0/24"),
			backend: []string{"192.0.2.0/24"},
			want:    cidrs("192.0.2.0/24"),
		},
		"a larger backend set follows the backend": {
			prior:   cidrs("10.0.0.0/24"),
			backend: []string{"10.0.0.0/24", "192.0.2.0/24"},
			want:    cidrs("10.0.0.0/24", "192.0.2.0/24"),
		},
		"a smaller backend set follows the backend": {
			prior:   cidrs("10.0.0.0/24", "192.0.2.0/24"),
			backend: []string{"10.0.0.0/24"},
			want:    cidrs("10.0.0.0/24"),
		},
		"removal outside Terraform maps to null": {
			prior:   cidrs("10.0.0.0/24"),
			backend: []string{},
			want:    types.SetNull(types.StringType),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := defaultTestListenerModel()
			state.AllowedCIDRs = tt.prior
			detail := &loadbalancersdk.ListenerDetailSchema{
				Id:                 listenerTestID,
				LoadBalancer:       loadbalancersdk.NestedLoadBalancerSchema{Id: listenerTestLoadBalancerID, Name: "lb"},
				ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
				AllowedCidrs:       tt.backend,
				CreatedAt:          time.Now(),
				UpdatedAt:          time.Now(),
			}

			populateListenerResourceState(detail, &state)

			if !state.AllowedCIDRs.Equal(tt.want) {
				t.Fatalf("allowed_cidrs = %s, want %s", state.AllowedCIDRs, tt.want)
			}
		})
	}
}

func TestPopulateListenerDataSourceStateReferences(t *testing.T) {
	t.Parallel()

	detail := &loadbalancersdk.ListenerDetailSchema{
		Id:               listenerTestID,
		LoadBalancer:     loadbalancersdk.NestedLoadBalancerSchema{Id: listenerTestLoadBalancerID, Name: "lb"},
		ServerGroup:      core.NullableFromPtr(&loadbalancersdk.NestedServerGroupSchema{Id: listenerTestServerGroupID, Name: "group"}),
		DefaultTlsCertId: core.NullableFromPtr(&listenerTestTLSCertID),
		SniTlsCerts:      []loadbalancersdk.NestedCertificateSchema{{Id: listenerTestSNICertID}, {Id: listenerTestTLSCertID}},
		AllowedCidrs:     []string{"10.0.0.0/24", "192.0.2.0/24"},
	}
	state := defaultTestListenerDataSourceModel()

	populateListenerDataSourceState(detail, &state)

	want := map[string]struct{ got, want attr.Value }{
		"load_balancer_id":    {state.LoadBalancerID, types.StringValue(listenerTestLoadBalancerID.String())},
		"server_group_id":     {state.ServerGroupID, types.StringValue(listenerTestServerGroupID.String())},
		"default_tls_cert_id": {state.DefaultTLSCertID, types.StringValue(listenerTestTLSCertID.String())},
		"sni_tls_cert_ids": {state.SNITLSCertIDs, types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue(listenerTestSNICertID.String()), types.StringValue(listenerTestTLSCertID.String()),
		})},
		"allowed_cidrs": {state.AllowedCIDRs, types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("10.0.0.0/24"), types.StringValue("192.0.2.0/24"),
		})},
	}
	for field, values := range want {
		if !values.got.Equal(values.want) {
			t.Errorf("%s = %s, want %s", field, values.got, values.want)
		}
	}
}

func TestListenerSameStringSet(t *testing.T) {
	t.Parallel()

	set := func(values ...attr.Value) types.Set {
		return types.SetValueMust(types.StringType, values)
	}
	str := types.StringValue
	tests := map[string]struct {
		left, right types.Set
		want        bool
	}{
		"same elements in another order":   {set(str("a"), str("b")), set(str("b"), str("a")), true},
		"configured whitespace is trimmed": {set(str(" a ")), set(str("a")), true},
		"two empty sets":                   {set(), set(), true},
		"null left":                        {types.SetNull(types.StringType), set(str("a")), false},
		"unknown right":                    {set(str("a")), types.SetUnknown(types.StringType), false},
		"different length":                 {set(str("a")), set(str("a"), str("b")), false},
		"same length and another element":  {set(str("a"), str("b")), set(str("a"), str("c")), false},
		"null element on the left":         {set(types.StringNull()), set(str("a")), false},
		"unknown element on the right":     {set(str("a")), set(types.StringUnknown()), false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := listenerSameStringSet(tt.left, tt.right); got != tt.want {
				t.Fatalf("listenerSameStringSet(%s, %s) = %t, want %t", tt.left, tt.right, got, tt.want)
			}
		})
	}
}
