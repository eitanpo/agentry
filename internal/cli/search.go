package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/list"
	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/model"
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
		Use:   "search [turn|session] <pattern> [session-id]",
		Short: "find where a pattern appears, in one session or across many",
		Long: "Find where a pattern appears, in one session or across many.\n\n" +
			"With no unit, searches the session `view` would render, or the one whose id\n" +
			"you name. `search turn` reports matching turns across the sessions the\n" +
			"selectors choose; `search session` reports those sessions and how many of\n" +
			"their turns matched. A first positional naming a unit is that unit, in the\n" +
			"singular or the plural, so search for one of those four words with\n" +
			"`agentry search -- turn`.\n\n" +
			"PATTERN is a regular expression. Case comes from the pattern itself: all\n" +
			"lower case matches any casing, and one upper-case letter makes case matter.\n" +
			"Write (?i) or (?-i) at its start to override either way, or pass -F to search\n" +
			"for the pattern as text rather than as an expression.\n\n" +
			"Patterns are compiled by Go's regexp package, whose syntax is RE2. Look-around\n" +
			"((?=…), (?<=…)) and backreferences (\\1) are not part of it and are reported as a\n" +
			"parse error; no flag here makes them available.",
		Args:              cobra.RangeArgs(1, 3),
		ValidArgsFunction: completeSearchArgs,
		Example: "  agentry search \"unplaced tokens\"\n" +
			"  agentry search \"unplaced tokens\" <uuid>\n" +
			"  agentry search turn \"unplaced tokens\" --since 7d\n" +
			"  agentry search session \"unplaced tokens\" --all-projects\n" +
			"  agentry search turn \"a phrase\" --format jsonl",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args, noColor)
		},
	}
	addFormatFlag(cmd)
	// The listing's own selectors — which is also where -F comes from, the two
	// verbs sharing one pattern flag as they share one pattern compiler. From the
	// same table the listing registers, so `--since`
	// and every `--used-*` mean here what they mean there. No cap by default:
	// see addSelectionFlags.
	addSelectionFlags(cmd, "all")
	return cmd
}

// searchNouns are the units a search can report, keyed by every word that names
// one and valued by the unit itself. A first positional naming one is that noun
// rather than a pattern — the rule agentry already applies a level up, where a
// first token naming a verb is the verb and not a session id.
//
// The plural names the same unit: "search sessions" is how the question is
// spoken, and a caller typing it means the unit. Read as a pattern, it takes the
// word after it as a session id, so the run fails over a session nobody named
// and says nothing about the noun it nearly was.
var searchNouns = map[string]string{
	"turn":     "turn",
	"turns":    "turn",
	"session":  "session",
	"sessions": "session",
}

// searchArgs is what the positionals said: which unit to report, the pattern,
// and the one session to search where the caller named one.
type searchArgs struct {
	noun    string
	pattern string
	id      string
}

// parseSearchArgs splits the positionals. A noun is read only from the first
// one, and only when the caller did not put it behind `--`: that is the
// end-of-options form, the one place a positional is guaranteed to be read as
// itself, and it is how a caller searches for the word "turn".
//
// A noun with no pattern is a usage error naming what is missing rather than a
// guess between two valid readings, since a wrong guess returns a plausible
// answer to a question nobody asked.
func parseSearchArgs(cmd *cobra.Command, args []string) (searchArgs, error) {
	literalFirst := cmd.ArgsLenAtDash() == 0
	if noun, named := searchNouns[args[0]]; named && !literalFirst {
		if len(args) < 2 {
			return searchArgs{}, usageErr("agentry search %s: no pattern given — write `agentry search %s <pattern>`, or `agentry search -- %s` to search for that word", args[0], args[0], args[0])
		}
		out := searchArgs{noun: noun, pattern: args[1]}
		if len(args) == 3 {
			out.id = args[2]
		}
		return out, nil
	}
	if len(args) == 3 {
		return searchArgs{}, usageErr("agentry search takes a pattern and at most one session id; %q is a third", args[2])
	}
	out := searchArgs{pattern: args[0]}
	if len(args) == 2 {
		out.id = args[1]
	}
	return out, nil
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
	spec, err := parseSearchArgs(cmd, args)
	if err != nil {
		return err
	}
	literal := literalPattern(cmd)
	re, err := compilePattern(spec.pattern, literal)
	if err != nil {
		return err
	}
	// A hit is one line, so a pattern needing a break between two matches nothing
	// and the empty result would read as the passage being absent from the
	// session — the one answer this verb must never give wrongly. There is no
	// multiline mode to offer, so the error points at the line to search for.
	if patternSpansLines(spec.pattern, literal) {
		return usageErr("the pattern needs a line break to match, and a hit is one line: search for one of its lines instead")
	}

	// A noun with no id searches every session the selectors choose; anything else
	// searches exactly one, the noun then only choosing what to report about it.
	if spec.noun != "" && spec.id == "" {
		return searchSessions(cmd, spec.noun, re, format, noColor)
	}
	return searchOne(cmd, spec, re, from, format, noColor)
}

