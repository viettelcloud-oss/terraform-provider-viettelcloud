// Package reference models a practitioner's reference to another Viettel Cloud
// object as a nested object of string criteria, never as a plain string.
//
// A later release can then let practitioners select the object by more
// criteria by adding attributes to the object. Adding an attribute keeps
// existing configurations valid and decodes existing state with the new
// attribute null, so it is not a breaking change. Changing a plain string into
// an object is.
package reference

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Criterion names shared by references.
const (
	ID   = "id"
	Name = "name"
)

// Attributes returns the nested attributes of a reference that selects the
// object by one required criterion.
func Attributes(criterion, description string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		criterion: schema.StringAttribute{Required: true, Description: description},
	}
}

// PreservedAttributes returns the nested attributes of a reference that selects
// the object by one criterion, for a reference attribute that is Optional and
// Computed so that omitting it preserves the prior value.
//
// Terraform plans an omitted Optional and Computed attribute as null, not as
// its prior value, when that value holds a non-Computed nested attribute. The
// criterion is therefore Computed too, and a validator keeps it required
// whenever the reference is configured.
func PreservedAttributes(criterion, description string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		criterion: schema.StringAttribute{
			Optional:    true,
			Computed:    true,
			Description: description + " Required when the reference is configured.",
			Validators:  []validator.String{ConfiguredCriterion()},
		},
	}
}

// ConfiguredCriterion returns a validator that rejects a null criterion. The
// Framework validates nested attributes only inside a configured object, so it
// makes an Optional criterion required wherever its reference is configured.
func ConfiguredCriterion() validator.String {
	return configuredCriterion{}
}

type configuredCriterion struct{}

func (configuredCriterion) Description(context.Context) string {
	return "Required when the enclosing object is configured."
}

func (v configuredCriterion) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (configuredCriterion) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() {
		resp.Diagnostics.AddAttributeError(req.Path, "Missing reference criterion", req.Path.String()+" must be configured.")
	}
}

// AttributeTypes returns the attribute types of a reference with the given
// string criteria.
func AttributeTypes(criteria ...string) map[string]attr.Type {
	attributeTypes := make(map[string]attr.Type, len(criteria))
	for _, criterion := range criteria {
		attributeTypes[criterion] = types.StringType
	}
	return attributeTypes
}

// ObjectType returns the object type of a reference with the given string
// criteria, for use as a set or list element type.
func ObjectType(criteria ...string) types.ObjectType {
	return types.ObjectType{AttrTypes: AttributeTypes(criteria...)}
}

// Get returns one criterion of a reference. A null or unknown reference, or a
// reference without that criterion, yields a null or unknown string to match.
func Get(reference types.Object, criterion string) types.String {
	if reference.IsUnknown() {
		return types.StringUnknown()
	}
	if reference.IsNull() {
		return types.StringNull()
	}
	value, ok := reference.Attributes()[criterion].(types.String)
	if !ok {
		return types.StringNull()
	}
	return value
}

// Value returns a reference whose only criterion is value. A null or unknown
// value yields a null or unknown reference, so an unset optional reference
// stays unset.
func Value(criterion string, value types.String) types.Object {
	attributeTypes := AttributeTypes(criterion)
	if value.IsUnknown() {
		return types.ObjectUnknown(attributeTypes)
	}
	if value.IsNull() {
		return types.ObjectNull(attributeTypes)
	}
	return types.ObjectValueMust(attributeTypes, map[string]attr.Value{criterion: value})
}

// Of returns a known reference from every criterion value. Unset criteria are
// passed as null strings.
func Of(values map[string]types.String) types.Object {
	attributeTypes := make(map[string]attr.Type, len(values))
	attributes := make(map[string]attr.Value, len(values))
	for criterion, value := range values {
		attributeTypes[criterion] = types.StringType
		attributes[criterion] = value
	}
	return types.ObjectValueMust(attributeTypes, attributes)
}

// Strings returns one criterion of every reference in a set. A null or
// unknown set yields nil, so the caller must check the set state first.
func Strings(references types.Set, criterion string) []types.String {
	if references.IsNull() || references.IsUnknown() {
		return nil
	}
	values := make([]types.String, 0, len(references.Elements()))
	for _, element := range references.Elements() {
		object, ok := element.(types.Object)
		if !ok {
			continue
		}
		values = append(values, Get(object, criterion))
	}
	return values
}

// Set returns a known set of references whose only criterion is each value.
func Set(criterion string, values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, Value(criterion, types.StringValue(value)))
	}
	return types.SetValueMust(ObjectType(criterion), elements)
}
