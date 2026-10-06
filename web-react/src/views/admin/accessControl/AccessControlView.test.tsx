/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import AppRouter from '@/router/AppRouter'
import { useAuthStore } from '@/stores/auth'
import { destinationBudget, destinationNode, destinationPolicies, destinationStatus, samplePolicy } from '@/test/accessControlFixtures'
import type { DestinationListDetail, DestinationListSummary } from '@/api/accessControl'
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
const listReport = { accepted: 1, ignored: 1, ignored_broad: 1, rewritten: 0, samples: [{ line: 1, text: 'domain:hsbc', reason: 'broad_entry' }] }
const listDetail: DestinationListDetail = { id: 7, name: 'Finance', kind: 'geosite', source_url: '', geosite_category: 'category-finance', geosite_attrs: '', owner_group_id: 0, updated_at: 3000, entry_count: 1, regexp_count: 0, state: 'ready', last_fetched_at: 1000, last_error: '', entries: ['domain:bank.example'], content_sha256: 'full-digest', parse_report: listReport }
const listSummary: DestinationListSummary = { ...listDetail, parse_report_summary: listReport, used_by: [] }
function listsAPI(items: DestinationListSummary[]) {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items, budget: destinationBudget, refresh_hours: 12 } : url.endsWith('/lists/7') ? listDetail : { settings: {}, effective: {} } }))
}
it('shows filtered broad entries as a usable category, excluded from the problem filter', async () => {
  listsAPI([listSummary]); mount('/admin/access-control?tab=lists')
  const title = await screen.findAllByText('Finance'); expect(title.length).toBeGreaterThan(0)
  expect(screen.getAllByText(`${P}parse_report.broad_removed`).length).toBeGreaterThan(0)
  const tile = screen.getByRole('button', { name: `${P}lists.problem 0` })
  fireEvent.click(tile)
  await waitFor(() => expect(screen.queryByText('Finance')).toBeNull())
})
it('opens a bounded entry drawer and preserves the list tab when closing its cold link', async () => {
  listsAPI([listSummary]); const router = mount('/admin/access-control?tab=lists&sheet=list&list=7')
  const dialog = await screen.findByRole('dialog', { name: 'Finance' })
  expect(within(dialog).getByText('domain:bank.example')).toBeTruthy()
  expect(within(dialog).getByText('domain:hsbc')).toBeTruthy()
  fireEvent.change(within(dialog).getByRole('textbox', { name: `${P}list_entries.search` }), { target: { value: 'missing' } })
  expect(router.state.location.search).not.toContain('missing')
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists'))
})
it('opens the saved entries after list creation without a competing discard prompt', async () => {
  listsAPI([]); api.post.mockImplementation(async (url: string) => ({ data: url.endsWith('/preview') ? { parse_report: listReport, entries: ['domain:bank.example'], entry_count: 1, content_sha256: 'full-digest' } : { ...listDetail, kind: 'custom' } }))
  const router = mount('/admin/access-control?tab=lists')
  fireEvent.click(await screen.findByRole('button', { name: `${P}lists.create` }))
  fireEvent.change(await screen.findByRole('textbox', { name: `${P}list_editor.name` }), { target: { value: 'New list' } })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}list_editor.text` }), { target: { value: 'bank.example' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists&sheet=list&list=7'))
  expect(confirmation).not.toHaveBeenCalled()
})
it('distinguishes a first failed download from refresh failure retaining old content', async () => {
  listsAPI([{ ...listSummary, state: 'failed', last_fetched_at: null }, { ...listSummary, id: 8, name: 'Older list', state: 'failed', last_fetched_at: 1000 }]); mount('/admin/access-control?tab=lists')
  expect((await screen.findAllByText(`${P}lists.state_failed_first`)).length).toBeGreaterThan(0)
  expect(screen.getAllByText(`${P}lists.state_failed_old`).length).toBeGreaterThan(0)
})
it('keeps the entry drawer and its editor as distinct React children', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
  try {
    listsAPI([listSummary]); mount('/admin/access-control?tab=lists&sheet=list&list=7')
    const dialog = await screen.findByRole('dialog', { name: 'Finance' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.edit' }))
    await screen.findByRole('textbox', { name: `${P}list_editor.name` })
    expect(errors.mock.calls.some(args => args.some(value => String(value).includes('same key')))).toBe(false)
  } finally { errors.mockRestore() }
})
it('converts observation with every field, edit version and explicit risk choice without reading hit data', async () => {
  const observation = { ...samplePolicy, action: 'observe' as const, name: 'Watch', counts_as_risk: false, list_ids: [7], scope: 'groups' as const, group_ids: [3, 9], template_key: 'crypto', inline: { ports: '443', network: 'tcp' as const, cidrs: ['192.0.2.0/24'] } }
  let converted = false
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: converted ? [{ ...observation, action: 'block', counts_as_risk: true }] : [], observe: converted ? [] : [observation] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  api.put.mockImplementation(async () => { converted = true; return { data: { ...observation, action: 'block', counts_as_risk: true, priority: 1, updated_at: 4000 } } })
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  fireEvent.click(within(dialog).getByRole('checkbox', { name: `${P}promotion.risk` }))
  fireEvent.click(within(dialog).getByRole('button', { name: `${P}promotion.action` }))
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dest/policies/12', expect.objectContaining({ name: 'Watch', action: 'block', list_ids: [7], inline: observation.inline, scope: 'groups', group_ids: [3, 9], enabled: true, counts_as_risk: true, template_key: 'crypto', updated_at: 2000 }), { _skipErrorToast: true }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(api.get.mock.calls.some(([url]) => /\/(hits|records|usage)/.test(url))).toBe(false)
  expect(snack).toHaveBeenCalledWith(`${P}promotion.saved_position`, 'success')
})
it('keeps a stale conversion risk choice until explicit reload, and cancellation never updates the policy', async () => {
  let row = { ...samplePolicy, action: 'observe' as const, name: 'Watch', counts_as_risk: false }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [], observe: [row] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  let dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.cancel' }))
  expect(api.put).not.toHaveBeenCalled()
  fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  const risk = within(dialog).getByRole('checkbox', { name: `${P}promotion.risk` }) as HTMLInputElement
  fireEvent.click(risk); api.put.mockRejectedValueOnce(err(409, 'dest_policy_stale'))
  fireEvent.click(within(dialog).getByRole('button', { name: `${P}promotion.action` }))
  const reload = await within(dialog).findByRole('button', { name: `${P}promotion.reload` })
  expect(risk.checked).toBe(true)
  row = { ...row, name: 'Latest', enabled: false, updated_at: 5000 }
  fireEvent.click(reload)
  await screen.findByRole('dialog', { name: `${P}promotion.title Latest` })
  expect(risk.checked).toBe(false)
  fireEvent.click(screen.getByRole('button', { name: `${P}promotion.action` }))
  await waitFor(() => expect(api.put).toHaveBeenLastCalledWith('/admin/dest/policies/12', expect.objectContaining({ name: 'Latest', enabled: false, counts_as_risk: false, updated_at: 5000 }), { _skipErrorToast: true }))
})
it('prevents duplicate promotion writes and closing while the save is pending', async () => {
  const row = { ...samplePolicy, action: 'observe' as const, name: 'Watch' }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [], observe: [row] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  let finish: (value: unknown) => void = () => {}
  api.put.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  const submit = within(dialog).getByRole('button', { name: `${P}promotion.action` })
  fireEvent.click(submit); fireEvent.click(submit)
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.cancel' }))
  expect(screen.getByRole('dialog')).toBe(dialog)
  finish({ data: { ...row, action: 'block' } })
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(snack).toHaveBeenCalledWith(`${P}promotion.saved`, 'success')
})
it('stops conversion when explicit reload finds a policy already changed to another action', async () => {
  let row = { ...samplePolicy, action: 'observe' as 'observe' | 'allow', name: 'Watch' }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [], [row.action]: [row] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  api.put.mockRejectedValueOnce(err(409, 'dest_policy_stale'))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  fireEvent.click(within(dialog).getByRole('button', { name: `${P}promotion.action` }))
  const reload = await within(dialog).findByRole('button', { name: `${P}promotion.reload` })
  row = { ...row, action: 'allow', updated_at: 5000 }; fireEvent.click(reload)
  await within(dialog).findByText(`${P}promotion.already_changed`)
  expect(within(dialog).queryByRole('button', { name: `${P}promotion.action` })).toBeNull()
  expect(api.put).toHaveBeenCalledTimes(1)
})
it('keeps risk selection when discard is declined and never writes on accepted cancellation', async () => {
  const row = { ...samplePolicy, action: 'observe' as const, name: 'Watch' }
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [], observe: [row] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } }))
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.menu Watch` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}promotion.action` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}promotion.title Watch` })
  const risk = within(dialog).getByRole('checkbox', { name: `${P}promotion.risk` })
  fireEvent.click(risk); confirmation.mockResolvedValueOnce(false)
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(1))
  expect((risk as HTMLInputElement).checked).toBe(true)
  expect(screen.getByRole('dialog')).toBe(dialog)
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(api.put).not.toHaveBeenCalled()
})
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
