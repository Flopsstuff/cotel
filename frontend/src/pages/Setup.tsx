import { useState, useCallback, useEffect } from 'react'
import { Check, Copy, Settings, Download } from 'lucide-react'
import { unzipSync } from 'fflate'
import { TabBar } from '../components/TabBar'
import { SegmentedControl } from '../components/SegmentedControl'
import { DropZone } from '../components/DropZone'
import { ManifestCard } from '../components/ManifestCard'
import type { ZipManifest } from '../components/ManifestCard'
import { InlineAlert } from '../components/InlineAlert'
import { Toast } from '../components/Toast'
import { LoadingSkeleton, ErrorState } from '../components'
import { useSettings, updateSettings } from '../api'
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
          title="Add yourself as a user (optional)"
          description="Create a named user to track your usage and optionally restrict anonymous access."
        >
          <ol className={styles.verifyList}>
            <li>Go to <a href="/users" className={styles.link}>Users</a> and click <strong>Add user</strong>.</li>
            <li>Copy the generated token.</li>
            <li>Add to Claude Code settings:</li>
          </ol>
          <div className={styles.codeBlock}>
            <CopyButton text={OTLP_HEADERS_SNIPPET} />
            <pre className={styles.pre}>{OTLP_HEADERS_SNIPPET}</pre>
          </div>
          <p className={styles.hint}>
            Replace <code className={styles.code}>YOUR_TOKEN_HERE</code> with the token shown after user creation.
          </p>
          <p className={styles.hint}>
            To block unauthenticated sends, open the{' '}
            <a href="/setup" className={styles.link}>Settings tab</a>{' '}
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
            When enabled, spans without an <code className={styles.code}>Authorization</code> header are accepted and attributed to no user.
            Disable to require every OTLP sender to present a valid user token.
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
          <code className={styles.code}>Authorization: Bearer &lt;token&gt;</code>.{' '}
          Go to <a href="/users" className={styles.link}>Users</a> to manage tokens.
        </div>
      )}

      {saveError && (
        <div className={styles.errorMsg}>{saveError}</div>
      )}
    </div>
  )
}

// ─── Page ────────────────────────────────────────────────────────────────────

type TabId = 'getting-started' | 'export' | 'import' | 'settings'

const TABS = [
  { id: 'getting-started', label: 'Getting Started' },
  { id: 'export', label: 'Export' },
  { id: 'import', label: 'Import' },
  { id: 'settings', label: 'Settings' },
]

export default function Setup() {
  const [activeTab, setActiveTab] = useState<TabId>('getting-started')

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
