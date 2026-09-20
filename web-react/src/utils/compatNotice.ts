import type { CompatStatusResponse } from '@/api/servers'

// What the panel's compatibility state deserves to say to an operator.
//
// The endpoint reports facts; this decides which of them is worth a banner. The
// rule is that a banner is for a state someone would otherwise MISREAD, not for
// every non-ideal one: a range that is simply the compiled floor is normal, and a
// banner for it would train people to ignore banners.

export type CompatNoticeKind =
  | 'policy-not-applicable'
  | 'policy-expired'
  | 'range-stale'

export interface CompatNotice {
  kind: CompatNoticeKind
  /** Values the message interpolates; named so a translation cannot drop one. */
  values: Record<string, string>
}

/**
 * Picks the ONE state worth showing, or nothing.
 *
 * Ordered by how much the state would mislead: a policy reviewed for a different
 * build looks like a working panel and is not; an expired policy looks like an
 * enforcing one; a stale range looks fresh. A range that is merely old is not on
 * this list — nothing misleads an operator about a range nobody claimed was new.
 */
export function compatNotice(status: CompatStatusResponse | null | undefined): CompatNotice | null {
  if (!status) return null

  const policy = status.policy
  if (policy?.installed && !policy.applicable) {
    // The worst of the three: everything looks configured, and none of it applies
    // to the build that is running.
    return { kind: 'policy-not-applicable', values: { revision: String(policy.revision ?? '') } }
  }
  if (policy?.expired) {
    return { kind: 'policy-expired', values: { expires: policy.expires_at ?? '' } }
  }
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
