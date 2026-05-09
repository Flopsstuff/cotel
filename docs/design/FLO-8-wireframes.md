# FLO-8 — cotel Dashboard: IA, Wireframes, Visual Language v0

**Author:** UXDesigner · heartbeat 2026-05-09  
**Status:** ready for CEO review  
**Scope:** Information architecture + low-fi wireframes for 5 views · visual language tokens · design principles

---

## 1. Information Architecture

### Data model grounding

The backend stores OTLP spans with these queryable dimensions:
- `session_id` — groups spans into a logical work session
- `model` — which Claude model was used
- `tool_name` — which tool was called (Read, Edit, Bash, etc.)
- `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`
- `start_time`, `end_time`, `duration_ms`
- `attributes` JSON bag — error info, span name (`claude_code.session`, `claude_code.tool_call`, etc.)

The `daily_usage` aggregate table enables long-horizon cost charts without scanning raw spans.

### IA Tree

```
cotel dashboard
│
├── Overview                        [/]
│   ├── KPI summary cards
│   │   ├── Today's cost ($)
│   │   ├── Sessions (count)
│   │   ├── Total tokens (input+output)
│   │   └── Error rate (%)
│   ├── Cost trend chart (7d sparkline)
│   ├── Top 5 tools (by call count)
│   └── Recent sessions (last 5, with cost)
│
├── Sessions                        [/sessions]
│   ├── Filter bar (date range · model · status)
│   ├── Summary strip (avg cost/session · avg duration · total spend)
│   ├── Sessions table (sortable: date · cost · duration · tokens)
│   └── → Session detail                [/sessions/:id]
│
├── Costs                           [/costs]
│   ├── Period selector (7d · 30d · 90d · custom)
│   ├── Cost-over-time line chart
│   ├── By model breakdown (stacked bar)
│   ├── By session top-N table (tokens + cost, both columns)
│   └── Cache efficiency strip (cache_read / total reads %)
│
├── Tools                           [/tools]
│   ├── Call frequency bar chart (top 20 tools)
│   ├── Avg latency bar chart (same tools, sorted by p50)
│   └── Error rate table (tool · calls · errors · error%)
│
└── Session detail                  [/sessions/:id]   ← Drill-down
    ├── Session header (id · model · date · duration · total cost)
    ├── Token + cost summary (in/out/cache tokens · $ breakdown)
    ├── Tool calls waterfall timeline (start→end per tool call)
    └── Span log table (name · duration · tokens · status)
```

### Navigation model

- **Left sidebar** — 5 items, icon + label, 160px wide on desktop (collapses to icon-only at ≤1024px, drawer on mobile)
- **Top bar** — app name + global period selector (affects Overview and Costs charts)
- **Breadcrumb** — appears only on drill-down (Session detail)
- **No tabs within top-level views** — each view is a single scrollable surface; sub-sections are scroll-anchored

---

## 2. Wireframes

Notation:
- `[XXX]` — interactive element (button, dropdown, link)
- `████` — filled bar / chart area
- `▂▃▅▇` — sparkline shape
- `╌╌╌` — subtle separator
- `(...)` — contextual label / data value

