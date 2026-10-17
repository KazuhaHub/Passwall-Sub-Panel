// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'
import type { RouteLinesView } from '@/api/vlessRoute'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const initial = (): RouteLinesView => ({ node_id: 1, supported: true, state: { revision: 0, lines: [], status: '' }, warnings: [] })

it('saves and reopens the five optional fields on the existing route without adding a rule or outbound', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'applied', lines: [{ id: 'line', route: 1, name: 'B', outbound_tag: 'direct', enabled: true }] }
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] } })
  api.put.mockImplementation(async (url, body) => {
    if (String(url).endsWith('/vless-route-lines')) {
      view.state = { revision: view.state.revision + 2, status: 'applied', lines: body.lines.map((line: object) => ({ ...line, route: 1 })) }
      return { data: view }
    }
    return { data: node }
  })
  mount(<NodesView />)
  let dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const accordion = await within(dialog).findByRole('button', { name: 'admin:nodes.vless_route.match.title' })
  expect(accordion.getAttribute('aria-expanded')).toBe('false')
  fireEvent.click(accordion)
  const fields = within(dialog).getByRole('group', { name: 'admin:nodes.vless_route.match.title' })
  expect(within(fields).getAllByRole('combobox')).toHaveLength(2)
  expect(within(fields).getAllByRole('textbox')).toHaveLength(3)
  expect(within(fields).queryByRole('button')).toBeNull()
  expect(fields.querySelector('.MuiFormHelperText-root')).toBeNull()
  expect(within(dialog).queryByText('admin:nodes.vless_route.match.hint')).toBeNull()
  const entryControl = within(dialog).getByRole('combobox', { name: 'admin:nodes.vless_route.entry' }).closest('.MuiFormControl-root')
  const outboundControl = within(dialog).getByRole('combobox', { name: /admin:nodes.vless_route.outbound/ }).closest('.MuiFormControl-root')
  expect(entryControl?.parentElement).toBe(outboundControl?.parentElement)
  fireEvent.mouseDown(within(fields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.network' }))
  fireEvent.click(await screen.findByRole('option', { name: 'TCP' }))
  fireEvent.mouseDown(within(fields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.protocols' }))
  fireEvent.click(await screen.findByRole('option', { name: 'tls' }))
  fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' })
  fireEvent.change(within(fields).getByLabelText('admin:nodes.vless_route.match.ip'), { target: { value: '192.0.2.0/24\ngeoip:private' } })
  fireEvent.change(within(fields).getByLabelText('admin:nodes.vless_route.match.domain'), { target: { value: 'domain:example.com\nregexp:^a{1,3}\\.example$' } })
  fireEvent.change(within(fields).getByLabelText('admin:nodes.vless_route.match.port'), { target: { value: '443,1000-2000' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const first = api.put.mock.calls.find(([url]) => String(url).endsWith('/vless-route-lines'))![1].lines
  expect(first).toHaveLength(1)
  expect(first[0]).toEqual({ id: 'line', name: 'B', enabled: true, only_line_name: false, entry_relay_id: '', outbound_tag: 'direct', network: 'tcp', protocols: ['tls'], ip: ['192.0.2.0/24', 'geoip:private'], domain: ['domain:example.com', 'regexp:^a{1,3}\\.example$'], port: '443,1000-2000' })
  dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const reopened = await within(dialog).findByRole('button', { name: 'admin:nodes.vless_route.match.title' })
  expect(reopened.getAttribute('aria-expanded')).toBe('false')
  fireEvent.click(reopened)
  const savedFields = within(dialog).getByRole('group', { name: 'admin:nodes.vless_route.match.title' })
  expect(within(savedFields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.network' }).textContent).toBe('TCP')
  expect(within(savedFields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.protocols' }).textContent).toBe('tls')
  expect(within(savedFields).getByLabelText('admin:nodes.vless_route.match.ip')).toHaveProperty('value', '192.0.2.0/24\ngeoip:private')
  expect(within(savedFields).getByLabelText('admin:nodes.vless_route.match.domain')).toHaveProperty('value', 'domain:example.com\nregexp:^a{1,3}\\.example$')
  expect(within(savedFields).getByDisplayValue('443,1000-2000')).toBeTruthy()
})

it('clears all five saved optional conditions without changing the route identity or outbound', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'applied', lines: [{ id: 'line', route: 1, name: 'B', outbound_tag: 'direct', enabled: true, network: 'tcp', protocols: ['tls'], ip: ['192.0.2.0/24', 'geoip:private'], domain: ['domain:example.com', 'regexp:^a{1,3}\\.example$'], port: '443,1000-2000' }] }
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] } })
  api.put.mockImplementation(async (url, body) => ({ data: String(url).endsWith('/vless-route-lines') ? { ...view, state: { ...view.state, revision: 4, lines: body.lines.map((line: object) => ({ ...line, route: 1 })) } } : node }))
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  fireEvent.click(await within(dialog).findByRole('button', { name: 'admin:nodes.vless_route.match.title' }))
  const fields = within(dialog).getByRole('group', { name: 'admin:nodes.vless_route.match.title' })
  fireEvent.mouseDown(within(fields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.network' }))
  fireEvent.click(await screen.findByRole('option', { name: 'admin:nodes.vless_route.match.any' }))
  fireEvent.mouseDown(within(fields).getByRole('combobox', { name: 'admin:nodes.vless_route.match.protocols' }))
  fireEvent.click(await screen.findByRole('option', { name: 'tls' }))
  fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' })
  for (const field of ['ip', 'domain', 'port']) fireEvent.change(within(fields).getByLabelText(`admin:nodes.vless_route.match.${field}`), { target: { value: '' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const last = api.put.mock.calls.filter(([url]) => String(url).endsWith('/vless-route-lines')).at(-1)![1].lines[0]
  expect(last).toEqual({ id: 'line', name: 'B', enabled: true, only_line_name: false, entry_relay_id: '', outbound_tag: 'direct' })
  for (const key of ['network', 'protocols', 'ip', 'domain', 'port']) expect(last[key]).toBeUndefined()
})

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
  const onlyLineName = within(dialog).getByRole('switch', { name: 'admin:nodes.relay.only_line_name' }) as HTMLInputElement
  expect(onlyLineName.checked).toBe(false)
  fireEvent.click(onlyLineName)
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(confirmation).not.toHaveBeenCalled()
  const routeWrites = api.put.mock.calls.filter(([url]) => String(url).endsWith('/vless-route-lines'))
  expect(routeWrites).toHaveLength(1)
  expect(routeWrites[0][1]).toEqual({ revision: 0, lines: [{ name: 'B', only_line_name: true, entry_relay_id: '', outbound_tag: 'direct', enabled: true }] })
  const reopened = await editRow()
  fireEvent.click(within(reopened).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  expect(await within(reopened).findByDisplayValue('B')).toBeTruthy()
  expect((within(reopened).getByRole('switch', { name: 'admin:nodes.relay.only_line_name' }) as HTMLInputElement).checked).toBe(true)
  expect(within(reopened).queryByText('route 1001')).toBeNull()
  expect(within(reopened).getByText('admin:nodes.vless_route.state_applied').closest('[role="alert"]')?.className).toMatch(/Success/)
})

it('defaults each legacy relay to false and keeps independent display options after saving and reopening', async () => {
  const legacy = { ...node, relays: [
    { id: 'first', name: 'R1', address: 'first.example', port: 0, enabled: true },
    { id: 'second', name: 'R2', address: 'second.example', port: 0, enabled: true },
  ] }
  installReads({ '/admin/nodes': list([legacy]), '/admin/nodes/1/vless-route-lines': initial(), '/admin/nodes/1/vless-route-outbounds': { items: [] } })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...legacy, ...body } }))
  mount(<NodesView />)
  const dialog = await editRow()
  const toggles = within(dialog).getAllByRole('switch', { name: 'admin:nodes.relay.only_line_name' }) as HTMLInputElement[]
  expect(toggles.map(toggle => toggle.checked)).toEqual([false, false])
  fireEvent.click(toggles[0])
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const body = api.put.mock.calls.find(([url]) => url === '/admin/nodes/1/metadata')![1]
  expect(body.relays.map((line: { only_line_name: boolean }) => line.only_line_name)).toEqual([true, false])
  const reopened = await editRow()
  expect((within(reopened).getAllByRole('switch', { name: 'admin:nodes.relay.only_line_name' }) as HTMLInputElement[]).map(toggle => toggle.checked)).toEqual([true, false])
  fireEvent.click(within(reopened).getByRole('button', { name: 'admin:nodes.relay.add' }))
  expect((within(reopened).getAllByRole('switch', { name: 'admin:nodes.relay.only_line_name' }) as HTMLInputElement[])[2].checked).toBe(false)
})

it('allows blank route names and a saved relay entry while the direct line is hidden', async () => {
  const view = initial()
  const configured = { ...node, hide_direct: true, show_relay_status: false, relays: [
    { id: 'front', name: 'R1', address: 'relay.example', port: 8443, enabled: true },
    { id: 'off', name: 'Off', address: 'off.example', port: 0, enabled: false },
  ] }
  installReads({ '/admin/nodes': list([configured]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] } })
  api.put.mockImplementation(async (url, body) => {
    if (String(url).endsWith('/vless-route-lines')) {
      view.state = { revision: 2, status: 'applied', lines: body.lines.map((line: object) => ({ ...line, id: 'stable', route: 1001 })) }
      return { data: view }
    }
    return { data: { ...configured, ...body } }
  })
  mount(<NodesView />)
  const dialog = await editRow()
  expect(within(dialog).queryByRole('switch', { name: 'admin:nodes.relay.show_relay_status' })).toBeNull()
  expect((within(dialog).getByRole('switch', { name: 'admin:nodes.relay.hide_direct' }) as HTMLInputElement).checked).toBe(true)
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const add = await within(dialog).findByRole('button', { name: 'admin:nodes.vless_route.add' })
  await waitFor(() => expect((add as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(add)
  const name = within(dialog).getByLabelText('admin:nodes.vless_route.name') as HTMLInputElement
  expect(name.required).toBe(false)
  const entry = within(dialog).getByRole('combobox', { name: 'admin:nodes.vless_route.entry' })
  expect(entry.textContent).toBe('admin:nodes.vless_route.entry_direct')
  fireEvent.mouseDown(entry)
  expect((await screen.findByRole('option', { name: 'Off' })).getAttribute('aria-disabled')).toBe('true')
  expect(screen.getByRole('option', { name: 'admin:nodes.vless_route.entry_direct' }).getAttribute('aria-disabled')).toBe('true')
  fireEvent.click(screen.getByRole('option', { name: 'R1' }))
  fireEvent.mouseDown(within(dialog).getByRole('combobox', { name: /admin:nodes.vless_route.outbound/ }))
  fireEvent.click(await screen.findByRole('option', { name: 'direct [freedom]' }))
  expect(name.value).toBe('')
  expect(name.placeholder).toBe('direct')
  expect(within(dialog).queryByText('admin:nodes.vless_route.hidden_direct')).toBeNull()
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const route = api.put.mock.calls.find(([url]) => String(url).endsWith('/vless-route-lines'))![1]
  expect(route.lines[0]).toMatchObject({ name: '', entry_relay_id: 'front', outbound_tag: 'direct' })
  const metadata = api.put.mock.calls.find(([url]) => String(url).endsWith('/metadata'))![1]
  expect(metadata).not.toHaveProperty('show_relay_status')
  const reopened = await editRow()
  fireEvent.click(within(reopened).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const savedEntry = await within(reopened).findByRole('combobox', { name: 'admin:nodes.vless_route.entry' })
  expect(savedEntry.textContent).toBe('R1')
  expect((within(reopened).getByLabelText('admin:nodes.vless_route.name') as HTMLInputElement).value).toBe('')
})

it('shows hidden-direct dependent routes as inactive without losing their saved configuration', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'applied', lines: [{ id: 'route', route: 1001, name: 'B', outbound_tag: 'direct', enabled: true }] }
  const configured = { ...node, hide_direct: true }
  installReads({ '/admin/nodes': list([configured]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] } })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByDisplayValue('B')
  const enabled = within(dialog).getByRole('switch', { name: 'admin:nodes.relay.enabled' }) as HTMLInputElement
  expect(enabled.checked).toBe(false)
  expect(enabled.disabled).toBe(true)
  expect(within(dialog).getByText('admin:nodes.vless_route.entry_unavailable')).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.relay.title' }))
  fireEvent.click(within(dialog).getByRole('switch', { name: 'admin:nodes.relay.hide_direct' }))
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const restored = within(dialog).getByRole('switch', { name: 'admin:nodes.relay.enabled' }) as HTMLInputElement
  expect(restored.checked).toBe(true)
  await waitFor(() => expect(restored.disabled).toBe(false))
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
  expect(within(dialog).getByText('remote_update_unconfirmed').tagName).toBe('CODE')
  expect(within(dialog).queryByText('admin:nodes.vless_route.state_applied')).toBeNull()
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(api.put.mock.calls.some(([url]) => String(url).endsWith('/vless-route-lines'))).toBe(true))
  expect(screen.getByRole('dialog')).toBeTruthy()
})

it.each(['unsupported_or_stopped_core', 'inbound_binding_changed', 'routing_or_outbound_changed', 'unsupported_or_changed_binding', 'routes_withdrawn'])('shows the exact drift identifier %s separately from the status explanation', async error => {
  const view = initial()
  view.state = { revision: 2, status: 'drift', error, lines: [] }
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [] } })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const code = await within(dialog).findByText(error)
  expect(code.tagName).toBe('CODE')
  expect(code.parentElement?.textContent).toBe(`admin:nodes.vless_route.error_code ${error}`)
  expect(within(dialog).getByRole('alert').textContent).toContain('admin:nodes.vless_route.state_drift')
})

