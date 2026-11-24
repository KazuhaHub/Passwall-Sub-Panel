import type { User } from '@/api/types'

/**
 * The one-time result of a create (the initial password, the subscription link) travelling from the create page
 * to the new user's page. It lives in memory only, on purpose: router state is written into the browser's history
 * entry and would outlive the page, and a password must not. A reload before it is read loses it, which is the
 * contract of "shown once". `take` hands it over and forgets it.
 */
interface Handoff { user: User; password: string }

const pending = new Map<number, Handoff>()

export function leaveSecret(user: User, password: string) { pending.set(user.id, { user, password }) }

export function takeSecret(userId: number): Handoff | undefined {
  const h = pending.get(userId)
  pending.delete(userId)
  return h
}
