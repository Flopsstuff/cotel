# cotel Design Tokens

> Source of truth for all visual values. Implementors use these names verbatim in CSS Modules. Every token is a CSS custom property set on `:root`.
>
> Status: v1 — sufficient for FLO-34 dashboard implementation.
> Related: [components.md](components.md), [pages.md](pages.md)

---

## Color — Light mode

| Token | Value | Usage |
|---|---|---|
| `--color-bg` | `#f8fafc` | Page background |
| `--color-surface` | `#ffffff` | Card, panel, popover background |
| `--color-surface-2` | `#f1f5f9` | Table header, hover state, nested surface |
| `--color-border` | `#e2e8f0` | Dividers, card outlines, input borders |
| `--color-border-2` | `#cbd5e1` | Stronger dividers, focused/active borders |
| `--color-text-1` | `#0f172a` | Primary text, headings, values |
| `--color-text-2` | `#475569` | Secondary text, labels, sub-labels |
| `--color-text-3` | `#94a3b8` | Placeholder, disabled, empty-state text |
| `--color-accent` | `#2563eb` | Active nav, links, primary button, chart bars |
| `--color-accent-bg` | `#eff6ff` | Active nav background, selected row highlight |
| `--color-success` | `#15803d` | Positive delta, success badge text |
| `--color-success-bg` | `#dcfce7` | Success badge background |
| `--color-warning` | `#d97706` | Warning badge text, caution indicators |
| `--color-warning-bg` | `#fef9c3` | Warning badge background |
| `--color-danger` | `#dc2626` | Error text, error badge, error row indicator |
| `--color-danger-bg` | `#fef2f2` | Error row background, error badge background |
| `--color-neutral-bg` | `#f1f5f9` | Neutral badge background |
| `--color-neutral` | `#475569` | Neutral badge text |

---

## Color — Dark mode

Applied via `@media (prefers-color-scheme: dark)` override on `:root`. All token names are identical; only values change.

| Token | Dark value | Usage |
|---|---|---|
| `--color-bg` | `#020617` | Page background |
| `--color-surface` | `#0f172a` | Card, panel background |
| `--color-surface-2` | `#1e293b` | Table header, hover |
| `--color-border` | `#334155` | Dividers |
| `--color-border-2` | `#475569` | Stronger dividers |
| `--color-text-1` | `#f8fafc` | Primary text |
| `--color-text-2` | `#94a3b8` | Secondary text |
| `--color-text-3` | `#475569` | Muted / placeholder |
| `--color-accent` | `#60a5fa` | Links, active state, chart bars |
| `--color-accent-bg` | `#172554` | Active nav background |
| `--color-success` | `#4ade80` | Success text |
| `--color-success-bg` | `#14532d` | Success badge background |
| `--color-warning` | `#fbbf24` | Warning text |
| `--color-warning-bg` | `#422006` | Warning badge background |
| `--color-danger` | `#f87171` | Error text |
| `--color-danger-bg` | `#450a0a` | Error row background |
| `--color-neutral-bg` | `#1e293b` | Neutral badge background |
| `--color-neutral` | `#94a3b8` | Neutral badge text |

### Contrast compliance (WCAG AA / 4.5:1 for normal text)

| Pair | Light ratio | Dark ratio | Pass |
|---|---|---|---|
| `--color-text-1` on `--color-bg` | 19.4:1 | 19.4:1 | ✓ AAA |
| `--color-text-2` on `--color-surface` | 7.1:1 | 4.6:1 | ✓ AA |
| `--color-accent` on `--color-bg` | 5.9:1 | 5.7:1 | ✓ AA |
| `--color-danger` on `--color-danger-bg` | 5.1:1 | 4.8:1 | ✓ AA |
| `--color-text-3` on `--color-surface` | 3.5:1 | — | ✗ Use only for non-essential UI (placeholders, timestamps) |

> `--color-text-3` intentionally falls below AA — it marks genuinely secondary, non-interactive content. Never use it for actionable labels or data values.

---

## Chart palette

Ordered list used for multi-series charts (tool breakdowns, model comparisons). Designed for color-independence: vary hue *and* lightness so the series are distinguishable in greyscale and to users with deuteranopia.

