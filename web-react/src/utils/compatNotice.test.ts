import { describe, expect, it } from 'vitest'
import type { CompatStatusResponse } from '@/api/servers'
import { compatNotice } from './compatNotice'

function status(overrides: Partial<CompatStatusResponse> = {}): CompatStatusResponse {
  return {
    xui: { min_version: '3.4.2', max_tested: '3.8.5' },
    sui: { max_tested: '1.6.3' },
    ...overrides,
  }
}

// A banner is for a state someone would otherwise MISREAD. Every non-ideal state
// getting one is how banners become invisible.
describe('compatNotice', () => {
  it('says nothing about a working state', () => {
    expect(compatNotice(status())).toBeNull()
    expect(compatNotice(status({ xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-19T00:00:00Z' } }))).toBeNull()
  })

  it('says nothing when there is no status at all', () => {
    // The read is best-effort; a panel that cannot report its state must not
    // claim one.
    expect(compatNotice(null)).toBeNull()
    expect(compatNotice(undefined)).toBeNull()
  })

  it('flags a range whose last refresh failed, and carries the reason', () => {
    // The range still works; what an operator cannot tell without being told is
    // that it is the LAST GOOD one.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-19T00:00:00Z', last_error: 'github unreachable' },
    }))
    expect(notice?.kind).toBe('range-stale')
    expect(notice?.values.max).toBe('3.8.5')
    expect(notice?.values.error).toBe('github unreachable')
  })

  it('flags a range that could not be fetched at all, because nothing else says why', () => {
    // EVERY PANEL READS UNKNOWN in this state, and the reason is here and nowhere
    // else. Without this the page said nothing, which leaves "the panel is broken"
    // as the only diagnosis an operator can reach.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', last_error: '3x-ui-v4.json answered HTTP 403' },
    }))
    expect(notice?.kind).toBe('range-unavailable')
    expect(notice?.values.error).toBe('3x-ui-v4.json answered HTTP 403')
  })

  it('prefers the range it still has over the failure that produced it', () => {
    // A range that is merely last-good is the notice below: it tells the operator
    // what the ceiling is, which the "nothing fetched" one cannot.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', max_tested: '3.8.5', last_error: 'github unreachable' },
    }))
    expect(notice?.kind).toBe('range-stale')
  })

  it('flags a core catalog the panel is falling back on, and carries its age', () => {
    // THE ONE AN OPERATOR CANNOT SEE FROM THE SELECTOR: a reviewed release that has
    // since been withdrawn is still on the list, and nothing about the list says so.
    // The age of the review is what they act on, so it is in the message.
    const notice = compatNotice(status({
      core_catalog: {
        source: 'shipped with this panel build; regenerated when this build is cut',
        review_time: '2026-09-11T00:00:00Z',
        last_error: 'release/4.0.1 does not publish core-catalog.json',
        falling_back: true,
      },
    }))
    expect(notice?.kind).toBe('core-catalog-stale')
    expect(notice?.values.review).toBe('2026-09-11T00:00:00Z')
    expect(notice?.values.error).toBe('release/4.0.1 does not publish core-catalog.json')
  })

  it('says nothing about a core catalog read from the origin', () => {
    expect(compatNotice(status({
      core_catalog: { source: 'read from the published release', review_time: '2026-09-20T00:00:00Z', last_success: '2026-09-21T11:56:00Z' },
    }))).toBeNull()
  })

  it('says nothing when a panel cannot report its core catalog at all', () => {
    // No field means an older panel, not a failing one. Inventing a banner from its
    // silence would be reading a conclusion out of a missing answer.
    expect(compatNotice(status({ core_catalog: undefined }))).toBeNull()
  })

  // THIS CASE USED TO ASSERT THE OPPOSITE, and the reasoning it recorded was
  // "no range means the panel says unknown everywhere already; a second message
  // about the same absence is noise". What that missed is WHY: the per-panel badge
  // says the range is missing and never says why it is missing, and the two causes
  // — a document nobody fetched, and a fetch that keeps failing — send an operator
  // to different places. A panel whose compat fetch is being refused looks, from
  // every other surface, exactly like a panel with a broken compatibility system.
  it('flags a refresh that failed even when there is no range to fall back on', () => {
    const notice = compatNotice(status({ xui: { last_error: 'github unreachable' } }))
    expect(notice?.kind).toBe('range-unavailable')
    expect(notice?.values.error).toBe('github unreachable')
  })

  it('does not flag a range merely because it is old', () => {
    // Nobody claimed the range was fresh. This is the case that would turn the
    // banner into wallpaper.
    const notice = compatNotice(status({
      xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-01-01T00:00:00Z' },
    }))
    expect(notice).toBeNull()
  })
})
