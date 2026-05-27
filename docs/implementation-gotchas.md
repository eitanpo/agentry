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

**glamour bakes a 2-space left margin into every rendered line.** The standard `dark`
and `notty` styles set `Document.Margin = 2`, so prose never hugs a custom left-rail
prefix. To remove it, copy the style config (`styles.DarkStyleConfig` /
`NoTTYStyleConfig` — both values, so assignment is a safe deep copy of the embedded
`Document` block), set `Document.Margin` to a pointer to `0`, and pass it via
`WithStyles` instead of `WithStandardStyle`. Do *not* strip leading spaces from output
lines — that also flattens the relative indentation of code blocks and nested lists.

**lipgloss `Background`/`BorderBackground` emit an empty `\x1b[;m` under the Ascii
profile.** Setting a background on a style still writes a (color-stripped but non-empty)
SGR sequence, so a style that renders clean under `termenv.Ascii` starts leaking ESC
bytes once a background is added — breaking any "no color → zero ESC bytes" check. A
plain foreground-only border does not. Fix: apply background colors only when color is
on (guard on the color flag), not relying on the Ascii profile to strip them.

**`bufio.Scanner`'s default 64 KB token limit is too small.** Session lines embed full
tool results and can exceed it; raise the scanner buffer or long lines silently drop.
