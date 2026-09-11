// Package cost rolls a set of session summaries up into dollars: what a day, a
// week, a month, a model or a session cost, priced from the tokens each response
// spent rather than read from Claude Code's own per-session record.
//
// It is the aggregating counterpart to the spend package, which phrases one
// session's figures. The two do not overlap: spend never prices anything and
// this never composes a session's line.
package cost

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/price"
	"github.com/eitanpo/agentry/internal/spend"
	"github.com/muesli/termenv"
)

// The axes --by accepts. Total is the default: one figure for the whole
// selection, which is the question asked without qualification.
const (
	ByTotal   = "total"
	ByDay     = "day"
	ByWeek    = "week"
	ByMonth   = "month"
	ByModel   = "model"
	BySession = "session"
)

// Axes is every --by value, in the order help and completion offer them:
// the default first, then the time buckets coarsening, then the two axes that
// are not time.
var Axes = []string{ByTotal, ByDay, ByWeek, ByMonth, ByModel, BySession}

// unknownDay labels the bucket of a response whose entry carries no timestamp in
// a session that carries none either. The tokens were spent whatever the log
// forgot, so they are named rather than dropped.
const unknownDay = "unknown"

// idPrefix is how many characters of a session id `--by session` prints. It is
// the listing's floor, and 8 hex characters separated all 443 sessions on the
// development machine; --format json carries every id in full.
const idPrefix = 8

// Bucket is one row of a roll-up, or the total beneath them.
type Bucket struct {
	// Key is the axis value: a date, a week's Monday, a month, a model id, or a
	// session id. Empty on the total, which is every key at once.
	Key string `json:"key,omitempty"`
	// Label is the human name of a key that is an opaque id — a session's title,
	// and nothing else. Empty on every other axis, where the key reads for itself.
	Label    string      `json:"label,omitempty"`
	Sessions int         `json:"sessions"`
	Usage    model.Usage `json:"usage"`
	// CostUSD is what the tokens above cost at list price. An estimate rather than
	// a bill: it prices the responses the transcript records, which is short of
	// what Claude Code pays where it makes a request without writing an entry, and
	// ahead of Claude Code's own record where that record covers only part of a
	// session. It excludes any tokens spent on a model with no price here, which
	// UnpricedModels names.
	CostUSD float64 `json:"costUSD"`
}

// Unpriced is a model the table holds no rate for, reported so its tokens are
// visibly missing from the dollars rather than silently counted as free.
type Unpriced struct {
	Model    string      `json:"model"`
	Sessions int         `json:"sessions"`
	Usage    model.Usage `json:"usage"`
}

// Recorded is Claude Code's own dollar total over the sessions it kept a record
// for — the external figure a computed one is checked against.
//
// It covers only the sessions lying wholly inside the window, because a record
// is one number for a whole session: a session straddling the window's edge has
// no share of its record to contribute, so counting it whole would overstate the
// comparison exactly where the computed figure was cut down.
type Recorded struct {
	Sessions int     `json:"sessions"`
	CostUSD  float64 `json:"costUSD"`
}

// Report is a whole roll-up: the rows, the total they sum to, and what a reader
// needs in order to know what the figure is not.
type Report struct {
	By             string     `json:"by"`
	Buckets        []Bucket   `json:"buckets"`
	Total          Bucket     `json:"total"`
	Recorded       *Recorded  `json:"recorded,omitempty"`
	UnpricedModels []Unpriced `json:"unpricedModels,omitempty"`
	// PricesVerified is the day the rate table was last checked against its
	// sources, so a caller can tell how old the prices it was handed are.
	PricesVerified string `json:"pricesVerified"`
}

