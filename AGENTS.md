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

1. If the change adds a command, verb, or flag, write the invocation — see **Writing the invocation** below.
2. Edit the PRODUCT.md section that specifies the behavior. If none exists, add it.
3. If the user-facing surface (commands, flags, output) changed, update README.md.
4. Change the code to match.

**Writing the invocation** means listing the literal line a caller types for each question the request names, then naming the commonest question of that set. Either the bare command answers it, or the change says why not. Two reasons hold: the verb has no single commonest question; or every question the request named is secondary to one the bare command already answers. Step 2 cannot stand in for this — specifying a behavior does not choose an invocation, and a flag matrix satisfies the spec completely while still leaving the widest answer the hardest to type. PRODUCT.md's CLI conventions section owns what a bare form owes and why; do not restate it here. Carry the lines into the report.

In your change, name the PRODUCT.md section that specifies the new behavior. If you cannot point to one, the docs step was skipped — do it first.

Exceptions (code-only, no doc edit): a bug fix that makes code match the existing spec; a refactor with no observable change.

## Budgets that delete facts

A budget is any limit on how much a surface prints: a line count, a column width, a row cap, a verbosity gate. **When a change would drop, truncate, abbreviate or hide a fact the program already holds so that a surface stays inside one, write these four lines in the change and carry them into the report:**

1. **The budget and its source** — the number, and the PRODUCT.md line that sets it. A budget this change introduces, or one no line states, is written `unverified`. One imposed from outside — the column count a terminal reports, a field a format fixes — is written `external`: the limit is not yours, but choosing to delete the fact rather than wrap it or move it to a roomier surface still is. **Name who picked the number you are writing down.** An outside limit you then divide hands you a second budget of your own, and it is the one doing the deleting: the terminal fixes the row's width, you allot the columns inside it, and every one of those widths is `unverified` however tight the row. A change that meets both writes both lines rather than filing the pair as `external`.
2. **What it deletes** — the fact that stops being printed, and where else a reader who ran this same command in this same format still finds it, or `nowhere`. Another format is not that reader's output: a fact carried only by `--format json` is gone from the text render, so a deletion there answers `nowhere` and takes the consequence the third line weighs.
3. **The measured cost of one more unit** — render a real session and count what one more line, column or row adds against what that surface already prints.
4. **The decision** — `budget kept` or `budget relaxed`, in those two words, beside the labels the first two lines produced. The words name the budget rather than the fact, because `kept` alone has been written for both and a reader cannot tell which was meant. Where you write `budget kept`, name what makes relaxing it cost more than the fact it deletes. `budget kept` standing next to `unverified` and `nowhere` contradicts the default below, and the contradiction is the thing this line exists to put on the page.

Keep the budget only where relaxing it costs more than the deleted fact is worth. An `unverified` budget deleting a fact found `nowhere` else loses by default: it is this change's own invention, and agentry prints to a scrolling terminal, where a line costs a reader nothing and a missing fact costs them the answer. The fourth line is where that default is honored or overruled in writing: three lines of correct labelling followed by the cut they argue against is not a decision, and without the word on the page it only looks like one.

Do not run this for a number the user gave you, or a cap whose remainder the output names — `… (85 more files)` deletes nothing. **A limit the user asked for is a number, or a rule that fixes one** — cap it at five, one row per day. A preference is not: keep it short, don't make the default any longer, that's too wide. Whoever picks the number owns it, so a preference leaves the four lines owed in full, and almost every request voices one. Where a limit you were handed still deletes a fact found `nowhere` else, name that fact in the report anyway, the way an instruction to skip a test is honored and recorded: the limit was chosen for you, the silence was not. What the exemption never waives is the authority order: a limit that contradicts a PRODUCT.md line is a spec change, so say that and settle the spec first rather than writing the exception into it as part of the change. PRODUCT.md's CLI conventions section owns what a surface may drop; this section owns what you emit before dropping it.

**Changing what the terminal prints — layout, width behavior, color, glyphs, tables, verbosity — load `/tui-designer` first.** Nothing else fires it here, and every surface this repo has is terminal output.

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

## Git

Commit directly to `main`; do not create a feature branch. This is a solo repo whose releases are tags on `main`, so branch-and-PR adds ceremony without a reviewer. Pushing is still a separate, explicit step.

## Versioning

This section owns the **bump decision**; [DEVELOPMENT.md](DEVELOPMENT.md) owns the release mechanics and does not restate the policy.

`main.go`'s `var Version` holds the **last published** version — the Makefile and release tags derive from it. It changes **only when publishing a release**, never in a feature commit: feature work leaves it untouched and accumulates under the next version. To publish, bump `var Version` and create the matching `vX.Y.Z` tag in the same release. So a feature commit states no version; the bump decision is made once, at release, covering everything since the last tag.

Choose the bump from everything accumulated since the last tag, under pre-1.0 SemVer:

- **MINOR** (`0.Y.0`) — the release lets a caller ask something no released version could answer.
- **PATCH** (`0.y.Z`) — a fix; a refactor with no observable change; or a release that adds no new command, verb, flag, or output mode and only fills in a fact on a surface already carrying its siblings.
- **MAJOR** — reserved for the 1.0 stabilization.

**Adding fields does not by itself make a release MINOR** — capability decides, not surface area. The third PATCH case is not a judgment call, and it has two gates, both required. Adding a command, verb, flag, or output mode fails the first outright, however small the addition. Passing the second means naming the released version that already answers the question this release makes reachable; where you cannot name one, the bump is MINOR. Record the chosen bump in the release commit message, naming that earlier version whenever you used this case — the decision is otherwise invisible the moment the release ships.

## Building

When you compile-check in this repo, run `make` (or `make build`), never bare `go build` / `go install`. `make` compiles the whole module **and** installs the working-tree binary to `~/go/bin`; bare `go build` only compiles. The user tests your changes by running the global `agentry` from other projects, so a bare `go build` that skips the install leaves them testing stale behavior. See [DEVELOPMENT.md](DEVELOPMENT.md).

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
