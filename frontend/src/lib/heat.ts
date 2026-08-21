// Shared fill scale for every cell grid in the dashboard: the History calendar
// and hour-of-day heatmaps and the Overview activity grid all read against the
// same five steps, so a reader who learns the scale on one page keeps it on the
// next.

// Percentages of --color-chart-1 mixed over the empty-cell colour. Four filled
// steps plus the empty one is what GitHub's contribution graph uses, and it is
// about as many as the eye separates reliably at this cell size.
const FILLED = [20, 45, 70, 100]

export const HEAT_STEPS = [0, ...FILLED]

export function heatFill(pct: number): string {
  if (pct <= 0) return 'var(--color-surface-2)'
  if (pct >= 100) return 'var(--color-chart-1)'
  return `color-mix(in srgb, var(--color-chart-1) ${pct}%, var(--color-surface-2))`
}

// heatScale cuts the counts in view at their own quartiles, so each step holds
// about a quarter of the busy cells whatever the shape of the distribution.
// Scaling against the maximum instead — linearly or on a log — puts most of a
// working week in the top step and reads as one flat block: a day of 200 spans
// and a day of 700 are both "busy" against a 722-span peak, which is exactly the
// difference the grid exists to show.
export function heatScale(counts: number[]): (count: number) => string {
  const busy = counts.filter((c) => c > 0).sort((a, b) => a - b)
  if (busy.length === 0) return () => heatFill(0)

  // A run with no spread has no quartiles to cut at; every busy cell is the peak.
  if (busy[0] === busy[busy.length - 1]) {
    return (count) => heatFill(count > 0 ? 100 : 0)
  }

  const cuts = [0.25, 0.5, 0.75].map((p) => busy[Math.floor((busy.length - 1) * p)])
  return (count) => {
    if (count <= 0) return heatFill(0)
    let step = 0
    while (step < cuts.length && count > cuts[step]) step++
    return heatFill(FILLED[step])
  }
}
