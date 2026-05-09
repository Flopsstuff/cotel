import { useState, useMemo, useCallback } from 'react'
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis, Tooltip,
  ResponsiveContainer, Legend,
} from 'recharts'
import { useHistory } from '../api'
import { useUserContext } from '../context/UserContext'
import type { HistoryResponse, HeatmapCell } from '../api'
import {
  Card, KpiCard, EmptyState, ErrorState, KpiSkeleton, ChartSkeleton, ChartTooltip,
} from '../components'
import styles from './History.module.css'

type Granularity = 'hour' | 'day' | 'week' | 'month'

interface Preset {
  label: string
  days: number
  gran: Granularity
}

const PRESETS: Preset[] = [
  { label: '24h', days: 1, gran: 'hour' },
  { label: '7d',  days: 7, gran: 'day' },
  { label: '30d', days: 30, gran: 'day' },
  { label: '3mo', days: 90, gran: 'week' },
  { label: '1y',  days: 365, gran: 'month' },
]

const MODEL_COLORS = [
  'var(--color-chart-1)',
  'var(--color-chart-2)',
  'var(--color-chart-3)',
  'var(--color-chart-4)',
  'var(--color-chart-5)',
]

const DOW_LABELS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}
function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return isoDate(d)
}
function today(): string {
  return isoDate(new Date())
}
function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

// ---- Calendar Heatmap ----

interface DayInfo {
  date: string
  count: number
  cost: number
}

interface CalHeatmapProps {
  days: DayInfo[]
  from: string
  to: string
}

function heatColor(count: number, max: number): string {
  if (count === 0) return 'var(--color-surface-2)'
  const intensity = Math.min(1, Math.log(count + 1) / Math.log(max + 1))
  if (intensity < 0.25)
    return 'color-mix(in srgb, var(--color-chart-1) 20%, var(--color-surface-2))'
  if (intensity < 0.5)
    return 'color-mix(in srgb, var(--color-chart-1) 45%, var(--color-surface-2))'
  if (intensity < 0.75)
    return 'color-mix(in srgb, var(--color-chart-1) 70%, var(--color-surface-2))'
  return 'var(--color-chart-1)'
}

