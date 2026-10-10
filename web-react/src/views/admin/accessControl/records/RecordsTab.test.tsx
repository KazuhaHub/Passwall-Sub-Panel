/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import AppRouter from '@/router/AppRouter'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import type { DestinationHitsPage } from '@/api/destinationHits'
import AccessControlView from '../AccessControlView'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.records.'
const hour = Date.parse('2026-10-09T09:00:00Z')
const page: DestinationHitsPage = {
  group_by: 'none', page: 1, page_size: 50, total: 1,
  items: [{ hour, user_id: 13, user_upn: 'alice@example.test', panel_id: 1, panel_name: 'Tokyo',
    source: 'p12', source_name: null, action: 'block', dest: 'smtp.example.test', port: 587, count: 7, first_at: hour + 180000, last_at: hour + 300000 }],
  summary: { block: 7, deny: 9, observe: 3, users: 2 }, sources: [{ source: 'p12', name: null }],
  dropped_in_range: 2, losses: { rows: 2, events: 3, unmatched: 4, scope: 'panel', complete: false },
}
function mount(initial = '/admin/access-control?tab=records') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <AccessControlView /> }], { initialEntries: [initial] })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return router
}
const hitsCalls = () => api.get.mock.calls.filter(([url]) => url === '/admin/dest/hits')
beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  useSiteStore.setState({ timezone: 'Asia/Kathmandu' })
  api.get.mockImplementation(async (url: string, config?: { params?: { group_by?: string; page?: number; page_size?: number } }) => {
    if (url === '/admin/dest/hits') return { data: { ...page, page: config?.params?.page ?? 1, page_size: config?.params?.page_size ?? 50,
      group_by: config?.params?.group_by,
      items: (config?.params?.page ?? 1) > 1 ? [] : config?.params?.group_by === 'none' ? page.items : [{ key: 'example.test', name: null, count: 7, user_count: 2, source_count: 1, last_at: hour + 300000 }] } }
    if (url.endsWith('/policies')) return { data: destinationPolicies() }
    if (url.endsWith('/status')) return { data: destinationStatus() }
    if (url.endsWith('/settings')) return { data: { effective: { dest_hit_retention_days: 30 } } }
    if (url === '/admin/dest/users/13') return { data: { group: null, exemption: null, hits_available: false, recent_hits: { days: 7, items: [], losses: { rows: 0, events: 0, unmatched: 0, scope: 'panel', complete: false } }, usage_available: null, usage_nodes: null } }
    if (url.endsWith('/users')) return { data: { items: [], total: 0 } }
    return { data: { items: [] } }
  })
})
afterEach(() => { cleanup(); useSiteStore.setState({ timezone: '' }) })

it('mounts shared record links and renders stored actions, deleted labels and the actual panel hour', async () => {
  mount('/admin/access-control?tab=records&rec_user=13&rec_panel=1&rec_source=p12')
  await screen.findByText('smtp.example.test:587')
  expect(screen.getByRole('tab', { name: `${P}title`, selected: true })).toBeTruthy()
  expect(screen.getByText('14:45–15:45')).toBeTruthy()
  expect(screen.getByText(`${P}deleted_policy`)).toBeTruthy()
  expect(screen.getByText(`${P}loss_notice`)).toBeTruthy()
  expect(screen.getByText(`${P}loss_rows`)).toBeTruthy()
  expect(screen.getByText(`${P}loss_events`)).toBeTruthy()
  expect(screen.getByText(`${P}loss_unmatched`)).toBeTruthy()
  expect(hitsCalls()[0][1].params).toMatchObject({ user_id: 13, panel_id: 1, source: 'p12', page: 1, page_size: 50, group_by: 'none' })
  expect(screen.queryByRole('button', { name: new RegExp(`${P}deny`) })).toBeNull()
  expect(screen.queryByRole('checkbox', { name: `${P}include_trial` })).toBeNull()
})

