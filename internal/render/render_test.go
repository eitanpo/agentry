package render

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/eitanpo/agentry/internal/model"
)

// TestSessionJSON pins the --format json shape: the full model, event kinds as
// strings not ordinals, nested subagent streams, and isError elided when false.
func TestSessionJSON(t *testing.T) {
	sess := &model.Session{
		Meta: model.Meta{ID: "s1", Model: "claude-opus-4-8", Usage: model.Usage{Input: 10, Output: 20}},
		Turns: []model.Turn{{
			Prompt:    "do it",
			ToolCount: 2,
			Events: []model.Event{
				{Kind: model.EventText, Text: "sure"},
				{Kind: model.EventThinking, Text: "hmm"},
				{Kind: model.EventTool, Tool: &model.Tool{
					Name: "Bash", Args: "ls", Result: "boom", IsError: true,
					Subagent: []model.Event{{Kind: model.EventText, Text: "child"}},
				}},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Read"}},
			},
		}},
	}
	var b strings.Builder
	if err := SessionJSON(&b, sess); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}

	meta := got["meta"].(map[string]any)
	if meta["id"] != "s1" || meta["model"] != "claude-opus-4-8" {
		t.Errorf("meta wrong: %s", b.String())
	}
	if usage := meta["usage"].(map[string]any); usage["input"].(float64) != 10 || usage["output"].(float64) != 20 {
		t.Errorf("usage wrong: %s", b.String())
	}

	turns := got["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("want 1 turn, got %d: %s", len(turns), b.String())
	}
	events := turns[0].(map[string]any)["events"].([]any)
	if len(events) != 4 {
		t.Fatalf("want 4 events, got %d: %s", len(events), b.String())
	}
	// Event kinds serialize as stable strings, not iota ordinals.
	kinds := []string{"text", "thinking", "tool", "tool"}
	for i, want := range kinds {
		if k := events[i].(map[string]any)["kind"]; k != want {
			t.Errorf("event %d kind = %v, want %q", i, k, want)
		}
	}
	// The erroring tool carries its result, isError=true, and a nested stream.
	tool := events[2].(map[string]any)["tool"].(map[string]any)
	if tool["name"] != "Bash" || tool["isError"] != true || tool["result"] != "boom" {
		t.Errorf("tool wrong: %s", b.String())
	}
	sub := tool["subagent"].([]any)
	if len(sub) != 1 || sub[0].(map[string]any)["kind"] != "text" {
		t.Errorf("subagent stream wrong: %s", b.String())
	}
	// A non-erroring tool omits isError entirely (false is elided).
	okTool := events[3].(map[string]any)["tool"].(map[string]any)
	if _, present := okTool["isError"]; present {
		t.Errorf("isError should be omitted when false: %s", b.String())
	}
}

func minimalSession() *model.Session {
	return &model.Session{
		Meta: model.Meta{Model: "claude-opus-4-7"},
		Turns: []model.Turn{{
			Prompt: "hi there",
			Events: []model.Event{{Kind: model.EventText, Text: "hello back"}},
		}},
	}
}

func TestPlural(t *testing.T) {
	tests := []struct {
		n    int
		noun string
		want string
	}{
		{1, "tool", "1 tool"}, {2, "tool", "2 tools"}, {0, "error", "0 errors"},
	}
	for _, tt := range tests {
		if got := plural(tt.n, tt.noun); got != tt.want {
			t.Errorf("plural(%d, %q) = %q, want %q", tt.n, tt.noun, got, tt.want)
		}
	}
}

