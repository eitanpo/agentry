package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/render"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"list", "lst", 1},       // one deletion
		{"prompt", "prompts", 1}, // one insertion
		{"kitten", "sitting", 3}, // canonical example
		{"view", "veiw", 2},      // transposition costs 2 in plain edit distance
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNearest(t *testing.T) {
	cases := []struct {
		tok        string
		candidates []string
		want       string
	}{
		{"lst", verbNames, "list"},          // mistyped verb
		{"veiw", verbNames, "view"},         // transposed verb, within threshold
		{"prompt", includeNames, "prompts"}, // the reported flag-value typo
		{"detaild", levelNames, "detailed"}, // mistyped level
		{"al", includeNames, "all"},
		{"xyzzy", verbNames, ""},             // nothing close enough
		{"zzzzzzzz", levelNames, ""},         // far from every candidate
		{"prompts", includeNames, "prompts"}, // exact match returns itself
	}
	for _, c := range cases {
		if got := nearest(c.tok, c.candidates); got != c.want {
			t.Errorf("nearest(%q, %v) = %q, want %q", c.tok, c.candidates, got, c.want)
		}
	}
}

func TestLooksLikeID(t *testing.T) {
	ids := []string{
		"deadbeef",
		"ba6b3ded-475b-4c3a-96fe-99698a557d14",
		"ABCDEF0123", // uppercase hex
	}
	for _, s := range ids {
		if !looksLikeID(s) {
			t.Errorf("looksLikeID(%q) = false, want true", s)
		}
	}
	notIDs := []string{
		"", "list", "view", "lst", "search", "xyz",
	}
	for _, s := range notIDs {
		if looksLikeID(s) {
			t.Errorf("looksLikeID(%q) = true, want false", s)
		}
	}
}

// exec runs an isolated command tree with the given args, returning the exit
// code and whatever was written to stdout and stderr.
func exec(args ...string) (code int, stdout, stderr string) {
	root := newRootCmd("test")
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	code = run(root, args)
	return code, out.String(), errBuf.String()
}

