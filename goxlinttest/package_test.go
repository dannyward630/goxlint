package goxlinttest_test

import (
	"fmt"
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/goxlinttest"
)

func TestRunPackageBothPhasesAndTypes(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(fmt.Sprintf("typed=%t", typed), func(t *testing.T) {
			t.Parallel()
			var visits atomic.Int32
			var packages int
			rule := &goxlint.Rule{
				Meta: goxlint.Meta{NeedsTypes: typed, Messages: map[string]string{"found": "Found {{phase}} {{name}}."}},
				Create: func(ctx *goxlint.Context) goxlint.Visitor {
					if (ctx.TypesInfo != nil) != typed || (ctx.Pkg != nil) != typed {
						t.Error("visitor type information does not match NeedsTypes")
					}
					return goxlint.Visit(goxlint.On(func(fn *ast.FuncDecl) {
						visits.Add(1)
						ctx.Report(goxlint.Diagnostic{Node: fn.Name, MessageID: "found", Data: goxlint.Data{"phase": "file", "name": fn.Name.Name}})
					}))
				},
				AnalyzePackage: func(pkg *goxlint.PackageContext) {
					packages++
					if visits.Load() != 3 {
						t.Errorf("package analysis ran after %d visits, want 3", visits.Load())
					}
					if pkg.Name != "p" || len(pkg.Files) != 2 || pkg.Files[0].Filename != "a.go" || filepath.ToSlash(pkg.Files[1].Filename) != "nested/b.go" {
						t.Errorf("unexpected package: %+v", pkg)
						return
					}
					for _, file := range pkg.Files {
						if (file.TypesInfo != nil) != typed || (file.Pkg != nil) != typed {
							t.Error("package type information does not match NeedsTypes")
						}
						if typed && (file.Pkg != pkg.Files[0].Pkg || file.TypesInfo != pkg.Files[0].TypesInfo || file.Pkg.Scope().Lookup("first") == nil) {
							t.Error("files were not type-checked together")
						}
						// Report in reverse order to check position sorting after both phases.
						for i := len(file.File.Decls) - 1; i >= 0; i-- {
							if fn, ok := file.File.Decls[i].(*ast.FuncDecl); ok {
								file.Report(goxlint.Diagnostic{Node: fn.Name, MessageID: "found", Data: goxlint.Data{"phase": "package", "name": fn.Name.Name}})
							}
						}
					}
				},
			}
			goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
				Invalid: []goxlinttest.PackageCase{{
					Files: map[string]string{
						"a.go":                          "func first() int { return value }\nvar value = 1\nfunc next() {}",
						filepath.Join("nested", "b.go"): "package p\n//line virtual.go:100\nfunc second() int { return first() }",
					},
					Errors: []goxlinttest.PackageError{
						{Filename: "a.go", MessageID: "found", Message: "Found file first.", Line: 1, Column: 6},
						{Filename: "a.go", MessageID: "found", Message: "Found package first.", Line: 1, Column: 6},
						{Filename: "a.go", Message: "Found file next.", Line: 3, Column: 6},
						{Filename: "a.go", Message: "Found package next.", Line: 3, Column: 6},
						{Filename: "nested/b.go", Message: "Found file second.", Line: 3, Column: 6},
						{Filename: "nested/b.go", Message: "Found package second.", Line: 3, Column: 6},
					},
				}},
			})
			if packages != 1 {
				t.Errorf("AnalyzePackage ran %d times, want 1", packages)
			}
		})
	}
}

