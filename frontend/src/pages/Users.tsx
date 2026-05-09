import { useState, useMemo } from 'react'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import { useUsers } from '../api'
import type { UserRow } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, ChartTooltip } from '../components'
import styles from './Users.module.css'

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function displayName(row: UserRow): string {
  return row.is_default || row.user_id === '' ? '(default)' : row.user_id
}

function UserIdCell({ row }: { row: UserRow }) {
  if (row.is_default || row.user_id === '') {
    return <span className={styles.defaultBadge}>(default)</span>
  }
  return <span className={styles.userIdCell}>{row.user_id}</span>
}

const CHART_COLORS = [
  'var(--color-chart-1)',
  'var(--color-chart-2)',
  'var(--color-chart-3)',
  'var(--color-chart-4)',
  'var(--color-chart-5)',
]

export default function Users() {
  const { data, error, isLoading } = useUsers()
  const [search, setSearch] = useState('')

  const allItems = useMemo(() => data?.items ?? [], [data])

  const filtered = useMemo(() => {
    const q = search.toLowerCase()
    return allItems.filter((u) =>
      q === '' || displayName(u).toLowerCase().includes(q),
    )
  }, [allItems, search])

  const chartData = useMemo(() =>
    allItems.slice(0, 10).map((u) => ({
      name: displayName(u),
      cost_usd: u.total_cost_usd,
      is_default: u.is_default,
    })),
    [allItems],
  )

  return (
    <div>
      <h1 className={styles.title}>Users</h1>

      {isLoading ? (
        <>
          <div className={styles.chartBlock}><ChartSkeleton /></div>
          <LoadingSkeleton rows={6} height={40} />
        </>
      ) : error ? (
        <ErrorState message={error.message} />
      ) : !data || allItems.length === 0 ? (
        <EmptyState
          heading="No users yet"
          subtext="Set OTEL_RESOURCE_ATTRIBUTES=user.id=yourname before running Claude Code to tag telemetry by user."
        />
      ) : (
        <>
          <Card title="Cost by User (top 10)">
            <div className={styles.chartBlock}>
              <ResponsiveContainer width="100%" height={Math.max(160, chartData.length * 32)}>
                <BarChart data={chartData} layout="vertical" margin={{ top: 4, right: 16, bottom: 4, left: 0 }}>
                  <XAxis
                    type="number"
                    tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                    tickFormatter={(v) => `$${Number(v).toFixed(2)}`}
                  />
                  <YAxis
                    type="category"
                    dataKey="name"
                    tick={{ fontSize: 11, fill: 'var(--color-text-3)' }}
                    width={120}
                  />
                  <Tooltip content={<ChartTooltip formatter={(v) => [`$${Number(v).toFixed(4)}`, 'Cost']} />} />
                  <Bar dataKey="cost_usd" radius={[0, 2, 2, 0]}>
                    {chartData.map((entry, i) => (
                      <Cell
                        key={entry.name}
                        fill={entry.is_default ? 'var(--color-neutral)' : CHART_COLORS[i % CHART_COLORS.length]}
                        opacity={entry.is_default ? 0.55 : 1}
                      />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          </Card>

          <div className={styles.filterBar}>
            <input
              className={styles.searchInput}
              placeholder="Search user…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>

          <Card title={`All Users${search ? ` — ${filtered.length} of ${allItems.length}` : ` (${allItems.length})`}`}>
            <DataTable<UserRow>
              columns={[
                {
                  key: 'user_id',
                  label: 'User',
                  render: (_, row) => <UserIdCell row={row as UserRow} />,
                },
                {
                  key: 'total_cost_usd',
                  label: 'Cost',
                  sortable: true,
                  render: (v) => `$${Number(v).toFixed(4)}`,
                },
                {
                  key: 'sessions',
                  label: 'Sessions',
                  sortable: true,
                },
                {
                  key: 'total_input_tokens',
                  label: 'Input Tokens',
                  sortable: true,
                  render: (v) => fmtTokens(Number(v)),
                },
                {
                  key: 'total_output_tokens',
                  label: 'Output Tokens',
                  sortable: true,
                  render: (v) => fmtTokens(Number(v)),
                },
                {
                  key: 'span_count',
                  label: 'Spans',
                  sortable: true,
                },
                {
                  key: 'last_seen',
                  label: 'Last Seen',
                  render: (v) => new Date(String(v)).toLocaleString(),
                },
              ]}
              rows={filtered}
            />
          </Card>
        </>
      )}
    </div>
  )
}
