// @vitest-environment jsdom
import { StrictMode, type ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import type { RuntimeKnob, RuntimeKnobValues, UISettings } from '@/api/settings'

// Its own harness rather than adminSaveHarness: that one's t returns the bare
// key, and the point here is the NUMBER in each "in effect" caption, which
// only an interpolating t over the real bundle shows.
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('@/i18n', () => ({ default: { t: (key: string) => key, language: 'zh-CN' } }))
vi.mock('@/components/CodeEditor', () => ({ default: () => null }))
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
  Trans: ({ children }: { children: ReactElement }) => children,
}))

import zh from '@/locales/zh-CN/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import SettingsView from './SettingsView'
dict.current = flatten(zh as Nested)

// The 23 runtime knobs (ports.RuntimeEffective), each with the settings key
// its label lives under: the five per-group thresholds in the risk block,
// the risk center's five in its own, the thirteen former constants in the
// advanced panel.
const KNOBS: [RuntimeKnob, string][] = [
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
const ADVANCED = KNOBS.slice(10).map(([k]) => k)

// Distinct numbers, so a caption that reads the wrong key shows the wrong one.
const EFFECTIVE: RuntimeKnobValues = Object.fromEntries(KNOBS.map(([k], i) => [k, 100 + i]))
const DEFAULTS: RuntimeKnobValues = Object.fromEntries(KNOBS.map(([k], i) => [k, 500 + i]))

function settings(overrides: Partial<UISettings> = {}): UISettings {
  return {
    sub_base_url: 'https://panel.example.test', panel_path: '', timezone: 'UTC',
    cron_traffic_pull_minutes: 5, cron_reconcile_minutes: 5, node_poll_seconds: 30, full_report_seconds: 60,
    node_task_offline_reconcile_days: 30, node_task_backup_restore_days: 30, node_task_result_retention_days: 90,
    max_panel_concurrency: 8, audit_retention_days: 90, auth_event_retention_days: 90, sync_task_retention_days: 90,
    traffic_history_days: 730, expire_before_days: 7, traffic_remain_percent: 10,
    emergency_access_enabled: false, emergency_access_hours: 1, emergency_access_max_count: 1, emergency_access_quota_gb: 0,
    geo_ip_enabled: false, geo_ip_auto_update: false, geo_ip_update_source: 'dbip',
    geo_anomaly_scope: '', geo_anomaly_co_travel: '', geo_anomaly_ignore_addresses: '',
    sub_clients: [], quick_links: [],
    ...Object.fromEntries(KNOBS.map(([k]) => [k, 0])),
    runtime_effective: EFFECTIVE,
    runtime_defaults: DEFAULTS,
    ...overrides,
  } as UISettings
}

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })

async function mountSettings(s: UISettings) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/settings/ui') return { data: s }
    if (url === '/admin/groups') return { data: { items: [], total: 0, page: 1, page_size: 25 } }
    throw new Error(`Unexpected GET ${url}`)
  })
  render(
    <StrictMode>
      <MemoryRouter>
        <ThemeProvider theme={theme}>
          <QueryClientProvider client={makeTestQueryClient()}><SettingsView /></QueryClientProvider>
        </ThemeProvider>
      </MemoryRouter>
    </StrictMode>,
  )
  await screen.findByText(dict.current['settings.risk_center.section'])
}

function field(labelKey: string): HTMLInputElement {
  return screen.getByRole('spinbutton', { name: dict.current[labelKey] }) as HTMLInputElement
}

function description(input: HTMLInputElement): string {
  const id = input.getAttribute('aria-describedby') ?? ''
  return document.getElementById(id)?.textContent ?? ''
}

function advancedToggle(): HTMLElement {
  return screen.getByRole('button', { name: dict.current['settings.risk_center.advanced'] })
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  api.put.mockImplementation(async (_url: string, data: UISettings) => ({ data }))
})
afterEach(cleanup)

describe('geo and risk runtime knobs', () => {
  it('SettingsView renders the runtime knobs with their values in effect', async () => {
    await mountSettings(settings())
    // Nothing advanced is configured, so the panel starts closed.
    expect(advancedToggle().getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(advancedToggle())
    await waitFor(() => expect(advancedToggle().getAttribute('aria-expanded')).toBe('true'))

    for (const [knob, labelKey] of KNOBS) {
      const input = field(labelKey)
      // Unset is an empty field whose placeholder is the shipped default,
      // not a 0 that reads as "zero days".
      expect(input.value, knob).toBe('')
      expect(input.placeholder, knob).toBe(String(DEFAULTS[knob]))
      // The number the server runs with, from the server: this page holds
      // no copy of any default or clamp.
      expect(description(input), knob).toContain(`当前生效：${EFFECTIVE[knob]}`)
    }
  })

  it('shows a stored value as typed, beside what it became', async () => {
    // A flag threshold of 1 is judged as 2 (one day over is a download); the
    // field keeps the admin's 1 and the caption says 2.
    await mountSettings(settings({
      risk_usage_flag_days: 1,
      runtime_effective: { ...EFFECTIVE, risk_usage_flag_days: 2 },
    }))
    const input = field('settings.risk.usage_flag_days')
    expect(input.value).toBe('1')
    expect(description(input)).toContain('当前生效：2')
  })

  it('opens the advanced panel when one of its knobs is configured', async () => {
    // A changed former constant must not hide behind a closed panel.
    for (const knob of ADVANCED) {
      await mountSettings(settings({ [knob]: 7 }))
      expect(advancedToggle().getAttribute('aria-expanded'), knob).toBe('true')
      cleanup()
    }
  })

  it('saves an emptied field as 0, the shipped default, and never sends a number it did not show', async () => {
    await mountSettings(settings({ risk_connection_retention_days: 30, risk_live_refresh_cooldown_seconds: 60 }))
    fireEvent.change(field('settings.risk_center.connection_retention_days'), { target: { value: '' } })
    fireEvent.change(field('settings.risk_center.live_refresh_cooldown_seconds'), { target: { value: '45' } })
    fireEvent.click(screen.getByRole('button', { name: dict.current['settings.save'] }))

    await waitFor(() => expect(api.put).toHaveBeenCalledOnce())
    expect(api.put).toHaveBeenCalledWith('/admin/settings/ui', expect.objectContaining({
      risk_connection_retention_days: 0,
      risk_live_refresh_cooldown_seconds: 45,
    }))
  })

  it('renders without the maps an older server does not send', async () => {
    await mountSettings(settings({ runtime_effective: undefined, runtime_defaults: undefined }))
    const input = field('settings.risk_center.connection_retention_days')
    expect(input.placeholder).toBe('')
    expect(description(input)).not.toContain('当前生效')
  })
})
