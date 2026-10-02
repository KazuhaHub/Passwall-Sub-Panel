// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import {
  NATIVE, SUI, THREE_XUI, c, g, hist, productionMetrics, productionSnapshot, serverList, type ServerRow,
} from '@/test/diagnosticsFixtures'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack, default: () => null }))
const confirmMock = vi.hoisted(() => vi.fn(async (_opts: unknown) => true))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
const copy = vi.hoisted(() => vi.fn(async (_text: string) => true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))

// t over the REAL zh-CN admin and nav bundles, so every assertion reads as
// the copy an operator sees, and a key the page asks for but the bundles
// lack renders as its raw key path instead of passing.
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const key = k.includes(':') ? k : `admin:${k}`
      const raw = dict.current[key] ?? k
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN', exists: (k: string) => k in dict.current },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import zhNav from '@/locales/zh-CN/nav.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = {
  ...Object.fromEntries(Object.entries(flatten(zh as Nested)).map(([k, v]) => [`admin:${k}`, v])),
  ...Object.fromEntries(Object.entries(flatten(zhNav as Nested)).map(([k, v]) => [`nav:${k}`, v])),
}

import DiagnosticsView from './DiagnosticsView'

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })

const served = vi.hoisted(() => ({
  snap: undefined as unknown,
  servers: undefined as unknown,
}))

function serve(snap: DiagnosticsSnapshot, servers: ServerRow[] = [THREE_XUI, NATIVE], total?: number) {
  served.snap = snap
  served.servers = serverList(servers, total)
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/diagnostics/metrics') return { data: served.snap }
    if (url === '/admin/settings/ui') return { data: { cron_traffic_pull_minutes: 2 } }
    if (url === '/admin/servers') return { data: served.servers }
    throw new Error(`unexpected GET ${url}`)
  })
}

function mount() {
  render(
    <ThemeProvider theme={theme}>
      <QueryClientProvider client={makeTestQueryClient()}>
        <MemoryRouter initialEntries={['/admin/diagnostics']}>
          <Routes>
            <Route path="/admin/diagnostics" element={<DiagnosticsView />} />
            <Route path="/admin/dashboard" element={<p>dashboard</p>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

const card = (id: string) => document.getElementById(`diag-card-${id}`) as HTMLElement
const statusLine = () => screen.getByTestId('diag-status')

async function loaded() {
  await screen.findByTestId('diag-status')
}

function openMenu() {
  fireEvent.click(screen.getByRole('button', { name: '更多操作' }))
  return screen.getByRole('menu')
}

function openRaw() {
  const region = screen.getByTestId('raw-metrics')
  fireEvent.click(within(region).getByRole('button', { name: /全部原始指标/ }))
  return region
}

function search(region: HTMLElement, text: string) {
  fireEvent.change(within(region).getByRole('textbox', { name: '按名称、标签或说明搜索' }), { target: { value: text } })
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('the status line', () => {
  // Amber, never red, when the worst thing on the page is a warning.
  it('reads "needs attention" when only warnings were found', async () => {
    const m = productionMetrics()
    m.counters = m.counters.map(x => x.name === 'psp_lifecycle_sync_error_total' ? c(x.name, 0)
      : x.name === 'psp_push_client_config_error_total' ? c(x.name, 3) : x)
    serve({ ...productionSnapshot(), metrics: m })
    mount()
    await loaded()
    expect(statusLine().getAttribute('data-tone')).toBe('attention')
    expect(within(statusLine()).getByText('需要留意：配额兜底刷新')).toBeTruthy()
  })

  it('reads "needs action" and names the card when an error was found', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    expect(statusLine().getAttribute('data-tone')).toBe('action')
    expect(within(statusLine()).getByText('需要处理：用户状态同步')).toBeTruthy()
  })
})

describe('the production reading', () => {
  it('lists the status write failures with their share, origin and where to go', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const finding = screen.getByTestId('finding-lifecycle_errors')
    expect(within(finding).getByText('17 次未能把用户状态写入面板（共核对 900 次，1.9%）')).toBeTruthy()
    expect(within(finding).getByText(/配额兜底刷新本区间没有失败/)).toBeTruthy()
    const link = within(finding).getByRole('link', { name: '前往同步任务' })
    expect(link.getAttribute('href')).toBe('/admin/sync-tasks')
    expect(within(finding).getByText('shared-client lifecycle push failed')).toBeTruthy()
  })

  it('shows the poll as running on schedule, with the least number of polls the interval implies', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const poll = card('poll')
    expect(within(poll).getByText('运行中')).toBeTruthy()
    expect(within(poll).getByText('按间隔至少应有 1,773 轮')).toBeTruthy()
    expect(within(poll).getByText('1,774 轮')).toBeTruthy()
    expect(screen.queryByTestId('finding-poll_behind')).toBeNull()
  })

  // Colour is never the only signal: every badge carries its words and an icon.
  it('draws each card state as an icon and words', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const badge = within(card('poll')).getByTestId('state-badge')
    expect(badge.getAttribute('data-state')).toBe('ok')
    expect(badge.textContent).toBe('运行中')
    expect(badge.querySelector('svg')).not.toBeNull()
    const failing = within(card('lifecycle')).getByTestId('state-badge')
    expect(failing.getAttribute('data-state')).toBe('failing')
    expect(failing.textContent).toBe('需要处理')
  })
})

