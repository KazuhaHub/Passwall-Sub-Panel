// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, installReads, list, mount, node, server } from '@/test/adminSaveHarness'
import { LegacyProbe, openInboundEditor, openMeta } from './legacyHarness'

const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"tls","tlsSettings":{"serverName":"example.test"}}', sniffing: '{}', allocate: '' }
const localInbound = { ...inbound, stream_settings: '{"network":"tcp","security":"none"}' }
const realityInbound = {
  ...inbound,
  stream_settings: JSON.stringify({
    network: 'tcp', security: 'reality',
    realitySettings: {
      target: 'example.com:443', serverNames: ['example.com'], privateKey: 'private', shortIds: ['abcd'],
      settings: { publicKey: 'public', fingerprint: 'firefox', spiderX: '/' },
    },
  }),
}

function expectNoEditableInbound(dialog: HTMLElement) {
  expect(dialog.querySelector('#edit-inbound-form')).toBeNull()
  expect(within(dialog).queryByRole('button', { name: 'common:actions.ok' })).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
  expect(api.post).not.toHaveBeenCalled()
}

it('forces and locks the REALITY fingerprint for Xray 26.9.8 or newer', async () => {
  installReads({
    '/admin/servers': list([{ ...server, xray_version: '26.9.9' }]),
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: realityInbound, clients: [] },
  })
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
  const fingerprint = await within(dialog).findByLabelText('admin:nodes.create_dialog.reality_fingerprint')
  await waitFor(() => expect(fingerprint.getAttribute('aria-disabled')).toBe('true'))
  expect(within(dialog).getByText('chrome')).toBeTruthy()
  expect(within(dialog).getByText('admin:nodes.create_dialog.reality_fingerprint_mlkem_locked')).toBeTruthy()
})

it('uses the fresh detail node when opening the inbound editor', async () => {
  const freshNode = { ...node, flow: 'xtls-rprx-vision', cert_source: 'psp_managed', cert_id: 7 }
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node: freshNode, inbound, clients: [] },
    '/admin/certs': { certs: [{ id: 7, name: 'managed', domains: ['example.test'], status: 'active' }] },
  })
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
  await waitFor(() => expect(within(dialog).getByDisplayValue('psp_managed')).toBeTruthy())
  expect(within(dialog).getByDisplayValue('xtls-rprx-vision')).toBeTruthy()
})

it('reopens node metadata from the update response without reloading the stale list', async () => {
  const saved = { ...node, display_name: 'new-name' }
  installReads({ '/admin/nodes': list([node]) })
  // The save's own response is what the list learns; the refetch the save triggers then reads the same.
  api.put.mockImplementationOnce(async () => { installReads({ '/admin/nodes': list([saved]) }); return { data: saved } })
  mount(<LegacyProbe />)
  const dialog = await openMeta()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  // Positions are written only by PUT /nodes/order: an explicit sort_order here would put a dragged node back.
  expect(api.put.mock.calls[0][1]).not.toHaveProperty('sort_order')
  const reopened = await openMeta()
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
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
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
  expect(await within(dialog).findByRole('alert')).toBeTruthy()
  expect(within(dialog).getByText('admin:nodes.edit_inbound_dialog.load_next')).toBeTruthy()
  expect(within(dialog).queryByText('admin:nodes.edit_inbound_dialog.unsupported')).toBeNull()
  expectNoEditableInbound(dialog)

  const reads = () => api.get.mock.calls.filter(([url]) => url === '/admin/nodes/1').length
  const before = reads()
  installReads({ '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: localInbound, clients: [] } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.retry' }))
  await waitFor(() => expect(dialog.querySelector('#edit-inbound-form')).not.toBeNull())
  expect(within(dialog).queryByRole('alert')).toBeNull()
  expect(within(dialog).getByDisplayValue('vless')).toBeTruthy()
  // One more read for the retry (the dialog loads on mount, which StrictMode does twice).
  expect(reads()).toBe(before + 1)
  expect(api.put).not.toHaveBeenCalled()
})

it('keeps a transport failure open for retry without exposing defaults', async () => {
  installReads({ '/admin/nodes': list([node]) })
  const read = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string) => url === '/admin/nodes/1'
    ? Promise.reject(new Error('network unavailable')) : read(url))
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
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
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
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
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
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
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
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
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
  expect(await within(dialog).findByText('admin:nodes.edit_inbound_dialog.unsupported_ss_method')).toBeTruthy()
  expect(within(dialog).queryByDisplayValue('vless')).toBeNull()
  expectNoEditableInbound(dialog)
})

it.each(['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'])('preserves supported Shadowsocks method %s and its original password on save', async method => {
  installReads({
    '/admin/nodes': list([node]),
    '/admin/nodes/1': { node, inbound: { ...localInbound, protocol: 'shadowsocks', settings: JSON.stringify({ method, password: 'original-secret' }) }, clients: [] },
  })
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
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
  mount(<LegacyProbe />)
  const firstDialog = await openInboundEditor()
  fireEvent.click(within(firstDialog).getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const dialog = await openInboundEditor()
  expect(await within(dialog).findByDisplayValue('8443')).toBeTruthy()
  await act(async () => {
    resolveFirst({ data: { node, inbound: { ...inbound, protocol: 'future-protocol' }, clients: [] } })
    await first
  })
  expect(within(dialog).getByDisplayValue('8443')).toBeTruthy()
  expect(within(dialog).queryByText('admin:nodes.edit_inbound_dialog.unsupported')).toBeNull()
  expect(api.put).not.toHaveBeenCalled()
})

it('does not send sort_order when the inbound editor persists a changed VLESS flow', async () => {
  const flowNode = { ...node, flow: '', sort_order: 30 }
  installReads({ '/admin/nodes': list([flowNode]), '/admin/nodes/1': { node: flowNode, inbound: localInbound, clients: [] } })
  mount(<LegacyProbe />)
  const dialog = await openInboundEditor()
  await within(dialog).findByDisplayValue('vless')
  const flowSelect = within(within(dialog).getByText('admin:nodes.create_dialog.vless_flow').parentElement as HTMLElement).getByRole('combobox')
  fireEvent.mouseDown(flowSelect)
  fireEvent.click(await screen.findByRole('option', { name: 'xtls-rprx-vision' }))
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
  await waitFor(() => expect(api.put.mock.calls.some(([url]) => url === '/admin/nodes/1/metadata')).toBe(true))
  const body = api.put.mock.calls.find(([url]) => url === '/admin/nodes/1/metadata')![1]
  expect(body).not.toHaveProperty('sort_order')
  expect(body.flow).toBe('xtls-rprx-vision')
})
