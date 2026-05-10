import styles from './ManifestCard.module.css'

interface ManifestTable {
  row_count: number
  columns: string[]
}

export interface ZipManifest {
  format_version: number
  cotel_version: string
  export_at: string
  period: string
  period_start: string
  period_end: string
  tables: {
    spans?: ManifestTable
    daily_usage?: ManifestTable
  }
}

interface ManifestCardProps {
  manifest: ZipManifest
  filename: string
}

export function ManifestCard({ manifest, filename }: ManifestCardProps) {
  const spanCount = manifest.tables.spans?.row_count ?? 0
  const dailyCount = manifest.tables.daily_usage?.row_count ?? 0
  const periodStart = manifest.period_start.slice(0, 10)
  const periodEnd = manifest.period_end.slice(0, 10)
  const exportDate = new Date(manifest.export_at)
  const exportDateStr = isNaN(exportDate.getTime()) ? '—' : exportDate.toLocaleString()

  return (
    <div className={styles.card}>
      <div className={styles.header}>
        <span className={styles.badge}>Ready to import</span>
        <span className={styles.filename}>{filename}</span>
      </div>
      <dl className={styles.grid}>
        <div className={styles.item}>
          <dt className={styles.key}>Period</dt>
          <dd className={styles.val}>{manifest.period}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.key}>Date range</dt>
          <dd className={styles.val}>{periodStart} → {periodEnd}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.key}>Spans</dt>
          <dd className={styles.val}>{spanCount.toLocaleString()}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.key}>Daily rows</dt>
          <dd className={styles.val}>{dailyCount.toLocaleString()}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.key}>Format version</dt>
          <dd className={styles.val}>v{manifest.format_version}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.key}>Exported at</dt>
          <dd className={styles.val}>{exportDateStr}</dd>
        </div>
      </dl>
    </div>
  )
}
