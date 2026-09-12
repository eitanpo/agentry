package price

import (
	"math"
	"testing"

	"github.com/eitanpo/agentry/internal/model"
)

const million = 1000000

// close compares dollars to a hundredth of a cent, since the arithmetic is
// floating point and the assertions are exact rates.
func nearly(got, want float64) bool { return math.Abs(got-want) < 1e-4 }

// TestOfPricesEachCounterAtItsOwnRate walks one million tokens through each
// counter separately, so a rate swapped between two of them fails here rather
// than cancelling out in a mixed total.
func TestOfPricesEachCounterAtItsOwnRate(t *testing.T) {
	cases := []struct {
		name  string
		usage model.Usage
		want  float64
	}{
		{"input", model.Usage{Input: million}, 5},
		{"output", model.Usage{Output: million}, 25},
		{"cache read", model.Usage{CacheRead: million}, 0.5},
		{"cache write for five minutes", model.Usage{CacheCreate: million}, 6.25},
		{"cache write for an hour", model.Usage{CacheCreate: million, CacheCreate1h: million}, 10},
	}
	for _, c := range cases {
		got, ok := Of("claude-opus-5", c.usage)
		if !ok {
			t.Fatalf("%s: claude-opus-5 has no price", c.name)
		}
		if !nearly(got, c.want) {
			t.Errorf("%s: Of = %f, want %f", c.name, got, c.want)
		}
	}
}

// TestOfSplitsTheCacheWrite checks the one counter that is two prices: the hour
// share at the hour rate and the remainder at the five-minute rate.
func TestOfSplitsTheCacheWrite(t *testing.T) {
	got, ok := Of("claude-opus-5", model.Usage{CacheCreate: million, CacheCreate1h: 400000})
	if !ok {
		t.Fatal("claude-opus-5 has no price")
	}
	want := 0.4*10 + 0.6*6.25
	if !nearly(got, want) {
		t.Errorf("Of = %f, want %f (0.4M at the hour rate, 0.6M at the five-minute one)", got, want)
	}
}

// TestOfClampsAnOversizedHourShare covers the log that reports a split larger
// than the flat counter it splits — 165 tokens across the local corpus do. The
// hour share is capped at the flat counter, so the five-minute remainder is zero
// rather than negative.
func TestOfClampsAnOversizedHourShare(t *testing.T) {
	got, _ := Of("claude-opus-5", model.Usage{CacheCreate: 100, CacheCreate1h: 400})
	if want := 100 * 10.0 / 1e6; !nearly(got, want) {
		t.Errorf("Of = %f, want %f", got, want)
	}
}

// TestOfPricesAnUnsplitCacheWriteAtTheFiveMinuteRate pins what a log too old to
// carry the split reduces to: the lower of the two rates, which is the one such
// a log still supports.
func TestOfPricesAnUnsplitCacheWriteAtTheFiveMinuteRate(t *testing.T) {
	got, _ := Of("claude-opus-5", model.Usage{CacheCreate: million})
	if !nearly(got, 6.25) {
		t.Errorf("Of = %f, want 6.25", got)
	}
}

// TestOfMatchesTheLongestModelPrefix covers the two shapes a log's id takes
// beyond the catalog's — a release date and the million-token context suffix —
// and the pair a first-match rule would confuse: Fable 5.1 reads cache four
// times cheaper than Fable 5, and "claude-fable-5" prefixes both ids.
func TestOfMatchesTheLongestModelPrefix(t *testing.T) {
	cases := []struct {
		id   string
		want float64
	}{
		{"claude-fable-5", 1},
		{"claude-fable-5-1", 0.25},
		{"claude-fable-5-1-20260301", 0.25},
		{"claude-opus-5", 0.5},
		{"claude-opus-5[1m]", 0.5},
		{"claude-haiku-4-5-20251001", 0.1},
	}
	for _, c := range cases {
		got, ok := Of(c.id, model.Usage{CacheRead: million})
		if !ok {
			t.Errorf("%s has no price", c.id)
			continue
		}
		if !nearly(got, c.want) {
			t.Errorf("%s: a million cache reads = %f, want %f", c.id, got, c.want)
		}
	}
}

