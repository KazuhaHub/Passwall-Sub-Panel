// @vitest-environment jsdom
import { StrictMode, type ReactElement } from 'react'
import { MemoryRouter } from 'react-router'
import { ThemeProvider } from '@mui/material/styles'
import { render, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { queryWrapper, makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads } from '@/test/adminSaveHarness'
import MeView from './MeView'

const confirmMock = vi.hoisted(() => vi.fn(async () => true))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount(ui: ReactElement) {
  return render(
    <StrictMode>
      <MemoryRouter>
        <ThemeProvider theme={theme}>{ui}</ThemeProvider>
      </MemoryRouter>
    </StrictMode>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const profile = {
  id: 1,
  upn: 'user@example.test',
  display_name: 'User',
  role: 'user',
  enabled: true,
  can_change_password: true,
  can_edit_personal_rules: true,
  sub_url: 'https://sub.example.test/token',
  uuid: 'uuid-1',
  traffic_limit_bytes: 10 * 1024 * 1024 * 1024, // 10 GB
  traffic_reset_period: 'monthly',
  expire_at: null,
}

const emptyHistory = { points: [] }

describe('usage panel while the usage read is still in flight', () => {
  it('shows a loading state instead of 0.00/X GB, 0% and today 0', async () => {
    const pendingUsage = deferred<{ data: { user_id: number; permanent_total_bytes: number; period_used_bytes: number; today_used_bytes: number } }>()
    installReads({
      '/user/me': profile,
      '/user/me/traffic/history': emptyHistory,
    })
    api.get.mockImplementation(async (url: string) => {
      if (url === '/user/me') return { data: profile }
      if (url === '/user/me/traffic/history') return { data: emptyHistory }
      if (url === '/user/me/traffic') return pendingUsage.promise
      throw new Error(`Unexpected GET ${url}`)
    })

    mount(<MeView />)

    // The profile (which gates the page-level spinner) loads fast; the usage
    // card must not lie about a zero reading while ITS OWN request is still
    // in flight — that's a separate, slower query.
    await screen.findByText('user:profile.usage_section')
    expect(screen.queryByText('0.00 / 10 GB')).toBeNull()
    expect(screen.queryByText('0.0%')).toBeNull()
    // Both the period line and the "today" figure switch to the loading
    // string while usageLoading is true.
    expect(screen.getAllByText('common:status.loading').length).toBeGreaterThan(0)

    pendingUsage.resolve({
      data: { user_id: 1, permanent_total_bytes: 0, period_used_bytes: 2 * 1024 * 1024 * 1024, today_used_bytes: 5 * 1024 * 1024 },
    })

    await waitFor(() => expect(screen.getByText('2.00 / 10 GB')).toBeTruthy())
    expect(screen.getByText('20.0%')).toBeTruthy()
    expect(screen.getByText('5.00 MB')).toBeTruthy()
    expect(screen.queryByText('common:status.loading')).toBeNull()
  })
})

describe('reset credentials button', () => {
  it('stays busy until the reset settles, and ignores a second click', async () => {
    installReads({
      '/user/me': profile,
      '/user/me/traffic': { user_id: 1, permanent_total_bytes: 0, period_used_bytes: 0, today_used_bytes: 0 },
      '/user/me/traffic/history': emptyHistory,
    })
    const pending = deferred<{ data: { sub_token: string; sub_url: string; uuid: string } }>()
    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/reset-credentials') return pending.promise
      throw new Error(`Unexpected POST ${url}`)
    })

    mount(<MeView />)

    const button = await screen.findByRole('button', { name: 'user:sub.reset' })
    fireEvent.click(button)

    // Busy immediately — before the confirm dialog's own promise, let alone
    // the reset request, has had a chance to settle.
    expect((button as HTMLButtonElement).disabled).toBe(true)
    expect(button.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/reset-credentials'))
    // A second click while still busy must not re-open the confirm dialog or
    // fire a second reset (which the finding calls out as "double reset").
    fireEvent.click(button)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(within(button).getByRole('progressbar')).toBeTruthy()

    pending.resolve({ data: { sub_token: 'tok2', sub_url: 'https://sub.example.test/new', uuid: 'uuid-2' } })
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
    expect(button.getAttribute('aria-busy')).toBeNull()
  })
})
