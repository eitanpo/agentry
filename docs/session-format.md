# Claude Code session log format

Reverse-engineered from real logs (observed 2026-05-27, re-verified 2026-06-19).
**Not an official spec** —
Claude Code may change it without notice; re-verify against live files before relying
on a detail here. `internal/parse` and `internal/locate` encode this format.

## Location and naming

- Root: `~/.claude/projects/`.
- One folder per project (working directory). The folder name is the project's
  absolute path with the leading `/` stripped, every `/` replaced by `-`, and a `-`
  prefixed. E.g. `/Users/me/Projects/dotfiles` → `-Users-me-Projects-dotfiles`.
- One file per session: `<project>/<session-uuid>.jsonl`. The session id is a full UUID.
- Subagent sidecars: `<project>/<session-uuid>/subagents/agent-<id>.jsonl`, each with an
  `agent-<id>.meta.json` sibling. A sidecar is itself session-shaped JSONL.

## Line format

Each line is one JSON event. Lines can be large — tool results are stored inline with
full content. Malformed lines occur; skip them rather than failing.

Common top-level fields: `type`, `timestamp` (RFC3339), `message`, plus context such as
`cwd`, `gitBranch`, `sessionId`, `uuid`, `parentUuid`, `version`, `isSidechain`.

The set grows over time and is additive — Claude Code adds fields without removing
existing ones, so unknown fields are expected and the parser ignores them. Fields seen
since the initial observation, with their meaning:

- `entrypoint` — session origin (`cli`, `claude-desktop`, …).
- `userType` — `external` for human-driven sessions.
- `promptSource` — set to `sdk` when a `user` prompt arrived via the SDK rather than
  being typed; absent for typed prompts.
- `attributionSkill` (assistant entries) — names the **inline** skill whose execution
  produced this main-chain turn (see Subagent stitching). Absent on turns not run under
  a skill.
- `toolUseResult` — a top-level **structured** mirror of a tool's result, on the same
  `user` entry that carries the `tool_result` block. Shape varies by tool (e.g. `Edit`
  → `structuredPatch`/`userModified`; `Bash` → `stdout`/`stderr`; sometimes just a
  string). For `Agent` and forked-`Skill` calls it includes `agentId` — the stitch key
  the parser uses (see Subagent stitching). The result **body** still comes from the
  in-`message` `tool_result` block.
- `sourceToolAssistantUUID` / `sourceToolUseID` — link a synthetic `user` entry back to
  the assistant turn / tool call that generated it.
- `system` entries carry `subtype` (`stop_hook_summary`, `compact_boundary`,
  `turn_duration`, `away_summary`, …) and sometimes `level` (`info`, `suggestion`).
- Hook metadata: `hookInfos`, `hookErrors`, `hookCount`, `hookAdditionalContext`.

### Entry types

Observed: `assistant`, `user`, `system`, `ai-title`, `custom-title`, `attachment`,
`file-history-snapshot`, `last-prompt`, `permission-mode` (and `progress`,
`queue-operation` as transient noise). **Only `assistant` and `user` carry renderable
content**; ignore the rest.

The last entry's type is not an end-of-session marker — it is whatever happened last.
There is no reliable in-file "session complete" signal.

`ai-title` entries carry a top-level `aiTitle` string — Claude Code's own session
summary, rewritten as the session evolves, so multiple appear and the last is the
current one (`sessionId` names the session). It is not renderable content, but the
session listing (`agentry list`) uses the latest `aiTitle` as the session's title.

`custom-title` entries carry a top-level `customTitle` string — the name you give a
session by renaming it in Claude Code. It overrides `aiTitle` in the listing (see the
title ladder in PRODUCT.md §`agentry list`), and once one is written Claude Code stops
appending fresh `ai-title` entries, so the latest `aiTitle` is frozen at its pre-rename
value. The latest non-empty `customTitle` wins.

### message

`{ role, model, content, usage, ... }`. `content` is **either** a JSON string **or** an
array of typed blocks:

- `text` — `.text`
- `thinking` — `.thinking` (+ `.signature`)
- `tool_use` — `.id`, `.name`, `.input` (object). `.input` shape varies by tool;
  the fields used for a call's identity (`list --include tools`) are `.command`
  (Bash), `.skill` (Skill), and `.subagent_type` (Agent).
- `tool_result` (inside `user` entries) — `.tool_use_id`, `.is_error`, `.content`
  (a string, or an array of `{type:"text", text}`)

### usage (assistant entries)

`input_tokens`, `output_tokens`, `cache_read_input_tokens`,
`cache_creation_input_tokens` (plus nested `cache_creation`, `iterations`,
`server_tool_use`, and metadata `service_tier`, `speed`, `inference_geo` — none needed
for token totals).

## User entries: typed vs injected

A `user` entry's string content is a human-typed prompt **unless** it is
system-injected. Injected markers include `<local-command-caveat>`, `<bash-input>`,
`<bash-stdout>`, `<bash-stderr>`, `<local-command-stdout>`,
`Base directory for this skill:`, and `<task-notification>` (the harness's
background-task event/completion reports — a `user` entry wrapping `<task-id>`,
`<status>`, `<summary>`, `<output-file>`, not anything the human typed). Slash
commands appear as `<command-name>…</command-name>` / `<command-args>…</command-args>`.
The leading slash in `<command-name>` is **inconsistent**: built-ins carry it (`/clear`,
`/compact`, `/refine`), custom commands do not (`sonar`, `exa`, `agent-guidelines`). So
code that reconstructs the prompt must normalize — strip any leading slashes and add
exactly one — rather than blindly prefixing `/` (which doubles built-ins to `//clear`).
Trailing text typed on the same line as a command lands in `<command-args>`, so
`/clear improve the parser` records as name `/clear`, args `improve the parser` — not a
separate prompt. Note: a command can also appear as **plain string content** (no
`<command-name>` wrapper, e.g. a literal `/commit push`), which renders verbatim with its
single slash. Array-of-`text` user content is also injected (e.g. skill bodies), not a
typed prompt.

## Subagent stitching

A `tool_use` that spawns a child session writes a sidecar; stitching maps the call to it.

- **`Agent` calls** always fork a sidecar. The id is `toolUseResult.agentId` on the
  result `user` entry (see Common top-level fields) → `agent-<id>.jsonl`. Pre-structured
  logs lack that field but carry an `agentId: <id>` line in the `tool_result` text; the
  parser prefers the structured field and falls back to the text line.
- **`Skill` calls run in one of two modes**, distinguished by the tool's result:
  - **Inline** — result text `Launching skill: <name>`. The skill runs in the **main
    chain**: its body is injected as a `user` entry carrying `Base directory for this
    skill: <path>`, and the assistant turns it produces are tagged
    `attributionSkill: <name>`. **No sidecar is written** — there is nothing to stitch.
  - **Forked** — result text `Skill "<name>" completed (forked execution).`. This writes
    a sidecar, and `toolUseResult.agentId` gives its id directly — the key the parser
    uses. The sidecar also names its skill in a `Base directory for this skill: <path>`
    line (base name = skill name); the parser falls back to matching by that name for
    pre-structured logs that lack `agentId`. Name-matching is ambiguous when the same
    skill forks more than once in a session, so the `agentId` is preferred.
- Because inline skills inject the `Base directory for this skill:` marker into the main
  chain, that marker now appears in **both** main-chain and sidecar files — sidecar
  skill-name detection must read only `agent-*.jsonl`, not the main log.
- Subagents nest recursively; a sidecar may itself contain `Agent`/`Skill` calls. Guard
  against reference cycles — see [implementation-gotchas.md](implementation-gotchas.md).
