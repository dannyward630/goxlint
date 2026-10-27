package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/internal/config"
)

func writeTestFile(t *testing.T, name, source string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testPlugin(typed bool) goxlint.Plugin {
	return goxlint.Plugin{Name: "test", Rules: []*goxlint.Rule{{
		Meta: goxlint.Meta{Name: "rename", Category: goxlint.Correctness, NeedsTypes: typed, Fixable: goxlint.FixSafe},
		Create: func(ctx *goxlint.Context) goxlint.Visitor {
			return goxlint.Visit(goxlint.On(func(id *ast.Ident) {
				if id.Name == "bad" {
					ctx.Report(goxlint.Diagnostic{Node: id, Message: "rename bad", Fix: &goxlint.Fix{
						Kind: goxlint.FixSafe, Edits: []goxlint.Edit{goxlint.ReplaceNode(id, "good")},
					}})
				}
			}))
		},
	}}}
}

func rulesTestPlugin() goxlint.Plugin {
	fileRule := func(*goxlint.Context) goxlint.Visitor { return nil }
	packageRule := func(*goxlint.PackageContext) {}
	return goxlint.Plugin{Name: "demo", Rules: []*goxlint.Rule{
		{
			Meta: goxlint.Meta{
				Name: "zeta", Description: "Zeta rule.", Category: goxlint.Style,
				Messages: map[string]string{"zeta": "Zeta message."},
				Fixable:  goxlint.FixSuggestion, NeedsTypes: true,
			},
			AnalyzePackage: packageRule,
		},
		{
			Meta: goxlint.Meta{
				Name: "alpha", Description: "Alpha rule.", Category: goxlint.Correctness,
				Messages: map[string]string{"alpha": "Alpha message."},
			},
			Create: fileRule,
		},
		{
			Meta: goxlint.Meta{
				Name: "both", Description: "Combined rule.", Category: goxlint.Restriction,
				Fixable: goxlint.FixDangerous,
			},
			Create:         fileRule,
			AnalyzePackage: packageRule,
		},
	}}
}

func TestRulesJSONUsesResolvedConfigAndMetadata(t *testing.T) {
	t.Chdir(t.TempDir())
	configPath := filepath.Join(t.TempDir(), "custom.json")
	writeTestFile(t, configPath, `{"categories":{"correctness":"warn","style":"error","restriction":"off"},"rules":{"demo/alpha":"error"}}`)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"rules", "--format", "json", "-c", configPath}, &stdout, &stderr, rulesTestPlugin()); code != 0 {
		t.Fatalf("want success, got %d: %s", code, &stderr)
	}
	var got []ruleJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, &stdout)
	}
	if len(got) != 3 {
		t.Fatalf("want one object per rule, got %d: %s", len(got), &stdout)
	}
	if got[0].ID != "demo/alpha" || got[1].ID != "demo/both" || got[2].ID != "demo/zeta" {
		t.Fatalf("rules are not sorted by ID: %#v", got)
	}
	if got[0].Plugin != "demo" || got[0].Name != "alpha" || got[0].Description != "Alpha rule." ||
		got[0].Category != goxlint.Correctness || got[0].Scope != "file" || got[0].Fixable != "none" ||
		got[0].NeedsTypes || got[0].Messages["alpha"] != "Alpha message." || got[0].Enabled != "error" {
		t.Fatalf("unexpected alpha rule: %#v", got[0])
	}
	if got[1].Scope != "file+package" || got[1].Fixable != "dangerous" || got[1].Enabled != "off" {
		t.Fatalf("unexpected both rule: %#v", got[1])
	}
	if got[2].Scope != "package" || got[2].Fixable != "suggestion" || !got[2].NeedsTypes || got[2].Enabled != "error" {
		t.Fatalf("unexpected zeta rule: %#v", got[2])
	}
}

func TestRulesJSONFindsNearestConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTestFile(t, ".goxlintrc.json", `{"categories":{"style":"warn"}}`)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"rules", "--format=json"}, &stdout, &stderr, rulesTestPlugin()); code != 0 {
		t.Fatalf("want success, got %d: %s", code, &stderr)
	}
	var got []ruleJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := map[string]string{"demo/alpha": "off", "demo/both": "off", "demo/zeta": "warn"}
	for _, rule := range got {
		if want[rule.ID] != rule.Enabled {
			t.Errorf("%s enabled=%q, want %q", rule.ID, rule.Enabled, want[rule.ID])
		}
	}
}

func TestRulesDefaultTableMatchesLegacyOutput(t *testing.T) {
	plugin := rulesTestPlugin()
	reg := config.NewRegistry(plugin)
	var legacy, got, stderr bytes.Buffer
	listRules(&legacy, reg)
	if code := Run([]string{"rules"}, &got, &stderr, plugin); code != 0 {
		t.Fatalf("want success, got %d: %s", code, &stderr)
	}
	if !bytes.Equal(got.Bytes(), legacy.Bytes()) {
		t.Fatalf("default output changed:\nwant:\n%s\ngot:\n%s", &legacy, &got)
	}
}

func TestRulesTableRejectsConfigFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	configPath := filepath.Join(t.TempDir(), "does-not-exist.json")
	if code := Run([]string{"rules", "-c", configPath}, &stdout, &stderr, rulesTestPlugin()); code != 2 {
		t.Fatalf("want usage exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "-c is only supported with --format json") {
		t.Fatalf("missing config-format error: %s", &stderr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("table output should be empty: %s", &stdout)
	}
}

func TestRulesUnknownFormatExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"rules", "--format", "yaml"}, &stdout, &stderr, rulesTestPlugin()); code != 2 {
		t.Fatalf("want usage exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown format "yaml"`) {
		t.Fatalf("missing unknown-format error: %s", &stderr)
	}
}

func TestLoadErrorsFail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typed   bool
		broken  string
		missing bool
	}{
		{name: "missing file", missing: true},
		{name: "syntax error", broken: "package p\nfunc broken(\n"},
		{name: "type error", typed: true, broken: "package p\nvar _ int = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, "go.mod", "module example.com/test\n\ngo 1.26.0\n")
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}}`)
			const source = "package p\nvar bad = 1\n"
			writeTestFile(t, "valid.go", source)
			if !tc.missing {
				writeTestFile(t, "broken.go", tc.broken)
			}
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--fix", "--format=json", "valid.go", "broken.go"}, &stdout, &stderr, testPlugin(tc.typed))
			if code != 2 || !strings.Contains(stderr.String(), "goxlint: load:") {
				t.Fatalf("want exit 2 and load error, got %d: %s", code, &stderr)
			}
			if !strings.Contains(stdout.String(), "rename bad") {
				t.Fatalf("missing diagnostics from loadable file: %s", &stdout)
			}
			got, err := os.ReadFile("valid.go")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != source {
				t.Fatalf("modified source despite load error: %q", got)
			}
		})
	}
}

func TestInvalidFormatDoesNotApplyFixes(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}}`)
	const source = "package p\nvar bad = 1\n"
	writeTestFile(t, "example.go", source)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--fix", "--format=invalid", "example.go"}, &stdout, &stderr, testPlugin(false))
	if code != 2 || !strings.Contains(stderr.String(), "unknown format") {
		t.Fatalf("want invalid format error, got %d: %s", code, &stderr)
	}
	got, err := os.ReadFile("example.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != source {
		t.Fatalf("modified source with invalid format: %q", got)
	}
}

