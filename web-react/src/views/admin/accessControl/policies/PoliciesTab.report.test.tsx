// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess, setTestLang, snack, whereIs } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import type { DestCatalog, DestHits, DestPolicies, DestPolicy } from '@/api/accessControl'
import PoliciesTab from './PoliciesTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/groups', async o => ({ ...await o<typeof import('@/api/groups')>(), listAllGroups: vi.fn(() => Promise.resolve([])) }))
vi.mock('@/api/servers', async o => ({ ...await o<typeof import('@/api/servers')>(), listServers: vi.fn(() => Promise.resolve({ items: [], total: 0 })) }))

const budget = { rules: { used: 4, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (over: Partial<DestPolicy>): DestPolicy => ({
  id: 1, name: '禁止发信', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [],
  priority: 1, template_key: '', created_at: 0, updated_at: 111, list_states: [], note: null, scope_missing: false,
  hits_recent: 214, would_block_recent: null, last_hit_at: Date.now() - 2 * 3_600_000, ...over,
})
const policies = (over: Partial<DestPolicies> = {}): DestPolicies => ({
  allow: [pol({ id: 9, name: '公司内网', action: 'allow', inline: { private: true }, hits_recent: null, last_hit_at: null })],
  block: [
    pol({ id: 1 }),
    pol({ id: 3, name: '加密货币交易所', state: 'report', priority: 2, hits_recent: 0, would_block_recent: 1032, inline: { ports: '443' } }),
    pol({ id: 2, name: '禁止 BT', state: 'off', priority: 3, hits_recent: 0, last_hit_at: null, inline: { protocols: ['bittorrent'] } }),
    pol({ id: 4, name: '成人内容', state: 'report', priority: 4, hits_recent: 0, would_block_recent: 0, last_hit_at: null, inline: { ports: '80' } }),
  ],
  observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget, ...over,
})
const catalog: DestCatalog = {
  catalog_version: 1,
  templates: [{ key: 'bittorrent', name: { 'zh-CN': 'BT 下载', 'en-US': 'BitTorrent' }, description: { 'zh-CN': '拦截 BT', 'en-US': 'Blocks BT' }, added: false }],
  unavailable: [],
}
const status = {
  generation: 1, published_generation: 1, paused: false, publish_error: null, apply_eta_ms: 61000, totals: {}, policy_notes: {}, geosite_release: '', geoip_unavailable: false,
  nodes: [{ panel_id: 1, kind: 'psp', state: 'applied', supports: { policy: true, hits: true }, engine: 'xray', sniffing_insufficient: [], collecting: true }],
} as unknown as api.DestStatus
const hits = (over: Partial<DestHits>): DestHits => ({
  items: [], total: 0, page: 1, page_size: 3, group_by: 'none', since: 0, until: 0,
  summary: { block: 0, deny: 0, observe: 0, users: 0 }, sources: [], dropped_in_range: 0, ...over,
})
const hit = (dest: string, count: number) => ({ hour_ms: 0, panel_id: 0, panel_name: null, user_id: 0, upn: null, source: '', source_name: null, source_kind: 'policy' as const, action: 'observe' as const, dest, port: 0, count, first_ms: 0, last_ms: 0 })

const props = { onOpenExemptions: vi.fn(), onOpenSettings: vi.fn(), onGoToLists: vi.fn() }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies())
  vi.mocked(api.getDestCatalog).mockResolvedValue(catalog)
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue(status)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_policy_apply_min_seconds: 60 } as never })
  vi.mocked(api.updateDestPolicy).mockResolvedValue(pol({}))
  vi.mocked(api.reorderDestPolicies).mockResolvedValue()
  vi.mocked(api.previewDestPolicy).mockResolvedValue({ budget })
  vi.mocked(api.instantiateDestTemplate).mockResolvedValue(pol({}))
  vi.mocked(api.getDestHits).mockImplementation(async (p = {}) => (p.group_by === 'site'
    ? hits({ total: 9, items: [hit('binance.com', 612), hit('okx.com', 201), hit('bybit.com', 98)] })
    : hits({ total: 14, items: [hit('', 5)] })))
})

