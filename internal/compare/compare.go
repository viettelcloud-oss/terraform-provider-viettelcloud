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
	parsed, err := core.ParseUUID(v.ValueString())
	return err == nil && parsed == w
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
		id, err := core.ParseUUID(s.ValueString())
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
		id, err := core.ParseUUID(s.ValueString())
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
