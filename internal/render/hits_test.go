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

// TestHitsLineShape pins the three fields a hit line carries and the order they
// appear in: the turn to go read, where inside it the line sits, then the whole
// matching line. A consumer splitting on the separator reaches the text in one
// cut, so the separator must not appear in either field before it.
func TestHitsLineShape(t *testing.T) {
	var b strings.Builder
	if err := Hits(&b, hitFixture(), regexp.MustCompile("tally helper"), false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want one per hit (3)", len(lines))
	}
	want := []string{
		"turn 3 · prompt:1 · find every caller",
		"turn 3 · Agent[Explore@haiku] instruction:1 · sweep the tally helper",
		// Line 4 of that subagent's text block: the number is what says a hit sits
		// past the ten lines a rendered result body would show.
		"turn 3 · Agent[Explore@haiku] › text:4 · the tally helper is called twice",
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, lines[i], w)
		}
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
	// One hit is one output line, whatever its length — what makes the output
	// pipe into a line-oriented reader.
	if n := strings.Count(b.String(), "\n"); n != 1 {
		t.Errorf("newlines = %d, want 1 for one hit", n)
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
