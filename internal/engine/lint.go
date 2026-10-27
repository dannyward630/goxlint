// Package engine runs goxlint rules over parsed files.
package engine

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"

	"golang.org/x/tools/go/ast/inspector"

	"github.com/egoisutolabs/goxlint"
)

// Severity is how a configured rule's diagnostics are treated.
type Severity int

const (
	Off Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Warn:
		return "warning"
	case Error:
		return "error"
	default:
		return "off"
	}
}

// ConfiguredRule is a rule enabled with a severity and options.
type ConfiguredRule struct {
	ID       string // "<plugin>/<rule>"
	Plugin   string
	Rule     *goxlint.Rule
	Severity Severity
	Options  json.RawMessage
}

// Diagnostic is a reported problem with resolved positions.
type Diagnostic struct {
	RuleID   string
	Severity Severity
	Filename string
	Start    token.Position
	End      token.Position
	Message  string
	Help     string
	Fix      *Fix
	internal bool // rule failures make unused-suppression analysis unreliable
}

// LintOptions controls engine diagnostics independently of plugin rules.
// The zero value disables unused-suppression reporting for isolated rule tests.
type LintOptions struct {
	// SinglePackage treats all input files as one package, as in an analysis.Pass.
	SinglePackage            bool
	ReportUnusedSuppressions Severity
}

// Fix is a resolved fix with byte-offset edits.
type Fix struct {
	Kind    goxlint.FixKind
	Message string
	Edits   []Edit
}

// Edit replaces Source[Start:End] with NewText.
type Edit struct {
	Start, End int
	NewText    string
}

// File is one file ready to lint.
type File struct {
	Fset     *token.FileSet
	AST      *ast.File
	Filename string
	Source   []byte
	// Pkg and Info are nil in syntax-only mode.
	Pkg  *types.Package
	Info *types.Info
	// Incomplete marks a file whose package, or a dependency, failed to load.
	// Package rules and the unused-suppression audit skip it, and the CLI
	// applies no fixes to it.
	Incomplete bool
}

type handler struct {
	rule *ConfiguredRule
	fn   func(ast.Node)
}

// LintFile runs rules over f in a single AST walk and returns unsuppressed
// diagnostics sorted by position.
func LintFile(f File, rules []ConfiguredRule, opts LintOptions) []Diagnostic {
	return LintAll([]File{f}, func(string) []ConfiguredRule { return rules }, opts)[0]
}

// lintFile collects raw diagnostics. Suppressions run only after package analysis.
func lintFile(f File, rules []ConfiguredRule, opts LintOptions) ([]Diagnostic, LintOptions) {
	var (
		diags   []Diagnostic
		stack   []ast.Node
		enter   = map[reflect.Type][]handler{}
		exit    = map[reflect.Type][]handler{}
		ifaces  []ifaceHandler // handlers for interface types such as ast.Stmt
		filter  []ast.Node
		seen    = map[reflect.Type]bool{}
		tokFile = f.Fset.File(f.AST.FileStart)
	)
	if f.Incomplete {
		opts.ReportUnusedSuppressions = Off
	}
	for i := range rules {
		cr := &rules[i]
		if cr.Severity == Off {
			continue
		}
		if cr.Rule.Create == nil {
			continue
		}
		if cr.Rule.Meta.NeedsTypes && f.Info == nil {
			opts.ReportUnusedSuppressions = Off
			continue
		}
		var pkg *types.Package
		var info *types.Info
		if cr.Rule.Meta.NeedsTypes {
			pkg, info = f.Pkg, f.Info
		}
		report := func(d goxlint.Diagnostic) {
			diags = append(diags, resolve(f, tokFile, cr, d))
		}
		ctx := goxlint.NewContext(cr.Rule, cr.ID, f.Fset, f.AST, f.Filename, f.Source, pkg, info, cr.Options, &stack, report)
		visitor, err := create(cr, ctx)
		if err != nil {
			diags = append(diags, internalError(f, cr, err))
			continue
		}
		for _, h := range visitor {
			if h.NodeType.Kind() == reflect.Interface {
				ifaces = append(ifaces, ifaceHandler{handler{rule: cr, fn: h.Fn}, h.NodeType, h.Exit})
				continue
			}
			m := enter
			if h.Exit {
				m = exit
			}
			m[h.NodeType] = append(m[h.NodeType], handler{rule: cr, fn: h.Fn})
			if !seen[h.NodeType] {
				seen[h.NodeType] = true
				// SAFETY: NodeType is a concrete type from goxlint.On[N ast.Node], so it implements ast.Node.
				filter = append(filter, reflect.Zero(h.NodeType).Interface().(ast.Node))
			}
		}
	}
	if len(filter) == 0 && len(ifaces) == 0 {
		return diags, opts
	}
	if len(ifaces) > 0 {
		filter = nil // visit every node
	}

	failed := map[*ConfiguredRule]bool{}
	call := func(h handler, n ast.Node) {
		if failed[h.rule] {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				failed[h.rule] = true
				diags = append(diags, internalError(f, h.rule, fmt.Errorf("panic: %v", r)))
			}
		}()
		h.fn(n)
	}
	inspector.New([]*ast.File{f.AST}).WithStack(filter, func(n ast.Node, push bool, s []ast.Node) bool {
		stack = s
		t := reflect.TypeOf(n)
		hs := exit[t]
		if push {
			hs = enter[t]
		}
		for _, h := range hs {
			call(h, n)
		}
		for _, h := range ifaces {
			if h.exit != push && t.Implements(h.iface) {
				call(h.handler, n)
			}
		}
		return true
	})
	return diags, opts
}

