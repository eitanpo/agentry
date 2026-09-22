// Package list selects and formats session summaries for `agentry list`:
// recency ordering, time-window filtering, and a one-row-per-session view. It is
// the listing counterpart to the render package; both consume model types and
// share the project's color/width conventions.
package list

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/breakdown"
	"github.com/eitanpo/agentry/internal/entrypoint"
	"github.com/eitanpo/agentry/internal/jsonl"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/render"
	"github.com/eitanpo/agentry/internal/schema"
	"github.com/eitanpo/agentry/internal/spend"
	"github.com/eitanpo/agentry/internal/trail"
	"github.com/muesli/termenv"
)

const fallbackWidth = 100 // used when stdout is not a TTY

// Options configures the listing output.
type Options struct {
	Width   int
	Color   bool
	Prompts bool // --include prompts: list each session's prompts under its row
	Tools   bool // --include tools: break down each session's tool calls under its row
	Files   bool // --include files: list each file the session modified
	Model   bool // --include model: name the model and reasoning effort the session ran on
	Cost    bool // --include cost: name what the session spent, in tokens and dollars
	Outputs bool // --include outputs: list the PRs the session opened and the artifacts it published
	// LastReply is the one channel that shows part of what it names: the session's
	// final reply rather than every reply. PRODUCT.md's --include section owns why,
	// and the channel's name is what carries the exception to a caller.
	LastReply bool // --include last-reply
}

// Prompt blocks reuse the renderer's turn chrome: a left rail closed by a rule.
const (
	promptGlyph = "❯"
	// Budgeted against the widest prefix a detail line can take, "  ╰─ ❯ ": a
	// one-line block draws the closing rule in place of the rail glyph, which is
	// one column wider, so budgeting against the rail would let that line wrap.
	railVisualW = 7
)

// activity is the time a session is ordered and filtered by: its last entry,
// falling back to its first when only one timestamp is known.
func activity(s model.Summary) time.Time { return Activity(s.Start, s.End) }

