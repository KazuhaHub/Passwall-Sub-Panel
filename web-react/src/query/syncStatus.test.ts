// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { WATCH_BUDGET_MS, WATCH_INTERVAL_MS, watchIntervalMs } from './syncStatus'

describe('watchIntervalMs', () => {
  it('polls on the configured cadence while there is something to watch', () => {
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, state: 'active_tasks' }))
      .toBe(WATCH_INTERVAL_MS)
  })

  it('stops once there is nothing pending', () => {
    // Continuing to poll a settled target is pure load. The ADR is explicit
    // that stopping here must not be reported as upstream having succeeded.
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, state: 'no_active_tasks' }))
      .toBe(false)
  })

  it('keeps polling while the first answer is still unknown', () => {
    // No data yet (or a failed read) is not "settled" — the next round is how
    // an unknown becomes an answer, within the budget.
    expect(watchIntervalMs({ watching: true, elapsedMs: 0 })).toBe(WATCH_INTERVAL_MS)
  })

  it('stops at the wall-clock budget even with tasks still active', () => {
    // The budget bounds what THIS screen does, not how long the backend may
    // retry: a task can outlive the window by design.
    expect(watchIntervalMs({ watching: true, elapsedMs: WATCH_BUDGET_MS, state: 'active_tasks' }))
      .toBe(false)
    expect(watchIntervalMs({ watching: true, elapsedMs: WATCH_BUDGET_MS + 1, state: 'active_tasks' }))
      .toBe(false)
  })

  it('does not poll when the area is closed', () => {
    // Closing the area (or switching target, or losing the session) stops the
    // observation; it never cancels the backend task.
    expect(watchIntervalMs({ watching: false, elapsedMs: 0, state: 'active_tasks' })).toBe(false)
  })

  it('never returns a cadence shorter than the configured one', () => {
    // A guard against a future edit that makes this hotter than the ADR's
    // registered 15s without updating the shared constant.
    const got = watchIntervalMs({ watching: true, elapsedMs: 1, state: 'active_tasks' })
    expect(got).toBe(WATCH_INTERVAL_MS)
    expect(WATCH_INTERVAL_MS).toBeGreaterThanOrEqual(10_000)
  })
})