type ifaceHandler struct {
	handler
	iface reflect.Type
	exit  bool
}

func create(cr *ConfiguredRule, ctx *goxlint.Context) (v goxlint.Visitor, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in Create: %v", r)
		}
	}()
	return cr.Rule.Create(ctx), nil
}

func resolve(f File, tf *token.File, cr *ConfiguredRule, d goxlint.Diagnostic) Diagnostic {
	pos, end := d.Pos, d.End
	if d.Node != nil {
		pos, end = d.Node.Pos(), d.Node.End()
	}
	if !end.IsValid() {
		end = pos
	}
	if !pos.IsValid() || pos < token.Pos(tf.Base()) || end < pos || end > token.Pos(tf.Base()+tf.Size()) {
		return internalError(f, cr, fmt.Errorf("diagnostic positions must be ordered and within the current file"))
	}
	out := Diagnostic{
		RuleID:   cr.ID,
		Severity: cr.Severity,
		Filename: f.Filename,
		// Physical positions match Source, the code frame and suppressions;
		// //line directives would point outside this file.
		Start:   f.Fset.PositionFor(pos, false),
		End:     f.Fset.PositionFor(end, false),
		Message: d.Message,
		Help:    d.Help,
	}
	out.Start.Filename, out.End.Filename = f.Filename, f.Filename
	if d.Fix != nil {
		fix := &Fix{Kind: d.Fix.Kind, Message: d.Fix.Message}
		for _, e := range d.Fix.Edits {
			// token.File.Offset clamps invalid positions, which could turn a
			// broken plugin edit into an unintended change at a file boundary.
			if !e.Pos.IsValid() || e.Pos < token.Pos(tf.Base()) || e.End < e.Pos || e.End > token.Pos(tf.Base()+tf.Size()) {
				return internalError(f, cr, fmt.Errorf("invalid fix edit: positions must be ordered and within the current file"))
			}
			fix.Edits = append(fix.Edits, Edit{Start: tf.Offset(e.Pos), End: tf.Offset(e.End), NewText: e.NewText})
		}
		out.Fix = fix
	}
	return out
}

func internalError(f File, cr *ConfiguredRule, err error) Diagnostic {
	pos := f.Fset.PositionFor(f.AST.Package, false)
	pos.Filename = f.Filename
	return Diagnostic{
		RuleID:   cr.ID,
		Severity: Error,
		Filename: f.Filename,
		Start:    pos,
		End:      pos,
		Message:  fmt.Sprintf("internal error in rule: %v", err),
		internal: true,
	}
}

func finish(f File, diags []Diagnostic, opts LintOptions) []Diagnostic {
	for _, d := range diags {
		if d.internal {
			opts.ReportUnusedSuppressions = Off
			break
		}
	}
	diags = applySuppressions(f, diags, opts.ReportUnusedSuppressions)
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i].Start, diags[j].Start
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return diags
}
