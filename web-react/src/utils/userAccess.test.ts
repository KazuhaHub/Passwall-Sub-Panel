import { describe, expect, it } from 'vitest'

import type { User } from '@/api/types'
import { accountEnabledForEdit, accountStateOf, proxyEnabled, serviceStateOf } from './userAccess'

function user(overrides: Partial<User> = {}): User {
  return {
    id: 1,
    upn: 'user@example.test',
    sso_provider: 'local',
    role: 'user',
    group_id: 1,
    uuid: 'uuid',
    sub_url: '/sub/token',
    traffic_limit_bytes: 0,
    traffic_reset_period: 'monthly',
    enabled: true,
    emergency_used_count: 0,
    created_at: '2026-07-17T00:00:00Z',
    ...overrides,
  }
}

describe('user access snapshot readers', () => {
  it('prefers the backend access snapshot over contradictory raw fields', () => {
    const value = user({
      enabled: false,
      account_status: 'disabled',
      service_status: 'account_disabled',
      access: {
        account_state: 'active',
        service_state: 'expired',
        can_login: true,
        can_use_portal: true,
        can_subscribe: false,
        proxy_enabled: false,
        legacy_service_encoding: true,
      },
    })
    expect(accountStateOf(value)).toBe('active')
    expect(serviceStateOf(value)).toBe('expired')
    expect(accountEnabledForEdit(value)).toBe(true)
    expect(proxyEnabled(value)).toBe(false)
  })

  it('uses flat server statuses for rolling-upgrade compatibility', () => {
    const value = user({ account_status: 'pending_approval', service_status: 'account_disabled' })
    expect(accountStateOf(value)).toBe('pending_approval')
    expect(serviceStateOf(value)).toBe('account_disabled')
    expect(accountEnabledForEdit(value)).toBe(false)
  })

  it('does not derive expiry or traffic state in the browser', () => {
    const value = user({ expire_at: '2000-01-01T00:00:00Z', service_status: undefined })
    expect(serviceStateOf(value)).toBe('active')
  })
})
