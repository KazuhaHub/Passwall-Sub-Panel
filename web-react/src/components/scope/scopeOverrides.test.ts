import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/scopeSettings', () => ({
  deleteGroupScopeOverride: vi.fn(),
  getGroupScopeSettings: vi.fn(),
  setGroupScopeOverride: vi.fn(),
}))
vi.mock('@/api/settings', () => ({ getUISettings: vi.fn() }))

import {
  deleteGroupScopeOverride,
  getGroupScopeSettings,
  setGroupScopeOverride,
} from '@/api/scopeSettings'
import { getUISettings } from '@/api/settings'
import { kvFromGlobal, loadScopeState, saveScopeState, type ScopeState } from './scopeOverrides'

describe('scope override state', () => {
  beforeEach(() => vi.clearAllMocks())

  it('serializes global values into the backend KV representation', () => {
    expect(kvFromGlobal('bool', true)).toBe('1')
    expect(kvFromGlobal('bool', false)).toBe('0')
    expect(kvFromGlobal('int', 12)).toBe('12')
    expect(kvFromGlobal('float', 1.25)).toBe('1.25')
    expect(kvFromGlobal('str', null)).toBe('')
  })

  it('merges sparse overrides with inherited global settings', async () => {
    vi.mocked(getGroupScopeSettings).mockResolvedValue({
      scope_type: 'group',
      scope_id: 42,
      overridable: ['security.totp_enabled', 'notify.expire_before_days'],
      overrides: { 'notify.expire_before_days': '21' },
    })
    vi.mocked(getUISettings).mockResolvedValue({
      totp_enabled: true,
      expire_before_days: 7,
    } as Awaited<ReturnType<typeof getUISettings>>)

    const state = await loadScopeState(42)

    expect(getGroupScopeSettings).toHaveBeenCalledWith(42, undefined)
    expect(state.global['security.totp_enabled']).toBe('1')
    expect(state.edit['security.totp_enabled']).toEqual({ on: false, value: '1' })
    expect(state.global['notify.expire_before_days']).toBe('7')
    expect(state.edit['notify.expire_before_days']).toEqual({ on: true, value: '21' })
  })

  it('persists only changed allowlisted values', async () => {
    const state: ScopeState = {
      overridable: [
        'security.totp_enabled',
        'security.passkey_enabled',
        'notify.expire_before_days',
        'notify.traffic_remain_percent',
      ],
      global: {},
      orig: {
        'security.totp_enabled': '1',
        'notify.expire_before_days': '7',
        'notify.traffic_remain_percent': '20',
      },
      edit: {
        'security.totp_enabled': { on: false, value: '1' },
        'security.passkey_enabled': { on: true, value: '1' },
        'notify.expire_before_days': { on: true, value: '14' },
        'notify.traffic_remain_percent': { on: true, value: '20' },
        // Present in editor state, but absent from the server allowlist.
        'security.emergency_access_enabled': { on: true, value: '1' },
      },
    }

    await saveScopeState(9, state)

    expect(deleteGroupScopeOverride).toHaveBeenCalledTimes(1)
    expect(deleteGroupScopeOverride).toHaveBeenCalledWith(9, 'security', 'totp_enabled')
    expect(setGroupScopeOverride).toHaveBeenCalledTimes(2)
    expect(setGroupScopeOverride).toHaveBeenNthCalledWith(1, 9, 'security', 'passkey_enabled', '1')
    expect(setGroupScopeOverride).toHaveBeenNthCalledWith(2, 9, 'notify', 'expire_before_days', '14')
  })
})
