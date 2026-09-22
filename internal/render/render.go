// Package render turns a model.Session into a styled terminal view: glamour
// renders the markdown bodies (prose, code blocks), lipgloss draws the chrome
// (boxes, per-actor glyphs, color). When color is off the same layout is
// emitted as plain text.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/breakdown"
	"github.com/eitanpo/agentry/internal/entrypoint"
	"github.com/eitanpo/agentry/internal/jsonl"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/price"
	"github.com/eitanpo/agentry/internal/spend"
	"github.com/eitanpo/agentry/internal/trail"
	"github.com/muesli/termenv"
)

const (
	fallbackWidth = 100 // used when stdout is not a TTY
	// toolBodyMaxLines bounds a result body of an unselected turn, which is machine output nothing
	// limits: one file read can run longer than every reply in its turn put
	// together, so uncapped bodies bury the transcript in the output of the work.
	// The overflow is named rather than dropped silently, which is what keeps this
	// a cap and not a deletion. PRODUCT.md's Verbosity section owns the rule and
	// the number; an instruction delegated to a subagent is deliberately outside
	// it — see toolPrompt.
	toolBodyMaxLines = 10
	assistantIndent  = "  " // left pad before the assistant turn's rail (│ … ╰─)
	railGlyph        = "│"  // the rail a bounded block hangs off
	railClose        = "╰─" // the rule that closes one
	glyphUser        = "❯"
	glyphClaude      = "◆"
	glyphTool        = "●"
	glyphSubagent    = "▶"
	glyphThinking    = "✻"
	glyphOK          = "✓"
	glyphErr         = "✗"
	minContentWidth  = 20
	headerPrefix     = "Session · "
	sectionIndent    = "  " // footer rows, aligned with the per-turn table's
	// filesShown and identityEntriesShown bound the two footer sections whose
	// length the session decides — a session can touch far more files or tool
	// identities than typically fit in a footer, so each is capped.
	filesShown           = 10
	identityEntriesShown = 8
	// cardLabelWidth pads the closing card's labels into one column, so the values
	// beside them line up and the id is selectable as one run of text.
	cardLabelWidth = 9
	// dateAndTime dates a header timestamp; timeOnly is the shorter form the end
	// of a same-day session takes, where repeating the date says nothing.
	dateAndTime = "Jan 2 15:04"
	timeOnly    = "15:04"
)

// Channels selects which optional sections render. Tools gates the per-call
// activation line (that a tool fired); ToolResults gates its result body.
type Channels struct {
	Thinking, Tools, ToolResults, Subagents, Metrics bool
}

// Options configures a render pass.
type Options struct {
	Width    int
	Color    bool
	Channels Channels
	// Selected is the span of turns the caller narrowed the transcript to, nil
	// for a whole session. The turns are already filtered when this is set; the
	// span is carried so the render can say which it is showing. Without that
	// line a slice is a whole session as far as the page shows: the header still
	// counts thirty-one turns, three follow it, and nothing says whether the rest
	// were excluded or failed.
	Selected *model.TurnRange
}

type renderer struct {
	opts    Options
	gcache  map[int]*glamour.TermRenderer
	user    lipgloss.Style
	userRow lipgloss.Style
	claude  lipgloss.Style
	tool    lipgloss.Style
	subnt   lipgloss.Style
	think   lipgloss.Style
	ok      lipgloss.Style
	bad     lipgloss.Style
	body    lipgloss.Style
	brief   lipgloss.Style
	args    lipgloss.Style
	link    lipgloss.Style
	dim     lipgloss.Style
	border  lipgloss.Style
	userBox lipgloss.Style
}

