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
	"os"
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
	ByAgent   = "agent"
	ByProject = "project"
	BySession = "session"
)

// Axes is every --by value, in the order help and completion offer them:
// the default first, then the time buckets coarsening, then the four axes that
// are not time, narrowing.
var Axes = []string{ByTotal, ByDay, ByWeek, ByMonth, ByModel, ByAgent, ByProject, BySession}

// mainThreadAgent names the tokens a session spent itself, on the axis that
// groups the rest by what they were delegated to. It is a row rather than an
// omission so the rows sum to the total and each one's share can be read off
// them; in the local corpus the main thread is the large majority of every
// session, which a delegated-only table would hide.
const mainThreadAgent = "(main thread)"

// unknownProject names the row of a session whose log recorded no working
// directory. Its dollars were spent somewhere, so the row is named rather than
// dropped — the rule unknownDay follows for a response with no timestamp.
const unknownProject = "(unknown)"

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
	// Turns and ActiveSeconds are the work the bucket's dollars bought: the turns
	// that started inside it, and the seconds those turns ran. Both are zero on
	// the model and agent axes, where a turn belongs to no single row — one turn
	// can run on two models and delegate to two agents — so the columns are
	// dropped there rather than filled with a number that divides wrongly.
	Turns         int `json:"turns,omitempty"`
	ActiveSeconds int `json:"activeSeconds,omitempty"`
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

// Spread is how a per-turn cost is distributed across the sessions in the
// window, which one average cannot say: locally the mean turn costs $2.53 and
// the median $0.71, because a few very large turns carry the bill. Reported only
// once enough sessions have turns for a median to mean anything.
type Spread struct {
	Sessions      int     `json:"sessions"`
	MedianPerTurn float64 `json:"medianPerTurn"`
	P90PerTurn    float64 `json:"p90PerTurn"`
}

// Selection is what a roll-up was asked to count: which sessions, over what
// span. It is carried because the table cannot say it otherwise — the rows are
// days or models, and the line beneath them reads "Total", which names no scope
// at all. The same figure means very different things for one folder and for a
// whole machine.
type Selection struct {
	// Scope is the set of sessions in the caller's own words — "this folder",
	// "every project", or the path that was named.
	Scope string `json:"scope"`
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
}

// line says what was priced, for the note that opens a roll-up's footer.
func (sel Selection) line() string {
	return "priced " + sel.Scope + span(sel.Since, sel.Until)
}

// span phrases a pair of day bounds, either of which may be absent. It is shared
// by the roll-up's note and the summary's rows so one window is never described
// two ways in one session's output.
func span(since, until string) string {
	switch {
	case since != "" && until != "":
		return " from " + since + " to " + until
	case since != "":
		return " since " + since
	case until != "":
		return " up to " + until
	}
	return ", all time"
}

// Report is a whole roll-up: the rows, the total they sum to, and what a reader
// needs in order to know what the figure is not.
type Report struct {
	By             string     `json:"by"`
	Selection      *Selection `json:"selection,omitempty"`
	Buckets        []Bucket   `json:"buckets"`
	Total          Bucket     `json:"total"`
	Spread         *Spread    `json:"spread,omitempty"`
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
	spent := map[string]float64{} // session id -> in-window dollars
	worked := map[string]int{}    // session id -> in-window turns
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
			spent[s.ID] += usd
		}
		// Turns attach only to rows the tokens already made, so a roll-up gains no
		// row for a day that held turns and no priced response — the table is about
		// spend, and a zero-dollar row on it reads as a day that cost nothing rather
		// than as one nothing could be priced for.
		for _, a := range s.DailyActivity {
			key, ok := activityKey(by, a.Day, s)
			if !ok || !inWindow(a.Day, sinceDay, untilDay) {
				continue
			}
			worked[s.ID] += a.Turns
			b, ok := rows[key]
			if !ok {
				continue
			}
			b.Turns += a.Turns
			b.ActiveSeconds += a.ActiveSeconds
			r.Total.Turns += a.Turns
			r.Total.ActiveSeconds += a.ActiveSeconds
		}
	}
	r.Spread = spreadOf(spent, worked)
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
	case ByAgent:
		return agentName(d.Agent), ""
	case ByProject:
		return projectName(s.Cwd), ""
	case BySession:
		return s.ID, s.Title
	}
	return ByTotal, ""
}

