// Package model is the canonical in-memory representation of a Claude Code
// session. The parser produces it, the renderer consumes it, and `--format
// json` serializes it. It carries no presentation concerns.
package model

import (
	"encoding/json"
	"strconv"
	"time"
)

// Session is a fully parsed session log.
type Session struct {
	Meta  Meta   `json:"meta"`
	Turns []Turn `json:"turns"`
}

// Meta is session-level metadata aggregated across all turns and subagents.
type Meta struct {
	ID string `json:"id"`
	// Model is the model the session ran on, resolved like Entrypoint: the last
	// value the log carries. Models lists every distinct one, set only when the
	// session switched mid-way. Empty on a session whose log names none, which is
	// not a claim that it ran on nothing — the log simply does not say.
	Model  string   `json:"model,omitempty"`
	Models []string `json:"models,omitempty"`
	// Effort is the reasoning effort the model ran at, resolved like Entrypoint:
	// the last value the session carries. Efforts lists every distinct one, set
	// only when it changed mid-session. Empty on a session predating the field
	// (about half of them), which is not a claim that effort was any particular
	// value — the log simply does not say.
	Effort  string    `json:"effort,omitempty"`
	Efforts []string  `json:"efforts,omitempty"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Usage   Usage     `json:"usage"`
	// CacheSaving mirrors the Summary field of the same name, computed from the
	// same responses, so a saving read off a rendered session and one read off a
	// listing cannot differ. Nil on a session none of whose responses agentry
	// holds a price for, which is not a claim that caching saved nothing.
	CacheSaving *CacheSaving `json:"cacheSaving,omitempty"`
	// CostUSD is what Claude Code recorded the session as having cost, mirroring
	// the Summary field of the same name. A pointer because a free session and a
	// log that records no cost are different facts and both would marshal as zero;
	// nil is omitted, so a caller aggregating the field sees the gap.
	CostUSD *float64 `json:"costUSD,omitempty"`
	// LinesAdded and LinesRemoved mirror the Summary fields of those names. They
	// come from the same record CostUSD does, so all three are present together or
	// all absent.
	LinesAdded   *int `json:"linesAdded,omitempty"`
	LinesRemoved *int `json:"linesRemoved,omitempty"`
	NumSubagents int  `json:"numSubagents"`
	// Entrypoint and Entrypoints mirror the Summary fields of the same names,
	// resolved identically, so the render path and the listing never disagree
	// about where one session ran.
	Entrypoint  string   `json:"entrypoint,omitempty"`
	Entrypoints []string `json:"entrypoints,omitempty"`
	// PRs and Artifacts likewise mirror the Summary fields of those names, so a
	// rendered session and a listing never disagree about what one session
	// produced.
	PRs       []PR       `json:"prs,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Summary is a lightweight session descriptor for listing: enough to identify
// and choose a session without parsing its full turn stream.
type Summary struct {
	ID       string     `json:"id"`
	Start    time.Time  `json:"start"`
	End      time.Time  `json:"end"`
	Title    string     `json:"title"`             // chosen title (ai-title, else first non-/clear prompt)
	Prompts  []string   `json:"prompts,omitempty"` // user prompts in order, /clear omitted (for --include prompts)
	NumTurns int        `json:"numTurns"`
	Tools    []ToolStat `json:"tools,omitempty"`    // top-level tool calls aggregated by identity (for --include tools)
	Commands []string   `json:"commands,omitempty"` // distinct top-level Bash commands (for --used-command / --used)
	// Replies is every non-blank assistant text block the main thread wrote, in
	// order — the corpus --reply-matches tests. Thinking blocks are excluded:
	// reasoning is not a reply.
	//
	// Deliberately not serialized, alone among the content fields. Reply text is
	// the largest thing a session holds — 2.8 MB across one local project's 59
	// sessions, against 776 KB for that project's whole 50-session JSON listing —
	// and --include gates no JSON key, so carrying it would quadruple every
	// listing for every caller. The filter reads it in memory; a caller who wants
	// the prose renders the session, whose JSON carries every text event in full.
	Replies []string `json:"-"`
	// RootUUID is the uuid of the session's first content entry — the
	// conversation root. A fork copies its parent's chain verbatim, so a fork and
	// its parent share a RootUUID; the listing groups them into one fork family.
	RootUUID string `json:"rootUuid,omitempty"`
	// Cwd is the working directory the session ran in, read from the log rather
	// than derived from the project folder's name, which is lossy. It is what
	// distinguishes rows once a listing spans more than one project.
	Cwd string `json:"cwd,omitempty"`
	// Entrypoint is where the session was run, as the log's own value ("cli",
	// "claude-desktop", "sdk-cli"). A session resumed elsewhere carries more than
	// one; this is the last, matching the last-activity time the row is ordered by.
	Entrypoint string `json:"entrypoint,omitempty"`
	// Entrypoints is every distinct value in first-seen order, set only when the
	// session carries more than one. The text table compresses that to a "+"
	// suffix, so this is where the divergence survives intact.
	Entrypoints []string `json:"entrypoints,omitempty"`
	// Files is every file the session modified, as an absolute path in first-seen
	// order. Read from Claude Code's own file-history entries rather than from
	// tool arguments, so it covers a file changed by a shell command as well as
	// one edited by a tool. Empty for a session whose log carries no such entries,
	// which is not a claim that nothing changed.
	Files []string `json:"files,omitempty"`
	// Denials groups the calls that were refused rather than run. Separate from
	// Tools because a denial is an outcome, not another call: the same (tool,
	// identity) pair can appear in both.
	Denials []DenialStat `json:"denials,omitempty"`
	// Model/Models and Effort/Efforts mirror the Meta fields of the same names,
	// resolved identically, so a listing and a rendered header never disagree
	// about what one session ran on.
	Model   string   `json:"model,omitempty"`
	Models  []string `json:"models,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
	// Usage is the session's token tally over the main thread and every subagent
	// it spawned — the same total Meta.Usage carries, so a cost read off a listing
	// and one read off a rendered session agree. Paired with Model, it is what
	// lets a single cross-project listing answer what a model cost.
	Usage Usage `json:"usage"`
	// CostUSD is Claude Code's own running dollar total for the session, read from
	// the log's last record of it rather than derived from Usage. Nil on a session
	// whose log carries none (anything before Claude Code 2.1.241), which is not a
	// claim it was free — DailyUsage is what puts a figure on such a session, at
	// agentry's own prices and as an estimate. Costs no sidecar read: Claude Code
	// prices every request a session's process makes into one ledger, subagents
	// included, so there is nothing here to add and adding would double count.
	CostUSD *float64 `json:"costUSD,omitempty"`
	// LinesAdded and LinesRemoved are how much code the session changed, from the
	// same record CostUSD comes from — so all three are present together, and a
	// zero here is a session that changed nothing rather than one nothing was
	// recorded for. Unlike CostUSD this is known to reach delegated work: a local
	// session whose main thread made no edit call, and whose subagents made 29,
	// recorded 22 lines added.
	LinesAdded   *int `json:"linesAdded,omitempty"`
	LinesRemoved *int `json:"linesRemoved,omitempty"`
	// DailyUsage splits Usage by model and by the local day each response was
	// spent on, so a cost roll-up can bucket a session's spend by day without
	// re-reading its log. Summing every entry's Usage reproduces Usage exactly —
	// same responses, same deduplication, only grouped — so the two cannot
	// disagree about what one session spent.
	DailyUsage []DailyUsage `json:"dailyUsage,omitempty"`
	// CacheSaving is DailyUsage priced twice — as billed, and with nothing cached
	// — so a caller can say what caching took off this session without pricing the
	// tally itself. Priced per model from the split rather than from Usage, since
	// the rates are not proportional across models. Nil on a session none of whose
	// responses agentry holds a price for.
	CacheSaving *CacheSaving `json:"cacheSaving,omitempty"`
	// DailyActivity splits the session's turns and the time they ran for by the
	// local day each turn started on, so a cost roll-up can divide a bucket's
	// dollars by the work done in that same bucket. Summing every entry's Turns
	// reproduces NumTurns exactly.
	DailyActivity []DailyActivity `json:"dailyActivity,omitempty"`
	// PRs and Artifacts are what the session produced beyond its own transcript,
	// each deduplicated in first-seen order (Claude Code re-records both on later
	// turns). They come from entries Claude Code writes for the session as a whole,
	// so unlike Tools they are not limited to the main thread's calls.
	PRs       []PR       `json:"prs,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	// Born is the session file's creation time, used to order a fork family
	// (earliest = original). Filesystem metadata, not session content, so it is
	// not serialized. Zero when unreadable; off macOS it falls back to mtime.
	Born time.Time `json:"-"`
}

// ToolStat counts the top-level tool calls in a session that share a tool name
// and identity, for `agentry list --include tools`. Identity is the call's
// grouping label: the invoked program for Bash, the skill for Skill, the
// subagent type for Agent; empty for tools whose name is their own identity
// (Edit, Read, WebFetch, …). Top-level only — calls made inside subagents are
// not counted, matching Turn.ToolCount.
type ToolStat struct {
	Tool     string `json:"tool"`
	Identity string `json:"identity,omitempty"`
	Count    int    `json:"count"`
}

// DenialStat counts the top-level calls refused for one reason, grouped the way
// an auto-allow decision is made: by what refused them, then by which call. Kind
// is the log's own toolDenialKind — "permission-rule", "automode-blocked",
// "automode-unavailable", or "user-rejected" — and never a generic failure, so a
// call that ran and errored is absent here.
type DenialStat struct {
	Kind     string `json:"kind"`
	Tool     string `json:"tool"`
	Identity string `json:"identity,omitempty"`
	Count    int    `json:"count"`
}

// PR is a pull request a session opened, from Claude Code's own pr-link record.
type PR struct {
	Repository string `json:"repository,omitempty"`
	Number     int    `json:"number,omitempty"`
	URL        string `json:"url,omitempty"`
}

// Ref names a pull request the way a person does: "owner/repo#14". Degrades to
// whichever half the record carries, and is empty when it carries neither.
func (p PR) Ref() string {
	switch {
	case p.Repository != "" && p.Number > 0:
		return p.Repository + "#" + strconv.Itoa(p.Number)
	case p.Repository != "":
		return p.Repository
	case p.Number > 0:
		return "#" + strconv.Itoa(p.Number)
	}
	return ""
}

// Key identifies the pull request: its URL, or the "owner/repo#14" reference when
// the record carries no URL. It is both what deduplication groups by and what a
// text view prints, so a listing cannot name one pull request two ways. Empty
// when the record names nothing at all, which is the signal to drop it.
func (p PR) Key() string {
	if p.URL != "" {
		return p.URL
	}
	return p.Ref()
}

// Artifact is a page a session published, from Claude Code's own frame-link
// record. Title is optional — a third of observed records carry none. Path is the
// local file the page was rendered from, which can move between publishes while
// the page keeps its URL.
type Artifact struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	Path  string `json:"path,omitempty"`
}