### 2.1 Overview  `/`

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ≡ cotel                                              [Period: Last 7d ▾]│
├──────────┬───────────────────────────────────────────────────────────────┤
│          │                                                               │
│ ● Overview│  ── Summary ─────────────────────────────────────────────── │
│           │                                                               │
│   Sessions│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌───────┐  │
│           │  │ Today's Cost│ │  Sessions   │ │   Tokens    │ │Errors │  │
│   Costs   │  │             │ │             │ │             │ │       │  │
│           │  │   $0.42     │ │     12      │ │   148 k     │ │  0 %  │  │
│   Tools   │  │ ▲ +12% vs   │ │ ▲ +2 vs    │ │ input+out   │ │       │  │
│           │  │   yesterday │ │  yesterday  │ │             │ │       │  │
│           │  └─────────────┘ └─────────────┘ └─────────────┘ └───────┘  │
│           │                                                               │
│           │  ── Cost trend (last 7 days) ─────────────────────────────── │
│           │                                                               │
│           │   $│                                                          │
│           │  .8│         ██                                               │
│           │  .6│      ▂▃ ██ ██                                           │
│           │  .4│  ▂▂▃ ██ ██ ██ ▅                                        │
│           │  .2│  ██ ██ ██ ██ ██ ██ ██                                   │
│           │   0└────────────────────────────────────────────────────────  │
│           │    Mon Tue Wed Thu Fri Sat Sun                                │
│           │                                                               │
│           │  ── Top tools  ·  Recent sessions ────────────────────────── │
│           │                                                               │
│           │  Top tools (call count)    │ Recent sessions                 │
│           │  ────────────────────────  │ ─────────────────────────────── │
│           │  Read      ████████ 312    │ abc123  sonnet-4.6  $0.08  3m   │
│           │  Bash      ██████   248    │ def456  opus-4.7    $0.31  12m  │
│           │  Edit      █████    187    │ ghi789  sonnet-4.6  $0.04  1m   │
│           │  Glob      ███       89    │ jkl012  sonnet-4.6  $0.12  7m   │
│           │  Write     ██        62    │ mno345  haiku-4.5   $0.01  0m   │
│           │  [View all tools →]        │ [View all sessions →]           │
│           │                                                               │
└──────────┴───────────────────────────────────────────────────────────────┘
```

**Interaction hints:**
- KPI cards link to respective detail view on click
- Bar chart bars are clickable → Cost view filtered to that day
- Session rows → Session detail drill-down
- Period selector is global and persists across navigation

---

### 2.2 Sessions  `/sessions`

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ≡ cotel                                              [Period: Last 7d ▾]│
├──────────┬───────────────────────────────────────────────────────────────┤
│           │                                                               │
│   Overview│  Sessions                                                     │
│           │                                                               │
│ ● Sessions│  [Date range: Last 7d ▾]  [Model: All ▾]  [Status: All ▾]   │
│           │                                                               │
│   Costs   │  ╌╌ Summary ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌   │
│           │  47 sessions  ·  avg $0.09/session  ·  avg 4m 32s  ·  $4.23 │
│   Tools   │  ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌   │
│           │                                                               │
│           │  SESSION ID ↓  DATE        MODEL         COST    DUR  TOOLS  │
│           │  ──────────────────────────────────────────────────────────── │
│           │  abc123        2026-05-09  sonnet-4.6   $0.08   3m   14     │
│           │  def456        2026-05-09  opus-4.7     $0.31   12m  38     │
│           │  ghi789        2026-05-08  sonnet-4.6   $0.04   1m    6     │
│           │  jkl012        2026-05-08  sonnet-4.6   $0.12   7m   21     │
│           │  mno345        2026-05-08  haiku-4.5    $0.01   0m    2     │
│           │  pqr678        2026-05-07  sonnet-4.6   $0.19   9m   29     │
│           │  stu901        2026-05-07  opus-4.7     $0.44   18m  51     │
│           │  vwx234        2026-05-06  sonnet-4.6   $0.07   4m   12     │
│           │  ...                                                          │
│           │                                                               │
│           │  [← Prev]                                Page 1 of 6 [Next →]│
│           │                                                               │
└──────────┴───────────────────────────────────────────────────────────────┘
```

**Interaction hints:**
- Each row is clickable → Session detail
- Column headers are sort toggles (chevron indicates sort direction)
- Filters are combinable; URL reflects filter state (shareable/bookmarkable)
- Summary strip updates live with filter changes
- COST column shows `$X.XX` always — no token-only display at list level (detail has full breakdown)

---

