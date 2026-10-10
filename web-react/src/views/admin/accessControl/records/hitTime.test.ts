import { expect, it } from 'vitest'
import { hitTime } from './hitTime'

it('keeps fractional panel offsets and groups by the actual panel date', () => {
  const time = hitTime('en-US', 'Asia/Kathmandu')
  expect(time.range(Date.parse('2026-10-09T09:00:00Z'))).toBe('14:45–15:45')
  expect(time.day(Date.parse('2026-10-09T20:00:00Z'))).toBe('2026-10-10')
  expect(time.full(Date.parse('2026-10-09T09:03:00Z'))).toContain('14:48')
})

it('formats both boundaries independently across daylight-saving changes', () => {
  const time = hitTime('en-US', 'America/Los_Angeles')
  expect(time.range(Date.parse('2026-03-08T09:00:00Z'))).toBe('01:00–03:00')
  expect(time.full(Date.parse('2026-11-01T08:00:00Z'))).toContain('GMT-7')
  expect(time.full(Date.parse('2026-11-01T09:00:00Z'))).toContain('GMT-8')
})

it('uses browser time consistently when the configured time zone is invalid', () => {
  const at = Date.parse('2026-10-09T09:00:00Z'), fallback = hitTime('en-US', ''), invalid = hitTime('en-US', 'invalid-zone')
  expect(invalid.range(at)).toBe(fallback.range(at))
  expect(invalid.day(at)).toBe(fallback.day(at))
})
