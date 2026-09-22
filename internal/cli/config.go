package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/config"
	"github.com/eitanpo/agentry/internal/cost"
	"github.com/eitanpo/agentry/internal/entrypoint"
	"github.com/eitanpo/agentry/internal/jsonl"
)

// configSetting is one settable default: the flag it stands for, the section it
// is written under, and the check its value has to pass. A setting with no
// section is a top-level key, which is what a flag means when it means the same
// thing on every verb.
//
// unsetMeans is what the file would have to say to ask for the built-in
// default, for the two flags whose own default is the empty string. Without it
// `agentry config` would report --from's default as "" — a value the flag
// accepts and that means something else.
type configSetting struct {
	section    string
	key        string
	unsetMeans string
	// values is the accepted set for a key whose values are an enum; takes says
	// what the key accepts where no finite set can. One of the two is always
	// filled, because `agentry config` prints it and a key that cannot say what
	// it accepts sends its reader to a document the file cannot link to.
	values []string
	takes  string
	// check overrides membership in values. Only a key whose values are not a
	// set needs one.
	check func(string) error
}

// accepts is what the key takes, spelled the way the flag's own help spells it.
func (s configSetting) accepts() string {
	if s.takes != "" {
		return s.takes
	}
	return strings.Join(s.values, "|")
}

// validate checks one value for this key. Deriving the check from values by
// default is what keeps the report and the parser honest: a key cannot offer one
// set in `agentry config` and accept another.
func (s configSetting) validate(v string) error {
	if s.check != nil {
		return s.check(v)
	}
	return oneOf(v, s.values)
}

// configSettings is the settable surface, in the order `agentry config` prints
// it. Membership is not a judgment per flag: PRODUCT.md's Configuration section
// makes a flag settable exactly when it has a default to replace, which is why
// the selectors (--since, --project, --used-*) are absent and cannot be added
// here without changing that rule first.
//
// The two `level` entries are why the file has sections at all — the render
// path's level takes four values and cost's takes two, so one flat key could
// only ever be right for one of them.
var configSettings = []configSetting{
	{key: "format", unsetMeans: defaultFormat, values: formatNames},
	{key: "from", unsetMeans: entrypoint.CLI + "+" + entrypoint.App, values: entrypoint.Names},
	{section: "view", key: "level", values: levelNames},
	{section: "list", key: "limit", takes: "N|all", check: func(v string) error { _, err := parseLimitValue(v); return err }},
	{section: "cost", key: "by", values: cost.Axes},
	{section: "cost", key: "chart", values: cost.Charts},
	{section: "cost", key: "level", values: costLevelNames},
}

// configSections are the section names the file may carry, derived from the
// settings themselves so a new setting cannot name a section the parser then
// rejects.
var configSections = func() []string {
	var names []string
	for _, s := range configSettings {
		if s.section != "" && !slices.Contains(names, s.section) {
			names = append(names, s.section)
		}
	}
	return names
}()

// name is how a setting is written when both halves have to be said at once —
// in `agentry config`, in an error, and in the docs.
func (s configSetting) name() string {
	if s.section == "" {
		return s.key
	}
	return s.section + "." + s.key
}

// oneOf checks a config value against an enum flag's accepted set, naming the
// nearest valid value on a typo the way every flag on the command line does.
// The message names no flag: the caller did not use one, and pointing them at
// --format when they mistyped a line in a file sends them to the wrong place.
func oneOf(v string, names []string) error {
	if slices.Contains(names, v) {
		return nil
	}
	if g := nearest(v, names); g != "" {
		return fmt.Errorf("unknown value %q — did you mean %q?", v, g)
	}
	return fmt.Errorf("unknown value %q (want: %s)", v, strings.Join(names, ", "))
}

