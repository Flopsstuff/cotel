import styles from './KpiCard.module.css'

interface KpiCardProps {
  label: string
  value: string
  delta?: string
  deltaDir?: 'up' | 'down' | 'neutral'
}

export function KpiCard({ label, value, delta, deltaDir = 'neutral' }: KpiCardProps) {
  const deltaClass =
    deltaDir === 'up' ? styles.deltaUp :
    deltaDir === 'down' ? styles.deltaDown :
    styles.deltaNeutral

  return (
    <div className={styles.card}>
      <span className={styles.label}>{label}</span>
      <span className={styles.value}>{value}</span>
      {delta && <span className={`${styles.delta} ${deltaClass}`}>{delta}</span>}
    </div>
  )
}