function CalendarHeatmap({ days, from, to }: CalHeatmapProps) {
  const [tip, setTip] = useState<{ x: number; y: number; info: DayInfo } | null>(null)

  const dataMap = useMemo(() => {
    const m = new Map<string, DayInfo>()
    days.forEach(d => m.set(d.date, d))
    return m
  }, [days])

  const maxCount = useMemo(() => Math.max(1, ...days.map(d => d.count)), [days])

  // Build all dates from→to as UTC date strings
  const allDates = useMemo(() => {
    const result: string[] = []
    const start = new Date(from + 'T00:00:00Z')
    const end = new Date(to + 'T00:00:00Z')
    const cur = new Date(start)
    while (cur <= end) {
      result.push(isoDate(cur))
      cur.setUTCDate(cur.getUTCDate() + 1)
    }
    return result
  }, [from, to])

  // Pad so the grid starts on Sunday (col 0 = Sun)
  const startDow = new Date(allDates[0] + 'T00:00:00Z').getUTCDay()

  const CELL = 14
  const GAP = 3
  const STEP = CELL + GAP
  const totalCells = startDow + allDates.length
  const cols = Math.ceil(totalCells / 7)
  const svgW = cols * STEP
  const svgH = 7 * STEP + 24

  // Compute month label positions
  const monthLabels = useMemo(() => {
    const labels: { x: number; label: string }[] = []
    let lastMonth = ''
    allDates.forEach((date, i) => {
      const col = Math.floor((startDow + i) / 7)
      const colStart = col * STEP
      const month = date.slice(0, 7)
      if (month !== lastMonth) {
        labels.push({
          x: colStart,
          label: new Date(date + 'T00:00:00Z').toLocaleString('en', { month: 'short' }),
        })
        lastMonth = month
      }
    })
    return labels
  }, [allDates, startDow, STEP])

  const handleMouseEnter = useCallback((e: React.MouseEvent<SVGRectElement>, info: DayInfo) => {
    setTip({ x: e.clientX, y: e.clientY, info })
  }, [])

  return (
    <div className={styles.calWrap}>
      <svg width={svgW} height={svgH} className={styles.calSvg} onMouseLeave={() => setTip(null)}>
        {/* Month labels */}
        {monthLabels.map(({ x, label }) => (
          <text key={label + x} x={x} y={12} className={styles.calMonthLabel}>{label}</text>
        ))}
        {/* Cells */}
        {allDates.map((date, i) => {
          const idx = startDow + i
          const col = Math.floor(idx / 7)
          const row = idx % 7
          const x = col * STEP
          const y = row * STEP + 20
          const info: DayInfo = dataMap.get(date) ?? { date, count: 0, cost: 0 }
          return (
            <rect
              key={date}
              x={x} y={y}
              width={CELL} height={CELL}
              rx={2}
              style={{ fill: heatColor(info.count, maxCount), cursor: 'default' }}
              onMouseEnter={(e) => handleMouseEnter(e, info)}
              onMouseMove={(e) => setTip(t => t ? { ...t, x: e.clientX, y: e.clientY } : null)}
            />
          )
        })}
      </svg>
      {/* DOW axis */}
      <div className={styles.calDow}>
        {DOW_LABELS.map(d => <span key={d}>{d}</span>)}
      </div>
      {/* Legend */}
      <div className={styles.calLegend}>
        <span className={styles.calLegendLabel}>Less</span>
        {[0, 0.2, 0.45, 0.7, 1].map((v, i) => (
          <div
            key={i}
            className={styles.calLegendCell}
            style={{
              background: v === 0
                ? 'var(--color-surface-2)'
                : `color-mix(in srgb, var(--color-chart-1) ${Math.round(v * 100)}%, var(--color-surface-2))`,
            }}
          />
        ))}
        <span className={styles.calLegendLabel}>More</span>
      </div>
      {tip && (
        <div
          className={styles.floatTip}
          style={{ left: tip.x + 12, top: tip.y - 44 }}
        >
          <strong>{tip.info.date}</strong>
          <br />
          {tip.info.count} spans · ${tip.info.cost.toFixed(4)}
        </div>
      )}
    </div>
  )
}

// ---- Hour-of-Day × Day-of-Week heatmap ----

interface HourDowHeatmapProps {
  heatmap: HeatmapCell[]
}

function HourDowHeatmap({ heatmap }: HourDowHeatmapProps) {
  const [tip, setTip] = useState<{ x: number; y: number; dow: number; hour: number; count: number } | null>(null)

  const grid = useMemo(() => {
    // grid[dow][hour] = count
    const g: number[][] = Array.from({ length: 7 }, () => new Array(24).fill(0))
    heatmap.forEach(cell => {
      const dow = new Date(cell.date + 'T00:00:00Z').getUTCDay()
      g[dow][cell.hour] += Number(cell.count)
    })
    return g
  }, [heatmap])

  const maxCount = useMemo(() =>
    Math.max(1, ...grid.flatMap(row => row)),
    [grid],
  )

  const hours = Array.from({ length: 24 }, (_, i) => i)

  return (
    <div className={styles.hourWrap} onMouseLeave={() => setTip(null)}>
      <div className={styles.hourGrid}>
        {/* Header: hours */}
        <div className={styles.hourRowLabel} />
        {hours.map(h => (
          <div key={h} className={styles.hourColLabel}>
            {h % 3 === 0 ? `${h}h` : ''}
          </div>
        ))}
        {/* Rows: DOW */}
        {DOW_LABELS.map((day, dow) => (
          <>
            <div key={`label-${day}`} className={styles.hourRowLabel}>{day}</div>
            {hours.map(h => {
              const count = grid[dow][h]
              return (
                <div
                  key={`${dow}-${h}`}
                  className={styles.hourCell}
                  style={{ background: heatColor(count, maxCount) }}
                  onMouseEnter={(e) => setTip({ x: e.clientX, y: e.clientY, dow, hour: h, count })}
                  onMouseMove={(e) => setTip(t => t ? { ...t, x: e.clientX, y: e.clientY } : null)}
                />
              )
            })}
          </>
        ))}
      </div>
      {tip && (
        <div
          className={styles.floatTip}
          style={{ left: tip.x + 12, top: tip.y - 44 }}
        >
          <strong>{DOW_LABELS[tip.dow]} {tip.hour}:00</strong>
          <br />
          {tip.count} spans
        </div>
      )}
    </div>
  )
}

