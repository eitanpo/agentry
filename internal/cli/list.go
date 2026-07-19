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
	addListFlags(cmd)
	addFormatFlag(cmd)
	return cmd
}

// addListFlags installs the listing selectors and detail toggles. They live on
// both the `list` verb and the root, because bare `agentry` defaults to listing;
// a flag is read from whichever command was invoked. --format is added
// separately (addFormatFlag) since it is shared with the render path.
func addListFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 10, "cap to N most-recent sessions (0 = no cap)")
	cmd.Flags().String("since", "", "only sessions active at or after WHEN (today|yesterday, Nh|Nd|Nw, YYYY-MM-DD)")
	cmd.Flags().String("until", "", "only sessions active at or before WHEN")
	cmd.Flags().String("include", "", "add detail channels (comma-separated): prompts, tools, all")
	cmd.Flags().String("used-tool", "", "only sessions that used this tool, by name (Bash, Skill, Agent, WebFetch, …)")
	cmd.Flags().String("used-skill", "", "only sessions that invoked this skill")
	cmd.Flags().String("used-agent", "", "only sessions that spawned this subagent type")
	cmd.Flags().String("used-command", "", "only sessions that ran a Bash command matching this text")
	cmd.Flags().String("used", "", "only sessions that used this as a skill, agent, or command")
}

// usedFlags are the --used* filter flags; any of them, like a time filter,
// lifts the default --limit so a filtered listing is not silently capped.
var usedFlags = []string{"used-tool", "used-skill", "used-agent", "used-command", "used"}

func runList(cmd *cobra.Command, noColor *bool) error {
	limit, _ := cmd.Flags().GetInt("limit")
	since, _ := cmd.Flags().GetString("since")
	until, _ := cmd.Flags().GetString("until")
	include, _ := cmd.Flags().GetString("include")

	var showPrompts, showTools bool
	for _, tok := range strings.Split(include, ",") {
		switch tok = strings.TrimSpace(tok); tok {
		case "": // empty entries (e.g. unset flag) contribute nothing
		case "prompts":
			showPrompts = true
		case "tools":
			showTools = true
		case "all":
			showPrompts, showTools = true, true
		default:
			if g := nearest(tok, includeNames); g != "" {
				return usageErr("--include: unknown channel %q — did you mean %q?", tok, g)
			}
			return usageErr("--include: unknown channel %q (want: prompts, tools, all)", tok)
		}
	}

	// Validate --format before touching the filesystem, so a bad value errors
	// (with a suggestion) the same way a bad --include channel does.
	format, err := parseFormat(cmd)
	if err != nil {
		return err
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
	// A time or --used* filter without an explicit --limit lifts the default
	// cap, so a filtered listing shows every match, not just ten.
	filtering := cmd.Flags().Changed("since") || cmd.Flags().Changed("until")
	for _, f := range usedFlags {
		filtering = filtering || cmd.Flags().Changed(f)
	}
	if filtering && !cmd.Flags().Changed("limit") {
		limit = 0
	}

	get := func(name string) string { v, _ := cmd.Flags().GetString(name); return v }
	filters := list.Filters{
		Tool:    get("used-tool"),
		Skill:   get("used-skill"),
		Agent:   get("used-agent"),
		Command: get("used-command"),
		Any:     get("used"),
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

	selected := list.Select(list.FilterByTools(sums, filters), sinceT, untilT, limit)
	if format == "json" {
		if err := list.RenderJSON(os.Stdout, selected); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}
	color, width := terminal(*noColor)
	if err := list.Render(os.Stdout, selected, list.Options{
		Width: width, Color: color, Prompts: showPrompts, Tools: showTools,
	}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}