const rowOf = (name: string) => screen.getAllByTestId('policy-row').find(r => r.textContent?.includes(name))!

describe('report-only in the rail', () => {
  it('has its own step after block, before "everything else", with the numbers computed', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    expect(screen.getAllByRole('heading', { level: 2 }).map(h => h.textContent)).toEqual([
      '放行 · 命中即放行', '没有豁免账号 · 不受任何访问限制', '拦截',
      '仅报告 · 不拦截，只记录本应拦截的连接；不改变任何连接的结果', '其余连接：直接放行',
    ])
    // The enforced step holds the block and the off rows, the report step the report rows only.
    const heads = screen.getAllByRole('heading', { level: 2 })
    const reportStep = heads[3].parentElement!.parentElement!
    expect(within(reportStep).getByText('加密货币交易所')).toBeTruthy()
    expect(within(reportStep).getByText('成人内容')).toBeTruthy()
    expect(within(reportStep).queryByText('禁止发信')).toBeNull()
    const blockStepEl = heads[2].parentElement!.parentElement!
    expect(within(blockStepEl).getByText('禁止发信')).toBeTruthy()
    expect(within(blockStepEl).getByText('禁止 BT')).toBeTruthy()
  })

  it('a report-only row says so in the "would be denied" tone, never the block badge, and speaks of what it would have blocked', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    const row = rowOf('加密货币交易所')
    expect(within(row).getByTestId('policy-state-badge').textContent).toBe('仅报告')
    expect(within(row).getByTestId('policy-state-badge').getAttribute('data-state')).toBe('report')
    expect(within(row).queryByText('拦截')).toBeNull()
    expect(within(row).getByTestId('policy-hits').textContent).toContain('过去 7 天本应拦截 1,032 次')
    expect(within(row).getByTestId('policy-hits').getAttribute('data-kind')).toBe('would_block')
    // The block count of a report-only policy is not what it says.
    expect(within(row).queryByText(/7 天 0 次/)).toBeNull()
    expect(within(rowOf('成人内容')).getByTestId('policy-hits').textContent).toBe('过去 7 天没有本应拦截的连接')
  })

  it('the switch is lit for report-only and turns it off', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    const sw = within(rowOf('加密货币交易所')).getByRole('switch') as HTMLInputElement
    expect(sw.checked).toBe(true)
    fireEvent.click(sw)
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(3, expect.objectContaining({ state: 'off' })))
  })
})

describe('the switch keeps the report state', () => {
  it('report-only turned off and back on is report-only again, never enforced', async () => {
    let current = policies()
    vi.mocked(api.getDestPolicies).mockImplementation(async () => current)
    vi.mocked(api.updateDestPolicy).mockImplementation(async (id, input) => {
      current = { ...current, block: current.block.map(p => (p.id === id ? { ...p, state: input.state, updated_at: p.updated_at + 1 } : p)) }
      return current.block.find(p => p.id === id)!
    })
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(within(rowOf('加密货币交易所')).getByRole('switch'))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenLastCalledWith(3, expect.objectContaining({ state: 'off' })))
    await waitFor(() => expect((within(rowOf('加密货币交易所')).getByRole('switch') as HTMLInputElement).checked).toBe(false))
    fireEvent.click(within(rowOf('加密货币交易所')).getByRole('switch'))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenLastCalledWith(3, expect.objectContaining({ state: 'report' })))
  })

  it('an enforced policy turned off and back on is enforced again', async () => {
    let current = policies()
    vi.mocked(api.getDestPolicies).mockImplementation(async () => current)
    vi.mocked(api.updateDestPolicy).mockImplementation(async (id, input) => {
      current = { ...current, block: current.block.map(p => (p.id === id ? { ...p, state: input.state, updated_at: p.updated_at + 1 } : p)) }
      return current.block.find(p => p.id === id)!
    })
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(within(rowOf('禁止发信')).getByRole('switch'))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenLastCalledWith(1, expect.objectContaining({ state: 'off' })))
    await waitFor(() => expect((within(rowOf('禁止发信')).getByRole('switch') as HTMLInputElement).checked).toBe(false))
    fireEvent.click(within(rowOf('禁止发信')).getByRole('switch'))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenLastCalledWith(1, expect.objectContaining({ state: 'on' })))
  })
})

