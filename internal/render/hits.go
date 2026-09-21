package render

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/eitanpo/agentry/internal/jsonl"
	"github.com/eitanpo/agentry/internal/search"
)

// hitSeparator divides a hit line's three fields. It is the separator the
// header and the closing card already use, and it cannot occur in a turn
// number or a part name, so a consumer splitting on it reaches the matched text
// in one cut however many separators that text holds itself.
const hitSeparator = " · "

// Hits writes one line per hit: the turn to go read, where inside that turn the
// line sits, and the whole matching line. Line-oriented on purpose — the gap
// this closes was a reader rendering a session to a file and grepping it, so
// what replaces that had better pipe.
//
// The matched text is never cut to the terminal's width. A long line wraps for
// display, which costs the reader a wrapped line and leaves them the fact;
// cutting it would delete the half a hit exists to show, and PRODUCT.md's CLI
// conventions prefer a wrap to a drop for exactly that reason. Nothing caps the
// number of hits either: the caller pipes to `head` the way they would any
// search, and a cap agentry chose would hide matches a pattern asked for.
func Hits(w io.Writer, hits []search.Hit, re *regexp.Regexp, color bool) error {
	if !color {
		// The same global the session render sets: under the Ascii profile every
		// style renders to plain text, so one styling path serves both.
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	r := &renderer{opts: Options{Color: color}}
	r.initStyles()
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(r.dim.Render(fmt.Sprintf("turn %d", h.Turn)))
		b.WriteString(hitSeparator)
		b.WriteString(r.tool.Render(hitLocation(h)))
		b.WriteString(hitSeparator)
		b.WriteString(r.highlight(h.Text, re))
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// hitLocation names where inside the turn the line sits: the calls delegated
// through to reach it, then which piece of the record matched. A hit in the
// main thread is the part alone.
func hitLocation(h search.Hit) string {
	var parts []string
	parts = append(parts, h.Delegation...)
	where := fmt.Sprintf("%s:%d", h.Part, h.Line)
	// The line number rides the part, the way a compiler writes file:line, because
	// a hit deep in a long body is a different errand from one at its head: a
	// result body renders only its first ten lines, so a line past that is a hit
	// no render will show and the caller needs the machine form to read it. It was
	// left to --format json until the accounting said otherwise — one more field
	// costs a few characters on a line and no extra lines at all.
	//
	// A tool's own body also names the call, since three of the six parts can only
	// occur inside one and the part alone would not say which. It is named by the
	// same label the delegation chain above uses, so one call does not read two
	// ways depending on whether the hit was inside it or beneath it.
	if h.Tool != "" {
		where = search.Label(h.Tool, h.Identity, h.Model) + " " + where
	}
	parts = append(parts, where)
	return strings.Join(parts, " › ")
}

// highlight emphasizes every match inside one line. With color off it returns
// the line unchanged: the line is the content and the emphasis is an aid, so
// nothing here is the only carrier of a fact.
func (r *renderer) highlight(line string, re *regexp.Regexp) string {
	if !r.opts.Color || re == nil {
		return line
	}
	spans := re.FindAllStringIndex(line, -1)
	if spans == nil {
		return line
	}
	var b strings.Builder
	at := 0
	for _, s := range spans {
		// A zero-width match advances nothing and would emphasize an empty span, so
		// it is passed over rather than marked.
		if s[0] >= s[1] {
			continue
		}
		b.WriteString(r.body.Render(line[at:s[0]]))
		b.WriteString(r.user.Render(line[s[0]:s[1]]))
		at = s[1]
	}
	b.WriteString(r.body.Render(line[at:]))
	return b.String()
}

// HitsJSON writes the hits as one indented array — the machine form of the same
// facts, carrying each hit's line number within its body, which the text lines
// leave out. Always a well-formed array, `[]` where nothing matched, the rule
// the listing's array already follows so a consumer can pipe into jq without a
// guard.
func HitsJSON(w io.Writer, hits []search.Hit) error {
	if hits == nil {
		hits = []search.Hit{}
	}
	b, err := json.MarshalIndent(hits, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// HitsJSONL writes one `hit` record per line under the standard envelope. A run
// that matched nothing writes nothing rather than an empty array, there being no
// wrapper to emit — the rule the listing's stream already follows.
//
// session rides the envelope rather than the payload, so a stream merged from
// two searches says which session each hit came from after a `cat`.
func HitsJSONL(w io.Writer, hits []search.Hit, session string) error {
	enc := jsonl.New(w)
	for _, h := range hits {
		if err := enc.Emit("hit", session, time.Time{}, h); err != nil {
			return err
		}
	}
	return nil
}