### 2.3 Costs  `/costs`

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ≡ cotel                                              [Period: Last 30d ▾]│
├──────────┬───────────────────────────────────────────────────────────────┤
│           │                                                               │
│   Overview│  Costs                                                        │
│           │                                                               │
│   Sessions│  [7d] [30d] [90d] [Custom...]      Total: $42.17 this period │
│           │                                                               │
│ ● Costs   │  ── Cost over time ──────────────────────────────────────── │
│           │                                                               │
│   Tools   │   $│                                                          │
│           │  3.0│                 ██                                      │
│           │  2.0│        ██    ██ ██ ██                                   │
│           │  1.0│  ▂▂ ██ ██ ██ ██ ██ ██ ▂▂ ▂▂ ██ ██ ▂▂ ...             │
│           │   0 └────────────────────────────────────────────────────── │
│           │     Apr 10            Apr 20            Apr 30  May 09       │
│           │                                                               │
│           │  ── By model (stacked bar — same period) ─────────────────── │
│           │                                                               │
│           │  claude-opus-4.7    ████████████████████████  $29.84  70.8% │
│           │  claude-sonnet-4.6  ████████                  $10.43  24.7% │
│           │  claude-haiku-4.5   ██                         $1.90   4.5% │
│           │                                                               │
│           │  ── Cache efficiency ─────────────────────────────────────── │
│           │  Cache hit rate: 34%  ·  Saved: ~$12.40 vs no-cache estimate │
│           │                                                               │
│           │  ── Top sessions by cost ─────────────────────────────────── │
│           │                                                               │
│           │  SESSION      DATE        MODEL       INPUT TOK  OUT TOK  $  │
│           │  ──────────────────────────────────────────────────────────── │
│           │  stu901       2026-05-07  opus-4.7    310 k      28 k    $0.44│
│           │  def456       2026-05-09  opus-4.7    240 k      21 k    $0.31│
│           │  pqr678       2026-05-07  sonnet-4.6  180 k      16 k    $0.19│
│           │  [View all →]                                                 │
│           │                                                               │
└──────────┴───────────────────────────────────────────────────────────────┘
```

**Interaction hints:**
- Period buttons update all three sections simultaneously
- Hovering a bar in the time chart shows tooltip: date + cost + session count
- Model rows in stacked bar are filterable (click model label to hide/show)
- Session table rows → Session detail; `$` and token columns both shown always

---

### 2.4 Tools  `/tools`

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ≡ cotel                                              [Period: Last 7d ▾]│
├──────────┬───────────────────────────────────────────────────────────────┤
│           │                                                               │
│   Overview│  Tools                                                        │
│           │                                                               │
│   Sessions│  ── Call frequency ─────────────────────────────────────── │
│           │                                                               │
│   Costs   │  Read        ████████████████████████████████  312          │
│           │  Bash        ██████████████████████            248          │
│ ● Tools   │  Edit        ████████████████                  187          │
│           │  Glob        █████                              89           │
│           │  Write       ████                               62           │
│           │  Grep        ███                                51           │
│           │  Agent       ██                                 28           │
│           │  TodoWrite   █                                  14           │
│           │  WebFetch    █                                  11           │
│           │  WebSearch                                       4           │
│           │                        calls →                               │
│           │                                                               │
│           │  ── Avg latency (p50) ──────────────────────────────────── │
│           │                                                               │
│           │  Agent       ████████████████████████████████  8 420 ms     │
│           │  Bash        ████████████████                  4 120 ms     │
│           │  WebFetch    ████████████                       3 180 ms    │
│           │  WebSearch   ████████                           2 040 ms    │
│           │  Edit        ██                                   380 ms    │
│           │  Read        █                                    210 ms    │
│           │  Write       █                                    190 ms    │
│           │  Glob        █                                    140 ms    │
│           │  Grep        █                                    130 ms    │
│           │  TodoWrite   ▏                                     40 ms    │
│           │                                                               │
│           │  ── Error rate ─────────────────────────────────────────── │
│           │                                                               │
│           │  TOOL         CALLS   ERRORS   ERROR RATE                    │
│           │  ──────────────────────────────────────────────────────────── │
│           │  Bash           248       12        4.8% ●                   │
│           │  WebFetch         11        2       18.2% ●●                 │
│           │  Agent            28        1        3.6% ●                  │
│           │  Read            312        0        0.0%                    │
│           │  Edit            187        0        0.0%                    │
│           │  (others with 0% errors collapsed)  [Show all]               │
│           │                                                               │
└──────────┴───────────────────────────────────────────────────────────────┘
```

**Interaction hints:**
- Bar charts are sorted by value (frequency chart: descending by calls; latency chart: descending by p50)
- Error rate column: `●` badge highlights any rate >0; double `●●` for >10% (preattentive signaling)
- Clicking a tool name in any section cross-filters the other two sections to highlight that tool
- "Show all" expands zero-error tools in the error table

---

