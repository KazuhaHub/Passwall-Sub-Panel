/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import type { RiskUserRow } from '@/api/riskSignals'
import RiskCenterView from './RiskCenterView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
// t over the REAL zh-CN admin bundle, flattened as the SPA registers it, so a
// key the page asks for but the bundle lacks shows up as its raw key instead
// of passing on a defaultValue.
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

function Where() {
  const loc = useLocation()
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

function mount(url: string) {
  render(
    <MemoryRouter initialEntries={[url]}>
      <ThemeProvider theme={theme}>
        <Routes>
          <Route path="/admin/risk" element={<><RiskCenterView /><Where /></>} />
          <Route path="/admin/dashboard" element={<><p>dashboard</p><Where /></>} />
        </Routes>
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

// Both tab reads answer; the location-database status fails, which costs the
// Geo tab nothing but its advisory banner.
function serve(risk: RiskUserRow[] = []) {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/geo-anomalies') return { data: { items: [] } }
    if (url === '/admin/risk-signals') return { data: { items: risk } }
    throw new Error(`unexpected GET ${url}`)
  })
}

function fetched(url: string): boolean {
  return api.get.mock.calls.some(([u]) => u === url)
}

function selectedTab(): string {
  const tab = screen.getAllByRole('tab').find(el => el.getAttribute('aria-selected') === 'true')
  return tab?.textContent ?? ''
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

describe('RiskCenterView', () => {
  it('opens on the location tab by default', async () => {
    serve()
    mount('/admin/risk')

    expect(await screen.findByRole('heading', { name: '风控中心' })).toBeTruthy()
    expect(selectedTab()).toBe('异地并发')
    await waitFor(() => expect(fetched('/admin/geo-anomalies')).toBe(true))
    // Only the open tab reads: the risk list is not fetched behind it.
    expect(fetched('/admin/risk-signals')).toBe(false)
  })

  it('renders the location tab from ?tab=geo', async () => {
    serve()
    mount('/admin/risk?tab=geo')

    await waitFor(() => expect(fetched('/admin/geo-anomalies')).toBe(true))
    expect(selectedTab()).toBe('异地并发')
  })

  it('renders the risk tab from ?tab=risk', async () => {
    serve()
    mount('/admin/risk?tab=risk')

    await waitFor(() => expect(fetched('/admin/risk-signals')).toBe(true))
    expect(selectedTab()).toBe('风险信号')
    expect(fetched('/admin/geo-anomalies')).toBe(false)
  })

  // Each risk row carries the account's concurrent-location verdict, and its
  // chip is the way to the evidence behind it. The URL owns the tab, so the
  // chip writes it there: a refresh or a copied link lands on the same tab.
  it("the risk tab's location link switches to ?tab=geo", async () => {
    serve([{ user_id: 7, upn: 'alice', signals: [],
      geo: { state: 'suspect', flagged: false, tier: 'region', updated_at_ms: 1 } }])
    mount('/admin/risk?tab=risk')

    const row = (await screen.findByText('alice')).closest('tr') as HTMLElement
    fireEvent.click(within(row).getByRole('button', { name: '在「异地并发」中查看' }))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/risk?tab=geo'))
    expect(selectedTab()).toBe('异地并发')
    await waitFor(() => expect(fetched('/admin/geo-anomalies')).toBe(true))
  })

  // The route is admin-only already (ADMIN_ONLY_ROUTES bounces an operator in
  // RequireAuth); the page checks its own capability as well, so it never
  // asks an adminGroup endpoint for an answer that can only be 403.
  it('renders nothing and redirects without risk.view', async () => {
    serve()
    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mount('/admin/risk?tab=geo')

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/dashboard'))
    expect(screen.getByText('dashboard')).toBeTruthy()
    expect(screen.queryByRole('tab')).toBeNull()
    expect(api.get).not.toHaveBeenCalled()
  })
})
