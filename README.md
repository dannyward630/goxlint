<p align="center">
  <img src="docs/assets/goxlint-mascot.png" alt="The goxlint mascot, a blue oxpecker that cleans itself with a lint roller" width="200">
</p>

# goxlint

[![CI](https://github.com/egoisutolabs/goxlint/actions/workflows/ci.yml/badge.svg)](https://github.com/egoisutolabs/goxlint/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/egoisutolabs/goxlint.svg)](https://pkg.go.dev/github.com/egoisutolabs/goxlint)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

goxlint is an engine to build your own Go linter.

Write the rules that are specific to your project, in Go, in a few lines. Use
them to keep the conventions of your team, and to give coding agents rules
that their code must pass.

```text
  × acme(no-println): Do not write to stdout with `fmt.Println`.
   ╭─[server/handler.go:11:3]
10 │     if err != nil {
11 │         fmt.Println(err)
   ·         ────────────────
12 │         http.Error(w, "not found", http.StatusNotFound)
   ╰────
  help: Use the request logger.

Found 0 warnings and 1 error.
Finished in 175ms on 1 file with 1 rule using 8 threads (typed mode).
```

> **Status: v0.0.2, early release.** The API of the `goxlint`, `cli`,
> `goxlinttest`, and `goxlintanalysis` packages can change before v1.0.0. Read the
> release notes before you update.
>
> goxlint was named goose-lint in v0.0.1. Change imports from
> `github.com/egoisutolabs/goose-lint` to `github.com/egoisutolabs/goxlint`.

You write rules in Go. goxlint loads the code, runs your rules, and shows the
results. goxlint also gives configuration, automatic fixes, suppression comments,
and a test harness for rules. You do not write this infrastructure yourself.

The rule API is similar to the [oxlint](https://oxc.rs/docs/guide/usage/linter)
and ESLint rule API. A rule registers a function for each AST node type that it
examines. When the rule finds a problem, it calls `ctx.Report`.

```go
var NoFoo = &goxlint.Rule{
	Meta: goxlint.Meta{
		Name:     "no-foo",
		Category: goxlint.Suspicious,
		Messages: map[string]string{"foo": "Do not use `{{name}}`."},
	},
	Create: func(ctx *goxlint.Context) goxlint.Visitor {
		return goxlint.Visit(
			goxlint.On(func(id *ast.Ident) {
				if id.Name == "foo" {
					ctx.Report(goxlint.Diagnostic{Node: id, MessageID: "foo", Data: goxlint.Data{"name": id.Name}})
				}
			}),
		)
	},
}
```

## Why goxlint

The standard way to write a Go check is
[`go/analysis`](https://pkg.go.dev/golang.org/x/tools/go/analysis). It is a good
API for one check. But a linter needs more than checks: configuration,
suppression comments, fixes, output formats, and tests. With `go/analysis`, you
write these parts yourself, or you build a custom golangci-lint binary.

goxlint gives you these parts. You write only the rules.

The same rule, in each API:

<table>
<tr><th>go/analysis</th><th>goxlint</th></tr>
<tr><td>

```go
var Analyzer = &analysis.Analyzer{
	Name:     "noprintln",
	Doc:      "reports calls to fmt.Println",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
			call := n.(*ast.CallExpr)
			fn, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
			if ok && fn.Pkg() != nil &&
				fn.Pkg().Path() == "fmt" && fn.Name() == "Println" {
				pass.Reportf(call.Pos(), "do not use fmt.Println")
			}
		})
		return nil, nil
	},
}
```

</td><td>

```go
var NoPrintln = &goxlint.Rule{
	Meta: goxlint.Meta{
		Name:       "no-println",
		Category:   goxlint.Restriction,
		NeedsTypes: true,
		Messages: map[string]string{
			"println": "Do not use `fmt.Println`.",
		},
	},
	Create: func(ctx *goxlint.Context) goxlint.Visitor {
		return goxlint.Visit(
			goxlint.On(func(call *ast.CallExpr) {
				if ctx.IsPkgFunc(call, "fmt", "Println") {
					ctx.Report(goxlint.Diagnostic{
						Node: call, MessageID: "println",
					})
				}
			}),
		)
	},
}
```

</td></tr>
</table>

The rules are almost the same size. The difference is what you get around the
rule:

| | `go/analysis` with `singlechecker` | goxlint |
| --- | --- | --- |
| Configuration file | No. Flags only. | `.goxlintrc.json`: severities, options, ignore patterns, per-file overrides. |
| Severities | No. | `off`, `warn`, `error`, for each rule and category. |
| Suppression comments | No. | `//goxlint:ignore` for a line or a file, with unused-suppression reports. |
| Fixes | Suggested fixes, applied with `-fix`. | Fixes with three levels: `safe`, `suggestion`, `dangerous`. |
| Output | Text or JSON. | Code frames, `unix`, `json`, and GitHub annotations. |
| Rule tests | `analysistest`: a `testdata` directory with `// want` comments. | `goxlinttest`: code samples inline in the test. |
| Speed without types | Every run type-checks. | Syntax-only rules run without type-checking. |
| Package rules | Yes. | Yes. |

Use [`goxlintanalysis`](docs/go-analysis.md) to run the same rules with
`go/analysis` drivers, such as `go vet -vettool`, `multichecker`, and a
golangci-lint module plugin. The adapter keeps suppression comments and
converts safe and suggestion fixes. The host controls configuration and
output. Editor use requires a host that registers the analyzers; the adapter
does not add them to stock gopls.

## Features

| Feature             | Description                                                                                           |
| ------------------- | ----------------------------------------------------------------------------------------------------- |
| Visitor rules       | A rule registers handlers for AST node types. All rules share one walk of each file.                  |
| Package rules       | A rule can also examine all files of a package together.                                              |
| Type information    | A rule can request `go/types` data. goxlint loads types only when a rule needs them.                  |
| Configuration       | A `.goxlintrc.json` file sets severities, rule options, ignore patterns, and per-file overrides.      |
| Fixes               | A rule can attach a fix. Fixes have three levels: safe, suggestion, and dangerous.                    |
| Suppressions        | `//goxlint:ignore` comments disable a rule for a line or a file. goxlint reports unused suppressions. |
| Output              | Code frames for people. Also `unix`, `json`, and `github` formats for tools and CI.                   |
| Rule tests          | The `goxlinttest` package tests file and package rules with valid and invalid code samples.           |
| go/analysis adapter | Run rules with existing analysis drivers. See [the adapter guide](docs/go-analysis.md).               |

## Quick start

goxlint is a library. You make a small `main` package that registers your
plugins, and that program is your linter.

1. Add goxlint to your module:

    ```sh
    go get github.com/egoisutolabs/goxlint
    ```

2. Make a plugin and the command:

    ```go
    // tools/lint/main.go
    package main

    import (
    	"github.com/egoisutolabs/goxlint/cli"
    	"github.com/egoisutolabs/goxlint"
    )

    var Plugin = goxlint.Plugin{Name: "acme", Rules: []*goxlint.Rule{NoFoo}}

    func main() { cli.Main(Plugin) }
    ```

3. Run your linter:

    ```sh
    go run ./tools/lint ./...
    ```

For the full procedure, read [Getting started](docs/getting-started.md).

## Example linter

The [`examples/goxlint`](examples/goxlint/README.md) directory has a complete linter
that is built with goxlint. It has two example plugins. Use them as a reference
when you write your own rules.

```sh
go run ./examples/goxlint rules
go run ./examples/goxlint ./...
```

## Documentation

| Document                                   | Contents                                                           |
| ------------------------------------------ | ------------------------------------------------------------------ |
| [Getting started](docs/getting-started.md) | Make a linter, write a rule, and run it.                           |
| [Command line](docs/cli.md)                | Commands, flags, output formats, and exit codes.                   |
| [Configuration](docs/configuration.md)     | The `.goxlintrc.json` file.                                          |
| [Writing rules](docs/writing-rules.md)     | The rule API: metadata, visitors, the context, and reports.        |
| [Package rules](docs/package-rules.md)     | Rules that examine all files of a package.                         |
| [Fixes](docs/fixes.md)                     | Fix levels and how goxlint applies fixes.                            |
| [Suppressions](docs/suppressions.md)       | `//goxlint:ignore` comments and unused-suppression reports.          |
| [Testing rules](docs/testing-rules.md)     | The `goxlinttest` package.                                           |
| [go/analysis adapter](docs/go-analysis.md) | Use rules with go vet, multichecker, and golangci-lint. |
| [How goxlint works](docs/how-goxlint-works.md) | Loading, the walk, the package phase, and the order of operations. |

## Requirements

- Go 1.26 or later.

## Development

```sh
go test -race ./...
go vet ./...
```

To report a problem or send a change, read [Contributing](CONTRIBUTING.md).

## License

goxlint is available under the [MIT License](LICENSE).
