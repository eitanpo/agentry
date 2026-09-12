package cost

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/eitanpo/agentry/internal/model"
)

const million = 1000000

// The fixture spans a Monday and the Tuesday after it, so a week bucket has one
// key that is neither day's own and a month bucket has one key for both.
const (
	monday  = "2026-03-02"
	tuesday = "2026-03-03"
)

// out is a session's tokens as output alone, which prices at the model's output
// rate and nothing else — a million on Sonnet 5 is $10 and on Opus 5 is $25.
func out(day, modelID string, tokens int) model.DailyUsage {
	return model.DailyUsage{Day: day, Model: modelID, Usage: model.Usage{Output: tokens}}
}

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

// fixture is two sessions: one spanning both days on two models, one sitting on
// the second day alone. Only the first carries Claude Code's own record.
func fixture() []model.Summary {
	recorded := 7.5
	return []model.Summary{
		{
			ID: "aaaaaaaa-1111", Title: "First session",
			Start: day(monday), End: day(tuesday), CostUSD: &recorded,
			DailyUsage: []model.DailyUsage{
				out(monday, "claude-sonnet-5", million),
				out(tuesday, "claude-opus-5", million),
			},
		},
		{
			ID: "bbbbbbbb-2222", Title: "Second session",
			Start: day(tuesday), End: day(tuesday),
			DailyUsage: []model.DailyUsage{out(tuesday, "claude-sonnet-5", 2*million)},
		},
	}
}

func nearly(got, want float64) bool { return math.Abs(got-want) < 1e-4 }

// TestBuildByDay pins the bucket a response lands in and the session count a
// bucket reports, which is not the count of rows: one session spans both days
// and is counted in each, while the total counts it once.
func TestBuildByDay(t *testing.T) {
	r := Build(fixture(), ByDay, time.Time{}, time.Time{})
	if len(r.Buckets) != 2 {
		t.Fatalf("Buckets = %d, want 2: %+v", len(r.Buckets), r.Buckets)
	}
	if r.Buckets[0].Key != monday || r.Buckets[1].Key != tuesday {
		t.Errorf("keys = %q, %q, want %q, %q in that order (oldest first)",
			r.Buckets[0].Key, r.Buckets[1].Key, monday, tuesday)
	}
	if got := r.Buckets[0]; got.Sessions != 1 || !nearly(got.CostUSD, 10) {
		t.Errorf("%s = %d session(s) at $%.2f, want 1 at $10.00", monday, got.Sessions, got.CostUSD)
	}
	if got := r.Buckets[1]; got.Sessions != 2 || !nearly(got.CostUSD, 45) {
		t.Errorf("%s = %d session(s) at $%.2f, want 2 at $45.00", tuesday, got.Sessions, got.CostUSD)
	}
	if r.Total.Sessions != 2 {
		t.Errorf("Total.Sessions = %d, want 2; the rows sum to 3 because one session spans both days", r.Total.Sessions)
	}
	if !nearly(r.Total.CostUSD, 55) {
		t.Errorf("Total.CostUSD = %f, want 55", r.Total.CostUSD)
	}
	if r.Total.Usage.Output != 4*million {
		t.Errorf("Total output = %d, want %d", r.Total.Usage.Output, 4*million)
	}
}

// TestBuildByWeekAndMonth pins the two coarser keys: a week is labelled by its
// Monday, a month by its calendar month.
func TestBuildByWeekAndMonth(t *testing.T) {
	for _, c := range []struct{ by, key string }{{ByWeek, monday}, {ByMonth, "2026-03"}} {
		r := Build(fixture(), c.by, time.Time{}, time.Time{})
		if len(r.Buckets) != 1 {
			t.Fatalf("--by %s: Buckets = %+v, want one", c.by, r.Buckets)
		}
		if r.Buckets[0].Key != c.key {
			t.Errorf("--by %s: key = %q, want %q", c.by, r.Buckets[0].Key, c.key)
		}
		if r.Buckets[0].Sessions != 2 || !nearly(r.Buckets[0].CostUSD, 55) {
			t.Errorf("--by %s: %d session(s) at $%.2f, want 2 at $55.00", c.by, r.Buckets[0].Sessions, r.Buckets[0].CostUSD)
		}
	}
}

