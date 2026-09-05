package pricing_test

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"strings"
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

func TestComputeSnapshotSuffix(t *testing.T) {
	// Claude Code sends Haiku 4.5 as a dated snapshot ID; the table is keyed
	// undated, so the "-20251001" tail must be stripped before lookup.
	dated := pricing.Compute("claude-haiku-4-5-20251001", 1_000_000, 500_000, 0, 0)
	undated := pricing.Compute("claude-haiku-4-5", 1_000_000, 500_000, 0, 0)
	if dated != undated {
		t.Errorf("snapshot suffix not stripped: dated=%v undated=%v", dated, undated)
	}
	// 1M input @ $1 + 0.5M output @ $5 = 1 + 2.5 = 3.5
	want := 3.5
	if math.Abs(dated-want) > 0.000001 {
		t.Errorf("got %.8f, want %.8f", dated, want)
	}
}

func TestComputeSnapshotAndTierSuffix(t *testing.T) {
	// Both tails at once — tier is stripped first, then the date.
	both := pricing.Compute("claude-opus-5-20250930[1m]", 1_000_000, 500_000, 0, 0)
	bare := pricing.Compute("claude-opus-5", 1_000_000, 500_000, 0, 0)
	if both != bare {
		t.Errorf("combined suffixes not stripped: both=%v bare=%v", both, bare)
	}
	if both <= 0 {
		t.Errorf("claude-opus-5-20250930[1m] must have non-zero cost, got %v", both)
	}
}

func TestComputeSnapshotStripIsExactlyEightDigits(t *testing.T) {
	// The strip must not eat a version segment or a digit run of another length.
	for _, model := range []string{
		"claude-haiku-4-5",
		"claude-3-5-haiku",
		"claude-opus-5",
	} {
		if got := pricing.Compute(model, 1_000_000, 0, 0, 0); got <= 0 {
			t.Errorf("%s: undated ID mangled, got %v", model, got)
		}
	}
	for _, model := range []string{
		"claude-haiku-4-5-2025100",   // 7 digits
		"claude-haiku-4-5-202510011", // 9 digits
		"claude-haiku-4-5-2025100a",  // not all digits
	} {
		if got := pricing.Compute(model, 1_000_000, 0, 0, 0); got != 0 {
			t.Errorf("%s: expected no strip and cost 0, got %v", model, got)
		}
	}
}

func TestComputeUnknownDatedModel(t *testing.T) {
	// A dated ID whose base is still unknown must return 0, not panic.
	got := pricing.Compute("claude-unknown-99-20251001", 1_000_000, 500_000, 0, 0)
	if got != 0 {
		t.Errorf("unknown dated model: expected 0, got %v", got)
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
	// Regression guard: the whole fleet rides "claude-opus-5[1m]", which
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

func TestComputeUnknownModelLogsOnce(t *testing.T) {
	prev := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	model := "claude-logonce-alpha"
	if got := pricing.Compute(model, 1, 0, 0, 0); got != 0 {
		t.Fatalf("unknown model: expected 0, got %v", got)
	}
	if !strings.Contains(buf.String(), `unknown model "claude-logonce-alpha"`) {
		t.Fatalf("first call did not log warning: %q", buf.String())
	}

	buf.Reset()
	pricing.Compute(model, 1, 0, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("repeat call logged: %q", buf.String())
	}

	buf.Reset()
	pricing.Compute(model+"-20260315", 1, 0, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("canonical variant logged: %q", buf.String())
	}
}

func TestComputeUnknownModelLogsEachCanonicalOnce(t *testing.T) {
	prev := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	a := "claude-logonce-bravo"
	b := "claude-logonce-charlie"
	pricing.Compute(a, 1, 0, 0, 0)
	pricing.Compute(b, 1, 0, 0, 0)
	out := buf.String()
	if !strings.Contains(out, `unknown model "`+a+`"`) {
		t.Errorf("missing log for %s: %q", a, out)
	}
	if !strings.Contains(out, `unknown model "`+b+`"`) {
		t.Errorf("missing log for %s: %q", b, out)
	}
	if n := strings.Count(out, "unknown model"); n != 2 {
		t.Errorf("expected 2 unknown-model lines, got %d in %q", n, out)
	}

	buf.Reset()
	pricing.Compute(a, 1, 0, 0, 0)
	pricing.Compute(b, 1, 0, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("repeat of distinct models logged: %q", buf.String())
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

func TestComputeUnknownModelLogCap(t *testing.T) {
	prev := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	// Process-wide set is not reset; fill well past 256 unique canonical IDs.
	const n = 300
	for i := 0; i < n; i++ {
		pricing.Compute(fmt.Sprintf("claude-logonce-cap-%03d", i), 1, 0, 0, 0)
	}
	out := buf.String()
	if n := strings.Count(out, "unknown-model warnings suppressed"); n != 1 {
		t.Fatalf("expected exactly one suppression line, got %d in %q", n, out)
	}
	if n := strings.Count(out, "unknown model"); n > 256 {
		t.Errorf("logged %d unknown-model warnings, cap is 256", n)
	}

	buf.Reset()
	pricing.Compute("claude-logonce-cap-after", 1, 0, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("post-cap unique model logged: %q", buf.String())
	}
	if got := pricing.Compute("claude-logonce-cap-after-2", 1, 0, 0, 0); got != 0 {
		t.Errorf("post-cap unknown still returns 0, got %v", got)
	}
}
