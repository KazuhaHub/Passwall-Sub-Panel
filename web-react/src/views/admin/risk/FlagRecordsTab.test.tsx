/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import type { QueryClient } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import type { GeoWhy } from '@/api/geoAnomalies'
import type { FlagRecord } from '@/api/riskCenter'
import FlagRecordsTab from './FlagRecordsTab'

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

const why = (over: Partial<GeoWhy>): GeoWhy => ({
  code: 'suspect', tier: 'region', scope: 'city', tol: { countries: 1, regions: 2, cities: 3 },
  flag_after: 6, clear_after: 6, min_placed_ratio: 0.5, ...over,
})
const evidence = (w: GeoWhy) => ({
  v: 3, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
  coverage: { placed: 3, unplaced: 0, region_known: 3, city_known: 3 }, networks: 3,
  spread: { countries: 1, regions: 3, region_country: 'CN', cities: 3, city_country: 'CN' }, why: w,
})
const rec = (over: Partial<FlagRecord>): FlagRecord => ({
  id: 1, user_id: 7, upn: 'alice', display_name: 'Alice', source: 'geo', event: 'enter_suspect',
  level: 'suspect', prev_level: '', state: 'suspect', code: 'suspect', params: null, at_ms: 1_790_000_000_000,
  ...over,
})

let client: QueryClient

function serve(items: FlagRecord[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/flags') return { data: { items, total: items.length, page: 1, page_size: 25 } }
    if (url === '/admin/users') return { data: { items: [], total: 0, page: 1, page_size: 50 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function mount(props: { userId?: number; compact?: boolean } = {}) {
  client = makeTestQueryClient()
  render(
    <ThemeProvider theme={theme}><FlagRecordsTab {...props} /></ThemeProvider>,
    { wrapper: queryWrapper(client) },
  )
}

function flagParams(): Record<string, unknown>[] {
  return api.get.mock.calls
    .filter(([u]) => u === '/admin/risk-center/flags')
    .map(([, cfg]) => (cfg as { params: Record<string, unknown> }).params)
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(cleanup)

describe('FlagRecordsTab', () => {
  it('renders a geo suspect record with its over/need suffix from params', async () => {
    serve([rec({ params: { over: 2, under: 0, ban_over: 0, flagged: false, tier: 'region', evidence: evidence(why({})) } })])
    mount()
    const cell = await screen.findByText('同时在 CN 的 3 个省 / 州（容错 2），连续 2 / 6 次')
    const row = cell.closest('tr') as HTMLElement
    expect(within(row).getByText('进入疑似')).toBeTruthy()
    expect(within(row).getByText('异地并发')).toBeTruthy()
    expect(within(row).getByText('alice')).toBeTruthy()
  })

  it('a flagged_clearing record with its under count', async () => {
    serve([rec({ event: 'enter_flagged', level: 'flagged', state: 'clean', code: 'flagged_clearing',
      params: { over: 0, under: 3, ban_over: 0, flagged: true, tier: 'region',
        evidence: evidence(why({ code: 'flagged_clearing' })) } })])
    mount()
    expect(await screen.findByText('已连续 3 / 6 次在容错内，满 6 次后解除；标记原因：跨省')).toBeTruthy()
  })

  it('a risk record through riskCodeText', async () => {
    serve([rec({ source: 'devices', event: 'enter_flagged', level: 'flagged', state: 'flagged', code: 'over',
      params: { v: 1, recurrent: 5, max_devices: 3, distinct: 6 } })])
    mount()
    const cell = await screen.findByText('5 台常用设备（上限 3）')
    expect(within(cell.closest('tr') as HTMLElement).getByText('设备数')).toBeTruthy()
  })

  it('geo_auto codes with params', async () => {
    serve([
      rec({ id: 2, source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'region',
        params: { tier: 'region', spread: 4, duration_minutes: 30 } }),
      rec({ id: 3, source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
        code: 'replaced', params: { replaced_by: 'expired' } }),
    ])
    mount()
    expect(await screen.findByText('同一国家同时在 4 个省或州，暂停 30 分钟')).toBeTruthy()
    expect(screen.getByText('被「已过期」暂停替换')).toBeTruthy()
    expect(screen.getByText('被另一种暂停替换')).toBeTruthy()
  })

  // Params are the record's evidence of that moment, shown raw on demand; a
  // record with none has nothing to expand.
  it('expands a record to its params', async () => {
    serve([
      rec({ params: { over: 2, under: 0, ban_over: 0, flagged: false, tier: 'region', evidence: evidence(why({})) } }),
      rec({ id: 9, source: 'geo_auto', event: 'auto_lifted_admin', level: '', prev_level: 'suspended', state: '',
        code: 'admin_resume', params: null }),
    ])
    mount()
    const buttons = await screen.findAllByRole('button', { name: '查看证据' })
    expect(buttons).toHaveLength(1)
    fireEvent.click(buttons[0])
    expect(await screen.findByText(/"ban_over": 0/)).toBeTruthy()
  })

  // Each filter changes what the server answers, so each must be part of
  // the query key: otherwise one filter's rows would be served under another.
  it('filters go into the query key', async () => {
    serve([])
    mount()
    await screen.findByText('没有符合条件的记录')

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '来源' }))
    fireEvent.click(await screen.findByRole('option', { name: '自动临时暂停' }))
    await waitFor(() => expect(flagParams().some(p => p.source === 'geo_auto')).toBe(true))

    fireEvent.mouseDown(screen.getByRole('combobox', { name: '级别' }))
    fireEvent.click(await screen.findByRole('option', { name: '已解除' }))
    await waitFor(() => expect(flagParams().some(p => p.source === 'geo_auto' && p.level === 'cleared')).toBe(true))

    const keys = client.getQueryCache().findAll({ queryKey: ['private'] }).map(q => JSON.stringify(q.queryKey))
    expect(keys.some(k => k.includes('"flags"') && k.includes('"source":"geo_auto"') && k.includes('"level":"cleared"'))).toBe(true)
    // A filter change starts again at the first page.
    expect(flagParams().at(-1)).toMatchObject({ page: 1 })
  })

  // Inside the lookup the account is fixed: no user filter, no user column,
  // and every request names the account.
  it('scopes a compact list to one account', async () => {
    serve([rec({ params: null })])
    mount({ userId: 7, compact: true })
    await screen.findByText('进入疑似')
    expect(flagParams().every(p => p.user_id === 7 && p.page_size === 50)).toBe(true)
    expect(screen.queryByRole('columnheader', { name: '用户' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: '用户' })).toBeNull()
  })
})