it('filters metric cards, resets pages and keeps the search out of shared links', async () => {
  const router = mount('/admin/access-control?tab=records&rec_page=4')
  await screen.findByText(`${P}page_empty`)
  const metrics = within(screen.getByRole('group', { name: `${P}metrics` }))
  fireEvent.click(metrics.getByRole('button', { name: new RegExp(`${P}block`) }))
  await waitFor(() => expect(hitsCalls().at(-1)?.[1].params).toMatchObject({ action: 'block', source_kind: 'policy', page: 1 }))
  expect(router.state.location.search).not.toContain('rec_page')
  expect(metrics.getByRole('button', { name: new RegExp(`${P}block`) }).getAttribute('aria-pressed')).toBe('true')
  const search = screen.getByRole('textbox', { name: `${P}search` })
  fireEvent.change(search, { target: { value: ' private%_host.test ' } })
  await waitFor(() => expect(hitsCalls().at(-1)?.[1].params.q).toBe('private%_host.test'))
  expect(router.state.location.search).not.toContain('private')
  expect(router.state.location.search).not.toContain('rec_q')
  expect(screen.getByRole('button', { name: new RegExp(`${P}observe`) }).textContent).toContain('3')
})

it('reads grouped data and opens the destination tester while preserving record filters', async () => {
  const router = mount('/admin/access-control?tab=records&rec_source=p12')
  await screen.findByText('smtp.example.test:587')
  fireEvent.click(screen.getByRole('button', { name: `${P}group_site` }))
  await screen.findByText('example.test')
  expect(hitsCalls().at(-1)?.[1].params.group_by).toBe('site')
  expect(router.state.location.search).toContain('rec_group_by=site')
  fireEvent.click(screen.getByRole('button', { name: `${P}group_none` }))
  await screen.findByText('smtp.example.test:587')
  fireEvent.click(screen.getByRole('button', { name: `${P}row_menu` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}test` }))
  await waitFor(() => expect(router.state.location.search).toContain('sheet=test'))
  expect(router.state.location.search).toContain('rec_source=p12')
  expect(router.state.location.state.prefill).toEqual({ target: 'smtp.example.test' })
})

it('rejects an oversized custom range without querying a fallback window', async () => {
  mount('/admin/access-control?tab=records&rec_since=2026-01-01T00%3A00&rec_until=2026-03-01T00%3A00')
  await screen.findByText(`${P}invalid_range`)
  expect(hitsCalls()).toHaveLength(0)
})

it('shows a failed read explicitly and retries without presenting complete zero counters', async () => {
  const original = api.get.getMockImplementation()!
  let failed = true
  api.get.mockImplementation((url: string, config?: unknown) => url === '/admin/dest/hits' && failed ? Promise.reject(new Error('unavailable')) : original(url, config))
  mount()
  const error = await screen.findByRole('alert', { name: `${P}failed` })
  failed = false
  fireEvent.click(within(error).getByRole('button', { name: 'common:actions.retry' }))
  await screen.findByText('smtp.example.test:587')
  expect(hitsCalls()).toHaveLength(2)
})

it('does not read records on other tabs and cancels a pending records read on departure', async () => {
  const router = mount('/admin/access-control?tab=policies')
  await screen.findByRole('tab', { name: `${P}title` })
  expect(hitsCalls()).toHaveLength(0)
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, config?: unknown) => url === '/admin/dest/hits' ? new Promise(() => {}) : original(url, config))
  fireEvent.click(screen.getByRole('tab', { name: `${P}title` }))
  await waitFor(() => expect(hitsCalls()).toHaveLength(1))
  const signal = hitsCalls()[0][1].signal as AbortSignal
  await act(async () => { await router.navigate('/admin/access-control?tab=policies') })
  expect(signal.aborted).toBe(true)
})

it('keeps incomplete custom input editable without issuing a default-range request', async () => {
  const router = mount('/admin/access-control?tab=records&rec_since=2026-10-08T00%3A00&rec_until=2026-10-09T00%3A00')
  await screen.findByText('smtp.example.test:587')
  const since = screen.getByLabelText(`${P}since`) as HTMLInputElement
  fireEvent.change(since, { target: { value: '' } })
  await screen.findByText(`${P}invalid_range`)
  expect(since.value).toBe('')
  expect(hitsCalls()).toHaveLength(1)
  expect(router.state.location.search).toContain('rec_since=2026-10-08')
  fireEvent.change(since, { target: { value: '2026-10-07T00:00' } })
  await waitFor(() => expect(hitsCalls()).toHaveLength(2))
  expect(hitsCalls().at(-1)?.[1].params.since).toBe(new Date('2026-10-07T00:00').getTime())
})

it('creates an allow exception through the existing transactional flow without rebuilding lists', async () => {
  api.post.mockResolvedValue({ data: { entry: 'domain:example.test', list_id: 4, policy_id: 5, created: { list_id: 4, policy_id: 5 } } })
  mount()
  await screen.findByText('smtp.example.test:587')
  fireEvent.click(screen.getByRole('button', { name: `${P}row_menu` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}add_exception` }))
  const dialog = await screen.findByRole('dialog', { name: 'admin:access_control.exception.title' })
  expect(within(dialog).getByText('admin:access_control.exception.first_use')).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:access_control.exception.save' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/exceptions', { target: 'smtp.example.test', match: 'site', scope: 'global' }, { _skipErrorToast: true }))
  expect(api.put).not.toHaveBeenCalled()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('opens and focuses hit retention without changing the record filters', async () => {
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, config?: unknown) => url.endsWith('/settings') ? Promise.resolve({ data: { settings: {}, defaults: {}, effective: { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 } } }) : original(url, config))
  const router = mount('/admin/access-control?tab=records&rec_source=p12')
  await screen.findByText('smtp.example.test:587')
  fireEvent.click(screen.getByRole('button', { name: `${P}modify_retention` }))
  const dialog = await screen.findByRole('dialog', { name: 'admin:access_control.settings.title' })
  const retention = await within(dialog).findByRole('textbox', { name: 'admin:access_control.settings.dest_hit_retention_days' })
  await waitFor(() => expect(document.activeElement).toBe(retention))
  expect(router.state.location.search).toContain('rec_source=p12')
  expect(router.state.location.search).toContain('sheet=settings')
  expect(api.put).not.toHaveBeenCalled()
})

