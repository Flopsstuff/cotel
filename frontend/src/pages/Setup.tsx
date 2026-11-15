import { useState, useCallback, useEffect, useRef } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Check, Copy, Settings, Download, Plus, Trash2 } from 'lucide-react'
import { unzipSync } from 'fflate'
import { TabBar } from '../components/TabBar'
import { SegmentedControl } from '../components/SegmentedControl'
import { DropZone } from '../components/DropZone'
import { ManifestCard } from '../components/ManifestCard'
import type { ZipManifest } from '../components/ManifestCard'
import { InlineAlert } from '../components/InlineAlert'
import { Toast } from '../components/Toast'
import { LoadingSkeleton, ErrorState, EmptyState } from '../components'
import { useSettings, updateSettings, useIngestKeys, createIngestKey, revokeIngestKey } from '../api'
import type { IngestKey, CreatedIngestKey } from '../api'
import styles from './Setup.module.css'

// ─── Getting Started content ────────────────────────────────────────────────

const DOCKER_CMD = `docker run -d \\
  --name cotel \\
  -p 4318:4318 \\
  -p 8080:8080 \\
  -v cotel-data:/data \\
  ghcr.io/flopsstuff/cotel:main`

const DEFAULT_INGEST = 'http://localhost:4318'

function buildSettingsJson(ingestBase: string): string {
  return `{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "${ingestBase}/v1/traces"
  }
}`
}

function buildAgentPrompt(ingestBase: string): string {
  return `Enable Claude Code telemetry so my sessions are tracked locally. \
Edit the file ~/.claude/settings.json and merge in the following JSON — create the file if it doesn't exist:

{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "${ingestBase}/v1/traces"
  }
}

After saving, tell me to restart Claude Code so the changes take effect.`
}

const OTLP_HEADERS_SNIPPET = `{
  "env": {
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer YOUR_TOKEN_HERE"
  }
}`

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  const copy = useCallback(async () => {
    await navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [text])

  return (
    <button className={styles.copyBtn} onClick={copy} title="Copy to clipboard">
      {copied ? <Check size={14} /> : <Copy size={14} />}
      <span>{copied ? 'Copied!' : 'Copy'}</span>
    </button>
  )
}

interface StepProps {
  number: number
  title: string
  description: string
  children: React.ReactNode
}

function Step({ number, title, description, children }: StepProps) {
  return (
    <div className={styles.step}>
      <div className={styles.stepHeader}>
        <span className={styles.stepNumber}>{number}</span>
        <div>
          <div className={styles.stepTitle}>{title}</div>
          <div className={styles.stepDesc}>{description}</div>
        </div>
      </div>
      {children}
    </div>
  )
}

