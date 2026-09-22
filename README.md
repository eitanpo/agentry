# agentry

**AGENT ReplaY**  — render a Claude Code session log into a styled terminal view.

See [PRODUCT.md](PRODUCT.md) for scope and design rationale.

## Install

macOS (Homebrew cask):

```
brew install eitanpo/tap/agentry
```

Linux: `go install github.com/eitanpo/agentry@latest`, or download a binary from the [releases](https://github.com/eitanpo/agentry/releases).

### Shell completion

The Homebrew cask installs tab-completion automatically — nothing to do. For a `go install` or a downloaded binary, generate and load the script for your shell:

```
source <(agentry completion zsh)     # add to ~/.zshrc
source <(agentry completion bash)    # add to ~/.bashrc
agentry completion fish | source     # or: > ~/.config/fish/completions/agentry.fish
```

Completion covers the verbs and flags, the enum values of `--format`, `--level`, `--from`, `--by` and `--effort`, and — the useful one — the current project's **session ids**, each shown with its title, so you tab a UUID instead of pasting it.

## Usage

Run `agentry` from the directory you ran Claude Code in:

```
agentry                     # list this project's sessions (see below)
agentry <uuid>              # render a session by id, or any unambiguous prefix (agentry 489ce01)
agentry view                # render the most recent session (no id needed)
agentry view --from sdk     # render the most recent headless run (a hook, `claude -p`)
agentry search "a phrase"   # where inside the most recent session that phrase sits
agentry cost                # what these sessions cost, computed from their tokens
agentry config              # the defaults in effect, and the file that sets them
agentry schema              # the shape of --format json and --format jsonl output
agentry <uuid> --turn 11    # render one turn of it, not the whole log
agentry <uuid> --no-metrics # render without the footer's aggregate sections
agentry <uuid> --format json | jq  # the full session model as JSON, for piping
agentry <uuid> --format jsonl | grep '"type":"turn"'   # just the prompts, without reading the whole session
```

With no id, `agentry` lists this directory's sessions and those of any project nested under it (below); with an id it renders that one, looked up in the same set of projects, under `~/.claude/projects/` — or under `$CLAUDE_CONFIG_DIR/projects/` when you set that variable, the same one Claude Code reads to choose where to write; the id may be a full UUID or any prefix that names one session, and a prefix matching several is an error saying how many. `agentry view` with no id picks the most recent session you actually worked in, skipping headless runs — an id you name is always rendered as asked. `--from` changes which kind it picks: `--from sdk` for the last headless run, `--from all` for the last session of any kind. Asking for a kind this project has none of is an error, not a quiet fall back to another kind. The first token is a verb (`view`, `list`, `cost`, `config`) when it names one, otherwise a session id — they can't collide, since ids are hex and verbs are words. Flags may go before or after operands, and a mistyped verb, flag, or value is met with a "did you mean" suggestion rather than full help.

To find a session, list them — bare `agentry` does this, and `agentry list` is its explicit form:

```
agentry                                   # the 10 most recent sessions (bare command == list)
agentry --since today                     # list flags work on the bare command too
agentry list                              # the explicit form of the bare listing
agentry list --limit 25                   # the 25 most recent
agentry list --since today                # everything from today
agentry list --since 7d                   # the last 7 days
agentry list --since 2026-06-01 --until 2026-06-03
agentry list --include prompts            # list each session's prompts beneath its row
agentry list --include tools              # break down each session's tool calls by command/skill/agent
agentry list --include files              # every file each session modified
agentry list --include model              # what each session ran on: model and reasoning effort
agentry list --include cost               # what each session amounted to: tokens, dollars, lines changed
agentry list --include outputs            # the PRs each session opened and the artifacts it published
agentry list --include last-reply         # each session's whole final reply, rendered as in view
agentry list --model opus                 # only sessions that ran on an opus model
agentry list --model claude-opus-5        # only that one model
agentry list --effort xhigh               # only sessions run at xhigh reasoning effort
agentry list --min-lines 500              # only sessions that changed 500+ lines of code
agentry list --max-lines 0                # only sessions that recorded changing nothing
agentry list --used-command exa           # only sessions that ran a Bash command matching "exa"
agentry list --used-skill expert          # only sessions that invoked the expert skill
agentry list --used-file PRODUCT.md       # only sessions that modified that file
agentry list --used researcher            # skill, agent, or command matching "researcher"
agentry list --used-command 'git commit' --not-used-skill review   # committed without loading a skill
agentry list --opened-pr 187              # the session that opened PR 187
agentry list --all-projects --opened-pr acme-corp/build-tools --include outputs
agentry list --published-artifact cost    # sessions that published an artifact matching "cost"
agentry list --reply-matches '(?m)^\*{0,2}Learnings\b'       # sessions whose replies carried a Learnings block
agentry list --not-reply-matches 'file://' # sessions that never printed a file:// link
agentry list --used-skill expert --format json | jq   # machine-readable, for piping
agentry list --format jsonl | head -3   # the first rows, without parsing the rest
agentry list --all-projects --from all --limit all --format json \
  | jq -r 'group_by(.model)[] | "\(.[0].model) \(map(.usage.output) | add)"'   # output tokens per model, everywhere
agentry list --all-projects --limit all --format json \
  | jq -r '.[] | select(.prs) | "\(.id) \(.prs | map(.url) | join(" "))"'   # every session that opened any PR
agentry list                               # this directory and every project nested under it
agentry list --all-projects                # every project, not just this directory
agentry list --project ~/Projects/me/app   # that repo and every worktree nested in it
agentry list --project ~/Projects/me       # every repo under that directory
agentry list --from app                    # only sessions started in the desktop app
agentry list --from all                    # include headless runs, hidden by default
```

**What it cost.** `agentry cost` prices the tokens itself, so every session gets a dollar figure and
the dollars add up over any window — which Claude Code's own per-session record cannot do, holding one number per session that no day can be split out of:

To find where inside a session a passage sits, search it. Listing picks which session; this picks the place:

```
agentry search "unplaced tokens"          # the most recent session, same default as `agentry view`
agentry search "unplaced tokens" 489ce01  # a session by id or unambiguous prefix
agentry search "tally helper" --from sdk  # the most recent headless run instead
agentry search "(?-i)Agent"               # case-insensitive by default; (?-i) makes case matter
agentry search -F "compilePattern("       # the pattern as text, punctuation and all
agentry search "load the skill" --format json | jq   # machine-readable hits, for piping
```

Then render the turn it names, instead of the whole session:

```
agentry <uuid> --turn 11                  # one turn
agentry <uuid> --turn 11-14               # an inclusive span
agentry view --turn 11 --level full       # the most recent session, one turn, every body
agentry <uuid> --turn 11 --format json    # just that turn's slice of the model
```

The header keeps describing the whole session and a line under it says which turns are shown, so a
slice is never mistaken for a complete render. `--turn` narrows `--format json` and `--format jsonl`
too: `meta.numTurns` still gives the session's total and each turn carries its own `turn` number, so a
one-turn document still says which turn of how many. A start past the last turn is an error naming the
count; an end past it clamps, so `--turn 29-99` means "from 29 to the end".

Each hit is one line: the turn to go read, where inside that turn the line sits, and the whole matching
line. The middle field names the calls delegated through to reach it and then which piece matched —
`prompt`, `text`, `thinking`, `args`, `instruction` (the brief an `Agent` call handed a subagent) or
`result`, with the line's number inside that body after a colon. It searches the whole session whatever `--level` would show, so a passage you know is in the
log is never reported missing; nothing is capped and no line is cut, so pipe to `head` when a pattern
is broad. A pattern that matched nothing prints nothing and exits zero.

The pattern is a regular expression — Go's `regexp`, whose syntax is RE2, so look-around and
backreferences are parse errors rather than features you are missing a flag for. Text carrying `(`,
`[` or `.` needs `-F` to be read as itself.
A pattern containing a newline is a usage error rather than a search that can never match, a hit being
one line.

```
agentry cost                              # the three scopes, a calendar, and where the money went
agentry cost --level minimal              # the three scope rows and the notes alone
agentry cost --chart spark                # a compact daily line instead of the calendar
agentry cost --by day                     # a shaded calendar above one row per day
agentry cost --by week                    # one row per week, labelled by its Monday
agentry cost --by month                   # one row per calendar month
agentry cost --by model                   # what each model cost you
agentry cost --by agent                   # what each skill and subagent cost you
agentry cost --by project                 # what each repo cost you, worktrees folded in
agentry cost --by session                 # one row per session, priciest first
agentry cost --by day --chart line        # a braille line plot instead of the calendar
agentry cost --by week --chart none       # the table alone, no picture
agentry cost --since 30d                  # the last 30 days
agentry cost --since today --by session   # today's sessions, priciest first
agentry cost --all-projects --by month    # every project, month by month
agentry cost --project ~/Projects/me --by model
agentry cost --all-projects --by day --format json | jq   # machine-readable, for piping
```

Bare `agentry cost` answers more than the three scopes, because the sweep that finds them has already
read everything else: a **Day by day** calendar of the machine window, then **Where it went** (project
directories), **What ran it** (delegations) and **On which model**, each showing its five largest rows
and naming the remainder, and a closing line placing the last day against the window's median and top
decile. `--level minimal` prints the three scopes and the notes alone.

**Bare `agentry cost` answers three questions at once** — the session you were just in, this
folder's whole history, and everything this machine ran in the last thirty days — so the commonest
question needs no flags:

```
This session  Cost calculation per session          100M       $65.70
This folder   8 sessions, all time                  512M      $349.62
This machine  301 sessions, last 30 days            8.1B     $6049.13
```

Each row names its own window. "This session" is the newest session here that was not a headless
run, the same one `agentry view` shows; the other two count every session of every kind, because a
hook costs real money. `--by`, `--all-projects` and `--project` switch to the
single-scope table instead — a different shape, or a scope of their own. `--since`, `--until` and
`--from` narrow these same three rows rather than replacing them, so `agentry cost --since 7d` is
still all three scopes, each counting a week. The single-scope table opens its footer with what it
priced, since a line reading `Total` names no scope.

The figure is **an estimate, not a bill**: it prices the responses the transcript records, at the
same rates and by the same formula Claude Code uses. The two totals agree closely, and a per-session gap runs both ways — low where Claude Code paid for a background request
it wrote no entry for, high where its own record covers only part of the session. Rates come from one table stamped with the date it was verified against
published prices, and `--format json` carries that date. Web search, geography-pinned inference, and
an organization's negotiated rates are not priced. A model with
no price is named rather than counted as free. Every row's tokens are attributed to the local
calendar day of the response that spent them, so a session running past midnight splits across both
days, and the rows always sum to the total — there is no `--limit` to make them not. In the
single-scope table, headless sessions are left out by the same default the listing applies and
`cost` says on stderr how many it did not price, since a total that quietly omitted them is just
lower than your bill; the three-row summary counts them in its folder and machine figures.

**A roll-up says what the dollars bought, not just what they were.** Every axis except `model` and
`agent` carries `Turns`, `Active` and `$/turn` columns, and two lines under the total: what a turn
and an active hour cost, then the median cost per turn across the sessions in the window and the
level the top tenth sits above. Both are there because one average hides the shape. Active time runs from each prompt to the last thing
the assistant said in that turn, with any silence over five minutes counted as five minutes — the
log cannot tell a long test run from a person who walked away. A session's wall-clock span is never divided by, and cost per line changed is not offered
at all, because Claude Code records line counts for a minority of sessions.

**A rendered session ends with a footer saying what it touched, produced and spent.** After the last
turn come five sections, each shown only when it has something: `Files` (every file the session
modified), `Outputs` (each pull request it opened and artifact it published, clickable on a
terminal), `Tools (by identity)` (which skills, agents and commands ran, how often, and which of them failed or were refused),
`Cost` (what the session's dollars went to), `Summary (by token cost)` (the per-turn table),
`Day by day` (turns, active time and cost per day, on a session that ran across more than one) and
`Session`. None of them is gated on `--level`, so a bare `agentry <uuid>` shows all seven;
`--no-metrics` drops the four aggregates and keeps the files, the outputs and the closing card.

**Every session gets a dollar figure, and you can tell it from Claude Code's own.** Claude Code records a cost for some sessions; agentry prices the rest from their tokens, the same way
`agentry cost` does. A recorded figure prints as `$12.50` and a computed one as `~$12.50`, and the
`Cost` section splits the computed total by model, by subagent, per turn, per active hour, and by
what caching saved. Long lists are capped and name what they left out — the full ones are
in `--format json`. These are the same facts `list --include files`, `--include tools` and
`--include outputs` read.

**The last section names the session, so you can act on what you just read.** `Session` closes the
render with the session's id, its title, the directory it ran in, the conversation root it shares
with any fork of itself, the log file it was read from, its counts and its spend, then the two
commands that reach it again —
`agentry <id>` to re-render it and `claude --resume <id>` to pick it back up. Before this, a render
named the session nowhere: you had to go back to a listing to find the id of the session you were
looking at.

**The header says what the session was, the footer what it did.** The box above the transcript gives
each fact a line: when the session ran, with its **active time** rather than the span from first entry to last — a session's wall-clock span can run many times its active time when it sits idle between turns — then what it ran on, then its turns, tools, subagents, failed calls and
refused calls, keeping a failure and a refusal apart because they ask for different things, then
what it spent. A line too long for the box wraps inside it, so a narrow terminal costs you a line
rather than a field.

**Headless sessions are hidden unless you ask for them.** Anything non-interactive — a `claude -p`
from a script, a hook, a CI step — writes a session log like any other, and on a machine that uses
hooks these outnumber the ones you typed. They are excluded by default so a listing shows work you
did; `--from sdk` shows only those, `--from all` shows everything. When a listing spans more than
one kind, each row gains a 3-letter tag: `cli` (terminal), `app` (desktop), `sdk` (headless), and
`cli+` for a session that started in one and was resumed in another.

**A listing covers this directory and everything nested under it.** That matters because Claude
Code gives every git worktree its own project folder, so a repo's sessions are split across them —
standing in the main checkout lists the worktrees' sessions too. `--project PATH` applies the same
subtree rule from a root you name instead, and `--all-projects` covers every project there is. All
three reach projects whose directory you have since deleted or renamed, which walking directories
yourself cannot. When a listing spans more than one project, each row gains a project column before
the title, labelled by the repository — a worktree's sessions carry the repo's name, not the
worktree's, so one repo reads as one project. Inside a single repository that slot carries a
worktree column instead, naming which worktree each session ran in (`—` for the repo's own
checkout); it appears only when the sessions span more than one. `--format json` carries the full
path as `cwd` on every session. Rendering follows the
same scope, so an id copied off a listing opens where you read it — no `cd` into the worktree
first — and `agentry view` with no id reaches the whole subtree too, picking the most recently
written session file rather than the current directory's.

Sessions print oldest-to-newest, so the most recent is at the bottom, next to your prompt. Each row shows the last-activity time (when the session's most recent turn ended — the same recency the list is ordered by), **active time** — how long its turns ran, not the span from its first entry to its last, which can run considerably longer — turn count, a title (a name you chose if set — from renaming the session, or from `--name` / `/rename`, whichever the log records last — else Claude Code's own `ai-title` summary, falling back to the first prompt, skipping a leading `/clear` or a shell command typed with `!`), and its id, shortened to the shortest prefix unique among the rows and never under 8 characters — copy it and pass it to `agentry <id>` to render that session. `--format json` keeps every id in full. A forked session (Claude Code's `--fork-session` / `/branch`) is grouped under the original it was forked from and its title indented with `└─`; while it still carries the original's inherited title it is shown by its first new prompt instead, so the two are distinguishable. A title that just repeats the row's worktree — what you get when one argument names both the worktree and the conversation, as `devx -n plan -w` does — is replaced by the session's first prompt for the same reason: the worktree column already shows it.

### Options

| Flag | Mode | Default | Description |
|---|---|---|---|
| `--turn N\|N-M` | render | — | Render one turn, or an inclusive span, instead of the whole transcript. The numbers are the ones `agentry search` prints and a rendered session counts by, so a hit's turn number goes straight in. The header still describes the whole session, with a line beneath it naming the span shown. It narrows `--format json` and `--format jsonl` as well, where `meta.numTurns` and each turn's own `turn` number keep the slice legible. A start past the last turn errors and names the count; an end past it clamps. Not valid on a bare listing, which has no transcript to slice — the rule every render flag follows. |
| `--level minimal\|standard\|detailed\|full` | render | `minimal` | Preset of channel defaults. Passing this or any channel toggle to a bare listing — `agentry` with no id — is an error naming the flags, since a listing has no transcript to shape; they were silently ignored there until this version. `minimal` prompts+response; `standard` +thinking+metrics; `detailed` +tools+subagents (no output); `full` +tool-results. On `cost` the flag takes `minimal` or `standard` only, and defaults to `standard`: `minimal` prints the three scope rows and the notes alone, `standard` adds the calendar and the three breakdowns. The default differs by verb because the commonest question does. |
| `--[no-]thinking\|tools\|tool-results\|subagents\|metrics` | render | — | Override a single channel on top of `--level` (adds or subtracts). `tools` = a tool fired; `tool-results` = its output, and on an `Agent` call the instruction it was handed — printed whole, above the result or the expanded subagent stream, behind the `❯` glyph a typed prompt carries. A result body is cut to its first ten lines with the remainder named (`… 38 more lines`), a single result being unbounded machine output; `--format json` carries it whole. An `Agent` line also names what it delegated to: `Agent[Explore@haiku]` is the subagent type and the model, and the `@model` half is absent when the call left the subagent on the session's model. |
| `--limit N\|all` | `list` | `10` | Cap to N most-recent; `all` removes the cap (`0` also still does, being what the flag took before `all` existed). `all` rather than a sentinel number because `--from` and `--include` already spell an exhaustive value that way, and because `0` means literally zero on `--max-lines`. The default caps a **bare** listing only: any flag that selects which sessions appear lifts it, while `--include` and `--format` leave it in force since they shape rows rather than choose them. `--format json` is never capped by default, because a capped array is indistinguishable from a complete one. An explicit `--limit` always wins. Whenever a cap does hide sessions, the count and the way to see them go to stderr, below the table and after a blank line — `agentry: 672 more session(s) hidden by --limit — pass --limit all to list them` — so the table on stdout stays line-oriented and the notice reads as the listing's last word. |
| `--since WHEN`, `--until WHEN` | `list`, `cost` | — | Filter by last-activity time. WHEN: `today`/`yesterday`, `Nh`/`Nd`/`Nw`, or `YYYY-MM-DD`. On `cost` the bounds are compared against each day of spend instead, so a session straddling the edge contributes only the days inside the window. |
| `--include CHANNELS` | `list` | — | Add per-session detail. Comma-separated; channels: `prompts`, `tools`, `files`, `model`, `cost`, `outputs`, `last-reply` (or `all`). `tools` breaks down a session's top-level tool calls grouped by identity — Bash by program, Skill by name, Agent by subagent type, Edit/Write by target file, everything else by tool name — and adds a `Denied` line naming the calls that were refused and by what (`permission-rule`, `automode-blocked`, `automode-unavailable`, `user-rejected`), which an error glyph alone cannot tell you. `files` lists every file the session modified by any means, from Claude Code's own file-history record rather than from tool arguments. `model` names what the session ran on — its model and reasoning effort, in the rendered header's phrasing — which is otherwise invisible in the text table. `cost` names what it amounted to — the token tally, then the dollar total and the lines added and removed that Claude Code recorded — in the rendered header's exact wording, and is the only place the text table states any of them; the recorded halves are absent on any session whose log carries no cost record, which is every session before Claude Code 2.1.241 and many since, and the line counters are dropped again on a session that changed nothing. `outputs` lists what the session produced beyond its transcript: one line per pull request it opened (its URL) and per artifact it published (title, then `claude.ai` URL), deduplicated, since Claude Code re-records both on later turns. `last-reply` shows the session's whole final assistant reply under its row, laid out as `agentry view` lays out a reply — the `◆` glyph on its own line, then the markdown rendered by the same helper, so what a reply says is the same on both. The block is narrower than a full render, so long lines wrap at different columns. It is the only channel that shows part of what it names rather than all of it, hence the name, and by far the longest, so reach for it when you want the answers rather than a scan. |
| `--used-tool NAME` | `list` | — | Only sessions where that tool fired, by tool-use name (case-insensitive, exact). The "which mechanism" axis. |
| `--used-skill`, `--used-agent`, `--used-command` | `list` | — | Identity axis: a Skill's skill, an Agent's subagent type, a Bash command's text (case-insensitive substring). |
| `--used-file PATH` | `list` | — | Only sessions that modified a matching file (case-insensitive substring, so `list.go` catches every directory's and `internal/cli/list.go` names one). Reads `Edit`/`Write` targets and the tracked-file record together; the tool targets do nearly all the work, since many sessions have no tracked-file record at all. Not covered by `--used`. |
| `--used TOKEN` | `list` | — | Catch-all over the identity axis: skill name, agent type, or command. Not tool names — use `--used-tool` for those. |
| `--opened-pr TEXT` | `list` | — | Only sessions that opened a matching pull request, over its repository, number, and URL (case-insensitive substring, so `build-tools` selects a repository's worth and `187` picks one). Read from Claude Code's own `pr-link` record, which is written for the session as a whole — so this finds a PR opened inside a subagent, which `--used-command 'gh pr'` cannot. |
| `--published-artifact TEXT` | `list` | — | Only sessions that published a matching artifact, over its title, its `claude.ai` URL, and the local file it was rendered from (case-insensitive substring). Same session-level record as `--opened-pr`. |
| `--reply-matches PATTERN` | `list` | — | Only sessions whose assistant reply text matched. PATTERN is a **regular expression** (RE2), case-insensitive by default (`(?-i)` to override) — the one filter here that is not a substring match, because prose questions are positional and alternation-shaped. Tested against each assistant text block separately, so `^`/`$` anchor to one reply: `'(?m)^\*{0,2}Learnings\b'` finds the block, where the substring `Learnings` would also hit every session that merely mentioned it. Thinking blocks and subagent sidecars are not read. An unparseable pattern is a usage error, and `-F` reads PATTERN as text instead. Reply text is matched but never printed or serialized — see the `--format json` note in [PRODUCT.md](PRODUCT.md) for the size reason. |
| `-F`, `--fixed-strings` | `search`, `list` | off | Read the pattern as text, not as a regular expression — `agentry search -F "compilePattern("` finds that call instead of erroring on the unclosed group. Matching stays case-insensitive; the flag chooses how the pattern is read, not how it is compared. On `list` it governs `--reply-matches` (and its `--not-` twin), and passing it to a listing that gave no pattern filter is a usage error rather than a flag silently doing nothing. |
| `--not-used-*`, `--not-opened-pr`, `--not-published-artifact`, `--not-reply-matches` | `list` | — | Every filter in this family has a `--not-` twin (`--not-used-tool`, `--not-used-skill`, `--not-used-agent`, `--not-used-command`, `--not-used-file`, `--not-used`, `--not-opened-pr`, `--not-published-artifact`, `--not-reply-matches`) keeping the sessions the positive one drops. Combine the two for a compliance audit: `--used-command 'git commit' --not-used-skill review`, or count the misses of a reply rule with `--not-reply-matches`. For the `--used*` flags, absence is judged over top-level calls only, so a subagent may have used what the main thread did not; the two output filters read session-level records and carry no such gap. |
| `--model NAME` | `list` | — | Only sessions that ran on a matching model (case-insensitive substring, so `opus` covers `claude-opus-5` and `claude-opus-4-8` while `claude-opus-5` picks one). Matches any model the session carried, so one that switched mid-way matches both. |
| `--effort LEVEL` | `list` | — | Only sessions run at that reasoning effort (`low`, `medium`, `high`, `xhigh`, `max`). Case-insensitive and **exact**, unlike the substring filters — the levels nest, so `high` must not quietly include `xhigh`. Unknown levels return nothing rather than erroring, since the set grows. |
| `--min-lines N` | `list` | — | Only sessions that changed at least N lines — additions plus removals, from Claude Code's own record. Inclusive. A session whose log carries no such record matches no bound, which is most sessions: the record exists only from Claude Code 2.1.241 and not on every session since. A negative value is a usage error. |
| `--max-lines N` | `list` | — | Only sessions that changed at most N lines, on the same rule. `--max-lines 0` selects the sessions that recorded changing nothing — never the ones with no record. Combined with `--min-lines` it makes a range, and a floor above the ceiling is a usage error rather than an empty listing. |
| `--by total\|day\|week\|month\|model\|agent\|project\|session` | `cost` | `total` | Which bucket the dollars are added up in. Time buckets print oldest first; `model`, `agent`, `project` and `session` print priciest first. Days begin at local midnight and weeks at Monday. `agent` groups by what the tokens were delegated to — a subagent type, a forked skill with its leading slash, or `(main thread)`; a delegation that delegates again is charged to the call you made, so a skill's row is what invoking it cost in full. `project` groups by the directory each session ran in, read from the log, cut at its first hidden segment — so a repo's worktrees under `.claude-worktrees` count with the repo while a sibling repo does not. Every row carries a bar for its share of the total, drawn from block characters so it survives `NO_COLOR` and a pipe; it is the last column and the first one dropped on a narrow terminal. There is no `--limit`, so the rows always account for the total line. |
| `--chart auto\|none\|spark\|line\|calendar` | `cost` | `auto` | Which picture is drawn above the rows. `auto` draws one wherever the buckets are a span of time — a calendar for `--by day`, a sparkline for `--by week` and `--by month`, none for the axes with no time order — and a calendar in the summary's Day by day section, where `spark` asks for the compact line under the machine row instead. The rows, total and notes are always kept; `none` prints the table alone. `line` is a braille plot at four times the horizontal resolution of block characters; `calendar` shades each square by which quarter of the spending days it falls in, and on a roll-up needs `--by day`. A picture with too few buckets, or a terminal too narrow for it, is simply absent. Rejected, naming both flags, for an explicit picture on an axis with no time order and alongside `--format json`. |
| `--all-projects` | `list`, `cost` | — | Every project under the projects root (`~/.claude/projects/`, or `$CLAUDE_CONFIG_DIR/projects/` when set), not just this directory's. Mutually exclusive with `--project`. |
| `--project PATH` | `list`, `cost` | — | PATH's sessions instead of this directory's, including every project nested under PATH. It moves the root, not the depth — a listing already covers what is nested under the current directory, which is how standing in a repo picks up its git worktrees. |
| `--from cli\|app\|sdk\|all` | `list`, `view`, `search`, `cost` | `cli`+`app` | Where the session was run. `sdk` is anything non-interactive (`claude -p`, a hook, CI) and is **hidden by default**; `all` restores it. On `view` and `search` (no id) it picks which kind the most-recent lookup walks back to; it cannot be combined with a session id. |
| `schema [verb\|record]` | — | — | Print the shape of the machine-readable output: every `--format json` document, every `--format jsonl` record type, and the envelope those lines carry. Read off the Go types the emitters pass, so it cannot drift from what you receive. Bare prints everything; an argument narrows to one verb (`agentry schema view`) or one record type (`agentry schema hit`). Each key prints with its type, its nesting, and whether it is absent rather than empty. `--format json` emits the same description as data. |
| `--format json\|jsonl\|text` | render, `search`, `list`, `cost`, `config`, `schema` | `text` | `json` emits machine-readable output for piping. On the render path it's the full session model (`meta` + `turns`, ignoring `--level`/channels and color but honoring `--turn`, which is a selector rather than a view), with `meta.effort` beside `meta.model`, `meta.prs` and `meta.artifacts` carrying what the session produced, and each tool call carrying the `identity` that `list --include tools` groups by plus the `model` an `Agent` call delegated to and the `prompt` it delegated, whole; on `list` it's a JSON array of per-session summaries, each carrying its `cwd`, the `files` it modified as absolute paths, its `denials`, its `model` and `effort`, its `usage`, `dailyUsage` (that same tally split per model per local day, which is what `agentry cost` buckets), `costUSD`, `linesAdded` and `linesRemoved` — the token tally over the main thread and every subagent, plus Claude Code's own dollar and line totals where the log recorded them, the same number the render path reports as `meta.usage`, which is what makes a cross-project cost tally one call instead of one render per session — and its outputs, `prs` (`{repository, number, url}`) and `artifacts` (`{title, url, path}`) (ignoring `--include` and color), and stdout is always a valid array — a directory with no project, or a project with no sessions, prints `[]` while still reporting the error on stderr and exiting non-zero, so you can pipe into `jq` without a guard. On `search` it is an array of hits, each carrying `turn`, `delegation`, `part`, `tool`, `identity`, `model`, `line` and `text`, and always well-formed — `[]` where nothing matched. On `cost` it is one object — `by`, `buckets`, `total`, `recorded`, `unpricedModels`, `pricesVerified` — emitted even when nothing matched. `jsonl` emits the same facts one JSON value per line, for large sessions, streaming consumers, and merging two sessions with `cat`. Each line is an envelope — `type`, `tool`, `sessionId`, `ordinal`, `time`, `payload`. The render path emits a `meta` record, a `turn` record per turn carrying its prompt, and an `event` record per event, with a subagent's events following their tool call at one greater `depth` rather than nested inside it; `list` emits one `session` record per row; `search` emits one `hit` record per match, whose envelope `sessionId` says which session it came from; `cost` emits a `report` (or `overview`) header, then `bucket` (or `scope`) records, then a `total`. `jq -s .` turns a stream back into the document `json` would have given you. Run `agentry schema` for the keys of any of these without reading this table. |
| `--init` | `config` | — | Write a starter settings file at the path `agentry config` prints, every key at its built-in default. Refuses rather than overwrites when a file is already there, exiting 73. Leaves out `from`, whose default no single value expresses, and says so on stderr. Rejected alongside `--format json` or `jsonl`, naming both flags, since it writes a file rather than the report those shape. |
| `--no-color` | global | — | Disable color (also honors the `NO_COLOR` env var). |
| `--help`, `--version` | global | — | Per-verb `--help` lists only that mode's flags. |

Bare `agentry` is the listing, so the "`list`" flags apply to it as well as to `agentry list`; `cost` takes the selection and scope flags marked for it and none of the detail ones; the "render" flags apply to `agentry <uuid>` and `view`; "global" flags work anywhere.

### Configuration

Every default in the table above can be set once in a file instead of retyped on every run. The file is optional and there is one per machine, at `$XDG_CONFIG_HOME/agentry/config.json` — `~/.config/agentry/config.json` when `XDG_CONFIG_HOME` is unset, empty, or a relative path:

```json
{
  "format": "text",
  "from": "all",
  "view": { "level": "detailed" },
  "list": { "limit": 25 },
  "cost": { "by": "day", "chart": "line", "level": "minimal" }
}
```

Keys are flag names. A flag that means one thing everywhere (`format`, `from`) is a top-level key; a flag that belongs to one verb, or that means different things on different verbs, sits in that verb's section — `--level` takes four values on `view` and two on `cost`, which is why the sections exist. The bare command reads `view`'s section when it renders and `list`'s when it lists, exactly as its flags do. `"limit"` takes a JSON number or `"all"`, the two forms `--limit` takes.

Only flags with a default in the table above have a key. The selectors — `--since`, `--project`, `--model`, `--min-lines`, the `--used*` family — deliberately have none: a persisted filter would make a bare listing hide sessions for a reason nothing on screen explains.

A flag you pass beats the file, and the file beats the built-in default. The file sets what a flag *defaults to*, so rules about a flag being passed are unchanged: a selector still lifts the listing's cap, whether that cap came from the file or from the built-in ten, and `--format json` is still never capped. Pass `--limit` explicitly to hold your cap alongside a selector.

`NO_COLOR` and `CLAUDE_CONFIG_DIR` keep working and are not config keys — the first is a property of the terminal, the second says where Claude Code's logs live rather than how they are shown, so the settings file is deliberately not stored under it.

```
agentry config                  # every setting: its value, where it came from, and what it accepts
agentry config --init           # write a starter file with every key at its default
agentry config --format json    # the same report, for scripting
```

`agentry config` is the key reference as well as the report — each row names what that key accepts, spelled the way the flag's help spells it, so a JSON file that cannot carry comments does not have to explain itself. `--init` writes the file for you at the path above, refusing rather than overwriting when one is already there (exit 73). It leaves out `from`: that key's default is two entrypoints at once and `--from` has no token meaning both, so any value written there would quietly change what you see; it tells you so on stderr.

A file that cannot be parsed, an unknown key, or a value its flag would reject stops the run with a message naming the file and the key, and exits 78 (`EX_CONFIG`) — not the usage code, since nothing on your command line was wrong. An unknown key gets the same "did you mean" suggestion a mistyped flag gets; it is never skipped silently. `agentry --help` and `agentry --version` keep working while you fix it.

Markdown-file export, content search, and an interactive browser are planned — see the roadmap in [PRODUCT.md](PRODUCT.md).

## Development

Go + [Charm](https://charm.sh) (Glamour, Lip Gloss). Released via GoReleaser to a Homebrew tap. Build, test, and install workflow: [DEVELOPMENT.md](DEVELOPMENT.md).

## License

MIT — see [LICENSE](LICENSE).