func TestRunExitCodes(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"warn"}}`)
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "warnings", code: 0},
		{name: "errors", args: []string{"-D", "test/rename"}, code: 1},
		{name: "deny warnings", args: []string{"--deny-warnings"}, code: 1},
		{name: "quiet still counts warnings", args: []string{"--quiet", "--deny-warnings"}, code: 1},
		{name: "max warnings", args: []string{"--max-warnings=0"}, code: 1},
		{name: "fix", args: []string{"--fix"}, code: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTestFile(t, "example.go", "package p\nvar bad = 1\n")
			var stdout, stderr bytes.Buffer
			args := append([]string{"--format=json", filepath.Join(".", "example.go")}, tc.args...)
			if code := Run(args, &stdout, &stderr, testPlugin(false)); code != tc.code {
				t.Fatalf("want exit %d, got %d: %s", tc.code, code, &stderr)
			}
			if tc.name == "fix" && strings.TrimSpace(stdout.String()) != "[]" {
				t.Fatalf("fixed file was not re-linted: %s", &stdout)
			}
		})
	}
}

func TestUnusedSuppressionReporting(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		args         []string
		code         int
		severity     string
		quiet        bool
	}{
		{name: "default warning", severity: "warning"},
		{name: "config error", config: `,"reportUnusedSuppressions":"error"`, code: 1, severity: "error"},
		{name: "config off", config: `,"reportUnusedSuppressions":"off"`},
		{name: "flag overrides config", config: `,"reportUnusedSuppressions":"off"`, args: []string{"--report-unused-suppressions=error"}, code: 1, severity: "error"},
		{name: "flag off", args: []string{"--report-unused-suppressions=off"}},
		{name: "deny warnings", args: []string{"--deny-warnings"}, code: 1, severity: "warning"},
		{name: "max warnings", args: []string{"--max-warnings=0"}, code: 1, severity: "warning"},
		{name: "quiet", args: []string{"--quiet", "--deny-warnings"}, code: 1, severity: "warning", quiet: true},
		{name: "disabled rule", args: []string{"-A", "test/rename"}, severity: "warning"},
		{name: "file override", config: `,"overrides":[{"files":["example.go"],"rules":{"test/rename":"off"}}]`, severity: "warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}`+tc.config+`}`)
			name := "good"
			if tc.name == "disabled rule" || tc.name == "file override" {
				name = "bad"
			}
			writeTestFile(t, "example.go", "package p\n//goxlint:ignore test/rename reason\nvar "+name+" = 1\n")
			var stdout, stderr bytes.Buffer
			args := append([]string{"--format=json", "example.go"}, tc.args...)
			if code := Run(args, &stdout, &stderr, testPlugin(false)); code != tc.code {
				t.Fatalf("want exit %d, got %d: %s", tc.code, code, &stderr)
			}
			if tc.severity == "" || tc.quiet {
				if strings.TrimSpace(stdout.String()) != "[]" {
					t.Fatalf("want no output diagnostics: %s", &stdout)
				}
			} else {
				for _, want := range []string{`"rule": "goxlint/unused-suppression"`, `"severity": "` + tc.severity + `"`, `"line": 2`, `"column": 18`} {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("missing %s in output: %s", want, &stdout)
					}
				}
			}
		})
	}
}

func TestInvalidUnusedSuppressionSeverity(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		args         []string
	}{
		{name: "config", config: `,"reportUnusedSuppressions":"invalid"`},
		{name: "flag", args: []string{"--report-unused-suppressions=invalid"}},
		{name: "empty flag", args: []string{"--report-unused-suppressions="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}`+tc.config+`}`)
			const source = "package p\nvar bad = 1\n"
			writeTestFile(t, "example.go", source)
			var stdout, stderr bytes.Buffer
			args := append([]string{"--fix", "example.go"}, tc.args...)
			if code := Run(args, &stdout, &stderr, testPlugin(false)); code != 2 || !strings.Contains(stderr.String(), "unknown severity") {
				t.Fatalf("want severity error, got exit %d: %s", code, &stderr)
			}
			got, err := os.ReadFile("example.go")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != source {
				t.Fatalf("changed source despite invalid config: %q", got)
			}
		})
	}
}

func TestUnusedSuppressionsSkipLoadErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}}`)
	writeTestFile(t, "example.go", "package p\n//goxlint:ignore test/rename\nvar good = 1\n")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--format=json", "example.go", "missing.go"}, &stdout, &stderr, testPlugin(false)); code != 2 {
		t.Fatalf("want load error, got exit %d: %s", code, &stderr)
	}
	if strings.TrimSpace(stdout.String()) != "[]" {
		t.Fatalf("audited incomplete load: %s", &stdout)
	}
}

