import { afterEach, describe, expect, it, vi } from 'vitest'
import { formatRelativeTimeShort } from './relativeTime'

// The Users page's buckets, unchanged by the move: the risk center's queue
// reads its "last change" in the same words the Users list reads "last
// online" in.
const t = (k: string, o?: Record<string, unknown>) => `${k}${o && 'count' in o ? `:${String(o.count)}` : ''}`
const R = 'admin:users.relative_time.'
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR

afterEach(() => { vi.useRealTimers() })

describe('formatRelativeTimeShort', () => {
  it.each([
    [0, `${R}just_now`],
    [59 * SEC, `${R}just_now`],
    [60 * SEC, `${R}minutes_ago:1`],
    [59 * MIN + 59 * SEC, `${R}minutes_ago:59`],
    [HOUR, `${R}hours_ago:1`],
    [23 * HOUR + 59 * MIN, `${R}hours_ago:23`],
    [DAY, `${R}days_ago:1`],
    [29 * DAY + 23 * HOUR, `${R}days_ago:29`],
  ])('%d ms ago reads %s', (diff, want) => {
    expect(formatRelativeTimeShort(diff, t)).toBe(want)
  })

  // A clock a little ahead of the server's must not read "in the future".
  it('reads a negative difference as just now', () => {
    expect(formatRelativeTimeShort(-5 * MIN, t)).toBe(`${R}just_now`)
  })

  // Past a month a relative label stops helping; the date says it plainly.
  it('reads thirty days and more as the local date', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 8, 28, 12, 0, 0))
    expect(formatRelativeTimeShort(30 * DAY, t)).toBe('2026-08-29')
    expect(formatRelativeTimeShort(400 * DAY, t)).toBe('2025-08-24')
  })
})
