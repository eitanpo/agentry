# agentry — Product

Agent Replay: a single-binary CLI that renders a Claude Code session log into a readable, styled terminal view.

## Why

Claude Code stores each session as JSONL under `~/.claude/projects/`. Reading that raw is impractical. Routing the log through an agent's context instead strips terminal styling, forces a prompt-injection envelope, and is bounded by context size. `agentry` renders the log directly to the human's terminal: styling is native, there is no injection surface, and session size is unbounded.

## Users and usage

A developer who runs Claude Code in a project directory and wants to review a past session. The user runs `agentry` from the same directory they ran Claude in:

```
agentry                # the most recent session (by time) in this directory's project
agentry <uuid>         # a specific session, by full id
agentry view [<uuid>]  # explicit alias of the above; owns the render flags' help page
agentry list           # list this project's sessions (see Listing sessions)
```

With no argument, `agentry` selects the **most recent session by modification time** in the current project — which may include a session that is still in progress. With an argument, the session id must be a **full UUID**; partial-id matching and content search are on the roadmap. To find a session, list them with `agentry list` (see Listing sessions).

`agentry` is organized as verbs. The first token is treated as a verb when it names one (`view`, `list`), otherwise as a session id — unambiguous because ids are hex (`0-9a-f`, hyphens) and verbs are English words, so they never collide. `view` is an explicit alias of the bare-render path: `agentry`, `agentry <uuid>`, and `agentry view <uuid>` render identically, but `view` owns the render flags' own help page and is discoverable in `agentry --help`.

`agentry` resolves logs from the current directory: it maps `$PWD` to the Claude project folder under `~/.claude/projects/`. Running from a different directory targets a different project — this is intentional. If the directory has no matching project, or the named session does not exist, `agentry` writes an error to stderr and exits with a distinct non-zero code.

## Output

`agentry` prints a styled view of the session: colorized turns with per-actor glyphs, plus code blocks, thinking, tool calls, and subagents (subject to verbosity).

A turn shows the user prompt as a highlighted block enclosed in a rounded border — the prompt text prefixed with a `❯` glyph (wrapped lines hang-indent to align under the prompt) — then the assistant's reply as an indented left rail (`│`) headed by a `◆` glyph aligned with the rail — prose, thinking, and tool calls hang off the rail, which a closing rule (`╰─`) terminates. With color off, the prompt highlight degrades to plain `❯`-prefixed text.

Markdown links (`[text](url)`) in assistant prose render as OSC 8 terminal hyperlinks: the link text is shown in a dedicated link color and is clickable, and the raw URL is hidden. The URL is used verbatim as the href, for any scheme (`https://`, `obsidian://`, …) — agentry constructs nothing. Only assistant prose is linkified (not tool output, thinking, or prompts), and a link whose text glamour wrapped across lines stays plain. Like color, this is gated on a terminal: with color off, the link renders in glamour's default form (the text followed by the URL).

Color is auto-detected: styled (ANSI) when stdout is a terminal; plain, unstyled text when stdout is piped or redirected, or when `NO_COLOR` is set. `--no-color` forces plain output. Output is not paged in this version — pipe to a pager if you want one.

`--format json` emits the full parsed session model as JSON instead of the styled view — the render path's machine-readable form, for agent consumption and piping into `jq`. It serializes the same in-memory model that drives terminal rendering: a top-level object with `meta` (`id`, `model`, `start`, `end`, `usage`, `numSubagents`) and `turns`. Each turn carries its `prompt`, `start`, `end`, `usage`, `toolCount`, `errorCount`, and an ordered `events` array; each event has a `kind` — the string `text`, `thinking`, or `tool` (not its ordinal) — with a `text` body for the first two, or a `tool` object (`name`, `args`, `result`, `isError`, `start`, `end`, and a nested `subagent` event stream when the call spawned one) for the last. Being the complete model, it ignores the verbosity flags (`--level` and the channel toggles) and color — those shape only the text view, whereas the JSON is always the whole model. `--format` also accepts `text` (the default styled view); an unknown value is a usage error, suggesting the nearest valid format like every other enum flag. This is the render-path counterpart of `list --format json` (see Listing sessions), which serializes the lightweight per-session summaries.

## Listing sessions