// validateConfig checks every key and value in the file before the command tree
// is built, so a bad settings file fails the same way whatever verb was typed,
// and fails before a single session is read.
//
// An unknown key is an error rather than a line quietly skipped: a settings file
// whose typo is ignored reads as a preference that was applied, and the caller
// finds out only by noticing that the behavior never changed.
func validateConfig(s *config.Settings) error {
	if s == nil {
		return nil
	}
	for _, key := range sortedKeys(s.Global) {
		setting, ok := findSetting("", key)
		if !ok {
			return configErr(s, "unknown key %q%s", key, suggest(key, append(globalKeyNames(), configSections...)))
		}
		if err := setting.validate(s.Global[key]); err != nil {
			return configErr(s, "%s: %v", setting.name(), err)
		}
	}
	for _, section := range sortedSections(s.Verbs) {
		if !slices.Contains(configSections, section) {
			return configErr(s, "unknown section %q%s", section, suggest(section, append(configSections, globalKeyNames()...)))
		}
		for _, key := range sortedKeys(s.Verbs[section]) {
			setting, ok := findSetting(section, key)
			if !ok {
				return configErr(s, "unknown key %q in section %q%s", key, section, suggest(key, sectionKeyNames(section)))
			}
			if err := setting.validate(s.Verbs[section][key]); err != nil {
				return configErr(s, "%s: %v", setting.name(), err)
			}
		}
	}
	return nil
}

// configErr wraps a message with the file it came from and the exit code a
// broken settings file gets. The path leads every message: the caller typed
// nothing, so the first thing they need is which file to open.
func configErr(s *config.Settings, format string, a ...any) error {
	return &exitError{code: exConfig, err: fmt.Errorf("%s: %s", s.Path, fmt.Sprintf(format, a...))}
}

// suggest returns the " — did you mean ..." tail for a name close to a valid
// one, or nothing at all. It returns a fragment rather than a whole message so
// the key and the section cases can say what they are about first.
func suggest(name string, candidates []string) string {
	if g := nearest(name, candidates); g != "" {
		return fmt.Sprintf(" — did you mean %q?", g)
	}
	return ""
}

func findSetting(section, key string) (configSetting, bool) {
	for _, s := range configSettings {
		if s.section == section && s.key == key {
			return s, true
		}
	}
	return configSetting{}, false
}

func globalKeyNames() []string {
	var names []string
	for _, s := range configSettings {
		if s.section == "" {
			names = append(names, s.key)
		}
	}
	return names
}

func sectionKeyNames(section string) []string {
	var names []string
	for _, s := range configSettings {
		if s.section == section {
			names = append(names, s.key)
		}
	}
	return names
}

// sortedKeys and sortedSections make the first complaint about a file
// deterministic. A map's order would otherwise pick a different one of two bad
// keys per run, which is a bug report nobody can reproduce.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedSections(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configTargets names the commands a setting's flag has to be planted on. The
// root carries both the list and render flag sets, because bare `agentry` lists
// or renders depending on its argument, so it takes the view and list sections
// as well as its own verb's.
func configTargets(section string, tree verbs) []*cobra.Command {
	switch section {
	case "view":
		return []*cobra.Command{tree.root, tree.view}
	case "list":
		return []*cobra.Command{tree.root, tree.list}
	case "cost":
		return []*cobra.Command{tree.cost}
	}
	return []*cobra.Command{tree.root, tree.view, tree.search, tree.list, tree.cost}
}

// verbs is the assembled command tree, held together so the config layer can
// reach each verb's flag set by name rather than by walking and guessing.
type verbs struct {
	root, view, search, list, cost *cobra.Command
}

// builtinDefaults records what each setting defaults to with no file, read off
// the flags themselves rather than written down a second time. It has to run
// before applyConfig, which overwrites exactly these values.
func builtinDefaults(tree verbs) map[string]string {
	out := map[string]string{}
	for _, s := range configSettings {
		cmd := configTargets(s.section, tree)[0]
		f := cmd.Flags().Lookup(s.key)
		if f == nil {
			continue
		}
		value := f.DefValue
		if value == "" {
			value = s.unsetMeans
		}
		out[s.name()] = value
	}
	return out
}

