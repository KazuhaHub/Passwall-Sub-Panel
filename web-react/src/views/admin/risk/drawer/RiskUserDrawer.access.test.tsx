// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useAuthStore } from '@/stores/auth'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as acApi from '@/api/accessControl'
import * as riskApi from '@/api/riskCenter'
import { confirmMock, renderAccess, setTestLang, whereIs } from '@/test/accessControlHarness'
import RiskUserDrawer from './RiskUserDrawer'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/riskCenter', async importOriginal => ({ ...await importOriginal<typeof import('@/api/riskCenter')>(), getRiskUser: vi.fn() }))
// The other four tabs have their own tests; here they are inert.
for (const t of ['Overview', 'Connections', 'Devices', 'Timeline']) {
  vi.doMock(`./${t}Tab`, () => ({ default: () => <p data-testid={`${t}-tab`} /> }))
}
vi.mock('./DrawerHeader', () => ({ default: () => <p data-testid="risk-drawer-header" /> }))
vi.mock('../../accessControl/sheets/AddExemptionDialog', () => ({
  default: ({ open, lockedUserId }: { open: boolean; lockedUserId?: number }) => open ? <p data-testid="add-exemption">{lockedUserId}</p> : null,
}))

const summary = {
  user: { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 2, group_name: 'Team A', enabled: true, traffic_limit_bytes: 0,
    access: { account_state: 'active', service_state: 'active', can_login: true, can_use_portal: true, can_subscribe: true, proxy_enabled: true } },
  attention: [], review: { dismissed: false, trusted: false, levels: {}, escalated: [] }, geo: null, signals: [], live: null, devices: [],
  device_window_hours: 24, devices_unavailable: false,
} as unknown as riskApi.RiskUserSummary

const view = (over: Partial<acApi.DestUserView> = {}): acApi.DestUserView => ({
  user_id: 7, upn: 'alice', exemption: null, group: { id: 2, name: 'Team A', mode: 'open', stage: null },
  hits_available: null, recent_hits: null, usage_available: null, usage_nodes: null, ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(riskApi.getRiskUser).mockResolvedValue(summary)
  vi.mocked(acApi.getDestUser).mockResolvedValue(view())
  vi.mocked(acApi.getDestStatus).mockResolvedValue({ apply_eta_ms: 0, nodes: [], generation: 0, published_generation: 0, paused: false, publish_error: null, policy_notes: {}, totals: {} } as unknown as acApi.DestStatus)
})

const tabs = () => screen.getAllByRole('tab').map(t => t.textContent)

describe('the access tab', () => {
  it.each(['risk', 'users', 'access'] as const)('is the fifth tab for an admin on host %s', async host => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host={host} />)
    await screen.findByTestId('risk-drawer-header')
    expect(tabs()).toHaveLength(5)
    expect(tabs()[4]).toBe('访问')
  })

  it('is absent for an operator', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />, { role: 'operator' })
    await screen.findByTestId('risk-drawer-header')
    expect(tabs()).toHaveLength(4)
  })

  it('reads the account only after the tab is chosen', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="risk" />)
    await screen.findByTestId('risk-drawer-header')
    expect(acApi.getDestUser).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('tab', { name: '访问' }))
    expect(await screen.findByText('Team A')).toBeTruthy()
    expect(acApi.getDestUser).toHaveBeenCalledTimes(1)
    expect(screen.getByText('不限')).toBeTruthy()
    expect(screen.getByText('未豁免')).toBeTruthy()
  })

  it('names the allowlist the group is under, in the group words, and the unrestricted one by what it is', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view({ group: { id: 2, name: 'Team A', state: 'report', state_changed_at: Date.now() - 3600_000, mode: 'allowlist', stage: null } }))
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText('白名单 · 仅报告 · 第 1 天')).toBeTruthy()
    expect(screen.queryByText('不限')).toBeNull()
  })

  it('host access opens on it', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText('Team A')).toBeTruthy()
    expect(screen.getByRole('tab', { name: '访问' }).getAttribute('aria-selected')).toBe('true')
  })

  it('opens the exemption dialog locked to this account', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    fireEvent.click(await screen.findByRole('button', { name: '豁免此账号…' }))
    expect((await screen.findByTestId('add-exemption')).textContent).toBe('7')
  })

  it('lifts an exemption after asking, through the same mutation as the sheet', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view({ exemption: {
      user_id: 7, upn: 'alice', reason: 'debugging', created_by: 1, created_by_upn: 'root', created_at: Date.now() - 3_600_000,
      expires_at: null, expired: false,
    } }))
    vi.mocked(acApi.deleteDestExemption).mockResolvedValue()
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText(/豁免：debugging · 由 root/)).toBeTruthy()
    expect(screen.getByText('访问策略不对这个账号生效')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '取消豁免' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '取消 alice 的豁免？' })))
    await waitFor(() => expect(acApi.deleteDestExemption).toHaveBeenCalledWith(7))
  })

  it('shows a tab-local error with a retry', async () => {
    vi.mocked(acApi.getDestUser).mockRejectedValueOnce(new Error('x'))
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText('读取这个账号的访问信息失败')).toBeTruthy()
    expect(screen.getAllByRole('tab')).toHaveLength(5)
  })

  it('scrolls the tabs on a phone only when all five show', async () => {
    const mm = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
    try {
      renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />, { role: 'operator' })
      await screen.findByTestId('risk-drawer-header')
      expect(document.querySelector('.MuiTabs-scrollableX')).toBeNull()
      cleanup()
      renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />)
      await screen.findByTestId('risk-drawer-header')
      expect(document.querySelector('.MuiTabs-scrollableX')).not.toBeNull()
    } finally { window.matchMedia = mm }
  })

  it('falls back to the overview when the permission to see the access tab goes while it is selected', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    await screen.findByText('Team A')
    act(() => { useAuthStore.setState({ role: 'operator' }) })
    await waitFor(() => expect(tabs()).toHaveLength(4))
    expect(screen.getByRole('tab', { name: '概览' }).getAttribute('aria-selected')).toBe('true')
  })
})

