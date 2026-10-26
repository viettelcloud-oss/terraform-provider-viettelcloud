package selector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
)

// testCriteria mixes string and boolean criteria, as the catalog selectors do.
func testCriteria() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"name":                schema.StringAttribute{Optional: true},
		"os":                  schema.StringAttribute{Optional: true},
		"version":             schema.StringAttribute{Optional: true},
		"type":                schema.StringAttribute{Optional: true},
		"multiattach":         schema.BoolAttribute{Optional: true},
		"encryption_key_size": schema.StringAttribute{Optional: true},
	}
}

func testAttributeTypes() map[string]attr.Type {
	return AttributeTypes(Attribute("Test selector.", testCriteria()))
}

func TestAttributeAddsComputedIDAndValidator(t *testing.T) {
	t.Parallel()
	criteria := testCriteria()
	attribute := Attribute("Test selector.", criteria)
	if _, ok := criteria["id"]; ok {
		t.Fatal("Attribute changed the caller's criteria")
	}
	id, ok := attribute.Attributes["id"].(schema.StringAttribute)
	if !ok || !id.Computed || id.Optional || id.Required {
		t.Fatalf("id must be computed only: %#v", attribute.Attributes["id"])
	}
	if !attribute.Optional || len(attribute.Validators) != 1 || attribute.Description != "Test selector." {
		t.Fatalf("unexpected attribute: %#v", attribute)
	}
	attributeTypes := AttributeTypes(attribute)
	if len(attributeTypes) != len(criteria)+1 || attributeTypes["multiattach"] == nil || attributeTypes["id"] == nil {
		t.Fatalf("attribute types do not follow the schema: %v", attributeTypes)
	}
}

// WithID must keep the attribute types of the object it receives, whatever
// criteria that object has.
func TestWithIDKeepsAttributeTypes(t *testing.T) {
	t.Parallel()
	for _, criteria := range []map[string]schema.Attribute{
		{"os": schema.StringAttribute{Optional: true}},
		{"multiattach": schema.BoolAttribute{Optional: true}},
	} {
		attributeTypes := AttributeTypes(Attribute("Test selector.", criteria))
		got := WithID(selectorTestCriteria(attributeTypes, nil), types.StringValue((core.UUID{1}).String()))
		if !got.Type(context.Background()).Equal(types.ObjectType{AttrTypes: attributeTypes}) {
			t.Fatalf("WithID changed the attribute types: %v", got)
		}
	}
}

func TestSelectorValidationBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		value                 types.Object
		planError, applyError bool
	}{
		{"null", types.ObjectNull(testAttributeTypes()), true, true},
		{"unknown object", types.ObjectUnknown(testAttributeTypes()), false, true},
		{"empty object", selectorTestCriteria(testAttributeTypes(), nil), true, true},
		{"whitespace", selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue(" \t ")}), true, true},
		{"unknown criterion", selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"version": types.StringUnknown()}), false, true},
		{"mixed known and unknown", selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"os": types.StringValue("Ubuntu"), "version": types.StringUnknown()}), false, true},
		{"false is configured", selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"multiattach": types.BoolValue(false)}), false, false},
		{"zero key size is configured", selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"encryption_key_size": types.StringValue("0")}), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Validate(tc.value, "selector", false).HasError(); got != tc.planError {
				t.Fatalf("plan error: got %v want %v", got, tc.planError)
			}
			if got := Validate(tc.value, "selector", true).HasError(); got != tc.applyError {
				t.Fatalf("apply error: got %v want %v", got, tc.applyError)
			}
		})
	}
	var response validator.ObjectResponse
	criteriaValidator{}.ValidateObject(context.Background(), validator.ObjectRequest{ConfigValue: types.ObjectNull(testAttributeTypes())}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("optional null selector: %v", response.Diagnostics)
	}
}

func selectorTestCriteria(typesMap map[string]attr.Type, values map[string]attr.Value) types.Object {
	complete := make(map[string]attr.Value, len(typesMap))
	for name, typ := range typesMap {
		if value, ok := values[name]; ok {
			complete[name] = value
		} else if typ.Equal(types.BoolType) {
			complete[name] = types.BoolNull()
		} else {
			complete[name] = types.StringNull()
		}
	}
	return types.ObjectValueMust(typesMap, complete)
}

