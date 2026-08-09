import { useState, useCallback } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Copy, Check, RotateCcw, Trash2, Activity, ListTree } from 'lucide-react'
import { useUser, rotateUserToken, deleteUser } from '../api'
import type { User } from '../api'
import { Card, KpiCard, LoadingSkeleton, ErrorState, SegmentedControl, DeleteUserModal } from '../components'
import { getCookie, setCookie } from '../lib/cookie'
import styles from './UserDetail.module.css'

const ANON_ID = '__anonymous__'
const RANGE_COOKIE = 'cotel_users_range'
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365

type RangeKey = 'all' | 'year' | 'month' | 'week' | 'day'

const RANGE_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'year', label: 'Year' },
  { value: 'month', label: 'Month' },
  { value: 'week', label: 'Week' },
  { value: 'day', label: 'Day' },
]

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

export default function UserDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const [range, setRange] = useState<RangeKey>(() => {
    const c = getCookie(RANGE_COOKIE)
    return isRangeKey(c) ? c : 'month'
  })
  const { data, error, isLoading, mutate } = useUser(id, range)

  const [copied, setCopied] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [showDelete, setShowDelete] = useState(false)

  const changeRange = (v: string) => {
    if (!isRangeKey(v)) return
    setRange(v)
    setCookie(RANGE_COOKIE, v, COOKIE_MAX_AGE)
  }

  const copyToken = useCallback(async (token: string) => {
    await navigator.clipboard.writeText(token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [])

  const handleRotate = async () => {
    if (!data) return
    if (!confirm(`Rotate token for "${data.name}"? The old token will stop working immediately.`)) return
    setActionError(null)
    try {
      await rotateUserToken(data.id)
      await mutate()
      setNotice('Token rotated')
      setTimeout(() => setNotice(null), 3000)
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Rotate failed')
    }
  }

  const handleDeleteAnon = async () => {
    if (!confirm('Delete all anonymous telemetry? All unattributed spans and daily summaries will be permanently removed. This cannot be undone.')) return
    setActionError(null)
    try {
      await deleteUser(ANON_ID)
      navigate('/users')
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Delete failed')
    }
  }

  if (isLoading) return <LoadingSkeleton rows={6} height={44} />
  if (error) {
    const is404 = error.message.includes('404')
    return is404 ? (
      <div className={styles.notFound}>
        <p className={styles.notFoundTitle}>User not found</p>
        <p className={styles.notFoundSub}>User <code>{id}</code> does not exist.</p>
        <Link to="/users" className={styles.backLink}>← Back to Users</Link>
      </div>
    ) : (
      <ErrorState message={error.message} />
    )
  }
  if (!data) return null

  const isAnon = data.id === ANON_ID
  const activityId = isAnon ? ANON_ID : data.name
  const suffix = RANGE_SUFFIX[range]
  const scoped = (label: string) => (suffix ? `${label} (${suffix})` : label)

  return (
    <div>
      <div className={styles.nav}>
        <Link to="/users" className={styles.backLink}>← Users</Link>
      </div>

      <h1 className={styles.title}>
        <span className={isAnon ? styles.nameAnon : undefined}>{data.name}</span>
      </h1>

      <div className={styles.controlsRow}>
        <span className={styles.rangeLabel}>Cost &amp; sessions:</span>
        <SegmentedControl options={RANGE_OPTIONS} value={range} onChange={changeRange} />
      </div>

      {actionError && <div className={styles.actionError}>{actionError}</div>}

      <div className={styles.kpiRow}>
        <KpiCard label={scoped('Cost')} value={`$${data.cost.toFixed(2)}`} />
        <KpiCard label={scoped('Sessions')} value={data.sessions.toLocaleString()} />
        <KpiCard label="Created" value={isAnon || !data.created_at ? '—' : new Date(data.created_at).toLocaleDateString()} />
        <KpiCard label="Last seen" value={data.last_seen ? new Date(data.last_seen).toLocaleString() : '—'} />
      </div>

      {!isAnon && (
        <Card title="Token">
          <div className={styles.tokenRow}>
            <code className={styles.tokenText}>{data.token}</code>
            <button className={styles.iconBtn} onClick={() => copyToken(data.token)} title="Copy token">
              {copied ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </div>
          {notice && <div className={styles.notice}>{notice}</div>}
          <p className={styles.hint}>
            Set <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer {data.token}</code> in your Claude Code settings.
          </p>
        </Card>
      )}

      <Card title="Actions">
        <div className={styles.actionsRow}>
          <Link className={styles.linkBtn} to={`/?user_id=${encodeURIComponent(activityId)}`}>
            <Activity size={14} /> View activity
          </Link>
          <Link className={styles.linkBtn} to={`/sessions?user_id=${encodeURIComponent(activityId)}`}>
            <ListTree size={14} /> View sessions
          </Link>
          {!isAnon && (
            <button className={styles.linkBtn} onClick={handleRotate}>
              <RotateCcw size={14} /> Rotate token
            </button>
          )}
          {isAnon ? (
            <button className={`${styles.linkBtn} ${styles.dangerBtn}`} onClick={handleDeleteAnon}>
              <Trash2 size={14} /> Delete anonymous data
            </button>
          ) : (
            <button className={`${styles.linkBtn} ${styles.dangerBtn}`} onClick={() => setShowDelete(true)}>
              <Trash2 size={14} /> Delete user
            </button>
          )}
        </div>
      </Card>

      {showDelete && !isAnon && (
        <DeleteUserModal
          user={data as User}
          onClose={() => setShowDelete(false)}
          onDeleted={() => {
            setShowDelete(false)
            navigate('/users')
          }}
        />
      )}
    </div>
  )
}
