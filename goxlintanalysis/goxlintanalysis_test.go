package goxlintanalysis_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/goxlintanalysis"
)

func identifierRule(kind goxlint.FixKind) *goxlint.Rule {
	return &goxlint.Rule{
		Meta: goxlint.Meta{Name: "no-bad", Category: goxlint.Suspicious, Fixable: kind},
		Create: func(ctx *goxlint.Context) goxlint.Visitor {
			opts := struct {
				Name string `json:"name"`
			}{Name: "bad"}
			if !ctx.Options(&opts) {
				return nil
			}
			return goxlint.Visit(goxlint.On(func(id *ast.Ident) {
				if id.Name != opts.Name {
					return
				}
				d := goxlint.Diagnostic{Node: id, Message: "avoid " + id.Name, Help: "Use good instead."}
				if kind != goxlint.FixNone {
					d.Fix = &goxlint.Fix{Kind: kind, Message: "Rename to good", Edits: []goxlint.Edit{goxlint.ReplaceNode(id, "good")}}
				}
				ctx.Report(d)
			}))
		},
	}
}

func TestVisitorAndSuppressions(t *testing.T) {
	a := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone))
	results := analysistest.Run(t, analysistest.TestData(), a, "visitor")
	if len(results) != 1 || len(results[0].Diagnostics) != 1 {
		t.Fatalf("unexpected results: %+v", results)
	}
	d := results[0].Diagnostics[0]
	if d.Category != "suspicious" || d.Message != "avoid bad\nUse good instead." {
		t.Fatalf("lost metadata: %+v", d)
	}
}

func TestPackageRule(t *testing.T) {
	rule := &goxlint.Rule{Meta: goxlint.Meta{Name: "package-check", NeedsTypes: true}, AnalyzePackage: func(pkg *goxlint.PackageContext) {
		for _, ctx := range pkg.Files {
			if ctx.Pkg == nil || ctx.TypesInfo == nil {
				panic("missing type information")
			}
			ctx.Report(goxlint.Diagnostic{Node: ctx.File.Name, Message: fmt.Sprintf("package has %d files", len(pkg.Files))})
		}
	}}
	analysistest.Run(t, analysistest.TestData(), goxlintanalysis.Analyzer("acme", rule), "packagecheck")
}

func TestSuggestedFixes(t *testing.T) {
	for _, kind := range []goxlint.FixKind{goxlint.FixSafe, goxlint.FixSuggestion} {
		t.Run(kind.String(), func(t *testing.T) {
			a := goxlintanalysis.Analyzer("acme", identifierRule(kind))
			results := analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), a, "fixes")
			fixes := results[0].Diagnostics[0].SuggestedFixes
			if len(fixes) != 1 || fixes[0].Message != "Rename to good" {
				t.Fatalf("missing suggested fix: %+v", fixes)
			}
		})
	}
	results := analysistest.Run(t, analysistest.TestData(), goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixDangerous)), "fixes")
	if len(results[0].Diagnostics[0].SuggestedFixes) != 0 {
		t.Fatal("converted a dangerous fix")
	}
}

func TestOptions(t *testing.T) {
	a := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone), goxlintanalysis.WithOptions(json.RawMessage(`{"name":"custom"}`)))
	analysistest.Run(t, analysistest.TestData(), a, "options")
	for _, raw := range []string{`{"unknown":true}`, `{"name":`, `{"name":123}`} {
		a := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone), goxlintanalysis.WithOptions(json.RawMessage(raw)))
		analysistest.Run(t, analysistest.TestData(), a, "invalidoptions")
	}
}

func TestRuleFailures(t *testing.T) {
	for _, stage := range []string{"create", "visitor", "package", "bad report", "bad fix"} {
		t.Run(stage, func(t *testing.T) {
			rule := &goxlint.Rule{Meta: goxlint.Meta{Name: "broken", Fixable: goxlint.FixSafe}}
			switch stage {
			case "create":
				rule.Create = func(*goxlint.Context) goxlint.Visitor { panic("boom") }
			case "visitor":
				rule.Create = func(*goxlint.Context) goxlint.Visitor {
					return goxlint.Visit(goxlint.On(func(*ast.File) { panic("boom") }))
				}
			case "package":
				rule.AnalyzePackage = func(*goxlint.PackageContext) { panic("boom") }
			case "bad report":
				rule.Create = func(ctx *goxlint.Context) goxlint.Visitor {
					ctx.Report(goxlint.Diagnostic{Message: "missing position"})
					return nil
				}
			case "bad fix":
				rule.Create = func(ctx *goxlint.Context) goxlint.Visitor {
					ctx.Report(goxlint.Diagnostic{Node: ctx.File.Name, Message: "bad edit", Fix: &goxlint.Fix{Kind: goxlint.FixSafe, Edits: []goxlint.Edit{{Pos: token.NoPos, End: token.NoPos}}}})
					return nil
				}
			}
			results := analysistest.Run(t, analysistest.TestData(), goxlintanalysis.Analyzer("acme", rule), "broken")
			if results[0].Err != nil {
				t.Fatalf("rule failure escaped to driver: %v", results[0].Err)
			}
		})
	}
}