// TestBuildBySessionAndModelSortByCost pins the order the two size questions are
// asked in — most expensive first — and the title a session key carries, since
// the key itself is an opaque id.
func TestBuildBySessionAndModelSortByCost(t *testing.T) {
	r := Build(fixture(), BySession, time.Time{}, time.Time{})
	if len(r.Buckets) != 2 {
		t.Fatalf("Buckets = %+v, want two", r.Buckets)
	}
	if r.Buckets[0].Key != "aaaaaaaa-1111" || !nearly(r.Buckets[0].CostUSD, 35) {
		t.Errorf("first row = %q at $%.2f, want aaaaaaaa-1111 at $35.00", r.Buckets[0].Key, r.Buckets[0].CostUSD)
	}
	if r.Buckets[0].Label != "First session" {
		t.Errorf("Label = %q, want the session title", r.Buckets[0].Label)
	}
	if r.Buckets[1].Key != "bbbbbbbb-2222" {
		t.Errorf("second row = %q, want the cheaper session", r.Buckets[1].Key)
	}

	r = Build(fixture(), ByModel, time.Time{}, time.Time{})
	if len(r.Buckets) != 2 {
		t.Fatalf("Buckets = %+v, want two", r.Buckets)
	}
	if r.Buckets[0].Key != "claude-sonnet-5" || !nearly(r.Buckets[0].CostUSD, 30) {
		t.Errorf("first row = %q at $%.2f, want claude-sonnet-5 at $30.00", r.Buckets[0].Key, r.Buckets[0].CostUSD)
	}
	if r.Buckets[1].Key != "claude-opus-5" || !nearly(r.Buckets[1].CostUSD, 25) {
		t.Errorf("second row = %q at $%.2f, want claude-opus-5 at $25.00", r.Buckets[1].Key, r.Buckets[1].CostUSD)
	}
}

// TestBuildByTotalPrintsNoRows pins the default: the total is the whole answer,
// so a one-row table that repeats it is not produced.
func TestBuildByTotalPrintsNoRows(t *testing.T) {
	r := Build(fixture(), ByTotal, time.Time{}, time.Time{})
	if r.Buckets != nil {
		t.Errorf("Buckets = %+v, want none", r.Buckets)
	}
	if !nearly(r.Total.CostUSD, 55) || r.Total.Sessions != 2 {
		t.Errorf("Total = %d session(s) at $%.2f, want 2 at $55.00", r.Total.Sessions, r.Total.CostUSD)
	}
}

// TestWindowCutsDaysNotSessions pins what a bound applies to: the days of spend.
// A session straddling the edge keeps the days inside the window and loses the
// rest, where the listing would either keep or drop the whole session.
func TestWindowCutsDaysNotSessions(t *testing.T) {
	r := Build(fixture(), ByDay, day(tuesday), time.Time{})
	if len(r.Buckets) != 1 || r.Buckets[0].Key != tuesday {
		t.Fatalf("Buckets = %+v, want the second day alone", r.Buckets)
	}
	if !nearly(r.Total.CostUSD, 45) {
		t.Errorf("Total.CostUSD = %f, want 45: the first day's $10 is outside the window", r.Total.CostUSD)
	}
	if r.Total.Sessions != 2 {
		t.Errorf("Total.Sessions = %d, want 2; both sessions spent something inside the window", r.Total.Sessions)
	}
}

// TestRecordedCoversWhollyInsideSessionsOnly pins the one comparison that is
// exact. Claude Code's record is one number for a whole session, so a session
// half outside the window contributes none of it.
func TestRecordedCoversWhollyInsideSessionsOnly(t *testing.T) {
	r := Build(fixture(), ByDay, time.Time{}, time.Time{})
	if r.Recorded == nil {
		t.Fatal("Recorded = nil, want the one session carrying a record")
	}
	if r.Recorded.Sessions != 1 || !nearly(r.Recorded.CostUSD, 7.5) {
		t.Errorf("Recorded = %+v, want 1 session at $7.50", *r.Recorded)
	}

	// The recorded session starts on the first day, so a window opening on the
	// second excludes it — and the other session carries no record at all.
	r = Build(fixture(), ByDay, day(tuesday), time.Time{})
	if r.Recorded != nil {
		t.Errorf("Recorded = %+v, want nil: no session lies wholly inside this window with a record", *r.Recorded)
	}
}

