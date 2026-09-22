package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/eitanpo/agentry/internal/search"
)

func hitFixture() []search.Hit {
	return []search.Hit{
		{Turn: 3, Part: search.PartPrompt, Line: 1, Text: "find every caller"},
		{
			Turn: 3, Part: search.PartInstruction, Line: 1,
			Tool: "Agent", Identity: "Explore", Model: "haiku",
			Text: "sweep the tally helper",
		},
		{
			Turn: 3, Delegation: []string{"Agent[Explore@haiku]"}, Part: search.PartText,
			Line: 4, Text: "the tally helper is called twice",
		},
	}
}

// TestHitsFindingShape pins the block a matching turn prints: the turn named
// once with the count of its matching lines, then each line as a locator row and
// the matching text indented beneath it. The turn used to sit beside every line,
// which restated one address on row after row — a pattern that occurs in a path
// produced hundreds of rows all reading "turn 1".
//
// One row per line was the older shape still and did not fit either: the locator
// alone runs to 58 columns on a real session. Each level's indent is what tells
// the three kinds of row apart, there being no blank line between them.
func TestHitsFindingShape(t *testing.T) {
	var b strings.Builder
	if err := Hits(&b, hitFixture(), regexp.MustCompile("tally helper"), false, 0); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	want := []string{
		"turn 3 · 3 lines",
		"  prompt:1",
		"    find every caller",
		"  Agent[Explore@haiku] instruction:1",
		"    sweep the tally helper",
		// Line 4 of that subagent's text block: the number is what says a hit sits
		// past the ten lines a rendered result body would show.
		"  Agent[Explore@haiku] › text:4",
		"    the tally helper is called twice",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %d, want a turn row and two per line (%d):\n%s", len(lines), len(want), b.String())
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, lines[i], w)
		}
	}
}

// TestFindingsHeadsEachSessionOnce pins the grouping a cross-session run prints:
// one heading per session, the findings indented under it, and the heading
// carrying the fields a caller chooses between sessions on. A run confined to one
// session prints no heading at all — the caller already knows which session they
// searched, and a heading would indent every finding to tell them.
//
// It also pins the three properties the row's layout exists for: the rows print
// oldest-to-newest although selection hands them over newest-first, every field
// but the title is a column of one width, and a title carrying a line break is
// joined rather than ended at the break.
func TestFindingsHeadsEachSessionOnce(t *testing.T) {
	newer := time.Date(2026, 5, 2, 11, 0, 0, 0, time.Local)
	older := time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local)
	// Newest first, the order selection hands over.
	groups := []search.Group{
		{
			Match: search.Match{Session: "bbbb2222", Activity: newer, Project: "agentry", Title: "second\nand a second line", Matched: 2, Turns: 30},
			Hits:  []search.Hit{{Session: "bbbb2222", Turn: 9, Part: search.PartText, Line: 2, Text: "the tally helper"}},
		},
		{
			Match: search.Match{Session: "aaaa1111", Activity: older, Project: "dotfiles", Title: "first", Matched: 1, Turns: 4},
			Hits:  []search.Hit{{Session: "aaaa1111", Turn: 3, Part: search.PartPrompt, Line: 1, Text: "find every caller"}},
		},
	}
	var b strings.Builder
	if err := Findings(&b, groups, nil, false, 0); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// Exact rows, because every space in them is doing work: the count is
	// right-aligned to the widest, the label padded to the widest, and the title
	// last and whole.
	wantOlder := "2026-05-01 10:00   1/4t  dotfiles  aaaa1111  first"
	wantNewer := "2026-05-02 11:00  2/30t  agentry   bbbb2222  second and a second line"
	for _, want := range []string{
		wantOlder,
		wantNewer,
		"  │ turn 3 · 1 line",
		"  │   prompt:1",
		"  │     find every caller",
		"  ╰─",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}

	// Oldest first, so the most recent match lands nearest the prompt.
	if i, j := strings.Index(out, wantOlder), strings.Index(out, wantNewer); i < 0 || j < 0 || i > j {
		t.Errorf("rows print newest-first: older at %d, newer at %d", i, j)
	}

	// The id starts at the same column in both rows, which is the whole point of
	// padding the fields before it: a ragged edge is what a reader cannot scan.
	if i, j := strings.Index(wantOlder, "aaaa1111"), strings.Index(wantNewer, "bbbb2222"); i != j {
		t.Errorf("the id column starts at %d in one row and %d in the other", i, j)
	}

	// The heading is the session row, so `search session` and a `search turn`
	// heading describe one session one way — laid out over the same run, since a
	// column's width is a property of the run and not of one row.
	var rows strings.Builder
	if err := Matches(&rows, []search.Match{groups[0].Match, groups[1].Match}, false); err != nil {
		t.Fatal(err)
	}
	for _, row := range strings.Split(strings.TrimRight(rows.String(), "\n"), "\n") {
		if !strings.Contains(out, row) {
			t.Errorf("a session row and the heading for it differ:\n row %q\n headings %q", row, out)
		}
	}
	// And `search session` on its own orders them the same way. Asserted against
	// its own output rather than inferred from the headings: the two write the run
	// through different loops, and only one of them was reversed at first.
	if i, j := strings.Index(rows.String(), wantOlder), strings.Index(rows.String(), wantNewer); i < 0 || j < 0 || i > j {
		t.Errorf("session rows print newest-first: older at %d, newer at %d", i, j)
	}

	var one strings.Builder
	if err := Hits(&one, groups[0].Hits, nil, false, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(one.String(), "aaaa1111") {
		t.Errorf("a one-session run printed a heading: %q", one.String())
	}
	if strings.HasPrefix(one.String(), " ") {
		t.Errorf("a one-session run indented its findings: %q", one.String())
	}
}