describe('the raw metrics', () => {
  it('lists a series no card uses', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const raw = openRaw()
    fireEvent.click(within(raw).getByRole('button', { name: /^原生节点/ }))
    expect(within(raw).getByText('psp_node_host_snapshot_bytes')).toBeTruthy()
  })

  it('opens a histogram on every quantile and on each bucket\'s own count', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const raw = openRaw()
    search(raw, 'psp_poll_ms')
    fireEvent.click(within(raw).getByRole('button', { name: /1,774 个样本/ }))
    const stats = within(raw).getByTestId('hist-psp_poll_ms')
    expect(within(stats).getByText('p90')).toBeTruthy()
    expect(within(stats).getByText('2,100')).toBeTruthy()
    expect(within(stats).getByText('p99')).toBeTruthy()
    expect(within(stats).getByText('7,800')).toBeTruthy()
    const buckets = within(stats).getByRole('table')
    expect(within(buckets).getByText('本档次数')).toBeTruthy()
    // 400 → 1,500 → 1,770 cumulative: 1,100 and 270 in those buckets alone.
    expect(within(buckets).getByText('1,100')).toBeTruthy()
    expect(within(buckets).getByText('270')).toBeTruthy()
    expect(within(buckets).getByText('超过最后一档')).toBeTruthy()
  })

  it('tells an older server from a value that has not happened yet', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const raw = openRaw()
    search(raw, 'psp_live_connections')
    expect(within(raw).getByText('此服务端版本没有该指标')).toBeTruthy()
    search(raw, 'psp_saml_acs_failure_total')
    expect(within(raw).getByText('尚未出现任何取值（即全部为 0）')).toBeTruthy()
  })

  it('shows the server\'s own description as it was sent', async () => {
    serve(productionSnapshot())
    mount()
    await loaded()
    const raw = openRaw()
    search(raw, 'psp_poll_total')
    expect(within(raw).getByText('Traffic poll cycles started.')).toBeTruthy()
    expect(within(raw).getAllByText(/服务端说明/).length).toBeGreaterThan(0)
  })
})

describe('export', () => {
  it('copies the API response exactly as it arrived', async () => {
    const snap = productionSnapshot()
    serve(snap)
    mount()
    await loaded()
    fireEvent.click(within(openMenu()).getByRole('menuitem', { name: '复制全部数据（JSON）' }))
    await waitFor(() => expect(copy).toHaveBeenCalledTimes(1))
    const sent = JSON.parse(copy.mock.calls[0][0])
    expect(sent.kind).toBe('psp-diagnostics')
    expect(sent.api).toEqual(snap)
    expect(sent.page.expected_polls_min).toBe(1773)
    expect(sent.page.findings.map((f: { id: string }) => f.id)).toContain('lifecycle_errors')
  })
})

