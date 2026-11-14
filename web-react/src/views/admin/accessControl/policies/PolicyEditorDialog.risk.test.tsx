// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestPolicy } from '@/api/accessControl'
import PolicyEditorDialog, { emptyForm, formOf, inputOf } from './PolicyEditorDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [], total: 0 })) }))

const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null,
  counts_as_risk: true, ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ generation: 0, published_generation: 0, nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
  vi.mocked(api.updateDestPolicy).mockResolvedValue(pol())
  vi.mocked(api.createDestPolicy).mockResolvedValue(pol())
})

const mount = (policy: DestPolicy | null, initial = emptyForm()) =>
  renderAccess(<PolicyEditorDialog open policy={policy} initial={initial} onClose={vi.fn()} onSaved={vi.fn()} />)

describe('counts_as_risk', () => {
  it('round-trips through the form: a block policy keeps it, an allow policy never sends it', () => {
    expect(inputOf(formOf(pol())).counts_as_risk).toBe(true)
    expect(inputOf(formOf(pol({ counts_as_risk: false }))).counts_as_risk).toBe(false)
    expect(inputOf(formOf(pol({ action: 'allow', counts_as_risk: false }))).counts_as_risk).toBeUndefined()
    expect(inputOf({ ...formOf(pol()), action: 'allow' }).counts_as_risk).toBeUndefined()
  })

  it('a block policy shows the toggle as stored and saves the changed value', async () => {
    mount(pol())
    const box = await screen.findByRole('checkbox', { name: /计入风险/ }) as HTMLInputElement
    expect(box.checked).toBe(true)
    fireEvent.click(box)
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(1, expect.objectContaining({ counts_as_risk: false })))
  })

  it('an allow policy has no toggle, and switching a block policy to allow hides it and sends no value', async () => {
    mount(pol())
    await screen.findByRole('checkbox', { name: /计入风险/ })
    fireEvent.click(screen.getByRole('button', { name: '放行' }))
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: /计入风险/ })).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
    expect(vi.mocked(api.updateDestPolicy).mock.calls[0][1]).not.toHaveProperty('counts_as_risk')
  })
})

describe('saving a report-only policy as On', () => {
  it('asks first, naming the policy, and does not write when declined', async () => {
    confirmMock.mockImplementation(() => Promise.resolve(false))
    mount(pol({ state: 'report', counts_as_risk: false }))
    fireEvent.click(await screen.findByRole('button', { name: '开启' }))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '把「禁止发信」改为拦截？', confirmText: '改为拦截' })))
    expect(api.updateDestPolicy).not.toHaveBeenCalled()
  })

  it('writes once it is confirmed', async () => {
    mount(pol({ state: 'report' }))
    fireEvent.click(await screen.findByRole('button', { name: '开启' }))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(1, expect.objectContaining({ state: 'on' })))
    expect(confirmMock).toHaveBeenCalled()
  })

  it('does not ask for any other save', async () => {
    mount(pol({ state: 'on' }))
    fireEvent.click(await screen.findByRole('checkbox', { name: /计入风险/ }))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
    expect(confirmMock).not.toHaveBeenCalled()
  })
})