func TestFmtToolDuration(t *testing.T) {
	base := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		start, end time.Time
		want       string
	}{
		{"sub-10s", base, base.Add(1500 * time.Millisecond), "1.5s"},
		{"seconds", base, base.Add(12 * time.Second), "12s"},
		{"minutes", base, base.Add(90 * time.Second), "1m30s"},
		{"whole minute", base, base.Add(2 * time.Minute), "2m"},
		{"zero end", base, time.Time{}, ""},
		{"negative", base.Add(time.Second), base, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtToolDuration(tt.start, tt.end); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFmtDuration(t *testing.T) {
	base := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		start, end time.Time
		want       string
	}{
		{"minutes", base, base.Add(5 * time.Minute), "5m"},
		{"hours", base, base.Add(time.Hour + time.Minute), "1h01m"},
		{"zero", time.Time{}, base, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtDuration(tt.start, tt.end); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrapPlain(t *testing.T) {
	t.Run("short line unchanged", func(t *testing.T) {
		got := wrapPlain("hello world", 40)
		if len(got) != 1 || got[0] != "hello world" {
			t.Errorf("got %q, want one line", got)
		}
	})
	t.Run("wraps at width", func(t *testing.T) {
		got := wrapPlain("aaaa bbbb cccc dddd", 10)
		if len(got) < 2 {
			t.Fatalf("expected multiple lines, got %q", got)
		}
		for _, line := range got {
			if len([]rune(line)) > 10 {
				t.Errorf("line %q exceeds width 10", line)
			}
		}
	})
	t.Run("preserves explicit newlines", func(t *testing.T) {
		got := wrapPlain("a\nb", 40)
		if len(got) != 2 {
			t.Errorf("got %q, want 2 lines", got)
		}
	})
}

func TestExtractLinks(t *testing.T) {
	src, links := extractLinks(
		"Top: [Researcher task](obsidian://open?vault=research&file=06-Tasks%2FR.md) — see [Diffy](obsidian://open?vault=research&file=02-Wiki%2FDiffy.md).")
	wantSrc := "Top: Researcher task — see Diffy."
	if src != wantSrc {
		t.Errorf("src = %q, want %q", src, wantSrc)
	}
	if len(links) != 2 || links[0].text != "Researcher task" || links[1].text != "Diffy" {
		t.Fatalf("links = %+v", links)
	}
	if links[0].url != "obsidian://open?vault=research&file=06-Tasks%2FR.md" {
		t.Errorf("links[0].url = %q", links[0].url)
	}
	if got := mustExtract(t, "no links here"); got != "no links here" {
		t.Errorf("plain text altered: %q", got)
	}
}

func TestExtractLinksBareURL(t *testing.T) {
	t.Run("bare url stays in source, becomes its own link", func(t *testing.T) {
		src, links := extractLinks("Visit https://example.com for more.")
		if src != "Visit https://example.com for more." {
			t.Errorf("bare URL altered in source: %q", src)
		}
		if len(links) != 1 || links[0].text != "https://example.com" || links[0].url != "https://example.com" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("trailing sentence punctuation excluded from href", func(t *testing.T) {
		_, links := extractLinks("See https://example.com.")
		if len(links) != 1 || links[0].url != "https://example.com" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("url inside a markdown link is not double-matched", func(t *testing.T) {
		_, links := extractLinks("A [labeled](https://example.com/x) link.")
		if len(links) != 1 || links[0].text != "labeled" || links[0].url != "https://example.com/x" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("markdown link and bare url kept in source order", func(t *testing.T) {
		_, links := extractLinks("bare https://a.com then [txt](https://b.com)")
		if len(links) != 2 || links[0].url != "https://a.com" || links[1].url != "https://b.com" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("bare obsidian open URI collapses to a [[wikilink]] label", func(t *testing.T) {
		uri := "obsidian://open?vault=research&file=02-Wiki%2FDiffy.md"
		src, links := extractLinks("see " + uri + " here")
		if src != "see [[Diffy]] here" {
			t.Errorf("src = %q, want %q", src, "see [[Diffy]] here")
		}
		if len(links) != 1 || links[0].text != "[[Diffy]]" || links[0].url != uri {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("obsidian label keeps a heading anchor", func(t *testing.T) {
		_, links := extractLinks("obsidian://open?vault=v&file=Note%23Heading")
		if len(links) != 1 || links[0].text != "[[Note#Heading]]" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("trailing punctuation stays in prose after the label", func(t *testing.T) {
		src, links := extractLinks("see obsidian://open?vault=v&file=Note.md.")
		if src != "see [[Note]]." {
			t.Errorf("src = %q, want %q", src, "see [[Note]].")
		}
		if links[0].url != "obsidian://open?vault=v&file=Note.md" {
			t.Errorf("href = %q", links[0].url)
		}
	})
	t.Run("obsidian URI without a file stays a raw bare link", func(t *testing.T) {
		uri := "obsidian://search?query=diffy"
		src, links := extractLinks("run " + uri + " now")
		if src != "run "+uri+" now" {
			t.Errorf("src altered: %q", src)
		}
		if len(links) != 1 || links[0].text != uri || links[0].url != uri {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("any scheme:// is autolinked as a raw bare link", func(t *testing.T) {
		for _, uri := range []string{"ftp://host/file", "vscode://file/tmp/x.go:12", "myapp+beta://do/it"} {
			_, links := extractLinks("go " + uri + " now")
			if len(links) != 1 || links[0].text != uri || links[0].url != uri {
				t.Errorf("%q: links = %+v", uri, links)
			}
		}
	})
	t.Run("scheme without // is not autolinked", func(t *testing.T) {
		if got := mustExtract(t, "write mailto:a@b.com or tel:123 now"); got != "write mailto:a@b.com or tel:123 now" {
			t.Errorf("altered: %q", got)
		}
	})
	t.Run("balanced parens kept, wrapping paren dropped", func(t *testing.T) {
		wiki := "https://en.wikipedia.org/wiki/Ruby_(programming_language)"
		_, links := extractLinks("see (" + wiki + ") ok")
		if len(links) != 1 || links[0].url != wiki {
			t.Fatalf("balanced-paren URL truncated: links = %+v", links)
		}
	})
	t.Run("trailing paren and period peeled off the href", func(t *testing.T) {
		_, links := extractLinks("see https://x.com/foo).")
		if len(links) != 1 || links[0].url != "https://x.com/foo" {
			t.Fatalf("links = %+v", links)
		}
	})
	t.Run("obsidian note name with markdown chars is escaped in source", func(t *testing.T) {
		uri := "obsidian://open?vault=v&file=My%20%2ANote%2A.md" // file = "My *Note*.md"
		src, links := extractLinks("see " + uri + " x")
		if len(links) != 1 || links[0].text != "[[My *Note*]]" || links[0].url != uri {
			t.Fatalf("links = %+v", links)
		}
		if !strings.Contains(src, `[[My \*Note\*]]`) {
			t.Errorf("note name not escaped in source: %q", src)
		}
	})
}

func mustExtract(t *testing.T, s string) string {
	t.Helper()
	out, links := extractLinks(s)
	if len(links) != 0 {
		t.Fatalf("expected no links, got %+v", links)
	}
	return out
}

// stripOSC removes OSC 8 hyperlink sequences (ESC ] … ST) so the remaining
// CSI-styled text can be checked for what's actually visible.
func stripOSC(s string) string {
	for {
		i := strings.Index(s, "\x1b]")
		if i < 0 {
			return s
		}
		end := strings.Index(s[i:], "\x1b\\")
		if end < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+end+2:]
	}
}

func TestLinkifyMarkdown(t *testing.T) {
	const osc = "\x1b]8;;"
	r := &renderer{}
	r.initStyles()
	// visible reduces a styled line to the text the user actually sees.
	visible := func(s string) string { p, _ := stripANSI(stripOSC(s)); return p }

	t.Run("wraps styled text, drops url from view", func(t *testing.T) {
		// glamour fragments the text with SGR codes; linkify must still match it.
		out := []string{"see \x1b[1mResearcher task\x1b[0m here"}
		url := "obsidian://open?vault=research&file=R.md"
		r.linkifyMarkdown(out, []mdLinkSpec{{text: "Researcher task", url: url}})
		if !strings.Contains(out[0], osc+url+"\x1b\\") || !strings.Contains(out[0], "\x1b]8;;\x1b\\") {
			t.Errorf("missing OSC 8 hyperlink, got %q", out[0])
		}
		if got := visible(out[0]); got != "see Researcher task here" {
			t.Errorf("visible text = %q, want %q", got, "see Researcher task here")
		}
	})

	t.Run("unmatched link leaves line unchanged", func(t *testing.T) {
		out := []string{"nothing to see"}
		r.linkifyMarkdown(out, []mdLinkSpec{{text: "absent", url: "u"}})
		if out[0] != "nothing to see" {
			t.Errorf("line changed: %q", out[0])
		}
	})

	t.Run("multiple links in source order", func(t *testing.T) {
		out := []string{"A and B"}
		r.linkifyMarkdown(out, []mdLinkSpec{{text: "A", url: "ua"}, {text: "B", url: "ub"}})
		if !strings.Contains(out[0], osc+"ua\x1b\\") || !strings.Contains(out[0], osc+"ub\x1b\\") {
			t.Errorf("missing hyperlinks, got %q", out[0])
		}
		if got := visible(out[0]); got != "A and B" {
			t.Errorf("visible text = %q, want %q", got, "A and B")
		}
	})

	t.Run("bare url: visible text is the url, href matches", func(t *testing.T) {
		// glamour colors a bare URL but emits no OSC 8; a bare-URL spec has
		// text == url, so linkify wraps the URL text as its own hyperlink.
		url := "https://example.com"
		out := []string{"Visit \x1b[38;5;30;4m" + url + "\x1b[0m for more."}
		r.linkifyMarkdown(out, []mdLinkSpec{{text: url, url: url}})
		if !strings.Contains(out[0], osc+url+"\x1b\\") || !strings.Contains(out[0], "\x1b]8;;\x1b\\") {
			t.Errorf("missing OSC 8 hyperlink, got %q", out[0])
		}
		if got := visible(out[0]); got != "Visit "+url+" for more." {
			t.Errorf("visible text = %q", got)
		}
	})
}

// TestMarkdownBareURLEndToEnd drives the full color path — extractLinks, glamour,
// then linkifyMarkdown — proving a bare URL in prose emerges as an OSC 8 link
// with a clean href (trailing period excluded), while a URL glamour word-wrapped
// across lines degrades to plain (no OSC 8).
func TestMarkdownBareURLEndToEnd(t *testing.T) {
	r := &renderer{opts: Options{Color: true}, gcache: map[int]*glamour.TermRenderer{}}
	r.initStyles()

	joined := strings.Join(r.markdown("See https://example.com. now", 80), "\n")
	href := "\x1b]8;;https://example.com\x1b\\"
	if !strings.Contains(joined, href) {
		t.Errorf("bare URL not linkified with clean href; got %q", joined)
	}
	if strings.Contains(joined, "\x1b]8;;https://example.com.\x1b\\") {
		t.Errorf("trailing period leaked into href; got %q", joined)
	}

	// A bare obsidian open URI renders as a [[wikilink]] label (visible text)
	// hyperlinked to the full URI (href), not the raw URI string.
	obs := "obsidian://open?vault=v&file=02-Wiki%2FDiffy.md"
	obsOut := strings.Join(r.markdown("Open "+obs+" now", 80), "\n")
	if !strings.Contains(obsOut, "\x1b]8;;"+obs+"\x1b\\") {
		t.Errorf("obsidian href missing; got %q", obsOut)
	}
	if vis, _ := stripANSI(stripOSC(obsOut)); !strings.Contains(vis, "[[Diffy]]") || strings.Contains(vis, "obsidian://") {
		t.Errorf("visible text should be [[Diffy]], not the raw URI; got %q", vis)
	}

	// A URL containing balanced parens keeps them in the href; the wrapping
	// paren is dropped.
	wiki := "https://en.wikipedia.org/wiki/Ruby_(programming_language)"
	wikiOut := strings.Join(r.markdown("see ("+wiki+") ok", 200), "\n")
	if !strings.Contains(wikiOut, "\x1b]8;;"+wiki+"\x1b\\") {
		t.Errorf("balanced-paren URL href truncated; got %q", wikiOut)
	}

	// A note name with markdown-active chars still renders its [[label]]
	// verbatim and stays clickable (the name is escaped before glamour).
	star := "obsidian://open?vault=v&file=My%20%2ANote%2A.md"
	starOut := strings.Join(r.markdown("Ref "+star+" x", 80), "\n")
	if !strings.Contains(starOut, "\x1b]8;;"+star+"\x1b\\") {
		t.Errorf("obsidian href missing for starred note; got %q", starOut)
	}
	if vis, _ := stripANSI(stripOSC(starOut)); !strings.Contains(vis, "[[My *Note*]]") {
		t.Errorf("visible label should be [[My *Note*]]; got %q", vis)
	}

	long := "https://example.com/very/long/path/that/keeps/going/way/past/the/edge/of/the/wrap/width/for/sure"
	wrapped := strings.Join(r.markdown("Long: "+long, 40), "\n")
	if strings.Contains(wrapped, "\x1b]8;;") {
		t.Errorf("wrapped URL should stay plain, but got an OSC 8 link: %q", wrapped)
	}
}

func TestTruncateAndOneLine(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q, want abc…", got)
	}
	if got := truncate("ab", 3); got != "ab" {
		t.Errorf("truncate short = %q, want ab", got)
	}
	// Joined, not ended at the first line: ending there deleted "second" with
	// nothing to mark that it went, and the column applied next cannot mark a cut
	// it never saw.
	if got := oneLine("  first\nsecond  "); got != "first second" {
		t.Errorf("oneLine = %q, want \"first second\"", got)
	}
}

// gatingSession has one turn with a plain tool (a result body) and a subagent
// call (a nested event stream plus its own result body), so a render can be
// probed for which channels surfaced what.
func gatingSession() *model.Session {
	return &model.Session{
		Meta: model.Meta{Model: "claude-opus-4-7"},
		Turns: []model.Turn{{
			Prompt: "go",
			Events: []model.Event{
				{Kind: model.EventText, Text: "RESPONSEMARKER"},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: "TOOLBODYMARKER"}},
				{Kind: model.EventTool, Tool: &model.Tool{
					Name:     "Agent",
					Prompt:   "AGENTPROMPTMARKER",
					Result:   "AGENTRESULTMARKER",
					Subagent: []model.Event{{Kind: model.EventText, Text: "NESTEDMARKER"}},
				}},
			},
		}},
	}
}

func renderChannels(t *testing.T, ch Channels) string {
	t.Helper()
	var b strings.Builder
	if err := Session(&b, gatingSession(), Options{Width: 80, Color: false, Channels: ch}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestChannelGating verifies the activation/body/expansion split: Tools gates
// whether a tool's head line appears, ToolResults gates its result body and a
// delegated call's instruction, and Subagents gates expansion of a nested
// stream (falling through to the ToolResults body when off). The response text
// is always shown.
func TestChannelGating(t *testing.T) {
	has := func(t *testing.T, s, marker string, want bool) {
		t.Helper()
		if got := strings.Contains(s, marker); got != want {
			t.Errorf("contains %q = %v, want %v", marker, got, want)
		}
	}

	t.Run("minimal shows response, no tools", func(t *testing.T) {
		out := renderChannels(t, Channels{})
		has(t, out, "RESPONSEMARKER", true)
		has(t, out, "Read", false)
		has(t, out, "TOOLBODYMARKER", false)
		has(t, out, "NESTEDMARKER", false)
		has(t, out, "AGENTPROMPTMARKER", false)
	})

	t.Run("detailed: activation + expansion, no bodies", func(t *testing.T) {
		out := renderChannels(t, Channels{Thinking: true, Tools: true, Subagents: true, Metrics: true})
		has(t, out, "Read", true)            // tool fired
		has(t, out, "TOOLBODYMARKER", false) // but no result body
		has(t, out, "NESTEDMARKER", true)    // subagent expanded
		has(t, out, "AGENTRESULTMARKER", false)
		// The instruction is a body too, so the level that shows no bodies shows
		// none of it either — the expansion beneath it is the shape of the work.
		has(t, out, "AGENTPROMPTMARKER", false)
	})

	t.Run("full: activation + expansion + bodies", func(t *testing.T) {
		out := renderChannels(t, Channels{Thinking: true, Tools: true, ToolResults: true, Subagents: true, Metrics: true})
		has(t, out, "Read", true)
		has(t, out, "TOOLBODYMARKER", true)
		has(t, out, "NESTEDMARKER", true)
		has(t, out, "AGENTPROMPTMARKER", true)
	})

	t.Run("subagents off falls through to result body", func(t *testing.T) {
		out := renderChannels(t, Channels{Tools: true, ToolResults: true})
		has(t, out, "Agent", true)             // head line present
		has(t, out, "NESTEDMARKER", false)     // not expanded
		has(t, out, "AGENTRESULTMARKER", true) // its result body shown instead
		has(t, out, "AGENTPROMPTMARKER", true) // and the instruction above it either way
	})

	t.Run("tools on, results off: head without body", func(t *testing.T) {
		out := renderChannels(t, Channels{Tools: true})
		has(t, out, "Read", true)
		has(t, out, "TOOLBODYMARKER", false)
		has(t, out, "AGENTPROMPTMARKER", false)
	})
}

// TestResultBodyNamesItsRemainder pins the cap on a result body: ten source
// lines, then a count of what is left. The count is what makes this a cap rather
// than a deletion, so a wrong count is the same defect as no count at all.
//
// The wrapped case is why the test exists. The cap counts display lines and the
// remainder counts source lines, and subtracting one from the other printed
// "… -6 more lines" on any body long enough to wrap — which at a narrow width is
// every long body.
func TestResultBodyNamesItsRemainder(t *testing.T) {
	render := func(t *testing.T, result string, width int) string {
		t.Helper()
		sess := &model.Session{Turns: []model.Turn{{
			Prompt: "go",
			Events: []model.Event{{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: result}}},
		}}}
		var b strings.Builder
		opts := Options{Width: width, Color: false, Channels: Channels{Tools: true, ToolResults: true}}
		if err := Session(&b, sess, opts); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("short lines name the lines left over", func(t *testing.T) {
		lines := make([]string, 0, toolBodyMaxLines*2)
		for i := 0; i < toolBodyMaxLines*2; i++ {
			lines = append(lines, fmt.Sprintf("row%02d", i))
		}
		out := render(t, strings.Join(lines, "\n"), 120)
		want := fmt.Sprintf("… %d more lines", toolBodyMaxLines)
		if !strings.Contains(out, want) {
			t.Errorf("want %q, got %q", want, out)
		}
	})

	t.Run("wrapped lines never name a negative remainder", func(t *testing.T) {
		// Each source line is far wider than the rail, so every one of them wraps
		// into several display lines and the cap is reached partway through the
		// body rather than at a line boundary.
		long := strings.Repeat("wordy ", 40)
		lines := make([]string, 0, toolBodyMaxLines)
		for i := 0; i < toolBodyMaxLines; i++ {
			lines = append(lines, long)
		}
		out := render(t, strings.Join(lines, "\n"), 40)
		// The exact count, not merely a non-negative one: subtracting display
		// lines from source lines printed "-6 more lines" on some widths and
		// "0 more lines" on others, and a remainder of zero beside nine missing
		// lines reads as a complete body.
		if !strings.Contains(out, "… 9 more lines") {
			t.Errorf("want the nine unprinted source lines named, got %q", out)
		}
	})

	t.Run("one very long line shows its head and says so", func(t *testing.T) {
		// The body is a single source line wrapping past the cap. Printing nothing
		// but a remainder would hide the result entirely; printing the head with no
		// remainder would truncate it silently.
		out := render(t, strings.Repeat("token ", 200), 40)
		if !strings.Contains(out, "token") {
			t.Errorf("the head of the line was dropped: %q", out)
		}
		if !strings.Contains(out, "… 1 more line") {
			t.Errorf("want the one unprinted line named, got %q", out)
		}
	})

	t.Run("a body inside the cap names no remainder", func(t *testing.T) {
		out := render(t, "one\ntwo\nthree", 120)
		if strings.Contains(out, "more line") {
			t.Errorf("a body that fit named a remainder: %q", out)
		}
	})
}

// TestDelegatedPromptPrintsWhole pins the instruction an Agent call was handed:
// that it prints at all, that it prints entire where a result body is capped,
// that it stands above the expansion rather than inside it, and that a tool
// carrying no instruction prints no prompt chrome.
//
// The uncapped half is the point of the test. A reader asking what a subagent
// was told is asking for all of it — the whole reason this fact was worth adding
// is that the activation line already carries the description — so a cap here
// would reinstate the gap while looking like a feature.
func TestDelegatedPromptPrintsWhole(t *testing.T) {
	brief := make([]string, 0, toolBodyMaxLines*3)
	for i := 0; i < toolBodyMaxLines*3; i++ {
		brief = append(brief, fmt.Sprintf("BRIEFLINE%02d", i))
	}
	sess := &model.Session{Turns: []model.Turn{{
		Prompt: "go",
		Events: []model.Event{
			{Kind: model.EventTool, Tool: &model.Tool{
				Name:     "Agent",
				Identity: "Explore",
				Args:     "sweep for callers",
				Prompt:   strings.Join(brief, "\n"),
				Subagent: []model.Event{{Kind: model.EventText, Text: "NESTEDMARKER"}},
			}},
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: "TOOLBODYMARKER"}},
		},
	}}}
	var b strings.Builder
	opts := Options{Width: 120, Color: false, Channels: Channels{Tools: true, ToolResults: true, Subagents: true}}
	if err := Session(&b, sess, opts); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, line := range brief {
		if !strings.Contains(out, line) {
			t.Errorf("instruction line %q missing — the brief was truncated", line)
		}
	}
	if strings.Contains(out, "more line") {
		t.Errorf("the instruction was capped, but only a result body is: %q", out)
	}
	// The ❯ glyph is what says "this is what the call asked for" rather than
	// what it returned, the two being otherwise adjacent bodies on one rail.
	if !strings.Contains(out, glyphUser+" "+brief[0]) {
		t.Errorf("want the ❯ glyph opening the instruction, got %q", out)
	}
	// Above the expansion: the call's terms come before the work it produced.
	if strings.Index(out, brief[0]) > strings.Index(out, "NESTEDMARKER") {
		t.Error("the instruction printed below the expanded stream, not above it")
	}
	// A tool that delegates nothing gets no prompt chrome — the gate is the field
	// being set, not the channel being on.
	body := out[strings.Index(out, "TOOLBODYMARKER"):]
	if strings.Contains(body, glyphUser) {
		t.Errorf("a non-delegating call printed a ❯: %q", body)
	}
}

// TestHeaderEffort pins how the header reports reasoning effort. It reads as a
// phrase because "high" alone beside a model name does not say high what.
func TestHeaderEffort(t *testing.T) {
	head := func(t *testing.T, m model.Meta) string {
		t.Helper()
		sess := &model.Session{Meta: m, Turns: []model.Turn{{Prompt: "go"}}}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("named beside the model", func(t *testing.T) {
		out := head(t, model.Meta{Model: "claude-opus-5", Effort: "high"})
		if !strings.Contains(out, "claude-opus-5 · high effort") {
			t.Errorf("want the effort after the model, got %q", out)
		}
	})

	t.Run("a mid-session change shows the transition", func(t *testing.T) {
		out := head(t, model.Meta{Model: "claude-opus-5", Effort: "high", Efforts: []string{"xhigh", "high"}})
		if !strings.Contains(out, "xhigh→high effort") {
			t.Errorf("want the whole sequence, got %q", out)
		}
	})

	t.Run("a session without the field says nothing", func(t *testing.T) {
		// Half of sessions predate it; an invented default would be a claim about
		// how the model was run that the log never made.
		out := head(t, model.Meta{Model: "claude-opus-5"})
		if strings.Contains(out, "effort") {
			t.Errorf("no effort recorded, so none should be shown: %q", out)
		}
	})
}

// TestDenialOnToolLine pins that a refused call says so. Both a denied call and
// a failed one carry isError, so the error glyph alone sends a reader to fix the
// tool when the thing to fix is a permission rule.
func TestDenialOnToolLine(t *testing.T) {
	line := func(t *testing.T, tool *model.Tool) string {
		t.Helper()
		sess := &model.Session{Turns: []model.Turn{{
			Prompt: "go",
			Events: []model.Event{{Kind: model.EventTool, Tool: tool}},
		}}}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: Channels{Tools: true}}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("a denied call names what refused it", func(t *testing.T) {
		out := line(t, &model.Tool{Name: "Bash", Args: "rm -rf /tmp/x", IsError: true, Denial: "permission-rule"})
		if !strings.Contains(out, "denied: permission-rule") {
			t.Errorf("want the denial kind on the line, got %q", out)
		}
	})

	t.Run("a call that ran and failed says nothing about denial", func(t *testing.T) {
		out := line(t, &model.Tool{Name: "Bash", Args: "false", IsError: true})
		if strings.Contains(out, "denied") {
			t.Errorf("an ordinary error is not a denial: %q", out)
		}
	})
}

// TestDelegationMarker pins what an Agent line says about the work it handed
// off. Its args are the human description, so without this the line names the
// task and never which agent ran it or on what model.
func TestDelegationMarker(t *testing.T) {
	line := func(t *testing.T, tool *model.Tool) string {
		t.Helper()
		sess := &model.Session{Turns: []model.Turn{{
			Prompt: "go",
			Events: []model.Event{{Kind: model.EventTool, Tool: tool}},
		}}}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: Channels{Tools: true}}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("type and model", func(t *testing.T) {
		out := line(t, &model.Tool{Name: "Agent", Identity: "Explore", Model: "haiku", Args: "sweep"})
		if !strings.Contains(out, "Agent[Explore@haiku]") {
			t.Errorf("want Agent[Explore@haiku] on the line, got %q", out)
		}
	})

	t.Run("no model named shows the type alone", func(t *testing.T) {
		// The subagent inherited the session's model. Printing that model here
		// would report a choice the caller never made.
		out := line(t, &model.Tool{Name: "Agent", Identity: "researcher", Args: "research"})
		if !strings.Contains(out, "Agent[researcher]") {
			t.Errorf("want Agent[researcher], got %q", out)
		}
		if strings.Contains(out, "@") {
			t.Errorf("no model was named, so nothing should follow @: %q", out)
		}
	})

	t.Run("neither named leaves no empty brackets", func(t *testing.T) {
		out := line(t, &model.Tool{Name: "Agent", Args: "unnamed"})
		if strings.Contains(out, "[") {
			t.Errorf("an Agent naming neither should print no brackets: %q", out)
		}
	})

	t.Run("other tools are not bracketed", func(t *testing.T) {
		// Bash carries an identity too, but its args already open with the
		// program — bracketing it would repeat what the line already shows.
		out := line(t, &model.Tool{Name: "Bash", Identity: "git", Args: "git status"})
		if strings.Contains(out, "Bash[") {
			t.Errorf("only Agent is bracketed, got %q", out)
		}
	})
}

func TestSessionPlainNoANSI(t *testing.T) {
	// A minimal render must contain no ESC bytes when color is off.
	var b strings.Builder
	err := Session(&b, minimalSession(), Options{Width: 80, Color: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(b.String(), '\x1b') {
		t.Error("plain render contains ANSI escape bytes")
	}
	if !strings.Contains(b.String(), "hi there") {
		t.Error("render missing prompt text")
	}
}

// TestHeaderModel pins how the header names what a session ran on. The model
// used to be printed unconditionally from the first assistant entry, so a
// session that switched was reported as still on the model it left, and one
// whose log names none was reported as "unknown".
func TestHeaderModel(t *testing.T) {
	head := func(t *testing.T, m model.Meta) string {
		t.Helper()
		sess := &model.Session{Meta: m, Turns: []model.Turn{{Prompt: "go"}}}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("a mid-session switch shows the transition", func(t *testing.T) {
		out := head(t, model.Meta{Model: "claude-opus-5", Models: []string{"claude-sonnet-5", "claude-opus-5"}})
		if !strings.Contains(out, "claude-sonnet-5→claude-opus-5") {
			t.Errorf("want the whole sequence, got %q", out)
		}
	})

	t.Run("a session naming no model says nothing", func(t *testing.T) {
		// The word "unknown" claimed a fact the log does not carry; the effort and
		// entrypoint beside it already stay silent in this case.
		out := head(t, model.Meta{Effort: "high"})
		if strings.Contains(out, "unknown") {
			t.Errorf("no model recorded, so none should be named: %q", out)
		}
		if !strings.Contains(out, "high effort") {
			t.Errorf("the rest of the header should survive a missing model: %q", out)
		}
	})
}

// TestOutputsSection pins what a rendered session says it produced. The header
// describes what a session was; without this, a pull request the session opened
// is visible in `list` and nowhere in the render path, which is the two paths
// knowing different things about one session.
func TestOutputsSection(t *testing.T) {
	sess := &model.Session{
		Meta: model.Meta{
			ID: "s1",
			PRs: []model.PR{
				{Repository: "eitanpo/central", Number: 14, URL: "https://github.com/eitanpo/central/pull/14"},
			},
			Artifacts: []model.Artifact{
				{Title: "Cost report", URL: "https://claude.ai/code/artifact/aaa"},
				{URL: "https://claude.ai/code/artifact/bbb"},
			},
		},
		Turns: []model.Turn{{Prompt: "ship it"}},
	}

	t.Run("plain output shows every URL, since there is no href to hide one in", func(t *testing.T) {
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "── Outputs ──") {
			t.Fatalf("no Outputs section: %q", out)
		}
		for _, want := range []string{
			"https://github.com/eitanpo/central/pull/14",
			"Cost report  https://claude.ai/code/artifact/aaa",
			"https://claude.ai/code/artifact/bbb",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q: %q", want, out)
			}
		}
		if strings.Contains(out, "\x1b]8;;") {
			t.Errorf("plain output must carry no OSC 8 escape: %q", out)
		}
	})

	t.Run("the section renders at minimal verbosity", func(t *testing.T) {
		// It is deliberately ungated: thinking and tool bodies are hidden at low
		// verbosity because they are the machinery, and a link to the pull request
		// the session opened is the opposite of machinery.
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{}}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "pull/14") {
			t.Errorf("Outputs must not be gated on a channel: %q", b.String())
		}
	})

	t.Run("a session that produced nothing draws no section", func(t *testing.T) {
		var b strings.Builder
		quiet := &model.Session{Meta: model.Meta{ID: "s1"}, Turns: []model.Turn{{Prompt: "think"}}}
		if err := Session(&b, quiet, Options{Width: 100, Color: false}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "Outputs") {
			t.Errorf("empty section drawn: %q", b.String())
		}
	})

	t.Run("with color on, an artifact links by title and a PR by its URL", func(t *testing.T) {
		// The split assistant prose already makes: a markdown link hides its URL
		// behind a name, a bare URL is its own name. An artifact id is an opaque
		// uuid, so the title is the only useful name it has.
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: true}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "\x1b]8;;https://claude.ai/code/artifact/aaa\x1b\\") {
			t.Errorf("artifact is not an OSC 8 hyperlink: %q", out)
		}
		// The artifact's URL is hidden behind its title, so it appears only in the
		// href — never as visible text beside it.
		plain, _ := stripANSI(out)
		if strings.Contains(plain, "Cost report  https://claude.ai/code/artifact/aaa") {
			t.Errorf("a linked artifact must hide its URL from the visible text: %q", plain)
		}
		if !strings.Contains(plain, "https://github.com/eitanpo/central/pull/14") {
			t.Errorf("a pull request's visible text is its URL: %q", plain)
		}
	})
}

// TestHeaderCost pins that the rendered header states what the session cost, and
// that it says nothing where the log recorded no cost — the rule the model and
// effort beside it already follow.
func TestHeaderCost(t *testing.T) {
	sess := minimalSession()
	cost, added, removed := 17.254517250000003, 342, 8
	sess.Meta.Usage = model.Usage{Input: 236, Output: 111499, CacheRead: 17633669}
	sess.Meta.CostUSD = &cost
	sess.Meta.LinesAdded, sess.Meta.LinesRemoved = &added, &removed

	var b strings.Builder
	if err := Session(&b, sess, Options{Width: 100, Color: false}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Tokens: 236 in / 111k out") {
		t.Errorf("header missing the token tally: %q", out)
	}
	if !strings.Contains(out, "$17.25") {
		t.Errorf("header missing the cost: %q", out)
	}
	if !strings.Contains(out, "+342/-8") {
		t.Errorf("header missing the lines changed: %q", out)
	}

	t.Run("no cost recorded, no dollar figure", func(t *testing.T) {
		sess := minimalSession()
		sess.Meta.Usage = model.Usage{Input: 236, Output: 4}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "$") {
			t.Errorf("a session with no cost record must show no dollar figure: %q", b.String())
		}
	})
}

// ── Header: what the session was ───────────────────────────────────────────

// headerOf returns the boxed header alone, so an assertion about the header
// cannot be satisfied by text from a turn below it — "denied" appears on a tool
// line too, and "active" could appear in a prompt.
func headerOf(out string) string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "╯") { // the box's bottom-right corner
			return strings.Join(lines[:i+1], "\n")
		}
	}
	return out
}

func renderHeader(t *testing.T, s *model.Session, width int) string {
	t.Helper()
	var b strings.Builder
	if err := Session(&b, s, Options{Width: width, Color: false}); err != nil {
		t.Fatal(err)
	}
	return headerOf(b.String())
}

// TestHeaderActiveTime pins the header's first line (PRODUCT.md §Output): dated
// clock times, the active time in place of the wall-clock span, and the day
// count only on a session that spanned more than one day. The span is the
// regression this guards — it can run many times the active time, so a header
// showing it misreports the work.
func TestHeaderActiveTime(t *testing.T) {
	sameDay := &model.Session{
		Meta: model.Meta{
			Start:         time.Date(2026, 9, 18, 9, 12, 0, 0, time.Local),
			End:           time.Date(2026, 9, 18, 11, 40, 0, 0, time.Local),
			DailyActivity: []model.DailyActivity{{Day: "2026-09-18", Turns: 1, ActiveSeconds: 2520}},
		},
		Turns: []model.Turn{{Prompt: "hi"}},
	}
	across4Days := &model.Session{
		Meta: model.Meta{
			Start: time.Date(2026, 9, 15, 18, 3, 0, 0, time.Local),
			End:   time.Date(2026, 9, 18, 12, 32, 0, 0, time.Local),
			DailyActivity: []model.DailyActivity{
				{Day: "2026-09-15", Turns: 5, ActiveSeconds: 900},
				{Day: "2026-09-16", Turns: 5, ActiveSeconds: 900},
				{Day: "2026-09-17", Turns: 6, ActiveSeconds: 900},
				{Day: "2026-09-18", Turns: 14, ActiveSeconds: 1140},
			},
		},
		Turns: []model.Turn{{Prompt: "hi"}},
	}

	t.Run("a same-day session dates the start and not the end", func(t *testing.T) {
		got := renderHeader(t, sameDay, 100)
		if !strings.Contains(got, "Sep 18 09:12 → 11:40") {
			t.Errorf("times wrong: %q", got)
		}
		if !strings.Contains(got, "42m active") {
			t.Errorf("no active figure: %q", got)
		}
		if strings.Contains(got, "over") {
			t.Errorf("single-day session must name no day count: %q", got)
		}
		if strings.Contains(got, "2h28m") { // the wall-clock span
			t.Errorf("wall-clock span is not printed: %q", got)
		}
	})

	t.Run("a multi-day session dates both ends and counts the days", func(t *testing.T) {
		got := renderHeader(t, across4Days, 120)
		if !strings.Contains(got, "Sep 15 18:03 → Sep 18 12:32") {
			t.Errorf("times wrong: %q", got)
		}
		if !strings.Contains(got, "1h04m active over 4 days") {
			t.Errorf("active figure or day count wrong: %q", got)
		}
		if strings.Contains(got, "66h") { // the wall-clock span, 66h29m
			t.Errorf("wall-clock span is not printed: %q", got)
		}
	})

	t.Run("a session with no recorded activity names no active time", func(t *testing.T) {
		// Saying nothing, rather than "0m active": the log does not record how long
		// such a session worked, and a zero would assert that it worked for none.
		got := renderHeader(t, minimalSession(), 100)
		if strings.Contains(got, "active") {
			t.Errorf("active figure invented: %q", got)
		}
	})
}

// TestHeaderFailedAndDenied pins the split (PRODUCT.md §Output): a call that ran
// and failed is counted apart from one that was refused, because they ask for
// different things. The log marks a refusal as an error too, so a single count
// filed a refusal as a failure.
func TestHeaderFailedAndDenied(t *testing.T) {
	// The tallies sit on Meta because that is where the header reads them: Turns
	// holds what a caller asked to render and Meta holds the session, so a
	// narrowed transcript cannot make the header report a smaller session. The
	// parser fills both from the same entries, and TestLoadTalliesAgreeWithTurns
	// pins them against each other.
	sess := &model.Session{
		Meta: model.Meta{
			ID:       "s1",
			NumTurns: 1,
			Tools: []model.ToolStat{
				{Tool: "Bash", Count: 1}, {Tool: "Write", Identity: "hosts", Count: 1}, {Tool: "Read", Count: 1},
			},
			Failures: []model.ToolStat{{Tool: "Bash", Count: 1}},
			Denials:  []model.DenialStat{{Kind: "permission-rule", Tool: "Write", Identity: "hosts", Count: 1}},
		},
		Turns: []model.Turn{{
			Number: 1,
			Prompt: "go",
			Events: []model.Event{
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", IsError: true}},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Write", IsError: true, Denial: "permission-rule"}},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Read"}},
			},
		}},
	}

	t.Run("each is counted on its own", func(t *testing.T) {
		got := renderHeader(t, sess, 100)
		for _, want := range []string{"1 failed", "1 denied"} {
			if !strings.Contains(got, want) {
				t.Errorf("header missing %q: %q", want, got)
			}
		}
		if strings.Contains(got, "2 failed") {
			t.Errorf("a refused call was counted as a failure: %q", got)
		}
	})

	t.Run("a clean session shows neither count", func(t *testing.T) {
		got := renderHeader(t, minimalSession(), 100)
		if strings.Contains(got, "failed") || strings.Contains(got, "denied") {
			t.Errorf("zero counts must be dropped: %q", got)
		}
	})
}

// TestHeaderKeepsEveryField pins the four-line header (PRODUCT.md §Output):
// when the session ran and what it ran on sit on lines of their own, and no
// terminal width drops a field. The regression it guards is what line two
// replaced — one identity line at the 100-column fallback width had no room for
// the entrypoint and deleted it, and a reader saw nothing saying so.
func TestHeaderKeepsEveryField(t *testing.T) {
	sess := &model.Session{
		Meta: model.Meta{
			Model:      "claude-opus-5",
			Effort:     "high",
			Entrypoint: "cli",
			// Chosen so the identity line will not fit the fallback width's box.
			Start: time.Date(2026, 9, 15, 18, 3, 0, 0, time.Local),
			End:   time.Date(2026, 9, 18, 12, 32, 0, 0, time.Local),
			DailyActivity: []model.DailyActivity{
				{Day: "2026-09-15", Turns: 5, ActiveSeconds: 1200},
				{Day: "2026-09-16", Turns: 5, ActiveSeconds: 1200},
				{Day: "2026-09-17", Turns: 6, ActiveSeconds: 1200},
				{Day: "2026-09-18", Turns: 14, ActiveSeconds: 1140},
			},
		},
		Turns: []model.Turn{{Prompt: "hi"}},
	}

	t.Run("when and what it ran on are separate lines", func(t *testing.T) {
		got := renderHeader(t, sess, 100)
		var timeLine, ranOnLine string
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, "Sep 15 18:03") {
				timeLine = l
			}
			if strings.Contains(l, "claude-opus-5") {
				ranOnLine = l
			}
		}
		if timeLine == "" || ranOnLine == "" {
			t.Fatalf("header lost a line: %q", got)
		}
		if timeLine == ranOnLine {
			t.Errorf("times and model share a line: %q", timeLine)
		}
		if !strings.Contains(ranOnLine, "high effort") || !strings.Contains(ranOnLine, "cli") {
			t.Errorf("second line must carry effort and entrypoint: %q", ranOnLine)
		}
	})

	// The width a render falls back to when stdout is not a terminal, which is
	// where the old single line ran out of room.
	t.Run("the fallback width keeps the entrypoint", func(t *testing.T) {
		got := renderHeader(t, sess, 100)
		for _, want := range []string{"Sep 15 18:03", "1h19m active over 4 days", "claude-opus-5", "high effort", "cli"} {
			if !strings.Contains(got, want) {
				t.Errorf("header dropped %q: %q", want, got)
			}
		}
	})

	// Narrower than the second line needs: it wraps inside the box, which costs a
	// line and keeps every field, where dropping cost the field and said nothing.
	t.Run("a narrow terminal wraps rather than dropping", func(t *testing.T) {
		got := renderHeader(t, sess, 34)
		for _, want := range []string{"Sep 15 18:03", "1h19m", "claude-opus-5", "effort", "cli"} {
			if !strings.Contains(got, want) {
				t.Errorf("width 34 dropped %q: %q", want, got)
			}
		}
	})

	t.Run("a session whose log names none of them prints no second line", func(t *testing.T) {
		got := renderHeader(t, minimalSession(), 100)
		if strings.Contains(got, "effort") || strings.Contains(got, "unknown") {
			t.Errorf("header invented a field the log does not carry: %q", got)
		}
	})
}

// ── Footer: what the session did ───────────────────────────────────────────

// footerSession carries content for all five footer sections: files it modified,
// outputs it produced, tools by identity, turns to rank, and two days to split.
func footerSession() *model.Session {
	return &model.Session{
		Meta: model.Meta{
			ID:    "s1",
			Files: []string{"/repo/internal/render/render.go", "/repo/PRODUCT.md"},
			PRs:   []model.PR{{Repository: "eitanpo/agentry", Number: 14, URL: "https://github.com/eitanpo/agentry/pull/14"}},
			Tools: []model.ToolStat{
				{Tool: "Skill", Identity: "coding-guidelines", Count: 1},
				{Tool: "Bash", Identity: "grep", Count: 21},
			},
			Denials: []model.DenialStat{{Kind: "permission-rule", Tool: "Write", Identity: "/etc/hosts", Count: 1}},
			// NumTurns and the per-turn tool counts below say the same thing twice
			// because the parser fills them from one pass: 22 calls across two turns.
			// A fixture whose Meta tally and whose turns disagree describes no real
			// session, and the header reads Meta, so the disagreement would surface
			// there as a count no session could produce.
			NumTurns: 2,
			DailyActivity: []model.DailyActivity{
				{Day: "2026-09-17", Turns: 1, ActiveSeconds: 600},
				{Day: "2026-09-18", Turns: 1, ActiveSeconds: 900},
			},
		},
		Turns: []model.Turn{
			{Number: 1, Prompt: "first", ToolCount: 21, Usage: model.Usage{Input: 10, Output: 100}},
			{Number: 2, Prompt: "second", ToolCount: 1, Usage: model.Usage{Input: 20, Output: 200}},
		},
	}
}

// TestFooterSections pins the footer (PRODUCT.md §Output): five sections in a
// fixed order, none of them gated on verbosity, and the three aggregates leaving
// together on --no-metrics. The ungating is the regression this guards: gating
// cost real lines, and hid the sections from anyone who never typed the flag.
func TestFooterSections(t *testing.T) {
	order := []string{
		"── Files ──",
		"── Outputs ──",
		"── Tools (by identity) ──",
		"── Summary (by token cost) ──",
		"── Day by day ──",
	}

	t.Run("all five print at minimal verbosity, in order", func(t *testing.T) {
		// Channels{Metrics: true} is what the CLI resolves at every level, including
		// minimal — TestLevelChannels pins that half.
		var b strings.Builder
		if err := Session(&b, footerSession(), Options{Width: 100, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		at := -1
		for _, heading := range order {
			i := strings.Index(out, heading)
			if i < 0 {
				t.Fatalf("no %q section: %q", heading, out)
			}
			if i < at {
				t.Errorf("%q is out of order: %q", heading, out)
			}
			at = i
		}
	})

	t.Run("--no-metrics drops the three aggregates and keeps the outcomes", func(t *testing.T) {
		var b strings.Builder
		if err := Session(&b, footerSession(), Options{Width: 100, Color: false, Channels: Channels{}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		for _, want := range []string{"── Files ──", "── Outputs ──"} {
			if !strings.Contains(out, want) {
				t.Errorf("%q must survive --no-metrics: %q", want, out)
			}
		}
		for _, gone := range order[2:] {
			if strings.Contains(out, gone) {
				t.Errorf("%q must leave with --no-metrics: %q", gone, out)
			}
		}
	})

	t.Run("a session with no files draws no Files section", func(t *testing.T) {
		sess := footerSession()
		sess.Meta.Files = nil
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "── Files ──") {
			t.Errorf("empty section drawn: %q", b.String())
		}
	})

	t.Run("a single-day session draws no Day by day section", func(t *testing.T) {
		// The header's active figure already says it, and one row would repeat it.
		sess := footerSession()
		sess.Meta.DailyActivity = sess.Meta.DailyActivity[:1]
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "── Day by day ──") {
			t.Errorf("one-day session drew the section: %q", b.String())
		}
	})

	t.Run("a session with nothing to report draws no footer at all", func(t *testing.T) {
		var b strings.Builder
		bare := &model.Session{Meta: model.Meta{ID: "s1"}}
		if err := Session(&b, bare, Options{Width: 100, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		for _, heading := range order {
			if strings.Contains(b.String(), heading) {
				t.Errorf("%q drawn around nothing: %q", heading, b.String())
			}
		}
	})
}

// TestFooterCaps pins the two sections whose length the session decides: a
// session can run up files and tool identities well past what fits, so each is
// capped and says what it left out rather than being gated away.
func TestFooterCaps(t *testing.T) {
	t.Run("Files shows ten paths and names the remainder", func(t *testing.T) {
		sess := footerSession()
		sess.Meta.Files = nil
		for i := 0; i < 12; i++ {
			sess.Meta.Files = append(sess.Meta.Files, fmt.Sprintf("/repo/file%02d.go", i))
		}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "file09.go") || strings.Contains(out, "file10.go") {
			t.Errorf("cap is not ten paths: %q", out)
		}
		if !strings.Contains(out, "(2 more files)") {
			t.Errorf("remainder not named: %q", out)
		}
	})

	t.Run("a tool category shows eight entries and names the remainder", func(t *testing.T) {
		sess := footerSession()
		sess.Meta.Tools = nil
		for i := 0; i < 10; i++ {
			sess.Meta.Tools = append(sess.Meta.Tools, model.ToolStat{
				Tool: "Bash", Identity: fmt.Sprintf("cmd%02d", i), Count: 10 - i,
			})
		}
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 200, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "cmd07 ×3") || strings.Contains(out, "cmd08") {
			t.Errorf("cap is not eight entries: %q", out)
		}
		if !strings.Contains(out, "+2 more") {
			t.Errorf("remainder not named: %q", out)
		}
	})
}

// TestSessionJSONCarriesFooterFacts pins that the render path's JSON carries
// what the footer prints, uncapped. Without it the spec's claim that the two
// paths carry the same per-session fields is false, and the only way to read one
// session's files back out is the listing.
func TestSessionJSONCarriesFooterFacts(t *testing.T) {
	var b strings.Builder
	if err := SessionJSON(&b, footerSession()); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatal(err)
	}
	meta := got["meta"].(map[string]any)
	files, ok := meta["files"].([]any)
	if !ok || len(files) != 2 {
		t.Errorf("meta.files missing or short: %q", b.String())
	}
	if days, ok := meta["dailyActivity"].([]any); !ok || len(days) != 2 {
		t.Errorf("meta.dailyActivity missing or short: %q", b.String())
	}
}

// TestFooterHasNoBlankPaddedLines guards an artifact the footer made visible: a
// newline inside a styled string makes the style pad the empty line after it, so
// a section's remainder line was followed by a line of spaces. It cost nothing
// while the per-turn table was the last thing rendered and printed a whitespace
// line between sections once the footer had more below it.
func TestFooterHasNoBlankPaddedLines(t *testing.T) {
	sess := footerSession()
	sess.Meta.Files = nil
	for i := 0; i < 12; i++ { // enough files to draw a remainder line
		sess.Meta.Files = append(sess.Meta.Files, fmt.Sprintf("/repo/file%02d.go", i))
	}
	for _, color := range []bool{false, true} {
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 100, Color: color, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(b.String(), "\n") {
			if line != "" && strings.TrimSpace(line) == "" {
				t.Errorf("color=%v line %d is whitespace only: %q", color, i, line)
			}
		}
	}
}

// TestSessionCard pins the footer's closing section (PRODUCT.md §Output): the
// render names the session it just showed. Until this section existed it named
// it nowhere, so a reader at the bottom of a long render had to go back to a
// listing to find the id of what they were reading.
func TestSessionCard(t *testing.T) {
	sess := footerSession()
	sess.Meta.ID = "7a8a84e4-7e68-4e4a-9549-4047e0c7a48d"
	sess.Meta.Title = "the footer phase"
	sess.Meta.Cwd = "/Users/dev/Projects/me/agentry"
	sess.Meta.RootUUID = "84a562af-5ae3-4f49-93bd-1c4b28b377c1"
	sess.Meta.Path = "/Users/dev/.claude/projects/-Users-dev-Projects-me-agentry/7a8a84e4.jsonl"
	sess.Meta.NumSubagents = 2
	sess.Meta.Usage = model.Usage{Input: 30, Output: 300} // the header's own totals, which the card restates
	sess.Meta.Model = "claude-opus-5"
	sess.Meta.Effort = "high"
	sess.Meta.Entrypoint = "cli"
	sess.Meta.Start = time.Date(2026, 9, 17, 9, 12, 0, 0, time.Local)
	sess.Meta.End = time.Date(2026, 9, 18, 11, 40, 0, 0, time.Local)

	render := func(t *testing.T, ch Channels) string {
		t.Helper()
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: ch}); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	t.Run("it names the session every way the log does", func(t *testing.T) {
		out := render(t, Channels{Metrics: true})
		for _, want := range []string{
			"── Session ──",
			"id       7a8a84e4-7e68-4e4a-9549-4047e0c7a48d",
			"title    the footer phase",
			"project  /Users/dev/Projects/me/agentry",
			"root     84a562af-5ae3-4f49-93bd-1c4b28b377c1",
			"log      /Users/dev/.claude/projects/-Users-dev-Projects-me-agentry/7a8a84e4.jsonl",
			"when     Sep 17 09:12 → Sep 18 11:40 · 25m active over 2 days",
			"ran on   claude-opus-5 · high effort · cli",
			"render   agentry 7a8a84e4-7e68-4e4a-9549-4047e0c7a48d",
			"resume   claude --resume 7a8a84e4-7e68-4e4a-9549-4047e0c7a48d",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("card missing %q: %q", want, out)
			}
		}
	})

	t.Run("it restates the header's size and spend", func(t *testing.T) {
		// The restatement is the point: on a long session the header has scrolled
		// away by the time a reader decides what to do with what they read. Counted
		// twice in the output, so a card that dropped either line fails here.
		out := render(t, Channels{Metrics: true})
		for _, line := range []string{"2 turns · 22 tools · 2 subagents · 1 denied", "Tokens: 30 in / 300 out"} {
			if n := strings.Count(out, line); n != 2 {
				t.Errorf("%q appears %d times, want 2 (header and card): %q", line, n, out)
			}
		}
	})

	t.Run("it is the last section, and --no-metrics keeps it", func(t *testing.T) {
		// An id is how a reader acts on the render, not detail about the work, so
		// the flag that drops the three tables must not drop the card with them.
		out := render(t, Channels{})
		card := strings.Index(out, "── Session ──")
		if card < 0 {
			t.Fatalf("--no-metrics dropped the card: %q", out)
		}
		for _, earlier := range []string{"── Files ──", "── Outputs ──"} {
			if i := strings.Index(out, earlier); i < 0 || i > card {
				t.Errorf("%q must come before the card: %q", earlier, out)
			}
		}
		if rest := out[card:]; strings.Count(rest, "── ") != 1 {
			t.Errorf("the card must be the last section: %q", rest)
		}
	})

	t.Run("a session the log named nothing shows no naming rows", func(t *testing.T) {
		bare := footerSession()
		bare.Meta.ID = "s1"
		var b strings.Builder
		if err := Session(&b, bare, Options{Width: 120, Color: false, Channels: Channels{}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "id       s1") {
			t.Fatalf("card missing its id: %q", out)
		}
		for _, absent := range []string{"title  ", "project", "root   ", "log    "} {
			if strings.Contains(out, absent) {
				t.Errorf("empty row %q drawn: %q", absent, out)
			}
		}
	})
}

// TestSessionJSONCarriesNamingHandles pins that the naming rows are readable
// without parsing the text view, which is what an agent piping --format json
// needs to refer to the session it just read.
func TestSessionJSONCarriesNamingHandles(t *testing.T) {
	sess := footerSession()
	sess.Meta.Title = "the footer phase"
	sess.Meta.Cwd = "/repo"
	sess.Meta.RootUUID = "84a562af"
	sess.Meta.Path = "/logs/s1.jsonl"
	var b strings.Builder
	if err := SessionJSON(&b, sess); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatal(err)
	}
	meta := got["meta"].(map[string]any)
	for key, want := range map[string]string{"title": "the footer phase", "cwd": "/repo", "rootUuid": "84a562af", "path": "/logs/s1.jsonl"} {
		if meta[key] != want {
			t.Errorf("meta.%s = %v, want %q", key, meta[key], want)
		}
	}
}

// TestTurnRuleSplitsFailedAndDenied pins the rule beneath each turn
// (PRODUCT.md §Output): it counts a refused call apart from a failed one, the
// same split the header and the card make, so the turns' figures add up to the
// header's. A rule reading "2 errors" sent a reader looking for two things to
// fix where one was a permission boundary holding.
func TestTurnRuleSplitsFailedAndDenied(t *testing.T) {
	sess := &model.Session{
		Meta: model.Meta{ID: "s1"},
		Turns: []model.Turn{{
			Prompt:    "go",
			ToolCount: 3,
			Events: []model.Event{
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", IsError: true}},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Write", IsError: true, Denial: "permission-rule"}},
				{Kind: model.EventTool, Tool: &model.Tool{Name: "Read"}},
			},
		}},
	}
	var b strings.Builder
	if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{}}); err != nil {
		t.Fatal(err)
	}
	rule := ""
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Contains(line, "╰─ ") && strings.Contains(line, "tool") {
			rule = line
		}
	}
	if rule == "" {
		t.Fatalf("no per-turn rule in %q", b.String())
	}
	for _, want := range []string{"3 tools", "1 failed", "1 denied"} {
		if !strings.Contains(rule, want) {
			t.Errorf("rule %q missing %q", rule, want)
		}
	}
	if strings.Contains(rule, "error") {
		t.Errorf("rule still reports errors as one count: %q", rule)
	}
}

