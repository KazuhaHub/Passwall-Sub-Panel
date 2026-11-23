// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserActivity } from './UserActivity'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// The real English strings, so an assertion reads what a person reads and a missing key shows as a raw key.
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)


function mount(userId = 2) {
  render(
    <UserActivity userId={userId} />,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('UserActivity', () => {
  it('reports a failed load as unavailable instead of claiming there were no sign-ins', async () => {
    // "No sign-in records" is a claim about the account. A failed request is not
    // evidence for it — presenting an error as an empty history hides a problem
    // the admin is specifically looking here to find.
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('Sign-in records are temporarily unavailable')).toBeTruthy())
    expect(screen.queryByText('No sign-in records')).toBeNull()
  })

  it('reports an empty history as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [], total: 0 } })
    mount()

    await waitFor(() => expect(screen.getByText('No sign-in records')).toBeTruthy())
    expect(screen.queryByText('Sign-in records are temporarily unavailable')).toBeNull()
  })

  it('renders the sign-ins it receives', async () => {
    api.get.mockResolvedValue({
      data: {
        items: [{ id: 1, upn: 'u@x.test', method: 'local', outcome: 'success', ip: '10.0.0.1', at: '2026-09-18T10:00:00Z' }],
        total: 1,
      },
    })
    mount()

    await waitFor(() => expect(screen.getByText('OK')).toBeTruthy())
    expect(screen.getByText(/10\.0\.0\.1/)).toBeTruthy()
  })
})
