import { useMemo, useState } from 'react'
import type { HistoryBucket } from '../api'
import type { RangeKey } from '../lib/range'
import { HEAT_STEPS, heatFill, heatScale } from '../lib/heat'
import styles from './ActivityGrid.module.css'

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// Every range draws the same shape — a wide, seven-or-six-row lattice about
// 115px tall — so the block does not resize under a reader who switches range.
// What changes is the two time units the axes carry.
interface GridSpec {
  granularity: string
  cols: number
  rows: number
  cellMs: number
  cellHeight: number
  // Cells run down a column before moving right, the way a GitHub contribution
  // graph reads, except on the week grid: a column there is one hour of the
  // day, so time runs across a row and each row is a whole day.
  rowMajor?: boolean
  // Width reserved for the row labels down the left edge.
  labelWidth: number
  cellLabel: string
  // start is the first cell of the lattice, so that the last cell holds now.
  start: (now: Date) => Date
  colLabel: (t: Date, col: number, cols: number) => string
  rowLabel: (t: Date, row: number) => string
}

// The granularity each range asks /history for: one bucket per cell, so the
// grid never has to re-bucket a series the server already bucketed.
export const GRID_GRANULARITY: Record<RangeKey, string> = {
  all: 'day',
  year: 'day',
  month: '4h',
  week: 'hour',
  day: '10m',
}

const SHORT_MONTH = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const SHORT_DOW = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

function utcMidnight(d: Date): Date {
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()))
}

function utcHour(d: Date): Date {
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate(), d.getUTCHours()))
}

const pad = (n: number) => String(n).padStart(2, '0')

// bucketKey rebuilds the label /history emits for an instant. CAST(start_time
// AS TIMESTAMP) renders the stored TIMESTAMPTZ in UTC whatever the server's
// timezone is, so the whole grid is placed in UTC and every cell start falls on
// a bucket boundary by construction.
function bucketKey(ms: number, cellMs: number): string {
  const d = new Date(ms)
  const date = `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`
  if (cellMs >= DAY) return date
  return `${date} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`
}

const YEAR_SPEC: GridSpec = {
  granularity: 'day',
  cols: 53,
  rows: 7,
  cellMs: DAY,
  cellHeight: 14,
  labelWidth: 30,
  cellLabel: 'day',
  // Anchored on the weekday, so a row is always the same weekday and the last
  // column is the week in progress.
  start: (now) => new Date(utcMidnight(now).getTime() - (52 * 7 + now.getUTCDay()) * DAY),
  colLabel: (t) => (t.getUTCDate() <= 7 ? SHORT_MONTH[t.getUTCMonth()] : ''),
  rowLabel: (_t, row) => (row % 2 === 1 ? SHORT_DOW[row] : ''),
}

const MONTH_SPEC: GridSpec = {
  granularity: '4h',
  cols: 31,
  rows: 6,
  cellMs: 4 * HOUR,
  cellHeight: 17,
  labelWidth: 30,
  cellLabel: '4 hours',
  start: (now) => new Date(utcMidnight(now).getTime() - 30 * DAY),
  colLabel: (t, col, cols) => {
    if (t.getUTCDate() === 1) return SHORT_MONTH[t.getUTCMonth()]
    return (cols - 1 - col) % 3 === 0 ? String(t.getUTCDate()) : ''
  },
  rowLabel: (t) => `${pad(t.getUTCHours())}h`,
}

const WEEK_SPEC: GridSpec = {
  granularity: 'hour',
  cols: 24,
  rows: 7,
  cellMs: HOUR,
  cellHeight: 14,
  rowMajor: true,
  labelWidth: 52,
  cellLabel: 'hour',
  start: (now) => new Date(utcMidnight(now).getTime() - 6 * DAY),
  colLabel: (t, col) => (col % 3 === 0 ? `${t.getUTCHours()}h` : ''),
  rowLabel: (t) => `${SHORT_DOW[t.getUTCDay()]} ${t.getUTCDate()}`,
}

const DAY_SPEC: GridSpec = {
  granularity: '10m',
  cols: 24,
  rows: 6,
  cellMs: 10 * MINUTE,
  cellHeight: 17,
  labelWidth: 30,
  cellLabel: '10 minutes',
  start: (now) => new Date(utcHour(now).getTime() - 23 * HOUR),
  colLabel: (t, col) => (col % 3 === 0 ? `${t.getUTCHours()}h` : ''),
  rowLabel: (t) => `:${pad(t.getUTCMinutes())}`,
}

const SPECS: Record<RangeKey, GridSpec> = {
  // All keeps the year lattice: an unbounded window has no grid of its own, and
  // a year of days is the coarsest cell the four ranges use.
  all: YEAR_SPEC,
  year: YEAR_SPEC,
  month: MONTH_SPEC,
  week: WEEK_SPEC,
  day: DAY_SPEC,
}

// Lower bound of the window /history answered, matching rangeSince server-side.
// A cell that ends at or before it was never queried, and is drawn as absent
// rather than as an empty cell — the grid must not show a zero it did not ask
// for.
const WINDOW_MS: Record<RangeKey, number> = {
  all: Infinity,
  year: 365 * DAY,
  month: 30 * DAY,
  week: 7 * DAY,
  day: DAY,
}