// TestUnpricedModelIsNamedNotZeroed pins the answer for a model the table does
// not hold: its tokens count and its dollars do not, and the report says which
// model so a reader is not handed a cheap-looking total.
func TestUnpricedModelIsNamedNotZeroed(t *testing.T) {
	sums := append(fixture(), model.Summary{
		ID: "cccccccc-3333", Title: "Third session",
		Start: day(tuesday), End: day(tuesday),
		DailyUsage: []model.DailyUsage{out(tuesday, "claude-nope-9", million)},
	})
	r := Build(sums, ByTotal, time.Time{}, time.Time{})
	if len(r.UnpricedModels) != 1 || r.UnpricedModels[0].Model != "claude-nope-9" {
		t.Fatalf("UnpricedModels = %+v, want claude-nope-9", r.UnpricedModels)
	}
	if r.UnpricedModels[0].Sessions != 1 || r.UnpricedModels[0].Usage.Output != million {
		t.Errorf("UnpricedModels[0] = %+v, want 1 session and its million tokens", r.UnpricedModels[0])
	}
	if !nearly(r.Total.CostUSD, 55) {
		t.Errorf("Total.CostUSD = %f, want 55: an unpriced model adds no dollars", r.Total.CostUSD)
	}
	if r.Total.Usage.Output != 5*million {
		t.Errorf("Total output = %d, want %d: its tokens are real", r.Total.Usage.Output, 5*million)
	}
}

// TestRenderWritesRowsATotalAndItsNotes pins the text form: a header naming the
// axis, one row per bucket, the total, and the lines saying what the figure is
// and what Claude Code's own record says.
func TestRenderWritesRowsATotalAndItsNotes(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Build(fixture(), ByDay, time.Time{}, time.Time{}), Options{Width: 100}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"Day", "Sessions", "Tokens", "Cost",
		monday, tuesday,
		"$10.00", "$45.00",
		"Total", "$55.00",
		"an estimate, not a bill",
		"Claude Code recorded $7.50 for the 1 session it kept a record for",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestRenderBySessionShowsTitles pins the one axis with a different table: the
// session count has no column, because every row is a session, so the title
// column carries what the total covers.
func TestRenderBySessionShowsTitles(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Build(fixture(), BySession, time.Time{}, time.Time{}), Options{Width: 100}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"Session", "Title", "aaaaaaaa", "First session", "2 sessions", "$35.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "aaaaaaaa-1111") {
		t.Errorf("the full id is printed where 8 characters were meant:\n%s", got)
	}
}

// TestRenderJSONEmitsAnObjectWhenNothingMatched pins the empty case: the shape a
// caller pipes into jq does not change with the result.
func TestRenderJSONEmitsAnObjectWhenNothingMatched(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, Build(nil, ByDay, time.Time{}, time.Time{})); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{`"by": "day"`, `"buckets": []`, `"total"`, `"pricesVerified"`} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %s:\n%s", want, got)
		}
	}
}

// TestBuildOverviewPricesThreeScopes pins the summary bare `agentry cost`
// prints: the session row named by its title, the folder row over its whole
// history, and the machine row over its window, with the window's first day
// carried on that row alone.
func TestBuildOverviewPricesThreeScopes(t *testing.T) {
	sums := fixture()
	o := BuildOverview(&sums[0], sums, sums, Window{MachineSince: day(tuesday)})
	if len(o.Scopes) != 3 {
		t.Fatalf("Scopes = %+v, want three", o.Scopes)
	}
	one, all, machine := o.Scopes[0], o.Scopes[1], o.Scopes[2]
	if one.Scope != ScopeSession || one.Label != "First session" || !nearly(one.CostUSD, 35) {
		t.Errorf("session row = %+v, want the first session at $35.00 under its title", one)
	}
	if one.Since != "" {
		t.Errorf("session row carries a window (%q); it counts the whole session", one.Since)
	}
	if all.Scope != ScopeFolder || all.Sessions != 2 || !nearly(all.CostUSD, 55) {
		t.Errorf("folder row = %+v, want 2 sessions at $55.00 over all time", all)
	}
	if all.Since != "" {
		t.Errorf("folder row carries a window (%q); it counts all time", all.Since)
	}
	// The machine row's window cuts the first day, so it prices less than the
	// folder row over the same two sessions — which is the point of naming the day.
	if machine.Scope != ScopeMachine || machine.Since != tuesday || !nearly(machine.CostUSD, 45) {
		t.Errorf("machine row = %+v, want $45.00 since %s", machine, tuesday)
	}
	if o.PricesVerified == "" {
		t.Error("PricesVerified is empty; a caller cannot tell how old the rates are")
	}
}