function GettingStartedTab() {
  const [ingestBase, setIngestBase] = useState(DEFAULT_INGEST)
  const [publicURL, setPublicURL] = useState('')

  useEffect(() => {
    fetch('/api/v1/health')
      .then(r => r.ok ? r.json() : null)
      .then((data: { public_ingest_url?: string } | null) => {
        if (data?.public_ingest_url) {
          const u = data.public_ingest_url.replace(/\/+$/, '')
          setPublicURL(u)
          setIngestBase(u)
        }
      })
      .catch(() => {})
  }, [])

  const settingsJson = buildSettingsJson(ingestBase)
  const agentPrompt = buildAgentPrompt(ingestBase)

  return (
    <>
      {publicURL && (
        <div className={styles.alertWrap} style={{ marginBottom: 'var(--space-4)' }}>
          <InlineAlert kind="info" message={`Public ingest URL active — snippets use ${publicURL}`} />
        </div>
      )}
      <div className={styles.steps}>
        <Step
          number={1}
          title="Start cotel"
          description="One Docker command — data persists in a named volume."
        >
          <div className={styles.codeBlock}>
            <CopyButton text={DOCKER_CMD} />
            <pre className={styles.pre}>{DOCKER_CMD}</pre>
          </div>
          <p className={styles.hint}>
            Dashboard is available at <code className={styles.code}>http://localhost:8080</code> once the container starts.
          </p>
        </Step>

        <Step
          number={2}
          title="Configure Claude Code"
          description="Add these environment variables to your global Claude Code settings."
        >
          <p className={styles.hint}>
            Open <code className={styles.code}>~/.claude/settings.json</code> and merge in:
          </p>
          <div className={styles.codeBlock}>
            <CopyButton text={settingsJson} />
            <pre className={styles.pre}>{settingsJson}</pre>
          </div>
        </Step>

        <Step
          number={3}
          title="Verify"
          description="Restart Claude Code, run any session, then check the Overview page."
        >
          <ul className={styles.verifyList}>
            <li>Restart Claude Code after editing <code className={styles.code}>settings.json</code>.</li>
            <li>Start any session (a simple question is enough).</li>
            <li>
              Open <a href="/" className={styles.link}>Overview</a> — the session appears within 30 seconds.
            </li>
          </ul>
        </Step>

        <Step
          number={4}
          title="Give each sender its own token (optional)"
          description="An actor is whoever sends telemetry - you, an AI agent or a host. Give each one its own token to see its usage separately and to restrict anonymous access."
        >
          <ol className={styles.verifyList}>
            <li>Go to <a href="/actors" className={styles.link}>Actors</a> and click <strong>Add actor</strong>.</li>
            <li>Copy the generated token.</li>
            <li>Add to Claude Code settings:</li>
          </ol>
          <div className={styles.codeBlock}>
            <CopyButton text={OTLP_HEADERS_SNIPPET} />
            <pre className={styles.pre}>{OTLP_HEADERS_SNIPPET}</pre>
          </div>
          <p className={styles.hint}>
            Replace <code className={styles.code}>YOUR_TOKEN_HERE</code> with the token shown after the actor is created.
          </p>
          <p className={styles.hint}>
            To block unauthenticated sends, open the{' '}
            <Link to="/setup?tab=settings" className={styles.link}>Settings tab</Link>{' '}
            and disable <strong>Allow anonymous OTLP</strong>.
          </p>
        </Step>
      </div>

      <div className={styles.promptSection}>
        <div className={styles.promptHeader}>
          <div className={styles.promptTitle}>Agent prompt</div>
          <div className={styles.promptSubtitle}>
            Paste this into any Claude Code session and Claude will configure telemetry for you automatically.
          </div>
        </div>
        <div className={styles.codeBlock}>
          <CopyButton text={agentPrompt} />
          <pre className={styles.pre}>{agentPrompt}</pre>
        </div>
      </div>

      <div className={styles.linksRow}>
        <a
          href="https://github.com/Flopsstuff/cotel"
          target="_blank"
          rel="noopener noreferrer"
          className={styles.link}
        >
          GitHub
        </a>
        <span className={styles.linksSep}>·</span>
        <a
          href="https://flopsstuff.github.io/cotel"
          target="_blank"
          rel="noopener noreferrer"
          className={styles.link}
        >
          Documentation
        </a>
      </div>
    </>
  )
}

// ─── Export tab ──────────────────────────────────────────────────────────────

type Period = 'day' | 'week' | 'month'

const PERIOD_OPTIONS = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
]

function getYesterday(): string {
  const d = new Date()
  d.setDate(d.getDate() - 1)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

type ExportState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'success'; filename: string; sizeKb: number }
  | { kind: 'error'; message: string }

