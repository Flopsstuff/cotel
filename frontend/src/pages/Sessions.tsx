import { useState, useMemo } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useSessions } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, sessionStatusBadge } from '../components'
import type { SessionItem } from '../api'
import styles from './Sessions.module.css'

export default function Sessions() {
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<'all' | 'ok' | 'error'>('all')
  const navigate = useNavigate()

  const { data, error, isLoading } = useSessions(page, 50, 'start_time', 'desc')

  const filtered = useMemo(() => {
    if (!data) return []
    return data.items.filter(
      (s) =>
        (search === '' || s.session_id.toLowerCase().includes(search.toLowerCase())) &&
        (statusFilter === 'all' || s.status === statusFilter),
    )
  }, [data, search, statusFilter])

  const totalPages = data ? Math.ceil(data.total / data.limit) || 1 : 1

  return (
    <div>
      <h1 className={styles.title}>Sessions</h1>

      <div className={styles.filterBar}>
        <input
          className={styles.searchInput}
          placeholder="Search by session ID…"
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(1) }}
        />
        <div className={styles.chips}>
          {(['all', 'ok', 'error'] as const).map((s) => (
            <button
              key={s}
              className={[styles.chip, statusFilter === s ? styles.chipActive : ''].filter(Boolean).join(' ')}
              onClick={() => { setStatusFilter(s); setPage(1) }}
            >
              {s}
            </button>
          ))}
        </div>
      </div>

      {isLoading ? (
        <LoadingSkeleton rows={10} height={44} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : filtered.length === 0 ? (
        <EmptyState
          heading="No sessions found"
          subtext={search || statusFilter !== 'all' ? 'Try adjusting your filters.' : 'Start using cotel to record sessions.'}
        />
      ) : (
        <Card>
          <DataTable<SessionItem>
            columns={[
              {
                key: 'session_id',
                label: 'Session',
                sortable: true,
                render: (v) => (
                  <Link to={`/sessions/${v}`} className={styles.sessionLink}>
                    {String(v).slice(0, 16)}…
                  </Link>
                ),
              },
              {
                key: 'first_seen',
                label: 'Started',
                sortable: true,
                render: (v) => new Date(String(v)).toLocaleString(),
              },
              {
                key: 'last_seen',
                label: 'Last Seen',
                sortable: true,
                render: (v) => new Date(String(v)).toLocaleString(),
              },
              { key: 'model', label: 'Model', sortable: true },
              {
                key: 'cost_usd',
                label: 'Cost',
                sortable: true,
                render: (v) => `$${Number(v).toFixed(4)}`,
              },
              {
                key: 'input_tokens',
                label: 'In Tokens',
                sortable: true,
                render: (v) => Number(v).toLocaleString(),
              },
              {
                key: 'output_tokens',
                label: 'Out Tokens',
                sortable: true,
                render: (v) => Number(v).toLocaleString(),
              },
              {
                key: 'tool_calls',
                label: 'Tools',
                sortable: true,
              },
              {
                key: 'status',
                label: 'Status',
                render: (v) => sessionStatusBadge(String(v)),
              },
            ]}
            rows={filtered}
            onRowClick={(row) => navigate(`/sessions/${row.session_id}`)}
          />
          <div className={styles.pagination}>
            <span className={styles.pageInfo}>
              {data?.total ?? 0} sessions — page {page} / {totalPages}
            </span>
            <div className={styles.pageButtons}>
              <button
                className={styles.pageBtn}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page <= 1}
              >
                ← Prev
              </button>
              <button
                className={styles.pageBtn}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
              >
                Next →
              </button>
            </div>
          </div>
        </Card>
      )}
    </div>
  )
}
