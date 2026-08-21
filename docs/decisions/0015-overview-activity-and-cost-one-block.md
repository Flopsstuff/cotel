# ADR 0015 — Overview: spans and cost share one block, and one plot

**Date:** 2026-08-21
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

The Overview stacked two adjacent chart blocks, History and Costs, each holding a
single-series chart over the same window:

| Block | Endpoint | Series | Height |
|---|---|---|---|
| History | `GET /history` (`granularity=day`, or `hour` on the `day` range) | `spans` per bucket | 160 px |
| Costs | `GET /costs` | `cost_usd` per day | 140 px |

Two requests, two cards, two headers, two axes — for two series that answer the
same question at a glance: *how much did we do, and what did it cost?* Reading
the pair meant comparing the shape of one chart against the shape of another
card below it, from memory, with no shared x-position to sight along.

Worse, the two blocks did not always agree on their x-axis. `/history` buckets
hourly on the `day` range, `/costs` is daily-only, so on `Day` the top chart
showed 24 hourly points and the one below it showed a single daily point. And
the two answered different windows until ADR-0014 gave `/history` a `range` key —
before that, History stopped a month back while Costs beside it charted a year.

The board asked for one block with both series on one field in different colours.

## Options considered

**1. Keep two blocks, tighten them.** Cheapest, but does not answer the request
and leaves the `Day` mismatch in place.

**2. Client-side join of `/history` and `/costs`.** Merge the two payloads on the
date key in the page. Rejected: it re-derives an alignment the server already
guarantees, and it can only be correct when both endpoints happen to have
resolved the same window, the same user filter and the same bucket width. Three
things to keep in sync at the call site is three things to get wrong. *(Lens:
trust the boundary.)*

**3. One series from `/history` alone.** `historyBucket` already carries
`cost_usd` beside `spans` — the same row, from the same `spans ∪ daily_usage`
union at the same raw-floor split (ADR-0014). Both series are bucketed, windowed
and user-filtered identically **by construction**, and the Overview drops from
two requests to one. Chosen.

Given one dataset, the plot itself is the second decision: the two measures are
counts and dollars, three orders of magnitude apart. Plotted on one scale the
cost line is a flat trace on the axis floor.

- **3a. Two y-axes on one plot.** What the board asked for, and the shape the
  request only has as a single field.
- **3b. Index both to a common base (=100 at t0).** One honest axis, but an
  Overview panel exists to be read in absolute terms — "417 spans, $2.14" — and
  "137 vs. base" is not that.
- **3c. Two stacked plots sharing an x-axis inside one block.** Honest scales and
  sightable alignment, but two plots again.

## Decision

The Overview carries **one block, `Activity & Cost`**, fed by a single
`/history` call, plotting `spans` as a filled area against a left axis and
`cost_usd` as a line against a right axis (option 3 + 3a). The block header links
to both full pages, `History →` and `Costs →`, so neither is orphaned.

Colours are `--color-chart-1` (blue) for spans and `--color-chart-4` (amber) for
cost — the existing tokens, unchanged. The pair was validated rather than eyeballed:
CVD separation ΔE 32.3 protan / 29.3 tritan in light, 29.9 / 24.6 in dark, against
a target of ≥ 8.

A two-scale plot is a known way to mislead: the alignment of the scales is
arbitrary, so where the two lines cross means nothing, and a chart can invent a
correlation the data does not hold. We take it deliberately, and pay for it:

- The two series wear **different marks** — a filled area and a bare line — so
  the eye does not read them as two comparable lines with a meaningful crossing.
- The legend names the axis each series reads against, in words: `Spans (left
  axis)`, `Cost (right axis, USD)`. Identity is not carried by colour alone.
- The right axis is tick-formatted in dollars, the left in bare counts, so the
  two scales are self-labelling.
- Axis and legend text stay in ink tokens, not series colours.

The mitigation is honest about what it is. It makes the plot *readable*; it does
not make the scale alignment meaningful. If the board later wants the stronger
form, 3c is a contained change behind the same block.

## Consequences

- The Overview issues one fewer request per range change. `useCosts` is no longer
  called from the page; the Costs page is its only caller.
- Both series now stop and start together on every range, including `Day`, where
  cost is charted hourly for the first time.
- The `covered_since` note already shown for spans now covers cost too — the same
  buckets carry both, so there is one coverage statement instead of one stated
  and one silent.
- `StatSection` takes an optional `links` array beside `viewAllHref`; the single
  link stays the default for every other block.
- The README's Overview hero and its Overview bullet change shape; both are
  re-taken and rewritten with this change, not after it.
- The two-scale plot is a standing exception to the usual one-axis rule, recorded
  here so the next person to open the file finds the reasoning rather than
  re-deriving it — or "fixing" it into a flat line on one axis.
