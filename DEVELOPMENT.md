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
| `make build` | local binary `./agentry` (gitignored), stamped with a build timestamp |
| `make install` | install to `~/go/bin`, stamped — then run `agentry` from any project directory |

`make build`/`make install` inject a UTC build timestamp as semver build metadata so every
build is distinguishable (see [Versioning](#versioning)). Plain `go build -o agentry .` /
`go install .` also work but print the bare base version without a timestamp.

`agentry` resolves the session from the **current** directory, so run the installed binary from
the project whose log you want — not from this repo. The installed binary is a snapshot, not
a live link: re-run `make install` after each change you want reflected in the global `agentry`.

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

The base version is canonical in `main.go` (`var Version`, currently `0.2.0`). `make build`
and `make install` append a UTC build timestamp as the semver build-metadata segment, e.g.
`0.2.0+20260527T131005Z`, so every local build is distinct — useful for confirming a rebuild
took effect. Plain `go build`/`go install`/`go run` (no make) print the bare base `0.2.0`.

Release builds set the full version from the git tag via `-ldflags "-X main.Version=<v>"`
(GoReleaser does this on tag).

Increment policy (pre-1.0): bump MINOR (`0.x.0`) for a backward-compatible feature or
behavior addition, PATCH (`0.x.y`) for bug fixes and refactors; MAJOR is reserved for the
1.0 stabilization. Bump `var Version` in the same change that adds the surface.

## Releasing

Releases are cut locally with [GoReleaser](https://goreleaser.com) (`brew install goreleaser`),
driven by [`.goreleaser.yaml`](.goreleaser.yaml). It builds the binaries, creates the GitHub
release, and pushes the Homebrew **cask** to the `eitanpo/homebrew-tap` repo (which must exist).

1. Set `var Version` in `main.go` to the release version (drop the `v`, e.g. `0.1.0`) so non-tag
   builds report it; commit.
2. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`. GoReleaser derives the version from
   the tag and injects it via `-ldflags`.
3. Dry-run first: `goreleaser release --snapshot --clean` (builds locally, publishes nothing).
4. Publish — both tokens can be the gh token, since the same account owns both repos:

   ```
   export GITHUB_TOKEN=$(gh auth token)
   export HOMEBREW_TAP_GITHUB_TOKEN=$GITHUB_TOKEN
   goreleaser release --clean
   ```

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