// TestCostSection pins the footer's money section (PRODUCT.md §Output): what
// Claude Code recorded, what agentry prices the same tokens at, and which model,
// which delegation and what unit of work that price went to. The recorded figure
// is often missing, so a footer carrying only it would answer "what did this
// cost" for a minority of renders.
//
// Dollar amounts are not asserted: the rate table is versioned and a rate change
// must not fail this test. What is asserted is which rows appear and how each
// figure is marked.
func TestCostSection(t *testing.T) {
	priced := func() *model.Session {
		s := footerSession()
		recorded := 12.50
		s.Meta.CostUSD = &recorded
		s.Meta.DailyActivity = []model.DailyActivity{
			{Day: "2026-09-17", Turns: 1, ActiveSeconds: 600},
			{Day: "2026-09-18", Turns: 1, ActiveSeconds: 900},
		}
		s.Meta.DailyUsage = []model.DailyUsage{
			{Day: "2026-09-17", Model: "claude-opus-5", Usage: model.Usage{Input: 20000, Output: 40000}},
			{Day: "2026-09-18", Model: "claude-opus-5", Agent: "general-purpose", Usage: model.Usage{Input: 5000, Output: 9000}},
		}
		return s
	}

	t.Run("it separates the record from the price agentry computed", func(t *testing.T) {
		var b strings.Builder
		if err := Session(&b, priced(), Options{Width: 120, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, "── Cost ──") {
			t.Fatalf("no Cost section: %q", out)
		}
		if !strings.Contains(out, "recorded $12.50") {
			t.Errorf("the record must print unmarked, as Claude Code's own figure: %q", out)
		}
		for _, want := range []string{"priced   ~$", "by model claude-opus-5 ~$", "by agent main ~$", "general-purpose ~$", "/turn", "/active hour"} {
			if !strings.Contains(out, want) {
				t.Errorf("Cost section missing %q: %q", want, out)
			}
		}
	})

	t.Run("a day carries what that day cost", func(t *testing.T) {
		var b strings.Builder
		if err := Session(&b, priced(), Options{Width: 120, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		day := ""
		for _, line := range strings.Split(b.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "2026-09-17") {
				day = line
			}
		}
		if !strings.Contains(day, "~$") {
			t.Errorf("day row carries no price: %q", day)
		}
	})

	t.Run("a model with no rate is named, not counted as free", func(t *testing.T) {
		sess := priced()
		sess.Meta.DailyUsage = append(sess.Meta.DailyUsage, model.DailyUsage{
			Day: "2026-09-18", Model: "claude-not-in-the-table-9", Usage: model.Usage{Input: 1000, Output: 2000},
		})
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "unpriced claude-not-in-the-table-9") {
			t.Errorf("unpriced model not named: %q", b.String())
		}
	})

	t.Run("a session with neither a record nor priceable tokens draws no section", func(t *testing.T) {
		sess := footerSession()
		sess.Meta.CostUSD = nil
		sess.Meta.DailyUsage = nil
		var b strings.Builder
		if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: Channels{Metrics: true}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "── Cost ──") {
			t.Errorf("empty section drawn: %q", b.String())
		}
	})

	t.Run("--no-metrics takes it with the other aggregates", func(t *testing.T) {
		var b strings.Builder
		if err := Session(&b, priced(), Options{Width: 120, Color: false, Channels: Channels{}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "── Cost ──") {
			t.Errorf("Cost survived --no-metrics: %q", b.String())
		}
	})
}

// TestFailedLine pins that the footer names which tools failed (PRODUCT.md
// §Output): the header counts failures, and until this line existed nothing said
// where to look. It also pins that the line and the header count the same calls
// — they are read from different places, the line from the parser's tally and
// the count from the event stream, so nothing but a test keeps them equal.
func TestFailedLine(t *testing.T) {
	sess := footerSession()
	sess.Meta.Failures = []model.ToolStat{
		{Tool: "Bash", Identity: "go", Count: 2},
		{Tool: "Edit", Identity: "/repo/internal/cli/cli.go", Count: 1},
	}
	sess.Turns = []model.Turn{{
		Prompt:    "run the checks",
		ToolCount: 4,
		Events: []model.Event{
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", Identity: "go", IsError: true}},
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", Identity: "go", IsError: true}},
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Edit", Identity: "/repo/internal/cli/cli.go", IsError: true}},
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Write", Identity: "/etc/hosts", IsError: true, Denial: "permission-rule"}},
		},
	}}
	var b strings.Builder
	if err := Session(&b, sess, Options{Width: 120, Color: false, Channels: Channels{Metrics: true}}); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "Failed  Bash/go ×2, Edit/cli.go ×1") {
		t.Errorf("Failed line missing or misshaped: %q", out)
	}
	// The refused call belongs to the Denied line, not this one.
	if strings.Contains(out, "Failed") && strings.Contains(out, "Failed  Write") {
		t.Errorf("a refused call was named as a failure: %q", out)
	}
	if !strings.Contains(headerOf(out), "3 failed") || !strings.Contains(headerOf(out), "1 denied") {
		t.Errorf("header counts wrong: %q", headerOf(out))
	}

	// The line's own counts add up to the number the header reports.
	failedLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Failed  ") {
			failedLine = line
		}
	}
	total := 0
	for _, part := range strings.Split(failedLine, "×")[1:] {
		n := 0
		if _, err := fmt.Sscanf(part, "%d", &n); err != nil {
			t.Fatalf("unreadable count in %q: %v", failedLine, err)
		}
		total += n
	}
	if total != 3 {
		t.Errorf("Failed line sums to %d, header says 3: %q", total, failedLine)
	}
}