`agentry list` lists the sessions in the current directory's project instead of rendering one, so you can find the one you want. Sessions are selected by recency (most-recent activity) and printed oldest-to-newest, so the most recent sits at the bottom, nearest your prompt — the convention of `ls -ltr`, shell history, and chat logs for unpaged scrolling output. Each is one row: the session's start time, its duration (first prompt to last output), its turn count, a title, and the full session id — which you pass back to `agentry <id>` to render it. The title is chosen by a fallback ladder: the session's manual title (the `custom-title` Claude Code writes when you rename a session, latest one winning) when present — an explicit rename wins over everything below, and Claude Code freezes its own summary once you set one; otherwise Claude Code's own session summary (the `ai-title` it writes into the log, latest one winning); otherwise the first user prompt, skipping a leading `/clear` (which only resets context and says nothing about the session) in favor of the next prompt; a session that is nothing but `/clear` keeps it. A `/clear` typed with trailing text on the same line (Claude Code records the text as the command's arguments) is still treated as `/clear` and skipped — the trailing text describes the reset, not the session.

Forked sessions are grouped. Claude Code's `--fork-session` (and the in-session `/branch`) copies a session's conversation into a new file, so a fork and its parent share the same root entry; agentry detects this by their shared root id and lists the family together — the original session's row, then each fork indented beneath it with a `└─` marker. The original is the **earliest-created file** in the family: a fork is a new file written at fork time and cannot reproduce the original's file-creation time, even though its copied content reproduces the original's in-conversation timestamps. (On platforms where a file's creation time is not readable — anything but macOS — agentry falls back to the modification time, which can mis-order a family whose sessions all stayed active.) A family takes its position in the list from its most-recently-active member. `/clear` is not a fork: it starts an empty session with a fresh root id, so it never joins a family.

A fork inherits its parent's title — Claude Code seeds the new session with the parent's `ai-title`, so until it regenerates one the two are identical and indistinguishable in the list. While a fork's title still equals its parent's, the listing instead titles it by its **first divergent prompt**: the first prompt the fork has that its parent does not — the first turn unique to the fork (a leading `/clear` is skipped, as in the title ladder, since `/clear` is already absent from the prompt list). A fork that has added no new prompt yet keeps the shared title. This is a text-table refinement only; `--format json` reports each session's real stored title.

By default the list shows the 10 most-recent sessions. `--limit N` changes the cap; `--limit 0` removes it. Two time-frame filters narrow the window, both compared against a session's last-activity time:

- `--since WHEN` — sessions active at or after WHEN.
- `--until WHEN` — sessions active at or before WHEN.

WHEN is one of: `today` or `yesterday` (local midnight of that day); a span back from now with a unit — `Nh`, `Nd`, or `Nw` (e.g. `24h`, `7d`, `2w`); or an absolute local date `YYYY-MM-DD`. An unrecognized WHEN is a usage error.

`--include CHANNELS` adds per-session detail, given as a comma-separated list of channels. All detail renders under the session's row on a left rail closed by a rule — the same chrome as a rendered turn — and the rail-and-rule bounds the block, separating one session from the next. When more than one channel is selected they share a single block (one rail, one closing rule). `all` selects every channel. An unknown channel is a usage error. The channels:

- `prompts` — the session's user prompts, listed in order each prefixed with the `❯` glyph, `/clear` omitted (including a `/clear` carrying trailing arguments); every prompt is shown (the view is opt-in).
- `tools` — a breakdown of the session's **top-level** tool calls, grouped by identity rather than by bare tool name, because the signal is *which* commands, skills, and agents ran. Four category lines, each shown only when non-empty: `Skills` (by skill name), `Agents` (by subagent type — named agents appear by name), `Bash` (by invoked program — the first command token, leading `VAR=` assignments stripped, reduced to its basename), and `Other` (every remaining tool by its own name). Within a line, entries read `name ×count`, ordered by count descending then name. "Top-level" means the calls the main thread made — calls inside subagents are not counted, matching the per-turn tool count.

A second family of flags filters the listing by the tool calls a session made — answering "which sessions used X?" without reading each one. Two axes:

- `--used-tool NAME` — sessions where that tool fired, matched against the tool-use name (`Bash`, `WebFetch`, `Skill`, `Agent`, …), case-insensitive and exact. This is the "which mechanism" axis.
- `--used-skill NAME`, `--used-agent TYPE`, `--used-command PATTERN` — the identity axis: what was invoked *through* the three identity-bearing tools — a Skill call's skill, an Agent call's subagent type, a Bash command's text.
- `--used TOKEN` — a catch-all over the identity axis: matches a skill name, an agent type, or a command. It deliberately does **not** match tool names, so the two axes never collide — to filter by tool name use `--used-tool`.

