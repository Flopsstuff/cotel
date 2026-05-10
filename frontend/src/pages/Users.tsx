import { useState, useCallback } from 'react'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import { Copy, Check, Plus, RotateCcw, Trash2 } from 'lucide-react'
import { useUsers, createUser, rotateUserToken, deleteUser } from '../api'
import type { User } from '../api'
import { Card, EmptyState, ErrorState, LoadingSkeleton, ChartSkeleton, ChartTooltip } from '../components'
import styles from './Users.module.css'

const CHART_COLORS = [
  'var(--color-chart-1)',
  'var(--color-chart-2)',
  'var(--color-chart-3)',
  'var(--color-chart-4)',
  'var(--color-chart-5)',
]

function CopyToken({ token }: { token: string }) {
  const [copied, setCopied] = useState(false)
  const copy = useCallback(async () => {
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

export default function Users() {
  const { data, error, isLoading, mutate } = useUsers()
  const [showAdd, setShowAdd] = useState(false)
  const [newUser, setNewUser] = useState<User | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const users = data?.users ?? []
  const chartData = users.slice(0, 10).map((u, i) => ({
    name: u.name,
    cost: u.cost,
    colorIdx: i,
  }))

  const handleCreated = (u: User) => {
    setShowAdd(false)
    setNewUser(u)
    mutate()
  }

  const handleRotate = async (u: User) => {
    if (!confirm(`Rotate token for "${u.name}"? The old token will stop working immediately.`)) return
    setActionError(null)
    try {
      await rotateUserToken(u.id)
      mutate()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Rotate failed')
    }
  }

  const handleDelete = async (u: User) => {
    if (!confirm(`Delete user "${u.name}"? This cannot be undone.`)) return
    setActionError(null)
    try {
      await deleteUser(u.id)
      mutate()
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Delete failed')
    }
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

          <Card title={`All users (${users.length})`}>
            <div className={styles.helpCallout}>
              Copy a token and set{' '}
              <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer &lt;token&gt;</code>{' '}
              in your Claude Code settings (<code className={styles.code}>~/.claude/settings.json</code> or env).
            </div>

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
                {users.map((u) => (
                  <tr key={u.id} className={styles.tr}>
                    <td className={styles.td}>
                      <span className={styles.userName}>{u.name}</span>
                    </td>
                    <td className={styles.td}>
                      <CopyToken token={u.token} />
                    </td>
                    <td className={styles.td}>
                      <span className={styles.dimText}>{new Date(u.created_at).toLocaleDateString()}</span>
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
                        <button
                          className={styles.actionBtn}
                          title="Rotate token"
                          onClick={() => handleRotate(u)}
                        >
                          <RotateCcw size={13} />
                        </button>
                        <button
                          className={`${styles.actionBtn} ${styles.actionBtnDanger}`}
                          title="Delete user"
                          onClick={() => handleDelete(u)}
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </>
      )}

      {showAdd && (
        <AddUserModal onClose={() => setShowAdd(false)} onCreated={handleCreated} />
      )}
    </div>
  )
}
