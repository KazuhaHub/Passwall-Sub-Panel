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
    'risk.view',
    'diagnostics.view',
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

  // The risk center is a read, not a config mutation, so it has its own
  // capability rather than borrowing config.write — but it reads adminGroup
  // endpoints only, so an operator offered it would collect nothing but 403s.
  it('keeps the risk center admin-only', () => {
    expect(roleCan('admin', 'risk.view')).toBe(true)
    expect(roleCan('operator', 'risk.view')).toBe(false)
  })

  // Every diagnostics endpoint is adminGroup (the registry read and the
  // clear), so the page is admin-only for the same reason the risk center is.
  it('keeps diagnostics admin-only', () => {
    expect(roleCan('admin', 'diagnostics.view')).toBe(true)
    expect(roleCan('operator', 'diagnostics.view')).toBe(false)
  })

  it('fails closed for users and missing roles', () => {
    for (const capability of capabilities) {
      expect(roleCan('user', capability)).toBe(false)
      expect(roleCan('', capability)).toBe(false)
    }
  })
})
