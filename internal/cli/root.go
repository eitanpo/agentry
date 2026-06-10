package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/parse"
	"github.com/eitanpo/agentry/internal/render"
)

// newRootCmd assembles the command tree. The root both renders (bare agentry /
// agentry <id>) and parents the verbs. noColor is shared by reference into the
// verbs so the persistent flag has one backing value.
func newRootCmd(version string) *cobra.Command {
	var noColor bool

	root := &cobra.Command{
		Use:   "agentry [session-id]",
		Short: "render a Claude Code session log to the terminal",
		Long: "agentry " + version + " — render a Claude Code session log to the terminal\n\n" +
			"With no argument it renders the current project's most recent session;\n" +
			"with a full session id it renders that one. Use `agentry list` to find a session.",
		Args:          cobra.MaximumNArgs(1),
		Version:       version,
		SilenceErrors: true, // Execute prints in agentry's voice
		SilenceUsage:  true, // a usage error must not dump full help
		Example: "  agentry                      render the most recent session\n" +
			"  agentry <uuid>               render a specific session\n" +
			"  agentry --level full         render the most recent in full detail\n" +
			"  agentry view --tools <uuid>  render one session, showing tool calls\n" +
			"  agentry list                 list sessions to find an id\n" +
			"  agentry list --since 7d      list sessions from the last 7 days",
		RunE: func(cmd *cobra.Command, args []string) error {
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

	color, width := terminal(*noColor)
	if err := render.Session(os.Stdout, sess, render.Options{
		Width: width, Color: color, Channels: channels,
	}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
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
