// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, Link, RouterProvider } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ConfirmHost from '@/components/ConfirmHost'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { createAppTheme } from '@/theme'
import { api, installReads, list } from '../testHarness'
import { forward, landing } from '../lines/editorHarness'
import NodeDetailRoute from '../NodeDetailRoute'

vi.mock('@/components/feedback/toasts', () => ({ pushToast: vi.fn(), pushSnack: vi.fn() }))

const psp = (id: number, name: string) => ({ id, name, url: `https://s${id}.example`, panel_type: 'psp', capabilities: ['inbound.enable', 'inbound.update', 'inbound.create', 'inbound.delete'] })
const servers = [psp(1, 'Landing server'), psp(2, 'Osaka')]
const inbound = { id: 5, protocol: 'vless', remark: 'x', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"none"}', sniffing: '{}', allocate: '' }
// The source-country rules are a shared section that still draws with MUI, which needs its theme.
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const routes = { supported: true, routes: [], default_exit: { kind: 'local' }, sent: false, running: false, warnings: [] }

function mount(node = landing([forward]), path = '/admin/nodes/1') {
  installReads({
    '/admin/nodes/1': { node, inbound, clients: [] }, '/admin/nodes': list([node]), '/admin/servers': list(servers), '/admin/nodes/order': { order_version: 'v', rows: [] },
    '/admin/nodes/1/routes': routes, '/admin/dest/lists': { items: [], refresh_hours: 24, budget: {} }, '/admin/certs': { certs: [] }, '/admin/transit/probes': { items: [] },
    '/admin/nodes/1/source-guard': { can_guard: true, lines: [], cloudflare: { cidrs: [], source: 'builtin', builtin_date: '2026-11-18', auto_fetch: true } },
    '/admin/nodes/5': { node: landing([], { id: 5, display_name: 'Parent', relays: [{ id: 'T1', name: 'SH tunnel', address: 'sh.example', port: 31002, enabled: true }] }), clients: [] },
    '/admin/dest/source-rules/1': { rule: { node_id: 1, countries: [], cidrs: [], state: 'off', updated_at: null }, native: true, geoip_available: true },
  })
  const router = createMemoryRouter([
    { path: '/admin/nodes', element: <div>the list <Link to="/admin/nodes/1">again</Link></div> },
    { path: '/admin/nodes/:id', element: <NodeDetailRoute /> },
  ], { initialEntries: [path] })
  render(<QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><ConfirmHost /><RouterProvider router={router} /></ThemeProvider></QueryClientProvider>)
  return router
}

// The back link of the page header (the app shell, which is not mounted here, also has a "Nodes" entry).
const back = () => within(document.querySelector('[data-slot="page-header"]') as HTMLElement).getByRole('button', { name: 'Nodes' })

beforeEach(() => { vi.clearAllMocks() })

describe('node page: tabs and leaving', () => {
  it('keeps the drafts of the tabs when switching between them, and asks once on leaving, naming the dirty tabs', async () => {
    const router = mount()
    fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
    fireEvent.change(await screen.findByLabelText(/Display name/), { target: { value: 'Renamed' } })
    fireEvent.click(screen.getByRole('tab', { name: 'Exit' }))
    fireEvent.click(await screen.findByRole('radio', { name: /Leave through another server/ }))
    // Back to the first tab: nothing is asked, and the draft is still there.
    fireEvent.click(screen.getByRole('tab', { name: 'General' }))
    expect(router.state.location.pathname).toBe('/admin/nodes/1')
    expect((screen.getByLabelText(/Display name/) as HTMLInputElement).value).toBe('Renamed')
    expect(screen.queryByRole('alertdialog')).toBeNull()

    fireEvent.click(back())
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/Exit, General have unsaved changes/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: /Keep editing/ }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(router.state.location.pathname).toBe('/admin/nodes/1')

    fireEvent.click(back())
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: /Discard/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes'))
  })

  it('does not ask when nothing is unsaved', async () => {
    const router = mount()
    await screen.findByRole('tab', { name: 'General' })
    fireEvent.click(back())
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes'))
  })

  it('opens the tab named in the address, so the coverage sheet can link to the Inbound tab', async () => {
    mount(landing([forward]), '/admin/nodes/1?tab=inbound')
    expect(await screen.findByLabelText(/^Port/)).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Inbound' }).getAttribute('aria-selected')).toBe('true')
  })

  it('does not read the other tabs until they are opened', async () => {
    mount()
    await screen.findByRole('tab', { name: 'General' })
    expect(api.get.mock.calls.some(([u]) => u === '/admin/nodes/1/routes')).toBe(false)
    fireEvent.click(screen.getByRole('tab', { name: 'Exit' }))
    await waitFor(() => expect(api.get.mock.calls.some(([u]) => u === '/admin/nodes/1/routes')).toBe(true))
  })
})

