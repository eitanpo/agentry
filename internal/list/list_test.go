package list

import (
	"strings"
	"testing"
	"time"

	"github.com/eitanpo/agentry/internal/model"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 6, 3, 14, 30, 0, 0, time.Local)
	midnightToday := time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)

	tests := []struct {
		in   string
		want time.Time
	}{
		{"today", midnightToday},
		{"yesterday", midnightToday.AddDate(0, 0, -1)},
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"2w", now.Add(-2 * 7 * 24 * time.Hour)},
		{"2026-06-01", time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)},
		{"TODAY", midnightToday}, // case-insensitive
	}
	for _, tt := range tests {
		got, err := ParseWhen(tt.in, now)
		if err != nil {
			t.Errorf("ParseWhen(%q) error: %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("ParseWhen(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "soon", "5", "5y", "2026/06/01", "-3d"} {
		if _, err := ParseWhen(bad, now); err == nil {
			t.Errorf("ParseWhen(%q) = nil error, want error", bad)
		}
	}
}

func TestSelect(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 6, 3, h, 0, 0, 0, time.UTC) }
	sums := []model.Summary{
		{ID: "noon", End: at(12)},
		{ID: "morning", End: at(9)},
		{ID: "evening", End: at(18)},
		{ID: "onlystart", Start: at(15)}, // no End: activity falls back to Start
	}

	t.Run("orders most-recent first", func(t *testing.T) {
		got := Select(sums, time.Time{}, time.Time{}, 0)
		want := []string{"evening", "onlystart", "noon", "morning"}
		assertIDs(t, got, want)
	})

	t.Run("limit caps count", func(t *testing.T) {
		got := Select(sums, time.Time{}, time.Time{}, 2)
		assertIDs(t, got, []string{"evening", "onlystart"})
	})

	t.Run("since drops earlier", func(t *testing.T) {
		got := Select(sums, at(12), time.Time{}, 0)
		assertIDs(t, got, []string{"evening", "onlystart", "noon"})
	})

	t.Run("until drops later", func(t *testing.T) {
		got := Select(sums, time.Time{}, at(12), 0)
		assertIDs(t, got, []string{"noon", "morning"})
	})

	t.Run("window matching none is empty", func(t *testing.T) {
		got := Select(sums, at(20), time.Time{}, 0)
		if len(got) != 0 {
			t.Errorf("got %d, want 0", len(got))
		}
	})
}

func TestFmtDur(t *testing.T) {
	base := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		secs int
		want string
	}{
		{8, "8s"},
		{45 * 60, "45m"},
		{2*3600 + 5*60, "2h05m"},
		{27*3600 + 14*60, "27h14m"},
	}
	for _, tt := range tests {
		got := fmtDur(base, base.Add(time.Duration(tt.secs)*time.Second))
		if got != tt.want {
			t.Errorf("fmtDur(%ds) = %q, want %q", tt.secs, got, tt.want)
		}
	}
	if got := fmtDur(time.Time{}, base); got != "" {
		t.Errorf("fmtDur(zero start) = %q, want empty", got)
	}
	if got := fmtDur(base, base.Add(-time.Hour)); got != "" {
		t.Errorf("fmtDur(negative) = %q, want empty", got)
	}
}

func TestRenderPlain(t *testing.T) {
	sums := []model.Summary{
		{
			ID:       "abc123",
			Start:    time.Date(2026, 6, 3, 14, 5, 0, 0, time.UTC),
			End:      time.Date(2026, 6, 3, 14, 50, 0, 0, time.UTC),
			NumTurns: 12,
			Title:    "first\nline only",
		},
	}
	var b strings.Builder
	if err := Render(&b, sums, Options{Width: 100, Color: false}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Count(out, "\n") != 1 {
		t.Errorf("want one row, got %q", out)
	}
	for _, want := range []string{"abc123", "45m", "12t", "first"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "line only") {
		t.Errorf("title should be truncated at newline: %q", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("color off should emit no ANSI: %q", out)
	}
}

func TestRenderNewestLast(t *testing.T) {
	// Input arrives most-recent first (as Select returns it); output must print
	// it oldest-to-newest, so the newest row is last.
	sums := []model.Summary{
		{ID: "newer", End: time.Date(2026, 6, 3, 18, 0, 0, 0, time.UTC)},
		{ID: "older", End: time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)},
	}
	var b strings.Builder
	if err := Render(&b, sums, Options{Width: 100, Color: false}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Index(out, "older") > strings.Index(out, "newer") {
		t.Errorf("want older before newer (newest last), got:\n%s", out)
	}
}

func TestRenderIncludePrompts(t *testing.T) {
	sums := []model.Summary{
		{ID: "s1", Title: "do a thing", Prompts: []string{"first ask", "second ask"}},
	}
	// Off: prompts absent.
	var off strings.Builder
	if err := Render(&off, sums, Options{Width: 100, Color: false, Prompts: false}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off.String(), "first ask") {
		t.Errorf("prompts should be hidden without Prompts: %q", off.String())
	}
	// On: prompts listed, each with the glyph, one per line.
	var on strings.Builder
	if err := Render(&on, sums, Options{Width: 100, Color: false, Prompts: true}); err != nil {
		t.Fatal(err)
	}
	out := on.String()
	for _, p := range []string{"first ask", "second ask"} {
		if !strings.Contains(out, "❯ "+p) {
			t.Errorf("output missing %q with glyph: %q", p, out)
		}
	}
	// Prompts are grouped on a rail and the block is closed by a rule.
	if !strings.Contains(out, "│ ❯ first ask") {
		t.Errorf("prompt not on the rail: %q", out)
	}
	if !strings.Contains(out, "╰─") {
		t.Errorf("session block not closed by a rule: %q", out)
	}
}

func assertIDs(t *testing.T, got []model.Summary, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d sessions %v, want %d %v", len(got), ids(got), len(want), want)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("position %d = %q, want %q (got %v)", i, got[i].ID, want[i], ids(got))
		}
	}
}

func ids(sums []model.Summary) []string {
	out := make([]string, len(sums))
	for i, s := range sums {
		out[i] = s.ID
	}
	return out
}
