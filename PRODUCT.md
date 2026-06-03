# agentry — Product

Agent Replay: a single-binary CLI that renders a Claude Code session log into a readable, styled terminal view.

## Why

Claude Code stores each session as JSONL under `~/.claude/projects/`. Reading that raw is impractical. Routing the log through an agent's context instead strips terminal styling, forces a prompt-injection envelope, and is bounded by context size. `agentry` renders the log directly to the human's terminal: styling is native, there is no injection surface, and session size is unbounded.

## Users and usage

A developer who runs Claude Code in a project directory and wants to review a past session. The user runs `agentry` from the same directory they ran Claude in:

```
agentry            # the most recent session (by time) in this directory's project
agentry <uuid>     # a specific session, by full id
```

With no argument, `agentry` selects the **most recent session by modification time** in the current project — which may include a session that is still in progress. With an argument, the session id must be a **full UUID**; partial-id matching and content search are on the roadmap. To find a session, list them with `--list` (see Listing sessions).

`agentry` resolves logs from the current directory: it maps `$PWD` to the Claude project folder under `~/.claude/projects/`. Running from a different directory targets a different project — this is intentional. If the directory has no matching project, or the named session does not exist, `agentry` writes an error to stderr and exits with a distinct non-zero code.

## Output

`agentry` prints a styled view of the session: colorized turns with per-actor glyphs, plus code blocks, thinking, tool calls, and subagents (subject to verbosity).

A turn shows the user prompt as a highlighted block enclosed in a rounded border — the prompt text prefixed with a `❯` glyph (wrapped lines hang-indent to align under the prompt) — then the assistant's reply as an indented left rail (`│`) headed by a `◆` glyph aligned with the rail — prose, thinking, and tool calls hang off the rail, which a closing rule (`╰─`) terminates. With color off, the prompt highlight degrades to plain `❯`-prefixed text.

Markdown links (`[text](url)`) in assistant prose render as OSC 8 terminal hyperlinks: the link text is shown in a dedicated link color and is clickable, and the raw URL is hidden. The URL is used verbatim as the href, for any scheme (`https://`, `obsidian://`, …) — agentry constructs nothing. Only assistant prose is linkified (not tool output, thinking, or prompts), and a link whose text glamour wrapped across lines stays plain. Like color, this is gated on a terminal: with color off, the link renders in glamour's default form (the text followed by the URL).

Color is auto-detected: styled (ANSI) when stdout is a terminal; plain, unstyled text when stdout is piped or redirected, or when `NO_COLOR` is set. `--no-color` forces plain output. Output is not paged in this version — pipe to a pager if you want one.

## Listing sessions

`agentry --list` lists the sessions in the current directory's project instead of rendering one, so you can find the one you want. Sessions are selected by recency (most-recent activity) and printed oldest-to-newest, so the most recent sits at the bottom, nearest your prompt — the convention of `ls -ltr`, shell history, and chat logs for unpaged scrolling output. Each is one row: the session's start time, its duration (first prompt to last output), its turn count, a title, and the full session id — which you pass back to `agentry <id>` to render it. The title is chosen by a fallback ladder: Claude Code's own session summary (the `ai-title` it writes into the log, latest one winning) when present; otherwise the first user prompt, skipping a leading `/clear` (which only resets context and says nothing about the session) in favor of the next prompt; a session that is nothing but `/clear` keeps it.

By default the list shows the 10 most-recent sessions. `--limit N` changes the cap; `--limit 0` removes it. Two time-frame filters narrow the window, both compared against a session's last-activity time:

- `--since WHEN` — sessions active at or after WHEN.
- `--until WHEN` — sessions active at or before WHEN.

WHEN is one of: `today` or `yesterday` (local midnight of that day); a span back from now with a unit — `Nh`, `Nd`, or `Nw` (e.g. `24h`, `7d`, `2w`); or an absolute local date `YYYY-MM-DD`. An unrecognized WHEN is a usage error.

`--include CHANNELS` adds per-session detail, given as a comma-separated list of channels. The one channel today is `prompts`: under each session's row, its user prompts are listed in order on a left rail closed by a rule — the same chrome as a rendered turn — each prefixed with the `❯` glyph, with `/clear` omitted; every prompt is shown (the view is opt-in). The rail-and-rule groups a session's prompts and bounds the block, separating one session from the next. `all` selects every channel. An unknown channel is a usage error.

When a time filter is given and `--limit` is not, the cap is lifted — `agentry --list --since today` shows every session from today, not just ten. A window that matches no session prints nothing and exits zero; a project with no sessions at all is an error, the same as in render mode. List mode takes no session-id argument and ignores the rendering flags (`--level`, channel toggles).

Color follows the same rule as rendered output: styled on a terminal, plain when piped or with `NO_COLOR` / `--no-color`.

## Verbosity

`--level minimal | standard | detailed | full` selects how much of each turn is shown: prompts only → + thinking → + tools → + subagents. Per-channel overrides (`--thinking` / `--no-thinking`, `--tools` / `--no-tools`, …) adjust individual channels. Default is `minimal`.

## CLI conventions

Data on stdout, diagnostics on stderr. `NO_COLOR` (any non-empty value) or a non-TTY stdout disables color. `--help` and `--version` are always available. Exit codes follow `sysexits` — a missing project or session exits with a distinct non-zero code, not a generic failure.

## Scope

**MVP:** resolve a session from the current directory (no-arg → most recent by modification time; full-UUID arg → that session); list sessions with recency and time-frame selectors (`--list`); styled terminal output with auto color detection; verbosity levels and channel overrides.

**Roadmap:**

- Session selection beyond a full id: content search, partial-id match.
- `--format json`: the structured session model emitted for agent consumption. The model is built in memory first and drives terminal rendering; exposing it is serialization.
- `--format md` / `-o FILE`: markdown-file export.
- Interactive session browser.
- Bare-URL autolinks: render plain `https://…` URLs (not wrapped in markdown link syntax) as OSC 8 hyperlinks too.
- Paging.
- homebrew-core distribution.

**Non-goals:** editing or replaying sessions; inline images; non–Claude Code log formats, until explicitly scoped.

## Log format compatibility

Claude Code's on-disk log format evolves across versions. `agentry` tracks the current format and retains support for older ones it has previously handled: a format change must not break rendering of sessions written by earlier Claude Code versions. Unrecognized fields and entry types are ignored, not treated as errors.

## Distribution

Built as a single static binary by GoReleaser. macOS installs from a personal Homebrew tap, distributed as a cask (GoReleaser deprecated formulae for pre-built binaries): `brew install eitanpo/tap/agentry`. Homebrew casks are macOS-only; on Linux install with `go install` or download a release binary. homebrew-core is a later target.
