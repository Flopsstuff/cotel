# cotel Page Specs

> Layout sketches and annotated field lists for each of the 6 dashboard pages.  
> All component names reference [components.md](components.md). All tokens reference [tokens.md](tokens.md).
>
> Status: v1 — covers Overview, Sessions, Session Detail, Costs, Tools, Models.  
> Min supported viewport: **1280px** wide.
>
> Design lens: Shneiderman "Overview first, zoom and filter, details on demand" throughout.

---

## Global shell

All pages share this shell. The shell renders before any page data loads.

```
┌──────────────────────────────────────────────────────────────────────────┐
│  cotel  (sidebar brand)                                                  │
├──────────────────────────────────────────────────────────────────────────┤
│ SIDE │                                                                   │
│  BAR │   [Page content — see each page spec below]                      │
│      │                                                                   │
│  nav │                                                                   │
│items │                                                                   │
│      │                                                                   │
└──────┴───────────────────────────────────────────────────────────────────┘
```

### Shell layout

| Element | Spec |
|---|---|
| Layout | CSS grid: `160px auto` columns |
| Sidebar | `<SidebarNav>` — fixed height 100vh, sticky |
| Main area | `padding: --space-8`, `overflow-y: auto`, `min-width: 0` |
| Breakpoint | At ≤768px: sidebar collapses to 48px (icon-only mode) |
| Min viewport | 1280px wide (dashboard context; no mobile table layout needed) |

### Global controls

The date range picker (`<DateRangePicker>`) and refresh indicator (`<RefreshIndicator>`) live at the top-right of the main area, not in the sidebar. They appear on every page in the same position.

```
Main area top-right:
  [  30d  ▾  ]   [●]
```

These are rendered by a shared `<PageHeader>` component that wraps every page's title and controls:

```
┌─────────────────────────────────────────────────────────────┐
│  Overview                         [  30d  ▾  ]  [●]        │
└─────────────────────────────────────────────────────────────┘
```

---

## 1. Overview  (`/`)

**Purpose:** Landing page. Single-screen summary of the period. Entry point to every other page.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Overview                              [  30d  ▾  ]  [●]     │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │                                                              │
│      │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐       │
│      │  │Sessions │  │  Cost   │  │  Input  │  │ Output  │       │
│      │  │  142    │  │ $47.23  │  │  2.1M   │  │  890K   │       │
│      │  │↑12 (9%) │  │↑8% prev │  │         │  │         │       │
│      │  └─────────┘  └─────────┘  └─────────┘  └─────────┘       │
│      │                                                              │
│      │  Daily cost ─────────────────────────────────────────────   │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │  $4.00 ┤                        ▐▌                    │ │
│      │  │  $2.00 ┤  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  │ │
│      │  │  $0.00 └───────────────────────────────────────────── │ │
│      │  └────────────────────────────────────────────────────────┘ │
│      │                                                              │
│      │  Recent sessions                                            │
│      │  ┌──────────────────────────────────────────────────────┐  │
│      │  │ Session ID  Started     Duration  Model       Cost   │  │
│      │  │ sess_abc…   2h ago      5m 12s    sonnet-4-6  $0.024 │  │
│      │  │ sess_def…   4h ago      12m 47s   opus-4-7    $0.187 │  │
│      │  │ sess_ghi…   Yesterday   2m 03s    haiku-4-5   $0.003 │  │
│      │  │ sess_jkl…   Yesterday   8m 31s    sonnet-4-6  $0.051 │  │
│      │  │ sess_mno…   2 days ago  15m 22s   sonnet-4-6  $0.094 │  │
│      │  └──────────────────────────────────────────────────────┘  │
│      │                              → View all sessions            │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| KPI row | `<KpiCard>` | Sessions | `GET /overview → total_sessions` | Integer |
| KPI row | `<KpiCard>` | Total Cost | `GET /overview → total_cost_usd` | `$0.00` |
| KPI row | `<KpiCard>` | Input Tokens | `GET /overview → total_input_tokens` | `1.2M`, `890K`, `12.4K` |
| KPI row | `<KpiCard>` | Output Tokens | `GET /overview → total_output_tokens` | Same |
| KPI row (delta) | `<KpiCard>` | Delta vs prev | Compare `?since=N` vs `?since=2N&until=N` | `+8%`, `--` |
| Daily cost | `<ChartWrapper>` + Chart.js bar | Day labels | `GET /costs/timeseries` | x: `MMM D`, y: `$0.00` |
| Daily cost | — | Y-axis max | Derived: max(day_cost) × 1.2 | Rounded to nearest `$1` |
| Recent sessions | `<DataTable>` | Session ID | `GET /sessions?limit=5&sort=started_at:desc` | Monospace, truncated to 16 chars + `…` |
| Recent sessions | `<DataTable>` | Started | `started_at` | Relative time (`2h ago`, `Yesterday`) via `date-fns.formatDistanceToNow` |
| Recent sessions | `<DataTable>` | Duration | `duration_ms` | `Xm Ys` — minutes + seconds |
| Recent sessions | `<DataTable>` | Model | `model` | `<StatBadge variant="neutral">` |
| Recent sessions | `<DataTable>` | Cost | `total_cost_usd` | `$0.000` (3dp) |
| "View all" link | Plain `<a>` | — | Navigates to `/sessions` | Right-aligned below table |

