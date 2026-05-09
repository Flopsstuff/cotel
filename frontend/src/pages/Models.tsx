import { useModels } from '../api'
import { Card, DataTable, ErrorState, LoadingState } from '../components'
import type { ModelItem } from '../api'

export default function Models() {
  const { data, error, isLoading } = useModels()

  if (isLoading) return <LoadingState />
  if (error) return <ErrorState message={error.message} />
  if (!data) return null

  return (
    <div>
      <h1 style={{ marginTop: 0 }}>Models</h1>
      <Card>
        <DataTable<ModelItem>
          columns={[
            { key: 'model', label: 'Model' },
            { key: 'span_count', label: 'Spans' },
            { key: 'total_cost_usd', label: 'Total Cost', render: (v) => `$${Number(v).toFixed(4)}` },
            { key: 'total_input_tokens', label: 'Input Tokens' },
            { key: 'total_output_tokens', label: 'Output Tokens' },
          ]}
          rows={data.items}
        />
        {data.items.length === 0 && <p style={{ color: '#9ca3af', textAlign: 'center' }}>No model data yet.</p>}
      </Card>
    </div>
  )
}
