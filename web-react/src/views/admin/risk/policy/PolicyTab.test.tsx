/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { createMemoryRouter, useLocation, useSearchParams } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import AppRouter from '@/router/AppRouter'
import type { RiskPolicySettings, RiskPolicyView } from '@/api/riskCenter'
import type { Group } from '@/api/types'

// A page of forty-eight fields and a group editor, mounted per test: heavier
// than the default budget on a loaded runner.
vi.setConfig({ testTimeout: 30_000 })

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack, default: () => null }))
const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock, default: () => null }))
// t over the REAL zh-CN admin bundle, so a key the page asks for but the
// bundle lacks shows up as its raw key instead of passing on a defaultValue.
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
import { allGroupsQuery } from '@/query/groups'
import { settingsKeys } from '@/query/keys'
import { sessionScope } from '@/query/session'
import { POLICY_KEYS } from './policyKeys'
import PolicyTab from './PolicyTab'
dict.current = flatten(zh as Nested)

/** The zh-CN text of an admin key. */
const L = (key: string) => {
  const v = dict.current[key]
  if (v === undefined) throw new Error(`no zh-CN text for ${key}`)
  return v
}

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })

// The 23 runtime knobs, each with the settings key its label lives under.
const KNOBS: [string, string][] = [
  ['risk_usage_warmup_days', 'settings.risk.usage_warmup_days'],
  ['risk_usage_flag_days', 'settings.risk.usage_flag_days'],
  ['risk_usage_suspect_days', 'settings.risk.usage_suspect_days'],
  ['risk_login_warmup_logins', 'settings.risk.login_warmup_logins'],
  ['risk_login_hold_days', 'settings.risk.login_hold_days'],
  ['risk_connection_retention_days', 'settings.risk_center.connection_retention_days'],
  ['risk_flag_record_retention_days', 'settings.risk_center.flag_record_retention_days'],
  ['risk_live_snapshot_stale_minutes', 'settings.risk_center.live_snapshot_stale_minutes'],
  ['risk_live_refresh_cooldown_seconds', 'settings.risk_center.live_refresh_cooldown_seconds'],
  ['risk_device_infer_hours', 'settings.risk_center.device_infer_hours'],
  ['geo_anomaly_fresh_window_seconds', 'settings.geo_anomaly.fresh_window_seconds'],
  ['geo_anomaly_shared_exit_min_users', 'settings.geo_anomaly.shared_exit_min_users'],
  ['geo_anomaly_ban_max_per_poll', 'settings.geo_anomaly.ban_max_per_poll'],
  ['geo_anomaly_lift_max_per_poll', 'settings.geo_anomaly.lift_max_per_poll'],
  ['geo_anomaly_infra_refresh_minutes', 'settings.geo_anomaly.infra_refresh_minutes'],
  ['geo_anomaly_infra_host_ttl_minutes', 'settings.geo_anomaly.infra_host_ttl_minutes'],
  ['risk_refresh_interval_minutes', 'settings.risk.refresh_interval_minutes'],
  ['risk_first_delay_minutes', 'settings.risk.first_delay_minutes'],
  ['risk_alert_freshness_hours', 'settings.risk.alert_freshness_hours'],
  ['risk_window_days', 'settings.risk.window_days'],
  ['risk_usage_baseline_days', 'settings.risk.usage_baseline_days'],
  ['risk_usage_recent_days', 'settings.risk.usage_recent_days'],
  ['risk_login_lookback_days', 'settings.risk.login_lookback_days'],
]

// The served defaults: the shipped geo and risk thresholds, and for the
// runtime knobs distinct numbers, so a placeholder read off the wrong key
// shows the wrong one.
const DEFAULTS: Record<string, number> = {
  geo_anomaly_max_places: 1, geo_anomaly_max_regions: 1, geo_anomaly_max_cities: 2,
  geo_anomaly_flag_after_polls: 3, geo_anomaly_clear_after_polls: 6, geo_anomaly_min_placed_ratio: 0.5,
  geo_anomaly_ban_max_countries: 1, geo_anomaly_ban_max_regions: 2, geo_anomaly_ban_max_cities: 3,
  geo_anomaly_ban_after_polls: 6, geo_anomaly_ban_duration_minutes: 60,
  risk_min_days: 3, risk_max_devices: 3, risk_usage_ratio: 3, risk_usage_floor_gb: 3,
  ...Object.fromEntries(KNOBS.map(([k], i) => [k, 500 + i])),
}
const EFFECTIVE: Record<string, number> = Object.fromEntries(KNOBS.map(([k], i) => [k, 100 + i]))