// searchOne searches the single session the caller named, or the one `view` with
// no id would render — the question a caller has while reading a session, which
// is why it is the bare form.
func searchOne(cmd *cobra.Command, spec searchArgs, re *regexp.Regexp, from, format string, noColor *bool) error {
	if spec.id != "" && cmd.Flags().Changed("from") {
		// --from chooses among sessions; an id has already chosen one. Accepting
		// both and ignoring the flag would leave a caller believing it applied —
		// the rule the render path follows for the same pair.
		return usageErr("--from cannot be combined with a session id: %q already names the session to search", spec.id)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return noInputErr(err)
	}
	var path string
	if spec.id != "" {
		path, err = locate.Session(cwd, spec.id)
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
	if spec.noun == "session" {
		// One session, asked about as a session: a row or nothing, which is the
		// same answer the cross-session form gives for a corpus of one.
		var matches []search.Match
		if len(hits) > 0 {
			matches = []search.Match{{
				Session: sess.Meta.ID, Activity: list.Activity(sess.Meta.Start, sess.Meta.End),
				Title: sess.Meta.Title, Matched: search.TurnsIn(hits), Turns: len(sess.Turns),
			}}
		}
		return emitMatches(cmd, matches, format, noColor)
	}
	out := cmd.OutOrStdout()
	var err2 error
	switch format {
	case "json":
		err2 = render.HitsJSON(out, hits)
	case "jsonl":
		err2 = render.HitsJSONL(out, hits, sess.Meta.ID)
	default:
		color, width := terminal(*noColor)
		err2 = render.Hits(out, hits, re, color, width)
	}
	if err2 != nil {
		return &exitError{code: 1, err: err2}
	}
	return nil
}

// searchSessions searches every session the selectors choose. The selection is
// the listing's, in the listing's order: summarize the scope, drop the
// entrypoints --from excludes, apply the filters, then cap. The entrypoint step
// runs first for the reason it does there — capping before filtering returns
// fewer sessions than asked for and gives no hint why.
//
// runList performs the same four steps on the same values; a test holds the two
// verbs to selecting the same sessions for the same flags, since a filter that
// means one thing on a listing and another here would be one flag with two
// behaviors.
func searchSessions(cmd *cobra.Command, noun string, re *regexp.Regexp, format string, noColor *bool) error {
	from, err := parseFrom(cmd)
	if err != nil {
		return err
	}
	sinceT, untilT, err := parseWindow(cmd)
	if err != nil {
		return err
	}
	limit, err := parseLimit(cmd)
	if err != nil {
		return err
	}
	filters, run, changed, err := parseFilters(cmd)
	if err != nil {
		return err
	}
	paths, err := sessionPaths(cmd)
	if err != nil {
		return err
	}
	sums := parse.SummarizeAll(paths)
	selected, _ := list.Select(list.Filter(list.FilterByFrom(sums, from), filters, run, changed), sinceT, untilT, limit)

	// A summary carries no path — a listing row is named by its id — so the
	// scope's own paths are the way back to the file each selected session came
	// from.
	byID := make(map[string]string, len(paths))
	for _, p := range paths {
		byID[strings.TrimSuffix(filepath.Base(p), ".jsonl")] = p
	}
	labels, _ := list.RowLabels(selected)

	groups := searchEach(selected, byID, labels, re)

	if noun == "session" {
		matches := make([]search.Match, 0, len(groups))
		for _, g := range groups {
			matches = append(matches, g.Match)
		}
		return emitMatches(cmd, matches, format, noColor)
	}
	out := cmd.OutOrStdout()
	switch format {
	case "json":
		var hits []search.Hit
		for _, g := range groups {
			hits = append(hits, g.Hits...)
		}
		err = render.HitsJSON(out, hits)
	case "jsonl":
		var hits []search.Hit
		for _, g := range groups {
			hits = append(hits, g.Hits...)
		}
		// No run-wide session: every hit names its own, which is what makes the
		// stream addressable.
		err = render.HitsJSONL(out, hits, "")
	default:
		color, width := terminal(*noColor)
		err = render.Findings(out, groups, re, color, width)
	}
	if err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}

// searchEach searches every selected session, in parallel and in the order the
// selection gave them. Parallel because a full parse is far dearer than the
// summarize the selection ran — sequentially, a sweep of 395 sessions took nine
// seconds where the selection itself takes one, and the flow this verb serves is
// one a caller runs while reading.
//
// The session is dropped as soon as it has been searched, which is why this holds
// hits rather than loading every session first: a corpus of hundreds of full
// sessions does not belong in memory at once to answer a question about lines.
//
// A log that will not parse is skipped rather than fatal, the rule the listing's
// own summarize step already follows: one bad file must not empty a sweep.
func searchEach(selected []model.Summary, byID map[string]string, labels map[string]string, re *regexp.Regexp) []search.Group {
	found := make([]*search.Group, len(selected))
	workers := runtime.NumCPU()
	if workers > len(selected) {
		workers = len(selected)
	}
	if workers < 1 {
		return nil
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sum := selected[i]
				path, ok := byID[sum.ID]
				if !ok {
					continue
				}
				sess, err := parse.Load(path)
				if err != nil {
					continue
				}
				hits := search.In(sess, re)
				if len(hits) == 0 {
					continue
				}
				for h := range hits {
					hits[h].Session = sum.ID
				}
				found[i] = &search.Group{
					Match: search.Match{
						Session: sum.ID, Activity: list.Activity(sum.Start, sum.End),
						Project: labels[sum.Cwd], Title: sum.Title,
						Matched: search.TurnsIn(hits), Turns: sum.NumTurns,
					},
					Hits: hits,
				}
			}
		}()
	}
	for i := range selected {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var groups []search.Group
	for _, g := range found {
		if g != nil {
			groups = append(groups, *g)
		}
	}
	return groups
}

func emitMatches(cmd *cobra.Command, matches []search.Match, format string, noColor *bool) error {
	out := cmd.OutOrStdout()
	var err error
	switch format {
	case "json":
		err = render.MatchesJSON(out, matches)
	case "jsonl":
		err = render.MatchesJSONL(out, matches)
	default:
		color, _ := terminal(*noColor)
		err = render.Matches(out, matches, color)
	}
	if err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}
