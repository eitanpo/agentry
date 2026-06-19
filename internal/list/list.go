// Package list selects and formats session summaries for `agentry list`:
// recency ordering, time-window filtering, and a one-row-per-session view. It is
// the listing counterpart to the render package; both consume model types and
// share the project's color/width conventions.
package list

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/muesli/termenv"
)

const fallbackWidth = 100 // used when stdout is not a TTY

// Options configures the listing output.
type Options struct {
	Width   int
	Color   bool
	Prompts bool // --include prompts: list each session's prompts under its row
	Tools   bool // --include tools: break down each session's tool calls under its row
}

// Prompt blocks reuse the renderer's turn chrome: a left rail closed by a rule.
const (
	railIndent  = "  "
	railGlyph   = "│"
	railClose   = "╰─"
	promptGlyph = "❯"
	railVisualW = 6 // "  │ ❯ " — visible columns before the prompt text
)

// activity is the time a session is ordered and filtered by: its last entry,
// falling back to its first when only one timestamp is known.
func activity(s model.Summary) time.Time {
	if !s.End.IsZero() {
		return s.End
	}
	return s.Start
}

// Select orders summaries most-recent first by activity time, drops any outside
// [since, until] (a zero bound is open), and caps to limit (limit <= 0 = no
// cap). It does not mutate the input slice.
func Select(sums []model.Summary, since, until time.Time, limit int) []model.Summary {
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
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Filters narrows a listing to sessions whose top-level tool calls match. An
// empty field imposes no constraint; set fields AND together. Tool is matched
// case-insensitively and exact (the tool-use name); the rest are
// case-insensitive substring. Any is the identity catch-all — a skill name,
// subagent type, or command — and deliberately ignores tool names.
type Filters struct {
	Tool    string
	Skill   string
	Agent   string
	Command string
	Any     string
}

// Empty reports whether no constraint is set, so callers can skip filtering.
func (f Filters) Empty() bool {
	return f.Tool == "" && f.Skill == "" && f.Agent == "" && f.Command == "" && f.Any == ""
}

// Match reports whether s satisfies every set field.
func (f Filters) Match(s model.Summary) bool {
	if f.Tool != "" && !hasTool(s.Tools, f.Tool) {
		return false
	}
	if f.Skill != "" && !hasIdentity(s.Tools, "Skill", f.Skill) {
		return false
	}
	if f.Agent != "" && !hasIdentity(s.Tools, "Agent", f.Agent) {
		return false
	}
	if f.Command != "" && !hasCommand(s.Commands, f.Command) {
		return false
	}
	if f.Any != "" &&
		!hasIdentity(s.Tools, "Skill", f.Any) &&
		!hasIdentity(s.Tools, "Agent", f.Any) &&
		!hasCommand(s.Commands, f.Any) {
		return false
	}
	return true
}

// FilterByTools keeps only the summaries matching f, preserving input order. A
// no-op (returns the input) when f is empty.
func FilterByTools(sums []model.Summary, f Filters) []model.Summary {
	if f.Empty() {
		return sums
	}
	out := make([]model.Summary, 0, len(sums))
	for _, s := range sums {
		if f.Match(s) {
			out = append(out, s)
		}
	}
	return out
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

// Render writes one row per session: start time, turn count, title, and full id.
// The id is last and the title padded to a fixed column so ids align and a row
// can be selected and its id passed back to `agentry <id>`.
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
	// columns: when(16) dur(7,right) turns(4,right) title(rest) id(36), 2-space gaps
	const whenW, durW, turnsW, idW = 16, 7, 4, 36
	titleW := width - (whenW + durW + turnsW + idW + 4*2)
	if titleW < 10 {
		titleW = 10
	}

	// Print oldest-to-newest so the most recent session lands at the bottom,
	// nearest the prompt — the ls -ltr / shell-history / chat convention for
	// scrolling (unpaged) stdout. sums arrives most-recent first from Select.
	promptW := width - railVisualW
	if promptW < 10 {
		promptW = 10
	}
	// A session shows a detail block (rail + closing rule) when any --include
	// channel is on; the channels share one block.
	block := opts.Prompts || opts.Tools
	var b strings.Builder
	for i := len(sums) - 1; i >= 0; i-- {
		s := sums[i]
		if block && i < len(sums)-1 {
			b.WriteByte('\n') // blank line separates session blocks
		}
		when := "????-??-?? ??:??"
		if t := s.Start; !t.IsZero() {
			when = t.Local().Format("2006-01-02 15:04")
		} else if t := activity(s); !t.IsZero() {
			when = t.Local().Format("2006-01-02 15:04")
		}
		title := truncate(oneLine(s.Title), titleW)
		fmt.Fprintf(&b, "%s  %s  %s  %s  %s\n",
			meta.Render(when),
			meta.Render(fmt.Sprintf("%*s", durW, fmtDur(s.Start, s.End))),
			dim.Render(fmt.Sprintf("%*dt", turnsW-1, s.NumTurns)),
			pad(title, titleW),
			meta.Render(s.ID))
		rail := railIndent + dim.Render(railGlyph) + " "
		if opts.Prompts {
			for _, p := range s.Prompts {
				fmt.Fprintf(&b, "%s%s %s\n", rail, dim.Render(promptGlyph), truncate(oneLine(p), promptW))
			}
		}
		if opts.Tools {
			for _, line := range toolLines(s.Tools) {
				fmt.Fprintf(&b, "%s%s\n", rail, dim.Render(truncate(line, promptW)))
			}
		}
		if block {
			fmt.Fprintf(&b, "%s%s\n", railIndent, dim.Render(railClose))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// toolLines renders a session's tool breakdown as one line per non-empty
// category: Skills / Agents / Bash labelled by identity, Other by tool name.
// Entries within a line are ordered by count descending, then name ascending.
func toolLines(stats []model.ToolStat) []string {
	var skills, agents, bash, other []model.ToolStat
	for _, st := range stats {
		switch st.Tool {
		case "Skill":
			skills = append(skills, st)
		case "Agent":
			agents = append(agents, st)
		case "Bash":
			bash = append(bash, st)
		default:
			other = append(other, st)
		}
	}
	var lines []string
	emit := func(label string, group []model.ToolStat, byTool bool) {
		if len(group) == 0 {
			return
		}
		name := func(st model.ToolStat) string {
			if byTool {
				return st.Tool
			}
			if st.Identity == "" {
				return "?"
			}
			return st.Identity
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Count != group[j].Count {
				return group[i].Count > group[j].Count
			}
			return name(group[i]) < name(group[j])
		})
		parts := make([]string, len(group))
		for i, st := range group {
			parts[i] = fmt.Sprintf("%s ×%d", name(st), st.Count)
		}
		lines = append(lines, fmt.Sprintf("%-7s %s", label, strings.Join(parts, ", ")))
	}
	emit("Skills", skills, false)
	emit("Agents", agents, false)
	emit("Bash", bash, false)
	emit("Other", other, true)
	return lines
}

// pad right-fills s with spaces to width display columns (rune count). s is
// assumed already truncated to <= width.
func pad(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// fmtDur renders a session's first-prompt-to-last-output span compactly:
// "45m", "2h05m", or "8s"; empty when either bound is unknown.
func fmtDur(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	secs := int(end.Sub(start).Seconds())
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

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}
