// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserServerUsage } from './UserServerUsage'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// The real English strings, so an assertion reads what a person reads and a missing key shows as a raw key.
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)


function mount(userId = 2) {
  render(
    <UserServerUsage userId={userId} />,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('UserServerUsage', () => {
  it('reports a failed load as unavailable instead of claiming the user has no servers', async () => {
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('Server usage is temporarily unavailable')).toBeTruthy())
    expect(screen.queryByText('No servers for this user')).toBeNull()
  })

  it('reports an empty breakdown as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText('No servers for this user')).toBeTruthy())
    expect(screen.queryByText('Server usage is temporarily unavailable')).toBeNull()
  })

  const row = { panel_id: 1, server_name: 'srv-a', node_count: 2, lifetime_up_bytes: 1024, lifetime_down_bytes: 1024, lifetime_total_bytes: 2048, period_up_bytes: 512, period_down_bytes: 512, period_total_bytes: 1024, today_up_bytes: 0, today_down_bytes: 0, today_total_bytes: 0, traffic_factor_milli: 1500, charged_lifetime_bytes: 3072, charged_period_bytes: 1536 }

  it('shows a mixed chip instead of a multiplier when the server lines are priced differently', async () => {
    api.get.mockResolvedValue({ data: { items: [{ ...row, traffic_factor_milli: null }] } })
    mount()
    await waitFor(() => expect(screen.getByText('srv-a')).toBeTruthy())
    expect(screen.getByTestId('traffic-factor-mixed')).toBeTruthy()
    expect(screen.queryByTestId('traffic-factor-badge')).toBeNull()
  })

  it('defaults to charged totals, shows the factor badge, and toggles to raw', async () => {
    api.get.mockResolvedValue({ data: { items: [row, { ...row, panel_id: 2, server_name: 'srv-b', traffic_factor_milli: 1000, charged_lifetime_bytes: 2048, charged_period_bytes: 1024 }] } })
    mount()

    await waitFor(() => expect(screen.getByText('srv-a')).toBeTruthy())
    // One badge: only the ×1.5 server, not the 1000 one.
    expect(screen.getAllByTestId('traffic-factor-badge').map(b => b.textContent)).toEqual(['×1.5'])
    // Charged mode: srv-a lifetime shows 3.0K charged (grand total 5.0K) with the raw amount beneath.
    expect(screen.getByText('3.0K')).toBeTruthy()
    expect(screen.getByText('5.0K')).toBeTruthy()
    expect(screen.queryByText('↑1.0K ↓1.0K')).toBeNull()

    fireEvent.click(screen.getByRole('radio', { name: 'Raw' }))
    // Raw mode: unchanged behaviour, up/down splits are back and the charged figure is gone.
    expect(screen.getAllByText('↑1.0K ↓1.0K').length).toBeGreaterThan(0)
    expect(screen.queryByText('3.0K')).toBeNull()
    expect(screen.getAllByText('2.0K').length).toBeGreaterThan(0)
  })
})
