import { useState, useCallback, useEffect, useRef } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Copy, Check, RotateCcw, Trash2, Activity, ListTree } from 'lucide-react'
import { useActor, rotateActorToken, deleteActor, setActorPaperclipAgentId } from '../api'
import type { Actor } from '../api'
import { Card, KpiCard, LoadingSkeleton, ErrorState, SegmentedControl, DeleteActorModal } from '../components'
import { getCookie, setCookie } from '../lib/cookie'
import styles from './ActorDetail.module.css'

const ANON_ID = '__anonymous__'
const RANGE_COOKIE = 'cotel_actors_range'
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

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

interface PaperclipAgentCardProps {
  actorId: string
  value: string | null
  onSaved: () => Promise<unknown>
}

// PaperclipAgentCard maps a Paperclip agent UUID to the actor, so spans that
// Paperclip sends with the shared ingest key land on this actor.
function PaperclipAgentCard({ actorId, value, onSaved }: PaperclipAgentCardProps) {
  const [input, setInput] = useState(value ?? '')
  const [invalid, setInvalid] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => { setInput(value ?? '') }, [value])
  useEffect(() => () => { if (savedTimer.current) clearTimeout(savedTimer.current) }, [])

  const submit = async (next: string) => {
    setSaving(true)
    setSaved(false)
    setSaveError(null)
    try {
      const actor = await setActorPaperclipAgentId(actorId, next)
      setInput(actor.paperclip_agent_id ?? '')
      await onSaved()
      setSaved(true)
      if (savedTimer.current) clearTimeout(savedTimer.current)
      savedTimer.current = setTimeout(() => setSaved(false), 3000)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = input.trim()
    if (!UUID_RE.test(trimmed)) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    void submit(trimmed)
  }

  const handleClear = () => {
    setInvalid(false)
    void submit('')
  }

  return (
    <Card title="Paperclip agent">
      <p className={styles.cardDesc}>
        Spans that Paperclip sends with the shared ingest key are attributed to this actor when their{' '}
        <code className={styles.code}>paperclip.agent.id</code> matches. Find the agent&apos;s ID in
        Paperclip, in the agent page URL or the agent settings.
      </p>
      <form className={styles.fieldRow} onSubmit={handleSave}>
        <label className={styles.srOnly} htmlFor="paperclip-agent-id">Paperclip agent ID</label>
        <input
          id="paperclip-agent-id"
          className={`${styles.textInput} ${invalid ? styles.textInputInvalid : ''}`}
          placeholder="00000000-0000-0000-0000-000000000000"
          value={input}
          onChange={(e) => {
            setInput(e.target.value)
            setSaved(false)
            if (UUID_RE.test(e.target.value.trim())) setInvalid(false)
          }}
          disabled={saving}
          spellCheck={false}
          autoComplete="off"
          aria-invalid={invalid}
          aria-describedby={invalid ? 'paperclip-agent-id-error' : undefined}
        />
        <button type="submit" className={styles.primaryBtn} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
        {value && (
          <button type="button" className={styles.linkBtn} onClick={handleClear} disabled={saving}>
            Clear
          </button>
        )}
        {saved && <span className={styles.savedNote}><Check size={14} /> Saved</span>}
      </form>
      {invalid && (
        <div id="paperclip-agent-id-error" className={styles.fieldError}>
          Enter the agent&apos;s UUID, e.g. 3f2b8c1e-9a4d-4e6f-b7c2-1d5e8a9f0b3c.
        </div>
      )}
      {saveError && <div className={styles.fieldError}>{saveError}</div>}
    </Card>
  )
}

function isRangeKey(v: string | null): v is RangeKey {
  return v === 'all' || v === 'year' || v === 'month' || v === 'week' || v === 'day'
}

export default function ActorDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const [range, setRange] = useState<RangeKey>(() => {
    const c = getCookie(RANGE_COOKIE)
    return isRangeKey(c) ? c : 'month'
  })
  const { data, error, isLoading, mutate } = useActor(id, range)

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
      await rotateActorToken(data.id)
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
      await deleteActor(ANON_ID)
      navigate('/actors')
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Delete failed')
    }
  }

  if (isLoading) return <LoadingSkeleton rows={6} height={44} />
  if (error) {
    const is404 = error.message.includes('404')
    return is404 ? (
      <div className={styles.notFound}>
        <p className={styles.notFoundTitle}>Actor not found</p>
        <p className={styles.notFoundSub}>Actor <code>{id}</code> does not exist.</p>
        <Link to="/actors" className={styles.backLink}>← Back to Actors</Link>
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
        <Link to="/actors" className={styles.backLink}>← Actors</Link>
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
        <Card title="Actor token">
          <div className={styles.tokenRow}>
            <code className={styles.tokenText}>{data.token}</code>
            <button className={styles.iconBtn} onClick={() => copyToken(data.token)} title="Copy token">
              {copied ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </div>
          {notice && <div className={styles.notice}>{notice}</div>}
          <ul className={styles.hintList}>
            <li>
              Send it directly: set{' '}
              <code className={styles.code}>OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer {data.token}</code>.
            </li>
            <li>
              With an <Link to="/setup?tab=settings" className={styles.hintLink}>ingest key</Link>: set{' '}
              <code className={styles.code}>COTEL_ACTOR_TOKEN={data.token}</code> and configure{' '}
              <code className={styles.code}>scripts/otel-headers-helper.py</code> as Claude Code&apos;s{' '}
              <code className={styles.code}>otelHeadersHelper</code>, which sends it as{' '}
              <code className={styles.code}>X-Cotel-Actor-Token</code>. Other senders set that header themselves.
            </li>
          </ul>
        </Card>
      )}

      {!isAnon && (
        <PaperclipAgentCard
          key={data.id}
          actorId={data.id}
          value={data.paperclip_agent_id ?? null}
          onSaved={() => mutate()}
        />
      )}

      <Card title="Actions">
        <div className={styles.actionsRow}>
          <Link className={styles.linkBtn} to={`/?actor_id=${encodeURIComponent(activityId)}`}>
            <Activity size={14} /> View activity
          </Link>
          <Link className={styles.linkBtn} to={`/sessions?actor_id=${encodeURIComponent(activityId)}`}>
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
              <Trash2 size={14} /> Delete actor
            </button>
          )}
        </div>
      </Card>

      {showDelete && !isAnon && (
        <DeleteActorModal
          actor={data as Actor}
          onClose={() => setShowDelete(false)}
          onDeleted={() => {
            setShowDelete(false)
            navigate('/actors')
          }}
        />
      )}
    </div>
  )
}