it('does not invent an error identifier for an applying state without one', async () => {
  const view = initial()
  view.state = { revision: 1, status: 'applying', lines: [] }
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1/vless-route-lines': view, '/admin/nodes/1/vless-route-outbounds': { items: [] } })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByText('admin:nodes.vless_route.state_applying')
  expect(within(dialog).queryByText('admin:nodes.vless_route.error_code')).toBeNull()
})

it('refreshes state on the refresh button and on tab reentry without discarding an edited name', async () => {
  const view = initial()
  view.state = { revision: 2, status: 'applied', lines: [{ id: 'one', route: 1, name: 'B', outbound_tag: 'direct', enabled: true }] }
  const reads = (latest: RouteLinesView) => installReads({ '/admin/nodes': list([node]), '/admin/nodes/1/vless-route-lines': latest, '/admin/nodes/1/vless-route-outbounds': { items: [{ tag: 'direct', protocol: 'freedom', source: 'template' }] } })
  reads(view)
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  const name = await within(dialog).findByDisplayValue('B')
  fireEvent.change(name, { target: { value: 'edited' } })
  reads({ ...view, state: { ...view.state, revision: 3, status: 'drift', error: 'routing_or_outbound_changed' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.vless_route.refresh' }))
  await within(dialog).findByText('routing_or_outbound_changed')
  expect(within(dialog).getByDisplayValue('edited')).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.relay.title' }))
  reads({ ...view, state: { ...view.state, revision: 4, status: 'unknown', error: 'remote_check_unavailable' } })
  fireEvent.click(within(dialog).getByRole('tab', { name: 'admin:nodes.vless_route.title' }))
  await within(dialog).findByText('remote_check_unavailable')
  expect(within(dialog).getByDisplayValue('edited')).toBeTruthy()
  expect(api.put).not.toHaveBeenCalled()
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
  expect(write?.[1]).toEqual({ revision: 2, lines: [{ id: 'two', name: 'Two', only_line_name: false, entry_relay_id: '', outbound_tag: 'direct', enabled: false }] })
})
