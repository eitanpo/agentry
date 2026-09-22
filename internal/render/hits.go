package render

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/eitanpo/agentry/internal/jsonl"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/schema"
	"github.com/eitanpo/agentry/internal/search"
)

// hitSeparator divides a hit line's three fields. It is the separator the
// header and the closing card already use, and it cannot occur in a turn
// number or a part name, so a consumer splitting on it reaches the matched text
// in one cut however many separators that text holds itself.
const hitSeparator = " · "

// findingIndent is how far a finding's text sits under its locator. Two columns:
// enough that the indent alone marks where one finding ends and the next begins,
// which is why no blank line separates them — a blank line would restate the
// boundary at a quarter more height again.
const findingIndent = "  "

// matchGap divides the session row's columns, and whenFormat is the time its
// first column holds. Two spaces and this layout are the listing's, so a reader
// moving between the two surfaces reads one row shape.
const (
	matchGap   = "  "
	whenFormat = "2006-01-02 15:04"
)

// Hits writes one finding per hit as two rows: a locator naming the turn and
// where inside it the line sits, then the matching line indented beneath. One
// row does not fit — measured over 206 real hits the locator runs to 58 columns
// and 72% of findings pass 80 columns with both on a line, wrapping mid-token
// with no indent to mark the continuation. Two rows cost 20% more height rather
// than double, because that text was already wrapping.
//
// The matched text is never cut to the terminal's width. A long line wraps for
// display, which costs the reader a wrapped line and leaves them the fact;
// cutting it would delete the half a hit exists to show, and PRODUCT.md's CLI
// conventions prefer a wrap to a drop for exactly that reason. Nothing caps the
// number of hits either: the caller pipes to `head` the way they would any
// search, and a cap agentry chose would hide matches a pattern asked for.
func Hits(w io.Writer, hits []search.Hit, re *regexp.Regexp, color bool, width int) error {
	return Findings(w, []search.Group{{Hits: hits}}, re, color, width)
}