// TestHitsNamesACallOneWay pins that a call inside which a hit sits and the same
// call in a delegation chain print the identical label. Two spellings would make
// one hit list disagree with itself about which call it is pointing at.
func TestHitsNamesACallOneWay(t *testing.T) {
	var b strings.Builder
	if err := Hits(&b, hitFixture(), nil, false, 0); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "Agent[Explore@haiku]"); n != 2 {
		t.Errorf("the bracketed label appears %d times, want 2 — once inside the call, once in the chain:\n%s", n, b.String())
	}
}

// TestHitsCutsALongLineAroundItsMatch pins the one-row rule and the window it
// cuts. A matching line can be a whole captured result — twenty-five thousand
// characters occurs in real logs, which wraps to hundreds of screen rows and
// buries every other finding in the run.
//
// The window is centred on the match, and the failure that demands it is the
// head-cut: a window taken from the start of a long line need not hold the match
// at all, so the row would print characters the pattern never touched while
// claiming to show where it matched.
func TestHitsCutsALongLineAroundItsMatch(t *testing.T) {
	const column = 60
	// The match sits far past the column, which is where a head-cut window loses
	// it.
	long := strings.Repeat("token ", 200) + "the tally helper" + strings.Repeat(" more", 200)
	hits := []search.Hit{{Turn: 1, Part: search.PartResult, Tool: "Read", Line: 1, Text: long}}
	var b strings.Builder
	if err := Hits(&b, hits, regexp.MustCompile("tally helper"), false, column); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "tally helper") {
		t.Errorf("the window does not hold the match it reported: %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the line was cut and nothing says so: %q", out)
	}
	// A turn row, a locator and one text row however long the text, and the text
	// row fits the column it was cut to — its own indent counted in, since a row
	// that fits its budget and not the terminal still wraps.
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 for one finding in one turn: %q", len(rows), out)
	}
	if n := utf8.RuneCountInString(rows[2]); n > column {
		t.Errorf("the text row is %d columns wide, want at most %d: %q", n, column, rows[2])
	}

	// A line that fits is printed whole, with nothing to name.
	short := []search.Hit{{Turn: 1, Part: search.PartText, Line: 1, Text: "the tally helper"}}
	var fits strings.Builder
	if err := Hits(&fits, short, regexp.MustCompile("tally"), false, column); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fits.String(), "the tally helper") || strings.Contains(fits.String(), "…") {
		t.Errorf("a line that fits was cut: %q", fits.String())
	}
}

// TestHitsHighlightsOnlyWithColor pins the emphasis as an aid rather than the
// carrier of a fact: with color off the line is the text verbatim.
func TestHitsHighlightsOnlyWithColor(t *testing.T) {
	hits := []search.Hit{{Turn: 1, Part: search.PartText, Line: 1, Text: "the tally helper"}}
	var plain strings.Builder
	if err := Hits(&plain, hits, regexp.MustCompile("tally"), false, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b") {
		t.Errorf("color was off and an escape was written: %q", plain.String())
	}
	if !strings.HasSuffix(strings.TrimRight(plain.String(), "\n"), "the tally helper") {
		t.Errorf("the text was altered with color off: %q", plain.String())
	}
}

