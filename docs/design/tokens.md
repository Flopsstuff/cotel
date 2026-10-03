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

Ordered list used for multi-series charts (tool breakdowns, model comparisons). Every pair separates under colour-vision deficiency, and the series keep the same loudness order in both schemes. The two rules below say what that means, on what ruler, and what the palette measures today.

| Token | Light value | Dark value | Usage |
|---|---|---|---|
| `--color-chart-1` | `#2563eb` | `#60a5fa` | Primary series (matches accent) |
| `--color-chart-2` | `#8373bc` | `#8b6eda` | Second series |
| `--color-chart-3` | `#046642` | `#01d699` | Third series |
| `--color-chart-4` | `#b36519` | `#c89716` | Fourth series |
| `--color-chart-5` | `#e4307e` | `#e965ab` | Fifth series |

Use `--color-chart-1` → `--color-chart-5` in order; do not skip. When more than 5 series exist, group the tail as "Other" (use `--color-text-3`).

The dark values are **not** the light values lightened. Rule 1 makes the dark scheme's lightness order the reverse of the light scheme's, because the surface flips underneath: against white a token is loud by being dark, against `#0f172a` it is loud by being light. A token that reads quiet in one scheme is therefore a *lighter* colour in light mode and a *darker* one in dark mode.

### The ruler

Both rules below are measured, not argued, so a proposed value can be checked before it ships. Simulate colour-vision deficiency with **Machado 2009 at severity 1.0** applied to linearised sRGB, then take **Euclidean distance in OKLab × 100** — that is what "ΔE" means on this page. Lightness is OKLab `L`. Salience is WCAG contrast against `--color-surface` (`#ffffff` light, `#0f172a` dark).

The ruler is a pinned contract, not a recipe to reimplement: [ADR-0017](../decisions/0017-chart-palette-ruler-is-pinned.md) makes its matrices and constants normative in `internal/design/palette.go`, and the Go test beside them fails CI when either rule breaks. **Both tables below are generated output** — regenerate them with `go test ./internal/design/... -update` instead of editing rows by hand.

There is no lightness ceiling. A bare `L` cap was proposed and rejected: it reads a symptom of rule 1 off a single token while leaving the same fault in the neighbouring ones.

### Rule 1 — salience order is the same in both schemes

A token's rank by contrast against the surface is a promise about how loud that series is. The ranks must agree across schemes, or the same chart puts a different series on top depending on the reader's theme.

| Token | Light contrast | Rank | Dark contrast | Rank |
|---|---|---|---|---|
| `--color-chart-1` | 5.17:1 | 2 | 7.02:1 | 2 |
| `--color-chart-2` | 4.08:1 | 5 | 4.54:1 | 5 |
| `--color-chart-3` | 7.04:1 | 1 | 9.42:1 | 1 |
| `--color-chart-4` | 4.38:1 | 3 | 6.71:1 | 3 |
| `--color-chart-5` | 4.18:1 | 4 | 5.88:1 | 4 |

The ranks agree. Where two of these share one plot — spans in `chart-1` against cost in `chart-4` on the Overview — the primary series is the louder one in both schemes; before this palette the dark scheme promoted cost over spans and the light scheme did not.

The floor is 4:1, a third above the 3:1 WCAG 1.4.11 asks of a 2 px stroke. `chart-1` cannot be the loudest token: it is pinned to `--color-accent`, and rule 2 needs the other four spread far enough in lightness that two of them must land outside it on either side. Rank 2 of 5 is the loudest position available to it.

### Rule 2 — any two tokens that can share a plot stay ΔE ≥ 8 apart

Because the palette is used in order, "can share a plot" means every pair. The bar of 8 is this project's own, set in ADR-0015. Worst case over protanopia, deuteranopia and tritanopia, with the simulation that produced it:

| Pair | Light | Dark |
|---|---|---|
| 1–2 | 11.4 (protan) | 9.7 (deutan) |
| 1–3 | 12.9 (tritan) | 8.4 (tritan) |
| 1–4 | 31.6 (protan) | 24.2 (tritan) |
| 1–5 | 18.3 (protan) | 13.0 (deutan) |
| 2–3 | 17.4 (tritan) | 23.2 (tritan) |
| 2–4 | 16.0 (tritan) | 16.0 (tritan) |
| 2–5 | 10.7 (protan) | 9.2 (protan) |
| 3–4 | 8.6 (protan) | 12.2 (deutan) |
| 3–5 | 9.4 (protan) | 9.5 (deutan) |
| 4–5 | 10.0 (deutan) | 9.3 (tritan) |

The worst pair in the product is 8.4 — `1–3` under tritanopia in dark, with `3–4` under protanopia in light next at 8.6. Treat both as "passing, with no room". The previous palette put `chart-1` and `chart-2` at 0.4 and 0.3 — the same colour to a deuteranope — and since the palette is handed out in order, that was the pair every two-series chart drew.

ΔE 8 is "tellable apart", not "comfortable". It does not license colour as the only channel: a chart whose series must be identified at a glance still earns a direct label or a different mark per series. It licenses the legend swatch to work for a reader who has one.

### How the values were derived

Re-deriving the palette means solving both rules at once; nudging one token cannot do it. The constraints a replacement value must satisfy:

- `chart-1` is pinned — it is `--color-accent`, and it is the one chart token with product meaning.
- Hue family per index is kept: blue, violet, green, amber, pink. Every value here is within 3° of the hue it replaces; only lightness and chroma moved.
- ≥ 4:1 against the surface, and chroma ≥ 0.10 in OKLab. The chroma floor is what keeps a quiet token a colour rather than a tint — a pale near-neutral reads as "no series assigned", which is how an earlier candidate palette failed by eye while passing both rules on paper.
- `chart-4` must no longer be byte-identical to `--color-warning`, which it was in both schemes.

The Tailwind ramps the rest of this file is built from cannot satisfy this: at a 4:1 floor no assignment of their shades meets both rules, because a ramp steps lightness for one surface and rule 1 needs the two schemes ordered oppositely. These ten values are therefore off-ramp, and are the only colour tokens in the file that are.

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
  --color-chart-2: #8373bc;
  --color-chart-3: #046642;
  --color-chart-4: #b36519;
  --color-chart-5: #e4307e;

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
    --color-chart-2: #8b6eda;
    --color-chart-3: #01d699;
    --color-chart-4: #c89716;
    --color-chart-5: #e965ab;
  }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    transition-duration: 0ms !important;
    animation-duration:  0ms !important;
  }
}
```
