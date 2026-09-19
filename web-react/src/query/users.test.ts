// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { policies } from './policies'
import { sessionScope } from './session'
import { usersListQuery } from './users'

const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })

describe('usersListQuery', () => {
  it('polls while the list is visible, so a page left open discovers external changes', () => {
    // The headline gap: a page that is merely left open never learns that
    // somebody else changed the data. Focus/visibility refetching only covers
    // leaving and coming back — it is not a timer.
    const opts = usersListQuery(scope, { page: 1 })
    expect(opts.refetchInterval).toBe(60_000)
  })

  it('polls no faster than the policy allows', () => {
    // Guards against someone tuning this by hand instead of through policies.ts,
    // which is where the load budget and its measurement date are recorded.
    expect(usersListQuery(scope, { page: 1 }).refetchInterval).toBe(policies.usersList.refetchInterval)
  })

  it('keys the list by every parameter that changes the response', () => {
    const base = usersListQuery(scope, { page: 1, keyword: 'a' }).queryKey
    // A different page, filter, or session must be a different cache entry —
    // otherwise one view's rows would be served under another's query.
    expect(base).not.toEqual(usersListQuery(scope, { page: 1, keyword: 'b' }).queryKey)
    expect(base).not.toEqual(usersListQuery(scope, { page: 2, keyword: 'a' }).queryKey)
    expect(base).not.toEqual(
      usersListQuery(sessionScope({ userId: 2, role: 'admin', authEpoch: 1 }), { page: 1, keyword: 'a' }).queryKey,
    )
    // ...and identical parameters must reuse one entry.
    expect(base).toEqual(usersListQuery(scope, { page: 1, keyword: 'a' }).queryKey)
  })
})
