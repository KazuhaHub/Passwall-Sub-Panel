// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { WATCH_BUDGET_MS, WATCH_INTERVAL_MS, useObservationWindow, watchIntervalMs } from './syncStatus'

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

  it('stops outright on a refusal that will not change on its own', () => {
    // 403 (not allowed to read this target) and 404 (no such target) are
    // answers, not failures. Polling them for the rest of the budget would
    // repeat the same refusal twenty times (ADR 0034, frontend §7).
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, errorStatus: 403 })).toBe(false)
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, errorStatus: 404 })).toBe(false)
  })

  it('keeps waiting through a failure that may clear on its own', () => {
    // A 503 means the store could not answer, not that it never will — the
    // next round is how an unknown becomes an answer, within the budget.
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, errorStatus: 503 }))
      .toBe(WATCH_INTERVAL_MS)
    expect(watchIntervalMs({ watching: true, elapsedMs: 0, errorStatus: 500 }))
      .toBe(WATCH_INTERVAL_MS)
    expect(watchIntervalMs({ watching: true, elapsedMs: 0 })).toBe(WATCH_INTERVAL_MS)
  })
})

describe('useObservationWindow', () => {
  afterEach(() => vi.useRealTimers())

  it('opens watching and reports the wall clock it started at', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useObservationWindow())

    expect(result.current.watching).toBe(true)
    expect(result.current.expired).toBe(false)
    // The start time is what the interval predicate measures against, so it
    // must be a real clock reading, not a counter.
    expect(result.current.windowStart).toBe(Date.now())
  })

  it('closes on the wall clock, not on a tick count', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useObservationWindow())

    act(() => { vi.advanceTimersByTime(WATCH_BUDGET_MS - 1) })
    expect(result.current.watching).toBe(true)

    act(() => { vi.advanceTimersByTime(1) })
    expect(result.current.watching).toBe(false)
    expect(result.current.expired).toBe(true)
  })

  it('begins a new window when restarted, so a spent budget is not sticky', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useObservationWindow())

    act(() => { vi.advanceTimersByTime(WATCH_BUDGET_MS) })
    expect(result.current.expired).toBe(true)
    const spentStart = result.current.windowStart

    act(() => { result.current.restart() })
    expect(result.current.watching).toBe(true)
    expect(result.current.expired).toBe(false)
    // The new window must move the clock forward; a restart that only cleared
    // the flag would re-expire immediately against the old start time.
    expect(result.current.windowStart).toBeGreaterThan(spentStart)

    act(() => { vi.advanceTimersByTime(WATCH_BUDGET_MS - 1) })
    expect(result.current.watching).toBe(true)
  })
})
