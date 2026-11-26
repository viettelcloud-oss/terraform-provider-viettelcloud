package network

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// user_strings_test.go tests the validators in user_strings.go.

func TestNameRejectsBlankValue(t *testing.T) {
	t.Parallel()

	values := map[string]struct {
		value     types.String
		wantError bool
	}{
		"name":         {value: types.StringValue("web")},
		"padded name":  {value: types.StringValue("  web  ")},
		"empty name":   {value: types.StringValue(""), wantError: true},
		"blank name":   {value: types.StringValue("   "), wantError: true},
		"null name":    {value: types.StringNull()},
		"unknown name": {value: types.StringUnknown()},
	}
	for resourceName, resourceUnderTest := range map[string]resource.Resource{
		"subnet":         &SubnetResource{},
		"security_group": &SecurityGroupResource{},
	} {
		var response resource.SchemaResponse
		resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &response)
		attribute, ok := response.Schema.Attributes["name"].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s name is %T, want a string attribute", resourceName, response.Schema.Attributes["name"])
		}
		for name, test := range values {
			t.Run(resourceName+"/"+name, func(t *testing.T) {
				t.Parallel()
				request := validator.StringRequest{Path: path.Root("name"), ConfigValue: test.value}
				var validatorResponse validator.StringResponse
				for _, v := range attribute.Validators {
					v.ValidateString(context.Background(), request, &validatorResponse)
				}
				if validatorResponse.Diagnostics.HasError() != test.wantError {
					t.Fatalf("diagnostics = %v, want error %t", validatorResponse.Diagnostics, test.wantError)
				}
			})
		}
	}
}