### Layout notes

- KPI row: `display: flex`, `gap: --space-3`, `flex-wrap: wrap`, `margin-bottom: --space-6`.
- Daily cost chart: height `200px`, fills available width.
- Recent sessions table: non-sortable (fixed: most recent first), non-paginated, no filter bar.
- "View all sessions" link: right-aligned, `--text-sm`, `--color-accent`.

### Shipped section order

The page stacks six `<StatSection>` blocks, each a summary of one resource with a
"View all" link to its full page, in this order:

1. **Users** — top 5 by spend in the range. Hidden while the page is scoped to a
   single user via `?user_id=`, where a top-5-users table would be the one panel
   on the page not answering for that user.
2. **History** — activity area chart. `hour` granularity on the `Day` range,
   `day` otherwise.
3. **Costs** — daily spend line. No inner by-model table: the Models block below
   is the same data at full width.
4. **Tools** — top 5 by call count.
5. **Models** — all models by span count.
6. **Sessions** — 5 most recent. Last, because it is the only block that cannot
   honour a long range (see `covered_since` in the API reference).

The header carries one `<SegmentedControl>` bound to `RANGE_OPTIONS` and
persisted in the `cotel_overview_range` cookie — its own key, so changing the
range here does not move the Users or Tools page. KPI labels take their suffix
from `RANGE_SUFFIX` (`All` renders none)
([ADR-0014](../decisions/0014-overview-single-range-selector.md)).

---

## 2. Sessions  (`/sessions`)

**Purpose:** Full session list with filter and sort. Entry point to session drill-down.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Sessions                              [  30d  ▾  ]  [●]     │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │                                                              │
│      │  [Model: All ▾]  [Search session ID…    🔍]                 │
│      │                                                              │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │ Session ID↕  Started↕  Duration↕  Model  Cost↕  Spans │ │
│      │  │ sess_abc…    May9 14:32  5m 12s   s-4-6  $0.024   47  │ │
│      │  │ sess_def…    May9 12:15  12m 47s  o-4-7  $0.187   182 │ │
│      │  │ sess_ghi…    May8 09:01  2m 03s   h-4-5  $0.003   12  │ │
│      │  │ …                                                      │ │
│      │  └────────────────────────────────────────────────────────┘ │
│      │                                                              │
│      │  [← Prev]  1  [2]  3  4  [Next →]   Page 2 of 14           │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| Filter bar | `<select>` | Model filter | `GET /sessions/models` (distinct values) | Dropdown: "All", then model names |
| Filter bar | `<input>` | Session ID search | Client-side prefix match or `?q=` | Free text |
| Table | `<DataTable>` | Session ID | `session_id` | Monospace, 16ch + `…`, click → `/sessions/:id` |
| Table | `<DataTable>` | Started | `started_at` | `MMM D, HH:mm` (absolute on this page) |
| Table | `<DataTable>` | Duration | `duration_ms` | `Xm Ys` |
| Table | `<DataTable>` | Model | `model` | `<StatBadge variant="neutral">` |
| Table | `<DataTable>` | Cost | `total_cost_usd` | `$0.000`, right-aligned |
| Table | `<DataTable>` | Spans | `span_count` | Integer, right-aligned |
| Rows | `<DataTable>` | Error indicator | `has_error: true` | Row background: `--color-danger-bg` |
| Pagination | `<Pagination>` | Page / total | `GET /sessions` response metadata | `page`, `total_pages` |