describe('the access tab: the hits of this account (S16, 2c)', () => {
  const NOW = Date.now()
  const hits = (over: Partial<acApi.DestUserView> = {}) => view({
    hits_available: true,
    recent_hits: {
      days: 3,
      items: [
        { source: 'p1', source_name: '禁止发信', action: 'block', count: 214, top_dests: ['smtp.gmail.com', 'mail.qq.com'], panels: [5], last_at: NOW - 3_600_000 },
        { source: 'p3', source_name: '加密货币交易所', action: 'observe', count: 1037, top_dests: ['www.binance.com'], panels: [5, 6], last_at: NOW - 600_000 },
        { source: 'p9', source_name: null, action: 'block', count: 4, top_dests: ['tracker.example.org'], panels: [6], last_at: NOW - 86_400_000 },
        { source: 's4', source_name: '东京 01', action: 'block', count: 2, top_dests: [], panels: [5], last_at: NOW - 86_400_000 },
        { source: 's8', source_name: null, action: 'block', count: 1, top_dests: [], panels: [5], last_at: NOW - 86_400_000 },
      ],
    },
    ...over,
  })
  const nodes = [{ panel_id: 5, panel_name: '东京 01' }, { panel_id: 6, panel_name: '洛杉矶 02' }]
  beforeEach(() => {
    vi.mocked(acApi.getDestStatus).mockResolvedValue({ apply_eta_ms: 0, nodes, generation: 0, published_generation: 0, paused: false, publish_error: null, policy_notes: {}, totals: {} } as unknown as acApi.DestStatus)
  })
  const open = () => renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)

  it('lists each rule that was hit: its action, the count, the destinations most hit and the nodes they were hit on', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(hits())
    open()
    const section = await screen.findByTestId('access-hits')
    expect(section.textContent).toContain('过去 3 天命中')
    const rows = Array.from(section.querySelectorAll('[data-testid="access-hit"]'))
    expect(rows).toHaveLength(5)
    expect(rows[0].textContent).toContain('禁止发信')
    expect(rows[0].textContent).toContain('214')
    expect(rows[0].querySelector('[data-state="block"]')?.textContent).toBe('拦截')
    await waitFor(() => expect(rows[0].textContent).toContain('smtp.gmail.com · mail.qq.com · 东京 01'))
    // Report-only hits are what the rule WOULD have blocked, not a block.
    expect(rows[1].querySelector('[data-state="observe"]')?.textContent).toBe('本应拦截')
    expect(rows[1].textContent).toContain('1,037')
    await waitFor(() => expect(rows[1].textContent).toContain('www.binance.com · 东京 01、洛杉矶 02'))
    expect(rows[2].textContent).toContain('已删除的策略')
    expect(rows[3].textContent).toContain('来源规则 · 东京 01')
    expect(rows[4].textContent).toContain('已删除的来源规则')
  })

  it('the window is the days the server answered (held to the retention), in the words', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(hits({ recent_hits: { days: 1, items: [] } }))
    open()
    expect(await screen.findByText('过去 1 天没有命中')).toBeTruthy()
  })

  it('view all goes to the records filtered to this account over that window', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(hits())
    const { router } = open()
    fireEvent.click(await screen.findByRole('button', { name: '在记录中查看全部' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_user=7&rec_since=3d'))
  })

  it('no node of the account records: say that, never "no hits", and offer the node coverage', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view({ hits_available: false, recent_hits: { days: 7, items: [] } }))
    const { router } = open()
    expect(await screen.findByText('这个账号所在的节点都不记录命中')).toBeTruthy()
    expect(screen.queryByText(/没有命中/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '节点覆盖' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?sheet=nodes'))
  })

  it('a server without the hit store has no hit section at all', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view())
    open()
    await screen.findByText('Team A')
    expect(screen.queryByTestId('access-hits')).toBeNull()
    expect(screen.queryByText(/命中/)).toBeNull()
  })

  it('in English', async () => {
    setTestLang('en-US')
    vi.mocked(acApi.getDestUser).mockResolvedValue(hits())
    open()
    const section = await screen.findByTestId('access-hits')
    expect(section.textContent).toContain('Hits in the past 3 days')
    expect(screen.getByRole('button', { name: 'View all in records' })).toBeTruthy()
    expect(section.textContent).toContain('Would be blocked')
  })
})