// TestBuildOverviewWithoutAProjectPricesTheMachineAlone pins the answer from a
// directory Claude Code has never run in: two rows absent rather than two rows
// of zero, since no session is a different fact from no spend.
func TestBuildOverviewWithoutAProjectPricesTheMachineAlone(t *testing.T) {
	o := BuildOverview(nil, nil, fixture(), Window{})
	if len(o.Scopes) != 1 || o.Scopes[0].Scope != ScopeMachine {
		t.Fatalf("Scopes = %+v, want the machine row alone", o.Scopes)
	}
	if !nearly(o.Scopes[0].CostUSD, 55) {
		t.Errorf("machine row = $%.2f, want $55.00 — an unbounded window counts every day", o.Scopes[0].CostUSD)
	}
}

// bigFixture is a wider corpus than fixture(): five sessions spread over four
// days and three priced models plus one unpriced model, so a rollup on any axis
// has more than one bucket and the total is not trivially one number repeated.
// It exists to pin the invariant TestMachineTotalAgreesWithDirectTotal checks:
// a coincidental match on a two-session fixture would not catch a real
// accumulation bug the way a wider corpus does.
func bigFixture() []model.Summary {
	const (
		d1 = "2026-08-12"
		d2 = "2026-08-20"
		d3 = "2026-09-01"
		d4 = "2026-09-10"
	)
	return []model.Summary{
		{
			ID: "s1", Start: day(d1), End: day(d1),
			DailyUsage: []model.DailyUsage{out(d1, "claude-sonnet-5", 3*million)},
		},
		{
			ID: "s2", Start: day(d2), End: day(d2),
			DailyUsage: []model.DailyUsage{
				out(d2, "claude-opus-5", 2*million),
				out(d2, "claude-sonnet-5", million),
			},
		},
		{
			ID: "s3", Start: day(d2), End: day(d3),
			DailyUsage: []model.DailyUsage{
				out(d2, "claude-haiku-4-5", 5*million),
				out(d3, "claude-sonnet-5", 4*million),
			},
		},
		{
			ID: "s4", Start: day(d3), End: day(d3),
			DailyUsage: []model.DailyUsage{out(d3, "claude-nope-9", million)}, // unpriced
		},
		{
			ID: "s5", Start: day(d4), End: day(d4),
			DailyUsage: []model.DailyUsage{out(d4, "claude-opus-5", 7*million)},
		},
	}
}

// TestMachineTotalAgreesWithDirectTotal pins the guarantee cost.go's own
// BuildOverview doc comment claims: "a figure read off the summary and one read
// off `--by total` over the same sessions cannot differ." It holds on the
// current code and is kept to pin that guarantee against a future change to
// BuildOverview's machine scope — not as a reproduction of anything: a report
// of `agentry cost` and `agentry cost --all-projects --from all --since 30d`
// disagreeing by cents over the same 305 sessions was the two paths reading a
// live log seconds apart, and no in-package fixture can express that.
// Also pins that the total does not depend on which axis it was
// rolled up by, since every --by value shares the one accumulation loop in
// Build: a day, week, month, model, or session rollup of the same sessions
// must sum to the same total as ByTotal.
func TestMachineTotalAgreesWithDirectTotal(t *testing.T) {
	sums := bigFixture()
	since := day("2026-08-12")

	direct := Build(sums, ByTotal, since, time.Time{}).Total
	o := BuildOverview(nil, nil, sums, Window{MachineSince: since})
	if len(o.Scopes) != 1 || o.Scopes[0].Scope != ScopeMachine {
		t.Fatalf("Scopes = %+v, want the machine row alone", o.Scopes)
	}
	machine := o.Scopes[0]

	if !nearly(machine.CostUSD, direct.CostUSD) {
		t.Errorf("machine row $%.6f, direct total $%.6f: the two must agree on the same sessions",
			machine.CostUSD, direct.CostUSD)
	}
	if machine.Sessions != direct.Sessions {
		t.Errorf("machine row %d session(s), direct total %d: the two must agree", machine.Sessions, direct.Sessions)
	}
	if machine.Usage != direct.Usage {
		t.Errorf("machine row usage %+v, direct total usage %+v: the two must agree", machine.Usage, direct.Usage)
	}

	for _, by := range Axes {
		got := Build(sums, by, since, time.Time{}).Total.CostUSD
		if !nearly(got, direct.CostUSD) {
			t.Errorf("--by %s: Total.CostUSD = %.6f, want %.6f (the same as --by total): "+
				"a rollup's total must not depend on the axis it groups by", by, got, direct.CostUSD)
		}
	}
}