// applyConfig plants the file's values as flag defaults on the tree it was
// given. It sets the flag's value and its printed default, and never touches
// Changed — the file says what a flag defaults to, not that a caller passed it.
// That is what keeps every existing rule about a passed flag meaning what it
// meant: a listing capped by `list.limit` is still a listing whose --limit was
// not typed, so a selector lifts that cap exactly as it lifts the built-in ten.
//
// It runs after validateConfig, so every value here is one its flag accepts and
// every key names a flag that exists on its targets. Each of those flags is a
// string flag, whose Set never fails.
func applyConfig(s *config.Settings, tree verbs) {
	if s == nil {
		return
	}
	for _, setting := range configSettings {
		value, ok := s.Global[setting.key]
		if setting.section != "" {
			value, ok = s.Verbs[setting.section][setting.key]
		}
		if !ok {
			continue
		}
		for _, cmd := range configTargets(setting.section, tree) {
			f := cmd.Flags().Lookup(setting.key)
			if f == nil {
				continue
			}
			_ = f.Value.Set(value)
			f.DefValue = value
		}
	}
}

// configValue is one row of `agentry config`: what the setting is called, what
// it resolves to, and which of the two layers put it there.
type configValue struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Source  string `json:"source"`
	Accepts string `json:"accepts"`
}

// configReport is the whole answer `agentry config` gives, in the machine forms
// as well as the text one. Path is present whether or not the file exists,
// because a caller with no file is asking where to make one.
type configReport struct {
	Path     string        `json:"path"`
	Found    bool          `json:"found"`
	Settings []configValue `json:"settings"`
}

const (
	sourceFile    = "config"
	sourceBuiltin = "default"
)

// newConfigCmd is the `config` verb. It reports the settings file and what each
// default resolves to — the one question a settings file creates that nothing
// else can answer, since reading the file tells a caller what they wrote and not
// what agentry made of it. It never writes the file: the path it prints is where
// to create one.
func newConfigCmd(s *config.Settings, defaults map[string]string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "show the settings file and the defaults in effect",
		Long: "agentry config — the defaults in effect, and the file that sets them\n\n" +
			"Each row names a setting, the value agentry will use, whether that value\n" +
			"came from your settings file or is agentry's built-in default, and what\n" +
			"the setting accepts. The file is optional; --init writes one for you.",
		Args: cobra.NoArgs,
		Example: "  agentry config                 what agentry defaults to, and where it read that\n" +
			"  agentry config --init          write a starter settings file\n" +
			"  agentry config --format json   the report, for scripting",
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := parseFormat(cmd)
			if err != nil {
				return err
			}
			if start, _ := cmd.Flags().GetBool("init"); start {
				// Names both flags rather than the value alone, like every other
				// rejected pair: --format is fine on its own, and it is the
				// combination that cannot be honoured.
				if machineFormat(format) {
					return usageErr("--init cannot be combined with --format %s: it writes a settings file rather than the report --format shapes", format)
				}
				return initConfig(cmd, s, defaults)
			}
			return writeConfigReport(cmd, format, buildConfigReport(s, defaults))
		},
	}
	cmd.Flags().Bool("init", false, "write a starter settings file at that path, every key at its default")
	addFormatFlag(cmd)
	return cmd
}

// initConfig writes the starter file. The refusal to overwrite is the open
// itself (O_EXCL) rather than a prior check, so a file that appears between
// reading the settings and writing them is still not clobbered: settings are
// hand-written, and regenerating one over them destroys the only copy.
func initConfig(cmd *cobra.Command, s *config.Settings, defaults map[string]string) error {
	path, err := settingsPath(s)
	if err != nil {
		return &exitError{code: exCantCreate, err: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &exitError{code: exCantCreate, err: err}
	}
	body, skipped := starterFile(defaults)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return &exitError{code: exCantCreate, err: fmt.Errorf("a settings file is already there: %s — edit it, or move it aside first", path)}
	}
	if err != nil {
		return &exitError{code: exCantCreate, err: err}
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return &exitError{code: exCantCreate, err: err}
	}
	if err := f.Close(); err != nil {
		return &exitError{code: exCantCreate, err: err}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
	// A key the file cannot express is named rather than silently absent: a
	// starter file missing a key reads as a key that does not exist.
	for _, k := range skipped {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agentry: %s is left out — its default is %q and no single value means that; set it to %s to change it\n",
			k.name(), k.unsetMeans, k.accepts())
	}
	return nil
}

// settingsPath is where the file belongs, from the settings agentry already
// read where there are any and from the same resolution otherwise.
func settingsPath(s *config.Settings) (string, error) {
	if s != nil && s.Path != "" {
		return s.Path, nil
	}
	return config.Path()
}