// Select orders summaries most-recent first by activity time, drops any outside
// [since, until] (a zero bound is open), and caps to limit (limit <= 0 = no
// cap). It does not mutate the input slice. The second return is how many
// summaries were inside the window before the cap, so a caller can name what the
// cap hid; it equals len of the first return whenever nothing was dropped.
func Select(sums []model.Summary, since, until time.Time, limit int) ([]model.Summary, int) {
	out := make([]model.Summary, 0, len(sums))
	for _, s := range sums {
		t := activity(s)
		if !since.IsZero() && t.Before(since) {
			continue
		}
		if !until.IsZero() && t.After(until) {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return activity(out[i]).After(activity(out[j]))
	})
	// The match count is returned alongside the capped slice rather than left for
	// the caller to recompute: the function that truncates is the only one that
	// knows what it dropped, and a cap unable to say so is the silent truncation
	// the --limit rule exists to prevent.
	matched := len(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, matched
}

// Tag renders a session's entrypoint as the column value: the 3-character name
// from the entrypoint package, plus a "+" when the session started under a
// different one and was resumed. The header form spells that transition out
// (entrypoint.Trail); a four-wide column cannot.
func Tag(s model.Summary) string {
	tag := entrypoint.Tag(s.Entrypoint)
	if len(s.Entrypoints) > 1 {
		tag += "+"
	}
	return tag
}

// FilterByFrom keeps the sessions matching the --from selector. The empty
// selector is the default: everything except non-interactive runs.
//
// Excluding those by default is a deliberate change to what a bare listing
// returns. They are the bulk of what a machine using hooks accumulates and
// almost none of what anyone reads back — typically one-turn runs of a few
// seconds. An unrecognized entrypoint is kept, since a new value is more
// likely to be a new way of working than a new kind of noise.
func FilterByFrom(sums []model.Summary, from string) []model.Summary {
	if from == entrypoint.All {
		return sums
	}
	out := make([]model.Summary, 0, len(sums))
	for _, s := range sums {
		if entrypoint.Matches(from, s.Entrypoint) {
			out = append(out, s)
		}
	}
	return out
}

// Criteria is one set of tests over what a session did. An empty field imposes
// no constraint. Tool is matched case-insensitively and exact (the tool-use
// name); the rest are case-insensitive substring. Any is the identity catch-all —
// a skill name, subagent type, or command — and deliberately ignores tool names.
// File is the "what did the work land on" axis, read from both records of it:
// Edit/Write targets and the tracked-file list.
//
// PR and Artifact are the "what came out of it" axis and are the two fields that
// do not read tool calls at all: they test Claude Code's own session-level record
// of a pull request opened or a page published, so they see work a subagent did
// as readily as work the main thread did.
//
// Reply is the "what the reply said" axis and the one field that is a compiled
// regular expression rather than a substring: it matches prose, where the
// questions are positional and alternation-shaped. nil is the unset value, so a
// caller sets it only with a pattern that already compiled — an invalid one is
// rejected as a usage error before any session is read.
type Criteria struct {
	Tool     string
	Skill    string
	Agent    string
	Command  string
	Any      string
	File     string
	PR       string
	Artifact string
	Reply    *regexp.Regexp
}

func (c Criteria) empty() bool {
	return c == Criteria{}
}

// hit runs every set test against s and reports whether all of them matched and
// whether any did. One traversal serves both sides of a filter: the positive
// flags require all, the negated ones forbid any.
func (c Criteria) hit(s model.Summary) (all, any bool) {
	all = true
	add := func(matched bool) {
		if matched {
			any = true
		} else {
			all = false
		}
	}
	if c.Tool != "" {
		add(hasTool(s.Tools, c.Tool))
	}
	if c.Skill != "" {
		add(hasIdentity(s.Tools, "Skill", c.Skill))
	}
	if c.Agent != "" {
		add(hasIdentity(s.Tools, "Agent", c.Agent))
	}
	if c.Command != "" {
		add(hasCommand(s.Commands, c.Command))
	}
	if c.Any != "" {
		add(hasAny(s, c.Any))
	}
	if c.File != "" {
		add(hasFile(s, c.File))
	}
	if c.PR != "" {
		add(hasPR(s.PRs, c.PR))
	}
	if c.Artifact != "" {
		add(hasArtifact(s.Artifacts, c.Artifact))
	}
	if c.Reply != nil {
		add(hasReply(s.Replies, c.Reply))
	}
	return all, any
}

// Filters narrows a listing by what a session used. Used keeps the sessions that
// match every one of its tests; NotUsed drops the sessions matching any of its
// own. Both sides are the same Criteria type on purpose: a filter and its
// negation must accept the same values and match by the same rule, and two
// parallel field lists would let a new filter be added to one side only.
type Filters struct {
	Used    Criteria
	NotUsed Criteria
}

// Empty reports whether no constraint is set, so callers can skip filtering.
func (f Filters) Empty() bool {
	return f.Used.empty() && f.NotUsed.empty()
}

// Match reports whether s satisfies every positive test and no negated one.
func (f Filters) Match(s model.Summary) bool {
	if all, _ := f.Used.hit(s); !all {
		return false
	}
	if _, any := f.NotUsed.hit(s); any {
		return false
	}
	return true
}

// Run narrows a listing by what a session ran on. An empty field imposes no
// constraint.
//
// Model is a case-insensitive substring because model names nest by family and
// version, and one flag has to serve both readings: "opus" for the family,
// "opus-5" for the release. Effort is case-insensitive and exact, alone among
// the value filters — the levels nest as substrings, so a substring rule would
// make --effort high quietly include xhigh, which is a wrong answer rather than
// a wide one. Neither validates against a known set: both grow without notice,
// and a value agentry has not heard of should return no sessions, not an error.
type Run struct {
	Model  string
	Effort string
}

// Empty reports whether no constraint is set, so callers can skip filtering.
func (r Run) Empty() bool { return r.Model == "" && r.Effort == "" }

// Match tests s against both fields, over every value the session carried
// rather than only the resolved one: a session that switched from Sonnet to
// Opus really did run both, and a test seeing only the last would deny it ran
// the first.
func (r Run) Match(s model.Summary) bool {
	if r.Model != "" && !anyMatch(carried(s.Model, s.Models), r.Model, containsFold) {
		return false
	}
	if r.Effort != "" && !anyMatch(carried(s.Effort, s.Efforts), r.Effort, strings.EqualFold) {
		return false
	}
	return true
}

// carried expands the parser's "resolved value, plus the full list only when it
// changed" pair back into every value the session held. It is the inverse of
// what the parser stores, so a filter reads the same set the parser saw.
func carried(resolved string, all []string) []string {
	if len(all) > 1 {
		return all
	}
	if resolved == "" {
		return nil
	}
	return []string{resolved}
}

func anyMatch(vals []string, want string, eq func(string, string) bool) bool {
	for _, v := range vals {
		if eq(v, want) {
			return true
		}
	}
	return false
}

// Selector is one axis a listing can be narrowed on: what a session used
// (Filters), what it ran on (Run), or how much it changed (Changed). Each answers
// the same two questions, so Filter treats them alike and a fourth axis needs
// only the pair of methods rather than a fourth copy of the loop below.
type Selector interface {
	// Empty reports whether the axis imposes no constraint.
	Empty() bool
	// Match reports whether s satisfies every constraint set on this axis.
	Match(model.Summary) bool
}

// Filter keeps the summaries matching every selector, preserving input order.
// Selectors imposing no constraint are skipped, so a caller passes all of them
// unconditionally and pays nothing on a listing given no filter flags — where
// every selector is empty the input slice is returned rather than copied.
func Filter(sums []model.Summary, sels ...Selector) []model.Summary {
	active := make([]Selector, 0, len(sels))
	for _, sel := range sels {
		if !sel.Empty() {
			active = append(active, sel)
		}
	}
	if len(active) == 0 {
		return sums
	}
	out := make([]model.Summary, 0, len(sums))
	for _, s := range sums {
		keep := true
		for _, sel := range active {
			if !sel.Match(s) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, s)
		}
	}
	return out
}

