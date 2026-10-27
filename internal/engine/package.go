package engine

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoisutolabs/goxlint"
)

// LintAll runs file visitors in parallel, then package analyzers, then applies
// suppressions once to the combined findings. Results retain the input order.
func LintAll(files []File, rulesFor func(filename string) []ConfiguredRule, opts LintOptions) [][]Diagnostic {
	out := make([][]Diagnostic, len(files))
	rules := make([][]ConfiguredRule, len(files))
	fileOpts := make([]LintOptions, len(files))
	parallel(len(files), func(i int) {
		rules[i] = rulesFor(files[i].Filename)
		out[i], fileOpts[i] = lintFile(files[i], rules[i], opts)
	})
	groups := packageGroups(files, opts.SinglePackage)
	parallel(len(groups), func(i int) {
		lintPackage(files, groups[i], rules, out, fileOpts)
	})
	parallel(len(files), func(i int) {
		out[i] = finish(files[i], out[i], fileOpts[i])
	})
	return out
}

func packageGroups(files []File, singlePackage bool) [][]int {
	type packageKey struct{ dir, name string }
	byPackage := map[packageKey]int{}
	var groups [][]int
	for i, f := range files {
		key := packageKey{filepath.Dir(f.Filename), f.AST.Name.Name}
		if singlePackage {
			key = packageKey{}
		}
		group, ok := byPackage[key]
		if !ok {
			group = len(groups)
			byPackage[key] = group
			groups = append(groups, nil)
		}
		groups[group] = append(groups[group], i)
	}
	for _, group := range groups {
		slices.SortFunc(group, func(a, b int) int {
			return strings.Compare(files[a].Filename, files[b].Filename)
		})
	}
	return groups
}

func lintPackage(files []File, group []int, rules [][]ConfiguredRule, out [][]Diagnostic, opts []LintOptions) {
	byID := map[string]ConfiguredRule{}
	var ids []string
	for _, i := range group {
		for _, cr := range rules[i] {
			if cr.Severity == Off || cr.Rule.AnalyzePackage == nil {
				continue
			}
			if _, ok := byID[cr.ID]; !ok {
				byID[cr.ID] = cr
				ids = append(ids, cr.ID)
			}
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		cr := byID[id]
		incomplete := false
		for _, i := range group {
			incomplete = incomplete || files[i].Incomplete || (cr.Rule.Meta.NeedsTypes && files[i].Info == nil)
		}
		if incomplete {
			disableSuppressionAudit(group, opts)
			continue
		}
		contexts := make([]*goxlint.Context, len(group))
		pending := make([][]Diagnostic, len(group))
		firstEnabled := -1
		for j, i := range group {
			configured := cr
			configured.Severity, configured.Options = Off, nil
			for _, candidate := range rules[i] {
				if candidate.ID == id {
					configured = candidate
					break
				}
			}
			if firstEnabled == -1 && configured.Severity != Off {
				firstEnabled = j
			}
			f := files[i]
			var stack []ast.Node
			var pkg *types.Package
			var info *types.Info
			if cr.Rule.Meta.NeedsTypes {
				pkg, info = f.Pkg, f.Info
			}
			report := func(d goxlint.Diagnostic) {
				if configured.Severity != Off {
					pending[j] = append(pending[j], resolve(f, f.Fset.File(f.AST.FileStart), &configured, d))
				}
			}
			ctx := goxlint.NewContext(cr.Rule, id, f.Fset, f.AST, f.Filename, f.Source, pkg, info, configured.Options, &stack, report)
			ctx.Enabled = configured.Severity != Off
			contexts[j] = ctx
		}
		ctx := &goxlint.PackageContext{Name: files[group[0]].AST.Name.Name, Files: contexts}
		if err := analyzePackage(cr.Rule, ctx); err != nil {
			i := group[firstEnabled]
			out[i] = append(out[i], internalError(files[i], &cr, err))
			disableSuppressionAudit(group, opts)
			continue
		}
		for j, i := range group {
			for _, d := range pending[j] {
				if d.internal {
					disableSuppressionAudit(group, opts)
				}
			}
			out[i] = append(out[i], pending[j]...)
		}
	}
}

func disableSuppressionAudit(group []int, opts []LintOptions) {
	for _, i := range group {
		opts[i].ReportUnusedSuppressions = Off
	}
}

func analyzePackage(rule *goxlint.Rule, ctx *goxlint.PackageContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in AnalyzePackage: %v", r)
		}
	}()
	rule.AnalyzePackage(ctx)
	return nil
}