function blank(): RiskPolicySettings {
  return {
    ...Object.fromEntries(POLICY_KEYS.map(k => [k, 0])),
    geo_anomaly_scope: '', geo_anomaly_co_travel: '', geo_anomaly_ignore_addresses: '',
    geo_anomaly_allow_anywhere: false, geo_anomaly_ban_enabled: false,
    risk_hwid_capture_off: false, risk_sub_spread_off: false, risk_devices_off: false,
    risk_usage_shift_off: false, risk_login_country_off: false,
  } as RiskPolicySettings
}

// The server: one stored policy the GET reads and the PUT merges into, and
// the global settings the group editor's baseline reads.
const server = vi.hoisted(() => ({
  policy: {} as Record<string, unknown>,
  withMaps: true,
  putError: null as unknown,
  overrides: {} as Record<string, string>,
}))

function view(): RiskPolicyView {
  return {
    settings: { ...server.policy } as unknown as RiskPolicySettings,
    ...(server.withMaps ? { defaults: DEFAULTS, effective: EFFECTIVE } : {}),
  } as RiskPolicyView
}

const GEO_ROWS = [
  'geo_anomaly.scope', 'geo_anomaly.max_places', 'geo_anomaly.max_regions', 'geo_anomaly.max_cities',
  'geo_anomaly.flag_after_polls', 'geo_anomaly.clear_after_polls', 'geo_anomaly.co_travel', 'geo_anomaly.allow_anywhere',
  'geo_anomaly.ban_enabled', 'geo_anomaly.ban_max_countries', 'geo_anomaly.ban_max_regions', 'geo_anomaly.ban_max_cities',
  'geo_anomaly.ban_after_polls', 'geo_anomaly.ban_duration_minutes', 'risk.sub_spread_off', 'risk.min_days',
]

function serve(over: Partial<RiskPolicySettings> = {}) {
  server.policy = { ...blank(), ...over }
  server.withMaps = true
  server.putError = null
  server.overrides = { 'geo_anomaly.max_regions': '2' }
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/risk-center/policy') return { data: view() }
    if (url === '/admin/settings/geoip/status') {
      return {
        data: {
          enabled: true, dir: '', active: 'city.mmdb', update: { updating: false },
          available: [{ name: 'city.mmdb', active: true, granularity: 'city' }],
        },
      }
    }
    if (url === '/admin/groups') {
      return {
        data: {
          items: [{ id: 2, slug: 'team-a', name: 'Team A' }, { id: 3, slug: 'team-b', name: 'Team B' }],
          total: 2, page: 1, page_size: 200,
        },
      }
    }
    const scope = /^\/admin\/groups\/(\d+)\/scope-settings$/.exec(url)
    if (scope) {
      return {
        data: {
          scope_type: 'group', scope_id: Number(scope[1]), overridable: GEO_ROWS,
          overrides: Number(scope[1]) === 2 ? { ...server.overrides } : {},
        },
      }
    }
    if (url === '/admin/settings/ui') return { data: { ...server.policy } }
    throw new Error(`unexpected GET ${url}`)
  })
  api.put.mockImplementation(async (url: string, body: { settings?: Record<string, unknown> }) => {
    if (url === '/admin/risk-center/policy') {
      if (server.putError) throw server.putError
      server.policy = { ...server.policy, ...body.settings }
      return { data: view() }
    }
    if (/^\/admin\/groups\/\d+\/scope-settings$/.test(url)) return { data: {} }
    throw new Error(`unexpected PUT ${url}`)
  })
  api.delete.mockResolvedValue({ data: {} })
}

let lastLocation = ''
function Where() {
  const loc = useLocation()
  lastLocation = loc.pathname + loc.search
  return <p data-testid="location">{lastLocation}</p>
}

// A stand-in for the page's tab bar: a switch away from the policy is what
// the leave guard stands in front of.
function TabSwitch() {
  const [, setParams] = useSearchParams()
  return (
    <button type="button" onClick={() => setParams(prev => {
      const out = new URLSearchParams(prev)
      out.set('tab', 'queue')
      return out
    }, { replace: true })}>go queue</button>
  )
}

