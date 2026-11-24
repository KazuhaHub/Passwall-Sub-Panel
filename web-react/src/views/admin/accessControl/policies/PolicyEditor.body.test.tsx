// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestPolicy } from '@/api/accessControl'
import PolicyEditor from './PolicyEditor'
import { emptyForm } from './policyForm'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([{ id: 3, name: '学生', members: 40 }, { id: 4, name: '员工', members: 10 }])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [], total: 0 })) }))

const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'off', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null, ...over,
})
const status = { generation: 1, published_generation: 1, paused: false, publish_error: null, apply_eta_ms: 61000, totals: {}, policy_notes: {}, nodes: [{ panel_id: 1, kind: 'psp', state: 'applied', supports: { policy: true }, engine: 'xray', sniffing_insufficient: [] }] } as unknown as api.DestStatus

beforeEach(() => {
  vi.clearAllMocks()
  // One policy already running, so a second one switched on is not "the first".
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [pol({ id: 2, state: 'on' })], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue(status)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
  vi.mocked(api.createDestPolicy).mockResolvedValue(pol())
  vi.mocked(api.updateDestPolicy).mockResolvedValue(pol())
})

const mount = (policy: DestPolicy | null, initial = emptyForm()) =>
  renderAccess(<PolicyEditor policy={policy} initial={initial} back={{ to: '/admin/access-control', label: '访问控制' }} onCreated={vi.fn()} />)

describe('what the editor sends', () => {
  it('a new policy is created with exactly the body the form describes, the order of its lists kept', async () => {
    mount(null, { ...emptyForm(), name: ' 禁止发信 ', description: '', listIds: [2, 1], cidrs: '10.0.0.0/8\n\n192.168.0.0/16 ', ports: '25,465', network: 'tcp', private: true, scope: 'groups', groupIds: [3, 4] })
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestPolicy).toHaveBeenCalled())
    expect(vi.mocked(api.createDestPolicy).mock.calls[0][0]).toEqual({
      name: '禁止发信', description: '', action: 'block', state: 'report', list_ids: [2, 1],
      inline: { cidrs: ['10.0.0.0/8', '192.168.0.0/16'], ports: '25,465', network: 'tcp', private: true },
      scope: 'groups', group_ids: [3, 4], panel_ids: [], counts_as_risk: false,
    })
  })

  it('an edit sends the whole policy with the updated_at it was read at', async () => {
    mount(pol({ state: 'on' }))
    fireEvent.change(await screen.findByLabelText(/名称/), { target: { value: '禁止发信 2' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
    expect(vi.mocked(api.updateDestPolicy).mock.calls[0]).toEqual([1, {
      name: '禁止发信 2', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' },
      scope: 'all', group_ids: [], panel_ids: [], counts_as_risk: false, updated_at: 111,
    }])
  })
})

describe('switching a paused policy on from the editor', () => {
  it('names who it reaches, focuses Cancel, and writes nothing when declined', async () => {
    confirmMock.mockImplementation(() => Promise.resolve(false))
    mount(pol({ scope: 'groups', group_ids: [3, 4] }))
    fireEvent.click(await screen.findByRole('radio', { name: '开启' }))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalled())
    const opts = (confirmMock.mock.calls as unknown as unknown[][])[0][0] as { title: string; message: string; initialFocus: string }
    expect(opts.title).toBe('启用策略「禁止发信」？')
    expect(opts.message).toContain('2 个分组（学生、员工），共 50 人')
    expect(opts.message).toContain('1 台节点')
    expect(opts.initialFocus).toBe('cancel')
    expect(api.updateDestPolicy).not.toHaveBeenCalled()
  })
})
