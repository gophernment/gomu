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

## Goal: better than gremlins and go-mutesting, in every dimension

Competitor facts come from the READMEs of <https://github.com/go-gremlins/gremlins> and
<https://github.com/avito-tech/go-mutesting> and from <https://gremlins.dev/latest/>, read on 2026-10-14.
Re-check them before quoting. "Better" is only claimed with numbers: see "Proving it".

### Where each tool stands

| Dimension | gremlins | go-mutesting | Gomu v0.1.0 | Target |
| --- | --- | --- | --- | --- |
| Operators | 11 types, each switchable by flag; 5 on by default | Widest set: arithmetic, bitwise, assign, loop control, number inc/dec, branch emptying, `&&`/`\|\|` term removal, statement removal | 5 operators | Superset of both, each selectable |
| Operator selection | one `--<name>` flag per type | config and annotations | none | `-operators`, `-exclude-operators` |
| Speed | worker pool, tests only covered mutants; its docs say big modules take hours | no coverage, no parallelism mentioned | one module copy + `-overlay`; mutants of one file in parallel, files in sequence | schemata or precompiled binaries, cache, cross-file parallelism |
| Changed-code mode | `--diff <ref>` | none | none | `-diff <ref>` |
| False-positive control | `--exclude-files` | annotations (`mutator-disable-*`), MD5 blacklist ("badly implemented" per its README) | none | annotations, stable ignore list, config |
| Thresholds / exit codes | efficacy (exit 10) and mutant coverage (exit 11) | none seen | one score threshold (exit 1) | both thresholds with distinct exit codes |
| Reports | JSON via `-o`, status filter | JSON, HTML | text, JSON, HTML | + SARIF, JUnit, GitHub annotations, source context in HTML |
| CI | GitHub Action, Docker image | none seen | none | Action, Docker, binaries, example workflows |
| Config file | `.gremlins.yaml` | YAML | none | one config file (JSON, stdlib only, or ask before adding a dependency) |
| Build options | `--tags`, `--coverpkg`, `--integration`, `--test-cpu` | build tags | none | `-tags`, `-coverpkg`, `-integration`, `-test-cpu` |
| Safety | copies the module (details not documented) | rewrites the original file during the run | sandbox copy, env allowlist, no absolute paths in errors, never touches the tree | keep the lead; add a kill -9 test and fuzzing |
| Accuracy | syntax only | syntax only | syntax only; `+` on strings gives not-viable mutants | type information (`go/types`), no not-viable noise |
| Dependencies | third party | third party | stdlib only | stay stdlib only unless the user agrees |
| Maturity | 0.x, community, releases | fork, no releases listed | v0.1.0, no users yet | releases, docs, stable JSON schema |

Gomu is already ahead on safety, the sandbox plus `-overlay` design, dependency-free, one-file HTML report with a
strict CSP, and dogfooding. It is behind on operator count, selection, ignore mechanisms, diff mode, config, CI
packaging and speed on big modules. Maturity and community cannot be coded; they come from releases, docs and use.

### Ideas that can put Gomu ahead, not just level

1. **Mutant schemata.** Generate one instrumented copy of a file with every mutation guarded by
   `if gomuActive == N`, compile each package once, and pick the mutant with an environment variable. Removes the
   per-mutant compile and link, which dominates the time. Constraints: the rewrite must keep the file compiling
   for every guard, so it needs type information for some operators; code size grows; guard-free fallback to
   `-overlay` stays for operators that cannot be guarded.
2. **Result cache.** Key = hash of the mutated file, the package tests and the dependencies (`go list -deps`).
   A second run on unchanged code costs almost nothing. Stored outside the sandbox, `0o700`, bounded in size.
3. **Run only the tests that cover the mutant.** Per-test coverage tells which tests reach a line; pass them as
   `-run`. Compare the gain against the cost of collecting per-test coverage before committing to it.
4. **Type information without dependencies**: `go list -export` plus `go/importer` and `go/types`. Makes `return`
   type-correct (structs, named types, generics), removes the string-concatenation not-viable mutants and lets
   `deletion` skip calls whose removal cannot compile.
5. **Fewer equivalent mutants**: skip known-equivalent patterns (`x + 0`, `x * 1`, `<` to `<=` against a value that
   is checked by an earlier guard), de-duplicate mutants that print the same, rank survivors so the likely real
   gaps come first.