// Key identifies the artifact: its published URL, falling back to the local file.
// It must be the URL rather than the path, because republishing from a moved file
// keeps the URL and changes the path — keying on the path would report one
// artifact as two.
func (a Artifact) Key() string {
	if a.URL != "" {
		return a.URL
	}
	return a.Path
}

// Usage is a token tally. Cache fields mirror the Anthropic usage object.
type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheCreate int `json:"cacheCreate"`
	// CacheCreate1h is the share of CacheCreate bought for an hour rather than
	// five minutes — a subset of it, not a fifth counter, so a caller summing the
	// tally must not add this one in. It is tracked because the two cost different
	// rates, an hour of cache being twice the input rate against 1.25 times it for
	// five minutes, and the flat counter the log leads with hides which: 59% of the
	// local corpus's cache-creation tokens were hour writes when measured
	// 2026-09-11, so pricing all of them at the five-minute rate undercounts cache
	// writes by close to a fifth.
	// Zero on a log that carries no split, which prices as five-minute writes.
	CacheCreate1h int `json:"cacheCreate1h"`
}

// CacheSaving is one session's responses priced twice: at the rates they were
// billed at, and with every token that was read from cache or written to it
// charged as fresh input instead. Caching does not change how many tokens a
// request sends, only the rate the resent context is charged at, so the
// difference between the two is what caching took off the bill.
//
// The two prices are carried rather than the share between them because a share
// of one session cannot be added to a share of another, where these sum across
// any set of sessions and the share is taken afterwards.
type CacheSaving struct {
	WithCacheUSD    float64 `json:"withCacheUSD"`
	WithoutCacheUSD float64 `json:"withoutCacheUSD"`
}