func TestRunPackageOptionsAndDisabledFiles(t *testing.T) {
	t.Parallel()
	type options struct {
		Label string `json:"label"`
		Count int    `json:"count"`
	}
	check := func(ctx *goxlint.Context) {
		opts := options{Label: "default", Count: 1}
		if !ctx.Options(&opts) {
			return
		}
		ctx.Report(goxlint.Diagnostic{Node: ctx.File.Name, Message: fmt.Sprintf("%s %d", opts.Label, opts.Count)})
	}
	rule := &goxlint.Rule{
		Meta: goxlint.Meta{NeedsTypes: true},
		Create: func(ctx *goxlint.Context) goxlint.Visitor {
			if ctx.Filename == "disabled.go" {
				t.Error("Create ran for a disabled file")
			}
			check(ctx)
			return nil
		},
		AnalyzePackage: func(pkg *goxlint.PackageContext) {
			if len(pkg.Files) != 3 {
				t.Errorf("got %d files, want disabled file included", len(pkg.Files))
			}
			for _, ctx := range pkg.Files {
				if ctx.Filename == "disabled.go" {
					if ctx.Enabled || ctx.TypesInfo == nil || ctx.Pkg.Scope().Lookup("evidence") == nil {
						t.Error("disabled file must remain typed evidence")
					}
					opts := options{Label: "unchanged"}
					ctx.Options(&opts)
					if opts.Label != "unchanged" {
						t.Error("disabled file received options")
					}
				}
				check(ctx) // Reports from disabled files must be ignored.
			}
		},
	}
	goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
		Invalid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go":        "package p\nvar _ = evidence",
				"b.go":        "package p",
				"disabled.go": "package p\nconst evidence = 1",
			},
			Options:     options{Label: "common", Count: 9},
			FileOptions: map[string]any{"b.go": map[string]string{"label": "override"}, "disabled.go": nil},
			Errors: []goxlinttest.PackageError{
				{Filename: "a.go", Message: "common 9"}, {Filename: "a.go", Message: "common 9"},
				{Filename: "b.go", Message: "override 1"}, {Filename: "b.go", Message: "override 1"},
			},
		}},
	})
}

func TestRunPackageSuppressions(t *testing.T) {
	t.Parallel()
	report := func(ctx *goxlint.Context, fn *ast.FuncDecl) {
		ctx.Report(goxlint.Diagnostic{Node: fn.Name, Message: "found"})
	}
	rule := &goxlint.Rule{
		Create: func(ctx *goxlint.Context) goxlint.Visitor {
			return goxlint.Visit(goxlint.On(func(fn *ast.FuncDecl) { report(ctx, fn) }))
		},
		AnalyzePackage: func(pkg *goxlint.PackageContext) {
			for _, file := range pkg.Files {
				for _, decl := range file.File.Decls {
					if fn, ok := decl.(*ast.FuncDecl); ok {
						report(file, fn)
					}
				}
			}
		},
	}
	goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go": "//goxlint:ignore test/rule intended\nfunc first() {}",
				"b.go": "//goxlint:ignore-file test/rule intended\npackage p\nfunc second() {}",
				"c.go": "//goxlint:ignore test/rule unused suppression is not audited\nvar value = 1",
			},
		}},
		Invalid: []goxlinttest.PackageCase{{
			Files:  map[string]string{"p.go": "//goxlint:ignore another/rule unrelated\nfunc f() {}"},
			Errors: []goxlinttest.PackageError{{}, {}}, // Zero fields must remain unchecked.
		}},
	})
}

func TestRunPackageAllDisabled(t *testing.T) {
	t.Parallel()
	rule := &goxlint.Rule{
		Create:         func(*goxlint.Context) goxlint.Visitor { t.Error("unexpected Create"); return nil },
		AnalyzePackage: func(*goxlint.PackageContext) { t.Error("unexpected AnalyzePackage") },
	}
	goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Files: map[string]string{"p.go": "var value = 1"}, FileOptions: map[string]any{"p.go": nil},
		}},
	})
}

func TestRunPackageUnusedImportsAndVariables(t *testing.T) {
	t.Parallel()
	goxlinttest.RunPackage(t, &goxlint.Rule{}, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go": "import \"strings\"\nfunc first() { unused := second() }",
				"b.go": "func second() int { return 1 }",
			},
		}},
	})
}

