import { describe, expect, it } from 'vitest'
import type { User } from '@/api/types'
import { accountAxis, serviceAxis } from './status'

const u = (extra: Partial<User> = {}): User => ({
  id: 2, upn: 'a@x.test', role: 'user', group_id: 1, uuid: '', sub_url: '', sso_provider: 'local', traffic_limit_bytes: 0, ip_limit: 0, device_limit: 0,
  traffic_reset_period: 'monthly', enabled: true, emergency_used_count: 0, created_at: '2026-01-01T00:00:00Z', ...extra,
})

// A user has two independent switches. Each case below is a state an admin can be looking at, and each one is
// told on the axis it is on: an over-quota or expired user is not an "account disabled" user.
describe('the two axes', () => {
  it('an ordinary user is fine on both', () => {
    expect(accountAxis(u())).toMatchObject({ tone: 'success', label: 'users.status.account_active' })
    expect(serviceAxis(u())).toMatchObject({ tone: 'success', label: 'users.status.service_active' })
  })

  it.each([
    ['traffic_exceeded', 'warning', 'users.status.traffic_exhausted'],
    ['expired', 'danger', 'users.status.expired'],
    ['blocked_client', 'danger', 'users.status.blocked'],
    ['emergency_active', 'info', 'users.status.emergency_active'],
  ])('%s is a SERVICE state: the account stays active', (service_status, tone, label) => {
    const user = u({ service_status })
    expect(accountAxis(user).label).toBe('users.status.account_active')
    expect(serviceAxis(user)).toMatchObject({ tone, label })
    expect(serviceAxis(user).explain).not.toBe('')
  })

  it.each([
    ['geo_auto', 'users.status.geo_auto', 'users.axis.suspended_geo_auto'],
    ['geo_anomaly', 'users.status.geo_manual', 'users.axis.suspended_geo_manual'],
    ['service_manual', 'users.status.service_suspended', 'users.axis.suspended_manual'],
  ])('a %s hold is named as the risk center names it', (reason, label, explain) => {
    const held = u({ service_status: 'manual_suspended', service_disabled_reason: reason, service_disable_detail: 'because' })
    expect(serviceAxis(held)).toMatchObject({ tone: 'danger', label, explain, detail: 'because' })
  })

  it('a disabled account is an ACCOUNT state, with the reason, and takes the service with it', () => {
    const off = u({ enabled: false, account_status: 'disabled', service_status: 'account_disabled', auto_disabled_reason: 'abuse' })
    expect(accountAxis(off)).toMatchObject({ tone: 'danger', label: 'users.status.account_disabled', detail: 'abuse' })
    expect(serviceAxis(off)).toMatchObject({ tone: 'neutral', label: 'users.status.service_unavailable' })
  })

  it.each([
    ['pending_approval', 'users.status.pending_approval'],
    ['pending_email_verify', 'users.status.pending_email_verify'],
    ['pending_delete', 'users.status.pending_delete'],
  ])('%s is told as itself, not as "disabled"', (account_status, label) => {
    expect(accountAxis(u({ enabled: false, account_status })).label).toBe(label)
  })

  it('a status this build does not know is shown as the server said it', () => {
    expect(serviceAxis(u({ service_status: 'something_new' }))).toMatchObject({ tone: 'neutral', label: 'something_new', explain: '' })
  })
})