### Sort defaults

- Initial sort: `started_at DESC` (most recent first).
- Sortable columns: Session ID, Started, Duration, Cost.
- Model and Spans are not sortable in v1.

### Filter behavior

- Model filter: URL param `?model=sonnet-4-6`. "All" clears the param.
- Search: `?q=<prefix>`. Applied server-side; minimum 3 characters before triggering.
- Both filters combine with `AND` semantics.
- Filters + sort state persist in URL.

### Row click

Click any row → navigate to `/sessions/:session_id`.

---

## 3. Session Detail  (`/sessions/:session_id`)

**Purpose:** Full breakdown of a single session: header metadata, span timeline, cost summary, raw attributes.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Sessions / sess_abc123defg456h                              │
│      │  ← Breadcrumb                                                │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │  Session  sess_abc123defg456h…                               │
│      │                                                              │
│      │  ┌─────────────────────────────────────────────────────┐    │
│      │  │ Started       Duration    Model        Cost         │    │
│      │  │ May 9, 14:32  5m 12s      sonnet-4-6   $0.024       │    │
│      │  └─────────────────────────────────────────────────────┘    │
│      │                                                              │
│      │  COST + TOKENS ─────────────────────────────────────────    │
│      │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│      │  │ Total    │  │ Input    │  │ Output   │  │ Spans    │   │
│      │  │ $0.024   │  │ 12,480   │  │ 4,230    │  │ 47       │   │
│      │  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
│      │                                                              │
│      │  SPANS ──────────────────────────────────────────────────   │
│      │  ┌──────────────────────────────────────────────────────┐   │
│      │  │ Name↕     Type    Duration↕  Tokens  Cost   Status   │   │
│      │  │ session   root    5m 12s      —       —      ✓        │   │
│      │  │ turn-1    llm     2.1s       8,240   $0.018  ✓        │   │
│      │  │ bash-1    tool    0.8s        —       —      ✓        │   │
│      │  │ turn-2    llm     1.4s       8,470   $0.006  ✓        │   │
│      │  │ bash-2    tool    0.2s        —       —      ✗ error  │   │
│      │  └──────────────────────────────────────────────────────┘   │
│      │                                                              │
│      │  ▸ RAW ATTRIBUTES (collapsible)                             │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| Breadcrumb | `<Breadcrumb>` | "Sessions" link | — | Links to `/sessions` |
| Breadcrumb | `<Breadcrumb>` | Current session | `session_id` | Truncated: 24ch max |
| Session header | `<InfoGrid>` | Started | `started_at` | `MMM D, YYYY, HH:mm:ss` |
| Session header | `<InfoGrid>` | Duration | `duration_ms` | `Xm Ys` |
| Session header | `<InfoGrid>` | Model | `model` | `<StatBadge variant="neutral">` |
| Session header | `<InfoGrid>` | Cost | `total_cost_usd` | `$0.000` |
| KPI row | `<KpiCard>` | Total Cost | Aggregated from spans | `$0.000` |
| KPI row | `<KpiCard>` | Input Tokens | `SUM(input_tokens)` | Integer with `,` separators |
| KPI row | `<KpiCard>` | Output Tokens | `SUM(output_tokens)` | Integer with `,` separators |
| KPI row | `<KpiCard>` | Spans | `COUNT(spans)` | Integer |
| Span table | `<DataTable>` | Name | `span.name` | Monospace, truncated |
| Span table | `<DataTable>` | Type | `span.kind` or derived | `<StatBadge>`: `llm` accent, `tool` neutral, `root` neutral |
| Span table | `<DataTable>` | Duration | `end_time - start_time` | `X.Xs` for <60s, `Xm Ys` for ≥60s |
| Span table | `<DataTable>` | Tokens | `input_tokens + output_tokens` | Integer or `—` for tool spans |
| Span table | `<DataTable>` | Cost | `cost_usd` | `$0.000` or `—` for tool spans |
| Span table | `<DataTable>` | Status | `status_code` | `✓` in `--color-success`, `✗ error` in `--color-danger` |
| Error rows | `<DataTable>` | Error rows | `status_code == ERROR` | Row background `--color-danger-bg` |
| Raw attributes | `<details>`/`<summary>` | JSON blob | `span.attributes` | Collapsible; monospace, `--text-mono-sm`, `--color-text-2` |

