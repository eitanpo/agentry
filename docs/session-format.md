# Claude Code session log format

Reverse-engineered from real logs (observed 2026-05-27). **Not an official spec** —
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
`server_tool_use`, not needed for token totals).

## User entries: typed vs injected

A `user` entry's string content is a human-typed prompt **unless** it is
system-injected. Injected markers include `<local-command-caveat>`, `<bash-input>`,
`<bash-stdout>`, `<bash-stderr>`, `<local-command-stdout>`,
`Base directory for this skill:`, and `<task-notification>` (the harness's
background-task event/completion reports — a `user` entry wrapping `<task-id>`,
`<status>`, `<summary>`, `<output-file>`, not anything the human typed). Slash
commands appear as `<command-name>…</command-name>` / `<command-args>…</command-args>`;
the `<command-name>` value **already includes the leading slash** (e.g. `/clear`,
`/research-lookup`), so code that prefixes `/` itself doubles it (`//clear`) — strip or
skip the prefix. Array-of-`text` user content is also injected (e.g. skill bodies), not a
typed prompt.

## Subagent stitching

- An `Agent` tool call's `tool_result` text contains a line `agentId: <id>`. Map the
  `tool_use_id` → `agent-<id>` to find the sidecar.
- A `Skill` tool call carries no agentId; match it to a sidecar by skill name. Each
  sidecar names its skill in a `Base directory for this skill: <path>` line — the base
  name of that path is the skill name.
- Subagents nest recursively; a sidecar may itself contain `Agent`/`Skill` calls. Guard
  against reference cycles — see [implementation-gotchas.md](implementation-gotchas.md).
