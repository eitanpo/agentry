// Package parse turns a session JSONL file (and its subagent sidecar files)
// into the canonical model.Session. The extraction logic is ported from the
// claude-logs-search Python reference, verified against live logs under
// ~/.claude/projects/. The LLM-safety envelope from the reference is
// deliberately omitted — agentry renders for humans, not for re-ingestion.
package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/price"
)

// Only user and assistant entries carry content we render; every other type
// (ai-title, attachment, system, permission-mode, file-history-snapshot,
// progress, queue-operation, last-prompt, …) is ignored.

var agentIDRe = regexp.MustCompile(`agentId:\s*(\S+)`)

// injectedMarkers identify user entries that are system-injected, not typed.
// The wrapper around a typed shell command is deliberately absent: it holds what
// a person pressed, and typedShellCommand reads it as the prompt it is. The two
// wrappers around that command's output stay, since the harness wrote those.
var injectedMarkers = []string{
	"<local-command-caveat>", shellOutOpen, shellErrOpen,
	"Base directory for this skill:", "<local-command-stdout>",
	"<task-notification>", // harness-injected background-task event/completion, not a typed prompt
	// What Claude Code writes where a reply was cut short. It covers the plain
	// form and the "for tool use" one, and reading either as a prompt would open
	// a turn out of somebody pressing escape.
	"[Request interrupted by user",
}

// Load parses the session at jsonlPath into a Session.
func Load(jsonlPath string) (*model.Session, error) {
	entries, err := loadEntries(jsonlPath)
	if err != nil {
		return nil, err
	}

	stem := strings.TrimSuffix(filepath.Base(jsonlPath), filepath.Ext(jsonlPath))
	subs := loadSubagents(subagentDir(jsonlPath))
	nameLinks := sidecarNameLinks(entries, subs)
	eps := entrypoints(entries)
	effs := efforts(entries)
	ms := models(entries)
	cost, added, removed := recordedTotals(entries)
	turns := splitTurns(entries)
	cwd := sessionCwd(entries)
	scans := scansOfSubagents(subs, nameLinks, firstStamp(entries))
	typedPerTurn := typedCommandForks(turns, scans, spawnedSidecars(entries, scans))
	typedNames := skillsInTurnOrder(typedPerTurn)

	sess := &model.Session{
		Meta: model.Meta{
			ID:           stem,
			Model:        lastOf(ms),
			Models:       manyOrNone(ms),
			NumSubagents: len(subs),
			NumTurns:     len(turns),
			Entrypoint:   lastOf(eps),
			Entrypoints:  manyOrNone(eps),
			Effort:       lastOf(effs),
			Efforts:      manyOrNone(effs),
			CostUSD:      cost,
			LinesAdded:   added,
			LinesRemoved: removed,
			PRs:          sessionPRs(entries),
			Artifacts:    sessionArtifacts(entries),
			// The footer's own facts, read by the same helpers the listing uses so
			// that one session's files and tally cannot differ between the two
			// surfaces. DailyActivity carries the header's active time as well as the
			// day-by-day rows.
			Title:         sessionTitle(lastTitleOf(entries, manualTitleTypes...), lastTitleOf(entries, "ai-title"), turns),
			Cwd:           cwd,
			Path:          jsonlPath,
			RootUUID:      rootUUID(entries),
			Files:         sessionFiles(entries, cwd),
			DailyActivity: dailyActivity(turns, firstStamp(entries)),
			Tools:         toolStats(entries, typedNames),
			Failures:      failureStats(entries),
			Denials:       denialStats(entries),
		},
	}
	sess.Meta.Start, sess.Meta.End = timeRange(entries)

	daily := groupDaily(labelledRecords(entries, scans, firstStamp(entries)))
	for _, d := range daily {
		sess.Meta.Usage.Add(d.Usage)
	}
	sess.Meta.DailyUsage = daily
	sess.Meta.CacheSaving = cacheSaving(daily)

	forksPerTurn := unclaimedForks(turns, entries, subs, nameLinks)
	for i, t := range turns {
		turn := model.Turn{
			Number: i + 1,
			Prompt: t.prompt,
			Start:  t.start,
			End:    t.end,
			Events: buildEvents(t.entries, subs, nameLinks, map[string]bool{}),
		}
		// The fork leads the turn and the printed reply follows it, the order a
		// turn that called a tool and then answered already reads in.
		turn.Events = append(forkEvents(typedPerTurn[i], subs, nameLinks, localCommandOutputs(t.entries)), turn.Events...)
		tm := turnMetrics(t, subs, nameLinks, forksPerTurn[i], len(typedPerTurn[i]))
		turn.Usage, turn.ToolCount, turn.ErrorCount, turn.CostUSD = tm.usage, tm.tools, tm.errors, tm.costUSD
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
	eps := entrypoints(entries)
	effs := efforts(entries)
	ms := models(entries)
	cwd := sessionCwd(entries)
	cost, added, removed := recordedTotals(entries)
	scans := readSidecarScans(jsonlPath, firstStamp(entries))
	typed := typedCommandForks(turns, scans, spawnedSidecars(entries, scans))
	daily, usage := sessionSpend(entries, scans)
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
		Title:    sessionTitle(lastTitleOf(entries, manualTitleTypes...), lastTitleOf(entries, "ai-title"), turns),
		Prompts:  prompts,
		NumTurns: len(turns),
		Tools:    toolStats(entries, skillsInTurnOrder(typed)),
		Commands: bashCommands(entries),
		Replies:  replyTexts(entries),
		RootUUID: rootUUID(entries),
		Cwd:      cwd,
		Files:    sessionFiles(entries, cwd),
		Failures: failureStats(entries),
		Denials:  denialStats(entries),
		Born:     fileBorn(jsonlPath),
		// The last value is the session's, matching the last-activity time the
		// listing orders by. The full list is kept only when it diverges, so a
		// single-entrypoint session serializes one field rather than two.
		Entrypoint:    lastOf(eps),
		Entrypoints:   manyOrNone(eps),
		Model:         lastOf(ms),
		Models:        manyOrNone(ms),
		Effort:        lastOf(effs),
		Efforts:       manyOrNone(effs),
		Usage:         usage,
		DailyUsage:    daily,
		CacheSaving:   cacheSaving(daily),
		DailyActivity: dailyActivity(turns, firstStamp(entries)),
		CostUSD:       cost,
		LinesAdded:    added,
		LinesRemoved:  removed,
		PRs:           sessionPRs(entries),
		Artifacts:     sessionArtifacts(entries),
	}, nil
}

// SummarizeAll summarizes every named session in the order given, skipping any
// that will not parse — the same skip a caller makes on a malformed line, and the
// reason this returns no error: one unreadable log must not cost the caller the
// other six hundred.
//
// Parallel because the sweep is where a cross-project answer spends its time.
// Each session is an independent read of its own files, Summarize shares no state
// between calls, and every goroutine writes one slot of its own — so the only
// ordering this has to preserve is the caller's, which it does by index rather
// than by arrival.
func SummarizeAll(paths []string) []model.Summary {
	if len(paths) == 0 {
		return nil
	}
	got := make([]model.Summary, len(paths))
	parsed := make([]bool, len(paths))
	workers := runtime.NumCPU()
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if s, err := Summarize(paths[i]); err == nil {
					got[i], parsed[i] = s, true
				}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := make([]model.Summary, 0, len(paths))
	for i, ok := range parsed {
		if ok {
			out = append(out, got[i])
		}
	}
	return out
}

// subagentDir is where a session's subagent sidecars live, next to its own log.
// Named once because Load and Summarize must look in the same place: a listing
// that read a different directory would report a different cost for the session
// the render path is showing.
func subagentDir(jsonlPath string) string {
	stem := strings.TrimSuffix(filepath.Base(jsonlPath), filepath.Ext(jsonlPath))
	return filepath.Join(filepath.Dir(jsonlPath), stem, "subagents")
}

// sessionSpend totals a session's tokens the way Load builds Meta.Usage — the
// main thread plus every subagent sidecar — and returns the same responses split
// by model and local day. The total is the sum of the split rather than a second
// reading of the log, so the two cannot drift apart.
//
// Summarize is otherwise deliberately cheap, and this is the one place it opens
// files the main log does not name — sidecars can be a large share of a
// project tree's bytes. It is paid anyway, because a tally that silently
// dropped delegated work would answer the cost question wrong for exactly the
// sessions that cost the most. The split itself adds arithmetic and no file
// reads.
//
// One tally per file, not one for the session: a request id is unique to its own
// log, and a shared tally would let a sidecar's response displace a main-thread
// response that happened to key the same, dropping tokens the session spent.
func sessionSpend(entries []entry, scans map[string]sidecarScan) ([]model.DailyUsage, model.Usage) {
	daily := groupDaily(labelledRecords(entries, scans, firstStamp(entries)))
	var total model.Usage
	for _, d := range daily {
		total.Add(d.Usage)
	}
	return daily, total
}

// readSidecarScans reads every sidecar beside a session log. Summarize is
// otherwise deliberately cheap, and this is the one place it opens files the
// main log does not name — sidecars can be a large share of a project tree's
// bytes. It is paid anyway, because a tally that silently dropped delegated
// work would answer the cost question wrong for exactly the sessions that cost
// the most.
func readSidecarScans(jsonlPath string, fallback time.Time) map[string]sidecarScan {
	paths, _ := filepath.Glob(filepath.Join(subagentDir(jsonlPath), "agent-*.jsonl"))
	scans := make(map[string]sidecarScan, len(paths))
	for _, p := range paths {
		scans[strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))] = readSidecar(p, fallback)
	}
	return scans
}

// scansOfSubagents is readSidecarScans' counterpart for Load, which has already
// parsed every sidecar and must not open them a second time. Both produce the
// same shape, so everything downstream reads one session identically whichever
// path built it.
func scansOfSubagents(subs map[string]*subagent, nameLinks map[string]string, fallback time.Time) map[string]sidecarScan {
	scans := make(map[string]sidecarScan, len(subs))
	for key, sub := range subs {
		scans[key] = sidecarScan{
			records:  mainTally(sub.entries, fallback).records(),
			children: sidecarChildren(sub.entries, nameLinks),
			skill:    sub.forkedSkill,
			promptID: sub.promptID,
			start:    firstStamp(sub.entries),
		}
	}
	return scans
}