### Span table sort

- Initial sort: `start_time ASC` (chronological timeline).
- Sortable: Name, Duration, Tokens, Cost.

### Raw attributes section

```
▸ Raw attributes   ← <summary>, click to expand
  {
    "session_id": "sess_abc123…",
    "tool_name": "bash",
    "exit_code": 1,
    …
  }
```

- Rendered with `white-space: pre`, `--font-mono`, `--text-mono-sm`.
- Background: `--color-surface-2`, padding `--space-3`, border-radius `--radius-base`.
- Each span has its own collapsible attributes block, shown inline in the row as an expandable sub-row, or as a separate section below the table. v1: single combined JSON block for the whole session.

---

## 4. Costs  (`/costs`)

**Purpose:** Financial breakdown — daily trend, model split, top sessions by spend.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Costs                                 [  30d  ▾  ]  [●]     │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │                                                              │
│      │  ┌─────────────────────────────────────────────────────┐    │
│      │  │  Total cost: $47.23   (+8% vs prev 30d)             │    │  ← headline card, full-width
│      │  └─────────────────────────────────────────────────────┘    │
│      │                                                              │
│      │  Daily cost ──────────────────────────────────────────────  │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │  $4.00 ┤                        ▐▌                    │ │
│      │  │  $2.00 ┤  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  │ │
│      │  │  $0.00 └───────────────────────────────────────────── │ │
│      │  └────────────────────────────────────────────────────────┘ │
│      │                                                              │
│      │  By model ──────────────────  Top sessions ────────────     │
│      │  ┌───────────────────────────┐  ┌────────────────────────┐  │
│      │  │ Model      Sessions  Cost │  │ Session ID     Cost    │  │
│      │  │ sonnet-4-6  98       $31  │  │ sess_def…      $0.187  │  │
│      │  │ opus-4-7    12       $14  │  │ sess_xyz…      $0.154  │  │
│      │  │ haiku-4-5   32       $2   │  │ sess_abc…      $0.024  │  │
│      │  └───────────────────────────┘  └────────────────────────┘  │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| Headline | Full-width `<KpiCard>` | Total cost | `GET /overview → total_cost_usd` | `$0.00` (2dp) |
| Headline | `<KpiCard>` | Delta | Compare prev period | `+8% vs prev 30d` |
| Daily cost | `<ChartWrapper>` + Chart.js bar | Day / cost | `GET /costs/timeseries` | x: `MMM D`, y: `$0.00` |
| Daily cost | — | Hover tooltip | Chart.js tooltip | `May 9: $3.47` |
| By model | `<DataTable>` | Model | `GET /costs/by-model → model` | `<StatBadge variant="neutral">` |
| By model | `<DataTable>` | Sessions | `session_count` | Integer |
| By model | `<DataTable>` | Cost | `total_cost_usd` | `$0.00`, right-aligned, sorted desc |
| By model | `<DataTable>` | % of total | Derived | `30%` in `--color-text-2`, right-aligned |
| Top sessions | `<DataTable>` | Session ID | `GET /costs/by-session?limit=10` | Monospace, links to `/sessions/:id` |
| Top sessions | `<DataTable>` | Cost | `total_cost_usd` | `$0.000`, right-aligned |

### Layout

Two-column grid for "By model" and "Top sessions":  
`display: grid; grid-template-columns: 1fr 1fr; gap: --space-6;`  
At <900px: stack to single column.

---

## 5. Tools  (`/tools`)

