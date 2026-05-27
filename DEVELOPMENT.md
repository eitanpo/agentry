# Development

Contributor guide for building, testing, and installing `ase` from source. For what `ase`
does see [PRODUCT.md](PRODUCT.md); for install-via-brew and usage see [README.md](README.md).

## Prerequisites

- Go — version in the `go` directive of [go.mod](go.mod).
- For `go install` to make `ase` runnable everywhere, `$(go env GOPATH)/bin` (usually
  `~/go/bin`) must be on your `PATH`.

## Build, run, install

| Command | Use |
|---|---|
| `go run .` | quickest iteration inside the repo |
| `go build -o ase .` | local binary in the repo (gitignored) |
| `go install .` | install to `~/go/bin`; then run `ase` from any project directory |

`ase` resolves the session from the **current** directory, so run the installed binary from
the project whose log you want — not from this repo. The installed binary is a snapshot, not
a live link: re-run `go install .` after each change you want reflected in the global `ase`.

## Tests

- Run: `go test ./...`
- Tests sit beside the code as `*_test.go`; fixtures live in `testdata/` (e.g.
  `internal/parse/testdata/sample.jsonl`). Add a fixture session there to cover new parsing
  cases.

## Auto build-test-install hook (optional, per-developer)

A local [`.claude/settings.local.json`](.claude/settings.local.json) `Stop` hook can run
`go build ./... && go test ./...` after each change and `go install .` on success — blocking
and reporting the failure otherwise. It is gitignored (personal, not shared). After creating
or editing it, open `/hooks` once or restart so Claude Code reloads the config.

## Versioning

`ase --version` prints `dev` for source builds. Release builds set it via
`-ldflags "-X main.Version=<v>"` (GoReleaser does this on tag).

## Where things are documented

- Claude Code log format (files, folders, JSONL): [docs/session-format.md](docs/session-format.md)
- Code/runtime traps: [docs/implementation-gotchas.md](docs/implementation-gotchas.md)
- Build / test / install / local-tooling gotchas: capture them here.
