// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestPolicy } from '@/api/accessControl'
import PolicyEditorDialog, { emptyForm } from './PolicyEditorDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([{ id: 3, name: '学生' }])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [{ id: 5, name: '东京', panel_type: 'psp' }], total: 1 })) }))

const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false, hits_recent: null, last_hit_at: null, ...over,
})
const bad = (field: string) => Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { code: 'dest_policy_invalid', field, error: 'bad' } } })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ generation: 0, published_generation: 0, nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
})

const mount = (policy: DestPolicy | null, initial = emptyForm()) =>
  renderAccess(<PolicyEditorDialog open policy={policy} initial={initial} onClose={vi.fn()} onSaved={vi.fn()} />)

describe('PolicyEditorDialog round 3', () => {
  it('shows a server error for description under the description field', async () => {
    vi.mocked(api.createDestPolicy).mockRejectedValue(bad('description'))
    mount(null, { ...emptyForm(), name: 'p', ports: '25' })
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))
    const field = (await screen.findByLabelText('说明')).closest('.MuiFormControl-root')!
    await waitFor(() => expect(field.textContent).toContain('策略的内容无效'))
  })

  it('shows group_ids and panel_ids errors under their pickers', async () => {
    vi.mocked(api.createDestPolicy).mockRejectedValue(bad('group_ids'))
    mount(null, { ...emptyForm(), name: 'p', ports: '25', scope: 'groups', groupIds: [3] })
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))
    const picker = (await screen.findByLabelText('分组')).closest('.MuiFormControl-root')!
    await waitFor(() => expect(picker.textContent).toContain('策略的内容无效'))
  })

  it('drops deleted group and panel ids when seeding and says how many', async () => {
    mount(pol({ scope: 'groups', group_ids: [3, 99], }))
    expect(await screen.findByText(/1 个已删除/)).toBeTruthy()
  })

  it('limits the description by UTF-8 bytes (512), not characters', async () => {
    mount(pol())
    const field = await screen.findByLabelText('说明')
    fireEvent.change(field, { target: { value: '汉'.repeat(200) } }) // 200 chars, 600 bytes
    expect(await screen.findByText(/512 字节/)).toBeTruthy()
    expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(field, { target: { value: '汉'.repeat(100) } }) // 300 bytes
    await waitFor(() => expect(screen.queryByText(/512 字节/)).toBeNull())
  })

  it('disables the action toggle for a locked catalog policy', async () => {
    mount(pol({ template_key: 'bittorrent' }))
    const allow = await screen.findByRole('button', { name: '放行' }) as HTMLButtonElement
    expect(allow.disabled).toBe(true)
  })

  it('limits the name by UTF-8 bytes (128), not characters, and the maxLength attribute does not cut it first', async () => {
    mount(pol())
    const field = await screen.findByLabelText(/名称/)
    fireEvent.change(field, { target: { value: '汉'.repeat(50) } }) // 50 chars, 150 bytes
    expect(await screen.findByText(/128 字节/)).toBeTruthy()
    expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(field, { target: { value: '汉'.repeat(40) } }) // 120 bytes
    await waitFor(() => expect(screen.queryByText(/128 字节/)).toBeNull())
  })

  it('does not prune panel ids when the server list is only one page of a longer one', async () => {
    const servers = await import('@/api/servers')
    vi.mocked(servers.listServers).mockResolvedValueOnce({ items: [{ id: 5, name: '东京', panel_type: 'psp' }], total: 300 } as never)
    mount(pol({ scope: 'panels', panel_ids: [5, 99] }))
    await screen.findByLabelText(/名称/)
    await new Promise(r => setTimeout(r, 50))
    expect(screen.queryByText(/个已删除/)).toBeNull()
  })
})
