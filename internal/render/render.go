// Package render turns a model.Session into a styled terminal view: glamour
// renders the markdown bodies (prose, code blocks), lipgloss draws the chrome
// (boxes, per-actor glyphs, color). When color is off the same layout is
// emitted as plain text.
package render

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/muesli/termenv"
)

const (
	fallbackWidth    = 100 // used when stdout is not a TTY
	toolBodyMaxLines = 10
	assistantIndent  = "  " // left pad before the assistant turn's rail (│ … ╰─)
	glyphUser        = "❯"
	glyphClaude      = "◆"
	glyphTool        = "●"
	glyphSubagent    = "▶"
	glyphThinking    = "✻"
	glyphOK          = "✓"
	glyphErr         = "✗"
)

// Channels selects which optional sections render.
type Channels struct {
	Thinking, Tools, Subagents, Metrics bool
}

// Options configures a render pass.
type Options struct {
	Width    int
	Color    bool
	Channels Channels
}

type renderer struct {
	opts    Options
	gcache  map[int]*glamour.TermRenderer
	user    lipgloss.Style
	userRow lipgloss.Style
	claude  lipgloss.Style
	tool    lipgloss.Style
	subnt   lipgloss.Style
	think   lipgloss.Style
	ok      lipgloss.Style
	bad     lipgloss.Style
	body    lipgloss.Style
	args    lipgloss.Style
	link    lipgloss.Style
	dim     lipgloss.Style
	border  lipgloss.Style
	userBox lipgloss.Style
}