// SessionJSON writes the full session model to w as indented JSON — the render
// path's machine-readable form (`--format json`), for agent consumption and
// piping into jq. It emits the complete model regardless of verbosity or color,
// which shape only the styled text view.
func SessionJSON(w io.Writer, s *model.Session) error {
	if s.Turns == nil {
		// An empty array, not null. A session with no turns is a real outcome — a
		// log holding only bookkeeping entries produces one — and marshalling it as
		// null gives the document's primary collection a second shape that every
		// consumer has to branch on. The listing and both roll-up shapes already
		// normalize this way; the render path was the one that did not, and a
		// consumer iterating turns crashed on the difference.
		// Copied rather than assigned through: the other three emitters take their
		// payload by value, so normalizing is local to them. This one takes a
		// pointer, and a serializer that edits its caller's session is a side
		// effect nothing at the call site would expect.
		normalized := *s
		normalized.Turns = []model.Turn{}
		s = &normalized
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// turnRecord is a `turn` line's payload: everything model.Turn carries except
// its events, which become records of their own. Inlining them would put a
// delegated session inside one line, which is the problem the line format
// exists to avoid, one level down.
type turnRecord struct {
	Turn       int         `json:"turn"`
	Prompt     string      `json:"prompt"`
	Start      time.Time   `json:"start"`
	End        time.Time   `json:"end"`
	Usage      model.Usage `json:"usage"`
	ToolCount  int         `json:"toolCount"`
	ErrorCount int         `json:"errorCount"`
}

// eventRecord is an `event` line's payload. depth carries the nesting that
// model.Event holds structurally: a tool call that spawned a subagent is
// followed by that subagent's events at depth+1, in document order, so the tree
// is recovered from depth and order rather than from a parent reference.
type eventRecord struct {
	Turn  int `json:"turn"`
	Depth int `json:"depth"`
	// Index counts an event's position among its siblings at its own depth, so it
	// restarts inside every subagent stream and two subagents in one turn both
	// emit an index 1. It orders a sibling list; it does not identify a record.
	// The envelope's ordinal is the field that is unique across the stream.
	Index int             `json:"index"`
	Kind  model.EventKind `json:"kind"`
	Text  string          `json:"text,omitempty"`
	Tool  *model.Tool     `json:"tool,omitempty"`
}

// SessionJSONL writes the session to w as JSON Lines (`--format jsonl`): a meta
// record, then per turn a turn record and one record per event. It carries the
// same facts as SessionJSON and, like it, ignores verbosity and color.
func SessionJSONL(w io.Writer, s *model.Session) error {
	enc := jsonl.New(w)
	id := s.Meta.ID
	if err := enc.Emit("meta", id, s.Meta.Start, s.Meta); err != nil {
		return err
	}
	for _, t := range s.Turns {
		n := t.Number
		rec := turnRecord{
			Turn: n, Prompt: t.Prompt, Start: t.Start, End: t.End,
			Usage: t.Usage, ToolCount: t.ToolCount, ErrorCount: t.ErrorCount,
		}
		if err := enc.Emit("turn", id, t.Start, rec); err != nil {
			return err
		}
		if err := emitEvents(enc, id, t.Events, n, 0); err != nil {
			return err
		}
	}
	return nil
}

// emitEvents writes one event stream and recurses into any subagent beneath it.
// The tool is copied without its Subagent field: the nested stream follows as
// its own records, and leaving it in place would restore the nesting this format
// removes.
func emitEvents(enc *jsonl.Encoder, session string, events []model.Event, turn, depth int) error {
	for i, e := range events {
		rec := eventRecord{Turn: turn, Depth: depth, Index: i + 1, Kind: e.Kind, Text: e.Text}
		var nested []model.Event
		at := time.Time{}
		if e.Tool != nil {
			flat := *e.Tool
			nested = flat.Subagent
			flat.Subagent = nil
			rec.Tool = &flat
			at = e.Tool.Start
		}
		if err := enc.Emit("event", session, at, rec); err != nil {
			return err
		}
		if err := emitEvents(enc, session, nested, turn, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// Session writes the styled session to w.
func Session(w io.Writer, s *model.Session, opts Options) error {
	if opts.Width <= 0 {
		opts.Width = fallbackWidth
	}
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii) // strips all ANSI from styles
	}
	r := &renderer{opts: opts, gcache: map[int]*glamour.TermRenderer{}}
	r.initStyles()

	var b strings.Builder
	b.WriteString(r.header(s))
	if line := r.selection(s); line != "" {
		b.WriteString(line)
	}
	for _, t := range s.Turns {
		b.WriteString("\n")
		b.WriteString(r.turn(t))
	}
	// The footer: what the session touched and produced, then how it worked. No
	// section is gated on verbosity — a two-turn session already renders 257 lines
	// and the per-turn table adds five, so a gate saved a reader nothing they would
	// notice while costing them the section's existence. Length is bounded by each
	// section's own cap instead. PRODUCT.md's Output section owns the rule.
	//
	// Outcomes lead, so a reader who stops after two sections still has them. The
	// three aggregates leave together on --no-metrics, which is why they sit last.
	footer := []string{r.files(s), r.outputs(s)}
	if opts.Channels.Metrics {
		footer = append(footer, r.identities(s), r.cost(s), r.summary(s), r.dayByDay(s))
	}
	// The card closes the render, so the id a reader needs to cite or resume the
	// session is the last thing on screen rather than thousands of lines above it.
	footer = append(footer, r.card(s))
	for _, section := range footer {
		if section == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(section)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (r *renderer) initStyles() {
	c := func(code string) lipgloss.Color { return lipgloss.Color(code) }
	userBg := c("237")                                                            // prompt-row highlight
	r.user = lipgloss.NewStyle().Foreground(c("6")).Bold(true).Background(userBg) // cyan ❯ on highlight
	r.userRow = lipgloss.NewStyle().Background(userBg)
	r.claude = lipgloss.NewStyle().Foreground(c("5")).Bold(true)    // magenta
	r.tool = lipgloss.NewStyle().Foreground(c("3")).Bold(true)      // yellow
	r.subnt = lipgloss.NewStyle().Foreground(c("4")).Bold(true)     // blue
	r.think = lipgloss.NewStyle().Foreground(c("243")).Italic(true) // medium gray, readable but secondary
	r.ok = lipgloss.NewStyle().Foreground(c("2")).Bold(true)        // green
	r.bad = lipgloss.NewStyle().Foreground(c("1")).Bold(true)       // red
	r.body = lipgloss.NewStyle().Foreground(c("15"))                // tool result body: bright white
	r.brief = lipgloss.NewStyle().Foreground(c("6")).Bold(true)     // delegated instruction's ❯: the prompt's cyan without the prompt row's highlight
	r.args = lipgloss.NewStyle().Foreground(c("248"))               // tool args parenthetical: light gray
	r.link = lipgloss.NewStyle().Foreground(c("80"))                // hyperlink text: sky cyan, distinct from glamour's heading blue (39) (underline omitted — lipgloss renders it per-rune)
	r.dim = lipgloss.NewStyle().Foreground(c("8"))
	r.border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c("7")).
		Padding(0, 1)
	r.userBox = r.border // prompt box: border + padding sit on the highlight
	if r.opts.Color {    // guard: BorderBackground emits empty ANSI under the Ascii profile
		r.userBox = r.border.Background(userBg).BorderBackground(userBg)
	}
}

// ── Session header ─────────────────────────────────────────────────────────

func (r *renderer) header(s *model.Session) string {
	m := s.Meta
	// One fact to a line: when the session ran, what it ran on, how big it was,
	// what it spent. The first two shared a line until this version, and at the
	// 100-column fallback width that line had no room for the entrypoint and
	// dropped it — a fact the reader never learned was missing. A line still too
	// long for the box now wraps inside it, so no width loses a field.
	lines := []string{r.claude.Render(headerPrefix + when(m))}
	if ranOn := strings.Join(ranOnParts(m), " · "); ranOn != "" {
		lines = append(lines, r.claude.Render(ranOn))
	}
	lines = append(lines, strings.Join(r.countParts(s), " · "))
	// What the session spent, in the wording the listing's cost channel also
	// prints — one phrasing, so a spend read off a rendered session and one read
	// off a listing cannot differ.
	spent := spend.Line(m.Usage, m.CacheSaving, m.CostUSD, m.LinesAdded, m.LinesRemoved)
	lines = append(lines, r.dim.Render(spent))
	return r.box(strings.Join(lines, "\n")) + "\n"
}

// countParts is the session's size, as the header's third line and the closing
// card both print it. One helper because two surfaces counting one session
// differently is the failure a reader cannot detect — both look like counts.
// selection names the span a narrowed transcript is showing, directly beneath
// the header that counts the whole session, which is where the two would
// otherwise contradict each other. Empty for a whole session, which needs no
// line: a render that shows everything saying so would put chrome on every
// session to describe the ordinary case.
//
// It goes on stdout rather than stderr, unlike the listing's cap note. That note
// sits among line-oriented rows a caller reads a field off; a render is prose
// and a reader who pipes one, pastes one, or reads one back later needs it to
// say what it holds.
func (r *renderer) selection(s *model.Session) string {
	sel := r.opts.Selected
	if sel == nil {
		return ""
	}
	span := fmt.Sprintf("turn %d", sel.From)
	if sel.To != sel.From {
		// A plain hyphen rather than an en dash, so the span reads back as the
		// flag value that produced it: a reader who copies "10-12" out of this
		// line can pass it straight to --turn, where a dash they cannot type
		// would make the line decorative.
		span = fmt.Sprintf("turns %d-%d", sel.From, sel.To)
	}
	return r.dim.Render(fmt.Sprintf("%sshowing %s of %s", assistantIndent, span, plural(s.Meta.NumTurns, "turn"))) + "\n"
}

// Every figure comes off Meta rather than out of Turns, because Turns holds
// what a caller asked to render and Meta holds the session. A narrowed
// transcript counted from Turns reported "1 turn" beside the whole session's
// tokens and dollars, which reads as one turn having cost the lot. The two
// sources agree exactly on a whole session — the session tallies are the
// per-turn counts grouped, not recounted — so this is one source rather than a
// second answer.
func (r *renderer) countParts(s *model.Session) []string {
	parts := []string{plural(s.Meta.NumTurns, "turn"), plural(totalOf(s.Meta.Tools), "tool")}
	if s.Meta.NumSubagents > 0 {
		parts = append(parts, plural(s.Meta.NumSubagents, "subagent"))
	}
	// A call that failed and a call that was never allowed to run ask for
	// different things — a failure is something to go fix, a refusal is a boundary
	// that held — so they are counted apart rather than summed into "errors".
	// Each is dropped when it is zero, the rule every optional figure here follows.
	failed, denied := totalOf(s.Meta.Failures), deniedTotal(s.Meta.Denials)
	if failed > 0 {
		parts = append(parts, r.bad.Render(fmt.Sprintf("%d failed", failed)))
	}
	if denied > 0 {
		parts = append(parts, r.dim.Render(fmt.Sprintf("%d denied", denied)))
	}
	return parts
}

// when phrases when the session ran and how long it was working: each clock
// time dated when the two fall on different days, then the active time — the sum
// of the turns' own spans — with the day count on a session that spanned more
// than one.
//
// The span between the two timestamps is deliberately absent. A session's
// wall-clock span can run many times its active time, so printing it would
// read as the session's duration while actually measuring how long a terminal
// stayed open. Both timestamps stay on the line,
// so a reader who wants the span still has it. PRODUCT.md's header section owns
// the rule.
func when(m model.Meta) string {
	line := fmt.Sprintf("%s → %s", fmtTime(m.Start), fmtTime(m.End))
	if !m.Start.IsZero() && !m.End.IsZero() {
		start, end := m.Start.Local(), m.End.Local()
		endFormat := dateAndTime
		if start.YearDay() == end.YearDay() && start.Year() == end.Year() {
			endFormat = timeOnly
		}
		line = fmt.Sprintf("%s → %s", start.Format(dateAndTime), end.Format(endFormat))
	}
	// No recorded turn means no activity figure, rather than "0m active": the log
	// does not say how long a session with no turn worked, and the rule the model
	// and effort follow is to say nothing where the log is silent.
	if days := len(m.DailyActivity); days > 0 {
		active := spend.Duration(model.ActiveSeconds(m.DailyActivity)) + " active"
		if days > 1 {
			active += " over " + plural(days, "day")
		}
		line += " · " + active
	}
	return line
}

// totalOf sums a tool tally's counts. The tallies are top-level calls grouped by
// tool and identity, so the sum is the session's own call count — the figure the
// header prints beside its turn count.
func totalOf(stats []model.ToolStat) int {
	n := 0
	for _, s := range stats {
		n += s.Count
	}
	return n
}

// deniedTotal sums the refusals. A separate function because a denial is
// counted by a different row type: the same call can appear among the tools it
// ran as and among the refusals it was stopped as, which is why the two are not
// one list.
func deniedTotal(stats []model.DenialStat) int {
	n := 0
	for _, s := range stats {
		n += s.Count
	}
	return n
}

// ranOnParts is what the session ran on — the model, the effort it was run at,
// and where it was started — as the header's second line and the closing card's
// "ran on" row both print it. One helper for the reason countParts is one: two
// surfaces describing the same session differently is a disagreement no reader
// can detect, since each reads as a fact.
//
// A field the log does not carry is left out rather than guessed: "unknown"
// asserted a model the log never named, which is the rule the effort and the
// entrypoint have always followed. The entrypoint is spelled out in full —
// "app→cli" rather than the "+" the listing compresses it to, because a line
// here has room a four-character column does not.
func ranOnParts(m model.Meta) []string {
	var parts []string
	for _, part := range []string{
		trail.Of(m.Model, m.Models),
		trailEffort(m),
		entrypoint.Trail(m.Entrypoint, m.Entrypoints),
	} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// failedAndDenied counts the session's own top-level calls that failed and that
// were refused, matching the tool count beside them — a call made inside a
// subagent is not counted, the rule the per-turn tool count already follows.
//
// A refusal is tested first and on Denial alone: the log marks a refused call as
// an error too, so testing IsError first would file every refusal as a failure.
func failedAndDenied(s *model.Session) (failed, denied int) {
	for _, t := range s.Turns {
		f, d := failedAndDeniedIn(t.Events)
		failed, denied = failed+f, denied+d
	}
	return failed, denied
}

// failedAndDeniedIn counts one stream's own calls, which is what the rule under
// a turn reports. Nested subagent streams are not walked, so a turn's counts add
// up to the session's.
func failedAndDeniedIn(events []model.Event) (failed, denied int) {
	for _, e := range events {
		if e.Kind != model.EventTool || e.Tool == nil {
			continue
		}
		switch {
		case e.Tool.Denial != "":
			denied++
		case e.Tool.IsError:
			failed++
		}
	}
	return failed, denied
}

func (r *renderer) box(content string) string {
	w := r.opts.Width - 2
	if w < 20 {
		w = 20
	}
	return r.border.Width(w).Render(content) + "\n"
}

// Rail bounds a block of lines the way a turn's reply is bounded: a left rail
// down the block and a rule closing it. It is the shape for every multi-line
// block printed under a header, so one shape means one thing wherever a reader
// meets it, and it lives here because the turn's reply is where it comes from.
//
// One line hangs off the closing rule instead, because chrome must never cost
// more lines than the content it bounds. Two or more keep the rail: content on
// the rule there would give the last line different chrome from its siblings
// while saying nothing different about it. No content prints nothing — a bare
// rule bounds nothing, and reads as a header that found nothing rather than one
// with nothing to find.
func Rail(lines []string, dim lipgloss.Style) []string {
	switch len(lines) {
	case 0:
		return nil
	case 1:
		return []string{assistantIndent + dim.Render(railClose) + " " + lines[0]}
	}
	out := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		out = append(out, assistantIndent+dim.Render(railGlyph)+" "+line)
	}
	return append(out, assistantIndent+dim.Render(railClose))
}

// RailWidth is the columns Rail's own prefix takes, which a caller sizing its
// content to a terminal has to subtract before it lays that content out.
//
// It reports the wider of the two prefixes Rail writes: the closing rule a lone
// line hangs off is one column wider than the rail its siblings hang off. The
// caller sizes its rows before it knows how many there will be, so a budget
// taken from the narrower prefix puts a one-line block past the terminal, where
// it wraps onto a line carrying no chrome at all.
func RailWidth() int {
	return max(
		lipgloss.Width(assistantIndent+railGlyph+" "),
		lipgloss.Width(assistantIndent+railClose+" "),
	)
}

// ── Turns ────────────────────────────────────────────────────────────────

func (r *renderer) turn(t model.Turn) string {
	var b strings.Builder
	b.WriteString(r.userPrompt(t.Prompt))

	bar := assistantIndent + r.dim.Render(railGlyph) + " "
	b.WriteString(assistantIndent + r.claude.Render(glyphClaude) + "\n")
	for _, line := range r.events(t.Events, bar, 0) {
		b.WriteString(line + "\n")
	}
	b.WriteString(r.turnClose(t) + "\n")
	return b.String()
}

// userPrompt renders the prompt as a highlighted block enclosed in a rounded
// border, the prompt text prefixed with the ❯ glyph. Wrapped and continuation
// lines hang-indent two columns (the width of "❯ ") so they align under the
// first character of the prompt. The highlight fills the box's inner width so it
// spans edge to edge inside the border.
func (r *renderer) userPrompt(prompt string) string {
	w := r.opts.Width - 2
	if w < 20 {
		w = 20
	}
	inner := w - 2 // text area inside the border's horizontal padding
	lines := wrapPlain(prompt, inner-2)
	for i, line := range lines {
		if i == 0 {
			lines[i] = r.user.Render(glyphUser) + r.userRow.Render(" "+line)
		} else {
			lines[i] = r.userRow.Render("  " + line)
		}
	}
	block := r.userRow.Width(inner).Render(strings.Join(lines, "\n"))
	return r.userBox.Width(w).Render(block) + "\n"
}

func (r *renderer) turnClose(t model.Turn) string {
	parts := []string{r.claude.Render(glyphClaude)}
	if d := fmtDuration(t.Start, t.End); d != "" {
		parts[0] += " " + d
	}
	if t.ToolCount > 0 {
		parts = append(parts, plural(t.ToolCount, "tool"))
	}
	// Split the same way the header and the card are: a call that was refused is
	// not a call that went wrong, and a rule reading "5 errors" sent a reader
	// looking for five things to fix.
	failed, denied := failedAndDeniedIn(t.Events)
	if failed > 0 {
		parts = append(parts, r.bad.Render(fmt.Sprintf("%d failed", failed)))
	}
	if denied > 0 {
		parts = append(parts, r.dim.Render(fmt.Sprintf("%d denied", denied)))
	}
	parts = append(parts, r.turnSpend(t)...)
	return assistantIndent + r.dim.Render(railClose+" ") + strings.Join(parts, " · ")
}

// turnSpend is what the turn's tokens came to, phrased the way the header
// phrases the session's so a reader meets one wording twice rather than two.
// The header answers what the whole session spent; without this a reader with a
// turn in front of them has to find that turn's row in the summary table below
// to learn what it cost, and the table carries no cache share or dollars at all.
//
// Each field is dropped where the log does not support it: no tokens on a turn
// that made no request, no cache share where nothing was cached or read back —
// the rule the header follows — and no dollars for a turn whose models agentry
// holds no rate for, since zero would read as free rather than as unpriced.
func (r *renderer) turnSpend(t model.Turn) []string {
	u := t.Usage
	if u == (model.Usage{}) {
		return nil
	}
	out := []string{fmt.Sprintf("%s in / %s out", spend.Tokens(u.Input), spend.Tokens(u.Output))}
	// Nothing sent to be cached and nothing read back means there is no share to
	// report, where a printed 0% reads as a measurement of caching that did not
	// happen. The header's own wording states this rule; its condition also
	// admits a turn with input and no cache at all, which prints that 0%.
	if u.CacheRead+u.CacheCreate > 0 {
		in := u.Input + u.CacheRead + u.CacheCreate
		out = append(out, fmt.Sprintf("cache %.0f%%", float64(u.CacheRead)/float64(in)*100))
	}
	if t.CostUSD != nil {
		// The tilde marks a figure agentry computed, which is what separates it
		// everywhere from Claude Code's own recorded total.
		out = append(out, "~"+spend.USD(*t.CostUSD))
	}
	for i := range out {
		out[i] = r.dim.Render(out[i])
	}
	return out
}

// events renders an assistant event stream, each line carrying the left-bar
// prefix. depth controls glamour wrap width for nesting.
func (r *renderer) events(events []model.Event, prefix string, depth int) []string {
	var out []string
	avail := r.opts.Width - lipgloss.Width(prefix)
	for _, e := range events {
		switch e.Kind {
		case model.EventText:
			for _, line := range r.markdown(e.Text, avail) {
				out = append(out, prefix+line)
			}
		case model.EventThinking:
			if !r.opts.Channels.Thinking {
				continue
			}
			for i, line := range wrapPlain(e.Text, avail-2) {
				lead := "  "
				if i == 0 {
					lead = glyphThinking + " "
				}
				out = append(out, prefix+r.think.Render(lead+line))
			}
		case model.EventTool:
			if !r.opts.Channels.Tools {
				continue
			}
			out = append(out, r.toolLines(e.Tool, prefix, depth)...)
		}
		out = append(out, strings.TrimRight(prefix, " ")) // spacer
	}
	for len(out) > 0 && out[len(out)-1] == strings.TrimRight(prefix, " ") {
		out = out[:len(out)-1]
	}
	return out
}

// delegation is the bracketed suffix naming what an Agent call handed work to —
// "[Explore@haiku]", the subagent type and the model, either half absent when
// the call did not name it. Only Agent gets one: it is the sole tool whose args
// hide its own identity, since that string is the human description, where a
// Bash line already opens with its program and a Skill line with its skill.
func delegation(t *model.Tool) string {
	if t.Name != "Agent" {
		return ""
	}
	label := t.Identity
	if t.Model != "" {
		label += "@" + t.Model
	}
	if label == "" {
		return "" // an Agent call that named neither says nothing rather than "[]"
	}
	return "[" + label + "]"
}

func (r *renderer) toolLines(t *model.Tool, prefix string, depth int) []string {
	glyph, style := glyphTool, r.tool
	if t.Subagent != nil {
		glyph, style = glyphSubagent, r.subnt
	}
	status := r.ok.Render(glyphOK)
	if t.IsError {
		status = r.bad.Render(glyphErr)
	}
	dur := fmtToolDuration(t.Start, t.End)
	// A refused call says why in place of its duration. The error glyph alone
	// reads as "ran and failed", which sends a reader to fix the wrong thing —
	// and how long a call took before being denied is not worth the space.
	if t.Denial != "" {
		dur = r.bad.Render("denied: " + t.Denial)
	}

	// The call's number, which is the field a reader arrives with from a search:
	// the tool's name says what the call was and only the number says which of the
	// turn's calls it is. It leads the line so a reader scans one column for it
	// rather than reading every line to its end.
	called := ""
	if t.Call > 0 {
		called = r.dim.Render(fmt.Sprintf("call %d", t.Call)) + hitSeparator
	}
	head := fmt.Sprintf("%s%s %s%s%s %s %s",
		prefix, r.dim.Render("╭─"), called, style.Render(glyph+" "+t.Name+delegation(t)),
		r.args.Render("("+argsSummary(t.Args)+")"), status, dur)
	out := []string{strings.TrimRight(head, " ")}
	bodyPrefix := prefix + r.dim.Render("│") + " "

	// The whole argument text, where the parenthetical above left something out
	// and the caller has narrowed to this turn. Above the instruction and the
	// result because it is what the call was asked to do, and those are what came
	// back.
	if r.opts.Selected != nil && r.opts.Channels.ToolResults && argsElided(t.Args) {
		out = append(out, r.toolArgs(t.Args, bodyPrefix)...)
	}

	// The instruction a delegated call was handed, above whatever the call went on
	// to produce. It rides ToolResults rather than a channel of its own because it
	// is this call's body, and the activation line has already flattened it to a
	// description of a few words.
	if t.Prompt != "" && r.opts.Channels.ToolResults {
		out = append(out, r.toolPrompt(t.Prompt, bodyPrefix)...)
	}

	if t.Subagent != nil && r.opts.Channels.Subagents {
		return append(out, r.events(t.Subagent, bodyPrefix, depth+1)...)
	}
	// Otherwise show the (possibly truncated) result body, if enabled. With
	// ToolResults off the activation line stands alone — the notion that the
	// tool fired, without its output.
	if r.opts.Channels.ToolResults {
		return append(out, r.toolBody(t.Result, bodyPrefix)...)
	}
	return out
}

// toolArgsInlineMax bounds the activation line's parenthetical. Sixty characters
// because the line already carries the call's name, status and duration, and the
// summary is there to say which call this is rather than what it did.
const toolArgsInlineMax = 60

// argsSummary is the activation line's parenthetical: the call's arguments
// joined onto one line and cut to a width, ending in "…" where the cut fired.
// The cut is named because a summary showing none of its elision reads as the
// whole argument — which is how a script passed to a shell came to look like a
// one-line call.
func argsSummary(args string) string {
	return truncate(OneLine(args), toolArgsInlineMax)
}

// argsElided reports whether the parenthetical leaves anything out: characters
// past the width, which is all it can lose now that the lines are joined rather
// than ended at the first. A short multi-line argument is shown whole, and the
// caller that used to be told otherwise printed the arguments twice.
func argsElided(args string) bool {
	joined := OneLine(args)
	return truncate(joined, toolArgsInlineMax) != joined
}

// toolArgs lays a call's whole argument text beneath its activation line, for a
// render the caller narrowed to one turn. The parenthetical above is a summary,
// so a search hit reported at `args:65` named a line no render would show, and
// the search-then-read pair in PRODUCT.md's User flows turns on this block.
//
// Labelled with a word rather than marked with a glyph, and the word is the one
// `agentry search` prints for that part, so a hit's location and the block
// holding it read alike. The result body beneath needs no label of its own,
// being the only other body a call can carry.
//
// Uncapped, because it is only reached on a selected turn, where toolBody's cap
// has already lifted.
func (r *renderer) toolArgs(text, prefix string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return append([]string{prefix + r.dim.Render("args")}, r.toolBody(text, prefix)...)
}

// toolPrompt lays out a delegated call's instruction beneath its activation
// line: the ❯ glyph a typed prompt carries, then the text at the rail's width,
// continuation lines hang-indented two columns so they align under the first
// character the way a turn's own prompt block aligns them.
//
// No line cap, where toolBody caps a result at toolBodyMaxLines. A result is
// machine output whose size nothing bounds, so a cap there is what keeps one
// file read from burying the turn that made it; an instruction is text somebody
// wrote, and the first ten lines of a fifty-line brief answer nothing a reader
// came for. Measured on a real session rather than assumed — the commit that
// added this records the count.
func (r *renderer) toolPrompt(text, prefix string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	const hangIndent = "  " // the width of "❯ ", so wrapped lines align under the text
	width := r.opts.Width - lipgloss.Width(prefix) - len(hangIndent)
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		for _, w := range wrapPlain(raw, width) {
			lead := hangIndent
			if len(out) == 0 {
				lead = r.brief.Render(glyphUser) + " "
			}
			out = append(out, strings.TrimRight(prefix+lead+r.body.Render(w), " "))
		}
	}
	return out
}

// gutterWidth is the columns a numbered body spends on its numbers: the digits
// plus the space separating them from the text, and nothing where the body is
// one line and carries no number.
func (r *renderer) gutterWidth(numberW int) int {
	if numberW == 0 {
		return 0
	}
	return numberW + 1
}

// lineNumber is the gutter one display line carries: the source line's number on
// the first display line it takes, and blanks on the lines it wrapped onto, so a
// continuation is not read as a line of its own.
func (r *renderer) lineNumber(number, wrapped, numberW int) string {
	if numberW == 0 {
		return ""
	}
	if wrapped > 0 {
		return strings.Repeat(" ", r.gutterWidth(numberW))
	}
	return r.dim.Render(fmt.Sprintf("%*d", numberW, number)) + " "
}

func (r *renderer) toolBody(text, prefix string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// A body of more than one line is numbered from 1, because that is what a
	// search hit counts from: a hit reported at result:66 located nothing while
	// the body it named printed unnumbered, leaving the reader to count by eye.
	// One line needs no number, being the only one a hit could have named.
	numberW := 0
	if len(lines) > 1 {
		numberW = len(strconv.Itoa(len(lines)))
	}
	width := r.opts.Width - lipgloss.Width(prefix) - r.gutterWidth(numberW)
	var out []string
	limit := r.bodyCap()
	// The cap counts display lines and the remainder counts source lines, so the
	// two are tracked apart. Subtracting one from the other reported a negative
	// remainder on any body whose lines wrapped, which is every long body at a
	// narrow width — a cap that names what it left out cannot name "-6 more
	// lines". PRODUCT.md's Verbosity section owns the rule this restores.
	whole := 0 // source lines printed entire
	for i, raw := range lines {
		room := limit - len(out)
		if room <= 0 {
			break
		}
		wrapped := wrapPlain(raw, width)
		if len(wrapped) > room {
			// A line too long for the room left shows its head rather than being
			// dropped, so a body that is one very long line is not blank. It stays
			// outside whole, which is what makes the remainder name it.
			for j, w := range wrapped[:room] {
				out = append(out, prefix+r.lineNumber(i+1, j, numberW)+r.body.Render(w))
			}
			break
		}
		for j, w := range wrapped {
			out = append(out, prefix+r.lineNumber(i+1, j, numberW)+r.body.Render(w))
		}
		whole++
	}
	if whole < len(lines) {
		out = append(out, prefix+r.dim.Render(fmt.Sprintf("… %s", plural(len(lines)-whole, "more line"))))
	}
	return out
}

// bodyCap is how many display lines a result body may print. The cap answers a
// whole session's worth of bodies burying the transcript inside the output of the
// work; a caller who named one turn has already narrowed, so it lifts there.
//
// Without that, a search hit could name a line inside a body that no render would
// ever show — the flow in PRODUCT.md's User flows section turns on the second
// command being able to display every hit the first one reported.
func (r *renderer) bodyCap() int {
	if r.opts.Selected != nil {
		return math.MaxInt
	}
	return toolBodyMaxLines
}

// Reply lays out one assistant reply the way a rendered turn lays out its own:
// the glyph alone on a line, then the prose through glamour at the given wrap
// width. It returns one string per display line and adds no left rail, which
// the caller supplies — the listing's last-reply channel hangs these lines off
// the same rail and closing rule a turn uses.
//
// It exists so the listing does not grow a second markdown path: a reply read
// off a listing and the same reply under the render path go through this one
// function, so the two cannot drift in how a reply reads. The glamour renderer
// is built per call rather than cached across calls, which costs one
// construction per session in a listing.
func Reply(text string, width int, color bool) []string {
	r := &renderer{opts: Options{Width: width, Color: color}, gcache: map[int]*glamour.TermRenderer{}}
	r.initStyles()
	return append([]string{r.claude.Render(glyphClaude)}, r.markdown(text, width)...)
}

// markdown renders a body through glamour at the given wrap width, returning
// trimmed lines. With color on, markdown links in the prose become OSC 8
// terminal hyperlinks (see linkifyMarkdown).
func (r *renderer) markdown(text string, width int) []string {
	// With color on, strip [text](url) syntax before glamour so it renders the
	// link text as plain prose (no inline URL noise, no wrap-mangled href), then
	// wrap each rendered text in an OSC 8 hyperlink. With color off, leave the
	// source untouched — glamour emits its default "text url" form.
	src, links := text, []mdLinkSpec(nil)
	if r.opts.Color {
		src, links = extractLinks(text)
	}

	var lines []string
	if g := r.glamourFor(width); g == nil {
		lines = wrapPlain(src, width)
	} else if out, err := g.Render(src); err != nil {
		lines = wrapPlain(src, width)
	} else {
		lines = strings.Split(strings.Trim(out, "\n"), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ") // drop glamour's wrap padding
		}
	}
	if len(links) > 0 {
		r.linkifyMarkdown(lines, links)
	}
	return lines
}

// linkPattern matches either a markdown inline link [text](url) (no nested
// brackets, parens, or newline) or a bare scheme://… URL of any scheme (the
// RFC 3986 scheme grammar: a leading letter, then letters/digits/+/-/.). A
// scheme without "//" (mailto:, tel:) is deliberately excluded — too easy to
// false-match ordinary "word:" prose. Alternation is leftmost-first, so at a
// '[' the markdown form wins and a URL inside its (url) is consumed there, never
// re-matched as bare. The bare branch runs to the next whitespace; trimBareURL
// then peels trailing sentence punctuation and unbalanced ')' back off, so a URL
// wrapped in "(see https://x)" and a URL that itself contains balanced parens
// (a Wikipedia article, say) both resolve correctly.
var linkPattern = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]+)\)|[a-zA-Z][a-zA-Z0-9+.-]*://[^\s]+`)

