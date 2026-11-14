// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestList, DestPolicy } from '@/api/accessControl'
import PolicyEditorDialog, { emptyForm } from './PolicyEditorDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([{ id: 3, name: '学生' }])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [], total: 0 })) }))

const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const list = (id: number, name: string, side: 'dest' | 'source'): DestList => ({ id, name, kind: 'custom', side, entry_count: 3, regexp_count: 0, state: 'ready', empty: false, last_fetched_at: 1, last_error: '', owner_group_id: 0, updated_at: 1, used_by: [] })
const pol = (over: Partial<DestPolicy> = {}): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25', network: 'tcp', protocols: ['bittorrent'] }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null, ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [list(1, '目的地列表', 'dest'), list(2, '来源列表', 'source')], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ generation: 0, published_generation: 0, nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
})

const mount = (policy: DestPolicy | null, initial = emptyForm()) =>
  renderAccess(<PolicyEditorDialog open policy={policy} initial={initial} onClose={vi.fn()} onSaved={vi.fn()} />)

describe('PolicyEditorDialog review fixes', () => {
  it('a 400 naming a field with no input shows in the dialog top error', async () => {
    vi.mocked(api.createDestPolicy).mockRejectedValue(Object.assign(new Error('x'), {
      isAxiosError: true, response: { status: 400, data: { code: 'dest_policy_invalid', field: 'inline.network', error: 'bad' } },
    }))
    mount(null, { ...emptyForm(), name: 'p', ports: '25' })
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))
    expect(await screen.findByText('策略的内容无效')).toBeTruthy()
    expect(document.querySelector('[role="alert"], .MuiAlert-message')?.textContent).toContain('策略的内容无效')
  })

  it('BitTorrent is disabled for allow and cleared when switching to allow', async () => {
    mount(null, { ...emptyForm(), name: 'p', bittorrent: true })
    const bt = await screen.findByRole('checkbox', { name: /BT/ }) as HTMLInputElement
    expect(bt.checked).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '放行' }))
    await waitFor(() => expect((screen.getByRole('checkbox', { name: /BT/ }) as HTMLInputElement).disabled).toBe(true))
    expect((screen.getByRole('checkbox', { name: /BT/ }) as HTMLInputElement).checked).toBe(false)
  })

  it('locks the match fields of a catalog policy and says why', async () => {
    mount(pol({ template_key: 'bittorrent' }))
    expect(await screen.findByText('匹配内容由规则组目录决定')).toBeTruthy()
    expect((screen.getByRole('checkbox', { name: /BT/ }) as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByRole('textbox', { name: /端口/ }) as HTMLInputElement).disabled).toBe(true)
    expect((screen.getByRole('textbox', { name: /名称/ }) as HTMLInputElement).disabled).toBe(false)
  })

  it('requires a group when the scope is groups: error shown, save disabled', async () => {
    mount(null, { ...emptyForm(), name: 'p', ports: '25', scope: 'groups' })
    expect((await screen.findByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('offers only destination lists', async () => {
    mount(null)
    fireEvent.mouseDown(await screen.findByRole('combobox', { name: /列表/ }))
    expect(await screen.findByText(/目的地列表/)).toBeTruthy()
    expect(screen.queryByText(/来源列表/)).toBeNull()
  })
})
