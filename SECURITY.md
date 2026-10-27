# Security policy

## Supported versions

goxlint is before v1.0.0. We correct security problems only in the latest
release.

| Version | Supported |
| --- | --- |
| Latest v0.x release | Yes |
| Earlier releases | No |
| `github.com/egoisutolabs/goose-lint` (v0.0.1) | No. Use `github.com/egoisutolabs/goxlint`. |

## Report a vulnerability

Do not open a public issue for a security problem.

1. Go to the [Security tab](https://github.com/egoisutolabs/goxlint/security)
   of the repository.
2. Select **Report a vulnerability**.
3. Write the report. Include this information:
   - The goxlint version or commit, and the output of `go version`.
   - The steps, the input files, and the configuration that show the problem.
   - The effect, for example a file that goxlint changes without permission.

GitHub sends the report only to the maintainers.

## What to expect

- We reply to the report in 7 days or less.
- We tell you if we accept the report, and we tell you about the progress of
  the correction.
- We publish a GitHub security advisory and a new release when the correction
  is ready. If you agree, we name you in the advisory.

Do not disclose the problem in public before the advisory is published.

## Scope

These are security problems in goxlint:

- `--fix` or `goxlint init` writes to a file that the run did not select.
- A configuration file or a Go source file causes goxlint to read or write
  files outside the project, or to run a program.
- Go source that goxlint lints causes the engine to stop responding or to use
  memory without limit. A rule that is slow or has a defect is not included.

These are not security problems in goxlint:

- Defects in rules from plugins. A plugin is Go code that you compile into
  your linter. It has the same access as your program. Report these to the
  owner of the plugin.
- Defects in the example plugins in `examples/`. Open a public issue.
- Problems in the `go` command or in `golang.org/x/tools`. goxlint uses the
  `go` command to load packages in typed mode. Report these problems to the
  [Go project](https://go.dev/security/policy).
