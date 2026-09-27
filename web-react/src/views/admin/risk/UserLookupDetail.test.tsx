/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import UserLookupDetail from './UserLookupDetail'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
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

const alice = {
  id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1, enabled: true,
  traffic_limit_bytes: 100 * 2 ** 30, traffic_reset_period: 'monthly', ip_limit: 0, device_limit: 0,
  emergency_used_count: 0, account_status: 'active', service_status: 'active', created_at: '2026-01-01T00:00:00Z',
}

const geoRow = {
  user_id: 7, upn: 'alice', state: 'suspect', reason: 'stored', tier: 'region', flagged: false, places: ['CN'],
  live_ips: 3, concurrent_ips: 3, excluded_ips: 0, complete: true, over_streak: 2, under_streak: 0, ban_streak: 0,
  evidence: {
    v: 3, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
    coverage: { placed: 3, unplaced: 0, region_known: 3, city_known: 3 }, networks: 3,
    spread: { countries: 1, regions: 3, region_country: 'CN', cities: 3, city_country: 'CN' },
    why: { code: 'suspect', tier: 'region', scope: 'city', tol: { countries: 1, regions: 2, cities: 3 },
      flag_after: 6, clear_after: 6, min_placed_ratio: 0.5 },
  },
  updated_at_ms: 1_790_000_000_000,
}

const riskRow = {
  user_id: 7, upn: 'alice', geo: { state: 'suspect', flagged: false, tier: 'region', updated_at_ms: 1 },
  signals: [{ kind: 'devices', state: 'flagged', code: 'over', updated_at_ms: 1,
    evidence: { v: 1, window_days: 7, window_start: '2026-09-20', min_days: 3, max_devices: 3, recurrent: 5,
      distinct: 6, devices: [], fetches_with_hwid: 10, fetches_without: 0, clients: [] } }],
}

const empty = { items: [], total: 0, page: 1, page_size: 25 }

function liveView() {
  return {
    snapshot: {
      taken_at: '2026-09-26T10:00:05Z', source: 'poll', age_seconds: 42, stale: false, stale_after_seconds: 900,
      panels_asked: 1, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0, users: 0, connections: 0, truncated: 0,
    },
    refresh: { cooldown_seconds: 30, available_in_seconds: 0 },
    device_window_hours: 24, devices_unavailable: false, panels: [], items: [], total: 0, page: 1, page_size: 1,
  }
}

type Reply = unknown | ((cfg: { params?: Record<string, unknown> }) => unknown)

function serve(over: Record<string, Reply> = {}) {
  const routes: Record<string, Reply> = {
    '/admin/users/7': alice,
    '/admin/users': empty,
    '/admin/traffic/user/7': { user_id: 7, permanent_total_bytes: 0, period_used_bytes: 5 * 2 ** 30, today_used_bytes: 0 },
    '/admin/traffic/user/7/servers': { items: [] },
    '/admin/risk-center/live': liveView(),
    '/admin/geo-anomalies': { items: [geoRow] },
    '/admin/risk-signals': { items: [riskRow] },
    '/admin/risk-center/connections': empty,
    '/admin/risk-center/flags': empty,
    '/admin/sub-logs': { items: [], total: 0 },
    '/admin/auth-events': { items: [], total: 0 },
    ...over,
  }
  api.get.mockImplementation(async (url: string, cfg: { params?: Record<string, unknown> } = {}) => {
    if (!(url in routes)) throw new Error(`unexpected GET ${url}`)
    const r = routes[url]
    if (r instanceof Error || (r as { isAxiosError?: boolean })?.isAxiosError) throw r
    return { data: typeof r === 'function' ? (r as (c: typeof cfg) => unknown)(cfg) : r }
  })
}

function mount() {
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}><UserLookupDetail userId={7} /></ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

function requested(url: string): { params?: Record<string, unknown> }[] {
  return api.get.mock.calls.filter(([u]) => u === url).map(([, cfg]) => (cfg ?? {}) as { params?: Record<string, unknown> })
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(cleanup)

const SECTIONS = ['概况', '实时连接', '异地并发', '风险信号', '连接历史', '标记记录', '最近订阅拉取', '最近登录', '用量']

describe('UserLookupDetail', () => {
  it('renders every section for the id', async () => {
    serve()
    mount()
    for (const name of SECTIONS) {
      expect(await screen.findByRole('heading', { name })).toBeTruthy()
    }
    expect(screen.getAllByText('alice').length).toBeGreaterThan(0)
    // The overview links to the account's usage trend, where the numbers are.
    expect(screen.getByRole('link', { name: /查看用量趋势/ }).getAttribute('href'))
      .toBe('/admin/traffic?tab=trend&scope=user&user=7')
  })

  // One account, one row: the lookup asks for that account alone and never
  // loads the fleet lists to pick it out of them.
  it('queries geo and risk with user_id, never the fleet lists', async () => {
    serve()
    mount()
    await screen.findByText('同时在 CN 的 3 个省 / 州（容错 2），连续 2 / 6 次')
    await screen.findByText('5 台常用设备（上限 3）', { exact: false })

    for (const url of ['/admin/geo-anomalies', '/admin/risk-signals', '/admin/risk-center/live',
      '/admin/risk-center/connections', '/admin/risk-center/flags', '/admin/sub-logs', '/admin/auth-events']) {
      const calls = requested(url)
      expect(calls.length, url).toBeGreaterThan(0)
      for (const cfg of calls) expect(cfg.params?.user_id, url).toBe(7)
    }
  })

  it('shows no_geo when the detector has no row', async () => {
    serve({ '/admin/geo-anomalies': { items: [] }, '/admin/risk-signals': { items: [] } })
    mount()
    expect(await screen.findByText('异地并发检测对这个账号没有记录')).toBeTruthy()
    expect(await screen.findByText('这个账号还没有风险信号')).toBeTruthy()
  })

  // A deleted account has no sections to show, and asking for them would
  // only fetch the fleet's answer to "nobody".
  it('not_found for a 404', async () => {
    serve({ '/admin/users/7': { isAxiosError: true, response: { status: 404, data: { error: 'Not found' } } } })
    mount()
    expect(await screen.findByText('用户不存在或已删除')).toBeTruthy()
    await waitFor(() => expect(requested('/admin/users/7').length).toBeGreaterThan(0))
    expect(screen.queryByRole('heading', { name: '实时连接' })).toBeNull()
    for (const url of ['/admin/geo-anomalies', '/admin/risk-signals', '/admin/risk-center/live']) {
      expect(requested(url)).toHaveLength(0)
    }
  })
})
