package locate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectDirName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/me/Projects/dotfiles", "-Users-me-Projects-dotfiles"},
		{"/a", "-a"},
		{"/", "-"},
		{"/x/y/z", "-x-y-z"},
	}
	for _, tt := range tests {
		if got := projectDirName(tt.path); got != tt.want {
			t.Errorf("projectDirName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestSession(t *testing.T) {
	root := t.TempDir()
	old := ProjectsRoot
	ProjectsRoot = root
	t.Cleanup(func() { ProjectsRoot = old })

	const cwd = "/fake/proj"
	projDir := filepath.Join(root, "-fake-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(projDir, "older.jsonl")
	newer := filepath.Join(projDir, "newer.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour)
	mustChtime(t, older, base)
	mustChtime(t, newer, base.Add(time.Minute))

	t.Run("no arg picks newest by mtime", func(t *testing.T) {
		got, err := Session(cwd, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != newer {
			t.Errorf("got %q, want %q", got, newer)
		}
	})

	t.Run("id resolves that session", func(t *testing.T) {
		got, err := Session(cwd, "older")
		if err != nil {
			t.Fatal(err)
		}
		if got != older {
			t.Errorf("got %q, want %q", got, older)
		}
	})

	t.Run("missing id is ErrNoSession", func(t *testing.T) {
		if _, err := Session(cwd, "nope"); !errors.Is(err, ErrNoSession) {
			t.Errorf("got %v, want ErrNoSession", err)
		}
	})

	t.Run("unknown project is ErrNoProject", func(t *testing.T) {
		if _, err := Session("/no/such/project", ""); !errors.Is(err, ErrNoProject) {
			t.Errorf("got %v, want ErrNoProject", err)
		}
	})
}

func TestSessions(t *testing.T) {
	root := t.TempDir()
	old := ProjectsRoot
	ProjectsRoot = root
	t.Cleanup(func() { ProjectsRoot = old })

	const cwd = "/fake/proj"
	projDir := filepath.Join(root, "-fake-proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("empty project is ErrNoSession", func(t *testing.T) {
		if _, err := Sessions(cwd); !errors.Is(err, ErrNoSession) {
			t.Errorf("got %v, want ErrNoSession", err)
		}
	})

	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(projDir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("lists every session jsonl", func(t *testing.T) {
		got, err := Sessions(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("got %d sessions, want 2: %v", len(got), got)
		}
	})

	t.Run("unknown project is ErrNoProject", func(t *testing.T) {
		if _, err := Sessions("/no/such/project"); !errors.Is(err, ErrNoProject) {
			t.Errorf("got %v, want ErrNoProject", err)
		}
	})
}

func mustChtime(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}
