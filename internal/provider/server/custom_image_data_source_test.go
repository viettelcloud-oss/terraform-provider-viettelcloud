package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

func TestCustomImageDataSourceSchemaConfigureRead(t *testing.T) {
	t.Parallel()
	for _, byID := range []bool{true, false} {
		t.Run(map[bool]string{true: "id", false: "filters"}[byID], func(t *testing.T) {
			image := customImageFixture()
			image.Owner = &serversdk.NestedUserSchema{Id: image.Id, Username: "image-owner"}
			client := customImageClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Project-ID") != placementGroupProjectID.String() {
					t.Error("wrong project")
				}
				if byID {
					if req.URL.Path != "/v2/server/custom-images/"+image.Id.String()+"/" {
						t.Error(req.URL.Path)
					}
					writeCustomImageResponse(t, w, image)
				} else {
					if req.URL.Query().Get("name") != "image-a" || req.URL.Query().Get("region") != "region-a" {
						t.Error(req.URL.String())
					}
					_ = json.NewEncoder(w).Encode(serversdk.PagedCustomImageSchema{Count: 1, Results: []serversdk.CustomImageSchema{*image}})
				}
			})
			d := NewCustomImageDataSource().(*CustomImageDataSource)
			var configured datasource.ConfigureResponse
			d.Configure(context.Background(), datasource.ConfigureRequest{}, &configured)
			d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{Server: client, ProjectID: placementGroupProjectID}}, &configured)
			var meta datasource.MetadataResponse
			d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &meta)
			if meta.TypeName != "viettelcloud_custom_image" {
				t.Fatal(meta.TypeName)
			}
			var schemaResponse datasource.SchemaResponse
			d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
			if _, ok := schemaResponse.Schema.Attributes["region_id"]; ok {
				t.Fatal("unexpected attribute region_id")
			}
			for _, field := range []string{"project", "owner", "owner_id"} {
				a := schemaResponse.Schema.Attributes[field]
				if !a.IsComputed() || a.IsOptional() || a.IsRequired() {
					t.Fatalf("%s must be a computed output", field)
				}
			}
			model := CustomImageDataSourceModel{SharedProjectIDs: types.SetNull(types.StringType)}
			if byID {
				model.ID = types.StringValue(image.Id.String())
			} else {
				model.Name = types.StringValue(" image-a ")
				model.Region = types.StringValue(" region-a ")
			}
			config := tfsdk.Config{Schema: schemaResponse.Schema}
			state := tfsdk.State{Schema: schemaResponse.Schema}
			if diags := state.Set(context.Background(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			config.Raw = state.Raw
			response := datasource.ReadResponse{State: state}
			d.Read(context.Background(), datasource.ReadRequest{Config: config}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var got CustomImageDataSourceModel
			_ = response.State.Get(context.Background(), &got)
			if got.Project.ValueString() != image.Project.Name || got.OwnerID.ValueString() != image.Owner.Id.String() || got.Owner.ValueString() != image.Owner.Username {
				t.Fatalf("ownership outputs differ from the API: %#v", got)
			}
			if got.ID.ValueString() != image.Id.String() || got.Status.ValueString() != "active" || (!byID && (got.Name.ValueString() != " image-a " || got.Region.ValueString() != " region-a ")) {
				t.Fatal(got)
			}
		})
	}
	d := &CustomImageDataSource{}
	var response datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "bad"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected type error")
	}
}

func TestCustomImageLookupErrors(t *testing.T) {
	t.Parallel()
	for _, model := range []CustomImageDataSourceModel{
		{},
		{CustomImageModel: CustomImageModel{ID: types.StringValue("invalid")}},
		{CustomImageModel: CustomImageModel{Name: types.StringValue(" ")}},
		{VolumeID: types.StringValue(" ")},
		{CustomImageModel: CustomImageModel{State: types.StringValue("invalid")}},
		{CustomImageModel: CustomImageModel{Status: types.StringValue("invalid")}},
	} {
		_, _, diags := customImageFilter(model)
		if !diags.HasError() {
			t.Fatal(model)
		}
	}
	for _, status := range []int{404, 500} {
		d := &CustomImageDataSource{client: customImageClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })}
		_, diags := d.getCustomImage(context.Background(), CustomImageDataSourceModel{CustomImageModel: CustomImageModel{ID: types.StringValue(customImageFixture().Id.String())}})
		if !diags.HasError() {
			t.Fatal("expected API diagnostic")
		}
	}
	for _, count := range []int{0, 2} {
		d := &CustomImageDataSource{client: customImageClient(t, func(w http.ResponseWriter, _ *http.Request) {
			images := []serversdk.CustomImageSchema{}
			for range count {
				images = append(images, *customImageFixture())
			}
			_ = json.NewEncoder(w).Encode(serversdk.PagedCustomImageSchema{Count: count, Results: images})
		})}
		_, diags := d.getCustomImage(context.Background(), CustomImageDataSourceModel{CustomImageModel: CustomImageModel{Name: types.StringValue("image-a")}})
		if !diags.HasError() {
			t.Fatal("cardinality error not reported")
		}
	}
}
