import styles from './SegmentedControl.module.css'

export interface SegmentOption {
  value: string
  label: string
}

interface SegmentedControlProps {
  options: SegmentOption[]
  value: string
  onChange: (value: string) => void
}

export function SegmentedControl({ options, value, onChange }: SegmentedControlProps) {
  return (
    <div className={styles.control} role="group">
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
