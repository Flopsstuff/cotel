# ADR 0017 — The chart-palette ruler is pinned, and a Go test holds it

**Date:** 2026-10-03
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

`docs/design/tokens.md` states two measurable rules for the chart palette — a
salience rank that must agree across schemes with a 4:1 floor, and ΔE ≥ 8
separation for every pair under simulated colour-vision deficiency. The palette
had shipped violating both for months: `--color-chart-1` and `--color-chart-2`
were ΔE 0.3 apart under deuteranopia, the pair every two-series chart draws,
while the doc claimed the palette carried identity in colour. Nothing measured
it. The re-derived palette (PR #105) fixes both, and nothing measures that
either.

A guard is obvious. The trap is less so. "ΔE" is only defined relative to an
implementation, and the margins the re-derived palette holds are thin — the
tightest pair clears the bar by 0.4, the contrast floor by 0.08. Reimplementing
the ruler from the prose in `tokens.md` reproduces the recorded table only
partly: an independent implementation (linear-space Machado 2009 at severity
1.0, OKLab, worst case over the three CVDs) agrees to the cent on every cell
whose worst case is protanopia or deuteranopia, and diverges on every cell whose
worst case is tritanopia — 1–2 light reads 11.4 (protan) against a recorded 8.8
(tritan), 1–4 light 31.6 against 24.5. The rank and contrast columns reproduce
exactly, and those are the two columns that were independently re-checked at
review time; the ΔE column has had one tool behind it.

So the divergence is large enough to move a cell across the bar. A guard that
re-derives the ruler from prose is therefore not a guard: it can fail CI on a
palette the doc certifies as passing, and the cheapest way out of that failure
is to delete the test.

The cause was identified after this ADR was first accepted, and it was not the
matrices: both tools use these same constants in linear space. The derivation
tool behind the recorded table clamped the simulated colour back into the sRGB
cube before converting to OKLab. Re-adding that one step to an independent
implementation reproduces the recorded table on all twenty cells and all twenty
CVD labels. The diagnosis above — that the divergence is confined to tritanopia
— was therefore wrong: `1–5` light is a protanopia cell and it moved too, 17.6
against 18.3, which is what rules a matrix difference out. Tritanopia merely
dominated the symptom, because the tritan matrix is the one that leaves the cube
often and far: across both schemes it takes 5 of 10 tokens out of gamut, against
2 for protanopia and 1 for deuteranopia. The Decision below pins the gamut
policy, which is the degree of freedom that was never written down.

## Options considered

1. **Assert the numbers recorded in `tokens.md`.** Cheapest, and wrong: it
   pins the output of one unrecorded tool as ground truth and still cannot say
   what a new value measures.
2. **Re-derive the ruler in the test from the prose.** What the task implies.
   Rejected: it reintroduces exactly the ambiguity above, and whichever tritan
   variant the implementer picks becomes a silent, unrecorded contract.
3. **Pin the ruler's constants normatively, and let the test carry them.** The
   ruler stops being a description of a measurement and becomes the
   measurement. Chosen.
4. Pin, and additionally generate the doc's tables from the test. Chosen as a
   follow-on consequence, not a separate option.

## Decision

**The ruler is a pinned numeric contract, not a prose recipe.** Its constants
live in `internal/design/palette.go` and are normative: linear-space Machado
2009 severity-1.0 matrices applied to linearised sRGB, the OKLab forward
transform, and WCAG 2.x relative luminance. ΔE is Euclidean distance in OKLab
× 100; a pair's score is the **minimum** over protanopia, deuteranopia and
tritanopia, because the worst case is the one that has to clear the bar.

```
protanopia   0.152286  1.052583 -0.204868   deuteranopia  0.367322  0.860646 -0.227968
             0.114503  0.786281  0.099216                 0.280085  0.672501  0.047413
            -0.003882 -0.048116  1.051998                -0.011820  0.042940  0.968881

tritanopia   1.255528 -0.076749 -0.178779
            -0.078411  0.930809  0.147602
             0.004733  0.691367  0.303900
```

**The simulated colour is not clamped to the sRGB gamut.** The matrix product
is carried into OKLab as-is, and the cube root takes the signed continuation
for a negative cone response. This clause is as normative as the matrices, and
for the same reason: it is a free parameter that changes the numbers. Clamping
is a display concession — out-of-gamut output means the percept is not
displayable on this monitor, not that two percepts have moved closer together,
and folding two of them onto a cube face books a loss of separation the reader
still perceives. It is also not conservative but merely noisy: clamping lowers
eight of the twenty cells and *raises* `1–3` dark, so its bias has no
consistent sign. On the binding pair it is the more generous of the two
candidate rulers (8.56 clamped against 8.41 pinned), and a gate should not be
the more generous one on the pair with the least room.

The bar stays ADR-0015's ΔE ≥ 8; the contrast floor stays 4:1. A Go test in
`internal/design` parses `frontend/src/styles/tokens.css`, measures both
schemes, and fails CI when either rule breaks. Go, because `go test ./...`
already runs in CI and the frontend has no test runner — adding one to assert
sixty lines of arithmetic would buy a toolchain to maintain and nothing else
(*boring tech*, *keep the deployment story trivial*).

The ruler itself is tested against values that do not come from this palette —
published OKLab conversions and WCAG contrast pairs — so a fault in the ruler
is distinguishable from a fault in the palette. A measurement contract with no
fixtures is just an opinion with a build step.

**`tokens.md`'s two tables become generated output**, written by the test under
`-update` rather than typed. Hand-maintained measurements drift from the values
they describe: before PR #105 the dark rank column disagreed with the contrast
values printed beside it in the same row, transposing `chart-1` and `chart-5`,
and the prose conclusion drawn from it ("the dark order is the light order
reversed") was true only of the transposed column. Generating the tables retires
that whole class of defect.

Where the pinned ruler and the recorded table disagree, **the pinned ruler
wins and the table is regenerated.** This is safe to do now and only now:
under the pinned ruler every cell of the current palette still passes — worst
pair 8.4, contrast floor 4.08:1, ranks identical across schemes — so
regenerating restates numbers without moving any of them across a bar. Adopting
the pinned ruler is a relabelling today and a real constraint on the next
palette change.

## Consequences

- A palette change that breaks either rule fails CI with the offending pair or
  rank inversion and both measurements named, so it can be fixed at the value
  rather than at the test.
- The divergence was reconciled rather than left to decree, and the constants
  survived it unchanged: the disagreement was the gamut clamp, now pinned
  above, and the recorded table main carries is the correct one. The ΔE column
  has since been reproduced by two further independent implementations that
  agree with it on all twenty cells.
- There is one ruler and it has an address. A palette proposal should be
  measured with `internal/design`'s own `ParseHex`, `DeltaE`, `WorstDeltaE` and
  `ParseTokens` rather than a fresh script, because a second implementation is
  what opened this question — and the gamut policy shows that getting every
  published constant right is not sufficient to land on the same number.
- The margins are documented as thin. The next palette proposal should treat
  8.4 and 4.08:1 as "passing, with no room", not as headroom.
- `internal/design` carries no production consumer. It is imported by nothing
  and adds nothing to the binary; it exists so the contract has an address.
