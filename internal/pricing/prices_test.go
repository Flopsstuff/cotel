package pricing_test

import (
	"math"
	"testing"

	"github.com/Flopsstuff/cotel/internal/pricing"
)

func TestComputeKnownModel(t *testing.T) {
	// claude-sonnet-4-6: $3/MTok in, $15/MTok out, $0.30/MTok cache-read, $3.75/MTok cache-write
	got := pricing.Compute("claude-sonnet-4-6", 1_000_000, 0, 0, 0)
	want := 3.00
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("input only: got %.6f, want %.6f", got, want)
	}
}

func TestComputeAllTokenTypes(t *testing.T) {
	// claude-opus-4-7 with mixed token types (corrected 2026-08 rates: $5/$25/$0.50/$6.25)
	got := pricing.Compute("claude-opus-4-7", 2048, 768, 512, 256)
	// 2048/M*5 + 768/M*25 + 512/M*0.5 + 256/M*6.25
	want := (2048*5.0 + 768*25.0 + 512*0.5 + 256*6.25) / 1_000_000
	if math.Abs(got-want) > 0.000001 {
		t.Errorf("got %.8f, want %.8f", got, want)
	}
	if got < 0.009 {
		t.Errorf("cost suspiciously low: %.8f", got)
	}
}

func TestComputeTierSuffix(t *testing.T) {
	// "[1m]" suffix must be stripped before lookup
	withSuffix := pricing.Compute("claude-opus-4-7[1m]", 1_000_000, 0, 0, 0)
	withoutSuffix := pricing.Compute("claude-opus-4-7", 1_000_000, 0, 0, 0)
	if withSuffix != withoutSuffix {
		t.Errorf("tier suffix not stripped: with=%v without=%v", withSuffix, withoutSuffix)
	}
}

func TestComputeUnknownModel(t *testing.T) {
	// Unknown model must return 0, not panic or error.
	got := pricing.Compute("claude-unknown-99", 1_000_000, 500_000, 0, 0)
	if got != 0 {
		t.Errorf("unknown model: expected 0, got %v", got)
	}
}

func TestComputeEmptyModel(t *testing.T) {
	// Empty model (non-LLM spans) must silently return 0 with no log noise.
	got := pricing.Compute("", 0, 0, 0, 0)
	if got != 0 {
		t.Errorf("empty model: expected 0, got %v", got)
	}
}

func TestComputeZeroTokens(t *testing.T) {
	got := pricing.Compute("claude-sonnet-4-6", 0, 0, 0, 0)
	if got != 0 {
		t.Errorf("zero tokens: expected 0, got %v", got)
	}
}

func TestComputeOpus5AllTokenTypes(t *testing.T) {
	// claude-opus-5: $5/MTok in, $25/MTok out, $0.50/MTok cache-read, $6.25/MTok cache-write
	got := pricing.Compute("claude-opus-5", 2048, 768, 512, 256)
	want := (2048*5.0 + 768*25.0 + 512*0.5 + 256*6.25) / 1_000_000
	if math.Abs(got-want) > 0.000001 {
		t.Errorf("got %.8f, want %.8f", got, want)
	}
	if got <= 0 {
		t.Errorf("cost must be non-zero: %.8f", got)
	}
}

func TestComputeOpus5TierSuffixNonZero(t *testing.T) {
	// Regression for FLO-550: the whole fleet rides "claude-opus-5[1m]", which
	// was absent from the table, so Compute() returned cost_usd = 0 for every
	// span. The "[1m]" suffix must be stripped to "claude-opus-5" and yield a
	// NON-ZERO cost.
	withSuffix := pricing.Compute("claude-opus-5[1m]", 1_000_000, 500_000, 0, 0)
	withoutSuffix := pricing.Compute("claude-opus-5", 1_000_000, 500_000, 0, 0)
	if withSuffix != withoutSuffix {
		t.Errorf("tier suffix not stripped: with=%v without=%v", withSuffix, withoutSuffix)
	}
	if withSuffix <= 0 {
		t.Errorf("claude-opus-5[1m] must have non-zero cost, got %v", withSuffix)
	}
	// 1M input @ $5 + 0.5M output @ $25 = 5 + 12.5 = 17.5
	want := 17.5
	if math.Abs(withSuffix-want) > 0.000001 {
		t.Errorf("got %.8f, want %.8f", withSuffix, want)
	}
}

func TestComputeHaiku45Corrected(t *testing.T) {
	// claude-haiku-4-5 corrected to $1.00/$5.00 per MTok (was a stale $0.80/$4.00).
	got := pricing.Compute("claude-haiku-4-5", 1_000_000, 0, 0, 0)
	want := 1.00
	if math.Abs(got-want) > 0.0001 {
		t.Errorf("input only: got %.6f, want %.6f", got, want)
	}
	// Guard against the stale $0.80 rate creeping back in.
	if math.Abs(got-0.80) < 0.0001 {
		t.Errorf("haiku-4-5 still using stale $0.80 rate")
	}
}
