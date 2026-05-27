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

With no argument, `agentry` selects the **most recent session by modification time** in the current project — which may include a session that is still in progress. With an argument, the session id must be a **full UUID**; partial-id matching and content search are on the roadmap.

`agentry` resolves logs from the current directory: it maps `$PWD` to the Claude project folder under `~/.claude/projects/`. Running from a different directory targets a different project — this is intentional. If the directory has no matching project, or the named session does not exist, `agentry` writes an error to stderr and exits with a distinct non-zero code.

## Output

`agentry` prints a styled view of the session: colorized turns with per-actor glyphs, plus code blocks, thinking, tool calls, and subagents (subject to verbosity).

A turn shows the user prompt as a highlighted block enclosed in a rounded border — the prompt text prefixed with a `❯` glyph (wrapped lines hang-indent to align under the prompt) — then the assistant's reply as an indented left rail (`│`) headed by a `◆` glyph aligned with the rail — prose, thinking, and tool calls hang off the rail, which a closing rule (`╰─`) terminates. With color off, the prompt highlight degrades to plain `❯`-prefixed text.

Color is auto-detected: styled (ANSI) when stdout is a terminal; plain, unstyled text when stdout is piped or redirected, or when `NO_COLOR` is set. `--no-color` forces plain output. Output is not paged in this version — pipe to a pager if you want one.

## Verbosity

`--level minimal | standard | detailed | full` selects how much of each turn is shown: prompts only → + thinking → + tools → + subagents. Per-channel overrides (`--thinking` / `--no-thinking`, `--tools` / `--no-tools`, …) adjust individual channels. Default is `minimal`.

## CLI conventions

Data on stdout, diagnostics on stderr. `NO_COLOR` (any non-empty value) or a non-TTY stdout disables color. `--help` and `--version` are always available. Exit codes follow `sysexits` — a missing project or session exits with a distinct non-zero code, not a generic failure.

## Scope

**MVP:** resolve a session from the current directory (no-arg → most recent by modification time; full-UUID arg → that session); styled terminal output with auto color detection; verbosity levels and channel overrides.

**Roadmap:**

- Session selection beyond a full id: content search, partial-id match, recency selectors.
- `--format json`: the structured session model emitted for agent consumption. The model is built in memory first and drives terminal rendering; exposing it is serialization.
- `--format md` / `-o FILE`: markdown-file export.
- Interactive session browser.
- Terminal hyperlinks: render URLs as clickable links (OSC 8) on terminals that support it; plain URLs otherwise.
- Paging.
- homebrew-core distribution.

**Non-goals:** editing or replaying sessions; inline images; non–Claude Code log formats, until explicitly scoped.

## Log format compatibility

Claude Code's on-disk log format evolves across versions. `agentry` tracks the current format and retains support for older ones it has previously handled: a format change must not break rendering of sessions written by earlier Claude Code versions. Unrecognized fields and entry types are ignored, not treated as errors.

## Distribution

Built as a single static binary by GoReleaser. macOS installs from a personal Homebrew tap, distributed as a cask (GoReleaser deprecated formulae for pre-built binaries): `brew install eitanpo/tap/agentry`. Homebrew casks are macOS-only; on Linux install with `go install` or download a release binary. homebrew-core is a later target.
