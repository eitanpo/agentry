package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("an absolute XDG_CONFIG_HOME is honored", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got, err := Path()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(xdg, "agentry", "config.json"); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
	})

	// A relative config home would resolve against whatever directory agentry was
	// run in, which is the one thing a per-machine file must not do — so the
	// Base Directory Specification's rule for an invalid value applies and the
	// variable is ignored. Unset and empty need no case of their own: neither is
	// absolute, so all three take this branch.
	for name, value := range map[string]string{
		"a relative value is ignored": "relative/config",
		"an empty value is ignored":   "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", value)
			got, err := Path()
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(home, ".config", "agentry", "config.json"); got != want {
				t.Errorf("Path() = %q, want %q", got, want)
			}
		})
	}
}

// write puts a settings file where Load will look for it and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "agentry")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() with no file errored: %v", err)
	}
	if s.Found {
		t.Error("Found = true with no file on disk")
	}
	// The path is what a caller with no file is asking for: where to make one.
	if s.Path == "" {
		t.Error("Path is empty; a caller with no file still needs somewhere to create it")
	}
	if len(s.Global) != 0 || len(s.Verbs) != 0 {
		t.Errorf("no file yielded settings: %+v", s)
	}
}

func TestLoadShapesTheFile(t *testing.T) {
	write(t, `{
	  "format": "json",
	  "list": { "limit": 25 },
	  "cost": { "by": "day" }
	}`)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Found {
		t.Error("Found = false for a file that is on disk")
	}
	if got := s.Global["format"]; got != "json" {
		t.Errorf("Global[format] = %q, want %q", got, "json")
	}
	// A number reaches the flag's own parser in the spelling the file used, so
	// `25` and `"25"` are one request rather than two shapes to handle.
	if got := s.Verbs["list"]["limit"]; got != "25" {
		t.Errorf("Verbs[list][limit] = %q, want %q", got, "25")
	}
	if got := s.Verbs["cost"]["by"]; got != "day" {
		t.Errorf("Verbs[cost][by] = %q, want %q", got, "day")
	}
	if _, ok := s.Global["list"]; ok {
		t.Error("a section landed among the top-level settings")
	}
}

func TestLoadRejectsWhatNoFlagCouldTake(t *testing.T) {
	cases := map[string]struct{ body, wants string }{
		"a boolean value":      {`{"format": true}`, "must be a string or a number"},
		"an array value":       {`{"format": ["json"]}`, "must be a string or a number"},
		"a value in a section": {`{"list": {"limit": [1]}}`, "list.limit"},
		"malformed json":       {`{"format":`, "config.json"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := write(t, c.body)
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s", c.body)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error = %q, want it to mention %q", err, c.wants)
			}
			// Every message leads with the file, because the caller typed nothing
			// and the first thing they need is which file to open.
			if !strings.HasPrefix(err.Error(), path) {
				t.Errorf("error = %q, want it to start with the file path %q", err, path)
			}
		})
	}
}
