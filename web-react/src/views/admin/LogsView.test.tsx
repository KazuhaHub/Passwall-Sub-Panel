// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, useLocation } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
// t over the REAL zh-CN admin bundle (then the defaultValue, then the key),
// so a key the page asks for but the bundle lacks shows up as its raw key.
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
      const raw = dict.current[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN' },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import { flatten, type Nested } from '@/i18n/options'
dict.current = flatten(zh as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function Where() {
  const loc = useLocation()
  return <p data-testid="location">{loc.pathname + loc.search}</p>
}

function mount(url = '/admin/logs') {
  render(
    <MemoryRouter initialEntries={[url]}>
      <ThemeProvider theme={theme}>
        <LogsView />
        <Where />
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

// The device column carries what only an admin may read: the sub-log API adds
// the device fields for admins only, so an operator offered the column would
// see it always empty. (The location and risk tabs that used to sit here moved
// to the admin-only risk center; LogsRoute.test covers their old links.)
describe('LogsView admin-only device column', () => {
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
      throw new Error(`Unexpected GET ${url}`)
    })
  }

  afterEach(() => useAuthStore.setState({ role: '' }))

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

// The risk center's drawer links here with the account's id (exact) and its
// UPN (display only): `q` would be a fuzzy search over user, IP, UA and
// client, and would match other accounts whose names contain it.
describe('LogsView exact user filter', () => {
  const list = (items: unknown[]) => ({ items, total: items.length })

  function serve() {
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/sub-logs' || url === '/admin/audit' || url === '/admin/auth-events'
        || url === '/admin/email-logs') return { data: list([]) }
      if (url === '/admin/settings/ui') return { data: {} }
      throw new Error(`Unexpected GET ${url}`)
    })
  }

  const lastParams = (url: string) =>
    [...api.get.mock.calls].reverse().find(([u]) => u === url)?.[1]?.params as Record<string, unknown> | undefined

  beforeEach(() => useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true }))
  afterEach(() => useAuthStore.setState({ role: '' }))

  it('filters the sub log by user_id behind a removable chip', async () => {
    serve()
    mount('/admin/logs?tab=sub&user_id=7&upn=alice')

    const chip = (await screen.findByText('用户：alice')).closest('.MuiChip-root') as HTMLElement
    await waitFor(() => expect(lastParams('/admin/sub-logs')).toMatchObject({ user_id: 7 }))

    fireEvent.click(within(chip).getByTestId('CancelIcon'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/logs?tab=sub'))
    expect(screen.queryByText('用户：alice')).toBeNull()
    await waitFor(() => expect(lastParams('/admin/sub-logs')?.user_id).toBeUndefined())
  })

  it('filters the auth log the same way', async () => {
    serve()
    mount('/admin/logs?tab=auth&user_id=7&upn=alice')

    expect(await screen.findByText('用户：alice')).toBeTruthy()
    await waitFor(() => expect(lastParams('/admin/auth-events')).toMatchObject({ user_id: 7 }))
  })

  it('ignores a malformed id', async () => {
    serve()
    mount('/admin/logs?tab=sub&user_id=7x&upn=alice')

    await waitFor(() => expect(lastParams('/admin/sub-logs')).toBeDefined())
    expect(lastParams('/admin/sub-logs')?.user_id).toBeUndefined()
    expect(screen.queryByText('用户：alice')).toBeNull()
  })
})
