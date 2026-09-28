/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, Route, Routes, useLocation, useNavigationType } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import type { QueueRow, QueueView } from '@/api/riskCenter'
import RiskCenterView from './RiskCenterView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
// t over the REAL zh-CN admin bundle, flattened as the SPA registers it, so a
// key the page asks for but the bundle lacks shows up as its raw key instead
// of passing on a defaultValue.
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
import { flatten, type Nested } from '@/i18n/options'
dict.current = flatten(zh as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

// Every location the page passed through and how it got there, so a test
// can tell one replace from two updates in a row.
const seen: string[] = []
let lastNav = ''

function Where() {
  const loc = useLocation()
  lastNav = useNavigationType()
  seen.push(loc.pathname + loc.search)
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

function mount(url: string) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <ThemeProvider theme={theme}>
        <Routes>
          <Route path="/admin/risk" element={<><RiskCenterView /><Where /></>} />
          <Route path="/admin/dashboard" element={<><p>dashboard</p><Where /></>} />
        </Routes>
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

const liveView = {
  snapshot: {
    taken_at: '2026-09-26T10:00:05Z', source: 'poll', age_seconds: 42, stale: false, stale_after_seconds: 900,
    panels_asked: 1, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0, users: 0, connections: 0, truncated: 0,
  },
  refresh: { cooldown_seconds: 30, available_in_seconds: 0 },
  device_window_hours: 24, devices_unavailable: false, panels: [], items: [], total: 0, page: 1, page_size: 25,
}

const alice = { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1, enabled: true,
  traffic_limit_bytes: 0, account_status: 'active', service_status: 'active' }

// The drawer's summary of account 7: nothing at attention, nothing judged,
// nobody connected — enough to name the account the drawer opened.
const aliceSummary = {
  user: { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1, group_name: 'Team A', enabled: true,
    traffic_limit_bytes: 0 },
  attention: [],
  review: { dismissed: false, dismissed_at_ms: 0, dismissed_by: 0, dismissed_by_upn: '', note: '', levels: {},
    reopened: false, lapsed: false, escalated: [], trusted: false, trusted_at_ms: 0, trusted_by: 0, trusted_by_upn: '' },
  geo: null, signals: [], live: liveView, devices: [], device_window_hours: 24, devices_unavailable: false,
}

const aliceRow: QueueRow = {
  user_id: 7, upn: 'alice', display_name: 'Alice', group_id: 1, group_name: 'Team A', level: 'suspect',
  auto_suspended: false, urgent: false, service_state: 'active', sources: [{ source: 'devices', level: 'suspect' }],
  geo: null, signals: [{ kind: 'devices', state: 'suspect', code: 'over_building', updated_at_ms: 1, evidence: null }],
  changed_at_ms: 0, review: { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] },
}

function queue(items: QueueRow[] = []): QueueView {
  return {
    items, total: items.length, page: 1, page_size: 25, global_detectors_off: false,
    counts: { online: 3, online_taken_at: '2026-09-26T10:00:05Z', online_stale: false, urgent: 0, flagged: 0,
      suspect: items.length, auto_suspended: 0, dismissed: 0, trusted: 0, geo_unknown: 0 },
  }
}

function serve(rows: QueueRow[] = []) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/queue') return { data: queue(rows) }
    if (url === '/admin/settings/geoip/status') {
      return { data: { enabled: true, dir: '', active: 'city.mmdb', available: [], update: { updating: false } } }
    }
    if (url === '/admin/users/7') return { data: alice }
    if (url === '/admin/risk-center/users/7') return { data: aliceSummary }
    if (url === '/admin/traffic/user/7') {
      return { data: { user_id: 7, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 } }
    }
    if (url === '/admin/risk-center/live') return { data: liveView }
    if (url === '/admin/risk-center/flags') return { data: { items: [], total: 0, page: 1, page_size: 25 } }
    if (url === '/admin/users') return { data: { items: [alice], total: 1, page: 1, page_size: 50 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function fetched(url: string): boolean {
  return api.get.mock.calls.some(([u]) => u === url)
}

function queueReads(): Record<string, unknown>[] {
  return api.get.mock.calls
    .filter(([u]) => u === '/admin/risk-center/queue')
    .map(([, cfg]) => (cfg as { params?: Record<string, unknown> } | undefined)?.params ?? {})
}

function location(): string {
  return screen.getByTestId('location').textContent ?? ''
}

function selectedTab(): string {
  // The page's own tabs: the drawer, portaled after the page, has its own,
  // and while it is open the page behind it is aria-hidden.
  const [page] = screen.getAllByRole('tablist', { hidden: true })
  const tab = within(page).getAllByRole('tab', { hidden: true }).find(el => el.getAttribute('aria-selected') === 'true')
  return tab?.textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  seen.length = 0
  lastNav = ''
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('RiskCenterView', () => {
  it('opens on the queue by default and reads only the queue', async () => {
    serve()
    mount('/admin/risk')

    expect(await screen.findByRole('heading', { name: '风控中心' })).toBeTruthy()
    expect(selectedTab()).toBe('待处理')
    await waitFor(() => expect(fetched('/admin/risk-center/queue')).toBe(true))
    // Only the open tab reads: the lists behind the other tabs are not
    // fetched behind it.
    expect(fetched('/admin/risk-center/live')).toBe(false)
    expect(fetched('/admin/risk-center/flags')).toBe(false)
    // The default is never written by a render.
    expect(location()).toBe('/admin/risk')
  })

  it('offers the three tabs in order', async () => {
    serve()
    mount('/admin/risk')
    await screen.findByRole('heading', { name: '风控中心' })
    expect(screen.getAllByRole('tab').map(el => el.textContent)).toEqual(['待处理', '在线', '记录'])
  })

  it('says what the page is in one line', async () => {
    serve()
    mount('/admin/risk')
    expect(await screen.findByText('需要处理的账号、此刻的连接、发生过的变化与检测策略。')).toBeTruthy()
  })

  it.each([
    ['live', '在线', '/admin/risk-center/live'],
    ['records', '记录', '/admin/risk-center/flags'],
  ])('?tab=%s reads only its own list', async (tab, label, url) => {
    serve()
    mount(`/admin/risk?tab=${tab}`)
    await waitFor(() => expect(fetched(url)).toBe(true))
    expect(selectedTab()).toBe(label)
    expect(fetched('/admin/risk-center/queue')).toBe(false)
  })

  // Every switch writes the tab, replacing: a tab is a view of the page, and
  // Back leaves the page rather than stepping through the tabs.
  it('a tab switch writes the tab and replaces', async () => {
    serve()
    mount('/admin/risk')
    await screen.findByRole('heading', { name: '风控中心' })
    fireEvent.click(screen.getByRole('tab', { name: '记录' }))
    await waitFor(() => expect(location()).toBe('/admin/risk?tab=records'))
    expect(lastNav).toBe('REPLACE')
    fireEvent.click(screen.getByRole('tab', { name: '待处理' }))
    await waitFor(() => expect(location()).toBe('/admin/risk?tab=queue'))
  })

  it('each tab explains itself behind a help button', async () => {
    serve()
    mount('/admin/risk')
    await screen.findByRole('heading', { name: '风控中心' })
    fireEvent.click(screen.getByRole('button', { name: '说明' }))
    expect(await screen.findByText(/^每个需要处理的账号一行，最紧急的在前/)).toBeTruthy()
  })

  // The records tab's paragraph moved behind its "?" like the others'.
  it('the records tab explains itself behind a help button', async () => {
    serve()
    mount('/admin/risk?tab=records')
    await screen.findByRole('heading', { name: '风控中心' })
    fireEvent.click(screen.getByRole('button', { name: '说明' }))
    expect(await screen.findByText(/^每次进入或离开「疑似」「已标记」/)).toBeTruthy()
  })

  it('the online card switches to the live tab', async () => {
    serve()
    mount('/admin/risk')
    fireEvent.click(await screen.findByRole('button', { name: /^在线账号/ }))
    await waitFor(() => expect(location()).toBe('/admin/risk?tab=live'))
    expect(selectedTab()).toBe('在线')
  })

  // Any account can be opened, whatever tab is showing (P10).
  it('the header picker opens the drawer', async () => {
    serve()
    mount('/admin/risk?tab=records')
    await screen.findByRole('heading', { name: '风控中心' })
    const picker = screen.getByRole('combobox', { name: '选择用户' })
    fireEvent.keyDown(picker, { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: 'Alice (alice)' }))

    await waitFor(() => expect(location()).toBe('/admin/risk?tab=records&user=7'))
    expect(lastNav).toBe('PUSH')
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('alice')).toBeTruthy()
  })

  // A row opens its account over the tab it was opened from. The drawer is
  // a drill-down, so it PUSHES `user=` — in ONE update, keeping the tab.
  it('a queue row opens the drawer over the queue', async () => {
    serve([aliceRow])
    mount('/admin/risk?tab=queue')
    const upn = await screen.findByRole('button', { name: '打开 alice 的风控详情' })
    const before = seen.length
    fireEvent.click(upn)

    await waitFor(() => expect(location()).toBe('/admin/risk?tab=queue&user=7'))
    expect([...new Set(seen.slice(before))]).toEqual(['/admin/risk?tab=queue&user=7'])
    expect(selectedTab()).toBe('待处理')
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('alice')).toBeTruthy()
  })

  it('a ?user= link opens the drawer over the current tab', async () => {
    serve()
    mount('/admin/risk?tab=records&user=7')

    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('alice')).toBeTruthy()
    expect(selectedTab()).toBe('记录')
    await waitFor(() => expect(fetched('/admin/risk-center/flags')).toBe(true))
  })

  // A malformed id asks for nobody: no drawer, and no read at all — never a
  // summary of #0 or of whatever Number() made of the text.
  it.each(['abc', '0', '-7', '7.5', '07x'])('a malformed user=%s opens nothing', async raw => {
    serve()
    mount(`/admin/risk?tab=records&user=${raw}`)

    await waitFor(() => expect(fetched('/admin/risk-center/flags')).toBe(true))
    expect(screen.queryByTestId('risk-drawer-header')).toBeNull()
    expect(api.get.mock.calls.some(([u]) => String(u).startsWith('/admin/risk-center/users/'))).toBe(false)
  })

  // The old five tabs' links — bookmarks, old bell entries, the Users page's
  // lookup link — land where their question is answered now, in ONE replace
  // before any tab mounts, so nothing is read with the old link's meaning.
  it.each([
    ['?tab=geo', '?tab=queue&source=geo', '待处理'],
    ['?tab=risk', '?tab=queue', '待处理'],
    ['?tab=flags', '?tab=records', '记录'],
    ['?tab=connections', '?tab=live', '在线'],
    ['?tab=user', '?tab=queue', '待处理'],
    ['?tab=user&id=abc', '?tab=queue', '待处理'],
  ])('%s lands on %s in one replace', async (from, to, label) => {
    serve()
    mount(`/admin/risk${from}`)

    await waitFor(() => expect(location()).toBe(`/admin/risk${to}`))
    expect(lastNav).toBe('REPLACE')
    expect([...new Set(seen)]).toEqual([`/admin/risk${from}`, `/admin/risk${to}`])
    expect(selectedTab()).toBe(label)
    expect(fetched('/admin/geo-anomalies')).toBe(false)
    expect(fetched('/admin/risk-signals')).toBe(false)
    // The queue, when it is the landing, is read with the new link's filters
    // only.
    for (const p of queueReads()) expect(p.source).toBe(to.includes('source=geo') ? 'geo' : undefined)
  })

  it('an old lookup link opens that account in the drawer over the queue', async () => {
    serve()
    mount('/admin/risk?tab=user&id=7')

    await waitFor(() => expect(location()).toBe('/admin/risk?tab=queue&user=7'))
    expect(lastNav).toBe('REPLACE')
    expect([...new Set(seen)]).toEqual(['/admin/risk?tab=user&id=7', '/admin/risk?tab=queue&user=7'])
    const header = await screen.findByTestId('risk-drawer-header')
    expect(within(header).getByText('alice')).toBeTruthy()
  })

  // The route is admin-only already (ADMIN_ONLY_ROUTES bounces an operator in
  // RequireAuth); the page checks its own capability as well, so it never
  // asks an adminGroup endpoint for an answer that can only be 403.
  it.each(['/admin/risk', '/admin/risk?tab=geo', '/admin/risk?tab=user&id=7'])(
    'redirects an operator from %s without a read', async url => {
      serve()
      useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
      mount(url)

      await waitFor(() => expect(location()).toBe('/admin/dashboard'))
      expect(screen.getByText('dashboard')).toBeTruthy()
      expect(screen.queryByRole('tab')).toBeNull()
      expect(api.get).not.toHaveBeenCalled()
    })
})