describe('node page: tunnel listener', () => {
  const child = landing([], { id: 1, display_name: 'tunnel-1', parent_node_id: 5, parent_line_id: 'T1', parent_node_name: 'Parent' })

  it('has only the Inbound and Exit tabs, names its line, and offers no node actions', async () => {
    mount(child)
    const banner = await screen.findByTestId('listener-banner')
    await waitFor(() => expect(banner.textContent).toMatch(/tunnel line SH tunnel of Parent/))
    expect(screen.getAllByRole('tab').map(t => t.textContent)).toEqual(['Exit', 'Inbound'])
    // It opens on the tab that is left of the usual four.
    expect(screen.getByRole('tab', { name: 'Inbound' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.queryByRole('button', { name: /Disable node|Enable node/ })).toBeNull()
    expect(screen.getByRole('link', { name: /line/i })).toBeTruthy()
  })

  it('shows only the routes of the listener on the Exit tab, with the note that they are its own', async () => {
    mount(child, '/admin/nodes/1?tab=exit')
    expect(await screen.findByText(/Routes by destination are set per listener/)).toBeTruthy()
    expect(screen.queryByRole('radio', { name: /Leave through another server/ })).toBeNull()
  })
})

describe('node page: delete', () => {
  it('opens the same impact dialog as the list, and goes back to the list once the node is gone', async () => {
    const node = landing([forward])
    const router = mount(node)
    // The impact read the dialog needs, and what the detail read answers once the node is deleted.
    const base = api.get.getMockImplementation()!
    let gone = false
    api.get.mockImplementation(async (url: string, ...rest: unknown[]) => {
      if (url === '/admin/nodes/1/delete-impact') return { data: { node: { id: 1, name: 'Landing' }, tunnels: [], blocked_by_routes: [], lines: [{ node_id: 1, line_id: 'direct', landing_name: 'Landing', line_name: 'Landing', line_type: 'direct' }] } }
      if (gone && url === '/admin/nodes/1') throw Object.assign(new Error('nf'), { response: { status: 404 } })
      return base(url, ...rest)
    })
    api.delete.mockImplementation(async () => { gone = true; return { data: {} } })
    await screen.findByRole('tab', { name: 'General' })
    const header = document.querySelector('[data-slot="page-header"]') as HTMLElement
    const buttons = within(header).getAllByRole('button')
    fireEvent.click(buttons[buttons.length - 1])
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete node' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(await within(dialog).findByRole('button', { name: /^Delete node and 1 line/ }))
    await waitFor(() => expect(api.delete).toHaveBeenCalled())
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes'))
  })

  it('goes to the list even when a tab has unsaved edits, once the node it was asked to delete is gone', async () => {
    const router = mount(landing([forward]))
    const base = api.get.getMockImplementation()!
    let gone = false
    api.get.mockImplementation(async (url: string, ...rest: unknown[]) => {
      if (url === '/admin/nodes/1/delete-impact') return { data: { node: { id: 1, name: 'Landing' }, tunnels: [], blocked_by_routes: [], lines: [{ node_id: 1, line_id: 'direct', landing_name: 'Landing', line_name: 'Landing', line_type: 'direct' }] } }
      if (gone && url === '/admin/nodes/1') throw Object.assign(new Error('nf'), { response: { status: 404 } })
      return base(url, ...rest)
    })
    api.delete.mockImplementation(async () => { gone = true; return { data: {} } })
    fireEvent.click(await screen.findByRole('tab', { name: 'General' }))
    fireEvent.change(await screen.findByLabelText(/Display name/), { target: { value: 'Unsaved' } })
    const header = document.querySelector('[data-slot="page-header"]') as HTMLElement
    const buttons = within(header).getAllByRole('button')
    fireEvent.click(buttons[buttons.length - 1])
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Delete node' }))
    fireEvent.click(await within(await screen.findByRole('alertdialog')).findByRole('button', { name: /^Delete node and 1 line/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes'))
    expect(screen.queryByText(/have unsaved changes/)).toBeNull()
  })
})
