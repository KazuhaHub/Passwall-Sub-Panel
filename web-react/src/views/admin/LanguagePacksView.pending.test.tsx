// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import LanguagePacksView from './LanguagePacksView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
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
        <LanguagePacksView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(res => { resolve = res })
  return { promise, resolve }
}

const pack = { code: 'fr-FR', name: 'Français', author: 'someone', base_version: '' }

beforeEach(() => {
  vi.clearAllMocks()
  // The delete icon is canConfig-gated (admin/operator only); the default ''
  // role would hide it and the test would find nothing to click.
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  confirmMock.mockResolvedValue(true)
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/locales') return { data: [pack] }
    if (url === '/version') return { data: { version: 'dev', commit: '', build_date: '' } }
    throw new Error(`Unexpected GET ${url}`)
  })
})
afterEach(cleanup)

describe('LanguagePacksView pending feedback', () => {
  it('keeps the delete icon busy until the request settles, and ignores a second click', async () => {
    const pending = deferred()
    api.delete.mockReturnValue(pending.promise)
    mount()

    const cell = await screen.findByText('Français')
    const row = cell.closest('tr')!
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(deleteButton)
    // Busy immediately — before the confirm dialog's own promise, let alone
    // the delete request, has had a chance to settle.
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(deleteButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    // A second click while the row is still busy must not re-open the confirm
    // dialog or fire a second delete.
    fireEvent.click(deleteButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.delete).toHaveBeenCalledTimes(1)
    expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
    expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  })
})
