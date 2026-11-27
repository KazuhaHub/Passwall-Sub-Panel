/** @vitest-environment jsdom */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import UserAutocomplete from './UserAutocomplete'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// The real English strings, so an assertion reads what a person reads and a missing key shows as a raw key.
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

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
      <UserAutocomplete value={null} onChange={onChange} label="user" />,
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

  // A fixed width wider than the container used to spill over the next field (the access-control
  // records filters, where the grid column was narrower than 240px). The width is a ceiling now.
  it('never grows wider than its container', () => {
    const { container } = render(
      <UserAutocomplete value={null} onChange={vi.fn()} label="user" width={240} />,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    const box = container.firstElementChild as HTMLElement
    expect(box.style.width).toBe('100%')
    expect(box.style.maxWidth).toBe('240px')
  })

  it('picks a user by id', async () => {
    const onChange = vi.fn()
    render(
      <UserAutocomplete value={null} onChange={onChange} label="user" />,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    const input = screen.getByRole('combobox')
    fireEvent.mouseDown(input)
    fireEvent.change(input, { target: { value: 'al' } })
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
      <UserAutocomplete value={7} onChange={vi.fn()} label="user" />,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    await waitFor(() => expect((screen.getByRole('combobox') as HTMLInputElement).value).toBe('Alice (alice)'))
  })

  it('fullWidth fills the parent and has no fixed pixel width (a 375px sheet must not overflow)', () => {
    const { container } = render(
      <UserAutocomplete value={null} onChange={() => {}} label="user" fullWidth />,
      { wrapper: queryWrapper(makeTestQueryClient()) },
    )
    // jsdom has no stylesheet, so the contract is checked at its source: the fill-the-parent class and
    // no inline pixel width.
    const root = container.firstElementChild as HTMLElement
    expect(root.className).toContain('w-full')
    expect(root.style.width).toBe('')
  })
})