// projectName is the row a session falls under on the project axis: its working
// directory cut at the first hidden segment, with the home directory
// abbreviated so a column whose rows share a long prefix still reads.
//
// The cut is what folds a repository's worktrees into the repository. A tool
// that checks a branch out beside the repo puts it under a dot-directory —
// `<repo>/.claude-worktrees/<name>` — so cutting there names the repo, while a
// sibling repo under a shared parent is untouched, since no segment between
// them is hidden. Cutting by the selection's own directories instead would let
// one session run from a parent folder swallow every repo beneath it: locally
// a single session in `~/Projects/wix-private` merged three repos into one row
// worth $3,552.
func projectName(cwd string) string {
	if cwd == "" {
		return unknownProject
	}
	return abbrevHome(projectRoot(cwd))
}

// projectRoot cuts a path at its first hidden segment. A path whose only
// non-empty segments are hidden — a dot-directory in the home directory, say —
// keeps that first segment, since cutting it away would name the home directory
// and merge every such directory into one row.
func projectRoot(cwd string) string {
	segs := strings.Split(cwd, "/")
	for i, seg := range segs {
		if !strings.HasPrefix(seg, ".") || seg == "" {
			continue
		}
		if i+1 < len(segs) {
			// Keep the hidden segment only where dropping it would leave nothing but
			// the root or the home directory to name the row by.
			cut := strings.Join(segs[:i], "/")
			if cut == "" || cut == homeDir {
				return strings.Join(segs[:i+1], "/")
			}
			return cut
		}
		return cwd
	}
	return cwd
}

// homeDir is resolved once. A machine with no resolvable home abbreviates
// nothing, which prints the path in full rather than failing.
var homeDir, _ = os.UserHomeDir()

// abbrevHome writes the home directory as "~", the listing's abbreviation.
func abbrevHome(path string) string {
	if homeDir == "" {
		return path
	}
	if path == homeDir {
		return "~"
	}
	if strings.HasPrefix(path, homeDir+"/") {
		return "~" + path[len(homeDir):]
	}
	return path
}

// agentName names the row a response falls under on the agent axis, standing in
// for the main thread, whose records carry no label.
func agentName(agent string) string {
	if agent == "" {
		return mainThreadAgent
	}
	return agent
}

// activityKey is the row a day's turns fall under, and whether the axis takes
// turns at all. The model and agent axes do not: a turn can run on two models
// and delegate to two agents, so it has no one row to be counted in.
func activityKey(by, day string, s model.Summary) (string, bool) {
	switch by {
	case ByDay:
		return dayLabel(day), true
	case ByWeek:
		return weekOf(day), true
	case ByMonth:
		return monthOf(day), true
	case ByProject:
		// A turn belongs to one session, and a session to one directory, so the
		// project axis takes turns where the model and agent axes cannot.
		return projectName(s.Cwd), true
	case BySession:
		return s.ID, true
	case ByTotal:
		return ByTotal, true
	}
	return "", false
}

// minSpreadSessions is how many sessions a distribution needs before it is worth
// printing. Below it the median is one session's own figure restated, which
// reads as a second measurement and is not one.
const minSpreadSessions = 3