// Changed narrows a listing by how much code a session changed: the lines it
// added plus the lines it removed, as Claude Code recorded them. Both bounds are
// inclusive, and each is a pointer rather than a sentinel because zero is a real
// bound — --max-lines 0 asks for the sessions that changed nothing, which no
// "0 means unset" rule could express.
//
// A session whose log recorded no line counters matches neither bound. It makes
// no claim about how much it changed, so a floor cannot be met and a ceiling
// cannot be respected — the rule --model already follows for a session naming no
// model. That is most sessions, so a bounded listing is a listing of the sessions
// Claude Code kept a record for.
type Changed struct {
	Min *int
	Max *int
}

// Empty reports whether no bound is set, so callers can skip filtering.
func (c Changed) Empty() bool { return c.Min == nil && c.Max == nil }

// Match reports whether s changed a number of lines within both bounds.
func (c Changed) Match(s model.Summary) bool {
	if c.Empty() {
		return true
	}
	if s.LinesAdded == nil || s.LinesRemoved == nil {
		return false
	}
	n := *s.LinesAdded + *s.LinesRemoved
	if c.Min != nil && n < *c.Min {
		return false
	}
	if c.Max != nil && n > *c.Max {
		return false
	}
	return true
}

// hasAny is the identity catch-all: a skill name, a subagent type, or a command.
// Named rather than inlined so the positive and negated forms of --used cannot
// end up asking different questions.
func hasAny(s model.Summary, sub string) bool {
	return hasIdentity(s.Tools, "Skill", sub) ||
		hasIdentity(s.Tools, "Agent", sub) ||
		hasCommand(s.Commands, sub)
}

// hasFile reports whether the session modified a file matching sub. Edit and
// Write targets are checked first because they answer nearly every case: many
// sessions carry no tracked-file record, so consulting only that record would
// silently never match them. The tracked list is the backstop for a change no
// tool argument names, so it is insurance rather than the primary source.
func hasFile(s model.Summary, sub string) bool {
	if hasIdentity(s.Tools, "Edit", sub) || hasIdentity(s.Tools, "Write", sub) {
		return true
	}
	for _, f := range s.Files {
		if containsFold(f, sub) {
			return true
		}
	}
	return false
}

// hasPR reports whether the session opened a pull request matching sub, tested
// over all three of the ways one gets named: the repository, the number, and the
// URL. All three because the question arrives in all three forms — a repository's
// worth of work, one pull request by number, or a URL pasted from a browser — and
// which field a given phrasing lands in is not something a caller should have to
// know. The number is matched as text so it shares the substring rule, which does
// mean "4" also matches pull request 14; the same looseness every substring
// filter in this family carries.
func hasPR(prs []model.PR, sub string) bool {
	for _, p := range prs {
		if containsFold(p.Repository, sub) || containsFold(p.URL, sub) {
			return true
		}
		if p.Number > 0 && containsFold(strconv.Itoa(p.Number), sub) {
			return true
		}
	}
	return false
}

// hasArtifact reports whether the session published an artifact matching sub,
// over its title, its published URL, and the local file it was rendered from —
// the same "every way it gets named" rule hasPR follows. The local path is
// included because an artifact is often remembered by the file that produced it
// rather than by a title it may not even carry.
func hasArtifact(as []model.Artifact, sub string) bool {
	for _, a := range as {
		if containsFold(a.Title, sub) || containsFold(a.URL, sub) || containsFold(a.Path, sub) {
			return true
		}
	}
	return false
}

// hasReply reports whether any of the session's assistant text blocks matches
// re. Per block rather than over the blocks joined, because ^ and $ have to
// anchor to one reply: "did a reply open with praise" is a question about a
// reply, and a joined corpus would leave it answerable only about the session.
//
// Reply text is the one thing the filters read that --format json does not
// carry, so this is the only way to ask the question over a corpus at all.
func hasReply(replies []string, re *regexp.Regexp) bool {
	for _, r := range replies {
		if re.MatchString(r) {
			return true
		}
	}
	return false
}

func hasTool(stats []model.ToolStat, name string) bool {
	for _, st := range stats {
		if strings.EqualFold(st.Tool, name) {
			return true
		}
	}
	return false
}

func hasIdentity(stats []model.ToolStat, tool, sub string) bool {
	for _, st := range stats {
		if st.Tool == tool && containsFold(st.Identity, sub) {
			return true
		}
	}
	return false
}

func hasCommand(cmds []string, sub string) bool {
	for _, c := range cmds {
		if containsFold(c, sub) {
			return true
		}
	}
	return false
}

