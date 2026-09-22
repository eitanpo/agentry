// Package find locates a pattern inside one parsed session. The listing's
// filters choose which session to open; this answers the question that follows —
// where inside it a passage sits.
//
// It searches the whole parsed model rather than what a render would show. A
// search bounded by --level would answer "where does this appear at this
// verbosity", which is a question nobody asks: a reader who cannot find a
// passage they know is in the log has been told the log does not hold it. That
// is the rule --format json already follows for the same reason.
package search

import (
	"regexp"
	"strings"
	"time"

	"github.com/eitanpo/agentry/internal/model"
)

// Part names which piece of a record matched. A turn's own prompt and the
// instruction handed to a subagent are separate values although the log calls
// both a prompt: one is what a person typed and the other is what a delegated
// run was told, and a reader who searched for one is not looking at the other.
type Part string

const (
	PartPrompt      Part = "prompt"      // the turn's own user prompt
	PartText        Part = "text"        // assistant prose
	PartThinking    Part = "thinking"    // assistant reasoning
	PartArgs        Part = "args"        // a tool call's one-line argument summary
	PartInstruction Part = "instruction" // the brief an Agent call delegated
	PartResult      Part = "result"      // a tool call's result body
)

// Hit is one line inside a session that matched.
type Hit struct {
	// Session is the id of the session the hit came from, set only on a run that
	// searched more than one: a turn number names nothing without it, so a
	// cross-session consumer needs both halves of the address and a single-session
	// one already knows which session it asked about.
	Session string `json:"session,omitempty"`
	// Turn is 1-based and counts the main thread's turns, so a hit inside a
	// subagent carries the turn of the call that spawned it — which is the turn a
	// reader renders to go read it.
	Turn int `json:"turn"`
	// Delegation is the chain of calls from the main thread down to the hit, each
	// element written the way a rendered activation line writes it
	// ("Agent[Explore@haiku]"). Empty for a hit in the main thread. It is what
	// separates two identical passages that a session both wrote and delegated.
	Delegation []string `json:"delegation,omitempty"`
	Part       Part     `json:"part"`
	// Tool, Identity and Model name the call the hit sits in, all empty for a hit
	// in a turn's prompt or in assistant text. Identity is the same grouping label
	// the listing groups by and Model the model an Agent call delegated to, so a
	// hit, a tally and a rendered activation line name one call alike. They are
	// carried apart rather than as one rendered label because a consumer filtering
	// hits by subagent type would otherwise have to parse the label back out.
	Tool     string `json:"tool,omitempty"`
	Identity string `json:"identity,omitempty"`
	Model    string `json:"model,omitempty"`
	// Call is the position in the turn of the call this hit sits in, as the parsed
	// model numbers it — the innermost call the locator names, which for a hit in a
	// delegated stream's own prose is the call that spawned that stream. Zero for a
	// hit in the turn's prompt or in the main thread's prose, which sit in no call.
	//
	// Without it a turn that ran one tool repeatedly gave every one of those calls
	// the same address, and a reader sent to the second of four had nothing to tell
	// it from the other three.
	Call int `json:"call,omitempty"`
	// Block is the position among the turn's prose of the reply or reasoning block
	// this hit sits in, as the parsed model numbers it. Zero for a hit in a call's
	// own body or in the turn's prompt, neither of which is a prose block.
	//
	// Without it a turn holding fifty reasoning blocks addressed a hit in any of
	// them the same way, each block numbering its own lines from one.
	Block int `json:"block,omitempty"`
	// Line is 1-based within the matched body, so a hit deep in a long result body
	// can be told from one at its head. The text render omits it: a body's own
	// line number locates nothing a reader can navigate to, where the turn does.
	Line int `json:"line"`
	// Text is the whole matching line, untrimmed of its content and never cut to a
	// width. A hit the caller cannot read in full is a hit they have to go looking
	// for twice.
	Text string `json:"text"`
}

// Match is one session that holds findings — the row `agentry search session`
// prints, and the heading `agentry search turn` groups its findings under. Its
// fields are the listing's own so that one session is not described two ways,
// plus the count of distinct turns that matched, which is the thing a caller
// chooses between sessions on.
type Match struct {
	Session string `json:"session"`
	// Activity is the one time this row carries: the session's last activity,
	// which is what a listing row shows and what these rows are ordered by. A
	// second time would let the column disagree with the order.
	Activity time.Time `json:"activity"`
	Project  string    `json:"project,omitempty"`
	Title    string    `json:"title,omitempty"`
	// Matched counts the distinct turns holding a finding, and Turns is how many
	// the session has. Both, because the share is what a caller reads: three of
	// four turns matching is a different prospect from three of two hundred.
	Matched int `json:"turnsMatched"`
	Turns   int `json:"numTurns"`
}