// labelledRecords is every response the session spent tokens on, the main
// thread's and every sidecar's, each named by the delegation it was spent under.
// An agent axis whose every row reads "main" says nothing about where a session's
// money went, and the listing and the render have to name one session's spend
// identically, so both reach this through the same scans.
func labelledRecords(entries []entry, scans map[string]sidecarScan, fallback time.Time) []usageRecord {
	recs := mainTally(entries, fallback).records()
	labels := agentLabels(entries)
	nameUnclaimedForks(labels, scans)
	spreadAgentLabels(labels, scans)
	for key, scan := range scans {
		label := labels[key]
		if label == "" {
			label = unattributedAgent
		}
		for i := range scan.records {
			scan.records[i].agent = label
		}
		recs = append(recs, scan.records...)
	}
	return recs
}

// sidecarChildren names the sidecars one already-parsed subagent log spawned —
// the set readSidecar collects while scanning the file, recovered here from
// entries Load has already read rather than by opening them again.
func sidecarChildren(entries []entry, nameLinks map[string]string) []string {
	var out []string
	for _, spawns := range []map[string]string{agentIDMap(entries), skillSidecarMap(entries)} {
		for _, fileKey := range spawns {
			out = append(out, fileKey)
		}
	}
	// A forked skill a subagent started of its own accord is paired to its call
	// by name rather than by a structured id, and that pairing marks the log
	// claimed. Reading only the structured maps here left such a log claimed and
	// unreachable at once, so nothing charged it to anything, silently losing a
	// nested fork's tokens. nameLinks is keyed across every log in the session,
	// so only calls this log made may take from it.
	for _, e := range entries {
		for _, b := range e.blocks {
			if b.typ != "tool_use" {
				continue
			}
			if key, ok := nameLinks[b.id]; ok {
				out = append(out, key)
			}
		}
	}
	return out
}

// cacheSaving prices the split for what caching took off the session, nil where no
// response carried a model agentry holds a price for — a gap in the price table
// rather than a session caching saved nothing on.
func cacheSaving(daily []model.DailyUsage) *model.CacheSaving {
	c, ok := price.Saving(daily)
	if !ok {
		return nil
	}
	return &c
}

// unattributedAgent labels a sidecar that no spawn record points at and whose
// own opening entry names no skill either, which is what is left once
// spreadAgentLabels has followed the chains and nameUnclaimedForks has read the
// forks' own markers. Naming the tokens keeps the agent axis summing to the
// session total.
const unattributedAgent = "(unattributed)"

// agentLabels maps each subagent sidecar's file key ("agent-xxx") to the name of
// the delegation that spawned it. Both spawning tools are read, since a forked
// skill costs money under its own name exactly as a named agent does.
func agentLabels(entries []entry) map[string]string {
	ident := map[string]string{}
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_use" && (b.name == "Agent" || b.name == "Skill") {
				ident[b.id] = spawnLabel(b.name, toolIdentity(b.name, b.input))
			}
		}
	}
	out := map[string]string{}
	for _, spawns := range []map[string]string{agentIDMap(entries), skillSidecarMap(entries)} {
		for toolUseID, fileKey := range spawns {
			if label := ident[toolUseID]; label != "" {
				out[fileKey] = label
			}
		}
	}
	return out
}

// typedCommandSkills names the skill of every fork a caller started by typing
// its slash command, in the order those prompts were typed. Three conditions,
// all required, and the third is what makes the set countable as invocations.
//
// The log has to name the sidecar nowhere — no spawning call in the main log,
// no parent sidecar listing it as a child. The sidecar's own opening entry has
// to name a skill, which is what a forked skill's log begins with. And the
// prompt it was forked under has to be that skill's slash command.
//
// Without the third condition the set also holds forked Skill calls in logs
// written before the structured link existed, which a name pairing claims and
// which the tally therefore already counts through their call, so counting
// them here would report each of them twice. Their prompts are ordinary prose,
// so the condition excludes every one.
//
// An alias typed for a skill under another name is not counted, since nothing
// in the log relates the two. That undercounts where the cost axis does not,
// and the axis is right to be laxer: naming money spent needs no invocation.
func typedCommandForks(turns []rawTurn, scans map[string]sidecarScan, spawned map[string]bool) [][]typedFork {
	keys := make([]string, 0, len(scans))
	for key, scan := range scans {
		if spawned[key] || scan.skill == "" || scan.promptID == "" {
			continue
		}
		keys = append(keys, key)
	}
	// Sorted so two runs over one log produce the same order, which iterating
	// the map would not.
	sort.Strings(keys)
	byPrompt := turnOfPrompt(turns)
	out := make([][]typedFork, len(turns))
	for _, key := range keys {
		scan := scans[key]
		i := ownerTurn(turns, byPrompt, scan.promptID, scan.start)
		if i < 0 || slashCommandName(turns[i].prompt) != scan.skill {
			continue
		}
		out[i] = append(out[i], typedFork{key: key, skill: scan.skill})
	}
	return out
}

// typedFork is one log a typed slash command started: the sidecar holding it,
// and the skill that ran.
type typedFork struct{ key, skill string }

// turnOfPrompt indexes the turns by the prompt id each was submitted under,
// keeping the earliest where two share one.
//
// A prompt id does not identify a turn. Claude Code writes the same one on a
// typed command and on a shell command queued behind it, so two turns answer to
// it, and charging both would double-count a forked skill's call: once in the
// tools tally and once in its output tokens. The earliest of the two is the
// submission that actually started the fork.
func turnOfPrompt(turns []rawTurn) map[string]int {
	out := map[string]int{}
	for i, t := range turns {
		if t.promptID == "" {
			continue
		}
		if _, taken := out[t.promptID]; !taken {
			out[t.promptID] = i
		}
	}
	return out
}

// ownerTurn is the turn a forked log belongs to, or -1 where none does.
//
// The prompt id decides it wherever the main log records that submission. Where
// it does not — a sidecar can be stamped with an id no user entry in the main
// log carries — the fork falls to the last turn that had already started when
// it began, which is the turn it ran inside. A log carrying no
// prompt id at all is placed nowhere: nothing ties it to a turn, and landing it
// on whichever turn spans the gap would invent an attribution rather than find
// one.
func ownerTurn(turns []rawTurn, byPrompt map[string]int, promptID string, start time.Time) int {
	if promptID == "" {
		return -1
	}
	if i, ok := byPrompt[promptID]; ok {
		return i
	}
	owner := -1
	for i, t := range turns {
		if !t.start.After(start) {
			owner = i
		}
	}
	if owner < 0 && len(turns) > 0 {
		return 0
	}
	return owner
}

// skillsInTurnOrder flattens the typed commands into the order they were typed,
// which is the order the tally lists them in. A map would otherwise hand two
// runs over one log two different orders.
func skillsInTurnOrder(perTurn [][]typedFork) []string {
	var out []string
	for _, forks := range perTurn {
		for _, f := range forks {
			out = append(out, f.skill)
		}
	}
	return out
}

// slashCommandName is the command a prompt invokes, empty when the prompt is
// not one. The name is the first word after the slash; everything following it
// is the command's arguments.
func slashCommandName(prompt string) string {
	if !strings.HasPrefix(prompt, "/") {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(prompt, "/"), " ")
	return strings.TrimSpace(name)
}

// spawnedSidecars names every sidecar some record in the session points at —
// a spawning call in the main log, or a parent sidecar naming it as its child.
func spawnedSidecars(entries []entry, scans map[string]sidecarScan) map[string]bool {
	out := make(map[string]bool, len(scans))
	for _, spawns := range []map[string]string{agentIDMap(entries), skillSidecarMap(entries)} {
		for _, key := range spawns {
			out[key] = true
		}
	}
	for _, scan := range scans {
		for _, child := range scan.children {
			out[child] = true
		}
	}
	return out
}

// nameUnclaimedForks names each sidecar that no spawn record in the session
// points at with the skill its own log declares. A slash command the caller
// typed forks exactly as a Skill call does and writes its log the same way, but
// nothing in the main log is a tool call, so every map keyed on one misses it
// and its tokens land on the unattributed row — which names no skill anyone can
// decide to stop running.
//
// It runs before spreadAgentLabels and skips a sidecar some log spawned, so a
// nested skill keeps taking the name of the call the session itself made rather
// than its own. What is left is the fork nobody claims, whose only naming is the
// marker line Claude Code wrote into it.
func nameUnclaimedForks(labels map[string]string, scans map[string]sidecarScan) {
	spawned := make(map[string]bool, len(scans))
	for _, scan := range scans {
		for _, child := range scan.children {
			spawned[child] = true
		}
	}
	for key, scan := range scans {
		if labels[key] != "" || spawned[key] || scan.skill == "" {
			continue
		}
		labels[key] = spawnLabel("Skill", scan.skill)
	}
}

// spreadAgentLabels carries each delegation's name down the chain it started, so
// a subagent that delegates again is charged to the call the session itself made
// rather than to a row naming something the session never invoked. What a reader
// can act on is the delegation they chose; the depth below it is that choice's
// cost, not a separate one.
//
// It runs to a fixed point because a chain can be deeper than two, and the scans
// are a map with no ordering to rely on. A cycle cannot extend it: a label is
// written once and only to a child that has none.
func spreadAgentLabels(labels map[string]string, scans map[string]sidecarScan) {
	for changed := true; changed; {
		changed = false
		for parent, scan := range scans {
			label := labels[parent]
			if label == "" {
				continue
			}
			for _, child := range scan.children {
				if labels[child] == "" {
					labels[child] = label
					changed = true
				}
			}
		}
	}
}

// spawnLabel names one delegation on the agent axis. A skill keeps the leading
// slash it is invoked by, which is what tells a reader that a row is a skill and
// not an agent type in a list that mixes both.
func spawnLabel(tool, identity string) string {
	if identity == "" {
		return ""
	}
	if tool == "Skill" {
		return "/" + identity
	}
	return identity
}

