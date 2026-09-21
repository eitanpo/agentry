package search

import (
	"regexp"
	"testing"

	"github.com/eitanpo/agentry/internal/model"
)

// sample is one turn holding every part a hit can sit in, plus a delegated
// stream so a nested hit has a chain to carry. Each body says which part it is,
// so one pattern per part pins that part alone.
func sample() *model.Session {
	return &model.Session{Turns: []model.Turn{
		{
			Prompt: "needle in the prompt",
			Events: []model.Event{
				{Kind: model.EventText, Text: "needle in the prose"},
				{Kind: model.EventThinking, Text: "needle in the reasoning"},
				{Kind: model.EventTool, Tool: &model.Tool{
					Name:     "Agent",
					Identity: "Explore",
					Model:    "haiku",
					Args:     "needle in the args",
					Prompt:   "needle in the instruction",
					Result:   "needle in the result",
					Subagent: []model.Event{
						{Kind: model.EventText, Text: "needle inside the subagent"},
						{Kind: model.EventTool, Tool: &model.Tool{
							Name:   "Bash",
							Args:   "grep needle",
							Result: "needle two levels down",
						}},
					},
				}},
			},
		},
		{Prompt: "nothing here", Events: []model.Event{{Kind: model.EventText, Text: "needle in turn two"}}},
	}}
}

func hitsFor(t *testing.T, pattern string) []Hit {
	t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return In(sample(), re)
}

// TestInCoversEveryPart pins that a search reaches all six parts of a record.
// A part left out is the defect this verb exists to remove: a reader told a
// passage is absent when it is merely in a body nothing looked at.
func TestInCoversEveryPart(t *testing.T) {
	want := map[Part]string{
		PartPrompt:      "needle in the prompt",
		PartText:        "needle in the prose",
		PartThinking:    "needle in the reasoning",
		PartArgs:        "needle in the args",
		PartInstruction: "needle in the instruction",
		PartResult:      "needle in the result",
	}
	got := map[Part]string{}
	for _, h := range hitsFor(t, "needle in the") {
		if _, dup := got[h.Part]; dup {
			continue // the first hit of each part is the one these cases name
		}
		got[h.Part] = h.Text
	}
	for part, text := range want {
		if got[part] != text {
			t.Errorf("part %q: got %q, want %q", part, got[part], text)
		}
	}
}

// TestInCarriesTheDelegationChain pins where a nested hit says it is: the chain
// of calls down to it, and the main thread's turn rather than the subagent's own
// numbering — the turn being what a reader renders to reach the passage.
func TestInCarriesTheDelegationChain(t *testing.T) {
	hits := hitsFor(t, "inside the subagent")
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	h := hits[0]
	if h.Turn != 1 {
		t.Errorf("turn = %d, want the spawning call's turn 1", h.Turn)
	}
	if len(h.Delegation) != 1 || h.Delegation[0] != "Agent[Explore@haiku]" {
		t.Errorf("delegation = %v, want [Agent[Explore@haiku]]", h.Delegation)
	}

	deep := hitsFor(t, "two levels down")
	if len(deep) != 1 {
		t.Fatalf("deep hits = %d, want 1", len(deep))
	}
	// The chain names the call the stream hangs off, not the call the body
	// belongs to — that one is named by Tool on the hit itself.
	if len(deep[0].Delegation) != 1 {
		t.Errorf("delegation = %v, want one element for a hit one stream down", deep[0].Delegation)
	}
	if deep[0].Tool != "Bash" {
		t.Errorf("tool = %q, want Bash", deep[0].Tool)
	}
}

// TestInReportsOneHitPerLine pins the unit: a line, not a match. Two matches on
// one line are one place to go read, and counting them twice would make a hit
// count read as a location count.
func TestInReportsOneHitPerLine(t *testing.T) {
	sess := &model.Session{Turns: []model.Turn{{
		Prompt: "alpha alpha alpha\nbeta\nalpha again",
	}}}
	hits := In(sess, regexp.MustCompile("alpha"))
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 — one per matching line", len(hits))
	}
	if hits[0].Line != 1 || hits[1].Line != 3 {
		t.Errorf("lines = %d and %d, want 1 and 3", hits[0].Line, hits[1].Line)
	}
	if hits[0].Text != "alpha alpha alpha" {
		t.Errorf("text = %q, want the whole line", hits[0].Text)
	}
}

// TestInWalksInDocumentOrder pins that hits arrive in the order a render prints
// them, so a hit list and a rendered session walk one session the same way.
func TestInWalksInDocumentOrder(t *testing.T) {
	hits := hitsFor(t, "needle")
	if len(hits) < 2 {
		t.Fatalf("hits = %d, want several", len(hits))
	}
	lastTurn := 0
	for _, h := range hits {
		if h.Turn < lastTurn {
			t.Fatalf("turn %d came after turn %d", h.Turn, lastTurn)
		}
		lastTurn = h.Turn
	}
	if hits[0].Part != PartPrompt {
		t.Errorf("first hit is %q, want the turn's own prompt", hits[0].Part)
	}
	if hits[len(hits)-1].Turn != 2 {
		t.Errorf("last hit is in turn %d, want turn 2", hits[len(hits)-1].Turn)
	}
}

// TestInFindsNothingWhereThereIsNothing pins the empty result as nil rather
// than a one-element slice of zeroes, which is what a caller counting hits reads.
func TestInFindsNothingWhereThereIsNothing(t *testing.T) {
	if hits := hitsFor(t, "haystack"); len(hits) != 0 {
		t.Errorf("hits = %d, want 0", len(hits))
	}
}

// TestLabelNamesACall pins the one string both places a hit can name a call use.
// Only Agent is bracketed, for the reason the renderer gives: it is the one tool
// whose args hide its identity.
func TestLabelNamesACall(t *testing.T) {
	cases := []struct {
		what                  string
		tool, identity, model string
		want                  string
	}{
		{"an Agent naming both", "Agent", "Explore", "haiku", "Agent[Explore@haiku]"},
		{"an Agent naming no model", "Agent", "researcher", "", "Agent[researcher]"},
		{"an Agent naming neither", "Agent", "", "", "Agent"},
		{"another tool with an identity", "Bash", "git", "", "Bash"},
	}
	for _, c := range cases {
		if got := Label(c.tool, c.identity, c.model); got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, got, c.want)
		}
	}
}
