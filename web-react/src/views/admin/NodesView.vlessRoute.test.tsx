// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'
import type { RouteLinesView } from '@/api/vlessRoute'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const initial = (): RouteLinesView => ({ node_id: 1, supported: true, state: { revision: 0, lines: [], status: '' }, warnings: [] })

it('keeps relay and route tabs separate and saves routes with the existing node Save button', async () => {
  const view = initial()
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1/vless-route-lines': view,
    '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }, { tag: 'remote', protocol: '', source: 'subscription' }] },
  })
  confirmation.mockResolvedValue(true)
  api.put.mockImplementation(async (url, body) => {
    if (String(url).endsWith('/vless-route-lines')) {
      Object.assign(view, { state: { revision: 2, status: 'applied', lines: [{ ...body.lines[0], id: 'stable-route', route: 1001 }] } })
      return { data: view }
    }
    return { data: { ...node, display_name: body.display_name } }
  })
  mount(<NodesView />)
  const dialog = await editRow()
  expect(within(dialog).getByRole('tab', { name: 'admin:nodes.relay.title' })).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const add = await within(dialog).findByRole('button', { name: 'admin:nodes.vless_route.add' })
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(add)
  fireEvent.change(within(dialog).getByLabelText(/admin:nodes.vless_route.name/), { target: { value: 'B' } })
  fireEvent.mouseDown(within(dialog).getByLabelText(/admin:nodes.vless_route.outbound/))
  expect(await screen.findByRole('option', { name: 'direct [freedom]' })).toBeTruthy()
  expect(screen.queryByRole('option', { name: 'admin:nodes.vless_route.select_outbound' })).toBeNull()
  expect(screen.getByRole('option', { name: 'remote [admin:nodes.vless_route.unknown_protocol]' })).toBeTruthy()
  expect(screen.getAllByRole('option')).toHaveLength(2)
  fireEvent.click(screen.getByRole('option', { name: 'direct [freedom]' }))
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(confirmation).not.toHaveBeenCalled()
  const routeWrites = api.put.mock.calls.filter(([url]) => String(url).endsWith('/vless-route-lines'))
  expect(routeWrites).toHaveLength(1)
  expect(routeWrites[0][1]).toEqual({ revision: 0, lines: [{ name: 'B', outbound_tag: 'direct', enabled: true }] })
  const reopened = await editRow()
  fireEvent.click(within(reopened).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  expect(await within(reopened).findByDisplayValue('B')).toBeTruthy()
  expect(within(reopened).queryByText('route 1001')).toBeNull()
  expect(within(reopened).queryByText('admin:nodes.vless_route.state_applied')).toBeNull()
})

it('keeps the editor open after metadata succeeds but route application is unknown', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'unknown', error: 'remote_update_unconfirmed', lines: [{ id: 'route', route: 1001, name: 'B', outbound_tag: 'direct', enabled: true }] }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1/vless-route-lines': view,
    '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] },
  })
  confirmation.mockResolvedValue(true)
  api.put.mockImplementation(async url => ({ data: String(url).endsWith('/vless-route-lines') ? view : node }))
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByDisplayValue('B')
  expect(within(dialog).getByRole('alert').textContent).toContain('admin:nodes.vless_route.state_unknown')
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(api.put.mock.calls.some(([url]) => String(url).endsWith('/vless-route-lines'))).toBe(true))
  expect(screen.getByRole('dialog')).toBeTruthy()
})

it('keeps enable, delete and reorder controls working in the relay-style route layout', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'applied', lines: [
    { id: 'one', route: 1, name: 'One', outbound_tag: 'direct', enabled: true },
    { id: 'two', route: 2, name: 'Two', outbound_tag: 'direct', enabled: true },
  ] }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1/vless-route-lines': view,
    '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] },
  })
  confirmation.mockResolvedValue(true)
  api.put.mockImplementation(async (url, body) => ({ data: String(url).endsWith('/vless-route-lines')
    ? { ...view, state: { revision: 4, status: 'applied', lines: body.lines } }
    : node }))
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByDisplayValue('One')
  fireEvent.click(within(dialog).getAllByRole('button', { name: 'admin:rules.members.move_down' })[0])
  expect(within(dialog).getAllByLabelText(/admin:nodes.vless_route.name/).map(input => (input as HTMLInputElement).value)).toEqual(['Two', 'One'])
  fireEvent.click(within(dialog).getAllByRole('switch')[0])
  fireEvent.click(within(dialog).getAllByRole('button', { name: 'admin:nodes.relay.remove' })[1])
  expect(within(dialog).queryByDisplayValue('One')).toBeNull()
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const write = api.put.mock.calls.find(([url]) => String(url).endsWith('/vless-route-lines'))
  expect(write?.[1]).toEqual({ revision: 2, lines: [{ id: 'two', name: 'Two', outbound_tag: 'direct', enabled: false }] })
})
