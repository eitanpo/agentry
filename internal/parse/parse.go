// Package parse turns a session JSONL file (and its subagent sidecar files)
// into the canonical model.Session. The extraction logic is ported from the
// claude-logs-search Python reference, verified against live logs under
// ~/.claude/projects/. The LLM-safety envelope from the reference is
// deliberately omitted — agentry renders for humans, not for re-ingestion.
package parse

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eitanpo/agentry/internal/model"
)

// Only user and assistant entries carry content we render; every other type
// (ai-title, attachment, system, permission-mode, file-history-snapshot,
// progress, queue-operation, last-prompt, …) is ignored.

var agentIDRe = regexp.MustCompile(`agentId:\s*(\S+)`)

// injectedMarkers identify user entries that are system-injected, not typed.
var injectedMarkers = []string{
	"<local-command-caveat>", "<bash-stdout>", "<bash-stderr>",
	"<bash-input>", "Base directory for this skill:", "<local-command-stdout>",
}

// Load parses the session at jsonlPath into a Session.
func Load(jsonlPath string) (*model.Session, error) {
	entries, err := loadEntries(jsonlPath)
	if err != nil {
		return nil, err
	}

	projectDir := filepath.Dir(jsonlPath)
	stem := strings.TrimSuffix(filepath.Base(jsonlPath), filepath.Ext(jsonlPath))
	subs := loadSubagents(filepath.Join(projectDir, stem, "subagents"))

	sess := &model.Session{
		Meta: model.Meta{
			ID:           stem,
			Model:        extractModel(entries),
			NumSubagents: len(subs),
		},
	}
	sess.Meta.Start, sess.Meta.End = timeRange(entries)

	sess.Meta.Usage = sumUsage(entries)
	for _, s := range subs {
		sess.Meta.Usage.Add(sumUsage(s.entries))
	}

	for _, t := range splitTurns(entries) {
		turn := model.Turn{
			Prompt: t.prompt,
			Start:  t.start,
			End:    t.end,
			Events: buildEvents(t.entries, subs, map[string]bool{}),
		}
		turn.Usage, turn.ToolCount, turn.ErrorCount = turnMetrics(t.entries, subs)
		sess.Turns = append(sess.Turns, turn)
	}
	return sess, nil
}

// ── Raw JSONL decoding ───────────────────────────────────────────────────

type entry struct {
	typ        string
	t          time.Time
	model      string
	usage      model.Usage
	contentStr string  // set when message.content is a JSON string
	hasStr     bool    // distinguishes "" content from absent/array content
	blocks     []block // set when message.content is a JSON array
}

type block struct {
	typ        string
	text       string         // text blocks
	thinking   string         // thinking blocks
	id         string         // tool_use id
	name       string         // tool_use name
	input      map[string]any // tool_use input
	toolUseID  string         // tool_result target
	isError    bool           // tool_result error flag
	resultText string         // tool_result flattened text
}

type rawEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type rawMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   rawUsage        `json:"usage"`
}

