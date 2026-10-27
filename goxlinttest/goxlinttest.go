// Package goxlinttest is the RuleTester for goxlint rules.
//
//	func TestNoFoo(t *testing.T) {
//		goxlinttest.Run(t, NoFoo, goxlinttest.Cases{
//			Valid: []goxlinttest.Case{
//				{Code: `var bar = 1`},
//			},
//			Invalid: []goxlinttest.Case{
//				{
//					Code:   `var foo = 1`,
//					Errors: []goxlinttest.Error{{MessageID: "foo", Line: 1}},
//					Output: `var bar = 1`,
//				},
//			},
//		})
//	}
//
// Code without a package clause is wrapped in "package p". Line numbers in
// Errors are relative to Code as written. Every case is type-checked, so code
// must compile, apart from unused variables and imports.
// Use RunPackage to test several source files together as one package.
package goxlinttest

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/internal/engine"
)

// Cases are the inputs for one rule.
type Cases struct {
	Valid   []Case
	Invalid []Case
}

// Case is one test input.
type Case struct {
	Name string
	Code string
	// Filename defaults to "p.go". Use a _test.go name to test test-only behavior.
	Filename string
	// Options are encoded to JSON and passed to the rule like config options.
	Options any
	// Errors are the expected diagnostics, in position order. Invalid cases only.
	Errors []Error
	// Output is the expected code after applying every fix. Leave empty when
	// the rule reports no fix.
	Output string
}

// Error is an expected diagnostic. Zero fields are not checked.
type Error struct {
	MessageID string
	Message   string
	Line      int
	Column    int
}

// PackageCases are the package inputs for one rule.
type PackageCases struct {
	Valid   []PackageCase
	Invalid []PackageCase
}

// PackageCase is one package test input. Files are type-checked together.
type PackageCase struct {
	Name string
	// Files maps filenames to source. Code without a package clause gets package p.
	Files map[string]string
	// Options are encoded to JSON and applied to every file.
	Options any
	// FileOptions replaces Options for the named files. A literal nil disables
	// the rule for that file, but keeps the file as package-analysis evidence.
	// A typed nil pointer stays enabled and is encoded as JSON null.
	//goxlint:ignore anti-slop/no-any-map-values JSON options accept user-defined structs or maps for each file
	FileOptions map[string]any
	// Errors are ordered by filename, then position. Invalid cases only.
	Errors []PackageError
}

// PackageError is an expected package diagnostic. Zero fields are not checked.
// Line is relative to the source in Files, before an added package clause.
type PackageError struct {
	Filename  string
	MessageID string
	Message   string
	Line      int
	Column    int
}

const ruleID = "test/rule"

// Run tests rule against cases.
func Run(t *testing.T, rule *goxlint.Rule, cases Cases) {
	t.Helper()
	for i, c := range cases.Valid {
		t.Run(caseName("valid", i, c.Name), func(t *testing.T) {
			t.Helper()
			res := lint(t, rule, c)
			for _, d := range res.diags {
				t.Errorf("unexpected diagnostic at %d:%d: %s", res.line(d), d.Start.Column, d.Message)
			}
		})
	}
	for i, c := range cases.Invalid {
		t.Run(caseName("invalid", i, c.Name), func(t *testing.T) {
			t.Helper()
			if len(c.Errors) == 0 {
				t.Fatal("invalid case must list Errors")
			}
			res := lint(t, rule, c)
			checkErrors(t, rule, res, c.Errors)
			checkOutput(t, res, c)
		})
	}
}

// RunPackage tests file visitors and package analysis against multi-file cases.
// Every case must contain at least one file. All files form one Go package,
// even if their filenames contain different directories. Fixes are not checked.
func RunPackage(t *testing.T, rule *goxlint.Rule, cases PackageCases) {
	t.Helper()
	for i, c := range cases.Valid {
		t.Run(caseName("valid", i, c.Name), func(t *testing.T) {
			t.Helper()
			res := lintPackage(t, rule, c)
			checkErrors(t, rule, res, nil)
		})
	}
	for i, c := range cases.Invalid {
		t.Run(caseName("invalid", i, c.Name), func(t *testing.T) {
			t.Helper()
			if len(c.Errors) == 0 {
				t.Fatal("invalid case must list Errors")
			}
			res := lintPackage(t, rule, c)
			want := make([]Error, len(c.Errors))
			for i, w := range c.Errors {
				want[i] = Error{MessageID: w.MessageID, Message: w.Message, Line: w.Line, Column: w.Column}
			}
			checkErrors(t, rule, res, want)
			for i, w := range c.Errors {
				got := res.diags[i].Filename
				if w.Filename != "" && filepath.ToSlash(w.Filename) != filepath.ToSlash(got) {
					t.Errorf("error %d: filename = %q, want %q", i, got, w.Filename)
				}
			}
		})
	}
}

