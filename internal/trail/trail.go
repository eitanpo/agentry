// Package trail spells a per-session setting that may have changed mid-way:
// model, reasoning effort, entrypoint. All three are read as "the last value
// wins, and the whole sequence is kept only when it diverged", so all three
// display the same way — the values joined by an arrow.
//
// It is its own package for the reason the entrypoint package is: the renderer
// and the listing both show these settings, they are siblings, and the arrow is
// a value the two must agree on for one session to read alike in both.
package trail

import "strings"

// Arrow separates the values of a setting that changed mid-session.
const Arrow = "→"

// Of joins the whole sequence when the setting changed, and returns the
// resolved value alone when it did not. all is empty or single for the common
// case, matching what the parser stores: the full list is kept only when it
// holds more than one value.
func Of(resolved string, all []string) string {
	if len(all) > 1 {
		return strings.Join(all, Arrow)
	}
	return resolved
}
