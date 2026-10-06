/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import type { RiskUserSummary } from '@/api/riskCenter'
import ConfirmHost, { confirm } from '@/components/ConfirmHost'
import RiskUserDrawer, { riskDrawerZIndex } from './RiskUserDrawer'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
// t over the REAL zh-CN bundles, so a key the drawer asks for but the bundle
// lacks shows up as its raw key instead of passing on a defaultValue.
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
      const raw = dict.current[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN' },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import zhCommon from '@/locales/zh-CN/common.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = {
  ...flatten(zh as Nested),
  ...Object.fromEntries(Object.entries(flatten(zhCommon as Nested)).map(([k, v]) => [`common:${k}`, v])),
}

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

const TAKEN = '2026-09-28T10:00:05Z'
const TAKEN_MS = Date.parse(TAKEN)
const JUDGED_MS = 1_790_000_000_000

const EV = {
  v: 2, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
  coverage: { placed: 0, unplaced: 0, region_known: 0, city_known: 0 }, networks: 0,
  spread: { countries: 0, regions: 0, region_country: '', cities: 0, city_country: '' },
  why: { code: 'idle_none', scope: 'city', tol: { countries: 1, regions: 1, cities: 2 }, flag_after: 3,
    clear_after: 6, min_placed_ratio: 0.5 },
}

const NO_REVIEW = {
  dismissed: false, dismissed_at_ms: 0, dismissed_by: 0, dismissed_by_upn: '', note: '', levels: {},
  reopened: false, lapsed: false, escalated: [], trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '',
}

const ACTIVE = { account_state: 'active', service_state: 'active', can_login: true, can_use_portal: true,
  can_subscribe: true, proxy_enabled: true }

function summary(
  over: Omit<Partial<RiskUserSummary>, 'user'> & { user?: Partial<RiskUserSummary['user']> } = {},
): RiskUserSummary {
  const { user, ...rest } = over
  return {
    user: { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 2, group_name: 'Team A',
      enabled: true, traffic_limit_bytes: 0, access: ACTIVE, ...user },
    attention: [{ source: 'devices', level: 'suspect' }],
    review: NO_REVIEW,
    geo: {
      user_id: 7, upn: 'alice', state: 'idle', reason: 'no connections', tier: '', flagged: false, places: [],
      live_ips: 0, concurrent_ips: 0, excluded_ips: 0, complete: true, over_streak: 0, under_streak: 0,
      ban_streak: 0, evidence: EV, updated_at_ms: JUDGED_MS, stale: false,
    },
    signals: [
      { kind: 'sub_spread', state: 'exempt', code: 'trusted', evidence: null, updated_at_ms: JUDGED_MS, stale: false },
      { kind: 'devices', state: 'suspect', code: 'over_building', updated_at_ms: JUDGED_MS, stale: true,
        evidence: { v: 1, window_days: 7, window_start: '2026-09-21', min_days: 3, max_devices: 3, recurrent: 2,
          distinct: 4, devices: [], fetches_with_hwid: 10, fetches_without: 0, clients: [] } },
    ],
    live: {
      snapshot: { taken_at: TAKEN, source: 'poll', age_seconds: 42, stale: false, stale_after_seconds: 900,
        panels_asked: 1, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0, users: 1, connections: 1,
        truncated: 0 },
      refresh: { cooldown_seconds: 30, available_in_seconds: 0 },
      device_window_hours: 24, devices_unavailable: false, panels: [{ id: 1, name: 'P1' }],
      items: [{ user_id: 7, upn: 'alice', display_name: 'Alice', stale_addresses: 0, unread_panels: 0, connections: [
        { panel_id: 1, panel_name: 'P1', node: 'n1', source_key: '198.51.100.20', ip: '198.51.100.20', exclusion: '',
          seen_at: 1_790_000_000, region: { country_code: 'CN', country: 'China', region: 'Guangdong',
            region_code: 'GD', city: 'Shenzhen' }, devices: [] },
      ] }],
      total: 1, page: 1, page_size: 1,
    },
    devices: [{ label: 'iPhone 16', device_id4: 'ab12', client_type: 'mihomo', ua: 'ClashMeta/1.19', fetches: 12,
      first_at_ms: JUDGED_MS - 3_600_000, last_at_ms: JUDGED_MS, sources: ['198.51.100.20'], sources_more: 2 }],
    device_window_hours: 24,
    devices_unavailable: false,
    ...rest,
  } as RiskUserSummary
}

const bob = () => summary({ user: { id: 8, upn: 'bob', display_name: 'Bob' } })

const history = {
  items: [{ user_id: 7, upn: 'alice', display_name: 'Alice', panel_id: 1, panel_name: 'P1', node: 'n1',
    source_key: '203.0.113.9', ip: '203.0.113.9', exclusion: '', region: null, first_seen_ms: 1_000,
    last_seen_ms: 2_000, count: 3 }],
  total: 1, page: 1, page_size: 200,
}

// Two of the account's 120 records: a verdict with its evidence, and an
// admin's trust, which stores no evidence to show.
const flags = {
  items: [
    { id: 1, user_id: 7, upn: 'alice', display_name: 'Alice', source: 'devices', event: 'enter_suspect',
      level: 'suspect', prev_level: '', state: 'suspect', code: 'over_building', at_ms: JUDGED_MS,
      params: { v: 1, window_days: 7, window_start: '2026-09-21', min_days: 3, max_devices: 3, recurrent: 2,
        distinct: 4, fetches_with_hwid: 10, fetches_without: 0, clients: [],
        devices: [{ label: 'iPad Air', hwid4: 'cd34', days: 2, last_ms: JUDGED_MS, client: '', recurrent: false }] } },
    { id: 2, user_id: 7, upn: 'alice', display_name: 'Alice', source: 'review', event: 'trusted', level: '',
      prev_level: '', state: '', code: 'trusted', params: null, at_ms: JUDGED_MS - 60_000 },
  ],
  total: 120, page: 1, page_size: 50,
}

const logins = { items: [{ id: 9, user_id: 7, upn: 'alice', method: 'local', outcome: 'success', ip: '192.0.2.44',
  at: '2026-09-28T09:00:00Z' }], total: 1 }

function httpError(status: number, data: unknown) {
  return new AxiosError('failed', 'ERR_BAD_REQUEST', undefined, undefined,
    { status, data, statusText: '', headers: {}, config: {} } as AxiosResponse)
}

function serve(user: () => RiskUserSummary | Error = () => summary()) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/users/7') {
      const u = user()
      if (u instanceof Error) throw u
      return { data: u }
    }
    if (url === '/admin/risk-center/users/8') return { data: bob() }
    if (url.startsWith('/admin/traffic/user/')) {
      return { data: { user_id: 7, permanent_total_bytes: 0, period_used_bytes: 2 * 1024 ** 3, today_used_bytes: 0 } }
    }
    if (url === '/admin/risk-center/connections') return { data: history }
    if (url === '/admin/risk-center/flags') return { data: flags }
    if (url === '/admin/auth-events') return { data: logins }
    if (url === '/admin/settings/geoip/status') throw new Error('not needed')
    throw new Error(`unexpected GET ${url}`)
  })
}