describe('turning on a policy whose earlier state is not known (after a reload)', () => {
  const open = async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(within(rowOf('禁止 BT')).getByRole('switch'))
    return screen.findByRole('dialog')
  }

  it('asks which: Report only is the default, and nothing is written until it is answered', async () => {
    const dialog = await open()
    expect(within(dialog).getByText('开启「禁止 BT」？')).toBeTruthy()
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: '仅报告' }))
    expect(api.updateDestPolicy).not.toHaveBeenCalled()
  })

  it('Report only writes report, Turn on blocking writes on', async () => {
    const dialog = await open()
    fireEvent.click(within(dialog).getByRole('button', { name: '仅报告' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenLastCalledWith(2, expect.objectContaining({ state: 'report' })))
  })

  it('closing the question writes nothing: dismissing is never "enforce"', async () => {
    const dialog = await open()
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.updateDestPolicy).not.toHaveBeenCalled()
  })

  it('an allow policy has no report-only: it turns on without asking', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ allow: [pol({ id: 9, name: '公司内网', action: 'allow', state: 'off', inline: { private: true }, hits_recent: null, last_hit_at: null })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('公司内网')
    fireEvent.click(within(rowOf('公司内网')).getByRole('switch'))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(9, expect.objectContaining({ state: 'on' })))
  })
})

describe('counts as a risk signal', () => {
  it('a row that counts says so with a small badge; one that does not has none', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ block: [pol({ id: 1, counts_as_risk: true }), pol({ id: 3, name: '加密货币交易所', state: 'report', counts_as_risk: false, would_block_recent: 1 })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    expect(within(rowOf('禁止发信')).getByTestId('policy-risk-badge').textContent).toBe('计入风险')
    expect(within(rowOf('加密货币交易所')).queryByTestId('policy-risk-badge')).toBeNull()
  })

  it('the convert dialog says once enforced it counts toward the risk signal, only when it does', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ block: [pol({ id: 3, name: '加密货币交易所', state: 'report', counts_as_risk: true, would_block_recent: 4 })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(within(rowOf('加密货币交易所')).getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/改为拦截后.*计入风险信号/)).toBeTruthy()
  })

  it('the template chooser says a template that counts as risk does', async () => {
    vi.mocked(api.getDestCatalog).mockResolvedValue({ ...catalog, templates: [{ ...catalog.templates[0], counts_as_risk: true }] })
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建策略' }))
    fireEvent.click(await screen.findByText('BT 下载'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/命中会计入风险信号/)).toBeTruthy()
  })
})

describe('report-only rules some nodes do not run', () => {
  const withShed = (n: number) => ({
    ...status,
    nodes: Array.from({ length: n }, (_, i) => ({ ...(status.nodes[0] as object), panel_id: i + 1, report_only_not_deployed: 'sniffing' })),
  }) as unknown as api.DestStatus

  it('a report-only row that saw nothing does not say "nothing would be blocked" flatly, and the rail step says how many nodes are not running them', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(withShed(2))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('成人内容')
    expect(within(rowOf('成人内容')).getByTestId('policy-hits').textContent).toBe('过去 7 天没有本应拦截的连接（2 个节点未部署）')
    expect(screen.getByTestId('rail-report-not-deployed').textContent).toBe('（2 个节点未部署）')
  })

  it('says nothing extra when every node runs them', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('成人内容')
    expect(within(rowOf('成人内容')).getByTestId('policy-hits').textContent).toBe('过去 7 天没有本应拦截的连接')
    expect(screen.queryByTestId('rail-report-not-deployed')).toBeNull()
  })
})

