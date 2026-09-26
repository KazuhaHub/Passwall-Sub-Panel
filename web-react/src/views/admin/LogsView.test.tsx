// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
import LogsView from './LogsView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
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
        <LogsView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('LogsView', () => {
  it('reports a failed log read instead of rendering an empty log', async () => {
    // "No log entries" is a claim about the audit trail. The loaders ran with no
    // catch, so a failure raised an unhandled rejection and left the table
    // reading as an account with a clean history.
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载日志')).toBeTruthy())
  })
})

// The risk tab and the device column carry what only an admin may read: the
// risk endpoint is adminGroup, and the sub-log API adds the device fields for
// admins only. An operator offered either would see a tab that can only 403,
// or a column that is always empty.
describe('LogsView admin-only risk views', () => {
  const list = (items: unknown[]) => ({ items, total: items.length })
  const subLogs = [
    { id: 1, user_id: 7, user_upn: 'alice', ip: '203.0.113.7', ua: 'ClashMeta/1.0', client_type: 'mihomo',
      accessed_at: '2026-09-20T00:00:00Z', device_label: 'iOS 17.5 · iPhone15,2', device_id4: 'ab12' },
    { id: 2, user_id: 7, user_upn: 'alice', ip: '203.0.113.8', ua: 'v2rayN/7', client_type: 'uri-list',
      accessed_at: '2026-09-20T00:01:00Z', device_id4: 'cd34' },
    { id: 3, user_id: 7, user_upn: 'alice', ip: '203.0.113.9', ua: 'curl/8', client_type: 'uri-list',
      accessed_at: '2026-09-20T00:02:00Z' },
  ]

  function serve() {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/sub-logs') return { data: list(subLogs) }
      if (url === '/admin/audit' || url === '/admin/auth-events' || url === '/admin/email-logs') return { data: list([]) }
      if (url === '/admin/settings/ui') return { data: {} }
      if (url === '/admin/risk-signals') return { data: { items: [] } }
      throw new Error(`Unexpected GET ${url}`)
    })
  }

  afterEach(() => useAuthStore.setState({ role: '' }))

  it('offers the risk tab to admins only', async () => {
    serve()
    useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
    mount()

    fireEvent.click(await screen.findByRole('tab', { name: '风险信号' }))
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/risk-signals', expect.anything()))
    cleanup()

    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mount()
    await screen.findByText('curl/8')
    expect(screen.queryByRole('tab', { name: '风险信号' })).toBeNull()
  })

  it('shows the declared device to admins only', async () => {
    serve()
    useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
    mount()

    await screen.findByText('iOS 17.5 · iPhone15,2')
    expect(screen.queryByRole('columnheader', { name: '设备' })).not.toBeNull()
    // Only the 4-character prefix of the digest reaches the page, and a
    // fetch that declared nothing says so rather than leaving a blank.
    expect(screen.getByText('#cd34')).toBeTruthy()
    const row3 = screen.getByText('curl/8').closest('tr') as HTMLElement
    expect(row3.querySelectorAll('td')[5]?.textContent).toBe('—')
    cleanup()

    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mount()
    await screen.findByText('curl/8')
    expect(screen.queryByRole('columnheader', { name: '设备' })).toBeNull()
    expect(screen.queryByText('iOS 17.5 · iPhone15,2')).toBeNull()
    expect(screen.queryByText('#cd34')).toBeNull()
  })
})
