package design_test

import (
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/Flopsstuff/cotel/internal/design"
)

var update = flag.Bool("update", false, "rewrite the generated tables in docs/design/tokens.md from the measured values")

const (
	tokensCSS = "../../frontend/src/styles/tokens.css"
	tokensDoc = "../../docs/design/tokens.md"
)

func schemes(t *testing.T) [2]design.Scheme {
	t.Helper()
	src, err := os.ReadFile(tokensCSS)
	if err != nil {
		t.Fatalf("read %s: %v", tokensCSS, err)
	}
	light, dark, err := design.ParseTokens(string(src))
	if err != nil {
		t.Fatalf("parse %s: %v", tokensCSS, err)
	}
	return [2]design.Scheme{light, dark}
}

// Rule 1: every chart token clears the contrast floor against its own scheme's
// surface, and the salience rank order is identical in both schemes.
func TestSalienceRule(t *testing.T) {
	s := schemes(t)
	for _, sc := range s {
		c := sc.Contrasts()
		for i := 1; i <= design.ChartTokens; i++ {
			if got := c[i-1]; got < design.ContrastFloor {
				t.Errorf("%s (%s): contrast %.2f:1 against --color-surface %s, floor is %.1f",
					design.ChartName(i), sc.Name, got, sc.Surface.Hex(), design.ContrastFloor)
			}
		}
	}

	light, dark := s[0].Contrasts().Order(), s[1].Contrasts().Order()
	if fmt.Sprint(light) != fmt.Sprint(dark) {
		t.Errorf("salience rank differs: light %s vs dark %s - %s\n%s",
			orderList(light), orderList(dark), describeRankDiff(light, dark),
			rankEvidence(s, light, dark))
	}
}

// Rule 2: every pair stays at least DeltaEBar apart under the worst of the
// three simulated deficiencies, in both schemes.
func TestSeparationRule(t *testing.T) {
	for _, sc := range schemes(t) {
		for _, p := range design.Pairs() {
			a, b := sc.Chart[p[0]-1], sc.Chart[p[1]-1]
			e, at := design.WorstDeltaE(a, b)
			if e < design.DeltaEBar {
				t.Errorf("%s vs %s (%s): dE %.1f under %s, bar is %.1f (%s vs %s)",
					design.ChartName(p[0]), design.ChartName(p[1]), sc.Name,
					e, at, design.DeltaEBar, a.Hex(), b.Hex())
			}
		}
	}
}

