# agentry

**AGENT ReplaY**  — render a Claude Code session log into a styled terminal view.

See [PRODUCT.md](PRODUCT.md) for scope and design rationale.

## Install

macOS (Homebrew cask):

```
brew install eitanpo/tap/agentry
```

Linux: `go install github.com/eitanpo/agentry@latest`, or download a binary from the [releases](https://github.com/eitanpo/agentry/releases).

### Shell completion

The Homebrew cask installs tab-completion automatically — nothing to do. For a `go install` or a downloaded binary, generate and load the script for your shell:

```
source <(agentry completion zsh)     # add to ~/.zshrc
source <(agentry completion bash)    # add to ~/.bashrc
agentry completion fish | source     # or: > ~/.config/fish/completions/agentry.fish
```

Completion covers the verbs and flags, the enum values of `--format`/`--level`, and — the useful one — the current project's **session ids**, each shown with its title, so you tab a UUID instead of pasting it.

## Usage

Run `agentry` from the directory you ran Claude Code in:

```
agentry                     # list this project's sessions (see below)
agentry <uuid>              # render a specific session, by full id
agentry view                # render the most recent session (no id needed)
agentry view --from sdk     # render the most recent headless run (a hook, `claude -p`)
agentry <uuid> --format json | jq  # the full session model as JSON, for piping
```

With no id, `agentry` lists this project's sessions (below); with a full-UUID id it renders that one, mapping the current directory to its Claude project folder under `~/.claude/projects/`. `agentry view` with no id picks the most recent session you actually worked in, skipping headless runs — an id you name is always rendered as asked. `--from` changes which kind it picks: `--from sdk` for the last headless run, `--from all` for the last session of any kind. Asking for a kind this project has none of is an error, not a quiet fall back to another kind. The first token is a verb (`view`, `list`) when it names one, otherwise a session id — they can't collide, since ids are hex and verbs are words. Flags may go before or after operands, and a mistyped verb, flag, or value is met with a "did you mean" suggestion rather than full help.

To find a session, list them — bare `agentry` does this, and `agentry list` is its explicit form:

```
agentry                                   # the 10 most recent sessions (bare command == list)
agentry --since today                     # list flags work on the bare command too
agentry list                              # the explicit form of the bare listing
agentry list --limit 25                   # the 25 most recent
agentry list --since today                # everything from today
agentry list --since 7d                   # the last 7 days
agentry list --since 2026-06-01 --until 2026-06-03
agentry list --include prompts            # list each session's prompts beneath its row
agentry list --include tools              # break down each session's tool calls by command/skill/agent
agentry list --include files              # every file each session modified
agentry list --used-command exa           # only sessions that ran a Bash command matching "exa"
agentry list --used-skill expert          # only sessions that invoked the expert skill
agentry list --used-file PRODUCT.md       # only sessions that modified that file
agentry list --used researcher            # skill, agent, or command matching "researcher"
agentry list --used-command 'git commit' --not-used-skill review   # committed without loading a skill
agentry list --used-skill expert --format json | jq   # machine-readable, for piping
agentry list --all-projects                # every project, not just this directory
agentry list --project ~/Projects/me/app   # that repo and every worktree nested in it
agentry list --project ~/Projects/me       # every repo under that directory
agentry list --from app                    # only sessions started in the desktop app
agentry list --from all                    # include headless runs, hidden by default
```

**Headless sessions are hidden unless you ask for them.** Anything non-interactive — a `claude -p`
from a script, a hook, a CI step — writes a session log like any other, and on a machine that uses
hooks these outnumber the ones you typed. They are excluded by default so a listing shows work you
did; `--from sdk` shows only those, `--from all` shows everything. When a listing spans more than
one kind, each row gains a 3-letter tag: `cli` (terminal), `app` (desktop), `sdk` (headless), and
`cli+` for a session that started in one and was resumed in another.

`--project PATH` lists PATH's sessions **and everything nested under it**. That matters because
Claude Code gives every git worktree its own project folder, so a repo's sessions are split
across them and `agentry list` in the main checkout shows none of the worktree ones — naming the
repo sweeps them back in. Both scope flags also reach projects whose directory you have since
deleted or renamed, which walking directories yourself cannot. When a listing spans more than one
project, each row gains a project column before the title; `--format json` carries the full path
as `cwd` on every session.

Sessions print oldest-to-newest, so the most recent is at the bottom, next to your prompt. Each row shows the last-activity time (when the session's most recent turn ended — the same recency the list is ordered by), duration, turn count, a title (a name you chose if set — from renaming the session, or from `--name` / `/rename`, whichever the log records last — else Claude Code's own `ai-title` summary, falling back to the first prompt, skipping a leading `/clear`), and the full id — copy an id and pass it to `agentry <id>` to render that session. A forked session (Claude Code's `--fork-session` / `/branch`) is grouped under the original it was forked from and its title indented with `└─`; while it still carries the original's inherited title it is shown by its first new prompt instead, so the two are distinguishable.

### Options

| Flag | Mode | Default | Description |
|---|---|---|---|
| `--level minimal\|standard\|detailed\|full` | render | `minimal` | Preset of channel defaults. `minimal` prompts+response; `standard` +thinking+metrics; `detailed` +tools+subagents (no output); `full` +tool-results. |
| `--[no-]thinking\|tools\|tool-results\|subagents\|metrics` | render | — | Override a single channel on top of `--level` (adds or subtracts). `tools` = a tool fired; `tool-results` = its output. An `Agent` line also names what it delegated to: `Agent[Explore@haiku]` is the subagent type and the model, and the `@model` half is absent when the call left the subagent on the session's model. |
| `--limit N` | `list` | `10` | Cap to N most-recent (`0` = no cap; lifted when a time filter is set). |
| `--since WHEN`, `--until WHEN` | `list` | — | Filter by last-activity time. WHEN: `today`/`yesterday`, `Nh`/`Nd`/`Nw`, or `YYYY-MM-DD`. |
| `--include CHANNELS` | `list` | — | Add per-session detail. Comma-separated; channels: `prompts`, `tools`, `files` (or `all`). `tools` breaks down a session's top-level tool calls grouped by identity — Bash by program, Skill by name, Agent by subagent type, Edit/Write by target file, everything else by tool name — and adds a `Denied` line naming the calls that were refused and by what (`permission-rule`, `automode-blocked`, `automode-unavailable`, `user-rejected`), which an error glyph alone cannot tell you. `files` lists every file the session modified by any means, from Claude Code's own file-history record rather than from tool arguments. |
| `--used-tool NAME` | `list` | — | Only sessions where that tool fired, by tool-use name (case-insensitive, exact). The "which mechanism" axis. |
| `--used-skill`, `--used-agent`, `--used-command` | `list` | — | Identity axis: a Skill's skill, an Agent's subagent type, a Bash command's text (case-insensitive substring). |
| `--used-file PATH` | `list` | — | Only sessions that modified a matching file (case-insensitive substring, so `list.go` catches every directory's and `internal/cli/list.go` names one). Reads `Edit`/`Write` targets and the tracked-file record together; the tool targets do nearly all the work, since about half of sessions have no tracked-file record at all. Not covered by `--used`. |
| `--used TOKEN` | `list` | — | Catch-all over the identity axis: skill name, agent type, or command. Not tool names — use `--used-tool` for those. |
| `--not-used-*` | `list` | — | Every `--used*` flag above has a `--not-` twin (`--not-used-tool`, `--not-used-skill`, `--not-used-agent`, `--not-used-command`, `--not-used-file`, `--not-used`) keeping the sessions the positive one drops. Combine the two for a compliance audit: `--used-command 'git commit' --not-used-skill review`. Absence is judged over top-level calls only, so a subagent may have used what the main thread did not. |
| `--all-projects` | `list` | — | Every project under `~/.claude/projects/`, not just this directory's. Mutually exclusive with `--project`. |
| `--project PATH` | `list` | — | PATH's sessions instead of this directory's, including every project nested under PATH — which is how naming a repo picks up its git worktrees. |
| `--from cli\|app\|sdk\|all` | `list`, `view` | `cli`+`app` | Where the session was run. `sdk` is anything non-interactive (`claude -p`, a hook, CI) and is **hidden by default**; `all` restores it. On `view` (no id) it picks which kind the most-recent lookup walks back to; it cannot be combined with a session id. |
| `--format json\|text` | render, `list` | `text` | `json` emits machine-readable output for piping. On the render path it's the full session model (`meta` + `turns`, ignoring `--level`/channels and color), with `meta.effort` beside `meta.model` and each tool call carrying the `identity` that `list --include tools` groups by plus the `model` an `Agent` call delegated to; on `list` it's a JSON array of per-session summaries, each carrying its `cwd`, the `files` it modified as absolute paths, and its `denials` (ignoring `--include` and color), and stdout is always a valid array — a directory with no project, or a project with no sessions, prints `[]` while still reporting the error on stderr and exiting non-zero, so you can pipe into `jq` without a guard. |
| `--no-color` | global | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | global | — | Per-verb `--help` lists only that mode's flags. |

Bare `agentry` is the listing, so the "`list`" flags apply to it as well as to `agentry list`; the "render" flags apply to `agentry <uuid>` and `view`; "global" flags work anywhere.

Markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap. Build, test, and install workflow: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT — see [LICENSE](LICENSE).
