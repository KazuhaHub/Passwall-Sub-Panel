/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { sessionScope } from '@/query/session'
import GroupDestinationSummary from './GroupDestinationSummary'

const calls = vi.hoisted(() => ({ read: vi.fn(), lists: vi.fn() }))
vi.mock('@/query/destinationGroups', () => ({ useDestinationGroup: calls.read }))
vi.mock('@/query/accessControl', () => ({ useDestinationLists: calls.lists }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
const P = 'admin:groups.destination.'
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const group = { group_id: 7, mode: 'allowlist', stage: 'trial', stage_changed_at: Date.now(), list_ids: [20], base_list_id: 10, extra_list_id: 11, eligible_nodes: 1, total_nodes: 2, ineligible: [{ panel_id: 2, name: 'Third party', reason: 'xui' }] }
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
const props = () => ({ scope, groupID: 7, busy: false, onOpen: vi.fn() })
beforeEach(() => { vi.clearAllMocks(); calls.read.mockReturnValue({ data: group, error: null }); calls.lists.mockReturnValue({ data: { items: [10, 11, 20].map(id => ({ id, name: `List ${id}`, entry_count: id })) }, error: null }) })
afterEach(cleanup)
it('makes no private read for a new group', () => {
  render(<GroupDestinationSummary {...props()} groupID={null} />, { wrapper })
  expect(screen.getByText(`${P}create_hint`)).toBeTruthy()
  expect(calls.read).not.toHaveBeenCalled(); expect(calls.lists).not.toHaveBeenCalled()
})
it('renders owned and selected list counts with eligible coverage and a readonly edit link', () => {
  const actions = props(); render(<GroupDestinationSummary {...actions} />, { wrapper })
  expect(screen.getByText(`${P}lists`)).toBeTruthy(); expect(screen.getByText(`${P}nodes`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}edit` }))
  expect(actions.onOpen).toHaveBeenCalledExactlyOnceWith('/admin/access-control?tab=allowlist&al_group=7')
})
it('opens first activation for open groups and disables irrelevant list reads', () => {
  calls.read.mockReturnValue({ data: { ...group, mode: 'open', stage: '' }, error: null })
  const actions = props(); render(<GroupDestinationSummary {...actions} />, { wrapper })
  expect(calls.lists).toHaveBeenCalledWith(scope, false)
  expect(screen.getByText(`${P}open_notice`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}enable` }))
  expect(actions.onOpen).toHaveBeenCalledWith('/admin/access-control?tab=allowlist&al_enable=7')
})
it('withholds cached summaries on read failure and retries the failed source', () => {
  const retry = vi.fn(); calls.read.mockReturnValue({ data: group, error: new Error('failed'), refetch: retry })
  render(<GroupDestinationSummary {...props()} />, { wrapper })
  expect(screen.queryByText(`${P}lists`)).toBeNull(); expect(screen.queryByRole('button', { name: `${P}edit` })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' })); expect(retry).toHaveBeenCalledOnce()
})
it('keeps failed or missing list metadata explicitly unknown', () => {
  calls.lists.mockReturnValue({ data: { items: [] }, error: new Error('failed'), refetch: vi.fn() })
  render(<GroupDestinationSummary {...props()} />, { wrapper })
  expect(screen.getByText(`${P}lists_unknown`)).toBeTruthy(); expect(screen.queryByText(`${P}lists`)).toBeNull()
})
it('holds summary departure while the containing group save is pending', () => {
  render(<GroupDestinationSummary {...props()} busy />, { wrapper })
  expect((screen.getByRole('button', { name: `${P}edit` }) as HTMLButtonElement).disabled).toBe(true)
})
