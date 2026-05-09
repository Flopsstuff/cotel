import { useTools } from '../api'
import { Card, DataTable, ErrorState, LoadingState } from '../components'
import type { ToolItem } from '../api'

export default function Tools() {
  const { data, error, isLoading } = useTools()

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Tools</h1>
      <Card>
        <DataTable<ToolItem>
          columns={[
            { key: 'name', label: 'Tool' },
            { key: 'calls', label: 'Calls' },
            { key: 'avg_duration_ms', label: 'Avg Duration', render: (v) => `${Number(v).toFixed(0)}ms` },
            { key: 'fail_count', label: 'Errors' },
            { key: 'fail_rate', label: 'Error Rate', render: (v) => `${Number(v).toFixed(1)}%` },
          ]}
          rows={data.items}
        />
        {data.items.length === 0 && <p style={{ color: '#9ca3af', textAlign: 'center' }}>No tool data yet.</p>}
      </Card>
    </div>
  )
}
