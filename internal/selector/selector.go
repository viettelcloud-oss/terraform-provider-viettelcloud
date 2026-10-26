// Package selector implements catalog selectors: nested objects that select
// one catalog entry, such as an image or a volume type, by combined criteria
// and record the selected entry in a computed "id".
//
// A known id is the selected entry, an unknown id is resolved at apply, and a
// null id is an entry that the backend does not identify. See Pending and
// ErrNoIdentity, and "Catalog Selectors" in CONTRIBUTING.md.
//
// This package holds the logic that does not depend on the kind of entry. The
// service that owns the entry defines its criteria and filter in a leaf
// selector package, such as server/selector.
package selector

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
)

// Attribute returns an optional selector attribute with the given criteria. It
// adds the computed "id" and a validator that requires at least one criterion.
func Attribute(description string, criteria map[string]schema.Attribute) schema.SingleNestedAttribute {
	attributes := maps.Clone(criteria)
	attributes["id"] = schema.StringAttribute{Computed: true, Description: "ID of the selected catalog entry."}
	return schema.SingleNestedAttribute{
		Optional: true, Description: description,
		Validators: []validator.Object{criteriaValidator{}},
		Attributes: attributes,
	}
}

// AttributeTypes returns the object attribute types of a selector attribute.
func AttributeTypes(attribute schema.SingleNestedAttribute) map[string]attr.Type {
	return attribute.GetType().(types.ObjectType).AttrTypes
}

// Text returns the trimmed value of a known string criterion, or nil.
func Text(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	return new(strings.TrimSpace(value.ValueString()))
}

type criteriaValidator struct{}

func (criteriaValidator) Description(context.Context) string {
	return "At least one non-empty selector criterion is required."
}

func (v criteriaValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (criteriaValidator) ValidateObject(_ context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() {
		return
	}
	resp.Diagnostics.Append(Validate(req.ConfigValue, req.Path.String(), false)...)
}

// Validate rejects a selector with no criteria or with a blank criterion.
// While planning (known is false) it defers unknown criteria. At apply
// (known is true) it rejects them before any API lookup.
func Validate(value types.Object, field string, known bool) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		if value.IsUnknown() && !known {
			return diags
		}
		diags.AddError("Missing selector criteria", field+" must be configured and known.")
		return diags
	}
	supplied := false
	for name, value := range value.Attributes() {
		if name == "id" {
			continue
		}
		if value.IsNull() {
			continue
		}
		supplied = true
		if value.IsUnknown() {
			if known {
				diags.AddError("Unknown selector criterion", field+"."+name+" must be known before apply.")
			}
			continue
		}
		if text, ok := value.(types.String); ok && strings.TrimSpace(text.ValueString()) == "" {
			diags.AddError("Empty selector criterion", field+"."+name+" must not be empty or whitespace-only.")
		}
	}
	if !supplied {
		diags.AddError("Missing selector criteria", field+" must contain at least one criterion.")
	}
	return diags
}

// Equal reports whether the criteria of a and b match, and ignores the
// computed id. It trims string criteria and keeps false, null, and unknown
// distinct.
func Equal(a, b types.Object) bool {
	if a.IsNull() || a.IsUnknown() || b.IsNull() || b.IsUnknown() {
		return a.Equal(b)
	}
	for name, value := range a.Attributes() {
		if name == "id" {
			continue
		}
		other := b.Attributes()[name]
		if text, ok := value.(types.String); ok && !text.IsNull() && !text.IsUnknown() {
			if otherText, ok := other.(types.String); ok && !otherText.IsNull() && !otherText.IsUnknown() {
				if strings.TrimSpace(text.ValueString()) == strings.TrimSpace(otherText.ValueString()) {
					continue
				}
			}
		}
		if !value.Equal(other) {
			return false
		}
	}
	return true
}

func HasUnknown(value types.Object) bool {
	if value.IsUnknown() {
		return true
	}
	for name, criterion := range value.Attributes() {
		if name == "id" {
			continue
		}
		if criterion.IsUnknown() {
			return true
		}
	}
	return false
}

// ID returns the selected catalog ID when it is known.
func ID(value types.Object) (core.UUID, bool) {
	if value.IsNull() || value.IsUnknown() {
		return core.UUID{}, false
	}
	id, ok := value.Attributes()["id"].(types.String)
	if !ok || id.IsNull() || id.IsUnknown() {
		return core.UUID{}, false
	}
	parsed, err := core.ParseUUID(id.ValueString())
	return parsed, err == nil
}

