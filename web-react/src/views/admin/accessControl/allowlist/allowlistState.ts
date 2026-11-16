import type { DestGroup, DestRef } from '@/api/accessControl'

// WHAT THE ALLOWLIST SCREENS READ OF A GROUP, as pure functions so a test holds
// each rule. A group's state is off / report / on, any to any; "report" is the
// admin-facing word for the state in which the allowlist is only recorded.

const DAY_MS = 86_400_000

/** Which day of its state a group is on (1 on the day it began); null when the server did not say. */
export function stateDay(changedAt: number | null, now: number): number | null {
  if (changedAt === null) return null
  return Math.max(0, Math.floor((now - changedAt) / DAY_MS)) + 1
}

/** The ranges the report offers: 1, 3 and 7 days, minus those over the trial retention (R14). */
export function reportDayOptions(retention: number | undefined): number[] {
  return [1, 3, 7].filter(d => !retention || retention <= 0 || d <= retention || d === 1)
}

/** A group that is off and has no row has no allowlist to show; one that is off WITH a row keeps its lists. */
export function groupHasAllowlist(g: Pick<DestGroup, 'state' | 'updated_at'>): boolean {
  return g.state !== 'off' || g.updated_at !== null
}

/** The one thing that stops a turn-on: a list that has not been downloaded. Everything else is a note (D3). */
export function blockedByLists(notReady: readonly DestRef[]): boolean {
  return notReady.length > 0
}
