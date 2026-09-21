import { describe, expect, it } from 'vitest'
import type { CompatStatusResponse } from '@/api/servers'
import { compatNotice } from './compatNotice'

function status(overrides: Partial<CompatStatusResponse> = {}): CompatStatusResponse {
  return {
    xui: { min_version: '3.4.2', max_tested: '3.8.5' },
    sui: { max_tested: '1.6.3' },
    ...overrides,
  }
}

// A banner is for a state someone would otherwise MISREAD. Every non-ideal state
// getting one is how banners become invisible.
describe('compatNotice', () => {
  it('says nothing about a working state', () => {
    expect(compatNotice(status())).toBeNull()
    expect(compatNotice(status({ xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-19T00:00:00Z' } }))).toBeNull()
  })

  it('says nothing when there is no status at all', () => {
    // The read is best-effort; a panel that cannot report its state must not
    // claim one.
    expect(compatNotice(null)).toBeNull()
    expect(compatNotice(undefined)).toBeNull()
  })

  it('flags a range whose last refresh failed, and carries the reason', () => {
    // The range still works; what an operator cannot tell without being told is
    // that it is the LAST GOOD one.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-19T00:00:00Z', last_error: 'github unreachable' },
    }))
    expect(notice?.kind).toBe('range-stale')
    expect(notice?.values.max).toBe('3.8.5')
    expect(notice?.values.error).toBe('github unreachable')
  })

  it('does not flag a refresh failure when there is no range to fall back on', () => {
    // No range means the panel says "unknown" everywhere already; a second
    // message about the same absence is noise, and the range line says it.
    const notice = compatNotice(status({ xui: { last_error: 'github unreachable' } }))
    expect(notice).toBeNull()
  })

  it('does not flag a range merely because it is old', () => {
    // Nobody claimed the range was fresh. This is the case that would turn the
    // banner into wallpaper.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-01-01T00:00:00Z' },
    }))
    expect(notice).toBeNull()
  })
})
