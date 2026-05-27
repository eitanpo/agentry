# agentry

**Agent Replay** (**agent** + re**play**) — render a Claude Code session log into a styled terminal view.

See [PRODUCT.md](PRODUCT.md) for scope and design rationale.

> Status: in development. Not yet released.

## Install

macOS (Homebrew cask):

```
brew install eitanpo/tap/agentry
```

Linux: `go install github.com/eitanpo/agentry@latest`, or download a binary from the [releases](https://github.com/eitanpo/agentry/releases).

Available once the first release is tagged.

## Usage

Run `agentry` from the directory you ran Claude Code in:

```
agentry            # the most recent session (by time) in this directory's project
agentry <uuid>     # a specific session, by full id
```

`agentry` finds the session by mapping the current directory to its Claude project folder under `~/.claude/projects/`.

### Options

| Flag | Default | Description |
|---|---|---|
| `--level minimal\|standard\|detailed\|full` | `minimal` | How much of each turn to show. |
| `--no-color` | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | — | — |

JSON output, markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap. Build, test, and install workflow: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT — see [LICENSE](LICENSE).
