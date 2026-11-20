// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Node } from '@/api/types'
import { api, installReads, list, mountPage } from '../testHarness'
import { useNodeDetail } from '../lines/useNodeDetail'
import { InboundTab } from './InboundTab'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

const server = { id: 1, name: 'server', url: 'https://panel.example.test', panel_type: '3xui', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
const node = { id: 1, panel_id: 1, inbound_id: 1, panel_name: 'server', display_name: 'old-name', server_address: 'proxy.example.test', region: 'US', tags: [], sort_order: 30, enabled: true, protocol: 'vless', flow: '', relays: [], cert_source: 'manual', cert_id: 0 } as unknown as Node
const inbound = { id: 1, protocol: 'vless', remark: 'old-name', enable: true, port: 443, listen: '', settings: '{"decryption":"none"}', stream_settings: '{"network":"tcp","security":"tls","tlsSettings":{"serverName":"example.test"}}', sniffing: '{}', allocate: '' }
const plain = { ...inbound, stream_settings: '{"network":"tcp","security":"none"}' }
const reality = {
  ...inbound,
  stream_settings: JSON.stringify({ network: 'tcp', security: 'reality', realitySettings: { target: 'example.com:443', serverNames: ['example.com'], privateKey: 'private', shortIds: ['abcd'], settings: { publicKey: 'public', fingerprint: 'firefox', spiderX: '/' } } }),
}

// The page hands the tab the node it reads; the tab's own refetch after a save updates it.
function Live({ onDirtyChange }: { onDirtyChange: (d: boolean) => void }) {
  const q = useNodeDetail(1)
  return <InboundTab node={q.data?.node ?? node} onDirtyChange={onDirtyChange} />
}
function open(detail: unknown, over: { servers?: unknown[]; list?: unknown } = {}) {
  installReads({ '/admin/servers': list(over.servers ?? [server]), '/admin/nodes': list([node]), '/admin/nodes/1': detail, '/admin/certs': { certs: [] } })
  const onDirtyChange = vi.fn()
  mountPage(<Live onDirtyChange={onDirtyChange} />)
  return { onDirtyChange }
}
const form = () => screen.findByRole('button', { name: 'Save' }).catch(() => null)
const inboundPuts = () => api.put.mock.calls.filter(c => c[0] === '/admin/nodes/1/inbound')
const metadataPuts = () => api.put.mock.calls.filter(c => c[0] === '/admin/nodes/1/metadata')
const changePort = async (port: string) => { fireEvent.change(await screen.findByLabelText(/^Port/), { target: { value: port } }) }

beforeEach(() => { toast.mockReset(); api.put.mockResolvedValue({ data: {} }) })
void form

describe('Inbound tab: reading', () => {
  it('forces and locks the REALITY fingerprint for Xray 26.9.8 or newer, and opening is not an edit', async () => {
    const { onDirtyChange } = open({ node, inbound: reality, clients: [] }, { servers: [{ ...server, xray_version: '26.9.9' }] })
    const fingerprint = await screen.findByLabelText('Fingerprint')
    await waitFor(() => expect((fingerprint as HTMLSelectElement).disabled).toBe(true))
    expect((fingerprint as HTMLSelectElement).value).toBe('chrome')
    expect(screen.getByText(/fixed to Chrome/)).toBeTruthy()
    expect(onDirtyChange).not.toHaveBeenCalledWith(true)
  })

  it('uses the fresh detail node: its flow and its managed certificate', async () => {
    open({ node: { ...node, flow: 'xtls-rprx-vision', cert_source: 'psp_managed', cert_id: 7 }, inbound, clients: [] })
    expect(((await screen.findByLabelText('VLESS Flow')) as HTMLSelectElement).value).toBe('xtls-rprx-vision')
    expect((screen.getByLabelText('Certificate source') as HTMLSelectElement).value).toBe('psp_managed')
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
  ])('shows a retryable read failure rather than "unsupported" for %s, with no fields to save over it', async (_, detail) => {
    open(detail)
    const alert = await screen.findByRole('alert')
    expect(within(alert).getByText(/Check this node's saved configuration/)).toBeTruthy()
    expect(screen.queryByText(/This protocol is not supported/i)).toBeNull()
    expect(screen.queryByLabelText(/^Port/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
    installReads({ '/admin/servers': list([server]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: plain, clients: [] }, '/admin/certs': { certs: [] } })
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(((await screen.findByLabelText(/^Port/)) as HTMLInputElement).value).toBe('443')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('keeps a transport failure open for retry without exposing defaults', async () => {
    open(new Error('network unavailable'))
    // The read fails: nothing to edit, and the way back is a retry.
    expect(await screen.findByText(/network unavailable/)).toBeTruthy()
    expect(screen.queryByLabelText(/^Port/)).toBeNull()
    installReads({ '/admin/servers': list([server]), '/admin/nodes': list([node]), '/admin/nodes/1': { node, inbound: plain, clients: [] }, '/admin/certs': { certs: [] } })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(((await screen.findByLabelText(/^Port/)) as HTMLInputElement).value).toBe('443')
  })

  it('accepts empty JSON strings as empty objects', async () => {
    open({ node, inbound: { ...plain, settings: '', stream_settings: '', sniffing: '' }, clients: [] })
    expect(((await screen.findByLabelText(/^Port/)) as HTMLInputElement).value).toBe('443')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('rejects an unknown protocol as unsupported, with no retry and no save', async () => {
    open({ node, inbound: { ...inbound, protocol: 'future-protocol' }, clients: [] })
    expect(await screen.findByText(/not supported/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull()
  })

  it.each(['aes-256-gcm', '2022-future-unsupported'])('does not convert the unsupported Shadowsocks method %s to VLESS', async method => {
    open({ node, inbound: { ...plain, protocol: 'shadowsocks', settings: JSON.stringify({ method, password: 'original-secret' }) }, clients: [] })
    expect(await screen.findByText(/Shadowsocks method/i)).toBeTruthy()
    expect(screen.queryByLabelText(/^Port/)).toBeNull()
  })

  it('says a pending sync is not proof of health, and still lets the saved target be edited', async () => {
    open({ node: { ...node, config_sync_state: 'pending', flow: 'xtls-rprx-vision' }, inbound: plain, inbound_error: 'agent unavailable; saved target returned', clients: [] })
    expect(await screen.findByText(/synchronization has not been confirmed/)).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

describe('Inbound tab: saving', () => {
  it('sends the inbound, and nothing else when nothing but the port changed', async () => {
    const { onDirtyChange } = open({ node, inbound: plain, clients: [] })
    await changePort('8443')
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(true))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(inboundPuts()).toHaveLength(1))
    expect(inboundPuts()[0][1]).toMatchObject({ protocol: 'vless', port: 8443, remark: 'old-name' })
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(api.post).not.toHaveBeenCalled()
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ severity: 'success' })))
  })

  it.each(['2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305'])('keeps the Shadowsocks method %s and its password when saving', async method => {
    open({ node, inbound: { ...plain, protocol: 'shadowsocks', settings: JSON.stringify({ method, password: 'original-secret' }) }, clients: [] })
    await changePort('8444')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(inboundPuts()).toHaveLength(1))
    const saved = inboundPuts()[0][1] as { protocol: string; settings: string }
    expect(saved.protocol).toBe('shadowsocks')
    expect(JSON.parse(saved.settings)).toEqual(expect.objectContaining({ method, password: 'original-secret' }))
  })

  it('persists a changed VLESS flow through the metadata endpoint with the flow only, never sort_order', async () => {
    open({ node: { ...node, sort_order: 30 }, inbound: plain, clients: [] })
    fireEvent.change(await screen.findByLabelText('VLESS Flow'), { target: { value: 'xtls-rprx-vision' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    expect(metadataPuts()[0][1]).toEqual({ flow: 'xtls-rprx-vision' })
    // Same order as the legacy dialog: the inbound first.
    expect(api.put.mock.calls.map(c => c[0])).toEqual(['/admin/nodes/1/inbound', '/admin/nodes/1/metadata'])
  })

  it('says the inbound is saved and which part is not when the flow write fails', async () => {
    open({ node, inbound: plain, clients: [] })
    fireEvent.change(await screen.findByLabelText('VLESS Flow'), { target: { value: 'xtls-rprx-vision' } })
    api.put.mockImplementation(async (url: string) => {
      if (url === '/admin/nodes/1/metadata') throw Object.assign(new Error('x'), { response: { data: { error: 'flow refused' } } })
      return { data: {} }
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/The inbound was saved, but its VLESS flow was not: flow refused/)).toBeTruthy()
    expect(toast).not.toHaveBeenCalledWith(expect.objectContaining({ severity: 'success' }))
  })

  it('binds and unbinds the managed certificate after the inbound, and names it when that fails', async () => {
    open({ node: { ...node, cert_source: 'psp_managed', cert_id: 7 }, inbound, clients: [] })
    await screen.findByLabelText('Certificate source')
    fireEvent.change(screen.getByLabelText('Certificate source'), { target: { value: '' } })
    api.put.mockImplementation(async (url: string) => {
      if (url === '/admin/nodes/1/cert-source') throw Object.assign(new Error('x'), { response: { data: { error: 'cert refused' } } })
      return { data: {} }
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/certificate source was not: cert refused/)).toBeTruthy()
    expect(api.put.mock.calls.map(c => c[0])).toEqual(['/admin/nodes/1/inbound', '/admin/nodes/1/cert-source'])
    expect(api.put.mock.calls[1][1]).toEqual({ source: 'manual', cert_id: 0 })
  })

  it('refuses incomplete REALITY keys before any request and shows why in the bar', async () => {
    open({ node, inbound: reality, clients: [] })
    fireEvent.change(await screen.findByLabelText(/Private Key/), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Generate or fill in Reality privateKey/)).toBeTruthy()
    expect(api.put).not.toHaveBeenCalled()
  })
})

describe('Inbound tab: JSON mode', () => {
  it('switches to the raw documents, sends them as written, and refuses invalid JSON', async () => {
    const { onDirtyChange } = open({ node, inbound: plain, clients: [] })
    fireEvent.click(await screen.findByRole('radio', { name: 'JSON' }))
    // Turning it on snapshots the form into the editors; that is not an edit.
    const settings = await screen.findByLabelText('settings')
    expect((settings as HTMLTextAreaElement).value).toContain('decryption')
    expect(onDirtyChange).not.toHaveBeenCalledWith(true)
    fireEvent.change(settings, { target: { value: '{' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/settings is not valid JSON/)).toBeTruthy()
    expect(api.put).not.toHaveBeenCalled()
    fireEvent.change(settings, { target: { value: '{"decryption":"none","x":1}' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(inboundPuts()).toHaveLength(1))
    expect(inboundPuts()[0][1]).toMatchObject({ settings: '{"decryption":"none","x":1}' })
  })

  it('offers no JSON mode for a server that is not 3X-UI', async () => {
    open({ node, inbound: plain, clients: [] }, { servers: [{ ...server, panel_type: 'sui' }] })
    await screen.findByLabelText(/^Port/)
    expect(screen.queryByRole('radio', { name: 'JSON' })).toBeNull()
  })
})
