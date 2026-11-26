package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *testFile {
	t.Helper()
	f, err := parse([]byte(src), "test.tftest.hcl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func runNames(f *testFile, kept map[string]bool) []string {
	var got []string
	for _, r := range f.runs {
		if kept[r.name] {
			got = append(got, r.name)
		}
	}
	return got
}

func TestParseStates(t *testing.T) {
	f := mustParse(t, `
run "root" {}
run "explicitMain" {
  state_key = "main"
}
run "compactModule" {
  module { source = "./fixtures/a" }
}
run "otherModule" {
  module {
    source = "./fixtures/b"
  }
}
run "keyWinsOverModule" {
  state_key = "./fixtures/a"
  module { source = "./fixtures/a" }
}
`)
	want := map[string]state{
		"root":              {kind: rootState},
		"explicitMain":      {kind: keyState, name: "main"},
		"compactModule":     {kind: moduleState, name: "./fixtures/a"},
		"otherModule":       {kind: moduleState, name: "./fixtures/b"},
		"keyWinsOverModule": {kind: keyState, name: "./fixtures/a"},
	}
	for name, s := range want {
		if got := f.byName[name].state; got != s {
			t.Errorf("run %q state = %v, want %v", name, got, s)
		}
	}
}

func TestParseReferences(t *testing.T) {
	f := mustParse(t, `
run "a" {}
run "b" {}
run "c" {}
run "d" {}
run "e" {}
run "f" {}

run "subject" {
  # This run replaces run.a.
  variables {
    plain      = run.b.id
    bracket    = run["c"].id
    template   = "id-${run.d.id}"
    literal    = "run.a.id"
    heredoc    = <<-EOT
      ${run.e.id}
      run.a.id
    EOT
    collection = [for x in run.f.ids : x]
    local      = [for run in var.items : run.id]
    directive  = "%{for run in var.items}${run}%{endfor}"
  }
}
`)
	got := slices.Sorted(func(yield func(string) bool) {
		for name := range f.byName["subject"].references {
			if !yield(name) {
				return
			}
		}
	})
	if want := []string{"b", "c", "d", "e", "f"}; !slices.Equal(got, want) {
		t.Errorf("references = %v, want %v", got, want)
	}
}

func TestParseSharedReferences(t *testing.T) {
	f := mustParse(t, `
provider "viettelcloud" {
  token = run.providerSetup.token
}

variables {
  vpc_id = run.variablesSetup.id
}

run "providerSetup" {}
run "variablesSetup" {}
`)
	if !f.shared["providerSetup"] || !f.shared["variablesSetup"] || len(f.shared) != 2 {
		t.Errorf("shared = %v, want providerSetup and variablesSetup", f.shared)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"syntax error": {
			src:  `run "a" {`,
			want: "Unclosed configuration block",
		},
		"duplicate run": {
			src:  "run \"a\" {}\nrun \"a\" {}\n",
			want: `duplicate run "a"`,
		},
		"run without name": {
			src:  "run {}\n",
			want: "a run block needs one non-empty name",
		},
		"interpolated state key": {
			src:  "run \"a\" {\n  state_key = \"${var.key}\"\n}\n",
			want: "state_key must be a non-empty literal string",
		},
		"module without source": {
			src:  "run \"a\" {\n  module {}\n}\n",
			want: "module block has no source",
		},
		"computed run name": {
			src:  "run \"a\" {\n  variables {\n    id = run[var.name].id\n  }\n}\n",
			want: "a run reference must name the run literally",
		},
		"unknown run reference": {
			src:  "run \"a\" {\n  variables {\n    id = run.missing.id\n  }\n}\n",
			want: `run "a" references unknown run.missing`,
		},
		"unknown provider reference": {
			src:  "provider \"p\" {\n  token = run.missing.token\n}\n\nrun \"a\" {}\n",
			want: "a block other than run references unknown run.missing",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parse([]byte(tt.src), "test.tftest.hcl")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

const chainedFile = `
provider "p" {
  token = run.providerSetup.token
}

run "providerSetup" {
  state_key = "provider"
}

run "createNetwork" {
  state_key = "network"
}

run "updateNetwork" {
  state_key = "network"
}

run "createServer" {
  state_key = "server"
  variables {
    network_id = run.createNetwork.id
  }
}

run "updateServer" {
  state_key = "server"
}

run "importServer" {
  state_key = "import"
  variables {
    id = run.createServer.id
  }
}

run "deleteServer" {
  state_key = "server"
}

run "lookupRoot" {}
`

func TestKeep(t *testing.T) {
	f := mustParse(t, chainedFile)
	tests := map[string]struct {
		targets []string
		want    []string
	}{
		"first run of a state": {
			targets: []string{"createNetwork"},
			want:    []string{"providerSetup", "createNetwork"},
		},
		"earlier runs of the same state and the referenced prefix": {
			targets: []string{"updateServer"},
			want:    []string{"providerSetup", "createNetwork", "createServer", "updateServer"},
		},
		"transitive reference keeps only the referenced prefix": {
			targets: []string{"importServer"},
			want:    []string{"providerSetup", "createNetwork", "createServer", "importServer"},
		},
		"several targets": {
			targets: []string{"updateNetwork", "lookupRoot"},
			want:    []string{"providerSetup", "createNetwork", "updateNetwork", "lookupRoot"},
		},
		"last run keeps its whole state": {
			targets: []string{"deleteServer"},
			want:    []string{"providerSetup", "createNetwork", "createServer", "updateServer", "deleteServer"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			kept, err := f.keep(tt.targets)
			if err != nil {
				t.Fatal(err)
			}
			if got := runNames(f, kept); !slices.Equal(got, tt.want) {
				t.Errorf("kept = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKeepUnknownRun(t *testing.T) {
	f := mustParse(t, chainedFile)
	if _, err := f.keep([]string{"missing"}); err == nil || !strings.Contains(err.Error(), `unknown run "missing"`) {
		t.Fatalf("keep error = %v, want unknown run", err)
	}
}

func TestRender(t *testing.T) {
	src := strings.Join([]string{
		"# Index of the file.",
		"#   run \"keep\" {",
		"#   run \"drop\" {",
		"",
		"variables {",
		"  region = \"r1\"",
		"}",
		"",
		"# Comment for keep.",
		"run \"keep\" {",
		"  variables {",
		"    description = <<-EOT",
		"      first",
		"",
		"",
		"      last",
		"    EOT",
		"  }",
		"}",
		"",
		"# Comment for drop.",
		"// Second comment for drop.",
		"run \"drop\" { # A separate chain.",
		"  state_key = \"other\"",
		"} # Trailing comment for drop.",
		"",
		"run \"after\" {",
		"  state_key = \"after\"",
		"}",
		"",
	}, "\n")
	f := mustParse(t, src)
	kept, err := f.keep([]string{"keep", "after"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# Generated by tools/select-tftest-runs from tests/x.tftest.hcl. Do not edit or commit.",
		"# Selected runs: keep, after.",
		"# Kept 2 of 3 runs.",
		"",
		"variables {",
		"  region = \"r1\"",
		"}",
		"",
		"# Comment for keep.",
		"run \"keep\" {",
		"  variables {",
		"    description = <<-EOT",
		"      first",
		"",
		"",
		"      last",
		"    EOT",
		"  }",
		"}",
		"",
		"run \"after\" {",
		"  state_key = \"after\"",
		"}",
		"",
	}, "\n")
	if got := string(f.render("tests/x.tftest.hcl", []string{"keep", "after"}, kept)); got != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderPreservesCRLF(t *testing.T) {
	src := "run \"keep\" {\r\n  variables {\r\n    text = <<EOT\r\na\r\n\r\nb\r\nEOT\r\n  }\r\n}\r\n\r\nrun \"drop\" {\r\n  state_key = \"other\"\r\n}\r\n"
	f := mustParse(t, src)
	kept, err := f.keep([]string{"keep"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(f.render("tests/x.tftest.hcl", []string{"keep"}, kept))
	body := "run \"keep\" {\r\n  variables {\r\n    text = <<EOT\r\na\r\n\r\nb\r\nEOT\r\n  }\r\n}\r\n\r\n"
	if !strings.HasSuffix(got, body) {
		t.Errorf("render = %q, want suffix %q", got, body)
	}
}

func TestRepositoryTestFiles(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "integration-tests", "tests", "*"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no repository test files found")
	}
	for _, path := range paths {
		if strings.HasSuffix(path, selectedSuffix) {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			f, err := parse(src, path)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.runs) == 0 {
				t.Fatal("no runs")
			}
			for _, r := range f.runs {
				kept, err := f.keep([]string{r.name})
				if err != nil {
					t.Fatal(err)
				}
				for name := range r.references {
					if !kept[name] {
						t.Errorf("run %q: referenced run %q is not kept", r.name, name)
					}
				}
				out := string(f.render(path, []string{r.name}, kept))
				if _, err := parse([]byte(out), path); err != nil {
					t.Errorf("run %q: copy does not parse: %v", r.name, err)
				}
				if got := strings.Count(out, "\nrun \""); got != len(kept) {
					t.Errorf("run %q: copy has %d runs, want %d", r.name, got, len(kept))
				}
			}
		})
	}
}