it('opens the in-page account drawer and returns to the same records without re-reading them', async () => {
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, config?: unknown) => url === '/admin/risk-center/users/13' ? Promise.resolve({ data: {
    user: { id: 13, upn: 'alice@example.test', display_name: '', role: 'user', enabled: true, group_id: 0, group_name: '', traffic_limit_bytes: 0 },
    attention: [], review: { dismissed: false, trusted: false, escalated: [] }, geo: null, signals: [], live: {}, devices: [], device_window_hours: 24, devices_unavailable: false,
  } }) : original(url, config))
  const router = mount('/admin/access-control?tab=records&rec_source=p12')
  await screen.findByText('smtp.example.test:587')
  fireEvent.click(screen.getByRole('button', { name: 'alice@example.test' }))
  await screen.findByRole('tab', { name: 'admin:risk_center.drawer.tab_access', selected: true })
  expect(router.state.location.search).toContain('user=13')
  expect(router.state.location.search).toContain('rec_source=p12')
  fireEvent.click(screen.getByRole('button', { name: 'admin:risk_center.drawer.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=records&rec_source=p12'))
  expect(hitsCalls()).toHaveLength(1)
})

it('keeps zero observed losses incomplete and does not display unknown loss as a known zero', async () => {
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, config?: unknown) => url === '/admin/dest/hits' ? Promise.resolve({ data: { ...page, total: 0, items: [], dropped_in_range: 0,
    losses: { rows: 0, events: 0, unmatched: 0, scope: 'panel', complete: false } } }) : original(url, config))
  mount()
  await screen.findByText(`${P}empty`)
  expect(screen.getByText(`${P}incomplete`)).toBeTruthy()
  expect(screen.queryByText(`${P}loss_rows`)).toBeNull()
  expect(screen.queryByText(`${P}loss_events`)).toBeNull()
  expect(screen.queryByText(`${P}loss_unmatched`)).toBeNull()
})
