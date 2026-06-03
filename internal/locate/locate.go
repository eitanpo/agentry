// Package locate maps the current directory to its Claude project folder and
// selects which session JSONL to render.
package locate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoProject means $PWD has no matching folder under ~/.claude/projects.
var ErrNoProject = errors.New("no Claude project for this directory")

// ErrNoSession means the project folder holds no selectable session.
var ErrNoSession = errors.New("session not found")

// ProjectsRoot is the directory Claude Code stores logs under. Overridable
// for tests.
var ProjectsRoot = defaultProjectsRoot()

func defaultProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude/projects"
	}
	return filepath.Join(home, ".claude", "projects")
}

// projectDirName encodes an absolute path the way Claude Code names its project
// folders: strip the leading "/", replace every "/" with "-", prefix "-".
// e.g. /Users/x/Projects/dotfiles -> -Users-x-Projects-dotfiles.
func projectDirName(absPath string) string {
	trimmed := strings.TrimPrefix(absPath, "/")
	return "-" + strings.ReplaceAll(trimmed, "/", "-")
}

// ProjectDir returns the project folder for the given working directory, or
// ErrNoProject if it does not exist.
func ProjectDir(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(ProjectsRoot, projectDirName(abs))
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", ErrNoProject
	}
	return dir, nil
}

// Session returns the JSONL path to render. With a non-empty id it resolves
// <id>.jsonl in the project dir. With an empty id it picks the most recent
// session: the newest *.jsonl by modification time (which may be one still in
// progress).
func Session(cwd, id string) (string, error) {
	dir, err := ProjectDir(cwd)
	if err != nil {
		return "", err
	}
	if id != "" {
		path := filepath.Join(dir, id+".jsonl")
		if _, err := os.Stat(path); err != nil {
			return "", ErrNoSession
		}
		return path, nil
	}
	return mostRecent(dir)
}

// Sessions returns the paths of every session JSONL in cwd's project, in no
// particular order. ErrNoProject if the directory maps to no project,
// ErrNoSession if the project holds no sessions.
func Sessions(cwd string) ([]string, error) {
	dir, err := ProjectDir(cwd)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, ErrNoSession
	}
	return matches, nil
}

func mostRecent(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return "", err
	}
	var newest string
	var newestMod int64
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); newest == "" || mod > newestMod {
			newest, newestMod = path, mod
		}
	}
	if newest == "" {
		return "", ErrNoSession
	}
	return newest, nil
}
