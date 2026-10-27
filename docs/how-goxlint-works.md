# How goxlint works

This document describes what goxlint does during a run. Read it to understand
performance, load errors, and the order of results.

## Overview

1. Read the command line and the configuration.
2. Select the load mode.
3. Load the files.
4. Run the file rules on each file.
5. Run the package rules on each package.
6. Apply suppressions and report unused suppressions.
7. Apply fixes, if a fix flag is set. Then load and lint again.
8. Show the diagnostics and the summary. Exit with a code.

## 1. Configuration

goxlint finds `.goxlintrc.json` and decodes it. Then it applies the `-D`, `-W`, and
`-A` flags. Then it validates all rule IDs and categories against the
registered plugins. An error stops the run with exit code `2`. goxlint does not
load or change files before this step is complete.

## 2. Load mode

goxlint has two load modes.

| Mode | When | What a rule gets |
| --- | --- | --- |
| Syntax mode | No enabled rule has `NeedsTypes`. | The syntax tree and the source. |
| Typed mode | One or more enabled rules have `NeedsTypes`. | Also `Pkg` and `TypesInfo`. |

Syntax mode is much faster. It only parses files.

## 3. Loading

### Syntax mode

goxlint expands the patterns itself:

- `dir/...` walks the directory tree. goxlint skips `vendor`, `testdata`,
  `node_modules`, and directories whose names start with `.` or `_`.
- goxlint reads all `.go` files. It does not examine build constraints.
- goxlint does not stop at nested modules.
- goxlint parses the files at the same time on all processors.

### Typed mode

goxlint uses `golang.org/x/tools/go/packages`. It has the same behavior as
`go build` and `go vet`:

- The Go tool expands the patterns. `./...` does not go into nested modules.
- goxlint loads test files too.
- Build constraints apply. Files that need a build tag are not loaded. Give
  the tags with `--tags`.
- A `.go` file pattern loads the package that contains the file.

Each file is linted one time, also when it is in more than one variant of a
package.

### Both modes

goxlint does not lint generated files or files that agree with `ignorePatterns`.

### Load errors

A load error occurs when:

- A file or a pattern does not exist.
- A file has a syntax error.
- A package has a type error (typed mode).

A load error does not stop the run. goxlint shows each error one time on stderr.
It lints all files that it can, and it exits with code `2`.

A load error makes some results unreliable. goxlint marks these files as
incomplete:

- All files in the directory of a file that did not load (syntax mode).
- All files of a package that has errors (typed mode).
- All files of a package that imports a package with errors, directly or
  through other packages (typed mode). Their type information can be partial.

For an incomplete file, goxlint:

- Runs the file rules and shows their diagnostics.
- Does not run package rules on its package.
- Does not report unused suppressions.
- Does not apply fixes.

Files in other packages are not affected.

## 4. File rules

For each file, goxlint finds the enabled rules after overrides. It calls `Create`
of each rule. Then it walks the syntax tree one time. During the walk, it calls
the handlers of all rules.

- goxlint visits only the node types that the rules registered. An interface
  handler, such as `ast.Stmt`, makes goxlint visit all nodes.
- goxlint lints different files at the same time.
- If a rule causes a panic, goxlint stops that rule on that file and reports an
  internal error. Other rules continue.

## 5. Package rules

goxlint puts the files in groups by directory and package name. For each group,
it runs each enabled package rule one time, in rule ID order. Different groups
run at the same time. Read [Package rules](package-rules.md).

## 6. Suppressions

goxlint applies suppressions to the diagnostics of both phases together. Then it
adds unused-suppression reports. Read [Suppressions](suppressions.md).

## 7. Fixes

If you give a fix flag, goxlint applies the permitted fixes and writes the files.
Then it loads and lints all files again. It repeats this process until no fixes
apply, with a limit of ten passes. The output shows the diagnostics after the
last pass. Read [Fixes](fixes.md).

## 8. Output

goxlint sorts the diagnostics of each file by line and column. It shows them in
the selected format. The `default` format also shows a summary:

```text
Found 2 warnings and 1 error.
Finished in 412ms on 38 files with 6 rules using 8 threads (typed mode).
```

The rule count is the number of rules that the top-level configuration
enables. Rules that only an override enables are not in the count.

## Positions

All positions are physical positions in the file. goxlint ignores `//line`
directives. Thus the line and column always agree with the code frame and with
suppression comments.

## Performance

- Keep rules in syntax mode when possible. Typed mode type-checks all
  dependencies.
- Register concrete node types, not interface types.
- Do expensive work only when a cheap test agrees. For example, compare a name
  before you get type information.
