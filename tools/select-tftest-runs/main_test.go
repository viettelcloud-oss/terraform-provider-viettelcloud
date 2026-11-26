package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliFile = `# Index.

run "setup" {
  state_key = "setup"
}

run "create" {
  state_key = "subject"
  variables {
    id = run.setup.id
  }
}

run "update" {
  state_key = "subject"
}
`

// writeTestFile writes src as tests/subject.tftest.hcl and returns its path
// and the path of its copy.
func writeTestFile(t *testing.T, src string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tests")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "subject.tftest.hcl")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(dir, "subject.selected.tftest.hcl")
}

func TestExecuteList(t *testing.T) {
	path, output := writeTestFile(t, cliFile)
	var stdout, stderr bytes.Buffer
	if err := execute([]string{"-list", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	want := `setup  (state_key "setup", keeps 1 runs)
create  (state_key "subject", keeps 2 runs)
update  (state_key "subject", keeps 3 runs)
`
	if got := stdout.String(); got != want {
		t.Errorf("stdout =\n%s\nwant\n%s", got, want)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Errorf("-list wrote %s", output)
	}
}

func TestExecuteSelect(t *testing.T) {
	path, output := writeTestFile(t, cliFile)
	var stdout, stderr bytes.Buffer
	if err := execute([]string{"-run", "create", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "tests/subject.selected.tftest.hcl\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "kept 2 of 3 runs\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	copied, err := os.ReadFile(filepath.Clean(output))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(copied), `run "setup" {`) || strings.Contains(string(copied), `run "update" {`) {
		t.Errorf("copy =\n%s\nwant setup and create only", copied)
	}
}

func TestExecuteErrorsDoNotWriteCopy(t *testing.T) {
	tests := map[string]struct {
		src  string
		args func(path string) []string
		want string
	}{
		"no file": {
			src:  cliFile,
			args: func(string) []string { return []string{"-run", "create"} },
			want: "name exactly one test file",
		},
		"two files": {
			src:  cliFile,
			args: func(path string) []string { return []string{"-run", "create", path, path} },
			want: "name exactly one test file",
		},
		"no run": {
			src:  cliFile,
			args: func(path string) []string { return []string{path} },
			want: "pass -run at least once",
		},
		"copy as source": {
			src: cliFile,
			args: func(path string) []string {
				return []string{"-run", "create", strings.TrimSuffix(path, suffix) + selectedSuffix}
			},
			want: "is not a source .tftest.hcl file",
		},
		"unknown run": {
			src:  cliFile,
			args: func(path string) []string { return []string{"-run", "missing", path} },
			want: `unknown run "missing"`,
		},
		"no runs": {
			src:  "variables {}\n",
			args: func(path string) []string { return []string{"-list", path} },
			want: "has no run blocks",
		},
		"parse failure": {
			src:  "run \"a\" {\n  variables {\n    id = run.missing.id\n  }\n}\n",
			args: func(path string) []string { return []string{"-run", "a", path} },
			want: "references unknown run.missing",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			path, output := writeTestFile(t, tt.src)
			if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			err := execute(tt.args(path), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("execute error = %v, want it to contain %q", err, tt.want)
			}
			if got, _ := os.ReadFile(filepath.Clean(output)); string(got) != "existing" {
				t.Errorf("copy = %q, want it unchanged", got)
			}
		})
	}
}
