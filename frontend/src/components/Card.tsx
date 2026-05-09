import { ReactNode } from 'react'
import styles from './Card.module.css'

interface CardProps {
  title?: string
  action?: ReactNode
  children: ReactNode
  noPad?: boolean
}

export function Card({ title, action, children, noPad }: CardProps) {
  return (
    <div className={styles.card}>
      {(title || action) && (
        <div className={styles.header}>
          {title && <span className={styles.title}>{title}</span>}
          {action}
        </div>
      )}
      <div className={noPad ? styles.bodyNoPad : styles.body}>{children}</div>
    </div>
  )
}
