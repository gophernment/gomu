# Gomu

**Go** + **mu**tation testing. Named after Luffy's Gomu Gomu no Mi: Gomu stretches your code into
mutants and checks whether your tests are tough enough to bounce them back.

## What is mutation testing?

Line coverage tells you a test *ran* some code, not that it would *notice* a bug. A mutation tester makes
small changes (mutants) to your code, such as `+` to `-` or `<` to `<=`, then runs your tests on each one.

| Result        | Meaning                                              |
| ------------- | ---------------------------------------------------- |
| `killed`      | a test failed, so your tests caught the change       |
| `survived`    | all tests passed, so your tests missed the change    |
| `timed out`   | tests hung or looped, counted as caught              |
| `not covered` | no test runs that line, not scored                   |
| `not viable`  | the mutant does not compile, not scored              |

Score = (killed + timed out) / (killed + timed out + survived).

## Usage

```sh
go install github.com/pallat/gomu/cmd/gomu@latest

gomu                          # every non-test file of the module in the current directory
gomu internal/...             # a subtree
gomu pkg/a.go pkg/b.go        # single files
gomu -threshold 80 ./...      # exit 1 below 80%
gomu -v -workers 4 ./...      # list every mutant, 4 parallel test runs per file
gomu -format json ./... > gomu.json   # machine-readable report for CI
gomu -format html ./... > gomu.html   # one-page report to open in a browser
```

```text
SURVIVED    internal/app/analyze.go:130:19  logical      && -> ||

Mutation score: 91.1% (245 of 269 evaluated mutants detected)
Killed 241 | Survived 24 | Timed out 4 | Not covered 13 | Not viable 3
```

| Flag         | Default | Meaning                                                           |
| ------------ | ------- | ----------------------------------------------------------------- |
| `-dir`       | `.`     | module directory (must contain `go.mod`)                          |
| `-threshold` | `0`     | minimum score in percent; `0` disables. Fails if nothing was evaluated |
| `-workers`   | `0`     | parallel test runs per file; `0` means `GOMAXPROCS`               |
| `-v`         | off     | list every mutant instead of only the survivors                   |
| `-format`    | `text`  | report on stdout: `text`, `json` or `html`                        |

With `-format json`, stdout is one JSON document and nothing else (notes and errors go to stderr). `score` is
`null` when no mutant was evaluated; `mutants` is always an array and lists the survivors, or every mutant with `-v`:

```json
{
  "score": 91.1,
  "evaluated": 269,
  "summary": {"killed": 241, "survived": 24, "timedOut": 4, "notCovered": 13, "notViable": 3, "pending": 0},
  "mutants": [
    {"file": "internal/app/analyze.go", "line": 130, "column": 19, "operator": "logical",
     "original": "&&", "replacement": "||", "status": "survived"}
  ]
}
```

With `-format html`, stdout is one self-contained page: inline style, no script, no external resource, and a
Content-Security-Policy that forbids everything else. It shows the score, the totals, and the mutants grouped by
file. Open it in a browser or keep it as a CI artifact.

Exit codes: `0` ok, `1` score below `-threshold`, `2` error (bad flag, failing baseline tests, ...).

Mutation operators: `arithmetic` (`+ - * / %`), `conditional` (`< <= > >= == !=`), `logical` (`&& ||`).
Only code inside function bodies is mutated. Test files, `vendor`, `testdata`, hidden directories and
generated files are skipped.

## How it works, and why it is safe

1. The module is **copied to a private temp directory** (without `.git`; symbolic links are not copied).
   Everything below runs in the copy. A mutant can make code do anything, including deleting files, and it can
   only ever damage the copy. Your working tree is never written to. (Gomu once deleted one of its own packages
   before it worked this way; there is a regression test for it.)
2. The unmodified tests run once. If they fail, Gomu stops: mutating code with failing tests proves nothing.
   The coverage profile of this run lets Gomu skip mutants on lines no test executes, and its duration sets the
   per-mutant timeout (3x, at least 10s).
3. Each mutant is written to a temp file and compiled with `go test -overlay`, so nothing is copied per mutant.
   Workers run in parallel, each with its own parsed copy of the file and its own overlay. Only the tests of the
   mutated file's package run, with `-failfast`, and `go vet` is off so vet findings never count as kills.
4. `go` runs without a shell and with an allowlisted environment, so secrets in your environment do not reach
   the code under test. Output is scanned, not stored, so a noisy test cannot exhaust memory.

Known limits:

- The module root must contain `go.mod`. `replace` directives that point outside the module, `go.work`, and
  tests that need symbolic links will not work in the copy.
- A module larger than 200,000 files or 2 GiB is refused.
- Without type information, `+` on two non-literal strings produces a mutant that fails to compile; it is
  reported as `not viable` and does not affect the score.
- Files are tested one after another; only mutants within a file run in parallel.
- Mutants are not de-duplicated or ranked, and there is no incremental mode yet.

## Architecture

Clean architecture with DCI (Data, Context, Interaction). Dependencies point inward only:
`cmd -> adapter -> app -> domain`.

```text
cmd/gomu/          flags, wiring, exit codes
internal/
  mutant/          domain: Mutant, Status, Summary, score
  mutator/         domain: Mutator role, Mutation, Registry, operators
  app/             DCI contexts: Discover, Execute, Analyze (roles are small interfaces)
  adapter/
    goast/         safe parse, mutation sites in function bodies
    fsys/          sandbox (module copy) and overlay.json writer
    cover/         coverage profile parser
    gotest/        go test runner
    report/        text, JSON and HTML reports
```

Adding a mutation operator means adding one file that implements `mutator.Mutator` and registering it in
`cmd/gomu`; existing code does not change.

## Development

Requires the Go version in [`go.mod`](go.mod). Standard library only, no dependencies.

```sh
go vet ./...
go test -race -cover ./...
go run ./cmd/gomu ./...      # Gomu on itself
```

Project conventions for contributors and AI assistants are in [`CLAUDE.md`](CLAUDE.md).

## License

[Apache-2.0](LICENSE).
