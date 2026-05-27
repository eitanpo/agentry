# ase — Agent Guide

`ase` (Agent Session Explorer) renders Claude Code session logs to the terminal. See [PRODUCT.md](PRODUCT.md) for what it does and why, and [README.md](README.md) for install and usage. Do not restate their content here.

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

For each behavior you touched, name three things: the PRODUCT.md section, the README.md section (or "no user-facing change"), and the code location. If the three disagree, the change is not done.

## Stack

Go; Charm Glamour + Lip Gloss for rendering; GoReleaser + a Homebrew tap for distribution. Build and run details live in README.md — do not duplicate them here.
