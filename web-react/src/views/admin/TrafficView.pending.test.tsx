// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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

// A promise the test resolves on its own schedule, so it can inspect the
// button mid-request instead of racing a real network call.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

const emptyList = { items: [], total: 0, page: 1, page_size: 500 }
const emptyHistory = { scope: 'all' as const, period: 'day' as const, since: '2026-01-01', until: '2026-01-01', items: [] }

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('TrafficView refresh buttons', () => {
  // The rank leaderboard's isPending flips to false as soon as the FIRST
  // load lands, so a manual refresh on a slow link left this button looking
  // inert for the whole retry window (see TrafficView.tsx's `loading` /
  // `activeRank.isPending` split). The fix reads `activeRank.isFetching`
  // instead, which stays true for every refetch, not just the first one.
  it('shows a busy Rank-tab Refresh button while a refetch is in flight, and a second click does not start a second one', async () => {
    const row = { user_id: 1, upn: 'user@test', permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 }
    let topCalls = 0
    const second = deferred<{ data: { items: (typeof row)[] } }>()
    api.get.mockImplementation((url: string) => {
      switch (url) {
        case '/admin/traffic/top':
          topCalls += 1
          return topCalls === 1 ? Promise.resolve({ data: { items: [row] } }) : second.promise
        case '/admin/traffic/nodes/top':
          return Promise.resolve({ data: { items: [] } })
        case '/admin/traffic/history':
          return Promise.resolve({ data: emptyHistory })
        case '/admin/nodes':
        case '/admin/users':
          return Promise.resolve({ data: emptyList })
        case '/admin/settings/ui':
          return Promise.resolve({ data: {} })
        default:
          return Promise.reject(new Error(`unexpected GET ${url}`))
      }
    })

    mount()
    fireEvent.click(screen.getByRole('tab', { name: 'traffic.tab_rank' }))
    await screen.findByText('user@test')
    expect(topCalls).toBe(1)

    const button = screen.getByRole('button', { name: 'traffic.refresh' })
    fireEvent.click(button)

    await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('true'))
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(within(button).getByRole('progressbar')).toBeTruthy()

    // A second click while the first refetch is still pending must not fire
    // a second request.
    fireEvent.click(button)
    expect(topCalls).toBe(2)

    await act(async () => { second.resolve({ data: { items: [row] } }); await second.promise })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    expect(button.getAttribute('aria-busy')).toBeNull()
  })

  // Same gap on the Trend tab's chart Refresh: `chartLoading` /
  // `historyQuery.isPending` is deliberately kept as the "loading, not empty"
  // signal for the chart itself (see the SHARED PIECES note), so the button's
  // OWN busy state has to come from `historyQuery.isFetching` instead.
  it('shows a busy Trend-tab Refresh button while the chart history is refetching, and a second click does not start a second one', async () => {
    let historyCalls = 0
    const second = deferred<{ data: typeof emptyHistory }>()
    api.get.mockImplementation((url: string) => {
      switch (url) {
        case '/admin/traffic/top':
        case '/admin/traffic/nodes/top':
          return Promise.resolve({ data: { items: [] } })
        case '/admin/traffic/history':
          historyCalls += 1
          return historyCalls === 1 ? Promise.resolve({ data: emptyHistory }) : second.promise
        case '/admin/nodes':
        case '/admin/users':
          return Promise.resolve({ data: emptyList })
        case '/admin/settings/ui':
          return Promise.resolve({ data: {} })
        default:
          return Promise.reject(new Error(`unexpected GET ${url}`))
      }
    })

    mount()
    // Trend is the default tab. Wait for the first history load to settle
    // before touching Refresh.
    await waitFor(() => expect(historyCalls).toBe(1))
    const button = screen.getByRole('button', { name: 'traffic.refresh' })
    await waitFor(() => expect(button.getAttribute('aria-busy')).toBeNull())

    fireEvent.click(button)
    await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('true'))
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(within(button).getByRole('progressbar')).toBeTruthy()

    fireEvent.click(button)
    expect(historyCalls).toBe(2)

    await act(async () => { second.resolve({ data: emptyHistory }); await second.promise })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    expect(button.getAttribute('aria-busy')).toBeNull()
  })
})
