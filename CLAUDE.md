# Gomu

Gomu = **Go** + **mu**tation testing. Also nods to Luffy's Gomu Gomu no Mi: the tool stretches your code
into mutants and checks whether your tests are tough enough to bounce them back.

Gomu is a production-grade mutation testing CLI for Go. It mutates source code, runs the tests, and reports
which mutants survived (tests too weak) or were killed (tests caught the change).

Inspiration (study the ideas, do not copy code or licenses blindly):

- <https://github.com/avito-tech/go-mutesting> (AST mutators, per-mutant test run)
- <https://github.com/go-gremlins/gremlins> (coverage-guided, parallel workers, CI-friendly thresholds)

## Working rules for Claude

- **One file at a time.** Never scaffold or rewrite the whole repo in one go. Do the single file asked, then stop.
- Write the test file together with (or before) the code file it covers. Prefer TDD.
- Do not add features, flags, config, or abstractions that are not requested (YAGNI).
- Ask before adding a third-party dependency. Default to the standard library.
- Reply in terse Thai per the user's global rules; code, comments, commits, and docs are in English.

## Principles

- **KISS / YAGNI**: smallest design that works. No plugin system, no DI container, no generics for their own sake.
- **Go idiom**: small interfaces defined at the consumer side, accept interfaces and return structs, errors are
  values (wrap with `%w`, no panics for flow control), `context.Context` first param, zero values useful,
  no package-level mutable state, no `init()` side effects, no stutter (`mutant.Mutant` -> fine, `mutant.MutantService` -> no).
- **Clean architecture** (onion / hexagonal / screaming):
  - Package names scream the domain (`mutant`, `mutator`, `runner`, `report`), not technology (`utils`, `helpers`, `models`, `controllers`).
  - Dependencies point inward only: `adapter -> app -> domain`. Domain imports nothing but the standard library.
  - Ports (interfaces) live in the inner layer that needs them; adapters implement them.
- **DCI** (Data, Context, Interaction):
  - **Data**: plain domain types (`Mutant`, `Result`, `Status`). They know their own state only, nothing about use cases.
  - **Context**: one type per use case (`Discover`, `Generate`, `Execute`, `Report`) in `app/`. It holds the
    collaborators, assigns them to roles, and runs the scenario.
  - **Interaction**: behavior lives in roles, expressed as small consumer-side interfaces declared in the
    context's file (e.g. `Finder`, `Applier`, `TestRunner`). Objects play a role by satisfying the interface;
    no inheritance, no god service.
  - Roles and contexts are readable top to bottom as the use-case story.
- **Open/Closed**: adding a mutator or a reporter = adding a new file and registering it, never editing existing logic.

## Layout (target, create files only when asked)

```text
cmd/gomu/            main.go: wiring only, parse flags, build adapters, call app. No logic.
internal/
  mutant/            domain: Mutant, Status (Killed, Survived, TimedOut, NotCovered), Result, score. Pure.
  mutator/           domain: Mutator interface + one file per operator (arithmetic, conditional, logical, ...).
  app/               DCI contexts: Discover, Generate, Execute, Report. Role interfaces defined here.
  adapter/
    goast/           go/parser + go/ast + go/printer: find mutation sites, apply a mutation.
    gotest/          run `go test` for a mutant (exec adapter).
    cover/           parse coverage profile -> covered lines.
    fsys/            safe file access, overlay writer.
    report/          text, json (more only on request).
```

## Performance

- Never copy the whole module per mutant. Use `go test -overlay overlay.json` so the original tree is untouched
  and only one changed file is swapped.
- Worker pool sized by `runtime.GOMAXPROCS` (flag to override); each worker uses its own overlay file and `-count=1`.
- Coverage-guided: run baseline `go test -coverprofile` once; skip mutants on uncovered lines (`NotCovered`).
- Baseline must pass first; derive per-mutant timeout from baseline duration times a factor, `context.WithTimeout`.
- Run only the tests of the package that owns the mutated file; use `-failfast` so the first killing test ends the run.
- Parse each file once, mutate AST nodes in place and revert (or reprint), avoid reparsing per mutant.
- Pre-allocate slices when size is known, stream results over channels, no global locks on the hot path.
- Add benchmarks (`testing.B`) for hot paths: mutation site discovery, apply, overlay write.

## Security (secure coding)

- Run external commands with `exec.CommandContext`, fixed binary (`go`), argument slice, **never** `sh -c` or string concat.
- Validate and clean every path from CLI/config: `filepath.Clean`, must stay inside the module root
  (reject `..` escape and symlink escape via `filepath.EvalSymlinks`).
- Never write to or modify the user's source files. Mutants live only in overlay/temp files created with
  `os.CreateTemp` / `os.MkdirTemp`, mode `0o600` / `0o700`, always removed (`defer`, also on signal via `signal.NotifyContext`).
- Mutated code is untrusted-ish: every run has timeout and context cancellation; cap captured output size
  (`io.LimitReader`) to avoid memory exhaustion; no unbounded goroutines.
- Pass a minimal environment to child processes (explicit allowlist: `PATH`, `HOME`, `GOCACHE`, `GOFLAGS`, ...), do not leak secrets.
- Do not log source contents or env values. Reports contain file, line, operator only.
- No `unsafe`, no `reflect` tricks, no `math/rand` for anything security-related, no network calls.
- Errors returned to the user must not expose internal absolute paths beyond what is needed.
- CI gates: `go vet`, `staticcheck`, `gosec`, `govulncheck`, `go test -race`.

## Testing (production grade)

- Table-driven tests with `t.Run`, `t.Parallel()` where safe, `t.TempDir()` for files, `t.Helper()` in helpers.
- Test behavior through the public API; use fakes written by hand for ports, no mocking framework.
- Every mutator: tests for each mutation it produces, for sites it must NOT touch, and for output that still compiles.
- Adapters: integration tests against small fixture modules under `testdata/`.
- Cover error paths, context cancellation, timeouts, path traversal attempts, empty input, malformed Go source.
- Fuzz (`testing.F`) the AST site discovery and path validation.
- Target >= 90% coverage on `mutant`, `mutator`, `app`; run `go test -race -cover ./...`.
- **Dogfood**: Gomu must eventually run on itself with a high mutation score.

## Definition of done (per file)

1. `gofmt` / `goimports` clean, `go vet` clean.
2. Test file exists and passes with `-race`.
3. Exported identifiers have doc comments starting with the identifier name.
4. No dependency pointing outward across layers.
5. Stop and report. Do not continue to the next file unless asked.
