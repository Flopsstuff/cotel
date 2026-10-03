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

// The guard's value is the failure text, so assert it names the pair, the
// deficiency and both colours rather than only that it failed.
func TestFailureTextNamesThePairAndBothMeasurements(t *testing.T) {
	s := brokenSchemes(t)
	light := s[0]
	e, at := design.WorstDeltaE(light.Chart[0], light.Chart[1])

	msg := strings.Join([]string{
		design.ChartName(1), design.ChartName(2), light.Name,
		at.String(), light.Chart[0].Hex(), light.Chart[1].Hex(),
	}, " ")
	for _, want := range []string{"chart-1", "chart-2", "light", "deuteranopia", "#2563eb", "#7c3aed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("a rule 2 failure could not name %q (from %q)", want, msg)
		}
	}
	if e <= 0 {
		t.Errorf("ΔE %.1f is not a usable measurement", e)
	}
}