func TestUnusedSuppressionFormats(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, ".goxlintrc.json", `{}`)
	const source = "package p\n//goxlint:ignore unknown/rule reason\nvar good = 1\n"
	writeTestFile(t, "example.go", source)
	for _, tc := range []struct{ format, want string }{
		{"default", "goxlint(unused-suppression)"},
		{"unix", "example.go:2:18:"},
		{"github", "::warning file=example.go,line=2,endLine=2,col=18,endColumn=30,title=goxlint/unused-suppression::"},
		{"json", `"rule": "goxlint/unused-suppression"`},
	} {
		t.Run(tc.format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run([]string{"--fix", "--format=" + tc.format, "example.go"}, &stdout, &stderr); code != 0 {
				t.Fatalf("unexpected exit %d: %s", code, &stderr)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("missing %q in output: %s", tc.want, &stdout)
			}
			got, err := os.ReadFile("example.go")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != source {
				t.Fatalf("audit unexpectedly changed source: %q", got)
			}
		})
	}
}

func TestLoadErrorsStayInTheirPackage(t *testing.T) {
	for _, tc := range []struct {
		name, broken string
		typed        bool
	}{
		{name: "syntax error", broken: "package broken\nfunc broken(\n"},
		{name: "type error", broken: "package broken\nvar X int = true\n", typed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, "go.mod", "module example.com/test\n\ngo 1.26.0\n")
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/rename":"error"}}`)
			for _, dir := range []string{"ok", "broken", "importer"} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeTestFile(t, "ok/a.go", "package ok\nvar bad = 1\n")
			writeTestFile(t, "ok/b.go", "package ok\n//goxlint:ignore test/rename stale\nvar fine = 1\n")
			writeTestFile(t, "broken/broken.go", tc.broken)
			const importer = "package importer\nimport \"example.com/test/broken\"\nvar bad = broken.X\n"
			writeTestFile(t, "importer/i.go", importer)

			var stdout, stderr bytes.Buffer
			code := Run([]string{"--fix", "--format=unix", "./..."}, &stdout, &stderr, testPlugin(tc.typed))
			if code != 2 {
				t.Fatalf("want exit 2, got %d: %s", code, &stderr)
			}
			if n := strings.Count(stderr.String(), "goxlint: load:"); n != 1 {
				t.Fatalf("want the load error once, got %d: %s", n, &stderr)
			}
			if got, _ := os.ReadFile("ok/a.go"); string(got) != "package ok\nvar good = 1\n" {
				t.Fatalf("healthy package was not fixed: %q", got)
			}
			if !strings.Contains(filepath.ToSlash(stdout.String()), "ok/b.go:2:18: Unused suppression") {
				t.Fatalf("healthy package was not audited: %s", &stdout)
			}
			// Without types there are no imports to follow, so only typed mode
			// treats the importer of a broken package as incomplete.
			if got, _ := os.ReadFile("importer/i.go"); tc.typed && string(got) != importer {
				t.Fatalf("fixed a file importing a broken package: %q", got)
			}
		})
	}
}

func unparenRule(kind goxlint.FixKind) *goxlint.Rule {
	return &goxlint.Rule{
		Meta: goxlint.Meta{Name: "unparen", Category: goxlint.Correctness, Fixable: kind},
		Create: func(ctx *goxlint.Context) goxlint.Visitor {
			return goxlint.Visit(goxlint.On(func(expr *ast.ParenExpr) {
				ctx.Report(goxlint.Diagnostic{Node: expr, Message: "remove parentheses", Fix: &goxlint.Fix{
					Kind: kind, Edits: []goxlint.Edit{goxlint.ReplaceNode(expr, ctx.Text(expr.X))},
				}})
			}))
		},
	}
}

