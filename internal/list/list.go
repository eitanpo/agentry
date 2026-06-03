// Package list selects and formats session summaries for `agentry --list`:
// recency ordering, time-window filtering, and a one-row-per-session view. It is
// the listing counterpart to the render package; both consume model types and
// share the project's color/width conventions.
package list

import (
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
	var b strings.Builder
	for i := len(sums) - 1; i >= 0; i-- {
		s := sums[i]
		if opts.Prompts && i < len(sums)-1 {
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
		if opts.Prompts {
			rail := railIndent + dim.Render(railGlyph) + " "
			for _, p := range s.Prompts {
				fmt.Fprintf(&b, "%s%s %s\n", rail, dim.Render(promptGlyph), truncate(oneLine(p), promptW))
			}
			fmt.Fprintf(&b, "%s%s\n", railIndent, dim.Render(railClose))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
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
