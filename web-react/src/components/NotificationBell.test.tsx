// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, useLocation } from 'react-router'
import { fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import NotificationBell from './NotificationBell'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// Render the default value so assertions read as the copy a user would see.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'en-US' },
  }),
}))

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
    expect(screen.queryByText('暂无通知')).toBeNull()
  })

  it('reports an empty feed as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { alerts: [], counts: { error: 0, warning: 0, info: 0 } } })
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.getByText('暂无通知')).toBeTruthy())
    expect(screen.queryByText('通知暂时不可用')).toBeNull()
  })

  it('renders the geo_anomaly title with its count', async () => {
    api.get.mockResolvedValue(feed({ key: 'geo_anomaly', type: 'geo_anomaly', severity: 'warning', count: 3 }))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    // A singleton with a count, not a row per account: the title is the
    // whole message, and without a case it would render as an empty line.
    await waitFor(() => expect(screen.queryByText('3 个账号被标记为异地并发')).not.toBeNull())
  })

  it('renders the geo_auto_suspended title with its count and its own icon', async () => {
    api.get.mockResolvedValue(feed({ key: 'geo_auto_suspended', type: 'geo_auto_suspended', severity: 'warning', count: 2 }))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.queryByText('2 个账号因异地并发被自动临时暂停')).not.toBeNull())
    // Distinct from the flag's shield: this one says the panel already ACTED.
    expect(screen.queryByTestId('PauseCircleOutlinedIcon')).not.toBeNull()
  })

  it.each(['geo_anomaly', 'geo_auto_suspended'])('opens the Geo tab from %s', async type => {
    api.get.mockResolvedValue(feed({ key: type, type, severity: 'warning', count: 1 }))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))
    fireEvent.click(await screen.findByRole('menuitem'))

    // The tab lists the accounts with the evidence beside each; the Logs
    // page's default tab (subscription logs) would say nothing about them.
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/logs?tab=geo'))
  })
})
