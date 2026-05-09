# cotel — Wireframes (Low-fi, ASCII)

> UX design lenses applied: Shneiderman "Overview first, zoom and filter, details on demand";
> Tufte data-ink ratio; Cognitive Load minimization; Fitts's Law for interactive targets;
> Pi-friendly (no JS-heavy patterns).

---

## Global shell

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          Overview  |  Sessions  |  Costs  |  Tools   [30d ▾]    │
└─────────────────────────────────────────────────────────────────────────┘
```

- Sticky top bar, ~48px tall.
- Active section is **bold / underlined**.
- Time range picker is right-aligned in the bar; applies globally on change (GET with `?since=`).
- No hamburger menu — 4 items always visible.

---

## 1. Overview  (`/`)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          [Overview]  Sessions  |  Costs  |  Tools    [30d ▾]    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐  ┌──────────┐│
│  │   Sessions    │  │  Total Cost   │  │  Input Tokens │  │  Output  ││
│  │     142       │  │    $47.23     │  │    2.1 M      │  │  890 K   ││
│  │ +12 vs prev   │  │  +8% vs prev  │  │               │  │          ││
│  └───────────────┘  └───────────────┘  └───────────────┘  └──────────┘│
│                                                                         │
│  Daily cost  ─────────────────────────────────────────────────────────  │
│  $4.00 ┤                              ▐▌                               │
│  $2.00 ┤  ▐▌    ▐▌   ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  ▐▌  │
│  $0.00 └──────────────────────────────────────────────────────────────  │
│         Apr 9                                                   May 9   │
│                                                                         │
│  Recent sessions                                                        │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Session ID     Started      Duration    Model         Cost        │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ sess_abc123    2h ago       5m 12s      sonnet-4-6    $0.024      │ │
│  │ sess_def456    4h ago       12m 47s     opus-4-7      $0.187      │ │
│  │ sess_ghi789    Yesterday    2m 03s      haiku-4-5     $0.003      │ │
│  │ sess_jkl012    Yesterday    8m 31s      sonnet-4-6    $0.051      │ │
│  │ sess_mno345    2 days ago   15m 22s     sonnet-4-6    $0.094      │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                    → View all sessions  │
└─────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**
- 4 metric cards: sessions, cost, input tokens, output tokens. Miller's Law — 4 ≤ 7±2.
- Delta vs previous period ("+12%") surfaces anomalies (Von Restorff).
- Daily cost chart is the primary visual; no pie charts (Tufte: data-ink).
- Recent sessions table = 5 rows. Not paginated here — cognitive load low.
- "View all sessions" link → `/sessions`.

---

## 2. Sessions list  (`/sessions`)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          Overview  |  [Sessions]  |  Costs  |  Tools  [30d ▾]   │
├─────────────────────────────────────────────────────────────────────────┤
│  Sessions                                                               │
│                                                                         │
│  [Model: All ▾]  [Date: Last 30d ▾]  [Search session ID…         🔍]  │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Session ID     Started            Duration    Model       Cost    │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ sess_abc123    May 9, 14:32       5m 12s      sonnet-4-6  $0.024  │ │
│  │ sess_def456    May 9, 12:15       12m 47s     opus-4-7    $0.187  │ │
│  │ sess_ghi789    May 8, 22:01       2m 03s      haiku-4-5   $0.003  │ │
│  │ sess_jkl012    May 8, 18:44       8m 31s      sonnet-4-6  $0.051  │ │
│  │ sess_mno345    May 7, 09:12       15m 22s     sonnet-4-6  $0.094  │ │
│  │ sess_pqr678    May 7, 08:33       3m 07s      haiku-4-5   $0.004  │ │
│  │ ...            ...                ...         ...         ...     │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  Showing 1–20 of 142          [← Previous]  Page 1 of 8  [Next →]     │
└─────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**
- Filters above table (Progressive Disclosure — only show what's needed).
- Model filter: values from existing `model` column; "All" default.
- Date range: matches global time range picker but can be overridden per-session.
- Row click → Session Detail. Whole row is the Fitts target (large hit area).
- Sorted newest first (default). Column headers are clickable to re-sort (future).
- No status filter in MVP (active vs completed needs backend support — flagged in IA).

---

## 3. Session detail  (`/sessions/:session_id`)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          Overview  |  [Sessions]  |  Costs  |  Tools  [30d ▾]   │
│  ← Back to Sessions                                                     │
├─────────────────────────────────────────────────────────────────────────┤
│  sess_abc123                                                            │
│  May 9 2026  14:32:01 → 14:37:13     Duration: 5m 12s      $0.024      │
│  Model: claude-sonnet-4-6                                               │
│  ─────────────────────────────────────────────────────────────────────  │
│                                                                         │
│  TIMELINE                                                               │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  14:32:01  ●  session start                                       │ │
│  │  14:32:03  ├─ ⚙  Read       (0.2s)                               │ │
│  │  14:32:08  ├─ ⚙  Bash       (4.7s)                               │ │
│  │  14:32:15  ├─ ◆  API call   in:1024 / out:512 / cache:256  $0.008│ │
│  │  14:32:19  ├─ ⚙  Edit       (0.1s)                               │ │
│  │  14:33:02  ├─ ◆  API call   in:2048 / out:1024            $0.016 │ │
│  │  14:37:13  ●  session end                                         │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  ┌─────────────────────────────┐   ┌─────────────────────────────┐    │
│  │ Cost breakdown              │   │ Token usage                 │    │
│  │  API calls       $0.024     │   │  Input          3,072       │    │
│  │  Tools           —          │   │  Output         1,536       │    │
│  │  ─────────────────────────  │   │  Cache read       256       │    │
│  │  Total           $0.024     │   │  Cache write      128       │    │
│  └─────────────────────────────┘   └─────────────────────────────┘    │
│                                                                         │
│  ▶ Raw span attributes  (click to expand)                              │
│    [ collapsed JSON viewer — <details> element, no JS ]               │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**
- Two icon types: ⚙ = tool call, ◆ = LLM API call. Simple legend via tooltip.
- Indented timeline: parent–child via `parent_span_id`. Root = session span.
- Cost shown inline per API call (Tufte: put data in context, not separate).
- Raw attributes in `<details>` — zero JS, accessible, Pi-friendly (Norman: visibility).
- Error spans (future): red ✗ icon on the timeline row when `status=ERROR`.
- Timeline rows are not interactive in MVP; hover highlight acceptable.

---

## 4. Costs  (`/costs`)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          Overview  |  Sessions  |  [Costs]  |  Tools  [30d ▾]   │
├─────────────────────────────────────────────────────────────────────────┤
│  Costs                                          Total: $47.23  (30d)   │
│                                                                         │
│  Daily cost                                                             │
│  $4 ┤                    ██                                             │
│  $3 ┤         █          ██   █                                         │
│  $2 ┤  █  █   █  █  █  █ ██  █  █  █  █  █                            │
│  $1 ┤  █  █ █ █  █  █  █ ██  █  █  █  █  █  █  █  █  █  █  █  █     │
│  $0 └──────────────────────────────────────────────────────────────    │
│      Apr 9                                                      May 9  │
│                                                                         │
│  ┌────────────────────────────────┐   ┌─────────────────────────────┐  │
│  │ By model                       │   │ Top sessions by cost        │  │
│  │ Model          Cost    Share   │   │ Session ID      Cost        │  │
│  │ sonnet-4-6    $31.50   67%     │   │ sess_def456    $0.187       │  │
│  │ opus-4-7      $14.20   30%     │   │ sess_xyz789    $0.142       │  │
│  │ haiku-4-5     $1.53     3%    │   │ sess_abc123    $0.094       │  │
│  └────────────────────────────────┘   │ sess_mno345    $0.051       │  │
│                                        │ ...                         │  │
│                                        └─────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**
- Y-axis starts at 0 — no truncation (Tufte: axis honesty).
- Bar chart from `daily_usage.total_cost_usd` aggregated per day — lightweight SQL.
- By-model table shows share percentage (Anchoring — percentage is more scannable than raw).
- Top-10 sessions by cost: helps user identify expensive outliers quickly (Pareto 80/20).
- Session IDs in this table are links → `/sessions/:id`.
- No pie chart — pie charts violate Gestalt Prägnanz for comparison tasks.

---

## 5. Tools breakdown  (`/tools`)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel          Overview  |  Sessions  |  Costs  |  [Tools]  [30d ▾]  │
├─────────────────────────────────────────────────────────────────────────┤
│  Tools                                          1,247 calls  (30d)     │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │ Tool       Calls   Avg duration   Fail rate   Usage               │ │
│  ├───────────────────────────────────────────────────────────────────┤ │
│  │ Bash         423       4.2s          12%      ████████████░░░░    │ │
│  │ Read         387       0.1s           0%      ███████████░░░░░    │ │
│  │ Edit         201       0.2s           2%      ██████░░░░░░░░░░    │ │
│  │ Grep         156       0.3s           1%      █████░░░░░░░░░░░    │ │
│  │ Write         48       0.1s           0%      ██░░░░░░░░░░░░░░    │ │
│  │ Agent         32      45.3s           6%      █░░░░░░░░░░░░░░░    │ │
│  │ WebFetch      15       1.8s           8%      ░░░░░░░░░░░░░░░░    │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
│  NOTE: Fail rate requires error status extraction — see API gaps.       │
│                                                                         │
│  Call trend (last 30d)    [Bash ▾]  [Read ▾]  + Add tool              │
│  ┌───────────────────────────────────────────────────────────────────┐ │
│  │  ── Bash   ·· Read                                                │ │
│  │  ·   ·                  ·                                         │ │
│  │ · · · · ··  · ·· ·  ·  · · ·· ··  · ·  · ·  · ·· ·  · · ·     │ │
│  └───────────────────────────────────────────────────────────────────┘ │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

**Design notes:**
- Usage bar = inline proportional bar (pure CSS, no JS). Max width = Bash (highest).
- Fail rate column: 0% is rendered as plain "0%" (no red highlight); >5% is highlighted red (Von Restorff — attention only where action may be needed).
- Avg duration: Bash 4.2s vs Agent 45.3s — this is expected. No anomaly flag needed unless configurable threshold added later.
- Trend chart is optional in MVP — can ship tools table alone first. Chart uses `daily_usage` grouped by tool_name.
- "Fail rate requires error extraction" note — honest about MVP scope (Nielsen: visibility of system status).

---

## Visual language notes (pre-token)

Full token table belongs in the design-system doc. This is the minimum spec for implementers:

| Element         | Light                  | Dark                    |
|-----------------|------------------------|-------------------------|
| Background      | `#f9fafb` (gray-50)    | `#111827` (gray-900)    |
| Surface (card)  | `#ffffff`              | `#1f2937` (gray-800)    |
| Border          | `#e5e7eb` (gray-200)   | `#374151` (gray-700)    |
| Text primary    | `#111827` (gray-900)   | `#f9fafb` (gray-50)     |
| Text secondary  | `#6b7280` (gray-500)   | `#9ca3af` (gray-400)    |
| Accent / cost   | `#2563eb` (blue-600)   | `#3b82f6` (blue-400)    |
| Error / fail    | `#dc2626` (red-600)    | `#f87171` (red-400)     |
| Success / zero  | `#16a34a` (green-600)  | `#4ade80` (green-400)   |
| Bar fill        | `#3b82f6` (blue-500)   | `#60a5fa` (blue-400)    |

Typography: system-ui font stack (no web font load). 14px base, 12px small, 16px heading. Monospace for session IDs, token counts.

Spacing: 4px base unit. Cards: 16px padding. Table cells: 12px vertical, 16px horizontal.