// asciiPunct is the CommonMark set of backslash-escapable ASCII punctuation.
const asciiPunct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// mdLinkSpec is one link's visible text and its href, in source order.
type mdLinkSpec struct{ text, url string }

// extractLinks rewrites text for glamour and records its links in source order.
// A markdown link [text](url) is reduced to its text — glamour renders inline
// links as "text url" with the raw URL shown and no OSC 8, so we keep the URL
// out of glamour and re-attach it afterward (see linkifyMarkdown). A bare URL is
// left in place — glamour renders it as text (coloring http/https via its own
// autolinker, leaving obsidian:// plain) but never emits OSC 8 — with its text
// and href both the URL, trailing punctuation trimmed off the href. A bare
// obsidian://open?…&file=X URI is the exception: its source is replaced by a
// [[X]] wikilink label (see obsidianNote) that links to the full URI, so a note
// reference shows [[Note]] instead of the raw URI string.
func extractLinks(text string) (string, []mdLinkSpec) {
	var links []mdLinkSpec
	src := linkPattern.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "[") {
			sm := linkPattern.FindStringSubmatch(m)
			links = append(links, mdLinkSpec{sm[1], sm[2]})
			return sm[1]
		}
		href := trimBareURL(m)
		trailer := m[len(href):] // punctuation trimmed off the href stays in prose
		if note, ok := obsidianNote(href); ok {
			links = append(links, mdLinkSpec{"[[" + note + "]]", href})
			// glamour parses whatever we feed it, so a note name containing *, `,
			// [] etc. would be mangled and no longer match the recorded label —
			// escape the name so glamour renders it verbatim (brackets survive
			// on their own; see linkifyMarkdown for the post-render match).
			return "[[" + escapeMarkdown(note) + "]]" + trailer
		}
		links = append(links, mdLinkSpec{href, href})
		return m // leave the bare URL in source; glamour renders it verbatim
	})
	return src, links
}

