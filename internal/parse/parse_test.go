package parse

import (
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/price"
)

func TestSummarize(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sample" {
		t.Errorf("ID = %q, want sample", s.ID)
	}
	if s.NumTurns != 3 {
		t.Errorf("NumTurns = %d, want 3", s.NumTurns)
	}
	if s.Title != "first prompt" {
		t.Errorf("Title = %q, want %q", s.Title, "first prompt")
	}
	// The shell command the caller ran with "!" is a prompt: they typed it, and a
	// reply to it would otherwise be charged to the prompt above.
	wantPrompts := []string{"first prompt", "! echo hi", "second prompt"}
	if len(s.Prompts) != len(wantPrompts) {
		t.Fatalf("Prompts = %v, want %v", s.Prompts, wantPrompts)
	}
	for i, w := range wantPrompts {
		if s.Prompts[i] != w {
			t.Errorf("Prompts[%d] = %q, want %q", i, s.Prompts[i], w)
		}
	}
	wantStart := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 5, 27, 10, 1, 3, 0, time.UTC)
	if !s.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", s.Start, wantStart)
	}
	if !s.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", s.End, wantEnd)
	}
	// sample.jsonl has one Bash (ls -la) and one Read.
	assertToolStats(t, s.Tools, []model.ToolStat{
		{Tool: "Bash", Identity: "ls", Count: 1},
		{Tool: "Read", Identity: "", Count: 1},
	})
}

func TestSummarizeToolStats(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// git ×2 (status + push), exa ×1, jq ×1 (after leading VAR= assignments),
	// Skill expert ×1, Agent researcher ×2, Edit ×1. Order is first-seen.
	// Edit carries its target path: a summary saying a session edited something
	// and never what cannot tell one kind of work from another.
	assertToolStats(t, s.Tools, []model.ToolStat{
		{Tool: "Bash", Identity: "git", Count: 2},
		{Tool: "Bash", Identity: "exa", Count: 1},
		{Tool: "Skill", Identity: "expert", Count: 1},
		{Tool: "Agent", Identity: "researcher", Count: 2},
		{Tool: "Edit", Identity: "/a/b.go", Count: 1},
		{Tool: "Bash", Identity: "jq", Count: 1},
	})
	// Commands are the distinct full Bash commands, first-seen order, for
	// --used-command / --used substring matching.
	wantCmds := []string{
		"git status",
		"/Users/x/.claude/skills/exa/scripts/exa --contents -n 5 query",
		"git push origin main",
		"FOO=1 BAR=2 jq . file.json",
	}
	if len(s.Commands) != len(wantCmds) {
		t.Fatalf("Commands = %q, want %q", s.Commands, wantCmds)
	}
	for i, w := range wantCmds {
		if s.Commands[i] != w {
			t.Errorf("Commands[%d] = %q, want %q", i, s.Commands[i], w)
		}
	}
}

// TestSummarizeDenials pins the outcome a summary could not report: which calls
// were refused and by what. A denied call errors like any other, so without the
// kind it is indistinguishable from one that ran and failed — and the fix for
// each is different.
func TestSummarizeDenials(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "outcomes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Two Bash/rm denials collapse into one entry; the user-rejected git call is
	// a separate kind and stays separate. First-seen order.
	want := []model.DenialStat{
		{Kind: "permission-rule", Tool: "Bash", Identity: "rm", Count: 2},
		{Kind: "user-rejected", Tool: "Bash", Identity: "git", Count: 1},
	}
	if len(s.Denials) != len(want) {
		t.Fatalf("Denials = %+v, want %+v", s.Denials, want)
	}
	for i := range want {
		if s.Denials[i] != want[i] {
			t.Errorf("Denials[%d] = %+v, want %+v", i, s.Denials[i], want[i])
		}
	}
	// A call that ran is in Tools and in no denial entry — the two are different
	// questions about the same session, not one tally.
	assertToolStats(t, s.Tools, []model.ToolStat{
		{Tool: "Bash", Identity: "rm", Count: 2},
		{Tool: "Edit", Identity: "/repo/internal/list/list.go", Count: 1},
		{Tool: "Write", Identity: "/repo/docs/notes.md", Count: 1},
		{Tool: "Bash", Identity: "git", Count: 1},
	})
}

// TestSummarizeFiles pins the session-level record of what changed. The log
// mixes path forms — relative to the working directory inside it, absolute
// outside — so a reader grouping by path gets two spellings of one file unless
// they are resolved first.
func TestSummarizeFiles(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "outcomes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// list.go appears twice in the log (a delta, then the snapshot) and once
	// here; the snapshot's relative paths are resolved against cwd, and the
	// absolute one is left alone. Order is first-seen across entries — the delta
	// precedes the snapshot — and within the one snapshot, whose backups are a
	// map with no order of its own, sorted so a session reads the same every run.
	want := []string{
		"/repo/internal/list/list.go",
		"/elsewhere/shared.md",
		"/repo/docs/notes.md",
	}
	if len(s.Files) != len(want) {
		t.Fatalf("Files = %q, want %q", s.Files, want)
	}
	for i := range want {
		if s.Files[i] != want[i] {
			t.Errorf("Files[%d] = %q, want %q", i, s.Files[i], want[i])
		}
	}
}

// TestSummarizeFilesWithoutHistory pins the absence case: a session whose log
// carries no file-history entries reports no files. Claiming it changed nothing
// would be a different, unsupported statement.
func TestSummarizeFilesWithoutHistory(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 0 {
		t.Errorf("Files = %q, want none", s.Files)
	}
	if len(s.Denials) != 0 {
		t.Errorf("Denials = %+v, want none", s.Denials)
	}
}

func TestBashProgram(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"ls -la", "ls"},
		{"git push origin main", "git"},
		{"/Users/x/.claude/skills/exa/scripts/exa --contents q", "exa"},
		{"FOO=1 BAR=2 jq . f.json", "jq"},
		{"   ", ""},
		{"", ""},
		{"=notassign cmd", "=notassign"}, // leading '=' is not a VAR= assignment
	}
	for _, tt := range tests {
		if got := bashProgram(tt.cmd); got != tt.want {
			t.Errorf("bashProgram(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func assertToolStats(t *testing.T, got, want []model.ToolStat) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ToolStats = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ToolStats[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSummarizePrefersAITitle(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "ai-title.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The latest ai-title wins over the first prompt and over an earlier ai-title.
	if s.Title != "Refactor the widget pipeline and add tests" {
		t.Errorf("Title = %q, want the latest ai-title", s.Title)
	}
}

func TestSummarizePrefersCustomTitle(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "custom-title.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// A manual rename (custom-title) wins over the ai-title, which Claude Code
	// freezes at its stale pre-rename value once a custom title is set.
	if s.Title != "widgets" {
		t.Errorf("Title = %q, want the custom-title", s.Title)
	}
}

// A session named with --name or /rename carries an agent-name and, when that is
// the only naming that happened, no custom-title. Reading only custom-title falls
// through to the ai-title and shows a name the user never chose.
func TestSummarizePrefersAgentNameOverAITitle(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "agent-name.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "cloudsmith" {
		t.Errorf("Title = %q, want the agent-name", s.Title)
	}
}

// custom-title and agent-name are both names the user chose, so neither wins by
// kind — the later entry wins. Both orderings are asserted because a ladder that
// simply ranks one type above the other passes exactly one of them.
func TestSummarizeManualTitleLastWins(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{"agent-name then custom-title", "manual-title-order.jsonl", "renamed later"},
		{"custom-title then agent-name", "manual-title-order-reversed.jsonl", "named later"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Summarize(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if s.Title != tt.want {
				t.Errorf("Title = %q, want %q — the last manual title in the file", s.Title, tt.want)
			}
		})
	}
}

func TestSummarizeSkipsLeadingClear(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "clear-start.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// /clear is the first turn but is skipped: the title is the next prompt.
	if s.Title != "actually fix the parser" {
		t.Errorf("Title = %q, want %q", s.Title, "actually fix the parser")
	}
	// The /clear turn still counts toward the turn total.
	if s.NumTurns != 2 {
		t.Errorf("NumTurns = %d, want 2", s.NumTurns)
	}
	// /clear is omitted from the prompt list, leaving only the real prompt.
	if len(s.Prompts) != 1 || s.Prompts[0] != "actually fix the parser" {
		t.Errorf("Prompts = %v, want [actually fix the parser]", s.Prompts)
	}
}

func TestSummarizeRootUUID(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "rooted.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// RootUUID is the first entry's uuid — the conversation root that a fork
	// copies verbatim, so it keys the fork family.
	if s.RootUUID != "root-aaa" {
		t.Errorf("RootUUID = %q, want %q", s.RootUUID, "root-aaa")
	}
	// Born is read from the file (birthtime on macOS, else mtime); it must be set
	// so a fork family can be ordered. The testdata file exists, so it is non-zero.
	if s.Born.IsZero() {
		t.Error("Born is zero, want the file's creation/modification time")
	}
}

// TestSummarizeCwd pins the field a cross-project listing is read by: without
// it every row names a session and not where it ran. The fixture opens with a
// meta entry that carries no cwd, so taking the first entry's value
// unconditionally would report none.
func TestSummarizeCwd(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "cwd.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Cwd != "/Users/me/Projects/me/agentry" {
		t.Errorf("Cwd = %q, want %q", s.Cwd, "/Users/me/Projects/me/agentry")
	}
}

