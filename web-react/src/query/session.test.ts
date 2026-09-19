// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { panelAPIBase } from '@/panelPath'
import { ANON_USER_ID, scopeKey, sessionScope } from './session'

const key = (o: { userId: number | null; role: string; authEpoch: number }) => scopeKey(sessionScope(o))

describe('sessionScope', () => {
  it('collapses every signed-out state into one anonymous scope', () => {
    // Otherwise a signed-out admin and a signed-out operator would own
    // different cache slots that can never be reached again.
    expect(sessionScope({ userId: null, role: 'admin', authEpoch: 3 }).userId).toBe(ANON_USER_ID)
    expect(key({ userId: null, role: 'admin', authEpoch: 3 })).toBe(
      key({ userId: null, role: 'operator', authEpoch: 9 }),
    )
  })

  it('is stable for the same session, so a token refresh does not isolate', () => {
    const scope = { userId: 7, role: 'admin', authEpoch: 1 }
    expect(key(scope)).toBe(key(scope))
  })

  it('changes when the account switches', () => {
    const scope = { userId: 7, role: 'admin', authEpoch: 1 }
    expect(key(scope)).not.toBe(key({ ...scope, userId: 8 }))
  })

  it('changes when the role changes', () => {
    // A role change redacts a different set of fields, so the cached bodies
    // from the previous role must not be reused.
    const scope = { userId: 7, role: 'admin', authEpoch: 1 }
    expect(key(scope)).not.toBe(key({ ...scope, role: 'operator' }))
  })

  it('changes when the session generation advances', () => {
    // Logout-then-login as the same account must not inherit the old cache.
    const scope = { userId: 7, role: 'admin', authEpoch: 1 }
    expect(key(scope)).not.toBe(key({ ...scope, authEpoch: 2 }))
  })

  it('includes the panel API base so a prefix change is a different scope', () => {
    expect(key({ userId: 1, role: 'admin', authEpoch: 1 })).toContain(panelAPIBase)
  })
})