// trimBareURL peels off the run of the greedy bare-URL match that isn't really
// part of the link: trailing sentence punctuation, and a trailing ')' that has
// no matching '(' inside the URL (so "(see https://x)" drops the wrapping paren
// while "…/Foo_(bar)" keeps its balanced one). It loops because the two kinds
// interleave, e.g. "…/foo)." → "…/foo".
func trimBareURL(u string) string {
	for {
		if t := strings.TrimRight(u, ".,;:!?"); t != u {
			u = t
			continue
		}
		if strings.HasSuffix(u, ")") && strings.Count(u, ")") > strings.Count(u, "(") {
			u = u[:len(u)-1]
			continue
		}
		return u
	}
}

// escapeMarkdown backslash-escapes ASCII punctuation so glamour renders the
// string as literal text rather than interpreting *, _, `, [] etc. as markup.
func escapeMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x80 && strings.IndexByte(asciiPunct, byte(r)) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// obsidianNote extracts the wikilink note name from an obsidian://open?…&file=<path>
// URI — the file's base name with folders and any trailing .md dropped, a
// #heading or #^block anchor kept. It reports false for any other obsidian URI
// (no file, or an action other than open), leaving it to render as a plain bare
// URL.
func obsidianNote(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "obsidian" || u.Host != "open" {
		return "", false
	}
	file := u.Query().Get("file")
	if file == "" {
		return "", false
	}
	return strings.TrimSuffix(path.Base(file), ".md"), true
}

