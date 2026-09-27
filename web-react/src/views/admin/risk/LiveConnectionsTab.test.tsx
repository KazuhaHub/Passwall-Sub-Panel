/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { LIVE_REFRESH_TIMEOUT_MS, type LiveSnapshotInfo, type LiveUser, type LiveView } from '@/api/riskCenter'
import LiveConnectionsTab from './LiveConnectionsTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack, default: () => null }))
// t over the REAL zh-CN admin bundle, so a key the tab asks for but the
// bundle lacks shows up as its raw key.
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

const alice: LiveUser = {
  user_id: 7, upn: 'alice', display_name: 'Alice', stale_addresses: 0, unread_panels: 0,
  connections: [
    {
      panel_id: 1, panel_name: 'jp-1', node: 'guid-a', source_key: '2001:db8:1:2::/64', ip: '2001:db8:1:2::5',
      exclusion: '', seen_at: 1_790_000_000,
      region: { country_code: 'CN', country: 'China', region: 'Guangdong', region_code: 'GD', city: 'Shenzhen' },
      devices: [{ label: 'iOS 17.5 · iPhone15,2', device_id4: 'ab12', client_type: 'clash-meta',
        ua: 'ClashMetaForAndroid/2.10', fetches: 3, last_at_ms: 1_790_000_000_000 }],
    },
    {
      panel_id: 1, panel_name: 'jp-1', node: 'guid-a', source_key: '203.0.113.9', ip: '203.0.113.9',
      exclusion: 'infra', seen_at: 1_790_000_000, region: null, devices: [],
    },
  ],
}

function view(snap: Partial<LiveSnapshotInfo> = {}, over: Partial<LiveView> = {}): LiveView {
  return {
    snapshot: {
      taken_at: '2026-09-26T10:00:05Z', source: 'poll', age_seconds: 125, stale: false, stale_after_seconds: 900,
      panels_asked: 3, panels_unread: [], panels_unsupported: [], unreferenced_nodes: 0,
      users: 1, connections: 2, truncated: 0, ...snap,
    },
    refresh: { cooldown_seconds: 30, available_in_seconds: 0 },
    device_window_hours: 24, devices_unavailable: false,
    panels: [{ id: 1, name: 'jp-1' }, { id: 2, name: 'hk-1' }, { id: 4, name: 'sui-1' }],
    items: [alice], total: 1, page: 1, page_size: 25,
    ...over,
  }
}

