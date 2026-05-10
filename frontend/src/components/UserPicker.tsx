import { useSearchParams } from 'react-router-dom'
import { useUsers } from '../api'
import styles from './UserPicker.module.css'

export function UserPicker() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { data } = useUsers()
  const userId = searchParams.get('user_id') ?? ''

  function handleChange(e: React.ChangeEvent<HTMLSelectElement>) {
    const val = e.target.value
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (val) {
        next.set('user_id', val)
      } else {
        next.delete('user_id')
      }
      return next
    })
  }

  return (
    <div className={styles.wrap}>
      <label className={styles.label} htmlFor="user-picker">
        User
      </label>
      <select id="user-picker" className={styles.select} value={userId} onChange={handleChange}>
        <option value="">All users</option>
        {data?.users.map((u) => (
          <option key={u.id} value={u.id}>
            {u.name}
          </option>
        ))}
      </select>
    </div>
  )
}
