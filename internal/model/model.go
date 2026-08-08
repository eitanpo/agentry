// Package model is the canonical in-memory representation of a Claude Code
// session. The parser produces it, the renderer consumes it, and `--format
// json` serializes it. It carries no presentation concerns.
package model

import (
	"encoding/json"
	"time"
)

// Session is a fully parsed session log.
type Session struct {
	Meta  Meta   `json:"meta"`
	Turns []Turn `json:"turns"`
}

// Meta is session-level metadata aggregated across all turns and subagents.
type Meta struct {
	ID    string `json:"id"`
	Model string `json:"model,omitempty"`
	// Effort is the reasoning effort the model ran at, resolved like Entrypoint:
	// the last value the session carries. Efforts lists every distinct one, set
	// only when it changed mid-session. Empty on a session predating the field
	// (about half of them), which is not a claim that effort was any particular
	// value — the log simply does not say.
	Effort       string    `json:"effort,omitempty"`
	Efforts      []string  `json:"efforts,omitempty"`
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Usage        Usage     `json:"usage"`
	NumSubagents int       `json:"numSubagents"`
	// Entrypoint and Entrypoints mirror the Summary fields of the same names,
	// resolved identically, so the render path and the listing never disagree
	// about where one session ran.
	Entrypoint  string   `json:"entrypoint,omitempty"`
	Entrypoints []string `json:"entrypoints,omitempty"`
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

// Usage is a token tally. Cache fields mirror the Anthropic usage object.
type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheCreate int `json:"cacheCreate"`
}

// Add accumulates another tally into this one.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheCreate += o.CacheCreate
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