function mount(url = '/admin/risk?tab=policy', client: QueryClient = makeTestQueryClient()): { client: QueryClient } {
  const router = createMemoryRouter([
    { path: '/admin/risk', element: <><PolicyTab /><TabSwitch /><Where /></> },
  ], { initialEntries: [url] })
  render(
    <ThemeProvider theme={theme}>
      <QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider>
    </ThemeProvider>,
  )
  return { client }
}

function card(titleKey: string): HTMLElement {
  return screen.getByRole('region', { name: L(titleKey) })
}

async function loaded() {
  await screen.findByRole('region', { name: L('risk_center.policy.card.geo') })
}

function field(labelKey: string, within_: HTMLElement | Document = document): HTMLInputElement {
  const root = within_ instanceof HTMLElement ? within(within_) : screen
  return root.getByRole('textbox', { name: L(labelKey) }) as HTMLInputElement
}

function description(input: HTMLElement): string {
  const id = input.getAttribute('aria-describedby') ?? ''
  return document.getElementById(id)?.textContent ?? ''
}

function type(input: HTMLInputElement, value: string) {
  fireEvent.focus(input)
  fireEvent.change(input, { target: { value } })
  fireEvent.blur(input)
}

function expandAll() {
  for (const b of screen.getAllByRole('button', { name: L('risk_center.policy.advanced') })) {
    if (b.getAttribute('aria-expanded') !== 'true') fireEvent.click(b)
  }
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: L('risk_center.policy.save') }) as HTMLButtonElement
}

function policyPuts(): Record<string, unknown>[] {
  return api.put.mock.calls
    .filter(([u]) => u === '/admin/risk-center/policy')
    .map(([, body]) => (body as { settings: Record<string, unknown> }).settings)
}

beforeEach(() => {
  vi.clearAllMocks()
  lastLocation = ''
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('PolicyTab, the page', () => {
  it('lays out the six detector cards and the group exceptions', async () => {
    serve()
    mount()
    await loaded()
    for (const k of ['geo', 'sub_spread', 'devices', 'usage_shift', 'login_country', 'data', 'groups']) {
      expect(card(`risk_center.policy.card.${k}`), k).toBeTruthy()
    }
    // Nothing is unsaved on arrival.
    expect(saveButton().disabled).toBe(true)
  })

  it('says the location database is at city level', async () => {
    serve()
    mount()
    await loaded()
    expect(await within(card('risk_center.policy.card.geo')).findByText(`地区库：${L('risk_center.policy.geoip_city')}`))
      .toBeTruthy()
  })

  it('shows a load failure with a retry, and the unwired answer on its own', async () => {
    serve()
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/risk-center/policy') throw Object.assign(new Error('boom'), { isAxiosError: true, response: { status: 500, data: { error: 'boom' } } })
      return { data: {} }
    })
    mount()
    expect(await screen.findByText('读取失败：boom')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy()
    cleanup()
    api.get.mockImplementation(async () => {
      throw Object.assign(new Error('503'), { isAxiosError: true, response: { status: 503, data: {} } })
    })
    mount()
    expect(await screen.findByText(L('risk_center.unwired'))).toBeTruthy()
  })
})

describe('PolicyTab, fields', () => {
  // Migrated from the settings page: every runtime knob shows the default
  // as its empty placeholder and, under the hint, the number the server
  // runs with — the server's, never a copy.
  it('shows every runtime knob with its default and its value in effect', async () => {
    serve()
    mount()
    await loaded()
    expandAll()
    for (const [knob, labelKey] of KNOBS) {
      const input = field(labelKey)
      expect(input.value, knob).toBe('')
      expect(input.placeholder, knob).toBe(String(DEFAULTS[knob]))
      expect(description(input), knob).toContain(`当前生效：${EFFECTIVE[knob]}`)
    }
  })

  it('shows a stored value as typed, beside what it became', async () => {
    serve({ risk_usage_flag_days: 10 })
    server.withMaps = true
    const effective = { ...EFFECTIVE, risk_usage_flag_days: 7 }
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/risk-center/policy') return { data: { ...view(), effective } }
      if (url === '/admin/settings/geoip/status') return { data: { enabled: true, dir: '', active: '', available: [], update: { updating: false } } }
      if (url === '/admin/groups') return { data: { items: [], total: 0, page: 1, page_size: 200 } }
      throw new Error(`unexpected GET ${url}`)
    })
    mount()
    await loaded()
    const input = field('settings.risk.usage_flag_days')
    expect(input.value).toBe('10')
    expect(description(input)).toContain('当前生效：7')
  })

  it('renders without the maps a server that sends none', async () => {
    serve()
    server.withMaps = false
    mount()
    await loaded()
    const input = field('settings.risk_center.connection_retention_days')
    expect(input.placeholder).toBe('')
    expect(description(input)).not.toContain('当前生效')
  })

  // A changed advanced value must not hide behind a closed panel.
  it('opens a card\'s advanced panel when one of its values is configured', async () => {
    serve({ geo_anomaly_fresh_window_seconds: 60 })
    mount()
    await loaded()
    const geoAdvanced = within(card('risk_center.policy.card.geo')).getByRole('button', { name: L('risk_center.policy.advanced') })
    expect(geoAdvanced.getAttribute('aria-expanded')).toBe('true')
    const usageAdvanced = within(card('risk_center.policy.card.usage_shift')).getByRole('button', { name: L('risk_center.policy.advanced') })
    expect(usageAdvanced.getAttribute('aria-expanded')).toBe('false')
  })

  it('refuses to save while a field is out of range', async () => {
    serve()
    mount()
    await loaded()
    type(field('settings.risk.usage_ratio'), '1.2')
    expect(screen.getByText('有 1 项超出范围，无法保存')).toBeTruthy()
    expect(saveButton().disabled).toBe(true)
    type(field('settings.risk.usage_ratio'), '2')
    expect(saveButton().disabled).toBe(false)
  })
})