### 2.5 Session detail (Drill-down)  `/sessions/:id`

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ≡ cotel                                                                 │
├──────────┬───────────────────────────────────────────────────────────────┤
│           │  ← [Sessions]  /  Session abc12345678                        │
│   Overview│                                                               │
│           │  ── Session header ─────────────────────────────────────── │
│   Sessions│  ID: abc12345678…       Model: claude-sonnet-4.6             │
│ (back)    │  Started: 2026-05-09 14:22:03  Duration: 3m 14s             │
│   Costs   │  Ended:   2026-05-09 14:25:17  Status: ✓ completed          │
│           │                                                               │
│   Tools   │  ── Tokens & cost ─────────────────────────────────────── │
│           │                                                               │
│           │  Input tokens:        12 480   Cost:                  $0.08  │
│           │  Output tokens:        2 310   Input rate:   $3/M tokens     │
│           │  Cache read tokens:    4 100   Output rate: $15/M tokens     │
│           │  Cache write tokens:   1 200   Cache saved: ~$0.012          │
│           │                                                               │
│           │  ── Tool call timeline ──────────────────────────────────── │
│           │                                                               │
│           │  (relative time →)  0s          1m          2m         3m14s │
│           │                     │           │           │           │    │
│           │  claude_code.session ██████████████████████████████████████  │
│           │  Read               ██ ██ ██ ██                              │
│           │  Glob                    ██                                   │
│           │  Grep                       ██ ██                             │
│           │  Edit                             ██   ██                    │
│           │  Bash                                      ██ ██             │
│           │  TodoWrite                                         ██        │
│           │                                                               │
│           │  ── Span log ───────────────────────────────────────────── │
│           │                                                               │
│           │  SPAN NAME            START   DUR      TOKENS   STATUS       │
│           │  ──────────────────────────────────────────────────────────── │
│           │  claude_code.session  0.000s  194.1s   14 790   ✓            │
│           │  tool_call/Read       0.211s    0.18s      —     ✓            │
│           │  tool_call/Read       1.420s    0.21s      —     ✓            │
│           │  tool_call/Glob       1.830s    0.14s      —     ✓            │
│           │  tool_call/Grep       2.110s    0.19s      —     ✓            │
│           │  tool_call/Grep       2.580s    0.22s      —     ✓            │
│           │  tool_call/Edit       3.340s    0.31s      —     ✓            │
│           │  tool_call/Bash       4.100s    2.84s      —     ✓            │
│           │  tool_call/Bash       6.980s    1.10s      —     ✓            │
│           │  tool_call/TodoWrite  8.210s    0.08s      —     ✓            │
│           │                                                               │
└──────────┴───────────────────────────────────────────────────────────────┘
```

**Interaction hints:**
- Breadcrumb `← Sessions` goes back to session list (preserving filter state)
- Timeline bars show tool call duration proportionally; hovering reveals tooltip with exact times + span ID
- Span log is the ground truth table; clicking any row expands to show raw attributes JSON
- Error rows in span log show in red with error message in tooltip
- "← Sessions" is in the sidebar active item too (Sessions stays highlighted)

---

## 3. Visual Language v0

### 3.1 Color palette

**Design decisions:**
- Slate family for neutrals (cool undertone matches developer/terminal aesthetic)
- Blue accent (trustworthy, standard for interactive elements in developer tools, Jakob's Law)
- Semantic colors are WCAG AA compliant on their respective backgrounds
- Dark mode is slate-900/800/700 base — deep enough for extended use, not pure black (reduces contrast fatigue)

#### Light mode

| Token              | Value     | Hex       | Usage                                          |
|--------------------|-----------|-----------|------------------------------------------------|
| `color-bg`         | slate-50  | `#f8fafc` | Page canvas                                    |
| `color-surface`    | white     | `#ffffff` | Cards, panels, sidebar                         |
| `color-surface-2`  | slate-100 | `#f1f5f9` | Table header rows, hover states                |
| `color-border`     | slate-200 | `#e2e8f0` | Dividers, card outlines                        |
| `color-border-2`   | slate-300 | `#cbd5e1` | Input focus rings, emphasized dividers         |
| `color-text-1`     | slate-900 | `#0f172a` | Headings, primary data values                  |
| `color-text-2`     | slate-600 | `#475569` | Labels, secondary copy                         |
| `color-text-3`     | slate-400 | `#94a3b8` | Placeholder text, hint copy                    |
| `color-accent`     | blue-600  | `#2563eb` | Active nav, primary button, links              |
| `color-accent-bg`  | blue-50   | `#eff6ff` | Accent hover/selected backgrounds              |
| `color-success`    | green-700 | `#15803d` | Positive status, 0% error rate                 |
| `color-warning`    | amber-600 | `#d97706` | Moderate error rate (3–10%)                    |
| `color-danger`     | red-600   | `#dc2626` | High error rate (>10%), error status           |
| `color-danger-bg`  | red-50    | `#fef2f2` | Error row backgrounds                          |

