# Gomu

**Go** + **mu**tation testing. Named after Luffy's Gomu Gomu no Mi: Gomu stretches your code into
mutants and checks whether your tests are tough enough to bounce them back.

> **Status: early development.** The domain model is in place; the CLI, AST adapter, and test runner are not
> written yet. The usage below is the planned design, not a working command.

## What is mutation testing?

Line coverage tells you a test *ran* some code, not that it would *notice* a bug. A mutation tester makes
small changes (mutants) to your code, such as `+` to `-` or `<` to `<=`, then runs your tests on each one.

| Result       | Meaning                                              |
| ------------ | ---------------------------------------------------- |
| `killed`     | a test failed, so your tests caught the change       |
| `survived`   | all tests passed, so your tests missed the change    |
| `timed out`  | tests hung or looped, counted as caught              |
| `not covered`| no test runs that line, not scored                   |

Score = (killed + timed out) / (killed + timed out + survived).

## Planned usage

```sh
go install github.com/pallat/gomu/cmd/gomu@latest
gomu ./...
```

Planned design goals:

- **Fast**: uses `go test -overlay`, so the source tree is never copied; mutants run in parallel;
  mutants on uncovered lines are skipped using a coverage profile.
- **Safe**: never modifies your source files, runs `go` without a shell, applies timeouts and output limits,
  keeps path access inside the module root.
- **Simple**: standard library only, one binary, no config required.

Inspired by [go-mutesting](https://github.com/avito-tech/go-mutesting) and
[gremlins](https://github.com/go-gremlins/gremlins).

## Architecture

Clean architecture with DCI (Data, Context, Interaction). Dependencies point inward only:
`adapter -> app -> domain`.

```text
cmd/gomu/          wiring only
internal/
  mutant/          domain: Mutant, Status, Summary, score          (done)
  mutator/         domain: Mutator role, Mutation, Registry,       (in progress)
                   operators: arithmetic, conditional, logical
  app/             DCI contexts: Discover, Generate, Execute, Report
  adapter/         goast, gotest, cover, fsys, report
```

Adding a mutation operator means adding one file that implements `mutator.Mutator` and registering it;
existing code does not change.

## Development

Requires the Go version in [`go.mod`](go.mod).

```sh
go vet ./...
go test -race -cover ./...
```

Project conventions for contributors and AI assistants are in [`CLAUDE.md`](CLAUDE.md).

## License

[Apache-2.0](LICENSE).
