/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'
import type { GeoAnomaly, GeoEvidence } from '@/api/geoAnomalies'
import type {
  DevicesEvidence, LoginCountryEvidence, RiskSignal, SubSpreadEvidence, UsageShiftEvidence,
} from '@/api/riskSignals'
import { DetectorStateChip } from './DetectorStateChip'
import { GeoDistance, GeoPlaces } from './GeoEvidence'
import {
  DayStrip, DevicesPanel, LoginPanel, RiskKindChip, RiskKindEvidence, SubSpreadPanel, UsagePanel,
} from './RiskEvidence'

vi.mock('@/api/client', () => ({ client: {} }))
const destinationNames = vi.hoisted(() => ({ data: { allow: [] as { id: number; name: string }[], block: [] as { id: number; name: string }[], observe: [] as { id: number; name: string }[] }, isSuccess: true, isError: false }))
vi.mock('@/query/accessControl', () => ({ useDestinationPolicies: () => destinationNames }))
vi.mock('@/query/useQueryScope', () => ({ useQueryScope: () => 'evidence-session' }))
// t over the REAL zh-CN admin bundle, flattened as the SPA registers it, so
// the assertions read the Chinese an admin sees and a key a renderer asks for
// but the bundle lacks shows up as its raw key.
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
// A panel timezone no CI browser runs in, so a time rendered in the
// browser's zone cannot pass for one rendered in the panel's.
const PANEL_TZ = 'Pacific/Chatham'

function mount(node: ReactNode) {
  render(<ThemeProvider theme={theme}>{node}</ThemeProvider>)
}

const chipOf = (label: string) => screen.getByText(label).closest('.MuiChip-root') as HTMLElement

describe('destination blocking lower bounds', () => {
  it('resolves current policy names and explicitly marks deleted sources', () => {
    destinationNames.data.block = [{ id: 12, name: 'Current renamed policy' }]
    mount(<RiskKindEvidence kind="dest_block" evidence={{ v: 1, window_hours: 24, threshold: 20, total: 37, nodes: 1,
      by_source: [{ source: 'p12', count: 30 }, { source: 'p99', count: 7 }], coverage_complete: false,
      losses: { rows: 0, events: 0, unmatched: 0 } }} />)
    expect(screen.getByText('Current renamed policy')).toBeTruthy()
    expect(screen.getByText('已删除策略 p99')).toBeTruthy()
  })

  it('does not label a source deleted when current names cannot be read', () => {
    destinationNames.isSuccess = false
    destinationNames.isError = true
    mount(<RiskKindEvidence kind="dest_block" evidence={{ window_hours: 24, threshold: 20, total: 1, nodes: 1,
      by_source: [{ source: 'p12', count: 1 }] }} />)
    expect(screen.getByText('策略 p12')).toBeTruthy()
    expect(screen.getByText('当前策略名称读取失败')).toBeTruthy()
    expect(document.body.textContent).not.toContain('已删除')
  })
  it('shows incomplete coverage and separate panel losses without destinations', () => {
    mount(<RiskKindEvidence kind="dest_block" userID={7} evidence={{
      v: 1, window_hours: 24, total: 37, threshold: 20, nodes: 2,
      by_source: [{ source: 'p12', count: 37 }], coverage_complete: false,
      losses: { rows: 3, events: 7, unmatched: 2, complete: false, scope: 'panel' },
      target: 'private-host.test', port: 443,
    }} />)
    expect(screen.getByText(/最近 24 小时.*37.*20/)).toBeTruthy()
    expect(screen.getByText(/覆盖不完整/)).toBeTruthy()
    expect(screen.getByText(/节点范围.*3.*7.*2/)).toBeTruthy()
    expect(document.body.textContent).not.toContain('private-host.test')
    expect(document.body.textContent).not.toContain('443')
    expect(screen.getByRole('link', { name: '查看阻断记录' }).getAttribute('href'))
      .toBe('/admin/access-control?tab=records&rec_user=7&rec_since=24h&rec_action=block')
  })

  it('does not paint an incomplete clean signal green', () => {
    mount(<RiskKindChip sig={{ kind: 'dest_block' as RiskSignal['kind'], state: 'clean', code: 'within', updated_at_ms: 1,
      evidence: { total: 1, threshold: 20, window_hours: 24, coverage_complete: false } }} />)
    expect(chipOf('正常').className).not.toContain('MuiChip-colorSuccess')
  })
})