// Session writes the styled session to w.
func Session(w io.Writer, s *model.Session, opts Options) error {
	if opts.Width <= 0 {
		opts.Width = fallbackWidth
	}
	if !opts.Color {
		lipgloss.SetColorProfile(termenv.Ascii) // strips all ANSI from styles
	}
	r := &renderer{opts: opts, gcache: map[int]*glamour.TermRenderer{}}
	r.initStyles()

	var b strings.Builder
	b.WriteString(r.header(s))
	for _, t := range s.Turns {
		b.WriteString("\n")
		b.WriteString(r.turn(t))
	}
	if opts.Channels.Metrics {
		b.WriteString("\n")
		b.WriteString(r.summary(s))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (r *renderer) initStyles() {
	c := func(code string) lipgloss.Color { return lipgloss.Color(code) }
	userBg := c("237")                                                            // prompt-row highlight
	r.user = lipgloss.NewStyle().Foreground(c("6")).Bold(true).Background(userBg) // cyan ❯ on highlight
	r.userRow = lipgloss.NewStyle().Background(userBg)
	r.claude = lipgloss.NewStyle().Foreground(c("5")).Bold(true)    // magenta
	r.tool = lipgloss.NewStyle().Foreground(c("3")).Bold(true)      // yellow
	r.subnt = lipgloss.NewStyle().Foreground(c("4")).Bold(true)     // blue
	r.think = lipgloss.NewStyle().Foreground(c("243")).Italic(true) // medium gray, readable but secondary
	r.ok = lipgloss.NewStyle().Foreground(c("2")).Bold(true)        // green
	r.bad = lipgloss.NewStyle().Foreground(c("1")).Bold(true)       // red
	r.body = lipgloss.NewStyle().Foreground(c("15"))                // tool result body: bright white
	r.args = lipgloss.NewStyle().Foreground(c("248"))               // tool args parenthetical: light gray
	r.link = lipgloss.NewStyle().Foreground(c("80"))                // hyperlink text: sky cyan, distinct from glamour's heading blue (39) (underline omitted — lipgloss renders it per-rune)
	r.dim = lipgloss.NewStyle().Foreground(c("8"))
	r.border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c("7")).
		Padding(0, 1)
	r.userBox = r.border // prompt box: border + padding sit on the highlight
	if r.opts.Color {    // guard: BorderBackground emits empty ANSI under the Ascii profile
		r.userBox = r.border.Background(userBg).BorderBackground(userBg)
	}
}

// ── Session header ─────────────────────────────────────────────────────────

func (r *renderer) header(s *model.Session) string {
	m := s.Meta
	title := fmt.Sprintf("Session · %s → %s", fmtTime(m.Start), fmtTime(m.End))
	if d := fmtDuration(m.Start, m.End); d != "" {
		title += " · " + d
	}
	title += " · " + m.Model

	tools, errs := 0, 0
	for _, t := range s.Turns {
		tools += t.ToolCount
		errs += t.ErrorCount
	}
	counts := []string{
		plural(len(s.Turns), "turn"), plural(tools, "tool"),
	}
	if errs > 0 {
		counts = append(counts, r.bad.Render(plural(errs, "error")))
	}
	if m.NumSubagents > 0 {
		counts = append(counts, plural(m.NumSubagents, "subagent"))
	}

	tokens := fmt.Sprintf("Tokens: %s in / %s out", fmtTok(m.Usage.Input), fmtTok(m.Usage.Output))
	if cacheIn := m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheCreate; cacheIn > 0 {
		tokens += fmt.Sprintf("  ·  cache %.0f%%", float64(m.Usage.CacheRead)/float64(cacheIn)*100)
	}

	body := r.claude.Render(title) + "\n" +
		strings.Join(counts, " · ") + "\n" +
		r.dim.Render(tokens)
	return r.box(body) + "\n"
}

func (r *renderer) box(content string) string {
	w := r.opts.Width - 2
	if w < 20 {
		w = 20
	}
	return r.border.Width(w).Render(content) + "\n"
}

// ── Turns ────────────────────────────────────────────────────────────────

func (r *renderer) turn(t model.Turn) string {
	var b strings.Builder
	b.WriteString(r.userPrompt(t.Prompt))

	bar := assistantIndent + r.dim.Render("│") + " "
	b.WriteString(assistantIndent + r.claude.Render(glyphClaude) + "\n")
	for _, line := range r.events(t.Events, bar, 0) {
		b.WriteString(line + "\n")
	}
	b.WriteString(r.turnClose(t) + "\n")
	return b.String()
}

// userPrompt renders the prompt as a highlighted block enclosed in a rounded
// border, the prompt text prefixed with the ❯ glyph. Wrapped and continuation
// lines hang-indent two columns (the width of "❯ ") so they align under the
// first character of the prompt. The highlight fills the box's inner width so it
// spans edge to edge inside the border.
func (r *renderer) userPrompt(prompt string) string {
	w := r.opts.Width - 2
	if w < 20 {
		w = 20
	}
	inner := w - 2 // text area inside the border's horizontal padding
	lines := wrapPlain(prompt, inner-2)
	for i, line := range lines {
		if i == 0 {
			lines[i] = r.user.Render(glyphUser) + r.userRow.Render(" "+line)
		} else {
			lines[i] = r.userRow.Render("  " + line)
		}
	}
	block := r.userRow.Width(inner).Render(strings.Join(lines, "\n"))
	return r.userBox.Width(w).Render(block) + "\n"
}

func (r *renderer) turnClose(t model.Turn) string {
	parts := []string{r.claude.Render(glyphClaude)}
	if d := fmtDuration(t.Start, t.End); d != "" {
		parts[0] += " " + d
	}
	if t.ToolCount > 0 {
		parts = append(parts, plural(t.ToolCount, "tool"))
	}
	if t.ErrorCount > 0 {
		parts = append(parts, r.bad.Render(plural(t.ErrorCount, "error")))
	}
	return assistantIndent + r.dim.Render("╰─ ") + strings.Join(parts, " · ")
}

// events renders an assistant event stream, each line carrying the left-bar
// prefix. depth controls glamour wrap width for nesting.
func (r *renderer) events(events []model.Event, prefix string, depth int) []string {
	var out []string
	avail := r.opts.Width - lipgloss.Width(prefix)
	for _, e := range events {
		switch e.Kind {
		case model.EventText:
			for _, line := range r.markdown(e.Text, avail) {
				out = append(out, prefix+line)
			}
		case model.EventThinking:
			if !r.opts.Channels.Thinking {
				continue
			}
			for i, line := range wrapPlain(e.Text, avail-2) {
				lead := "  "
				if i == 0 {
					lead = glyphThinking + " "
				}
				out = append(out, prefix+r.think.Render(lead+line))
			}
		case model.EventTool:
			if !r.opts.Channels.Tools {
				continue
			}
			out = append(out, r.toolLines(e.Tool, prefix, depth)...)
		}
		out = append(out, strings.TrimRight(prefix, " ")) // spacer
	}
	for len(out) > 0 && out[len(out)-1] == strings.TrimRight(prefix, " ") {
		out = out[:len(out)-1]
	}
	return out
}

func (r *renderer) toolLines(t *model.Tool, prefix string, depth int) []string {
	glyph, style := glyphTool, r.tool
	if t.Subagent != nil {
		glyph, style = glyphSubagent, r.subnt
	}
	status := r.ok.Render(glyphOK)
	if t.IsError {
		status = r.bad.Render(glyphErr)
	}
	dur := fmtToolDuration(t.Start, t.End)

	head := fmt.Sprintf("%s%s %s%s %s %s",
		prefix, r.dim.Render("╭─"), style.Render(glyph+" "+t.Name),
		r.args.Render("("+truncate(oneLine(t.Args), 60)+")"), status, dur)
	out := []string{strings.TrimRight(head, " ")}

	if t.Subagent != nil && r.opts.Channels.Subagents {
		nested := prefix + r.dim.Render("│") + " "
		return append(out, r.events(t.Subagent, nested, depth+1)...)
	}
	// Otherwise show the (possibly truncated) result body.
	bodyPrefix := prefix + r.dim.Render("│") + " "
	return append(out, r.toolBody(t.Result, bodyPrefix)...)
}

func (r *renderer) toolBody(text, prefix string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	width := r.opts.Width - lipgloss.Width(prefix)
	lines := strings.Split(text, "\n")
	var out []string
	for _, raw := range lines {
		if len(out) >= toolBodyMaxLines {
			extra := len(lines) - len(out)
			out = append(out, prefix+r.dim.Render(fmt.Sprintf("… %s", plural(extra, "more line"))))
			break
		}
		for _, w := range wrapPlain(raw, width) {
			out = append(out, prefix+r.body.Render(w))
			if len(out) >= toolBodyMaxLines {
				break
			}
		}
	}
	return out
}

// markdown renders a body through glamour at the given wrap width, returning
// trimmed lines. With color on, markdown links in the prose become OSC 8
// terminal hyperlinks (see linkifyMarkdown).
func (r *renderer) markdown(text string, width int) []string {
	// With color on, strip [text](url) syntax before glamour so it renders the
	// link text as plain prose (no inline URL noise, no wrap-mangled href), then
	// wrap each rendered text in an OSC 8 hyperlink. With color off, leave the
	// source untouched — glamour emits its default "text url" form.
	src, links := text, []mdLinkSpec(nil)
	if r.opts.Color {
		src, links = stripMarkdownLinks(text)
	}

	var lines []string
	if g := r.glamourFor(width); g == nil {
		lines = wrapPlain(src, width)
	} else if out, err := g.Render(src); err != nil {
		lines = wrapPlain(src, width)
	} else {
		lines = strings.Split(strings.Trim(out, "\n"), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " ") // drop glamour's wrap padding
		}
	}
	if len(links) > 0 {
		r.linkifyMarkdown(lines, links)
	}
	return lines
}

