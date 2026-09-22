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
	"github.com/eitanpo/agentry/internal/theme"
)

// hitSeparator divides the fields of a turn's row and of a locator. It is the
// separator the header and the closing card already use, and it cannot occur in
// a turn number, a call number or a part name, so a consumer splitting on it
// reaches the last field in one cut however many separators that field holds
// itself.
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
	// A single-session run draws no label column, so which end of a label would
	// survive cannot arise.
	return Findings(w, []search.Group{{Hits: hits}}, re, color, width, false)
}

// Findings writes each session's findings under its own heading, which is the
// row `agentry search session` prints on its own — one row type for both nouns
// rather than two invented ones. A single group whose match names no session is
// the one-session case and prints no heading: the caller already knows which
// session they searched, and a heading would indent every finding to say so.
func Findings(w io.Writer, groups []search.Group, re *regexp.Regexp, color bool, width int, keepTail bool) error {
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
	layout := layOutMatches(matches, width, keepTail)
	for i, g := range inDisplayOrder(groups) {
		// Under a heading the findings are that heading's block, so they hang off
		// the rail every block under a header hangs off; the rail's own columns come
		// out of the text they bound.
		column := width - len(findingIndent) - len(textIndent)
		if heading {
			column -= RailWidth()
		}
		var block []string
		for _, turn := range byTurn(g.Hits) {
			block = append(block, r.turnHead(turn))
			shown := 0
			seen := map[string]bool{}
			for _, h := range turn {
				// The sample passes over a row the reader has already seen, and what
				// they see is the window, not the whole line: a pattern inside a path
				// is named again by every command that touched that path, and those
				// lines differ only in a tail the window cuts. Three rows of one path
				// say less than one row of it does.
				text := findingText(h.Text, re, column)
				if shown == linesPerTurn || seen[text] {
					continue
				}
				seen[text] = true
				shown++
				block = append(block,
					findingIndent+r.tool.Render(hitLocation(h)),
					findingIndent+textIndent+r.highlight(text, re))
			}
			if rest := len(turn) - shown; rest > 0 {
				block = append(block, findingIndent+r.dim.Render(fmt.Sprintf("…  (%s)", plural(rest, "more line"))))
			}
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
func Matches(w io.Writer, matches []search.Match, color bool, width int, keepTail bool) error {
	if width <= 0 {
		width = fallbackWidth
	}
	if !color {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	r := &renderer{opts: Options{Color: color}}
	r.initStyles()
	layout := layOutMatches(matches, width, keepTail)
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

// matchLayout is the width of each column across a run of session rows, plus how
// much of an id tells these rows apart. Computed over the whole run before any
// row is written, because a column is only a column if every row agrees on its
// width — and because the significant half of an id is a property of the set of
// ids, not of any one of them.
type matchLayout struct {
	count, label, title, id, unique int
	// keepTail says which end of a label survives its column. It comes from the
	// chooser that produced the labels rather than from reading the label, so one
	// value is not cut two ways on two surfaces.
	keepTail bool
}

// titleFloor is the narrowest the title column goes. A terminal too narrow for
// the floor overruns rather than dropping the id, the id being what every other
// verb takes and the title being readable at any length.
const titleFloor = 10

// labelMax and labelFloor bound the path column, which holds a project or, inside
// one project, a worktree. The absolute cap is what a path suffix or a chosen
// name needs; the share cap below is what stops a long one starving the title.
const (
	labelMax   = 24
	labelFloor = 8
)

// LabelColumn is the width the path column takes, given the widest label in the
// run and the columns the label and the title share. It is capped twice:
// absolutely, and at a third of the shared space — without the second, one long
// project name leaves the title at its floor, and the title is what the row is
// actually read by.
//
// One owner, because a listing row and a search row divide the same space
// between the same two columns. Two copies of the rule would let one surface
// starve a column the other protects.
func LabelColumn(widest, shared int) int {
	if widest <= 0 {
		return 0
	}
	if third := shared / 3; widest > third {
		widest = third
	}
	if widest > labelMax {
		widest = labelMax
	}
	if widest < labelFloor {
		widest = labelFloor
	}
	return widest
}

func layOutMatches(matches []search.Match, width int, keepTail bool) matchLayout {
	l := matchLayout{keepTail: keepTail}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		if n := utf8.RuneCountInString(matchCount(m)); n > l.count {
			l.count = n
		}
		if n := utf8.RuneCountInString(m.Project); n > l.label {
			l.label = n
		}
		if n := utf8.RuneCountInString(m.Session); n > l.id {
			l.id = n
		}
		ids = append(ids, m.Session)
	}
	l.unique = UniqueIDPrefix(ids)
	// The label and the title share what the fixed columns leave, and the title
	// takes what the label does not: it is the one column whose content has no
	// length worth aligning to, so it absorbs the remainder the way the listing's
	// does. Three gaps without a label column, four with.
	gaps := 3 * len(matchGap)
	if l.label > 0 {
		gaps += len(matchGap)
	}
	shared := width - len(whenFormat) - l.count - l.id - gaps
	l.label = LabelColumn(l.label, shared)
	l.title = max(shared-l.label, titleFloor)
	return l
}

// IDFloor is the shortest id prefix any surface emphasizes. The floor is not
// about today's collisions but about tomorrow's: three rows would otherwise
// emphasize 1-character prefixes that stop resolving as the machine fills up,
// and an id is copied off one listing and passed back later.
const IDFloor = 8

// UniqueIDPrefix is how much of an id a reader has to type for it to name one of
// these rows and no other — the shortest length at which no two collide, floored
// at IDFloor. A caller passes every id it is about to print, because the answer
// is a property of the set and not of any one id.
func UniqueIDPrefix(ids []string) int {
	longest := 0
	for _, id := range ids {
		if n := len(id); n > longest {
			longest = n
		}
	}
	if longest <= IDFloor {
		return longest // every id is already shorter than the floor
	}
	for n := IDFloor; n < longest; n++ {
		seen := make(map[string]bool, len(ids))
		clash := false
		for _, id := range ids {
			p := id
			if len(p) > n {
				p = p[:n]
			}
			if seen[p] {
				clash = true
				break
			}
			seen[p] = true
		}
		if !clash {
			return n
		}
	}
	return longest
}

// SessionID draws an id whole with its significant half emphasized. Weight
// rather than presence is what separates the two jobs the value does: the prefix
// is what a reader scans the column by and hands back to agentry, and the whole
// string is what `claude --resume` requires, which is why both halves print. A
// caller with color off gets the same characters, since dropping half of a value
// a reader copies would be the worse degradation.
//
// One owner, so the listing and a search row emphasize the same characters. A
// search row drew all thirty-six the same and silently dropped the only thing on
// it that said how much to type.
func SessionID(id string, unique int) string {
	if len(id) <= unique {
		return theme.Meta().Render(id)
	}
	return theme.Meta().Render(id[:unique]) + theme.Dim().Render(id[unique:])
}

// linesPerTurn bounds how many of a turn's matching lines print. Three, because
// the sample says why the turn matched rather than standing in for reading it:
// the first line establishes that it matched, the next two say whether the
// pattern runs through the turn's own words or only through machine output it
// quoted, and a fourth answers nothing the third did not. What keeps this a cap
// rather than a deletion is the count beside the turn and the remainder beneath
// the sample, which together say how much more the turn holds.
const linesPerTurn = 3

// textIndent is how far a matching line's text sits under its locator, and the
// locator under its turn. One step for each level, so the block's three kinds of
// row are told apart by where they start rather than by a glyph each.
const textIndent = "  "

// byTurn splits one session's hits into a group per turn, in the order the turns
// matched — which is turn order, the hits arriving in document order. The turn
// heads its group rather than repeating beside each line: it is the address a
// reader navigates to, and one turn can match on hundreds of lines.
func byTurn(hits []search.Hit) [][]search.Hit {
	var turns [][]search.Hit
	at := map[int]int{}
	for _, h := range hits {
		i, ok := at[h.Turn]
		if !ok {
			at[h.Turn] = len(turns)
			turns = append(turns, []search.Hit{h})
			continue
		}
		turns[i] = append(turns[i], h)
	}
	return turns
}

// turnHead is a turn's row: which turn to go read, and how many of its lines
// matched. The count is there because it is what separates a turn the pattern
// runs through from one that mentions it once, and the sample beneath cannot say
// that on its own once it is capped.
func (r *renderer) turnHead(turn []search.Hit) string {
	return r.dim.Render(fmt.Sprintf("turn %d", turn[0].Turn)) +
		hitSeparator + r.dim.Render(plural(len(turn), "line"))
}

// matchCount is how many of a session's turns matched over how many it holds,
// in the turns column's own form. Both halves, because the share is what a
// caller reads: three of four turns matching is a different prospect from three
// of two hundred, and the bare count cannot tell them apart.
func matchCount(m search.Match) string {
	return fmt.Sprintf("%d/%dt", m.Matched, m.Turns)
}

// fitLabel cuts a label to its column, from whichever end does not tell these
// rows apart: a project label is a path suffix and keeps its tail, a worktree
// name keeps the head somebody chose. Which it is comes from the chooser that
// produced the labels, never guessed from the string.
//
// Padding alone was what this row did, and padding does not cut: one worktree
// name of thirty-two characters put every row fourteen columns past the terminal,
// where the id a reader copies is what the wrap breaks.
func fitLabel(label string, width int, keepTail bool) string {
	if keepTail {
		return truncateLeft(label, width)
	}
	return truncate(label, width)
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
// turns matched, its project or worktree, its title, and its id. The order is
// the listing's, the id last, because the two rows carry the same facts and a
// reader moving between them should not find one field in two places.
//
// Every column is fixed-width, the title taking whatever the others leave. The
// id ends the row rather than the title because the id is the value a reader
// copies out and hands to every other verb, and a value being copied must not
// move left and right as the title beside it changes length.
func (r *renderer) matchRow(m search.Match, l matchLayout) string {
	var b strings.Builder
	// A summary always carries a start, so a zero time here is a row that cannot
	// occur rather than a case to label. It pays for its column in spaces, which
	// keeps the rest of the row aligned without spelling "unknown" a second way.
	when := strings.Repeat(" ", len(whenFormat))
	if !m.Activity.IsZero() {
		when = m.Activity.Local().Format(whenFormat)
	}
	// Every field takes the role the listing draws that same field in. The two
	// rows carry the same facts in the same columns, so a reader moving between
	// them should not have to learn a second colour for each one — and this row
	// had a different one for all five: a dimmer time, a brighter count, a
	// tool-call yellow for the project, and one flat shade over the whole id where
	// the listing emphasizes the half a reader types.
	b.WriteString(r.meta.Render(when))
	b.WriteString(matchGap)
	b.WriteString(r.dim.Render(fmt.Sprintf("%*s", l.count, matchCount(m))))
	if l.label > 0 {
		b.WriteString(matchGap)
		b.WriteString(r.dim.Render(padRight(fitLabel(m.Project, l.label, l.keepTail), l.label)))
	}
	// The title takes the terminal's own foreground, as the listing's does. A
	// fixed near-white is the brightest thing on a dark background and close to
	// invisible on a light one, and the title is the field the row is read by.
	b.WriteString(matchGap)
	b.WriteString(padRight(r.plain.Render(truncate(OneLine(m.Title), l.title)), l.title))
	b.WriteString(matchGap)
	b.WriteString(SessionID(m.Session, l.unique))
	return strings.TrimRight(b.String(), " ")
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
	located := strings.Join(parts, " › ")
	// The call's own number leads, because it is the field a reader carries to the
	// rendered turn: the tool's name and the chain say what the call was, and only
	// the number says which of the turn's calls it is. A turn that ran one tool
	// repeatedly gave all of those calls one address without it. It names the
	// innermost call the rest of the locator reaches, which is the delegating call
	// where the hit sits in a delegated stream's own prose.
	if h.Call > 0 {
		located = fmt.Sprintf("call %d", h.Call) + hitSeparator + located
	}
	return located
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
		b.WriteString(r.match.Render(line[s[0]:s[1]]))
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