func TestCriteriaIgnoreComputedID(t *testing.T) {
	t.Parallel()
	a := selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue(" Ubuntu "), "id": types.StringUnknown()})
	b := WithID(selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue("Ubuntu")}), types.StringValue((core.UUID{1}).String()))
	if !Equal(a, b) || HasUnknown(a) || Validate(a, "boot.image", true).HasError() {
		t.Fatal("computed ID affected criteria")
	}
	if !Validate(selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"id": types.StringUnknown()}), "boot.image", false).HasError() {
		t.Fatal("ID counted as a criterion")
	}
	if Equal(selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"multiattach": types.BoolValue(false)}), selectorTestCriteria(testAttributeTypes(), nil)) {
		t.Fatal("false treated as omitted")
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := core.UUID{1}
	prior := WithID(selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue("Ubuntu")}), types.StringValue(id.String()))
	changed := selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"os": types.StringValue("Ubuntu"), "version": types.StringValue("22.04")})
	for _, tc := range []struct {
		name                    string
		planned                 types.Object
		prior                   types.Object
		candidates              []core.UUID
		findError, currentError error
		want                    Decision
		wantError               bool
		calls, reads            int
		nullID                  bool
	}{
		{name: "equal", planned: WithID(prior, types.StringUnknown()), prior: prior, want: Same},
		{name: "unknown", planned: types.ObjectUnknown(testAttributeTypes()), prior: prior, want: Unknown},
		{name: "unknown criterion", planned: selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"version": types.StringUnknown()}), prior: prior, want: Unknown},
		{name: "same ID", planned: changed, prior: prior, candidates: []core.UUID{id}, want: Same, calls: 1},
		{name: "current among many", planned: changed, prior: prior, candidates: []core.UUID{{2}, id}, want: Same, calls: 1},
		{name: "different ID", planned: changed, prior: prior, candidates: []core.UUID{{2}}, want: Different, calls: 1},
		{name: "not found", planned: changed, prior: prior, want: Different, wantError: true, calls: 1},
		{name: "API error", planned: changed, prior: prior, findError: errors.New("catalog offline"), want: Different, wantError: true, calls: 1},
		{name: "missing prior ID", planned: changed, prior: WithID(prior, types.StringNull()), candidates: []core.UUID{id}, want: Same, calls: 1, reads: 1},
		{name: "equal criteria without prior ID", planned: WithID(prior, types.StringUnknown()), prior: WithID(prior, types.StringNull()), want: Same, reads: 1},
		{name: "fallback error", planned: changed, prior: WithID(prior, types.StringNull()), candidates: []core.UUID{id}, currentError: errors.New("resource offline"), want: Different, wantError: true, calls: 1, reads: 1},
		{name: "equal criteria for unidentified entry", planned: WithID(prior, types.StringUnknown()), prior: WithID(prior, types.StringNull()), currentError: fmt.Errorf("volume: %w", ErrNoIdentity), want: Same, nullID: true, reads: 1},
		{name: "equal criteria with fallback error", planned: WithID(prior, types.StringUnknown()), prior: WithID(prior, types.StringNull()), currentError: errors.New("resource offline"), want: Different, wantError: true, reads: 1},
		{name: "changed criteria for unidentified entry", planned: changed, prior: WithID(prior, types.StringNull()), candidates: []core.UUID{id}, currentError: ErrNoIdentity, want: Different, wantError: true, calls: 1, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, reads := 0, 0
			got, gotID, diags := Decide(ctx, tc.planned, tc.prior, "boot.image", func(context.Context) ([]core.UUID, error) { calls++; return tc.candidates, tc.findError }, func(context.Context) (core.UUID, error) { reads++; return id, tc.currentError })
			if got != tc.want || diags.HasError() != tc.wantError || calls != tc.calls || reads != tc.reads {
				t.Fatalf("decision=%v ID=%v diagnostics=%v calls=%d reads=%d", got, gotID, diags, calls, reads)
			}
			if tc.wantError && !strings.Contains(diags[0].Detail(), "boot.image") {
				t.Fatal("field missing from diagnostic")
			}
			if tc.nullID {
				if !gotID.IsNull() {
					t.Fatalf("unidentified entry must keep a null ID, got %v", gotID)
				}
				return
			}
			if !tc.wantError && tc.want == Same && gotID.ValueString() != id.String() {
				t.Fatal("Same did not retain current ID")
			}
			if tc.want != Same && !gotID.IsUnknown() {
				t.Fatal("changed identity must stay unknown until apply")
			}
		})
	}
}

func TestPending(t *testing.T) {
	t.Parallel()
	criteria := selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue("Ubuntu")})
	for _, tc := range []struct {
		name  string
		value types.Object
		want  bool
	}{
		{"null selector", types.ObjectNull(testAttributeTypes()), false},
		{"unknown selector", types.ObjectUnknown(testAttributeTypes()), true},
		{"unknown ID", WithID(criteria, types.StringUnknown()), true},
		{"known ID", WithID(criteria, types.StringValue(core.UUID{1}.String())), false},
		{"unidentified entry", WithID(criteria, types.StringNull()), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Pending(tc.value); got != tc.want {
				t.Fatalf("Pending = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRefresh(t *testing.T) {
	t.Parallel()
	id, other := core.UUID{1}, core.UUID{2}
	prior := WithID(selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"os": types.StringValue(" Ubuntu "), "type": types.StringValue("server")}), types.StringValue(id.String()))
	imported := selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue("new-image"), "id": types.StringValue(other.String())})
	for _, tc := range []struct {
		name      string
		prior     types.Object
		current   *core.UUID
		want      types.Object
		wantError bool
	}{
		{"origin missing", prior, nil, prior, false},
		{"import", types.ObjectNull(testAttributeTypes()), &other, imported, false},
		{"record first ID", WithID(prior, types.StringNull()), &id, prior, false},
		{"same ID ignores metadata", prior, &id, prior, false},
		{"outside change", prior, &other, imported, false},
		{"failed apply", WithID(prior, types.StringValue(other.String())), &id, selectorTestCriteria(testAttributeTypes(), map[string]attr.Value{"name": types.StringValue("new-image"), "id": types.StringValue(id.String())}), false},
		{"unknown ID", WithID(prior, types.StringUnknown()), &id, WithID(prior, types.StringUnknown()), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := Refresh(tc.prior, testAttributeTypes(), tc.current, "new-image")
			if diags.HasError() != tc.wantError || !got.Equal(tc.want) {
				t.Fatalf("got %v diagnostics %v want %v", got, diags, tc.want)
			}
		})
	}
	// ID drift exposes different criteria to the next plan.
	drifted, _ := Refresh(prior, testAttributeTypes(), &other, "new-image")
	decision, _, diags := Decide(context.Background(), prior, drifted, "boot.image", func(context.Context) ([]core.UUID, error) { return []core.UUID{id}, nil }, func(context.Context) (core.UUID, error) { t.Fatal("unexpected fallback"); return core.UUID{}, nil })
	if diags.HasError() || decision != Different {
		t.Fatalf("drift hidden: %v %v", decision, diags)
	}
}