func TestNamesAndValidation(t *testing.T) {
	for _, tc := range []struct{ plugin, rule, want string }{
		{"acme", "no-println", "acme_no_println"},
		{"3acme", "no-foo", "_3acme_no_foo"},
		{"acme.v2", "some rule!", "acme_v2_some_rule_"},
		{"équipe", "règle", "équipe_règle"},
		{"", "", "_"},
	} {
		rule := identifierRule(goxlint.FixNone)
		rule.Meta.Name = tc.rule
		a := goxlintanalysis.Analyzer(tc.plugin, rule)
		if a.Name != tc.want {
			t.Errorf("name = %q, want %q", a.Name, tc.want)
		}
		if err := analysis.Validate([]*analysis.Analyzer{a}); err != nil {
			t.Fatal(err)
		}
	}
	rule := func(name string) *goxlint.Rule { r := identifierRule(goxlint.FixNone); r.Meta.Name = name; return r }
	as := goxlintanalysis.Analyzers(
		goxlint.Plugin{Name: "z", Rules: []*goxlint.Rule{rule("last")}},
		goxlint.Plugin{Name: "a", Rules: []*goxlint.Rule{rule("no_foo"), rule("no_foo_2"), rule("no-foo")}},
	)
	var names []string
	for _, a := range as {
		names = append(names, a.Name)
	}
	if !slices.Equal(names, []string{"a_no_foo", "a_no_foo_2", "a_no_foo_2_2", "z_last"}) {
		t.Fatalf("order or collisions: %v", names)
	}
	if err := analysis.Validate(as); err != nil {
		t.Fatal(err)
	}
	if len(goxlintanalysis.Analyzers()) != 0 {
		t.Fatal("empty input returned analyzers")
	}
}

