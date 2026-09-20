// Package cli wires agentry's command-line surface: a Cobra verb tree whose
// bare/root path renders a session, with `view` and `list` verbs. It owns
// argument parsing, did-you-mean suggestions, and the mapping from errors to
// sysexits exit codes; the render/list/parse/locate/model packages do the work.
package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/eitanpo/agentry/internal/config"
	"github.com/eitanpo/agentry/internal/entrypoint"
	"github.com/eitanpo/agentry/internal/render"
)

// sysexits.h codes.
const (
	exUsage      = 64 // command-line usage error
	exNoInput    = 66 // input (project/session) does not exist
	exConfig     = 78 // the settings file is unreadable, or says something agentry cannot honor
	exCantCreate = 73 // a file could not be written, or is already there
)

// levels maps each verbosity preset to the channels it enables.
// levels are the transcript's verbosity presets. The metrics channel is not
// among them: the footer's aggregate sections print at every level and leave
// only on --no-metrics, so no level turns them on or off. PRODUCT.md's Verbosity
// section owns the split.
var levels = map[string]render.Channels{
	"minimal":  {},
	"standard": {Thinking: true},
	"detailed": {Thinking: true, Tools: true, Subagents: true},
	"full":     {Thinking: true, Tools: true, ToolResults: true, Subagents: true},
}

// Candidate sets for nearest(): valid verbs, --level values, --include channels.
var (
	verbNames    = []string{"view", "list", "cost", "config"}
	levelNames   = []string{"minimal", "standard", "detailed", "full"}
	includeNames = []string{"prompts", "tools", "files", "model", "cost", "outputs", "last-reply", "all"}
	formatNames  = []string{"json", "jsonl", "text"}
	// --limit takes a count or this one keyword, so the suggestion set is a single
	// entry: a mistyped number is arithmetic, not a near-miss on a name.
	limitNames = []string{"all"}
)

// effortLevels are the levels `claude --effort` accepts, offered as completion
// for --effort. They are suggestions only: the filter never validates against
// them, because Claude Code can add a level without notice and an unknown one
// should return no sessions rather than a usage error.
var effortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// fixedComp is a shell-completion handler for an enum flag: it offers a static
// candidate set and suppresses file completion, so `--format <Tab>` proposes
// json|text rather than filenames.
func fixedComp(candidates []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
}

// parseFormat validates the --format flag — shared by the render path and
// list — returning the normalized value ("" and "text" both mean the text
// form) or a usage error that names the offending value and suggests the
// nearest valid format.
func parseFormat(cmd *cobra.Command) (string, error) {
	format, _ := cmd.Flags().GetString("format")
	if format == "" {
		return format, nil
	}
	for _, name := range formatNames {
		if format == name {
			return format, nil
		}
	}
	if g := nearest(format, formatNames); g != "" {
		return "", usageErr("--format: unknown format %q — did you mean %q?", format, g)
	}
	// Derived from formatNames rather than spelled again: an error offering a
	// format that no longer exists, or omitting one that does, is invisible to a
	// search for the name it got wrong.
	return "", usageErr("--format: unknown format %q (want: %s)", format, strings.Join(formatNames, ", "))
}

// machineFormat reports whether format is one of the machine-readable forms.
// Several rules turn on that rather than on a particular spelling — the
// listing's default cap, --chart's rejection, what an empty result prints — and
// keying them on the literal "json" is how a second machine format silently
// inherits the text form's behavior.
func machineFormat(format string) bool { return format == "json" || format == "jsonl" }

// defaultFormat is what a caller who named no format gets, and is the flag's own
// default so that help prints it. An empty default would leave pflag with
// nothing to print and the help text asserting a default of its own, which then
// contradicts the settings file the moment one sets this key.
const defaultFormat = "text"

// formatHelp is the --format flag's one-line help, derived from formatNames so
// the two cannot disagree. It names no default: pflag prints the flag's own,
// which is the one a caller actually gets, file or no file.
func formatHelp() string {
	return "output format: " + strings.Join(formatNames[:len(formatNames)-1], ", ") + " or " + formatNames[len(formatNames)-1]
}

// parseFrom validates the --from selector, shared by the listing and by the
// render path's no-id resolution — both mean the same thing by "cli" and both
// must reject the same typo. It returns the value unchanged (empty = the
// default) or a usage error naming the nearest valid value, like every other
// enum flag.
func parseFrom(cmd *cobra.Command) (string, error) {
	from, _ := cmd.Flags().GetString("from")
	if from == "" || slices.Contains(entrypoint.Names, from) {
		return from, nil
	}
	if g := nearest(from, entrypoint.Names); g != "" {
		return "", usageErr("--from: unknown source %q — did you mean %q?", from, g)
	}
	return "", usageErr("--from: unknown source %q (want: %s)", from, strings.Join(entrypoint.Names, ", "))
}

// exitError carries the sysexits code a failure should exit with. RunE returns
// these so Execute can both print the message in agentry's voice and exit with
// the right code, rather than Cobra's default of dumping usage and exiting 1.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func usageErr(format string, a ...any) error {
	return &exitError{code: exUsage, err: fmt.Errorf(format, a...)}
}

func noInputErr(err error) error {
	return &exitError{code: exNoInput, err: err}
}