// TestRenderOverviewNamesEachWindow pins the text form: each row says which
// window it counted, so the three reading differently is stated rather than
// left to be discovered.
func TestRenderOverviewNamesEachWindow(t *testing.T) {
	sums := fixture()
	var buf bytes.Buffer
	if err := RenderOverview(&buf, BuildOverview(&sums[0], sums, sums, Window{MachineSince: day(tuesday)}), Options{Width: 100}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"This session", "First session", "$35.00",
		"This folder", "2 sessions, all time", "$55.00",
		"This machine", "2 sessions since " + tuesday, "$45.00",
		"an estimate, not a bill",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Sessions") {
		t.Errorf("summary printed the roll-up's header row:\n%s", got)
	}
}

// agentOut is a delegated response: the same output-only tally out builds,
// labelled with what it was delegated to.
func agentOut(day, modelID, agent string, tokens int) model.DailyUsage {
	d := out(day, modelID, tokens)
	d.Agent = agent
	return d
}

// worked is one day's turns and the minutes they ran for.
func worked(day string, turns, minutes int) model.DailyActivity {
	return model.DailyActivity{Day: day, Turns: turns, ActiveSeconds: minutes * 60}
}

// delegating is one session that spent on its own thread, on a named subagent
// and on a forked skill, priced so the three rows sort in an order none of the
// other fields would produce by accident.
func delegating() []model.Summary {
	return []model.Summary{{
		ID: "cccccccc-3333", Title: "Delegating session",
		Start: day(monday), End: day(monday),
		DailyUsage: []model.DailyUsage{
			out(monday, "claude-opus-5", million),                     // $25 on the main thread
			agentOut(monday, "claude-sonnet-5", "Explore", million),   // $10
			agentOut(monday, "claude-sonnet-5", "/lookup", 2*million), // $20
		},
		DailyActivity: []model.DailyActivity{worked(monday, 4, 40)},
	}}
}

// TestBuildByAgentSplitsTheBillWithoutGrowingIt pins the axis and the rule that
// makes it readable as a share: the main thread is a row, so the rows sum to the
// same total every other axis reports over the same sessions.
func TestBuildByAgentSplitsTheBillWithoutGrowingIt(t *testing.T) {
	r := Build(delegating(), ByAgent, time.Time{}, time.Time{})
	want := []struct {
		key  string
		cost float64
	}{{mainThreadAgent, 25}, {"/lookup", 20}, {"Explore", 10}}
	if len(r.Buckets) != len(want) {
		t.Fatalf("buckets = %+v, want %d rows", r.Buckets, len(want))
	}
	for i, w := range want {
		if r.Buckets[i].Key != w.key {
			t.Errorf("row %d key = %q, want %q (rows sort by cost, priciest first)", i, r.Buckets[i].Key, w.key)
		}
		if !nearly(r.Buckets[i].CostUSD, w.cost) {
			t.Errorf("row %q = $%.2f, want $%.2f", w.key, r.Buckets[i].CostUSD, w.cost)
		}
	}
	if total := Build(delegating(), ByTotal, time.Time{}, time.Time{}).Total.CostUSD; !nearly(r.Total.CostUSD, total) {
		t.Errorf("agent total $%.2f, plain total $%.2f — the axis must split the bill, not add to it",
			r.Total.CostUSD, total)
	}
}