// SavedUSD is what caching took off the bill.
func (c CacheSaving) SavedUSD() float64 { return c.WithoutCacheUSD - c.WithCacheUSD }

// SavedShare is that saving as a fraction of the uncached price. Zero where the
// uncached price is zero, which is the one case the division has no answer for
// and which describes a session that spent nothing either way.
func (c CacheSaving) SavedShare() float64 {
	if c.WithoutCacheUSD <= 0 {
		return 0
	}
	return c.SavedUSD() / c.WithoutCacheUSD
}

// Add accumulates another tally into this one.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheCreate += o.CacheCreate
	u.CacheCreate1h += o.CacheCreate1h
}

// DailyUsage is one model's tokens on one local calendar day — the finest grain
// a cost roll-up prices and buckets by. Day is a local date (2026-09-11) rather
// than an instant: the bucket boundary is local midnight, and a date states that
// where a timestamp would invite a second reading of it in another zone.
//
// The model belongs in the key because rates differ per model, and the day
// because a session's spend is attributed to the day each response was spent on
// rather than to the day the session ended — 29 of the 69 locally recorded
// sessions run past a midnight, and Claude Code's own per-session record has no
// form that could be split across one.
type DailyUsage struct {
	Day   string `json:"day"`
	Model string `json:"model,omitempty"`
	// Agent is what the tokens were delegated to — a subagent type, or a forked
	// skill written with its leading slash. Empty for the main thread, which is
	// the majority of every session and is named rather than labelled so the
	// agent axis sums to the session's total instead of to its delegated part.
	Agent string `json:"agent,omitempty"`
	Usage Usage  `json:"usage"`
}

