package network

// region_reference_test.go covers the region reference that the VPC, security
// group, and Elastic IP resources share.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func TestRegionReferenceSchemaAndReplacement(t *testing.T) {
	t.Parallel()

	before := reference.Value(reference.Name, types.StringValue("vn-central"))
	plans := map[string]struct {
		plan types.Object
		want bool
	}{
		"same name updates in place": {plan: before},
		"another name replaces":      {plan: reference.Value(reference.Name, types.StringValue("vn-north")), want: true},
		"untrimmed name replaces":    {plan: reference.Value(reference.Name, types.StringValue(" vn-central ")), want: true},
		"unknown name replaces":      {plan: reference.Value(reference.Name, types.StringUnknown()), want: true},
	}
	for resourceName, resourceUnderTest := range map[string]resource.Resource{
		"vpc":            &VPCResource{},
		"security_group": &SecurityGroupResource{},
		"elastic_ip":     &ElasticIPResource{},
	} {
		var response resource.SchemaResponse
		resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &response)
		region := requiredReferenceAttribute(t, response.Schema, "region", reference.Name)
		for name, test := range plans {
			t.Run(resourceName+"/"+name, func(t *testing.T) {
				t.Parallel()
				if got := referenceRequiresReplace(t, response.Schema, region, before, test.plan); got != test.want {
					t.Fatalf("RequiresReplace = %t, want %t", got, test.want)
				}
			})
		}
	}
}

func TestResolveRegionReference(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{7}
	resolve := func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		if filter.Name == nil || *filter.Name != " vn-central " {
			return projectsdk.ProjectRegionSchema{}, errors.New("no region matches")
		}
		return projectsdk.ProjectRegionSchema{Region: projectsdk.NestedRegionSchema{Id: regionID, Name: "vn-central"}}, nil
	}

	got, diags := resolveRegionReference(context.Background(), reference.Value(reference.Name, types.StringValue(" vn-central ")), resolve)
	if diags.HasError() || got != regionID {
		t.Fatalf("resolveRegionReference() = %s, %v; want %s and the unmodified name passed to the finder", got, diags, regionID)
	}

	tests := map[string]struct {
		region  types.Object
		summary string
	}{
		"null reference":  {region: types.ObjectNull(reference.AttributeTypes(reference.Name)), summary: "Missing region"},
		"unknown":         {region: types.ObjectUnknown(reference.AttributeTypes(reference.Name)), summary: "Missing region"},
		"unknown name":    {region: reference.Value(reference.Name, types.StringUnknown()), summary: "Missing region"},
		"blank name":      {region: reference.Value(reference.Name, types.StringValue(" ")), summary: "Missing region"},
		"unresolved name": {region: reference.Value(reference.Name, types.StringValue("vn-north")), summary: "Unable to resolve region"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, diags := resolveRegionReference(context.Background(), test.region, resolve)
			if !diags.HasError() || diags.Errors()[0].Summary() != test.summary ||
				!strings.Contains(diags.Errors()[0].Detail(), "region.name") {
				t.Fatalf("expected %q diagnostic naming region.name, got %v", test.summary, diags)
			}
		})
	}
}

func TestRegionReferenceState(t *testing.T) {
	t.Parallel()

	backend := reference.Value(reference.Name, types.StringValue("vn-central"))
	untrimmed := reference.Value(reference.Name, types.StringValue("  vn-central  "))
	tests := map[string]struct {
		configured types.Object
		want       types.Object
	}{
		"untrimmed name is kept":      {configured: untrimmed, want: untrimmed},
		"another name takes backend":  {configured: reference.Value(reference.Name, types.StringValue("vn-north")), want: backend},
		"unknown takes backend":       {configured: types.ObjectUnknown(reference.AttributeTypes(reference.Name)), want: backend},
		"imported null takes backend": {configured: types.ObjectNull(reference.AttributeTypes(reference.Name)), want: backend},
		"unknown name takes backend":  {configured: reference.Value(reference.Name, types.StringUnknown()), want: backend},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := regionReferenceState(test.configured, "vn-central"); !got.Equal(test.want) {
				t.Fatalf("regionReferenceState() = %s, want %s", got, test.want)
			}
		})
	}
}
