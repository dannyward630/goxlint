// Package cli implements the goxlint command. Repositories that vendor their own
// rules build a binary that registers them:
//
//	package main
//
//	import (
//		"github.com/egoisutolabs/goxlint/cli"
//		"example.com/repo/tools/goxlint/antislop"
//	)
//
//	func main() { cli.Main(antislop.Plugin) }
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/internal/config"
	"github.com/egoisutolabs/goxlint/internal/engine"
	"github.com/egoisutolabs/goxlint/internal/output"
)

// Main runs goxlint with the given plugins and exits.
func Main(plugins ...goxlint.Plugin) {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr, plugins...))
}

// Run runs goxlint and returns the exit code: 0 success, 1 lint failure,
// 2 usage or operational error (including incomplete loading).
func Run(args []string, stdout, stderr io.Writer, plugins ...goxlint.Plugin) int {
	reg := config.NewRegistry(plugins...)
	if len(args) > 0 {
		switch args[0] {
		case "rules":
			return listRulesCommand(args[1:], stdout, stderr, reg)
		case "init":
			return initConfig(stdout, stderr, reg)
		}
	}
	code, err := lint(args, stdout, stderr, reg)
	if err != nil {
		fmt.Fprintln(stderr, "goxlint:", err)
		return 2
	}
	return code
}

type severityFlag struct {
	sev  string
	list *[][2]string
}

func (f severityFlag) String() string { return "" }
func (f severityFlag) Set(v string) error {
	*f.list = append(*f.list, [2]string{f.sev, v})
	return nil
}

