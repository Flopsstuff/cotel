import { useState, useCallback, useRef } from 'react'
import { Key, Plus, RefreshCw, Trash2, Copy, Check, AlertTriangle } from 'lucide-react'
import { mutate } from 'swr'
import { useTokens, createToken, rotateToken, revokeToken } from '../api'
import { EmptyState, ErrorState, LoadingSkeleton, InlineAlert } from '../components'
import styles from './Tokens.module.css'

// ─── Copy button ──────────────────────────────────────────────────────────────

function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = useCallback(async () => {
    await navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [text])
  return (
    <button
      type="button"
      className={[styles.copyBtn, className].filter(Boolean).join(' ')}
      onClick={copy}
      title="Copy to clipboard"
    >
      {copied ? <Check size={14} /> : <Copy size={14} />}
      <span>{copied ? 'Copied!' : 'Copy'}</span>
    </button>
  )
}

// ─── Modal ────────────────────────────────────────────────────────────────────

function Modal({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div className={styles.overlay} onClick={onClose}>
      <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
        {children}
      </div>
    </div>
  )
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function formatDate(iso: string) {
  return new Date(iso).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })
}

// ─── Dialog state ─────────────────────────────────────────────────────────────

type Dialog =
  | { kind: 'create' }
  | { kind: 'show-token'; name: string; plaintext: string; action: 'created' | 'rotated' }
  | { kind: 'confirm-rotate'; id: string; name: string }
  | { kind: 'confirm-revoke'; id: string; name: string }

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function Tokens() {
  const { data, error, isLoading } = useTokens()
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [busy, setBusy] = useState(false)
  const [apiError, setApiError] = useState<string | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)

  const closeDialog = useCallback(() => {
    setDialog(null)
    setApiError(null)
  }, [])

  const openCreate = useCallback(() => {
    setApiError(null)
    setDialog({ kind: 'create' })
  }, [])

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault()
    const name = nameRef.current?.value.trim() ?? ''
    if (!name) return
    setBusy(true)
    setApiError(null)
    try {
      const res = await createToken(name)
      await mutate('/api/v1/tokens')
      setDialog({ kind: 'show-token', name: res.name, plaintext: res.token, action: 'created' })
    } catch (err) {
      setApiError(err instanceof Error ? err.message : 'Create failed')
    } finally {
      setBusy(false)
    }
  }

  const handleRotateConfirm = async () => {
    if (dialog?.kind !== 'confirm-rotate') return
    const { id, name } = dialog
    setBusy(true)
    setApiError(null)
    try {
      const res = await rotateToken(id)
      await mutate('/api/v1/tokens')
      setDialog({ kind: 'show-token', name, plaintext: res.token, action: 'rotated' })
    } catch (err) {
      setApiError(err instanceof Error ? err.message : 'Rotate failed')
    } finally {
      setBusy(false)
    }
  }

  const handleRevokeConfirm = async () => {
    if (dialog?.kind !== 'confirm-revoke') return
    const { id } = dialog
    setBusy(true)
    setApiError(null)
    try {
      await revokeToken(id)
      await mutate('/api/v1/tokens')
      closeDialog()
    } catch (err) {
      setApiError(err instanceof Error ? err.message : 'Revoke failed')
    } finally {
      setBusy(false)
    }
  }

  const items = data?.items ?? []

  return (
    <div>
      <div className={styles.header}>
        <Key size={20} className={styles.headerIcon} />
        <div className={styles.headerText}>
          <h1 className={styles.title}>Tokens</h1>
          <p className={styles.subtitle}>Bearer tokens for OTLP authentication.</p>
        </div>
        <button type="button" className={styles.newBtn} onClick={openCreate}>
          <Plus size={14} />
          New Token
        </button>
      </div>

      {isLoading && <LoadingSkeleton rows={3} />}
      {error && <ErrorState message={error.message} />}

      {!isLoading && !error && items.length === 0 && (
        <EmptyState
          heading="No tokens yet"
          subtext="Create a bearer token to authenticate OTLP traffic."
        />
      )}

      {!isLoading && !error && items.length > 0 && (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th className={styles.th}>Name</th>
                <th className={styles.th}>Prefix</th>
                <th className={styles.th}>Created</th>
                <th className={styles.th}>Last used</th>
                <th className={styles.th} />
              </tr>
            </thead>
            <tbody>
              {items.map((t) => (
                <tr key={t.id} className={styles.tr}>
                  <td className={styles.td}>{t.name}</td>
                  <td className={styles.td}>
                    <code className={styles.prefix}>{t.prefix}…</code>
                  </td>
                  <td className={styles.td}>{formatDate(t.created_at)}</td>
                  <td className={styles.td}>
                    {t.last_used_at
                      ? formatDate(t.last_used_at)
                      : <span className={styles.never}>Never</span>}
                  </td>
                  <td className={styles.td}>
                    <div className={styles.actions}>
                      <button
                        type="button"
                        className={styles.actionBtn}
                        onClick={() => { setApiError(null); setDialog({ kind: 'confirm-rotate', id: t.id, name: t.name }) }}
                      >
                        <RefreshCw size={13} />
                        Rotate
                      </button>
                      <button
                        type="button"
                        className={[styles.actionBtn, styles.dangerBtn].join(' ')}
                        onClick={() => { setApiError(null); setDialog({ kind: 'confirm-revoke', id: t.id, name: t.name }) }}
                      >
                        <Trash2 size={13} />
                        Revoke
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* ── Create dialog ──────────────────────────────────────────── */}
      {dialog?.kind === 'create' && (
        <Modal onClose={closeDialog}>
          <div className={styles.dialogTitle}>New token</div>
          <form onSubmit={handleCreate}>
            <label className={styles.label} htmlFor="token-name">Name</label>
            <input
              id="token-name"
              ref={nameRef}
              className={styles.input}
              placeholder="e.g. My Claude Agent"
              autoFocus
              disabled={busy}
            />
            {apiError && <div className={styles.dialogAlert}><InlineAlert kind="error" message={apiError} /></div>}
            <div className={styles.dialogActions}>
              <button type="submit" className={styles.primaryBtn} disabled={busy}>
                {busy ? <><span className={styles.spinner} />Creating…</> : 'Create'}
              </button>
              <button type="button" className={styles.secondaryBtn} onClick={closeDialog} disabled={busy}>
                Cancel
              </button>
            </div>
          </form>
        </Modal>
      )}

      {/* ── Show plaintext dialog ──────────────────────────────────── */}
      {dialog?.kind === 'show-token' && (
        <Modal onClose={closeDialog}>
          <div className={styles.dialogTitle}>
            {dialog.action === 'created' ? 'Token created' : 'Token rotated'}
          </div>
          <div className={styles.warningBanner}>
            <AlertTriangle size={15} />
            <span>Copy this token now — it won't be shown again.</span>
          </div>
          <p className={styles.dialogLabel}>Token</p>
          <div className={styles.tokenBlock}>
            <CopyButton text={dialog.plaintext} className={styles.tokenCopyBtn} />
            <code className={styles.tokenValue}>{dialog.plaintext}</code>
          </div>
          <p className={styles.dialogLabel}>OTLP header value</p>
          <div className={styles.tokenBlock}>
            <CopyButton text={`Authorization=Bearer ${dialog.plaintext}`} className={styles.tokenCopyBtn} />
            <code className={styles.tokenValue}>{`Authorization=Bearer ${dialog.plaintext}`}</code>
          </div>
          <div className={styles.dialogActions}>
            <button type="button" className={styles.primaryBtn} onClick={closeDialog}>Done</button>
          </div>
        </Modal>
      )}

      {/* ── Confirm rotate dialog ──────────────────────────────────── */}
      {dialog?.kind === 'confirm-rotate' && (
        <Modal onClose={closeDialog}>
          <div className={styles.dialogTitle}>Rotate token?</div>
          <p className={styles.dialogBody}>
            <strong>{dialog.name}</strong> will stop working immediately. A new token will be
            generated and shown once.
          </p>
          {apiError && <div className={styles.dialogAlert}><InlineAlert kind="error" message={apiError} /></div>}
          <div className={styles.dialogActions}>
            <button type="button" className={styles.primaryBtn} onClick={handleRotateConfirm} disabled={busy}>
              {busy ? <><span className={styles.spinner} />Rotating…</> : 'Rotate'}
            </button>
            <button type="button" className={styles.secondaryBtn} onClick={closeDialog} disabled={busy}>
              Cancel
            </button>
          </div>
        </Modal>
      )}

      {/* ── Confirm revoke dialog ──────────────────────────────────── */}
      {dialog?.kind === 'confirm-revoke' && (
        <Modal onClose={closeDialog}>
          <div className={styles.dialogTitle}>Revoke token?</div>
          <p className={styles.dialogBody}>
            <strong>{dialog.name}</strong> will be permanently deleted. Any agent using this token
            will lose access immediately.
          </p>
          {apiError && <div className={styles.dialogAlert}><InlineAlert kind="error" message={apiError} /></div>}
          <div className={styles.dialogActions}>
            <button
              type="button"
              className={[styles.primaryBtn, styles.dangerPrimaryBtn].join(' ')}
              onClick={handleRevokeConfirm}
              disabled={busy}
            >
              {busy ? <><span className={styles.spinner} />Revoking…</> : 'Revoke'}
            </button>
            <button type="button" className={styles.secondaryBtn} onClick={closeDialog} disabled={busy}>
              Cancel
            </button>
          </div>
        </Modal>
      )}
    </div>
  )
}