// linkifyMarkdown wraps each link's visible text in glamour's rendered lines in
// an OSC 8 hyperlink to its href, editing lines in place. glamour interleaves
// SGR color codes through the text, so we match on the ANSI-stripped text and
// splice the wrapper back onto the styled string by offset. The matched text is
// re-rendered in the dedicated link style (replacing glamour's prose styling).
// Links are matched in source order; a link whose text was split across wrapped
// lines simply stays plain (no hyperlink) rather than blocking later links.
func (r *renderer) linkifyMarkdown(lines []string, links []mdLinkSpec) {
	placed := make([]bool, len(links))
	for n, line := range lines {
		plain, idx := stripANSI(line)
		type span struct{ ss, se, li int }
		var spans []span
		cursor := 0 // plain-byte offset; matches advance it to stay ordered
		for li := range links {
			if placed[li] {
				continue
			}
			at := strings.Index(plain[cursor:], links[li].text)
			if at < 0 {
				continue
			}
			s := cursor + at
			e := s + len(links[li].text)
			spans = append(spans, span{idx[s], idx[e], li})
			placed[li] = true
			cursor = e
		}
		if len(spans) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, sp := range spans {
			b.WriteString(line[last:sp.ss])
			// Through the shared helper, so prose links and the Outputs section
			// cannot emit two different escape sequences for one idea. Unguarded
			// because reaching here already means links were collected, which
			// markdown does only when color is on.
			b.WriteString(r.hyperlink(links[sp.li].text, links[sp.li].url))
			last = sp.se
		}
		b.WriteString(line[last:])
		lines[n] = b.String()
	}
}