function tree(userId: number | null, onClose = vi.fn(), host: 'risk' | 'users' | 'access' = 'risk') {
  return (
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <RiskUserDrawer userId={userId} onClose={onClose} host={host} />
      </ThemeProvider>
    </MemoryRouter>
  )
}

function mount(userId: number | null, onClose = vi.fn(), host: 'risk' | 'users' | 'access' = 'risk') {
  return render(tree(userId, onClose, host), { wrapper: queryWrapper(makeTestQueryClient()) })
}

const header = async () => screen.findByTestId('risk-drawer-header')
const selectedTab = () => screen.getAllByRole('tab').find(el => el.getAttribute('aria-selected') === 'true')?.textContent

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('RiskUserDrawer', () => {
  it('stacks above dialogs, so it can open from the Users edit dialog', () => {
    expect(riskDrawerZIndex(theme)).toBeGreaterThan(theme.zIndex.modal)
  })

  it('stays below a confirm raised while it is open, so the confirm can be answered', async () => {
    // The policy page's leave guard: a route link in the drawer (查看用量趋势,
    // the logs links) leaves a dirty 策略 tab, and the guard asks through the
    // app's one confirm. Beneath the drawer's backdrop it could not be
    // clicked, and a click meant for it closed the drawer instead.
    serve()
    render(
      <MemoryRouter>
        <ThemeProvider theme={theme}>
          <RiskUserDrawer userId={7} onClose={vi.fn()} host="risk" />
          <ConfirmHost />
        </ThemeProvider>
      </MemoryRouter>,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    await header()

    act(() => { void confirm({ title: 'leave-guard', message: 'unsaved' }) })

    const asked = (await screen.findByText('leave-guard')).closest('.MuiDialog-root')!
    const drawer = document.querySelector('.MuiDrawer-root')!
    const z = (el: Element) => Number(getComputedStyle(el).zIndex)
    expect(z(drawer)).toBe(riskDrawerZIndex(theme))
    expect(z(asked)).toBeGreaterThan(z(drawer))
  })

  it.each([
    ['geo_auto', '异地自动暂停'],
    ['geo_anomaly', '异地人工暂停'],
    ['service_manual', '代理服务暂停'],
  ])('names the %s hold in the header', async (reason, label) => {
    serve(() => summary({ user: { service_disabled_reason: reason, service_disabled_at_ms: JUDGED_MS,
      service_disable_detail: 'call us', access: { ...ACTIVE, service_state: 'manual_suspended', proxy_enabled: false } } }))
    mount(7)
    const h = await header()
    expect(within(h).getByText('alice')).toBeTruthy()
    expect(within(h).getByText(label)).toBeTruthy()
    const panelTz = useSiteStore.getState().timezone
    expect(within(h).getByText(`暂停于 ${formatMsDualTz(JUDGED_MS, panelTz)}`, { exact: false })).toBeTruthy()
    expect(within(h).getByText('call us', { exact: false })).toBeTruthy()
  })

  it('a missing account says so', async () => {
    serve(() => httpError(404, { error: 'Not found' }))
    mount(7)
    expect(await screen.findByText('用户不存在或已删除')).toBeTruthy()
  })

  it('an unwired risk center says so', async () => {
    serve(() => httpError(503, { error: 'unavailable' }))
    mount(7)
    expect(await screen.findByText('本部署未启用风控中心。')).toBeTruthy()
  })

  it('a failed read offers a retry', async () => {
    let fail = true
    serve(() => (fail ? httpError(500, { error: 'boom' }) : summary()))
    mount(7)
    expect(await screen.findByText('读取失败：boom')).toBeTruthy()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await header()).toBeTruthy()
  })

  it('概览: the five detectors in order, trust read as trust, stale verdicts marked, the geo reason always', async () => {
    serve()
    mount(7)
    await header()
    const rows = screen.getAllByTestId('detector-row')
    expect(rows.map(r => within(r).getByTestId('detector-title').textContent))
      .toEqual(['异地并发', '订阅多地', '设备数', '用量变化', '登录国家'])

    // Geo: "no data" beside the reason that says why, never a bare chip.
    expect(within(rows[0]).getByText('无数据')).toBeTruthy()
    expect(within(rows[0]).getByText('此刻没有连接')).toBeTruthy()
    const panelTz = useSiteStore.getState().timezone
    expect(within(rows[0]).getByText(`最后判定 ${formatMsDualTz(JUDGED_MS, panelTz)}`)).toBeTruthy()
    expect(within(rows[1]).getByText('已信任')).toBeTruthy()
    expect(within(rows[2]).getByText('疑似')).toBeTruthy()
    expect(within(rows[2]).getByText('超过新鲜度未重新判定，不计入待处理')).toBeTruthy()
    expect(within(rows[0]).queryByText('超过新鲜度未重新判定，不计入待处理')).toBeNull()
    expect(within(rows[3]).getByText('尚未计算')).toBeTruthy()
  })

  it('概览: the review and trust lines name the admin, by id once gone', async () => {
    serve(() => summary({ review: { ...NO_REVIEW, dismissed: true, dismissed_at_ms: JUDGED_MS, dismissed_by: 3,
      dismissed_by_upn: '', note: 'known traveller', reopened: true, escalated: ['devices'],
      trusted: true, trusted_at_ms: JUDGED_MS, trusted_by: 4, trusted_by_upn: 'root' } }))
    mount(7)
    await header()
    const panelTz = useSiteStore.getState().timezone
    const at = formatMsDualTz(JUDGED_MS, panelTz)
    expect(screen.getByText(`由 #3 于 ${at} 忽略`)).toBeTruthy()
    expect(screen.getByText('备注：known traveller')).toBeTruthy()
    expect(screen.getByText('忽略后再次升级：设备数')).toBeTruthy()
    expect(screen.getByText(`由 root 于 ${at} 设为信任`)).toBeTruthy()
  })

  it('连接: one two-line list, live addresses online and last seen at the snapshot', async () => {
    serve()
    mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '连接' }))

    // The live entry shows at once; the history's arrives with its read.
    await waitFor(() => expect(screen.getAllByTestId('conn-entry')).toHaveLength(2))
    const entries = screen.getAllByTestId('conn-entry')
    const panelTz = useSiteStore.getState().timezone
    const [online, offline] = entries
    expect(within(online).getByText('198.51.100.20')).toBeTruthy()
    expect(within(online).getByText('在线')).toBeTruthy()
    expect(within(online).getByText(`最近 ${formatMsDualTz(TAKEN_MS, panelTz)}`, { exact: false })).toBeTruthy()
    expect(within(offline).getByText('203.0.113.9')).toBeTruthy()
    expect(within(offline).queryByText('在线')).toBeNull()
    expect(within(offline).getByText(`最近 ${formatMsDualTz(2_000, panelTz)}`, { exact: false })).toBeTruthy()
    // One account's history, newest first, in one page.
    const call = api.get.mock.calls.find(([u]) => u === '/admin/risk-center/connections')
    expect(call?.[1]?.params).toMatchObject({ user_id: 7, page: 1, page_size: 200, sort_by: 'last_seen', sort_dir: 'desc' })
    // No refresh here: the Live tab owns the fleet-wide rationed refresh.
    expect(screen.queryByRole('button', { name: '立即刷新' })).toBeNull()
  })

  // The node is 3X-UI's raw id, explained as it is on the Live tab. The
  // upstream time of a live source is the panel's scan, the same for every
  // address still connected, never when this one was last used.
  it('连接: the node id says what it is, and a live source is still connected as of the panel’s scan', async () => {
    serve()
    mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '连接' }))
    await waitFor(() => expect(screen.getAllByTestId('conn-entry')).toHaveLength(2))
    const [online] = screen.getAllByTestId('conn-entry')
    const panelTz = useSiteStore.getState().timezone
    const tips = async () => (await screen.findAllByRole('tooltip')).map(el => el.textContent ?? '')

    fireEvent.mouseOver(within(online).getByText('n1'))
    expect(await tips()).toContain('3X-UI 的节点标识，原样显示')

    fireEvent.mouseOver(within(online).getByText(`最近 ${formatMsDualTz(TAKEN_MS, panelTz)}`, { exact: false }))
    await waitFor(async () => expect((await tips()).some(tip =>
      tip.includes(`仍有连接（截至 ${formatMsDualTz(1_790_000_000_000, panelTz)}）`)
      && tip.includes('这里是面板扫描的时间，不是这个地址最后一次使用的时间'))).toBe(true))
    expect(screen.queryByText(/面板时钟/)).toBeNull()
  })

  it('设备: two-line entries and an exact link to the sub logs', async () => {
    serve()
    mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '设备' }))

    const [entry] = await screen.findAllByTestId('device-entry')
    expect(within(entry).getByText('iPhone 16')).toBeTruthy()
    expect(within(entry).getByText('mihomo')).toBeTruthy()
    expect(within(entry).getByText('拉取 12 次')).toBeTruthy()
    expect(within(entry).getByText('198.51.100.20', { exact: false })).toBeTruthy()
    expect(within(entry).getByText('另有 2 个', { exact: false })).toBeTruthy()
    const link = screen.getByRole('link', { name: '在日志管理中查看全部订阅记录' })
    expect(link.getAttribute('href')).toBe('/admin/logs?tab=sub&user_id=7&upn=alice')
  })

  it('设备: an unreadable sub log says so instead of an empty list', async () => {
    serve(() => summary({ devices: [], devices_unavailable: true }))
    mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '设备' }))
    expect(await screen.findByText('订阅日志暂时读不到')).toBeTruthy()
    expect(screen.queryByText('这段时间没有订阅拉取')).toBeNull()
  })

  // A side panel is no place for the Records page's table: one entry per
  // record in two lines — when, which source, what changed; then why — with
  // the evidence one click away, one filter, and the whole list a link away.
  it('时间线: the account’s records as a two-line list, then its logins and an exact auth-log link', async () => {
    serve()
    mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '时间线' }))

    const entries = await screen.findAllByTestId('timeline-entry')
    expect(entries).toHaveLength(2)
    const first = within(entries[0])
    expect(first.getByText('设备数')).toBeTruthy()
    expect(first.getByText('进入疑似')).toBeTruthy()
    expect(first.getByText('共 4 台设备，其中常用 2 台（上限 3）')).toBeTruthy()
    // The exact time, in panel time, behind the short one.
    const panelTz = useSiteStore.getState().timezone
    fireEvent.mouseOver(first.getByTestId('timeline-time'))
    expect((await screen.findByRole('tooltip')).textContent).toBe(formatMsDualTz(JUDGED_MS, panelTz))

    // The evidence, drawn as on the Records page, without the sentence twice.
    fireEvent.click(first.getByRole('button', { name: '查看证据' }))
    const detail = within(first.getByTestId('record-detail'))
    expect(detail.getByText('iPad Air')).toBeTruthy()
    expect(detail.getByRole('button', { name: '原始数据' })).toBeTruthy()
    expect(screen.getAllByText('共 4 台设备，其中常用 2 台（上限 3）')).toHaveLength(1)
    // A record without params has nothing to expand.
    expect(within(entries[1]).getByText('管理员处理')).toBeTruthy()
    expect(within(entries[1]).queryByRole('button', { name: '查看证据' })).toBeNull()

    // No table, no time range, no filters behind a button: those are the
    // Records page's, one link away, filtered to this account.
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByLabelText('开始（浏览器时间）')).toBeNull()
    expect(screen.queryByRole('button', { name: '更多筛选' })).toBeNull()
    expect(screen.getByText('仅显示最近 2 条（共 120 条）')).toBeTruthy()
    const all = screen.getByRole('link', { name: '在记录中查看全部' })
    expect(all.getAttribute('href')).toBe('/admin/risk?tab=records&rec_user=7')

    // The one filter: the source, read for this account only.
    const flagParams = () => api.get.mock.calls.filter(([u]) => u === '/admin/risk-center/flags').map(([, c]) => c?.params)
    expect(flagParams()[0]).toMatchObject({ user_id: 7, page: 1, page_size: 50 })
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '来源' }))
    fireEvent.click(await screen.findByRole('option', { name: '管理员处理' }))
    await waitFor(() => expect(flagParams().at(-1)).toMatchObject({ user_id: 7, source: 'review' }))

    expect(await screen.findByText('192.0.2.44', { exact: false })).toBeTruthy()
    expect(screen.getByText('最近登录')).toBeTruthy()
    const link = screen.getByRole('link', { name: '在认证日志中查看' })
    expect(link.getAttribute('href')).toBe('/admin/logs?tab=auth&user_id=7&upn=alice')
  })

  it('Esc closes it', async () => {
    serve()
    const onClose = vi.fn()
    mount(7, onClose)
    const h = await header()
    fireEvent.keyDown(within(h).getByText('alice'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('the close button is labelled', async () => {
    serve()
    const onClose = vi.fn()
    mount(7, onClose)
    await header()
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('switching account starts again on 概览', async () => {
    serve()
    const r = mount(7)
    await header()
    fireEvent.click(screen.getByRole('tab', { name: '连接' }))
    expect(selectedTab()).toBe('连接')

    r.rerender(tree(8))
    await waitFor(() => expect(within(screen.getByTestId('risk-drawer-header')).getByText('bob')).toBeTruthy())
    expect(selectedTab()).toBe('概览')
  })

  it('another hold links to the Users page, except on the Users page', async () => {
    const blocked = () => summary({ user: { service_disabled_reason: 'blocked_client', service_disabled_at_ms: JUDGED_MS,
      access: { ...ACTIVE, service_state: 'blocked_client', proxy_enabled: false } } })
    serve(blocked)
    mount(7)
    await header()
    const link = screen.getByRole('link', { name: '在用户管理中处理' })
    expect(link.getAttribute('href')).toBe('/admin/users?q=alice')
    // Neither service action is offered for a hold the risk center did not write.
    expect(screen.queryByRole('button', { name: '恢复代理服务' })).toBeNull()
    expect(screen.queryByRole('button', { name: '暂停代理服务' })).toBeNull()
    cleanup()

    serve(blocked)
    mount(7, vi.fn(), 'users')
    await header()
    expect(screen.queryByRole('link', { name: '在用户管理中处理' })).toBeNull()
  })

  it('offers the service action and the review actions from the one matrix', async () => {
    serve()
    mount(7)
    await header()
    expect(screen.getByRole('button', { name: '暂停代理服务' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '忽略' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '更多操作' }))
    expect(await screen.findByRole('menuitem', { name: '信任此账号' })).toBeTruthy()
  })

  it('reads nothing while closed', () => {
    serve()
    mount(null)
    expect(api.get).not.toHaveBeenCalled()
  })
  it('loads access only after tab selection, without later-stage usage reads', async () => {
    serve(); const original = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string) => url === '/admin/dest/users/7' ? { data: { group: { id: 2, name: 'Students', mode: 'open', stage: '' }, exemption: null } } : original(url))
    mount(7); await header()
    expect(api.get.mock.calls.some(([url]) => url.startsWith('/admin/dest/'))).toBe(false)
    fireEvent.click(screen.getByRole('tab', { name: '访问' }))
    await screen.findByText('Students')
    expect(api.get.mock.calls.filter(([url]) => url === '/admin/dest/users/7')).toHaveLength(1)
    expect(api.get.mock.calls.some(([url]) => url.includes('usage='))).toBe(false)
  })
  it('starts the access host on access', async () => {
    serve(); api.get.mockImplementation(async (url: string) => ({ data: url.startsWith('/admin/dest/') ? { group: null, exemption: null } : summary() }))
    mount(7, vi.fn(), 'access'); await header()
    expect(selectedTab()).toBe('访问')
  })
  it('does not show or read destination access for operators', async () => {
    serve(); useAuthStore.setState({ role: 'operator' })
    mount(7, vi.fn(), 'access'); await header()
    expect(screen.queryByRole('tab', { name: '访问' })).toBeNull()
    expect(selectedTab()).toBe('概览')
    expect(api.get.mock.calls.some(([url]) => url.startsWith('/admin/dest/'))).toBe(false)
  })
  it('also offers access from the users host while keeping its overview default', async () => {
    serve(); mount(7, vi.fn(), 'users'); await header()
    expect(screen.getByRole('tab', { name: '访问' })).toBeTruthy()
    expect(selectedTab()).toBe('概览')
    expect(api.get.mock.calls.some(([url]) => url.startsWith('/admin/dest/'))).toBe(false)
  })
})