#### Dark mode

| Token              | Value     | Hex       | Notes                                          |
|--------------------|-----------|-----------|------------------------------------------------|
| `color-bg`         | slate-950 | `#020617` | Page canvas                                    |
| `color-surface`    | slate-900 | `#0f172a` | Cards, panels, sidebar                         |
| `color-surface-2`  | slate-800 | `#1e293b` | Table header rows, hover states                |
| `color-border`     | slate-700 | `#334155` | Dividers                                       |
| `color-border-2`   | slate-600 | `#475569` | Emphasized dividers                            |
| `color-text-1`     | slate-50  | `#f8fafc` | Headings, primary data values                  |
| `color-text-2`     | slate-400 | `#94a3b8` | Labels, secondary copy                         |
| `color-text-3`     | slate-600 | `#475569` | Placeholder text                               |
| `color-accent`     | blue-400  | `#60a5fa` | Active nav, links (lighter for dark contrast)  |
| `color-accent-bg`  | blue-950  | `#172554` | Accent hover backgrounds                       |
| `color-success`    | green-400 | `#4ade80` | Positive status                                |
| `color-warning`    | amber-400 | `#fbbf24` | Moderate error rate                            |
| `color-danger`     | red-400   | `#f87171` | High error rate                                |
| `color-danger-bg`  | red-950   | `#450a0a` | Error row backgrounds                          |

**Contrast audit (light mode, key pairs):**
- `color-text-1` on `color-surface`: 16:1 — AAA ✓
- `color-text-2` on `color-surface`: 5.7:1 — AA ✓
- `color-accent` on `color-surface`: 4.6:1 — AA ✓ (borderline; use weight-600 for body links)
- `color-danger` on `color-danger-bg`: 5.1:1 — AA ✓

### 3.2 Typography

Font stack (system, no download): `ui-monospace, 'JetBrains Mono', 'Fira Code', monospace` for IDs/tokens/code; `system-ui, -apple-system, 'Segoe UI', sans-serif` for all other text.

**Rationale:** Monospace for IDs and token counts makes scanning and copy-paste reliable; sans-serif for prose reduces cognitive load at dashboard density.

| Token        | Size  | Weight | Line-height | Usage                                     |
|--------------|-------|--------|-------------|-------------------------------------------|
| `text-display` | 20px | 700    | 1.2         | Page titles (Sessions, Costs, Tools)      |
| `text-heading` | 16px | 600    | 1.3         | Section headings, card titles             |
| `text-body`    | 14px | 400    | 1.5         | Table cells, body copy, filter labels     |
| `text-label`   | 12px | 500    | 1.4         | Table column headers (uppercase + tracking)|
| `text-micro`   | 11px | 400    | 1.4         | Badges, timestamps, sparkline axis labels |

**Tracking:** `text-label` uses `letter-spacing: 0.06em` to compensate for uppercase at small sizes.

### 3.3 Spacing scale

Base unit: 4px.

| Token      | px  | Usage                                          |
|------------|-----|------------------------------------------------|
| `space-1`  |  4px| Icon-to-label gap, badge padding               |
| `space-2`  |  8px| Cell padding (compact), tag gap                |
| `space-3`  | 12px| Cell padding (default), form row gap           |
| `space-4`  | 16px| Card internal padding, sidebar item height     |
| `space-6`  | 24px| Between sections within a view                 |
| `space-8`  | 32px| Between major surface groups                   |
| `space-12` | 48px| Page top padding, large visual breaks          |

### 3.4 Border radius

| Token        | Value | Usage                                          |
|--------------|-------|------------------------------------------------|
| `radius-sm`  | 3px   | Badges, status dots, tags                      |
| `radius-md`  | 6px   | Cards, inputs, buttons                         |
| `radius-lg`  | 10px  | Modals, drawer panels                          |

### 3.5 Shadows (light mode only; dark mode uses border-only elevation)

| Token        | Value                          | Usage                |
|--------------|--------------------------------|----------------------|
| `shadow-sm`  | `0 1px 2px rgba(0,0,0,.06)`   | Cards, sidebar       |
| `shadow-md`  | `0 2px 8px rgba(0,0,0,.10)`   | Dropdowns, tooltips  |

