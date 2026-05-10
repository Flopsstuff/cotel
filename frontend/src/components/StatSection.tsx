import { useState, Children, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ChevronDown, ChevronUp } from 'lucide-react'
import styles from './StatSection.module.css'

interface StatSectionProps {
  title: string
  children: ReactNode
  viewAllHref?: string
  defaultExpanded?: boolean
}

export function StatSection({ title, children, viewAllHref, defaultExpanded = true }: StatSectionProps) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const childArray = Children.toArray(children)

  return (
    <div className={styles.card}>
      <div className={styles.header}>
        <button
          className={styles.titleBtn}
          onClick={() => setExpanded((e) => !e)}
          aria-expanded={expanded}
        >
          {expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          <span className={styles.title}>{title}</span>
        </button>
        {viewAllHref && (
          <Link to={viewAllHref} className={styles.viewAll}>
            View all →
          </Link>
        )}
      </div>
      <div className={styles.content}>
        {expanded ? childArray : childArray[0]}
      </div>
    </div>
  )
}
