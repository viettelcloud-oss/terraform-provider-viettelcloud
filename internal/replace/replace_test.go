package replace

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	present = tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	absent  = tftypes.NewValue(tftypes.Object{}, nil)
)

func TestString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		state, plan, config types.String
		create, destroy     bool
		want, wantIfConfig  bool
	}{
		"same value":                 {state: types.StringValue("a"), plan: types.StringValue("a"), config: types.StringValue("a")},
		"changed spelling":           {state: types.StringValue("a"), plan: types.StringValue(" a "), config: types.StringValue(" a ")},
		"another value":              {state: types.StringValue("a"), plan: types.StringValue("b"), config: types.StringValue("b"), want: true, wantIfConfig: true},
		"unknown value":              {state: types.StringValue("a"), plan: types.StringUnknown(), config: types.StringUnknown(), want: true, wantIfConfig: true},
		"removed computed value":     {state: types.StringValue("a"), plan: types.StringUnknown(), config: types.StringNull(), want: true},
		"removed value":              {state: types.StringValue("a"), plan: types.StringNull(), config: types.StringNull(), want: true},
		"create":                     {state: types.StringNull(), plan: types.StringValue("a"), config: types.StringValue("a"), create: true},
		"destroy":                    {state: types.StringValue("a"), plan: types.StringNull(), config: types.StringNull(), destroy: true},
		"value set on null in state": {state: types.StringNull(), plan: types.StringValue("a"), config: types.StringValue("a"), want: true, wantIfConfig: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := planmodifier.StringRequest{
				State:       tfsdk.State{Raw: present},
				Plan:        tfsdk.Plan{Raw: present},
				StateValue:  tt.state,
				PlanValue:   tt.plan,
				ConfigValue: tt.config,
			}
			if tt.create {
				request.State.Raw = absent
			}
			if tt.destroy {
				request.Plan.Raw = absent
			}
			for modifier, want := range map[string]bool{"String": tt.want, "StringIfConfigured": tt.wantIfConfig} {
				m := String(compare.SameTrimmedString)
				if modifier == "StringIfConfigured" {
					m = StringIfConfigured(compare.SameTrimmedString)
				}
				var response planmodifier.StringResponse
				m.PlanModifyString(context.Background(), request, &response)
				if response.RequiresReplace != want {
					t.Errorf("%s: RequiresReplace = %t, want %t", modifier, response.RequiresReplace, want)
				}
			}
		})
	}
}

func TestReference(t *testing.T) {
	t.Parallel()

	attributeTypes := reference.AttributeTypes(reference.ID)
	id := func(value string) types.Object { return reference.Value(reference.ID, types.StringValue(value)) }
	const uuid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tests := map[string]struct {
		state, plan, config types.Object
		want, wantIfConfig  bool
	}{
		"same reference":     {state: id(uuid), plan: id(uuid), config: id(uuid)},
		"changed spelling":   {state: id(uuid), plan: id("{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}"), config: id("{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}")},
		"another object":     {state: id(uuid), plan: id("00000000-0000-0000-0000-000000000002"), config: id("00000000-0000-0000-0000-000000000002"), want: true, wantIfConfig: true},
		"unparsable value":   {state: id(uuid), plan: id("not-a-uuid"), config: id("not-a-uuid"), want: true, wantIfConfig: true},
		"unknown criterion":  {state: id(uuid), plan: reference.Value(reference.ID, types.StringUnknown()), config: reference.Value(reference.ID, types.StringUnknown()), want: true, wantIfConfig: true},
		"unknown reference":  {state: id(uuid), plan: types.ObjectUnknown(attributeTypes), config: types.ObjectUnknown(attributeTypes), want: true, wantIfConfig: true},
		"removed computed":   {state: id(uuid), plan: types.ObjectUnknown(attributeTypes), config: types.ObjectNull(attributeTypes), want: true},
		"removed reference":  {state: id(uuid), plan: types.ObjectNull(attributeTypes), config: types.ObjectNull(attributeTypes), want: true},
		"reference set late": {state: types.ObjectNull(attributeTypes), plan: id(uuid), config: id(uuid), want: true, wantIfConfig: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			request := planmodifier.ObjectRequest{
				State:       tfsdk.State{Raw: present},
				Plan:        tfsdk.Plan{Raw: present},
				StateValue:  tt.state,
				PlanValue:   tt.plan,
				ConfigValue: tt.config,
			}
			for modifier, want := range map[string]bool{"Reference": tt.want, "ReferenceIfConfigured": tt.wantIfConfig} {
				m := Reference(reference.ID, compare.SameUUIDString)
				if modifier == "ReferenceIfConfigured" {
					m = ReferenceIfConfigured(reference.ID, compare.SameUUIDString)
				}
				var response planmodifier.ObjectResponse
				m.PlanModifyObject(context.Background(), request, &response)
				if response.RequiresReplace != want {
					t.Errorf("%s: RequiresReplace = %t, want %t", modifier, response.RequiresReplace, want)
				}
			}
		})
	}
}

func TestReferenceSkipsCreateAndDestroy(t *testing.T) {
	t.Parallel()

	before := reference.Value(reference.Name, types.StringValue("vn-central"))
	after := reference.Value(reference.Name, types.StringValue("vn-north"))
	for name, raw := range map[string][2]tftypes.Value{"create": {absent, present}, "destroy": {present, absent}} {
		request := planmodifier.ObjectRequest{
			State:       tfsdk.State{Raw: raw[0]},
			Plan:        tfsdk.Plan{Raw: raw[1]},
			StateValue:  before,
			PlanValue:   after,
			ConfigValue: after,
		}
		var response planmodifier.ObjectResponse
		Reference(reference.Name, compare.SameTrimmedString).PlanModifyObject(context.Background(), request, &response)
		if response.RequiresReplace {
			t.Errorf("%s: RequiresReplace = true, want false", name)
		}
	}
}
