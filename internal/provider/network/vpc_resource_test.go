package network

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var vpcTestUUID = core.UUID{1}

func TestVPCModelsMatchSchemas(t *testing.T) {
	t.Parallel()

	var resourceSchemaResponse resource.SchemaResponse
	(&VPCResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceSchemaResponse)
	resourcePlan := tfsdk.Plan{Schema: resourceSchemaResponse.Schema}
	if diags := resourcePlan.Set(context.Background(), &VPCResourceModel{Region: types.ObjectNull(reference.AttributeTypes(reference.Name))}); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}

	var dataSourceSchemaResponse datasource.SchemaResponse
	(&VPCDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &dataSourceSchemaResponse)
	dataSourceState := tfsdk.State{Schema: dataSourceSchemaResponse.Schema}
	if diags := dataSourceState.Set(context.Background(), &VPCDataSourceModel{}); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestBuildVPCCreateBodyResolvesExactRegionAndNormalizesName(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{7}
	calls := 0
	body, diags := buildVPCCreateBody(context.Background(), VPCResourceModel{
		VPCModel: VPCModel{
			Name:        types.StringValue("  production  "),
			Description: types.StringValue("primary network"),
			CIDR:        types.StringValue("10.0.0.0/16"),
		},
		Region: reference.Value(reference.Name, types.StringValue("vn-central")),
	}, func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		calls++
		if filter.Name == nil || *filter.Name != "vn-central" {
			t.Fatalf("expected exact region name, got %#v", filter.Name)
		}
		return projectsdk.ProjectRegionSchema{
			Region: projectsdk.NestedRegionSchema{Id: regionID, Name: *filter.Name},
		}, nil
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if calls != 1 || body.RegionId != regionID || body.Name != "production" || body.Cidr != "10.0.0.0/16" ||
		body.Description == nil || *body.Description != "primary network" {
		t.Fatalf("unexpected VPC create body: calls=%d body=%#v", calls, body)
	}
}

func TestBuildVPCCreateBodyDoesNotNormalizeRegion(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{7}
	calls := 0
	body, diags := buildVPCCreateBody(context.Background(), VPCResourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("production"),
			CIDR: types.StringValue("10.0.0.0/16"),
		},
		Region: reference.Value(reference.Name, types.StringValue("  vn-central  ")),
	}, func(_ context.Context, filter projectlookup.RegionFilter) (projectsdk.ProjectRegionSchema, error) {
		calls++
		if filter.Name == nil || *filter.Name != "  vn-central  " {
			t.Fatalf("expected unmodified region name, got %#v", filter.Name)
		}
		return projectsdk.ProjectRegionSchema{
			Region: projectsdk.NestedRegionSchema{Id: regionID, Name: "vn-central"},
		}, nil
	})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if calls != 1 || body.RegionId != regionID {
		t.Fatalf("unexpected VPC create body: calls=%d body=%#v", calls, body)
	}
}

func TestBuildVPCUpdateBodyNormalizesName(t *testing.T) {
	t.Parallel()

	body := buildVPCUpdateBody(VPCResourceModel{
		VPCModel: VPCModel{
			Name:        types.StringValue("  VPC 1  "),
			Description: types.StringValue("network"),
		},
	})
	if body.Name != "VPC 1" || body.Description == nil || *body.Description != "network" {
		t.Fatalf("unexpected update body: %#v", body)
	}
}

func TestPopulateVPCState(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	var state VPCResourceModel
	populateVPCResourceState(vpc, &state)

	if state.ID.ValueString() != vpc.Id.String() {
		t.Errorf("expected ID %s, got %s", vpc.Id.String(), state.ID.ValueString())
	}
	if state.Name.ValueString() != vpc.Name {
		t.Errorf("expected Name %s, got %s", vpc.Name, state.Name.ValueString())
	}
	if state.CIDR.ValueString() != vpc.Cidr {
		t.Errorf("expected CIDR %s, got %s", vpc.Cidr, state.CIDR.ValueString())
	}
	if reference.Get(state.Region, reference.Name).ValueString() != vpc.Region.Name {
		t.Errorf("expected Region %s, got %s", vpc.Region.Name, reference.Get(state.Region, reference.Name).ValueString())
	}
	if state.DisplayName.ValueString() != vpc.DisplayName {
		t.Errorf("expected DisplayName %s, got %s", vpc.DisplayName, state.DisplayName.ValueString())
	}
	if state.Description.ValueString() != "test vpc description" {
		t.Errorf("expected Description 'test vpc description', got %s", state.Description.ValueString())
	}
}

