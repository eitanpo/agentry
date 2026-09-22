package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

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

// TestHitsFindingShape pins the two rows a finding carries and what sits on
// each: the locator names the turn to go read and where inside it the line sits,
// and the matching line follows indented beneath. One row was the old shape and
// did not fit — the locator alone runs to 58 columns on a real session.
//
// The text's indent is what separates one finding from the next, there being no
// blank line between them, so a locator at column zero and its text at column
// two is the boundary rather than a decoration.
func TestHitsFindingShape(t *testing.T) {
	var b strings.Builder
	if err := Hits(&b, hitFixture(), regexp.MustCompile("tally helper"), false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	want := []string{
		"turn 3 · prompt:1",
		"  find every caller",
		"turn 3 · Agent[Explore@haiku] instruction:1",
		"  sweep the tally helper",
		// Line 4 of that subagent's text block: the number is what says a hit sits
		// past the ten lines a rendered result body would show.
		"turn 3 · Agent[Explore@haiku] › text:4",
		"  the tally helper is called twice",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %d, want two per hit (%d):\n%s", len(lines), len(want), b.String())
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
func TestFindingsHeadsEachSessionOnce(t *testing.T) {
	groups := []search.Group{
		{
			Match: search.Match{Session: "aaaa1111", Project: "dotfiles", Title: "first", Turns: 1},
			Hits:  []search.Hit{{Session: "aaaa1111", Turn: 3, Part: search.PartPrompt, Line: 1, Text: "find every caller"}},
		},
		{
			Match: search.Match{Session: "bbbb2222", Project: "agentry", Title: "second", Turns: 1},
			Hits:  []search.Hit{{Session: "bbbb2222", Turn: 9, Part: search.PartText, Line: 2, Text: "the tally helper"}},
		},
	}
	var b strings.Builder
	if err := Findings(&b, groups, nil, false); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"aaaa1111 · first · 1 turn matched",
		"bbbb2222 · second · 1 turn matched",
		"  turn 3 · prompt:1",
		"    find every caller",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	// The heading is the session row, so `search session` and a `search turn`
	// heading describe one session one way.
	var rows strings.Builder
	if err := Matches(&rows, []search.Match{groups[0].Match}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, strings.TrimRight(rows.String(), "\n")) {
		t.Errorf("the heading and the session row differ:\n heading in %q\n row %q", out, rows.String())
	}

	var one strings.Builder
	if err := Hits(&one, groups[0].Hits, nil, false); err != nil {
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
	if err := Hits(&b, hitFixture(), nil, false); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "Agent[Explore@haiku]"); n != 2 {
		t.Errorf("the bracketed label appears %d times, want 2 — once inside the call, once in the chain:\n%s", n, b.String())
	}
}

// TestHitsLeavesLongLinesWhole pins that no line is cut to a width. A hit the
// caller cannot read in full is one they have to go looking for twice, and the
// wrap the terminal does costs them a line instead of the fact.
func TestHitsLeavesLongLinesWhole(t *testing.T) {
	long := strings.Repeat("token ", 200)
	hits := []search.Hit{{Turn: 1, Part: search.PartResult, Tool: "Read", Line: 1, Text: long}}
	var b strings.Builder
	if err := Hits(&b, hits, nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), long) {
		t.Errorf("the matching line was cut: %q", b.String())
	}
	if strings.Contains(b.String(), "…") {
		t.Errorf("a hit line was elided, but nothing here is capped: %q", b.String())
	}
	// One hit is two output lines whatever the text's length: the wrap is the
	// terminal's to do, so nothing here breaks the text across lines itself.
	if n := strings.Count(b.String(), "\n"); n != 2 {
		t.Errorf("newlines = %d, want 2 for one finding", n)
	}
}

// TestHitsHighlightsOnlyWithColor pins the emphasis as an aid rather than the
// carrier of a fact: with color off the line is the text verbatim.
func TestHitsHighlightsOnlyWithColor(t *testing.T) {
	hits := []search.Hit{{Turn: 1, Part: search.PartText, Line: 1, Text: "the tally helper"}}
	var plain strings.Builder
	if err := Hits(&plain, hits, regexp.MustCompile("tally"), false); err != nil {
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
