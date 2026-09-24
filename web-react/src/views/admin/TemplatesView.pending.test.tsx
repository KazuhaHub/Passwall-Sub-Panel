// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import TemplatesView from './TemplatesView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))
// The YAML editor pulls in CodeMirror, which is irrelevant here.
vi.mock('@/components/CodeEditor', () => ({
  default: ({ value, onChange }: { value: string; onChange: (v: string) => void }) =>
    <textarea aria-label="code" value={value} onChange={e => onChange(e.target.value)} />,
}))
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
        <TemplatesView />
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

const list = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 25 })
const customTpl = { slug: 'custom-tpl', name: 'custom-template', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }
const seededTpl = { slug: 'default-mihomo', name: 'seeded-template', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }

beforeEach(() => {
  vi.clearAllMocks()
  // The delete/reset icons are canConfig-gated (admin/operator only); the
  // default '' role would hide them and the test would find nothing to click.
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  confirmMock.mockResolvedValue(true)
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/templates') return { data: list([]) }
    if (url === '/admin/rules') return { data: list([]) }
    throw new Error(`Unexpected GET ${url}`)
  })
})
afterEach(cleanup)

describe('TemplatesView pending feedback', () => {
  it('keeps the delete icon busy until the request settles, and ignores a second click', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/templates') return { data: list([customTpl]) }
      if (url === '/admin/rules') return { data: list([]) }
      throw new Error(`Unexpected GET ${url}`)
    })
    const pending = deferred()
    api.delete.mockReturnValue(pending.promise)
    mount()

    const cell = await screen.findByText('custom-template')
    const row = cell.closest('tr')!
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(deleteButton)
    expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
    expect(deleteButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
    fireEvent.click(deleteButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.delete).toHaveBeenCalledTimes(1)
    expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
    expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  })

  it('keeps the reset-to-default icon busy until the request settles, and ignores a second click', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/templates') return { data: list([seededTpl]) }
      if (url === '/admin/rules') return { data: list([]) }
      throw new Error(`Unexpected GET ${url}`)
    })
    const pending = deferred()
    api.post.mockReturnValue(pending.promise)
    mount()

    const cell = await screen.findByText('seeded-template')
    const row = cell.closest('tr')!
    const resetButton = within(row).getByTestId('RestartAltIcon').closest('button')!
    await waitFor(() => expect((resetButton as HTMLButtonElement).disabled).toBe(false))

    fireEvent.click(resetButton)
    expect((resetButton as HTMLButtonElement).disabled).toBe(true)
    expect(resetButton.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    fireEvent.click(resetButton)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(within(resetButton).getByRole('progressbar')).toBeTruthy()

    pending.resolve()
    await waitFor(() => expect((resetButton as HTMLButtonElement).disabled).toBe(false))
    expect(resetButton.getAttribute('aria-busy')).toBeNull()
  })
})