function ExportTab() {
  const [period, setPeriod] = useState<Period>('day')
  const [date, setDate] = useState(getYesterday)
  const [state, setState] = useState<ExportState>({ kind: 'idle' })

  const handleExport = async () => {
    setState({ kind: 'loading' })
    const ac = new AbortController()
    const timer = setTimeout(() => ac.abort(), 30_000)
    try {
      const res = await fetch(`/api/v1/export?period=${period}&date=${date}`, { signal: ac.signal })
      if (!res.ok) {
        const text = await res.text().catch(() => res.statusText)
        throw new Error(text || `${res.status} ${res.statusText}`)
      }
      const blob = await res.blob()
      const filename = `cotel-export-${period}-${date}.zip`
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = filename
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(a.href)
      setState({ kind: 'success', filename, sizeKb: Math.round(blob.size / 1024) })
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setState({ kind: 'error', message: 'Request timed out' })
      } else {
        setState({ kind: 'error', message: err instanceof Error ? err.message : 'Export failed' })
      }
    } finally {
      clearTimeout(timer)
    }
  }

  return (
    <div className={styles.panelCard}>
      <div className={styles.panelTitle}>Export data</div>
      <p className={styles.panelDesc}>
        Download telemetry data for a selected period as a ZIP archive containing spans and daily usage CSVs.
      </p>

      <div className={styles.controlRow}>
        <SegmentedControl
          options={PERIOD_OPTIONS}
          value={period}
          onChange={(v) => { setPeriod(v as Period); setState({ kind: 'idle' }) }}
        />
        <input
          type="date"
          className={styles.dateInput}
          value={date}
          onChange={(e) => { setDate(e.target.value); setState({ kind: 'idle' }) }}
        />
        <button
          type="button"
          className={styles.primaryBtn}
          onClick={handleExport}
          disabled={state.kind === 'loading'}
        >
          {state.kind === 'loading' ? (
            <><span className={styles.spinner} />Exporting…</>
          ) : (
            <><Download size={14} />Export</>
          )}
        </button>
      </div>

      {state.kind === 'success' && (
        <div className={styles.alertWrap}>
          <InlineAlert kind="success" message={`Saved ${state.filename} (${state.sizeKb} KB)`} />
        </div>
      )}
      {state.kind === 'error' && (
        <div className={styles.alertWrap}>
          <InlineAlert kind="error" message={state.message} />
        </div>
      )}
    </div>
  )
}

// ─── Import tab ──────────────────────────────────────────────────────────────

type ImportState =
  | { kind: 'idle' }
  | { kind: 'parsing' }
  | { kind: 'ready'; file: File; manifest: ZipManifest }
  | { kind: 'parse-error'; message: string }
  | { kind: 'loading' }
  | { kind: 'success'; message: string }
  | { kind: 'error'; message: string }

async function parseZipManifest(file: File): Promise<ZipManifest> {
  const buf = await file.arrayBuffer()
  const files = unzipSync(new Uint8Array(buf), {
    filter: (f) => f.name === 'manifest.json',
  })
  const bytes = files['manifest.json']
  if (!bytes) throw new Error('No manifest.json found in this ZIP')
  const json = new TextDecoder().decode(bytes)
  return JSON.parse(json) as ZipManifest
}

function ImportTab() {
  const [state, setState] = useState<ImportState>({ kind: 'idle' })

  const reset = useCallback(() => setState({ kind: 'idle' }), [])

  const handleFile = useCallback(async (file: File) => {
    setState({ kind: 'parsing' })
    try {
      const manifest = await parseZipManifest(file)
      setState({ kind: 'ready', file, manifest })
    } catch (err) {
      setState({ kind: 'parse-error', message: err instanceof Error ? err.message : 'Failed to read ZIP' })
    }
  }, [])

  const handleConfirm = async () => {
    if (state.kind !== 'ready') return
    const { file } = state
    setState({ kind: 'loading' })
    const ac = new AbortController()
    const timer = setTimeout(() => ac.abort(), 60_000)
    try {
      const formData = new FormData()
      formData.append('file', file)
      const res = await fetch('/api/v1/import', { method: 'POST', body: formData, signal: ac.signal })
      if (!res.ok) {
        const text = await res.text().catch(() => res.statusText)
        throw new Error(text || `${res.status} ${res.statusText}`)
      }
      const data = await res.json()
      const spans = (data.spans_imported ?? 0).toLocaleString()
      const daily = (data.daily_usage_imported ?? 0).toLocaleString()
      setState({ kind: 'success', message: `Imported ${spans} spans, ${daily} daily rows` })
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setState({ kind: 'error', message: 'Request timed out' })
      } else {
        setState({ kind: 'error', message: err instanceof Error ? err.message : 'Import failed' })
      }
    } finally {
      clearTimeout(timer)
    }
  }

  return (
    <div className={styles.panelCard}>
      <div className={styles.panelTitle}>Import data</div>
      <p className={styles.panelDesc}>
        Upload a cotel export ZIP to restore or merge telemetry data into this instance.
      </p>

      {(state.kind === 'idle' || state.kind === 'parse-error') && (
        <>
          <DropZone onFile={handleFile} />
          {state.kind === 'parse-error' && (
            <div className={styles.alertWrap}>
              <InlineAlert kind="error" message={state.message} />
            </div>
          )}
        </>
      )}

      {state.kind === 'parsing' && (
        <div className={styles.alertWrap}>
          <InlineAlert kind="info" message="Reading ZIP manifest…" />
        </div>
      )}

      {state.kind === 'ready' && (
        <>
          <ManifestCard manifest={state.manifest} filename={state.file.name} />
          <div className={styles.actionRow}>
            <button type="button" className={styles.primaryBtn} onClick={handleConfirm}>
              Confirm Import
            </button>
            <button type="button" className={styles.secondaryBtn} onClick={reset}>
              Cancel
            </button>
          </div>
        </>
      )}

      {state.kind === 'loading' && (
        <div className={styles.alertWrap}>
          <InlineAlert kind="info" message="Uploading and importing…" />
        </div>
      )}

      {state.kind === 'error' && (
        <>
          <div className={styles.alertWrap}>
            <InlineAlert kind="error" message={state.message} />
          </div>
          <div className={styles.actionRow}>
            <button type="button" className={styles.secondaryBtn} onClick={reset}>
              Try another file
            </button>
          </div>
        </>
      )}

      {state.kind === 'success' && (
        <Toast kind="success" message={state.message} onDismiss={reset} />
      )}
    </div>
  )
}

