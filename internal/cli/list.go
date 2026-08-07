package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/entrypoint"
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
	cmd.Flags().String("from", "", "where the session ran: cli, app, sdk, all (default: everything but sdk)")
	// Complete the enum flag to its allowed values instead of filenames.
	_ = cmd.RegisterFlagCompletionFunc("from", fixedComp(entrypoint.Names))
	cmd.Flags().Bool("all-projects", false, "list every project's sessions, not just this directory's")
	cmd.Flags().String("project", "", "list PATH's sessions instead of this directory's, including anything nested under it")
}

// sessionPaths resolves which sessions the listing covers: this directory's by
// default, one named subtree's under --project, or every project's under
// --all-projects. The two scope flags are mutually exclusive — silently
// preferring one would make the other look broken rather than rejected.
func sessionPaths(cmd *cobra.Command) ([]string, error) {
	allProjects, _ := cmd.Flags().GetBool("all-projects")
	project, _ := cmd.Flags().GetString("project")
	if allProjects && project != "" {
		return nil, usageErr("--all-projects and --project are mutually exclusive: --all-projects covers every project, so naming one narrows nothing")
	}
	switch {
	case allProjects:
		return locate.SessionsAll()
	case project != "":
		return locate.SessionsUnder(project)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return locate.Sessions(cwd)
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
	from, _ := cmd.Flags().GetString("from")
	if from != "" && !slices.Contains(entrypoint.Names, from) {
		if g := nearest(from, entrypoint.Names); g != "" {
			return usageErr("--from: unknown source %q — did you mean %q?", from, g)
		}
		return usageErr("--from: unknown source %q (want: %s)", from, strings.Join(entrypoint.Names, ", "))
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

	paths, err := sessionPaths(cmd)
	if err != nil {
		// A usage error is the caller's mistake, not an empty result: it must not
		// be dressed up as a well-formed empty listing.
		var ue *exitError
		if errors.As(err, &ue) && ue.code == exUsage {
			return err
		}
		// Under --format json the output contract is an array, and these
		// failures — no project for the directory or --project path, or no
		// project holding a session — are the only ones that would leave stdout
		// empty instead. Emitting [] keeps one shape for every outcome so a
		// caller sweeping directories can pipe into jq without guarding. The
		// error still goes to stderr with its exit code: an empty array is not a
		// claim of success.
		if format == "json" {
			_ = list.RenderJSON(os.Stdout, nil)
		}
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

	// The entrypoint filter runs before the rest so --limit counts sessions the
	// caller will actually see: capping first and filtering after would return
	// fewer than N rows and give no hint why.
	visible := list.FilterByFrom(sums, from)
	selected := list.Select(list.FilterByTools(visible, filters), sinceT, untilT, limit)
	// A default that empties the listing must say so. Hidden non-interactive
	// sessions are the one exclusion the caller did not ask for, so without this
	// an empty result is indistinguishable from a project holding nothing.
	// Written to the command's error stream, not os.Stderr directly, so it goes
	// wherever the caller routed diagnostics — the same stream errors use.
	if from == "" && len(visible) == 0 && len(sums) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "agentry: %d headless session(s) hidden — pass --from all to include them\n", len(sums))
	}
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
