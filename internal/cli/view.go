package cli

import (
	"strings"

	"github.com/spf13/cobra"

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
	for _, ch := range channelNames {
		cmd.Flags().Bool(ch, false, "show "+ch)
		cmd.Flags().Bool("no-"+ch, false, "hide "+ch)
	}
	cmd.Flags().Bool("no-"+metricsChannel, false, "hide the footer's tool, cost and day tables")
	// Complete the enum flag to its allowed values instead of filenames.
	_ = cmd.RegisterFlagCompletionFunc("level", fixedComp(levelNames))
}

// addFormatFlag installs --format, shared by the render path and list. It is an
// output-form switch, not a verbosity knob, so it is left out of the render-flag
// help group (isRenderFlag) — like --no-color, it lists under plain Flags. Every
// command that emits output registers it once (the root, which carries both the
// render and list flag sets, must not double-register it).
func addFormatFlag(cmd *cobra.Command) {
	cmd.Flags().String("format", "", formatHelp())
	_ = cmd.RegisterFlagCompletionFunc("format", fixedComp(formatNames))
}

// isRenderFlag reports whether a flag name belongs to the render group
// (--level and the channel toggles, including their --no- forms). Drives the
// grouped help layout; --no-color is deliberately excluded (it is global).
func isRenderFlag(name string) bool {
	if name == "level" {
		return true
	}
	bare := strings.TrimPrefix(name, "no-")
	if bare == metricsChannel {
		return true
	}
	for _, ch := range channelNames {
		if bare == ch {
			return true
		}
	}
	return false
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
