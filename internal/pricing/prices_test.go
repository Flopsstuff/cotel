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
	// claude-opus-4-7 with mixed token types
	got := pricing.Compute("claude-opus-4-7", 2048, 768, 512, 256)
	// 2048/M*15 + 768/M*75 + 512/M*1.5 + 256/M*18.75
	want := (2048*15.0 + 768*75.0 + 512*1.5 + 256*18.75) / 1_000_000
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