// stripANSI returns line with CSI escape sequences removed, plus idx mapping
// each plain byte offset to its offset in the styled line (idx[len(plain)] =
// len(line)), so a match on the plain text can be spliced back onto the styled
// string.
func stripANSI(line string) (string, []int) {
	var plain strings.Builder
	idx := make([]int, 0, len(line))
	for i := 0; i < len(line); {
		if line[i] == 0x1b { // skip a CSI escape: ESC '[' … final byte (0x40–0x7e)
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++ // past '['; its byte 0x5b is itself in the final-byte range
				for j < len(line) && !(line[j] >= 0x40 && line[j] <= 0x7e) {
					j++
				}
				if j < len(line) {
					j++ // include the final byte
				}
			} else {
				j++
			}
			i = j
			continue
		}
		idx = append(idx, i)
		plain.WriteByte(line[i])
		i++
	}
	idx = append(idx, len(line))
	return plain.String(), idx
}

func (r *renderer) glamourFor(width int) *glamour.TermRenderer {
	if width < 20 {
		width = 20
	}
	if g, ok := r.gcache[width]; ok {
		return g
	}
	style := styles.DarkStyleConfig
	if !r.opts.Color {
		style = styles.NoTTYStyleConfig
	}
	// Drop glamour's 2-space document margin so prose hugs the left rail; the
	// rail prefix (assistantIndent + "│ ") supplies all the indentation.
	zero := uint(0)
	style.Document.Margin = &zero
	g, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		g = nil
	}
	r.gcache[width] = g
	return g
}

// ── Outputs ────────────────────────────────────────────────────────────────

// railHeaded bounds a footer section's rows with the rail every block under a
// header hangs off. The first line is the header and the rest are the block: each
// already carries sectionIndent, which the rail's own prefix replaces, so a
// section goes on writing its rows and the chrome is applied in one place.
//
// A section with no rows is returned untouched, since a rule bounding nothing
// reads as a section that found nothing rather than one with nothing to find.
func (r *renderer) railHeaded(section string) string {
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) < 2 {
		return section
	}
	rows := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		rows = append(rows, strings.TrimPrefix(line, sectionIndent))
	}
	return strings.Join(append(lines[:1], Rail(rows, r.dim)...), "\n") + "\n"
}

// outputs lists what the session produced beyond its own transcript: the pull
// requests it opened, then the artifacts it published. Empty when it produced
// neither, which is the signal not to draw the section at all.
//
// The two kinds link differently, following the split assistant prose already
// makes. A pull request is its own URL as the link text, the way a bare URL in
// prose is — the URL already spells the repository and the number, so there is no
// better name to show. An artifact is its title with the URL hidden in the href,
// the way a markdown link is, because a claude.ai artifact id is an opaque uuid
// and showing it in place of a name would be strictly worse than the name.
func (r *renderer) outputs(s *model.Session) string {
	m := s.Meta
	if len(m.PRs) == 0 && len(m.Artifacts) == 0 {
		return ""
	}
	// Two columns of indent, matching the summary table's rows below.
	const indent = "  "
	width := max(r.opts.Width-len(indent), 20)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Outputs ──") + "\n")
	for _, p := range m.PRs {
		u := p.Key()
		b.WriteString(indent + r.maybeLink(truncate(u, width), u) + "\n")
	}
	for _, a := range m.Artifacts {
		href, text := a.Key(), a.Title
		switch {
		case text == "":
			text = href // no title recorded: the URL is the only name there is
		case !r.opts.Color:
			// Plain output has no href to hide the URL in, so it is shown beside the
			// title — the same degradation a markdown link in prose makes.
			text += "  " + href
		}
		b.WriteString(indent + r.maybeLink(truncate(text, width), href) + "\n")
	}
	return r.railHeaded(b.String())
}

