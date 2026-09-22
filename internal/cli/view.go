package cli

import (
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/render"
)

// channelNames are the transcript's per-channel overrides, each exposed as
// --name / --no-name. Single source for both flag registration and
// render-flag-group detection.
var channelNames = []string{"thinking", "tools", "tool-results", "subagents"}

// metricsChannel names the footer's three aggregate sections. It is the one
// channel with no --metrics form: the sections print at every level, so the flag
// could never change anything, and a flag that changes nothing reads as a
// setting that failed. Only --no-metrics exists, and it takes all three away.
const metricsChannel = "metrics"

// newViewCmd is the explicit render verb. It behaves exactly like the bare
// command but owns the render flags' own help page and is listed in
// `agentry --help`.
func newViewCmd(noColor *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "view [session-id]",
		Short:             "render a session (explicit form of the bare command)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeSessionIDs,
		Example: "  agentry view <uuid>\n" +
			"  agentry view --level full <uuid>\n" +
			"  agentry view --turn 11 --level full\n" +
			"  agentry view --tools --no-thinking <uuid>\n" +
			"  agentry view --from sdk",
		RunE: func(cmd *cobra.Command, args []string) error {
			return renderSession(cmd, args, noColor, false)
		},
	}
	addRenderFlags(cmd)
	addFormatFlag(cmd)
	addFromFlag(cmd)
	return cmd
}

