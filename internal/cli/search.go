package cli

import (
	"errors"
	"os"
	"regexp/syntax"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/parse"
	"github.com/eitanpo/agentry/internal/render"
	"github.com/eitanpo/agentry/internal/search"
)

// newSearchCmd is the within-session search. The listing's filters choose which
// session to open; this answers the question that follows — where inside it a
// passage sits. Without it the only way to reach a passage was to render the
// session to a file and grep that, which costs a full render and hands back line
// numbers that belong to one flag combination.
//
// It carries no render flags. A search bounded by --level would report that a
// passage is absent when the level merely hid it, which is the same reason
// --format json ignores them.
func newSearchCmd(noColor *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <pattern> [session-id]",
		Short: "find where inside a session a pattern appears",
		Long: "Find where inside one session a pattern appears.\n\n" +
			"PATTERN is a regular expression and is matched without regard to case.\n" +
			"Write (?-i) at its start to make case matter, or pass -F to search for the\n" +
			"pattern as text rather than as an expression.\n\n" +
			"Patterns are compiled by Go's regexp package, whose syntax is RE2. Look-around\n" +
			"((?=…), (?<=…)) and backreferences (\\1) are not part of it and are reported as a\n" +
			"parse error; no flag here makes them available.",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeSearchArgs,
		Example: "  agentry search \"unplaced tokens\"\n" +
			"  agentry search \"unplaced tokens\" <uuid>\n" +
			"  agentry search \"tally helper\" --from sdk\n" +
			"  agentry search \"tally helper\" --format json",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args, noColor)
		},
	}
	addFormatFlag(cmd)
	addFromFlag(cmd)
	addFixedStringsFlag(cmd)
	return cmd
}

// patternSpansLines reports whether the pattern needs a line break to match. A
// literal pattern is compared byte for byte, so only a real break counts; an
// expression is parsed, which is what catches the \n escape a caller is far more
// likely to type than the character itself.
func patternSpansLines(pattern string, literal bool) bool {
	if literal {
		return strings.Contains(pattern, "\n")
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		// A malformed pattern is the compiler's to report, and its message names
		// the pattern; answering here would replace that message with this one.
		return false
	}
	return holdsNewline(parsed)
}

// holdsNewline reports whether any literal in the parsed pattern is a newline.
// One branch of an alternation is enough: that branch can match nothing here,
// and a caller who wrote it meant it to match something.
func holdsNewline(parsed *syntax.Regexp) bool {
	if parsed.Op == syntax.OpLiteral && slices.Contains(parsed.Rune, '\n') {
		return true
	}
	for _, sub := range parsed.Sub {
		if holdsNewline(sub) {
			return true
		}
	}
	return false
}

// completeSearchArgs completes the session id at the second position only. The
// first is the caller's pattern, which nothing can suggest — offering session
// ids there would put a uuid where a pattern goes.
func completeSearchArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeSessionIDs(cmd, args, toComplete)
}

// runSearch resolves the session the same way the render path does, searches the
// whole parsed model, and writes the hits.
//
// A run that matched nothing prints nothing and exits zero, which is the rule a
// listing whose filters excluded every session already follows. That differs
// from grep, whose exit code reports whether it matched; agentry's own contract
// wins, and a caller testing for hits tests whether the output was empty.
func runSearch(cmd *cobra.Command, args []string, noColor *bool) error {
	format, err := parseFormat(cmd)
	if err != nil {
		return err
	}
	from, err := parseFrom(cmd)
	if err != nil {
		return err
	}
	literal := literalPattern(cmd)
	re, err := compilePattern(args[0], literal)
	if err != nil {
		return err
	}
	// A hit is one line, so a pattern needing a break between two matches nothing
	// and the empty result would read as the passage being absent from the
	// session — the one answer this verb must never give wrongly. There is no
	// multiline mode to offer, so the error points at the line to search for.

	if patternSpansLines(args[0], literal) {
		return usageErr("the pattern needs a line break to match, and a hit is one line: search for one of its lines instead")
	}

	var id string
	if len(args) == 2 {
		id = args[1]
		// --from chooses among sessions; an id has already chosen one. Accepting
		// both and ignoring the flag would leave a caller believing it applied —
		// the rule the render path follows for the same pair.
		if cmd.Flags().Changed("from") {
			return usageErr("--from cannot be combined with a session id: %q already names the session to search", id)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return noInputErr(err)
	}
	var path string
	if id != "" {
		path, err = locate.Session(cwd, id)
	} else {
		path, err = mostRecent(cmd, cwd, from)
	}
	if err != nil {
		var amb *locate.AmbiguousIDError
		if errors.As(err, &amb) {
			return usageErr("%v:\n  %s", amb, strings.Join(amb.IDs, "\n  "))
		}
		return noInputErr(err)
	}
	sess, err := parse.Load(path)
	if err != nil {
		return noInputErr(err)
	}

	hits := search.In(sess, re)
	out := cmd.OutOrStdout()
	switch format {
	case "json":
		err = render.HitsJSON(out, hits)
	case "jsonl":
		err = render.HitsJSONL(out, hits, sess.Meta.ID)
	default:
		color, _ := terminal(*noColor)
		err = render.Hits(out, hits, re, color)
	}
	if err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}
