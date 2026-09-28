/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import type { QueryClient } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import type { GeoWhy } from '@/api/geoAnomalies'
import type { FlagRecord } from '@/api/riskCenter'
import RecordsTab from './RecordsTab'

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
  v: 3, spots: [{ cc: 'CN', region: 'Guangdong', city: 'Shenzhen', n: 2 }, { cc: 'CN', region: 'Hunan', city: '', n: 1 }],
  excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
  coverage: { placed: 3, unplaced: 0, region_known: 3, city_known: 3 }, networks: 3,
  spread: { countries: 1, regions: 3, region_country: 'CN', cities: 3, city_country: 'CN' }, why: w,
})
const rec = (over: Partial<FlagRecord>): FlagRecord => ({
  id: 1, user_id: 7, upn: 'alice', display_name: 'Alice', source: 'geo', event: 'enter_suspect',
  level: 'suspect', prev_level: '', state: 'suspect', code: 'suspect', params: null, at_ms: 1_790_000_000_000,
  ...over,
})
const geoParams = { over: 2, under: 0, ban_over: 0, flagged: false, tier: 'region', evidence: evidence(why({})) }
const dismissal = rec({
  id: 5, source: 'review', event: 'dismissed', level: '', prev_level: '', state: '', code: 'dismissed',
  // A name is data, not a code: the fixture's carries an "@" so the raw-code
  // check below cannot mistake it for one.
  params: { by: 3, levels: { geo: 'flagged' } }, actor_upn: 'root@corp',
})

let client: QueryClient

function serve(items: FlagRecord[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/flags') return { data: { items, total: items.length, page: 1, page_size: 25 } }
    if (url === '/admin/users') return { data: { items: [], total: 0, page: 1, page_size: 50 } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function Where() {
  const loc = useLocation()
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

// Both instances live under the page's router: the page's keeps its filters
// in the URL, the drawer's must not touch it.
function mount(props: { userId?: number; compact?: boolean } = {}, search = '?tab=records') {
  client = makeTestQueryClient()
  render(
    <MemoryRouter initialEntries={[`/admin/risk${search}`]}>
      <ThemeProvider theme={theme}>
        <Routes>
          <Route path="/admin/risk" element={<><RecordsTab {...props} /><Where /></>} />
        </Routes>
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(client) },
  )
}

function location(): string {
  return screen.getByTestId('location').textContent ?? ''
}

function urlParams(): URLSearchParams {
  return new URLSearchParams(location().split('?')[1] ?? '')
}

function flagParams(): Record<string, unknown>[] {
  return api.get.mock.calls
    .filter(([u]) => u === '/admin/risk-center/flags')
    .map(([, cfg]) => (cfg as { params: Record<string, unknown> }).params)
}

async function pick(combobox: string, option: string) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: combobox }))
  fireEvent.click(await screen.findByRole('option', { name: option }))
}

/** The expanded detail rows, in order. */
function details(): HTMLElement[] {
  return screen.queryAllByTestId('record-detail')
}

/** Every text node under `el` outside the raw-data block, trimmed, non-empty. */
function textsOutsideRaw(el: HTMLElement): string[] {
  const out: string[] = []
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n.parentElement?.closest('pre')) continue
    const s = (n.textContent ?? '').trim()
    if (s) out.push(s)
  }
  return out
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useSiteStore.setState({ timezone: '' })
})

// A panel timezone no CI browser runs in, so a time rendered in the
// browser's zone cannot pass for one rendered in the panel's.
const PANEL_TZ = 'Pacific/Chatham'