// jsonlSession is the fixture the JSON Lines tests share: two turns, a tool call
// that spawned a subagent which itself delegated, so depth reaches 2 and the
// flattening has a real tree to reproduce.
func jsonlSession() *model.Session {
	return &model.Session{
		Meta: model.Meta{ID: "s1", Model: "claude-opus-4-8", NumTurns: 2, Usage: model.Usage{Input: 10, Output: 20}},
		Turns: []model.Turn{{
			// Numbered like the parser numbers them. A hand-built turn that leaves
			// this zero is not a shortcut: the stream reports a turn's number from
			// the turn, since its index in a selected slice is not its place in the
			// session.
			Number:    1,
			Prompt:    "first",
			ToolCount: 1,
			Events: []model.Event{
				{Kind: model.EventText, Text: "sure"},
				{Kind: model.EventTool, Tool: &model.Tool{
					Name: "Agent", Args: "dig",
					Subagent: []model.Event{
						{Kind: model.EventText, Text: "child"},
						{Kind: model.EventTool, Tool: &model.Tool{
							Name:     "Agent",
							Subagent: []model.Event{{Kind: model.EventText, Text: "grandchild"}},
						}},
					},
				}},
			},
		}, {
			Number: 2,
			Prompt: "second",
			Events: []model.Event{{Kind: model.EventText, Text: "done"}},
		}},
	}
}

