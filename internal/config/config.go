// Package config loads .goxlintrc.json and resolves which rules apply to each file.
//
//	{
//	  "categories": { "correctness": "error", "style": "warn" },
//	  "rules": {
//	    "anti-slop/no-any-parameters": "error",
//	    "anti-slop/require-safety-comment-for-type-assertion": ["error", { "markers": ["SAFETY"] }]
//	  },
//	  "ignorePatterns": ["gen/**"],
//	  "overrides": [
//	    { "files": ["**/*_test.go"], "rules": { "anti-slop/no-any-parameters": "off" } }
//	  ]
//	}
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/egoisutolabs/goxlint"
	"github.com/egoisutolabs/goxlint/internal/engine"
)

// FileName is the config file goxlint looks for.
const FileName = ".goxlintrc.json"

// Config is the parsed config file.
type Config struct {
	// ReportUnusedSuppressions is an engine diagnostic severity; omitted means warn.
	ReportUnusedSuppressions string                      `json:"reportUnusedSuppressions,omitempty"`
	Categories               map[goxlint.Category]string `json:"categories,omitempty"`
	Rules                    map[string]RuleSetting      `json:"rules,omitempty"`
	IgnorePatterns           []string                    `json:"ignorePatterns,omitempty"`
	Overrides                []Override                  `json:"overrides,omitempty"`
}

// Override changes rule settings for files matching any of Files.
type Override struct {
	Files []string               `json:"files"`
	Rules map[string]RuleSetting `json:"rules"`
}

// RuleSetting is "off" | "warn" | "error" or ["warn" | "error", options].
type RuleSetting struct {
	Severity engine.Severity
	Options  json.RawMessage
}

func (r *RuleSetting) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		sev, err := ParseSeverity(s)
		r.Severity = sev
		return err
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil || len(arr) == 0 || len(arr) > 2 {
		return fmt.Errorf(`rule setting must be a severity or [severity, options], got %s`, b)
	}
	if err := json.Unmarshal(arr[0], &s); err != nil {
		return fmt.Errorf("rule severity must be a string, got %s", arr[0])
	}
	sev, err := ParseSeverity(s)
	if err != nil {
		return err
	}
	r.Severity = sev
	if len(arr) == 2 {
		r.Options = arr[1]
	}
	return nil
}

func (r RuleSetting) MarshalJSON() ([]byte, error) {
	sev := severityName(r.Severity)
	if len(r.Options) == 0 {
		return json.Marshal(sev)
	}
	return json.Marshal([]any{sev, r.Options})
}

// ParseSeverity accepts oxlint's spellings.
func ParseSeverity(s string) (engine.Severity, error) {
	switch s {
	case "off", "allow":
		return engine.Off, nil
	case "warn":
		return engine.Warn, nil
	case "error", "deny":
		return engine.Error, nil
	}
	return engine.Off, fmt.Errorf("unknown severity %q (use off, warn or error)", s)
}

func severityName(s engine.Severity) string {
	switch s {
	case engine.Warn:
		return "warn"
	case engine.Error:
		return "error"
	default:
		return "off"
	}
}

// Default is used when no config file is found: correctness rules warn.
func Default() *Config {
	return &Config{Categories: map[goxlint.Category]string{goxlint.Correctness: "warn"}}
}

