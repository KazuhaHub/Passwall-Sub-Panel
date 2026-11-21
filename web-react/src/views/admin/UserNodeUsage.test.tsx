// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserNodeUsage } from './UserNodeUsage'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// The real English strings, so an assertion reads what a person reads and a missing key shows as a raw key.
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)


function mount(userId = 2) {
  render(
    <UserNodeUsage userId={userId} />,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('UserNodeUsage', () => {
  it('reports a failed load as unavailable instead of claiming the user has no nodes', async () => {
    // "This user has no nodes" is a statement about their group coverage. A
    // failed request is not evidence for it, and here it would send an admin
    // looking for a permissions problem that may not exist.
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('Node usage is temporarily unavailable')).toBeTruthy())
    expect(screen.queryByText('No nodes for this user')).toBeNull()
  })

  it('reports an empty breakdown as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText('No nodes for this user')).toBeTruthy())
    expect(screen.queryByText('Node usage is temporarily unavailable')).toBeNull()
  })

  const row = {
    node_id: 1, display_name: 'hk-1', panel_name: 'srv', region: 'HK', client_email: 'u@x',
    lifetime_up_bytes: 1024, lifetime_down_bytes: 1024, lifetime_total_bytes: 2048,
    period_up_bytes: 512, period_down_bytes: 512, period_total_bytes: 1024,
    today_up_bytes: 0, today_down_bytes: 0, today_total_bytes: 0,
    traffic_factor_milli: 0, charged_lifetime_bytes: 0, charged_period_bytes: 0,
  }

  it('defaults to charged totals with a factor badge and switches to raw', async () => {
    api.get.mockResolvedValue({ data: { items: [row, { ...row, node_id: 2, display_name: 'jp-1', region: 'JP', traffic_factor_milli: 1000, charged_lifetime_bytes: 2048, charged_period_bytes: 1024 }] } })
    mount()

    await waitFor(() => expect(screen.getByText('hk-1')).toBeTruthy())
    expect(screen.getAllByTestId('traffic-factor-badge')).toHaveLength(1)
    // Charged: the free node contributes 0 to the 2.0K grand total.
    expect(screen.queryByText('4.0K')).toBeNull()
    expect(screen.getAllByText('2.0K').length).toBeGreaterThan(0)
    expect(screen.queryByText('↑1.0K ↓1.0K')).toBeNull()

    fireEvent.click(screen.getByRole('radio', { name: 'Raw' }))
    expect(screen.getByText('4.0K')).toBeTruthy()
    expect(screen.getAllByText('↑1.0K ↓1.0K').length).toBeGreaterThan(0)
  })
})
