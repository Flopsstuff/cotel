import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  LineChart, Line, AreaChart, Area,
  XAxis, YAxis, Tooltip, ResponsiveContainer,
} from 'recharts'
import {
  useOverview, useSessions, useCosts, useHistory, useTools, useModels, useUsersPage,
} from '../api'
import type { SessionItem, ToolItem, ModelItem, User } from '../api'
import {
  KpiCard, DataTable, EmptyState, ErrorState, RefreshIndicator, SegmentedControl,
  KpiSkeleton, ChartSkeleton, LoadingSkeleton, sessionStatusBadge, failRateBadge, ChartTooltip,
} from '../components'
import { StatSection } from '../components/StatSection'
import { RANGE_OPTIONS, RANGE_SUFFIX, useRangeCookie } from '../lib/range'
import type { RangeKey } from '../lib/range'
import styles from './Overview.module.css'

const RANGE_COOKIE = 'cotel_overview_range'
const ANON_ID = '__anonymous__'

// /history is bounded by from/to rather than the range key, so "All" has to name
// a start date. Nothing cotel can store predates this by decades.
const HISTORY_EPOCH = '2000-01-01'

const RANGE_DAYS: Record<RangeKey, number | null> = {
  all: null,
  year: 365,
  month: 30,
  week: 7,
  day: 1,
}

interface SectionProps {
  range: RangeKey
  userId?: string
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}

function formatDay(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

// ---- Compact section components ----

function UsersSection({ range }: { range: RangeKey }) {
  const navigate = useNavigate()
  const { data, isLoading, error } = useUsersPage({
    range,
    sort: 'cost',
    order: 'desc',
    page: 1,
    limit: 5,
  })

  if (isLoading && !data) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.users.length === 0)
    return <EmptyState heading="No users" subtext="Users appear once telemetry is recorded in the selected range." />

  return (
    <DataTable<User>
      columns={[
        {
          key: 'name',
          label: 'Name',
          render: (v, row) => (
            <span className={row.id === ANON_ID ? styles.anonUser : undefined}>{String(v)}</span>
          ),
        },
        { key: 'cost', label: 'Cost', render: (v) => `$${Number(v).toFixed(2)}` },
        { key: 'sessions', label: 'Sessions', render: (v) => Number(v).toLocaleString() },
        {
          key: 'last_seen',
          label: 'Last seen',
          render: (v) => (v ? new Date(String(v)).toLocaleString() : '—'),
        },
      ]}
      rows={data.users}
      onRowClick={(row) => navigate(`/users/${encodeURIComponent(row.id)}`)}
    />
  )
}

function HistorySection({ range, userId, coveredSince }: SectionProps & { coveredSince: string | null }) {
  const days = RANGE_DAYS[range]
  const from = useMemo(
    () => (days === null ? HISTORY_EPOCH : isoDate(new Date(Date.now() - days * 86400_000))),
    [days],
  )
  const to = useMemo(() => isoDate(new Date()), [])
  const { data, isLoading, error } = useHistory(range === 'day' ? 'hour' : 'day', from, to, userId)

  if (isLoading && !data) return <ChartSkeleton />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.buckets.length === 0)
    return <EmptyState heading="No activity data" subtext="Activity will appear once sessions are recorded." />

  return (
    <>
      {coveredSince && (
        <p className={styles.coverageNote}>
          Charted from {formatDay(coveredSince)} — this chart is built from raw spans, and earlier
          days in this range survive only as daily totals.
        </p>
      )}
      <ResponsiveContainer width="100%" height={160}>
        <AreaChart data={data.buckets} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id="grad-hist-spans" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--color-chart-1)" stopOpacity={0.25} />
              <stop offset="95%" stopColor="var(--color-chart-1)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <XAxis
            dataKey="bucket"
            tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
            tickFormatter={(b) => String(b).slice(5)}
            interval="preserveStartEnd"
          />
          <YAxis tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} width={36} />
          <Tooltip content={<ChartTooltip formatter={(v) => [String(Math.round(v as number)), 'Spans']} />} />
          <Area
            type="monotone"
            dataKey="spans"
            stroke="var(--color-chart-1)"
            strokeWidth={2}
            fill="url(#grad-hist-spans)"
            dot={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </>
  )
}