// Build prices every session's per-day-per-model tokens, keeps the days inside
// the window, and groups what is left on the named axis.
//
// The bounds are read as the whole local day each falls on, since a day is the
// finest bucket a response can be attributed to — so `--since 24h` at noon
// counts all of yesterday too, which is the honest reading of a bound that lands
// inside a bucket. A day the log could not place is dropped once a bound is
// given: a window is a claim about time, and such a response cannot be shown to
// satisfy it — the rule the line-count filters already follow.
func Build(sums []model.Summary, by string, since, until time.Time) Report {
	sinceDay, untilDay := dayString(since), dayString(until)
	r := Report{By: by, PricesVerified: price.VerifiedOn}
	rows := map[string]*Bucket{}
	seen := map[string]map[string]bool{} // bucket key -> session ids counted
	unpriced := map[string]*Unpriced{}
	var order []string

	for _, s := range sums {
		for _, d := range s.DailyUsage {
			if !inWindow(d.Day, sinceDay, untilDay) {
				continue
			}
			usd, priced := price.Of(d.Model, d.Usage)
			if !priced {
				u, ok := unpriced[d.Model]
				if !ok {
					u = &Unpriced{Model: d.Model}
					unpriced[d.Model] = u
				}
				u.Usage.Add(d.Usage)
			}
			key, label := bucketOf(by, d, s)
			b, ok := rows[key]
			if !ok {
				b = &Bucket{Key: key, Label: label}
				rows[key] = b
				seen[key] = map[string]bool{}
				order = append(order, key)
			}
			b.Usage.Add(d.Usage)
			b.CostUSD += usd
			if !seen[key][s.ID] {
				seen[key][s.ID] = true
				b.Sessions++
			}
			r.Total.Usage.Add(d.Usage)
			r.Total.CostUSD += usd
		}
	}
	// The total's session count is the sessions that contributed, which is not the
	// sum of the rows': one session spans several days and is counted in each.
	total := map[string]bool{}
	for _, ids := range seen {
		for id := range ids {
			total[id] = true
		}
	}
	r.Total.Sessions = len(total)

	for _, key := range order {
		r.Buckets = append(r.Buckets, *rows[key])
	}
	sortBuckets(r.Buckets, by)
	for _, m := range unpriced {
		for _, s := range sums {
			if usesModel(s, m.Model) {
				m.Sessions++
			}
		}
		r.UnpricedModels = append(r.UnpricedModels, *m)
	}
	sort.Slice(r.UnpricedModels, func(i, j int) bool { return r.UnpricedModels[i].Model < r.UnpricedModels[j].Model })
	r.Recorded = recordedOf(sums, sinceDay, untilDay)
	if by == ByTotal {
		r.Buckets = nil // the total is the whole answer; a one-row table repeats it
	}
	return r
}

// usesModel reports whether any of the session's tokens went to that model.
func usesModel(s model.Summary, modelID string) bool {
	for _, d := range s.DailyUsage {
		if d.Model == modelID {
			return true
		}
	}
	return false
}

// bucketOf is the key a response falls under on the chosen axis, with the label
// a key needs when it is an opaque id.
func bucketOf(by string, d model.DailyUsage, s model.Summary) (key, label string) {
	switch by {
	case ByDay:
		return dayLabel(d.Day), ""
	case ByWeek:
		return weekOf(d.Day), ""
	case ByMonth:
		return monthOf(d.Day), ""
	case ByModel:
		return d.Model, ""
	case BySession:
		return s.ID, s.Title
	}
	return ByTotal, ""
}

// dayLabel names a day bucket, standing in for a response the log could not
// place in time.
func dayLabel(day string) string {
	if day == "" {
		return unknownDay
	}
	return day
}

// weekOf is the Monday of the week a day falls in, which is where Claude Code's
// own week starts and where the tools that price its logs start theirs.
func weekOf(day string) string {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return unknownDay
	}
	back := (int(t.Weekday()) + 6) % 7 // Monday 0 … Sunday 6
	return t.AddDate(0, 0, -back).Format("2006-01-02")
}

// monthOf is the calendar month a day falls in.
func monthOf(day string) string {
	if len(day) < 7 {
		return unknownDay
	}
	return day[:7]
}

// dayString is the local calendar day an instant falls on, empty for the zero
// time — which is how an unset bound reads as no bound.
func dayString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// inWindow compares a day against the bounds as dates, which a lexical
// comparison does correctly for this format: the fields run widest first and are
// zero-padded.
func inWindow(day, sinceDay, untilDay string) bool {
	if sinceDay == "" && untilDay == "" {
		return true
	}
	if day == "" {
		return false
	}
	if sinceDay != "" && day < sinceDay {
		return false
	}
	return untilDay == "" || day <= untilDay
}

// sortBuckets orders the rows by what the axis is read for: time chronologically,
// so the most recent lands nearest the prompt as a listing's does, and the two
// axes that are questions about size by size, most expensive first.
func sortBuckets(bs []Bucket, by string) {
	switch by {
	case ByModel, BySession:
		sort.SliceStable(bs, func(i, j int) bool {
			if bs[i].CostUSD != bs[j].CostUSD {
				return bs[i].CostUSD > bs[j].CostUSD
			}
			return bs[i].Key < bs[j].Key
		})
	default:
		sort.SliceStable(bs, func(i, j int) bool { return bs[i].Key < bs[j].Key })
	}
}

