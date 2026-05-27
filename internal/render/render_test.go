package render

import (
	"strings"
	"testing"
	"time"

	"github.com/eitanpo/ase/internal/model"
)

func minimalSession() *model.Session {
	return &model.Session{
		Meta: model.Meta{Model: "claude-opus-4-7"},
		Turns: []model.Turn{{
			Prompt: "hi there",
			Events: []model.Event{{Kind: model.EventText, Text: "hello back"}},
		}},
	}
}

func TestFmtTok(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"}, {999, "999"}, {1000, "1.0k"}, {1500, "1.5k"}, {9999, "10.0k"}, {15000, "15k"},
	}
	for _, tt := range tests {
		if got := fmtTok(tt.n); got != tt.want {
			t.Errorf("fmtTok(%d) = %q, want %q", tt.n, got, tt.want)
		}
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

func TestTruncateAndOneLine(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q, want abc…", got)
	}
	if got := truncate("ab", 3); got != "ab" {
		t.Errorf("truncate short = %q, want ab", got)
	}
	if got := oneLine("  first\nsecond  "); got != "first" {
		t.Errorf("oneLine = %q, want first", got)
	}
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
