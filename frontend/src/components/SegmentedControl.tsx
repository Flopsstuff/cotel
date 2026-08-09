import styles from './SegmentedControl.module.css'

export interface SegmentOption {
  value: string
  label: string
}

interface SegmentedControlProps {
  options: SegmentOption[]
  value: string
  onChange: (value: string) => void
  ariaLabel?: string
}

export function SegmentedControl({ options, value, onChange, ariaLabel }: SegmentedControlProps) {
  return (
    <div className={styles.control} role="group" aria-label={ariaLabel}>
      {options.map(opt => (
        <button
          key={opt.value}
          type="button"
          aria-pressed={opt.value === value}
          className={[styles.segment, opt.value === value ? styles.active : ''].filter(Boolean).join(' ')}
          onClick={() => onChange(opt.value)}
        >
          {opt.label}
        </button>
      ))}
    </div>
  )
}
