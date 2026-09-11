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
		"Claude Code recorded $7.50 for the 1 of these sessions it kept a record for",
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
	for _, want := range []string{"Session", "Title", "aaaaaaaa", "First session", "2 session(s)", "$35.00"} {
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