// Execute builds the command tree and runs it, returning the process exit code.
// version is injected from main (ldflags target main.Version).
//
// The settings file is read and checked before the tree is built, because its
// values become the flags' defaults and a flag's default has to be right before
// anything parses a command line against it. A file agentry cannot honor stops
// the run here, whatever verb was typed: the message names the file and the key,
// which is the whole of the repair.
func Execute(version string) int {
	settings, err := config.Load()
	if err == nil {
		err = validateConfig(settings)
	}
	return run(newTree(version, settings, err), os.Args[1:])
}

// newTree builds the command tree for what reading the settings file produced.
// A file agentry cannot honor yields a tree that refuses every verb with that
// error — as a run-time refusal rather than an early exit, so `--help` and
// `--version` still answer. Cobra settles both before it reaches this hook, and
// a caller repairing their settings file is exactly who needs to look something
// up.
func newTree(version string, settings *config.Settings, loadErr error) *cobra.Command {
	if loadErr != nil {
		settings = nil
	}
	root := newRootCmd(version, settings)
	if loadErr != nil {
		var ee *exitError
		if !errors.As(loadErr, &ee) {
			loadErr = &exitError{code: exConfig, err: loadErr}
		}
		root.PersistentPreRunE = func(*cobra.Command, []string) error { return loadErr }
	}
	return root
}

// run executes an assembled command tree with explicit args and maps the
// outcome to a sysexits code, printing any error in agentry's voice. Split from
// Execute so tests can drive it with injected args and a captured output stream.
func run(root *cobra.Command, args []string) int {
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(root.ErrOrStderr(), "agentry: %s\n", err.Error())
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	// Flag-parse / unknown-command errors from Cobra/pflag are usage errors.
	return exUsage
}

// usageTemplate is agentry's help/usage layout. It mirrors Cobra's default but
// splits a render command's local flags into a render-scoped group and the rest,
// so the bare command's help no longer presents --level and the channel toggles
// as if they were global. Commands with no render flags (list, completion) omit
// the render section and read like the default. Set on root; the rest of the
// tree inherits it.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{$rf := renderFlagUsages .}}{{if $rf}}

Render flags for single sessions:
{{$rf | trimTrailingWhitespaces}}{{end}}{{$lf := otherLocalFlagUsages .}}{{if $lf}}

Flags:
{{$lf | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

// renderFlagUsages / otherLocalFlagUsages partition a command's local flags for
// the usage template: the render group vs. everything else local (e.g. --help,
// --version, and --no-color on the root). Inherited flags are left to the
// template's Global Flags section.
func renderFlagUsages(cmd *cobra.Command) string     { return localFlagUsages(cmd, true) }
func otherLocalFlagUsages(cmd *cobra.Command) string { return localFlagUsages(cmd, false) }

func localFlagUsages(cmd *cobra.Command, render bool) string {
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if isRenderFlag(f.Name) == render {
			fs.AddFlag(f)
		}
	})
	if !fs.HasFlags() {
		return ""
	}
	return fs.FlagUsages()
}

// flagErrorFunc replaces pflag's bare "unknown flag: --x" with a suggestion of
// the nearest valid flag on the command that failed to parse. Set once on the
// root; Cobra resolves it up the parent chain, so it applies to every verb, and
// cmd is the verb whose flag set is the right candidate pool.
func flagErrorFunc(cmd *cobra.Command, err error) error {
	const prefix = "unknown flag: "
	if msg := err.Error(); strings.HasPrefix(msg, prefix) {
		tok := strings.TrimPrefix(msg, prefix) // e.g. "--thnking"
		if g := nearest(strings.TrimLeft(tok, "-"), flagNames(cmd)); g != "" {
			return usageErr("unknown flag %s — did you mean --%s?", tok, g)
		}
	}
	return usageErr("%s", err.Error())
}

// flagNames returns the long names of every flag visible on cmd (locals plus
// inherited persistents), the pool flag-name suggestions are drawn from.
func flagNames(cmd *cobra.Command) []string {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	return names
}

// nearest returns the candidate closest to tok by edit distance when one is
// within a length-scaled threshold, else "" — no confident guess means suggest
// nothing. Levenshtein is a tool choice here, not a standardized metric.
func nearest(tok string, candidates []string) string {
	maxDist := 2
	if len(tok) > 6 {
		maxDist = 3
	}
	best, bestDist := "", -1
	for _, c := range candidates {
		d := levenshtein(tok, c)
		if d <= maxDist && (bestDist < 0 || d < bestDist) {
			best, bestDist = c, d
		}
	}
	return best
}

// levenshtein is the standard edit distance (insert/delete/substitute = 1).
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

// terminal reports whether to colorize and the render width. Color is on only
// for a TTY stdout with NO_COLOR unset and --no-color absent.
//
// The probe reads the process's own stdout while the renderers write to
// cmd.OutOrStdout() — the same file in every real invocation. A caller that
// points the command's writer at some other terminal gets color and width
// measured for stdout rather than for what it is about to write to.
func terminal(noColor bool) (color bool, width int) {
	fd := int(os.Stdout.Fd())
	isTTY := term.IsTerminal(fd)
	color = isTTY && !noColor && os.Getenv("NO_COLOR") == ""
	if isTTY {
		if w, _, err := term.GetSize(fd); err == nil {
			width = w
		}
	}
	return color, width
}
