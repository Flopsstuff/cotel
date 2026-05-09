// Package pricing computes ingest-time cost from token counts.
// Prices are USD per million tokens sourced from Anthropic's published pricing page.
// Last verified: 2026-05. Update this table when Anthropic revises rates.
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
var table = map[string]ModelPrices{
	"claude-opus-4-7": {
		InputPerMTok:      15.00,
		OutputPerMTok:     75.00,
		CacheReadPerMTok:   1.50,
		CacheWritePerMTok: 18.75,
	},
	"claude-sonnet-4-6": {
		InputPerMTok:       3.00,
		OutputPerMTok:     15.00,
		CacheReadPerMTok:   0.30,
		CacheWritePerMTok:  3.75,
	},
	"claude-haiku-4-5": {
		InputPerMTok:       0.80,
		OutputPerMTok:      4.00,
		CacheReadPerMTok:   0.08,
		CacheWritePerMTok:  1.00,
	},
	// Legacy / alias entries
	"claude-3-5-sonnet": {
		InputPerMTok:       3.00,
		OutputPerMTok:     15.00,
		CacheReadPerMTok:   0.30,
		CacheWritePerMTok:  3.75,
	},
	"claude-3-5-haiku": {
		InputPerMTok:       0.80,
		OutputPerMTok:      4.00,
		CacheReadPerMTok:   0.08,
		CacheWritePerMTok:  1.00,
	},
	"claude-3-opus": {
		InputPerMTok:      15.00,
		OutputPerMTok:     75.00,
		CacheReadPerMTok:   1.50,
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
