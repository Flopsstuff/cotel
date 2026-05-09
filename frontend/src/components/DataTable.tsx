import { ReactNode, useState, useMemo } from 'react'
import styles from './DataTable.module.css'

type SortState<T> = { key: keyof T; dir: 'asc' | 'desc' } | null

export interface Column<T> {
  key: keyof T
  label: string
  render?: (value: unknown, row: T) => ReactNode
  sortable?: boolean
}

interface DataTableProps<T extends object> {
  columns: Column<T>[]
  rows: T[]
  onRowClick?: (row: T) => void
}

export function DataTable<T extends object>({ columns, rows, onRowClick }: DataTableProps<T>) {
  const [sort, setSort] = useState<SortState<T>>(null)

  const sorted = useMemo(() => {
    if (!sort) return rows
    const { key, dir } = sort
    return [...rows].sort((a, b) => {
      const av = a[key] as unknown
      const bv = b[key] as unknown
      const sa = typeof av === 'number' ? av : String(av ?? '')
      const sb = typeof bv === 'number' ? bv : String(bv ?? '')
      const cmp = sa < sb ? -1 : sa > sb ? 1 : 0
      return dir === 'asc' ? cmp : -cmp
    })
  }, [rows, sort])

  function handleHeaderClick(col: Column<T>) {
    if (!col.sortable) return
    setSort((prev) =>
      prev?.key === col.key
        ? { key: col.key, dir: prev.dir === 'asc' ? 'desc' : 'asc' }
        : { key: col.key, dir: 'asc' },
    )
  }

  return (
    <div className={styles.wrapper}>
      <table className={styles.table}>
        <thead>
          <tr>
            {columns.map((col, colIdx) => {
              const isActive = sort?.key === col.key
              const thClass = [
                styles.th,
                col.sortable ? styles.thSortable : '',
                isActive ? styles.thActive : '',
              ]
                .filter(Boolean)
                .join(' ')
              return (
                <th
                  key={colIdx}
                  scope="col"
                  aria-sort={isActive ? (sort!.dir === 'asc' ? 'ascending' : 'descending') : col.sortable ? 'none' : undefined}
                  className={thClass}
                  onClick={() => handleHeaderClick(col)}
                >
                  {col.label}
                  {col.sortable && (
                    <span className={styles.sortIcon}>
                      {isActive ? (sort!.dir === 'asc' ? '↑' : '↓') : '↕'}
                    </span>
                  )}
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row, i) => (
            <tr
              key={i}
              className={[styles.tr, onRowClick ? styles.trClickable : ''].filter(Boolean).join(' ')}
              onClick={() => onRowClick?.(row)}
            >
              {columns.map((col, colIdx) => (
                <td key={colIdx} className={styles.td}>
                  {col.render
                    ? col.render(row[col.key] as unknown, row)
                    : String((row[col.key] as unknown) ?? '')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
