// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserNodeUsage } from './UserNodeUsage'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'en-US' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount(userId = 2) {
  render(
    <ThemeProvider theme={theme}>
      <UserNodeUsage userId={userId} />
    </ThemeProvider>,
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

    await waitFor(() => expect(screen.getByText('暂时无法获取节点用量')).toBeTruthy())
    expect(screen.queryByText('该用户暂无节点')).toBeNull()
  })

  it('reports an empty breakdown as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText('该用户暂无节点')).toBeTruthy())
    expect(screen.queryByText('暂时无法获取节点用量')).toBeNull()
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

    fireEvent.click(screen.getByRole('button', { name: '原始' }))
    expect(screen.getByText('4.0K')).toBeTruthy()
    expect(screen.getAllByText('↑1.0K ↓1.0K').length).toBeGreaterThan(0)
  })
})