// dailyActivity counts each day's turns and the seconds they ran for, bucketed
// by the day a turn started on. A turn spanning midnight is counted whole on the
// day it began, since a turn is the unit being counted and splitting one would
// invent a fraction the log does not record.
func dailyActivity(turns []rawTurn, fallback time.Time) []model.DailyActivity {
	idx := map[string]int{}
	var out []model.DailyActivity
	for _, tn := range turns {
		day := dayOf(tn.start, fallback)
		i, ok := idx[day]
		if !ok {
			i = len(out)
			idx[day] = i
			out = append(out, model.DailyActivity{Day: day})
		}
		out[i].Turns++
		out[i].ActiveSeconds += activeSeconds(tn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// maxIdleGap bounds how much of one silence between entries counts as work. The
// log cannot tell a ten-minute test run from a person who walked away mid-turn:
// both are one gap between two timestamps. Left unbounded the measure collapses
// — a single turn left open for days can dominate a whole month's supposed
// activity. Capping each gap bounds the error in the direction that
// understates, which is the safe direction for a figure dollars are divided by.
const maxIdleGap = 5 * time.Minute

// activeSeconds is how long one turn worked: from its prompt to the last thing
// the assistant said in it, with every silence longer than maxIdleGap counted as
// maxIdleGap.
//
// It stops at the last assistant entry rather than at rawTurn.end, which is the
// last entry of any type. A session resumed weeks later writes bookkeeping
// entries — a cost record, a file-history snapshot — that the splitter files
// under the turn still open, and measuring to those reads a day of absence as a
// day of work.
func activeSeconds(tn rawTurn) int {
	last := -1
	for i, e := range tn.entries {
		if e.typ == "assistant" && !e.t.IsZero() {
			last = i
		}
	}
	if last < 0 || tn.start.IsZero() {
		return 0
	}
	total := time.Duration(0)
	prev := tn.start
	for _, e := range tn.entries[:last+1] {
		if e.t.IsZero() {
			continue
		}
		if gap := e.t.Sub(prev); gap > 0 {
			if gap > maxIdleGap {
				gap = maxIdleGap
			}
			total += gap
		}
		prev = e.t
	}
	return int(total.Seconds())
}

// usageOnly is the slice of an entry a token tally needs. Sidecars are read
// through it rather than through loadEntries, which would also decode every
// content block on the way, adding real time to a cross-project listing.
// Reading each sidecar's own delegation links in the same pass adds a further
// share of that cost, which is what buys the agent axis its chains. An
// unreadable file or a malformed line is skipped rather than
// raised — it undercounts the tally, where an error would drop the whole
// session from the listing over a subagent's log.
type usageOnly struct {
	Type string `json:"type"`
	// Timestamp and the message's model are what place a delegated response in a
	// day bucket and at a rate. A subagent is priced by the model that answered
	// inside it, which is not always the session's own.
	Timestamp string `json:"timestamp"`
	// RequestID and UUID are what usageKey groups by, so a sidecar's tokens are
	// deduplicated on the same rule as the main log's rather than counting a
	// delegated reply once per content block.
	RequestID string `json:"requestId"`
	UUID      string `json:"uuid"`
	// PromptID pairs the log with the prompt that forked it, which is what a
	// typed slash command leaves in place of a spawning call.
	PromptID string `json:"promptId"`
	// ToolUseResult is raw because a result is an object on a delegation and a
	// plain string on plenty of other tools; decoding it as an object would fail
	// the whole line and lose the usage beside it.
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Message       struct {
		Model string   `json:"model"`
		Usage rawUsage `json:"usage"`
		// Content is raw because it is a string on the entry carrying the skill
		// marker and an array on every other; decoding it as a string would fail
		// the whole line and lose the usage beside it.
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// sidecarScan is one subagent log read once: the responses it holds, the keys of
// the logs it delegated to in turn, and the skill its own first entry names.
type sidecarScan struct {
	records  []usageRecord
	children []string
	// skill is what the log says about itself, which is the only naming available
	// for a fork no spawn record points at. Empty on a subagent log, which names
	// an agent type rather than a skill.
	skill string
	// promptID is the prompt this log was forked under. It pairs the log with the
	// turn that started it, which is the only pairing a typed slash command
	// leaves behind.
	promptID string
	// start is when the log's first entry was written, which places a fork whose
	// prompt id names a submission the main log does not record.
	start time.Time
}

// readSidecar tallies one subagent log and notes every log it spawned in turn,
// in a single pass. The two are read together because a second pass would double
// the file reads this function exists to keep cheap, and because a delegation
// chain cannot be labelled until every link in it has been seen.
//
// The spawning call is left unnamed here: a nested delegation is attributed to
// the delegation the session itself made, so what this needs from a child is its
// key, never its own type.
func readSidecar(path string, fallback time.Time) sidecarScan {
	var out sidecarScan
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	var t usageTally
	first := true
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // sidecars hold large tool results too
	for sc.Scan() {
		var re usageOnly
		if json.Unmarshal(sc.Bytes(), &re) != nil {
			continue
		}
		if re.Type == "assistant" {
			ts, _ := time.Parse(time.RFC3339, re.Timestamp)
			t.add(usageKey(re.RequestID, re.UUID), usageRecord{
				day: dayOf(ts, fallback), model: re.Message.Model,
				usage: re.Message.Usage.tally(),
			})
		}
		if id := sidecarAgentID(re.ToolUseResult); id != "" {
			out.children = append(out.children, "agent-"+id)
		}
		// First line only, and tested on the raw bytes before the content is
		// decoded: the marker names the log when the log opens with it, but a
		// sidecar that loaded a skill partway through is named for its agent
		// type rather than that skill.
		if first {
			first = false
			out.promptID = re.PromptID
			out.start, _ = time.Parse(time.RFC3339, re.Timestamp)
			if bytes.Contains(sc.Bytes(), []byte(skillMarker)) {
				var text string
				if json.Unmarshal(re.Message.Content, &text) == nil {
					out.skill = skillFromText(text)
				}
			}
		}
	}
	out.records = t.records()
	return out
}

// sidecarAgentID reads the spawned log's id out of a toolUseResult, which is an
// object on a delegation and something else entirely on every other tool.
//
// Inside a sidecar the field needs no further guard. A subagent log names itself
// with a top-level agentId on every entry, never one nested here, so an agentId
// in this position is always a log this one spawned — including a skill forked
// into the background, whose result carries the link and no tool_result block to
// match it against.

func sidecarAgentID(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '{' {
		return ""
	}
	var r struct {
		AgentID string `json:"agentId"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return r.AgentID
}

// entrypoints returns every distinct entrypoint the session carries, in
// first-seen order. Meta entries omit the field, so an absent value is skipped
// rather than recorded as a change. A session resumed in another client carries
// two — observed as contiguous blocks, never interleaved.
func entrypoints(entries []entry) []string {
	return distinct(entries, func(e entry) string { return e.entrypoint })
}

// efforts returns every distinct reasoning effort the session carries, in
// first-seen order. Only assistant entries have the field, so an absent value is
// skipped rather than recorded as a change — otherwise every user turn would
// read as effort being switched off and back on.
func efforts(entries []entry) []string {
	return distinct(entries, func(e entry) string { return e.effort })
}

// distinct collects the non-empty values of one per-entry setting in first-seen
// order. Entrypoint and effort are the same shape of fact — absent on some entry
// types, occasionally changed mid-session — and both resolve as "last wins" with
// the full list kept only when it diverges, so they share the reading.
func distinct(entries []entry, field func(entry) string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		v := field(e)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func lastOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// manyOrNone returns s only when it holds more than one value, so the common
// single-entrypoint case does not repeat what Entrypoint already says.
func manyOrNone(s []string) []string {
	if len(s) < 2 {
		return nil
	}
	return s
}

// sessionCwd is the working directory the session ran in — the first non-empty
// cwd any entry carries. Meta entries (ai-title, agent-name, …) omit the field,
// so taking the first entry's value unconditionally would report none.
func sessionCwd(entries []entry) string {
	for _, e := range entries {
		if e.cwd != "" {
			return e.cwd
		}
	}
	return ""
}

// rootUUID is the uuid of the first entry that carries one — the conversation
// root. A fork copies its parent's chain verbatim, root entry included, so
// sessions sharing a root uuid are one fork family. /clear starts an empty
// session, so its root uuid is fresh and it does not join the parent's family.
func rootUUID(entries []entry) string {
	for _, e := range entries {
		if e.uuid != "" {
			return e.uuid
		}
	}
	return ""
}

// fileBorn is the session file's creation time, used to order a fork family
// (earliest = original). A fork is a new file written at fork time, so its
// birthtime exceeds the original's — a signal the fork cannot forge, unlike the
// in-content timestamps it copies. Off macOS, where creation time is not
// portably readable, it falls back to the modification time.
func fileBorn(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	if bt, ok := fileBirthtime(fi); ok {
		return bt
	}
	return fi.ModTime()
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

// replyTexts returns the main thread's assistant text blocks in order — the
// corpus --reply-matches tests. One entry per block rather than one joined
// string, so a pattern's ^ and $ anchor to a single reply.
//
// Thinking blocks are excluded because reasoning is not a reply: a rule about
// what a reply said must not be satisfied by a thought the user never saw.
// Blank blocks are dropped on the same rule buildEvents applies, so the render
// path and the filter agree on which blocks are replies at all.
func replyTexts(entries []entry) []string {
	var out []string
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "text" && strings.TrimSpace(b.text) != "" {
				out = append(out, b.text)
			}
		}
	}
	return out
}

// toolStats aggregates the session's top-level tool calls by (tool, identity),
// preserving first-seen order so output is stable before the renderer sorts it.
// It counts only the main thread's calls — subagent sidecars are not loaded —
// matching the top-level population of turnMetrics.
// toolStats counts a session's top-level calls by identity. typed names the
// skills a caller invoked by typing their slash commands, which make no tool
// call for the loop below to see; they are counted here so that a skill the
// transcript shows expanding is also a row in the tally, and so that selecting
// sessions by the skill they used finds the ones that typed it.
func toolStats(entries []entry, typed []string) []model.ToolStat {
	type key struct{ tool, identity string }
	counts := map[key]int{}
	var order []key
	bump := func(k key) {
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_use" {
				continue
			}
			bump(key{b.name, toolIdentity(b.name, b.input)})
		}
	}
	// After the calls the model made, since a typed command sits outside their
	// order: it belongs to a prompt rather than to a position in a reply.
	for _, skill := range typed {
		bump(key{"Skill", skill})
	}
	out := make([]model.ToolStat, 0, len(order))
	for _, k := range order {
		out = append(out, model.ToolStat{Tool: k.tool, Identity: k.identity, Count: counts[k]})
	}
	return out
}

// toolIdentity is the grouping label for a tool call: the invoked program for
// Bash, the skill for Skill, the subagent type for Agent, the target file for
// Edit and Write. Empty for every other tool, whose own name is its identity.
// Field names verified against live logs.
//
// Read also carries file_path but is deliberately left without an identity: the
// question these labels answer is what a session changed, and a Read tally by
// path would be the largest group in most sessions while changing nothing.
func toolIdentity(name string, input map[string]any) string {
	str := func(k string) string { s, _ := input[k].(string); return s }
	switch name {
	case "Bash":
		return bashProgram(str("command"))
	case "Skill":
		return str("skill")
	case "Agent":
		return str("subagent_type")
	case "Edit", "Write":
		return str("file_path")
	default:
		return ""
	}
}

// toolModel returns the model a call delegated to, or "" when it named none.
// Read from any tool's input rather than gated on the name: `Agent` is the only
// tool that carries the field today, and a later tool carrying it would mean
// the same thing.
func toolModel(input map[string]any) string {
	s, _ := input["model"].(string)
	return s
}

// toolPrompt returns the instruction a delegated call was handed, or "" when the
// tool is not one that delegates. Gated on the name where toolModel is not,
// because "prompt" does not mean one thing across tools: docs/session-format.md
// records it on an Agent call's input as the brief the subagent runs on, and a
// tool that passes a prompt to something other than a delegated run would be
// answering a different question under the same key.
func toolPrompt(name string, input map[string]any) string {
	if name != "Agent" {
		return ""
	}
	s, _ := input["prompt"].(string)
	return s
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
func lastTitleOf(entries []entry, typ ...string) string {
	title := ""
	for _, e := range entries {
		if !slices.Contains(typ, e.typ) || strings.TrimSpace(e.title) == "" {
			continue
		}
		title = e.title
	}
	return title
}

// manualTitleTypes are the two entries that record a name the user chose: a
// custom-title from renaming the session, an agent-name from --name or /rename.
// Neither outranks the other by kind, so lastTitleOf takes whichever the log
// records last — these entries carry no timestamp, so file order is the only
// ordering there is.
var manualTitleTypes = []string{"custom-title", "agent-name"}

// sessionTitle picks a listing title by a fallback ladder: a manual title the
// user chose (see manualTitleTypes) if present, else Claude Code's ai-title,
// else the first turn's prompt skipping a leading /clear (which resets context
// and describes nothing), else the first prompt.
func sessionTitle(manualTitle, aiTitle string, turns []rawTurn) string {
	if t := strings.TrimSpace(manualTitle); t != "" {
		return t
	}
	if t := strings.TrimSpace(aiTitle); t != "" {
		return t
	}
	for _, t := range turns {
		if !isClearCmd(t.prompt) && !isTypedShell(t.prompt) {
			return t.prompt
		}
	}
	if len(turns) > 0 {
		return turns[0].prompt
	}
	return ""
}

// isTypedShell reports whether a turn prompt is a shell command the caller ran
// with "!", which names what someone ran rather than what the session was for.
// It matches the prefix userPrompt writes; a prompt a person typed beginning
// with the same two characters would be skipped too, which costs that session
// its next prompt as a title and no more.
func isTypedShell(prompt string) bool {
	return strings.HasPrefix(prompt, shellPromptPrefix)
}

// isClearCmd reports whether a turn prompt is the /clear command. userPrompt
// renders it with one leading slash ("/clear"), but trimming all leading slashes
// also matches an older "//clear" rendering and a bare "clear".
//
// "/clear <text>" — Claude Code records same-line text after /clear as the
// command's arguments — still counts: the clear resets context and the trailing
// text describes the reset, not the session. The leading-slash check (s != rest)
// keeps prose like "clear the table" from being misread as a reset.
func isClearCmd(prompt string) bool {
	s := strings.TrimSpace(prompt)
	rest := strings.TrimLeft(s, "/")
	return rest == "clear" || (s != rest && strings.HasPrefix(rest, "clear "))
}

// ── Raw JSONL decoding ───────────────────────────────────────────────────

type entry struct {
	typ        string
	t          time.Time
	uuid       string
	model      string
	usage      model.Usage
	title      string  // set on ai-title (aiTitle), custom-title (customTitle) and agent-name (agentName) entries
	contentStr string  // set when message.content is a JSON string
	hasStr     bool    // distinguishes "" content from absent/array content
	blocks     []block // set when message.content is a JSON array
	// toolUseResultAgentID is the structured spawn-child id from the top-level
	// toolUseResult.agentId, set on the user entry carrying an Agent/forked-Skill
	// tool_result. Empty when absent (older logs) or for non-spawning tools.
	toolUseResultAgentID string
	// isCompactSummary marks the user entry Claude Code writes at a compaction
	// boundary, whose content is the summary rather than anything typed.
	isCompactSummary bool
	// cwd is the working directory the session ran in. Meta entries omit it, so
	// the session's value is the first non-empty one.
	cwd string
	// entrypoint is where the session was run. Meta entries omit it, and a
	// session resumed elsewhere carries two values in contiguous blocks.
	entrypoint string
	// effort is the reasoning effort, carried on assistant entries only. Absent
	// on sessions predating Claude Code 2.1.212, and it can change mid-session.
	effort string
	// denialKind is why a call was refused, on the user entry carrying its
	// tool_result. Empty on every other entry and on results that ran.
	denialKind string
	// trackingPath (file-history-delta) and trackedPaths (file-history-snapshot)
	// are Claude Code's own record of which files changed, in the log's own mix of
	// repo-relative and absolute forms — sessionFiles resolves them.
	trackingPath string
	trackedPaths []string
	// pr and frame carry the payloads of the two entries recording what a session
	// produced. Each is filled only on its own entry type, so the frame's title
	// cannot be mistaken for the session title the three title entries carry.
	pr    model.PR
	frame model.Artifact
	// requestID names the API response this assistant entry came from, the key a
	// token tally groups by. Empty on non-assistant entries and on older logs.
	requestID string
	// promptID names the prompt submission an entry belongs to. Claude Code writes
	// it on user entries only, and writes the same value into the first entry of a
	// log that prompt forked — the one link between a typed slash command and the
	// session it spawned, since a typed command makes no tool call.
	promptID string
	// turnCompanion marks harness-attached material filed as a user entry.
	turnCompanion bool
	// harnessReminder marks a user entry the harness attached to a tool call —
	// a skill body, a re-invocation notice — which older logs file under the
	// user's name with no turnCompanion flag.
	harnessReminder bool
	// localCommandOutput is what a local_command system entry printed, unwrapped
	// from its marker. Empty on every other entry. A slash command the caller
	// typed leaves this and nothing else in the main log, so it is that turn's
	// whole visible reply.
	localCommandOutput string
	// cost is the running totals a cost-state entry carries. Nil on every other
	// entry type, which is what makes "the log recorded none" a single check.
	cost *costState
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
	UUID          string          `json:"uuid"` // entry id; the first one is the conversation root (fork-family key)
	Message       json.RawMessage `json:"message"`
	AiTitle       string          `json:"aiTitle"`       // ai-title entries: Claude Code's own session summary
	CustomTitle   string          `json:"customTitle"`   // custom-title entries: the name set by renaming the session
	AgentName     string          `json:"agentName"`     // agent-name entries: the name set by --name or /rename
	Cwd           string          `json:"cwd"`           // working directory the session ran in
	Entrypoint    string          `json:"entrypoint"`    // where the session was run: cli, claude-desktop, sdk-cli
	Effort        string          `json:"effort"`        // reasoning effort, on assistant entries: low, high, xhigh, …
	ToolUseResult json.RawMessage `json:"toolUseResult"` // structured tool-result mirror; carries agentId for spawn children
	// ToolDenialKind is why a call was refused, on the user entry carrying its
	// tool_result. Not to be confused with permission-mode entries, which record
	// the mode in effect and never a per-call decision.
	ToolDenialKind string `json:"toolDenialKind"`
	// TrackingPath is the file a file-history-delta entry records a change to,
	// and Snapshot the cumulative tracked set on a file-history-snapshot entry.
	// Both are Claude Code's own record of what changed, independent of tools.
	TrackingPath string       `json:"trackingPath"`
	Snapshot     *rawSnapshot `json:"snapshot"`
	// pr-link and frame-link payloads: what the session produced. Title is
	// frame-link's own field and unrelated to aiTitle/customTitle/agentName, which
	// is why it is not folded into the title trio above.
	PRNumber     int    `json:"prNumber"`
	PRURL        string `json:"prUrl"`
	PRRepository string `json:"prRepository"`
	FrameURL     string `json:"frameUrl"`
	FramePath    string `json:"path"`
	FrameTitle   string `json:"title"`
	// IsCompactSummary flags the compaction-boundary user entry. Absent in logs
	// written before Claude Code added it, hence the text fallback in userPrompt.
	IsCompactSummary bool `json:"isCompactSummary"`
	// RequestID names the API response an assistant entry came from. Claude Code
	// splits one response across an entry per content block and repeats the whole
	// response's usage on each, so this is what a token tally groups by. Absent on
	// entries Claude Code composed itself and on logs predating the field.
	RequestID string `json:"requestId"`
	// PromptID names the prompt submission an entry belongs to, on user entries
	// only. A forked log repeats its parent prompt's value on its first entry,
	// which is what pairs a fork with the turn that started it.
	PromptID string `json:"promptId"`
	// Subtype and Content carry a system entry's kind and its payload. The one
	// kind read here is local_command, whose content is what the harness printed
	// to the terminal — for a slash command the caller typed, the only record of
	// the turn's reply the main log holds. Content is raw because other system
	// kinds put an object here and a string decode would fail the whole line.
	Subtype string          `json:"subtype"`
	Content json.RawMessage `json:"content"`
	// TurnCompanion marks a user entry as material the harness attached to the
	// turn rather than anything a person typed. Claude Code's own prompt test
	// refuses to count an entry carrying it; written since 2.1.236.
	TurnCompanion bool `json:"turnCompanion"`
	// IsMeta and SourceToolUseID together mark a reminder the harness attached to
	// a tool call. IsMeta alone does not: a prompt a person actually typed can
	// carry it too, and refusing on it would delete their turns.
	IsMeta          bool   `json:"isMeta"`
	SourceToolUseID string `json:"sourceToolUseID"`
	// TotalCostUSD, TotalLinesAdded and TotalLinesRemoved are the session's totals
	// so far, all three on a cost-state entry. They carry together, so their
	// presence is the entry's presence and costState reads them together rather
	// than deciding each one's absence separately.
	TotalCostUSD      *float64 `json:"totalCostUSD"`
	TotalLinesAdded   *int     `json:"totalLinesAdded"`
	TotalLinesRemoved *int     `json:"totalLinesRemoved"`
}

// rawSnapshot is the file-history-snapshot payload. Only the keys of
// trackedFileBackups matter — they are the paths — so the values are left
// unparsed rather than modelling a backup record agentry never reads.
type rawSnapshot struct {
	TrackedFileBackups map[string]json.RawMessage `json:"trackedFileBackups"`
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
	// CacheCreation splits the flat counter above by how long the cache was
	// bought for. Only the hour share is read: the five-minute share is the flat
	// counter less that, which is also what an older log carrying no split reduces
	// to. Absent on such a log, where the zero prices every write at the
	// five-minute rate.
	CacheCreation struct {
		Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// tally is the usage object as the model carries it. Named once because three
// readers decode it — the parser, the sidecar scan, and the tests — and a reader
// that skipped a counter would report a different spend for the same session.
func (u rawUsage) tally() model.Usage {
	return model.Usage{
		Input: u.Input, Output: u.Output,
		CacheRead: u.CacheRead, CacheCreate: u.CacheCreate,
		CacheCreate1h: u.CacheCreation.Ephemeral1h,
	}
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

const (
	// localCommandSubtype marks the system entry holding what a locally-run slash
	// command printed to the terminal.
	localCommandSubtype = "local_command"
	localCommandOpen    = "<local-command-stdout>"
	localCommandClose   = "</local-command-stdout>"
)

// unwrapLocalCommand strips the marker Claude Code wraps a local command's
// output in, and the terminal escapes the command wrote into it. Text carrying
// no marker keeps its body, since the wrapper is the harness's framing rather
// than part of what the command said.
func unwrapLocalCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, localCommandOpen) {
		text = strings.TrimPrefix(text, localCommandOpen)
		text = strings.TrimSuffix(strings.TrimSpace(text), localCommandClose)
	}
	return strings.TrimSpace(stripEscapes(text))
}

const (
	// A shell command run in the session with "!" reaches the log as two user
	// entries: the command in shellInputOpen, and both its output streams in a
	// second entry. Only the first was typed.
	shellInputOpen  = "<bash-input>"
	shellInputClose = "</bash-input>"
	shellOutOpen    = "<bash-stdout>"
	shellOutClose   = "</bash-stdout>"
	shellErrOpen    = "<bash-stderr>"
	shellErrClose   = "</bash-stderr>"
	// shellPromptPrefix marks such a command in the prompt list, so a reader can
	// tell what someone ran from what they asked.
	shellPromptPrefix = "! "
)

// typedShellCommand returns the command a caller ran in the session with "!",
// for a user entry whose whole content is the wrapper around it.
//
// The test is the entry in full rather than a substring, because a compaction
// summary can quote these wrappers when it summarizes a session that ran one,
// and reading a quotation as a command would open a turn nobody took and title
// the session after it.
func typedShellCommand(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, shellInputOpen)
	if !ok {
		return "", false
	}
	cmd, ok := strings.CutSuffix(rest, shellInputClose)
	if !ok {
		return "", false
	}
	cmd = strings.TrimSpace(cmd)
	return cmd, cmd != ""
}

// shellOutput splits what a typed shell command printed into its two streams,
// reporting false for any other content. Either wrapper can be absent — a
// command can write to only one stream — and the two are returned apart rather
// than joined, because the log records them as separate strings and printing
// one block would claim an interleaving it does not hold.
func shellOutput(content string) (out, errOut string, ok bool) {
	rest := content
	if body, cut := strings.CutPrefix(rest, shellOutOpen); cut {
		i := strings.Index(body, shellOutClose)
		if i < 0 {
			return "", "", false
		}
		out, rest = body[:i], body[i+len(shellOutClose):]
		ok = true
	}
	if body, cut := strings.CutPrefix(rest, shellErrOpen); cut {
		i := strings.Index(body, shellErrClose)
		if i < 0 {
			return "", "", false
		}
		errOut, rest = body[:i], body[i+len(shellErrClose):]
		ok = true
	}
	if !ok || rest != "" {
		return "", "", false
	}
	return out, errOut, true
}

// shellOutputEvents renders what a typed shell command printed, fenced on the
// rule a local command's output already follows: the command laid its output out
// for a terminal, and reflowing it as prose destroys any alignment it drew. A
// stream that printed nothing contributes no event, so a silent command renders
// as its prompt alone rather than as an empty fence.
func shellOutputEvents(out, errOut string) []model.Event {
	var evs []model.Event
	if text := strings.TrimSpace(stripEscapes(out)); text != "" {
		evs = append(evs, model.Event{Kind: model.EventText, Text: fenced(text)})
	}
	if text := strings.TrimSpace(stripEscapes(errOut)); text != "" {
		evs = append(evs, model.Event{Kind: model.EventText, Text: "`stderr`\n\n" + fenced(text)})
	}
	return evs
}

// fenced wraps captured terminal output in a code fence, so the markdown
// renderer prints it as it was printed. A local command lays its own output out
// for a terminal — /context draws a fixed-width grid of block glyphs — and
// reflowing that as prose runs the grid together into a paragraph no reader can
// read back. The fence opens one backtick longer than the longest run inside,
// so output that itself holds a fence cannot close this one early.
func fenced(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
			continue
		}
		run = 0
	}
	fence := strings.Repeat("`", max(longest+1, 3))
	return fence + "\n" + text + "\n" + fence
}

// stripEscapes removes the escape sequences a command wrote for a terminal it
// was printing to directly. The output is re-rendered through markdown here, so
// a sequence left in place is passed through as text: /context styles its
// headings bold, and other commands can leave similar codes behind. Newlines
// and tabs are content and survive.
func stripEscapes(text string) string {
	if !strings.ContainsRune(text, 0x1b) {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] != 0x1b {
			out.WriteByte(text[i])
			i++
			continue
		}
		i = endOfEscape(text, i)
	}
	return out.String()
}

// endOfEscape returns the offset just past the escape sequence starting at i.
// Two forms reach a captured stream: a control sequence, ESC '[' up to a byte in
// 0x40–0x7e, and an operating-system command, ESC ']' up to a bell or a string
// terminator. Anything else is a two-byte escape, and an unterminated sequence
// consumes the rest rather than leaving its bytes to print as text.
func endOfEscape(text string, i int) int {
	j := i + 1
	if j >= len(text) {
		return len(text)
	}
	switch text[j] {
	case '[':
		for j++; j < len(text); j++ {
			if text[j] >= 0x40 && text[j] <= 0x7e {
				return j + 1
			}
		}
		return len(text)
	case ']':
		for j++; j < len(text); j++ {
			if text[j] == 0x07 {
				return j + 1
			}
			if text[j] == 0x1b && j+1 < len(text) && text[j+1] == '\\' {
				return j + 2
			}
		}
		return len(text)
	default:
		return j + 1
	}
}

func loadEntries(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []entry
	// A log can carry the same entry twice: whole turns written a second time
	// under fresh prompt ids, every repeat byte-identical to its first copy.
	// Rendering both would show the conversation twice, rank the repeats as
	// their own steps, and count their tokens, dollars and tool calls again in
	// the per-turn figures, while the session tally deduplicates them away.
	seenUUID := map[string]bool{}
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
		if re.UUID != "" {
			if seenUUID[re.UUID] {
				continue
			}
			seenUUID[re.UUID] = true
		}
		e := entry{
			// Only one of the three title fields is ever set on a given entry —
			// each belongs to a different entry type — so concatenating picks it.
			typ: re.Type, uuid: re.UUID, title: re.AiTitle + re.CustomTitle + re.AgentName,
			isCompactSummary: re.IsCompactSummary, cwd: re.Cwd, entrypoint: re.Entrypoint,
			effort:     re.Effort,
			denialKind: re.ToolDenialKind, trackingPath: re.TrackingPath,
			requestID: re.RequestID, promptID: re.PromptID, turnCompanion: re.TurnCompanion,
			harnessReminder: re.IsMeta && re.SourceToolUseID != "",
		}
		if re.Type == "cost-state" {
			e.cost = &costState{}
			if re.TotalCostUSD != nil {
				e.cost.costUSD = *re.TotalCostUSD
			}
			if re.TotalLinesAdded != nil {
				e.cost.linesAdded = *re.TotalLinesAdded
			}
			if re.TotalLinesRemoved != nil {
				e.cost.linesRemoved = *re.TotalLinesRemoved
			}
		}
		switch re.Type {
		case "pr-link":
			e.pr = model.PR{Repository: re.PRRepository, Number: re.PRNumber, URL: re.PRURL}
		case "frame-link":
			e.frame = model.Artifact{Title: re.FrameTitle, URL: re.FrameURL, Path: re.FramePath}
		case "system":
			if re.Subtype == localCommandSubtype {
				var text string
				if json.Unmarshal(re.Content, &text) == nil {
					e.localCommandOutput = unwrapLocalCommand(text)
				}
			}
		}
		if re.Snapshot != nil {
			for p := range re.Snapshot.TrackedFileBackups {
				e.trackedPaths = append(e.trackedPaths, p)
			}
			// Map iteration is unordered, and the touched-file list is documented as
			// first-seen order, so a snapshot's own paths are sorted to make one
			// session's output identical on every run.
			sort.Strings(e.trackedPaths)
		}
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
				e.usage = msg.Usage.tally()
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

// syntheticModel is the model Claude Code writes on assistant messages it
// composed itself rather than received from a model — an API-error notice, a
// session-limit warning, "No response requested.". They carry zero tokens and
// name no model the session ran on, so counting one would end a session on a
// model that never ran.
const syntheticModel = "<synthetic>"

// models returns every distinct model the session ran on, in first-seen order.
// Only assistant entries name one, so an absent value is skipped rather than
// recorded as a change — the same reading efforts uses, and for the same reason.
// Switching mid-session happens, and the values come in contiguous blocks, so
// the last one is the session's.
func models(entries []entry) []string {
	return distinct(entries, func(e entry) string {
		if e.typ != "assistant" || e.model == syntheticModel {
			return ""
		}
		return e.model
	})
}

// usageTally totals assistant tokens while counting each API response once.
// Claude Code splits one response across an entry per content block and repeats
// that response's whole usage object on every one of them, so adding the entries
// up multiplies a reply's tokens by how many blocks it held — a large factor,
// and not a constant one, so it is enough of a per-turn variable to reorder the
// summary as well as inflate the totals.
//
// The repeats are not identical, which is why one entry per response is picked
// by its output count rather than by arriving first. Claude Code writes each
// entry as the reply streams: input and both cache counts match across a
// response's entries, while output grows, so the first entry can hold a partial
// figure.
type usageTally struct {
	// keyless holds the entries that name no response, kept as they arrive; best
	// holds one entry per response, replaced when a higher output shows up.
	keyless []usageRecord
	best    map[string]usageRecord
}

// usageRecord is one response's tokens beside the two facts a cost roll-up
// groups them by: the model that answered, which sets the rate, and the local
// day the response was spent on, which sets the bucket. The tally carries them
// so the deduplication rule above is written once and serves both the session
// total and the split — a second tally applying the rule again is a second
// chance to apply it differently.
type usageRecord struct {
	day   string
	model string
	// agent is what the response was delegated to, empty on the main thread —
	// the label the agent axis groups by, carried here so one deduplication rule
	// serves that split as well as the day-and-model one.
	agent string
	usage model.Usage
}

// add offers one assistant entry to the tally, keeping it when it is the highest
// output seen for its response. An entry naming neither a response nor itself is
// counted every time: with no identity there is nothing to compare against, so
// such an entry keeps the undeduplicated behavior rather than collapsing into
// whichever came first.
//
// The whole usage object of the winning entry is kept, not a per-counter
// maximum, so the four counters stay as one response reported them; the three
// that do not grow are identical across the group either way.
func (t *usageTally) add(key string, r usageRecord) {
	if key == "" {
		t.keyless = append(t.keyless, r)
		return
	}
	if prev, ok := t.best[key]; ok && prev.usage.Output >= r.usage.Output {
		return
	}
	if t.best == nil {
		t.best = map[string]usageRecord{}
	}
	t.best[key] = r
}

// sum totals what the tally kept. Addition of the per-response entries is
// commutative, so map iteration order does not reach the result.
func (t usageTally) sum() model.Usage {
	var out model.Usage
	for _, r := range t.records() {
		out.Add(r.usage)
	}
	return out
}

// records is every response the tally kept, in no particular order — the input
// to grouping, and what makes the split and the total the same set of responses
// read two ways.
func (t usageTally) records() []usageRecord {
	out := make([]usageRecord, 0, len(t.keyless)+len(t.best))
	out = append(out, t.keyless...)
	for _, r := range t.best {
		out = append(out, r)
	}
	return out
}

// usageKey identifies the response an assistant entry belongs to. Claude Code
// composes some assistant entries itself and writes no requestId on them, so the
// entry's own id stands in — which counts that entry once, exactly as it was
// counted before. Most assistant entries carry the field, so that fallback is
// for the synthetic entries and for a log old enough to predate it.
func usageKey(requestID, uuid string) string {
	if requestID != "" {
		return requestID
	}
	return uuid
}

func sumUsage(entries []entry) model.Usage {
	return mainTally(entries, time.Time{}).sum()
}

// mainTally reads the main log's assistant entries into one tally. fallback is
// the timestamp a record takes when its own entry carries none, so a response
// still lands on a day; callers with no day to answer for pass the zero time.
func mainTally(entries []entry, fallback time.Time) usageTally {
	var t usageTally
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		t.add(usageKey(e.requestID, e.uuid), usageRecord{
			day: dayOf(e.t, fallback), model: e.model, usage: e.usage,
		})
	}
	return t
}

// dayOf is the local calendar date a response's tokens are attributed to. Local
// rather than UTC because the question asked of a daily figure is what today
// cost, and today is the caller's. Empty only for a response whose entry carries
// no timestamp in a session that carries none either, which buckets as unknown
// rather than being dropped — the tokens were spent whatever the log forgot.
func dayOf(t, fallback time.Time) string {
	if t.IsZero() {
		t = fallback
	}
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// firstStamp is the session's earliest known timestamp, standing in for an entry
// that carries none.
func firstStamp(entries []entry) time.Time {
	for _, e := range entries {
		if !e.t.IsZero() {
			return e.t
		}
	}
	return time.Time{}
}

// groupDaily collapses responses into one entry per model per day, ordered by
// day then model so one session's split is identical on every run — map
// iteration reaches the records in no order, and a listing that reshuffled its
// own output could not be diffed between runs.
//
// A response that spent no tokens is dropped rather than grouped: the entries
// Claude Code composes itself carry a placeholder model and zero counters, and a
// bucket for them would name a model the session never ran on. Dropping them
// changes no total, since their tokens are zero.
func groupDaily(recs []usageRecord) []model.DailyUsage {
	idx := map[string]int{}
	var out []model.DailyUsage
	for _, r := range recs {
		if r.usage == (model.Usage{}) {
			continue
		}
		key := r.day + "\x00" + r.model + "\x00" + r.agent
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, model.DailyUsage{Day: r.day, Model: r.model, Agent: r.agent})
		}
		out[i].Usage.Add(r.usage)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// ── Tool results and agent stitching ─────────────────────────────────────

type toolResult struct {
	end     time.Time
	isError bool
	text    string
	denial  string
}

// toolResultMap indexes each call's outcome by tool_use id. The denial kind is
// read from the entry rather than the block: the log puts toolDenialKind at the
// top level of the user entry carrying the tool_result, not inside the result.
func toolResultMap(entries []entry) map[string]toolResult {
	m := map[string]toolResult{}
	for _, e := range entries {
		if e.typ != "user" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_result" {
				m[b.toolUseID] = toolResult{end: e.t, isError: b.isError, text: b.resultText, denial: e.denialKind}
			}
		}
	}
	return m
}

// failureStats groups the session's top-level calls that ran and failed, by tool
// and identity — the counterpart to denialStats, and what tells a reader which
// tool a header reading "9 failed" is about.
//
// A refused call is excluded rather than counted here: the log flags it as an
// error too, so counting on the flag alone would file a permission boundary that
// held as something to go fix. Denial is what separates them, the same test the
// header's two counts make.
func failureStats(entries []entry) []model.ToolStat {
	results := toolResultMap(entries)
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
			r, ok := results[b.id]
			if !ok || !r.isError || r.denial != "" {
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

// denialStats groups the session's refused top-level calls by what refused them
// and which call it was — the shape an auto-allow decision is made in. A denial
// is matched back to its tool_use by id, so a result with no matching call (a
// truncated log) contributes nothing rather than an entry named "".
func denialStats(entries []entry) []model.DenialStat {
	type call struct{ tool, identity string }
	calls := map[string]call{}
	for _, e := range entries {
		if e.typ != "assistant" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ == "tool_use" {
				calls[b.id] = call{b.name, toolIdentity(b.name, b.input)}
			}
		}
	}
	type key struct{ kind, tool, identity string }
	counts := map[key]int{}
	var order []key
	for _, e := range entries {
		if e.typ != "user" || e.denialKind == "" {
			continue
		}
		for _, b := range e.blocks {
			if b.typ != "tool_result" {
				continue
			}
			c, ok := calls[b.toolUseID]
			if !ok {
				continue
			}
			k := key{e.denialKind, c.tool, c.identity}
			if counts[k] == 0 {
				order = append(order, k)
			}
			counts[k]++
		}
	}
	out := make([]model.DenialStat, 0, len(order))
	for _, k := range order {
		out = append(out, model.DenialStat{Kind: k.kind, Tool: k.tool, Identity: k.identity, Count: counts[k]})
	}
	return out
}

// sessionFiles is every file the session modified, absolute and deduplicated in
// first-seen order. The log mixes forms — a path inside the session's working
// directory is recorded relative to it, one outside is absolute — so a relative
// path is resolved against cwd. With no cwd (a log predating the field) the
// relative paths are kept as they are: reporting them beats dropping a real
// change, and an unrooted path still names the file.
func sessionFiles(entries []entry, cwd string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) && cwd != "" {
			p = filepath.Join(cwd, p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, e := range entries {
		add(e.trackingPath)
		for _, p := range e.trackedPaths {
			add(p)
		}
	}
	return out
}

// recordedTotals expands a session's cost-state record into the three model
// fields that carry it. All three are present together or all nil, which is the
// record's own presence rule: a caller cannot be handed a line count from a log
// that recorded no cost, because one entry carries both.
func recordedTotals(entries []entry) (costUSD *float64, linesAdded, linesRemoved *int) {
	cs := sessionCostState(entries)
	if cs == nil {
		return nil, nil, nil
	}
	return &cs.costUSD, &cs.linesAdded, &cs.linesRemoved
}

// costState is what Claude Code recorded a session as having amounted to: its
// dollar cost and the lines it added and removed. A missing counter reads zero
// rather than being tracked separately, because all three carry together — so
// the record either exists or it does not.
type costState struct {
	costUSD      float64
	linesAdded   int
	linesRemoved int
}

// sessionCostState is the session's last cost-state record. Claude Code rewrites
// the entry as the session runs and each one holds the totals so far, so the last
// is the answer and adding them up would multiply it. Nil when the log carries no
// such entry — every session before Claude Code 2.1.241 and plenty since — which
// is not a claim the session was free or changed nothing.
func sessionCostState(entries []entry) *costState {
	var last *costState
	for _, e := range entries {
		if e.cost != nil {
			last = e.cost
		}
	}
	return last
}

// sessionPRs and sessionArtifacts are what the session produced beyond its own
// transcript, each read from the entry type Claude Code writes for it.
func sessionPRs(entries []entry) []model.PR {
	return dedupeOutputs(entries, "pr-link",
		func(e entry) model.PR { return e.pr },
		model.PR.Key, latestPR)
}

func sessionArtifacts(entries []entry) []model.Artifact {
	return dedupeOutputs(entries, "frame-link",
		func(e entry) model.Artifact { return e.frame },
		model.Artifact.Key, latestArtifact)
}

// dedupeOutputs collapses one kind of re-recorded session event into the distinct
// things it names, in first-seen order. Claude Code re-emits both pr-link and
// frame-link on later turns of the same session, and heavily, so the entries are
// a stream of restatements, not a list. A record naming nothing (no key) is
// dropped: it identifies no thing a reader could act on.
//
// One function rather than two loops because the rule is one rule. A copy per
// entry type is where a later kind of output quietly stops deduplicating.
func dedupeOutputs[T any](entries []entry, typ string, get func(entry) T, key func(T) string, merge func(old, cur T) T) []T {
	var out []T
	at := map[string]int{}
	for _, e := range entries {
		if e.typ != typ {
			continue
		}
		v := get(e)
		k := key(v)
		if k == "" {
			continue
		}
		if i, ok := at[k]; ok {
			out[i] = merge(out[i], v)
			continue
		}
		at[k] = len(out)
		out = append(out, v)
	}
	return out
}

// latestPR and latestArtifact fold a re-record over the entry already held: the
// newer values win, except where the newer entry says nothing. An omitted field
// is an omission and not a deletion — the log gives no way to tell those apart,
// and discarding a value agentry already read is the worse guess. It is not
// hypothetical for an artifact: a frame-link entry can carry no title at all,
// so a republish from a moved file would otherwise lose the name the artifact
// had.
func latestPR(old, cur model.PR) model.PR {
	if cur.Repository == "" {
		cur.Repository = old.Repository
	}
	if cur.Number == 0 {
		cur.Number = old.Number
	}
	if cur.URL == "" {
		cur.URL = old.URL
	}
	return cur
}

func latestArtifact(old, cur model.Artifact) model.Artifact {
	if cur.Title == "" {
		cur.Title = old.Title
	}
	if cur.URL == "" {
		cur.URL = old.URL
	}
	if cur.Path == "" {
		cur.Path = old.Path
	}
	return cur
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

// unlinkedSkillCall is a Skill tool_use carrying no structured sidecar id — the
// only calls the legacy name fallback may pair.
type unlinkedSkillCall struct {
	toolUseID string
	skill     string
	at        time.Time
}

// sidecarNameLinks pairs the session's Skill calls that carry no structured
// sidecar id with the sidecars no structured id claims, once for the whole
// session, and returns tool_use id → sidecar key.
//
// Two properties the per-call map scan it replaces had neither of. It is a
// function of the data rather than of Go's randomized map order, so two runs over
// one log render identically. And it never hands a sidecar to a call that did not
// spawn it: a sidecar named by any log's toolUseResult.agentId already has an
// owner, and giving it to a same-named Skill call stole it — attachSubagent marks
// every expansion in seen, so the owning Agent call then rendered as a bare leaf
// and its whole subtree left the transcript. Real sessions make that the normal
// case, not the corner one: most sidecars under a real session are claimed by a
// structured link, while its Skill calls without one are inline skills that
// spawned nothing and must stay leaves.
//
// What remains for the fallback is the pre-structured forked skill: an unclaimed
// sidecar whose own first entry names the skill. Calls are paired in log order
// against candidates in start order, each sidecar used once, and only with a
// sidecar that started at or after the call — a spawn cannot precede its caller.
func sidecarNameLinks(entries []entry, subs map[string]*subagent) map[string]string {
	keys := make([]string, 0, len(subs))
	for key := range subs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	logs := make([][]entry, 0, len(subs)+1)
	logs = append(logs, entries)
	for _, key := range keys {
		logs = append(logs, subs[key].entries)
	}

	claimed := map[string]bool{}
	var calls []unlinkedSkillCall
	for _, log := range logs {
		linked := skillSidecarMap(log)
		for _, key := range agentIDMap(log) {
			claimed[key] = true
		}
		for _, key := range linked {
			claimed[key] = true
		}
		for _, e := range log {
			if e.typ != "assistant" {
				continue
			}
			for _, b := range e.blocks {
				if b.typ != "tool_use" || b.name != "Skill" || linked[b.id] != "" {
					continue
				}
				if skill, _ := b.input["skill"].(string); skill != "" {
					calls = append(calls, unlinkedSkillCall{toolUseID: b.id, skill: skill, at: e.t})
				}
			}
		}
	}

	// Candidates keep their start order per skill name, so the pairing below
	// consumes the oldest unused sidecar first.
	candidates := map[string][]string{}
	for _, key := range keys {
		if claimed[key] || subs[key].skillName == "" {
			continue
		}
		candidates[subs[key].skillName] = append(candidates[subs[key].skillName], key)
	}
	for skill := range candidates {
		ids := candidates[skill]
		sort.SliceStable(ids, func(i, j int) bool {
			return firstStamp(subs[ids[i]].entries).Before(firstStamp(subs[ids[j]].entries))
		})
	}

	sort.SliceStable(calls, func(i, j int) bool {
		if !calls[i].at.Equal(calls[j].at) {
			return calls[i].at.Before(calls[j].at)
		}
		return calls[i].toolUseID < calls[j].toolUseID
	})

	out := map[string]string{}
	used := map[string]bool{}
	for _, c := range calls {
		for _, key := range candidates[c.skill] {
			if used[key] || firstStamp(subs[key].entries).Before(c.at) {
				continue
			}
			used[key] = true
			out[c.toolUseID] = key
			break
		}
	}
	return out
}

// ── Subagents ────────────────────────────────────────────────────────────

type subagent struct {
	entries   []entry
	skillName string
	// forkedSkill is the skill named by the log's opening entry, which is what a
	// forked skill's log begins with. It is empty on a subagent log that loaded a
	// skill partway through, where skillName is not, and whose own name is an
	// agent type.
	forkedSkill string
	// promptID is the prompt this log was forked under, repeated from the parent
	// session's own entry. Empty on a log written before Claude Code carried the
	// field, which leaves the fork chargeable to no turn.
	promptID string
}

// openingSkill names the skill a log opens by loading, empty when its first
// entry carries no marker. Distinct from subagentSkill, which finds a marker
// anywhere and so also answers for a subagent that loaded a skill of its own.
func openingSkill(entries []entry) string {
	if len(entries) == 0 {
		return ""
	}
	return skillFromText(entries[0].contentStr)
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
		subs[id] = &subagent{
			entries:     entries,
			skillName:   subagentSkill(entries),
			forkedSkill: openingSkill(entries),
			promptID:    firstPromptID(entries),
		}
	}
	return subs
}

// firstPromptID is the prompt a log was started under, taken from the earliest
// entry carrying one. Every entry of a forked log that carries the field carries
// the parent prompt's value, so the earliest is the whole answer; taking it from
// the earliest rather than the last is what keeps that true if a fork ever
// records a prompt of its own.
func firstPromptID(entries []entry) string {
	for _, e := range entries {
		if e.promptID != "" {
			return e.promptID
		}
	}
	return ""
}

// skillMarker opens the line Claude Code writes into a forked skill's own log,
// naming the directory the skill was loaded from. It is the only record inside a
// sidecar that says which skill ran, which is what names a fork the main log
// holds no spawning call for.
const skillMarker = "Base directory for this skill:"

// skillFromText reads the skill's name off the marker line, empty when the text
// carries no marker. The name is the directory's base, since that is what a
// caller types after the slash.
func skillFromText(text string) string {
	if !strings.Contains(text, skillMarker) {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, skillMarker) {
			p := strings.TrimSpace(line[strings.Index(line, skillMarker)+len(skillMarker):])
			return filepath.Base(p)
		}
	}
	return ""
}

func subagentSkill(entries []entry) string {
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
		if skill := skillFromText(text); skill != "" {
			return skill
		}
	}
	return ""
}

// totalAgentUsage sums an agent's tokens plus those of every agent it spawned.
// delegatedRecords is every response one delegated log paid for, and every log
// it delegated to in turn, kept split by model rather than summed.
//
// Split because the sum cannot be priced: cache reads cost a fortieth of input
// on one tier and a tenth on every other, so a turn that delegated to a cheaper
// model and was priced at one rate reports a bill nobody was sent. It follows
// sidecarChildren rather than the Agent links alone, so a delegation that forked
// a skill carries that skill's tokens too.
func delegatedRecords(key string, subs map[string]*subagent, nameLinks map[string]string, seen map[string]bool, fallback time.Time) []usageRecord {
	if seen[key] {
		return nil
	}
	seen[key] = true
	s := subs[key]
	if s == nil {
		return nil
	}
	recs := mainTally(s.entries, fallback).records()
	for _, child := range sidecarChildren(s.entries, nameLinks) {
		recs = append(recs, delegatedRecords(child, subs, nameLinks, seen, fallback)...)
	}
	return recs
}

// ── Turn splitting ─────────────────────────────────────────────────────────

type rawTurn struct {
	prompt string
	start  time.Time
	end    time.Time
	// promptID comes from the prompt entry itself, which splitTurns consumes as
	// the boundary rather than adding to entries. A fork the prompt started
	// carries the same value and is charged to this turn by it.
	promptID string
	entries  []entry
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
				cur = &rawTurn{prompt: prompt, start: e.t, end: e.t, promptID: e.promptID}
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

// compactSummaryPlaceholder stands in for a compaction boundary's summary, which
// is a user entry Claude Code wrote rather than a prompt anyone typed.
const compactSummaryPlaceholder = "[context compacted — see session log for full summary]"

// promptText is the text a user entry offers for the prompt tests below, from
// either shape the log records content in. A plain string is the common case;
// the software development kit writes the same text as a list of text blocks
// instead, and a session whose prompts all arrive that way opened no turn at all
// until this read them — leaving its whole transcript blank and every response
// it made outside the per-turn tallies.
//
// One text block among others is not enough: a tool result, an image or an
// unrecognized block means the entry is carrying something a prompt does not, so
// the entry is refused whatever text sits beside it.
func promptText(e entry) (string, bool) {
	if e.hasStr {
		return e.contentStr, true
	}
	if len(e.blocks) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(e.blocks))
	for _, b := range e.blocks {
		if b.typ != "text" {
			return "", false
		}
		parts = append(parts, b.text)
	}
	return strings.Join(parts, "\n"), true
}

// userPrompt returns the human-typed prompt for a user entry, or ok=false for
// system-injected content (tool results, skill bodies, bash output).
func userPrompt(e entry) (string, bool) {
	content, ok := promptText(e)
	if !ok {
		return "", false
	}
	// The compaction check runs before the injected markers because the flag says
	// what the entry *is*, while a marker only says what its text contains — and a
	// summary of a conversation can quote one, which would drop the boundary
	// entirely instead of standing in for it.
	if e.isCompactSummary {
		return compactSummaryPlaceholder, true
	}
	// Claude Code's own prompt test refuses an entry carrying this, so agentry
	// refuses it too rather than guessing from the text — a skill body, a
	// re-invocation notice, an image-rescaling note and a malformed-tool-call
	// nudge are all filed as user entries and none was typed. It sits below the
	// compaction check for the reason that check leads: a boundary stands in for
	// the conversation it replaced and has to keep its place in the sequence.
	// Older logs carry no marker, which is what the text markers below still
	// cover.
	if e.turnCompanion || e.harnessReminder {
		return "", false
	}
	// Above the marker loop because the loop still refuses this command's output,
	// and below turnCompanion because that flag says what an entry is.
	if cmd, ok := typedShellCommand(content); ok {
		return shellPromptPrefix + cmd, true
	}
	for _, m := range injectedMarkers {
		if strings.Contains(content, m) {
			return "", false
		}
	}
	// Fallback for logs written before Claude Code added isCompactSummary. It
	// matches Claude Code's own wording, so an upstream rewording silently stops
	// it firing — which on a flagged entry no longer matters, and on an unflagged
	// one would spill the whole summary into the prompt list.
	if strings.Contains(content, "This session is being continued from a previous conversation") {
		return compactSummaryPlaceholder, true
	}
	if strings.Contains(content, "<command-name>") {
		cmd, args := "?", ""
		if m := cmdNameRe.FindStringSubmatch(content); m != nil {
			cmd = m[1]
		}
		if m := cmdArgsRe.FindStringSubmatch(content); m != nil {
			args = strings.TrimSpace(m[1])
		}
		// Claude Code records command-name inconsistently — built-ins carry a
		// leading slash ("/clear"), custom commands do not ("sonar") — so strip any
		// leading slashes and add exactly one rather than doubling to "//clear".
		return strings.TrimRight("/"+strings.TrimLeft(cmd, "/")+" "+args, " "), true
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
func buildEvents(entries []entry, subs map[string]*subagent, nameLinks map[string]string, seen map[string]bool) []model.Event {
	results := toolResultMap(entries)
	agents := agentIDMap(entries)
	skills := skillSidecarMap(entries)
	var out []model.Event
	for _, e := range entries {
		// What a locally-run slash command printed is the turn's reply: the caller
		// read it in the terminal and the main log holds no assistant text beside
		// it. Rendered as ordinary turn text rather than as a tool result, because
		// a result is gated on a channel and the turn would otherwise render blank.
		if e.localCommandOutput != "" {
			out = append(out, model.Event{Kind: model.EventText, Text: fenced(e.localCommandOutput)})
			continue
		}
		// What a typed shell command printed is the turn's reply for the same
		// reason: the caller read it in the terminal and no assistant text stands
		// beside it. The entry is filed under the user's name, so it has to be
		// taken before the assistant-only skip below.
		if e.typ == "user" && e.hasStr {
			if shellOut, shellErr, isOutput := shellOutput(e.contentStr); isOutput {
				out = append(out, shellOutputEvents(shellOut, shellErr)...)
				continue
			}
		}
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
					Name: b.name,
					Args: formatToolArgs(b.name, b.input),
					// Same function the listing groups by, so the two paths cannot
					// drift into naming one call two things.
					Identity: toolIdentity(b.name, b.input),
					Model:    toolModel(b.input),
					Prompt:   toolPrompt(b.name, b.input),
					Denial:   res.denial,
					Result:   res.text,
					IsError:  res.isError,
					Start:    e.t,
					End:      res.end,
				}
				attachSubagent(tool, b, agents, skills, nameLinks, subs, seen)
				out = append(out, model.Event{Kind: model.EventTool, Tool: tool})
			}
		}
	}
	return out
}

// forkEvents renders the logs a turn forked without making a tool call — what a
// slash command the caller typed leaves behind. Claude Code writes no tool_use
// for one, so there is no call for attachSubagent to hang the stream on and the
// step renders as a bare turn marker; this builds the call the log omitted, so
// the fork expands under the subagents channel like every other delegation.
//
// The synthesized call carries no result body. What the fork returned is the
// local_command output, which the turn already renders as its own text, and
// putting it in both places prints it twice at the levels that show results.
// printed is what the turn's local commands wrote, so the closing message the
// fork and the main log both hold is dropped from the stream and kept at the
// outer level — one value, one record of it, at the depth the reader lands on.
// The raw text rather than the turn's events, because the event carries the
// fence wrapped around it and would match nothing the fork holds.
func forkEvents(forks []typedFork, subs map[string]*subagent, nameLinks map[string]string, printed []string) []model.Event {
	var out []model.Event
	for _, f := range forks {
		sub, ok := subs[f.key]
		if !ok {
			continue
		}
		start, end := timeRange(sub.entries)
		stream := buildEvents(sub.entries, subs, nameLinks, map[string]bool{f.key: true})
		out = append(out, model.Event{Kind: model.EventTool, Tool: &model.Tool{
			Name: "Skill",
			// The skill alone, where a Skill call's args also carry what was passed
			// to it: the prompt box directly above already shows the line the caller
			// typed, arguments included.
			Args:     f.skill,
			Identity: f.skill,
			Start:    start,
			End:      end,
			Subagent: dropHoistedReply(stream, printed),
		}})
	}
	return out
}

// localCommandOutputs is what the locally-run commands in one turn printed.
func localCommandOutputs(entries []entry) []string {
	var out []string
	for _, e := range entries {
		if e.localCommandOutput != "" {
			out = append(out, e.localCommandOutput)
		}
	}
	return out
}

// dropHoistedReply removes the stream's closing message when the turn already
// prints it. A forked skill's last word reaches the main log as the local
// command's output, so the same text is in both places and rendering the
// expansion would repeat what the reader just read.
func dropHoistedReply(stream []model.Event, printed []string) []model.Event {
	last := len(stream) - 1
	if last < 0 || stream[last].Kind != model.EventText {
		return stream
	}
	for _, text := range printed {
		if strings.TrimSpace(text) == strings.TrimSpace(stream[last].Text) {
			return stream[:last]
		}
	}
	return stream
}

// attachSubagent fills tool.Subagent for Agent and forked-Skill calls that
// spawned a sidecar. Agent and forked-Skill links resolve by id (the structured
// agentId, see sidecarIDs); for a Skill with no id link it falls back to matching
// a sidecar by skill name (pre-structured forked logs), precomputed once per
// session by sidecarNameLinks. An inline skill — which runs in the main chain and
// writes no sidecar — matches nothing and renders as a leaf call, its work staying
// inline in the transcript.
func attachSubagent(tool *model.Tool, b block, agents, skills, nameLinks map[string]string, subs map[string]*subagent, seen map[string]bool) {
	key := ""
	switch b.name {
	case "Agent":
		key = agents[b.id]
	case "Skill":
		key = skills[b.id]
		if key == "" {
			key = nameLinks[b.id]
		}
	}
	if key == "" || seen[key] || subs[key] == nil {
		return
	}
	seen[key] = true
	tool.Subagent = buildEvents(subs[key].entries, subs, nameLinks, seen)
}

// turnMetrics totals what one turn spent, including every log it forked. forks
// names the logs charged to this turn by prompt id rather than by a tool call,
// which is the only route a slash command the caller typed leaves behind.
//
// typedCalls is how many of those the turn should also count as calls, which is
// the narrower set the tool tally uses: the turn renders a call for each, and a
// rule reading "0 tools" beside a visible call contradicts the transcript above
// it. Charging and counting differ because they answer different questions — a
// fork whose prompt agentry cannot name still spent money.
func turnMetrics(t rawTurn, subs map[string]*subagent, nameLinks map[string]string, forks []string, typedCalls int) (m turnTotals) {
	m.tools = typedCalls
	results := toolResultMap(t.entries)
	spawns := turnSpawns(t.entries, nameLinks)
	// One seen set for the whole turn, so a log two links both resolve to is
	// counted once rather than added twice to the figure that ranks the summary.
	seen := map[string]bool{}
	// One tally for the turn, because a response's blocks all sit inside the turn
	// that prompted it — a response never spans two turns, so deduplicating within
	// the turn loses nothing and double-counting here would reorder the summary.
	var tally usageTally
	// Delegated responses stay out of that tally and keep their own model: a
	// request id is unique to its own log, and pricing needs the model each
	// response actually ran on.
	var delegated []usageRecord
	for _, e := range t.entries {
		if e.typ != "assistant" {
			continue
		}
		tally.add(usageKey(e.requestID, e.uuid), usageRecord{
			day: dayOf(e.t, time.Time{}), model: e.model, usage: e.usage,
		})
		for _, b := range e.blocks {
			if b.typ != "tool_use" {
				continue
			}
			m.tools++
			if results[b.id].isError {
				m.errors++
			}
			if key, ok := spawns[b.id]; ok {
				delegated = append(delegated, delegatedRecords(key, subs, nameLinks, seen, t.start)...)
			}
		}
	}
	for _, key := range forks {
		delegated = append(delegated, delegatedRecords(key, subs, nameLinks, seen, t.start)...)
	}
	recs := append(tally.records(), delegated...)
	for _, r := range recs {
		m.usage.Add(r.usage)
	}
	m.costUSD = priceRecords(recs)
	return m
}

// turnTotals is what one turn amounted to: its tokens, what they are worth, and
// how its top-level calls went.
type turnTotals struct {
	usage   model.Usage
	costUSD *float64
	tools   int
	errors  int
}

// priceRecords is what a set of responses is worth at list prices, each priced
// at the model it ran on before the sum. Nil where not one of them ran on a
// model agentry holds a rate for — the rule the cost roll-up follows, since a
// response priced at zero reports the work as free rather than as unpriced.
//
// A turn mixing a priced model with an unpriced one returns what the priced part
// came to, which is the same partial figure the session-level total reports and
// names its unpriced models beside.
func priceRecords(recs []usageRecord) *float64 {
	total, any := 0.0, false
	for _, r := range recs {
		if usd, ok := price.Of(r.model, r.usage); ok {
			total += usd
			any = true
		}
	}
	if !any {
		return nil
	}
	return &total
}

// turnSpawns maps each tool call in a turn to the log it forked, over both
// spawning tools and the name pairing Load resolved for a forked skill whose
// result carries no structured link. A call that spawned nothing is absent from
// all three, so the lookup itself is the test: naming a tool here instead would
// have to name every tool that can fork, and a turn whose fork came from the one
// left out is charged nothing for it.
func turnSpawns(entries []entry, nameLinks map[string]string) map[string]string {
	out := map[string]string{}
	for _, spawns := range []map[string]string{agentIDMap(entries), skillSidecarMap(entries), nameLinks} {
		for toolUseID, key := range spawns {
			out[toolUseID] = key
		}
	}
	return out
}

// unclaimedForks groups by prompt id the logs no spawn record in the session
// names. A skill the caller invokes by typing its slash command forks a session
// and writes its log beside the others, but the main log holds no tool call for
// it, so every map keyed on a tool call misses it and its tokens are charged to
// no turn at all. Claude Code stamps the fork's first entry with the prompt id
// of the message that started it, which is the pairing that remains.
//
// The logs come back already assigned to turns, because the prompt id alone
// does not decide which turn owns one — see ownerTurn.
func unclaimedForks(turns []rawTurn, entries []entry, subs map[string]*subagent, nameLinks map[string]string) [][]string {
	claimed := map[string]bool{}
	for _, key := range nameLinks {
		claimed[key] = true
	}
	logs := make([][]entry, 0, len(subs)+1)
	logs = append(logs, entries)
	for _, sub := range subs {
		logs = append(logs, sub.entries)
	}
	for _, log := range logs {
		for _, spawns := range []map[string]string{agentIDMap(log), skillSidecarMap(log)} {
			for _, key := range spawns {
				claimed[key] = true
			}
		}
	}
	keys := make([]string, 0, len(subs))
	for key, sub := range subs {
		if claimed[key] || sub.promptID == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	byPrompt := turnOfPrompt(turns)
	out := make([][]string, len(turns))
	for _, key := range keys {
		sub := subs[key]
		if i := ownerTurn(turns, byPrompt, sub.promptID, firstStamp(sub.entries)); i >= 0 {
			out[i] = append(out[i], key)
		}
	}
	return out
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
