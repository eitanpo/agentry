package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eitanpo/agentry/internal/config"
	"github.com/eitanpo/agentry/internal/locate"
)

// execWith is exec with a settings file in force — the state Execute puts the
// tree in after reading one off disk.
func execWith(s *config.Settings, args ...string) (code int, stdout, stderr string) {
	root := newRootCmd("test", s)
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	code = run(root, args)
	return code, out.String(), errBuf.String()
}

// fileSettings is a settings file as Load would have returned it.
func fileSettings(global map[string]string, verbs map[string]map[string]string) *config.Settings {
	if global == nil {
		global = map[string]string{}
	}
	if verbs == nil {
		verbs = map[string]map[string]string{}
	}
	return &config.Settings{Path: "/nowhere/agentry/config.json", Found: true, Global: global, Verbs: verbs}
}

// TestConfigSetsTheListingCap is the behavior the settings file exists for: a
// default replaced without the flag being passed. It also pins the part that is
// easy to get wrong — the file sets what --limit defaults to, so every rule
// about --limit having been *passed* keeps its meaning.
func TestConfigSetsTheListingCap(t *testing.T) {
	const sessions = 12
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	orig := locate.ProjectsRoot
	locate.ProjectsRoot = root
	t.Cleanup(func() { locate.ProjectsRoot = orig })
	for i := 0; i < sessions; i++ {
		writeProject(t, root, cwd, fmt.Sprintf("%08d-0000-0000-0000-000000000000", i))
	}
	capped := fileSettings(nil, map[string]map[string]string{"list": {"limit": "3"}})
	rows := func(t *testing.T, args ...string) int {
		t.Helper()
		_, out, _ := execWith(capped, append([]string{"list"}, args...)...)
		return strings.Count(out, fixtureDate)
	}

	if n := rows(t); n != 3 {
		t.Errorf("a bare listing showed %d rows, want the file's cap of 3", n)
	}
	// The bare command lists, so it reads the list section too — a cap that
	// applied to `agentry list` alone would miss the form most callers type.
	_, bare, _ := execWith(capped)
	if n := strings.Count(bare, fixtureDate); n != 3 {
		t.Errorf("bare `agentry` showed %d rows, want the file's cap of 3", n)
	}
	// A selector lifts a configured cap exactly as it lifts the built-in ten:
	// the file changed the default, and --limit still was not passed.
	if n := rows(t, "--from", "all"); n != sessions {
		t.Errorf("--from all showed %d rows, want every session (%d)", n, sessions)
	}
	// A flag beats the file, which is the whole of the precedence rule.
	if n := rows(t, "--limit", "5"); n != 5 {
		t.Errorf("--limit 5 showed %d rows, want 5", n)
	}
	if n := rows(t, "--limit", "all"); n != sessions {
		t.Errorf("--limit all showed %d rows, want every session (%d)", n, sessions)
	}
}

// TestConfigSetsHelpsDefault pins that the file lands before parsing rather than
// after it: help is generated from the flag's default, so a default planted too
// late shows the caller a number agentry is no longer using.
func TestConfigSetsHelpsDefault(t *testing.T) {
	s := fileSettings(nil, map[string]map[string]string{"view": {"level": "detailed"}})
	_, out, _ := execWith(s, "view", "--help")
	if !strings.Contains(out, `--level string`) || !strings.Contains(out, `(default "detailed")`) {
		t.Errorf("view --help did not offer the configured default:\n%s", out)
	}
}

// TestHelpStatesOneDefaultPerFlag pins the other half of planting defaults
// early: a flag whose help text spells its own default out contradicts the
// settings file the moment one sets that key, because pflag prints the flag's
// real default beside it.
func TestHelpStatesOneDefaultPerFlag(t *testing.T) {
	s := fileSettings(map[string]string{"format": "json"}, nil)
	_, out, _ := execWith(s, "list", "--help")
	if !strings.Contains(out, `(default "json")`) {
		t.Errorf("list --help did not offer the configured format:\n%s", out)
	}
	if strings.Contains(out, "text (default)") {
		t.Errorf("list --help still claims text is the default:\n%s", out)
	}
}

// TestConfigLevelIsPerVerb pins the collision the file's sections exist for:
// `full` is a level the render path takes and `cost` does not.
func TestConfigLevelIsPerVerb(t *testing.T) {
	if err := validateConfig(fileSettings(nil, map[string]map[string]string{"view": {"level": "full"}})); err != nil {
		t.Errorf("view.level full was rejected: %v", err)
	}
	if err := validateConfig(fileSettings(nil, map[string]map[string]string{"cost": {"level": "full"}})); err == nil {
		t.Error("cost.level full was accepted; cost takes minimal or standard")
	}
}

