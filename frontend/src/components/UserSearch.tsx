import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useUsers } from '../api'
import styles from './UserSearch.module.css'

export function UserSearch() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { data } = useUsers()
  const userId = searchParams.get('user_id') ?? ''
  const [query, setQuery] = useState('')

  const users = data?.users ?? []
  const filtered = query
    ? users.filter((u) => u.name.toLowerCase().includes(query.toLowerCase()))
    : users

  function selectUser(name: string) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (name) {
        next.set('user_id', name)
      } else {
        next.delete('user_id')
      }
      return next
    })
  }

  return (
    <div className={styles.wrap}>
      <input
        className={styles.searchInput}
        placeholder="Search users…"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        aria-label="Search users"
      />
      <div className={styles.chips}>
        <button
          className={[styles.chip, !userId ? styles.chipActive : ''].filter(Boolean).join(' ')}
          onClick={() => selectUser('')}
        >
          All users
        </button>
        {filtered.map((u) => {
          const active = userId === u.name
          return (
            <button
              key={u.id}
              className={[styles.chip, active ? styles.chipActive : ''].filter(Boolean).join(' ')}
              onClick={() => selectUser(active ? '' : u.name)}
            >
              {u.name}
              {active && <span className={styles.clear} aria-hidden>×</span>}
            </button>
          )
        })}
      </div>
    </div>
  )
}
