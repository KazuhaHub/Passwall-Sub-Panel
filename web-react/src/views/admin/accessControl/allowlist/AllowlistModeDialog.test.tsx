/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import type { DestinationGroupDetail } from '@/api/destinationGroups'
import AllowlistModeDialog from './AllowlistModeDialog'

const calls = vi.hoisted(() => ({ read: vi.fn(), save: vi.fn(), snack: vi.fn() }))
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroup: calls.read, useSaveDestinationGroup: () => ({ mutateAsync: calls.save, isPending: false }) }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: calls.snack }))
vi.mock('../PendingActionGuard', () => ({ default: () => null }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.allowlist_mode.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const detail: DestinationGroupDetail = { group_id: 7, group_name: 'Guests', mode: 'allowlist', stage: 'trial', list_ids: [20], base_list_id: 10, extra_list_id: 11, updated_at: 1000, stage_changed_at: 1000, total_nodes: 1, eligible_nodes: 1, applied_nodes: 1, members: 51, ineligible: [], would_deny_recent: { days: 3, sites: 4, count: 9 }, denied_24h: 0, readiness: { lists_ready: true, not_ready_lists: [], unapplied_nodes: [], base_nonempty: false, remaining_sites_24h: 4, top_sites: [{ site: 'example.test', count: 9, last_at: 1000, added: false }], trial_losses: { rows: 0, events: 0, unmatched: 0, scope: 'panel', complete: false } } }
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
function props() { return { scope, groupID: 7, target: 'enforce' as const, retentionDays: 3, onClose: vi.fn(), onReport: vi.fn(), onNodes: vi.fn() } }
function ready(data = detail) { return { data, isPending: false, isFetching: false, isFetchedAfterMount: true, error: null, refetch: vi.fn().mockResolvedValue({ data, error: null }) } }
beforeEach(() => { vi.clearAllMocks(); calls.read.mockReturnValue(ready()); calls.save.mockResolvedValue({}) })
afterEach(cleanup)
it('treats an empty base and remaining sites as warnings while saving the complete fresh CAS mode once', async () => {
  const actions = props(); render(<AllowlistModeDialog {...actions} />, { wrapper })
  const button = screen.getByRole('button', { name: `${P}confirm_enforce` })
  expect((button as HTMLButtonElement).disabled).toBe(false)
  expect(screen.getByText(`${P}base_empty`)).toBeTruthy()
  expect(screen.getByText(`${P}remaining`)).toBeTruthy()
  fireEvent.click(button); fireEvent.click(button)
  await waitFor(() => expect(actions.onClose).toHaveBeenCalledOnce())
  expect(calls.save).toHaveBeenCalledExactlyOnceWith({ groupID: 7, input: { mode: 'allowlist', stage: 'enforce', list_ids: [20], updated_at: 1000 } })
})
it.each(['zero', 'unapplied', 'lists', 'fetching', 'cached', 'error'])('blocks enforcement when readiness is %s', reason => {
  const q = ready(structuredClone(detail))
  if (reason === 'zero') q.data.eligible_nodes = 0
  if (reason === 'unapplied') { q.data.applied_nodes = 0; q.data.readiness.unapplied_nodes = [{ panel_id: 1, name: 'Tokyo' }] }
  if (reason === 'lists') { q.data.readiness.lists_ready = false; q.data.readiness.not_ready_lists = [{ id: 20, name: 'Pending' }] }
  if (reason === 'fetching') q.isFetching = true
  if (reason === 'cached') q.isFetchedAfterMount = false
  calls.read.mockReturnValue(reason === 'error' ? { ...q, error: new Error('read failed') } : q)
  render(<AllowlistModeDialog {...props()} />, { wrapper })
  expect((screen.getByRole('button', { name: `${P}confirm_enforce` }) as HTMLButtonElement).disabled).toBe(true)
  expect(calls.save).not.toHaveBeenCalled()
})
it.each(['trial', 'open'] as const)('allows %s to relax restrictions without enforcement blockers', async target => {
  const q = ready({ ...detail, stage: 'enforce', eligible_nodes: 0, applied_nodes: 0, readiness: { ...detail.readiness, lists_ready: false } })
  calls.read.mockReturnValue(q)
  render(<AllowlistModeDialog {...props()} target={target} />, { wrapper })
  const button = screen.getByRole('button', { name: `${P}confirm_${target}` })
  expect(button.className).not.toContain('colorError')
  fireEvent.click(button)
  await waitFor(() => expect(calls.save).toHaveBeenCalledOnce())
  expect(calls.save.mock.calls[0][0].input).toEqual({ mode: target === 'open' ? 'open' : 'allowlist', stage: target === 'open' ? '' : 'trial', list_ids: [20], updated_at: 1000 })
})
it('requires an explicit fresh review after a conflict and retains the intended transition', async () => {
  calls.save.mockRejectedValueOnce({ isAxiosError: true, response: { status: 409, data: { error: 'dest_mode_stale' } } })
  const q = ready(), actions = props()
  calls.read.mockReturnValue(q)
  const view = render(<AllowlistModeDialog {...actions} />, { wrapper })
  fireEvent.click(screen.getByRole('button', { name: `${P}confirm_enforce` }))
  await screen.findByText(`${P}conflict`)
  expect((screen.getByRole('button', { name: `${P}confirm_enforce` }) as HTMLButtonElement).disabled).toBe(true)
  q.data = { ...detail, updated_at: 2000 }
  q.refetch.mockResolvedValue({ data: q.data, error: null })
  view.rerender(<AllowlistModeDialog {...actions} />)
  fireEvent.click(screen.getByRole('button', { name: `${P}review` }))
  await waitFor(() => expect((screen.getByRole('button', { name: `${P}confirm_enforce` }) as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(screen.getByRole('button', { name: `${P}confirm_enforce` }))
  await waitFor(() => expect(calls.save).toHaveBeenCalledTimes(2))
  expect(calls.save.mock.calls[1][0].input.updated_at).toBe(2000)
})
it('holds closing while a save is pending and exposes actionable node/report links', async () => {
  let finish!: (value: unknown) => void
  calls.save.mockImplementation(() => new Promise(resolve => { finish = resolve }))
  const actions = props(); render(<AllowlistModeDialog {...actions} />, { wrapper })
  fireEvent.click(screen.getByRole('button', { name: `${P}nodes` }))
  expect(actions.onNodes).toHaveBeenCalledWith(7)
  fireEvent.click(screen.getByRole('button', { name: `${P}report` }))
  expect(actions.onReport).toHaveBeenCalledWith(7)
  fireEvent.click(screen.getByRole('button', { name: `${P}confirm_enforce` }))
  expect((screen.getByRole('button', { name: 'common:actions.cancel' }) as HTMLButtonElement).disabled).toBe(true)
  await act(async () => finish({}))
  expect(actions.onClose).toHaveBeenCalledOnce()
})