// spreadOf is the median and top-decile cost per turn across the sessions that
// both spent and worked inside the window. A session with turns and no priced
// tokens is left out rather than entered as free: its model had no price, and a
// zero would drag the median toward a figure nobody paid.
func spreadOf(spent map[string]float64, worked map[string]int) *Spread {
	var rates []float64
	for id, usd := range spent {
		if n := worked[id]; n > 0 && usd > 0 {
			rates = append(rates, usd/float64(n))
		}
	}
	if len(rates) < minSpreadSessions {
		return nil
	}
	sort.Float64s(rates)
	mid := len(rates) / 2
	median := rates[mid]
	if len(rates)%2 == 0 {
		median = (rates[mid-1] + rates[mid]) / 2
	}
	p90 := rates[len(rates)-1]
	if i := int(float64(len(rates)) * 0.9); i < len(rates) {
		p90 = rates[i]
	}
	return &Spread{Sessions: len(rates), MedianPerTurn: median, P90PerTurn: p90}
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
	case ByModel, ByAgent, ByProject, BySession:
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
	// Chart replaces the rows with a picture of them. Empty and ChartNone both
	// print the table, so a caller that never heard of charts gets one.
	Chart string
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
	turnsW    = 7
	activeW   = 8
	perTurnW  = 8
	gap       = 2
)

// numericWidth is the space the fixed right-hand columns take, each counted with
// the gap that precedes it — what the one variable-width column has to give way
// to on a narrow terminal.
func numericWidth(by string, showTurns bool) int {
	w := (gap + tokensW) + (gap + costW)
	if by != BySession {
		w += gap + sessionsW
	}
	if showTurns {
		w += (gap + turnsW) + (gap + activeW) + (gap + perTurnW)
	}
	return w
}

// barMax and barMin bound the share column. Wider than barMax buys no precision
// a reader uses — the figure beside it is exact — and narrower than barMin the
// eighth-blocks cannot separate a small row from an empty one, so the column is
// dropped instead of drawn misleadingly.
const (
	barMax = 24
	barMin = 6
)

// projectKeyMax bounds the project column. Forty characters holds the tail of
// every local path that names a repository, which is the part that identifies
// it — the leading directories are shared by every row and carry nothing.
const projectKeyMax = 40

// barEighths are the leading fractions of a cell, so a bar ends on an eighth of
// a column rather than rounding to the nearest whole one.
var barEighths = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// barWidth is the room the share column gets after every other column has taken
// what it needs, zero when there is not enough left for one.
func barWidth(width, used int) int {
	if width <= 0 {
		width = 100
	}
	free := width - used - gap
	switch {
	case free < barMin:
		return 0
	case free > barMax:
		return barMax
	}
	return free
}

// bar draws one row's share of the largest row. A row that spent anything at all
// draws at least the narrowest mark: rounding it away would render a row that
// cost real money exactly like one that cost nothing.
func bar(v, max float64, w int) string {
	if w <= 0 || max <= 0 || v <= 0 {
		return ""
	}
	eighths := int(v / max * float64(w) * 8)
	if eighths > w*8 {
		eighths = w * 8
	}
	full, rem := eighths/8, eighths%8
	if full == 0 && rem == 0 {
		rem = 1
	}
	out := strings.Repeat("█", full)
	if rem > 0 {
		out += string(barEighths[rem])
	}
	return out
}

// barTiers shade a bar by the share it draws, largest first. The shade repeats
// what the bar's length and the figure beside it already say — no row is
// distinguished by its color alone, so the table reads the same under NO_COLOR,
// through a pipe, and to a reader who cannot separate the hues.
var barTiers = []struct {
	atLeast float64
	color   lipgloss.AdaptiveColor
}{
	{0.80, lipgloss.AdaptiveColor{Light: "160", Dark: "203"}},
	{0.50, lipgloss.AdaptiveColor{Light: "166", Dark: "215"}},
	{0.25, lipgloss.AdaptiveColor{Light: "136", Dark: "179"}},
	{0.10, lipgloss.AdaptiveColor{Light: "64", Dark: "108"}},
	{0.00, lipgloss.AdaptiveColor{Light: "66", Dark: "109"}},
}

func barStyle(frac float64) lipgloss.Style {
	for _, t := range barTiers {
		if frac >= t.atLeast {
			return lipgloss.NewStyle().Foreground(t.color)
		}
	}
	return lipgloss.NewStyle()
}

