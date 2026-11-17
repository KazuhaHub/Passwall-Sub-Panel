// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import TrafficView from './TrafficView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
// ECharts has no layout in jsdom and throws on dispose; not what this tests.
vi.mock('@/components/TrafficChart', () => ({ default: () => null }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <TrafficView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
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

    await waitFor(() => expect(screen.getByText('暂时无法加载流量数据')).toBeTruthy())
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
    fireEvent.click(await screen.findByRole('button', { name: 'traffic.scope_node' }))
    await screen.findByText('plain-node')
    expect(screen.getByText('traffic.node_tunnel_line')).toBeTruthy()
    expect(screen.queryByText('hidden-listener')).toBeNull()
  })
})