func caseName(kind string, i int, name string) string {
	if name != "" {
		return kind + "/" + name
	}
	return fmt.Sprintf("%s/%d", kind, i)
}

type result struct {
	diags  []engine.Diagnostic
	src    []byte
	prefix string
}

// line converts a diagnostic line back to a line in Case.Code.
func (r result) line(d engine.Diagnostic) int {
	return d.Start.Line - strings.Count(r.prefix, "\n")
}

var (
	mu   sync.Mutex
	fset = token.NewFileSet()
	imp  types.Importer
)

func lint(t *testing.T, rule *goxlint.Rule, c Case) result {
	t.Helper()
	prefix := ""
	if !hasPackageClause(c.Code) {
		prefix = "package p\n\n"
	}
	src := []byte(prefix + c.Code)
	filename := c.Filename
	if filename == "" {
		filename = "p.go"
	}

	opts := encodeOptions(t, c.Options)

	mu.Lock()
	defer mu.Unlock()
	af, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	files := []engine.File{{Fset: fset, AST: af, Filename: filename, Source: src}}
	typeCheck(t, files)
	cr := engine.ConfiguredRule{ID: ruleID, Plugin: "test", Rule: rule, Severity: engine.Error, Options: opts}
	diags := engine.LintFile(files[0], []engine.ConfiguredRule{cr}, engine.LintOptions{})
	return result{diags: diags, src: src, prefix: prefix}
}

func lintPackage(t *testing.T, rule *goxlint.Rule, c PackageCase) result {
	t.Helper()
	if len(c.Files) == 0 {
		t.Fatal("package case must contain Files")
	}
	for name := range c.FileOptions {
		if _, ok := c.Files[name]; !ok {
			t.Fatalf("FileOptions names unknown file %q", name)
		}
	}
	options := encodeOptions(t, c.Options)
	rules := make(map[string][]engine.ConfiguredRule, len(c.Files))
	var files []engine.File
	var prefixes []int

	mu.Lock()
	defer mu.Unlock()
	for _, name := range slices.Sorted(maps.Keys(c.Files)) {
		if name == "" {
			t.Fatal("package filename must not be empty")
		}
		code := c.Files[name]
		prefix := ""
		if !hasPackageClause(code) {
			prefix = "package p\n\n"
		}
		src := []byte(prefix + code)
		af, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		files = append(files, engine.File{Fset: fset, AST: af, Filename: name, Source: src})
		prefixes = append(prefixes, strings.Count(prefix, "\n"))
		opts := options
		if override, ok := c.FileOptions[name]; ok {
			if override == nil {
				continue
			}
			opts = encodeOptions(t, override)
		}
		rules[name] = []engine.ConfiguredRule{{ID: ruleID, Plugin: "test", Rule: rule, Severity: engine.Error, Options: opts}}
	}
	typeCheck(t, files)
	var res result
	for i, diags := range engine.LintAll(files, func(name string) []engine.ConfiguredRule {
		return rules[name]
	}, engine.LintOptions{SinglePackage: true}) {
		for _, d := range diags {
			d.Start.Line -= prefixes[i]
			d.End.Line -= prefixes[i]
			res.diags = append(res.diags, d)
		}
	}
	return res
}

//goxlint:ignore anti-slop/no-any-parameters this JSON boundary immediately marshals user-defined option values
func encodeOptions(t *testing.T, options any) json.RawMessage {
	t.Helper()
	if options == nil {
		return nil
	}
	b, err := json.Marshal(options)
	if err != nil {
		t.Fatalf("encoding options: %v", err)
	}
	return b
}