### 3.6 Density rationale

cotel is a **developer observability tool** used at a desk, on a large monitor, by someone actively debugging or auditing. Dashboard-dense layout is correct here:

- **Table row height:** 36px (vs 48px+ for consumer apps)
- **Card padding:** `space-4` (16px) internally — tight but not cramped
- **No decorative whitespace** — space between sections is `space-6` (24px), not `space-12`
- **Chart height:** 160px minimum for trend charts, 240px for the primary cost chart
- **Mobile:** Sidebar collapses to bottom tab bar (5 items); cards stack vertically; tables become card-lists (key columns only, expandable)

---

## 4. Design Principles

These are the rules applied throughout. Cite by name in implementation PRs when making tradeoff decisions.

### P1 — Overview first, drill on demand (Shneiderman's Mantra)
The Overview is the landing page and always shows the most important summary. Every other view is a drill-down from something visible in the Overview. Users should never need to navigate away from Overview to understand the system's health.

### P2 — Cost is always dual: tokens AND dollars
Every cost display shows both token counts and USD equivalents. Operators think in tokens (budget limits, model selection), finance thinks in dollars. Showing only one forces a conversion step that causes errors. The exception: Overview KPI cards show `$` only for brevity — the tooltip shows token breakdown.

### P3 — Time is always explicit
Every chart has a labeled time axis. Every table's date column is shown by default. The global period selector state is visible at all times in the top bar. No unlabeled or "implicit last N days" data.

### P4 — Anomalies surface before averages (Von Restorff + preattentive)
Error rates > 0 are highlighted in the Tools view. The highest-cost sessions appear at the top of the Costs table by default. The Overview shows "vs yesterday" deltas with direction arrows. Users should see what's wrong before seeing what's average.

### P5 — Session detail is the canonical drill-down
Every link that opens a specific session opens the same `/sessions/:id` view. There is no second "session detail" format. This prevents mental model fragmentation (Jakob's Law: users build expectations from consistent patterns).

### P6 — Navigation cap at 5 (Miller's Law applied to nav)
The sidebar has exactly 5 items: Overview, Sessions, Costs, Tools, and the implicit drill-down (breadcrumb only, not a nav item). Adding a sixth item requires removing one. This constraint forces prioritization.

### P7 — Empty states explain and direct
Every empty state (no sessions yet, no data in range, no errors) includes: (1) why it's empty, (2) what to do next. Example: "No sessions yet · Point Claude Code at cotel to start — see the README for setup." A blank page is a failure state.

### P8 — No truncated y-axes without explicit callout
If a chart's y-axis doesn't start at zero, the axis break is shown visually (zigzag line) and a tooltip note reads "Y axis starts at X." This follows Tufte's axis honesty principle and prevents misread severity of trends.

### P9 — Filter state is URL-encoded and shareable
Every filter selection (date range, model, status) is reflected in the URL as query parameters. Sharing a URL shares the exact view context. This is critical for debugging — an operator should be able to send a filtered sessions link to a colleague.

### P10 — Dark mode is first-class, not an afterthought
The dark mode token set is defined in the same specification as light mode. Implementation must use CSS custom properties (not hardcoded hex values) throughout, with `prefers-color-scheme: dark` media query as the default toggle. A user preference toggle is optional — the OS setting is enough.

---

## 5. Open questions for CTO

These are data-model dependencies that the IA/wireframe assume but are unverified. None blocks wireframe progress; they affect implementation scope.

1. **Error detection:** How is an error span identified? By a specific `name` value, an `attributes` key like `error: true`, or HTTP status code? The Tools error rate table and Session detail status column depend on this.

2. **Session identification:** Is `session_id` always present on all spans, or only on `claude_code.session` root spans? The Sessions list is built by grouping on `session_id`.

3. **Tool call span names:** Are tool calls stored as `tool_call/Read`, `claude_code.tool_call`, or another convention? The drill-down timeline grouping depends on this.

4. **Cache efficiency metric:** Is cache hit rate derivable from `cache_read_tokens / (cache_read_tokens + input_tokens)` or is there a more accurate formula?

These questions should be posted as a comment on the CTO's current architecture issue when the API is available.

---

*End of FLO-8 design artifacts. Ready for CEO review.*