// TestOfReportsAnUnknownModel pins the answer a caller has to act on: no price,
// and no zero dressed up as one.
func TestOfReportsAnUnknownModel(t *testing.T) {
	got, ok := Of("claude-nope-9", model.Usage{Output: million})
	if ok {
		t.Errorf("claude-nope-9 reports a price of %f", got)
	}
	if got != 0 {
		t.Errorf("Of = %f, want 0 beside the false", got)
	}
}

// TestEveryModelNamesAKnownTier catches a tier renamed in one map and not the
// other, which would otherwise price that model's whole family at zero rather
// than failing.
func TestEveryModelNamesAKnownTier(t *testing.T) {
	for id, tier := range modelTiers {
		r, ok := tiers[tier]
		if !ok {
			t.Errorf("%s names tier %q, which the rate table does not hold", id, tier)
			continue
		}
		if r.Input == 0 || r.Output == 0 || r.CacheRead == 0 || r.CacheWrite5m == 0 || r.CacheWrite1h == 0 {
			t.Errorf("%s (tier %s) has an unpriced counter: %+v", id, tier, r)
		}
	}
}

// TestSavingPricesEachModelAtItsOwnRates pins the reason the saving walks the
// per-model split instead of the session's summed tally. The two models here read
// cache at different fractions of their own input rate — a tenth for one, a
// fortieth for the other — so a session mixing them has a saving that neither
// model's rates produce on their own, and a shortcut through the summed tally
// fails here instead of quietly reporting a share the session did not get.
func TestSavingPricesEachModelAtItsOwnRates(t *testing.T) {
	daily := []model.DailyUsage{
		// A million cache reads at $0.50 against $5.00 of input had they not been cached.
		{Model: "claude-opus-5", Usage: model.Usage{CacheRead: million}},
		// The same million at $0.25 against $10.00, the tier that breaks the ratio.
		{Model: "claude-fable-5-1", Usage: model.Usage{CacheRead: million}},
	}
	got, ok := Saving(daily)
	if !ok {
		t.Fatal("Saving reported nothing priceable on two priced models")
	}
	if !nearly(got.WithCacheUSD, 0.75) || !nearly(got.WithoutCacheUSD, 15) {
		t.Errorf("Saving = %+v, want {0.75 15}", got)
	}
	// 95%, where the summed tally gives 90% at one model's rates and 97.5% at the
	// other's — the failure the split exists to prevent.
	if !nearly(got.SavedShare(), 0.95) {
		t.Errorf("SavedShare = %v, want 0.95", got.SavedShare())
	}
}

// TestSavingLeavesUnpricedModelsOutOfBothHalves pins that a model with no price
// is absent from the comparison rather than counted as zero in it. Counting it at
// zero in both halves would leave the share unchanged while the two dollar figures
// understate what the session spent, so a caller summing them across sessions
// would get a total no model produced.
func TestSavingLeavesUnpricedModelsOutOfBothHalves(t *testing.T) {
	unpriced := model.DailyUsage{Model: "claude-unheard-of-9", Usage: model.Usage{CacheRead: million}}
	priced := model.DailyUsage{Model: "claude-opus-5", Usage: model.Usage{CacheRead: million}}

	mixed, ok := Saving([]model.DailyUsage{priced, unpriced})
	if !ok {
		t.Fatal("Saving reported nothing priceable on a mix holding one priced model")
	}
	alone, _ := Saving([]model.DailyUsage{priced})
	if mixed != alone {
		t.Errorf("Saving with an unpriced model = %+v, want the priced model's own %+v", mixed, alone)
	}

	if _, ok := Saving([]model.DailyUsage{unpriced}); ok {
		t.Error("Saving must report false where no response carries a model it can price")
	}
}