beforeEach(() => {
  useSiteStore.setState({ timezone: PANEL_TZ })
  destinationNames.data = { allow: [], block: [], observe: [] }
  destinationNames.isSuccess = true
  destinationNames.isError = false
})
afterEach(() => {
  cleanup()
  useSiteStore.setState({ timezone: '' })
})

describe('DetectorStateChip', () => {
  // One word per state on every surface: the Geo tab's idle used to read
  // 无连接 where the risk tab said 无数据 for the same "nothing to judge".
  it('says 无数据 for idle', () => {
    mount(<DetectorStateChip state="idle" />)
    expect(screen.getByText('无数据')).toBeTruthy()
    expect(screen.queryByText('无连接')).toBeNull()
  })

  // Trust is an admin's decision about the account; a policy exemption is a
  // setting's. They must never read as the same word.
  it('says 已信任 for an exemption by trust, and 已豁免 for any other', () => {
    mount(<>
      <DetectorStateChip state="exempt" code="trusted" />
      <DetectorStateChip state="exempt" code="allow_anywhere" />
    </>)
    expect(screen.getByText('已信任')).toBeTruthy()
    expect(screen.getByText('已豁免')).toBeTruthy()
  })

  it('says 尚未计算 for a detector with no row', () => {
    mount(<DetectorStateChip state="not_computed" />)
    expect(chipOf('尚未计算').className).not.toContain('MuiChip-colorSuccess')
  })

  // A clean verdict judged while a panel was unreadable stands on a FLOOR of
  // the account's sources — "clean as far as could be seen" — and is never
  // drawn in the colour of a clean bill of health. A flag on a floor is, if
  // anything, an understatement and keeps its colour.
  it('never draws a partial clean verdict as success', () => {
    mount(<>
      <DetectorStateChip state="clean" complete={false} />
      <DetectorStateChip state="flagged" complete={false} />
    </>)
    expect(chipOf('正常').className).not.toContain('MuiChip-colorSuccess')
    expect(chipOf('正常').className).toContain('MuiChip-colorDefault')
    expect(chipOf('已标记').className).toContain('MuiChip-colorError')
    cleanup()

    mount(<DetectorStateChip state="clean" />)
    expect(chipOf('正常').className).toContain('MuiChip-colorSuccess')
  })

  it('carries its tooltip', () => {
    mount(<DetectorStateChip state="suspect" tooltip="why" />)
    expect(chipOf('疑似').closest('[aria-label]')?.getAttribute('aria-label')).toBe('why')
  })
})

describe('DayStrip', () => {
  // One cell per window day, oldest first, each titled with its panel-local
  // date; a filled cell is a day the place, client or device was seen on.
  it('draws one cell per day, in order', () => {
    mount(<DayStrip mask={0b10101} labels={['2026-09-18', '2026-09-19', '2026-09-20', '2026-09-21', '2026-09-22']} />)
    const strip = screen.getByTestId('day-strip')
    expect(strip.children.length).toBe(5)
    expect([...strip.children].map(c => c.getAttribute('data-on'))).toEqual(['true', 'false', 'true', 'false', 'true'])
    expect([...strip.children].map(c => c.getAttribute('title'))).toEqual(
      ['2026-09-18', '2026-09-19', '2026-09-20', '2026-09-21', '2026-09-22'])
  })
})

const NONE = { shared: 0, listed: 0, infra: 0, internal: 0 }

function subSpread(over: Partial<SubSpreadEvidence> = {}): SubSpreadEvidence {
  return {
    v: 1, window_days: 5, window_start: '2026-09-18', retention_days: 5, min_days: 3, min_placed_pct: 50,
    tolerance: 1, country: 'CN', groups: 1, groups_all: 2,
    provinces: [
      { cc: 'CN', region: 'Guangdong', rc: 'GD', days: 0b10101, established: true, group: 1 },
      { cc: 'CN', region: 'Hunan', days: 0b00100, established: false, group: 2 },
    ],
    identities: [{ kind: 'ua', label: 'ClashMeta/1.0', days: 0b11111, provinces: [0] }],
    foreign: [], excluded: NONE, coverage: { sources: 3, placed: 3, region_known: 3 },
    ...over,
  }
}

