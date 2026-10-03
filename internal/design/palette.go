// Package design is the pinned measurement contract for the chart palette.
//
// The constants here are normative, not descriptive: ADR-0017 pins them so that
// "contrast" and "ΔE" mean exactly one thing for this project. ΔE is Euclidean
// distance in OKLab x 100 after simulating colour-vision deficiency with the
// Machado 2009 severity-1.0 matrices applied to linearised sRGB, and a pair
// scores the minimum over the three deficiencies. Changing a constant here
// changes what the palette rules mean, and is an edit to that ADR.
//
// Nothing imports this package. It exists so the contract has an address; the
// assertions live in the test alongside it.
package design

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ChartTokens is the length of the chart palette: --color-chart-1..5.
const ChartTokens = 5

// The two bars. ContrastFloor is rule 1's salience floor against the scheme's
// surface; DeltaEBar is rule 2's separation bar, set by ADR-0015.
const (
	ContrastFloor = 4.0
	DeltaEBar     = 8.0
)

// Color is a non-linear sRGB colour with components in [0,1].
type Color struct {
	R, G, B float64
}

// LinearRGB is an sRGB colour with the transfer function removed. Simulation
// output can fall outside [0,1]; it is not clamped, because clamping would
// quietly move a measurement.
type LinearRGB struct {
	R, G, B float64
}

// OKLab is a colour in Björn Ottosson's OKLab space.
type OKLab struct {
	L, A, B float64
}

// ParseHex reads a #rgb or #rrggbb CSS hex colour.
func ParseHex(s string) (Color, error) {
	h := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return Color{}, fmt.Errorf("not a hex colour: %q", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("not a hex colour: %q", s)
	}
	return Color{
		R: float64(v>>16&0xff) / 255,
		G: float64(v>>8&0xff) / 255,
		B: float64(v&0xff) / 255,
	}, nil
}

