import { useCosts } from '../api'
import { Card, DataTable, ErrorState, LoadingState } from '../components'
import type { CostsResponse } from '../api'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'

type DayRow = CostsResponse['daily'][number]
type ModelRow = CostsResponse['by_model'][number]
type TopSessionRow = CostsResponse['top_sessions'][number]

export default function Costs() {
  const { data, error, isLoading } = useCosts()

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Costs</h1>
      <Card title="Daily Cost (last 30 days)">
        {data.daily.length === 0 ? (
          <p style={{ color: '#9ca3af' }}>No cost data yet.</p>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <BarChart data={data.daily}>
              <XAxis dataKey="date" tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} />
              <Tooltip formatter={(v: number) => `$${v.toFixed(4)}`} />
              <Bar dataKey="cost_usd" fill="#3b82f6" />
            </BarChart>
          </ResponsiveContainer>
        )}
      </Card>
      <Card title="By Model">
        <DataTable<ModelRow>
          columns={[
            { key: 'model', label: 'Model' },
            { key: 'cost_usd', label: 'Cost', render: (v) => `$${Number(v).toFixed(4)}` },
          ]}
          rows={data.by_model}
        />
      </Card>
      <Card title="Top Sessions by Cost">
        <DataTable<TopSessionRow>
          columns={[
            { key: 'session_id', label: 'Session', render: (v) => <code style={{ fontSize: '0.8rem' }}>{String(v).slice(0, 12)}…</code> },
            { key: 'cost_usd', label: 'Cost', render: (v) => `$${Number(v).toFixed(4)}` },
            { key: 'first_seen', label: 'Started', render: (v) => new Date(String(v)).toLocaleString() },
          ]}
          rows={data.top_sessions}
        />
      </Card>
    </div>
  )
}