func TestOverlappingFixPasses(t *testing.T) {
	for _, tc := range []struct {
		flag  string
		kind  goxlint.FixKind
		fixed bool
	}{
		{"--fix", goxlint.FixSafe, true},
		{"--fix-suggestions", goxlint.FixSuggestion, true},
		{"--fix-dangerously", goxlint.FixDangerous, true},
		{"--fix", goxlint.FixSuggestion, false},
		{"--fix-suggestions", goxlint.FixDangerous, false},
	} {
		t.Run(tc.flag+"/"+tc.kind.String(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/unparen":"error"}}`)
			const original = "package p\nvar value = ((1))\n"
			writeTestFile(t, "example.go", original)
			var stdout, stderr bytes.Buffer
			code := Run([]string{tc.flag, "--format=json", "example.go"}, &stdout, &stderr,
				goxlint.Plugin{Name: "test", Rules: []*goxlint.Rule{unparenRule(tc.kind)}})
			wantSource := original
			if tc.fixed {
				wantSource = "package p\nvar value = 1\n"
				if code != 0 || strings.TrimSpace(stdout.String()) != "[]" {
					t.Fatalf("want no remaining diagnostics, got exit %d: %s; %s", code, &stdout, &stderr)
				}
				if stderr.String() != "Applied 2 fix(es) in 2 pass(es).\n" {
					t.Fatalf("unexpected fix summary: %s", &stderr)
				}
			} else if code != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "remove parentheses") {
				t.Fatalf("fix level was not respected: exit %d: %s; %s", code, &stdout, &stderr)
			}
			got, err := os.ReadFile("example.go")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != wantSource {
				t.Fatalf("got source %q, want %q", got, wantSource)
			}
		})
	}
}

func TestFixPassLimitBoundary(t *testing.T) {
	for _, depth := range []int{0, 10, 11} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, ".goxlintrc.json", `{"rules":{"test/unparen":"error"}}`)
			writeTestFile(t, "example.go", "package p\nvar value = "+strings.Repeat("(", depth)+"1"+strings.Repeat(")", depth)+"\n")
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--fix", "--format=json", "example.go"}, &stdout, &stderr,
				goxlint.Plugin{Name: "test", Rules: []*goxlint.Rule{unparenRule(goxlint.FixSafe)}})
			if depth > 10 {
				if code != 1 || !strings.Contains(stderr.String(), "fixes did not stabilize after 10 passes") || !strings.Contains(stderr.String(), "example.go") {
					t.Fatalf("missing pass-limit warning: exit %d: %s", code, &stderr)
				}
				if !strings.Contains(stdout.String(), "remove parentheses") {
					t.Fatalf("missing final diagnostic: %s", &stdout)
				}
			} else if code != 0 || strings.TrimSpace(stdout.String()) != "[]" || strings.Contains(stderr.String(), "warning:") {
				t.Fatalf("stable file reported a problem: exit %d: %s; %s", code, &stdout, &stderr)
			}
			if depth == 0 {
				if stderr.Len() != 0 {
					t.Fatalf("reported fixes for a clean file: %s", &stderr)
				}
			} else if !strings.HasPrefix(stderr.String(), "Applied 10 fix(es) in 10 pass(es).\n") || strings.Count(stderr.String(), "Applied ") != 1 {
				t.Fatalf("unexpected fix summary: %s", &stderr)
			}
			remaining := max(0, depth-10)
			want := "package p\nvar value = " + strings.Repeat("(", remaining) + "1" + strings.Repeat(")", remaining) + "\n"
			if got, err := os.ReadFile("example.go"); err != nil || string(got) != want {
				t.Fatalf("unexpected final source %q: %v", got, err)
			}
		})
	}
}