// Group pairs a session with its findings, for the text render. The machine
// forms carry one or the other rather than this pairing: the turn noun writes
// hits that each name their session, and the session noun writes matches with no
// hits at all.
type Group struct {
	Match Match
	Hits  []Hit
}

// TurnsIn counts the distinct turns hits fall in, which is not len(hits): one
// turn commonly matches on several lines, and a count of lines offered as a count
// of turns would rank a session with one wordy turn above one with ten.
func TurnsIn(hits []Hit) int {
	seen := map[int]bool{}
	for _, h := range hits {
		seen[h.Turn] = true
	}
	return len(seen)
}

// In returns every line of the session that re matches, in document order:
// each turn's prompt, then its events, descending into a delegated stream where
// the call that spawned it sits.
func In(s *model.Session, re *regexp.Regexp) []Hit {
	var hits []Hit
	for i, t := range s.Turns {
		turn := i + 1
		hits = append(hits, linesIn(re, Hit{Turn: turn, Part: PartPrompt}, t.Prompt)...)
		hits = append(hits, inEvents(re, t.Events, turn, nil, 0)...)
	}
	return hits
}

// inEvents walks one event stream. delegation is the chain of calls that led
// here, carried by value into each recursion so sibling streams cannot see each
// other's path, and call is the call that spawned this stream — zero at the main
// thread, which sits in no call. A delegated stream's own prose is addressed by
// that spawning call, there being no nearer one to name.
func inEvents(re *regexp.Regexp, events []model.Event, turn int, delegation []string, call int) []Hit {
	var hits []Hit
	for _, e := range events {
		switch e.Kind {
		case model.EventText:
			hits = append(hits, linesIn(re, Hit{Turn: turn, Delegation: delegation, Call: call, Block: e.Block, Part: PartText}, e.Text)...)
		case model.EventThinking:
			hits = append(hits, linesIn(re, Hit{Turn: turn, Delegation: delegation, Call: call, Block: e.Block, Part: PartThinking}, e.Text)...)
		case model.EventTool:
			if e.Tool == nil {
				continue
			}
			hits = append(hits, inTool(re, e.Tool, turn, delegation)...)
		}
	}
	return hits
}

// inTool searches one call's own three bodies, then the stream it spawned. The
// order is the order a render prints them, so a hit list and a rendered session
// walk the session the same way.
func inTool(re *regexp.Regexp, t *model.Tool, turn int, delegation []string) []Hit {
	at := Hit{Turn: turn, Delegation: delegation, Call: t.Call, Tool: t.Name, Identity: t.Identity, Model: t.Model}
	var hits []Hit
	for _, body := range []struct {
		part Part
		text string
	}{
		{PartArgs, t.Args},
		{PartInstruction, t.Prompt},
		{PartResult, t.Result},
	} {
		here := at
		here.Part = body.part
		hits = append(hits, linesIn(re, here, body.text)...)
	}
	if len(t.Subagent) > 0 {
		hits = append(hits, inEvents(re, t.Subagent, turn, append(delegation, Label(t.Name, t.Identity, t.Model)), t.Call)...)
	}
	return hits
}

// Label writes a call the way a rendered activation line writes it, so a hit
// names the call by the same string a reader will scroll past to reach it. One
// function serves both places a hit can name a call — the chain it was delegated
// through, and the call its own matching body belongs to — because two callers
// spelling one call differently is what makes a hit list disagree with itself.
//
// Only Agent carries the bracketed half, for the reason the renderer gives: it
// is the one tool whose args hide its identity.
func Label(tool, identity, model string) string {
	if tool != "Agent" {
		return tool
	}
	label := identity
	if model != "" {
		label += "@" + model
	}
	if label == "" {
		return tool
	}
	return tool + "[" + label + "]"
}

// linesIn splits a body into lines and returns one hit per matching line, each
// a copy of at with its line number and text filled in. Per line rather than
// per match: two matches on one line are one place to go read, and reporting
// them twice would make a hit count read as a location count.
func linesIn(re *regexp.Regexp, at Hit, text string) []Hit {
	if text == "" {
		return nil
	}
	var hits []Hit
	for i, line := range strings.Split(text, "\n") {
		if !re.MatchString(line) {
			continue
		}
		h := at
		h.Line = i + 1
		h.Text = line
		hits = append(hits, h)
	}
	return hits
}
