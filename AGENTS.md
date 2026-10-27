# AGENTS.md

Instructions for AI coding agents that work in this repository. Human
contributors: read [CONTRIBUTING.md](CONTRIBUTING.md). The rules there also
apply to agents.

## Project

goxlint is an engine to build your own Go linter, with an oxlint-style rule
API. The engine is the product. The plugins in `examples/` are only examples.
Do not describe the example rules as the purpose of the project.

Module: `github.com/egoisutolabs/goxlint`. Minimum Go version: 1.26.

## Commands

Run all of these before you finish. CI runs the same checks.

```sh
gofmt -l .                       # must print nothing
go vet ./...
go test -race ./...
go run ./examples/goxlint ./...  # goxlint lints itself; must report 0 diagnostics
```

Run one package: `go test ./internal/engine -run TestName`.

## Layout

| Path | Contents |
| --- | --- |
| `rule.go`, `context.go` | Package `goxlint`: the public rule API. |
| `goxlinttest/` | Package `goxlinttest`: the public rule test harness. |
| `cli/` | Package `cli`: the public command-line runner. |
| `internal/engine/` | Loading, the AST walk, package rules, suppressions, fixes. |
| `internal/config/` | `.goxlintrc.json` parsing and rule resolution. |
| `internal/output/` | Output formats: default, unix, json, github. |
| `examples/goxlint/` | An example linter. Its tests run the CLI end to end. |
| `docs/` | User documents. Read `docs/how-goxlint-works.md` before you change the engine. |

## Rules

- Keep `go 1.26.0` in `go.mod`. Do not lower it.
- Do not add dependencies. The only dependency is `golang.org/x/tools`.
- The public API is the `goxlint`, `cli`, and `goxlinttest` packages. Do not
  change it unless the task asks for it. If you change it, say so in the pull
  request and write what users must change.
- Put new non-public code in `internal/`. Do not export names only for tests.
- Every change in behavior needs a test. Tests must not use the network and
  must pass on Linux, macOS, and Windows (use `filepath.ToSlash` when you
  compare paths in output).
- Use `token.FileSet.PositionFor(pos, false)` for positions that users see.
  goxlint reports physical positions and ignores `//line` directives.
- A failing rule must not stop the run. The engine turns rule panics and bad
  reports into internal-error diagnostics.
- A load error in one package must not change results for other packages.
  Load errors mark only the affected files as `Incomplete`.
- Match the code around your change: names, comment density, idiom.
- Do not add a `//goxlint:ignore` comment to make the self-lint pass unless
  the diagnostic is wrong. Always write the reason in the comment.

## Documents

- Write `docs/`, `README.md`, and other Markdown files for users in ASD-STE100
  Simplified Technical English: short sentences, active voice, one meaning for
  each word, numbered steps for procedures. Use the terms in `docs/README.md`.
- Update the documents in the same change as the behavior.
- Make sure that each Go code sample in the documents compiles.

## Pull requests

- Make one logical change in each pull request. Refer to the issue, for
  example `Closes #10`.
- In the description, write what changed, why, and which checks you ran.
- Say that the change is AI-assisted, and name the tool.
- Do not commit generated binaries, coverage files, or local editor files.

## Security

Do not report security problems in public issues or pull requests. Read
[SECURITY.md](SECURITY.md).