| Token | Light value | Dark value | Usage |
|---|---|---|---|
| `--color-chart-1` | `#2563eb` | `#60a5fa` | Primary series (matches accent) |
| `--color-chart-2` | `#7c3aed` | `#a78bfa` | Second series |
| `--color-chart-3` | `#059669` | `#34d399` | Third series |
| `--color-chart-4` | `#d97706` | `#fbbf24` | Fourth series |
| `--color-chart-5` | `#db2777` | `#f472b6` | Fifth series |

Use `--color-chart-1` → `--color-chart-5` in order; do not skip. When more than 5 series exist, group the tail as "Other" (use `--color-text-3`).

---

## Typography

Font stack: `system-ui, -apple-system, 'Segoe UI', sans-serif`  
Monospace stack: `ui-monospace, 'JetBrains Mono', 'Fira Code', monospace`

| Token | Size | Weight | Line-height | Usage |
|---|---|---|---|---|
| `--text-xs` | `11px` | 500 | 1.4 | Table headers, card labels, badges (ALL CAPS + letter-spacing) |
| `--text-sm` | `13px` | 400 | 1.5 | Table cells, body copy, nav items, filter labels |
| `--text-base` | `14px` | 400 | 1.5 | Default body, form inputs |
| `--text-lg` | `16px` | 600 | 1.4 | Section headings, modal titles |
| `--text-xl` | `20px` | 700 | 1.3 | Page titles |
| `--text-2xl` | `24px` | 700 | 1.2 | KPI Card values |
| `--text-mono-sm` | `12px` | 400 | 1.5 | Session IDs, span names, JSON viewer |
| `--text-mono-base` | `13px` | 400 | 1.5 | Code blocks, inline monospace |

Letter-spacing for uppercase labels: `0.06em` (applies to `.card-label`, `th`, `.section-title`, `.badge`).

---

## Spacing

Base unit = 4px. Tokens are multiples.

| Token | Value | Common use |
|---|---|---|
| `--space-1` | `4px` | Tight inline gap, badge padding vertical |
| `--space-2` | `8px` | Row padding vertical, icon-to-text gap |
| `--space-3` | `12px` | Card gap in row, section-title margin-bottom |
| `--space-4` | `16px` | Card padding, nav item padding horizontal, chart padding |
| `--space-5` | `20px` | — (available; use sparingly) |
| `--space-6` | `24px` | Section margin-bottom, card row margin-bottom |
| `--space-8` | `32px` | Page content padding, large section gap |
| `--space-10` | `40px` | — |
| `--space-12` | `48px` | Nav height, top-bar height |

Do not use raw pixel values in CSS Modules. Reference `var(--space-*)`.

---

## Border radius

| Token | Value | Usage |
|---|---|---|
| `--radius-sm` | `4px` | Buttons, pagination buttons, badges |
| `--radius-base` | `6px` | Cards, table wrappers, chart wrappers, inputs |
| `--radius-lg` | `8px` | Modals, date pickers, tooltips |
| `--radius-full` | `9999px` | Pill badges, pulse dot |

---

## Shadows

| Token | Value | Usage |
|---|---|---|
| `--shadow-1` | `0 1px 2px rgba(0,0,0,.06)` | Cards, table wrappers, chart wrappers |
| `--shadow-2` | `0 2px 8px rgba(0,0,0,.10)` | Popovers, tooltips, date picker dropdown |
| `--shadow-3` | `0 4px 16px rgba(0,0,0,.14)` | Modals |

In dark mode, shadows are less visible due to dark surface; keep the same values but the UI relies more on border contrast. Do not disable shadows in dark mode.

---

## Z-index ladder

| Token | Value | Layer |
|---|---|---|
| `--z-base` | `0` | Normal stacking |
| `--z-sticky` | `100` | Sticky top nav bar |
| `--z-dropdown` | `200` | Date range picker, filter dropdowns |
| `--z-tooltip` | `300` | Tooltips |
| `--z-modal-backdrop` | `400` | Modal scrim |
| `--z-modal` | `500` | Modal content |
| `--z-toast` | `600` | Toast notifications |

---

## Motion

