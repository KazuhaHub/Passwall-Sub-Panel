// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserActivity } from './UserActivity'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'en-US' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount(userId = 2) {
  render(
    <ThemeProvider theme={theme}>
      <UserActivity userId={userId} />
    </ThemeProvider>,
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

    await waitFor(() => expect(screen.getByText('暂时无法获取登录记录')).toBeTruthy())
    expect(screen.queryByText('暂无登录记录')).toBeNull()
  })

  it('reports an empty history as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [], total: 0 } })
    mount()

    await waitFor(() => expect(screen.getByText('暂无登录记录')).toBeTruthy())
    expect(screen.queryByText('暂时无法获取登录记录')).toBeNull()
  })

  it('renders the sign-ins it receives', async () => {
    api.get.mockResolvedValue({
      data: {
        items: [{ id: 1, upn: 'u@x.test', method: 'local', outcome: 'success', ip: '10.0.0.1', at: '2026-09-18T10:00:00Z' }],
        total: 1,
      },
    })
    mount()

    await waitFor(() => expect(screen.getByText('成功')).toBeTruthy())
    expect(screen.getByText(/10\.0\.0\.1/)).toBeTruthy()
  })
})
