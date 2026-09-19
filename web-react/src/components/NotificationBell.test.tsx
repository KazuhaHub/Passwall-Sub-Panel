// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import NotificationBell from './NotificationBell'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

// Render the default value so assertions read as the copy a user would see.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'en-US' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  const client = makeTestQueryClient()
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <NotificationBell />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(client) },
  )
  return client
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})

afterEach(cleanup)

describe('NotificationBell', () => {
  it('says notifications are unavailable when the first load fails, not that there are none', async () => {
    // "No notifications" is a claim about the server; a failed request is not
    // evidence for it. Reporting an empty success here would hide an outage.
    api.get.mockRejectedValue(new Error('network down'))
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.getByText('通知暂时不可用')).toBeTruthy())
    expect(screen.queryByText('暂无通知')).toBeNull()
  })

  it('reports an empty feed as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { alerts: [], counts: { error: 0, warning: 0, info: 0 } } })
    mount()

    fireEvent.click(await screen.findByLabelText('notifications'))

    await waitFor(() => expect(screen.getByText('暂无通知')).toBeTruthy())
    expect(screen.queryByText('通知暂时不可用')).toBeNull()
  })
})
