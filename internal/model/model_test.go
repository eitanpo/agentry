package model

import "testing"

func spanSession() *Session {
	s := &Session{Meta: Meta{ID: "s1", NumTurns: 4, NumSubagents: 2}}
	for i := 1; i <= 4; i++ {
		s.Turns = append(s.Turns, Turn{Number: i, Prompt: "turn"})
	}
	return s
}

// TestSelectKeepsMetaWhole pins the contract a narrowed render rests on: Meta
// describes the session and Turns describes what was asked for. Recomputing Meta
// over the span would report a different session under the same id, and the
// rendered header reads every one of its figures off Meta.
func TestSelectKeepsMetaWhole(t *testing.T) {
	out := Select(spanSession(), TurnRange{From: 2, To: 3})
	if out.Meta.NumTurns != 4 {
		t.Errorf("NumTurns = %d, want the session's 4", out.Meta.NumTurns)
	}
	if out.Meta.NumSubagents != 2 {
		t.Errorf("NumSubagents = %d, want the session's 2", out.Meta.NumSubagents)
	}
	if len(out.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(out.Turns))
	}
	if out.Turns[0].Number != 2 || out.Turns[1].Number != 3 {
		t.Errorf("selected turns %d and %d, want 2 and 3", out.Turns[0].Number, out.Turns[1].Number)
	}
}

// TestSelectSelectsByNumberNotIndex pins which number decides. Position would
// give the same answer only on a session whose turns start at one and run
// unbroken, which is exactly the case that hides the bug.
func TestSelectSelectsByNumberNotIndex(t *testing.T) {
	s := &Session{
		Meta:  Meta{NumTurns: 30},
		Turns: []Turn{{Number: 11, Prompt: "a"}, {Number: 12, Prompt: "b"}},
	}
	out := Select(s, TurnRange{From: 12, To: 12})
	if len(out.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(out.Turns))
	}
	if out.Turns[0].Prompt != "b" {
		t.Errorf("selected %q, want the turn numbered 12", out.Turns[0].Prompt)
	}
}

// TestSelectLeavesTheCallersSessionAlone pins the copy. A caller that asked to
// render part of a session did not ask for its parsed model to lose the rest,
// and the same model is read again for the footer and the closing card.
func TestSelectLeavesTheCallersSessionAlone(t *testing.T) {
	s := spanSession()
	Select(s, TurnRange{From: 1, To: 1})
	if len(s.Turns) != 4 {
		t.Errorf("the caller's session now holds %d turns, want 4", len(s.Turns))
	}
}

// TestSelectEmptySpanIsAnArray pins the empty result as an array rather than
// nil, the rule the render path's JSON already follows: a consumer iterating the
// document's primary collection must not first branch on which shape it got.
func TestSelectEmptySpanIsAnArray(t *testing.T) {
	out := Select(spanSession(), TurnRange{From: 9, To: 9})
	if out.Turns == nil {
		t.Fatal("Turns is nil, want an empty array")
	}
	if len(out.Turns) != 0 {
		t.Errorf("turns = %d, want 0", len(out.Turns))
	}
}
