# Testing rules

The `goxlinttest` package tests file and package rules with code samples. It has
the same function as the ESLint and oxlint `RuleTester`.

## A test

```go
package rules

import (
	"testing"

	"github.com/egoisutolabs/goxlint/goxlinttest"
)

func TestMaxParams(t *testing.T) {
	goxlinttest.Run(t, MaxParams, goxlinttest.Cases{
		Valid: []goxlinttest.Case{
			{Code: "func f(a, b int) {}"},
			{Code: "func f(a, b, c int) {}", Options: map[string]int{"max": 3}},
		},
		Invalid: []goxlinttest.Case{
			{
				Code:   "func f(a, b, c, d, e int) {}",
				Errors: []goxlinttest.Error{{MessageID: "tooMany", Line: 1, Column: 6}},
			},
		},
	})
}
```

`goxlinttest.Run` makes one subtest for each case. The subtest names are
`valid/0`, `invalid/0`, and so on. Set `Name` to give a case a better name.

## Cases

| Field | Description |
| --- | --- |
| `Name` | Optional. The subtest name. |
| `Code` | The Go source of the case. |
| `Filename` | Optional. The default is `p.go`. Use a `_test.go` name to test the behavior of the rule on test files. |
| `Options` | Optional. goxlint encodes this value as JSON and gives it to the rule as its options. |
| `Errors` | Invalid cases only. The diagnostics that you expect, in the order of their positions. |
| `Output` | Invalid cases only. The code that you expect after goxlint applies all fixes. |

A valid case fails if the rule reports a diagnostic. An invalid case fails if
the number of diagnostics is not the number of `Errors`, or if a diagnostic
does not agree with its `Error`.

## Expected errors

Each `goxlinttest.Error` can examine these fields. goxlint does not examine a field
with the zero value.

| Field | Examines |
| --- | --- |
| `MessageID` | The message agrees with this message template. |
| `Message` | The message is exactly this text. |
| `Line` | The line of the start position, relative to `Code`. |
| `Column` | The column of the start position, in bytes, starting at 1. |

Use `MessageID` for most cases. Use `Message` to examine the values in the
placeholders.

## The code

These conditions apply to the `Code` field:

- If `Code` has no `package` clause, `goxlinttest` adds `package p`. Line numbers
  stay relative to `Code`.
- `goxlinttest` type-checks all cases. The code must compile. Unused variables and
  unused imports are permitted.
- The code can import packages from the standard library.
- Each case is one file.

`goxlinttest` always type-checks the code. The rule gets the type information
only when it has `NeedsTypes`. This is the same behavior as in the linter.

## Fixes

Set `Output` to test a fix. `goxlinttest` applies all fixes of all levels and
compares the result with `Output`. This example uses a rule that removes the
prefix `failed to` from error messages:

```go
{
	Code:   "func f() error { return errors.New(\"failed to open\") }",
	Errors: []goxlinttest.Error{{MessageID: "redundantPrefix"}},
	Output: "func f() error { return errors.New(\"open\") }",
},
```

- If the rule makes a fix and `Output` is empty, the test fails. The failure
  message shows the fixed code.
- If `Output` is set and the rule makes no fix, the test fails.
- `Output` does not contain the `package p` line that `goxlinttest` adds.

## Options

Test the default options and each option. Also test options that are not
correct:

```go
{
	Code:    "func f() {}",
	Options: map[string]any{"typo": 1},
	Errors:  []goxlinttest.Error{{Message: `invalid options for test/rule: json: unknown field "typo"`}},
},
```

In tests, the rule ID is always `test/rule`.

## Test files

Set `Filename` to a name that ends with `_test.go`:

```go
{Code: "func f(v any) {}", Filename: "p_test.go"},
```

## Package rules

Use `goxlinttest.RunPackage` for a case with more than one file. It runs both
`Create` and `AnalyzePackage`. All files form one package, even when their
names contain different directories.

This example tests `LargePackage` from [Package rules](package-rules.md):

```go
package rules

import (
	"testing"

	"github.com/egoisutolabs/goxlint/goxlinttest"
)

func TestLargePackage(t *testing.T) {
	goxlinttest.RunPackage(t, LargePackage, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{
			{
				Name:    "within limit",
				Files:   map[string]string{"a.go": "package p"},
				Options: map[string]int{"maxFiles": 1},
			},
		},
		Invalid: []goxlinttest.PackageCase{
			{
				Name: "disabled file still counts",
				Files: map[string]string{
					"a.go": "package p",
					"b.go": "package p",
				},
				Options:     map[string]int{"maxFiles": 1},
				FileOptions: map[string]any{"b.go": nil},
				Errors: []goxlinttest.PackageError{
					{Filename: "a.go", MessageID: "large", Line: 1, Column: 9},
				},
			},
		},
	})
}
```

`RunPackage` takes a `PackageCases` struct with `Valid` and `Invalid` lists,
like `Run` takes `Cases`. It uses the same subtest names as `Run`. Each
`PackageCase` has these fields:

| Field | Description |
| --- | --- |
| `Name` | Optional. The subtest name. |
| `Files` | A map from filename to Go source. Supply at least one file. Filenames must not be empty. |
| `Options` | Optional. The rule options for all files. |
| `FileOptions` | Optional. Options that replace `Options` for named files. Each key must name a file in `Files`. A `nil` value disables the rule for that file. |
| `Errors` | Invalid cases only. Expected diagnostics, ordered by filename, then line and column. |

A disabled file stays in `PackageContext.Files` with `Enabled == false`. Its
source still participates in type-checking. File visitors do not run for that
file, and reports from its context are ignored. A disabled file has no rule
options. If all files are disabled, neither rule phase runs.

Use a literal `nil` in `FileOptions` to disable a file. A typed nil pointer,
such as `(*Opts)(nil)`, stays enabled and is encoded as JSON `null`.

Each `PackageError` has the fields of `Error`, plus `Filename`. Fields with a
zero value are not checked. Lines are relative to each source string in
`Files`. If a file has no package clause, the harness adds `package p` and
adjusts diagnostic lines, as `Run` does. All files must have the same package
name. The harness type-checks them together. Unused variables and imports are
permitted. Type information reaches the rule only if it has `NeedsTypes`.

Use `test/rule` in suppression comments. Suppressions apply to diagnostics from
both rule phases. The harness does not report unused suppressions. An internal
rule error fails the test and shows the error message, even if it matches an
expected error.

`RunPackage` does not check fix output. It does not load packages from disk or
apply build tags. It does not create Go test variants. Put a `_test.go` file in
`Files` to test filename-based rule behavior.

## Recommendations

- Add a valid case for each pattern that the rule must not report. False
  positives are the most frequent rule defect.
- Add an invalid case for each form of the problem.
- Examine `Line` and `Column` in one or more cases. They make sure that the
  report is on the correct node.
- Run the tests with `-race`.