// DailyActivity is how much a session did on one local calendar day: the turns
// that started that day, and the seconds those turns ran for.
//
// Turns are bucketed by the day each one started rather than by the session's
// own day, which is what lets a day bucket divide that day's cost by that day's
// turns. Nothing here is per model or per agent — one turn can run on two models
// and delegate to two agents — so those two axes carry no turn count at all.
//
// ActiveSeconds sums each turn's span, from the prompt to the last entry that
// turn wrote. It excludes the gap between turns, which is what separates it from
// a session's wall span: the local corpus holds 5,324 session-hours inside a
// 720-hour month, because a session sits open across days. It does count a turn
// that waited on a slow tool or on a permission prompt, since the log cannot
// tell waiting from working.
type DailyActivity struct {
	Day           string `json:"day"`
	Turns         int    `json:"turns"`
	ActiveSeconds int    `json:"activeSeconds"`
}

// Turn is one user prompt and the assistant activity that followed it.
type Turn struct {
	Prompt     string    `json:"prompt"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Events     []Event   `json:"events,omitempty"`
	Usage      Usage     `json:"usage"`      // tokens spent in this turn, including its subagents
	ToolCount  int       `json:"toolCount"`  // top-level tool calls in this turn
	ErrorCount int       `json:"errorCount"` // top-level tool calls that errored
}

// EventKind discriminates the Event union.
type EventKind int

const (
	EventText     EventKind = iota // assistant prose
	EventThinking                  // assistant reasoning
	EventTool                      // a tool call
)

// MarshalJSON renders the kind as a stable string ("text", "thinking",
// "tool") rather than its ordinal, so --format json is self-describing and
// insensitive to the iota order.
func (k EventKind) MarshalJSON() ([]byte, error) {
	s := "unknown"
	switch k {
	case EventText:
		s = "text"
	case EventThinking:
		s = "thinking"
	case EventTool:
		s = "tool"
	}
	return json.Marshal(s)
}

// Event is one ordered item in an assistant's output stream.
type Event struct {
	Kind EventKind `json:"kind"`
	Text string    `json:"text,omitempty"` // body for EventText and EventThinking
	Tool *Tool     `json:"tool,omitempty"` // set for EventTool
}

// Tool is a single tool call and its result.
type Tool struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"` // short single-line summary of the call's input
	// Identity is the call's grouping label, the same value ToolStat.Identity
	// carries — so a rendered call and a listing's tally name it identically
	// instead of the render path knowing less. Empty for tools whose own name is
	// their identity.
	Identity string `json:"identity,omitempty"`
	// Model is the model this call delegated to, taken from the input rather than
	// from Args, which flattens it away. Only Agent names one, and only sometimes:
	// empty means the subagent ran on the session's own model, which is why it is
	// not defaulted to Meta.Model — "inherited" and "chosen" are different facts.
	Model string `json:"model,omitempty"`
	// Denial is why this call was refused rather than run, the log's own
	// toolDenialKind. Empty for every call that ran, including one that ran and
	// failed — IsError is true either way, so this is what tells the two apart.
	Denial   string    `json:"denial,omitempty"`
	Result   string    `json:"result,omitempty"`
	IsError  bool      `json:"isError,omitempty"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Subagent []Event   `json:"subagent,omitempty"` // nested event stream when this call spawned a subagent
}