interface Cell {
  col: number
  row: number
  t: number
  count: number
  covered: boolean
}

function cellTitle(t: number, cellMs: number): string {
  const date = new Date(t).toLocaleDateString('en-GB', {
    timeZone: 'UTC', weekday: 'short', day: 'numeric', month: 'short',
  })
  if (cellMs >= DAY) return `${date} (UTC)`
  const clock = (ms: number) =>
    new Date(ms).toLocaleTimeString('en-GB', {
      timeZone: 'UTC', hour: '2-digit', minute: '2-digit', hour12: false,
    })
  return `${date}, ${clock(t)}–${clock(t + cellMs)} UTC`
}

export interface ActivityGridProps {
  range: RangeKey
  buckets: HistoryBucket[]
}

export function ActivityGrid({ range, buckets }: ActivityGridProps) {
  const [tip, setTip] = useState<{ x: number; y: number; cell: Cell } | null>(null)
  const spec = SPECS[range]

  const { cells, fill, max } = useMemo(() => {
    // Re-anchored on each payload rather than on each render, so the lattice
    // holds still between refreshes instead of sliding under the cursor.
    const now = Date.now()
    const counts = new Map<string, number>()
    buckets.forEach((b) => counts.set(b.bucket, Number(b.spans)))

    const start = spec.start(new Date(now)).getTime()
    const windowStart = now - WINDOW_MS[range]
    const out: Cell[] = []
    let max = 0
    for (let i = 0; i < spec.cols * spec.rows; i++) {
      const t = start + i * spec.cellMs
      const count = counts.get(bucketKey(t, spec.cellMs)) ?? 0
      out.push({
        col: spec.rowMajor ? i % spec.cols : Math.floor(i / spec.rows),
        row: spec.rowMajor ? Math.floor(i / spec.cols) : i % spec.rows,
        t,
        count,
        covered: t + spec.cellMs > windowStart && t <= now,
      })
      if (count > max) max = count
    }
    // The scale is cut on the cells actually drawn, not on the whole response:
    // on `all` the series runs further back than the lattice reaches.
    return { cells: out, fill: heatScale(out.filter((c) => c.covered).map((c) => c.count)), max }
  }, [buckets, range, spec])

  // Labels read off the lattice itself, so they cannot drift from the cells.
  const colLabels = cells
    .filter((c) => c.row === 0)
    .map((c) => ({ col: c.col, text: spec.colLabel(new Date(c.t), c.col, spec.cols) }))
    .filter((l) => l.text)
  const rowLabels = cells
    .filter((c) => c.col === 0)
    .map((c) => ({ row: c.row, text: spec.rowLabel(new Date(c.t), c.row) }))
    .filter((l) => l.text)

  return (
    <div className={styles.wrap}>
      <div className={styles.scroll} onMouseLeave={() => setTip(null)}>
        <div
          className={styles.grid}
          role="img"
          aria-label={`Span activity, one cell per ${spec.cellLabel}, in UTC`}
          style={{
            gridTemplateColumns: `${spec.labelWidth}px repeat(${spec.cols}, minmax(0, 1fr))`,
            gridTemplateRows: `14px repeat(${spec.rows}, ${spec.cellHeight}px)`,
          }}
        >
          {colLabels.map(({ col, text }) => (
            <div key={`c${col}`} className={styles.colLabel} style={{ gridColumn: col + 2, gridRow: 1 }}>
              {text}
            </div>
          ))}
          {rowLabels.map(({ row, text }) => (
            <div key={`r${row}`} className={styles.rowLabel} style={{ gridColumn: 1, gridRow: row + 2 }}>
              {text}
            </div>
          ))}
          {cells.map((cell) => (
            <div
              key={cell.t}
              className={cell.covered ? styles.cell : styles.cellAbsent}
              style={{
                gridColumn: cell.col + 2,
                gridRow: cell.row + 2,
                background: cell.covered ? fill(cell.count) : undefined,
              }}
              onMouseEnter={(e) => cell.covered && setTip({ x: e.clientX, y: e.clientY, cell })}
              onMouseMove={(e) => setTip((prev) => (prev ? { ...prev, x: e.clientX, y: e.clientY } : null))}
            />
          ))}
        </div>
      </div>

      <div className={styles.footer}>
        <span className={styles.legendLabel}>Less</span>
        {HEAT_STEPS.map((pct) => (
          <div key={pct} className={styles.legendCell} style={{ background: heatFill(pct) }} />
        ))}
        <span className={styles.legendLabel}>More</span>
        <span className={styles.footNote}>
          one cell = {spec.cellLabel}, UTC · busiest {max.toLocaleString()} span{max === 1 ? '' : 's'}
        </span>
      </div>

      {tip && (
        <div className={styles.floatTip} style={{ left: tip.x + 12, top: tip.y - 44 }}>
          <strong>{cellTitle(tip.cell.t, spec.cellMs)}</strong>
          <br />
          {tip.cell.count.toLocaleString()} span{tip.cell.count === 1 ? '' : 's'}
        </div>
      )}
    </div>
  )
}
