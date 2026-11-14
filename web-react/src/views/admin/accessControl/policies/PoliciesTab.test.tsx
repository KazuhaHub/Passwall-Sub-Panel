// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestCatalog, DestPolicies, DestPolicy } from '@/api/accessControl'
import PoliciesTab from './PoliciesTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async importOriginal => ({ ...await importOriginal<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([{ id: 3, name: '学生' }])) }))
vi.mock('@/api/servers', async importOriginal => ({ ...await importOriginal<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [{ id: 5, name: '东京 01', panel_type: 'psp' }], total: 1 })) }))

const budget = { rules: { used: 2, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 230, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy>): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25,465,587', network: 'tcp' }, scope: 'all',
  group_ids: [], panel_ids: [], priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false,
  hits_recent: null, would_block_recent: null, last_hit_at: null, ...over,
})
const policies = (over: Partial<DestPolicies> = {}): DestPolicies => ({
  allow: [pol({ id: 9, name: '公司内网', action: 'allow', inline: { private: true } })],
  block: [pol({ id: 1 }), pol({ id: 2, name: '禁止 BT', state: 'off', priority: 2, inline: { protocols: ['bittorrent'] } }), pol({ id: 3, name: '分组限定', priority: 3, scope: 'groups', group_ids: [3], inline: { ports: '1' } })],
  observe: [], exemptions: { count: 3 }, allowlist_groups: [], hit_window_days: 7, budget, ...over,
})
const catalog: DestCatalog = {
  catalog_version: 1,
  templates: [{ key: 'bittorrent', name: { 'zh-CN': 'BT 下载', 'en-US': 'BitTorrent' }, description: { 'zh-CN': '拦截 BT', 'en-US': 'Blocks BT' }, added: false }],
  unavailable: [{ key: 'gambling', name: { 'zh-CN': '博彩', 'en-US': 'Gambling' }, reason: { 'zh-CN': '没有通用的博彩分类', 'en-US': 'none' } }],
}
const status = (nodes = 1) => ({
  generation: 1, published_generation: 1, paused: false, publish_error: null, apply_eta_ms: 61000, totals: {}, policy_notes: {}, geosite_release: '', geoip_unavailable: false,
  last_write_at: null, next_publish_at: null, publish_error_at: null,
  nodes: Array.from({ length: nodes }, (_, i) => ({ panel_id: i + 1, kind: 'psp', state: 'applied', supports: { policy: true }, engine: 'xray', sniffing_insufficient: [] })),
}) as unknown as api.DestStatus

const props = { onOpenExemptions: vi.fn(), onOpenSettings: vi.fn(), onGoToLists: vi.fn() }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies())
  vi.mocked(api.getDestCatalog).mockResolvedValue(catalog)
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: { dest_policy_apply_min_seconds: 0, dest_list_refresh_hours: 0, dest_rules_update_mode: '', dest_hit_retention_days: 0, dest_trial_retention_days: 0 }, defaults: {}, effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: 7 } })
  vi.mocked(api.updateDestPolicy).mockResolvedValue(pol({}))
  vi.mocked(api.reorderDestPolicies).mockResolvedValue()
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
})

describe('the rail', () => {
  it('draws allow, exempt, block, rest with computed numbers, quota with amber, and rows in two lines', async () => {
    renderAccess(<PoliciesTab {...props} />)
    expect(await screen.findByText('禁止发信')).toBeTruthy()
    const heads = screen.getAllByRole('heading', { level: 2 }).map(h => h.textContent)
    expect(heads).toEqual(['放行 · 命中即放行', '豁免账号 · 3 个 · 不受任何访问限制', '拦截', '仅报告 · 不拦截，只记录本应拦截的连接；不改变任何连接的结果', '其余连接：直接放行'])
    expect(screen.getByText(/额度/).textContent).toContain('正则 230 / 256')
    const rows = screen.getAllByTestId('policy-row')
    expect(rows).toHaveLength(4)
    expect(within(rows[0]).getByText('内网与云元数据地址 · 所有人')).toBeTruthy()
    expect(within(rows[1]).getByText('端口 25,465,587 且 TCP · 所有人')).toBeTruthy()
    expect(within(rows[2]).getByText('已停用')).toBeTruthy()
    expect(within(rows[3]).getByText(/学生/)).toBeTruthy()
    // No allowlist step and no "watch only" action: those are not part of this build.
    expect(screen.queryByText(/仅观察|白名单/)).toBeNull()
  })

  it('the exemptions step opens the sheet', async () => {
    renderAccess(<PoliciesTab {...props} />)
    fireEvent.click(await screen.findByRole('button', { name: '管理' }))
    expect(props.onOpenExemptions).toHaveBeenCalled()
  })
})