**Purpose:** Tool usage analytics — call volume, duration, failure rate, trend.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Tools                                 [  30d  ▾  ]  [●]     │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │                                                              │
│      │  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│      │  │  Calls   │  │ Avg Dur  │  │  Fail %  │                  │
│      │  │  1,842   │  │  0.8s    │  │  2.3%    │                  │
│      │  └──────────┘  └──────────┘  └──────────┘                  │
│      │                                                              │
│      │  Tool calls by day ──────────────────────────────────────── │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │  (multi-series bar: top 3 tools, stacked or grouped)   │ │
│      │  └────────────────────────────────────────────────────────┘ │
│      │                                                              │
│      │  Tool breakdown ───────────────────────────────────────────  │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │ Tool name↕   Calls↕  Avg dur↕  Fail %↕  Usage bar     │ │
│      │  │ Bash         942     0.9s       1.2%     ████████████  │ │
│      │  │ Read          441     0.1s       0.0%     █████         │ │
│      │  │ Edit          220     0.2s       0.5%     ██▌           │ │
│      │  │ Grep          180     0.3s       0.0%     ██            │ │
│      │  │ Write          59     0.1s       0.0%     ▌             │ │
│      │  └────────────────────────────────────────────────────────┘ │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| KPI row | `<KpiCard>` | Total calls | `GET /tools/stats → total_calls` | Integer with `,` |
| KPI row | `<KpiCard>` | Avg duration | `GET /tools/stats → avg_duration_ms` | `0.8s`, `1.2m` |
| KPI row | `<KpiCard>` | Fail % | `failed_calls / total_calls` | `2.3%` |
| Trend chart | `<ChartWrapper>` + Chart.js bar | Day / tool / count | `GET /tools/timeseries` | Multi-series, top 3 tools; others grouped as "Other" |
| Breakdown | `<DataTable>` | Tool name | `tool_name` | Monospace |
| Breakdown | `<DataTable>` | Calls | `call_count` | Integer, right-aligned, sorted desc by default |
| Breakdown | `<DataTable>` | Avg duration | `avg_duration_ms` | `0.9s` |
| Breakdown | `<DataTable>` | Fail % | `fail_rate` | `1.2%`, colored `--color-danger` if >5% |
| Breakdown | `<DataTable>` | Usage bar | `call_count / max_call_count` | CSS bar: `--color-accent` fill, proportional width, max 100px wide |

### Fail % threshold coloring

- < 1%: `--color-text-1` (default)
- 1–5%: `--color-warning`
- > 5%: `--color-danger`

This uses preattentive attributes (color) to surface anomalies per Tufte / Von Restorff.

### Trend chart — multi-series config

```ts
// Chart.js dataset config (example)
datasets: tools.slice(0, 3).map((tool, i) => ({
  label: tool.name,
  data: tool.dailyCounts,
  backgroundColor: `var(--color-chart-${i + 1})`,
}))
```

Show legend for multi-series tool chart (override Chart.js default). Position: top.

---

## 6. Models  (`/models`)

**Purpose:** Model-level analytics — cost, token usage, session count, and model comparison.

> This page is net-new (not in the prior IA doc). Add `Models` as the 5th nav item in `<SidebarNav>` after `Tools`.

```
┌──────┬──────────────────────────────────────────────────────────────┐
│      │  Models                                [  30d  ▾  ]  [●]     │
│      ├──────────────────────────────────────────────────────────────┤
│ nav  │                                                              │
│      │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│      │  │ Sessions │  │ Cost     │  │ In Tokens│  │Out Tokens│   │
│      │  │  142     │  │ $47.23   │  │  2.1M    │  │  890K    │   │
│      │  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
│      │                                                              │
│      │  Cost by model ─────────────────────────────────────────── │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │  (horizontal bar chart: model → cost, sorted by cost)  │ │
│      │  └────────────────────────────────────────────────────────┘ │
│      │                                                              │
│      │  Model breakdown ──────────────────────────────────────────  │
│      │  ┌────────────────────────────────────────────────────────┐ │
│      │  │ Model↕     Sessions↕  Cost↕   Input↕   Output↕  $/tok │ │
│      │  │ sonnet-4-6  98        $31.20  1.4M      620K    $0.022 │ │
│      │  │ opus-4-7    12        $14.10  480K      200K    $0.029 │ │
│      │  │ haiku-4-5   32        $1.93   220K       70K    $0.009 │ │
│      │  └────────────────────────────────────────────────────────┘ │
└──────┴──────────────────────────────────────────────────────────────┘
```

### Annotated fields

