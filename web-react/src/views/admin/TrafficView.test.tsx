// @vitest-environment jsdom
import { createMemoryRouter, RouterProvider } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import TrafficView from './TrafficView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
// ECharts has no layout in jsdom and throws on dispose; not what this tests.
vi.mock('@/components/TrafficChart', () => ({ default: () => null }))
// A separate section with its own tests; not what this file tests.
vi.mock('./UserServerUsage', () => ({ UserServerUsage: () => null }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

function mount(entry = '/') {
  const router = createMemoryRouter([{ path: '/', element: <TrafficView /> }], { initialEntries: [entry] })
  render(<RouterProvider router={router} />, { wrapper: queryWrapper(makeTestQueryClient()) })
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('TrafficView', () => {
  it('reports a failed read instead of rendering an empty, all-zero chart', async () => {
    // The four loads ran inside one Promise.all with no catch, so a failure
    // produced an unhandled rejection and left the page showing zero traffic —
    // indistinguishable from "this fleet moved no data".
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('traffic.load_failed')).toBeTruthy())
  })

  it('reads a tunnel listener\'s row as its line of the landing, and other rows by name', async () => {
    const emptyList = { items: [], total: 0, page: 1, page_size: 25 }
    const base = { panel_name: 'entry', region: 'HK', tags: [], permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 }
    api.get.mockImplementation(async (url: string) => {
      switch (url) {
        case '/admin/traffic/nodes/top':
          return { data: { items: [
            { ...base, node_id: 1, display_name: 'plain-node' },
            { ...base, node_id: 77, display_name: 'hidden-listener', tunnel_line: true, parent_node_id: 5, parent_name: 'HK-1', line_name: 'Mobile' },
          ] } }
        case '/admin/traffic/top': return { data: { items: [] } }
        case '/admin/nodes': case '/admin/users': return { data: emptyList }
        case '/admin/settings/ui': return { data: {} }
        default: return { data: { items: [], buckets: [], series: [], totals: {}, total: 0 } }
      }
    })
    mount()
    fireEvent.click(await screen.findByRole('tab', { name: 'traffic.tab_rank' }))
    fireEvent.click(await screen.findByRole('radio', { name: 'traffic.scope_node' }))
    await screen.findByText('plain-node')
    expect(screen.getByText('traffic.node_tunnel_line')).toBeTruthy()
    expect(screen.queryByText('hidden-listener')).toBeNull()
  })

  it('asks for the history the address names: the user, the range, the granularity and the zone', async () => {
    // Filter state lives in the URL, so a shared link or a refresh lands on the same view, and the
    // request carries exactly what the link says ("view usage" on a user opens ?user=N).
    const emptyList = { items: [], total: 0, page: 1, page_size: 200 }
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/nodes' || url === '/admin/users') return { data: emptyList }
      if (url === '/admin/settings/ui') return { data: {} }
      if (url.startsWith('/admin/traffic/user/7/history')) return { data: { scope: 'user', user_id: 7, period: 'hour', since: '', until: '', items: [] } }
      return { data: { items: [] } }
    })
    mount('/?user=7&range=7&period=hour&tz=Asia/Tokyo')

    await waitFor(() => expect(api.get.mock.calls.some(([u]) => u === '/admin/traffic/user/7/history')).toBe(true))
    const call = api.get.mock.calls.find(([u]) => u === '/admin/traffic/user/7/history')!
    expect(call[1].params).toMatchObject({ period: 'hour', tz: 'Asia/Tokyo' })
    // The id has no row in the (empty) user list, and still reads as that user, not as "all users".
    expect(await screen.findByDisplayValue('#7')).toBeTruthy()
  })
})