// recordedOf sums Claude Code's own totals over the sessions lying wholly inside
// the window. Nil when no session qualifies, so the caller says nothing rather
// than printing a zero that reads as "Claude Code recorded nothing".
func recordedOf(sums []model.Summary, sinceDay, untilDay string) *Recorded {
	var out Recorded
	for _, s := range sums {
		if s.CostUSD == nil {
			continue
		}
		if !inWindow(dayString(s.Start), sinceDay, untilDay) ||
			!inWindow(dayString(s.End), sinceDay, untilDay) {
			continue
		}
		out.Sessions++
		out.CostUSD += *s.CostUSD
	}
	if out.Sessions == 0 {
		return nil
	}
	return &out
}

// Options configures the text form.
type Options struct {
	Width int
	Color bool
}

// RenderJSON writes the report as an indented object — the roll-up's
// machine-readable form. An empty selection still emits the object with a zeroed
// total, so a caller piping into jq reads the exit code rather than guarding the
// shape.
func RenderJSON(w io.Writer, r Report) error {
	if r.Buckets == nil && r.By != ByTotal {
		r.Buckets = []Bucket{} // an empty array, not null
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// column widths: the key column stretches to its contents, the three numeric
// columns are fixed and right-aligned so figures line up down the page.
const (
	sessionsW = 8
	tokensW   = 11
	costW     = 11
	gap       = 2
)

// Render writes the rows, the total they sum to, and the notes that say what the
// figure is and is not.
func Render(w io.Writer, r Report, opts Options) error {
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	head := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	note := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	keyW := len(axisHeading(r.By))
	labelW := 0
	for _, b := range r.Buckets {
		if n := utf8.RuneCountInString(b.Key); n > keyW {
			keyW = n
		}
		if n := utf8.RuneCountInString(displayTitle(b.Label)); n > labelW {
			labelW = n
		}
	}
	if r.By == BySession {
		keyW = idPrefix
		if labelW < len("Title") {
			labelW = len("Title")
		}
		if max := labelCap(opts.Width, keyW); labelW > max {
			labelW = max
		}
	}
	if keyW < len("Total") {
		keyW = len("Total")
	}

	var out strings.Builder
	if len(r.Buckets) > 0 {
		out.WriteString(head.Render(headerRow(r.By, keyW, labelW)) + "\n")
		for _, b := range r.Buckets {
			out.WriteString(row(r.By, b, keyW, labelW) + "\n")
		}
		out.WriteString("\n")
	}
	out.WriteString(totalRow(r, keyW, labelW) + "\n")
	for _, line := range notes(r) {
		out.WriteString(note.Render(line) + "\n")
	}
	_, err := io.WriteString(w, out.String())
	return err
}

// axisHeading names the key column after the axis it holds.
func axisHeading(by string) string {
	switch by {
	case ByDay:
		return "Day"
	case ByWeek:
		return "Week"
	case ByMonth:
		return "Month"
	case ByModel:
		return "Model"
	case BySession:
		return "Session"
	}
	return "Total"
}

// labelCap bounds a session title so the numeric columns survive a narrow
// terminal, the listing's rule applied to a table with one variable column.
func labelCap(width, keyW int) int {
	if width <= 0 {
		width = 100
	}
	cap := width - (keyW + tokensW + costW + 3*gap)
	if cap < 10 {
		cap = 10
	}
	return cap
}

func headerRow(by string, keyW, labelW int) string {
	if by == BySession {
		return padRight("Session", keyW) + strings.Repeat(" ", gap) +
			padRight("Title", labelW) + strings.Repeat(" ", gap) +
			padLeft("Tokens", tokensW) + strings.Repeat(" ", gap) + padLeft("Cost", costW)
	}
	return padRight(axisHeading(by), keyW) + strings.Repeat(" ", gap) +
		padLeft("Sessions", sessionsW) + strings.Repeat(" ", gap) +
		padLeft("Tokens", tokensW) + strings.Repeat(" ", gap) + padLeft("Cost", costW)
}

func row(by string, b Bucket, keyW, labelW int) string {
	tokens := spend.Tokens(b.Usage.Input + b.Usage.Output + b.Usage.CacheRead + b.Usage.CacheCreate)
	if by == BySession {
		return padRight(shortID(b.Key), keyW) + strings.Repeat(" ", gap) +
			padRight(cut(displayTitle(b.Label), labelW), labelW) + strings.Repeat(" ", gap) +
			padLeft(tokens, tokensW) + strings.Repeat(" ", gap) + padLeft(spend.USD(b.CostUSD), costW)
	}
	return padRight(b.Key, keyW) + strings.Repeat(" ", gap) +
		padLeft(fmt.Sprintf("%d", b.Sessions), sessionsW) + strings.Repeat(" ", gap) +
		padLeft(tokens, tokensW) + strings.Repeat(" ", gap) + padLeft(spend.USD(b.CostUSD), costW)
}

// totalRow prints the figure every row sums to. On the session axis the session
// count has no column of its own — the rows are sessions — so it is spelled into
// the title column, where it says what the total covers.
func totalRow(r Report, keyW, labelW int) string {
	t := r.Total
	tokens := spend.Tokens(t.Usage.Input + t.Usage.Output + t.Usage.CacheRead + t.Usage.CacheCreate)
	if r.By == BySession {
		return padRight("Total", keyW) + strings.Repeat(" ", gap) +
			padRight(cut(sessionCount(t.Sessions), labelW), labelW) + strings.Repeat(" ", gap) +
			padLeft(tokens, tokensW) + strings.Repeat(" ", gap) + padLeft(spend.USD(t.CostUSD), costW)
	}
	return padRight("Total", keyW) + strings.Repeat(" ", gap) +
		padLeft(fmt.Sprintf("%d", t.Sessions), sessionsW) + strings.Repeat(" ", gap) +
		padLeft(tokens, tokensW) + strings.Repeat(" ", gap) + padLeft(spend.USD(t.CostUSD), costW)
}

// notes are what a reader needs in order not to misread the figure: that it is
// computed and an estimate, what Claude Code's own record says over the part of
// the window it can be compared on, and which models are in the tokens but in
// none of the dollars.
func notes(r Report) []string {
	out := []string{fmt.Sprintf(
		"computed from the transcript at %s list prices — an estimate, not a bill", r.PricesVerified)}
	if r.Recorded != nil {
		out = append(out, fmt.Sprintf("Claude Code recorded %s for the %s it kept a record for",
			spend.USD(r.Recorded.CostUSD), sessionCount(r.Recorded.Sessions)))
	}
	for _, m := range r.UnpricedModels {
		out = append(out, fmt.Sprintf("%s has no price here: its %s tokens over %s are in the tokens above and in no dollar figure",
			m.Model, spend.Tokens(m.Usage.Input+m.Usage.Output+m.Usage.CacheRead+m.Usage.CacheCreate), sessionCount(m.Sessions)))
	}
	return out
}

// displayTitle flattens a title to one line, since a prompt-derived one carries
// newlines and would break the row it sits in.
func displayTitle(title string) string {
	return strings.TrimSpace(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(title))
}

// padRight and padLeft measure in runes, not bytes: a session title carries
// whatever a person typed, and a byte count would push every column right of a
// multibyte character out of line.
func padRight(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func padLeft(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}

// shortID abbreviates a session id to its first characters, with no ellipsis:
// what is printed has to resolve when passed back to `agentry <id>`, and a cell
// ending in an ellipsis is one character short of the id it claims to be.
func shortID(id string) string {
	r := []rune(id)
	if len(r) <= idPrefix {
		return id
	}
	return string(r[:idPrefix])
}

// cut shortens a cell to the column, keeping the head: a title's first words are
// what name it.
func cut(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// The three scopes the no-flag summary answers at once. A person asks what they
// are spending, not what one directory spent over all time, so the verb answers
// the session they were just in, this directory's whole history, and the
// machine's recent window together.
const (
	ScopeSession = "session"
	ScopeFolder  = "folder"
	ScopeMachine = "machine"
)

// Scope is one row of that summary: what a scope spent, and the window it
// counted.
type Scope struct {
	Scope string `json:"scope"`
	// Label is the session's title, and is carried for that scope alone — the
	// other two are named by their session count and their window, which the two
	// fields below already hold.
	Label string `json:"label,omitempty"`
	// Since is the first day the scope counts, on the machine scope alone. It is a
	// date rather than the words "last thirty days" because a day is the finest
	// bucket a response is attributed to, so a thirty-day bound counts thirty-one
	// calendar days; naming the day keeps that out of a reader's head.
	Since    string      `json:"since,omitempty"`
	Sessions int         `json:"sessions"`
	Usage    model.Usage `json:"usage"`
	CostUSD  float64     `json:"costUSD"`
}

// Overview is the whole summary: the scopes, and the same notes the roll-up
// carries so a reader of either knows what the figure is not.
type Overview struct {
	Scopes         []Scope    `json:"scopes"`
	Recorded       *Recorded  `json:"recorded,omitempty"`
	UnpricedModels []Unpriced `json:"unpricedModels,omitempty"`
	PricesVerified string     `json:"pricesVerified"`
}

// BuildOverview prices the three scopes through Build, so a figure read off the
// summary and one read off `--by total` over the same sessions cannot differ.
//
// session is nil and folder empty when this directory has no Claude project;
// those rows are then absent rather than zero, since no session is a different
// fact from no spend. The notes come from the machine scope, the widest of the
// three — a model with no price or a session carrying Claude Code's own record
// is worth naming once, against the largest set that holds it.
func BuildOverview(session *model.Summary, folder, machine []model.Summary, since time.Time) Overview {
	wide := Build(machine, ByTotal, since, time.Time{})
	out := Overview{
		Recorded:       wide.Recorded,
		UnpricedModels: wide.UnpricedModels,
		PricesVerified: price.VerifiedOn,
	}
	if session != nil {
		one := Build([]model.Summary{*session}, ByTotal, time.Time{}, time.Time{}).Total
		out.Scopes = append(out.Scopes, Scope{
			Scope: ScopeSession, Label: displayTitle(session.Title),
			Sessions: one.Sessions, Usage: one.Usage, CostUSD: one.CostUSD,
		})
	}
	if len(folder) > 0 {
		all := Build(folder, ByTotal, time.Time{}, time.Time{}).Total
		out.Scopes = append(out.Scopes, Scope{
			Scope: ScopeFolder, Sessions: all.Sessions, Usage: all.Usage, CostUSD: all.CostUSD,
		})
	}
	out.Scopes = append(out.Scopes, Scope{
		Scope: ScopeMachine, Since: dayString(since),
		Sessions: wide.Total.Sessions, Usage: wide.Total.Usage, CostUSD: wide.Total.CostUSD,
	})
	return out
}

// scopeName heads a summary row, in the second person: the reader is asking what
// they spent, and "This session" answers that where "session" alone reads as a
// column header.
func scopeName(scope string) string {
	switch scope {
	case ScopeSession:
		return "This session"
	case ScopeFolder:
		return "This folder"
	}
	return "This machine"
}

// scopeWindow describes what a row counted, for the two rows whose key does not
// say: a folder's whole history, and the machine's window named by its first day.
func scopeWindow(s Scope) string {
	switch s.Scope {
	case ScopeSession:
		return s.Label
	case ScopeFolder:
		return fmt.Sprintf("%s, all time", sessionCount(s.Sessions))
	}
	if s.Since == "" {
		return fmt.Sprintf("%s, all time", sessionCount(s.Sessions))
	}
	return fmt.Sprintf("%s since %s", sessionCount(s.Sessions), s.Since)
}

func sessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// RenderOverview writes the three-row summary and the notes beneath it.
func RenderOverview(w io.Writer, o Overview, opts Options) error {
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	head := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	note := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	nameW := 0
	for _, s := range o.Scopes {
		if n := utf8.RuneCountInString(scopeName(s.Scope)); n > nameW {
			nameW = n
		}
	}
	// The window column is sized to its own contents rather than given the rest of
	// the line: three rows of text followed by a run of spaces would push the two
	// figures — the reason the summary is printed — to the far right of a wide
	// terminal, where they read as belonging to nothing.
	windowW := 0
	for _, s := range o.Scopes {
		if n := utf8.RuneCountInString(scopeWindow(s)); n > windowW {
			windowW = n
		}
	}
	if max := labelCap(opts.Width, nameW); windowW > max {
		windowW = max
	}
	var out strings.Builder
	for _, s := range o.Scopes {
		tokens := spend.Tokens(s.Usage.Input + s.Usage.Output + s.Usage.CacheRead + s.Usage.CacheCreate)
		out.WriteString(head.Render(padRight(scopeName(s.Scope), nameW)) + strings.Repeat(" ", gap) +
			padRight(cut(scopeWindow(s), windowW), windowW) + strings.Repeat(" ", gap) +
			padLeft(tokens, tokensW) + strings.Repeat(" ", gap) +
			padLeft(spend.USD(s.CostUSD), costW) + "\n")
	}
	out.WriteString("\n")
	for _, line := range notes(Report{
		PricesVerified: o.PricesVerified, Recorded: o.Recorded, UnpricedModels: o.UnpricedModels,
	}) {
		out.WriteString(note.Render(line) + "\n")
	}
	_, err := io.WriteString(w, out.String())
	return err
}

// RenderOverviewJSON writes the summary as an indented object, the machine-
// readable form of the same three rows.
func RenderOverviewJSON(w io.Writer, o Overview) error {
	if o.Scopes == nil {
		o.Scopes = []Scope{}
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
