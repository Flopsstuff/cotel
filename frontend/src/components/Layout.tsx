import { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { Activity, DollarSign, Layers, Cpu, Wrench, CalendarDays, Settings, User } from 'lucide-react'
import { useUsers } from '../api'
import { useUserContext } from '../context/UserContext'
import styles from './Layout.module.css'

const navItems = [
  { to: '/', label: 'Overview', icon: <Activity size={15} />, end: true },
  { to: '/sessions', label: 'Sessions', icon: <Layers size={15} /> },
  { to: '/history', label: 'History', icon: <CalendarDays size={15} /> },
  { to: '/costs', label: 'Costs', icon: <DollarSign size={15} /> },
  { to: '/tools', label: 'Tools', icon: <Wrench size={15} /> },
  { to: '/models', label: 'Models', icon: <Cpu size={15} /> },
  { to: '/setup', label: 'Setup', icon: <Settings size={15} /> },
]

export function Layout({ children }: { children: ReactNode }) {
  const { data: usersData } = useUsers()
  const { userId, setUserId } = useUserContext()
  const users = usersData?.items ?? []

  return (
    <div className={styles.shell}>
      <nav className={styles.sidebar}>
        <div className={styles.brand}>cotel</div>
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
        <div className={styles.userSection}>
          <div className={styles.userLabel}>
            <User size={13} />
            <span className={styles.navLabel}>User</span>
          </div>
          <select
            className={styles.userSelect}
            value={userId}
            onChange={(e) => setUserId(e.target.value)}
            title="Filter by user"
          >
            <option value="">All users</option>
            {users.map((u) => (
              <option key={u} value={u}>{u}</option>
            ))}
          </select>
        </div>
      </nav>
      <main className={styles.main}>{children}</main>
    </div>
  )
}
