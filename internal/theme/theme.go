// Package theme is the one place agentry names a color. Every surface — the
// listing, a rendered session, a search result, the cost roll-up — asks for a
// role here rather than picking a code of its own, so the same fact is drawn the
// same way wherever a reader meets it.
//
// The roles are named for the job the text does, never for its hue. A caller
// asking for "the row's own facts" keeps reading correctly if that stops being
// gray, where a caller asking for "gray" has written the palette into its own
// file. Four packages had done exactly that, and one row's time, count, label,
// id and title were each drawn differently from the same fields in the listing
// beside it — nothing looked broken on either surface alone.
//
// Styles are returned by value, so a caller may chain Bold, Width or Background
// onto one without changing what anybody else gets.
package theme

import "github.com/charmbracelet/lipgloss"

// The palette, as terminal color codes. They sit together so the whole scheme
// can be read at once, and nothing outside this file names one: a color that
// appears in two files is two colors the moment one of them is edited.
//
// Codes 1-7 are the terminal's own scheme, which a reader has already chosen and
// can read; the 8-bit codes are for the shades between them, where no named
// entry says "quieter than the text but louder than the background".
// The palette, as terminal color codes. They sit together so the whole scheme
// can be read at once, and nothing outside this file names one: a color that
// appears in two files is two colors the moment one of them is edited.
//
// Codes 1-7 are the terminal's own scheme, which a reader has already chosen and
// can read; the 8-bit codes are for the shades between them, where no named
// entry says "quieter than the text but louder than the background".
const (
	codeDim      = "8"   // bright black: quieter than body text on either background
	codeMeta     = "250" // light gray, still legible where dim would be chrome
	codeBody     = "15"  // bright white
	codeArgs     = "248" // light gray, quieter than the call it annotates
	codeThinking = "243" // medium gray, readable but secondary
	codeLink     = "80"  // sky cyan, distinct from glamour's heading blue (39)
	codePromptBg = "237" // the prompt row's highlight
	codeBorder   = "7"
	codeRed      = "1"
	codeGreen    = "2"
	codeYellow   = "3"
	codeBlue     = "4"
	codeMagenta  = "5"
	codeCyan     = "6"
)

// Meta is a row's own facts: when it happened, how long it took, the half of an
// id a reader has to type. Legible rather than loud — these are what a row is
// scanned by, and they sit beside quieter chrome rather than competing with the
// title.
func Meta() lipgloss.Style { return fg(codeMeta) }

// Dim is everything secondary on a row and every piece of chrome: a count, a
// path label, a rail, a table's note, the half of an id nobody types. It is the
// one role a reader is meant to skip over, which is why so much uses it.
func Dim() lipgloss.Style { return fg(codeDim) }

// Body is a verbatim body's own text — a tool's result, a machine-readable
// value. Brighter than the prose around it because it is quoted material and the
// quoting has to be visible.
func Body() lipgloss.Style { return fg(codeBody) }

// Plain is text that takes the terminal's own foreground: a title, a session's
// prose. It is a role rather than an absence, because "draw this in whatever the
// reader chose" is a decision — a fixed near-white would be the brightest thing
// on a dark background and nearly the background itself on a light one.
func Plain() lipgloss.Style { return lipgloss.NewStyle() }

// Prompt is the glyph leading a prompt somebody typed, on the prompt row's
// highlight. PromptRow is that highlight alone, for the rest of the row, and
// PromptBackground is the color the two share — a renderer that draws a box
// around the row needs the color rather than a style.
func Prompt() lipgloss.Style {
	return fg(codeCyan).Bold(true).Background(PromptBackground())
}
func PromptRow() lipgloss.Style        { return lipgloss.NewStyle().Background(PromptBackground()) }
func PromptBackground() lipgloss.Color { return lipgloss.Color(codePromptBg) }

// Match is the span of a line a search pattern matched. It is drawn like a typed
// prompt — the same highlight, for the same reason: both are the words the reader
// themselves supplied, found again in the log. Named separately all the same,
// because a scheme that changes how a prompt looks should not silently change
// how a match looks.
func Match() lipgloss.Style { return Prompt() }

// Instruction is the prompt's own color without the prompt row's highlight, for
// the brief a delegated run was handed: a subagent reads its instruction the way
// a session reads a prompt, so the two are drawn alike.
func Instruction() lipgloss.Style { return fg(codeCyan).Bold(true) }

// Assistant heads what the model wrote — the reply's glyph and the session
// header's own lines.
func Assistant() lipgloss.Style { return fg(codeMagenta).Bold(true) }

// Tool names a tool call. Subagent names one that spawned a child session, which
// is a different kind of event and not a louder one.
func Tool() lipgloss.Style     { return fg(codeYellow).Bold(true) }
func Subagent() lipgloss.Style { return fg(codeBlue).Bold(true) }

// Thinking is the model's reasoning: italic as well as quiet, because it is the
// one channel a reader may want to skim past without reading.
func Thinking() lipgloss.Style { return fg(codeThinking).Italic(true) }

// OK and Bad are a call's outcome. Both carry a glyph as well as a color, since
// color alone cannot be the only carrier of a fact.
func OK() lipgloss.Style  { return fg(codeGreen).Bold(true) }
func Bad() lipgloss.Style { return fg(codeRed).Bold(true) }

// Args is a call's argument parenthetical, quieter than the call it annotates.
func Args() lipgloss.Style { return fg(codeArgs) }

// Link is hyperlinked text.
func Link() lipgloss.Style { return fg(codeLink) }

// Border is the rule around a box.
func Border() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(codeBorder)).
		Padding(0, 1)
}

// Plot is a drawn line — the braille plot above a table of figures. Heat is a
// shaded square in a calendar. Both adapt to the terminal's background, having
// to sit against it rather than beside text.
func Plot() lipgloss.Style { return adaptive("31", "80") }
func Heat() lipgloss.Style { return adaptive("94", "180") }

// Magnitude shades something drawn by rank, loudest first — a bar's five bands.
// The shade repeats what the bar's own length and the figure beside it already
// say, so nothing is distinguished by color alone. A rank past the last band
// takes the terminal's own foreground.
//
// Ranks rather than values: which share earns which band is the caller's policy,
// and how loud a band is drawn is this file's.
func Magnitude(rank int) lipgloss.Style {
	bands := [][2]string{
		{"160", "203"},
		{"166", "215"},
		{"136", "179"},
		{"64", "108"},
		{"66", "109"},
	}
	if rank < 0 || rank >= len(bands) {
		return lipgloss.NewStyle()
	}
	return adaptive(bands[rank][0], bands[rank][1])
}

func adaptive(light, dark string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: light, Dark: dark})
}

func fg(code string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(code))
}