// decodeJSONL parses the stream into records, failing on the first line that is
// not a standalone JSON value — which is the property the format promises.
func decodeJSONL(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not a standalone JSON value: %v\n%s", i+1, err, line)
		}
		out = append(out, rec)
	}
	return out
}

// TestSessionJSONLEnvelope pins the envelope every record carries: the five keys
// a consumer reads without knowing the producer, a stream-wide ordinal starting
// at 1, and the schema written once. A missing tool or sessionId is what makes a
// concatenated stream unreadable, which is the case the format exists for.
func TestSessionJSONLEnvelope(t *testing.T) {
	var b strings.Builder
	if err := SessionJSONL(&b, jsonlSession()); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(b.String(), "[") {
		t.Error("stream is wrapped in an array")
	}
	recs := decodeJSONL(t, b.String())
	for i, r := range recs {
		for _, k := range []string{"type", "tool", "sessionId", "ordinal", "payload"} {
			if _, ok := r[k]; !ok {
				t.Errorf("record %d missing %q: %v", i+1, k, r)
			}
		}
		if r["tool"] != "claude-code" {
			t.Errorf("record %d tool = %v, want claude-code", i+1, r["tool"])
		}
		if r["sessionId"] != "s1" {
			t.Errorf("record %d sessionId = %v, want s1", i+1, r["sessionId"])
		}
		if got := r["ordinal"].(float64); int(got) != i+1 {
			t.Errorf("record %d ordinal = %v, want %d", i+1, got, i+1)
		}
		if _, ok := r["schema"]; ok != (i == 0) {
			t.Errorf("record %d schema present = %v, want %v", i+1, ok, i == 0)
		}
	}
}

