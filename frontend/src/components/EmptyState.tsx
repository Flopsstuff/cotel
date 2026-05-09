import styles from './EmptyState.module.css'

interface EmptyStateProps {
  heading: string
  subtext?: string
}

export function EmptyState({ heading, subtext }: EmptyStateProps) {
  return (
    <div className={styles.container}>
      <span className={styles.icon} aria-hidden>○</span>
      <p className={styles.heading}>{heading}</p>
      {subtext && <p className={styles.subtext}>{subtext}</p>}
    </div>
  )
}
