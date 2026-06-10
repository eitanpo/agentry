// Package cli wires agentry's command-line surface: a Cobra verb tree whose
// bare/root path renders a session, with `view` and `list` verbs. It owns
// argument parsing, did-you-mean suggestions, and the mapping from errors to
// sysexits exit codes; the render/list/parse/locate/model packages do the work.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/eitanpo/agentry/internal/render"
)

// sysexits.h codes.
const (
	exUsage   = 64 // command-line usage error
	exNoInput = 66 // input (project/session) does not exist
)

// levels maps each verbosity preset to the channels it enables.
var levels = map[string]render.Channels{
	"minimal":  {},
	"standard": {Thinking: true},
	"detailed": {Thinking: true, Tools: true, Metrics: true},
	"full":     {Thinking: true, Tools: true, Subagents: true, Metrics: true},
}

// Candidate sets for nearest(): valid verbs, --level values, --include channels.
var (
	verbNames    = []string{"view", "list"}
	levelNames   = []string{"minimal", "standard", "detailed", "full"}
	includeNames = []string{"prompts", "all"}
)

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
func Execute(version string) int {
	return run(newRootCmd(version), os.Args[1:])
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
