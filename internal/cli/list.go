package cli

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/list"
	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/parse"
)

// newListCmd is the `list` verb: resolve the project's sessions, summarize,
// filter, and print one row per session. Its flags exist only here — render
// flags are structurally absent, not silently ignored.
func newListCmd(noColor *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list this project's sessions instead of rendering one",
		Args:  cobra.NoArgs,
		Example: "  agentry list\n" +
			"  agentry list --limit 25\n" +
			"  agentry list --since today\n" +
			"  agentry list --include prompts",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, noColor)
		},
	}
	cmd.Flags().Int("limit", 10, "cap to N most-recent sessions (0 = no cap)")
	cmd.Flags().String("since", "", "only sessions active at or after WHEN (today|yesterday, Nh|Nd|Nw, YYYY-MM-DD)")
	cmd.Flags().String("until", "", "only sessions active at or before WHEN")
	cmd.Flags().String("include", "", "add detail channels (comma-separated): prompts, all")
	return cmd
}

func runList(cmd *cobra.Command, noColor *bool) error {
	limit, _ := cmd.Flags().GetInt("limit")
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	include, _ := cmd.Flags().GetString("include")

	var showPrompts bool
	for _, tok := range strings.Split(include, ",") {
		switch tok = strings.TrimSpace(tok); tok {
		case "": // empty entries (e.g. unset flag) contribute nothing
		case "prompts", "all":
			showPrompts = true
		default:
			if g := nearest(tok, includeNames); g != "" {
				return usageErr("--include: unknown channel %q — did you mean %q?", tok, g)
			}
			return usageErr("--include: unknown channel %q (want: prompts, all)", tok)
		}
	}

	now := time.Now()
	var sinceT, untilT time.Time
	if since != "" {
		t, err := list.ParseWhen(since, now)
		if err != nil {
			return usageErr("--since: %v", err)
		}
		sinceT = t
	}
	if until != "" {
		t, err := list.ParseWhen(until, now)
		if err != nil {
			return usageErr("--until: %v", err)
		}
		untilT = t
	}
	// A time filter without an explicit --limit lifts the default cap, so
	// "list --since today" shows every session in the window, not just ten.
	if (cmd.Flags().Changed("since") || cmd.Flags().Changed("until")) && !cmd.Flags().Changed("limit") {
		limit = 0
	}

	cwd, err := os.Getwd()
	if err != nil {
		return noInputErr(err)
	}
	paths, err := locate.Sessions(cwd)
	if err != nil {
		return noInputErr(err)
	}

	var sums []model.Summary
	for _, p := range paths {
		s, err := parse.Summarize(p)
		if err != nil {
			continue // skip a session that won't parse, like a malformed line
		}
		sums = append(sums, s)
	}

	color, width := terminal(*noColor)
	if err := list.Render(os.Stdout, list.Select(sums, sinceT, untilT, limit), list.Options{
		Width: width, Color: color, Prompts: showPrompts,
	}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}