// TestSummarizeEntrypoint pins how a session resumed in another client is
// resolved: last value wins (matching the last-activity time the listing orders
// by), and every distinct value is kept in first-seen order so the JSON does not
// lose what the text table compresses to a "+".
func TestSummarizeEntrypoint(t *testing.T) {
	t.Run("resumed session keeps both, resolves to the last", func(t *testing.T) {
		s, err := Summarize(filepath.Join("testdata", "entrypoint-resumed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if s.Entrypoint != "cli" {
			t.Errorf("Entrypoint = %q, want %q (the last value)", s.Entrypoint, "cli")
		}
		want := []string{"claude-desktop", "cli"}
		if len(s.Entrypoints) != 2 || s.Entrypoints[0] != want[0] || s.Entrypoints[1] != want[1] {
			t.Errorf("Entrypoints = %v, want %v (first-seen order)", s.Entrypoints, want)
		}
	})

	t.Run("single-entrypoint session lists none", func(t *testing.T) {
		// Entrypoints exists to record divergence; repeating a single value would
		// put a redundant array on every session in the JSON. Asserting Entrypoint
		// too is what keeps this honest — a fixture carrying no entrypoint at all
		// would satisfy the nil check for the wrong reason.
		s, err := Summarize(filepath.Join("testdata", "entrypoint-single.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if s.Entrypoint != "cli" {
			t.Fatalf("Entrypoint = %q, want %q — the fixture must actually carry one", s.Entrypoint, "cli")
		}
		if s.Entrypoints != nil {
			t.Errorf("Entrypoints = %v, want nil for a session with one value", s.Entrypoints)
		}
	})

	t.Run("a session with no entrypoint at all is not an error", func(t *testing.T) {
		// Logs written before Claude Code added the field carry none, and the
		// format doc requires older sessions keep rendering.
		s, err := Summarize(filepath.Join("testdata", "cwd.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if s.Entrypoint != "" || s.Entrypoints != nil {
			t.Errorf("Entrypoint = %q, Entrypoints = %v; want both empty", s.Entrypoint, s.Entrypoints)
		}
	})
}

func TestIsClearCmd(t *testing.T) {
	clear := []string{"//clear", "/clear", "  //clear  ", "clear",
		"/clear improve the parser", "//clear do the thing"}
	notClear := []string{"//clear-cache", "/research-lookup x", "clear the table", ""}
	for _, p := range clear {
		if !isClearCmd(p) {
			t.Errorf("isClearCmd(%q) = false, want true", p)
		}
	}
	for _, p := range notClear {
		if isClearCmd(p) {
			t.Errorf("isClearCmd(%q) = true, want false", p)
		}
	}
}

func TestLoad(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	if sess.Meta.Model != "claude-opus-4-7" {
		t.Errorf("model = %q, want claude-opus-4-7", sess.Meta.Model)
	}
	// Usage sums across both assistant entries.
	wantUsage := model.Usage{Input: 14, Output: 28, CacheRead: 5, CacheCreate: 3}
	if sess.Meta.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", sess.Meta.Usage, wantUsage)
	}
	if sess.Meta.NumSubagents != 0 {
		t.Errorf("subagents = %d, want 0", sess.Meta.NumSubagents)
	}

	// The injected <task-notification> entry must not start a turn; the typed
	// shell command must.
	if len(sess.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(sess.Turns))
	}

	turn0 := sess.Turns[0]
	if turn0.Prompt != "first prompt" {
		t.Errorf("turn0 prompt = %q, want %q", turn0.Prompt, "first prompt")
	}
	if turn0.ToolCount != 1 || turn0.ErrorCount != 0 {
		t.Errorf("turn0 tools=%d errors=%d, want 1/0", turn0.ToolCount, turn0.ErrorCount)
	}
	kinds := eventKinds(turn0.Events)
	wantKinds := []model.EventKind{model.EventThinking, model.EventText, model.EventTool}
	if !equalKinds(kinds, wantKinds) {
		t.Errorf("turn0 event kinds = %v, want %v", kinds, wantKinds)
	}
	tool := lastTool(turn0.Events)
	if tool == nil {
		t.Fatal("turn0 has no tool event")
	}
	if tool.Name != "Bash" || tool.Args != "ls -la" {
		t.Errorf("tool = %q(%q), want Bash(ls -la)", tool.Name, tool.Args)
	}
	if tool.Result != "file listing output" || tool.IsError {
		t.Errorf("tool result=%q err=%v, want non-error file listing", tool.Result, tool.IsError)
	}

	shell := sess.Turns[1]
	if shell.Prompt != "! echo hi" {
		t.Errorf("shell turn prompt = %q, want %q", shell.Prompt, "! echo hi")
	}
	if texts := eventTexts(shell.Events); len(texts) != 1 || !strings.Contains(texts[0], "hi") {
		t.Errorf("shell turn texts = %q, want one block holding what the command printed", texts)
	}

	turn1 := sess.Turns[2]
	if turn1.Prompt != "second prompt" {
		t.Errorf("turn1 prompt = %q, want %q", turn1.Prompt, "second prompt")
	}
	if turn1.ErrorCount != 1 {
		t.Errorf("turn1 errors = %d, want 1", turn1.ErrorCount)
	}
	errTool := lastTool(turn1.Events)
	if errTool == nil || !errTool.IsError || errTool.Result != "file not found" {
		t.Errorf("turn1 error tool = %+v, want Read error 'file not found'", errTool)
	}
}

// TestLoadStitching pins subagent stitching across both the structured
// toolUseResult.agentId key and the legacy fallbacks, so a regression in either
// path is caught. The session wires four spawning calls to sidecars plus one
// inline skill that must stay a leaf:
//   - Agent  via toolUseResult.agentId (result text has no agentId line)
//   - Agent  via the legacy "agentId:" result line (no toolUseResult)
//   - Skill  forked via toolUseResult.agentId
//   - Skill  forked via legacy skill-name match (toolUseResult is a bare string)
//   - Skill  inline ("Launching skill") with no sidecar → no expansion
func TestLoadStitching(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "stitch.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.NumSubagents != 4 {
		t.Errorf("subagents = %d, want 4", sess.Meta.NumSubagents)
	}
	if len(sess.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(sess.Turns))
	}

	var tools []*model.Tool
	for _, e := range sess.Turns[0].Events {
		if e.Kind == model.EventTool {
			tools = append(tools, e.Tool)
		}
	}
	if len(tools) != 5 {
		t.Fatalf("tool events = %d, want 5", len(tools))
	}

	// firstText returns the first text event of a tool's expansion, or "" if the
	// tool has no subagent attached.
	firstText := func(tool *model.Tool) string {
		for _, e := range tool.Subagent {
			if e.Kind == model.EventText {
				return e.Text
			}
		}
		return ""
	}

	cases := []struct {
		idx      int
		wantText string // "" means: expect no expansion
	}{
		{0, "explorer aaa1 work"}, // Agent, structured agentId
		{1, "explorer aaa2 work"}, // Agent, legacy agentId line
		{2, "alpha work"},         // Skill, structured agentId
		{3, "beta work"},          // Skill, legacy name match
		{4, ""},                   // Skill, inline — leaf, no sidecar
	}
	for _, c := range cases {
		got := firstText(tools[c.idx])
		if got != c.wantText {
			t.Errorf("tool[%d] %s(%s): expansion first text = %q, want %q",
				c.idx, tools[c.idx].Name, tools[c.idx].Args, got, c.wantText)
		}
	}
	if tools[4].Subagent != nil {
		t.Errorf("inline skill tool[4] has %d subagent events, want none", len(tools[4].Subagent))
	}
}