// These cases all fail before any filesystem access, so they are deterministic
// regardless of the working directory.
func TestUsageErrorsSuggest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // substring expected on stderr
	}{
		{"mistyped verb", []string{"lst"}, `did you mean "list"`},
		{"mistyped flag value", []string{"list", "--include", "prompt"}, `did you mean "prompts"`},
		{"mistyped flag name", []string{"--thnking"}, "did you mean --thinking"},
		{"mistyped level value", []string{"--level", "detaild"}, `did you mean "detailed"`},
		{"list rejects positional", []string{"list", "foo"}, `unknown command "foo"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, stderr := exec(c.args...)
			if code != exUsage {
				t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr = %q, want substring %q", stderr, c.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := exec("--version")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if want := "agentry test"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want substring %q", stdout, want)
	}
}

func TestHelpExitsZero(t *testing.T) {
	code, _, _ := exec("--help")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

func TestNoVerboseShorthandForVersion(t *testing.T) {
	// -v must not be bound to --version: -v conventionally means verbose.
	code, _, _ := exec("-v")
	if code != exUsage {
		t.Errorf("`-v` exit = %d, want %d (exUsage) — -v must not be a version alias", code, exUsage)
	}
	// --version still works.
	if code, out, _ := exec("--version"); code != 0 || !strings.Contains(out, "agentry") {
		t.Errorf("--version: exit=%d out=%q, want 0 and contains \"agentry\"", code, out)
	}
}

func TestRootHelpGroupsRenderFlagsAndShowsExamples(t *testing.T) {
	_, out, _ := exec("--help")
	for _, want := range []string{
		"agentry test — ",                   // version leads the help header (exec builds with version "test")
		"Render flags for single sessions:", // render group has its own scoped heading...
		"--level",                           // ...containing the render flags
		"Examples:",                         // examples are present
		"agentry list",                      // a concrete list example line
		"agentry view --tools",              // a concrete view example line
	} {
		if !strings.Contains(out, want) {
			t.Errorf("root help missing %q\n--- help ---\n%s", want, out)
		}
	}
	// --no-color is global, not a render flag: it must appear after the render
	// group, under the plain Flags heading, not inside "Render flags".
	renderIdx := strings.Index(out, "Render flags")
	flagsIdx := strings.Index(out, "\nFlags:")
	noColorIdx := strings.Index(out, "--no-color")
	if !(renderIdx < flagsIdx && flagsIdx < noColorIdx) {
		t.Errorf("expected order: Render flags < Flags: < --no-color; got %d, %d, %d", renderIdx, flagsIdx, noColorIdx)
	}
}

func TestListHelpOmitsRenderGroup(t *testing.T) {
	_, out, _ := exec("list", "--help")
	if strings.Contains(out, "Render flags") {
		t.Errorf("list help should not show the render-flags group:\n%s", out)
	}
	if !strings.Contains(out, "--limit") {
		t.Errorf("list help missing its own flags:\n%s", out)
	}
}

// resolveChannels parses render flags off a throwaway command and returns the
// Channels they resolve to — the level preset with any per-channel overrides
// applied.
func resolveChannels(t *testing.T, args ...string) render.Channels {
	t.Helper()
	cmd := &cobra.Command{RunE: func(*cobra.Command, []string) error { return nil }}
	addRenderFlags(cmd)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	ch, err := channelsFromFlags(cmd)
	if err != nil {
		t.Fatalf("channelsFromFlags %v: %v", args, err)
	}
	return ch
}

// TestLevelChannels pins the level→channel ladder (PRODUCT.md §Verbosity):
// breadth before depth — detailed adds tool *activation* and subagent
// expansion, full alone adds tool-result bodies; metrics rides from standard up.
// It also checks per-channel overrides add and subtract on top of a level,
// including the hyphenated --tool-results flag and its --no- form.
func TestLevelChannels(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want render.Channels
	}{
		{"default is minimal", nil, render.Channels{}},
		{"minimal", []string{"--level", "minimal"}, render.Channels{}},
		{"standard adds thinking+metrics", []string{"--level", "standard"},
			render.Channels{Thinking: true, Metrics: true}},
		{"detailed adds tools+subagents, no results", []string{"--level", "detailed"},
			render.Channels{Thinking: true, Tools: true, Subagents: true, Metrics: true}},
		{"full adds tool-results", []string{"--level", "full"},
			render.Channels{Thinking: true, Tools: true, ToolResults: true, Subagents: true, Metrics: true}},
		{"override subtracts thinking", []string{"--level", "detailed", "--no-thinking"},
			render.Channels{Tools: true, Subagents: true, Metrics: true}},
		{"override adds metrics to minimal", []string{"--level", "minimal", "--metrics"},
			render.Channels{Metrics: true}},
		{"override adds tool-results to detailed", []string{"--level", "detailed", "--tool-results"},
			render.Channels{Thinking: true, Tools: true, ToolResults: true, Subagents: true, Metrics: true}},
		{"override subtracts tool-results from full", []string{"--level", "full", "--no-tool-results"},
			render.Channels{Thinking: true, Tools: true, Subagents: true, Metrics: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveChannels(t, c.args...); got != c.want {
				t.Errorf("channels = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestIsRenderFlag(t *testing.T) {
	render := []string{"level", "thinking", "no-thinking", "tools", "tool-results", "no-tool-results", "no-metrics", "subagents"}
	for _, n := range render {
		if !isRenderFlag(n) {
			t.Errorf("isRenderFlag(%q) = false, want true", n)
		}
	}
	notRender := []string{"no-color", "version", "help", "limit", "since", "include", "color"}
	for _, n := range notRender {
		if isRenderFlag(n) {
			t.Errorf("isRenderFlag(%q) = true, want false", n)
		}
	}
}

// TestFlagOperandOrdering is the regression guard for the reported ordering bug:
// flags must parse whether they precede or follow the session-id operand. Both
// orders should reach session resolution (and fail there with exNoInput in a
// project-less temp dir), never bottom out as a usage error from a parser that
// stopped at the first operand.
func TestFlagOperandOrdering(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := [][]string{
		{"deadbeef", "--level", "full"}, // flag after operand (the old trap)
		{"--level", "full", "deadbeef"}, // flag before operand
		{"view", "--level", "full", "deadbeef"},
	}
	for _, args := range cases {
		code, _, _ := exec(args...)
		if code != exNoInput {
			t.Errorf("args %v: exit = %d, want %d (exNoInput) — flags must parse on either side of the operand", args, code, exNoInput)
		}
	}
}

// TestBareCommandResolves confirms the zero-argument path reaches session
// resolution rather than erroring on argument handling.
func TestBareCommandResolves(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, _ := exec()
	if code != exNoInput {
		t.Errorf("bare command: exit = %d, want %d (exNoInput in a project-less dir)", code, exNoInput)
	}
}
