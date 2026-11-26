package network

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
)

// user_strings.go owns the state mapping and validation of user-supplied
// strings that the API trims.

// preservedNullableString keeps the configured spelling of an optional string
// when the backend value differs from it only by outer whitespace. The API
// trims request strings, so a padded configured value comes back without the
// padding. A null backend value stays null, so that real drift still shows.
func preservedNullableString(configured, backend types.String) types.String {
	if backend.IsNull() || backend.IsUnknown() {
		return backend
	}
	return compare.PreservedString(configured, backend.ValueString())
}

// isBlank reports whether a known value is empty once outer whitespace is
// removed. The provider sends the trimmed value, so such a value would reach
// the API as an empty string.
func isBlank(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown() && strings.TrimSpace(v.ValueString()) == ""
}

// notBlank rejects a configured value that is empty once outer whitespace is
// removed, because the API requires a non-empty value. It accepts outer
// whitespace around other text.
type notBlank struct{}

func (notBlank) Description(context.Context) string {
	return "Must contain a character other than whitespace."
}

func (v notBlank) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (notBlank) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if isBlank(req.ConfigValue) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", req.Path.String()+" must not be empty or contain only whitespace.")
	}
}
