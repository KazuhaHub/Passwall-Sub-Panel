// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, editRow, installReads, list, mount, node } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"tls","tlsSettings":{"serverName":"example.test"}}', sniffing: '{}', allocate: '' }
const localInbound = { ...inbound, stream_settings: '{"network":"tcp","security":"none"}' }

function expectNoEditableInbound(dialog: HTMLElement) {
  expect(dialog.querySelector('#edit-inbound-form')).toBeNull()
  expect(within(dialog).queryByRole('button', { name: 'common:actions.ok' })).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
  expect(api.post).not.toHaveBeenCalled()
}

it('uses the fresh detail node when opening the inbound editor', async () => {
  const freshNode = { ...node, flow: 'xtls-rprx-vision', cert_source: 'psp_managed', cert_id: 7 }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node: freshNode, inbound, clients: [] },
    '/admin/certs': { certs: [{ id: 7, name: 'managed', domains: ['example.test'], status: 'active' }] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  await waitFor(() => expect(within(dialog).getByDisplayValue('psp_managed')).toBeTruthy())
  expect(within(dialog).getByDisplayValue('xtls-rprx-vision')).toBeTruthy()
})

it('reopens node metadata from the update response without reloading the stale list', async () => {
  const saved = { ...node, display_name: 'new-name' }
  installReads({ '/admin/nodes': list([node]) })
  api.put.mockResolvedValueOnce({ data: saved })
  mount(<NodesView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
})

it('shows desired and observed endpoints separately when they drift', async () => {
  const drifted = {
    ...node,
    observed_protocol: 'trojan',
    observed_port: 8443,
    endpoint_in_sync: false,
  }
  installReads({ '/admin/nodes': list([drifted]) })
  mount(<NodesView />)

  expect(await screen.findByText(/vless:443/)).toBeTruthy()
  expect(await screen.findByText(/trojan:8443/)).toBeTruthy()
})

it.each([
  ['missing configuration', { node, clients: [] }],
  ['an upstream read error', { node, clients: [], inbound_error: 'agent is unavailable' }],
  ['malformed saved JSON', { node, clients: [], inbound: { ...inbound, settings: '{' } }],
  ['a non-object saved JSON', { node, clients: [], inbound: { ...inbound, stream_settings: '[]' } }],
  ['a missing protocol', { node, clients: [], inbound: { ...inbound, protocol: undefined } }],
  ['a blank protocol', { node, clients: [], inbound: { ...inbound, protocol: '' } }],
  ['a whitespace-only protocol', { node, clients: [], inbound: { ...inbound, protocol: '   ' } }],
  ['a missing port', { node, clients: [], inbound: { ...inbound, port: undefined } }],
  ['a zero port', { node, clients: [], inbound: { ...inbound, port: 0 } }],
  ['an out-of-range port', { node, clients: [], inbound: { ...inbound, port: 65536 } }],
  ['a non-number port', { node, clients: [], inbound: { ...inbound, port: '443' } }],
  ['a fractional port', { node, clients: [], inbound: { ...inbound, port: 443.5 } }],
])('shows a retryable read failure rather than unsupported protocol for %s', async (_, detail) => {
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1': detail })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByRole('alert')).toBeTruthy()
  expect(within(dialog).getByText('admin:nodes.edit_inbound_dialog.load_next')).toBeTruthy()
  expect(within(dialog).queryByText('admin:nodes.edit_inbound_dialog.unsupported')).toBeNull()
  expectNoEditableInbound(dialog)

  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: localInbound, clients: [] } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.retry' }))
  await waitFor(() => expect(dialog.querySelector('#edit-inbound-form')).not.toBeNull())
  expect(within(dialog).queryByRole('alert')).toBeNull()
  expect(within(dialog).getByDisplayValue('vless')).toBeTruthy()
  expect(api.get.mock.calls.filter(([url]) => url === '/admin/nodes/1')).toHaveLength(2)
  expect(api.put).not.toHaveBeenCalled()
})

it('keeps a transport failure open for retry without exposing defaults', async () => {
  installReads({ '/admin/nodes': list([node]) })
  const read = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string) => url === '/admin/nodes/1'
    ? Promise.reject(new Error('network unavailable')) : read(url))
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  await within(dialog).findByRole('alert')
  expectNoEditableInbound(dialog)

  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: localInbound, clients: [] } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.retry' }))
  await waitFor(() => expect(dialog.querySelector('#edit-inbound-form')).not.toBeNull())
  expect(within(dialog).getByDisplayValue('443')).toBeTruthy()
})

