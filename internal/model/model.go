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
