import { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Search, ChevronLeft, ChevronRight } from 'lucide-react'
import { useTools, useBashCommands } from '../api'
import { Card, DataTable, EmptyState, ErrorState, LoadingSkeleton, SegmentedControl, failRateBadge } from '../components'
import type { ToolItem, BashCommandItem } from '../api'
import type { Column, SortState } from '../components'
import { RANGE_OPTIONS, useRangeCookie } from '../lib/range'
import styles from './Tools.module.css'

const RANGE_COOKIE = 'cotel_tools_range'
// Unpaginated by default, so the page looks unchanged until pagination is
// turned on; the server contract already supports it.
const PAGE_SIZE = 0

type ToolSortKey = 'name' | 'calls' | 'avg_duration_ms' | 'fail_count' | 'fail_rate'
type BashSortKey = 'command' | 'calls' | 'avg_duration_ms' | 'fail_count' | 'fail_rate'

function truncateCommand(cmd: string, max = 80): string {
  return cmd.length > max ? cmd.slice(0, max - 1) + '…' : cmd
}

function formatDay(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

function Pagination({ page, total, onChange }: { page: number; total: number; onChange: (p: number) => void }) {
  if (PAGE_SIZE <= 0 || total <= PAGE_SIZE) return null
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  return (
    <div className={styles.pagination}>
      <button className={styles.pageBtn} disabled={page <= 1} onClick={() => onChange(Math.max(1, page - 1))}>
        <ChevronLeft size={14} />
      </button>
      <span className={styles.pageInfo}>{page} / {totalPages}</span>
      <button
        className={styles.pageBtn}
        disabled={page >= totalPages}
        onClick={() => onChange(Math.min(totalPages, page + 1))}
      >
        <ChevronRight size={14} />
      </button>
    </div>
  )
}

export default function Tools() {
  const [searchParams, setSearchParams] = useSearchParams()
  const userId = searchParams.get('user_id') ?? undefined

  const [range, setRange] = useRangeCookie(RANGE_COOKIE)
  const [sort, setSort] = useState<ToolSortKey>('calls')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [q, setQ] = useState('')

  const [bashSort, setBashSort] = useState<BashSortKey>('calls')
  const [bashOrder, setBashOrder] = useState<'asc' | 'desc'>('desc')
  const [bashPage, setBashPage] = useState(1)

  // Debounce the search box into the server-side q parameter.
  useEffect(() => {
    const t = setTimeout(() => {
      setQ(search.trim())
      setPage(1)
    }, 300)
    return () => clearTimeout(t)
  }, [search])

  const { data, error, isLoading } = useTools({
    range,
    q: q || undefined,
    sort,
    order,
    page,
    limit: PAGE_SIZE,
    user_id: userId,
  })

  const { data: bashData, isLoading: bashLoading } = useBashCommands({
    range,
    sort: bashSort,
    order: bashOrder,
    page: bashPage,
    limit: PAGE_SIZE,
    user_id: userId,
  })

  const total = data?.total ?? 0
  const bashTotal = bashData?.total ?? 0

  function clearUserFilter() {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete('user_id')
      return next
    })
  }

  const changeRange = (v: string) => {
    setRange(v)
    setPage(1)
    setBashPage(1)
  }

  const handleSortChange = (next: NonNullable<SortState<ToolItem>>) => {
    setSort(next.key as ToolSortKey)
    setOrder(next.dir)
    setPage(1)
  }

  const handleBashSortChange = (next: NonNullable<SortState<BashCommandItem>>) => {
    setBashSort(next.key as BashSortKey)
    setBashOrder(next.dir)
    setBashPage(1)
  }

  const toolColumns: Column<ToolItem>[] = [
    { key: 'name', label: 'Tool', sortable: true },
    { key: 'calls', label: 'Calls', sortable: true },
    { key: 'avg_duration_ms', label: 'Avg Duration', sortable: true, render: (v) => `${Number(v).toFixed(0)}ms` },
    { key: 'fail_count', label: 'Errors', sortable: true },
    { key: 'fail_rate', label: 'Error Rate', sortable: true, render: (v) => failRateBadge(Number(v)) },
  ]

  const bashColumns: Column<BashCommandItem>[] = [
    {
      key: 'command',
      label: 'Command',
      sortable: true,
      render: (v) => (
        <span className={styles.commandCell} title={String(v)}>
          {truncateCommand(String(v))}
        </span>
      ),
    },
    { key: 'calls', label: 'Calls', sortable: true },
    { key: 'avg_duration_ms', label: 'Avg Duration', sortable: true, render: (v) => `${Number(v).toFixed(0)}ms` },
    { key: 'fail_count', label: 'Errors', sortable: true },
    { key: 'fail_rate', label: 'Error Rate', sortable: true, render: (v) => failRateBadge(Number(v)) },
  ]

  return (
    <div>
      <div className={styles.toolbar}>
        <h1 className={styles.title}>Tools</h1>
        {userId && (
          <button className={styles.userChip} onClick={clearUserFilter}>
            Filtered by: <strong>{userId}</strong> ×
          </button>
        )}

        <div className={styles.searchWrap}>
          <Search size={14} className={styles.searchIcon} />
          <input
            className={styles.searchInput}
            placeholder="Search tools…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          {search && <button className={styles.searchClear} onClick={() => setSearch('')}>×</button>}
        </div>

        <SegmentedControl
          options={RANGE_OPTIONS}
          value={range}
          onChange={changeRange}
          ariaLabel="Range for tool statistics"
        />
      </div>

      {isLoading && !data ? (
        <LoadingSkeleton rows={8} height={40} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : (
        <Card title="All Tools">
          {total === 0 ? (
            q ? (
              <div className={styles.noResults}>No tools match "{q}"</div>
            ) : (
              <EmptyState
                heading="No tool data"
                subtext="Tool usage will appear here once sessions are recorded in the selected range."
              />
            )
          ) : (
            <>
              <DataTable<ToolItem>
                columns={toolColumns}
                rows={data!.items}
                sort={{ key: sort as keyof ToolItem, dir: order }}
                onSortChange={handleSortChange}
              />
              {data?.duration_stats_since && (
                <p className={styles.statsNote}>
                  Duration and error figures start from {formatDay(data.duration_stats_since)} — earlier
                  days were rolled up before those totals were recorded, so they count toward Calls only.
                </p>
              )}
              <Pagination page={page} total={total} onChange={setPage} />
            </>
          )}
        </Card>
      )}

      {bashLoading && !bashData ? (
        <Card title="Bash Command Breakdown">
          <LoadingSkeleton rows={4} height={36} />
        </Card>
      ) : bashTotal > 0 ? (
        <Card title="Bash Command Breakdown">
          <p className={styles.sectionNote}>
            Each distinct command passed to the Bash tool. Commands are extracted from the{' '}
            <code>command</code> span attribute.
          </p>
          <DataTable<BashCommandItem>
            columns={bashColumns}
            rows={bashData!.items}
            sort={{ key: bashSort as keyof BashCommandItem, dir: bashOrder }}
            onSortChange={handleBashSortChange}
          />
          {bashData?.covered_since && (
            <p className={styles.statsNote}>
              This breakdown starts from {formatDay(bashData.covered_since)} — older activity is kept
              only as daily totals, which carry no command detail.
            </p>
          )}
          <Pagination page={bashPage} total={bashTotal} onChange={setBashPage} />
        </Card>
      ) : null}
    </div>
  )
}
