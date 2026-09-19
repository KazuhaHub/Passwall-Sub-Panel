import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  announcePending, isUserObserved, observeUser, onPendingForUser, syncPendingOf, userIdFromURL,
} from './syncPending'

afterEach(() => { vi.restoreAllMocks() })

describe('syncPendingOf', () => {
  it('reports queued work when the header says so', () => {
    expect(syncPendingOf({ headers: { 'x-sync-pending': '1' } })).toBe('reported')
  })

  it('reports nothing when the header is absent, which is not success', () => {
    // The header is a hint that there is something worth looking at. Its
    // absence is not evidence the write reached the panel, so the value is
    // "not reported" rather than a completion (ADR 0034).
    expect(syncPendingOf({ headers: {} })).toBe('not_reported')
    expect(syncPendingOf({ headers: { 'x-sync-pending': '0' } })).toBe('not_reported')
  })
})

describe('userIdFromURL', () => {
  it('reads the target out of the user-scoped admin routes', () => {
    expect(userIdFromURL('/admin/users/7')).toBe(7)
    expect(userIdFromURL('/admin/users/7/set-enabled')).toBe(7)
    expect(userIdFromURL('/panel/api/admin/users/42/rules?x=1')).toBe(42)
  })

  it('does not invent a target where there is none', () => {
    expect(userIdFromURL('/admin/users')).toBeUndefined()
    expect(userIdFromURL('/admin/sync-tasks?page=2')).toBeUndefined()
    expect(userIdFromURL('/admin/groups/7')).toBeUndefined()
    expect(userIdFromURL(undefined)).toBeUndefined()
  })
})

describe('observation registry', () => {
  it('is refcounted, so two areas on one target do not close each other', () => {
    const offA = observeUser(7)
    const offB = observeUser(7)
    expect(isUserObserved(7)).toBe(true)

    offA()
    expect(isUserObserved(7)).toBe(true)
    offB()
    expect(isUserObserved(7)).toBe(false)
  })

  it('never lets one target close another', () => {
    const off = observeUser(7)
    off()
    expect(isUserObserved(8)).toBe(false)
  })
})

describe('announcePending', () => {
  it('tells listeners which target a write just queued work for', () => {
    const seen: number[] = []
    const off = onPendingForUser(id => seen.push(id))

    announcePending('/admin/users/7/set-enabled')
    announcePending('/admin/users/9')

    expect(seen).toEqual([7, 9])
    off()
    announcePending('/admin/users/7')
    expect(seen).toEqual([7, 9])
  })

  it('says nothing for a write with no user target', () => {
    const seen: number[] = []
    const off = onPendingForUser(id => seen.push(id))

    announcePending('/admin/sync-tasks')
    announcePending(undefined)

    expect(seen).toEqual([])
    off()
  })
})