| Region | Component | Field | Source | Format |
|---|---|---|---|---|
| KPI row | `<KpiCard>` | Sessions | Rollup across all models | Integer |
| KPI row | `<KpiCard>` | Cost | Rollup | `$0.00` |
| KPI row | `<KpiCard>` | Input Tokens | Rollup | `1.2M` |
| KPI row | `<KpiCard>` | Output Tokens | Rollup | `890K` |
| Cost chart | `<ChartWrapper>` + Chart.js horizontal bar | Model / cost | `GET /costs/by-model` | y: model name, x: cost `$0.00`; sorted desc by cost |
| Breakdown | `<DataTable>` | Model | `model` | `<StatBadge variant="neutral">` |
| Breakdown | `<DataTable>` | Sessions | `session_count` | Integer |
| Breakdown | `<DataTable>` | Cost | `total_cost_usd` | `$0.00`, right-aligned |
| Breakdown | `<DataTable>` | Input Tokens | `total_input_tokens` | `1.4M` |
| Breakdown | `<DataTable>` | Output Tokens | `total_output_tokens` | `620K` |
| Breakdown | `<DataTable>` | $/1K tokens | `total_cost_usd / (total_tokens / 1000)` | `$0.000` |

### Default sort

Cost descending. Identifies the most expensive model at a glance (Goal-Gradient / Serial Position).

### Cost chart — horizontal bar

Use Chart.js `indexAxis: 'y'` for horizontal bars. This lets model names read cleanly on the y-axis without rotation.  
Color: `--color-chart-1` through `--color-chart-5` in order of cost rank.

### API requirement

`GET /costs/by-model` must return `input_tokens`, `output_tokens`, `session_count` per model — flag for CTO if not present in current backend.

---

## Interaction notes

### Auto-refresh

- **Default:** no auto-refresh. Data loads on page load and filter change.
- **Manual refresh:** clicking the `<RefreshIndicator>` dot triggers a refetch of all data on the current page.
- **Stale threshold:** if `lastFetchedAt` is > 5 minutes ago, the dot switches to `stale` state.
- **v1 scope:** no polling, no WebSocket/SSE. This matches the Pi-friendly constraint (no background timers draining resources).
- **Future (v2):** optional auto-refresh interval (e.g. every 60s) configured in settings.

### Sort and filter pattern

- All filter and sort state lives in the URL query string.
- No client-side hidden state for filters. Pasting the URL recreates the exact view (deep linkable).
- Filter changes trigger a new `GET` request; results replace current data (no merge).
- Loading state: `<LoadingSkeleton>` replaces data region while refetching.
- Debounce: search input waits 400ms (Doherty Threshold) before triggering fetch.

### Tooltip trigger

- **Charts:** hover on data point. Chart.js handles this natively.
- **Truncated table cells:** hover triggers a CSS tooltip after 200ms delay. No click-to-reveal.
- **No touch tooltip fallback in v1.** Min viewport is 1280px (desktop context).

### Breakpoint

- **Min supported viewport:** 1280px wide.
- **Sidebar collapse breakpoint:** 768px (icon-only sidebar).
- **Two-column → single-column breakpoint:** 900px (Costs page breakdown tables).
- No mobile-first design required for v1; cotel is a developer dashboard, used on desktop.
- Future: responsive table patterns (horizontal scroll wrapper with sticky first column) if mobile access is needed.

### Number formatting

| Value type | Format | Example |
|---|---|---|
| Currency | `$0.000` (3dp for costs < $1), `$0.00` (2dp for > $1) | `$0.024`, `$47.23` |
| Large integers | Compact: `K`, `M` (1 decimal) | `1.4M`, `890K`, `12K` |
| Duration < 60s | `X.Xs` | `0.8s`, `2.4s` |
| Duration ≥ 60s | `Xm Ys` | `5m 12s`, `12m 47s` |
| Percentages | `0.0%` (1dp) | `1.2%`, `2.3%` |
| Relative time | `date-fns.formatDistanceToNow` | `2h ago`, `Yesterday`, `3 days ago` |

---

## API endpoints required

Summary of endpoints needed for all 6 pages. Flag gaps to CTO before FLO-34 starts.

| Endpoint | Pages | Status |
|---|---|---|
| `GET /overview` | Overview, Models | Exists (refine: add token rollups) |
| `GET /sessions?page=&limit=&model=&q=&sort=&since=` | Sessions, Overview | New |
| `GET /sessions/:id` | Session Detail | New |
| `GET /sessions/models` | Sessions filter | New (distinct model list) |
| `GET /costs/timeseries?since=` | Overview, Costs | New |
| `GET /costs/by-model?since=` | Costs, Models | New |
| `GET /costs/by-session?since=&limit=` | Costs | New |
| `GET /tools/stats?since=` | Tools | New |
| `GET /tools/timeseries?since=` | Tools trend chart | New |
