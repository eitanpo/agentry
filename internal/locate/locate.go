// Package locate maps the current directory to its Claude project folder and
// selects which session JSONL to render.
package locate

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
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

// ProjectDirName encodes an absolute path the way Claude Code names its project
// folders: every character outside [A-Za-z0-9] becomes "-", the leading "/"
// included — which is why the name starts with one.
// e.g. /Users/x/Projects/dotfiles -> -Users-x-Projects-dotfiles.
//
// It is not only "/" that is replaced. "." and "_" go too, so
// /Users/x/.claude/worktrees/w encodes as -Users-x--claude-worktrees-w with a
// doubled "-". Replacing only "/" reproduced 32 of the 63 project folders on
// the development machine; this rule reproduces all 63. Getting it wrong is not
// a near miss — the wrong name simply does not exist, so agentry reported "no
// Claude project for this directory" for every dot-component path, which is
// every worktree Claude Code creates under <repo>/.claude/worktrees/.
//
// Exported so tests build a fixture project folder through the same encoder the
// lookup uses, rather than open-coding the rule a second time and drifting.
func ProjectDirName(absPath string) string {
	var b strings.Builder
	b.Grow(len(absPath))
	for _, r := range absPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ProjectDir returns the project folder for the given working directory, or
// ErrNoProject if it does not exist.
func ProjectDir(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(ProjectsRoot, ProjectDirName(abs))
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

// ProjectDirs returns every project folder under ProjectsRoot, sorted. An
// unreadable root is an error; a readable but empty one returns no dirs.
func ProjectDirs() ([]string, error) {
	ents, err := os.ReadDir(ProjectsRoot)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(ProjectsRoot, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// ProjectCwd returns the working directory a project folder belongs to, read
// from the cwd field of its sessions.
//
// The folder name cannot be reversed — "-a-b-c" could encode /a/b/c or /a/b-c
// (see ProjectDirName) — but every log entry records the path outright, so
// reversing is unnecessary. Reading it also works for a project whose directory
// has since been deleted or renamed, which walking the filesystem cannot: on the
// development machine 37 of 63 project folders had no surviving directory.
//
// One folder maps to exactly one working directory, since the folder name is
// derived from that path, so the first cwd found settles it. Returns "" with no
// error when no session carries one.
func ProjectCwd(dir string) (string, error) {
	sessions, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return "", err
	}
	sort.Strings(sessions)
	for _, path := range sessions {
		if cwd := firstCwd(path); cwd != "" {
			return cwd, nil
		}
	}
	return "", nil
}

// firstCwd scans a session log for the first entry carrying a non-empty cwd.
// Malformed lines are skipped rather than aborting the file, matching the
// parser: a reader that stops at the first bad line silently drops every later
// one, which reads as an absent field rather than as an error.
func firstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		var e struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Cwd != "" {
			return e.Cwd
		}
	}
	return ""
}

// maxLine bounds a single log line. Tool results are stored inline with full
// content, so lines run far past bufio's 64KB default; a short buffer would
// fail the scan on exactly the sessions that did the most work.
const maxLine = 16 * 1024 * 1024

// SessionsAll returns every session JSONL under every project folder, in no
// particular order, paired with nothing — the caller reads each session's own
// cwd. ErrNoSession when no project holds a session.
func SessionsAll() ([]string, error) {
	dirs, err := ProjectDirs()
	if err != nil {
		return nil, err
	}
	return sessionsIn(dirs)
}

// SessionsUnder returns every session JSONL belonging to a project at or under
// root — root itself plus anything nested inside it, which is how a repo picks
// up the worktrees Claude Code creates under <repo>/.claude/worktrees/ and how
// a parent directory picks up every repo beneath it. Selection is by each
// project's recorded cwd, not by its folder name, because the name is lossy.
// ErrNoProject when no project matches.
func SessionsUnder(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dirs, err := ProjectDirs()
	if err != nil {
		return nil, err
	}
	var matched []string
	for _, dir := range dirs {
		cwd, err := ProjectCwd(dir)
		if err != nil || cwd == "" {
			continue
		}
		if underPath(abs, cwd) {
			matched = append(matched, dir)
		}
	}
	if len(matched) == 0 {
		return nil, ErrNoProject
	}
	return sessionsIn(matched)
}

// underPath reports whether path is root or lives inside it, compared by whole
// path components — so /a/bc is not "under" /a/b, which a string-prefix test
// would wrongly accept.
func underPath(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func sessionsIn(dirs []string) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		if err != nil {
			return nil, err
		}
		out = append(out, matches...)
	}
	if len(out) == 0 {
		return nil, ErrNoSession
	}
	return out, nil
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
