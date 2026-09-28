// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, useLocation } from 'react-router'
import { fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { riskCenterKeys } from '@/query/keys'
import { sessionScope } from '@/query/session'
import NotificationBell from './NotificationBell'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// t over the REAL zh-CN admin bundle (then the defaultValue, then the key),
// so assertions read as the copy a user sees and a key the bell asks for but
// the bundle lacks shows up as its raw key.
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

// Renders where the router currently is, so a test can assert on a deep link
// without mocking navigate.
function Location() {
  const loc = useLocation()
  return <div data-testid="location">{loc.pathname + loc.search}</div>
}

function mount() {
  const client = makeTestQueryClient()
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <NotificationBell />
        <Location />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(client) },
  )
  return client
}

function feed(...alerts: object[]) {
  return { data: { alerts, counts: { error: 0, warning: alerts.length, info: 0 } } }
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})

afterEach(cleanup)

describe('NotificationBell', () => {
  it('says notifications are unavailable when the first load fails, not that there are none', async () => {
    // "No notifications" is a claim about the server; a failed request is not
    // evidence for it. Reporting an empty success here would hide an outage.
    api.get.mockRejectedValue(new Error('network down'))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.getByText('通知暂时不可用')).toBeTruthy())
    expect(screen.queryByText('暂无需要处理的通知')).toBeNull()
  })

  it('reports an empty feed as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { alerts: [], counts: { error: 0, warning: 0, info: 0 } } })
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.getByText('暂无需要处理的通知')).toBeTruthy())
    expect(screen.queryByText('通知暂时不可用')).toBeNull()
  })

  it('renders the risk_queue title with its count and the shield', async () => {
    api.get.mockResolvedValue(feed({ key: 'risk_queue', type: 'risk_queue', severity: 'warning', count: 3 }))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    // One entry for every account that needs action now, not a row each; the
    // words say "needs action now" because suspect-only accounts do not ring,
    // so the number is not the size of the whole queue.
    await waitFor(() => expect(screen.queryByText('3 个账号需立即处理')).not.toBeNull())
    expect(screen.queryByTestId('ShieldOutlinedIcon')).not.toBeNull()
  })

  it('opens the queue filtered to the accounts that need action now, and refreshes it', async () => {
    api.get.mockResolvedValue(feed({ key: 'risk_queue', type: 'risk_queue', severity: 'warning', count: 1 }))
    const client = mount()
    // A queue page already in the cache (the bell lives in the top bar, so the
    // risk center may have been open a minute ago): clicking the entry must
    // not land on that stale page.
    const scope = sessionScope({ userId: null, role: '', authEpoch: 0 })
    const cached = riskCenterKeys.queue(scope, { urgent: true })
    client.setQueryData(cached, { items: [], total: 0 })

    fireEvent.click(await screen.findByLabelText('notifications'))
    fireEvent.click(await screen.findByRole('menuitem'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/risk?tab=queue&urgent=1'))
    expect(client.getQueryState(cached)?.isInvalidated).toBe(true)
  })

  // The entries the risk center's one entry replaced have no deep link any
  // more: a feed that still carried one (a cached response across the
  // upgrade) falls back to the dashboard rather than to a retired tab. The
  // AlertType union no longer names any of the three, so tsc's excess-property
  // check keeps them out of ROUTE; this pins the behaviour for two of them.
  it.each(['geo_anomaly', 'risk_signals'])('has no route for the retired %s entry', async type => {
    api.get.mockResolvedValue(feed({ key: type, type, severity: 'warning', count: 1 }))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))
    fireEvent.click(await screen.findByRole('menuitem'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/dashboard'))
  })
})
