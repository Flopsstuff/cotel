# cotel Component Specs

> Implementation guide for the React SPA component library. All token references use names from [tokens.md](tokens.md). Components are implemented as CSS Modules (`ComponentName.module.css`).
>
> Status: v1 — covers all components referenced by [pages.md](pages.md).
> Related: [tokens.md](tokens.md), [pages.md](pages.md)

---

## Component index

| Component | Used on | Purpose |
|---|---|---|
| [KPI Card](#kpi-card) | Overview, Costs, Tools | Single metric with optional delta |
| [Stat Badge](#stat-badge) | Session detail, tables | Inline status / numeric pill |
| [Data Table](#data-table) | All pages | Sortable, paginated tabular data |
| [Sidebar Nav](#sidebar-nav) | Global shell | Left-rail page navigation |
| [Top Bar](#top-bar) | Global shell | Brand + nav + time range control |
| [Chart Wrapper](#chart-wrapper) | Overview, Costs, Tools, Models | Consistent framing for Chart.js canvases |
| [Tooltip](#tooltip) | Charts, truncated cells | On-hover context |
| [Loading Skeleton](#loading-skeleton) | All data regions | Content placeholder during fetch |
| [Empty State](#empty-state) | Tables, charts | No-data feedback |
| [Error State](#error-state) | All data regions | Fetch failure feedback |
| [Date Range Picker](#date-range-picker) | Top bar | Global time filter |
| [Refresh Indicator](#refresh-indicator) | Top bar | Auto-refresh status |
| [Breadcrumb](#breadcrumb) | Session Detail | Drill-down navigation context |
| [Pagination](#pagination) | Sessions list | Page navigation |

---

## KPI Card

**Purpose:** Display a single key metric with a label and optional period delta.  
**Miller's Law:** Limit to 4 cards per row to stay within 7±2 attentional chunk.

### Anatomy

```
┌──────────────────────┐
│  LABEL               │  ← --text-xs, --color-text-2, uppercase
│  Value               │  ← --text-2xl, --color-text-1, tabular-nums
│  +8% vs prev period  │  ← --text-sm, delta color (success/danger/muted)
└──────────────────────┘
```

### Spec

| Property | Token | Notes |
|---|---|---|
| Background | `--color-surface` | |
| Border | `1px solid --color-border` | |
| Border radius | `--radius-base` | |
| Shadow | `--shadow-1` | |
| Padding | `--space-4` | All sides |
| Min width | `140px` | Allows wrapping at narrow viewports |
| Flex grow | `1` | All 4 cards share equal width |
| Label font | `--text-xs`, 500 weight | Uppercase, letter-spacing 0.06em |
| Label color | `--color-text-2` | |
| Label margin-bottom | `--space-1` | |
| Value font | `--text-2xl`, 700 weight | `font-variant-numeric: tabular-nums` |
| Value color | `--color-text-1` | |
| Delta font | `--text-sm` | |
| Delta positive | `--color-success` | Prefixed with "↑" |
| Delta negative | `--color-danger` | Prefixed with "↓" |
| Delta neutral (0%) | `--color-text-3` | "—" or "0%" |

### Props

```tsx
interface KpiCardProps {
  label: string;
  value: string;           // pre-formatted: "$47.23", "2.1M", "142"
  delta?: string;          // pre-formatted: "+8%", "-3%", or undefined
  deltaDir?: 'up' | 'down' | 'neutral';
}
```

### States

- **Loading:** Replace value + delta with `<Skeleton width="80px" height="28px" />` and `<Skeleton width="60px" height="16px" />`.
- **Error:** Replace value with `--` in `--color-text-3`. No delta shown.

---

## Stat Badge

**Purpose:** Inline pill for categorical status or numeric context (model name, session status, error count).

### Anatomy

```
 ┌──────────────┐
 │  ACTIVE      │  ← pill, rounded-full
 └──────────────┘
```

### Variants

| Variant | Background | Text color | Use case |
|---|---|---|---|
| `success` | `--color-success-bg` | `--color-success` | Active session, 0% error rate |
| `warning` | `--color-warning-bg` | `--color-warning` | Partial errors, slow latency |
| `danger` | `--color-danger-bg` | `--color-danger` | Error, failed span |
| `neutral` | `--color-neutral-bg` | `--color-neutral` | Model name, any categorical label |
| `accent` | `--color-accent-bg` | `--color-accent` | Highlighted / current period |

### Spec

| Property | Token |
|---|---|
| Font | `--text-xs`, 600 weight |
| Uppercase + letter-spacing | `0.03em` |
| Padding | `--space-1` vertical, 6px horizontal |
| Border radius | `--radius-full` |
| Display | `inline-block` |

### Props

```tsx
interface StatBadgeProps {
  label: string;
  variant: 'success' | 'warning' | 'danger' | 'neutral' | 'accent';
}
```

---

## Data Table

**Purpose:** Tabular data with sortable columns, optional row click, optional pagination. Used on every page.

### Anatomy

```
┌────────────────────────────────────────────────────────────┐
│  COL A ↕   COL B ↕   COL C        COL D                  │  ← thead
├────────────────────────────────────────────────────────────┤
│  value a   value b   value c       value d                │  ← tr
│  value a   value b   value c       value d                │
│  …                                                        │
└────────────────────────────────────────────────────────────┘
[Pagination]
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Wrapper background | `--color-surface` | |
| Wrapper border | `1px solid --color-border` | |
| Wrapper border-radius | `--radius-base` | |
| Wrapper shadow | `--shadow-1` | |
| Wrapper overflow | `hidden` | Clips border-radius on thead |
| `thead` background | `--color-surface-2` | |
| `th` font | `--text-xs`, 500 weight | Uppercase, letter-spacing 0.06em |
| `th` color | `--color-text-2` | |
| `th` padding | `--space-2` vertical, `--space-3` horizontal | |
| `td` font | `--text-sm` | |
| `td` color | `--color-text-1` | |
| `td` padding | `--space-2` vertical, `--space-3` horizontal | |
| `td` border-top | `1px solid --color-border` | |
| Monospace cells | `--font-mono`, `--text-mono-sm` | Session IDs, span names |
| Muted cells | `--color-text-2` | Timestamps, secondary values |
| Error rows | `--color-danger-bg` background | Whole row, not just cell |
| Hover (clickable rows) | `--color-surface-2` background | Cursor: pointer |
| Active sort column | Sort icon `↑` / `↓` in `--color-accent` | Inactive: `↕` in `--color-text-3` |
| `min-width` | `600px` inside wrapper | Triggers horizontal scroll on narrow viewports |

### Sort behavior

- Click any `th` with the `sortable` prop → sort asc; click again → desc; click again → remove sort.
- Sort state is URL-driven (`?sort=cost&dir=desc`) so it survives reload and browser back.
- Initial sort: most recent first for session tables; highest first for cost/usage tables.

### Column alignment

- Numeric columns (cost, tokens, duration, count): right-aligned (`text-align: right`), monospace.
- Text columns (session ID, model, tool name): left-aligned.
- Status columns: center-aligned.

### Props

```tsx
interface Column<T> {
  key: keyof T;
  header: string;
  sortable?: boolean;
  align?: 'left' | 'right' | 'center';
  render?: (value: T[keyof T], row: T) => React.ReactNode;
}

interface DataTableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: keyof T;
  onRowClick?: (row: T) => void;
  sortKey?: string;
  sortDir?: 'asc' | 'desc';
  onSort?: (key: string, dir: 'asc' | 'desc') => void;
  isLoading?: boolean;
  isEmpty?: boolean;
  emptyMessage?: string;
  errorMessage?: string;
}
```

### States

- **Loading:** Render `<LoadingSkeleton rows={5} />` inside the table wrapper.
- **Empty:** Render `<EmptyState />` inside the wrapper.
- **Error:** Render `<ErrorState />` inside the wrapper.

---

## Sidebar Nav

**Purpose:** Left-rail navigation. Width 160px desktop, icon-only at ≤768px.

### Anatomy

```
┌──────────────┐
│  cotel       │  ← brand label, --text-base 700
├──────────────┤
│  Overview    │  ← nav item
│  Sessions    │  ← nav item (active)
│  Costs       │
│  Tools       │
│  Models      │
└──────────────┘
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Sidebar width | `160px` | Fixed; collapses to `48px` at ≤768px |
| Sidebar background | `--color-surface` | |
| Sidebar border-right | `1px solid --color-border` | |
| Brand padding | `--space-4` horizontal, `--space-3` bottom | Bottom border = 1px solid `--color-border` |
| Brand font | `--text-base`, 700 | |
| Nav item padding | `10px --space-4` | 10px vertical (not on scale; matches existing) |
| Nav item font | `--text-sm`, 500 | |
| Nav item color | `--color-text-2` | Default |
| Nav item hover | `--color-surface-2` background, `--color-text-1` text | Transition `--duration-fast` |
| Active item background | `--color-accent-bg` | |
| Active item text | `--color-accent` | |
| Active item border-left | `3px solid --color-accent` | Width = 3px, part of padding budget |
| Disabled item | `--color-text-3`, `pointer-events: none` | For unreleased pages |
| Collapsed state (≤768px) | `width: 48px` | Hide text labels via `overflow: hidden` or `display: none` on `span` |

### Props

```tsx
interface NavItem {
  label: string;
  href: string;
  disabled?: boolean;
}

interface SidebarNavProps {
  items: NavItem[];
  activePath: string;   // current pathname, used to determine active item
}
```

---

## Top Bar

**Purpose:** Global shell element containing brand, primary nav, time range picker, and refresh indicator.  
Used at ≥1280px viewport as a horizontal top bar. Below 1280px the sidebar nav collapses; the top bar stays for brand + time range.

### Anatomy

```
┌─────────────────────────────────────────────────────────────────────────┐
│  cotel    Overview  Sessions  Costs  Tools  Models      [30d ▾]  [●]  │
└─────────────────────────────────────────────────────────────────────────┘
```

> **Architecture note:** The current Go-template implementation uses a sidebar; the React SPA may use either pattern. This spec covers both. The sidebar nav (left rail) is preferred for dashboards ≥1280px wide per Jakob's Law (desktop analytics convention). A top bar is an acceptable alternative — confirm with CTO before switching. The token/component specs work for both.

### Spec

| Element | Token | Notes |
|---|---|---|
| Height | `--space-12` (48px) | |
| Background | `--color-surface` | |
| Border-bottom | `1px solid --color-border` | |
| Z-index | `--z-sticky` | Sticky position |
| Brand font | `--text-base`, 700 | |
| Nav item font | `--text-sm`, 500 | |
| Active nav underline | `2px solid --color-accent` below text | |
| Right section gap | `--space-3` | Between date picker and refresh dot |

---

## Chart Wrapper

**Purpose:** Consistent visual framing for Chart.js canvases (bar charts, line charts). Handles title, canvas, and empty/loading states.

### Anatomy

```
┌──────────────────────────────────────────┐
│  Section title (optional)                │  ← --text-xs, uppercase, --color-text-2
│  ┌────────────────────────────────────┐  │
│  │  [Chart.js canvas]                 │  │  ← height: 200px default
│  └────────────────────────────────────┘  │
└──────────────────────────────────────────┘
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Wrapper background | `--color-surface` | |
| Wrapper border | `1px solid --color-border` | |
| Wrapper border-radius | `--radius-base` | |
| Wrapper padding | `--space-4` | |
| Wrapper shadow | `--shadow-1` | |
| Canvas height (default) | `200px` | `height` prop overrides |
| Canvas width | `100%` | Responsive |
| Title font | `--text-xs`, 600, uppercase | Letter-spacing 0.06em |
| Title color | `--color-text-2` | |
| Title margin-bottom | `--space-3` | |

### Chart.js defaults (register globally)

Configure Chart.js defaults in a `chartDefaults.ts` module so every chart inherits them automatically:

```ts
Chart.defaults.font.family = 'system-ui, -apple-system, "Segoe UI", sans-serif';
Chart.defaults.font.size = 11;
Chart.defaults.color = 'var(--color-text-2)';
Chart.defaults.borderColor = 'var(--color-border)';
Chart.defaults.plugins.legend.display = false;  // hide legend by default; override per chart
Chart.defaults.plugins.tooltip.backgroundColor = 'var(--color-surface)';
Chart.defaults.plugins.tooltip.titleColor = 'var(--color-text-1)';
Chart.defaults.plugins.tooltip.bodyColor = 'var(--color-text-2)';
Chart.defaults.plugins.tooltip.borderColor = 'var(--color-border-2)';
Chart.defaults.plugins.tooltip.borderWidth = 1;
Chart.defaults.plugins.tooltip.padding = 8;
Chart.defaults.plugins.tooltip.cornerRadius = 6;  // --radius-base
```

Bar color: `--color-chart-1` for single-series. Use chart palette tokens for multi-series.  
Y-axis: gridlines in `--color-border`, no x-axis gridlines.  
X-axis labels: `--text-xs` (11px), `--color-text-3`.

### States

- **Loading:** Show `<LoadingSkeleton height="200px" />` inside the wrapper.
- **Empty:** Show `<EmptyState message="No data for this period" />` inside the wrapper.

### Props

```tsx
interface ChartWrapperProps {
  title?: string;
  height?: number;        // px, default 200
  isLoading?: boolean;
  isEmpty?: boolean;
  children: React.ReactNode;  // Chart.js component
}
```

---

## Tooltip

**Purpose:** On-hover context for chart data points and truncated table cells.

### Spec

| Property | Token | Notes |
|---|---|---|
| Background | `--color-surface` | |
| Border | `1px solid --color-border-2` | |
| Border radius | `--radius-lg` | |
| Shadow | `--shadow-2` | |
| Padding | `--space-2` vertical, `--space-3` horizontal | |
| Font | `--text-sm` | |
| Title color | `--color-text-1` | |
| Body color | `--color-text-2` | |
| Z-index | `--z-tooltip` | |
| Trigger | Hover (`mouseenter` / `mouseleave`) | No click trigger for table cells |
| Delay | 200ms appear, 0ms dismiss | Prevents flicker on quick passes |
| Max width | `240px` | Truncate with ellipsis beyond |

**Chart tooltips** are handled by Chart.js with defaults set in `chartDefaults.ts` (see above). Do not build a custom React tooltip for chart data points.

**Table cell tooltips** (for truncated session IDs, long model names): use the HTML `title` attribute as a fallback, but add a CSS-only tooltip via `::after` pseudo-element for styled presentation.

---

## Loading Skeleton

**Purpose:** Placeholder content during data fetch. Prevents layout shift.

### Anatomy

```
┌──────────────────────────────────────────┐
│  ░░░░░░░░░░░░░░░░░░░  (shimmer)          │
│  ░░░░░░░░░░░                             │
│  ░░░░░░░░░░░░░░░░░░░░░░░░░░░             │
└──────────────────────────────────────────┘
```

### Spec

| Property | Token | Notes |
|---|---|---|
| Background | gradient from `--color-surface-2` → `--color-border` → `--color-surface-2` | Animated shimmer left-to-right |
| Border radius | `--radius-sm` | |
| Animation | `shimmer 1.5s ease-in-out infinite` | Keyframe: `background-position 0% → 100%` |
| Duration | `--duration-slow` × 5 = ~1.5s | Matches skeleton convention |

### Shimmer keyframe

```css
@keyframes shimmer {
  0%   { background-position: -200% 0; }
  100% { background-position:  200% 0; }
}
.skeleton {
  background: linear-gradient(
    90deg,
    var(--color-surface-2) 25%,
    var(--color-border) 50%,
    var(--color-surface-2) 75%
  );
  background-size: 200% 100%;
  animation: shimmer 1.5s ease-in-out infinite;
  border-radius: var(--radius-sm);
}
```

### Props

```tsx
interface SkeletonProps {
  width?: string;    // default "100%"
  height?: string;   // default "1em"
  rows?: number;     // renders N stacked skeletons with gap
}
```

### Table skeleton

`rows={5}` renders 5 full-width skeleton bars with `--space-2` gap, inside the table wrapper, replacing `tbody`.

---

## Empty State

**Purpose:** Shown when a query returns zero results. Distinct from error — data loaded successfully, just nothing to show.

### Anatomy

```
┌──────────────────────────────────────────┐
│                                          │
│          No sessions found.              │  ← message
│          Try adjusting the filters       │  ← sub-message (optional)
│          or date range.                  │
│                                          │
└──────────────────────────────────────────┘
```

### Spec

| Property | Token | Notes |
|---|---|---|
| Container padding | `--space-8` | |
| Text align | center | |
| Background | `--color-surface` | |
| Border | `1px solid --color-border` | |
| Border radius | `--radius-base` | |
| Primary message font | `--text-base` | |
| Primary message color | `--color-text-3` | |
| Sub-message font | `--text-sm` | |
| Sub-message color | `--color-text-3` | |
| Icon (optional) | Unicode or inline SVG, 24px | `--color-text-3` fill |

### Props

```tsx
interface EmptyStateProps {
  message?: string;        // default: "No data available."
  subMessage?: string;
  icon?: React.ReactNode;  // optional icon above message
}
```

---

## Error State

**Purpose:** Shown when a data fetch fails. Provides the error reason and a retry action.

### Anatomy

```
┌──────────────────────────────────────────┐
│                                          │
│   ⚠  Failed to load sessions            │  ← error title, --color-danger
│      GET /sessions returned 500.        │  ← error detail (optional)
│      [Retry]                            │  ← button
│                                          │
└──────────────────────────────────────────┘
```

### Spec

| Property | Token | Notes |
|---|---|---|
| Container | Same as Empty State | Use `--color-danger-bg` tint instead of plain surface |
| Title font | `--text-base`, 500 | |
| Title color | `--color-danger` | |
| Detail font | `--text-sm` | |
| Detail color | `--color-text-2` | |
| Retry button | `--color-accent` text, `--color-accent-bg` background | `--radius-sm`, `--space-2` vertical padding, `--space-4` horizontal |
| Retry button hover | `--color-accent` background, `white` text | |

### Props

```tsx
interface ErrorStateProps {
  title?: string;          // default: "Failed to load data."
  detail?: string;         // HTTP error detail, optional
  onRetry?: () => void;    // if present, shows Retry button
}
```

---

## Date Range Picker

**Purpose:** Global time filter. Renders as a button in the nav that opens a dropdown with preset options. Default: 30d.

### Anatomy (closed)

```
  [  30d  ▾  ]
```

### Anatomy (open)

```
  [  30d  ▾  ]
  ┌────────────┐
  │  7 days    │  ← option
  │  30 days   │  ← active (checkmark)
  │  90 days   │
  │  ──────    │
  │  Custom…   │  ← opens date input pair (out of scope for v1)
  └────────────┘
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Button background | `--color-surface` | |
| Button border | `1px solid --color-border-2` | |
| Button border-radius | `--radius-sm` | |
| Button padding | `--space-1` vertical, `--space-3` horizontal | |
| Button font | `--text-sm`, 500 | |
| Button color | `--color-text-1` | |
| Dropdown background | `--color-surface` | |
| Dropdown border | `1px solid --color-border-2` | |
| Dropdown border-radius | `--radius-lg` | |
| Dropdown shadow | `--shadow-2` | |
| Dropdown z-index | `--z-dropdown` | |
| Option padding | `--space-2` vertical, `--space-3` horizontal | |
| Option font | `--text-sm` | |
| Option hover | `--color-surface-2` background | |
| Active option | `--color-accent` text + checkmark | |
| Separator | `1px solid --color-border` | Above "Custom…" |

### Behavior

- Selecting a preset closes the dropdown and updates URL: `?since=<ISO-date>`.
- Dropdown closes on outside click or `Escape`.
- Custom date: v1 deferred — show "Custom…" as disabled until FLO-34 scope expanded.

### Props

```tsx
type Preset = '7d' | '30d' | '90d';

interface DateRangePickerProps {
  value: Preset;
  onChange: (preset: Preset) => void;
}
```

---

## Refresh Indicator

**Purpose:** Small pulse dot in the top-right of the nav to signal that data is fresh or currently refreshing.

### Anatomy

```
  ●   — solid dot, pulsing when fetching
  ○   — hollow / muted dot when idle and data is stale (>5 min old)
```

### Spec

| State | Color | Animation | Title (tooltip) |
|---|---|---|---|
| **Fresh / idle** | `--color-success` | None | "Data current as of HH:MM" |
| **Fetching** | `--color-accent` | Pulse animation (scale 1→1.4→1, 1s loop) | "Refreshing…" |
| **Stale** | `--color-text-3` | None | "Last updated HH:MM — click to refresh" |
| **Error** | `--color-danger` | None | "Last refresh failed — click to retry" |

```css
@keyframes pulse {
  0%, 100% { transform: scale(1);   opacity: 1;   }
  50%       { transform: scale(1.4); opacity: 0.7; }
}
```

| Property | Token |
|---|---|
| Dot size | `10px × 10px` |
| Border radius | `--radius-full` |
| Cursor | `pointer` (clicking triggers manual refresh) |

### Props

```tsx
type RefreshStatus = 'fresh' | 'fetching' | 'stale' | 'error';

interface RefreshIndicatorProps {
  status: RefreshStatus;
  lastFetchedAt?: Date;
  onRefresh: () => void;
}
```

### Auto-refresh behavior

See [pages.md § Interaction notes — Auto-refresh](pages.md#interaction-notes).

---

## Breadcrumb

**Purpose:** Contextual location indicator on drill-down pages (Session Detail).

### Anatomy

```
Sessions  /  sess_abc123…
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Font | `--text-sm` | |
| Link color | `--color-accent` | |
| Link hover | Underline | |
| Separator | ` / ` in `--color-text-3` | |
| Current page | `--color-text-1`, no underline | Truncate long IDs with ellipsis |
| Margin-bottom | `--space-4` | Separates from page title |

---

## Pagination

**Purpose:** Page navigation for the Sessions list.

### Anatomy

```
  [← Prev]  1  [2]  3  4  [Next →]   Page 2 of 14
```

### Spec

| Element | Token | Notes |
|---|---|---|
| Container | `display: flex`, `align-items: center`, `gap: --space-2` | |
| Font | `--text-sm` | |
| Button padding | `--space-1` vertical, `10px` horizontal | |
| Button border | `1px solid --color-border-2` | |
| Button border-radius | `--radius-sm` | |
| Button background | `--color-surface` | |
| Button hover | `--color-surface-2` background | |
| Current page | `--color-accent` background, `white` text | |
| Disabled (Prev on page 1) | `--color-text-3`, `pointer-events: none` | |
| Page count label | `--color-text-2` | Right of buttons |
| Margin-top | `--space-4` | |

### Props

```tsx
interface PaginationProps {
  page: number;         // 1-indexed
  totalPages: number;
  onPageChange: (page: number) => void;
}
```

Render at most 5 page buttons at once: first, last, current ±1, with `…` gaps.