describe('writes', () => {
  it('the switch PUTs the whole policy with updated_at and no first-publish confirm when something is already on', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(screen.getByRole('switch', { name: '启用「禁止 BT」' }))
    // Its earlier state is not known here, so the admin says which it is turned on as.
    fireEvent.click(await screen.findByRole('button', { name: '开启拦截' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
    const [id, body] = vi.mocked(api.updateDestPolicy).mock.calls[0]
    expect(id).toBe(2)
    expect(body).toMatchObject({ name: '禁止 BT', state: 'on', action: 'block', updated_at: 111, inline: { protocols: ['bittorrent'] } })
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('the first policy to be enabled asks first, with the node count and minutes', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ allow: [], block: [pol({ id: 2, state: 'off' })] }))
    renderAccess(<PoliciesTab {...props} />)
    fireEvent.click(await screen.findByRole('switch', { name: '启用「禁止发信」' }))
    fireEvent.click(await screen.findByRole('button', { name: '开启拦截' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '启用第一条访问策略？', message: expect.stringContaining('1 台节点会在约 2 分钟内') })))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
  })

  it('moving sends ALL ids of the action in the new order', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(screen.getByRole('button', { name: '「禁止 BT」的更多操作' }))
    fireEvent.click(await screen.findByText('上移'))
    await waitFor(() => expect(api.reorderDestPolicies).toHaveBeenCalledWith('block', [2, 1, 3]))
  })

  it('delete asks, naming the policy, then deletes', async () => {
    vi.mocked(api.deleteDestPolicy).mockResolvedValue()
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(screen.getByRole('button', { name: '「禁止 BT」的更多操作' }))
    fireEvent.click(await screen.findByText('删除'))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '删除策略「禁止 BT」？', destructive: true })))
    await waitFor(() => expect(api.deleteDestPolicy).toHaveBeenCalledWith(2))
  })
})

describe('the editor', () => {
  it('opens on a click, saves only when changed, and an over-quota preview blocks the save', async () => {
    renderAccess(<PoliciesTab {...props} />)
    fireEvent.click(await screen.findByText('禁止发信'))
    const dialog = await screen.findByRole('dialog')
    const save = within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    fireEvent.change(within(dialog).getByLabelText(/名称/), { target: { value: '禁止发信 2' } })
    expect(save.disabled).toBe(false)
    expect(within(dialog).getByText('只改名称或说明不会让节点重启')).toBeTruthy()
    vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget: { ...budget, regexps: { used: 300, limit: 256 } } })
    fireEvent.change(within(dialog).getByLabelText('端口'), { target: { value: '26' } })
    await waitFor(() => expect(within(dialog).getAllByText(/超出额度时新版本不会下发/).length).toBeGreaterThan(0), { timeout: 2000 })
    expect((within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('a 409 stale answer offers to load the latest instead of overwriting', async () => {
    const stale = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_policy_stale' } } })
    vi.mocked(api.updateDestPolicy).mockRejectedValue(stale)
    renderAccess(<PoliciesTab {...props} />)
    fireEvent.click(await screen.findByText('禁止发信'))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText(/名称/), { target: { value: 'x' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByText('这条策略刚被其他人修改过')).toBeTruthy()
    expect(within(dialog).getByRole('button', { name: '载入最新' })).toBeTruthy()
  })

  it('history state opens the editor and marks the row once, then clears the state', async () => {
    const { router } = renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    await act(async () => { await router.navigate('/admin/access-control?tab=policies', { state: { openPolicy: 2, highlightPolicy: 2 } }) })
    expect((await screen.findByRole('dialog')).textContent).toContain('禁止 BT')
    await waitFor(() => expect(router.state.location.state).toBeNull())
  })
})

describe('templates', () => {
  it('with no policy at all shows the catalog, including the honest card, and adding asks for a state', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ allow: [], block: [] }))
    vi.mocked(api.instantiateDestTemplate).mockResolvedValue(pol({}))
    renderAccess(<PoliciesTab {...props} />)
    expect(await screen.findByText('还没有访问策略')).toBeTruthy()
    expect(screen.getByTestId('template-unavailable').textContent).toContain('没有通用的博彩分类')
    fireEvent.click(within(screen.getByTestId('template-unavailable')).getByRole('button', { name: '新建列表' }))
    expect(props.onGoToLists).toHaveBeenCalledWith('new_custom')
    fireEvent.click(screen.getByRole('button', { name: '使用模板' }))
    const dialog = await screen.findByRole('dialog')
    const add = within(dialog).getByRole('button', { name: '添加' }) as HTMLButtonElement
    expect(add.disabled).toBe(true)
    fireEvent.click(within(dialog).getByRole('button', { name: '关闭' }))
  })

  it('instantiates with the state the admin picked, in the UI language', async () => {
    vi.mocked(api.instantiateDestTemplate).mockResolvedValue(pol({}))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建策略' }))
    fireEvent.click(await screen.findByText('BT 下载'))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '开启' }))
    fireEvent.click(within(dialog).getByRole('button', { name: '添加' }))
    await waitFor(() => expect(api.instantiateDestTemplate).toHaveBeenCalledWith('bittorrent', 'on', expect.any(String)))
  })

  it('a taken policy name on instantiate says so in the chooser, apart from "already added"', async () => {
    const taken = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_name_taken' } } })
    vi.mocked(api.instantiateDestTemplate).mockRejectedValue(taken)
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建策略' }))
    fireEvent.click(await screen.findByText('BT 下载'))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '开启' }))
    fireEvent.click(within(dialog).getByRole('button', { name: '添加' }))
    expect(await within(dialog).findByText(/已有同名策略/)).toBeTruthy()
  })
})
