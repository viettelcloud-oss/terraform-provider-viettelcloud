// Package compare answers whether a Terraform value is semantically the same as
// the backend's, so callers can preserve a practitioner's configured
// representation without hiding real drift.
//
// Comparison is per type because equivalence differs: UUIDs ignore case, IP
// addresses compare parsed forms, MAC addresses ignore separator style.
package compare

// backend's?" so callers can preserve a practitioner's configured
// representation without hiding real drift. Comparison is per type because
// equivalence differs: UUIDs ignore case, IP addresses compare parsed forms,
// MAC addresses ignore separator style.

import (
	"encoding/json"
	"net"
	"net/netip"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
)

func SameUUID(v types.String, w core.UUID) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	parsed, err := core.ParseUUID(strings.TrimSpace(v.ValueString()))
	return err == nil && parsed == w
}

// PreservedUUID keeps the configured spelling of id, such as upper case or
// braces, when it names the same object, and the backend's otherwise.
func PreservedUUID(configured types.String, id core.UUID) types.String {
	if SameUUID(configured, id) {
		return configured
	}
	return types.StringValue(id.String())
}

func SameIPAddress(v types.String, w string) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	configured, err := netip.ParseAddr(strings.TrimSpace(v.ValueString()))
	if err != nil {
		return false
	}
	backend, err := netip.ParseAddr(strings.TrimSpace(w))
	return err == nil && configured == backend
}

func SameMACAddress(v types.String, w string) bool {
	if v.IsNull() || v.IsUnknown() {
		return false
	}
	configured, err := net.ParseMAC(strings.TrimSpace(v.ValueString()))
	if err != nil {
		return false
	}
	backend, err := net.ParseMAC(strings.TrimSpace(w))
	return err == nil && configured.String() == backend.String()
}

func SameUUIDSet(v, w types.Set) bool {
	if v.IsNull() || v.IsUnknown() || w.IsNull() || w.IsUnknown() || len(v.Elements()) != len(w.Elements()) {
		return false
	}

	ids := make(map[core.UUID]struct{}, len(v.Elements()))
	for _, e := range v.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			return false
		}
		id, err := core.ParseUUID(strings.TrimSpace(s.ValueString()))
		if err != nil {
			return false
		}
		ids[id] = struct{}{}
	}
	for _, e := range w.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			return false
		}
		id, err := core.ParseUUID(strings.TrimSpace(s.ValueString()))
		if err != nil {
			return false
		}
		if _, ok := ids[id]; !ok {
			return false
		}
	}
	return true
}

func SameJSON(v, w types.String) bool {
	if v.IsNull() || v.IsUnknown() || w.IsNull() || w.IsUnknown() {
		return false
	}
	var configured, backend any
	return json.Unmarshal([]byte(v.ValueString()), &configured) == nil && json.Unmarshal([]byte(w.ValueString()), &backend) == nil && reflect.DeepEqual(configured, backend)
}

// SameTrimmed reports whether v is known and equals w once outer whitespace is
// removed from both. The API trims request strings, so a configured value with
// outer whitespace comes back without it.
func SameTrimmed(v types.String, w string) bool {
	return !v.IsNull() && !v.IsUnknown() && strings.TrimSpace(v.ValueString()) == strings.TrimSpace(w)
}

// PreservedString keeps the configured spelling of a value when it differs
// from the backend value only by outer whitespace, and the backend value
// otherwise.
func PreservedString(configured types.String, backend string) types.String {
	if SameTrimmed(configured, backend) {
		return configured
	}
	return types.StringValue(backend)
}

// SameTrimmedString reports whether two known values differ at most by outer
// whitespace. A plan modifier uses it to compare a planned value with the
// prior state value.
func SameTrimmedString(v, w types.String) bool {
	return !w.IsNull() && !w.IsUnknown() && SameTrimmed(v, w.ValueString())
}

// SameUUIDString reports whether two known values parse as the same UUID.
func SameUUIDString(v, w types.String) bool {
	if w.IsNull() || w.IsUnknown() {
		return false
	}
	id, err := core.ParseUUID(strings.TrimSpace(w.ValueString()))
	return err == nil && SameUUID(v, id)
}

// SameIPAddressString reports whether two known values parse as the same IP
// address.
func SameIPAddressString(v, w types.String) bool {
	return !w.IsNull() && !w.IsUnknown() && SameIPAddress(v, w.ValueString())
}

// SameMACAddressString reports whether two known values parse as the same MAC
// address.
func SameMACAddressString(v, w types.String) bool {
	return !w.IsNull() && !w.IsUnknown() && SameMACAddress(v, w.ValueString())
}