function CostsSection({ range, userId }: SectionProps) {
  const { data, isLoading, error } = useCosts(undefined, undefined, userId, range)

  if (isLoading && !data) return <ChartSkeleton />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.daily.length === 0)
    return <EmptyState heading="No cost data" subtext="Costs will appear once sessions are recorded." />

  return (
    <ResponsiveContainer width="100%" height={140}>
      <LineChart data={data.daily} margin={{ top: 4, right: 8, bottom: 4, left: 0 }}>
        <XAxis
          dataKey="date"
          tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
          tickFormatter={(d) => String(d).slice(5)}
          interval="preserveStartEnd"
        />
        <YAxis
          tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
          tickFormatter={(v) => `$${Number(v).toFixed(2)}`}
          width={52}
        />
        <Tooltip content={<ChartTooltip formatter={(v) => [`$${Number(v).toFixed(2)}`, 'Cost']} />} />
        <Line type="monotone" dataKey="cost_usd" stroke="var(--color-chart-1)" strokeWidth={2} dot={false} />
      </LineChart>
    </ResponsiveContainer>
  )
}

function ToolsSection({ range, userId }: SectionProps) {
  const { data, isLoading, error } = useTools({
    range,
    sort: 'calls',
    order: 'desc',
    page: 1,
    limit: 5,
    user_id: userId,
  })

  if (isLoading && !data) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.items.length === 0)
    return <EmptyState heading="No tool data" subtext="Tool usage will appear once sessions are recorded." />

  return (
    <DataTable<ToolItem>
      columns={[
        { key: 'name', label: 'Tool', sortable: true },
        { key: 'calls', label: 'Calls', sortable: true },
        {
          key: 'avg_duration_ms',
          label: 'Avg Duration',
          sortable: true,
          render: (v) => `${Number(v).toFixed(0)}ms`,
        },
        {
          key: 'fail_rate',
          label: 'Error Rate',
          sortable: true,
          render: (v) => failRateBadge(Number(v)),
        },
      ]}
      rows={data.items}
    />
  )
}

function ModelsSection({ range, userId }: SectionProps) {
  const { data, isLoading, error } = useModels(userId, range)

  const rows = useMemo(() => {
    if (!data) return []
    return [...data.items].sort((a, b) => b.span_count - a.span_count)
  }, [data])

  const maxCost = useMemo(() => Math.max(...rows.map((r) => r.total_cost_usd), 1), [rows])

  if (isLoading && !data) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (rows.length === 0)
    return <EmptyState heading="No model data" subtext="Model usage will appear once sessions are recorded." />

  return (
    <DataTable<ModelItem & { _costBar: number }>
      columns={[
        { key: 'model', label: 'Model', sortable: true },
        { key: 'span_count', label: 'Spans', sortable: true },
        {
          key: 'total_cost_usd',
          label: 'Total Cost',
          sortable: true,
          render: (v) => `$${Number(v).toFixed(2)}`,
        },
        {
          key: '_costBar',
          label: 'Cost (rel)',
          render: (_v, row) => {
            const pct = maxCost > 0 ? (row.total_cost_usd / maxCost) * 100 : 0
            return (
              <div className={styles.barContainer}>
                <div className={styles.bar} style={{ width: `${pct}%` }} />
              </div>
            )
          },
        },
        {
          key: 'total_input_tokens',
          label: 'In Tokens',
          sortable: true,
          render: (v) => Number(v).toLocaleString(),
        },
        {
          key: 'total_output_tokens',
          label: 'Out Tokens',
          sortable: true,
          render: (v) => Number(v).toLocaleString(),
        },
      ]}
      rows={rows.map((r) => ({ ...r, _costBar: r.total_cost_usd }))}
    />
  )
}

