/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import EnableAllowlistDialog from './EnableAllowlistDialog'

const calls = vi.hoisted(() => ({ groups: vi.fn(), lists: vi.fn(), preview: vi.fn(), save: vi.fn(), confirm: vi.fn(), snack: vi.fn() }))
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroups: calls.groups, useDestinationGroupPreview: calls.preview, useSaveDestinationGroup: () => ({ mutateAsync: calls.save, isPending: false }) }))
vi.mock('@/query/accessControl', () => ({ useDestinationLists: calls.lists }))
vi.mock('@/hooks/useLeaveGuard', () => ({ useLeaveGuard: () => 'unblocked' }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: calls.confirm }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: calls.snack }))
vi.mock('@/components/CodeEditor', () => ({ default: ({ value, onChange, ariaLabel }: { value: string; onChange: (text: string) => void; ariaLabel: string }) => <textarea aria-label={ariaLabel} value={value} onChange={e => onChange(e.target.value)} /> }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.allowlist_enable.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const group = { group_id: 7, group_name: 'Guests', mode: 'open', stage: '', list_ids: [], base_list_id: 0, extra_list_id: 0, updated_at: 0, stage_changed_at: null, total_nodes: 2, eligible_nodes: 2, applied_nodes: 0, members: 51, ineligible: [], would_deny_recent: { days: 7, sites: 0, count: 0 }, denied_24h: 0 }
const list = { id: 20, name: 'Finance', kind: 'remote', owner_group_id: 0, entry_count: 10, last_fetched_at: null, state: 'failed' }
const preview = { members: 51, total_nodes: 2, eligible_nodes: 1, ineligible: [{ panel_id: 3, name: 'Old node', reason: 'xui' }], base_hosts: ['dns.example.test'], base_hosts_available: true, base_list_id: 0, extra_list_id: 0, budget: {} }
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
function props() { return { scope, initialGroupID: 7, onClose: vi.fn(), onStarted: vi.fn(), onList: vi.fn() } }
beforeEach(() => { vi.clearAllMocks(); calls.groups.mockReturnValue({ data: { items: [group] }, error: null, isPending: false, isFetching: false, refetch: vi.fn().mockResolvedValue({ data: { items: [group] } }) }); calls.lists.mockReturnValue({ data: { items: [list] }, error: null, isFetching: false }); calls.preview.mockReturnValue({ data: preview, error: null, isFetching: false, isPending: false }); calls.save.mockResolvedValue({ group_id: 7 }); calls.confirm.mockResolvedValue(true) })
afterEach(cleanup)
function chooseLists() { fireEvent.click(screen.getByRole('button', { name: `${P}next` })); fireEvent.click(screen.getByRole('checkbox', { name: /Finance/ })) }
it('uses sequential steps and saves one atomic trial request with the prospective DNS hosts and initial CAS zero', async () => {
  const actions = props(); render(<EnableAllowlistDialog {...actions} />, { wrapper })
  chooseLists()
  expect(screen.getByText(`${P}unready`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}next` }))
  expect(screen.getByText('Old node')).toBeTruthy()
  expect(screen.getByText(`${P}trial_notice`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await waitFor(() => expect(actions.onStarted).toHaveBeenCalledWith(7))
  expect(calls.save).toHaveBeenCalledExactlyOnceWith({ groupID: 7, input: { mode: 'allowlist', stage: 'trial', list_ids: [20], updated_at: 0, base_hosts: ['dns.example.test'] } })
})
it('requires at least one list and supports manual base hosts when template extraction is unavailable', async () => {
  calls.preview.mockReturnValue({ data: { ...preview, base_hosts: [], base_hosts_available: false }, error: null, isFetching: false })
  render(<EnableAllowlistDialog {...props()} />, { wrapper })
  fireEvent.click(screen.getByRole('button', { name: `${P}next` }))
  expect((screen.getByRole('button', { name: `${P}next` }) as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByText(`${P}dns_unknown`)).toBeTruthy()
  fireEvent.change(await screen.findByRole('textbox', { name: `${P}base_hosts` }), { target: { value: 'manual.example.test\n' } })
  fireEvent.click(screen.getByRole('checkbox', { name: /Finance/ }))
  fireEvent.click(screen.getByRole('button', { name: `${P}next` }))
  fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await waitFor(() => expect(calls.save).toHaveBeenCalledOnce())
  expect(calls.save.mock.calls[0][0].input.base_hosts).toEqual(['manual.example.test'])
})
it('asks before enabling with zero eligible nodes and never silently blocks the owner choice', async () => {
  calls.preview.mockReturnValue({ data: { ...preview, eligible_nodes: 0 }, error: null, isFetching: false })
  calls.confirm.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
  render(<EnableAllowlistDialog {...props()} />, { wrapper }); chooseLists(); fireEvent.click(screen.getByRole('button', { name: `${P}next` }))
  fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await waitFor(() => expect(calls.confirm).toHaveBeenCalledOnce())
  expect(calls.save).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await waitFor(() => expect(calls.save).toHaveBeenCalledOnce())
})
it('preserves owned base/extra sources when reopening a previously disabled group', async () => {
  calls.groups.mockReturnValue({ data: { items: [{ ...group, base_list_id: 10, extra_list_id: 11, updated_at: 1000 }] } })
  render(<EnableAllowlistDialog {...props()} />, { wrapper }); chooseLists()
  expect(screen.queryByRole('textbox', { name: `${P}base_hosts` })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: `${P}next` })); fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await waitFor(() => expect(calls.save).toHaveBeenCalledOnce())
  expect(calls.save.mock.calls[0][0].input).toEqual({ mode: 'allowlist', stage: 'trial', list_ids: [20], updated_at: 1000 })
})
it('preserves the list draft after a failed save and requires a fresh review after a conflict', async () => {
  calls.save.mockRejectedValueOnce({ isAxiosError: true, response: { status: 409, data: { error: 'dest_mode_stale' } } })
  render(<EnableAllowlistDialog {...props()} />, { wrapper }); chooseLists(); fireEvent.click(screen.getByRole('button', { name: `${P}next` })); fireEvent.click(screen.getByRole('button', { name: `${P}start` }))
  await screen.findByText(`${P}conflict`)
  expect((screen.getByRole('button', { name: `${P}start` }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: `${P}back` }))
  expect((screen.getByRole('checkbox', { name: /Finance/ }) as HTMLInputElement).checked).toBe(true)
})
it('allows cancelling a dirty close and shows all-enabled and read-error states without submission', async () => {
  const actions = props(), view = render(<EnableAllowlistDialog {...actions} />, { wrapper }); chooseLists()
  calls.confirm.mockResolvedValue(false)
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(calls.confirm).toHaveBeenCalledOnce()); expect(actions.onClose).not.toHaveBeenCalled()
  view.unmount(); calls.groups.mockReturnValue({ data: { items: [{ ...group, mode: 'allowlist', stage: 'trial' }] } })
  render(<EnableAllowlistDialog {...props()} />, { wrapper })
  expect(screen.getByText(`${P}all_enabled`)).toBeTruthy()
  expect((screen.getByRole('button', { name: `${P}next` }) as HTMLButtonElement).disabled).toBe(true)
})
