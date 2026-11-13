// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useNavigate } from 'react-router'

import { renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestPolicy } from '@/api/accessControl'
import PoliciesTab from './PoliciesTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([{ id: 3, name: '学生' }])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [], total: 0 })) }))

const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy>): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false, hits_recent: null, last_hit_at: null, ...over,
})
const status = { generation: 1, published_generation: 1, paused: false, publish_error: null, apply_eta_ms: 0, totals: {}, policy_notes: {}, nodes: [{ panel_id: 1, kind: 'psp', state: 'applied', supports: { policy: true }, engine: 'xray', sniffing_insufficient: [] }] } as unknown as api.DestStatus
const props = { onOpenExemptions: vi.fn(), onOpenSettings: vi.fn(), onGoToLists: vi.fn() }

function Jumper() {
  const nav = useNavigate()
  return <button onClick={() => nav('/admin/access-control?tab=policies', { state: { openPolicy: 2, highlightPolicy: 2 } })}>jump2</button>
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [pol({ id: 1 }), pol({ id: 2, name: '禁止 BT', inline: { ports: '1' }, priority: 2 })], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestCatalog).mockResolvedValue({ catalog_version: 1, templates: [], unavailable: [] })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue(status)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
  vi.mocked(api.updateDestPolicy).mockResolvedValue(pol({}))
})

describe('history-state requests', () => {
  it('opens the editor for a SECOND request while the tab stays mounted', async () => {
    const { router } = renderAccess(<><Jumper /><PoliciesTab {...props} /></>, { route: '/admin/access-control?tab=policies' })
    await screen.findAllByTestId('policy-row')
    await act(async () => { await router.navigate('/admin/access-control?tab=policies', { state: { openPolicy: 1 } }) })
    expect(await screen.findByText('编辑策略「禁止发信」')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByText('编辑策略「禁止发信」')).toBeNull())
    await act(async () => { await router.navigate('/admin/access-control?tab=policies', { state: { openPolicy: 2 } }) })
    expect(await screen.findByText('编辑策略「禁止 BT」')).toBeTruthy()
  })
})

describe('the row', () => {
  it('keeps the switch (disabled) while saving instead of swapping it for a spinner', async () => {
    let release!: () => void
    vi.mocked(api.updateDestPolicy).mockReturnValue(new Promise(r => { release = () => r(pol({})) }))
    renderAccess(<PoliciesTab {...props} />)
    const sw = (await screen.findByRole('switch', { name: /禁止发信/ })) as HTMLInputElement
    fireEvent.click(sw)
    await waitFor(() => expect((screen.getByRole('switch', { name: /禁止发信/ }) as HTMLInputElement).disabled).toBe(true))
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(screen.getByRole('switch', { name: /禁止发信/ })).toBe(sw)
    await act(async () => release())
  })

  it('disables every reorder entry while one reorder is in flight', async () => {
    let release!: () => void
    vi.mocked(api.reorderDestPolicies).mockReturnValue(new Promise<void>(r => { release = r }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findAllByTestId('policy-row')
    fireEvent.click(screen.getByRole('button', { name: /禁止发信.*(菜单|操作)|(菜单|操作).*禁止发信/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '下移' }))
    fireEvent.click(screen.getByRole('button', { name: /禁止 BT.*(菜单|操作)|(菜单|操作).*禁止 BT/ }))
    const up = await screen.findByRole('menuitem', { name: '上移' })
    expect(up.getAttribute('aria-disabled')).toBe('true')
    await act(async () => release())
  })
})
