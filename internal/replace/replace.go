// Package replace builds plan modifiers that replace a resource when an
// immutable value changes, but not when only its spelling changes.
//
// The provider accepts some values in more than one spelling, such as a name
// with outer whitespace or a UUID in upper case. It sends the normalized form
// to the API and keeps the configured spelling in state. The Framework's
// RequiresReplace compares raw values, so a change of spelling alone would
// destroy and create an identical object. These modifiers ask a comparison
// function from package compare instead, and a spelling change then updates
// state in place.
package replace

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// Same reports whether a planned value and a prior state value name the same
// backend value. It must return false when either value is null or unknown.
type Same func(plan, state types.String) bool

const (
	description           = "Changing the value replaces the resource. A change of spelling only updates state."
	configuredDescription = "Changing the configured value replaces the resource. A change of spelling, or removing the value, only updates state."
)

// String returns a modifier that replaces the resource when the planned value
// differs from the prior state value, unless same reports them the same.
func String(same Same) planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !same(req.PlanValue, req.StateValue)
		},
		description, description,
	)
}

// StringIfConfigured works like String, but it never replaces the resource
// when the value is not configured, as RequiresReplaceIfConfigured does.
func StringIfConfigured(same Same) planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.ConfigValue.IsNull() && !same(req.PlanValue, req.StateValue)
		},
		configuredDescription, configuredDescription,
	)
}

// Reference returns a modifier for a reference object that replaces the
// resource when the planned criterion differs from the prior one, unless same
// reports them the same.
func Reference(criterion string, same Same) planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !same(reference.Get(req.PlanValue, criterion), reference.Get(req.StateValue, criterion))
		},
		description, description,
	)
}

// ReferenceIfConfigured works like Reference, but it never replaces the
// resource when the reference is not configured, as
// RequiresReplaceIfConfigured does.
func ReferenceIfConfigured(criterion string, same Same) planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.ConfigValue.IsNull() &&
				!same(reference.Get(req.PlanValue, criterion), reference.Get(req.StateValue, criterion))
		},
		configuredDescription, configuredDescription,
	)
}
