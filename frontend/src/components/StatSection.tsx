import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ChevronDown, ChevronUp } from 'lucide-react'
import styles from './StatSection.module.css'

interface SectionLink {
  label: string
  href: string
}

interface StatSectionProps {
  title: string
  children: ReactNode
  viewAllHref?: string
  links?: SectionLink[]
  defaultExpanded?: boolean
}

export function StatSection({
  title,
  children,
  viewAllHref,
  links,
  defaultExpanded = true,
}: StatSectionProps) {
  const [expanded, setExpanded] = useState(defaultExpanded)
  const headerLinks = links ?? (viewAllHref ? [{ label: 'View all', href: viewAllHref }] : [])

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
        {headerLinks.length > 0 && (
          <div className={styles.links}>
            {headerLinks.map((l) => (
              <Link key={l.href} to={l.href} className={styles.viewAll}>
                {l.label} →
              </Link>
            ))}
          </div>
        )}
      </div>
      <div className={styles.content}>
        {expanded && children}
      </div>
    </div>
  )
}
