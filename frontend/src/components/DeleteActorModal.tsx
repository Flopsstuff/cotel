import { useState, useEffect, useRef } from 'react'
import { deleteActor } from '../api'
import type { Actor, DeleteMode } from '../api'
import styles from './DeleteActorModal.module.css'

interface DeleteActorModalProps {
  actor: Actor
  onClose: () => void
  onDeleted: (mode: DeleteMode) => void
}

export function DeleteActorModal({ actor, onClose, onDeleted }: DeleteActorModalProps) {
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

  const modeAReady = mode === 'actor_only'
  const modeBReady = mode === 'actor_and_history' && confirmText === actor.name
  const canSubmit = modeAReady || modeBReady

  const handleSubmit = async () => {
    if (!mode || !canSubmit) return
    setLoading(true)
    setError(null)
    try {
      await deleteActor(actor.id, mode)
      onDeleted(mode)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Delete failed')
      setLoading(false)
    }
  }

  const handleModeChange = (next: DeleteMode) => {
    setMode(next)
    if (next !== 'actor_and_history') setConfirmText('')
    setError(null)
  }

  const confirmLabel = mode === 'actor_and_history' ? 'Permanently delete' : 'Delete actor'

  return (
    <div className={styles.modalOverlay} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className={styles.modal} role="dialog" aria-modal="true" aria-labelledby="delete-modal-title">
        <div className={styles.modalHeader}>
          <h2 className={styles.modalTitle} id="delete-modal-title">Delete actor</h2>
          <button className={styles.dismissBtn} onClick={onClose} aria-label="Close">×</button>
        </div>
        <p className={styles.modalPreamble}>
          You are about to delete <strong>{actor.name}</strong>. Choose what to delete:
        </p>

        <div
          ref={firstRadioRef}
          role="radio"
          aria-checked={mode === 'actor_only'}
          tabIndex={0}
          className={`${styles.radioCard} ${mode === 'actor_only' ? styles.radioCardSelectedA : ''}`}
          onClick={() => handleModeChange('actor_only')}
          onKeyDown={(e) => (e.key === ' ' || e.key === 'Enter') && handleModeChange('actor_only')}
        >
          <div className={styles.radioRow}>
            <span className={styles.radioMark}>{mode === 'actor_only' ? '●' : '○'}</span>
            <span className={styles.radioLabel}>Delete actor only</span>
          </div>
          <p className={styles.radioDesc}>
            Revokes {actor.name}'s login access. All session history and telemetry stays attributed to {actor.name}. Reversible by re-creating an actor with the same name.
          </p>
        </div>

        <div
          role="radio"
          aria-checked={mode === 'actor_and_history'}
          tabIndex={0}
          className={`${styles.radioCard} ${mode === 'actor_and_history' ? styles.radioCardSelectedB : ''}`}
          onClick={() => handleModeChange('actor_and_history')}
          onKeyDown={(e) => (e.key === ' ' || e.key === 'Enter') && handleModeChange('actor_and_history')}
        >
          <div className={styles.radioRow}>
            <span className={styles.radioMark}>{mode === 'actor_and_history' ? '●' : '○'}</span>
            <span className={styles.radioLabel}>Delete actor + history</span>
            <span className={styles.permanentBadge}>⚠ Permanent</span>
          </div>
          <p className={styles.radioDesc}>
            Revokes access and permanently removes {actor.name} and all their telemetry. This cannot be undone.
          </p>
          {mode === 'actor_and_history' && (
            <div className={`${styles.confirmInputWrap} ${styles.confirmInputAnimate}`}>
              <label className={styles.confirmLabel}>Type "{actor.name}" to confirm:</label>
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
            className={mode === 'actor_and_history' ? styles.dangerBtn : styles.secondaryBtn}
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