// Pending reports whether the selected entry is unresolved until apply.
// A null ID is not pending: it comes from state whose entry the backend no
// longer identifies.
func Pending(value types.Object) bool {
	if value.IsNull() || value.IsUnknown() {
		return value.IsUnknown()
	}
	id, ok := value.Attributes()["id"]
	return !ok || id.IsUnknown()
}

// WithID records the selected entry without changing criteria.
func WithID(value types.Object, id types.String) types.Object {
	if value.IsNull() || value.IsUnknown() {
		return value
	}
	values := value.Attributes()
	values["id"] = id
	return types.ObjectValueMust(attributeTypes(values), values)
}

// attributeTypes reports the types of selector values. Selector criteria are
// strings or booleans, and the computed ID is a string.
func attributeTypes(values map[string]attr.Value) map[string]attr.Type {
	result := make(map[string]attr.Type, len(values))
	for name, value := range values {
		switch value.(type) {
		case types.Bool:
			result[name] = types.BoolType
		case types.String:
			result[name] = types.StringType
		default:
			panic(fmt.Sprintf("selector criterion %q has unsupported type %T", name, value))
		}
	}
	return result
}

// ErrNoIdentity reports that the backend response does not identify the
// selected entry, such as an image removed from the catalog.
var ErrNoIdentity = errors.New("response does not identify the selected entry")

type Decision int

const (
	Same Decision = iota
	Different
	Unknown
)

// Decide retains the current entry when it still matches changed criteria.
// Only changed criteria need a catalog lookup. New entries resolve at apply.
func Decide(ctx context.Context, planned, prior types.Object, field string,
	find func(context.Context) ([]core.UUID, error),
	current func(context.Context) (core.UUID, error),
) (Decision, types.String, diag.Diagnostics) {
	var diags diag.Diagnostics
	unknown := types.StringUnknown()
	equal := Equal(planned, prior)
	if !equal && HasUnknown(planned) {
		return Unknown, unknown, diags
	}
	if planned.IsNull() {
		return Different, unknown, diags
	}
	var candidates []core.UUID
	if !equal {
		diags.Append(Validate(planned, field, true)...)
		if diags.HasError() {
			return Different, unknown, diags
		}
		var err error
		candidates, err = find(ctx)
		if err != nil {
			diags.AddError("Unable to resolve selector", fmt.Sprintf("%s: %s", field, err))
			return Different, unknown, diags
		}
	}
	id, known := ID(prior)
	if !known {
		var err error
		id, err = current(ctx)
		// State written before selectors had IDs keeps unchanged criteria when
		// the backend no longer identifies the entry. Refresh keeps the null ID.
		if equal && errors.Is(err, ErrNoIdentity) {
			return Same, types.StringNull(), diags
		}
		if err != nil {
			diags.AddError("Unable to read selector identity", fmt.Sprintf("%s: %s", field, err))
			return Different, unknown, diags
		}
	}
	if equal {
		return Same, types.StringValue(id.String()), diags
	}
	if slices.Contains(candidates, id) {
		return Same, types.StringValue(id.String()), diags
	}
	if len(candidates) == 0 {
		diags.AddError("Unable to resolve selector", field+": catalog entry not found.")
	}
	return Different, unknown, diags
}

// Refresh tracks catalog identity, not catalog metadata, without API calls.
// An unknown prior ID is a provider bug because apply must record the resolved ID.
func Refresh(prior types.Object, attributeTypes map[string]attr.Type, current *core.UUID, name string) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !prior.IsNull() && !prior.IsUnknown() {
		id := prior.Attributes()["id"]
		if id != nil && id.IsUnknown() {
			diags.AddError("Unknown selector identity", "The provider must record the resolved catalog ID before refreshing selector state.")
			return prior, diags
		}
	}
	if current == nil {
		return prior, diags
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		if id, known := ID(prior); !known || id == *current {
			return WithID(prior, types.StringValue(current.String())), diags
		}
	}
	values := make(map[string]attr.Value, len(attributeTypes))
	for key, typ := range attributeTypes {
		if typ.Equal(types.BoolType) {
			values[key] = types.BoolNull()
		} else {
			values[key] = types.StringNull()
		}
	}
	values["name"] = types.StringValue(name)
	values["id"] = types.StringValue(current.String())
	return types.ObjectValueMust(attributeTypes, values), diags
}
