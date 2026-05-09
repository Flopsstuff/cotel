import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import useSWR from 'swr'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import { fetcher, type OverviewResponse, type SessionItem } from '../api'
import { Card, KpiCard, DataTable, EmptyState, ErrorState, RefreshIndicator, KpiSkeleton, ChartSkeleton, LoadingSkeleton, sessionStatusBadge, ChartTooltip } from '../components'
import styles from './Overview.module.css'

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

export default function Overview() {
  const [paused, setPaused] = useState(false)
  const navigate = useNavigate()

  const { data, error, isLoading, isValidating, mutate } = useSWR<OverviewResponse>(
    '/api/v1/overview',
    fetcher,
    { refreshInterval: paused ? 0 : 30_000 },
  )

  const { data: recent, isLoading: recentLoading } = useSWR<{ items: SessionItem[]; total: number; page: number; limit: number }>(
    '/api/v1/sessions?page=1&limit=5&sort=start_time&order=desc',
    fetcher,
  )

  return (
    <div>
      <div className={styles.header}>
        <h1 className={styles.title}>Overview</h1>
        <RefreshIndicator
          isValidating={isValidating}
          paused={paused}
          onToggle={() => setPaused((p) => !p)}
        />
      </div>

      {isLoading ? (
        <>
          <KpiSkeleton />
          <div className={styles.chartCard}><ChartSkeleton /></div>
        </>
      ) : error ? (
        <ErrorState message={error.message} onRetry={() => mutate()} />
      ) : data ? (
        <>
          <div className={styles.kpiRow}>
            <KpiCard label="Sessions" value={String(data.sessions_count)} />
            <KpiCard label="Total Cost (30d)" value={`$${data.total_cost_usd.toFixed(4)}`} />
            <KpiCard label="Input Tokens (30d)" value={fmtTokens(data.total_input_tokens)} />
            <KpiCard label="Output Tokens (30d)" value={fmtTokens(data.total_output_tokens)} />
          </div>

          <Card title="Daily Cost — last 30 days">
            {data.daily_costs.length === 0 ? (
              <EmptyState heading="No cost data yet" subtext="Costs will appear once sessions are recorded." />
            ) : (
              <ResponsiveContainer width="100%" height={200}>
                <BarChart data={data.daily_costs} margin={{ top: 4, right: 8, bottom: 4, left: 0 }}>
                  <XAxis dataKey="date" tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} tickFormatter={(d) => d.slice(5)} />
                  <YAxis tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} tickFormatter={(v) => `$${v.toFixed(2)}`} width={52} />
                  <Tooltip content={<ChartTooltip formatter={(v) => [`$${v.toFixed(4)}`, 'Cost']} />} />
                  <Bar dataKey="cost_usd" fill="var(--color-chart-1)" radius={[2, 2, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </Card>

          <div className={styles.tableRow}>
            <Card title="Top Models">
              {data.top_models.length === 0 ? (
                <EmptyState heading="No model data" />
              ) : (
                <DataTable<{ model: string; span_count: number }>
                  columns={[
                    { key: 'model', label: 'Model', sortable: true },
                    { key: 'span_count', label: 'Spans', sortable: true },
                  ]}
                  rows={data.top_models}
                />
              )}
            </Card>
            <Card title="Top Tools">
              {data.top_tools.length === 0 ? (
                <EmptyState heading="No tool data" />
              ) : (
                <DataTable<{ tool_name: string; call_count: number }>
                  columns={[
                    { key: 'tool_name', label: 'Tool', sortable: true },
                    { key: 'call_count', label: 'Calls', sortable: true },
                  ]}
                  rows={data.top_tools}
                />
              )}
            </Card>
          </div>
        </>
      ) : null}

      <Card title="Recent Sessions">
        {recentLoading ? (
          <LoadingSkeleton rows={5} height={36} />
        ) : !recent || recent.items.length === 0 ? (
          <EmptyState heading="No sessions yet" subtext="Start using cotel to record sessions." />
        ) : (
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
                  key: 'first_seen',
                  label: 'Started',
                  render: (v) => new Date(String(v)).toLocaleString(),
                },
                {
                  key: 'model',
                  label: 'Model',
                },
                {
                  key: 'cost_usd',
                  label: 'Cost',
                  render: (v) => `$${Number(v).toFixed(4)}`,
                },
                {
                  key: 'status',
                  label: 'Status',
                  render: (v) => sessionStatusBadge(String(v)),
                },
              ]}
              rows={recent.items}
              onRowClick={(row) => navigate(`/sessions/${row.session_id}`)}
            />
            <div className={styles.viewAll}>
              <Link to="/sessions" className={styles.viewAllLink}>View all sessions →</Link>
            </div>
          </>
        )}
      </Card>
    </div>
  )
}
