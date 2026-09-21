package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/eitanpo/agentry/internal/config"
)

// turnFixture drops the sample log into a fresh project and returns its id. The
// log holds three turns — two prompts with a typed `!` shell command between
// them, which opens a turn of its own — so a span has somewhere to stop short of
// the end.
func turnFixture(t *testing.T) string {
	t.Helper()
	return searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
}

// headerOf is the render's opening box — everything up to and including the line
// that closes it. The render package has a helper of this name for its own tests;
// this is the same idea in the package that owns the flag, rather than an export
// added to render for a test's sake.
func headerOf(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "\u2570") {
			return strings.Join(lines[:i+1], "\n")
		}
	}
	return out
}

// TestTurnRendersOneTurn pins the flag's point: the transcript narrows, and the
// output says which turns it is showing rather than looking like a whole session.
func TestTurnRendersOneTurn(t *testing.T) {
	id := turnFixture(t)
	code, out, errOut := exec(id, "--turn", "1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "first prompt") {
		t.Errorf("the selected turn is missing: %q", out)
	}
	if strings.Contains(out, "second prompt") {
		t.Errorf("an unselected turn was rendered: %q", out)
	}
	if !strings.Contains(out, "showing turn 1 of 3 turns") {
		t.Errorf("the render does not say what it is showing: %q", out)
	}
}

// TestTurnSpanReadsBackAsItsFlag pins the dash. A reader copies the span out of
// the line and passes it to --turn, so the line must print the syntax the flag
// accepts rather than a typographic dash they cannot type.
func TestTurnSpanReadsBackAsItsFlag(t *testing.T) {
	id := turnFixture(t)
	_, out, _ := exec(id, "--turn", "1-2")
	if !strings.Contains(out, "showing turns 1-2 of 3 turns") {
		t.Errorf("span line wrong: %q", out)
	}
	if strings.Contains(out, "1–2") {
		t.Errorf("the span printed an en dash, which --turn does not accept: %q", out)
	}
}

// TestTurnHeaderStillDescribesTheSession pins the fix for the first shape this
// took. Counting the header off the rendered turns printed "1 turn" beside the
// whole session's tokens and dollars, which reads as one turn having cost the lot.
func TestTurnHeaderStillDescribesTheSession(t *testing.T) {
	id := turnFixture(t)
	_, sliced, _ := exec(id, "--turn", "1")
	if !strings.Contains(sliced, "3 turns") {
		t.Errorf("header does not count the whole session: %q", headerOf(sliced))
	}
	if strings.Contains(headerOf(sliced), "1 turn ") {
		t.Errorf("header counted the slice: %q", headerOf(sliced))
	}

	// And a whole render is unchanged by the flag existing.
	id = turnFixture(t)
	_, whole, _ := exec(id)
	if !strings.Contains(whole, "3 turns") {
		t.Errorf("an unnarrowed header changed: %q", headerOf(whole))
	}
	if strings.Contains(whole, "showing turn") {
		t.Errorf("a whole render printed a selection line: %q", whole)
	}
}

// TestTurnBoundsAreCheckedAgainstTheSession pins the two rules: the start must
// exist, and the end is a ceiling. A caller asking for turn 99 has made a
// mistake an empty render would not report; a caller asking for 2-99 means "to
// the end".
func TestTurnBoundsAreCheckedAgainstTheSession(t *testing.T) {
	id := turnFixture(t)
	code, _, errOut := exec(id, "--turn", "99")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "3 turns") {
		t.Errorf("the error does not name the count: %q", errOut)
	}

	id = turnFixture(t)
	code, out, _ := exec(id, "--turn", "2-99")
	if code != 0 {
		t.Errorf("exit = %d, want 0 — an end past the last turn clamps", code)
	}
	if !strings.Contains(out, "showing turns 2-3 of 3 turns") {
		t.Errorf("the span did not clamp to the last turn: %q", out)
	}
}

// TestTurnRejectsValuesWithNoReading pins the values that can only be mistakes.
// Each error names the value, since cobra reports only that a flag was bad.
func TestTurnRejectsValuesWithNoReading(t *testing.T) {
	cases := []struct{ value, says string }{
		{"0", "numbered from 1"},
		{"3-1", "ends before it starts"},
		{"abc", "want a turn number"},
		{"2-", "want a turn number"},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			id := turnFixture(t)
			code, out, errOut := exec(id, "--turn", c.value)
			if code != exUsage {
				t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
			}
			if !strings.Contains(errOut, c.says) {
				t.Errorf("stderr %q does not explain the problem (%q)", errOut, c.says)
			}
			if !strings.Contains(errOut, c.value) {
				t.Errorf("stderr %q does not name the value", errOut)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
		})
	}
}

// TestTurnNarrowsTheMachineForms pins that a turn number is a selector rather
// than a view: it reaches --format json and jsonl, where --level and the channel
// toggles do not. Both fields that keep a slice legible are checked, since
// without them a one-turn document cannot say which turn of how many.
func TestTurnNarrowsTheMachineForms(t *testing.T) {
	id := turnFixture(t)
	_, out, _ := exec(id, "--turn", "3", "--format", "json")
	var doc struct {
		Meta struct {
			NumTurns int `json:"numTurns"`
		} `json:"meta"`
		Turns []struct {
			Turn int `json:"turn"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %q", err, out)
	}
	if len(doc.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(doc.Turns))
	}
	if doc.Turns[0].Turn != 3 {
		t.Errorf("the turn is numbered %d, want 3 — its index in the slice is not its place", doc.Turns[0].Turn)
	}
	if doc.Meta.NumTurns != 3 {
		t.Errorf("numTurns = %d, want the session's 3", doc.Meta.NumTurns)
	}

	id = turnFixture(t)
	_, out, _ = exec(id, "--turn", "3", "--format", "jsonl")
	turns := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Turn int `json:"turn"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("not a JSON line (%v): %q", err, line)
		}
		if rec.Type != "turn" {
			continue
		}
		turns++
		if rec.Payload.Turn != 3 {
			t.Errorf("turn record numbered %d, want 3", rec.Payload.Turn)
		}
	}
	if turns != 1 {
		t.Errorf("turn records = %d, want 1", turns)
	}
}

