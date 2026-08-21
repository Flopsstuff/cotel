# ADR 0016 — Overview: an activity grid, one cell per bucket

**Date:** 2026-08-21
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

The Overview charts activity as a line over time. A line answers *how much, and
when* — it does not answer *what does a week here look like*. Rhythm is the
question a telemetry front door gets asked most: which hours are dead, whether
the weekend is quiet, whether the nightly bot really runs nightly. A line at 200
px tall over 30 buckets flattens all of it.

The board asked for a GitHub-contributions-style block of cells on the Overview,
counting spans, with a grid per range:

| Range | Grid asked for | Cell |
|---|---|---|
| Year | 53 × 7 | day |
| Month | 31 × 6 | 4 hours |
| Week | 27 × 7 | hour |
| Day | 24 × 6 | 10 minutes |

`All` keeps the year grid. Exact cell counts and the intensity ramp were left to
engineering ("количество и яркость сам подбери").

Three of those four are already coherent: **columns are the coarse unit, rows the
subdivision of it.** 53 weeks × 7 weekdays is GitHub's own layout; 31 days × 6
four-hour slots and 24 hours × 6 ten-minute slots are the same idea one and two
zoom levels in. Every one of them tiles its window exactly — 371 days, 31 days,
24 hours.

Two things had to be decided before any of it could be drawn: what the week grid
actually is, and where the data comes from.

## Options considered

### The week grid

27 columns of hour cells over 7 rows is 189 hours — 7.875 days. It tiles neither
a week nor a day, so no column is a fixed hour and no row is a fixed day; the
grid would drift by three hours per row and mean nothing at either axis.

**1. 7 × 24 — one column per day, 24 hour-rows.** The only layout that keeps
"columns coarse, rows fine" for the week too. It is also 24 rows tall: a 430 px
column of cells in a block whose other three ranges are ~115 px, so the page
jumps by a third of a screen when the range changes.

**2. 24 × 7 — one column per hour of the day, one row per day (chosen).** 168
cells, exactly the 7-day window. Same wide, short shape as the other three
grids. It is the transpose of option 1, so on this grid alone time runs *across*
a row rather than down a column — and it is the layout the History page's
hour-of-day heatmap already uses, so it is not a new idiom in the product, just
a second appearance of one. It is also what the board's `27 × 7` was reaching
for: seven rows, a day each.

### Where the cells come from

**A. Fold client-side from the hourly series.** `/history` already serves
`hour`. The month grid could fetch 744 hourly buckets and sum each four; the day
grid cannot be built at all, because nothing below an hour exists. Rejected on
the second count alone.

**B. A dedicated `GET /activity?range=…` returning the grid.** The server would
own the grid shape. Rejected: a new public contract, forever, for a bucket width
— and it would duplicate the window, user-filter and roll-up-union logic
`/history` already carries. *(Lens: schema and public interfaces are forever.)*

**C. Two more bucket widths on `/history` (chosen).** `10m` and `4h` join
`hour`, `day`, `week`, `month`. `/history` *is* this product's "spans over time,
bucketed" contract; ADR-0014 closed with "the next one is a parameter, not a
design", and this is that. The grid then asks for exactly the width it draws:
one bucket, one cell, no client-side re-bucketing to get wrong.

## Decision

**A `Span activity` block leads the Overview**, directly under the KPI row and
above the resource sections. It is the page's at-a-glance pulse; the sections
below it are the itemised answers.

**One cell is one `/history` bucket.** The range picks both the grid and the
granularity it fetches, and nothing else in the page knows the mapping:

| Range | Grid | Cell | `granularity` | Window tiled |
|---|---|---|---|---|
| `year`, `all` | 53 × 7, column-major | day | `day` | 371 days |
| `month` | 31 × 6, column-major | 4 h | `4h` | 31 days |
| `week` | 24 × 7, row-major | 1 h | `hour` | 7 days |
| `day` | 24 × 6, column-major | 10 min | `10m` | 24 hours |

On `year` and `all` that is the identical request the History block already
makes, so SWR serves both blocks from one fetch. The other three ranges cost one
extra `/history` call, over a window of at most 31 days of raw spans.

**Sub-day widths stay raw-only, like `hour`.** `daily_usage` buckets whole UTC
days, so `10m` and `4h` are answered from `spans` alone and report the shortfall
in `covered_since` — the rule ADR-0014 set, extended to two more widths rather
than re-argued. Only the day-celled year grid crosses the union, and it is the
one grid that needs to.

**The grid is placed in UTC.** `CAST(start_time AS TIMESTAMP)` renders the
stored `TIMESTAMPTZ` in UTC whatever the server's timezone is — the same
property the roll-up depends on for a day to be a day — so every cell start
falls on a bucket boundary by construction and the client never has to reconcile
two notions of midnight. The footer says `UTC` and every tooltip repeats it.

**A cell outside the queried window is drawn absent, not empty.** The lattice
is a fixed 53 × 7 and the window is a rolling 365 days, so the leading edge and
the rest of today are cells nothing was ever asked about. They are drawn as an
outline with no fill, distinct from the empty-but-covered colour, and carry no
tooltip. An empty cell means "we looked and there was nothing"; an outline means
"we did not look". Conflating the two is how a heatmap invents a quiet weekend.

**The ramp is cut at the quartiles of the cells in view, in five steps, shared
with the History page.** Scaling against the maximum is the obvious choice and
it was the first one built; it renders as one flat block of full-intensity
cells. Against a 722-span peak a 200-span day and a 700-span day are both
"busy" — on a linear ramp and on a log ramp alike, since the log only
compresses the top harder — and that difference is the entire point of the
grid. Quartiles put about a quarter of the busy cells in each step whatever the
shape of the distribution, which is what GitHub's graph does and why it reads.
The cost is that a step means a rank, not an amount: the footer therefore names
the busiest cell in view, and every tooltip gives the cell's own count. Degenerate
input is handled explicitly — a run with no spread has no quartiles to cut at,
so every busy cell takes the top step rather than all landing in the bottom one.

`frontend/src/lib/heat.ts` holds the scale that the History calendar and
hour-of-day heatmaps had a private copy of, so the three grids in the product
cannot drift apart. Four filled steps plus empty is about as many as the eye
separates at this cell size. Colours stay on `--color-chart-1` over
`--color-surface-2`: no new tokens, and the existing light/dark pair carries
over unchanged.

## Consequences

- `GET /history` takes `granularity=10m` and `granularity=4h`. Both are additive
  and every existing caller is untouched; an unrecognised width still falls back
  to `day` rather than 400ing.
- The Overview makes one extra request on the `month`, `week` and `day` ranges,
  and none on `year` / `all`.
- The block holds one height (~115 px of cells) across all four ranges, so
  switching range does not move the page under the reader.
- The week grid reads across, the other three read down. Both axes are labelled
  on every grid, which is what actually resolves it for a reader; the
  inconsistency is deliberate and is the price of keeping one block shape.
- The heat scale moves out of `History.tsx`. Any future cell grid gets it by
  importing it, and a change to the ramp lands on every grid at once — including
  the two History heatmaps, which pick up the quartile cut with this change.
- The grid shows a maximum of 371 days on `all`, however far back the data goes.
  A fixed lattice cannot grow unbounded, and the History page is one click away
  for the full series.
- Cells are `<div>`s in one CSS grid with explicit `gridColumn` / `gridRow`, at
  most 371 of them. Placement is explicit rather than flow-ordered so the same
  code renders both the column-major and the row-major grids.