// Findings writes each session's findings under its own heading, which is the
// row `agentry search session` prints on its own — one row type for both nouns
// rather than two invented ones. A single group whose match names no session is
// the one-session case and prints no heading: the caller already knows which
// session they searched, and a heading would indent every finding to say so.
func Findings(w io.Writer, groups []search.Group, re *regexp.Regexp, color bool, width int) error {
	if width <= 0 {
		width = fallbackWidth
	}
	if !color {
		// The same global the session render sets: under the Ascii profile every
		// style renders to plain text, so one styling path serves both.
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	r := &renderer{opts: Options{Color: color}}
	r.initStyles()
	var b strings.Builder
	heading := len(groups) > 1 || (len(groups) == 1 && groups[0].Match.Session != "")
	matches := make([]search.Match, 0, len(groups))
	for _, g := range groups {
		matches = append(matches, g.Match)
	}
	layout := layOutMatches(matches)
	for i, g := range inDisplayOrder(groups) {
		// Under a heading the findings are that heading's block, so they hang off
		// the rail every block under a header hangs off; the rail's own columns come
		// out of the text they bound.
		column := width - len(findingIndent)
		if heading {
			column -= RailWidth()
		}
		var block []string
		for _, h := range g.Hits {
			block = append(block,
				r.dim.Render(fmt.Sprintf("turn %d", h.Turn))+hitSeparator+r.tool.Render(hitLocation(h)),
				findingIndent+r.highlight(findingText(h.Text, re, column), re))
		}
		if heading {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(r.matchRow(g.Match, layout))
			b.WriteString("\n")
			block = Rail(block, r.dim)
		}
		for _, line := range block {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// Matches writes one row per session that holds findings — `agentry search
// session` with nothing beneath each row. The same row Findings heads a group
// with, so a caller reading both sees one session described one way.
func Matches(w io.Writer, matches []search.Match, color bool) error {
	if !color {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	r := &renderer{opts: Options{Color: color}}
	r.initStyles()
	layout := layOutMatches(matches)
	var b strings.Builder
	for _, m := range inDisplayOrder(matches) {
		b.WriteString(r.matchRow(m, layout))
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// inDisplayOrder reverses a run for printing. Selection hands these over
// most-recent first, which is how the rows are chosen; printed oldest-to-newest
// the most recent lands at the bottom, nearest the prompt, which is where the
// terminal leaves the reader — the ls -ltr / shell-history convention the
// listing already follows for unpaged output. Selection and display order are
// separate on purpose: the order does not turn on whether stdout is a terminal,
// so a piped run reads the same as one on screen.
func inDisplayOrder[T any](run []T) []T {
	out := make([]T, len(run))
	for i, v := range run {
		out[len(run)-1-i] = v
	}
	return out
}

// matchLayout is the width of each fixed column across a run of session rows.
// Computed over the whole run before any row is written, because a column is
// only a column if every row agrees on its width.
type matchLayout struct{ count, label int }

func layOutMatches(matches []search.Match) matchLayout {
	var l matchLayout
	for _, m := range matches {
		if n := utf8.RuneCountInString(matchCount(m)); n > l.count {
			l.count = n
		}
		if n := utf8.RuneCountInString(m.Project); n > l.label {
			l.label = n
		}
	}
	return l
}

// matchCount is how many of a session's turns matched over how many it holds,
// in the turns column's own form. Both halves, because the share is what a
// caller reads: three of four turns matching is a different prospect from three
// of two hundred, and the bare count cannot tell them apart.
func matchCount(m search.Match) string {
	return fmt.Sprintf("%d/%dt", m.Matched, m.Turns)
}

// flattenTitle puts a title carrying line breaks on one line. It joins rather
// than ending at the first break: a title is a session's first prompt where
// nothing better was recorded, and a prompt can run to many lines, so cutting at
// the break deletes the rest of it with nothing to mark that it went. Joined,
// the row wraps and the reader keeps the whole of it.
func flattenTitle(title string) string {
	return strings.Join(strings.Fields(title), " ")
}

// padRight is the listing's own padding, by rune count: a label column here is a
// path or a worktree name, which is what that measure already serves there.
func padRight(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// matchRow is the session row: the session's last activity, how many of its
// turns matched, its project or worktree, its id, and its title. Every field is
// a fixed-width column but the title, which comes last and is never cut.
//
// The count has to read down a column, being what a caller chooses between
// sessions on, and it cannot while it sits at the end of a line whose length
// varies with the title beside it. Keeping the title whole is what puts it last:
// any column after an uncut title would be ragged instead.
func (r *renderer) matchRow(m search.Match, l matchLayout) string {
	var b strings.Builder
	// A summary always carries a start, so a zero time here is a row that cannot
	// occur rather than a case to label. It pays for its column in spaces, which
	// keeps the rest of the row aligned without spelling "unknown" a second way.
	when := strings.Repeat(" ", len(whenFormat))
	if !m.Activity.IsZero() {
		when = m.Activity.Local().Format(whenFormat)
	}
	b.WriteString(r.dim.Render(when))
	b.WriteString(matchGap)
	b.WriteString(r.body.Render(fmt.Sprintf("%*s", l.count, matchCount(m))))
	if l.label > 0 {
		b.WriteString(matchGap)
		b.WriteString(r.tool.Render(padRight(m.Project, l.label)))
	}
	b.WriteString(matchGap)
	b.WriteString(r.body.Render(m.Session))
	if title := flattenTitle(m.Title); title != "" {
		b.WriteString(matchGap)
		b.WriteString(r.body.Render(title))
	}
	return b.String()
}

// findingText is the one row a finding's text occupies: the matching line where
// it fits the column, and a window of it around the match where it does not.
//
// A matching line can be a whole captured result — lines of twenty-five thousand
// characters occur in real logs, which wrap to hundreds of screen rows and bury
// every other finding in the run. The window is centred on the match rather than
// cut from the head of the line, because a head-cut window need not contain the
// match at all: it would print characters the pattern never touched and leave the
// reader to take on trust that the passage is in there somewhere.
//
// The cut end carries an ellipsis, so what went is named rather than silently
// absent. What it costs the reader is the rest of that line, which the locator
// beside it says how to reach: `agentry view --turn <n>` prints the body whole.
func findingText(line string, re *regexp.Regexp, column int) string {
	runes := []rune(line)
	if column < minContentWidth {
		column = minContentWidth
	}
	if len(runes) <= column {
		return line
	}
	// Both ellipses are budgeted for up front rather than after placing the
	// window: subtracting them afterwards can push the match back out of a window
	// that had just been sized to hold it.
	const ellipsis = "…"
	room := column - 2*len([]rune(ellipsis))
	if room < minContentWidth {
		room = minContentWidth
	}
	start := 0
	if re != nil {
		if at := re.FindStringIndex(line); at != nil {
			from := len([]rune(line[:at[0]]))
			width := len([]rune(line[at[0]:at[1]]))
			// Centred where the match is shorter than the window; otherwise the window
			// opens at the match, which is the most of it a row can show.
			start = from
			if width < room {
				start = from - (room-width)/2
			}
		}
	}
	if start+room > len(runes) {
		start = len(runes) - room
	}
	if start < 0 {
		start = 0
	}
	out := string(runes[start : start+room])
	if start > 0 {
		out = ellipsis + out
	}
	if start+room < len(runes) {
		out += ellipsis
	}
	return out
}

// hitLocation names where inside the turn the line sits: the calls delegated
// through to reach it, then which piece of the record matched. A hit in the
// main thread is the part alone.
func hitLocation(h search.Hit) string {
	var parts []string
	parts = append(parts, h.Delegation...)
	where := fmt.Sprintf("%s:%d", h.Part, h.Line)
	// The line number rides the part, the way a compiler writes file:line, because
	// a hit deep in a long body is a different errand from one at its head: a
	// result body renders only its first ten lines, so a line past that is a hit
	// no render will show and the caller needs the machine form to read it. It was
	// left to --format json until the accounting said otherwise — one more field
	// costs a few characters on a line and no extra lines at all.
	//
	// A tool's own body also names the call, since three of the six parts can only
	// occur inside one and the part alone would not say which. It is named by the
	// same label the delegation chain above uses, so one call does not read two
	// ways depending on whether the hit was inside it or beneath it.
	if h.Tool != "" {
		where = search.Label(h.Tool, h.Identity, h.Model) + " " + where
	}
	parts = append(parts, where)
	return strings.Join(parts, " › ")
}

// highlight emphasizes every match inside one line. With color off it returns
// the line unchanged: the line is the content and the emphasis is an aid, so
// nothing here is the only carrier of a fact.
func (r *renderer) highlight(line string, re *regexp.Regexp) string {
	if !r.opts.Color || re == nil {
		return line
	}
	spans := re.FindAllStringIndex(line, -1)
	if spans == nil {
		return line
	}
	var b strings.Builder
	at := 0
	for _, s := range spans {
		// A zero-width match advances nothing and would emphasize an empty span, so
		// it is passed over rather than marked.
		if s[0] >= s[1] {
			continue
		}
		b.WriteString(r.body.Render(line[at:s[0]]))
		b.WriteString(r.user.Render(line[s[0]:s[1]]))
		at = s[1]
	}
	b.WriteString(r.body.Render(line[at:]))
	return b.String()
}

// HitsJSON writes the hits as one indented array — the machine form of the same
// facts, carrying each hit's line number within its body, which the text lines
// leave out. Always a well-formed array, `[]` where nothing matched, the rule
// the listing's array already follows so a consumer can pipe into jq without a
// guard.
func HitsJSON(w io.Writer, hits []search.Hit) error {
	if hits == nil {
		hits = []search.Hit{}
	}
	b, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// HitsJSONL writes one `hit` record per line under the standard envelope. A run
// that matched nothing writes nothing rather than an empty array, there being no
// wrapper to emit — the rule the listing's stream already follows.
//
// The session rides the envelope rather than the payload, so a stream merged
// from two searches says which session each hit came from after a `cat`. A hit
// carrying its own session wins over the run's, which is what makes a
// cross-session stream addressable: the consumer reads the session from the
// envelope and the turn from the payload, and the pair is the address
// `agentry view <id> --turn <n>` takes.
func HitsJSONL(w io.Writer, hits []search.Hit, session string) error {
	enc := jsonl.New(w)
	for _, h := range hits {
		of := session
		if h.Session != "" {
			of = h.Session
		}
		if err := enc.Emit("hit", of, time.Time{}, h); err != nil {
			return err
		}
	}
	return nil
}

// MatchesJSON writes the session matches as one array, always well-formed: `[]`
// where nothing matched, so a consumer pipes into jq without a guard.
func MatchesJSON(w io.Writer, matches []search.Match) error {
	if matches == nil {
		matches = []search.Match{}
	}
	b, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// MatchesJSONL writes one `sessionMatch` record per line. Each names its own
// session on the envelope, which is the whole point of the noun: the payload
// says how many turns matched and the envelope says which session they are in.
func MatchesJSONL(w io.Writer, matches []search.Match) error {
	enc := jsonl.New(w)
	for _, m := range matches {
		if err := enc.Emit("sessionMatch", m.Session, m.Activity, m); err != nil {
			return err
		}
	}
	return nil
}

// Shapes describes what the render path and the search verb write in their
// machine-readable forms. Named here rather than written down a second time: each
// entry names the very value an emitter passes, so a field added to one of these
// types is described without anyone remembering to say so.
func Shapes() []schema.Shape {
	return []schema.Shape{
		schema.Document("view", "agentry view --format json", model.Session{}),
		schema.Record("view", "meta", model.Meta{}),
		schema.Record("view", "turn", turnRecord{}),
		// The stream's event record carries the tool object without its nested
		// subagent stream, which the emitter clears and which follows as records
		// of its own. The type permits the key; no line ever holds it.
		schema.Record("view", "event", eventRecord{}, schema.Omit("tool.subagent")),
		schema.Document("search", "agentry search --format json", []search.Hit{}),
		schema.Record("search", "hit", search.Hit{}),
		schema.Document("search", "agentry search session --format json", []search.Match{}),
		schema.Record("search", "sessionMatch", search.Match{}),
	}
}
