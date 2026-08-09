import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Copy, Check, Plus, Search, ChevronLeft, ChevronRight } from 'lucide-react'
import { useUsersPage, createUser } from '../api'
import type { User } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, SegmentedControl } from '../components'
import type { Column, SortState } from '../components'
import { getCookie, setCookie } from '../lib/cookie'
import styles from './Users.module.css'

const ANON_ID = '__anonymous__'
const PAGE_SIZE = 50
const RANGE_COOKIE = 'cotel_users_range'
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365 // one year

type RangeKey = 'all' | 'year' | 'month' | 'week' | 'day'
type SortKey = 'name' | 'cost' | 'sessions' | 'created_at' | 'last_seen'

const RANGE_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'year', label: 'Year' },
  { value: 'month', label: 'Month' },
  { value: 'week', label: 'Week' },
  { value: 'day', label: 'Day' },
]

// Short suffix shown on the range-governed columns so it is obvious the switcher
// only scopes Cost and Sessions.
const RANGE_SUFFIX: Record<RangeKey, string> = {
  all: '',
  year: '1y',
  month: '30d',
  week: '7d',
  day: '24h',
}

function isRangeKey(v: string | null): v is RangeKey {
  return v === 'all' || v === 'year' || v === 'month' || v === 'week' || v === 'day'
}

interface UserVM {
  id: string
  name: string
  cost: number
  sessions: number
  created_at: string
  last_seen: string | null
  isAnon: boolean
}

function AddUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: (u: User) => void }) {
  const [name, setName] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    setLoading(true)
    setError(null)
    try {
      const u = await createUser(trimmed)
      onCreated(u)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Create failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={styles.modalOverlay} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className={styles.modal}>
        <h2 className={styles.modalTitle}>Add user</h2>
        <form onSubmit={handleSubmit}>
          <label className={styles.fieldLabel}>Name</label>
          <input
            className={styles.fieldInput}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. alice"
            autoFocus
          />
          {error && <div className={styles.fieldError}>{error}</div>}
          <div className={styles.modalActions}>
            <button type="submit" className={styles.primaryBtn} disabled={loading || !name.trim()}>
              {loading ? 'Creating…' : 'Create'}
            </button>
            <button type="button" className={styles.secondaryBtn} onClick={onClose}>Cancel</button>
          </div>
        </form>
      </div>
    </div>
  )
}

