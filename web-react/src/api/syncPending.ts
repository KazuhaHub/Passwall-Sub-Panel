import type { AxiosResponse } from 'axios'

/**
 * What a write response said about work it had to queue for background retry.
 *
 * `not_reported` is NOT success. The `X-Sync-Pending` header is a hint that
 * there is something worth going to look at; its absence says nothing about
 * whether the write reached the upstream panel. Any caller that treats
 * `not_reported` as "the panel is in sync" has reintroduced the bug this whole
 * contract exists to prevent (ADR 0034).
 */
export type SyncPendingReport = 'reported' | 'not_reported'

export function syncPendingOf(res: Pick<AxiosResponse, 'headers'>): SyncPendingReport {
  return res.headers?.['x-sync-pending'] === '1' ? 'reported' : 'not_reported'
}

/**
 * The user id in a user-scoped admin URL, or undefined for anything else.
 *
 * The pending header itself carries no target, so the URL is the only thing
 * that attributes a queued write to the resource it touched. User writes are
 * consistently `/admin/users/<id>` or `/admin/users/<id>/<action>`; anything
 * that does not match is deliberately treated as having no user target rather
 * than as a guess.
 */
export function userIdFromURL(url: string | undefined): number | undefined {
  const m = /\/admin\/users\/(\d+)(?:[/?]|$)/.exec(url ?? '')
  if (!m) return undefined
  const id = Number(m[1])
  return Number.isSafeInteger(id) ? id : undefined
}

// Which user ids currently have a status area on screen. Refcounted because
// more than one area can show the same target, and the first to unmount must
// not speak for the rest.
const observed = new Map<number, number>()

/** Marks a user's status area as on screen. Returns the release function. */
export function observeUser(id: number): () => void {
  observed.set(id, (observed.get(id) ?? 0) + 1)
  return () => {
    const remaining = (observed.get(id) ?? 1) - 1
    if (remaining > 0) observed.set(id, remaining)
    else observed.delete(id)
  }
}

export function isUserObserved(id: number): boolean {
  return observed.has(id)
}

type PendingListener = (userId: number) => void
const listeners = new Set<PendingListener>()

/** Subscribes to "a write just queued work for this user". Returns the unsubscribe. */
export function onPendingForUser(fn: PendingListener): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

/**
 * Publishes a write's pending report to whoever is watching that target, so an
 * open status area can start observing rather than waiting to be reopened
 * (ADR 0034, frontend §3). A write with no user target publishes nothing.
 */
export function announcePending(url: string | undefined): void {
  const id = userIdFromURL(url)
  if (id === undefined) return
  listeners.forEach(fn => fn(id))
}