// TestSessionJSONLTurnsCarryPromptsWithoutEvents pins the split that decides the
// format's worst line: the prompt lives on the turn record, and the events do
// not. Inlining them would put a delegated session inside one line.
func TestSessionJSONLTurnsCarryPromptsWithoutEvents(t *testing.T) {
	var b strings.Builder
	if err := SessionJSONL(&b, jsonlSession()); err != nil {
		t.Fatal(err)
	}
	var prompts []string
	for _, r := range decodeJSONL(t, b.String()) {
		if r["type"] != "turn" {
			continue
		}
		p := r["payload"].(map[string]any)
		if _, ok := p["events"]; ok {
			t.Error("turn record carries its events inline")
		}
		prompts = append(prompts, p["prompt"].(string))
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(prompts, want) {
		t.Errorf("prompts = %v, want %v", prompts, want)
	}
}

// TestSessionJSONLDepthReproducesTheTree walks the stream back into a tree and
// compares it with the model the parser built. Depth plus document order is the
// only thing carrying the nesting, so this is what proves nothing was dropped
// and no subagent was reparented.
func TestSessionJSONLDepthReproducesTheTree(t *testing.T) {
	sess := jsonlSession()
	var b strings.Builder
	if err := SessionJSONL(&b, sess); err != nil {
		t.Fatal(err)
	}
	var flat []map[string]any
	for _, r := range decodeJSONL(t, b.String()) {
		if r["type"] == "event" {
			flat = append(flat, r["payload"].(map[string]any))
		}
	}
	var texts []string
	var depths []int
	var turns []int
	var indexes []int
	for _, e := range flat {
		depths = append(depths, int(e["depth"].(float64)))
		turns = append(turns, int(e["turn"].(float64)))
		indexes = append(indexes, int(e["index"].(float64)))
		if s, ok := e["text"].(string); ok {
			texts = append(texts, s)
		} else {
			texts = append(texts, "@"+e["tool"].(map[string]any)["name"].(string))
		}
	}
	wantTexts := []string{"sure", "@Agent", "child", "@Agent", "grandchild", "done"}
	wantDepth := []int{0, 0, 1, 1, 2, 0}
	wantTurn := []int{1, 1, 1, 1, 1, 2}
	wantIndex := []int{1, 2, 1, 2, 1, 1}
	if !reflect.DeepEqual(texts, wantTexts) {
		t.Errorf("order = %v, want %v", texts, wantTexts)
	}
	if !reflect.DeepEqual(depths, wantDepth) {
		t.Errorf("depths = %v, want %v", depths, wantDepth)
	}
	if !reflect.DeepEqual(turns, wantTurn) {
		t.Errorf("turns = %v, want %v — a subagent's events keep their parent turn", turns, wantTurn)
	}
	// index orders a sibling list and restarts inside each subagent stream, which
	// is why two records here carry index 1 at three different depths. A reader
	// who takes it for a per-turn identifier gets collisions, so the spec says
	// what it counts and this pins it.
	if !reflect.DeepEqual(indexes, wantIndex) {
		t.Errorf("indexes = %v, want %v", indexes, wantIndex)
	}
	// The nested stream must not also remain inside the tool it hung from, or the
	// format has both shapes at once and the worst line is unchanged.
	for _, e := range flat {
		if tool, ok := e["tool"].(map[string]any); ok {
			if _, nested := tool["subagent"]; nested {
				t.Error("event record still nests its subagent stream")
			}
		}
	}
}

// TestSessionJSONLEmptySession pins that a session with no turns emits its meta
// record and nothing else — the shape that removes the "turns": null the
// document form emits, which crashed two consumers.
func TestSessionJSONLEmptySession(t *testing.T) {
	var b strings.Builder
	if err := SessionJSONL(&b, &model.Session{Meta: model.Meta{ID: "s1"}}); err != nil {
		t.Fatal(err)
	}
	recs := decodeJSONL(t, b.String())
	if len(recs) != 1 || recs[0]["type"] != "meta" {
		t.Fatalf("want one meta record, got %d: %v", len(recs), recs)
	}
	if strings.Contains(b.String(), "null") {
		t.Errorf("stream carries a null: %s", b.String())
	}
}

// TestSessionJSONLCarriesWhatJSONCarries pins that the two machine formats
// describe the same session. The line form exists to be cheaper to read, not to
// say less, and the phase that added it must not perturb the document form
// either — so this compares the facts both ways round rather than freezing one
// format's bytes, which would fail on every deliberate change instead of only
// on a divergence.
func TestSessionJSONLCarriesWhatJSONCarries(t *testing.T) {
	sess := jsonlSession()

	var doc strings.Builder
	if err := SessionJSON(&doc, sess); err != nil {
		t.Fatal(err)
	}
	var asDoc struct {
		Meta  map[string]any `json:"meta"`
		Turns []struct {
			Prompt string `json:"prompt"`
		} `json:"turns"`
	}
	if err := json.Unmarshal([]byte(doc.String()), &asDoc); err != nil {
		t.Fatal(err)
	}

	var stream strings.Builder
	if err := SessionJSONL(&stream, sess); err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	var prompts []string
	events := 0
	for _, r := range decodeJSONL(t, stream.String()) {
		switch r["type"] {
		case "meta":
			meta = r["payload"].(map[string]any)
		case "turn":
			prompts = append(prompts, r["payload"].(map[string]any)["prompt"].(string))
		case "event":
			events++
		}
	}

	if !reflect.DeepEqual(meta, asDoc.Meta) {
		t.Errorf("meta differs between the formats:\njsonl %v\njson  %v", meta, asDoc.Meta)
	}
	var wantPrompts []string
	for _, turn := range asDoc.Turns {
		wantPrompts = append(wantPrompts, turn.Prompt)
	}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Errorf("prompts = %v, want %v", prompts, wantPrompts)
	}
	// Counted over the tree, since the document nests what the stream flattens.
	var count func([]model.Event) int
	count = func(evs []model.Event) int {
		n := 0
		for _, e := range evs {
			n++
			if e.Tool != nil {
				n += count(e.Tool.Subagent)
			}
		}
		return n
	}
	want := 0
	for _, turn := range sess.Turns {
		want += count(turn.Events)
	}
	if events != want {
		t.Errorf("stream carries %d events, the model has %d", events, want)
	}
}

