package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/parse"
	"github.com/eitanpo/agentry/internal/render"
)

// newRootCmd assembles the command tree. The bare root lists (no argument) or
// renders (a full session id); it also parents the verbs. Because it does both,
// it carries both flag sets — the list selectors and the render toggles. noColor
// is shared by reference into the verbs so the persistent flag has one backing
// value.
func newRootCmd(version string) *cobra.Command {
	var noColor bool

	root := &cobra.Command{
		Use:   "agentry [session-id]",
		Short: "render Claude Code session logs (bare command lists them)",
		Long: "agentry " + version + " — render a Claude Code session log to the terminal\n\n" +
			"With no argument it lists the current project's sessions; pass a full\n" +
			"session id to render one, or `agentry view` to render the most recent.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeSessionIDs,
		Version:           version,
		SilenceErrors:     true, // Execute prints in agentry's voice
		SilenceUsage:      true, // a usage error must not dump full help
		Example: "  agentry                      list this project's sessions\n" +
			"  agentry --since today        list sessions active today\n" +
			"  agentry <uuid>               render a specific session\n" +
			"  agentry view                 render the most recent session\n" +
			"  agentry view --level full    render the most recent in full detail\n" +
			"  agentry list --since 7d      list sessions from the last 7 days",
		RunE: func(cmd *cobra.Command, args []string) error {
			// No id lists; a full id renders. renderSession handles the
			// verb-vs-id did-you-mean for a non-id first token.
			if len(args) == 0 {
				return runList(cmd, &noColor)
			}
			return renderSession(cmd, args, &noColor, true)
		},
	}
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color (also honors NO_COLOR)")
	// Predefine --version (no -v shorthand) so Cobra's auto flag — which would
	// bind -v — is skipped; -v conventionally means verbose, not version.
	root.Flags().Bool("version", false, "print version and exit")
	root.SetVersionTemplate("agentry {{.Version}}\n")
	root.SetFlagErrorFunc(flagErrorFunc)
	addRenderFlags(root)
	addListFlags(root)
	addFormatFlag(root)

	cobra.AddTemplateFunc("renderFlagUsages", renderFlagUsages)
	cobra.AddTemplateFunc("otherLocalFlagUsages", otherLocalFlagUsages)
	root.SetUsageTemplate(usageTemplate)

	root.AddCommand(newViewCmd(&noColor))
	root.AddCommand(newListCmd(&noColor))
	return root
}

// renderSession resolves the session and renders it. isRoot distinguishes the
// bare command (where a non-id first token was meant as a verb and gets a
// did-you-mean) from explicit `view` (where the token is always an id).
func renderSession(cmd *cobra.Command, args []string, noColor *bool, isRoot bool) error {
	channels, err := channelsFromFlags(cmd)
	if err != nil {
		return err
	}
	format, err := parseFormat(cmd)
	if err != nil {
		return err
	}

	var id string
	if len(args) == 1 {
		id = args[0]
		if isRoot && !looksLikeID(id) {
			if g := nearest(id, verbNames); g != "" {
				return usageErr("unknown command %q — did you mean %q?", id, g)
			}
			return usageErr("unknown command %q (run \"agentry --help\")", id)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return noInputErr(err)
	}
	path, err := locate.Session(cwd, id)
	if err != nil {
		return noInputErr(err)
	}
	sess, err := parse.Load(path)
	if err != nil {
		return noInputErr(err)
	}

	if format == "json" {
		if err := render.SessionJSON(os.Stdout, sess); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}

	color, width := terminal(*noColor)
	if err := render.Session(os.Stdout, sess, render.Options{
		Width: width, Color: color, Channels: channels,
	}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}

// completeSessionIDs is the shell-completion handler for the render path's
// positional session id. It lists the current project's sessions and offers
// each id annotated with its title (as `agentry list` would show it). Cobra's
// hidden __complete callback runs it on every Tab, so it reflects the sessions
// present at that moment; NoFileComp keeps an id from decaying into filename
// completion. Errors resolve to "no suggestions", never a crash mid-Tab.
func completeSessionIDs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 { // the render path takes at most one id
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	paths, err := locate.Sessions(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, p := range paths {
		s, err := parse.Summarize(p)
		if err != nil || !strings.HasPrefix(s.ID, toComplete) {
			continue
		}
		out = append(out, s.ID+"\t"+compTitle(s.Title))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// compTitle flattens a session title to a single short line fit for a
// completion description — a tab or newline would corrupt the shell's menu.
func compTitle(title string) string {
	title = strings.TrimSpace(strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(title))
	if r := []rune(title); len(r) > 50 {
		title = string(r[:47]) + "..."
	}
	if title == "" {
		return "(untitled)"
	}
	return title
}

// looksLikeID reports whether tok has the shape of a session id — hex digits
// and hyphens only. Verbs are English words and so always fail this, which is
// what makes first-token routing (verb vs. id) unambiguous.
func looksLikeID(tok string) bool {
	if tok == "" {
		return false
	}
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
