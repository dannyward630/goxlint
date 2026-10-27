# Contributing to goxlint

Thank you for your help. This document tells you how to report problems, how
to set up the project, and how to send a change.

The documents in this repository use ASD-STE100 Simplified Technical English.
Read [Style of the documents](#style-of-the-documents).

## Report a problem

To report a security problem, do not open an issue. Read
[SECURITY.md](SECURITY.md).

For other problems, open an issue on GitHub. Include this information:

- The goxlint version, or the commit.
- The output of `go version`.
- The command that you ran, and the full output.
- Your `.goxlintrc.json`, if you have one.
- The smallest Go code that shows the problem.
- The result that you expected.

If a rule from your own plugin causes the problem, include the rule if
possible. If you cannot share it, describe what the rule does.

## Request a feature

Open an issue before you write a large change. Describe the problem that the
feature solves, not only the solution. We can then agree on the design before
you start.

Small corrections, for example errors in the documents, do not need an issue.

## Set up the project

You must have Go 1.26 or later.

```sh
git clone https://github.com/egoisutolabs/goxlint.git
cd goxlint
go test ./...
```

## Repository layout

| Path | Contents |
| --- | --- |
| `rule.go`, `context.go` | The `goxlint` package: the rule API. |
| `goxlinttest/` | The `goxlinttest` package: the rule test harness. |
| `cli/` | The `cli` package: the command-line runner. |
| `internal/engine/` | Loading, the walk, the package phase, suppressions, and fixes. |
| `internal/config/` | The `.goxlintrc.json` file and rule resolution. |
| `internal/output/` | The output formats. |
| `examples/goxlint/` | An example linter with example plugins. |
| `docs/` | The user documents. |

The engine is the product. The plugins in `examples/` show how to use the
engine. Put engine behavior in the engine, not in an example plugin.

## Make a change

1. Fork the repository and make a branch from `main`.
2. Make the change.
3. Add or change tests. Each change in behavior must have a test.
4. Update the documents in `docs/` and `README.md` if the change affects users.
5. Do the checks in [Before you send a pull request](#before-you-send-a-pull-request).
6. Open a pull request.

### Public API

These packages are the public API:

- `github.com/egoisutolabs/goxlint`
- `github.com/egoisutolabs/goxlint/cli`
- `github.com/egoisutolabs/goxlint/goxlinttest`
- `github.com/egoisutolabs/goxlint/goxlintanalysis`

goxlint is before v1.0.0. A release can change the public API. But each change
to the public API must have a reason. Write the reason in the pull request,
and write what users must change.

Do not export a name only for tests. Put internal code in `internal/`.

### Dependencies

goxlint has one dependency: `golang.org/x/tools`. Do not add a dependency
without an issue that explains why it is necessary.

Do not change the `go` line in `go.mod` to an earlier version. The minimum
version is Go 1.26.

### Code

- Write code that agrees with the code near it: names, comments, and idiom.
- Keep comments short. Write why the code does something, not what it does.
- Use `token.FileSet.PositionFor(pos, false)` for positions that users see.
  goxlint uses physical positions and ignores `//line` directives.
- A rule must not cause the engine to stop. Report rule failures as internal
  errors.

### Tests

- Engine tests are in `internal/engine`. Command-line tests are in `cli`.
- Test rule behavior with `goxlinttest`. Read
  [Testing rules](docs/testing-rules.md).
- Do not use the network in tests.
- Make tests that are independent of the order and of the operating system.

## Before you send a pull request

Do the same checks as CI:

```sh
gofmt -l .
go vet ./...
go test -race ./...
go run ./examples/goxlint ./...
```

- `gofmt -l .` must show no files.
- goxlint lints its own code with the example linter. The run must show no
  diagnostics. If a diagnostic is not correct, add a `//goxlint:ignore`
  comment with a reason.

## Pull requests

- Make one logical change in each pull request.
- Write a title that tells what the change does, for example
  `engine: skip the audit for incomplete files`.
- In the description, write what changed and why. Refer to the issue, for
  example `Fixes #4`.
- Keep the commit history clean. We can squash the commits when we merge.

## Style of the documents

Write the documents in `docs/` and `README.md` in ASD-STE100 Simplified
Technical English:

- Write short sentences. Write one instruction in each sentence.
- Use the active voice.
- Use one word for one meaning. Use the terms in
  [the document index](docs/README.md).
- Write procedures as numbered steps.
- Use `**CAUTION:**` for a risk to the user's code or results.

Make sure that each code sample compiles and runs.

## Releases

The maintainers make the releases:

1. Make sure that CI passes on `main`.
2. Update the version in the status note in `README.md`.
3. Tag the commit, for example `git tag v0.0.3`, and push the tag.
4. Write the release notes on GitHub. Write each change to the public API.

The Go module proxy and pkg.go.dev get the new version from the tag.

## License

goxlint uses the [MIT License](LICENSE). When you send a contribution, you
agree that it is available under this license.
