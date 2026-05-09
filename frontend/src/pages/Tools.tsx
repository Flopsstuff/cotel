import { useTools } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, failRateBadge } from '../components'
import type { ToolItem } from '../api'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import styles from './Tools.module.css'

export default function Tools() {
  const { data, error, isLoading } = useTools()

  const topTools = data?.items.slice().sort((a, b) => b.calls - a.calls).slice(0, 10) ?? []

  return (
    <div>
      <h1 className={styles.title}>Tools</h1>

      {isLoading ? (
        <>
          <div className={styles.chartBlock}><ChartSkeleton /></div>
          <LoadingSkeleton rows={8} height={40} />
        </>
      ) : error ? (
        <ErrorState message={error.message} />
      ) : !data || data.items.length === 0 ? (
        <EmptyState heading="No tool data" subtext="Tool usage will appear here once sessions are recorded." />
      ) : (
        <>
          <Card title="Top 10 Tools by Call Count">
            <ResponsiveContainer width="100%" height={260}>
              <BarChart data={topTools} layout="vertical" margin={{ top: 4, right: 8, bottom: 4, left: 0 }}>
                <XAxis type="number" tick={{ fontSize: 11, fill: 'var(--color-text-3)' }} />
                <YAxis
                  type="category"
                  dataKey="name"
                  tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                  width={160}
                />
                <Tooltip formatter={(v: number) => [v, 'Calls']} />
                <Bar dataKey="calls" fill="var(--color-chart-1)" radius={[0, 2, 2, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </Card>

          <Card title="All Tools">
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
                { key: 'fail_count', label: 'Errors', sortable: true },
                {
                  key: 'fail_rate',
                  label: 'Error Rate',
                  sortable: true,
                  render: (v) => failRateBadge(Number(v)),
                },
              ]}
              rows={data.items}
            />
          </Card>
        </>
      )}
    </div>
  )
}
