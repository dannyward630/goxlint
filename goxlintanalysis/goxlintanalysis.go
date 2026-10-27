// Package goxlintanalysis adapts goxlint rules to go/analysis drivers.
// The driver supplies the package and source. The adapter does not load
// .goxlintrc.json or apply fixes itself.
package goxlintanalysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/internal/engine"
)

// Option configures an adapter.
type Option func(*options)

type options struct {
	ruleOptions json.RawMessage
}

// WithOptions supplies the JSON object consumed by Context.Options.
// It copies the input. If repeated, the last WithOptions value wins.
func WithOptions(value json.RawMessage) Option {
	data := bytes.Clone(value)
	return func(o *options) { o.ruleOptions = bytes.Clone(data) }
}

// Analyzer converts a non-nil rule to an analysis.Analyzer. Its name is the
// rule ID with non-identifier characters replaced by underscores. Names that
// start with a digit get an underscore prefix. The caller must not mutate the
// rule while analysis runs. Use WithOptions for rule options.
func Analyzer(plugin string, rule *goxlint.Rule, opts ...Option) *analysis.Analyzer {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	id := plugin + "/" + rule.Meta.Name
	doc := rule.Meta.Description
	if strings.TrimSpace(doc) == "" {
		doc = "runs the " + id + " rule"
	}
	return &analysis.Analyzer{
		Name: analyzerName(id), Doc: doc,
		//goxlint:ignore anti-slop/no-any-returns go/analysis.Analyzer.Run requires this signature
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, plugin, id, rule, o.ruleOptions)
		},
	}
}

// Analyzers converts all plugin rules, sorted by rule ID. It uses default rule
// options. Colliding analyzer names get numeric suffixes in rule ID order.
func Analyzers(plugins ...goxlint.Plugin) []*analysis.Analyzer {
	type entry struct {
		id, plugin string
		rule       *goxlint.Rule
	}
	var entries []entry
	for _, plugin := range plugins {
		for _, rule := range plugin.Rules {
			entries = append(entries, entry{plugin.Name + "/" + rule.Meta.Name, plugin.Name, rule})
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return strings.Compare(a.id, b.id) })
	out := make([]*analysis.Analyzer, 0, len(entries))
	used := map[string]bool{}
	for _, e := range entries {
		a := Analyzer(e.plugin, e.rule)
		base := a.Name
		for n := 2; used[a.Name]; n++ {
			a.Name = base + "_" + strconv.Itoa(n)
		}
		used[a.Name] = true
		out = append(out, a)
	}
	return out
}

func analyzerName(id string) string {
	name := strings.Map(func(r rune) rune {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, id)
	for _, r := range name {
		if unicode.IsDigit(r) {
			name = "_" + name
		}
		break
	}
	return name
}

func run(pass *analysis.Pass, plugin, id string, rule *goxlint.Rule, options json.RawMessage) error {
	readFile := pass.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	var files []engine.File
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		name := pass.Fset.File(file.FileStart).Name()
		src, err := readFile(name)
		if err != nil {
			return fmt.Errorf("%s: read %s: %w", id, name, err)
		}
		files = append(files, engine.File{Fset: pass.Fset, AST: file, Filename: name, Source: src, Pkg: pass.Pkg, Info: pass.TypesInfo})
	}
	configured := []engine.ConfiguredRule{{ID: id, Plugin: plugin, Rule: rule, Severity: engine.Error, Options: options}}
	results := engine.LintAll(files, func(string) []engine.ConfiguredRule { return configured }, engine.LintOptions{SinglePackage: true})
	// Pass functions are not concurrency-safe. Report only after the engine joins
	// its file workers, and use the same physical token file for every offset.
	for i, diags := range results {
		tf := pass.Fset.File(files[i].AST.FileStart)
		for _, d := range diags {
			message := d.Message
			if d.Help != "" {
				message += "\n" + d.Help
			}
			report := analysis.Diagnostic{Pos: tf.Pos(d.Start.Offset), End: tf.Pos(d.End.Offset), Message: message, Category: string(rule.Meta.Category)}
			if fix, ok := suggestedFix(tf, d.Fix); ok {
				report.SuggestedFixes = []analysis.SuggestedFix{fix}
			}
			pass.Report(report)
		}
	}
	return nil
}

func suggestedFix(file *token.File, fix *engine.Fix) (analysis.SuggestedFix, bool) {
	if fix == nil || (fix.Kind != goxlint.FixSafe && fix.Kind != goxlint.FixSuggestion) || len(fix.Edits) == 0 {
		return analysis.SuggestedFix{}, false
	}
	edits := slices.Clone(fix.Edits)
	slices.SortStableFunc(edits, func(a, b engine.Edit) int { return a.Start - b.Start })
	out := analysis.SuggestedFix{Message: fix.Message}
	if out.Message == "" {
		out.Message = "Apply " + fix.Kind.String() + " fix"
	}
	for i, edit := range edits {
		if edit.Start < 0 || edit.End < edit.Start || edit.End > file.Size() {
			return analysis.SuggestedFix{}, false
		}
		if i > 0 && (edit.Start < edits[i-1].End || edit.Start == edits[i-1].Start) {
			return analysis.SuggestedFix{}, false
		}
		out.TextEdits = append(out.TextEdits, analysis.TextEdit{Pos: file.Pos(edit.Start), End: file.Pos(edit.End), NewText: []byte(edit.NewText)})
	}
	return out, true
}
