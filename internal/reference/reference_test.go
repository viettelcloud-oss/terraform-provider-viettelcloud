package reference

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAttributes(t *testing.T) {
	got := Attributes(ID, "VPC ID (UUID).")
	attribute, ok := got[ID].(schema.StringAttribute)
	if !ok || len(got) != 1 {
		t.Fatalf("Attributes() = %#v, want one string attribute %q", got, ID)
	}
	if !attribute.Required || attribute.Description != "VPC ID (UUID)." {
		t.Fatalf("attribute = %#v, want required with description", attribute)
	}
}

func TestPreservedAttributes(t *testing.T) {
	got := PreservedAttributes(Name, "Zone name.")
	attribute, ok := got[Name].(schema.StringAttribute)
	if !ok || len(got) != 1 {
		t.Fatalf("PreservedAttributes() = %#v, want one string attribute %q", got, Name)
	}
	if attribute.Required || !attribute.Optional || !attribute.Computed || len(attribute.Validators) != 1 {
		t.Fatalf("attribute = %#v, want optional and computed with one validator", attribute)
	}
	if attribute.Description != "Zone name. Required when the reference is configured." {
		t.Fatalf("description = %q", attribute.Description)
	}
}

func TestConfiguredCriterion(t *testing.T) {
	tests := []struct {
		name    string
		value   types.String
		wantErr bool
	}{
		{"null", types.StringNull(), true},
		{"unknown", types.StringUnknown(), false},
		{"known", types.StringValue("vn-central-1a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp validator.StringResponse
			ConfiguredCriterion().ValidateString(context.Background(), validator.StringRequest{
				Path: path.Root("zone").AtName(Name), ConfigValue: tt.value,
			}, &resp)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tt.wantErr)
			}
			if tt.wantErr && resp.Diagnostics[0].Detail() != "zone.name must be configured." {
				t.Fatalf("detail = %q", resp.Diagnostics[0].Detail())
			}
		})
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		name      string
		reference types.Object
		criterion string
		want      types.String
	}{
		{"known", Value(ID, types.StringValue("a")), ID, types.StringValue("a")},
		{"null reference", types.ObjectNull(AttributeTypes(ID)), ID, types.StringNull()},
		{"unknown reference", types.ObjectUnknown(AttributeTypes(ID)), ID, types.StringUnknown()},
		{"unknown criterion", Value(ID, types.StringValue("a")), Name, types.StringNull()},
		{"null criterion", Of(map[string]types.String{ID: types.StringNull(), "cidr": types.StringValue("10.0.0.0/24")}), ID, types.StringNull()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Get(tt.reference, tt.criterion); !got.Equal(tt.want) {
				t.Fatalf("Get() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestValue(t *testing.T) {
	if got := Value(Name, types.StringNull()); !got.IsNull() {
		t.Fatalf("Value(null) = %s, want null", got)
	}
	if got := Value(Name, types.StringUnknown()); !got.IsUnknown() {
		t.Fatalf("Value(unknown) = %s, want unknown", got)
	}
	want := types.ObjectValueMust(map[string]attr.Type{Name: types.StringType}, map[string]attr.Value{Name: types.StringValue("vn-1a")})
	if got := Value(Name, types.StringValue("vn-1a")); !got.Equal(want) {
		t.Fatalf("Value() = %s, want %s", got, want)
	}
}

func TestSetAndStrings(t *testing.T) {
	set := Set(ID, []string{"a", "b"})
	if !set.ElementType(t.Context()).Equal(ObjectType(ID)) {
		t.Fatalf("element type = %s, want %s", set.ElementType(t.Context()), ObjectType(ID))
	}
	got := map[string]bool{}
	for _, value := range Strings(set, ID) {
		got[value.ValueString()] = true
	}
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("Strings() = %v, want a and b", got)
	}
	if Strings(types.SetNull(ObjectType(ID)), ID) != nil {
		t.Fatal("Strings(null) must be nil")
	}
	if Strings(types.SetUnknown(ObjectType(ID)), ID) != nil {
		t.Fatal("Strings(unknown) must be nil")
	}
}
