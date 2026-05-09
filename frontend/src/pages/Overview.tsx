import { useOverview } from '../api'
import { Card, ErrorState, LoadingState } from '../components'

export default function Overview() {
  const { data, error, isLoading } = useOverview()

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Overview</h1>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: '1rem', marginBottom: '1.5rem' }}>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Sessions</div>
          <div style={{ fontSize: '1.8rem', fontWeight: 700 }}>{data.sessions_count}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Total Cost</div>
          <div style={{ fontSize: '1.8rem', fontWeight: 700 }}>${data.total_cost_usd.toFixed(4)}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Input Tokens</div>
          <div style={{ fontSize: '1.8rem', fontWeight: 700 }}>{(data.total_input_tokens / 1000).toFixed(1)}k</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Output Tokens</div>
          <div style={{ fontSize: '1.8rem', fontWeight: 700 }}>{(data.total_output_tokens / 1000).toFixed(1)}k</div>
        </Card>
      </div>
      <Card title="Top Models (last 30 days)">
        {data.top_models.length === 0 ? (
          <p style={{ color: '#9ca3af' }}>No data yet.</p>
        ) : (
          <ul style={{ margin: 0, padding: 0, listStyle: 'none' }}>
            {data.top_models.map((m) => (
              <li key={m.model} style={{ display: 'flex', justifyContent: 'space-between', padding: '0.3rem 0', borderBottom: '1px solid #f3f4f6' }}>
                <span>{m.model}</span>
                <span style={{ color: '#6b7280' }}>{m.span_count} spans</span>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Card title="Top Tools (last 30 days)">
        {data.top_tools.length === 0 ? (
          <p style={{ color: '#9ca3af' }}>No data yet.</p>
        ) : (
          <ul style={{ margin: 0, padding: 0, listStyle: 'none' }}>
            {data.top_tools.map((t) => (
              <li key={t.tool_name} style={{ display: 'flex', justifyContent: 'space-between', padding: '0.3rem 0', borderBottom: '1px solid #f3f4f6' }}>
                <span>{t.tool_name}</span>
                <span style={{ color: '#6b7280' }}>{t.call_count} calls</span>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}