// ─── Settings tab ────────────────────────────────────────────────────────────

function SettingsTab() {
  return (
    <>
      <AnonymousIngestSection />
      <IngestKeysSection />
    </>
  )
}

function AnonymousIngestSection() {
  const { data, error, isLoading, mutate } = useSettings()
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const handleToggle = async (checked: boolean) => {
    setSaving(true)
    setSaveError(null)
    try {
      await updateSettings({ allow_anonymous: checked })
      await mutate()
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (isLoading) return <LoadingSkeleton rows={3} height={32} />
  if (error) return <ErrorState message={error.message} />

  return (
    <div className={styles.section}>
      <div className={styles.sectionTitle}>OTLP Ingest</div>

      <div className={styles.settingRow}>
        <div className={styles.settingInfo}>
          <div className={styles.settingLabel}>Allow anonymous OTLP</div>
          <div className={styles.settingDesc}>
            When enabled, spans without an <code className={styles.code}>Authorization</code> header are accepted and attributed to no actor.
            Disable to require every OTLP sender to present a valid actor token or ingest key.
          </div>
        </div>
        <label className={styles.toggle}>
          <input
            type="checkbox"
            className={styles.toggleInput}
            checked={data?.allow_anonymous ?? true}
            disabled={saving}
            onChange={(e) => handleToggle(e.target.checked)}
          />
          <span className={styles.toggleTrack} />
        </label>
      </div>

      {data?.allow_anonymous === false && (
        <div className={styles.warning}>
          Anonymous OTLP is disabled. All ingest requests must include{' '}
          <code className={styles.code}>Authorization: Bearer &lt;actor token or ingest key&gt;</code>.{' '}
          Actor tokens are on the <a href="/actors" className={styles.link}>Actors</a> pages; ingest keys are below.
        </div>
      )}

      {saveError && (
        <div className={styles.errorMsg}>{saveError}</div>
      )}
    </div>
  )
}

// ─── Ingest keys ─────────────────────────────────────────────────────────────

function formatDate(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })
}

function senderSnippet(key: string): string {
  return `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer ${key}
COTEL_ACTOR_TOKEN=<the actor's token>   # sent as X-Cotel-Actor-Token by the headers helper`
}

function Modal({ titleId, children, onClose }: { titleId: string; children: React.ReactNode; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className={styles.overlay} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className={styles.modal} role="dialog" aria-modal="true" aria-labelledby={titleId}>
        {children}
      </div>
    </div>
  )
}

