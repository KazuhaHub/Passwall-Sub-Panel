// @vitest-environment jsdom
import { StrictMode } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { useAuthStore } from '@/stores/auth'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { installReads, list, node, server } from '@/test/adminSaveHarness'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { createAppTheme } from '@/theme'
import NodesView from './NodesView'

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"none"}', sniffing: '{}', allocate: '' }
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

// The coverage sheet's "edit inbound" lands on /admin/nodes?inbound=<id>: the
// view opens that node's edit dialog once the list has loaded, then drops the
// param so a reload or Back does not open it again.
it('opens the edit dialog for ?inbound=<id> and removes the param', async () => {
  installReads({ '/admin/servers': list([server]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound, clients: [] } })
  const router = createMemoryRouter([{ path: '/admin/nodes', element: <NodesView /> }], { initialEntries: ['/admin/nodes?inbound=1'] })
  render(<StrictMode><QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider></QueryClientProvider></StrictMode>)
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByText(/admin:nodes.edit_inbound_dialog.title/)).toBeTruthy()
  await waitFor(() => expect(router.state.location.search).toBe(''))
})

it('ignores an id that is not a node and still drops the param', async () => {
  installReads({ '/admin/servers': list([server]), '/admin/nodes': list([node]) })
  const router = createMemoryRouter([{ path: '/admin/nodes', element: <NodesView /> }], { initialEntries: ['/admin/nodes?inbound=99'] })
  render(<StrictMode><QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider></QueryClientProvider></StrictMode>)
  await screen.findByText('old-name')
  await waitFor(() => expect(router.state.location.search).toBe(''))
  expect(screen.queryByRole('dialog')).toBeNull()
})

// The source-country section is only for a native (psp) panel's inbound, and is
// a separate save from the node form.
const dest = {
  '/admin/dest/source-rules/1': { rule: { node_id: 1, countries: [], cidrs: [], state: 'off', updated_at: null }, native: true, geoip_available: true },
  '/admin/dest/source-rules': { items: [], geoip_available: true },
  '/admin/dest/policies': { allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} },
  '/admin/dest/status': { nodes: [], policy_notes: {}, totals: {}, apply_eta_ms: 0 },
}
const mountEdit = (panelType: string, role: 'admin' | 'operator' = 'admin') => {
  installReads({ '/admin/servers': list([{ ...server, panel_type: panelType }]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound, clients: [] }, ...dest })
  useAuthStore.setState({ role })
  const router = createMemoryRouter([{ path: '/admin/nodes', element: <NodesView /> }], { initialEntries: ['/admin/nodes?inbound=1'] })
  render(<StrictMode><QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider></QueryClientProvider></StrictMode>)
  return router
}

it('shows the deny-source section in the inbound dialog of a psp panel only', async () => {
  mountEdit('psp')
  expect(await screen.findByTestId('source-rules-section')).toBeTruthy()
})

it('does not show it for a 3X-UI panel', async () => {
  mountEdit('3xui')
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText(/admin:nodes.create_dialog/, undefined, { timeout: 3000 }).catch(() => undefined)
  expect(screen.queryByTestId('source-rules-section')).toBeNull()
})

// An operator reads the nodes page but cannot edit an inbound or read adminGroup
// access control: the deep link does nothing for them (and the param is still
// dropped), and the section is never offered.
it('an operator following ?inbound= gets no dialog and no source section, and the param goes', async () => {
  const router = mountEdit('psp', 'operator')
  await waitFor(() => expect(router.state.location.search).toBe(''))
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(screen.queryByTestId('source-rules-section')).toBeNull()
})