func TestConfigRejectsWhatItCannotHonor(t *testing.T) {
	cases := map[string]struct {
		settings *config.Settings
		wants    []string
	}{
		"an unknown top-level key": {
			fileSettings(map[string]string{"fromat": "json"}, nil),
			[]string{`unknown key "fromat"`, `did you mean "format"`},
		},
		"an unknown section": {
			fileSettings(nil, map[string]map[string]string{"cots": {"by": "day"}}),
			[]string{`unknown section "cots"`, `did you mean "cost"`},
		},
		"an unknown key inside a section": {
			fileSettings(nil, map[string]map[string]string{"list": {"limt": "5"}}),
			[]string{`unknown key "limt"`, `section "list"`, `did you mean "limit"`},
		},
		"a selector, which has no default to replace": {
			fileSettings(map[string]string{"since": "today"}, nil),
			[]string{`unknown key "since"`},
		},
		"a value its flag would reject": {
			fileSettings(nil, map[string]map[string]string{"view": {"level": "detaild"}}),
			[]string{"view.level", `did you mean "detailed"`},
		},
		"a limit that is not a count": {
			fileSettings(nil, map[string]map[string]string{"list": {"limit": "twenty"}}),
			[]string{"list.limit", `neither a count nor "all"`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateConfig(c.settings)
			if err == nil {
				t.Fatal("the settings were accepted")
			}
			// A file is not a command line: the caller typed nothing wrong, so the
			// usage code would send them to re-read their own invocation.
			var ee *exitError
			if !errors.As(err, &ee) || ee.code != exConfig {
				t.Errorf("exit code = %v, want %d (EX_CONFIG)", err, exConfig)
			}
			for _, want := range append(c.wants, c.settings.Path) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

// TestConfigVerbReportsEverySetting pins the question a settings file creates:
// not what the caller wrote, but what agentry made of it.
func TestConfigVerbReportsEverySetting(t *testing.T) {
	s := fileSettings(map[string]string{"from": "all"}, map[string]map[string]string{"list": {"limit": "25"}})
	code, out, _ := execWith(s, "config")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}
	for _, want := range []string{s.Path, "list.limit", "25", sourceFile, "cost.by", "total", sourceBuiltin} {
		if !strings.Contains(out, want) {
			t.Errorf("`agentry config` did not report %q:\n%s", want, out)
		}
	}
	// A row that vanishes when it is not configured makes a missing key look like
	// a missing setting, so every setting is reported either way.
	if got, want := strings.Count(out, "\n"), len(configSettings); got < want {
		t.Errorf("output has %d lines for %d settings:\n%s", got, want, out)
	}

	t.Run("json carries the same facts", func(t *testing.T) {
		_, out, _ := execWith(s, "config", "--format", "json")
		var got configReport
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("stdout is not valid JSON (%v); got %q", err, out)
		}
		if len(got.Settings) != len(configSettings) {
			t.Errorf("json carried %d settings, want %d", len(got.Settings), len(configSettings))
		}
		if got.Path != s.Path || !got.Found {
			t.Errorf("json reported path %q found %v, want %q true", got.Path, got.Found, s.Path)
		}
	})

	t.Run("a machine's own defaults report as defaults", func(t *testing.T) {
		// What Load returns when the file is not there: a path to create one at,
		// and nothing set.
		none := &config.Settings{Path: "/nowhere/agentry/config.json"}
		_, out, _ := execWith(none, "config")
		// Counted rather than searched for: the file's own path ends in
		// config.json, so looking for the word alone finds the header.
		if n := strings.Count(out, sourceBuiltin); n != len(configSettings) {
			t.Errorf("%d of %d rows reported the built-in default:\n%s", n, len(configSettings), out)
		}
		// --from's default is two entrypoints and its flag's default is the empty
		// string, which would otherwise print as a blank the flag also accepts.
		if !strings.Contains(out, "cli+app") {
			t.Errorf("`agentry config` did not name --from's built-in default:\n%s", out)
		}
	})
}

// TestBrokenConfigRefusesVerbsButNotHelp pins what a caller can still do while
// their settings file is broken. The refusal has to reach every verb, and it has
// to leave the two surfaces that help them fix it — a version to quote in a bug
// report, and help to check what a key was called.
func TestBrokenConfigRefusesVerbsButNotHelp(t *testing.T) {
	broken := errors.New("/nowhere/agentry/config.json: unknown key \"levl\"")
	exec := func(args ...string) (int, string, string) {
		root := newTree("test", nil, broken)
		var out, errBuf bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errBuf)
		return run(root, args), out.String(), errBuf.String()
	}
	for _, verb := range []string{"list", "view", "cost", "config"} {
		code, _, stderr := exec(verb)
		if code != exConfig {
			t.Errorf("`agentry %s` exited %d, want %d (EX_CONFIG)", verb, code, exConfig)
		}
		if !strings.Contains(stderr, "unknown key") {
			t.Errorf("`agentry %s` said %q, want the settings error", verb, stderr)
		}
	}
	for _, flag := range []string{"--help", "--version"} {
		code, out, _ := exec(flag)
		if code != 0 {
			t.Errorf("`agentry %s` exited %d, want 0 while the settings file is broken", flag, code)
		}
		if out == "" {
			t.Errorf("`agentry %s` printed nothing", flag)
		}
	}
}

// TestConfigSettingsNameRealFlags pins the settings table against the flag sets
// it plants values on. A renamed or moved flag otherwise leaves a key that
// validates, reports, and changes nothing.
func TestConfigSettingsNameRealFlags(t *testing.T) {
	root := newRootCmd("test", nil)
	tree := verbs{root: root}
	for _, c := range root.Commands() {
		switch c.Name() {
		case "view":
			tree.view = c
		case "list":
			tree.list = c
		case "cost":
			tree.cost = c
		}
	}
	for _, s := range configSettings {
		for _, cmd := range configTargets(s.section, tree) {
			if cmd.Flags().Lookup(s.key) == nil {
				t.Errorf("%s has no flag --%s to set", cmd.Name(), s.key)
			}
		}
	}
}

// loadFrom reads a settings file out of a temporary config home with the real
// loader, so the tests below exercise the resolution `agentry config --init`
// writes through rather than a path handed to them.
func loadFrom(t *testing.T, dir string) *config.Settings {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	s, err := config.Load()
	if err != nil {
		t.Fatalf("loading settings from %s: %v", dir, err)
	}
	return s
}

// TestConfigNamesWhatEachKeyAccepts pins the report as the key reference. A
// JSON file carries no comments, so a row that names a key without naming what
// it takes sends its reader to a document the file cannot link to.
func TestConfigNamesWhatEachKeyAccepts(t *testing.T) {
	_, out, _ := execWith(fileSettings(nil, nil), "config")
	for _, want := range []string{
		"accepts",
		"minimal|standard|detailed|full", // view.level
		"minimal|standard",               // cost.level, the narrower set on the same name
		"N|all",                          // the one key that is not an enum
		"json|jsonl|text",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("`agentry config` did not name %q:\n%s", want, out)
		}
	}
	// Derived from the flag's own value list rather than spelled again, so the
	// two cannot drift apart.
	for _, setting := range configSettings {
		if setting.takes == "" && setting.accepts() != strings.Join(setting.values, "|") {
			t.Errorf("%s accepts %q, want its flag's values", setting.name(), setting.accepts())
		}
	}
}

