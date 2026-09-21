import type { CompatStatusResponse } from '@/api/servers'

// What the panel's compatibility state deserves to say to an operator.
//
// The endpoint reports facts; this decides which of them is worth a banner. The
// rule is that a banner is for a state someone would otherwise MISREAD, not for
// every non-ideal one: a range that is simply the compiled floor is normal, and a
// banner for it would train people to ignore banners.
//
// THERE USED TO BE TWO POLICY KINDS ABOVE THE RANGE ONE — a policy reviewed for a
// different build, and an expired one — and they are gone with the signed policy
// itself. What is left is the state that was always the panel's own: a range it
// could not refresh and is serving from the last good fetch, which looks exactly
// like a fresh one from outside.

export type CompatNoticeKind = 'range-stale'

export interface CompatNotice {
  kind: CompatNoticeKind
  /** Values the message interpolates; named so a translation cannot drop one. */
  values: Record<string, string>
}

/** Picks the ONE state worth showing, or nothing. */
export function compatNotice(status: CompatStatusResponse | null | undefined): CompatNotice | null {
  if (!status) return null

  if (status.xui?.last_error && status.xui?.max_tested) {
    // The range works AND the last attempt to update it failed — the state an
    // operator cannot distinguish from a fresh fetch without being told.
    return {
      kind: 'range-stale',
      values: { max: status.xui.max_tested, error: status.xui.last_error },
    }
  }
  return null
}
