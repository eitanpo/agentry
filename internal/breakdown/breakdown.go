// Package breakdown groups a session's tool calls by identity — the tally of
// which skills, agents and commands ran, and how often — for every surface that
// prints it: the listing's tools channel and the rendered session's footer.
//
// It sits outside both callers because the listing imports the renderer, so a
// helper owned by either one would close a cycle, and a second copy of the
// grouping would let the two surfaces disagree about the same session.
package breakdown

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eitanpo/agentry/internal/model"
)

// join formats each entry of items and joins them with ", ", keeping at most max
// of them and naming how many it dropped. max <= 0 keeps every entry. The
// ordering is the caller's: what survives a cap is the head of it.
func join[T any](items []T, max int, format func(T) string) string {
	shown, hidden := items, 0
	if max > 0 && len(items) > max {
		shown, hidden = items[:max], len(items)-max
	}
	parts := make([]string, 0, len(shown)+1)
	for _, it := range shown {
		parts = append(parts, format(it))
	}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", hidden))
	}
	return strings.Join(parts, ", ")
}

// Tally is the tool activity one breakdown prints: the calls a session made, the
// ones that ran and failed, and the ones refused before they ran. It is a struct
// rather than three arguments because two same-typed slices side by side is a
// swap no compiler catches and no output makes obvious.
type Tally struct {
	Calls    []model.ToolStat
	Failures []model.ToolStat
	Denials  []model.DenialStat
}

// Lines renders a session's tool breakdown as one line per non-empty category:
// Skills / Agents / Bash labelled by identity, Other by tool name. Entries within
// a line are ordered by count descending, then name ascending.
//
// maxPerCategory bounds how many entries a line shows, naming the remainder;
// 0 shows every entry. A caller with a whole terminal line per session leaves it
// at 0, while one printing inside a bounded block passes a cap.
func Lines(t Tally, maxPerCategory int) []string {
	var skills, agents, bash, edits, other []model.ToolStat
	for _, st := range t.Calls {
		switch st.Tool {
		case "Skill":
			skills = append(skills, st)
		case "Agent":
			agents = append(agents, st)
		case "Bash":
			bash = append(bash, st)
		case "Edit", "Write":
			edits = append(edits, st)
		default:
			other = append(other, st)
		}
	}
	var lines []string
	emit := func(label string, group []model.ToolStat, label2 func(model.ToolStat) string) {
		if len(group) == 0 {
			return
		}
		name := func(st model.ToolStat) string {
			if label2 != nil {
				return label2(st)
			}
			if st.Identity == "" {
				return "?"
			}
			return st.Identity
		}
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Count != group[j].Count {
				return group[i].Count > group[j].Count
			}
			return name(group[i]) < name(group[j])
		})
		lines = append(lines, fmt.Sprintf("%-7s %s", label, join(group, maxPerCategory, func(st model.ToolStat) string {
			return fmt.Sprintf("%s ×%d", name(st), st.Count)
		})))
	}
	byTool := func(st model.ToolStat) string { return st.Tool }
	// Edits are shortened: the table has one line to spend, and a column of
	// repeated directory prefixes distinguishes nothing. --format json keeps the
	// full path, so the compression costs a reader nothing they cannot recover —
	// the same trade Bash makes in showing a program, not its whole command line.
	// Shortening stops at whatever is unique within this session, so two files
	// sharing a base name stay two entries the reader can tell apart.
	editPaths := map[string]bool{}
	for _, st := range edits {
		if st.Identity != "" {
			editPaths[st.Identity] = true
		}
	}
	editLabels := ShortestUniqueLabels(editPaths)
	byPath := func(st model.ToolStat) string {
		if l := editLabels[st.Identity]; l != "" {
			return l
		}
		return "?"
	}
	emit("Skills", skills, nil)
	emit("Agents", agents, nil)
	emit("Bash", bash, nil)
	emit("Edits", edits, byPath)
	emit("Other", other, byTool)
	// Both outcome lines come last and in this order: a failure is something to go
	// fix and a refusal is a boundary that held, so the one asking for action is
	// the one a reader meets first.
	if line := failureLine(t.Failures, maxPerCategory); line != "" {
		lines = append(lines, line)
	}
	if line := denialLine(t.Denials, maxPerCategory); line != "" {
		lines = append(lines, line)
	}
	return lines
}