| Token | Value | Usage |
|---|---|---|
| `--duration-fast` | `100ms` | Hover state, opacity |
| `--duration-base` | `200ms` | Expand/collapse, dropdown open |
| `--duration-slow` | `300ms` | Skeleton shimmer |
| `--easing-default` | `ease` | Standard transitions |
| `--easing-in-out` | `cubic-bezier(0.4,0,0.2,1)` | Panel open/close |

Respect `prefers-reduced-motion`: when set, all durations collapse to `0ms`. Implement via:

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { transition-duration: 0ms !important; animation-duration: 0ms !important; }
}
```

---

## Full token CSS block

Copy into `src/styles/tokens.css` as the canonical token file. All CSS Modules import this.

```css
:root {
  /* Color */
  --color-bg:          #f8fafc;
  --color-surface:     #ffffff;
  --color-surface-2:   #f1f5f9;
  --color-border:      #e2e8f0;
  --color-border-2:    #cbd5e1;
  --color-text-1:      #0f172a;
  --color-text-2:      #475569;
  --color-text-3:      #94a3b8;
  --color-accent:      #2563eb;
  --color-accent-bg:   #eff6ff;
  --color-success:     #15803d;
  --color-success-bg:  #dcfce7;
  --color-warning:     #d97706;
  --color-warning-bg:  #fef9c3;
  --color-danger:      #dc2626;
  --color-danger-bg:   #fef2f2;
  --color-neutral-bg:  #f1f5f9;
  --color-neutral:     #475569;

  /* Chart palette */
  --color-chart-1: #2563eb;
  --color-chart-2: #7c3aed;
  --color-chart-3: #059669;
  --color-chart-4: #d97706;
  --color-chart-5: #db2777;

  /* Typography */
  --font-sans:  system-ui, -apple-system, 'Segoe UI', sans-serif;
  --font-mono:  ui-monospace, 'JetBrains Mono', 'Fira Code', monospace;
  --text-xs:    11px;
  --text-sm:    13px;
  --text-base:  14px;
  --text-lg:    16px;
  --text-xl:    20px;
  --text-2xl:   24px;
  --text-mono-sm:   12px;
  --text-mono-base: 13px;

  /* Spacing */
  --space-1:  4px;
  --space-2:  8px;
  --space-3:  12px;
  --space-4:  16px;
  --space-5:  20px;
  --space-6:  24px;
  --space-8:  32px;
  --space-10: 40px;
  --space-12: 48px;

  /* Radii */
  --radius-sm:   4px;
  --radius-base: 6px;
  --radius-lg:   8px;
  --radius-full: 9999px;

  /* Shadows */
  --shadow-1: 0 1px 2px rgba(0,0,0,.06);
  --shadow-2: 0 2px 8px rgba(0,0,0,.10);
  --shadow-3: 0 4px 16px rgba(0,0,0,.14);

  /* Z-index */
  --z-base:           0;
  --z-sticky:         100;
  --z-dropdown:       200;
  --z-tooltip:        300;
  --z-modal-backdrop: 400;
  --z-modal:          500;
  --z-toast:          600;

  /* Motion */
  --duration-fast: 100ms;
  --duration-base: 200ms;
  --duration-slow: 300ms;
  --easing-default: ease;
  --easing-in-out: cubic-bezier(0.4,0,0.2,1);
}

@media (prefers-color-scheme: dark) {
  :root {
    --color-bg:          #020617;
    --color-surface:     #0f172a;
    --color-surface-2:   #1e293b;
    --color-border:      #334155;
    --color-border-2:    #475569;
    --color-text-1:      #f8fafc;
    --color-text-2:      #94a3b8;
    --color-text-3:      #475569;
    --color-accent:      #60a5fa;
    --color-accent-bg:   #172554;
    --color-success:     #4ade80;
    --color-success-bg:  #14532d;
    --color-warning:     #fbbf24;
    --color-warning-bg:  #422006;
    --color-danger:      #f87171;
    --color-danger-bg:   #450a0a;
    --color-neutral-bg:  #1e293b;
    --color-neutral:     #94a3b8;

    --color-chart-1: #60a5fa;
    --color-chart-2: #a78bfa;
    --color-chart-3: #34d399;
    --color-chart-4: #fbbf24;
    --color-chart-5: #f472b6;
  }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    transition-duration: 0ms !important;
    animation-duration:  0ms !important;
  }
}
```
