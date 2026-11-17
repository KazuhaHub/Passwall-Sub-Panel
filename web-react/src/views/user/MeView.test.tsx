// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import MeView from './MeView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
// The real module initializes i18next on import, which rejects in a jsdom test
// without its full setup and surfaces as an unhandled error.
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'en-US' } }))
// The UI language, per test: the service notice picks its text by it.
const ui = vi.hoisted(() => ({ language: 'en-US' }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { get language() { return ui.language } },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount(entry = '/') {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <ThemeProvider theme={theme}>
        <MeView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  ui.language = 'en-US'
})

describe('MeView server status lines', () => {
  function serveStatus(nodes: unknown[]) {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/user/me') return { data: profile }
      if (url === '/user/me/server-status') return { data: { nodes } }
      if (url === '/user/me/traffic') return { data: { user_id: 1, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 } }
      if (url === '/user/me/traffic/history') return { data: { points: [] } }
      throw new Error(`Unexpected GET ${url}`)
    })
  }
  it('renders relay names with independent only-line options and shared entry statuses', async () => {
    serveStatus([{ name: 'Parent', region: 'JP', status: 'ok', show_direct: false,
      relay_statuses: [{ name: 'Relay only', only_line_name: true, status: 'down' }, { name: 'Relay combined', status: 'ok' }],
    }])
    mount('/?tab=status')
    await screen.findByText('Relay only')
    expect(screen.getByText('Parent Relay combined')).toBeTruthy()
    expect(screen.queryByText('Parent')).toBeNull()
    expect(screen.queryByText('Parent Relay only')).toBeNull()
    expect(screen.getAllByText('离线')).toHaveLength(1)
    expect(screen.getAllByText('正常')).toHaveLength(1)
  })
  it('badges only nodes whose factor differs from 1000 and labels used traffic as charged', async () => {
    serveStatus([
      { name: 'Plain', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000 },
      { name: 'Pricey', region: 'US', status: 'ok', show_direct: true, traffic_factor_milli: 1500 },
      { name: 'Gratis', region: 'DE', status: 'ok', show_direct: true, traffic_factor_milli: 0 },
    ])
    mount('/?tab=status')
    await screen.findByText('Pricey')
    expect(screen.getAllByTestId('traffic-factor-badge').map(b => b.textContent)).toEqual(['×1.5', '不计流量'])
  })
  it('badges each direct and relay line by its own factor and ignores hidden direct lines for the charged label', async () => {
    serveStatus([{ name: 'Parent', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000,
      relay_statuses: [{ name: 'Cheap', status: 'ok', traffic_factor_milli: 0 }, { name: 'Dear', status: 'ok', traffic_factor_milli: 2000 }, { name: 'Same', status: 'ok', traffic_factor_milli: 1000 }] }])
    mount('/?tab=status')
    await screen.findByText('Parent Cheap')
    expect(screen.getAllByTestId('traffic-factor-badge').map(b => b.textContent)).toEqual(['不计流量', '×2'])
  })
  it('labels used traffic as charged when only a relay line has a custom factor', async () => {
    serveStatus([{ name: 'P', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000,
      relay_statuses: [{ name: 'R', status: 'ok', traffic_factor_milli: 1500 }] }])
    mount('/')
    await screen.findByText('profile.traffic_used_charged')
  })
  it('labels used traffic as charged when a hidden direct line has a custom factor', async () => {
    serveStatus([{ name: 'P', region: 'JP', status: 'ok', show_direct: false, traffic_factor_milli: 1500,
      relay_statuses: [{ name: 'R', status: 'ok', traffic_factor_milli: 1000 }] }])
    mount('/')
    await screen.findByText('profile.traffic_used_charged')
  })
  it('names a relay row with the backend-resolved node name', async () => {
    serveStatus([{ name: 'P ×1', region: 'JP', status: 'ok', show_direct: false, traffic_factor_milli: 1000,
      relay_statuses: [{ name: 'R', node_name: 'P ×2', status: 'ok', traffic_factor_milli: 2000 }] }])
    mount('/?tab=status')
    await screen.findByText('P ×2 R')
  })
  it('uses the plain used-traffic label when every factor is default', async () => {
    serveStatus([{ name: 'Plain', region: 'JP', status: 'ok', show_direct: true, traffic_factor_milli: 1000 }])
    mount('/')
    await screen.findByText('profile.traffic_used')
    expect(screen.queryByText('profile.traffic_used_charged')).toBeNull()
  })
  it('labels used traffic as charged when a visible node has a custom factor', async () => {
    serveStatus([{ name: 'Pricey', region: 'US', status: 'ok', show_direct: true, traffic_factor_milli: 1500 }])
    mount('/')
    await screen.findByText('profile.traffic_used_charged')
    expect(screen.queryByText('profile.traffic_used')).toBeNull()
  })
  it('shows the empty state when every entry is hidden or inactive', async () => {
    serveStatus([{ name: 'Hidden', region: 'JP', status: 'ok', show_direct: false }])
    mount('/?tab=status')
    await screen.findByText('暂无可显示的节点')
    expect(screen.queryByText('Hidden')).toBeNull()
  })
  it('shows coarse quality and rtt on relay lines only when present', async () => {
    serveStatus([{ name: 'P', region: 'JP', status: 'ok', show_direct: true,
      relay_statuses: [
        { name: 'Good', only_line_name: true, status: 'ok', quality: 'ok', rtt_ms: 42.4 },
        { name: 'Poor', only_line_name: true, status: 'ok', quality: 'degraded', rtt_ms: 180 },
        { name: 'Dead', only_line_name: true, status: 'ok', quality: 'down' },
        { name: 'Unknown', only_line_name: true, status: 'ok' },
      ] }])
    mount('/?tab=status')
    await screen.findByText('Good')
    const chips = screen.getAllByTestId('relay-quality')
    expect(chips.map(c => c.dataset.quality)).toEqual(['ok', 'degraded', 'down'])
    // The test translator returns the default text uninterpolated.
    expect(chips[0].textContent).toBe('良好{{ms}} ms')
    expect(chips[1].textContent).toBe('较差{{ms}} ms')
    expect(chips[2].textContent).toBe('不可用')
  })
  it('keeps legacy direct and relay responses working without the new fields', async () => {
    serveStatus([{ name: 'Old', region: 'JP', status: 'unknown', relay_statuses: [{ name: 'Relay', status: 'unknown' }] }])
    mount('/?tab=status')
    await screen.findByText('Old Relay')
    expect(screen.getByText('Old')).toBeTruthy()
    expect(screen.queryByTestId('relay-quality')).toBeNull()
  })
})
afterEach(cleanup)