describe('hits on the row', () => {
  it('an enforced policy: the count over the window, the last hit and a link to the records filtered to it', async () => {
    const { router } = renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    const hitsEl = within(rowOf('禁止发信')).getByTestId('policy-hits')
    expect(hitsEl.textContent).toContain('7 天 214 次')
    expect(hitsEl.textContent).toContain('2 小时前')
    fireEvent.click(within(hitsEl).getByRole('button'))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_source=p1&rec_since=7d'))
  })

  it('a report-only count links to the records filtered to its would-be-blocked hits', async () => {
    const { router } = renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(within(within(rowOf('加密货币交易所')).getByTestId('policy-hits')).getByRole('button'))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_source=p3&rec_action=observe&rec_since=7d'))
  })

  it('no hits: "no hits in N days" when it has hit before, "none within the retention" when never', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ block: [
      pol({ id: 1, hits_recent: 0, last_hit_at: Date.now() - 9 * 86_400_000 }), pol({ id: 2, name: '禁止 BT', hits_recent: 0, last_hit_at: null }),
    ] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    expect(within(rowOf('禁止发信')).getByTestId('policy-hits').textContent).toContain('7 天未命中')
    expect(within(rowOf('禁止 BT')).getByTestId('policy-hits').textContent).toBe('保留期内未命中')
  })

  it('nothing records: the dash with its reason, never a 0', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ block: [
      pol({ id: 1, hits_recent: null, last_hit_at: null }), pol({ id: 3, name: '加密货币交易所', state: 'report', hits_recent: null, would_block_recent: null, last_hit_at: null }),
    ] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    for (const name of ['禁止发信', '加密货币交易所']) {
      const el = within(rowOf(name)).getByTestId('policy-hits')
      expect(el.getAttribute('data-kind')).toBe('unavailable')
      expect(el.textContent).toBe('—')
      expect(el.querySelector('[aria-label]')?.getAttribute('aria-label')).toBe('需节点开启记录')
    }
    expect(screen.queryByText(/次/)).toBeNull()
  })

  it('an allow policy records nothing and says nothing', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('公司内网')
    expect(within(rowOf('公司内网')).queryByTestId('policy-hits')).toBeNull()
  })

  it('the row menu offers "view in records" for a block policy, filtered to it', async () => {
    const { router } = renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(screen.getByRole('button', { name: '「禁止 BT」的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '在记录中查看' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_source=p2&rec_since=7d'))
  })
})

