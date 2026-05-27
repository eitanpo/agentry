# Implementation gotchas

Non-obvious traps hit while building ase. Append new ones as they're found — see
[AGENTS.md](../AGENTS.md) → Implementation notes for when. Format: symptom → cause → fix.
Format-of-the-log findings go in [session-format.md](session-format.md) instead.

## Subagent parsing

**Skill→subagent matching is by name, first match wins.** A `Skill` call carries no
agentId, so `internal/parse` matches it to a sidecar by skill name. When the same skill
runs more than once in a turn, every call resolves to the first matching sidecar — later
invocations render the wrong stream. (`Agent` calls avoid this; they stitch by explicit
agentId.)

**Subagent references can cycle.** A skill sidecar can contain a `Skill` call that
matches itself by name, so naive recursive expansion infinite-loops (stack overflow).
`buildEvents` threads a `seen` set of sidecar ids to break cycles.

## Rendering and dependencies

**`termenv.Ascii` is `3`, not `0`.** Passing a literal `0` to
`lipgloss.SetColorProfile` selects `TrueColor` (color on) — the opposite of intent. Use
the named `termenv.Ascii` constant to strip ANSI, and verify "no color" by counting ESC
bytes in piped output, not by eye.

**glamour pads wrapped lines with trailing spaces.** Right-trim each rendered line or
boxed/prefixed layouts inherit ragged trailing whitespace.

**`bufio.Scanner`'s default 64 KB token limit is too small.** Session lines embed full
tool results and can exceed it; raise the scanner buffer or long lines silently drop.
