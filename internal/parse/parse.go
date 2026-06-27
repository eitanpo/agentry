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
	"<task-notification>", // harness-injected background-task event/completion, not a typed prompt
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

// Summarize scans a session JSONL into a lightweight Summary without building
// the full event tree or loading subagents — cheap enough to run over every
// session in a project for `agentry list`. Title reuses the same turn-splitting as
// Load, so it matches the prompt the renderer would show.
func Summarize(jsonlPath string) (model.Summary, error) {
	entries, err := loadEntries(jsonlPath)
	if err != nil {
		return model.Summary{}, err
	}
	stem := strings.TrimSuffix(filepath.Base(jsonlPath), filepath.Ext(jsonlPath))
	start, end := timeRange(entries)
	turns := splitTurns(entries)
	var prompts []string
	for _, tn := range turns {
		if !isClearCmd(tn.prompt) {
			prompts = append(prompts, tn.prompt)
		}
	}
	return model.Summary{
		ID:       stem,
		Start:    start,
		End:      end,
		Title:    sessionTitle(lastTitleOf(entries, "custom-title"), lastTitleOf(entries, "ai-title"), turns),
		Prompts:  prompts,
		NumTurns: len(turns),
		Tools:    toolStats(entries),
		Commands: bashCommands(entries),
	}, nil
}

// bashCommands returns the session's distinct top-level Bash commands in
// first-seen order, the corpus --used-command and --used substring-match.
func bashCommands(entries []entry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_use" || b.name != "Bash" {
				continue
			}
			cmd, _ := b.input["command"].(string)
			if cmd == "" || seen[cmd] {
				continue
			}
			seen[cmd] = true
			out = append(out, cmd)
		}
	}
	return out
}

// toolStats aggregates the session's top-level tool calls by (tool, identity),
// preserving first-seen order so output is stable before the renderer sorts it.
// It counts only the main thread's calls — subagent sidecars are not loaded —
// matching the top-level population of turnMetrics.
func toolStats(entries []entry) []model.ToolStat {
	type key struct{ tool, identity string }
	counts := map[key]int{}
	var order []key
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_use" {
				continue
			}
			k := key{b.name, toolIdentity(b.name, b.input)}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
	}
	out := make([]model.ToolStat, 0, len(order))
	for _, k := range order {
		out = append(out, model.ToolStat{Tool: k.tool, Identity: k.identity, Count: counts[k]})
	}
	return out
}

// toolIdentity is the grouping label for a tool call: the invoked program for
// Bash, the skill for Skill, the subagent type for Agent. Empty for every other
// tool, whose own name is its identity. Field names verified against live logs.
func toolIdentity(name string, input map[string]any) string {
	str := func(k string) string { s, _ := input[k].(string); return s }
	switch name {
	case "Bash":
		return bashProgram(str("command"))
	case "Skill":
		return str("skill")
	case "Agent":
		return str("subagent_type")
	default:
		return ""
	}
}

// bashProgram reduces a shell command to the program a histogram groups by: the
// first token after any leading VAR=value assignments, reduced to its basename
// ("/a/b/exa --x" → "exa"). A heuristic — a pipeline or "cd x && y" reports only
// its first program, which is enough for a usage tally.
func bashProgram(cmd string) string {
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) && isAssignment(fields[i]) {
		i++
	}
	if i >= len(fields) {
		return ""
	}
	return filepath.Base(fields[i])
}

