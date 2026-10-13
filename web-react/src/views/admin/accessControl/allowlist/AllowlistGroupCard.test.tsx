/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { ThemeProvider } from '@mui/material'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import type { DestinationGroupSummary } from '@/api/destinationGroups'
import type { DestinationListSummary } from '@/api/accessControl'
import AllowlistGroupCard from './AllowlistGroupCard'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, opts?: Record<string, unknown>) => key + (opts && Object.keys(opts.replace ?? opts).length ? ` ${JSON.stringify(opts.replace ?? opts)}` : ''), i18n: { language: 'en-US' } }) }))
const P = 'admin:access_control.allowlist.'
const group: DestinationGroupSummary = { group_id: 7, group_name: 'Guests', mode: 'allowlist', stage: 'trial', list_ids: [20], base_list_id: 10, extra_list_id: 11, updated_at: 1000, stage_changed_at: Date.now() - 2 * 86400000, total_nodes: 3, eligible_nodes: 2, applied_nodes: 1, members: 51, ineligible: [{ panel_id: 9, name: 'Old node', reason: 'xui' }], would_deny_recent: { days: 1, sites: 128, count: 9420 }, denied_24h: 1203 }
const list = (id: number, name: string, count: number): DestinationListSummary => ({ id, name, entry_count: count, regexp_count: 0, kind: 'custom', source_url: '', geosite_category: '', geosite_attrs: '', state: 'ready', last_fetched_at: null, last_error: '', owner_group_id: id === 20 ? 0 : 7, updated_at: 1000, parse_report_summary: null, used_by: [] })
const lists = [list(20, 'Finance', 312), list(10, 'Base', 1), list(11, 'Extra', 0)]
const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>{children}</ThemeProvider>
function props() { return { group, lists, onList: vi.fn(), onReport: vi.fn(), onDenied: vi.fn(), onEdit: vi.fn(), onChangeMode: vi.fn(), onOpenGroup: vi.fn() } }
afterEach(cleanup)

it('keeps the trial day separate from outstanding receipts and uses the server retention window', () => {
  render(<AllowlistGroupCard {...props()} />, { wrapper })
  expect(screen.getByRole('heading', { name: 'Guests' })).toBeTruthy()
  expect(screen.getByTestId('ScienceOutlinedIcon')).toBeTruthy()
  expect(screen.getByTestId('HourglassEmptyIcon')).toBeTruthy()
  expect(screen.getByText(new RegExp(`${P}trial_day`)).textContent).toContain('"days":"3"')
  expect(screen.getByText(new RegExp(`${P}would_deny`)).textContent).toContain('"days":"1"')
  expect(screen.getByText(new RegExp(`${P}would_deny`)).textContent).toContain('9,420')
  expect(screen.getByText(new RegExp(`${P}pending`)).textContent).toContain('"count":"1"')
})
it('opens actual selected and owned lists, reports and mode confirmation through explicit actions', () => {
  const actions = props(); render(<AllowlistGroupCard {...actions} />, { wrapper })
  fireEvent.click(screen.getByRole('button', { name: /Finance/ }))
  expect(actions.onList).toHaveBeenCalledWith(20)
  fireEvent.click(screen.getByRole('button', { name: /Extra/ }))
  expect(actions.onList).toHaveBeenLastCalledWith(11)
  fireEvent.click(screen.getByRole('button', { name: new RegExp(`${P}report`) }))
  expect(actions.onReport).toHaveBeenCalledWith(group)
  fireEvent.click(screen.getByRole('button', { name: new RegExp(`${P}enforce`) }))
  expect(actions.onChangeMode).toHaveBeenCalledWith(group, 'enforce')
})
it('explains exclusions on touch and distinguishes zero eligibility from a download in progress', () => {
  render(<AllowlistGroupCard {...props()} group={{ ...group, total_nodes: 1, eligible_nodes: 0, applied_nodes: 0 }} />, { wrapper })
  expect(screen.getByText(`${P}zero_nodes`)).toBeTruthy()
  expect(screen.queryByRole('alert')).toBeNull()
  expect(screen.queryByTestId('HourglassEmptyIcon')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: new RegExp(`${P}excluded`) }))
  const detail = screen.getByRole('dialog')
  expect(within(detail).getByText(new RegExp(`Old node · ${P}reason.xui`))).toBeTruthy()
})
it('shows enforced denial counts and keeps destructive actions in the named group menu', () => {
  const actions = props(), enforced = { ...group, stage: 'enforce' as const, applied_nodes: 2 }
  render(<AllowlistGroupCard {...actions} group={enforced} />, { wrapper })
  expect(screen.getByTestId('LockOutlinedIcon')).toBeTruthy()
  expect(screen.getByText(new RegExp(`${P}denied`)).textContent).toContain('1,203')
  expect(screen.queryByRole('button', { name: new RegExp(`${P}report`) })).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: `${P}view_denied` }))
  expect(actions.onDenied).toHaveBeenCalledWith(enforced)
  fireEvent.click(screen.getByRole('button', { name: new RegExp(`${P}menu`) }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}close_mode` }))
  expect(actions.onChangeMode).toHaveBeenCalledWith(enforced, 'open')
})
it('shows unfetched list warnings but preserves last-good failed downloads as ready', () => {
  const pending = { ...lists[0], kind: 'remote' as const, state: 'failed' as const, last_fetched_at: null }
  const view = render(<AllowlistGroupCard {...props()} lists={[pending, ...lists.slice(1)]} />, { wrapper })
  expect(screen.getByText(new RegExp(`${P}not_ready`)).textContent).toContain('Finance')
  expect((screen.getByRole('button', { name: `${P}enforce` }) as HTMLButtonElement).disabled).toBe(false)
  view.rerender(<AllowlistGroupCard {...props()} lists={[{ ...pending, last_fetched_at: 1000 }, ...lists.slice(1)]} />)
  expect(screen.queryByText(new RegExp(`${P}not_ready`))).toBeNull()
})
it('never fabricates list counts while list metadata is missing', () => {
  render(<AllowlistGroupCard {...props()} lists={undefined} />, { wrapper })
  expect(screen.getByText(`${P}lists_unknown`)).toBeTruthy()
  expect(screen.queryByRole('button', { name: /Finance/ })).toBeNull()
})
