import { useSearchParams } from 'react-router-dom'
import { useTools, useBashCommands } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, failRateBadge, ChartTooltip } from '../components'
import type { ToolItem, BashCommandItem } from '../api'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import styles from './Tools.module.css'

function truncateCommand(cmd: string, max = 80): string {
  return cmd.length > max ? cmd.slice(0, max - 1) + '…' : cmd
}

export default function Tools() {
  const [searchParams, setSearchParams] = useSearchParams()
  const userId = searchParams.get('user_id') ?? undefined
  const { data, error, isLoading } = useTools(userId)
  const { data: bashData, isLoading: bashLoading } = useBashCommands()

  const topTools = data?.items.slice().sort((a, b) => b.calls - a.calls).slice(0, 10) ?? []
  const hasBashCommands = (bashData?.items.length ?? 0) > 0

  function clearUserFilter() {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete('user_id')
      return next
    })
  }

  return (
    <div>
      <div className={styles.pageHeader}>
        <h1 className={styles.title}>Tools</h1>
        {userId && (
          <button className={styles.userChip} onClick={clearUserFilter}>
            Filtered by: <strong>{userId}</strong> ×
          </button>
        )}
      </div>

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
                <Tooltip content={<ChartTooltip formatter={(v) => [String(v), 'Calls']} />} />
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

          {bashLoading ? (
            <Card title="Bash Command Breakdown">
              <LoadingSkeleton rows={4} height={36} />
            </Card>
          ) : hasBashCommands ? (
            <Card title="Bash Command Breakdown">
              <p className={styles.sectionNote}>
                Each distinct command passed to the Bash tool, sorted by call count.
                Commands are extracted from the <code>command</code> span attribute.
              </p>
              <DataTable<BashCommandItem>
                columns={[
                  {
                    key: 'command',
                    label: 'Command',
                    sortable: true,
                    render: (v) => (
                      <span className={styles.commandCell} title={String(v)}>
                        {truncateCommand(String(v))}
                      </span>
                    ),
                  },
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
                rows={bashData!.items}
              />
            </Card>
          ) : null}
        </>
      )}
    </div>
  )
}
