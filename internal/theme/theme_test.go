package theme

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
