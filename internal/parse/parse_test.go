package parse

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
)

const testUUID = "00000000-0000-0000-0000-000000000001"

func TestUUIDString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value          types.String
		want           string
		wantSummary    string
		wantDetailPart string
	}{
		"known": {
			value: types.StringValue(testUUID),
			want:  testUUID,
		},
		"null": {
			value:          types.StringNull(),
			wantSummary:    "Missing UUID value",
			wantDetailPart: "project_id must be provided.",
		},
		"unknown": {
			value:          types.StringUnknown(),
			wantSummary:    "Missing UUID value",
			wantDetailPart: "project_id must be provided.",
		},
		"empty": {
			value:          types.StringValue(""),
			wantSummary:    "Missing UUID value",
			wantDetailPart: "project_id must be provided.",
		},
		"invalid": {
			value:          types.StringValue("not-a-uuid"),
			wantSummary:    "Invalid UUID",
			wantDetailPart: "project_id must be a valid UUID:",
		},
		"whitespace": {
			value:          types.StringValue(" "),
			wantSummary:    "Invalid UUID",
			wantDetailPart: "project_id must be a valid UUID:",
		},
		"padded": {
			value: types.StringValue("\t" + testUUID + "  "),
			want:  testUUID,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, diags := UUIDString(tt.value, "project_id")
			assertUUIDDiagnostics(t, diags, tt.wantSummary, tt.wantDetailPart)

			if tt.wantSummary != "" {
				if got != core.NilUUID {
					t.Fatalf("UUIDString() UUID = %q, want NilUUID", got)
				}
				return
			}
			if got.String() != tt.want {
				t.Fatalf("UUIDString() UUID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOptionalUUIDString(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value          types.String
		want           string
		wantSummary    string
		wantDetailPart string
	}{
		"known": {
			value: types.StringValue(testUUID),
			want:  testUUID,
		},
		"null": {
			value: types.StringNull(),
		},
		"unknown": {
			value: types.StringUnknown(),
		},
		"empty": {
			value: types.StringValue(""),
		},
		"invalid": {
			value:          types.StringValue("not-a-uuid"),
			wantSummary:    "Invalid UUID",
			wantDetailPart: "vpc_id must be a valid UUID:",
		},
		"whitespace": {
			value:          types.StringValue(" "),
			wantSummary:    "Invalid UUID",
			wantDetailPart: "vpc_id must be a valid UUID:",
		},
		"padded": {
			value: types.StringValue("  " + testUUID + "\n"),
			want:  testUUID,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, diags := OptionalUUIDString(tt.value, "vpc_id")
			assertUUIDDiagnostics(t, diags, tt.wantSummary, tt.wantDetailPart)

			if tt.want == "" {
				if got != nil {
					t.Fatalf("OptionalUUIDString() UUID = %q, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("OptionalUUIDString() UUID = nil, want a parsed UUID")
			}
			if got.String() != tt.want {
				t.Fatalf("OptionalUUIDString() UUID = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertUUIDDiagnostics(t *testing.T, diags diag.Diagnostics, wantSummary, wantDetailPart string) {
	t.Helper()

	if wantSummary == "" {
		if diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		return
	}

	if !diags.HasError() {
		t.Fatalf("expected diagnostic %q, got none", wantSummary)
	}
	errors := diags.Errors()
	if len(errors) != 1 {
		t.Fatalf("diagnostic count = %d, want 1: %v", len(errors), diags)
	}
	if got := errors[0].Summary(); got != wantSummary {
		t.Fatalf("diagnostic summary = %q, want %q", got, wantSummary)
	}
	if got := errors[0].Detail(); !strings.Contains(got, wantDetailPart) {
		t.Fatalf("diagnostic detail = %q, want it to contain %q", got, wantDetailPart)
	}
}
