package list

import (
	"encoding/json"
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

func TestRenderJSON(t *testing.T) {
	sums := []model.Summary{
		{ID: "s1", Title: "do work", NumTurns: 3,
			Tools:    []model.ToolStat{{Tool: "Bash", Identity: "git", Count: 2}},
			Commands: []string{"git status"}},
	}
	var b strings.Builder
	if err := RenderJSON(&b, sums); err != nil {
		t.Fatal(err)
	}
	// Parses back as an array carrying the tagged model fields.
	var got []map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if len(got) != 1 || got[0]["id"] != "s1" || got[0]["title"] != "do work" {
		t.Fatalf("unexpected JSON: %s", b.String())
	}
	tools, ok := got[0]["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing/wrong: %s", b.String())
	}
	tool := tools[0].(map[string]any)
	if tool["tool"] != "Bash" || tool["identity"] != "git" || tool["count"].(float64) != 2 {
		t.Errorf("tool entry wrong: %s", b.String())
	}
	if cmds := got[0]["commands"].([]any); len(cmds) != 1 || cmds[0] != "git status" {
		t.Errorf("commands wrong: %s", b.String())
	}

	// Empty input serializes as an array, not null.
	var empty strings.Builder
	if err := RenderJSON(&empty, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(empty.String()) != "[]" {
		t.Errorf("empty input = %q, want []", empty.String())
	}
}

func TestFilterByTools(t *testing.T) {
	sums := []model.Summary{
		{ID: "expert-run", Tools: []model.ToolStat{
			{Tool: "Skill", Identity: "expert", Count: 2},
			{Tool: "Agent", Identity: "general-purpose", Count: 9},
		}, Commands: []string{"git status", "python3 collect.py"}},
		{ID: "exa-run", Tools: []model.ToolStat{
			{Tool: "Bash", Identity: "exa", Count: 1},
			{Tool: "Skill", Identity: "sonar-search", Count: 1},
		}, Commands: []string{"/skills/exa/scripts/exa --contents q"}},
		{ID: "research", Tools: []model.ToolStat{
			{Tool: "Agent", Identity: "researcher", Count: 3},
		}, Commands: nil},
	}
	match := func(f Filters) []string { return ids(FilterByTools(sums, f)) }
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name string
		f    Filters
		want []string
	}{
		{"empty is no-op", Filters{}, []string{"expert-run", "exa-run", "research"}},
		{"used-tool exact, case-insensitive", Filters{Tool: "bash"}, []string{"exa-run"}},
		{"used-skill substring", Filters{Skill: "sonar"}, []string{"exa-run"}}, // sonar-search
		{"used-agent", Filters{Agent: "researcher"}, []string{"research"}},
		{"used-command substring", Filters{Command: "git"}, []string{"expert-run"}},
		{"used matches command", Filters{Any: "exa"}, []string{"exa-run"}}, // via command text
		{"used matches skill", Filters{Any: "expert"}, []string{"expert-run"}},
		{"used does not match tool name", Filters{Any: "Bash"}, nil}, // identity axis only
		{"AND of two fields", Filters{Skill: "expert", Agent: "general"}, []string{"expert-run"}},
		{"AND with no overlap", Filters{Skill: "expert", Agent: "researcher"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := match(c.f); !eq(got, c.want) {
				t.Errorf("FilterByTools(%+v) = %v, want %v", c.f, got, c.want)
			}
		})
	}
}

func TestRenderIncludeTools(t *testing.T) {
	sums := []model.Summary{
		{ID: "s1", Title: "do work", Tools: []model.ToolStat{
			{Tool: "Bash", Identity: "gh", Count: 12},
			{Tool: "Bash", Identity: "git", Count: 40},
			{Tool: "Skill", Identity: "expert", Count: 2},
			{Tool: "Agent", Identity: "researcher", Count: 9},
			{Tool: "Read", Identity: "", Count: 100},
		}},
	}
	// Off: breakdown absent.
	var off strings.Builder
	if err := Render(&off, sums, Options{Width: 100, Color: false, Tools: false}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(off.String(), "git ×40") {
		t.Errorf("tools should be hidden without Tools: %q", off.String())
	}
	// On: one line per category, entries count-desc, Other by tool name.
	var on strings.Builder
	if err := Render(&on, sums, Options{Width: 100, Color: false, Tools: true}); err != nil {
		t.Fatal(err)
	}
	out := on.String()
	for _, want := range []string{"Skills", "expert ×2", "Agents", "researcher ×9", "Bash", "Other", "Read ×100"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
	// Within Bash, the higher count sorts first.
	if i, j := strings.Index(out, "git ×40"), strings.Index(out, "gh ×12"); i < 0 || j < 0 || i > j {
		t.Errorf("Bash entries not ordered count-desc (git before gh): %q", out)
	}
	// The block is closed by a rule, like --include prompts.
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
