package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/cost"
	"github.com/eitanpo/agentry/internal/entrypoint"
	"github.com/eitanpo/agentry/internal/list"
	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/parse"
)

// newCostCmd is the `cost` verb: price every session's tokens and add the
// dollars up on one axis. It takes the listing's selection and scope flags and
// none of its detail ones — `--include` shapes rows this verb does not print,
// and `--limit` would cap rows the total line still counts.
func newCostCmd(noColor *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "what you are spending: this session, this folder, this machine",
		Long: "agentry cost — price the sessions from their tokens\n\n" +
			"With no flags it answers three scopes at once: the session this directory\n" +
			"was last working in, the directory's whole history, and everything this\n" +
			"machine ran in the last 30 days. --since, --until and --from narrow those\n" +
			"same three rows; --by, --all-projects and --project switch to a\n" +
			"single-scope roll-up on one axis.\n\n" +
			"The figure is computed from the transcript at list prices — an estimate,\n" +
			"not a bill.",
		Args: cobra.NoArgs,
		Example: "  agentry cost                       this session, this folder, this machine\n" +
			"  agentry cost --since 7d            the same three scopes, over a week\n" +
			"  agentry cost --by day              one row per day, this directory\n" +
			"  agentry cost --since 30d --by week\n" +
			"  agentry cost --all-projects --by model\n" +
			"  agentry cost --by session          priciest session first",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCost(cmd, noColor)
		},
	}
	cmd.Flags().String("by", cost.ByTotal, "bucket the dollars by: "+strings.Join(cost.Axes, ", "))
	_ = cmd.RegisterFlagCompletionFunc("by", fixedComp(cost.Axes))
	cmd.Flags().String("since", "", "only spend on or after WHEN (today|yesterday, Nh|Nd|Nw, YYYY-MM-DD)")
	cmd.Flags().String("until", "", "only spend on or before WHEN")
	cmd.Flags().Bool("all-projects", false, "price every project's sessions, not just this directory's")
	cmd.Flags().String("project", "", "price PATH's sessions instead of this directory's, including anything nested under it")
	addFromFlag(cmd)
	addFormatFlag(cmd)
	return cmd
}

// parseBy validates --by, naming the nearest axis on a typo like every other
// enum flag.
func parseBy(cmd *cobra.Command) (string, error) {
	by, _ := cmd.Flags().GetString("by")
	if by == "" {
		return cost.ByTotal, nil
	}
	for _, a := range cost.Axes {
		if by == a {
			return by, nil
		}
	}
	if g := nearest(by, cost.Axes); g != "" {
		return "", usageErr("--by: unknown axis %q — did you mean %q?", by, g)
	}
	return "", usageErr("--by: unknown axis %q (want: %s)", by, strings.Join(cost.Axes, ", "))
}

// costShapers are the flags that replace the three-scope summary rather than
// narrow it. --by asks for a different shape entirely, and the two scope flags
// contradict the summary's own rows, which are scopes.
//
// The window and entrypoint flags are deliberately absent. They filter the same
// question the summary answers, so they move its rows instead of collapsing
// them: a caller asking for the last week wants the three scopes over a week,
// not one number whose line does not even say which scope it covers. --format
// and --no-color are absent for a different reason — neither chooses what is
// counted, only how it is written.
var costShapers = []string{"by", "all-projects", "project"}

// summaryMode reports whether the verb should answer with the three-scope
// summary — the answer to being asked nothing, and to being asked only to narrow
// it.
func summaryMode(cmd *cobra.Command) bool {
	for _, f := range costShapers {
		if cmd.Flags().Changed(f) {
			return false
		}
	}
	return true
}

// machineWindow is how far back the summary's machine row counts. Thirty days is
// the span a spend question is usually asked over, and the row prints the first
// day it covers rather than the span, so a reader is never left converting one
// into the other.
const machineWindow = 30

func runCost(cmd *cobra.Command, noColor *bool) error {
	// Every value is validated before a file is opened, so a typo errors at once
	// rather than after a scan of every project.
	by, err := parseBy(cmd)
	if err != nil {
		return err
	}
	format, err := parseFormat(cmd)
	if err != nil {
		return err
	}
	from, err := parseFrom(cmd)
	if err != nil {
		return err
	}
	since, until, err := parseWindow(cmd)
	if err != nil {
		return err
	}
	if summaryMode(cmd) {
		return runCostSummary(cmd, noColor, format, from, cost.Window{
			Since: since, Until: until,
			MachineSince: time.Now().AddDate(0, 0, -machineWindow),
		})
	}

	paths, err := sessionPaths(cmd)
	if err != nil {
		var ue *exitError
		if errors.As(err, &ue) && ue.code == exUsage {
			return err
		}
		// Under --format json the contract is one object whatever happened, so a
		// directory with no project still writes the zeroed report to stdout and
		// the reason to stderr. The exit code is what separates "nothing matched"
		// from "nothing to look in" — the rule list --format json follows.
		if format == "json" {
			_ = cost.RenderJSON(os.Stdout, cost.Build(nil, by, since, until))
		}
		return noInputErr(err)
	}

	// SummarizeAll, not a loop over Summarize: it skips a session that won't
	// parse on the same rule and keeps the input order, so the selection is
	// unchanged, but it reads the corpus across every core the way the listing and
	// the three-scope summary already do.
	//
	// The two paths reading at the same speed is what keeps them comparable. A
	// session log is appended to while agentry reads it, so a path that takes
	// seconds longer over the same corpus prices entries the faster path never
	// saw: sequentially this verb took 3.2s against a 1.3GB tree the summary
	// crossed in 0.5s, and `agentry cost` against `agentry cost --all-projects
	// --from all --since 30d` reported totals differing by cents — read as an
	// accumulation bug, when the two had simply read the log 2.7s apart.
	sums := parse.SummarizeAll(paths)
	// Headless runs are excluded by the same default the listing applies: a
	// machine using hooks accumulates hundreds of them, and a total that quietly
	// counted them would answer a different question than the listing beside it.
	//
	// Unlike the listing, the exclusion is reported whenever it removed anything
	// rather than only when it emptied the result. A listing that dropped rows
	// still shows the rows it kept; a total that dropped sessions shows one number
	// that is lower than the caller's bill by however much those cost — locally,
	// $426 of $10,244 across 246 headless sessions — with nothing on screen to say
	// so. It goes to the error stream, so a total piped into another program is
	// still the total alone.
	visible := list.FilterByFrom(sums, from)
	if from == "" && len(visible) < len(sums) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agentry: %d headless session(s) not priced — pass --from all to include them\n",
			len(sums)-len(visible))
	}
	report := cost.Build(visible, by, since, until)
	report.Selection = &cost.Selection{
		Scope: scopeName(cmd), Since: dayOf(since), Until: dayOf(until),
	}

	if format == "json" {
		if err := cost.RenderJSON(os.Stdout, report); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}
	color, width := terminal(*noColor)
	if err := cost.Render(os.Stdout, report, cost.Options{Width: width, Color: color}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}

