# Development

Contributor guide for building, testing, and installing `agentry` from source. For what `agentry`
does see [PRODUCT.md](PRODUCT.md); for install-via-brew and usage see [README.md](README.md).

## Prerequisites

- Go — version in the `go` directive of [go.mod](go.mod).
- For `go install` to make `agentry` runnable everywhere, `$(go env GOPATH)/bin` (usually
  `~/go/bin`) must be on your `PATH`.

## Build, run, install

| Command | Use |
|---|---|
| `go run .` | quickest iteration inside the repo (prints the bare base version, e.g. `0.1.0`) |
| `make` / `make build` | compile-check the whole module **and** install to `~/go/bin`, stamped — the default goal, so the global `agentry` you run from other projects always reflects your latest work |
| `make install` | install only, skipping the whole-module compile-check — the install step `make release` reuses |

The global binary is the only one you invoke — `agentry` resolves the session from the
**current** directory, so you run the installed binary from the project whose log you want, not
from this repo. So any change you want to *run* must be installed; bare `make` (or `make build`)
does that as the second half of building, which is why building no longer produces a throwaway
local artifact.

`make build`/`make install` stamp the binary with a UTC build timestamp as semver build metadata,
plus a `.dirty` suffix when the working tree has uncommitted changes — so `agentry --version`
both distinguishes each rebuild and flags an unreleased dev build (see [Versioning](#versioning)).
Plain `go build`/`go install` (no make) print the bare base version.

The installed binary is a snapshot, not a live link: re-run `make` after each change you want
reflected in the global `agentry`.

## Tests

- Run: `go test ./...`
- Tests sit beside the code as `*_test.go`; fixtures live in `testdata/` (e.g.
  `internal/parse/testdata/sample.jsonl`). Add a fixture session there to cover new parsing
  cases.
- A render test that asserts on color must `lipgloss.SetColorProfile(termenv.ANSI256)` first.
  Test stdout is not a TTY, so lipgloss auto-detects "no color" and strips all lipgloss styling
  (backgrounds, foregrounds) even when `Options.Color` is true — glamour keeps its own colors,
  which makes the stripping easy to miss.

## Auto build-test-install hook (optional, per-developer)

A local [`.claude/settings.local.json`](.claude/settings.local.json) `Stop` hook can run
`go build ./... && go test ./...` after each change and `make install` on success — blocking
and reporting the failure otherwise. It is gitignored (personal, not shared). After creating
or editing it, open `/hooks` once or restart so Claude Code reloads the config.

## Versioning

The base version is canonical in `main.go` (`var Version`), and holds the **last published**
release. `make build` and `make install` append a UTC build timestamp as the semver
build-metadata segment, plus a `.dirty` suffix when the working tree has uncommitted changes —
e.g. `0.5.0+20260527T131005Z` when clean, `0.5.0+20260527T131005Z.dirty` with local edits — so
every local build is distinct (confirming a rebuild took effect) and an unreleased dev build is
obvious. Plain `go build`/`go install`/`go run` (no make) print the bare base version.

Release builds set the full version from the git tag via `-ldflags "-X main.Version=<v>"`
(GoReleaser does this on tag).

Increment policy (pre-1.0): bump MINOR (`0.x.0`) for a backward-compatible feature or
behavior addition, PATCH (`0.x.y`) for bug fixes and refactors; MAJOR is reserved for the
1.0 stabilization. `var Version` changes **only when releasing** (step 1 below), never in a
feature commit — the bump covers everything accumulated since the last tag.

## Releasing

Releases are cut locally with [GoReleaser](https://goreleaser.com) (`brew install goreleaser`),
driven by [`.goreleaser.yaml`](.goreleaser.yaml). It builds the binaries, creates the GitHub
release, and pushes the Homebrew **cask** to the `eitanpo/homebrew-tap` repo (which must exist).

1. Set `var Version` in `main.go` to the release version (drop the `v`, e.g. `0.1.0`) so non-tag
   builds report it; commit.
2. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`. GoReleaser derives the version from
   the tag and injects it via `-ldflags`.
3. Dry-run first: `make release-dry` (builds all targets locally, publishes nothing, installs nothing).
4. Publish: `make release`. This runs `goreleaser release --clean` (sourcing both tokens from
   `gh auth token`, since the same account owns both repos) and then `make install`, so this
   machine ends up running the version just shipped. Refreshing the local install is part of the
   release target, not a separate step to remember.

The cask lands at `Casks/agentry.rb` in the tap. macOS binaries are unsigned, so the cask's
post-install hook strips the quarantine attribute. Linux has no cask — `go install` instead.

The `--snapshot` cask under `dist/homebrew/` is **not** representative: its `version` is
`<last-tag>-SNAPSHOT-<sha>` and its download URLs are pinned to the previous tag. Only the
real `release --clean` regenerates the cask with the correct `version` and `v#{version}`
URLs. Verify the published cask in the tap (read `Casks/agentry.rb` back from
`eitanpo/homebrew-tap`), not the snapshot artifact.

## Where things are documented

- Claude Code log format (files, folders, JSONL): [docs/session-format.md](docs/session-format.md)
- Code/runtime traps: [docs/implementation-gotchas.md](docs/implementation-gotchas.md)
- Build / test / install / local-tooling gotchas: capture them here.
