// @vitest-environment jsdom
import { MemoryRouter } from 'react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import DashboardView from './DashboardView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
// The real chart mounts ECharts, which has no layout in jsdom and throws on
// dispose; it is not what this file is testing.
vi.mock('@/components/TrafficChart', () => ({ default: () => null }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

function mount() {
  render(
    <MemoryRouter>
      <DashboardView />
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('DashboardView', () => {
  it('says the summary is unavailable rather than showing a zeroed all-clear', async () => {
    // Every figure on this page is `summary?.x ?? 0`. With the loader's failure
    // silently clearing `loading`, a failed read rendered "0 users / 0 nodes /
    // all nodes healthy" — a clean bill of health for a fleet nobody read.
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('dashboard.summary_failed')).toBeTruthy())
    // And none of the figures that would have read as zero.
    expect(screen.queryByText('dashboard.metric.users')).toBeNull()
  })

  it('links each attention card to the page where it is resolved and says why a node is listed', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/dashboard/summary') {
        return { data: {
          user_total: 3, user_enabled: 2, user_disabled: 1, user_emergency: 0,
          node_total: 2, node_enabled: 2, node_healthy: 1, group_count: 1,
          expiring_users: [],
          node_alerts: [{ id: 9, display_name: 'HK-1', panel_name: 'edge-a', health_state: 'unreachable' }],
        } }
      }
      return { data: { items: [] } }
    })
    mount()

    await screen.findByText('HK-1')
    // The state is worded, not only coloured.
    expect(screen.getByText('unreachable')).toBeTruthy()
    const hrefs = screen.getAllByRole('link').map(a => a.getAttribute('href'))
    expect(hrefs).toContain('/admin/nodes')
    expect(hrefs).toContain('/admin/users')
    expect(hrefs).toContain('/admin/traffic')
    // Nothing expires, so that card is a quiet all-clear.
    expect(screen.getByText('dashboard.alerts.expiring_empty')).toBeTruthy()
  })
})
