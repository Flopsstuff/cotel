import styles from './StatusBadge.module.css'

type Variant = 'success' | 'warning' | 'danger' | 'neutral' | 'accent'

interface StatusBadgeProps {
  label: string
  variant: Variant
}

export function StatusBadge({ label, variant }: StatusBadgeProps) {
  return (
    <span className={`${styles.badge} ${styles[variant]}`}>{label}</span>
  )
}

export function sessionStatusBadge(status: string) {
  const variant: Variant = status === 'ok' ? 'success' : status === 'error' ? 'danger' : 'neutral'
  return <StatusBadge label={status} variant={variant} />
}

export function failRateBadge(rate: number) {
  const variant: Variant = rate < 5 ? 'success' : rate < 15 ? 'warning' : 'danger'
  return <StatusBadge label={`${rate.toFixed(1)}%`} variant={variant} />
}