// containsFold reports whether sub occurs in s, case-insensitively.
func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// ParseWhen interprets a --since/--until value relative to now:
//
//	today | yesterday      local midnight of that day
//	<N>h | <N>d | <N>w      that many hours/days/weeks before now
//	YYYY-MM-DD             local midnight of that date
//
// Any other input is an error.
func ParseWhen(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "today":
		return midnight(now), nil
	case "yesterday":
		return midnight(now).AddDate(0, 0, -1), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if d, ok := parseSpan(s); ok {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (want today, yesterday, Nh/Nd/Nw, or YYYY-MM-DD)", s)
}

func midnight(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// parseSpan parses "<N>h", "<N>d", or "<N>w" into a duration. (time.ParseDuration
// has no day or week unit, so we handle the span grammar ourselves.)
func parseSpan(s string) (time.Duration, bool) {
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, true
	}
	return 0, false
}

// RenderJSON writes the summaries as an indented JSON array — the listing's
// machine-readable form. It serializes the full model per session regardless of
// the --include channels (which shape only the text view); an empty slice
// emits "[]". sums arrives in Select order (most-recent first), preserved here.
func RenderJSON(w io.Writer, sums []model.Summary) error {
	if sums == nil {
		sums = []model.Summary{} // marshal an empty array, not null
	}
	b, err := json.MarshalIndent(sums, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// RenderJSONL writes the summaries as JSON Lines — one `session` record per
// row, each carrying its own id in the envelope. The array wrapper disappears,
// so an empty selection writes nothing at all rather than "[]": there is no
// wrapper for the empty case to live in, and zero lines is a well-formed stream.
func RenderJSONL(w io.Writer, sums []model.Summary) error {
	enc := jsonl.New(w)
	for _, s := range sums {
		if err := enc.Emit("session", s.ID, s.Start, s); err != nil {
			return err
		}
	}
	return nil
}

// forkGlyph prefixes a fork's title, indenting it under its family's original.
const (
	forkGlyph  = "└─ "
	forkGlyphW = 3 // display columns of forkGlyph
)

// frow is one display row: a session and whether it is a fork shown indented
// under its family's original.
type frow struct {
	s    model.Summary
	fork bool
}

// arrange turns the selected summaries (most-recent first, as Select returns)
// into display rows in top-to-bottom print order. Sessions that share a RootUUID
// are one fork family — a fork copies its parent's history, root entry included.
// A family prints as a contiguous block: its original (the earliest-born file)
// first, then each fork indented beneath in birth order. Families are ordered by
// their most-recently-active member, oldest first, so the newest prints last
// (bottom), matching the ungrouped layout.
func arrange(sums []model.Summary) []frow {
	groups := map[string][]model.Summary{}
	var order []string // family keys in first-seen (most-recent-first) order
	for i, s := range sums {
		key := s.RootUUID
		if key == "" {
			key = "\x00" + strconv.Itoa(i) // no root id: a family of one that cannot collide
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], s)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return familyActivity(groups[order[i]]).Before(familyActivity(groups[order[j]]))
	})
	var rows []frow
	for _, key := range order {
		fam := groups[key]
		sort.SliceStable(fam, func(i, j int) bool { return fam[i].Born.Before(fam[j].Born) })
		parent := fam[0] // earliest-born file: the original the forks descend from
		for i, s := range fam {
			// A fork (i > 0) inherits its parent's title until Claude Code
			// regenerates one. While it still matches, title it by the first prompt
			// unique to the fork so the rows are distinguishable. s is a copy, so
			// this shapes only the text table, not the stored summary.
			if i > 0 && s.Title == parent.Title {
				if d := firstDivergentPrompt(parent, s); d != "" {
					s.Title = d
				}
			}
			// A title that only repeats the row's worktree is a launch handle, not a
			// description: one argument named both, and the worktree column beside it
			// already carries the string. Falls to the first prompt, which the list
			// already excludes /clear from. Checked after the fork rule so a rewritten
			// fork title is judged rather than the inherited one.
			if wt := worktreeName(s.Cwd); wt != "" && strings.TrimSpace(s.Title) == wt && len(s.Prompts) > 0 {
				s.Title = s.Prompts[0]
			}
			rows = append(rows, frow{s: s, fork: i > 0})
		}
	}
	return rows
}

// firstDivergentPrompt returns the fork's first prompt beyond the prefix it
// shares with its parent — the first turn unique to the fork. Prompt lists
// already exclude /clear (see parse.Summarize), so a leading /clear is skipped
// for free. Empty when the fork has added no new prompt yet.
func firstDivergentPrompt(parent, fork model.Summary) string {
	i := 0
	for i < len(parent.Prompts) && i < len(fork.Prompts) && parent.Prompts[i] == fork.Prompts[i] {
		i++
	}
	if i < len(fork.Prompts) {
		return fork.Prompts[i]
	}
	return ""
}

// familyActivity is a fork family's position key: the most-recent activity among
// its members, so an active fork keeps the whole family near the bottom.
func familyActivity(fam []model.Summary) time.Time {
	var newest time.Time
	for _, s := range fam {
		if a := activity(s); a.After(newest) {
			newest = a
		}
	}
	return newest
}

// Render writes one row per session: last-activity time, turn count, title, and full id.
// The id is last and the title padded to a fixed column so ids align and a row
// can be selected and its id passed back to `agentry <id>`. Forks are grouped
// under their family's original and their titles indented (see arrange).
func Render(w io.Writer, sums []model.Summary, opts Options) error {
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii) // strips ANSI from styles
	}
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("250")) // time/duration/id: light gray, legible
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))    // turns: secondary

	width := opts.Width
	if width <= 0 {
		width = fallbackWidth
	}
	// columns: when(16) dur(7,right) turns(4,right) [from] [project|worktree] title(rest) id(>=8), 2-space gaps
	const whenW, durW, turnsW, projMaxW = 16, 7, 4, 24
	// The id is printed whole, since `claude --resume` refuses anything shorter
	// and a listing that showed only a prefix left that command needing a second
	// one to expand what was already on screen. idWidth still runs: it decides how
	// much of the id is emphasized, not how much is drawn. See idWidth for the
	// floor's reasoning.
	uniqueW := idWidth(sums)
	idW := idColumnW(sums)
	// The entrypoint tag follows the project column's rule: drawn only when the
	// listing spans more than one, so a listing of one kind is unchanged. Width
	// is 4 to fit the "+" a resumed session takes.
	tags := varyingTags(sums)
	fromW := 0
	if tags != nil {
		fromW = 4
	}
	// The project column exists only when the listing spans more than one, so a
	// single-project listing — every listing before --all-projects/--project —
	// keeps its exact previous layout. Inside one project the same slot carries
	// the worktree instead: the two are mutually exclusive by construction, since
	// one needs more than one project and the other exactly one, so the title
	// never pays for two path columns. A project label is a path suffix and keeps
	// its tail; a worktree name keeps its head, which is the part someone chose.
	labels, keepTail := RowLabels(sums)
	projW := 0
	for _, l := range labels {
		if n := utf8.RuneCountInString(l); n > projW {
			projW = n
		}
	}
	gaps := 4
	if projW > 0 {
		gaps++
	}
	if fromW > 0 {
		gaps++
	}
	// The path column is capped twice: absolutely, and at a third of what the
	// two variable columns have to share. Without the second cap a long project
	// name starves the title — at 100 columns a 21-character repo name leaves the
	// title at its 10-column floor, which is the column the row is actually read
	// by. The same cap serves the worktree column, which fills the same slot.
	if projW > 0 {
		avail := width - (whenW + durW + turnsW + idW + fromW + gaps*2)
		if cap := avail / 3; projW > cap {
			projW = cap
		}
		if projW > projMaxW {
			projW = projMaxW
		}
		if projW < 8 {
			projW = 8
		}
	}
	titleW := width - (whenW + durW + turnsW + idW + fromW + projW + gaps*2)
	if titleW < 10 {
		titleW = 10
	}
	// Fit the path labels to the column once the width is settled: truncation has
	// to see the whole set, or two labels that shorten to the same string are
	// drawn as one label claiming to be two places.
	cells := fitLabels(labels, projW, keepTail)

	// Print oldest-to-newest so the most recent session lands at the bottom,
	// nearest the prompt — the ls -ltr / shell-history / chat convention for
	// scrolling (unpaged) stdout. sums arrives most-recent first from Select.
	promptW := width - railVisualW
	if promptW < 10 {
		promptW = 10
	}
	var b strings.Builder
	rows := arrange(sums)
	for _, r := range rows {
		s := r.s
		// The when column shows the session's last activity (its most recent turn's
		// end), the same time it is ordered by; activity falls back to Start when no
		// later timestamp is known.
		when := "????-??-?? ??:??"
		if t := activity(s); !t.IsZero() {
			when = t.Local().Format("2006-01-02 15:04")
		}
		// A fork's title is indented under its family's original; the marker eats
		// into the title column so the when/turns/id columns stay aligned.
		title := truncate(render.OneLine(s.Title), titleW)
		if r.fork {
			title = forkGlyph + truncate(render.OneLine(s.Title), titleW-forkGlyphW)
		}
		from := ""
		if fromW > 0 {
			from = dim.Render(pad(tags[s.ID], fromW)) + "  "
		}
		proj := ""
		if projW > 0 {
			proj = dim.Render(pad(cells[s.Cwd], projW)) + "  "
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s%s%s  %s\n",
			meta.Render(when),
			meta.Render(fmt.Sprintf("%*s", durW, fmtActive(s.DailyActivity))),
			dim.Render(fmt.Sprintf("%*dt", turnsW-1, s.NumTurns)),
			from,
			proj,
			pad(title, titleW),
			renderID(s.ID, uniqueW, meta, dim))
		// The block's lines are collected before any is written, because how many
		// there are decides whether the closing rule carries the only one or stands
		// on a line of its own.
		var detail []string
		// What the session ran on leads the block: it describes the session, where
		// the channels below it enumerate the session's contents.
		if opts.Model {
			if line := runLine(s); line != "" {
				detail = append(detail, dim.Render(truncate(line, promptW)))
			}
		}
		// What it spent sits beside what it ran on, for the same reason: both
		// describe the session, where every channel below them enumerates its
		// contents. The wording is the rendered header's, built by the one helper
		// both call, so the two surfaces cannot report one session's spend
		// differently.
		if opts.Cost {
			detail = append(detail, dim.Render(truncate(spend.Line(s.Usage, s.CacheSaving, s.CostUSD, s.LinesAdded, s.LinesRemoved), promptW)))
		}
		if opts.Prompts {
			for _, p := range s.Prompts {
				detail = append(detail, dim.Render(promptGlyph)+" "+truncate(render.OneLine(p), promptW))
			}
		}
		// The conversation's far end, so it sits with the prompts rather than among
		// the channels below that enumerate what the session did. A session whose log
		// holds no assistant text contributes no line, the rule every optional channel
		// follows — a headless run that never answered would otherwise show an empty one.
		//
		// The reply goes through the render package's own layout rather than being
		// shortened to a line here, so it reads exactly as it does under the render
		// path. Unlike every other channel its length is the reply's, not one line
		// per item; PRODUCT.md's --include section carries what that costs a caller.
		if opts.LastReply && len(s.Replies) > 0 {
			detail = append(detail, render.Reply(s.Replies[len(s.Replies)-1], promptW, opts.Color)...)
		}
		if opts.Tools {
			for _, line := range breakdown.Lines(breakdown.Tally{Calls: s.Tools, Failures: s.Failures, Denials: s.Denials}, 0) {
				detail = append(detail, dim.Render(truncate(line, promptW)))
			}
		}
		if opts.Files {
			// Truncated from the left: what distinguishes one modified file from
			// another is its tail, the same reason the project column truncates
			// that way.
			for _, f := range s.Files {
				detail = append(detail, dim.Render(truncateLeft(f, promptW)))
			}
		}
		if opts.Outputs {
			// One rule, two directions: each line is cut so that the half identifying
			// the thing survives. A pull request is identified by its URL's tail — the
			// repository and the number — with the scheme and host every line repeats
			// at the head, so it truncates from the left like the files channel. An
			// artifact is identified by its title, at the head, so its line truncates
			// from the right and lets the URL go: a claude.ai artifact id is an opaque
			// uuid that identifies nothing to a reader, and --format json carries it in
			// full — the trade the Edits line already makes for paths.
			for _, p := range s.PRs {
				detail = append(detail, dim.Render(truncateLeft(p.Key(), promptW)))
			}
			for _, a := range s.Artifacts {
				detail = append(detail, dim.Render(truncate(artifactLine(a), promptW)))
			}
		}
		// One line hangs off the closing rule instead of taking a rail line above a
		// bare one, so the chrome never costs more lines than the content it bounds.
		// Two or more keep the rail: content on the rule there would give the last
		// line different chrome from its siblings without saying anything different.
		// A block every selected channel left empty prints nothing at all: a bare
		// rule here would cost a line bounding no content, and would read as a
		// channel that ran and found nothing rather than one with nothing to find.
		for _, line := range render.Rail(detail, dim) {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// runLine names what a session ran on, in the rendered header's phrasing: the
// model, then the reasoning effort as a phrase ("high" alone would not say high
// what), each spelling out a mid-session change with the shared arrow. Empty
// when the log names neither, so a session predating both fields shows no line
// rather than an empty one.
func runLine(s model.Summary) string {
	var parts []string
	if m := trail.Of(s.Model, s.Models); m != "" {
		parts = append(parts, m)
	}
	if e := trail.Of(s.Effort, s.Efforts); e != "" {
		parts = append(parts, e+" effort")
	}
	return strings.Join(parts, " · ")
}

// artifactLine names one published artifact: its title, then its URL. The title
// leads because it is the only half a person recognizes — the URL ends in an
// opaque uuid — and an artifact whose record carried no title is its URL alone
// rather than a line that opens with a blank column.
//
// A pull request needs no such function: its URL already spells the repository
// and the number, so PR.Key is both its identity and its whole display.
func artifactLine(a model.Artifact) string {
	return strings.TrimSpace(a.Title + "  " + a.Key())
}

// varyingTags maps session id to its entrypoint tag, but only when the listing
// spans more than one — the same rule the project column uses, so a listing of a
// single kind keeps the layout it had before entrypoints were shown. nil is the
// signal not to draw the column. A resumed session's "+" does not by itself
// count as variation: what varies is where sessions ran, and one row reading
// "cli+" among plain "cli" rows says that on its own.
func varyingTags(sums []model.Summary) map[string]string {
	tags := make(map[string]string, len(sums))
	kinds := map[string]bool{}
	for _, s := range sums {
		t := Tag(s)
		tags[s.ID] = t
		kinds[strings.TrimSuffix(t, "+")] = true
	}
	if len(kinds) < 2 {
		return nil
	}
	return tags
}

// worktreeMarker is the path segment Claude Code puts a repo's worktrees under.
// A cwd containing it names a place inside a repo, not a project of its own.
const worktreeMarker = "/.claude/worktrees/"

// projectRoot is the repository a cwd belongs to: itself, or the repo holding it
// when it is one of that repo's worktrees. The listing's scope rule already
// treats a repo's worktrees as the repo, and a label that disagreed reported one
// project as several.
func projectRoot(cwd string) string {
	if i := strings.Index(cwd, worktreeMarker); i >= 0 {
		return cwd[:i]
	}
	return cwd
}

// worktreeName is the worktree a cwd names, or "" for a repo's own checkout.
func worktreeName(cwd string) string {
	if i := strings.Index(cwd, worktreeMarker); i >= 0 {
		return cwd[i+len(worktreeMarker):]
	}
	return ""
}

// projectLabels maps each distinct session cwd to the shortest suffix of path
// components that tells its project apart from the others present. One project
// listed shows as its own directory name; two repos sharing a basename grow a
// component each until they differ, so `me/agentry` and `acme-private/agentry`
// both appear rather than two identical `agentry` rows.
//
// The alternative reads worse in both directions: a bare basename collides
// exactly where a repo tree groups colliding names under owners, and a full path
// does not fit — at 80 columns the title column is already at its floor. Returns
// nil when fewer than two projects are present, which is the signal not to draw
// the column at all.
//
// Labels are computed over projectRoot, not over the cwd, so several worktrees
// of one repo share one label and count as one project.
// RowLabels is the label a row draws in its path column, keyed by session cwd:
// the project where the set spans more than one, the worktree where the set sits
// inside one project, and none where neither tells the rows apart. The second
// return says which end of a label survives truncation — a project label is a
// path suffix and keeps its tail, a worktree name keeps the head somebody chose.
//
// Exported, and the only chooser: `agentry search`'s session rows label a
// session exactly as a listing row does, where one session labelled two ways
// reads as two sessions. Reading only the project half is what left a search
// across one repository's worktrees with no label on any row.
func RowLabels(sums []model.Summary) (map[string]string, bool) {
	if labels := projectLabels(sums); labels != nil {
		return labels, true
	}
	return worktreeLabels(sums), false
}

// Activity is the time a row shows and the time the rows are ordered by: the
// last activity of a session running from start to end, falling back to the
// start where the log records no later moment. It takes the pair rather than a
// summary so the one caller holding a parsed session instead of a summary reaches
// the same rule; exported because printing one time while sorting by another
// reads as no order at all.
func Activity(start, end time.Time) time.Time {
	if !end.IsZero() {
		return end
	}
	return start
}

func projectLabels(sums []model.Summary) map[string]string {
	roots := map[string]bool{}
	byCwd := map[string]string{}
	for _, s := range sums {
		if s.Cwd == "" {
			continue
		}
		root := projectRoot(s.Cwd)
		roots[root] = true
		byCwd[s.Cwd] = root
	}
	if len(roots) < 2 {
		return nil
	}
	rootLabels := breakdown.ShortestUniqueLabels(roots)
	labels := make(map[string]string, len(byCwd))
	for cwd, root := range byCwd {
		labels[cwd] = rootLabels[root]
	}
	return labels
}

// worktreeLabels maps each distinct session cwd to the worktree it ran in, "—"
// for a session in the repo's own checkout. It fills the project column's slot
// inside one repository, where the worktree is the only thing telling one line of
// work from another. Returns nil unless the listing holds exactly one project and
// more than one place within it — so it is never drawn beside the project column,
// and never drawn when every session sat in the same place.
func worktreeLabels(sums []model.Summary) map[string]string {
	roots := map[string]bool{}
	labels := map[string]string{}
	places := map[string]bool{}
	for _, s := range sums {
		if s.Cwd == "" {
			continue
		}
		roots[projectRoot(s.Cwd)] = true
		name := worktreeName(s.Cwd)
		if name == "" {
			name = "—"
		}
		labels[s.Cwd] = name
		places[name] = true
	}
	if len(roots) != 1 || len(places) < 2 {
		return nil
	}
	return labels
}

// fitLabels shortens each label to width columns, keeping the end that carries
// the meaning: a project label is a path suffix and keeps its tail, a worktree
// name keeps its head. Where two labels then render identically, the colliding
// ones move the ellipsis inward and keep enough of the other end to tell them
// apart — one character more than the longest run they share there, the mirror
// of the distinguishing-prefix rule (lcp+1).
//
// The comparison is over distinct labels, so many rows sharing one label are not
// a collision: the "—" every session in a repo's own checkout shows is left
// alone, as is a worktree several sessions ran in. Where no split fits, the
// duplicate stands — inventing a marker would mean nothing outside this listing,
// and the session id already separates the rows.
//
// Not the shortest-unique-substring problem, which asks for the shortest
// substring of one string unique within that same string; this asks which part
// of each member of a set separates it from the others, so the suffix-array
// machinery for SUS does not apply.
func fitLabels(labels map[string]string, width int, keepTail bool) map[string]string {
	cut := func(s string) string {
		if keepTail {
			return truncateLeft(s, width)
		}
		return truncate(s, width)
	}
	groups := map[string][]string{}
	for _, l := range labels {
		if _, ok := groups[cut(l)]; !ok {
			groups[cut(l)] = nil
		}
	}
	for _, l := range labels {
		g := cut(l)
		if !slices.Contains(groups[g], l) {
			groups[g] = append(groups[g], l)
		}
	}
	fitted := make(map[string]string, len(labels))
	for cwd, l := range labels {
		fitted[cwd] = cut(l)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		keep := 1 + sharedRun(group, keepTail)
		head := width - 1 - keep // room left for the other end
		if keepTail {
			head, keep = keep, width-1-keep
		}
		if head < 1 || keep < 1 {
			continue // no split fits: the duplicate stands
		}
		for cwd, l := range labels {
			if !slices.Contains(group, l) {
				continue
			}
			fitted[cwd] = l[:head] + "…" + l[len(l)-keep:]
		}
	}
	return fitted
}

// sharedRun is the longest run every pair in group shares at the end truncation
// discards — the tail when heads are kept, the head when tails are kept. One
// character past it is what separates the pair that agrees the longest.
func sharedRun(group []string, keepTail bool) int {
	longest := 0
	for _, a := range group {
		for _, b := range group {
			if a == b {
				continue
			}
			n := 0
			for n < len(a) && n < len(b) {
				if keepTail {
					if a[n] != b[n] {
						break
					}
				} else if a[len(a)-1-n] != b[len(b)-1-n] {
					break
				}
				n++
			}
			if n > longest {
				longest = n
			}
		}
	}
	return longest
}

// idFloor is the shortest id a listing prints. The floor is not about today's
// collisions but about tomorrow's: a listing of three rows would otherwise
// print 1-character ids that stop resolving as the machine fills up, since an
// id is copied out of one listing and passed back later.
const idFloor = 8

// idWidth is the shortest prefix length that tells these sessions apart, floored
// at idFloor and never longer than the ids themselves. This is git's rule for
// abbreviated object names, computed over the rows in hand.
//
// The check is a pairwise scan over one listing — small by construction, 10
// rows by default — so the trie that makes shortest-unique-prefix O(n·L)
// instead of O(n²·L) would buy nothing here beyond code to read.
func idWidth(sums []model.Summary) int {
	longest := 0
	for _, s := range sums {
		if n := len(s.ID); n > longest {
			longest = n
		}
	}
	if longest <= idFloor {
		return longest // every id is already shorter than the floor
	}
	for n := idFloor; n < longest; n++ {
		seen := make(map[string]bool, len(sums))
		clash := false
		for _, s := range sums {
			p := abbrevID(s.ID, n)
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

// idColumnW is the width the id column takes, every id being printed whole.
func idColumnW(sums []model.Summary) int {
	w := 0
	for _, s := range sums {
		if n := len(s.ID); n > w {
			w = n
		}
	}
	return w
}

// renderID draws the id whole, the prefix that tells these rows apart in the id's
// own color and the rest of it a shade down. Weight rather than presence is what
// separates the two jobs the value does: the prefix is what a reader scans the
// column by and hands back to agentry, and the whole string is what `claude
// --resume` requires. A caller with color off gets the same characters, since
// dropping half of a value a reader copies would be the worse degradation.
func renderID(id string, uniqueW int, bright, faint lipgloss.Style) string {
	if len(id) <= uniqueW {
		return bright.Render(id)
	}
	return bright.Render(id[:uniqueW]) + faint.Render(id[uniqueW:])
}

// abbrevID cuts an id to n characters, or returns it whole when it is shorter.
func abbrevID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

// pad right-fills s with spaces to width display columns (rune count). s is
// assumed already truncated to <= width.
func pad(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// fmtActive renders the duration column: how long the session's turns ran, not
// the span from its first entry to its last. That span reported how long a
// terminal stayed open, which can run many times longer than the recorded
// active time when a session sits open. A session whose log carries no turn
// records shows nothing rather than "0m", the rule every column here follows
// for a fact the log does not carry. The rendered header names the same
// figure, from the same sum.
func fmtActive(days []model.DailyActivity) string {
	if len(days) == 0 {
		return ""
	}
	return fmtDur(model.ActiveSeconds(days))
}

// fmtDur renders a span of seconds compactly:
// "45m", "2h05m", or "8s"; empty when either bound is unknown.
func fmtDur(secs int) string {
	if secs < 0 {
		return ""
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	h, m := secs/3600, (secs%3600)/60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// truncateLeft drops leading runes instead of trailing ones, for values whose
// tail carries the meaning — a path suffix, where the last component is what
// distinguishes one project from another.
func truncateLeft(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return "…" + string(r[len(r)-(limit-1):])
}

// Shapes describes what a listing writes in its machine-readable forms. Each
// entry names the value the emitter passes, so the description cannot drift from
// what a caller actually receives.
func Shapes() []schema.Shape {
	return []schema.Shape{
		schema.Document("list", "agentry list --format json", []model.Summary{}),
		schema.Record("list", "session", model.Summary{}),
	}
}
