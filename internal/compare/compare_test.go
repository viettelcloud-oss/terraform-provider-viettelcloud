package compare

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
)

func TestSameUUID(t *testing.T) {
	t.Parallel()

	want := mustParseUUID(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	tests := map[string]struct {
		value types.String
		want  bool
	}{
		"same canonical UUID": {value: types.StringValue(want.String()), want: true},
		"same uppercase UUID": {value: types.StringValue(strings.ToUpper(want.String())), want: true},
		"padded UUID":         {value: types.StringValue("  " + want.String() + "\n"), want: true},
		"different UUID":      {value: types.StringValue("00000000-0000-0000-0000-000000000001")},
		"invalid UUID":        {value: types.StringValue("not-a-uuid")},
		"null":                {value: types.StringNull()},
		"unknown":             {value: types.StringUnknown()},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := SameUUID(tt.value, want); got != tt.want {
				t.Fatalf("SameUUID() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestPreservedUUID(t *testing.T) {
	t.Parallel()

	id := mustParseUUID(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	upper := types.StringValue(strings.ToUpper(id.String()))
	tests := map[string]struct {
		configured types.String
		want       types.String
	}{
		"same UUID keeps configured spelling": {configured: upper, want: upper},
		"different UUID takes backend value":  {configured: types.StringValue("00000000-0000-0000-0000-000000000001"), want: types.StringValue(id.String())},
		"null takes backend value":            {configured: types.StringNull(), want: types.StringValue(id.String())},
		"unknown takes backend value":         {configured: types.StringUnknown(), want: types.StringValue(id.String())},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := PreservedUUID(tt.configured, id); !got.Equal(tt.want) {
				t.Fatalf("PreservedUUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameIPAddress(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value   types.String
		backend string
		want    bool
	}{
		"same IPv4":            {value: types.StringValue(" 10.0.1.10 "), backend: "10.0.1.10", want: true},
		"same normalized IPv6": {value: types.StringValue("2001:0db8:0000:0000:0000:0000:0000:0001"), backend: "2001:db8::1", want: true},
		"different address":    {value: types.StringValue("10.0.1.10"), backend: "10.0.1.11"},
		"invalid configured":   {value: types.StringValue("not-an-address"), backend: "10.0.1.10"},
		"invalid backend":      {value: types.StringValue("10.0.1.10"), backend: "not-an-address"},
		"null":                 {value: types.StringNull(), backend: "10.0.1.10"},
		"unknown":              {value: types.StringUnknown(), backend: "10.0.1.10"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := SameIPAddress(tt.value, tt.backend); got != tt.want {
				t.Fatalf("SameIPAddress() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSameMACAddress(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value   types.String
		backend string
		want    bool
	}{
		"same normalized address": {value: types.StringValue(" AA-BB-CC-DD-EE-FF "), backend: "aa:bb:cc:dd:ee:ff", want: true},
		"different address":       {value: types.StringValue("aa:bb:cc:dd:ee:ff"), backend: "aa:bb:cc:dd:ee:00"},
		"invalid configured":      {value: types.StringValue("not-a-mac"), backend: "aa:bb:cc:dd:ee:ff"},
		"invalid backend":         {value: types.StringValue("aa:bb:cc:dd:ee:ff"), backend: "not-a-mac"},
		"null":                    {value: types.StringNull(), backend: "aa:bb:cc:dd:ee:ff"},
		"unknown":                 {value: types.StringUnknown(), backend: "aa:bb:cc:dd:ee:ff"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := SameMACAddress(tt.value, tt.backend); got != tt.want {
				t.Fatalf("SameMACAddress() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSameUUIDSet(t *testing.T) {
	t.Parallel()

	first := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	second := "00000000-0000-0000-0000-000000000002"
	stringSet := func(values ...attr.Value) types.Set {
		return types.SetValueMust(types.StringType, values)
	}
	tests := map[string]struct {
		configured types.Set
		backend    types.Set
		want       bool
	}{
		"same values in different representations": {
			configured: stringSet(types.StringValue(strings.ToUpper(first)), types.StringValue(second)),
			backend:    stringSet(types.StringValue(second), types.StringValue(first)),
			want:       true,
		},
		"same empty sets": {
			configured: stringSet(),
			backend:    stringSet(),
			want:       true,
		},
		"different UUID": {
			configured: stringSet(types.StringValue(first)),
			backend:    stringSet(types.StringValue(second)),
		},
		"different lengths": {
			configured: stringSet(types.StringValue(first)),
			backend:    stringSet(types.StringValue(first), types.StringValue(second)),
		},
		"invalid configured UUID": {
			configured: stringSet(types.StringValue("not-a-uuid")),
			backend:    stringSet(types.StringValue(first)),
		},
		"invalid backend UUID": {
			configured: stringSet(types.StringValue(first)),
			backend:    stringSet(types.StringValue("not-a-uuid")),
		},
		"unknown configured element": {
			configured: stringSet(types.StringUnknown()),
			backend:    stringSet(types.StringValue(first)),
		},
		"non-string configured element": {
			configured: types.SetValueMust(types.Int64Type, []attr.Value{types.Int64Value(1)}),
			backend:    stringSet(types.StringValue(first)),
		},
		"null configured set": {
			configured: types.SetNull(types.StringType),
			backend:    stringSet(),
		},
		"unknown backend set": {
			configured: stringSet(),
			backend:    types.SetUnknown(types.StringType),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := SameUUIDSet(tt.configured, tt.backend); got != tt.want {
				t.Fatalf("SameUUIDSet() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestSameJSON(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		configured types.String
		backend    types.String
		want       bool
	}{
		"key order and whitespace": {types.StringValue(`{ "b": "two", "a": "one" }`), types.StringValue(`{"a":"one","b":"two"}`), true},
		"nested values":            {types.StringValue(`{"items":[true,null,{"n":1.0}]}`), types.StringValue(`{"items":[true,null,{"n":1}]}`), true},
		"empty object":             {types.StringValue("{}"), types.StringValue("{}"), true},
		"different value":          {types.StringValue(`{"a":"one"}`), types.StringValue(`{"a":"two"}`), false},
		"different type":           {types.StringValue(`{"a":"1"}`), types.StringValue(`{"a":1}`), false},
		"array order":              {types.StringValue(`[1,2]`), types.StringValue(`[2,1]`), false},
		"invalid configured":       {types.StringValue(`{`), types.StringValue(`{}`), false},
		"invalid backend":          {types.StringValue(`{}`), types.StringValue(`{`), false},
		"trailing document":        {types.StringValue(`{} {}`), types.StringValue(`{}`), false},
		"null configured":          {types.StringNull(), types.StringValue(`{}`), false},
		"null backend":             {types.StringValue(`{}`), types.StringNull(), false},
		"unknown configured":       {types.StringUnknown(), types.StringValue(`{}`), false},
		"unknown backend":          {types.StringValue(`{}`), types.StringUnknown(), false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := SameJSON(tt.configured, tt.backend); got != tt.want {
				t.Fatalf("SameJSON() = %t, want %t", got, tt.want)
			}
		})
	}
}

func mustParseUUID(t *testing.T, value string) core.UUID {
	t.Helper()

	id, err := core.ParseUUID(value)
	if err != nil {
		t.Fatalf("parse test UUID %q: %v", value, err)
	}
	return id
}

func TestPreservedString(t *testing.T) {
	t.Parallel()

	const backend = "urn:role:agent_builder:viewer"
	padded := types.StringValue(" " + backend + "\n")
	tests := map[string]struct {
		configured types.String
		want       types.String
	}{
		"outer whitespace keeps configured spelling": {configured: padded, want: padded},
		"same value keeps configured value":          {configured: types.StringValue(backend), want: types.StringValue(backend)},
		"inner whitespace takes backend value":       {configured: types.StringValue("urn:role: agent_builder:viewer"), want: types.StringValue(backend)},
		"case change takes backend value":            {configured: types.StringValue(strings.ToUpper(backend)), want: types.StringValue(backend)},
		"null takes backend value":                   {configured: types.StringNull(), want: types.StringValue(backend)},
		"unknown takes backend value":                {configured: types.StringUnknown(), want: types.StringValue(backend)},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := PreservedString(tt.configured, backend); !got.Equal(tt.want) {
				t.Fatalf("PreservedString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSameStringPairs(t *testing.T) {
	t.Parallel()

	const id = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	tests := map[string]struct {
		same func(v, w types.String) bool
		v, w types.String
		want bool
	}{
		"trimmed equal":        {same: SameTrimmedString, v: types.StringValue(" hn "), w: types.StringValue("hn"), want: true},
		"trimmed differ":       {same: SameTrimmedString, v: types.StringValue("hn"), w: types.StringValue("hcm")},
		"trimmed state null":   {same: SameTrimmedString, v: types.StringValue("hn"), w: types.StringNull()},
		"trimmed plan unknown": {same: SameTrimmedString, v: types.StringUnknown(), w: types.StringValue("hn")},
		"uuid padded upper":    {same: SameUUIDString, v: types.StringValue(" " + strings.ToUpper(id)), w: types.StringValue(id), want: true},
		"uuid differ":          {same: SameUUIDString, v: types.StringValue(id), w: types.StringValue("00000000-0000-0000-0000-000000000001")},
		"uuid state invalid":   {same: SameUUIDString, v: types.StringValue(id), w: types.StringValue("x")},
		"ip padded":            {same: SameIPAddressString, v: types.StringValue(" 10.0.0.1 "), w: types.StringValue("10.0.0.1"), want: true},
		"ip state null":        {same: SameIPAddressString, v: types.StringValue("10.0.0.1"), w: types.StringNull()},
		"mac separators":       {same: SameMACAddressString, v: types.StringValue("AA-BB-CC-DD-EE-FF"), w: types.StringValue("aa:bb:cc:dd:ee:ff"), want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tt.same(tt.v, tt.w); got != tt.want {
				t.Fatalf("got %t, want %t", got, tt.want)
			}
		})
	}
}
