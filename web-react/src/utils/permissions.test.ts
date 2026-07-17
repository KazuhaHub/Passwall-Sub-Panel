import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/auth', () => ({ useAuthStore: vi.fn() }))

import { roleCan, type Capability } from './permissions'

describe('roleCan', () => {
  const capabilities: Capability[] = [
    'config.write',
    'users.write',
    'users.elevate',
    'traffic.write',
    'sync.operate',
  ]

  it('grants every built-in capability to admins', () => {
    for (const capability of capabilities) expect(roleCan('admin', capability)).toBe(true)
  })

  it('keeps infrastructure and elevation actions admin-only', () => {
    expect(roleCan('operator', 'users.write')).toBe(true)
    expect(roleCan('operator', 'traffic.write')).toBe(true)
    expect(roleCan('operator', 'sync.operate')).toBe(true)
    expect(roleCan('operator', 'config.write')).toBe(false)
    expect(roleCan('operator', 'users.elevate')).toBe(false)
  })

  it('fails closed for users and missing roles', () => {
    for (const capability of capabilities) {
      expect(roleCan('user', capability)).toBe(false)
      expect(roleCan('', capability)).toBe(false)
    }
  })
})
