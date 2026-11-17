// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserServerUsage } from './UserServerUsage'

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
      <UserServerUsage userId={userId} />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('UserServerUsage', () => {
  it('reports a failed load as unavailable instead of claiming the user has no servers', async () => {
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法获取服务器用量')).toBeTruthy())
    expect(screen.queryByText('该用户暂无服务器')).toBeNull()
  })

  it('reports an empty breakdown as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText('该用户暂无服务器')).toBeTruthy())
    expect(screen.queryByText('暂时无法获取服务器用量')).toBeNull()
  })

  const row = { panel_id: 1, server_name: 'srv-a', node_count: 2, lifetime_up_bytes: 1024, lifetime_down_bytes: 1024, lifetime_total_bytes: 2048, period_up_bytes: 512, period_down_bytes: 512, period_total_bytes: 1024, today_up_bytes: 0, today_down_bytes: 0, today_total_bytes: 0, traffic_factor_milli: 1500, charged_lifetime_bytes: 3072, charged_period_bytes: 1536 }

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

    fireEvent.click(screen.getByRole('button', { name: '原始' }))
    // Raw mode: unchanged behaviour, up/down splits are back and the charged figure is gone.
    expect(screen.getAllByText('↑1.0K ↓1.0K').length).toBeGreaterThan(0)
    expect(screen.queryByText('3.0K')).toBeNull()
    expect(screen.getAllByText('2.0K').length).toBeGreaterThan(0)
  })
})
