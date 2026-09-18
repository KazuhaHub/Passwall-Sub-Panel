import { panelAPIBase } from '@/panelPath'

/**
 * Identifies one authenticated session. Every private query key is scoped by
 * this value, so a cache entry produced under one identity can never be read
 * under another — the user DTO is redacted per caller, so the same URL does
 * not return the same body for an operator and an admin.
 *
 * `authEpoch` is a non-secret session generation that changes on login,
 * logout-then-login, and identity/role switches. A plain access-token refresh
 * deliberately leaves it alone: the session is the same session.
 */
export interface QueryScope {
  apiBase: string
  userId: number
  role: string
  authEpoch: number
}

/** userId 0 marks "no session" — an anonymous scope owns no private cache. */
export const ANON_USER_ID = 0

export function sessionScope(input: {
  userId: number | null
  role: string
  authEpoch: number
}): QueryScope {
  if (!input.userId) {
    // Epoch is pinned to 0 here on purpose: with no session there is nothing
    // for a generation to protect, and letting it vary would give every
    // signed-out state its own unreachable cache slot.
    return { apiBase: panelAPIBase, userId: ANON_USER_ID, role: 'anon', authEpoch: 0 }
  }
  return {
    apiBase: panelAPIBase,
    userId: input.userId,
    role: input.role,
    authEpoch: input.authEpoch,
  }
}

/**
 * A stable string form used as React state identity for the QueryClient and as
 * the prefix of every private query key. Two scopes with the same key are the
 * same session; anything else forces a fresh cache.
 */
export function scopeKey(s: QueryScope): string {
  return `${s.apiBase}|${s.userId}|${s.role}|${s.authEpoch}`
}
