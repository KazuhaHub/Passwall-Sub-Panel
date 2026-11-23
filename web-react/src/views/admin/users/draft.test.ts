import { describe, expect, it } from 'vitest'
import type { User } from '@/api/types'
import { blankDraft, createRequest, draftOfUser, draftProblems, isDirty, periodUsedMoved, updateRequest } from './draft'
import { canManageUser, canQuickRenew, canResumeService, canSuspendService, parseSort, renewedExpireAt } from './model'

const GB = 1024 ** 3
const u = (extra: Partial<User> = {}): User => ({
  id: 2, upn: 'a@x.test', role: 'user', group_id: 1, uuid: '', sub_url: '', sso_provider: 'local', traffic_limit_bytes: 0, ip_limit: 0, device_limit: 0,
  traffic_reset_period: 'monthly', enabled: true, emergency_used_count: 0, created_at: '2026-01-01T00:00:00Z', ...extra,
})

describe('the create request', () => {
  it('reads "follow the group" as 0, the way the backend does, and sends the picked day', () => {
    const d = { ...blankDraft(1, '2026-12-31'), upn: 'new@x.test' }
    expect(createRequest(d)).toEqual({
      upn: 'new@x.test', email: undefined, display_name: undefined, password: undefined, group_id: 1, expire_date: '2026-12-31',
      traffic_limit_gb: 0, ip_limit: 0, device_limit: 0, traffic_reset_period: 'monthly', remark: undefined,
    })
  })

  it('sends no expiry for a permanent user, and the limits that were set', () => {
    const d = { ...blankDraft(3, ''), upn: 'new@x.test', expire_mode: 'permanent' as const, traffic: 1.5, ip: 2, device: 4, reset_period: 'never' as const, remark: 'note' }
    expect(createRequest(d)).toMatchObject({ group_id: 3, expire_date: undefined, traffic_limit_gb: 1.5, ip_limit: 2, device_limit: 4, traffic_reset_period: 'never', remark: 'note' })
  })
})

describe('the update request', () => {
  it('sends a limit as its value or as the inherit flag, never both', () => {
    const own = draftOfUser(u({ traffic_limit_bytes: 5 * GB, ip_limit: 3 }), 0)
    expect(updateRequest(own, u())).toMatchObject({ traffic_limit_gb: 5, ip_limit: 3, device_limit: 0 })
    const followed = draftOfUser(u({ inherits_traffic_limit: true, inherits_ip_limit: true, inherits_device_limit: true, traffic_limit_bytes: 50 * GB }), 0)
    const req = updateRequest(followed, u())
    expect(req).toMatchObject({ inherit_traffic_limit: true, inherit_ip_limit: true, inherit_device_limit: true })
    expect(req).not.toHaveProperty('traffic_limit_gb')
  })

  it('clears the expiry for a permanent user and otherwise sends the day, and sends the UPN only when it changed', () => {
    expect(updateRequest(draftOfUser(u(), 0), u())).toMatchObject({ clear_expire: true })
    expect(updateRequest(draftOfUser(u(), 0), u())).not.toHaveProperty('upn')
    const dated = draftOfUser(u({ expire_at: '2026-05-30T15:59:59Z', expire_date: '2026-05-30' }), 0)
    expect(updateRequest(dated, u())).toMatchObject({ expire_date: '2026-05-30' })
    expect(updateRequest(dated, u())).not.toHaveProperty('clear_expire')
    expect(updateRequest({ ...dated, upn: 'b@x.test' }, u())).toMatchObject({ upn: 'b@x.test' })
  })
})

describe('the draft', () => {
  it('is dirty only when something changed; a sub-megabyte usage jitter is not a change', () => {
    const base = draftOfUser(u(), 1)
    expect(isDirty(base, base)).toBe(false)
    expect(isDirty({ ...base, remark: 'x' }, base)).toBe(true)
    expect(periodUsedMoved({ ...base, period_used: 1.0004 })).toBe(false)
    expect(isDirty({ ...base, period_used: 2 }, base)).toBe(true)
    // A leaderboard that arrives late moves the base's usage, not what this draft started from.
    expect(isDirty({ ...base }, { ...base, period_used: 9, period_used_initial: 9 })).toBe(false)
  })

  it('holds the rules the old forms held', () => {
    expect(draftProblems({ ...blankDraft(1, '2026-12-31'), upn: 'not-an-email' }, 'create')).toHaveProperty('upn')
    expect(draftProblems({ ...blankDraft(1, '2026-12-31'), upn: 'a@x.test', password: 'short' }, 'create')).toHaveProperty('password')
    expect(draftProblems({ ...blankDraft('', ''), upn: 'a@x.test' }, 'create')).toMatchObject({ group_id: expect.any(String), expire_date: expect.any(String) })
    // A legacy UPN that is not an email is still a valid thing to keep when editing.
    expect(draftProblems({ ...draftOfUser(u({ upn: 'legacy' }), 0) }, 'edit')).toEqual({})
  })
})

describe('what an account may be asked', () => {
  it('lets an operator act on role=user only, and an admin on anyone', () => {
    expect(canManageUser({ role: 'user' }, false)).toBe(true)
    expect(canManageUser({ role: 'admin' }, false)).toBe(false)
    expect(canManageUser({ role: 'operator' }, false)).toBe(false)
    expect(canManageUser({ role: 'admin' }, true)).toBe(true)
  })

  it('renews from the later of now and the current expiry, and only a dated account can be renewed', () => {
    expect(canQuickRenew(u())).toBe(false)
    expect(canQuickRenew(u({ expire_at: '2030-01-01T00:00:00Z' }))).toBe(true)
    const future = '2999-01-01T00:00:00Z'
    expect(renewedExpireAt(u({ expire_at: future }), 30)).toBe('2999-01-31T00:00:00.000Z')
    expect(new Date(renewedExpireAt(u({ expire_at: '2000-01-01T00:00:00Z' }), 30)).getTime()).toBeGreaterThan(Date.now())
  })

  it('offers resume for a hold and suspend only for a healthy account', () => {
    expect(canResumeService(u({ service_status: 'blocked_client' }))).toBe(true)
    expect(canResumeService(u({ service_status: 'manual_suspended' }))).toBe(true)
    expect(canResumeService(u({ service_status: 'traffic_exceeded' }))).toBe(false)
    expect(canSuspendService(u())).toBe(true)
    expect(canSuspendService(u({ service_status: 'expired' }))).toBe(false)
  })
})

it('reads the address\'s sort, and anything unknown as the default', () => {
  expect(parseSort('upn-asc')).toEqual({ by: 'upn', dir: 'asc' })
  expect(parseSort('last_online_at-desc')).toEqual({ by: 'last_online_at', dir: 'desc' })
  expect(parseSort('password-asc')).toEqual({ by: 'id', dir: 'desc' })
  expect(parseSort('')).toEqual({ by: 'id', dir: 'desc' })
})
