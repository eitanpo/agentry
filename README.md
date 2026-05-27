# ase

**Agent Session Explorer** — render a Claude Code session log into a styled terminal view.

See [PRODUCT.md](PRODUCT.md) for scope and design rationale.

> Status: in development. Not yet released.

## Install

```
brew install eitanpo/tap/ase
```

Available once the first release is tagged.

## Usage

Run `ase` from the directory you ran Claude Code in:

```
ase            # the most recent completed session in this directory's project
ase <uuid>     # a specific session, by full id
```

`ase` finds the session by mapping the current directory to its Claude project folder under `~/.claude/projects/`.

### Options

| Flag | Default | Description |
|---|---|---|
| `--level minimal\|standard\|detailed\|full` | `detailed` | How much of each turn to show. |
| `--no-color` | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | — | — |

JSON output, markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap.

## License

MIT — see [LICENSE](LICENSE).
