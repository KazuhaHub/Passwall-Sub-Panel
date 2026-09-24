// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import LogsView from './LogsView'

// SLOW-NETWORK FEEDBACK: destructive actions (Purge old / Clear all) on a
// 3-10s link gave no visible response for the whole round trip, so a click
// read as dead and invited a second, duplicate destructive request. These
// hold the request open (a promise the test resolves by hand) and assert the
// button goes busy immediately, ignores a second click meanwhile, and comes
// back once the request settles.

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  // Purge/Clear are admin-only (config.write); without a role the capability
  // gate hides them entirely and every lookup below would fail on "not found"
  // rather than on the busy-state gap these tests exist to catch.
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <LogsView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const list = (items: unknown[]) => ({ items, total: items.length })

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

// Every tab read must resolve (empty is fine) so the active tab is never
// stuck on its own loading state while we exercise a different tab's button.
function installDefaultReads() {
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/sub-logs') return { data: list([]) }
    if (url === '/admin/audit') return { data: list([]) }
    if (url === '/admin/auth-events') return { data: list([]) }
    if (url === '/admin/email-logs') return { data: list([]) }
    if (url === '/admin/settings/ui') return { data: {} }
    throw new Error(`Unexpected GET ${url}`)
  })
}

it('shows Purge old (sub logs) as busy while in flight, ignores a second click, and recovers', async () => {
  installDefaultReads()
  mount()
  await waitFor(() => expect(screen.queryByRole('progressbar')).toBeNull())

  const gate = deferred<{ data: { deleted: number } }>()
  api.post.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:logs.purge_old' }) as HTMLButtonElement
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect(button.disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(api.post.mock.calls.length).toBe(1)
  expect(api.post).toHaveBeenCalledWith('/admin/sub-logs/purge')

  gate.resolve({ data: { deleted: 0 } })
  await waitFor(() => expect(button.disabled).toBe(false))
  expect(button.getAttribute('aria-busy')).toBeNull()
})

it('shows Clear all (sub logs) as busy while in flight, ignores a second click, and recovers', async () => {
  installDefaultReads()
  mount()
  await waitFor(() => expect(screen.queryByRole('progressbar')).toBeNull())

  const gate = deferred<{ data: unknown }>()
  api.delete.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:logs.clear_all' }) as HTMLButtonElement
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect(button.disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(api.delete.mock.calls.length).toBe(1)
  expect(api.delete).toHaveBeenCalledWith('/admin/sub-logs')

  gate.resolve({ data: {} })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('shows Clear all (audit) as busy while in flight, ignores a second click, and recovers', async () => {
  installDefaultReads()
  mount()
  await waitFor(() => expect(screen.queryByRole('progressbar')).toBeNull())
  fireEvent.click(screen.getByRole('tab', { name: 'admin:logs.tab_audit' }))
  await screen.findByRole('button', { name: 'common:search.placeholder' })

  const gate = deferred<{ data: unknown }>()
  api.delete.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:logs.clear_all' }) as HTMLButtonElement
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect(button.disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(api.delete.mock.calls.length).toBe(1)
  expect(api.delete).toHaveBeenCalledWith('/admin/audit')

  gate.resolve({ data: {} })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('shows Purge old (email logs) as busy while in flight, ignores a second click, and recovers', async () => {
  installDefaultReads()
  mount()
  await waitFor(() => expect(screen.queryByRole('progressbar')).toBeNull())
  fireEvent.click(screen.getByRole('tab', { name: 'admin:logs.tab_email' }))
  await screen.findByRole('button', { name: 'admin:logs.purge_old' })

  const gate = deferred<{ data: { deleted: number } }>()
  api.post.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:logs.purge_old' }) as HTMLButtonElement
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect(button.disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(api.post.mock.calls.length).toBe(1)
  expect(api.post).toHaveBeenCalledWith('/admin/email-logs/purge')

  gate.resolve({ data: { deleted: 0 } })
  await waitFor(() => expect(button.disabled).toBe(false))
})

it('shows Clear all (email logs) as busy while in flight, ignores a second click, and recovers', async () => {
  installDefaultReads()
  mount()
  await waitFor(() => expect(screen.queryByRole('progressbar')).toBeNull())
  fireEvent.click(screen.getByRole('tab', { name: 'admin:logs.tab_email' }))
  await screen.findByRole('button', { name: 'admin:logs.clear_all' })

  const gate = deferred<{ data: unknown }>()
  api.delete.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:logs.clear_all' }) as HTMLButtonElement
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect(button.disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(api.delete.mock.calls.length).toBe(1)
  expect(api.delete).toHaveBeenCalledWith('/admin/email-logs')

  gate.resolve({ data: {} })
  await waitFor(() => expect(button.disabled).toBe(false))
})
