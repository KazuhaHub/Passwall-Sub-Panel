/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import type { GeoAnomaly } from '@/api/geoAnomalies'
import GeoAnomaliesTab, { stateColor } from './GeoAnomaliesTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
// The strings the localized tests read, as zh-CN ships them; every other key
// falls through to its defaultValue exactly as before, so the rest of this
// file still reads the component's own Chinese defaults.
const zh = vi.hoisted((): Record<string, string> => ({
  'admin:geo_anomalies.reason_over_region': '同时在 {{country}} 的 {{spread}} 个省 / 州（容错 {{tolerance}}）',
  'admin:geo_anomalies.reason_flagged_suffix': '，已持续 {{over}} / {{need}} 次',
  'admin:geo_anomalies.max_km': '相距约 {{km}} 公里',
  'admin:geo_regions.cn.GD': '广东',
}))
// The UI language, read on every render so a test can switch it; afterEach
// puts it back.
const lang = vi.hoisted(() => ({ current: 'zh-CN' }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const raw = zh[k] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: lang.current },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <ThemeProvider theme={theme}>
      <GeoAnomaliesTab />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(() => {
  cleanup()
  lang.current = 'zh-CN'
})

// Colour is the first thing an operator reads on this table, so it has to
// track what they should DO rather than how alarming the word sounds.
describe('stateColor', () => {
  // Only flagged is actionable, and it must be the only one that looks like an
  // emergency — otherwise the ramp and the verdict are indistinguishable.
  it('reserves the alarm colour for the one actionable state', () => {
    expect(stateColor('flagged')).toBe('error')
    for (const s of ['suspect', 'clean', 'unknown', 'idle', 'exempt', 'disabled'] as const) {
      expect(stateColor(s)).not.toBe('error')
    }
  })

  // The single most important rule here. "Cannot tell" on a fleet whose geo
  // database has quietly stopped working looks EXACTLY like a clean fleet if
  // it is coloured like one — a detector that has stopped detecting would read
  // as a fleet with nobody sharing.
  it('never colours a non-verdict as clean', () => {
    for (const s of ['unknown', 'exempt', 'disabled', 'idle'] as const) {
      expect(stateColor(s)).not.toBe('success')
    }
    expect(stateColor('clean')).toBe('success')
  })

  // Suspect is the visible ramp: distinct from both a flag and a clean row, so
  // the eventual flag does not appear out of nowhere.
  it('gives the ramp its own colour', () => {
    const suspect = stateColor('suspect')
    expect(suspect).not.toBe(stateColor('flagged'))
    expect(suspect).not.toBe(stateColor('clean'))
  })
})

describe('GeoAnomaliesTab read states', () => {
  it('reports an unwired detector as its own message, not as an empty table', async () => {
    // 503 means the detector is not running in this build. Rendering that as
    // "no anomalies yet" would describe a monitored fleet when nothing is
    // being monitored at all.
    api.get.mockRejectedValue({ isAxiosError: true, response: { status: 503 } })
    mount()

    await waitFor(() => expect(screen.getByText('本部署未启用异地并发检测。')).toBeTruthy())
    expect(screen.queryByText(/还没有判定记录/)).toBeNull()
  })

  it('shows the empty state only for a successful empty read', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText(/还没有判定记录/)).toBeTruthy())
    expect(screen.queryByText('本部署未启用异地并发检测。')).toBeNull()
  })
})

function row(over: Partial<GeoAnomaly>): GeoAnomaly {
  return {
    user_id: 1,
    upn: 'alice',
    state: 'clean',
    reason: 'within tolerance',
    tier: '',
    flagged: false,
    places: [],
    live_ips: 0,
    concurrent_ips: 0,
    excluded_ips: 0,
    complete: true,
    over_streak: 0,
    under_streak: 0,
    ban_streak: 0,
    evidence: {
      v: 1, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
      coverage: { placed: 0, unplaced: 0, region_known: 0, city_known: 0 }, networks: 0,
      spread: { countries: 0, regions: 0, region_country: '', cities: 0, city_country: '' },
    },
    updated_at_ms: 1,
    ...over,
  }
}

// The list and the location-database status are two reads; a status of
// undefined makes that read fail, which must cost nothing but the banner.
function serve(items: GeoAnomaly[], status?: object) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/geo-anomalies') return { data: { items } }
    if (url === '/admin/settings/geoip/status' && status) return { data: status }
    throw new Error(`unexpected GET ${url}`)
  })
}

function rowOf(name: string): HTMLElement {
  return screen.getByText(name).closest('tr') as HTMLElement
}