// TestLoadEffort pins the setting that separates two sessions on the same model:
// how hard it was run. Effort moves both cost and quality, and no output named
// it before, so those two sessions were indistinguishable in every view.
func TestLoadEffort(t *testing.T) {
	t.Run("a session that changed effort keeps both values", func(t *testing.T) {
		// Rare but real: a session can change effort mid-run.
		sess, err := Load(filepath.Join("testdata", "effort-changed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		// The resolved value is the last, matching how the entrypoint resolves and
		// what the session's most recent activity actually ran at.
		if sess.Meta.Effort != "high" {
			t.Errorf("Effort = %q, want the last value", sess.Meta.Effort)
		}
		want := []string{"xhigh", "high"}
		if len(sess.Meta.Efforts) != len(want) {
			t.Fatalf("Efforts = %q, want %q", sess.Meta.Efforts, want)
		}
		for i := range want {
			if sess.Meta.Efforts[i] != want[i] {
				t.Errorf("Efforts[%d] = %q, want %q", i, sess.Meta.Efforts[i], want[i])
			}
		}
	})

	t.Run("a session predating the field reports none", func(t *testing.T) {
		// A session predating the field has no effort at all. Reporting a default
		// would state a setting the log does not record.
		sess, err := Load(filepath.Join("testdata", "sample.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if sess.Meta.Effort != "" || sess.Meta.Efforts != nil {
			t.Errorf("Effort = %q / %q, want neither", sess.Meta.Effort, sess.Meta.Efforts)
		}
	})
}

// TestLoadCarriesDelegation pins the structured facts a rendered Agent call used
// to lose. Args flattens an Agent's input to its human description, so before
// this the subagent type, the delegated model and the instruction the subagent
// ran on were unrecoverable from a rendered session — a cost audit asking what a
// subagent ran on had nowhere to read it, and a reader asking what it was told
// had to go back to the raw log.
func TestLoadCarriesDelegation(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "agent-delegation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(sess.Turns))
	}
	var tools []*model.Tool
	for _, e := range sess.Turns[0].Events {
		if e.Kind == model.EventTool {
			tools = append(tools, e.Tool)
		}
	}
	if len(tools) != 4 {
		t.Fatalf("tool events = %d, want 4", len(tools))
	}

	cases := []struct {
		what                     string
		tool                     *model.Tool
		identity, model_, prompt string
	}{
		// The audit case: every fact named, none derivable from args.
		{"an Agent naming type and model", tools[0], "Explore", "haiku", "find every caller"},
		// No model named means the subagent inherited the session's. Defaulting to
		// Meta.Model here would report a choice the caller never made.
		{"an Agent naming no model", tools[1], "researcher", "", "research it"},
		// subagent_type is optional in the log; agentry reports it absent rather
		// than guessing the harness default.
		{"an Agent naming no type", tools[2], "", "sonnet", "do a thing"},
		// Identity is not Agent-only: it is the same label the listing groups by,
		// which is what stops the two paths naming one call two things. The
		// instruction is Agent-only, though: this call's input carries a "prompt"
		// key and the field stays empty, because a prompt passed to something that
		// is not a delegated run answers a different question under the same name.
		{"a Bash call", tools[3], "git", "", ""},
	}
	for _, c := range cases {
		if c.tool.Identity != c.identity {
			t.Errorf("%s: identity = %q, want %q", c.what, c.tool.Identity, c.identity)
		}
		if c.tool.Model != c.model_ {
			t.Errorf("%s: model = %q, want %q", c.what, c.tool.Model, c.model_)
		}
		if c.tool.Prompt != c.prompt {
			t.Errorf("%s: prompt = %q, want %q", c.what, c.tool.Prompt, c.prompt)
		}
	}
	// Args keeps being the human summary — the new fields are additions to it,
	// not a redefinition, so anything reading args still sees what it did.
	if tools[0].Args != "sweep for callers" {
		t.Errorf("args = %q, want the description", tools[0].Args)
	}
}

func TestUserPrompt(t *testing.T) {
	tests := []struct {
		name   string
		entry  entry
		want   string
		wantOK bool
	}{
		{"typed", entry{hasStr: true, contentStr: "hello"}, "hello", true},
		// The wrapper around a typed shell command holds what a person pressed, so
		// it is a prompt; the wrappers around what it printed are the harness's.
		{"typed shell command", entry{hasStr: true, contentStr: "<bash-input>x</bash-input>"}, "! x", true},
		{"shell output injected", entry{hasStr: true, contentStr: "<bash-stdout>out</bash-stdout><bash-stderr></bash-stderr>"}, "", false},
		{"shell wrapper quoted inside a prompt", entry{hasStr: true, contentStr: "why did <bash-input>ls</bash-input> fail?"}, "why did <bash-input>ls</bash-input> fail?", true},
		{"skill injected", entry{hasStr: true, contentStr: "Base directory for this skill: /x"}, "", false},
		{"command", entry{hasStr: true, contentStr: "<command-name>foo</command-name><command-args>bar</command-args>"}, "/foo bar", true},
		{"command name with slash not doubled", entry{hasStr: true, contentStr: "<command-name>/clear</command-name><command-args>fix it</command-args>"}, "/clear fix it", true},
		{"compaction by text, pre-flag logs", entry{hasStr: true, contentStr: "...This session is being continued from a previous conversation..."}, compactSummaryPlaceholder, true},
		// The flag alone must be enough: this body is what an upstream rewording of
		// the summary looks like, and the text match cannot see it.
		{"compaction by flag, reworded body", entry{hasStr: true, isCompactSummary: true, contentStr: "Picking up where the last context left off. Summary follows."}, compactSummaryPlaceholder, true},
		// A summary quoting an injected marker must still read as the boundary
		// rather than being dropped as injected content.
		{"compaction by flag, body quotes an injected marker", entry{hasStr: true, isCompactSummary: true, contentStr: "Earlier the user ran <bash-input>ls</bash-input> and then asked for a fix."}, compactSummaryPlaceholder, true},
		{"empty", entry{hasStr: true, contentStr: "   "}, "", false},
		{"array content not a prompt", entry{hasStr: false}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := userPrompt(tt.entry)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestFormatToolArgs(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "ls"}, "ls"},
		{"Read", map[string]any{"file_path": "/a"}, "/a"},
		{"Grep", map[string]any{"pattern": "x"}, "x"},
		{"Skill", map[string]any{"skill": "s", "args": "a"}, "s a"},
		{"Unknown", map[string]any{"foo": "bar"}, `{"foo":"bar"}`},
		{"Unknown empty", map[string]any{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatToolArgs(tt.name, tt.input); got != tt.want {
				t.Errorf("formatToolArgs(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// eventTexts is what a turn printed as prose, in order, leaving out thinking
// and tool calls.
func eventTexts(events []model.Event) []string {
	var out []string
	for _, e := range events {
		if e.Kind == model.EventText {
			out = append(out, e.Text)
		}
	}
	return out
}

func eventKinds(events []model.Event) []model.EventKind {
	out := make([]model.EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func equalKinds(a, b []model.EventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lastTool(events []model.Event) *model.Tool {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == model.EventTool {
			return events[i].Tool
		}
	}
	return nil
}

// TestModelResolution pins how a session's model is read. It used to be the
// first assistant entry's, which misreports a session that switched models,
// naming one the session had already left.
func TestModelResolution(t *testing.T) {
	t.Run("a session that switched keeps both, resolving to the last", func(t *testing.T) {
		sess, err := Load(filepath.Join("testdata", "model-changed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if sess.Meta.Model != "claude-opus-5" {
			t.Errorf("Model = %q, want the last one the session ran on", sess.Meta.Model)
		}
		want := []string{"claude-sonnet-5", "claude-opus-5"}
		if !equalStrings(sess.Meta.Models, want) {
			t.Errorf("Models = %q, want %q", sess.Meta.Models, want)
		}
	})

	t.Run("<synthetic> is not a model", func(t *testing.T) {
		// Claude Code writes it on messages it composed itself — an API-error
		// notice, a session-limit warning. The fixture ends on one, which is the
		// shape that matters: counting it would end the session on a model that
		// never ran.
		sess, err := Load(filepath.Join("testdata", "model-changed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if sess.Meta.Model == syntheticModel {
			t.Errorf("Model = %q, which names no model the session ran on", sess.Meta.Model)
		}
		for _, m := range sess.Meta.Models {
			if m == syntheticModel {
				t.Errorf("Models = %q, want %q excluded", sess.Meta.Models, syntheticModel)
			}
		}
	})

	t.Run("a session naming no model reports none", func(t *testing.T) {
		// It used to report the word "unknown", which asserted a fact the log does
		// not carry. Effort and entrypoint already say nothing in this case.
		sess, err := Load(filepath.Join("testdata", "rooted.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if sess.Meta.Model != "" || sess.Meta.Models != nil {
			t.Errorf("Model = %q / %q, want neither", sess.Meta.Model, sess.Meta.Models)
		}
	})
}

// TestSummarizeCarriesRun pins that a listing knows what a session ran on. The
// render path named the model and effort while a Summary carried neither, so
// "which sessions ran at xhigh" had no answer short of rendering each one.
func TestSummarizeCarriesRun(t *testing.T) {
	t.Run("model and its trail", func(t *testing.T) {
		s, err := Summarize(filepath.Join("testdata", "model-changed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if s.Model != "claude-opus-5" {
			t.Errorf("Model = %q, want the last one", s.Model)
		}
		want := []string{"claude-sonnet-5", "claude-opus-5"}
		if !equalStrings(s.Models, want) {
			t.Errorf("Models = %q, want %q", s.Models, want)
		}
	})

	t.Run("effort and its trail", func(t *testing.T) {
		s, err := Summarize(filepath.Join("testdata", "effort-changed.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if s.Effort != "high" {
			t.Errorf("Effort = %q, want the last one", s.Effort)
		}
		want := []string{"xhigh", "high"}
		if !equalStrings(s.Efforts, want) {
			t.Errorf("Efforts = %q, want %q", s.Efforts, want)
		}
	})

	t.Run("a summary agrees with the rendered session", func(t *testing.T) {
		// The two paths read one session, so they must not name two different
		// models: that disagreement is the whole reason this field exists.
		path := filepath.Join("testdata", "model-changed.jsonl")
		s, err := Summarize(path)
		if err != nil {
			t.Fatal(err)
		}
		sess, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Model != sess.Meta.Model || !equalStrings(s.Models, sess.Meta.Models) {
			t.Errorf("summary %q/%q vs meta %q/%q", s.Model, s.Models, sess.Meta.Model, sess.Meta.Models)
		}
	})
}

// TestSummarizeUsageIncludesSubagents pins that a listing's token tally covers
// delegated work. Summarize otherwise never opens a sidecar, so the natural
// implementation counts the main thread alone — and undercounts exactly the
// sessions that cost the most, while Meta.Usage reports the full figure.
func TestSummarizeUsageIncludesSubagents(t *testing.T) {
	path := filepath.Join("testdata", "subagent-usage.jsonl")
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	want := model.Usage{Input: 110, Output: 220, CacheRead: 55, CacheCreate: 33}
	if s.Usage != want {
		t.Errorf("Usage = %+v, want %+v (main thread plus the agent-u1 sidecar)", s.Usage, want)
	}

	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Usage != sess.Meta.Usage {
		t.Errorf("summary usage %+v != meta usage %+v; a cost read off a listing must match one read off a render", s.Usage, sess.Meta.Usage)
	}
}

// TestSummarizeOutputs pins how a session's outputs are read. Claude Code
// re-records a pr-link or frame-link entry on later turns, so the natural
// implementation — collect every entry — reports one pull request as many, and
// the fixture's five pr-link entries name three pull requests.
func TestSummarizeOutputs(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "outputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pull requests are deduplicated in first-seen order", func(t *testing.T) {
		want := []model.PR{
			{Repository: "eitanpo/central", Number: 14, URL: "https://github.com/eitanpo/central/pull/14"},
			{Repository: "eitanpo/central", Number: 27, URL: "https://github.com/eitanpo/central/pull/27"},
			{Repository: "acme-private/devex-costs", Number: 3, URL: "https://github.com/acme-private/devex-costs/pull/3"},
		}
		if len(s.PRs) != len(want) {
			t.Fatalf("PRs = %+v, want %d entries", s.PRs, len(want))
		}
		for i := range want {
			if s.PRs[i] != want[i] {
				t.Errorf("PRs[%d] = %+v, want %+v", i, s.PRs[i], want[i])
			}
		}
	})

	t.Run("an artifact republished from a moved file stays one artifact", func(t *testing.T) {
		// The fixture publishes artifact aaa from a scratchpad file, then from a
		// path inside the repository. Keying on the path would report two.
		if len(s.Artifacts) != 2 {
			t.Fatalf("Artifacts = %+v, want 2", s.Artifacts)
		}
		got := s.Artifacts[0]
		if got.URL != "https://claude.ai/code/artifact/aaa" {
			t.Errorf("first artifact URL = %q", got.URL)
		}
		// The later record wins on the path it names...
		if got.Path != "/repo/reports/cost.html" {
			t.Errorf("Path = %q, want the later publish's path", got.Path)
		}
		// ...but says nothing about the title, and an omission is not a deletion.
		if got.Title != "Cost report" {
			t.Errorf("Title = %q, want the earlier record's title to survive", got.Title)
		}
	})

	t.Run("a summary agrees with the rendered session", func(t *testing.T) {
		// The two paths read one session, so they must not name different outputs:
		// a pull request visible in the listing and absent from the render is the
		// disagreement carrying these onto Meta was meant to end.
		sess, err := Load(filepath.Join("testdata", "outputs.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(s.PRs, sess.Meta.PRs) {
			t.Errorf("summary PRs %+v vs meta %+v", s.PRs, sess.Meta.PRs)
		}
		if !slices.Equal(s.Artifacts, sess.Meta.Artifacts) {
			t.Errorf("summary artifacts %+v vs meta %+v", s.Artifacts, sess.Meta.Artifacts)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSummarizeReplies pins the corpus --reply-matches tests: the main thread's
// assistant text, and nothing else. Each exclusion here is a way the filter
// would otherwise answer a question about the reply with something that was not
// one.
func TestSummarizeReplies(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("text blocks are carried in order, one entry per block", func(t *testing.T) {
		// One entry per block rather than one joined string, so a pattern's ^ and $
		// anchor to a single reply.
		want := []string{"here is an answer", "trying to read"}
		if !equalStrings(s.Replies, want) {
			t.Errorf("Replies = %q, want %q", s.Replies, want)
		}
	})

	t.Run("thinking is not a reply", func(t *testing.T) {
		// sample.jsonl's first assistant entry thinks "let me think" before
		// answering. A rule about what a reply said must not be satisfied by a
		// thought the user never saw.
		for _, r := range s.Replies {
			if strings.Contains(r, "let me think") {
				t.Errorf("a thinking block was carried as a reply: %q", r)
			}
		}
	})

	t.Run("a subagent's reply is not the session's", func(t *testing.T) {
		// Sidecars are opened only for the token tally. --reply-matches is a
		// top-level filter like the --used* family, and this is the observable
		// that keeps it one.
		sub, err := Summarize(filepath.Join("testdata", "subagent-usage.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range sub.Replies {
			if strings.Contains(r, "found it") {
				t.Errorf("a sidecar reply was carried: %q", r)
			}
		}
	})
}

// TestUsageCountsEachResponseOnce pins the rule that separates a token tally
// from a line count. Claude Code splits one API response across an assistant
// entry per content block and writes that response's whole usage object on every
// one of them, so the obvious implementation — add up every assistant entry —
// reports a reply three times over. The fixture's first response spans three
// entries carrying identical usage; a regression reads 300 output tokens where
// the response produced 100.
func TestUsageCountsEachResponseOnce(t *testing.T) {
	path := filepath.Join("testdata", "blocked-usage.jsonl")
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	// Main thread: req_A once (10/100/40/4), req_B once (1/7/2/0), and the
	// synthetic entry that names no response, keyed by its own uuid (3/0/0/0).
	// The sidecar's two entries are one response (1000/2000/500/300).
	want := model.Usage{Input: 1014, Output: 2107, CacheRead: 542, CacheCreate: 304}
	if s.Usage != want {
		t.Errorf("Usage = %+v, want %+v", s.Usage, want)
	}

	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.Usage != want {
		t.Errorf("Meta.Usage = %+v, want %+v; the render path must count responses the way a listing does", sess.Meta.Usage, want)
	}
	// The per-turn tally is the surface that orders the summary, so it dedupes
	// too — a turn inflated by its block count would sort above turns that cost
	// more. Subagents are excluded here: this fixture spawns none.
	if len(sess.Turns) != 1 {
		t.Fatalf("Turns = %d, want 1", len(sess.Turns))
	}
	wantTurn := model.Usage{Input: 14, Output: 107, CacheRead: 42, CacheCreate: 4}
	if sess.Turns[0].Usage != wantTurn {
		t.Errorf("turn Usage = %+v, want %+v", sess.Turns[0].Usage, wantTurn)
	}
}

// TestUsageTakesHighestOutputOfAResponse pins the rule that a response's entries
// are not identical: Claude Code writes each one as the reply streams, so output
// grows across the group while the other three counters hold. Counting the first
// entry instead can undercount output substantially, so this is the difference
// between a tally that matches Claude Code's own record and one that falls well
// short of it.
func TestUsageTakesHighestOutputOfAResponse(t *testing.T) {
	path := filepath.Join("testdata", "streaming-usage.jsonl")
	// One response, three entries reading 3, 771, 771. The other counters are the
	// same on every entry, so they are counted once as they always were.
	want := model.Usage{Input: 10, Output: 771, CacheRead: 40, CacheCreate: 4}

	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Usage != want {
		t.Errorf("Usage = %+v, want %+v; the first entry of the group holds only 3 output", s.Usage, want)
	}

	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.Usage != want {
		t.Errorf("Meta.Usage = %+v, want %+v; the render path must pick the same entry a listing does", sess.Meta.Usage, want)
	}
	if len(sess.Turns) != 1 {
		t.Fatalf("Turns = %d, want 1", len(sess.Turns))
	}
	if sess.Turns[0].Usage != want {
		t.Errorf("turn Usage = %+v, want %+v; the summary orders turns by this figure", sess.Turns[0].Usage, want)
	}
}

// TestUsageHighestWinsRegardlessOfOrder covers what a fixture cannot: the partial
// entry arriving last. Real logs happen to write these entries in ascending
// order, so a rule that took the last entry would pass today and undercount
// the first time Claude Code writes them in another order.
func TestUsageHighestWinsRegardlessOfOrder(t *testing.T) {
	descending := []entry{
		{typ: "assistant", requestID: "req_X", uuid: "x1", usage: model.Usage{Input: 5, Output: 900}},
		{typ: "assistant", requestID: "req_X", uuid: "x2", usage: model.Usage{Input: 5, Output: 2}},
	}
	if got := sumUsage(descending); got.Output != 900 {
		t.Errorf("Output = %d, want 900; the highest entry wins whichever way the group is written", got.Output)
	}
	if got := sumUsage(descending); got.Input != 5 {
		t.Errorf("Input = %d, want 5; the winning entry's whole usage object is kept, counted once", got.Input)
	}
}

// TestUsageKeylessEntriesEachCount covers the case the fixtures cannot reach: an
// assistant entry naming neither a response nor itself. Deduplicating on an empty
// key would collapse every such entry into the first, silently undercounting a
// log old enough to carry neither field.
func TestUsageKeylessEntriesEachCount(t *testing.T) {
	entries := []entry{
		{typ: "assistant", usage: model.Usage{Output: 5}},
		{typ: "assistant", usage: model.Usage{Output: 7}},
	}
	if got := sumUsage(entries); got.Output != 12 {
		t.Errorf("Output = %d, want 12; an entry with no identity has nothing to deduplicate against", got.Output)
	}
}

// TestSessionCostReadsLastRecord pins that a session's cost is the last cost-state
// entry rather than a sum of them. Claude Code rewrites the entry as the session
// runs and each one carries the running total, so adding them up multiplies the
// answer — the fixture's two entries sum to 3.25 against a true total of 2.75.
func TestSessionCostReadsLastRecord(t *testing.T) {
	path := filepath.Join("testdata", "cost-state.jsonl")
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.CostUSD == nil {
		t.Fatal("CostUSD = nil, want 2.75")
	}
	if *s.CostUSD != 2.75 {
		t.Errorf("CostUSD = %v, want 2.75 (the last record, not the sum of both)", *s.CostUSD)
	}
	// The line counters ride the same entry, so the last record settles them too:
	// the fixture's first record says 3 added and its second says 9, and a reader
	// that summed the entries would report 12 lines the session never wrote.
	if s.LinesAdded == nil || s.LinesRemoved == nil {
		t.Fatalf("LinesAdded/LinesRemoved = %v/%v, want 9/4", s.LinesAdded, s.LinesRemoved)
	}
	if *s.LinesAdded != 9 || *s.LinesRemoved != 4 {
		t.Errorf("lines = +%d/-%d, want +9/-4 (the last record, not the sum of both)", *s.LinesAdded, *s.LinesRemoved)
	}

	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.CostUSD == nil || *sess.Meta.CostUSD != *s.CostUSD {
		t.Errorf("Meta.CostUSD = %v, want %v; a listing and a render must agree", sess.Meta.CostUSD, *s.CostUSD)
	}
	if sess.Meta.LinesAdded == nil || *sess.Meta.LinesAdded != *s.LinesAdded {
		t.Errorf("Meta.LinesAdded = %v, want %v; a listing and a render must agree", sess.Meta.LinesAdded, *s.LinesAdded)
	}
}

// TestSessionCostAbsentWithoutRecord pins that a log carrying no cost record
// reports no cost rather than zero. A session Claude Code wrote no total for is
// not a session that was free, and a zero would be indistinguishable from a
// measurement once a caller aggregates the field.
func TestSessionCostAbsentWithoutRecord(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil on a log with no cost-state entry", *s.CostUSD)
	}
	// The line counters share the cost's record, so they share its absence. A zero
	// here would read as a session that changed nothing, which this log does not say.
	if s.LinesAdded != nil || s.LinesRemoved != nil {
		t.Errorf("lines = %v/%v, want nil/nil on a log with no cost-state entry", s.LinesAdded, s.LinesRemoved)
	}
}

// TestTurnCompanionIsNotAPrompt pins that harness-attached material filed as a
// user entry does not become a turn. The fixture's companion entry is a skill
// re-invocation notice — plain string content matching none of the injected
// markers — so only Claude Code's own marker keeps it out, and without it the
// session reads as three turns and could be titled by a notice nobody typed.
func TestTurnCompanionIsNotAPrompt(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "turn-companion.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"the real prompt", "a second real prompt"}
	if !slices.Equal(s.Prompts, want) {
		t.Errorf("Prompts = %q, want %q", s.Prompts, want)
	}
	if s.NumTurns != 2 {
		t.Errorf("NumTurns = %d, want 2", s.NumTurns)
	}
	if s.Title != "the real prompt" {
		t.Errorf("Title = %q, want %q", s.Title, "the real prompt")
	}
}

// TestDailyUsageSplitsByDayAndModel pins the grain a cost roll-up buckets by:
// one entry per model per local day, over the main log and its sidecars, with
// the response deduplication applied before the split rather than after.
//
// The expected days are derived from the fixture's own timestamps rather than
// written out, because the boundary is local midnight and the test runs in
// whatever zone the machine is set to. The two instants are two days apart, so
// they stay distinct days in every zone.
func TestDailyUsageSplitsByDayAndModel(t *testing.T) {
	path := filepath.Join("testdata", "daily-usage.jsonl")
	day := func(stamp string) string {
		ts, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatal(err)
		}
		return ts.Local().Format("2006-01-02")
	}
	first, second := day("2026-03-01T12:00:00Z"), day("2026-03-03T12:00:00Z")
	want := []model.DailyUsage{
		// req_A streams 5 output then 50; the highest wins, and the hour share of
		// its cache write survives into the bucket that prices it.
		{Day: first, Model: "claude-opus-5", Usage: model.Usage{
			Input: 10, Output: 50, CacheRead: 100, CacheCreate: 200, CacheCreate1h: 200}},
		{Day: first, Model: "claude-sonnet-5", Usage: model.Usage{Input: 20, Output: 30}},
		// The sidecar's own model, not the session's: a subagent is priced by what
		// answered inside it. The fixture's main log holds no call that spawned this
		// sidecar, which is the shape a nested delegation leaves behind, so the
		// tokens are named as unattributed rather than folded into the main thread's.
		{Day: second, Model: "claude-haiku-4-5-20251001", Agent: unattributedAgent, Usage: model.Usage{
			Input: 100, Output: 200, CacheRead: 50, CacheCreate: 30}},
		{Day: second, Model: "claude-opus-5", Usage: model.Usage{
			Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}},
	}

	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.DailyUsage, want) {
		t.Errorf("DailyUsage = %+v,\n          want %+v", s.DailyUsage, want)
	}
	// The fixture's <synthetic> entry spends nothing and names no model the
	// session ran on, so it must not appear as a bucket of its own.
	for _, d := range s.DailyUsage {
		if d.Model == syntheticModel {
			t.Errorf("DailyUsage carries a %s bucket: %+v", syntheticModel, d)
		}
	}
	var total model.Usage
	for _, d := range s.DailyUsage {
		total.Add(d.Usage)
	}
	if total != s.Usage {
		t.Errorf("the split sums to %+v but Usage is %+v; the two must be one set of responses read two ways", total, s.Usage)
	}
}

// TestSummarizeAllKeepsOrderAndSkipsBadFiles pins what the parallel sweep owes
// its caller: the order it was given, whatever order the workers finish in, and
// a skip rather than a failure for a log that will not parse — one unreadable
// file must not cost the caller the rest.
func TestSummarizeAllKeepsOrderAndSkipsBadFiles(t *testing.T) {
	if got := SummarizeAll(nil); got != nil {
		t.Errorf("SummarizeAll(nil) = %+v, want nil", got)
	}

	// Three real fixtures with a missing file between each pair, so a sweep that
	// dropped a slot or filled one out of order shows up as a wrong id.
	want := []string{"sample", "cost-state", "daily-usage"}
	paths := []string{
		filepath.Join("testdata", "sample.jsonl"),
		filepath.Join("testdata", "no-such-session.jsonl"),
		filepath.Join("testdata", "cost-state.jsonl"),
		filepath.Join("testdata", "also-missing.jsonl"),
		filepath.Join("testdata", "daily-usage.jsonl"),
	}
	got := SummarizeAll(paths)
	if len(got) != len(want) {
		t.Fatalf("SummarizeAll returned %d summaries, want %d: %+v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("summary %d = %q, want %q — the caller's order is what it reads back by", i, got[i].ID, id)
		}
	}

	// The same sessions read one at a time must give the same answer, since the
	// only thing the sweep changes is how many are read at once.
	for i, p := range []string{paths[0], paths[2], paths[4]} {
		one, err := Summarize(p)
		if err != nil {
			t.Fatal(err)
		}
		if one.Usage != got[i].Usage {
			t.Errorf("%s: parallel Usage = %+v, serial = %+v", p, got[i].Usage, one.Usage)
		}
	}
}

// TestDelegatedTokensCarryTheirCallersName pins the agent axis's whole input:
// a subagent labelled by its type, a forked skill labelled by its invoked name
// with the slash kept, and the session's own tokens left unlabelled so the axis
// sums to the session total rather than to its delegated part.
func TestDelegatedTokensCarryTheirCallersName(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "agent-attribution.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, d := range s.DailyUsage {
		got[d.Agent] += d.Usage.Output
	}
	// Explore holds 1500: its own 1000 plus the 500 its own delegation spent.
	want := map[string]int{"": 350, "Explore": 1500, "/lookup": 2000}
	if len(got) != len(want) {
		t.Fatalf("agents = %v, want %v", got, want)
	}
	for agent, tokens := range want {
		if got[agent] != tokens {
			t.Errorf("agent %q = %d output tokens, want %d", agent, got[agent], tokens)
		}
	}
	// Every delegated token is also in the session's own total, so the axis is a
	// breakdown of the bill rather than an addition to it.
	if s.Usage.Output != 3850 {
		t.Errorf("session output = %d, want 3850", s.Usage.Output)
	}
}

// TestNestedDelegationChargesTheCallTheSessionMade pins where a chain's cost
// lands: on the delegation the session itself chose, not on a row naming
// something it never invoked and could not decide to stop running.
func TestNestedDelegationChargesTheCallTheSessionMade(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "agent-attribution.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's Explore run delegates again, and that grandchild's log names
	// no caller of its own in the session's main log.
	for _, d := range s.DailyUsage {
		if d.Agent == unattributedAgent {
			t.Errorf("a traceable delegation was left unattributed: %+v", d)
		}
		if d.Agent == "Plan" {
			t.Errorf("nested delegation charged to %q, want the session's own call", d.Agent)
		}
	}
}

// TestActiveTimeIgnoresTrailingBookkeeping pins the measurement a resumed
// session breaks: the fixture's last entry is a file-history snapshot written a
// day after the work, and measuring the turn to it would report 24 hours of
// activity for 70 seconds of it.
func TestActiveTimeIgnoresTrailingBookkeeping(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "agent-attribution.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DailyActivity) != 1 {
		t.Fatalf("DailyActivity = %+v, want one day", s.DailyActivity)
	}
	a := s.DailyActivity[0]
	if a.Turns != 1 {
		t.Errorf("turns = %d, want 1", a.Turns)
	}
	if a.ActiveSeconds != 70 {
		t.Errorf("activeSeconds = %d, want 70 (10:00:00 to the last assistant entry at 10:01:10)", a.ActiveSeconds)
	}
	// The per-day split must reproduce the count the listing prints, or a roll-up
	// dividing by it would disagree with the session it came from.
	total := 0
	for _, d := range s.DailyActivity {
		total += d.Turns
	}
	if total != s.NumTurns {
		t.Errorf("DailyActivity turns = %d, NumTurns = %d", total, s.NumTurns)
	}
}

// TestCacheSavingAgreesAcrossBothReadPaths pins that the render path and the
// listing put the same saving on one session. They reach it differently — Load
// prices the sidecars it already parsed, Summarize prices the ones it opens for
// the split — and a session whose header and whose listing row disagreed about
// what caching saved would leave a reader with no way to tell which was right.
//
// The fixture's saving is negative, which is the case worth pinning end to end: it
// writes a cache for an hour at twice the input rate and reads almost none of it
// back, so it paid more than the same tokens would have cost uncached. A clamp
// anywhere along either path fails here.
func TestCacheSavingAgreesAcrossBothReadPaths(t *testing.T) {
	path := filepath.Join("testdata", "daily-usage.jsonl")

	sum, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if sum.CacheSaving == nil || sess.Meta.CacheSaving == nil {
		t.Fatalf("both paths must price a fixture whose models are all priced: listing %v, render %v",
			sum.CacheSaving, sess.Meta.CacheSaving)
	}
	if *sum.CacheSaving != *sess.Meta.CacheSaving {
		t.Errorf("listing priced %+v, render priced %+v", *sum.CacheSaving, *sess.Meta.CacheSaving)
	}

	// Hand-priced from the split the test above pins, each model at its own rates.
	want := model.CacheSaving{WithCacheUSD: 0.004914, WithoutCacheUSD: 0.004410}
	if math.Abs(sum.CacheSaving.WithCacheUSD-want.WithCacheUSD) > 1e-9 ||
		math.Abs(sum.CacheSaving.WithoutCacheUSD-want.WithoutCacheUSD) > 1e-9 {
		t.Errorf("CacheSaving = %+v, want %+v", *sum.CacheSaving, want)
	}
	if sum.CacheSaving.SavedShare() >= 0 {
		t.Errorf("SavedShare = %v; the fixture paid more than it would have uncached",
			sum.CacheSaving.SavedShare())
	}

	// The split and the total are the same responses read two ways, so the path
	// that groups them and the path that sums them cannot report different tokens.
	if sum.Usage != sess.Meta.Usage {
		t.Errorf("listing tallied %+v, render tallied %+v", sum.Usage, sess.Meta.Usage)
	}
}

// TestLoadDailyUsageMatchesSummarize pins that a render and a listing split one
// session's spend identically — same day, same model, same delegation. The
// render path builds the split from sidecars it already parsed while the listing
// reads them off disk, so the two are separate code paths over one question, and
// the render path charged every sidecar to the main thread until this version:
// its agent axis showed one row and answered nothing about where money went.
func TestLoadDailyUsageMatchesSummarize(t *testing.T) {
	path := filepath.Join("testdata", "subagent-usage.jsonl")
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key := func(rows []model.DailyUsage) map[string]model.Usage {
		out := map[string]model.Usage{}
		for _, r := range rows {
			out[r.Day+"|"+r.Model+"|"+r.Agent] = r.Usage
		}
		return out
	}
	want, got := key(s.DailyUsage), key(sess.Meta.DailyUsage)
	if len(got) != len(want) {
		t.Fatalf("render split has %d rows, listing has %d: %+v vs %+v", len(got), len(want), sess.Meta.DailyUsage, s.DailyUsage)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("row %q: render %+v, listing %+v", k, got[k], w)
		}
	}
	// The fixture delegates, so at least one row must name something other than
	// the main thread — without it the comparison above would pass on two equally
	// unlabelled splits.
	delegated := false
	for _, r := range sess.Meta.DailyUsage {
		if r.Agent != "" {
			delegated = true
		}
	}
	if !delegated {
		t.Errorf("no delegated row in %+v; the agent axis would read as one row", sess.Meta.DailyUsage)
	}
}

// TestSummarizeFailures pins which calls a session's failure tally holds: the
// ones that ran and failed, named by tool and identity, with refused calls left
// out. A refused call carries the log's error flag too, so counting on that flag
// alone reports a permission boundary that held as something to go fix — and the
// header's own counts make the same split, on the same field.
func TestSummarizeFailures(t *testing.T) {
	path := filepath.Join("testdata", "failures.jsonl")
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	// Two failing `go` commands collapse into one entry by identity; the failing
	// Edit is its own; the denied rm and the Read that worked are absent.
	want := []model.ToolStat{
		{Tool: "Bash", Identity: "go", Count: 2},
		{Tool: "Edit", Identity: "/repo/internal/cli/cli.go", Count: 1},
	}
	assertToolStats(t, s.Failures, want)
	for _, f := range s.Failures {
		if f.Identity == "rm" {
			t.Errorf("a refused call was counted as a failure: %+v", s.Failures)
		}
	}
	if len(s.Denials) != 1 {
		t.Errorf("Denials = %+v, want the one refused call", s.Denials)
	}

	// The render path reads the same log through different code, so the two tally
	// it identically or one of the surfaces is lying about the same session.
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	assertToolStats(t, sess.Meta.Failures, want)
}

// TestLoadNameLinkPairing pins the legacy skill-name fallback against the two
// ways it has gone wrong. The fixture holds an inline "beta" Skill call that
// spawned nothing, followed by an Agent call owning a sidecar whose log names the
// skill "beta", then two forked "gamma" calls with two unclaimed "gamma" sidecars:
//
//   - the inline call must not take the Agent's sidecar, which would also erase
//     the Agent's own expansion, since attachSubagent expands each sidecar once;
//   - the two gamma calls must take different sidecars, not the same one twice.
//
// Load runs repeatedly because Go randomizes map iteration order per range, so a
// pick that read one would vary between calls inside a single test process.
func TestLoadNameLinkPairing(t *testing.T) {
	want := []string{"", "owned agent work", "gamma first", "gamma second"}
	for i := 0; i < 20; i++ {
		sess, err := Load(filepath.Join("testdata", "namelink.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if len(sess.Turns) != 1 {
			t.Fatalf("turns = %d, want 1", len(sess.Turns))
		}
		var got []string
		for _, e := range sess.Turns[0].Events {
			if e.Kind != model.EventTool {
				continue
			}
			text := ""
			for _, se := range e.Tool.Subagent {
				if se.Kind == model.EventText {
					text = se.Text
					break
				}
			}
			got = append(got, text)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: expansions = %q, want %q", i, got, want)
		}
	}
}

// TestTypedForkAttribution pins what happens to a skill the caller invoked by
// typing its slash command. Claude Code forks it into a session of its own and
// writes the log beside every other sidecar, but the main log holds no tool call
// for it — so before this was handled its tokens were charged to no turn at all
// and its name was whatever the unattributed row is called.
//
// The fixture carries the three shapes together, because the rule for each is
// only correct against the other two: a Skill call that forked (c2), a typed
// command that forked (c1), and a subagent that loaded a skill partway through
// and must not be named by it (c3).
func TestTypedForkAttribution(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "typed-fork.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(sess.Turns))
	}

	t.Run("a forked Skill call is charged to the turn that made it", func(t *testing.T) {
		// 100 output of the turn's own, plus the c2 sidecar its Skill call spawned
		// and the c4 sidecar no record names. Matching the tool name "Agent" alone
		// leaves every forked skill's tokens out of the turn that paid for them.
		want := model.Usage{Input: 6, Output: 670}
		if sess.Turns[0].Usage != want {
			t.Errorf("turn 1 usage = %+v, want %+v", sess.Turns[0].Usage, want)
		}
	})

	t.Run("a typed command's fork is charged to its turn by prompt id", func(t *testing.T) {
		if got := sess.Turns[1].Prompt; got != "/commit push" {
			t.Fatalf("turn 2 prompt = %q", got)
		}
		want := model.Usage{Input: 7, Output: 300}
		if sess.Turns[1].Usage != want {
			t.Errorf("turn 2 usage = %+v, want %+v; the turn makes no tool call, so prompt id is the only link", sess.Turns[1].Usage, want)
		}
	})

	t.Run("each fork is named by the skill its own log opens with", func(t *testing.T) {
		want := map[string]model.Usage{
			// The main thread carries no label of its own; the surfaces that print
			// the axis are what name it.
			"": {Output: 100},
			// Both lookup runs: the one a Skill call spawned and the one no record
			// names, which is still a lookup whatever the log forgot.
			"/lookup":         {Input: 6, Output: 570},
			"/commit":         {Input: 7, Output: 300},
			unattributedAgent: {Output: 900},
		}
		got := map[string]model.Usage{}
		for _, d := range sess.Meta.DailyUsage {
			u := got[d.Agent]
			u.Add(d.Usage)
			got[d.Agent] = u
		}
		for agent, w := range want {
			if got[agent] != w {
				t.Errorf("agent %q = %+v, want %+v", agent, got[agent], w)
			}
		}
		if len(got) != len(want) {
			t.Errorf("agent rows = %v, want exactly %d", got, len(want))
		}
	})

	t.Run("the typed command's step renders what it printed and what it ran", func(t *testing.T) {
		events := sess.Turns[1].Events
		if len(events) != 2 {
			t.Fatalf("events = %d, want the fork's call then the printed reply", len(events))
		}
		call := events[0]
		if call.Kind != model.EventTool || call.Tool.Name != "Skill" || call.Tool.Identity != "commit" {
			t.Fatalf("first event = %+v, want a Skill call named commit", call)
		}
		if call.Tool.Result != "" {
			t.Errorf("Result = %q, want empty; the printed reply is the turn's own text", call.Tool.Result)
		}
		// Fenced, because a local command laid its output out for a terminal and
		// reflowing it as prose runs a fixed-width grid into one paragraph.
		want := "```\nAborted: policy requires a feature branch.\n```"
		if events[1].Kind != model.EventText || events[1].Text != want {
			t.Errorf("second event = %+v, want the local command's output fenced as turn text", events[1])
		}
		// The fork said the same thing its printed output says. Keeping both prints
		// it twice, so the expansion carries the work and the turn carries the word.
		if len(call.Tool.Subagent) != 1 || call.Tool.Subagent[0].Kind != model.EventTool {
			t.Fatalf("expansion = %+v, want the fork's one tool call and no trailing reply", call.Tool.Subagent)
		}
	})

	t.Run("a typed command is a skill in the tally, a prose-prompted fork is not", func(t *testing.T) {
		got := map[string]int{}
		for _, st := range sess.Meta.Tools {
			if st.Tool == "Skill" {
				got[st.Identity] = st.Count
			}
		}
		// commit was typed, so it is counted though the model called nothing.
		// lookup is counted once, for the Skill call the model made — the c4
		// sidecar is a lookup run under a prose prompt, the shape a name pairing
		// claims in pre-structured logs, and counting it here would double it.
		want := map[string]int{"commit": 1, "lookup": 1}
		if !maps.Equal(got, want) {
			t.Errorf("Skill tally = %v, want %v", got, want)
		}
	})

	t.Run("the turn counts the call its transcript shows", func(t *testing.T) {
		// A rule reading "0 tools" beneath a rendered Skill line contradicts the
		// page above it, and the header sums these counts.
		if sess.Turns[1].ToolCount != 1 {
			t.Errorf("turn 2 tool count = %d, want 1 for the typed command", sess.Turns[1].ToolCount)
		}
		total := 0
		for _, st := range sess.Meta.Tools {
			total += st.Count
		}
		turns := 0
		for _, tn := range sess.Turns {
			turns += tn.ToolCount
		}
		if total != turns {
			t.Errorf("tally total %d != summed turn counts %d; the header reads the second", total, turns)
		}
	})

	t.Run("the listing's tally matches the render's", func(t *testing.T) {
		s, err := Summarize(filepath.Join("testdata", "typed-fork.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(s.Tools, sess.Meta.Tools) {
			t.Errorf("listing tools %+v != render tools %+v", s.Tools, sess.Meta.Tools)
		}
	})

	t.Run("the listing names the agents the render does", func(t *testing.T) {
		s, err := Summarize(filepath.Join("testdata", "typed-fork.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		render := map[string]model.Usage{}
		for _, d := range sess.Meta.DailyUsage {
			u := render[d.Agent]
			u.Add(d.Usage)
			render[d.Agent] = u
		}
		listing := map[string]model.Usage{}
		for _, d := range s.DailyUsage {
			u := listing[d.Agent]
			u.Add(d.Usage)
			listing[d.Agent] = u
		}
		if !maps.Equal(listing, render) {
			t.Errorf("listing agents %v != render agents %v; one session's spend must read the same on both paths", listing, render)
		}
	})
}

// TestStripEscapes covers the shapes a captured terminal stream carries. The
// /context command styles its headings, and those codes reach the log verbatim;
// re-rendered through markdown they would print as text.
func TestStripEscapes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no escape is untouched", "plain text", "plain text"},
		{"a control sequence goes", "\x1b[1mContext Usage\x1b[22m", "Context Usage"},
		{"an operating-system command ends at a bell", "a\x1b]8;;http://x\x07b", "ab"},
		{"an operating-system command ends at a string terminator", "a\x1b]0;title\x1b\\b", "ab"},
		{"a two-byte escape goes", "a\x1bMb", "ab"},
		{"an unterminated sequence takes the rest", "keep\x1b[1;2", "keep"},
		{"newlines and tabs are content", "one\n\ttwo", "one\n\ttwo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripEscapes(c.in); got != c.want {
				t.Errorf("stripEscapes(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestFenced pins the fence escalation. Captured output can itself hold a code
// fence, and a fence no longer than that one closes this block early, spilling
// the rest of the output into the transcript as markdown.
func TestFenced(t *testing.T) {
	cases := []struct{ name, in, wantFence string }{
		{"plain output takes three backticks", "hello", "```"},
		{"an inline span still takes three", "run `ls` first", "```"},
		{"a fence inside escalates to four", "```\ncode\n```", "````"},
		{"the longest run wins", "``a````b```", "`````"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fenced(c.in)
			want := c.wantFence + "\n" + c.in + "\n" + c.wantFence
			if got != want {
				t.Errorf("fenced(%q) = %q, want %q", c.in, got, want)
			}
		})
	}
}

// TestTurnCostPricesEachModel pins that a turn is priced per model rather than
// summed first. The fixture's second turn runs entirely on the sonnet fork while
// the session runs on opus, so a turn priced at the session's model reports a
// bill nobody was sent — cache reads alone differ by a factor of four between
// the two tiers.
func TestTurnCostPricesEachModel(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "typed-fork.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	typedTurn := sess.Turns[1]
	if typedTurn.CostUSD == nil {
		t.Fatal("the typed command's turn carries no cost; its fork ran on a priced model")
	}
	want, ok := price.Of("claude-sonnet-5", typedTurn.Usage)
	if !ok {
		t.Fatal("the test's own reference price is unavailable")
	}
	if math.Abs(*typedTurn.CostUSD-want) > 1e-12 {
		t.Errorf("turn cost = %v, want %v (the fork's own model, not the session's)", *typedTurn.CostUSD, want)
	}
	if wrong, ok := price.Of("claude-opus-5", typedTurn.Usage); ok && math.Abs(want-wrong) < 1e-12 {
		t.Skip("the two models price this usage identically, so the test cannot tell them apart")
	}

	// The first turn mixes the main thread's opus with two sonnet forks, so its
	// cost is the sum of the parts at their own rates rather than the whole at
	// either rate.
	mixed := sess.Turns[0]
	if mixed.CostUSD == nil {
		t.Fatal("the first turn carries no cost")
	}
	atOneRate, _ := price.Of("claude-opus-5", mixed.Usage)
	if math.Abs(*mixed.CostUSD-atOneRate) < 1e-12 {
		t.Errorf("mixed turn priced as if one model ran it: %v", *mixed.CostUSD)
	}
}

// TestTypedShellCommandBecomesItsOwnTurn pins the three things a shell command
// run in the session with "!" was missing: the command itself, what it printed,
// and the tokens of whatever Claude then said about the result.
//
// The fixture opens on such a command, which is the case that lost the most:
// before the first prompt there was no open turn to fold the reply into, so its
// entries were discarded from the transcript while still counting in the header.
func TestTypedShellCommandBecomesItsOwnTurn(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "typed-shell.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(sess.Turns))
	}

	opening := sess.Turns[0]
	if opening.Prompt != "! agentry" {
		t.Errorf("opening prompt = %q, want %q", opening.Prompt, "! agentry")
	}
	texts := eventTexts(opening.Events)
	if len(texts) != 2 {
		t.Fatalf("opening turn texts = %q, want the command's output and Claude's reply", texts)
	}
	if !strings.Contains(texts[0], "no Claude project") {
		t.Errorf("first text = %q, want what the command printed", texts[0])
	}
	if !strings.Contains(texts[0], "stderr") {
		t.Errorf("first text = %q, want the stream named: the command wrote nothing to stdout", texts[0])
	}
	if !strings.Contains(texts[1], "nothing to render") {
		t.Errorf("second text = %q, want Claude's reply to the failure", texts[1])
	}
	if opening.Usage.Output != 732 {
		t.Errorf("opening turn output = %d, want 732 charged to the command that caused it", opening.Usage.Output)
	}

	// The acceptance check for the defect this fixture was written from: every
	// response now belongs to a turn, so the rules add up to the header.
	var summed model.Usage
	for _, turn := range sess.Turns {
		summed.Add(turn.Usage)
	}
	if summed != sess.Meta.Usage {
		t.Errorf("turns sum to %+v, session reports %+v", summed, sess.Meta.Usage)
	}

	// A prompt names what the session was for; a command names what someone ran.
	if sess.Meta.Title != "fix it" {
		t.Errorf("title = %q, want the first prompt that is not a shell command", sess.Meta.Title)
	}

	silent := sess.Turns[2]
	if silent.Prompt != "! echo done" {
		t.Errorf("third prompt = %q, want %q", silent.Prompt, "! echo done")
	}
	if texts := eventTexts(silent.Events); len(texts) != 1 || strings.Contains(texts[0], "\x1b") {
		t.Errorf("third turn texts = %q, want one block with the terminal escapes stripped", texts)
	}
}

// TestShellOutput pins which entries are a typed command's output and which are
// not. The wrappers appear inside ordinary prose — a compaction summary quotes
// them when it summarizes a session that ran one — so a substring match would
// turn a sentence about a command into the command's output.
func TestShellOutput(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		out, errOut string
		ok          bool
	}{
		{"both streams", "<bash-stdout>listing</bash-stdout><bash-stderr>warning</bash-stderr>", "listing", "warning", true},
		{"stdout only, empty stderr", "<bash-stdout>listing</bash-stdout><bash-stderr></bash-stderr>", "listing", "", true},
		{"stderr wrapper alone", "<bash-stderr>Command failed</bash-stderr>", "", "Command failed", true},
		{"both empty", "<bash-stdout></bash-stdout><bash-stderr></bash-stderr>", "", "", true},
		{"quoted inside prose", "the run printed <bash-stdout>x</bash-stdout> and stopped", "", "", false},
		{"trailing text after the wrappers", "<bash-stdout>x</bash-stdout><bash-stderr></bash-stderr> and then", "", "", false},
		{"unclosed wrapper", "<bash-stdout>x", "", "", false},
		{"an ordinary prompt", "render the session", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, ok := shellOutput(tt.content)
			if out != tt.out || errOut != tt.errOut || ok != tt.ok {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", out, errOut, ok, tt.out, tt.errOut, tt.ok)
			}
		})
	}
}

// TestPromptsRecordedAsBlocks pins that a session whose prompts arrive as text
// blocks rather than as a plain string gets turns at all. The software
// development kit writes them that way, and such a session rendered blank:
// every prompt refused, no turn opened, and every response it made left out of
// the per-turn tallies while still counting in the header.
//
// The fixture also carries the two entries that must stay refused in this shape
// — a tool result, and the line the harness writes where a reply was cut short.
func TestPromptsRecordedAsBlocks(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "block-prompts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2: two prompts, and neither the tool result nor the interruption", len(sess.Turns))
	}
	if sess.Turns[0].Prompt != "probe the worktree and report" {
		t.Errorf("first prompt = %q", sess.Turns[0].Prompt)
	}
	if sess.Turns[1].Prompt != "now do the same for the other one" {
		t.Errorf("second prompt = %q", sess.Turns[1].Prompt)
	}

	var summed model.Usage
	for _, turn := range sess.Turns {
		summed.Add(turn.Usage)
	}
	if summed != sess.Meta.Usage {
		t.Errorf("turns sum to %+v, session reports %+v", summed, sess.Meta.Usage)
	}
	if sess.Turns[0].Usage.Output != 520 {
		t.Errorf("first turn output = %d, want both of its responses", sess.Turns[0].Usage.Output)
	}
}

// TestPromptText pins which block shapes offer text to the prompt tests. One
// text block among others is not a prompt: accepting an entry for the text
// beside a result would wrongly turn tool results into turns as well.
func TestPromptText(t *testing.T) {
	tests := []struct {
		name string
		e    entry
		want string
		ok   bool
	}{
		{"a plain string", entry{hasStr: true, contentStr: "render it"}, "render it", true},
		{"an empty string is still content", entry{hasStr: true, contentStr: ""}, "", true},
		{"one text block", entry{blocks: []block{{typ: "text", text: "render it"}}}, "render it", true},
		{"two text blocks join on a newline", entry{blocks: []block{{typ: "text", text: "first"}, {typ: "text", text: "second"}}}, "first\nsecond", true},
		{"a tool result", entry{blocks: []block{{typ: "tool_result", resultText: "output"}}}, "", false},
		{"text beside a tool result", entry{blocks: []block{{typ: "text", text: "see this"}, {typ: "tool_result"}}}, "", false},
		{"an image", entry{blocks: []block{{typ: "image"}}}, "", false},
		{"no content at all", entry{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := promptText(tt.e)
			if got != tt.want || ok != tt.ok {
				t.Errorf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestOnePromptIDTwoTurnsChargesTheForkOnce pins the one place a prompt id is
// not a turn. Claude Code writes the same id on a typed slash command and on a
// shell command queued behind it, so both turns look up the same forked skill
// session. Charging it to both counted its tokens twice, rendered its call in
// both turns, and listed the skill twice in the tools tally.
//
// The fixture models a "/commit" whose skill forks, then a queued "git push"
// carrying the same prompt id.
func TestOnePromptIDTwoTurnsChargesTheForkOnce(t *testing.T) {
	path := filepath.Join("testdata", "shared-prompt-id.jsonl")
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(sess.Turns))
	}

	var summed model.Usage
	for _, turn := range sess.Turns {
		summed.Add(turn.Usage)
	}
	if summed != sess.Meta.Usage {
		t.Errorf("turns sum to %+v, session reports %+v", summed, sess.Meta.Usage)
	}
	if got := sess.Turns[0].Usage.Output; got != 1483 {
		t.Errorf("command turn output = %d, want the fork's 1483", got)
	}
	if got := sess.Turns[1].Usage.Output; got != 556 {
		t.Errorf("queued shell turn output = %d, want only its own reply", got)
	}

	var skills int
	for _, turn := range sess.Turns {
		for _, e := range turn.Events {
			if e.Tool != nil && e.Tool.Name == "Skill" {
				skills++
			}
		}
	}
	if skills != 1 {
		t.Errorf("Skill events across the session = %d, want 1", skills)
	}

	// The listing counts the same call from its own path, so the two surfaces
	// must not disagree about how many times the skill ran.
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stat := range s.Tools {
		if stat.Tool == "Skill" && stat.Count != 1 {
			t.Errorf("listing counts Skill %s %d times, want 1", stat.Identity, stat.Count)
		}
	}
}

// balance is what a session's rules leave unaccounted for against its header:
// zero on a session whose turns carry everything it spent.
func balance(t *testing.T, sess *model.Session) {
	t.Helper()
	var summed model.Usage
	for _, turn := range sess.Turns {
		summed.Add(turn.Usage)
	}
	if summed != sess.Meta.Usage {
		t.Errorf("turns sum to %+v, session reports %+v", summed, sess.Meta.Usage)
	}
}

// TestEntriesThatLookLikeTurnsAndAreNot pins two entries that opened turns they
// should not have. A log can write the same entry a second time — a whole turn
// replayed under a fresh prompt id — and the harness files a reminder about a
// tool call under the user's name.
func TestEntriesThatLookLikeTurnsAndAreNot(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "not-a-turn.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 1 {
		var got []string
		for _, turn := range sess.Turns {
			got = append(got, turn.Prompt)
		}
		t.Fatalf("turns = %q, want the one prompt a person typed", got)
	}
	if sess.Meta.Usage.Output != 420 {
		t.Errorf("session output = %d, want 420: the replayed response counted once", sess.Meta.Usage.Output)
	}
	balance(t, sess)
}

// TestNestedForkedSkillIsCharged pins a forked skill a subagent started of its
// own accord. The session-wide name pairing resolves it, which marks the log
// claimed, but the walk that charges a turn followed structured ids only — so
// the log was claimed and unreachable at once and nothing counted it.
func TestNestedForkedSkillIsCharged(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "nested-fork.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(sess.Turns))
	}
	if got := sess.Turns[0].Usage.Output; got != 90+200+1551 {
		t.Errorf("turn output = %d, want the reply, the subagent and the skill it forked", got)
	}
	balance(t, sess)
}

// TestForkWithNoMatchingPromptIsPlacedByTime pins a forked log stamped with a
// prompt id no user entry in the main log carries. A lookup by that id found no
// turn, so the fork's tokens, its dollars and its row in the tools tally all
// went missing. It belongs to the turn it ran inside.
func TestForkWithNoMatchingPromptIsPlacedByTime(t *testing.T) {
	path := filepath.Join("testdata", "orphan-fork.jsonl")
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(sess.Turns))
	}
	if got := sess.Turns[0].Usage.Output; got != 80 {
		t.Errorf("first turn output = %d, want 80: the fork ran after it closed", got)
	}
	if got := sess.Turns[1].Usage.Output; got != 659 {
		t.Errorf("command turn output = %d, want the fork's 659", got)
	}
	balance(t, sess)

	var skills int
	for _, e := range sess.Turns[1].Events {
		if e.Tool != nil && e.Tool.Name == "Skill" {
			skills++
		}
	}
	if skills != 1 {
		t.Errorf("Skill events on the command turn = %d, want 1", skills)
	}
	s, err := Summarize(path)
	if err != nil {
		t.Fatal(err)
	}
	var listed int
	for _, stat := range s.Tools {
		if stat.Tool == "Skill" {
			listed += stat.Count
		}
	}
	if listed != 1 {
		t.Errorf("listing counts Skill %d times, want 1", listed)
	}
}

// TestLoadTalliesAgreeWithTurns pins the invariant the rendered header rests on.
// The header reads its counts off Meta rather than off Turns, because Turns holds
// what a caller asked to render and Meta holds the session — so a narrowed
// transcript cannot make the header report a smaller session. That only holds
// while the two agree on a whole session, and nothing else checks it: Meta's
// tallies are built from the entries and the per-turn counts from the turn tree,
// by different code down different paths.
func TestLoadTalliesAgreeWithTurns(t *testing.T) {
	for _, logFile := range []string{"sample.jsonl", "tools.jsonl", "failures.jsonl", "agent-delegation.jsonl"} {
		t.Run(logFile, func(t *testing.T) {
			sess, err := Load(filepath.Join("testdata", logFile))
			if err != nil {
				t.Fatal(err)
			}
			if sess.Meta.NumTurns != len(sess.Turns) {
				t.Errorf("NumTurns = %d, turns = %d", sess.Meta.NumTurns, len(sess.Turns))
			}
			tools, errs := 0, 0
			for _, turn := range sess.Turns {
				tools += turn.ToolCount
				errs += turn.ErrorCount
			}
			total := func(stats []model.ToolStat) int {
				n := 0
				for _, s := range stats {
					n += s.Count
				}
				return n
			}
			if got := total(sess.Meta.Tools); got != tools {
				t.Errorf("Meta.Tools sums to %d, the turns to %d", got, tools)
			}
			// ErrorCount counts every top-level call that errored, and a refused
			// call errors too, so the turns' figure is the failures and the denials
			// together. Meta keeps them apart because they ask for different things.
			denied := 0
			for _, d := range sess.Meta.Denials {
				denied += d.Count
			}
			if got := total(sess.Meta.Failures) + denied; got != errs {
				t.Errorf("Meta.Failures+Denials sums to %d, the turns' ErrorCount to %d", got, errs)
			}
		})
	}
}

// TestLoadNumbersEveryTurn pins that a turn carries its own place in the session.
// Its index in Turns says the same thing only while the whole session is present,
// and a selected slice restarts that index — so an unnumbered turn is
// unidentifiable in exactly the output a caller asked to narrow.
func TestLoadNumbersEveryTurn(t *testing.T) {
	sess, err := Load(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Turns) == 0 {
		t.Fatal("fixture holds no turns")
	}
	for i, turn := range sess.Turns {
		if turn.Number != i+1 {
			t.Errorf("turn at index %d is numbered %d, want %d", i, turn.Number, i+1)
		}
	}
}

// TestTypedCommandOnASystemEntryBecomesItsOwnTurn pins the second shape a typed
// command reaches the log in: the line the caller typed recorded on a
// local_command system entry rather than as their prompt, with some commands then
// writing their report into the conversation as an entry of Claude Code's own.
//
// Read as prompts, both halves land wrong. The command renders nowhere, and its
// report becomes a turn nobody took — which is how a session came to be listed
// under the first line of a context report.
//
// The fixture carries both kinds: /context, which writes a report, and /model,
// which writes none and is followed by a prompt somebody typed. The second is
// what holds the window shut. Claiming "the next user entry" for a command would
// take that prompt for the command's output and lose the turn.
func TestTypedCommandOnASystemEntryBecomesItsOwnTurn(t *testing.T) {
	path := filepath.Join("testdata", "typed-command-entry.jsonl")
	sess, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	prompts := make([]string, 0, len(sess.Turns))
	for _, tn := range sess.Turns {
		prompts = append(prompts, tn.Prompt)
	}
	want := []string{"/context", "now trim the skills", "/model", "thanks"}
	if !slices.Equal(prompts, want) {
		t.Fatalf("prompts = %q, want %q", prompts, want)
	}

	texts := eventTexts(sess.Turns[0].Events)
	if len(texts) != 2 {
		t.Fatalf("the command's turn holds %d texts, want what it printed and the report it wrote: %q", len(texts), texts)
	}
	if !strings.Contains(texts[0], "45.5k/1m tokens") {
		t.Errorf("first text = %q, want what the command printed to the terminal", texts[0])
	}
	if !strings.Contains(texts[1], "| Skills | 6.6k |") {
		t.Errorf("second text = %q, want the report the command wrote into the conversation", texts[1])
	}

	if sess.Meta.Title != "/context" {
		t.Errorf("title = %q, want the command the session opened with", sess.Meta.Title)
	}

	// The report is the only entry in that turn carrying a prompt id, and a forked
	// command is charged to its turn through that id, so the turn takes it over.
	entries, err := loadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if id := splitTurns(entries)[0].promptID; id != "p_context" {
		t.Errorf("the command's turn carries prompt id %q, want the one its report arrived under", id)
	}
}

// TestEveryCallCarriesItsPositionInTheTurn pins the numbering both a render and
// a search read: every call in a turn gets its position in that turn, counted in
// the order a reader scrolls through them — a call before the stream it spawned.
//
// One pass assigns it because two surfaces numbering separately would drift, and
// the drift is invisible: a search naming the third call while a render numbered
// a different one sends a reader to the wrong call with both outputs looking
// correct.
func TestEveryCallCarriesItsPositionInTheTurn(t *testing.T) {
	t.Run("depth first, a call before the stream it spawned", func(t *testing.T) {
		call := func(name string, nested ...model.Event) model.Event {
			return model.Event{Kind: model.EventTool, Tool: &model.Tool{Name: name, Subagent: nested}}
		}
		events := []model.Event{
			call("Read"),
			call("Agent",
				call("Grep"),
				model.Event{Kind: model.EventText, Text: "not a call"},
				call("Write"),
			),
			call("Bash"),
		}
		numberCalls(events)

		got := map[string]int{}
		var walk func([]model.Event)
		walk = func(stream []model.Event) {
			for _, e := range stream {
				if e.Kind != model.EventTool {
					continue
				}
				got[e.Tool.Name] = e.Tool.Call
				walk(e.Tool.Subagent)
			}
		}
		walk(events)

		want := map[string]int{"Read": 1, "Agent": 2, "Grep": 3, "Write": 4, "Bash": 5}
		for name, n := range want {
			if got[name] != n {
				t.Errorf("%s is call %d, want %d (all: %v)", name, got[name], n, got)
			}
		}
	})

	t.Run("a parsed session's calls are numbered", func(t *testing.T) {
		sess, err := Load(filepath.Join("testdata", "typed-fork.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		fork := sess.Turns[1].Events[0]
		if fork.Kind != model.EventTool {
			t.Fatalf("first event = %+v, want the fork's call", fork)
		}
		if fork.Tool.Call != 1 {
			t.Errorf("the turn's first call is numbered %d, want 1", fork.Tool.Call)
		}
		// The call inside the expansion follows the call that spawned it, which is
		// the order the render prints the two in.
		nested := fork.Tool.Subagent[0]
		if nested.Tool.Call != 2 {
			t.Errorf("the call inside the expansion is numbered %d, want 2", nested.Tool.Call)
		}
	})
}
