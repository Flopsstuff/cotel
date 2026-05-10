import styles from './TabBar.module.css'

export interface TabItem {
  id: string
  label: string
}

interface TabBarProps {
  tabs: TabItem[]
  activeTab: string
  onChange: (id: string) => void
}

export function TabBar({ tabs, activeTab, onChange }: TabBarProps) {
  return (
    <div className={styles.tabBar} role="tablist">
      {tabs.map(tab => (
        <button
          key={tab.id}
          role="tab"
          type="button"
          aria-selected={tab.id === activeTab}
          className={[styles.tab, tab.id === activeTab ? styles.active : ''].filter(Boolean).join(' ')}
          onClick={() => onChange(tab.id)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  )
}
