package theme

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestNoColorIsNamedOutsideTheTheme is the only thing that keeps this package
// the palette. Four packages each held their own codes before it, and one row's
// time, count, label, id and title were every one of them drawn differently from
// the same fields in the listing beside it — with nothing looking wrong on either
// surface read alone, which is why no test and no reader caught it.
//
// Every way lipgloss takes a color counts, not only the one a package happened
// to use. The roll-up named its shades through AdaptiveColor while everything
// else used Color, so a guard written against one constructor would have passed
// a file holding five colors of its own.
//
// Test files are exempt: a test asserting an exact escape sequence is checking
// what shipped, which is the one place a literal code says something.
func TestNoColorIsNamedOutsideTheTheme(t *testing.T) {
	root := filepath.Join("..", "..")
	var named []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) == filepath.Join(root, "internal", "theme") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, how := range []string{
				"lipgloss.Color(",
				"lipgloss.AdaptiveColor{",
				"lipgloss.ANSIColor(",
				"lipgloss.CompleteColor{",
				"lipgloss.CompleteAdaptiveColor{",
			} {
				if strings.Contains(line, how) {
					named = append(named, filepath.ToSlash(path)+":"+strconv.Itoa(i+1))
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) > 0 {
		t.Errorf("a color is named outside the theme, which is how the palette drifts:\n  %s",
			strings.Join(named, "\n  "))
	}
}

// TestEveryChosenShadeIsAPair pins that a shade agentry picked for itself reads
// against both backgrounds. A gray chosen to sit on a dark terminal is close to
// the page on a light one, and a fixed near-white body turned a rendered
// session's tool output into text a light-theme reader could not see. A role
// whose two backgrounds render alike is a role that has stopped adapting.
//
// The dark half of each pair is also pinned to what agentry printed before the
// pairs existed, because the whole safety of the change is that a dark terminal's
// output did not move.
func TestEveryChosenShadeIsAPair(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	for _, role := range []struct {
		what string
		of   func() lipgloss.Style
		dark string // the code this role printed before it was paired
	}{
		{"a row's own facts", Meta, "38;5;250"},
		// Written 97 rather than 38;5;15: the code is one of the basic sixteen, so
		// the shorter escape is what actually went to the terminal.
		{"a quoted body", Body, "97"},
		{"a call's arguments", Args, "38;5;248"},
		{"reasoning", Thinking, "38;5;243"},
		{"a hyperlink", Link, "38;5;80"},
	} {
		t.Run(role.what, func(t *testing.T) {
			lipgloss.SetHasDarkBackground(true)
			onDark := role.of().Render("x")
			lipgloss.SetHasDarkBackground(false)
			onLight := role.of().Render("x")

			if onDark == onLight {
				t.Errorf("renders the same on both backgrounds (%q), so it adapts to neither", onDark)
			}
			if !strings.Contains(onDark, role.dark) {
				t.Errorf("on a dark background: %q, want the %s it printed before pairing", onDark, role.dark)
			}
		})
	}

	// The prompt row's highlight is a background rather than a foreground, and it
	// is the same defect upside down: a dark band drawn across a light page.
	lipgloss.SetHasDarkBackground(true)
	onDark := PromptRow().Render("x")
	lipgloss.SetHasDarkBackground(false)
	if onLight := PromptRow().Render("x"); onDark == onLight {
		t.Errorf("the prompt row's highlight is the same band on both backgrounds: %q", onDark)
	}
	if !strings.Contains(onDark, "48;5;237") {
		t.Errorf("on a dark background the highlight is %q, want the 48;5;237 it printed before pairing", onDark)
	}
}