// Render writes the rows, the total they sum to, and the notes that say what the
// figure is and is not.
func Render(w io.Writer, r Report, opts Options) error {
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	head := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	note := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// One condition covers both reasons the three columns are dropped: an axis a
	// turn cannot be attributed to leaves the total at zero, and so does a corpus
	// of logs too old to carry turn timings.
	showTurns := r.Total.Turns > 0
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
		if max := labelCap(opts.Width, keyW, numericWidth(r.By, showTurns)+gap+barMin); labelW > max {
			labelW = max
		}
	}
	// The project axis is the other variable-width key, and an unbounded one: a
	// session run inside a temporary working directory carries its whole path,
	// which locally reached 140 characters and pushed every figure off screen.
	// Bounded by a fixed width as well as by the terminal's, because a path long
	// enough to fill a wide terminal would take the room every other column and
	// the share bar need, to show directories the reader already recognises.
	if r.By == ByProject {
		if keyW > projectKeyMax {
			keyW = projectKeyMax
		}
		if max := labelCap(opts.Width, 0, numericWidth(r.By, showTurns)+gap+barMin); keyW > max {
			keyW = max
		}
	}
	if keyW < len("Total") {
		keyW = len("Total")
	}

	used := keyW + numericWidth(r.By, showTurns)
	if r.By == BySession {
		used += gap + labelW
	}
	barW := barWidth(opts.Width, used)
	var maxCost float64
	for _, b := range r.Buckets {
		if b.CostUSD > maxCost {
			maxCost = b.CostUSD
		}
	}

	var out strings.Builder
	if len(r.Buckets) > 0 {
		// A chart that cannot be drawn from these rows falls back to the table and
		// says why on the notes beneath it, rather than printing an empty frame.
		if chart, ok := renderChart(opts.Chart, r.Buckets, opts); ok {
			out.WriteString(chart + "\n")
		} else {
			out.WriteString(head.Render(headerRow(r.By, keyW, labelW, showTurns, barW)) + "\n")
			for _, b := range r.Buckets {
				out.WriteString(row(r.By, b, keyW, labelW, showTurns, barW, maxCost) + "\n")
			}
			out.WriteString("\n")
		}
	}
	out.WriteString(totalRow(r, keyW, labelW, showTurns) + "\n")
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
	case ByAgent:
		return "Delegated to"
	case ByProject:
		return "Project"
	case BySession:
		return "Session"
	}
	return "Total"
}

// labelCap bounds a session title so the numeric columns survive a narrow
// terminal, the listing's rule applied to a table with one variable column.
func labelCap(width, keyW, numeric int) int {
	if width <= 0 {
		width = 100
	}
	cap := width - (keyW + gap + numeric)
	if cap < 10 {
		cap = 10
	}
	return cap
}

func headerRow(by string, keyW, labelW int, showTurns bool, barW int) string {
	cells := []string{padRight(axisHeading(by), keyW)}
	if by == BySession {
		cells = append(cells, padRight("Title", labelW))
	} else {
		cells = append(cells, padLeft("Sessions", sessionsW))
	}
	cells = append(cells, padLeft("Tokens", tokensW), padLeft("Cost", costW))
	if showTurns {
		cells = append(cells, padLeft("Turns", turnsW), padLeft("Active", activeW), padLeft("$/turn", perTurnW))
	}
	if barW > 0 {
		cells = append(cells, "Share")
	}
	return joinCells(cells)
}