describe('SubSpreadPanel', () => {
  it('draws a day strip per province and per client, each window day long', () => {
    mount(<SubSpreadPanel ev={subSpread()} />)
    const strips = screen.getAllByTestId('day-strip')
    expect(strips.length).toBe(3) // two provinces, one client
    for (const s of strips) expect(s.children.length).toBe(5)
    expect([...strips[0].children].map(c => c.getAttribute('data-on'))).toEqual(['true', 'false', 'true', 'false', 'true'])
    expect(screen.getByText('第 2 组')).toBeTruthy()
    expect(screen.getByText('常驻')).toBeTruthy()
  })

  // A Chinese UI names a CN province by its ISO code, in the province line
  // and in the client's "visited" line alike; a province without a code keeps
  // the database's name.
  it('names a province in the admin\'s language where its ISO code is known', () => {
    mount(<SubSpreadPanel ev={subSpread()} />)
    expect(screen.getByText('广东')).toBeTruthy()
    expect(screen.queryByText('Guangdong')).toBeNull()
    expect(screen.getByText('Hunan')).toBeTruthy()
    expect(screen.getByText('去过：广东')).toBeTruthy()
  })
})

describe('DevicesPanel', () => {
  const ev: DevicesEvidence = {
    v: 1, window_days: 3, window_start: '2026-09-20', min_days: 3, max_devices: 3, recurrent: 1, distinct: 1,
    devices: [{ label: 'Pixel', hwid4: 'ab12', days: 0b111, last_ms: 1_790_000_000_000, client: 'ClashMeta', recurrent: true }],
    fetches_with_hwid: 10, fetches_without: 2, clients: [{ label: 'v2rayN', days: 0b001 }],
  }

  it('lists each declared device with its prefix, days and last sighting in panel time', () => {
    mount(<DevicesPanel ev={ev} />)
    expect(screen.getByText('Pixel')).toBeTruthy()
    expect(screen.getByText('#ab12')).toBeTruthy()
    expect(screen.getByText('常用')).toBeTruthy()
    expect(screen.getByText(`最后一次 ${formatMsDualTz(1_790_000_000_000, PANEL_TZ)}`)).toBeTruthy()
    expect(screen.getByText('带设备标识的拉取 10 次，不带的 2 次')).toBeTruthy()
    expect(screen.getAllByTestId('day-strip').length).toBe(2) // the device, the client without one
  })
})

describe('UsagePanel', () => {
  // The series is baseline + judged days, both settings: a 14 + 3 series is
  // 17 bars, and the title and caption say 17 and 3 — the days this verdict
  // was judged on, never the shipped 35 and 7.
  it('titles the series by its length and captions the judged days', () => {
    const ev: UsageShiftEvidence = {
      v: 1, end_date: '2026-09-24', series: new Array(17).fill(2 ** 30), history_days: 14,
      median: 2 ** 30, ratio: 3, floor: 3 * 2 ** 30, thresholds: [1, 1, 1], over: [true, true, false],
      over_days: 2, fleet_factors: [1, 1, 1],
      baseline_days: 14, recent_days: 3, warmup_days: 7, flag_days: 2, suspect_days: 2,
    }
    mount(<UsagePanel ev={ev} />)
    expect(screen.getByText('最近 17 天每日用量（截至 2026-09-24）')).toBeTruthy()
    expect(screen.getByText(/；最近 3 天超标 2 天$/)).toBeTruthy()
  })

  // A warm-up carries the series alone; a caption of zeros would read as a
  // judgement.
  it('draws no caption before judging', () => {
    const ev: UsageShiftEvidence = {
      v: 1, end_date: '2026-09-24', series: [1, 2, 3], history_days: 3, median: 0, ratio: 0, floor: 0,
      thresholds: [], over: [], over_days: 0, fleet_factors: [],
    }
    mount(<UsagePanel ev={ev} />)
    expect(screen.getByText('最近 3 天每日用量（截至 2026-09-24）')).toBeTruthy()
    expect(screen.queryByText(/基线中位数/)).toBeNull()
  })
})

describe('LoginPanel', () => {
  it('counts logins over the hold the verdict was judged with, and times events in panel time', () => {
    const ev: LoginCountryEvidence = {
      v: 1, lookback_days: 30, hold_days: 3, warmup: 2, logins: 6, recent: 2, judged: 2,
      skipped: { infra: 0, internal: 0, listed: 0, node_country: 0, unplaced: 0 }, known: ['CN'],
      events: [{ cc: 'JP', at_ms: 1_790_000_000_000, method: 'password' }],
    }
    mount(<LoginPanel ev={ev} />)
    expect(screen.getByText(/^30 天内登录 6 次；最近 3 天 2 次，其中已判断 2 次；/)).toBeTruthy()
    expect(screen.getByText('新国家登录')).toBeTruthy()
    expect(screen.getByText(formatMsDualTz(1_790_000_000_000, PANEL_TZ))).toBeTruthy()
  })
})

