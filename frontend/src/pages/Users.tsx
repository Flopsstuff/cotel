import { useState, useCallback, useMemo, useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import { Copy, Check, Plus, RotateCcw, Trash2, Search, ChevronLeft, ChevronRight } from 'lucide-react'
import { useUsers, createUser, rotateUserToken, deleteUser } from '../api'
import type { User, DeleteMode } from '../api'
import { Card, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, ChartTooltip } from '../components'
import styles from './Users.module.css'

const CHART_COLORS = [
  'var(--color-chart-1)',
  'var(--color-chart-2)',
  'var(--color-chart-3)',
  'var(--color-chart-4)',
  'var(--color-chart-5)',
]

const ANON_ID = '__anonymous__'
const PAGE_SIZE = 25

function CopyToken({ token }: { token: string }) {
  const [copied, setCopied] = useState(false)
  const copy = useCallback(async (e: React.MouseEvent) => {
    e.stopPropagation()
    await navigator.clipboard.writeText(token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [token])
  return (
    <div className={styles.tokenCell}>
      <code className={styles.tokenText}>{token}</code>
      <button className={styles.iconBtn} onClick={copy} title="Copy token">
        {copied ? <Check size={13} /> : <Copy size={13} />}
      </button>
    </div>
  )
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

interface DeleteUserModalProps {
  user: User
  onClose: () => void
  onDeleted: (mode: DeleteMode) => void
}

function DeleteUserModal({ user, onClose, onDeleted }: DeleteUserModalProps) {
  const [mode, setMode] = useState<DeleteMode | null>(null)
  const [confirmText, setConfirmText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const firstRadioRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    firstRadioRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const modeAReady = mode === 'user_only'
  const modeBReady = mode === 'user_and_history' && confirmText === user.name
  const canSubmit = modeAReady || modeBReady

  const handleSubmit = async () => {
    if (!mode || !canSubmit) return
    setLoading(true)
    setError(null)
    try {
      await deleteUser(user.id, mode)
      onDeleted(mode)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed')
      setLoading(false)
    }
  }

  const handleModeChange = (next: DeleteMode) => {
    setMode(next)
    if (next !== 'user_and_history') setConfirmText('')
    setError(null)
  }

  const confirmLabel = mode === 'user_and_history' ? 'Permanently delete' : 'Delete user'

  return (
    <div className={styles.modalOverlay} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className={styles.modal} role="dialog" aria-modal="true" aria-labelledby="delete-modal-title">
        <div className={styles.modalHeader}>
          <h2 className={styles.modalTitle} id="delete-modal-title">Delete user</h2>
          <button className={styles.dismissBtn} onClick={onClose} aria-label="Close">×</button>
        </div>
        <p className={styles.modalPreamble}>
          You are about to delete <strong>{user.name}</strong>. Choose what to delete:
        </p>

        {/* Mode A */}
        <div
          ref={firstRadioRef}
          role="radio"
          aria-checked={mode === 'user_only'}
          tabIndex={0}
          className={`${styles.radioCard} ${mode === 'user_only' ? styles.radioCardSelectedA : ''}`}
          onClick={() => handleModeChange('user_only')}
          onKeyDown={(e) => (e.key === ' ' || e.key === 'Enter') && handleModeChange('user_only')}
        >
          <div className={styles.radioRow}>
            <span className={styles.radioMark}>{mode === 'user_only' ? '●' : '○'}</span>
            <span className={styles.radioLabel}>Delete user only</span>
          </div>
          <p className={styles.radioDesc}>
            Revokes {user.name}'s login access. All session history and telemetry stays attributed to {user.name}. Reversible by re-creating a user with the same name.
          </p>
        </div>

        {/* Mode B */}
        <div
          role="radio"
          aria-checked={mode === 'user_and_history'}
          tabIndex={0}
          className={`${styles.radioCard} ${mode === 'user_and_history' ? styles.radioCardSelectedB : ''}`}
          onClick={() => handleModeChange('user_and_history')}
          onKeyDown={(e) => (e.key === ' ' || e.key === 'Enter') && handleModeChange('user_and_history')}
        >
          <div className={styles.radioRow}>
            <span className={styles.radioMark}>{mode === 'user_and_history' ? '●' : '○'}</span>
            <span className={styles.radioLabel}>Delete user + history</span>
            <span className={styles.permanentBadge}>⚠ Permanent</span>
          </div>
          <p className={styles.radioDesc}>
            Revokes access and permanently replaces {user.name} with [deleted] in all telemetry. Session data is preserved but permanently unattributable. This cannot be undone.
          </p>
          {mode === 'user_and_history' && (
            <div className={`${styles.confirmInputWrap} ${styles.confirmInputAnimate}`}>
              <label className={styles.confirmLabel}>Type "{user.name}" to confirm:</label>
              <input
                className={styles.confirmInput}
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                autoFocus
                onKeyDown={(e) => e.key === 'Enter' && modeBReady && !loading && handleSubmit()}
                spellCheck={false}
              />
            </div>
          )}
        </div>

        {error && <div className={styles.fieldError}>{error}</div>}

        <div className={styles.modalFooter}>
          <button className={styles.ghostBtn} onClick={onClose} disabled={loading}>Cancel</button>
          <button
            className={mode === 'user_and_history' ? styles.dangerBtn : styles.secondaryBtn}
            disabled={!canSubmit || loading}
            onClick={handleSubmit}
          >
            {loading ? 'Deleting…' : (mode == null ? 'Delete' : confirmLabel)}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function Users() {
  const navigate = useNavigate()
  const { data, error, isLoading, mutate } = useUsers()
  const [showAdd, setShowAdd] = useState(false)
  const [newUser, setNewUser] = useState<User | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null)
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)

  const users = data?.users ?? []
  const chartData = [...users]
    .sort((a, b) => b.cost - a.cost)
    .slice(0, 10)
    .map((u, i) => ({ name: u.name, cost: u.cost, colorIdx: i }))

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return users
    return users.filter(
      (u) => u.name.toLowerCase().includes(q) || u.token.toLowerCase().startsWith(q)
    )
  }, [users, search])

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const safePage = Math.min(page, totalPages - 1)
  const pageSlice = filtered.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE)

  const handleSearch = (q: string) => {
    setSearch(q)
    setPage(0)
  }

  const handleCreated = (u: User) => {
    setShowAdd(false)
    setNewUser(u)
    mutate()
  }

  const handleRotate = async (e: React.MouseEvent, u: User) => {
    e.stopPropagation()
    if (!confirm(`Rotate token for "${u.name}"? The old token will stop working immediately.`)) return
    setActionError(null)
    try {
      await rotateUserToken(u.id)
      mutate()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Rotate failed')
    }
  }

  const showToast = (msg: string) => {
    setToast(msg)
    setTimeout(() => setToast(null), 4000)
  }

  const handleDeleteAnon = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (!confirm('Delete all anonymous telemetry? All unattributed spans and daily summaries will be permanently removed. This cannot be undone.')) return
    setActionError(null)
    try {
      await deleteUser(ANON_ID)
      mutate()
      showToast('Anonymous telemetry deleted')
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Delete failed')
    }
  }

  const handleDeleteClick = (e: React.MouseEvent, u: User) => {
    e.stopPropagation()
    if (u.id === ANON_ID) {
      handleDeleteAnon(e)
    } else {
      setDeleteTarget(u)
    }
  }

  const handleDeleteConfirmed = (u: User, mode: DeleteMode) => {
    setDeleteTarget(null)
    mutate()
    const msg = mode === 'user_and_history'
      ? `"${u.name}" removed and history anonymized`
      : `"${u.name}" removed`
    showToast(msg)
  }

  const handleRowClick = (u: User) => {
    const uid = u.id === ANON_ID ? ANON_ID : u.name
    navigate(`/?user_id=${encodeURIComponent(uid)}`)
  }

  return (
    <div>
      <div className={styles.pageHeader}>
        <h1 className={styles.title}>Users</h1>
        <button className={styles.primaryBtn} onClick={() => setShowAdd(true)}>
          <Plus size={14} /> Add user
        </button>
      </div>

      {newUser && <NewTokenBanner user={newUser} onDismiss={() => setNewUser(null)} />}
      {actionError && <div className={styles.errorMsg}>{actionError}</div>}
      {toast && <div className={styles.toastMsg}>{toast}</div>}

      {isLoading ? (
        <>
          <div className={styles.chartBlock}><ChartSkeleton /></div>
          <LoadingSkeleton rows={4} height={40} />
        </>
      ) : error ? (
        <ErrorState message={error.message} />
      ) : users.length === 0 ? (
        <EmptyState
          heading="No users yet"
          subtext="Add a user to get a token for authenticated OTLP ingest. Anonymous sends are allowed by default."
        />
      ) : (
        <>
          {chartData.length > 0 && (
            <Card title="Cost by user (top 10)">
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
                    <Bar dataKey="cost" radius={[0, 2, 2, 0]}>
                      {chartData.map((entry) => (
                        <Cell key={entry.name} fill={CHART_COLORS[entry.colorIdx % CHART_COLORS.length]} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </Card>
          )}

          <Card title={`All users (${filtered.length}${filtered.length !== users.length ? ` of ${users.length}` : ''})`}>
            <div className={styles.helpCallout}>
              Copy a token and set{' '}
              <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer &lt;token&gt;</code>{' '}
              in your Claude Code settings (<code className={styles.code}>~/.claude/settings.json</code> or env).
              Click a row to view that user's activity.
            </div>

            <div className={styles.searchRow}>
              <div className={styles.searchWrap}>
                <Search size={14} className={styles.searchIcon} />
                <input
                  className={styles.searchInput}
                  placeholder="Search by name or token prefix…"
                  value={search}
                  onChange={(e) => handleSearch(e.target.value)}
                />
                {search && (
                  <button className={styles.searchClear} onClick={() => handleSearch('')}>×</button>
                )}
              </div>
            </div>

            {filtered.length === 0 ? (
              <div className={styles.noResults}>No users match "{search}"</div>
            ) : (
              <>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th className={styles.th}>Name</th>
                      <th className={styles.th}>Token</th>
                      <th className={styles.th}>Created</th>
                      <th className={styles.th}>Last seen</th>
                      <th className={styles.th}>Cost</th>
                      <th className={styles.th}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pageSlice.map((u) => {
                      const isAnon = u.id === ANON_ID
                      return (
                        <tr
                          key={u.id}
                          className={`${styles.tr} ${styles.trClickable}`}
                          onClick={() => handleRowClick(u)}
                          title={isAnon ? 'View anonymous activity' : `View ${u.name}'s activity`}
                        >
                          <td className={styles.td}>
                            <span className={isAnon ? styles.userNameAnon : styles.userName}>{u.name}</span>
                          </td>
                          <td className={styles.td}>
                            {isAnon
                              ? <span className={styles.dimText}>—</span>
                              : <CopyToken token={u.token} />
                            }
                          </td>
                          <td className={styles.td}>
                            <span className={styles.dimText}>
                              {isAnon ? '—' : new Date(u.created_at).toLocaleDateString()}
                            </span>
                          </td>
                          <td className={styles.td}>
                            <span className={styles.dimText}>
                              {u.last_seen ? new Date(u.last_seen).toLocaleString() : '—'}
                            </span>
                          </td>
                          <td className={styles.td}>
                            <span className={styles.dimText}>${u.cost.toFixed(4)}</span>
                          </td>
                          <td className={styles.td}>
                            <div className={styles.actions}>
                              {!isAnon && (
                                <button
                                  className={styles.actionBtn}
                                  title="Rotate token"
                                  onClick={(e) => handleRotate(e, u)}
                                >
                                  <RotateCcw size={13} />
                                </button>
                              )}
                              <button
                                className={`${styles.actionBtn} ${styles.actionBtnDanger}`}
                                title={isAnon ? 'Delete anonymous data' : 'Delete user'}
                                onClick={(e) => handleDeleteClick(e, u)}
                              >
                                <Trash2 size={13} />
                              </button>
                            </div>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>

                {totalPages > 1 && (
                  <div className={styles.pagination}>
                    <button
                      className={styles.pageBtn}
                      disabled={safePage === 0}
                      onClick={() => setPage(safePage - 1)}
                    >
                      <ChevronLeft size={14} />
                    </button>
                    <span className={styles.pageInfo}>
                      {safePage + 1} / {totalPages}
                    </span>
                    <button
                      className={styles.pageBtn}
                      disabled={safePage >= totalPages - 1}
                      onClick={() => setPage(safePage + 1)}
                    >
                      <ChevronRight size={14} />
                    </button>
                  </div>
                )}
              </>
            )}
          </Card>
        </>
      )}

      {showAdd && (
        <AddUserModal onClose={() => setShowAdd(false)} onCreated={handleCreated} />
      )}
      {deleteTarget && (
        <DeleteUserModal
          user={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={(mode) => handleDeleteConfirmed(deleteTarget, mode)}
        />
      )}
    </div>
  )
}
