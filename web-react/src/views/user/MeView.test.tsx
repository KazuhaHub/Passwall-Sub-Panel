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

function mount() {
  render(
    <MemoryRouter>
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
