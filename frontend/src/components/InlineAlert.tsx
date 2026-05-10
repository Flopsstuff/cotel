import { Info, CheckCircle, AlertTriangle, XCircle, type LucideIcon } from 'lucide-react'
import styles from './InlineAlert.module.css'

type AlertKind = 'info' | 'success' | 'warning' | 'error'

const ICONS: Record<AlertKind, LucideIcon> = {
  info: Info,
  success: CheckCircle,
  warning: AlertTriangle,
  error: XCircle,
}

interface InlineAlertProps {
  kind: AlertKind
  message: string
}

export function InlineAlert({ kind, message }: InlineAlertProps) {
  const Icon = ICONS[kind]
  return (
    <div className={[styles.alert, styles[kind]].join(' ')} role="alert">
      <Icon size={14} className={styles.icon} />
      <span className={styles.message}>{message}</span>
    </div>
  )
}
