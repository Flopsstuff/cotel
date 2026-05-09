import { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { Activity, BarChart2, Cpu, DollarSign, Layers, Wrench } from 'lucide-react'

// ---- Layout ----

export function Layout({ children }: { children: ReactNode }) {
  return (
    <div style={{ display: 'flex', minHeight: '100vh', fontFamily: 'system-ui, sans-serif' }}>
      <Sidebar />
      <main style={{ flex: 1, padding: '1.5rem', overflowY: 'auto' }}>{children}</main>
    </div>
  )
}

// ---- Sidebar ----

const navItems = [
  { to: '/', label: 'Overview', icon: <Activity size={16} />, end: true },
  { to: '/sessions', label: 'Sessions', icon: <Layers size={16} /> },
  { to: '/costs', label: 'Costs', icon: <DollarSign size={16} /> },
  { to: '/tools', label: 'Tools', icon: <Wrench size={16} /> },
  { to: '/models', label: 'Models', icon: <Cpu size={16} /> },
  { to: '/charts', label: 'Charts', icon: <BarChart2 size={16} /> },
]

export function Sidebar() {
  return (
    <nav
      style={{
        width: 200,
        background: '#1a1a2e',
        color: '#e0e0e0',
        padding: '1rem 0',
        flexShrink: 0,
      }}
    >
      <div style={{ padding: '0 1rem 1.5rem', fontWeight: 700, fontSize: '1.1rem', color: '#fff' }}>
        cotel
      </div>
      {navItems.map(({ to, label, icon, end }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          style={({ isActive }) => ({
            display: 'flex',
            alignItems: 'center',
            gap: '0.5rem',
            padding: '0.6rem 1rem',
            color: isActive ? '#fff' : '#a0a0b0',
            background: isActive ? '#16213e' : 'transparent',
            textDecoration: 'none',
            fontSize: '0.9rem',
          })}
        >
          {icon}
          {label}
        </NavLink>
      ))}
    </nav>
  )
}

// ---- Card ----

export function Card({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <div
      style={{
        background: '#fff',
        border: '1px solid #e5e7eb',
        borderRadius: 8,
        padding: '1rem',
        marginBottom: '1rem',
      }}
    >
      {title && <h3 style={{ margin: '0 0 0.75rem', fontSize: '0.95rem', color: '#374151' }}>{title}</h3>}
      {children}
    </div>
  )
}

// ---- DataTable ----

export function DataTable<T extends object>({
  columns,
  rows,
}: {
  columns: { key: keyof T; label: string; render?: (v: unknown, row: T) => ReactNode }[]
  rows: T[]
}) {
  return (
    <div style={{ overflowX: 'auto' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem' }}>
        <thead>
          <tr style={{ borderBottom: '2px solid #e5e7eb' }}>
            {columns.map((c) => (
              <th
                key={String(c.key)}
                style={{ textAlign: 'left', padding: '0.5rem 0.75rem', color: '#6b7280', fontWeight: 600 }}
              >
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} style={{ borderBottom: '1px solid #f3f4f6' }}>
              {columns.map((c) => (
                <td key={String(c.key)} style={{ padding: '0.5rem 0.75rem', color: '#111827' }}>
                  {c.render ? c.render(row[c.key] as unknown, row) : String((row[c.key] as unknown) ?? '')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ---- ErrorState ----

export function ErrorState({ message }: { message?: string }) {
  return (
    <div
      style={{
        padding: '2rem',
        textAlign: 'center',
        color: '#dc2626',
        background: '#fef2f2',
        borderRadius: 8,
        border: '1px solid #fecaca',
      }}
    >
      {message ?? 'An error occurred. Please try again.'}
    </div>
  )
}

// ---- LoadingState ----

export function LoadingState() {
  return (
    <div style={{ padding: '2rem', textAlign: 'center', color: '#9ca3af' }}>Loading…</div>
  )
}