// Hex renders the colour back as #rrggbb, for failure messages.
func (c Color) Hex() string {
	b := func(v float64) int { return int(math.Round(v * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", b(c.R), b(c.G), b(c.B))
}

// Linear removes the sRGB transfer function.
func (c Color) Linear() LinearRGB {
	return LinearRGB{R: linearize(c.R), G: linearize(c.G), B: linearize(c.B)}
}

func linearize(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// Luminance is WCAG 2.x relative luminance.
func (c Color) Luminance() float64 {
	l := c.Linear()
	return 0.2126*l.R + 0.7152*l.G + 0.0722*l.B
}

// ContrastRatio is the WCAG 2.x contrast ratio between two colours, >= 1.
func ContrastRatio(a, b Color) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// CVD is one of the three simulated colour-vision deficiencies.
type CVD int

const (
	Protanopia CVD = iota
	Deuteranopia
	Tritanopia
)

// CVDs is the set a pair is scored over, in the order the doc tables name them.
var CVDs = [...]CVD{Protanopia, Deuteranopia, Tritanopia}

func (d CVD) String() string {
	switch d {
	case Protanopia:
		return "protanopia"
	case Deuteranopia:
		return "deuteranopia"
	case Tritanopia:
		return "tritanopia"
	}
	return fmt.Sprintf("CVD(%d)", int(d))
}

// Short is the abbreviation used in the generated doc tables.
func (d CVD) Short() string {
	switch d {
	case Protanopia:
		return "protan"
	case Deuteranopia:
		return "deutan"
	case Tritanopia:
		return "tritan"
	}
	return d.String()
}

// Machado 2009 severity-1.0 matrices, pinned by ADR-0017. They act on
// linearised sRGB. Each row sums to 1, so greys are fixed points.
var cvdMatrix = [3][3][3]float64{
	Protanopia: {
		{0.152286, 1.052583, -0.204868},
		{0.114503, 0.786281, 0.099216},
		{-0.003882, -0.048116, 1.051998},
	},
	Deuteranopia: {
		{0.367322, 0.860646, -0.227968},
		{0.280085, 0.672501, 0.047413},
		{-0.011820, 0.042940, 0.968881},
	},
	Tritanopia: {
		{1.255528, -0.076749, -0.178779},
		{-0.078411, 0.930809, 0.147602},
		{0.004733, 0.691367, 0.303900},
	},
}

// Matrix returns the pinned simulation matrix, so a fixture can check it.
func (d CVD) Matrix() [3][3]float64 {
	return cvdMatrix[d]
}

// Simulate applies the pinned matrix in linear space.
func (c Color) Simulate(d CVD) LinearRGB {
	l := c.Linear()
	m := cvdMatrix[d]
	return LinearRGB{
		R: m[0][0]*l.R + m[0][1]*l.G + m[0][2]*l.B,
		G: m[1][0]*l.R + m[1][1]*l.G + m[1][2]*l.B,
		B: m[2][0]*l.R + m[2][1]*l.G + m[2][2]*l.B,
	}
}

// OKLab converts linear sRGB to OKLab.
func (l LinearRGB) OKLab() OKLab {
	lo := 0.4122214708*l.R + 0.5363325363*l.G + 0.0514459929*l.B
	mo := 0.2119034982*l.R + 0.6806995451*l.G + 0.1073969566*l.B
	so := 0.0883024619*l.R + 0.2817188376*l.G + 0.6299787005*l.B
	// math.Cbrt keeps the sign: a simulated colour can leave the sRGB cube, and
	// the odd continuation is the meaningful reading of a negative cone response.
	l_, m_, s_ := math.Cbrt(lo), math.Cbrt(mo), math.Cbrt(so)
	return OKLab{
		L: 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_,
		A: 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_,
		B: 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_,
	}
}

// OKLab converts a non-linear sRGB colour to OKLab.
func (c Color) OKLab() OKLab {
	return c.Linear().OKLab()
}

// Distance is the Euclidean distance between two OKLab colours.
func (o OKLab) Distance(p OKLab) float64 {
	dl, da, db := o.L-p.L, o.A-p.A, o.B-p.B
	return math.Sqrt(dl*dl + da*da + db*db)
}

// DeltaE scores a pair under one deficiency: OKLab distance x 100.
func DeltaE(a, b Color, d CVD) float64 {
	return 100 * a.Simulate(d).OKLab().Distance(b.Simulate(d).OKLab())
}

// WorstDeltaE scores a pair as the minimum over the three deficiencies, and
// names the one that produced it. The worst case is what has to clear the bar.
func WorstDeltaE(a, b Color) (float64, CVD) {
	worst, at := math.Inf(1), CVDs[0]
	for _, d := range CVDs {
		if e := DeltaE(a, b, d); e < worst {
			worst, at = e, d
		}
	}
	return worst, at
}

// Scheme is one colour scheme's chart palette and the surface it is drawn on.
type Scheme struct {
	Name    string
	Surface Color
	Chart   [ChartTokens]Color
}

// ChartToken names the CSS custom property for a 1-based chart index.
func ChartToken(i int) string {
	return fmt.Sprintf("--color-chart-%d", i)
}

// ChartName is the short form used in failure messages.
func ChartName(i int) string {
	return fmt.Sprintf("chart-%d", i)
}

// Pairs lists every chart-token index pair, in doc-table order.
func Pairs() [][2]int {
	var out [][2]int
	for a := 1; a <= ChartTokens; a++ {
		for b := a + 1; b <= ChartTokens; b++ {
			out = append(out, [2]int{a, b})
		}
	}
	return out
}

// Contrasts is each chart token's contrast against the scheme's surface,
// indexed from zero for chart-1.
type Contrasts [ChartTokens]float64

// Contrasts measures rule 1's salience for the scheme.
func (s Scheme) Contrasts() Contrasts {
	var out Contrasts
	for i, c := range s.Chart {
		out[i] = ContrastRatio(c, s.Surface)
	}
	return out
}

// Order lists the 1-based token indices by descending contrast — the salience
// rank order rule 1 requires to agree across schemes. Equal contrasts order by
// index, so the result is deterministic.
func (c Contrasts) Order() []int {
	idx := make([]int, 0, ChartTokens)
	for i := range c {
		idx = append(idx, i+1)
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := c[idx[a]-1], c[idx[b]-1]
		if ca == cb {
			return idx[a] < idx[b]
		}
		return ca > cb
	})
	return idx
}

// Ranks is the inverse of Order: rank per token, 1 being the loudest.
func (c Contrasts) Ranks() [ChartTokens]int {
	var out [ChartTokens]int
	for pos, token := range c.Order() {
		out[token-1] = pos + 1
	}
	return out
}

// ParseTokens reads the light and dark chart palettes out of tokens.css. The
// top-level :root block is light; the one nested in the prefers-color-scheme:
// dark media query is dark. A token that is absent or unparseable is an error,
// never a skipped measurement.
func ParseTokens(src string) (light, dark Scheme, err error) {
	const darkAt = "@media (prefers-color-scheme: dark)"
	i := strings.Index(src, darkAt)
	if i < 0 {
		return light, dark, fmt.Errorf("no %q block", darkAt)
	}
	media, after, err := braceBlock(src[i:])
	if err != nil {
		return light, dark, fmt.Errorf("dark media query: %w", err)
	}
	// Light comes from the source with the dark media query excised, not from
	// the first :root in the file: a dark block declared above the top-level
	// :root would otherwise be measured as both schemes, leaving the light
	// palette unmeasured and the cross-scheme rank rule trivially satisfied.
	lightDecls, err := rootBlock(src[:i] + src[i+after:])
	if err != nil {
		return light, dark, fmt.Errorf("light :root: %w", err)
	}
	darkDecls, err := rootBlock(media)
	if err != nil {
		return light, dark, fmt.Errorf("dark :root: %w", err)
	}
	if light, err = newScheme("light", lightDecls); err != nil {
		return light, dark, err
	}
	dark, err = newScheme("dark", darkDecls)
	return light, dark, err
}

func newScheme(name string, decls map[string]string) (Scheme, error) {
	s := Scheme{Name: name}
	get := func(token string) (Color, error) {
		raw, ok := decls[token]
		if !ok {
			return Color{}, fmt.Errorf("%s scheme: %s is not declared", name, token)
		}
		c, err := ParseHex(raw)
		if err != nil {
			return Color{}, fmt.Errorf("%s scheme: %s: %w", name, token, err)
		}
		return c, nil
	}
	var err error
	if s.Surface, err = get("--color-surface"); err != nil {
		return s, err
	}
	for i := 1; i <= ChartTokens; i++ {
		if s.Chart[i-1], err = get(ChartToken(i)); err != nil {
			return s, err
		}
	}
	return s, nil
}

// rootBlock returns the custom properties declared in the first :root block.
func rootBlock(src string) (map[string]string, error) {
	i := strings.Index(src, ":root")
	if i < 0 {
		return nil, fmt.Errorf("no :root block")
	}
	body, _, err := braceBlock(src[i:])
	if err != nil {
		return nil, err
	}
	return declarations(body), nil
}

// braceBlock returns the contents of the first brace-balanced block in src,
// and the offset just past its closing brace.
func braceBlock(src string) (string, int, error) {
	open := strings.Index(src, "{")
	if open < 0 {
		return "", 0, fmt.Errorf("no opening brace")
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return src[open+1 : i], i + 1, nil
			}
		}
	}
	return "", 0, fmt.Errorf("unbalanced braces")
}

func declarations(body string) map[string]string {
	out := map[string]string{}
	for _, decl := range strings.Split(stripComments(body), ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "--") {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

func stripComments(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+j+2:]
	}
}