6. **Say what to fix.** HTML shows the source around each survivor with the mutated line, grouped by file and
   function, with the covering tests when known. No script; `<details>` only.
7. **Stable fingerprints** for mutants (file, function, operator, ordinal, hash of the line) so ignore lists and
   caches survive line shifts, which is where go-mutesting's blacklist breaks.

### Milestones

Each milestone ends with a tag and a README update; keep dogfooding at or above 99%.

**v0.2.0, parity.** Nothing a competitor can do that Gomu cannot.
- Operators from both lists that Gomu lacks: loop control (`break`/`continue`), number increment/decrement
  (`++`/`--`, `+= `/`-=`), bitwise, assignment inversion, invert negatives, branch emptying (`if`/`else`/`case`),
  `&&`/`||` term removal, self-assignment removal, constants (`0`/`1`, `true`/`false`), removing `!`.
- `-operators` and `-exclude-operators`; `-list-operators`; a registry name lookup in `cmd/gomu`.
- `-diff <git ref>` (mutate only changed lines), `-exclude <glob>`, `-dry-run`.
- Ignore annotations (`//gomu:ignore`, `//gomu:ignore-func`, `//gomu:ignore-file`) with an optional operator list.
- Separate thresholds `-threshold-efficacy` and `-threshold-coverage` with distinct exit codes (10 and 11 are taken
  by gremlins; reusing them eases migration).
- `-tags`, `-coverpkg`, `-integration` (whole-module tests), `-test-cpu`.
- Cross-file parallelism with one global cap on concurrent `go test` runs.
- CI: GitHub Actions workflow (vet, race tests, staticcheck, gosec, govulncheck, dogfood with `-threshold`),
  goreleaser binaries, Docker image, an example workflow for users.

**v0.3.0, fast.** Faster than both on the same module and operators.
- Benchmarks (`testing.B`) for site discovery, apply and overlay write, which `CLAUDE.md` asks for and are missing.
- Precompile once per package, then mutant schemata (idea 1) or a precompiled test binary per mutant.
- Result cache (idea 2) and `-incremental`.
- Per-test selection (idea 3) if measurement shows a win.
- Process-group kill on timeout.
- Publish numbers: see "Proving it".

**v0.4.0, smart.** Fewer false alarms and more useful output.
- `go/types` through export data (idea 4); type-correct `return`; drop the string-concatenation limit.
- Equivalent-mutant reduction, de-duplication and ranking (idea 5); stable fingerprints (idea 7).
- A config file (`.gomu.json`) that mirrors the flags; flags win.
- Reports: SARIF (GitHub code scanning), JUnit XML, GitHub Actions annotations, richer HTML (idea 6).

**v1.0.0, trusted.**
- JSON report gets a `schemaVersion` and a written compatibility promise; flags and exit codes frozen.
- Windows and Linux CI (the sandbox tests that need symlinks or `sh` are skipped on Windows today).
- Documentation site or `docs/`: quick start, every flag, every operator with an example, CI recipes, migration
  guides from gremlins and go-mutesting.
- Benchmarks and agreement report published; at least a few real modules run end to end.
- `go.work` and `replace` outside the module supported or clearly rejected with a helpful message.

### Proving it

Do not claim "better" without this. Put the method and results in `docs/benchmarks.md`.

- Pick three open source Go modules: small (a few thousand lines), medium, and large (the ones gremlins says take
  hours). Pin commits.
- Run Gomu, gremlins and go-mutesting with the same operators enabled and the same machine. Record wall time,
  peak memory, disk used, mutants per second, number of mutants, killed/survived counts.
- **Agreement:** for mutants that all tools generate, compare the verdicts and explain every disagreement.
- **Signal quality:** audit 50 survivors per tool by hand; report how many are equivalent or noise.
- **Safety:** `kill -9` a run halfway; check that the source tree is byte for byte unchanged and no temp files are
  left. Run on a module whose tests delete files. go-mutesting rewrites originals, so expect it to differ.
- Repeat after each milestone so regressions show.

## Backlog

The milestones above replace the old flat backlog. Items that are not in a milestone yet:

- Per-file breakdown and ANSI colours in the text report.
- `go.work` and `replace` outside the module in the sandbox copy.
- A "why did this survive" explanation mode.

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
