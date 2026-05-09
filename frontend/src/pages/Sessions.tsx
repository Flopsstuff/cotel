import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useSessions } from '../api'
import { Card, DataTable, ErrorState, LoadingState } from '../components'
import type { SessionItem } from '../api'

export default function Sessions() {
  const [page, setPage] = useState(1)
  const { data, error, isLoading } = useSessions(page, 50)

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  const totalPages = Math.ceil(data.total / data.limit) || 1

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Sessions</h1>
      <Card>
        <DataTable<SessionItem>
          columns={[
            {
              key: 'session_id',
              label: 'Session',
              render: (v) => (
                <Link to={`/sessions/${v}`} style={{ color: '#2563eb', fontFamily: 'monospace', fontSize: '0.8rem' }}>
                  {String(v).slice(0, 12)}…
                </Link>
              ),
            },
            { key: 'model', label: 'Model' },
            { key: 'first_seen', label: 'Started', render: (v) => new Date(String(v)).toLocaleString() },
            { key: 'cost_usd', label: 'Cost', render: (v) => `$${Number(v).toFixed(4)}` },
            { key: 'input_tokens', label: 'In Tokens' },
            { key: 'output_tokens', label: 'Out Tokens' },
            { key: 'tool_calls', label: 'Tool Calls' },
            { key: 'status', label: 'Status' },
          ]}
          rows={data.items}
        />
        <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: '1rem', alignItems: 'center' }}>
          <span style={{ color: '#6b7280', fontSize: '0.85rem' }}>
            {data.total} sessions — page {page}/{totalPages}
          </span>
          <div style={{ display: 'flex', gap: '0.5rem' }}>
            <button onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page <= 1}>
              ←
            </button>
            <button onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={page >= totalPages}>
              →
            </button>
          </div>
        </div>
      </Card>
    </div>
  )
}