func row(by string, b Bucket, keyW, labelW int, showTurns bool, barW int, maxCost float64) string {
	key := b.Key
	switch by {
	case BySession:
		key = shortID(b.Key)
	case ByProject:
		key = cutLeft(b.Key, keyW)
	}
	cells := []string{padRight(key, keyW)}
	if by == BySession {
		cells = append(cells, padRight(cut(displayTitle(b.Label), labelW), labelW))
	} else {
		cells = append(cells, padLeft(fmt.Sprintf("%d", b.Sessions), sessionsW))
	}
	cells = append(cells, figures(b)...)
	if showTurns {
		cells = append(cells, work(b)...)
	}
	// The share column is left off the total rather than filled: a bar spanning
	// the whole width would say only that the total is the total.
	if barW > 0 && maxCost > 0 {
		frac := b.CostUSD / maxCost
		cells = append(cells, barStyle(frac).Render(bar(b.CostUSD, maxCost, barW)))
	}
	return strings.TrimRight(joinCells(cells), " ")
}

// totalRow prints the figure every row sums to. On the session axis the session
// count has no column of its own — the rows are sessions — so it is spelled into
// the title column, where it says what the total covers.
func totalRow(r Report, keyW, labelW int, showTurns bool) string {
	t := r.Total
	cells := []string{padRight("Total", keyW)}
	if r.By == BySession {
		cells = append(cells, padRight(cut(sessionCount(t.Sessions), labelW), labelW))
	} else {
		cells = append(cells, padLeft(fmt.Sprintf("%d", t.Sessions), sessionsW))
	}
	cells = append(cells, figures(t)...)
	if showTurns {
		cells = append(cells, work(t)...)
	}
	return joinCells(cells)
}

func joinCells(cells []string) string { return strings.Join(cells, strings.Repeat(" ", gap)) }

// cutLeft trims a path to fit, dropping the front rather than the back. Paths in
// one column share their leading directories and differ at the end, so cutting
// the way a title is cut would leave rows that all read alike.
func cutLeft(s string, w int) string {
	if w < 2 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	return "…" + string(r[len(r)-(w-1):])
}

// figures is the pair every row carries: what the bucket spent, in tokens and in
// dollars.
func figures(b Bucket) []string {
	return []string{
		padLeft(spend.Tokens(allTokens(b.Usage)), tokensW),
		padLeft(spend.USD(b.CostUSD), costW),
	}
}

// work is what those dollars bought. The per-turn cell is blank rather than zero
// on a bucket with no turn of its own — a day can hold spend from a turn that
// began the day before, and a dash of zero there would read as a free turn.
func work(b Bucket) []string {
	perTurn := ""
	if b.Turns > 0 {
		perTurn = spend.USD(b.CostUSD / float64(b.Turns))
	}
	return []string{
		padLeft(fmt.Sprintf("%d", b.Turns), turnsW),
		padLeft(spend.Duration(b.ActiveSeconds), activeW),
		padLeft(perTurn, perTurnW),
	}
}

// allTokens is every counter a bucket holds. CacheCreate1h is a share of
// CacheCreate rather than a fifth counter, so adding it would count those tokens
// twice.
func allTokens(u model.Usage) int {
	return u.Input + u.Output + u.CacheRead + u.CacheCreate
}

// notes are what a reader needs in order not to misread the figure: that it is
// computed and an estimate, what Claude Code's own record says over the part of
// the window it can be compared on, and which models are in the tokens but in
// none of the dollars.
func notes(r Report) []string {
	var out []string
	if r.Selection != nil {
		out = append(out, r.Selection.line())
	}
	out = append(out, fmt.Sprintf(
		"computed from the transcript at %s list prices — an estimate, not a bill", r.PricesVerified))
	if line := workLine(r.Total); line != "" {
		out = append(out, line)
	}
	if r.Spread != nil {
		out = append(out, fmt.Sprintf("half of those %s cost under %s a turn; the top tenth above %s",
			sessionCount(r.Spread.Sessions), spend.USD(r.Spread.MedianPerTurn), spend.USD(r.Spread.P90PerTurn)))
	}
	if r.Recorded != nil {
		out = append(out, fmt.Sprintf("Claude Code recorded %s for the %s it kept a record for",
			spend.USD(r.Recorded.CostUSD), sessionCount(r.Recorded.Sessions)))
	}
	for _, m := range r.UnpricedModels {
		out = append(out, fmt.Sprintf("%s has no price here: its %s tokens over %s are in the tokens above and in no dollar figure",
			m.Model, spend.Tokens(allTokens(m.Usage)), sessionCount(m.Sessions)))
	}
	return out
}