describe('clearing the statistics', () => {
  const previous: MetricsSnapshot = productionMetrics({
    counters: [c('psp_poll_total', 1774), c('psp_poll_error_total', 4242)],
  })

  it('confirms, clears, and keeps the closed window to view and download', async () => {
    serve(productionSnapshot())
    api.post.mockImplementation(async (url: string) => {
      if (url !== '/admin/diagnostics/metrics/reset') throw new Error(`unexpected POST ${url}`)
      served.snap = productionSnapshot({ window_ms: 5_000, since_unix_ms: previous.since_unix_ms + previous.window_ms, counters: [] })
      return { data: { reset: true, previous } }
    })
    mount()
    await loaded()
    fireEvent.click(within(openMenu()).getByRole('menuitem', { name: '清零统计…' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/diagnostics/metrics/reset'))
    expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ destructive: true, confirmText: '清零' }))

    const kept = await screen.findByTestId('reset-kept')
    expect(kept.textContent).toMatch(/已清零。清零前 2 天 11 小时 的统计可以下载保存/)
    expect(within(kept).getByRole('button', { name: '下载清零前的数据' })).toBeTruthy()

    const raw = openRaw()
    fireEvent.click(within(raw).getByRole('button', { name: /^清零前/ }))
    expect(within(raw).getByText(/正在查看清零前的数据/)).toBeTruthy()
    search(raw, 'psp_poll_error_total')
    expect(within(raw).getByText('4,242')).toBeTruthy()
  })
})

describe('the blackout', () => {
  const young = () => {
    const snap = productionSnapshot({
      window_ms: 30_000,
      counters: [c('psp_push_client_config_error_total', 2), c('psp_push_client_config_total', 2)],
    })
    return { ...snap, uptime_ms: 30_000 }
  }

  it('holds the cards\' numbers back but still lists what has already happened', async () => {
    serve(young())
    mount()
    await loaded()
    expect(statusLine().getAttribute('data-tone')).toBe('attention')
    expect(screen.getByTestId('finding-push_errors')).toBeTruthy()
    expect(within(card('poll')).getByText('第一轮采集尚未完成，暂不显示数字。')).toBeTruthy()
    expect(within(card('poll')).queryByText('已运行')).toBeNull()
    expect(within(card('floor')).queryByText('已刷新')).toBeNull()
  })

  it('still opens the raw metrics, with a note that a zero means nothing yet', async () => {
    serve(young())
    mount()
    await loaded()
    const raw = openRaw()
    expect(within(raw).getByText('统计开始还不到一轮采集，数字仅供参考，0 不代表正常。')).toBeTruthy()
  })

  it('will not clear a window that has not finished one poll, and says why', async () => {
    serve(young())
    mount()
    await loaded()
    const menu = openMenu()
    const item = within(menu).getByRole('menuitem', { name: /清零统计…/ })
    expect(item.getAttribute('aria-disabled')).toBe('true')
    expect(within(menu).getByText('统计开始还不到一轮采集，暂时无需清零')).toBeTruthy()
  })
})

describe('panel facts', () => {
  // No 3X-UI requests, no node activity, and live IPs missing on some polls.
  const quiet = () => productionSnapshot({
    counters: [
      c('psp_poll_total', 1774),
      c('psp_lifecycle_sync_total', 10),
      c('psp_push_client_config_total', 3),
      c('psp_poll_floor_push_enqueued_total', 3),
      c('psp_live_ip_users_incomplete_total', 50),
    ],
    gauges: [g('psp_poll_interval_ms', 120_000), g('psp_push_sem_capacity', 8)],
    histograms: [hist('psp_poll_ms', { count: 1774, p50: 800, p95: 2000 })],
  })

  it('says a card is not in use, and explains S-UI, only from a complete server list', async () => {
    serve(quiet(), [SUI])
    mount()
    await loaded()
    await waitFor(() => expect(within(card('panel_api')).getByTestId('state-badge').textContent).toBe('未接入'))
    expect(within(card('node')).getByTestId('state-badge').textContent).toBe('未接入')
    expect(within(screen.getByTestId('finding-liveip_incomplete')).getByText(/已接入 S-UI 面板/)).toBeTruthy()
  })

  it('says neither when the list it read was incomplete', async () => {
    serve(quiet(), [SUI], 300)
    mount()
    await loaded()
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/servers', expect.anything()))
    // Give the list read a chance to land before asserting its absence.
    await new Promise(r => setTimeout(r, 0))
    expect(screen.queryByText('未接入')).toBeNull()
    expect(within(screen.getByTestId('finding-liveip_incomplete')).queryByText(/已接入 S-UI 面板/)).toBeNull()
  })
})