describe('RiskKindEvidence', () => {
  // One entry point for a kind's evidence, so the table, the drawer and a
  // flag record's params render one kind the same way.
  it('renders the panel of its kind, and nothing for an unknown kind or no evidence', () => {
    mount(<RiskKindEvidence kind="login_country" evidence={{
      v: 1, lookback_days: 30, hold_days: 7, warmup: 2, logins: 1, recent: 1, judged: 1,
      skipped: { infra: 0, internal: 0, listed: 0, node_country: 0, unplaced: 0 }, known: [], events: [],
    } satisfies LoginCountryEvidence} />)
    expect(screen.getByText('已知国家')).toBeTruthy()
    cleanup()

    const { container } = render(<ThemeProvider theme={theme}>
      <RiskKindEvidence kind="travel" evidence={{ v: 1 }} />
      <RiskKindEvidence kind="devices" evidence={null} />
    </ThemeProvider>)
    expect(container.textContent).toBe('')
  })
})

describe('RiskKindChip', () => {
  const sig = (over: Partial<RiskSignal>): RiskSignal => ({
    kind: 'sub_spread', state: 'clean', code: 'within', evidence: null, updated_at_ms: 1_790_000_000_000, ...over,
  })

  // A kind with no row is "not computed", never blank and never clean.
  it('shows a kind with no row as not computed', () => {
    mount(<RiskKindChip sig={undefined} />)
    expect(chipOf('—').closest('[aria-label]')?.getAttribute('aria-label')).toBe('尚未计算')
  })

  it('labels by the one vocabulary and explains itself with its code and panel time', () => {
    mount(<RiskKindChip sig={sig({ state: 'exempt', code: 'trusted' })} />)
    const tip = chipOf('已信任').closest('[aria-label]')?.getAttribute('aria-label') ?? ''
    expect(tip).toContain('管理员已信任此账号，不判断订阅地点')
    expect(tip).toContain(formatMsDualTz(1_790_000_000_000, PANEL_TZ))
  })
})

describe('GeoPlaces and GeoDistance', () => {
  const evidence = (over: Partial<GeoEvidence> = {}): GeoEvidence => ({
    v: 3, spots: [{ cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Shenzhen', n: 2 }], excluded: NONE, stale: 0,
    coverage: { placed: 2, unplaced: 0, region_known: 2, city_known: 2 }, networks: 2,
    spread: { countries: 1, regions: 1, region_country: 'CN', cities: 1, city_country: 'CN', max_km: 1070 },
    ...over,
  })
  const row = (ev: GeoEvidence, places: string[] = ['CN']): Pick<GeoAnomaly, 'places' | 'evidence'> => ({ places, evidence: ev })

  it('lists the places by country, region and city, in the admin\'s language', () => {
    mount(<GeoPlaces row={row(evidence())} />)
    expect(screen.getByText(/广东 2 \(Shenzhen 2\)/)).toBeTruthy()
  })

  it('falls back to the recorded countries for a row an older build wrote', () => {
    mount(<GeoPlaces row={row(evidence({ v: 0, spots: [] }), ['DE', 'JP'])} />)
    expect(screen.getByText('DE · JP')).toBeTruthy()
  })

  // The distance is never judged, and says so; where none was measured (or
  // the row predates the field) nothing is drawn.
  it('shows the distance, marked as not judged, only where it was measured', () => {
    mount(<GeoDistance evidence={evidence()} />)
    const hint = screen.getByText('相距约 1070 公里').closest('[aria-label]')
    expect(hint?.getAttribute('aria-label') ?? '').toContain('不参与判定')
    cleanup()

    const base = evidence()
    const { container } = render(<ThemeProvider theme={theme}>
      <GeoDistance evidence={evidence({ spread: { ...base.spread, max_km: undefined } })} />
      <GeoDistance evidence={evidence({ v: 2 })} />
    </ThemeProvider>)
    expect(container.textContent).toBe('')
  })
})