it('accepts empty JSON strings as empty objects in a saved VLESS target', async () => {
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: { ...localInbound, settings: '', stream_settings: '', sniffing: '' }, clients: [] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByDisplayValue('vless')).toBeTruthy()
  expect(within(dialog).queryByRole('alert')).toBeNull()
  expect(within(dialog).getByRole('button', { name: 'common:actions.ok' })).toBeTruthy()
  expect(api.put).not.toHaveBeenCalled()
})

it.each(['pending', 'drift', 'failed'])('edits a valid local VLESS target while %s without claiming runtime health', async config_sync_state => {
  const target = { ...node, config_sync_state, flow: 'xtls-rprx-vision' }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node: target, inbound: localInbound, inbound_error: 'agent unavailable; saved target returned', clients: [] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByText('admin:nodes.edit_inbound_dialog.sync_notice')).toBeTruthy()
  expect(within(dialog).getByDisplayValue('vless')).toBeTruthy()
  expect(within(dialog).getByDisplayValue('xtls-rprx-vision')).toBeTruthy()
  expect(within(dialog).queryByRole('alert')).toBeNull()
  fireEvent.change(within(dialog).getByDisplayValue('443'), { target: { value: '8443' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(api.put).toHaveBeenCalledWith('/admin/nodes/1/inbound', expect.objectContaining({ protocol: 'vless', port: 8443 }))
  expect(api.post).not.toHaveBeenCalled()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('rejects an unknown protocol as truly unsupported with no retry or save', async () => {
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: { ...inbound, protocol: 'future-protocol' }, clients: [] } })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByText('admin:nodes.edit_inbound_dialog.unsupported')).toBeTruthy()
  expect(within(dialog).queryByRole('alert')).toBeNull()
  expect(within(dialog).queryByRole('button', { name: 'common:actions.retry' })).toBeNull()
  expectNoEditableInbound(dialog)
})

it.each(['aes-256-gcm', '2022-future-unsupported'])('does not convert unsupported Shadowsocks method %s to VLESS', async method => {
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: { ...localInbound, protocol: 'shadowsocks', settings: JSON.stringify({ method, password: 'original-secret' }) }, clients: [] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByText('admin:nodes.edit_inbound_dialog.unsupported_ss_method')).toBeTruthy()
  expect(within(dialog).queryByDisplayValue('vless')).toBeNull()
  expectNoEditableInbound(dialog)
})

it.each(['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'])('preserves supported Shadowsocks method %s and its original password on save', async method => {
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: { ...localInbound, protocol: 'shadowsocks', settings: JSON.stringify({ method, password: 'original-secret' }) }, clients: [] },
  })
  mount(<NodesView />)
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByDisplayValue('ss2022')).toBeTruthy()
  expect(within(dialog).getByDisplayValue(method)).toBeTruthy()
  expect(within(dialog).getByDisplayValue('original-secret')).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const [url, saved] = api.put.mock.calls[0]
  expect(url).toBe('/admin/nodes/1/inbound')
  expect(saved.protocol).toBe('shadowsocks')
  expect(JSON.parse(saved.settings)).toEqual(expect.objectContaining({ method, password: 'original-secret' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
})

it('ignores an older read result after closing and reopening the editor', async () => {
  installReads({ '/admin/nodes': list([node]) })
  const read = api.get.getMockImplementation()!
  let resolveFirst!: (value: { data: unknown }) => void
  const first = new Promise<{ data: unknown }>(resolve => { resolveFirst = resolve })
  let reads = 0
  api.get.mockImplementation((url: string) => {
    if (url !== '/admin/nodes/1') return read(url)
    reads += 1
    return reads === 1 ? first : Promise.resolve({ data: { node, inbound: { ...localInbound, port: 8443 }, clients: [] } })
  })
  mount(<NodesView />)
  const firstDialog = await editRow('old-name', 'VpnKeyIcon')
  fireEvent.click(within(firstDialog).getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const dialog = await editRow('old-name', 'VpnKeyIcon')
  expect(await within(dialog).findByDisplayValue('8443')).toBeTruthy()
  await act(async () => {
    resolveFirst({ data: { node, inbound: { ...inbound, protocol: 'future-protocol' }, clients: [] } })
    await first
  })
  expect(within(dialog).getByDisplayValue('8443')).toBeTruthy()
  expect(within(dialog).queryByText('admin:nodes.edit_inbound_dialog.unsupported')).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
})
