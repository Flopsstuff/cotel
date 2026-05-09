import { useState } from 'react'
import styles from './DateRangePicker.module.css'

interface DateRangePickerProps {
  from: string
  to: string
  onChange: (from: string, to: string) => void
}

export function DateRangePicker({ from, to, onChange }: DateRangePickerProps) {
  const [draft, setDraft] = useState({ from, to })

  return (
    <div className={styles.picker}>
      <label className={styles.label}>
        From
        <input
          type="date"
          className={styles.input}
          value={draft.from}
          max={draft.to}
          onChange={(e) => setDraft((d) => ({ ...d, from: e.target.value }))}
        />
      </label>
      <label className={styles.label}>
        To
        <input
          type="date"
          className={styles.input}
          value={draft.to}
          min={draft.from}
          onChange={(e) => setDraft((d) => ({ ...d, to: e.target.value }))}
        />
      </label>
      <button className={styles.applyBtn} onClick={() => onChange(draft.from, draft.to)}>
        Apply
      </button>
    </div>
  )
}