// TestRenderFlagGroupMembership pins the group's contents as a literal list.
// The rejection subtests below derive their cases from renderFlagNames, so they
// cannot notice a flag dropped from it — dropping one silently removes both its
// help grouping and its rejection, which is two invisible regressions from one
// edit. Naming the members here is what makes that edit fail.
//
// The second half is the direction that matters more: a render flag registered
// on the command and left out of the group would be silently ignored on a
// listing all over again. Every flag the bare command carries is therefore
// accounted for, either as a render flag or as one of the listing and global
// flags named here.
func TestRenderFlagGroupMembership(t *testing.T) {
	want := []string{
		"level", "turn", "no-metrics",
		"thinking", "no-thinking",
		"tools", "no-tools",
		"tool-results", "no-tool-results",
		"subagents", "no-subagents",
	}
	got := renderFlagNames()
	if len(got) != len(want) {
		t.Fatalf("render group = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("render group[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Anything the bare command carries that is not a render flag has to be a
	// listing selector, an output-form switch, or global. A new render flag will
	// land here and fail until it joins the group.
	// The listing's own flags are taken from the `list` verb rather than written
	// out here, so a new selector added there does not fail this test. Only the
	// three the root adds for itself are named.
	notRender := map[string]bool{"no-color": true, "version": true, "help": true}
	newListCmd(new(bool)).Flags().VisitAll(func(f *pflag.Flag) { notRender[f.Name] = true })
	root := newRootCmd("test", nil)
	root.Flags().VisitAll(func(f *pflag.Flag) {
		if isRenderFlag(f.Name) || notRender[f.Name] {
			return
		}
		t.Errorf("--%s is on the bare command and in neither group: add it to renderFlagNames or to this test's listing set", f.Name)
	})
}

// TestRenderFlagsAreNotListingFlags pins that every render flag errors on a
// bare listing rather than being ignored. The bare command carries both flag
// sets because it lists or renders depending on its argument, so these were
// registered and did nothing at all when no id followed — a caller who asked for
// full detail got a listing and no sign their flag had been dropped.
func TestRenderFlagsAreNotListingFlags(t *testing.T) {
	for _, flag := range renderFlagNames() {
		t.Run(flag, func(t *testing.T) {
			turnFixture(t)
			args := []string{"--" + flag}
			// The two that take a value need one; the channel toggles are booleans.
			if flag == "level" {
				args = append(args, "full")
			}
			if flag == "turn" {
				args = append(args, "1")
			}
			code, out, errOut := exec(args...)
			if code != exUsage {
				t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
			}
			if !strings.Contains(errOut, "--"+flag) {
				t.Errorf("stderr does not name the flag: %q", errOut)
			}
			if out != "" {
				t.Errorf("a listing was printed anyway: %q", out)
			}
		})
	}
}

// TestRenderFlagsOnAListingAreAllNamed pins that a caller who passed three
// fixes three at once. Reporting the first would send them round the loop once
// per flag, which is the shape a did-you-mean already refuses to take.
func TestRenderFlagsOnAListingAreAllNamed(t *testing.T) {
	turnFixture(t)
	_, _, errOut := exec("--level", "full", "--tools", "--no-thinking")
	for _, want := range []string{"--level", "--tools", "--no-thinking"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not name %s", errOut, want)
		}
	}
	if !strings.Contains(errOut, "belong to") {
		t.Errorf("stderr %q does not read as a plural: three flags were passed", errOut)
	}
}

// TestListingFlagsStillListOnTheBareCommand pins the other side: the flags that
// do belong there keep working, so the rejection is scoped to the render group
// rather than to any flag on a listing.
func TestListingFlagsStillListOnTheBareCommand(t *testing.T) {
	turnFixture(t)
	code, out, errOut := exec("--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out == "" {
		t.Error("a list flag on the bare command printed nothing")
	}

	// --no-color is global rather than the render path's, so it is not in the
	// group and must not be rejected.
	turnFixture(t)
	if code, _, errOut = exec("--no-color"); code != 0 {
		t.Errorf("--no-color on a listing exited %d (stderr %q) — it is global, not a render flag", code, errOut)
	}
}

// TestSettingsFileDefaultIsNotACallerAskingForIt pins why presence is read via
// Changed. A settings file plants view.level as the flag's default, which is not
// the caller passing it — rejecting that would make a file nobody can list with.
func TestSettingsFileDefaultIsNotACallerAskingForIt(t *testing.T) {
	turnFixture(t)
	code, out, errOut := execWith(&config.Settings{Found: true, Verbs: map[string]map[string]string{
		"view": {"level": "full"},
	}}, "--limit", "1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out == "" {
		t.Error("a settings file setting view.level made the bare listing print nothing")
	}
}