function SessionsSection({ range, userId }: SectionProps) {
  const navigate = useNavigate()
  const { data, isLoading, error } = useSessions(1, 5, 'start_time', 'desc', userId, range)

  if (isLoading && !data) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.items.length === 0)
    return <EmptyState heading="No sessions" subtext="Start using cotel to record sessions." />

  return (
    <>
      <DataTable<SessionItem>
        columns={[
          {
            key: 'session_id',
            label: 'Session',
            render: (v) => (
              <Link to={`/sessions/${v}`} className={styles.sessionLink}>
                {String(v).slice(0, 16)}…
              </Link>
            ),
          },
          {
            key: 'user_id',
            label: 'User',
            render: (v) =>
              v ? String(v) : <span className={styles.anonUser}>anonymous</span>,
          },
          {
            key: 'first_seen',
            label: 'Started',
            render: (v) => new Date(String(v)).toLocaleString(),
          },
          { key: 'model', label: 'Model' },
          {
            key: 'cost_usd',
            label: 'Cost',
            render: (v) => `$${Number(v).toFixed(2)}`,
          },
          {
            key: 'status',
            label: 'Status',
            render: (v) => sessionStatusBadge(String(v)),
          },
        ]}
        rows={data.items}
        onRowClick={(row) => navigate(`/sessions/${row.session_id}`)}
      />
      {data.covered_since && (
        <p className={styles.coverageNote}>
          Sessions are listed from {formatDay(data.covered_since)} — earlier days in this range were
          rolled up into daily totals, which carry no per-session start time, model or status. The
          totals above still cover the whole range.
        </p>
      )}
    </>
  )
}

// ---- Main page ----

export default function Overview() {
  const [paused, setPaused] = useState(false)
  const [searchParams, setSearchParams] = useSearchParams()
  const userId = searchParams.get('user_id') ?? undefined
  const [range, setRange] = useRangeCookie(RANGE_COOKIE)

  const { data, error, isLoading, isValidating, mutate } = useOverview(paused ? 0 : 30_000, userId, range)

  // History is charted from raw spans, so it starts at the same raw floor the
  // sessions list reports. Same SWR key as the Sessions section below, so the
  // two share one request rather than issuing two.
  const { data: sessions } = useSessions(1, 5, 'start_time', 'desc', userId, range)
  const coveredSince = sessions?.covered_since ?? null

  const userParam = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  const suffix = RANGE_SUFFIX[range]
  const scoped = (label: string) => (suffix ? `${label} (${suffix})` : label)

  const clearUserFilter = () =>
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete('user_id')
      return next
    })

  return (
    <div>
      <div className={styles.header}>
        <div className={styles.headerLeft}>
          <h1 className={styles.title}>Overview</h1>
          {userId && (
            <button className={styles.userChip} onClick={clearUserFilter} title="Show all users">
              Scoped to: <strong>{userId === ANON_ID ? 'Anonymous' : userId}</strong> ×
            </button>
          )}
        </div>
        <div className={styles.headerRight}>
          <SegmentedControl
            options={RANGE_OPTIONS}
            value={range}
            onChange={setRange}
            ariaLabel="Range for every figure on this page"
          />
          <RefreshIndicator
            isValidating={isValidating}
            paused={paused}
            onToggle={() => setPaused((p) => !p)}
          />
        </div>
      </div>

      {isLoading && !data ? (
        <>
          <KpiSkeleton />
          <div className={styles.chartCard}><ChartSkeleton /></div>
        </>
      ) : error ? (
        <ErrorState message={error.message} onRetry={() => mutate()} />
      ) : data ? (
        <div className={styles.kpiRow}>
          <KpiCard label={scoped('Sessions')} value={String(data.sessions_count)} />
          <KpiCard label={scoped('Active Users')} value={String(data.users_count)} />
          <KpiCard label={scoped('Total Cost')} value={`$${data.total_cost_usd.toFixed(2)}`} />
          <KpiCard label={scoped('Input Tokens')} value={fmtTokens(data.total_input_tokens)} />
          <KpiCard label={scoped('Output Tokens')} value={fmtTokens(data.total_output_tokens)} />
        </div>
      ) : null}

      {!userId && (
        <StatSection title="Users" viewAllHref="/users">
          <UsersSection range={range} />
        </StatSection>
      )}

      <StatSection title="History" viewAllHref={`/history${userParam}`}>
        <HistorySection range={range} userId={userId} coveredSince={coveredSince} />
      </StatSection>

      <StatSection title="Costs" viewAllHref={`/costs${userParam}`}>
        <CostsSection range={range} userId={userId} />
      </StatSection>

      <StatSection title="Tools" viewAllHref={`/tools${userParam}`}>
        <ToolsSection range={range} userId={userId} />
      </StatSection>

      <StatSection title="Models" viewAllHref={`/models${userParam}`}>
        <ModelsSection range={range} userId={userId} />
      </StatSection>

      <StatSection title="Sessions" viewAllHref={`/sessions${userParam}`}>
        <SessionsSection range={range} userId={userId} />
      </StatSection>
    </div>
  )
}