func sourcePass(t *testing.T, sources map[string]string) (*analysis.Pass, *[]analysis.Diagnostic) {
	t.Helper()
	fs := token.NewFileSet()
	var names []string
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	var files []*ast.File
	for _, name := range names {
		file, err := parser.ParseFile(fs, name, sources[name], parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	var diags []analysis.Diagnostic
	return &analysis.Pass{Fset: fs, Files: files, Pkg: types.NewPackage("example.com/p", "p"), Report: func(d analysis.Diagnostic) { diags = append(diags, d) }, ReadFile: func(name string) ([]byte, error) {
		src, ok := sources[name]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(src), nil
	}}, &diags
}

func TestPhysicalPositionsAndDriverSource(t *testing.T) {
	// No file exists on disk. Both rule source access and edits must use the driver snapshot.
	const src = "package p\n//line virtual.go:900:20\nvar bad = 1\n"
	pass, diags := sourcePass(t, map[string]string{"overlay.go": src})
	rule := identifierRule(goxlint.FixSafe)
	create := rule.Create
	rule.Create = func(ctx *goxlint.Context) goxlint.Visitor {
		if string(ctx.Source) != src {
			t.Error("did not use driver source")
		}
		return create(ctx)
	}
	a := goxlintanalysis.Analyzer("acme", rule)
	if _, err := a.Run(pass); err != nil {
		t.Fatal(err)
	}
	if len(*diags) != 1 {
		t.Fatalf("got %+v", *diags)
	}
	d := (*diags)[0]
	pos := pass.Fset.PositionFor(d.Pos, false)
	end := pass.Fset.PositionFor(d.End, false)
	if pos.Filename != "overlay.go" || pos.Line != 3 || pos.Column != 5 || end.Column != 8 {
		t.Fatalf("adjusted or incorrect positions: %v-%v", pos, end)
	}
	edit := d.SuggestedFixes[0].TextEdits[0]
	if edit.Pos != d.Pos || edit.End != d.End || string(edit.NewText) != "good" {
		t.Fatalf("wrong edit: %+v", edit)
	}
}

func TestOnePassIsOnePackage(t *testing.T) {
	pass, diags := sourcePass(t, map[string]string{filepath.Join("one", "a.go"): "package p\n", filepath.Join("two", "b.go"): "package p\n"})
	calls := 0
	rule := &goxlint.Rule{Meta: goxlint.Meta{Name: "package"}, AnalyzePackage: func(pkg *goxlint.PackageContext) {
		calls++
		if len(pkg.Files) != 2 {
			t.Errorf("pass was split into multiple groups: %d", len(pkg.Files))
		}
		for _, ctx := range pkg.Files {
			ctx.Report(goxlint.Diagnostic{Node: ctx.File.Name, Message: "package finding"})
		}
	}}
	if _, err := goxlintanalysis.Analyzer("acme", rule).Run(pass); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(*diags) != 2 {
		t.Fatalf("calls=%d, diags=%+v", calls, *diags)
	}
}

func TestSourceFailuresAndGeneratedFiles(t *testing.T) {
	a := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone))
	pass, diags := sourcePass(t, map[string]string{"p.go": "package p\nvar bad = 1\n"})
	failure := errors.New("snapshot unavailable")
	pass.ReadFile = func(string) ([]byte, error) { return nil, failure }
	if _, err := a.Run(pass); !errors.Is(err, failure) || len(*diags) != 0 {
		t.Fatalf("read failure lost: %v, %+v", err, *diags)
	}
	pass, diags = sourcePass(t, map[string]string{"p.go": "// Code generated by test. DO NOT EDIT.\npackage p\nvar bad = 1\n"})
	pass.ReadFile = func(string) ([]byte, error) { t.Error("read a generated file"); return nil, failure }
	if _, err := a.Run(pass); err != nil || len(*diags) != 0 {
		t.Fatalf("generated file was analyzed: %v, %+v", err, *diags)
	}
	name := filepath.Join(t.TempDir(), "p.go")
	const src = "package p\nvar bad = 1\n"
	if err := os.WriteFile(name, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	pass, diags = sourcePass(t, map[string]string{name: src})
	pass.ReadFile = nil
	if _, err := a.Run(pass); err != nil || len(*diags) != 1 {
		t.Fatalf("legacy driver read failed: %v, %+v", err, *diags)
	}
}

func TestOptionsAreCopiedAndReusable(t *testing.T) {
	raw := json.RawMessage(`{"name":"custom"}`)
	opt := goxlintanalysis.WithOptions(raw)
	copy(raw, []byte(strings.Repeat("x", len(raw))))
	a := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone), goxlintanalysis.WithOptions(json.RawMessage(`{"name":"other"}`)), opt)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			pass, diags := sourcePass(t, map[string]string{"p.go": "package p\nvar custom = 1\n"})
			if _, err := a.Run(pass); err != nil || len(*diags) != 1 {
				t.Errorf("options not stable: %v, %+v", err, *diags)
			}
		})
	}
	wg.Wait()
	b := goxlintanalysis.Analyzer("acme", identifierRule(goxlint.FixNone), opt)
	pass, diags := sourcePass(t, map[string]string{"p.go": "package p\nvar custom = 1\n"})
	if _, err := b.Run(pass); err != nil || len(*diags) != 1 {
		t.Fatalf("option not reusable: %v, %+v", err, *diags)
	}
}

func TestOverlappingFixIsNotSuggested(t *testing.T) {
	rule := &goxlint.Rule{Meta: goxlint.Meta{Name: "fix", Fixable: goxlint.FixSafe}, Create: func(ctx *goxlint.Context) goxlint.Visitor {
		edit := goxlint.ReplaceNode(ctx.File.Name, "other")
		ctx.Report(goxlint.Diagnostic{Node: ctx.File.Name, Message: "overlap", Fix: &goxlint.Fix{Kind: goxlint.FixSafe, Edits: []goxlint.Edit{edit, edit}}})
		return nil
	}}
	pass, diags := sourcePass(t, map[string]string{"p.go": "package p\n"})
	if _, err := goxlintanalysis.Analyzer("acme", rule).Run(pass); err != nil {
		t.Fatal(err)
	}
	if len(*diags) != 1 || len((*diags)[0].SuggestedFixes) != 0 {
		t.Fatalf("invalid fix exposed to driver: %+v", *diags)
	}
}
