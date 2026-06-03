# agentry

**AGENT ReplaY**  — render a Claude Code session log into a styled terminal view.

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

To find a session, list them:

```
agentry --list                              # the 10 most recent sessions in this project
agentry --list --limit 25                   # the 25 most recent
agentry --list --since today                # everything from today
agentry --list --since 7d                   # the last 7 days
agentry --list --since 2026-06-01 --until 2026-06-03
agentry --list --include prompts            # list each session's prompts beneath its row
```

Sessions print oldest-to-newest, so the most recent is at the bottom, next to your prompt. Each row shows the start time, duration, turn count, a title (Claude Code's own `ai-title` summary, falling back to the first prompt, skipping a leading `/clear`), and the full id — copy an id and pass it to `agentry <id>` to render that session.

### Options

| Flag | Default | Description |
|---|---|---|
| `--level minimal\|standard\|detailed\|full` | `minimal` | How much of each turn to show. |
| `--list` | — | List this project's sessions instead of rendering one. |
| `--limit N` | `10` | With `--list`, cap to N most-recent (`0` = no cap; lifted when a time filter is set). |
| `--since WHEN`, `--until WHEN` | — | With `--list`, filter by last-activity time. WHEN: `today`/`yesterday`, `Nh`/`Nd`/`Nw`, or `YYYY-MM-DD`. |
| `--include CHANNELS` | — | With `--list`, add per-session detail. Comma-separated; channels: `prompts` (or `all`). |
| `--no-color` | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | — | — |

JSON output, markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap. Build, test, and install workflow: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT — see [LICENSE](LICENSE).
