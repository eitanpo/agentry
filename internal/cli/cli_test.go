package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/locate"
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
		{"tols", includeNames, "tools"},      // mistyped tools channel
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
		{"mistyped level value", []string{"view", "--level", "detaild"}, `did you mean "detailed"`},
		{"mistyped used flag", []string{"list", "--user-tool", "x"}, "did you mean --used-tool"},
		{"mistyped format value", []string{"list", "--format", "jsn"}, `did you mean "json"`},
		{"mistyped format value on render", []string{"--format", "jsn"}, `did you mean "json"`},
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
		"agentry view --level full",         // a concrete view (render) example line
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

// TestBareCommandResolves confirms the zero-argument path reaches the project
// listing rather than erroring on argument handling — a project-less dir bottoms
// out at exNoInput (no sessions to list), not a usage error.
func TestBareCommandResolves(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, _ := exec()
	if code != exNoInput {
		t.Errorf("bare command: exit = %d, want %d (exNoInput in a project-less dir)", code, exNoInput)
	}
}

// sessionlessProject points locate at an empty temp projects root for a fresh
// working directory. With makeDir it also creates the project folder but leaves
// it empty, which is the "project exists, holds no sessions" case; without it,
// the directory maps to no project at all. The two are distinct code paths in
// locate but must look identical to a caller reading stdout.
func sessionlessProject(t *testing.T, makeDir bool) {
	t.Helper()
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	orig := locate.ProjectsRoot
	locate.ProjectsRoot = root
	t.Cleanup(func() { locate.ProjectsRoot = orig })
	if makeDir {
		name := locate.ProjectDirName(cwd)
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestListJSONAlwaysEmitsArray pins the output contract: under --format json,
// stdout is a well-formed array even on the two failures that yield no sessions,
// so a caller sweeping directories can pipe into jq without a guard. The exit
// code must stay non-zero — the empty array is not a claim of success — and the
// text path must keep printing nothing, so the change stays scoped to json.
func TestListJSONAlwaysEmitsArray(t *testing.T) {
	cases := []struct {
		name    string
		makeDir bool
	}{
		{"no project for the directory", false},
		{"project exists but holds no sessions", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sessionlessProject(t, tt.makeDir)
			var code int
			out := captureStdout(t, func() { code, _, _ = exec("list", "--format", "json") })
			if code != exNoInput {
				t.Errorf("exit = %d, want %d (exNoInput) — [] on stdout must not turn the failure into a success", code, exNoInput)
			}
			var got []any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not valid JSON (%v); got %q — this is the guard every sweeping script would need", err, out)
			}
			if len(got) != 0 {
				t.Errorf("stdout = %q, want an empty array", out)
			}

			sessionlessProject(t, tt.makeDir)
			textOut := captureStdout(t, func() { exec("list") })
			if textOut != "" {
				t.Errorf("text stdout = %q, want empty — only --format json owes a parseable shape", textOut)
			}
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. The render/list paths write to os.Stdout directly (not the cobra
// command's out buffer that exec() captures), so a behavioral output assertion
// has to intercept the real stream.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

// fixtureProject points locate at a temp projects root and drops the sample
// session into the project folder for a fresh working directory, returning the
// session id (the file stem). Encoding is derived from the resolved cwd the code
// will actually see (os.Getwd after Chdir), so it matches ProjectDir's mapping
// even where TempDir hands back a symlinked path (macOS /var → /private/var).
func fixtureProject(t *testing.T) (id string) {
	t.Helper()
	id = "ba6b3ded-475b-4c3a-96fe-99698a557d14"
	srcAbs, err := filepath.Abs("../parse/testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(srcAbs)
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

	name := locate.ProjectDirName(cwd)
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// crossProjectFixture builds three projects under one temp root — a repo, a
// worktree nested inside it, and an unrelated project elsewhere — each holding
// one session whose entries record its cwd. It returns the repo's path. The
// working directory is left somewhere with no project of its own, so any session
// a listing finds came from a scope flag rather than from the cwd.
func crossProjectFixture(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	root := t.TempDir()
	orig := locate.ProjectsRoot
	locate.ProjectsRoot = root
	t.Cleanup(func() { locate.ProjectsRoot = orig })

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	for _, p := range []struct{ cwd, id string }{
		{repo, "11111111-1111-1111-1111-111111111111"},
		{filepath.Join(repo, ".claude", "worktrees", "feature"), "22222222-2222-2222-2222-222222222222"},
		{filepath.Join(base, "unrelated"), "33333333-3333-3333-3333-333333333333"},
	} {
		dir := filepath.Join(root, locate.ProjectDirName(p.cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","cwd":"` + p.cwd + `","timestamp":"2026-06-03T14:00:00Z","uuid":"u-` + p.id +
			`","message":{"role":"user","content":"hello"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, p.id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// TestScopeFlags pins the two ways a listing reaches past the current directory,
// and the one way it refuses to.
func TestScopeFlags(t *testing.T) {
	t.Run("--project sweeps the repo and its nested worktree", func(t *testing.T) {
		// The worktree is a separate project because Claude Code slugs its own
		// path; naming the repo has to reach it, or auditing a repo silently
		// omits every session run in a worktree of it.
		repo := crossProjectFixture(t)
		out := captureStdout(t, func() { exec("list", "--project", repo, "--limit", "0", "--format", "json") })
		var got []map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not valid JSON (%v); got %q", err, out)
		}
		if len(got) != 2 {
			t.Fatalf("got %d sessions, want 2 (the repo and its worktree): %s", len(got), out)
		}
		if !strings.Contains(out, "worktrees/feature") {
			t.Errorf("the nested worktree's session is missing: %s", out)
		}
		if strings.Contains(out, "unrelated") {
			t.Errorf("a project outside the named path was swept in: %s", out)
		}
	})

	t.Run("--all-projects spans every project", func(t *testing.T) {
		crossProjectFixture(t)
		out := captureStdout(t, func() { exec("list", "--all-projects", "--limit", "0", "--format", "json") })
		var got []map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not valid JSON (%v); got %q", err, out)
		}
		if len(got) != 3 {
			t.Errorf("got %d sessions, want 3: %s", len(got), out)
		}
	})

	t.Run("each session carries its own cwd", func(t *testing.T) {
		// This field is the whole reason a cross-project listing is readable:
		// without it every row names a session and not where it ran.
		crossProjectFixture(t)
		out := captureStdout(t, func() { exec("list", "--all-projects", "--limit", "0", "--format", "json") })
		var got []struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		for _, s := range got {
			if s.Cwd == "" {
				t.Errorf("a session reported no cwd: %s", out)
			}
		}
	})

	t.Run("the two scope flags are mutually exclusive", func(t *testing.T) {
		// Silently preferring one would make the other look broken rather than
		// rejected, and the caller would never learn which scope it got.
		repo := crossProjectFixture(t)
		code, _, errOut := exec("list", "--all-projects", "--project", repo)
		if code != exUsage {
			t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
		}
		if !strings.Contains(errOut, "mutually exclusive") {
			t.Errorf("error should say the flags conflict, got %q", errOut)
		}
	})

	t.Run("--project naming a path with no project is exNoInput", func(t *testing.T) {
		crossProjectFixture(t)
		code, _, _ := exec("list", "--project", t.TempDir())
		if code != exNoInput {
			t.Errorf("exit = %d, want %d (exNoInput)", code, exNoInput)
		}
	})
}

// entrypointFixture builds one project holding a session per named entrypoint,
// ids being "s<index>". An empty string writes a session with no entrypoint field.
func entrypointFixture(t *testing.T, eps ...string) {
	t.Helper()
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
	for i, ep := range eps {
		id := fmt.Sprintf("0000000%d-0000-0000-0000-000000000000", i)
		field := ""
		if ep != "" {
			field = `"entrypoint":"` + ep + `",`
		}
		body := `{"type":"user",` + field + `"cwd":"` + cwd + `","timestamp":"2026-06-03T14:0` +
			strconv.Itoa(i) + `:00Z","uuid":"u` + strconv.Itoa(i) +
			`","message":{"role":"user","content":"hello"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFromFlag pins the entrypoint selector and the default it overrides.
func TestFromFlag(t *testing.T) {
	count := func(t *testing.T, args ...string) int {
		t.Helper()
		out := captureStdout(t, func() { exec(append([]string{"list", "--limit", "0", "--format", "json"}, args...)...) })
		var got []map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not valid JSON (%v); got %q", err, out)
		}
		return len(got)
	}

	t.Run("headless sessions are hidden by default", func(t *testing.T) {
		// The one exclusion the caller did not ask for. On a machine using hooks
		// these outnumber typed sessions, so listing them buries real work.
		entrypointFixture(t, "cli", "claude-desktop", "sdk-cli")
		if n := count(t); n != 2 {
			t.Errorf("default listing had %d sessions, want 2 (headless hidden)", n)
		}
	})

	t.Run("--from all restores them", func(t *testing.T) {
		entrypointFixture(t, "cli", "claude-desktop", "sdk-cli")
		if n := count(t, "--from", "all"); n != 3 {
			t.Errorf("--from all had %d sessions, want 3", n)
		}
	})

	t.Run("--from sdk selects only headless", func(t *testing.T) {
		entrypointFixture(t, "cli", "claude-desktop", "sdk-cli")
		if n := count(t, "--from", "sdk"); n != 1 {
			t.Errorf("--from sdk had %d sessions, want 1", n)
		}
	})

	t.Run("an unknown value is a usage error with a suggestion", func(t *testing.T) {
		entrypointFixture(t, "cli")
		code, _, errOut := exec("list", "--from", "ap")
		if code != exUsage {
			t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
		}
		if !strings.Contains(errOut, `"app"`) {
			t.Errorf("error should suggest \"app\", got %q", errOut)
		}
	})

	t.Run("a default-emptied listing says so and still exits zero", func(t *testing.T) {
		// Without the note this is indistinguishable from a project holding
		// nothing — a silent default is the failure the note exists to prevent.
		entrypointFixture(t, "sdk-cli", "sdk-cli")
		code, out, errOut := exec("list")
		if code != 0 {
			t.Errorf("exit = %d, want 0 — hiding by default is a filter, not a failure", code)
		}
		if out != "" {
			t.Errorf("stdout should be empty, got %q", out)
		}
		if !strings.Contains(errOut, "hidden") || !strings.Contains(errOut, "--from all") {
			t.Errorf("stderr should name the hidden count and the flag, got %q", errOut)
		}
	})

	t.Run("no note when the listing is empty for another reason", func(t *testing.T) {
		// A time filter matching nothing is the caller's own doing; blaming the
		// entrypoint default there would send them after the wrong flag.
		entrypointFixture(t, "cli")
		_, _, errOut := exec("list", "--until", "2020-01-01")
		if strings.Contains(errOut, "hidden") {
			t.Errorf("stderr should not mention hiding, got %q", errOut)
		}
	})
}

// TestViewSkipsHeadless pins `view`'s no-id resolution. On a machine using hooks
// the newest session is usually a few-second headless run, so "show me my last
// session" would otherwise render a hook.
func TestViewSkipsHeadless(t *testing.T) {
	// entrypointFixture writes ids s0..sN with ascending timestamps; the render
	// header names the model, so the fixture varies it to identify which session
	// was chosen without depending on id formatting.
	t.Run("picks the newest interactive session", func(t *testing.T) {
		entrypointFixture(t, "cli", "sdk-cli")
		out := captureStdout(t, func() { exec("view", "--format", "json") })
		var got struct {
			Meta struct {
				ID         string `json:"id"`
				Entrypoint string `json:"entrypoint"`
			} `json:"meta"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not valid JSON (%v); got %q", err, out)
		}
		if got.Meta.Entrypoint != "cli" {
			t.Errorf("view resolved to a %q session, want the interactive one", got.Meta.Entrypoint)
		}
	})

	t.Run("a named id is rendered whatever its kind", func(t *testing.T) {
		// An id is an explicit request. Second-guessing it would leave headless
		// sessions unreachable, since the listing hides them too.
		entrypointFixture(t, "cli", "sdk-cli")
		out := captureStdout(t, func() {
			exec("view", "00000001-0000-0000-0000-000000000000", "--format", "json")
		})
		if !strings.Contains(out, `"entrypoint": "sdk-cli"`) {
			t.Errorf("a named headless id must still render: %q", out)
		}
	})

	t.Run("all-headless project renders anyway and says so", func(t *testing.T) {
		// Refusing would be wrong: sessions plainly exist, and unlike a listing
		// there is no empty result to return.
		entrypointFixture(t, "sdk-cli", "sdk-cli")
		// The render path writes to os.Stdout, not the command's out stream, so
		// the payload comes from captureStdout while exec supplies code and stderr.
		var code int
		var errOut string
		out := captureStdout(t, func() { code, _, errOut = exec("view", "--format", "json") })
		if code != 0 {
			t.Errorf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, `"entrypoint": "sdk-cli"`) {
			t.Errorf("want the most recent headless session rendered, got %q", out)
		}
		if !strings.Contains(errOut, "headless") {
			t.Errorf("stderr should explain the fallback, got %q", errOut)
		}
	})
}

// TestMetaCarriesEntrypoint pins that the render path knows what the listing
// knows. The two disagreeing about the same session is the defect this closes.
func TestMetaCarriesEntrypoint(t *testing.T) {
	entrypointFixture(t, "claude-desktop")
	out := captureStdout(t, func() {
		exec("view", "00000000-0000-0000-0000-000000000000", "--format", "json")
	})
	if !strings.Contains(out, `"entrypoint": "claude-desktop"`) {
		t.Errorf("meta should carry the entrypoint: %q", out)
	}
}

// TestCompletionSkipsHeadless pins that tabbing a UUID offers the ids a listing
// would show. Completion has no room to explain why a hook run is in the menu.
func TestCompletionSkipsHeadless(t *testing.T) {
	entrypointFixture(t, "cli", "sdk-cli")
	got, _ := completeSessionIDs(nil, nil, "")
	if len(got) != 1 {
		t.Fatalf("completion offered %d ids, want 1 (headless skipped): %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "00000000-") {
		t.Errorf("completion offered %q, want the interactive session", got[0])
	}
}

// TestBareCommandLists pins the front-door behavior: bare `agentry` produces the
// exact same output as `agentry list` (Option A — the bare command IS the
// listing), and a distinct output from rendering a session by id. If bare
// agentry regressed to rendering the most recent session, the first assertion
// would fail (list table vs rendered turns).
func TestBareCommandLists(t *testing.T) {
	id := fixtureProject(t)

	bare := captureStdout(t, func() { exec() })
	listed := captureStdout(t, func() { exec("list") })
	rendered := captureStdout(t, func() { exec(id) })

	if bare != listed {
		t.Errorf("bare `agentry` output must equal `agentry list`\n--- bare ---\n%s\n--- list ---\n%s", bare, listed)
	}
	if !strings.Contains(bare, id) {
		t.Errorf("bare listing should name the session id %q\n%s", id, bare)
	}
	if bare == rendered {
		t.Errorf("bare `agentry` (list) must differ from rendering the session by id")
	}
}

// TestBareListFlagsApply pins Option A's second half: the list selectors work on
// the bare command, not only on `agentry list`. --limit 0 vs a filtered --since
// take different paths, but the simplest observable is that a list flag parses on
// the root at all — a bad --since value is a usage error there, just as on `list`.
func TestBareListFlagsApply(t *testing.T) {
	fixtureProject(t)

	// A list flag is accepted on the bare command: `agentry --since today`
	// lists (exit 0) rather than erroring as an unknown flag. Captured so the
	// list output does not leak into the test log.
	var code int
	captureStdout(t, func() { code, _, _ = exec("--since", "today") })
	if code != 0 {
		t.Errorf("`agentry --since today`: exit = %d, want 0", code)
	}
	// And it flows through the same parser: a bogus WHEN is a usage error on the
	// bare command exactly as on `list`.
	if code, _, _ := exec("--since", "notaday"); code != exUsage {
		t.Errorf("`agentry --since notaday`: exit = %d, want %d (exUsage)", code, exUsage)
	}
}

// TestCompleteFlagValues pins that the enum flags complete to their allowed
// values (not filenames) via Cobra's __complete callback, on both the render
// path and list. The trailing ":4" is ShellCompDirectiveNoFileComp.
func TestCompleteFlagValues(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"__complete", "--format", ""}, []string{"json", "text"}},
		{[]string{"__complete", "--level", ""}, []string{"minimal", "standard", "detailed", "full"}},
		{[]string{"__complete", "list", "--format", ""}, []string{"json", "text"}},
	}
	for _, c := range cases {
		_, out, _ := exec(c.args...)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: output missing %q\n%s", c.args, w, out)
			}
		}
		if !strings.Contains(out, ":4") {
			t.Errorf("%v: missing NoFileComp directive (:4)\n%s", c.args, out)
		}
	}
}

// TestCompleteSessionIDsNoProject confirms the session-id completer degrades to
// "no suggestions" (never a crash or a file-completion fallback) when the cwd
// maps to no project.
func TestCompleteSessionIDsNoProject(t *testing.T) {
	t.Chdir(t.TempDir())
	got, dir := completeSessionIDs(nil, nil, "")
	if len(got) != 0 {
		t.Errorf("want no suggestions in a project-less dir, got %v", got)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %d, want NoFileComp", dir)
	}
}

func TestCompTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain title", "plain title"},
		{"has\ttab\nand newline", "has tab and newline"},
		{"  padded  ", "padded"},
		{"", "(untitled)"},
		{strings.Repeat("x", 60), strings.Repeat("x", 47) + "..."},
	}
	for _, c := range cases {
		if got := compTitle(c.in); got != c.want {
			t.Errorf("compTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