// files lists what the session modified, from Claude Code's own file-history
// records rather than from tool arguments — so a file a shell command rewrote
// appears here, and a session whose log carries no such record lists nothing
// rather than claiming it changed nothing.
//
// Capped because the list is unbounded: a session can touch far more files
// than fit here, and --format json carries every path, so the cap costs a
// reader nothing they cannot recover.
func (r *renderer) files(s *model.Session) string {
	paths := s.Meta.Files
	if len(paths) == 0 {
		return ""
	}
	width := max(r.opts.Width-RailWidth(), minContentWidth)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Files ──") + "\n")
	shown := paths
	if len(shown) > filesShown {
		shown = shown[:filesShown]
	}
	for _, f := range shown {
		// Truncated from the left: what distinguishes one modified file from another
		// is its tail, the same reason the listing's files channel cuts that way.
		b.WriteString(sectionIndent + truncateLeft(f, width) + "\n")
	}
	if rest := len(paths) - len(shown); rest > 0 {
		b.WriteString(r.dim.Render(fmt.Sprintf("%s…  (%s)", sectionIndent, plural(rest, "more file"))) + "\n")
	}
	return r.railHeaded(b.String())
}

// identities tallies which skills, agents and commands ran, through the same
// helper the listing's tools channel calls, so neither surface can report one
// session's work differently from the other.
//
// The per-category cap is not optional: an uncapped line can run far longer
// than a small session's whole transcript.
func (r *renderer) identities(s *model.Session) string {
	lines := breakdown.Lines(breakdown.Tally{Calls: s.Meta.Tools, Failures: s.Meta.Failures, Denials: s.Meta.Denials}, identityEntriesShown)
	if len(lines) == 0 {
		return ""
	}
	width := max(r.opts.Width-RailWidth(), minContentWidth)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Tools (by identity) ──") + "\n")
	for _, line := range lines {
		b.WriteString(sectionIndent + truncate(line, width) + "\n")
	}
	return r.railHeaded(b.String())
}

// dayByDay splits the session's turns and active time across the days it ran on.
//
// It prints only on a session that spanned more than one day, where the figure a
// reader wants — four days of work, or four days of leaving a terminal open — is
// the one a single span cannot give. On a single-day session the header's active
// figure already says it, and a one-row section would repeat it.
func (r *renderer) dayByDay(s *model.Session) string {
	days := s.Meta.DailyActivity
	if len(days) < 2 {
		return ""
	}
	_, _, byDay, _, _ := priceSession(s.Meta.DailyUsage)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Day by day ──") + "\n")
	for _, d := range days {
		line := fmt.Sprintf("%s%s  %4d %-6s %7s",
			sectionIndent, d.Day, d.Turns, turnNoun(d.Turns), spend.Duration(d.ActiveSeconds))
		// A day agentry could price carries what it cost; one it could not carries
		// nothing, rather than a zero that would read as a free day.
		if usd := byDay[d.Day]; usd > 0 {
			line += fmt.Sprintf("  %9s", "~"+spend.USD(usd))
		}
		b.WriteString(line + "\n")
	}
	return r.railHeaded(b.String())
}

// turnNoun is the bare noun plural() would attach to a count, for a column that
// aligns the number itself and so cannot take the two as one string.
func turnNoun(n int) string {
	if n == 1 {
		return "turn"
	}
	return "turns"
}

// trailEffort phrases the reasoning effort as the header does — "high effort"
// rather than a bare "high", which beside a model name would not say high what.
func trailEffort(m model.Meta) string {
	if e := trail.Of(m.Effort, m.Efforts); e != "" {
		return e + " effort"
	}
	return ""
}

// card closes the render by naming the session and restating its size: the id to
// render or resume it by, the words it is called, the directory it ran in, and
// the conversation root it shares with any fork of itself.
//
// It is the one place a header fact is repeated, and the repetition is the
// point. A large session runs to thousands of lines, so by the time a reader
// reaches the end, the header is out of reach exactly when they want to cite,
// resume or re-render what they just read. Nothing else
// in the render names the session at all. PRODUCT.md's Output section owns the
// exception to the header-or-footer rule.
func (r *renderer) card(s *model.Session) string {
	m := s.Meta
	if m.ID == "" {
		return ""
	}
	width := max(r.opts.Width-RailWidth()-cardLabelWidth, minContentWidth)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Session ──") + "\n")
	row := func(label, value string, fromLeft bool) {
		if value == "" {
			return
		}
		if fromLeft {
			value = truncateLeft(value, width)
		} else {
			value = truncate(value, width)
		}
		b.WriteString(sectionIndent + r.dim.Render(fmt.Sprintf("%-*s", cardLabelWidth, label)) + value + "\n")
	}
	row("id", m.ID, false)
	row("title", OneLine(m.Title), false)
	// The directory is cut from the left, like every other path here: what names
	// one repository against another is the tail.
	row("project", m.Cwd, true)
	row("root", m.RootUUID, false)
	// Cut from the left like the project row: the file's own name is its tail.
	row("log", m.Path, true)
	// The header's own four lines, restated in full: a card that named the session
	// but not when it ran or what it ran on would send a reader back up the very
	// scroll it exists to spare them.
	row("when", when(m), false)
	row("ran on", strings.Join(ranOnParts(m), " · "), false)
	row("size", strings.Join(r.countParts(s), " · "), false)
	row("spend", r.dim.Render(spend.Line(m.Usage, m.CacheSaving, m.CostUSD, m.LinesAdded, m.LinesRemoved)), false)
	// Both commands take the id in full rather than a prefix: the listing can
	// shorten one because it knows every id beside it, and a single render knows
	// none of them, so a prefix printed here could name two sessions.
	row("render", "agentry "+m.ID, false)
	row("resume", "claude --resume "+m.ID, false)
	return r.railHeaded(b.String())
}

// spent is one axis's share of what the session's tokens are worth: a model, a
// delegation target, or a day.
type spent struct {
	name string
	usd  float64
}

// priceSession values the session's tokens at list prices, split the three ways
// the footer reports. It is agentry's own arithmetic over this log, not Claude
// Code's record: the two usually agree closely and can differ either way on one
// session, which is why every figure derived here is printed with a leading
// "~" and the record is not.
//
// Tokens spent on a model agentry holds no price for are left out of every total
// and named instead, the rule the cost roll-up already follows — counting them
// at zero would report a session as cheaper than it was.
func priceSession(daily []model.DailyUsage) (byModel, byAgent []spent, byDay map[string]float64, total float64, unpriced []string) {
	models, agents := map[string]float64{}, map[string]float64{}
	byDay = map[string]float64{}
	seenUnpriced := map[string]bool{}
	for _, d := range daily {
		usd, ok := price.Of(d.Model, d.Usage)
		if !ok {
			if d.Model != "" && !seenUnpriced[d.Model] {
				seenUnpriced[d.Model] = true
				unpriced = append(unpriced, d.Model)
			}
			continue
		}
		agent := d.Agent
		if agent == "" {
			// The main thread is the majority of every session, so it is named rather
			// than left blank: an axis missing its largest row reads as a breakdown of
			// the delegated part alone.
			agent = "main"
		}
		models[d.Model] += usd
		agents[agent] += usd
		byDay[d.Day] += usd
		total += usd
	}
	return sortedSpend(models), sortedSpend(agents), byDay, total, unpriced
}

// sortedSpend orders an axis by dollars descending, then by name, so the row a
// reader wants first is first and two runs of one session print alike.
func sortedSpend(m map[string]float64) []spent {
	out := make([]spent, 0, len(m))
	for name, usd := range m {
		out = append(out, spent{name, usd})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].usd != out[j].usd {
			return out[i].usd > out[j].usd
		}
		return out[i].name < out[j].name
	})
	return out
}

// joinSpend formats an axis inline, keeping at most max entries and naming the
// remainder, the way the tool tally caps its own categories.
func joinSpend(entries []spent, max int) string {
	shown, hidden := entries, 0
	if max > 0 && len(entries) > max {
		shown, hidden = entries[:max], len(entries)-max
	}
	parts := make([]string, 0, len(shown)+1)
	for _, e := range shown {
		parts = append(parts, fmt.Sprintf("%s ~%s", e.name, spend.USD(e.usd)))
	}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", hidden))
	}
	return strings.Join(parts, " · ")
}