func orderList(order []int) string {
	parts := make([]string, len(order))
	for i, v := range order {
		parts[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// describeRankDiff names the tokens that moved, calling out the common case of
// two tokens trading places.
func describeRankDiff(light, dark []int) string {
	var moved []int
	for i := range light {
		if light[i] != dark[i] {
			moved = append(moved, light[i])
		}
	}
	names := make([]string, len(moved))
	for i, tok := range moved {
		names[i] = design.ChartName(tok)
	}
	if len(moved) == 2 {
		return names[0] + " and " + names[1] + " swap"
	}
	return strings.Join(names, ", ") + " move"
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// rankEvidence prints the contrasts behind a disputed rank, so the inversion
// can be fixed at the colour.
func rankEvidence(s [2]design.Scheme, light, dark []int) string {
	var b strings.Builder
	lc, dc := s[0].Contrasts(), s[1].Contrasts()
	for i := 1; i <= design.ChartTokens; i++ {
		fmt.Fprintf(&b, "  %s: light %.2f:1 (rank %d), dark %.2f:1 (rank %d)\n",
			design.ChartName(i), lc[i-1], indexOf(light, i)+1, dc[i-1], indexOf(dark, i)+1)
	}
	return strings.TrimRight(b.String(), "\n")
}

// The ruler's own fixtures. These values come from outside this palette —
// published OKLab conversions and WCAG contrast pairs — so a fault in the ruler
// is distinguishable from a fault in the palette.

func TestOKLabFixtures(t *testing.T) {
	// Ottosson's published sRGB -> OKLab conversions.
	cases := []struct {
		hex  string
		want design.OKLab
	}{
		{"#ffffff", design.OKLab{L: 1.0, A: 0.0, B: 0.0}},
		{"#000000", design.OKLab{L: 0.0, A: 0.0, B: 0.0}},
		{"#ff0000", design.OKLab{L: 0.6279554, A: 0.2248631, B: 0.1258463}},
		{"#00ff00", design.OKLab{L: 0.8664396, A: -0.2338876, B: 0.1794985}},
		{"#0000ff", design.OKLab{L: 0.4520137, A: -0.0324463, B: -0.3115281}},
	}
	const tol = 1e-4
	for _, tc := range cases {
		c, err := design.ParseHex(tc.hex)
		if err != nil {
			t.Fatalf("ParseHex(%s): %v", tc.hex, err)
		}
		got := c.OKLab()
		if math.Abs(got.L-tc.want.L) > tol || math.Abs(got.A-tc.want.A) > tol || math.Abs(got.B-tc.want.B) > tol {
			t.Errorf("OKLab(%s) = (%.7f, %.7f, %.7f), want (%.7f, %.7f, %.7f)",
				tc.hex, got.L, got.A, got.B, tc.want.L, tc.want.A, tc.want.B)
		}
	}
}

func TestContrastFixtures(t *testing.T) {
	// Published WCAG pairs: the extremes, and two greys on white that the
	// guidelines' own examples put at the AA and AAA thresholds.
	cases := []struct {
		a, b string
		want float64
		tol  float64
	}{
		{"#000000", "#ffffff", 21.0, 1e-9},
		{"#ffffff", "#ffffff", 1.0, 1e-9},
		{"#767676", "#ffffff", 4.54, 0.005},
		{"#595959", "#ffffff", 7.00, 0.005},
	}
	for _, tc := range cases {
		a, err := design.ParseHex(tc.a)
		if err != nil {
			t.Fatalf("ParseHex(%s): %v", tc.a, err)
		}
		b, err := design.ParseHex(tc.b)
		if err != nil {
			t.Fatalf("ParseHex(%s): %v", tc.b, err)
		}
		if got := design.ContrastRatio(a, b); math.Abs(got-tc.want) > tc.tol {
			t.Errorf("ContrastRatio(%s, %s) = %.4f, want %.2f", tc.a, tc.b, got, tc.want)
		}
		if got := design.ContrastRatio(b, a); math.Abs(got-tc.want) > tc.tol {
			t.Errorf("ContrastRatio(%s, %s) = %.4f, want %.2f (not symmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestSimulationFixtures(t *testing.T) {
	// The pinned matrices are normalised: every row sums to 1. A mistyped
	// constant breaks this before it breaks any palette measurement.
	for _, d := range design.CVDs {
		for i, row := range d.Matrix() {
			sum := row[0] + row[1] + row[2]
			if math.Abs(sum-1) > 2e-6 {
				t.Errorf("%s matrix row %d sums to %.6f, want 1", d, i, sum)
			}
		}
	}

	// Consequently a grey is a fixed point of all three simulations, and a
	// colour is zero distance from itself.
	grey, err := design.ParseHex("#808080")
	if err != nil {
		t.Fatalf("ParseHex: %v", err)
	}
	want := grey.Linear()
	for _, d := range design.CVDs {
		got := grey.Simulate(d)
		if math.Abs(got.R-want.R) > 1e-6 || math.Abs(got.G-want.G) > 1e-6 || math.Abs(got.B-want.B) > 1e-6 {
			t.Errorf("%s moves grey: (%.6f, %.6f, %.6f), want (%.6f, %.6f, %.6f)",
				d, got.R, got.G, got.B, want.R, want.G, want.B)
		}
		if e := design.DeltaE(grey, grey, d); e != 0 {
			t.Errorf("%s: dE of a colour against itself = %v, want 0", d, e)
		}
	}
}

func TestParseTokensRejectsMissingToken(t *testing.T) {
	const src = `:root {
  --color-surface: #ffffff;
  --color-chart-1: #2563eb;
  --color-chart-2: #8373bc;
  --color-chart-3: #046642;
  --color-chart-5: #e4307e;
}
@media (prefers-color-scheme: dark) {
  :root { --color-surface: #0f172a; }
}`
	_, _, err := design.ParseTokens(src)
	if err == nil {
		t.Fatal("ParseTokens accepted a palette missing --color-chart-4")
	}
	if !strings.Contains(err.Error(), "--color-chart-4") {
		t.Errorf("error does not name the missing token: %v", err)
	}
}

func TestParseTokensSeparatesSchemes(t *testing.T) {
	const src = `:root {
  /* Color */
  --color-surface: #ffffff;
  --color-chart-1: #111111;
  --color-chart-2: #222222;
  --color-chart-3: #333333;
  --color-chart-4: #444444;
  --color-chart-5: #555555;
}
@media (prefers-color-scheme: dark) {
  :root {
    --color-surface: #000000;
    --color-chart-1: #aaa;
    --color-chart-2: #bbbbbb;
    --color-chart-3: #cccccc;
    --color-chart-4: #dddddd;
    --color-chart-5: #eeeeee;
  }
}`
	light, dark, err := design.ParseTokens(src)
	if err != nil {
		t.Fatalf("ParseTokens: %v", err)
	}
	if got := light.Surface.Hex(); got != "#ffffff" {
		t.Errorf("light surface = %s, want #ffffff", got)
	}
	if got := dark.Surface.Hex(); got != "#000000" {
		t.Errorf("dark surface = %s, want #000000", got)
	}
	if got := light.Chart[0].Hex(); got != "#111111" {
		t.Errorf("light chart-1 = %s, want #111111", got)
	}
	if got := dark.Chart[0].Hex(); got != "#aaaaaa" {
		t.Errorf("dark chart-1 = %s, want #aaaaaa (from shorthand)", got)
	}
}

// The doc's two measurement tables are generated output: run
// `go test ./internal/design/... -update` to rewrite them. Hand-maintained
// measurements drift from the values they describe.

const (
	salienceHeader   = "| Token | Light contrast | Rank | Dark contrast | Rank |"
	separationHeader = "| Pair | Light | Dark |"
)

func TestDocTablesAreGenerated(t *testing.T) {
	s := schemes(t)
	raw, err := os.ReadFile(tokensDoc)
	if err != nil {
		t.Fatalf("read %s: %v", tokensDoc, err)
	}
	doc := string(raw)

	for _, table := range []struct{ header, body string }{
		{salienceHeader, salienceTable(s)},
		{separationHeader, separationTable(s)},
	} {
		doc, err = replaceTable(doc, table.header, table.body)
		if err != nil {
			t.Fatalf("%s: %v", tokensDoc, err)
		}
	}

	if doc == string(raw) {
		return
	}
	if *update {
		if err := os.WriteFile(tokensDoc, []byte(doc), 0o644); err != nil {
			t.Fatalf("write %s: %v", tokensDoc, err)
		}
		t.Logf("rewrote the generated tables in %s", tokensDoc)
		return
	}
	t.Errorf("%s tables disagree with the measured values; re-run with -update\n%s",
		tokensDoc, lineDiff(string(raw), doc))
}

func salienceTable(s [2]design.Scheme) string {
	lc, dc := s[0].Contrasts(), s[1].Contrasts()
	lr, dr := lc.Ranks(), dc.Ranks()
	var b strings.Builder
	b.WriteString(salienceHeader + "\n|---|---|---|---|---|\n")
	for i := 1; i <= design.ChartTokens; i++ {
		fmt.Fprintf(&b, "| `%s` | %.2f:1 | %d | %.2f:1 | %d |\n",
			design.ChartToken(i), lc[i-1], lr[i-1], dc[i-1], dr[i-1])
	}
	return strings.TrimRight(b.String(), "\n")
}

func separationTable(s [2]design.Scheme) string {
	var b strings.Builder
	b.WriteString(separationHeader + "\n|---|---|---|\n")
	for _, p := range design.Pairs() {
		cell := func(sc design.Scheme) string {
			e, at := design.WorstDeltaE(sc.Chart[p[0]-1], sc.Chart[p[1]-1])
			return fmt.Sprintf("%.1f (%s)", e, at.Short())
		}
		fmt.Fprintf(&b, "| %d\u2013%d | %s | %s |\n", p[0], p[1], cell(s[0]), cell(s[1]))
	}
	return strings.TrimRight(b.String(), "\n")
}

// replaceTable swaps the contiguous run of table lines starting at header for
// body, leaving the surrounding prose untouched.
func replaceTable(doc, header, body string) (string, error) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			if start >= 0 {
				return "", fmt.Errorf("table header %q appears more than once", header)
			}
			start = i
		}
	}
	if start < 0 {
		return "", fmt.Errorf("table header %q not found", header)
	}
	end := start
	for end < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[end]), "|") {
		end++
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, strings.Split(body, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

func lineDiff(before, after string) string {
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	var out strings.Builder
	for i := 0; i < len(b) && i < len(a); i++ {
		if b[i] != a[i] {
			fmt.Fprintf(&out, "  line %d:\n    have %s\n    want %s\n", i+1, b[i], a[i])
		}
	}
	return strings.TrimRight(out.String(), "\n")
}