// workLine says what the total's dollars bought, in the two units a person can
// act on: a turn, and an hour actually spent in turns. The hourly half is
// dropped when no turn carried a usable span, since dividing by zero time would
// print a rate nothing was measured at.
func workLine(t Bucket) string {
	if t.Turns == 0 {
		return ""
	}
	line := fmt.Sprintf("%d turns at %s each", t.Turns, spend.USD(t.CostUSD/float64(t.Turns)))
	if t.ActiveSeconds > 0 {
		line += fmt.Sprintf(", over %s of active time at %s an hour",
			spend.Duration(t.ActiveSeconds), spend.USD(t.CostUSD/(float64(t.ActiveSeconds)/3600)))
	}
	return line
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
	Since string `json:"since,omitempty"`
	// Until is the last day the scope counts, set only when the caller named one.
	Until    string      `json:"until,omitempty"`
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
func BuildOverview(session *model.Summary, folder, machine []model.Summary, w Window) Overview {
	// An explicit lower bound replaces the machine row's default rather than
	// narrowing it further: a caller who named a window asked for that window on
	// every row, and applying both would report a span nobody chose.
	machineSince := w.Since
	if machineSince.IsZero() {
		machineSince = w.MachineSince
	}
	wide := Build(machine, ByTotal, machineSince, w.Until)
	out := Overview{
		Recorded:       wide.Recorded,
		UnpricedModels: wide.UnpricedModels,
		PricesVerified: price.VerifiedOn,
	}
	narrowSince, narrowUntil := dayString(w.Since), dayString(w.Until)
	// A row is dropped when the window leaves it holding no session at all. The
	// alternative is a row reading $0.00, which says the session was free where
	// the truth is that it falls outside the span asked for — the same reason a
	// directory with no project prints no row rather than a zero one.
	if one := Build([]model.Summary{deref(session)}, ByTotal, w.Since, w.Until).Total; session != nil && one.Sessions > 0 {
		out.Scopes = append(out.Scopes, Scope{
			Scope: ScopeSession, Label: displayTitle(session.Title),
			Since: narrowSince, Until: narrowUntil,
			Sessions: one.Sessions, Usage: one.Usage, CostUSD: one.CostUSD,
		})
	}
	if all := Build(folder, ByTotal, w.Since, w.Until).Total; all.Sessions > 0 {
		out.Scopes = append(out.Scopes, Scope{
			Scope: ScopeFolder, Since: narrowSince, Until: narrowUntil,
			Sessions: all.Sessions, Usage: all.Usage, CostUSD: all.CostUSD,
		})
	}
	out.Scopes = append(out.Scopes, Scope{
		Scope: ScopeMachine, Since: dayString(machineSince), Until: narrowUntil,
		Sessions: wide.Total.Sessions, Usage: wide.Total.Usage, CostUSD: wide.Total.CostUSD,
	})
	return out
}

// deref is the session a summary row prices, or an empty one when this directory
// has no session to price — which Build reports as no session rather than as a
// session that spent nothing.
func deref(s *model.Summary) model.Summary {
	if s == nil {
		return model.Summary{}
	}
	return *s
}

// Window is the span a summary counts over. Since and Until bound every row and
// are zero when the caller named neither; MachineSince stands in for the machine
// row alone in that case, which is what makes the widest row a recent figure
// rather than a lifetime one.
type Window struct {
	Since, Until time.Time
	MachineSince time.Time
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
	if s.Scope == ScopeSession {
		return s.Label
	}
	return sessionCount(s.Sessions) + span(s.Since, s.Until)
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
	if max := labelCap(opts.Width, nameW, numericWidth(BySession, false)); windowW > max {
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