func TestOscillatingFixesStop(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, ".goxlintrc.json", `{"categories":{"correctness":"error"}}`)
	originals := map[string]string{
		"a.go": "package p\nfunc a(left int) int { return 1 }\n",
		"b.go": "package p\nfunc b(left int) int { return 1 }\n",
	}
	for name, source := range originals {
		writeTestFile(t, name, source)
	}
	writeTestFile(t, "stable.go", "package p\nvar bad = 1\n")
	plugin := testPlugin(false)
	for _, pair := range [][2]string{{"left", "right"}, {"right", "left"}} {
		from, to := pair[0], pair[1]
		plugin.Rules = append(plugin.Rules, &goxlint.Rule{
			Meta: goxlint.Meta{Name: "swap-" + from, Category: goxlint.Correctness, Fixable: goxlint.FixSafe},
			Create: func(ctx *goxlint.Context) goxlint.Visitor {
				return goxlint.Visit(goxlint.On(func(id *ast.Ident) {
					if id.Name == from {
						ctx.Report(goxlint.Diagnostic{Node: id, Message: "replace " + from + " with " + to, Fix: &goxlint.Fix{
							Kind: goxlint.FixSafe, Edits: []goxlint.Edit{goxlint.ReplaceNode(id, to)},
						}})
					}
				}))
			},
		})
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--fix", "--format=json", "."}, &stdout, &stderr, plugin)
	if code != 1 || !strings.HasPrefix(stderr.String(), "Applied 21 fix(es) in 10 pass(es).\n") {
		t.Fatalf("unexpected fix result: exit %d: %s", code, &stderr)
	}
	if strings.Count(stderr.String(), "warning:") != 1 || !strings.Contains(stderr.String(), "a.go") || !strings.Contains(stderr.String(), "b.go") || strings.Contains(stderr.String(), "stable.go") {
		t.Fatalf("warning must name only unstable files: %s", &stderr)
	}
	var diags []struct{ Rule, File, Message string }
	if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
		t.Fatal(err)
	}
	if len(diags) != 2 {
		t.Fatalf("want two final diagnostics, got %s", &stdout)
	}
	for _, d := range diags {
		if d.Rule != "test/swap-left" || d.Message != "replace left with right" {
			t.Errorf("diagnostic does not match the final source: %+v", d)
		}
		if got, err := os.ReadFile(d.File); err != nil || string(got) != originals[d.File] {
			t.Errorf("unexpected final source %q: %v", got, err)
		}
	}
	if got, err := os.ReadFile("stable.go"); err != nil || string(got) != "package p\nvar good = 1\n" {
		t.Fatalf("stable file was not fixed: %q: %v", got, err)
	}
}

func TestFixPassSkipsNewlyIncompletePackages(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, "go.mod", "module example.com/test\n\ngo 1.26.0\n")
	writeTestFile(t, ".goxlintrc.json", `{"categories":{"correctness":"error"}}`)
	for _, dir := range []string{"healthy", "broken"} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, "healthy/a.go", "package healthy\nvar value = ((1))\n")
	writeTestFile(t, "broken/b.go", "package broken\nvar value = 1\n")
	plugin := goxlint.Plugin{Name: "test", Rules: []*goxlint.Rule{
		unparenRule(goxlint.FixSafe),
		{Meta: goxlint.Meta{Name: "types", Category: goxlint.Correctness, NeedsTypes: true}},
		{
			Meta: goxlint.Meta{Name: "break", Category: goxlint.Correctness, Fixable: goxlint.FixDangerous},
			Create: func(ctx *goxlint.Context) goxlint.Visitor {
				if filepath.Base(ctx.Filename) != "b.go" {
					return nil
				}
				return goxlint.Visit(goxlint.On(func(spec *ast.ValueSpec) {
					value := spec.Values[0]
					replacement := "missing"
					if ctx.Text(value) == "missing" {
						replacement = "0"
					}
					ctx.Report(goxlint.Diagnostic{Node: value, Message: "replace with " + replacement, Fix: &goxlint.Fix{
						Kind: goxlint.FixDangerous, Edits: []goxlint.Edit{goxlint.ReplaceNode(value, replacement)},
					}})
				}))
			},
		},
	}}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--fix-dangerously", "--format=json", "./..."}, &stdout, &stderr, plugin)
	if code != 2 || strings.Count(stderr.String(), "goxlint: load:") != 1 || !strings.HasPrefix(stderr.String(), "Applied 3 fix(es) in 2 pass(es).\n") {
		t.Fatalf("unexpected fix/load result: exit %d: %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "replace with 0") {
		t.Fatalf("missing diagnostic after the package became incomplete: %s", &stdout)
	}
	for name, want := range map[string]string{
		"healthy/a.go": "package healthy\nvar value = 1\n",
		"broken/b.go":  "package broken\nvar value = missing\n",
	} {
		if got, err := os.ReadFile(name); err != nil || string(got) != want {
			t.Errorf("%s: got %q, want %q: %v", name, got, want, err)
		}
	}
}
