import { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { Activity, Cpu, DollarSign, Layers, Wrench } from 'lucide-react'
import styles from './Layout.module.css'

// ---- Layout + Sidebar ----

const navItems = [
  { to: '/', label: 'Overview', icon: <Activity size={16} />, end: true },
  { to: '/sessions', label: 'Sessions', icon: <Layers size={16} /> },
  { to: '/costs', label: 'Costs', icon: <DollarSign size={16} /> },
  { to: '/tools', label: 'Tools', icon: <Wrench size={16} /> },
  { to: '/models', label: 'Models', icon: <Cpu size={16} /> },
]

export function Sidebar() {
  return (
    <nav className={styles.sidebar}>
      <div className={styles.brand}>cotel</div>
      {navItems.map(({ to, label, icon, end }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          className={({ isActive }) =>
            [styles.navLink, isActive ? styles.navLinkActive : ''].filter(Boolean).join(' ')
          }
        >
          {icon}
          {label}
        </NavLink>
      ))}
    </nav>
  )
}

export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className={styles.shell}>
      <Sidebar />
      <main className={styles.main}>{children}</main>
    </div>
  )
}

// ---- Card ----

export function Card({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <div className={styles.card}>
      {title && <h3 className={styles.cardTitle}>{title}</h3>}
      {children}
    </div>
  )
}

// ---- Re-exports from individual component files ----

export { KpiCard } from './KpiCard'
export { StatusBadge, sessionStatusBadge, failRateBadge } from './StatusBadge'
export { DataTable } from './DataTable'
export type { Column } from './DataTable'
export { LoadingSkeleton, KpiSkeleton, ChartSkeleton } from './LoadingSkeleton'
export { EmptyState } from './EmptyState'
export { ErrorState } from './ErrorState'
export { RefreshIndicator } from './RefreshIndicator'
export { DateRangePicker } from './DateRangePicker'

// Legacy alias kept for pages that haven't been updated yet
export { LoadingSkeleton as LoadingState } from './LoadingSkeleton'
