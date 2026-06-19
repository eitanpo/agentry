# agentry — Agent Guide

`agentry` (Agent Replay) renders Claude Code session logs to the terminal. See [PRODUCT.md](PRODUCT.md) for what it does and why, and [README.md](README.md) for install and usage. Do not restate their content here.

## Authority order

When sources disagree, the higher one wins; fix the lower to match.

1. **PRODUCT.md** — observable behavior, UX, scope. The spec.
2. **README.md** — install and usage. Must match PRODUCT.md.
3. **Code** — must match both.

Design docs under `design/` are ephemeral planning artifacts, not authority (see Phases).

## Change workflow — docs before code

Any change that alters observable behavior starts in the docs, not the code:

1. Edit the PRODUCT.md section that specifies the behavior. If none exists, add it.
2. If the user-facing surface (commands, flags, output) changed, update README.md.
3. Change the code to match.

In your change, name the PRODUCT.md section that specifies the new behavior. If you cannot point to one, the docs step was skipped — do it first.

Exceptions (code-only, no doc edit): a bug fix that makes code match the existing spec; a refactor with no observable change.

## Phases and throwaway design

Work proceeds in phases. Each phase:

1. Write a design doc at `design/<phase>.md` — the implementation plan for that phase only.
2. Implement against it.
3. On merge, fold every durable decision into PRODUCT.md and README.md, then delete `design/<phase>.md`.

Design docs do not accumulate. The durable record is PRODUCT.md + README.md + code + git history.

## Before reporting a change done

For each observable behavior you changed, name four things: the PRODUCT.md section, the README.md section (or "no user-facing change"), the code location, and the test that fails if the behavior regresses. If the first three disagree, the change is not done. Write the test as part of the change — a green build and a manual check show it works now, not that it stays working; do not offer the test as optional or defer it to a follow-up.

Three exits, each requiring a stated reason in the report:

- **No observable behavior changed** — a pure refactor (name the existing suite that still passes) or a doc/process-only edit (state "no behavior surface"). No new test required; this mirrors the code-only exceptions in §Change workflow.
- **Genuinely unobservable** — "can't be pinned" holds only when the behavior produces no output a test could assert. agentry renders to a writer and sets exit codes, so rendered text, stdout, and exit status are all observable; effort or fiddliness is not a valid reason. Name the specific technical barrier.
- **Explicit user instruction to skip** — honor it, and record in the report that you skipped the test and why, so the decision is visible rather than silent.

## Implementation notes

Living reference docs:

- [DEVELOPMENT.md](DEVELOPMENT.md) — build, test, and install workflow from source.
- [docs/session-format.md](docs/session-format.md) — structure of Claude Code session logs (files, folders, JSONL schema). The parser and locator encode this; update it when the observed format changes.
- [docs/implementation-gotchas.md](docs/implementation-gotchas.md) — non-obvious traps in agentry's own code and runtime.

**Capture habit.** During any change, record a terse symptom → cause → fix entry when any of these occur:

- a dependency or API behaved opposite to its name or documentation;
- you chose a concrete behavior for something the spec left unspecified;
- real data or a real API diverged from the reference or from your assumption;
- a fix resolved a crash, infinite loop, or silent wrong-output that a naive reading would not predict.

Route by subject: log-format findings → `docs/session-format.md`; build/test/install or local-tooling findings → `DEVELOPMENT.md`; everything else (code/runtime traps) → `docs/implementation-gotchas.md`.

## Stack

Go; Charm Glamour + Lip Gloss for rendering; GoReleaser + a Homebrew tap for distribution. Build and run details live in README.md — do not duplicate them here.
