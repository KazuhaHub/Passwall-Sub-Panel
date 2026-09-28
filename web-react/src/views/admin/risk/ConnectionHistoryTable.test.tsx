/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import type { ConnectionRecord } from '@/api/riskCenter'
import ConnectionHistoryTable from './ConnectionHistoryTable'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
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

const record = (over: Partial<ConnectionRecord>): ConnectionRecord => ({
  user_id: 7, upn: 'alice', display_name: 'Alice', panel_id: 1, panel_name: 'jp-1', node: 'guid-a',
  source_key: '2001:db8:1:2::/64', ip: '2001:db8:1:2::5', exclusion: '',
  region: { country_code: 'CN', country: 'China', region: 'Guangdong', region_code: 'GD', city: 'Shenzhen' },
  first_seen_ms: 1_789_000_000_000, last_seen_ms: 1_790_000_000_000, count: 12, ...over,
})

function serve(items: ConnectionRecord[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/connections') return { data: { items, total: items.length, page: 1, page_size: 25 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function mount() {
  render(
    <ThemeProvider theme={theme}><ConnectionHistoryTable userId={7} /></ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(() => {
  cleanup()
  useSiteStore.setState({ timezone: '' })
})

// A panel timezone no CI browser runs in, so a time rendered in the
// browser's zone cannot pass for one rendered in the panel's.
const PANEL_TZ = 'Pacific/Chatham'

describe('ConnectionHistoryTable', () => {
  it("reads one account's history and shows each source it was judged from", async () => {
    serve([record({}), record({ source_key: '203.0.113.9', ip: '203.0.113.9', exclusion: 'shared', count: 1, region: null })])
    mount()

    const row = (await screen.findByText('2001:db8:1:2::5')).closest('tr') as HTMLElement
    expect(within(row).getByText('来源 2001:db8:1:2::/64')).toBeTruthy()
    expect(within(row).getByText('12')).toBeTruthy()
    expect(within(row).getByText('参与判定')).toBeTruthy()
    const shared = screen.getByText('203.0.113.9').closest('tr') as HTMLElement
    expect(within(shared).getByText('共享出口')).toBeTruthy()

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/risk-center/connections',
      expect.objectContaining({ params: expect.objectContaining({ user_id: 7, page: 1 }) })))
    // The one table that keeps IP addresses says so, and for how long.
    expect(screen.getByText(/含 IP，仅管理员可见，默认保留 7 天/)).toBeTruthy()
  })

  it('says so when there is no history', async () => {
    serve([])
    mount()
    expect(await screen.findByText('没有符合条件的记录')).toBeTruthy()
  })

  it('prints first and last sightings in panel time', async () => {
    useSiteStore.setState({ timezone: PANEL_TZ })
    serve([record({})])
    mount()
    const row = (await screen.findByText('2001:db8:1:2::5')).closest('tr') as HTMLElement
    expect(within(row).getByText(formatMsDualTz(1_789_000_000_000, PANEL_TZ))).toBeTruthy()
    expect(within(row).getByText(formatMsDualTz(1_790_000_000_000, PANEL_TZ))).toBeTruthy()
  })
})