// failureLine reports the calls that ran and failed, worst first, naming the
// tool and what it was pointed at. Without it a header reading "9 failed" says
// only that something went wrong, never where to look — and the refusals beside
// it were already named this way.
//
// A refused call is not here: it carries the log's error flag too, and the
// parser separates them on the denial reason rather than on that flag.
func failureLine(failures []model.ToolStat, maxPerCategory int) string {
	if len(failures) == 0 {
		return ""
	}
	sorted := append([]model.ToolStat(nil), failures...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Count != sorted[j].Count {
			return sorted[i].Count > sorted[j].Count
		}
		return sorted[i].Tool < sorted[j].Tool
	})
	return fmt.Sprintf("%-7s %s", "Failed", join(sorted, maxPerCategory, func(st model.ToolStat) string {
		what := st.Tool
		if st.Identity != "" {
			// The base name, the same compression the Edits line makes: a column of
			// repeated directory prefixes distinguishes nothing, and --format json
			// keeps the whole path.
			what += "/" + filepath.Base(st.Identity)
		}
		return fmt.Sprintf("%s ×%d", what, st.Count)
	}))
}

// denialLine reports the calls that were refused and by what, grouped kind by
// kind. It is part of the tools block because a denial is an outcome of a call,
// but it is not a ToolStat: the same call can both run and be refused in one
// session, and collapsing the two would report neither honestly.
func denialLine(denials []model.DenialStat, maxPerCategory int) string {
	if len(denials) == 0 {
		return ""
	}
	sorted := append([]model.DenialStat(nil), denials...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Count != sorted[j].Count {
			return sorted[i].Count > sorted[j].Count
		}
		return sorted[i].Kind < sorted[j].Kind
	})
	return fmt.Sprintf("%-7s %s", "Denied", join(sorted, maxPerCategory, func(d model.DenialStat) string {
		what := d.Tool
		if d.Identity != "" {
			what += "/" + filepath.Base(d.Identity)
		}
		return fmt.Sprintf("%s: %s ×%d", d.Kind, what, d.Count)
	}))
}

// ShortestUniqueLabels maps each path to the shortest suffix of its components
// that no other path in the set shares: "list.go" where that names one file,
// "cli/list.go" and "list/list.go" where two files would otherwise print
// identically. Shared by the project column and the Edits breakdown — two places
// shortening paths for one table have to shorten them the same way, and a bare
// base name is a label that silently merges distinct things on screen.
func ShortestUniqueLabels(paths map[string]bool) map[string]string {
	labels := make(map[string]string, len(paths))
	for p := range paths {
		parts := strings.Split(strings.Trim(p, string(filepath.Separator)), string(filepath.Separator))
		// Grow the suffix until no other path yields the same one. A path that is
		// a suffix of another (/a/b vs /x/a/b) exhausts its components first and
		// keeps the whole thing, which is already unique.
		label := p
		for n := 1; n <= len(parts); n++ {
			cand := strings.Join(parts[len(parts)-n:], "/")
			if uniqueSuffix(paths, p, cand) {
				label = cand
				break
			}
		}
		labels[p] = label
	}
	return labels
}

// uniqueSuffix reports whether cand identifies self alone among paths — no other
// path ends in the same components.
func uniqueSuffix(paths map[string]bool, self, cand string) bool {
	for p := range paths {
		if p == self {
			continue
		}
		parts := strings.Split(strings.Trim(p, string(filepath.Separator)), string(filepath.Separator))
		n := strings.Count(cand, "/") + 1
		if n > len(parts) {
			continue
		}
		if strings.Join(parts[len(parts)-n:], "/") == cand {
			return false
		}
	}
	return true
}