describe('RecordsTab', () => {
  it('renders a geo suspect record with its over/need suffix from params', async () => {
    serve([rec({ params: geoParams })])
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

  // An admin's dismissal is a record too: who, and what they accepted.
  it('a review record names the admin and the accepted levels', async () => {
    serve([dismissal])
    mount()
    const cell = await screen.findByText('由 root@corp 忽略（当时：异地并发 已标记）')
    const row = cell.closest('tr') as HTMLElement
    expect(within(row).getByText('管理员处理')).toBeTruthy()
    expect(within(row).getByText('忽略')).toBeTruthy()
  })

  // A record without params has nothing to expand, and its reason cell says
  // what changed rather than printing the code.
  it('a record without params says its event and has nothing to expand', async () => {
    serve([rec({ id: 9, source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'region',
      params: null })])
    mount()
    const row = (await screen.findByText('alice')).closest('tr') as HTMLElement
    expect(within(row).getAllByText('自动临时暂停').length).toBe(3)
    expect(within(row).queryByText('region')).toBeNull()
    expect(screen.queryByRole('button', { name: '查看证据' })).toBeNull()
  })

  // The expanded row reads like the rest of the page: the sentence, then the
  // record's numbers under their names — places for a geo record. The JSON
  // is the raw data behind them, one more click away.
  it('expands a geo record to its sentence, places and named values; JSON only on demand', async () => {
    serve([rec({ params: geoParams })])
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }))
    const [detail] = details()
    const d = within(detail)
    expect(d.getByText('同时在 CN 的 3 个省 / 州（容错 2），连续 2 / 6 次')).toBeTruthy()
    expect(d.getByText(/CN 3: Guangdong 2 \(Shenzhen 2\) · Hunan 1/)).toBeTruthy()
    expect(d.getByText('连续超限次数')).toBeTruthy()
    expect(d.getByText('跨省')).toBeTruthy()
    expect(detail.querySelector('pre')).toBeNull()
    expect(d.queryByText(/"ban_over": 0/)).toBeNull()

    fireEvent.click(d.getByRole('button', { name: '原始数据' }))
    expect(d.getByText(/"ban_over": 0/)).toBeTruthy()
    fireEvent.click(d.getByRole('button', { name: '收起原始数据' }))
    expect(d.queryByText(/"ban_over": 0/)).toBeNull()
  })

  it('expands a risk record to its kind’s evidence', async () => {
    serve([rec({ source: 'devices', event: 'enter_flagged', level: 'flagged', state: 'flagged', code: 'over',
      params: {
        v: 1, window_days: 7, window_start: '2026-09-20', min_days: 3, max_devices: 3, recurrent: 5, distinct: 6,
        devices: [{ label: 'iPhone 15', hwid4: 'ab12', days: 5, last_ms: 1_790_000_000_000, client: '', recurrent: true }],
        fetches_with_hwid: 9, fetches_without: 1, clients: [],
      } })])
    mount()
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }))
    const d = within(details()[0])
    expect(d.getByText('iPhone 15')).toBeTruthy()
    expect(d.getByText('#ab12')).toBeTruthy()
  })

  // No code reaches the admin as a word: every value in an expanded row is a
  // label, a number, a time or a name. Only the raw data shows the wire.
  it('no expanded row shows a raw code outside the raw data', async () => {
    useSiteStore.setState({ timezone: PANEL_TZ })
    serve([
      rec({ params: geoParams }),
      rec({ id: 2, source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'region',
        params: { tier: 'region', spread: 4, duration_minutes: 30 } }),
      rec({ id: 3, source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
        code: 'replaced', params: { replaced_by: 'geo_anomaly' } }),
      rec({ id: 4, source: 'geo_auto', event: 'auto_lifted_expiry', level: '', prev_level: 'suspended', state: '',
        code: 'expired', params: { duration_minutes: 30, suspended_at_ms: 1_789_990_000_000 } }),
      dismissal,
      rec({ id: 6, source: 'review', event: 'trusted', level: '', state: '', code: 'trusted', params: { by: 3 } }),
    ])
    mount()
    for (const b of await screen.findAllByRole('button', { name: '查看证据' })) fireEvent.click(b)
    expect(details()).toHaveLength(6)
    for (const detail of details()) {
      fireEvent.click(within(detail).getByRole('button', { name: '原始数据' }))
      expect(detail.querySelector('pre')).not.toBeNull()
      const raw = textsOutsideRaw(detail).filter(s => /^[a-z_]+$/.test(s))
      expect(raw).toEqual([])
    }
    const all = details().map(d => d.textContent ?? '').join('\n')
    expect(all).toContain('异地人工暂停')
    expect(all).toContain('由 #3 设为信任')
    // A time stored inside a record reads in panel time, like the row's own.
    expect(all).toContain(formatMsDualTz(1_789_990_000_000, PANEL_TZ))
  })

  // The bar holds what an admin filters by most: who, which source, when.
  // Level and change sit behind 更多筛选 — still the server's filters, so
  // still in the query key.
  it('level and event live behind 更多筛选 and still enter the query key', async () => {
    serve([])
    mount()
    await screen.findByText('没有符合条件的记录')
    expect(screen.queryByRole('combobox', { name: '级别' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: '变化' })).toBeNull()

    const more = screen.getByRole('button', { name: '更多筛选' })
    fireEvent.click(more)
    await pick('级别', '已解除')
    await waitFor(() => expect(flagParams().some(p => p.level === 'cleared')).toBe(true))
    await pick('变化', '忽略')
    await waitFor(() => expect(flagParams().some(p => p.level === 'cleared' && p.event === 'dismissed')).toBe(true))

    const keys = client.getQueryCache().findAll({ queryKey: ['private'] }).map(q => JSON.stringify(q.queryKey))
    expect(keys.some(k => k.includes('"flags"') && k.includes('"level":"cleared"') && k.includes('"event":"dismissed"')))
      .toBe(true)
    // The button says how many of its filters are set.
    expect(within(more.closest('.MuiBadge-root') as HTMLElement).getByText('2')).toBeTruthy()
    // A filter change starts again at the first page.
    expect(flagParams().at(-1)).toMatchObject({ page: 1 })
  })

  it('offers the admin review source', async () => {
    serve([])
    mount()
    await screen.findByText('没有符合条件的记录')
    await pick('来源', '管理员处理')
    await waitFor(() => expect(flagParams().some(p => p.source === 'review')).toBe(true))
  })

  // datetime-local is read in the browser's timezone, while every time the
  // page PRINTS is the panel's; the bounds say which one they are.
  it('labels the time bounds as browser time', async () => {
    serve([])
    mount()
    await screen.findByText('没有符合条件的记录')
    expect(screen.getByLabelText('开始（浏览器时间）')).toBeTruthy()
    expect(screen.getByLabelText('结束（浏览器时间）')).toBeTruthy()
  })

  // The page's filters are its URL, so opening an account and coming Back,
  // following a drawer link, a reload or a copied link shows the same page.
  it('keeps the page instance’s filters in the URL, and they survive a remount', async () => {
    serve([rec({})])
    mount({}, '?tab=records&user=9&rec_page=2')
    await screen.findByText('alice')
    await pick('来源', '管理员处理')
    await waitFor(() => expect(urlParams().get('rec_source')).toBe('review'))
    // A new filter starts at the first page; the page's other params stay.
    expect(urlParams().has('rec_page')).toBe(false)
    expect(urlParams().get('tab')).toBe('records')
    expect(urlParams().get('user')).toBe('9')

    fireEvent.change(screen.getByLabelText('开始（浏览器时间）'), { target: { value: '2026-09-01T08:00' } })
    await waitFor(() => expect(urlParams().get('rec_since')).toBe('2026-09-01T08:00'))
    fireEvent.click(screen.getByRole('button', { name: '更多筛选' }))
    await pick('变化', '忽略')
    await waitFor(() => expect(urlParams().get('rec_event')).toBe('dismissed'))
    const want = {
      page: 1, page_size: 25, source: 'review', event: 'dismissed',
      since: new Date(Date.parse('2026-09-01T08:00')).toISOString(),
    }
    await waitFor(() => expect(flagParams().at(-1)).toEqual(want))
    const search = `?${urlParams().toString()}`
    cleanup()

    vi.clearAllMocks()
    serve([rec({})])
    mount({}, search)
    await screen.findByText('alice')
    expect(flagParams().at(-1)).toEqual(want)
    expect(screen.getByRole('combobox', { name: '来源' }).textContent).toBe('管理员处理')
    expect((screen.getByLabelText('开始（浏览器时间）') as HTMLInputElement).value).toBe('2026-09-01T08:00')
  })

  // The drawer's list is one account's and lives only as long as the
  // drawer: its filters are its own, and the page's URL is not its to write.
  it('the compact instance writes nothing to the URL', async () => {
    serve([rec({})])
    mount({ userId: 7, compact: true }, '?tab=queue&user=7')
    await screen.findAllByText('进入疑似')
    await pick('来源', '管理员处理')
    await waitFor(() => expect(flagParams().some(p => p.source === 'review' && p.user_id === 7)).toBe(true))
    fireEvent.click(screen.getByRole('button', { name: '更多筛选' }))
    await pick('级别', '已标记')
    await waitFor(() => expect(flagParams().some(p => p.level === 'flagged')).toBe(true))
    expect(location()).toBe('/admin/risk?tab=queue&user=7')
  })

  // Inside the drawer the account is fixed: no user filter, no user column,
  // and every request names the account.
  it('scopes a compact list to one account', async () => {
    serve([rec({ params: null })])
    mount({ userId: 7, compact: true })
    await screen.findAllByText('进入疑似')
    expect(flagParams().every(p => p.user_id === 7 && p.page_size === 50)).toBe(true)
    expect(screen.queryByRole('columnheader', { name: '用户' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: '用户' })).toBeNull()
  })

  // Every other page reads times in the panel's timezone; the records read
  // the browser's, so one instant printed two ways across the admin.
  it("prints each record's time in panel time", async () => {
    useSiteStore.setState({ timezone: PANEL_TZ })
    serve([rec({})])
    mount()
    expect(await screen.findByText(formatMsDualTz(1_790_000_000_000, PANEL_TZ))).toBeTruthy()
  })
})
