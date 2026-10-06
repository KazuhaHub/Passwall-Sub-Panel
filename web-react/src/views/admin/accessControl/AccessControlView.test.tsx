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
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: Record<string, unknown>) => key + (options?.name ? ` ${options.name}` : key === 'admin:access_control.templates.count' ? ` ${options?.count}/${options?.regexps}` : ''), i18n: { language: 'en-US' } }) }))
vi.mock('@/components/CodeEditor', () => ({ default: (p: { value: string; onChange: (s: string) => void; ariaLabel: string; readOnly: boolean }) => <textarea aria-label={p.ariaLabel} value={p.value} readOnly={p.readOnly} onChange={e => p.onChange(e.target.value)} /> }))
import AccessControlView from './AccessControlView'
const P = 'admin:access_control.'
it('opens the account Access drawer from the header and closes back to the page', async () => {
  const original = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string, config?: unknown) => {
    if (url === '/admin/users') return { data: { items: [{ id: 13, upn: 'alice@example.test' }], total: 1 } }
    if (url === '/admin/users/13') return { data: { id: 13, upn: 'alice@example.test' } }
    if (url === '/admin/dest/users/13') return { data: { group: null, exemption: null } }
    if (url === '/admin/risk-center/users/13') return { data: { user: { id: 13, upn: 'alice@example.test', display_name: '', role: 'user', enabled: true, group_id: 0, group_name: '', traffic_limit_bytes: 0 }, attention: [], review: { dismissed: false, trusted: false, escalated: [] }, geo: null, signals: [], live: {}, devices: [], device_window_hours: 24, devices_unavailable: false } }
    if (url.startsWith('/admin/risk-center/')) throw err(503, 'unavailable')
    return original(url, config)
  })
  const router = mount()
  fireEvent.mouseDown(await screen.findByRole('combobox', { name: `${P}view_account` }))
  fireEvent.click(await screen.findByRole('option', { name: 'alice@example.test' }))
  await screen.findByRole('tab', { name: 'admin:risk_center.drawer.tab_access', selected: true })
  expect(router.state.location.search).toBe('?user=13')
  fireEvent.click(screen.getByRole('button', { name: 'admin:risk_center.drawer.close' }))
  await waitFor(() => expect(router.state.location.search).toBe(''))
})
it('opens list refresh settings focused on the refresh interval', async () => {
  listsAPI([listSummary]); settingsAPI(); mount('/admin/access-control?tab=lists')
  fireEvent.click(await screen.findByRole('button', { name: `${P}list_editor.change_refresh` }))
  const refresh = await screen.findByRole('textbox', { name: `${P}settings.dest_list_refresh_hours` })
  await waitFor(() => expect(document.activeElement).toBe(refresh))
})
it('replaces a cold list sheet with refresh settings and closes in place', async () => {
  listsAPI([listSummary]); settingsAPI(); const router = mount('/admin/access-control?tab=lists&sheet=list&list=7')
  fireEvent.click(await screen.findByRole('button', { name: 'common:actions.edit' }))
  const editor = await screen.findByRole('dialog', { name: `${P}list_editor.edit_title` })
  fireEvent.click(within(editor).getByRole('button', { name: `${P}list_editor.change_refresh` }))
  const settings = await screen.findByRole('dialog', { name: `${P}settings.title` })
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists&sheet=settings'))
  fireEvent.click(within(settings).getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists'))
})
it('replaces a cold list drawer with a state-prefilled test and closes in place', async () => {
  listsAPI([listSummary]); const router = mount('/admin/access-control?tab=lists&sheet=list&list=7')
  fireEvent.click(await screen.findByRole('button', { name: `${P}test.test_entry domain:bank.example` }))
  const target = await screen.findByRole('textbox', { name: `${P}test.target` }) as HTMLInputElement
  expect(target.value).toBe('bank.example'); expect(router.state.location.search).toBe('?tab=lists&sheet=test')
  expect(router.state.location.state).toEqual({ prefill: { target: 'bank.example' } })
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists'))
  expect(router.state.location.state).toBeNull()
})
it('opens testing from the page without automatically posting any target', async () => {
  const router = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}test.title` }))
  await screen.findByRole('dialog', { name: `${P}test.title` })
  expect(router.state.location.search).toBe('?sheet=test')
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe(''))
})
it('opens the matched policy and replaces the sheet history without leaking the target', async () => {
  api.post.mockResolvedValue({ data: { verdict: 'block', terminating_step: 'block', unpublished: false, steps: [{ step: 'block', result: 'hit', policy_id: 12, name: 'No mail' }], notes: [], nodes: [] } })
  const router = mount('/admin/access-control?tab=lists&sheet=test')
  fireEvent.change(await screen.findByRole('textbox', { name: `${P}test.target` }), { target: { value: 'secret.example.test' } })
  fireEvent.click(screen.getByRole('button', { name: `${P}test.submit` }))
  fireEvent.click(await screen.findByRole('button', { name: `${P}test.open_policy No mail` }))
  await screen.findByRole('dialog', { name: `${P}editor.edit_title No mail` })
  expect(router.state.location.search).toBe('?tab=policies')
  expect(screen.queryByRole('dialog', { name: `${P}test.title` })).toBeNull()
})
it('opens exemptions with owned history and preserves the list tab on cold close', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/exemptions') ? { items: [] } : url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items: [] } : { effective: {} } }))
  const router = mount('/admin/access-control?tab=lists&sheet=exemptions')
  const drawer = await screen.findByRole('dialog', { name: `${P}exemptions.title` })
  expect(await within(drawer).findByText(`${P}exemptions.empty`)).toBeTruthy()
  fireEvent.click(within(drawer).getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists'))
})
it('opens an exempt account in one navigation that clears sheet-owned parameters', async () => {
  api.get.mockImplementation(async (url: string) => {
    if (url.startsWith('/admin/risk-center/')) throw err(503, 'unavailable')
    return { data: url.endsWith('/exemptions') ? { items: [{ user_id: 13, upn: 'alice@test', reason: 'Diagnostics', created_by: 42, created_by_upn: 'admin@test', created_at: Date.now(), expires_at: null, expired: false }] } : url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items: [] } : { effective: {} } }
  })
  const router = mount('/admin/access-control?tab=lists&sheet=exemptions&list=7&node_state=problem')
  fireEvent.click(await screen.findByRole('button', { name: `${P}exemptions.menu` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}exemptions.open_account` }))
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists&user=13'))
  expect(router.state.location.state).toBeNull()
  expect(screen.queryByRole('dialog', { name: `${P}exemptions.title` })).toBeNull()
})
it('replaces an owned exemptions sheet so closing the account returns to the page without reviving it', async () => {
  api.get.mockImplementation(async (url: string) => {
    if (url.startsWith('/admin/risk-center/')) throw err(503, 'unavailable')
    return { data: url.endsWith('/exemptions') ? { items: [{ user_id: 13, upn: 'alice@test', reason: 'Diagnostics', created_by: 42, created_by_upn: 'admin@test', created_at: Date.now(), expires_at: null, expired: false }] } : url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items: [] } : { effective: {} } }
  })
  const router = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}exemptions.manage` }))
  fireEvent.click(await screen.findByRole('button', { name: `${P}exemptions.menu` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}exemptions.open_account` }))
  fireEvent.click(await screen.findByRole('button', { name: 'admin:risk_center.drawer.close' }))
  await waitFor(() => expect(router.state.location.search).toBe(''))
  expect(screen.queryByRole('dialog', { name: `${P}exemptions.title` })).toBeNull()
})
it('opens only the account on conflicting cold drawer parameters and clears the inactive sheet on close', async () => {
  api.get.mockImplementation(async (url: string) => {
    if (url.startsWith('/admin/risk-center/')) throw err(503, 'unavailable')
    return { data: url.endsWith('/policies') ? destinationPolicies() : url.endsWith('/status') ? destinationStatus() : url.endsWith('/lists') ? { items: [] } : { effective: {} } }
  })
  const router = mount('/admin/access-control?tab=lists&user=13&sheet=exemptions&list=7&node_state=problem')
  const close = await screen.findByRole('button', { name: 'admin:risk_center.drawer.close' })
  expect(screen.queryByRole('dialog', { name: `${P}exemptions.title` })).toBeNull()
  expect(api.get.mock.calls.some(([url]) => url.endsWith('/exemptions'))).toBe(false)
  fireEvent.click(close)
  await waitFor(() => expect(router.state.location.search).toBe('?tab=lists'))
})
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
function settingsAPI() {
  const original = api.get.getMockImplementation()!
  const defaults = { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 }
  api.get.mockImplementation(async (url: string, config?: unknown) => url.endsWith('/dest/settings') ? { data: { settings: { ...defaults }, defaults, effective: defaults } } : original(url, config))
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
it('opens a category template with actual cached counts and saves its list only with the policy', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [] }) : url.endsWith('/status') ? destinationStatus() : url.endsWith('/categories') ? { categories: [{ name: 'category-cryptocurrency', count: 37, regexp_count: 2, attrs: ['!cn'] }, { name: 'category-porn', count: 18, regexp_count: 11, attrs: [] }], updated_at: 1000 } : url.endsWith('/lists') || url.endsWith('/groups') ? { items: [], total: 0 } : { effective: {} } }))
  api.post.mockImplementation(async (url: string, input: Record<string, unknown>) => ({ data: url.endsWith('/preview') ? { budget: destinationBudget, new_list_preview: { parse_report: { accepted: 37, ignored: 1, ignored_broad: 1, rewritten: 0, samples: [{ line: 7, text: 'domain:hsbc', reason: 'broad_entry' }] }, entries: ['domain:exchange.test'], content_sha256: 'full-category-digest', entry_count: 37, regexp_count: 2, http_status: 0, bytes: 0 } } : { ...samplePolicy, ...input, id: 50, list_ids: [9] } }))
  mount()
  const use = await screen.findByRole('button', { name: `${P}templates.use ${P}policies.template_crypto` })
  expect(screen.getByText(`${P}templates.count 37/2`)).toBeTruthy()
  expect(screen.getByText(`${P}templates.count 18/11`)).toBeTruthy()
  expect(screen.queryByRole('heading', { name: `${P}policies.allow` })).toBeNull()
  fireEvent.click(use)
  expect((await screen.findByRole('textbox', { name: `${P}editor.name` }) as HTMLInputElement).value).toBe(`${P}policies.template_crypto`)
  await waitFor(() => expect(api.post.mock.calls.some(([url, input]) => url.endsWith('/policies/preview') && input.new_list?.geosite_category === 'category-cryptocurrency')).toBe(true))
  expect(api.post.mock.calls.some(([url]) => url.endsWith('/lists') || url.endsWith('/geosite/refresh'))).toBe(false)
  await screen.findByText(`${P}parse_report.broad_removed`)
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/policies', expect.objectContaining({ action: 'observe', enabled: true, counts_as_risk: false, template_key: 'crypto', list_ids: [], new_list: expect.objectContaining({ kind: 'geosite', geosite_category: 'category-cryptocurrency' }) }), { _skipErrorToast: true }))
})
it('shows missing-category download controls without automatically downloading or saving template lists', async () => {
  api.get.mockImplementation(async (url: string) => { if (url.endsWith('/categories')) throw err(503, 'dest_geosite_unavailable'); return { data: url.endsWith('/policies') ? destinationPolicies({ block: [] }) : url.endsWith('/status') ? destinationStatus() : { effective: {} } } })
  mount()
  const downloads = await screen.findAllByRole('button', { name: `${P}categories.download` })
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(downloads[0])
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/geosite/refresh', undefined, { _skipErrorToast: true }))
  expect(api.post.mock.calls.some(([url]) => url.endsWith('/lists') || url.endsWith('/policies'))).toBe(false)
})
it('opens templates from the new-policy menu even when policies already exist', async () => {
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}policies.create` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}policies.template_mail` }))
  expect((await screen.findByRole('textbox', { name: `${P}editor.ports` }) as HTMLInputElement).value).toBe('25,465,587')
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(api.post.mock.calls.some(([url]) => url.endsWith('/lists') || url.endsWith('/policies'))).toBe(false)
})
it('opens list creation from the finance explanation without writing a policy or inventing a financial template', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [] }) : url.endsWith('/status') ? destinationStatus() : url.endsWith('/categories') ? { categories: [], updated_at: 1000 } : url.endsWith('/lists') ? { items: [], budget: destinationBudget, refresh_hours: 24 } : { effective: {} } }))
  const router = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}templates.create_list` }))
  await screen.findByRole('textbox', { name: `${P}list_editor.name` })
  expect(router.state.location.search).toBe('?tab=lists')
  expect(api.post).not.toHaveBeenCalled()
})
it('keeps an over-quota category template editable but blocks saving its rejected preview', async () => {
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/policies') ? destinationPolicies({ block: [], budget: { ...destinationBudget, regexps: { used: 250, limit: 256 } } }) : url.endsWith('/status') ? destinationStatus() : url.endsWith('/categories') ? { categories: [{ name: 'category-porn', count: 18, regexp_count: 11, attrs: [] }], updated_at: 1000 } : url.endsWith('/lists') || url.endsWith('/groups') ? { items: [], total: 0 } : { effective: {} } }))
  api.post.mockRejectedValue(err(400, 'dest_policy_over_limit'))
  mount()
  expect(await screen.findByRole('button', { name: `${P}templates.over_summary` })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}templates.use ${P}policies.template_porn` }))
  await screen.findByText(`${P}quota.exceeded`)
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(true)
  expect(api.post.mock.calls.some(([url]) => url.endsWith('/policies') || url.endsWith('/lists'))).toBe(false)
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
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}templates.blank` }))
  fireEvent.change(await screen.findByRole('textbox', { name: `${P}editor.name` }), { target: { value: 'New policy' } })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}editor.ports` }), { target: { value: '443' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/policies', expect.objectContaining({ name: 'New policy', action: 'block', enabled: false, inline: expect.objectContaining({ ports: '443' }), list_ids: [], scope: 'all', group_ids: [] }), { _skipErrorToast: true }))
  const body = api.post.mock.calls.find(call => call[0] === '/admin/dest/policies')![1]
  expect(body).not.toHaveProperty('updated_at'); expect(body).not.toHaveProperty('id')
})
