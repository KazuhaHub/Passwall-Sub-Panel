import { describe, expect, it } from 'vitest'

import { formatDualDate, formatDualTz, panelDayStr } from './datetime'

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
})