// mdLink matches a markdown inline link [text](url) with no nested brackets,
// parens, or newline.
var mdLink = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]+)\)`)

// mdLinkSpec is one link's visible text and its href, in source order.
type mdLinkSpec struct{ text, url string }

// stripMarkdownLinks reduces each [text](url) to its text, returning the
// rewritten source and the links in source order. glamour renders inline links
// as "text url" with the raw URL shown and no OSC 8, so we keep the URL out of
// glamour entirely and re-attach it as a hyperlink afterward.
func stripMarkdownLinks(text string) (string, []mdLinkSpec) {
	var links []mdLinkSpec
	src := mdLink.ReplaceAllStringFunc(text, func(m string) string {
		sm := mdLink.FindStringSubmatch(m)
		links = append(links, mdLinkSpec{sm[1], sm[2]})
		return sm[1]
	})
	return src, links
}

// linkifyMarkdown wraps each link's visible text in glamour's rendered lines in
// an OSC 8 hyperlink to its href, editing lines in place. glamour interleaves
// SGR color codes through the text, so we match on the ANSI-stripped text and
// splice the wrapper back onto the styled string by offset. The matched text is
// re-rendered in the dedicated link style (replacing glamour's prose styling).
// Links are matched in source order; a link whose text was split across wrapped
// lines simply stays plain (no hyperlink) rather than blocking later links.
func (r *renderer) linkifyMarkdown(lines []string, links []mdLinkSpec) {
	placed := make([]bool, len(links))
	for n, line := range lines {
		plain, idx := stripANSI(line)
		type span struct{ ss, se, li int }
		var spans []span
		cursor := 0 // plain-byte offset; matches advance it to stay ordered
		for li := range links {
			if placed[li] {
				continue
			}
			at := strings.Index(plain[cursor:], links[li].text)
			if at < 0 {
				continue
			}
			s := cursor + at
			e := s + len(links[li].text)
			spans = append(spans, span{idx[s], idx[e], li})
			placed[li] = true
			cursor = e
		}
		if len(spans) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, sp := range spans {
			b.WriteString(line[last:sp.ss])
			b.WriteString("\x1b]8;;" + links[sp.li].url + "\x1b\\")
			b.WriteString(r.link.Render(links[sp.li].text))
			b.WriteString("\x1b]8;;\x1b\\")
			last = sp.se
		}
		b.WriteString(line[last:])
		lines[n] = b.String()
	}
}

// stripANSI returns line with CSI escape sequences removed, plus idx mapping
// each plain byte offset to its offset in the styled line (idx[len(plain)] =
// len(line)), so a match on the plain text can be spliced back onto the styled
// string.
func stripANSI(line string) (string, []int) {
	var plain strings.Builder
	idx := make([]int, 0, len(line))
	for i := 0; i < len(line); {
		if line[i] == 0x1b { // skip a CSI escape: ESC '[' … final byte (0x40–0x7e)
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++ // past '['; its byte 0x5b is itself in the final-byte range
				for j < len(line) && !(line[j] >= 0x40 && line[j] <= 0x7e) {
					j++
				}
				if j < len(line) {
					j++ // include the final byte
				}
			} else {
				j++
			}
			i = j
			continue
		}
		idx = append(idx, i)
		plain.WriteByte(line[i])
		i++
	}
	idx = append(idx, len(line))
	return plain.String(), idx
}

func (r *renderer) glamourFor(width int) *glamour.TermRenderer {
	if width < 20 {
		width = 20
	}
	if g, ok := r.gcache[width]; ok {
		return g
	}
	style := styles.DarkStyleConfig
	if !r.opts.Color {
		style = styles.NoTTYStyleConfig
	}
	// Drop glamour's 2-space document margin so prose hugs the left rail; the
	// rail prefix (assistantIndent + "│ ") supplies all the indentation.
	zero := uint(0)
	style.Document.Margin = &zero
	g, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		g = nil
	}
	r.gcache[width] = g
	return g
}

// ── Summary table (metrics channel) ────────────────────────────────────────

func (r *renderer) summary(s *model.Session) string {
	if len(s.Turns) == 0 {
		return ""
	}
	type row struct {
		n     int
		tok   int
		tools int
		label string
	}
	var rows []row
	total := 0
	for i, t := range s.Turns {
		tok := t.Usage.Input + t.Usage.Output
		total += tok
		rows = append(rows, row{i + 1, tok, t.ToolCount, oneLine(t.Prompt)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].tok > rows[j].tok })

	var b strings.Builder
	b.WriteString(r.dim.Render("── Summary (by token cost) ──") + "\n")
	b.WriteString(r.dim.Render("    % tok    tokens  tools  step") + "\n")
	limit := min(len(rows), 8)
	for _, rw := range rows[:limit] {
		pct := 0.0
		if total > 0 {
			pct = float64(rw.tok) / float64(total) * 100
		}
		b.WriteString(fmt.Sprintf("  %5.1f%%  %8s  %5d  %d. %s\n",
			pct, fmtTok(rw.tok), rw.tools, rw.n, truncate(rw.label, max(r.opts.Width-30, 20))))
	}
	if rest := len(rows) - limit; rest > 0 {
		b.WriteString(r.dim.Render(fmt.Sprintf("  …  (%s)\n", plural(rest, "more step"))))
	}
	return b.String()
}

// ── Formatting helpers ──────────────────────────────────────────────────────

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "??:??"
	}
	return t.Local().Format("15:04")
}

func fmtDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	secs := int(end.Sub(start).Seconds())
	if secs < 0 {
		return ""
	}
	h, m := secs/3600, (secs%3600)/60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func fmtToolDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	secs := end.Sub(start).Seconds()
	if secs < 0 {
		return ""
	}
	switch {
	case secs < 10:
		return fmt.Sprintf("%.1fs", secs)
	case secs < 60:
		return fmt.Sprintf("%.0fs", secs)
	}
	mins, rem := int(secs)/60, int(secs)%60
	if mins < 60 {
		if rem == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%02ds", mins, rem)
	}
	return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
}

func fmtTok(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.0fk", float64(n)/1000)
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// wrapPlain soft-wraps plain text (no ANSI) to maxWidth runes per line.
func wrapPlain(text string, maxWidth int) []string {
	if maxWidth < 10 {
		maxWidth = 10
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		if len([]rune(raw)) <= maxWidth {
			out = append(out, raw)
			continue
		}
		var line strings.Builder
		for _, word := range strings.Fields(raw) {
			switch {
			case line.Len() == 0:
				line.WriteString(word)
			case len([]rune(line.String()))+1+len([]rune(word)) > maxWidth:
				out = append(out, line.String())
				line.Reset()
				line.WriteString(word)
			default:
				line.WriteByte(' ')
				line.WriteString(word)
			}
		}
		if line.Len() > 0 {
			out = append(out, line.String())
		}
	}
	return out
}