func TestRunPackageFailures(t *testing.T) {
	// Run failing harness cases in this test binary so we can assert that they
	// fail testing.T and retain the useful failure message.
	if scenario := os.Getenv("GOXLINT_PACKAGE_FAILURE"); scenario != "" {
		runPackageFailure(t, scenario)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, message string }{
		{"filename", `filename = "p.go", want "other.go"`},
		{"message", `message = "found", want "other"`},
		{"message ID", `does not come from message ID "other"`},
		{"unknown message ID", `rule has no message ID "missing"`},
		{"line", "line = 1, want 5"},
		{"column", "column = 6, want 8"},
		{"count", "got 1 diagnostics, want 2"},
		{"valid diagnostic", "p.go:1:6 found"},
		{"no errors", "invalid case must list Errors"},
		{"package panic", "internal error in rule: panic in AnalyzePackage: broken package"},
		{"visitor panic", "broken visitor"},
		{"bad report", "internal error in rule:"},
		{"parse", "parse error:"},
		{"type", "type error:"},
		{"disabled type", "undefined: missing"},
		{"mixed packages", "type error:"},
		{"empty files", "package case must contain Files"},
		{"empty filename", "package filename must not be empty"},
		{"unknown file option", `FileOptions names unknown file "missing.go"`},
		{"options encoding", "encoding options:"},
		{"override encoding", "encoding options:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(executable, "-test.run=^TestRunPackageFailures$")
			cmd.Env = append(os.Environ(), "GOXLINT_PACKAGE_FAILURE="+tc.name)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.message) {
				t.Fatalf("want failure containing %q; got %v:\n%s", tc.message, err, out)
			}
		})
	}
}

func runPackageFailure(t *testing.T, scenario string) {
	t.Helper()
	rule := &goxlint.Rule{
		Meta: goxlint.Meta{Messages: map[string]string{"found": "found", "other": "other"}},
		AnalyzePackage: func(pkg *goxlint.PackageContext) {
			ctx := pkg.Files[0]
			ctx.Report(goxlint.Diagnostic{Node: ctx.File.Decls[0].(*ast.FuncDecl).Name, MessageID: "found"})
		},
	}
	c := goxlinttest.PackageCase{
		Files:  map[string]string{"p.go": "func f() {}"},
		Errors: []goxlinttest.PackageError{{Filename: "p.go", MessageID: "found", Message: "found", Line: 1, Column: 6}},
	}
	switch scenario {
	case "filename":
		c.Errors[0].Filename = "other.go"
	case "message":
		c.Errors[0].Message = "other"
	case "message ID":
		c.Errors[0].MessageID = "other"
	case "unknown message ID":
		c.Errors[0].MessageID = "missing"
	case "line":
		c.Errors[0].Line = 5
	case "column":
		c.Errors[0].Column = 8
	case "count":
		c.Errors = append(c.Errors, goxlinttest.PackageError{})
	case "valid diagnostic":
		goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
			Valid: []goxlinttest.PackageCase{c},
		})
		return
	case "no errors":
		c.Errors = nil
	case "package panic":
		rule.AnalyzePackage = func(*goxlint.PackageContext) { panic("broken package") }
		// Internal errors must fail even if the caller expects their exact text.
		c.Errors = []goxlinttest.PackageError{{Message: "internal error in rule: panic in AnalyzePackage: broken package"}}
	case "visitor panic":
		rule.Create = func(*goxlint.Context) goxlint.Visitor { panic("broken visitor") }
	case "bad report":
		rule.AnalyzePackage = func(pkg *goxlint.PackageContext) {
			pkg.Files[0].Report(goxlint.Diagnostic{MessageID: "missing"})
		}
	case "parse":
		c.Files["p.go"] = "func {"
	case "type", "disabled type":
		c.Files["p.go"] = "func f() { missing() }"
		if scenario == "disabled type" {
			c.FileOptions = map[string]any{"p.go": nil}
		}
	case "mixed packages":
		c.Files["other.go"] = "package other"
	case "empty files":
		c.Files = nil
	case "empty filename":
		c.Files = map[string]string{"": "package p"}
	case "unknown file option":
		c.FileOptions = map[string]any{"missing.go": nil}
	case "options encoding":
		c.Options = make(chan int)
	case "override encoding":
		c.FileOptions = map[string]any{"p.go": make(chan int)}
	default:
		t.Fatalf("unknown failure scenario %q", scenario)
	}
	goxlinttest.RunPackage(t, rule, goxlinttest.PackageCases{
		Invalid: []goxlinttest.PackageCase{c},
	})
}
