import { useMemo } from 'react'
import { useModels } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton } from '../components'
import type { ModelItem } from '../api'
import styles from './Models.module.css'

export default function Models() {
  const { data, error, isLoading } = useModels()

  const rows = useMemo(() => {
    if (!data) return []
    return [...data.items].sort((a, b) => b.span_count - a.span_count)
  }, [data])

  const maxCost = useMemo(() => Math.max(...rows.map((r) => r.total_cost_usd), 1), [rows])

  return (
    <div>
      <h1 className={styles.title}>Models</h1>

      {isLoading ? (
        <LoadingSkeleton rows={6} height={52} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : rows.length === 0 ? (
        <EmptyState heading="No model data" subtext="Model usage will appear here once sessions are recorded." />
      ) : (
        <Card>
          <DataTable<ModelItem & { _costBar: number }>
            columns={[
              { key: 'model', label: 'Model', sortable: true },
              { key: 'span_count', label: 'Spans', sortable: true },
              {
                key: 'total_cost_usd',
                label: 'Total Cost',
                sortable: true,
                render: (v) => `$${Number(v).toFixed(4)}`,
              },
              {
                key: '_costBar',
                label: 'Cost (relative)',
                render: (_v, row) => {
                  const pct = maxCost > 0 ? (row.total_cost_usd / maxCost) * 100 : 0
                  return (
                    <div className={styles.barContainer}>
                      <div className={styles.bar} style={{ width: `${pct}%` }} />
                    </div>
                  )
                },
              },
              {
                key: 'total_input_tokens',
                label: 'In Tokens',
                sortable: true,
                render: (v) => Number(v).toLocaleString(),
              },
              {
                key: 'total_output_tokens',
                label: 'Out Tokens',
                sortable: true,
                render: (v) => Number(v).toLocaleString(),
              },
            ]}
            rows={rows.map((r) => ({ ...r, _costBar: r.total_cost_usd }))}
          />
        </Card>
      )}
    </div>
  )
}
