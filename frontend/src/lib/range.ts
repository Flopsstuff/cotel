import { useCallback, useState } from 'react'
import { getCookie, setCookie } from './cookie'

export type RangeKey = 'all' | 'year' | 'month' | 'week' | 'day'

export const RANGE_OPTIONS: { value: RangeKey; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'year', label: 'Year' },
  { value: 'month', label: 'Month' },
  { value: 'week', label: 'Week' },
  { value: 'day', label: 'Day' },
]

// Short suffix shown on the range-governed column headers so it stays obvious
// which figures the switcher scopes.
export const RANGE_SUFFIX: Record<RangeKey, string> = {
  all: '',
  year: '1y',
  month: '30d',
  week: '7d',
  day: '24h',
}

export function isRangeKey(v: string | null): v is RangeKey {
  return v === 'all' || v === 'year' || v === 'month' || v === 'week' || v === 'day'
}

const COOKIE_MAX_AGE = 60 * 60 * 24 * 365 // one year

// useRangeCookie persists the selected range under a page-specific cookie key.
// Each list page passes its own key so switching range on one page does not
// silently move another.
export function useRangeCookie(cookieKey: string, fallback: RangeKey = 'month') {
  const [range, setRangeState] = useState<RangeKey>(() => {
    const c = getCookie(cookieKey)
    return isRangeKey(c) ? c : fallback
  })

  const setRange = useCallback(
    (v: string) => {
      if (!isRangeKey(v)) return
      setRangeState(v)
      setCookie(cookieKey, v, COOKIE_MAX_AGE)
    },
    [cookieKey],
  )

  return [range, setRange] as const
}