// ---- Main page ----

function buildKpis(data: HistoryResponse) {
  const sessions = data.buckets.reduce((s, b) => s + Number(b.sessions), 0)
  const spans = data.buckets.reduce((s, b) => s + Number(b.spans), 0)
  const cost = data.buckets.reduce((s, b) => s + b.cost_usd, 0)
  const days = Math.max(1, data.buckets.length)
  return { sessions, spans, cost, avgCost: cost / days }
}

function pivotByModel(data: HistoryResponse): { rows: Record<string, number | string>[]; models: string[] } {
  const models = [...new Set(data.by_model.map(r => r.model))].slice(0, 5)
  const bucketMap = new Map<string, Record<string, number | string>>()
  data.buckets.forEach(b => {
    bucketMap.set(b.bucket, { bucket: b.bucket })
  })
  data.by_model.forEach(r => {
    if (!models.includes(r.model)) return
    const entry = bucketMap.get(r.bucket)
    if (entry) entry[r.model] = r.cost_usd
  })
  const rows = Array.from(bucketMap.values())
  return { rows, models }
}

function calendarDays(data: HistoryResponse): DayInfo[] {
  const m = new Map<string, DayInfo>()
  data.heatmap.forEach(cell => {
    const existing = m.get(cell.date) ?? { date: cell.date, count: 0, cost: 0 }
    existing.count += Number(cell.count)
    existing.cost += cell.cost_usd
    m.set(cell.date, existing)
  })
  return Array.from(m.values())
}

function tickLabel(gran: Granularity, bucket: string): string {
  if (gran === 'hour') return bucket.slice(11, 16)
  if (gran === 'month') return bucket
  return bucket.slice(5)
}

