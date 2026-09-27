/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import type { RiskUserRow } from '@/api/riskSignals'
import RiskCenterView from './RiskCenterView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
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

// Every location the page passed through, so a test can tell one URL update
// from two in a row.
const seen: string[] = []

function Where() {
  const loc = useLocation()
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

// Every tab's reads answer; the location-database status fails, which costs
// the Geo tab nothing but its advisory banner. The user lookup reads account
// 7 itself; its sections' other reads fail as unexpected, which costs these
// tests nothing: they ask which account the page looked up, not what the
// sections say about it.
function serve(risk: RiskUserRow[] = [], geo: unknown[] = []) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/users/7') return { data: alice }
    if (url === '/admin/geo-anomalies') return { data: { items: geo } }
    if (url === '/admin/risk-signals') return { data: { items: risk } }
    if (url === '/admin/risk-center/live') return { data: liveView }
    if (url === '/admin/risk-center/flags') return { data: { items: [], total: 0, page: 1, page_size: 25 } }
    if (url === '/admin/users') return { data: { items: [], total: 0, page: 1, page_size: 50 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function fetched(url: string): boolean {
  return api.get.mock.calls.some(([u]) => u === url)
}

function selectedTab(): string {
  const tab = screen.getAllByRole('tab').find(el => el.getAttribute('aria-selected') === 'true')
  return tab?.textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  seen.length = 0
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('RiskCenterView', () => {
  it('opens on the live connections tab by default', async () => {
    serve()
    mount('/admin/risk')

    expect(await screen.findByRole('heading', { name: '风控中心' })).toBeTruthy()
    expect(selectedTab()).toBe('实时连接')
    await waitFor(() => expect(fetched('/admin/risk-center/live')).toBe(true))
    // Only the open tab reads: the lists behind the other tabs are not
    // fetched behind it.
    expect(fetched('/admin/geo-anomalies')).toBe(false)
    expect(fetched('/admin/risk-signals')).toBe(false)
    expect(fetched('/admin/risk-center/flags')).toBe(false)
  })

  it('offers the five tabs in order', async () => {
    serve()
    mount('/admin/risk')
    await screen.findByRole('heading', { name: '风控中心' })
    expect(screen.getAllByRole('tab').map(el => el.textContent)).toEqual(
      ['实时连接', '异地并发', '风险信号', '标记记录', '用户查询'])
  })

  it('renders the flag records from ?tab=flags', async () => {
    serve()
    mount('/admin/risk?tab=flags')
    await waitFor(() => expect(fetched('/admin/risk-center/flags')).toBe(true))
    expect(selectedTab()).toBe('标记记录')
  })

  // A row on any tab opens its account in the lookup. The tab and the id
  // are written in ONE update: two in a row would pass through a URL that
  // names the lookup with no account, or an account on the wrong tab.
  it('openUser sets tab and id in one update', async () => {
    serve([], [{
      user_id: 7, upn: 'alice', state: 'suspect', reason: 'stored', tier: 'region', flagged: false, places: ['CN'],
      live_ips: 3, concurrent_ips: 3, excluded_ips: 0, complete: true, over_streak: 1, under_streak: 0, ban_streak: 0,
      evidence: { v: 0, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
        coverage: { placed: 0, unplaced: 0, region_known: 0, city_known: 0 }, networks: 0,
        spread: { countries: 0, regions: 0, region_country: '', cities: 0, city_country: '' } },
      updated_at_ms: 1,
    }])
    mount('/admin/risk?tab=geo')

    const row = (await screen.findByText('alice')).closest('tr') as HTMLElement
    const before = seen.length
    fireEvent.click(within(row).getByRole('button', { name: '查看用户' }))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/risk?tab=user&id=7'))
    expect(selectedTab()).toBe('用户查询')
    const passed = [...new Set(seen.slice(before))]
    expect(passed).toEqual(['/admin/risk?tab=user&id=7'])
  })

  // The link the Users page and every "open user" button write. Driven
  // through the router, not handed in as a prop: what is pinned is that the
  // page reads ?id= and looks THAT account up.
  it('a ?tab=user&id= link opens that account in the lookup', async () => {
    serve()
    mount('/admin/risk?tab=user&id=7')

    const overview = (await screen.findByRole('heading', { name: '概况' })).closest('section') as HTMLElement
    expect(await within(overview).findByText('#7')).toBeTruthy()
    expect(within(overview).getByText('alice')).toBeTruthy()
    expect(selectedTab()).toBe('用户查询')
    expect(fetched('/admin/users/7')).toBe(true)
  })

  // A malformed id asks for nobody: the pick hint, and no account read at
  // all — never a lookup of #0 or of whatever Number() made of the text.
  it.each(['abc', '0', '-7', '7.5', '07x'])('a malformed id=%s asks for a pick instead', async raw => {
    serve()
    mount(`/admin/risk?tab=user&id=${raw}`)

    expect(await screen.findByText('按用户名或显示名搜索')).toBeTruthy()
    expect(selectedTab()).toBe('用户查询')
    expect(screen.queryByRole('heading', { name: '概况' })).toBeNull()
    expect(api.get.mock.calls.some(([u]) => /^\/admin\/users\/[^/]+$/.test(u))).toBe(false)
  })

  it('renders the location tab from ?tab=geo', async () => {
    serve()
    mount('/admin/risk?tab=geo')

    await waitFor(() => expect(fetched('/admin/geo-anomalies')).toBe(true))
    expect(selectedTab()).toBe('异地并发')
  })

  it('renders the risk tab from ?tab=risk', async () => {
    serve()
    mount('/admin/risk?tab=risk')

    await waitFor(() => expect(fetched('/admin/risk-signals')).toBe(true))
    expect(selectedTab()).toBe('风险信号')
    expect(fetched('/admin/geo-anomalies')).toBe(false)
  })

  // Each risk row carries the account's concurrent-location verdict, and its
  // chip is the way to the evidence behind it. The URL owns the tab, so the
  // chip writes it there: a refresh or a copied link lands on the same tab.
  it("the risk tab's location link switches to ?tab=geo", async () => {
    serve([{ user_id: 7, upn: 'alice', signals: [],
      geo: { state: 'suspect', flagged: false, tier: 'region', updated_at_ms: 1 } }])
    mount('/admin/risk?tab=risk')

    const row = (await screen.findByText('alice')).closest('tr') as HTMLElement
    fireEvent.click(within(row).getByRole('button', { name: '在「异地并发」中查看' }))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/risk?tab=geo'))
    expect(selectedTab()).toBe('异地并发')
    await waitFor(() => expect(fetched('/admin/geo-anomalies')).toBe(true))
  })

  // The route is admin-only already (ADMIN_ONLY_ROUTES bounces an operator in
  // RequireAuth); the page checks its own capability as well, so it never
  // asks an adminGroup endpoint for an answer that can only be 403.
  it('renders nothing and redirects without risk.view', async () => {
    serve()
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mount('/admin/risk?tab=geo')

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/dashboard'))
    expect(screen.getByText('dashboard')).toBeTruthy()
    expect(screen.queryByRole('tab')).toBeNull()
    expect(api.get).not.toHaveBeenCalled()
  })
})