// cost is the footer's money section: what Claude Code recorded, what this log's
// tokens are worth at list prices, and which model, which delegation and what
// unit of work that price went to.
//
// It exists because the recorded figure answers the question for only a
// minority of sessions, and answers only "how much", never "on what".
// The two figures are printed on separate rows rather than blended: a record and
// an estimate that differ by a few percent must be tellable apart, which is what
// the "~" marks.
func (r *renderer) cost(s *model.Session) string {
	m := s.Meta
	byModel, byAgent, _, total, unpriced := priceSession(m.DailyUsage)
	if m.CostUSD == nil && total == 0 {
		return ""
	}
	width := max(r.opts.Width-RailWidth()-cardLabelWidth, minContentWidth)

	var b strings.Builder
	b.WriteString(r.dim.Render("── Cost ──") + "\n")
	row := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString(sectionIndent + r.dim.Render(fmt.Sprintf("%-*s", cardLabelWidth, label)) + truncate(value, width) + "\n")
	}
	if m.CostUSD != nil {
		row("recorded", spend.USD(*m.CostUSD))
	}
	if total > 0 {
		row("priced", "~"+spend.USD(total)+r.dim.Render("   at list prices, from this log's tokens"))
		row("by model", joinSpend(byModel, identityEntriesShown))
		row("by agent", joinSpend(byAgent, identityEntriesShown))

		// What the money bought, which is the figure that carries between sessions:
		// a total says what one session cost, a rate says whether it was expensive.
		var each []string
		if n := len(s.Turns); n > 0 {
			each = append(each, "~"+spend.USD(total/float64(n))+" /turn")
		}
		if secs := model.ActiveSeconds(m.DailyActivity); secs > 0 {
			each = append(each, "~"+spend.USD(total/(float64(secs)/3600))+" /active hour")
		}
		if m.CacheSaving != nil && m.CacheSaving.SavedUSD() > 0 {
			each = append(each, "caching saved ~"+spend.USD(m.CacheSaving.SavedUSD()))
		}
		row("each", strings.Join(each, "  ·  "))
	}
	// Named rather than counted at zero, so a session priced short says so.
	if len(unpriced) > 0 {
		row("unpriced", strings.Join(unpriced, ", ")+r.dim.Render("   no list price held for this model"))
	}
	return r.railHeaded(b.String())
}

// truncateLeft cuts s to limit runes from the left, keeping the tail. Paths are
// cut this way because the tail is what distinguishes one from another; the
// listing cuts its own path columns the same way.
func truncateLeft(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return "…" + string(r[len(r)-(limit-1):])
}

// maybeLink hyperlinks text when color is on and leaves it plain when off. The
// escape is invisible in a terminal but is literal bytes in a pipe or a file, and
// plain output's contract is plain text.
func (r *renderer) maybeLink(text, url string) string {
	if !r.opts.Color {
		return text
	}
	return r.hyperlink(text, url)
}

// hyperlink wraps text in an OSC 8 terminal hyperlink to url, in the link style.
// It carries no color policy of its own — callers that render in both modes gate
// it through maybeLink — so the escape sequence has exactly one home and prose
// links and the Outputs section cannot drift into emitting different bytes.
func (r *renderer) hyperlink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + r.link.Render(text) + "\x1b]8;;\x1b\\"
}

// ── Summary table (metrics channel) ────────────────────────────────────────

func (r *renderer) summary(s *model.Session) string {
	if len(s.Turns) == 0 {
		return ""
	}
	type row struct {
		n     int
		tok   int
		tools int
		label string
	}
	var rows []row
	total := 0
	for i, t := range s.Turns {
		tok := t.Usage.Input + t.Usage.Output
		total += tok
		rows = append(rows, row{i + 1, tok, t.ToolCount, OneLine(t.Prompt)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].tok > rows[j].tok })

	var b strings.Builder
	b.WriteString(r.dim.Render("── Summary (by token cost) ──") + "\n")
	b.WriteString(r.dim.Render("    % tok    tokens  tools  step") + "\n")
	limit := min(len(rows), 8)
	for _, rw := range rows[:limit] {
		pct := 0.0
		if total > 0 {
			pct = float64(rw.tok) / float64(total) * 100
		}
		// The label's budget is measured off the chrome actually written beside it
		// — the rail the section hangs off, the three figure columns, and the turn
		// number, which is as wide as the number is. A budget stated as its own
		// number drifted from those columns and put the row past the terminal,
		// where it wrapped onto a line carrying none of the section's rail.
		figures := fmt.Sprintf("%5.1f%%  %8s  %5d  ", pct, spend.Tokens(rw.tok), rw.tools)
		step := fmt.Sprintf("%d. ", rw.n)
		label := truncate(rw.label, max(r.opts.Width-RailWidth()-lipgloss.Width(figures)-lipgloss.Width(step), minContentWidth))
		b.WriteString(sectionIndent + figures + step + label + "\n")
	}
	if rest := len(rows) - limit; rest > 0 {
		b.WriteString(r.dim.Render(fmt.Sprintf("  …  (%s)", plural(rest, "more step"))) + "\n")
	}
	return r.railHeaded(b.String())
}

// ── Formatting helpers ──────────────────────────────────────────────────────

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "??:??"
	}
	return t.Local().Format("15:04")
}

func fmtDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	secs := int(end.Sub(start).Seconds())
	if secs < 0 {
		return ""
	}
	// The shape is shared with the cost roll-up's elapsed column; an absent or
	// backwards span stays blank here, which that column has no case for.
	return spend.Duration(secs)
}

func fmtToolDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	secs := end.Sub(start).Seconds()
	if secs < 0 {
		return ""
	}
	switch {
	case secs < 10:
		return fmt.Sprintf("%.1fs", secs)
	case secs < 60:
		return fmt.Sprintf("%.0fs", secs)
	}
	mins, rem := int(secs)/60, int(secs)%60
	if mins < 60 {
		if rem == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%02ds", mins, rem)
	}
	return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// OneLine puts text on one line by joining its lines, not by ending at the
// first. Ending there deleted every line after it with nothing to mark that they
// went: a session titled by a multi-line prompt listed under its opening words
// and read as a session about them. Joined, whatever will not fit is cut by the
// column the caller applies next, and that cut ends in an ellipsis.
//
// Runs of whitespace collapse with the line breaks, since a title or a prompt
// laid out for reading — an indented list, a table's padding — becomes a row of
// gaps once its lines are joined.
//
// Exported because the listing draws the same titles and prompts into columns of
// its own. How a title reaches one line is one rule on both surfaces, and a
// second copy of it can only come to differ from the first.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate cuts s to limit runes, the ellipsis counted among them. Counting it
// is what makes the limit a width a caller can budget against: spending one more
// column than it was given puts a row one past the terminal, where it wraps onto
// a line carrying none of its block's chrome. The listing's own copy and
// truncateLeft below already count it this way.
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// wrapPlain soft-wraps plain text (no ANSI) to maxWidth runes per line.
func wrapPlain(text string, maxWidth int) []string {
	if maxWidth < 10 {
		maxWidth = 10
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		if len([]rune(raw)) <= maxWidth {
			out = append(out, raw)
			continue
		}
		var line strings.Builder
		for _, word := range strings.Fields(raw) {
			switch {
			case line.Len() == 0:
				line.WriteString(word)
			case len([]rune(line.String()))+1+len([]rune(word)) > maxWidth:
				out = append(out, line.String())
				line.Reset()
				line.WriteString(word)
			default:
				line.WriteByte(' ')
				line.WriteString(word)
			}
		}
		if line.Len() > 0 {
			out = append(out, line.String())
		}
	}
	return out
}