describe('PolicyTab, switches and presets', () => {
  // The geo card's switch is the scope: off stores 'off', on puts back what
  // it was, so switching off and on again changes nothing.
  it('switches geo off as the scope and back', async () => {
    serve({ geo_anomaly_scope: 'region' })
    mount()
    await loaded()
    const toggle = within(card('risk_center.policy.card.geo')).getByRole('switch', { name: L('risk_center.policy.enabled') })
    fireEvent.click(toggle)
    expect(screen.getByText(`未保存：${L('risk_center.policy.part_policy')}`)).toBeTruthy()
    fireEvent.click(toggle)
    expect(screen.queryByText(`未保存：${L('risk_center.policy.part_policy')}`)).toBeNull()
    fireEvent.click(toggle)
    fireEvent.click(saveButton())
    await waitFor(() => expect(policyPuts()).toEqual([{ geo_anomaly_scope: 'off' }]))
  })

  it('switches a risk signal off through its negative key', async () => {
    serve()
    mount()
    await loaded()
    const devices = within(card('risk_center.policy.card.devices'))
    fireEvent.click(devices.getByRole('switch', { name: L('risk_center.policy.enabled') }))
    expect(devices.getByRole('switch', { name: L('risk_center.policy.disabled') })).toBeTruthy()
    fireEvent.click(saveButton())
    await waitFor(() => expect(policyPuts()).toEqual([{ risk_devices_off: true }]))
  })

  it('fills the key thresholds from a preset, and a hand edit reads as custom', async () => {
    serve()
    mount()
    await loaded()
    const geo = card('risk_center.policy.card.geo')
    const presetButton = (key: string) => within(geo).getByRole('button', { name: L(`risk_center.policy.preset_${key}`) })
    expect(presetButton('standard').getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(presetButton('strict'))
    expect(presetButton('strict').getAttribute('aria-pressed')).toBe('true')
    expect(field('settings.geo_anomaly.max_cities', geo).value).toBe('1')
    expect(field('settings.geo_anomaly.flag_after', geo).value).toBe('2')
    expect(field('settings.geo_anomaly.clear_after', geo).value).toBe('8')
    // A strict value that IS the default is stored unset.
    expect(field('settings.geo_anomaly.max_places', geo).value).toBe('')
    type(field('settings.geo_anomaly.max_cities', geo), '5')
    expect(presetButton('custom').getAttribute('aria-pressed')).toBe('true')
  })
})

describe('PolicyTab, saving', () => {
  // D10: the body carries only what changed, so a tab opened before another
  // admin's save cannot revert it by saving a field of its own.
  it('puts exactly the edited keys and refreshes the settings read', async () => {
    serve({ geo_anomaly_max_cities: 4 })
    const { client } = mount()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    await loaded()
    type(field('settings.geo_anomaly.max_cities'), '')
    type(field('settings.risk.max_devices'), '5')
    fireEvent.click(saveButton())

    await waitFor(() => expect(policyPuts()).toEqual([{ geo_anomaly_max_cities: 0, risk_max_devices: 5 }]))
    await waitFor(() => expect(snack).toHaveBeenCalledWith(L('risk_center.policy.saved'), 'success'))
    const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: useAuthStore.getState().authEpoch })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: settingsKeys.ui(scope) })
    // Reseeded from the answer: nothing is unsaved any more.
    await waitFor(() => expect(saveButton().disabled).toBe(true))
    expect(field('settings.risk.max_devices').value).toBe('5')
  })

  it('saves an emptied runtime knob as 0, the shipped default', async () => {
    serve({ risk_connection_retention_days: 30, risk_live_refresh_cooldown_seconds: 60 })
    mount()
    await loaded()
    type(field('settings.risk_center.connection_retention_days'), '')
    type(field('settings.risk_center.live_refresh_cooldown_seconds'), '45')
    fireEvent.click(saveButton())
    await waitFor(() => expect(policyPuts()).toEqual([
      { risk_connection_retention_days: 0, risk_live_refresh_cooldown_seconds: 45 },
    ]))
  })

  // Another admin saved while this tab was open. The page is not made dirty
  // by it; it says so, and loading the latest keeps this admin's edits.
  it('notices a newer policy without calling it unsaved, and rebases onto it', async () => {
    serve()
    const { client } = mount()
    await loaded()
    type(field('settings.risk.max_devices'), '5')
    server.policy = { ...server.policy, geo_anomaly_max_cities: 7 }
    await act(async () => { await client.refetchQueries() })

    expect(await screen.findByText(L('risk_center.policy.stale'))).toBeTruthy()
    // Only this admin's change is unsaved; the other admin's is not "mine".
    expect(screen.getByText(`未保存：${L('risk_center.policy.part_policy')}`)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: L('risk_center.policy.load_latest') }))
    expect(field('settings.geo_anomaly.max_cities').value).toBe('7')
    expect(field('settings.risk.max_devices').value).toBe('5')
    expect(screen.queryByText(L('risk_center.policy.stale'))).toBeNull()
    fireEvent.click(saveButton())
    await waitFor(() => expect(policyPuts()).toEqual([{ risk_max_devices: 5 }]))
  })

  it('is not dirty after a refetch changed a key nobody touched here', async () => {
    serve()
    const { client } = mount()
    await loaded()
    server.policy = { ...server.policy, risk_min_days: 2 }
    await act(async () => { await client.refetchQueries() })
    expect(await screen.findByText(L('risk_center.policy.stale'))).toBeTruthy()
    expect(saveButton().disabled).toBe(true)
    expect(screen.queryByText(/^未保存：/)).toBeNull()
  })

  // A bad ignore list is refused whole, naming the lines: the page marks
  // the field, opens the panel it sits in, and names what to fix.
  it('marks the ignore list the server refused and opens its panel', async () => {
    serve()
    mount()
    await loaded()
    server.putError = Object.assign(new Error('400'), {
      isAxiosError: true,
      response: { status: 400, data: { error: 'bad', field: 'geo_anomaly_ignore_addresses', bad: ['10.0.0.300'] } },
    })
    type(field('settings.risk.max_devices'), '5')
    fireEvent.click(saveButton())

    const geo = card('risk_center.policy.card.geo')
    await waitFor(() => expect(within(geo).getByRole('button', { name: L('risk_center.policy.advanced') })
      .getAttribute('aria-expanded')).toBe('true'))
    const ignore = field('settings.geo_anomaly.ignore_addresses', geo)
    expect(ignore.getAttribute('aria-invalid')).toBe('true')
    expect(description(ignore)).toContain('10.0.0.300')
    expect(snack).not.toHaveBeenCalledWith(expect.stringMatching(/^保存失败/), 'error')
  })

  it('toasts any other failure', async () => {
    serve()
    mount()
    await loaded()
    server.putError = Object.assign(new Error('500'), { isAxiosError: true, response: { status: 500, data: { error: 'down' } } })
    type(field('settings.risk.max_devices'), '5')
    fireEvent.click(saveButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith('保存失败：down', 'error'))
  })

  it('discards the draft', async () => {
    serve()
    mount()
    await loaded()
    type(field('settings.risk.max_devices'), '5')
    fireEvent.click(screen.getByRole('button', { name: L('risk_center.policy.discard') }))
    expect(field('settings.risk.max_devices').value).toBe('')
    expect(saveButton().disabled).toBe(true)
  })
})