// TestConfigInitWritesAValidStarterFile pins the round trip: what --init writes
// has to survive the check any hand-written file gets, or the first thing a
// caller does with agentry's own output is fix it.
func TestConfigInitWritesAValidStarterFile(t *testing.T) {
	dir := t.TempDir()
	code, out, stderr := execWith(loadFrom(t, dir), "config", "--init")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "config.json") {
		t.Errorf("stdout = %q, want the file it wrote", out)
	}

	written := loadFrom(t, dir)
	if !written.Found {
		t.Fatal("no settings file after --init")
	}
	if err := validateConfig(written); err != nil {
		t.Fatalf("agentry wrote a file it rejects: %v", err)
	}
	if got := written.Verbs["list"]["limit"]; got != "10" {
		t.Errorf("list.limit = %q, want the built-in default 10", got)
	}
	// from's default is two entrypoints and --from has no value meaning both, so
	// writing any value would silently narrow what the caller sees.
	if got, ok := written.Global["from"]; ok {
		t.Errorf("from was written as %q; no value of --from means its default", got)
	}
	if !strings.Contains(stderr, "from is left out") {
		t.Errorf("stderr = %q, want the key it could not write", stderr)
	}
}

func TestConfigInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentry", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := []byte(`{"list": {"limit": 25}}`)
	if err := os.WriteFile(path, mine, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := execWith(loadFrom(t, dir), "config", "--init")
	if code != exCantCreate {
		t.Errorf("exit code = %d, want %d (EX_CANTCREAT)", code, exCantCreate)
	}
	if !strings.Contains(stderr, "already there") {
		t.Errorf("stderr = %q, want it to say the file is already there", stderr)
	}
	// The refusal is the point: settings are hand-written and this is the only
	// copy of them.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(mine) {
		t.Errorf("the settings file changed:\n%s", after)
	}
}

func TestConfigInitRejectsMachineFormat(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := execWith(loadFrom(t, dir), "config", "--init", "--format", "json")
	if code != exUsage {
		t.Errorf("exit code = %d, want %d", code, exUsage)
	}
	// Both flags, since either is fine alone and it is the pair that cannot be
	// honoured.
	for _, want := range []string{"--init", "--format"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to name %s", stderr, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "agentry", "config.json")); err == nil {
		t.Error("the rejected combination still wrote a file")
	}
}
