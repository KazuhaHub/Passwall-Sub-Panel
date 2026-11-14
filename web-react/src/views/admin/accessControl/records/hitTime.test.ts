import { describe, expect, it } from 'vitest'

import { dayKey, dayRelation, formatClock, formatHourRange } from './hitTime'

const H = 3_600_000

describe('the hour a hit row covers, in the PANEL\'s time zone', () => {
  it('writes the bucket start and end in the panel zone', () => {
    expect(formatHourRange(Date.UTC(2026, 10, 13, 6), 'Asia/Shanghai')).toBe('14:00–15:00')
    expect(formatHourRange(Date.UTC(2026, 10, 13, 6), 'UTC')).toBe('06:00–07:00')
  })

  it('a zone that is not on the hour is shown as it is, never rounded (14:30–15:30)', () => {
    expect(formatHourRange(Date.UTC(2026, 10, 13, 9), 'Asia/Kolkata')).toBe('14:30–15:30')
    expect(formatHourRange(Date.UTC(2026, 10, 13, 9), 'Asia/Kathmandu')).toBe('14:45–15:45')
  })

  it('an hour that ends past midnight reads as the next day\'s 00:00', () => {
    expect(formatHourRange(Date.UTC(2026, 10, 13, 15), 'Asia/Shanghai')).toBe('23:00–00:00')
  })

  it('the first and last moment inside a bucket (the hover) are clock times in the same zone', () => {
    expect(formatClock(Date.UTC(2026, 10, 13, 6, 3), 'Asia/Shanghai')).toBe('14:03')
  })
})

describe('the day a row belongs to', () => {
  it('is the panel zone\'s calendar day, not the browser\'s or UTC\'s', () => {
    // 23:30 UTC on the 12th is already the 13th in Shanghai.
    expect(dayKey(Date.UTC(2026, 10, 12, 23, 30), 'Asia/Shanghai')).toBe('2026-11-13')
    expect(dayKey(Date.UTC(2026, 10, 12, 23, 30), 'UTC')).toBe('2026-11-12')
  })

  it('says today, yesterday or a date', () => {
    const now = Date.UTC(2026, 10, 13, 6)
    expect(dayRelation(now - H, now, 'Asia/Shanghai')).toBe('today')
    expect(dayRelation(now - 20 * H, now, 'Asia/Shanghai')).toBe('yesterday')
    expect(dayRelation(now - 80 * H, now, 'Asia/Shanghai')).toBe('date')
  })

  it('an empty zone falls back to the browser\'s, and a bad one to UTC rather than throwing', () => {
    expect(() => formatHourRange(Date.UTC(2026, 10, 13, 6), '')).not.toThrow()
    expect(formatHourRange(Date.UTC(2026, 10, 13, 6), 'Not/AZone')).toBe('06:00–07:00')
  })
})
