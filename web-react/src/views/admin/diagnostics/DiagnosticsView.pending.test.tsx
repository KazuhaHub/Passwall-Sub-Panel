// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { productionSnapshot, serverList, THREE_XUI } from '@/test/diagnosticsFixtures'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
// The reset is gated by ConfirmHost, which is not mounted here; resolve it
// true so the request itself fires and its wait is observable.
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: vi.fn(async () => true) }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string) => (k.includes(':') ? k : `admin:${k}`),
    i18n: { language: 'en-US', exists: () => false },
  }),
}))

import DiagnosticsView from './DiagnosticsView'

// SLOW-NETWORK FEEDBACK: on a 3-10 s link, a click that gets no visible
// response reads as a dead control and invites a duplicate request. These
// cases hold a request open (a promise the test resolves by hand) and assert
// the control goes busy at once and ignores a second click meanwhile.

function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const METRICS = '/admin/diagnostics/metrics'

function reads(metrics: () => Promise<unknown> = async () => ({ data: productionSnapshot() })) {
  api.get.mockImplementation(async (url: string) => {
    if (url === METRICS) return metrics()
    if (url === '/admin/settings/ui') return { data: { cron_traffic_pull_minutes: 2 } }
    if (url === '/admin/servers') return { data: serverList([THREE_XUI]) }
    throw new Error(`unexpected GET ${url}`)
  })
}

const metricReads = () => api.get.mock.calls.filter(([url]) => url === METRICS).length

function mount() {
  render(
    <ThemeProvider theme={theme}>
      <QueryClientProvider client={makeTestQueryClient()}>
        <MemoryRouter><DiagnosticsView /></MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(() => {
  cleanup()
  useAuthStore.setState({ role: '' })
})

it('shows progress while the first reading is on its way', async () => {
  const gate = deferred<{ data: unknown }>()
  reads(() => gate.promise)
  mount()
  expect(await screen.findByRole('progressbar')).toBeTruthy()
  expect(screen.queryByTestId('diag-status')).toBeNull()
  gate.resolve({ data: productionSnapshot() })
  expect(await screen.findByTestId('diag-status')).toBeTruthy()
})

it('shows Refresh as busy while a refetch is in flight, and does not fetch twice on a second click', async () => {
  reads()
  mount()
  await screen.findByTestId('diag-status')
  const gate = deferred<{ data: unknown }>()
  reads(() => gate.promise)
  const before = metricReads()

  const button = screen.getByRole('button', { name: 'admin:diagnostics.actions.refresh' })
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(within(button).getByRole('progressbar')).toBeTruthy()
  expect(metricReads()).toBe(before + 1)

  gate.resolve({ data: productionSnapshot() })
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
})

it('disables the clear action while a clear is in flight', async () => {
  reads()
  mount()
  await screen.findByTestId('diag-status')
  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)

  fireEvent.click(screen.getByRole('button', { name: 'admin:diagnostics.actions.more' }))
  fireEvent.click(within(screen.getByRole('menu')).getByRole('menuitem', { name: /admin:diagnostics\.actions\.reset/ }))
  await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))

  fireEvent.click(screen.getByRole('button', { name: 'admin:diagnostics.actions.more' }))
  const item = within(screen.getByRole('menu')).getByRole('menuitem', { name: /admin:diagnostics\.actions\.reset/ })
  expect(item.getAttribute('aria-disabled')).toBe('true')
  expect(item.getAttribute('aria-busy')).toBe('true')
  fireEvent.click(item)
  expect(api.post).toHaveBeenCalledTimes(1)

  gate.resolve({ data: { reset: true, previous: productionSnapshot().metrics } })
  await waitFor(() => expect(item.getAttribute('aria-busy')).toBeNull())
})
