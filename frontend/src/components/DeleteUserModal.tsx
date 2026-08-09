import { useState, useEffect, useRef } from 'react'
import { deleteUser } from '../api'
import type { User, DeleteMode } from '../api'
import styles from './DeleteUserModal.module.css'

interface DeleteUserModalProps {
  user: User
  onClose: () => void
  onDeleted: (mode: DeleteMode) => void
}

export function DeleteUserModal({ user, onClose, onDeleted }: DeleteUserModalProps) {
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
            Revokes access and permanently removes {user.name} and all their telemetry. This cannot be undone.
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