const profile = {
  id: 1,
  upn: 'user@example.test',
  display_name: 'User',
  role: 'user',
  enabled: true,
  can_change_password: true,
  can_edit_personal_rules: true,
  sub_url: 'https://sub.example.test/token',
  uuid: 'uuid-1',
  traffic_limit_bytes: 0,
  traffic_reset_period: 'monthly',
  expire_at: null,
}

function serveProfile(over: Record<string, unknown>) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/user/me') return { data: { ...profile, ...over } }
    if (url === '/user/me/traffic') return { data: { user_id: 1, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 } }
    if (url === '/user/me/traffic/history') return { data: { points: [] } }
    throw new Error(`Unexpected GET ${url}`)
  })
}

// What geoenforce.go writes into service_disable_detail: Chinese, with the
// minutes the suspension lasts.
const GEO_AUTO_DETAIL = '检测到账号同时在同一国家的 2 个省或州使用，代理服务已临时暂停，约 60 分钟后自动恢复'
const GEO_AUTO_TITLE = '服务已临时暂停'
const GEO_AUTO_BODY = '检测到账号在多个地区同时使用，代理服务已临时暂停，到时会自动恢复。如有疑问请联系管理员。'
const geoAuto = {
  service_status: 'manual_suspended',
  service_disabled_reason: 'geo_auto',
  service_disable_detail: GEO_AUTO_DETAIL,
}

describe('MeView', () => {
  it('explains itself when the profile read fails instead of rendering a blank page', async () => {
    // The page used to `return null` when the profile was missing, and the
    // loader had no catch — so a failed read produced a completely blank
    // screen with no spinner, no message and nothing to retry.
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载，请稍后重试')).toBeTruthy())
  })
})

describe('MeView service notice for an automatic geo suspension', () => {
  it('gives a non-Chinese UI the localized notice, not the stored Chinese detail', async () => {
    // The backend has no reader locale and writes the detail in Chinese. An
    // English reader used to get that Chinese sentence as the whole notice.
    // (This mock's t returns each call's default, so the texts read here are
    // the ones the component asks for, whatever language it ships them in.)
    ui.language = 'en-US'
    serveProfile(geoAuto)
    mount()

    await screen.findByText(GEO_AUTO_TITLE)
    expect(screen.getByText(GEO_AUTO_BODY)).toBeTruthy()
    expect(screen.queryByText(GEO_AUTO_DETAIL)).toBeNull()
  })

  it.each(['zh-CN', 'zh-TW'])('keeps the stored detail, which states the minutes, for a %s UI', async lang => {
    ui.language = lang
    serveProfile(geoAuto)
    mount()

    await screen.findByText(GEO_AUTO_TITLE)
    expect(screen.getByText(GEO_AUTO_DETAIL)).toBeTruthy()
    expect(screen.queryByText(GEO_AUTO_BODY)).toBeNull()
  })

  it.each(['en-US', 'zh-CN'])('still shows an admin\'s own suspension note in a %s UI', async lang => {
    // Only the detector's hold is replaced. An admin's note is theirs, in
    // whatever language they wrote it, and is the only place its reason lives.
    ui.language = lang
    serveProfile({
      service_status: 'manual_suspended',
      service_disabled_reason: 'service_manual',
      service_disable_detail: 'Unpaid invoice — contact billing',
    })
    mount()

    await screen.findByText('服务已暂停')
    expect(screen.getByText('Unpaid invoice — contact billing')).toBeTruthy()
    expect(screen.queryByText(GEO_AUTO_TITLE)).toBeNull()
  })
})
