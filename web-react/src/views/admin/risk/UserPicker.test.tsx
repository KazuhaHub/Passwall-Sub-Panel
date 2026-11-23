/** @vitest-environment jsdom */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import { UserPicker } from './UserPicker'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k: string) => k, i18n: { language: 'zh-CN' } }),
}))

const alice = { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 1 }

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

const keywords = () => api.get.mock.calls
  .filter(([u]) => u === '/admin/users')
  .map(([, cfg]) => String((cfg as { params?: { keyword?: string } }).params?.keyword ?? ''))

describe('UserPicker', () => {
  // The user list is a paged server search: one request per pause in typing, not one per key.
  it('asks the server once for the settled keyword', async () => {
    render(<UserPicker value={null} onChange={vi.fn()} />, { wrapper: queryWrapper(makeTestQueryClient()) })
    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'a' } })
    fireEvent.change(input, { target: { value: 'al' } })
    fireEvent.change(input, { target: { value: 'ali' } })
    await waitFor(() => expect(keywords()).toContain('ali'))
    expect(keywords()).not.toContain('a')
    expect(keywords()).not.toContain('al')
  })

  it('reports the id of the account picked, and names a picked account found by id', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<UserPicker value={null} onChange={onChange} />, { wrapper: queryWrapper(makeTestQueryClient()) })
    await waitFor(() => expect(keywords().length).toBeGreaterThan(0))
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: 'Alice (alice)' }))
    expect(onChange).toHaveBeenCalledWith(7)
    rerender(<UserPicker value={7} onChange={onChange} />)
    await waitFor(() => expect((screen.getByRole('combobox') as HTMLInputElement).value).toBe('Alice (alice)'))
  })
})