// TestTurnsLandInTheBucketTheyStartedIn pins the reason turns are carried per
// day rather than per session: a day row has to divide that day's dollars by
// that day's turns, not by every turn of every session that touched it.
func TestTurnsLandInTheBucketTheyStartedIn(t *testing.T) {
	sums := fixture()
	sums[0].DailyActivity = []model.DailyActivity{worked(monday, 3, 30), worked(tuesday, 5, 50)}
	sums[1].DailyActivity = []model.DailyActivity{worked(tuesday, 2, 10)}

	r := Build(sums, ByDay, time.Time{}, time.Time{})
	want := map[string]struct{ turns, seconds int }{
		monday:  {3, 30 * 60},
		tuesday: {7, 60 * 60},
	}
	for _, b := range r.Buckets {
		w, ok := want[b.Key]
		if !ok {
			t.Fatalf("unexpected row %q", b.Key)
		}
		if b.Turns != w.turns || b.ActiveSeconds != w.seconds {
			t.Errorf("%s = %d turns / %ds, want %d turns / %ds", b.Key, b.Turns, b.ActiveSeconds, w.turns, w.seconds)
		}
	}
	if r.Total.Turns != 10 {
		t.Errorf("total turns = %d, want 10", r.Total.Turns)
	}
}

// TestModelAndAgentAxesCountNoTurns pins the one case where a column is dropped
// rather than filled: a turn can run on two models and delegate to two agents,
// so it belongs to no row on either axis.
func TestModelAndAgentAxesCountNoTurns(t *testing.T) {
	for _, by := range []string{ByModel, ByAgent} {
		r := Build(delegating(), by, time.Time{}, time.Time{})
		if r.Total.Turns != 0 {
			t.Errorf("--by %s counted %d turns; a turn has no single %s", by, r.Total.Turns, by)
		}
		var buf bytes.Buffer
		if err := Render(&buf, r, Options{Width: 120}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "$/turn") {
			t.Errorf("--by %s rendered a $/turn column:\n%s", by, buf.String())
		}
	}
}

// TestRenderShowsWhatTheDollarsBought pins the three columns and the two lines
// under the total, including the spread — one average cannot say that a few very
// large turns carry the bill.
func TestRenderShowsWhatTheDollarsBought(t *testing.T) {
	sums := fixture()
	sums[0].DailyActivity = []model.DailyActivity{worked(monday, 2, 30), worked(tuesday, 3, 30)}
	sums[1].DailyActivity = []model.DailyActivity{worked(tuesday, 4, 20)}
	sums = append(sums, delegating()...)

	var buf bytes.Buffer
	if err := Render(&buf, Build(sums, BySession, time.Time{}, time.Time{}), Options{Width: 120}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"Turns", "Active", "$/turn", " turns at $", "of active time at $", "half of those 3 sessions"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output is missing %q:\n%s", want, got)
		}
	}
}

// TestSpreadNeedsEnoughSessionsToMeanAnything pins the floor: below it the
// median is one session's own figure restated, which reads as a second
// measurement and is not one.
func TestSpreadNeedsEnoughSessionsToMeanAnything(t *testing.T) {
	sums := fixture()
	sums[0].DailyActivity = []model.DailyActivity{worked(monday, 2, 30)}
	sums[1].DailyActivity = []model.DailyActivity{worked(tuesday, 4, 20)}
	if r := Build(sums, BySession, time.Time{}, time.Time{}); r.Spread != nil {
		t.Errorf("spread reported over %d sessions, want none below %d", r.Spread.Sessions, minSpreadSessions)
	}
}

// TestWindowNarrowsEveryScope pins what a caller asking for a window gets: all
// three rows moved to it, and the machine's own default replaced rather than
// applied on top — a span nobody chose is worse than either bound alone.
func TestWindowNarrowsEveryScope(t *testing.T) {
	sums := fixture()
	sess := sums[0]
	o := BuildOverview(&sess, sums, sums, Window{
		Since:        day(tuesday),
		MachineSince: day(monday),
	})
	if len(o.Scopes) != 3 {
		t.Fatalf("Scopes = %+v, want three rows", o.Scopes)
	}
	for _, s := range o.Scopes {
		if s.Since != tuesday {
			t.Errorf("%s row counts from %q, want the window's own %q", s.Scope, s.Since, tuesday)
		}
	}
	// Monday's spend is outside the window, so the folder row must have dropped it.
	all := Build(sums, ByTotal, time.Time{}, time.Time{}).Total
	for _, s := range o.Scopes {
		if s.Scope == ScopeFolder && !(s.CostUSD < all.CostUSD) {
			t.Errorf("folder row = $%.2f over the whole fixture's $%.2f; the window changed nothing",
				s.CostUSD, all.CostUSD)
		}
	}
}