// addRenderFlags installs the verbosity preset and per-channel overrides. They
// live on both root and view because both render; a flag is read from whichever
// command was invoked. --format is added separately (addFormatFlag) because it
// is shared with list, and the root carries both flag sets — adding it here too
// would double-register it there.
func addRenderFlags(cmd *cobra.Command) {
	cmd.Flags().String("level", "minimal", "verbosity: minimal|standard|detailed|full")
	cmd.Flags().String("turn", "", "render one turn or an inclusive span: N or N-M")
	for _, ch := range channelNames {
		cmd.Flags().Bool(ch, false, "show "+ch)
		cmd.Flags().Bool("no-"+ch, false, "hide "+ch)
	}
	cmd.Flags().Bool("no-"+metricsChannel, false, "hide the footer's tool, cost and day tables")
	// Complete the enum flag to its allowed values instead of filenames.
	_ = cmd.RegisterFlagCompletionFunc("level", fixedComp(levelNames))
	// --turn takes a number, which nothing can suggest. Say so explicitly, or the
	// shell falls back to offering filenames where a turn number goes.
	_ = cmd.RegisterFlagCompletionFunc("turn", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
}

// addFormatFlag installs --format, shared by the render path and list. It is an
// output-form switch, not a verbosity knob, so it is left out of the render-flag
// help group (isRenderFlag) — like --no-color, it lists under plain Flags. Every
// command that emits output registers it once (the root, which carries both the
// render and list flag sets, must not double-register it).
func addFormatFlag(cmd *cobra.Command) {
	cmd.Flags().String("format", defaultFormat, formatHelp())
	_ = cmd.RegisterFlagCompletionFunc("format", fixedComp(formatNames))
}

// parseTurns reads --turn into the span of turns to render, nil when the flag
// was not given. The syntax is the one the render prints back — "11" or "11-14",
// with a plain hyphen — so a number copied out of a rendered session or a search
// hit goes straight back in.
//
// Validation here is what the value can be wrong about on its own: turns are
// 1-based, so zero and negatives are mistakes with no reading, and a span whose
// end precedes its start is empty by construction rather than by selection.
// Whether the turns exist is a question about the session and is answered once it
// is loaded — see clampTurns.
func parseTurns(cmd *cobra.Command) (*model.TurnRange, error) {
	raw, _ := cmd.Flags().GetString("turn")
	if !cmd.Flags().Changed("turn") || raw == "" {
		return nil, nil
	}
	from, to, err := splitTurnSpan(raw)
	if err != nil {
		return nil, err
	}
	if from < 1 {
		return nil, usageErr("invalid --turn %q: turns are numbered from 1", raw)
	}
	if to < from {
		return nil, usageErr("invalid --turn %q: the span ends before it starts", raw)
	}
	return &model.TurnRange{From: from, To: to}, nil
}

// splitTurnSpan reads "N" as the single turn N and "N-M" as the inclusive span.
// A malformed value names itself in the error, since cobra reports only that a
// flag was bad and the caller cannot see which half of a span it read.
func splitTurnSpan(raw string) (from, to int, err error) {
	one := func(s string) (int, error) {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil {
			return 0, usageErr("invalid --turn %q: want a turn number (11) or a span (11-14)", raw)
		}
		return n, nil
	}
	head, tail, isSpan := strings.Cut(raw, "-")
	if !isSpan {
		n, err := one(raw)
		return n, n, err
	}
	if from, err = one(head); err != nil {
		return 0, 0, err
	}
	to, err = one(tail)
	return from, to, err
}

// clampTurns settles a span against the session it will select from. The start
// must name a turn that exists, because a caller asking for turn 99 of a
// twelve-turn session has made a mistake and an empty render would not say so —
// the error names the count, which is the one thing they cannot work out from
// nothing being printed. The end is a ceiling rather than an assertion, so
// "11-99" reads as "from 11 to as far as it goes" and renders to the last turn.
func clampTurns(r *model.TurnRange, numTurns int) error {
	if r.From > numTurns {
		noun := "turns"
		if numTurns == 1 {
			noun = "turn"
		}
		return usageErr("--turn %d: the session has %d %s", r.From, numTurns, noun)
	}
	if r.To > numTurns {
		r.To = numTurns
	}
	return nil
}

// renderFlagNames are the flags that shape a rendered transcript: the verbosity
// preset, the turn selector, and every channel toggle in both forms. --no-color
// is deliberately absent, being global rather than the render path's.
//
// One list with two readers, because they have to agree: the grouped help layout
// shows these under their own heading, and the bare command rejects them when it
// is listing rather than rendering. A flag in one reader and not the other is
// either help that lies about a flag's scope or a flag silently ignored.
func renderFlagNames() []string {
	names := []string{"level", "turn", "no-" + metricsChannel}
	for _, ch := range channelNames {
		names = append(names, ch, "no-"+ch)
	}
	return names
}

// isRenderFlag reports whether a flag name belongs to the render group. Drives
// the grouped help layout.
func isRenderFlag(name string) bool {
	return slices.Contains(renderFlagNames(), name)
}

// renderFlagsPassed names the render flags present on the command line, in the
// order renderFlagNames declares them so one command line always produces one
// message. Presence is what matters, so each is read via Changed — a settings
// file plants these as defaults, which is not the caller asking for them.
func renderFlagsPassed(cmd *cobra.Command) []string {
	var passed []string
	for _, name := range renderFlagNames() {
		if cmd.Flags().Changed(name) {
			passed = append(passed, "--"+name)
		}
	}
	return passed
}

// rejectRenderFlags is the error for a render flag passed to the bare command
// when it is listing. The bare command carries both flag sets because it lists
// or renders depending on its argument, so these are registered there and did
// nothing at all when no id followed — a caller who asked for full detail got a
// listing and no sign their flag had been dropped. `list` itself never had them
// registered, which is why the silence was the bare form's alone.
//
// Every offending flag is named rather than the first, so a caller who passed
// three fixes three at once instead of running three times.
func rejectRenderFlags(names []string) error {
	if len(names) == 1 {
		return usageErr("%s belongs to the render path: pass a session id, or use `agentry view %s`", names[0], names[0])
	}
	list := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	return usageErr("%s belong to the render path: pass a session id, or use `agentry view` with them", list)
}

// channelsFromFlags resolves the --level preset and applies any per-channel
// overrides present on cmd.
func channelsFromFlags(cmd *cobra.Command) (render.Channels, error) {
	level, _ := cmd.Flags().GetString("level")
	channels, ok := levels[level]
	if !ok {
		if g := nearest(level, levelNames); g != "" {
			return channels, usageErr("invalid --level %q — did you mean %q?", level, g)
		}
		return channels, usageErr("invalid --level %q (want minimal|standard|detailed|full)", level)
	}
	applyChannel(&channels.Thinking, cmd, "thinking")
	applyChannel(&channels.Tools, cmd, "tools")
	applyChannel(&channels.ToolResults, cmd, "tool-results")
	applyChannel(&channels.Subagents, cmd, "subagents")
	// On by default and off only when asked, which is why it is set here rather
	// than in the levels map: the footer's aggregates are session-level facts, not
	// transcript detail, so no verbosity level governs them.
	channels.Metrics = true
	applyChannel(&channels.Metrics, cmd, metricsChannel)
	return channels, nil
}

// applyChannel overrides a channel default with explicit --name / --no-name
// flags. --no-name wins if both are somehow present. Presence is what matters,
// so it is read via Changed, not the flag's value.
func applyChannel(dst *bool, cmd *cobra.Command, name string) {
	if cmd.Flags().Changed(name) {
		*dst = true
	}
	if cmd.Flags().Changed("no-" + name) {
		*dst = false
	}
}