func lint(args []string, stdout, stderr io.Writer, reg *config.Registry) (int, error) {
	fs := flag.NewFlagSet("goxlint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configPath   = fs.String("c", "", "path to config file (default: nearest "+config.FileName+")")
		format       = fs.String("format", "default", "output format: "+strings.Join(output.Formats, ", "))
		fix          = fs.Bool("fix", false, "apply safe fixes")
		fixSuggest   = fs.Bool("fix-suggestions", false, "apply safe and suggested fixes")
		fixDangerous = fs.Bool("fix-dangerously", false, "apply all fixes, including dangerous ones")
		quiet        = fs.Bool("quiet", false, "report errors only")
		denyWarnings = fs.Bool("deny-warnings", false, "exit with 1 when there are warnings")
		maxWarnings  = fs.Int("max-warnings", -1, "exit with 1 when warnings exceed this number")
		tags         = fs.String("tags", "", "comma-separated build tags for type-aware loading, as in go build -tags")
		unused       = fs.String("report-unused-suppressions", "", "unused suppression severity: off, warn, error (default: config or warn)")
		overrides    [][2]string
	)
	fs.Var(severityFlag{"error", &overrides}, "D", "deny a rule or category (repeatable)")
	fs.Var(severityFlag{"warn", &overrides}, "W", "warn on a rule or category (repeatable)")
	fs.Var(severityFlag{"off", &overrides}, "A", "allow (disable) a rule or category (repeatable)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: goxlint [flags] [packages]\n       goxlint rules\n       goxlint init\n\nflags:")
		fs.PrintDefaults()
	}

	// Allow flags after package patterns, like oxlint.
	var patterns []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0, nil
			}
			return 2, nil
		}
		if fs.NArg() == 0 {
			break
		}
		patterns = append(patterns, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if err := output.ValidateFormat(*format); err != nil {
		return 0, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	cfg, root := config.Default(), cwd
	if *configPath == "" {
		*configPath = config.Find(cwd)
	}
	if *configPath != "" {
		if cfg, err = config.Load(*configPath); err != nil {
			return 0, err
		}
		root = filepath.Dir(must(filepath.Abs(*configPath)))
	}
	if err := applySeverityFlags(cfg, overrides); err != nil {
		return 0, err
	}
	var unusedSet bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "report-unused-suppressions" {
			unusedSet = true
		}
	})
	if unusedSet {
		if _, err := config.ParseSeverity(*unused); err != nil {
			return 0, fmt.Errorf("report-unused-suppressions: %w", err)
		}
		cfg.ReportUnusedSuppressions = *unused
	}
	resolved, err := config.Resolve(cfg, reg, root)
	if err != nil {
		return 0, err
	}

	maxFix := goxlint.FixNone
	switch {
	case *fixDangerous:
		maxFix = goxlint.FixDangerous
	case *fixSuggest:
		maxFix = goxlint.FixSuggestion
	case *fix:
		maxFix = goxlint.FixSafe
	}

	typed := resolved.AnyOverrideNeedsTypes(reg) || slices.ContainsFunc(resolved.AllRules(), func(r *goxlint.Rule) bool { return r.Meta.NeedsTypes })
	opts := engine.LoadOptions{Dir: cwd, Patterns: patterns, Typed: typed, Tags: splitTags(*tags), Ignore: resolved.Ignored}
	rulesFor := func(name string) []engine.ConfiguredRule { return resolved.RulesFor(name, reg) }

	start := time.Now()
	files, loadErrs := engine.Load(opts)
	lintOpts := engine.LintOptions{ReportUnusedSuppressions: resolved.ReportUnusedSuppressions}
	results := engine.LintAll(files, rulesFor, lintOpts)

	if maxFix != goxlint.FixNone {
		fixed := 0
		for i, f := range files {
			// An incomplete package may leave rules without the context needed for safe fixes.
			if f.Incomplete {
				continue
			}
			src, applied := engine.ApplyFixes(f.Source, results[i], maxFix)
			if len(applied) == 0 {
				continue
			}
			if err := os.WriteFile(f.Filename, src, 0o644); err != nil {
				return 0, err
			}
			fixed += len(applied)
		}
		if fixed > 0 {
			fmt.Fprintf(stderr, "Applied %d fix(es).\n", fixed)
			files, loadErrs = engine.Load(opts)
			results = engine.LintAll(files, rulesFor, lintOpts)
		}
	}

	for _, e := range loadErrs {
		fmt.Fprintln(stderr, "goxlint: load:", e)
	}

	var diags []engine.Diagnostic
	sources := map[string][]byte{}
	warnings, errs := 0, 0
	for i, ds := range results {
		for _, d := range ds {
			if d.Severity == engine.Warn {
				warnings++
				if *quiet {
					continue
				}
			} else {
				errs++
			}
			diags = append(diags, d)
		}
		sources[files[i].Filename] = files[i].Source
	}

	p := &output.Printer{W: stdout, Format: *format, Color: colorEnabled(stdout), Root: cwd, Sources: sources}
	if err := p.Print(diags); err != nil {
		return 0, err
	}
	if *format == "" || *format == "default" {
		ruleCount := len(resolved.AllRules())
		fmt.Fprintf(stdout, "\nFound %d warning%s and %d error%s.\nFinished in %s on %d file%s with %d rule%s using %d threads (%s mode).\n",
			warnings, plural(warnings), errs, plural(errs), time.Since(start).Round(time.Millisecond),
			len(files), plural(len(files)), ruleCount, plural(ruleCount), runtime.GOMAXPROCS(0), mode(typed))
	}

	switch {
	case len(loadErrs) > 0:
		return 2, nil
	case errs > 0, *denyWarnings && warnings > 0, *maxWarnings >= 0 && warnings > *maxWarnings:
		return 1, nil
	}
	return 0, nil
}

func applySeverityFlags(cfg *config.Config, flags [][2]string) error {
	if len(flags) == 0 {
		return nil
	}
	cfg.Categories = maps.Clone(cfg.Categories)
	cfg.Rules = maps.Clone(cfg.Rules)
	if cfg.Categories == nil {
		cfg.Categories = map[goxlint.Category]string{}
	}
	if cfg.Rules == nil {
		cfg.Rules = map[string]config.RuleSetting{}
	}
	for _, f := range flags {
		sev, target := f[0], f[1]
		if slices.Contains(goxlint.Categories, goxlint.Category(target)) {
			cfg.Categories[goxlint.Category(target)] = sev
			continue
		}
		s, err := config.ParseSeverity(sev)
		if err != nil {
			return err
		}
		setting := cfg.Rules[target]
		setting.Severity = s
		cfg.Rules[target] = setting
	}
	return nil
}