// TestRollUpSaysWhatItPriced pins the line a lone total cannot do without: the
// rows are days or models and the figure beneath them reads "Total", which names
// no scope at all.
func TestRollUpSaysWhatItPriced(t *testing.T) {
	r := Build(fixture(), ByTotal, time.Time{}, time.Time{})
	r.Selection = &Selection{Scope: "this folder", Since: monday, Until: tuesday}
	var buf bytes.Buffer
	if err := Render(&buf, r, Options{Width: 100}); err != nil {
		t.Fatal(err)
	}
	want := "priced this folder from " + monday + " to " + tuesday
	if !strings.Contains(buf.String(), want) {
		t.Errorf("rendered output is missing %q:\n%s", want, buf.String())
	}
}

// projectFixture is three sessions in three directories: a repository, a
// worktree that repository's tooling made beneath a dot-directory, and a
// sibling repository sharing the same parent.
func projectFixture() []model.Summary {
	return []model.Summary{
		{
			ID: "cccccccc-1111", Cwd: "/home/u/Projects/app",
			DailyUsage:    []model.DailyUsage{out(monday, "claude-sonnet-5", million)},
			DailyActivity: []model.DailyActivity{{Day: monday, Turns: 2, ActiveSeconds: 60}},
		},
		{
			ID: "cccccccc-2222", Cwd: "/home/u/Projects/app/.claude-worktrees/feature",
			DailyUsage:    []model.DailyUsage{out(monday, "claude-sonnet-5", million)},
			DailyActivity: []model.DailyActivity{{Day: monday, Turns: 3, ActiveSeconds: 90}},
		},
		{
			ID: "cccccccc-3333", Cwd: "/home/u/Projects/other",
			DailyUsage: []model.DailyUsage{out(monday, "claude-sonnet-5", million)},
		},
	}
}

// TestProjectAxisFoldsAWorktreeIntoItsRepository pins the cut that makes the
// axis answer "which repo costs me". A worktree lives under a dot-directory
// inside the repository, so its dollars belong to the repository; a sibling
// repository under the same parent does not, and stays a row of its own.
func TestProjectAxisFoldsAWorktreeIntoItsRepository(t *testing.T) {
	old := homeDir
	homeDir = "/home/u"
	t.Cleanup(func() { homeDir = old })

	r := Build(projectFixture(), ByProject, time.Time{}, time.Time{})
	got := map[string]float64{}
	for _, b := range r.Buckets {
		got[b.Key] = b.CostUSD
	}
	if len(got) != 2 {
		t.Fatalf("Build(--by project) made %d rows %v, want 2: the worktree folds into its repository", len(got), got)
	}
	if !nearly(got["~/Projects/app"], 20) {
		t.Errorf("~/Projects/app = %v, want 20 — both its own session and its worktree's", got["~/Projects/app"])
	}
	if !nearly(got["~/Projects/other"], 10) {
		t.Errorf("~/Projects/other = %v, want 10 — a sibling repository is not folded in", got["~/Projects/other"])
	}
}

// TestProjectAxisCountsTurns is the half the model and agent axes cannot do: a
// turn belongs to one session and a session to one directory, so the project
// axis carries the Turns column.
func TestProjectAxisCountsTurns(t *testing.T) {
	old := homeDir
	homeDir = "/home/u"
	t.Cleanup(func() { homeDir = old })

	r := Build(projectFixture(), ByProject, time.Time{}, time.Time{})
	for _, b := range r.Buckets {
		if b.Key == "~/Projects/app" && b.Turns != 5 {
			t.Errorf("~/Projects/app turns = %d, want 5 — the repository's own two plus the worktree's three", b.Turns)
		}
	}
}

// TestProjectRootKeepsALoneHiddenDirectory pins the exception: cutting at the
// first hidden segment would name the home directory for ~/.config and ~/.bob
// alike, merging every such directory into one row.
func TestProjectRootKeepsALoneHiddenDirectory(t *testing.T) {
	old := homeDir
	homeDir = "/home/u"
	t.Cleanup(func() { homeDir = old })

	for _, tt := range []struct{ cwd, want string }{
		{"/home/u/.bob", "/home/u/.bob"},
		{"/home/u/.config/nvim", "/home/u/.config"},
		{"/home/u/Projects/app/.claude-worktrees/x", "/home/u/Projects/app"},
		{"/home/u/Projects/app", "/home/u/Projects/app"},
		{"/private/tmp/work", "/private/tmp/work"},
	} {
		if got := projectRoot(tt.cwd); got != tt.want {
			t.Errorf("projectRoot(%q) = %q, want %q", tt.cwd, got, tt.want)
		}
	}
}

