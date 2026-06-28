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
agentry                # the most recent session (by time) in this directory's project
agentry <uuid>         # a specific session, by full id
agentry view <uuid>    # same as above; view is the explicit render verb
```

`agentry` finds the session by mapping the current directory to its Claude project folder under `~/.claude/projects/`. The first token is a verb (`view`, `list`) when it names one, otherwise a session id — they can't collide, since ids are hex and verbs are words. Flags may go before or after operands, and a mistyped verb, flag, or value is met with a "did you mean" suggestion rather than full help.

To find a session, list them:

```
agentry list                              # the 10 most recent sessions in this project
agentry list --limit 25                   # the 25 most recent
agentry list --since today                # everything from today
agentry list --since 7d                   # the last 7 days
agentry list --since 2026-06-01 --until 2026-06-03
agentry list --include prompts            # list each session's prompts beneath its row
agentry list --include tools              # break down each session's tool calls by command/skill/agent
agentry list --used-command exa           # only sessions that ran a Bash command matching "exa"
agentry list --used-skill expert          # only sessions that invoked the expert skill
agentry list --used researcher            # skill, agent, or command matching "researcher"
agentry list --used-skill expert --format json | jq   # machine-readable, for piping
```

Sessions print oldest-to-newest, so the most recent is at the bottom, next to your prompt. Each row shows the start time, duration, turn count, a title (the manual `custom-title` from renaming the session if set, else Claude Code's own `ai-title` summary, falling back to the first prompt, skipping a leading `/clear`), and the full id — copy an id and pass it to `agentry <id>` to render that session. A forked session (Claude Code's `--fork-session` / `/branch`) is grouped under the original it was forked from and its title indented with `└─`; while it still carries the original's inherited title it is shown by its first new prompt instead, so the two are distinguishable.

### Options

| Flag | Mode | Default | Description |
|---|---|---|---|
| `--level minimal\|standard\|detailed\|full` | render | `minimal` | Preset of channel defaults. `minimal` prompts+response; `standard` +thinking+metrics; `detailed` +tools+subagents (no output); `full` +tool-results. |
| `--[no-]thinking\|tools\|tool-results\|subagents\|metrics` | render | — | Override a single channel on top of `--level` (adds or subtracts). `tools` = a tool fired; `tool-results` = its output. |
| `--limit N` | `list` | `10` | Cap to N most-recent (`0` = no cap; lifted when a time filter is set). |
| `--since WHEN`, `--until WHEN` | `list` | — | Filter by last-activity time. WHEN: `today`/`yesterday`, `Nh`/`Nd`/`Nw`, or `YYYY-MM-DD`. |
| `--include CHANNELS` | `list` | — | Add per-session detail. Comma-separated; channels: `prompts`, `tools` (or `all`). `tools` breaks down a session's top-level tool calls grouped by identity — Bash by program, Skill by name, Agent by subagent type, everything else by tool name. |
| `--used-tool NAME` | `list` | — | Only sessions where that tool fired, by tool-use name (case-insensitive, exact). The "which mechanism" axis. |
| `--used-skill`, `--used-agent`, `--used-command` | `list` | — | Identity axis: a Skill's skill, an Agent's subagent type, a Bash command's text (case-insensitive substring). |
| `--used TOKEN` | `list` | — | Catch-all over the identity axis: skill name, agent type, or command. Not tool names — use `--used-tool` for those. |
| `--format json\|text` | `list` | `text` | `json` emits the selected sessions as a JSON array (full model per session) for piping; ignores `--include` and color. |
| `--no-color` | global | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | global | — | Per-verb `--help` lists only that mode's flags. |

"render" flags apply to the bare command and `view`; "`list`" flags apply to `agentry list`; "global" flags work anywhere.

JSON output, markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap. Build, test, and install workflow: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT — see [LICENSE](LICENSE).
