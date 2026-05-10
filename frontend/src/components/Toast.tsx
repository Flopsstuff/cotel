import { useEffect } from 'react'
import { CheckCircle, XCircle, X } from 'lucide-react'
import styles from './Toast.module.css'

interface ToastProps {
  message: string
  kind: 'success' | 'error'
  onDismiss: () => void
  duration?: number
}

export function Toast({ message, kind, onDismiss, duration = 4000 }: ToastProps) {
  useEffect(() => {
    const t = setTimeout(onDismiss, duration)
    return () => clearTimeout(t)
  }, [onDismiss, duration])

  const Icon = kind === 'success' ? CheckCircle : XCircle

  return (
    <div className={[styles.toast, styles[kind]].join(' ')} role="status" aria-live="polite">
      <Icon size={16} className={styles.icon} />
      <span className={styles.message}>{message}</span>
      <button type="button" className={styles.dismiss} onClick={onDismiss} aria-label="Dismiss">
        <X size={14} />
      </button>
    </div>
  )
}
