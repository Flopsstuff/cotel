import { useParams } from 'react-router-dom'
import { useSession } from '../api'
import { Card, DataTable, ErrorState, LoadingState } from '../components'
import type { SpanDetail } from '../api'

export default function SessionDetail() {
  const { id } = useParams<{ id: string }>()
  const { data, error, isLoading } = useSession(id ?? '')

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0, fontFamily: 'monospace', fontSize: '1rem' }}>
        Session {data.session_id}
      </h1>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: '1rem', marginBottom: '1.5rem' }}>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Model</div>
          <div>{data.model || '—'}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Cost</div>
          <div>${data.cost_usd.toFixed(4)}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Input Tokens</div>
          <div>{data.input_tokens.toLocaleString()}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Output Tokens</div>
          <div>{data.output_tokens.toLocaleString()}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Cache Read</div>
          <div>{data.cache_read_tokens.toLocaleString()}</div>
        </Card>
        <Card>
          <div style={{ fontSize: '0.8rem', color: '#6b7280' }}>Cache Write</div>
          <div>{data.cache_write_tokens.toLocaleString()}</div>
        </Card>
      </div>
      <Card title={`Spans (${data.spans.length})`}>
        <DataTable<SpanDetail>
          columns={[
            { key: 'start_time', label: 'Time', render: (v) => new Date(String(v)).toLocaleTimeString() },
            { key: 'name', label: 'Name' },
            { key: 'tool_name', label: 'Tool' },
            { key: 'duration_ms', label: 'Duration', render: (v) => `${Number(v).toFixed(0)}ms` },
            { key: 'input_tokens', label: 'In' },
            { key: 'output_tokens', label: 'Out' },
            { key: 'status', label: 'Status' },
          ]}
          rows={data.spans}
        />
      </Card>
    </div>
  )
}