// Find looks for FileName in dir and its parents. It returns "" if none exists.
func Find(dir string) string {
	for {
		p := filepath.Join(dir, FileName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Load parses the config file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Registry indexes rules from plugins by "<plugin>/<rule>".
type Registry struct {
	IDs   []string
	Rules map[string]*goxlint.Rule
}

// NewRegistry builds a registry. It panics on duplicate rule IDs.
func NewRegistry(plugins ...goxlint.Plugin) *Registry {
	r := &Registry{Rules: map[string]*goxlint.Rule{}}
	for _, p := range plugins {
		for _, rule := range p.Rules {
			id := p.Name + "/" + rule.Meta.Name
			if _, dup := r.Rules[id]; dup {
				//goxlint:ignore anti-slop/no-panic-in-library plugins are compiled in, so a duplicate is a build mistake
				panic("duplicate rule " + id)
			}
			r.Rules[id] = rule
			r.IDs = append(r.IDs, id)
		}
	}
	sort.Strings(r.IDs)
	return r
}

// Resolved is a config resolved against a registry.
type Resolved struct {
	ReportUnusedSuppressions engine.Severity
	Root                     string // directory paths in the config are relative to
	base                     []engine.ConfiguredRule
	overrides                []resolvedOverride
	ignore                   []string
}

type resolvedOverride struct {
	files []string
	rules map[string]RuleSetting
}

// Resolve validates c against reg. Root is the directory containing the config.
func Resolve(c *Config, reg *Registry, root string) (*Resolved, error) {
	unused := c.ReportUnusedSuppressions
	if unused == "" {
		unused = "warn"
	}
	unusedSeverity, err := ParseSeverity(unused)
	if err != nil {
		return nil, fmt.Errorf("reportUnusedSuppressions: %w", err)
	}
	settings := map[string]RuleSetting{}
	for cat, sev := range c.Categories {
		if !slices.Contains(goxlint.Categories, cat) {
			return nil, fmt.Errorf("unknown category %q", cat)
		}
		s, err := ParseSeverity(sev)
		if err != nil {
			return nil, fmt.Errorf("category %s: %w", cat, err)
		}
		for _, id := range reg.IDs {
			if reg.Rules[id].Meta.Category == cat {
				settings[id] = RuleSetting{Severity: s}
			}
		}
	}
	if err := checkRules(c.Rules, reg); err != nil {
		return nil, err
	}
	for id, s := range c.Rules {
		settings[id] = s
	}
	r := &Resolved{Root: root, ignore: c.IgnorePatterns, base: build(settings, reg), ReportUnusedSuppressions: unusedSeverity}
	for _, o := range c.Overrides {
		if err := checkRules(o.Rules, reg); err != nil {
			return nil, err
		}
		r.overrides = append(r.overrides, resolvedOverride{files: o.Files, rules: o.Rules})
	}
	return r, nil
}

func checkRules(rules map[string]RuleSetting, reg *Registry) error {
	for id := range rules {
		if _, ok := reg.Rules[id]; !ok {
			return fmt.Errorf("unknown rule %q (run `goxlint rules` to list rules)", id)
		}
	}
	return nil
}

func build(settings map[string]RuleSetting, reg *Registry) []engine.ConfiguredRule {
	var out []engine.ConfiguredRule
	for _, id := range reg.IDs {
		s, ok := settings[id]
		if !ok || s.Severity == engine.Off {
			continue
		}
		plugin, _, _ := strings.Cut(id, "/")
		out = append(out, engine.ConfiguredRule{ID: id, Plugin: plugin, Rule: reg.Rules[id], Severity: s.Severity, Options: s.Options})
	}
	return out
}

// RulesFor returns the rules that apply to the file at path, after overrides.
func (r *Resolved) RulesFor(path string, reg *Registry) []engine.ConfiguredRule {
	rel := r.rel(path)
	var settings map[string]RuleSetting
	for _, o := range r.overrides {
		if !matchAny(o.files, rel) {
			continue
		}
		if settings == nil {
			settings = map[string]RuleSetting{}
			for _, cr := range r.base {
				settings[cr.ID] = RuleSetting{Severity: cr.Severity, Options: cr.Options}
			}
		}
		for id, s := range o.rules {
			settings[id] = s
		}
	}
	if settings == nil {
		return r.base
	}
	return build(settings, reg)
}

// AllRules returns every rule enabled anywhere in the config, used to decide
// whether type information is needed.
func (r *Resolved) AllRules() []*goxlint.Rule {
	var out []*goxlint.Rule
	for _, cr := range r.base {
		out = append(out, cr.Rule)
	}
	return out
}

// Severity returns the resolved base severity for a rule, including off for
// rules that are not enabled by the configuration. File overrides are applied
// later by RulesFor because they depend on the file being linted.
func (r *Resolved) Severity(id string) string {
	for _, cr := range r.base {
		if cr.ID == id {
			return severityName(cr.Severity)
		}
	}
	return "off"
}

// AnyOverrideNeedsTypes reports whether an override enables a rule needing types.
func (r *Resolved) AnyOverrideNeedsTypes(reg *Registry) bool {
	for _, o := range r.overrides {
		for id, s := range o.rules {
			if s.Severity != engine.Off && reg.Rules[id].Meta.NeedsTypes {
				return true
			}
		}
	}
	return false
}

// Ignored reports whether path matches an ignore pattern.
func (r *Resolved) Ignored(path string, isDir bool) bool {
	rel := r.rel(path)
	if isDir {
		return matchAny(r.ignore, rel) || matchAny(r.ignore, rel+"/")
	}
	return matchAny(r.ignore, rel)
}

func (r *Resolved) rel(path string) string {
	rel, err := filepath.Rel(r.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
