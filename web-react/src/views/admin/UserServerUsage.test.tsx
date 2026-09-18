// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { UserServerUsage } from './UserServerUsage'

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
      <UserServerUsage userId={userId} />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('UserServerUsage', () => {
  it('reports a failed load as unavailable instead of claiming the user has no servers', async () => {
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法获取服务器用量')).toBeTruthy())
    expect(screen.queryByText('该用户暂无服务器')).toBeNull()
  })

  it('reports an empty breakdown as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText('该用户暂无服务器')).toBeTruthy())
    expect(screen.queryByText('暂时无法获取服务器用量')).toBeNull()
  })
})
