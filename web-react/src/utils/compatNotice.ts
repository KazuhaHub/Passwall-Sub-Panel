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

export type CompatNoticeKind = 'core-catalog-stale' | 'range-unavailable' | 'range-stale'

export interface CompatNotice {
  kind: CompatNoticeKind
  /** Values the message interpolates; named so a translation cannot drop one. */
  values: Record<string, string>
}

/** Picks the ONE state worth showing, or nothing. */
export function compatNotice(status: CompatStatusResponse | null | undefined): CompatNotice | null {
  if (!status) return null

  // THE REVIEW THE CORE CHOICES REST ON, and it comes first because offering a core
  // is a claim about what may be installed. Falling back means the panel is offering
  // what it last agreed with the project rather than what the project says today —
  // invisible from the selector, because an old reviewed release looks exactly like a
  // current one, and it is the state in which a release pulled since is still on
  // offer. The age of the review is what an operator acts on, so it is in the message.
  if (status.core_catalog?.falling_back) {
    const catalog = status.core_catalog
    return {
      kind: 'core-catalog-stale',
      values: {
        source: catalog.source ?? '',
        review: catalog.review_time ?? '',
        error: catalog.last_error ?? '',
      },
    }
  }

  // A REFRESH THAT FAILED IS WORTH SAYING EVEN WHEN THERE IS NOTHING TO FALL BACK
  // ON, and that is the case this did not cover: the notice below is about a range
  // that is the LAST GOOD one, so it needs a range to describe. With none, every
  // panel reads Unknown, the reason sits in `last_error`, and the page said nothing
  // at all — leaving "the panel is broken" as the only available diagnosis.
  if (status.xui?.last_error && !status.xui?.max_tested) {
    return { kind: 'range-unavailable', values: { error: status.xui.last_error } }
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
