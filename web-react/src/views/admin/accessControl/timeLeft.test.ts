import { describe, expect, it } from 'vitest'

import { timeLeft } from './timeLeft'

// A span still AHEAD must never read "ago": the drawer once said
// "22 hours ago, then expires" because it borrowed the past-tense formatter.
const t = (key: string, opts?: Record<string, unknown>) => `${key.split('.').pop()}:${opts?.count}`

describe('timeLeft', () => {
  it('uses minutes, hours or days by size', () => {
    expect(timeLeft(t, 30_000)).toBe('left_minutes:1')
    expect(timeLeft(t, 40 * 60_000)).toBe('left_minutes:40')
    expect(timeLeft(t, 22 * 3_600_000)).toBe('left_hours:22')
    expect(timeLeft(t, 5 * 24 * 3_600_000)).toBe('left_days:5')
  })
})
