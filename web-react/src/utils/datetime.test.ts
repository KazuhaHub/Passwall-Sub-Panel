import { describe, expect, it } from 'vitest'

import { formatDualDate, formatDualTz, formatMsDualTz, panelDayStr } from './datetime'

describe('datetime helpers', () => {
  it('returns placeholders for empty and invalid timestamps', () => {
    expect(formatDualTz('', 'UTC')).toBe('-')
    expect(formatDualTz('not-a-date', 'UTC')).toBe('-')
    expect(formatDualDate(undefined, 'UTC')).toBe('-')
    expect(formatDualDate('not-a-date', 'UTC')).toBe('-')
  })

  it('calculates panel-local days before applying offsets', () => {
    const instant = new Date('2025-01-01T01:00:00Z')
    expect(panelDayStr('America/Los_Angeles', 0, instant)).toBe('2024-12-31')
    expect(panelDayStr('America/Los_Angeles', 1, instant)).toBe('2025-01-01')
    expect(panelDayStr('Asia/Tokyo', -1, instant)).toBe('2024-12-31')
  })

  it('falls back to the local calendar for an invalid timezone', () => {
    const instant = new Date(2025, 4, 10, 12, 0, 0)
    expect(panelDayStr('Invalid/Timezone', 2, instant)).toBe('2025-05-12')
  })

  // The risk center's times are epoch milliseconds. They read in the panel's
  // timezone like every other page's times, and a zero is "never", not 1970.
  it('formats epoch milliseconds exactly as formatDualTz formats the instant', () => {
    const ms = 1_790_000_000_000
    const iso = new Date(ms).toISOString()
    for (const tz of ['', 'UTC', 'Asia/Tokyo', 'America/Los_Angeles']) {
      expect(formatMsDualTz(ms, tz), tz).toBe(formatDualTz(iso, tz))
    }
    // The panel's wall clock, not the browser's: Tokyo reads nine hours on.
    expect(formatMsDualTz(ms, 'Asia/Tokyo'))
      .toContain(new Date(ms).toLocaleString(undefined, { timeZone: 'Asia/Tokyo' }))
  })

  it('renders an absent or unusable instant as a dash', () => {
    for (const v of [0, null, undefined, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(formatMsDualTz(v, 'UTC'), String(v)).toBe('—')
    }
  })
})