func TestPopulateVPCResourceStatePreservesConfiguredValuesWhenBackendTrimsWhitespace(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	state := VPCResourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("  test-vpc  "),
		},
		Region: reference.Value(reference.Name, types.StringValue("  region-1  ")),
	}
	populateVPCResourceState(vpc, &state)
	if state.Name.ValueString() != "  test-vpc  " {
		t.Fatalf("expected configured name representation to be preserved, got %q", state.Name.ValueString())
	}
	if reference.Get(state.Region, reference.Name).ValueString() != "  region-1  " {
		t.Fatalf("expected configured region representation to be preserved, got %q", reference.Get(state.Region, reference.Name).ValueString())
	}
}

func TestPopulateVPCResourceStateUsesChangedBackendValues(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	state := VPCResourceModel{
		VPCModel: VPCModel{
			Name: types.StringValue("different-vpc"),
		},
		Region: reference.Value(reference.Name, types.StringValue("different-region")),
	}
	populateVPCResourceState(vpc, &state)
	if state.Name.ValueString() != vpc.Name {
		t.Fatalf("expected backend name %q, got %q", vpc.Name, state.Name.ValueString())
	}
	if reference.Get(state.Region, reference.Name).ValueString() != vpc.Region.Name {
		t.Fatalf("expected backend region %q, got %q", vpc.Region.Name, reference.Get(state.Region, reference.Name).ValueString())
	}
}

func TestPopulateVPCStateNilDescription(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	vpc.Description = nil
	var state VPCResourceModel
	populateVPCResourceState(vpc, &state)

	if !state.Description.IsNull() {
		t.Errorf("expected null Description, got %s", state.Description.ValueString())
	}
}

func TestPopulateVPCStateNil(t *testing.T) {
	t.Parallel()

	var state VPCResourceModel
	populateVPCResourceState(nil, &state)

	if !state.ID.IsNull() {
		t.Errorf("expected null ID, got %s", state.ID.ValueString())
	}
}

func TestVPCReadAfterImportFillsRegionReference(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	state := readImportedState(t, func(client *networksdk.Client) resource.ResourceWithImportState {
		return &VPCResource{client: client, projectID: vpcTestUUID}
	}, vpc.Id.String(), vpc)

	var region types.Object
	if diags := state.GetAttribute(context.Background(), path.Root("region"), &region); diags.HasError() {
		t.Fatalf("read region: %v", diags)
	}
	if want := reference.Value(reference.Name, types.StringValue(vpc.Region.Name)); !region.Equal(want) {
		t.Fatalf("imported region = %s, want %s", region, want)
	}
}

func TestPopulateVPCDataSourceStateKeepsRegion(t *testing.T) {
	t.Parallel()

	vpc := mockVPC()
	var state VPCDataSourceModel
	populateVPCDataSourceState(vpc, &state)
	if state.Region.ValueString() != vpc.Region.Name {
		t.Fatalf("expected region %s, got %s", vpc.Region.Name, state.Region.ValueString())
	}
}

func mockVPC() *networksdk.VPCSchema {
	description := "test vpc description"
	return &networksdk.VPCSchema{
		Id:          vpcTestUUID,
		Name:        "test-vpc",
		Description: &description,
		Cidr:        "10.0.0.0/16",
		DisplayName: "test-vpc",
		Region: networksdk.NestedRegionSchema{
			Id:   vpcTestUUID,
			Name: "region-1",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}