function NewTokenBanner({ user, onDismiss }: { user: User; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false)
  const copy = useCallback(async () => {
    await navigator.clipboard.writeText(user.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [user.token])

  return (
    <div className={styles.newTokenBanner}>
      <div className={styles.newTokenHeader}>
        <span className={styles.newTokenTitle}>User <strong>{user.name}</strong> created — copy your token</span>
        <button className={styles.dismissBtn} onClick={onDismiss}>×</button>
      </div>
      <div className={styles.tokenCell}>
        <code className={styles.tokenText}>{user.token}</code>
        <button className={styles.iconBtn} onClick={copy} title="Copy token">
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      </div>
      <p className={styles.newTokenHint}>
        Set <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer {user.token}</code> in your Claude Code settings.
      </p>
    </div>
  )
}

export default function Users() {
  const navigate = useNavigate()

  const [range, setRange] = useState<RangeKey>(() => {
    const c = getCookie(RANGE_COOKIE)
    return isRangeKey(c) ? c : 'month'
  })
  const [sort, setSort] = useState<SortKey>('cost')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [q, setQ] = useState('')

  const [showAdd, setShowAdd] = useState(false)
  const [newUser, setNewUser] = useState<User | null>(null)

  // Debounce the search box into the server-side q parameter.
  useEffect(() => {
    const t = setTimeout(() => {
      setQ(search.trim())
      setPage(1)
    }, 300)
    return () => clearTimeout(t)
  }, [search])

  const { data, error, isLoading, mutate } = useUsersPage({
    range,
    q: q || undefined,
    sort,
    order,
    page,
    limit: PAGE_SIZE,
  })

  const total = data?.total ?? 0
  const rows: UserVM[] = (data?.users ?? []).map((u) => ({
    id: u.id,
    name: u.name,
    cost: u.cost,
    sessions: u.sessions,
    created_at: u.created_at,
    last_seen: u.last_seen,
    isAnon: u.id === ANON_ID,
  }))

  const suffix = RANGE_SUFFIX[range]
  const scoped = (label: string) => (suffix ? `${label} (${suffix})` : label)

  const changeRange = (v: string) => {
    if (!isRangeKey(v)) return
    setRange(v)
    setCookie(RANGE_COOKIE, v, COOKIE_MAX_AGE)
    setPage(1)
  }

  const handleSortChange = (next: NonNullable<SortState<UserVM>>) => {
    setSort(next.key as SortKey)
    setOrder(next.dir)
    setPage(1)
  }

  const handleCreated = (u: User) => {
    setShowAdd(false)
    setNewUser(u)
    mutate()
  }

  const columns: Column<UserVM>[] = [
    {
      key: 'name',
      label: 'Name',
      sortable: true,
      render: (_v, row) => (
        <span className={row.isAnon ? styles.userNameAnon : styles.userName}>{row.name}</span>
      ),
    },
    {
      key: 'cost',
      label: scoped('Cost'),
      sortable: true,
      render: (v) => <span className={styles.dimText}>${Number(v).toFixed(2)}</span>,
    },
    {
      key: 'sessions',
      label: scoped('Sessions'),
      sortable: true,
      render: (v) => <span className={styles.dimText}>{Number(v).toLocaleString()}</span>,
    },
    {
      key: 'created_at',
      label: 'Created',
      sortable: true,
      render: (v, row) => (
        <span className={styles.dimText}>
          {row.isAnon || !v ? '—' : new Date(String(v)).toLocaleDateString()}
        </span>
      ),
    },
    {
      key: 'last_seen',
      label: 'Last seen',
      sortable: true,
      render: (v) => (
        <span className={styles.dimText}>{v ? new Date(String(v)).toLocaleString() : '—'}</span>
      ),
    },
  ]

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div>
      <div className={styles.pageHeader}>
        <h1 className={styles.title}>Users</h1>
        <button className={styles.primaryBtn} onClick={() => setShowAdd(true)}>
          <Plus size={14} /> Add user
        </button>
      </div>

      {newUser && <NewTokenBanner user={newUser} onDismiss={() => setNewUser(null)} />}

      {isLoading ? (
        <LoadingSkeleton rows={6} height={40} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : (
        <Card>
          <div className={styles.controlsRow}>
            <div className={styles.rangeGroup}>
              <span className={styles.rangeLabel}>Cost &amp; sessions:</span>
              <SegmentedControl options={RANGE_OPTIONS} value={range} onChange={changeRange} />
            </div>
            <div className={styles.searchWrap}>
              <Search size={14} className={styles.searchIcon} />
              <input
                className={styles.searchInput}
                placeholder="Search by name…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
              {search && (
                <button className={styles.searchClear} onClick={() => setSearch('')}>×</button>
              )}
            </div>
          </div>

          {total === 0 ? (
            q ? (
              <div className={styles.noResults}>No users match "{q}"</div>
            ) : (
              <EmptyState
                heading="No users yet"
                subtext="Add a user to get a token for authenticated OTLP ingest. Anonymous sends are allowed by default."
              />
            )
          ) : (
            <>
              <DataTable<UserVM>
                columns={columns}
                rows={rows}
                sort={{ key: sort as keyof UserVM, dir: order }}
                onSortChange={handleSortChange}
                onRowClick={(row) => navigate(`/users/${encodeURIComponent(row.id)}`)}
              />

              {total > PAGE_SIZE && (
                <div className={styles.pagination}>
                  <button
                    className={styles.pageBtn}
                    disabled={page <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    <ChevronLeft size={14} />
                  </button>
                  <span className={styles.pageInfo}>{page} / {totalPages}</span>
                  <button
                    className={styles.pageBtn}
                    disabled={page >= totalPages}
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  >
                    <ChevronRight size={14} />
                  </button>
                </div>
              )}
            </>
          )}
        </Card>
      )}

      {showAdd && <AddUserModal onClose={() => setShowAdd(false)} onCreated={handleCreated} />}
    </div>
  )
}