// TestSessionJSONEmptyTurnsIsAnArray pins the render path to the normalization
// the listing and both roll-up shapes already do: an empty top-level collection
// marshals as [] rather than null. A session with no turns is a real outcome,
// and null gave the document's primary collection a second shape that every
// consumer had to branch on — and a real consumer iterating turns crashed on
// the difference.
func TestSessionJSONEmptyTurnsIsAnArray(t *testing.T) {
	sess := &model.Session{Meta: model.Meta{ID: "s1"}}
	var b strings.Builder
	if err := SessionJSON(&b, sess); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `"turns": null`) {
		t.Errorf("empty turns marshalled as null:\n%s", b.String())
	}
	var got struct {
		Turns []model.Turn `json:"turns"`
	}
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Turns == nil {
		t.Error("turns key is absent or null; a consumer iterating it cannot do so unguarded")
	}
	if len(got.Turns) != 0 {
		t.Errorf("turns = %v, want empty", got.Turns)
	}
	// Normalizing must not reach back into the caller's session: the other
	// emitters take their payload by value, this one takes a pointer.
	if sess.Turns != nil {
		t.Error("serializing mutated the caller's session")
	}
}

// TestTurnRuleCarriesSpend pins the three spend fields the rule closes with and
// the case each is dropped in. Without them the only per-turn spend on the page
// is the summary table's token column, which has neither cache share nor dollars
// and sits far below the turn it describes.
func TestTurnRuleCarriesSpend(t *testing.T) {
	usd := 0.5
	cases := []struct {
		name  string
		turn  model.Turn
		want  []string
		avoid []string
	}{
		{
			name: "tokens, cache share and dollars",
			turn: model.Turn{
				Prompt:  "go",
				Usage:   model.Usage{Input: 6, Output: 5200, CacheRead: 294, CacheCreate: 0},
				CostUSD: &usd,
			},
			want: []string{"6 in / 5.2k out", "cache 98%", "~$0.50"},
		},
		{
			name: "no cache share where nothing was cached",
			turn: model.Turn{Prompt: "go", Usage: model.Usage{Input: 10, Output: 20}, CostUSD: &usd},
			want: []string{"10 in / 20 out", "~$0.50"},
			// Input alone is the denominator, so a share would read 0% and claim a
			// measurement where the log supports none.
			avoid: []string{"cache "},
		},
		{
			name:  "no dollars for a turn agentry cannot price",
			turn:  model.Turn{Prompt: "go", Usage: model.Usage{Input: 10, Output: 20}},
			want:  []string{"10 in / 20 out"},
			avoid: []string{"$"},
		},
		{
			name:  "nothing at all for a turn that made no request",
			turn:  model.Turn{Prompt: "go"},
			avoid: []string{" in / ", "cache ", "$"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			sess := &model.Session{Meta: model.Meta{ID: "s1"}, Turns: []model.Turn{c.turn}}
			if err := Session(&b, sess, Options{Width: 100, Color: false, Channels: Channels{}}); err != nil {
				t.Fatal(err)
			}
			rule := ""
			for _, line := range strings.Split(b.String(), "\n") {
				if strings.Contains(line, "╰─ ") {
					rule = line
				}
			}
			if rule == "" {
				t.Fatalf("no per-turn rule in %q", b.String())
			}
			for _, w := range c.want {
				if !strings.Contains(rule, w) {
					t.Errorf("rule %q missing %q", rule, w)
				}
			}
			for _, a := range c.avoid {
				if strings.Contains(rule, a) {
					t.Errorf("rule %q should not carry %q", rule, a)
				}
			}
		})
	}
}

