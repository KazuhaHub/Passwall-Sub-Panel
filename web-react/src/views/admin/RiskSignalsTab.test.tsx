/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import type { RiskSignal, RiskUserRow } from '@/api/riskSignals'
import RiskSignalsTab from './RiskSignalsTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
// t over the REAL zh-CN admin bundle, flattened as the SPA registers it, so
// the assertions read the Chinese an admin sees and a key the component asks
// for but the bundle lacks shows up as its raw key.
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

function mount(onOpenGeo = vi.fn()) {
  render(
    <ThemeProvider theme={theme}>
      <RiskSignalsTab onOpenGeo={onOpenGeo} />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
  return onOpenGeo
}

function serve(items: RiskUserRow[]) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-signals') return { data: { items } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function sig(kind: string, state: RiskSignal['state'], over: Partial<RiskSignal> = {}): RiskSignal {
  return { kind: kind as RiskSignal['kind'], state, code: 'within', evidence: null, updated_at_ms: 1_700_000_000_000, ...over }
}

function user(id: number, upn: string, signals: RiskSignal[], over: Partial<RiskUserRow> = {}): RiskUserRow {
  return { user_id: id, upn, geo: null, signals, ...over }
}

function rowOf(name: string): HTMLElement {
  return screen.getByText(name).closest('tr') as HTMLElement
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('RiskSignalsTab read states', () => {
  it('reports an unwired worker as its own message, not as an empty table', async () => {
    // 503 means this build is not computing signals. "Nothing computed yet"
    // would describe a watched fleet when nothing is being watched.
    api.get.mockRejectedValue({ isAxiosError: true, response: { status: 503 } })
    mount()

    await waitFor(() => expect(screen.getByText('本部署未启用风险信号。')).toBeTruthy())
    expect(screen.queryByText(/还没有计算结果/)).toBeNull()
  })

  it('tells "nothing computed" from "nothing needs attention"', async () => {
    serve([])
    mount()
    await waitFor(() => expect(screen.getByText(/还没有计算结果/)).toBeTruthy())
    cleanup()

    serve([user(1, 'alice', [sig('devices', 'clean')])])
    mount()
    await waitFor(() => expect(screen.getByText('目前没有需要关注的账号。')).toBeTruthy())
    expect(screen.queryByText(/还没有计算结果/)).toBeNull()
  })
})

describe('RiskSignalsTab rows', () => {
  it('lists the accounts that need attention by default; show-all reveals the rest', async () => {
    serve([
      user(1, 'alice', [sig('sub_spread', 'flagged', { code: 'spread' })]),
      user(2, 'bob', [sig('devices', 'clean'), sig('usage_shift', 'idle', { code: 'no_usage' })]),
      // Latched on concurrent locations and idle there: still needs a look.
      user(3, 'carol', [sig('devices', 'unknown', { code: 'no_hwid' })],
        { geo: { state: 'idle', flagged: true, tier: 'region', updated_at_ms: 1 } }),
    ])
    mount()

    await screen.findByText('alice')
    expect(screen.queryByText('carol')).not.toBeNull()
    expect(screen.queryByText('bob')).toBeNull()

    fireEvent.click(screen.getByRole('switch', { name: '显示全部账号' }))
    expect(await screen.findByText('bob')).toBeTruthy()
  })

  it('never colours "cannot tell" or "no data" as clean', async () => {
    // A fleet whose clients send no x-hwid reads devices: unknown on every
    // row. Coloured like clean, a signal that has nothing to judge would look
    // like a fleet with nothing to find.
    serve([user(1, 'alice', [
      sig('sub_spread', 'clean'),
      sig('devices', 'unknown', { code: 'no_hwid' }),
      sig('usage_shift', 'idle', { code: 'no_usage' }),
      sig('login_country', 'flagged', { code: 'new_country' }),
    ])])
    mount()

    await screen.findByText('alice')
    const chip = (label: string) => within(rowOf('alice')).getByText(label).closest('.MuiChip-root') as HTMLElement
    expect(chip('正常').className).toContain('MuiChip-colorSuccess')
    for (const label of ['无法判断', '无数据']) {
      expect(chip(label).className, label).not.toContain('MuiChip-colorSuccess')
    }
    expect(chip('已标记').className).toContain('MuiChip-colorError')
  })

  it('explains each chip with its code and when it was computed', async () => {
    serve([user(1, 'alice', [sig('sub_spread', 'flagged', {
      code: 'spread', updated_at_ms: 1_700_000_000_000,
      evidence: {
        v: 1, window_days: 7, window_start: '2026-09-18', min_days: 3, min_placed_pct: 50, tolerance: 1,
        country: 'CN', groups: 2, groups_all: 2, provinces: [], identities: [], foreign: [],
        excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, coverage: { sources: 3, placed: 3, region_known: 3 },
      },
    })])])
    mount()

    await screen.findByText('alice')
    const tip = within(rowOf('alice')).getByText('已标记').closest('[aria-label]')?.getAttribute('aria-label') ?? ''
    expect(tip).toContain('CN 有 2 组互不相连的常驻省份（容错 1）')
    expect(tip).toContain(new Date(1_700_000_000_000).toLocaleString())
  })

  it('draws one day cell per window day', async () => {
    serve([user(1, 'alice', [
      sig('sub_spread', 'suspect', {
        code: 'spread_building',
        evidence: {
          v: 1, window_days: 5, window_start: '2026-09-18', retention_days: 5, min_days: 3, min_placed_pct: 50,
          tolerance: 1, country: 'CN', groups: 1, groups_all: 2,
          provinces: [
            { cc: 'CN', region: 'Guangdong', days: 0b10101, established: true, group: 1 },
            { cc: 'CN', region: 'Hunan', days: 0b00100, established: false, group: 2 },
          ],
          identities: [{ kind: 'ua', label: 'ClashMeta/1.0', days: 0b11111, provinces: [0] }],
          foreign: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 },
          coverage: { sources: 3, placed: 3, region_known: 3 },
        },
      }),
    ])])
    mount()

    await screen.findByText('alice')
    fireEvent.click(within(rowOf('alice')).getByRole('button', { name: '查看证据' }))

    const strips = await screen.findAllByTestId('day-strip')
    expect(strips.length).toBe(3) // two provinces, one client
    for (const s of strips) expect(s.children.length).toBe(5)
    // Guangdong's mask 0b10101: window days 0, 2 and 4, oldest first.
    const gd = strips[0]
    expect([...gd.children].map(c => c.getAttribute('data-on'))).toEqual(['true', 'false', 'true', 'false', 'true'])
    // Each cell names its panel-local day.
    expect([...gd.children].map(c => c.getAttribute('title'))).toEqual(
      ['2026-09-18', '2026-09-19', '2026-09-20', '2026-09-21', '2026-09-22'])
    expect(screen.queryByText('Guangdong')).not.toBeNull()
    expect(screen.queryByText('第 2 组')).not.toBeNull()
  })

  it('names a province in the admin\'s language where its ISO code is known', async () => {
    // zh-CN from the real bundle: the province line and the client's
    // "visited" line both read 广东, and a province without a code keeps the
    // database's name.
    serve([user(1, 'alice', [
      sig('sub_spread', 'suspect', {
        code: 'spread_building',
        evidence: {
          v: 1, window_days: 3, window_start: '2026-09-18', retention_days: 3, min_days: 3, min_placed_pct: 50,
          tolerance: 1, country: 'CN', groups: 1, groups_all: 2,
          provinces: [
            { cc: 'CN', region: 'Guangdong', rc: 'GD', days: 0b111, established: true, group: 1 },
            { cc: 'CN', region: 'Hunan', days: 0b001, established: false, group: 2 },
          ],
          identities: [{ kind: 'ua', label: 'ClashMeta/1.0', days: 0b111, provinces: [0] }],
          foreign: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 },
          coverage: { sources: 3, placed: 3, region_known: 3 },
        },
      }),
    ])])
    mount()

    await screen.findByText('alice')
    fireEvent.click(within(rowOf('alice')).getByRole('button', { name: '查看证据' }))

    expect(await screen.findByText('广东')).toBeTruthy()
    expect(screen.queryByText('Guangdong')).toBeNull()
    expect(screen.queryByText('Hunan')).not.toBeNull()
    expect(screen.queryByText('去过：广东')).not.toBeNull()
  })

  it('opens the Geo tab from the concurrent-location chip', async () => {
    serve([user(1, 'alice', [sig('devices', 'clean')],
      { geo: { state: 'idle', flagged: true, tier: 'region', updated_at_ms: 1 } })])
    const onOpenGeo = mount()

    await screen.findByText('alice')
    // The latch outlives the state, as on the Geo tab itself.
    expect(within(rowOf('alice')).queryByText('仍在标记中')).not.toBeNull()
    fireEvent.click(within(rowOf('alice')).getByRole('button', { name: '在「异地并发」中查看' }))
    expect(onOpenGeo).toHaveBeenCalledOnce()
  })

  it('ignores a kind this build does not know', async () => {
    // A newer server's kind has no column here; drawing it in some other
    // kind's cell would put a verdict under the wrong heading.
    serve([user(1, 'alice', [sig('travel', 'flagged'), sig('login_country', 'suspect', { code: 'learning' })])])
    mount()

    await screen.findByText('alice')
    const row = within(rowOf('alice'))
    expect(row.queryByText('已标记')).toBeNull()
    expect(row.queryByText('疑似')).not.toBeNull()
    // The three known kinds without a row read "not computed", never blank.
    const missing = row.getAllByText('—').filter(e => e.closest('.MuiChip-root'))
    expect(missing.length).toBe(3)
    expect(missing[0].closest('[aria-label]')?.getAttribute('aria-label')).toBe('尚未计算')
  })

  it('shows the OLDEST signal time as last computed', async () => {
    // A kind that has been skipped for days (infrastructure never loaded,
    // a scan failing) must show its age, not hide behind a fresh sibling.
    serve([user(1, 'alice', [
      sig('sub_spread', 'flagged', { code: 'spread', updated_at_ms: 1_700_000_300_000 }),
      sig('devices', 'clean', { updated_at_ms: 1_700_000_100_000 }),
      sig('usage_shift', 'clean', { updated_at_ms: 1_700_000_200_000 }),
    ])])
    mount()

    await screen.findByText('alice')
    const cells = rowOf('alice').querySelectorAll('td')
    expect(cells[6]?.textContent).toBe(new Date(1_700_000_100_000).toLocaleString())
  })
})
