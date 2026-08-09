// Package pricing computes ingest-time cost from token counts.
// Prices are USD per million tokens sourced from Anthropic's published pricing page.
// Last verified: 2026-08 against https://platform.claude.com/docs/en/about-claude/models/overview
// Update this table when Anthropic revises rates.
package pricing

import (
	"log"
	"strings"
)

// ModelPrices holds per-million-token rates for one model tier.
type ModelPrices struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// table maps canonical model IDs to pricing. Keys must be lowercase and match
// what Claude Code emits in the "model" span attribute (without tier suffixes).
//
// Cache rates follow Anthropic's convention: CacheRead = 0.1 x input,
// CacheWrite = 1.25 x input (the 5-minute ephemeral TTL).
var table = map[string]ModelPrices{
	// Claude 5 family
	"claude-fable-5": {
		InputPerMTok:      10.00,
		OutputPerMTok:     50.00,
		CacheReadPerMTok:  1.00,
		CacheWritePerMTok: 12.50,
	},
	"claude-mythos-5": {
		InputPerMTok:      10.00,
		OutputPerMTok:     50.00,
		CacheReadPerMTok:  1.00,
		CacheWritePerMTok: 12.50,
	},
	"claude-opus-5": {
		InputPerMTok:      5.00,
		OutputPerMTok:     25.00,
		CacheReadPerMTok:  0.50,
		CacheWritePerMTok: 6.25,
	},
	// Sonnet 5 has a $2/$10 introductory rate through 2026-08-31; we bill the
	// standard $3/$15 rate. A date-dependent rate would silently rot, so the
	// standard rate is the boring, correct choice.
	"claude-sonnet-5": {
		InputPerMTok:      3.00,
		OutputPerMTok:     15.00,
		CacheReadPerMTok:  0.30,
		CacheWritePerMTok: 3.75,
	},
	// Claude 4.x family
	"claude-opus-4-8": {
		InputPerMTok:      5.00,
		OutputPerMTok:     25.00,
		CacheReadPerMTok:  0.50,
		CacheWritePerMTok: 6.25,
	},
	"claude-opus-4-7": {
		InputPerMTok:      5.00,
		OutputPerMTok:     25.00,
		CacheReadPerMTok:  0.50,
		CacheWritePerMTok: 6.25,
	},
	"claude-opus-4-6": {
		InputPerMTok:      5.00,
		OutputPerMTok:     25.00,
		CacheReadPerMTok:  0.50,
		CacheWritePerMTok: 6.25,
	},
	"claude-sonnet-4-6": {
		InputPerMTok:      3.00,
		OutputPerMTok:     15.00,
		CacheReadPerMTok:  0.30,
		CacheWritePerMTok: 3.75,
	},
	"claude-haiku-4-5": {
		InputPerMTok:      1.00,
		OutputPerMTok:     5.00,
		CacheReadPerMTok:  0.10,
		CacheWritePerMTok: 1.25,
	},
	// Legacy / alias entries
	"claude-3-5-sonnet": {
		InputPerMTok:      3.00,
		OutputPerMTok:     15.00,
		CacheReadPerMTok:  0.30,
		CacheWritePerMTok: 3.75,
	},
	"claude-3-5-haiku": {
		InputPerMTok:      0.80,
		OutputPerMTok:     4.00,
		CacheReadPerMTok:  0.08,
		CacheWritePerMTok: 1.00,
	},
	"claude-3-opus": {
		InputPerMTok:      15.00,
		OutputPerMTok:     75.00,
		CacheReadPerMTok:  1.50,
		CacheWritePerMTok: 18.75,
	},
}

// canonicalID strips tier suffixes like "[1m]" from model identifiers before lookup.
// Claude Code may append these for extended-context variants that share base pricing.
func canonicalID(model string) string {
	if i := strings.IndexByte(model, '['); i != -1 {
		return model[:i]
	}
	return model
}

// Known reports whether the model (after stripping tier suffixes) is present in
// the price table. Callers use this to distinguish a genuinely unpriced model
// from a priced model that happens to compute a zero cost (e.g. a span with zero
// billable tokens). Do not infer "unknown model" from Compute() == 0.
func Known(model string) bool {
	_, ok := table[canonicalID(model)]
	return ok
}

// Compute returns the estimated cost in USD for the given token counts and model.
// If the model is not in the price table it logs a warning and returns 0 so that
// an unknown model never causes an ingest failure.
func Compute(model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64 {
	p, ok := table[canonicalID(model)]
	if !ok {
		if model != "" {
			log.Printf("pricing: unknown model %q — cost_usd will be 0 for this span", model)
		}
		return 0
	}
	const perM = 1_000_000.0
	return (float64(inputTokens)*p.InputPerMTok +
		float64(outputTokens)*p.OutputPerMTok +
		float64(cacheReadTokens)*p.CacheReadPerMTok +
		float64(cacheWriteTokens)*p.CacheWritePerMTok) / perM
}
