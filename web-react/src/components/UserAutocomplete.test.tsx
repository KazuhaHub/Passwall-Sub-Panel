/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import UserAutocomplete from './UserAutocomplete'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

const alice = { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1 }

function keywords(): string[] {
  return api.get.mock.calls
    .filter(([u]) => u === '/admin/users')
    .map(([, cfg]) => String((cfg as { params?: { keyword?: string } }).params?.keyword ?? ''))
}

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  api.get.mockImplementation(async (url: string) => {
    if (url === '/admin/users') return { data: { items: [alice], total: 1, page: 1, page_size: 50 } }
    if (url === '/admin/users/7') return { data: alice }
    throw new Error(`unexpected GET ${url}`)
  })
})
afterEach(cleanup)

describe('UserAutocomplete', () => {
  // The user list is a paged server search. Asking it on every keystroke
  // would send one request per character typed; the keyword waits until the
  // typing pauses, and only the settled one is asked.
  it('debounces the keyword', async () => {
    const onChange = vi.fn()
    render(
      <ThemeProvider theme={theme}><UserAutocomplete value={null} onChange={onChange} label="user" /></ThemeProvider>,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'a' } })
    fireEvent.change(input, { target: { value: 'al' } })
    fireEvent.change(input, { target: { value: 'ali' } })

    await waitFor(() => expect(keywords()).toContain('ali'))
    expect(keywords()).not.toContain('a')
    expect(keywords()).not.toContain('al')
    const req = api.get.mock.calls.find(([u, cfg]) =>
      u === '/admin/users' && (cfg as { params: { keyword?: string } }).params.keyword === 'ali')
    expect(req?.[1]).toMatchObject({ params: { page: 1, page_size: 50, keyword: 'ali' } })
  })

  it('picks a user by id', async () => {
    const onChange = vi.fn()
    render(
      <ThemeProvider theme={theme}><UserAutocomplete value={null} onChange={onChange} label="user" /></ThemeProvider>,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    fireEvent.mouseDown(screen.getByRole('combobox'))
    fireEvent.click(await screen.findByRole('option', { name: 'Alice (alice)' }))
    expect(onChange).toHaveBeenCalledWith(7)
  })

  // A selected id that is not on the current page of results (a deep link, a
  // row opened from another tab) still shows who it is.
  it('names a selected user that the search did not return', async () => {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/users') return { data: { items: [], total: 0, page: 1, page_size: 50 } }
      if (url === '/admin/users/7') return { data: alice }
      throw new Error(`unexpected GET ${url}`)
    })
    render(
      <ThemeProvider theme={theme}><UserAutocomplete value={7} onChange={vi.fn()} label="user" /></ThemeProvider>,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    await waitFor(() => expect((screen.getByRole('combobox') as HTMLInputElement).value).toBe('Alice (alice)'))
  })
})