describe('PolicyTab, group exceptions', () => {
  async function pickGroup(name: string) {
    const picker = await within(card('risk_center.policy.card.groups')).findByRole('combobox', { name: L('risk_center.policy.pick_group') })
    await waitFor(() => expect(picker.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(picker)
    fireEvent.click(await screen.findByRole('option', { name }))
  }

  function groupRow(label: string): HTMLElement {
    const groups = card('risk_center.policy.card.groups')
    // The row is the line holding the label, its switch and its value.
    let el: HTMLElement | null = within(groups).getByText(label)
    while (el && !within(el).queryByRole('switch')) el = el.parentElement
    if (!el) throw new Error(`no row ${label}`)
    return el
  }

  it('opens the group named in the URL, with each row explained', async () => {
    serve()
    mount('/admin/risk?tab=policy&group=2')
    await loaded()
    const groups = card('risk_center.policy.card.groups')
    await waitFor(() => expect(within(groups).getByText('城市容错')).toBeTruthy())
    expect(within(groups).getByText(L('settings.geo_anomaly.max_cities_hint'))).toBeTruthy()
    expect(api.get).toHaveBeenCalledWith('/admin/groups/2/scope-settings', expect.anything())
  })

  // The list endpoint pages at 200. The picker reads every page, as the
  // settings scope rail it replaces did: group 201+ is on offer, and a link
  // to one (the group dialog's) shows it picked and names it.
  it('offers every group, past the first page of 200', async () => {
    serve()
    const many = Array.from({ length: 201 }, (_, i) => ({ id: i + 2, slug: `g${i + 2}`, name: `Group ${i + 2}` }))
    const base = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, cfg?: { params?: { page?: number; page_size?: number } }) => {
      if (url !== '/admin/groups') return base(url, cfg)
      const page = cfg?.params?.page ?? 1
      const size = cfg?.params?.page_size ?? 200
      return { data: { items: many.slice((page - 1) * size, page * size), total: many.length, page, page_size: size } }
    })
    mount('/admin/risk?tab=policy&group=202')
    await loaded()
    const picker = await within(card('risk_center.policy.card.groups'))
      .findByRole('combobox', { name: L('risk_center.policy.pick_group') })
    await waitFor(() => expect(picker.textContent).toBe('Group 202'))
    await waitFor(() => expect(within(card('risk_center.policy.card.groups')).getByText('城市容错')).toBeTruthy())
    fireEvent.click(within(groupRow('城市容错')).getByRole('switch'))
    expect(screen.getByText('未保存：分组「Group 202」的例外')).toBeTruthy()
  })

  // The group dialog patches the list it shows, not this catalogue, so each
  // open of the page reads the groups again: a group made since is on offer.
  it('reads the groups again on every open', async () => {
    serve()
    const client = makeTestQueryClient()
    const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: useAuthStore.getState().authEpoch })
    client.setQueryData(allGroupsQuery(scope).queryKey, [{ id: 2, slug: 'team-a', name: 'Team A' }] as Group[])
    mount('/admin/risk?tab=policy&group=3', client)
    await loaded()
    const picker = await within(card('risk_center.policy.card.groups'))
      .findByRole('combobox', { name: L('risk_center.policy.pick_group') })
    await waitFor(() => expect(picker.textContent).toBe('Team B'))
  })

  it('writes the picked group to the URL', async () => {
    serve()
    mount()
    await loaded()
    await pickGroup('Team B')
    await waitFor(() => expect(lastLocation).toBe('/admin/risk?tab=policy&group=3'))
  })

  it('saves the group through its own PUT and DELETE, and names it among the unsaved parts', async () => {
    serve()
    mount('/admin/risk?tab=policy&group=2')
    await loaded()
    await waitFor(() => expect(within(card('risk_center.policy.card.groups')).getByText('城市容错')).toBeTruthy())

    // Override the cities; drop the stored regions override.
    fireEvent.click(within(groupRow('城市容错')).getByRole('switch'))
    fireEvent.change(within(groupRow('城市容错')).getByRole('spinbutton'), { target: { value: '4' } })
    fireEvent.click(within(groupRow('省级容错')).getByRole('switch'))
    expect(screen.getByText(L('risk_center.policy.group_dirty'))).toBeTruthy()
    expect(screen.getByText('未保存：分组「Team A」的例外')).toBeTruthy()
    // The page's own save is for the policy; the group has its own.
    expect(saveButton().disabled).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: L('risk_center.policy.group_save') }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith(L('risk_center.policy.group_saved'), 'success'))
    expect(api.put).toHaveBeenCalledWith('/admin/groups/2/scope-settings',
      { type: 'geo_anomaly', name: 'max_cities', value: '4' })
    expect(api.delete).toHaveBeenCalledWith('/admin/groups/2/scope-settings/geo_anomaly/max_regions')
  })

  it('names both parts while both are unsaved', async () => {
    serve()
    mount('/admin/risk?tab=policy&group=2')
    await loaded()
    await waitFor(() => expect(within(card('risk_center.policy.card.groups')).getByText('城市容错')).toBeTruthy())
    type(field('settings.risk.max_devices'), '5')
    fireEvent.click(within(groupRow('城市容错')).getByRole('switch'))
    expect(screen.getByText('未保存：策略、分组「Team A」的例外')).toBeTruthy()
  })

  it('asks before leaving a group with unsaved exceptions', async () => {
    serve()
    mount('/admin/risk?tab=policy&group=2')
    await loaded()
    await waitFor(() => expect(within(card('risk_center.policy.card.groups')).getByText('城市容错')).toBeTruthy())
    fireEvent.click(within(groupRow('城市容错')).getByRole('switch'))

    confirmMock.mockResolvedValueOnce(false)
    await pickGroup('Team B')
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({
      title: L('risk_center.policy.switch_group_title'), message: L('risk_center.policy.leave_message'),
    })))
    expect(lastLocation).toBe('/admin/risk?tab=policy&group=2')

    confirmMock.mockResolvedValueOnce(true)
    await pickGroup('Team B')
    await waitFor(() => expect(lastLocation).toBe('/admin/risk?tab=policy&group=3'))
  })

  // The group rows show the global value they inherit; a policy save moves
  // it, so the card reads its baseline again.
  it('reloads its baseline after a policy save', async () => {
    serve()
    mount('/admin/risk?tab=policy&group=2')
    await loaded()
    await waitFor(() => expect(within(groupRow('城市容错')).getByText('全局: 2（默认）')).toBeTruthy())
    type(field('settings.geo_anomaly.max_cities'), '5')
    fireEvent.click(saveButton())
    await waitFor(() => expect(within(groupRow('城市容错')).getByText('全局: 5')).toBeTruthy())
  })
})

describe('PolicyTab, leaving', () => {
  it('asks before a tab switch drops unsaved changes', async () => {
    serve()
    mount()
    await loaded()
    type(field('settings.risk.max_devices'), '5')

    confirmMock.mockResolvedValueOnce(false)
    fireEvent.click(screen.getByRole('button', { name: 'go queue' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({
      title: L('risk_center.policy.leave_title'), destructive: true,
    })))
    expect(lastLocation).toBe('/admin/risk?tab=policy')

    confirmMock.mockResolvedValueOnce(true)
    fireEvent.click(screen.getByRole('button', { name: 'go queue' }))
    await waitFor(() => expect(lastLocation).toBe('/admin/risk?tab=queue'))
  })

  it('lets a clean page go without asking', async () => {
    serve()
    mount()
    await loaded()
    fireEvent.click(screen.getByRole('button', { name: 'go queue' }))
    await waitFor(() => expect(lastLocation).toBe('/admin/risk?tab=queue'))
    expect(confirmMock).not.toHaveBeenCalled()
  })
})
