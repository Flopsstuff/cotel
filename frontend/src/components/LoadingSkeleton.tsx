import styles from './LoadingSkeleton.module.css'

export function LoadingSkeleton({ rows = 5, height = 20 }: { rows?: number; height?: number }) {
  return (
    <div className={styles.container}>
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className={styles.line} style={{ height }} />
      ))}
    </div>
  )
}

export function KpiSkeleton() {
  return (
    <div className={styles.kpiGrid}>
      {Array.from({ length: 4 }).map((_, i) => (
        <div key={i} className={styles.kpiCard} />
      ))}
    </div>
  )
}

export function ChartSkeleton() {
  return <div className={styles.chartBlock} />
}
