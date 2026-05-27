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
| `go run .` | quickest iteration inside the repo (prints the bare base version, e.g. `0.1.0`) |
| `make build` | local binary `./ase` (gitignored), stamped with a build timestamp |
| `make install` | install to `~/go/bin`, stamped — then run `ase` from any project directory |

`make build`/`make install` inject a UTC build timestamp as semver build metadata so every
build is distinguishable (see [Versioning](#versioning)). Plain `go build -o ase .` /
`go install .` also work but print the bare base version without a timestamp.

`ase` resolves the session from the **current** directory, so run the installed binary from
the project whose log you want — not from this repo. The installed binary is a snapshot, not
a live link: re-run `make install` after each change you want reflected in the global `ase`.

## Tests

- Run: `go test ./...`
- Tests sit beside the code as `*_test.go`; fixtures live in `testdata/` (e.g.
  `internal/parse/testdata/sample.jsonl`). Add a fixture session there to cover new parsing
  cases.

## Auto build-test-install hook (optional, per-developer)

A local [`.claude/settings.local.json`](.claude/settings.local.json) `Stop` hook can run
`go build ./... && go test ./...` after each change and `make install` on success — blocking
and reporting the failure otherwise. It is gitignored (personal, not shared). After creating
or editing it, open `/hooks` once or restart so Claude Code reloads the config.

## Versioning

The base version is canonical in `main.go` (`var Version`, currently `0.1.0`). `make build`
and `make install` append a UTC build timestamp as the semver build-metadata segment, e.g.
`0.1.0+20260527T131005Z`, so every local build is distinct — useful for confirming a rebuild
took effect. Plain `go build`/`go install`/`go run` (no make) print the bare base `0.1.0`.

Release builds set the full version from the git tag via `-ldflags "-X main.Version=<v>"`
(GoReleaser does this on tag).

## Where things are documented

- Claude Code log format (files, folders, JSONL): [docs/session-format.md](docs/session-format.md)
- Code/runtime traps: [docs/implementation-gotchas.md](docs/implementation-gotchas.md)
- Build / test / install / local-tooling gotchas: capture them here.