export default function History() {
  const [gran, setGran] = useState<Granularity>('day')
  const [from, setFrom] = useState(daysAgo(30))
  const [to, setTo] = useState(today())
  const [activePreset, setActivePreset] = useState<string>('30d')
  const { userId } = useUserContext()

  const { data, error, isLoading } = useHistory(gran, from, to, userId)

  const applyPreset = (p: Preset) => {
    setActivePreset(p.label)
    setGran(p.gran)
    setFrom(daysAgo(p.days))
    setTo(today())
  }

  const kpis = useMemo(() => data ? buildKpis(data) : null, [data])
  const { rows: modelRows, models } = useMemo(
    () => data ? pivotByModel(data) : { rows: [], models: [] },
    [data],
  )
  const calDays = useMemo(() => data ? calendarDays(data) : [], [data])

  return (
    <div>
      <div className={styles.header}>
        <h1 className={styles.title}>History</h1>
        <div className={styles.controls}>
          <div className={styles.presets}>
            {PRESETS.map(p => (
              <button
                key={p.label}
                className={`${styles.presetBtn} ${activePreset === p.label ? styles.presetBtnActive : ''}`}
                onClick={() => applyPreset(p)}
              >
                {p.label}
              </button>
            ))}
          </div>
          <div className={styles.granSwitcher}>
            {(['hour', 'day', 'week', 'month'] as Granularity[]).map(g => (
              <button
                key={g}
                className={`${styles.granBtn} ${gran === g ? styles.granBtnActive : ''}`}
                onClick={() => { setGran(g); setActivePreset('') }}
              >
                {g.charAt(0).toUpperCase() + g.slice(1)}
              </button>
            ))}
          </div>
        </div>
      </div>

      {isLoading ? (
        <>
          <KpiSkeleton />
          <ChartSkeleton />
        </>
      ) : error ? (
        <ErrorState message={error.message} />
      ) : !data ? null : (
        <>
          {/* KPI row */}
          {kpis && (
            <div className={styles.kpiRow}>
              <KpiCard label="Sessions" value={String(kpis.sessions)} />
              <KpiCard label="Spans" value={fmtTokens(kpis.spans)} />
              <KpiCard label="Total Cost" value={`$${kpis.cost.toFixed(4)}`} />
              <KpiCard label={`Avg Cost / ${gran}`} value={`$${kpis.avgCost.toFixed(4)}`} />
            </div>
          )}

          {/* Activity over time */}
          <Card title="Activity over Time">
            {data.buckets.length === 0 ? (
              <EmptyState heading="No data in this range" />
            ) : (
              <div className={styles.dualChart}>
                <div className={styles.chartPane}>
                  <p className={styles.chartLabel}>Spans</p>
                  <ResponsiveContainer width="100%" height={160}>
                    <AreaChart data={data.buckets} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
                      <defs>
                        <linearGradient id="grad-spans" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="5%" stopColor="var(--color-chart-1)" stopOpacity={0.25} />
                          <stop offset="95%" stopColor="var(--color-chart-1)" stopOpacity={0} />
                        </linearGradient>
                      </defs>
                      <XAxis
                        dataKey="bucket"
                        tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                        tickFormatter={(b) => tickLabel(gran, b)}
                        interval="preserveStartEnd"
                      />
                      <YAxis tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} width={40} />
                      <Tooltip content={<ChartTooltip formatter={(v) => [String(Math.round(v)), 'Spans']} />} />
                      <Area
                        type="monotone"
                        dataKey="spans"
                        stroke="var(--color-chart-1)"
                        strokeWidth={2}
                        fill="url(#grad-spans)"
                        dot={false}
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                </div>
                <div className={styles.chartPane}>
                  <p className={styles.chartLabel}>Cost (USD)</p>
                  <ResponsiveContainer width="100%" height={160}>
                    <AreaChart data={data.buckets} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
                      <defs>
                        <linearGradient id="grad-cost" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="5%" stopColor="var(--color-chart-3)" stopOpacity={0.25} />
                          <stop offset="95%" stopColor="var(--color-chart-3)" stopOpacity={0} />
                        </linearGradient>
                      </defs>
                      <XAxis
                        dataKey="bucket"
                        tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                        tickFormatter={(b) => tickLabel(gran, b)}
                        interval="preserveStartEnd"
                      />
                      <YAxis
                        tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                        tickFormatter={(v) => `$${v.toFixed(2)}`}
                        width={52}
                      />
                      <Tooltip content={<ChartTooltip formatter={(v) => [`$${v.toFixed(4)}`, 'Cost']} />} />
                      <Area
                        type="monotone"
                        dataKey="cost_usd"
                        stroke="var(--color-chart-3)"
                        strokeWidth={2}
                        fill="url(#grad-cost)"
                        dot={false}
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                </div>
              </div>
            )}
          </Card>

          {/* Calendar heatmap */}
          <Card title="Activity Calendar">
            {calDays.length === 0 ? (
              <EmptyState heading="No data in this range" />
            ) : (
              <CalendarHeatmap days={calDays} from={data.from} to={data.to} />
            )}
          </Card>

          {/* Hour-of-day heatmap */}
          <Card title="Activity by Hour & Day of Week">
            {data.heatmap.length === 0 ? (
              <EmptyState heading="No data in this range" />
            ) : (
              <HourDowHeatmap heatmap={data.heatmap} />
            )}
          </Card>

          {/* Cost by model over time */}
          {models.length > 0 && (
            <Card title="Cost by Model">
              <ResponsiveContainer width="100%" height={220}>
                <BarChart data={modelRows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
                  <XAxis
                    dataKey="bucket"
                    tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                    tickFormatter={(b) => tickLabel(gran, String(b))}
                    interval="preserveStartEnd"
                  />
                  <YAxis
                    tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                    tickFormatter={(v) => `$${v.toFixed(2)}`}
                    width={52}
                  />
                  <Tooltip
                    content={
                      <ChartTooltip
                        formatter={(v, n) => [`$${Number(v).toFixed(4)}`, String(n)]}
                      />
                    }
                  />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  {models.map((model, i) => (
                    <Bar
                      key={model}
                      dataKey={model}
                      stackId="cost"
                      fill={MODEL_COLORS[i % MODEL_COLORS.length]}
                    />
                  ))}
                </BarChart>
              </ResponsiveContainer>
            </Card>
          )}
        </>
      )}
    </div>
  )
}
