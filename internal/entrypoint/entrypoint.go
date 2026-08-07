// Package entrypoint names where a session was run. The log's values are
// verbose and one of them is a misnomer — sdk-cli is written by any
// non-interactive run, a plain `claude -p` included, with no SDK involved — so
// the vocabulary agentry shows users lives here rather than at each use.
//
// It is its own package because both the listing and the renderer need it, and
// they are siblings: neither imports the other, and a shared meaning is not a
// reason to make one depend on the other.
package entrypoint

import "strings"

// Selector values, as accepted by --from and shown in the listing's tag column.
const (
	CLI = "cli" // the terminal
	App = "app" // the desktop app
	SDK = "sdk" // any non-interactive run
	All = "all" // no constraint; --from only, never a tag
)

// Names are the accepted --from values, for validation and suggestions.
var Names = []string{CLI, App, SDK, All}

// logHeadless is the log's value for a non-interactive run. Named once so the
// string does not spread across the listing default, `view`'s no-id resolution,
// and session-id completion, which all mean the same thing by "headless".
const logHeadless = "sdk-cli"

// logValues maps each selector to the log values it covers. All is absent: it
// constrains nothing, so a lookup that misses is the correct answer for it.
var logValues = map[string][]string{
	CLI: {"cli"},
	App: {"claude-desktop"},
	SDK: {logHeadless},
}

// IsHeadless reports whether a session was a non-interactive run — a `claude -p`
// from a script, a hook, a CI step. A session started headless and resumed
// interactively is not headless: the resolved entrypoint is the last one, and
// somebody worked in it.
func IsHeadless(logValue string) bool { return logValue == logHeadless }

// Matches reports whether a log value satisfies a selector. The empty selector
// is the default — everything except non-interactive runs.
func Matches(selector, logValue string) bool {
	switch selector {
	case All:
		return true
	case "":
		return !IsHeadless(logValue)
	}
	for _, want := range logValues[selector] {
		if logValue == want {
			return true
		}
	}
	return false
}

// Tag names one log value in three characters. An unrecognized value shows as
// "?" rather than being dropped, since Claude Code adds entrypoints without
// notice and a blank would read as "no data". A session predating the field
// really does carry no data, and gets the blank.
func Tag(logValue string) string {
	switch logValue {
	case "cli":
		return CLI
	case "claude-desktop":
		return App
	case logHeadless:
		return SDK
	case "":
		return ""
	}
	return "?"
}

// Trail spells an entrypoint history out for a single-session header, where
// there is room for it: "app→cli" for a session started in the desktop app and
// resumed from the terminal, or the one tag when it never moved. This is the
// same information a listing compresses to a "+" suffix for want of width.
func Trail(resolved string, all []string) string {
	if len(all) > 1 {
		parts := make([]string, len(all))
		for i, e := range all {
			parts[i] = Tag(e)
		}
		return strings.Join(parts, "→")
	}
	return Tag(resolved)
}