function serve(v: LiveView | ((params: Record<string, unknown>) => LiveView)) {
  api.get.mockImplementation(async (url: string, cfg: { params?: Record<string, unknown> } = {}) => {
    if (url === '/admin/risk-center/live') return { data: typeof v === 'function' ? v(cfg.params ?? {}) : v }
    if (url === '/admin/users') return { data: { items: [], total: 0, page: 1, page_size: 50 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function liveReads(): number {
  return api.get.mock.calls.filter(([u]) => u === '/admin/risk-center/live').length
}

function mount() {
  render(
    <ThemeProvider theme={theme}><LiveConnectionsTab /></ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

async function expandAlice() {
  fireEvent.click(await screen.findByRole('button', { name: '查看连接' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(cleanup)

describe('LiveConnectionsTab', () => {
  it('shows the snapshot time and source', async () => {
    serve(view())
    mount()
    expect(await screen.findByText(/^快照 .+（2 分钟前，来自定时轮询）$/)).toBeTruthy()
    cleanup()

    serve(view({ source: 'refresh', age_seconds: 20 }))
    mount()
    expect(await screen.findByText(/^快照 .+（刚刚，手动刷新）$/)).toBeTruthy()
  })

  it('says so when there is no snapshot yet', async () => {
    serve(view({ taken_at: null, source: '', stale: true }, { items: [], total: 0 }))
    mount()
    expect(await screen.findByText('还没有快照：启动后第一次流量轮询完成时出现，也可以立即刷新。')).toBeTruthy()
    // "Nobody is connected" would be a claim the page cannot make yet.
    expect(screen.queryByText('此刻没有在连的账号')).toBeNull()
  })

  it('warns when stale', async () => {
    serve(view({ stale: true, stale_after_seconds: 900 }))
    mount()
    expect(await screen.findByText('快照已超过 15 分钟，可能已经过时。')).toBeTruthy()
  })

  it('lists unread panels by name', async () => {
    serve(view({ panels_unread: [{ id: 2, name: 'hk-1' }] }))
    mount()
    expect(await screen.findByText('1 块面板读取失败，其上的连接未列出：hk-1')).toBeTruthy()
  })

  // S-UI has no live read at all. Drawn as a failure it would sit there
  // forever and teach the admin to ignore the one alert that matters.
  it('shows unsupported panels as info, not failure', async () => {
    serve(view({ panels_unsupported: [{ id: 4, name: 'sui-1' }] }))
    mount()
    const text = await screen.findByText('1 块面板不提供在线地址（如 S-UI），其上的连接无法列出：sui-1')
    const alert = text.closest('.MuiAlert-root') as HTMLElement
    expect(alert.className).toContain('Info')
    expect(alert.className).not.toMatch(/Error|Warning/)
    expect(screen.queryByText(/读取失败/)).toBeNull()
  })

  // With no previous reference, FreshLiveIPsWithin waives only the "has the
  // node rescanned since" check: the live window still applies to each
  // node's newest scan. What that trusts is that every node is still
  // scanning — so a node that has stopped shows its last scan's addresses,
  // not everything the upstream remembered for 30 minutes.
  it('notes unreferenced nodes, and says what is trusted for them', async () => {
    serve(view({ unreferenced_nodes: 2 }))
    mount()
    expect(await screen.findByText(
      '有 2 个节点还没有上一次的参照时间（启动后第一次读取），无法确认它们是否仍在扫描，这次一律当作仍在扫描：'
      + '已经停止扫描的节点，它最后一次扫描（最多 30 分钟前）看到的地址也会当作在连。')).toBeTruthy()
  })

  it('says how many connections the per-account cap left out', async () => {
    serve(view({ truncated: 5 }))
    mount()
    expect(await screen.findByText('有 5 个连接超出单账号 64 个的上限，未列出')).toBeTruthy()
  })

  // A device is a guess from the account's own fetches, never something the
  // connection carried; the page must say so beside every device it shows.
  it('labels devices as inferred', async () => {
    serve(view())
    mount()
    await expandAlice()
    const row = screen.getByText('2001:db8:1:2::5').closest('tr') as HTMLElement
    expect(within(row).getByText('iOS 17.5 · iPhone15,2')).toBeTruthy()
    expect(within(row).getByText('推断')).toBeTruthy()
  })

  // A fetch through PSP's own relay is every account's: nothing about the
  // device behind it can be told, and "not inferable" would hide why.
  it('shows via-relay for infra sources', async () => {
    serve(view())
    mount()
    await expandAlice()
    const row = screen.getByText('203.0.113.9').closest('tr') as HTMLElement
    expect(within(row).getByText('经中转，无法推断')).toBeTruthy()
    expect(within(row).queryByText('无法推断')).toBeNull()
  })

  it('refresh 429 shows the retry seconds', async () => {
    serve(view())
    api.post.mockRejectedValue({
      isAxiosError: true,
      response: { status: 429, data: { error: 'refresh_throttled', reason: 'cooldown', retry_after_seconds: 12 } },
    })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '立即刷新' }))

    await waitFor(() => expect(snack).toHaveBeenCalledWith('刷新过于频繁，请 12 秒后再试', 'warning'))
    expect(api.post).toHaveBeenCalledWith('/admin/risk-center/live/refresh', undefined,
      { _skipErrorToast: true, timeout: LIVE_REFRESH_TIMEOUT_MS })
  })

  it('a refresh already running says so', async () => {
    serve(view())
    api.post.mockRejectedValue({
      isAxiosError: true,
      response: { status: 429, data: { error: 'refresh_throttled', reason: 'in_progress', retry_after_seconds: 2 } },
    })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '立即刷新' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('正在刷新，请稍候', 'info'))
  })

  it('just_polled shows its message', async () => {
    serve(view())
    api.post.mockResolvedValue({ data: {
      refreshed: false, reason: 'just_polled', taken_at: '2026-09-26T10:00:05Z', source: 'poll',
      panels_asked: 3, panels_unread: [], panels_unsupported: [], connections: 2,
    } })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '立即刷新' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('刚完成一次轮询，已显示它的快照', 'info'))
  })

  // A refresh replaces the snapshot every page of the view reads from, so
  // the page re-reads it rather than showing the one from before the click.
  it('a refresh re-reads the snapshot and reports what it read', async () => {
    serve(view())
    api.post.mockResolvedValue({ data: {
      refreshed: true, reason: '', taken_at: '2026-09-26T10:01:00Z', source: 'refresh',
      panels_asked: 3, panels_unread: [], panels_unsupported: [], connections: 9,
    } })
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '立即刷新' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已刷新：3 块面板，9 个连接', 'success'))
    await waitFor(() => expect(liveReads()).toBe(2))
  })

  // An empty page is not always "nobody": a filter can match no one while
  // the snapshot lists many, and an unread panel's connections are unknown,
  // not absent. Each says what it is.
  it('says "nobody is connected" only for an empty, fully read, unfiltered snapshot', async () => {
    serve(view({ users: 0, connections: 0 }, { items: [], total: 0 }))
    mount()
    expect(await screen.findByText('此刻没有在连的账号')).toBeTruthy()
  })

  it('a filter that matches nobody says no match, not "nobody is connected"', async () => {
    serve(params => (params.exclusion === 'shared' ? view({}, { items: [], total: 0 }) : view()))
    mount()
    await screen.findByText('alice')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '来源' }))
    fireEvent.click(await screen.findByRole('option', { name: '共享出口' }))

    expect(await screen.findByText('没有符合条件的连接')).toBeTruthy()
    expect(screen.queryByText('此刻没有在连的账号')).toBeNull()
  })

  it('an empty snapshot with a panel unread says the rest is unknown', async () => {
    serve(view({ users: 0, connections: 0, panels_unread: [{ id: 2, name: 'hk-1' }] }, { items: [], total: 0 }))
    mount()
    expect(await screen.findByText('没有列出任何连接，但有 1 块面板读取失败，那里的连接无从得知')).toBeTruthy()
    expect(screen.queryByText('此刻没有在连的账号')).toBeNull()
  })

  it('filters by source', async () => {
    serve(view())
    mount()
    await screen.findByText('alice')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '来源' }))
    fireEvent.click(await screen.findByRole('option', { name: '已排除' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/risk-center/live',
      expect.objectContaining({ params: expect.objectContaining({ exclusion: 'excluded', page: 1 }) })))
  })
})
