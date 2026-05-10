import { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { Activity, DollarSign, Layers, Cpu, Wrench, CalendarDays, Settings, Users } from 'lucide-react'
import { LogoMark } from './Logo'
import styles from './Layout.module.css'

const navItems = [
  { to: '/', label: 'Overview', icon: <Activity size={15} />, end: true },
  { to: '/sessions', label: 'Sessions', icon: <Layers size={15} /> },
  { to: '/users', label: 'Users', icon: <Users size={15} /> },
  { to: '/history', label: 'History', icon: <CalendarDays size={15} /> },
  { to: '/costs', label: 'Costs', icon: <DollarSign size={15} /> },
  { to: '/tools', label: 'Tools', icon: <Wrench size={15} /> },
  { to: '/models', label: 'Models', icon: <Cpu size={15} /> },
  { to: '/setup', label: 'Setup', icon: <Settings size={15} /> },
]

export function Layout({ children }: { children: ReactNode }) {
  return (
    <div className={styles.shell}>
      <nav className={styles.sidebar}>
        <a
          href="https://github.com/Flopsstuff/cotel"
          target="_blank"
          rel="noopener noreferrer"
          className={styles.brand}
        >
          <LogoMark size={20} />
          <span className={styles.brandName}>cotel</span>
        </a>
        {navItems.map(({ to, label, icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              isActive ? `${styles.navLink} ${styles.navLinkActive}` : styles.navLink
            }
          >
            {icon}
            <span className={styles.navLabel}>{label}</span>
          </NavLink>
        ))}
      </nav>
      <main className={styles.main}>{children}</main>
    </div>
  )
}