Identity-axis matches (everything but `--used-tool`) are case-insensitive substring, so `--used-skill sonar` also catches `sonar-search` and `--used-command 'git push'` matches that phrase anywhere in a command. Substring cuts both ways: a short token over-matches (`--used-command exa` also hits `exact`), so disambiguate with a more specific pattern (`scripts/exa`) when a token is also a common substring. The population is the same top-level calls `--include tools` counts — calls inside subagents are not considered. Multiple filter flags combine with AND (a session must satisfy all); for OR, run separate listings. Like the `--include tools` breakdown, these read a session's tool calls, so they pair naturally: filter to the sessions that used something, then `--include tools` to see what else those sessions did.

`--format json` emits the selected sessions as a JSON array instead of the text table — the listing's machine-readable form, for piping into `jq`. It carries the full model per session (`id`, `start`, `end`, `title`, `numTurns`, `prompts`, `tools`, `commands`, and `rootUuid` — the shared root id that groups a fork family), in the same order the table shows (most-recent first); the JSON stays flat (fork grouping shapes only the text table). Being the complete model, it ignores `--include` (a text-shaping flag) and color; the selectors (`--since`/`--until`/`--limit`/`--used*`) still choose which sessions appear, and an empty selection emits `[]`. `--format` also accepts `text` (the default); an unknown value is a usage error. The default text table renders as below.

When a time filter **or any `--used*` filter** is given and `--limit` is not, the cap is lifted — `agentry list --since today` shows every session from today, not just ten. A window that matches no session prints nothing and exits zero; a project with no sessions at all is an error, the same as in render mode. `list` takes no session-id argument; the rendering flags (`--level`, channel toggles) do not exist on it — they live on the render path, not as flags `list` silently ignores.

Color follows the same rule as rendered output: styled on a terminal, plain when piped or with `NO_COLOR` / `--no-color`.

## Verbosity

`--level minimal | standard | detailed | full` is a preset over independent channels; default `minimal`. Each level is just a named set of channel defaults — per-channel overrides (`--thinking` / `--no-thinking`, `--tools`, `--tool-results`, `--subagents`, `--metrics`, and their `--no-` forms) adjust any channel on top of the level, adding or subtracting (e.g. `--level detailed --no-thinking`).

The user prompt and the agent's response text are always shown — they are the irreducible transcript, not a channel. Levels layer notions of the work onto that base, breadth before depth: each level adds more *kinds* of detail until the deepest level adds the result bodies.

- **minimal** — prompts and response only.
- **standard** — + thinking; + metrics.
- **detailed** — + tools (the notion that a tool fired) and + subagents (skill and subagent expansion). The shape of the work, without tool output.
- **full** — + tool-results (each tool's output body). The content of the work.

The channels:

- **thinking** — assistant reasoning blocks.
- **tools** — one line per tool call: that it fired, with name, truncated args, status, and duration.
- **tool-results** — the result body of each tool call. `tools` shows *that* a tool fired; `tool-results` shows *what it returned*.
- **subagents** — expansion of nested work: a call that spawned a child session renders that session's inner event stream instead of a result body. This covers `Agent` calls and **forked** skills — the ones that run in a separate session. An **inline** skill runs in the main chain, so its work already shows as ordinary turns there and the call stays a leaf (nothing to expand). Inside an expanded stream the same channels apply, so at `detailed` a forked skill expands to its tool-activation lines without their bodies.
- **metrics** — the session summary table (per-turn token and tool breakdown). The per-turn footer (duration, tool count, errors) is always shown, independent of this channel.

## CLI conventions

Data on stdout, diagnostics on stderr. `NO_COLOR` (any non-empty value) or a non-TTY stdout disables color. `--help` and `--version` are always available, and each verb has its own `--help` listing only the flags valid in that mode. The bare command's help leads with the program version, groups the render flags (which also apply to `view`, not to `list`) under their own heading so their scope is legible, and every help screen carries usage examples. Exit codes follow `sysexits` — a missing project or session exits with a distinct non-zero code, not a generic failure.

Flags and operands may appear in any order (`agentry list --since today` and `agentry list` with flags before or after operands both parse). A mistyped verb, flag name, or enum value (e.g. `--include prompt` for `prompts`) errors to stderr, names the offending token, and suggests the nearest valid name by edit distance when one is close enough — it never auto-runs the guess and never dumps full help.

## Scope

**MVP:** resolve a session from the current directory (no-arg → most recent by modification time; full-UUID arg → that session); list sessions with recency and time-frame selectors (`agentry list`); styled terminal output with auto color detection; verbosity levels and channel overrides.

**Roadmap:**

- Session selection beyond a full id: content search (a `search` verb), partial-id match.
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
