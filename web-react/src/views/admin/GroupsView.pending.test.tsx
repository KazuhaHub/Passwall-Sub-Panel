// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import GroupsView from './GroupsView'

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
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <GroupsView />
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

const group = {
  id: 1, slug: 'eligible', name: 'Eligible Group',
  tag_filter: { all: true, tags: [], mode: 'all' as const },
  members: 0, remark: '',
}
const groupsPage = { items: [group], total: 1, page: 1, page_size: 200 }
const emptyNodes = { items: [], total: 0, page: 1, page_size: 500 }

beforeEach(() => {
  vi.clearAllMocks()
  // The delete icon only renders for a capability that carries config.write;
  // without this the row action under test wouldn't exist at all.
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
afterEach(cleanup)

describe('GroupsView delete-row feedback', () => {
  // confirmDelete (417-432) awaited deleteGroup + load() with no busy state
  // at all, unlike the batch-delete button beside it — on a slow link the
  // row's delete icon looked dead for 3-10s and invited a second click.
  it('shows a busy delete icon while the delete request is in flight, and a second click does not delete twice', async () => {
    api.get.mockImplementation((url: string) => {
      if (url === '/admin/groups') return Promise.resolve({ data: groupsPage })
      if (url === '/admin/nodes') return Promise.resolve({ data: emptyNodes })
      return Promise.reject(new Error(`unexpected GET ${url}`))
    })
    const del = deferred<void>()
    api.delete.mockImplementation(() => del.promise)

    mount()
    await screen.findByText('Eligible Group')

    const button = screen.getByRole('button', { name: 'admin:groups.action.delete' })
    fireEvent.click(button)

    await waitFor(() => expect(button.getAttribute('aria-busy')).toBe('true'))
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(within(button).getByRole('progressbar')).toBeTruthy()

    // A second click while the delete is still pending must not fire a
    // second DELETE request.
    fireEvent.click(button)
    expect(api.delete).toHaveBeenCalledTimes(1)

    await act(async () => { del.resolve(); await del.promise.catch(() => {}) })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    expect(button.getAttribute('aria-busy')).toBeNull()
  })
})
