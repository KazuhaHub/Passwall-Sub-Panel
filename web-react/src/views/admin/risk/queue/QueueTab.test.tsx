/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { AxiosError, type AxiosResponse } from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { formatDualTz, formatMsDualTz } from '@/utils/datetime'
import type { GeoAnomaly } from '@/api/geoAnomalies'
import type { QueueCounts, QueueRow, QueueView } from '@/api/riskCenter'
import type { GeoIPStatus } from '@/api/settings'
import RiskUserDrawer from '../drawer/RiskUserDrawer'
import { useDrawerParam } from '../drawerParam'
import QueueTab from './QueueTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
// t over the REAL zh-CN bundles, so a key the tab asks for but the bundle
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

const NOW = Date.now()
const HELD_AT = NOW - 2 * 3_600_000
const NO_REVIEW = { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] }

const aliceGeo: GeoAnomaly = {
  user_id: 7, upn: 'alice', state: 'flagged', reason: 'stored', tier: 'region', flagged: true, places: ['CN'],
  live_ips: 3, concurrent_ips: 3, excluded_ips: 0, complete: true, over_streak: 4, under_streak: 0, ban_streak: 0,
  updated_at_ms: NOW - 60_000,
  evidence: {
    v: 3, stale: 0, networks: 2, excluded: { shared: 0, listed: 0, infra: 0, internal: 0 },
    coverage: { placed: 3, unplaced: 0, region_known: 3, city_known: 3 },
    spots: [
      { cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Shenzhen', n: 2 },
      { cc: 'CN', region: 'Hunan', rc: 'HN', city: 'Changsha', n: 1 },
    ],
    spread: { countries: 1, regions: 2, region_country: 'CN', cities: 2, city_country: 'CN', max_km: 640 },
    why: { code: 'flagged_sustained', tier: 'region', scope: 'city', tol: { countries: 1, regions: 1, cities: 2 },
      flag_after: 3, clear_after: 6, min_placed_ratio: 0.5 },
  },
}

// alice: flagged on locations and held by the detector. bob: devices
// suspect, a dismissal that lapsed. carol: sub_spread flagged, a dismissal
// that escalated since.
const alice: QueueRow = {
  user_id: 7, upn: 'alice', display_name: 'Alice', group_id: 1, group_name: 'Team A', level: 'flagged',
  auto_suspended: true, urgent: true, service_state: 'manual_suspended', service_disabled_reason: 'geo_auto',
  service_disabled_at_ms: HELD_AT,
  sources: [{ source: 'geo', level: 'flagged' }, { source: 'geo_auto', level: 'suspended' }],
  geo: aliceGeo, signals: [], changed_at_ms: NOW - 5 * 60_000, review: NO_REVIEW,
}
const bob: QueueRow = {
  user_id: 8, upn: 'bob', display_name: 'Bob', group_id: 1, group_name: 'Team A', level: 'suspect',
  auto_suspended: false, urgent: false, service_state: 'active',
  sources: [{ source: 'devices', level: 'suspect' }], geo: null,
  signals: [{ kind: 'devices', state: 'suspect', code: 'over_building', updated_at_ms: NOW,
    evidence: { v: 1, window_days: 7, window_start: '2026-09-21', min_days: 3, max_devices: 3, recurrent: 2,
      distinct: 4, devices: [], fetches_with_hwid: 10, fetches_without: 0, clients: [] } }],
  changed_at_ms: NOW - 3 * 3_600_000, review: { ...NO_REVIEW, dismissed: true, lapsed: true },
}
const carol: QueueRow = {
  user_id: 9, upn: 'carol', display_name: '', group_id: 2, group_name: 'Team B', level: 'flagged',
  auto_suspended: false, urgent: true, service_state: 'active',
  sources: [{ source: 'sub_spread', level: 'flagged' }], geo: null,
  signals: [{ kind: 'sub_spread', state: 'flagged', code: 'spread', updated_at_ms: NOW, evidence: null }],
  changed_at_ms: 0, review: { ...NO_REVIEW, dismissed: true, reopened: true, escalated: ['sub_spread'] },
}

const COUNTS: QueueCounts = {
  online: 12, online_taken_at: '2026-09-28T10:00:05Z', online_stale: false, urgent: 2, flagged: 2, suspect: 1,
  auto_suspended: 1, dismissed: 1, trusted: 1, geo_unknown: 0,
}

function view(over: Partial<QueueView> = {}): QueueView {
  return { items: [alice, bob, carol], total: 3, page: 1, page_size: 25, counts: COUNTS,
    global_detectors_off: false, ...over }
}

const CITY_DB: GeoIPStatus = {
  enabled: true, dir: '', active: 'city.mmdb', update: { updating: false },
  available: [{ file: 'city.mmdb', type: 'city', granularity: 'city', build_epoch: 1, active: true }],
}

const liveView = {
  snapshot: { taken_at: '2026-09-28T10:00:05Z', source: 'poll', age_seconds: 42, stale: false,
    stale_after_seconds: 900, panels_asked: 1, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0,
    users: 0, connections: 0, truncated: 0 },
  refresh: { cooldown_seconds: 30, available_in_seconds: 0 },
  device_window_hours: 24, devices_unavailable: false, panels: [], items: [], total: 0, page: 1, page_size: 1,
}
const aliceSummary = {
  user: { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1, group_name: 'Team A',
    enabled: true, traffic_limit_bytes: 0 },
  attention: [], geo: null, signals: [], live: liveView, devices: [], device_window_hours: 24,
  devices_unavailable: false,
  review: { dismissed: false, dismissed_at_ms: 0, dismissed_by: 0, dismissed_by_upn: '', note: '', levels: {},
    reopened: false, lapsed: false, escalated: [], trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '' },
}

function httpError(status: number, data: unknown) {
  return new AxiosError('failed', 'ERR_BAD_REQUEST', undefined, undefined,
    { status, data, statusText: '', headers: {}, config: {} } as AxiosResponse)
}

type Answer = QueueView | Error | ((params: Record<string, unknown>) => QueueView | Error)

function serve(queue: Answer = view(), geoip: GeoIPStatus | Error = CITY_DB) {
  api.get.mockImplementation(async (url: string, cfg: { params?: Record<string, unknown> } = {}) => {
    if (url === '/admin/risk-center/queue') {
      const v = typeof queue === 'function' ? queue(cfg.params ?? {}) : queue
      if (v instanceof Error) throw v
      return { data: v }
    }
    if (url === '/admin/settings/geoip/status') {
      if (geoip instanceof Error) throw geoip
      return { data: geoip }
    }
    if (url === '/admin/risk-center/users/7') return { data: aliceSummary }
    if (url === '/admin/traffic/user/7') {
      return { data: { user_id: 7, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 } }
    }
    throw new Error(`unexpected GET ${url}`)
  })
}

const seen: string[] = []
function Where() {
  const loc = useLocation()
  seen.push(loc.pathname + loc.search)
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

const openLive = vi.fn()
const openPolicy = vi.fn()

// The tab as the page hosts it: rows open the page's drawer through the URL.
function Page() {
  const drawer = useDrawerParam('user')
  return (
    <>
      <QueueTab onOpenUser={drawer.open} onOpenLive={openLive} onOpenPolicy={openPolicy} />
      <RiskUserDrawer userId={drawer.id} onClose={drawer.close} host="risk" />
      <Where />
    </>
  )
}

function mount(search = '?tab=queue') {
  render(
    <MemoryRouter initialEntries={[`/admin/risk${search}`]}>
      <ThemeProvider theme={theme}>
        <Routes><Route path="/admin/risk" element={<Page />} /></Routes>
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

function location(): string {
  return screen.getByTestId('location').textContent ?? ''
}
function urlParams(): URLSearchParams {
  return new URLSearchParams(location().split('?')[1] ?? '')
}

function queueReads(): Record<string, unknown>[] {
  return api.get.mock.calls
    .filter(([u]) => u === '/admin/risk-center/queue')
    .map(([, cfg]) => (cfg as { params?: Record<string, unknown> } | undefined)?.params ?? {})
}
function lastQueueRead(): Record<string, unknown> {
  const reads = queueReads()
  return reads[reads.length - 1]
}

async function rowOf(upn: string): Promise<HTMLElement> {
  return (await screen.findByRole('button', { name: `打开 ${upn} 的风控详情` })).closest('tr') as HTMLElement
}

/** A metric card: its label and then its number (or "—"), which tells the
 *  已忽略 card from the 已忽略 status button. */
function card(label: string): HTMLElement {
  return screen.getByRole('button', { name: new RegExp(`^${label}\\s*(\\d+|—)$`) })
}

beforeEach(() => {
  vi.clearAllMocks()
  seen.length = 0
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  useSiteStore.setState({ timezone: 'Asia/Shanghai' })
})
afterEach(() => {
  vi.useRealTimers()
  cleanup()
  useAuthStore.setState({ role: '' })
  useSiteStore.setState({ timezone: '' })
  vi.unstubAllGlobals()
})

describe('QueueTab rows', () => {
  it('asks for the open queue by default', async () => {
    serve()
    mount()
    await rowOf('alice')
    expect(lastQueueRead()).toEqual({ status: 'open', page: 1, page_size: 25 })
  })

  it('shows the level, the hold and the review of each row', async () => {
    serve()
    mount()
    const a = await rowOf('alice')
    expect(within(a).getByText('已标记')).toBeTruthy()
    expect(within(a).getByText('异地自动暂停')).toBeTruthy()
    expect(within(a).getByText('Alice · Team A')).toBeTruthy()
    const b = await rowOf('bob')
    expect(within(b).getByText('疑似')).toBeTruthy()
    expect(within(b).getByText('忽略已过期')).toBeTruthy()
    const c = await rowOf('carol')
    expect(within(c).getByText('忽略后再次升级')).toBeTruthy()
  })

  // The hold is the level column's chip and its own card; drawn again as a
  // signal it would say one fact three times (D18).
  it('draws a chip per signal, never one for the hold', async () => {
    serve()
    mount()
    const a = await rowOf('alice')
    expect(within(a).getByText('异地并发 · 跨省')).toBeTruthy()
    expect(within(a).queryByText('自动临时暂停')).toBeNull()
    expect(within(await rowOf('bob')).getByText('设备数')).toBeTruthy()
  })

  it('reads the reason as places and distance, and a kind as its sentence', async () => {
    serve()
    mount()
    expect(within(await rowOf('alice')).getByText('广东、湖南 · 相距约 640 公里')).toBeTruthy()
    expect(within(await rowOf('bob')).getByText('共 4 台设备，其中常用 2 台（上限 3）')).toBeTruthy()
  })

  // Relative in the column, absolute (panel time) in the tooltip; nothing
  // on record reads "—", never "just now".
  it('shows the last change as relative time with the exact time in its tooltip', async () => {
    serve()
    mount()
    const cell = within(await rowOf('alice')).getByText('5 分钟前')
    expect(cell.getAttribute('aria-label')).toBe(formatMsDualTz(alice.changed_at_ms, 'Asia/Shanghai'))
    expect(within(await rowOf('carol')).getByText('—')).toBeTruthy()
  })

  it('says when the hold began in the hold chip', async () => {
    serve()
    mount()
    const chip = within(await rowOf('alice')).getByText('异地自动暂停').closest('.MuiChip-root') as HTMLElement
    expect(chip.getAttribute('aria-label'))
      .toBe(`自 ${formatMsDualTz(HELD_AT, 'Asia/Shanghai')} 起自动暂停，到期自动恢复`)
  })
})

describe('QueueTab metric cards', () => {
  it('shows the counts', async () => {
    serve()
    mount()
    await rowOf('alice')
    expect(card('在线账号').textContent).toContain('12')
    expect(card('已标记').textContent).toContain('2')
    expect(card('疑似').textContent).toContain('1')
    expect(card('异地自动暂停').textContent).toContain('1')
    expect(card('已忽略').textContent).toContain('1')
  })

  it('toggles a filter as a pressed button, and clears it on a second click', async () => {
    serve()
    mount('?tab=queue&page=2')
    await rowOf('alice')
    const flagged = card('已标记')
    expect(flagged.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(flagged)
    await waitFor(() => expect(urlParams().get('level')).toBe('flagged'))
    // A new filter starts at the first page.
    expect(urlParams().has('page')).toBe(false)
    await waitFor(() => expect(card('已标记').getAttribute('aria-pressed')).toBe('true'))
    await waitFor(() => expect(lastQueueRead()).toEqual({ status: 'open', level: 'flagged', page: 1, page_size: 25 }))

    fireEvent.click(card('已标记'))
    await waitFor(() => expect(urlParams().has('level')).toBe(false))
    expect(card('已标记').getAttribute('aria-pressed')).toBe('false')
  })

  // Every held account, whatever its review state (D17): the card asks for
  // status=all as well.
  it('the hold card filters held accounts in every status', async () => {
    serve()
    mount()
    await rowOf('alice')
    fireEvent.click(card('异地自动暂停'))
    await waitFor(() => expect(urlParams().get('auto')).toBe('1'))
    expect(urlParams().get('status')).toBe('all')
    await waitFor(() => expect(lastQueueRead())
      .toEqual({ status: 'all', auto_suspended: true, page: 1, page_size: 25 }))
  })

  it('the suspect and dismissed cards set their filters', async () => {
    serve()
    mount()
    await rowOf('alice')
    fireEvent.click(card('疑似'))
    await waitFor(() => expect(urlParams().get('level')).toBe('suspect'))
    fireEvent.click(card('已忽略'))
    await waitFor(() => expect(urlParams().get('status')).toBe('dismissed'))
    // One card at a time: the dismissed card replaced the suspect one.
    expect(urlParams().has('level')).toBe(false)
  })

  it('the online card opens the live tab', async () => {
    serve()
    mount()
    await rowOf('alice')
    const online = card('在线账号')
    // A way to another tab, not a filter: nothing to be pressed.
    expect(online.hasAttribute('aria-pressed')).toBe(false)
    fireEvent.click(online)
    expect(openLive).toHaveBeenCalled()
  })

  it('names the snapshot the online count is from', async () => {
    serve()
    mount()
    await rowOf('alice')
    const tip = `来自 ${formatDualTz('2026-09-28T10:00:05Z', 'Asia/Shanghai')} 的快照`
    expect(card('在线账号').getAttribute('title')).toBe(tip)
  })
})

describe('QueueTab filters', () => {
  it('switches the status, trusted included', async () => {
    serve()
    mount()
    await rowOf('alice')
    const group = screen.getByRole('group', { name: '状态' })
    expect(within(group).getAllByRole('button').map(b => b.textContent)).toEqual(['待处理', '已忽略', '已信任', '全部'])
    fireEvent.click(within(group).getByRole('button', { name: '已信任' }))
    await waitFor(() => expect(urlParams().get('status')).toBe('trusted'))
    await waitFor(() => expect(lastQueueRead()).toEqual({ status: 'trusted', page: 1, page_size: 25 }))
    fireEvent.click(within(group).getByRole('button', { name: '待处理' }))
    await waitFor(() => expect(urlParams().has('status')).toBe(false))
  })

  it('filters by several sources as one comma list, never the hold', async () => {
    serve()
    mount()
    await rowOf('alice')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '来源' }))
    const list = await screen.findByRole('listbox')
    expect(within(list).getAllByRole('option').map(o => o.textContent))
      .toEqual(['异地并发', '订阅多地', '设备数', '用量变化', '登录国家'])
    fireEvent.click(within(list).getByRole('option', { name: '设备数' }))
    await waitFor(() => expect(urlParams().get('source')).toBe('devices'))
    fireEvent.click(within(screen.getByRole('listbox')).getByRole('option', { name: '异地并发' }))
    await waitFor(() => expect(urlParams().get('source')).toBe('geo,devices'))
    await waitFor(() => expect(lastQueueRead()).toEqual({ status: 'open', source: 'geo,devices', page: 1, page_size: 25 }))
  })

  // The search writes the URL once the typing pauses, reading the URL as it
  // is THEN: a card clicked while it waits is kept, not undone.
  it('writes the search once after 300 ms without undoing a card clicked meanwhile', async () => {
    serve()
    mount()
    await rowOf('alice')
    vi.useFakeTimers()
    const box = screen.getByRole('textbox', { name: '搜索用户' })
    fireEvent.change(box, { target: { value: 'a' } })
    fireEvent.change(box, { target: { value: 'al' } })
    fireEvent.change(box, { target: { value: 'ali' } })
    act(() => { vi.advanceTimersByTime(299) })
    expect(urlParams().has('q')).toBe(false)

    fireEvent.click(card('已标记'))
    expect(urlParams().get('level')).toBe('flagged')
    const before = seen.length
    act(() => { vi.advanceTimersByTime(1) })
    expect(urlParams().get('q')).toBe('ali')
    expect(urlParams().get('level')).toBe('flagged')
    // One write for the three keystrokes.
    expect(new Set(seen.slice(before)).size).toBe(1)
    act(() => { vi.advanceTimersByTime(1000) })
    expect(new Set(seen.slice(before)).size).toBe(1)
  })

  // The bell's filter: a chip that says what the list is narrowed to, and
  // takes the narrowing away.
  it('shows the urgent filter as a removable chip', async () => {
    serve()
    mount('?tab=queue&urgent=1')
    await rowOf('alice')
    expect(lastQueueRead()).toEqual({ status: 'open', urgent: true, page: 1, page_size: 25 })
    const chip = screen.getByText('仅需立即处理').closest('.MuiChip-root') as HTMLElement
    fireEvent.click(within(chip).getByTestId('CancelIcon'))
    await waitFor(() => expect(urlParams().has('urgent')).toBe(false))
    expect(screen.queryByText('仅需立即处理')).toBeNull()
  })
})

describe('QueueTab opening an account', () => {
  it('a row click opens the drawer through the URL', async () => {
    serve()
    mount()
    fireEvent.click(within(await rowOf('bob')).getByText('设备数'))
    await waitFor(() => expect(urlParams().get('user')).toBe('8'))
  })

  // The UPN is the keyboard way in: Enter opens the drawer, and Esc brings
  // the focus back to where it was.
  it('Enter on the user opens the drawer and focus returns after Esc', async () => {
    serve()
    mount()
    const upn = within(await rowOf('alice')).getByRole('button', { name: '打开 alice 的风控详情' })
    upn.focus()
    fireEvent.keyDown(upn, { key: 'Enter' })
    await waitFor(() => expect(location()).toBe('/admin/risk?tab=queue&user=7'))
    const header = await screen.findByTestId('risk-drawer-header')
    fireEvent.keyDown(within(header).getByText('alice'), { key: 'Escape' })
    await waitFor(() => expect(location()).toBe('/admin/risk?tab=queue'))
    await waitFor(() => expect(document.activeElement).toBe(
      screen.getByRole('button', { name: '打开 alice 的风控详情' })))
  })

  // The menu decides from the row alone: opening it reads nothing.
  it('the row menu offers the action matrix without a fetch', async () => {
    serve()
    mount()
    const a = await rowOf('alice')
    const reads = api.get.mock.calls.length
    fireEvent.click(within(a).getByRole('button', { name: '更多操作' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map(m => m.textContent))
      .toEqual(['改为人工暂停', '恢复代理服务', '忽略', '信任此账号'])
    expect(api.get.mock.calls.length).toBe(reads)
    // The menu is not the row: nothing opened behind it.
    expect(urlParams().has('user')).toBe(false)
  })

  // Trust is offered in the menu only where it changes something: a
  // devices-only row has no location source.
  it('the row menu leaves trust out for a devices-only row', async () => {
    serve()
    mount()
    fireEvent.click(within(await rowOf('bob')).getByRole('button', { name: '更多操作' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitem').map(m => m.textContent))
      .toEqual(['暂停代理服务', '再次忽略', '取消忽略'])
  })

  it('a menu action opens its dialog', async () => {
    serve()
    mount()
    fireEvent.click(within(await rowOf('alice')).getByRole('button', { name: '更多操作' }))
    fireEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: '忽略' }))
    expect(within(await screen.findByRole('dialog')).getByText('忽略 alice 当前的信号？')).toBeTruthy()
    expect(urlParams().has('user')).toBe(false)
  })
})

describe('QueueTab states', () => {
  it('warns when the location database is unavailable', async () => {
    serve(view(), { ...CITY_DB, enabled: false })
    mount()
    await rowOf('alice')
    const warn = await screen.findByText(/^IP 地区库不可用/)
    expect((warn.closest('.MuiAlert-root') as HTMLElement).className).toContain('Warning')
    const link = screen.getByRole('link', { name: '前往 IP 地区显示' })
    expect(link.getAttribute('href')).toBe('/admin/settings?tab=general')
  })

  it('warns as well when no database is active', async () => {
    serve(view(), { ...CITY_DB, active: '', available: [] })
    mount()
    expect(await screen.findByText(/^IP 地区库不可用/)).toBeTruthy()
  })

  it('notes a country-only database', async () => {
    serve(view(), { ...CITY_DB, available: [{ ...CITY_DB.available[0], granularity: 'country' }] })
    mount()
    const info = await screen.findByText('当前地区库只能定位到国家，跨省、跨城两级不会触发。')
    expect((info.closest('.MuiAlert-root') as HTMLElement).className).toContain('Info')
    expect(screen.queryByText(/^IP 地区库不可用/)).toBeNull()
  })

  // No evidence of a dead database is not evidence of one.
  it('says nothing when the database status cannot be read', async () => {
    serve(view(), new Error('boom'))
    mount()
    await rowOf('alice')
    expect(screen.queryByText(/^IP 地区库不可用/)).toBeNull()
  })

  it('counts the accounts that cannot be placed', async () => {
    serve(view({ counts: { ...COUNTS, geo_unknown: 3 } }))
    mount()
    expect(await screen.findByText('3 个账号当前无法判断地区')).toBeTruthy()
  })

  it('says an empty queue is empty, and that detectors are off', async () => {
    serve(view({ items: [], total: 0, global_detectors_off: true }))
    mount()
    expect(await screen.findByText('没有需要处理的账号')).toBeTruthy()
    expect(screen.getByText('全局设置关闭了所有检测（分组例外可能仍开启部分检测）。')).toBeTruthy()
    // The switches are on the policy tab, one click away.
    fireEvent.click(screen.getByRole('button', { name: '前往策略' }))
    expect(openPolicy).toHaveBeenCalledOnce()
  })

  it('offers the policy only when the detectors are off', async () => {
    serve(view({ items: [], total: 0, global_detectors_off: false }))
    mount()
    expect(await screen.findByText('没有需要处理的账号')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '前往策略' })).toBeNull()
  })

  it.each([
    ['?tab=queue&status=dismissed', '没有已忽略的账号'],
    ['?tab=queue&status=trusted', '没有已信任的账号'],
    ['?tab=queue&source=geo', '没有符合条件的账号'],
    ['?tab=queue&q=zed', '没有符合条件的账号'],
    ['?tab=queue&status=trusted&level=flagged', '没有符合条件的账号'],
  ])('%s empty says %s', async (search, text) => {
    serve(view({ items: [], total: 0 }))
    mount(search)
    expect(await screen.findByText(text)).toBeTruthy()
    expect(screen.queryByText('没有需要处理的账号')).toBeNull()
  })

  it('reports a failed read with a retry', async () => {
    let fail = true
    serve(() => (fail ? httpError(500, { error: 'db down' }) : view()))
    mount()
    expect(await screen.findByText('读取失败：db down')).toBeTruthy()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await rowOf('alice')).toBeTruthy()
  })

  it('says the risk center is not wired on a 503', async () => {
    serve(httpError(503, { error: 'risk center not configured' }))
    mount()
    expect(await screen.findByText('本部署未启用风控中心。')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })
})

describe('QueueTab on a phone', () => {
  // Three columns fold into one caption line under the user, so the table
  // fits a phone without scrolling sideways.
  it('moves signals, last change and review under the user cell', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: true, media: query, onchange: null, addListener: vi.fn(), removeListener: vi.fn(),
      addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    serve()
    mount()
    const b = await rowOf('bob')
    const headers = screen.getAllByRole('columnheader').map(h => h.textContent)
    expect(headers).not.toContain('信号')
    expect(headers).not.toContain('最近变化')
    expect(headers).not.toContain('处理')
    const userCell = within(b).getByRole('button', { name: '打开 bob 的风控详情' }).closest('td') as HTMLElement
    expect(within(userCell).getByText('设备数')).toBeTruthy()
    expect(within(userCell).getByText('3 小时前')).toBeTruthy()
    expect(within(userCell).getByText('忽略已过期')).toBeTruthy()
  })
})