// TestHitsJSONIsAlwaysAnArray pins the empty case as `[]` rather than null, the
// rule the listing's array already follows so a consumer can pipe into jq
// without first branching on which of two shapes it got back.
func TestHitsJSONIsAlwaysAnArray(t *testing.T) {
	var b strings.Builder
	if err := HitsJSON(&b, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Errorf("no hits emitted %q, want []", got)
	}

	b.Reset()
	if err := HitsJSON(&b, hitFixture()); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(b.String()), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("elements = %d, want 3", len(out))
	}
	// The line number is the field the text lines leave out, so its absence here
	// would leave it nowhere at all.
	if out[2]["line"] != float64(4) {
		t.Errorf("line = %v, want 4", out[2]["line"])
	}
	if out[2]["model"] != nil {
		t.Errorf("a hit in no call carries a model: %v", out[2]["model"])
	}
}

// TestHitsJSONLRecords pins the record type and where the session id rides: on
// the envelope, which is what says whose hits a merged stream's lines are.
func TestHitsJSONLRecords(t *testing.T) {
	var b strings.Builder
	if err := HitsJSONL(&b, hitFixture(), "sess-1"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("records = %d, want 3", len(lines))
	}
	var env struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Ordinal   int    `json:"ordinal"`
		Payload   struct {
			Part string `json:"part"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "hit" {
		t.Errorf("type = %q, want hit", env.Type)
	}
	if env.SessionID != "sess-1" {
		t.Errorf("sessionId = %q, want sess-1", env.SessionID)
	}
	if env.Ordinal != 1 {
		t.Errorf("ordinal = %d, want 1", env.Ordinal)
	}
	if env.Payload.Part != "prompt" {
		t.Errorf("payload part = %q, want prompt", env.Payload.Part)
	}

	// Nothing matched writes nothing, there being no wrapper to emit — the rule
	// the listing's stream already follows.
	b.Reset()
	if err := HitsJSONL(&b, nil, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "" {
		t.Errorf("no hits wrote %q, want nothing", b.String())
	}
}

// TestATurnShowsASampleOfItsLines pins the block a heavily matching turn prints:
// the turn named once with how many of its lines matched, three of those lines,
// and a row naming the rest. A pattern that occurs in a path matches in every
// command that touched it, so one turn can carry hundreds of lines — printed in
// full they were a reader's whole screen, every row restating one turn number.
//
// The sample also passes over a row already shown, judged by the window the
// reader sees rather than the whole line: the lines behind those rows differ
// only in a tail the window cuts, so three of them said less than one.
func TestATurnShowsASampleOfItsLines(t *testing.T) {
	var hits []search.Hit
	for _, text := range []string{
		"alpha tally", "alpha tally", "beta tally", "gamma tally", "delta tally", "epsilon tally",
	} {
		hits = append(hits, search.Hit{Turn: 1, Part: search.PartText, Line: len(hits) + 1, Text: text})
	}
	hits = append(hits, search.Hit{Turn: 4, Part: search.PartPrompt, Line: 1, Text: "omega tally"})

	var b strings.Builder
	if err := Hits(&b, hits, regexp.MustCompile("tally"), false, 0); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"turn 1 · 6 lines",
		"    alpha tally",
		"    beta tally",
		"    gamma tally",
		"…  (3 more lines)",
		"turn 4 · 1 line",
		"    omega tally",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	for _, gone := range []string{"delta tally", "epsilon tally"} {
		if strings.Contains(out, gone) {
			t.Errorf("a line past the sample printed anyway (%q):\n%s", gone, out)
		}
	}
	if n := strings.Count(out, "alpha tally"); n != 1 {
		t.Errorf("the repeated line printed %d times, want once:\n%s", n, out)
	}
	// The turn is the address a reader navigates to, and naming it once per block
	// rather than once per line is the whole point of the block.
	if n := strings.Count(out, "turn 1"); n != 1 {
		t.Errorf("turn 1 is named %d times, want once:\n%s", n, out)
	}
}