// parseWindow reads --since and --until off the command line. Shared wording
// with the listing through list.ParseWhen, so one WHEN value means the same
// thing on both verbs and a typo is rejected identically.
func parseWindow(cmd *cobra.Command) (since, until time.Time, err error) {
	now := time.Now()
	for _, b := range []struct {
		flag string
		set  *time.Time
	}{{"since", &since}, {"until", &until}} {
		v, _ := cmd.Flags().GetString(b.flag)
		if v == "" {
			continue
		}
		t, perr := list.ParseWhen(v, now)
		if perr != nil {
			return time.Time{}, time.Time{}, usageErr("--%s: %v", b.flag, perr)
		}
		*b.set = t
	}
	return since, until, nil
}

// runCostSummary answers the three scopes at once: the session this directory was
// last working in, the directory's whole history, and the machine's last thirty
// days.
//
// One parse of every session on the machine serves all three, because the three
// scopes nest — the machine's sessions contain the directory's, which contain the
// one. Re-reading the narrow scopes would double the sweep that is already the
// whole cost of the answer.
func runCostSummary(cmd *cobra.Command, noColor *bool, format, from string, w cost.Window) error {
	paths, err := locate.SessionsAll()
	if err != nil {
		// The machine holds no session at all. Under --format json the contract is
		// one object whatever happened, so a summary of nothing still goes to stdout
		// — built rather than zero-valued, so it carries the price table's date like
		// every other summary — and the reason goes to stderr with the exit code.
		if format == "json" {
			_ = cost.RenderOverviewJSON(os.Stdout, cost.BuildOverview(nil, nil, nil, w))
		}
		return noInputErr(err)
	}
	machine := parse.SummarizeAll(paths)
	// Only an explicit --from filters the two wide rows. Their default is every
	// session of every kind, headless included, because they are meant to be the
	// bill and a hook costs real money — so the listing's default exclusion must
	// not reach them uninvited.
	if from != "" {
		machine = list.FilterByFrom(machine, from)
	}

	var folder []model.Summary
	var session *model.Summary
	cwd, err := os.Getwd()
	if err != nil {
		return noInputErr(err)
	}
	// Recency order, so the first session in scope that was not headless is the
	// one `agentry view` would render — the same resolution, from the same rule.
	here, hereErr := locate.SessionsByRecency(cwd)
	if hereErr != nil {
		// A directory with no project has no session and no folder row. Saying so
		// keeps their absence from reading as two scopes that spent nothing.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"agentry: no Claude project for this directory — pricing this machine only\n")
	} else {
		parsed := make(map[string]*model.Summary, len(machine))
		for i := range machine {
			parsed[machine[i].ID] = &machine[i]
		}
		for _, p := range here {
			s, ok := parsed[sessionID(p)]
			if !ok {
				continue // it would not parse, and the sweep already skipped it
			}
			if from != "" && !entrypoint.Matches(from, s.Entrypoint) {
				continue
			}
			folder = append(folder, *s)
			// The session row skips headless runs under the default, the same
			// resolution `agentry view` makes; a named --from picks the newest of
			// the kind that was asked for instead.
			if session == nil && entrypoint.Matches(from, s.Entrypoint) {
				session = s
			}
		}
	}

	o := cost.BuildOverview(session, folder, machine, w)
	if format == "json" {
		if err := cost.RenderOverviewJSON(os.Stdout, o); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}
	color, width := terminal(*noColor)
	if err := cost.RenderOverview(os.Stdout, o, cost.Options{Width: width, Color: color}); err != nil {
		return &exitError{code: 1, err: err}
	}
	return nil
}

// scopeName says which sessions a roll-up counted, in the words the caller chose
// them with. It is what the total line would otherwise leave unsaid.
func scopeName(cmd *cobra.Command) string {
	if all, _ := cmd.Flags().GetBool("all-projects"); all {
		return "every project"
	}
	if p, _ := cmd.Flags().GetString("project"); p != "" {
		return p
	}
	return "this folder"
}

// dayOf is the local calendar day a bound falls on, empty for an unset bound —
// the same reading the roll-up gives its window, so the note and the rows cannot
// describe one bound two ways.
func dayOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

// sessionID is the id a session log's path carries: its file stem, which is how
// the parser names the session it read.
func sessionID(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
