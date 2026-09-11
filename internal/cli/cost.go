package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/cost"
	"github.com/eitanpo/agentry/internal/list"
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
		Short: "what the sessions cost, priced from their tokens",
		Args:  cobra.NoArgs,
		Example: "  agentry cost\n" +
			"  agentry cost --by day\n" +
			"  agentry cost --since 30d --by week\n" +
			"  agentry cost --all-projects --by model\n" +
			"  agentry cost --by session",
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

	var sums []model.Summary
	for _, p := range paths {
		s, err := parse.Summarize(p)
		if err != nil {
			continue // skip a session that won't parse, as the listing does
		}
		sums = append(sums, s)
	}
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