function IngestKeysSection() {
  const { data, error, isLoading, mutate } = useIngestKeys()
  const [name, setName] = useState('')
  const [nameMissing, setNameMissing] = useState(false)
  const nameRef = useRef<HTMLInputElement>(null)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreatedIngestKey | null>(null)
  const [revoking, setRevoking] = useState<IngestKey | null>(null)
  const [revokeBusy, setRevokeBusy] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      // The button stays clickable; an empty name is answered here, visibly.
      setNameMissing(true)
      nameRef.current?.focus()
      return
    }
    setCreating(true)
    setCreateError(null)
    try {
      const res = await createIngestKey(trimmed)
      setCreated(res)
      setName('')
      await mutate()
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Create failed')
    } finally {
      setCreating(false)
    }
  }

  const closeRevoke = useCallback(() => {
    if (revokeBusy) return
    setRevoking(null)
    setRevokeError(null)
  }, [revokeBusy])

  const handleRevoke = async () => {
    if (!revoking) return
    setRevokeBusy(true)
    setRevokeError(null)
    try {
      await revokeIngestKey(revoking.id)
      const revokedId = revoking.id
      setCreated((cur) => (cur?.id === revokedId ? null : cur))
      setRevoking(null)
    } catch (err) {
      setRevokeError(err instanceof Error ? err.message : 'Revoke failed')
    } finally {
      setRevokeBusy(false)
      await mutate()
    }
  }

  const keys = data?.ingest_keys ?? []

  return (
    <div className={styles.section}>
      <div className={styles.sectionTitle}>Ingest keys</div>
      <p className={styles.settingDesc}>
        An ingest key authorizes OTLP ingest for many senders at once, for example every agent in a
        Paperclip company. Each sender sends the key as{' '}
        <code className={styles.code}>Authorization: Bearer &lt;key&gt;</code> and identifies itself
        with its actor token in <code className={styles.code}>X-Cotel-Actor-Token</code>; without it,
        spans are stored as Anonymous. An ingest key only authorizes ingest: it is refused on export and import.
      </p>

      <form className={styles.keyForm} onSubmit={handleCreate}>
        <label className={styles.srOnly} htmlFor="ingest-key-name">Key name</label>
        <input
          id="ingest-key-name"
          ref={nameRef}
          className={`${styles.textInput} ${nameMissing ? styles.textInputInvalid : ''}`}
          placeholder="Key name, e.g. paperclip-acme"
          value={name}
          onChange={(e) => { setName(e.target.value); if (e.target.value.trim()) setNameMissing(false) }}
          disabled={creating}
          maxLength={100}
          aria-invalid={nameMissing}
          aria-describedby={nameMissing ? 'ingest-key-name-error' : undefined}
        />
        <button type="submit" className={styles.primaryBtn} disabled={creating}>
          {creating ? <><span className={styles.spinner} />Generating…</> : <><Plus size={14} />Generate key</>}
        </button>
      </form>
      {nameMissing && (
        <div id="ingest-key-name-error" className={styles.fieldError}>
          Enter a name for the key, e.g. the company or fleet that will use it.
        </div>
      )}
      {createError && (
        <div className={styles.alertWrap}>
          <InlineAlert kind="error" message={createError} />
        </div>
      )}

      {created && (
        <div className={styles.newKey}>
          <div className={styles.newKeyHeader}>
            <span className={styles.newKeyTitle}>Key “{created.name}” created</span>
            <button type="button" className={styles.secondaryBtn} onClick={() => setCreated(null)}>
              Done
            </button>
          </div>
          <div className={styles.codeBlock}>
            <CopyButton text={created.token} />
            <pre className={`${styles.pre} ${styles.preWrap}`}>{created.token}</pre>
          </div>
          <p className={styles.hint}>
            Each sender sets these environment variables. Actor tokens are on each actor's page under{' '}
            <Link to="/actors" className={styles.link}>Actors</Link>.
          </p>
          <div className={styles.codeBlock}>
            <CopyButton text={senderSnippet(created.token)} />
            <pre className={`${styles.pre} ${styles.preScroll}`}>{senderSnippet(created.token)}</pre>
          </div>
        </div>
      )}

      <div className={styles.keyList}>
        {isLoading ? (
          <LoadingSkeleton rows={2} height={32} />
        ) : error ? (
          <ErrorState message={error.message} />
        ) : keys.length === 0 ? (
          <EmptyState
            heading="No ingest keys"
            subtext="Generate one to let many senders share a single Authorization header."
          />
        ) : (
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th className={styles.th}>Name</th>
                  <th className={styles.th}>Key</th>
                  <th className={styles.th}>Created</th>
                  <th className={styles.th}><span className={styles.srOnly}>Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.id} className={styles.tr}>
                    <td className={styles.td}>{k.name}</td>
                    <td className={styles.td}>
                      <span className={styles.keyCell}>
                        <code className={styles.code} title={k.token}>{k.token.slice(0, 16)}…</code>
                        <CopyButton text={k.token} />
                      </span>
                    </td>
                    <td className={styles.td}>{formatDate(k.created_at)}</td>
                    <td className={`${styles.td} ${styles.tdActions}`}>
                      <button
                        type="button"
                        className={styles.revokeBtn}
                        onClick={() => { setRevokeError(null); setRevoking(k) }}
                      >
                        <Trash2 size={13} />
                        Revoke
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {revoking && (
        <Modal titleId="revoke-key-title" onClose={closeRevoke}>
          <h2 className={styles.dialogTitle} id="revoke-key-title">Revoke ingest key?</h2>
          <p className={styles.dialogBody}>
            <strong>{revoking.name}</strong> (<code className={styles.code}>{revoking.token.slice(0, 16)}…</code>)
            stops working immediately for every sender that uses it. This cannot be undone.
          </p>
          {revokeError && (
            <div className={styles.alertWrap}>
              <InlineAlert kind="error" message={revokeError} />
            </div>
          )}
          <div className={styles.actionRow}>
            <button
              type="button"
              className={`${styles.primaryBtn} ${styles.dangerPrimaryBtn}`}
              onClick={handleRevoke}
              disabled={revokeBusy}
            >
              {revokeBusy ? <><span className={styles.spinner} />Revoking…</> : 'Revoke'}
            </button>
            <button type="button" className={styles.secondaryBtn} onClick={closeRevoke} disabled={revokeBusy}>
              Cancel
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}

// ─── Page ────────────────────────────────────────────────────────────────────

type TabId = 'getting-started' | 'export' | 'import' | 'settings'

const TAB_IDS: TabId[] = ['getting-started', 'export', 'import', 'settings']

function isTabId(v: string | null): v is TabId {
  return v !== null && (TAB_IDS as string[]).includes(v)
}

const TABS = [
  { id: 'getting-started', label: 'Getting Started' },
  { id: 'export', label: 'Export' },
  { id: 'import', label: 'Import' },
  { id: 'settings', label: 'Settings' },
]

export default function Setup() {
  // The tab lives in ?tab= so other pages can link straight to one,
  // e.g. /setup?tab=settings for ingest keys.
  const [searchParams, setSearchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const activeTab: TabId = isTabId(tabParam) ? tabParam : 'getting-started'
  const setActiveTab = (id: TabId) => {
    setSearchParams(id === 'getting-started' ? {} : { tab: id }, { replace: true })
  }

  return (
    <div>
      <div className={styles.header}>
        <Settings size={20} className={styles.headerIcon} />
        <div>
          <h1 className={styles.title}>Setup</h1>
          <p className={styles.subtitle}>
            Configure telemetry, manage data, and adjust runtime settings.
          </p>
        </div>
      </div>

      <TabBar tabs={TABS} activeTab={activeTab} onChange={(id) => setActiveTab(id as TabId)} />

      {activeTab === 'getting-started' && (
        <div id="panel-getting-started" role="tabpanel" aria-labelledby="tab-getting-started">
          <GettingStartedTab />
        </div>
      )}
      {activeTab === 'export' && (
        <div id="panel-export" role="tabpanel" aria-labelledby="tab-export">
          <ExportTab />
        </div>
      )}
      {activeTab === 'import' && (
        <div id="panel-import" role="tabpanel" aria-labelledby="tab-import">
          <ImportTab />
        </div>
      )}
      {activeTab === 'settings' && (
        <div id="panel-settings" role="tabpanel" aria-labelledby="tab-settings">
          <SettingsTab />
        </div>
      )}
    </div>
  )
}
