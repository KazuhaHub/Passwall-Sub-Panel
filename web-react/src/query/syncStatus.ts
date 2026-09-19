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

export function useSyncStatus(scope: QueryScope, userId: number, enabled: boolean) {
  return useQuery({ ...syncStatusQuery(scope, userId), enabled })
}

export function useMySyncStatus(scope: QueryScope, enabled: boolean) {
  return useQuery({ ...mySyncStatusQuery(scope), enabled })
}