// typeCheck shares the source importer with single-file cases. Caller holds mu.
func typeCheck(t *testing.T, files []engine.File) {
	t.Helper()
	if imp == nil {
		imp = importer.ForCompiler(fset, "source", nil)
	}
	var syntax []*ast.File
	for _, file := range files {
		syntax = append(syntax, file.AST)
	}
	info := &types.Info{
		Types:        map[ast.Expr]types.TypeAndValue{},
		Defs:         map[*ast.Ident]types.Object{},
		Uses:         map[*ast.Ident]types.Object{},
		Implicits:    map[ast.Node]types.Object{},
		Instances:    map[*ast.Ident]types.Instance{},
		Selections:   map[*ast.SelectorExpr]*types.Selection{},
		Scopes:       map[ast.Node]*types.Scope{},
		FileVersions: map[*ast.File]string{},
	}
	conf := types.Config{Importer: imp, Error: func(err error) {
		msg := err.Error()
		if strings.Contains(msg, "declared and not used") || strings.Contains(msg, "imported and not used") {
			return
		}
		t.Errorf("type error: %v", err)
	}}
	// Type errors were already reported through conf.Error.
	pkg, _ := conf.Check(syntax[0].Name.Name, fset, syntax, info)
	if t.Failed() {
		t.FailNow()
	}

	for i := range files {
		files[i].Pkg, files[i].Info = pkg, info
	}
}

func hasPackageClause(code string) bool {
	for line := range strings.SplitSeq(code, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		return strings.HasPrefix(line, "package ")
	}
	return false
}

func checkErrors(t *testing.T, rule *goxlint.Rule, res result, want []Error) {
	t.Helper()
	for _, d := range res.diags {
		if strings.HasPrefix(d.Message, "internal error") {
			t.Fatalf("%s", d.Message)
		}
	}
	if len(res.diags) != len(want) {
		var got []string
		for _, d := range res.diags {
			got = append(got, fmt.Sprintf("  %s:%d:%d %s", d.Filename, res.line(d), d.Start.Column, d.Message))
		}
		t.Fatalf("got %d diagnostics, want %d:\n%s", len(res.diags), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		d := res.diags[i]
		if w.MessageID != "" {
			tmpl, ok := rule.Meta.Messages[w.MessageID]
			if !ok {
				t.Errorf("error %d: rule has no message ID %q", i, w.MessageID)
			} else if !matchesTemplate(tmpl, d.Message) {
				t.Errorf("error %d: message %q does not come from message ID %q", i, d.Message, w.MessageID)
			}
		}
		if w.Message != "" && w.Message != d.Message {
			t.Errorf("error %d: message = %q, want %q", i, d.Message, w.Message)
		}
		if w.Line != 0 && w.Line != res.line(d) {
			t.Errorf("error %d: line = %d, want %d", i, res.line(d), w.Line)
		}
		if w.Column != 0 && w.Column != d.Start.Column {
			t.Errorf("error %d: column = %d, want %d", i, d.Start.Column, w.Column)
		}
	}
}

func checkOutput(t *testing.T, res result, c Case) {
	t.Helper()
	fixed, applied := engine.ApplyFixes(res.src, res.diags, goxlint.FixDangerous)
	if len(applied) == 0 {
		if c.Output != "" {
			t.Errorf("expected a fix producing:\n%s\nbut the rule reported no fix", c.Output)
		}
		return
	}
	got := strings.TrimPrefix(string(fixed), res.prefix)
	if c.Output == "" {
		t.Errorf("rule reported a fix; set Output to:\n%s", got)
		return
	}
	if got != c.Output {
		t.Errorf("fixed output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, c.Output)
	}
}

// matchesTemplate reports whether msg could be produced from tmpl by
// substituting its {{placeholders}}.
func matchesTemplate(tmpl, msg string) bool {
	var parts []string
	for {
		i := strings.Index(tmpl, "{{")
		if i < 0 {
			parts = append(parts, tmpl)
			break
		}
		j := strings.Index(tmpl[i:], "}}")
		if j < 0 {
			parts = append(parts, tmpl)
			break
		}
		parts = append(parts, tmpl[:i])
		tmpl = tmpl[i+j+2:]
	}
	if !strings.HasPrefix(msg, parts[0]) {
		return false
	}
	msg = msg[len(parts[0]):]
	for _, p := range parts[1:] {
		k := strings.Index(msg, p)
		if p == parts[len(parts)-1] {
			return strings.HasSuffix(msg, p)
		}
		if k < 0 {
			return false
		}
		msg = msg[k+len(p):]
	}
	return msg == ""
}
