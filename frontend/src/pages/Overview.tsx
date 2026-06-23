import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  BarChart, Bar, LineChart, Line, AreaChart, Area,
  XAxis, YAxis, Tooltip, ResponsiveContainer,
} from 'recharts'
import {
  useOverview, useSessions, useCosts, useHistory, useTools, useModels,
} from '../api'
import type { SessionItem, ToolItem, ModelItem } from '../api'
import {
  Card, KpiCard, DataTable, EmptyState, ErrorState, RefreshIndicator,
  KpiSkeleton, ChartSkeleton, LoadingSkeleton, sessionStatusBadge, failRateBadge, ChartTooltip,
} from '../components'
import { UserSearch } from '../components/UserSearch'
import { StatSection } from '../components/StatSection'
import styles from './Overview.module.css'

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}

// ---- Compact section components ----

function SessionsSection({ userId }: { userId?: string }) {
  const navigate = useNavigate()
  const { data, isLoading, error } = useSessions(1, 5, 'start_time', 'desc', userId)

  if (isLoading) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.items.length === 0)
    return <EmptyState heading="No sessions" subtext="Start using cotel to record sessions." />

  return (
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
  )
}

function HistorySection({ userId }: { userId?: string }) {
  const from = useMemo(() => isoDate(new Date(Date.now() - 30 * 86400_000)), [])
  const to = useMemo(() => isoDate(new Date()), [])
  const { data, isLoading, error } = useHistory('day', from, to, userId)

  if (isLoading) return <ChartSkeleton />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.buckets.length === 0)
    return <EmptyState heading="No activity data" subtext="Activity will appear once sessions are recorded." />

  return (
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
  )
}

function CostsSection({ userId }: { userId?: string }) {
  const { data, isLoading, error } = useCosts(undefined, undefined, userId)

  if (isLoading) return <ChartSkeleton />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.daily.length === 0)
    return <EmptyState heading="No cost data" subtext="Costs will appear once sessions are recorded." />

  const topModels = data.by_model.slice(0, 3)

  return (
    <div>
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
      {topModels.length > 0 && (
        <DataTable<{ model: string; cost_usd: number }>
          columns={[
            { key: 'model', label: 'Model', sortable: true },
            {
              key: 'cost_usd',
              label: 'Cost',
              sortable: true,
              render: (v) => `$${Number(v).toFixed(2)}`,
            },
          ]}
          rows={topModels}
        />
      )}
    </div>
  )
}

function ToolsSection({ userId }: { userId?: string }) {
  const { data, isLoading, error } = useTools(userId)

  if (isLoading) return <LoadingSkeleton rows={5} height={36} />
  if (error) return <ErrorState message={error.message} />
  if (!data || data.items.length === 0)
    return <EmptyState heading="No tool data" subtext="Tool usage will appear once sessions are recorded." />

  const top5 = data.items.slice().sort((a, b) => b.calls - a.calls).slice(0, 5)

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
      rows={top5}
    />
  )
}

function ModelsSection({ userId }: { userId?: string }) {
  const { data, isLoading, error } = useModels(userId)

  const rows = useMemo(() => {
    if (!data) return []
    return [...data.items].sort((a, b) => b.span_count - a.span_count)
  }, [data])

  const maxCost = useMemo(() => Math.max(...rows.map((r) => r.total_cost_usd), 1), [rows])

  if (isLoading) return <LoadingSkeleton rows={5} height={36} />
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

// ---- Main page ----

export default function Overview() {
  const [paused, setPaused] = useState(false)
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') ?? undefined

  const { data, error, isLoading, isValidating, mutate } = useOverview(paused ? 0 : 30_000, userId)

  const userParam = userId ? `?user_id=${encodeURIComponent(userId)}` : ''

  return (
    <div>
      <div className={styles.header}>
        <h1 className={styles.title}>Overview</h1>
        <div className={styles.headerRight}>
          <RefreshIndicator
            isValidating={isValidating}
            paused={paused}
            onToggle={() => setPaused((p) => !p)}
          />
        </div>
      </div>

      <UserSearch />

      {isLoading ? (
        <>
          <KpiSkeleton />
          <div className={styles.chartCard}><ChartSkeleton /></div>
        </>
      ) : error ? (
        <ErrorState message={error.message} onRetry={() => mutate()} />
      ) : data ? (
        <div className={styles.kpiRow}>
          <KpiCard label="Sessions (30d)" value={String(data.sessions_count)} />
          <KpiCard label="Users" value={String(data.users_count)} />
          <KpiCard label="Total Cost (30d)" value={`$${data.total_cost_usd.toFixed(2)}`} />
          <KpiCard label="Input Tokens (30d)" value={fmtTokens(data.total_input_tokens)} />
          <KpiCard label="Output Tokens (30d)" value={fmtTokens(data.total_output_tokens)} />
        </div>
      ) : null}

      <StatSection title="Sessions" viewAllHref={`/sessions${userParam}`}>
        <SessionsSection userId={userId} />
      </StatSection>

      <StatSection title="History" viewAllHref={`/history${userParam}`}>
        <HistorySection userId={userId} />
      </StatSection>

      <StatSection title="Costs" viewAllHref={`/costs${userParam}`}>
        <CostsSection userId={userId} />
      </StatSection>

      <StatSection title="Tools" viewAllHref={`/tools${userParam}`}>
        <ToolsSection userId={userId} />
      </StatSection>

      <StatSection title="Models" viewAllHref={`/models${userParam}`}>
        <ModelsSection userId={userId} />
      </StatSection>
    </div>
  )
}
