// Command select-tftest-runs writes a copy of a Terraform test file that runs
// only the named run blocks and the runs that they need.
//
// `terraform test -filter` selects whole files and has no run filter. The copy
// keeps each named run, each earlier run in the same state, and each run that a
// kept run references through `run.<name>`, together with the earlier runs in
// the state of that run. Every block other than a run stays, and so do the
// runs that those blocks reference.
//
// Usage:
//
//	select-tftest-runs -list FILE
//	select-tftest-runs -run NAME [-run NAME]... FILE
//
// The copy goes next to FILE as `<name>.selected.tftest.hcl`.
// select-tftest-runs prints its path relative to the parent of the test
// directory, which is the form that `terraform -chdir=integration-tests test
// -filter` expects.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	suffix         = ".tftest.hcl"
	selectedSuffix = ".selected" + suffix
)

// names collects the values of a repeated flag.
type names []string

func (n *names) String() string { return strings.Join(*n, ",") }

func (n *names) Set(value string) error {
	*n = append(*n, value)
	return nil
}

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "select-tftest-runs: %v\n", err)
		os.Exit(1)
	}
}

func execute(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("select-tftest-runs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	list := flags.Bool("list", false, "list the runs in FILE and exit")
	var targets names
	flags.Var(&targets, "run", "name of a run to keep; repeat for more runs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("name exactly one test file after the flags")
	}
	path := flags.Arg(0)
	if !strings.HasSuffix(path, suffix) || strings.HasSuffix(path, selectedSuffix) {
		return fmt.Errorf("%s is not a source %s file", path, suffix)
	}
	if !*list && len(targets) == 0 {
		return errors.New("pass -run at least once, or pass -list")
	}

	dir, name := filepath.Split(path)
	root, err := os.OpenRoot(cmp.Or(dir, "."))
	if err != nil {
		return err
	}
	defer root.Close()
	src, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	f, err := parse(src, path)
	if err != nil {
		return err
	}
	if len(f.runs) == 0 {
		return fmt.Errorf("%s has no run blocks", path)
	}

	if *list {
		return f.list(stdout)
	}
	kept, err := f.keep(targets)
	if err != nil {
		return err
	}
	output := strings.TrimSuffix(name, suffix) + selectedSuffix
	if err := root.WriteFile(output, f.render(filterPath(path), targets, kept), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "kept %d of %d runs\n", len(kept), len(f.runs))
	fmt.Fprintln(stdout, filterPath(filepath.Join(dir, output)))
	return nil
}

// list prints each run in file order with its state and the number of runs
// that selecting it keeps.
func (f *testFile) list(w io.Writer) error {
	for _, r := range f.runs {
		kept, err := f.keep([]string{r.name})
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s  (%s, keeps %d runs)\n", r.name, r.state, len(kept))
	}
	return nil
}

// filterPath returns path relative to the parent of its directory, such as
// tests/vpc_resource.tftest.hcl.
func filterPath(path string) string {
	dir, file := filepath.Split(filepath.Clean(path))
	return filepath.ToSlash(filepath.Join(filepath.Base(dir), file))
}
