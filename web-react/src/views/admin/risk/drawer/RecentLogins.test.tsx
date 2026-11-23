/** @vitest-environment jsdom */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import RecentLogins from './RecentLogins'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k: string) => k, i18n: { language: 'zh-CN' } }) }))

beforeEach(() => { vi.clearAllMocks(); useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true }) })
afterEach(cleanup)

describe('RecentLogins', () => {
  // A failed read is not an empty history, and it can be tried again.
  it('offers a retry after a failed read', async () => {
    let fail = true
    api.get.mockImplementation(async () => {
      if (fail) throw new Error('boom')
      return { data: { items: [{ id: 1, outcome: 'success', method: 'local', at: '2026-11-01T00:00:00Z', ip: '203.0.113.5', region: null, reason: '' }], total: 1 } }
    })
    render(<RecentLogins userId={7} />, { wrapper: queryWrapper(makeTestQueryClient()) })
    expect(await screen.findByText('admin:users.activity.unavailable')).toBeTruthy()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'admin:risk_center.retry' }))
    await waitFor(() => expect(screen.getByText(/203\.0\.113\.5/)).toBeTruthy())
  })
})
