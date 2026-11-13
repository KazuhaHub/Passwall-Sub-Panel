// @vitest-environment jsdom
import { StrictMode } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { api, installReads, list, node, server } from '@/test/adminSaveHarness'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { createAppTheme } from '@/theme'
import NodesView from './NodesView'

const confirmMock = vi.hoisted(() => vi.fn(() => Promise.resolve(true)))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock, default: () => null }))

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"none"}', sniffing: '{}', allocate: '' }
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const dest = {
  '/admin/dest/source-rules/1': { rule: { node_id: 1, countries: [], cidrs: [], state: 'off', updated_at: null }, native: true, geoip_available: true },
  '/admin/dest/source-rules': { items: [], geoip_available: true },
  '/admin/dest/policies': { allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} },
  '/admin/dest/status': { nodes: [], policy_notes: {}, totals: {}, apply_eta_ms: 0 },
}

async function mountEdit() {
  installReads({ '/admin/servers': list([{ ...server, panel_type: 'psp' }]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound, clients: [] }, ...dest })
  api.put.mockResolvedValue({ data: { rule: { node_id: 1, countries: [], cidrs: [], state: 'on', updated_at: 5 }, geoip_available: true } })
  const router = createMemoryRouter([{ path: '/admin/nodes', element: <NodesView /> }], { initialEntries: ['/admin/nodes?inbound=1'] })
  render(<StrictMode><QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider></QueryClientProvider></StrictMode>)
  const section = await screen.findByTestId('source-rules-section')
  const dialog = screen.getByRole('dialog')
  await waitFor(() => expect((dialog.querySelector('button[type=submit], form button[type=submit]') ?? dialog.querySelector('[type=submit]')) as HTMLButtonElement | null).toBeTruthy())
  return { section, dialog }
}
const submit = (dialog: HTMLElement) => {
  const b = (dialog.querySelector('button[type=submit]') ?? dialog.parentElement!.querySelector('button[type=submit]')) as HTMLButtonElement
  fireEvent.click(b)
}

beforeEach(() => confirmMock.mockClear())

// The source-country section is its own endpoint with its own [Save]. A dirty
// draft at OK asks the existing "discard unsaved changes?" (S20), nothing else:
// Yes saves the inbound only and drops the draft; No or a dismiss aborts the OK.
const titlesAsked = () => (confirmMock.mock.calls as unknown as [{ title: string }][]).map(c => c[0].title)
const inboundPuts = () => api.put.mock.calls.filter(c => c[0] === '/admin/nodes/1/inbound')
const sourcePuts = () => api.put.mock.calls.filter(c => c[0] === '/admin/dest/source-rules/1')

it('shows an amber hint in the section while the draft is dirty, and not otherwise', async () => {
  const { section } = await mountEdit()
  expect(within(section).queryByText('admin:access_control.source_rules.unsaved.summary')).toBeNull()
  fireEvent.click(within(section).getByRole('switch'))
  expect(within(section).getByText('admin:access_control.source_rules.unsaved.summary')).toBeTruthy()
})

it('a dirty draft at OK asks the discard question; Yes saves the inbound only and drops the draft', async () => {
  const { section, dialog } = await mountEdit()
  fireEvent.click(within(section).getByRole('switch'))
  submit(dialog)
  await waitFor(() => expect(inboundPuts()).toHaveLength(1))
  expect(titlesAsked()).toEqual(['admin:access_control.confirm.discard.title'])
  expect(sourcePuts()).toHaveLength(0)
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('No or a dismiss aborts the OK and leaves the dialog open with both drafts intact', async () => {
  confirmMock.mockImplementationOnce(() => Promise.resolve(false))
  const { section, dialog } = await mountEdit()
  fireEvent.click(within(section).getByRole('switch'))
  submit(dialog)
  await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
  await new Promise(r => setTimeout(r, 30))
  expect(inboundPuts()).toHaveLength(0)
  expect(screen.getByRole('dialog')).toBeTruthy()
  expect((within(section).getByRole('switch') as HTMLInputElement).checked).toBe(true)
  expect(within(section).getByText('admin:access_control.source_rules.unsaved.summary')).toBeTruthy()
})

it('the source [Save] button still saves the draft on its own', async () => {
  const { section } = await mountEdit()
  fireEvent.click(within(section).getByRole('switch'))
  fireEvent.click(within(section).getByRole('button', { name: 'admin:access_control.source_rules.save' }))
  await waitFor(() => expect(sourcePuts()).toHaveLength(1))
  expect(inboundPuts()).toHaveLength(0)
})

it('an unchanged source draft closes after the inbound save without any question', async () => {
  const { dialog } = await mountEdit()
  submit(dialog)
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/nodes/1/inbound', expect.anything()))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(confirmMock).not.toHaveBeenCalled()
})
