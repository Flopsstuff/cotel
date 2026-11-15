import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Copy, Check, Plus, Search, ChevronLeft, ChevronRight } from 'lucide-react'
import { useActorsPage, createActor } from '../api'
import type { Actor } from '../api'
import { DataTable, EmptyState, ErrorState, LoadingSkeleton, SegmentedControl } from '../components'
import type { Column, SortState } from '../components'
import { RANGE_OPTIONS, RANGE_SUFFIX, useRangeCookie } from '../lib/range'
import styles from './Actors.module.css'

const ANON_ID = '__anonymous__'
const PAGE_SIZE = 50
const RANGE_COOKIE = 'cotel_actors_range'

type SortKey = 'name' | 'cost' | 'sessions' | 'created_at' | 'last_seen'

interface ActorVM {
  id: string
  name: string
  cost: number
  sessions: number
  created_at: string
  last_seen: string | null
  isAnon: boolean
}

function AddActorModal({ onClose, onCreated }: { onClose: () => void; onCreated: (u: Actor) => void }) {
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
      const u = await createActor(trimmed)
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
        <h2 className={styles.modalTitle}>Add actor</h2>
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

function NewTokenBanner({ actor, onDismiss }: { actor: Actor; onDismiss: () => void }) {
  const [copied, setCopied] = useState(false)
  const copy = useCallback(async () => {
    await navigator.clipboard.writeText(actor.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [actor.token])

  return (
    <div className={styles.newTokenBanner}>
      <div className={styles.newTokenHeader}>
        <span className={styles.newTokenTitle}>Actor <strong>{actor.name}</strong> created — copy your token</span>
        <button className={styles.dismissBtn} onClick={onDismiss}>×</button>
      </div>
      <div className={styles.tokenCell}>
        <code className={styles.tokenText}>{actor.token}</code>
        <button className={styles.iconBtn} onClick={copy} title="Copy token">
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      </div>
      <p className={styles.newTokenHint}>
        Set <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer {actor.token}</code> in your Claude Code settings.
      </p>
    </div>
  )
}

export default function Actors() {
  const navigate = useNavigate()

  const [range, setRange] = useRangeCookie(RANGE_COOKIE)
  const [sort, setSort] = useState<SortKey>('cost')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [q, setQ] = useState('')

  const [showAdd, setShowAdd] = useState(false)
  const [newActor, setNewActor] = useState<Actor | null>(null)

  // Debounce the search box into the server-side q parameter.
  useEffect(() => {
    const t = setTimeout(() => {
      setQ(search.trim())
      setPage(1)
    }, 300)
    return () => clearTimeout(t)
  }, [search])

  const { data, error, isLoading, mutate } = useActorsPage({
    range,
    q: q || undefined,
    sort,
    order,
    page,
    limit: PAGE_SIZE,
  })

  const total = data?.total ?? 0
  const rows: ActorVM[] = (data?.actors ?? []).map((u) => ({
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
    setRange(v)
    setPage(1)
  }

  const handleSortChange = (next: NonNullable<SortState<ActorVM>>) => {
    setSort(next.key as SortKey)
    setOrder(next.dir)
    setPage(1)
  }

  const handleCreated = (u: Actor) => {
    setShowAdd(false)
    setNewActor(u)
    mutate()
  }

  const columns: Column<ActorVM>[] = [
    {
      key: 'name',
      label: 'Name',
      sortable: true,
      render: (_v, row) => (
        <span className={row.isAnon ? styles.actorNameAnon : styles.actorName}>{row.name}</span>
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
      <div className={styles.toolbar}>
        <h1 className={styles.title}>Actors</h1>

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

        <SegmentedControl
          options={RANGE_OPTIONS}
          value={range}
          onChange={changeRange}
          ariaLabel="Range for cost and sessions"
        />

        <button className={`${styles.primaryBtn} ${styles.addBtn}`} onClick={() => setShowAdd(true)}>
          <Plus size={14} /> Add actor
        </button>
      </div>

      {newActor && <NewTokenBanner actor={newActor} onDismiss={() => setNewActor(null)} />}

      {isLoading ? (
        <LoadingSkeleton rows={6} height={40} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : total === 0 ? (
        q ? (
          <div className={styles.noResults}>No actors match "{q}"</div>
        ) : (
          <EmptyState
            heading="No actors yet"
            subtext="Add an actor to get a token for authenticated OTLP ingest. Anonymous sends are allowed by default."
          />
        )
      ) : (
        <>
          <DataTable<ActorVM>
            columns={columns}
            rows={rows}
            sort={{ key: sort as keyof ActorVM, dir: order }}
            onSortChange={handleSortChange}
            onRowClick={(row) => navigate(`/actors/${encodeURIComponent(row.id)}`)}
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

      {showAdd && <AddActorModal onClose={() => setShowAdd(false)} onCreated={handleCreated} />}
    </div>
  )
}
