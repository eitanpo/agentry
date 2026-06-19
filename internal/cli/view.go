package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/render"
)

// channelNames are the per-channel overrides, each exposed as --name / --no-name.
// Single source for both flag registration and render-flag-group detection.
var channelNames = []string{"thinking", "tools", "tool-results", "subagents", "metrics"}

// newViewCmd is the explicit render verb. It behaves exactly like the bare
// command but owns the render flags' own help page and is listed in
// `agentry --help`.
func newViewCmd(noColor *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view [session-id]",
		Short: "render a session (explicit form of the bare command)",
		Args:  cobra.MaximumNArgs(1),
		Example: "  agentry view <uuid>\n" +
			"  agentry view --level full <uuid>\n" +
			"  agentry view --tools --no-thinking <uuid>",
		RunE: func(cmd *cobra.Command, args []string) error {
			return renderSession(cmd, args, noColor, false)
		},
	}
	addRenderFlags(cmd)
	return cmd
}

// addRenderFlags installs the verbosity preset and per-channel overrides. They
// live on both root and view because both render; a flag is read from whichever
// command was invoked.
func addRenderFlags(cmd *cobra.Command) {
	cmd.Flags().String("level", "minimal", "verbosity: minimal|standard|detailed|full")
	for _, ch := range channelNames {
		cmd.Flags().Bool(ch, false, "show "+ch)
		cmd.Flags().Bool("no-"+ch, false, "hide "+ch)
	}
}

// isRenderFlag reports whether a flag name belongs to the render group
// (--level and the channel toggles, including their --no- forms). Drives the
// grouped help layout; --no-color is deliberately excluded (it is global).
func isRenderFlag(name string) bool {
	if name == "level" {
		return true
	}
	bare := strings.TrimPrefix(name, "no-")
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
	applyChannel(&channels.Metrics, cmd, "metrics")
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
