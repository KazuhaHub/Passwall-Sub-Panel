/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import AllowlistTab from './AllowlistTab'

const queries = vi.hoisted(() => ({ groups: vi.fn(), lists: vi.fn() }))
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroups: queries.groups }))
vi.mock('@/query/accessControl', () => ({ useDestinationLists: queries.lists }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.allowlist.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
function props() { return { scope, active: true, onEnable: vi.fn(), onList: vi.fn(), onReport: vi.fn(), onDenied: vi.fn(), onEdit: vi.fn(), onChangeMode: vi.fn(), onOpenGroup: vi.fn() } }
const trial = { group_id: 7, group_name: 'Guests', mode: 'allowlist', stage: 'trial', list_ids: [], base_list_id: 10, extra_list_id: 11, updated_at: 1000, stage_changed_at: 1000, total_nodes: 0, eligible_nodes: 0, applied_nodes: 0, members: 51, ineligible: [], would_deny_recent: { days: 1, sites: 0, count: 0 }, denied_24h: 0 }
beforeEach(() => { vi.clearAllMocks(); queries.groups.mockReturnValue({ isPending: false, isFetching: false, data: { items: [trial] }, error: null, refetch: vi.fn() }); queries.lists.mockReturnValue({ isPending: false, isFetching: false, data: undefined, error: null, refetch: vi.fn() }) })
afterEach(cleanup)

it('shows two skeletons during the first read, then an explicit empty state for open groups', () => {
  queries.groups.mockReturnValueOnce({ isPending: true, error: null })
  const actions = props(), view = render(<AllowlistTab {...actions} />, { wrapper })
  expect(screen.getAllByRole('progressbar')).toHaveLength(2)
  expect(screen.queryByText(`${P}empty_title`)).toBeNull()
  queries.groups.mockReturnValue({ isPending: false, error: null, data: { items: [{ ...trial, mode: 'open', stage: '' }] } })
  view.rerender(<AllowlistTab {...actions} />)
  expect(screen.getByText(`${P}empty_title`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}enable` }))
  expect(actions.onEnable).toHaveBeenCalledOnce()
  expect(screen.queryByRole('article')).toBeNull()
})
it('withholds stale group cards on a read failure and retries explicitly', () => {
  const refetch = vi.fn(); queries.groups.mockReturnValue({ isPending: false, isFetching: false, data: { items: [trial] }, error: new Error('unavailable'), refetch })
  render(<AllowlistTab {...props()} />, { wrapper })
  expect(screen.getByRole('alert').textContent).toBe(`${P}read_failed` + 'common:actions.retry')
  expect(screen.queryByRole('heading', { name: 'Guests' })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
  expect(refetch).toHaveBeenCalledOnce()
})
it('keeps list failures separate from known coverage and gates both reads when inactive', () => {
  queries.lists.mockReturnValue({ data: undefined, error: new Error('lists unavailable'), refetch: vi.fn() })
  const view = render(<AllowlistTab {...props()} />, { wrapper })
  expect(screen.getByRole('heading', { name: 'Guests' })).toBeTruthy()
  expect(screen.getByText(`${P}lists_failed`)).toBeTruthy()
  expect(screen.getByText(`${P}lists_unknown`)).toBeTruthy()
  view.rerender(<AllowlistTab {...props()} active={false} />)
  expect(queries.groups).toHaveBeenLastCalledWith(scope, false)
  expect(queries.lists).toHaveBeenLastCalledWith(scope, false)
  expect(screen.queryByRole('article')).toBeNull()
})