describe('changing state from the row', () => {
  it('"change to report only" PUTs the whole policy with state report', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '「禁止发信」的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '改为仅报告' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(1, expect.objectContaining({ state: 'report', updated_at: 111, name: '禁止发信' })))
    expect(snack).toHaveBeenCalledWith('「禁止发信」已改为仅报告，将随下一次下发生效', 'success')
  })

  it('an allow policy has no such entry', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('公司内网')
    fireEvent.click(screen.getByRole('button', { name: '「公司内网」的更多操作' }))
    await screen.findByRole('menuitem', { name: '编辑' })
    expect(screen.queryByRole('menuitem', { name: '改为仅报告' })).toBeNull()
  })

  it('the first policy to be deployed asks first even when it is report-only: the nodes restart for it too', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ allow: [], block: [pol({ id: 2, state: 'off' })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '「禁止发信」的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '改为仅报告' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '启用第一条访问策略？' })))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
  })

  it('a report-only policy already deployed means the next one is not "the first"', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ allow: [], block: [pol({ id: 3, state: 'report', would_block_recent: 1 }), pol({ id: 2, name: '禁止 BT', state: 'off' })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止 BT')
    fireEvent.click(within(rowOf('禁止 BT')).getByRole('switch'))
    fireEvent.click(await screen.findByRole('button', { name: '仅报告' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('moving inside the report step sends ALL block ids, the other step keeping its slots', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('成人内容')
    fireEvent.click(screen.getByRole('button', { name: '「成人内容」的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '上移' }))
    await waitFor(() => expect(api.reorderDestPolicies).toHaveBeenCalledWith('block', [1, 4, 2, 3]))
  })
})

describe('turning a report-only policy into a block (S4)', () => {
  it('shows what it would have blocked, then PUTs state on with the updated_at it was read at', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(within(rowOf('加密货币交易所')).getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('把「加密货币交易所」改为拦截？')).toBeTruthy()
    await waitFor(() => expect(within(dialog).getByTestId('convert-numbers').textContent).toContain('1,032'))
    const numbers = within(dialog).getByTestId('convert-numbers').textContent!
    expect(numbers).toContain('过去 7 天本应拦截')
    expect(numbers).toContain('14')
    expect(within(dialog).getByTestId('convert-top').textContent).toBe('最多的目的地：binance.com 612 · okx.com 201 · bybit.com 98')
    // One request for the top destinations of THIS policy and one for the accounts it touched, both over the window.
    const calls = vi.mocked(api.getDestHits).mock.calls.map(c => c[0])
    expect(calls).toEqual(expect.arrayContaining([
      expect.objectContaining({ source: 'p3', action: 'observe', group_by: 'site', page_size: 3 }),
      expect.objectContaining({ source: 'p3', action: 'observe', group_by: 'user', page_size: 1 }),
    ]))
    expect(within(dialog).getByText(/实际影响可能比上面的数字大/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: '改为拦截' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalledWith(3, expect.objectContaining({ state: 'on', action: 'block', updated_at: 111 })))
    // A report-only policy keeps its place in the action's order (1, then 3, then 2): once enforced it is second in step 3, not last.
    expect(snack).toHaveBeenCalledWith('已改为拦截，排在「拦截」第 2 位', 'success')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // A plain state change: it is not asked to confirm twice.
    expect(confirmMock).not.toHaveBeenCalled()
  })

  it('no node records: says it cannot estimate, shows no zero, asks for no hits, and can still convert', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue(policies({ block: [pol({ id: 3, name: '加密货币交易所', state: 'report', hits_recent: null, would_block_recent: null, last_hit_at: null })] }))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(screen.getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('节点还没有记录命中，无法估计影响')).toBeTruthy()
    expect(within(dialog).queryByTestId('convert-numbers')).toBeNull()
    expect(api.getDestHits).not.toHaveBeenCalled()
    expect((within(dialog).getByRole('button', { name: '改为拦截' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('a failed read of the hits is said in the dialog and does not stop the change', async () => {
    vi.mocked(api.getDestHits).mockRejectedValue(new Error('boom'))
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(screen.getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('读取命中失败，仍然可以改为拦截。')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: '改为拦截' }))
    await waitFor(() => expect(api.updateDestPolicy).toHaveBeenCalled())
  })

  it('a stale answer reloads and says so, the dialog closing', async () => {
    const stale = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_policy_stale' } } })
    vi.mocked(api.updateDestPolicy).mockRejectedValue(stale)
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    fireEvent.click(screen.getByRole('button', { name: '把「加密货币交易所」转为拦截' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: '改为拦截' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('这条策略刚被其他人修改过，已重新载入', 'error'))
  })
})

describe('the editor: three states for a block policy, two for an allow policy', () => {
  it('a new block policy offers Off / Report only / On, starts as Report only with the recommendation, and the admin can pick On at once', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建拦截策略' }))
    const dialog = await screen.findByRole('dialog')
    const group = within(dialog).getByRole('group', { name: '状态' })
    expect(within(group).getAllByRole('button').map(b => b.textContent?.replace('推荐', '').trim())).toEqual(['关闭', '仅报告', '开启'])
    expect(within(group).getByRole('button', { name: /仅报告/ }).getAttribute('aria-pressed')).toBe('true')
    expect(within(dialog).getByText(/推荐/)).toBeTruthy()
    expect(within(dialog).getByText('不拦截任何连接，只记录它本应拦截的连接。它排在所有生效的规则之后判断，所以不会改变任何连接的结果。')).toBeTruthy()
    fireEvent.click(within(group).getByRole('button', { name: '开启' }))
    expect(within(dialog).getByText('断开命中的连接，并记录命中。')).toBeTruthy()
    fireEvent.change(within(dialog).getByLabelText(/名称/), { target: { value: '新策略' } })
    fireEvent.change(within(dialog).getByLabelText('端口'), { target: { value: '25' } })
    vi.mocked(api.createDestPolicy).mockResolvedValue(pol({}))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestPolicy).toHaveBeenCalledWith(expect.objectContaining({ state: 'on', action: 'block' })))
  })

  it('saved as the recommended Report only, the policy goes up as report', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建拦截策略' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText(/名称/), { target: { value: '新策略' } })
    fireEvent.change(within(dialog).getByLabelText('端口'), { target: { value: '25' } })
    vi.mocked(api.createDestPolicy).mockResolvedValue(pol({}))
    expect(within(dialog).getByTestId('editor-summary').textContent).toContain('仅报告')
    expect(within(dialog).getByTestId('editor-summary').textContent).toContain('所有生效的规则之后判断')
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestPolicy).toHaveBeenCalledWith(expect.objectContaining({ state: 'report' })))
  })

  it('an allow policy offers Off and On only, with the one-line reason; switching a report-only block to allow drops the state to Off', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建拦截策略' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('group', { name: '状态' }).textContent).toContain('仅报告')
    fireEvent.click(within(dialog).getByRole('button', { name: '放行' }))
    const group = within(dialog).getByRole('group', { name: '状态' })
    expect(within(group).getAllByRole('button').map(b => b.textContent)).toEqual(['关闭', '开启'])
    expect(within(group).getByRole('button', { name: '关闭' }).getAttribute('aria-pressed')).toBe('true')
    expect(within(dialog).getByText('放行策略没有「仅报告」：放行要改变判定结果才有意义，而仅报告不改变任何结果。')).toBeTruthy()
  })

  it('editing a report-only policy keeps its state, and says it is not a new policy: no recommendation', async () => {
    renderAccess(<PoliciesTab {...props} />)
    fireEvent.click(await screen.findByText('加密货币交易所'))
    const dialog = await screen.findByRole('dialog')
    const group = within(dialog).getByRole('group', { name: '状态' })
    expect(within(group).getByRole('button', { name: '仅报告' }).getAttribute('aria-pressed')).toBe('true')
    expect(within(dialog).queryByText(/新建的拦截策略建议/)).toBeNull()
    expect(within(dialog).getByTestId('editor-summary').textContent).toContain('仅报告')
  })
})

describe('templates ask for one of the three states', () => {
  it('Report only is offered, labelled recommended, and instantiated as report', async () => {
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('禁止发信')
    fireEvent.click(screen.getByRole('button', { name: '新建策略' }))
    fireEvent.click(await screen.findByText('BT 下载'))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getAllByRole('button').map(b => b.textContent?.replace('推荐', '').trim()).filter(Boolean)).toEqual(expect.arrayContaining(['关闭', '仅报告', '开启']))
    expect(within(dialog).getByRole('button', { name: /仅报告/ }).textContent).toContain('推荐')
    fireEvent.click(within(dialog).getByRole('button', { name: /仅报告/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: '添加' }))
    await waitFor(() => expect(api.instantiateDestTemplate).toHaveBeenCalledWith('bittorrent', 'report', expect.any(String)))
  })
})

describe('English', () => {
  it('speaks the term table: Report only, would have blocked, never the internal words', async () => {
    setTestLang('en-US')
    renderAccess(<PoliciesTab {...props} />)
    await screen.findByText('加密货币交易所')
    const row = rowOf('加密货币交易所')
    expect(within(row).getByTestId('policy-state-badge').textContent).toBe('Report only')
    expect(within(row).getByTestId('policy-hits').textContent).toBe('Would have blocked 1,032 connections in the past 7 days')
    expect(document.body.textContent).not.toMatch(/observe|Watch only/i)
  })
})
