// Package model is the canonical in-memory representation of a Claude Code
// session. The parser produces it, the renderer consumes it, and the roadmap
// --format json will serialize it. It carries no presentation concerns.
package model

import "time"

// Session is a fully parsed session log.
type Session struct {
	Meta  Meta
	Turns []Turn
}

// Meta is session-level metadata aggregated across all turns and subagents.
type Meta struct {
	ID           string
	Model        string
	Start        time.Time
	End          time.Time
	Usage        Usage
	NumSubagents int
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

// Usage is a token tally. Cache fields mirror the Anthropic usage object.
type Usage struct {
	Input       int
	Output      int
	CacheRead   int
	CacheCreate int
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
	Prompt     string
	Start      time.Time
	End        time.Time
	Events     []Event
	Usage      Usage // tokens spent in this turn, including its subagents
	ToolCount  int   // top-level tool calls in this turn
	ErrorCount int   // top-level tool calls that errored
}

// EventKind discriminates the Event union.
type EventKind int

const (
	EventText     EventKind = iota // assistant prose
	EventThinking                  // assistant reasoning
	EventTool                      // a tool call
)

// Event is one ordered item in an assistant's output stream.
type Event struct {
	Kind EventKind
	Text string // body for EventText and EventThinking
	Tool *Tool  // set for EventTool
}

// Tool is a single tool call and its result.
type Tool struct {
	Name     string
	Args     string // short single-line summary of the call's input
	Result   string
	IsError  bool
	Start    time.Time
	End      time.Time
	Subagent []Event // nested event stream when this call spawned a subagent
}
