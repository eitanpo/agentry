# Implementation gotchas

Non-obvious traps hit while building agentry. Append new ones as they're found — see
[AGENTS.md](../AGENTS.md) → Implementation notes for when. Format: symptom → cause → fix.
Format-of-the-log findings go in [session-format.md](session-format.md) instead.

## Subagent parsing

**Forked-skill stitching prefers `toolUseResult.agentId`; name-match is a fallback.**
`Agent` and forked-`Skill` results both carry `toolUseResult.agentId`, which `sidecarIDs`
maps to the sidecar unambiguously. Only pre-structured logs (no `agentId`) fall back to
matching a sidecar by skill name — and that fallback is first-match-wins, so a skill
forked more than once in those old logs still resolves later calls to the first sidecar.

**Inline skills must NOT be nested under their `Skill` call.** A skill that runs inline
(result `Launching skill: <name>`) writes no sidecar; its work is ordinary main-chain
turns tagged `attributionSkill: <name>`. That tag persists until the next user prompt, so
it covers the bulk of the turn's real work — not a short delimited sub-execution. Folding
those turns into the call's `Subagent` would hide them whenever the subagents channel is
off (minimal/standard), violating PRODUCT.md's "response text is always shown." So
`attachSubagent` leaves an inline skill as a leaf; only forked skills (real sidecars)
expand.

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

**glamour renders inline links as `text url`, never OSC 8 — and pre-render OSC 8 gets
mangled.** glamour v1.0.0's `LinkElement` prints the link text then the raw URL (its
`SkipHref` is set only for links inside tables; there is no global option). Injecting an
OSC 8 hyperlink into the markdown *source* before glamour fails too: glamour's reflow
word-wrap is CSI-aware but treats OSC sequences as ordinary text, so it counts the URL
bytes as width and wraps *inside* the escape, leaking the URL. The working approach
(`extractLinks` + `linkifyMarkdown`): strip `[text](url)` to just `text` before
glamour so it renders clean prose, then wrap the rendered text in OSC 8 afterward.

**glamour auto-links and colors bare `http(s)://` URLs, but not other schemes.** goldmark's
linkify recognizes `http`/`https` in prose and glamour paints them in its link color (still
no OSC 8) — so a bare `https://…` reaches `linkifyMarkdown` already styled, while a bare
`obsidian://…` (or any non-http scheme) arrives as plain text. Either way our own linkify
matches the URL substring and re-wraps it, so the difference is only cosmetic — but don't
assume a bare URL is plain when you match it.

**Anything you feed glamour is parsed as markdown — including text you reconstruct.** When
a rendered label isn't verbatim source but something we build (the `[[note]]` wikilink from
an `obsidian://open?file=…` URI), a name containing `*`, `` ` ``, `[]`, `_` etc. is
interpreted as markup: glamour drops the `*`, and the post-render substring match for the
recorded label then fails, so the link silently doesn't attach. Escape reconstructed text
with `escapeMarkdown` before it goes into the glamour source. (The surrounding `[[ ]]`
survive unescaped — a `[…]` with no matching reference definition renders literally.)

**glamour interleaves SGR codes through inline text — even between adjacent chars.** Text
comes back fragmented as `Re`·`\x1b[0m\x1b[1m`·`searcher`·…, so a regex/substring run on
the styled output can miss a span. To match text in glamour output, strip ANSI to a plain
string while recording each plain byte's offset in the styled string (`stripANSI`), match
on the plain text, then splice insertions back at the mapped offsets.

**A CSI-skip loop must step past the `[` before scanning for the final byte.** `[` is
0x5b, inside the CSI final-byte range 0x40–0x7e, so a loop that stops at the first
in-range byte treats the `[` itself as the terminator and consumes only `\x1b[`, leaving
the parameter bytes in the "plain" text. Advance past `[` first, then scan.

**`bufio.Scanner`'s default 64 KB token limit is too small.** Session lines embed full
tool results and can exceed it; raise the scanner buffer or long lines silently drop.

## CLI / Cobra

**Cobra's built-in did-you-mean (`SuggestionsMinimumDistance`) never fires when the root
command is runnable.** It triggers only on the "unknown subcommand" path. A root with its
own `RunE` (agentry renders on the bare command) treats an unmatched first token as a
positional arg and passes it to `RunE` — so `agentry lst` reaches the render path with
id `"lst"`, not an unknown-command error. Verb suggestions are therefore hand-rolled in
`renderSession`: a first token that is not hex-shaped (`looksLikeID`) can only be a
mistyped verb, so it is run through `nearest()` against the verb list. The hex-vs-word
invariant is what makes this unambiguous — a real session id never reaches the verb check.

**pflag emits a bare `unknown flag: --x` with no suggestion.** Cobra's suggestion machinery
covers subcommands only; flag-name typos are not covered. agentry layers `nearest()` over
the failing command's flag set via `SetFlagErrorFunc` on the root (Cobra resolves it up the
parent chain, so it applies to every verb, and the `*cobra.Command` it receives is the verb
whose flags failed — the correct candidate pool). Flag *values* (`--include prompt`,
`--level detaild`) are validated by agentry itself, so those suggestions live at each enum
site, not in the flag-error hook.
