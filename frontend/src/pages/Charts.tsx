import { useOverview } from '../api'
import { Card, ErrorState, LoadingState } from '../components'
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, BarChart, Bar } from 'recharts'

export default function Charts() {
  const { data, error, isLoading } = useOverview()

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Charts</h1>
      <Card title="Daily Cost Trend">
        {data.daily_costs.length === 0 ? (
          <p style={{ color: '#9ca3af' }}>No data yet.</p>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <LineChart data={data.daily_costs}>
              <XAxis dataKey="date" tick={{ fontSize: 11 }} />
              <YAxis tick={{ fontSize: 11 }} />
              <Tooltip formatter={(v: number) => `$${v.toFixed(4)}`} />
              <Line type="monotone" dataKey="cost_usd" stroke="#3b82f6" dot={false} />
            </LineChart>
          </ResponsiveContainer>
        )}
      </Card>
      <Card title="Top Models by Span Count">
        {data.top_models.length === 0 ? (
          <p style={{ color: '#9ca3af' }}>No data yet.</p>
        ) : (
          <ResponsiveContainer width="100%" height={200}>
            <BarChart data={data.top_models} layout="vertical">
              <XAxis type="number" tick={{ fontSize: 11 }} />
              <YAxis type="category" dataKey="model" tick={{ fontSize: 11 }} width={160} />
              <Tooltip />
              <Bar dataKey="span_count" fill="#10b981" />
            </BarChart>
          </ResponsiveContainer>
        )}
      </Card>
    </div>
  )
}
