package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eitanpo/agentry/internal/locate"
)

// parseTestdata is the log directory as an absolute path, resolved at package
// init. A relative path cannot serve: these tests change the working directory,
// so the second call in one test function would resolve against the temporary
// directory the first call left behind.
var parseTestdata = func() string {
	abs, err := filepath.Abs(filepath.Join("..", "parse", "testdata"))
	if err != nil {
		panic(err)
	}
	return abs
}()

// searchFixture drops one named log into the project folder for a fresh working
// directory and returns its session id. It takes the log to use, where
// fixtureProject fixes one: the delegation log is what puts an Agent
// instruction in reach, and that part is the whole reason this verb matters.
//
// A log with a subagent sidecar beside it brings the sidecar too, under the name
// the parser derives from the log it is written beside. Without it a fixture's
// delegated turns load as tool calls with nothing beneath them, and anything
// about a nested stream reads as absent rather than as untested.
func searchFixture(t *testing.T, logFile, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parseTestdata, logFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	orig := locate.ProjectsRoot
	locate.ProjectsRoot = root
	t.Cleanup(func() { locate.ProjectsRoot = orig })

	dir := filepath.Join(root, locate.ProjectDirName(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sidecars := filepath.Join(parseTestdata, strings.TrimSuffix(logFile, ".jsonl"), "subagents")
	if entries, err := os.ReadDir(sidecars); err == nil {
		into := filepath.Join(dir, id, "subagents")
		if err := os.MkdirAll(into, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			sidecar, err := os.ReadFile(filepath.Join(sidecars, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(into, entry.Name()), sidecar, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return id
}

// TestSearchBareFormSearchesTheSessionViewWouldRender pins the invocation this
// verb exists for: the question a caller has while reading a session, answered
// without naming the session again.
func TestSearchBareFormSearchesTheSessionViewWouldRender(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	code, out, errOut := exec("search", "second prompt")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "second prompt") {
		t.Errorf("stdout does not carry the hit: %q", out)
	}
	// Turn 3, not 2: the sample log holds a typed `!` shell command between the
	// two prompts, and that opens a turn like any other prompt.
	if !strings.Contains(out, "turn 3") {
		t.Errorf("stdout does not name the turn to go read: %q", out)
	}
}

// TestSearchReachesADelegatedInstruction pins the passage that used to be
// unreachable: the brief an Agent call handed a subagent, which the activation
// line replaces with a description of a few words.
func TestSearchReachesADelegatedInstruction(t *testing.T) {
	id := searchFixture(t, "agent-delegation.jsonl", "aa11bb22-cc33-dd44-ee55-ff6677889900")
	code, out, errOut := exec("search", "find every caller", id)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "instruction") {
		t.Errorf("the hit is not reported as an instruction: %q", out)
	}
	if !strings.Contains(out, "Agent[Explore@haiku]") {
		t.Errorf("the hit does not name the call it sits in: %q", out)
	}
}

// TestSearchMatchingNothingExitsZero pins the empty result as an ordinary
// answer rather than a failure — the rule a listing whose filters excluded every
// session already follows. It differs from grep on purpose, so the difference is
// pinned rather than left to be discovered by a script.
func TestSearchMatchingNothingExitsZero(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	code, out, errOut := exec("search", "no such passage anywhere")
	if code != 0 {
		t.Errorf("exit = %d, want 0 — nothing matched is an answer, not a failure", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

// TestSearchIsCaseInsensitive pins the pattern semantics shared with the
// listing's --reply-matches, both compiled by one function so a pattern typed at
// either means one thing.
func TestSearchIsCaseInsensitive(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	if _, out, _ := exec("search", "SECOND PROMPT"); !strings.Contains(out, "second prompt") {
		t.Errorf("an upper-case pattern missed a lower-case line: %q", out)
	}
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	if _, out, _ := exec("search", "(?-i)SECOND PROMPT"); out != "" {
		t.Errorf("(?-i) did not restore case sensitivity: %q", out)
	}
}

// TestSearchRejectsAMalformedPattern pins the diagnostic: a usage error naming
// the pattern, since cobra would otherwise report only that a flag was bad.
func TestSearchRejectsAMalformedPattern(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	code, out, errOut := exec("search", "unclosed(")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "unclosed(") {
		t.Errorf("stderr does not name the pattern: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
}

// TestSearchRejectsFromBesideAnID pins the pair the render path already
// rejects: the id has chosen the session, so the selector could only contradict
// it, and accepting both silently would leave a caller believing it applied.
func TestSearchRejectsFromBesideAnID(t *testing.T) {
	id := searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	code, _, errOut := exec("search", "second prompt", id, "--from", "sdk")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "--from") {
		t.Errorf("stderr does not name the offending flag: %q", errOut)
	}
}

// TestSearchCarriesNoRenderFlags pins that verbosity is not this verb's axis. A
// search bounded by --level would report a passage absent when the level merely
// hid it, which is the failure the verb exists to remove.
func TestSearchCarriesNoRenderFlags(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	code, _, errOut := exec("search", "second prompt", "--level", "full")
	if code == 0 {
		t.Errorf("exit = 0, want a usage error — --level is not a search flag")
	}
	if !strings.Contains(errOut, "level") {
		t.Errorf("stderr does not name the unknown flag: %q", errOut)
	}

	// The thinking channel is searched whatever a render would show, which is
	// what makes the flag unnecessary rather than merely absent.
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	_, out, _ := exec("search", "let me think")
	if !strings.Contains(out, "thinking") {
		t.Errorf("a thinking block was not searched at the default level: %q", out)
	}
}

// TestSearchMachineFormats pins both machine forms: an array that is always
// well-formed, and one enveloped record per hit.
func TestSearchMachineFormats(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	_, out, _ := exec("search", "second prompt", "--format", "json")
	var hits []map[string]any
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("stdout is not valid JSON (%v): %q", err, out)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0]["part"] != "prompt" {
		t.Errorf("part = %v, want prompt", hits[0]["part"])
	}

	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	_, out, _ = exec("search", "no such passage anywhere", "--format", "json")
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("no hits emitted %q, want [] so a consumer needs no guard", out)
	}

	id := searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	_, out, _ = exec("search", "second prompt", "--format", "jsonl")
	var env struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
	}
	first := strings.SplitN(strings.TrimRight(out, "\n"), "\n", 2)[0]
	if err := json.Unmarshal([]byte(first), &env); err != nil {
		t.Fatalf("stdout is not a JSON line (%v): %q", err, out)
	}
	if env.Type != "hit" {
		t.Errorf("type = %q, want hit", env.Type)
	}
	if env.SessionID != id {
		t.Errorf("sessionId = %q, want %q", env.SessionID, id)
	}
}

// TestSearchIsSuggestedForAMistypedVerb pins the verb into the did-you-mean set,
// so a caller who typed the neighbouring word is pointed at it rather than told
// the command does not exist.
func TestSearchIsSuggestedForAMistypedVerb(t *testing.T) {
	searchFixture(t, "sample.jsonl", "ba6b3ded-475b-4c3a-96fe-99698a557d14")
	_, _, errOut := exec("serach")
	if !strings.Contains(errOut, "search") {
		t.Errorf("stderr does not suggest the verb: %q", errOut)
	}
}

// TestSearchLiteralPatternReadsTheTextItWasGiven pins -F on both sides: the
// punctuation a caller typed stops being an expression, and a pattern that was
// only ever a parse error becomes a search. Case is deliberately still ignored —
// the flag chooses how the pattern is read, not how it is compared.
func TestSearchLiteralPatternReadsTheTextItWasGiven(t *testing.T) {
	const id = "ba6b3ded-475b-4c3a-96fe-99698a557d14"

	searchFixture(t, "sample.jsonl", id)
	code, out, errOut := exec("search", "l. -la")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "ls -la") {
		t.Fatalf("the expression form did not match, so the pair below proves nothing: %q", out)
	}

	searchFixture(t, "sample.jsonl", id)
	code, out, errOut = exec("search", "-F", "l. -la")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("-F let the dot match any character: %q", out)
	}

	searchFixture(t, "sample.jsonl", id)
	if _, out, _ = exec("search", "-F", "ls -la"); !strings.Contains(out, "ls -la") {
		t.Errorf("-F did not find text that is really there: %q", out)
	}

	searchFixture(t, "sample.jsonl", id)
	if _, out, _ = exec("search", "-F", "LS -LA"); !strings.Contains(out, "ls -la") {
		t.Errorf("-F started caring about case: %q", out)
	}

	// The errand the flag exists for: punctuation that is not a pattern at all.
	searchFixture(t, "sample.jsonl", id)
	if code, _, _ = exec("search", "prompt("); code != exUsage {
		t.Errorf("an unclosed group without -F exits %d, want %d (exUsage)", code, exUsage)
	}
	searchFixture(t, "sample.jsonl", id)
	if code, _, errOut = exec("search", "-F", "prompt("); code != 0 {
		t.Errorf("an unclosed group with -F exits %d, want 0 (stderr %q)", code, errOut)
	}
}

// TestSearchRefusesAPatternThatSpansLines pins the refusal. A hit is one line, so
// a pattern needing a break between two can only ever print nothing, and nothing
// printed is how this verb says a passage is absent from the session.
func TestSearchRefusesAPatternThatSpansLines(t *testing.T) {
	const id = "ba6b3ded-475b-4c3a-96fe-99698a557d14"

	searchFixture(t, "sample.jsonl", id)
	code, out, errOut := exec("search", `second\nprompt`)
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "line break") {
		t.Errorf("the error does not say what is wrong with the pattern: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}

	// A real break, which is what -F has to be checked against: escaping cannot
	// turn it into anything a line-oriented hit could hold.
	searchFixture(t, "sample.jsonl", id)
	if code, _, _ = exec("search", "-F", "second\nprompt"); code != exUsage {
		t.Errorf("a literal pattern holding a break exits %d, want %d (exUsage)", code, exUsage)
	}

	// A class that may match a break is not a pattern that needs one, and
	// rejecting it would refuse the commonest way to write "any whitespace".
	searchFixture(t, "sample.jsonl", id)
	if code, out, errOut = exec("search", `second\sprompt`); code != 0 {
		t.Errorf("a whitespace class exits %d, want 0 (stderr %q)", code, errOut)
	}
}

// TestListFixedStringsNeedsAPatternToRead pins the flag's scope on the listing.
// It governs the reply filter, and on a listing that gave no pattern it would do
// nothing at all — the silence the bare command used to keep about a render flag.
func TestListFixedStringsNeedsAPatternToRead(t *testing.T) {
	const id = "ba6b3ded-475b-4c3a-96fe-99698a557d14"

	searchFixture(t, "sample.jsonl", id)
	code, out, errOut := exec("list", "-F")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "reply-matches") {
		t.Errorf("the error does not name the flag it governs: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}

	// Beside the filter it governs, it reads that filter's pattern as text.
	searchFixture(t, "sample.jsonl", id)
	if _, out, errOut = exec("list", "--reply-matches", "here i. an answer"); !strings.Contains(out, id[:8]) {
		t.Fatalf("the expression form selected nothing, so the pair below proves nothing (stdout %q, stderr %q)", out, errOut)
	}
	searchFixture(t, "sample.jsonl", id)
	if _, out, _ = exec("list", "-F", "--reply-matches", "here i. an answer"); strings.Contains(out, id[:8]) {
		t.Errorf("-F let the dot match any character in a reply filter: %q", out)
	}
	searchFixture(t, "sample.jsonl", id)
	if _, out, _ = exec("list", "-F", "--reply-matches", "here is an answer"); !strings.Contains(out, id[:8]) {
		t.Errorf("-F did not select a session whose reply really holds that text: %q", out)
	}
}

// TestSearchHelpNamesTheCaseDefaultAndItsEscape pins the sentence the flag help
// owes a caller. Matching insensitively is not what someone arriving from grep
// expects, and a default whose escape is written only in the spec is one they
// cannot get out of from the terminal they are in.
func TestSearchHelpNamesTheCaseDefaultAndItsEscape(t *testing.T) {
	code, out, errOut := exec("search", "--help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	// The engine is named because two constructs a caller reaches for are not in
	// it, and no flag here can supply them: found by tripping over it, that reads
	// as a defect in the search rather than a property of the engine.
	for _, want := range []string{"without regard to case", "(?-i)", "-F", "RE2", "Look-around", "backreferences"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help does not mention %q: %q", want, out)
		}
	}
}
