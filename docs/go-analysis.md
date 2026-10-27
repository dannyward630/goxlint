# Use rules with go/analysis

The `goxlintanalysis` package converts goxlint rules to
`golang.org/x/tools/go/analysis.Analyzer` values. Use these values with an
analysis driver, such as `singlechecker`, `multichecker`, `go vet -vettool`,
golangci-lint, or Bazel `nogo`.

The adapter uses the existing `golang.org/x/tools` dependency. It does not
add a dependency to goxlint.

## Convert rules

Use `goxlintanalysis.Analyzer(pluginName, rule)` for one rule. Use
`goxlintanalysis.Analyzers(plugins...)` for all rules in a set of plugins.

`Analyzers` returns rules in rule ID order. Analyzer names use underscores
instead of characters that are not valid in a Go identifier. For example,
`acme/no-println` becomes `acme_no_println`. A name that starts with a digit
gets an underscore prefix. `Analyzers` adds numeric suffixes when names
conflict. If you combine calls to `Analyzer` yourself, give each result a
different `Name` before you pass it to a driver.

To supply rule options, pass `goxlintanalysis.WithOptions` to `Analyzer`.
The argument is a `json.RawMessage` with the same options object that the
rule accepts in `.goxlintrc.json`. Do not include the severity or the outer
array. The adapter copies the JSON. The rule reads it with `ctx.Options`.
Invalid options produce a diagnostic when the rule calls `ctx.Options`.

For example, this complete program sets a complexity limit:

```go
package main

import (
	"encoding/json"

	"github.com/egoisutolabs/goxlint/examples/goxlint/plugins/maintainability"
	"github.com/egoisutolabs/goxlint/goxlintanalysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	analyzer := goxlintanalysis.Analyzer(
		"maintainability",
		maintainability.CyclomaticComplexity,
		goxlintanalysis.WithOptions(json.RawMessage(`{"max": 12}`)),
	)
	singlechecker.Main(analyzer)
}
```

The programs in this document use example plugins. Replace them with your
own plugins when you build a linter for your project.

## Make a multichecker command

1. Add a goxlint version that includes `goxlintanalysis` to your Go module:

    ```sh
    go get github.com/egoisutolabs/goxlint
    ```

2. Create `tools/vet/main.go` with this code:

    ```go
    package main

    import (
    	"github.com/egoisutolabs/goxlint/examples/goxlint/plugins/antislop"
    	"github.com/egoisutolabs/goxlint/goxlintanalysis"
    	"golang.org/x/tools/go/analysis/multichecker"
    )

    func main() {
    	multichecker.Main(goxlintanalysis.Analyzers(antislop.Plugin)...)
    }
    ```

3. Run the command directly:

    ```sh
    go run ./tools/vet ./...
    ```

4. To use the command with `go vet`, build it:

    ```sh
    go build -o ./goxlint-vet ./tools/vet
    ```

5. Run `go vet` with the command:

    ```sh
    go vet -vettool=./goxlint-vet ./...
    ```

On Windows, use `goxlint-vet.exe` as the output file and in `-vettool`.
Use the driver's `-help` output to see its flags.

## Make a golangci-lint module plugin

A golangci-lint module plugin needs a registration package. This dependency
belongs to your plugin module, not to goxlint.

1. Add the registration package to your module:

    ```sh
    go get github.com/golangci/plugin-module-register/register
    ```

2. Create `tools/gclplugin/plugin.go` with this code:

    ```go
    package gclplugin

    import (
    	"github.com/egoisutolabs/goxlint/examples/goxlint/plugins/antislop"
    	"github.com/egoisutolabs/goxlint/goxlintanalysis"
    	"github.com/golangci/plugin-module-register/register"
    	"golang.org/x/tools/go/analysis"
    )

    func init() {
    	register.Plugin("project-rules", newPlugin)
    }

    func newPlugin(_ any) (register.LinterPlugin, error) {
    	return plugin{}, nil
    }

    type plugin struct{}

    func (plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
    	return goxlintanalysis.Analyzers(antislop.Plugin), nil
    }

    func (plugin) GetLoadMode() string {
    	return register.LoadModeTypesInfo
    }
    ```

3. Configure a custom golangci-lint build to import your registration package.
   Follow the [module plugin procedure](https://golangci-lint.run/docs/plugins/module-plugins/).
4. Enable the plugin in your `.golangci.yml`:

    ```yaml
    version: "2"
    linters:
      default: none
      enable:
        - project-rules
      settings:
        custom:
          project-rules:
            type: module
            description: Project rules built with goxlint.
    ```

5. Run the custom golangci-lint binary.

Use `register.LoadModeTypesInfo` when any rule needs types. The example above
uses typed rules. The constructor can also decode plugin settings and pass
rule options to `goxlintanalysis.Analyzer`.

## Driver behavior

The analysis driver loads and type-checks packages. The adapter uses its
syntax trees, file set, package, and type information. It does not load
packages again. Syntax-only rules still receive no type information through
the goxlint context, but the driver can incur the cost of type-checking.

The adapter reads source through `pass.ReadFile`. This supports unsaved editor
buffers and virtual files. If an older driver does not supply `ReadFile`, the
adapter reads from disk. A source read error returns an analysis error. It
does not produce a clean result from an incomplete source snapshot.

All non-generated files in the pass form one package for package rules.
The adapter skips generated files before it reads or analyzes them. It reports
physical token positions, not positions changed by `//line` directives. The
host decides how to display those positions.

Each analyzer runs one rule. It keeps `//goxlint:ignore` and
`//goxlint:ignore-file` comments. The targets use the original rule ID, such as
`acme/no-println`, not the analyzer name. The unused-suppression audit is off:
a suppression for another analyzer cannot be checked in this pass.

Diagnostics include the rule message and help text. Their `Category` is the
rule category. A rule panic or an invalid report becomes an internal-error
diagnostic. It does not stop the analysis driver.

The adapter does not read `.goxlintrc.json`. It does not select rules by their
category. `Analyzers` converts every rule supplied by its plugins. Select the
analyzers and set the severity with your host's configuration. Use
`WithOptions` to supply each rule's options. Use the goxlint CLI when you need
its configuration file, output formats, or unused-suppression audit.

## Suggested fixes

The adapter converts `safe` and `suggestion` fixes to `analysis.SuggestedFix`
values. It does not convert `dangerous` fixes. It also omits empty fixes and
fixes with invalid or overlapping edits. The adapter never writes a file.

**CAUTION:** A `suggestion` fix can change behavior. An analysis driver can
apply suggested fixes automatically, for example with `-fix`. Review the
changes before you use them.

## Editors and gopls

The adapter produces the diagnostic and fix types used by analysis drivers
in editors. It does not register analyzers in a running gopls process.
Stock gopls does not load an arbitrary analyzer from a module setting. A
custom integration must register the analyzer, or run the checker as an
external tool. See the [upstream gopls extension issue](https://github.com/golang/go/issues/59869).

This package does not provide an LSP server or a gopls extension.
