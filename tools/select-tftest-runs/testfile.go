package main

// testfile.go owns parsing a test file, selecting runs, and rendering the copy.

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// stateKind says how Terraform chooses the state of a run.
type stateKind int

const (
	rootState stateKind = iota
	moduleState
	keyState
)

// state identifies the state that a run applies to. Terraform groups runs by
// `state_key`, then by module source, and puts the rest in the root state.
type state struct {
	kind stateKind
	name string
}

func (s state) String() string {
	switch s.kind {
	case keyState:
		return fmt.Sprintf("state_key %q", s.name)
	case moduleState:
		return fmt.Sprintf("module %q", s.name)
	case rootState:
		return "root module"
	}
	return fmt.Sprintf("unknown state kind %d", s.kind)
}

// run is a top-level run block. start and end cover the block, the comment
// lines directly above it, and the rest of its closing line.
type run struct {
	name       string
	index      int
	state      state
	references map[string]bool
	start, end int
}

// testFile is a parsed .tftest.hcl file. shared holds the runs that the
// blocks other than runs reference, because the copy keeps those blocks.
type testFile struct {
	src       []byte
	headerEnd int
	runs      []*run
	byName    map[string]*run
	shared    map[string]bool
}

// parse reads src and reports every syntax error, unsupported state
// identifier, and unknown run reference before a copy is written.
func parse(src []byte, filename string) (*testFile, error) {
	file, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	tokens, diags := hclsyntax.LexConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected body type %T", filename, file.Body)
	}

	f := &testFile{
		src:       src,
		headerEnd: headerEnd(src, tokens),
		byName:    map[string]*run{},
		shared:    map[string]bool{},
	}
	for _, attr := range body.Attributes {
		if err := addReferences(f.shared, attr.Expr); err != nil {
			return nil, err
		}
	}
	for _, block := range body.Blocks {
		if block.Type != "run" {
			if err := bodyReferences(f.shared, block.Body); err != nil {
				return nil, err
			}
			continue
		}
		r, err := parseRun(block, src, tokens, f.headerEnd)
		if err != nil {
			return nil, err
		}
		if _, ok := f.byName[r.name]; ok {
			return nil, fmt.Errorf("%s: duplicate run %q", block.DefRange(), r.name)
		}
		r.index = len(f.runs)
		f.runs = append(f.runs, r)
		f.byName[r.name] = r
	}

	for _, r := range f.runs {
		for name := range r.references {
			if _, ok := f.byName[name]; !ok {
				return nil, fmt.Errorf("run %q references unknown run.%s", r.name, name)
			}
		}
	}
	for name := range f.shared {
		if _, ok := f.byName[name]; !ok {
			return nil, fmt.Errorf("a block other than run references unknown run.%s", name)
		}
	}
	return f, nil
}

func parseRun(block *hclsyntax.Block, src []byte, tokens hclsyntax.Tokens, floor int) (*run, error) {
	if len(block.Labels) != 1 || block.Labels[0] == "" {
		return nil, fmt.Errorf("%s: a run block needs one non-empty name", block.DefRange())
	}
	s, err := runState(block)
	if err != nil {
		return nil, err
	}
	r := &run{name: block.Labels[0], state: s, references: map[string]bool{}}
	if err := bodyReferences(r.references, block.Body); err != nil {
		return nil, err
	}
	r.start, r.end = extent(src, tokens, block.Range(), floor)
	return r, nil
}

func runState(block *hclsyntax.Block) (state, error) {
	if attr, ok := block.Body.Attributes["state_key"]; ok {
		name, err := literal(attr)
		return state{kind: keyState, name: name}, err
	}
	for _, nested := range block.Body.Blocks {
		if nested.Type != "module" {
			continue
		}
		attr, ok := nested.Body.Attributes["source"]
		if !ok {
			return state{}, fmt.Errorf("%s: module block has no source", nested.DefRange())
		}
		name, err := literal(attr)
		return state{kind: moduleState, name: name}, err
	}
	return state{kind: rootState}, nil
}

// literal returns the value of a quoted string without interpolation, because
// the state of a run must be known without evaluating the test file.
func literal(attr *hclsyntax.Attribute) (string, error) {
	tmpl, ok := attr.Expr.(*hclsyntax.TemplateExpr)
	if !ok || !tmpl.IsStringLiteral() {
		return "", fmt.Errorf("%s: %s must be a non-empty literal string", attr.SrcRange, attr.Name)
	}
	value, diags := tmpl.Value(nil)
	if diags.HasErrors() {
		return "", diags
	}
	return value.AsString(), nil
}

func bodyReferences(refs map[string]bool, body *hclsyntax.Body) error {
	for _, attr := range body.Attributes {
		if err := addReferences(refs, attr.Expr); err != nil {
			return err
		}
	}
	for _, block := range body.Blocks {
		if err := bodyReferences(refs, block.Body); err != nil {
			return err
		}
	}
	return nil
}