// TestSelectedTurnPrintsBodiesWhole pins the second half of the search-then-read
// flow: a hit the search reports by its line number inside a result body has to
// be visible in the render of the turn it names. The cap that keeps a
// whole-session render scannable would otherwise hide any hit past its tenth
// line, leaving the caller told the text exists with no way to reach it.
func TestSelectedTurnPrintsBodiesWhole(t *testing.T) {
	lines := make([]string, 0, toolBodyMaxLines*3)
	for i := 0; i < toolBodyMaxLines*3; i++ {
		lines = append(lines, fmt.Sprintf("row%02d", i))
	}
	body := strings.Join(lines, "\n")
	deep := lines[len(lines)-1]

	sess := &model.Session{
		Meta: model.Meta{NumTurns: 2},
		Turns: []model.Turn{
			{Number: 1, Prompt: "one", Events: []model.Event{{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: body}}}},
			{Number: 2, Prompt: "two", Events: []model.Event{{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: body}}}},
		},
	}
	render := func(t *testing.T, selected *model.TurnRange) string {
		t.Helper()
		var b strings.Builder
		opts := Options{Width: 120, Color: false, Channels: Channels{Tools: true, ToolResults: true}, Selected: selected}
		shown := sess
		if selected != nil {
			shown = model.Select(sess, *selected)
		}
		if err := Session(&b, shown, opts); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	whole := render(t, &model.TurnRange{From: 2, To: 2})
	if !strings.Contains(whole, deep) {
		t.Errorf("a selected turn hid the body's last line (%q): %q", deep, whole)
	}
	if strings.Contains(whole, "more lines") {
		t.Errorf("a selected turn still named a remainder: %q", whole)
	}

	// The cap is what makes a whole-session render scannable, so selecting one
	// turn must not be the same as turning it off everywhere.
	capped := render(t, nil)
	if strings.Contains(capped, deep) {
		t.Errorf("a whole-session render printed a capped body whole: %q", capped)
	}
	if !strings.Contains(capped, fmt.Sprintf("… %d more lines", toolBodyMaxLines*2)) {
		t.Errorf("a whole-session render stopped naming its remainder: %q", capped)
	}
}

// TestSelectedTurnPrintsArgumentsWhole pins the other half of the
// search-then-read pair. The activation line's parenthetical is a summary — the
// first line, cut to a width — so a hit the search reported at `args:65` named a
// line no render would show, and four of five hits in a real turn were
// unreachable for exactly this reason.
func TestSelectedTurnPrintsArgumentsWhole(t *testing.T) {
	lines := []string{"python3 - <<'PY'", strings.Repeat("import io; ", 8), "deep = 'the last line'", "PY"}
	args := strings.Join(lines, "\n")
	deep := lines[2]

	sess := &model.Session{
		Meta: model.Meta{NumTurns: 1},
		Turns: []model.Turn{{
			Number: 1, Prompt: "go",
			Events: []model.Event{{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", Args: args, Result: "done"}}},
		}},
	}
	render := func(t *testing.T, selected *model.TurnRange) string {
		t.Helper()
		var b strings.Builder
		opts := Options{Width: 120, Color: false, Channels: Channels{Tools: true, ToolResults: true}, Selected: selected}
		if err := Session(&b, sess, opts); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	whole := render(t, &model.TurnRange{From: 1, To: 1})
	if !strings.Contains(whole, deep) {
		t.Errorf("a selected turn hid a line of the call's arguments (%q):\n%s", deep, whole)
	}
	// Labelled with the word `agentry search` prints for that part, so a hit's
	// location and the block holding it read alike.
	if !strings.Contains(whole, "args\n") {
		t.Errorf("the argument block carries no label:\n%s", whole)
	}

	// A whole-session render is untouched: the pressure the summary answers is a
	// session's worth of calls, and this block is the narrowed case only.
	capped := render(t, nil)
	if strings.Contains(capped, deep) {
		t.Errorf("a whole-session render printed argument text whole:\n%s", capped)
	}
	if strings.Contains(capped, "args\n") {
		t.Errorf("a whole-session render grew an argument block:\n%s", capped)
	}
}

// TestArgsSummaryNamesWhatItCut pins that the parenthetical says when it left
// something out. It joins the argument's lines and cuts the result to a width, so
// the width is the one thing it can lose and the one thing it has to name;
// leaving that unmarked is how a script passed to a shell came to read as a
// one-line call.
func TestArgsSummaryNamesWhatItCut(t *testing.T) {
	long := strings.Repeat("x", toolArgsInlineMax+10)
	for _, tc := range []struct {
		what   string
		args   string
		elided bool
	}{
		{"a short single line leaves nothing out", "ls -la", false},
		// The lines are joined rather than ended at the first, so a short
		// multi-line argument is shown entire and there is nothing to name. Ending
		// at the first line deleted the rest with no mark, and claimed an elision
		// on arguments it had in fact shown whole.
		{"a short multi-line argument fits once joined", "short\nand more", false},
		{"a line past the width is named", long, true},
		{"many lines past the width are named once", long + "\nmore", true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got := argsSummary(tc.args)
			if ends := strings.HasSuffix(got, "…"); ends != tc.elided {
				t.Errorf("argsSummary(%q) = %q; names an elision = %v, want %v", tc.args, got, ends, tc.elided)
			}
			if n := strings.Count(got, "…"); n > 1 {
				t.Errorf("argsSummary(%q) = %q, naming the elision %d times", tc.args, got, n)
			}
			if argsElided(tc.args) != tc.elided {
				t.Errorf("argsElided(%q) = %v, want %v", tc.args, argsElided(tc.args), tc.elided)
			}
		})
	}

	// Nothing left out means no block on a selected turn either, or the block
	// would repeat the line above it.
	sess := &model.Session{
		Meta: model.Meta{NumTurns: 1},
		Turns: []model.Turn{{
			Number: 1, Prompt: "go",
			Events: []model.Event{{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", Args: "ls -la", Result: "done"}}},
		}},
	}
	var b strings.Builder
	opts := Options{Width: 120, Color: false, Channels: Channels{Tools: true, ToolResults: true}, Selected: &model.TurnRange{From: 1, To: 1}}
	if err := Session(&b, sess, opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "args\n") {
		t.Errorf("a call whose parenthetical is complete grew a block repeating it:\n%s", b.String())
	}
}

// TestNumbersABodyOfManyLines pins the gutter a verbatim body carries and what
// it is for: `agentry search` reports a hit as `result:66`, and the number
// located nothing while the body it named printed unnumbered — the reader was
// told which line held the passage and left to count sixty-six lines by eye.
//
// Three properties, each of which has to hold for the number to mean anything: a
// numbered line carries its own source number, a line that wrapped carries
// blanks rather than repeating it, and a one-line body carries no number at all.
func TestNumbersABodyOfManyLines(t *testing.T) {
	long := strings.Repeat("word ", 60) // one source line, several display lines
	body := strings.Join([]string{"first", "second", long, "fourth"}, "\n")
	sess := &model.Session{
		Meta: model.Meta{NumTurns: 1},
		Turns: []model.Turn{{Number: 1, Prompt: "one", Events: []model.Event{
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Read", Result: body}},
			{Kind: model.EventTool, Tool: &model.Tool{Name: "Bash", Result: "just the one line"}},
		}}},
	}
	var b strings.Builder
	opts := Options{Width: 60, Color: false, Channels: Channels{Tools: true, ToolResults: true},
		Selected: &model.TurnRange{From: 1, To: 1}}
	if err := Session(&b, sess, opts); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// The number a hit would cite, against the line it names.
	for _, want := range []string{"1 first", "2 second", "4 fourth"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q numbered in:\n%s", want, out)
		}
	}

	// The third source line wraps. Only its first display line carries the
	// number, or the body would report more lines than it holds.
	if n := strings.Count(out, "3 word"); n != 1 {
		t.Errorf("the number appears on %d display lines of one source line, want 1:\n%s", n, out)
	}

	// The one-line body beside it carries none: its only line is the one a hit
	// could have named, and a lone "1" is chrome saying nothing.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "just the one line") && strings.Contains(line, "1 just") {
			t.Errorf("a one-line body was numbered: %q", line)
		}
	}
}
