/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import AppRouter from '@/router/AppRouter'
import { useAuthStore } from '@/stores/auth'
import { destinationNode, destinationPolicies, destinationStatus, samplePolicy } from '@/test/accessControlFixtures'
const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: Record<string, unknown>) => key + (options?.name ? ` ${options.name}` : ''), i18n: { language: 'en-US' } }) }))
vi.mock('@/components/CodeEditor', () => ({ default: (p: { value: string; onChange: (s: string) => void; ariaLabel: string; readOnly: boolean }) => <textarea aria-label={p.ariaLabel} value={p.value} readOnly={p.readOnly} onChange={e => p.onChange(e.target.value)} /> }))
import AccessControlView from './AccessControlView'
const P = 'admin:access_control.'
const err = (status: number, error: string) => ({ isAxiosError: true, response: { status, data: { error } }, message: error })
function mount(initial = '/admin/access-control') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <AccessControlView /> }, { path: '/admin/dashboard', element: <p>Dashboard</p> }], { initialEntries: [initial] })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return router
}
beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ published_has_access_control: true }) : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items: [] } : url.endsWith('/groups') ? { items: [], total: 0 } : { settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 75 } } }))
  api.put.mockResolvedValue({ data: samplePolicy })
  api.post.mockResolvedValue({ data: { budget: destinationPolicies().budget } })
  confirmation.mockResolvedValue(true)
})
afterEach(cleanup)
it.each(['operator', 'user'] as const)('does not read access APIs for %s', async role => {
  useAuthStore.setState({ role }); mount()
  await screen.findByText('Dashboard')
  expect(api.get).not.toHaveBeenCalled()
})
it('toggles with every policy field and its version without treating the save as applied', async () => {
  mount(); const toggle = await screen.findByRole('switch', { name: `${P}policies.toggle No mail` })
  fireEvent.click(toggle)
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dest/policies/12', expect.objectContaining({ name: 'No mail', action: 'block', enabled: false, list_ids: [], inline: samplePolicy.inline, scope: 'all', group_ids: [], counts_as_risk: true, template_key: '', updated_at: 2000 }), { _skipErrorToast: true }))
  expect(confirmation).not.toHaveBeenCalled()
})
it('keeps a stale editor draft until the user explicitly loads the latest version', async () => {
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.edit No mail` }))
  const name = await screen.findByRole('textbox', { name: `${P}editor.name` })
  fireEvent.change(name, { target: { value: 'My draft' } })
  api.put.mockRejectedValueOnce(err(409, 'dest_policy_stale'))
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  const reload = await screen.findByRole('button', { name: `${P}editor.reload` })
  expect((name as HTMLInputElement).value).toBe('My draft')
  fireEvent.click(reload)
  await waitFor(() => expect((name as HTMLInputElement).value).toBe('No mail'))
})
it('requires first-enable confirmation using published facts and sends no write on cancellation', async () => {
  const disabled = { ...samplePolicy, enabled: false }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ published_has_access_control: false, block: [disabled] }) : url.endsWith('/status') ? destinationStatus() : { effective: { dest_policy_apply_min_seconds: 75 } } }))
  confirmation.mockResolvedValue(false)
  mount(); fireEvent.click(await screen.findByRole('switch', { name: `${P}policies.toggle No mail` }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledWith(expect.objectContaining({ title: `${P}confirm.first_title` })))
  expect(api.put).not.toHaveBeenCalled()
})
it('does not render policy controls when the destination service is unavailable', async () => {
  api.get.mockRejectedValue(err(503, 'dest_unwired')); mount()
  await screen.findByText(`${P}unwired`)
  expect(screen.queryByRole('switch')).toBeNull()
})
it('opens node coverage from an owned URL and closes it with Back', async () => {
  const router = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}coverage.open` }))
  const drawer = await screen.findByRole('dialog', { name: `${P}coverage.title` })
  expect(router.state.location.search).toContain('sheet=nodes')
  fireEvent.click(within(drawer).getByRole('button', { name: new RegExp(`${P}coverage.filter_applied`) }))
  expect(router.state.location.search).toContain('node_state=applied')
  expect(router.state.location.state).toMatchObject({ drawer: 'sheet' })
  fireEvent.click(within(drawer).getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe(''))
})
it('removes owned node filters when closing a cold coverage deep link without changing the tab', async () => {
  const router = mount('/admin/access-control?tab=policies&sheet=nodes&node_state=problem')
  const drawer = await screen.findByRole('dialog', { name: `${P}coverage.title` })
  fireEvent.click(within(drawer).getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=policies'))
})
it('submits every ID in the action segment when moving, including disabled policies', async () => {
  const rows = [samplePolicy, { ...samplePolicy, id: 19, name: 'Disabled', enabled: false }]
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: rows }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu No mail` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}policies.down` }))
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dest/policies/order', { action: 'block', ids: [19,12] }, { _skipErrorToast: true }))
})
it('requires destructive confirmation for deletion and sends no delete on cancellation', async () => {
  confirmation.mockResolvedValue(false)
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu No mail` }))
  fireEvent.click(screen.getByRole('menuitem', { name: 'common:actions.delete' }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledWith(expect.objectContaining({ destructive: true })))
  expect(api.delete).not.toHaveBeenCalled()
})
it('blocks saving a preview that exceeds the server budget', async () => {
  api.post.mockResolvedValue({ data: { budget: { ...destinationPolicies().budget, domains: { used: 50001, limit: 50000 } } } })
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.edit No mail` }))
  const name = await screen.findByRole('textbox', { name: `${P}editor.name` })
  fireEvent.change(name, { target: { value: 'New name' } })
  await waitFor(() => expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(true))
  await screen.findByText(`${P}quota.exceeded`)
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  expect(api.put).not.toHaveBeenCalled()
})
it('keeps save available after a preview read fails and still validates at the write endpoint', async () => {
  api.post.mockRejectedValue(err(500, 'preview_read_failed'))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.edit No mail` }))
  const name = await screen.findByRole('textbox', { name: `${P}editor.name` })
  fireEvent.change(name, { target: { value: 'New name' } })
  await screen.findByText(`${P}editor.preview_failed`)
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(false)
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.put).toHaveBeenCalled())
})
it('keeps a duplicate-name draft and attaches the error to its name field', async () => {
  api.put.mockRejectedValue(err(409, 'dest_name_taken'))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.edit No mail` }))
  const name = await screen.findByRole('textbox', { name: `${P}editor.name` })
  fireEvent.change(name, { target: { value: 'Taken' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(name.getAttribute('aria-invalid')).toBe('true'))
  expect((name as HTMLInputElement).value).toBe('Taken')
})
it('does not announce the publication countdown through the primary live region', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus({ generation: 2, next_publish_at: Date.now() + 10000 }) : { effective: {} } }))
  mount(); const live = await screen.findByRole('status')
  await waitFor(() => expect(live.textContent).toBe(`${P}verdict.unpublished`))
  expect(live.textContent).not.toContain('countdown')
  expect(screen.getByLabelText(`${P}publish_at`)).toBeTruthy()
})
it('does not claim fallback exhaustion is applied until the empty candidate is confirmed', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus({ nodes: [destinationNode({ fallback_exhausted: true, minted_kind: 'empty', pending_since: 3000, applied_at: 2000, minted_at: 3000 })] }) : { effective: {} } }))
  mount('/admin/access-control?sheet=nodes')
  await screen.findByText(`${P}coverage.fallback_stopping`)
  expect(screen.queryByText(`${P}coverage.fallback_exhausted`)).toBeNull()
})
it('reports saved pause state after a failed publication instead of claiming the fleet is paused', async () => {
  api.put.mockRejectedValue({ isAxiosError: true, response: { status: 503, data: { error: 'dest_policy_publish_unavailable', pause_saved: true, paused: true } } })
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}pause` }))
  await waitFor(() => expect(snack).toHaveBeenCalledWith(`${P}pause_saved`, 'error'))
  expect(api.put).toHaveBeenCalledWith('/admin/dest/pause', { paused: true }, { _skipErrorToast: true })
})
it('blocks navigation that would discard an editor draft when the user cancels', async () => {
  confirmation.mockResolvedValue(false)
  const router = mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.edit No mail` }))
  const name = await screen.findByRole('textbox', { name: `${P}editor.name` })
  fireEvent.change(name, { target: { value: 'Unsaved' } })
  await router.navigate('/admin/dashboard')
  await waitFor(() => expect(confirmation).toHaveBeenCalledWith(expect.objectContaining({ title: `${P}confirm.discard_title` })))
  expect(router.state.location.pathname).toBe('/admin/access-control')
  expect((name as HTMLInputElement).value).toBe('Unsaved')
})
it('creates a disabled policy with a full create body and no existing version fields', async () => {
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.create` }))
  fireEvent.change(await screen.findByRole('textbox', { name: `${P}editor.name` }), { target: { value: 'New policy' } })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}editor.ports` }), { target: { value: '443' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/policies', expect.objectContaining({ name: 'New policy', action: 'block', enabled: false, inline: expect.objectContaining({ ports: '443' }), list_ids: [], scope: 'all', group_ids: [] }), { _skipErrorToast: true }))
  const body = api.post.mock.calls.find(call => call[0] === '/admin/dest/policies')![1]
  expect(body).not.toHaveProperty('updated_at'); expect(body).not.toHaveProperty('id')
})
