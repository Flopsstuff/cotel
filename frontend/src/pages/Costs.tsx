import { useState } from 'react'
import { useCosts } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, DateRangePicker, ChartTooltip } from '../components'
import type { CostsResponse } from '../api'
import {
  LineChart, Line, BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Legend,
} from 'recharts'
import styles from './Costs.module.css'

type ModelRow = CostsResponse['by_model'][number]
type TopSessionRow = CostsResponse['top_sessions'][number]

function today() {
  return new Date().toISOString().slice(0, 10)
}
function daysAgo(n: number) {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}

export default function Costs() {
  const [from, setFrom] = useState(daysAgo(30))
  const [to, setTo] = useState(today())

  const { data, error, isLoading } = useCosts(from, to)

  const totalCost = data?.by_model.reduce((s, m) => s + m.cost_usd, 0) ?? 0

  return (
    <div>
      <div className={styles.header}>
        <h1 className={styles.title}>Costs</h1>
        <DateRangePicker from={from} to={to} onChange={(f, t) => { setFrom(f); setTo(t) }} />
      </div>

      {isLoading ? (
        <>
          <div className={styles.chartBlock}><ChartSkeleton /></div>
          <LoadingSkeleton rows={5} height={36} />
        </>
      ) : error ? (
        <ErrorState message={error.message} />
      ) : !data ? null : (
        <>
          <Card title="Daily Cost">
            {data.daily.length === 0 ? (
              <EmptyState heading="No cost data" subtext="No costs recorded in this date range." />
            ) : (
              <ResponsiveContainer width="100%" height={220}>
                <LineChart data={data.daily} margin={{ top: 4, right: 8, bottom: 4, left: 0 }}>
                  <XAxis dataKey="date" tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} tickFormatter={(d) => d.slice(5)} />
                  <YAxis tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} tickFormatter={(v) => `$${v.toFixed(2)}`} width={52} />
                  <Tooltip content={<ChartTooltip formatter={(v) => [`$${v.toFixed(4)}`, 'Cost']} />} />
                  <Line type="monotone" dataKey="cost_usd" stroke="var(--color-chart-1)" strokeWidth={2} dot={false} />
                </LineChart>
              </ResponsiveContainer>
            )}
          </Card>

          <Card title="Cost by Model">
            {data.by_model.length === 0 ? (
              <EmptyState heading="No model data" />
            ) : (
              <ResponsiveContainer width="100%" height={200}>
                <BarChart data={data.by_model} layout="vertical" margin={{ top: 4, right: 8, bottom: 4, left: 0 }}>
                  <XAxis type="number" tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} tickFormatter={(v) => `$${v.toFixed(2)}`} />
                  <YAxis type="category" dataKey="model" tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} width={160} />
                  <Tooltip content={<ChartTooltip formatter={(v) => [`$${v.toFixed(4)}`, 'Cost']} />} />
                  <Bar dataKey="cost_usd" fill="var(--color-chart-1)" radius={[0, 2, 2, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </Card>

          <Card title="Model Summary">
            {data.by_model.length === 0 ? (
              <EmptyState heading="No model data" />
            ) : (
              <DataTable<ModelRow>
                columns={[
                  { key: 'model', label: 'Model', sortable: true },
                  {
                    key: 'cost_usd',
                    label: 'Total Cost',
                    sortable: true,
                    render: (v) => `$${Number(v).toFixed(4)}`,
                  },
                  {
                    key: 'cost_usd',
                    label: '% of Total',
                    render: (v) =>
                      totalCost > 0 ? `${((Number(v) / totalCost) * 100).toFixed(1)}%` : '—',
                  },
                ]}
                rows={data.by_model}
              />
            )}
          </Card>

          <Card title="Top Sessions by Cost">
            {data.top_sessions.length === 0 ? (
              <EmptyState heading="No session data" />
            ) : (
              <DataTable<TopSessionRow>
                columns={[
                  {
                    key: 'session_id',
                    label: 'Session',
                    sortable: true,
                    render: (v) => (
                      <code className={styles.sessionId}>{String(v).slice(0, 16)}…</code>
                    ),
                  },
                  {
                    key: 'cost_usd',
                    label: 'Cost',
                    sortable: true,
                    render: (v) => `$${Number(v).toFixed(4)}`,
                  },
                  {
                    key: 'first_seen',
                    label: 'Started',
                    sortable: true,
                    render: (v) => new Date(String(v)).toLocaleString(),
                  },
                ]}
                rows={data.top_sessions}
              />
            )}
          </Card>
        </>
      )}
    </div>
  )
}
