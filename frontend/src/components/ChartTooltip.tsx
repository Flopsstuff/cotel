import styles from "./ChartTooltip.module.css"

interface Payload { name: string; value: number; color?: string }

interface ChartTooltipProps {
  active?: boolean
  payload?: Payload[]
  label?: string
  formatter?: (value: number, name: string) => [string, string]
}

export function ChartTooltip({ active, payload, label, formatter }: ChartTooltipProps) {
  if (!active || !payload?.length) return null
  return (
    <div className={styles.tooltip}>
      {label && <p className={styles.label}>{label}</p>}
      {payload.map((p, i) => {
        const [formattedValue, formattedName] = formatter
          ? formatter(p.value, p.name)
          : [String(p.value), p.name]
        return (
          <p key={i} className={styles.row}>
            {p.color && <span className={styles.dot} style={{ background: p.color }} />}
            <span className={styles.name}>{formattedName}:</span>
            <span className={styles.value}>{formattedValue}</span>
          </p>
        )
      })}
    </div>
  )
}
