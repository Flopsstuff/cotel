import styles from './RefreshIndicator.module.css'

interface RefreshIndicatorProps {
  isValidating?: boolean
  paused?: boolean
  onToggle?: () => void
}

export function RefreshIndicator({ isValidating, paused, onToggle }: RefreshIndicatorProps) {
  const dotClass = [
    styles.dot,
    paused ? styles.dotPaused : '',
    !paused && isValidating ? styles.dotPulsing : '',
  ].filter(Boolean).join(' ')

  return (
    <div className={styles.container}>
      <span className={dotClass} title={paused ? 'Auto-refresh paused' : isValidating ? 'Refreshing…' : 'Live'} />
      <span className={styles.label}>{paused ? 'Paused' : isValidating ? 'Refreshing…' : 'Live'}</span>
      {onToggle && (
        <button className={styles.toggleBtn} onClick={onToggle}>
          {paused ? 'Resume' : 'Pause'}
        </button>
      )}
    </div>
  )
}
