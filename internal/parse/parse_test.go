package parse

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/eitanpo/agentry/internal/model"
)

func TestSummarize(t *testing.T) {
	s, err := Summarize(filepath.Join("testdata", "sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sample" {
		t.Errorf("ID = %q, want sample", s.ID)
	}
	if s.NumTurns != 2 {
		t.Errorf("NumTurns = %d, want 2", s.NumTurns)
	}
	if s.Title != "first prompt" {
		t.Errorf("Title = %q, want %q", s.Title, "first prompt")
	}
	wantPrompts := []string{"first prompt", "second prompt"}
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

func TestIsClearCmd(t *testing.T) {
	clear := []string{"//clear", "/clear", "  //clear  ", "clear"}
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

	// The injected <bash-input> and <task-notification> entries must not start
	// their own turns.
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(sess.Turns))
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

	turn1 := sess.Turns[1]
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

func TestUserPrompt(t *testing.T) {
	tests := []struct {
		name   string
		entry  entry
		want   string
		wantOK bool
	}{
		{"typed", entry{hasStr: true, contentStr: "hello"}, "hello", true},
		{"bash injected", entry{hasStr: true, contentStr: "<bash-input>x</bash-input>"}, "", false},
		{"skill injected", entry{hasStr: true, contentStr: "Base directory for this skill: /x"}, "", false},
		{"command", entry{hasStr: true, contentStr: "<command-name>foo</command-name><command-args>bar</command-args>"}, "/foo bar", true},
		{"compaction", entry{hasStr: true, contentStr: "...This session is being continued from a previous conversation..."}, "[context compacted — see session log for full summary]", true},
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
