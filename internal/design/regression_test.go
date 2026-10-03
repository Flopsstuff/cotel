package design_test

import (
	"strings"
	"testing"

	"github.com/Flopsstuff/cotel/internal/design"
)

// The palette exactly as it shipped before the re-derive. It violated both
// rules, and no check measured it for months. Kept here so the guard is held
// against a known-bad palette and not only against a passing one: if this ever
// reports nothing, the guard has gone blind and the rules above are decoration.
const brokenTokensCSS = `
:root {
  --color-surface: #ffffff;
  --color-chart-1: #2563eb;
  --color-chart-2: #7c3aed;
  --color-chart-3: #059669;
  --color-chart-4: #d97706;
  --color-chart-5: #db2777;
}
@media (prefers-color-scheme: dark) {
  :root {
    --color-surface: #0f172a;
    --color-chart-1: #60a5fa;
    --color-chart-2: #a78bfa;
    --color-chart-3: #34d399;
    --color-chart-4: #fbbf24;
    --color-chart-5: #f472b6;
  }
}`

func brokenSchemes(t *testing.T) [2]design.Scheme {
	t.Helper()
	light, dark, err := design.ParseTokens(brokenTokensCSS)
	if err != nil {
		t.Fatalf("parse the pre-re-derive palette: %v", err)
	}
	return [2]design.Scheme{light, dark}
}

func TestGuardRejectsThePreRederivePalette(t *testing.T) {
	s := brokenSchemes(t)

	// chart-1 against chart-2 was the defect that motivated the rules: the pair
	// the ordered palette hands out first, indistinguishable to a deuteranope.
	for _, sc := range s {
		e, at := design.WorstDeltaE(sc.Chart[0], sc.Chart[1])
		if e >= design.DeltaEBar {
			t.Errorf("%s: chart-1 vs chart-2 measured ΔE %.1f under %s, expected it to fail the bar of %.1f",
				sc.Name, e, at, design.DeltaEBar)
		}
		if at != design.Deuteranopia {
			t.Errorf("%s: chart-1 vs chart-2 worst case was %s, expected deuteranopia", sc.Name, at)
		}
		if e > 1 {
			t.Errorf("%s: chart-1 vs chart-2 measured ΔE %.1f, expected under 1 — the two were the same colour", sc.Name, e)
		}
	}

	// Rule 1 failed two ways: chart-4 sat under the floor in light, and the
	// dark rank order disagreed with the light one.
	if got := s[0].Contrasts()[3]; got >= design.ContrastFloor {
		t.Errorf("light chart-4 measured %.2f:1, expected it to fail the floor of %.1f", got, design.ContrastFloor)
	}
	if light, dark := s[0].Contrasts().Ranks(), s[1].Contrasts().Ranks(); light == dark {
		t.Errorf("rank orders agreed (%v), expected them to disagree", light)
	}
}

// The guard's value is the failure text, so assert the text the rules actually
// emit — not a reconstruction of it from the same parts.
func TestFailureTextNamesTheOffenderAndBothMeasurements(t *testing.T) {
	s := brokenSchemes(t)

	msg, failed := separationFailure(s[0], [2]int{1, 2})
	if !failed {
		t.Fatal("rule 2 passed chart-1 against chart-2 on the pre-re-derive palette")
	}
	// The pair, the scheme, the deficiency, the measurement, the bar, and both
	// colours, so the reader can fix the colour instead of the test.
	for _, want := range []string{
		"chart-1 vs chart-2", "(light)", "deuteranopia", "bar is 8.0", "#2563eb", "#7c3aed",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("rule 2 failure does not name %q:\n  %s", want, msg)
		}
	}

	msg, failed = salienceFloorFailure(s[0], 4)
	if !failed {
		t.Fatal("rule 1's floor passed light chart-4, which measured 3.19:1")
	}
	for _, want := range []string{"chart-4 (light)", "--color-surface #ffffff", "floor is 4.0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rule 1 floor failure does not name %q:\n  %s", want, msg)
		}
	}

	msg, failed = salienceRankFailure(s)
	if !failed {
		t.Fatal("rule 1's rank check passed, but the pre-re-derive orders disagreed")
	}
	for _, want := range []string{"light [", "dark [", "chart-1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rule 1 rank failure does not name %q:\n  %s", want, msg)
		}
	}
}