// TestRenderDrawsAShareBarOnEveryRowThatSpent pins both halves of the bar: the
// largest row fills the column, and a row too small to round up to one mark
// still draws the narrowest one rather than reading as a row that cost nothing.
func TestRenderDrawsAShareBarOnEveryRowThatSpent(t *testing.T) {
	sums := []model.Summary{
		{ID: "dddddddd-1111", DailyUsage: []model.DailyUsage{out(monday, "claude-opus-5", 1000*million)}},
		{ID: "dddddddd-2222", DailyUsage: []model.DailyUsage{out(tuesday, "claude-opus-5", million / 100)}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, Build(sums, ByDay, time.Time{}, time.Time{}), Options{Width: 120}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(buf.String(), "\n")
	if !strings.Contains(lines[0], "Share") {
		t.Fatalf("header = %q, want a Share column", lines[0])
	}
	if !strings.Contains(lines[1], "█") {
		t.Errorf("largest row = %q, want a full block", lines[1])
	}
	if !strings.Contains(lines[2], "▏") {
		t.Errorf("smallest row = %q, want the narrowest mark rather than an empty cell", lines[2])
	}
	total := lines[4]
	if strings.ContainsAny(total, "█▏") {
		t.Errorf("total row = %q, want no bar — a full-width bar says only that the total is the total", total)
	}
}

// TestRenderDropsTheShareBarBeforeAnyFigure pins the degradation order: a
// terminal too narrow for everything loses the bar, never a number.
func TestRenderDropsTheShareBarBeforeAnyFigure(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Build(fixture(), ByDay, time.Time{}, time.Time{}), Options{Width: 46}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "Share") || strings.ContainsAny(got, "█▏▎▍▌▋▊▉") {
		t.Errorf("narrow render = %q, want no share column", got)
	}
	if !strings.Contains(got, "$10.00") {
		t.Errorf("narrow render = %q, want the figures kept", got)
	}
}

// TestRenderCalendarReplacesTheRowsAndKeepsTheNotes pins what a chart does and
// does not take the place of: the rows go, and the total and its notes stay,
// because those say what the picture is of.
func TestRenderCalendarReplacesTheRowsAndKeepsTheNotes(t *testing.T) {
	var buf bytes.Buffer
	r := Build(fixture(), ByDay, time.Time{}, time.Time{})
	if err := Render(&buf, r, Options{Width: 100, Chart: ChartCalendar}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, monday) {
		t.Errorf("calendar render = %q, want the day rows replaced", got)
	}
	if !strings.Contains(got, "Mon") || !strings.ContainsAny(got, string(calendarShades)) {
		t.Errorf("calendar render = %q, want a weekday grid with shaded days", got)
	}
	if !strings.Contains(got, "Total") || !strings.Contains(got, "estimate, not a bill") {
		t.Errorf("calendar render = %q, want the total and its notes kept", got)
	}
}

// TestChartFallsBackToTheTableWhenItCannotBeDrawn pins the one case a picture
// has nothing to say: a single bucket is not a line, and printing an empty
// frame would be worse than printing the row.
func TestChartFallsBackToTheTableWhenItCannotBeDrawn(t *testing.T) {
	sums := []model.Summary{{ID: "eeeeeeee-1111", DailyUsage: []model.DailyUsage{out(monday, "claude-opus-5", million)}}}
	var buf bytes.Buffer
	if err := Render(&buf, Build(sums, ByDay, time.Time{}, time.Time{}), Options{Width: 100, Chart: ChartLine}); err != nil {
		t.Fatal(err)
	}
	// The header, not the day: the plot labels its own span with that same day,
	// so a test looking for the date passes whether or not the table was printed.
	if !strings.Contains(buf.String(), "Sessions") {
		t.Errorf("one-bucket line chart = %q, want the table instead", buf.String())
	}
}

// TestLineChartPlotsOldestLeft pins the axis direction against the listing's
// convention, and that the plot labels the range it drew.
func TestLineChartPlotsOldestLeft(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Build(fixture(), ByDay, time.Time{}, time.Time{}), Options{Width: 100, Chart: ChartLine}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, monday+" → "+tuesday) {
		t.Errorf("line chart = %q, want the span labelled oldest first", got)
	}
}