// starterFile renders every key whose built-in default the file can express,
// and returns the ones it cannot. Expressibility is decided by the key's own
// validation rather than by a list kept beside it, so the generated file passes
// the same check a hand-written one does — `from` is left out because its
// default is two entrypoints and no value of --from means both.
//
// Written key by key rather than marshalled from a map, because a map orders
// its keys alphabetically and would interleave the sections with the settings
// above them; the file a caller opens is in the order the docs teach it.
func starterFile(defaults map[string]string) ([]byte, []configSetting) {
	var skipped []configSetting
	expressible := func(s configSetting) (string, bool) {
		v := defaults[s.name()]
		return v, s.validate(v) == nil
	}
	var lines []string
	for _, setting := range configSettings {
		if setting.section != "" {
			continue
		}
		if v, ok := expressible(setting); ok {
			lines = append(lines, "  "+jsonPair(setting.key, v))
		} else {
			skipped = append(skipped, setting)
		}
	}
	for _, section := range configSections {
		var inner []string
		for _, setting := range configSettings {
			if setting.section != section {
				continue
			}
			if v, ok := expressible(setting); ok {
				inner = append(inner, "    "+jsonPair(setting.key, v))
			} else {
				skipped = append(skipped, setting)
			}
		}
		if len(inner) > 0 {
			lines = append(lines, fmt.Sprintf("  %q: {\n%s\n  }", section, strings.Join(inner, ",\n")))
		}
	}
	return []byte("{\n" + strings.Join(lines, ",\n") + "\n}\n"), skipped
}

// jsonPair renders one key and value. A value that is a whole number is written
// as one, since the only key taking a count also takes the word "all" and a
// caller who edits a quoted 10 into a quoted 25 should not have to wonder which
// spelling the file wanted.
func jsonPair(key, value string) string {
	if _, err := strconv.Atoi(value); err == nil {
		return fmt.Sprintf("%q: %s", key, value)
	}
	return fmt.Sprintf("%q: %q", key, value)
}

// buildConfigReport resolves every setting against the file, in the order
// configSettings declares. A setting absent from the file reports the built-in
// default and says so, rather than being left out — a row that disappears when
// it is not configured makes the absence of a key look like the absence of a
// setting.
func buildConfigReport(s *config.Settings, defaults map[string]string) configReport {
	r := configReport{}
	if s != nil {
		r.Path, r.Found = s.Path, s.Found
	}
	for _, setting := range configSettings {
		value, source := defaults[setting.name()], sourceBuiltin
		if s != nil {
			from := s.Global
			if setting.section != "" {
				from = s.Verbs[setting.section]
			}
			if v, ok := from[setting.key]; ok {
				value, source = v, sourceFile
			}
		}
		r.Settings = append(r.Settings, configValue{
			Key: setting.name(), Value: value, Source: source, Accepts: setting.accepts(),
		})
	}
	return r
}

func writeConfigReport(cmd *cobra.Command, format string, r configReport) error {
	out := cmd.OutOrStdout()
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	case "jsonl":
		// One record, which is a well-formed stream. The settings are a fixed
		// handful that arrive together, so splitting them into a record apiece
		// would buy a consumer nothing the single object does not already give.
		if err := jsonl.New(out).Emit("config", "", time.Time{}, r); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}

	state := "not created yet"
	if r.Found {
		state = "in use"
	}
	fmt.Fprintf(out, "settings file: %s (%s)\n\n", r.Path, state)
	// The accepted values close the row rather than being cut to fit it: they are
	// the widest column and the one a caller is reading the table for, and the
	// alternative to printing them here is a JSON file with no comments and a
	// reference only this document holds.
	key, value, source := len("setting"), len("value"), len("source")
	for _, v := range r.Settings {
		key = max(key, len(v.Key))
		value = max(value, len(v.Value))
		source = max(source, len(v.Source))
	}
	fmt.Fprintf(out, "%-*s  %-*s  %-*s  %s\n", key, "setting", value, "value", source, "source", "accepts")
	for _, v := range r.Settings {
		fmt.Fprintf(out, "%-*s  %-*s  %-*s  %s\n", key, v.Key, value, v.Value, source, v.Source, v.Accepts)
	}
	return nil
}
