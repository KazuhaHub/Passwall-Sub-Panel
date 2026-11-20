// @vitest-environment jsdom
import { StrictMode } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { api, installReads, list, node, server } from '@/test/adminSaveHarness'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { createAppTheme } from '@/theme'
import NodeDetailRoute from './NodeDetailRoute'
import NodesPage, { legacyRedirect } from './NodesPage'

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"none"}', sniffing: '{}', allocate: '' }
const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })
const dest = {
  '/admin/dest/source-rules/1': { rule: { node_id: 1, countries: [], cidrs: [], state: 'off', updated_at: null }, native: true, geoip_available: true },
  '/admin/dest/source-rules': { items: [], geoip_available: true },
  '/admin/dest/policies': { allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} },
  '/admin/dest/status': { nodes: [], policy_notes: {}, totals: {}, apply_eta_ms: 0 },
}

describe('legacy query strings', () => {
  it('sends ?inbound=<id> to the node page, the old tab names to their new homes, and leaves the rest alone', () => {
    expect(legacyRedirect('?inbound=12', true)).toBe('/admin/nodes/12?tab=inbound')
    expect(legacyRedirect('?tab=lines', true)).toBe('?view=node')
    expect(legacyRedirect('?tab=managed&q=hk', true)).toBe('?q=hk')
    expect(legacyRedirect('?tab=unmanaged', true)).toBeNull()
    expect(legacyRedirect('?view=node', true)).toBeNull()
  })
  it('an operator has no node page: the inbound parameter only goes away', () => {
    expect(legacyRedirect('?inbound=12&q=a', false)).toBe('?q=a')
  })
})

// The coverage sheet's "edit inbound" lands on /admin/nodes?inbound=<id>: it now continues to the node's page,
// whose Inbound tab shows the form.
const mountAt = (path: string, panelType = 'psp', role: 'admin' | 'operator' = 'admin') => {
  installReads({
    '/admin/servers': list([{ ...server, panel_type: panelType }]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound, clients: [] },
    '/admin/nodes/order': { order_version: 'v1', rows: [] }, '/admin/nodes/separator': list([]), ...dest,
  })
  useAuthStore.setState({ role })
  const router = createMemoryRouter([
    { path: '/admin/nodes', element: <NodesPage /> },
    { path: '/admin/nodes/:id', element: <NodeDetailRoute /> },
  ], { initialEntries: [path] })
  render(<StrictMode><QueryClientProvider client={makeTestQueryClient()}><ThemeProvider theme={theme}><RouterProvider router={router} /></ThemeProvider></QueryClientProvider></StrictMode>)
  return router
}

it('opens the inbound editor for ?inbound=<id> through the node page', async () => {
  const router = mountAt('/admin/nodes?inbound=1')
  expect(await screen.findByLabelText(/create_dialog.port/)).toBeTruthy()
  expect(screen.getByRole('tab', { name: /tabs.inbound/ }).getAttribute('aria-selected')).toBe('true')
  expect(router.state.location.pathname).toBe('/admin/nodes/1')
  expect(router.state.location.search).toBe('?tab=inbound')
})

it('says so when the node does not exist and opens nothing', async () => {
  const router = mountAt('/admin/nodes?inbound=99')
  api.get.mockImplementation(async (url: string) => { throw Object.assign(new Error('not found'), { url }) })
  await waitFor(() => expect(router.state.location.pathname).toBe('/admin/nodes/99'))
  expect(screen.queryByLabelText(/create_dialog.port/)).toBeNull()
})

// The source-country section is only for a native (psp) panel's inbound, and is a separate save from the node form.
it('shows the deny-source section in the inbound editor of a psp panel only', async () => {
  mountAt('/admin/nodes/1?tab=inbound')
  expect(await screen.findByTestId('source-rules-section')).toBeTruthy()
})

it('does not show it for a 3X-UI panel', async () => {
  mountAt('/admin/nodes/1?tab=inbound', '3xui')
  // The section is decided in the same render that shows the form, so the form being there is the signal to look.
  await screen.findByLabelText(/create_dialog.port/)
  expect(screen.queryByTestId('source-rules-section')).toBeNull()
  // The section shows a skeleton while its read is pending, so its absence alone could be that; the read itself is the proof.
  expect(api.get.mock.calls.filter(([url]) => String(url).includes('/admin/dest/source-rules/1'))).toEqual([])
})

// An operator reads the nodes page but has no node page and cannot edit an inbound: the deep link does nothing
// for them (and the param is still dropped), and the section is never offered.
it('an operator following ?inbound= gets the list, no editor and no source section, and the param goes', async () => {
  const router = mountAt('/admin/nodes?inbound=1', 'psp', 'operator')
  await waitFor(() => expect(router.state.location.search).toBe(''))
  expect(router.state.location.pathname).toBe('/admin/nodes')
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(screen.queryByTestId('source-rules-section')).toBeNull()
})
