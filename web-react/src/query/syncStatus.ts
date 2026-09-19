import { useCallback, useEffect, useState } from 'react'
import { queryOptions, useQuery } from '@tanstack/react-query'
import { getMySyncStatus, getSyncStatus, type SyncStatusState } from '@/api/syncStatus'
import { syncStatusKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The observation cadence and wall-clock budget for a status area that is open.
 *
 * These are UX parameters, NOT an upstream convergence SLA: a task can be
 * retried by the backend for far longer than five minutes, and the window
 * closing says nothing about whether it succeeded. Registered here so the
 * numbers exist in one place (ADR 0034's frontend section).
 */
export const WATCH_INTERVAL_MS = 15_000
export const WATCH_BUDGET_MS = 5 * 60_000

/**
 * How long to wait before the next status read, or false to stop.
 *
 * Pure so the rules can be read and tested on their own — this is the part of
 * the observation that decides load, and it has three ways to stop.
 */
export function watchIntervalMs(input: {
  /** The area is open, the target and session are unchanged, the tab visible. */
  watching: boolean
  /** Wall-clock time since this observation window began. */
  elapsedMs: number
  /** The state from the last successful read; undefined while still unknown. */
  state?: SyncStatusState
}): number | false {
  if (!input.watching) return false
  if (input.elapsedMs >= WATCH_BUDGET_MS) return false
  // Settled: nothing pending. An unknown state (no answer yet, or a failed
  // read) deliberately does NOT stop the window — the next round is how an
  // unknown becomes an answer.
  if (input.state === 'no_active_tasks') return false
  return WATCH_INTERVAL_MS
}

export function syncStatusQuery(scope: QueryScope, userId: number) {
  return queryOptions({
    queryKey: syncStatusKeys.user(scope, userId),
    queryFn: ({ signal }) => getSyncStatus(userId, { signal }),
    // The watcher owns the pacing. A library-level retry would stack on top of
    // the interval and quietly double the request rate.
    retry: false,
    ...freshness(policies.syncStatus),
  })
}

export function mySyncStatusQuery(scope: QueryScope) {
  return queryOptions({
    queryKey: syncStatusKeys.mine(scope),
    queryFn: ({ signal }) => getMySyncStatus({ signal }),
    retry: false,
    ...freshness(policies.syncStatus),
  })
}

/**
 * The wall clock of one open status area: when it started, and whether its
 * budget is spent.
 *
 * Both halves matter and neither implies the other. `windowStart` is what the
 * interval predicate measures against, so the window always ends on time even
 * when the browser throttles the timer below (a background tab's `setTimeout`
 * can be delayed indefinitely). `expired` exists only because the predicate
 * runs inside the library and cannot make the component re-render — without it
 * a spent budget would leave a stale "watching" label on screen.
 *
 * `restart` begins a new window. It is a state setter rather than a ref write
 * for exactly that reason: the caller is a button, and a restart that did not
 * re-render would leave the hook holding the old start time.
 */
export function useObservationWindow() {
  const [windowStart, setWindowStart] = useState(() => Date.now())
  const [expired, setExpired] = useState(false)

  useEffect(() => {
    setExpired(false)
    const remaining = WATCH_BUDGET_MS - (Date.now() - windowStart)
    if (remaining <= 0) {
      setExpired(true)
      return
    }
    const id = setTimeout(() => setExpired(true), remaining)
    return () => clearTimeout(id)
  }, [windowStart])

  const restart = useCallback(() => setWindowStart(Date.now()), [])
  return { watching: !expired, windowStart, expired, restart }
}

/**
 * The observation window a status read is paced by, as passed to the hooks
 * below. Disabling the query when the window closes is deliberate: after the
 * budget, a focus revalidation would be an automatic refresh the "paused"
 * label says is not happening. The manual refresh button is the way back in.
 */
export interface ObservationWindow {
  watching: boolean
  /** Epoch ms the window began; the budget is measured from here. */
  windowStart: number
}

export function useSyncStatus(scope: QueryScope, userId: number, window: ObservationWindow) {
  return useQuery({
    ...syncStatusQuery(scope, userId),
    enabled: window.watching,
    refetchInterval: (query) =>
      watchIntervalMs({
        watching: window.watching,
        elapsedMs: Date.now() - window.windowStart,
        state: query.state.data?.state,
      }),
  })
}

export function useMySyncStatus(scope: QueryScope, window: ObservationWindow) {
  return useQuery({
    ...mySyncStatusQuery(scope),
    enabled: window.watching,
    refetchInterval: (query) =>
      watchIntervalMs({
        watching: window.watching,
        elapsedMs: Date.now() - window.windowStart,
        state: query.state.data?.state,
      }),
  })
}
