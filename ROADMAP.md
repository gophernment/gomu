# Gomu roadmap and hand-over notes

For people and AI agents who pick this project up later. Read `README.md` for what Gomu does and
`CLAUDE.md` (if present locally) for the working rules. This file says where the work stopped and what to
watch out for.

## State

Feature complete for the first release: discovery, sandbox copy, baseline with coverage, five operators
(`arithmetic`, `conditional`, `logical`, `deletion`, `return`), text/JSON/HTML reports, threshold exit code.
Standard library only.

Dogfood (`go run ./cmd/gomu ./...`, about 5 minutes) gives **99.8%**: one survivor, `internal/adapter/fsys/sandbox.go`
(`err == nil && cerr != nil` after `out.Close()`), which cannot be tested without a file system abstraction.
Coverage: every package is at or above 93%.

## Backlog, most useful first

1. **Test files in parallel.** Files are tested one after another; only the mutants of one file run in
   parallel, so a small file leaves cores idle. Dogfood takes about 5 minutes. A sketch: run several `Execute`
   contexts at once, but all workers share one module copy, see "Shared copy" below, so cap the total number of
   concurrent `go test` runs rather than the number per file.
2. **`-operators` flag** to enable a subset (for example `-operators arithmetic,deletion`). Not added yet because
   of YAGNI; worth it once runs get long. Adding it means a name lookup in `cmd/gomu` over the `Registry`.
3. **More operators**, one file each (see "Adding an operator"): constants (`0`/`1`, `true`/`false`), removing `!`,
   `break`/`continue`, `i++`/`i--`, `return` for named result types (needs type information, which Gomu does not
   have; `go/types` would be a large step).
4. **Process-group kill** on timeout. Today a hung `go test` is stopped by `-timeout`, then by a context backstop
   plus `WaitDelay`; children it started can outlive it.
5. **Incremental mode**, mutant de-duplication or ranking, per-file breakdown in reports, ANSI colours.
6. **CI documentation**: a GitHub Actions example using `-format json` or `html` as an artifact and `-threshold`.
7. **`go.work` and `replace` outside the module** do not work in the sandbox copy; supporting them means copying
   or linking more than the module root.

### Not worth doing

21 mutants stay "not covered" on purpose: error branches that cannot happen (`filepath.Abs` failing, `Stat`
failing after `Open` worked, `json.Marshal` of a fixed struct, a duplicate in the fixed operator list,
`main()`/`os.Exit`). Covering them would need fault-injection seams added only for tests.

## Known limits

- The module root must contain `go.mod`; a module over 200,000 files or 2 GiB is refused.
- **Shared copy.** Workers share one copy of the module. A test that writes into its package directory can
  disturb later mutants. Packages that then fail to load are reported as `not viable`, never as kills.
- `+` on two non-literal strings gives a mutant that does not compile (`not viable`, not scored).
- No type information: `return` only handles result types that are plainly bool, string, numeric, error, `any`,
  pointer, slice, map, channel, func or interface.

## Pitfalls that already cost time

- **Never run Gomu on a tree without the sandbox, and never run mutated code anywhere else.** A mutant can do
  anything, including deleting files. The first dogfood run deleted `internal/adapter/fsys`. The sandbox copy and
  `Close` guards (`ErrNotOwned`) exist because of that, with regression tests.
- **Tests must never write below a path that a mutant can turn into `""` or a relative path.** `""` means the
  current directory, which for `go test` is the package being mutated. Check `filepath.IsAbs` and use
  `t.Fatalf` (not `t.Errorf`) before writing or removing; in tests that make a function fail on purpose, call
  `t.Chdir(t.TempDir())` first. Symptom of a miss: a flood of `not viable` mutants after one point in a file.
- **Equivalent mutants** (the mutation changes nothing observable) should be removed by simplifying the code,
  not hidden by contrived tests. Typical: a fast path whose result equals the slow path, a check repeated one
  call later, an unobservable return value.
- **Error messages must not contain absolute paths.** Wrap OS errors with the package's `cause(err)`; `fsys`,
  `goast`, `gotest` and `cmd/gomu` have one each. A test checks each place.
- Mutated code that stops cleaning up leaves temp directories. `go test` runs with `TMPDIR` inside the sandbox
  (`Sandbox.TempDir`, `Runner.WithTempDir`) so `Close` removes them. If `ls $TMPDIR/gomu-*` shows leftovers after
  a dogfood run, delete them and look at whether a mutant disabled that protection.
- Adding an operator changes mutant counts: the e2e tests in `cmd/gomu/main_test.go` assert exact counts and
  scores on small fixtures and need updating.

## Adding an operator

1. `internal/mutator/<name>.go`: a type with `Name()` and `Mutations(ast.Node) []Mutation`; `Apply` changes the
   node in place, `Revert` restores it exactly. Do not modify the node in `Mutations`.
2. `internal/mutator/<name>_test.go`: each mutation, nodes it must not touch, nil nodes, `Revert`, and that the
   result still parses. `mutateFile` in `helper_test.go` does most of it.
3. Register it in `cmd/gomu/main.go`; add a row to the operator table in `README.md`.
4. Run the dogfood and fix the survivors it finds; update the e2e counts.

## Checks before a commit

```sh
gofmt -l .                 # prints nothing
go vet ./...
go test -race -cover ./...
go run ./cmd/gomu ./...    # about 5 minutes; keep the score high
```
