package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
)

// requiredReferenceAttribute returns the named reference attribute after it
// checks that the reference is a required object selecting by one required
// criterion.
func requiredReferenceAttribute(t *testing.T, resourceSchema schema.Schema, name, criterion string) schema.SingleNestedAttribute {
	t.Helper()

	attribute, ok := resourceSchema.Attributes[name].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("%s is %T, want a single nested attribute", name, resourceSchema.Attributes[name])
	}
	if !attribute.Required || attribute.Optional || attribute.Computed {
		t.Fatalf("%s flags: required=%t optional=%t computed=%t, want required only",
			name, attribute.Required, attribute.Optional, attribute.Computed)
	}
	inner, ok := attribute.Attributes[criterion].(schema.StringAttribute)
	if len(attribute.Attributes) != 1 || !ok || !inner.Required {
		t.Fatalf("%s attributes = %#v, want one required string %q", name, attribute.Attributes, criterion)
	}
	if attribute.Description == "" || inner.Description == "" {
		t.Fatalf("%s and %s.%s need descriptions", name, name, criterion)
	}
	return attribute
}

// referenceRequiresReplace runs the plan modifiers of a reference attribute on
// an update from stateValue to planValue and reports whether they replace the
// resource.
func referenceRequiresReplace(
	t *testing.T,
	resourceSchema schema.Schema,
	attribute schema.SingleNestedAttribute,
	stateValue, planValue types.Object,
) bool {
	t.Helper()

	// The modifiers treat a null state or plan as create or destroy, so the
	// update is modeled by an object whose attributes are all null.
	raw := nullAttributesValue(t, resourceSchema)

	request := planmodifier.ObjectRequest{
		State:       tfsdk.State{Schema: resourceSchema, Raw: raw},
		Plan:        tfsdk.Plan{Schema: resourceSchema, Raw: raw},
		StateValue:  stateValue,
		PlanValue:   planValue,
		ConfigValue: planValue,
	}
	var response planmodifier.ObjectResponse
	for _, modifier := range attribute.PlanModifiers {
		modifier.PlanModifyObject(context.Background(), request, &response)
	}
	if response.Diagnostics.HasError() {
		t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
	}
	return response.RequiresReplace
}

// readImportedState imports id and then reads the resource, the way Terraform
// does for `terraform import`. The network API answers every request with
// body.
func readImportedState(
	t *testing.T,
	newResource func(*networksdk.Client) resource.ResourceWithImportState,
	id string,
	body any,
) tfsdk.State {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	client, err := networksdk.NewClient(server.URL, networksdk.WithHTTPClient(server.Client()), networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	resourceUnderTest := newResource(client)

	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	resourceSchema := schemaResponse.Schema
	importResponse := resource.ImportStateResponse{
		State: tfsdk.State{Schema: resourceSchema, Raw: nullAttributesValue(t, resourceSchema)},
	}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	return readResponse.State
}

// nullAttributesValue returns a known resource object whose attributes are
// all null, which is the state Terraform starts an import from.
func nullAttributesValue(t *testing.T, resourceSchema schema.Schema) tftypes.Value {
	t.Helper()

	objectType, ok := resourceSchema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("resource schema type is not an object")
	}
	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		values[name] = tftypes.NewValue(attributeType, nil)
	}
	return tftypes.NewValue(objectType, values)
}