// addReferences adds the name of every run that expr reads. Variables omits
// the local variables of for expressions, so a loop variable named run is
// not a reference.
func addReferences(refs map[string]bool, expr hclsyntax.Expression) error {
	for _, traversal := range expr.Variables() {
		if traversal.RootName() != "run" {
			continue
		}
		name, ok := runName(traversal)
		if !ok {
			return fmt.Errorf("%s: a run reference must name the run literally", traversal.SourceRange())
		}
		refs[name] = true
	}
	return nil
}

func runName(traversal hcl.Traversal) (string, bool) {
	if len(traversal) < 2 {
		return "", false
	}
	switch step := traversal[1].(type) {
	case hcl.TraverseAttr:
		return step.Name, true
	case hcl.TraverseIndex:
		if step.Key.Type() != cty.String || !step.Key.IsKnown() || step.Key.IsNull() {
			return "", false
		}
		return step.Key.AsString(), true
	default:
		return "", false
	}
}

// headerEnd returns the end of the comment index at the top of the file. The
// copy replaces the index, because the index lists runs that the copy drops.
func headerEnd(src []byte, tokens hclsyntax.Tokens) int {
	for _, token := range tokens {
		if token.Type != hclsyntax.TokenComment && token.Type != hclsyntax.TokenNewline {
			return lineStart(src, token.Range.Start.Byte)
		}
	}
	return len(src)
}

// extent widens the range of a block to the line comments directly above it
// and to the rest of its closing line, so that a dropped run leaves no
// orphaned comment. When blank lines surround the block, the range also takes
// the blank line after it. It never starts before floor.
func extent(src []byte, tokens hclsyntax.Tokens, r hcl.Range, floor int) (int, int) {
	first := tokenAt(tokens, r.Start.Byte)
	start := r.Start.Byte
	if line := lineStart(src, start); isBlank(src[line:start]) {
		start = line
		for i := first - 1; i >= 0; i-- {
			token := tokens[i]
			line := lineStart(src, token.Range.Start.Byte)
			if token.Type != hclsyntax.TokenComment || !isLineComment(token.Bytes) ||
				!isBlank(src[line:token.Range.Start.Byte]) || line < floor {
				break
			}
			start = line
		}
	}

	end := r.End.Byte
	if last := tokenAt(tokens, r.End.Byte-1); last+1 < len(tokens) {
		next := tokens[last+1]
		if next.Type == hclsyntax.TokenNewline ||
			next.Type == hclsyntax.TokenComment && isLineComment(next.Bytes) {
			end = next.Range.End.Byte
		}
	}
	if next := tokenAt(tokens, end); start > 0 && start == lineStart(src, start) &&
		isBlank(src[lineStart(src, start-1):start]) && next < len(tokens) &&
		tokens[next].Type == hclsyntax.TokenNewline && tokens[next].Range.Start.Byte == end {
		end = tokens[next].Range.End.Byte
	}
	return start, end
}

func tokenAt(tokens hclsyntax.Tokens, offset int) int {
	return sort.Search(len(tokens), func(i int) bool {
		return tokens[i].Range.Start.Byte >= offset
	})
}

func lineStart(src []byte, offset int) int {
	return bytes.LastIndexByte(src[:offset], '\n') + 1
}

func isBlank(b []byte) bool {
	return len(bytes.TrimLeft(b, " \t\r\n")) == 0
}

func isLineComment(b []byte) bool {
	return bytes.HasPrefix(b, []byte("#")) || bytes.HasPrefix(b, []byte("//"))
}

// keep returns the targets, each earlier run in the state of a kept run, and
// each run that a kept run or a shared block references. The earlier runs
// build the state that a kept run starts from. Terraform destroys every state
// at the end of the file, so the runs after a target are not needed.
func (f *testFile) keep(targets []string) (map[string]bool, error) {
	pending := slices.Clone(targets)
	for name := range f.shared {
		pending = append(pending, name)
	}
	kept := map[string]bool{}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		target, ok := f.byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown run %q; use -list to see the runs", name)
		}
		for _, r := range f.runs[:target.index+1] {
			if r.state != target.state || kept[r.name] {
				continue
			}
			kept[r.name] = true
			for ref := range r.references {
				pending = append(pending, ref)
			}
		}
	}
	return kept, nil
}

// render returns the copy that holds only the kept runs. Every other byte
// after the comment index is copied unchanged.
func (f *testFile) render(source string, targets []string, kept map[string]bool) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Generated by tools/select-tftest-runs from %s. Do not edit or commit.\n", source)
	fmt.Fprintf(&b, "# Selected runs: %s.\n", strings.Join(targets, ", "))
	fmt.Fprintf(&b, "# Kept %d of %d runs.\n\n", len(kept), len(f.runs))
	cursor := f.headerEnd
	for _, r := range f.runs {
		if kept[r.name] {
			continue
		}
		b.Write(f.src[cursor:r.start])
		cursor = r.end
	}
	b.Write(f.src[cursor:])
	return b.Bytes()
}
