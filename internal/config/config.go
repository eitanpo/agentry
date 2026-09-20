// Package config reads agentry's optional settings file — the defaults a
// caller would otherwise retype as flags on every invocation.
//
// It reads and shapes the file; it does not know which keys exist. Values come
// back as the strings a flag would have been given, so the cli package can
// check them with the same parsers it uses for the command line and report a
// bad value in the same words. PRODUCT.md's Configuration section owns the key
// surface and the precedence rule.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// dir and file are the path's two halves, under whichever config home applies.
const (
	dir  = "agentry"
	file = "config.json"
)

// Settings is one settings file as read from disk. Path is always set — a
// caller with no file still needs somewhere to be told to create one — and
// Found says whether anything was there to read.
//
// Global holds the keys that sit at the file's top level; Verbs holds one map
// per section. Which names are legal in either is the cli package's business:
// this package reports what the file said, in the shape the file said it.
type Settings struct {
	Path   string
	Found  bool
	Global map[string]string
	Verbs  map[string]map[string]string
}

// Path returns where agentry looks for its settings file:
// $XDG_CONFIG_HOME/agentry/config.json, or ~/.config/agentry/config.json when
// that variable is unset, empty, or relative. Ignoring a relative value is the
// Base Directory Specification's own rule for an invalid one, and the rule
// agentry already applies to CLAUDE_CONFIG_DIR — a relative config home would
// otherwise resolve against the directory the caller happened to run in, which
// is the one thing a per-machine file must not do.
func Path() (string, error) {
	if home := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, dir, file), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory to look for %s in: %w", filepath.Join(dir, file), err)
	}
	return filepath.Join(home, ".config", dir, file), nil
}

// Load reads the settings file. A file that is not there is not an error: it
// means every default is the built-in one, which is the state most callers are
// in. Anything else — unreadable, not an object, a value of a type no flag
// could take — is an error naming the file, because a settings file that is
// present and wrong has to be repaired rather than worked around.
func Load() (*Settings, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	s := &Settings{
		Path:   path,
		Global: map[string]string{},
		Verbs:  map[string]map[string]string{},
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Found = true

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, value := range top {
		// A section is told from a setting by the shape of its value, not by its
		// name: this package does not know the section names, and a caller who
		// writes an object where a string belongs gets told which key it was.
		if isObject(value) {
			var section map[string]json.RawMessage
			if err := json.Unmarshal(value, &section); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, name, err)
			}
			keys := map[string]string{}
			for key, v := range section {
				str, err := scalar(v)
				if err != nil {
					return nil, fmt.Errorf("%s: %s.%s: %w", path, name, key, err)
				}
				keys[key] = str
			}
			s.Verbs[name] = keys
			continue
		}
		str, err := scalar(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, name, err)
		}
		s.Global[name] = str
	}
	return s, nil
}

// isObject reports whether a JSON value is an object, which is what makes it a
// section rather than a setting.
func isObject(raw json.RawMessage) bool {
	return strings.HasPrefix(strings.TrimLeft(string(raw), " \t\r\n"), "{")
}

// scalar renders one JSON value as the string its flag would have been given.
// A number is kept in the spelling the file used, so a count reaches the flag's
// own parser rather than being reformatted on the way — `"limit": 25` and
// `"limit": "25"` are the same request, and a number that is not a count is the
// flag's error to report, in the flag's words.
func scalar(raw json.RawMessage) (string, error) {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str, nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		return num.String(), nil
	}
	return "", fmt.Errorf("value must be a string or a number, not %s", strings.TrimSpace(string(raw)))
}