describe('the access tab: per-site usage of this account (S16, tier B)', () => {
  const nodes = [{ panel_id: 5, name: '东京 01' }]
  const usage = (over: Partial<acApi.DestUsage> = {}): acApi.DestUsage => ({
    effective_days: 1, nodes, items: [{ site: 'google.com', count: 1204 }, { site: '(ip)', count: 40 }],
    others: { sites: 3, count: 12 }, total: 1256, dropped_in_range: 0, ...over,
  })
  const open = () => renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
  const available = (over: Partial<acApi.DestUserView> = {}) => view({ usage_available: true, usage_nodes: nodes, ...over })
  beforeEach(() => {
    vi.mocked(acApi.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_usage_retention_days: 7 } as never })
    vi.mocked(acApi.postDestUserUsage).mockResolvedValue(usage())
  })

  it('reads nothing until the button is clicked, and says that the view is audited', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    open()
    const section = await screen.findByTestId('access-usage')
    expect(section.textContent).toContain('按网站用量')
    expect(section.textContent).toContain('只有管理员能查看，每次查看都会记入管理审计日志。')
    expect(acApi.postDestUserUsage).not.toHaveBeenCalled()
    fireEvent.click(within(section).getByRole('button', { name: '查看用量' }))
    await waitFor(() => expect(acApi.postDestUserUsage).toHaveBeenCalledTimes(1))
    expect(acApi.postDestUserUsage).toHaveBeenCalledWith(7, { range: '24h' })
  })

  it('one click is one read: while it is pending the button cannot be clicked again and the range and node controls are off', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available({ usage_nodes: [{ panel_id: 5, name: '东京 01' }, { panel_id: 6, name: '洛杉矶 02' }] }))
    let release: (u: acApi.DestUsage) => void = () => {}
    vi.mocked(acApi.postDestUserUsage).mockImplementation(() => new Promise<acApi.DestUsage>(r => { release = r }))
    vi.mocked(acApi.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_usage_retention_days: 3 } as never })
    open()
    const button = await screen.findByRole('button', { name: '查看用量' }) as HTMLButtonElement
    const days = await screen.findByRole('button', { name: '3 天' }) as HTMLButtonElement
    fireEvent.click(button)
    fireEvent.click(button)
    await waitFor(() => expect(button.disabled).toBe(true))
    expect(acApi.postDestUserUsage).toHaveBeenCalledTimes(1)
    expect(days.disabled).toBe(true)
    expect(screen.getByRole('combobox', { name: '节点' }).getAttribute('aria-disabled')).toBe('true')
    release(usage())
    await waitFor(() => expect(button.disabled).toBe(false))
  })

  it('lists the sites most used, then the rest as one line, the total and the node it came from', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    open()
    fireEvent.click(await screen.findByRole('button', { name: '查看用量' }))
    const section = await screen.findByTestId('access-usage')
    await waitFor(() => expect(section.textContent).toContain('google.com'))
    const rows = Array.from(section.querySelectorAll('[data-testid="usage-site"]'))
    expect(rows.map(r => r.textContent)).toEqual(['google.com1,204', '(ip)40'])
    expect(section.textContent).toContain('其他 3 个网站')
    expect(section.textContent).toContain('12')
    expect(section.textContent).toContain('共 1,256 次连接')
    expect(section.textContent).toContain('来自 东京 01')
  })

  it('offers 24 hours and min(7, retention) days: a retention of 3 offers 3, and choosing it reads again', async () => {
    vi.mocked(acApi.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_usage_retention_days: 3 } as never })
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    open()
    const section = await screen.findByTestId('access-usage')
    const days = await within(section).findByRole('button', { name: '3 天' })
    expect(within(section).getByRole('button', { name: '24 小时' })).toBeTruthy()
    expect(within(section).queryByRole('button', { name: '7 天' })).toBeNull()
    fireEvent.click(within(section).getByRole('button', { name: '查看用量' }))
    await waitFor(() => expect(acApi.postDestUserUsage).toHaveBeenCalledTimes(1))
    fireEvent.click(days)
    await waitFor(() => expect(acApi.postDestUserUsage).toHaveBeenLastCalledWith(7, { range: '3d' }))
  })

  it('says when the answer covers fewer days than asked, and how many usage rows are known to be missing', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    vi.mocked(acApi.postDestUserUsage).mockResolvedValue(usage({ effective_days: 5, dropped_in_range: 8 }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '查看用量' }))
    expect(await screen.findByText('有 8 行用量因节点或面板的限制没有保存，实际次数可能更多。')).toBeTruthy()
  })

  it('an empty range says so', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    vi.mocked(acApi.postDestUserUsage).mockResolvedValue(usage({ items: [], others: { sites: 0, count: 0 }, total: 0 }))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '查看用量' }))
    expect(await screen.findByText('这段时间没有记录')).toBeTruthy()
  })

  it('with several nodes the usage can be narrowed to one, which is sent as panel_id', async () => {
    const two = [{ panel_id: 5, name: '东京 01' }, { panel_id: 6, name: '洛杉矶 02' }]
    vi.mocked(acApi.getDestUser).mockResolvedValue(available({ usage_nodes: two }))
    open()
    const select = await screen.findByLabelText('节点')
    fireEvent.mouseDown(select)
    fireEvent.click(await screen.findByRole('option', { name: '洛杉矶 02' }))
    fireEvent.click(screen.getByRole('button', { name: '查看用量' }))
    await waitFor(() => expect(acApi.postDestUserUsage).toHaveBeenCalledWith(7, { range: '24h', panel_id: 6 }))
  })

  it('no node of the account records usage: says so, offers no read, never calls the endpoint', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view({ usage_available: false, usage_nodes: [] }))
    open()
    expect(await screen.findByText('这个账号所在的节点都没有开启按网站用量')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '查看用量' })).toBeNull()
    expect(acApi.postDestUserUsage).not.toHaveBeenCalled()
  })

  it('a failed read is said in the section with a retry', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    vi.mocked(acApi.postDestUserUsage).mockRejectedValueOnce(new Error('x'))
    open()
    fireEvent.click(await screen.findByRole('button', { name: '查看用量' }))
    expect(await screen.findByText('读取用量失败')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(acApi.postDestUserUsage).toHaveBeenCalledTimes(2))
  })

  it('in English the range words follow the count', async () => {
    setTestLang('en-US')
    vi.mocked(acApi.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_usage_retention_days: 1 } as never })
    vi.mocked(acApi.getDestUser).mockResolvedValue(available())
    open()
    const section = await screen.findByTestId('access-usage')
    expect(await within(section).findByRole('button', { name: '24 hours' })).toBeTruthy()
    // A retention of one day is the 24 hours: no second button for the same window.
    expect(within(section).queryByRole('button', { name: '1 day' })).toBeNull()
    setTestLang('zh-CN')
  })
})