type rawUsage struct {
	Input       int `json:"input_tokens"`
	Output      int `json:"output_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

func loadEntries(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // logs hold large tool results
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var re rawEntry
		if json.Unmarshal([]byte(line), &re) != nil {
			continue // skip malformed lines, as the reference does
		}
		e := entry{typ: re.Type}
		if ts, err := time.Parse(time.RFC3339, re.Timestamp); err == nil {
			e.t = ts
		}
		if len(re.Message) > 0 {
			var msg rawMessage
			if json.Unmarshal(re.Message, &msg) == nil {
				e.model = msg.Model
				e.usage = model.Usage{
					Input: msg.Usage.Input, Output: msg.Usage.Output,
					CacheRead: msg.Usage.CacheRead, CacheCreate: msg.Usage.CacheCreate,
				}
				e.contentStr, e.hasStr, e.blocks = decodeContent(msg.Content)
			}
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// decodeContent handles message.content being either a JSON string or an array
// of typed blocks.
func decodeContent(raw json.RawMessage) (str string, hasStr bool, blocks []block) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if raw[0] == '"' {
		_ = json.Unmarshal(raw, &str)
		return str, true, nil
	}
	var rbs []rawBlock
	if json.Unmarshal(raw, &rbs) != nil {
		return "", false, nil
	}
	for _, rb := range rbs {
		blocks = append(blocks, block{
			typ: rb.Type, text: rb.Text, thinking: rb.Thinking,
			id: rb.ID, name: rb.Name, input: rb.Input,
			toolUseID: rb.ToolUseID, isError: rb.IsError,
			resultText: flattenResult(rb.Content),
		})
	}
	return "", false, blocks
}

// flattenResult extracts text from a tool_result's content, which is either a
// string or an array of {type:"text", text:...} blocks.
func flattenResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// ── Session-level extraction ───────────────────────────────────────────────

func timeRange(entries []entry) (start, end time.Time) {
	for _, e := range entries {
		if e.t.IsZero() {
			continue
		}
		if start.IsZero() {
			start = e.t
		}
		end = e.t
	}
	return start, end
}

func extractModel(entries []entry) string {
	for _, e := range entries {
		if e.typ == "assistant" && e.model != "" {
			return e.model
		}
	}
	return "unknown"
}

func sumUsage(entries []entry) model.Usage {
	var u model.Usage
	for _, e := range entries {
		if e.typ == "assistant" {
			u.Add(e.usage)
		}
	}
	return u
}

// ── Tool results and agent stitching ─────────────────────────────────────

type toolResult struct {
	end     time.Time
	isError bool
	text    string
}

func toolResultMap(entries []entry) map[string]toolResult {
	m := map[string]toolResult{}
	for _, e := range entries {
		if e.typ != "user" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_result" {
				m[b.toolUseID] = toolResult{end: e.t, isError: b.isError, text: b.resultText}
			}
		}
	}
	return m
}

// agentIDMap maps an Agent tool_use id to its subagent log key ("agent-xxx"),
// recovered from the "agentId: …" line in the tool's result text.
func agentIDMap(entries []entry) map[string]string {
	agentTools := map[string]bool{}
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_use" && b.name == "Agent" {
				agentTools[b.id] = true
			}
		}
	}
	m := map[string]string{}
	for _, e := range entries {
		if e.typ != "user" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_result" || !agentTools[b.toolUseID] {
				continue
			}
			if mt := agentIDRe.FindStringSubmatch(b.resultText); mt != nil {
				m[b.toolUseID] = "agent-" + mt[1]
			}
		}
	}
	return m
}

// ── Subagents ────────────────────────────────────────────────────────────

type subagent struct {
	entries   []entry
	skillName string
}

func loadSubagents(dir string) map[string]*subagent {
	subs := map[string]*subagent{}
	matches, _ := filepath.Glob(filepath.Join(dir, "agent-*.jsonl"))
	for _, path := range matches {
		entries, err := loadEntries(path)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		subs[id] = &subagent{entries: entries, skillName: subagentSkill(entries)}
	}
	return subs
}

func subagentSkill(entries []entry) string {
	const marker = "Base directory for this skill:"
	for _, e := range entries {
		text := e.contentStr
		if !e.hasStr {
			for _, b := range e.blocks {
				if b.typ == "text" {
					text = b.text
					break
				}
			}
		}
		if !strings.Contains(text, marker) {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, marker) {
				p := strings.TrimSpace(line[strings.Index(line, marker)+len(marker):])
				return filepath.Base(p)
			}
		}
	}
	return ""
}

// totalAgentUsage sums an agent's tokens plus those of every agent it spawned.
func totalAgentUsage(id string, subs map[string]*subagent, seen map[string]bool) model.Usage {
	var u model.Usage
	if seen[id] {
		return u
	}
	seen[id] = true
	s := subs[id]
	if s == nil {
		return u
	}
	u = sumUsage(s.entries)
	for _, nestedID := range agentIDMap(s.entries) {
		u.Add(totalAgentUsage(nestedID, subs, seen))
	}
	return u
}

// ── Turn splitting ─────────────────────────────────────────────────────────

type rawTurn struct {
	prompt  string
	start   time.Time
	end     time.Time
	entries []entry
}

func splitTurns(entries []entry) []rawTurn {
	var turns []rawTurn
	var cur *rawTurn
	for _, e := range entries {
		if e.typ == "user" {
			if prompt, ok := userPrompt(e); ok {
				if cur != nil {
					turns = append(turns, *cur)
				}
				cur = &rawTurn{prompt: prompt, start: e.t, end: e.t}
				continue
			}
		}
		if cur != nil {
			cur.entries = append(cur.entries, e)
			if !e.t.IsZero() {
				cur.end = e.t
			}
		}
	}
	if cur != nil {
		turns = append(turns, *cur)
	}
	return turns
}

var (
	cmdNameRe = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
	cmdArgsRe = regexp.MustCompile(`<command-args>(.*?)</command-args>`)
)

// userPrompt returns the human-typed prompt for a user entry, or ok=false for
// system-injected content (tool results, skill bodies, bash output).
func userPrompt(e entry) (string, bool) {
	if !e.hasStr {
		return "", false
	}
	content := e.contentStr
	for _, m := range injectedMarkers {
		if strings.Contains(content, m) {
			return "", false
		}
	}
	if strings.Contains(content, "This session is being continued from a previous conversation") {
		return "[context compacted — see session log for full summary]", true
	}
	if strings.Contains(content, "<command-name>") {
		cmd, args := "?", ""
		if m := cmdNameRe.FindStringSubmatch(content); m != nil {
			cmd = m[1]
		}
		if m := cmdArgsRe.FindStringSubmatch(content); m != nil {
			args = strings.TrimSpace(m[1])
		}
		return strings.TrimRight("/"+cmd+" "+args, " "), true
	}
	if text := strings.TrimSpace(content); text != "" {
		return text, true
	}
	return "", false
}

// ── Event building ─────────────────────────────────────────────────────────

// buildEvents flattens an assistant stream into ordered events. seen holds the
// subagent ids already expanded on the current path, breaking reference cycles
// (a skill subagent can match itself by name).
func buildEvents(entries []entry, subs map[string]*subagent, seen map[string]bool) []model.Event {
	results := toolResultMap(entries)
	agents := agentIDMap(entries)
	var out []model.Event
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			switch b.typ {
			case "text":
				if strings.TrimSpace(b.text) != "" {
					out = append(out, model.Event{Kind: model.EventText, Text: b.text})
				}
			case "thinking":
				if strings.TrimSpace(b.thinking) != "" {
					out = append(out, model.Event{Kind: model.EventThinking, Text: b.thinking})
				}
			case "tool_use":
				res := results[b.id]
				tool := &model.Tool{
					Name:    b.name,
					Args:    formatToolArgs(b.name, b.input),
					Result:  res.text,
					IsError: res.isError,
					Start:   e.t,
					End:     res.end,
				}
				attachSubagent(tool, b, agents, subs, seen)
				out = append(out, model.Event{Kind: model.EventTool, Tool: tool})
			}
		}
	}
	return out
}

// attachSubagent fills tool.Subagent for Agent and Skill calls that spawned one.
func attachSubagent(tool *model.Tool, b block, agents map[string]string, subs map[string]*subagent, seen map[string]bool) {
	key := ""
	switch b.name {
	case "Agent":
		key = agents[b.id]
	case "Skill":
		if skill, _ := b.input["skill"].(string); skill != "" {
			for id, s := range subs {
				if s.skillName == skill {
					key = id
					break
				}
			}
		}
	}
	if key == "" || seen[key] || subs[key] == nil {
		return
	}
	seen[key] = true
	tool.Subagent = buildEvents(subs[key].entries, subs, seen)
}

func turnMetrics(entries []entry, subs map[string]*subagent) (u model.Usage, tools, errs int) {
	results := toolResultMap(entries)
	agents := agentIDMap(entries)
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		u.Add(e.usage)
		for _, b := range e.blocks {
			if b.typ != "tool_use" {
				continue
			}
			tools++
			if results[b.id].isError {
				errs++
			}
			if b.name == "Agent" {
				if id, ok := agents[b.id]; ok {
					u.Add(totalAgentUsage(id, subs, map[string]bool{}))
				}
			}
		}
	}
	return u, tools, errs
}

// formatToolArgs is a short one-line summary of a tool call's input.
func formatToolArgs(name string, input map[string]any) string {
	get := func(k string) string {
		s, _ := input[k].(string)
		return s
	}
	switch name {
	case "Bash":
		return get("command")
	case "Read", "Write", "Edit":
		return get("file_path")
	case "Grep", "Glob":
		return get("pattern")
	case "Skill":
		return strings.TrimSpace(get("skill") + " " + get("args"))
	case "Agent":
		return get("description")
	case "WebFetch":
		return get("url")
	case "WebSearch", "ToolSearch":
		return get("query")
	default:
		if len(input) == 0 {
			return ""
		}
		b, _ := json.Marshal(input)
		return string(b)
	}
}
