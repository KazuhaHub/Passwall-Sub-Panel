/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import TrialReport from './TrialReport'

const calls = vi.hoisted(() => ({ detail: vi.fn(), report: vi.fn(), lists: vi.fn(), add: vi.fn(), snack: vi.fn() }))
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroup: calls.detail, useDestinationGroupTrial: calls.report, useAddDestinationGroupSites: () => ({ mutateAsync: calls.add, isPending: false }) }))
vi.mock('@/query/accessControl', () => ({ useDestinationLists: calls.lists }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: calls.snack }))
vi.mock('../PendingActionGuard', () => ({ default: () => null }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.trial_report.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const sites = [{ site: 'new.test', count: 9, last_at: 1000, added: false }, { site: '(ip)', count: 2, last_at: 2000, added: false }, { site: 'covered.test', count: 3, last_at: 3000, added: true }, ...Array.from({ length: 27 }, (_, n) => ({ site: `site${n}.test`, count: 1, last_at: 1000, added: false }))]
const detail = { group_id: 7, group_name: 'Guests', mode: 'allowlist', stage: 'trial', list_ids: [20], extra_list_id: 11, base_list_id: 10, stage_changed_at: Date.now(), denied_24h: 12 }
const report = { effective_days: 1, items: sites, dropped_in_range: 4, losses: { rows: 4, events: 7, unmatched: 2, scope: 'panel', complete: false } }
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
function props() { return { scope, groupID: 7, days: 7, retentionDays: 7, onDays: vi.fn(), onBack: vi.fn(), onEnforce: vi.fn(), onList: vi.fn(), onTest: vi.fn(), onDenied: vi.fn() } }
beforeEach(() => { vi.clearAllMocks(); calls.detail.mockReturnValue({ data: detail, isFetchedAfterMount: true, error: null, isPending: false, refetch: vi.fn() }); calls.report.mockReturnValue({ data: report, error: null, isPending: false, isFetching: false, refetch: vi.fn() }); calls.lists.mockReturnValue({ data: { items: [{ id: 11, name: 'Extra', kind: 'custom', owner_group_id: 7 }, { id: 20, name: 'Shared', kind: 'custom', owner_group_id: 0 }, { id: 99, name: 'Other group', kind: 'custom', owner_group_id: 99 }] }, error: null }); calls.add.mockResolvedValue({}) })
afterEach(cleanup)
it('keeps all sites, server-effective days and separate panel loss units without account/sample columns', () => {
  render(<TrialReport {...props()} />, { wrapper })
  expect(screen.getAllByRole('article')).toHaveLength(30)
  expect(screen.getByText('site26.test')).toBeTruthy()
  expect(screen.getByText(`${P}effective_days`)).toBeTruthy()
  expect(screen.getByText(`${P}incomplete`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}loss_details` }))
  expect(within(screen.getByRole('dialog')).getByText(`${P}loss_scope`)).toBeTruthy()
  expect(screen.queryByText('user_id')).toBeNull()
  expect(screen.queryByText('sample_host')).toBeNull()
})
it('never offers add/selection for IP or already-covered rows and opens the actual extra list for IPs', () => {
  const actions = props(); render(<TrialReport {...actions} />, { wrapper })
  const ip = screen.getAllByRole('article').find(row => row.textContent?.includes('(ip)'))!
  expect(within(ip).queryByRole('checkbox')).toBeNull()
  expect(within(ip).queryByRole('button', { name: `${P}add` })).toBeNull()
  fireEvent.click(within(ip).getByRole('button', { name: `${P}edit_extra` }))
  expect(actions.onList).toHaveBeenCalledWith(11)
  const covered = screen.getAllByRole('article').find(row => row.textContent?.includes('covered.test'))!
  expect(within(covered).queryByRole('checkbox')).toBeNull()
  expect(within(covered).getByText(`${P}added`)).toBeTruthy()
})
it('adds single and bulk whole sites through one atomic entries request per action', async () => {
  render(<TrialReport {...props()} />, { wrapper })
  const row = screen.getAllByRole('article').find(item => item.textContent?.includes('new.test'))!
  fireEvent.click(within(row).getByRole('button', { name: `${P}add` }))
  await waitFor(() => expect(calls.add).toHaveBeenCalledWith({ listID: 11, sites: ['new.test'] }))
  fireEvent.click(screen.getByRole('checkbox', { name: 'site0.test' })); fireEvent.click(screen.getByRole('checkbox', { name: 'site1.test' }))
  fireEvent.click(screen.getByRole('button', { name: `${P}add_selected` }))
  await waitFor(() => expect(calls.add).toHaveBeenCalledWith({ listID: 11, sites: ['site0.test', 'site1.test'] }))
  expect(calls.add).toHaveBeenCalledTimes(2)
})
it('searches the complete report in component state and keeps explicit read failures out of stale results', () => {
  const view = render(<TrialReport {...props()} />, { wrapper })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}search` }), { target: { value: 'site26' } })
  expect(screen.getAllByRole('article')).toHaveLength(1)
  expect(screen.getByText('site26.test')).toBeTruthy()
  calls.report.mockReturnValue({ data: report, error: new Error('unavailable'), isFetching: false, refetch: vi.fn() })
  view.rerender(<TrialReport {...props()} />)
  expect(screen.getByText(`${P}read_failed`)).toBeTruthy()
  expect(screen.queryByRole('article')).toBeNull()
})
it('shows real enforced denial counts and links to records without fetching historical trial rows', () => {
  const enforced = { ...detail, stage: 'enforce' }
  calls.detail.mockReturnValue({ data: enforced, isFetchedAfterMount: true, error: null })
  const actions = props(); render(<TrialReport {...actions} />, { wrapper })
  expect(calls.report).toHaveBeenLastCalledWith(scope, 7, 7, false)
  expect(screen.queryByRole('article')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: `${P}view_denied` }))
  expect(actions.onDenied).toHaveBeenCalledWith(7)
})
