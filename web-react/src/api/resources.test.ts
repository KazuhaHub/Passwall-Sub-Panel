import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}))
vi.mock('./client', () => ({ client: http }))

import * as groups from './groups'
import * as rules from './rules'
import * as syncTasks from './syncTasks'
import * as templates from './templates'
import * as traffic from './traffic'
import * as users from './users'

beforeEach(() => {
  Object.values(http).forEach(mock => mock.mockReset())
  http.get.mockImplementation((url: string) => {
    if (url.includes('/passkeys')) return Promise.resolve({ data: { passkeys: [{ id: 3 }] } })
    if (url.endsWith('/rules')) return Promise.resolve({ data: { personal_rules: 'DOMAIN,test.example' } })
    if (url.includes('/traffic/top') || url.includes('/nodes/top') || url.endsWith('/nodes') || url.endsWith('/servers')) {
      return Promise.resolve({ data: { items: [{ id: 1 }] } })
    }
    return Promise.resolve({ data: { items: [], total: 0 } })
  })
  http.post.mockImplementation((url: string) => {
    if (url.includes('/recovery/regenerate')) return Promise.resolve({ data: { recovery_codes: ['code'] } })
    if (url.endsWith('/purge')) return Promise.resolve({ data: { deleted: 4 } })
    return Promise.resolve({ data: { value: 'ok' } })
  })
  http.put.mockResolvedValue({ data: { value: 'ok' } })
  http.delete.mockImplementation((url: string) => Promise.resolve({
    data: url.endsWith('/passkeys') ? { revoked: 2 } : {},
  }))
})

describe('resource API contracts', () => {
  it('maps every user operation to its backend route', async () => {
    const signal = new AbortController().signal
    await users.listUsers({ page: 2, keyword: 'alice' }, signal)
    await users.getUser(7)
    await users.createUser({} as never)
    await users.updateUser(7, { display_name: 'Alice' })
    await users.deleteUser(7)
    await users.setEnabled(7, false, 'review')
    await users.setServiceStatus(7, false, 'manual', 'detail')
    await users.resetCredentials(7)
    await users.resetPassword(7)
    await users.resetEmergencyUsage(7)
    await users.reset2FA(7)
    await expect(users.regenerateUser2FARecovery(7)).resolves.toEqual(['code'])
    await users.unlinkSSO(7)
    await expect(users.listUserPasskeys(7)).resolves.toEqual([{ id: 3 }])
    await users.revokeUserPasskey(7, 3)
    await expect(users.revokeAllUserPasskeys(7)).resolves.toBe(2)
    await expect(users.getUserRules(7)).resolves.toBe('DOMAIN,test.example')
    await users.updateUserRules(7, 'rules')

    expect(http.get).toHaveBeenCalledWith('/admin/users', {
      params: { page: 2, keyword: 'alice' }, signal,
    })
    expect(http.post).toHaveBeenCalledWith('/admin/users/7/reset-password', { password: '' })
    expect(http.post).toHaveBeenCalledWith('/admin/users/7/set-service-status', {
      enabled: false, reason: 'manual', detail: 'detail',
    })
    expect(http.delete).toHaveBeenCalledWith('/admin/users/7/passkeys/3')
    expect(http.put).toHaveBeenCalledWith('/admin/users/7/rules', { personal_rules: 'rules' })
  })

  it('maps group, rule, template, and sync-task operations', async () => {
    const signal = new AbortController().signal
    await groups.listGroups({ page_size: 10 }, signal)
    await groups.getGroup(2)
    await groups.createGroup({ slug: 'g', name: 'Group' })
    await groups.updateGroup(2, { name: 'Renamed' })
    await groups.updateGroupLayout(2, { columns: 2 } as never)
    await groups.deleteGroup(2)

    const rule = { slug: 'r', name: 'Rule', sort: 1, enabled: true, proxy_group_order: [], content: '' }
    await rules.listRuleSets({ keyword: 'r' }, signal)
    await rules.getRuleSet('r')
    await rules.saveRuleSet(rule)
    await rules.deleteRuleSet('r')
    await rules.resetRuleSet('r')

    const template = { slug: 't', name: 'Template', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }
    await templates.listTemplates({}, signal)
    await templates.getTemplate('t')
    await templates.saveTemplate(template)
    await templates.deleteTemplate('t')
    await templates.resetTemplate('t')

    await syncTasks.listSyncTasks({ status: 'pending' })
    await syncTasks.retrySyncTask(5)
    await syncTasks.cancelSyncTask(5)
    await expect(syncTasks.purgeFinishedSyncTasks()).resolves.toEqual({ deleted: 4 })

    expect(http.get).toHaveBeenCalledWith('/admin/groups', {
      params: { page: 1, page_size: 10 }, signal,
    })
    expect(http.put).toHaveBeenCalledWith('/admin/rules/r', rule)
    expect(http.post).toHaveBeenCalledWith('/admin/templates/t/reset')
    expect(http.post).toHaveBeenCalledWith('/admin/sync-tasks/5/retry')
  })

  it('maps traffic scopes and always includes a timezone', async () => {
    await expect(traffic.topTraffic(5, { silent: true })).resolves.toEqual([{ id: 1 }])
    await traffic.trafficHistory({ period: 'day', tz: 'Asia/Taipei' })
    await traffic.userTrafficHistory(7, { period: 'week', tz: 'UTC' })
    await traffic.userTraffic(7)
    await traffic.setUserTraffic(7, 1.5)
    await traffic.pollTrafficNow()
    await expect(traffic.topNodes(3)).resolves.toEqual([{ id: 1 }])
    await traffic.nodeTrafficHistory({ node_id: 9, tz: 'UTC' })
    await expect(traffic.getUserNodeUsage(7)).resolves.toEqual([{ id: 1 }])
    await expect(traffic.getUserServerUsage(7)).resolves.toEqual([{ id: 1 }])
    await traffic.getMyUsage()
    await traffic.getMyTrafficHistory({ period: 'month', tz: 'Asia/Taipei' })

    expect(http.get).toHaveBeenCalledWith('/admin/traffic/top', {
      params: { limit: 5 }, _skipErrorToast: true,
    })
    expect(http.get).toHaveBeenCalledWith('/admin/traffic/history', {
      params: { period: 'day', tz: 'Asia/Taipei' },
    })
    expect(http.put).toHaveBeenCalledWith('/admin/traffic/user/7', { period_used_gb: 1.5 })
    expect(http.get).toHaveBeenCalledWith('/user/me/traffic/history', {
      params: { period: 'month', tz: 'Asia/Taipei' },
    })
  })
})