// isAssignment reports whether tok is a leading shell VAR=value assignment (the
// name left of '=' is a non-empty run of identifier characters).
func isAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for _, r := range tok[:eq] {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// lastTitleOf returns the most recent non-empty title carried on entries of the
// given type. ai-title and custom-title both regenerate/rewrite as the session
// evolves, so the last one wins.
func lastTitleOf(entries []entry, typ string) string {
	title := ""
	for _, e := range entries {
		if e.typ == typ && strings.TrimSpace(e.title) != "" {
			title = e.title
		}
	}
	return title
}

// sessionTitle picks a listing title by a fallback ladder: the manual
// custom-title (set by renaming the session) if present, else Claude Code's
// ai-title, else the first turn's prompt skipping a leading /clear (which resets
// context and describes nothing), else the first prompt.
func sessionTitle(customTitle, aiTitle string, turns []rawTurn) string {
	if t := strings.TrimSpace(customTitle); t != "" {
		return t
	}
	if t := strings.TrimSpace(aiTitle); t != "" {
		return t
	}
	for _, t := range turns {
		if !isClearCmd(t.prompt) {
			return t.prompt
		}
	}
	if len(turns) > 0 {
		return turns[0].prompt
	}
	return ""
}

// isClearCmd reports whether a turn prompt is the /clear command. The recorded
// command-name already carries a leading slash, so userPrompt yields "//clear";
// trimming all leading slashes matches regardless of how many there are.
func isClearCmd(prompt string) bool {
	return strings.TrimLeft(strings.TrimSpace(prompt), "/") == "clear"
}

// ── Raw JSONL decoding ───────────────────────────────────────────────────

type entry struct {
	typ        string
	t          time.Time
	model      string
	usage      model.Usage
	title      string  // set on ai-title (aiTitle) and custom-title (customTitle) entries
	contentStr string  // set when message.content is a JSON string
	hasStr     bool    // distinguishes "" content from absent/array content
	blocks     []block // set when message.content is a JSON array
	// toolUseResultAgentID is the structured spawn-child id from the top-level
	// toolUseResult.agentId, set on the user entry carrying an Agent/forked-Skill
	// tool_result. Empty when absent (older logs) or for non-spawning tools.
	toolUseResultAgentID string
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
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	Message       json.RawMessage `json:"message"`
	AiTitle       string          `json:"aiTitle"`       // ai-title entries: Claude Code's own session summary
	CustomTitle   string          `json:"customTitle"`   // custom-title entries: the name set by renaming the session
	ToolUseResult json.RawMessage `json:"toolUseResult"` // structured tool-result mirror; carries agentId for spawn children
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
		e := entry{typ: re.Type, title: re.AiTitle + re.CustomTitle}
		if ts, err := time.Parse(time.RFC3339, re.Timestamp); err == nil {
			e.t = ts
		}
		// toolUseResult is sometimes a structured object (spawn children carry
		// agentId), sometimes a plain string — only the object form has an id.
		if len(re.ToolUseResult) > 0 && re.ToolUseResult[0] == '{' {
			var tur struct {
				AgentID string `json:"agentId"`
			}
			if json.Unmarshal(re.ToolUseResult, &tur) == nil {
				e.toolUseResultAgentID = tur.AgentID
			}
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

// sidecarIDs maps the tool_use ids of spawning calls (the named tool) to their
// subagent log key ("agent-xxx"). It prefers the structured toolUseResult.agentId
// carried on the result entry and falls back to the "agentId: …" line in the
// result text — the only mechanism in pre-structured logs, present on Agent
// results. Restricting to a single tool name keeps an "agentId:" string in some
// unrelated result from being misread as a spawn link.
func sidecarIDs(entries []entry, toolName string) map[string]string {
	tools := map[string]bool{}
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_use" && b.name == toolName {
				tools[b.id] = true
			}
		}
	}
	m := map[string]string{}
	for _, e := range entries {
		if e.typ != "user" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_result" || !tools[b.toolUseID] {
				continue
			}
			id := e.toolUseResultAgentID
			if id == "" {
				if mt := agentIDRe.FindStringSubmatch(b.resultText); mt != nil {
					id = mt[1]
				}
			}
			if id != "" {
				m[b.toolUseID] = "agent-" + id
			}
		}
	}
	return m
}

// agentIDMap maps an Agent tool_use id to its subagent log key.
func agentIDMap(entries []entry) map[string]string { return sidecarIDs(entries, "Agent") }

// skillSidecarMap maps a forked-Skill tool_use id to its subagent log key.
// Inline skills run in the main chain and write no sidecar, so they never appear
// here — leaving attachSubagent to fall back to legacy name matching, then to no
// expansion.
func skillSidecarMap(entries []entry) map[string]string { return sidecarIDs(entries, "Skill") }

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
	skills := skillSidecarMap(entries)
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
				attachSubagent(tool, b, agents, skills, subs, seen)
				out = append(out, model.Event{Kind: model.EventTool, Tool: tool})
			}
		}
	}
	return out
}

// attachSubagent fills tool.Subagent for Agent and forked-Skill calls that
// spawned a sidecar. Agent and forked-Skill links resolve by id (the structured
// agentId, see sidecarIDs); for a Skill with no id link it falls back to matching
// a sidecar by skill name (pre-structured forked logs). An inline skill — which
// runs in the main chain and writes no sidecar — matches nothing and renders as a
// leaf call, its work staying inline in the transcript.
func attachSubagent(tool *model.Tool, b block, agents, skills map[string]string, subs map[string]*subagent, seen map[string]bool) {
	key := ""
	switch b.name {
	case "Agent":
		key = agents[b.id]
	case "Skill":
		key = skills[b.id]
		if key == "" {
			if skill, _ := b.input["skill"].(string); skill != "" {
				for id, s := range subs {
					if s.skillName == skill {
						key = id
						break
					}
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
