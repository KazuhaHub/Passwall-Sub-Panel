// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'
import type { RouteEditorView } from '@/api/vlessRoute'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const editor = (lines: RouteEditorView['state']['lines'] = []): RouteEditorView =>
  ({ node_id: 1, supported: true, outbounds: [{ tag: 'direct', protocol: 'freedom' }], state: { revision: 2, status: 'applied', lines }, warnings: [] })

const visibilityCalls = () => api.post.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/line-visibility')

it('previews each line\'s visible groups from the unsaved draft and flags a line no group sees', async () => {
  const configured = { ...node, region: 'HK', tags: ['edge'], relays: [
    { id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'CN', tags: ['cmcc'] },
    { id: 'sh', name: 'SH', address: 'sh.example', port: 0, enabled: true, region: 'HK', tags: ['edge'] },
  ] }
  installReads({ '/admin/nodes': list([configured]), '/admin/nodes/1/vless-route-editor': editor() })
  api.post.mockImplementation(async (url: string) => String(url).endsWith('/line-visibility')
    ? { data: { groups: [{ id: 1, name: 'VIP' }], node_groups: [1], relays: [[], [1]], routes: [] } }
    : { data: {} })
  mount(<NodesView />)
  const dialog = await editRow()
  await waitFor(() => expect(within(dialog).getAllByTestId('line-visibility')).toHaveLength(2))
  const captions = within(dialog).getAllByTestId('line-visibility').map(el => el.textContent)
  expect(captions).toEqual(['admin:nodes.line_labels.visible_none', 'admin:nodes.line_labels.visible_groups'])
  const body = visibilityCalls().at(-1)![1]
  expect(body).toEqual({ region: 'HK', tags: ['edge'], relays: [{ id: 'gz', region: 'CN', tags: ['cmcc'] }, { id: 'sh', region: 'HK', tags: ['edge'] }], routes: [] })

  // Editing a label re-asks with the draft value, without saving anything.
  const before = visibilityCalls().length
  fireEvent.change(within(dialog).getAllByLabelText('admin:nodes.line_labels.region')[0], { target: { value: 'HK' } })
  await waitFor(() => expect(visibilityCalls().length).toBeGreaterThan(before))
  expect(visibilityCalls().at(-1)![1].relays[0]).toEqual({ id: 'gz', region: 'HK', tags: ['cmcc'] })
  expect(api.put).not.toHaveBeenCalled()
})

it('shows "unknown" rather than "no group" when the preview cannot be read', async () => {
  const configured = { ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'US', tags: [] }] }
  installReads({ '/admin/nodes': list([configured]), '/admin/nodes/1/vless-route-editor': editor() })
  api.post.mockRejectedValue(new Error('down'))
  mount(<NodesView />)
  const dialog = await editRow()
  await waitFor(() => expect(within(dialog).getByTestId('line-visibility').textContent).toBe('admin:nodes.line_labels.visibility_unknown'))
})

it('previews route lines with their entry relay once the routing tab is open', async () => {
  const configured = { ...node, relays: [{ id: 'gz', name: 'GZ', address: 'gz.example', port: 0, enabled: true, region: 'US', tags: [] }] }
  installReads({ '/admin/nodes': list([configured]), '/admin/nodes/1/vless-route-editor': editor([{ id: 'line', route: 1, name: 'B', outbound_tag: 'direct', entry_relay_id: 'gz', enabled: true, region: 'US', tags: ['stream'] }]) })
  api.post.mockImplementation(async () => ({ data: { groups: [{ id: 1, name: 'VIP' }], node_groups: [1], relays: [[1]], routes: [[]] } }))
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByLabelText('admin:nodes.line_labels.region')
  await waitFor(() => expect(visibilityCalls().some(([, body]) => body.routes.length === 1)).toBe(true))
  const body = visibilityCalls().filter(([, b]) => b.routes.length === 1).at(-1)![1]
  expect(body.routes[0]).toEqual({ id: 'line', entry_relay_id: 'gz', region: 'US', tags: ['stream'] })
  // The relay tab stays mounted (hidden) beside the routing tab: the route's
  // caption is the last one.
  await waitFor(() => expect(within(dialog).getAllByTestId('line-visibility').at(-1)!.textContent).toBe('admin:nodes.line_labels.visible_none'))
  expect(screen.queryByText('admin:nodes.line_labels.visibility_unknown')).toBeNull()
})