func listRules(w io.Writer, reg *config.Registry) int {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tCATEGORY\tSCOPE\tFIX\tTYPES\tDESCRIPTION")
	for _, id := range reg.IDs {
		m := reg.Rules[id].Meta
		scope := ruleScope(reg.Rules[id])
		types := ""
		if m.NeedsTypes {
			types = "yes"
		}
		fix := ""
		if m.Fixable != goxlint.FixNone {
			fix = m.Fixable.String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", id, m.Category, scope, fix, types, m.Description)
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d rules.\n", len(reg.IDs))
	return 0
}

func listRulesCommand(args []string, stdout, stderr io.Writer, reg *config.Registry) int {
	fs := flag.NewFlagSet("goxlint rules", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "table", "output format: table or json")
	configPath := fs.String("c", "", "config file for resolved rule severity in JSON output")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: goxlint rules [--format table|json] [-c path]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "goxlint rules: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintf(stderr, "goxlint rules: unknown format %q (use table or json)\n", *format)
		return 2
	}
	if *format == "table" {
		return listRules(stdout, reg)
	}
	resolved, err := resolveRulesConfig(*configPath, reg)
	if err != nil {
		fmt.Fprintln(stderr, "goxlint:", err)
		return 2
	}
	return listRulesJSON(stdout, stderr, reg, resolved)
}

func resolveRulesConfig(configPath string, reg *config.Registry) (*config.Resolved, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cfg, root := config.Default(), cwd
	if configPath == "" {
		configPath = config.Find(cwd)
	}
	if configPath != "" {
		cfg, err = config.Load(configPath)
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(configPath)
		if err != nil {
			return nil, err
		}
		root = filepath.Dir(abs)
	}
	return config.Resolve(cfg, reg, root)
}

type ruleJSON struct {
	ID          string            `json:"id"`
	Plugin      string            `json:"plugin"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    goxlint.Category  `json:"category"`
	Scope       string            `json:"scope"`
	Fixable     string            `json:"fixable"`
	NeedsTypes  bool              `json:"needsTypes"`
	Messages    map[string]string `json:"messages"`
	Enabled     string            `json:"enabled"`
}

func listRulesJSON(w, stderr io.Writer, reg *config.Registry, resolved *config.Resolved) int {
	rules := make([]ruleJSON, 0, len(reg.IDs))
	for _, id := range reg.IDs {
		rule := reg.Rules[id]
		plugin, name, _ := strings.Cut(id, "/")
		messages := rule.Meta.Messages
		if messages == nil {
			messages = map[string]string{}
		}
		rules = append(rules, ruleJSON{
			ID:          id,
			Plugin:      plugin,
			Name:        name,
			Description: rule.Meta.Description,
			Category:    rule.Meta.Category,
			Scope:       ruleScope(rule),
			Fixable:     rule.Meta.Fixable.String(),
			NeedsTypes:  rule.Meta.NeedsTypes,
			Messages:    messages,
			Enabled:     resolved.Severity(id),
		})
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(rules); err != nil {
		fmt.Fprintln(stderr, "goxlint: write rules JSON:", err)
		return 2
	}
	return 0
}

func ruleScope(rule *goxlint.Rule) string {
	if rule.AnalyzePackage != nil {
		if rule.Create != nil {
			return "file+package"
		}
		return "package"
	}
	return "file"
}

func initConfig(stdout, stderr io.Writer, reg *config.Registry) int {
	if _, err := os.Stat(config.FileName); err == nil {
		fmt.Fprintf(stderr, "goxlint: %s already exists\n", config.FileName)
		return 2
	}
	cfg := config.Default()
	cfg.Rules = map[string]config.RuleSetting{}
	for _, id := range reg.IDs {
		cfg.Rules[id] = config.RuleSetting{Severity: engine.Error}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "goxlint:", err)
		return 2
	}
	if err := os.WriteFile(config.FileName, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(stderr, "goxlint:", err)
		return 2
	}
	fmt.Fprintf(stdout, "Created %s with %d rules enabled.\n", config.FileName, len(reg.IDs))
	return 0
}

func splitTags(s string) []string {
	var tags []string
	for t := range strings.SplitSeq(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func mode(typed bool) string {
	if typed {
		return "typed"
	}
	return "syntax"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