describe('GeoAnomaliesTab rows', () => {
  it('shows concurrent / window', async () => {
    // The window (what the upstream remembers for 30 minutes) is not what was
    // judged. Showing it alone made a commuter's day of addresses look like
    // seven places at once.
    serve([row({ upn: 'alice', concurrent_ips: 2, live_ips: 7 })])
    mount()

    await screen.findByText('alice')
    expect(within(rowOf('alice')).queryByText('2 / 7')).not.toBeNull()
  })

  it('names what was set aside in the IP count\'s hint', async () => {
    serve([row({
      upn: 'alice', concurrent_ips: 2, live_ips: 9, excluded_ips: 3,
      evidence: { ...row({}).evidence, excluded: { shared: 1, listed: 0, infra: 2, internal: 0 } },
    })])
    mount()

    await screen.findByText('alice')
    const hint = within(rowOf('alice')).queryByText('2 / 9')?.closest('[aria-label]')
    expect(hint?.getAttribute('aria-label') ?? '').toContain('另有 3 个已排除：共享出口 1、忽略名单 0、本机节点/中转 2、内网 0')
  })

  it('marks an auto-suspended row', async () => {
    serve([
      row({ user_id: 1, upn: 'alice', state: 'flagged', flagged: true, tier: 'region',
        service_disabled_reason: 'geo_auto', service_disabled_at_ms: 1_700_000_000_000 }),
      // Somebody else's hold is not the detector's doing and must not read as it.
      row({ user_id: 2, upn: 'bob', state: 'flagged', flagged: true, tier: 'country',
        service_disabled_reason: 'service_manual' }),
    ])
    mount()

    await screen.findByText('alice')
    expect(within(rowOf('alice')).queryByText('自动暂停中')).not.toBeNull()
    expect(within(rowOf('bob')).queryByText('自动暂停中')).toBeNull()
  })

  it('keeps a flagged account that went idle visibly flagged, with its tier', async () => {
    // The streak freezes while idle, so the latch is still on. Reading the
    // state alone ("idle") would make the row look cleared.
    serve([row({ upn: 'alice', state: 'idle', flagged: true, tier: 'city' })])
    mount()

    await screen.findByText('alice')
    const r = within(rowOf('alice'))
    expect(r.queryByText('仍在标记中')).not.toBeNull()
    expect(r.queryByText('跨城')).not.toBeNull()
  })

  it('renders the most severe rows first', async () => {
    // The server answers newest first; the admin needs what to act on first.
    serve([
      row({ user_id: 1, upn: 'clean-user', state: 'clean', updated_at_ms: 9 }),
      row({ user_id: 2, upn: 'flagged-user', state: 'flagged', flagged: true, tier: 'country', updated_at_ms: 1 }),
    ])
    mount()

    await screen.findByText('flagged-user')
    const users = screen.getAllByRole('row').slice(1).map(tr => tr.querySelector('td')?.textContent)
    expect(users).toEqual(['flagged-user', 'clean-user'])
  })

  it('lists where the account was, by country, region and city', async () => {
    serve([row({
      upn: 'alice', state: 'suspect', tier: 'region', places: ['CN'],
      evidence: {
        ...row({}).evidence,
        spots: [
          { cc: 'CN', region: 'Guangdong', city: 'Shenzhen', n: 2 },
          { cc: 'CN', region: 'Hunan', city: 'Changsha', n: 1 },
        ],
      },
    })])
    mount()

    await screen.findByText('alice')
    const text = rowOf('alice').textContent ?? ''
    expect(text).toContain('Guangdong')
    expect(text).toContain('Shenzhen')
    expect(text).toContain('Hunan')
    expect(text).toContain('Changsha')
  })

  // The Places cell alone, so an assertion cannot be met by the same word in
  // the reason beside it.
  const placesOf = (name: string) => rowOf(name).querySelectorAll('td')[2]?.textContent ?? ''

  it('lists the cities of a country whose database named no region', async () => {
    // A location record can carry a city and no subdivision, and the city
    // tier counts those cities on their own. A row it flags must name them:
    // the missing region prints as "?" like any other unresolved level.
    serve([row({
      upn: 'alice', state: 'flagged', flagged: true, tier: 'city', places: ['SG'],
      evidence: {
        ...row({}).evidence,
        spots: [
          { cc: 'SG', region: '', city: 'Alpha', n: 2 },
          { cc: 'SG', region: '', city: 'Beta', n: 1 },
        ],
      },
    })])
    mount()

    await screen.findByText('alice')
    expect(placesOf('alice')).toContain('SG 3: ? 3 (Alpha 2, Beta 1)')
  })

  it('prints country-only evidence as the country alone', async () => {
    // Nothing below the country was resolved, so there is nothing to list,
    // and a "?" would read as a region the database half-knew.
    serve([row({
      upn: 'alice', state: 'flagged', flagged: true, tier: 'country', places: ['DE', 'JP'],
      evidence: {
        ...row({}).evidence,
        spots: [
          { cc: 'DE', region: '', city: '', n: 2 },
          { cc: 'JP', region: '', city: '', n: 1 },
        ],
      },
    })])
    mount()

    await screen.findByText('alice')
    const places = placesOf('alice')
    expect(places).toContain('DE 2')
    expect(places).toContain('JP 1')
    expect(places).not.toContain('?')
  })

  it('renders the localized reason', async () => {
    // The Basis cell used to print the server's English sentence verbatim on
    // every page. A v2 row carries the branch and the policy it was judged
    // with; a row written before that keeps its stored English.
    serve([
      row({
        user_id: 1, upn: 'alice', state: 'flagged', flagged: true, tier: 'region', over_streak: 3,
        reason: 'in 2 regions of CN at once ([CN/Guangdong CN/Hunan]); tolerance is 1, sustained for 3 of 3 checks',
        evidence: {
          ...row({}).evidence,
          v: 2,
          spread: { countries: 1, regions: 2, region_country: 'CN', cities: 2, city_country: 'CN' },
          why: {
            code: 'flagged_sustained', tier: 'region', scope: 'city',
            tol: { countries: 1, regions: 1, cities: 2 }, flag_after: 3, clear_after: 6, min_placed_ratio: 0.5,
          },
        },
      }),
      row({ user_id: 2, upn: 'bob', reason: 'within tolerance' }),
    ])
    mount()

    await screen.findByText('alice')
    const reasonOf = (name: string) => rowOf(name).querySelectorAll('td')[4]?.textContent
    expect(reasonOf('alice')).toBe('同时在 CN 的 2 个省 / 州（容错 1），已持续 3 / 3 次')
    expect(reasonOf('bob')).toBe('within tolerance')
  })

  // Guangdong carries its ISO code; the city is the database's own spelling
  // in every language.
  const coded = (over: Partial<GeoAnomaly['evidence']> = {}) => row({
    upn: 'alice', state: 'suspect', tier: 'region', places: ['CN'],
    evidence: {
      ...row({}).evidence,
      spots: [{ cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Shenzhen', n: 2 }],
      ...over,
    },
  })

  it('names a Chinese province in Chinese for a Chinese UI', async () => {
    serve([coded()])
    mount()

    await screen.findByText('alice')
    expect(placesOf('alice')).toContain('广东 2 (Shenzhen 2)')
  })

  it('keeps the database\'s province name for any other UI', async () => {
    // The sub-log and auth-event region columns show the database's English;
    // an English Geo tab reads the same.
    lang.current = 'en-US'
    serve([coded()])
    mount()

    await screen.findByText('alice')
    expect(placesOf('alice')).toContain('Guangdong 2 (Shenzhen 2)')
    expect(placesOf('alice')).not.toContain('广东')
  })

  it('shows how far apart the concurrent sources are, marked as not judged', async () => {
    const base = row({}).evidence
    serve([coded({ v: 3, spread: { ...base.spread, max_km: 1070 } })])
    mount()

    await screen.findByText('alice')
    expect(placesOf('alice')).toContain('相距约 1070 公里')
    const hint = within(rowOf('alice')).getByText('相距约 1070 公里').closest('[aria-label]')
    expect(hint?.getAttribute('aria-label') ?? '').toContain('不参与判定')
  })

  it('shows no distance where none was measured or recorded', async () => {
    // v3 without max_km: nothing measured. v2: the field did not exist, so a
    // number there is not one this build vouches for.
    const base = row({}).evidence
    serve([
      coded({ v: 3 }),
      { ...coded({ v: 2, spread: { ...base.spread, max_km: 1070 } }), user_id: 2, upn: 'bob' },
    ])
    mount()

    await screen.findByText('alice')
    expect(placesOf('alice')).not.toContain('相距')
    expect(placesOf('bob')).not.toContain('相距')
  })

  it('falls back to the recorded places for a row an older build wrote', async () => {
    serve([row({ upn: 'alice', places: ['DE', 'JP'], evidence: { ...row({}).evidence, v: 0 } })])
    mount()

    await screen.findByText('alice')
    expect(within(rowOf('alice')).queryByText('DE · JP')).not.toBeNull()
  })
})

describe('GeoAnomaliesTab coarse-database banner', () => {
  const status = (granularity: string) => ({
    enabled: true, dir: '', active: 'x.mmdb', update: { updating: false },
    available: [{ file: 'x.mmdb', type: '', granularity, build_epoch: 0, active: true }],
  })

  it('shows the coarse-database banner when the active database is country-only', async () => {
    serve([row({ upn: 'alice' })], status('country'))
    mount()

    await waitFor(() => expect(screen.queryByText(/只能定位到国家/)).not.toBeNull())
  })

  it('shows no banner for a city database or a failed status read', async () => {
    serve([row({ upn: 'alice' })], status('city'))
    mount()
    await screen.findByText('alice')
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/settings/geoip/status', expect.anything()))
    expect(screen.queryByText(/只能定位到国家/)).toBeNull()
    cleanup()

    serve([row({ upn: 'bob' })])
    mount()
    await screen.findByText('bob')
    expect(screen.queryByText(/只能定位到国家/)).toBeNull()
  })
})
