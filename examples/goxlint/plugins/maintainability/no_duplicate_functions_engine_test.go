package maintainability

import (
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"testing"

	"github.com/egoisutolabs/goxlint/internal/engine"
)

// These checks need the engine's suppression audit and resolved fixes, which
// RunPackage intentionally does not expose.
func TestDuplicateSuppressionAuditAndFixes(t *testing.T) {
	sources := map[string]string{
		"a.go": "package p\nfunc first(x int) int { return x+1 }",
		"b.go": "package p\n//goxlint:ignore maintainability/no-duplicate-functions shared implementation required\nfunc second(y int) int { return y+1 }",
	}
	if diags := lintDuplicatesWithAudit(t, sources); len(diags) != 0 {
		t.Fatalf("duplicate suppression was not used: %+v", diags)
	}
	sources["b.go"] = "package p\nfunc second(y int) int { return y+1 }"
	diags := lintDuplicatesWithAudit(t, sources)
	if len(diags) != 1 || diags[0].RuleID != "maintainability/no-duplicate-functions" || diags[0].Fix != nil {
		t.Fatalf("unexpected duplicate diagnostic: %+v", diags)
	}
	sources["b.go"] = "package p\n//goxlint:ignore maintainability/no-duplicate-functions deliberately unused\nfunc second(y int) int { return y+2 }"
	diags = lintDuplicatesWithAudit(t, sources)
	if len(diags) != 1 || diags[0].RuleID != "goxlint/unused-suppression" {
		t.Fatalf("missing unused-suppression diagnostic: %+v", diags)
	}
}

func lintDuplicatesWithAudit(t *testing.T, sources map[string]string) []engine.Diagnostic {
	t.Helper()
	var names []string
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	fs := token.NewFileSet()
	var files []engine.File
	var syntax []*ast.File
	for _, name := range names {
		file, err := parser.ParseFile(fs, name, sources[name], parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		syntax = append(syntax, file)
		files = append(files, engine.File{Fset: fs, AST: file, Filename: name, Source: []byte(sources[name])})
	}
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{}, Implicits: map[ast.Node]types.Object{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fs, "source", nil)}
	pkg, err := conf.Check("example.com/p", fs, syntax, info)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		files[i].Pkg, files[i].Info = pkg, info
	}
	configure := func(string) []engine.ConfiguredRule {
		return []engine.ConfiguredRule{{ID: "maintainability/no-duplicate-functions", Plugin: "maintainability", Rule: NoDuplicateFunctions, Severity: engine.Warn, Options: json.RawMessage(`{"minStatements":1}`)}}
	}
	var out []engine.Diagnostic
	for _, diags := range engine.LintAll(files, configure, engine.LintOptions{ReportUnusedSuppressions: engine.Warn}) {
		out = append(out, diags...)
	}
	return out
}
