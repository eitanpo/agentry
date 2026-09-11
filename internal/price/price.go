// Package price holds what a model's tokens cost, so agentry can put a dollar
// figure on a session whose log carries none — which is most of them, Claude
// Code having written its own cost record only since 2.1.241.
//
// The table is undated and stamped with the day it was verified instead. No rate
// moved for any model in local use between 2026-07-02 and 2026-09-11, measured
// against LiteLLM's dated snapshots, so rates keyed to a timestamp would add a
// second thing to get wrong and recover no fact; a stale flat table is also the
// easier of the two to notice.
package price

import (
	"strings"

	"github.com/eitanpo/agentry/internal/model"
)

// VerifiedOn is the day every rate below was last read from its sources, carried
// into `agentry cost --format json` so a caller can tell how old the prices they
// were handed are.
const VerifiedOn = "2026-09-11"

// Rates is one tier's price in dollars per million tokens. Cache writes are two
// prices rather than one: buying an hour of cache costs twice the input rate
// where five minutes costs 1.25 times it.
type Rates struct {
	Input        float64
	CacheWrite5m float64
	CacheWrite1h float64
	CacheRead    float64
	Output       float64
}

// tiers is Claude Code 2.1.268's own pricing_tiers object, read out of the
// binary's baked model catalog on 2026-09-11 and agreeing with Anthropic's
// published prices. The tier names are Claude Code's, kept rather than
// paraphrased so re-verifying a rate is a grep of the binary.
//
// web_search sits in that object too, at $0.01 a search for every tier, and is
// deliberately absent here: a search is billed per request rather than per token
// and agentry reads no request count, so a session that searched is priced
// slightly low and PRODUCT.md says so.
var tiers = map[string]Rates{
	"tier_2_10":                  {Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.2, Output: 10},
	"tier_3_15":                  {Input: 3, CacheWrite5m: 3.75, CacheWrite1h: 6, CacheRead: 0.3, Output: 15},
	"tier_5_25":                  {Input: 5, CacheWrite5m: 6.25, CacheWrite1h: 10, CacheRead: 0.5, Output: 25},
	"tier_10_50":                 {Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 1, Output: 50},
	"tier_10_50_cache_read_0_25": {Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
	"tier_15_75":                 {Input: 15, CacheWrite5m: 18.75, CacheWrite1h: 30, CacheRead: 1.5, Output: 75},
	"haiku_45":                   {Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, Output: 5},
	"haiku_35":                   {Input: 0.8, CacheWrite5m: 1, CacheWrite1h: 1.6, CacheRead: 0.08, Output: 4},
}

// modelTiers maps a model id to the tier it is billed at — all 19 entries of the
// same catalog, each model's own "pricing" key.
//
// A log's id carries more than the catalog's: a release date
// (claude-haiku-4-5-20251001) and a suffix asking for the million-token context
// window. Both are matched by taking the longest catalog id that prefixes the
// log's, which also keeps Fable 5.1 off Fable 5's rates — the two differ only in
// what a cache read costs, and a first-match rule would price 5.1 four times too
// high on reads. The long-context suffix is deliberately not a tier of its own:
// the catalog prices a model one way however wide its window is.
var modelTiers = map[string]string{
	"claude-3-5-haiku":  "haiku_35",
	"claude-haiku-4-5":  "haiku_45",
	"claude-3-5-sonnet": "tier_3_15",
	"claude-3-7-sonnet": "tier_3_15",
	"claude-sonnet-4-0": "tier_3_15",
	"claude-sonnet-4-5": "tier_3_15",
	"claude-sonnet-4-6": "tier_3_15",
	"claude-sonnet-5":   "tier_2_10",
	"claude-opus-4-0":   "tier_15_75",
	"claude-opus-4-1":   "tier_15_75",
	"claude-opus-4-5":   "tier_5_25",
	"claude-opus-4-6":   "tier_5_25",
	"claude-opus-4-7":   "tier_5_25",
	"claude-opus-4-8":   "tier_5_25",
	"claude-opus-5":     "tier_5_25",
	"claude-fable-5":    "tier_10_50",
	"claude-fable-5-1":  "tier_10_50_cache_read_0_25",
	"claude-mythos-5":   "tier_10_50",
	"claude-mythos-5-1": "tier_10_50_cache_read_0_25",
}

// Of returns what one model's tokens cost at list price, and whether that model
// has a price at all. A caller handed false must name the model rather than add
// the zero: models appear without notice, and a zero reads as a session that
// cost nothing instead of one nothing is known about.
//
// The five-minute share of the cache writes is the flat counter less the hour
// share, so a log too old to split them is priced entirely at the five-minute
// rate — the lower of the two, and the one such a log can still be shown to
// support. Where the split exceeds the flat counter — 165 tokens across the whole
// local corpus, measured 2026-09-11 — the five-minute share clamps at zero rather
// than turning the subtraction negative, which is the Math.min Claude Code's own
// pricing function applies there.
func Of(modelID string, u model.Usage) (float64, bool) {
	r, ok := ratesOf(modelID)
	if !ok {
		return 0, false
	}
	write1h := u.CacheCreate1h
	if write1h > u.CacheCreate {
		write1h = u.CacheCreate
	}
	write5m := u.CacheCreate - write1h
	perMillion := float64(u.Input)*r.Input +
		float64(u.Output)*r.Output +
		float64(u.CacheRead)*r.CacheRead +
		float64(write5m)*r.CacheWrite5m +
		float64(write1h)*r.CacheWrite1h
	return perMillion / 1e6, true
}

// ratesOf resolves a log's model id to its tier's rates by longest prefix.
func ratesOf(modelID string) (Rates, bool) {
	best := ""
	for prefix := range modelTiers {
		if len(prefix) > len(best) && strings.HasPrefix(modelID, prefix) {
			best = prefix
		}
	}
	if best == "" {
		return Rates{}, false
	}
	return tiers[modelTiers[best]], true
}
